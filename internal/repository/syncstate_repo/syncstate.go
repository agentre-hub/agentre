// Package syncstate_repo provides account-sync metadata access for identity
// tables and the per-device CLI overlay projection.
//
// 为什么是一个包而不是分散方法：六列（sync_id / sync_account_id / sync_version /
// sync_updated_at / sync_origin / sync_deleted_at）由 syncmeta_entity.SyncMeta 匿名
// 内嵌进七张表，同名同型——这正是它们能被一套 SQL 覆盖的原因。业务列仍然只由各自
// 域的仓储写；这里只碰同步列，以及「按同步标识找到本机那一行」这一个跨域查询。
//
// 表名一律走 tableOf 的白名单，绝不接受调用方给的任意串。
package syncstate_repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"
	"gorm.io/gorm"

	"github.com/agentre-ai/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-ai/agentre/internal/pkg/syncwire"
)

//go:generate mockgen -source syncstate.go -destination mock_syncstate_repo/mock_syncstate.go

// ErrUnknownKind 表示调用方给了一个不属于同步组的对象类型。
var ErrUnknownKind = errors.New("syncstate: unknown sync kind")

// SyncStateRepo 账号级七张表同步元数据列的访问接口。
type SyncStateRepo interface {
	// FindLocalID 按同步标识取本机自增主键；查不到返回 (0, nil)。
	// 只对有 id 列的对象类型可用——成员关系（project_agents）是联合主键，
	// 它也从不被别的行引用。
	FindLocalID(ctx context.Context, kind, syncID string) (int64, error)
	// FindVersion 按同步标识取本机存着的同步版本号与墓碑标记；查不到返回 found=false。
	FindVersion(ctx context.Context, kind, syncID string) (version int64, deleted bool, found bool, err error)
	// FindRow 按同步标识把整行读进 dest（不过滤 status —— 墓碑行也要读得到）。
	// 查不到返回 false。
	FindRow(ctx context.Context, kind, syncID string, dest any) (bool, error)
	// SaveMeta 按同步标识写回六列同步元数据，不碰任何业务列。
	SaveMeta(ctx context.Context, kind, syncID string, meta syncmeta_entity.SyncMeta) error
	// ClaimUnowned 把本机**尚未属于任何账号**的存活行归入 accountID（R12a），返回
	// 被收进来的那些行供上行入队。没有同步标识的历史行（迁移前就存在、此后一次
	// 都没被 Create/Update 触达过）在这里就地补一个——标识本该在行创建时生成，
	// 它们只是早于本轮。已经属于别的账号的行一行不动（R13a）。
	ClaimUnowned(ctx context.Context, kind string, accountID int64) ([]ClaimedRow, error)
	// ResetVersions 把某个账号名下全部行的同步版本号清零：这些版本号是**上一套
	// server 序列**里的值，server 换了一套（库被重建 / 用户自建）之后它们既比不了
	// 大小，也会把新序列的全量快照整个挡在门外（applyInbound 的版本守卫是
	// 「本机版本 >= 来的版本就不落」，旧序列的 500 永远大过新序列的 3）。
	//
	// 只清版本号：同步标识、所属账号与**墓碑标记**一律不动——清掉墓碑标记等于
	// 让已经删掉的行重新变成一条待上行的新建（R6）。
	ResetVersions(ctx context.Context, kind string, accountID int64) error
	// ListUnversioned 列出某账号名下**还没有版本号**的存活行的同步标识。
	//
	// 版本号只由 server 在受理上行时分配，因此「同步版本号为 0」就是「server 从没
	// 见过这一行」。紧接在一次全量快照之后问它，答案就是「这份快照没送来的行」
	// ——那些行只存在于本机，必须重新上行，否则它们静默留在这台机器上。
	ListUnversioned(ctx context.Context, kind string, accountID int64) ([]string, error)
}

// ClaimedRow 是一次认领里被收进当前账号的一行（R12a）。
type ClaimedRow struct {
	SyncID  string
	Version int64
}

var defaultSyncState SyncStateRepo

// SyncState 取默认仓储单例。
func SyncState() SyncStateRepo { return defaultSyncState }

// RegisterSyncState 注入仓储实现，由 bootstrap 调用一次。
func RegisterSyncState(impl SyncStateRepo) { defaultSyncState = impl }

// NewSyncState 构造默认 GORM 实现。
func NewSyncState() SyncStateRepo { return &syncStateRepo{} }

type syncStateRepo struct{}

// tableOf 把同步组的对象类型映射到本机表名。白名单之外一律报错——表名会被拼进
// SQL，绝不接受调用方给的任意串。
func tableOf(kind string) (string, error) {
	switch kind {
	case syncwire.KindProject:
		return "projects", nil
	case syncwire.KindDepartment:
		return "departments", nil
	case syncwire.KindAgent:
		return "agents", nil
	case syncwire.KindAgentBackend:
		return "agent_backends", nil
	case syncwire.KindAgentExecTarget:
		return "agent_exec_targets", nil
	case syncwire.KindProjectAgent:
		return "project_agents", nil
	case syncwire.KindProjectLocation:
		return "project_locations", nil
	case syncwire.KindLLMProvider:
		return "llm_providers", nil
	case syncwire.KindAgentBackendCLI:
		return "agent_backend_cli_overlays", nil
	}
	return "", fmt.Errorf("%w: %s", ErrUnknownKind, kind)
}

func (r *syncStateRepo) FindLocalID(ctx context.Context, kind, syncID string) (int64, error) {
	table, err := tableOf(kind)
	if err != nil {
		return 0, err
	}
	if kind == syncwire.KindProjectAgent {
		return 0, fmt.Errorf("%w: %s has no auto-increment id", ErrUnknownKind, kind)
	}
	if syncID == "" {
		return 0, nil
	}
	var id int64
	err = db.Ctx(ctx).Table(table).
		Where("sync_id = ?", syncID).
		Select("id").Row().Scan(&id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || isNoRows(err) {
			return 0, nil
		}
		return 0, err
	}
	return id, nil
}

func (r *syncStateRepo) FindVersion(ctx context.Context, kind, syncID string) (int64, bool, bool, error) {
	table, err := tableOf(kind)
	if err != nil {
		return 0, false, false, err
	}
	if syncID == "" {
		return 0, false, false, nil
	}
	var version, deletedAt int64
	err = db.Ctx(ctx).Table(table).
		Where("sync_id = ?", syncID).
		Select("sync_version", "sync_deleted_at").Row().Scan(&version, &deletedAt)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || isNoRows(err) {
			return 0, false, false, nil
		}
		return 0, false, false, err
	}
	return version, deletedAt > 0, true, nil
}

func (r *syncStateRepo) FindRow(ctx context.Context, kind, syncID string, dest any) (bool, error) {
	if _, err := tableOf(kind); err != nil {
		return false, err
	}
	if syncID == "" {
		return false, nil
	}
	err := db.Ctx(ctx).Where("sync_id = ?", syncID).Take(dest).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (r *syncStateRepo) SaveMeta(ctx context.Context, kind, syncID string, meta syncmeta_entity.SyncMeta) error {
	table, err := tableOf(kind)
	if err != nil {
		return err
	}
	if syncID == "" {
		return nil
	}
	return db.Ctx(ctx).Table(table).
		Where("sync_id = ?", syncID).
		Updates(map[string]any{
			"sync_account_id": meta.SyncAccountID,
			"sync_version":    meta.SyncVersion,
			"sync_updated_at": meta.SyncUpdatedAt,
			"sync_origin":     meta.SyncOrigin,
			"sync_deleted_at": meta.SyncDeletedAt,
		}).Error
}

// hasStatusColumn 报告某张账号级表有没有 status 软删列。成员关系与执行目标是硬删，
// 没有这一列——认领时的「存活」判据因此按表分两种。
func hasStatusColumn(kind string) bool {
	switch kind {
	case syncwire.KindProjectAgent, syncwire.KindAgentExecTarget:
		return false
	default:
		return true
	}
}

func (r *syncStateRepo) ClaimUnowned(ctx context.Context, kind string, accountID int64) ([]ClaimedRow, error) {
	table, err := tableOf(kind)
	if err != nil {
		return nil, err
	}
	if accountID == 0 {
		return nil, nil
	}

	// 只认领**存活**的行：本机已经软删的行不该被当成一次新建推上账号（R6）。
	// rowid 是 SQLite 给每张普通表的隐式主键，成员关系那张联合主键表也有——用它
	// 逐行补标识，七张表一套 SQL。
	query := db.Ctx(ctx).Table(table).
		Where("sync_account_id = 0 AND sync_deleted_at = 0")
	if hasStatusColumn(kind) {
		query = query.Where("status = ?", consts.ACTIVE)
	}
	var rows []struct {
		Rowid       int64  `gorm:"column:rowid"`
		SyncID      string `gorm:"column:sync_id"`
		SyncVersion int64  `gorm:"column:sync_version"`
	}
	if err := query.Select("rowid", "sync_id", "sync_version").Scan(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]ClaimedRow, 0, len(rows))
	for _, row := range rows {
		syncID := row.SyncID
		updates := map[string]any{"sync_account_id": accountID}
		if syncID == "" {
			syncID = syncmeta_entity.NewSyncID()
			updates["sync_id"] = syncID
		}
		if err := db.Ctx(ctx).Table(table).
			Where("rowid = ?", row.Rowid).Updates(updates).Error; err != nil {
			return nil, err
		}
		out = append(out, ClaimedRow{SyncID: syncID, Version: row.SyncVersion})
	}
	return out, nil
}

func (r *syncStateRepo) ResetVersions(ctx context.Context, kind string, accountID int64) error {
	table, err := tableOf(kind)
	if err != nil {
		return err
	}
	if accountID == 0 {
		return nil
	}
	// 属于别的账号的行一行不动（R13a），未归属的行也不动——它们本来就没有版本号，
	// 归属由 ClaimUnowned 负责（R12a）。
	return db.Ctx(ctx).Table(table).
		Where("sync_account_id = ?", accountID).
		Update("sync_version", 0).Error
}

func (r *syncStateRepo) ListUnversioned(ctx context.Context, kind string, accountID int64) ([]string, error) {
	table, err := tableOf(kind)
	if err != nil {
		return nil, err
	}
	if accountID == 0 {
		return nil, nil
	}

	// 与 ClaimUnowned 同一套「存活」判据：本机已经软删的行不该被当成一次新建推上
	// 账号（R6），墓碑同理。
	query := db.Ctx(ctx).Table(table).
		Where("sync_account_id = ? AND sync_version = 0 AND sync_deleted_at = 0 AND sync_id != ''", accountID)
	if hasStatusColumn(kind) {
		query = query.Where("status = ?", consts.ACTIVE)
	}
	var rows []struct {
		SyncID string `gorm:"column:sync_id"`
	}
	if err := query.Select("sync_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.SyncID)
	}
	return out, nil
}

// isNoRows 兼容 database/sql 的 sql.ErrNoRows —— Row().Scan 不经过 GORM 的错误翻译。
func isNoRows(err error) bool {
	return err != nil && err.Error() == "sql: no rows in result set"
}

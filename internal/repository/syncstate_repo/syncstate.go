// Package syncstate_repo provides account-sync metadata access for identity
// tables and the per-device CLI overlay projection.
//
// 为什么是一个包而不是分散方法：六列（sync_id / sync_account_id / sync_version /
// sync_updated_at / sync_origin_fingerprint / sync_deleted_at）由 syncmeta_entity.SyncMeta 匿名
// 内嵌进同步组的每张表，同名同型——这正是它们能被一套 SQL 覆盖的原因。业务列仍然
// 只由各自域的仓储写；这里只碰同步列，以及「按同步标识找到本机那一行」这一个跨域
// 查询。
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

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
)

//go:generate mockgen -source syncstate.go -destination mock_syncstate_repo/mock_syncstate.go

// ErrUnknownKind 表示调用方给了一个不属于同步组的对象类型。
var ErrUnknownKind = errors.New("syncstate: unknown sync kind")

// SyncStateRepo 账号级各表同步元数据列的访问接口。
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
	// ClaimForAccount 把本机**不属于 accountID**的存活行归入它，返回被收进来的那些
	// 行供上行入队。两类行都收：
	//
	//   - 尚未属于任何账号的行（R12a）。没有同步标识的历史行（迁移前就存在、此后
	//     一次都没被 Create/Update 触达过）在这里就地补一个——标识本该在行创建时
	//     生成，它们只是早于本轮。
	//   - 属于**别的**账号的行（规格 2026-09-04 决策 1）。这一类的同步版本号一并
	//     清零：那是上一个账号那套序列里的坐标，在这个账号里既不可比、也不能当基
	//     版本用（拿它上行是撒谎，见决策 2），清零后按 R4a 当新建。
	//
	// 两类共用「存活」判据：本机已软删的行与墓碑一律不收——给一个账号推它从没有过
	// 的墓碑会按 R6 永久占掉那个同步标识，那个对象在这个账号里再也建不回来。
	ClaimForAccount(ctx context.Context, kind string, accountID int64) ([]ClaimedRow, error)
	// ResetVersions 把某张表上全部行的同步版本号清零：这些版本号是**上一套
	// (server, 账号) 序列**里的值，换了一套之后它们既比不了大小，也会把新序列的
	// 全量快照整个挡在门外（applyInbound 的版本守卫是「本机版本 >= 来的版本就不
	// 落」，旧序列的 500 永远大过新序列的 3）。
	//
	// **不按账号过滤**（规格 2026-09-04）。换账号时本机的行还盖着**上一个**账号的
	// 版本号，按当前账号过滤等于一行都清不到；而跨账号唯一会撞上的那个固定同步标识
	// （agent_entity.DefaultAgentSyncID）恰恰因此让新账号的那份被守卫挡掉，随后被
	// 上一个账号那份覆盖上去。未归属的行本来就没有版本号，收进来是空操作。
	//
	// 只清版本号：同步标识、所属账号与**墓碑标记**一律不动——清掉墓碑标记等于
	// 让已经删掉的行重新变成一条待上行的新建（R6）。
	//
	// 未登录时不会被调用：唯一的调用方是 sync_svc 的身份 rebase，而那条路径在
	// SyncOnce 里排在取账号之后。
	ResetVersions(ctx context.Context, kind string) error
	// ListUnversioned 列出某账号名下**还没有版本号**的存活行的同步标识。
	//
	// 版本号只由 server 在受理上行时分配，因此「同步版本号为 0」就是「server 从没
	// 见过这一行」。紧接在一次全量快照之后问它，答案就是「这份快照没送来的行」
	// ——那些行只存在于本机，必须重新上行，否则它们静默留在这台机器上。
	ListUnversioned(ctx context.Context, kind string, accountID int64) ([]string, error)
	// ListUnsyncedTombstones 列出某账号名下**本机已经软删、但这条删除从没送达过
	// server** 的行（同步标识 + 它当前的版本号，供上行当基版本）。
	//
	// 这是「删除必须到达各端」（R6）在本机这一侧的兜底。会落进这个集合的行有两
	// 类：登出期间删掉的（那一刻没有账号可入队），以及历史上任何一次入队没发生
	// 的删除。它们此后不被别的取数看见——ClaimForAccount 与 ListUnversioned 都只收
	// **存活**的行——server 那份于是一直活着。
	//
	// 三个条件缺一不可：归属这个账号；sync_version != 0（为 0 = server 从没见过
	// 这一行，没有什么要删的，凭空推一条墓碑只会占掉那个标识）；sync_deleted_at
	// 仍为 0（非 0 = 那条墓碑已经送达过，见 applyPushResult 与 saveInboundMeta）。
	//
	// 硬删的那几类（成员关系 / 执行目标 / issue 标签）返回空集：行已经不在了，
	// 「软删但没上行」这个状态在它们身上根本不存在，删除靠父行的级联入队送达。
	ListUnsyncedTombstones(ctx context.Context, kind string, accountID int64) ([]ClaimedRow, error)
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
	case syncwire.KindLabel:
		return "labels", nil
	case syncwire.KindIssue:
		return "issues", nil
	case syncwire.KindIssueLabel:
		return "issue_labels", nil
	}
	return "", fmt.Errorf("%w: %s", ErrUnknownKind, kind)
}

func (r *syncStateRepo) FindLocalID(ctx context.Context, kind, syncID string) (int64, error) {
	table, err := tableOf(kind)
	if err != nil {
		return 0, err
	}
	if kind == syncwire.KindProjectAgent || kind == syncwire.KindIssueLabel {
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
			"sync_account_id":         meta.SyncAccountID,
			"sync_version":            meta.SyncVersion,
			"sync_updated_at":         meta.SyncUpdatedAt,
			"sync_origin_fingerprint": meta.SyncOriginFingerprint,
			"sync_deleted_at":         meta.SyncDeletedAt,
		}).Error
}

// hasStatusColumn 报告某张账号级表有没有 status 软删列。成员关系、执行目标与任务
// ↔ 标签关联是硬删，没有这一列——认领时的「存活」判据因此按表分两种。
func hasStatusColumn(kind string) bool {
	switch kind {
	case syncwire.KindProjectAgent, syncwire.KindAgentExecTarget, syncwire.KindIssueLabel:
		return false
	default:
		return true
	}
}

func (r *syncStateRepo) ClaimForAccount(ctx context.Context, kind string, accountID int64) ([]ClaimedRow, error) {
	table, err := tableOf(kind)
	if err != nil {
		return nil, err
	}
	if accountID == 0 {
		return nil, nil
	}

	// 只认领**存活**的行：本机已经软删的行不该被当成一次新建推上账号（R6）。
	// rowid 是 SQLite 给每张普通表的隐式主键，成员关系那张联合主键表也有——用它
	// 逐行补标识与清版本号，每张表一套 SQL。
	//
	// sync_account_id 一并取出来：它决定这一行是「还没上过云」还是「属于上一个
	// 账号」，而后者的版本号必须清零（见接口注释）。
	query := db.Ctx(ctx).Table(table).
		Where("(sync_account_id = 0 OR sync_account_id <> ?) AND sync_deleted_at = 0", accountID)
	if hasStatusColumn(kind) {
		query = query.Where("status = ?", consts.ACTIVE)
	}
	var rows []struct {
		Rowid         int64  `gorm:"column:rowid"`
		SyncID        string `gorm:"column:sync_id"`
		SyncVersion   int64  `gorm:"column:sync_version"`
		SyncAccountID int64  `gorm:"column:sync_account_id"`
	}
	if err := query.Select("rowid", "sync_id", "sync_version", "sync_account_id").Scan(&rows).Error; err != nil {
		return nil, err
	}

	if len(rows) == 0 {
		return nil, nil
	}

	// 逐行的 UPDATE 全在**同一个事务**里：认领是「改归属」与「入队上行」两件事的
	// 第一半，而调用方只凭本次的返回值入队（claimForCurrentAccount）。中途失败却让
	// 前几行的归属留在库里，那几行此后一次取数都碰不到——下一轮 ClaimForAccount 按
	// 归属过滤已经收不到它们，ListUnversioned 只在 rebase 那条路径上被问——于是它们
	// 静默地再也不上行。一起回滚，重跑才真的是幂等的。
	out := make([]ClaimedRow, 0, len(rows))
	if err := db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		for _, row := range rows {
			syncID := row.SyncID
			version := row.SyncVersion
			updates := map[string]any{"sync_account_id": accountID}
			if syncID == "" {
				syncID = syncmeta_entity.NewSyncID()
				updates["sync_id"] = syncID
			}
			if row.SyncAccountID != 0 {
				// 上一个账号那套序列里的坐标：清零，并按 0 交回去当基版本（R4a 新建）。
				updates["sync_version"] = 0
				version = 0
			}
			if err := tx.Table(table).
				Where("rowid = ?", row.Rowid).Updates(updates).Error; err != nil {
				return err
			}
			out = append(out, ClaimedRow{SyncID: syncID, Version: version})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *syncStateRepo) ResetVersions(ctx context.Context, kind string) error {
	table, err := tableOf(kind)
	if err != nil {
		return err
	}
	// 判据是「这一行有版本号」而不是「这一行属于谁」：换账号时要清的恰恰是**别的**
	// 账号那些（见接口注释）。它同时替掉了 gorm 对无条件全表更新的拦阻。
	//
	// 但只清**存活**的行（与 ClaimForAccount 同一套判据）。软删行上的版本号不是一个
	// 待重建的坐标，而是「server 见过这一行」这条事实本身——ListUnsyncedTombstones
	// 正是靠 sync_version != 0 找出「本机已删、这条删除却从没送达」的那些行（R6 的
	// 本机兜底）。把它一并清掉，等于在换账号时静默擦掉上一个账号欠着的每一条删除，
	// 登回那个账号时再也没有任何取数会看见它们，server 上于是永远留着用户删过的对象。
	query := db.Ctx(ctx).Table(table).
		Where("sync_version <> ? AND sync_deleted_at = 0", 0)
	if hasStatusColumn(kind) {
		query = query.Where("status = ?", consts.ACTIVE)
	}
	return query.Update("sync_version", 0).Error
}

func (r *syncStateRepo) ListUnversioned(ctx context.Context, kind string, accountID int64) ([]string, error) {
	table, err := tableOf(kind)
	if err != nil {
		return nil, err
	}
	if accountID == 0 {
		return nil, nil
	}

	// 与 ClaimForAccount 同一套「存活」判据：本机已经软删的行不该被当成一次新建推上
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

func (r *syncStateRepo) ListUnsyncedTombstones(
	ctx context.Context, kind string, accountID int64,
) ([]ClaimedRow, error) {
	table, err := tableOf(kind)
	if err != nil {
		return nil, err
	}
	if accountID == 0 || !hasStatusColumn(kind) {
		return nil, nil
	}

	// 逐列指名扫描：ClaimedRow.Version 对应的列是 sync_version，而 GORM 默认按字段名
	// 推列名（Version → version），不指名就每一行都扫回 0，基版本随之全变成「新建」。
	var rows []struct {
		SyncID      string `gorm:"column:sync_id"`
		SyncVersion int64  `gorm:"column:sync_version"`
	}
	if err := db.Ctx(ctx).Table(table).
		Where("sync_account_id = ? AND sync_version != 0 AND sync_deleted_at = 0 AND sync_id != ''", accountID).
		Where("status = ?", consts.DELETE).
		Select("sync_id", "sync_version").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]ClaimedRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, ClaimedRow{SyncID: row.SyncID, Version: row.SyncVersion})
	}
	return out, nil
}

// Package agent_backend_repo 提供 Agent 后端的持久化访问。
package agent_backend_repo

import (
	"context"
	"errors"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/repository/repoquery"
)

//go:generate mockgen -source agent_backend.go -destination mock_agent_backend_repo/mock_agent_backend.go

// AgentBackendRepo Agent 后端仓储。
type AgentBackendRepo interface {
	Create(ctx context.Context, b *agent_backend_entity.AgentBackend) error
	Update(ctx context.Context, b *agent_backend_entity.AgentBackend) error
	Find(ctx context.Context, id int64) (*agent_backend_entity.AgentBackend, error)
	BatchFind(ctx context.Context, ids []int64) (map[int64]*agent_backend_entity.AgentBackend, error)
	FindByName(ctx context.Context, name string) (*agent_backend_entity.AgentBackend, error)
	// ExistsByName 判断是否存在同名行，**不看 status**——决策 25:扫描创建的撞名
	// 判据必须把墓碑一并计入，否则「扫描建 → 被删 → 再扫描建」每轮都会留一条新
	// 墓碑（Problem 19），把决策 24 的回收持续抵消掉。FindByName 只看 ACTIVE 行，
	// 服务于重命名 / 手动创建那类校验，语义不变；ExistsByName 是专给扫描创建路径
	// 用的更宽判据。
	ExistsByName(ctx context.Context, name string) (bool, error)
	// ListByDevice 列出指向同一台设备（canonical fingerprint）的全部启用 backend。
	// 一台机器可以有多档（Claude Code / Codex / Pi Agent 各一档，R14「自己」是一组
	// 而不是一个）；R14 顺序解析用它识别「本机」那几档。
	ListByDevice(ctx context.Context, deviceID string) ([]*agent_backend_entity.AgentBackend, error)
	List(ctx context.Context) ([]*agent_backend_entity.AgentBackend, error)
	Delete(ctx context.Context, id int64) error
	ListCLIOverlays(ctx context.Context) ([]*agent_backend_entity.CLIOverlay, error)
	FindCLIOverlay(ctx context.Context, backendSyncID, fingerprint string) (*agent_backend_entity.CLIOverlay, error)
	CreateCLIOverlay(ctx context.Context, overlay *agent_backend_entity.CLIOverlay) error
	UpdateCLIOverlay(ctx context.Context, overlay *agent_backend_entity.CLIOverlay) error
	DeleteCLIOverlay(ctx context.Context, id int64) error
	ClaimRelative(ctx context.Context, fingerprint string) ([]RelativeClaim, error)
	// ListTombstonesOlderThan 列出「墓碑且 updatetime 早于 cutoff(ms epoch)」的行——
	// 决策 24 回收判据的前一半(墓碑 AND 超过保留期)。另一半(无任何引用)由调用方
	// 结合 ListExecTargetBackendRefs / chat_repo.SessionRepo.ListExecAgentBackendRefs
	// 过滤，仓储本身不知道"引用"这个跨领域概念。
	ListTombstonesOlderThan(ctx context.Context, cutoff int64) ([]*agent_backend_entity.AgentBackend, error)
	// ListExecTargetBackendRefs 列出全部 Agent 执行目标对后端的引用，不限后端自身
	// 状态——决策 24 的回收判据与悬空引用巡检都要看"这个 backend id 有没有被任何
	// 执行目标提到过"。
	ListExecTargetBackendRefs(ctx context.Context) ([]ExecTargetBackendRef, error)
	// PurgeTombstones 物理删除给定 id 的墓碑行(决策 24:回收 = 硬删除，不是再软删
	// 一次)。防御性地只删 status = DELETE 的行，调用方传入的 id 集合即使算错也
	// 波及不到 ACTIVE 行。
	PurgeTombstones(ctx context.Context, ids []int64) (int64, error)
}

// ExecTargetBackendRef 是 agent_exec_targets 里一条「执行目标 → 引用的后端」记录。
type ExecTargetBackendRef struct {
	ExecTargetID   int64 `gorm:"column:id"`
	AgentBackendID int64 `gorm:"column:agent_backend_id"`
}

// RelativeClaim is the locally atomic replacement of one legacy relative
// backend and every execution target that references it.
type RelativeClaim struct {
	OriginalBackend *agent_backend_entity.AgentBackend
	ClaimedBackend  *agent_backend_entity.AgentBackend
	OriginalTargets []*agent_entity.AgentExecTarget
	ClaimedTargets  []*agent_entity.AgentExecTarget
}

var defaultAgentBackend AgentBackendRepo

// AgentBackend 取默认仓储单例。
func AgentBackend() AgentBackendRepo { return defaultAgentBackend }

// RegisterAgentBackend 注入仓储实现，由 bootstrap 调用一次。
func RegisterAgentBackend(impl AgentBackendRepo) { defaultAgentBackend = impl }

type agentBackendRepo struct{}

// NewAgentBackend 构造默认 GORM 实现。
func NewAgentBackend() AgentBackendRepo { return &agentBackendRepo{} }

func (r *agentBackendRepo) Create(ctx context.Context, b *agent_backend_entity.AgentBackend) error {
	// 同步标识在行创建时就地生成，未登录期间也照常写入（R1/R12a）。
	b.EnsureSyncID()
	return db.Ctx(ctx).Create(b).Error
}

func (r *agentBackendRepo) Update(ctx context.Context, b *agent_backend_entity.AgentBackend) error {
	// 迁移前已存在、还没有标识的历史行在下一次落库时补齐（JIT），已有标识的行
	// 原样保留（R1：终身不变）。
	b.EnsureSyncID()
	return db.Ctx(ctx).Save(b).Error
}

func (r *agentBackendRepo) Find(ctx context.Context, id int64) (*agent_backend_entity.AgentBackend, error) {
	out := &agent_backend_entity.AgentBackend{}
	err := db.Ctx(ctx).Where("id = ? AND status = ?", id, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *agentBackendRepo) BatchFind(ctx context.Context, ids []int64) (map[int64]*agent_backend_entity.AgentBackend, error) {
	return repoquery.ActiveMap[agent_backend_entity.AgentBackend](ctx, "id", ids, func(b *agent_backend_entity.AgentBackend) int64 {
		return b.ID
	})
}

func (r *agentBackendRepo) FindByName(ctx context.Context, name string) (*agent_backend_entity.AgentBackend, error) {
	out := &agent_backend_entity.AgentBackend{}
	err := db.Ctx(ctx).Where("name = ? AND status = ?", name, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *agentBackendRepo) ExistsByName(ctx context.Context, name string) (bool, error) {
	var count int64
	err := db.Ctx(ctx).Model(&agent_backend_entity.AgentBackend{}).Where("name = ?", name).Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *agentBackendRepo) ListByDevice(ctx context.Context, deviceID string) ([]*agent_backend_entity.AgentBackend, error) {
	var rows []*agent_backend_entity.AgentBackend
	err := db.Ctx(ctx).
		Where("device_fingerprint = ? AND status = ?", deviceID, consts.ACTIVE).
		Order("id ASC").
		Find(&rows).Error
	return rows, err
}

func (r *agentBackendRepo) List(ctx context.Context) ([]*agent_backend_entity.AgentBackend, error) {
	var rows []*agent_backend_entity.AgentBackend
	if err := db.Ctx(ctx).Where("status = ?", consts.ACTIVE).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *agentBackendRepo) Delete(ctx context.Context, id int64) error {
	// updatetime 跟着软删一起写:决策 24 的墓碑回收按"距被软删多久"判定保留期，
	// 不落这个时间戳，回收就无从分辨一条墓碑是刚产生的还是躺了很久——软删前最后
	// 一次合法更新的旧 updatetime 不能代表"何时被软删"。
	return db.Ctx(ctx).Model(&agent_backend_entity.AgentBackend{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     consts.DELETE,
			"updatetime": time.Now().UnixMilli(),
		}).Error
}

func (r *agentBackendRepo) ListTombstonesOlderThan(ctx context.Context, cutoff int64) ([]*agent_backend_entity.AgentBackend, error) {
	var rows []*agent_backend_entity.AgentBackend
	err := db.Ctx(ctx).Where("status = ? AND updatetime < ?", consts.DELETE, cutoff).
		Order("id ASC").Find(&rows).Error
	return rows, err
}

func (r *agentBackendRepo) ListExecTargetBackendRefs(ctx context.Context) ([]ExecTargetBackendRef, error) {
	var refs []ExecTargetBackendRef
	err := db.Ctx(ctx).Model(&agent_entity.AgentExecTarget{}).
		Select("id, agent_backend_id").
		Where("agent_backend_id > ?", 0).
		Find(&refs).Error
	return refs, err
}

func (r *agentBackendRepo) PurgeTombstones(ctx context.Context, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	res := db.Ctx(ctx).Where("id IN ? AND status = ?", ids, consts.DELETE).
		Delete(&agent_backend_entity.AgentBackend{})
	return res.RowsAffected, res.Error
}

func (r *agentBackendRepo) ListCLIOverlays(ctx context.Context) ([]*agent_backend_entity.CLIOverlay, error) {
	var rows []*agent_backend_entity.CLIOverlay
	err := db.Ctx(ctx).Where("status = ?", consts.ACTIVE).Order("id ASC").Find(&rows).Error
	return rows, err
}

func (r *agentBackendRepo) FindCLIOverlay(ctx context.Context, backendSyncID, fingerprint string) (*agent_backend_entity.CLIOverlay, error) {
	out := &agent_backend_entity.CLIOverlay{}
	err := db.Ctx(ctx).Where("backend_sync_id = ? AND agentred_fingerprint = ? AND status = ?", backendSyncID, fingerprint, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *agentBackendRepo) CreateCLIOverlay(ctx context.Context, overlay *agent_backend_entity.CLIOverlay) error {
	overlay.EnsureSyncID()
	return db.Ctx(ctx).Create(overlay).Error
}

func (r *agentBackendRepo) UpdateCLIOverlay(ctx context.Context, overlay *agent_backend_entity.CLIOverlay) error {
	overlay.EnsureSyncID()
	return db.Ctx(ctx).Save(overlay).Error
}

func (r *agentBackendRepo) DeleteCLIOverlay(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Model(&agent_backend_entity.CLIOverlay{}).Where("id = ?", id).Update("status", consts.DELETE).Error
}

// ClaimRelative clones every still-relative backend for fingerprint, fans out
// its execution targets, and tombstones the old rows in one transaction. Only
// DeviceID == "" is eligible: another desktop's already named clone can never
// be claimed again.
func (r *agentBackendRepo) ClaimRelative(ctx context.Context, fingerprint string) ([]RelativeClaim, error) {
	if fingerprint == "" {
		return nil, nil
	}
	claims := make([]RelativeClaim, 0)
	err := db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		var originals []*agent_backend_entity.AgentBackend
		if err := tx.Where("device_fingerprint = ? AND status = ?", "", consts.ACTIVE).Order("id ASC").Find(&originals).Error; err != nil {
			return err
		}
		for _, original := range originals {
			var originalTargets []*agent_entity.AgentExecTarget
			if err := tx.Where("agent_backend_id = ?", original.ID).
				Order("agent_id ASC, sort_order ASC, id ASC").Find(&originalTargets).Error; err != nil {
				return err
			}

			claimed := *original
			claimed.ID = 0
			claimed.DeviceFingerprint = fingerprint
			claimed.SyncMeta = syncmeta_entity.SyncMeta{SyncAccountID: original.SyncAccountID}
			claimed.EnsureSyncID()
			if err := tx.Create(&claimed).Error; err != nil {
				return err
			}

			// The unique (agent_id, sort_order) index includes the old target.
			// Vacate those slots before inserting their replacements, as
			// replaceExecTargets does for a changed target list.
			for _, target := range originalTargets {
				if err := tx.Where("id = ?", target.ID).Delete(&agent_entity.AgentExecTarget{}).Error; err != nil {
					return err
				}
			}

			claimedTargets := make([]*agent_entity.AgentExecTarget, 0, len(originalTargets))
			for _, target := range originalTargets {
				copyTarget := *target
				copyTarget.ID = 0
				copyTarget.AgentBackendID = claimed.ID
				copyTarget.SyncMeta = syncmeta_entity.SyncMeta{SyncAccountID: target.SyncAccountID}
				copyTarget.EnsureSyncID()
				if err := tx.Create(&copyTarget).Error; err != nil {
					return err
				}
				claimedTargets = append(claimedTargets, &copyTarget)
			}
			if err := tx.Model(&agent_backend_entity.AgentBackend{}).Where("id = ?", original.ID).
				Update("status", consts.DELETE).Error; err != nil {
				return err
			}
			claims = append(claims, RelativeClaim{
				OriginalBackend: original, ClaimedBackend: &claimed,
				OriginalTargets: originalTargets, ClaimedTargets: claimedTargets,
			})
		}
		return nil
	})
	return claims, err
}

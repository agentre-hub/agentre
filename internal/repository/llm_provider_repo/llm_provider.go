// Package llm_provider_repo 提供 LLM 供应商及其稳定模型的持久化访问。
package llm_provider_repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/repository/repoquery"
)

//go:generate mockgen -source llm_provider.go -destination mock_llm_provider_repo/mock_llm_provider.go

// RefCounts 引用影响计数(Backend / Session / Route 三路),Provider 与 Model 共用同一形状。
type RefCounts struct {
	// Backends 主绑定到该 key 的 agent_backends 数。
	Backends int64
	// Sessions 会话级钉住该 key 的 chat_sessions 数。
	Sessions int64
	// Routes 其 config_json.modelRoutes 结构化 target 引用了该 key 的 agent_backends 数。
	Routes int64
}

// ProviderRefCounts 一个 Provider 的引用影响计数(Backend / Session / Route 三路)。
// 用于「改默认模型」或「删除 Provider」前的引用保护确认。
type ProviderRefCounts = RefCounts

// ModelRefCounts 一个 Model(model_key)的引用影响计数。
// 用于「编辑被引用 Model 的 model_id」或「删除 Model」前的引用保护确认。
type ModelRefCounts = RefCounts

// LLMProviderRepo LLM 供应商 + 模型仓储。单一口子覆盖 Provider CRUD、Model
// CRUD/list/find、原子 create/import/default 变更与 Backend/Session/Route
// 引用影响计数。ModelKey 不可变；展示读取不暴露明文 API Key（由实体 MaskedAPIKey
// 承担脱敏边界）。
type LLMProviderRepo interface {
	// ── Provider CRUD ──
	Update(ctx context.Context, p *llm_provider_entity.LLMProvider) error
	// UpdateReach 只写「谁能连到」两列。它们不走 Update / UpsertFromSync：整行回写一份读得早
	// 或同步下行出来的实体，会把它们拍回旧值 / 默认。
	UpdateReach(ctx context.Context, id int64, mode, devicesJSON string) error
	Find(ctx context.Context, id int64) (*llm_provider_entity.LLMProvider, error)
	FindByKey(ctx context.Context, key string) (*llm_provider_entity.LLMProvider, error)
	BatchFindByKey(ctx context.Context, keys []string) (map[string]*llm_provider_entity.LLMProvider, error)
	// ListByKeysAnyStatus 是 FindByKey 的批量形态，同样**不过滤 status**：软删的 provider
	// 仍按 provider_key 返回，供展示侧保留名字、判定侧给出 ProviderInactive（spec 决策 7）。
	// 与只取 ACTIVE 的 BatchFindByKey 刻意不同口径。空 keys 不发 SQL。
	ListByKeysAnyStatus(ctx context.Context, keys []string) (map[string]*llm_provider_entity.LLMProvider, error)
	FindByName(ctx context.Context, name string) (*llm_provider_entity.LLMProvider, error)
	List(ctx context.Context) ([]*llm_provider_entity.LLMProvider, error)
	// DeleteWithModels 在同一事务内软删除一个 Provider 及其全部 Models（spec 决策 17：
	// 无引用 Provider 的删除事务同时移除其 Models，任一步失败整体回滚，不留半批）。
	DeleteWithModels(ctx context.Context, id int64) error
	// UpsertFromSync replaces one provider's account payload and nested model
	// catalog atomically. provider_key is its sync identity; missing local models
	// are soft-deleted rather than left as stale entries.
	UpsertFromSync(ctx context.Context, p *llm_provider_entity.LLMProvider, models []*llm_provider_model_entity.LLMProviderModel) error

	// ── Provider + Models 原子操作（成功或失败，不留半批） ──
	// CreateWithModels 在同一事务内写 Provider、其全部 Models 并落 default_model_key。
	CreateWithModels(ctx context.Context, p *llm_provider_entity.LLMProvider, models []*llm_provider_model_entity.LLMProviderModel, defaultModelKey string) error
	// ImportModels 原子落地一组导入结果：updates 为已存在行的 only-fill-empty 补齐（保留
	// 稳定 model_key），inserts 为新模型行；在同一事务内执行，任一步失败整体回滚，不留半批。
	ImportModels(ctx context.Context, updates, inserts []*llm_provider_model_entity.LLMProviderModel) error

	// ── Model CRUD ──
	CreateModel(ctx context.Context, m *llm_provider_model_entity.LLMProviderModel) error
	// UpdateModel 更新模型可编辑字段；model_key 不可变，绝不进入 UPDATE。
	UpdateModel(ctx context.Context, m *llm_provider_model_entity.LLMProviderModel) error
	FindModel(ctx context.Context, id int64) (*llm_provider_model_entity.LLMProviderModel, error)
	// FindModelByKey 按稳定 model_key 查（含 enabled=0 的停用模型，供 fixed-model 失效提示）。
	FindModelByKey(ctx context.Context, modelKey string) (*llm_provider_model_entity.LLMProviderModel, error)
	// BatchFindModelsByKey 是 FindModelByKey 的批量形态，同口径只取 ACTIVE（含 enabled=0），
	// 按 model_key 索引。空 keys 不发 SQL。
	BatchFindModelsByKey(ctx context.Context, keys []string) (map[string]*llm_provider_model_entity.LLMProviderModel, error)
	ListModels(ctx context.Context, providerID int64) ([]*llm_provider_model_entity.LLMProviderModel, error)
	// CountModelsByProvider 按 provider 分组统计 ACTIVE 模型数（status=ACTIVE，不含软删除），
	// 只查给定 providerIDs；空列表直接返回空 map，不发 SQL。
	CountModelsByProvider(ctx context.Context, providerIDs []int64) (map[int64]int64, error)
	DeleteModel(ctx context.Context, id int64) error

	// ── 引用影响计数 ──
	CountProviderReferences(ctx context.Context, providerKey string) (ProviderRefCounts, error)
	CountModelReferences(ctx context.Context, modelKey string) (ModelRefCounts, error)
}

var defaultLLMProvider LLMProviderRepo

// LLMProvider 取默认仓储单例。
func LLMProvider() LLMProviderRepo { return defaultLLMProvider }

// RegisterLLMProvider 注入仓储实现，由 bootstrap 调用一次。
func RegisterLLMProvider(impl LLMProviderRepo) { defaultLLMProvider = impl }

// ErrKeychainUnavailable 表示系统钥匙串读写失败：key 不会退回落进 SQLite 明文列。
var ErrKeychainUnavailable = errors.New("llm_provider_repo: system keychain unavailable")

type llmProviderRepo struct{ kc keychain.Keychain }

// store 取钥匙串；没注册任何实现时为 nil。
func (r *llmProviderRepo) store() keychain.Keychain {
	if r.kc != nil {
		return r.kc
	}
	return keychain.Default()
}

// saveKey 把 key 写进钥匙串（空 key = 删槽位）。钥匙串不可用就报
// ErrKeychainUnavailable，调用方据此中止，绝不退回把明文写进 SQLite 列。
func (r *llmProviderRepo) saveKey(providerKey, apiKey string) error {
	account := keychain.LLMProviderAPIKeyAccount(providerKey)
	if account == "" {
		return nil
	}
	kc := r.store()
	if kc == nil {
		if apiKey == "" {
			return nil // 没有钥匙串也就没有要清的槽位
		}
		return ErrKeychainUnavailable
	}
	var err error
	if apiKey == "" {
		err = kc.Delete(account)
	} else {
		err = kc.Set(account, apiKey)
	}
	if err != nil && !errors.Is(err, keychain.ErrNotFound) {
		return fmt.Errorf("%w: %v", ErrKeychainUnavailable, err)
	}
	return nil
}

// hydrate 把钥匙串里的 key 回填到实体（SQLite 列恒为空）。槽位里没有就保持列值不动
// （尚未被迁移搬走的旧明文行仍可用）。读路径所有出口都经过它，调用方不感知 key 的住处。
func (r *llmProviderRepo) hydrate(p *llm_provider_entity.LLMProvider) error {
	if p == nil {
		return nil
	}
	account := keychain.LLMProviderAPIKeyAccount(p.ProviderKey)
	kc := r.store()
	if account == "" || kc == nil {
		return nil
	}
	v, err := kc.Get(account)
	switch {
	case err == nil:
		p.APIKey = v
	case !errors.Is(err, keychain.ErrNotFound):
		return fmt.Errorf("%w: %v", ErrKeychainUnavailable, err)
	}
	return nil
}

func (r *llmProviderRepo) hydrateAll(rows []*llm_provider_entity.LLMProvider) error {
	for _, p := range rows {
		if err := r.hydrate(p); err != nil {
			return err
		}
	}
	return nil
}

// hydrateMap 回填按 provider_key 索引的 map。
func (r *llmProviderRepo) hydrateMap(m map[string]*llm_provider_entity.LLMProvider) error {
	for _, p := range m {
		if err := r.hydrate(p); err != nil {
			return err
		}
	}
	return nil
}

// NewLLMProvider 构造默认 GORM 实现，key 存取走 keychain.Default()（调用时才取）。
func NewLLMProvider() LLMProviderRepo { return &llmProviderRepo{} }

// NewLLMProviderWithKeychain 构造把 key 放进指定钥匙串的实现。
func NewLLMProviderWithKeychain(kc keychain.Keychain) LLMProviderRepo {
	return &llmProviderRepo{kc: kc}
}

func (r *llmProviderRepo) Update(ctx context.Context, p *llm_provider_entity.LLMProvider) error {
	p.SyncID = p.ProviderKey
	if err := r.saveKey(p.ProviderKey, p.APIKey); err != nil {
		return err
	}
	return db.Ctx(ctx).Omit(append([]string{"api_key"}, reachColumns...)...).Save(p).Error
}

// reachColumns 是只由 UpdateReach 写的列。
var reachColumns = []string{"reach_mode", "reach_devices_json"}

func (r *llmProviderRepo) UpdateReach(ctx context.Context, id int64, mode, devicesJSON string) error {
	return db.Ctx(ctx).Model(&llm_provider_entity.LLMProvider{}).Where("id = ?", id).Updates(map[string]any{
		"reach_mode": mode, "reach_devices_json": devicesJSON, "updatetime": time.Now().UnixMilli(),
	}).Error
}

func (r *llmProviderRepo) Find(ctx context.Context, id int64) (*llm_provider_entity.LLMProvider, error) {
	out := &llm_provider_entity.LLMProvider{}
	err := db.Ctx(ctx).Where("id = ? AND status = ?", id, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := r.hydrate(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *llmProviderRepo) BatchFindByKey(ctx context.Context, keys []string) (map[string]*llm_provider_entity.LLMProvider, error) {
	out, err := repoquery.ActiveMap[llm_provider_entity.LLMProvider](ctx, "provider_key", keys, func(p *llm_provider_entity.LLMProvider) string {
		return p.ProviderKey
	})
	if err != nil {
		return nil, err
	}
	if err := r.hydrateMap(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *llmProviderRepo) FindByName(ctx context.Context, name string) (*llm_provider_entity.LLMProvider, error) {
	out := &llm_provider_entity.LLMProvider{}
	err := db.Ctx(ctx).Where("name = ? AND status = ?", name, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := r.hydrate(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *llmProviderRepo) FindByKey(ctx context.Context, key string) (*llm_provider_entity.LLMProvider, error) {
	out := &llm_provider_entity.LLMProvider{}
	err := db.Ctx(ctx).Where("provider_key = ?", key).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := r.hydrate(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *llmProviderRepo) ListByKeysAnyStatus(ctx context.Context, keys []string) (map[string]*llm_provider_entity.LLMProvider, error) {
	out := make(map[string]*llm_provider_entity.LLMProvider, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	var rows []*llm_provider_entity.LLMProvider
	if err := db.Ctx(ctx).Where("provider_key IN ?", keys).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ProviderKey] = row
	}
	if err := r.hydrateMap(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *llmProviderRepo) List(ctx context.Context) ([]*llm_provider_entity.LLMProvider, error) {
	var rows []*llm_provider_entity.LLMProvider
	if err := db.Ctx(ctx).Where("status = ?", consts.ACTIVE).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	if err := r.hydrateAll(rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// DeleteWithModels 在同一事务内软删除 Provider 及其全部 Models（spec 决策 17）。
func (r *llmProviderRepo) DeleteWithModels(ctx context.Context, id int64) error {
	var keys []string
	if err := db.Ctx(ctx).Model(&llm_provider_entity.LLMProvider{}).Where("id = ?", id).Pluck("provider_key", &keys).Error; err != nil {
		return err
	}
	if err := r.deleteWithModelsTx(ctx, id); err != nil {
		return err
	}
	// 事务成功后清钥匙串槽位：尽力而为，槽位残留不影响正确性（行已软删，没人再按这个 key 取）。
	for _, key := range keys {
		_ = r.saveKey(key, "")
	}
	return nil
}

func (r *llmProviderRepo) deleteWithModelsTx(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		ctx = db.WithContextDB(ctx, tx)
		if err := db.Ctx(ctx).Model(&llm_provider_entity.LLMProvider{}).
			Where("id = ?", id).
			Update("status", consts.DELETE).Error; err != nil {
			return err
		}
		return db.Ctx(ctx).Model(&llm_provider_model_entity.LLMProviderModel{}).
			Where("provider_id = ?", id).
			Update("status", consts.DELETE).Error
	})
}

// CreateWithModels 事务内原子写入 Provider + Models + 默认模型。
func (r *llmProviderRepo) CreateWithModels(ctx context.Context, p *llm_provider_entity.LLMProvider, models []*llm_provider_model_entity.LLMProviderModel, defaultModelKey string) error {
	p.SyncID = p.ProviderKey
	if err := r.saveKey(p.ProviderKey, p.APIKey); err != nil {
		return err
	}
	err := db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		ctx = db.WithContextDB(ctx, tx)
		if err := db.Ctx(ctx).Omit("api_key").Create(p).Error; err != nil {
			return err
		}
		for _, m := range models {
			m.ProviderID = p.ID
			if err := db.Ctx(ctx).Create(m).Error; err != nil {
				return err
			}
		}
		return db.Ctx(ctx).Model(&llm_provider_entity.LLMProvider{}).
			Where("id = ?", p.ID).
			Update("default_model_key", defaultModelKey).Error
	})
	if err != nil {
		// 没落库就别留下无主的 key。
		_ = r.saveKey(p.ProviderKey, "")
	}
	return err
}

// UpsertFromSync applies the complete nested provider payload as one transaction.
// Model keys are stable and global, so existing rows are updated in place; any
// active local model not present in the authoritative payload becomes deleted.
//
// The adapter hands in a freshly built row (payload fields only, sync-blind by
// design — it doesn't read the local row first). This is therefore the one seam
// that knows both the incoming payload and the local row it lands on, so it owns
// stamping createtime/updatetime and carrying over the six sync_* columns:
// an existing row keeps its createtime and sync metadata (including sync_version,
// which the lost-change restore path relies on as the next upload's base
// version) and only gets a fresh updatetime; a brand-new row gets both stamps
// set to this landing time and starts with zero sync metadata (Problem 4).
func (r *llmProviderRepo) UpsertFromSync(ctx context.Context, p *llm_provider_entity.LLMProvider, models []*llm_provider_model_entity.LLMProviderModel) error {
	p.SyncID = p.ProviderKey
	// 下行收到的 key 只进钥匙串，SQLite 列恒为空。
	if err := r.saveKey(p.ProviderKey, p.APIKey); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		var existing llm_provider_entity.LLMProvider
		err := tx.Where("provider_key = ?", p.ProviderKey).First(&existing).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			p.Createtime, p.Updatetime = now, now
			if err := tx.Omit("api_key").Create(p).Error; err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			p.ID = existing.ID
			p.Createtime = existing.Createtime
			p.Updatetime = now
			p.SyncMeta = existing.SyncMeta
			// SyncID 终身不变、恒等于 provider_key（各写口共同维持的不变式）：上面整
			// 段搬 existing.SyncMeta 会连带搬回 existing.SyncID，正常情况下两者本就
			// 相等，这里重新赋值只是不把这份不变式的成立与否交给「这一行的历史是否
			// 干净」去赌。
			p.SyncID = p.ProviderKey
			if err := tx.Omit(append([]string{"api_key"}, reachColumns...)...).Save(p).Error; err != nil {
				return err
			}
		}

		keys := make([]string, 0, len(models))
		for _, model := range models {
			if model == nil {
				continue
			}
			model.ProviderID = p.ID
			keys = append(keys, model.ModelKey)
			var current llm_provider_model_entity.LLMProviderModel
			err := tx.Where("model_key = ?", model.ModelKey).First(&current).Error
			switch {
			case errors.Is(err, gorm.ErrRecordNotFound):
				model.Createtime, model.Updatetime = now, now
				if err := tx.Create(model).Error; err != nil {
					return err
				}
			case err != nil:
				return err
			default:
				model.ID = current.ID
				model.Createtime = current.Createtime
				model.Updatetime = now
				if err := tx.Save(model).Error; err != nil {
					return err
				}
			}
		}
		missing := tx.Model(&llm_provider_model_entity.LLMProviderModel{}).
			Where("provider_id = ? AND status = ?", p.ID, consts.ACTIVE)
		if len(keys) > 0 {
			missing = missing.Where("model_key NOT IN ?", keys)
		}
		return missing.Update("status", consts.DELETE).Error
	})
}

// ImportModels 原子导入：updates（已存在行补齐，Omit model_key 保留稳定 key）+ inserts
// （新行）在同一事务内落地，任一步失败整体回滚，不留半批状态。
func (r *llmProviderRepo) ImportModels(ctx context.Context, updates, inserts []*llm_provider_model_entity.LLMProviderModel) error {
	if len(updates) == 0 && len(inserts) == 0 {
		return nil
	}
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		ctx = db.WithContextDB(ctx, tx)
		for _, m := range updates {
			if err := db.Ctx(ctx).Omit("model_key").Save(m).Error; err != nil {
				return err
			}
		}
		for _, m := range inserts {
			if err := db.Ctx(ctx).Create(m).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *llmProviderRepo) CreateModel(ctx context.Context, m *llm_provider_model_entity.LLMProviderModel) error {
	return db.Ctx(ctx).Create(m).Error
}

// UpdateModel 只更新可编辑字段；model_key 不可变，Omit 后绝不进入 UPDATE 语句。
func (r *llmProviderRepo) UpdateModel(ctx context.Context, m *llm_provider_model_entity.LLMProviderModel) error {
	return db.Ctx(ctx).Omit("model_key").Save(m).Error
}

func (r *llmProviderRepo) FindModel(ctx context.Context, id int64) (*llm_provider_model_entity.LLMProviderModel, error) {
	out := &llm_provider_model_entity.LLMProviderModel{}
	err := db.Ctx(ctx).Where("id = ? AND status = ?", id, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *llmProviderRepo) FindModelByKey(ctx context.Context, modelKey string) (*llm_provider_model_entity.LLMProviderModel, error) {
	out := &llm_provider_model_entity.LLMProviderModel{}
	err := db.Ctx(ctx).Where("model_key = ? AND status = ?", modelKey, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *llmProviderRepo) BatchFindModelsByKey(ctx context.Context, keys []string) (map[string]*llm_provider_model_entity.LLMProviderModel, error) {
	return repoquery.ActiveMap[llm_provider_model_entity.LLMProviderModel](ctx, "model_key", keys, func(m *llm_provider_model_entity.LLMProviderModel) string {
		return m.ModelKey
	})
}

func (r *llmProviderRepo) ListModels(ctx context.Context, providerID int64) ([]*llm_provider_model_entity.LLMProviderModel, error) {
	var rows []*llm_provider_model_entity.LLMProviderModel
	if err := db.Ctx(ctx).Where("provider_id = ? AND status = ?", providerID, consts.ACTIVE).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// CountModelsByProvider 按 provider 分组统计 ACTIVE 模型数（与 ListModels 同口径：
// 只过滤 status=ACTIVE，不分 enabled），只查给定 providerIDs；空列表直接返回空 map。
func (r *llmProviderRepo) CountModelsByProvider(ctx context.Context, providerIDs []int64) (map[int64]int64, error) {
	out := make(map[int64]int64, len(providerIDs))
	if len(providerIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ProviderID int64
		Count      int64
	}
	if err := db.Ctx(ctx).
		Model(&llm_provider_model_entity.LLMProviderModel{}).
		Select("provider_id, COUNT(*) as count").
		Where("status = ? AND provider_id IN ?", consts.ACTIVE, providerIDs).
		Group("provider_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ProviderID] = row.Count
	}
	return out, nil
}

func (r *llmProviderRepo) DeleteModel(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Model(&llm_provider_model_entity.LLMProviderModel{}).
		Where("id = ?", id).
		Update("status", consts.DELETE).Error
}

// CountProviderReferences 统计某 Provider 被 Backend / Session / Route 引用的数量。
// Route 引用按结构化 target 中出现的 provider_key 字符串匹配（provider_key 是稳定
// UUID，LIKE 无歧义）。
func (r *llmProviderRepo) CountProviderReferences(ctx context.Context, providerKey string) (ProviderRefCounts, error) {
	return r.countRefs(ctx, "llm_provider_key", "provider_key", providerKey)
}

// CountModelReferences 统计某 Model（model_key）被 Backend / Session / Route 引用的数量。
func (r *llmProviderRepo) CountModelReferences(ctx context.Context, modelKey string) (ModelRefCounts, error) {
	return r.countRefs(ctx, "model_key", "model_key", modelKey)
}

// countRefs 按各表自己的列名（agent_backends / chat_sessions）统计三路引用。
func (r *llmProviderRepo) countRefs(ctx context.Context, backendColumn, sessionColumn, key string) (RefCounts, error) {
	var out RefCounts
	if err := db.Ctx(ctx).Table("agent_backends").
		Where(backendColumn+" = ? AND status = ?", key, consts.ACTIVE).
		Count(&out.Backends).Error; err != nil {
		return out, err
	}
	if err := db.Ctx(ctx).Table("chat_sessions").
		Where(sessionColumn+" = ? AND status = ?", key, consts.ACTIVE).
		Count(&out.Sessions).Error; err != nil {
		return out, err
	}
	if err := db.Ctx(ctx).Table("agent_backends").
		Where("status = ? AND json_extract(config_json, '$.modelRoutes') LIKE ?", consts.ACTIVE, "%"+key+"%").
		Count(&out.Routes).Error; err != nil {
		return out, err
	}
	return out, nil
}

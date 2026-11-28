package migrations

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/go-gormigrate/gormigrate/v2"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/backendcred"
	"github.com/agentre-hub/agentre/internal/pkg/deviceidentity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
)

// migration202611280101 引入引擎配置（spec 2026-11-25「组织成员与执行位置解耦」决策
// 2 / 3 与「迁移」1、2、3、6、7、8），并让成员、部门、会话、看板任务改为引用它。
//
// 这是增量的一步：agent_backends / agent_exec_targets 与读它们的派发路径原样保留，由后续
// 任务移除。新加的东西：
//   - engine_profiles               = 去掉设备与 CLI 路径的后端，另带「账号默认」标记；
//     账号默认至多一份（部分唯一索引）。
//   - agents.engine_profile_id      成员引用的引擎配置；0 = 跟随部门。
//   - agents.exec_constraint_json   「在哪跑」约束 {"mode":"any|labels|devices",...}。
//   - agents.skill_grants_runtime / skill_grants_json  技能授权及其授权时的运行时类型。
//   - departments.default_engine_profile_id  成员默认引擎；0 = 跟随上级。
//   - chat_sessions.engine_profile_id / issues.engine_profile_id  会话钉住的 / 任务选的引擎配置。
//
// 回填（按迁移规则逐条）：
//  1. 活后端按「除设备与 CLI 路径外配置完全相同」归组（类型、供应商、模型、环境变量、力度、
//     类型专属设置；JSON 按语义比较），一组合并成一份引擎配置，取组里 sync_id 字典序最小
//     那条后端的 sync_id 与名称。墓碑不参与取身份（各端回收的墓碑不同）：与某个活组配置
//     相同的墓碑指向那份配置，其余墓碑之间归组、落成已删除的配置，只为让钉在它上面的老
//     会话找得到最后的设置。
//  2. 成员引用第一档执行目标所在组的配置；执行目标覆盖了该组全部设备时约束为「任意设备」，
//     只覆盖一部分时为「指定设备」那几台；技能授权取第一档的，并记下该组的运行时类型。
//     没有执行目标的成员跟随部门（0）。
//  3. 部门成员默认引擎为空（列默认 0）。账号默认取 CEO 引用的那份；CEO 没有时取被成员
//     引用最多的一份（并列取 sync_id 小的），一份都没被引用时取 sync_id 最小的活配置。
//  6. 会话：钉住的执行后端换成它所在组的配置；钉住的设备列一律不动。
//  7. 看板任务：执行后端引用换成配置引用。
//  8. OpenClaw 的 Gateway token 槽位按后端 sync_id 命名，这里把组里第一份能读到的 token
//     复制到配置 sync_id 的槽位（旧槽位留给仍在用后端表的旧派发路径，随后端表一起移除）。
//     Hermes 的槽位按 serve 地址命名，地址属于配置本身，不需要搬。
//
// 不可逆的部分：无。所有旧列与旧表原样保留；新列可整体丢弃回到迁移前。
func migration202611280101() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202611280101",
		Migrate: func(tx *gorm.DB) error {
			var slots []openClawSlotCopy
			// gormigrate 默认不包事务：中途失败会留下一半的 ALTER，下次启动再跑同一条
			// ALTER 就撞「列已存在」永远起不来。SQLite 的 DDL 可以进事务。
			err := tx.Transaction(func(tx *gorm.DB) error {
				for _, stmt := range engineProfileDDL {
					if err := tx.Exec(stmt).Error; err != nil {
						return err
					}
				}
				var err error
				slots, err = backfillEngineProfiles(tx, keychain.Default())
				return err
			})
			if err != nil {
				return err
			}
			copyOpenClawSlots(keychain.Default(), slots)
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				"ALTER TABLE issues DROP COLUMN engine_profile_id",
				"ALTER TABLE chat_sessions DROP COLUMN engine_profile_id",
				"ALTER TABLE departments DROP COLUMN default_engine_profile_id",
				"ALTER TABLE agents DROP COLUMN skill_grants_json",
				"ALTER TABLE agents DROP COLUMN skill_grants_runtime",
				"ALTER TABLE agents DROP COLUMN exec_constraint_json",
				"ALTER TABLE agents DROP COLUMN engine_profile_id",
				"DROP TABLE IF EXISTS engine_profiles",
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}

var engineProfileDDL = []string{
	`CREATE TABLE IF NOT EXISTS engine_profiles (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	type TEXT NOT NULL,
	name TEXT NOT NULL,
	llm_provider_key TEXT NOT NULL DEFAULT '',
	model_key TEXT NOT NULL DEFAULT '',
	env_json TEXT NOT NULL DEFAULT '{}',
	reasoning_effort TEXT NOT NULL DEFAULT '',
	config_json TEXT NOT NULL DEFAULT '{}',
	is_account_default BOOLEAN NOT NULL DEFAULT 0,
	status INTEGER NOT NULL DEFAULT 1,
	createtime INTEGER NOT NULL DEFAULT 0,
	updatetime INTEGER NOT NULL DEFAULT 0,
	sync_id TEXT NOT NULL DEFAULT '',
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	sync_version BIGINT NOT NULL DEFAULT 0,
	sync_updated_at BIGINT NOT NULL DEFAULT 0,
	sync_origin_fingerprint TEXT NOT NULL DEFAULT '',
	sync_deleted_at BIGINT NOT NULL DEFAULT 0
)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS uniq_engine_profiles_sync_id ON engine_profiles(sync_id) WHERE sync_id != ''`,
	`CREATE UNIQUE INDEX IF NOT EXISTS uniq_engine_profiles_account_default ON engine_profiles(is_account_default) WHERE is_account_default = 1 AND status = 1`,
	"ALTER TABLE agents ADD COLUMN engine_profile_id INTEGER NOT NULL DEFAULT 0",
	`ALTER TABLE agents ADD COLUMN exec_constraint_json TEXT NOT NULL DEFAULT '{}'`,
	`ALTER TABLE agents ADD COLUMN skill_grants_runtime TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE agents ADD COLUMN skill_grants_json TEXT NOT NULL DEFAULT '[]'`,
	"ALTER TABLE departments ADD COLUMN default_engine_profile_id INTEGER NOT NULL DEFAULT 0",
	"ALTER TABLE chat_sessions ADD COLUMN engine_profile_id INTEGER NOT NULL DEFAULT 0",
	"ALTER TABLE issues ADD COLUMN engine_profile_id INTEGER NOT NULL DEFAULT 0",
}

// migBackend 是回填读到的一条后端。只取归组与建配置要用的列，不依赖实体（实体会随
// 后续任务变，迁移读到的必须是这一刻的表形状）。
type migBackend struct {
	ID                int64  `gorm:"column:id"`
	Type              string `gorm:"column:type"`
	Name              string `gorm:"column:name"`
	LLMProviderKey    string `gorm:"column:llm_provider_key"`
	ModelKey          string `gorm:"column:model_key"`
	DeviceFingerprint string `gorm:"column:device_fingerprint"`
	EnvJSON           string `gorm:"column:env_json"`
	ReasoningEffort   string `gorm:"column:reasoning_effort"`
	ConfigJSON        string `gorm:"column:config_json"`
	Status            int    `gorm:"column:status"`
	Createtime        int64  `gorm:"column:createtime"`
	Updatetime        int64  `gorm:"column:updatetime"`
	SyncID            string `gorm:"column:sync_id"`
	SyncAccountID     int64  `gorm:"column:sync_account_id"`
}

// migEngineProfile 是回填写入的一行引擎配置。
type migEngineProfile struct {
	ID               int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Type             string `gorm:"column:type"`
	Name             string `gorm:"column:name"`
	LLMProviderKey   string `gorm:"column:llm_provider_key"`
	ModelKey         string `gorm:"column:model_key"`
	EnvJSON          string `gorm:"column:env_json"`
	ReasoningEffort  string `gorm:"column:reasoning_effort"`
	ConfigJSON       string `gorm:"column:config_json"`
	IsAccountDefault bool   `gorm:"column:is_account_default"`
	Status           int    `gorm:"column:status"`
	Createtime       int64  `gorm:"column:createtime"`
	Updatetime       int64  `gorm:"column:updatetime"`
	SyncID           string `gorm:"column:sync_id"`
	SyncAccountID    int64  `gorm:"column:sync_account_id"`
}

func (migEngineProfile) TableName() string { return "engine_profiles" }

type migExecTarget struct {
	AgentID        int64  `gorm:"column:agent_id"`
	AgentBackendID int64  `gorm:"column:agent_backend_id"`
	SkillsJSON     string `gorm:"column:skills_json"`
}

type migAgent struct {
	ID          int64  `gorm:"column:id"`
	SystemBadge string `gorm:"column:system_badge"`
	Status      int    `gorm:"column:status"`
}

// migConstraint 与 agent_entity.ExecConstraint 的 JSON 同形。
type migConstraint struct {
	Mode    string   `json:"mode"`
	Devices []string `json:"devices,omitempty"`
}

// engineGroup 一组「除设备与 CLI 路径外配置完全相同」的后端。
type engineGroup struct {
	// live 组由活后端组成；否则是一组无活后端可并的墓碑，落成已删除的配置。
	live bool
	// members 决定配置的身份与设置，按 sync_id 字典序（空 sync_id 排最后）、再按 id 排。
	members []*migBackend
	// tombstones 是与活组配置相同的墓碑：只把钉在它们上面的会话 / 任务指到这份配置。
	tombstones []*migBackend
	profile    *migEngineProfile
	devices    map[string]bool // 组里活后端所在的设备（规范指纹）
}

// openClawSlotCopy 是迁移规则 8 要做的一次槽位复制：配置的槽位缺席时，按顺序取第一个
// 能读到的后端槽位。
type openClawSlotCopy struct {
	profileSyncID  string
	backendSyncIDs []string
}

const (
	migActive  = 1
	migDeleted = 2
)

func backfillEngineProfiles(tx *gorm.DB, kc keychain.Keychain) ([]openClawSlotCopy, error) {
	var backends []*migBackend
	if err := tx.Raw(`SELECT id, type, name, llm_provider_key, model_key, device_fingerprint, env_json,
reasoning_effort, config_json, status, createtime, updatetime, sync_id, sync_account_id
FROM agent_backends ORDER BY id`).Scan(&backends).Error; err != nil {
		return nil, err
	}
	if len(backends) == 0 {
		return nil, nil
	}

	selfFP := selfDeviceFingerprint(kc)
	canonicalDevice := func(fp string) string {
		if fp == "" {
			return selfFP
		}
		return fp
	}

	// 活后端先归组：配置的 sync_id 与名称只从活后端里取。墓碑会被回收（决策 24），各端
	// 留下的墓碑不一样，让它们参与「最小 sync_id」会让同一组在不同端得到不同的身份。
	liveByKey := map[string]*engineGroup{}
	deadByKey := map[string]*engineGroup{}
	var groups []*engineGroup
	for _, b := range backends {
		if b.Status != migActive {
			continue
		}
		key := engineGroupKey(b)
		g := liveByKey[key]
		if g == nil {
			g = &engineGroup{live: true, devices: map[string]bool{}}
			liveByKey[key] = g
			groups = append(groups, g)
		}
		g.members = append(g.members, b)
		g.devices[canonicalDevice(b.DeviceFingerprint)] = true
	}
	// 墓碑只为让钉在它上面的老会话 / 任务有处可指：与某个活组配置相同就指向那份活配置
	// （设置完全一样），否则在墓碑之间归组、落成一份已删除的配置。
	for _, b := range backends {
		if b.Status == migActive {
			continue
		}
		key := engineGroupKey(b)
		if g := liveByKey[key]; g != nil {
			g.tombstones = append(g.tombstones, b)
			continue
		}
		g := deadByKey[key]
		if g == nil {
			g = &engineGroup{devices: map[string]bool{}}
			deadByKey[key] = g
			groups = append(groups, g)
		}
		g.members = append(g.members, b)
	}
	for _, g := range groups {
		sort.SliceStable(g.members, func(i, j int) bool { return backendBefore(g.members[i], g.members[j]) })
		sort.SliceStable(g.tombstones, func(i, j int) bool { return backendBefore(g.tombstones[i], g.tombstones[j]) })
	}
	sort.SliceStable(groups, func(i, j int) bool { return backendBefore(groups[i].members[0], groups[j].members[0]) })

	groupOf := make(map[int64]*engineGroup, len(backends))
	var slots []openClawSlotCopy
	for _, g := range groups {
		rep := g.members[0]
		status := migDeleted
		if g.live {
			status = migActive
		}
		syncID := rep.SyncID
		if syncID == "" {
			syncID = syncmeta_entity.NewSyncID()
		}
		g.profile = &migEngineProfile{
			Type: rep.Type, Name: rep.Name,
			LLMProviderKey: rep.LLMProviderKey, ModelKey: rep.ModelKey,
			EnvJSON: rep.EnvJSON, ReasoningEffort: rep.ReasoningEffort, ConfigJSON: rep.ConfigJSON,
			Status: status, Createtime: rep.Createtime, Updatetime: rep.Updatetime,
			SyncID: syncID, SyncAccountID: rep.SyncAccountID,
		}
		if err := tx.Create(g.profile).Error; err != nil {
			return nil, err
		}
		mapped := append(append([]*migBackend(nil), g.members...), g.tombstones...)
		for _, m := range mapped {
			groupOf[m.ID] = g
		}
		if rep.Type == "openclaw" {
			copyPlan := openClawSlotCopy{profileSyncID: syncID}
			for _, m := range mapped {
				if m.SyncID != "" && m.SyncID != syncID {
					copyPlan.backendSyncIDs = append(copyPlan.backendSyncIDs, m.SyncID)
				}
			}
			if len(copyPlan.backendSyncIDs) > 0 {
				slots = append(slots, copyPlan)
			}
		}
	}
	backendByID := make(map[int64]*migBackend, len(backends))
	for _, b := range backends {
		backendByID[b.ID] = b
	}

	refs, ceoProfile, err := backfillMembers(tx, groupOf, backendByID, canonicalDevice)
	if err != nil {
		return nil, err
	}
	if def := pickAccountDefault(groups, refs, ceoProfile); def != nil {
		if err := tx.Exec("UPDATE engine_profiles SET is_account_default = 1 WHERE id = ?", def.ID).Error; err != nil {
			return nil, err
		}
	}

	for _, b := range backends {
		profileID := groupOf[b.ID].profile.ID
		if err := tx.Exec("UPDATE chat_sessions SET engine_profile_id = ? WHERE exec_agent_backend_id = ?", profileID, b.ID).Error; err != nil {
			return nil, err
		}
		if err := tx.Exec("UPDATE issues SET engine_profile_id = ? WHERE agent_backend_id = ?", profileID, b.ID).Error; err != nil {
			return nil, err
		}
	}
	return slots, nil
}

// backfillMembers 落迁移规则 2：每个成员引用第一档（活的）执行目标所在组的配置，并按
// 覆盖的设备给出约束、取第一档的技能授权。返回活成员对每份配置的引用数，以及 CEO 引用的
// 那份（没有为 nil）。
func backfillMembers(
	tx *gorm.DB,
	groupOf map[int64]*engineGroup,
	backendByID map[int64]*migBackend,
	canonicalDevice func(string) string,
) (map[*migEngineProfile]int, *migEngineProfile, error) {
	var agents []*migAgent
	if err := tx.Raw("SELECT id, system_badge, status FROM agents ORDER BY id").Scan(&agents).Error; err != nil {
		return nil, nil, err
	}
	var targets []*migExecTarget
	if err := tx.Raw("SELECT agent_id, agent_backend_id, skills_json FROM agent_exec_targets ORDER BY agent_id, sort_order, id").
		Scan(&targets).Error; err != nil {
		return nil, nil, err
	}
	byAgent := map[int64][]*migExecTarget{}
	for _, t := range targets {
		// 指向墓碑后端（或已回收的后端）的档已经不是能派发的档，不参与「第一档」。
		if b := backendByID[t.AgentBackendID]; b == nil || b.Status != migActive {
			continue
		}
		byAgent[t.AgentID] = append(byAgent[t.AgentID], t)
	}

	refs := map[*migEngineProfile]int{}
	var ceoProfile *migEngineProfile
	for _, a := range agents {
		list := byAgent[a.ID]
		if len(list) == 0 {
			continue // 没有执行目标：跟随部门
		}
		first := list[0]
		g := groupOf[first.AgentBackendID]
		covered := map[string]bool{}
		var devices []string
		for _, t := range list {
			if groupOf[t.AgentBackendID] != g {
				continue
			}
			fp := canonicalDevice(backendByID[t.AgentBackendID].DeviceFingerprint)
			if covered[fp] {
				continue
			}
			covered[fp] = true
			devices = append(devices, fp)
		}
		constraint := migConstraint{Mode: "any"}
		for fp := range g.devices {
			if !covered[fp] {
				constraint = migConstraint{Mode: "devices", Devices: devices}
				break
			}
		}
		constraintJSON, err := json.Marshal(constraint)
		if err != nil {
			return nil, nil, err
		}
		skills := strings.TrimSpace(first.SkillsJSON)
		if skills == "" {
			skills = "[]"
		}
		if err := tx.Exec(
			"UPDATE agents SET engine_profile_id = ?, exec_constraint_json = ?, skill_grants_runtime = ?, skill_grants_json = ? WHERE id = ?",
			g.profile.ID, string(constraintJSON), g.profile.Type, skills, a.ID,
		).Error; err != nil {
			return nil, nil, err
		}
		if a.Status != migActive {
			continue
		}
		refs[g.profile]++
		if a.SystemBadge == "DEFAULT" {
			ceoProfile = g.profile
		}
	}
	return refs, ceoProfile, nil
}

// pickAccountDefault 落迁移规则 3 的账号默认：CEO 引用的那份 › 引用最多的一份 › sync_id
// 最小的活配置。groups 已按 sync_id 排好，「并列取前」就是取 sync_id 小的。
func pickAccountDefault(groups []*engineGroup, refs map[*migEngineProfile]int, ceoProfile *migEngineProfile) *migEngineProfile {
	if ceoProfile != nil && ceoProfile.Status == migActive {
		return ceoProfile
	}
	var best *migEngineProfile
	bestRefs := -1
	for _, g := range groups {
		p := g.profile
		if p.Status != migActive {
			continue
		}
		if refs[p] > bestRefs {
			best, bestRefs = p, refs[p]
		}
	}
	return best
}

// backendBefore 是组内与组间的排序：sync_id 字典序在前，空 sync_id（迁移前的历史行，
// 没有跨端身份）排最后，同 sync_id 再按 id。
func backendBefore(a, b *migBackend) bool {
	if (a.SyncID == "") != (b.SyncID == "") {
		return a.SyncID != ""
	}
	if a.SyncID != b.SyncID {
		return a.SyncID < b.SyncID
	}
	return a.ID < b.ID
}

// engineGroupKey 是迁移规则 1 的「配置完全相同」：除设备与 CLI 路径（以及名称、状态、
// 同步元数据这些不属于配置的列）以外的每一格。JSON 列按语义比较——键序、空值键不算不同。
func engineGroupKey(b *migBackend) string {
	return strings.Join([]string{
		strings.TrimSpace(b.Type),
		strings.TrimSpace(b.LLMProviderKey),
		strings.TrimSpace(b.ModelKey),
		canonicalJSONObject(b.EnvJSON),
		strings.TrimSpace(b.ReasoningEffort),
		canonicalJSONObject(b.ConfigJSON),
	}, "\x00")
}

// canonicalJSONObject 把一个 JSON 对象规范成「去掉空值键、键排序」的形态；解析失败
// 原样返回（坏数据只和自己相同）。
func canonicalJSONObject(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}"
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return raw
	}
	for k, v := range obj {
		if isEmptyJSONValue(v) {
			delete(obj, k)
		}
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return string(out)
}

func isEmptyJSONValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	default:
		return false
	}
}

// selfDeviceFingerprint 读出本机设备指纹；没有（从未配对、从未登录）时为空串。只读不铸：
// 没有指纹的桌面端也没有任何远端后端，组里的设备集合只有「本机」一台，约束必然是「任意」。
func selfDeviceFingerprint(kc keychain.Keychain) string {
	if kc == nil {
		return ""
	}
	fp, err := kc.Get(deviceidentity.KeychainAccount)
	if err != nil {
		return ""
	}
	return fp
}

// copyOpenClawSlots 落迁移规则 8。尽力而为：钥匙串读写失败只记日志，不挡启动——旧槽位
// 原样还在，仍用后端表的派发路径照常读得到，用户最坏是重新填一次 token。
func copyOpenClawSlots(kc keychain.Keychain, slots []openClawSlotCopy) {
	if kc == nil {
		return
	}
	for _, slot := range slots {
		target := backendcred.OpenClawTokenAccount(slot.profileSyncID)
		if _, err := kc.Get(target); err == nil {
			continue
		} else if !errors.Is(err, keychain.ErrNotFound) {
			logger.Default().Warn("migrations.copyOpenClawSlots: read profile slot failed",
				zap.String("profileSyncId", slot.profileSyncID), zap.Error(err))
			continue
		}
		for _, backendSyncID := range slot.backendSyncIDs {
			token, err := kc.Get(backendcred.OpenClawTokenAccount(backendSyncID))
			if err != nil {
				continue
			}
			if err := kc.Set(target, token); err != nil {
				logger.Default().Warn("migrations.copyOpenClawSlots: write profile slot failed",
					zap.String("profileSyncId", slot.profileSyncID), zap.Error(err))
			}
			break
		}
	}
}

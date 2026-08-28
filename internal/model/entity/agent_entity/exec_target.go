package agent_entity

import (
	"encoding/json"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
)

// AgentExecTarget 是 Agent 有序执行目标列表里的一项。列表的每一项是一个
// backend；backend 自己的 device_fingerprint 决定这一档落在哪台机器上（空 = 当前桌面端，
// 非空 = 那台 agentred）。
//
// SkillsJSON 是这一执行目标自己的技能授权：每个目标各自持有一份，
// 发现从这一档所在的机器发起（本地进程内调 CLI / 远端走 daemon skills.list），
// 不与列表里别的档合并、不做并集。存放位置从 agents.skills_json 下沉到这里；
// GetSkills / SetSkills / GetEnabledPackIDs / SkillPackEnabled 随字段一起搬过来。
type AgentExecTarget struct {
	ID             int64  `gorm:"column:id;primaryKey;autoIncrement"`
	AgentID        int64  `gorm:"column:agent_id;type:bigint;not null"`
	AgentBackendID int64  `gorm:"column:agent_backend_id;type:bigint;not null"`
	SortOrder      int    `gorm:"column:sort_order;type:int;not null;default:0"`
	SkillsJSON     string `gorm:"column:skills_json;type:text;not null;default:'[]'"`
	// SyncMeta 是账号级同步元数据。
	syncmeta_entity.SyncMeta `gorm:"embedded"`
}

func (*AgentExecTarget) TableName() string { return "agent_exec_targets" }

// PrimaryExecTargets 把「一个 backend + 它那一份技能授权」这个 R15 之前的单档形状
// 转成执行目标列表：backend 为 0（没绑）时是空列表，否则是单元素列表（下标即
// sort_order，因此是 0）。迁移、仓储的单档写入路径与老 bundle 的导入回落共用这一
// 份转换（R15f），它们必须逐字节一致。
func PrimaryExecTargets(backendID int64, skillsJSON string) []*AgentExecTarget {
	if backendID <= 0 {
		return nil
	}
	return []*AgentExecTarget{{AgentBackendID: backendID, SkillsJSON: skillsJSON}}
}

func (t *AgentExecTarget) GetSkills() []AgentSkillItem {
	out := []AgentSkillItem{}
	if t == nil || t.SkillsJSON == "" {
		return out
	}
	_ = json.Unmarshal([]byte(t.SkillsJSON), &out)
	if out == nil {
		out = []AgentSkillItem{}
	}
	return out
}

func (t *AgentExecTarget) SetSkills(items []AgentSkillItem) {
	if items == nil {
		items = []AgentSkillItem{}
	}
	b, _ := json.Marshal(items)
	t.SkillsJSON = string(b)
}

// GetEnabledPackIDs 返回 enabled 的技能包 id。
func (t *AgentExecTarget) GetEnabledPackIDs() []string {
	out := []string{}
	for _, it := range t.GetSkills() {
		if it.Enabled {
			out = append(out, it.ID)
		}
	}
	return out
}

// SkillPackEnabled 报告某技能包是否开启。
func (t *AgentExecTarget) SkillPackEnabled(id string) bool {
	for _, it := range t.GetSkills() {
		if it.ID == id {
			return it.Enabled
		}
	}
	return false
}

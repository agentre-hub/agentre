// Package agent_entity 维护 Agent 的充血实体。
package agent_entity

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// SystemBadgeDefault 标记不可删除的 CEO 助手。
const SystemBadgeDefault = "DEFAULT"

// AgentSkillItem Agent 技能包(= Claude Code plugin)开关。ID 形如 "name@marketplace"。
type AgentSkillItem struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

// AgentToolItem Agent 内置工具开关（key 对应 internal/pkg/agenttool 注册表）。
type AgentToolItem struct {
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
}

// Agent 一条 Agent 记录。
type Agent struct {
	ID            int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Name          string `gorm:"column:name;type:text;not null"`
	Description   string `gorm:"column:description;type:text;not null;default:''"`
	AvatarColor   string `gorm:"column:avatar_color;type:text;not null;default:''"`
	AvatarIcon    string `gorm:"column:avatar_icon;type:text;not null;default:''"`
	AvatarDataURL string `gorm:"column:avatar_data_url;type:text;not null;default:''"`
	SystemBadge   string `gorm:"column:system_badge;type:text;not null;default:''"`
	DepartmentID  int64  `gorm:"column:department_id;type:bigint;not null;default:0"`
	ParentAgentID int64  `gorm:"column:parent_agent_id;type:bigint;not null;default:0"`
	// AgentBackendID 是 Agent 执行目标列表的**派生值**：sort_order 最小的那一档，
	// 没有目标行则为 0。仓储读取时一律由 agent_exec_targets 补齐；agents 表的
	// agent_backend_id 是旧写入列，不再作为读取来源。
	AgentBackendID int64  `gorm:"column:agent_backend_id;type:bigint;not null;default:0"`
	SortOrder      int    `gorm:"column:sort_order;type:int;not null;default:0"`
	PromptJSON     string `gorm:"column:prompt_json;type:text;not null;default:'[]'"`
	// SkillsJSON 是旧存储列，仅供仓储读写现有行；技能授权的读取与富方法
	// （GetSkills 等）属于 AgentExecTarget。业务代码
	// 不应再直接消费这个字段的语义，只应把它当成传给仓储层的原始载荷。
	SkillsJSON string `gorm:"column:skills_json;type:text;not null;default:'[]'"`
	ToolsJSON  string `gorm:"column:tools_json;type:text;not null;default:'[]'"`
	Status     int    `gorm:"column:status;type:int;not null;default:1"`
	Pinned     bool   `gorm:"column:pinned;type:boolean;not null;default:0"`
	Createtime int64  `gorm:"column:createtime;type:bigint;not null;default:0"`
	Updatetime int64  `gorm:"column:updatetime;type:bigint;not null;default:0"`
	// SyncMeta 是账号级同步元数据。
	syncmeta_entity.SyncMeta `gorm:"embedded"`
}

func (*Agent) TableName() string { return "agents" }

func (a *Agent) IsActive() bool { return a != nil && a.Status == consts.ACTIVE }
func (a *Agent) IsSystem() bool { return a != nil && a.SystemBadge == SystemBadgeDefault }

func (a *Agent) GetPrompt() []string {
	out := []string{}
	if a == nil || a.PromptJSON == "" {
		return out
	}
	_ = json.Unmarshal([]byte(a.PromptJSON), &out)
	if out == nil {
		out = []string{}
	}
	return out
}

func (a *Agent) SetPrompt(lines []string) {
	if lines == nil {
		lines = []string{}
	}
	b, _ := json.Marshal(lines)
	a.PromptJSON = string(b)
}

func (a *Agent) GetTools() []AgentToolItem {
	out := []AgentToolItem{}
	if a == nil || a.ToolsJSON == "" {
		return out
	}
	_ = json.Unmarshal([]byte(a.ToolsJSON), &out)
	if out == nil {
		out = []AgentToolItem{}
	}
	return out
}

func (a *Agent) SetTools(items []AgentToolItem) {
	if items == nil {
		items = []AgentToolItem{}
	}
	b, _ := json.Marshal(items)
	a.ToolsJSON = string(b)
}

// ToolEnabled 报告某内置工具是否开启。
func (a *Agent) ToolEnabled(key string) bool {
	for _, it := range a.GetTools() {
		if it.Key == key {
			return it.Enabled
		}
	}
	return false
}

var allowedAvatarColors = map[string]struct{}{
	"":         {},
	"agent-1":  {},
	"agent-2":  {},
	"agent-3":  {},
	"agent-4":  {},
	"agent-5":  {},
	"agent-6":  {},
	"agent-7":  {},
	"agent-8":  {},
	"agent-9":  {},
	"agent-10": {},
	"agent-11": {},
	"agent-12": {},
	"agent-13": {},
	"agent-14": {},
	"agent-15": {},
	"agent-16": {},
	"neutral":  {},
}

// Check 字段校验。
func (a *Agent) Check(ctx context.Context) error {
	if a == nil {
		return i18n.NewError(ctx, code.AgentNotFound)
	}
	if strings.TrimSpace(a.Name) == "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	if _, ok := allowedAvatarColors[a.AvatarColor]; !ok {
		return i18n.NewError(ctx, code.AgentInvalidColor)
	}
	if len(a.AvatarIcon) > 32 {
		return i18n.NewError(ctx, code.InvalidParameter)
	}

	if a.IsSystem() {
		if a.DepartmentID != 0 || a.ParentAgentID != 0 {
			return i18n.NewError(ctx, code.AgentSystemImmutable)
		}
	} else {
		hasDepartment := a.DepartmentID > 0
		hasParentAgent := a.ParentAgentID > 0
		if !hasDepartment && !hasParentAgent {
			return i18n.NewError(ctx, code.AgentDepartmentRequired)
		}
		if hasDepartment && hasParentAgent {
			return i18n.NewError(ctx, code.InvalidParameter)
		}
		// AgentBackendID == 0 表示"未配置后端"，对话时引导用户选择即可。
	}

	if !isValidJSONArray(a.PromptJSON) {
		return i18n.NewError(ctx, code.AgentInvalidPayload)
	}
	// 技能授权由 AgentExecTarget 存储并校验；Agent 行上的旧列只是仓储写入载荷。
	if !isValidJSONArray(a.ToolsJSON) {
		return i18n.NewError(ctx, code.AgentInvalidPayload)
	}
	return nil
}

func isValidJSONArray(s string) bool {
	if s == "" {
		return true
	}
	var v []json.RawMessage
	return json.Unmarshal([]byte(s), &v) == nil
}

// Package memory_entity 维护 Agent 记忆的充血实体：归属层、内容规则、上一版、
// 状态与预算取舍。
package memory_entity

import (
	"cmp"
	"slices"
	"unicode/utf8"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
)

const (
	// SummaryMaxChars 摘要上限（字数，按 rune 计）。
	SummaryMaxChars = 200
	// DetailMaxChars 详情上限（字数，按 rune 计）。
	DetailMaxChars = 4000
	// LayerBudgetChars 每层随会话带上的摘要预算（字数）。超出照常存，只是本次不带。
	LayerBudgetChars = 2000
	// RecentChangeWindowMillis 「最近变更」回看的时间窗：14 天。
	RecentChangeWindowMillis int64 = 14 * 24 * 60 * 60 * 1000
)

// OwnerKind 记忆所属的层。
type OwnerKind string

const (
	OwnerGlobal     OwnerKind = "global"
	OwnerDepartment OwnerKind = "department"
	OwnerAgent      OwnerKind = "agent"
	OwnerProject    OwnerKind = "project"
)

// Owner 一条记忆的归属：全局（ID 恒为 0）或某个部门 / agent / 项目。
type Owner struct {
	Kind OwnerKind `json:"kind"`
	ID   int64     `json:"id"`
}

// GlobalOwner 全局层。
func GlobalOwner() Owner { return Owner{Kind: OwnerGlobal} }

// Valid 报告归属形状是否合法（不查归属是否存在，那是 service 的事）。
func (o Owner) Valid() bool {
	switch o.Kind {
	case OwnerGlobal:
		return o.ID == 0
	case OwnerDepartment, OwnerAgent, OwnerProject:
		return o.ID > 0
	default:
		return false
	}
}

// State 条目状态：只有正常与已归档两种（决策 8）。
type State string

const (
	StateNormal   State = "normal"
	StateArchived State = "archived"
)

// ActorKind 写入者种类。
type ActorKind string

const (
	ActorUser ActorKind = "user"
	ActorAI   ActorKind = "ai"
)

// Actor 一次写入是谁做的：用户，或某个 agent 在某个会话里。
type Actor struct {
	Kind      ActorKind `json:"kind"`
	AgentID   int64     `json:"agentId"`
	SessionID int64     `json:"sessionId"`
}

// UserActor 用户本人。
func UserActor() Actor { return Actor{Kind: ActorUser} }

// AIActor agentID 这个 agent 在 sessionID 这个会话里。
func AIActor(agentID, sessionID int64) Actor {
	return Actor{Kind: ActorAI, AgentID: agentID, SessionID: sessionID}
}

// Valid 用户不带 id；AI 必须同时带 agent 与会话。
func (a Actor) Valid() bool {
	switch a.Kind {
	case ActorUser:
		return a.AgentID == 0 && a.SessionID == 0
	case ActorAI:
		return a.AgentID > 0 && a.SessionID > 0
	default:
		return false
	}
}

// ChangeAction 「最近变更」里一条变更做了什么。置顶不算变更。
type ChangeAction string

const (
	ActionCreated    ChangeAction = "created"
	ActionEdited     ChangeAction = "edited"
	ActionArchived   ChangeAction = "archived"
	ActionUnarchived ChangeAction = "unarchived"
	ActionRestored   ChangeAction = "restored"
	ActionMoved      ChangeAction = "moved"
)

// Change 最近一次变更：谁、做了什么、何时（毫秒 epoch）。
type Change struct {
	Action ChangeAction
	Actor  Actor
	At     int64
}

// Memory 一条记忆。摘要常驻提示词，详情由 agent 按需读取；Prev* 是上一版，只保留一版。
type Memory struct {
	ID          int64     `gorm:"column:id;primaryKey;autoIncrement"`
	OwnerKind   OwnerKind `gorm:"column:owner_kind;type:text;not null"`
	OwnerID     int64     `gorm:"column:owner_id;type:bigint;not null;default:0"`
	Summary     string    `gorm:"column:summary;type:text;not null"`
	Detail      string    `gorm:"column:detail;type:text;not null;default:''"`
	PrevSummary string    `gorm:"column:prev_summary;type:text;not null;default:''"`
	PrevDetail  string    `gorm:"column:prev_detail;type:text;not null;default:''"`
	Pinned      bool      `gorm:"column:pinned;type:boolean;not null;default:0"`
	State       State     `gorm:"column:state;type:text;not null;default:'normal'"`
	// 创建者：创建后不变。
	CreatorKind      ActorKind `gorm:"column:creator_kind;type:text;not null"`
	CreatorAgentID   int64     `gorm:"column:creator_agent_id;type:bigint;not null;default:0"`
	CreatorSessionID int64     `gorm:"column:creator_session_id;type:bigint;not null;default:0"`
	// 最近一次变更：「最近变更」与预算取舍里的「最近修改」都读 ChangedAt。
	ChangeAction    ChangeAction `gorm:"column:change_action;type:text;not null"`
	ChangeActorKind ActorKind    `gorm:"column:change_actor_kind;type:text;not null"`
	ChangeAgentID   int64        `gorm:"column:change_agent_id;type:bigint;not null;default:0"`
	ChangeSessionID int64        `gorm:"column:change_session_id;type:bigint;not null;default:0"`
	ChangedAt       int64        `gorm:"column:changed_at;type:bigint;not null;default:0"`
	Createtime      int64        `gorm:"column:createtime;type:bigint;not null;default:0"`
	Updatetime      int64        `gorm:"column:updatetime;type:bigint;not null;default:0"`
	// SyncMeta 账号级同步元数据（决策 15：记忆是一个同步种类）。
	syncmeta_entity.SyncMeta `gorm:"embedded"`
}

// TableName 绑定表名。
func (*Memory) TableName() string { return "memories" }

// NewMemory 构造一条正常状态的新记忆，创建者与最近变更都记为 actor。内容需已经过
// NormalizeContent + CheckContent。
func NewMemory(owner Owner, summary, detail string, actor Actor, now int64) *Memory {
	m := &Memory{
		OwnerKind:        owner.Kind,
		OwnerID:          owner.ID,
		Summary:          summary,
		Detail:           detail,
		State:            StateNormal,
		CreatorKind:      actor.Kind,
		CreatorAgentID:   actor.AgentID,
		CreatorSessionID: actor.SessionID,
	}
	m.recordChange(ActionCreated, actor, now)
	return m
}

// Owner 归属。
func (m *Memory) Owner() Owner { return Owner{Kind: m.OwnerKind, ID: m.OwnerID} }

// Creator 创建者。
func (m *Memory) Creator() Actor {
	return Actor{Kind: m.CreatorKind, AgentID: m.CreatorAgentID, SessionID: m.CreatorSessionID}
}

// LastChange 最近一次变更。
func (m *Memory) LastChange() Change {
	return Change{
		Action: m.ChangeAction,
		Actor:  Actor{Kind: m.ChangeActorKind, AgentID: m.ChangeAgentID, SessionID: m.ChangeSessionID},
		At:     m.ChangedAt,
	}
}

// IsArchived 是否已归档。
func (m *Memory) IsArchived() bool { return m.State == StateArchived }

// HasPrevious 是否有上一版。摘要必填，所以上一版的摘要非空即「有上一版」。
func (m *Memory) HasPrevious() bool { return m.PrevSummary != "" }

// SummaryChars 摘要字数，预算按它计。
func (m *Memory) SummaryChars() int { return utf8.RuneCountInString(m.Summary) }

// Edit 改摘要与详情，修改前的内容成为上一版。内容没变返回 false，什么都不动。
func (m *Memory) Edit(summary, detail string, actor Actor, now int64) bool {
	if summary == m.Summary && detail == m.Detail {
		return false
	}
	m.PrevSummary, m.PrevDetail = m.Summary, m.Detail
	m.Summary, m.Detail = summary, detail
	m.recordChange(ActionEdited, actor, now)
	return true
}

// RestorePrevious 把当前内容与上一版对调（决策 9）。没有上一版返回 false。
func (m *Memory) RestorePrevious(actor Actor, now int64) bool {
	if !m.HasPrevious() {
		return false
	}
	m.Summary, m.PrevSummary = m.PrevSummary, m.Summary
	m.Detail, m.PrevDetail = m.PrevDetail, m.Detail
	m.recordChange(ActionRestored, actor, now)
	return true
}

// Archive 归档：不再进提示词，可恢复。
func (m *Memory) Archive(actor Actor, now int64) {
	m.State = StateArchived
	m.recordChange(ActionArchived, actor, now)
}

// Unarchive 从归档恢复为正常。
func (m *Memory) Unarchive(actor Actor, now int64) {
	m.State = StateNormal
	m.recordChange(ActionUnarchived, actor, now)
}

// MoveTo 移到另一层。
func (m *Memory) MoveTo(owner Owner, actor Actor, now int64) {
	m.OwnerKind, m.OwnerID = owner.Kind, owner.ID
	m.recordChange(ActionMoved, actor, now)
}

func (m *Memory) recordChange(action ChangeAction, actor Actor, now int64) {
	m.ChangeAction = action
	m.ChangeActorKind, m.ChangeAgentID, m.ChangeSessionID = actor.Kind, actor.AgentID, actor.SessionID
	m.ChangedAt = now
}

// SplitByBudget 按「置顶在前、其余按最近修改（同时刻 id 新的在前）」排序，从头取到
// 预算放不下为止；放不下的那条及其后全部本次不带（决策 5）。不改动入参顺序。
func SplitByBudget(entries []*Memory, budget int) (carried, over []*Memory) {
	ordered := slices.Clone(entries)
	slices.SortStableFunc(ordered, func(a, b *Memory) int {
		if a.Pinned != b.Pinned {
			if a.Pinned {
				return -1
			}
			return 1
		}
		if c := cmp.Compare(b.ChangedAt, a.ChangedAt); c != 0 {
			return c
		}
		return cmp.Compare(b.ID, a.ID)
	})
	used := 0
	for i, m := range ordered {
		if used+m.SummaryChars() > budget {
			return ordered[:i], ordered[i:]
		}
		used += m.SummaryChars()
	}
	return ordered, nil
}

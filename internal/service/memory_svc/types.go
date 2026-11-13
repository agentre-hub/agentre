package memory_svc

import "github.com/agentre-hub/agentre/internal/model/entity/memory_entity"

// CreateRequest 新增一条记忆。Actor 是写入者：记忆页传 UserActor，会话里的 AI 写入
// 传 AIActor(agentID, sessionID)。Actor 不来自前端入参，由调用方按通道填写。
type CreateRequest struct {
	Owner   memory_entity.Owner
	Summary string
	Detail  string
	Actor   memory_entity.Actor
}

// UpdateRequest 改一条记忆的摘要与详情。
type UpdateRequest struct {
	ID      int64
	Summary string
	Detail  string
	Actor   memory_entity.Actor
}

// CreateResult Existed=true 表示同层已有摘要完全相同的正常条目，Item 就是那一条，没有新增。
type CreateResult struct {
	Item    *MemoryItem `json:"item"`
	Existed bool        `json:"existed"`
}

// ChangeDTO 最近一次变更。
type ChangeDTO struct {
	Action memory_entity.ChangeAction `json:"action"`
	Actor  memory_entity.Actor        `json:"actor"`
	At     int64                      `json:"at"`
}

// MemoryItem 一条记忆的前端视图。
type MemoryItem struct {
	ID          int64               `json:"id"`
	Owner       memory_entity.Owner `json:"owner"`
	Summary     string              `json:"summary"`
	Detail      string              `json:"detail"`
	Pinned      bool                `json:"pinned"`
	State       memory_entity.State `json:"state"`
	HasPrevious bool                `json:"hasPrevious"`
	PrevSummary string              `json:"prevSummary"`
	PrevDetail  string              `json:"prevDetail"`
	Creator     memory_entity.Actor `json:"creator"`
	// CreatorSessionExists AI 创建者的来源会话在本机是否还在（软删算不在）。记忆本身的
	// 来源不随会话删除而改，只是来源链接据此不可点；用户写入的恒为 false。
	CreatorSessionExists bool      `json:"creatorSessionExists"`
	LastChange           ChangeDTO `json:"lastChange"`
	Createtime           int64     `json:"createtime"`
	Updatetime           int64     `json:"updatetime"`
}

// OwnerList 记忆页选中某个归属时的右侧内容。UsedChars 是全部正常条目的摘要字数，
// 可以超过 Budget（超出时界面变提醒色）；Carried / OverBudget 是按预算取舍的两组。
// MemoryEnabled 只对 agent 归属有意义。
type OwnerList struct {
	Owner         memory_entity.Owner `json:"owner"`
	Budget        int                 `json:"budget"`
	UsedChars     int                 `json:"usedChars"`
	Carried       []*MemoryItem       `json:"carried"`
	OverBudget    []*MemoryItem       `json:"overBudget"`
	Archived      []*MemoryItem       `json:"archived"`
	MemoryEnabled bool                `json:"memoryEnabled"`
}

// OwnerCount 某个归属下的正常条目数（记忆页左列）。
type OwnerCount struct {
	Owner memory_entity.Owner `json:"owner"`
	Count int64               `json:"count"`
}

// VisibleLayer 会话可见的一层：本次带上的与超出预算的正常条目。
type VisibleLayer struct {
	Owner      memory_entity.Owner `json:"owner"`
	Budget     int                 `json:"budget"`
	Carried    []*MemoryItem       `json:"carried"`
	OverBudget []*MemoryItem       `json:"overBudget"`
}

// VisibleMemory 一个会话看得到的全部层，顺序固定：全局、部门（从最上级到所在部门）、
// agent、项目（有项目时）。没有条目的层也列出。
type VisibleMemory struct {
	Layers []*VisibleLayer `json:"layers"`
}

func toItem(m *memory_entity.Memory) *MemoryItem {
	change := m.LastChange()
	return &MemoryItem{
		ID:          m.ID,
		Owner:       m.Owner(),
		Summary:     m.Summary,
		Detail:      m.Detail,
		Pinned:      m.Pinned,
		State:       m.State,
		HasPrevious: m.HasPrevious(),
		PrevSummary: m.PrevSummary,
		PrevDetail:  m.PrevDetail,
		Creator:     m.Creator(),
		LastChange:  ChangeDTO{Action: change.Action, Actor: change.Actor, At: change.At},
		Createtime:  m.Createtime,
		Updatetime:  m.Updatetime,
	}
}

func toItems(rows []*memory_entity.Memory) []*MemoryItem {
	out := make([]*MemoryItem, 0, len(rows))
	for _, m := range rows {
		out = append(out, toItem(m))
	}
	return out
}

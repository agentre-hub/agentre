package memory_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/memory_repo"
)

// WriteAction 对话里记忆卡片代表的那次 AI 写入。取值来自 agrctl 结果行的动词
// （created / updated / archived），"already exists" 没有改动，不会走到这里。
type WriteAction string

const (
	WriteCreated  WriteAction = "created"
	WriteUpdated  WriteAction = "updated"
	WriteArchived WriteAction = "archived"
)

// UndoStatus 一次撤销的结局。
type UndoStatus string

const (
	// UndoDone 已撤销。
	UndoDone UndoStatus = "undone"
	// UndoChanged 条目在这次写入之后又被改过，没有动它。
	UndoChanged UndoStatus = "changed"
	// UndoGone 条目已经不存在（新增的已被删掉等）。
	UndoGone UndoStatus = "gone"
)

// UndoWriteRequest 撤销会话 SessionID 里对条目 ID 做的那次 Action 写入。
//
// 「之后又被改过」的判据是条目的最近一次变更：必须仍是这个会话里的 AI 做的、与 Action
// 对应的那种变更（created / edited / archived）。agrctl 的结果行没有版本号或时间，对话卡片
// 手里也没有可靠的写入时刻，所以不用时间比较；变更动作加会话是现有信息里最强的判据。
// 局限：同一会话对同一条目连续做两次同种写入（例如改两次）时，较早那张卡的撤销也会通过。
type UndoWriteRequest struct {
	ID        int64       `json:"id"`
	SessionID int64       `json:"sessionId"`
	Action    WriteAction `json:"action"`
}

// UndoWriteResult Item 只在 UndoDone 且条目还在（修改、归档的撤销）时有值。
type UndoWriteResult struct {
	Status UndoStatus  `json:"status"`
	Item   *MemoryItem `json:"item,omitempty"`
}

// 写入动作对应的变更动作。
var writeChange = map[WriteAction]memory_entity.ChangeAction{
	WriteCreated:  memory_entity.ActionCreated,
	WriteUpdated:  memory_entity.ActionEdited,
	WriteArchived: memory_entity.ActionArchived,
}

// Get 按编号读一条记忆；不存在返回 nil（不是错误：对话卡片据此知道它已被撤销）。
func (s *memorySvc) Get(ctx context.Context, id int64) (*MemoryItem, error) {
	m, err := memory_repo.Memory().Find(ctx, id)
	if err != nil || m == nil {
		return nil, err
	}
	return toItem(m), nil
}

func (s *memorySvc) UndoWrite(ctx context.Context, req *UndoWriteRequest) (*UndoWriteResult, error) {
	want, ok := writeChange[req.Action]
	if !ok || req.SessionID <= 0 {
		return nil, i18n.NewError(ctx, code.MemoryInvalidActor)
	}
	m, err := memory_repo.Memory().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return &UndoWriteResult{Status: UndoGone}, nil
	}
	last := m.LastChange()
	if last.Action != want || last.Actor.Kind != memory_entity.ActorAI || last.Actor.SessionID != req.SessionID {
		return &UndoWriteResult{Status: UndoChanged}, nil
	}

	user := memory_entity.UserActor()
	switch req.Action {
	case WriteCreated:
		if err := s.Delete(ctx, req.ID); err != nil {
			return nil, err
		}
		return &UndoWriteResult{Status: UndoDone}, nil
	case WriteUpdated:
		item, err := s.RestorePrevious(ctx, req.ID, user)
		if err != nil {
			return nil, err
		}
		return &UndoWriteResult{Status: UndoDone, Item: item}, nil
	default:
		item, err := s.Unarchive(ctx, req.ID, user)
		if err != nil {
			return nil, err
		}
		return &UndoWriteResult{Status: UndoDone, Item: item}, nil
	}
}

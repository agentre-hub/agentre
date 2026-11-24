package subagent_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// dispatchHandle 是一次派发登记的句柄;没登记(无端口 / 登记失败)时所有操作都是空操作。
type dispatchHandle struct {
	rec chat_svc.DispatchRecorder
	id  int64
}

// beginDispatch 在起轮之前登记这次 agent 派发(method=agent_call,子会话恒隔离)。
func (s *subagentSvc) beginDispatch(ctx context.Context, dispatcherSessionID, dispatchedSessionID int64, task string) dispatchHandle {
	if s.dispatches == nil {
		return dispatchHandle{}
	}
	id := s.dispatches.Begin(ctx, chat_svc.DispatchBegin{
		DispatcherSessionID: dispatcherSessionID,
		DispatchedSessionID: dispatchedSessionID,
		Method:              chat_svc.DispatchMethodAgentCall,
		Task:                task,
		Isolated:            true,
	})
	if id <= 0 {
		return dispatchHandle{}
	}
	return dispatchHandle{rec: s.dispatches, id: id}
}

func (d dispatchHandle) started(ctx context.Context) {
	if d.rec != nil {
		d.rec.Started(ctx, d.id)
	}
}

func (d dispatchHandle) finish(ctx context.Context, outcome chat_svc.DispatchOutcome) {
	if d.rec != nil {
		d.rec.Finish(ctx, d.id, outcome)
	}
}

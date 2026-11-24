package ctl_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// RegisterDispatchRecorder 装上派发关系登记端口(生产传 chat_svc.Dispatch(),须在 chat_svc 接线之后);
// 没装时 send 照常执行,只是不登记派发关系。
func (s *ctlSvc) RegisterDispatchRecorder(r chat_svc.DispatchRecorder) {
	s.mu.Lock()
	s.dispatches = r
	s.mu.Unlock()
}

// dispatchHandle 是一次派发登记的句柄;没登记(无端口 / 握手 token / 登记失败)时所有操作都是空操作。
type dispatchHandle struct {
	rec chat_svc.DispatchRecorder
	id  int64
}

// beginDispatch 在起轮之前登记这次 send。只有会话 token 才知道派发方是谁:握手 token
// (人 / 外部脚本)发起的 send 没有派发方会话,不登记。
func (h *ctlHandler) beginDispatch(ctx context.Context, cred credential, req sendRequest, dispatchedSessionID int64) dispatchHandle {
	if h.dispatches == nil || !cred.session {
		return dispatchHandle{}
	}
	id := h.dispatches.Begin(ctx, chat_svc.DispatchBegin{
		DispatcherSessionID: cred.ref.SessionID,
		DispatchedSessionID: dispatchedSessionID,
		Method:              chat_svc.DispatchMethodAgrctlSend,
		Task:                req.Text,
		Isolated:            req.Isolated,
	})
	if id <= 0 {
		return dispatchHandle{}
	}
	return dispatchHandle{rec: h.dispatches, id: id}
}

func (d dispatchHandle) recorded() bool { return d.rec != nil }

func (d dispatchHandle) started(ctx context.Context) {
	if d.recorded() {
		d.rec.Started(ctx, d.id)
	}
}

func (d dispatchHandle) finish(ctx context.Context, outcome chat_svc.DispatchOutcome) {
	if d.recorded() {
		d.rec.Finish(ctx, d.id, outcome)
	}
}

// track 盯着不带 --wait 的那一轮,收尾时把派发行落成终态,最后释放订阅。
func (d dispatchHandle) track(ctx context.Context, turnCh <-chan chat_svc.TurnResult, release func()) {
	defer release()
	res := <-turnCh
	d.finish(ctx, chat_svc.DispatchOutcomeOfTurn(res))
}

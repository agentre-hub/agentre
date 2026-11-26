package chat_svc

import "context"

// RunStateEvent 是「这台桌面端上某条会话开始运行 / 不再运行」在 Wails 事件总线上的
// 名字,载荷是会话的本地 id(int64)。
//
// 「在运行」与 ListSessionsSharingRoot 的候选同一口径:库里 agent_status 为
// running / waiting,或后台活动集合非空。它在这个集合可能变化的每一处发一次:
//   - 轮次边界的四个发布点(publishPeerTurnStarted / Ended、自主续轮的 Started /
//     Ended,见 peer_session.go)—— 用户轮、compact、派发轮(agrctl send、agent 派发、
//     对端发起)与自主续轮全部经过这里,Ended 在会话行落成终态之后才调;
//   - 后台活动集合在空与非空之间翻转(bg_running.go);
//   - 停止把一条没有轮次收尾的会话翻回 idle(reconcileSessionToIdle);
//   - 删除会话,软删落库之后(Delete)。
//
// 会话级旁路流(chat:autonomous:<id>)只有挂着这条会话对话面板的地方才听;关联窗要
// 知道**别的**会话开始 / 停下,而那条会话未必开着面板 —— 这条应用级事件就是给它的。
const RunStateEvent = "chat:run-state"

// emitRunStateChanged 发一次 RunStateEvent。没接 emitter 的裸 chatSvc(单测)不发。
func (s *chatSvc) emitRunStateChanged(sessionID int64) {
	if s.emitter == nil || sessionID <= 0 {
		return
	}
	s.emitter.Emit(context.Background(), RunStateEvent, sessionID)
}

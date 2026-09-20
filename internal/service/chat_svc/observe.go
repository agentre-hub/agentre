package chat_svc

import "sync"

// TurnResult 一个 turn 的终态(服务端观察口产出, 不经 Wails)。
// 只承载生命周期信号, 不含消息文本。
type TurnResult struct {
	SessionID          int64
	AssistantMessageID int64
	Aborted            bool
	Err                error
}

func (s *chatSvc) ensureObservers() *sync.Map {
	if s.turnObservers == nil {
		s.turnObservers = &sync.Map{}
	}
	return s.turnObservers
}

// ObserveTurn 订阅指定 session 下一次 turn 的终态。返回只读 channel + 取消函数。
// channel 带缓冲(1), publish 非阻塞; 调用方收到一条后应 cancel()。
// 订阅必须发生在 turn 起点之前(调度方先 ObserveTurn 再 Send), 否则快 turn 的回执会丢。
func (s *chatSvc) ObserveTurn(sessionID int64) (<-chan TurnResult, func()) {
	ch := make(chan TurnResult, 1)
	obs := s.ensureObservers()
	raw, _ := obs.LoadOrStore(sessionID, &sync.Map{})
	set := raw.(*sync.Map)
	set.Store(ch, struct{}{})
	cancel := func() { set.Delete(ch) }
	return ch, cancel
}

// publishTurnResult 向某 session 的所有订阅者非阻塞推送一条终态。
func (s *chatSvc) publishTurnResult(sessionID int64, r TurnResult) {
	if s.turnObservers == nil {
		return
	}
	raw, ok := s.turnObservers.Load(sessionID)
	if !ok {
		return
	}
	set := raw.(*sync.Map)
	set.Range(func(k, _ any) bool {
		ch := k.(chan TurnResult)
		select {
		case ch <- r:
		default: // 缓冲满(订阅方没收) → 丢弃, 不阻塞 turn
		}
		return true
	})
}

// SubscribeSessionEvents 见 ChatSvc:转发给外层 fanout emitter(若有装配)。
// 未装配扇出时返回一个已关闭的通道 + no-op 取消,调用方(SSE handler)不会挂住。
func (s *chatSvc) SubscribeSessionEvents(sessionID int64) (<-chan ChatStreamEvent, func()) {
	if s.sessionEvents == nil {
		ch := make(chan ChatStreamEvent)
		close(ch)
		return ch, func() {}
	}
	return s.sessionEvents.SubscribeSessionEvents(sessionID)
}

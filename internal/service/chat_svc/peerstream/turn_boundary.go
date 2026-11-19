package peerstream

import (
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

// queuedNotification 是订阅者队列里的一项:一条通知的方法名与它的参数。
type queuedNotification struct {
	method string
	params any
}

// 轮次边界帧:开轮(runtime.turnStarted / runtime.autonomousTurn.started)与终态
// (runtime.runResultDone / runtime.autonomousTurn.done)。
//
// 对端判「这一轮开始了 / 结束了」只认这几帧:浏览器控制台据终态帧放下停止键与三点,
// server 的账号镜像据它们把那一行在 running 与 idle / failed 之间翻。agentred 在
// handlers.RuntimeHandlers 里发它们,桌面端承载时此前一帧都不发 —— 派到桌面端跑的
// 一轮在对端因此永远是「进行中」(2026-11-19 线上)。
//
// 它们与持久帧同排一条订阅者队列,而且排在 publishMu 下:一次正在取号的持久帧发布
// 先完成入队,终态帧才排进去,对端不会先收场、再收到半截正文。
//
// 它们**不受**队列封顶约束:持久帧丢了能靠 seq 跳号补齐,边界帧不带号、不进日志,
// 丢了就再也补不回来。每轮只有一两帧,不封顶也撑不爆内存。
//
// 会话身份由 publication 盖上(调用方传进来的 ConversationID 一律被覆盖):它是这份
// 编号宇宙建立时就定死的那一个,调用方手里的会话行未必带着它。
//
// 没有订阅者(从没人 attach 过、或都已离开)时什么也不做,也不为此建 publication。

// PublishTurnStarted 发一条 runtime.turnStarted:一轮用户发起的轮次开始了。
func (s *Publisher) PublishTurnStarted(sessionID int64) {
	s.publishBoundary(sessionID, func(conversationID string) (string, any) {
		return wire.NotifyTurnStarted, wire.TurnStartedFrame{ConversationID: conversationID}
	})
}

// PublishRunResultDone 发一条 runtime.runResultDone:这一轮结束了。调用方必须先把
// 会话行落成终态再调它 —— 对端收到后立刻查清单时,看到的必须已经是终态(与 agentred
// 的 fanout 同一条次序保证)。
func (s *Publisher) PublishRunResultDone(sessionID int64, frame wire.RunResultDoneFrame) {
	s.publishBoundary(sessionID, func(conversationID string) (string, any) {
		frame.ConversationID = conversationID
		return wire.NotifyRunResultDone, frame
	})
}

// PublishAutonomousTurnStarted 发一条 runtime.autonomousTurn.started:后端自己续起了一轮。
func (s *Publisher) PublishAutonomousTurnStarted(sessionID int64, trigger string) {
	s.publishBoundary(sessionID, func(conversationID string) (string, any) {
		return wire.NotifyAutonomousTurnStarted, wire.AutonomousTurnStartedFrame{ConversationID: conversationID, Trigger: trigger}
	})
}

// PublishAutonomousTurnDone 发一条 runtime.autonomousTurn.done,次序要求同 PublishRunResultDone。
func (s *Publisher) PublishAutonomousTurnDone(sessionID int64, frame wire.RunResultDoneFrame) {
	s.publishBoundary(sessionID, func(conversationID string) (string, any) {
		frame.ConversationID = conversationID
		return wire.NotifyAutonomousTurnDone, frame
	})
}

func (s *Publisher) publishBoundary(sessionID int64, build func(conversationID string) (string, any)) {
	if sessionID <= 0 {
		return
	}
	value, ok := s.peerPublications.Load(sessionID)
	if !ok {
		return
	}
	publication := value.(*peerSessionPublication)
	publication.publishMu.Lock()
	defer publication.publishMu.Unlock()

	publication.mu.Lock()
	if publication.closed || len(publication.subscribers) == 0 {
		publication.mu.Unlock()
		return
	}
	method, params := build(publication.conversationID)
	// 攒着的预览文本先于边界帧排队,与持久帧同一条纪律。
	s.flushPreviewLocked(publication)
	for _, sub := range publication.subscribers {
		sub.pending = append(sub.pending, queuedNotification{method: method, params: params})
	}
	publication.mu.Unlock()
	publication.signal()
}

package chat_svc

import (
	"context"
	"sync"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
)

// peerNoticeHold 让轮中落库的会话设置切换 notice 等本轮收口之后再发给对端。
//
// 桌面端的 notice 是排在在跑 assistant 之后的独立一行(NextSeq),显示在整轮之后。对端
// 的顺序却是持久帧的**取号顺序**:轮中立即发布,notice 的号就落在本轮的轮内帧与收口帧
// 之间,拿帧重建转录的那两个面(浏览器控制台、桌面端 Peer Tab)会把它塞进在跑的回答,
// 再把收口帧另起一条 —— 一轮回答被劈成两段。
//
// 「本轮在跑」以对端发布本身为准:轮内任一条消息的持久帧发出即开(开轮的用户那一行、
// 轮内 checkpoint、steer 分段),收口时关 —— 两条 finalize 走 publishPeerTurnDone,
// 没走到收口的失败 / 起跑前中止由 failTurn / abortTurnBeforeStream 关,扣着的 notice
// 不会被永远压住。只认内存里的发布,与会话行上的 agent_status 无关:后者读出来的那一刻
// 轮次可能已经收口。
type peerNoticeHold struct {
	mu      sync.Mutex
	turnOn  bool
	pending []*chat_entity.Message
}

func (s *chatSvc) peerNoticeHoldFor(sessionID int64) *peerNoticeHold {
	v, _ := s.peerNoticeHolds.LoadOrStore(sessionID, &peerNoticeHold{})
	return v.(*peerNoticeHold)
}

// peerTurnFramesStarted 记下这条会话有一轮的持久帧开始发了。
func (s *chatSvc) peerTurnFramesStarted(sessionID int64) {
	hold := s.peerNoticeHoldFor(sessionID)
	hold.mu.Lock()
	hold.turnOn = true
	hold.mu.Unlock()
}

// peerTurnSettled 在本轮的收口帧发出之后调用:放行轮中扣下的 notice,按落库先后取号。
// 放行时持着锁:紧接着开起来的下一轮(自动接续)要先等这几条取完号,它的用户那一行
// 才不会抢到 notice 前面。
func (s *chatSvc) peerTurnSettled(ctx context.Context, sessionID int64) {
	hold := s.peerNoticeHoldFor(sessionID)
	hold.mu.Lock()
	defer hold.mu.Unlock()
	hold.turnOn = false
	for _, msg := range hold.pending {
		_, _ = s.peerStream().PublishMessageFrames(context.WithoutCancel(ctx), sessionID, msg, true)
	}
	hold.pending = nil
}

// publishSessionNotice 发布一条已落库的会话设置切换 notice:空闲时立即发,轮中扣到
// 本轮收口之后。
func (s *chatSvc) publishSessionNotice(ctx context.Context, sessionID int64, msg *chat_entity.Message) {
	hold := s.peerNoticeHoldFor(sessionID)
	hold.mu.Lock()
	if hold.turnOn {
		hold.pending = append(hold.pending, msg)
		hold.mu.Unlock()
		return
	}
	hold.mu.Unlock()
	_, _ = s.peerStream().PublishMessageFrames(ctx, sessionID, msg, true)
}

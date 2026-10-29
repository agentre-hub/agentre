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
// 扣留按**轮**记账,键是这一轮的流名(StreamName:插话分段与自动接续沿用同一个流名,
// 自主续轮各有各的):一条会话上可以同时有两轮(用户发起的那一轮排在自主续轮后面),
// 一轮收口只关它自己那一格,仍有别的轮在跑时 notice 继续扣着。
//
// 开轮点是这一轮已经在跑、此后的切换在桌面端必然排在它之后的那一刻:用户发起的轮在
// 用户那一行发出时(emitTurnStarted)、任何一轮进 runTurn 时(compact 与自动接续没有
// 前者)、自动接续的那一行落库之后、自主续轮宣告开轮时(emitStarted,assistant 行已
// 落库)。收口点:两条 finalize 走 publishPeerTurnDone,没走到收口的失败 / 起跑前中止
// 由 failTurn / abortTurnBeforeStream 关,runTurn 退出时(含被吞掉的 panic)再兜底关一次
// —— 扣着的 notice 不会被永远压住。只认内存里的开轮点,与会话行上的 agent_status
// 无关:后者读出来的那一刻轮次可能已经收口。
type peerNoticeHold struct {
	mu      sync.Mutex
	open    map[string]struct{}
	pending []*chat_entity.Message
}

func (s *chatSvc) peerNoticeHoldFor(sessionID int64) *peerNoticeHold {
	v, _ := s.peerNoticeHolds.LoadOrStore(sessionID, &peerNoticeHold{})
	return v.(*peerNoticeHold)
}

// peerTurnOpened 记下这条会话上 stream 那一轮在跑:之后落库的切换 notice 扣到它收口
// 之后。同一轮重复开轮是幂等的。
func (s *chatSvc) peerTurnOpened(sessionID int64, stream string) {
	hold := s.peerNoticeHoldFor(sessionID)
	hold.mu.Lock()
	if hold.open == nil {
		hold.open = make(map[string]struct{})
	}
	hold.open[stream] = struct{}{}
	hold.mu.Unlock()
}

// peerTurnSettled 在 stream 那一轮的收口帧发出之后调用。会话上再没有在跑的轮时放行
// 扣下的 notice,按落库先后取号。放行时持着锁:紧接着开起来的下一轮(自动接续)要先
// 等这几条取完号,它的用户那一行才不会抢到 notice 前面。
func (s *chatSvc) peerTurnSettled(ctx context.Context, sessionID int64, stream string) {
	hold := s.peerNoticeHoldFor(sessionID)
	hold.mu.Lock()
	defer hold.mu.Unlock()
	delete(hold.open, stream)
	if len(hold.open) > 0 {
		return
	}
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
	if len(hold.open) > 0 {
		hold.pending = append(hold.pending, msg)
		hold.mu.Unlock()
		return
	}
	hold.mu.Unlock()
	_, _ = s.peerStream().PublishMessageFrames(ctx, sessionID, msg, true)
}

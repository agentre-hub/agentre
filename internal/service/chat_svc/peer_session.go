package chat_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/peerstream"
)

// peer_session.go 是 chat_svc 到 peerstream 的接线:那份「每条会话唯一有序的通知
// 宇宙」住在兄弟包里,这里只留两样宿主侧的东西 —— 对端 RPC 入口的转交,以及轮内
// 三个发布点的私有薄封装(名字不变,轮的事件循环与两条收口路径照旧调它们)。

// PeerSessionSubscriber 是已鉴权账号对端注册进来的通知出口。
type PeerSessionSubscriber = peerstream.PeerSessionSubscriber

// PeerSessionSubscriberKeyer 让同一条连接在 attach 与 pull 两次调用间有稳定身份。
type PeerSessionSubscriberKeyer = peerstream.PeerSessionSubscriberKeyer

var (
	// ErrPeerSessionNotFound 见 peerstream。
	ErrPeerSessionNotFound = peerstream.ErrPeerSessionNotFound
	// ErrPeerSessionMetadata 见 peerstream。
	ErrPeerSessionMetadata = peerstream.ErrPeerSessionMetadata
	// ErrPeerSessionInvalidID 见 peerstream。
	ErrPeerSessionInvalidID = peerstream.ErrPeerSessionInvalidID
)

// ResolvePeerConversation 把线上对话身份反查成本机会话主键。
func ResolvePeerConversation(ctx context.Context, conversationID string) (int64, error) {
	return peerstream.ResolvePeerConversation(ctx, conversationID)
}

// peerStream 惰性构造这台桌面端的对端出口。惰性是必需的:不少单测直接字面量构造
// chatSvc,拿不到 NewChat 的构造时机(与 goals() / remotePool() 同一理由)。
func (s *chatSvc) peerStream() *peerstream.Publisher {
	s.peerStreamOnce.Do(func() { s.peerStreamImpl = peerstream.New() })
	return s.peerStreamImpl
}

func (s *chatSvc) ListPeerSessions(ctx context.Context, params wire.SessionListParams) (*wire.SessionListResult, error) {
	return s.peerStream().ListPeerSessions(ctx, params)
}

func (s *chatSvc) CountPeerSessions(ctx context.Context) (*wire.SessionCountsResult, error) {
	return s.peerStream().CountPeerSessions(ctx)
}

func (s *chatSvc) AttachPeerSession(
	ctx context.Context, params wire.SessionAttachParams, subscriber PeerSessionSubscriber,
) (wire.SessionAttachResult, error) {
	return s.peerStream().AttachPeerSession(ctx, params, subscriber)
}

func (s *chatSvc) PullPeerSession(
	ctx context.Context, params wire.SessionPullParams, subscriber PeerSessionSubscriber,
) (wire.SessionPullResult, error) {
	return s.peerStream().PullPeerSession(ctx, params, subscriber)
}

// publishPeerEvent / publishPeerMessageFrames / publishPeerTurnDone 是轮内那三个
// 发布点。保留私有薄封装(而不是让事件循环直接写 s.peerStream().Publish…)是为了让
// 「扇出必须发生在本地 Apply 之前」「两条收口路径都要发 turn done」这两条 AST 守卫
// 仍然守在调用点上 —— 它们认的是这三个名字。
func (s *chatSvc) publishPeerEvent(sessionID int64, event agentruntime.Event) {
	s.peerStream().PublishEvent(sessionID, event)
}

func (s *chatSvc) publishPeerMessageFrames(
	ctx context.Context, sessionID int64, msg *chat_entity.Message, userTurn bool,
) (minSeq int64, maxSeq int64) {
	return s.peerStream().PublishMessageFrames(ctx, sessionID, msg, userTurn)
}

func (s *chatSvc) publishPeerTurnDone(ctx context.Context, sessionID int64, msg *chat_entity.Message) {
	s.peerStream().PublishTurnDone(ctx, sessionID, msg)
}

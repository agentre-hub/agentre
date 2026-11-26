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

// stream 是收口的这一轮的流名:它扣下的切换 notice 随之放行(见 peer_notice_hold.go)。
func (s *chatSvc) publishPeerTurnDone(ctx context.Context, sessionID int64, stream string, msg *chat_entity.Message) {
	s.peerStream().PublishTurnDone(ctx, sessionID, msg)
	s.peerTurnSettled(ctx, sessionID, stream)
}

// 轮次边界帧的四个发布点(见 peerstream/turn_boundary.go)。对端判「这一轮开始了 /
// 结束了」只认它们 —— 浏览器控制台据终态帧放下停止键,server 的账号镜像据它们翻
// 那一行的生命周期。agentred 一直发,桌面端承载时此前一帧都不发。
//
// 两只「Ended」必须在会话行**落成终态之后**调:对端收到终态帧会立刻查清单,那时看到
// 的必须已经是终态(agentred 的 fanout 同样先 settleSession 再发帧)。AST 守卫认的是
// 这几个名字。
//
// 同一时刻本机也要知道「这条会话开始 / 不再运行」(关联窗的「同一份代码上在跑」),
// 四个发布点因此各自再发一次应用级的 RunStateEvent(见 run_state_event.go)。

// publishPeerTurnStarted 在一轮用户发起的轮次(含 compact)开起来时调。
func (s *chatSvc) publishPeerTurnStarted(sessionID int64) {
	s.peerStream().PublishTurnStarted(sessionID)
	s.emitRunStateChanged(sessionID)
}

// publishPeerTurnEnded 在一轮用户发起的轮次收场时调。stopErr 为 nil 是正常结束,
// agentruntime.ErrAborted 是用户点了停止,其余是故障。
func (s *chatSvc) publishPeerTurnEnded(sessionID int64, msg *chat_entity.Message, stopErr error) {
	s.peerStream().PublishRunResultDone(sessionID, peerTurnEndFrame(msg, stopErr))
	s.emitRunStateChanged(sessionID)
}

// publishPeerAutonomousTurnStarted 在后端自己续起的一轮宣告开轮时调。
func (s *chatSvc) publishPeerAutonomousTurnStarted(sessionID int64, trigger string) {
	s.peerStream().PublishAutonomousTurnStarted(sessionID, trigger)
	s.emitRunStateChanged(sessionID)
}

// publishPeerAutonomousTurnEnded 在自主续轮收场时调,stopErr 口径同 publishPeerTurnEnded。
func (s *chatSvc) publishPeerAutonomousTurnEnded(sessionID int64, msg *chat_entity.Message, stopErr error) {
	s.peerStream().PublishAutonomousTurnDone(sessionID, peerTurnEndFrame(msg, stopErr))
	s.emitRunStateChanged(sessionID)
}

// peerTurnEndFrame 把收场的那条 assistant 消息折成终态帧。
//
// 帧上的模型 / 用量 / 计时与 agentred 那一份同口径(都出自 internal/pkg/turnstats),
// 对端拿它们画 meta 那一行。零值一律不带:对端把 0 读作「有这个数,值是 0」。
//
// 停止原因的码由 wire.ToRPCError 给:用户点的停止(ErrAborted)映射成 ErrCodeAborted,
// 对端据此画「已中断」而不是故障(turnstate.IsFailure)。
func peerTurnEndFrame(msg *chat_entity.Message, stopErr error) wire.RunResultDoneFrame {
	var frame wire.RunResultDoneFrame
	if msg != nil {
		frame.Model = msg.Model
		frame.DurationMs = msg.DurationMs
		frame.FirstTokenMs = msg.FirstTokenMs
		frame.TokensPerSec = msg.TokensPerSec
		usage := wire.UsageWire{
			PromptTokens:        msg.PromptTokens,
			CompletionTokens:    msg.CompletionTokens,
			ReasoningTokens:     msg.ReasoningTokens,
			CachedTokens:        msg.CachedTokens,
			CacheCreationTokens: msg.CacheCreationTokens,
		}
		if usage != (wire.UsageWire{}) {
			frame.Usage = &usage
		}
	}
	if stopErr != nil {
		frame.StopErrMsg = stopErr.Error()
		if rpcErr := wire.ToRPCError(stopErr); rpcErr != nil {
			frame.StopErrCode = int(rpcErr.Code)
		}
	}
	return frame
}

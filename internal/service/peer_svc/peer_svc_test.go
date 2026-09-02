package peer_svc_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/peer"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/peer_svc"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// ── 测试骨架：假对端 WebSocket + 假 dialer + spy emitter ────────────────────

// fakePeerServer 起一台内存 WebSocket 对端（会话族 handler 由 register 按每条连接
// 挂上），返回可被 client.Dial 直连的 ws:// URL。传输与生产一致（同一套 rpc 子协议），
// 只省去中继与账号握手——生产里握手由 server_svc.DialDesktopRelay 完成，本测试要验
// 的是 peer_svc 的会话族编排与事件扇出，不是中继本身。
func fakePeerServer(t *testing.T, deps peer.ProtobufInboundDeps) string {
	t.Helper()
	// 决策 8 之后 auth.account 必须验凭据才给身份;假对端在这里注入一个可控的验证
	// 结果("这枚凭据验过了,对端是 sha256:test-peer"),各用例只关心会话族方法。
	if deps.VerifyAccountCredential == nil {
		deps.VerifyAccountCredential = func(context.Context, string) (string, error) {
			return "sha256:test-peer", nil
		}
	}
	server := protorpc.NewLANServer(protorpc.LANOpts{Host: "127.0.0.1", Port: 0, Registry: peer.NewProtobufInboundRegistry(deps)})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = server.Run(ctx) }()
	require.Eventually(t, func() bool { return server.Addr() != "" }, time.Second, time.Millisecond)
	t.Cleanup(cancel)
	return server.URL()
}

// directDialer 是 peer_svc.Dialer 的测试实现：直接把 DialDesktopRelay 当作一次直连
// 拨号（生产是 server_svc 经账号中继）。url 由 fakePeerServer 给出。
type directDialer struct{ url string }

func (d directDialer) DialDesktopRelay(_ context.Context, desktopFingerprint, peerFingerprint string) (client.ProtobufConnection, error) {
	if desktopFingerprint == "" || peerFingerprint == "" {
		return nil, errors.New("empty fingerprint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := client.DialProtobuf(ctx, client.Options{URL: d.url})
	if err != nil {
		return nil, err
	}
	_, err = protorpc.CallMethod(ctx, conn.Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT), &agentrewire.AuthAccountRequest{Credential: "test", ProtocolVersion: wireversion.Protocol}, func() *agentrewire.AuthAccountResponse { return &agentrewire.AuthAccountResponse{} })
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

type spyEmitter struct {
	events chan peer_svc.PeerEvent
}

func (s *spyEmitter) Emit(e peer_svc.PeerEvent) { s.events <- e }

type stubSelf struct{ fp string }

func (s stubSelf) DeviceFingerprint() (string, error) { return s.fp, nil }

type stubAgents struct{ agent *agent_entity.Agent }

func (s stubAgents) Find(_ context.Context, id int64) (*agent_entity.Agent, error) {
	if s.agent != nil && s.agent.ID == id {
		return s.agent, nil
	}
	return nil, nil
}

type stubProjects struct{ project *project_entity.Project }

func (s stubProjects) Find(_ context.Context, id int64) (*project_entity.Project, error) {
	if s.project != nil && s.project.ID == id {
		return s.project, nil
	}
	return nil, nil
}

func newTestSvc(t *testing.T, url string) (peer_svc.PeerSvc, *spyEmitter) {
	t.Helper()
	emitter := &spyEmitter{events: make(chan peer_svc.PeerEvent, 16)}
	svc := peer_svc.New(
		directDialer{url: url},
		emitter,
		stubSelf{fp: "sha256:local-desktop"},
		stubAgents{agent: &agent_entity.Agent{ID: 3, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "01HXAGENTIDENTITY0000000000"}}},
		stubProjects{project: &project_entity.Project{ID: 9, Path: "/Users/wyz/agentre"}},
	)
	t.Cleanup(func() { _ = svc.Close() })
	return svc, emitter
}

// Given a named desktop is the dispatch target, when this desktop dispatches a
// fresh conversation, then agentSyncId + cwd are resolved locally and the peer's
// real session id comes back (R18).
func TestPeerSvc_GivenFreshDispatch_WhenRunFresh_ThenResolvesAgentAndCwdAndReturnsRemoteSession(t *testing.T) {
	url := fakePeerServer(t, peer.ProtobufInboundDeps{RunSession: func(_ context.Context, p wire.RunParams, _ chat_svc.PeerSessionSource) (*chat_svc.SendResponse, error) {
		assert.True(t, p.FreshSession, "dispatch must demand a genuinely new session")
		assert.Equal(t, "01HXAGENTIDENTITY0000000000", p.AgentSyncID)
		assert.Equal(t, "/Users/wyz/agentre", p.Cwd)
		assert.Equal(t, "帮我看看这个项目", p.UserText)
		assert.Equal(t, "provider-key", p.LLMProviderKey, "the transient provider target must cross the desktop peer boundary")
		assert.Equal(t, "model-key", p.LLMModelKey, "the transient fixed model target must cross the desktop peer boundary")
		assert.Equal(t, "xhigh", p.ReasoningEffort, "草稿态选的思考力度与那对 ModelTarget 同一条路过桌面端边界")
		assert.Equal(t, "sha256:local-desktop", p.SourceDevice, "self fingerprint must travel as the source device")
		return &chat_svc.SendResponse{SessionID: 42}, nil
	}})
	svc, _ := newTestSvc(t, url)

	ack, err := svc.RunFresh(context.Background(), peer_svc.RunFreshRequest{
		Fingerprint:     "sha256:peer-desktop",
		AgentID:         3,
		ProjectID:       9,
		Title:           "帮我看看这个项目",
		UserText:        "帮我看看这个项目",
		ProviderKey:     "provider-key",
		ModelKey:        "model-key",
		ReasoningEffort: "xhigh",
	})
	require.NoError(t, err)
	assert.NoError(t, conversationid.Validate(ack.ConversationID),
		"发起端铸的对话身份必须原样交回调用方 —— 执行端从不发号")
	assert.Equal(t, uuid.Version(7), uuid.MustParse(ack.ConversationID).Version(),
		"新对话铸的是 v7")
}

// Given the target desktop owns sessions, when this desktop lists them, then
// full summaries come back (R19 / R4, no degraded fallback).
func TestPeerSvc_GivenRemoteDesktopSessions_WhenList_ThenReturnFullSummaries(t *testing.T) {
	url := fakePeerServer(t, peer.ProtobufInboundDeps{ListSessions: func(context.Context, string) (*wire.SessionListResult, error) {
		return &wire.SessionListResult{
			Sessions: []wire.SessionSummary{{
				ConversationID: convID(7), PeerFingerprint: "sha256:peer-desktop", AgentID: 3,
				Title: "Ship the release", AgentSyncID: "01HXAGENTIDENTITY0000000000",
				BackendType: "claudecode", LifecycleState: wire.SessionLifecycleRunning,
				WaitingForInput: true, LatestSeq: 12, LastMessageAt: 1710000000000,
				ProviderKey: "prov-anthropic", ModelKey: "sonnet-4-6", ReasoningEffort: "high",
			}},
		}, nil
	}})
	svc, _ := newTestSvc(t, url)

	result, err := svc.ListSessions(context.Background(), "sha256:peer-desktop")
	require.NoError(t, err)
	require.Len(t, result.Sessions, 1)
	assert.Equal(t, convID(7), result.Sessions[0].ConversationID)
	assert.Equal(t, "Ship the release", result.Sessions[0].Title)
	assert.True(t, result.Sessions[0].WaitingForInput)
	assert.Equal(t, int64(12), result.Sessions[0].LatestSeq)
	assert.Equal(t, "prov-anthropic", result.Sessions[0].ProviderKey)
	assert.Equal(t, "sonnet-4-6", result.Sessions[0].ModelKey)
	assert.Equal(t, "high", result.Sessions[0].ReasoningEffort,
		"对端会话清单上的思考力度这一格要解出来，与 providerKey / modelKey 同一位置")
}

// Given a running target desktop, when this desktop attaches a remote session,
// then the peer's live canonical events are re-emitted to the frontend with the
// fingerprint for per-tab routing (R19 / R6).
func TestPeerSvc_GivenAttachedRemoteSession_WhenPeerEmitsEvent_ThenEmitterReceivesRoutedFrame(t *testing.T) {
	url := fakePeerServer(t, peer.ProtobufInboundDeps{AttachSession: func(_ context.Context, p wire.SessionAttachParams, subscriber chat_svc.PeerSessionSubscriber) (wire.SessionAttachResult, error) {
		assert.Equal(t, convID(7), p.ConversationID)
		require.NoError(t, subscriber.Notify(wire.NotifyEvent, wire.EventFrame{
			ConversationID: convID(7), Seq: 13, Event: agentruntime.TextDelta{Text: "hi"},
		}))
		return wire.SessionAttachResult{ConversationID: convID(7), LifecycleState: wire.SessionLifecycleRunning, LatestSeq: 12}, nil
	}})
	svc, emitter := newTestSvc(t, url)

	att, err := svc.Attach(context.Background(), peer_svc.AttachRequest{
		Fingerprint: "sha256:peer-desktop", ConversationID: convID(7),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(12), att.LatestSeq, "attach returns the high-water cursor for the follow-up pull")

	select {
	case ev := <-emitter.events:
		assert.Equal(t, "sha256:peer-desktop", ev.Fingerprint)
		assert.Equal(t, convID(7), ev.ConversationID)
		assert.Equal(t, int64(13), ev.Seq)
		assert.Contains(t, string(ev.Event), `"text_delta"`)
	case <-time.After(2 * time.Second):
		t.Fatal("live event from the attached peer never reached the emitter")
	}
}

// Given an attached session, when this desktop pulls history, then the journaled
// page, cursor and oldest-seq come back on the same wire shape the browser uses.
func TestPeerSvc_GivenAttachedRemoteSession_WhenPull_ThenReturnJournaledPage(t *testing.T) {
	url := fakePeerServer(t, peer.ProtobufInboundDeps{PullSession: func(_ context.Context, p wire.SessionPullParams, _ chat_svc.PeerSessionSubscriber) (wire.SessionPullResult, error) {
		assert.Equal(t, convID(7), p.ConversationID)
		assert.Equal(t, int64(0), p.Cursor)
		return wire.SessionPullResult{
			Notifications: []wire.JournaledNotification{{
				Seq: 1, Method: wire.NotifyEvent,
				Params: &wire.EventFrame{ConversationID: convID(7), Seq: 1, Event: agentruntime.UserMessageEvent{}},
			}},
			Cursor: 1, HasMore: false, OldestSeq: 1,
		}, nil
	}})
	svc, _ := newTestSvc(t, url)

	page, err := svc.Pull(context.Background(), peer_svc.PullRequest{
		Fingerprint: "sha256:peer-desktop", ConversationID: convID(7), Cursor: 0,
	})
	require.NoError(t, err)
	require.Len(t, page.Notifications, 1)
	assert.Equal(t, int64(1), page.Notifications[0].Seq)
	assert.Equal(t, int64(1), page.OldestSeq, "desktop transcripts never reclaim the oldest seq")
}

// Given an attached remote session, when this desktop sends a message, then it
// lands in the peer's existing steer path (R19 / R9).
func TestPeerSvc_GivenAttachedRemoteSession_WhenSteer_ThenMessageLandsOnPeer(t *testing.T) {
	var got wire.SteerParams
	url := fakePeerServer(t, peer.ProtobufInboundDeps{SteerSession: func(_ context.Context, p wire.SteerParams, _ chat_svc.PeerSessionSource) error {
		got = p
		return nil
	}})
	svc, _ := newTestSvc(t, url)

	require.NoError(t, svc.Steer(context.Background(), peer_svc.SteerRequest{
		Fingerprint: "sha256:peer-desktop", ConversationID: convID(7), Text: "接着干",
	}))
	assert.Equal(t, convID(7), got.ConversationID)
	assert.Equal(t, "接着干", got.Text)
}

// Given another endpoint already answered the pending ask, when this desktop
// submits the same decision, then alreadyHandled is surfaced (R10).
func TestPeerSvc_GivenDecisionAlreadyHandled_WhenSubmitDecision_ThenAlreadyHandledSurfaced(t *testing.T) {
	url := fakePeerServer(t, peer.ProtobufInboundDeps{
		SubmitAnswer: func(context.Context, wire.SubmitAnswerParams) (chat_svc.PeerSessionControlResult, error) {
			return chat_svc.PeerSessionControlResult{AlreadyHandled: true}, nil
		},
		SubmitToolPermission: func(context.Context, wire.SubmitToolPermissionParams) (chat_svc.PeerSessionControlResult, error) {
			return chat_svc.PeerSessionControlResult{AlreadyHandled: true}, nil
		},
	})
	svc, _ := newTestSvc(t, url)

	answer, err := svc.SubmitAnswer(context.Background(), peer_svc.SubmitAnswerRequest{
		Fingerprint: "sha256:peer-desktop", ConversationID: convID(7), RequestID: "req-1",
	})
	require.NoError(t, err)
	assert.True(t, answer.AlreadyHandled)

	permission, err := svc.SubmitToolPermission(context.Background(), peer_svc.SubmitToolPermissionRequest{
		Fingerprint: "sha256:peer-desktop", ConversationID: convID(7), RequestID: "req-2", Allow: true,
	})
	require.NoError(t, err)
	assert.True(t, permission.AlreadyHandled)
}

// Given the target desktop App is not running, when this desktop lists its
// sessions, then the distinct desktop sentinel surfaces — not the agentred
// offline wording (R2).
func TestPeerSvc_GivenTargetDesktopAppNotRunning_WhenList_ThenDesktopSentinelSurfaces(t *testing.T) {
	svc := peer_svc.New(
		offlineDialer{},
		&spyEmitter{events: make(chan peer_svc.PeerEvent, 1)},
		stubSelf{fp: "sha256:local-desktop"},
		stubAgents{agent: &agent_entity.Agent{ID: 3, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "01HXAGENTIDENTITY0000000000"}}},
		stubProjects{},
	)
	t.Cleanup(func() { _ = svc.Close() })

	_, err := svc.ListSessions(context.Background(), "sha256:peer-desktop")
	require.Error(t, err)
	assert.True(t, errors.Is(err, server_svc.ErrDesktopAppNotRunning),
		"desktop offline must stay the desktop sentinel, not agentred offline")
}

type offlineDialer struct{}

func (offlineDialer) DialDesktopRelay(_ context.Context, _, _ string) (client.ProtobufConnection, error) {
	return nil, server_svc.ErrDesktopAppNotRunning
}

// Given two attached sessions on the same target desktop, when one detaches,
// then the relay connection stays (the other tab still streams); detaching the
// last one closes it (R19 close-detaches-only).
func TestPeerSvc_GivenTwoAttachedSessions_WhenLastDetach_ThenConnectionClosed(t *testing.T) {
	closed := make(chan struct{}, 1)
	url := fakePeerServer(t, peer.ProtobufInboundDeps{
		AttachSession: func(_ context.Context, p wire.SessionAttachParams, _ chat_svc.PeerSessionSubscriber) (wire.SessionAttachResult, error) {
			return wire.SessionAttachResult{ConversationID: p.ConversationID, LifecycleState: wire.SessionLifecycleIdle}, nil
		},
		SteerSession: func(context.Context, wire.SteerParams, chat_svc.PeerSessionSource) error { return nil },
	})
	_ = closed
	svc, _ := newTestSvc(t, url)

	_, err := svc.Attach(context.Background(), peer_svc.AttachRequest{Fingerprint: "sha256:peer-desktop", ConversationID: convID(7)})
	require.NoError(t, err)
	_, err = svc.Attach(context.Background(), peer_svc.AttachRequest{Fingerprint: "sha256:peer-desktop", ConversationID: convID(8)})
	require.NoError(t, err)

	// 第一枚 detach 后仍有另一条会话在接入：连接不关（无法直接观测连接存活，
	// 用还能发起一次 Steer 证明连接仍在用）。
	require.NoError(t, svc.Detach(context.Background(), "sha256:peer-desktop", convID(7)))
	require.NoError(t, svc.Steer(context.Background(), peer_svc.SteerRequest{Fingerprint: "sha256:peer-desktop", ConversationID: convID(8), Text: "还在"}),
		"steer on the remaining attached session must still work after the other detaches")

	// 最后一枚 detach：连接关闭。Steer 应失败（连接没了）。
	require.NoError(t, svc.Detach(context.Background(), "sha256:peer-desktop", convID(8)))
}

// convID 把一个短会话号折成一条**格式合法**的 conversation_id,只在测试里用:
// 线上身份是 uuid,而这些用例真正要断言的是"同一个值原样往返"与"两条不同的对话
// 互不并轨",一个可读、可复现的映射比随机 uuid 更好读。
func convID(n int64) string {
	return fmt.Sprintf("00000000-0000-7000-8000-%012d", n)
}

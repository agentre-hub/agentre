package peer_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-ai/agentre/internal/daemon/rpc"
	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-ai/agentre/internal/peer"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-ai/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-ai/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-ai/agentre/internal/repository/agent_repo"
	"github.com/agentre-ai/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-ai/agentre/internal/repository/chat_repo"
	"github.com/agentre-ai/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-ai/agentre/internal/service/chat_svc"
	"github.com/agentre-ai/agentre/internal/service/remote_device_svc"
	"github.com/agentre-ai/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

// Given a signed-in desktop App owns an inbound relay link, when the first
// physical connection drops, then it reconnects, accepts an account-authorized
// peer on the existing wire vocabulary, and disappears from the relay when the
// App lifetime ends.
func TestInbound_GivenRelayReconnectAndShutdown_WhenAuthorizedPeerCallsCapabilities_ThenItDispatchesAndUnregisters(t *testing.T) {
	var attempts atomic.Int32
	secondConnection := make(chan *websocket.Conn, 1)
	holdRelay := make(chan struct{})
	var releaseRelay sync.Once
	t.Cleanup(func() { releaseRelay.Do(func() { close(holdRelay) }) })
	upgrader := websocket.Upgrader{}
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/relay/daemon" {
			t.Errorf("path = %q, want /v1/relay/daemon", r.URL.Path)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer desktop-token" {
			t.Errorf("Authorization = %q, want desktop bearer", got)
			return
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if attempts.Add(1) == 1 {
			_ = ws.Close()
			return
		}
		secondConnection <- ws
		<-holdRelay
		_ = ws.Close()
	}))
	t.Cleanup(relay.Close)

	link := rpc.NewHubLink(rpc.HubLinkOptions{
		ServerURL:         relay.URL,
		AccessToken:       "desktop-token",
		RetryInitial:      time.Millisecond,
		RetryMax:          time.Millisecond,
		RetryWait:         func(context.Context, time.Duration) error { return nil },
		Random:            func() float64 { return 1 },
		HeartbeatInterval: time.Hour,
	})
	registerInboundPeerChat(t)
	inbound := peer.NewInbound(link)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- inbound.Run(ctx) }()
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			cancel()
			require.NoError(t, <-runDone)
		})
	}
	t.Cleanup(stop)

	var ws *websocket.Conn
	select {
	case ws = <-secondConnection:
	case <-time.After(time.Second):
		t.Fatal("desktop did not re-register after relay disconnect")
	}

	unauthenticated := relayRequest(t, ws, "desktop-peer", rpc.Frame{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: wire.MethodCapabilities,
		Params: mustJSON(t, wire.CapabilitiesParams{BackendType: "claudecode"}),
	})
	require.NotNil(t, unauthenticated.Error)
	assert.Equal(t, rpc.ErrUnauthorized.Code, unauthenticated.Error.Code)

	unauthenticatedList := relayRequest(t, ws, "desktop-peer", rpc.Frame{
		JSONRPC: "2.0", ID: json.RawMessage(`11`), Method: wire.MethodSessionList,
		Params: mustJSON(t, struct{}{}),
	})
	require.NotNil(t, unauthenticatedList.Error)
	assert.Equal(t, rpc.ErrUnauthorized.Code, unauthenticatedList.Error.Code)

	unauthenticatedAttach := relayRequest(t, ws, "desktop-peer", rpc.Frame{
		JSONRPC: "2.0", ID: json.RawMessage(`12`), Method: wire.MethodSessionAttach,
		Params: mustJSON(t, wire.SessionAttachParams{SessionID: 1}),
	})
	require.NotNil(t, unauthenticatedAttach.Error)
	assert.Equal(t, rpc.ErrUnauthorized.Code, unauthenticatedAttach.Error.Code)

	unauthenticatedWaiters := relayRequest(t, ws, "desktop-peer", rpc.Frame{
		JSONRPC: "2.0", ID: json.RawMessage(`13`), Method: wire.MethodSessionPendingWaiters,
		Params: mustJSON(t, wire.SessionPendingWaitersParams{SessionID: 1}),
	})
	require.NotNil(t, unauthenticatedWaiters.Error)
	assert.Equal(t, rpc.ErrUnauthorized.Code, unauthenticatedWaiters.Error.Code)

	authenticated := relayRequest(t, ws, "desktop-peer", rpc.Frame{
		JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "auth.account",
		Params: mustJSON(t, rpc.AccountParams{Credential: "same-account-device-jwt", DeviceFingerprint: "sha256:peer"}),
	})
	require.Nil(t, authenticated.Error)

	capabilities := relayRequest(t, ws, "desktop-peer", rpc.Frame{
		JSONRPC: "2.0", ID: json.RawMessage(`3`), Method: wire.MethodCapabilities,
		Params: mustJSON(t, wire.CapabilitiesParams{BackendType: "claudecode"}),
	})
	require.Nil(t, capabilities.Error)
	assert.NotEmpty(t, capabilities.Result, "the existing runtime method must reach the desktop peer registry")

	// The desktop session adapter uses the established runtime.session.* wire
	// family. JSON-RPC permits omitting params for a parameterless method, while
	// methods with required fields must continue to reject the same omission.
	missingAttachParams := relayRequest(t, ws, "desktop-peer", rpc.Frame{
		JSONRPC: "2.0", ID: json.RawMessage(`40`), Method: wire.MethodSessionAttach,
	})
	require.NotNil(t, missingAttachParams.Error)
	assert.Equal(t, rpc.ErrInvalidParams.Code, missingAttachParams.Error.Code)

	listed := relayRequest(t, ws, "desktop-peer", rpc.Frame{
		JSONRPC: "2.0", ID: json.RawMessage(`4`), Method: wire.MethodSessionList,
	})
	require.Nil(t, listed.Error, "a parameterless session-list request may omit params")
	var list wire.SessionListResult
	require.NoError(t, json.Unmarshal(listed.Result, &list))
	expectedSessions := []wire.SessionSummary{{
		SessionID:       1,
		PeerFingerprint: "sha256:desktop",
		AgentID:         7,
		Title:           "Ship the release",
		AgentSyncID:     "01HXAGENTIDENTITY0000000000",
		BackendType:     string(agent_backend_entity.TypeClaudeCode),
		LifecycleState:  wire.SessionLifecycleRunning,
		WaitingForInput: true,
		UpdatedAt:       1710000000000,
	}}
	require.Equal(t, expectedSessions, list.Sessions, "desktop rows carry every required summary field without a degraded fallback")

	explicitEmptyParams := relayRequest(t, ws, "desktop-peer", rpc.Frame{
		JSONRPC: "2.0", ID: json.RawMessage(`41`), Method: wire.MethodSessionList,
		Params: mustJSON(t, struct{}{}),
	})
	require.Nil(t, explicitEmptyParams.Error, "an explicit empty params object remains compatible")
	var explicitList wire.SessionListResult
	require.NoError(t, json.Unmarshal(explicitEmptyParams.Result, &explicitList))
	require.Equal(t, expectedSessions, explicitList.Sessions)

	attached := relayRequest(t, ws, "desktop-peer", rpc.Frame{
		JSONRPC: "2.0", ID: json.RawMessage(`5`), Method: wire.MethodSessionAttach,
		Params: mustJSON(t, wire.SessionAttachParams{SessionID: 1}),
	})
	require.Nil(t, attached.Error, "an authorized peer must attach a desktop session")
	var attachment wire.SessionAttachResult
	require.NoError(t, json.Unmarshal(attached.Result, &attachment))
	assert.Equal(t, wire.SessionAttachResult{
		SessionID: 1, BackendType: string(agent_backend_entity.TypeClaudeCode), LifecycleState: wire.SessionLifecycleRunning,
	}, attachment)

	pulled := relayRequest(t, ws, "desktop-peer", rpc.Frame{
		JSONRPC: "2.0", ID: json.RawMessage(`6`), Method: wire.MethodSessionPull,
		Params: mustJSON(t, wire.SessionPullParams{SessionID: 1}),
	})
	require.Nil(t, pulled.Error, "the attached peer must pull desktop history on the existing runtime.session.pull method")
	var page wire.SessionPullResult
	require.NoError(t, json.Unmarshal(pulled.Result, &page))
	assert.Equal(t, int64(0), page.OldestSeq, "empty desktop transcript has no reclaimed prefix")

	// 审批卡的数据源是这个方法，不是事件流：桌面端不答它，浏览器上一张待决策卡都
	// 画不出来（同一份浏览器代码对每种设备都调它，agentred 侧一直有）。
	waiters := relayRequest(t, ws, "desktop-peer", rpc.Frame{
		JSONRPC: "2.0", ID: json.RawMessage(`7`), Method: wire.MethodSessionPendingWaiters,
		Params: mustJSON(t, wire.SessionPendingWaitersParams{SessionID: 1}),
	})
	require.Nil(t, waiters.Error, "the attached peer must read this desktop session's still-blocked waiters")
	var pending wire.SessionPendingWaitersResult
	require.NoError(t, json.Unmarshal(waiters.Result, &pending))
	assert.Equal(t, wire.SessionPendingWaitersResult{
		ToolPermissions: []agentruntime.PendingToolPermission{{
			RequestID: "perm-1", ToolName: "Bash", Input: json.RawMessage(`{"command":"ls -la"}`),
		}},
		AskUserQuestions: []agentruntime.PendingAskUserQuestion{{
			RequestID: "ask-1", Questions: []agentruntime.AskQuestion{{ID: "q1", Question: "继续？"}},
		}},
	}, pending, "the desktop must hand back the same waiter payload shape agentred does")

	stop()
	require.NoError(t, ws.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err := ws.ReadMessage()
	require.Error(t, err, "desktop relay registration remained connected after App shutdown")
	releaseRelay.Do(func() { close(holdRelay) })
}

func registerInboundPeerChat(t *testing.T) {
	t.Helper()
	ctrl := gomock.NewController(t)
	agents := mock_agent_repo.NewMockAgentRepo(ctrl)
	backends := mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl)
	sessions := mock_chat_repo.NewMockSessionRepo(ctrl)
	messages := mock_chat_repo.NewMockMessageRepo(ctrl)
	device := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	prevChat := chat_svc.Chat()
	prevAgents := agent_repo.Agent()
	prevBackends := agent_backend_repo.AgentBackend()
	prevSessions := chat_repo.Session()
	prevMessages := chat_repo.Message()
	prevDevice := remote_device_svc.Default()
	agent_repo.RegisterAgent(agents)
	agent_backend_repo.RegisterAgentBackend(backends)
	chat_repo.RegisterSession(sessions)
	chat_repo.RegisterMessage(messages)
	remote_device_svc.SetDefault(device)
	chat_svc.RegisterChat(chat_svc.NewChat(chat_svc.NoopEmitter{}))
	t.Cleanup(func() {
		chat_svc.RegisterChat(prevChat)
		agent_repo.RegisterAgent(prevAgents)
		agent_backend_repo.RegisterAgentBackend(prevBackends)
		chat_repo.RegisterSession(prevSessions)
		chat_repo.RegisterMessage(prevMessages)
		remote_device_svc.SetDefault(prevDevice)
		ctrl.Finish()
	})

	agent := &agent_entity.Agent{
		ID: 7, Name: "Release captain", AgentBackendID: 11, Status: consts.ACTIVE,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "01HXAGENTIDENTITY0000000000"},
	}
	device.EXPECT().DeviceFingerprint().Return("sha256:desktop", nil).Times(2)
	agents.EXPECT().List(gomock.Any()).Return([]*agent_entity.Agent{agent}, nil).Times(2)
	sessions.EXPECT().ListByAgentPagedIncludingGroups(gomock.Any(), int64(7), 0, gomock.Any()).Return([]*chat_entity.Session{{
		ID: 1, AgentID: 7, Title: "Ship the release", AgentStatus: "waiting", LastMessageAt: 1710000000000, Status: consts.ACTIVE,
	}}, nil).Times(2)
	sessions.EXPECT().Find(gomock.Any(), int64(1)).Return(&chat_entity.Session{
		ID: 1, AgentID: 7, Title: "Ship the release", AgentStatus: "waiting", Status: consts.ACTIVE,
	}, nil).Times(2)
	messages.EXPECT().List(gomock.Any(), int64(1)).Return(nil, nil)
	agents.EXPECT().Find(gomock.Any(), int64(7)).Return(agent, nil).Times(2)
	backends.EXPECT().Find(gomock.Any(), int64(11)).Return(&agent_backend_entity.AgentBackend{
		ID: 11, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
	}, nil).AnyTimes()

	t.Cleanup(agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, &waiterRuntime{
		sessionID: 1,
		snapshot: agentruntime.WaiterSnapshot{
			ToolPermissions: []agentruntime.PendingToolPermission{{
				RequestID: "perm-1", ToolName: "Bash", Input: json.RawMessage(`{"command":"ls -la"}`),
			}},
			AskUserQuestions: []agentruntime.PendingAskUserQuestion{{
				RequestID: "ask-1", Questions: []agentruntime.AskQuestion{{ID: "q1", Question: "继续？"}},
			}},
		},
	}))
}

// waiterRuntime 是一个有审批协议的 backend runtime 替身:它只回答「这条会话此刻
// 还阻塞着哪些待决策」(agentruntime.WaiterLister),不跑轮次。
type waiterRuntime struct {
	sessionID int64
	snapshot  agentruntime.WaiterSnapshot
}

func (r *waiterRuntime) Capabilities() capability.Capabilities { return capability.Capabilities{} }

func (r *waiterRuntime) Run(context.Context, agentruntime.RunRequest) (
	<-chan agentruntime.Event, *agentruntime.RunResult, error,
) {
	return nil, nil, errors.New("waiterRuntime never runs a turn")
}

func (r *waiterRuntime) PendingWaiters(_ context.Context, sessionID int64) agentruntime.WaiterSnapshot {
	if sessionID != r.sessionID {
		return agentruntime.WaiterSnapshot{}
	}
	return r.snapshot
}

func relayRequest(t *testing.T, conn *websocket.Conn, channelID string, request rpc.Frame) rpc.Frame {
	t.Helper()
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, relayEnvelope(channelID, mustJSON(t, request))))
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	messageType, payload, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, messageType)
	responseID, responseJSON := unpackRelayEnvelope(t, payload)
	require.Equal(t, channelID, responseID)
	var response rpc.Frame
	require.NoError(t, json.Unmarshal(responseJSON, &response))
	return response
}

func relayEnvelope(channelID string, frame []byte) []byte {
	payload := make([]byte, 2+len(channelID)+len(frame))
	binary.BigEndian.PutUint16(payload, uint16(len(channelID)))
	copy(payload[2:], channelID)
	copy(payload[2+len(channelID):], frame)
	return payload
}

func unpackRelayEnvelope(t *testing.T, payload []byte) (string, []byte) {
	t.Helper()
	require.GreaterOrEqual(t, len(payload), 2)
	length := int(binary.BigEndian.Uint16(payload[:2]))
	require.GreaterOrEqual(t, len(payload), 2+length)
	return string(payload[2 : 2+length]), payload[2+length:]
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	require.NoError(t, err)
	return payload
}

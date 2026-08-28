package openclaw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/pkg/openclawgateway"
)

type runtimeRequestFrame struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type runtimeAgentParams struct {
	Message        string `json:"message"`
	AgentID        string `json:"agentId"`
	Model          string `json:"model"`
	SessionKey     string `json:"sessionKey"`
	IdempotencyKey string `json:"idempotencyKey"`
}

func runtimeGateway(t *testing.T, handler func(*websocket.Conn, int)) string {
	t.Helper()
	var connections atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		handler(conn, int(connections.Add(1)))
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func runtimeWrite(t *testing.T, conn *websocket.Conn, value any) {
	t.Helper()
	require.NoError(t, conn.WriteJSON(value))
}

func runtimeReadRequest(t *testing.T, conn *websocket.Conn) runtimeRequestFrame {
	t.Helper()
	var request runtimeRequestFrame
	require.NoError(t, conn.ReadJSON(&request))
	return request
}

// runtimeReadTurnRequest 吃掉开轮前的 exec.approval.list,返回真正的开轮请求。
// 开轮方法有两种:默认 chat.send(网关据此把本设备记成审批 reviewer),仅在 admin
// 下发 model override 时才用 agent。
// runtimeCanonicalKey 复刻网关对会话 key 的规范化:agent:<agentId>:<key>,
// 并在 sessions.messages.subscribe 的应答里回报(真实网关就是这么回的)。
func runtimeCanonicalKey(t *testing.T, params json.RawMessage) string {
	t.Helper()
	var value struct {
		Key     string `json:"key"`
		AgentID string `json:"agentId"`
	}
	require.NoError(t, json.Unmarshal(params, &value))
	agentID := value.AgentID
	if agentID == "" {
		agentID = "main"
	}
	if strings.HasPrefix(value.Key, "agent:") {
		return value.Key
	}
	return "agent:" + agentID + ":" + value.Key
}

// runtimeReadAfterSubscribe 读下一条请求;若是 sessions.messages.subscribe 就先应答
// 再读真正要断言的那条(chat.send 的轮次开轮前必订阅)。
func runtimeReadAfterSubscribe(t *testing.T, conn *websocket.Conn) runtimeRequestFrame {
	t.Helper()
	request := runtimeReadRequest(t, conn)
	if request.Method == "sessions.messages.subscribe" {
		runtimeWrite(t, conn, map[string]any{
			"type": "res", "id": request.ID, "ok": true,
			"payload": map[string]any{"subscribed": true, "key": runtimeCanonicalKey(t, request.Params)},
		})
		request = runtimeReadRequest(t, conn)
	}
	return request
}

func runtimeReadTurnRequest(t *testing.T, conn *websocket.Conn) runtimeRequestFrame {
	t.Helper()
	list := runtimeReadRequest(t, conn)
	require.Equal(t, "exec.approval.list", list.Method)
	runtimeWrite(t, conn, map[string]any{
		"type": "res", "id": list.ID, "ok": true, "payload": []any{},
	})
	request := runtimeReadRequest(t, conn)
	// chat.send 的事件只发给订阅者,所以开轮前会先 sessions.messages.subscribe。
	if request.Method == "sessions.messages.subscribe" {
		runtimeWrite(t, conn, map[string]any{
			"type": "res", "id": request.ID, "ok": true,
			"payload": map[string]any{"subscribed": true, "key": runtimeCanonicalKey(t, request.Params)},
		})
		request = runtimeReadRequest(t, conn)
	}
	require.Contains(t, []string{"chat.send", "agent"}, request.Method)
	return request
}

func runtimeHandshake(t *testing.T, conn *websocket.Conn, connection int) {
	runtimeHandshakeWithScopes(t, conn, connection, openclawgateway.RequiredOperatorScopes)
}

// runtimeHandshakeWithMethods 广播额外的可选方法(如 sessions.describe):它们不属于
// requiredRuntimeMethods,runtime 必须按 hello 里的广播来决定调不调。
func runtimeHandshakeWithMethods(t *testing.T, conn *websocket.Conn, connection int, extraMethods ...string) {
	runtimeHandshakeWith(t, conn, connection, openclawgateway.RequiredOperatorScopes, extraMethods)
}

// runtimeHandshakeWithoutMethods 模拟老网关:不广播某些可选方法。
func runtimeHandshakeWithoutMethods(t *testing.T, conn *websocket.Conn, connection int, drop ...string) {
	t.Helper()
	runtimeHandshakeWith(t, conn, connection, openclawgateway.RequiredOperatorScopes, nil, drop...)
}

func runtimeHandshakeWithScopes(t *testing.T, conn *websocket.Conn, connection int, scopes []string) {
	runtimeHandshakeWith(t, conn, connection, scopes, nil)
}

func runtimeHandshakeWith(t *testing.T, conn *websocket.Conn, connection int, scopes, extraMethods []string, drop ...string) {
	t.Helper()
	// 与真实网关(2026.7.1-2)一致:除 requiredRuntimeMethods 外还广播 chat.send 与
	// sessions.messages.subscribe —— 开轮走 chat.send 才能让本设备看到自己触发的审批。
	methods := append([]string{
		"agent", "agent.wait", "chat.abort", "agents.list", "models.list",
		"exec.approval.list", "exec.approval.resolve",
		"chat.send", "sessions.messages.subscribe",
	}, extraMethods...)
	if len(drop) > 0 {
		kept := methods[:0]
		for _, method := range methods {
			if !slices.Contains(drop, method) {
				kept = append(kept, method)
			}
		}
		methods = kept
	}
	runtimeWrite(t, conn, map[string]any{
		"type": "event", "event": "connect.challenge", "seq": 1,
		"payload": map[string]any{"nonce": "runtime-nonce", "ts": time.Now().UnixMilli()},
	})
	connect := runtimeReadRequest(t, conn)
	require.Equal(t, "connect", connect.Method)
	runtimeWrite(t, conn, map[string]any{
		"type": "res", "id": connect.ID, "ok": true,
		"payload": map[string]any{
			"type": "hello-ok", "protocol": 4,
			"server": map[string]any{"version": "2026.7.1-2", "connId": fmt.Sprintf("runtime-%d", connection)},
			"features": map[string]any{
				"methods": methods,
				"events":  []string{"agent", "chat", "exec.approval.requested", "exec.approval.resolved"},
			},
			"snapshot": map[string]any{"presence": []any{}, "health": map[string]any{}, "stateVersion": map[string]any{"presence": 1, "health": 1}, "uptimeMs": 1},
			"auth":     map[string]any{"role": "operator", "scopes": scopes},
			"policy":   map[string]any{"maxPayload": 1048576, "maxBufferedBytes": 2097152, "tickIntervalMs": 30000},
		},
	})
}

func runtimeIdentity(t *testing.T) *openclawgateway.DeviceIdentity {
	t.Helper()
	identity, err := openclawgateway.NewDeviceIdentityFromSeed(make([]byte, 32))
	require.NoError(t, err)
	return identity
}

func runtimeResolver(t *testing.T, gatewayURL string) ConfigResolver {
	t.Helper()
	return ConfigResolverFunc(func(context.Context, int64) (openclawgateway.Config, error) {
		return openclawgateway.Config{
			URL: gatewayURL, Identity: runtimeIdentity(t), Platform: "linux",
			ReconnectInitial: 5 * time.Millisecond, ReconnectMax: 20 * time.Millisecond,
		}, nil
	})
}

func runtimeBackend() *agent_backend_entity.AgentBackend {
	return &agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeOpenClaw), Name: "OpenClaw",
		OpenClawGatewayURL: "ws://127.0.0.1:18789", OpenClawAgentID: "main",
		OpenClawDefaultModel: "anthropic/claude-sonnet-4-6",
		OpenClawSessionMode:  agent_backend_entity.OpenClawSessionPerAgentRESession,
	}
}

func collectRuntimeEvents(t *testing.T, events <-chan agentruntime.Event) []agentruntime.Event {
	t.Helper()
	var result []agentruntime.Event
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return result
			}
			result = append(result, event)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out draining OpenClaw runtime events")
			return nil
		}
	}
}

func TestRuntimeCapabilities(t *testing.T) {
	runtime := New(nil)
	assert.True(t, runtime.Capabilities().Has(capability.CapAbort))
	assert.True(t, runtime.Capabilities().Has(capability.CapExecApproval))
	assert.False(t, runtime.Capabilities().Has(capability.CapAnswerUserAsk))
	assert.False(t, runtime.Capabilities().Has(capability.CapForkSession))
	var _ agentruntime.Aborter = runtime
}

// TestRuntimeRunsSelfFingerprintBackendLocally R13 认领后本机 OpenClaw backend 的
// DeviceID 是本机指纹:本地 runtime 必须把它当本机档拨本机网关,而不是在入口就把它
// 当远端档拦成 "remote secret enrollment is unavailable"。
func TestRuntimeRunsSelfFingerprintBackendLocally(t *testing.T) {
	paramsSeen := make(chan runtimeAgentParams, 1)
	gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
		runtimeHandshake(t, conn, connection)
		request := runtimeReadTurnRequest(t, conn)
		var params runtimeAgentParams
		require.NoError(t, json.Unmarshal(request.Params, &params))
		paramsSeen <- params
		runtimeWrite(t, conn, map[string]any{
			"type": "res", "id": request.ID, "ok": true,
			"payload": map[string]any{"runId": params.IdempotencyKey, "status": "accepted"},
		})
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "agent", "seq": 2,
			"payload": map[string]any{
				"runId": params.IdempotencyKey, "sessionKey": params.SessionKey,
				"seq": 1, "stream": "lifecycle", "ts": 1,
				"data": map[string]any{"phase": "end"},
			},
		})
	})

	runtime := New(runtimeResolver(t, gatewayURL))
	selfBackend := runtimeBackend()
	selfBackend.DeviceFingerprint = "sha256:self"
	events, result, err := runtime.Run(context.Background(), agentruntime.RunRequest{
		Backend: selfBackend, SessionID: 44, UserText: "run locally",
	})
	require.NoError(t, err, "self-fingerprint OpenClaw backend must dial the local gateway, not be rejected as remote")
	_ = collectRuntimeEvents(t, events)

	params := <-paramsSeen
	assert.Equal(t, "run locally", params.Message)
	assert.Empty(t, result.Model)
}

func TestRuntimeOmitsModelOverrideWithoutAdminScope(t *testing.T) {
	t.Run("Given a discovered backend model and non-admin operator scopes, when a turn starts, then the agent request inherits the Gateway model", func(t *testing.T) {
		paramsSeen := make(chan runtimeAgentParams, 1)
		gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
			runtimeHandshake(t, conn, connection)
			request := runtimeReadTurnRequest(t, conn)
			var params runtimeAgentParams
			require.NoError(t, json.Unmarshal(request.Params, &params))
			paramsSeen <- params
			runtimeWrite(t, conn, map[string]any{
				"type": "res", "id": request.ID, "ok": true,
				"payload": map[string]any{"runId": params.IdempotencyKey, "status": "accepted"},
			})
			runtimeWrite(t, conn, map[string]any{
				"type": "event", "event": "agent", "seq": 2,
				"payload": map[string]any{
					"runId": params.IdempotencyKey, "sessionKey": params.SessionKey,
					"seq": 1, "stream": "lifecycle", "ts": 1,
					"data": map[string]any{"phase": "end"},
				},
			})
		})

		runtime := New(runtimeResolver(t, gatewayURL))
		events, result, err := runtime.Run(context.Background(), agentruntime.RunRequest{
			Backend: runtimeBackend(), SessionID: 44, UserText: "inherit the configured model",
		})
		require.NoError(t, err)
		_ = collectRuntimeEvents(t, events)

		params := <-paramsSeen
		assert.Empty(t, params.Model)
		assert.Empty(t, result.Model)
	})
}

func TestRuntimeAdoptsCanonicalSessionKeyFromAcceptedResponse(t *testing.T) {
	t.Run("Given the Gateway canonicalizes a new session key, when accepted events arrive, then the canonical key is persisted and streamed", func(t *testing.T) {
		const canonicalSessionKey = "agent:main:agentre:12:45"
		gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
			runtimeHandshake(t, conn, connection)
			request := runtimeReadTurnRequest(t, conn)
			var params runtimeAgentParams
			require.NoError(t, json.Unmarshal(request.Params, &params))
			// 订阅应答已把 key 规范化,开轮请求带的就是规范 key。
			require.Equal(t, canonicalSessionKey, params.SessionKey)
			runtimeWrite(t, conn, map[string]any{
				"type": "res", "id": request.ID, "ok": true,
				"payload": map[string]any{
					"runId": params.IdempotencyKey, "sessionKey": canonicalSessionKey,
					"status": "accepted",
				},
			})
			runtimeWrite(t, conn, map[string]any{
				"type": "event", "event": "agent", "seq": 2,
				"payload": map[string]any{
					"runId": params.IdempotencyKey, "sessionKey": canonicalSessionKey,
					"seq": 1, "stream": "assistant", "ts": 1,
					"data": map[string]any{"text": "canonical response", "delta": "canonical response"},
				},
			})
			runtimeWrite(t, conn, map[string]any{
				"type": "event", "event": "agent", "seq": 3,
				"payload": map[string]any{
					"runId": params.IdempotencyKey, "sessionKey": canonicalSessionKey,
					"seq": 2, "stream": "lifecycle", "ts": 2,
					"data": map[string]any{"phase": "end"},
				},
			})
		})

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		runtime := New(runtimeResolver(t, gatewayURL))
		events, result, err := runtime.Run(ctx, agentruntime.RunRequest{
			Backend: runtimeBackend(), SessionID: 45, UserText: "canonicalize this session",
		})
		require.NoError(t, err)
		assert.Equal(t, canonicalSessionKey, result.ProviderSessionID)
		collected := collectRuntimeEvents(t, events)
		require.Len(t, collected, 2)
		assert.Equal(t, agentruntime.TextDelta{Text: "canonical response"}, collected[0])
		_, done := collected[1].(agentruntime.Done)
		assert.True(t, done)
	})
}

func TestRuntimeExecApprovalUsesGatewayDecisionsAndDoesNotFinishTheExec(t *testing.T) {
	approvalSent := make(chan struct{})
	allowRunFinish := make(chan struct{})
	var resolveCalls atomic.Int32
	gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
		runtimeHandshake(t, conn, connection)
		agentRequest := runtimeReadTurnRequest(t, conn)
		var params runtimeAgentParams
		require.NoError(t, json.Unmarshal(agentRequest.Params, &params))
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": agentRequest.ID, "ok": true, "payload": map[string]any{"runId": params.IdempotencyKey, "status": "accepted"}})
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "exec.approval.requested", "seq": 2,
			"payload": map[string]any{
				"id": "approval-1", "createdAtMs": int64(100), "expiresAtMs": time.Now().Add(time.Minute).UnixMilli(),
				"request": map[string]any{
					"command": "untrusted fallback", "commandPreview": "printf safe", "allowedDecisions": []string{"allow-once", "deny"},
					"host": "node", "nodeId": "node-1", "agentId": "main", "sessionKey": params.SessionKey,
					"systemRunPlan": map[string]any{"argv": []string{"printf", "safe"}, "cwd": "/workspace", "commandText": "printf safe", "agentId": "main", "sessionKey": params.SessionKey},
				},
			},
		})
		close(approvalSent)
		resolve := runtimeReadRequest(t, conn)
		require.Equal(t, "exec.approval.resolve", resolve.Method)
		resolveCalls.Add(1)
		var decision map[string]any
		require.NoError(t, json.Unmarshal(resolve.Params, &decision))
		require.Equal(t, map[string]any{"id": "approval-1", "decision": "allow-once"}, decision)
		// AgentRE returns only the Gateway decision. It must not rebuild or send
		// the canonical node systemRunPlan.
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": resolve.ID, "ok": true, "payload": map[string]any{"ok": true}})
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "exec.approval.resolved", "seq": 3,
			"payload": map[string]any{"id": "approval-1", "decision": "allow-once", "resolvedBy": "device-1", "ts": int64(150), "request": map[string]any{"sessionKey": params.SessionKey}},
		})
		<-allowRunFinish
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "agent", "seq": 4,
			"payload": map[string]any{"runId": params.IdempotencyKey, "sessionKey": params.SessionKey, "seq": 1, "stream": "lifecycle", "data": map[string]any{"phase": "end"}},
		})
	})

	runtime := New(runtimeResolver(t, gatewayURL))
	events, _, err := runtime.Run(context.Background(), agentruntime.RunRequest{
		Backend: runtimeBackend(), SessionID: 39, UserText: "run a command",
	})
	require.NoError(t, err)
	<-approvalSent
	requested, ok := (<-events).(agentruntime.ExecApprovalRequested)
	require.True(t, ok)
	assert.Equal(t, "approval-1", requested.ID)
	assert.Equal(t, "printf safe", requested.CommandText)
	assert.Equal(t, []string{"allow-once", "deny"}, requested.AllowedDecisions)
	assert.Equal(t, "node-1", requested.NodeID)

	resolution, err := runtime.ResolveExecApproval(context.Background(), 39, "approval-1", "allow-once")
	require.NoError(t, err)
	assert.Equal(t, "resolved", resolution.Status)
	assert.Equal(t, "allow-once", resolution.Decision)
	// Repeating the same UI action is idempotent and must not send a second RPC.
	resolution, err = runtime.ResolveExecApproval(context.Background(), 39, "approval-1", "allow-once")
	require.NoError(t, err)
	assert.Equal(t, "resolved", resolution.Status)
	assert.Equal(t, int32(1), resolveCalls.Load())

	resolved, ok := (<-events).(agentruntime.ExecApprovalResolved)
	require.True(t, ok)
	assert.Equal(t, "resolved", resolved.Status)
	select {
	case unexpected := <-events:
		t.Fatalf("approval resolution must not finish the exec turn: %#v", unexpected)
	case <-time.After(30 * time.Millisecond):
	}
	close(allowRunFinish)
	final := collectRuntimeEvents(t, events)
	require.Len(t, final, 1)
	_, ok = final[0].(agentruntime.Done)
	assert.True(t, ok)
}

func TestRuntimeExecApprovalTreatsExpiredAndRacingResolutionsAsIdempotentTerminals(t *testing.T) {
	t.Run("Given the approval expires before it is shown when the initial list is reconciled then it is terminal without a resolve RPC", func(t *testing.T) {
		gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
			runtimeHandshake(t, conn, connection)
			list := runtimeReadAfterSubscribe(t, conn)
			require.Equal(t, "exec.approval.list", list.Method)
			runtimeWrite(t, conn, map[string]any{
				"type": "res", "id": list.ID, "ok": true,
				"payload": []any{map[string]any{
					"id": "approval-expired", "createdAtMs": int64(1), "expiresAtMs": int64(2),
					"request": map[string]any{
						"command": "date", "allowedDecisions": []string{"deny"},
						// 真实网关的审批记录一律用规范化 key。
						"sessionKey": "agent:main:agentre:12:40",
					},
				}},
			})
			agentRequest := runtimeReadAfterSubscribe(t, conn)
			require.Equal(t, "chat.send", agentRequest.Method)
			var params runtimeAgentParams
			require.NoError(t, json.Unmarshal(agentRequest.Params, &params))
			runtimeWrite(t, conn, map[string]any{"type": "res", "id": agentRequest.ID, "ok": true, "payload": map[string]any{"runId": params.IdempotencyKey, "status": "accepted"}})
			runtimeWrite(t, conn, map[string]any{
				"type": "event", "event": "agent", "seq": 2,
				"payload": map[string]any{"runId": params.IdempotencyKey, "sessionKey": params.SessionKey, "seq": 1, "stream": "lifecycle", "data": map[string]any{"phase": "end"}},
			})
		})
		runtime := New(runtimeResolver(t, gatewayURL))
		events, _, err := runtime.Run(context.Background(), agentruntime.RunRequest{Backend: runtimeBackend(), SessionID: 40, UserText: "expired"})
		require.NoError(t, err)
		collected := collectRuntimeEvents(t, events)
		require.Len(t, collected, 2)
		expired, ok := collected[0].(agentruntime.ExecApprovalResolved)
		require.True(t, ok)
		assert.Equal(t, "expired", expired.Status)
		_, ok = collected[1].(agentruntime.Done)
		assert.True(t, ok)
	})

	t.Run("Given another client removes the pending approval when deny is submitted then APPROVAL_NOT_FOUND is an expired terminal", func(t *testing.T) {
		approvalSent := make(chan struct{})
		allowFinish := make(chan struct{})
		gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
			runtimeHandshake(t, conn, connection)
			agentRequest := runtimeReadTurnRequest(t, conn)
			var params runtimeAgentParams
			require.NoError(t, json.Unmarshal(agentRequest.Params, &params))
			runtimeWrite(t, conn, map[string]any{"type": "res", "id": agentRequest.ID, "ok": true, "payload": map[string]any{"runId": params.IdempotencyKey, "status": "accepted"}})
			runtimeWrite(t, conn, map[string]any{
				"type": "event", "event": "exec.approval.requested", "seq": 2,
				"payload": map[string]any{"id": "approval-race", "expiresAtMs": time.Now().Add(time.Minute).UnixMilli(), "request": map[string]any{
					"command": "false", "allowedDecisions": []string{"deny"}, "sessionKey": params.SessionKey,
				}},
			})
			close(approvalSent)
			resolve := runtimeReadRequest(t, conn)
			runtimeWrite(t, conn, map[string]any{
				"type": "res", "id": resolve.ID, "ok": false,
				"error": map[string]any{"code": "INVALID_REQUEST", "message": "unknown or expired approval id", "details": map[string]any{"reason": "APPROVAL_NOT_FOUND"}},
			})
			<-allowFinish
			runtimeWrite(t, conn, map[string]any{
				"type": "event", "event": "agent", "seq": 3,
				"payload": map[string]any{"runId": params.IdempotencyKey, "sessionKey": params.SessionKey, "seq": 1, "stream": "lifecycle", "data": map[string]any{"phase": "end"}},
			})
		})
		runtime := New(runtimeResolver(t, gatewayURL))
		events, _, err := runtime.Run(context.Background(), agentruntime.RunRequest{Backend: runtimeBackend(), SessionID: 41, UserText: "race"})
		require.NoError(t, err)
		<-approvalSent
		_, ok := (<-events).(agentruntime.ExecApprovalRequested)
		require.True(t, ok)
		resolution, err := runtime.ResolveExecApproval(context.Background(), 41, "approval-race", "deny")
		require.NoError(t, err)
		assert.Equal(t, "expired", resolution.Status)
		expired, ok := (<-events).(agentruntime.ExecApprovalResolved)
		require.True(t, ok)
		assert.Equal(t, "expired", expired.Status)
		close(allowFinish)
		collectRuntimeEvents(t, events)
	})
}

func TestRuntimeExecApprovalExpiresWhileGatewayRemainsConnected(t *testing.T) {
	allowFinish := make(chan struct{})
	gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
		runtimeHandshake(t, conn, connection)
		agentRequest := runtimeReadTurnRequest(t, conn)
		var params runtimeAgentParams
		require.NoError(t, json.Unmarshal(agentRequest.Params, &params))
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": agentRequest.ID, "ok": true, "payload": map[string]any{"runId": params.IdempotencyKey, "status": "accepted"}})
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "exec.approval.requested", "seq": 2,
			"payload": map[string]any{
				"id": "approval-online-expiry", "expiresAtMs": time.Now().Add(80 * time.Millisecond).UnixMilli(),
				"request": map[string]any{
					"command": "sleep 1", "allowedDecisions": []string{"allow-once", "deny"}, "sessionKey": params.SessionKey,
				},
			},
		})
		<-allowFinish
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "agent", "seq": 3,
			"payload": map[string]any{"runId": params.IdempotencyKey, "sessionKey": params.SessionKey, "seq": 1, "stream": "lifecycle", "data": map[string]any{"phase": "end"}},
		})
	})

	runtime := New(runtimeResolver(t, gatewayURL))
	events, _, err := runtime.Run(context.Background(), agentruntime.RunRequest{
		Backend: runtimeBackend(), SessionID: 43, UserText: "wait for approval expiry",
	})
	require.NoError(t, err)
	requested, ok := (<-events).(agentruntime.ExecApprovalRequested)
	require.True(t, ok)
	assert.Equal(t, "approval-online-expiry", requested.ID)

	select {
	case event := <-events:
		expired, ok := event.(agentruntime.ExecApprovalResolved)
		require.True(t, ok)
		assert.Equal(t, "approval-online-expiry", expired.ID)
		assert.Equal(t, "expired", expired.Status)
		assert.Empty(t, expired.Decision)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("connected approval did not expire at expiresAtMs")
	}

	close(allowFinish)
	collected := collectRuntimeEvents(t, events)
	require.Len(t, collected, 1)
	_, ok = collected[0].(agentruntime.Done)
	assert.True(t, ok)
}

func TestRuntimeExecApprovalReconnectListRestoresPendingWithoutReplayingTurn(t *testing.T) {
	var agentCalls atomic.Int32
	var listCalls atomic.Int32
	var runID string
	var sessionKey string
	gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
		runtimeHandshake(t, conn, connection)
		list := runtimeReadAfterSubscribe(t, conn)
		require.Equal(t, "exec.approval.list", list.Method)
		listCalls.Add(1)
		if connection == 1 {
			runtimeWrite(t, conn, map[string]any{"type": "res", "id": list.ID, "ok": true, "payload": []any{}})
			agentRequest := runtimeReadAfterSubscribe(t, conn)
			require.Equal(t, "chat.send", agentRequest.Method)
			agentCalls.Add(1)
			var params runtimeAgentParams
			require.NoError(t, json.Unmarshal(agentRequest.Params, &params))
			runID, sessionKey = params.IdempotencyKey, params.SessionKey
			runtimeWrite(t, conn, map[string]any{"type": "res", "id": agentRequest.ID, "ok": true, "payload": map[string]any{"runId": runID, "status": "accepted"}})
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "approval reconcile"), time.Now().Add(time.Second))
			return
		}
		runtimeWrite(t, conn, map[string]any{
			"type": "res", "id": list.ID, "ok": true,
			"payload": []any{map[string]any{
				"id": "approval-restored", "createdAtMs": int64(10), "expiresAtMs": time.Now().Add(time.Minute).UnixMilli(),
				"request": map[string]any{"command": "pwd", "allowedDecisions": []string{"allow-once", "allow-always", "deny"}, "sessionKey": sessionKey},
			}},
		})
		wait := runtimeReadRequest(t, conn)
		require.Equal(t, "agent.wait", wait.Method)
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": wait.ID, "ok": true, "payload": map[string]any{"runId": runID, "status": "running"}})
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "exec.approval.resolved", "seq": 2,
			"payload": map[string]any{"id": "approval-restored", "decision": "deny", "resolvedBy": "other-device", "ts": int64(20), "request": map[string]any{"sessionKey": sessionKey}},
		})
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "agent", "seq": 3,
			"payload": map[string]any{"runId": runID, "sessionKey": sessionKey, "seq": 1, "stream": "lifecycle", "data": map[string]any{"phase": "end"}},
		})
	})
	runtime := New(runtimeResolver(t, gatewayURL))
	events, _, err := runtime.Run(context.Background(), agentruntime.RunRequest{Backend: runtimeBackend(), SessionID: 42, UserText: "reconnect approval"})
	require.NoError(t, err)
	collected := collectRuntimeEvents(t, events)
	require.Len(t, collected, 3)
	restored, ok := collected[0].(agentruntime.ExecApprovalRequested)
	require.True(t, ok)
	assert.Equal(t, []string{"allow-once", "allow-always", "deny"}, restored.AllowedDecisions)
	resolved, ok := collected[1].(agentruntime.ExecApprovalResolved)
	require.True(t, ok)
	assert.Equal(t, "deny", resolved.Decision)
	_, ok = collected[2].(agentruntime.Done)
	assert.True(t, ok)
	assert.Equal(t, int32(1), agentCalls.Load())
	assert.Equal(t, int32(2), listCalls.Load())
}

func TestRuntimeStreamsTurnAndCreatesStableSessionMapping(t *testing.T) {
	paramsSeen := make(chan runtimeAgentParams, 1)
	gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
		runtimeHandshakeWithScopes(t, conn, connection, append(
			append([]string(nil), openclawgateway.RequiredOperatorScopes...),
			"operator.admin",
		))
		request := runtimeReadTurnRequest(t, conn)
		var params runtimeAgentParams
		require.NoError(t, json.Unmarshal(request.Params, &params))
		paramsSeen <- params
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": request.ID, "ok": true, "payload": map[string]any{"runId": params.IdempotencyKey, "status": "accepted"}})

		frames := []map[string]any{
			{"type": "event", "event": "agent", "seq": 2, "payload": map[string]any{"runId": params.IdempotencyKey, "sessionKey": params.SessionKey, "seq": 1, "stream": "assistant", "ts": 1, "data": map[string]any{"text": "Hello", "delta": "Hello"}}},
			{"type": "event", "event": "agent", "seq": 3, "payload": map[string]any{"runId": params.IdempotencyKey, "sessionKey": params.SessionKey, "seq": 2, "stream": "thinking", "ts": 2, "data": map[string]any{"text": "Think", "delta": "Think"}}},
			{"type": "event", "event": "agent", "seq": 4, "payload": map[string]any{"runId": params.IdempotencyKey, "sessionKey": params.SessionKey, "seq": 3, "stream": "tool", "ts": 3, "data": map[string]any{"phase": "start", "name": "exec", "toolCallId": "tool-1", "args": map[string]any{"command": "pwd"}}}},
			{"type": "event", "event": "agent", "seq": 5, "payload": map[string]any{"runId": params.IdempotencyKey, "sessionKey": params.SessionKey, "seq": 4, "stream": "tool", "ts": 4, "data": map[string]any{"phase": "result", "name": "exec", "toolCallId": "tool-1", "result": "ok", "isError": false}}},
			{"type": "event", "event": "agent", "seq": 6, "payload": map[string]any{"runId": params.IdempotencyKey, "sessionKey": params.SessionKey, "seq": 5, "stream": "lifecycle", "ts": 5, "data": map[string]any{"phase": "end", "usage": map[string]any{"input": 10, "output": 4, "cacheRead": 2, "cacheWrite": 1, "total": 17}}}},
		}
		for _, frame := range frames {
			runtimeWrite(t, conn, frame)
		}
		<-time.After(100 * time.Millisecond)
	})

	runtime := New(runtimeResolver(t, gatewayURL))
	events, result, err := runtime.Run(context.Background(), agentruntime.RunRequest{
		Backend: runtimeBackend(), SessionID: 34, AgentID: 8, UserText: "hello",
	})
	require.NoError(t, err)
	collected := collectRuntimeEvents(t, events)

	params := <-paramsSeen
	assert.Equal(t, "agentre:12:34", params.SessionKey)
	assert.Equal(t, "hello", params.Message)
	assert.Equal(t, "main", params.AgentID)
	assert.Equal(t, "anthropic/claude-sonnet-4-6", params.Model)
	_, err = uuid.Parse(params.IdempotencyKey)
	assert.NoError(t, err)

	require.Len(t, collected, 6)
	assert.Equal(t, agentruntime.TextDelta{Text: "Hello"}, collected[0])
	assert.Equal(t, agentruntime.ThinkingDelta{Text: "Think"}, collected[1])
	toolCall, ok := collected[2].(agentruntime.ToolCall)
	require.True(t, ok)
	assert.Equal(t, "tool-1", toolCall.ID)
	assert.Equal(t, "exec", toolCall.Name)
	assert.JSONEq(t, `{"command":"pwd"}`, string(toolCall.Input))
	toolResult, ok := collected[3].(agentruntime.ToolResult)
	require.True(t, ok)
	assert.Equal(t, "tool-1", toolResult.ToolCallID)
	assert.Equal(t, "ok", toolResult.Content)
	usage, ok := collected[4].(agentruntime.UsageUpdate)
	require.True(t, ok)
	assert.Equal(t, 10, usage.Usage.PromptTokens)
	assert.Equal(t, 4, usage.Usage.CompletionTokens)
	assert.Equal(t, 13, usage.TotalInputTokens)
	_, ok = collected[5].(agentruntime.Done)
	assert.True(t, ok)
	assert.Equal(t, "agentre:12:34", result.ProviderSessionID)
	assert.Equal(t, "anthropic/claude-sonnet-4-6", result.Model)
	require.NotNil(t, result.Usage)
	assert.Equal(t, 17, result.Usage.TotalTokens)
}

func TestRuntimeReusesProviderSessionAndIsolatesRunEvents(t *testing.T) {
	gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
		runtimeHandshake(t, conn, connection)
		request := runtimeReadTurnRequest(t, conn)
		var params runtimeAgentParams
		require.NoError(t, json.Unmarshal(request.Params, &params))
		// 订阅应答回报规范化 key,开轮请求带的是它。
		require.Equal(t, "agent:main:openclaw:existing-session", params.SessionKey)
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": request.ID, "ok": true, "payload": map[string]any{"runId": params.IdempotencyKey, "status": "accepted"}})
		frames := []map[string]any{
			{"type": "event", "event": "agent", "seq": 2, "payload": map[string]any{"runId": "old-run", "sessionKey": params.SessionKey, "seq": 1, "stream": "assistant", "ts": 1, "data": map[string]any{"delta": "old"}}},
			{"type": "event", "event": "agent", "seq": 3, "payload": map[string]any{"runId": params.IdempotencyKey, "sessionKey": "other-session", "seq": 1, "stream": "assistant", "ts": 1, "data": map[string]any{"delta": "other"}}},
			{"type": "event", "event": "agent", "seq": 4, "payload": map[string]any{"runId": params.IdempotencyKey, "sessionKey": params.SessionKey, "seq": 1, "stream": "assistant", "ts": 1, "data": map[string]any{"delta": "A"}}},
			{"type": "event", "event": "agent", "seq": 5, "payload": map[string]any{"runId": params.IdempotencyKey, "sessionKey": params.SessionKey, "seq": 1, "stream": "assistant", "ts": 1, "data": map[string]any{"delta": "duplicate"}}},
			{"type": "event", "event": "agent", "seq": 6, "payload": map[string]any{"runId": params.IdempotencyKey, "sessionKey": params.SessionKey, "seq": 2, "stream": "assistant", "ts": 2, "data": map[string]any{"delta": "B"}}},
			{"type": "event", "event": "agent", "seq": 7, "payload": map[string]any{"runId": params.IdempotencyKey, "sessionKey": params.SessionKey, "seq": 3, "stream": "lifecycle", "ts": 3, "data": map[string]any{"phase": "end"}}},
		}
		for _, frame := range frames {
			runtimeWrite(t, conn, frame)
		}
	})
	runtime := New(runtimeResolver(t, gatewayURL))
	events, result, err := runtime.Run(context.Background(), agentruntime.RunRequest{
		Backend: runtimeBackend(), SessionID: 35, ProviderSessionID: "openclaw:existing-session", UserText: "next",
	})
	require.NoError(t, err)
	collected := collectRuntimeEvents(t, events)
	require.Len(t, collected, 3)
	assert.Equal(t, agentruntime.TextDelta{Text: "A"}, collected[0])
	assert.Equal(t, agentruntime.TextDelta{Text: "B"}, collected[1])
	_, ok := collected[2].(agentruntime.Done)
	assert.True(t, ok)
	assert.Equal(t, "agent:main:openclaw:existing-session", result.ProviderSessionID)
}

func TestRuntimeAbortIsIdempotentAndDistinctFromCompletion(t *testing.T) {
	var abortCalls atomic.Int32
	allowTerminal := make(chan struct{})
	gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
		runtimeHandshake(t, conn, connection)
		agentRequest := runtimeReadTurnRequest(t, conn)
		var params runtimeAgentParams
		require.NoError(t, json.Unmarshal(agentRequest.Params, &params))
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": agentRequest.ID, "ok": true, "payload": map[string]any{"runId": params.IdempotencyKey, "status": "accepted"}})
		abortRequest := runtimeReadRequest(t, conn)
		require.Equal(t, "chat.abort", abortRequest.Method)
		abortCalls.Add(1)
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": abortRequest.ID, "ok": true, "payload": map[string]any{"ok": true, "aborted": true, "runIds": []string{params.IdempotencyKey}}})
		<-allowTerminal
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "agent", "seq": 2,
			"payload": map[string]any{"runId": params.IdempotencyKey, "sessionKey": params.SessionKey, "seq": 1, "stream": "lifecycle", "ts": 1, "data": map[string]any{"phase": "end", "aborted": true, "stopReason": "stop"}},
		})
	})
	runtime := New(runtimeResolver(t, gatewayURL))
	events, result, err := runtime.Run(context.Background(), agentruntime.RunRequest{
		Backend: runtimeBackend(), SessionID: 36, UserText: "long task",
	})
	require.NoError(t, err)
	_, abortErr := runtime.Abort(context.Background(), 36, 0)
	require.NoError(t, abortErr)
	_, abortErr = runtime.Abort(context.Background(), 36, 0)
	require.NoError(t, abortErr)
	close(allowTerminal)
	collected := collectRuntimeEvents(t, events)
	assert.Equal(t, int32(1), abortCalls.Load())
	assert.ErrorIs(t, result.StopErr, agentruntime.ErrAborted)
	require.Len(t, collected, 1)
	_, ok := collected[0].(agentruntime.Done)
	assert.True(t, ok)
	_, abortErr = runtime.Abort(context.Background(), 36, 0)
	assert.ErrorIs(t, abortErr, agentruntime.ErrNoActiveTurn)
}

func TestRuntimeReconcilesAfterDisconnectWithoutResubmittingUserMessage(t *testing.T) {
	var agentCalls atomic.Int32
	var waitCalls atomic.Int32
	var runIDMu sync.Mutex
	runID := ""
	gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
		runtimeHandshake(t, conn, connection)
		if connection == 1 {
			request := runtimeReadTurnRequest(t, conn)
			agentCalls.Add(1)
			var params runtimeAgentParams
			require.NoError(t, json.Unmarshal(request.Params, &params))
			runIDMu.Lock()
			runID = params.IdempotencyKey
			runIDMu.Unlock()
			runtimeWrite(t, conn, map[string]any{"type": "res", "id": request.ID, "ok": true, "payload": map[string]any{"runId": params.IdempotencyKey, "status": "accepted"}})
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "reconcile"), time.Now().Add(time.Second))
			return
		}
		list := runtimeReadAfterSubscribe(t, conn)
		require.Equal(t, "exec.approval.list", list.Method)
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": list.ID, "ok": true, "payload": []any{}})
		request := runtimeReadRequest(t, conn)
		require.Equal(t, "agent.wait", request.Method)
		waitCalls.Add(1)
		var waitParams struct {
			RunID string `json:"runId"`
		}
		require.NoError(t, json.Unmarshal(request.Params, &waitParams))
		runIDMu.Lock()
		expectedRunID := runID
		runIDMu.Unlock()
		require.Equal(t, expectedRunID, waitParams.RunID)
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": request.ID, "ok": true, "payload": map[string]any{"runId": waitParams.RunID, "status": "ok", "stopReason": "stop"}})
	})
	runtime := New(runtimeResolver(t, gatewayURL))
	events, result, err := runtime.Run(context.Background(), agentruntime.RunRequest{
		Backend: runtimeBackend(), SessionID: 37, UserText: "submit once",
	})
	require.NoError(t, err)
	collected := collectRuntimeEvents(t, events)
	require.Len(t, collected, 1)
	_, ok := collected[0].(agentruntime.Done)
	assert.True(t, ok)
	assert.NoError(t, result.StopErr)
	assert.Equal(t, int32(1), agentCalls.Load())
	assert.Equal(t, int32(1), waitCalls.Load())
}

func TestRuntimeSurfacesTerminalGatewayError(t *testing.T) {
	gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
		runtimeHandshake(t, conn, connection)
		request := runtimeReadTurnRequest(t, conn)
		var params runtimeAgentParams
		require.NoError(t, json.Unmarshal(request.Params, &params))
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": request.ID, "ok": true, "payload": map[string]any{"runId": params.IdempotencyKey, "status": "accepted"}})
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "agent", "seq": 2,
			"payload": map[string]any{"runId": params.IdempotencyKey, "sessionKey": params.SessionKey, "seq": 1, "stream": "lifecycle", "ts": 1, "data": map[string]any{"phase": "error", "error": "model unavailable"}},
		})
	})
	runtime := New(runtimeResolver(t, gatewayURL))
	events, result, err := runtime.Run(context.Background(), agentruntime.RunRequest{
		Backend: runtimeBackend(), SessionID: 38, UserText: "fail",
	})
	require.NoError(t, err)
	collected := collectRuntimeEvents(t, events)
	require.Len(t, collected, 1)
	errorEvent, ok := collected[0].(agentruntime.ErrorEvent)
	require.True(t, ok)
	assert.Contains(t, errorEvent.Err.Error(), "model unavailable")
	assert.True(t, errors.Is(result.StopErr, errorEvent.Err) || result.StopErr.Error() == errorEvent.Err.Error())
}

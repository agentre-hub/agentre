package openclaw

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/openclawgateway"
)

type chatSendParams struct {
	Message        string `json:"message"`
	SessionKey     string `json:"sessionKey"`
	AgentID        string `json:"agentId"`
	Deliver        bool   `json:"deliver"`
	IdempotencyKey string `json:"idempotencyKey"`
	Model          string `json:"model"`
}

// 网关只在 chat.send 里把「发起这一轮的设备」记成审批 reviewer(chat-pg 的
// ApprovalReviewerDeviceId),agent 方法根本不绑 —— 用 agent 起轮，本设备就看不到
// 自己这一轮触发的 exec 审批(实测:admin 连接收得到,AgentRE 收不到,轮次卡死)。
func TestRunStartsTurnThroughChatSend(t *testing.T) {
	t.Run("Given no model override, when a turn starts, then chat.send carries the message, agent id and idempotency key", func(t *testing.T) {
		seen := make(chan runtimeRequestFrame, 1)
		gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
			runtimeHandshake(t, conn, connection)
			request := runtimeReadTurnRequest(t, conn)
			seen <- request
			runtimeWrite(t, conn, map[string]any{
				"type": "res", "id": request.ID, "ok": true,
				"payload": map[string]any{"runId": "run-chat-send", "status": "started"},
			})
			runtimeWrite(t, conn, map[string]any{
				"type": "event", "event": "agent", "seq": 2,
				"payload": map[string]any{
					"runId": "run-chat-send", "sessionKey": "agent:main:agentre:12:80",
					"seq": 1, "stream": "lifecycle", "ts": 1,
					"data": map[string]any{"phase": "end"},
				},
			})
			startupServe(conn, func(req runtimeRequestFrame) map[string]any {
				if req.Method == "agent.wait" {
					return map[string]any{"type": "res", "id": req.ID, "ok": true, "payload": map[string]any{"runId": req.ID, "status": "ok"}}
				}
				return map[string]any{"type": "res", "id": req.ID, "ok": true, "payload": map[string]any{}}
			})
		})

		runtime := New(runtimeResolver(t, gatewayURL))
		events, result, err := runtime.Run(context.Background(), agentruntime.RunRequest{
			Backend: runtimeBackend(), SessionID: 80, UserText: "start me through chat.send",
		})
		require.NoError(t, err)
		_ = collectRuntimeEvents(t, events)

		request := <-seen
		assert.Equal(t, "chat.send", request.Method)
		var params chatSendParams
		require.NoError(t, json.Unmarshal(request.Params, &params))
		assert.Equal(t, "start me through chat.send", params.Message)
		// 订阅应答里的规范化 key 直接用于 chat.send:带 agentId 时非规范 key 会被
		// 网关判成冲突(agentId "main" does not match session key ...)。
		assert.Equal(t, "agent:main:agentre:12:80", params.SessionKey)
		assert.Equal(t, "main", params.AgentID)
		assert.False(t, params.Deliver)
		assert.NotEmpty(t, params.IdempotencyKey)
		assert.Empty(t, params.Model, "chat.send has no model parameter")
		assert.Equal(t, "agent:main:agentre:12:80", result.ProviderSessionID)
	})

	// chat.send 的应答只有 {runId,status},不像 agent 会回 sessionKey —— 规范化后的
	// key 只能从事件里认领,在此之前不能把自己这一轮的事件当成别人的丢掉。
	t.Run("Given chat.send omits the canonical key, when events arrive, then they stream and the canonical key is adopted", func(t *testing.T) {
		gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
			runtimeHandshake(t, conn, connection)
			request := runtimeReadTurnRequest(t, conn)
			runtimeWrite(t, conn, map[string]any{
				"type": "res", "id": request.ID, "ok": true,
				"payload": map[string]any{"runId": "run-adopt", "status": "started"},
			})
			runtimeWrite(t, conn, map[string]any{
				"type": "event", "event": "agent", "seq": 2,
				"payload": map[string]any{
					"runId": "run-adopt", "sessionKey": "agent:main:agentre:12:81",
					"seq": 1, "stream": "assistant", "ts": 1,
					"data": map[string]any{"text": "adopted", "delta": "adopted"},
				},
			})
			runtimeWrite(t, conn, map[string]any{
				"type": "event", "event": "agent", "seq": 3,
				"payload": map[string]any{
					"runId": "run-adopt", "sessionKey": "agent:main:agentre:12:81",
					"seq": 2, "stream": "lifecycle", "ts": 2,
					"data": map[string]any{"phase": "end"},
				},
			})
			startupServe(conn, func(req runtimeRequestFrame) map[string]any {
				if req.Method == "agent.wait" {
					return map[string]any{"type": "res", "id": req.ID, "ok": true, "payload": map[string]any{"runId": req.ID, "status": "ok"}}
				}
				return map[string]any{"type": "res", "id": req.ID, "ok": true, "payload": map[string]any{}}
			})
		})

		runtime := New(runtimeResolver(t, gatewayURL))
		events, result, err := runtime.Run(context.Background(), agentruntime.RunRequest{
			Backend: runtimeBackend(), SessionID: 81, UserText: "adopt the canonical key",
		})
		require.NoError(t, err)
		collected := collectRuntimeEvents(t, events)

		require.NotEmpty(t, collected)
		delta, ok := collected[0].(agentruntime.TextDelta)
		require.True(t, ok, "want the assistant delta to survive the unknown canonical key, got %#v", collected[0])
		assert.Equal(t, "adopted", delta.Text)
		assert.Equal(t, "agent:main:agentre:12:81", result.ProviderSessionID)
	})

	// 审批事件可能先于任何 agent/chat 帧到达 —— 那时 canonical key 还没认领,
	// 审批卡不能因此被丢掉。
	t.Run("Given an approval arrives before any turn event, when it names the canonical key, then the card still reaches AgentRE", func(t *testing.T) {
		gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
			runtimeHandshake(t, conn, connection)
			request := runtimeReadTurnRequest(t, conn)
			runtimeWrite(t, conn, map[string]any{
				"type": "res", "id": request.ID, "ok": true,
				"payload": map[string]any{"runId": "run-approval-first", "status": "started"},
			})
			runtimeWrite(t, conn, map[string]any{
				"type": "event", "event": "exec.approval.requested", "seq": 2,
				"payload": map[string]any{
					"id": "appr-early", "createdAtMs": 1, "expiresAtMs": 0,
					"request": map[string]any{
						"command": "echo hi", "sessionKey": "agent:main:agentre:12:82",
						"agentId": "main", "allowedDecisions": []string{"allow-once", "deny"},
					},
				},
			})
			runtimeWrite(t, conn, map[string]any{
				"type": "event", "event": "agent", "seq": 3,
				"payload": map[string]any{
					"runId": "run-approval-first", "sessionKey": "agent:main:agentre:12:82",
					"seq": 1, "stream": "lifecycle", "ts": 1,
					"data": map[string]any{"phase": "end"},
				},
			})
			startupServe(conn, func(req runtimeRequestFrame) map[string]any {
				if req.Method == "agent.wait" {
					return map[string]any{"type": "res", "id": req.ID, "ok": true, "payload": map[string]any{"runId": req.ID, "status": "ok"}}
				}
				return map[string]any{"type": "res", "id": req.ID, "ok": true, "payload": map[string]any{}}
			})
		})

		runtime := New(runtimeResolver(t, gatewayURL))
		events, _, err := runtime.Run(context.Background(), agentruntime.RunRequest{
			Backend: runtimeBackend(), SessionID: 82, UserText: "approval first",
		})
		require.NoError(t, err)
		collected := collectRuntimeEvents(t, events)

		var requested *agentruntime.ExecApprovalRequested
		for _, event := range collected {
			if value, ok := event.(agentruntime.ExecApprovalRequested); ok {
				requested = &value
			}
		}
		require.NotNil(t, requested, "want the approval card, got %#v", collected)
		assert.Equal(t, "appr-early", requested.ID)
		assert.Equal(t, "echo hi", requested.CommandText)
	})

	// model override 只有 admin 能下发,而 chat.send 根本没有 model 参数 —— 这种情况
	// 继续走 agent(admin 连接本来就能看见全部审批,不依赖 reviewer 绑定)。
	t.Run("Given an admin-granted model override, when a turn starts, then the agent RPC carries the model", func(t *testing.T) {
		seen := make(chan runtimeRequestFrame, 1)
		gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
			runtimeHandshakeWithScopes(t, conn, connection, append(
				append([]string(nil), openclawgateway.RequiredOperatorScopes...), "operator.admin"))
			request := runtimeReadTurnRequest(t, conn)
			seen <- request
			runtimeWrite(t, conn, map[string]any{
				"type": "res", "id": request.ID, "ok": true,
				"payload": map[string]any{"runId": "run-admin", "status": "accepted", "sessionKey": "agent:main:agentre:12:83"},
			})
			runtimeWrite(t, conn, map[string]any{
				"type": "event", "event": "agent", "seq": 2,
				"payload": map[string]any{
					"runId": "run-admin", "sessionKey": "agent:main:agentre:12:83",
					"seq": 1, "stream": "lifecycle", "ts": 1,
					"data": map[string]any{"phase": "end"},
				},
			})
			startupServe(conn, func(req runtimeRequestFrame) map[string]any {
				if req.Method == "agent.wait" {
					return map[string]any{"type": "res", "id": req.ID, "ok": true, "payload": map[string]any{"runId": req.ID, "status": "ok"}}
				}
				return map[string]any{"type": "res", "id": req.ID, "ok": true, "payload": map[string]any{}}
			})
		})

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		runtime := New(runtimeResolver(t, gatewayURL))
		events, result, err := runtime.Run(ctx, agentruntime.RunRequest{
			Backend: runtimeBackend(), SessionID: 83, UserText: "admin model override",
		})
		require.NoError(t, err)
		_ = collectRuntimeEvents(t, events)

		request := <-seen
		assert.Equal(t, "agent", request.Method)
		var params runtimeAgentParams
		require.NoError(t, json.Unmarshal(request.Params, &params))
		assert.Equal(t, "anthropic/claude-sonnet-4-6", params.Model)
		assert.Equal(t, "anthropic/claude-sonnet-4-6", result.Model)
	})
}

// chat.send 起的轮次不会广播 agent/chat 事件:网关只把它们发给
// sessions.messages.subscribe 的订阅者(server-chat 的 sendAgentPayload)。
// 不订阅就等于整轮没有任何流式输出,只剩 agent.wait 的终态。
func TestRunSubscribesBeforeChatSend(t *testing.T) {
	t.Run("Given the Gateway advertises the subscribe method, when a turn starts, then AgentRE subscribes before sending", func(t *testing.T) {
		methods := make(chan string, 8)
		subscribeParams := make(chan json.RawMessage, 1)
		sendParams := make(chan json.RawMessage, 1)
		gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
			runtimeHandshake(t, conn, connection)
			list := runtimeReadRequest(t, conn)
			require.Equal(t, "exec.approval.list", list.Method)
			runtimeWrite(t, conn, map[string]any{"type": "res", "id": list.ID, "ok": true, "payload": []any{}})
			subscribe := runtimeReadRequest(t, conn)
			methods <- subscribe.Method
			subscribeParams <- subscribe.Params
			runtimeWrite(t, conn, map[string]any{
				"type": "res", "id": subscribe.ID, "ok": true,
				"payload": map[string]any{"subscribed": true, "key": "agent:main:agentre:12:84"},
			})
			send := runtimeReadRequest(t, conn)
			methods <- send.Method
			sendParams <- send.Params
			runtimeWrite(t, conn, map[string]any{
				"type": "res", "id": send.ID, "ok": true,
				"payload": map[string]any{"runId": "run-sub", "status": "started"},
			})
			runtimeWrite(t, conn, map[string]any{
				"type": "event", "event": "agent", "seq": 2,
				"payload": map[string]any{
					"runId": "run-sub", "sessionKey": "agent:main:agentre:12:84",
					"seq": 1, "stream": "lifecycle", "ts": 1,
					"data": map[string]any{"phase": "end"},
				},
			})
			startupServe(conn, func(req runtimeRequestFrame) map[string]any {
				return map[string]any{"type": "res", "id": req.ID, "ok": true, "payload": map[string]any{}}
			})
		})

		runtime := New(runtimeResolver(t, gatewayURL))
		events, _, err := runtime.Run(context.Background(), agentruntime.RunRequest{
			Backend: runtimeBackend(), SessionID: 84, UserText: "subscribe first",
		})
		require.NoError(t, err)
		_ = collectRuntimeEvents(t, events)

		assert.Equal(t, "sessions.messages.subscribe", <-methods)
		assert.Equal(t, "chat.send", <-methods)
		var params struct {
			Key     string `json:"key"`
			AgentID string `json:"agentId"`
		}
		require.NoError(t, json.Unmarshal(<-subscribeParams, &params))
		assert.Equal(t, "agentre:12:84", params.Key)
		assert.Equal(t, "main", params.AgentID)
		var send chatSendParams
		require.NoError(t, json.Unmarshal(<-sendParams, &send))
		assert.Equal(t, "agent:main:agentre:12:84", send.SessionKey,
			"chat.send 必须用订阅应答回报的规范化 key")
	})

	// 老网关没有 sessions.messages.subscribe:订阅不了就不能用 chat.send,否则整轮
	// 收不到任何事件 —— 退回 agent(它会广播),宁可丢掉审批可见性也不能丢流式。
	t.Run("Given the Gateway lacks the subscribe method, when a turn starts, then the agent RPC is used", func(t *testing.T) {
		seen := make(chan runtimeRequestFrame, 1)
		gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
			runtimeHandshakeWithoutMethods(t, conn, connection, "sessions.messages.subscribe")
			request := runtimeReadTurnRequest(t, conn)
			seen <- request
			runtimeWrite(t, conn, map[string]any{
				"type": "res", "id": request.ID, "ok": true,
				"payload": map[string]any{"runId": "run-legacy", "status": "accepted", "sessionKey": "agent:main:agentre:12:85"},
			})
			runtimeWrite(t, conn, map[string]any{
				"type": "event", "event": "agent", "seq": 2,
				"payload": map[string]any{
					"runId": "run-legacy", "sessionKey": "agent:main:agentre:12:85",
					"seq": 1, "stream": "lifecycle", "ts": 1,
					"data": map[string]any{"phase": "end"},
				},
			})
			startupServe(conn, func(req runtimeRequestFrame) map[string]any {
				return map[string]any{"type": "res", "id": req.ID, "ok": true, "payload": map[string]any{}}
			})
		})

		runtime := New(runtimeResolver(t, gatewayURL))
		events, _, err := runtime.Run(context.Background(), agentruntime.RunRequest{
			Backend: runtimeBackend(), SessionID: 85, UserText: "legacy gateway",
		})
		require.NoError(t, err)
		_ = collectRuntimeEvents(t, events)

		assert.Equal(t, "agent", (<-seen).Method)
	})
}

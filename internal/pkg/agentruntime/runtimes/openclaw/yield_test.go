package openclaw

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// yieldGateway 是让出场景下假网关的一条连接:按顺序写帧,帧序号自增。
type yieldGateway struct {
	t    *testing.T
	conn *websocket.Conn
	seq  int
}

func (g *yieldGateway) event(name string, payload map[string]any) {
	g.t.Helper()
	g.seq++
	runtimeWrite(g.t, g.conn, map[string]any{"type": "event", "event": name, "seq": g.seq, "payload": payload})
}

func (g *yieldGateway) agent(runID, sessionKey string, seq int, stream string, data map[string]any) {
	g.t.Helper()
	g.event("agent", map[string]any{"runId": runID, "sessionKey": sessionKey, "seq": seq, "stream": stream, "data": data})
}

func (g *yieldGateway) chat(runID, sessionKey string, seq int, fields map[string]any) {
	g.t.Helper()
	payload := map[string]any{"runId": runID, "sessionKey": sessionKey, "seq": seq}
	for key, value := range fields {
		payload[key] = value
	}
	g.event("chat", payload)
}

// yieldedEnd 复刻 OpenClaw 2026.9.5 在 sessions_yield 后给父 run 的 lifecycle 终态。
func yieldedEnd() map[string]any {
	return map[string]any{"phase": "end", "yielded": true, "livenessState": "paused", "stopReason": "end_turn"}
}

// startYieldTurn 完成握手与开轮,返回父 run 的 runId 与规范化会话 key。
func startYieldTurn(t *testing.T, conn *websocket.Conn, connection int) (runID, sessionKey string) {
	t.Helper()
	runtimeHandshake(t, conn, connection)
	request := runtimeReadTurnRequest(t, conn)
	var params runtimeAgentParams
	require.NoError(t, json.Unmarshal(request.Params, &params))
	runtimeWrite(t, conn, map[string]any{"type": "res", "id": request.ID, "ok": true, "payload": map[string]any{"runId": params.IdempotencyKey, "status": "accepted"}})
	return params.IdempotencyKey, params.SessionKey
}

// drainUntilClosed 让假网关保持连接直到 runtime 收轮关连接,不对后续请求做断言。
func drainUntilClosed(conn *websocket.Conn) {
	for {
		var ignored json.RawMessage
		if conn.ReadJSON(&ignored) != nil {
			return
		}
	}
}

func followUpRunID(sessionKey, child string) string {
	return "announce:requester-settle:main:" + sessionKey + ":" + child + ":yield-1"
}

func nextEvent(t *testing.T, events <-chan agentruntime.Event) agentruntime.Event {
	t.Helper()
	select {
	case event, ok := <-events:
		require.True(t, ok, "event stream closed early")
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for OpenClaw runtime event")
		return nil
	}
}

func readableFollowUpError(t *testing.T, event agentruntime.Event) {
	t.Helper()
	errorEvent, ok := event.(agentruntime.ErrorEvent)
	require.True(t, ok, "expected ErrorEvent, got %#v", event)
	want := i18n.T(context.Background(), code.OpenClawYieldFollowUpUnconfirmed)
	require.NotEmpty(t, want)
	assert.Equal(t, want, errorEvent.Err.Error())
}

func TestRuntimeYieldedTurnStaysRunningUntilFollowUpRunEnds(t *testing.T) {
	t.Run("Given the parent run yields, when follow-up runs of the same session stream and one of them yields again, then the turn delivers their output and ends only on a non-yielded terminal", func(t *testing.T) {
		gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
			parent, sessionKey := startYieldTurn(t, conn, connection)
			follow1 := followUpRunID(sessionKey, "child-1")
			follow2 := followUpRunID(sessionKey, "child-2")
			g := &yieldGateway{t: t, conn: conn, seq: 1}
			g.agent(parent, sessionKey, 1, "assistant", map[string]any{"delta": "Waiting for the subagent."})
			g.chat(parent, sessionKey, 1, map[string]any{"state": "delta", "deltaText": "Waiting for the subagent."})
			g.agent(parent, sessionKey, 2, "tool", map[string]any{"phase": "start", "name": "sessions_yield", "toolCallId": "yield-call"})
			g.agent(parent, sessionKey, 3, "lifecycle", yieldedEnd())
			// 网关的 chat seq 沿用 agent seq,chat 帧之间天然有缺口 —— 这不是丢帧。
			g.chat(parent, sessionKey, 3, map[string]any{"state": "final", "yielded": true})
			// 同会话的心跳 run 与别的会话的 run 都不是本轮的后续。
			g.event("agent", map[string]any{"runId": "heartbeat-run", "sessionKey": sessionKey, "seq": 1, "stream": "assistant", "isHeartbeat": true, "data": map[string]any{"delta": "HEARTBEAT"}})
			g.agent(follow1, "agent:main:somewhere-else", 1, "assistant", map[string]any{"delta": "OTHER SESSION"})
			g.agent(follow1, sessionKey, 1, "lifecycle", map[string]any{"phase": "start"})
			g.agent(follow1, sessionKey, 2, "assistant", map[string]any{"delta": "Still waiting."})
			// 后续 run 自己也让出:只有 chat final 带 yielded。
			g.chat(follow1, sessionKey, 3, map[string]any{"state": "final", "yielded": true})
			// 已让出的 run 的迟到帧不能把它重新认作后续 run。
			g.agent(parent, sessionKey, 3, "assistant", map[string]any{"delta": "late parent"})
			g.agent(follow2, sessionKey, 1, "assistant", map[string]any{"delta": "FINAL: SUBAGENT-RESULT-42"})
			g.agent(follow2, sessionKey, 2, "lifecycle", map[string]any{"phase": "end"})
			g.chat(follow2, sessionKey, 2, map[string]any{"state": "final"})
			drainUntilClosed(conn)
		})
		runtime := New(runtimeResolver(t, gatewayURL))
		events, result, err := runtime.Run(context.Background(), agentruntime.RunRequest{
			Backend: runtimeBackend(), SessionID: 61, UserText: "spawn a subagent and wait",
		})
		require.NoError(t, err)
		collected := collectRuntimeEvents(t, events)
		require.Equal(t, []agentruntime.Event{
			agentruntime.TextDelta{Text: "Waiting for the subagent."},
			agentruntime.ToolCall{ID: "yield-call", Name: "sessions_yield", Input: json.RawMessage(`{}`)},
			agentruntime.TextDelta{Text: "Still waiting."},
			agentruntime.TextDelta{Text: "FINAL: SUBAGENT-RESULT-42"},
			agentruntime.Done{},
		}, collected)
		assert.NoError(t, result.StopErr)
	})
}

func TestRuntimeAbortDuringYieldedTurn(t *testing.T) {
	t.Run("Given the parent yielded and no follow-up run has started, when the user stops, then chat.abort targets the session and the turn ends as aborted", func(t *testing.T) {
		abortParams := make(chan map[string]any, 1)
		gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
			parent, sessionKey := startYieldTurn(t, conn, connection)
			g := &yieldGateway{t: t, conn: conn, seq: 1}
			g.agent(parent, sessionKey, 1, "lifecycle", yieldedEnd())
			// 让出期间出现的审批照常呈现。
			g.event("exec.approval.requested", map[string]any{
				"id": "approval-yield", "createdAtMs": int64(100), "expiresAtMs": time.Now().Add(time.Minute).UnixMilli(),
				"request": map[string]any{"command": "pwd", "allowedDecisions": []string{"allow-once", "deny"}, "sessionKey": sessionKey},
			})
			request := runtimeReadRequest(t, conn)
			require.Equal(t, "chat.abort", request.Method)
			var params map[string]any
			require.NoError(t, json.Unmarshal(request.Params, &params))
			abortParams <- params
			runtimeWrite(t, conn, map[string]any{"type": "res", "id": request.ID, "ok": true, "payload": map[string]any{"ok": true, "aborted": false}})
			drainUntilClosed(conn)
		})
		runtime := New(runtimeResolver(t, gatewayURL))
		events, result, err := runtime.Run(context.Background(), agentruntime.RunRequest{
			Backend: runtimeBackend(), SessionID: 62, UserText: "yield then stop",
		})
		require.NoError(t, err)
		requested, ok := nextEvent(t, events).(agentruntime.ExecApprovalRequested)
		require.True(t, ok, "the yielded turn must stay open and surface approvals")
		assert.Equal(t, "approval-yield", requested.ID)

		_, abortErr := runtime.Abort(context.Background(), 62, 0)
		require.NoError(t, abortErr)
		rest := collectRuntimeEvents(t, events)
		require.NotEmpty(t, rest)
		_, ok = rest[len(rest)-1].(agentruntime.Done)
		assert.True(t, ok)
		assert.ErrorIs(t, result.StopErr, agentruntime.ErrAborted)
		params := <-abortParams
		assert.Equal(t, "agent:main:agentre:12:62", params["sessionKey"])
		assert.NotContains(t, params, "runId", "no run is known yet; abort whatever the session is running")
	})

	t.Run("Given a follow-up run is streaming, when the user stops, then chat.abort targets that run and its aborted terminal ends the turn", func(t *testing.T) {
		abortParams := make(chan map[string]any, 1)
		gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
			parent, sessionKey := startYieldTurn(t, conn, connection)
			follow := followUpRunID(sessionKey, "child-1")
			g := &yieldGateway{t: t, conn: conn, seq: 1}
			g.agent(parent, sessionKey, 1, "lifecycle", yieldedEnd())
			g.agent(follow, sessionKey, 1, "assistant", map[string]any{"delta": "working"})
			request := runtimeReadRequest(t, conn)
			require.Equal(t, "chat.abort", request.Method)
			var params map[string]any
			require.NoError(t, json.Unmarshal(request.Params, &params))
			abortParams <- params
			runtimeWrite(t, conn, map[string]any{"type": "res", "id": request.ID, "ok": true, "payload": map[string]any{"ok": true, "aborted": true}})
			g.agent(follow, sessionKey, 2, "lifecycle", map[string]any{"phase": "end", "aborted": true})
			drainUntilClosed(conn)
		})
		runtime := New(runtimeResolver(t, gatewayURL))
		events, result, err := runtime.Run(context.Background(), agentruntime.RunRequest{
			Backend: runtimeBackend(), SessionID: 63, UserText: "yield then stop the follow-up",
		})
		require.NoError(t, err)
		assert.Equal(t, agentruntime.TextDelta{Text: "working"}, nextEvent(t, events))

		_, abortErr := runtime.Abort(context.Background(), 63, 0)
		require.NoError(t, abortErr)
		rest := collectRuntimeEvents(t, events)
		require.Equal(t, []agentruntime.Event{agentruntime.Done{}}, rest)
		assert.ErrorIs(t, result.StopErr, agentruntime.ErrAborted)
		params := <-abortParams
		assert.Equal(t, followUpRunID("agent:main:agentre:12:63", "child-1"), params["runId"])
	})
}

// reconnectYieldGateway: 第一条连接跑 firstConn 后断开,第二条连接先应答补订与
// exec.approval.list,再交给 secondConn。
func reconnectYieldGateway(t *testing.T, firstConn func(*yieldGateway, string, string), secondConn func(*websocket.Conn, string)) string {
	t.Helper()
	var parent, sessionKey string
	return runtimeGateway(t, func(conn *websocket.Conn, connection int) {
		if connection == 1 {
			parent, sessionKey = startYieldTurn(t, conn, connection)
			firstConn(&yieldGateway{t: t, conn: conn, seq: 1}, parent, sessionKey)
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "yield reconcile"), time.Now().Add(time.Second))
			return
		}
		runtimeHandshake(t, conn, connection)
		list := runtimeReadAfterSubscribe(t, conn)
		require.Equal(t, "exec.approval.list", list.Method)
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": list.ID, "ok": true, "payload": []any{}})
		secondConn(conn, sessionKey)
	})
}

func TestRuntimeReconnectDuringYieldedTurn(t *testing.T) {
	t.Run("Given the parent yielded and no follow-up run is known, when the connection drops, then the turn ends with a readable error instead of waiting forever", func(t *testing.T) {
		gatewayURL := reconnectYieldGateway(t, func(g *yieldGateway, parent, sessionKey string) {
			g.agent(parent, sessionKey, 1, "lifecycle", yieldedEnd())
		}, func(conn *websocket.Conn, _ string) {
			drainUntilClosed(conn)
		})
		runtime := New(runtimeResolver(t, gatewayURL))
		events, result, err := runtime.Run(context.Background(), agentruntime.RunRequest{
			Backend: runtimeBackend(), SessionID: 64, UserText: "yield then lose the connection",
		})
		require.NoError(t, err)
		collected := collectRuntimeEvents(t, events)
		require.Len(t, collected, 1)
		readableFollowUpError(t, collected[0])
		require.Error(t, result.StopErr)
	})

	t.Run("Given a follow-up run is streaming, when the connection drops, then agent.wait reconciles that follow-up run", func(t *testing.T) {
		waitedRunID := make(chan string, 1)
		gatewayURL := reconnectYieldGateway(t, func(g *yieldGateway, parent, sessionKey string) {
			g.agent(parent, sessionKey, 1, "lifecycle", yieldedEnd())
			g.agent(followUpRunID(sessionKey, "child-1"), sessionKey, 1, "assistant", map[string]any{"delta": "partial"})
		}, func(conn *websocket.Conn, _ string) {
			wait := runtimeReadRequest(t, conn)
			require.Equal(t, "agent.wait", wait.Method)
			var params struct {
				RunID string `json:"runId"`
			}
			require.NoError(t, json.Unmarshal(wait.Params, &params))
			waitedRunID <- params.RunID
			runtimeWrite(t, conn, map[string]any{"type": "res", "id": wait.ID, "ok": true, "payload": map[string]any{"runId": params.RunID, "status": "ok"}})
			drainUntilClosed(conn)
		})
		runtime := New(runtimeResolver(t, gatewayURL))
		events, result, err := runtime.Run(context.Background(), agentruntime.RunRequest{
			Backend: runtimeBackend(), SessionID: 65, UserText: "yield, stream, reconnect",
		})
		require.NoError(t, err)
		collected := collectRuntimeEvents(t, events)
		require.Equal(t, []agentruntime.Event{agentruntime.TextDelta{Text: "partial"}, agentruntime.Done{}}, collected)
		assert.NoError(t, result.StopErr)
		assert.Equal(t, followUpRunID("agent:main:agentre:12:65", "child-1"), <-waitedRunID)
	})

	t.Run("Given the parent yielded while disconnected, when agent.wait reports the yield, then the follow-up cannot be confirmed and the turn ends with a readable error", func(t *testing.T) {
		gatewayURL := reconnectYieldGateway(t, func(*yieldGateway, string, string) {}, func(conn *websocket.Conn, _ string) {
			wait := runtimeReadRequest(t, conn)
			require.Equal(t, "agent.wait", wait.Method)
			runtimeWrite(t, conn, map[string]any{"type": "res", "id": wait.ID, "ok": true, "payload": map[string]any{"status": "ok", "yielded": true, "livenessState": "paused", "stopReason": "end_turn"}})
			drainUntilClosed(conn)
		})
		runtime := New(runtimeResolver(t, gatewayURL))
		events, _, err := runtime.Run(context.Background(), agentruntime.RunRequest{
			Backend: runtimeBackend(), SessionID: 66, UserText: "yield while offline",
		})
		require.NoError(t, err)
		collected := collectRuntimeEvents(t, events)
		require.Len(t, collected, 1)
		readableFollowUpError(t, collected[0])
	})
}

func TestRuntimeYieldObservedByAgentWaitOnLiveConnection(t *testing.T) {
	// 真实网关(2026.9.5)给同一个 run 的 payload seq 本来就带缺口 —— 它按 run 编号,
	// 但只把其中一部分事件投给这条连接。缺口让 consume 去 reconcile,reconcile 的
	// agent.wait 会一直挂到 run 落终态,于是这次轮询拿回的正是「已让出」。此刻连接
	// 完好,让出帧就排在收件箱里,后续 run 也还会照常推流。
	t.Run("Given a per-run seq gap sends reconcile into agent.wait, when that call returns the yielded terminal on a live connection, then the turn keeps waiting and delivers the follow-up run's output", func(t *testing.T) {
		gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
			parent, sessionKey := startYieldTurn(t, conn, connection)
			follow := followUpRunID(sessionKey, "child-1")
			g := &yieldGateway{t: t, conn: conn, seq: 1}
			g.agent(parent, sessionKey, 6, "assistant", map[string]any{"delta": "Waiting for the subagent."})
			g.agent(parent, sessionKey, 8, "tool", map[string]any{"phase": "start", "name": "sessions_yield", "toolCallId": "yield-call"})
			wait := runtimeReadRequest(t, conn)
			require.Equal(t, "agent.wait", wait.Method)
			// agent.wait 还挂着的时候 run 让出:让出帧先到,应答随后。
			g.agent(parent, sessionKey, 20, "lifecycle", yieldedEnd())
			g.chat(parent, sessionKey, 20, map[string]any{"state": "final", "yielded": true})
			runtimeWrite(t, conn, map[string]any{"type": "res", "id": wait.ID, "ok": true, "payload": map[string]any{
				"runId": parent, "status": "ok", "yielded": true, "livenessState": "paused", "stopReason": "end_turn",
			}})
			g.agent(follow, sessionKey, 1, "assistant", map[string]any{"delta": "FINAL: SUBAGENT-RESULT-77"})
			g.agent(follow, sessionKey, 2, "lifecycle", map[string]any{"phase": "end"})
			drainUntilClosed(conn)
		})
		runtime := New(runtimeResolver(t, gatewayURL))
		events, result, err := runtime.Run(context.Background(), agentruntime.RunRequest{
			Backend: runtimeBackend(), SessionID: 67, UserText: "spawn a subagent and yield",
		})
		require.NoError(t, err)
		collected := collectRuntimeEvents(t, events)
		require.Equal(t, []agentruntime.Event{
			agentruntime.TextDelta{Text: "Waiting for the subagent."},
			agentruntime.ToolCall{ID: "yield-call", Name: "sessions_yield", Input: json.RawMessage(`{}`)},
			agentruntime.TextDelta{Text: "FINAL: SUBAGENT-RESULT-77"},
			agentruntime.Done{},
		}, collected)
		assert.NoError(t, result.StopErr)
	})
}

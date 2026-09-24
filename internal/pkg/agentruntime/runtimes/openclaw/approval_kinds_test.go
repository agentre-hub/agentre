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
)

// TestRuntimePluginApprovalRequestedAndExpiresAtTurnEnd 覆盖 plugin.approval.*：
// 事件译成 ApprovalKind=plugin 的泛化审批块,已脱敏内容只含 PluginName/ToolName/
// Description,不答复时随本轮结束过期(撤回/过期语义与 exec 共用,不重复测)。
func TestRuntimePluginApprovalRequestedAndExpiresAtTurnEnd(t *testing.T) {
	approvalSent := make(chan struct{})
	gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
		runtimeHandshake(t, conn, connection)
		agentRequest := runtimeReadTurnRequest(t, conn)
		var params runtimeAgentParams
		require.NoError(t, json.Unmarshal(agentRequest.Params, &params))
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": agentRequest.ID, "ok": true, "payload": map[string]any{"runId": params.IdempotencyKey, "status": "accepted"}})
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "plugin.approval.requested", "seq": 2,
			"payload": map[string]any{
				"approvalKind": "plugin", "id": "approval-plugin-1", "createdAtMs": int64(100), "expiresAtMs": time.Now().Add(time.Minute).UnixMilli(),
				"request": map[string]any{
					"pluginId": "whatsapp", "toolName": "send_message", "description": "Send a WhatsApp message",
					"allowedDecisions": []string{"allow-once", "allow-always", "deny"}, "agentId": "main", "sessionKey": params.SessionKey,
				},
			},
		})
		close(approvalSent)
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "agent", "seq": 3,
			"payload": map[string]any{"runId": params.IdempotencyKey, "sessionKey": params.SessionKey, "seq": 1, "stream": "lifecycle", "data": map[string]any{"phase": "end"}},
		})
	})

	runtime := New(runtimeResolver(t, gatewayURL))
	events, _, err := runtime.Run(context.Background(), agentruntime.RunRequest{
		Backend: runtimeBackend(), SessionID: 71, UserText: "send a whatsapp message",
	})
	require.NoError(t, err)
	<-approvalSent
	requested, ok := (<-events).(agentruntime.ExecApprovalRequested)
	require.True(t, ok)
	assert.Equal(t, "approval-plugin-1", requested.ID)
	assert.Equal(t, agentruntime.ApprovalKindPlugin, requested.ApprovalKind)
	assert.Equal(t, "whatsapp", requested.PluginName)
	assert.Equal(t, "send_message", requested.ToolName)
	assert.Equal(t, "Send a WhatsApp message", requested.Description)
	assert.Equal(t, []string{"allow-once", "allow-always", "deny"}, requested.AllowedDecisions)
	assert.Empty(t, requested.CommandText, "plugin approvals carry no exec command text")

	collected := collectRuntimeEvents(t, events)
	require.Len(t, collected, 2)
	expired, ok := collected[0].(agentruntime.ExecApprovalResolved)
	require.True(t, ok)
	assert.Equal(t, "approval-plugin-1", expired.ID)
	assert.Equal(t, "expired", expired.Status)
	_, ok = collected[1].(agentruntime.Done)
	assert.True(t, ok)
}

// TestRuntimePluginApprovalResolvedViaPluginMethod 覆盖 plugin 审批「可作答」:
// answer 经 plugin.approval.resolve(与 exec 平行的专属方法,不是 system-agent
// 那个不区分类别的 approval.resolve),终态经 plugin.approval.resolved 到达。
func TestRuntimePluginApprovalResolvedViaPluginMethod(t *testing.T) {
	approvalSent := make(chan struct{})
	allowRunFinish := make(chan struct{})
	gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
		runtimeHandshake(t, conn, connection)
		agentRequest := runtimeReadTurnRequest(t, conn)
		var params runtimeAgentParams
		require.NoError(t, json.Unmarshal(agentRequest.Params, &params))
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": agentRequest.ID, "ok": true, "payload": map[string]any{"runId": params.IdempotencyKey, "status": "accepted"}})
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "plugin.approval.requested", "seq": 2,
			"payload": map[string]any{
				"approvalKind": "plugin", "id": "approval-plugin-2", "createdAtMs": int64(100), "expiresAtMs": time.Now().Add(time.Minute).UnixMilli(),
				"request": map[string]any{
					"pluginId": "whatsapp", "toolName": "send_message",
					"allowedDecisions": []string{"allow-once", "deny"}, "agentId": "main", "sessionKey": params.SessionKey,
				},
			},
		})
		close(approvalSent)
		resolve := runtimeReadRequest(t, conn)
		require.Equal(t, "plugin.approval.resolve", resolve.Method)
		var decision map[string]any
		require.NoError(t, json.Unmarshal(resolve.Params, &decision))
		require.Equal(t, map[string]any{"id": "approval-plugin-2", "decision": "allow-once"}, decision)
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": resolve.ID, "ok": true, "payload": map[string]any{"ok": true}})
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "plugin.approval.resolved", "seq": 3,
			"payload": map[string]any{"id": "approval-plugin-2", "decision": "allow-once", "resolvedBy": "device-1", "ts": int64(150), "request": map[string]any{"sessionKey": params.SessionKey}},
		})
		<-allowRunFinish
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "agent", "seq": 4,
			"payload": map[string]any{"runId": params.IdempotencyKey, "sessionKey": params.SessionKey, "seq": 1, "stream": "lifecycle", "data": map[string]any{"phase": "end"}},
		})
	})

	runtime := New(runtimeResolver(t, gatewayURL))
	events, _, err := runtime.Run(context.Background(), agentruntime.RunRequest{
		Backend: runtimeBackend(), SessionID: 74, UserText: "send a whatsapp message",
	})
	require.NoError(t, err)
	<-approvalSent
	requested, ok := (<-events).(agentruntime.ExecApprovalRequested)
	require.True(t, ok)
	assert.Equal(t, agentruntime.ApprovalKindPlugin, requested.ApprovalKind)

	resolution, err := runtime.ResolveExecApproval(context.Background(), 74, "approval-plugin-2", "allow-once")
	require.NoError(t, err)
	assert.Equal(t, "resolved", resolution.Status)
	assert.Equal(t, "allow-once", resolution.Decision)

	resolved, ok := (<-events).(agentruntime.ExecApprovalResolved)
	require.True(t, ok)
	assert.Equal(t, "resolved", resolved.Status)
	assert.Equal(t, "allow-once", resolved.Decision)

	close(allowRunFinish)
	final := collectRuntimeEvents(t, events)
	require.Len(t, final, 1)
	_, ok = final[0].(agentruntime.Done)
	assert.True(t, ok)
}

// TestRuntimeSystemAgentApprovalRequestedAndResolvedViaGenericMethod 覆盖
// openclaw.approval.*：system-agent 审批经不区分类别的 approval.resolve 作答
// (网关没有给 system-agent 单独的 resolve RPC),终态经 openclaw.approval.resolved 到达。
func TestRuntimeSystemAgentApprovalRequestedAndResolvedViaGenericMethod(t *testing.T) {
	approvalSent := make(chan struct{})
	allowRunFinish := make(chan struct{})
	gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
		runtimeHandshake(t, conn, connection)
		agentRequest := runtimeReadTurnRequest(t, conn)
		var params runtimeAgentParams
		require.NoError(t, json.Unmarshal(agentRequest.Params, &params))
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": agentRequest.ID, "ok": true, "payload": map[string]any{"runId": params.IdempotencyKey, "status": "accepted"}})
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "openclaw.approval.requested", "seq": 2,
			"payload": map[string]any{
				"approvalKind": "system-agent", "id": "approval-sysagent-1", "createdAtMs": int64(100), "expiresAtMs": time.Now().Add(time.Minute).UnixMilli(),
				"request": map[string]any{
					"title": "OpenClaw change", "description": "Update the default model to gpt-6",
					"allowedDecisions": []string{"allow-once", "deny"}, "agentId": "main", "sessionKey": params.SessionKey,
				},
			},
		})
		close(approvalSent)
		resolve := runtimeReadRequest(t, conn)
		require.Equal(t, "approval.resolve", resolve.Method)
		var decision map[string]any
		require.NoError(t, json.Unmarshal(resolve.Params, &decision))
		// approval.resolve 的官方 closed schema 要求 kind:缺了它网关会拒掉这次作答。
		require.Equal(t, map[string]any{"id": "approval-sysagent-1", "kind": "system-agent", "decision": "allow-once"}, decision)
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": resolve.ID, "ok": true, "payload": map[string]any{"ok": true}})
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "openclaw.approval.resolved", "seq": 3,
			"payload": map[string]any{"id": "approval-sysagent-1", "decision": "allow-once", "resolvedBy": "device-1", "ts": int64(150), "request": map[string]any{"sessionKey": params.SessionKey}},
		})
		<-allowRunFinish
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "agent", "seq": 4,
			"payload": map[string]any{"runId": params.IdempotencyKey, "sessionKey": params.SessionKey, "seq": 1, "stream": "lifecycle", "data": map[string]any{"phase": "end"}},
		})
	})

	runtime := New(runtimeResolver(t, gatewayURL))
	events, _, err := runtime.Run(context.Background(), agentruntime.RunRequest{
		Backend: runtimeBackend(), SessionID: 72, UserText: "change the default model",
	})
	require.NoError(t, err)
	<-approvalSent
	requested, ok := (<-events).(agentruntime.ExecApprovalRequested)
	require.True(t, ok)
	assert.Equal(t, "approval-sysagent-1", requested.ID)
	assert.Equal(t, agentruntime.ApprovalKindSystemAgent, requested.ApprovalKind)
	assert.Equal(t, "Update the default model to gpt-6", requested.Description)
	assert.Equal(t, []string{"allow-once", "deny"}, requested.AllowedDecisions)

	resolution, err := runtime.ResolveExecApproval(context.Background(), 72, "approval-sysagent-1", "allow-once")
	require.NoError(t, err)
	assert.Equal(t, "resolved", resolution.Status)
	assert.Equal(t, "allow-once", resolution.Decision)

	resolved, ok := (<-events).(agentruntime.ExecApprovalResolved)
	require.True(t, ok)
	assert.Equal(t, "resolved", resolved.Status)
	assert.Equal(t, "allow-once", resolved.Decision)

	close(allowRunFinish)
	final := collectRuntimeEvents(t, events)
	require.Len(t, final, 1)
	_, ok = final[0].(agentruntime.Done)
	assert.True(t, ok)
}

// TestRuntimeReconnectReconcilesAllThreeApprovalKinds 覆盖重连对账:断线重连后,
// 只在网关 hello 广播了 plugin.approval.list / openclaw.approval.list 时才去问
// (老网关没有这两个方法,问了会把本来能用的 exec 对账搅坏),问到的三类挂起审批都要
// 恢复成可操作卡片。
func TestRuntimeReconnectReconcilesAllThreeApprovalKinds(t *testing.T) {
	var runID, sessionKey string
	gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
		if connection == 1 {
			runtimeHandshake(t, conn, connection)
			request := runtimeReadTurnRequest(t, conn)
			var params runtimeAgentParams
			require.NoError(t, json.Unmarshal(request.Params, &params))
			runID, sessionKey = params.IdempotencyKey, params.SessionKey
			runtimeWrite(t, conn, map[string]any{"type": "res", "id": request.ID, "ok": true, "payload": map[string]any{"runId": runID, "status": "accepted"}})
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "approval reconcile"), time.Now().Add(time.Second))
			return
		}
		runtimeHandshakeWithMethods(t, conn, connection, "plugin.approval.list", "openclaw.approval.list")
		execList := runtimeReadAfterSubscribe(t, conn)
		require.Equal(t, "exec.approval.list", execList.Method)
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": execList.ID, "ok": true, "payload": []any{}})

		pluginList := runtimeReadRequest(t, conn)
		require.Equal(t, "plugin.approval.list", pluginList.Method)
		runtimeWrite(t, conn, map[string]any{
			"type": "res", "id": pluginList.ID, "ok": true,
			"payload": []any{map[string]any{
				"id": "approval-plugin-restored", "createdAtMs": int64(10), "expiresAtMs": time.Now().Add(time.Minute).UnixMilli(),
				"request": map[string]any{
					"pluginId": "whatsapp", "toolName": "send_message",
					"allowedDecisions": []string{"allow-once", "deny"}, "sessionKey": sessionKey,
				},
			}},
		})

		systemAgentList := runtimeReadRequest(t, conn)
		require.Equal(t, "openclaw.approval.list", systemAgentList.Method)
		runtimeWrite(t, conn, map[string]any{
			"type": "res", "id": systemAgentList.ID, "ok": true,
			"payload": []any{map[string]any{
				"id": "approval-sysagent-restored", "createdAtMs": int64(10), "expiresAtMs": time.Now().Add(time.Minute).UnixMilli(),
				"request": map[string]any{
					"description":      "Update the default model",
					"allowedDecisions": []string{"allow-once", "deny"}, "sessionKey": sessionKey,
				},
			}},
		})

		wait := runtimeReadRequest(t, conn)
		require.Equal(t, "agent.wait", wait.Method)
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": wait.ID, "ok": true, "payload": map[string]any{"runId": runID, "status": "ok"}})
	})

	runtime := New(runtimeResolver(t, gatewayURL))
	events, _, err := runtime.Run(context.Background(), agentruntime.RunRequest{
		Backend: runtimeBackend(), SessionID: 73, UserText: "reconnect covers three kinds",
	})
	require.NoError(t, err)
	// The restored approvals are never answered before the turn's single
	// agent.wait reports it done, so they expire at turn end (existing,
	// kind-agnostic expirePendingApprovals semantics) — this also proves
	// reconcile really tracked them as pending, not just echoed the list.
	collected := collectRuntimeEvents(t, events)
	require.Len(t, collected, 5)

	byKind := map[string]agentruntime.ExecApprovalRequested{}
	for _, event := range collected[:2] {
		requested, ok := event.(agentruntime.ExecApprovalRequested)
		require.True(t, ok)
		byKind[requested.ApprovalKind] = requested
	}
	plugin, ok := byKind[agentruntime.ApprovalKindPlugin]
	require.True(t, ok, "plugin approval must be restored on reconnect")
	assert.Equal(t, "approval-plugin-restored", plugin.ID)
	systemAgent, ok := byKind[agentruntime.ApprovalKindSystemAgent]
	require.True(t, ok, "system-agent approval must be restored on reconnect")
	assert.Equal(t, "approval-sysagent-restored", systemAgent.ID)

	expiredIDs := map[string]bool{}
	for _, event := range collected[2:4] {
		resolved, ok := event.(agentruntime.ExecApprovalResolved)
		require.True(t, ok)
		assert.Equal(t, "expired", resolved.Status)
		expiredIDs[resolved.ID] = true
	}
	assert.True(t, expiredIDs["approval-plugin-restored"])
	assert.True(t, expiredIDs["approval-sysagent-restored"])

	_, ok = collected[4].(agentruntime.Done)
	assert.True(t, ok)
}

// TestApprovalPresentationCarriesWarningAndScope 覆盖 spec「呈现」:exec 显示命令与
// 警告(网关 warningText),exec / plugin 的 scope(ApprovalScope:message-send /
// payment / external-post / standing-grant)落到动作类别及其范围字段。
func TestApprovalPresentationCarriesWarningAndScope(t *testing.T) {
	matches := func(string) bool { return true }
	decode := func(t *testing.T, kind, raw string) agentruntime.ExecApprovalRequested {
		t.Helper()
		request, ok := decodeApprovalListItem(kind, json.RawMessage(raw), matches)
		require.True(t, ok)
		return request
	}

	t.Run("Given an exec approval with warningText and a message-send scope, Then the card gets the warning, category, targets and count", func(t *testing.T) {
		request := decode(t, agentruntime.ApprovalKindExec, `{"id":"e-1","request":{"command":"send-mail --all","sessionKey":"s","allowedDecisions":["allow-once","deny"],
			"warningText":"Sends to external recipients","scope":{"kind":"message-send","target":"#general","recipientCount":42,"recipients":["alice","bob"]}}}`)
		assert.Equal(t, "send-mail --all", request.CommandText)
		assert.Equal(t, []string{"Sends to external recipients"}, request.Warnings)
		assert.Equal(t, agentruntime.ApprovalActionMessage, request.ActionCategory)
		assert.Equal(t, []string{"#general", "alice", "bob"}, request.MessageTargets)
		assert.Equal(t, 42, request.RecipientCount)
	})

	t.Run("Given an exec approval without warning or scope, Then no warning or category is invented", func(t *testing.T) {
		request := decode(t, agentruntime.ApprovalKindExec, `{"id":"e-2","request":{"command":"ls","sessionKey":"s","allowedDecisions":["allow-once","deny"],"warningText":null,"scope":null}}`)
		assert.Empty(t, request.Warnings)
		assert.Empty(t, request.ActionCategory)
	})

	t.Run("Given a plugin approval with a payment scope, Then the amount and payee are shown", func(t *testing.T) {
		request := decode(t, agentruntime.ApprovalKindPlugin, `{"id":"p-1","request":{"pluginId":"stripe","title":"Pay","description":"Pay invoice","sessionKey":"s","allowedDecisions":["allow-once","deny"],
			"scope":{"kind":"payment","amount":"12.50","currency":"USD","target":"ACME Corp"}}}`)
		assert.Equal(t, agentruntime.ApprovalActionPayment, request.ActionCategory)
		assert.Equal(t, "12.50 USD", request.PaymentAmount)
		assert.Equal(t, "ACME Corp", request.PaymentPayee)
	})

	t.Run("Given an external-post scope, Then target and visibility are shown", func(t *testing.T) {
		request := decode(t, agentruntime.ApprovalKindPlugin, `{"id":"p-2","request":{"title":"Post","description":"Post it","sessionKey":"s","allowedDecisions":["allow-once","deny"],
			"scope":{"kind":"external-post","target":"x.com/acme","visibility":"public"}}}`)
		assert.Equal(t, agentruntime.ApprovalActionPublish, request.ActionCategory)
		assert.Equal(t, "x.com/acme", request.PublishTarget)
		assert.Equal(t, "public", request.PublishVisibility)
	})

	t.Run("Given a standing-grant scope on a plugin approval, Then the automation and its command are shown", func(t *testing.T) {
		request := decode(t, agentruntime.ApprovalKindPlugin, `{"id":"p-3","request":{"title":"Grant","description":"Standing grant","sessionKey":"s","allowedDecisions":["allow-once","allow-always","deny"],
			"scope":{"kind":"standing-grant","automation":"nightly-sync","command":"sync --all"}}}`)
		assert.Equal(t, agentruntime.ApprovalActionAutomation, request.ActionCategory)
		assert.Equal(t, "nightly-sync", request.AutomationName)
		assert.Equal(t, "sync --all", request.CommandText)
	})
}

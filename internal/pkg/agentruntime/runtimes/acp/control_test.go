package acp

import (
	"context"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// allKindsOptions 造一组覆盖四种 kind 的审批选项,optionId 各不相同 ——
// Submit 必须按 kind 选,绝不按 optionId 字面量(agent 自定义)。
func allKindsOptions() []acpsdk.PermissionOption {
	return []acpsdk.PermissionOption{
		{OptionId: "opt-view", Kind: "view", Name: "View"},
		{OptionId: "opt-allow-once", Kind: acpsdk.PermissionOptionKindAllowOnce, Name: "Allow once"},
		{OptionId: "opt-allow-always", Kind: acpsdk.PermissionOptionKindAllowAlways, Name: "Allow always"},
		{OptionId: "opt-reject-once", Kind: acpsdk.PermissionOptionKindRejectOnce, Name: "Reject once"},
		{OptionId: "opt-reject-always", Kind: acpsdk.PermissionOptionKindRejectAlways, Name: "Reject always"},
	}
}

func permissionToolCall() acpsdk.ToolCallUpdate {
	kind := acpsdk.ToolKindExecute
	status := acpsdk.ToolCallStatusPending
	title := "Run go test"
	return acpsdk.ToolCallUpdate{
		ToolCallId: "tc-perm",
		Kind:       &kind,
		Status:     &status,
		Title:      &title,
		RawInput:   map[string]any{"command": "go test ./..."},
	}
}

// startPermissionTurn 起一轮并让 agent 发出一条审批请求,交回事件流与应答通道。
func startPermissionTurn(t *testing.T, options []acpsdk.PermissionOption) (*Runtime, *fakeAgent, <-chan agentruntime.Event, <-chan acpsdk.RequestPermissionResponse) {
	t.Helper()
	fa := installFakeAgent(t, fakeAgentOptions{})
	r := New()
	events, _, err := r.Run(context.Background(), agentruntime.RunRequest{
		Backend: acpTestBackend(), SessionID: 21, UserText: "go", Cwd: t.TempDir(),
	})
	require.NoError(t, err)
	fa.waitPrompt(2 * time.Second)
	reply := fa.requestPermission(options, permissionToolCall())
	return r, fa, events, reply
}

// waitForPermissionRequest 从事件流里等到 ToolPermissionRequest。
func waitForPermissionRequest(t *testing.T, events <-chan agentruntime.Event) agentruntime.ToolPermissionRequest {
	t.Helper()
	for {
		select {
		case ev := <-events:
			if req, ok := ev.(agentruntime.ToolPermissionRequest); ok {
				return req
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for ToolPermissionRequest")
		}
	}
}

// finishPermissionTurn 让 agent 收尾本轮(prompt 已应答审批后正常结束)。
func finishPermissionTurn(t *testing.T, fa *fakeAgent, events <-chan agentruntime.Event) []agentruntime.Event {
	t.Helper()
	fa.respondPrompt(acpsdk.StopReasonEndTurn, nil)
	return drainEvents(t, events)
}

// TestSubmitToolPermission_FourStates 四态各自选到正确 kind 的 optionId。
func TestSubmitToolPermission_FourStates(t *testing.T) {
	cases := []struct {
		name        string
		allow       bool
		always      bool
		wantOption  string
		wantOutcome string
	}{
		{"allow once", true, false, "opt-allow-once", "allow_once"},
		{"allow always", true, true, "opt-allow-always", "allow_always"},
		{"reject once", false, false, "opt-reject-once", "reject_once"},
		{"reject always", false, true, "opt-reject-always", "reject_always"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, fa, events, reply := startPermissionTurn(t, allKindsOptions())
			req := waitForPermissionRequest(t, events)
			assert.Equal(t, "tc-perm", req.ToolCallID)
			assert.Equal(t, "Run go test", req.ToolName)
			assert.JSONEq(t, `{"command":"go test ./..."}`, string(req.Input))

			require.NoError(t, r.SubmitToolPermission(context.Background(), 21, req.RequestID, tc.allow, tc.always, ""))
			resp := fa.waitPermissionReply(reply, 2*time.Second)
			require.NotNil(t, resp.Outcome.Selected, "decision must select a real option")
			assert.Equal(t, acpsdk.PermissionOptionId(tc.wantOption), resp.Outcome.Selected.OptionId)

			got := finishPermissionTurn(t, fa, events)
			var resolved *agentruntime.ToolPermissionResolved
			for _, ev := range got {
				if res, ok := ev.(agentruntime.ToolPermissionResolved); ok {
					resolved = &res
				}
			}
			require.NotNil(t, resolved, "ToolPermissionResolved must be emitted")
			assert.Equal(t, req.RequestID, resolved.RequestID)
			assert.Equal(t, tc.allow, resolved.Allowed)
			assert.Equal(t, tc.always, resolved.AlwaysAllow)
		})
	}
}

// TestSubmitToolPermission_DenyReasonDropped denyReason 没有协议通道回传,
// 但必须出现在 ToolPermissionResolved 与日志里。
func TestSubmitToolPermission_DenyReasonDropped(t *testing.T) {
	r, fa, events, reply := startPermissionTurn(t, allKindsOptions())
	req := waitForPermissionRequest(t, events)

	require.NoError(t, r.SubmitToolPermission(context.Background(), 21, req.RequestID, false, false, "use safer flags"))
	resp := fa.waitPermissionReply(reply, 2*time.Second)
	require.NotNil(t, resp.Outcome.Selected)
	assert.Equal(t, acpsdk.PermissionOptionId("opt-reject-once"), resp.Outcome.Selected.OptionId,
		"the wire carries only the option id; ACP has no deny-feedback field")

	got := finishPermissionTurn(t, fa, events)
	for _, ev := range got {
		if res, ok := ev.(agentruntime.ToolPermissionResolved); ok {
			assert.Equal(t, "use safer flags", res.DenyReason)
		}
	}
}

// TestSubmitToolPermission_MissingKindAnswersCancelled 选项里没有对应 kind 时
// 回取消 outcome,绝不编造 optionId。
func TestSubmitToolPermission_MissingKindAnswersCancelled(t *testing.T) {
	options := []acpsdk.PermissionOption{
		{OptionId: "opt-view", Kind: "view", Name: "View"},
	}
	r, fa, events, reply := startPermissionTurn(t, options)
	req := waitForPermissionRequest(t, events)

	require.NoError(t, r.SubmitToolPermission(context.Background(), 21, req.RequestID, true, false, ""))
	resp := fa.waitPermissionReply(reply, 2*time.Second)
	require.NotNil(t, resp.Outcome.Cancelled, "no matching kind must answer with the canceled outcome") //nolint:misspell // SDK field name

	finishPermissionTurn(t, fa, events)
}

// TestSubmitToolPermission_UnknownRequestIDIdemponent requestID 不存在
// (已回过 / 超时)→ 幂等 no-op,不报错。
func TestSubmitToolPermission_UnknownRequestIDIdemponent(t *testing.T) {
	r, fa, events, reply := startPermissionTurn(t, allKindsOptions())
	req := waitForPermissionRequest(t, events)

	require.NoError(t, r.SubmitToolPermission(context.Background(), 21, req.RequestID, true, false, ""))
	fa.waitPermissionReply(reply, 2*time.Second)
	require.NoError(t, r.SubmitToolPermission(context.Background(), 21, req.RequestID, true, false, ""),
		"repeated submit must be an idempotent no-op")
	require.NoError(t, r.SubmitToolPermission(context.Background(), 21, "acp-perm-999", true, false, ""),
		"unknown requestID must be an idempotent no-op")
	require.NoError(t, r.SubmitToolPermission(context.Background(), 999, "whatever", true, false, ""),
		"unknown session must be an idempotent no-op")

	finishPermissionTurn(t, fa, events)
}

// TestAbort_AnswersPendingPermissionsCancelled Abort 时必须把挂着的审批全部
// 答取消应答(协议硬要求),而不是让它们悬在连接上。
func TestAbort_AnswersPendingPermissionsCancelled(t *testing.T) {
	r, fa, events, reply := startPermissionTurn(t, allKindsOptions())
	req := waitForPermissionRequest(t, events)

	_, err := r.Abort(context.Background(), 21, 0)
	require.NoError(t, err)
	resp := fa.waitPermissionReply(reply, 2*time.Second)
	require.NotNil(t, resp.Outcome.Cancelled, "abort must cancel pending request_permission") //nolint:misspell // SDK field name

	fa.respondPrompt(acpsdk.StopReasonCancelled, nil)
	got := drainEvents(t, events)

	// 挂着的审批卡必须随中断收起:事件流里要有对应的 Resolved(拒绝),
	// 且排在轮次终态事件之前。
	resolvedAt, doneAt := -1, -1
	for i, ev := range got {
		switch e := ev.(type) {
		case agentruntime.ToolPermissionResolved:
			require.Equal(t, req.RequestID, e.RequestID)
			require.False(t, e.Allowed)
			require.False(t, e.AlwaysAllow)
			require.Equal(t, -1, resolvedAt, "exactly one resolved event per pending request")
			resolvedAt = i
		case agentruntime.Done:
			doneAt = i
		}
	}
	require.GreaterOrEqual(t, resolvedAt, 0, "abort must emit ToolPermissionResolved for the pending request")
	require.GreaterOrEqual(t, doneAt, 0, "an aborted turn still ends with Done")
	require.Less(t, resolvedAt, doneAt, "resolved must precede the terminal event")
}

// TestContextCancel_AnswersPendingPermissionsCancelled 真实的停止路径是 chat_svc.Stop
// 先取消 turnCtx、之后才调 Abort —— 此时这一轮已经以 context canceled 收尾,Abort
// 只会拿到 ErrNoActiveTurn。所以 ctx 取消收尾这一轮时,同样要把挂着的审批答成取消
// 应答、在终态事件之前发出 Resolved(拒绝),并通知 agent 取消这一轮。
func TestContextCancel_AnswersPendingPermissionsCancelled(t *testing.T) {
	fa := installFakeAgent(t, fakeAgentOptions{})
	r := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, _, err := r.Run(ctx, agentruntime.RunRequest{
		Backend: acpTestBackend(), SessionID: 22, UserText: "go", Cwd: t.TempDir(),
	})
	require.NoError(t, err)
	fa.waitPrompt(2 * time.Second)
	reply := fa.requestPermission(allKindsOptions(), permissionToolCall())
	req := waitForPermissionRequest(t, events)

	cancel()
	got := drainEvents(t, events)

	resp := fa.waitPermissionReply(reply, 2*time.Second)
	require.NotNil(t, resp.Outcome.Cancelled, "ctx cancel must cancel pending request_permission") //nolint:misspell // SDK field name
	assert.True(t, fa.waitCancel(2*time.Second), "agent must receive session/cancel")

	resolvedAt, endAt := -1, -1
	for i, ev := range got {
		switch e := ev.(type) {
		case agentruntime.ToolPermissionResolved:
			require.Equal(t, req.RequestID, e.RequestID)
			require.False(t, e.Allowed)
			require.Equal(t, -1, resolvedAt, "exactly one resolved event per pending request")
			resolvedAt = i
		case agentruntime.ErrorEvent, agentruntime.Done:
			endAt = i
		}
	}
	require.GreaterOrEqual(t, resolvedAt, 0, "ctx cancel must emit ToolPermissionResolved for the pending request")
	require.GreaterOrEqual(t, endAt, 0, "the turn still ends with a terminal event")
	require.Less(t, resolvedAt, endAt, "resolved must precede the terminal event")
}

package ctlcmd

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// patDriver 是以个人 token 连上假世界的 acp 账号驱动。
func patDriver(t *testing.T, w *world) *AccountDriver {
	t.Helper()
	env := envMap("AGENTRE_TOKEN", personalToken, "AGENTRE_SERVER_URL", w.srv.srv.URL)
	ep, drv, err := ResolveACPExecutor("", "", false, env, &bytes.Buffer{})
	require.NoError(t, err)
	require.NotNil(t, drv, "a personal token selects the account executor")
	require.Empty(t, ep.Base)
	t.Cleanup(drv.Close)
	return drv
}

// finishTurnOnRun 让假 agentred 在每次 runtime.run 之后落一整轮并回到空闲。
func finishTurnOnRun(w *world, text string) {
	w.box.onRun = func(req *agentrewire.RuntimeRunRequest) {
		id := req.GetConversationId()
		w.box.addTurn(id, req.GetUserText(), text, true)
		w.box.mu.Lock()
		w.box.sessions[id].summary.LifecycleState = "idle"
		w.box.mu.Unlock()
	}
}

func collect(t *testing.T, events <-chan TurnEvent) []TurnEvent {
	t.Helper()
	var out []TurnEvent
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-timeout:
			t.Fatalf("turn did not finish; got %+v", out)
		}
	}
}

func kinds(evs []TurnEvent) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Kind)
	}
	return out
}

func TestACPDriver_GivenAPersonalTokenThenAPromptStartsAConversationAndStreamsTheTurn(t *testing.T) {
	w := newWorld(t)
	finishTurnOnRun(w, "hello there")
	drv := patDriver(t, w)

	id, events, err := drv.Prompt(context.Background(), PromptRequest{Agent: "reviewer", Text: "review it"})
	require.NoError(t, err)
	got := collect(t, events)
	assert.Equal(t, []string{TurnToolCall, TurnToolResult, TurnText, TurnDone}, kinds(got), "user_message frames are not echoed back")
	assert.Equal(t, "hello there", got[2].Delta)
	assert.Equal(t, "Bash", got[0].ToolName)
	assert.Equal(t, "tc1", got[0].ToolCallID)
	assert.Equal(t, "a.txt", got[1].ToolResult)

	runs := w.box.runRequests()
	require.Len(t, runs, 1)
	assert.Equal(t, id, runs[0].GetConversationId())
	assert.Equal(t, "AG-12", runs[0].GetAgentSyncId())
	assert.Equal(t, "review it", runs[0].GetUserText())
	assert.Equal(t, patFP, runs[0].GetSourceDevice(), "the personal token is the origin of the conversation")
}

func TestACPDriver_GivenAnExistingConversationThenOnlyTheNewTurnIsStreamed(t *testing.T) {
	w := newWorld(t)
	finishTurnOnRun(w, "first answer")
	drv := patDriver(t, w)
	id, events, err := drv.Prompt(context.Background(), PromptRequest{Agent: "reviewer", Text: "one"})
	require.NoError(t, err)
	collect(t, events)

	finishTurnOnRun(w, "second answer")
	same, events, err := drv.Prompt(context.Background(), PromptRequest{Conversation: id, Text: "two"})
	require.NoError(t, err)
	assert.Equal(t, id, same)
	got := collect(t, events)
	require.Equal(t, []string{TurnToolCall, TurnToolResult, TurnText, TurnDone}, kinds(got))
	assert.Equal(t, "second answer", got[2].Delta, "the first turn's frames are behind the cursor")
	assert.Len(t, w.box.runRequests(), 2)
}

func TestACPDriver_GivenAFailedTurnThenTheStreamEndsWithAnError(t *testing.T) {
	w := newWorld(t)
	w.box.onRun = func(req *agentrewire.RuntimeRunRequest) {
		w.box.finish(req.GetConversationId(), "failed", "partial", "model exploded")
	}
	drv := patDriver(t, w)
	_, events, err := drv.Prompt(context.Background(), PromptRequest{Agent: "reviewer", Text: "go"})
	require.NoError(t, err)
	got := collect(t, events)
	last := got[len(got)-1]
	assert.Equal(t, TurnError, last.Kind)
	assert.Equal(t, "model exploded", last.Error)
}

func TestACPDriver_GivenAToolPermissionThenItIsSurfacedAndAnswerReachesTheDevice(t *testing.T) {
	w := newWorld(t)
	w.box.onRun = func(req *agentrewire.RuntimeRunRequest) {
		id := req.GetConversationId()
		w.box.mu.Lock()
		s := w.box.sessions[id]
		w.box.seq++
		s.notes = append(s.notes, &agentrewire.DurableNotification{Seq: w.box.seq, Payload: &agentrewire.RpcNotification{
			Payload: &agentrewire.RpcNotification_RuntimeEvent{RuntimeEvent: &agentrewire.RuntimeEventNotification{
				ConversationId: id,
				Event: &agentrewire.RuntimeEventNotification_ToolPermissionRequest{ToolPermissionRequest: &agentrewire.ToolPermissionRequest{
					RequestId: "perm-1", ToolName: "Bash", Input: []byte(`{"command":"rm x"}`)}},
			}}}})
		s.summary.LatestSeq = w.box.seq
		w.box.mu.Unlock()
		w.box.setPending(id, &agentrewire.SessionPendingWaitersResponse{ToolPermissions: []*agentrewire.PendingToolPermission{
			{RequestId: "perm-1", ToolName: "Bash", Input: []byte(`{"command":"rm x"}`)}}}, true)
	}
	drv := patDriver(t, w)
	ctx, cancel := context.WithCancel(context.Background())
	id, events, err := drv.Prompt(ctx, PromptRequest{Agent: "reviewer", Text: "go"})
	require.NoError(t, err)
	defer func() { cancel(); collect(t, events) }() // 轮次没收尾：取消并等 tail 退出

	select {
	case ev := <-events:
		require.Equal(t, TurnPermission, ev.Kind)
		assert.Equal(t, "perm-1", ev.RequestID)
		assert.Equal(t, "Bash", ev.ToolName)
		assert.JSONEq(t, `{"command":"rm x"}`, string(ev.ToolInput))
	case <-time.After(5 * time.Second):
		t.Fatal("no permission event")
	}
	require.NoError(t, drv.Answer(context.Background(), id, "perm-1", true))
	perms := w.box.answered().permissions
	require.Len(t, perms, 1)
	assert.True(t, perms[0].GetAllow())
	assert.Equal(t, "perm-1", perms[0].GetRequestId())
}

func TestACPDriver_GivenStopThenTheTurnIsAbortedOnItsDevice(t *testing.T) {
	w := newWorld(t)
	drv := patDriver(t, w)
	w.box.onRun = nil
	ctx, cancel := context.WithCancel(context.Background())
	id, events, err := drv.Prompt(ctx, PromptRequest{Agent: "reviewer", Text: "go"})
	require.NoError(t, err)
	defer func() { cancel(); collect(t, events) }()
	require.NoError(t, drv.Stop(context.Background(), id))
	assert.Contains(t, w.box.callLog(), "abort:"+patFP)
}

func TestACPDriver_GivenTheLoginButNoPersonalTokenThenItRefusesWithTheEditorHint(t *testing.T) {
	w := newWorld(t)
	_, drv, err := ResolveACPExecutor("", "", false, envMap(), &bytes.Buffer{})
	require.Error(t, err)
	assert.Nil(t, drv)
	assert.Contains(t, err.Error(), "AGENTRE_TOKEN")
	assert.Contains(t, err.Error(), "chat")
	assert.Contains(t, err.Error(), "approve")
	assert.Contains(t, err.Error(), "agrctl create token")
	_ = w
}

func TestACPDriver_GivenAnExplicitEndpointOrNothingThenTheDesktopIsUsed(t *testing.T) {
	accountDirs(t)
	ep, drv, err := ResolveACPExecutor("http://127.0.0.1:9", "ctl-token", false, envMap(), &bytes.Buffer{})
	require.NoError(t, err)
	assert.Nil(t, drv)
	assert.Equal(t, "http://127.0.0.1:9", ep.Base)
}

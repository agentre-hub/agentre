package acp

import (
	"context"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/shellloc"
)

func execCall(id, command string) acpsdk.SessionUpdate {
	return acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
		ToolCallId: acpsdk.ToolCallId(id),
		Title:      "Run",
		Kind:       acpsdk.ToolKindExecute,
		Status:     acpsdk.ToolCallStatusPending,
		RawInput:   map[string]any{"command": command},
	}}
}

func finishCall(id string) acpsdk.SessionUpdate {
	return acpsdk.SessionUpdate{ToolCallUpdate: &acpsdk.SessionToolCallUpdate{
		ToolCallId: acpsdk.ToolCallId(id),
		Status:     ptrStatus(acpsdk.ToolCallStatusCompleted),
		Content:    []acpsdk.ToolCallContent{acpsdk.ToolContent(acpsdk.TextBlock("ok"))},
	}}
}

func runTurn(t *testing.T, fa *fakeAgent, r *Runtime, req agentruntime.RunRequest, updates ...acpsdk.SessionUpdate) []agentruntime.Event {
	t.Helper()
	events, _, err := r.Run(context.Background(), req)
	require.NoError(t, err)
	fa.waitPrompt(2 * time.Second)
	for _, u := range updates {
		fa.sendUpdate(u)
	}
	fa.respondPrompt(acpsdk.StopReasonEndTurn, nil)
	return drainEvents(t, events)
}

func cwdsByID(events []agentruntime.Event) map[string]string {
	m := map[string]string{}
	for _, ev := range events {
		switch e := ev.(type) {
		case agentruntime.ToolCall:
			m["call:"+e.ID] = e.Cwd
		case agentruntime.ToolResult:
			m["result:"+e.ToolCallID] = e.Cwd
		}
	}
	return m
}

func TestRun_ShellCommandsSetToolCwd(t *testing.T) {
	fa := installFakeAgent(t, fakeAgentOptions{})
	r := New()
	start := t.TempDir()
	req := agentruntime.RunRequest{Backend: acpTestBackend(), SessionID: 11, UserText: "go", Cwd: start}

	got := runTurn(t, fa, r, req,
		execCall("a", "ls"),
		finishCall("a"),
		execCall("b", "cd /srv/wt && make"),
		finishCall("b"),
		execCall("c", "cd $UNKNOWN"),
		finishCall("c"),
	)
	m := cwdsByID(got)
	assert.Equal(t, start, m["call:a"], "no cd yet: the session working directory")
	assert.Equal(t, start, m["result:a"])
	assert.Equal(t, "/srv/wt", m["call:b"], "cd moves the location")
	assert.Equal(t, "/srv/wt", m["result:b"])
	assert.Equal(t, "/srv/wt", m["call:c"], "an unreadable cd keeps the previous location")

	got = runTurn(t, fa, r, req, execCall("d", "git worktree add ../w2"), finishCall("d"))
	m = cwdsByID(got)
	assert.Equal(t, "/srv/wt", m["call:d"], "the location persists into the next turn")
	for _, ev := range got {
		if call, ok := ev.(agentruntime.ToolCall); ok && call.ID == "d" {
			assert.JSONEq(t, `{"command":"git worktree add ../w2","`+shellloc.WorktreeAddKey+`":"/srv/w2"}`, string(call.Input))
		}
	}
}

func TestRun_NonShellToolCarriesCurrentLocation(t *testing.T) {
	fa := installFakeAgent(t, fakeAgentOptions{})
	r := New()
	start := t.TempDir()
	req := agentruntime.RunRequest{Backend: acpTestBackend(), SessionID: 12, UserText: "go", Cwd: start}
	got := runTurn(t, fa, r, req,
		execCall("a", "cd /srv/wt"),
		acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
			ToolCallId: "r", Title: "Read", Kind: acpsdk.ToolKindRead, Status: acpsdk.ToolCallStatusPending,
			RawInput: map[string]any{"command": "cd /elsewhere"},
		}},
	)
	assert.Equal(t, "/srv/wt", cwdsByID(got)["call:r"], "only execute-kind calls are parsed as shell")
}

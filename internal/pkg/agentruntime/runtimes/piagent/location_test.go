package piagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/shellloc"
	pkgpi "github.com/agentre-hub/agentre/pkg/piagent"
)

func piTool(kind pkgpi.EventKind, id, name, input string) pkgpi.Event {
	return pkgpi.Event{Kind: kind, Tool: pkgpi.ToolEvent{ID: id, Name: name, Input: []byte(input), Content: "ok"}}
}

func drainWithLocation(t *testing.T, loc *shellloc.Tracker, events ...pkgpi.Event) []agentruntime.Event {
	t.Helper()
	out := make(chan agentruntime.Event, 64)
	events = append(events, pkgpi.Event{Kind: pkgpi.EventDone})
	drainStream(context.Background(), agentruntime.RunRequest{}, loc, &scriptStream{events: events}, out, &agentruntime.RunResult{}, nil)
	close(out)
	var got []agentruntime.Event
	for ev := range out {
		got = append(got, ev)
	}
	return got
}

func TestDrainStream_BashCommandsSetToolCwd(t *testing.T) {
	loc := shellloc.NewTracker("/w")
	got := drainWithLocation(t, loc,
		piTool(pkgpi.EventPreToolUse, "a", "bash", `{"command":"cd sub && ls"}`),
		piTool(pkgpi.EventPostToolUse, "a", "bash", ``),
		piTool(pkgpi.EventPreToolUse, "b", "read", `{"path":"x.go"}`),
		piTool(pkgpi.EventPostToolUse, "b", "read", ``),
		piTool(pkgpi.EventPreToolUse, "c", "bash", `{"command":"cd $SOMEWHERE"}`),
		piTool(pkgpi.EventPostToolUse, "c", "bash", ``),
		piTool(pkgpi.EventPreToolUse, "d", "bash", `{"command":"git worktree add ../wt -b feat"}`),
		piTool(pkgpi.EventPostToolUse, "d", "bash", ``),
	)
	calls := map[string]agentruntime.ToolCall{}
	results := map[string]agentruntime.ToolResult{}
	for _, ev := range got {
		switch e := ev.(type) {
		case agentruntime.ToolCall:
			calls[e.ID] = e
		case agentruntime.ToolResult:
			results[e.ToolCallID] = e
		}
	}
	require.Len(t, calls, 4)
	assert.Equal(t, "/w/sub", calls["a"].Cwd, "cd persists")
	assert.Equal(t, "/w/sub", results["a"].Cwd)
	assert.Equal(t, "/w/sub", calls["b"].Cwd, "a non-shell call carries the current location")
	assert.Equal(t, "/w/sub", results["b"].Cwd)
	assert.Equal(t, "/w/sub", calls["c"].Cwd, "an unreadable cd keeps the previous location")
	assert.Equal(t, "/w/sub", calls["d"].Cwd, "worktree add does not move the AI")
	assert.JSONEq(t, `{"command":"git worktree add ../wt -b feat","`+shellloc.WorktreeAddKey+`":"/w/wt"}`, string(calls["d"].Input))
	assert.Equal(t, "/w/sub", loc.Dir())
}

// 决策 16:子代理的位置变化只参与认领,不移动主会话的位置。子代理嵌套的 bash `cd`
// 仍让它自己的调用带上它的目录(chat_svc 据 ParentToolCallID 只认领),但主 agent
// 之后的调用还在主位置。
func TestDrainStream_SubagentCdDoesNotMoveMainLocation(t *testing.T) {
	loc := shellloc.NewTracker("/w")
	got := drainWithLocation(t, loc,
		pkgpi.Event{Kind: pkgpi.EventPreToolUse, Tool: pkgpi.ToolEvent{
			ID: "outer", Name: "subagent", Input: []byte(`{"agent":"worker","task":"inspect"}`),
		}},
		pkgpi.Event{Kind: pkgpi.EventToolUseUpdate, Tool: pkgpi.ToolEvent{
			ID: "outer", Name: "subagent",
			PartialResult: []byte(`{"details":{"mode":"single","results":[{"messages":[
				{"role":"assistant","content":[{"type":"toolCall","id":"inner","name":"bash","arguments":{"command":"cd ../wt && ls"}}],"stopReason":"toolUse"},
				{"role":"toolResult","toolCallId":"inner","content":[{"type":"text","text":"ok"}],"isError":false},
				{"role":"assistant","content":[{"type":"toolCall","id":"inner2","name":"read","arguments":{"path":"a.go"}}],"stopReason":"toolUse"}
			]}]}}`),
		}},
		piTool(pkgpi.EventPreToolUse, "m1", "read", `{"path":"x.go"}`),
	)
	var nested []agentruntime.ToolCall
	var nestedResult *agentruntime.ToolResult
	var mainCall *agentruntime.ToolCall
	for _, ev := range got {
		switch e := ev.(type) {
		case agentruntime.ToolCall:
			if e.ParentToolCallID != "" {
				nested = append(nested, e)
			} else if e.ID == "m1" {
				mainCall = &e
			}
		case agentruntime.ToolResult:
			if e.ParentToolCallID != "" {
				nestedResult = &e
			}
		}
	}
	require.Len(t, nested, 2)
	assert.Equal(t, "/wt", nested[0].Cwd, "the subagent's own location still rides its call (claim-only)")
	require.NotNil(t, nestedResult)
	assert.Equal(t, "/wt", nestedResult.Cwd)
	assert.Equal(t, "/wt", nested[1].Cwd, "a cd persists within the subagent's own run")
	require.NotNil(t, mainCall)
	assert.Equal(t, "/w", mainCall.Cwd, "the main agent stays where it was")
	assert.Equal(t, "/w", loc.Dir())
}

func TestDrainStream_NilTrackerLeavesCwdUnknown(t *testing.T) {
	got := drainWithLocation(t, nil, piTool(pkgpi.EventPreToolUse, "a", "bash", `{"command":"cd /x"}`))
	for _, ev := range got {
		if call, ok := ev.(agentruntime.ToolCall); ok {
			assert.Empty(t, call.Cwd)
		}
	}
}

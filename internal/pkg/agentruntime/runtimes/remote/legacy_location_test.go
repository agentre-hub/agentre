package remote

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

// spec 2026-11-24-chat-right-toolwindows「识别信号 > 远端会话」:执行设备上的 agentred 较旧、
// 不在工具事件上报目录(Cwd 为空)时,桌面端仍从已随事件到达的信号推出 AI 所在目录 ——
// Claude Code EnterWorktree / ExitWorktree 的结果元数据、Codex 命令的 cwd、pi / builtin / ACP
// 的命令文本。新 agentred 已报的目录原样保留;失败的结果不移动;子代理的嵌套事件不经这里推算。

// runLegacy 起一轮远端会话(cwd = /w),逐条投递 evs,收齐这一轮交给消费方的事件。
func runLegacy(t *testing.T, backend agent_backend_entity.BackendType, evs ...agentruntime.Event) []agentruntime.Event {
	t.Helper()
	_, cli, capture, rt := setupRemote(t)
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, result any) error {
			rp := params.(wire.RunParams)
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID}
			return nil
		}).AnyTimes()
	events, _, err := rt.Run(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: string(backend), ID: 1, Name: "x"},
		SessionID: 42,
		UserText:  "go",
		Cwd:       "/w",
	})
	require.NoError(t, err)
	for _, ev := range evs {
		capture.deliver(t, wire.NotifyEvent, wire.EventFrame{ConversationID: convOf(42), Event: ev})
	}
	capture.deliver(t, wire.NotifyRunResultDone, wire.RunResultDoneFrame{ConversationID: convOf(42)})
	var got []agentruntime.Event
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return got
			}
			got = append(got, ev)
		case <-timeout:
			t.Fatal("events never closed")
		}
	}
}

func callsAndResults(evs []agentruntime.Event) (map[string]agentruntime.ToolCall, map[string]agentruntime.ToolResult) {
	calls := map[string]agentruntime.ToolCall{}
	results := map[string]agentruntime.ToolResult{}
	for _, ev := range evs {
		switch e := ev.(type) {
		case agentruntime.ToolCall:
			calls[e.ID] = e
		case agentruntime.ToolResult:
			results[e.ToolCallID] = e
		}
	}
	return calls, results
}

func TestLegacyAgentred_ClaudeWorktreeResultsCarryTheLocation(t *testing.T) {
	got := runLegacy(t, agent_backend_entity.TypeClaudeCode,
		agentruntime.ToolCall{ID: "e1", Name: "EnterWorktree", Input: json.RawMessage(`{"name":"x"}`)},
		agentruntime.ToolResult{ToolCallID: "e1", Content: "ok", Meta: json.RawMessage(`{"worktreePath":"/w/.claude/worktrees/x","message":"m"}`)},
		agentruntime.ToolCall{ID: "e2", Name: "EnterWorktree", Input: json.RawMessage(`{"name":"y"}`)},
		agentruntime.ToolResult{ToolCallID: "e2", IsError: true, Meta: json.RawMessage(`{"worktreePath":"/w/.claude/worktrees/y"}`)},
		agentruntime.ToolCall{ID: "x1", Name: "ExitWorktree", Input: json.RawMessage(`{"action":"remove"}`)},
		agentruntime.ToolResult{ToolCallID: "x1", Content: "ok", Meta: json.RawMessage(`{"action":"remove","originalCwd":"/w","worktreePath":"/w/.claude/worktrees/x","restoredCwd":"/w"}`)},
		// 新 agentred 已报目录:原样保留。
		agentruntime.ToolCall{ID: "e3", Name: "EnterWorktree", Input: json.RawMessage(`{}`), Cwd: "/w"},
		agentruntime.ToolResult{ToolCallID: "e3", Meta: json.RawMessage(`{"worktreePath":"/other"}`), Cwd: "/w/.claude/worktrees/z"},
	)
	_, results := callsAndResults(got)
	assert.Equal(t, "/w/.claude/worktrees/x", results["e1"].Cwd)
	assert.Empty(t, results["e2"].Cwd, "a failed EnterWorktree does not move the AI")
	assert.Equal(t, "/w", results["x1"].Cwd)
	var meta map[string]any
	require.NoError(t, json.Unmarshal(results["x1"].Meta, &meta))
	assert.Equal(t, "/w/.claude/worktrees/x", meta[agentruntime.RevokeRootMetaKey], "ExitWorktree remove revokes the root")
	assert.Equal(t, "/w/.claude/worktrees/z", results["e3"].Cwd)
}

func TestLegacyAgentred_CodexCommandCwdIsTheLocation(t *testing.T) {
	got := runLegacy(t, agent_backend_entity.TypeCodex,
		agentruntime.ToolCall{ID: "c1", Name: "command_execution", Input: json.RawMessage(`{"command":"ls","cwd":"/w/wt"}`)},
		agentruntime.ToolResult{ToolCallID: "c1", Content: "ok"},
		agentruntime.ToolCall{ID: "c2", Name: "file_change", Input: json.RawMessage(`{"changes":[]}`)},
	)
	calls, results := callsAndResults(got)
	assert.Equal(t, "/w/wt", calls["c1"].Cwd)
	assert.Equal(t, "/w/wt", results["c1"].Cwd)
	assert.Empty(t, calls["c2"].Cwd)
}

func TestLegacyAgentred_ShellCommandTextMovesTheLocation(t *testing.T) {
	got := runLegacy(t, agent_backend_entity.TypeBuiltin,
		agentruntime.ToolCall{ID: "b1", Name: "bash", Input: json.RawMessage(`{"command":"cd ../wt && ls"}`)},
		agentruntime.ToolResult{ToolCallID: "b1", Content: "ok"},
		agentruntime.ToolCall{ID: "b2", Name: "read", Input: json.RawMessage(`{"path":"a.go"}`)},
		agentruntime.ToolCall{ID: "n1", Name: "bash", Input: json.RawMessage(`{"command":"cd /elsewhere"}`), ParentToolCallID: "outer"},
		agentruntime.ToolCall{ID: "b3", Name: "read", Input: json.RawMessage(`{"path":"b.go"}`)},
	)
	calls, results := callsAndResults(got)
	assert.Equal(t, "/wt", calls["b1"].Cwd)
	assert.Equal(t, "/wt", results["b1"].Cwd)
	assert.Equal(t, "/w", calls["b2"].Cwd, "builtin's bash starts every call in the session directory")
	assert.Empty(t, calls["n1"].Cwd, "nested subagent events are not inferred here")
	assert.Equal(t, "/w", calls["b3"].Cwd, "a subagent's cd does not move the main location")
}

func TestLegacyAgentred_ACPShellCdCarriesIntoLaterCalls(t *testing.T) {
	got := runLegacy(t, agent_backend_entity.TypeACP,
		agentruntime.ToolCall{ID: "b1", Name: "Run", Input: json.RawMessage(`{"command":"cd ../wt && ls"}`)},
		agentruntime.ToolCall{ID: "b2", Name: "Read", Input: json.RawMessage(`{"path":"a.go"}`)},
	)
	calls, _ := callsAndResults(got)
	assert.Equal(t, "/wt", calls["b2"].Cwd)
}

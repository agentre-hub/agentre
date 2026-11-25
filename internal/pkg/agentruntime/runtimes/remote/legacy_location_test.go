package remote

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/transcript"
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

// durableTool 是一次外层工具调用连同它的结果,照落库的 tool_use / tool_result 块形状。
type durableTool struct{ id, name, input, result string }

// durableToolFrames 把若干次工具调用按旧 agentred 落库的块形状放进一条 assistant 消息,
// 经 transcript.ProjectMessages 投影成它交来的持久帧(结果帧没有 Meta)。
func durableToolFrames(t *testing.T, tools ...durableTool) []agentruntime.Event {
	t.Helper()
	var blocks []string
	for _, tool := range tools {
		text, err := json.Marshal(tool.result)
		require.NoError(t, err)
		blocks = append(blocks,
			`{"type":"tool_use","data":{"id":"`+tool.id+`","name":"`+tool.name+`","input":`+tool.input+`}}`,
			`{"type":"tool_result","data":{"tool_use_id":"`+tool.id+`","content":[{"type":"text","data":{"text":`+string(text)+`}}]}}`)
	}
	msg := &transcript_entity.Message{ID: 20, Role: "assistant", BlocksJSON: "[" + strings.Join(blocks, ",") + "]"}
	frames, _, err := transcript.ProjectMessages(convOf(42), []*transcript_entity.Message{msg})
	require.NoError(t, err)
	var evs []agentruntime.Event
	for _, f := range frames {
		evs = append(evs, f.Event)
	}
	return evs
}

// 旧 agentred 交给桌面端进事件流的是**持久帧**:它把这一轮累积成块落库,再经
// transcript.ProjectMessages 投影成帧(实时那一份只是预览帧,不进事件流)。tool_result 块
// 只存 {tool_use_id, content, is_error},Claude Code 的结果元数据在落库时就没了 —— 这里
// 照 V35 那次真实会话落下的两块原样投影,所以结果帧上没有 Meta,只有结果正文。
func TestLegacyAgentred_ClaudeEnterWorktreeFromDurableFrameText(t *testing.T) {
	const wt = "/w/.claude/worktrees/feat-o"
	project := func(content string) []agentruntime.Event {
		t.Helper()
		return durableToolFrames(t, durableTool{id: "e1", name: "EnterWorktree", input: `{"name":"feat-o"}`, result: content})
	}
	created := project("Created worktree at \u201c" + wt + "\u201d on branch \u201cworktree-feat-o\u201d. The session is now working in the worktree. Use ExitWorktree to leave mid-session, or exit the session to be prompted.")
	_, results := callsAndResults(created)
	require.Contains(t, results, "e1")
	require.Empty(t, results["e1"].Meta, "precondition: the durable frame carries no result metadata")

	_, results = callsAndResults(runLegacy(t, agent_backend_entity.TypeClaudeCode, created...))
	assert.Equal(t, wt, results["e1"].Cwd, "the worktree path in the result text is where the AI now is")

	resumed := project("Resumed worktree at \u201c" + wt + "\u201d on branch \u201cworktree-feat-o\u201d. A worktree with this name already existed and was resumed as-is. The session is now working in the worktree.")
	_, results = callsAndResults(runLegacy(t, agent_backend_entity.TypeClaudeCode, resumed...))
	assert.Equal(t, wt, results["e1"].Cwd)

	// 路径里有打印不出的字符时,CLI 写的是改过的拼写并注明 —— 那不是真实路径,不报。
	respelled := project("Created worktree at \u201c/w/x'y\u201d (not its exact spelling) on branch \u201cb\u201d. The session is now working in the worktree.")
	_, results = callsAndResults(runLegacy(t, agent_backend_entity.TypeClaudeCode, respelled...))
	assert.Empty(t, results["e1"].Cwd)

	// 文字对不上已知形状(别的版本、被截断)时不猜。
	other := project("Switched to worktree " + wt)
	_, results = callsAndResults(runLegacy(t, agent_backend_entity.TypeClaudeCode, other...))
	assert.Empty(t, results["e1"].Cwd)
}

// ExitWorktree 经旧 agentred 的持久帧到达时同样没有 Meta;入参(落库的 tool_use 块)在。
// 形状照 V35 那次真实会话落下的块:入参 {"action":"remove","discard_changes":true},
// 正文「Exited and removed worktree at “…”. … Session is now back in “…”.」。退出后回到
// 进入 worktree 之前所在的目录(没有记录时回到这一轮的工作目录);action=remove 时撤销
// 进入的那个 worktree 根。
func TestLegacyAgentred_ClaudeExitWorktreeFromDurableFrame(t *testing.T) {
	const wt = "/w/.claude/worktrees/feat-o"
	enter := durableTool{id: "e1", name: "EnterWorktree", input: `{"name":"feat-o"}`,
		result: "Created worktree at \u201c" + wt + "\u201d on branch \u201cworktree-feat-o\u201d. The session is now working in the worktree."}
	revokeOf := func(r agentruntime.ToolResult) any {
		if len(r.Meta) == 0 {
			return nil
		}
		var meta map[string]any
		require.NoError(t, json.Unmarshal(r.Meta, &meta))
		return meta[agentruntime.RevokeRootMetaKey]
	}

	t.Run("keep returns to where the AI was and keeps the root", func(t *testing.T) {
		evs := durableToolFrames(t, enter, durableTool{id: "x1", name: "ExitWorktree", input: `{"action":"keep"}`,
			result: "Exited worktree. Your work is preserved at \u201c" + wt + "\u201d on branch \u201cworktree-feat-o\u201d. Session is now back in \u201c/w\u201d."})
		_, results := callsAndResults(evs)
		require.Empty(t, results["x1"].Meta, "precondition: the durable frame carries no result metadata")
		_, results = callsAndResults(runLegacy(t, agent_backend_entity.TypeClaudeCode, evs...))
		assert.Equal(t, "/w", results["x1"].Cwd)
		assert.Nil(t, revokeOf(results["x1"]), "keep does not revoke")
	})

	t.Run("remove returns and revokes the entered worktree", func(t *testing.T) {
		evs := durableToolFrames(t, enter, durableTool{id: "x1", name: "ExitWorktree", input: `{"action":"remove","discard_changes":true}`,
			result: "Exited and removed worktree at \u201c" + wt + "\u201d. Discarded 1 uncommitted file. Session is now back in \u201c/w\u201d."})
		_, results := callsAndResults(runLegacy(t, agent_backend_entity.TypeClaudeCode, evs...))
		assert.Equal(t, "/w", results["x1"].Cwd)
		assert.Equal(t, wt, revokeOf(results["x1"]))
	})

	t.Run("without a remembered entry the exit falls back to the run directory", func(t *testing.T) {
		evs := durableToolFrames(t, durableTool{id: "x1", name: "ExitWorktree", input: `{"action":"keep"}`, result: "Exited worktree."})
		_, results := callsAndResults(runLegacy(t, agent_backend_entity.TypeClaudeCode, evs...))
		assert.Equal(t, "/w", results["x1"].Cwd)
		assert.Nil(t, revokeOf(results["x1"]))
	})

	t.Run("a failed exit does not move the AI", func(t *testing.T) {
		evs := durableToolFrames(t, enter, durableTool{id: "x1", name: "ExitWorktree", input: `{"action":"remove"}`, result: "Worktree not removed; the session is still in it."})
		for i, ev := range evs {
			if r, ok := ev.(agentruntime.ToolResult); ok && r.ToolCallID == "x1" {
				r.IsError = true
				evs[i] = r
			}
		}
		_, results := callsAndResults(runLegacy(t, agent_backend_entity.TypeClaudeCode, evs...))
		assert.Empty(t, results["x1"].Cwd)
		assert.Nil(t, revokeOf(results["x1"]))
	})
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

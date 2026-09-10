package piagent

import (
	"errors"
	"testing"

	"github.com/cago-frame/agents/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	pkgpi "github.com/agentre-hub/agentre/pkg/piagent"
)

func TestTranslate_TextAndThinking(t *testing.T) {
	out, usage, err := translate(pkgpi.Event{Kind: pkgpi.EventTextDelta, Text: "hello"})
	require.NoError(t, err)
	require.Nil(t, usage)
	require.Len(t, out, 1)
	assert.Equal(t, agentruntime.TextDelta{Text: "hello"}, out[0])

	out, usage, err = translate(pkgpi.Event{Kind: pkgpi.EventThinkingDelta, Text: "think"})
	require.NoError(t, err)
	require.Nil(t, usage)
	require.Len(t, out, 1)
	assert.Equal(t, agentruntime.ThinkingDelta{Text: "think"}, out[0])
}

func TestTranslate_ToolEvents(t *testing.T) {
	out, usage, err := translate(pkgpi.Event{
		Kind: pkgpi.EventPreToolUse,
		Tool: pkgpi.ToolEvent{ID: "tool-1", Name: "bash", Input: []byte(`{"command":"pwd"}`)},
	})
	require.NoError(t, err)
	require.Nil(t, usage)
	require.Len(t, out, 1)
	call, ok := out[0].(agentruntime.ToolCall)
	require.True(t, ok)
	assert.Equal(t, "tool-1", call.ID)
	assert.Equal(t, "bash", call.Name)
	assert.JSONEq(t, `{"command":"pwd"}`, string(call.Input))

	out, usage, err = translate(pkgpi.Event{
		Kind: pkgpi.EventPostToolUse,
		Tool: pkgpi.ToolEvent{ID: "tool-1", Content: "done", IsError: true},
	})
	require.NoError(t, err)
	require.Nil(t, usage)
	require.Len(t, out, 1)
	res, ok := out[0].(agentruntime.ToolResult)
	require.True(t, ok)
	assert.Equal(t, "tool-1", res.ToolCallID)
	assert.Equal(t, "done", res.Content)
	assert.True(t, res.IsError)
}

// Pi 内置文件工具的 wire 形状是 {path, edits:[{oldText,newText}]} / {path, content},
// 与 claudecode 的 Edit/Write 同语义。translator 必须填 ToolCall.Canonical,前端才
// 走 FileEditCard / FileWriteCard 而不是通用 raw 工具卡。
func TestTranslate_RecognizesFileEditCanonical(t *testing.T) {
	out, _, err := translate(pkgpi.Event{
		Kind: pkgpi.EventPreToolUse,
		Tool: pkgpi.ToolEvent{
			ID:    "tool-edit",
			Name:  "edit",
			Input: []byte(`{"path":"/tmp/a.go","edits":[{"oldText":"foo\n","newText":"bar\n"}]}`),
		},
	})
	require.NoError(t, err)
	require.Len(t, out, 1)
	call, ok := out[0].(agentruntime.ToolCall)
	require.True(t, ok)
	fe, ok := call.Canonical.(canonical.FileEdit)
	require.True(t, ok, "edit tool 应识别成 canonical.FileEdit")
	require.Len(t, fe.Files, 1)
	assert.Equal(t, "/tmp/a.go", fe.Files[0].Path)
	assert.Equal(t, canonical.ChangeModified, fe.Files[0].Kind)
	assert.NotEmpty(t, fe.Files[0].Hunks)
}

func TestTranslate_RecognizesFileWriteCanonical(t *testing.T) {
	out, _, err := translate(pkgpi.Event{
		Kind: pkgpi.EventPreToolUse,
		Tool: pkgpi.ToolEvent{
			ID:    "tool-write",
			Name:  "write",
			Input: []byte(`{"path":"/tmp/b.go","content":"hello\nworld\n"}`),
		},
	})
	require.NoError(t, err)
	require.Len(t, out, 1)
	call := out[0].(agentruntime.ToolCall)
	fw, ok := call.Canonical.(canonical.FileWrite)
	require.True(t, ok, "write tool 应识别成 canonical.FileWrite")
	assert.Equal(t, "/tmp/b.go", fw.Path)
	assert.Equal(t, 2, fw.Lines)
}

func TestTranslate_NonFileToolsStayRaw(t *testing.T) {
	cases := []pkgpi.ToolEvent{
		{ID: "t1", Name: "bash", Input: []byte(`{"command":"pwd"}`)},
		{ID: "t2", Name: "read", Input: []byte(`{"path":"/tmp/a.go"}`)},
		// 注入的 MCP 工具即使叫 agent / task 也不该被当 subagent 派遣卡:
		// Pi 没有原生 subagent 协议。
		{ID: "t3", Name: "agent", Input: []byte(`{"description":"x"}`)},
		// input 解析不出已知形状时回落 raw。
		{ID: "t4", Name: "edit", Input: []byte(`{"path":"/tmp/a.go"}`)},
		{ID: "t5", Name: "edit", Input: nil},
	}
	for _, tool := range cases {
		out, _, err := translate(pkgpi.Event{Kind: pkgpi.EventPreToolUse, Tool: tool})
		require.NoError(t, err)
		require.Len(t, out, 1)
		call := out[0].(agentruntime.ToolCall)
		assert.Nil(t, call.Canonical, "tool %s 应走 raw 路径", tool.Name)
	}
}

func TestTranslate_Usage(t *testing.T) {
	out, usage, err := translate(pkgpi.Event{
		Kind: pkgpi.EventUsage,
		Usage: provider.Usage{
			PromptTokens:        10,
			CompletionTokens:    3,
			CachedTokens:        2,
			CacheCreationTokens: 4,
		},
		ContextWindow: 258000,
	})
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Len(t, out, 1)
	update, ok := out[0].(agentruntime.UsageUpdate)
	require.True(t, ok)
	assert.Equal(t, 16, update.TotalInputTokens)
	assert.Equal(t, 3, update.Usage.CompletionTokens)
	assert.Equal(t, 258000, update.ContextWindow)
}

func TestTranslate_ContextWindow(t *testing.T) {
	out, usage, err := translate(pkgpi.Event{Kind: pkgpi.EventContextWindow, ContextWindow: 200000})
	require.NoError(t, err)
	require.Nil(t, usage)
	require.Len(t, out, 1)
	assert.Equal(t, agentruntime.ContextWindowUpdated{Tokens: 200000}, out[0])

	out, usage, err = translate(pkgpi.Event{Kind: pkgpi.EventContextWindow})
	require.NoError(t, err)
	require.Nil(t, usage)
	assert.Empty(t, out)
}

func TestTranslate_RuntimeStatusAndCompactBoundary(t *testing.T) {
	out, usage, err := translate(pkgpi.Event{Kind: pkgpi.EventRuntimeStatus, Text: "compacting"})
	require.NoError(t, err)
	require.Nil(t, usage)
	require.Len(t, out, 1)
	assert.Equal(t, agentruntime.RuntimeStatus{Status: "compacting"}, out[0])

	out, usage, err = translate(pkgpi.Event{Kind: pkgpi.EventCompactBoundary})
	require.NoError(t, err)
	require.Nil(t, usage)
	require.Len(t, out, 1)
	assert.Equal(t, agentruntime.CompactBoundary{Trigger: "manual"}, out[0])
}

func TestTranslate_ErrorAndDone(t *testing.T) {
	boom := errors.New("boom")
	out, usage, err := translate(pkgpi.Event{Kind: pkgpi.EventError, Err: boom})
	require.ErrorIs(t, err, boom)
	require.Nil(t, usage)
	assert.Empty(t, out)

	out, usage, err = translate(pkgpi.Event{Kind: pkgpi.EventDone})
	require.NoError(t, err)
	require.Nil(t, usage)
	require.Len(t, out, 1)
	_, ok := out[0].(agentruntime.Done)
	assert.True(t, ok)
}

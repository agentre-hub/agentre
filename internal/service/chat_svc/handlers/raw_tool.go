package handlers

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-ai/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-ai/agentre/internal/service/chat_svc/turn"
)

type ToolCallHandler struct{}

func (ToolCallHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	tc.PauseGeneration()
	tc2 := ev.(agentruntime.ToolCall)
	var input map[string]any
	if len(tc2.Input) > 0 {
		_ = json.Unmarshal(tc2.Input, &input)
	}

	if tc2.ParentToolCallID != "" {
		// Subagent 内层 tool:NestedToolUseBlock (ToUI, 不喂 LLM)
		acc.AddToolUse(&blocks.NestedToolUseBlock{
			ID: tc2.ID, Name: tc2.Name, Input: input,
			ParentToolCallID: tc2.ParentToolCallID, SubagentRunID: tc2.SubagentRunID,
		}, "tool_use:"+tc2.ID)
	} else {
		// 外层 cago.ToolUseBlock (ToAll)
		acc.AddToolUse(&cagoblocks.ToolUseBlock{ID: tc2.ID, Name: tc2.Name, Input: input}, "tool_use:"+tc2.ID)
	}

	// claudecode v2.1.x:ExitPlanMode 的 input={},plan markdown 走前一个 Write 落
	// ~/.claude/plans/<slug>.md。这里把 content 寄到 tc,ExitPlanMode 兜底用。
	if tc != nil {
		if fw, ok := tc2.Canonical.(canonical.FileWrite); ok && isPlanFilePath(fw.Path) {
			tc.LastPlanWriteContent = fw.Content
		}
	}

	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{
			"kind":             "tool_use",
			"toolUseId":        tc2.ID,
			"toolName":         tc2.Name,
			"toolInput":        input,
			"parentToolCallId": tc2.ParentToolCallID,
			"subagentRunId":    tc2.SubagentRunID,
			"canonicalKind":    string(canonical.KindOf(tc2.Canonical)),
			"canonical":        tc2.Canonical,
		})
	}
	return nil
}

// isPlanFilePath 命中 claudecode CLI 的 plan 文件:`<home>/.claude/plans/<slug>.md`。
// 用 filepath.ToSlash 抹平 Windows 反斜杠;子串匹配避免硬编码 home 目录。
func isPlanFilePath(p string) bool {
	if p == "" {
		return false
	}
	return strings.Contains(filepath.ToSlash(p), "/.claude/plans/")
}

type ToolResultHandler struct{}

func (ToolResultHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	tr := ev.(agentruntime.ToolResult)

	// 孤儿 tool_result 丢弃(spec §1.2): 没对应的 tool_use 直接忽略。
	// 这里只查外层 cago.ToolUseBlock;内层 nested 的孤儿丢弃简化按"不严格判定"处理,
	// 由 SubagentDone handler 兜底清理。
	if tr.ParentToolCallID == "" && !acc.HasToolUse(tr.ToolCallID) {
		return nil
	}

	if tr.ParentToolCallID != "" {
		acc.AddToolResult(&blocks.NestedToolResultBlock{
			ToolCallID:       tr.ToolCallID,
			Content:          tr.Content,
			IsError:          tr.IsError,
			ParentToolCallID: tr.ParentToolCallID,
			SubagentRunID:    tr.SubagentRunID,
		})
	} else {
		acc.AddToolResult(&cagoblocks.ToolResultBlock{
			ToolUseID: tr.ToolCallID,
			Content:   []cagoblocks.ContentBlock{cagoblocks.TextBlock{Text: tr.Content}},
			IsError:   tr.IsError,
		})
	}

	if emit != nil {
		var meta map[string]any
		if len(tr.Meta) > 0 {
			_ = json.Unmarshal(tr.Meta, &meta)
		}
		emit.Emit(ctx, streamOf(tc), map[string]any{
			"kind":             "tool_result",
			"toolUseId":        tr.ToolCallID,
			"toolResult":       tr.Content,
			"isError":          tr.IsError,
			"parentToolCallId": tr.ParentToolCallID,
			"subagentRunId":    tr.SubagentRunID,
			"toolResultMeta":   meta,
		})
	}
	return nil
}

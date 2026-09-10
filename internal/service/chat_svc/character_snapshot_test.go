//go:build snapshot

package chat_svc

import (
	"testing"

	"github.com/cago-frame/agents/agent/blocks"

	chatblocks "github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"

	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// addToolUse / addBlock helpers — snapshot tests 调 AddToolUse/AddBlock 时不关心
// mutateKey,统一传空串。这两个 helper 让 sed-rename 后无须每行加 ", \"\"".
func addToolUse(a *turn.Accumulator, b blocks.ContentBlock) { a.AddToolUse(b, "") }
func addBlock(a *turn.Accumulator, b blocks.ContentBlock)   { a.AddBlock(b, "") }

// Persistence snapshot baselines —— characterization 安全网。
//
// 8 个 snapshot 覆盖各 backend × 各 control event 组合的 chat_messages.blocks_json
// 落库形态。重构 acc / block 类型 / 投影路径时若产生字节级 drift,本测试 fail。
//
// 录入: go test -tags=snapshot -update-snapshots -run TestSnapshot ./internal/service/chat_svc/...
// CI 跑: go test -tags=snapshot ./internal/service/chat_svc/...

// ── 1. claudecode AskUserQuestion turn ──────────────────────────────────────
func TestSnapshot_ClaudecodeAskUserQuestion(t *testing.T) {
	acc := turn.New()
	acc.AddText("Let me ask you a question first.")
	addBlock(acc, chatblocks.UserAskBlock{
		RequestID: "req-1",
		Questions: []chatblocks.AskQuestionDTO{{
			ID:       "q1",
			Question: "Which approach?",
			Header:   "Pick one",
			Options: []chatblocks.AskOptionDTO{
				{Label: "A", Description: "first"},
				{Label: "B", Description: "second"},
			},
		}},
		Answered: true,
		Answers: []chatblocks.AskAnswerDTO{
			{QuestionIndex: 0, Labels: []string{"A"}},
		},
	})
	acc.AddText("Thanks, proceeding with A.")

	assertBlocksSnapshot(t, "claudecode_ask_user_question", acc.Finalize())
}

// ── 2. claudecode Write + Edit turn ─────────────────────────────────────────
func TestSnapshot_ClaudecodeWriteEdit(t *testing.T) {
	acc := turn.New()
	acc.AddText("Will create then edit a file.")
	addToolUse(acc, &blocks.ToolUseBlock{
		ID:    "tu-1",
		Name:  "Write",
		Input: map[string]any{"file_path": "/tmp/a.txt", "content": "hello"},
	})
	acc.AddToolResult(&blocks.ToolResultBlock{
		ToolUseID: "tu-1",
		Content:   []blocks.ContentBlock{&blocks.TextBlock{Text: "wrote 5 bytes"}},
	})
	addToolUse(acc, &blocks.ToolUseBlock{
		ID:   "tu-2",
		Name: "Edit",
		Input: map[string]any{
			"file_path":  "/tmp/a.txt",
			"old_string": "hello",
			"new_string": "world",
		},
	})
	acc.AddToolResult(&blocks.ToolResultBlock{
		ToolUseID: "tu-2",
		Content:   []blocks.ContentBlock{&blocks.TextBlock{Text: "ok"}},
	})

	assertBlocksSnapshot(t, "claudecode_write_edit", acc.Finalize())
}

// ── 3. claudecode subagent turn (Task tool + nested calls) ──────────────────
// 当前 SubagentInfo 仅走 stream 不落 block;本 snapshot 锁定"现状落库形态"
// (Plan B/C 加 SubagentStateBlock 时与本基线 diff 即重构正确性证据)。
func TestSnapshot_ClaudecodeSubagent(t *testing.T) {
	acc := turn.New()
	acc.AddText("Delegating to subagent.")
	addToolUse(acc, &blocks.ToolUseBlock{
		ID:   "task-1",
		Name: "Task",
		Input: map[string]any{
			"description":   "find bug X",
			"subagent_type": "general-purpose",
			"prompt":        "explore repo",
		},
	})
	// 内层 nested tool_use(目前 cago ToolUseBlock 没有 ParentToolUseID 关联,
	// 通过事件流的时序 + chat_svc 投影补关联;此处仅模拟落库形态)。
	addToolUse(acc, &blocks.ToolUseBlock{
		ID:    "nested-1",
		Name:  "Read",
		Input: map[string]any{"file_path": "/tmp/x.go"},
	})
	acc.AddToolResult(&blocks.ToolResultBlock{
		ToolUseID: "nested-1",
		Content:   []blocks.ContentBlock{&blocks.TextBlock{Text: "file contents"}},
	})
	acc.AddToolResult(&blocks.ToolResultBlock{
		ToolUseID: "task-1",
		Content:   []blocks.ContentBlock{&blocks.TextBlock{Text: "subagent found X"}},
	})

	assertBlocksSnapshot(t, "claudecode_subagent", acc.Finalize())
}

// ── 4. claudecode ExitPlanMode (tool_permission_request 通道) ───────────────
func TestSnapshot_ClaudecodeExitPlanMode(t *testing.T) {
	acc := turn.New()
	acc.AddText("Plan ready for approval.")
	addBlock(acc, chatblocks.ToolPermissionBlock{
		RequestID: "perm-1",
		ToolName:  "ExitPlanMode",
		ToolInput: map[string]any{
			"plan": "## Plan\n- step A\n- step B\n",
		},
		Resolved:    true,
		Allowed:     true,
		AlwaysAllow: false,
	})
	acc.AddText("Executing approved plan.")

	assertBlocksSnapshot(t, "claudecode_exit_plan_mode", acc.Finalize())
}

// ── 5. codex file_change turn ───────────────────────────────────────────────
func TestSnapshot_CodexFileChange(t *testing.T) {
	acc := turn.New()
	addToolUse(acc, &blocks.ToolUseBlock{
		ID:   "fc-1",
		Name: "file_change",
		Input: map[string]any{
			"changes": []any{
				map[string]any{"path": "/a", "kind": "created", "diff": "+ hello\n"},
				map[string]any{"path": "/b", "kind": "modified", "diff": "-old\n+new\n"},
			},
		},
	})
	acc.AddToolResult(&blocks.ToolResultBlock{
		ToolUseID: "fc-1",
		Content:   []blocks.ContentBlock{&blocks.TextBlock{Text: "applied"}},
	})

	assertBlocksSnapshot(t, "codex_file_change", acc.Finalize())
}

// ── 6. codex update_plan turn ───────────────────────────────────────────────
func TestSnapshot_CodexUpdatePlan(t *testing.T) {
	acc := turn.New()
	acc.AddText("Updating plan.")
	addBlock(acc, PlanBlock{
		Steps: []PlanStepDTO{
			{Step: "step 1", Status: "completed"},
			{Step: "step 2", Status: "in_progress"},
		},
		Text: "- [x] step 1\n- [ ] step 2\n",
	})

	assertBlocksSnapshot(t, "codex_update_plan", acc.Finalize())
}

// ── 7. codex request_user_input turn ────────────────────────────────────────
func TestSnapshot_CodexRequestUserInput(t *testing.T) {
	acc := turn.New()
	acc.AddText("Need input.")
	addBlock(acc, chatblocks.UserAskBlock{
		RequestID: "rui-1",
		Questions: []chatblocks.AskQuestionDTO{{
			ID:       "q1",
			Question: "Continue?",
			Options: []chatblocks.AskOptionDTO{
				{Label: "yes"},
				{Label: "no"},
			},
		}},
		Answered: true,
		Answers: []chatblocks.AskAnswerDTO{
			{QuestionIndex: 0, Labels: []string{"yes"}},
		},
	})

	assertBlocksSnapshot(t, "codex_request_user_input", acc.Finalize())
}

// ── 8. builtin simple turn (无 control event) ───────────────────────────────
func TestSnapshot_BuiltinSimpleTurn(t *testing.T) {
	acc := turn.New()
	acc.AddText("Hello from builtin agent.")
	addToolUse(acc, &blocks.ToolUseBlock{
		ID:    "tu-1",
		Name:  "Bash",
		Input: map[string]any{"command": "echo hi"},
	})
	acc.AddToolResult(&blocks.ToolResultBlock{
		ToolUseID: "tu-1",
		Content:   []blocks.ContentBlock{&blocks.TextBlock{Text: "hi\n"}},
	})
	acc.AddText("Done.")

	assertBlocksSnapshot(t, "builtin_simple_turn", acc.Finalize())
}

package chat_svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/protorpctest"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo/mock_project_location_repo"
	chatblocks "github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/goal"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/ipc"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/view"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

type prepareTurnRuntime struct{}

func (prepareTurnRuntime) Capabilities() capability.Capabilities { return capability.Capabilities{} }

func (prepareTurnRuntime) Run(context.Context, agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	return nil, nil, nil
}

func TestToChatMessage_BlockTypes(t *testing.T) {
	m := &chat_entity.Message{ID: 1, SessionID: 9, Role: "assistant"}
	require.NoError(t, m.SetBlocks([]blocks.ContentBlock{
		blocks.TextBlock{Text: "hello"},
		blocks.ThinkingBlock{Text: "let me think"},
		blocks.ToolUseBlock{ID: "toolu_1", Name: "shell", Input: map[string]any{"cmd": "ls"}},
		blocks.ToolResultBlock{ToolUseID: "toolu_1", Content: []blocks.ContentBlock{blocks.TextBlock{Text: "file.txt"}}},
		PlanBlock{Text: "Plan\n- [x] Inspect files"},
	}))

	cm, err := toChatMessage(m)
	require.NoError(t, err)
	require.Len(t, cm.Blocks, 5)

	assert.Equal(t, "text", cm.Blocks[0].Type)
	assert.Equal(t, "hello", cm.Blocks[0].Text)

	assert.Equal(t, "thinking", cm.Blocks[1].Type)
	assert.Equal(t, "let me think", cm.Blocks[1].Text)

	assert.Equal(t, "tool_use", cm.Blocks[2].Type)
	assert.Equal(t, "toolu_1", cm.Blocks[2].ToolCallID)
	assert.Equal(t, "shell", cm.Blocks[2].ToolName)
	assert.Equal(t, "ls", cm.Blocks[2].ToolInput["cmd"])

	assert.Equal(t, "tool_result", cm.Blocks[3].Type)
	assert.Equal(t, "toolu_1", cm.Blocks[3].ToolCallID)
	assert.Equal(t, "file.txt", cm.Blocks[3].Text)
	assert.False(t, cm.Blocks[3].IsError)

	assert.Equal(t, "plan", cm.Blocks[4].Type)
	assert.Contains(t, cm.Blocks[4].Text, "Inspect files")
}

// TestToChatMessage_ToolApprovalBlock 验证 ToolApprovalBlock 经 toChatMessage 投影成
// type="tool_approval" + ToolApproval 字段保真(含 ToolKey)。
func TestToChatMessage_ToolApprovalBlock(t *testing.T) {
	m := &chat_entity.Message{ID: 1, SessionID: 9, Role: "assistant"}
	require.NoError(t, m.SetBlocks([]blocks.ContentBlock{
		chatblocks.ToolApprovalBlock{
			ToolKey:   "org",
			RequestID: "org-req-42",
			ToolName:  "org_invite",
			ToolInput: map[string]any{"user_id": "u-99"},
			Status:    "pending",
		},
	}))

	cm, err := toChatMessage(m)
	require.NoError(t, err)
	require.Len(t, cm.Blocks, 1)
	assert.Equal(t, "tool_approval", cm.Blocks[0].Type)
	require.NotNil(t, cm.Blocks[0].ToolApproval)
	assert.Equal(t, "org", cm.Blocks[0].ToolApproval.ToolKey)
	assert.Equal(t, "org-req-42", cm.Blocks[0].ToolApproval.RequestID)
	assert.Equal(t, "org_invite", cm.Blocks[0].ToolApproval.ToolName)
	assert.Equal(t, "u-99", cm.Blocks[0].ToolApproval.ToolInput["user_id"])
	assert.Equal(t, "pending", cm.Blocks[0].ToolApproval.Status)
}

// 历史:ToolResultMetaBlock 已整删,meta 字段改走 raw tool_result.Meta 字节透传
// (StreamToolResult 事件的 toolResultMeta 字段),不再独立 block;原先的
// TestToChatMessage_ToolResultWithMeta / OrphanToolResultMetaIsDropped 一并移除。

func TestToChatMessage_TokenFields(t *testing.T) {
	m := &chat_entity.Message{
		ID: 1, SessionID: 9, Role: "assistant", BlocksJSON: "[]",
		Model:               "claude-sonnet-4-6",
		PromptTokens:        100,
		CompletionTokens:    50,
		CachedTokens:        30,
		CacheCreationTokens: 20,
		ReasoningTokens:     10,
		DurationMs:          1234,
		FirstTokenMs:        420,
		TokensPerSec:        48.5,
	}
	cm, err := toChatMessage(m)
	require.NoError(t, err)
	assert.Equal(t, 100, cm.PromptTokens)
	assert.Equal(t, 50, cm.CompletionTokens)
	assert.Equal(t, 30, cm.CachedTokens)
	assert.Equal(t, 20, cm.CacheCreationTokens)
	assert.Equal(t, 10, cm.ReasoningTokens)
	assert.Equal(t, 1234, cm.DurationMs)
	assert.Equal(t, 420, cm.FirstTokenMs)
	assert.Equal(t, 48.5, cm.TokensPerSec)
}

// TestToChatMessage_NestedToolUse pins replay 把 subagent 内层 ToolUse 投影成
// type=tool_use + ParentToolCallID(json: parentToolUseId)。前端 chat.tsx
// collectChildren 据此把它从主流程移走、挂到外层 AgentSpawnCard.childBlocks。
// Read 这类只读工具没有 canonical 形状,内层同样不凭空造一个(写工具走
// TestToChatMessage_NestedToolUseCarriesCanonical 那一条)。
func TestToChatMessage_NestedToolUse(t *testing.T) {
	m := &chat_entity.Message{ID: 1, SessionID: 9, Role: "assistant"}
	require.NoError(t, m.SetBlocks([]blocks.ContentBlock{
		chatblocks.NestedToolUseBlock{
			ID:               "nested-1",
			Name:             "Read",
			Input:            map[string]any{"file_path": "/x.go"},
			ParentToolCallID: "task-outer-1",
			SubagentRunID:    "run-1",
		},
	}))

	cm, err := toChatMessage(m)
	require.NoError(t, err)
	require.Len(t, cm.Blocks, 1)
	assert.Equal(t, "tool_use", cm.Blocks[0].Type)
	assert.Equal(t, "nested-1", cm.Blocks[0].ToolCallID)
	assert.Equal(t, "Read", cm.Blocks[0].ToolName)
	assert.Equal(t, "/x.go", cm.Blocks[0].ToolInput["file_path"])
	assert.Equal(t, "task-outer-1", cm.Blocks[0].ParentToolCallID)
	assert.Equal(t, "run-1", cm.Blocks[0].SubagentRunID)
	assert.Nil(t, cm.Blocks[0].Canonical, "Read 没有 canonical 形状,不该凭空造一个")
}

// TestToChatMessage_NestedToolUseCarriesCanonical pins subagent 内层的**写工具**在
// replay 时同样带上 canonical。侧栏「变更」页与 AgentSpawnCard 组头的「改了几个
// 文件 / ±行数」都只认 canonical(前端 tier.ts / transcript-rows.ts 明确不查工具名
// 表),live 路径(handlers.ToolCallHandler)一直带着它 —— replay 不带就等于「会话
// 重开之后,子代理改过的文件从这两处一起消失」。判据与 SessionWrittenPaths 同一
// 条:嵌套调用也是 AI 的写入。
func TestToChatMessage_NestedToolUseCarriesCanonical(t *testing.T) {
	m := &chat_entity.Message{ID: 1, SessionID: 9, Role: "assistant"}
	require.NoError(t, m.SetBlocks([]blocks.ContentBlock{
		chatblocks.NestedToolUseBlock{
			ID:   "nested-edit-1",
			Name: "Edit",
			Input: map[string]any{
				"file_path":  "/repo/a.go",
				"old_string": "old\n",
				"new_string": "new\n",
			},
			ParentToolCallID: "task-outer-1",
			SubagentRunID:    "run-1",
		},
	}))

	cm, err := toChatMessage(m)
	require.NoError(t, err)
	require.Len(t, cm.Blocks, 1)
	require.NotNil(t, cm.Blocks[0].Canonical)
	assert.Equal(t, canonical.KindFileEdit, cm.Blocks[0].Canonical.Kind)
	require.NotNil(t, cm.Blocks[0].Canonical.FileEdit)
	require.Len(t, cm.Blocks[0].Canonical.FileEdit.Files, 1)
	patch := cm.Blocks[0].Canonical.FileEdit.Files[0]
	assert.Equal(t, "/repo/a.go", patch.Path)
	assert.Equal(t, 1, patch.Plus)
	assert.Equal(t, 1, patch.Minus)
}

// TestToChatMessage_NestedToolResult 同上,镜像 NestedToolResultBlock 路径:
// ToolCallID 原样落到块上、Content 拍平进 Text、ParentToolCallID 透传。
func TestToChatMessage_NestedToolResult(t *testing.T) {
	m := &chat_entity.Message{ID: 1, SessionID: 9, Role: "assistant"}
	require.NoError(t, m.SetBlocks([]blocks.ContentBlock{
		chatblocks.NestedToolResultBlock{
			ToolCallID:       "nested-1",
			Content:          "hello\n",
			IsError:          true,
			ParentToolCallID: "task-outer-1",
			SubagentRunID:    "run-1",
		},
	}))

	cm, err := toChatMessage(m)
	require.NoError(t, err)
	require.Len(t, cm.Blocks, 1)
	assert.Equal(t, "tool_result", cm.Blocks[0].Type)
	assert.Equal(t, "nested-1", cm.Blocks[0].ToolCallID)
	assert.Equal(t, "hello\n", cm.Blocks[0].Text)
	assert.True(t, cm.Blocks[0].IsError)
	assert.Equal(t, "task-outer-1", cm.Blocks[0].ParentToolCallID)
	assert.Equal(t, "run-1", cm.Blocks[0].SubagentRunID)
}

// TestToChatMessage_SkipsSubagentStateAndPermissionModeChange pins 两个无 UI 元素
// 的 ToUI block 在 replay 时被 skip(不下行成 type=unknown 让前端渲染 debug 卡)。
// SubagentStateBlock 是累计态(tokens/duration/status),前端 AgentSpawnCard
// 由外层 Task tool 的 canonical.agentSpawn 读 —— live 路径靠 dispatcher_emitter
// 注入,replay 不重算。PermissionModeChangeBlock 是审计 block。
func TestToChatMessage_SkipsSubagentStateAndPermissionModeChange(t *testing.T) {
	m := &chat_entity.Message{ID: 1, SessionID: 9, Role: "assistant"}
	require.NoError(t, m.SetBlocks([]blocks.ContentBlock{
		blocks.TextBlock{Text: "before"},
		chatblocks.SubagentStateBlock{ParentToolCallID: "outer", TotalTokens: 123, Status: "completed"},
		chatblocks.PermissionModeChangeBlock{From: "default", To: "plan", At: 1000},
		blocks.TextBlock{Text: "after"},
	}))

	cm, err := toChatMessage(m)
	require.NoError(t, err)
	require.Len(t, cm.Blocks, 2, "skip 后只剩 2 条 text,不能出现 type=unknown 兜底卡")
	assert.Equal(t, "text", cm.Blocks[0].Type)
	assert.Equal(t, "before", cm.Blocks[0].Text)
	assert.Equal(t, "text", cm.Blocks[1].Type)
	assert.Equal(t, "after", cm.Blocks[1].Text)
}

// TestToChatMessage_SubagentStateMergedOntoToolUseBlock 回归后台任务跨轮/重载后
// 不可见的问题:SubagentStateBlock 的元数据必须附到匹配的 tool_use ChatBlock 的
// .Subagent 字段上,不能再作为独立 block 下行前端(否则出 debug 卡)。
func TestToChatMessage_SubagentStateMergedOntoToolUseBlock(t *testing.T) {
	m := &chat_entity.Message{ID: 1, SessionID: 9, Role: "assistant"}
	require.NoError(t, m.SetBlocks([]blocks.ContentBlock{
		blocks.ToolUseBlock{ID: "tu1", Name: "Bash", Input: map[string]any{"command": "sleep 20"}},
		chatblocks.SubagentStateBlock{
			ParentToolCallID: "tu1",
			Kind:             "local_bash",
			Description:      "sleep 20",
			Status:           "running",
			TaskID:           "task-abc",
			TotalTokens:      100,
			DurationMs:       500,
			LastToolName:     "computer",
			ToolUses:         3,
			Model:            "claude-haiku-4-5-20251001",
		},
	}))

	cm, err := toChatMessage(m)
	require.NoError(t, err)
	// 只有 1 个 block(tool_use),不能有独立的 subagent_state 或 unknown 卡。
	require.Len(t, cm.Blocks, 1, "SubagentStateBlock 不能作为独立 block 下行,只能合入 tool_use")

	tb := cm.Blocks[0]
	assert.Equal(t, "tool_use", tb.Type)
	assert.Equal(t, "tu1", tb.ToolCallID)

	require.NotNil(t, tb.Subagent, "tool_use 块必须携带 .Subagent 元数据")
	assert.Equal(t, "local_bash", tb.Subagent.Kind)
	assert.Equal(t, "sleep 20", tb.Subagent.TaskDescription)
	assert.Equal(t, "running", tb.Subagent.Status)
	assert.Equal(t, "task-abc", tb.Subagent.TaskID)
	assert.Equal(t, 100, tb.Subagent.TotalTokens)
	assert.Equal(t, 500, tb.Subagent.DurationMs)
	assert.Equal(t, "computer", tb.Subagent.LastToolName)
	assert.Equal(t, 3, tb.Subagent.ToolUses)
	// R6:replay 路径下模型须与流式期间一致 —— 随 SubagentStateBlock.Model 一起投影。
	assert.Equal(t, "claude-haiku-4-5-20251001", tb.Subagent.Model)
}

// TestToChatMessage_SubagentStateWithNoMatchingToolUse 无匹配 tool_use 时
// SubagentStateBlock 仍然被 skip(不产生独立 block)。
func TestToChatMessage_SubagentStateWithNoMatchingToolUse(t *testing.T) {
	m := &chat_entity.Message{ID: 1, SessionID: 9, Role: "assistant"}
	require.NoError(t, m.SetBlocks([]blocks.ContentBlock{
		blocks.TextBlock{Text: "hello"},
		chatblocks.SubagentStateBlock{
			ParentToolCallID: "no-match",
			Kind:             "local_bash",
			Status:           "completed",
		},
	}))

	cm, err := toChatMessage(m)
	require.NoError(t, err)
	require.Len(t, cm.Blocks, 1, "无匹配 tool_use 时 SubagentStateBlock 仍 skip")
	assert.Equal(t, "text", cm.Blocks[0].Type)
}

func TestToChatMessage_NormalizedPiReplayPreservesGrouping(t *testing.T) {
	m := &chat_entity.Message{ID: 1, SessionID: 9, Role: "assistant"}
	runs := []agentruntime.SubagentRun{
		{ID: "run-0", Index: 0, Agent: "scout", Task: "inspect", RequestedModel: "small", Model: "observed", Status: "completed", Summary: "done"},
		{ID: "run-1", Index: 1, Agent: "worker", Task: "test", Status: "running", LastToolName: "bash"},
	}
	require.NoError(t, m.SetBlocks([]blocks.ContentBlock{
		blocks.ToolUseBlock{ID: "outer", Name: "Vendor__SubAgent", Input: map[string]any{"tasks": []any{}}},
		chatblocks.SubagentStateBlock{ParentToolCallID: "outer", Mode: "parallel", Runs: runs, Status: "running"},
		chatblocks.NestedToolUseBlock{ID: "child-0", Name: "Read", ParentToolCallID: "outer", SubagentRunID: "run-0"},
		chatblocks.NestedToolUseBlock{ID: "child-unknown", Name: "Bash", ParentToolCallID: "outer"},
	}))

	cm, err := toChatMessage(m)
	require.NoError(t, err)
	require.Len(t, cm.Blocks, 3)
	outer := cm.Blocks[0]
	require.NotNil(t, outer.Subagent)
	assert.Equal(t, "parallel", outer.Subagent.Mode)
	assert.Equal(t, runs, outer.Subagent.Runs)
	require.NotNil(t, outer.Canonical)
	require.NotNil(t, outer.Canonical.AgentSpawn)
	assert.Equal(t, "parallel", outer.Canonical.AgentSpawn.Mode)
	require.Len(t, outer.Canonical.AgentSpawn.Runs, 2)
	assert.Equal(t, "small", outer.Canonical.AgentSpawn.Runs[0].RequestedModel)
	assert.Equal(t, "run-0", cm.Blocks[1].SubagentRunID)
	assert.Empty(t, cm.Blocks[2].SubagentRunID, "missing run ID must survive as an unassigned fallback step")
}

func TestConvertOldEventToNew_PreservesSubagentRunID(t *testing.T) {
	call := convertOldEventToNew(agentruntime.RuntimeEvent{
		Kind: agentruntime.EventToolUseStart,
		ToolUse: &agentruntime.ToolUseEvent{
			ID: "child", ParentToolCallID: "outer", SubagentRunID: "run-1",
		},
	}).(agentruntime.ToolCall)
	assert.Equal(t, "run-1", call.SubagentRunID)

	result := convertOldEventToNew(agentruntime.RuntimeEvent{
		Kind: agentruntime.EventToolResult,
		ToolResult: &agentruntime.ToolResultEvent{
			ToolCallID: "child", ParentToolCallID: "outer", SubagentRunID: "run-1",
		},
	}).(agentruntime.ToolResult)
	assert.Equal(t, "run-1", result.SubagentRunID)
}

func TestToChatMessage_NoticeBlockProjection(t *testing.T) {
	m := &chat_entity.Message{ID: 1, SessionID: 9, Role: "assistant"}
	require.NoError(t, m.SetBlocks([]blocks.ContentBlock{
		blocks.NoticeBlock{Level: "info", Text: "hi"},
	}))

	cm, err := toChatMessage(m)
	require.NoError(t, err)
	require.Len(t, cm.Blocks, 1)
	assert.Equal(t, "notice", cm.Blocks[0].Type)
	assert.Equal(t, "info", cm.Blocks[0].Level)
	// 非结构化文本(旧数据 / 其它来源的 notice)原样渲染 Text,不带供应商字段。
	assert.Equal(t, "hi", cm.Blocks[0].Text)
	assert.Empty(t, cm.Blocks[0].ProviderKey)
}

func TestToChatMessage_NoticeBlockProjectionDecodesStructuredPayload(t *testing.T) {
	m := &chat_entity.Message{ID: 1, SessionID: 9, Role: "assistant"}
	require.NoError(t, m.SetBlocks([]blocks.ContentBlock{
		blocks.NoticeBlock{Level: "info", Text: `{"providerKey":"key-99"}`},
	}))

	cm, err := toChatMessage(m)
	require.NoError(t, err)
	require.Len(t, cm.Blocks, 1)
	assert.Equal(t, "notice", cm.Blocks[0].Type)
	assert.Equal(t, "info", cm.Blocks[0].Level)
	assert.Equal(t, "key-99", cm.Blocks[0].ProviderKey)
	// 结构化负载不把原始 JSON 泄漏给前端 —— 前端用 ProviderKey 走 t() 渲染。
	assert.Empty(t, cm.Blocks[0].Text)
}

// TestToChatMessage_NoticeBlockProjectionDecodesProviderSwitch 钉死切换 notice 的投影
// (2026-08-10 决策 9):切回「跟随 agent 绑定」时负载里没有 providerKey,只有 kind ——
// 投影必须靠 kind 认出它是结构化负载,否则会掉进「原样渲染 Text」的兜底分支,把原始
// JSON 直接泄漏到界面上。
func TestToChatMessage_NoticeBlockProjectionDecodesProviderSwitch(t *testing.T) {
	cases := []struct {
		name         string
		text         string
		providerKey  string
		providerName string
		modelKey     string
		modelName    string
	}{
		{name: "切到某个供应商(provider-default)", text: view.EncodeProviderSwitch("key-99", "", "中转 · GLM 5.2", ""), providerKey: "key-99", providerName: "中转 · GLM 5.2"},
		{name: "切到 fixed-model", text: view.EncodeProviderSwitch("key-99", "mk-haiku", "中转 · GLM 5.2", "GLM 5.2"), providerKey: "key-99", providerName: "中转 · GLM 5.2", modelKey: "mk-haiku", modelName: "GLM 5.2"},
		{name: "切回跟随 agent 绑定", text: view.EncodeProviderSwitch("", "", "", ""), providerKey: "", providerName: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &chat_entity.Message{ID: 1, SessionID: 9, Role: "assistant"}
			require.NoError(t, m.SetBlocks([]blocks.ContentBlock{
				blocks.NoticeBlock{Level: "info", Text: tc.text},
			}))

			cm, err := toChatMessage(m)
			require.NoError(t, err)
			require.Len(t, cm.Blocks, 1)
			assert.Equal(t, "notice", cm.Blocks[0].Type)
			assert.Equal(t, "switch", cm.Blocks[0].NoticeKind, "前端据此选切换文案而非回退文案")
			assert.Equal(t, tc.providerKey, cm.Blocks[0].ProviderKey)
			assert.Equal(t, tc.providerName, cm.Blocks[0].ProviderName, "展示名随投影透传给前端")
			assert.Equal(t, tc.modelKey, cm.Blocks[0].ModelKey, "固定模型 key 随投影透传给前端")
			assert.Equal(t, tc.modelName, cm.Blocks[0].ModelName, "固定模型展示名随投影透传给前端")
			assert.Empty(t, cm.Blocks[0].Text, "结构化负载不把原始 JSON 泄漏给前端")
		})
	}
}

// TestToChatMessage_NoticeBlockProjectionKeepsFallbackKindEmpty 既有回退 notice(含全部
// 旧数据)不带 kind:前端按空 kind 走回退文案,这一路不能被切换 notice 的新字段带偏。
func TestToChatMessage_NoticeBlockProjectionKeepsFallbackKindEmpty(t *testing.T) {
	m := &chat_entity.Message{ID: 1, SessionID: 9, Role: "assistant"}
	require.NoError(t, m.SetBlocks([]blocks.ContentBlock{
		blocks.NoticeBlock{Level: "info", Text: view.EncodeProviderFallback("gone-provider", "")},
	}))

	cm, err := toChatMessage(m)
	require.NoError(t, err)
	require.Len(t, cm.Blocks, 1)
	assert.Equal(t, "gone-provider", cm.Blocks[0].ProviderKey)
	assert.Empty(t, cm.Blocks[0].NoticeKind)
}

func TestAskQuestionsToDTO_PreservesRequestUserInputMetadata(t *testing.T) {
	got := chatblocks.QuestionsFromRuntime([]agentruntime.AskQuestion{{
		ID:          "target",
		Question:    "Which target?",
		Header:      "Target",
		MultiSelect: false,
		IsOther:     true,
		IsSecret:    true,
		Options: []agentruntime.AskOption{{
			Label:       "backend",
			Description: "Backend only.",
			Preview:     "go test ./...",
		}},
	}})

	require.Len(t, got, 1)
	assert.Equal(t, "target", got[0].ID)
	assert.Equal(t, "Which target?", got[0].Question)
	assert.Equal(t, "Target", got[0].Header)
	assert.False(t, got[0].MultiSelect)
	assert.True(t, got[0].IsOther)
	assert.True(t, got[0].IsSecret)
	require.Len(t, got[0].Options, 1)
	assert.Equal(t, "backend", got[0].Options[0].Label)
	assert.Equal(t, "Backend only.", got[0].Options[0].Description)
	assert.Equal(t, "go test ./...", got[0].Options[0].Preview)
}

func TestCreatePermissionMode_DefaultFallback(t *testing.T) {
	convey.Convey("createPermissionMode 在 raw 空串时回落到 backend.DefaultPermissionMode", t, func() {
		ctx := context.Background()
		be := &agent_backend_entity.AgentBackend{
			Type:                  string(agent_backend_entity.TypeClaudeCode),
			DefaultPermissionMode: "plan",
		}
		mode, err := ipc.CreatePermissionMode(ctx, be, "", true)
		assert.NoError(t, err)
		assert.Equal(t, "plan", mode)
	})

	convey.Convey("createPermissionMode 在 raw 与 backend default 都空时返回空串", t, func() {
		ctx := context.Background()
		be := &agent_backend_entity.AgentBackend{
			Type: string(agent_backend_entity.TypeClaudeCode),
		}
		mode, err := ipc.CreatePermissionMode(ctx, be, "", true)
		assert.NoError(t, err)
		assert.Equal(t, "", mode)
	})

	convey.Convey("createPermissionMode 在 raw 非空时不受 backend default 干扰", t, func() {
		ctx := context.Background()
		be := &agent_backend_entity.AgentBackend{
			Type:                  string(agent_backend_entity.TypeClaudeCode),
			DefaultPermissionMode: "plan",
		}
		mode, err := ipc.CreatePermissionMode(ctx, be, "bypassPermissions", true)
		assert.NoError(t, err)
		assert.Equal(t, "bypassPermissions", mode)
	})
}

// TestCreatePermissionMode_BypassDefaultStartsInPlan 覆盖 claudecode agent 配
// DefaultPermissionMode=bypassPermissions 时, 新会话以 plan 起手的派生规则。
//
// session.PermissionMode 留 plan 是为了让前端 pill 显示 Plan + 让用户先做计划,
// 真实 CLI 启动仍按 bypassPermissions(在 claudecode runtime 的 resolveLaunchMode
// 强制), 这条规则与 spawn-after SetPermissionMode 同步链共同支撑「先 plan 后
// bypass」工作流。
func TestCreatePermissionMode_BypassDefaultStartsInPlan(t *testing.T) {
	convey.Convey("Given claudecode + DefaultPermissionMode=bypass, When raw 空, Then 返 plan 起手", t, func() {
		ctx := context.Background()
		be := &agent_backend_entity.AgentBackend{
			Type:                  string(agent_backend_entity.TypeClaudeCode),
			DefaultPermissionMode: "bypassPermissions",
		}
		mode, err := ipc.CreatePermissionMode(ctx, be, "", true)
		assert.NoError(t, err)
		assert.Equal(t, "plan", mode)
	})

	convey.Convey("Given claudecode + bypass default, When planFirst=false (自律会话), Then 直接落 bypass 不强切 plan", t, func() {
		ctx := context.Background()
		be := &agent_backend_entity.AgentBackend{
			Type:                  string(agent_backend_entity.TypeClaudeCode),
			DefaultPermissionMode: "bypassPermissions",
		}
		mode, err := ipc.CreatePermissionMode(ctx, be, "", false)
		assert.NoError(t, err)
		assert.Equal(t, "bypassPermissions", mode)
	})

	convey.Convey("Given claudecode + bypass default, When raw 显式非空, Then 尊重 raw 不强切 plan", t, func() {
		ctx := context.Background()
		be := &agent_backend_entity.AgentBackend{
			Type:                  string(agent_backend_entity.TypeClaudeCode),
			DefaultPermissionMode: "bypassPermissions",
		}
		mode, err := ipc.CreatePermissionMode(ctx, be, "acceptEdits", true)
		assert.NoError(t, err)
		assert.Equal(t, "acceptEdits", mode)
	})

	convey.Convey("Given non-claudecode backend + bypass default, When raw 空, Then 不触发 plan 起手 (规则仅对 claudecode 生效)", t, func() {
		// codex / builtin 不应被这条规则影响; entity.Check 实际禁止非 claudecode 配
		// bypass, 这里用直接构造的实体跨过校验是为了断言推断分支的 backend 类型门禁。
		ctx := context.Background()
		be := &agent_backend_entity.AgentBackend{
			Type:                  string(agent_backend_entity.TypeCodex),
			DefaultPermissionMode: "bypassPermissions",
		}
		mode, err := ipc.CreatePermissionMode(ctx, be, "", true)
		// codex 不允许 bypassPermissions, validate 会回 ChatPermissionModeInvalid;
		// 关键是这里没有走 plan 分支, 错误从 validateRequestedPermissionMode 抛出。
		assert.Error(t, err)
		assert.Equal(t, "", mode)
	})
}

// TestCreatePermissionMode_CrossTypeOverrideFallsBack 回归用户报告: 新建会话在空会话
// 态把执行目标改选到另一个类型的后端(如 claudecode 主后端 → codex 后端)后首发,前端
// 按 agent 主后端推导的 permissionMode(如 acceptEdits/bypassPermissions)对实际后端
// 不合法, createPermissionMode 必须回落到该后端的默认派生而不是硬报
// ChatPermissionModeInvalid —— 否则一次合法的改选连第一条消息都发不出去。
//
// 真正需要拒绝非法 mode 的路径是 SetPermissionMode 那条 IPC 线(现有测试锁死), 新建
// 会话 Send 的 mode 只是前端偏好, 后端(唯一知道实际后端的地方)在边界归一。
func TestCreatePermissionMode_CrossTypeOverrideFallsBack(t *testing.T) {
	convey.Convey("Given codex 实际后端, When raw 是 claudecode 才合法的 acceptEdits, Then 回落到 codex 默认 default 不报错", t, func() {
		ctx := context.Background()
		be := &agent_backend_entity.AgentBackend{
			Type: string(agent_backend_entity.TypeCodex),
		}
		mode, err := ipc.CreatePermissionMode(ctx, be, "acceptEdits", true)
		assert.NoError(t, err)
		assert.Equal(t, "default", mode)
	})

	convey.Convey("Given codex 实际后端, When raw 是 claudecode 才合法的 bypassPermissions, Then 回落到 codex 默认不报错", t, func() {
		ctx := context.Background()
		be := &agent_backend_entity.AgentBackend{
			Type: string(agent_backend_entity.TypeCodex),
		}
		mode, err := ipc.CreatePermissionMode(ctx, be, "bypassPermissions", true)
		assert.NoError(t, err)
		assert.Equal(t, "default", mode)
	})

	convey.Convey("Given codex 实际后端, When raw 对该后端本来就合法(default), Then 尊重 raw 不回落", t, func() {
		ctx := context.Background()
		be := &agent_backend_entity.AgentBackend{
			Type: string(agent_backend_entity.TypeCodex),
		}
		mode, err := ipc.CreatePermissionMode(ctx, be, "plan", true)
		assert.NoError(t, err)
		assert.Equal(t, "plan", mode)
	})

	convey.Convey("Given builtin 实际后端(0 档), When raw 非空 carryover, Then 回落为空串不报错", t, func() {
		ctx := context.Background()
		be := &agent_backend_entity.AgentBackend{
			Type: string(agent_backend_entity.TypeBuiltin),
		}
		mode, err := ipc.CreatePermissionMode(ctx, be, "acceptEdits", true)
		assert.NoError(t, err)
		assert.Equal(t, "", mode)
	})
}

// TestResolveSessionCwd_LocalUsesCwdResolver 验证 be.IsLocal() 时走注入的 CwdResolver 回调。
func TestResolveSessionCwd_LocalUsesCwdResolver(t *testing.T) {
	prev := resolveCwdFn
	t.Cleanup(func() { resolveCwdFn = prev })
	resolveCwdFn = func(ctx context.Context, s *chat_entity.Session) (string, error) {
		return "/Users/me/proj", nil
	}
	sess := &chat_entity.Session{ID: 1, ProjectID: 10, AgentID: 7}
	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: ""} // local
	cwd, err := resolveSessionCwd(context.Background(), sess, be)
	require.NoError(t, err)
	assert.Equal(t, "/Users/me/proj", cwd)
}

// TestResolveSessionCwd_NilBackendUsesCwdResolver 验证 be 为 nil 时（back-compat）也走 CwdResolver。
func TestResolveSessionCwd_NilBackendUsesCwdResolver(t *testing.T) {
	prev := resolveCwdFn
	t.Cleanup(func() { resolveCwdFn = prev })
	resolveCwdFn = func(ctx context.Context, s *chat_entity.Session) (string, error) {
		return "/local", nil
	}
	sess := &chat_entity.Session{ID: 1, ProjectID: 10}
	cwd, err := resolveSessionCwd(context.Background(), sess, nil)
	require.NoError(t, err)
	assert.Equal(t, "/local", cwd)
}

// TestResolveSessionCwd_RemoteHitsProjectLocation 验证 be.IsRemote() 时查 project_location_repo。
func TestResolveSessionCwd_RemoteHitsProjectLocation(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	prevRepo := project_location_repo.ProjectLocation()
	mockRepo := mock_project_location_repo.NewMockProjectLocationRepo(ctrl)
	project_location_repo.RegisterProjectLocation(mockRepo)
	t.Cleanup(func() { project_location_repo.RegisterProjectLocation(prevRepo) })

	mockRepo.EXPECT().FindByProjectAndFingerprint(gomock.Any(), int64(10), testDeviceFingerprint(7)).Return(
		&project_location_entity.ProjectLocation{ID: 42, ProjectID: 10, DeviceID: testDeviceFingerprint(7), Path: "/home/me/proj"}, nil,
	)

	sess := &chat_entity.Session{ID: 1, ProjectID: 10}
	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: testDeviceFingerprint(7)} // remote
	cwd, err := resolveSessionCwd(context.Background(), sess, be)
	require.NoError(t, err)
	assert.Equal(t, "/home/me/proj", cwd)
}

// TestResolveSessionCwd_RemoteFreeSessionSkipsRepo 验证 ProjectID=0（自由会话）+ 远端 backend
// 时直接返回 ("", nil)，把 cwd 兜底权下放给远端 daemon 的 runtime（cwd=="" → AgentCwd）。
// 关键约束：根本不能去查 project_location_repo —— mockRepo 没设 EXPECT，被调用就会 fail。
func TestResolveSessionCwd_RemoteFreeSessionSkipsRepo(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	prevRepo := project_location_repo.ProjectLocation()
	mockRepo := mock_project_location_repo.NewMockProjectLocationRepo(ctrl)
	project_location_repo.RegisterProjectLocation(mockRepo)
	t.Cleanup(func() { project_location_repo.RegisterProjectLocation(prevRepo) })

	sess := &chat_entity.Session{ID: 1, ProjectID: 0, AgentID: 7}
	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: testDeviceFingerprint(7)} // remote
	cwd, err := resolveSessionCwd(context.Background(), sess, be)
	require.NoError(t, err)
	assert.Equal(t, "", cwd)
}

// TestResolveSessionCwd_RemoteMissingLocation 验证远端找不到记录时返回 ProjectLocationMissing 错误。
func TestResolveSessionCwd_RemoteMissingLocation(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	prevRepo := project_location_repo.ProjectLocation()
	mockRepo := mock_project_location_repo.NewMockProjectLocationRepo(ctrl)
	project_location_repo.RegisterProjectLocation(mockRepo)
	t.Cleanup(func() { project_location_repo.RegisterProjectLocation(prevRepo) })

	mockRepo.EXPECT().FindByProjectAndFingerprint(gomock.Any(), int64(10), testDeviceFingerprint(7)).Return(nil, gorm.ErrRecordNotFound)

	sess := &chat_entity.Session{ID: 1, ProjectID: 10}
	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: testDeviceFingerprint(7)}
	_, err := resolveSessionCwd(context.Background(), sess, be)
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, code.ProjectLocationMissing, httpErr.Code)
}

// TestResolveSessionCwd_LocalPropagatesLocalPathMissing 验证 R10:CwdResolver
// (project_svc.ResolveSessionCwd)对「本机未配置路径」返回的确定错误经
// resolveSessionCwd 原样透出 —— 不折叠成 ProjectLocationMissing / WorkspaceFsNoCwd,
// 也不是 ("", nil)。chat_svc 的全部读取点都经这条路径取 cwd,因此这里
// 通过即代表它们随解析点自动生效(R11)。
func TestResolveSessionCwd_LocalPropagatesLocalPathMissing(t *testing.T) {
	prev := resolveCwdFn
	t.Cleanup(func() { resolveCwdFn = prev })
	resolveCwdFn = func(ctx context.Context, s *chat_entity.Session) (string, error) {
		return "", i18n.NewError(ctx, code.ProjectLocalPathMissing)
	}
	sess := &chat_entity.Session{ID: 1, ProjectID: 10, AgentID: 7}
	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: ""} // local
	cwd, err := resolveSessionCwd(context.Background(), sess, be)
	require.Error(t, err)
	assert.Equal(t, "", cwd)
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, code.ProjectLocalPathMissing, httpErr.Code)
	assert.NotEqual(t, code.ProjectLocationMissing, httpErr.Code)
	assert.NotEqual(t, code.WorkspaceFsNoCwd, httpErr.Code)
}

// TestCwdUnavailableReasonFor 锁住 R10 的分类表：三种"没有 cwd"必须映射到三个
// 彼此可区分的取值，且未知/无归类原因的错误落空串兜底，不冒充第四种状态。
func TestCwdUnavailableReasonFor(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, "local-path-missing",
		cwdUnavailableReasonFor(i18n.NewError(ctx, code.ProjectLocalPathMissing)))
	assert.Equal(t, "location-missing",
		cwdUnavailableReasonFor(i18n.NewError(ctx, code.ProjectLocationMissing)))
	assert.Equal(t, "", cwdUnavailableReasonFor(i18n.NewError(ctx, code.WorkspaceFsNoCwd)))
	assert.Equal(t, "", cwdUnavailableReasonFor(errors.New("unrelated failure")))
	assert.Equal(t, "", cwdUnavailableReasonFor(nil))
}

// ── noopDaemonClient ─────────────────────────────────────────────────────────

type noopDaemonClient struct{ conn *protorpc.Conn }

func (c *noopDaemonClient) Conn() *protorpc.Conn {
	if c.conn == nil {
		c.conn = protorpc.NewConn(nil, protorpc.NewRegistry())
	}
	return c.conn
}

func (*noopDaemonClient) Call(_ context.Context, _ string, _, _ any) error { return nil }
func (*noopDaemonClient) Notify(_ string, _ any) error                     { return nil }
func (*noopDaemonClient) Handle(_ string, _ func(context.Context, json.RawMessage) (any, error)) {
}
func (*noopDaemonClient) Closed() <-chan struct{} { return nil }
func (*noopDaemonClient) Close() error            { return nil }

// recordingDaemonClient counts every Call invocation per method — used to
// assert that borrowRemoteRuntime issues exactly one runtime.capabilities
// prefetch on the cold path and zero on cache hits.
type recordingDaemonClient struct {
	mu    sync.Mutex
	calls map[string]int
	queue map[string][]func(params, result any) error
}

func newRecordingDaemonClient() *recordingDaemonClient {
	return &recordingDaemonClient{calls: map[string]int{}, queue: map[string][]func(params, result any) error{}}
}

func (c *recordingDaemonClient) Call(_ context.Context, method string, params, result any) error {
	c.mu.Lock()
	c.calls[method]++
	var fn func(params, result any) error
	if xs := c.queue[method]; len(xs) > 0 {
		fn = xs[0]
		c.queue[method] = xs[1:]
	}
	c.mu.Unlock()
	if fn != nil {
		return fn(params, result)
	}
	return nil
}
func (*recordingDaemonClient) Notify(_ string, _ any) error { return nil }
func (*recordingDaemonClient) Handle(_ string, _ func(context.Context, json.RawMessage) (any, error)) {
}
func (*recordingDaemonClient) Closed() <-chan struct{} { return nil }
func (*recordingDaemonClient) Close() error            { return nil }

func (c *recordingDaemonClient) count(method string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[method]
}

func (c *recordingDaemonClient) expect(method string, fn func(params, result any) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queue[method] = append(c.queue[method], fn)
}

// poolLeaseMocks 把 Pool/Lease/Client 三件套打包,简化各远端缓存测试的注入。
type poolLeaseMocks struct {
	pool   *mock_remote_device_svc.MockConnPool
	lease  *mock_remote_device_svc.MockLease
	client *noopDaemonClient
}

// installMockPool 构造一个 ConnPool / Lease / DaemonClientPort 三件套并注入 svc。
// Pool.Borrow 默认返同一个 Lease;Closed() 返一个永不关的 chan;Release() AnyTimes。
func installMockPool(t *testing.T, ctrl *gomock.Controller, svc *chatSvc, deviceID int64) *poolLeaseMocks {
	t.Helper()
	m := &poolLeaseMocks{
		pool:   mock_remote_device_svc.NewMockConnPool(ctrl),
		lease:  mock_remote_device_svc.NewMockLease(ctrl),
		client: &noopDaemonClient{},
	}
	m.pool.EXPECT().Borrow(gomock.Any(), deviceID).Return(m.lease, nil).AnyTimes()
	m.lease.EXPECT().Client().Return(protorpctest.WrapConnection(m.client)).AnyTimes()
	m.lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	m.lease.EXPECT().Release().AnyTimes()
	svc.setConnPoolForTest(m.pool)
	installPairedDevice(t, ctrl, deviceID)
	installExecDaemonRecorder(t, ctrl)
	return m
}

// installExecDaemonRecorder 给「借出成功即写 (设备, 实例标识) 到会话行」（R15b）装一
// 个宽松桩：只借运行时的测试不关心那次写入，但不装等于让 borrow 在 nil repo 上崩。
func installExecDaemonRecorder(t *testing.T, ctrl *gomock.Controller) {
	t.Helper()
	sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
	sessRepo.EXPECT().UpdateExecDaemon(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil).AnyTimes()
	// 远端 runtime 每条会话要问一次「它在线上叫什么」——读 chat_sessions.conversation_id
	// (见 remote_pool.sessionConversationID)。只借运行时的测试不关心取值,但不装
	// 等于让那一问在 nil repo 上崩。
	sessRepo.EXPECT().Find(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id int64) (*chat_entity.Session, error) {
			return &chat_entity.Session{ID: id, ConversationID: convID(id)}, nil
		}).AnyTimes()
	prev := chat_repo.Session()
	chat_repo.RegisterSession(sessRepo)
	t.Cleanup(func() { chat_repo.RegisterSession(prev) })
}

// testDeviceFingerprint 是这批测试里「第 n 台已配对 daemon」的规范指纹。backend 的
// DeviceID 只有指纹一种形态，派发边界再把它在本机配对表里解析成行 ID。
func testDeviceFingerprint(deviceID int64) string {
	return fmt.Sprintf("sha256:device-%d", deviceID)
}

// installPairedDevice 把 deviceID 那台机器登记成本机已配对 daemon，使
// testDeviceFingerprint(deviceID) 解析得出这一行。
func installPairedDevice(t *testing.T, ctrl *gomock.Controller, deviceID int64) *mock_remote_device_svc.MockRemoteDeviceSvc {
	t.Helper()
	view := &remote_device_svc.DeviceView{
		ID: deviceID, DaemonFingerprint: testDeviceFingerprint(deviceID), Online: true,
	}
	rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	rds.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()
	rds.EXPECT().List(gomock.Any()).Return([]*remote_device_svc.DeviceView{view}, nil).AnyTimes()
	rds.EXPECT().Get(gomock.Any(), deviceID).Return(view, nil).AnyTimes()
	rds.EXPECT().ListDeviceProviders(gomock.Any()).Return(nil).AnyTimes()
	prev := remote_device_svc.Default()
	remote_device_svc.SetDefault(rds)
	t.Cleanup(func() { remote_device_svc.SetDefault(prev) })
	return rds
}

// TestPrepareTurnRun_RemoteSendsEffectiveProviderKey 钉死决策 9 桌面侧:远端 backend
// 组装 RunRequest 时 wire 必须带 effectiveProviderKey(会话 provider_key 优先),
// 而不是 agent 绑定 —— daemon 按它自解。
func TestPrepareTurnRun_RemoteSendsEffectiveProviderKey(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	svc := &chatSvc{}
	installMockPool(t, ctrl, svc, 7)

	sess := &chat_entity.Session{ID: 100, AgentID: 7, ProviderKey: "session-key"}
	a := &agent_entity.Agent{ID: 7, AgentBackendID: 12}
	be := &agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "agent-bound-key",
		DeviceFingerprint: testDeviceFingerprint(7),
	}

	prepared, err := svc.prepareTurnRun(context.Background(), sess, a, be, nil, nil, nil, "", false, false)
	require.NoError(t, err)
	require.NotNil(t, prepared)
	assert.Equal(t, "session-key", prepared.req.LLMProviderKey, "远端应透传 effectiveProviderKey(会话 provider_key 优先)")
	assert.Nil(t, prepared.req.Provider, "远端不跨机器携带明文 APIKey")
}

// TestPrepareTurnRun_RemoteNoSessionProviderKeyFallsBackToAgentBinding 钉死决策 9
// 边界:会话无 provider_key 时 effectiveProviderKey = agent 绑定。
func TestPrepareTurnRun_RemoteNoSessionProviderKeyFallsBackToAgentBinding(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	svc := &chatSvc{}
	installMockPool(t, ctrl, svc, 7)

	sess := &chat_entity.Session{ID: 101, AgentID: 7, ProviderKey: ""}
	a := &agent_entity.Agent{ID: 7, AgentBackendID: 12}
	be := &agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "agent-bound-key",
		DeviceFingerprint: testDeviceFingerprint(7),
	}

	prepared, err := svc.prepareTurnRun(context.Background(), sess, a, be, nil, nil, nil, "", false, false)
	require.NoError(t, err)
	require.NotNil(t, prepared)
	assert.Equal(t, "agent-bound-key", prepared.req.LLMProviderKey, "无会话 provider_key 时回落到 agent 绑定")
	assert.Nil(t, prepared.req.Provider)
}

// TestPrepareTurnRun_SessionReasoningEffortOverridesBackendCopy 钉死「有效力度的合成
// 边界」(spec 2026-09-01 硬不变量 2): buildRunRequest 是把 Backend 交给 agentruntime
// 的两个边界之一,会话行非空的力度必须覆盖后端配置,写在 RunRequest.Backend 的
// **副本**上 —— 解析出的 be 本身不能被改写,否则同一实体在别的读路径(如 LoadSession)
// 上会带上这条会话专属的值。
func TestPrepareTurnRun_SessionReasoningEffortOverridesBackendCopy(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	svc := &chatSvc{}
	installMockPool(t, ctrl, svc, 7)

	sess := &chat_entity.Session{ID: 102, AgentID: 7, ReasoningEffort: "max"}
	a := &agent_entity.Agent{ID: 7, AgentBackendID: 12}
	be := &agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "agent-bound-key",
		DeviceFingerprint: testDeviceFingerprint(7), ReasoningEffort: "low",
	}

	prepared, err := svc.prepareTurnRun(context.Background(), sess, a, be, nil, nil, nil, "", false, false)
	require.NoError(t, err)
	require.NotNil(t, prepared)
	require.NotNil(t, prepared.req.Backend)
	assert.Equal(t, "max", prepared.req.Backend.ReasoningEffort, "会话行非空的力度覆盖后端配置")
	assert.NotSame(t, be, prepared.req.Backend, "合成结果必须写在副本上,不能就地改 be")
	assert.Equal(t, "low", be.ReasoningEffort, "解析出的后端实体本身不能被改写")
}

// TestPrepareTurnRun_EmptySessionReasoningEffortFallsBackToBackendConfig 覆盖合成规则
// 的另一半:会话行力度为空时,RunRequest 里的 backend 副本沿用后端配置的值。
func TestPrepareTurnRun_EmptySessionReasoningEffortFallsBackToBackendConfig(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	svc := &chatSvc{}
	installMockPool(t, ctrl, svc, 7)

	sess := &chat_entity.Session{ID: 103, AgentID: 7, ReasoningEffort: ""}
	a := &agent_entity.Agent{ID: 7, AgentBackendID: 12}
	be := &agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "agent-bound-key",
		DeviceFingerprint: testDeviceFingerprint(7), ReasoningEffort: "high",
	}

	prepared, err := svc.prepareTurnRun(context.Background(), sess, a, be, nil, nil, nil, "", false, false)
	require.NoError(t, err)
	require.NotNil(t, prepared)
	require.NotNil(t, prepared.req.Backend)
	assert.Equal(t, "high", prepared.req.Backend.ReasoningEffort, "会话行力度为空时回落后端配置")
}

// TestPrepareTurnRun_GivenSelfFingerprintBackend_ThenRunsLocally R13 认领后本机
// backend 的 DeviceID 是本机指纹（sha256:self）。prepareTurnRun 必须把这种档当作
// 本地档走全局 runtime 注册表，而不是走 borrowRemoteRuntimeForTurn —— 本机指纹
// 永远不是本机配对表里的 paired agentred 行，borrow 会报 AgentBackendInvalidDevice，
// 本地会话将全部起不了轮。
func TestPrepareTurnRun_GivenSelfFingerprintBackend_ThenRunsLocally(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	rds.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()
	rds.EXPECT().List(gomock.Any()).Return(nil, nil).AnyTimes()
	prevSvc := remote_device_svc.Default()
	remote_device_svc.SetDefault(rds)
	t.Cleanup(func() { remote_device_svc.SetDefault(prevSvc) })
	restoreRuntime := agentruntime.SwapRuntimeForTest(
		agent_backend_entity.TypeClaudeCode,
		prepareTurnRuntime{},
	)
	t.Cleanup(restoreRuntime)

	// 不装 conn pool：任何 borrow 尝试都会因 nil pool 而失败，测试据此暴露错误分支。
	svc := &chatSvc{}
	RegisterCwdResolver(func(context.Context, *chat_entity.Session) (string, error) { return "", nil })
	t.Cleanup(func() { RegisterCwdResolver(nil) })

	sess := &chat_entity.Session{ID: 100, AgentID: 7}
	a := &agent_entity.Agent{ID: 7, AgentBackendID: 12}
	be := &agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "",
		DeviceFingerprint: "sha256:self",
	}

	prepared, err := svc.prepareTurnRun(context.Background(), sess, a, be, nil, nil, nil, "", false, false)
	require.NoError(t, err)
	require.NotNil(t, prepared)
}

// TestBorrowRemoteRuntime_SharesConnAcrossSessions verifies the refcount cache:
// 同一 device 多次借出返回同一 *remote.Runtime 实例;release 减计数,归零摘出 map。
func TestBorrowRemoteRuntime_SharesConnAcrossSessions(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	svc := &chatSvc{}
	installMockPool(t, ctrl, svc, 7)

	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: testDeviceFingerprint(7)}

	r1, err := svc.borrowRemoteRuntime(context.Background(), be, 100)
	require.NoError(t, err)
	r2, err := svc.borrowRemoteRuntime(context.Background(), be, 101)
	require.NoError(t, err)
	assert.Same(t, r1, r2)

	assert.Equal(t, 2, svc.remoteRuntimeCount(7))

	svc.releaseRemoteRuntime(7, 100)
	assert.Equal(t, 1, svc.remoteRuntimeCount(7))

	svc.releaseRemoteRuntime(7, 101)
	assert.Equal(t, 0, svc.remoteRuntimeCount(7))
}

// TestBorrowRemoteRuntimeForTurn_DialFailure_HoldsNothingAndReleaseIsANoop 钉死
// borrowRemoteRuntimeForTurn 的错误契约:借用失败时它一件资源都没占住,交回的
// release 是可安全调用的 no-op(而不是 nil,也不是一个必须被调用的真释放)。
//
// 上游 selectTurnRunner 在 err != nil 时直接 return,那条路径上永远不会调 release;
// 只要这里的契约成立,那就不是租约泄漏。若哪天池改成「先记引用再报错」,或改成在
// 错误路径上交回一个真 release,这条测试立刻红 —— 泄漏就是从那一刻开始的。
func TestBorrowRemoteRuntimeForTurn_DialFailure_HoldsNothingAndReleaseIsANoop(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	svc := &chatSvc{}
	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(nil, errors.New("daemon offline")).AnyTimes()
	svc.setConnPoolForTest(pool)
	installPairedDevice(t, ctrl, 7)
	installExecDaemonRecorder(t, ctrl)

	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: testDeviceFingerprint(7)}

	rt, release, err := svc.borrowRemoteRuntimeForTurn(context.Background(), be, 100)
	require.Error(t, err)
	assert.Nil(t, rt)
	require.NotNil(t, release, "错误路径也必须交回一个可调用的 release")
	assert.Equal(t, 0, svc.remoteRuntimeCount(7), "借用失败不得留下任何会话引用")

	assert.NotPanics(t, release)
	assert.Equal(t, 0, svc.remoteRuntimeCount(7), "no-op release 不改变任何计数")
}

// TestBorrowRemoteRuntime_PrefetchesCapabilities_OncePerDevice 钉死 Plan B
// 行为:cold path borrow 时同步发一发 runtime.capabilities,缓存到 *remote.Runtime
// 内;同 device 后续 borrow 命中 cache,不再发 RPC。
func TestBorrowRemoteRuntime_PrefetchesCapabilities_OncePerDevice(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	rec := newRecordingDaemonClient()
	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	lease := mock_remote_device_svc.NewMockLease(ctrl)
	pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease, nil).AnyTimes()
	lease.EXPECT().Client().Return(protorpctest.WrapConnection(rec)).AnyTimes()
	lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	lease.EXPECT().Release().AnyTimes()

	svc := &chatSvc{}
	svc.setConnPoolForTest(pool)
	installPairedDevice(t, ctrl, 7)
	installExecDaemonRecorder(t, ctrl)

	be := &agent_backend_entity.AgentBackend{
		Type:              string(agent_backend_entity.TypeClaudeCode),
		DeviceFingerprint: testDeviceFingerprint(7),
	}
	_, err := svc.borrowRemoteRuntime(context.Background(), be, 100)
	require.NoError(t, err)
	assert.Equal(t, 1, rec.count(wire.MethodCapabilities), "cold borrow must prefetch capabilities once")

	// Second borrow same device → cache hit, no extra RPC.
	_, err = svc.borrowRemoteRuntime(context.Background(), be, 101)
	require.NoError(t, err)
	assert.Equal(t, 1, rec.count(wire.MethodCapabilities), "cache hit must not re-prefetch")
}

func TestGoal_RemoteReleasesRuntimeAfterOneShotRPC(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	rec := newRecordingDaemonClient()
	rec.expect(wire.MethodCapabilities, func(_, result any) error {
		*(result.(*wire.CapabilitiesResult)) = wire.CapabilitiesResult{
			Capabilities: capability.Capabilities{Set: map[capability.Capability]bool{capability.CapGoal: true}},
		}
		return nil
	})
	rec.expect(wire.MethodSetGoal, func(params, result any) error {
		gp, ok := params.(wire.GoalParams)
		require.True(t, ok, "expected wire.GoalParams, got %T", params)
		assert.Equal(t, execConvID(100), gp.ConversationID)
		assert.Equal(t, "codex-thread-123", gp.ProviderSessionID)
		*(result.(*wire.GoalResult)) = wire.GoalResult{Goal: &agentruntime.Goal{
			ThreadID:  "codex-thread-123",
			Objective: "ship remote goal",
			Status:    "active",
		}}
		return nil
	})

	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	lease := mock_remote_device_svc.NewMockLease(ctrl)
	pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease, nil)
	lease.EXPECT().Client().Return(protorpctest.WrapConnection(rec)).AnyTimes()
	lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	lease.EXPECT().Release().AnyTimes()

	svc := &chatSvc{}
	svc.setConnPoolForTest(pool)
	installPairedDevice(t, ctrl, 7)
	installExecDaemonRecorder(t, ctrl)
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, nil)
	t.Cleanup(restore)

	be := &agent_backend_entity.AgentBackend{
		ID:                12,
		Type:              string(agent_backend_entity.TypeCodex),
		DeviceFingerprint: testDeviceFingerprint(7),
		Status:            1,
	}
	sess := &chat_entity.Session{ID: 100, AgentID: 7, ProviderSessionID: "codex-thread-123"}
	objective := "ship remote goal"
	status := "active"

	g, release, err := svc.goals().SetOnSessionForTest(
		context.Background(), sess, &agent_entity.Agent{ID: 7}, be, nil, goal.Patch{
			Objective: &objective,
			Status:    &status,
		})
	require.NoError(t, err)
	defer release()
	require.NotNil(t, g)
	assert.Equal(t, "ship remote goal", g.Objective)

	release()
	assert.Equal(t, 0, svc.remoteRuntimeCount(7), "one-shot remote goal RPC must release its remote runtime lease")
	assert.Equal(t, 1, rec.count(wire.MethodSetGoal))
}

// TestBorrowRemoteRuntime_InvalidDevice 当 DeviceID 不是规范指纹时立即返回
// AgentBackendInvalidDevice — 不去摸 Pool。
func TestBorrowRemoteRuntime_InvalidDevice(t *testing.T) {
	svc := &chatSvc{}
	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: "not-a-number"}
	_, err := svc.borrowRemoteRuntime(context.Background(), be, 100)
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, code.AgentBackendInvalidDevice, httpErr.Code)
}

// TestBorrowRemoteRuntime_GivenFingerprintDeviceID_ResolvesPairedRowAndBorrows
// R13 认领后 / 对端同步回来的 agentred backend 以规范指纹（sha256:…）作为
// DeviceID。本地派发边界必须把它解析成本机 paired_agentreds 的行 ID 再拨号 —— 旧
// DeviceIDInt 语义只认数值，会让这类「判可用但跑不动」的目标永远报
// AgentBackendInvalidDevice。
func TestBorrowRemoteRuntime_GivenFingerprintDeviceID_ResolvesPairedRowAndBorrows(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	lease := mock_remote_device_svc.NewMockLease(ctrl)
	lease.EXPECT().Client().Return(&noopDaemonClient{}).AnyTimes()
	lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	lease.EXPECT().Release().AnyTimes()
	pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease, nil).AnyTimes()

	rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	rds.EXPECT().List(gomock.Any()).Return([]*remote_device_svc.DeviceView{
		{ID: 7, DaemonFingerprint: "sha256:daemon-x"},
	}, nil).AnyTimes()
	rds.EXPECT().Get(gomock.Any(), int64(7)).
		Return(&remote_device_svc.DeviceView{ID: 7, DaemonFingerprint: "sha256:daemon-x"}, nil).AnyTimes()
	prevSvc := remote_device_svc.Default()
	remote_device_svc.SetDefault(rds)
	t.Cleanup(func() { remote_device_svc.SetDefault(prevSvc) })

	sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
	prevRepo := chat_repo.Session()
	chat_repo.RegisterSession(sessRepo)
	t.Cleanup(func() { chat_repo.RegisterSession(prevRepo) })
	sessRepo.EXPECT().UpdateExecDaemon(gomock.Any(), int64(100), int64(7), "sha256:daemon-x", int64(0)).Return(nil)

	svc := &chatSvc{}
	svc.setConnPoolForTest(pool)

	be := &agent_backend_entity.AgentBackend{
		Type:              string(agent_backend_entity.TypeClaudeCode),
		DeviceFingerprint: "sha256:daemon-x",
	}
	rt, err := svc.borrowRemoteRuntime(context.Background(), be, 100)
	require.NoError(t, err)
	require.NotNil(t, rt)
	assert.Equal(t, 1, svc.remoteRuntimeCount(7),
		"a fingerprint DeviceID must resolve to its local paired row before dialing")
}

// TestBorrowRemoteRuntime_GivenUnpairedFingerprintDeviceID_RejectsWithInvalidDevice
// 指纹在本机配对表里查不到（这台 daemon 没在本机配对）时派发边界报不可派发，绝不
// 猜一个行号去拨号。
func TestBorrowRemoteRuntime_GivenUnpairedFingerprintDeviceID_RejectsWithInvalidDevice(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	pool.EXPECT().Borrow(gomock.Any(), gomock.Any()).Return(nil, errors.New("must not dial")).AnyTimes()

	rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	rds.EXPECT().List(gomock.Any()).Return(nil, nil).AnyTimes()
	prevSvc := remote_device_svc.Default()
	remote_device_svc.SetDefault(rds)
	t.Cleanup(func() { remote_device_svc.SetDefault(prevSvc) })

	svc := &chatSvc{}
	svc.setConnPoolForTest(pool)

	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: "sha256:not-paired-here"}
	_, err := svc.borrowRemoteRuntime(context.Background(), be, 100)
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, code.AgentBackendInvalidDevice, httpErr.Code)
}

// TestBorrowRemoteRuntime_DialFailure 当 Pool.Borrow 失败时返回 RemoteRunnerDialFailed,
// 且不在 cache 留下条目(防止下次 borrow 复用坏 entry)。
func TestBorrowRemoteRuntime_DialFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockPool := mock_remote_device_svc.NewMockConnPool(ctrl)
	mockPool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(nil, errors.New("boom"))

	svc := &chatSvc{}
	svc.setConnPoolForTest(mockPool)
	installPairedDevice(t, ctrl, 7)

	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: testDeviceFingerprint(7)}
	_, err := svc.borrowRemoteRuntime(context.Background(), be, 100)
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, code.RemoteRunnerDialFailed, httpErr.Code)

	assert.Equal(t, 0, svc.remoteRuntimeCount(7))
}

func TestMapTurnError_RemoteProviderMissing(t *testing.T) {
	svc := &chatSvc{}
	err := svc.mapTurnError(context.Background(), nil, &agent_backend_entity.AgentBackend{
		LLMProviderKey: "provider-key-1",
	}, &rpcerror.Error{
		Code:    rpcerror.ErrProviderMissing.Code,
		Message: "LLM provider provider-key-1 not configured",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "远端 agentred 未配置")
	assert.Contains(t, err.Error(), "provider-key-1")
}

// TestSelectRunner_LocalReturnsRegistry verifies local backend → agentruntime.For.
func TestSelectRunner_LocalReturnsRegistry(t *testing.T) {
	svc := &chatSvc{}
	be := &agent_backend_entity.AgentBackend{
		Type:              string(agent_backend_entity.TypeClaudeCode),
		DeviceFingerprint: "", // local
	}
	runner, err := svc.selectRunner(context.Background(), be, 100)
	require.NoError(t, err)
	require.NotNil(t, runner)
	// 是 *remote.Runtime 则说明走错了分支
	_, isRemote := runner.(*remote.Runtime)
	assert.False(t, isRemote, "local backend should not return *remote.Runtime")
}

// TestSelectRunner_RemoteBorrows verifies remote backend → borrowRemoteRuntime cache.
func TestSelectRunner_RemoteBorrows(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	svc := &chatSvc{}
	installMockPool(t, ctrl, svc, 7)

	be := &agent_backend_entity.AgentBackend{
		Type:              string(agent_backend_entity.TypeClaudeCode),
		DeviceFingerprint: testDeviceFingerprint(7),
	}
	runner, err := svc.selectRunner(context.Background(), be, 100)
	require.NoError(t, err)
	_, isRemote := runner.(*remote.Runtime)
	assert.True(t, isRemote)
	assert.Equal(t, 1, svc.remoteRuntimeCount(7))
}

// TestSelectRunner_RemoteIdempotent same sessionID → same instance + no refcount inflation.
func TestSelectRunner_RemoteIdempotent(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	svc := &chatSvc{}
	installMockPool(t, ctrl, svc, 7)

	be := &agent_backend_entity.AgentBackend{
		Type:              string(agent_backend_entity.TypeClaudeCode),
		DeviceFingerprint: testDeviceFingerprint(7),
	}
	r1, err := svc.selectRunner(context.Background(), be, 100)
	require.NoError(t, err)
	r2, err := svc.selectRunner(context.Background(), be, 100) // same sessionID
	require.NoError(t, err)
	assert.Same(t, r1, r2)
	assert.Equal(t, 1, svc.remoteRuntimeCount(7), "same sessionID must not inflate refcount")
}

func TestToolUseToChatBlock_Canonical(t *testing.T) {
	convey.Convey("Edit → Canonical FileEdit", t, func() {
		cb := toolUseToChatBlock("tu-1", "Edit", map[string]any{
			"file_path":  "/x.go",
			"old_string": "a\n",
			"new_string": "b\n",
		})
		convey.So(cb.Canonical, convey.ShouldNotBeNil)
		convey.So(string(cb.Canonical.Kind), convey.ShouldEqual, "file.edit")
		convey.So(cb.Canonical.FileEdit, convey.ShouldNotBeNil)
		convey.So(cb.Canonical.FileEdit.Files[0].Path, convey.ShouldEqual, "/x.go")
	})

	convey.Convey("file_change → Canonical FileEdit", t, func() {
		cb := toolUseToChatBlock("tu-2", "file_change", map[string]any{
			"changes": []any{
				map[string]any{"path": "a.go", "kind": "update", "diff": "@@ -1,1 +1,1 @@\n-a\n+A\n"},
			},
		})
		convey.So(cb.Canonical, convey.ShouldNotBeNil)
		convey.So(string(cb.Canonical.Kind), convey.ShouldEqual, "file.edit")
		convey.So(cb.Canonical.FileEdit, convey.ShouldNotBeNil)
	})

	convey.Convey("Write → Canonical FileWrite", t, func() {
		cb := toolUseToChatBlock("tu-3", "Write", map[string]any{
			"file_path": "/x.go",
			"content":   "hello\n",
		})
		convey.So(cb.Canonical, convey.ShouldNotBeNil)
		convey.So(string(cb.Canonical.Kind), convey.ShouldEqual, "file.write")
		convey.So(cb.Canonical.FileWrite, convey.ShouldNotBeNil)
		convey.So(cb.Canonical.FileWrite.Path, convey.ShouldEqual, "/x.go")
	})

	convey.Convey("Bash → Canonical=nil(走 RawToolCard 兜底)", t, func() {
		cb := toolUseToChatBlock("tu-4", "Bash", map[string]any{"command": "ls"})
		convey.So(cb.Canonical, convey.ShouldBeNil)
	})
}

// TestEventShowsProgressAfterError_SubagentModel 覆盖 wrap-up 复审第二轮 Finding 2:
// eventShowsProgressAfterError 是「错误后收到哪些事件才清除 streamStopErr」的跨切面
// 注册表,agentruntime.SubagentModel 漏登记 —— 瞬时 API 错误置上 streamStopErr 后,
// 子代理下一帧内部 assistant 到达时该事件会在 runTurn 循环里被 continue 掉(既不清
// 错误也不应用),要等随后的 ToolCall/SubagentProgress 才能自愈,凭空多一帧延迟。
func TestEventShowsProgressAfterError_SubagentModel(t *testing.T) {
	convey.Convey("SubagentModel 事件应被视为错误后的进度,从而清除 streamStopErr", t, func() {
		ev := agentruntime.SubagentModel{ToolCallID: "task-1", Model: "claude-haiku-4-5-20251001"}
		convey.So(eventShowsProgressAfterError(ev), convey.ShouldBeTrue)
	})
}

// SelfFingerprint 满足 client.ProtobufConnection:这个假连接从没握过手,本端指纹为空。
func (c *noopDaemonClient) SelfFingerprint() string { return "" }

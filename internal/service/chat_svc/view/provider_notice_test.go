package view_test

import (
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/view"
)

// TestModelDisplayName 钉死切换 notice 里的模型名：没填展示名的模型回落 ModelID ——
// 空串会让前端退到稳定 model_key，那是一串 UUID。
func TestModelDisplayName(t *testing.T) {
	assert.Equal(t, "GLM 5.2", view.ModelDisplayName(&llm_provider_model_entity.LLMProviderModel{
		ModelKey: "mk-1", ModelID: "glm-5.2", Name: "GLM 5.2",
	}))
	assert.Equal(t, "glm-5.2", view.ModelDisplayName(&llm_provider_model_entity.LLMProviderModel{
		ModelKey: "mk-1", ModelID: "glm-5.2",
	}), "没填展示名时回落 ModelID，而不是空串")
	assert.Equal(t, "", view.ModelDisplayName(nil))
}

// TestEncodeReasoningEffortSwitch 钉死会话级思考力度 notice 的负载形态（spec
// 2026-09-01 决策 7）：与 ModelTarget 切换同一条通道的结构化 JSON，靠 kind 判定，
// 档位随负载走；空档 = 改回跟随后端配置，字段省略而 kind 仍在。
func TestEncodeReasoningEffortSwitch(t *testing.T) {
	assert.Equal(t, `{"kind":"reasoning_effort","reasoningEffort":"max"}`,
		view.EncodeReasoningEffortSwitch("max"))
	assert.Equal(t, `{"kind":"reasoning_effort"}`, view.EncodeReasoningEffortSwitch(""))

	p, ok := view.DecodeProviderNotice(view.EncodeReasoningEffortSwitch(""))
	require.True(t, ok, "空档同样是有效负载 —— 判据是 kind 而不是字段非空")
	assert.Equal(t, view.NoticeKindReasoningEffort, p.Kind)
	assert.Empty(t, p.ReasoningEffort)
}

// TestNoticeOnlyMessage_SkipsReasoningEffortSwitch：思考力度 notice 与供应商切换 notice
// 一样是独立落库的旁白行，允许发生在轮中（NextSeq 把它排在在跑的那条 assistant 之后），
// 所以「末条 assistant = 在跑的那一轮」的推导必须跳过它 —— 否则轮中切一次档位，在跑的
// 那一轮就会被这条旁白行顶掉。
func TestNoticeOnlyMessage_SkipsReasoningEffortSwitch(t *testing.T) {
	m := &chat_entity.Message{Role: "assistant"}
	require.NoError(t, m.SetBlocks([]blocks.ContentBlock{blocks.NoticeBlock{
		Level: "info", Text: view.EncodeReasoningEffortSwitch("high"),
	}}))
	assert.True(t, view.NoticeOnlyMessage(m))

	running := &chat_entity.Message{Role: "assistant"}
	require.NoError(t, running.SetBlocks([]blocks.ContentBlock{blocks.TextBlock{Text: "answering"}}))
	assert.Equal(t, 0, view.LastTurnAssistantIndex([]*chat_entity.Message{running, m}),
		"在跑的那一轮不得被轮中追加的力度 notice 顶掉")
}

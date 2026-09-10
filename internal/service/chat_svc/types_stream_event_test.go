package chat_svc_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// TestStreamSubagentActivityStarted_KindValue 验证常量值与协议字符串一致。
func TestStreamSubagentActivityStarted_KindValue(t *testing.T) {
	assert.Equal(t, chat_svc.ChatStreamEventKind("subagent_activity_started"),
		chat_svc.StreamSubagentActivityStarted,
		"StreamSubagentActivityStarted 常量应等于 \"subagent_activity_started\"")
}

// TestStreamSubagentActivityStarted_EventMarshal 验证 ChatStreamEvent 可 JSON 序列化
// LaunchMessageID 和 ToolCallID 字段，且字段名与协议约定一致。
func TestStreamSubagentActivityStarted_EventMarshal(t *testing.T) {
	evt := chat_svc.ChatStreamEvent{
		Kind:            chat_svc.StreamSubagentActivityStarted,
		LaunchMessageID: 42,
		ToolCallID:      "toolu_abc123",
		Stream:          "chat:event:1:99",
	}
	data, err := json.Marshal(evt)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))

	assert.Equal(t, "subagent_activity_started", m["kind"])
	assert.EqualValues(t, 42, m["launchMessageId"], "LaunchMessageID 应序列化为 launchMessageId")
	assert.Equal(t, "toolu_abc123", m["toolUseId"])
	assert.Equal(t, "chat:event:1:99", m["stream"])
}

// TestStreamSubagentModel_EventMarshal 钉住 wire 形状:{kind, toolUseId, model}三个
// 字段,不夹带 subagent 全量快照(R4)。
func TestStreamSubagentModel_EventMarshal(t *testing.T) {
	evt := chat_svc.ChatStreamEvent{
		Kind:       chat_svc.StreamSubagentModel,
		ToolCallID: "toolu_abc123",
		Model:      "claude-haiku-4-5-20251001",
	}
	data, err := json.Marshal(evt)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))

	assert.Equal(t, "subagent_model", m["kind"])
	assert.Equal(t, "toolu_abc123", m["toolUseId"])
	assert.Equal(t, "claude-haiku-4-5-20251001", m["model"])
	_, hasSubagent := m["subagent"]
	assert.False(t, hasSubagent, "subagent_model 不应携带 subagent 全量快照字段")
}

package protowire_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/eventkind"
)

// 判别值(text_delta / tool_use_start / usage …)现在写在 .proto 的
// (agentre.wire.event_kind) 字段选项上,消费方从 descriptor 读它。本仓不消费那一格
// —— 桌面端走的是 agentruntime 自己的 JSON —— 但本仓是**标注它的人**,所以标错了得
// 在这里红,而不是等 agentre-server 上线后在页面上表现为「那一类卡片整块不渲染」。
//
// 断言把三套词表串成一条:agentruntime 的 Go 事件类型 → protowire 落成的 oneof 分支
// → 该分支在 schema 上标注的判别值,必须等于这个事件自己序列化出来的 kind。
func TestDeclaredEventKindMatchesTheRuntimeVocabulary(t *testing.T) {
	t.Parallel()

	events := []agentruntime.Event{
		agentruntime.TextDelta{}, agentruntime.ThinkingDelta{}, agentruntime.OutputActivity{},
		agentruntime.ToolCall{}, agentruntime.ToolResult{}, agentruntime.SteerConsumed{},
		agentruntime.UserAskRequest{}, agentruntime.UserAskResolved{},
		agentruntime.ToolPermissionRequest{}, agentruntime.ToolPermissionResolved{},
		agentruntime.ExecApprovalRequested{}, agentruntime.ExecApprovalResolved{},
		agentruntime.PermissionModeChanged{}, agentruntime.SubagentStarted{},
		agentruntime.SubagentProgress{}, agentruntime.SubagentDone{}, agentruntime.SubagentModel{},
		agentruntime.Retry{}, agentruntime.UsageUpdate{}, agentruntime.ContextWindowUpdated{},
		agentruntime.CompactBoundary{}, agentruntime.RuntimeStatus{}, agentruntime.ErrorEvent{},
		agentruntime.Done{}, agentruntime.UserMessageEvent{}, agentruntime.PlanUpdated{},
		agentruntime.UnrecognizedBlock{},
	}

	for _, event := range events {
		t.Run(runtimeKind(t, event), func(t *testing.T) {
			encoded, err := protowire.MarshalEventNotification("c", 1, event, false)
			require.NoError(t, err)

			var frame agentrewire.RpcFrame
			require.NoError(t, proto.Unmarshal(encoded, &frame))
			declared, payload, ok := eventkind.Of(frame.GetNotification().GetRuntimeEvent())

			require.Truef(t, ok, "%T 落成的 oneof 分支没在 .proto 上标注判别值", event)
			require.NotNil(t, payload)
			require.Equal(t, runtimeKind(t, event), declared,
				"schema 上标注的判别值与 agentruntime 自己序列化出来的 kind 不一致")
		})
	}
}

// runtimeKind 取一个事件序列化出来的 kind —— 桌面端这一侧的判别值真理源。
func runtimeKind(t *testing.T, event agentruntime.Event) string {
	t.Helper()
	encoded, err := json.Marshal(event)
	require.NoError(t, err)
	var head struct {
		Kind string `json:"kind"`
	}
	require.NoError(t, json.Unmarshal(encoded, &head))
	require.NotEmpty(t, head.Kind)
	return head.Kind
}

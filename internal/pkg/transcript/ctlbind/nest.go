package ctlbind

import (
	"fmt"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// NestChildEvent 把子会话的一条运行时事件转成父转录里挂在 shell 调用 parentToolCallID
// 下的嵌套事件。只转子会话自己的外层 ToolCall / ToolResult(卡片 STEPS 的一行);子会话
// 内部再嵌套的调用、文本、用量等其它事件返回 false,由调用方另行处理。
//
// 嵌套 ID 用 NestedToolCallID 由 (父调用, 子 ID) 确定地派生:同一条子调用 / 结果投递
// 多少次都落在同一个 ID 上,父转录的 handler 按 ID 去重;不同 shell 调用、或与父轮自己
// 的工具 ID(Codex 的 item ID 跨会话会重复)都不会撞。
func NestChildEvent(parentToolCallID string, ev agentruntime.Event) (agentruntime.Event, bool) {
	if parentToolCallID == "" {
		return nil, false
	}
	switch e := ev.(type) {
	case agentruntime.ToolCall:
		if e.ParentToolCallID != "" {
			return nil, false
		}
		return agentruntime.ToolCall{
			ID:               NestedToolCallID(parentToolCallID, e.ID),
			Name:             e.Name,
			Input:            e.Input,
			Canonical:        e.Canonical,
			ParentToolCallID: parentToolCallID,
		}, true
	case agentruntime.ToolResult:
		if e.ParentToolCallID != "" {
			return nil, false
		}
		return agentruntime.ToolResult{
			ToolCallID:       NestedToolCallID(parentToolCallID, e.ToolCallID),
			Content:          e.Content,
			IsError:          e.IsError,
			ParentToolCallID: parentToolCallID,
			Meta:             e.Meta,
		}, true
	}
	return nil, false
}

// NestedToolCallID 是子会话工具调用 innerID 镜像到父 shell 调用 parentToolCallID 下的
// 嵌套 ID。父 ID 带长度前缀,拼接不会产生歧义(同 Pi subagent tracker 的做法)。
func NestedToolCallID(parentToolCallID, innerID string) string {
	return fmt.Sprintf("agrctl:%d:%s:%s", len(parentToolCallID), parentToolCallID, innerID)
}

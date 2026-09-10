package builtin

import (
	"encoding/json"
	"strings"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// translate 把单个 cago agent.Event 翻译成 0/1 个新 sealed agentruntime.Event。
//
// 翻译是无状态、纯函数 —— 一帧 cago event 对应一个新 Event(SteerConsumed 单独
// 用单元素 Steers slice,Run() 负责把同一安全点连续到达的多帧合并以保持与现有
// 顶层 builtin.go (lines 230-272 flushSteers 逻辑) 等价的 wire 行为)。
//
// EventTurnEnd / EventDone / EventCancelled / EventCompacted / EventToolDelta /
// EventMessageEnd / EventRetry 当前都不翻译:
//   - TurnEnd / Done:由 Run() 把 ev.Usage / StopErr 写回 *RunResult,不下发独立事件。
//   - Retry:builtin 不在 chat_svc 透传重试(spec §3 没要求;cago retry 仍生效,
//     UI 不显示)。
//   - 其它细粒度:builtin 历史上也不下发,保持等价。
func translate(ev agent.Event) []agentruntime.Event {
	switch ev.Kind {
	case agent.EventTextDelta:
		return []agentruntime.Event{agentruntime.TextDelta{Text: ev.Delta}}
	case agent.EventThinkingDelta:
		return []agentruntime.Event{agentruntime.ThinkingDelta{Text: ev.Delta}}
	case agent.EventPreToolUse:
		if ev.Tool == nil {
			return nil
		}
		return []agentruntime.Event{agentruntime.ToolCall{
			ID:    ev.Tool.ToolUseID,
			Name:  ev.Tool.Name,
			Input: marshalToolInput(ev.Tool.Input),
		}}
	case agent.EventPostToolUse:
		if ev.Tool == nil || ev.Tool.Output == nil {
			return nil
		}
		return []agentruntime.Event{agentruntime.ToolResult{
			ToolCallID: ev.Tool.ToolUseID,
			Content:    flattenToolResultContent(ev.Tool.Output),
			IsError:    ev.Tool.Output.IsError,
		}}
	case agent.EventSteerConsumed:
		return []agentruntime.Event{agentruntime.SteerConsumed{
			Steers: []agentruntime.ConsumedSteer{{QueuedID: ev.SteerID, Text: ev.Delta}},
		}}
	case agent.EventError:
		if ev.Error == nil {
			return nil
		}
		return []agentruntime.Event{agentruntime.ErrorEvent{Err: ev.Error}}
	}
	return nil
}

// marshalToolInput 把 ToolUseBlock.Input (map[string]any) 编成 JSON 字节。
// 错误或空 map 时返回 nil(让 chat_svc 端 unmarshal 拿到 nil map,UI 仅展示 ID + Name)。
func marshalToolInput(input map[string]any) []byte {
	if len(input) == 0 {
		return nil
	}
	b, err := json.Marshal(input)
	if err != nil {
		return nil
	}
	return b
}

// flattenToolResultContent 拼接 ToolResultBlock.Content 里所有 TextBlock 文本。
// 非文本子块(image / ref / notice)当前丢弃。
func flattenToolResultContent(b *blocks.ToolResultBlock) string {
	if b == nil || len(b.Content) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, c := range b.Content {
		switch t := c.(type) {
		case blocks.TextBlock:
			sb.WriteString(t.Text)
		case *blocks.TextBlock:
			sb.WriteString(t.Text)
		}
	}
	return sb.String()
}

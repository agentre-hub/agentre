package piagent

import (
	"encoding/json"

	"github.com/cago-frame/agents/provider"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	pkgpi "github.com/agentre-hub/agentre/pkg/piagent"
)

func translate(ev pkgpi.Event) (events []agentruntime.Event, usage *provider.Usage, stopErr error) {
	switch ev.Kind {
	case pkgpi.EventTextDelta:
		if ev.Text != "" {
			events = append(events, agentruntime.TextDelta{Text: ev.Text})
		}
	case pkgpi.EventThinkingDelta:
		if ev.Text != "" {
			events = append(events, agentruntime.ThinkingDelta{Text: ev.Text})
		}
	case pkgpi.EventPreToolUse:
		events = append(events, agentruntime.ToolCall{
			ID:        ev.Tool.ID,
			Name:      ev.Tool.Name,
			Input:     ev.Tool.Input,
			Canonical: recognizeCanonical(ev.Tool.Name, ev.Tool.Input),
		})
	case pkgpi.EventPostToolUse:
		events = append(events, agentruntime.ToolResult{ToolCallID: ev.Tool.ID, Content: ev.Tool.Content, IsError: ev.Tool.IsError})
	case pkgpi.EventUsage:
		u := ev.Usage
		usage = &u
		events = append(events, agentruntime.UsageUpdate{
			Usage:            usage,
			TotalInputTokens: u.PromptTokens + u.CachedTokens + u.CacheCreationTokens,
			ContextWindow:    ev.ContextWindow,
		})
	case pkgpi.EventContextWindow:
		if ev.ContextWindow > 0 {
			events = append(events, agentruntime.ContextWindowUpdated{Tokens: ev.ContextWindow})
		}
	case pkgpi.EventCompactBoundary:
		events = append(events, agentruntime.CompactBoundary{Trigger: "manual"})
	case pkgpi.EventRuntimeStatus:
		events = append(events, agentruntime.RuntimeStatus{Status: ev.Text})
	case pkgpi.EventError:
		stopErr = ev.Err
	case pkgpi.EventDone:
		events = append(events, agentruntime.Done{})
	}
	return events, usage, stopErr
}

// piCanonicalToolNames 是 pi 内置的文件变更工具白名单。Pi 的工具名全小写且与
// claudecode / codex 不重名(read/bash/edit/write/grep/find/ls),这里只认会改文件
// 的两个;其余内置工具与注入的 MCP 工具一律走 raw 工具卡。
//
// 刻意不整表交给 canonical.FromToolUse:Pi extension subagent 必须先经过 drain
// boundary 的名称门槛、invocation classifier 与 stateful envelope tracker；在这个纯
// canonical helper 里按 task/agent 形状直接识别，会绕过协议校验并误分类同名 MCP 工具。
var piCanonicalToolNames = map[string]bool{"edit": true, "write": true}

// recognizeCanonical 按工具名 + raw input JSON 识别 pi 文件工具的 canonical 形状。
// 解析失败 / 工具不在白名单 → nil,走 raw tool_use 路径(前端通用 ToolInvocationCard)。
//
// 识别本身复用 canonical.FromToolUse —— live emit 路径(这里)和重放路径
// (chat_svc 从持久化 tool_use 重建)共用同一份实现,避免两边漂移。
func recognizeCanonical(name string, rawInput []byte) canonical.CanonicalTool {
	if len(rawInput) == 0 || !piCanonicalToolNames[name] {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(rawInput, &m); err != nil {
		return nil
	}
	if c, ok := canonical.FromToolUse(name, m); ok {
		return c
	}
	return nil
}

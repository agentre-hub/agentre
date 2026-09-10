// Package blocks: NestedToolUseBlock / NestedToolResultBlock 与 cago.ToolUseBlock 形态相似,
// 但 Audience=ToUI(防 subagent 内部工具流泄漏给外层 LLM)，并以 ParentToolCallID +
// 可选 SubagentRunID 保留父调用和 normalized run 分组。
package blocks

import cagoblocks "github.com/cago-frame/agents/agent/blocks"

type NestedToolUseBlock struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Input            map[string]any `json:"input,omitempty"`
	ParentToolCallID string         `json:"parent_tool_call_id"`
	SubagentRunID    string         `json:"subagent_run_id,omitempty"`
}

func (NestedToolUseBlock) Type() string                      { return "nested_tool_use" }
func (NestedToolUseBlock) Audience() cagoblocks.AudienceMask { return cagoblocks.ToUI }

type NestedToolResultBlock struct {
	ToolCallID       string `json:"tool_call_id"`
	Content          string `json:"content"`
	IsError          bool   `json:"is_error,omitempty"`
	ParentToolCallID string `json:"parent_tool_call_id"`
	SubagentRunID    string `json:"subagent_run_id,omitempty"`
}

func (NestedToolResultBlock) Type() string                      { return "nested_tool_result" }
func (NestedToolResultBlock) Audience() cagoblocks.AudienceMask { return cagoblocks.ToUI }

func init() {
	cagoblocks.RegisterFactory[NestedToolUseBlock]()
	cagoblocks.RegisterFactory[NestedToolResultBlock]()
}

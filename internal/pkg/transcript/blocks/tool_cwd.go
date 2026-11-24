package blocks

import cagoblocks "github.com/cago-frame/agents/agent/blocks"

// ToolCwdBlock 记下一次外层工具调用的「AI 当时所在目录」(spec
// 2026-11-24-chat-right-toolwindows 决策 17):Cwd 是产生 tool_use 时的目录,
// ResultCwd 是产生 tool_result 时的目录,空 = 执行侧没报。
//
// 它是挂在调用旁的独立块,而不是 tool_use 块上的一个字段:外层 tool_use 是外部包的
// cagoblocks.ToolUseBlock,加不了字段。形态照 SubagentStateBlock —— 读回时按
// ToolCallID 预扫、合进对应工具卡,自己不成卡。Audience=ToUI:它不是对话内容,
// 不喂给 LLM。只有执行侧确实报了目录时才落这块,没有目录的轮次块序列与从前逐块相同。
type ToolCwdBlock struct {
	ToolCallID string `json:"tool_call_id"`
	Cwd        string `json:"cwd,omitempty"`
	ResultCwd  string `json:"result_cwd,omitempty"`
}

func (ToolCwdBlock) Type() string                      { return "tool_cwd" }
func (ToolCwdBlock) Audience() cagoblocks.AudienceMask { return cagoblocks.ToUI }

func init() { cagoblocks.RegisterFactory[ToolCwdBlock]() }

package blocks

import cagoblocks "github.com/cago-frame/agents/agent/blocks"

// ToolApprovalBlock agent 内置工具(org / hook 等)写操作的服务端审批卡。
// ToolKey 标识来源工具(供前端选标题/文案与后处理);其余字段为各工具共用。
// Status: pending → approved | denied | expired(超时 / turn 中止 / app 重启悬空)。
// Result 仅 approved 后有值(执行结果或业务错误摘要)。
type ToolApprovalBlock struct {
	ToolKey   string         `json:"tool_key"`
	RequestID string         `json:"request_id"`
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input,omitempty"`
	Status    string         `json:"status"`
	Result    string         `json:"result,omitempty"`
}

func (ToolApprovalBlock) Type() string                      { return "tool_approval" }
func (ToolApprovalBlock) Audience() cagoblocks.AudienceMask { return cagoblocks.ToUI }

func init() { cagoblocks.RegisterFactory[ToolApprovalBlock]() }

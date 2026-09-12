package blocks

import cagoblocks "github.com/cago-frame/agents/agent/blocks"

// ToolPermissionBlock 持久化 can_use_tool 审批全态:工具名 + input + 决策 + 拒绝原因。
//
// 旧名 ToolPermissionRequestBlock,旧 Type "tool_permission_request" 改为 "tool_permission"。
// ToolCallID 关联到同 turn 内 raw ToolUseBlock.ID;DenyReason 仅 Resolved=true && !Allowed 时有效。
type ToolPermissionBlock struct {
	RequestID   string         `json:"request_id"`
	ToolCallID  string         `json:"tool_call_id,omitempty"`
	ToolName    string         `json:"tool_name"`
	ToolInput   map[string]any `json:"tool_input,omitempty"`
	Resolved    bool           `json:"resolved,omitempty"`
	Allowed     bool           `json:"allowed,omitempty"`
	AlwaysAllow bool           `json:"always_allow,omitempty"`
	DenyReason  string         `json:"deny_reason,omitempty"`
}

func (ToolPermissionBlock) Type() string                      { return "tool_permission" }
func (ToolPermissionBlock) Audience() cagoblocks.AudienceMask { return cagoblocks.ToUI }

func init() { cagoblocks.RegisterFactory[ToolPermissionBlock]() }

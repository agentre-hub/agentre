package blocks

import cagoblocks "github.com/cago-frame/agents/agent/blocks"

// ExecApprovalBlock persists the presentation-safe OpenClaw exec approval
// lifecycle. It intentionally excludes environment variables and systemRunPlan.
// Status is pending, resolved, or expired; approval resolution does not imply
// that the authorized command has finished.
type ExecApprovalBlock struct {
	ID               string   `json:"id"`
	CommandText      string   `json:"command_text"`
	CommandPreview   string   `json:"command_preview,omitempty"`
	AllowedDecisions []string `json:"allowed_decisions,omitempty"`
	Host             string   `json:"host,omitempty"`
	NodeID           string   `json:"node_id,omitempty"`
	AgentID          string   `json:"agent_id,omitempty"`
	Status           string   `json:"status"`
	Decision         string   `json:"decision,omitempty"`
	ResolvedBy       string   `json:"resolved_by,omitempty"`
	CreatedAtMs      int64    `json:"created_at_ms,omitempty"`
	ExpiresAtMs      int64    `json:"expires_at_ms,omitempty"`
	ResolvedAtMs     int64    `json:"resolved_at_ms,omitempty"`
}

func (ExecApprovalBlock) Type() string                      { return "exec_approval" }
func (ExecApprovalBlock) Audience() cagoblocks.AudienceMask { return cagoblocks.ToUI }

func init() { cagoblocks.RegisterFactory[ExecApprovalBlock]() }

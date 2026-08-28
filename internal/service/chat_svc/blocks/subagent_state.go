package blocks

import (
	cagoblocks "github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// SubagentStateBlock 持久化 subagent 累计态。
// ParentToolCallID 对应 Task 工具的 ToolCallID;NestedToolCallIDs 反向索引该 subagent
// 派遣下属于它的所有 NestedToolUseBlock.ID,replay 时按这个数组把内层卡片归集到父
// AgentSpawnCard。Mode/Runs 是 normalized Pi 全量快照：事件携带 Runs 时整片替换，
// 省略时保留旧值，兼容 legacy Claude/local_bash 数据。
type SubagentStateBlock struct {
	ParentToolCallID  string                     `json:"parent_tool_call_id"`
	TaskID            string                     `json:"task_id,omitempty"`
	Kind              string                     `json:"kind,omitempty"`        // local_bash | local_agent
	Description       string                     `json:"description,omitempty"` // 任务名（task_started.description）
	TotalTokens       int                        `json:"total_tokens,omitempty"`
	DurationMs        int                        `json:"duration_ms,omitempty"`
	Status            string                     `json:"status"`            // waiting | running | completed | failed | canceled | skipped | unknown
	Summary           string                     `json:"summary,omitempty"` // CLI task_notification.summary（如退出码说明）
	Mode              string                     `json:"mode,omitempty"`
	Runs              []agentruntime.SubagentRun `json:"runs,omitempty"`
	LastToolName      string                     `json:"last_tool_name,omitempty"`
	ToolUses          int                        `json:"tool_uses,omitempty"`
	NestedToolCallIDs []string                   `json:"nested_tool_call_ids,omitempty"`
	// Model 是子代理内部 assistant 帧解析出的实际模型(R2 覆盖派遣瞬间的入参别名)。
	// first-wins(R3):一经记录不再改写,由 SubagentModelHandler 负责。随本块一起
	// 落 blocks_json,replay 时随 subagentStateToChatBlockSubagent 一并投影(R6)。
	Model string `json:"model,omitempty"`
}

func (SubagentStateBlock) Type() string                      { return "subagent_state" }
func (SubagentStateBlock) Audience() cagoblocks.AudienceMask { return cagoblocks.ToUI }

func init() { cagoblocks.RegisterFactory[SubagentStateBlock]() }

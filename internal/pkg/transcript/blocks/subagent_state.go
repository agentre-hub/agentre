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
	Kind              string                     `json:"kind,omitempty"`        // local_bash | local_agent | agrctl_send
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
	// Resumes 记录该 task 每一次「中断后被重开」时的既有终态,按发生顺序排列。
	// CLI 恢复一个子代理(SendMessage)时用**同一个 task_id**、**新的 tool_use_id**
	// 重发 task_started;overlay 按 TaskID 归一到原卡后,原来那次的终态会被新一段
	// 覆盖,这个数组是它唯一的留存处 —— 卡片据此显示「中断 N 次后恢复」,而不是
	// 把失败发生过这件事抹掉。长度即恢复次数。
	Resumes []SubagentInterruption `json:"resumes,omitempty"`
	// Model 是子代理内部 assistant 帧解析出的实际模型(R2 覆盖派遣瞬间的入参别名)。
	// first-wins(R3):一经记录不再改写,由 SubagentModelHandler 负责。随本块一起
	// 落 blocks_json,replay 时随 subagentStateToChatBlockSubagent 一并投影(R6)。
	Model string `json:"model,omitempty"`

	// Origin 非空(agentruntime.SubagentOriginAgrctl)表示这张卡是把 `agrctl send --wait`
	// 的子会话绑到父轮那条 shell 调用上得来的,ParentToolCallID 就是那条 shell 调用。
	// 写入分工:嵌套步骤、模型、用量、工具数、FinalText 归注入方;前台卡(Kind =
	// agentruntime.SubagentKindAgrctl)不再接受 CLI 为这条调用发的任何 local_bash 帧;
	// 后台卡(Kind = local_bash)的状态 / task_id / summary 仍归 CLI。
	Origin string `json:"origin,omitempty"`
	// ChildSessionID 是 send 建出的子会话 ID(卡片的「打开会话」入口)。
	ChildSessionID int64 `json:"child_session_id,omitempty"`
	// Isolated 子会话是 `--isolated` 一次性会话,不在侧栏里,卡片不显示跳转入口。
	Isolated bool `json:"isolated,omitempty"`
	// FinalText 是子轮的最终文本(父 agent 从 stdout 拿到的那段)。单独存放,不写
	// Summary —— 后台 Bash 的 CLI summary 是退出码文本,两者互不覆盖。
	FinalText string `json:"final_text,omitempty"`
}

// SubagentInterruption 是一次中断的证据:被覆盖前那一段的终态与它的结论文本。
type SubagentInterruption struct {
	Status  string `json:"status"`
	Summary string `json:"summary,omitempty"`
}

func (SubagentStateBlock) Type() string                      { return "subagent_state" }
func (SubagentStateBlock) Audience() cagoblocks.AudienceMask { return cagoblocks.ToUI }

func init() { cagoblocks.RegisterFactory[SubagentStateBlock]() }

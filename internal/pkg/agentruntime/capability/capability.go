// Package capability 描述 runtime 支持哪些可选能力,前后端用 bool 集合做 UI gating,
// PermissionModeMeta 用结构化字段消除"每个 backend 各自维护 mode 白名单"的硬编码。
package capability

type Capability string

const (
	CapSteer               Capability = "steer"
	CapCancelSteer         Capability = "cancel_steer"
	CapDrainSteer          Capability = "drain_steer"
	CapAbort               Capability = "abort"
	CapStopBackgroundTask  Capability = "stop_background_task"
	CapImageInput          Capability = "image_input"
	CapSetPermission       Capability = "set_permission_mode"
	CapAnswerUserAsk       Capability = "answer_user_ask"
	CapToolPermission      Capability = "tool_permission_gate"
	CapExecApproval        Capability = "exec_approval"
	CapForkSession         Capability = "fork_session"
	CapReportContextWindow Capability = "report_context_window"
	CapCompact             Capability = "compact"
	CapGoal                Capability = "goal"
	CapMCPTools            Capability = "mcp_tools" // 该 runtime 接受 RunRequest.MCPServers, 可带注入的 MCP tool 启动(org/subagent/hook 工具注入的消费者)
	// CapAutonomousTurn 标记 backend 会「自发产生 turn」—— 当前仅 claudecode:CLI 在
	// run_in_background Bash 任务完成后自主注入 <task-notification> 并跑完整一轮。
	// 实现 AutonomousTurnSource 接口;chat_svc 据此每会话起 watcher 落纯 assistant 轮。
	CapAutonomousTurn Capability = "autonomous_turn"
	// CapSkills 标记 runtime 接受 RunRequest.EnabledPlugins,可按 agent 注入技能包
	// 开关(claudecode 经 --settings,codex 经 --config plugins.*.enabled)。
	CapSkills Capability = "skills"
	// CapReasoningEffort 标记 runtime 会把 Backend.ReasoningEffort 真的下发给它的
	// 引擎(claude 的 --effort / codex 的 model_reasoning_effort / pi 的 --thinking /
	// builtin 的模型参数)。composer 的会话级思考力度选择器据此渲染;为假的后端
	// (openclaw)整颗控件不渲染,而不是置灰(spec 2026-09-01 决策 6)。
	CapReasoningEffort Capability = "reasoning_effort"
)

// Capabilities 一个 runtime 的能力矩阵 + permission mode 元数据。
//
// Set bool 与"该 runtime 是否实现对应控制接口"必须一致(capability matrix 测试强制)。
type Capabilities struct {
	Set                map[Capability]bool
	PermissionModeMeta PermissionModeMeta
}

func (c Capabilities) Has(key Capability) bool {
	if c.Set == nil {
		return false
	}
	return c.Set[key]
}

// PermissionModeMeta 把"哪些 mode 合法 / 默认是哪个 / 能否运行时切换"结构化。
// 替代当前散落在 chat_svc 的 isKnownPermissionMode / normalizeStoredPermissionMode /
// validateRequestedPermissionMode 等硬编码 switch。
type PermissionModeMeta struct {
	AllowedModes         []string // 例 claudecode {default,acceptEdits,plan,bypassPermissions}
	DefaultMode          string   // 例 claudecode "acceptEdits"
	SwitchableDuringTurn bool     // codex=false; claudecode=true
	Order                []string // UI pill 循环顺序(空 = 同 AllowedModes 顺序)
	// LaunchDefaultMode 是 spawn runtime 时 chat_svc 在用户/backend 都没显式选 mode
	// 的兜底落库值。与 DefaultMode 的差异:DefaultMode 是 UI 展示/计算用的"该 runtime
	// 默认 mode 名";LaunchDefaultMode 是 wire 层语义:
	//   - claudecode "" — 不附 --permission-mode flag,让 pkg/claudecode args.go 兜底
	//   - codex "default" — 协议要求每次 launch 显式 collaboration mode
	// 历史 chat_svc.createPermissionMode 用 backendType switch 实现这一差异,挪到 meta
	// 之后 chat_svc 不再需要按 type 分支。
	LaunchDefaultMode string
}

package claudecode

import (
	"strconv"
	"strings"
)

// cliFlag 是 Claude Code CLI 的 argv flag 字面量。集中放这里逐字对齐 CLI 协议。
type cliFlag string

const (
	flagOutputFormat       cliFlag = "--output-format"
	flagInputFormat        cliFlag = "--input-format"
	flagVerbose            cliFlag = "--verbose"
	flagIncludePartialMsgs cliFlag = "--include-partial-messages"
	flagModel              cliFlag = "--model"
	flagResume             cliFlag = "--resume"
	flagAppendSystemPrompt cliFlag = "--append-system-prompt"
	flagPermissionMode     cliFlag = "--permission-mode"
	flagAllowedTools       cliFlag = "--allowedTools"
	flagDisallowedTools    cliFlag = "--disallowedTools"
	flagMaxTurns           cliFlag = "--max-turns"
	flagSettings           cliFlag = "--settings"
	flagMcpConfig          cliFlag = "--mcp-config"
	flagForkSession        cliFlag = "--fork-session"
	flagResumeSessionAt    cliFlag = "--resume-session-at"
	flagSessionID          cliFlag = "--session-id"
	flagEffort             cliFlag = "--effort"
	// flagPermissionPromptTool 启用 stdio control protocol：CLI 在每次工具调用前
	// 发一帧 control_request{subtype:"can_use_tool"}，等 host 写一帧 control_response
	// 才继续推进。host 用 Session.RespondToControl 配对回写。
	flagPermissionPromptTool cliFlag = "--permission-prompt-tool"
)

const formatStreamJSON = "stream-json"

// runSpec 是 buildArgs 的入参；纯结构体，不带方法，方便测试构造。
type runSpec struct {
	model           string
	systemPrompt    string
	permissionMode  string // 空 = acceptEdits
	allowedTools    []string
	disallowedTools []string
	maxTurns        int
	settings        string
	// mcpConfig 非空 = 下发 --mcp-config <json-or-file>。claude CLI 原生兼容
	// JSON 串或文件路径，注入额外 MCP tool server（如编排的 dispatch）。
	mcpConfig           string
	resumeID            string
	resumeSessionAtUUID string
	forkSession         bool
	sessionID           string
	effort              string // claude --effort <level>；空 = 不下发，走 CLI 默认。
	// permissionPromptTool 非空 → 写入 --permission-prompt-tool 实参。固定值
	// "stdio" 把许可决策走 stdio control protocol（hapi 项目已验证机制）。
	permissionPromptTool string
	extraArgs            []string
}

// buildArgs 把 runSpec 翻译为 claude CLI 的 argv（不含 binary）。pure function，无副作用。
func buildArgs(spec runSpec) []string {
	// 注意：不带 -p。claude CLI stdout 接管道时自动进入非交互模式，
	// stream-json 协议照常工作；-p 的额外语义是 "result 帧后立刻 exit"，
	// 这一点本来就由我们 Close stdin 触发 EOF 来驱动，不需要它。
	args := []string{
		string(flagOutputFormat), formatStreamJSON,
		string(flagInputFormat), formatStreamJSON,
		string(flagVerbose),
		string(flagIncludePartialMsgs),
	}
	if spec.model != "" {
		args = append(args, string(flagModel), spec.model)
	}
	if spec.effort != "" {
		args = append(args, string(flagEffort), spec.effort)
	}
	if spec.resumeID != "" {
		args = append(args, string(flagResume), spec.resumeID)
	}
	if spec.resumeSessionAtUUID != "" {
		args = append(args, string(flagResumeSessionAt), spec.resumeSessionAtUUID)
	}
	if spec.forkSession {
		args = append(args, string(flagForkSession))
	}
	if spec.sessionID != "" {
		args = append(args, string(flagSessionID), spec.sessionID)
	}
	if spec.systemPrompt != "" {
		args = append(args, string(flagAppendSystemPrompt), spec.systemPrompt)
	}
	mode := spec.permissionMode
	if mode == "" {
		mode = "acceptEdits"
	}
	args = append(args, string(flagPermissionMode), mode)
	if len(spec.allowedTools) > 0 {
		args = append(args, string(flagAllowedTools), strings.Join(spec.allowedTools, ","))
	}
	if len(spec.disallowedTools) > 0 {
		args = append(args, string(flagDisallowedTools), strings.Join(spec.disallowedTools, ","))
	}
	if spec.maxTurns > 0 {
		args = append(args, string(flagMaxTurns), strconv.Itoa(spec.maxTurns))
	}
	if spec.settings != "" {
		args = append(args, string(flagSettings), spec.settings)
	}
	if spec.mcpConfig != "" {
		args = append(args, string(flagMcpConfig), spec.mcpConfig)
	}
	if spec.permissionPromptTool != "" {
		args = append(args, string(flagPermissionPromptTool), spec.permissionPromptTool)
	}
	if len(spec.extraArgs) > 0 {
		args = append(args, spec.extraArgs...)
	}
	return args
}

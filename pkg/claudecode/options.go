package claudecode

// Option Client 级配置。WithXxx 函数返回 Option 闭包。
type Option func(*Client)

func WithBinary(path string) Option         { return func(c *Client) { c.binary = path } }
func WithCwd(path string) Option            { return func(c *Client) { c.cwd = path } }
func WithEnv(env map[string]string) Option  { return func(c *Client) { c.env = env } }
func WithSystemPrompt(p string) Option      { return func(c *Client) { c.systemPrompt = p } }
func WithPermissionMode(mode string) Option { return func(c *Client) { c.permissionMode = mode } }

// WithModel 下发 claude CLI 的 --model <id>。绑了非 Anthropic provider（gateway
// 透明改写 body.model 路由到 GLM/openrouter 等）的后端必须传：不传时 CLI 用本地
// 登录态的默认模型名报 system.init.model，Agentre 拿来当 result.Model 写库 → 前端
// 显示成 "claude-opus-4-7" 而不是真实的 provider model。
func WithModel(id string) Option { return func(c *Client) { c.model = id } }

// WithSessionID pins the claude --session-id to a caller-provided UUID. The
// hook process inspects the matching value from the hook stdin payload, so the
// caller must use the same UUID that it pushes Steer messages under.
func WithSessionID(uuid string) Option { return func(c *Client) { c.sessionID = uuid } }

// WithSettings passes --settings <value> to claude so the CLI loads extra
// settings (hooks, allowed tools, etc.) in addition to the user's global
// settings. value 可以是 JSON 文件路径,也可以直接是 JSON 字符串 —— claude
// CLI 原生兼容两种形态 (`--settings <file-or-json>`)。
func WithSettings(value string) Option { return func(c *Client) { c.settings = value } }

// WithSettingsEnv writes env into a private temporary settings file passed via
// --settings. Claude gives this source precedence over the user's global
// settings, which keeps an AgentRE-selected provider from being replaced by a
// user-level ANTHROPIC_BASE_URL or credential. The values never enter argv and
// the temporary file is removed when the Stream or Session closes.
func WithSettingsEnv(env map[string]string) Option {
	return func(c *Client) { c.settingsEnv = env }
}

// WithMcpConfig 下发 --mcp-config <json-or-file>。claude CLI 原生兼容 JSON 串
// 或文件路径，用来注入额外的 MCP tool server（如内置的 org / hook / subagent 工具）。注入的
// tool 还需经 WithAllowedTools 放进 --allowedTools 才会被 CLI 实际暴露。
func WithMcpConfig(value string) Option { return func(c *Client) { c.mcpConfig = value } }

// WithAllowedTools 追加 --allowedTools 白名单条目。多次调用累加（不是覆盖），
// 便于在已有 client 选项之上叠加注入工具（如 mcp__org__org_create_department）。空入参
// 忽略。
func WithAllowedTools(tools ...string) Option {
	return func(c *Client) {
		for _, t := range tools {
			if t != "" {
				c.allowedTools = append(c.allowedTools, t)
			}
		}
	}
}

// WithEffort 把 claude CLI 的 --effort <level> 标志固化在 Client 上。
// level 取值：""(不下发) / "low" / "medium" / "high" / "xhigh" / "max"。
// 该值在 Stream/Text 起每一轮 turn 时透传，单值 client 跨 turn 一致。
func WithEffort(level string) Option { return func(c *Client) { c.effort = level } }

// WithPermissionPromptTool 设置 --permission-prompt-tool 实参。固定值 "stdio"
// 启用 stdio control protocol —— Claude Code 每次工具调用前发一帧
// control_request 到 stdout，等 host 写一帧 control_response 才继续。host
// 通过 Session.RespondToControl 回写决定。空串 = 不下发，CLI 走默认 TUI 询问。
func WithPermissionPromptTool(tool string) Option {
	return func(c *Client) { c.permissionPromptTool = tool }
}

// WithRawSink 注册一个原始行回调:子进程每读到一行非空 stdout(未解析的 stream-json
// 帧)就同步调用一次。用于 debug 级原始帧转储 —— runtime 层把它接到 logger.Debug,
// 由「Debug Logging」开关热控。回调收到的 []byte 是 scanner 的复用缓冲,**不得跨调用
// 留存**(要留存自行 copy)。nil(默认)= 零采样开销。
func WithRawSink(sink func([]byte)) Option { return func(c *Client) { c.rawSink = sink } }

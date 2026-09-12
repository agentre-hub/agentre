package agentruntime

import (
	"fmt"
	"sort"
	"strings"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

// LaunchCommandTokenPlaceholder 是 gateway token 的字面量占位符，
// 当调用方未提供真实 token（Spec.Token == ""）时落到命令里给用户自行替换。
const LaunchCommandTokenPlaceholder = "<TOKEN>"

// claudeCodeBinaryDefault / codexBinaryDefault 与 claudecode.go / codex.go 的默认值保持一致。
const (
	claudeCodeBinaryDefault = "claude"
	codexBinaryDefault      = "codex"
)

// LaunchCommandSpec 是 BuildLaunchCommand 的入参。
// Effective 可为 nil（CLI 未绑定 LLM 供应商时不写 BASE_URL / API_KEY）。
type LaunchCommandSpec struct {
	Backend *agent_backend_entity.AgentBackend
	// Effective 是执行侧解析结果（EffectiveLLMConfig v1 seam）：ProviderKey 用于
	// env 网关门控，ModelID 决定 --model / -c model。nil = 无供应商（CLI 登录态）。
	Effective *EffectiveLLMConfig
	AgentID   int64
	// SessionID 是 Agentre chat_sessions.id；部分 backend 用它组装本地运行参数。
	SessionID int64
	// Cwd 非空时作为 cd 目标；为空时回退 AgentCwd(AgentID)。chat_svc 调
	// project_svc.ResolveSessionCwd 解析 project 维度 cwd 注入。
	Cwd               string
	ProviderSessionID string // claudecode/piagent 用作 --resume/--session；codex 用作 resume <session-id>
	GatewayURL        string // gateway URL，空字符串表示未关联 provider
	Token             string // gateway token；非空时内联进命令；空时落 <TOKEN> 占位
}

// BuildLaunchCommand 拼一条粘贴到终端就能跑的 shell 命令，形如：
//
//	cd '<cwd>' && KEY='val' KEY2='val2' <binary> --flag 'value' ...
//
// 仅支持 claudecode / codex / piagent；builtin 没有对应 CLI，直接返回 error。
func BuildLaunchCommand(spec LaunchCommandSpec) (string, error) {
	if spec.Backend == nil {
		return "", fmt.Errorf("agentruntime: nil backend")
	}
	cwd := spec.Cwd
	if cwd == "" {
		var err error
		cwd, err = AgentCwd(spec.AgentID)
		if err != nil {
			return "", err
		}
	}
	switch agent_backend_entity.BackendType(spec.Backend.Type) {
	case agent_backend_entity.TypeClaudeCode:
		return buildClaudeCodeShellCommand(spec, cwd)
	case agent_backend_entity.TypeCodex:
		return buildCodexShellCommand(spec, cwd)
	case agent_backend_entity.TypePiAgent:
		return buildPiAgentShellCommand(spec, cwd)
	default:
		return "", fmt.Errorf("agentruntime: backend %q has no shell command", spec.Backend.Type)
	}
}

func buildClaudeCodeShellCommand(spec LaunchCommandSpec, cwd string) (string, error) {
	// 即便 Token 为空，只要 GatewayURL 非空，仍用占位符喂 BuildClaudeCodeEnv，
	// 让它把 BASE_URL/AUTH_TOKEN 写进 env；真值替换在 assembleShellLine 里做。
	// ProviderKey 传 spec.Provider 的 key(effective provider，已过回退):backend 未绑定
	// 但会话选了供应商时，BuildClaudeCodeEnv 的 ANTHROPIC_* 门控只能靠它才知道有
	// effective provider(spec.Backend.LLMProviderKey 此时为空，回落不到)。
	deps := CLIDeps{}
	if spec.GatewayURL != "" {
		deps = CLIDeps{Token: tokenOrPlaceholder(spec.Token), GatewayURL: spec.GatewayURL}
		if spec.Effective != nil {
			deps.ProviderKey = spec.Effective.ProviderKey
		}
	}
	env, err := BuildClaudeCodeEnv(spec.Backend, deps)
	if err != nil {
		return "", err
	}

	binary := strings.TrimSpace(spec.Backend.CLIPath)
	if binary == "" {
		binary = claudeCodeBinaryDefault
	}

	argv := []string{binary}
	if spec.Effective != nil {
		if model := strings.TrimSpace(spec.Effective.ModelID); model != "" {
			argv = append(argv, "--model", model)
		}
	}
	if eff := spec.Backend.ReasoningEffort; eff != "" {
		argv = append(argv, "--effort", eff)
	}
	if sid := strings.TrimSpace(spec.ProviderSessionID); sid != "" {
		argv = append(argv, "--resume", sid)
	}

	return assembleShellLine(cwd, env, argv), nil
}

func buildCodexShellCommand(spec LaunchCommandSpec, cwd string) (string, error) {
	deps := CLIDeps{}
	if spec.GatewayURL != "" {
		deps = CLIDeps{Token: tokenOrPlaceholder(spec.Token), GatewayURL: spec.GatewayURL}
	}
	env, err := BuildCodexEnv(spec.Backend, deps)
	if err != nil {
		return "", err
	}

	binary := strings.TrimSpace(spec.Backend.CLIPath)
	if binary == "" {
		binary = codexBinaryDefault
	}

	argv := []string{binary}
	resumeID := strings.TrimSpace(spec.ProviderSessionID)
	if resumeID != "" {
		argv = append(argv, "resume")
	}
	for _, cfg := range BuildCodexConfig(deps) {
		argv = append(argv, "-c", cfg)
	}
	if eff := CodexReasoningEffortConfigValue(spec.Backend.ReasoningEffort); eff != "" {
		argv = append(argv, "-c", `model_reasoning_effort="`+eff+`"`)
	}
	if spec.Effective != nil {
		if model := strings.TrimSpace(spec.Effective.ModelID); model != "" {
			// codex 终端版用 -c 覆盖 model；与 BuildCodexConfig 的其它 -c 一致。
			argv = append(argv, "-c", `model="`+model+`"`)
		}
	}
	if resumeID != "" {
		argv = append(argv, resumeID)
	}

	return assembleShellLine(cwd, env, argv), nil
}

// assembleShellLine 把 cwd / env / argv 拼成一行 shell 命令：
//
//	cd '<cwd>' && KEY='val' KEY2='val2' <binary> arg arg ...
//
// 设计取舍：
//   - cwd / env value 永远用单引号包裹（即便不含特殊字符）—— 路径/值的语义需要明显边界，
//     方便用户拷贝后改字段，且对将来出现空格/$ 的输入鲁棒；
//   - argv 里只给含特殊字符的 token 加引号，让 `-c` / `--resume` 这类 flag 保持可读；
//   - env 按 key 字母序输出，保证可复现；
//   - 用 inline env 而非 `export` —— 整条单行可粘贴，且环境变量不会污染当前 shell。
func assembleShellLine(cwd string, env map[string]string, argv []string) string {
	var b strings.Builder
	b.WriteString("cd ")
	b.WriteString(shellQuoteAlways(cwd))
	b.WriteString(" && ")

	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(shellQuoteAlways(env[k]))
		b.WriteString(" ")
	}

	for i, a := range argv {
		if i == 0 {
			b.WriteString(a) // binary 路径不引用，沿用 PATH 解析行为
			continue
		}
		b.WriteString(" ")
		b.WriteString(shellQuoteArg(a))
	}
	return b.String()
}

// tokenOrPlaceholder：调用方提供的真实 token 优先；否则落占位符。
func tokenOrPlaceholder(token string) string {
	if t := strings.TrimSpace(token); t != "" {
		return t
	}
	return LaunchCommandTokenPlaceholder
}

// shellQuoteAlways 永远用 POSIX 单引号包裹；内部 quote 用 close-escape-open 替换。
// 空串输出一对裸单引号，避免被 shell 当成无参数。
func shellQuoteAlways(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellQuoteArg 是 argv 用的"懒引号"：纯 shell-safe 字符不加引号，
// 让 `-c` / `--resume` / 短 token 保持可读。否则走 shellQuoteAlways。
func shellQuoteArg(s string) string {
	if s != "" && isShellSafe(s) {
		return s
	}
	return shellQuoteAlways(s)
}

func isShellSafe(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '=' || r == ',':
		default:
			return false
		}
	}
	return true
}

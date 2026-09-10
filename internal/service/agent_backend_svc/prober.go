package agent_backend_svc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/agents/app/coding"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentprovider"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/piagent"
	"github.com/agentre-hub/agentre/internal/pkg/cliprober"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/service/llm_provider_svc"
)

// proberFor 按 backend 类型查 Prober；未注册返 nil。
//
// 默认注册表只包含 builtin in-process 路径；单测可临时替换这个包级 var
// 验证 Test() 的 BackendType 派发。
var proberRegistry = map[agent_backend_entity.BackendType]Prober{
	agent_backend_entity.TypeBuiltin:    builtinProber{},
	agent_backend_entity.TypeClaudeCode: cliProber{},
	agent_backend_entity.TypeCodex:      cliProber{},
	agent_backend_entity.TypePiAgent:    cliProber{},
}

// providerBuilder 是 agentprovider.Build 的间接引用，让单测能把 fake provider
// 注入 builtinProber 而不必真的去打 anthropic / openai 网络。生产路径保持透明。
var providerBuilder = agentprovider.Build

// proberFor 查表。
func proberFor(t agent_backend_entity.BackendType) Prober {
	if p, ok := proberRegistry[t]; ok {
		return p
	}
	return nil
}

// builtinProber 跑 cago app/coding，in-process 拉一轮 agent loop，回 assistant 末轮 text。
type builtinProber struct{}

func (builtinProber) Run(ctx context.Context, b *agent_backend_entity.AgentBackend, _ ProbeDeps) (string, error) {
	if b == nil {
		return "", errors.New("nil backend")
	}
	p, err := llm_provider_repo.LLMProvider().FindByKey(ctx, b.LLMProviderKey)
	if err != nil {
		return "", err
	}
	if p == nil || !p.IsActive() {
		return "", errors.New("llm provider missing or inactive")
	}
	// 执行侧模型解析（EffectiveLLMConfig v1 seam）：与 chat run 同一口径（sessionModelKeyFor）——
	// backend 钉了固定模型（b.LLMModelKey）时测固定模型，否则 provider-default 解析当前默认模型；
	// 不再读 Provider 旧单模型字段。测试连接必须测这条 backend 真正会跑的那个模型。
	resolved, err := llm_provider_svc.LLMProvider().ResolveTarget(ctx, llm_provider_svc.ModelTarget{
		ProviderKey: p.ProviderKey,
		ModelKey:    strings.TrimSpace(b.LLMModelKey),
	})
	if err != nil {
		return "", err
	}
	prov, err := providerBuilder(p)
	if err != nil {
		return "", err
	}

	cwd, err := os.MkdirTemp("", "agentre-backend-test-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(cwd) }()

	// 必须把解析出的 ModelID 透传到父 agent，否则 ChatStream req.Model 为空，
	// anthropic / openai 收到空 model 直接 400，呈现"200ms 完成但没调用记录"的假象。
	sys, err := coding.New(ctx, prov, cwd, coding.WithModel(resolved.ModelID))
	if err != nil {
		return "", err
	}
	defer func() { _ = sys.Close(ctx) }()

	conv := agent.NewConversation()
	runner, err := sys.Agent().TryRunner(conv)
	if err != nil {
		return "", err
	}
	defer func() { _ = runner.Close() }()

	if err := runner.Wait(ctx, fixedTestPrompt); err != nil {
		return "", err
	}
	return lastAssistantText(conv), nil
}

// buildClaudeCodeEnv 委托到 agentruntime.BuildClaudeCodeEnv。
// 保留包级 helper 以维持现有调用点与测试的命名稳定；逻辑与文档全部迁到
// internal/pkg/agentruntime/clienv.go，与 chat path 的 CLI runner 共享同一份装配规则，
// 避免两处漂移。
func buildClaudeCodeEnv(b *agent_backend_entity.AgentBackend, deps ProbeDeps) (map[string]string, error) {
	return agentruntime.BuildClaudeCodeEnv(b, agentruntime.CLIDeps{
		Token:      deps.Token,
		GatewayURL: deps.GatewayURL,
	})
}

// buildCodexEnv 委托到 agentruntime.BuildCodexEnv；同 buildClaudeCodeEnv。
func buildCodexEnv(b *agent_backend_entity.AgentBackend, deps ProbeDeps) (map[string]string, error) {
	return agentruntime.BuildCodexEnv(b, agentruntime.CLIDeps{
		Token:      deps.Token,
		GatewayURL: deps.GatewayURL,
	})
}

// buildPiAgentEnv 委托到 agentruntime.BuildPiAgentEnv；同其它 CLI env builder。
func buildPiAgentEnv(b *agent_backend_entity.AgentBackend) (map[string]string, error) {
	return agentruntime.BuildPiAgentEnv(b)
}

// cliProber 通过 cliprober fork 对应 CLI 子进程跑固定 ping。
type cliProber struct{}

func (cliProber) Run(ctx context.Context, b *agent_backend_entity.AgentBackend, deps ProbeDeps) (string, error) {
	if b == nil {
		return "", errors.New("nil backend")
	}
	env, configs, err := buildCLIProbeEnv(b, deps)
	if err != nil {
		return "", err
	}
	model := resolveCLIProbeModel(b, deps)
	extensions, env, model, err := buildPiAgentProviderProbe(ctx, b, env, model)
	if err != nil {
		return "", err
	}
	resp, err := cliprober.Probe(ctx, cliprober.ProbeRequest{
		Type:         b.Type,
		CLIPath:      b.CLIPath,
		Sandbox:      b.Sandbox,
		Approval:     b.Approval,
		Model:        model,
		Env:          env,
		CodexConfigs: configs,
		Extensions:   extensions,
	})
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

// buildPiAgentProviderProbe 为绑定供应商的 piagent 装配连通性探测参数（设计决策 #6 /
// agent-backend.md §2.3 不变量：Test 与 chat run 同一装配规则，不漂移）：
//   - provider 必须存在且 active（仿 builtinProber 的校验）；
//   - 物化 provider 扩展（piagent.MaterializeProviderExtension，与 chat run 同源）；
//   - env 在 buildPiAgentEnv 产出的 base 之上叠加 AGENTRE_PI_API_KEY_*；
//   - --model 覆盖为 agentre-<key>/<model>（盖掉 buildPiAgentProbeModel 的 ""）。
//
// 未绑定供应商的 piagent 保持现状：原样返回入参，不注入任何东西。
func buildPiAgentProviderProbe(ctx context.Context, b *agent_backend_entity.AgentBackend, env map[string]string, model string) (extensions []string, envOut map[string]string, modelOut string, err error) {
	if b == nil || !b.IsPiAgent() || b.LLMProviderKey == "" {
		return nil, env, model, nil
	}
	p, err := llm_provider_repo.LLMProvider().FindByKey(ctx, b.LLMProviderKey)
	if err != nil {
		return nil, env, model, err
	}
	if p == nil || !p.IsActive() {
		return nil, env, model, errors.New("llm provider missing or inactive")
	}
	// APIKey 空 → 配置错误（与 runtime.go 的检查一致：Test 与 chat run 同一失败路径，
	// 不 spawn Pi；消息只含 provider key，不含密钥）。
	if strings.TrimSpace(p.APIKey) == "" {
		return nil, env, model, fmt.Errorf("llm provider %q has empty APIKey", p.ProviderKey)
	}
	// 执行侧模型解析（EffectiveLLMConfig v1 seam）：与 chat run 同一口径（sessionModelKeyFor）——
	// backend 钉了固定模型（b.LLMModelKey）时测固定模型，否则 provider-default 解析当前默认模型；
	// 测试连接必须测这条 backend 真正会跑的那个模型。
	cfg, err := effectiveLLMForProbe(ctx, p, b.LLMModelKey)
	if err != nil {
		return nil, env, model, err
	}
	extPath, err := piagent.MaterializeProviderExtension(cfg)
	if err != nil {
		return nil, env, model, err
	}
	providerModel, err := agentruntime.PiAgentProviderModelName(cfg)
	if err != nil {
		return nil, env, model, err
	}
	return []string{extPath}, agentruntime.BuildPiAgentProviderEnv(env, cfg), providerModel, nil
}

// effectiveLLMForProbe 装配 Test 连通性用的执行侧配置：经 llm_provider_svc.ResolveTarget
// 解析模型（modelKey 空 = provider-default 解析当前默认模型，非空 = fixed-model），再经
// 共享构造口 agentruntime.NewEffectiveLLMConfig 装配 —— Mode 只在那一处计算，Test 与
// chat run 因此不会漂移。
func effectiveLLMForProbe(
	ctx context.Context, p *llm_provider_entity.LLMProvider, modelKey string,
) (*agentruntime.EffectiveLLMConfig, error) {
	target := llm_provider_svc.ModelTarget{ProviderKey: p.ProviderKey, ModelKey: strings.TrimSpace(modelKey)}
	resolved, err := llm_provider_svc.LLMProvider().ResolveTarget(ctx, target)
	if err != nil {
		return nil, err
	}
	return agentruntime.NewEffectiveLLMConfig(agentruntime.EffectiveLLMConfigInput{
		ProviderKey:      resolved.ProviderKey,
		ProviderType:     resolved.ProviderType,
		ProviderName:     p.Name,
		TargetModelKey:   target.ModelKey,
		ResolvedModelKey: resolved.ModelKey,
		ResolvedModelID:  resolved.ModelID,
		ContextWindow:    resolved.ContextWindow,
		MaxOutput:        resolved.MaxOutput,
		BaseURL:          resolved.BaseURL,
		APIKey:           resolved.APIKey,
		HasAPIKey:        resolved.HasAPIKey,
	}), nil
}

func buildPiAgentProbeModel(*agent_backend_entity.AgentBackend) string {
	return ""
}

// resolveCLIProbeModel 选 Test 连通性下发给 CLI 的模型,与 chat-path
// claudecode/session.go::ccBuildClientOpts 同优先级,避免 Test 与实际 chat run 漂移
// (agent-backend.md §2.3 不变量):provider/gateway 模型(deps.Model) → claudecode 后端
// DefaultModel(走 CLI 登录态时的自定义模型) → ""(CLI 默认)。piagent 未绑定时返回空
// (走 CLI 默认);绑定供应商时 --model 由 buildPiAgentProviderProbe 覆盖为
// agentre-<key>/<model>。
func resolveCLIProbeModel(b *agent_backend_entity.AgentBackend, deps ProbeDeps) string {
	if b.IsPiAgent() {
		return buildPiAgentProbeModel(b)
	}
	if m := strings.TrimSpace(deps.Model); m != "" {
		return m
	}
	if b.IsClaudeCode() {
		return strings.TrimSpace(b.DefaultModel)
	}
	return ""
}

func buildCLIProbeEnv(b *agent_backend_entity.AgentBackend, deps ProbeDeps) (map[string]string, []string, error) {
	switch agent_backend_entity.BackendType(b.Type) {
	case agent_backend_entity.TypeClaudeCode:
		env, err := buildClaudeCodeEnv(b, deps)
		return env, nil, err
	case agent_backend_entity.TypeCodex:
		env, err := buildCodexEnv(b, deps)
		if err != nil {
			return nil, nil, err
		}
		return env, agentruntime.BuildCodexConfig(agentruntime.CLIDeps{Token: deps.Token, GatewayURL: deps.GatewayURL}), nil
	case agent_backend_entity.TypePiAgent:
		env, err := buildPiAgentEnv(b)
		return env, nil, err
	default:
		return nil, nil, errors.New("unsupported CLI backend")
	}
}

// lastAssistantText 拼接末尾一条 assistant message 的所有 TextBlock 内容。
// 忽略 tool use 等其他 block 类型;固定 prompt 不触发工具,正常路径应直接是 "pong"。
func lastAssistantText(conv *agent.Conversation) string {
	msgs := conv.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != agent.RoleAssistant {
			continue
		}
		var b strings.Builder
		for _, blk := range msgs[i].Content {
			if tb, ok := blk.(blocks.TextBlock); ok {
				b.WriteString(tb.Text)
			}
		}
		return b.String()
	}
	return ""
}

package agentruntime

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
)

// piProviderAPIByType 把 llm_provider_entity 的三种供应商 Type 映射到 Pi 原生
// registerProvider 的 api 形状。其它 Type 返回 false（piagent 不支持）。
func piProviderAPIByType(t string) (string, bool) {
	switch llm_provider_entity.ProviderType(t) {
	case llm_provider_entity.TypeAnthropic:
		return "anthropic-messages", true
	case llm_provider_entity.TypeOpenAIChat:
		return "openai-completions", true
	case llm_provider_entity.TypeOpenAIResponse:
		return "openai-responses", true
	default:
		return "", false
	}
}

// sanitizeProviderKey 去掉 providerKey 中的非字母数字字符（如 UUID 的 '-'），
// 使拼出的 env 变量名合法。provider 注册名 / --model 值仍用原始 key（见下）。
func sanitizeProviderKey(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// PiAgentProviderEnvKey 返回 provider 扩展子进程 env 里承载 APIKey 的键名：
// "AGENTRE_PI_API_KEY_" + providerKey（去非字母数字），保证 env 变量名合法。
func PiAgentProviderEnvKey(providerKey string) string {
	return "AGENTRE_PI_API_KEY_" + sanitizeProviderKey(providerKey)
}

// PiAgentProviderModelName 返回绑定供应商时的模型选择值 "agentre-<key>/<model>"。
// provider 注册名与 --model 值都用原始 ProviderKey（UUID 形态）；Type 不可识别
// 或 ModelID 为空返回 error（绑定保存时已拦截，此处兜底）。
//
// cfg 是执行侧解析结果（EffectiveLLMConfig v1 seam）：模型 id 取解析出的 ModelID，
// 不再从 Provider 旧单模型字段读取。
func PiAgentProviderModelName(cfg *EffectiveLLMConfig) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("agentruntime: effective config is nil")
	}
	if _, ok := piProviderAPIByType(cfg.ProviderType); !ok {
		return "", fmt.Errorf("agentruntime: unsupported provider type %q", cfg.ProviderType)
	}
	model := strings.TrimSpace(cfg.ModelID)
	if model == "" {
		return "", fmt.Errorf("agentruntime: provider model is empty")
	}
	return "agentre-" + cfg.ProviderKey + "/" + model, nil
}

// PiAgentProviderExtension 渲染注入 Pi 的 provider 扩展源码（纯文本 JS）：
// 调用 pi.registerProvider 注册 "agentre-<key>"，APIKey 只以 $ENV_VAR 引用，
// 密钥本身绝不落入扩展源。contextWindow / maxTokens 为 0 时省略字段；
// 每个 model 固定带 cost（全 0），避免绑定模型 id 与用户 ~/.pi/agent 撞名时
// pi 0.83.0 模型合并崩溃（provider-composer.js applyModelOverride 读 model.cost.tiers）。
//
// cfg 是执行侧解析结果（EffectiveLLMConfig v1 seam）：模型 id / 窗口 / 最大输出
// 取解析值，Provider 名称 / BaseURL / Type 取连接信息。
func PiAgentProviderExtension(cfg *EffectiveLLMConfig) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("agentruntime: effective config is nil")
	}
	api, ok := piProviderAPIByType(cfg.ProviderType)
	if !ok {
		return "", fmt.Errorf("agentruntime: unsupported provider type %q", cfg.ProviderType)
	}
	model := strings.TrimSpace(cfg.ModelID)
	if model == "" {
		return "", fmt.Errorf("agentruntime: provider model is empty")
	}

	modelObj := fmt.Sprintf("{ id: %s, name: %s, reasoning: true, input: [\"text\",\"image\"]",
		strconv.Quote(model), strconv.Quote(model))
	if cfg.ContextWindow > 0 {
		modelObj += fmt.Sprintf(", contextWindow: %d", cfg.ContextWindow)
	}
	if cfg.MaxOutput > 0 {
		modelObj += fmt.Sprintf(", maxTokens: %d", cfg.MaxOutput)
	}
	modelObj += ", cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }"

	return fmt.Sprintf(
		"export default function (pi) { pi.registerProvider(%s, { name: %s, baseUrl: %s, api: %s, apiKey: %s, models: [%s] }) }",
		strconv.Quote("agentre-"+cfg.ProviderKey),
		strconv.Quote(cfg.ProviderName),
		strconv.Quote(cfg.BaseURL),
		strconv.Quote(api),
		strconv.Quote("$"+PiAgentProviderEnvKey(cfg.ProviderKey)),
		modelObj,
	), nil
}

// BuildPiAgentProviderEnv 返回 base 的副本，并注入 provider 的 env 键 → APIKey，
// 供子进程使用；不改入参 map。
func BuildPiAgentProviderEnv(base map[string]string, cfg *EffectiveLLMConfig) map[string]string {
	out := make(map[string]string, len(base)+1)
	for k, v := range base {
		out[k] = v
	}
	if cfg != nil && cfg.ProviderKey != "" {
		out[PiAgentProviderEnvKey(cfg.ProviderKey)] = cfg.APIKey
	}
	return out
}

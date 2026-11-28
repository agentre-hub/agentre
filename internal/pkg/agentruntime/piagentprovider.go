package agentruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
)

// PiAgentProviderRegistryEnvKey 是子进程 provider 注册表 env 的键名：Agentre 每会话注入
// {"agentre-<providerKey>": <provider config>}，随进程 env 继承到 Pi 子进程（subagent），
// 由装在 Pi 配置目录 extensions/ 下的 agentre-provider.js 读出并 registerProvider。
// 父进程的 --extension 到不了子进程（pi 只显式接受 --extension），env 是唯一通道；
// 见 runtimes/piagent/childprovider.go。
const PiAgentProviderRegistryEnvKey = "AGENTRE_PI_PROVIDERS"

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

// PiAgentGatewayTokenEnvKey 是承载会话网关 token 的 env 键名（与 claudecode 的 hook 子进程
// 同名）：provider 配置的 apiKey 只以 "$AGENTRE_GATEWAY_TOKEN" 引用它。真实供应商 key 不进入
// Pi 进程树，只在网关转发时换上。
const PiAgentGatewayTokenEnvKey = "AGENTRE_GATEWAY_TOKEN"

// piAgentGatewayBaseURL 返回 Pi 对该类型供应商应使用的网关 baseUrl：anthropic SDK 自己拼
// /v1/messages，所以用网关根；openai 系 SDK 拼 /chat/completions、/responses，网关路由是
// /v1/...，所以要带 /v1（与 BuildCodexConfig 同口径）。
func piAgentGatewayBaseURL(providerType, gatewayURL string) string {
	base := strings.TrimRight(strings.TrimSpace(gatewayURL), "/")
	if llm_provider_entity.ProviderType(providerType) == llm_provider_entity.TypeAnthropic {
		return base
	}
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return base
}

// piAgentProviderName 返回 provider 在 pi 里的注册名：扩展源码（--extension）与注册表 env
// 都用它，--model 的 provider 段也必须是同一个值，子进程才解析得到。
func piAgentProviderName(providerKey string) string {
	return "agentre-" + providerKey
}

// PiAgentProviderModelName 返回绑定供应商时的模型选择值 "agentre-<key>/<model>"。
// provider 注册名与 --model 值都用原始 ProviderKey（UUID 形态）；Type 不可识别
// 或 ModelID 为空返回 error（绑定保存时已拦截，此处兜底）。
//
// cfg 是执行侧解析结果（EffectiveLLMConfig v1 seam）：模型 id 取解析出的 ModelID，
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
	return piAgentProviderName(cfg.ProviderKey) + "/" + model, nil
}

// piProviderModelCost / piProviderModel / piProviderConfig 是 registerProvider config 的
// 序列化形状（字段名 = pi 读的键名）。
type piProviderModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

type piProviderModel struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Reasoning     bool                `json:"reasoning"`
	Input         []string            `json:"input"`
	ContextWindow int                 `json:"contextWindow,omitempty"`
	MaxTokens     int                 `json:"maxTokens,omitempty"`
	Cost          piProviderModelCost `json:"cost"`
}

type piProviderConfig struct {
	Name    string            `json:"name"`
	BaseURL string            `json:"baseUrl"`
	API     string            `json:"api"`
	APIKey  string            `json:"apiKey"`
	Models  []piProviderModel `json:"models"`
}

// piAgentProviderConfigJSON 渲染 pi registerProvider 的第二个参数（provider config）为 JSON。
// 两条下发路径共用它，字段不会各写一份：
//   - 父进程：per-session provider 扩展源码（PiAgentProviderExtension，走 --extension）；
//   - 子进程：PiAgentProviderRegistryEnvKey 注册表（走 env 继承）。
//
// contextWindow / maxTokens 为 0 时省略字段；每个 model 固定带 cost（全 0），避免绑定模型
// id 与用户 ~/.pi/agent 撞名时 pi 0.83.0 模型合并崩溃（provider-composer.js
// applyModelOverride 读 model.cost.tiers）。apiKey 只以 "$AGENTRE_GATEWAY_TOKEN" 引用会话网关
// token，真实供应商 key 不进入返回值，也就不会落进任何下发给 Pi 的文本。
func piAgentProviderConfigJSON(cfg *EffectiveLLMConfig, gatewayURL string) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("agentruntime: effective config is nil")
	}
	if strings.TrimSpace(gatewayURL) == "" {
		return "", fmt.Errorf("agentruntime: pi provider needs the Agentre gateway URL")
	}
	api, ok := piProviderAPIByType(cfg.ProviderType)
	if !ok {
		return "", fmt.Errorf("agentruntime: unsupported provider type %q", cfg.ProviderType)
	}
	model := strings.TrimSpace(cfg.ModelID)
	if model == "" {
		return "", fmt.Errorf("agentruntime: provider model is empty")
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	// provider 名 / baseUrl 里的 & 与 < 保持原样，日志与文件内容都可直接读。
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(piProviderConfig{
		Name: cfg.ProviderName,
		// Pi 只认网关：上游端点与真实 key 都在网关里，按 token 的目标转发。
		BaseURL: piAgentGatewayBaseURL(cfg.ProviderType, gatewayURL),
		API:     api,
		APIKey:  "$" + PiAgentGatewayTokenEnvKey,
		Models: []piProviderModel{{
			ID:        model,
			Name:      model,
			Reasoning: true,
			Input:     []string{"text", "image"},
			// 解析不出窗口/上限的供应商省掉这两个字段，让 pi 用自己的默认值。
			ContextWindow: cfg.ContextWindow,
			MaxTokens:     cfg.MaxOutput,
			Cost:          piProviderModelCost{},
		}},
	})
	if err != nil {
		return "", fmt.Errorf("agentruntime: render provider config: %w", err)
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// piAgentProviderRegistryJSON 渲染子进程注册表 env 的值 {"agentre-<key>": <config>}。
func piAgentProviderRegistryJSON(cfg *EffectiveLLMConfig, gatewayURL string) (string, error) {
	config, err := piAgentProviderConfigJSON(cfg, gatewayURL)
	if err != nil {
		return "", err
	}
	return "{" + strconv.Quote(piAgentProviderName(cfg.ProviderKey)) + ":" + config + "}", nil
}

// PiAgentProviderExtension 渲染注入 Pi 的 provider 扩展源码（纯文本 JS）：
// 调用 pi.registerProvider 注册 "agentre-<key>"，config 与子进程注册表同源
// （piAgentProviderConfigJSON），apiKey 只以 $AGENTRE_GATEWAY_TOKEN 引用，baseUrl 是会话网关，真实密钥绝不落入扩展源。
//
// cfg 是执行侧解析结果（EffectiveLLMConfig v1 seam）：模型 id / 窗口 / 最大输出
// 取解析值，Provider 名称 / BaseURL / Type 取连接信息。
func PiAgentProviderExtension(cfg *EffectiveLLMConfig, gatewayURL string) (string, error) {
	config, err := piAgentProviderConfigJSON(cfg, gatewayURL)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("export default function (pi) { pi.registerProvider(%s, %s) }",
		strconv.Quote(piAgentProviderName(cfg.ProviderKey)), config), nil
}

// BuildPiAgentProviderEnv 返回 base 的副本，并注入 provider 的两个 env 键，供 pi 进程及其
// 子进程（subagent）使用；不改入参 map：
//   - 会话网关 token → PiAgentGatewayTokenEnvKey，扩展源码与注册表的 apiKey 都只引用它；
//   - provider config → PiAgentProviderRegistryEnvKey，让子进程自己也注册 provider。
//
// 真实供应商 key 永不进入 env：绑定了供应商却没有网关 URL / token 时返回错误，而不是退回
// 直连（Pi 进程树里没有可直连的凭据）。
func BuildPiAgentProviderEnv(base map[string]string, cfg *EffectiveLLMConfig, deps CLIDeps) (map[string]string, error) {
	out := make(map[string]string, len(base)+2)
	maps.Copy(out, base)
	if cfg == nil || cfg.ProviderKey == "" {
		return out, nil
	}
	if strings.TrimSpace(deps.GatewayURL) == "" || strings.TrimSpace(deps.Token) == "" {
		return nil, fmt.Errorf("agentruntime: pi provider %q needs the Agentre gateway: make sure the local gateway is running", cfg.ProviderKey)
	}
	out[PiAgentGatewayTokenEnvKey] = deps.Token
	// 渲染不出 config（没解析出模型 / Type 不支持）时不注入注册表：registerProvider 不接受
	// 空 models，这种配置本来也不会下发 agentre-<key>/<model>。真正的失败由 providerRunConfig
	// 在同一条路径上显式返回。
	if registry, err := piAgentProviderRegistryJSON(cfg, deps.GatewayURL); err == nil {
		out[PiAgentProviderRegistryEnvKey] = registry
	}
	return out, nil
}

package agentruntime

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
)

// testProviderKey 是 UUID 形态的稳定键：带 '-'，env 键名里必须去掉。
const testProviderKey = "9a3b2c1d-4e5f-6a7b-8c9d-0e1f2a3b4c5d"

// testProviderConfig 构造一条 provider-default 执行侧配置（EffectiveLLMConfig v1 seam）。
func testProviderConfig(typ, modelID string) *EffectiveLLMConfig {
	return &EffectiveLLMConfig{
		Mode:          EffectiveModeProviderDefault,
		ProviderKey:   testProviderKey,
		ProviderType:  typ,
		ProviderName:  "Compat",
		ModelID:       modelID,
		ContextWindow: 200000,
		MaxOutput:     8192,
	}
}

func TestPiAgentProviderEnvKey_SanitizesProviderKey(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want string
	}{
		{"uuid 去 '-'", testProviderKey, "AGENTRE_PI_API_KEY_9a3b2c1d4e5f6a7b8c9d0e1f2a3b4c5d"},
		{"空 key 退化为前缀", "", "AGENTRE_PI_API_KEY_"},
		{"非字母数字全部剔除", "A-B!C_d.1", "AGENTRE_PI_API_KEY_ABCd1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, PiAgentProviderEnvKey(tc.key))
		})
	}
}

func TestPiAgentProviderModelName_AllTypes(t *testing.T) {
	cases := []struct {
		name    string
		typ     string
		model   string
		want    string
		wantErr bool
	}{
		{"anthropic", string(llm_provider_entity.TypeAnthropic), "claude-sonnet-4", "agentre-" + testProviderKey + "/claude-sonnet-4", false},
		{"openai-chat", string(llm_provider_entity.TypeOpenAIChat), "gpt-4o", "agentre-" + testProviderKey + "/gpt-4o", false},
		{"openai-response", string(llm_provider_entity.TypeOpenAIResponse), "o3", "agentre-" + testProviderKey + "/o3", false},
		{"未知 Type 报错", "deepseek", "gpt-4o", "", true},
		{"ModelID 为空报错", string(llm_provider_entity.TypeOpenAIChat), "", "", true},
		{"ModelID 纯空白报错", string(llm_provider_entity.TypeOpenAIChat), "  ", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testProviderConfig(tc.typ, tc.model)
			got, err := PiAgentProviderModelName(cfg)
			if tc.wantErr {
				require.Error(t, err)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPiAgentProviderModelName_NilConfig(t *testing.T) {
	_, err := PiAgentProviderModelName(nil)
	require.Error(t, err)
}

// providerConfigPayload 是 registerProvider 的第二个参数在测试里的解构形状。断言按字段值
// 做，不按渲染文本做：字段值是 Pi 真正读的契约，渲染文本只是它的载体。
type providerConfigPayload struct {
	Name    string `json:"name"`
	BaseURL string `json:"baseUrl"`
	API     string `json:"api"`
	APIKey  string `json:"apiKey"`
	Models  []struct {
		ID            string   `json:"id"`
		Name          string   `json:"name"`
		Reasoning     bool     `json:"reasoning"`
		Input         []string `json:"input"`
		ContextWindow int      `json:"contextWindow"`
		MaxTokens     int      `json:"maxTokens"`
		Cost          struct {
			Input      float64 `json:"input"`
			Output     float64 `json:"output"`
			CacheRead  float64 `json:"cacheRead"`
			CacheWrite float64 `json:"cacheWrite"`
		} `json:"cost"`
	} `json:"models"`
}

func decodeProviderConfig(t *testing.T, raw string) providerConfigPayload {
	t.Helper()
	var got providerConfigPayload
	require.NoError(t, json.Unmarshal([]byte(raw), &got))
	return got
}

func TestPiAgentProviderConfigJSON_APIMappingByProviderType(t *testing.T) {
	cases := []struct {
		name string
		typ  string
		api  string
	}{
		{"anthropic → anthropic-messages", string(llm_provider_entity.TypeAnthropic), "anthropic-messages"},
		{"openai-chat → openai-completions", string(llm_provider_entity.TypeOpenAIChat), "openai-completions"},
		{"openai-response → openai-responses", string(llm_provider_entity.TypeOpenAIResponse), "openai-responses"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testProviderConfig(tc.typ, "m-1")
			cfg.ProviderName = "My Compat"
			cfg.BaseURL = "https://proxy.example.com"
			cfg.APIKey = "sk-plaintext-secret"

			raw, err := piAgentProviderConfigJSON(cfg)
			require.NoError(t, err)
			got := decodeProviderConfig(t, raw)

			assert.Equal(t, "My Compat", got.Name)
			assert.Equal(t, "https://proxy.example.com", got.BaseURL)
			assert.Equal(t, tc.api, got.API)
			// 密钥只进 env：config 里只留 $ENV 引用，明文绝不落进下发给 Pi 的任何文本。
			assert.Equal(t, "$AGENTRE_PI_API_KEY_9a3b2c1d4e5f6a7b8c9d0e1f2a3b4c5d", got.APIKey)
			assert.NotContains(t, raw, "sk-plaintext-secret")

			require.Len(t, got.Models, 1)
			model := got.Models[0]
			assert.Equal(t, "m-1", model.ID)
			assert.Equal(t, "m-1", model.Name)
			assert.True(t, model.Reasoning)
			assert.Equal(t, []string{"text", "image"}, model.Input)
			assert.Equal(t, 200000, model.ContextWindow)
			assert.Equal(t, 8192, model.MaxTokens)
		})
	}
}

func TestPiAgentProviderConfigJSON_OmitsZeroWindowAndMaxTokensButKeepsCost(t *testing.T) {
	cfg := testProviderConfig(string(llm_provider_entity.TypeOpenAIChat), "qwen")
	cfg.ContextWindow = 0
	cfg.MaxOutput = 0
	cfg.BaseURL = "http://localhost:8080/v1"

	raw, err := piAgentProviderConfigJSON(cfg)
	require.NoError(t, err)
	var payload struct {
		Models []map[string]any `json:"models"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &payload))
	require.Len(t, payload.Models, 1)
	assert.NotContains(t, payload.Models[0], "contextWindow")
	assert.NotContains(t, payload.Models[0], "maxTokens")
	// 每个 model 都必须带 cost，避免绑定模型 id 与用户 ~/.pi/agent 撞名时 pi 0.83.0 模型合并
	// 崩溃（provider-composer.js applyModelOverride 读 model.cost.tiers）。
	assert.Equal(t, map[string]any{"input": 0.0, "output": 0.0, "cacheRead": 0.0, "cacheWrite": 0.0},
		payload.Models[0]["cost"])
}

// TestPiAgentProviderExtension_RegistersProviderWithTheConfigJSON 同时守住两条下发路径同源：
// 父进程走 --extension 的扩展源码、子进程走 env 注册表（PiAgentProviderRegistryJSON），
// 两边必须是同一份 config —— 任何一边少字段，子进程的注册就与父进程不一致。
func TestPiAgentProviderExtension_RegistersProviderWithTheConfigJSON(t *testing.T) {
	cfg := testProviderConfig(string(llm_provider_entity.TypeAnthropic), "claude-sonnet-4")
	cfg.APIKey = "sk-plaintext-secret"

	config, err := piAgentProviderConfigJSON(cfg)
	require.NoError(t, err)
	src, err := PiAgentProviderExtension(cfg)
	require.NoError(t, err)

	assert.Equal(t,
		`export default function (pi) { pi.registerProvider("agentre-`+testProviderKey+`", `+config+`) }`,
		src)
	assert.NotContains(t, src, "sk-plaintext-secret")

	registry, err := piAgentProviderRegistryJSON(cfg)
	require.NoError(t, err)
	assert.JSONEq(t, `{"agentre-`+testProviderKey+`": `+config+`}`, registry)
}

func TestPiAgentProviderRegistryJSON_KeysConfigByAgentreProviderName(t *testing.T) {
	cfg := testProviderConfig(string(llm_provider_entity.TypeOpenAIResponse), "deepseek-flash")

	raw, err := piAgentProviderRegistryJSON(cfg)
	require.NoError(t, err)

	var registry map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(raw), &registry))
	require.Len(t, registry, 1)
	config, ok := registry["agentre-"+testProviderKey]
	require.True(t, ok, "注册表键必须与 --model 的 provider 段一致")
	expected, err := piAgentProviderConfigJSON(cfg)
	require.NoError(t, err)
	assert.JSONEq(t, expected, string(config))
}

func TestPiAgentProviderExtension_Errors(t *testing.T) {
	base := testProviderConfig(string(llm_provider_entity.TypeAnthropic), "m")

	t.Run("未知 Type", func(t *testing.T) {
		cfg := *base
		cfg.ProviderType = "deepseek"
		_, err := PiAgentProviderExtension(&cfg)
		require.Error(t, err)
	})

	t.Run("ModelID 为空", func(t *testing.T) {
		cfg := *base
		cfg.ModelID = ""
		_, err := PiAgentProviderExtension(&cfg)
		require.Error(t, err)
	})

	t.Run("nil config", func(t *testing.T) {
		_, err := PiAgentProviderExtension(nil)
		require.Error(t, err)
	})
}

func TestBuildPiAgentProviderEnv(t *testing.T) {
	base := map[string]string{"FOO": "bar", "AGENTRE_PI_MCP_CONFIG": "/tmp/cfg.json"}
	cfg := testProviderConfig(string(llm_provider_entity.TypeAnthropic), "m")
	cfg.APIKey = "sk-secret-123"
	env := BuildPiAgentProviderEnv(base, cfg)
	// 含 base 全部键，新增 env 键 → APIKey。
	assert.Equal(t, "bar", env["FOO"])
	assert.Equal(t, "/tmp/cfg.json", env["AGENTRE_PI_MCP_CONFIG"])
	assert.Equal(t, "sk-secret-123", env["AGENTRE_PI_API_KEY_9a3b2c1d4e5f6a7b8c9d0e1f2a3b4c5d"])
	// 子进程继承不到父进程的 --extension（pi 只显式接受 --extension），provider 定义只能靠
	// 这个注册表 env 到达 subagent；与扩展源码同源、同样只带 $ENV 引用。
	expectedConfig, err := piAgentProviderConfigJSON(cfg)
	require.NoError(t, err)
	assert.JSONEq(t, `{"agentre-`+testProviderKey+`": `+expectedConfig+`}`, env[PiAgentProviderRegistryEnvKey])
	assert.NotContains(t, env[PiAgentProviderRegistryEnvKey], "sk-secret-123")
	// 不改入参 map。
	assert.NotContains(t, base, "AGENTRE_PI_API_KEY_9a3b2c1d4e5f6a7b8c9d0e1f2a3b4c5d")
	assert.NotContains(t, base, PiAgentProviderRegistryEnvKey)

	// nil config 退化为纯副本，不注入 env 键。
	envNil := BuildPiAgentProviderEnv(base, nil)
	assert.Equal(t, "bar", envNil["FOO"])
	assert.NotContains(t, envNil, "AGENTRE_PI_API_KEY_")
	assert.NotContains(t, envNil, PiAgentProviderRegistryEnvKey)
}

func TestBuildPiAgentProviderEnv_UnresolvedModelInjectsAPIKeyWithoutRegistry(t *testing.T) {
	// 没解析出 ModelID 时没有可注册的模型（注册表里的 provider 必须带 models），只注入
	// APIKey —— 这种配置本来也不会下发 agentre-<key>/<model> 给子进程。
	cfg := testProviderConfig(string(llm_provider_entity.TypeOpenAIChat), "")
	cfg.APIKey = "sk-secret-123"

	env := BuildPiAgentProviderEnv(map[string]string{"FOO": "bar"}, cfg)
	assert.Equal(t, "sk-secret-123", env[PiAgentProviderEnvKey(testProviderKey)])
	assert.NotContains(t, env, PiAgentProviderRegistryEnvKey)
}

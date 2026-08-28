package agent_backend_svc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/cago-frame/agents/provider"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo/mock_llm_provider_repo"
)

func TestBuildClaudeCodeEnv(t *testing.T) {
	t.Run("有 gateway + LLM provider 时注入 ANTHROPIC_* + AGENTRE_GATEWAY_* + 用户 env_json", func(t *testing.T) {
		b := &agent_backend_entity.AgentBackend{
			LLMProviderKey: "key-7", // 绑了 provider → claude CLI 该走 gateway 转发
			ModelRoutes:    `{"OPUS":{"providerKey":"key-opus"}}`,
			EnvJSON:        `{"ANTHROPIC_LOG":"info"}`,
		}
		env, err := buildClaudeCodeEnv(b, ProbeDeps{
			Token:      "tok-1",
			GatewayURL: "http://127.0.0.1:60080",
		})
		assert.NoError(t, err)
		assert.Equal(t, "http://127.0.0.1:60080", env["ANTHROPIC_BASE_URL"])
		assert.Equal(t, "tok-1", env["ANTHROPIC_AUTH_TOKEN"])
		// 不写 ANTHROPIC_API_KEY：x-api-key 与 OAuth 路径冲突，订阅模式下会被吞；
		// AUTH_TOKEN 走 Bearer，是 anthropic 文档专门压 OAuth 的覆盖位。
		_, hasKey := env["ANTHROPIC_API_KEY"]
		assert.False(t, hasKey, "ANTHROPIC_API_KEY 不应再写入；语义重叠且有版本会被 OAuth 抢")
		assert.Equal(t, "opus", env["ANTHROPIC_DEFAULT_OPUS_MODEL"])
		assert.Equal(t, "info", env["ANTHROPIC_LOG"])
		// PostToolUse hook 子进程的 env 通道：跟 LLMProviderKey 无关,有 gateway+token 就设。
		assert.Equal(t, "http://127.0.0.1:60080", env["AGENTRE_GATEWAY_URL"])
		assert.Equal(t, "tok-1", env["AGENTRE_GATEWAY_TOKEN"])
	})

	// OPUS/SONNET/HAIKU 三条 alias 字面量是 gateway 端识别的契约；
	// 改名（比如把字面量从 "sonnet" 改成 "sonnet-4"）应立刻让测试红。
	t.Run("OPUS/SONNET/HAIKU 三路 alias 都注入对应的字面量", func(t *testing.T) {
		b := &agent_backend_entity.AgentBackend{
			LLMProviderKey: "key-1",
			ModelRoutes:    `{"OPUS":{"providerKey":"key-opus"},"SONNET":{"providerKey":"key-sonnet"},"HAIKU":{"providerKey":"key-haiku"}}`,
		}
		env, err := buildClaudeCodeEnv(b, ProbeDeps{
			Token:      "tok",
			GatewayURL: "http://127.0.0.1:1",
		})
		assert.NoError(t, err)
		assert.Equal(t, "opus", env["ANTHROPIC_DEFAULT_OPUS_MODEL"])
		assert.Equal(t, "sonnet", env["ANTHROPIC_DEFAULT_SONNET_MODEL"])
		assert.Equal(t, "haiku", env["ANTHROPIC_DEFAULT_HAIKU_MODEL"])
	})

	t.Run("model_routes 不含 alias 时不注入 ANTHROPIC_DEFAULT_* 系列", func(t *testing.T) {
		b := &agent_backend_entity.AgentBackend{LLMProviderKey: "key-1"}
		env, err := buildClaudeCodeEnv(b, ProbeDeps{
			Token:      "tok",
			GatewayURL: "http://127.0.0.1:1",
		})
		assert.NoError(t, err)
		for _, k := range []string{
			"ANTHROPIC_DEFAULT_OPUS_MODEL",
			"ANTHROPIC_DEFAULT_SONNET_MODEL",
			"ANTHROPIC_DEFAULT_HAIKU_MODEL",
		} {
			_, has := env[k]
			assert.Falsef(t, has, "alias 表为空时不应注入 %s", k)
		}
	})

	t.Run("无 gateway 时不写 BASE_URL/API_KEY，让 CLI 走自身登录", func(t *testing.T) {
		b := &agent_backend_entity.AgentBackend{
			LLMProviderKey: "key-1",
			EnvJSON:        `{"ANTHROPIC_LOG":"debug"}`,
		}
		env, err := buildClaudeCodeEnv(b, ProbeDeps{})
		assert.NoError(t, err)
		_, hasBase := env["ANTHROPIC_BASE_URL"]
		_, hasKey := env["ANTHROPIC_API_KEY"]
		_, hasAuth := env["ANTHROPIC_AUTH_TOKEN"]
		assert.False(t, hasBase, "no gateway 不应注入 ANTHROPIC_BASE_URL")
		assert.False(t, hasKey, "no gateway 不应注入 ANTHROPIC_API_KEY")
		assert.False(t, hasAuth, "no gateway 不应注入 ANTHROPIC_AUTH_TOKEN")
		assert.Equal(t, "debug", env["ANTHROPIC_LOG"])
	})

	// 现象：用户 `claude login` 登过订阅账号后，CLI 在订阅模式下会优先用 OAuth session，
	// 单独写 ANTHROPIC_API_KEY 压不住，请求直接打到 anthropic.com 绕过 gateway。
	// 必须改用 ANTHROPIC_AUTH_TOKEN（走 `Authorization: Bearer <token>`），
	// 这才是 anthropic 文档里压 OAuth、转向自定义代理的标准开关。
	t.Run("有 gateway 时用 AUTH_TOKEN 走 Bearer 压 OAuth，不写 API_KEY 避免 header 冲突", func(t *testing.T) {
		b := &agent_backend_entity.AgentBackend{LLMProviderKey: "key-1"}
		env, err := buildClaudeCodeEnv(b, ProbeDeps{
			Token:      "tok-bearer",
			GatewayURL: "http://127.0.0.1:60080",
		})
		assert.NoError(t, err)
		assert.Equal(t, "tok-bearer", env["ANTHROPIC_AUTH_TOKEN"],
			"必须设 ANTHROPIC_AUTH_TOKEN 压住订阅 OAuth；只写 API_KEY 在订阅模式下会被 CLI 忽略")
		_, hasKey := env["ANTHROPIC_API_KEY"]
		assert.False(t, hasKey,
			"不要再写 ANTHROPIC_API_KEY：与 AUTH_TOKEN 语义重叠；同时下发会让上游收到两条冲突的 auth header")
		assert.Equal(t, "http://127.0.0.1:60080", env["ANTHROPIC_BASE_URL"])
	})
}

// modelCapturingProvider 实现 provider.Provider，单纯记录 agent loop 真正发出的
// CompletionRequest.Model。一旦 builtinProber 没把 LLMProvider.Model 透传过来，
// 这里读到的就是空串，意味着真正打到 LLM 的请求会被 anthropic / openai 以 model
// 字段为空为由直接 400，等价于"看似有调用、实际啥都没跑成"的 200ms 现象。
type modelCapturingProvider struct {
	mu        sync.Mutex
	seenModel string
}

func (p *modelCapturingProvider) Name() string { return "fake" }

func (p *modelCapturingProvider) ChatCompletion(_ context.Context, _ *provider.CompletionRequest) (*provider.CompletionResponse, error) {
	return nil, errors.New("modelCapturingProvider: ChatCompletion not used by builtinProber")
}

func (p *modelCapturingProvider) ChatStream(_ context.Context, req *provider.CompletionRequest) (<-chan provider.StreamChunk, error) {
	p.mu.Lock()
	if p.seenModel == "" {
		p.seenModel = req.Model
	}
	p.mu.Unlock()

	ch := make(chan provider.StreamChunk, 2)
	ch <- provider.StreamChunk{ContentDelta: "pong"}
	// 直接 close → loop.go 把 partial 收成 StopEndTurn 的 assistant turn，
	// builtinProber.lastAssistantText 即能拿到 "pong"。
	close(ch)
	return ch, nil
}

func (p *modelCapturingProvider) Model() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seenModel
}

func TestBuildPiAgentProbeModel(t *testing.T) {
	Convey("Given a pi-agent backend using ~/.pi/agent config", t, func() {
		Convey("When testing with reasoning_effort, then Agentre leaves model empty so pi uses user defaultProvider/defaultModel and thinking stays separate", func() {
			model := buildPiAgentProbeModel(&agent_backend_entity.AgentBackend{
				Type:            string(agent_backend_entity.TypePiAgent),
				ReasoningEffort: "high",
			})

			So(model, ShouldEqual, "")
		})
	})
}

// TestResolveCLIProbeModel 锁住「Test 连通性下发的模型」与 chat-path ccBuildClientOpts
// 同优先级,避免 Test 与实际 chat run 漂移（agent-backend.md §2.3 不变量）：
// provider/gateway 模型(deps.Model)→ claudecode 后端 DefaultModel → ""(CLI 默认)。
func TestResolveCLIProbeModel(t *testing.T) {
	cc := func(model string) *agent_backend_entity.AgentBackend {
		return &agent_backend_entity.AgentBackend{
			Type:         string(agent_backend_entity.TypeClaudeCode),
			DefaultModel: model,
		}
	}

	Convey("claudecode CLI 登录态(deps.Model 空) → 用后端 DefaultModel", t, func() {
		So(resolveCLIProbeModel(cc("claude-fable-5"), ProbeDeps{}), ShouldEqual, "claude-fable-5")
	})
	Convey("claudecode 绑 provider(deps.Model 非空) → provider 优先,忽略 DefaultModel", t, func() {
		So(resolveCLIProbeModel(cc("claude-fable-5"), ProbeDeps{Model: "glm-5.1"}), ShouldEqual, "glm-5.1")
	})
	Convey("claudecode 两者皆空 → 空(走 CLI 默认)", t, func() {
		So(resolveCLIProbeModel(cc(""), ProbeDeps{}), ShouldEqual, "")
	})
	Convey("claudecode DefaultModel 只有空白 → trim 后空", t, func() {
		So(resolveCLIProbeModel(cc("   "), ProbeDeps{}), ShouldEqual, "")
	})
	Convey("codex 不吃 DefaultModel 兜底(实体层本就禁止),deps 空 → 空", t, func() {
		codex := &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex)}
		So(resolveCLIProbeModel(codex, ProbeDeps{}), ShouldEqual, "")
	})
	Convey("piagent → buildPiAgentProbeModel(当前为空)", t, func() {
		pi := &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)}
		So(resolveCLIProbeModel(pi, ProbeDeps{Model: "ignored"}), ShouldEqual, "")
	})
}

func TestBuiltinProber_PassesProviderModelToAgent(t *testing.T) {
	// 现象：用户在前端点「测试连接」200ms 就完成、对应 LLM 后台看不到调用。
	// 根因之一：builtinProber 调 coding.New(ctx, prov, cwd) 没传 coding.WithModel(p.Model)，
	// 父 agent 的 model 字段是空字符串；anthropic / openai 收到空 model 字段会直接 400 而不
	// 计入正常调用记录。该测试通过 fake provider 捕获 agent loop 实际派发的
	// CompletionRequest.Model，验证它必须等于 LLMProvider.Model。
	t.Run("把 LLMProvider.Model 传到底层 agent 的 ChatStream req.Model", func(t *testing.T) {
		ctx := context.Background()
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		repoMock := mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl)
		llm_provider_repo.RegisterLLMProvider(repoMock)
		entity := &llm_provider_entity.LLMProvider{
			ProviderKey:     "key-11",
			Type:            string(llm_provider_entity.TypeAnthropic),
			Name:            "production",
			Enabled:         llm_provider_entity.EnabledOn,
			DefaultModelKey: "mk-11",
			APIKey:          "sk-test",
			BaseURL:         "http://127.0.0.1:0",
			Status:          consts.ACTIVE,
		}
		repoMock.EXPECT().FindByKey(gomock.Any(), "key-11").Return(entity, nil).AnyTimes()
		repoMock.EXPECT().FindModelByKey(gomock.Any(), "mk-11").Return(
			&llm_provider_model_entity.LLMProviderModel{ModelKey: "mk-11", ModelID: "claude-sonnet-4-6", Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE},
			nil).AnyTimes()

		capture := &modelCapturingProvider{}
		origBuilder := providerBuilder
		providerBuilder = func(_ *llm_provider_entity.LLMProvider) (provider.Provider, error) {
			return capture, nil
		}
		t.Cleanup(func() { providerBuilder = origBuilder })

		reply, err := builtinProber{}.Run(ctx, &agent_backend_entity.AgentBackend{
			ID:             1,
			Type:           string(agent_backend_entity.TypeBuiltin),
			Name:           "默认助手",
			LLMProviderKey: "key-11",
		}, ProbeDeps{})

		assert.NoError(t, err)
		assert.Equal(t, "pong", strings.TrimSpace(reply))
		assert.Equal(t, "claude-sonnet-4-6", capture.Model(),
			"builtinProber 必须把解析出的 ModelID 注入 coding.WithModel；空 model 会让真实 LLM 调用以 400 失败")
	})
}

func TestBuildPiAgentProviderProbe(t *testing.T) {
	Convey("Given a piagent backend bound to an active provider", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		repoMock := mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl)
		llm_provider_repo.RegisterLLMProvider(repoMock)
		t.Setenv("AGENTRE_DATA_DIR", t.TempDir())

		p := &llm_provider_entity.LLMProvider{
			ProviderKey:     "key-pi",
			Type:            string(llm_provider_entity.TypeOpenAIChat),
			Name:            "PiProvider",
			Enabled:         llm_provider_entity.EnabledOn,
			DefaultModelKey: "mk-pi",
			APIKey:          "sk-pi-1",
			BaseURL:         "https://pi.example",
			Status:          consts.ACTIVE,
		}
		repoMock.EXPECT().FindByKey(gomock.Any(), "key-pi").Return(p, nil).AnyTimes()
		repoMock.EXPECT().FindModelByKey(gomock.Any(), "mk-pi").Return(
			&llm_provider_model_entity.LLMProviderModel{ModelKey: "mk-pi", ModelID: "deepseek-v3", Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE},
			nil).AnyTimes()

		Convey("When assembling the probe params Then the provider extension is materialized, env gets the API key and model is agentre-<key>/<model>", func() {
			exts, envOut, modelOut, err := buildPiAgentProviderProbe(context.Background(), &agent_backend_entity.AgentBackend{
				Type:           string(agent_backend_entity.TypePiAgent),
				LLMProviderKey: "key-pi",
			}, map[string]string{"BASE": "1"}, "")

			So(err, ShouldBeNil)
			So(modelOut, ShouldEqual, "agentre-key-pi/deepseek-v3")
			So(len(exts), ShouldEqual, 1)
			ext := exts[0]
			So(filepath.Base(ext), ShouldStartWith, "agentre-provider-")
			So(filepath.Base(ext), ShouldEndWith, ".mjs")
			_, statErr := os.Stat(ext)
			So(statErr, ShouldBeNil)
			// env 在 buildPiAgentEnv 产出的 base 之上叠加 APIKey，不改入参 map。
			So(envOut["BASE"], ShouldEqual, "1")
			So(envOut["AGENTRE_PI_API_KEY_keypi"], ShouldEqual, "sk-pi-1")
			// 决策 #4：扩展文件只含 $ENV_VAR 引用，绝不含明文 APIKey。
			raw, readErr := os.ReadFile(ext) //nolint:gosec // test-owned temp path
			So(readErr, ShouldBeNil)
			So(strings.Contains(string(raw), "sk-pi-1"), ShouldBeFalse)
		})
	})

	Convey("Given a piagent backend pinned to a fixed model", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		repoMock := mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl)
		llm_provider_repo.RegisterLLMProvider(repoMock)
		t.Setenv("AGENTRE_DATA_DIR", t.TempDir())

		p := &llm_provider_entity.LLMProvider{
			ProviderKey:     "key-pi",
			Type:            string(llm_provider_entity.TypeOpenAIChat),
			Name:            "PiProvider",
			Enabled:         llm_provider_entity.EnabledOn,
			DefaultModelKey: "mk-default",
			APIKey:          "sk-pi-1",
			BaseURL:         "https://pi.example",
			Status:          consts.ACTIVE,
		}
		repoMock.EXPECT().FindByKey(gomock.Any(), "key-pi").Return(p, nil).AnyTimes()
		// 固定模型解析走 mk-fixed，而不是 provider 默认 mk-default。
		repoMock.EXPECT().FindModelByKey(gomock.Any(), "mk-fixed").Return(
			&llm_provider_model_entity.LLMProviderModel{ModelKey: "mk-fixed", ModelID: "deepseek-r2", Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE},
			nil).AnyTimes()
		repoMock.EXPECT().FindModelByKey(gomock.Any(), "mk-default").Return(
			&llm_provider_model_entity.LLMProviderModel{ModelKey: "mk-default", ModelID: "deepseek-v3", Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE},
			nil).AnyTimes()

		Convey("When assembling the probe params Then the fixed model (not the provider default) is resolved and probed", func() {
			_, _, modelOut, err := buildPiAgentProviderProbe(context.Background(), &agent_backend_entity.AgentBackend{
				Type:           string(agent_backend_entity.TypePiAgent),
				LLMProviderKey: "key-pi",
				LLMModelKey:    "mk-fixed",
			}, map[string]string{"BASE": "1"}, "")
			So(err, ShouldBeNil)
			So(modelOut, ShouldEqual, "agentre-key-pi/deepseek-r2")
		})
	})

	Convey("Given an unbound piagent backend", t, func() {
		Convey("When assembling the probe params Then everything stays as today: no extensions, no API key env, model untouched", func() {
			env := map[string]string{"BASE": "1"}
			exts, envOut, modelOut, err := buildPiAgentProviderProbe(context.Background(), &agent_backend_entity.AgentBackend{
				Type: string(agent_backend_entity.TypePiAgent),
			}, env, "")

			So(err, ShouldBeNil)
			So(exts, ShouldBeNil)
			So(modelOut, ShouldEqual, "")
			So(envOut, ShouldResemble, env)
		})
	})

	Convey("Given a piagent backend bound to a missing or inactive provider", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		repoMock := mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl)
		llm_provider_repo.RegisterLLMProvider(repoMock)
		t.Setenv("AGENTRE_DATA_DIR", t.TempDir())

		Convey("When the provider is missing Then an error is returned", func() {
			repoMock.EXPECT().FindByKey(gomock.Any(), "key-pi").Return(nil, nil)
			_, _, _, err := buildPiAgentProviderProbe(context.Background(), &agent_backend_entity.AgentBackend{
				Type:           string(agent_backend_entity.TypePiAgent),
				LLMProviderKey: "key-pi",
			}, map[string]string{}, "")
			So(err, ShouldNotBeNil)
		})

		Convey("When the provider is inactive Then an error is returned", func() {
			inactive := &llm_provider_entity.LLMProvider{
				ProviderKey: "key-pi",
				Type:        string(llm_provider_entity.TypeOpenAIChat),
				Name:        "PiProvider",
				APIKey:      "sk-pi-1",
				BaseURL:     "https://pi.example",
				Status:      consts.DELETE,
			}
			repoMock.EXPECT().FindByKey(gomock.Any(), "key-pi").Return(inactive, nil)
			_, _, _, err := buildPiAgentProviderProbe(context.Background(), &agent_backend_entity.AgentBackend{
				Type:           string(agent_backend_entity.TypePiAgent),
				LLMProviderKey: "key-pi",
			}, map[string]string{}, "")
			So(err, ShouldNotBeNil)
		})
	})

	Convey("Given a piagent backend bound to a provider with an empty APIKey", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		repoMock := mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl)
		llm_provider_repo.RegisterLLMProvider(repoMock)
		t.Setenv("AGENTRE_DATA_DIR", t.TempDir())

		p := &llm_provider_entity.LLMProvider{
			ProviderKey: "key-pi",
			Type:        string(llm_provider_entity.TypeOpenAIChat),
			Name:        "PiProvider",
			APIKey:      "",
			BaseURL:     "https://pi.example",
			Status:      consts.ACTIVE,
		}
		repoMock.EXPECT().FindByKey(gomock.Any(), "key-pi").Return(p, nil)

		Convey("When assembling the probe params Then a config error naming the provider is returned (no spawn, mirrors runtime)", func() {
			_, _, _, err := buildPiAgentProviderProbe(context.Background(), &agent_backend_entity.AgentBackend{
				Type:           string(agent_backend_entity.TypePiAgent),
				LLMProviderKey: "key-pi",
			}, map[string]string{}, "")
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "key-pi")
		})
	})
}

func TestBuildCodexEnv(t *testing.T) {
	t.Run("有 gateway 时只注入 API_KEY + 用户 env_json", func(t *testing.T) {
		b := &agent_backend_entity.AgentBackend{
			EnvJSON: `{"OPENAI_ORGANIZATION":"acme"}`,
		}
		env, err := buildCodexEnv(b, ProbeDeps{
			Token:      "tok-2",
			GatewayURL: "http://127.0.0.1:60080",
		})
		assert.NoError(t, err)
		assert.Equal(t, "tok-2", env["OPENAI_API_KEY"])
		assert.Equal(t, "acme", env["OPENAI_ORGANIZATION"])
		_, hasBase := env["OPENAI_BASE_URL"]
		assert.False(t, hasBase, "base_url 应由 Codex Config 覆盖，不靠 OPENAI_BASE_URL 环境变量")
	})

	t.Run("无 gateway 时不写 BASE_URL/API_KEY，让 CLI 走自身登录", func(t *testing.T) {
		b := &agent_backend_entity.AgentBackend{
			EnvJSON: `{"OPENAI_ORGANIZATION":"acme"}`,
		}
		env, err := buildCodexEnv(b, ProbeDeps{})
		assert.NoError(t, err)
		_, hasBase := env["OPENAI_BASE_URL"]
		_, hasKey := env["OPENAI_API_KEY"]
		assert.False(t, hasBase)
		assert.False(t, hasKey)
		assert.Equal(t, "acme", env["OPENAI_ORGANIZATION"])
	})
}

// TestEffectiveLLMForProbe 钉死 Test 连通性与 chat run 的装配同源（agent-backend.md
// §2.3 不变量）：探测用配置经共享构造口 agentruntime.NewEffectiveLLMConfig 装配，因此
// Mode 由本轮请求的 ModelKey 决定（backend 钉了固定模型 → fixed-model），且模型元数据
// ContextWindow / MaxOutput 一并带上 —— prober 从前手写这段并把 Mode 恒定写成
// provider-default。
func TestEffectiveLLMForProbe(t *testing.T) {
	newProvider := func() *llm_provider_entity.LLMProvider {
		return &llm_provider_entity.LLMProvider{
			ProviderKey:     "key-pi",
			Type:            string(llm_provider_entity.TypeOpenAIChat),
			Name:            "PiProvider",
			Enabled:         llm_provider_entity.EnabledOn,
			DefaultModelKey: "mk-default",
			APIKey:          "sk-pi-1",
			BaseURL:         "https://pi.example",
			Status:          consts.ACTIVE,
		}
	}
	setup := func(t *testing.T) {
		t.Helper()
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		repoMock := mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl)
		llm_provider_repo.RegisterLLMProvider(repoMock)
		repoMock.EXPECT().FindByKey(gomock.Any(), "key-pi").Return(newProvider(), nil).AnyTimes()
		repoMock.EXPECT().FindModelByKey(gomock.Any(), "mk-default").Return(
			&llm_provider_model_entity.LLMProviderModel{
				ModelKey: "mk-default", ModelID: "deepseek-v3", ContextWindow: 128000, MaxOutput: 8192,
				Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE,
			}, nil).AnyTimes()
		repoMock.EXPECT().FindModelByKey(gomock.Any(), "mk-fixed").Return(
			&llm_provider_model_entity.LLMProviderModel{
				ModelKey: "mk-fixed", ModelID: "deepseek-r2", ContextWindow: 64000, MaxOutput: 4096,
				Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE,
			}, nil).AnyTimes()
	}

	t.Run("provider-default 解析当前默认模型并带上元数据", func(t *testing.T) {
		setup(t)
		cfg, err := effectiveLLMForProbe(context.Background(), newProvider(), "")
		assert.NoError(t, err)
		assert.Equal(t, &agentruntime.EffectiveLLMConfig{
			Mode:          agentruntime.EffectiveModeProviderDefault,
			ProviderKey:   "key-pi",
			ModelKey:      "mk-default",
			ProviderType:  string(llm_provider_entity.TypeOpenAIChat),
			ProviderName:  "PiProvider",
			ModelID:       "deepseek-v3",
			ContextWindow: 128000,
			MaxOutput:     8192,
			BaseURL:       "https://pi.example",
			APIKey:        "sk-pi-1",
			HasAPIKey:     true,
		}, cfg)
	})

	t.Run("固定模型时 Mode 是 fixed-model", func(t *testing.T) {
		setup(t)
		cfg, err := effectiveLLMForProbe(context.Background(), newProvider(), " mk-fixed ")
		assert.NoError(t, err)
		assert.Equal(t, agentruntime.EffectiveModeFixedModel, cfg.Mode)
		assert.Equal(t, "mk-fixed", cfg.ModelKey)
		assert.Equal(t, "deepseek-r2", cfg.ModelID)
		assert.Equal(t, 64000, cfg.ContextWindow)
		assert.Equal(t, 4096, cfg.MaxOutput)
	})
}

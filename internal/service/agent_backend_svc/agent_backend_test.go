package agent_backend_svc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-ai/agentre/internal/daemon/handlers"
	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-ai/agentre/internal/pkg/code"
	"github.com/agentre-ai/agentre/internal/pkg/httpgateway"
	"github.com/agentre-ai/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-ai/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-ai/agentre/internal/repository/agent_repo"
	"github.com/agentre-ai/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-ai/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-ai/agentre/internal/repository/llm_provider_repo/mock_llm_provider_repo"
	"github.com/agentre-ai/agentre/internal/service/remote_device_svc"
	"github.com/agentre-ai/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

func setupSvcTest(t *testing.T) (
	context.Context,
	*mock_agent_backend_repo.MockAgentBackendRepo,
	*mock_llm_provider_repo.MockLLMProviderRepo,
	*mock_agent_repo.MockAgentRepo,
	*mockProber,
	*agentBackendSvc,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	backendMock := mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl)
	providerMock := mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl)
	agentMock := mock_agent_repo.NewMockAgentRepo(ctrl)
	prober := NewmockProber(ctrl)
	agent_backend_repo.RegisterAgentBackend(backendMock)
	llm_provider_repo.RegisterLLMProvider(providerMock)
	agent_repo.RegisterAgent(agentMock)

	svc := &agentBackendSvc{
		now:    func() int64 { return 1234567890 },
		prober: prober,
		probes: map[string]context.CancelFunc{},
	}
	return context.Background(), backendMock, providerMock, agentMock, prober, svc
}

func activeProvider(key string) *llm_provider_entity.LLMProvider {
	return &llm_provider_entity.LLMProvider{
		ProviderKey:     key,
		Type:            string(llm_provider_entity.TypeAnthropic),
		Name:            "Production",
		DefaultModelKey: "model-key-" + key,
		Enabled:         llm_provider_entity.EnabledOn,
		Status:          consts.ACTIVE,
	}
}

func activeProviderWithType(key string, typ llm_provider_entity.ProviderType) *llm_provider_entity.LLMProvider {
	p := activeProvider(key)
	p.Type = string(typ)
	return p
}

// activeDefaultModel 返回 provider 的启用默认模型记录（ModelID 与既有展示断言一致）。
func activeDefaultModel(providerKey string) *llm_provider_model_entity.LLMProviderModel {
	return defaultModelWithState(providerKey, "claude-sonnet-4-6", llm_provider_model_entity.EnabledOn)
}

// defaultModelWithState 构造一条属于 provider 的默认模型记录；enabled 由调用方决定，
// 用于覆盖“默认模型已停用 → 不可解析”的边界。
func defaultModelWithState(providerKey, modelID string, enabled int) *llm_provider_model_entity.LLMProviderModel {
	return &llm_provider_model_entity.LLMProviderModel{
		ModelKey: "model-key-" + providerKey,
		ModelID:  modelID,
		Enabled:  enabled,
		Status:   consts.ACTIVE,
	}
}

// expectDefaultModelResolution 注册 provider 默认模型解析的 FindModelByKey 期望，
// times 反映该路径上会解析的次数（如 piagent 校验 + 展示各一次）。
func expectDefaultModelResolution(providerMock *mock_llm_provider_repo.MockLLMProviderRepo, providerKey string, times int) {
	providerMock.EXPECT().FindModelByKey(gomock.Any(), "model-key-"+providerKey).
		Return(activeDefaultModel(providerKey), nil).Times(times)
}

type fakeBackendGateway struct {
	status httpgateway.GatewayStatus
	url    string
	token  string
}

func (f *fakeBackendGateway) IssueToken(context.Context, *agent_backend_entity.AgentBackend, time.Duration) (string, error) {
	if f.token != "" {
		return f.token, nil
	}
	return "test-token", nil
}

func (f *fakeBackendGateway) RevokeToken(string) {}

func (f *fakeBackendGateway) URL() string {
	if f.url != "" {
		return f.url
	}
	return "http://127.0.0.1:60080"
}

func (f *fakeBackendGateway) Status() httpgateway.GatewayStatus { return f.status }

func TestCreateBackend(t *testing.T) {
	convey.Convey("Create Agent backend", t, func() {
		ctx, backendMock, providerMock, _, _, svc := setupSvcTest(t)

		convey.Convey("成功创建 builtin", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "默认助手").Return(nil, nil)
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil)
			expectDefaultModelResolution(providerMock, "key-1", 1)
			backendMock.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
				DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
					b.ID = 42
					return nil
				})

			resp, err := svc.Create(ctx, &CreateBackendRequest{
				Type:           "builtin",
				Name:           "默认助手",
				LLMProviderKey: "key-1",
			})
			assert.NoError(t, err)
			assert.Equal(t, int64(42), resp.Item.ID)
			assert.Equal(t, "Production", resp.Item.LLMProviderName)
			assert.Equal(t, "claude-sonnet-4-6", resp.Item.LLMProviderModel)
			assert.True(t, resp.Item.LLMProviderActive)
		})

		convey.Convey("名字重复返回错误", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "dup").
				Return(&agent_backend_entity.AgentBackend{ID: 9, Name: "dup"}, nil)

			_, err := svc.Create(ctx, &CreateBackendRequest{
				Type: "builtin", Name: "dup", LLMProviderKey: "key-1",
			})
			assert.Error(t, err)
		})

		convey.Convey("LLM 供应商不存在", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "x").Return(nil, nil)
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-7").Return(nil, nil)

			_, err := svc.Create(ctx, &CreateBackendRequest{
				Type: "builtin", Name: "x", LLMProviderKey: "key-7",
			})
			assert.Error(t, err)
		})

		convey.Convey("LLM 供应商已停用", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "x").Return(nil, nil)
			p := activeProvider("key-1")
			p.Status = consts.DELETE
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(p, nil)

			_, err := svc.Create(ctx, &CreateBackendRequest{
				Type: "builtin", Name: "x", LLMProviderKey: "key-1",
			})
			assert.Error(t, err)
		})

		convey.Convey("成功创建 claudecode", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "cc").Return(nil, nil)
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil)
			expectDefaultModelResolution(providerMock, "key-1", 1)
			backendMock.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
				DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
					assert.Equal(t, string(agent_backend_entity.TypeClaudeCode), b.Type)
					assert.Empty(t, b.CLIPath, "CLI path belongs to a per-device overlay, not backend identity")
					b.ID = 43
					return nil
				})

			resp, err := svc.Create(ctx, &CreateBackendRequest{
				Type:           string(agent_backend_entity.TypeClaudeCode),
				Name:           "cc",
				LLMProviderKey: "key-1",
				CLIPath:        "/usr/local/bin/claude",
			})
			assert.NoError(t, err)
			assert.Equal(t, int64(43), resp.Item.ID)
		})

		convey.Convey("claudecode CLI 登录态保存 default_model", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "cc").Return(nil, nil)
			backendMock.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
				DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
					assert.Equal(t, "", b.LLMProviderKey)
					assert.Equal(t, "claude-fable-5", b.DefaultModel)
					b.ID = 46
					return nil
				})

			resp, err := svc.Create(ctx, &CreateBackendRequest{
				Type:         string(agent_backend_entity.TypeClaudeCode),
				Name:         "cc",
				DefaultModel: "claude-fable-5",
			})
			assert.NoError(t, err)
			assert.Equal(t, "claude-fable-5", resp.Item.DefaultModel)
		})

		convey.Convey("codex provider 类型不匹配", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "codex").Return(nil, nil)
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil)

			_, err := svc.Create(ctx, &CreateBackendRequest{
				Type:           string(agent_backend_entity.TypeCodex),
				Name:           "codex",
				LLMProviderKey: "key-1",
			})
			assert.Error(t, err)
		})

		convey.Convey("成功创建 codex", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "codex").Return(nil, nil)
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-2").
				Return(activeProviderWithType("key-2", llm_provider_entity.TypeOpenAIResponse), nil)
			expectDefaultModelResolution(providerMock, "key-2", 1)
			backendMock.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
				DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
					assert.Equal(t, string(agent_backend_entity.TypeCodex), b.Type)
					assert.Equal(t, "workspace-write", b.Sandbox)
					assert.Equal(t, "on-request", b.Approval)
					b.ID = 44
					return nil
				})

			resp, err := svc.Create(ctx, &CreateBackendRequest{
				Type:           string(agent_backend_entity.TypeCodex),
				Name:           "codex",
				LLMProviderKey: "key-2",
				Sandbox:        "workspace-write",
				Approval:       "on-request",
			})
			assert.NoError(t, err)
			assert.Equal(t, int64(44), resp.Item.ID)
		})

		convey.Convey("type 未知", func() {
			_, err := svc.Create(ctx, &CreateBackendRequest{
				Type: "foo", Name: "x", LLMProviderKey: "key-1",
			})
			assert.Error(t, err)
		})

		convey.Convey("claudecode 不关联供应商可成功创建", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "cc").Return(nil, nil)
			backendMock.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
				DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
					assert.Equal(t, "", b.LLMProviderKey)
					b.ID = 45
					return nil
				})

			resp, err := svc.Create(ctx, &CreateBackendRequest{
				Type:           string(agent_backend_entity.TypeClaudeCode),
				Name:           "cc",
				LLMProviderKey: "",
			})
			assert.NoError(t, err)
			assert.Equal(t, int64(45), resp.Item.ID)
			assert.Equal(t, "", resp.Item.LLMProviderName)
			assert.False(t, resp.Item.LLMProviderActive)
		})

		convey.Convey("pi-agent 不关联供应商可成功创建", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "pi").Return(nil, nil)
			backendMock.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
				DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
					assert.Equal(t, string(agent_backend_entity.TypePiAgent), b.Type)
					assert.Equal(t, "", b.LLMProviderKey)
					b.ID = 47
					return nil
				})

			resp, err := svc.Create(ctx, &CreateBackendRequest{
				Type:           string(agent_backend_entity.TypePiAgent),
				Name:           "pi",
				LLMProviderKey: "",
			})
			assert.NoError(t, err)
			assert.Equal(t, int64(47), resp.Item.ID)
		})

		convey.Convey("pi-agent 绑定三类 provider 可成功创建", func() {
			for _, typ := range []llm_provider_entity.ProviderType{
				llm_provider_entity.TypeAnthropic,
				llm_provider_entity.TypeOpenAIChat,
				llm_provider_entity.TypeOpenAIResponse,
			} {
				backendMock.EXPECT().FindByName(gomock.Any(), "pi").Return(nil, nil)
				providerMock.EXPECT().FindByKey(gomock.Any(), "key-4").
					Return(activeProviderWithType("key-4", typ), nil)
				// piagent 校验（requireMatchingProvider）+ 展示（toItem）各解析一次默认模型。
				expectDefaultModelResolution(providerMock, "key-4", 2)
				backendMock.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
					DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
						assert.Equal(t, string(agent_backend_entity.TypePiAgent), b.Type)
						assert.Equal(t, "key-4", b.LLMProviderKey)
						b.ID = 48
						return nil
					})

				resp, err := svc.Create(ctx, &CreateBackendRequest{
					Type:           string(agent_backend_entity.TypePiAgent),
					Name:           "pi",
					LLMProviderKey: "key-4",
				})
				assert.NoError(t, err)
				assert.Equal(t, int64(48), resp.Item.ID)
			}
		})

		convey.Convey("pi-agent 绑定 provider 但默认模型为空 → AgentBackendProviderModelRequired", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "pi").Return(nil, nil)
			p := activeProviderWithType("key-4", llm_provider_entity.TypeOpenAIResponse)
			p.DefaultModelKey = ""
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-4").Return(p, nil)

			_, err := svc.Create(ctx, &CreateBackendRequest{
				Type:           string(agent_backend_entity.TypePiAgent),
				Name:           "pi",
				LLMProviderKey: "key-4",
			})
			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, code.AgentBackendProviderModelRequired, httpErr.Code)
		})

		convey.Convey("pi-agent 绑定 provider 但默认模型已停用 → AgentBackendProviderModelRequired", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "pi").Return(nil, nil)
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-4").
				Return(activeProviderWithType("key-4", llm_provider_entity.TypeOpenAIResponse), nil)
			providerMock.EXPECT().FindModelByKey(gomock.Any(), "model-key-key-4").
				Return(defaultModelWithState("key-4", "claude-sonnet-4-6", llm_provider_model_entity.EnabledOff), nil)

			_, err := svc.Create(ctx, &CreateBackendRequest{
				Type:           string(agent_backend_entity.TypePiAgent),
				Name:           "pi",
				LLMProviderKey: "key-4",
			})
			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, code.AgentBackendProviderModelRequired, httpErr.Code)
		})

		convey.Convey("claudecode 绑定无默认模型的 provider 仍可创建", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "cc").Return(nil, nil)
			p := activeProvider("key-1")
			p.DefaultModelKey = ""
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(p, nil)
			backendMock.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
				DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
					assert.Equal(t, string(agent_backend_entity.TypeClaudeCode), b.Type)
					b.ID = 49
					return nil
				})

			resp, err := svc.Create(ctx, &CreateBackendRequest{
				Type:           string(agent_backend_entity.TypeClaudeCode),
				Name:           "cc",
				LLMProviderKey: "key-1",
			})
			assert.NoError(t, err)
			assert.Equal(t, int64(49), resp.Item.ID)
			// 展示侧：无默认模型 → 解析结果为空（目标已失效）。
			assert.Equal(t, "", resp.Item.LLMProviderModel)
		})

		convey.Convey("codex 绑定无默认模型的 provider 仍可创建", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "codex").Return(nil, nil)
			p := activeProviderWithType("key-2", llm_provider_entity.TypeOpenAIResponse)
			p.DefaultModelKey = ""
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-2").Return(p, nil)
			backendMock.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
				DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
					assert.Equal(t, string(agent_backend_entity.TypeCodex), b.Type)
					b.ID = 50
					return nil
				})

			resp, err := svc.Create(ctx, &CreateBackendRequest{
				Type:           string(agent_backend_entity.TypeCodex),
				Name:           "codex",
				LLMProviderKey: "key-2",
			})
			assert.NoError(t, err)
			assert.Equal(t, int64(50), resp.Item.ID)
		})

		convey.Convey("claudecode 绑定 fixed-model（ProviderKey+ModelKey）可创建，展示解析到指定模型", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "cc-fixed").Return(nil, nil)
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil)
			// 校验（requireOwnedEnabledModel）+ 展示（effectiveModelID）各解析一次固定模型。
			providerMock.EXPECT().FindModelByKey(gomock.Any(), "mk-fixed-1").
				Return(&llm_provider_model_entity.LLMProviderModel{
					ModelKey: "mk-fixed-1", ModelID: "claude-opus-4-8",
					Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE,
				}, nil).Times(2)
			backendMock.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
				DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
					assert.Equal(t, "key-1", b.LLMProviderKey)
					assert.Equal(t, "mk-fixed-1", b.LLMModelKey)
					b.ID = 51
					return nil
				})

			resp, err := svc.Create(ctx, &CreateBackendRequest{
				Type:           string(agent_backend_entity.TypeClaudeCode),
				Name:           "cc-fixed",
				LLMProviderKey: "key-1",
				LLMModelKey:    "mk-fixed-1",
			})
			assert.NoError(t, err)
			assert.Equal(t, "claude-opus-4-8", resp.Item.LLMProviderModel,
				"fixed-model 展示必须解析到指定模型，而不是默认模型")
		})

		convey.Convey("fixed-model 引用的模型不存在 → LLMProviderModelNotFound", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "cc").Return(nil, nil)
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil)
			providerMock.EXPECT().FindModelByKey(gomock.Any(), "mk-gone").Return(nil, nil)

			_, err := svc.Create(ctx, &CreateBackendRequest{
				Type:           string(agent_backend_entity.TypeClaudeCode),
				Name:           "cc",
				LLMProviderKey: "key-1",
				LLMModelKey:    "mk-gone",
			})
			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, code.LLMProviderModelNotFound, httpErr.Code)
		})

		convey.Convey("fixed-model 引用的模型不属于该 provider → LLMProviderModelNotOwned", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "cc").Return(nil, nil)
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil)
			providerMock.EXPECT().FindModelByKey(gomock.Any(), "mk-foreign").
				Return(&llm_provider_model_entity.LLMProviderModel{
					ModelKey: "mk-foreign", ModelID: "m", ProviderID: 9999,
					Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE,
				}, nil)

			_, err := svc.Create(ctx, &CreateBackendRequest{
				Type:           string(agent_backend_entity.TypeClaudeCode),
				Name:           "cc",
				LLMProviderKey: "key-1",
				LLMModelKey:    "mk-foreign",
			})
			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, code.LLMProviderModelNotOwned, httpErr.Code)
		})

		convey.Convey("fixed-model 引用的模型已停用 → LLMProviderModelDisabled", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "cc").Return(nil, nil)
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil)
			providerMock.EXPECT().FindModelByKey(gomock.Any(), "mk-off").
				Return(&llm_provider_model_entity.LLMProviderModel{
					ModelKey: "mk-off", ModelID: "m",
					Enabled: llm_provider_model_entity.EnabledOff, Status: consts.ACTIVE,
				}, nil)

			_, err := svc.Create(ctx, &CreateBackendRequest{
				Type:           string(agent_backend_entity.TypeClaudeCode),
				Name:           "cc",
				LLMProviderKey: "key-1",
				LLMModelKey:    "mk-off",
			})
			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, code.LLMProviderModelDisabled, httpErr.Code)
		})

		convey.Convey("pi-agent 绑定 fixed-model 且解析到模型时无需默认模型（默认可空）", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "pi-fixed").Return(nil, nil)
			p := activeProviderWithType("key-4", llm_provider_entity.TypeOpenAIResponse)
			p.DefaultModelKey = "" // 默认模型为空，但固定模型已钉住 → 仍可创建。
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-4").Return(p, nil)
			providerMock.EXPECT().FindModelByKey(gomock.Any(), "mk-fixed-pi").
				Return(&llm_provider_model_entity.LLMProviderModel{
					ModelKey: "mk-fixed-pi", ModelID: "gpt-fixed",
					Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE,
				}, nil).Times(2)
			backendMock.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
				DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
					assert.Equal(t, "mk-fixed-pi", b.LLMModelKey)
					b.ID = 52
					return nil
				})

			resp, err := svc.Create(ctx, &CreateBackendRequest{
				Type:           string(agent_backend_entity.TypePiAgent),
				Name:           "pi-fixed",
				LLMProviderKey: "key-4",
				LLMModelKey:    "mk-fixed-pi",
			})
			assert.NoError(t, err)
			assert.Equal(t, "gpt-fixed", resp.Item.LLMProviderModel)
		})

		convey.Convey("claudecode 类型化 route target（含 fixed-model）可创建", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "cc-routes").Return(nil, nil)
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil).AnyTimes()
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-2").Return(activeProvider("key-2"), nil).AnyTimes()
			// OPUS → provider-default；SONNET → fixed-model（各查一次模型）。
			// 展示侧 toItem 还会解析主 provider（key-1）的默认模型。
			providerMock.EXPECT().FindModelByKey(gomock.Any(), "model-key-key-1").
				Return(activeDefaultModel("key-1"), nil).AnyTimes()
			providerMock.EXPECT().FindModelByKey(gomock.Any(), "mk-sonnet").
				Return(&llm_provider_model_entity.LLMProviderModel{
					ModelKey: "mk-sonnet", ModelID: "claude-sonnet-fixed",
					Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE,
				}, nil)
			backendMock.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
				DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
					assert.Contains(t, b.ModelRoutes, "\"OPUS\"")
					assert.Contains(t, b.ModelRoutes, "\"providerKey\":\"key-1\"")
					assert.Contains(t, b.ModelRoutes, "\"providerKey\":\"key-2\"")
					assert.Contains(t, b.ModelRoutes, "\"modelKey\":\"mk-sonnet\"")
					b.ID = 53
					return nil
				})

			resp, err := svc.Create(ctx, &CreateBackendRequest{
				Type:           string(agent_backend_entity.TypeClaudeCode),
				Name:           "cc-routes",
				LLMProviderKey: "key-1",
				ModelRoutes: map[string]RouteTarget{
					"OPUS":   {ProviderKey: "key-1"},
					"SONNET": {ProviderKey: "key-2", ModelKey: "mk-sonnet"},
				},
			})
			assert.NoError(t, err)
			assert.Equal(t, "key-1", resp.Item.ModelRoutes["OPUS"].ProviderKey)
			assert.Empty(t, resp.Item.ModelRoutes["OPUS"].ModelKey)
			assert.Equal(t, "key-2", resp.Item.ModelRoutes["SONNET"].ProviderKey)
			assert.Equal(t, "mk-sonnet", resp.Item.ModelRoutes["SONNET"].ModelKey,
				"DTO 必须暴露类型化 route target，而不是原始 JSON 字符串")
		})

		convey.Convey("route fixed-model 引用的模型不存在 → 拒绝创建", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "cc-badroute").Return(nil, nil)
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil).AnyTimes()
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-2").Return(activeProvider("key-2"), nil).AnyTimes()
			providerMock.EXPECT().FindModelByKey(gomock.Any(), "mk-gone").Return(nil, nil)

			_, err := svc.Create(ctx, &CreateBackendRequest{
				Type:           string(agent_backend_entity.TypeClaudeCode),
				Name:           "cc-badroute",
				LLMProviderKey: "key-1",
				ModelRoutes: map[string]RouteTarget{
					"HAIKU": {ProviderKey: "key-2", ModelKey: "mk-gone"},
				},
			})
			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, code.LLMProviderModelNotFound, httpErr.Code)
		})
	})
}

func TestUpdateBackend(t *testing.T) {
	convey.Convey("Update Agent backend", t, func() {
		ctx, backendMock, providerMock, _, _, svc := setupSvcTest(t)

		convey.Convey("成功改名并切换 provider", func() {
			existing := &agent_backend_entity.AgentBackend{
				ID: 5, Type: "builtin", Name: "old", LLMProviderKey: "key-1", Status: consts.ACTIVE,
			}
			backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(existing, nil)
			backendMock.EXPECT().FindByName(gomock.Any(), "new").Return(nil, nil)
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-2").Return(activeProvider("key-2"), nil)
			expectDefaultModelResolution(providerMock, "key-2", 1)
			backendMock.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
				DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
					assert.Equal(t, "new", b.Name)
					assert.Equal(t, "key-2", b.LLMProviderKey)
					return nil
				})

			_, err := svc.Update(ctx, &UpdateBackendRequest{
				ID: 5, Name: "new", LLMProviderKey: "key-2",
			})
			assert.NoError(t, err)
		})

		convey.Convey("后端不存在", func() {
			backendMock.EXPECT().Find(gomock.Any(), int64(99)).Return(nil, nil)
			_, err := svc.Update(ctx, &UpdateBackendRequest{ID: 99, Name: "x", LLMProviderKey: "key-1"})
			assert.Error(t, err)
		})

		convey.Convey("新名字被别人占用", func() {
			existing := &agent_backend_entity.AgentBackend{
				ID: 5, Type: "builtin", Name: "old", LLMProviderKey: "key-1", Status: consts.ACTIVE,
			}
			backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(existing, nil)
			backendMock.EXPECT().FindByName(gomock.Any(), "dup").
				Return(&agent_backend_entity.AgentBackend{ID: 6, Name: "dup"}, nil)
			_, err := svc.Update(ctx, &UpdateBackendRequest{ID: 5, Name: "dup", LLMProviderKey: "key-1"})
			assert.Error(t, err)
		})

		convey.Convey("codex 切换到 anthropic provider 时返回类型不匹配", func() {
			existing := &agent_backend_entity.AgentBackend{
				ID: 5, Type: string(agent_backend_entity.TypeCodex), Name: "codex", LLMProviderKey: "key-2", Status: consts.ACTIVE,
			}
			backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(existing, nil)
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil)

			_, err := svc.Update(ctx, &UpdateBackendRequest{ID: 5, Name: "codex", LLMProviderKey: "key-1"})
			assert.Error(t, err)
		})

		convey.Convey("claudecode 清除供应商关联", func() {
			existing := &agent_backend_entity.AgentBackend{
				ID: 5, Type: string(agent_backend_entity.TypeClaudeCode), Name: "cc", LLMProviderKey: "key-2", Status: consts.ACTIVE,
			}
			backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(existing, nil)
			backendMock.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
				DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
					assert.Equal(t, "", b.LLMProviderKey)
					return nil
				})

			resp, err := svc.Update(ctx, &UpdateBackendRequest{ID: 5, Name: "cc", LLMProviderKey: ""})
			assert.NoError(t, err)
			assert.Equal(t, "", resp.Item.LLMProviderName)
			assert.False(t, resp.Item.LLMProviderActive)
		})
	})
}

func TestDeleteBackend(t *testing.T) {
	convey.Convey("Delete Agent backend", t, func() {
		ctx, backendMock, _, _, _, svc := setupSvcTest(t)

		convey.Convey("成功软删", func() {
			backendMock.EXPECT().Find(gomock.Any(), int64(3)).Return(
				&agent_backend_entity.AgentBackend{ID: 3, Status: consts.ACTIVE}, nil,
			)
			backendMock.EXPECT().Delete(gomock.Any(), int64(3)).Return(nil)
			_, err := svc.Delete(ctx, &DeleteBackendRequest{ID: 3})
			assert.NoError(t, err)
		})

		convey.Convey("不存在", func() {
			backendMock.EXPECT().Find(gomock.Any(), int64(9)).Return(nil, nil)
			_, err := svc.Delete(ctx, &DeleteBackendRequest{ID: 9})
			assert.Error(t, err)
		})
	})
}

func TestDeleteBackendEvenWhenInUse(t *testing.T) {
	convey.Convey("被 Agent 引用的后端也可正常删除（后端可选，不再阻止）", t, func() {
		ctx, backendMock, _, _, _, svc := setupSvcTest(t)
		backendMock.EXPECT().Find(gomock.Any(), int64(5)).
			Return(&agent_backend_entity.AgentBackend{ID: 5, Status: consts.ACTIVE}, nil)
		backendMock.EXPECT().Delete(gomock.Any(), int64(5)).Return(nil)
		_, err := svc.Delete(ctx, &DeleteBackendRequest{ID: 5})
		convey.So(err, convey.ShouldBeNil)
	})
}

func TestListBackends(t *testing.T) {
	convey.Convey("List Agent backends", t, func() {
		ctx, backendMock, providerMock, agentMock, _, svc := setupSvcTest(t)

		rows := []*agent_backend_entity.AgentBackend{
			{ID: 1, Type: "builtin", Name: "a", LLMProviderKey: "key-1", Status: consts.ACTIVE},
			{ID: 2, Type: "builtin", Name: "b", LLMProviderKey: "key-7", Status: consts.ACTIVE},          // provider 缺失
			{ID: 3, Type: string(agent_backend_entity.TypeClaudeCode), Name: "c", Status: consts.ACTIVE}, // 未关联 provider
		}
		backendMock.EXPECT().List(gomock.Any()).Return(rows, nil)
		agentMock.EXPECT().CountByBackends(gomock.Any(), []int64{1, 2, 3}).
			Return(map[int64]int64{1: 3}, nil)
		providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil)
		expectDefaultModelResolution(providerMock, "key-1", 1)
		providerMock.EXPECT().FindByKey(gomock.Any(), "key-7").Return(nil, nil)
		// LLMProviderKey == "" 不应触发 FindByKey；如果调用则 mock 严格模式会失败。

		resp, err := svc.List(ctx, &ListBackendsRequest{})
		assert.NoError(t, err)
		assert.Len(t, resp.Items, 3)

		// 第一条 provider 存活：name / model 填好；agentCount 命中
		assert.Equal(t, "Production", resp.Items[0].LLMProviderName)
		assert.True(t, resp.Items[0].LLMProviderActive)
		assert.Equal(t, int64(3), resp.Items[0].AgentCount)

		// 第二条 provider 不存在：active = false；CountByBackends 未返回则为 0
		assert.False(t, resp.Items[1].LLMProviderActive)
		assert.Equal(t, "", resp.Items[1].LLMProviderName)
		assert.Equal(t, int64(0), resp.Items[1].AgentCount)

		// 第三条 未关联：LLMProviderKey==""，跳过 FindByKey，直接 active=false
		assert.Equal(t, "", resp.Items[2].LLMProviderKey)
		assert.False(t, resp.Items[2].LLMProviderActive)
		assert.Equal(t, "", resp.Items[2].LLMProviderName)
	})
}

func TestTestBackend_HappyPath(t *testing.T) {
	convey.Convey("Test backend (saved ID, prober returns pong)", t, func() {
		ctx, backendMock, providerMock, _, proberMock, svc := setupSvcTest(t)

		saved := &agent_backend_entity.AgentBackend{
			ID:             7,
			Type:           string(agent_backend_entity.TypeBuiltin),
			Name:           "默认助手",
			LLMProviderKey: "key-1",
			Status:         consts.ACTIVE,
		}
		backendMock.EXPECT().Find(gomock.Any(), int64(7)).Return(saved, nil)
		providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil)
		proberMock.EXPECT().
			Run(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{}), gomock.Any()).
			Return("pong", nil)

		res, err := svc.Test(ctx, &TestBackendRequest{ID: 7})

		assert.NoError(t, err)
		assert.NotNil(t, res)
		assert.True(t, res.OK)
		assert.Equal(t, "pong", res.Message)
		assert.GreaterOrEqual(t, res.LatencyMs, int64(0))
	})

	convey.Convey("Codex 关联 provider 测试时把 gateway token 与真实模型传给 prober", t, func() {
		ctx, _, providerMock, _, proberMock, svc := setupSvcTest(t)
		svc.gateway = &fakeBackendGateway{
			status: httpgateway.GatewayStatus{State: "running"},
			url:    "http://127.0.0.1:60080",
			token:  "tok-codex",
		}
		provider := activeProviderWithType("key-2", llm_provider_entity.TypeOpenAIResponse)
		providerMock.EXPECT().FindByKey(gomock.Any(), "key-2").Return(provider, nil)
		// deps.Model 应取 provider 当前默认模型的 ModelID，而非旧单模型字段。
		providerMock.EXPECT().FindModelByKey(gomock.Any(), "model-key-key-2").
			Return(defaultModelWithState("key-2", "gpt-5-codex", llm_provider_model_entity.EnabledOn), nil)

		var gotDeps ProbeDeps
		proberMock.EXPECT().
			Run(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{}), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *agent_backend_entity.AgentBackend, deps ProbeDeps) (string, error) {
				gotDeps = deps
				return "pong", nil
			})

		res, err := svc.Test(ctx, &TestBackendRequest{
			Type:           string(agent_backend_entity.TypeCodex),
			Name:           "codex",
			LLMProviderKey: "key-2",
		})

		assert.NoError(t, err)
		assert.NotNil(t, res)
		assert.True(t, res.OK)
		assert.Equal(t, "tok-codex", gotDeps.Token)
		assert.Equal(t, "http://127.0.0.1:60080", gotDeps.GatewayURL)
		assert.Equal(t, "gpt-5-codex", gotDeps.Model)
	})
}

// TestTestBackend_SelfFingerprintRunsLocalProbe R13 认领后本机 backend 的 DeviceID
// 是本机指纹。测试按钮必须把它当本地档跑本地 prober，而不是误报「远端设备不存在」
// （本机指纹永远不是配对 agentred 行）。
func TestTestBackend_SelfFingerprintRunsLocalProbe(t *testing.T) {
	convey.Convey("给定指向本机指纹的 claudecode backend, Test 走本地 prober", t, func() {
		ctx, backendMock, _, _, proberMock, svc := setupSvcTest(t)

		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
		rds.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()
		rds.EXPECT().List(gomock.Any()).Return(nil, nil).AnyTimes()
		prevSvc := remote_device_svc.Default()
		remote_device_svc.SetDefault(rds)
		t.Cleanup(func() { remote_device_svc.SetDefault(prevSvc) })

		saved := &agent_backend_entity.AgentBackend{
			ID: 8, Type: string(agent_backend_entity.TypeClaudeCode), Name: "claude",
			LLMProviderKey: "", Status: consts.ACTIVE, DeviceID: "sha256:self",
		}
		backendMock.EXPECT().Find(gomock.Any(), int64(8)).Return(saved, nil)
		proberMock.EXPECT().
			Run(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{}), gomock.Any()).
			Return("pong", nil)

		res, err := svc.Test(ctx, &TestBackendRequest{ID: 8})
		assert.NoError(t, err)
		assert.NotNil(t, res)
		assert.True(t, res.OK)
		assert.Equal(t, "pong", res.Message)
	})
}

func TestTestBackend_NoProvider(t *testing.T) {
	convey.Convey("claudecode/codex 不关联供应商时跳过 gateway，调 prober 走 CLI 自身登录", t, func() {
		convey.Convey("claudecode draft 无 provider → 不签 token，prober 收到空 deps", func() {
			ctx, _, _, _, proberMock, svc := setupSvcTest(t)
			// gateway 保持 nil；流程不再因此 soft fail。
			var gotDeps ProbeDeps
			proberMock.EXPECT().
				Run(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{}), gomock.Any()).
				DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend, deps ProbeDeps) (string, error) {
					gotDeps = deps
					assert.Equal(t, "", b.LLMProviderKey)
					return "pong", nil
				})

			res, err := svc.Test(ctx, &TestBackendRequest{
				Type:           string(agent_backend_entity.TypeClaudeCode),
				Name:           "claude",
				LLMProviderKey: "",
			})
			assert.NoError(t, err)
			assert.NotNil(t, res)
			assert.True(t, res.OK)
			assert.Equal(t, "", gotDeps.Token)
			assert.Equal(t, "", gotDeps.GatewayURL)
		})

		convey.Convey("codex draft 无 provider → 同样跳过 gateway", func() {
			ctx, _, _, _, proberMock, svc := setupSvcTest(t)
			proberMock.EXPECT().
				Run(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{}), gomock.Any()).
				Return("pong", nil)

			res, err := svc.Test(ctx, &TestBackendRequest{
				Type:           string(agent_backend_entity.TypeCodex),
				Name:           "codex",
				LLMProviderKey: "",
			})
			assert.NoError(t, err)
			assert.NotNil(t, res)
			assert.True(t, res.OK)
		})
	})
}

func TestTestBackend_PiAgentBoundSkipsGateway(t *testing.T) {
	convey.Convey("piagent 绑 provider 且 gateway 未注入 → 不进 gateway 分支，直接调 prober（piagent 不走 gateway，连通性测真实 provider）", t, func() {
		ctx, _, providerMock, _, proberMock, svc := setupSvcTest(t)
		// gateway 保持 nil；旧逻辑会在 `!IsBuiltin() && LLMProviderKey != ""`
		// 分支报 GatewayUnavailable，piagent 绑定后应不再进该分支。
		providerMock.EXPECT().FindByKey(gomock.Any(), "key-pi").Return(activeProvider("key-pi"), nil)
		// piagent 绑定时必须能解析出启用默认模型，否则校验直接拒绝。
		expectDefaultModelResolution(providerMock, "key-pi", 1)
		proberMock.EXPECT().
			Run(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{}), gomock.Any()).
			Return("pong", nil)

		res, err := svc.Test(ctx, &TestBackendRequest{
			Type:           string(agent_backend_entity.TypePiAgent),
			Name:           "pi",
			LLMProviderKey: "key-pi",
		})

		assert.NoError(t, err)
		assert.NotNil(t, res)
		assert.True(t, res.OK)
	})
}

func TestTestBackend_Validation(t *testing.T) {
	convey.Convey("Test backend validation failures", t, func() {
		ctx, backendMock, providerMock, _, _, svc := setupSvcTest(t)

		convey.Convey("ID 找不到 → AgentBackendNotFound", func() {
			backendMock.EXPECT().Find(gomock.Any(), int64(99)).Return(nil, nil)
			res, err := svc.Test(ctx, &TestBackendRequest{ID: 99})
			assert.Error(t, err)
			assert.Nil(t, res)
		})

		convey.Convey("draft 缺 llm_provider_key → entity.Check 失败", func() {
			res, err := svc.Test(ctx, &TestBackendRequest{
				Type:           string(agent_backend_entity.TypeBuiltin),
				Name:           "draft",
				LLMProviderKey: "",
			})
			assert.Error(t, err)
			assert.Nil(t, res)
		})

		convey.Convey("draft 非 builtin 且 gateway 未注入 → soft failure", func() {
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil)
			res, err := svc.Test(ctx, &TestBackendRequest{
				Type:           string(agent_backend_entity.TypeClaudeCode),
				Name:           "claude",
				LLMProviderKey: "key-1",
			})
			assert.NoError(t, err)
			assert.NotNil(t, res)
			assert.False(t, res.OK)
		})

		convey.Convey("provider 不存在 → AgentBackendLLMProviderNotFound", func() {
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(nil, nil)
			res, err := svc.Test(ctx, &TestBackendRequest{
				Type:           string(agent_backend_entity.TypeBuiltin),
				Name:           "x",
				LLMProviderKey: "key-1",
			})
			assert.Error(t, err)
			assert.Nil(t, res)
		})

		convey.Convey("provider 非 active → AgentBackendLLMProviderInactive", func() {
			inactive := activeProvider("key-1")
			inactive.Status = consts.DELETE
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(inactive, nil)
			res, err := svc.Test(ctx, &TestBackendRequest{
				Type:           string(agent_backend_entity.TypeBuiltin),
				Name:           "x",
				LLMProviderKey: "key-1",
			})
			assert.Error(t, err)
			assert.Nil(t, res)
		})
	})
}

func TestTestBackend_ProberFailures(t *testing.T) {
	convey.Convey("Test backend prober errors → OK:false soft response", t, func() {
		ctx, backendMock, providerMock, _, proberMock, svc := setupSvcTest(t)
		saved := &agent_backend_entity.AgentBackend{
			ID:             7,
			Type:           string(agent_backend_entity.TypeBuiltin),
			Name:           "默认助手",
			LLMProviderKey: "key-1",
			Status:         consts.ACTIVE,
		}

		convey.Convey("prober 超时 → 测试超时文案", func() {
			backendMock.EXPECT().Find(gomock.Any(), int64(7)).Return(saved, nil)
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil)
			proberMock.EXPECT().
				Run(gomock.Any(), gomock.Any(), gomock.Any()).
				Return("", context.DeadlineExceeded)

			res, err := svc.Test(ctx, &TestBackendRequest{ID: 7})
			assert.NoError(t, err)
			assert.NotNil(t, res)
			assert.False(t, res.OK)
			assert.Equal(t, "测试超时（30s）", res.Message)
		})

		convey.Convey("prober 普通 error → 透传 err.Error()", func() {
			backendMock.EXPECT().Find(gomock.Any(), int64(7)).Return(saved, nil)
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil)
			proberMock.EXPECT().
				Run(gomock.Any(), gomock.Any(), gomock.Any()).
				Return("", errors.New("401 unauthorized"))

			res, err := svc.Test(ctx, &TestBackendRequest{ID: 7})
			assert.NoError(t, err)
			assert.NotNil(t, res)
			assert.False(t, res.OK)
			assert.Contains(t, res.Message, "401 unauthorized")
		})
	})
}

// proberSpy 让 dispatch 测试可以观察「哪个 BackendType 的 prober 实际被命中」。
// 用普通 struct 而不是 gomock 是因为我们要走真正的 proberRegistry 派发路径，
// 而不是在 svc 上注入 mock prober 把 proberFor 短路掉。
type proberSpy struct {
	typ   agent_backend_entity.BackendType
	seen  *agent_backend_entity.BackendType
	reply string
}

func (s proberSpy) Run(_ context.Context, _ *agent_backend_entity.AgentBackend, _ ProbeDeps) (string, error) {
	*s.seen = s.typ
	return s.reply, nil
}

// withProberRegistry 临时替换包级 proberRegistry，t.Cleanup 时复位。
// 让单测能精确观察 Test() 对 BackendType → Prober 的派发选择。
func withProberRegistry(t *testing.T, reg map[agent_backend_entity.BackendType]Prober) {
	t.Helper()
	orig := proberRegistry
	proberRegistry = reg
	t.Cleanup(func() { proberRegistry = orig })
}

// spyAllTypes 构造一个把三种 BackendType 都映射到 proberSpy 的 registry，
// 测试通过 seen 指针读出实际命中的 type。
func spyAllTypes(seen *agent_backend_entity.BackendType) map[agent_backend_entity.BackendType]Prober {
	return map[agent_backend_entity.BackendType]Prober{
		agent_backend_entity.TypeBuiltin:    proberSpy{typ: agent_backend_entity.TypeBuiltin, seen: seen, reply: "pong"},
		agent_backend_entity.TypeClaudeCode: proberSpy{typ: agent_backend_entity.TypeClaudeCode, seen: seen, reply: "pong"},
		agent_backend_entity.TypeCodex:      proberSpy{typ: agent_backend_entity.TypeCodex, seen: seen, reply: "pong"},
		agent_backend_entity.TypePiAgent:    proberSpy{typ: agent_backend_entity.TypePiAgent, seen: seen, reply: "pong"},
	}
}

// registerPermissiveProviderRepo 给 dispatch 测试装一个「FindByKey 永远返回 nil」的 mock，
// 防止万一旧的 bug 把执行流送到 builtinProber.Run（它会自己调 llm_provider_repo.FindByKey）
// 时把测试炸成 panic，让红色断言能干净地落到 dispatch 期望本身上。
func registerPermissiveProviderRepo(t *testing.T) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	m := mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl)
	m.EXPECT().FindByKey(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	llm_provider_repo.RegisterLLMProvider(m)
}

func TestTestBackend_DispatchByBackendType(t *testing.T) {
	// 验证 production 单例的派发契约：当 svc.prober 没被注入 mock 时，
	// Test() 应按 entity.Type 走 proberFor 查找；之前默认单例硬编码
	// prober: builtinProber{} 会让 claudecode / codex 全部错误地走 in-process
	// builtinProber，导致用户点「测试连接」既不会拉 CLI 子进程也不会经 gateway。
	convey.Convey("production 单例按 BackendType 派发 prober", t, func() {
		convey.Convey("type=claudecode 走注册表中的 claudecode prober，而不是 builtinProber", func() {
			registerPermissiveProviderRepo(t)
			var seen agent_backend_entity.BackendType
			withProberRegistry(t, spyAllTypes(&seen))

			ctx := context.Background()
			res, err := defaultAgentBackend.Test(ctx, &TestBackendRequest{
				Type:           string(agent_backend_entity.TypeClaudeCode),
				Name:           "cli",
				LLMProviderKey: "",
			})

			assert.NoError(t, err)
			assert.NotNil(t, res)
			assert.True(t, res.OK, "派发到 spy 后应返回 OK；如果 OK=false，说明 builtinProber 抢走了执行")
			assert.Equal(t, agent_backend_entity.TypeClaudeCode, seen,
				"production 单例必须按 BackendType 走 proberFor 派发；若仍走 builtinProber，CLI 后端就不会真的拉子进程")
		})

		convey.Convey("type=codex 走注册表中的 codex prober", func() {
			registerPermissiveProviderRepo(t)
			var seen agent_backend_entity.BackendType
			withProberRegistry(t, spyAllTypes(&seen))

			ctx := context.Background()
			res, err := defaultAgentBackend.Test(ctx, &TestBackendRequest{
				Type:           string(agent_backend_entity.TypeCodex),
				Name:           "cli",
				LLMProviderKey: "",
			})

			assert.NoError(t, err)
			assert.NotNil(t, res)
			assert.True(t, res.OK)
			assert.Equal(t, agent_backend_entity.TypeCodex, seen)
		})
	})
}

func TestTestBackend_Cancel(t *testing.T) {
	convey.Convey("Test 阻塞期间 CancelTest → 返回「已取消」", t, func() {
		ctx, backendMock, providerMock, _, proberMock, svc := setupSvcTest(t)
		saved := &agent_backend_entity.AgentBackend{
			ID:             7,
			Type:           string(agent_backend_entity.TypeBuiltin),
			Name:           "默认助手",
			LLMProviderKey: "key-1",
			Status:         consts.ACTIVE,
		}
		backendMock.EXPECT().Find(gomock.Any(), int64(7)).Return(saved, nil)
		providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil)

		// prober 模拟"长任务"：等 ctx 被 cancel 后返回 ctx.Err()。
		started := make(chan struct{})
		proberMock.EXPECT().
			Run(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(probeCtx context.Context, _ *agent_backend_entity.AgentBackend, _ ProbeDeps) (string, error) {
				close(started)
				<-probeCtx.Done()
				return "", probeCtx.Err()
			})

		// 启动 Test，挂起在 prober.Run。
		resCh := make(chan *TestBackendResponse, 1)
		go func() {
			res, _ := svc.Test(ctx, &TestBackendRequest{ID: 7, RequestID: "req-abc"})
			resCh <- res
		}()
		<-started

		cancelResp, err := svc.CancelTest(ctx, &CancelTestBackendRequest{RequestID: "req-abc"})
		assert.NoError(t, err)
		assert.True(t, cancelResp.Canceled, "正在跑的请求应被命中")

		select {
		case res := <-resCh:
			assert.NotNil(t, res)
			assert.False(t, res.OK)
			assert.Equal(t, "已取消", res.Message)
		case <-time.After(2 * time.Second):
			t.Fatal("Test 没在 cancel 后及时返回")
		}
	})

	convey.Convey("CancelTest 未知 ID → Canceled=false 不报错", t, func() {
		ctx, _, _, _, _, svc := setupSvcTest(t)
		resp, err := svc.CancelTest(ctx, &CancelTestBackendRequest{RequestID: "missing"})
		assert.NoError(t, err)
		assert.False(t, resp.Canceled)
	})

	convey.Convey("CancelTest 空 ID → Canceled=false 不报错（前端竞态防御）", t, func() {
		ctx, _, _, _, _, svc := setupSvcTest(t)
		resp, err := svc.CancelTest(ctx, &CancelTestBackendRequest{RequestID: ""})
		assert.NoError(t, err)
		assert.False(t, resp.Canceled)
	})

	convey.Convey("Test 正常返回后 probes 表清空（不泄漏 cancel）", t, func() {
		ctx, backendMock, providerMock, _, proberMock, svc := setupSvcTest(t)
		saved := &agent_backend_entity.AgentBackend{
			ID:             7,
			Type:           string(agent_backend_entity.TypeBuiltin),
			Name:           "默认助手",
			LLMProviderKey: "key-1",
			Status:         consts.ACTIVE,
		}
		backendMock.EXPECT().Find(gomock.Any(), int64(7)).Return(saved, nil)
		providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil)
		proberMock.EXPECT().
			Run(gomock.Any(), gomock.Any(), gomock.Any()).
			Return("pong", nil)

		_, err := svc.Test(ctx, &TestBackendRequest{ID: 7, RequestID: "req-clean"})
		assert.NoError(t, err)
		svc.probesMu.Lock()
		_, stillThere := svc.probes["req-clean"]
		svc.probesMu.Unlock()
		assert.False(t, stillThere, "probe 完成后应从注册表删除")
	})
}

// setupSvcTestWithRemoteDevice wraps setupSvcTest and additionally injects
// a mock RemoteDeviceSvc as the global default. The cleanup of the existing
// gomock ctrl restores the parent.
func setupSvcTestWithRemoteDevice(t *testing.T) (
	context.Context,
	*mock_agent_backend_repo.MockAgentBackendRepo,
	*mock_llm_provider_repo.MockLLMProviderRepo,
	*mock_agent_repo.MockAgentRepo,
	*mock_remote_device_svc.MockRemoteDeviceSvc,
	*mockProber,
	*agentBackendSvc,
) {
	t.Helper()
	ctx, backendMock, providerMock, agentMock, prober, svc := setupSvcTest(t)
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	rd := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	previous := remote_device_svc.Default()
	remote_device_svc.SetDefault(rd)
	t.Cleanup(func() { remote_device_svc.SetDefault(previous) })
	return ctx, backendMock, providerMock, agentMock, rd, prober, svc
}

func TestClaimRelativeBackends_PromotesExistingRowsWithoutCloningOrTombstoning(t *testing.T) {
	ctx, _, _, _, _, _, svc := setupSvcTestWithRemoteDevice(t)

	// Given existing device-scoped backend rows, when startup promotes the
	// account model, then it keeps every original identity. No type/name merge,
	// clone, tombstone, or fingerprint lookup is allowed.
	require.NoError(t, svc.ClaimRelativeBackends(ctx))
}

func TestClaimRelativeBackends_GivenNoRemoteIdentity_StillDoesNotBlockStartup(t *testing.T) {
	ctx, _, _, _, _, _, svc := setupSvcTestWithRemoteDevice(t)
	assert.NoError(t, svc.ClaimRelativeBackends(ctx))
}

// TestCLIOverlay_GivenNoExistingOverlay_CreatesLocalDeviceOverride verifies CLI
// paths are persisted independently from the account backend identity.
func TestCLIOverlay_GivenNoExistingOverlay_CreatesLocalDeviceOverride(t *testing.T) {
	ctx, backendMock, _, _, rd, _, svc := setupSvcTestWithRemoteDevice(t)
	rd.EXPECT().DeviceFingerprint().Return("sha256:self", nil)
	backendMock.EXPECT().FindCLIOverlay(ctx, "backend-1", "sha256:self").Return(nil, nil)
	backendMock.EXPECT().CreateCLIOverlay(ctx, gomock.AssignableToTypeOf(&agent_backend_entity.CLIOverlay{})).DoAndReturn(
		func(_ context.Context, overlay *agent_backend_entity.CLIOverlay) error {
			assert.Equal(t, "backend-1", overlay.BackendSyncID)
			assert.Equal(t, "sha256:self", overlay.AgentredFingerprint)
			assert.Equal(t, "/opt/claude", overlay.CLIPath)
			return nil
		})

	_, err := svc.SetCLIOverlay(ctx, &SetCLIOverlayRequest{BackendSyncID: "backend-1", CLIPath: "/opt/claude"})
	require.NoError(t, err)
}

// TestCLIOverlay_GivenMissingOverlay_ReportsPATH makes missing and explicit
// empty overlays equivalent at the Wails boundary without consulting CLIPath on
// an agent_backends row.
func TestCLIOverlay_GivenMissingOverlay_ReportsPATH(t *testing.T) {
	ctx, backendMock, _, _, rd, _, svc := setupSvcTestWithRemoteDevice(t)
	rd.EXPECT().DeviceFingerprint().Return("sha256:self", nil)
	backendMock.EXPECT().FindCLIOverlay(ctx, "backend-1", "sha256:self").Return(nil, nil)

	response, err := svc.GetCLIOverlay(ctx, &GetCLIOverlayRequest{BackendSyncID: "backend-1"})
	require.NoError(t, err)
	assert.Empty(t, response.CLIPath)
	assert.Equal(t, "path", response.Status)
}

func TestBackend_GivenCanonicalFingerprint_ResolvesPairedRowForRemoteProbe(t *testing.T) {
	ctx, backendMock, _, _, rd, _, svc := setupSvcTestWithRemoteDevice(t)
	fake := &fakeRemoteCLI{probeResp: &handlers.CLIProbeResult{Text: "pong"}}
	svc.remoteCLI = fake
	backendMock.EXPECT().Find(ctx, int64(9)).Return(&agent_backend_entity.AgentBackend{
		ID: 9, Type: "claudecode", Name: "desktop", DeviceID: "sha256:desktop-a", Status: consts.ACTIVE,
	}, nil)
	rd.EXPECT().List(ctx).Return([]*remote_device_svc.DeviceView{{ID: 7, DaemonFingerprint: "sha256:desktop-a"}}, nil)

	resp, err := svc.Test(ctx, &TestBackendRequest{ID: 9})
	require.NoError(t, err)
	assert.True(t, resp.OK)
	assert.Equal(t, 1, fake.probeCalls)
}

func TestResolveCLIPath_GivenCanonicalFingerprint_UsesLocalPairedRowAtDispatch(t *testing.T) {
	ctx, _, _, _, rd, _, svc := setupSvcTestWithRemoteDevice(t)
	fake := &fakeRemoteCLI{resolveResp: &ResolveCLIPathResponse{Path: "/remote/bin/claude", Found: true}}
	svc.remoteCLI = fake
	rd.EXPECT().List(ctx).Return([]*remote_device_svc.DeviceView{
		{ID: 7, DaemonFingerprint: "sha256:desktop-a"},
	}, nil)

	resp, err := svc.ResolveCLIPath(ctx, &ResolveCLIPathRequest{Type: "claudecode", DeviceID: "sha256:desktop-a"})
	require.NoError(t, err)
	assert.True(t, resp.Found)
	assert.Equal(t, int64(7), fake.resolveDeviceID)
}

func TestCreateBackend_GivenCanonicalFingerprint_PersistsItAfterMatchingPairedDevice(t *testing.T) {
	ctx, backendMock, providerMock, _, rd, _, svc := setupSvcTestWithRemoteDevice(t)
	providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil)
	expectDefaultModelResolution(providerMock, "key-1", 1)
	backendMock.EXPECT().FindByName(gomock.Any(), "desktop-cc").Return(nil, nil)
	backendMock.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
			assert.Equal(t, "sha256:desktop-a", b.DeviceID)
			b.ID = 11
			return nil
		},
	)
	rd.EXPECT().List(ctx).Return([]*remote_device_svc.DeviceView{
		{ID: 7, Name: "desktop-a", DaemonFingerprint: "sha256:desktop-a", Online: true},
	}, nil)
	// 展示侧要先判「这个指纹是不是本机」才决定按远端解析（DisplayDeviceID）。
	rd.EXPECT().DeviceFingerprint().Return("sha256:this-desktop", nil).AnyTimes()

	resp, err := svc.Create(ctx, &CreateBackendRequest{
		Type: "claudecode", Name: "desktop-cc", LLMProviderKey: "key-1", DeviceID: "sha256:desktop-a",
	})
	require.NoError(t, err)
	assert.Equal(t, "sha256:desktop-a", resp.Item.DeviceID)
	assert.Equal(t, "desktop-a", resp.Item.DeviceName)
	assert.True(t, resp.Item.Online)
}

func TestCreateBackend_RemoteDeviceValidation(t *testing.T) {
	convey.Convey("Create 时 deviceId 非空必须命中已知 paired device", t, func() {
		convey.Convey("device 不存在 → AgentBackendInvalidDevice", func() {
			ctx, _, providerMock, _, rd, _, svc := setupSvcTestWithRemoteDevice(t)
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil).AnyTimes()
			rd.EXPECT().Get(ctx, int64(7)).Return(nil, errors.New("not found"))
			_, err := svc.Create(ctx, &CreateBackendRequest{Type: "claudecode", Name: "remote-cc", DeviceID: "7"})
			convey.So(err, convey.ShouldNotBeNil)
		})
		convey.Convey("device 存在 → 允许并回填 view", func() {
			ctx, backendMock, providerMock, _, rd, _, svc := setupSvcTestWithRemoteDevice(t)
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil).AnyTimes()
			expectDefaultModelResolution(providerMock, "key-1", 1)
			backendMock.EXPECT().FindByName(gomock.Any(), "remote-cc").Return(nil, nil)
			backendMock.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, b *agent_backend_entity.AgentBackend) error { b.ID = 11; return nil },
			)
			rd.EXPECT().Get(ctx, int64(7)).Return(&remote_device_svc.DeviceView{ID: 7, Name: "linux-srv", Online: true}, nil).Times(2)
			resp, err := svc.Create(ctx, &CreateBackendRequest{
				Type: "claudecode", Name: "remote-cc",
				LLMProviderKey: "key-1", DeviceID: "7",
			})
			convey.So(err, convey.ShouldBeNil)
			convey.So(resp.Item.DeviceID, convey.ShouldEqual, "7")
			convey.So(resp.Item.DeviceName, convey.ShouldEqual, "linux-srv")
			convey.So(resp.Item.Online, convey.ShouldBeTrue)
		})
		convey.Convey("device 空 = 本机指纹（落库带指纹，视图仍按本机口径）", func() {
			ctx, backendMock, providerMock, _, rd, _, svc := setupSvcTestWithRemoteDevice(t)
			providerMock.EXPECT().FindByKey(gomock.Any(), "key-1").Return(activeProvider("key-1"), nil).AnyTimes()
			expectDefaultModelResolution(providerMock, "key-1", 1)
			backendMock.EXPECT().FindByName(gomock.Any(), "local-cc").Return(nil, nil)
			persisted := ""
			backendMock.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
					persisted = b.DeviceID
					b.ID = 12
					return nil
				},
			)
			rd.EXPECT().DeviceFingerprint().Return("sha256:this-desktop", nil).AnyTimes()
			resp, err := svc.Create(ctx, &CreateBackendRequest{
				Type: "claudecode", Name: "local-cc",
				LLMProviderKey: "key-1", DeviceID: "",
			})
			convey.So(err, convey.ShouldBeNil)
			// 持久化侧：空串被规范化成本机指纹（canonicalDeviceID）。
			convey.So(persisted, convey.ShouldEqual, "sha256:this-desktop")
			// 展示侧：本机指纹收敛回本机口径，否则界面把本机当成一台未配对的远端机。
			convey.So(resp.Item.DeviceID, convey.ShouldEqual, "")
			convey.So(resp.Item.DeviceName, convey.ShouldEqual, "")
		})
	})
}

func TestListBackends_EnrichesDeviceInfo(t *testing.T) {
	convey.Convey("List 把 device 信息回填到 view", t, func() {
		ctx, backendMock, providerMock, agentMock, rd, _, svc := setupSvcTestWithRemoteDevice(t)
		backendMock.EXPECT().List(ctx).Return([]*agent_backend_entity.AgentBackend{
			{ID: 1, Type: "claudecode", Name: "local-cc", DeviceID: "", Status: consts.ACTIVE},
			{ID: 2, Type: "claudecode", Name: "remote-cc", DeviceID: "7", Status: consts.ACTIVE},
		}, nil)
		agentMock.EXPECT().CountByBackends(gomock.Any(), []int64{1, 2}).Return(map[int64]int64{}, nil)
		// 两行 LLMProviderKey 均为 ""，跳过 provider FindByKey。
		providerMock.EXPECT().FindByKey(gomock.Any(), gomock.Any()).Times(0)
		rd.EXPECT().Get(ctx, int64(7)).Return(&remote_device_svc.DeviceView{ID: 7, Name: "linux-srv", Online: false}, nil)
		resp, err := svc.List(ctx, &ListBackendsRequest{})
		convey.So(err, convey.ShouldBeNil)
		convey.So(len(resp.Items), convey.ShouldEqual, 2)
		convey.So(resp.Items[0].DeviceID, convey.ShouldEqual, "")
		convey.So(resp.Items[1].DeviceID, convey.ShouldEqual, "7")
		convey.So(resp.Items[1].DeviceName, convey.ShouldEqual, "linux-srv")
		convey.So(resp.Items[1].Online, convey.ShouldBeFalse)
	})
}

package chat_svc_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/agents/provider"
	"github.com/cago-frame/agents/provider/providertest"
	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	piagentrt "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/piagent"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/httpgateway"
	"github.com/agentre-hub/agentre/internal/pkg/protorpctest"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo/mock_llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	chatblocks "github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/claudecode"
	pkgpiagent "github.com/agentre-hub/agentre/pkg/piagent"
)

type chatMocks struct {
	agent      *mock_agent_repo.MockAgentRepo
	backend    *mock_agent_backend_repo.MockAgentBackendRepo
	provider   *mock_llm_provider_repo.MockLLMProviderRepo
	session    *mock_chat_repo.MockSessionRepo
	message    *mock_chat_repo.MockMessageRepo
	execTarget *mock_agent_repo.MockAgentExecTargetRepo
	dbMock     sqlmock.Sqlmock
	ctx        context.Context
	events     []recorded
	svc        chat_svc.ChatSvc
}

type recorded struct {
	Name    string
	Payload any
}

type fakeChatGateway struct {
	status httpgateway.GatewayStatus
	url    string
	token  string
}

func (f *fakeChatGateway) IssueToken(context.Context, *agent_backend_entity.AgentBackend, time.Duration) (string, error) {
	if f.token != "" {
		return f.token, nil
	}
	return "chat-token", nil
}

func (f *fakeChatGateway) IssueTokenFor(
	ctx context.Context, be *agent_backend_entity.AgentBackend, _, _ string, ttl time.Duration,
) (string, error) {
	return f.IssueToken(ctx, be, ttl)
}

func (f *fakeChatGateway) SetTokenTarget(_, providerKey, _ string) (string, bool) {
	return providerKey, true
}

func (f *fakeChatGateway) RevokeToken(string) {}

func (f *fakeChatGateway) URL() string {
	if f.url != "" {
		return f.url
	}
	return "http://127.0.0.1:60080"
}

func (f *fakeChatGateway) Status() httpgateway.GatewayStatus { return f.status }

func setupChatTest(t *testing.T) *chatMocks {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	dbCtx, _, dbMock := testutils.Database(t)

	m := &chatMocks{
		agent:      mock_agent_repo.NewMockAgentRepo(ctrl),
		backend:    mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl),
		provider:   mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl),
		session:    mock_chat_repo.NewMockSessionRepo(ctrl),
		message:    mock_chat_repo.NewMockMessageRepo(ctrl),
		execTarget: mock_agent_repo.NewMockAgentExecTargetRepo(ctrl),
		dbMock:     dbMock,
		ctx:        dbCtx,
	}
	agent_repo.RegisterAgent(m.agent)
	agent_backend_repo.RegisterAgentBackend(m.backend)
	llm_provider_repo.RegisterLLMProvider(m.provider)
	chat_repo.RegisterSession(m.session)
	chat_repo.RegisterMessage(m.message)
	// pi 恢复标记落在 app_settings,用真实现走同一份 sqlmock,标记 SQL 才进得了断言。
	app_setting_repo.RegisterAppSetting(app_setting_repo.NewAppSetting())
	agent_repo.RegisterAgentExecTarget(m.execTarget)
	// R14 顺序解析的宽松桩：这批既有测试不关心本端覆盖（默认无覆盖）。PickExecTarget
	// 只有在执行目标列表非空且会话未钉住时才走到，这里默认空列表本就让它短路；
	// 注册一个恒 nil 的覆盖桩只是防御性地保证任何到达该路径的用例都不会打真实库。
	overrideMock := mock_agent_repo.NewMockAgentExecTargetOverrideRepo(ctrl)
	overrideMock.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	agent_repo.RegisterAgentExecTargetOverride(overrideMock)

	// 默认宽松桩：这批既有测试全部模拟"Agent 只有一个（隐式）执行目标"的场景
	// (m.agent 直接给 AgentBackendID，不途经真实的 agent_exec_targets 表)。
	// resolveAgentBackend 在会话没有钉住任何一档时，先看这个 Agent 的执行目标
	// 列表是否为空——为空就直接退化用 a.AgentBackendID，语义与
	// agent_repo.hydrateExecTargets 一致，不必每个测试都单独搭一份执行目标行
	// mock。真正要验证 R15 多档挑选 / 会话粘性写回的用例在
	// exec_target_pin_test.go 里用专门搭的、非空的执行目标列表覆盖这条默认。
	m.execTarget.EXPECT().ListByAgent(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	// 默认宽松桩：会话粘性钉住写回（R15b / 决策36）对这批既有测试是一个新增的
	// 无关副作用——它们都不关心"钉在哪一档"这件事本身。gomock 按注册顺序匹配,
	// 这条 AnyTimes() 注册在最前会拦掉这个方法的全部调用,因此不要在具体测试里
	// 对同一方法再叠加精确期望(永远匹配不到);真正要验证写回参数的用例改用
	// exec_target_pin_test.go 里独立搭建、不含这条宽松桩的专用 mock 环境。
	m.session.EXPECT().UpdateExecDaemon(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	emitter := chat_svc.EmitterFunc(func(_ context.Context, name string, payload any) {
		m.events = append(m.events, recorded{Name: name, Payload: payload})
	})
	m.svc = chat_svc.NewChat(emitter)
	chat_svc.RegisterChat(m.svc)
	return m
}

// expectTranscriptWindowFilled 放行 LoadSession 的第二步取数(窗口内正文补齐)。
// 读路径拆成「元数据全量(ListMeta)+ 正文按需取(FillBlocks)」两步之后,这批用例给的
// 消息在内存里本就带着正文,补齐对它们是个 no-op;真正校验「窗口有界、窗口外只给
// 元数据」的用例在 read_path_test.go。
func expectTranscriptWindowFilled(m *chatMocks) {
	m.message.EXPECT().FillBlocks(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
}

// capturedArg 是「先放行、事后断言」的 sqlmock 参数匹配器:参数本身要做结构化校验时,
// 用它把值捞出来,而不是把断言逻辑塞进 Match(失败时看不到差异)。
type capturedArg struct{ value driver.Value }

func (c *capturedArg) Match(v driver.Value) bool {
	c.value = v
	return true
}

func expectNoPiTranscriptRecovery(m *chatMocks, sessionID int64) {
	m.dbMock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
		WithArgs(fmt.Sprintf("chat.pi_recovery:%d", sessionID), 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}))
}

func expectLoadSessionBackend(
	m *chatMocks,
	ctx context.Context,
	sessionID int64,
	agentID int64,
	backendID int64,
	backendType agent_backend_entity.BackendType,
	provider *llm_provider_entity.LLMProvider,
	messages ...*chat_entity.Message,
) {
	providerKey := ""
	if provider != nil {
		providerKey = provider.ProviderKey
	}
	m.session.EXPECT().Find(ctx, sessionID).Return(&chat_entity.Session{ID: sessionID, AgentID: agentID, Status: consts.ACTIVE}, nil)
	m.agent.EXPECT().Find(ctx, agentID).Return(&agent_entity.Agent{ID: agentID, AgentBackendID: backendID, Status: consts.ACTIVE}, nil)
	m.backend.EXPECT().Find(ctx, backendID).Return(&agent_backend_entity.AgentBackend{
		ID: backendID, Type: string(backendType), LLMProviderKey: providerKey, Status: consts.ACTIVE,
	}, nil)
	if provider != nil {
		m.provider.EXPECT().FindByKey(ctx, provider.ProviderKey).Return(provider, nil).AnyTimes()
	}
	expectTranscriptWindowFilled(m)
	m.message.EXPECT().ListMeta(ctx, sessionID).Return(messages, nil)
}

func assertLoadSessionContextWindow(
	t *testing.T,
	m *chatMocks,
	ctx context.Context,
	sessionID int64,
	agentID int64,
	backendID int64,
	providerID int64,
	providerKey string,
	providerContextWindow int,
	want int,
	message string,
) {
	t.Helper()
	expectLoadSessionBackend(m, ctx, sessionID, agentID, backendID, agent_backend_entity.TypeClaudeCode, &llm_provider_entity.LLMProvider{
		ID:              providerID,
		ProviderKey:     providerKey,
		Type:            string(llm_provider_entity.TypeAnthropic),
		Status:          consts.ACTIVE,
		Enabled:         llm_provider_entity.EnabledOn,
		DefaultModelKey: "mk-" + providerKey,
	}, &chat_entity.Message{
		ID: sessionID * 10, SessionID: sessionID, Role: "assistant", BlocksJSON: "[]", Seq: 1, Model: "claude-haiku-4-5",
	})
	// 展示侧与执行侧同一解析（EffectiveLLMConfig v1 seam）：contextWindow 来自解析出的
	// 模型（providerContextWindow），不再是 Provider 行字段。显式配 0 = 未配置 → 落到
	// message.Model catalog。
	m.provider.EXPECT().FindModelByKey(ctx, "mk-"+providerKey).Return(
		&llm_provider_model_entity.LLMProviderModel{
			ModelKey: "mk-" + providerKey, ModelID: "claude-sonnet-4-6",
			ContextWindow: providerContextWindow, Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE,
		}, nil).AnyTimes()

	resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: sessionID})
	assert.NoError(t, err)
	assert.Equal(t, want, resp.Session.ContextWindow, message)
}

// TestLoadSession_ContextWindowUsesSessionFixedModel 钉死 spec 2026-08-11「Effective
// configuration」：展示侧与执行侧同一解析结果 —— 会话钉了 fixed-model 时，LoadSession 的
// 上下文窗口 / 模型目录必须按会话的 ModelKey 解析（fixed 模型），而不是 backend 绑定
// （provider-default 会解析成默认模型的窗口）。
func TestLoadSession_ContextWindowUsesSessionFixedModel(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	m.session.EXPECT().Find(ctx, int64(11)).Return(&chat_entity.Session{
		ID: 11, AgentID: 15, ProviderKey: "key-49", ModelKey: "mk-49-fixed", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(ctx, int64(15)).Return(&agent_entity.Agent{
		ID: 15, AgentBackendID: 44, Status: consts.ACTIVE,
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(44)).Return(&agent_backend_entity.AgentBackend{
		ID: 44, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "key-49", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(ctx, "key-49").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "key-49", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-49-default", ID: 49,
		Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE,
	}, nil).AnyTimes()
	// 会话钉的 fixed 模型：窗口 88000。default 模型（120000）绝不能被解析进展示。
	m.provider.EXPECT().FindModelByKey(ctx, "mk-49-fixed").Return(
		&llm_provider_model_entity.LLMProviderModel{ProviderID: 49, ModelKey: "mk-49-fixed", ModelID: "claude-opus-4-1", ContextWindow: 88000, Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE},
		nil).AnyTimes()
	m.provider.EXPECT().FindModelByKey(ctx, "mk-49-default").Return(
		&llm_provider_model_entity.LLMProviderModel{ProviderID: 49, ModelKey: "mk-49-default", ModelID: "claude-sonnet-4-6", ContextWindow: 120000, Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE},
		nil).AnyTimes()
	expectTranscriptWindowFilled(m)
	m.message.EXPECT().ListMeta(ctx, int64(11)).Return(nil, nil)

	resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 11})
	assert.NoError(t, err)
	assert.Equal(t, 88000, resp.Session.ContextWindow, "会话 fixed-model 的展示必须用会话 ModelKey 解析，而不是 backend 默认")
}

func expectLaunchCommandBackend(
	m *chatMocks,
	ctx context.Context,
	sessionID int64,
	agentID int64,
	backendID int64,
	backendType agent_backend_entity.BackendType,
	providerSessionID string,
	provider *llm_provider_entity.LLMProvider,
) {
	m.session.EXPECT().Find(ctx, sessionID).Return(&chat_entity.Session{
		ID: sessionID, AgentID: agentID, ProviderSessionID: providerSessionID, Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(ctx, agentID).Return(&agent_entity.Agent{
		ID: agentID, AgentBackendID: backendID, Status: consts.ACTIVE,
	}, nil)
	m.backend.EXPECT().Find(ctx, backendID).Return(&agent_backend_entity.AgentBackend{
		ID: backendID, Type: string(backendType), LLMProviderKey: provider.ProviderKey, Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(ctx, provider.ProviderKey).Return(provider, nil).AnyTimes()
}

func loadLaunchCommand(
	t *testing.T,
	m *chatMocks,
	ctx context.Context,
	sessionID int64,
	backendType agent_backend_entity.BackendType,
) string {
	t.Helper()
	resp, err := m.svc.GetLaunchCommand(ctx, &chat_svc.LaunchCommandRequest{SessionID: sessionID})
	assert.NoError(t, err)
	assert.Equal(t, string(backendType), resp.BackendType)
	assert.NotContains(t, resp.Command, "\n")
	return resp.Command
}

func expectCapabilitySessionBackend(
	m *chatMocks,
	ctx context.Context,
	providerSessionID string,
	agentName string,
	backendType agent_backend_entity.BackendType,
) {
	m.session.EXPECT().Find(ctx, int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE, ProviderSessionID: providerSessionID,
	}, nil)
	m.agent.EXPECT().Find(ctx, int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: agentName, AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(backendType), LLMProviderKey: "", Status: consts.ACTIVE,
	}, nil)
}

func TestCountActiveSessions(t *testing.T) {
	m := setupChatTest(t)
	m.session.EXPECT().
		CountActive(gomock.Any(), []string{"running", "waiting"}).
		Return(int64(5), nil)

	n, err := m.svc.CountActiveSessions(m.ctx)
	assert.NoError(t, err)
	assert.Equal(t, 5, n)
}

func TestRegisterGatewayBeforeNewChatMakesCLIBackendsChattable(t *testing.T) {
	chat_svc.RegisterChat(nil)
	chat_svc.RegisterGateway(nil)
	chat_svc.RegisterGateway(&fakeChatGateway{
		status: httpgateway.GatewayStatus{State: "running", URL: "http://127.0.0.1:60080"},
	})
	t.Cleanup(func() {
		chat_svc.RegisterGateway(nil)
		chat_svc.RegisterChat(nil)
	})

	m := setupChatTest(t)
	ctx := context.Background()

	m.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{
		{ID: 7, Name: "Coder", AgentBackendID: 12, Status: consts.ACTIVE},
	}, nil)
	m.backend.EXPECT().BatchFind(ctx, []int64{12}).Return(map[int64]*agent_backend_entity.AgentBackend{
		12: {ID: 12, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "key-21", Status: consts.ACTIVE},
	}, nil)
	m.provider.EXPECT().BatchFindByKey(ctx, []string{"key-21"}).Return(map[string]*llm_provider_entity.LLMProvider{
		"key-21": {ID: 21, Type: string(llm_provider_entity.TypeOpenAIResponse), Status: consts.ACTIVE},
	}, nil)
	m.session.EXPECT().CountRunningByAgents(ctx, []int64{7}).Return(map[int64]int{}, nil)
	m.session.EXPECT().CountByAgents(ctx, []int64{7}).Return(map[int64]int64{}, nil)
	m.session.EXPECT().ListIDsByAgents(ctx, []int64{7}).Return(map[int64][]int64{}, nil)
	m.session.EXPECT().ListByAgent(ctx, int64(7), 5).Return(nil, nil)
	m.session.EXPECT().ListAttentionByAgent(ctx, int64(7), 20).Return(nil, nil)

	resp, err := m.svc.ListAgents(ctx, &chat_svc.ListAgentsRequest{})
	assert.NoError(t, err)
	if assert.Len(t, resp.Agents, 1) {
		assert.True(t, resp.Agents[0].Chattable)
		assert.Empty(t, resp.Agents[0].ChattableHint)
	}
}

func TestListAgentsOpenClawAvailability(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()
	m.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{
		{ID: 31, Name: "Local Claw", AgentBackendID: 41, Status: consts.ACTIVE},
		{ID: 32, Name: "Remote Claw", AgentBackendID: 42, Status: consts.ACTIVE},
	}, nil)
	m.backend.EXPECT().BatchFind(ctx, []int64{41, 42}).Return(map[int64]*agent_backend_entity.AgentBackend{
		41: {ID: 41, Type: string(agent_backend_entity.TypeOpenClaw), OpenClawGatewayURL: "ws://127.0.0.1:18789", Status: consts.ACTIVE},
		42: {ID: 42, Type: string(agent_backend_entity.TypeOpenClaw), OpenClawGatewayURL: "ws://127.0.0.1:18789", DeviceFingerprint: "sha256:device-9", Status: consts.ACTIVE},
	}, nil)
	m.provider.EXPECT().BatchFindByKey(ctx, []string{}).Return(map[string]*llm_provider_entity.LLMProvider{}, nil)
	m.session.EXPECT().CountRunningByAgents(ctx, []int64{31, 32}).Return(map[int64]int{}, nil)
	m.session.EXPECT().CountByAgents(ctx, []int64{31, 32}).Return(map[int64]int64{}, nil)
	m.session.EXPECT().ListIDsByAgents(ctx, []int64{31, 32}).Return(map[int64][]int64{}, nil)
	for _, id := range []int64{31, 32} {
		m.session.EXPECT().ListByAgent(ctx, id, 5).Return(nil, nil)
		m.session.EXPECT().ListAttentionByAgent(ctx, id, 20).Return(nil, nil)
	}

	response, err := m.svc.ListAgents(ctx, &chat_svc.ListAgentsRequest{})
	assert.NoError(t, err)
	if assert.Len(t, response.Agents, 2) {
		assert.True(t, response.Agents[0].Chattable)
		assert.Empty(t, response.Agents[0].ChattableHint)
		assert.False(t, response.Agents[1].Chattable)
		assert.NotEmpty(t, response.Agents[1].ChattableHint)
	}
}

// listSingleAgentItem 跑单 Agent 的 ListAgents 场景（sqlmock + mockgen），返回该 Agent 的
// ChatAgentItem，省去每个 blockReason 分支重复铺会话 mock。
func listSingleAgentItem(
	t *testing.T,
	m *chatMocks,
	a *agent_entity.Agent,
	be *agent_backend_entity.AgentBackend,
	providers map[string]*llm_provider_entity.LLMProvider,
) *chat_svc.ChatAgentItem {
	t.Helper()
	ctx := context.Background()

	backendIDs := []int64{}
	beMap := map[int64]*agent_backend_entity.AgentBackend{}
	if a.AgentBackendID > 0 {
		backendIDs = []int64{a.AgentBackendID}
		if be != nil {
			beMap[a.AgentBackendID] = be
		}
	}
	keys := []string{}
	if be != nil && be.LLMProviderKey != "" {
		keys = []string{be.LLMProviderKey}
	}
	m.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{a}, nil)
	m.backend.EXPECT().BatchFind(ctx, backendIDs).Return(beMap, nil)
	m.provider.EXPECT().BatchFindByKey(ctx, keys).Return(providers, nil)
	m.session.EXPECT().CountRunningByAgents(ctx, []int64{a.ID}).Return(map[int64]int{}, nil)
	m.session.EXPECT().CountByAgents(ctx, []int64{a.ID}).Return(map[int64]int64{}, nil)
	m.session.EXPECT().ListIDsByAgents(ctx, []int64{a.ID}).Return(map[int64][]int64{}, nil)
	m.session.EXPECT().ListByAgent(ctx, a.ID, 5).Return(nil, nil)
	m.session.EXPECT().ListAttentionByAgent(ctx, a.ID, 20).Return(nil, nil)

	resp, err := m.svc.ListAgents(ctx, &chat_svc.ListAgentsRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Agents, 1)
	return &resp.Agents[0]
}

func TestListAgents_BlockReason(t *testing.T) {
	convey.Convey("ListAgents 为每个不可对话分支设置结构化 blockReason（空串=可对话）", t, func() {
		ctx := context.Background()

		// 共享 mock remote_device_svc：远端 backend 的 device 视图查询（ListAgents 内
		// rds.Get）与 remoteProviderKnownMissing 的 provider 列表都需要它；nil 默认时
		// 这两个查询会静默跳过，无法覆盖远端分支。
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		mockRDS := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
		// 本机指纹:用例里的 DeviceID 都是别机指纹,「是不是本机档」这一问对每次
		// 解析都要答一次。
		mockRDS.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()
		remote_device_svc.SetDefault(mockRDS)
		t.Cleanup(func() { remote_device_svc.SetDefault(nil) })

		convey.Convey("可对话（builtin + 激活供应商）→ blockReason 为空串", func() {
			m := setupChatTest(t)
			item := listSingleAgentItem(t, m,
				&agent_entity.Agent{ID: 1, Name: "A", AgentBackendID: 10, Status: consts.ACTIVE},
				&agent_backend_entity.AgentBackend{ID: 10, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-1", Status: consts.ACTIVE},
				map[string]*llm_provider_entity.LLMProvider{"key-1": {ID: 1, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}},
			)
			assert.True(t, item.Chattable)
			assert.Empty(t, item.BlockReason)
			assert.Empty(t, item.ChattableHint)
		})

		convey.Convey("可对话（CLI 后端走自身 login）→ blockReason 为空串", func() {
			m := setupChatTest(t)
			item := listSingleAgentItem(t, m,
				&agent_entity.Agent{ID: 2, Name: "B", AgentBackendID: 11, Status: consts.ACTIVE},
				&agent_backend_entity.AgentBackend{ID: 11, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "", Status: consts.ACTIVE},
				map[string]*llm_provider_entity.LLMProvider{},
			)
			assert.True(t, item.Chattable)
			assert.Empty(t, item.BlockReason)
		})

		convey.Convey("no-backend（CEO 无后端）", func() {
			m := setupChatTest(t)
			item := listSingleAgentItem(t, m,
				&agent_entity.Agent{ID: 3, Name: "CEO 助手", SystemBadge: agent_entity.SystemBadgeDefault, AgentBackendID: 0, Status: consts.ACTIVE},
				nil,
				map[string]*llm_provider_entity.LLMProvider{},
			)
			assert.False(t, item.Chattable)
			assert.Equal(t, chat_svc.BlockReasonNoBackend, item.BlockReason)
		})

		convey.Convey("no-backend（普通 Agent 无后端）", func() {
			m := setupChatTest(t)
			item := listSingleAgentItem(t, m,
				&agent_entity.Agent{ID: 4, Name: "C", AgentBackendID: 0, Status: consts.ACTIVE},
				nil,
				map[string]*llm_provider_entity.LLMProvider{},
			)
			assert.False(t, item.Chattable)
			assert.Equal(t, chat_svc.BlockReasonNoBackend, item.BlockReason)
		})

		convey.Convey("已绑定的后端被删除时保留目标存在信号，供设置导航忽略不可见的残留关联", func() {
			m := setupChatTest(t)
			item := listSingleAgentItem(t, m,
				&agent_entity.Agent{ID: 40, Name: "Deleted backend", AgentBackendID: 99, Status: consts.ACTIVE},
				nil,
				map[string]*llm_provider_entity.LLMProvider{},
			)
			assert.False(t, item.Chattable)
			assert.Equal(t, chat_svc.BlockReasonNoBackend, item.BlockReason)
			assert.True(t, item.HasBackendTarget)
		})

		convey.Convey("backend-requires-provider（内置后端找不到绑定的供应商）", func() {
			m := setupChatTest(t)
			item := listSingleAgentItem(t, m,
				&agent_entity.Agent{ID: 5, Name: "D", AgentBackendID: 12, Status: consts.ACTIVE},
				&agent_backend_entity.AgentBackend{ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "missing-key", Status: consts.ACTIVE},
				map[string]*llm_provider_entity.LLMProvider{},
			)
			assert.False(t, item.Chattable)
			assert.Equal(t, chat_svc.BlockReasonBackendRequiresProvider, item.BlockReason)
		})

		convey.Convey("provider-inactive（后端绑的供应商存在但未激活）", func() {
			m := setupChatTest(t)
			item := listSingleAgentItem(t, m,
				&agent_entity.Agent{ID: 6, Name: "E", AgentBackendID: 13, Status: consts.ACTIVE},
				&agent_backend_entity.AgentBackend{ID: 13, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-2", Status: consts.ACTIVE},
				map[string]*llm_provider_entity.LLMProvider{"key-2": {ID: 2, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.BAN}},
			)
			assert.False(t, item.Chattable)
			assert.Equal(t, chat_svc.BlockReasonProviderInactive, item.BlockReason)
		})

		convey.Convey("remote-provider-missing（远端 agentred 未配置该供应商）", func() {
			mockRDS.EXPECT().ListDeviceProviders(int64(42)).Return([]remote_device_svc.ProviderSummary{
				{Key: "other-key", Name: "Other", Type: "anthropic"},
			})
			mockRDS.EXPECT().List(ctx).Return([]*remote_device_svc.DeviceView{
				{ID: 42, DaemonFingerprint: "sha256:device-42", Online: true},
			}, nil).AnyTimes()

			m := setupChatTest(t)
			item := listSingleAgentItem(t, m,
				&agent_entity.Agent{ID: 7, Name: "F", AgentBackendID: 14, Status: consts.ACTIVE},
				&agent_backend_entity.AgentBackend{ID: 14, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "key-3", DeviceFingerprint: "sha256:device-42", Status: consts.ACTIVE},
				map[string]*llm_provider_entity.LLMProvider{"key-3": {ID: 3, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}},
			)
			assert.False(t, item.Chattable)
			assert.Equal(t, chat_svc.BlockReasonRemoteProviderMissing, item.BlockReason)
		})

		convey.Convey("backend-requires-provider（CLI 后端绑定了不匹配的供应商）", func() {
			m := setupChatTest(t)
			item := listSingleAgentItem(t, m,
				&agent_entity.Agent{ID: 18, Name: "J", AgentBackendID: 19, Status: consts.ACTIVE},
				&agent_backend_entity.AgentBackend{ID: 19, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "key-5", Status: consts.ACTIVE},
				map[string]*llm_provider_entity.LLMProvider{"key-5": {ID: 5, Type: string(llm_provider_entity.TypeOpenAIResponse), Status: consts.ACTIVE}},
			)
			assert.False(t, item.Chattable)
			assert.Equal(t, chat_svc.BlockReasonBackendRequiresProvider, item.BlockReason)
		})

		convey.Convey("gateway-not-running（本地 CLI 后端 + 网关未启动）", func() {
			m := setupChatTest(t)
			item := listSingleAgentItem(t, m,
				&agent_entity.Agent{ID: 8, Name: "G", AgentBackendID: 15, Status: consts.ACTIVE},
				&agent_backend_entity.AgentBackend{ID: 15, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "key-4", Status: consts.ACTIVE},
				map[string]*llm_provider_entity.LLMProvider{"key-4": {ID: 4, Type: string(llm_provider_entity.TypeOpenAIResponse), Status: consts.ACTIVE}},
			)
			assert.False(t, item.Chattable)
			assert.Equal(t, chat_svc.BlockReasonGatewayNotRunning, item.BlockReason)
		})

		convey.Convey("remote-openclaw-unavailable（远端 OpenClaw 暂不可用）", func() {
			mockRDS.EXPECT().List(ctx).Return(nil, nil).AnyTimes()

			m := setupChatTest(t)
			item := listSingleAgentItem(t, m,
				&agent_entity.Agent{ID: 9, Name: "H", AgentBackendID: 16, Status: consts.ACTIVE},
				&agent_backend_entity.AgentBackend{ID: 16, Type: string(agent_backend_entity.TypeOpenClaw), DeviceFingerprint: "sha256:device-9", Status: consts.ACTIVE},
				map[string]*llm_provider_entity.LLMProvider{},
			)
			assert.False(t, item.Chattable)
			assert.Equal(t, chat_svc.BlockReasonRemoteOpenClawUnavailable, item.BlockReason)
		})

		convey.Convey("unknown-backend（未知 Agent 后端类型）", func() {
			m := setupChatTest(t)
			item := listSingleAgentItem(t, m,
				&agent_entity.Agent{ID: 10, Name: "I", AgentBackendID: 17, Status: consts.ACTIVE},
				&agent_backend_entity.AgentBackend{ID: 17, Type: "weird", Status: consts.ACTIVE},
				map[string]*llm_provider_entity.LLMProvider{},
			)
			assert.False(t, item.Chattable)
			assert.Equal(t, chat_svc.BlockReasonUnknownBackend, item.BlockReason)
		})
	})
}

func TestListAgents(t *testing.T) {
	convey.Convey("ListAgents", t, func() {
		m := setupChatTest(t)
		ctx := context.Background()

		convey.Convey("claudecode backend.DefaultPermissionMode 透出到 ChatAgentItem", func() {
			// 前端新会话场景下，pill 需要拿到 backend 管理员预设的默认 mode 才能
			// 正确显示并把它当 raw 透回 chat_svc.Send；ChatAgentItem 必须暴露此字段。
			m.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{
				{ID: 3, Name: "CC Eng", AgentBackendID: 17, Status: consts.ACTIVE},
			}, nil)
			m.backend.EXPECT().BatchFind(ctx, []int64{17}).Return(map[int64]*agent_backend_entity.AgentBackend{
				17: {
					ID:                    17,
					Type:                  string(agent_backend_entity.TypeClaudeCode),
					LLMProviderKey:        "",
					DefaultPermissionMode: "plan",
					Status:                consts.ACTIVE,
				},
			}, nil)
			m.provider.EXPECT().BatchFindByKey(ctx, []string{}).Return(map[string]*llm_provider_entity.LLMProvider{}, nil)
			m.session.EXPECT().CountRunningByAgents(ctx, []int64{3}).Return(map[int64]int{}, nil)
			m.session.EXPECT().CountByAgents(ctx, []int64{3}).Return(map[int64]int64{}, nil)
			m.session.EXPECT().ListIDsByAgents(ctx, []int64{3}).Return(map[int64][]int64{}, nil)
			m.session.EXPECT().ListByAgent(ctx, int64(3), 5).Return(nil, nil)
			m.session.EXPECT().ListAttentionByAgent(ctx, int64(3), 20).Return(nil, nil)

			resp, err := m.svc.ListAgents(ctx, &chat_svc.ListAgentsRequest{})
			assert.NoError(t, err)
			if assert.Len(t, resp.Agents, 1) {
				assert.Equal(t, "plan", resp.Agents[0].DefaultPermissionMode)
				assert.Equal(t, string(agent_backend_entity.TypeClaudeCode), resp.Agents[0].BackendType)
			}
		})

		convey.Convey("非 claudecode 后端不带 DefaultPermissionMode（codex 用自己的 collaboration mode）", func() {
			m.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{
				{ID: 4, Name: "Codex", AgentBackendID: 18, Status: consts.ACTIVE},
			}, nil)
			m.backend.EXPECT().BatchFind(ctx, []int64{18}).Return(map[int64]*agent_backend_entity.AgentBackend{
				18: {
					ID:             18,
					Type:           string(agent_backend_entity.TypeCodex),
					LLMProviderKey: "",
					Status:         consts.ACTIVE,
				},
			}, nil)
			m.provider.EXPECT().BatchFindByKey(ctx, []string{}).Return(map[string]*llm_provider_entity.LLMProvider{}, nil)
			m.session.EXPECT().CountRunningByAgents(ctx, []int64{4}).Return(map[int64]int{}, nil)
			m.session.EXPECT().CountByAgents(ctx, []int64{4}).Return(map[int64]int64{}, nil)
			m.session.EXPECT().ListIDsByAgents(ctx, []int64{4}).Return(map[int64][]int64{}, nil)
			m.session.EXPECT().ListByAgent(ctx, int64(4), 5).Return(nil, nil)
			m.session.EXPECT().ListAttentionByAgent(ctx, int64(4), 20).Return(nil, nil)

			resp, err := m.svc.ListAgents(ctx, &chat_svc.ListAgentsRequest{})
			assert.NoError(t, err)
			if assert.Len(t, resp.Agents, 1) {
				assert.Empty(t, resp.Agents[0].DefaultPermissionMode)
			}
		})

		convey.Convey("CEO 未置顶（DB pinned=0，无默认置顶回填），无后端时 Chattable=false", func() {
			m.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{
				{ID: 1, Name: "CEO 助手", SystemBadge: agent_entity.SystemBadgeDefault, Pinned: false, AgentBackendID: 0, Status: consts.ACTIVE},
				{ID: 2, Name: "工程师", AgentBackendID: 7, Status: consts.ACTIVE},
			}, nil)
			m.backend.EXPECT().BatchFind(ctx, []int64{7}).Return(map[int64]*agent_backend_entity.AgentBackend{
				7: {ID: 7, Type: "builtin", LLMProviderKey: "key-11", Status: consts.ACTIVE},
			}, nil)
			m.provider.EXPECT().BatchFindByKey(ctx, []string{"key-11"}).Return(map[string]*llm_provider_entity.LLMProvider{
				"key-11": {ID: 11, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE},
			}, nil)
			m.session.EXPECT().CountRunningByAgents(ctx, []int64{1, 2}).Return(map[int64]int{1: 0, 2: 3}, nil)
			m.session.EXPECT().CountByAgents(ctx, []int64{1, 2}).Return(map[int64]int64{1: 0, 2: 12}, nil)
			m.session.EXPECT().ListIDsByAgents(ctx, []int64{1, 2}).Return(map[int64][]int64{
				2: {99, 50, 49, 48, 47, 46},
			}, nil)
			m.session.EXPECT().ListAttentionByAgent(ctx, int64(1), 20).Return(nil, nil)
			m.session.EXPECT().ListAttentionByAgent(ctx, int64(2), 20).Return([]*chat_entity.Session{
				{ID: 50, AgentID: 2, Title: "approve me", AgentStatus: "waiting", LastMessageAt: 1700000005000},
			}, nil)
			m.session.EXPECT().ListByAgent(ctx, int64(1), 5).Return(nil, nil)
			m.session.EXPECT().ListByAgent(ctx, int64(2), 5).Return([]*chat_entity.Session{
				{ID: 99, AgentID: 2, Title: "支付小队 / 工程师", AgentStatus: "running", LastMessageAt: 1700000000000},
			}, nil)

			resp, err := m.svc.ListAgents(ctx, &chat_svc.ListAgentsRequest{})
			assert.NoError(t, err)
			assert.Len(t, resp.Agents, 2)
			assert.False(t, resp.Agents[0].Pinned)
			assert.False(t, resp.Agents[0].Chattable)
			assert.True(t, resp.Agents[1].Chattable)
			assert.Equal(t, 3, resp.Agents[1].ActiveCount)
			assert.Equal(t, []int64{99, 50, 49, 48, 47, 46}, resp.Agents[1].SessionIDs)
			assert.Equal(t, "支付小队 / 工程师", resp.Agents[1].Sessions[0].Title)
			assert.Len(t, resp.Agents[0].AttentionSessions, 0, "CEO 没 attention session")
			if assert.Len(t, resp.Agents[1].AttentionSessions, 1) {
				assert.Equal(t, int64(50), resp.Agents[1].AttentionSessions[0].ID)
				assert.True(t, resp.Agents[1].AttentionSessions[0].NeedsAttention)
			}
		})

		convey.Convey("Given an agent has more sessions than the recent sidebar limit, when ListAgents, then SessionIDs contains all active ids", func() {
			m.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{
				{ID: 9, Name: "Deep History", AgentBackendID: 19, Status: consts.ACTIVE},
			}, nil)
			m.backend.EXPECT().BatchFind(ctx, []int64{19}).Return(map[int64]*agent_backend_entity.AgentBackend{
				19: {ID: 19, Type: string(agent_backend_entity.TypeCodex), Status: consts.ACTIVE},
			}, nil)
			m.provider.EXPECT().BatchFindByKey(ctx, []string{}).Return(map[string]*llm_provider_entity.LLMProvider{}, nil)
			m.session.EXPECT().CountRunningByAgents(ctx, []int64{9}).Return(map[int64]int{}, nil)
			m.session.EXPECT().CountByAgents(ctx, []int64{9}).Return(map[int64]int64{9: 6}, nil)
			m.session.EXPECT().ListIDsByAgents(ctx, []int64{9}).Return(map[int64][]int64{
				9: {6, 5, 4, 3, 2, 1},
			}, nil)
			m.session.EXPECT().ListByAgent(ctx, int64(9), 5).Return([]*chat_entity.Session{
				{ID: 6, AgentID: 9, Title: "s6", AgentStatus: "idle"},
				{ID: 5, AgentID: 9, Title: "s5", AgentStatus: "idle"},
				{ID: 4, AgentID: 9, Title: "s4", AgentStatus: "idle"},
				{ID: 3, AgentID: 9, Title: "s3", AgentStatus: "idle"},
				{ID: 2, AgentID: 9, Title: "s2", AgentStatus: "idle"},
			}, nil)
			m.session.EXPECT().ListAttentionByAgent(ctx, int64(9), 20).Return(nil, nil)

			resp, err := m.svc.ListAgents(ctx, &chat_svc.ListAgentsRequest{})
			assert.NoError(t, err)
			if assert.Len(t, resp.Agents, 1) {
				assert.Len(t, resp.Agents[0].Sessions, 5)
				assert.Equal(t, []int64{6, 5, 4, 3, 2, 1}, resp.Agents[0].SessionIDs)
			}
		})
	})
}

func TestListAgents_PopulatesDeviceFields(t *testing.T) {
	convey.Convey("ListAgents device fields", t, func() {
		m := setupChatTest(t)
		ctx := context.Background()

		// 注入 mock remote_device_svc 并在测试结束后恢复 nil。
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		mockRDS := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
		// 本机指纹:用例里的 DeviceID 都是别机指纹,「是不是本机档」这一问对每次
		// 解析都要答一次。
		mockRDS.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()
		remote_device_svc.SetDefault(mockRDS)
		t.Cleanup(func() { remote_device_svc.SetDefault(nil) })

		convey.Convey("本地 backend (DeviceID='') → DeviceID/DeviceName/Online 均为零值", func() {
			m.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{
				{ID: 5, Name: "本地 Agent", AgentBackendID: 20, Status: consts.ACTIVE},
			}, nil)
			m.backend.EXPECT().BatchFind(ctx, []int64{20}).Return(map[int64]*agent_backend_entity.AgentBackend{
				20: {ID: 20, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "", LLMProviderKey: "", Status: consts.ACTIVE},
			}, nil)
			m.provider.EXPECT().BatchFindByKey(ctx, []string{}).Return(map[string]*llm_provider_entity.LLMProvider{}, nil)
			m.session.EXPECT().CountRunningByAgents(ctx, []int64{5}).Return(map[int64]int{}, nil)
			m.session.EXPECT().CountByAgents(ctx, []int64{5}).Return(map[int64]int64{}, nil)
			m.session.EXPECT().ListIDsByAgents(ctx, []int64{5}).Return(map[int64][]int64{}, nil)
			m.session.EXPECT().ListByAgent(ctx, int64(5), 5).Return(nil, nil)
			m.session.EXPECT().ListAttentionByAgent(ctx, int64(5), 20).Return(nil, nil)
			// 本地 backend 不触发 remote_device_svc.Get

			resp, err := m.svc.ListAgents(ctx, &chat_svc.ListAgentsRequest{})
			assert.NoError(t, err)
			if assert.Len(t, resp.Agents, 1) {
				assert.Equal(t, "", resp.Agents[0].DeviceID)
				assert.Equal(t, "", resp.Agents[0].DeviceName)
				assert.False(t, resp.Agents[0].Online)
			}
		})

		convey.Convey("远端 backend + device 在线 → DeviceID/DeviceName/Online 填充", func() {
			m.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{
				{ID: 6, Name: "远端 Agent", AgentBackendID: 21, Status: consts.ACTIVE},
			}, nil)
			m.backend.EXPECT().BatchFind(ctx, []int64{21}).Return(map[int64]*agent_backend_entity.AgentBackend{
				21: {ID: 21, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "sha256:device-7", LLMProviderKey: "", Status: consts.ACTIVE},
			}, nil)
			m.provider.EXPECT().BatchFindByKey(ctx, []string{}).Return(map[string]*llm_provider_entity.LLMProvider{}, nil)
			m.session.EXPECT().CountRunningByAgents(ctx, []int64{6}).Return(map[int64]int{}, nil)
			m.session.EXPECT().CountByAgents(ctx, []int64{6}).Return(map[int64]int64{}, nil)
			m.session.EXPECT().ListIDsByAgents(ctx, []int64{6}).Return(map[int64][]int64{}, nil)
			m.session.EXPECT().ListByAgent(ctx, int64(6), 5).Return(nil, nil)
			m.session.EXPECT().ListAttentionByAgent(ctx, int64(6), 20).Return(nil, nil)
			mockRDS.EXPECT().List(ctx).Return([]*remote_device_svc.DeviceView{
				{ID: 7, DaemonFingerprint: "sha256:device-7", Name: "linux-srv", Online: true},
			}, nil)

			resp, err := m.svc.ListAgents(ctx, &chat_svc.ListAgentsRequest{})
			assert.NoError(t, err)
			if assert.Len(t, resp.Agents, 1) {
				assert.Equal(t, "sha256:device-7", resp.Agents[0].DeviceID)
				assert.Equal(t, "linux-srv", resp.Agents[0].DeviceName)
				assert.True(t, resp.Agents[0].Online)
			}
		})

		convey.Convey("远端 backend + device 查询失败 → DeviceID 填入但 DeviceName/Online 留零值（不报错）", func() {
			m.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{
				{ID: 7, Name: "孤儿 Agent", AgentBackendID: 22, Status: consts.ACTIVE},
			}, nil)
			m.backend.EXPECT().BatchFind(ctx, []int64{22}).Return(map[int64]*agent_backend_entity.AgentBackend{
				22: {ID: 22, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "sha256:device-9", LLMProviderKey: "", Status: consts.ACTIVE},
			}, nil)
			m.provider.EXPECT().BatchFindByKey(ctx, []string{}).Return(map[string]*llm_provider_entity.LLMProvider{}, nil)
			m.session.EXPECT().CountRunningByAgents(ctx, []int64{7}).Return(map[int64]int{}, nil)
			m.session.EXPECT().CountByAgents(ctx, []int64{7}).Return(map[int64]int64{}, nil)
			m.session.EXPECT().ListIDsByAgents(ctx, []int64{7}).Return(map[int64][]int64{}, nil)
			m.session.EXPECT().ListByAgent(ctx, int64(7), 5).Return(nil, nil)
			m.session.EXPECT().ListAttentionByAgent(ctx, int64(7), 20).Return(nil, nil)
			mockRDS.EXPECT().List(ctx).Return(nil, errors.New("device not found"))

			resp, err := m.svc.ListAgents(ctx, &chat_svc.ListAgentsRequest{})
			assert.NoError(t, err)
			if assert.Len(t, resp.Agents, 1) {
				assert.Equal(t, "sha256:device-9", resp.Agents[0].DeviceID, "DeviceID 应填入即使 device 查询失败")
				assert.Equal(t, "", resp.Agents[0].DeviceName)
				assert.False(t, resp.Agents[0].Online)
			}
		})
	})
}

func TestLoadSession(t *testing.T) {
	convey.Convey("LoadSession", t, func() {
		m := setupChatTest(t)
		ctx := context.Background()

		convey.Convey("正常返回 detail + messages 按 seq 升序", func() {
			m.session.EXPECT().Find(ctx, int64(3)).Return(&chat_entity.Session{
				ID: 3, AgentID: 7, Title: "draft", AgentStatus: "idle", LastMessageAt: 1, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(ctx, int64(7)).Return(&agent_entity.Agent{
				ID: 7, Name: "Eng", AvatarColor: "agent-2", AgentBackendID: 22, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(22)).Return(&agent_backend_entity.AgentBackend{
				ID: 22, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
			}, nil)
			expectTranscriptWindowFilled(m)
			m.message.EXPECT().ListMeta(ctx, int64(3)).Return([]*chat_entity.Message{
				{ID: 10, SessionID: 3, Role: "user", BlocksJSON: `[{"type":"text","data":{"text":"hi"}}]`, Seq: 1},
				{ID: 11, SessionID: 3, Role: "assistant", BlocksJSON: `[{"type":"text","data":{"text":"hello"}}]`, Seq: 2, Model: "claude-sonnet-4-6"},
			}, nil)

			resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 3})
			assert.NoError(t, err)
			assert.Equal(t, "Eng", resp.Session.AgentName)
			assert.Equal(t, string(agent_backend_entity.TypeClaudeCode), resp.Session.BackendType)
			assert.Len(t, resp.Messages, 2)
			assert.Equal(t, "hi", resp.Messages[0].Blocks[0].Text)
			assert.Equal(t, "claude-sonnet-4-6", resp.Messages[1].Model)
		})

		convey.Convey("session 不存在 → ChatSessionNotFound", func() {
			m.session.EXPECT().Find(ctx, int64(99)).Return(nil, nil)
			_, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 99})
			assert.Error(t, err)
		})

		convey.Convey("LLMProviderType 透传到 detail（前端按它判定 Usage 字段语义）", func() {
			convey.Convey("builtin + anthropic provider → llmProviderType=anthropic", func() {
				expectLoadSessionBackend(m, ctx, 20, 60, 70, agent_backend_entity.TypeBuiltin, &llm_provider_entity.LLMProvider{
					ID: 80, ProviderKey: "key-80", Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE,
				})

				resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 20})
				assert.NoError(t, err)
				assert.Equal(t, string(llm_provider_entity.TypeAnthropic), resp.Session.LLMProviderType)
			})

			convey.Convey("builtin + openai-chat provider → llmProviderType=openai-chat", func() {
				expectLoadSessionBackend(m, ctx, 21, 61, 71, agent_backend_entity.TypeBuiltin, &llm_provider_entity.LLMProvider{
					ID: 81, ProviderKey: "key-81", Type: string(llm_provider_entity.TypeOpenAIChat), Status: consts.ACTIVE,
				})

				resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 21})
				assert.NoError(t, err)
				assert.Equal(t, string(llm_provider_entity.TypeOpenAIChat), resp.Session.LLMProviderType)
			})

			convey.Convey("backend 无 provider 绑定（CLI 登录态）→ llmProviderType 留空", func() {
				m.session.EXPECT().Find(ctx, int64(22)).Return(&chat_entity.Session{ID: 22, AgentID: 62, Status: consts.ACTIVE}, nil)
				m.agent.EXPECT().Find(ctx, int64(62)).Return(&agent_entity.Agent{ID: 62, AgentBackendID: 72, Status: consts.ACTIVE}, nil)
				m.backend.EXPECT().Find(ctx, int64(72)).Return(&agent_backend_entity.AgentBackend{
					ID: 72, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "", Status: consts.ACTIVE,
				}, nil)
				expectTranscriptWindowFilled(m)
				m.message.EXPECT().ListMeta(ctx, int64(22)).Return(nil, nil)

				resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 22})
				assert.NoError(t, err)
				assert.Empty(t, resp.Session.LLMProviderType)
			})
		})

		convey.Convey("provider.ContextWindow > 0 → 直接透传到 detail", func() {
			m.session.EXPECT().Find(ctx, int64(4)).Return(&chat_entity.Session{
				ID: 4, AgentID: 8, Title: "ctx", Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(ctx, int64(8)).Return(&agent_entity.Agent{
				ID: 8, Name: "Eng", AgentBackendID: 33, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(33)).Return(&agent_backend_entity.AgentBackend{
				ID: 33, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-44", Status: consts.ACTIVE,
			}, nil)
			m.provider.EXPECT().FindByKey(ctx, "key-44").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-44", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-44", ID: 44, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
			// 解析出的模型显式配了 200k（EffectiveLLMConfig v1 seam：ContextWindow 来自模型，不再读 Provider 行）。
			m.provider.EXPECT().FindModelByKey(ctx, "mk-key-44").Return(
				&llm_provider_model_entity.LLMProviderModel{ModelKey: "mk-key-44", ModelID: "claude-sonnet-4-6", ContextWindow: 200000, Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE},
				nil).AnyTimes()
			expectTranscriptWindowFilled(m)
			m.message.EXPECT().ListMeta(ctx, int64(4)).Return(nil, nil)

			resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 4})
			assert.NoError(t, err)
			assert.Equal(t, 200000, resp.Session.ContextWindow)
		})

		convey.Convey("provider.ContextWindow == 0 → 走 cago catalog 兜底", func() {
			m.session.EXPECT().Find(ctx, int64(5)).Return(&chat_entity.Session{
				ID: 5, AgentID: 9, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(ctx, int64(9)).Return(&agent_entity.Agent{
				ID: 9, AgentBackendID: 34, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(34)).Return(&agent_backend_entity.AgentBackend{
				ID: 34, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "key-45", Status: consts.ACTIVE,
			}, nil)
			// ContextWindow 留 0；Model 取 cago 内置 catalog 已知的 claude-sonnet-4-6
			m.provider.EXPECT().FindByKey(ctx, "key-45").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-45", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-45", ID: 45, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
			m.provider.EXPECT().FindModelByKey(ctx, "mk-key-45").Return(
				&llm_provider_model_entity.LLMProviderModel{ModelKey: "mk-key-45", ModelID: "claude-sonnet-4-6", Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE},
				nil).AnyTimes()
			expectTranscriptWindowFilled(m)
			m.message.EXPECT().ListMeta(ctx, int64(5)).Return(nil, nil)

			resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 5})
			assert.NoError(t, err)
			assert.Greater(t, resp.Session.ContextWindow, 0, "应从 cago catalog 兜底解析出 contextWindow")
		})

		convey.Convey("backend 无 provider 绑定 → contextWindow 留 0", func() {
			m.session.EXPECT().Find(ctx, int64(6)).Return(&chat_entity.Session{
				ID: 6, AgentID: 10, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(ctx, int64(10)).Return(&agent_entity.Agent{
				ID: 10, AgentBackendID: 35, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(35)).Return(&agent_backend_entity.AgentBackend{
				ID: 35, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "", Status: consts.ACTIVE,
			}, nil)
			expectTranscriptWindowFilled(m)
			m.message.EXPECT().ListMeta(ctx, int64(6)).Return(nil, nil)

			resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 6})
			assert.NoError(t, err)
			assert.Equal(t, 0, resp.Session.ContextWindow)
		})

		convey.Convey("CLI login + 最新 assistant.Model → 走 catalog 解析（无 provider 也能拿到 contextWindow）", func() {
			// claudecode CLI 自身 login（LLMProviderKey=""），runner 已把 system.init.model 写回 message.Model。
			m.session.EXPECT().Find(ctx, int64(7)).Return(&chat_entity.Session{
				ID: 7, AgentID: 11, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(ctx, int64(11)).Return(&agent_entity.Agent{
				ID: 11, AgentBackendID: 40, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(40)).Return(&agent_backend_entity.AgentBackend{
				ID: 40, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "", Status: consts.ACTIVE,
			}, nil)
			expectTranscriptWindowFilled(m)
			m.message.EXPECT().ListMeta(ctx, int64(7)).Return([]*chat_entity.Message{
				{ID: 80, SessionID: 7, Role: "user", BlocksJSON: "[]", Seq: 1},
				{ID: 81, SessionID: 7, Role: "assistant", BlocksJSON: "[]", Seq: 2, Model: "claude-sonnet-4-6"},
			}, nil)

			resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 7})
			assert.NoError(t, err)
			assert.Greater(t, resp.Session.ContextWindow, 0, "无 provider 也应从 message.Model 反查 catalog 拿 contextWindow")
		})

		convey.Convey("claudecode + provider 未显式配置 ContextWindow → 用 message.Model 反映实际运行模型", func() {
			// claudecode 后端，provider.Model=sonnet 但 ContextWindow=0（未显式配），message.Model=haiku-4-5。
			// 新优先级：第 1 级 provider.ContextWindow 未命中（=0）→ 落到第 2 级 message.Model catalog。
			assertLoadSessionContextWindow(t, m, ctx, 8, 12, 41, 46, "key-46", 0, 200000, "应取 haiku 的 200k，而不是 sonnet 的 1M")
		})

		convey.Convey("claudecode + provider.ContextWindow > 0 → 显式配置覆盖 message.Model catalog", func() {
			// 核心新行为：LLM 供应商显式配了 500k，即使 message.Model=haiku（catalog 200k），也应取 500k。
			assertLoadSessionContextWindow(t, m, ctx, 9, 13, 42, 47, "key-47", 500000, 500000, "provider 显式 ContextWindow 应覆盖 message.Model catalog")
		})

		convey.Convey("session.ContextWindow > 0 → runtime 上报值覆盖所有 fallback", func() {
			// codex app-server 推的 modelContextWindow 已经落到 session.context_window 列，
			// 即使 provider 配了不同的窗口、message.Model 也指向另一个模型，runtime 实测值最权威。
			m.session.EXPECT().Find(ctx, int64(10)).Return(&chat_entity.Session{
				ID: 10, AgentID: 14, ContextWindow: 258400, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(ctx, int64(14)).Return(&agent_entity.Agent{
				ID: 14, AgentBackendID: 43, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(43)).Return(&agent_backend_entity.AgentBackend{
				ID: 43, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "key-48", Status: consts.ACTIVE,
			}, nil)
			m.provider.EXPECT().FindByKey(ctx, "key-48").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-48", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-48", ID: 48, Type: string(llm_provider_entity.TypeOpenAIResponse), Status: consts.ACTIVE}, nil).AnyTimes()
			expectProviderResolvable(m, "key-48")
			expectTranscriptWindowFilled(m)
			m.message.EXPECT().ListMeta(ctx, int64(10)).Return([]*chat_entity.Message{
				{ID: 110, SessionID: 10, Role: "assistant", BlocksJSON: "[]", Seq: 1, Model: "gpt-5-codex"},
			}, nil)

			resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 10})
			assert.NoError(t, err)
			assert.Equal(t, 258400, resp.Session.ContextWindow, "runtime 上报值应优先于 provider 配置和 catalog")
		})

		convey.Convey("message.Model 带未注册的 dated 后缀 → llmcatalog.Lookup 前缀匹配兜底", func() {
			// 模拟 runner 上报 haiku 的新日期版本（cago alias 还没收录这个具体日期），
			// llmcatalog.Lookup 按前缀匹配命中 claude-haiku-4-5 的 200k 窗口。
			m.session.EXPECT().Find(ctx, int64(11)).Return(&chat_entity.Session{
				ID: 11, AgentID: 15, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(ctx, int64(15)).Return(&agent_entity.Agent{
				ID: 15, AgentBackendID: 44, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(44)).Return(&agent_backend_entity.AgentBackend{
				ID: 44, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "", Status: consts.ACTIVE,
			}, nil)
			expectTranscriptWindowFilled(m)
			m.message.EXPECT().ListMeta(ctx, int64(11)).Return([]*chat_entity.Message{
				{ID: 120, SessionID: 11, Role: "assistant", BlocksJSON: "[]", Seq: 1, Model: "claude-haiku-4-5-20260515"},
			}, nil)

			resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 11})
			assert.NoError(t, err)
			assert.Equal(t, 200000, resp.Session.ContextWindow, "前缀匹配 claude-haiku-4-5 应拿到 200k 窗口")
		})
	})
}

func TestLoadSession_PopulatesDeviceFields(t *testing.T) {
	convey.Convey("LoadSession device fields", t, func() {
		m := setupChatTest(t)
		ctx := context.Background()

		// 注入 mock remote_device_svc 并在测试结束后恢复 nil。
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		mockRDS := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
		// 本机指纹:用例里的 DeviceID 都是别机指纹,「是不是本机档」这一问对每次
		// 解析都要答一次。
		mockRDS.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()
		remote_device_svc.SetDefault(mockRDS)
		t.Cleanup(func() { remote_device_svc.SetDefault(nil) })

		// 注入 CwdResolver 并在测试结束后清空。
		chat_svc.RegisterCwdResolver(func(_ context.Context, _ *chat_entity.Session) (string, error) {
			return "/Users/me/proj", nil
		})
		t.Cleanup(func() { chat_svc.RegisterCwdResolver(nil) })

		convey.Convey("本地 backend (DeviceID='') → DeviceID/DeviceName/Online 均为零值, Cwd 由 CwdResolver 填充", func() {
			m.session.EXPECT().Find(ctx, int64(100)).Return(&chat_entity.Session{
				ID: 100, AgentID: 50, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(ctx, int64(50)).Return(&agent_entity.Agent{
				ID: 50, Name: "本地 Agent", AgentBackendID: 60, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(60)).Return(&agent_backend_entity.AgentBackend{
				ID: 60, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "", Status: consts.ACTIVE,
			}, nil)
			expectTranscriptWindowFilled(m)
			m.message.EXPECT().ListMeta(ctx, int64(100)).Return(nil, nil)

			resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 100})
			assert.NoError(t, err)
			assert.Equal(t, "", resp.Session.DeviceID)
			assert.Equal(t, "", resp.Session.DeviceName)
			assert.False(t, resp.Session.Online)
			assert.Equal(t, "/Users/me/proj", resp.Session.Cwd)
		})

		convey.Convey("远端 backend + device 在线 → DeviceID/DeviceName/Online 填充", func() {
			m.session.EXPECT().Find(ctx, int64(101)).Return(&chat_entity.Session{
				ID: 101, AgentID: 51, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(ctx, int64(51)).Return(&agent_entity.Agent{
				ID: 51, Name: "远端 Agent", AgentBackendID: 61, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(61)).Return(&agent_backend_entity.AgentBackend{
				ID: 61, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "sha256:device-7", Status: consts.ACTIVE,
			}, nil)
			mockRDS.EXPECT().List(ctx).Return([]*remote_device_svc.DeviceView{
				{ID: 7, DaemonFingerprint: "sha256:device-7", Name: "linux-srv", Online: true},
			}, nil)
			expectTranscriptWindowFilled(m)
			m.message.EXPECT().ListMeta(ctx, int64(101)).Return(nil, nil)

			resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 101})
			assert.NoError(t, err)
			assert.Equal(t, "sha256:device-7", resp.Session.DeviceID)
			assert.Equal(t, "linux-srv", resp.Session.DeviceName)
			assert.True(t, resp.Session.Online)
		})

		convey.Convey("远端 backend + device 查询失败 → DeviceID 填入但 DeviceName/Online 留零值（不报错）", func() {
			m.session.EXPECT().Find(ctx, int64(102)).Return(&chat_entity.Session{
				ID: 102, AgentID: 52, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(ctx, int64(52)).Return(&agent_entity.Agent{
				ID: 52, Name: "孤儿 Agent", AgentBackendID: 62, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(62)).Return(&agent_backend_entity.AgentBackend{
				ID: 62, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "sha256:device-9", Status: consts.ACTIVE,
			}, nil)
			mockRDS.EXPECT().List(ctx).Return(nil, errors.New("device not found"))
			expectTranscriptWindowFilled(m)
			m.message.EXPECT().ListMeta(ctx, int64(102)).Return(nil, nil)

			resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 102})
			assert.NoError(t, err)
			assert.Equal(t, "sha256:device-9", resp.Session.DeviceID, "DeviceID 应填入即使 device 查询失败")
			assert.Equal(t, "", resp.Session.DeviceName)
			assert.False(t, resp.Session.Online)
		})

		convey.Convey("会话钉在非默认档(sess.ExecAgentBackendID) → 聊天头解析那一档而不是 Agent 的默认档(R15b)", func() {
			m.session.EXPECT().Find(ctx, int64(103)).Return(&chat_entity.Session{
				ID: 103, AgentID: 53, Status: consts.ACTIVE, ExecAgentBackendID: 72,
			}, nil)
			m.agent.EXPECT().Find(ctx, int64(53)).Return(&agent_entity.Agent{
				// Agent 的默认档是 71，会话却钉在 72 上——聊天头必须展示 72 的设备信息。
				ID: 53, Name: "多档 Agent", AgentBackendID: 71, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(72)).Return(&agent_backend_entity.AgentBackend{
				ID: 72, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "sha256:device-9", Status: consts.ACTIVE,
			}, nil)
			mockRDS.EXPECT().List(ctx).Return([]*remote_device_svc.DeviceView{
				{ID: 9, DaemonFingerprint: "sha256:device-9", Name: "pinned-device", Online: true},
			}, nil)
			expectTranscriptWindowFilled(m)
			m.message.EXPECT().ListMeta(ctx, int64(103)).Return(nil, nil)

			resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 103})
			assert.NoError(t, err)
			assert.Equal(t, "sha256:device-9", resp.Session.DeviceID, "必须解析钉住的档(72→设备9)，不是 Agent 默认档(71)")
			assert.Equal(t, "pinned-device", resp.Session.DeviceName)
		})

		convey.Convey("钉住的那一档已被删除 → 展示口径回落 Agent 当前第一档，后端字段不留空", func() {
			// 会话钉在 72 上，那一档后来被删（Find 只认活跃行 → nil）。展示口径若不做
			// 恢复，整组后端字段留空，前端 activeBackendType 为空串，composer 的模型
			// pill 与权限模式 pill 一起不渲染——直到用户发一条消息，执行侧
			// resolveAgentBackend 的恢复边界把钉档换成活的，reload 才把它们带回来。
			// 展示与执行必须同一口径。
			m.session.EXPECT().Find(ctx, int64(104)).Return(&chat_entity.Session{
				ID: 104, AgentID: 54, Status: consts.ACTIVE, ExecAgentBackendID: 72,
			}, nil)
			m.agent.EXPECT().Find(ctx, int64(54)).Return(&agent_entity.Agent{
				ID: 54, Name: "钉档已删", AgentBackendID: 71, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(72)).Return(nil, nil)
			m.backend.EXPECT().Find(ctx, int64(71)).Return(&agent_backend_entity.AgentBackend{
				ID: 71, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
			}, nil)
			expectTranscriptWindowFilled(m)
			m.message.EXPECT().ListMeta(ctx, int64(104)).Return(nil, nil)

			resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 104})
			assert.NoError(t, err)
			assert.Equal(t, string(agent_backend_entity.TypePiAgent), resp.Session.BackendType,
				"钉档已删时必须回落 Agent 当前第一档，否则前端 activeBackendType 为空、composer 的 pill 全部不渲染")
		})
	})
}

// TestLoadSession_PopulatesCwdUnavailableReason 锁住 R10 的最后一环：会话文件
// 面板要能区分"本机未配置路径"和其它没有 cwd 的情形，靠的就是这个字段——Wails
// 边界只过 Error() 字符串，没有它前端只能拿到空 cwd，猜不出具体原因。
func TestLoadSession_PopulatesCwdUnavailableReason(t *testing.T) {
	convey.Convey("LoadSession 把 resolveSessionCwd 的错误分类成 CwdUnavailableReason（R10）", t, func() {
		m := setupChatTest(t)
		ctx := context.Background()

		convey.Convey("本机未配置路径 → local-path-missing，Cwd 留空", func() {
			chat_svc.RegisterCwdResolver(func(_ context.Context, _ *chat_entity.Session) (string, error) {
				return "", i18n.NewError(context.Background(), code.ProjectLocalPathMissing)
			})
			t.Cleanup(func() { chat_svc.RegisterCwdResolver(nil) })

			m.session.EXPECT().Find(ctx, int64(200)).Return(&chat_entity.Session{
				ID: 200, AgentID: 60, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(ctx, int64(60)).Return(&agent_entity.Agent{
				ID: 60, Name: "本机未配置", AgentBackendID: 80, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(80)).Return(&agent_backend_entity.AgentBackend{
				ID: 80, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "", Status: consts.ACTIVE,
			}, nil)
			expectTranscriptWindowFilled(m)
			m.message.EXPECT().ListMeta(ctx, int64(200)).Return(nil, nil)

			resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 200})
			assert.NoError(t, err)
			assert.Equal(t, "", resp.Session.Cwd)
			assert.Equal(t, "local-path-missing", resp.Session.CwdUnavailableReason)
		})

		convey.Convey("cwd 正常解析 → CwdUnavailableReason 留空", func() {
			chat_svc.RegisterCwdResolver(func(_ context.Context, _ *chat_entity.Session) (string, error) {
				return "/Users/me/proj", nil
			})
			t.Cleanup(func() { chat_svc.RegisterCwdResolver(nil) })

			m.session.EXPECT().Find(ctx, int64(201)).Return(&chat_entity.Session{
				ID: 201, AgentID: 61, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(ctx, int64(61)).Return(&agent_entity.Agent{
				ID: 61, Name: "正常", AgentBackendID: 81, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(81)).Return(&agent_backend_entity.AgentBackend{
				ID: 81, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "", Status: consts.ACTIVE,
			}, nil)
			expectTranscriptWindowFilled(m)
			m.message.EXPECT().ListMeta(ctx, int64(201)).Return(nil, nil)

			resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 201})
			assert.NoError(t, err)
			assert.Equal(t, "/Users/me/proj", resp.Session.Cwd)
			assert.Equal(t, "", resp.Session.CwdUnavailableReason)
		})
	})
}

func TestLoadSession_PopulatesExecTargetCount(t *testing.T) {
	convey.Convey("LoadSession 填充 ExecTargetCount 给聊天头 chip 守卫用（R15/R20）", t, func() {
		m := setupChatTest(t)
		ctx := context.Background()
		chat_svc.RegisterCwdResolver(func(_ context.Context, _ *chat_entity.Session) (string, error) {
			return "", nil
		})
		t.Cleanup(func() { chat_svc.RegisterCwdResolver(nil) })

		convey.Convey("单档/未设置执行目标列表的老 Agent → ExecTargetCount 为 0（走默认宽松桩）", func() {
			m.session.EXPECT().Find(ctx, int64(111)).Return(&chat_entity.Session{
				ID: 111, AgentID: 55, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(ctx, int64(55)).Return(&agent_entity.Agent{
				ID: 55, Name: "单档", AgentBackendID: 91, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(91)).Return(&agent_backend_entity.AgentBackend{
				ID: 91, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
			}, nil)
			expectTranscriptWindowFilled(m)
			m.message.EXPECT().ListMeta(ctx, int64(111)).Return(nil, nil)

			resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 111})
			assert.NoError(t, err)
			assert.Equal(t, 0, resp.Session.ExecTargetCount)
		})
	})
}

func TestGetLaunchCommand(t *testing.T) {
	convey.Convey("GetLaunchCommand", t, func() {
		t.Setenv("AGENTRE_DATA_DIR", t.TempDir()) // 让 agentruntime.AgentCwd 落在临时目录

		// gateway 注入：BuildLaunchCommand 在 provider 非空时需要 gateway URL。
		chat_svc.RegisterGateway(&fakeChatGateway{
			status: httpgateway.GatewayStatus{State: "running", URL: "http://127.0.0.1:60080"},
		})
		t.Cleanup(func() { chat_svc.RegisterGateway(nil) })

		m := setupChatTest(t)
		ctx := context.Background()

		convey.Convey("claudecode + provider → 单行命令含 BASE_URL、永久 token、model、--resume", func() {
			expectLaunchCommandBackend(m, ctx, 3, 7, 22, agent_backend_entity.TypeClaudeCode, "sess-uuid", &llm_provider_entity.LLMProvider{
				ID: 33, ProviderKey: "key-33", Type: string(llm_provider_entity.TypeAnthropic),
				Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-33", Status: consts.ACTIVE,
			})
			m.provider.EXPECT().FindModelByKey(ctx, "mk-key-33").Return(
				&llm_provider_model_entity.LLMProviderModel{ModelKey: "mk-key-33", ModelID: "claude-sonnet-4-6", Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE},
				nil).AnyTimes()

			command := loadLaunchCommand(t, m, ctx, 3, agent_backend_entity.TypeClaudeCode)
			// gateway URL + fake gateway 发出的真实 token（"chat-token"）
			assert.Contains(t, command, "ANTHROPIC_BASE_URL='http://127.0.0.1:60080'")
			assert.Contains(t, command, "ANTHROPIC_AUTH_TOKEN='chat-token'")
			// 没有 <TOKEN> 占位符泄漏
			assert.NotContains(t, command, "<TOKEN>")
			// model + resume
			assert.Contains(t, command, "claude --model claude-sonnet-4-6 --resume sess-uuid")
		})

		convey.Convey("codex + provider session → 单行命令用 resume 子命令带 session id", func() {
			expectLaunchCommandBackend(m, ctx, 6, 8, 23, agent_backend_entity.TypeCodex, "codex-thread-123", &llm_provider_entity.LLMProvider{
				ID: 34, ProviderKey: "key-34", Type: string(llm_provider_entity.TypeOpenAIResponse),
				Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-34", Status: consts.ACTIVE,
			})
			m.provider.EXPECT().FindModelByKey(ctx, "mk-key-34").Return(
				&llm_provider_model_entity.LLMProviderModel{ModelKey: "mk-key-34", ModelID: "gpt-5-codex", Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE},
				nil).AnyTimes()

			command := loadLaunchCommand(t, m, ctx, 6, agent_backend_entity.TypeCodex)
			assert.Contains(t, command, "OPENAI_API_KEY='chat-token'")
			assert.Contains(t, command, "codex resume")
			assert.Contains(t, command, " codex-thread-123")
			assert.Contains(t, command, `-c 'model="gpt-5-codex"'`)
		})

		convey.Convey("piagent → 单行命令用原生 Session ID 恢复当前 chat session", func() {
			m.session.EXPECT().Find(ctx, int64(9)).Return(&chat_entity.Session{
				ID: 9, AgentID: 10, ProviderSessionID: "pi-native-9", Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(ctx, int64(10)).Return(&agent_entity.Agent{
				ID: 10, AgentBackendID: 24, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(24)).Return(&agent_backend_entity.AgentBackend{
				ID: 24, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
			}, nil)

			command := loadLaunchCommand(t, m, ctx, 9, agent_backend_entity.TypePiAgent)
			assert.Contains(t, command, "pi --session pi-native-9")
			assert.NotContains(t, command, "--session-dir")
			assert.NotContains(t, command, "agentre-9.jsonl")
			assert.NotContains(t, command, "--mode rpc")
		})

		convey.Convey("会话力度覆盖后端配置 → 复制的启动命令带会话力度且原 backend 实体不受影响", func() {
			be := &agent_backend_entity.AgentBackend{
				ID: 25, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
				ReasoningEffort: agent_backend_entity.ReasoningEffortLow,
			}
			m.session.EXPECT().Find(ctx, int64(11)).Return(&chat_entity.Session{
				ID: 11, AgentID: 11, Status: consts.ACTIVE, ReasoningEffort: agent_backend_entity.ReasoningEffortMax,
			}, nil)
			m.agent.EXPECT().Find(ctx, int64(11)).Return(&agent_entity.Agent{
				ID: 11, AgentBackendID: 25, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(25)).Return(be, nil)

			command := loadLaunchCommand(t, m, ctx, 11, agent_backend_entity.TypeClaudeCode)
			assert.Contains(t, command, "--effort max", "复制出去的命令要带会话覆盖后的有效力度")
			assert.NotContains(t, command, "--effort low")
			assert.Equal(t, agent_backend_entity.ReasoningEffortLow, be.ReasoningEffort, "解析出的后端实体本身不能被改写")
		})

		convey.Convey("会话力度为空 → 复制的启动命令回落后端配置的力度", func() {
			be := &agent_backend_entity.AgentBackend{
				ID: 26, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
				ReasoningEffort: agent_backend_entity.ReasoningEffortHigh,
			}
			m.session.EXPECT().Find(ctx, int64(12)).Return(&chat_entity.Session{
				ID: 12, AgentID: 12, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(ctx, int64(12)).Return(&agent_entity.Agent{
				ID: 12, AgentBackendID: 26, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(26)).Return(be, nil)

			command := loadLaunchCommand(t, m, ctx, 12, agent_backend_entity.TypeClaudeCode)
			assert.Contains(t, command, "--effort high", "会话力度为空时回落后端配置")
		})

		convey.Convey("builtin → ChatLaunchCommandNotAvailable", func() {
			m.session.EXPECT().Find(ctx, int64(4)).Return(&chat_entity.Session{ID: 4, AgentID: 9, Status: consts.ACTIVE}, nil)
			m.agent.EXPECT().Find(ctx, int64(9)).Return(&agent_entity.Agent{ID: 9, AgentBackendID: 5, Status: consts.ACTIVE}, nil)
			m.backend.EXPECT().Find(ctx, int64(5)).Return(&agent_backend_entity.AgentBackend{
				ID: 5, Type: string(agent_backend_entity.TypeBuiltin), Status: consts.ACTIVE,
			}, nil)

			_, err := m.svc.GetLaunchCommand(ctx, &chat_svc.LaunchCommandRequest{SessionID: 4})
			assert.Error(t, err)
		})

		convey.Convey("session 不存在 → ChatSessionNotFound", func() {
			m.session.EXPECT().Find(ctx, int64(404)).Return(nil, nil)
			_, err := m.svc.GetLaunchCommand(ctx, &chat_svc.LaunchCommandRequest{SessionID: 404})
			assert.Error(t, err)
		})

		convey.Convey("SessionID <= 0 → InvalidParameter", func() {
			_, err := m.svc.GetLaunchCommand(ctx, &chat_svc.LaunchCommandRequest{SessionID: 0})
			assert.Error(t, err)
		})
	})
}

type recordingRunner struct {
	requests chan agentruntime.RunRequest
}

// Capabilities 返一份联合 PermissionModeMeta —— recordingRunner 会同时被
// claudecode / codex 的测试 swap 进来,所以 AllowedModes 给两边的并集,
// SwitchableDuringTurn=true 保证不会误命中"飞行中拒切"分支。
type providerRecordingRunner struct {
	requests chan agentruntime.RunRequest
}

func (*providerRecordingRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{}
}

func (r *providerRecordingRunner) Run(_ context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	r.requests <- req
	events := make(chan agentruntime.Event, 1)
	events <- agentruntime.Done{}
	close(events)
	return events, &agentruntime.RunResult{ProviderSessionID: req.ProviderSessionID}, nil
}

func (*recordingRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Set: map[capability.Capability]bool{
			capability.CapImageInput: true,
		},
		PermissionModeMeta: capability.PermissionModeMeta{
			AllowedModes:         []string{"default", "acceptEdits", "plan", "bypassPermissions"},
			DefaultMode:          "acceptEdits",
			SwitchableDuringTurn: true,
		},
	}
}
func (r *recordingRunner) Run(_ context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	r.requests <- req
	events := make(chan agentruntime.Event, 1)
	events <- agentruntime.TextDelta{Text: "ok"}
	close(events)
	return events, &agentruntime.RunResult{ProviderSessionID: "builtin-100"}, nil
}

func TestSend_ImageInput(t *testing.T) {
	convey.Convey("Send image input", t, func() {
		convey.Convey("Given image-only message on image-capable builtin backend, when Send, then user blocks and RunRequest carry ImageBlock", func() {
			t.Setenv("AGENTRE_DATA_DIR", t.TempDir())
			m := setupChatTest(t)
			ctx := m.ctx
			runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
			t.Cleanup(restore)

			sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE}
			backend := &agent_backend_entity.AgentBackend{
				ID:             12,
				Type:           string(agent_backend_entity.TypeBuiltin),
				LLMProviderKey: "key-11",
				Status:         consts.ACTIVE,
			}
			agent := &agent_entity.Agent{ID: 7, Name: "Builtin", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`}
			provider := &llm_provider_entity.LLMProvider{
				ProviderKey: "key-11", Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE,
				Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-11",
			}

			m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(agent, nil)
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(backend, nil)
			m.provider.EXPECT().FindByKey(gomock.Any(), "key-11").Return(provider, nil).AnyTimes()
			expectProviderResolvable(m, "key-11")
			m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
			m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
			m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

			var userBlocks []blocks.ContentBlock
			m.dbMock.ExpectBegin()
			m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
			m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
					if msg.Role == "user" {
						msg.ID = 1000
						var err error
						userBlocks, err = msg.GetBlocks()
						require.NoError(t, err)
					} else {
						msg.ID = 1001
					}
					return nil
				}).Times(2)
			m.dbMock.ExpectCommit()

			resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{
				SessionID: 100,
				AgentID:   7,
				Images: []chat_svc.SendImage{{
					Name:    "shot.png",
					DataURL: "data:image/png;base64,iVBORw0KGgo=",
				}},
			})
			require.NoError(t, err)
			var req agentruntime.RunRequest
			select {
			case req = <-runner.requests:
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for runtime request")
			}
			chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

			require.Len(t, userBlocks, 1)
			img, ok := userBlocks[0].(blocks.ImageBlock)
			require.True(t, ok)
			assert.Equal(t, "image/png", img.MediaType)
			assert.Equal(t, []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}, img.Source.Inline)
			assert.Empty(t, req.UserText)
			require.Len(t, req.UserBlocks, 1)
			reqImg, ok := req.UserBlocks[0].(blocks.ImageBlock)
			require.True(t, ok)
			assert.Equal(t, img.MediaType, reqImg.MediaType)
			assert.Equal(t, img.Source.Inline, reqImg.Source.Inline)
		})

		convey.Convey("Given invalid image data URL, when Send, then it fails before repository calls", func() {
			m := setupChatTest(t)
			_, err := m.svc.Send(context.Background(), &chat_svc.SendRequest{
				AgentID: 7,
				Images: []chat_svc.SendImage{{
					Name:    "bad.txt",
					DataURL: "data:text/plain;base64,aGVsbG8=",
				}},
			})
			assert.Error(t, err)
		})

		convey.Convey("Given image message on backend without image capability, when Send, then it fails before persisting the turn", func() {
			m := setupChatTest(t)
			ctx := m.ctx
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, failRunner{err: errors.New("must not run")})
			t.Cleanup(restore)

			m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
				ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
				ID: 7, Name: "Claude", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
			}, nil)
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
			}, nil)

			_, err := m.svc.Send(ctx, &chat_svc.SendRequest{
				SessionID: 100,
				AgentID:   7,
				Images: []chat_svc.SendImage{{
					Name:    "shot.png",
					DataURL: "data:image/png;base64,iVBORw0KGgo=",
				}},
			})
			assert.Error(t, err)
		})

		convey.Convey("Given image message on self-fingerprint backend without image capability, when Send, then it fails with AgentBackendTypeUnsupported before persisting", func() {
			m := setupChatTest(t)
			ctx := m.ctx
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, failRunner{err: errors.New("must not run")})
			t.Cleanup(restore)

			ctrl := gomock.NewController(t)
			t.Cleanup(ctrl.Finish)
			rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
			rds.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()
			prevSvc := remote_device_svc.Default()
			remote_device_svc.SetDefault(rds)
			t.Cleanup(func() { remote_device_svc.SetDefault(prevSvc) })

			m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
				ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
				ID: 7, Name: "Claude", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
			}, nil)
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "sha256:self", Status: consts.ACTIVE,
			}, nil)

			_, err := m.svc.Send(ctx, &chat_svc.SendRequest{
				SessionID: 100,
				AgentID:   7,
				Images: []chat_svc.SendImage{{
					Name:    "shot.png",
					DataURL: "data:image/png;base64,iVBORw0KGgo=",
				}},
			})
			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, code.AgentBackendTypeUnsupported, httpErr.Code,
				"self-fingerprint local backend must fail the image capability check up front")
		})
	})
}

type preparedRecordingPiRunner struct {
	requests          chan agentruntime.RunRequest
	providerSessionID string
}

func (*preparedRecordingPiRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{
		capability.CapForkSession: true,
	}}
}

func (*preparedRecordingPiRunner) Run(context.Context, agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	return nil, nil, errors.New("prepared recording Pi runner must use PrepareRun")
}

func (r *preparedRecordingPiRunner) PrepareRun(_ context.Context, req agentruntime.RunRequest) (piagentrt.PreparedRun, error) {
	if r.requests != nil {
		r.requests <- req
	}
	return &preparedRecordingPiRun{runner: r}, nil
}

type preparedRecordingPiRun struct {
	runner *preparedRecordingPiRunner
}

func (p *preparedRecordingPiRun) ProviderSessionID() string {
	if p.runner.providerSessionID != "" {
		return p.runner.providerSessionID
	}
	return "pi-session-new"
}

func (p *preparedRecordingPiRun) Start(context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	events := make(chan agentruntime.Event, 1)
	events <- agentruntime.Done{}
	close(events)
	return events, &agentruntime.RunResult{ProviderSessionID: p.ProviderSessionID()}, nil
}

func (*preparedRecordingPiRun) Close(context.Context) error { return nil }

type forkStartupFailRunner struct {
	err error
}

func (*forkStartupFailRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{
		capability.CapForkSession: true,
	}}
}

func (r *forkStartupFailRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	return nil, nil, r.err
}

func (r *forkStartupFailRunner) PrepareRun(_ context.Context, _ agentruntime.RunRequest) (piagentrt.PreparedRun, error) {
	return nil, r.err
}

type preparedStartupFailRunner struct {
	err               error
	onStart           func()
	providerSessionID string

	mu           sync.Mutex
	prepareCalls int
	startCalls   int
	promptCalls  int
}

func (*preparedStartupFailRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{
		capability.CapForkSession: true,
	}}
}

func (r *preparedStartupFailRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	return nil, nil, errors.New("prepared startup failure runner must use PrepareRun")
}

func (r *preparedStartupFailRunner) PrepareRun(_ context.Context, _ agentruntime.RunRequest) (piagentrt.PreparedRun, error) {
	r.mu.Lock()
	r.prepareCalls++
	r.mu.Unlock()
	return &preparedStartupFailure{runner: r}, nil
}

func (r *preparedStartupFailRunner) Counts() (prepare, start, prompt int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.prepareCalls, r.startCalls, r.promptCalls
}

type preparedStartupFailure struct {
	runner *preparedStartupFailRunner
}

func (p *preparedStartupFailure) ProviderSessionID() string {
	if p.runner.providerSessionID != "" {
		return p.runner.providerSessionID
	}
	return "pi-session-new"
}

func (p *preparedStartupFailure) Start(context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	if p.runner.onStart != nil {
		p.runner.onStart()
	}
	p.runner.mu.Lock()
	p.runner.startCalls++
	p.runner.mu.Unlock()
	return nil, nil, p.runner.err
}

func (*preparedStartupFailure) Close(context.Context) error { return nil }

type preparedAcknowledgedRunner struct {
	onStart           func()
	providerSessionID string
	events            chan agentruntime.Event
}

func (*preparedAcknowledgedRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{
		capability.CapForkSession: true,
	}}
}

func (r *preparedAcknowledgedRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	return nil, nil, errors.New("acknowledged runner must use PrepareRun")
}

func (r *preparedAcknowledgedRunner) PrepareRun(_ context.Context, _ agentruntime.RunRequest) (piagentrt.PreparedRun, error) {
	return &preparedAcknowledgedStart{runner: r}, nil
}

type preparedAcknowledgedStart struct {
	runner *preparedAcknowledgedRunner
}

func (p *preparedAcknowledgedStart) ProviderSessionID() string {
	return p.runner.providerSessionID
}

func (p *preparedAcknowledgedStart) Start(context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	if p.runner.onStart != nil {
		p.runner.onStart()
	}
	events := p.runner.events
	if events == nil {
		events = make(chan agentruntime.Event, 1)
		events <- agentruntime.Done{}
		close(events)
	}
	return events, &agentruntime.RunResult{ProviderSessionID: p.runner.providerSessionID}, nil
}

func (*preparedAcknowledgedStart) Close(context.Context) error { return nil }

type blockingPiPreflightRunner struct {
	entered chan struct{}
	once    sync.Once

	mu    sync.Mutex
	calls int
}

func (*blockingPiPreflightRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{
		capability.CapForkSession: true,
	}}
}

func (r *blockingPiPreflightRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	return nil, nil, errors.New("blocking Pi preflight runner must be prepared")
}

func (r *blockingPiPreflightRunner) PrepareRun(ctx context.Context, _ agentruntime.RunRequest) (piagentrt.PreparedRun, error) {
	r.mu.Lock()
	r.calls++
	call := r.calls
	r.mu.Unlock()
	if call > 1 {
		return nil, errors.New("second Pi preflight reached")
	}
	r.once.Do(func() { close(r.entered) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(750 * time.Millisecond):
		return nil, errors.New("Pi preflight was not canceled")
	}
}

type blockingPiPreparedStartRunner struct {
	startEntered      chan struct{}
	closed            chan struct{}
	providerSessionID string
	startOnce         sync.Once
	closeOnce         sync.Once

	mu           sync.Mutex
	prepareCalls int
	promptCalls  int
}

func (*blockingPiPreparedStartRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{
		capability.CapForkSession: true,
	}}
}

func (r *blockingPiPreparedStartRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	return nil, nil, errors.New("blocking Pi prepared-start runner must be prepared")
}

func (r *blockingPiPreparedStartRunner) PrepareRun(_ context.Context, _ agentruntime.RunRequest) (piagentrt.PreparedRun, error) {
	r.mu.Lock()
	r.prepareCalls++
	call := r.prepareCalls
	r.mu.Unlock()
	if call > 1 {
		return nil, errors.New("second Pi preflight reached")
	}
	return &blockingPiPreparedStart{runner: r}, nil
}

func (r *blockingPiPreparedStartRunner) PromptCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.promptCalls
}

type blockingPiPreparedStart struct {
	runner *blockingPiPreparedStartRunner
}

func (p *blockingPiPreparedStart) ProviderSessionID() string {
	if p.runner.providerSessionID != "" {
		return p.runner.providerSessionID
	}
	return "pi-session-new"
}

func (p *blockingPiPreparedStart) Start(ctx context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	p.runner.startOnce.Do(func() { close(p.runner.startEntered) })
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-time.After(750 * time.Millisecond):
		p.runner.mu.Lock()
		p.runner.promptCalls++
		p.runner.mu.Unlock()
		return nil, nil, errors.New("Pi prompt started after cancellation was requested")
	}
}

func (p *blockingPiPreparedStart) Close(context.Context) error {
	p.runner.closeOnce.Do(func() { close(p.runner.closed) })
	return nil
}

type generationSafePreparedRunner struct {
	mu           sync.Mutex
	prepareCalls int
	closeCalls   int
	abortCalls   int
}

func (*generationSafePreparedRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{
		capability.CapForkSession: true,
	}}
}

func (*generationSafePreparedRunner) Run(context.Context, agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	return nil, nil, errors.New("generation-safe runner must use PrepareRun")
}

func (r *generationSafePreparedRunner) PrepareRun(context.Context, agentruntime.RunRequest) (piagentrt.PreparedRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prepareCalls++
	if r.prepareCalls > 1 {
		return nil, errors.New("second prepared generation reached")
	}
	return &generationSafePreparedRun{runner: r}, nil
}

func (r *generationSafePreparedRunner) Abort(context.Context, int64, uint64) (agentruntime.AbortOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.abortCalls++
	return agentruntime.AbortOutcome{}, nil
}

func (r *generationSafePreparedRunner) Counts() (prepare, closed, abort int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.prepareCalls, r.closeCalls, r.abortCalls
}

type generationSafePreparedRun struct {
	runner *generationSafePreparedRunner
}

func (*generationSafePreparedRun) ProviderSessionID() string { return "" }

func (*generationSafePreparedRun) Start(context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	return nil, nil, errors.New("empty prepared identity must stop before Start")
}

func (p *generationSafePreparedRun) Close(context.Context) error {
	p.runner.mu.Lock()
	defer p.runner.mu.Unlock()
	p.runner.closeCalls++
	return nil
}

type promptCountingRunner struct {
	mu          sync.Mutex
	promptCalls int
}

func (*promptCountingRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{
		capability.CapForkSession: true,
	}}
}

func (*promptCountingRunner) Run(context.Context, agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	return nil, nil, errors.New("prompt-counting runner must use PrepareRun")
}

func (r *promptCountingRunner) PrepareRun(context.Context, agentruntime.RunRequest) (piagentrt.PreparedRun, error) {
	return &promptCountingPreparedRun{runner: r}, nil
}

type promptCountingPreparedRun struct {
	runner *promptCountingRunner
}

func (*promptCountingPreparedRun) ProviderSessionID() string { return "pi-session-new" }

func (p *promptCountingPreparedRun) Start(context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	p.runner.mu.Lock()
	p.runner.promptCalls++
	p.runner.mu.Unlock()
	events := make(chan agentruntime.Event)
	close(events)
	return events, &agentruntime.RunResult{ProviderSessionID: "pi-session-new"}, nil
}

func (*promptCountingPreparedRun) Close(context.Context) error { return nil }

func (r *promptCountingRunner) PromptCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.promptCalls
}

type anchorResultRunner struct{}

func (*anchorResultRunner) Capabilities() capability.Capabilities { return capability.Capabilities{} }

func (*anchorResultRunner) Run(_ context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	events := make(chan agentruntime.Event, 2)
	events <- agentruntime.TextDelta{Text: "completed answer"}
	events <- agentruntime.Done{}
	close(events)
	return events, &agentruntime.RunResult{
		ProviderSessionID: req.ProviderSessionID,
		UserAnchor:        "pi-user-entry-new",
	}, nil
}

type stopOrderPiRunner struct {
	started      chan struct{}
	abortEntered chan struct{}
	releaseAbort chan struct{}

	mu               sync.Mutex
	runCtx           context.Context
	events           chan agentruntime.Event
	result           *agentruntime.RunResult
	abortCalls       int
	abortSawCanceled bool
	abortOnce        sync.Once
	finishOnce       sync.Once
}

func (*stopOrderPiRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{capability.CapAbort: true}}
}

func (r *stopOrderPiRunner) Run(ctx context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	r.mu.Lock()
	r.runCtx = ctx
	r.events = make(chan agentruntime.Event)
	r.result = &agentruntime.RunResult{ProviderSessionID: req.ProviderSessionID}
	events := r.events
	result := r.result
	r.mu.Unlock()
	close(r.started)
	return events, result, nil
}

func (r *stopOrderPiRunner) Abort(context.Context, int64, uint64) (agentruntime.AbortOutcome, error) {
	r.mu.Lock()
	r.abortCalls++
	runCtx := r.runCtx
	r.mu.Unlock()
	r.abortOnce.Do(func() { close(r.abortEntered) })
	select {
	case <-runCtx.Done():
		r.mu.Lock()
		r.abortSawCanceled = true
		r.result.UserAnchor = "pi-user-anchor-after-local-stop"
		events := r.events
		r.mu.Unlock()
		r.finishOnce.Do(func() { close(events) })
	case <-r.releaseAbort:
		r.mu.Lock()
		events := r.events
		r.mu.Unlock()
		r.finishOnce.Do(func() { close(events) })
	}
	return agentruntime.AbortOutcome{}, nil
}

func (r *stopOrderPiRunner) stopObservation() (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.abortCalls, r.abortSawCanceled
}

type compactRecordingRunner struct {
	*recordingRunner
}

func (r *compactRecordingRunner) Capabilities() capability.Capabilities {
	base := r.recordingRunner.Capabilities()
	base.Set = map[capability.Capability]bool{
		capability.CapCompact: true,
	}
	return base
}

func (r *compactRecordingRunner) Run(_ context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	r.requests <- req
	events := make(chan agentruntime.Event, 1)
	events <- agentruntime.CompactBoundary{Trigger: "manual"}
	close(events)
	return events, &agentruntime.RunResult{ProviderSessionID: req.ProviderSessionID}, nil
}

type goalRecordingRunner struct {
	*recordingRunner

	getReq   agentruntime.GoalRequest
	setReq   agentruntime.GoalRequest
	clearReq agentruntime.GoalRequest

	getErr   error
	setErr   error
	clearErr error
}

func (r *goalRecordingRunner) Capabilities() capability.Capabilities {
	base := r.recordingRunner.Capabilities()
	base.Set = map[capability.Capability]bool{
		capability.CapGoal: true,
	}
	return base
}

func (r *goalRecordingRunner) GetGoal(_ context.Context, req agentruntime.GoalRequest) (*agentruntime.Goal, error) {
	r.getReq = req
	if r.getErr != nil {
		return nil, r.getErr
	}
	return &agentruntime.Goal{ThreadID: req.ProviderSessionID, Objective: "ship goal rpc", Status: "active", TokensUsed: 7}, nil
}

func (r *goalRecordingRunner) SetGoal(_ context.Context, req agentruntime.GoalRequest) (*agentruntime.Goal, error) {
	r.setReq = req
	if r.setErr != nil {
		return nil, r.setErr
	}
	objective := ""
	if req.Objective != nil {
		objective = *req.Objective
	}
	status := ""
	if req.Status != nil {
		status = *req.Status
	}
	threadID := req.ProviderSessionID
	if threadID == "" {
		threadID = "codex-thread-created"
	}
	return &agentruntime.Goal{ThreadID: threadID, Objective: objective, Status: status}, nil
}

func (r *goalRecordingRunner) ClearGoal(_ context.Context, req agentruntime.GoalRequest) (bool, error) {
	r.clearReq = req
	return r.clearErr == nil, r.clearErr
}

type blockingProviderSessionRunner struct {
	started   chan struct{}
	release   chan struct{}
	sessionID string
}

func (r *blockingProviderSessionRunner) Capabilities() capability.Capabilities {
	return (&recordingRunner{}).Capabilities()
}

func (r *blockingProviderSessionRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	events := make(chan agentruntime.Event)
	close(r.started)
	go func() {
		<-r.release
		events <- agentruntime.TextDelta{Text: "done"}
		close(events)
	}()
	sessionID := r.sessionID
	if sessionID == "" {
		sessionID = "early-sid"
	}
	return events, &agentruntime.RunResult{ProviderSessionID: sessionID}, nil
}

func TestSend_PersistsProviderSessionIDBeforeStreamDrains(t *testing.T) {
	// Given 第一轮 Claude Code turn 已经 spawn 出 provider session,但 assistant
	// stream 还没结束,
	// When 用户立刻复制启动命令,
	// Then DB 中应已经有 provider session id,命令必须带 --resume。
	t.Setenv("AGENTRE_DATA_DIR", t.TempDir())
	m := setupChatTest(t)
	ctx := m.ctx

	runner := &blockingProviderSessionRunner{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
	t.Cleanup(restore)

	sess := &chat_entity.Session{
		ID:          100,
		AgentID:     7,
		AgentStatus: "idle",
		Status:      consts.ACTIVE,
	}
	backend := &agent_backend_entity.AgentBackend{
		ID:     12,
		Type:   string(agent_backend_entity.TypeClaudeCode),
		Status: consts.ACTIVE,
	}
	agent := &agent_entity.Agent{
		ID:             7,
		Name:           "Claude Local",
		AgentBackendID: 12,
		Status:         consts.ACTIVE,
		PromptJSON:     `[]`,
	}

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(agent, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(backend, nil)

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	persistedEarly := make(chan struct{})
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			if s.ProviderSessionID == "early-sid" {
				select {
				case <-persistedEarly:
				default:
					close(persistedEarly)
				}
			}
			return nil
		}).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	require.NoError(t, err)

	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		close(runner.release)
		t.Fatal("timed out waiting for runtime to start")
	}

	persistedBeforeDrain := false
	select {
	case <-persistedEarly:
		persistedBeforeDrain = true
	case <-time.After(300 * time.Millisecond):
	}

	if persistedBeforeDrain {
		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(agent, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(backend, nil)

		launch, lerr := m.svc.GetLaunchCommand(ctx, &chat_svc.LaunchCommandRequest{SessionID: 100})
		require.NoError(t, lerr)
		assert.Contains(t, launch.Command, "--resume early-sid")
	}

	close(runner.release)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)
	assert.True(t, persistedBeforeDrain, "provider session id must be persisted before the runtime event stream drains")
}

// TestSend_FreshSessionReflectsLocalProviderSessionID 覆盖挂账修复(2026-08-11)的
// 生产者侧:chat_svc 在本地 sess.ProviderSessionID 为空(regenerate 无锚点 / provider 会话
// 失效恢复同此)时,必须在 RunRequest 上声明 FreshSession=true,daemon 才不拿落库旧 id 续话;
// 本地有可续的原生会话时保持 false,决策 8 的续话语义原样。
func TestSend_FreshSessionReflectsLocalProviderSessionID(t *testing.T) {
	convey.Convey("Given 本地 provider_session_id 有无两种状态, When Send, Then RunRequest.FreshSession 跟随", t, func() {
		run := func(providerSessionID string) bool {
			t.Setenv("AGENTRE_DATA_DIR", t.TempDir())
			m := setupChatTest(t)
			ctx := m.ctx
			runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
			t.Cleanup(restore)

			sess := &chat_entity.Session{ID: 100, AgentID: 7, ProviderSessionID: providerSessionID, AgentStatus: "idle", Status: consts.ACTIVE}
			backend := &agent_backend_entity.AgentBackend{ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE}
			agent := &agent_entity.Agent{ID: 7, Name: "Claude", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`}

			m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(agent, nil)
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(backend, nil)
			m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
			m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
			m.dbMock.ExpectBegin()
			m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
			m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
					if msg.Role == "user" {
						msg.ID = 1000
					} else {
						msg.ID = 1001
					}
					return nil
				}).Times(2)
			m.dbMock.ExpectCommit()

			resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
			require.NoError(t, err)
			chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

			select {
			case req := <-runner.requests:
				return req.FreshSession
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for runtime request")
				return false
			}
		}

		convey.Convey("本地无 provider_session_id → FreshSession=true", func() {
			assert.True(t, run(""), "regenerate 无锚点 / 会话失效恢复:必须声明 freshSession,daemon 才不拿落库旧 id 续话")
		})
		convey.Convey("本地有 provider_session_id → FreshSession=false(resume)", func() {
			assert.False(t, run("claude-abc123"), "本地有可续的原生会话时保持决策 8 续话语义")
		})
	})
}

type streamErrorRunner struct{ err error }

func (streamErrorRunner) Capabilities() capability.Capabilities { return capability.Capabilities{} }
func (r streamErrorRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	streamErr := r.err
	if streamErr == nil {
		streamErr = errors.New("upstream failed")
	}
	events := make(chan agentruntime.Event, 2)
	events <- agentruntime.TextDelta{Text: "partial answer"}
	events <- agentruntime.ErrorEvent{Err: streamErr}
	close(events)
	return events, &agentruntime.RunResult{}, nil
}

type resultStopErrorRunner struct{ err error }

func (resultStopErrorRunner) Capabilities() capability.Capabilities { return capability.Capabilities{} }
func (r resultStopErrorRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	events := make(chan agentruntime.Event, 1)
	events <- agentruntime.TextDelta{Text: "partial answer"}
	close(events)
	return events, &agentruntime.RunResult{StopErr: r.err}, nil
}

type streamErrorThenRecoverRunner struct{}

func (streamErrorThenRecoverRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{}
}
func (r streamErrorThenRecoverRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	events := make(chan agentruntime.Event, 3)
	events <- agentruntime.ErrorEvent{Err: errors.New("temporary upstream hiccup")}
	events <- agentruntime.TextDelta{Text: "recovered answer"}
	events <- agentruntime.Done{}
	close(events)
	return events, &agentruntime.RunResult{}, nil
}

type streamErrorThenMetadataRunner struct{}

func (streamErrorThenMetadataRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{}
}
func (r streamErrorThenMetadataRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	events := make(chan agentruntime.Event, 3)
	events <- agentruntime.ErrorEvent{Err: errors.New("temporary upstream hiccup")}
	events <- agentruntime.UsageUpdate{Usage: &provider.Usage{PromptTokens: 1}}
	events <- agentruntime.Done{}
	close(events)
	return events, &agentruntime.RunResult{}, nil
}

type failRunner struct{ err error }

func (failRunner) Capabilities() capability.Capabilities { return capability.Capabilities{} }
func (r failRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	return nil, nil, r.err
}

type streamRetryRunner struct{}

func (streamRetryRunner) Capabilities() capability.Capabilities { return capability.Capabilities{} }
func (r streamRetryRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	events := make(chan agentruntime.Event, 3)
	events <- agentruntime.Retry{
		Message: "Reconnecting... 1/5",
		Details: "high demand",
		Attempt: 1,
		Max:     5,
	}
	events <- agentruntime.TextDelta{Text: "recovered"}
	events <- agentruntime.Done{}
	close(events)
	return events, &agentruntime.RunResult{}, nil
}

type streamSteerConsumedRunner struct{}

func (streamSteerConsumedRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{}
}
func (r streamSteerConsumedRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	events := make(chan agentruntime.Event, 3)
	events <- agentruntime.TextDelta{Text: "before "}
	events <- agentruntime.SteerConsumed{
		Steers: []agentruntime.ConsumedSteer{{QueuedID: "qid-1", Text: "follow-up",
			SourcePeer: "sha256:remote-peer", SourceName: "iPhone"}},
	}
	events <- agentruntime.TextDelta{Text: "after"}
	close(events)
	return events, &agentruntime.RunResult{}, nil
}

func TestSend_NewSession(t *testing.T) {
	convey.Convey("Send 新建 session 走通", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx // must carry DB for Transaction
		firstUserText := "优化一下 Edit/Write/file_change，能不能把 live 和 replay 通路统一起来，不要切走回来才正常"

		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
			ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE,
			PromptJSON: `["You are helpful."]`,
		}, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: "builtin", LLMProviderKey: "key-21", Status: consts.ACTIVE,
		}, nil)
		m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
		expectProviderResolvable(m, "key-21")

		fp := providertest.New().
			QueueStream(
				provider.StreamChunk{ContentDelta: "hello"},
				provider.StreamChunk{ContentDelta: "world"},
				provider.StreamChunk{FinishReason: provider.FinishStop, Usage: &provider.Usage{PromptTokens: 5, CompletionTokens: 2}},
			)
		chat_svc.SetProviderBuilderForTest(func(_ *llm_provider_entity.LLMProvider) (provider.Provider, error) {
			return fp, nil
		})
		t.Cleanup(chat_svc.ResetProviderBuilderForTest)

		m.session.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
				assert.Equal(t, firstUserText, s.Title)
				s.ID = 100
				return nil
			})

		// Transaction calls: Begin + repo calls via mock + Commit
		m.dbMock.ExpectBegin()
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
		m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				if msg.Role == "user" {
					msg.ID = 1000
				} else {
					msg.ID = 1001
				}
				return nil
			}).Times(2)
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).Times(1) // inside transaction
		m.dbMock.ExpectCommit()

		m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
			{ID: 1000, SessionID: 100, Role: "user", BlocksJSON: encodeText(firstUserText), Seq: 1},
			{ID: 1001, SessionID: 100, Role: "assistant", BlocksJSON: "[]", Seq: 2},
		}, nil).AnyTimes()
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes() // post-turn updates
		m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{
			AgentID: 7, Text: firstUserText,
		})
		assert.NoError(t, err)
		assert.Equal(t, int64(100), resp.SessionID)
		assert.NotZero(t, resp.AssistantMessageID)

		chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

		var got string
		for _, ev := range m.events {
			payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
			if !ok {
				continue
			}
			if payload.Kind == chat_svc.StreamChunk {
				got += payload.Delta
			}
		}
		assert.Equal(t, "helloworld", got)
	})
}

// TestSend_NewSessionWithExecTargetOverride_GivenNotInAgentsList_ThenRejects 用
// setupChatTest 的既有宽松桩（ListByAgent 恒返回空列表）证明 R15a 的校验闸门真的
// 挂在 Send 的新建会话路径上：指定一个不在(空)列表里的 agentBackendID 必须在
// 建会话之前就被拒绝——不设 session.Create 期望，一旦校验被跳过误建了会话，
// gomock 会因未预期调用直接判这条用例失败。
func TestSend_NewSessionWithExecTargetOverride_GivenNotInAgentsList_ThenRejects(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx

	_, err := m.svc.Send(ctx, &chat_svc.SendRequest{
		AgentID: 7, Text: "hi", ExecTargetOverride: 999,
	})
	require.Error(t, err, "指定的档不在这个 Agent 的执行目标列表里，必须拒绝")
}

// TestSend_NewSessionWithExecTargetOverride_GivenValidNonFirstTarget_ThenResolvesToIt
// 证明手动指定生效且不受 Agent 默认档影响：Agent 的执行目标列表把 backend 51 排
// 最前，手动指定排第二的 52；新建会话必须实际跑在 52 上（recordingRunner 收到的
// RunRequest.Backend.ID）且钉住的也是 52（UpdateExecDaemon 的 agentBackendID 参数）。
// 不复用 setupChatTest——它对 ListByAgent 的默认宽松桩恒返回空列表，测试内再叠加
// 的精确期望永远匹配不到（exec_target_pin_test.go 顶部注释踩过的同一个坑），这里
// 用一套不带默认桩的干净 mock。
func TestSend_NewSessionWithExecTargetOverride_GivenValidNonFirstTarget_ThenResolvesToIt(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	dbCtx, _, dbMock := testutils.Database(t)

	agentMock := mock_agent_repo.NewMockAgentRepo(ctrl)
	execTargetMock := mock_agent_repo.NewMockAgentExecTargetRepo(ctrl)
	backendMock := mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl)
	sessionMock := mock_chat_repo.NewMockSessionRepo(ctrl)
	messageMock := mock_chat_repo.NewMockMessageRepo(ctrl)

	prevAgent, prevExecTarget := agent_repo.Agent(), agent_repo.AgentExecTarget()
	prevBackend := agent_backend_repo.AgentBackend()
	prevSession, prevMessage := chat_repo.Session(), chat_repo.Message()
	agent_repo.RegisterAgent(agentMock)
	agent_repo.RegisterAgentExecTarget(execTargetMock)
	agent_backend_repo.RegisterAgentBackend(backendMock)
	chat_repo.RegisterSession(sessionMock)
	chat_repo.RegisterMessage(messageMock)
	t.Cleanup(func() {
		agent_repo.RegisterAgent(prevAgent)
		agent_repo.RegisterAgentExecTarget(prevExecTarget)
		agent_backend_repo.RegisterAgentBackend(prevBackend)
		chat_repo.RegisterSession(prevSession)
		chat_repo.RegisterMessage(prevMessage)
	})

	svc := chat_svc.NewChat(chat_svc.NoopEmitter{})
	chat_svc.RegisterChat(svc)

	runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
	t.Cleanup(restore)

	agentMock.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "多档", AgentBackendID: 51, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	execTargetMock.EXPECT().ListByAgent(gomock.Any(), int64(7)).Return([]*agent_entity.AgentExecTarget{
		{ID: 1, AgentID: 7, AgentBackendID: 51, SortOrder: 0},
		{ID: 2, AgentID: 7, AgentBackendID: 52, SortOrder: 1},
	}, nil).AnyTimes()
	backendMock.EXPECT().Find(gomock.Any(), int64(52)).Return(&agent_backend_entity.AgentBackend{
		ID: 52, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
	}, nil).AnyTimes()

	sessionMock.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			s.ID = 100
			return nil
		})
	sessionMock.EXPECT().UpdateExecDaemon(gomock.Any(), int64(100), int64(0), "", int64(52)).
		Return(nil)
	sessionMock.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	dbMock.ExpectBegin()
	messageMock.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	messageMock.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	dbMock.ExpectCommit()
	messageMock.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := svc.Send(dbCtx, &chat_svc.SendRequest{
		AgentID: 7, Text: "hi", ExecTargetOverride: 52,
	})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(svc, resp.AssistantMessageID)

	select {
	case req := <-runner.requests:
		assert.Equal(t, int64(52), req.Backend.ID, "手动指定的第二档必须真的跑起来，不是 Agent 默认的第一档")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime request")
	}
}

func TestSend_ExistingSessionUsesSessionAgentBackend(t *testing.T) {
	// Given 已有会话属于 Agent 7,
	// When 前端异常传入另一个 AgentID,
	// Then Send 必须以 chat_sessions.agent_id 为准，避免 A 会话误跑 B 后端。
	m := setupChatTest(t)
	ctx := m.ctx
	runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Correct", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{
		SessionID: 100, AgentID: 99, Text: "hi",
	})
	assert.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	select {
	case req := <-runner.requests:
		assert.Equal(t, int64(12), req.Backend.ID)
		assert.Equal(t, int64(7), req.AgentID)
		assert.Equal(t, int64(100), req.SessionID)
		assert.Equal(t, "hi", req.UserText)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime request")
	}
}

func TestSend_CodexPermissionModePersistsAndStartsTurnInMode(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, runner)
	t.Cleanup(restore)

	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
		PermissionMode: "default",
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Codex", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "", Status: consts.ACTIVE,
	}, nil)
	m.session.EXPECT().UpdatePermissionMode(gomock.Any(), int64(100), "plan").Return(nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{
		SessionID:      100,
		AgentID:        7,
		Text:           "hi",
		PermissionMode: "plan",
	})
	assert.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	select {
	case req := <-runner.requests:
		assert.Equal(t, "plan", req.CollaborationMode)
		assert.Equal(t, "", req.PermissionMode)
		assert.Equal(t, "plan", sess.PermissionMode)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime request")
	}
}

func TestSend_CodexLocalDoesNotInjectGatewayDeps(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	chat_svc.RegisterGateway(&fakeChatGateway{
		status: httpgateway.GatewayStatus{State: "running", URL: "http://127.0.0.1:60080"},
		token:  "chat-token",
	})
	t.Cleanup(func() { chat_svc.RegisterGateway(nil) })

	runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, runner)
	t.Cleanup(restore)

	sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Codex Local", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "", Status: consts.ACTIVE,
	}, nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{
		SessionID: 100,
		AgentID:   7,
		Text:      "hi",
	})
	assert.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	select {
	case req := <-runner.requests:
		assert.Empty(t, req.GatewayURL)
		assert.Empty(t, req.GatewayToken)
		assert.Nil(t, req.Provider)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime request")
	}
}

// TestSend_CodexLoginStateWithSessionProviderInjectsGatewayDeps 钉死 spec 2026-08-10
// 决策 6/问题 3:CLI 登录态 codex 后端(agent 未绑 LLM provider)上,会话选了一个
// agentre 供应商(sess.ProviderKey)时也要签网关 token、把 GatewayURL/Token 装进
// RunRequest —— shouldSignChatGateway 的门控必须看本轮 effective provider(prov),
// 不能只看 be.LLMProviderKey,否则决策 7(登录态双向可切)在 codex 上从未兑现。
// 与 TestSend_CodexLocalDoesNotInjectGatewayDeps(无 effective provider 时不装配)
// 互为正反例。
func TestSend_CodexLoginStateWithSessionProviderInjectsGatewayDeps(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	chat_svc.RegisterGateway(&fakeChatGateway{
		status: httpgateway.GatewayStatus{State: "running", URL: "http://127.0.0.1:60080"},
		token:  "chat-token",
	})
	t.Cleanup(func() { chat_svc.RegisterGateway(nil) })

	runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, runner)
	t.Cleanup(restore)

	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE, ProviderKey: "session-picked",
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Codex Local", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "session-picked").
		Return(newActiveProvider("session-picked", string(llm_provider_entity.TypeOpenAIResponse)), nil).AnyTimes()
	expectProviderResolvable(m, "session-picked")
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{
		SessionID: 100,
		AgentID:   7,
		Text:      "hi",
	})
	assert.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	select {
	case req := <-runner.requests:
		assert.NotEmpty(t, req.GatewayURL, "登录态后端上会话选了供应商也要装配网关 URL")
		assert.NotEmpty(t, req.GatewayToken, "登录态后端上会话选了供应商也要签网关 token")
		require.NotNil(t, req.Provider)
		assert.Equal(t, "session-picked", req.Provider.ProviderKey)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime request")
	}
}

func TestCompact_CodexStartsCompactTurnWithoutUserMessage(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	runner := &compactRecordingRunner{recordingRunner: &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, runner)
	t.Cleanup(restore)

	sess := &chat_entity.Session{
		ID:                100,
		AgentID:           7,
		AgentStatus:       "idle",
		Status:            consts.ACTIVE,
		ProviderSessionID: "codex-thread-123",
		PermissionMode:    "default",
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Codex", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "", Status: consts.ACTIVE,
	}, nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil)
	var createdRoles []string
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			createdRoles = append(createdRoles, msg.Role)
			msg.ID = 1001
			return nil
		}).Times(1)
	m.dbMock.ExpectCommit()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := m.svc.Compact(ctx, &chat_svc.CompactRequest{SessionID: 100})
	assert.NoError(t, err)
	assert.Equal(t, int64(100), resp.SessionID)
	assert.Equal(t, int64(1001), resp.AssistantMessageID)
	assert.NotEmpty(t, resp.Stream)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	assert.Equal(t, []string{"assistant"}, createdRoles)

	select {
	case req := <-runner.requests:
		assert.True(t, req.Compact)
		assert.Empty(t, req.UserText)
		assert.Equal(t, "codex-thread-123", req.ProviderSessionID)
		assert.Equal(t, "default", req.CollaborationMode)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime request")
	}

	var sawCompactBoundary bool
	for _, ev := range m.events {
		payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
		if !ok || payload.Kind != chat_svc.StreamCompactBoundary {
			continue
		}
		sawCompactBoundary = true
		require.NotNil(t, payload.Compact)
		assert.Equal(t, "manual", payload.Compact.Trigger)
	}
	assert.True(t, sawCompactBoundary, "compact turn should emit compact boundary divider")
}

// TestCompact_ExistingSession_ProviderKeyOverridesAgentBinding 钉死决策 3 对 Compact 的
// 覆盖:会话 provider_key 优先于 agent 绑定解析必须对**所有**本地 turn 入口生效,包括
// Compact —— #26 时代 compact 经 prepareTurnRun 的 ModelOverride 走会话级模型覆盖,
// 分支后 override 已移除,若 Compact 不应用 resolveSessionProvider,带 provider_key 的
// 会话在 compact 轮会悄悄退回 agent 绑定(且不提示),与 send / Regenerate / Edit 不一致。
func TestCompact_ExistingSession_ProviderKeyOverridesAgentBinding(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	chat_svc.RegisterGateway(&fakeChatGateway{
		status: httpgateway.GatewayStatus{State: "running", URL: "http://127.0.0.1:60080"},
	})
	t.Cleanup(func() { chat_svc.RegisterGateway(nil) })
	runner := &compactRecordingRunner{recordingRunner: &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, runner)
	t.Cleanup(restore)

	sess := &chat_entity.Session{
		ID:                100,
		AgentID:           7,
		AgentStatus:       "idle",
		Status:            consts.ACTIVE,
		ProviderSessionID: "codex-thread-123",
		ProviderKey:       "key-99",
		PermissionMode:    "default",
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Codex", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(newActiveProvider("key-21", string(llm_provider_entity.TypeOpenAIResponse)), nil).AnyTimes()
	expectProviderResolvable(m, "key-21")
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-99").Return(newActiveProvider("key-99", string(llm_provider_entity.TypeOpenAIResponse)), nil).AnyTimes()
	expectProviderResolvable(m, "key-99")
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			msg.ID = 1001
			return nil
		}).Times(1)
	m.dbMock.ExpectCommit()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := m.svc.Compact(ctx, &chat_svc.CompactRequest{SessionID: 100})
	assert.NoError(t, err)
	assert.Equal(t, int64(100), resp.SessionID)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	select {
	case req := <-runner.requests:
		require.NotNil(t, req.Provider)
		assert.Equal(t, "key-99", req.Provider.ProviderKey, "Compact 轮也必须按会话 provider_key 解析,而非 agent 绑定")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime request")
	}
}

// TestCompact_ExistingSession_MissingSessionProviderFallsBackWithNotice 钉死决策 8 对
// Compact 的覆盖:会话 provider_key 指向的供应商缺失 → compact 轮同样回退 agent 绑定并
// 追加一条持久 transcript notice(与 send / Regenerate / Edit 一致);provider_key 不清除。
func TestCompact_ExistingSession_MissingSessionProviderFallsBackWithNotice(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	chat_svc.RegisterGateway(&fakeChatGateway{
		status: httpgateway.GatewayStatus{State: "running", URL: "http://127.0.0.1:60080"},
	})
	t.Cleanup(func() { chat_svc.RegisterGateway(nil) })
	runner := &compactRecordingRunner{recordingRunner: &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, runner)
	t.Cleanup(restore)

	sess := &chat_entity.Session{
		ID:                100,
		AgentID:           7,
		AgentStatus:       "idle",
		Status:            consts.ACTIVE,
		ProviderSessionID: "codex-thread-123",
		ProviderKey:       "gone-provider",
		PermissionMode:    "default",
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Codex", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(newActiveProvider("key-21", string(llm_provider_entity.TypeOpenAIResponse)), nil).AnyTimes()
	expectProviderResolvable(m, "key-21")
	m.provider.EXPECT().FindByKey(gomock.Any(), "gone-provider").Return(nil, nil).AnyTimes()
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			msg.ID = 1001
			return nil
		}).Times(1)
	m.dbMock.ExpectCommit()
	var persisted *chat_entity.Message
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msg *chat_entity.Message) error {
			msgCopy := *msg
			persisted = &msgCopy
			return nil
		}).AnyTimes()

	resp, err := m.svc.Compact(ctx, &chat_svc.CompactRequest{SessionID: 100})
	assert.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	select {
	case req := <-runner.requests:
		require.NotNil(t, req.Provider)
		assert.Equal(t, "key-21", req.Provider.ProviderKey, "会话供应商缺失时 compact 应回退 agent 绑定")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime request")
	}

	require.NotNil(t, persisted, "compact assistant 消息应被持久化")
	persistedBlocks, err := persisted.GetBlocks()
	require.NoError(t, err)
	var noticeFound bool
	for _, b := range persistedBlocks {
		nb, ok := b.(blocks.NoticeBlock)
		if !ok {
			continue
		}
		var payload struct {
			ProviderKey string `json:"providerKey"`
		}
		if json.Unmarshal([]byte(nb.Text), &payload) == nil && payload.ProviderKey == "gone-provider" {
			noticeFound = true
		}
	}
	assert.True(t, noticeFound, "回退时 compact transcript 必须追加一条持久 notice,携带被回退的 provider_key")
}

func TestSend_PiAgentPersistsNativeSessionBeforeTurnDrain(t *testing.T) {
	m := setupChatTest(t)
	runner := &blockingProviderSessionRunner{
		started: make(chan struct{}), release: make(chan struct{}), sessionID: "pi-native-new",
	}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}", Status: consts.ACTIVE,
	}, nil)

	expectNoPiTranscriptRecovery(m, 100)
	persisted := make(chan struct{}, 1)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
		if s.ID == 100 && s.ProviderSessionID == "pi-native-new" {
			select {
			case persisted <- struct{}{}:
			default:
			}
		}
		return nil
	}).AnyTimes()
	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
		if msg.Role == "user" {
			msg.ID = 1000
		} else {
			msg.ID = 1001
		}
		return nil
	}).Times(2)
	m.dbMock.ExpectCommit()
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := m.svc.Send(m.ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	<-runner.started
	persistedBeforeDrain := false
	select {
	case <-persisted:
		persistedBeforeDrain = true
	case <-time.After(200 * time.Millisecond):
	}
	close(runner.release)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	assert.True(t, persistedBeforeDrain, "Pi native session ID must be durable before the event stream finishes")
}

func TestPiRecoveryGate_CompactAndChangedBackendReconcileBeforeProviderWork(t *testing.T) {
	tests := []struct {
		name        string
		backendType agent_backend_entity.BackendType
		invoke      func(chat_svc.ChatSvc, context.Context) error
	}{
		{
			name:        "Given Pi recovery is pending, when Compact mutates the session, then recovery fails before provider work",
			backendType: agent_backend_entity.TypePiAgent,
			invoke: func(svc chat_svc.ChatSvc, ctx context.Context) error {
				_, err := svc.Compact(ctx, &chat_svc.CompactRequest{SessionID: 100})
				return err
			},
		},
		{
			name:        "Given a Pi recovery marker survives a backend change, when Send changes provider mode, then recovery fails before the new provider runs",
			backendType: agent_backend_entity.TypeClaudeCode,
			invoke: func(svc chat_svc.ChatSvc, ctx context.Context) error {
				_, err := svc.Send(ctx, &chat_svc.SendRequest{
					SessionID: 100, AgentID: 7, Text: "must not run", PermissionMode: "plan",
				})
				return err
			},
		},
		{
			name:        "Given a Pi recovery marker survives a backend change, when SetPermissionMode mutates the backend, then recovery fails before dispatch",
			backendType: agent_backend_entity.TypeClaudeCode,
			invoke: func(svc chat_svc.ChatSvc, ctx context.Context) error {
				_, err := svc.SetPermissionMode(ctx, &chat_svc.SetPermissionModeRequest{SessionID: 100, Mode: "plan"})
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := setupChatTest(t)
			compactRunner := &compactRecordingRunner{recordingRunner: &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}}
			permissionRunner := &fakePermissionRunner{}
			var runner agentruntime.Runtime = compactRunner
			if tc.backendType == agent_backend_entity.TypeClaudeCode {
				runner = permissionRunner
			}
			restore := agentruntime.SwapRuntimeForTest(tc.backendType, runner)
			t.Cleanup(restore)

			m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
				ID: 100, AgentID: 7, ProviderSessionID: "pi-session-new", AgentStatus: "running", Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
				ID: 7, Name: "Changed backend", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
			}, nil)
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(tc.backendType), EnvJSON: "{}", Status: consts.ACTIVE,
			}, nil)
			m.dbMock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
				WithArgs("chat.pi_recovery:100", 1).
				WillReturnError(errors.New("pending Pi recovery read failed"))

			err := tc.invoke(m.svc, m.ctx)

			require.ErrorContains(t, err, "pending Pi recovery read failed")
			select {
			case req := <-compactRunner.requests:
				t.Fatalf("unreconciled Pi recovery reached provider work: %+v", req)
			default:
			}
			assert.False(t, permissionRunner.setCalled, "unreconciled Pi recovery changed provider mode")
			assert.NoError(t, m.dbMock.ExpectationsWereMet())
		})
	}
}

func TestCompact_PiAgentRequiresNativeProviderSession(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	runner := &compactRecordingRunner{recordingRunner: &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), EnvJSON: "{}", Status: consts.ACTIVE,
	}, nil)
	expectNoPiTranscriptRecovery(m, 100)

	resp, err := m.svc.Compact(ctx, &chat_svc.CompactRequest{SessionID: 100})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "请先发送一条消息")
	assert.Nil(t, resp)
	select {
	case req := <-runner.requests:
		t.Fatalf("Pi compact without a native provider session must not start runtime: %+v", req)
	default:
	}
}

func TestCompact_RequiresCodexProviderSessionAndCapability(t *testing.T) {
	t.Run("missing provider session", func(t *testing.T) {
		m := setupChatTest(t)
		ctx := context.Background()
		m.session.EXPECT().Find(ctx, int64(100)).Return(&chat_entity.Session{
			ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
		}, nil)
		m.agent.EXPECT().Find(ctx, int64(7)).Return(&agent_entity.Agent{
			ID: 7, Name: "Codex", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
		}, nil)
		m.backend.EXPECT().Find(ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "", Status: consts.ACTIVE,
		}, nil)

		_, err := m.svc.Compact(ctx, &chat_svc.CompactRequest{SessionID: 100})
		assert.Error(t, err)
	})

	t.Run("non-codex backend", func(t *testing.T) {
		m := setupChatTest(t)
		ctx := context.Background()
		expectCapabilitySessionBackend(m, ctx, "thread-1", "Claude", agent_backend_entity.TypeClaudeCode)

		_, err := m.svc.Compact(ctx, &chat_svc.CompactRequest{SessionID: 100})
		assert.Error(t, err)
	})

	t.Run("codex runtime without compact capability", func(t *testing.T) {
		m := setupChatTest(t)
		ctx := context.Background()
		runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, runner)
		t.Cleanup(restore)

		expectCapabilitySessionBackend(m, ctx, "thread-1", "Codex", agent_backend_entity.TypeCodex)

		_, err := m.svc.Compact(ctx, &chat_svc.CompactRequest{SessionID: 100})
		assert.Error(t, err)
	})
}

func TestGoal_CodexRoutesToGoalController(t *testing.T) {
	convey.Convey("Codex goal set/get/clear use provider thread metadata", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx
		runner := &goalRecordingRunner{recordingRunner: &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, runner)
		t.Cleanup(restore)

		expectCodexSession := func() {
			m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
				ID:                100,
				AgentID:           7,
				AgentStatus:       "idle",
				Status:            consts.ACTIVE,
				ProviderSessionID: "codex-thread-123",
			}, nil)
			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
				ID: 7, Name: "Codex", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
			}, nil)
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "", Status: consts.ACTIVE,
			}, nil)
		}

		expectCodexSession()
		objective := "ship goal rpc"
		status := "active"
		resp, err := m.svc.SetGoal(ctx, &chat_svc.SetGoalRequest{
			SessionID: 100,
			Objective: &objective,
			Status:    &status,
		})
		assert.NoError(t, err)
		require.NotNil(t, resp.Goal)
		assert.Equal(t, "ship goal rpc", resp.Goal.Objective)
		assert.Equal(t, "active", resp.Goal.Status)
		require.NotNil(t, runner.setReq.Objective)
		assert.Equal(t, "ship goal rpc", *runner.setReq.Objective)
		assert.Equal(t, "codex-thread-123", runner.setReq.ProviderSessionID)

		expectCodexSession()
		getResp, err := m.svc.GetGoal(ctx, &chat_svc.GoalRequest{SessionID: 100})
		assert.NoError(t, err)
		require.NotNil(t, getResp.Goal)
		assert.Equal(t, 7, getResp.Goal.TokensUsed)
		assert.Equal(t, "codex-thread-123", runner.getReq.ProviderSessionID)

		expectCodexSession()
		clearResp, err := m.svc.ClearGoal(ctx, &chat_svc.ClearGoalRequest{SessionID: 100})
		assert.NoError(t, err)
		assert.True(t, clearResp.Cleared)
		assert.Equal(t, "codex-thread-123", runner.clearReq.ProviderSessionID)
	})
}

// TestGoal_UsesSessionEffectiveProvider 钉死 /goal 入口的供应商口径：goal 与 turn 必须
// 落在同一家供应商上（会话 provider_key > agent 绑定，spec 2026-08-10「有效供应商解析
// （唯一口径）」）。
//
// goal 与 turn 共用同一个 codex app-server 会话池：acquireSession 的启动期比对键是
// (effectiveModel, effectiveProviderKey)（决策 4），goal 侧若还按 agent 绑定解析
// Provider，两边比对键就不一致 —— 一次 /goal 会把这条会话正在用的 app-server evict 掉
// 重 spawn（下一轮 turn 再 evict 一次），而且这次 goal 本身就打在用户没选的那家上游。
// 登录态后端（backend 未绑定、会话自己选了一家）是最直接的形态：Provider 会整个丢成 nil。
func TestGoal_UsesSessionEffectiveProvider(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	runner := &goalRecordingRunner{recordingRunner: &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}}
	t.Cleanup(agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, runner))

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
		ProviderSessionID: "codex-thread-123", ProviderKey: "session-picked",
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Codex", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	// 登录态 codex 后端：这一档没绑供应商，会话自己选了一家接管。
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "session-picked").Return(&llm_provider_entity.LLMProvider{ProviderKey: "session-picked", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-session-picked", ID: 34, Type: string(llm_provider_entity.TypeOpenAIResponse), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "session-picked")

	_, err := m.svc.GetGoal(ctx, &chat_svc.GoalRequest{SessionID: 100})
	require.NoError(t, err)
	require.NotNil(t, runner.getReq.Provider, "goal 必须带上本会话的 effective provider，而不是 agent 绑定")
	assert.Equal(t, "session-picked", runner.getReq.Provider.ProviderKey)
}

func TestStartGoal_CreatesCodexSessionAndSetsGoalBeforeFirstTurn(t *testing.T) {
	convey.Convey("Given a new Codex chat, when setting /goal before the first message, then a provider thread is created and stored without creating chat messages", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx
		runner := &goalRecordingRunner{recordingRunner: &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, runner)
		t.Cleanup(restore)

		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
			ID: 7, Name: "Codex", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
		}, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "", Status: consts.ACTIVE,
		}, nil)
		m.session.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, sess *chat_entity.Session) error {
				assert.Equal(t, int64(7), sess.AgentID)
				assert.Equal(t, "ship goal rpc", sess.Title)
				assert.Equal(t, "idle", sess.AgentStatus)
				assert.Empty(t, sess.PermissionMode)
				assert.Empty(t, sess.PermissionModeAtLaunch)
				assert.Empty(t, sess.ProviderSessionID)
				sess.ID = 100
				return nil
			})
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, sess *chat_entity.Session) error {
				assert.Equal(t, int64(100), sess.ID)
				assert.Equal(t, "codex-thread-created", sess.ProviderSessionID)
				assert.Equal(t, "idle", sess.AgentStatus)
				return nil
			})
		m.message.EXPECT().Create(gomock.Any(), gomock.Any()).Times(0)

		objective := "ship goal rpc"
		status := "active"
		resp, err := m.svc.StartGoal(ctx, &chat_svc.StartGoalRequest{
			AgentID:   7,
			Objective: &objective,
			Status:    &status,
		})

		assert.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, int64(100), resp.SessionID)
		require.NotNil(t, resp.Goal)
		assert.Equal(t, "ship goal rpc", resp.Goal.Objective)
		assert.Equal(t, "codex-thread-created", resp.Goal.ThreadID)
		require.NotNil(t, runner.setReq.Objective)
		assert.Equal(t, "ship goal rpc", *runner.setReq.Objective)
		assert.Empty(t, runner.setReq.ProviderSessionID)
	})

	convey.Convey("Given a new Codex chat, when /goal has no objective, then it is rejected before creating a session", t, func() {
		m := setupChatTest(t)
		blank := "   "

		resp, err := m.svc.StartGoal(m.ctx, &chat_svc.StartGoalRequest{
			AgentID:   7,
			Objective: &blank,
		})

		assert.Error(t, err)
		assert.Nil(t, resp)
	})
}

func TestGoal_RequiresCodexProviderSessionAndCapability(t *testing.T) {
	t.Run("missing provider session", func(t *testing.T) {
		m := setupChatTest(t)
		ctx := context.Background()
		m.session.EXPECT().Find(ctx, int64(100)).Return(&chat_entity.Session{
			ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
		}, nil)

		_, err := m.svc.GetGoal(ctx, &chat_svc.GoalRequest{SessionID: 100})
		assert.Error(t, err)
	})

	t.Run("non-codex backend", func(t *testing.T) {
		m := setupChatTest(t)
		ctx := context.Background()
		expectCapabilitySessionBackend(m, ctx, "thread-1", "Claude", agent_backend_entity.TypeClaudeCode)

		_, err := m.svc.GetGoal(ctx, &chat_svc.GoalRequest{SessionID: 100})
		assert.Error(t, err)
	})

	t.Run("codex runtime without goal capability", func(t *testing.T) {
		m := setupChatTest(t)
		ctx := context.Background()
		runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, runner)
		t.Cleanup(restore)

		expectCapabilitySessionBackend(m, ctx, "thread-1", "Codex", agent_backend_entity.TypeCodex)

		_, err := m.svc.GetGoal(ctx, &chat_svc.GoalRequest{SessionID: 100})
		assert.Error(t, err)
	})
}

func TestSend_ClaudeCodeLocalKeepsGatewayDepsForHooks(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	chat_svc.RegisterGateway(&fakeChatGateway{
		status: httpgateway.GatewayStatus{State: "running", URL: "http://127.0.0.1:60080"},
		token:  "chat-token",
	})
	t.Cleanup(func() { chat_svc.RegisterGateway(nil) })

	runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
	t.Cleanup(restore)

	sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Claude Local", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "", Status: consts.ACTIVE,
	}, nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{
		SessionID: 100,
		AgentID:   7,
		Text:      "hi",
	})
	assert.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	select {
	case req := <-runner.requests:
		assert.Equal(t, "http://127.0.0.1:60080", req.GatewayURL)
		assert.Equal(t, "chat-token", req.GatewayToken)
		assert.Nil(t, req.Provider)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime request")
	}
}

// capturingDaemonClient 抓住 remote.Runtime 通过 wire 向 daemon 发的
// runtime.run 请求体并立刻返错,让 chat_svc.runTurn 同步走到 failTurn,无需等待
// daemon 反向 notify。生产 *client.Client 是 WebSocket + Protobuf RPC,这里只覆盖
// chat_svc → remote.Runtime 编码出的 wire.RunParams 字段。
type capturingDaemonClient struct {
	runParams chan wire.RunParams
}

func (c *capturingDaemonClient) Call(_ context.Context, method string, params, _ any) error {
	if method == wire.MethodRun {
		if p, ok := params.(wire.RunParams); ok {
			select {
			case c.runParams <- p:
			default:
			}
		}
		return errors.New("captured for test")
	}
	return nil
}

func (*capturingDaemonClient) Notify(_ string, _ any) error { return nil }
func (*capturingDaemonClient) Handle(_ string, _ func(context.Context, json.RawMessage) (any, error)) {
}
func (*capturingDaemonClient) Closed() <-chan struct{} { return nil }
func (*capturingDaemonClient) Close() error            { return nil }

type preparedRemotePiClient struct {
	mu        sync.Mutex
	handlers  map[string]func(context.Context, json.RawMessage) (any, error)
	runParams []wire.RunParams
	activated func() bool
}

func newPreparedRemotePiClient(activated func() bool) *preparedRemotePiClient {
	return &preparedRemotePiClient{
		handlers:  map[string]func(context.Context, json.RawMessage) (any, error){},
		activated: activated,
	}
}

func (c *preparedRemotePiClient) Call(_ context.Context, method string, params, result any) error {
	switch method {
	case wire.MethodCapabilities:
		out := result.(*wire.CapabilitiesResult)
		out.Capabilities = capability.Capabilities{Set: map[capability.Capability]bool{
			capability.CapAbort:       true,
			capability.CapForkSession: true,
		}}
		return nil
	case wire.MethodRun:
		rp := params.(wire.RunParams)
		c.mu.Lock()
		c.runParams = append(c.runParams, rp)
		call := len(c.runParams)
		doneHandler := c.handlers[wire.NotifyRunResultDone]
		c.mu.Unlock()
		switch call {
		case 1:
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID}
			return nil
		case 2:
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID, ProviderSessionID: "pi-session-new"}
			return nil
		case 3:
			if c.activated == nil || !c.activated() {
				return errors.New("remote Pi prompt started before durable transcript activation")
			}
			if rp.ProviderSessionID != "pi-session-new" {
				return fmt.Errorf("remote Pi Start used provider session %q", rp.ProviderSessionID)
			}
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID, ProviderSessionID: "pi-session-new"}
			if doneHandler == nil {
				return errors.New("runtime.runResultDone handler not registered")
			}
			raw, err := json.Marshal(wire.RunResultDoneFrame{
				ConversationID: rp.ConversationID, ProviderSessionID: "pi-session-new", UserAnchor: "pi-entry-new",
			})
			if err != nil {
				return err
			}
			_, err = doneHandler(context.Background(), raw)
			return err
		default:
			return fmt.Errorf("unexpected runtime.run call %d", call)
		}
	case wire.MethodAbort:
		return nil
	default:
		return nil
	}
}

func (*preparedRemotePiClient) Notify(_ string, _ any) error { return nil }

func (c *preparedRemotePiClient) Handle(method string, fn func(context.Context, json.RawMessage) (any, error)) {
	c.mu.Lock()
	c.handlers[method] = fn
	c.mu.Unlock()
}

func (*preparedRemotePiClient) Closed() <-chan struct{} { return nil }
func (*preparedRemotePiClient) Close() error            { return nil }

func (c *preparedRemotePiClient) runs() []wire.RunParams {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]wire.RunParams, len(c.runParams))
	copy(out, c.runParams)
	return out
}

// TestSend_ClaudeCodeRemoteSkipsClientGatewayDeps 回归用户报告:
//   - agentred 部署在 local-coding,desktop 把本机 gateway URL (127.0.0.1:52401)
//     和明文 Provider 实体发给远端 claudecode 子进程,导致子进程拨自己的 loopback
//     拿到 "API Error: Unable to connect to API (ConnectionRefused)"。
//   - 根因:chat_svc.runTurn 无脑调 signChatTokenFor + 把 prov 塞进 req.Provider,
//     不区分本地/远端。远端 daemon 自己有 ProviderLookup + Gateway,该自家解。
//   - 修法:be.IsRemote() 时跳过 signChatTokenFor、清空 req.Provider,让 daemon
//     handlers/runtime.go 走自家 Lookup → 自家 Gateway 路径。同时也防止 APIKey
//     每个 turn 越线漂移。
func TestSend_ClaudeCodeRemoteSkipsClientGatewayDeps(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	chat_svc.RegisterGateway(&fakeChatGateway{
		status: httpgateway.GatewayStatus{State: "running", URL: "http://127.0.0.1:60080"},
		token:  "chat-token",
	})
	t.Cleanup(func() { chat_svc.RegisterGateway(nil) })

	capture := &capturingDaemonClient{runParams: make(chan wire.RunParams, 1)}

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	lease := mock_remote_device_svc.NewMockLease(ctrl)
	pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease, nil).AnyTimes()
	lease.EXPECT().Client().Return(protorpctest.WrapConnection(capture)).AnyTimes()
	lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	lease.EXPECT().Release().AnyTimes()
	chat_svc.SetConnPoolForTest(m.svc, pool)
	t.Cleanup(func() { chat_svc.SetConnPoolForTest(m.svc, nil) })
	pairChatTestDevices(t, 7)

	sess := &chat_entity.Session{ID: 100, ConversationID: convID(100), AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE}
	// AnyTimes:远端 runtime 出线前还要读一次这一行,问它的 conversation_id
	// (remote_pool.sessionConversationID)。
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil).AnyTimes()
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Claude Remote", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "key-5", DeviceFingerprint: "sha256:device-7", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-5").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-5", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-5", ID: 5, Type: string(llm_provider_entity.TypeAnthropic), Name: "huu-glm",
		APIKey:  "secret-key-should-not-cross-the-wire",
		BaseURL: "https://huu.dqy.ink", Status: consts.ACTIVE,
	}, nil).AnyTimes()
	expectProviderResolvable(m, "key-5")
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{
		SessionID: 100, AgentID: 7, Text: "hi",
	})
	assert.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	select {
	case p := <-capture.runParams:
		// 直接 marshal wire payload 后做子串扫描:即便未来谁手痒给 wire.RunParams 加
		// 回一个 GatewayURL/Token/Provider 之类字段,只要值真的越线进了 wire,这条
		// 断言就会红。比"字段级 == 空"更耐重命名/重构。
		raw, err := json.Marshal(p)
		require.NoError(t, err)
		body := string(raw)
		assert.NotContains(t, body, "127.0.0.1",
			"remote backend wire 不应含 desktop 本机 gateway 地址")
		assert.NotContains(t, body, "chat-token",
			"remote backend wire 不应含 desktop 签的 token")
		assert.NotContains(t, body, "secret-key-should-not-cross-the-wire",
			"remote backend wire 不应含 LLM provider APIKey 明文")
		// wire 必须带 stable provider key 给 daemon 自查 keychain。
		assert.Contains(t, body, `"llmProviderKey":"key-5"`,
			"远端 backend wire 必须带 stable provider key 给 daemon 解 keychain")
		// 防回归: 老的 int id 字段不能再出现。
		assert.NotContains(t, body, `"llmProviderId"`,
			"老的 int id 不能再出现在 wire 上")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime.run RPC")
	}
}

// TestSend_NewClaudeCodeBypassSessionPersistsPermissionModeAtLaunch 回归用户报告:
//   - 新建 claudecode 会话首发选 bypass,发完第一轮 bypass pill 反而被错灰。
//   - 根因:runtime 在 spawn goroutine 里写 at_launch,前端 LoadSession 抢在它前面
//     拿到空串,之后再不 reload,permissionModeDisabledReason 看到 active+launch=""
//     就把 bypass 禁用。
//   - 修法:Send 同步路径里 chat_repo.Session().Create 时就把 PermissionModeAtLaunch
//     写成 createPermissionMode 解析值,保证 LoadSession 永远拿得到正确值。
func TestSend_NewClaudeCodeBypassSessionPersistsPermissionModeAtLaunch(t *testing.T) {
	convey.Convey("新建 claudecode 会话首发 bypass: session.Create 时 PermissionModeAtLaunch 已落库", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx
		runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
			ID: 7, Name: "Claude", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
		}, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "", Status: consts.ACTIVE,
		}, nil)
		m.session.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, sess *chat_entity.Session) error {
				assert.Equal(t, "bypassPermissions", sess.PermissionMode)
				assert.Equal(t, "bypassPermissions", sess.PermissionModeAtLaunch,
					"at_launch 必须在 Send 同步写入,避免前端首轮 LoadSession 拿空串后 bypass 被错误禁用")
				sess.ID = 100
				return nil
			})
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		m.dbMock.ExpectBegin()
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
		m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				if msg.Role == "user" {
					msg.ID = 1000
				} else {
					msg.ID = 1001
				}
				return nil
			}).Times(2)
		m.dbMock.ExpectCommit()
		m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{
			AgentID:        7,
			Text:           "hi",
			PermissionMode: "bypassPermissions",
		})
		assert.NoError(t, err)
		chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)
	})
}

func TestSend_NewCodexSessionStoresPermissionModeBeforeFirstTurn(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, runner)
	t.Cleanup(restore)

	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Codex", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "", Status: consts.ACTIVE,
	}, nil)
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, sess *chat_entity.Session) error {
			assert.Equal(t, "plan", sess.PermissionMode)
			sess.ID = 100
			return nil
		})
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{
		AgentID:        7,
		Text:           "hi",
		PermissionMode: "plan",
	})
	assert.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	select {
	case req := <-runner.requests:
		assert.Equal(t, "plan", req.CollaborationMode)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime request")
	}
}

func TestSend_CodexPlanUpdatedPersistsVisiblePlanBlock(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, scriptedRunner{events: []agentruntime.RuntimeEvent{
		{Kind: agentruntime.EventPlanUpdated, Plan: []agentruntime.PlanStep{
			{Step: "Inspect files", Status: "completed"},
			{Step: "Describe next change", Status: "inProgress"},
		}},
		{Kind: agentruntime.EventDone},
	}})
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE, PermissionMode: "plan",
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Codex", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "", Status: consts.ACTIVE,
	}, nil)
	m.session.EXPECT().UpdatePermissionMode(gomock.Any(), int64(100), "plan").Return(nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	var final *chat_entity.Message
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			cp := *msg
			final = &cp
			return nil
		}).AnyTimes()
	// 轮内 checkpoint 已从 Update 改走 CheckpointBlocks(整表替换 → 差分写,见
	// chat_repo.syncBlocks);这条用例盯的正是 checkpoint 落了什么,两条路都要收。
	m.message.EXPECT().CheckpointBlocks(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message, _ string) error {
			cp := *msg
			final = &cp
			return nil
		}).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{
		SessionID:      100,
		AgentID:        7,
		Text:           "hi",
		PermissionMode: "plan",
	})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	require.NotNil(t, final)
	blocks, err := final.GetBlocks()
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	var planText string
	switch pb := blocks[0].(type) {
	case chat_svc.PlanBlock:
		planText = pb.Text
	case *chat_svc.PlanBlock:
		planText = pb.Text
	default:
		t.Fatalf("expected PlanBlock, got %T", blocks[0])
	}
	assert.Contains(t, planText, "Inspect files")
	assert.Contains(t, planText, "[>]")
}

// TestSend_UnansweredAskUserQuestionExpiresAtFinalize 回归 sess-1174 的死卡:
// turn 内 emit 了 AskUserQuestion 但未答(无 resolved 帧)就 turn done,
// finalize 必须把该 UserAskBlock 标 expired 并落库,否则该卡片在前端永远可点、
// 提交必失败(runtime SubmitAnswer 走 ErrNoActiveTurn / 无 waiter)。
func TestSend_UnansweredAskUserQuestionExpiresAtFinalize(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, scriptedRunner{events: []agentruntime.RuntimeEvent{
		{Kind: agentruntime.EventAskUserQuestion, AskUserQuestion: &agentruntime.AskUserQuestionEvent{
			RequestID: "ask-1",
			Questions: []agentruntime.AskQuestion{{ID: "q1", Question: "ok?", Options: []agentruntime.AskOption{{Label: "Y"}}}},
		}},
		{Kind: agentruntime.EventDone},
	}})
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Codex", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeCodex), Status: consts.ACTIVE,
	}, nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	var final *chat_entity.Message
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			cp := *msg
			final = &cp
			return nil
		}).AnyTimes()
	// 轮内 checkpoint 已从 Update 改走 CheckpointBlocks(整表替换 → 差分写,见
	// chat_repo.syncBlocks);这条用例盯的正是 checkpoint 落了什么,两条路都要收。
	m.message.EXPECT().CheckpointBlocks(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message, _ string) error {
			cp := *msg
			final = &cp
			return nil
		}).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	require.NotNil(t, final)
	bs, err := final.GetBlocks()
	require.NoError(t, err)
	var found bool
	for _, b := range bs {
		switch ua := b.(type) {
		case *chatblocks.UserAskBlock:
			if ua.RequestID == "ask-1" {
				found = true
				assert.True(t, ua.Expired, "未答 ask 应在 finalize 标 expired")
				assert.False(t, ua.Answered)
				assert.False(t, ua.Skipped)
			}
		case chatblocks.UserAskBlock:
			if ua.RequestID == "ask-1" {
				found = true
				assert.True(t, ua.Expired, "未答 ask 应在 finalize 标 expired")
			}
		}
	}
	assert.True(t, found, "应持久化 ask-1 的 UserAskBlock")
}

func TestSend_CodexPlanItemTextPersistsVisiblePlanBlock(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, scriptedRunner{events: []agentruntime.RuntimeEvent{
		{Kind: agentruntime.EventPlanUpdated, PlanText: "# Plan\n\n1. Inspect files\n2. Report findings\n"},
		{Kind: agentruntime.EventDone},
	}})
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE, PermissionMode: "plan",
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Codex", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "", Status: consts.ACTIVE,
	}, nil)
	m.session.EXPECT().UpdatePermissionMode(gomock.Any(), int64(100), "plan").Return(nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	var final *chat_entity.Message
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			cp := *msg
			final = &cp
			return nil
		}).AnyTimes()
	// 轮内 checkpoint 已从 Update 改走 CheckpointBlocks(整表替换 → 差分写,见
	// chat_repo.syncBlocks);这条用例盯的正是 checkpoint 落了什么,两条路都要收。
	m.message.EXPECT().CheckpointBlocks(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message, _ string) error {
			cp := *msg
			final = &cp
			return nil
		}).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{
		SessionID:      100,
		AgentID:        7,
		Text:           "hi",
		PermissionMode: "plan",
	})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	require.NotNil(t, final)
	blocks, err := final.GetBlocks()
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	pb, ok := blocks[0].(chat_svc.PlanBlock)
	if !ok {
		ptr, ptrOK := blocks[0].(*chat_svc.PlanBlock)
		require.True(t, ptrOK, "expected PlanBlock, got %T", blocks[0])
		pb = *ptr
	}
	assert.Equal(t, "# Plan\n\n1. Inspect files\n2. Report findings\n", pb.Text)
}

func TestSend_ActionablePlanBlockMarksSessionWaiting(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, newEventRunner{events: []agentruntime.Event{
		agentruntime.PlanUpdated{Plan: canonical.PlanUpdate{
			Text: "# Plan\n\n1. Inspect files\n2. Report findings\n",
			Actions: []canonical.PlanAction{
				{ID: "plan.execute", Kind: canonical.PlanActionApprove},
				{ID: "plan.refine", Kind: canonical.PlanActionRefine, RequiresFeedback: true},
			},
		}},
	}})
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE, PermissionMode: "plan",
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Codex", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "", Status: consts.ACTIVE,
	}, nil)
	m.session.EXPECT().UpdatePermissionMode(gomock.Any(), int64(100), "plan").Return(nil)

	var sessionUpdates []*chat_entity.Session
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, sess *chat_entity.Session) error {
			cp := *sess
			sessionUpdates = append(sessionUpdates, &cp)
			return nil
		}).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	var final *chat_entity.Message
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			cp := *msg
			final = &cp
			return nil
		}).AnyTimes()
	// 轮内 checkpoint 已从 Update 改走 CheckpointBlocks(整表替换 → 差分写,见
	// chat_repo.syncBlocks);这条用例盯的正是 checkpoint 落了什么,两条路都要收。
	m.message.EXPECT().CheckpointBlocks(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message, _ string) error {
			cp := *msg
			final = &cp
			return nil
		}).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{
		SessionID:      100,
		AgentID:        7,
		Text:           "hi",
		PermissionMode: "plan",
	})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	require.NotEmpty(t, sessionUpdates)
	last := sessionUpdates[len(sessionUpdates)-1]
	assert.Equal(t, "waiting", last.AgentStatus)
	assert.True(t, last.NeedsAttention)

	require.NotNil(t, final)
	blocks, err := final.GetBlocks()
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	pb, ok := blocks[0].(chat_svc.PlanBlock)
	if !ok {
		ptr, ptrOK := blocks[0].(*chat_svc.PlanBlock)
		require.True(t, ptrOK, "expected PlanBlock, got %T", blocks[0])
		pb = *ptr
	}
	require.Len(t, pb.Actions, 2)
	assert.Equal(t, "plan.execute", pb.Actions[0].ID)
}

// stopErrRunner 复刻 remote.Runtime 退避超上限后的收尾形状:events 正常关闭(先前
// 已经流出去的内容照旧落库),终止原因只经 RunResult.StopErr 交回,不走 Run 的返回错误。
type stopErrRunner struct {
	events []agentruntime.Event
	stop   error
}

func (stopErrRunner) Capabilities() capability.Capabilities { return capability.Capabilities{} }

func (r stopErrRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	ch := make(chan agentruntime.Event, len(r.events))
	for _, e := range r.events {
		ch <- e
	}
	close(ch)
	return ch, &agentruntime.RunResult{StopErr: r.stop}, nil
}

// Given 一条远端会话跑到一半,通道断了且重连退避超上限(remote.Runtime 把
// ErrDaemonDisconnected 放进 RunResult.StopErr 收尾),When 该 turn 收口,
// Then 会话落既有的 error 态,而不是新增第五个取值、也不是被当成用户主动中止落 idle。
//
// 为什么这条守卫必须打在 Go 层:R15 的映射是后端 runTurn 收尾那段 switch 决定的,
// 前端只是照单渲染。把它写成"前端 store 里塞 error 再读回 error"证明不了任何实现 ——
// 任何映射都能通过。这里从 StopErr 一路驱动到落库的 AgentStatus,映射被改错就变红。
func TestSend_TerminalDaemonDisconnectLandsErrorStatus(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, stopErrRunner{
		events: []agentruntime.Event{agentruntime.TextDelta{Text: "half a sentence"}},
		stop:   remote.ErrDaemonDisconnected,
	})
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")

	var sessionUpdates []*chat_entity.Session
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, sess *chat_entity.Session) error {
			cp := *sess
			sessionUpdates = append(sessionUpdates, &cp)
			return nil
		}).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	var final *chat_entity.Message
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			cp := *msg
			final = &cp
			return nil
		}).AnyTimes()
	// 轮内 checkpoint 已从 Update 改走 CheckpointBlocks(整表替换 → 差分写,见
	// chat_repo.syncBlocks);这条用例盯的正是 checkpoint 落了什么,两条路都要收。
	m.message.EXPECT().CheckpointBlocks(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message, _ string) error {
			cp := *msg
			final = &cp
			return nil
		}).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	require.NotEmpty(t, sessionUpdates)
	last := sessionUpdates[len(sessionUpdates)-1]
	assert.Equal(t, "error", last.AgentStatus,
		"终止性断连沿用既有 error 态:不新增第五个 AgentStatus 取值,也不能被当成主动中止落 idle")
	assert.False(t, last.NeedsAttention, "error 态不等人,不该点亮 attention")

	patches := captureSessionStatusPatches(m.events)
	require.NotEmpty(t, patches, "终态必须 emit 一帧 session_status,否则 tab 停在运行中")
	assert.Equal(t, "error", patches[len(patches)-1].AgentStatus)

	require.NotNil(t, final)
	assert.NotEmpty(t, final.ErrorText,
		"落 error 的理由要留在消息文案里 —— 中断与真实错误靠文案区分,不靠新状态")
	assert.NotContains(t, final.ErrorText, remote.ErrDaemonDisconnected.Error(),
		"交到用户面前的是人话文案,不是 Go 哨兵字符串")
}

// runStopErrTurnErrorText 跑一轮以 RunResult.StopErr 收尾的 turn,交出落库的终态文案。
func runStopErrTurnErrorText(t *testing.T, stop error) string {
	t.Helper()
	m := setupChatTest(t)
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, stopErrRunner{
		events: []agentruntime.Event{agentruntime.TextDelta{Text: "half a sentence"}},
		stop:   stop,
	})
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	var final *chat_entity.Message
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			cp := *msg
			final = &cp
			return nil
		}).AnyTimes()
	// 轮内 checkpoint 已从 Update 改走 CheckpointBlocks(整表替换 → 差分写,见
	// chat_repo.syncBlocks);这条用例盯的正是 checkpoint 落了什么,两条路都要收。
	m.message.EXPECT().CheckpointBlocks(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message, _ string) error {
			cp := *msg
			final = &cp
			return nil
		}).AnyTimes()

	resp, err := m.svc.Send(m.ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)
	require.NotNil(t, final, "终态必须落库一条 assistant 消息")
	return final.ErrorText
}

// Given 三种终止:daemon 重启把这一轮打断了 / 连不上那台 daemon 了 / agent 真的跑失败了,
// When 各自收口,Then 落库的终态文案两两不同 —— R15 明确「不向 AgentStatus 增加第五个
// 取值」,三者都落在既有的 error 上,**由消息文案区分**;界面约定进一步要求「被打断」的
// 措辞与真实运行错误分开(是被打断,不是跑失败)。
//
// 为什么守卫打在这里:文案是经 assistant 消息的 errorText 持久化后到达用户的,前端只是
// 照单渲染。把区分寄托在前端就等于没有区分 —— 重开 App 后前端手里只有这一个字符串。
func TestSend_TerminalStopErrCopy_DistinguishesInterruptedDisconnectedAndFailure(t *testing.T) {
	interrupted := runStopErrTurnErrorText(t, remote.ErrRunInterrupted)
	disconnected := runStopErrTurnErrorText(t, remote.ErrDaemonDisconnected)
	failed := runStopErrTurnErrorText(t, errors.New("API error 500: upstream exploded"))

	assert.NotEqual(t, disconnected, interrupted,
		"「被打断」与「连不上了」必须是两句话,否则用户分不出远端是重启了还是网断了")
	assert.NotEqual(t, failed, interrupted,
		"「被打断」不能与真实运行错误同文案 —— 是被打断,不是跑失败")
	assert.NotEqual(t, failed, disconnected)

	assert.NotContains(t, interrupted, "agentruntime/runtimes/remote:",
		"终态文案是给用户看的人话,不是 Go 哨兵字符串")
	assert.NotContains(t, disconnected, "agentruntime/runtimes/remote:")
	assert.Contains(t, interrupted, "打断",
		"中断态会话的措辞要说明这是被打断")
	assert.Equal(t, "API error 500: upstream exploded", failed,
		"真实运行错误照原样交给用户,不得被断连文案顶掉")
}

func TestResolvePlanAction_CodexExecuteContinuesWaitingPlan(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	runner := captureRunRequestRunner{
		events:   []agentruntime.Event{agentruntime.TextDelta{Text: "executed"}},
		requests: make(chan agentruntime.RunRequest, 1),
	}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, runner)
	t.Cleanup(restore)

	planMsg := &chat_entity.Message{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2}
	require.NoError(t, planMsg.SetBlocks([]blocks.ContentBlock{chat_svc.PlanBlock{
		Text: "# Plan\n\n1. Execute",
		Actions: []canonical.PlanAction{
			{ID: "plan.execute", Kind: canonical.PlanActionApprove},
			{ID: "plan.refine", Kind: canonical.PlanActionRefine, RequiresFeedback: true},
		},
	}}))

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "waiting", Status: consts.ACTIVE, PermissionMode: "plan",
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Codex", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "", Status: consts.ACTIVE,
	}, nil)
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
		{ID: 1000, SessionID: 100, Role: "user", Seq: 1},
		planMsg,
	}, nil).Times(2)
	m.session.EXPECT().UpdatePermissionMode(gomock.Any(), int64(100), "default").Return(nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1002
			} else {
				msg.ID = 1003
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()
	var clearedPlanActions bool
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.ID == planMsg.ID {
				bs, err := msg.GetBlocks()
				require.NoError(t, err)
				require.Len(t, bs, 1)
				switch p := bs[0].(type) {
				case chat_svc.PlanBlock:
					clearedPlanActions = len(p.Actions) == 0
				case *chat_svc.PlanBlock:
					clearedPlanActions = p != nil && len(p.Actions) == 0
				default:
					t.Fatalf("expected PlanBlock, got %T", bs[0])
				}
			}
			return nil
		}).AnyTimes()

	resp, err := m.svc.ResolvePlanAction(ctx, &chat_svc.ResolvePlanActionRequest{
		SessionID: 100,
		ActionID:  canonical.PlanActionIDExecute,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, int64(100), resp.SessionID)
	assert.Equal(t, int64(1002), resp.UserMessageID)
	assert.Equal(t, int64(1003), resp.AssistantMessageID)
	assert.Equal(t, chat_svc.StreamName(100, 1003), resp.Stream)
	chat_svc.WaitForStreamForTest(m.svc, 1003)

	req := <-runner.requests
	assert.Equal(t, "Implement the plan.", req.UserText)
	assert.Equal(t, "default", req.CollaborationMode)
	assert.True(t, clearedPlanActions)
}

func TestSend_CodexPlanEmptyTurnPersistsFallbackText(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, scriptedRunner{events: []agentruntime.RuntimeEvent{
		{Kind: agentruntime.EventDone},
	}})
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE, PermissionMode: "plan",
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Codex", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "", Status: consts.ACTIVE,
	}, nil)
	m.session.EXPECT().UpdatePermissionMode(gomock.Any(), int64(100), "plan").Return(nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	var final *chat_entity.Message
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			cp := *msg
			final = &cp
			return nil
		}).AnyTimes()
	// 轮内 checkpoint 已从 Update 改走 CheckpointBlocks(整表替换 → 差分写,见
	// chat_repo.syncBlocks);这条用例盯的正是 checkpoint 落了什么,两条路都要收。
	m.message.EXPECT().CheckpointBlocks(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message, _ string) error {
			cp := *msg
			final = &cp
			return nil
		}).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{
		SessionID:      100,
		AgentID:        7,
		Text:           "hi",
		PermissionMode: "plan",
	})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	require.NotNil(t, final)
	gotBlocks, err := final.GetBlocks()
	require.NoError(t, err)
	require.Len(t, gotBlocks, 1)
	var text string
	switch tb := gotBlocks[0].(type) {
	case blocks.TextBlock:
		text = tb.Text
	case *blocks.TextBlock:
		text = tb.Text
	default:
		t.Fatalf("expected TextBlock, got %T", gotBlocks[0])
	}
	assert.Contains(t, text, "Plan mode completed")

	// 正常收尾也必须在 StreamDone 前主动发布 idle。否则前端会先删除
	// LiveStream（底部停止输出），tab 却仍保留上一帧 running，直到异步 reload 完成。
	doneIdx, idleIdx := -1, -1
	for i, ev := range m.events {
		payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
		if !ok {
			continue
		}
		if idleIdx < 0 && payload.Kind == chat_svc.StreamSessionStatus &&
			payload.SessionStatus != nil && payload.SessionStatus.AgentStatus == "idle" {
			idleIdx = i
		}
		if doneIdx < 0 && payload.Kind == chat_svc.StreamDone {
			doneIdx = i
		}
	}
	require.GreaterOrEqual(t, idleIdx, 0, "正常收尾缺 session_status(idle)")
	require.GreaterOrEqual(t, doneIdx, 0, "正常收尾缺 StreamDone")
	assert.Less(t, idleIdx, doneIdx, "session_status(idle) 必须先于 StreamDone")
	var lastKind chat_svc.ChatStreamEventKind
	for _, ev := range m.events {
		if payload, ok := ev.Payload.(chat_svc.ChatStreamEvent); ok {
			lastKind = payload.Kind
		}
	}
	assert.Equal(t, chat_svc.StreamDone, lastKind,
		"explicit terminal outcome must be the final frame; a redundant closed tail can hide it in the Wails bridge")
}

func TestSend_RuntimeErrorsStayVisibleAndPersistedWithoutEnteringLogs(t *testing.T) {
	tests := []struct {
		name       string
		sentinel   string
		runner     agentruntime.Runtime
		logMessage string
	}{
		{
			name:       "ErrorEvent",
			sentinel:   "SENTINEL_CHAT_ERROR_EVENT",
			runner:     streamErrorRunner{err: errors.New("SENTINEL_CHAT_ERROR_EVENT")},
			logMessage: "chat_svc.runTurn: ErrorEvent intercepted",
		},
		{
			name:       "RunResult StopErr",
			sentinel:   "SENTINEL_CHAT_RESULT_STOP_ERROR",
			runner:     resultStopErrorRunner{err: errors.New("SENTINEL_CHAT_RESULT_STOP_ERROR")},
			logMessage: "chat_svc.runTurn: stopErr promoted from RunResult.StopErr",
		},
		{
			name:       "runner failure",
			sentinel:   "SENTINEL_CHAT_RUN_ERROR",
			runner:     failRunner{err: errors.New("SENTINEL_CHAT_RUN_ERROR")},
			logMessage: "chat_svc.failTurn: turn failed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := setupChatTest(t)
			core, logs := observer.New(zapcore.DebugLevel)
			capturingLogger := zap.New(core)
			oldLogger := logger.Default()
			logger.SetLogger(capturingLogger)
			t.Cleanup(func() { logger.SetLogger(oldLogger) })
			ctx := logger.WithContextLogger(m.ctx, capturingLogger)
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, test.runner)
			t.Cleanup(restore)

			m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
				ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
				ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
			}, nil)
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
			}, nil)
			m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
			expectProviderResolvable(m, "key-21")
			m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

			m.dbMock.ExpectBegin()
			m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
			m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
					if msg.Role == "user" {
						msg.ID = 1000
					} else {
						msg.ID = 1001
					}
					return nil
				}).Times(2)
			m.dbMock.ExpectCommit()

			m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
			var persistedMu sync.Mutex
			persistedErrorText := ""
			m.message.EXPECT().Update(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
					if msg != nil && msg.ErrorText != "" {
						persistedMu.Lock()
						persistedErrorText = msg.ErrorText
						persistedMu.Unlock()
					}
					return nil
				}).AnyTimes()

			resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
			require.NoError(t, err)
			chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

			var errorEvent *chat_svc.ChatStreamEvent
			for _, emitted := range m.events {
				payload, ok := emitted.Payload.(chat_svc.ChatStreamEvent)
				if ok && payload.Kind == chat_svc.StreamError {
					payloadCopy := payload
					errorEvent = &payloadCopy
					break
				}
			}
			require.NotNil(t, errorEvent)
			assert.Equal(t, test.sentinel, errorEvent.Error)
			require.NotNil(t, errorEvent.Message)
			assert.Equal(t, test.sentinel, errorEvent.Message.ErrorText)
			persistedMu.Lock()
			assert.Equal(t, test.sentinel, persistedErrorText)
			persistedMu.Unlock()

			captured := observedChatLogText(logs)
			assert.NotContains(t, captured, test.sentinel)
			matches := logs.FilterMessage(test.logMessage).All()
			require.Len(t, matches, 1)
			assert.Equal(t, "*errors.errorString", matches[0].ContextMap()["errorClass"])
			assert.Equal(t, int64(len(test.sentinel)), matches[0].ContextMap()["errorBytes"])
		})
	}
}

func observedChatLogText(logs *observer.ObservedLogs) string {
	var out strings.Builder
	for _, entry := range logs.All() {
		_, _ = fmt.Fprintf(&out, "%s %v\n", entry.Message, entry.ContextMap())
	}
	return out.String()
}

func TestSend_StreamErrorEventCarriesFinalAssistantMessage(t *testing.T) {
	// Given a runtime stream fails, when chat finalizes the assistant row, then
	// the error event carries the final snapshot. Pi process failures additionally
	// keep prompt, credential, and stderr payloads out of persisted/displayed copy.
	piSecrets := []string{
		"private user prompt: inspect payroll",
		"Authorization: Bearer private-chat-token",
		"stderr private session-entry payload",
	}
	tests := []struct {
		name         string
		backendType  agent_backend_entity.BackendType
		runtimeError error
		wantError    string
		secrets      []string
	}{
		{
			name:        "Given a generic runtime error, then the final assistant snapshot retains its useful message",
			backendType: agent_backend_entity.TypeBuiltin,
			wantError:   "upstream failed",
		},
		{
			name:        "Given a Pi process error with private payloads, then chat displays only the stable process classification",
			backendType: agent_backend_entity.TypePiAgent,
			runtimeError: &pkgpiagent.ExitError{
				Err:    errors.New("command failed with " + piSecrets[0]),
				Stderr: piSecrets[1] + " | " + piSecrets[2],
			},
			wantError: "piagent: process exited: process failure",
			secrets:   piSecrets,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := setupChatTest(t)
			ctx := m.ctx
			restore := agentruntime.SwapRuntimeForTest(tt.backendType, streamErrorRunner{err: tt.runtimeError})
			t.Cleanup(restore)

			m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
				ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
				ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
			}, nil)
			backend := &agent_backend_entity.AgentBackend{
				ID: 12, Type: string(tt.backendType), Status: consts.ACTIVE,
			}
			if tt.backendType == agent_backend_entity.TypeBuiltin {
				backend.LLMProviderKey = "key-21"
			}
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(backend, nil)
			if tt.backendType == agent_backend_entity.TypeBuiltin {
				m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
				expectProviderResolvable(m, "key-21")
			}
			m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
			if tt.backendType == agent_backend_entity.TypePiAgent {
				expectNoPiTranscriptRecovery(m, 100)
			}

			m.dbMock.ExpectBegin()
			m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
			m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
					if msg.Role == "user" {
						msg.ID = 1000
					} else {
						msg.ID = 1001
					}
					return nil
				}).Times(2)
			m.dbMock.ExpectCommit()

			m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
			m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

			resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
			require.NoError(t, err)
			chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

			var errorEvent *chat_svc.ChatStreamEvent
			for _, ev := range m.events {
				payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
				if ok && payload.Kind == chat_svc.StreamError {
					cp := payload
					errorEvent = &cp
					break
				}
			}
			if assert.NotNil(t, errorEvent) && assert.NotNil(t, errorEvent.Message) {
				assert.Equal(t, tt.wantError, errorEvent.Error)
				assert.Equal(t, int64(1001), errorEvent.Message.ID)
				assert.Equal(t, tt.wantError, errorEvent.Message.ErrorText)
				assert.Equal(t, "partial answer", errorEvent.Message.Blocks[0].Text)
				for _, secret := range tt.secrets {
					assert.NotContains(t, errorEvent.Error, secret)
					assert.NotContains(t, errorEvent.Message.ErrorText, secret)
				}
			}
		})
	}
}

// TestSend_StreamErrorAlsoEmitsSessionStatusError 回归:turn 内 ErrorEvent 触发
// stopErr 末端把 DB 翻 "error" 后,必须 emit 一帧 StreamSessionStatus{agentStatus:
// "error"}。否则后台 session 出错时,tab 的 status dot 翻红要等下次 ListChatAgents
// 才同步 —— bumpDone 只动 doneTick,不改 agentStatus。session_status patch 必须
// 在 StreamError 之前 emit,否则前端 finishStream 已经把 LiveStream entry 删了,
// StreamSubscriber 紧接着 unmount,后到的 session_status 永远收不到。
func TestSend_StreamErrorAlsoEmitsSessionStatusError(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, streamErrorRunner{})
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	patches := captureSessionStatusPatches(m.events)
	require.NotEmpty(t, patches, "stopErr 末端必须 emit 一帧 StreamSessionStatus 给前端,否则 tab 不翻红")
	last := patches[len(patches)-1]
	assert.Equal(t, "error", last.AgentStatus)
	assert.False(t, last.NeedsAttention, "error 态不需要 needsAttention")

	// 顺序:session_status 必须先于 StreamError emit。
	errorIdx, statusIdx := -1, -1
	for i, ev := range m.events {
		payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
		if !ok {
			continue
		}
		if statusIdx < 0 && payload.Kind == chat_svc.StreamSessionStatus &&
			payload.SessionStatus != nil && payload.SessionStatus.AgentStatus == "error" {
			statusIdx = i
		}
		if errorIdx < 0 && payload.Kind == chat_svc.StreamError {
			errorIdx = i
		}
	}
	require.GreaterOrEqual(t, statusIdx, 0, "缺 session_status(error)")
	require.GreaterOrEqual(t, errorIdx, 0, "缺 StreamError")
	assert.Less(t, statusIdx, errorIdx,
		"session_status 必须先于 StreamError,否则前端已经 finishStream 把订阅撤了")
}

func TestSend_StreamErrorFollowedByProgressDoesNotFailTurn(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, streamErrorThenRecoverRunner{})
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	var assistantSnaps []chat_entity.Message
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg != nil && msg.Role == "assistant" {
				assistantSnaps = append(assistantSnaps, *msg)
			}
			return nil
		}).AnyTimes()
	var sessionSnaps []chat_entity.Session
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			sessionSnaps = append(sessionSnaps, *s)
			return nil
		}).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	var sawDone, sawError bool
	for _, ev := range m.events {
		payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
		if !ok {
			continue
		}
		switch payload.Kind {
		case chat_svc.StreamDone:
			sawDone = true
			require.NotNil(t, payload.Message)
			assert.Empty(t, payload.Message.ErrorText)
			require.Len(t, payload.Message.Blocks, 1)
			assert.Equal(t, "recovered answer", payload.Message.Blocks[0].Text)
		case chat_svc.StreamError:
			sawError = true
		}
	}
	assert.True(t, sawDone)
	assert.False(t, sawError, "recoverable error followed by progress must not fail the turn")
	require.NotEmpty(t, sessionSnaps)
	assert.Equal(t, "idle", sessionSnaps[len(sessionSnaps)-1].AgentStatus)
	require.NotEmpty(t, assistantSnaps)
	assert.Empty(t, assistantSnaps[len(assistantSnaps)-1].ErrorText)
}

func TestSend_StreamErrorFollowedOnlyByMetadataStillFailsTurn(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, streamErrorThenMetadataRunner{})
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	var sessionSnaps []chat_entity.Session
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			sessionSnaps = append(sessionSnaps, *s)
			return nil
		}).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	var sawError bool
	for _, ev := range m.events {
		payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
		if ok && payload.Kind == chat_svc.StreamError {
			sawError = true
			assert.Contains(t, payload.Error, "temporary upstream hiccup")
		}
	}
	assert.True(t, sawError)
	require.NotEmpty(t, sessionSnaps)
	assert.Equal(t, "error", sessionSnaps[len(sessionSnaps)-1].AgentStatus)
}

// TestSend_FailTurnEmitsSessionStatusError 回归:runner.Run 直接同步返错走 failTurn,
// failTurn 把 DB 翻 "error" 后必须 emit 一帧 StreamSessionStatus{agentStatus:"error"}
// 给前端,语义与 stopErr 末端一致。同样要在 StreamError 之前 emit。
func TestSend_FailTurnEmitsSessionStatusError(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(
		agent_backend_entity.TypeBuiltin,
		failRunner{err: errors.New("runner boom")},
	)
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	patches := captureSessionStatusPatches(m.events)
	require.NotEmpty(t, patches, "failTurn 必须 emit 一帧 StreamSessionStatus(error)")
	last := patches[len(patches)-1]
	assert.Equal(t, "error", last.AgentStatus)
	assert.False(t, last.NeedsAttention)

	errorIdx, statusIdx := -1, -1
	for i, ev := range m.events {
		payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
		if !ok {
			continue
		}
		if statusIdx < 0 && payload.Kind == chat_svc.StreamSessionStatus &&
			payload.SessionStatus != nil && payload.SessionStatus.AgentStatus == "error" {
			statusIdx = i
		}
		if errorIdx < 0 && payload.Kind == chat_svc.StreamError {
			errorIdx = i
		}
	}
	require.GreaterOrEqual(t, statusIdx, 0, "缺 session_status(error)")
	require.GreaterOrEqual(t, errorIdx, 0, "缺 StreamError")
	assert.Less(t, statusIdx, errorIdx,
		"session_status 必须先于 StreamError,否则前端已经 finishStream 把订阅撤了")
}

func TestSend_StreamRetryEventIsForwardedWithoutFailingTurn(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, streamRetryRunner{})
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	assert.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	var retryEvent *chat_svc.ChatStreamEvent
	var sawDone bool
	var sawError bool
	for _, ev := range m.events {
		payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
		if !ok {
			continue
		}
		switch payload.Kind {
		case chat_svc.StreamRetry:
			cp := payload
			retryEvent = &cp
		case chat_svc.StreamDone:
			sawDone = true
		case chat_svc.StreamError:
			sawError = true
		}
	}
	if assert.NotNil(t, retryEvent) {
		assert.Equal(t, 1, retryEvent.RetryAttempt)
		assert.Equal(t, 5, retryEvent.RetryMaxAttempts)
		assert.Equal(t, "Reconnecting... 1/5", retryEvent.RetryMessage)
		assert.Equal(t, "high demand", retryEvent.RetryDetails)
		assert.NotZero(t, retryEvent.RetryAt)
	}
	assert.True(t, sawDone)
	assert.False(t, sawError)
}

// TestSend_StreamUsageEventsAreForwardedAndPersisted —— turn 内每次 EventUsage 都应：
//  1. emit 一条 StreamUsage（payload 字段一致），让前端 Composer 进度条实时刷新；
//  2. patch assistantMsg 的 token 列（per-frame Update 落库）；
//  3. prompt/cache 取最后一帧，completion 按调用累加。Done 的 last-call usage 不再覆盖合计。
func TestSend_StreamUsageEventsAreForwardedAndPersisted(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx

	// 一个吐两帧 EventUsage 的 fake runner —— 模拟 turn 内两次内部 API call 的边界。
	runner := scriptedRunner{events: []agentruntime.RuntimeEvent{
		{Kind: agentruntime.EventTextDelta, Text: "thinking..."},
		{Kind: agentruntime.EventUsage, Usage: &provider.Usage{
			PromptTokens: 200, CompletionTokens: 50, CachedTokens: 10000, CacheCreationTokens: 0,
		}},
		{Kind: agentruntime.EventUsage, Usage: &provider.Usage{
			PromptTokens: 50, CompletionTokens: 20, CachedTokens: 10300, CacheCreationTokens: 50,
		}},
		{Kind: agentruntime.EventDone},
	}}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()

	// 记录 assistant 消息 Update 的所有快照，确保 turn 末尾写到最新值。
	var assistantSnaps []chat_entity.Message
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "assistant" {
				assistantSnaps = append(assistantSnaps, *msg)
			}
			return nil
		}).AnyTimes()

	// per-frame usage 走**单列**写(不是整行 Update):整行会把已累积的 blocks_json
	// 一起重写,而 usage 每个 API call 来一帧。这里把每帧的落库值记下来单独断言。
	var usageWrites []chat_repo.MessageUsage
	m.message.EXPECT().UpdateUsage(gomock.Any(), int64(1001), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ int64, u chat_repo.MessageUsage) error {
			usageWrites = append(usageWrites, u)
			return nil
		}).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	// 收到的 StreamUsage 事件：两条，分别对应两帧 EventUsage。
	var usages []chat_svc.ChatStreamEvent
	for _, ev := range m.events {
		payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
		if ok && payload.Kind == chat_svc.StreamUsage {
			usages = append(usages, payload)
		}
	}
	require.Len(t, usages, 2, "每帧 EventUsage 应当 emit 一条 StreamUsage")
	require.NotNil(t, usages[0].Usage)
	assert.Equal(t, int64(1001), usages[0].Usage.MessageID, "StreamUsage 必须带 assistantMsg.ID 让前端按消息匹配")
	assert.Equal(t, 200, usages[0].Usage.PromptTokens)
	assert.Equal(t, 10000, usages[0].Usage.CachedTokens)
	require.NotNil(t, usages[1].Usage)
	assert.Equal(t, 50, usages[1].Usage.PromptTokens)
	assert.Equal(t, 10300, usages[1].Usage.CachedTokens)
	assert.Equal(t, 50, usages[1].Usage.CacheCreationTokens)

	// 两帧 EventUsage 各落一次单列写,且第二次带的是累积后的最终值。
	require.Len(t, usageWrites, 2, "每帧 EventUsage 落库一次 UpdateUsage")
	final := usageWrites[len(usageWrites)-1]
	assert.Equal(t, 50, final.PromptTokens, "↑ 取最后一次调用的 prompt")
	assert.Equal(t, 10300, final.CachedTokens)
	assert.Equal(t, 50, final.CacheCreationTokens)
	assert.Equal(t, 70, final.CompletionTokens, "↓ 为本轮各次 completion 之和")

	// turn 末尾仍有一次整行 Update(落 blocks_json),它带的 token 值必须与单列写一致 ——
	// 两条路径写同几列,不一致就说明内存实体和落库值分叉了。
	require.NotEmpty(t, assistantSnaps, "turn 末尾应当整行落一次 blocks_json")
	last := assistantSnaps[len(assistantSnaps)-1]
	assert.Equal(t, final.PromptTokens, last.PromptTokens)
	assert.Equal(t, final.CompletionTokens, last.CompletionTokens)
}

// scriptedRunner 按预设序列吐 RuntimeEvent 字面量(老 fixture 风格),内部转 NEW
// Event 喂给 chat_svc dispatcher。生产 runner 已直接发 NEW Event;这里保留
// RuntimeEvent 入参,是为了让大量老测试 fixture 字面量不必逐行重写,
// 通过 chat_svc.ConvertOldEventToNewForTest 桥接。
type scriptedRunner struct {
	events []agentruntime.RuntimeEvent
}

// Capabilities 返联合 meta(同 recordingRunner)—— scriptedRunner 也会被多个
// backend type 测试 swap 进来,统一给宽放白名单保证 chat_svc 校验放行。
func (scriptedRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		PermissionModeMeta: capability.PermissionModeMeta{
			AllowedModes:         []string{"default", "acceptEdits", "plan", "bypassPermissions"},
			DefaultMode:          "acceptEdits",
			SwitchableDuringTurn: true,
		},
	}
}

func (r scriptedRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	ch := make(chan agentruntime.Event, len(r.events))
	for _, e := range r.events {
		if ev := chat_svc.ConvertOldEventToNewForTest(e); ev != nil {
			ch <- ev
		}
	}
	close(ch)
	return ch, &agentruntime.RunResult{}, nil
}

type newEventRunner struct {
	events []agentruntime.Event
}

func (newEventRunner) Capabilities() capability.Capabilities {
	return scriptedRunner{}.Capabilities()
}

func (r newEventRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	ch := make(chan agentruntime.Event, len(r.events))
	for _, e := range r.events {
		ch <- e
	}
	close(ch)
	return ch, &agentruntime.RunResult{}, nil
}

type captureRunRequestRunner struct {
	events   []agentruntime.Event
	requests chan agentruntime.RunRequest
}

func (r captureRunRequestRunner) Capabilities() capability.Capabilities {
	return scriptedRunner{}.Capabilities()
}

func (r captureRunRequestRunner) Run(_ context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	ch := make(chan agentruntime.Event, len(r.events))
	for _, e := range r.events {
		ch <- e
	}
	close(ch)
	r.requests <- req
	return ch, &agentruntime.RunResult{}, nil
}

// captureSessionStatusPatches 抽出 emitter 收到的所有 StreamSessionStatus payload 序列。
// 单测靠它断言"等→应答"过程中 session 级 status 的翻转顺序。
func captureSessionStatusPatches(events []recorded) []chat_svc.ChatSessionStatusPatch {
	var out []chat_svc.ChatSessionStatusPatch
	for _, ev := range events {
		payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
		if !ok {
			continue
		}
		if payload.Kind == chat_svc.StreamSessionStatus && payload.SessionStatus != nil {
			out = append(out, *payload.SessionStatus)
		}
	}
	return out
}

// standardSendMocks 把 Send 路径上必经的 Find / Create / Update mock 配好。
// turn 内 runtime 走 scriptedRunner，所以这里只关心 session/agent/backend/provider
// 元数据查询 + message create 两条（user + assistant）+ session update 系列。
//
// captured 是 session.Update 的最终落库参数序列：单测断言「最后一条 NeedsAttention=false」
// 用它。
func standardSendMocks(t *testing.T, m *chatMocks, sessionID, agentID, backendID int64, providerKey string) *[]chat_entity.Session {
	t.Helper()
	captured := standardSendMocksWithoutMessageUpdate(t, m, sessionID, agentID, backendID, providerKey)
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	// 轮内 checkpoint 已从 Update 改走 CheckpointBlocks(整表替换 → 差分写,见
	// chat_repo.syncBlocks)。这条兜底许可同样要覆盖它,否则任何跑到 tool_result
	// 的用例都会撞上「未预期的调用」。想观察 checkpoint 内容的用例自己再挂捕获。
	m.message.EXPECT().CheckpointBlocks(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil).AnyTimes()
	return captured
}

func standardSendMocksWithoutMessageUpdate(t *testing.T, m *chatMocks, sessionID, agentID, backendID int64, providerKey string) *[]chat_entity.Session {
	t.Helper()
	m.session.EXPECT().Find(gomock.Any(), sessionID).Return(&chat_entity.Session{
		ID: sessionID, AgentID: agentID, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), agentID).Return(&agent_entity.Agent{
		ID: agentID, Name: "Eng", AgentBackendID: backendID, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), backendID).Return(&agent_backend_entity.AgentBackend{
		ID: backendID, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: providerKey, Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), providerKey).Return(newActiveProvider(providerKey, string(llm_provider_entity.TypeAnthropic)), nil).AnyTimes()
	expectProviderResolvable(m, providerKey)

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), sessionID).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	m.message.EXPECT().List(gomock.Any(), sessionID).Return(nil, nil).AnyTimes()

	captured := make([]chat_entity.Session, 0, 4)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			captured = append(captured, *s)
			return nil
		}).AnyTimes()
	return &captured
}

func TestSend_ErrorSessionMarksRunningAtTurnStart(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, scriptedRunner{
		events: []agentruntime.RuntimeEvent{{Kind: agentruntime.EventDone}},
	})
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "error", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	captured := make([]chat_entity.Session, 0, 2)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			captured = append(captured, *s)
			return nil
		}).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	require.NotEmpty(t, captured, "startTurn must persist a running snapshot before the async turn")
	assert.Equal(t, "running", captured[0].AgentStatus)
	assert.False(t, captured[0].NeedsAttention)
	assert.Equal(t, "idle", captured[len(captured)-1].AgentStatus)
}

// 回归(dev sess-21 卡 running):Send 不得在 startTurn 事务外预写 agent_status=running ——
// 事务持久化失败(如 SQLITE_BUSY)时无人回滚,DB 永久卡 running 且 quit 被 block。
// running 只能随 startTurn 事务内的 session Update 原子落库,事务失败即不残留,
// 故本测试不设任何 session.Update 期望:出现调用即违规。
func TestSend_PersistFailureDoesNotPersistRunning(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(0, errors.New("database is locked"))
	m.dbMock.ExpectRollback()

	_, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	require.Error(t, err, "事务持久化失败必须把错误返回给调用方")
}

// 回归(同上):新建会话必须以 idle Create;running 由 startTurn 事务内 Update 原子翻转。
// 否则 Create(running) 后事务失败,留下一条永久 running 的空会话。
func TestSend_NewSessionCreatesIdleNotRunning(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx

	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")

	var created chat_entity.Session
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			s.ID = 100
			created = *s
			return nil
		})

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(0, errors.New("database is locked"))
	m.dbMock.ExpectRollback()

	_, err := m.svc.Send(ctx, &chat_svc.SendRequest{AgentID: 7, Text: "hi"})
	require.Error(t, err)
	assert.Equal(t, "idle", created.AgentStatus,
		"新建会话以 idle 落库,事务失败时不残留 running")
}

// 回归(同上):Regenerate 与 Send 同病 —— 事务外预写 running。
func TestRegenerate_PersistFailureDoesNotPersistRunning(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, scriptedRunner{
		events: []agentruntime.RuntimeEvent{{Kind: agentruntime.EventDone}},
	})
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(&chat_entity.Message{
		ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1"),
	}, nil)
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
		{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("hi")},
		{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1")},
	}, nil).AnyTimes()
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")

	m.dbMock.ExpectBegin()
	m.message.EXPECT().DeleteFromSeq(gomock.Any(), int64(100), 1).
		Return(int64(0), errors.New("database is locked"))
	m.dbMock.ExpectRollback()

	_, err := m.svc.Regenerate(ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
	require.Error(t, err)
}

// TestSend_AskUserQuestionFlipsSessionToWaiting:
//   - 收到 EventAskUserQuestion 应 emit StreamSessionStatus{agentStatus=waiting, needsAttention=true}
//   - 收到 EventAskUserQuestionAnswered 应 emit StreamSessionStatus{agentStatus=running, needsAttention=false}
//   - turn 收尾后落库并 emit StreamSessionStatus{agentStatus=idle, needsAttention=false}
func TestSend_AskUserQuestionFlipsSessionToWaiting(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, scriptedRunner{events: []agentruntime.RuntimeEvent{
		{Kind: agentruntime.EventTextDelta, Text: "thinking..."},
		{Kind: agentruntime.EventAskUserQuestion, AskUserQuestion: &agentruntime.AskUserQuestionEvent{
			RequestID: "req-1",
			Questions: []agentruntime.AskQuestion{{
				Question: "Pick one",
				Options:  []agentruntime.AskOption{{Label: "A"}, {Label: "B"}},
			}},
		}},
		{Kind: agentruntime.EventAskUserQuestionAnswered, AskUserQuestion: &agentruntime.AskUserQuestionEvent{
			RequestID: "req-1",
			Answered:  true,
			Answers:   []agentruntime.AskAnswer{{QuestionIndex: 0, Labels: []string{"A"}}},
		}},
		{Kind: agentruntime.EventDone},
	}})
	t.Cleanup(restore)

	captured := standardSendMocks(t, m, 100, 7, 12, "key-21")

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	patches := captureSessionStatusPatches(m.events)
	require.Len(t, patches, 3, "AskUserQuestion + Answered + turn 收尾各 emit 一帧 StreamSessionStatus")
	assert.Equal(t, "waiting", patches[0].AgentStatus)
	assert.True(t, patches[0].NeedsAttention)
	assert.Equal(t, "running", patches[1].AgentStatus)
	assert.False(t, patches[1].NeedsAttention)
	assert.Equal(t, "idle", patches[2].AgentStatus)
	assert.False(t, patches[2].NeedsAttention)

	require.NotEmpty(t, *captured, "session 至少落库一次")
	final := (*captured)[len(*captured)-1]
	assert.Equal(t, "idle", final.AgentStatus, "turn 收尾应翻 idle")
	assert.False(t, final.NeedsAttention, "turn 收尾应清掉 NeedsAttention")
}

func TestSend_AskUserQuestionCheckpointsWaitingCard(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, scriptedRunner{events: []agentruntime.RuntimeEvent{
		{Kind: agentruntime.EventTextDelta, Text: "thinking..."},
		{Kind: agentruntime.EventAskUserQuestion, AskUserQuestion: &agentruntime.AskUserQuestionEvent{
			RequestID: "req-1",
			Questions: []agentruntime.AskQuestion{{
				Question: "Pick one",
				Options:  []agentruntime.AskOption{{Label: "A"}, {Label: "B"}},
			}},
		}},
	}})
	t.Cleanup(restore)

	_ = standardSendMocksWithoutMessageUpdate(t, m, 104, 7, 12, "key-21")

	var updates []chat_entity.Message
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "assistant" {
				updates = append(updates, *msg)
			}
			return nil
		}).AnyTimes()
	// 轮内 checkpoint 已从 Update 改走 CheckpointBlocks(整表替换 → 差分写,见
	// chat_repo.syncBlocks);这条用例盯的正是 checkpoint 落了什么,两条路都要收。
	m.message.EXPECT().CheckpointBlocks(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message, _ string) error {
			if msg.Role == "assistant" {
				updates = append(updates, *msg)
			}
			return nil
		}).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 104, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	require.GreaterOrEqual(t, len(updates), 2, "waiting request must checkpoint before final update")
	checkpointBlocks, err := updates[0].GetBlocks()
	require.NoError(t, err)
	require.True(t, hasBlockTypeForTest(checkpointBlocks, "user_ask"), "checkpoint must include the actionable user_ask card")
}

func TestSend_ToolPermissionCheckpointsWaitingCard(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, scriptedRunner{events: []agentruntime.RuntimeEvent{
		{Kind: agentruntime.EventToolPermissionRequest, ToolPermission: &agentruntime.ToolPermissionEvent{
			RequestID: "perm-1",
			ToolName:  "Bash",
			Input:     []byte(`{"command":"ls"}`),
		}},
	}})
	t.Cleanup(restore)

	_ = standardSendMocksWithoutMessageUpdate(t, m, 105, 7, 12, "key-21")

	var updates []chat_entity.Message
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "assistant" {
				updates = append(updates, *msg)
			}
			return nil
		}).AnyTimes()
	// 轮内 checkpoint 已从 Update 改走 CheckpointBlocks(整表替换 → 差分写,见
	// chat_repo.syncBlocks);这条用例盯的正是 checkpoint 落了什么,两条路都要收。
	m.message.EXPECT().CheckpointBlocks(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message, _ string) error {
			if msg.Role == "assistant" {
				updates = append(updates, *msg)
			}
			return nil
		}).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 105, AgentID: 7, Text: "run ls"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	require.GreaterOrEqual(t, len(updates), 2, "tool permission request must checkpoint before final update")
	checkpointBlocks, err := updates[0].GetBlocks()
	require.NoError(t, err)
	require.True(t, hasBlockTypeForTest(checkpointBlocks, "tool_permission"), "checkpoint must include the actionable tool_permission card")
}

// TestSend_OrphanToolResultFromAskUserQuestionIsDropped:
//
//	复现 AskUserQuestion 答完后前端冒出无主 tool 条的根因 ——
//	  - runtime 翻译层（translateClaudeCodeEvent）对 EventPreToolUse 用 Tool.Name
//	    过滤掉了 AskUserQuestion 的 tool_use；
//	  - 但 pkg/claudecode/session.go 的 parseUserContent 给 EventPostToolUse 只填
//	    了 ID + Response、没填 Name；EventPostToolUse 的同名过滤拿不到 Name 就漏过；
//	  - 结果就是 chat_svc 会收到一条 ToolCallID 但 acc 里没有对应 tool_use 的孤儿
//	    EventToolResult，再继续 emit StreamToolResult，前端用默认 toolName="tool"
//	    渲染出幽灵卡，DB 里也会留下无主 ToolResultBlock。
//
//	这里模拟孤儿 EventToolResult 流过 chat_svc 事件循环，断言：
//	  1. emitter 上不会出现 StreamToolResult；
func TestSend_OrphanToolResultFromAskUserQuestionIsDropped(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx

	askToolID := "toolu_ask_001"
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, scriptedRunner{events: []agentruntime.RuntimeEvent{
		{Kind: agentruntime.EventAskUserQuestion, AskUserQuestion: &agentruntime.AskUserQuestionEvent{
			RequestID: "req-orphan",
			Questions: []agentruntime.AskQuestion{{
				Question: "Pick one",
				Options:  []agentruntime.AskOption{{Label: "A"}, {Label: "B"}},
			}},
		}},
		{Kind: agentruntime.EventAskUserQuestionAnswered, AskUserQuestion: &agentruntime.AskUserQuestionEvent{
			RequestID: "req-orphan",
			Answered:  true,
			Answers:   []agentruntime.AskAnswer{{QuestionIndex: 0, Labels: []string{"A"}}},
		}},
		// translateClaudeCodeEvent 已经 drop 掉对应的 EventToolUseStart，
		// 但 EventToolResult 因 Name 为空逃过过滤漏到这里。
		{Kind: agentruntime.EventToolResult, ToolResult: &agentruntime.ToolResultEvent{
			ToolCallID: askToolID,
			Content:    `[{"label":"A"}]`,
		}},
		{Kind: agentruntime.EventDone},
	}})
	t.Cleanup(restore)

	_ = standardSendMocks(t, m, 102, 7, 12, "key-21")

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 102, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	for _, ev := range m.events {
		payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
		if !ok {
			continue
		}
		if payload.Kind == chat_svc.StreamToolResult {
			t.Errorf("孤儿 tool_result 不应被 emit；payload.ToolCallID=%q toolResult=%q",
				payload.ToolCallID, payload.ToolResult)
		}
	}
}

func TestSend_CheckpointsAssistantWhenToolResultArrives(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, scriptedRunner{events: []agentruntime.RuntimeEvent{
		{Kind: agentruntime.EventTextDelta, Text: "checking "},
		{Kind: agentruntime.EventToolUseStart, ToolUse: &agentruntime.ToolUseEvent{
			ID:    "toolu_1",
			Name:  "Bash",
			Input: []byte(`{"command":"pwd"}`),
		}},
		{Kind: agentruntime.EventToolResult, ToolResult: &agentruntime.ToolResultEvent{
			ToolCallID: "toolu_1",
			Content:    "/tmp/project",
		}},
		{Kind: agentruntime.EventTextDelta, Text: "done"},
		{Kind: agentruntime.EventDone},
	}})
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(103)).Return(&chat_entity.Session{
		ID: 103, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(103)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	m.message.EXPECT().List(gomock.Any(), int64(103)).Return(nil, nil).AnyTimes()
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	var updates []chat_entity.Message
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			updates = append(updates, *msg)
			return nil
		}).AnyTimes()
	// 轮内 checkpoint 已从 Update 改走 CheckpointBlocks(整表替换 → 差分写,见
	// chat_repo.syncBlocks);这条用例盯的正是 checkpoint 落了什么,两条路都要收。
	m.message.EXPECT().CheckpointBlocks(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message, _ string) error {
			updates = append(updates, *msg)
			return nil
		}).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 103, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	require.GreaterOrEqual(t, len(updates), 2, "tool_result checkpoint + final update should both persist assistant")
	checkpointBlocks, err := updates[0].GetBlocks()
	require.NoError(t, err)
	require.Len(t, checkpointBlocks, 3)
	assert.Equal(t, "checking ", blockTextForTest(t, checkpointBlocks[0]))
	assert.Equal(t, "toolu_1", toolUseIDForTest(t, checkpointBlocks[1]))
	assert.Equal(t, "toolu_1", toolResultIDForTest(t, checkpointBlocks[2]))

	finalBlocks, err := updates[len(updates)-1].GetBlocks()
	require.NoError(t, err)
	require.Len(t, finalBlocks, 4)
	assert.Equal(t, "done", blockTextForTest(t, finalBlocks[3]))
}

func blockTextForTest(t *testing.T, b blocks.ContentBlock) string {
	t.Helper()
	switch tb := b.(type) {
	case blocks.TextBlock:
		return tb.Text
	case *blocks.TextBlock:
		return tb.Text
	default:
		t.Fatalf("expected text block, got %T", b)
		return ""
	}
}

func hasBlockTypeForTest(bs []blocks.ContentBlock, typ string) bool {
	for _, b := range bs {
		if b != nil && b.Type() == typ {
			return true
		}
	}
	return false
}

func toolUseIDForTest(t *testing.T, b blocks.ContentBlock) string {
	t.Helper()
	switch tb := b.(type) {
	case blocks.ToolUseBlock:
		return tb.ID
	case *blocks.ToolUseBlock:
		return tb.ID
	default:
		t.Fatalf("expected tool use block, got %T", b)
		return ""
	}
}

func toolResultIDForTest(t *testing.T, b blocks.ContentBlock) string {
	t.Helper()
	switch tb := b.(type) {
	case blocks.ToolResultBlock:
		return tb.ToolUseID
	case *blocks.ToolResultBlock:
		return tb.ToolUseID
	default:
		t.Fatalf("expected tool result block, got %T", b)
		return ""
	}
}

// TestSend_ToolPermissionFlipsSessionToWaiting: ToolPermissionRequest / Resolved
// 对称走 ask 一样的 waiting → running → idle 翻转。
func TestSend_ToolPermissionFlipsSessionToWaiting(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, scriptedRunner{events: []agentruntime.RuntimeEvent{
		{Kind: agentruntime.EventToolPermissionRequest, ToolPermission: &agentruntime.ToolPermissionEvent{
			RequestID: "perm-1",
			ToolName:  "Bash",
			Input:     []byte(`{"command":"ls"}`),
		}},
		{Kind: agentruntime.EventToolPermissionResolved, ToolPermission: &agentruntime.ToolPermissionEvent{
			RequestID: "perm-1",
			ToolName:  "Bash",
			Input:     []byte(`{"command":"ls"}`),
			Resolved:  true,
			Allowed:   true,
		}},
		{Kind: agentruntime.EventDone},
	}})
	t.Cleanup(restore)

	captured := standardSendMocks(t, m, 101, 7, 12, "key-21")

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 101, AgentID: 7, Text: "run ls"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	patches := captureSessionStatusPatches(m.events)
	require.Len(t, patches, 3, "ToolPermission Request + Resolved + turn 收尾各 emit 一帧 StreamSessionStatus")
	assert.Equal(t, "waiting", patches[0].AgentStatus)
	assert.True(t, patches[0].NeedsAttention)
	assert.Equal(t, "running", patches[1].AgentStatus)
	assert.False(t, patches[1].NeedsAttention)
	assert.Equal(t, "idle", patches[2].AgentStatus)
	assert.False(t, patches[2].NeedsAttention)

	require.NotEmpty(t, *captured)
	final := (*captured)[len(*captured)-1]
	assert.Equal(t, "idle", final.AgentStatus)
	assert.False(t, final.NeedsAttention)
}

func TestSend_SteerConsumedSplitsMessages(t *testing.T) {
	// Given 当前 assistant 已经流出部分内容且用户排队了一条 follow-up
	// When runtime 报告这条 follow-up 已被消费
	// Then chat_svc 把当前 assistant 收口，插入正式 user 消息，并把后续流切到新的 assistant。
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, streamSteerConsumedRunner{})
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	createIDs := []int64{1000, 1001, 1002, 1003}
	createIdx := 0
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			msg.ID = createIDs[createIdx]
			createIdx++
			return nil
		}).Times(len(createIDs))

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.dbMock.ExpectCommit()

	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil)
	m.dbMock.ExpectCommit()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	assert.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	var consumed *chat_svc.ChatStreamEvent
	var done *chat_svc.ChatStreamEvent
	for _, ev := range m.events {
		payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
		if !ok {
			continue
		}
		switch payload.Kind {
		case chat_svc.StreamSteerConsumed:
			cp := payload
			consumed = &cp
		case chat_svc.StreamDone:
			cp := payload
			done = &cp
		}
	}

	if assert.NotNil(t, consumed) {
		assert.Equal(t, []string{"qid-1"}, consumed.QueuedIDs)
		if assert.NotNil(t, consumed.PreviousAssistantMessage) {
			assert.Equal(t, int64(1001), consumed.PreviousAssistantMessage.ID)
			assert.Equal(t, "before ", consumed.PreviousAssistantMessage.Blocks[0].Text)
		}
		if assert.Len(t, consumed.UserMessages, 1) {
			assert.Equal(t, int64(1002), consumed.UserMessages[0].ID)
			assert.Equal(t, "follow-up", consumed.UserMessages[0].Blocks[0].Text)
			// R17: 他端消息的来源标识随 UserMessages 带出(本机为空)。
			assert.Equal(t, "sha256:remote-peer", consumed.UserMessages[0].SourceDevice)
			assert.Equal(t, "iPhone", consumed.UserMessages[0].SourceDeviceName)
		}
		if assert.NotNil(t, consumed.AssistantMessage) {
			assert.Equal(t, int64(1003), consumed.AssistantMessage.ID)
			assert.Equal(t, 4, consumed.AssistantMessage.Seq)
		}
	}
	if assert.NotNil(t, done) && assert.NotNil(t, done.Message) {
		assert.Equal(t, int64(1003), done.Message.ID)
		assert.Equal(t, "after", done.Message.Blocks[0].Text)
	}
}

// autoContinueRunner 实现 BackendRunner + SteerDrainer：每次 Run 返一段简单
// 文本；第一次 DrainPending 返非空（模拟 turn 进行中又排了消息没被 hook 拉走），
// 之后返 nil。用来验证 chat_svc.runTurn 收尾时会取走残留并起新一轮。
type autoContinueRunner struct {
	mu             sync.Mutex
	runs           int
	pendingByRun   [][]agentruntime.ConsumedSteer
	drainCallIndex int
}

func (*autoContinueRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{}
}
func (r *autoContinueRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	r.mu.Lock()
	r.runs++
	idx := r.runs
	r.mu.Unlock()
	events := make(chan agentruntime.Event, 1)
	events <- agentruntime.TextDelta{Text: fmt.Sprintf("turn-%d", idx)}
	close(events)
	return events, &agentruntime.RunResult{ProviderSessionID: "auto-sid"}, nil
}

func (r *autoContinueRunner) Steer(_ context.Context, _ int64, _ string, _ string) error {
	return nil
}

func (r *autoContinueRunner) DrainPending(_ context.Context, _ int64) []agentruntime.ConsumedSteer {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.drainCallIndex >= len(r.pendingByRun) {
		return nil
	}
	out := r.pendingByRun[r.drainCallIndex]
	r.drainCallIndex++
	return out
}

func (r *autoContinueRunner) runCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runs
}

// TestSend_AutoContinuesWhenSteerInboxNonEmpty 验证 turn 自然结束后，如果 runner
// 的 SteerInbox 还有未消费的排队消息（PostToolUse hook 没拉走），chat_svc 会自动
// 合并成一段 user msg 起新一轮 —— 替代旧的 Stop hook block=continue 把戏。
func TestSend_AutoContinuesWhenSteerInboxNonEmpty(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx

	runner := &autoContinueRunner{
		pendingByRun: [][]agentruntime.ConsumedSteer{
			{
				{QueuedID: "qid-a", Text: "follow-up-1"},
				{QueuedID: "qid-b", Text: "follow-up-2"},
			},
			nil, // 第二轮收尾 drain 返空 → 不再续
		},
	}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	// 4 个 Create：first turn user/assistant + auto-continue user/assistant。
	createIDs := []int64{1000, 1001, 1002, 1003}
	createdMessages := make([]*chat_entity.Message, 0, 4)
	createIdx := 0
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			msg.ID = createIDs[createIdx]
			createIdx++
			cloned := *msg
			createdMessages = append(createdMessages, &cloned)
			return nil
		}).Times(len(createIDs))

	// 第一轮 startTurn 事务 + 第二轮 auto-continue 事务。
	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.dbMock.ExpectCommit()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil)
	m.dbMock.ExpectCommit()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	assert.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	// 两个 turn 都跑了
	assert.Equal(t, 2, runner.runCount(), "runner should be Run twice (initial + auto-continue)")

	// 合并文本作为第二轮 user msg：默认用 \n\n 拼接
	var newUser *chat_entity.Message
	for _, m := range createdMessages {
		if m.ID == 1002 {
			cm := m
			newUser = cm
			break
		}
	}
	if assert.NotNil(t, newUser, "auto-continue should create the merged user msg id=1002") {
		assert.Equal(t, "user", newUser.Role)
		assert.Contains(t, newUser.BlocksJSON, "follow-up-1")
		assert.Contains(t, newUser.BlocksJSON, "follow-up-2")
	}

	// 事件流：StreamSteerConsumed 应至少 emit 一次（带 QueuedIDs），StreamDone 也要有
	var consumed *chat_svc.ChatStreamEvent
	doneCount := 0
	for _, ev := range m.events {
		payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
		if !ok {
			continue
		}
		switch payload.Kind {
		case chat_svc.StreamSteerConsumed:
			cp := payload
			consumed = &cp
		case chat_svc.StreamDone:
			doneCount++
		}
	}
	if assert.NotNil(t, consumed, "expected StreamSteerConsumed event for auto-continue") {
		assert.ElementsMatch(t, []string{"qid-a", "qid-b"}, consumed.QueuedIDs)
		if assert.NotNil(t, consumed.PreviousAssistantMessage) {
			assert.Equal(t, int64(1001), consumed.PreviousAssistantMessage.ID)
		}
		if assert.Len(t, consumed.UserMessages, 1) {
			assert.Equal(t, int64(1002), consumed.UserMessages[0].ID)
		}
		if assert.NotNil(t, consumed.AssistantMessage) {
			assert.Equal(t, int64(1003), consumed.AssistantMessage.ID)
		}
	}
	assert.GreaterOrEqual(t, doneCount, 1, "should emit StreamDone after final turn closes")
}

// TestSend_AutoContinuesMultipleLevels 验证 auto-continue 能链式接续多轮：
// turn1 结束 → 残留 [A,B] → turn2 → 残留 [C] → turn3 → 空 → 收尾。每一层都
// 必须落 user+assistant 行 + emit StreamSteerConsumed。
func TestSend_AutoContinuesMultipleLevels(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx

	runner := &autoContinueRunner{
		pendingByRun: [][]agentruntime.ConsumedSteer{
			{{QueuedID: "qid-a", Text: "A", SourcePeer: "sha256:other-device", SourceName: "iPad"}, {QueuedID: "qid-b", Text: "B"}}, // 第 1 轮后
			{{QueuedID: "qid-c", Text: "C"}}, // 第 2 轮后
			nil,                              // 第 3 轮后收尾
		},
	}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	// 6 个 Create：3 轮 × (user+assistant)。
	createIDs := []int64{1000, 1001, 1002, 1003, 1004, 1005}
	createdMessages := make([]*chat_entity.Message, 0, 6)
	createIdx := 0
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			msg.ID = createIDs[createIdx]
			createIdx++
			cloned := *msg
			createdMessages = append(createdMessages, &cloned)
			return nil
		}).Times(len(createIDs))

	// 3 段事务：初始 startTurn + 2 次 auto-continue。
	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.dbMock.ExpectCommit()
	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil)
	m.dbMock.ExpectCommit()
	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(5, nil)
	m.dbMock.ExpectCommit()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	assert.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	assert.Equal(t, 3, runner.runCount(), "runner should be Run 3 times across the chain")

	// 第三轮的 user msg（id=1004）应当只包含 "C"，因为 [A,B] 已经在第二轮里消费过了。
	var thirdUser *chat_entity.Message
	for _, msg := range createdMessages {
		if msg.ID == 1004 {
			cm := msg
			thirdUser = cm
			break
		}
	}
	if assert.NotNil(t, thirdUser, "third turn should create the user msg id=1004") {
		assert.Equal(t, "user", thirdUser.Role)
		assert.Contains(t, thirdUser.BlocksJSON, `"C"`)
		assert.NotContains(t, thirdUser.BlocksJSON, `"A"`)
		assert.NotContains(t, thirdUser.BlocksJSON, `"B"`)
	}

	// 事件流：应当有 2 个 StreamSteerConsumed（一次链转移一个）+ 至少 1 个 StreamDone。
	consumedQueuedIDs := make([][]string, 0, 2)
	doneCount := 0
	for _, ev := range m.events {
		payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
		if !ok {
			continue
		}
		switch payload.Kind {
		case chat_svc.StreamSteerConsumed:
			consumedQueuedIDs = append(consumedQueuedIDs, payload.QueuedIDs)
		case chat_svc.StreamDone:
			doneCount++
		}
	}
	if assert.Len(t, consumedQueuedIDs, 2, "expect StreamSteerConsumed twice for two transitions") {
		assert.ElementsMatch(t, []string{"qid-a", "qid-b"}, consumedQueuedIDs[0])
		assert.ElementsMatch(t, []string{"qid-c"}, consumedQueuedIDs[1])
	}

	// R17: 合并出的 auto-continue user 消息带提交方来源(取第一条非空;本地/未知为空)。
	var autoUserMsg *chat_svc.ChatMessage
	for _, ev := range m.events {
		payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
		if !ok || payload.Kind != chat_svc.StreamSteerConsumed {
			continue
		}
		if len(payload.UserMessages) > 0 {
			um := payload.UserMessages[0]
			autoUserMsg = &um
			break
		}
	}
	if assert.NotNil(t, autoUserMsg, "auto-continue StreamSteerConsumed must carry a user message") {
		assert.Equal(t, "sha256:other-device", autoUserMsg.SourceDevice)
		assert.Equal(t, "iPad", autoUserMsg.SourceDeviceName)
	}
	assert.GreaterOrEqual(t, doneCount, 1, "final turn must emit StreamDone")
}

// TestSend_AutoContinuePersistFailure_FinalAgentStatusLogMatchesPersistedStatus
// 盯的是排障口径:自动接续落库失败后,恢复代码把 agent_status 从「仍 running 的中间态」
// 拍回 idle 并落库,却没有再打一条 "agent_status finalized"。日志里这条会话的最后一条
// 终态记录因此写着 running,与库里的 idle 相反 —— 排「会话卡 running」时按日志对时间线
// 会得出与事实相反的结论。
func TestSend_AutoContinuePersistFailure_FinalAgentStatusLogMatchesPersistedStatus(t *testing.T) {
	m := setupChatTest(t)

	core, logs := observer.New(zapcore.DebugLevel)
	capturingLogger := zap.New(core)
	oldLogger := logger.Default()
	logger.SetLogger(capturingLogger)
	t.Cleanup(func() { logger.SetLogger(oldLogger) })
	ctx := logger.WithContextLogger(m.ctx, capturingLogger)

	runner := &autoContinueRunner{
		pendingByRun: [][]agentruntime.ConsumedSteer{
			{{QueuedID: "qid-a", Text: "follow-up"}},
		},
	}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")

	var statusMu sync.Mutex
	var persistedStatuses []string
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, sess *chat_entity.Session) error {
			statusMu.Lock()
			persistedStatuses = append(persistedStatuses, sess.AgentStatus)
			statusMu.Unlock()
			return nil
		}).AnyTimes()
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	createIdx := int64(1000)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			msg.ID = createIdx
			createIdx++
			return nil
		}).AnyTimes()

	// 第一轮 startTurn 事务。
	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.dbMock.ExpectCommit()

	// 自动接续那笔事务:NextSeq 失败 → persistAutoContinueTurn 返错,走「pending 已被
	// drain 走无法回滚,只能丢」的恢复分支。
	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(0, errors.New("SENTINEL_AUTO_CONTINUE_SEQ"))
	m.dbMock.ExpectRollback()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	require.Equal(t, 1, runner.runCount(), "落库失败必须止步,不递归跑新一轮")

	statusMu.Lock()
	require.NotEmpty(t, persistedStatuses)
	lastPersisted := persistedStatuses[len(persistedStatuses)-1]
	statusMu.Unlock()
	require.Equal(t, "idle", lastPersisted, "恢复分支把会话拍回 idle 并落库")

	entries := logs.FilterMessage("chat_svc: agent_status finalized").All()
	require.NotEmpty(t, entries, "终态必须留下可对时间线的日志")
	last := entries[len(entries)-1]
	assert.Equal(t, lastPersisted, last.ContextMap()["agentStatus"],
		"这条会话最后一条 agent_status finalized 必须与落库的最终状态一致，否则排障按日志会看反")
}

func TestSend_Errors(t *testing.T) {
	convey.Convey("Send 错误路径", t, func() {

		convey.Convey("Agent 后端类型未知 → AgentBackendInvalidType", func() {
			m := setupChatTest(t)
			ctx := context.Background()
			m.agent.EXPECT().Find(ctx, int64(7)).Return(&agent_entity.Agent{
				ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: "definitely-not-a-real-type", Status: consts.ACTIVE,
			}, nil)
			_, err := m.svc.Send(ctx, &chat_svc.SendRequest{AgentID: 7, Text: "hi"})
			assert.Error(t, err)
		})

		convey.Convey("claudecode + provider 但 gateway 未起 → ChatBackendGatewayUnavailable", func() {
			m := setupChatTest(t)
			ctx := context.Background()
			m.agent.EXPECT().Find(ctx, int64(7)).Return(&agent_entity.Agent{
				ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: "claudecode", LLMProviderKey: "key-21", Status: consts.ACTIVE,
			}, nil)
			m.provider.EXPECT().FindByKey(ctx, "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
			expectProviderResolvable(m, "key-21")
			// chat_svc 默认无 gateway → 当 LLMProviderKey != "" 时按 unavailable 拒掉。
			_, err := m.svc.Send(ctx, &chat_svc.SendRequest{AgentID: 7, Text: "hi"})
			assert.Error(t, err)
		})

		convey.Convey("远端 claudecode + provider 缓存明确缺 key → 发送前返回可操作错误", func() {
			m := setupChatTest(t)
			ctx := context.Background()
			chat_svc.RegisterGateway(&fakeChatGateway{
				status: httpgateway.GatewayStatus{State: "running", URL: "http://127.0.0.1:60080"},
			})
			t.Cleanup(func() { chat_svc.RegisterGateway(nil) })

			ctrl := gomock.NewController(t)
			t.Cleanup(ctrl.Finish)
			mockRDS := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
			mockRDS.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()
			mockRDS.EXPECT().List(gomock.Any()).Return([]*remote_device_svc.DeviceView{
				{ID: 42, DaemonFingerprint: "sha256:device-42", Online: true},
			}, nil).AnyTimes()
			remote_device_svc.SetDefault(mockRDS)
			t.Cleanup(func() { remote_device_svc.SetDefault(nil) })

			m.agent.EXPECT().Find(ctx, int64(7)).Return(&agent_entity.Agent{
				ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: "claudecode", LLMProviderKey: "missing-key", DeviceFingerprint: "sha256:device-42", Status: consts.ACTIVE,
			}, nil)
			m.provider.EXPECT().FindByKey(ctx, "missing-key").Return(&llm_provider_entity.LLMProvider{ProviderKey: "missing-key", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-missing-key", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
			expectProviderResolvable(m, "missing-key")
			mockRDS.EXPECT().ListDeviceProviders(int64(42)).Return([]remote_device_svc.ProviderSummary{
				{Key: "other-key", Name: "Other", Type: "anthropic"},
			})

			_, err := m.svc.Send(ctx, &chat_svc.SendRequest{AgentID: 7, Text: "hi"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "远端 agentred 未配置")
			assert.Contains(t, err.Error(), "missing-key")
		})

		// 回归:远端 claudecode dial 失败时,runTurn 必须把 selectRunner 返回的真错
		// (RemoteRunnerDialFailed) 透传给 failTurn,而不是覆写成假的 "unsupported
		// backend type: claudecode" —— claudecode runtime 早就注册了,真错是 dial。
		convey.Convey("远端 claudecode + Pool.Borrow 失败 → ErrorText 透传真错而非 'unsupported backend type'", func() {
			m := setupChatTest(t)
			ctx := m.ctx

			ctrl := gomock.NewController(t)
			t.Cleanup(ctrl.Finish)
			mockPool := mock_remote_device_svc.NewMockConnPool(ctrl)
			mockPool.EXPECT().Borrow(gomock.Any(), int64(42)).
				Return(nil, errors.New("dial timeout")).AnyTimes()
			chat_svc.SetConnPoolForTest(m.svc, mockPool)
			t.Cleanup(func() { chat_svc.SetConnPoolForTest(m.svc, nil) })

			m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
				ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
				ID: 7, AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
			}, nil)
			// LLMProviderKey="" → 不走 gateway 校验;DeviceID="42" → 走远端 borrow 路径。
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: "claudecode", DeviceFingerprint: "sha256:device-42", Status: consts.ACTIVE,
			}, nil)

			m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

			m.dbMock.ExpectBegin()
			m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
			m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
					if msg.Role == "user" {
						msg.ID = 1000
					} else {
						msg.ID = 1001
					}
					return nil
				}).Times(2)
			m.dbMock.ExpectCommit()

			var capturedErrText string
			var mu sync.Mutex
			m.message.EXPECT().Update(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
					if msg.ErrorText != "" {
						mu.Lock()
						capturedErrText = msg.ErrorText
						mu.Unlock()
					}
					return nil
				}).AnyTimes()

			resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
			assert.NoError(t, err)

			chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

			mu.Lock()
			got := capturedErrText
			mu.Unlock()
			assert.NotEmpty(t, got, "failTurn 应给 message 写入 ErrorText")
			assert.NotContains(t, got, "unsupported backend type",
				"claudecode runtime 已注册;真错被 chat.go:1561 fmt.Errorf 覆写")
			assert.Contains(t, got, "无法连接到远端 agentred",
				"真错应是 RemoteRunnerDialFailed,需要透传给前端方便排查")
		})

		convey.Convey("空文本 → InvalidParameter", func() {
			m := setupChatTest(t)
			ctx := context.Background()
			_, err := m.svc.Send(ctx, &chat_svc.SendRequest{AgentID: 7, Text: "   "})
			assert.Error(t, err)
		})

		convey.Convey("文本过长 → ChatTextTooLong", func() {
			m := setupChatTest(t)
			ctx := context.Background()
			_, err := m.svc.Send(ctx, &chat_svc.SendRequest{AgentID: 7, Text: strings.Repeat("x", chat_entity.MessageTextMaxBytes+1)})
			assert.Error(t, err)
		})

		convey.Convey("同一 session 第二次 Send 抢锁失败 → ChatSendInFlight", func() {
			m := setupChatTest(t)
			ctx := m.ctx // must carry DB handle for Transaction

			// providerCalled is closed once the background goroutine has read
			// providerBuilder (i.e. entered runTurn and called providerBuilder(prov)).
			// We wait on it before the test returns so that the t.Cleanup that resets
			// providerBuilder doesn't race with the goroutine's read.
			providerCalled := make(chan struct{})

			releaseStream := make(chan struct{})
			// controlled stream keeps the first turn "in flight" until this test
			// has asserted the second Send failure, then closes so the goroutine
			// cannot leak into following tests that replace the package-level repos.
			fp := providertest.New().QueueStreamFunc(func(pCtx context.Context) <-chan provider.StreamChunk {
				ch := make(chan provider.StreamChunk)
				go func() {
					select {
					case <-releaseStream:
					case <-pCtx.Done():
					}
					close(ch)
				}()
				return ch
			})
			chat_svc.SetProviderBuilderForTest(func(p *llm_provider_entity.LLMProvider) (provider.Provider, error) {
				// signal that providerBuilder has been called; safe to reset after this
				select {
				case <-providerCalled:
				default:
					close(providerCalled)
				}
				return fp, nil
			})
			t.Cleanup(chat_svc.ResetProviderBuilderForTest)

			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
				ID: 7, AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
			}, nil).AnyTimes()
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: "builtin", LLMProviderKey: "key-21", Status: consts.ACTIVE,
			}, nil).AnyTimes()
			m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
			expectProviderResolvable(m, "key-21")
			m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
				ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
			}, nil).AnyTimes()
			// Update outside tx (set running) — only first Send reaches this
			m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

			// DB transaction for the first Send only
			m.dbMock.ExpectBegin()
			m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
			m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
					if msg.Role == "user" {
						msg.ID = 1
					} else {
						msg.ID = 2
					}
					return nil
				}).Times(2)
			m.dbMock.ExpectCommit()

			// List is called inside runTurn (before stream blocks), so mock it
			m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
			m.message.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

			// First Send — acquires lock, spawns goroutine that blocks on stream
			resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
			assert.NoError(t, err)

			// Second Send — TryLock fails immediately → ChatSendInFlight
			_, err = m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
			assert.Error(t, err)

			// Wait until the background goroutine has called providerBuilder before
			// t.Cleanup resets it, preventing a data race on the package-level variable.
			select {
			case <-providerCalled:
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for providerBuilder to be called")
			}
			close(releaseStream)
			chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)
		})
	})
}

func TestRenameAndDelete(t *testing.T) {
	convey.Convey("Rename + Delete", t, func() {
		ctx := context.Background()

		convey.Convey("Rename 校验 title 长度", func() {
			m := setupChatTest(t)
			m.session.EXPECT().Find(ctx, int64(5)).Return(&chat_entity.Session{ID: 5, AgentID: 1, AgentStatus: "idle", Status: consts.ACTIVE}, nil)
			m.session.EXPECT().Update(ctx, gomock.Any()).Return(nil)
			_, err := m.svc.Rename(ctx, &chat_svc.RenameRequest{SessionID: 5, Title: "new title"})
			assert.NoError(t, err)
		})

		convey.Convey("Rename 找不到 session → ChatSessionNotFound", func() {
			m := setupChatTest(t)
			m.session.EXPECT().Find(ctx, int64(99)).Return(nil, nil)
			_, err := m.svc.Rename(ctx, &chat_svc.RenameRequest{SessionID: 99, Title: "x"})
			assert.Error(t, err)
		})

		convey.Convey("Delete 只软删 session，不清理 Agent cwd", func() {
			m := setupChatTest(t)
			m.session.EXPECT().SoftDelete(ctx, int64(5)).Return(nil)
			_, err := m.svc.Delete(ctx, &chat_svc.DeleteRequest{SessionID: 5})
			assert.NoError(t, err)
		})
	})
}

// encodeText helper: pack a text block into StoredBlock-envelope JSON for fixtures.
func encodeText(s string) string {
	m := &chat_entity.Message{}
	_ = m.SetBlocks([]blocks.ContentBlock{&blocks.TextBlock{Text: s}})
	return m.BlocksJSON
}

// ── Regenerate ──────────────────────────────────────────────────────────────

func TestRegenerate_BuiltinTruncatesAndRestartsTurn(t *testing.T) {
	convey.Convey("Regenerate(builtin) 截断后启新 turn", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
		t.Cleanup(restore)

		// session 已经有：seq1 user "hi", seq2 assistant "v1"
		// 用户在 seq2 上点重新生成 → 期望删 seq>=1，并以 "hi" 重新跑一轮。
		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
			ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
		}, nil)
		m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(&chat_entity.Message{
			ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1"),
		}, nil)
		m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
			{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("hi")},
			{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1")},
		}, nil).AnyTimes()
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
			ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
		}, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
		}, nil)
		m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
		expectProviderResolvable(m, "key-21")
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		// 事务里：先 DeleteFromSeq(100, 1) 干掉 user+assistant，再 NextSeq + Create×2。
		m.dbMock.ExpectBegin()
		m.message.EXPECT().DeleteFromSeq(gomock.Any(), int64(100), 1).Return(int64(2), nil)
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
		newIDs := []int64{2000, 2001}
		var calls int
		m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				msg.ID = newIDs[calls]
				calls++
				return nil
			}).Times(2)
		m.dbMock.ExpectCommit()

		m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		resp, err := m.svc.Regenerate(ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
		assert.NoError(t, err)
		assert.Equal(t, int64(100), resp.SessionID)
		assert.NotZero(t, resp.AssistantMessageID)

		select {
		case req := <-runner.requests:
			assert.Equal(t, "hi", req.UserText, "重新生成必须用原 user 消息的文本重发")
		case <-time.After(2 * time.Second):
			t.Fatal("runtime never received the regenerated turn")
		}
		chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)
	})
}

// 回归：第一次 Send 走完后 builtin runner 会把 RunResult.ProviderSessionID 落到
// session（值是 "builtin-<sid>"）。如果 Regenerate 把 HasProviderSession() 误当成
// 必须有 Rewinder 的硬条件，就会在第二轮起返回 ChatRegenerateUnsupported，
// 表现就是「按钮点了没反应」。builtin 每轮历史从 chat_messages 重建，DB 截断后
// 直接重跑 turn 即可，应当与首轮无差。
func TestRegenerate_BuiltinWithProviderSessionStillRestartsTurn(t *testing.T) {
	convey.Convey("Regenerate(builtin) 即便 session 已有 ProviderSessionID 也应放行", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
		t.Cleanup(restore)

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
			ID: 100, AgentID: 7, ProviderSessionID: "builtin-100",
			AgentStatus: "idle", Status: consts.ACTIVE,
		}, nil)
		m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(&chat_entity.Message{
			ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1"),
		}, nil)
		m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
			{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("hi")},
			{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1")},
		}, nil).AnyTimes()
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
			ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
		}, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
		}, nil)
		m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
		expectProviderResolvable(m, "key-21")
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		m.dbMock.ExpectBegin()
		m.message.EXPECT().DeleteFromSeq(gomock.Any(), int64(100), 1).Return(int64(2), nil)
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
		newIDs := []int64{2000, 2001}
		var calls int
		m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				msg.ID = newIDs[calls]
				calls++
				return nil
			}).Times(2)
		m.dbMock.ExpectCommit()

		m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		resp, err := m.svc.Regenerate(ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
		assert.NoError(t, err, "builtin 有 ProviderSessionID 不应当作未支持")
		assert.Equal(t, int64(100), resp.SessionID)
		assert.NotZero(t, resp.AssistantMessageID)

		select {
		case req := <-runner.requests:
			assert.Equal(t, "hi", req.UserText, "重新生成必须用原 user 消息的文本重发")
			assert.Equal(t, "builtin-100", req.ProviderSessionID, "原 builtin convID 透传，runner 自己决定是否复用")
		case <-time.After(2 * time.Second):
			t.Fatal("runtime never received the regenerated turn")
		}
		chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)
	})
}

func TestRegenerate_CodexRollsBackProviderTurns(t *testing.T) {
	convey.Convey("Regenerate(codex) 按目标 user 到末尾的 turn 数生成 rollback anchor", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, runner)
		t.Cleanup(restore)

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
			ID: 100, AgentID: 7, ProviderSessionID: "cx-abc", AgentStatus: "idle", Status: consts.ACTIVE,
		}, nil)
		m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(&chat_entity.Message{
			ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1"),
		}, nil)
		m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
			{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("first")},
			{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1")},
			{ID: 1002, SessionID: 100, Role: "user", Seq: 3, BlocksJSON: encodeText("second")},
			{ID: 1003, SessionID: 100, Role: "assistant", Seq: 4, BlocksJSON: encodeText("v2")},
		}, nil).AnyTimes()
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
			ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
		}, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeCodex), Status: consts.ACTIVE,
		}, nil)
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		m.dbMock.ExpectBegin()
		m.message.EXPECT().DeleteFromSeq(gomock.Any(), int64(100), 1).Return(int64(4), nil)
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
		newIDs := []int64{2000, 2001}
		var calls int
		m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				msg.ID = newIDs[calls]
				calls++
				return nil
			}).Times(2)
		m.dbMock.ExpectCommit()
		m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		resp, err := m.svc.Regenerate(ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
		assert.NoError(t, err)

		select {
		case req := <-runner.requests:
			assert.Equal(t, "first", req.UserText)
			assert.Equal(t, "cx-abc", req.ProviderSessionID)
			assert.Equal(t, "2", req.ForkAnchor, "目标 user 自身和后续第二轮都要从 Codex thread rollback")
		case <-time.After(2 * time.Second):
			t.Fatal("runtime never received the regenerated codex turn")
		}
		chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)
	})
}

func TestPiRestart_PassesExactStoredAnchorToRunner(t *testing.T) {
	tests := []struct {
		name      string
		arrange   func(*chatMocks)
		invoke    func(chat_svc.ChatSvc, context.Context) (*chat_svc.SendResponse, error)
		wantText  string
		anchorSeq int
	}{
		{
			name: "Given a Pi assistant reply with a stored user anchor, when Regenerate runs, then it restarts from that exact anchor",
			arrange: func(m *chatMocks) {
				m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(&chat_entity.Message{
					ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1"),
				}, nil)
				m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
					{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: "pi-user-entry-exact"},
					{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1")},
				}, nil).AnyTimes()
			},
			invoke: func(svc chat_svc.ChatSvc, ctx context.Context) (*chat_svc.SendResponse, error) {
				return svc.Regenerate(ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
			},
			wantText:  "original",
			anchorSeq: 1,
		},
		{
			name: "Given a Pi user message with a stored anchor, when Edit runs, then it sends the replacement from that exact anchor",
			arrange: func(m *chatMocks) {
				m.message.EXPECT().Find(gomock.Any(), int64(1000)).Return(&chat_entity.Message{
					ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"),
					ForkAnchor: "pi-user-entry-exact",
				}, nil)
				m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
					{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: "pi-user-entry-exact"},
					{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("answer")},
				}, nil).AnyTimes()
			},
			invoke: func(svc chat_svc.ChatSvc, ctx context.Context) (*chat_svc.SendResponse, error) {
				return svc.Edit(ctx, &chat_svc.EditRequest{SessionID: 100, MessageID: 1000, Text: "replacement"})
			},
			wantText:  "replacement",
			anchorSeq: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := setupChatTest(t)
			runner := &preparedRecordingPiRunner{
				requests:          make(chan agentruntime.RunRequest, 1),
				providerSessionID: "pi-session-new",
			}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
			t.Cleanup(restore)

			m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
				ID: 100, AgentID: 7, ProviderSessionID: "pi-session-old", AgentStatus: "idle", Status: consts.ACTIVE,
			}, nil)
			tc.arrange(m)
			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
				ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
			}, nil)
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
			}, nil)
			expectAcknowledgedPiReplacement(m, tc.anchorSeq, 2000, 2001)

			resp, err := tc.invoke(m.svc, m.ctx)
			require.NoError(t, err)
			require.NotNil(t, resp)

			select {
			case req := <-runner.requests:
				assert.Equal(t, tc.wantText, req.UserText)
				assert.Equal(t, "pi-session-old", req.ProviderSessionID)
				assert.Equal(t, "pi-user-entry-exact", req.ForkAnchor)
			case <-time.After(2 * time.Second):
				t.Fatal("Pi runtime never received the restarted turn")
			}
			chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)
		})
	}
}

func TestSend_PiUserAnchorPersistenceRetriesAndSurfacesPermanentFailure(t *testing.T) {
	tests := []struct {
		name           string
		updateFailures int
		wantDone       bool
		wantError      bool
	}{
		{
			name:           "Given the first user-anchor update fails, when Pi completes the turn, then persistence retries and the turn remains successful",
			updateFailures: 1,
			wantDone:       true,
		},
		{
			name:           "Given user-anchor updates keep failing, when Pi completes the turn, then the answer is preserved but the turn reports an error",
			updateFailures: 2,
			wantError:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := setupChatTest(t)
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, &anchorResultRunner{})
			t.Cleanup(restore)

			m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
				ID: 100, AgentID: 7, ProviderSessionID: "pi-session", AgentStatus: "idle", Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
				ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
			}, nil)
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
			}, nil)
			m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
			expectNoPiTranscriptRecovery(m, 100)

			var userMsg *chat_entity.Message
			m.dbMock.ExpectBegin()
			m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
			m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, msg *chat_entity.Message) error {
					if msg.Role == "user" {
						msg.ID = 2000
						userMsg = msg
					} else {
						msg.ID = 2001
					}
					return nil
				}).Times(2)
			m.dbMock.ExpectCommit()

			userUpdateCalls := 0
			m.message.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, msg *chat_entity.Message) error {
					if msg.Role == "user" {
						userUpdateCalls++
						if userUpdateCalls <= tc.updateFailures {
							return errors.New("anchor update failed")
						}
					}
					return nil
				}).AnyTimes()

			resp, err := m.svc.Send(m.ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hello"})
			require.NoError(t, err)
			chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

			require.NotNil(t, userMsg)
			assert.Equal(t, "pi-user-entry-new", userMsg.ForkAnchor)
			assert.Equal(t, 2, userUpdateCalls)
			var gotDone, gotError bool
			var errorMessage *chat_svc.ChatMessage
			for _, recorded := range m.events {
				event, ok := recorded.Payload.(chat_svc.ChatStreamEvent)
				if !ok {
					continue
				}
				switch event.Kind {
				case chat_svc.StreamDone:
					gotDone = true
				case chat_svc.StreamError:
					gotError = true
					errorMessage = event.Message
				}
			}
			assert.Equal(t, tc.wantDone, gotDone)
			assert.Equal(t, tc.wantError, gotError)
			if tc.wantError {
				require.NotNil(t, errorMessage)
				require.NotEmpty(t, errorMessage.Blocks)
				assert.Equal(t, "completed answer", errorMessage.Blocks[0].Text)
			}
		})
	}
}

func TestSend_NonPiUserAnchorPersistenceFailureKeepsCompletedTurn(t *testing.T) {
	for _, backendType := range []agent_backend_entity.BackendType{
		agent_backend_entity.TypeClaudeCode,
		agent_backend_entity.TypeCodex,
		agent_backend_entity.TypeBuiltin,
	} {
		t.Run(string(backendType), func(t *testing.T) {
			m := setupChatTest(t)
			restore := agentruntime.SwapRuntimeForTest(backendType, &anchorResultRunner{})
			t.Cleanup(restore)

			sess := &chat_entity.Session{
				ID: 100, AgentID: 7, ProviderSessionID: "existing-session", AgentStatus: "idle", Status: consts.ACTIVE,
			}
			backend := &agent_backend_entity.AgentBackend{
				ID: 12, Type: string(backendType), Status: consts.ACTIVE,
			}
			m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
				ID: 7, Name: "Agent", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
			}, nil)
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(backend, nil)
			if backendType == agent_backend_entity.TypeBuiltin {
				backend.LLMProviderKey = "provider-key"
				m.provider.EXPECT().FindByKey(gomock.Any(), "provider-key").Return(&llm_provider_entity.LLMProvider{ProviderKey: "provider-key", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-provider-key", ID: 11, Type: string(llm_provider_entity.TypeOpenAIChat), Status: consts.ACTIVE}, nil).AnyTimes()
				expectProviderResolvable(m, "provider-key")
				m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
			}
			m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

			var userMsg *chat_entity.Message
			m.dbMock.ExpectBegin()
			m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
			m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, msg *chat_entity.Message) error {
					if msg.Role == "user" {
						msg.ID = 2000
						userMsg = msg
					} else {
						msg.ID = 2001
					}
					return nil
				}).Times(2)
			m.dbMock.ExpectCommit()

			userUpdateCalls := 0
			m.message.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, msg *chat_entity.Message) error {
					if msg.Role == "user" {
						userUpdateCalls++
						return errors.New("best-effort anchor update failed")
					}
					return nil
				}).AnyTimes()

			resp, err := m.svc.Send(m.ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hello"})
			require.NoError(t, err)
			chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

			require.NotNil(t, userMsg)
			assert.Equal(t, "pi-user-entry-new", userMsg.ForkAnchor)
			assert.Equal(t, 1, userUpdateCalls, "non-Pi anchor persistence remains one best-effort update")
			var gotDone, gotError bool
			for _, recorded := range m.events {
				event, ok := recorded.Payload.(chat_svc.ChatStreamEvent)
				if !ok {
					continue
				}
				switch event.Kind {
				case chat_svc.StreamDone:
					gotDone = true
				case chat_svc.StreamError:
					gotError = true
				}
			}
			assert.True(t, gotDone, "completed non-Pi answer must retain its prior success semantics")
			assert.False(t, gotError, "best-effort anchor persistence must not turn a non-Pi completion into StreamError")
		})
	}
}

func TestPiRestart_ForkStartupFailurePreservesExistingHistory(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(*chatMocks)
		invoke  func(chat_svc.ChatSvc, context.Context) (*chat_svc.SendResponse, error)
	}{
		{
			name: "Given a Pi regenerate fork is rejected, when Regenerate starts, then existing history is not truncated",
			arrange: func(m *chatMocks) {
				m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(&chat_entity.Message{
					ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1"),
				}, nil)
				m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
					{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: "pi-user-entry-exact"},
					{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1")},
				}, nil).AnyTimes()
			},
			invoke: func(svc chat_svc.ChatSvc, ctx context.Context) (*chat_svc.SendResponse, error) {
				return svc.Regenerate(ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
			},
		},
		{
			name: "Given a Pi edit fork is rejected, when Edit starts, then existing history is not truncated",
			arrange: func(m *chatMocks) {
				m.message.EXPECT().Find(gomock.Any(), int64(1000)).Return(&chat_entity.Message{
					ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"),
					ForkAnchor: "pi-user-entry-exact",
				}, nil)
			},
			invoke: func(svc chat_svc.ChatSvc, ctx context.Context) (*chat_svc.SendResponse, error) {
				return svc.Edit(ctx, &chat_svc.EditRequest{SessionID: 100, MessageID: 1000, Text: "replacement"})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := setupChatTest(t)
			runner := &forkStartupFailRunner{err: errors.New("pi fork rejected")}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
			t.Cleanup(restore)

			m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
				ID: 100, AgentID: 7, ProviderSessionID: "pi-session-old", AgentStatus: "idle", Status: consts.ACTIVE,
			}, nil)
			tc.arrange(m)
			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
				ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
			}, nil)
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
			}, nil)
			expectNoPiTranscriptRecovery(m, 100)
			// Intentionally omit transaction and message-mutation expectations:
			// any write before the failed fork preflight is a test failure.

			resp, err := tc.invoke(m.svc, m.ctx)
			if resp != nil {
				chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)
			}

			assert.Nil(t, resp)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "pi fork rejected")
		})
	}
}

// pairChatTestDevices 把给定几台机器登记成本机配对表的全部内容。backend 的 DeviceID
// 是规范指纹，派发边界要在那张表里解析出行 ID 才拨得动号。
func pairChatTestDevices(t *testing.T, deviceIDs ...int64) {
	t.Helper()
	ctrl := gomock.NewController(t)
	rows := make([]*remote_device_svc.DeviceView, 0, len(deviceIDs))
	for _, id := range deviceIDs {
		rows = append(rows, &remote_device_svc.DeviceView{
			ID: id, DaemonFingerprint: fmt.Sprintf("sha256:device-%d", id), Online: true,
		})
	}
	rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	rds.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()
	rds.EXPECT().List(gomock.Any()).Return(rows, nil).AnyTimes()
	rds.EXPECT().Get(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id int64) (*remote_device_svc.DeviceView, error) {
			for _, row := range rows {
				if row.ID == id {
					return row, nil
				}
			}
			return nil, nil
		}).AnyTimes()
	rds.EXPECT().ListDeviceProviders(gomock.Any()).Return(nil).AnyTimes()
	prev := remote_device_svc.Default()
	remote_device_svc.SetDefault(rds)
	t.Cleanup(func() { remote_device_svc.SetDefault(prev) })
}
func TestPiRestart_RemotePreparationPersistsForkIdentityBeforePromptStart(t *testing.T) {
	m := setupChatTest(t)
	pairChatTestDevices(t, 7)
	activated := false
	client := newPreparedRemotePiClient(func() bool { return activated })

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	lease := mock_remote_device_svc.NewMockLease(ctrl)
	pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease, nil)
	lease.EXPECT().Client().Return(protorpctest.WrapConnection(client)).AnyTimes()
	lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	lease.EXPECT().Release().Times(1)
	chat_svc.SetConnPoolForTest(m.svc, pool)
	t.Cleanup(func() { chat_svc.SetConnPoolForTest(m.svc, nil) })

	sess := &chat_entity.Session{
		ID: 100, ConversationID: convID(100), AgentID: 7, ProviderSessionID: "pi-session-old",
		AgentStatus: "idle", Status: consts.ACTIVE,
	}
	// AnyTimes:远端 runtime 出线前还要读一次这一行,问它的 conversation_id
	// (remote_pool.sessionConversationID)。
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil).AnyTimes()
	m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(&chat_entity.Message{
		ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("answer"),
	}, nil)
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
		{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: "pi-entry-old"},
		{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("answer")},
	}, nil).AnyTimes()
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi Remote", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), DeviceFingerprint: "sha256:device-7", Status: consts.ACTIVE,
	}, nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Cond(func(value any) bool {
		session, ok := value.(*chat_entity.Session)
		return ok && session.ProviderSessionID == "pi-session-new" && session.AgentStatus == "running"
	})).DoAndReturn(func(_ context.Context, _ *chat_entity.Session) error {
		activated = true
		return nil
	}).Times(1)
	expectAcknowledgedPiReplacement(m, 1, 2000, 2001)

	resp, err := m.svc.Regenerate(m.ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
	require.NoError(t, err)
	require.NotNil(t, resp)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	runs := client.runs()
	require.Len(t, runs, 3)
	assert.Equal(t, "pi-session-old", runs[0].ProviderSessionID)
	assert.Equal(t, "pi-entry-old", runs[0].ForkAnchor)
	assert.Equal(t, "pi-session-old", runs[1].ProviderSessionID)
	assert.Equal(t, "pi-session-new", runs[2].ProviderSessionID)
	assert.True(t, activated)
}

func TestPiRestart_OldPreparedCleanupDoesNotUseSessionWideAbortBeforeRetry(t *testing.T) {
	m := setupChatTest(t)
	runner := &generationSafePreparedRunner{}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
	t.Cleanup(restore)

	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, ProviderSessionID: "pi-session-old", AgentStatus: "idle", Status: consts.ACTIVE,
	}
	originalAssistant := &chat_entity.Message{
		ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("answer"),
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil).AnyTimes()
	m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(originalAssistant, nil).Times(2)
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
		{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: "pi-user-entry"},
		originalAssistant,
	}, nil).AnyTimes()
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil).AnyTimes()
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
	}, nil).AnyTimes()

	expectNoPiTranscriptRecovery(m, 100)
	firstResp, firstErr := m.svc.Regenerate(m.ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
	require.Nil(t, firstResp)
	require.ErrorContains(t, firstErr, "empty pre-prompt identity")
	expectNoPiTranscriptRecovery(m, 100)
	secondResp, secondErr := m.svc.Regenerate(m.ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
	require.Nil(t, secondResp)
	require.ErrorContains(t, secondErr, "second prepared generation reached")

	prepareCalls, closeCalls, abortCalls := runner.Counts()
	assert.Equal(t, 2, prepareCalls)
	assert.Equal(t, 1, closeCalls, "the old exact prepared process must be closed once")
	assert.Zero(t, abortCalls,
		"old prepared cleanup must not issue a session-wide abort that can hit an immediate retry")
}

func TestPiRestart_TranscriptFailureDoesNotSendPrompt(t *testing.T) {
	m := setupChatTest(t)
	runner := &promptCountingRunner{}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
	t.Cleanup(restore)
	expectNoPiTranscriptRecovery(m, 100)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, ProviderSessionID: "pi-session-old", AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(&chat_entity.Message{
		ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("answer"),
	}, nil)
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
		{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: "pi-user-entry"},
		{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("answer")},
	}, nil).AnyTimes()
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
	}, nil)

	m.dbMock.ExpectBegin()
	expectPiRecoveryNamespaceClaim(m, -201)
	expectPiRecoveryMarkerSave(m)
	m.dbMock.ExpectExec("UPDATE `chat_messages` SET `session_id`=\\? WHERE session_id = \\? AND seq >= \\?").
		WithArgs(int64(-201), int64(100), 1).
		WillReturnError(errors.New("transcript write failed"))
	m.dbMock.ExpectRollback()

	resp, err := m.svc.Regenerate(m.ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})

	require.Nil(t, resp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transcript write failed")
	assert.Zero(t, runner.PromptCalls(), "Pi must not receive the prompt before the transcript transaction commits")
}

func TestPiRestart_RecoveryNamespaceCollisionIsNonDestructive(t *testing.T) {
	m := setupChatTest(t)
	runner := &promptCountingRunner{}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
	t.Cleanup(restore)
	expectNoPiTranscriptRecovery(m, 100)

	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, ProviderSessionID: "pi-session-old", AgentStatus: "idle", Status: consts.ACTIVE,
	}
	originalAssistant := &chat_entity.Message{
		ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("answer"),
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(originalAssistant, nil)
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
		{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: "pi-user-entry"},
		originalAssistant,
	}, nil).AnyTimes()
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
	}, nil)

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
		WithArgs("chat.pi_recovery:100", 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}))
	m.dbMock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM `chat_messages` WHERE session_id = \\?").
		WithArgs(int64(-201)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	m.dbMock.ExpectRollback()

	resp, err := m.svc.Regenerate(m.ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
	require.Nil(t, resp)
	require.ErrorContains(t, err, chat_repo.ErrReplacementNamespaceCollision.Error())
	assert.Zero(t, runner.PromptCalls())
	assert.Equal(t, "pi-session-old", sess.ProviderSessionID)
	assert.Equal(t, "idle", sess.AgentStatus)
	assert.NoError(t, m.dbMock.ExpectationsWereMet())
}

func TestPiRestart_FirstRestoreFailureRecoversExactRowsAndSessionOnRetry(t *testing.T) {
	m := setupChatTest(t)
	expectNoPiTranscriptRecovery(m, 100)
	const recoverySessionID = int64(-201) // = -(100*2+1),由会话 id 推出
	originalUser := &chat_entity.Message{
		ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: "pi-user-entry",
	}
	originalAssistant := &chat_entity.Message{
		ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("answer"),
	}
	hiddenAssistant := *originalAssistant
	hiddenAssistant.SessionID = recoverySessionID
	restoredAssistant := *originalAssistant
	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, ProviderSessionID: "pi-session-old", AgentStatus: "idle", Status: consts.ACTIVE,
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil).AnyTimes()
	m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(originalAssistant, nil)
	m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(&hiddenAssistant, nil)
	m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(&restoredAssistant, nil)
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{originalUser, originalAssistant}, nil).AnyTimes()
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil).AnyTimes()
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
	}, nil).AnyTimes()

	m.dbMock.ExpectBegin()
	expectPiRecoveryNamespaceClaim(m, recoverySessionID)
	expectPiRecoveryMarkerSave(m)
	m.dbMock.ExpectExec("UPDATE `chat_messages` SET `session_id`=\\? WHERE session_id = \\? AND seq >= \\?").
		WithArgs(recoverySessionID, int64(100), 1).
		WillReturnResult(sqlmock.NewResult(0, 2))
	expectPiRecoveryMarkerSave(m)
	m.dbMock.ExpectCommit()
	createCalls := 0
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, message *chat_entity.Message) error {
			switch createCalls {
			case 0:
				message.ID = 2000
			case 1:
				message.ID = 2001
			}
			createCalls++
			return nil
		}).Times(2)
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, session *chat_entity.Session) error {
			assert.Equal(t, "pi-session-new", session.ProviderSessionID)
			assert.Equal(t, "running", session.AgentStatus)
			return nil
		}).Times(1)

	activationDurableBeforeStart := false
	runner := &preparedStartupFailRunner{
		err:               errors.New("prepared prompt startup failed"),
		providerSessionID: "pi-session-new",
	}
	runner.onStart = func() {
		activationErr := m.dbMock.ExpectationsWereMet()
		activationDurableBeforeStart = activationErr == nil
		if activationErr != nil {
			t.Errorf("prepared prompt started before replacement activation commit: %v", activationErr)
		}
		m.dbMock.ExpectBegin()
		m.dbMock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM `chat_messages` WHERE session_id = \\? AND seq >= \\? AND id NOT IN \\(\\?,\\?\\)").
			WithArgs(int64(100), 1, int64(2000), int64(2001)).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		m.dbMock.ExpectExec("UPDATE `chat_sessions` SET `provider_session_id`=\\?,`agent_status`=\\?,`last_message_at`=\\?,`updatetime`=\\? WHERE id = \\? AND provider_session_id = \\?").
			WithArgs("pi-session-old", "idle", int64(0), sqlmock.AnyArg(), int64(100), "pi-session-new").
			WillReturnError(errors.New("first restore write failed"))
		m.dbMock.ExpectRollback()
	}
	restoreRuntime := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)

	resp, err := m.svc.Regenerate(m.ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
	require.Nil(t, resp)
	require.Error(t, err)
	assert.ErrorContains(t, err, "prepared prompt startup failed")
	assert.ErrorContains(t, err, "first restore write failed")
	assert.True(t, activationDurableBeforeStart)
	assert.Equal(t, "pi-session-new", sess.ProviderSessionID,
		"a failed restore must leave the generation marker and active ownership intact for retry")
	assert.Equal(t, "running", sess.AgentStatus)
	assert.NoError(t, m.dbMock.ExpectationsWereMet())

	recovery := &chat_repo.ReplacementRecovery{
		RecoverySessionID:    recoverySessionID,
		SessionID:            100,
		FromSeq:              1,
		RequestMessageID:     1001,
		UserMessageID:        2000,
		AssistantMessageID:   2001,
		OldProviderSessionID: "pi-session-old",
		NewProviderSessionID: "pi-session-new",
		OldAgentStatus:       "idle",
		State:                chat_repo.ReplacementRecoveryPending,
	}
	marker, markerErr := chat_repo.NewReplacementRecoveryMarker(recovery)
	require.NoError(t, markerErr)
	m.dbMock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
		WithArgs(marker.Key, 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}).
			AddRow(marker.Key, marker.Value, marker.Updatetime))
	m.dbMock.ExpectBegin()
	m.dbMock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM `chat_messages` WHERE session_id = \\? AND seq >= \\? AND id NOT IN \\(\\?,\\?\\)").
		WithArgs(int64(100), 1, int64(2000), int64(2001)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	m.dbMock.ExpectExec("UPDATE `chat_sessions` SET `provider_session_id`=\\?,`agent_status`=\\?,`last_message_at`=\\?,`updatetime`=\\? WHERE id = \\? AND provider_session_id = \\?").
		WithArgs("pi-session-old", "idle", int64(0), sqlmock.AnyArg(), int64(100), "pi-session-new").
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.dbMock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id IN \\(SELECT id FROM `chat_messages` WHERE session_id = \\? AND id IN \\(\\?,\\?\\)\\)").
		WithArgs(int64(100), int64(2000), int64(2001)).
		WillReturnResult(sqlmock.NewResult(0, 4))
	m.dbMock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\? AND id IN \\(\\?,\\?\\)").
		WithArgs(int64(100), int64(2000), int64(2001)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	m.dbMock.ExpectExec("UPDATE `chat_messages` SET `session_id`=\\? WHERE session_id = \\? AND seq >= \\?").
		WithArgs(int64(100), recoverySessionID, 1).
		WillReturnResult(sqlmock.NewResult(0, 2))
	m.dbMock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id IN \\(SELECT id FROM `chat_messages` WHERE session_id = \\?\\)").
		WithArgs(recoverySessionID).
		WillReturnResult(sqlmock.NewResult(0, 3))
	m.dbMock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\?").
		WithArgs(recoverySessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.dbMock.ExpectExec("DELETE FROM `app_settings` WHERE `key` = \\?").
		WithArgs("chat.pi_recovery:100").
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.dbMock.ExpectCommit()

	restoreRuntime()
	secondRuntimeRestore := agentruntime.SwapRuntimeForTest(
		agent_backend_entity.TypePiAgent,
		&forkStartupFailRunner{err: errors.New("second preflight reached after recovery")},
	)
	defer secondRuntimeRestore()
	secondResp, secondErr := m.svc.Regenerate(m.ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
	require.Nil(t, secondResp)
	require.ErrorContains(t, secondErr, "second preflight reached after recovery")
	assert.Equal(t, int64(100), restoredAssistant.SessionID,
		"the exact original row must be reloaded from the visible session before retry preflight")
	assert.Equal(t, "pi-session-old", sess.ProviderSessionID)
	assert.Equal(t, "idle", sess.AgentStatus)
	assert.NoError(t, m.dbMock.ExpectationsWereMet())
}

func TestPiRecoveryGate_PendingMarkerCannotCreateHybridFollowupHistory(t *testing.T) {
	m := setupChatTest(t)
	m.dbMock.MatchExpectationsInOrder(false)
	const recoverySessionID = int64(-201) // = -(100*2+1),由会话 id 推出

	recovery := &chat_repo.ReplacementRecovery{
		RecoverySessionID:    recoverySessionID,
		SessionID:            100,
		FromSeq:              1,
		RequestMessageID:     1001,
		UserMessageID:        2000,
		AssistantMessageID:   2001,
		OldProviderSessionID: "pi-session-old",
		NewProviderSessionID: "pi-session-new",
		OldAgentStatus:       "idle",
		OldLastMessageAt:     99,
		State:                chat_repo.ReplacementRecoveryPending,
	}
	marker, markerErr := chat_repo.NewReplacementRecoveryMarker(recovery)
	require.NoError(t, markerErr)

	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, ProviderSessionID: "pi-session-new", AgentStatus: "running", Status: consts.ACTIVE,
	}
	activeUser := &chat_entity.Message{
		ID: 2000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("replacement"),
	}
	activeAssistant := &chat_entity.Message{
		ID: 2001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: "[]",
	}
	followups := make([]*chat_entity.Message, 0, 2)
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil).AnyTimes()
	m.message.EXPECT().Find(gomock.Any(), int64(2001)).Return(activeAssistant, nil).AnyTimes()
	m.message.EXPECT().List(gomock.Any(), int64(100)).DoAndReturn(
		func(context.Context, int64) ([]*chat_entity.Message, error) {
			return append([]*chat_entity.Message{activeUser, activeAssistant}, followups...), nil
		}).AnyTimes()
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil).AnyTimes()
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
	}, nil).AnyTimes()
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil).Times(1)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, message *chat_entity.Message) error {
			message.ID = 3001 + int64(len(followups))
			followups = append(followups, message)
			return nil
		}).Times(2)

	m.dbMock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
		WithArgs(marker.Key, 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}).
			AddRow(marker.Key, marker.Value, marker.Updatetime))
	m.dbMock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM `chat_messages` WHERE session_id = \\? AND seq >= \\? AND id NOT IN \\(\\?,\\?\\)").
		WithArgs(int64(100), 1, int64(2000), int64(2001)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	for range 2 {
		m.dbMock.ExpectBegin()
		m.dbMock.ExpectCommit()
	}
	m.dbMock.ExpectExec("UPDATE `chat_sessions` SET `provider_session_id`=\\?,`agent_status`=\\?,`last_message_at`=\\?,`updatetime`=\\? WHERE id = \\? AND provider_session_id = \\?").
		WithArgs("pi-session-old", "idle", int64(99), sqlmock.AnyArg(), int64(100), "pi-session-new").
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.dbMock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id IN \\(SELECT id FROM `chat_messages` WHERE session_id = \\? AND id IN \\(\\?,\\?\\)\\)").
		WithArgs(int64(100), int64(2000), int64(2001)).
		WillReturnResult(sqlmock.NewResult(0, 4))
	m.dbMock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\? AND id IN \\(\\?,\\?\\)").
		WithArgs(int64(100), int64(2000), int64(2001)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	m.dbMock.ExpectExec("UPDATE `chat_messages` SET `session_id`=\\? WHERE session_id = \\? AND seq >= \\?").
		WithArgs(int64(100), recoverySessionID, 1).
		WillReturnResult(sqlmock.NewResult(0, 2))
	m.dbMock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id IN \\(SELECT id FROM `chat_messages` WHERE session_id = \\?\\)").
		WithArgs(recoverySessionID).
		WillReturnResult(sqlmock.NewResult(0, 3))
	m.dbMock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\?").
		WithArgs(recoverySessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.dbMock.ExpectExec("DELETE FROM `app_settings` WHERE `key` = \\?").
		WithArgs("chat.pi_recovery:100").
		WillReturnResult(sqlmock.NewResult(0, 1))

	runner := &providerRecordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restoreRuntime := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
	t.Cleanup(restoreRuntime)

	resp, err := m.svc.Send(m.ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "follow-up"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)
	req := <-runner.requests

	if req.ProviderSessionID == "pi-session-new" {
		_, recoveryErr := m.svc.Regenerate(m.ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 2001})
		require.Error(t, recoveryErr)
		assert.Fail(t, "hybrid transcript reproduced",
			"pending recovery restored original seq 1/2 and old provider while follow-up rows %d/%d at seq 3/4 survived",
			followups[0].ID, followups[1].ID)
	}
	assert.Equal(t, "pi-session-old", req.ProviderSessionID,
		"the recovery gate must restore provider ownership before accepting the follow-up")
	assert.Equal(t, []int{3, 4}, []int{followups[0].Seq, followups[1].Seq})
	assert.NoError(t, m.dbMock.ExpectationsWereMet())
}

func TestPiRecoveryGate_FirstRestoreWriteFailureRetriesBeforeAppending(t *testing.T) {
	m := setupChatTest(t)
	const recoverySessionID = int64(-201) // = -(100*2+1),由会话 id 推出
	recovery := &chat_repo.ReplacementRecovery{
		RecoverySessionID:    recoverySessionID,
		SessionID:            100,
		FromSeq:              1,
		RequestMessageID:     1001,
		UserMessageID:        2000,
		AssistantMessageID:   2001,
		OldProviderSessionID: "pi-session-old",
		NewProviderSessionID: "pi-session-new",
		OldAgentStatus:       "idle",
		OldLastMessageAt:     99,
		State:                chat_repo.ReplacementRecoveryPending,
	}
	marker, markerErr := chat_repo.NewReplacementRecoveryMarker(recovery)
	require.NoError(t, markerErr)
	markerRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"key", "value", "updatetime"}).
			AddRow(marker.Key, marker.Value, marker.Updatetime)
	}

	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, ProviderSessionID: "pi-session-new", AgentStatus: "running", Status: consts.ACTIVE,
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil).AnyTimes()
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil).AnyTimes()
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
	}, nil).AnyTimes()
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
		WithArgs(marker.Key, 1).WillReturnRows(markerRows())
	m.dbMock.ExpectBegin()
	m.dbMock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM `chat_messages` WHERE session_id = \\? AND seq >= \\? AND id NOT IN \\(\\?,\\?\\)").
		WithArgs(int64(100), 1, int64(2000), int64(2001)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	m.dbMock.ExpectExec("UPDATE `chat_sessions` SET `provider_session_id`=\\?,`agent_status`=\\?,`last_message_at`=\\?,`updatetime`=\\? WHERE id = \\? AND provider_session_id = \\?").
		WithArgs("pi-session-old", "idle", int64(99), sqlmock.AnyArg(), int64(100), "pi-session-new").
		WillReturnError(errors.New("first recovery write failed"))
	m.dbMock.ExpectRollback()

	runner := &providerRecordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restoreRuntime := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
	t.Cleanup(restoreRuntime)
	firstResp, firstErr := m.svc.Send(m.ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "follow-up"})
	require.Nil(t, firstResp)
	require.ErrorContains(t, firstErr, "first recovery write failed")
	select {
	case req := <-runner.requests:
		t.Fatalf("failed recovery must not reach provider work: %+v", req)
	default:
	}
	assert.Equal(t, "pi-session-new", sess.ProviderSessionID)

	m.dbMock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
		WithArgs(marker.Key, 1).WillReturnRows(markerRows())
	m.dbMock.ExpectBegin()
	m.dbMock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM `chat_messages` WHERE session_id = \\? AND seq >= \\? AND id NOT IN \\(\\?,\\?\\)").
		WithArgs(int64(100), 1, int64(2000), int64(2001)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	m.dbMock.ExpectExec("UPDATE `chat_sessions` SET `provider_session_id`=\\?,`agent_status`=\\?,`last_message_at`=\\?,`updatetime`=\\? WHERE id = \\? AND provider_session_id = \\?").
		WithArgs("pi-session-old", "idle", int64(99), sqlmock.AnyArg(), int64(100), "pi-session-new").
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.dbMock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id IN \\(SELECT id FROM `chat_messages` WHERE session_id = \\? AND id IN \\(\\?,\\?\\)\\)").
		WithArgs(int64(100), int64(2000), int64(2001)).
		WillReturnResult(sqlmock.NewResult(0, 4))
	m.dbMock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\? AND id IN \\(\\?,\\?\\)").
		WithArgs(int64(100), int64(2000), int64(2001)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	m.dbMock.ExpectExec("UPDATE `chat_messages` SET `session_id`=\\? WHERE session_id = \\? AND seq >= \\?").
		WithArgs(int64(100), recoverySessionID, 1).
		WillReturnResult(sqlmock.NewResult(0, 2))
	m.dbMock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id IN \\(SELECT id FROM `chat_messages` WHERE session_id = \\?\\)").
		WithArgs(recoverySessionID).
		WillReturnResult(sqlmock.NewResult(0, 3))
	m.dbMock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\?").
		WithArgs(recoverySessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.dbMock.ExpectExec("DELETE FROM `app_settings` WHERE `key` = \\?").
		WithArgs("chat.pi_recovery:100").
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.dbMock.ExpectCommit()
	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil).Times(1)
	createdSeqs := make([]int, 0, 2)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, message *chat_entity.Message) error {
			message.ID = 4000 + int64(len(createdSeqs))
			createdSeqs = append(createdSeqs, message.Seq)
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	secondResp, secondErr := m.svc.Send(m.ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "follow-up"})
	require.NoError(t, secondErr)
	require.NotNil(t, secondResp)
	chat_svc.WaitForStreamForTest(m.svc, secondResp.AssistantMessageID)
	secondReq := <-runner.requests
	assert.Equal(t, "pi-session-old", secondReq.ProviderSessionID)
	assert.Equal(t, []int{3, 4}, createdSeqs)
	assert.NoError(t, m.dbMock.ExpectationsWereMet())
}

func TestPiRecoveryGate_AcknowledgedMarkerCleansHiddenOriginalsBeforeResume(t *testing.T) {
	m := setupChatTest(t)
	const recoverySessionID = int64(-201) // = -(100*2+1),由会话 id 推出
	recovery := &chat_repo.ReplacementRecovery{RecoverySessionID: recoverySessionID, SessionID: 100, FromSeq: 1,
		RequestMessageID: 1001, UserMessageID: 2000, AssistantMessageID: 2001,
		OldProviderSessionID: "pi-session-old", NewProviderSessionID: "pi-session-new",
		OldAgentStatus: "idle", State: chat_repo.ReplacementRecoveryAcknowledged,
	}
	marker, markerErr := chat_repo.NewReplacementRecoveryMarker(recovery)
	require.NoError(t, markerErr)
	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, ProviderSessionID: "pi-session-new", AgentStatus: "idle", Status: consts.ACTIVE,
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
	}, nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
		WithArgs(marker.Key, 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}).
			AddRow(marker.Key, marker.Value, marker.Updatetime))
	m.dbMock.ExpectBegin()
	m.dbMock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id IN \\(SELECT id FROM `chat_messages` WHERE session_id = \\?\\)").
		WithArgs(recoverySessionID).
		WillReturnResult(sqlmock.NewResult(0, 9))
	m.dbMock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\?").
		WithArgs(recoverySessionID).
		WillReturnResult(sqlmock.NewResult(0, 3))
	m.dbMock.ExpectExec("DELETE FROM `app_settings` WHERE `key` = \\?").
		WithArgs("chat.pi_recovery:100").
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.dbMock.ExpectCommit()
	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, message *chat_entity.Message) error {
			message.ID = 5000 + int64(message.Seq)
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	runner := &providerRecordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restoreRuntime := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
	t.Cleanup(restoreRuntime)
	resp, err := m.svc.Send(m.ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "resume"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)
	req := <-runner.requests
	assert.Equal(t, "pi-session-new", req.ProviderSessionID)
	assert.NoError(t, m.dbMock.ExpectationsWereMet())
}

func TestPiRecoveryGate_HiddenTargetWithoutExactMarkerIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name    string
		target  *chat_entity.Message
		invoke  func(chat_svc.ChatSvc, context.Context) (*chat_svc.SendResponse, error)
		arrange func(*chatMocks)
	}{
		{
			name: "regenerate hidden assistant",
			target: &chat_entity.Message{
				ID: 1001, SessionID: -999, Role: "assistant", Seq: 2, BlocksJSON: encodeText("hidden"),
			},
			invoke: func(svc chat_svc.ChatSvc, ctx context.Context) (*chat_svc.SendResponse, error) {
				return svc.Regenerate(ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
			},
			arrange: func(*chatMocks) {},
		},
		{
			name: "edit hidden user",
			target: &chat_entity.Message{
				ID: 1000, SessionID: -999, Role: "user", Seq: 1, BlocksJSON: encodeText("hidden"),
			},
			invoke: func(svc chat_svc.ChatSvc, ctx context.Context) (*chat_svc.SendResponse, error) {
				return svc.Edit(ctx, &chat_svc.EditRequest{SessionID: 100, MessageID: 1000, Text: "replacement"})
			},
			arrange: func(*chatMocks) {},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := setupChatTest(t)
			m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
				ID: 100, AgentID: 7, ProviderSessionID: "pi-session-old", AgentStatus: "idle", Status: consts.ACTIVE,
			}, nil)
			m.message.EXPECT().Find(gomock.Any(), tc.target.ID).Return(tc.target, nil)
			tc.arrange(m)
			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
				ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
			}, nil)
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
			}, nil)
			expectNoPiTranscriptRecovery(m, 100)

			resp, err := tc.invoke(m.svc, m.ctx)
			require.Nil(t, resp)
			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, code.ChatMessageNotFound, httpErr.Code)
			assert.NoError(t, m.dbMock.ExpectationsWereMet())
		})
	}
}

func TestPiRecoveryGate_AcknowledgedMarkerCannotCleanNewerProviderGeneration(t *testing.T) {
	m := setupChatTest(t)
	const recoverySessionID = int64(-201) // = -(100*2+1),由会话 id 推出
	recovery := &chat_repo.ReplacementRecovery{RecoverySessionID: recoverySessionID, SessionID: 100, FromSeq: 1,
		RequestMessageID: 1001, UserMessageID: 2000, AssistantMessageID: 2001,
		OldProviderSessionID: "pi-session-old", NewProviderSessionID: "pi-session-new",
		OldAgentStatus: "idle", State: chat_repo.ReplacementRecoveryAcknowledged,
	}
	marker, markerErr := chat_repo.NewReplacementRecoveryMarker(recovery)
	require.NoError(t, markerErr)
	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, ProviderSessionID: "pi-session-newer", AgentStatus: "idle", Status: consts.ACTIVE,
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
	}, nil)
	m.dbMock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
		WithArgs(marker.Key, 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}).
			AddRow(marker.Key, marker.Value, marker.Updatetime))

	runner := &providerRecordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restoreRuntime := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
	t.Cleanup(restoreRuntime)
	resp, err := m.svc.Send(m.ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "must not run"})
	require.Nil(t, resp)
	require.ErrorContains(t, err, chat_repo.ErrReplacementOwnershipLost.Error())
	assert.Equal(t, "pi-session-newer", sess.ProviderSessionID)
	select {
	case req := <-runner.requests:
		t.Fatalf("stale acknowledged cleanup reached newer provider generation: %+v", req)
	default:
	}
	assert.NoError(t, m.dbMock.ExpectationsWereMet())
}

func TestPiRestart_CrashRecoveryFindsPendingGenerationFromActiveMessage(t *testing.T) {
	m := setupChatTest(t)
	const recoverySessionID = int64(-201) // = -(100*2+1),由会话 id 推出
	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, ProviderSessionID: "pi-session-new", AgentStatus: "running", Status: consts.ACTIVE,
	}
	activeAssistant := &chat_entity.Message{
		ID: 2001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: "[]",
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.message.EXPECT().Find(gomock.Any(), int64(2001)).Return(activeAssistant, nil)
	m.message.EXPECT().Find(gomock.Any(), int64(2001)).Return(nil, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
	}, nil)

	recovery := &chat_repo.ReplacementRecovery{
		RecoverySessionID:    recoverySessionID,
		SessionID:            100,
		FromSeq:              1,
		RequestMessageID:     1001,
		UserMessageID:        2000,
		AssistantMessageID:   2001,
		OldProviderSessionID: "pi-session-old",
		NewProviderSessionID: "pi-session-new",
		OldAgentStatus:       "idle",
		OldLastMessageAt:     99,
		State:                chat_repo.ReplacementRecoveryPending,
	}
	marker, markerErr := chat_repo.NewReplacementRecoveryMarker(recovery)
	require.NoError(t, markerErr)
	m.dbMock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
		WithArgs(marker.Key, 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}).
			AddRow(marker.Key, marker.Value, marker.Updatetime))
	m.dbMock.ExpectBegin()
	m.dbMock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM `chat_messages` WHERE session_id = \\? AND seq >= \\? AND id NOT IN \\(\\?,\\?\\)").
		WithArgs(int64(100), 1, int64(2000), int64(2001)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	m.dbMock.ExpectExec("UPDATE `chat_sessions` SET `provider_session_id`=\\?,`agent_status`=\\?,`last_message_at`=\\?,`updatetime`=\\? WHERE id = \\? AND provider_session_id = \\?").
		WithArgs("pi-session-old", "idle", int64(99), sqlmock.AnyArg(), int64(100), "pi-session-new").
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.dbMock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id IN \\(SELECT id FROM `chat_messages` WHERE session_id = \\? AND id IN \\(\\?,\\?\\)\\)").
		WithArgs(int64(100), int64(2000), int64(2001)).
		WillReturnResult(sqlmock.NewResult(0, 4))
	m.dbMock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\? AND id IN \\(\\?,\\?\\)").
		WithArgs(int64(100), int64(2000), int64(2001)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	m.dbMock.ExpectExec("UPDATE `chat_messages` SET `session_id`=\\? WHERE session_id = \\? AND seq >= \\?").
		WithArgs(int64(100), recoverySessionID, 1).
		WillReturnResult(sqlmock.NewResult(0, 2))
	m.dbMock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id IN \\(SELECT id FROM `chat_messages` WHERE session_id = \\?\\)").
		WithArgs(recoverySessionID).
		WillReturnResult(sqlmock.NewResult(0, 3))
	m.dbMock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\?").
		WithArgs(recoverySessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.dbMock.ExpectExec("DELETE FROM `app_settings` WHERE `key` = \\?").
		WithArgs("chat.pi_recovery:100").
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.dbMock.ExpectCommit()

	runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
	t.Cleanup(restore)
	resp, err := m.svc.Regenerate(m.ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 2001})
	require.Nil(t, resp)
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, code.ChatMessageNotFound, httpErr.Code)
	assert.Equal(t, "pi-session-old", sess.ProviderSessionID)
	assert.Equal(t, "idle", sess.AgentStatus)
	assert.Equal(t, int64(99), sess.LastMessageAt)
	select {
	case req := <-runner.requests:
		t.Fatalf("crash recovery must finish before starting a new Pi generation: %+v", req)
	default:
	}
	assert.NoError(t, m.dbMock.ExpectationsWereMet())
}

func TestPiRestart_AcknowledgedCleanupFailureRetriesWithoutRestoringActiveGeneration(t *testing.T) {
	m := setupChatTest(t)
	const recoverySessionID = int64(-201) // = -(100*2+1),由会话 id 推出
	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, ProviderSessionID: "pi-session-new", AgentStatus: "running", Status: consts.ACTIVE,
	}
	hiddenAssistant := &chat_entity.Message{
		ID: 1001, SessionID: recoverySessionID, Role: "assistant", Seq: 2, BlocksJSON: encodeText("answer"),
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil).Times(2)
	m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(hiddenAssistant, nil).Times(2)
	m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(nil, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil).Times(2)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
	}, nil).Times(2)

	recovery := &chat_repo.ReplacementRecovery{
		RecoverySessionID:    recoverySessionID,
		SessionID:            100,
		FromSeq:              1,
		RequestMessageID:     1001,
		UserMessageID:        2000,
		AssistantMessageID:   2001,
		OldProviderSessionID: "pi-session-old",
		NewProviderSessionID: "pi-session-new",
		OldAgentStatus:       "idle",
		State:                chat_repo.ReplacementRecoveryAcknowledged,
	}
	marker, markerErr := chat_repo.NewReplacementRecoveryMarker(recovery)
	require.NoError(t, markerErr)
	m.dbMock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
		WithArgs(marker.Key, 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}).
			AddRow(marker.Key, marker.Value, marker.Updatetime))
	m.dbMock.ExpectBegin()
	m.dbMock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id IN \\(SELECT id FROM `chat_messages` WHERE session_id = \\?\\)").
		WithArgs(recoverySessionID).
		WillReturnError(errors.New("first cleanup write failed"))
	m.dbMock.ExpectRollback()
	m.dbMock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
		WithArgs(marker.Key, 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}).
			AddRow(marker.Key, marker.Value, marker.Updatetime))
	m.dbMock.ExpectBegin()
	m.dbMock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id IN \\(SELECT id FROM `chat_messages` WHERE session_id = \\?\\)").
		WithArgs(recoverySessionID).
		WillReturnResult(sqlmock.NewResult(0, 9))
	m.dbMock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\?").
		WithArgs(recoverySessionID).
		WillReturnResult(sqlmock.NewResult(0, 3))
	m.dbMock.ExpectExec("DELETE FROM `app_settings` WHERE `key` = \\?").
		WithArgs("chat.pi_recovery:100").
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.dbMock.ExpectCommit()

	firstResp, firstErr := m.svc.Regenerate(m.ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
	require.Nil(t, firstResp)
	require.ErrorContains(t, firstErr, "first cleanup write failed")
	secondResp, secondErr := m.svc.Regenerate(m.ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
	require.Nil(t, secondResp)
	var httpErr *httputils.Error
	require.ErrorAs(t, secondErr, &httpErr)
	assert.Equal(t, code.ChatMessageNotFound, httpErr.Code)
	assert.Equal(t, "pi-session-new", sess.ProviderSessionID,
		"ack cleanup must not restore the old provider identity over the active generation")
	assert.Equal(t, "running", sess.AgentStatus)
	assert.NoError(t, m.dbMock.ExpectationsWereMet())
}

func TestPiRestart_UnresolvedAcknowledgementStopsTurnAndRestoresPendingGeneration(t *testing.T) {
	m := setupChatTest(t)
	const recoverySessionID = int64(-201) // = -(100*2+1),由会话 id 推出
	originalUser := &chat_entity.Message{
		ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: "pi-user-entry",
	}
	originalAssistant := &chat_entity.Message{
		ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("answer"),
	}
	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, ProviderSessionID: "pi-session-old", AgentStatus: "idle", LastMessageAt: 99, Status: consts.ACTIVE,
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(originalAssistant, nil)
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{originalUser, originalAssistant}, nil).AnyTimes()
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
	}, nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	pendingMarker := piRecoveryMarker(chat_repo.ReplacementRecoveryPending, 1, 2000, 2001)
	m.dbMock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
		WithArgs("chat.pi_recovery:100", 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}))
	m.dbMock.ExpectBegin()
	expectPiRecoveryNamespaceClaim(m, recoverySessionID)
	expectPiRecoveryMarkerSave(m)
	m.dbMock.ExpectExec("UPDATE `chat_messages` SET `session_id`=\\? WHERE session_id = \\? AND seq >= \\?").
		WithArgs(recoverySessionID, int64(100), 1).
		WillReturnResult(sqlmock.NewResult(0, 2))
	expectPiRecoveryMarkerSave(m)
	m.dbMock.ExpectCommit()
	createCalls := 0
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, message *chat_entity.Message) error {
			switch createCalls {
			case 0:
				message.ID = 2000
			case 1:
				message.ID = 2001
			}
			createCalls++
			return nil
		}).Times(2)

	for range 2 {
		m.dbMock.ExpectBegin()
		m.dbMock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
			WithArgs("chat.pi_recovery:100", 1).
			WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}).
				AddRow(pendingMarker.Key, pendingMarker.Value, pendingMarker.Updatetime))
		m.dbMock.ExpectExec("INSERT INTO `app_settings`").
			WithArgs("chat.pi_recovery:100", sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnError(errors.New("acknowledgement state write failed"))
		m.dbMock.ExpectRollback()
	}
	m.dbMock.ExpectBegin()
	m.dbMock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM `chat_messages` WHERE session_id = \\? AND seq >= \\? AND id NOT IN \\(\\?,\\?\\)").
		WithArgs(int64(100), 1, int64(2000), int64(2001)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	m.dbMock.ExpectExec("UPDATE `chat_sessions` SET `provider_session_id`=\\?,`agent_status`=\\?,`last_message_at`=\\?,`updatetime`=\\? WHERE id = \\? AND provider_session_id = \\?").
		WithArgs("pi-session-old", "idle", int64(99), sqlmock.AnyArg(), int64(100), "pi-session-new").
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.dbMock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id IN \\(SELECT id FROM `chat_messages` WHERE session_id = \\? AND id IN \\(\\?,\\?\\)\\)").
		WithArgs(int64(100), int64(2000), int64(2001)).
		WillReturnResult(sqlmock.NewResult(0, 4))
	m.dbMock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\? AND id IN \\(\\?,\\?\\)").
		WithArgs(int64(100), int64(2000), int64(2001)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	m.dbMock.ExpectExec("UPDATE `chat_messages` SET `session_id`=\\? WHERE session_id = \\? AND seq >= \\?").
		WithArgs(int64(100), recoverySessionID, 1).
		WillReturnResult(sqlmock.NewResult(0, 2))
	m.dbMock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id IN \\(SELECT id FROM `chat_messages` WHERE session_id = \\?\\)").
		WithArgs(recoverySessionID).
		WillReturnResult(sqlmock.NewResult(0, 3))
	m.dbMock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\?").
		WithArgs(recoverySessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.dbMock.ExpectExec("DELETE FROM `app_settings` WHERE `key` = \\?").
		WithArgs("chat.pi_recovery:100").
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.dbMock.ExpectCommit()

	streamEvents := make(chan agentruntime.Event, 2)
	streamEvents <- agentruntime.TextDelta{Text: "must not be accepted"}
	streamEvents <- agentruntime.Done{}
	close(streamEvents)
	runner := &preparedAcknowledgedRunner{
		providerSessionID: "pi-session-new",
		events:            streamEvents,
	}
	restoreRuntime := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
	t.Cleanup(restoreRuntime)

	resp, err := m.svc.Regenerate(m.ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
	if resp != nil {
		chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)
	}
	assert.Nil(t, resp)
	require.ErrorContains(t, err, "acknowledgement state write failed")
	assert.Equal(t, "pi-session-old", sess.ProviderSessionID)
	assert.Equal(t, "idle", sess.AgentStatus)
	for _, recorded := range m.events {
		event, ok := recorded.Payload.(chat_svc.ChatStreamEvent)
		if ok {
			assert.NotEqual(t, chat_svc.StreamDone, event.Kind,
				"output must not be accepted while acknowledgement state is unresolved")
		}
	}
	assert.NoError(t, m.dbMock.ExpectationsWereMet())
}

func TestPiRestart_AcknowledgedPromptActivatesTranscriptWithForkedSessionAtomically(t *testing.T) {
	m := setupChatTest(t)
	expectNoPiTranscriptRecovery(m, 100)
	originalUser := &chat_entity.Message{
		ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: "pi-user-entry",
	}
	originalAssistant := &chat_entity.Message{
		ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("answer"),
	}
	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, ProviderSessionID: "pi-session-old", AgentStatus: "idle", Status: consts.ACTIVE,
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(originalAssistant, nil)
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{originalUser, originalAssistant}, nil).AnyTimes()
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
	}, nil)

	const recoverySessionID = int64(-201) // = -(100*2+1),由会话 id 推出
	pendingMarker := piRecoveryMarker(chat_repo.ReplacementRecoveryPending, 1, 2000, 2001)
	ownedMarker := &capturedArg{}
	m.dbMock.ExpectBegin()
	expectPiRecoveryNamespaceClaim(m, recoverySessionID)
	expectPiRecoveryMarkerSave(m)
	m.dbMock.ExpectExec("UPDATE `chat_messages` SET `session_id`=\\? WHERE session_id = \\? AND seq >= \\?").
		WithArgs(recoverySessionID, int64(100), 1).
		WillReturnResult(sqlmock.NewResult(0, 2))
	// 补齐两条 active 行 id 之后的标记回写:与被隐藏的原始行同属一个事务。
	m.dbMock.ExpectExec("INSERT INTO `app_settings`").
		WithArgs("chat.pi_recovery:100", ownedMarker, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	m.dbMock.ExpectCommit()

	var (
		activationConnPool any
		activationObserved bool
		createCalls        int
	)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(createCtx context.Context, message *chat_entity.Message) error {
			connPool := db.Ctx(createCtx).Statement.ConnPool
			_, inTransaction := connPool.(*sql.Tx)
			assert.True(t, inTransaction)
			if activationConnPool == nil {
				activationConnPool = connPool
			} else {
				assert.Same(t, activationConnPool, connPool)
			}
			switch createCalls {
			case 0:
				message.ID = 2000
				assert.Equal(t, int64(100), message.SessionID)
				assert.Equal(t, "user", message.Role)
			case 1:
				message.ID = 2001
				assert.Equal(t, int64(100), message.SessionID)
				assert.Equal(t, "assistant", message.Role)
			}
			createCalls++
			return nil
		}).Times(2)
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(updateCtx context.Context, session *chat_entity.Session) error {
			if session.AgentStatus == "running" {
				connPool := db.Ctx(updateCtx).Statement.ConnPool
				_, inTransaction := connPool.(*sql.Tx)
				assert.True(t, inTransaction,
					"the forked provider session must not depend on a later best-effort update")
				assert.Same(t, activationConnPool, connPool,
					"the moved originals, replacement rows, and provider session must share one transaction")
				assert.Equal(t, "pi-session-new", session.ProviderSessionID,
					"the canonical transcript and forked native session must activate together")
				activationObserved = true
			}
			return nil
		}).AnyTimes()

	activationDurableBeforePrompt := false
	streamEvents := make(chan agentruntime.Event)
	var closeStream sync.Once
	t.Cleanup(func() { closeStream.Do(func() { close(streamEvents) }) })
	runner := &preparedAcknowledgedRunner{
		providerSessionID: "pi-session-new",
		events:            streamEvents,
	}
	runner.onStart = func() {
		activationErr := m.dbMock.ExpectationsWereMet()
		activationDurableBeforePrompt = activationErr == nil
		if activationErr != nil {
			t.Errorf("Pi prompt started before activation committed: %v", activationErr)
		}
		assert.True(t, activationObserved,
			"Pi prompt started before canonical transcript and forked provider session activation")
		assert.Equal(t, "pi-session-new", sess.ProviderSessionID,
			"the forked provider session must be durable before Start sends the prompt")
		m.dbMock.ExpectBegin()
		m.dbMock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
			WithArgs("chat.pi_recovery:100", 1).
			WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}).
				AddRow(pendingMarker.Key, pendingMarker.Value, pendingMarker.Updatetime))
		m.dbMock.ExpectExec("INSERT INTO `app_settings`").
			WithArgs("chat.pi_recovery:100", sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))
		m.dbMock.ExpectCommit()
		m.dbMock.ExpectBegin()
		expectPiRecoveryCleanup(m, recoverySessionID)
		m.dbMock.ExpectCommit()
	}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
	t.Cleanup(restore)

	resp, err := m.svc.Regenerate(m.ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, activationDurableBeforePrompt)
	assert.True(t, activationObserved,
		"activation transaction did not persist the forked provider session")
	ownedRecovery, parseErr := chat_repo.ParseReplacementRecoveryMarker(&app_setting_entity.AppSetting{
		Key: "chat.pi_recovery:100", Value: fmt.Sprint(ownedMarker.value),
	})
	require.NoError(t, parseErr, "activation must persist one exact recovery marker")
	assert.Equal(t, int64(1001), ownedRecovery.RequestMessageID)
	assert.Equal(t, int64(2000), ownedRecovery.UserMessageID)
	assert.Equal(t, int64(2001), ownedRecovery.AssistantMessageID)
	assert.Equal(t, "pi-session-old", ownedRecovery.OldProviderSessionID)
	assert.Equal(t, "pi-session-new", ownedRecovery.NewProviderSessionID)
	assert.Equal(t, "pi-session-new", sess.ProviderSessionID,
		"the immediate post-activation state must already point at the forked Pi session")
	assert.NoError(t, m.dbMock.ExpectationsWereMet())

	closeStream.Do(func() { close(streamEvents) })
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)
}

func TestPiRestart_PromptRejectionRestoresOriginalRowsAndProviderIdentity(t *testing.T) {
	m := setupChatTest(t)
	expectNoPiTranscriptRecovery(m, 100)
	const recoverySessionID = int64(-201) // = -(100*2+1),由会话 id 推出
	runner := &preparedStartupFailRunner{
		err:               errors.New("Pi prompt rejected"),
		providerSessionID: "pi-session-new",
	}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
	t.Cleanup(restore)

	originalUser := &chat_entity.Message{
		ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: "pi-user-entry",
	}
	originalAssistant := &chat_entity.Message{
		ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("answer"),
	}
	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, ProviderSessionID: "pi-session-old", AgentStatus: "idle", LastMessageAt: 99, Status: consts.ACTIVE,
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(originalAssistant, nil)
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{originalUser, originalAssistant}, nil).AnyTimes()
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
	}, nil)

	m.dbMock.ExpectBegin()
	expectPiRecoveryNamespaceClaim(m, recoverySessionID)
	expectPiRecoveryMarkerSave(m)
	m.dbMock.ExpectExec("UPDATE `chat_messages` SET `session_id`=\\? WHERE session_id = \\? AND seq >= \\?").
		WithArgs(recoverySessionID, int64(100), 1).
		WillReturnResult(sqlmock.NewResult(0, 2))
	expectPiRecoveryMarkerSave(m)
	m.dbMock.ExpectCommit()
	createCalls := 0
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, message *chat_entity.Message) error {
			switch createCalls {
			case 0:
				message.ID = 2000
			case 1:
				message.ID = 2001
			}
			createCalls++
			return nil
		}).Times(2)
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, session *chat_entity.Session) error {
			assert.Equal(t, "pi-session-new", session.ProviderSessionID)
			assert.Equal(t, "running", session.AgentStatus)
			return nil
		}).Times(1)

	m.dbMock.ExpectBegin()
	m.dbMock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM `chat_messages` WHERE session_id = \\? AND seq >= \\? AND id NOT IN \\(\\?,\\?\\)").
		WithArgs(int64(100), 1, int64(2000), int64(2001)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	m.dbMock.ExpectExec("UPDATE `chat_sessions` SET `provider_session_id`=\\?,`agent_status`=\\?,`last_message_at`=\\?,`updatetime`=\\? WHERE id = \\? AND provider_session_id = \\?").
		WithArgs("pi-session-old", "idle", int64(99), sqlmock.AnyArg(), int64(100), "pi-session-new").
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.dbMock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id IN \\(SELECT id FROM `chat_messages` WHERE session_id = \\? AND id IN \\(\\?,\\?\\)\\)").
		WithArgs(int64(100), int64(2000), int64(2001)).
		WillReturnResult(sqlmock.NewResult(0, 4))
	m.dbMock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\? AND id IN \\(\\?,\\?\\)").
		WithArgs(int64(100), int64(2000), int64(2001)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	m.dbMock.ExpectExec("UPDATE `chat_messages` SET `session_id`=\\? WHERE session_id = \\? AND seq >= \\?").
		WithArgs(int64(100), recoverySessionID, 1).
		WillReturnResult(sqlmock.NewResult(0, 2))
	m.dbMock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id IN \\(SELECT id FROM `chat_messages` WHERE session_id = \\?\\)").
		WithArgs(recoverySessionID).
		WillReturnResult(sqlmock.NewResult(0, 3))
	m.dbMock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\?").
		WithArgs(recoverySessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.dbMock.ExpectExec("DELETE FROM `app_settings` WHERE `key` = \\?").
		WithArgs("chat.pi_recovery:100").
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.dbMock.ExpectCommit()

	resp, err := m.svc.Regenerate(m.ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})

	require.Nil(t, resp)
	require.ErrorContains(t, err, "Pi prompt rejected")
	assert.Equal(t, "pi-session-old", sess.ProviderSessionID)
	assert.Equal(t, "idle", sess.AgentStatus)
	assert.Equal(t, int64(99), sess.LastMessageAt)
	assert.Equal(t, int64(1000), originalUser.ID)
	assert.Equal(t, int64(1001), originalAssistant.ID)
	assert.NoError(t, m.dbMock.ExpectationsWereMet())
}

type piRestartResult struct {
	resp *chat_svc.SendResponse
	err  error
}

func expectCancelablePiRegenerate(m *chatMocks) (*chat_entity.Message, *chat_entity.Message) {
	expectNoPiTranscriptRecovery(m, 100)
	originalUser := &chat_entity.Message{
		ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: "pi-user-entry",
	}
	originalAssistant := &chat_entity.Message{
		ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("answer"),
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, ProviderSessionID: "pi-session-old", AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil).AnyTimes()
	m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(originalAssistant, nil).AnyTimes()
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{originalUser, originalAssistant}, nil).AnyTimes()
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil).AnyTimes()
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
	}, nil).AnyTimes()
	return originalUser, originalAssistant
}

func expectAcknowledgedPiReplacement(
	m *chatMocks,
	fromSeq int,
	userMessageID, assistantMessageID int64,
) int64 {
	expectNoPiTranscriptRecovery(m, 100)
	recoverySessionID, err := chat_repo.ReplacementRecoverySessionID(100)
	if err != nil {
		panic(err)
	}
	m.dbMock.ExpectBegin()
	expectPiRecoveryNamespaceClaim(m, recoverySessionID)
	expectPiRecoveryMarkerSave(m)
	m.dbMock.ExpectExec("UPDATE `chat_messages` SET `session_id`=\\? WHERE session_id = \\? AND seq >= \\?").
		WithArgs(recoverySessionID, int64(100), fromSeq).
		WillReturnResult(sqlmock.NewResult(0, 2))
	expectPiRecoveryMarkerSave(m)
	m.dbMock.ExpectCommit()
	// 状态翻转:按 key 点查 + 点写,不再借 chat_messages 的 model 列。
	m.dbMock.ExpectBegin()
	expectPiRecoveryMarkerLookup(m, chat_repo.ReplacementRecoveryPending, fromSeq, userMessageID, assistantMessageID)
	expectPiRecoveryMarkerSave(m)
	m.dbMock.ExpectCommit()
	m.dbMock.ExpectBegin()
	expectPiRecoveryCleanup(m, recoverySessionID)
	m.dbMock.ExpectCommit()
	createCalls := 0
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, message *chat_entity.Message) error {
			switch createCalls {
			case 0:
				message.ID = userMessageID
			case 1:
				message.ID = assistantMessageID
			}
			createCalls++
			return nil
		}).Times(2)
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	return recoverySessionID
}

// expectPiRecoveryNamespaceClaim 是申领一次替换生成的两步失败关闭检查:标记 key 未被占用,
// 且隐藏命名空间里没有残留行。
func expectPiRecoveryNamespaceClaim(m *chatMocks, recoverySessionID int64) {
	m.dbMock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
		WithArgs("chat.pi_recovery:100", 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}))
	m.dbMock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM `chat_messages` WHERE session_id = \\?").
		WithArgs(recoverySessionID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
}

// expectPiRecoveryMarkerSave 是标记的 upsert(建立与状态翻转都走它)。
func expectPiRecoveryMarkerSave(m *chatMocks) {
	m.dbMock.ExpectExec("INSERT INTO `app_settings`").
		WithArgs("chat.pi_recovery:100", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
}

// expectPiRecoveryMarkerLookup 是按 key 读回一条完整所有权的标记。
func expectPiRecoveryMarkerLookup(
	m *chatMocks,
	state chat_repo.ReplacementRecoveryState,
	fromSeq int,
	userMessageID, assistantMessageID int64,
) {
	marker := piRecoveryMarker(state, fromSeq, userMessageID, assistantMessageID)
	m.dbMock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
		WithArgs("chat.pi_recovery:100", 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}).
			AddRow(marker.Key, marker.Value, marker.Updatetime))
}

func piRecoveryMarker(
	state chat_repo.ReplacementRecoveryState,
	fromSeq int,
	userMessageID, assistantMessageID int64,
) *app_setting_entity.AppSetting {
	marker, err := chat_repo.NewReplacementRecoveryMarker(&chat_repo.ReplacementRecovery{
		SessionID:            100,
		FromSeq:              fromSeq,
		RequestMessageID:     1001,
		UserMessageID:        userMessageID,
		AssistantMessageID:   assistantMessageID,
		OldProviderSessionID: "pi-session-old",
		NewProviderSessionID: "pi-session-new",
		OldAgentStatus:       "idle",
		State:                state,
	})
	if err != nil {
		panic(err)
	}
	return marker
}

// expectPiRecoveryCleanup 是收尾清理:隐藏原始行的块行 → 隐藏原始行 → 标记。
func expectPiRecoveryCleanup(m *chatMocks, recoverySessionID int64) {
	m.dbMock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id IN \\(SELECT id FROM `chat_messages` WHERE session_id = \\?\\)").
		WithArgs(recoverySessionID).
		WillReturnResult(sqlmock.NewResult(0, 9))
	m.dbMock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\?").
		WithArgs(recoverySessionID).
		WillReturnResult(sqlmock.NewResult(0, 3))
	m.dbMock.ExpectExec("DELETE FROM `app_settings` WHERE `key` = \\?").
		WithArgs("chat.pi_recovery:100").
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectPiReplacementAndRollback(m *chatMocks) {
	const userMessageID, assistantMessageID = int64(2001), int64(2002)
	recoverySessionID, err := chat_repo.ReplacementRecoverySessionID(100)
	if err != nil {
		panic(err)
	}
	m.dbMock.ExpectBegin()
	expectPiRecoveryNamespaceClaim(m, recoverySessionID)
	expectPiRecoveryMarkerSave(m)
	m.dbMock.ExpectExec("UPDATE `chat_messages` SET `session_id`=\\? WHERE session_id = \\? AND seq >= \\?").
		WithArgs(recoverySessionID, int64(100), 1).
		WillReturnResult(sqlmock.NewResult(0, 2))
	expectPiRecoveryMarkerSave(m)
	m.dbMock.ExpectCommit()
	m.dbMock.ExpectBegin()
	m.dbMock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM `chat_messages` WHERE session_id = \\? AND seq >= \\? AND id NOT IN \\(\\?,\\?\\)").
		WithArgs(int64(100), 1, userMessageID, assistantMessageID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	m.dbMock.ExpectExec("UPDATE `chat_sessions` SET `provider_session_id`=\\?,`agent_status`=\\?,`last_message_at`=\\?,`updatetime`=\\? WHERE id = \\? AND provider_session_id = \\?").
		WithArgs("pi-session-old", "idle", int64(0), sqlmock.AnyArg(), int64(100), "pi-session-new").
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.dbMock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id IN \\(SELECT id FROM `chat_messages` WHERE session_id = \\? AND id IN \\(\\?,\\?\\)\\)").
		WithArgs(int64(100), userMessageID, assistantMessageID).
		WillReturnResult(sqlmock.NewResult(0, 4))
	m.dbMock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\? AND id IN \\(\\?,\\?\\)").
		WithArgs(int64(100), userMessageID, assistantMessageID).
		WillReturnResult(sqlmock.NewResult(0, 2))
	m.dbMock.ExpectExec("UPDATE `chat_messages` SET `session_id`=\\? WHERE session_id = \\? AND seq >= \\?").
		WithArgs(int64(100), recoverySessionID, 1).
		WillReturnResult(sqlmock.NewResult(0, 2))
	expectPiRecoveryCleanup(m, recoverySessionID)
	m.dbMock.ExpectCommit()

	createCalls := 0
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, message *chat_entity.Message) error {
			message.ID = userMessageID + int64(createCalls)
			createCalls++
			return nil
		}).Times(2)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Times(1)
}

func startPiRegenerate(svc chat_svc.ChatSvc, ctx context.Context) <-chan piRestartResult {
	resultC := make(chan piRestartResult, 1)
	go func() {
		resp, err := svc.Regenerate(ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
		resultC <- piRestartResult{resp: resp, err: err}
	}()
	return resultC
}

func TestPiRestart_CancellationStopsSynchronousPreflightBeforeTranscriptMutation(t *testing.T) {
	tests := []struct {
		name          string
		cancelRequest bool
	}{
		{name: "Given Stop during Pi preflight, then startup is canceled before transcript mutation"},
		{name: "Given request cancellation during Pi preflight, then startup is canceled before transcript mutation", cancelRequest: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := setupChatTest(t)
			runner := &blockingPiPreflightRunner{entered: make(chan struct{})}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
			t.Cleanup(restore)
			expectCancelablePiRegenerate(m)

			requestCtx, cancelRequest := context.WithCancel(m.ctx)
			defer cancelRequest()
			resultC := startPiRegenerate(m.svc, requestCtx)
			select {
			case <-runner.entered:
			case <-time.After(time.Second):
				t.Fatal("Pi preflight did not start")
			}
			if tt.cancelRequest {
				cancelRequest()
			} else {
				stopResp, stopErr := m.svc.Stop(m.ctx, &chat_svc.StopRequest{SessionID: 100})
				require.NoError(t, stopErr)
				require.NotNil(t, stopResp)
				assert.True(t, stopResp.Stopped)
			}

			var result piRestartResult
			select {
			case result = <-resultC:
			case <-time.After(200 * time.Millisecond):
				t.Error("cancellation did not reach synchronous Pi preflight")
				result = <-resultC
			}
			assert.Nil(t, result.resp)
			require.ErrorIs(t, result.err, context.Canceled)

			expectNoPiTranscriptRecovery(m, 100)
			secondResp, secondErr := m.svc.Regenerate(m.ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
			assert.Nil(t, secondResp)
			require.ErrorContains(t, secondErr, "second Pi preflight reached",
				"the per-session lock must be released after canceled preflight")
		})
	}
}

func TestPiRestart_StopCancelsActivationSQLBeforeDoingStopLookups(t *testing.T) {
	m := setupChatTest(t)
	expectNoPiTranscriptRecovery(m, 100)
	activationEntered := make(chan struct{})
	restartDone := make(chan struct{})
	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, ProviderSessionID: "pi-session-old", AgentStatus: "idle", Status: consts.ACTIVE,
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.session.EXPECT().Find(gomock.Any(), int64(100)).DoAndReturn(
		func(context.Context, int64) (*chat_entity.Session, error) {
			<-restartDone
			return sess, nil
		})
	originalAssistant := &chat_entity.Message{
		ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("answer"),
	}
	m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(originalAssistant, nil)
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
		{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: "pi-user-entry"},
		originalAssistant,
	}, nil).AnyTimes()
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil).AnyTimes()
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
	}, nil).AnyTimes()

	runner := &preparedAcknowledgedRunner{providerSessionID: "pi-session-new"}
	runner.onStart = func() { t.Error("Start must not run after activation SQL is canceled") }
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
	t.Cleanup(restore)

	m.dbMock.ExpectBegin()
	expectPiRecoveryNamespaceClaim(m, -201)
	expectPiRecoveryMarkerSave(m)
	m.dbMock.ExpectExec("UPDATE `chat_messages` SET `session_id`=\\? WHERE session_id = \\? AND seq >= \\?").
		WithArgs(int64(-201), int64(100), 1).
		WillReturnResult(sqlmock.NewResult(0, 2))
	m.dbMock.ExpectRollback()
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(writeCtx context.Context, message *chat_entity.Message) error {
			message.ID = 2000
			close(activationEntered)
			<-writeCtx.Done()
			return writeCtx.Err()
		}).Times(1)

	resultC := startPiRegenerate(m.svc, m.ctx)
	select {
	case <-activationEntered:
	case <-time.After(time.Second):
		t.Fatal("Pi activation transaction did not start")
	}
	stopResult := make(chan error, 1)
	go func() {
		_, err := m.svc.Stop(m.ctx, &chat_svc.StopRequest{SessionID: 100})
		stopResult <- err
	}()

	select {
	case result := <-resultC:
		close(restartDone)
		require.Nil(t, result.resp)
		require.Error(t, result.err)
	case <-time.After(200 * time.Millisecond):
		close(restartDone)
		t.Fatal("Stop did not cancel the registered activation SQL before its own repository lookups")
	}
	select {
	case stopErr := <-stopResult:
		require.NoError(t, stopErr)
	case <-time.After(time.Second):
		t.Fatal("Stop did not finish after activation cancellation")
	}
	assert.NoError(t, m.dbMock.ExpectationsWereMet())
}

func TestPiRestart_CancellationDuringPreparedStartRestoresTranscript(t *testing.T) {
	tests := []struct {
		name          string
		cancelRequest bool
	}{
		{name: "Given Stop during post-commit Pi prepared start, then prompt is withheld and transcript is restored"},
		{name: "Given request cancellation during post-commit Pi prepared start, then prompt is withheld and transcript is restored", cancelRequest: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := setupChatTest(t)
			runner := &blockingPiPreparedStartRunner{
				startEntered: make(chan struct{}),
				closed:       make(chan struct{}),
			}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
			t.Cleanup(restore)
			expectCancelablePiRegenerate(m)
			expectPiReplacementAndRollback(m)

			requestCtx, cancelRequest := context.WithCancel(m.ctx)
			defer cancelRequest()
			resultC := startPiRegenerate(m.svc, requestCtx)
			select {
			case <-runner.startEntered:
			case <-time.After(time.Second):
				t.Fatal("Pi prepared start did not begin after transcript commit")
			}
			if tt.cancelRequest {
				cancelRequest()
			} else {
				stopResp, stopErr := m.svc.Stop(m.ctx, &chat_svc.StopRequest{SessionID: 100})
				require.NoError(t, stopErr)
				require.NotNil(t, stopResp)
				assert.True(t, stopResp.Stopped)
			}

			var result piRestartResult
			select {
			case result = <-resultC:
			case <-time.After(200 * time.Millisecond):
				t.Error("cancellation did not stop post-commit Pi prepared start")
				result = <-resultC
			}
			assert.Nil(t, result.resp)
			require.ErrorIs(t, result.err, context.Canceled)
			assert.Zero(t, runner.PromptCalls(), "cancellation before prompt must not start the replacement turn")
			select {
			case <-runner.closed:
			case <-time.After(time.Second):
				t.Fatal("canceled prepared Pi process was not released")
			}
			expectNoPiTranscriptRecovery(m, 100)
			secondResp, secondErr := m.svc.Regenerate(m.ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
			assert.Nil(t, secondResp)
			require.ErrorContains(t, secondErr, "second Pi preflight reached",
				"the per-session lock must be released after canceled prepared start")
		})
	}
}

func TestPiRestart_RejectsEmptyAnchorBeforeTruncationOrRunnerStart(t *testing.T) {
	tests := []struct {
		name    string
		anchor  string
		arrange func(*chatMocks, string)
		invoke  func(chat_svc.ChatSvc, context.Context) (*chat_svc.SendResponse, error)
	}{
		{
			name: "Given an old Pi assistant reply whose user anchor is empty, when Regenerate runs, then it fails without truncating or starting the runner",
			arrange: func(m *chatMocks, anchor string) {
				m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(&chat_entity.Message{
					ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1"),
				}, nil)
				m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
					{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: anchor},
					{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1")},
				}, nil)
			},
			invoke: func(svc chat_svc.ChatSvc, ctx context.Context) (*chat_svc.SendResponse, error) {
				return svc.Regenerate(ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
			},
		},
		{
			name:   "Given an old Pi assistant reply whose user anchor is whitespace, when Regenerate runs, then it fails without truncating or sending a no-fork prompt",
			anchor: " \t\n ",
			arrange: func(m *chatMocks, anchor string) {
				m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(&chat_entity.Message{
					ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1"),
				}, nil)
				m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
					{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: anchor},
					{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1")},
				}, nil)
			},
			invoke: func(svc chat_svc.ChatSvc, ctx context.Context) (*chat_svc.SendResponse, error) {
				return svc.Regenerate(ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
			},
		},
		{
			name:   "Given an old Pi assistant reply whose anchor is padded, when Regenerate runs, then it rejects the opaque ID without rewriting it",
			anchor: " pi-user-entry ",
			arrange: func(m *chatMocks, anchor string) {
				m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(&chat_entity.Message{
					ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1"),
				}, nil)
				m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
					{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: anchor},
					{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1")},
				}, nil)
			},
			invoke: func(svc chat_svc.ChatSvc, ctx context.Context) (*chat_svc.SendResponse, error) {
				return svc.Regenerate(ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
			},
		},
		{
			name: "Given an old Pi user message whose anchor is empty, when Edit runs, then it fails without truncating or starting the runner",
			arrange: func(m *chatMocks, anchor string) {
				m.message.EXPECT().Find(gomock.Any(), int64(1000)).Return(&chat_entity.Message{
					ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: anchor,
				}, nil)
			},
			invoke: func(svc chat_svc.ChatSvc, ctx context.Context) (*chat_svc.SendResponse, error) {
				return svc.Edit(ctx, &chat_svc.EditRequest{SessionID: 100, MessageID: 1000, Text: "replacement"})
			},
		},
		{
			name:   "Given an old Pi user message whose anchor is padded, when Edit runs, then it rejects the opaque ID without rewriting it",
			anchor: "\tpi-user-entry\n",
			arrange: func(m *chatMocks, anchor string) {
				m.message.EXPECT().Find(gomock.Any(), int64(1000)).Return(&chat_entity.Message{
					ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: anchor,
				}, nil)
			},
			invoke: func(svc chat_svc.ChatSvc, ctx context.Context) (*chat_svc.SendResponse, error) {
				return svc.Edit(ctx, &chat_svc.EditRequest{SessionID: 100, MessageID: 1000, Text: "replacement"})
			},
		},
		{
			name:   "Given an old Pi user message whose anchor is whitespace, when Edit runs, then it fails without truncating or sending a no-fork prompt",
			anchor: " \r\n ",
			arrange: func(m *chatMocks, anchor string) {
				m.message.EXPECT().Find(gomock.Any(), int64(1000)).Return(&chat_entity.Message{
					ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: anchor,
				}, nil)
			},
			invoke: func(svc chat_svc.ChatSvc, ctx context.Context) (*chat_svc.SendResponse, error) {
				return svc.Edit(ctx, &chat_svc.EditRequest{SessionID: 100, MessageID: 1000, Text: "replacement"})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := setupChatTest(t)
			runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
			t.Cleanup(restore)
			sess := &chat_entity.Session{
				ID: 100, AgentID: 7, ProviderSessionID: "pi-session-old", AgentStatus: "idle", Status: consts.ACTIVE,
			}

			m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
			tc.arrange(m, tc.anchor)
			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
				ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
			}, nil)
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
			}, nil)
			expectNoPiTranscriptRecovery(m, 100)

			// Intentionally omit transaction, DeleteFromSeq, and persistence expectations:
			// any history mutation before the anchor rejection is an immediate mock failure.
			resp, err := tc.invoke(m.svc, m.ctx)
			require.Nil(t, resp)
			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, code.ChatRegenerateNoUserAnchor, httpErr.Code)
			assert.Equal(t, "pi-session-old", sess.ProviderSessionID)

			select {
			case <-runner.requests:
				t.Fatal("Pi runtime started despite the missing anchor")
			case <-time.After(50 * time.Millisecond):
			}
		})
	}
}

func TestPiRestart_LostProviderSessionFailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		arrange  func(*chatMocks)
		invoke   func(chat_svc.ChatSvc, context.Context) (*chat_svc.SendResponse, error)
		wantCode int
	}{
		{
			name: "Given an established Pi assistant turn whose provider session ID was lost, when Regenerate runs, then it fails closed without restarting blank",
			arrange: func(m *chatMocks) {
				m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(&chat_entity.Message{
					ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("answer"),
				}, nil)
				m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
					{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: "pi-user-entry"},
					{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("answer")},
				}, nil).AnyTimes()
			},
			invoke: func(svc chat_svc.ChatSvc, ctx context.Context) (*chat_svc.SendResponse, error) {
				return svc.Regenerate(ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
			},
			wantCode: code.ChatProviderSessionGone,
		},
		{
			name: "Given Pi accepted a first prompt before failing but its provider session ID and anchor were lost, when Regenerate runs, then it does not treat that partial turn as a startup failure",
			arrange: func(m *chatMocks) {
				m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(&chat_entity.Message{
					ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("partial answer"), ErrorText: "provider failed",
				}, nil)
				m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
					{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original")},
					{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("partial answer"), ErrorText: "provider failed"},
				}, nil).AnyTimes()
			},
			invoke: func(svc chat_svc.ChatSvc, ctx context.Context) (*chat_svc.SendResponse, error) {
				return svc.Regenerate(ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
			},
			wantCode: code.ChatProviderSessionGone,
		},
		{
			name: "Given a failed first Pi turn whose assistant blocks are malformed, when Regenerate runs, then it reports the malformed transcript without restarting blank",
			arrange: func(m *chatMocks) {
				m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(&chat_entity.Message{
					ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: "{", ErrorText: "startup failed",
				}, nil)
				m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
					{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original")},
					{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: "{", ErrorText: "startup failed"},
				}, nil).AnyTimes()
			},
			invoke: func(svc chat_svc.ChatSvc, ctx context.Context) (*chat_svc.SendResponse, error) {
				return svc.Regenerate(ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
			},
			wantCode: code.ChatBlocksMalformed,
		},
		{
			name: "Given an established Pi user turn whose provider session ID was lost, when Edit runs, then it fails closed without restarting blank",
			arrange: func(m *chatMocks) {
				m.message.EXPECT().Find(gomock.Any(), int64(1000)).Return(&chat_entity.Message{
					ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("original"), ForkAnchor: "pi-user-entry",
				}, nil)
			},
			invoke: func(svc chat_svc.ChatSvc, ctx context.Context) (*chat_svc.SendResponse, error) {
				return svc.Edit(ctx, &chat_svc.EditRequest{SessionID: 100, MessageID: 1000, Text: "replacement"})
			},
			wantCode: code.ChatProviderSessionGone,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := setupChatTest(t)
			runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
			t.Cleanup(restore)

			m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
				ID: 100, AgentID: 7, AgentStatus: "error", Status: consts.ACTIVE,
			}, nil)
			tc.arrange(m)
			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
				ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
			}, nil)
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
			}, nil)
			expectNoPiTranscriptRecovery(m, 100)

			resp, err := tc.invoke(m.svc, m.ctx)
			if resp != nil {
				chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)
			}
			require.Nil(t, resp)
			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, tc.wantCode, httpErr.Code)

			select {
			case req := <-runner.requests:
				t.Fatalf("Pi runtime restarted without its established provider session: %+v", req)
			default:
			}
		})
	}
}

func TestPiRestart_FailedFirstTurnCanRetryWithoutFork(t *testing.T) {
	m := setupChatTest(t)
	runner := &preparedRecordingPiRunner{
		requests:          make(chan agentruntime.RunRequest, 1),
		providerSessionID: "pi-session-new",
	}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
	t.Cleanup(restore)

	failedUser := &chat_entity.Message{
		ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("retry me"),
	}
	failedAssistant := &chat_entity.Message{
		ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: "[]", ErrorText: "startup failed",
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "error", Status: consts.ACTIVE,
	}, nil)
	m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(failedAssistant, nil)
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{failedUser, failedAssistant}, nil).AnyTimes()
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
	}, nil)
	expectAcknowledgedPiReplacement(m, 1, 2000, 2001)

	resp, err := m.svc.Regenerate(m.ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
	require.NoError(t, err)
	require.NotNil(t, resp)
	select {
	case req := <-runner.requests:
		assert.Empty(t, req.ProviderSessionID)
		assert.Empty(t, req.ForkAnchor)
		assert.Equal(t, "retry me", req.UserText)
	case <-time.After(2 * time.Second):
		t.Fatal("Pi failed-first-turn retry never reached the runtime")
	}
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)
}

func TestRegenerate_ClaudeCodeForksViaAnchor(t *testing.T) {
	convey.Convey("Regenerate(claudecode) 走 ForkAnchor 路径，把 user msg 的 ForkAnchor 透传给 runner", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		// session 有：seq1 user "hi"（ForkAnchor 已经在上一轮被写到 "anchor-uuid"），seq2 assistant "v1"
		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
			ID: 100, AgentID: 7, ProviderSessionID: "cc-old", AgentStatus: "idle", Status: consts.ACTIVE,
		}, nil)
		m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(&chat_entity.Message{
			ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1"),
		}, nil)
		m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
			{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("hi"), ForkAnchor: "anchor-uuid"},
			{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1")},
		}, nil).AnyTimes()
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
			ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
		}, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
		}, nil)
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		m.dbMock.ExpectBegin()
		m.message.EXPECT().DeleteFromSeq(gomock.Any(), int64(100), 1).Return(int64(2), nil)
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
		newIDs := []int64{2000, 2001}
		var calls int
		m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				msg.ID = newIDs[calls]
				calls++
				return nil
			}).Times(2)
		m.dbMock.ExpectCommit()
		m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		resp, err := m.svc.Regenerate(ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
		assert.NoError(t, err)

		select {
		case req := <-runner.requests:
			assert.Equal(t, "hi", req.UserText, "重新生成必须用原 user 消息的文本重发")
			assert.Equal(t, "anchor-uuid", req.ForkAnchor, "user msg 的 ForkAnchor 必须透传到 runner")
			assert.Equal(t, "cc-old", req.ProviderSessionID, "原 provider session id 透传，runner 会做 fork")
		case <-time.After(2 * time.Second):
			t.Fatal("runtime never received the regenerated turn")
		}
		chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)
	})
}

// failedForkRunner 复刻 CLI fork 失败的现场:`--resume <old> --resume-session-at <anchor>
// --fork-session` 里锚点不在 CLI 加载的那条分支上时,CLI 照样铸出一个新 session id,
// 但那一轮当场以 error 收场、那个 id 在磁盘上从来没有过转录文件。
type failedForkRunner struct {
	requests chan agentruntime.RunRequest
}

func (*failedForkRunner) Capabilities() capability.Capabilities {
	return (&recordingRunner{}).Capabilities()
}

func (r *failedForkRunner) Run(_ context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	r.requests <- req
	// 与真 claudecode runtime 同序:开跑时 result 里还是**旧** id(handle.ID() 就是
	// --resume 的那个),CLI 新铸的 id 与 StopErr 要等帧流走完、close(out) 之前才写进
	// result。第一条事件走无缓冲 channel,它发得出去就说明 runTurn 已经过了
	// attachRuntime 那次读,后面的写与那次读之间有 happens-before,不构成竞态。
	result := &agentruntime.RunResult{ProviderSessionID: req.ProviderSessionID}
	events := make(chan agentruntime.Event)
	go func() {
		events <- agentruntime.TextDelta{Text: ""}
		result.ProviderSessionID = "cc-ghost"
		result.StopErr = errors.New("No message found with message.uuid of: anchor-uuid")
		close(events)
	}()
	return events, result, nil
}

// TestRegenerate_ClaudeCodeFailedForkKeepsOldProviderSession 钉住 fork 失败时的会话归属。
//
// 现场(2026-08-31 sess-3509):fork 报 "No message found with message.uuid" 当场收场,
// chat_svc 照旧把 result 里那个新 id 认领进 session.provider_session_id —— 从此这个会话
// 指着一个磁盘上不存在的 CLI 会话,下一轮 --resume 必然 "No conversation found",
// 整段对话的上下文就此丢掉。fork 失败 = 源会话原样健在(--fork-session 不改源),
// 唯一正确的落点是保留旧 id。
func TestRegenerate_ClaudeCodeFailedForkKeepsOldProviderSession(t *testing.T) {
	convey.Convey("Given fork 轮以错误收场, When turn 收口, Then 不认领 CLI 新铸的 session id", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		runner := &failedForkRunner{requests: make(chan agentruntime.RunRequest, 1)}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		sess := &chat_entity.Session{
			ID: 100, AgentID: 7, ProviderSessionID: "cc-old", AgentStatus: "idle", Status: consts.ACTIVE,
		}
		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
		m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(&chat_entity.Message{
			ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1"),
		}, nil)
		m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
			{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("hi"), ForkAnchor: "anchor-uuid"},
			{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1")},
		}, nil).AnyTimes()
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
			ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
		}, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
		}, nil)

		var mu sync.Mutex
		var persisted []string
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
				mu.Lock()
				persisted = append(persisted, s.ProviderSessionID)
				mu.Unlock()
				return nil
			}).AnyTimes()

		m.dbMock.ExpectBegin()
		m.message.EXPECT().DeleteFromSeq(gomock.Any(), int64(100), 1).Return(int64(2), nil)
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
		newIDs := []int64{2000, 2001}
		var calls int
		m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				msg.ID = newIDs[calls]
				calls++
				return nil
			}).Times(2)
		m.dbMock.ExpectCommit()
		m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		resp, err := m.svc.Regenerate(ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
		require.NoError(t, err)

		select {
		case req := <-runner.requests:
			assert.Equal(t, "anchor-uuid", req.ForkAnchor)
		case <-time.After(2 * time.Second):
			t.Fatal("runtime never received the regenerated turn")
		}
		chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

		mu.Lock()
		defer mu.Unlock()
		assert.NotContains(t, persisted, "cc-ghost",
			"fork 失败铸出来的 session id 从来没有转录文件，认领它等于把整段对话指丢")
		assert.Equal(t, "cc-old", sess.ProviderSessionID, "源会话原样健在，必须继续指着它")
	})
}

func TestRegenerate_ClaudeCodeWithoutAnchorDropsSession(t *testing.T) {
	convey.Convey("Regenerate(claudecode) 首轮 user msg 没 anchor → 丢 session 当全新 turn 处理", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		// 首轮 user msg ForkAnchor 为空（在 JSONL 里 parentUuid=null）。
		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
			ID: 100, AgentID: 7, ProviderSessionID: "cc-old", AgentStatus: "idle", Status: consts.ACTIVE,
		}, nil)
		m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(&chat_entity.Message{
			ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1"),
		}, nil)
		m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
			{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("hi") /* ForkAnchor: "" */},
			{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1")},
		}, nil).AnyTimes()
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
			ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
		}, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
		}, nil)
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		m.dbMock.ExpectBegin()
		m.message.EXPECT().DeleteFromSeq(gomock.Any(), int64(100), 1).Return(int64(2), nil)
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
		newIDs := []int64{2000, 2001}
		var calls int
		m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				msg.ID = newIDs[calls]
				calls++
				return nil
			}).Times(2)
		m.dbMock.ExpectCommit()
		m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		resp, err := m.svc.Regenerate(ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1001})
		assert.NoError(t, err)

		select {
		case req := <-runner.requests:
			assert.Empty(t, req.ForkAnchor, "首轮无 anchor，不传 ForkAnchor")
			assert.Empty(t, req.ProviderSessionID, "首轮 anchor 缺失 → provider session 被丢弃，runner 创建新会话")
		case <-time.After(2 * time.Second):
			t.Fatal("runtime never received the regenerated turn")
		}
		chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)
	})
}

func TestRegenerate_RejectsNonAssistantTarget(t *testing.T) {
	convey.Convey("目标是 user 消息 → ChatRegenerateNotAssistant", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
			ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
		}, nil)
		m.message.EXPECT().Find(gomock.Any(), int64(1000)).Return(&chat_entity.Message{
			ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("hi"),
		}, nil)

		_, err := m.svc.Regenerate(ctx, &chat_svc.RegenerateRequest{SessionID: 100, MessageID: 1000})
		assert.Error(t, err)
	})
}

// TestEdit_BuiltinTruncatesAndReplaysNewText 编辑历史 user 消息 → 截到该 user
// （含）后用 NEW 文本重跑。和 Regenerate 的差别是 user 文本被替换。
func TestEdit_BuiltinTruncatesAndReplaysNewText(t *testing.T) {
	convey.Convey("Edit(builtin) 用新文本替换历史 user 后重跑 turn", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
		t.Cleanup(restore)

		// session 已经有：seq1 user "hi", seq2 assistant "v1"。
		// 编辑 user (id=1000) 为 "yo"，期望删 seq>=1，并以 "yo" 重新跑一轮。
		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
			ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
		}, nil)
		m.message.EXPECT().Find(gomock.Any(), int64(1000)).Return(&chat_entity.Message{
			ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("hi"),
		}, nil)
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
			ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
		}, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
		}, nil)
		m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
		expectProviderResolvable(m, "key-21")
		m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{}, nil).AnyTimes()
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		m.dbMock.ExpectBegin()
		m.message.EXPECT().DeleteFromSeq(gomock.Any(), int64(100), 1).Return(int64(2), nil)
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
		newIDs := []int64{2000, 2001}
		var calls int
		m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				msg.ID = newIDs[calls]
				calls++
				return nil
			}).Times(2)
		m.dbMock.ExpectCommit()
		m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		resp, err := m.svc.Edit(ctx, &chat_svc.EditRequest{SessionID: 100, MessageID: 1000, Text: "yo"})
		assert.NoError(t, err)
		assert.Equal(t, int64(100), resp.SessionID)
		assert.NotZero(t, resp.AssistantMessageID)

		select {
		case req := <-runner.requests:
			assert.Equal(t, "yo", req.UserText, "Edit 必须用 NEW 文本而不是原文回放")
		case <-time.After(2 * time.Second):
			t.Fatal("runtime never received the edited turn")
		}
	})
}

func TestEdit_CodexRollsBackProviderTurns(t *testing.T) {
	convey.Convey("Edit(codex) 按被编辑 user 到末尾的 turn 数生成 rollback anchor", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeCodex, runner)
		t.Cleanup(restore)

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
			ID: 100, AgentID: 7, ProviderSessionID: "cx-abc", AgentStatus: "idle", Status: consts.ACTIVE,
		}, nil)
		m.message.EXPECT().Find(gomock.Any(), int64(1002)).Return(&chat_entity.Message{
			ID: 1002, SessionID: 100, Role: "user", Seq: 3, BlocksJSON: encodeText("second"),
		}, nil)
		m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
			{ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("first")},
			{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1")},
			{ID: 1002, SessionID: 100, Role: "user", Seq: 3, BlocksJSON: encodeText("second")},
			{ID: 1003, SessionID: 100, Role: "assistant", Seq: 4, BlocksJSON: encodeText("v2")},
		}, nil).AnyTimes()
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
			ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
		}, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeCodex), Status: consts.ACTIVE,
		}, nil)
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		m.dbMock.ExpectBegin()
		m.message.EXPECT().DeleteFromSeq(gomock.Any(), int64(100), 3).Return(int64(2), nil)
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil)
		newIDs := []int64{2000, 2001}
		var calls int
		m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				msg.ID = newIDs[calls]
				calls++
				return nil
			}).Times(2)
		m.dbMock.ExpectCommit()
		m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		_, err := m.svc.Edit(ctx, &chat_svc.EditRequest{SessionID: 100, MessageID: 1002, Text: "second edited"})
		assert.NoError(t, err)

		select {
		case req := <-runner.requests:
			assert.Equal(t, "second edited", req.UserText)
			assert.Equal(t, "cx-abc", req.ProviderSessionID)
			assert.Equal(t, "1", req.ForkAnchor, "只需要 rollback 被编辑的最后一轮")
		case <-time.After(2 * time.Second):
			t.Fatal("runtime never received the edited codex turn")
		}
	})
}

// TestEdit_ClaudeCodeForksViaTargetAnchor claudecode 编辑：直接取 target.ForkAnchor
// 当 forkAnchor，不需要先找 user anchor。
func TestEdit_ClaudeCodeForksViaTargetAnchor(t *testing.T) {
	convey.Convey("Edit(claudecode) target.ForkAnchor 透传到 RunRequest.ForkAnchor", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
			ID: 100, AgentID: 7, ProviderSessionID: "cc-old", AgentStatus: "idle", Status: consts.ACTIVE,
		}, nil)
		m.message.EXPECT().Find(gomock.Any(), int64(1000)).Return(&chat_entity.Message{
			ID: 1000, SessionID: 100, Role: "user", Seq: 1, BlocksJSON: encodeText("看看目录"),
			ForkAnchor: "anchor-uuid",
		}, nil)
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
			ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
		}, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
		}, nil)
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		m.dbMock.ExpectBegin()
		m.message.EXPECT().DeleteFromSeq(gomock.Any(), int64(100), 1).Return(int64(2), nil)
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
		newIDs := []int64{2000, 2001}
		var calls int
		m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				msg.ID = newIDs[calls]
				calls++
				return nil
			}).Times(2)
		m.dbMock.ExpectCommit()
		m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		_, err := m.svc.Edit(ctx, &chat_svc.EditRequest{SessionID: 100, MessageID: 1000, Text: "看看xx目录"})
		assert.NoError(t, err)

		select {
		case req := <-runner.requests:
			assert.Equal(t, "看看xx目录", req.UserText, "新文本必须透传")
			assert.Equal(t, "anchor-uuid", req.ForkAnchor, "target.ForkAnchor 即 forkAnchor")
			assert.Equal(t, "cc-old", req.ProviderSessionID)
		case <-time.After(2 * time.Second):
			t.Fatal("runtime never received the edited turn")
		}
	})
}

func TestEdit_RejectsNonUserTarget(t *testing.T) {
	convey.Convey("Edit 目标是 assistant 消息 → ChatEditNotUser", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
			ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
		}, nil)
		m.message.EXPECT().Find(gomock.Any(), int64(1001)).Return(&chat_entity.Message{
			ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: encodeText("v1"),
		}, nil)

		_, err := m.svc.Edit(ctx, &chat_svc.EditRequest{SessionID: 100, MessageID: 1001, Text: "new"})
		assert.Error(t, err)
	})
}

func TestEdit_RejectsEmptyText(t *testing.T) {
	convey.Convey("Edit 文本去空白后为空 → InvalidParameter（不应进 DB / runtime）", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		_, err := m.svc.Edit(ctx, &chat_svc.EditRequest{SessionID: 100, MessageID: 1000, Text: "   "})
		assert.Error(t, err)
	})
}

// fakePermissionRunner 实现 BackendRunner + PermissionModeSetter，让
// SetPermissionMode 测试可以注入 runtime 行为（成功 / NoActive / 其它 err）。
type fakePermissionRunner struct {
	setMode   string
	setSID    int64
	setErr    error
	setCalled bool
}

// Capabilities 返与生产 claudecode runtime 一致的 PermissionModeMeta;
// SetPermissionMode 重构后按 meta 判定支持与可切性,fake 必须给出对应值。
func (*fakePermissionRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		PermissionModeMeta: capability.PermissionModeMeta{
			AllowedModes:         []string{"default", "acceptEdits", "plan", "bypassPermissions"},
			DefaultMode:          "acceptEdits",
			SwitchableDuringTurn: true,
		},
	}
}
func (r *fakePermissionRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	ch := make(chan agentruntime.Event)
	close(ch)
	return ch, &agentruntime.RunResult{}, nil
}

func (r *fakePermissionRunner) SetPermissionMode(_ context.Context, sessionID int64, mode string) error {
	r.setCalled = true
	r.setSID = sessionID
	r.setMode = mode
	return r.setErr
}

// fakeSteerableRunner 实现 BackendRunner + Steerer (+ SteerCanceler when
// the test sets cancelable=true)，让 Enqueue / CancelQueued 测试不依赖
// Send 路径。
type fakeSteerableRunner struct {
	// Steer 捕获
	steerText string
	steerID   string
	steerErr  error

	// CancelSteer 捕获（仅在 fakeCancelableRunner 上才会被读到）
	cancelGotID string
}

func (*fakeSteerableRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{}
}
func (r *fakeSteerableRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	ch := make(chan agentruntime.Event)
	close(ch)
	return ch, &agentruntime.RunResult{}, nil
}

func (r *fakeSteerableRunner) Steer(_ context.Context, _ int64, queuedID, text string) error {
	r.steerID = queuedID
	r.steerText = text
	return r.steerErr
}

// fakeCancelableRunner 是 fakeSteerableRunner 的超集：额外实现 SteerCanceler
// 让 chat_svc 的类型断言通过，验证 Cancellable=true / CancelQueued 转发路径。
type fakeCancelableRunner struct {
	fakeSteerableRunner

	// CancelSteer 返回值/错误注入；与基类的 cancelGotID 在同一对象上读写。
	cancelRemove []string
	cancelErr    error
}

func (r *fakeCancelableRunner) CancelSteer(_ context.Context, _ int64, queuedID string) ([]string, error) {
	r.cancelGotID = queuedID
	return r.cancelRemove, r.cancelErr
}

// nonSteerableRunner 只实现 BackendRunner，不实现 Steerer。用于验证
// Enqueue 在 type-assertion 失败时返回 ChatSteerUnsupported。
type nonSteerableRunner struct{}

func (nonSteerableRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{}
}
func (nonSteerableRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	ch := make(chan agentruntime.Event)
	close(ch)
	return ch, &agentruntime.RunResult{}, nil
}

func TestEnqueue_RoutesToSteerer(t *testing.T) {
	convey.Convey("Enqueue 转发到 backend runner 的 Steer", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		chat_svc.RegisterGateway(&fakeChatGateway{
			status: httpgateway.GatewayStatus{State: "running", URL: "http://127.0.0.1:60080"},
		})
		t.Cleanup(func() { chat_svc.RegisterGateway(nil) })

		runner := &fakeSteerableRunner{}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
			ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
		}, nil)
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
			ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE,
		}, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "key-21", Status: consts.ACTIVE,
		}, nil)
		m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
		expectProviderResolvable(m, "key-21")

		resp, err := m.svc.Enqueue(ctx, &chat_svc.EnqueueRequest{SessionID: 100, Text: "wait"})
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.True(t, resp.Queued)
		assert.NotEmpty(t, resp.QueuedID, "Enqueue should generate a queuedID")
		assert.Equal(t, resp.QueuedID, runner.steerID, "queuedID should be passed to runner.Steer")
		assert.Equal(t, "wait", runner.steerText)
		// fakeSteerableRunner does not implement SteerCanceler.
		assert.False(t, resp.Cancellable)
	})
}

func TestEnqueue_CancellableTrueWhenRunnerImplementsSteerCanceler(t *testing.T) {
	convey.Convey("runner 实现 SteerCanceler → EnqueueResponse.Cancellable=true", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		chat_svc.RegisterGateway(&fakeChatGateway{
			status: httpgateway.GatewayStatus{State: "running", URL: "http://127.0.0.1:60080"},
		})
		t.Cleanup(func() { chat_svc.RegisterGateway(nil) })

		runner := &fakeCancelableRunner{}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
			ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
		}, nil)
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
			ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE,
		}, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "key-21", Status: consts.ACTIVE,
		}, nil)
		m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
		expectProviderResolvable(m, "key-21")

		resp, err := m.svc.Enqueue(ctx, &chat_svc.EnqueueRequest{SessionID: 100, Text: "wait"})
		assert.NoError(t, err)
		assert.True(t, resp.Cancellable)
	})
}

func TestEnqueue_NoActiveTurn(t *testing.T) {
	convey.Convey("runner.Steer 返 ErrNoActiveTurn → ChatSteerNoActive", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		chat_svc.RegisterGateway(&fakeChatGateway{
			status: httpgateway.GatewayStatus{State: "running", URL: "http://127.0.0.1:60080"},
		})
		t.Cleanup(func() { chat_svc.RegisterGateway(nil) })

		runner := &fakeSteerableRunner{steerErr: agentruntime.ErrNoActiveTurn}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
			ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
		}, nil)
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
			ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE,
		}, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "key-21", Status: consts.ACTIVE,
		}, nil)
		m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
		expectProviderResolvable(m, "key-21")

		_, err := m.svc.Enqueue(ctx, &chat_svc.EnqueueRequest{SessionID: 100, Text: "wait"})
		assert.Error(t, err)
	})
}

func TestEnqueue_BackendNotSteerer(t *testing.T) {
	convey.Convey("runner 不实现 Steerer → ChatSteerUnsupported", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		// 临时把 builtin 替换成只实现 BackendRunner 的 fake，验证
		// type-assertion 失败时 Enqueue 返回错误。
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, nonSteerableRunner{})
		t.Cleanup(restore)

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
			ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
		}, nil)
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
			ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE,
		}, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
		}, nil)
		m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
		expectProviderResolvable(m, "key-21")

		_, err := m.svc.Enqueue(ctx, &chat_svc.EnqueueRequest{SessionID: 100, Text: "wait"})
		assert.Error(t, err)
	})
}

func TestEnqueue_RejectsEmptyText(t *testing.T) {
	convey.Convey("Enqueue 文本去空后为空 → InvalidParameter", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		_, err := m.svc.Enqueue(ctx, &chat_svc.EnqueueRequest{SessionID: 100, Text: "   "})
		assert.Error(t, err)
	})
}

func TestEnqueue_SessionNotFound(t *testing.T) {
	convey.Convey("Enqueue 找不到 session → ChatSessionNotFound", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx
		m.session.EXPECT().Find(gomock.Any(), int64(999)).Return(nil, nil)

		_, err := m.svc.Enqueue(ctx, &chat_svc.EnqueueRequest{SessionID: 999, Text: "hi"})
		assert.Error(t, err)
	})
}

// cancelQueuedFixture 复用 Enqueue 测试的 happy-path 数据填充。每个 CancelQueued
// 用例都要 Find session → Find agent → Find backend → Find provider，抽出来让
// 用例只关注「runner 行为差异」。
func cancelQueuedFixture(t *testing.T) (m *chatMocks, ctx context.Context) {
	t.Helper()
	m = setupChatTest(t)
	ctx = m.ctx
	chat_svc.RegisterGateway(&fakeChatGateway{
		status: httpgateway.GatewayStatus{State: "running", URL: "http://127.0.0.1:60080"},
	})
	t.Cleanup(func() { chat_svc.RegisterGateway(nil) })

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")
	return m, ctx
}

func TestCancelQueued_HitForwardsToCanceler(t *testing.T) {
	convey.Convey("CancelQueued 按 ID 命中 → 返 Removed 列表", t, func() {
		m, ctx := cancelQueuedFixture(t)
		runner := &fakeCancelableRunner{cancelRemove: []string{"qid-1"}}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		resp, err := m.svc.CancelQueued(ctx, &chat_svc.CancelQueuedRequest{SessionID: 100, QueuedID: "qid-1"})
		assert.NoError(t, err)
		assert.Equal(t, []string{"qid-1"}, resp.Removed)
		assert.Equal(t, "qid-1", runner.cancelGotID)
	})
}

func TestCancelQueued_ClearAllForwardsEmptyID(t *testing.T) {
	convey.Convey("CancelQueued QueuedID 为空 → 清空", t, func() {
		m, ctx := cancelQueuedFixture(t)
		runner := &fakeCancelableRunner{cancelRemove: []string{"a", "b"}}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		resp, err := m.svc.CancelQueued(ctx, &chat_svc.CancelQueuedRequest{SessionID: 100, QueuedID: ""})
		assert.NoError(t, err)
		assert.Equal(t, []string{"a", "b"}, resp.Removed)
		assert.Equal(t, "", runner.cancelGotID, "empty QueuedID should pass through as empty")
	})
}

func TestCancelQueued_RunnerWithoutCancelerReturnsUnsupported(t *testing.T) {
	convey.Convey("runner 不实现 SteerCanceler → ChatCancelUnsupported", t, func() {
		m, ctx := cancelQueuedFixture(t)
		// fakeSteerableRunner 只有 Steer，没有 CancelSteer。
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, &fakeSteerableRunner{})
		t.Cleanup(restore)

		_, err := m.svc.CancelQueued(ctx, &chat_svc.CancelQueuedRequest{SessionID: 100, QueuedID: "qid-1"})
		assert.Error(t, err)
	})
}

func TestCancelQueued_NotFoundError(t *testing.T) {
	convey.Convey("runner 返 ErrSteerNotFound → ChatCancelNotFound", t, func() {
		m, ctx := cancelQueuedFixture(t)
		runner := &fakeCancelableRunner{cancelErr: agentruntime.ErrSteerNotFound}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		_, err := m.svc.CancelQueued(ctx, &chat_svc.CancelQueuedRequest{SessionID: 100, QueuedID: "qid-gone"})
		assert.Error(t, err)
	})
}

func TestCancelQueued_NoActiveError(t *testing.T) {
	convey.Convey("runner 返 ErrNoActiveTurn → ChatSteerNoActive", t, func() {
		m, ctx := cancelQueuedFixture(t)
		runner := &fakeCancelableRunner{cancelErr: agentruntime.ErrNoActiveTurn}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		_, err := m.svc.CancelQueued(ctx, &chat_svc.CancelQueuedRequest{SessionID: 100, QueuedID: "qid"})
		assert.Error(t, err)
	})
}

func TestCancelQueued_SessionNotFound(t *testing.T) {
	convey.Convey("CancelQueued 找不到 session → ChatSessionNotFound", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx
		m.session.EXPECT().Find(gomock.Any(), int64(999)).Return(nil, nil)

		_, err := m.svc.CancelQueued(ctx, &chat_svc.CancelQueuedRequest{SessionID: 999, QueuedID: ""})
		assert.Error(t, err)
	})
}

func TestStop_LocalPiCancellationCannotHangBehindAcceptedTurnAbort(t *testing.T) {
	m := setupChatTest(t)
	runner := &stopOrderPiRunner{
		started:      make(chan struct{}),
		abortEntered: make(chan struct{}),
		releaseAbort: make(chan struct{}),
	}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
	t.Cleanup(restore)

	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, ProviderSessionID: "pi-session", AgentStatus: "idle", Status: consts.ACTIVE,
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
	}, nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	var persistedAnchor string
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msg *chat_entity.Message) error {
			if msg != nil && msg.Role == "user" {
				persistedAnchor = msg.ForkAnchor
			}
			return nil
		}).AnyTimes()
	expectNoPiTranscriptRecovery(m, 100)
	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 2000
			} else {
				msg.ID = 2001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	resp, err := m.svc.Send(m.ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hello"})
	require.NoError(t, err)
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("Pi runtime did not accept the turn")
	}

	type stopCallResult struct {
		resp *chat_svc.StopResponse
		err  error
	}
	stopC := make(chan stopCallResult, 1)
	go func() {
		stopped, stopErr := m.svc.Stop(m.ctx, &chat_svc.StopRequest{SessionID: 100})
		stopC <- stopCallResult{resp: stopped, err: stopErr}
	}()
	<-runner.abortEntered
	select {
	case <-runner.runCtx.Done():
	case <-time.After(200 * time.Millisecond):
		close(runner.releaseAbort)
		<-stopC
		t.Fatal("accepted local Pi cancellation waited behind its abort write")
	}
	select {
	case result := <-stopC:
		require.NoError(t, result.err)
		require.NotNil(t, result.resp)
		assert.True(t, result.resp.Stopped)
	case <-time.After(200 * time.Millisecond):
		close(runner.releaseAbort)
		<-stopC
		t.Fatal("Stop did not remain bounded after canceling the accepted turn")
	}
	abortCalls, abortSawCanceled := runner.stopObservation()
	assert.Equal(t, 1, abortCalls)
	assert.True(t, abortSawCanceled, "the settlement window must start before waiting for the abort write")
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)
	assert.Equal(t, "pi-user-anchor-after-local-stop", persistedAnchor)
}

func TestStop_PiAnchorPersistenceFailureRemainsExplicitAfterAbort(t *testing.T) {
	m := setupChatTest(t)
	runner := &stopOrderPiRunner{
		started:      make(chan struct{}),
		abortEntered: make(chan struct{}),
		releaseAbort: make(chan struct{}),
	}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, ProviderSessionID: "pi-session", AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
	}, nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	expectNoPiTranscriptRecovery(m, 100)
	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 2000
			} else {
				msg.ID = 2001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	var persistedAssistantError string
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				return errors.New("anchor update permanently failed")
			}
			persistedAssistantError = msg.ErrorText
			return nil
		}).AnyTimes()

	resp, err := m.svc.Send(m.ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hello"})
	require.NoError(t, err)
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("Pi runtime did not accept the turn")
	}
	stopped, stopErr := m.svc.Stop(m.ctx, &chat_svc.StopRequest{SessionID: 100})
	require.NoError(t, stopErr)
	require.NotNil(t, stopped)
	require.True(t, stopped.Stopped)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	assert.Contains(t, persistedAssistantError, "persist user anchor")
	var gotError, gotAborted bool
	for _, recorded := range m.events {
		event, ok := recorded.Payload.(chat_svc.ChatStreamEvent)
		if !ok {
			continue
		}
		switch event.Kind {
		case chat_svc.StreamError:
			gotError = true
			assert.Contains(t, event.Error, "persist user anchor")
		case chat_svc.StreamAborted:
			gotAborted = true
		}
	}
	assert.True(t, gotError, "anchor persistence failure must remain an explicit terminal error")
	assert.False(t, gotAborted, "a plain aborted terminal event would hide the uneditable row")
}

func TestStop_NoActiveTurnReturnsError(t *testing.T) {
	convey.Convey("Stop 无活跃 turn 且会话已是终态 → ChatStopNoActive", t, func() {
		m := setupChatTest(t)
		// activeCancels 没记录 + DB 会话已是 idle:turn 早自然收尾,没有遗孤要
		// reconcile,保持原 ChatStopNoActive 语义(前端静默,无害的「太晚了」)。
		m.session.EXPECT().Find(m.ctx, int64(100)).Return(
			&chat_entity.Session{ID: 100, AgentStatus: "idle", Status: consts.ACTIVE}, nil)
		_, err := m.svc.Stop(m.ctx, &chat_svc.StopRequest{SessionID: 100})
		assert.Error(t, err, "无活跃 turn 且会话已终态应返 ChatStopNoActive")
	})
}

func TestStop_NoActiveTurnSessionGoneReturnsError(t *testing.T) {
	convey.Convey("Stop 无活跃 turn 且会话查不到 → ChatStopNoActive", t, func() {
		m := setupChatTest(t)
		m.session.EXPECT().Find(m.ctx, int64(101)).Return(nil, nil)
		_, err := m.svc.Stop(m.ctx, &chat_svc.StopRequest{SessionID: 101})
		assert.Error(t, err, "会话不存在没什么可 reconcile,仍返 ChatStopNoActive")
	})
}

func TestStop_OrphanRunningSessionReconciledToIdle(t *testing.T) {
	convey.Convey("Stop 无活跃 turn 但会话卡在 running(重启遗孤)→ reconcile 回 idle", t, func() {
		m := setupChatTest(t)
		// 复现「重启后旧会话停不掉」:app crash / wails dev 热重载 / 第二实例 让 turn
		// goroutine 死了但 DB agent_status 还停在 running,内存 activeCancels 已空。
		// 旧逻辑直接返 ChatStopNoActive 被前端静默吞掉 → 会话永远停不下来。新逻辑把它
		// reconcile 回 idle(等同 abort 收尾),让那颗一直亮着的「停止」按钮真能生效。
		m.session.EXPECT().Find(m.ctx, int64(200)).Return(
			&chat_entity.Session{ID: 200, AgentStatus: "running", Status: consts.ACTIVE}, nil)
		// 遗孤前先问一次 runtime 有没有带外轮在飞;这条会话解析不出 agent,
		// 拿不到 runner,直接落回 reconcile。
		m.agent.EXPECT().Find(m.ctx, int64(0)).Return(nil, nil)
		var updated *chat_entity.Session
		m.session.EXPECT().Update(m.ctx, gomock.Any()).DoAndReturn(
			func(_ context.Context, s *chat_entity.Session) error {
				updated = s
				return nil
			})

		resp, err := m.svc.Stop(m.ctx, &chat_svc.StopRequest{SessionID: 200})

		assert.NoError(t, err, "遗孤会话点停止不应报错")
		assert.NotNil(t, resp)
		assert.True(t, resp.Stopped, "应当回报已停止")
		assert.NotNil(t, updated, "应当把状态落库")
		assert.Equal(t, "idle", updated.AgentStatus, "遗孤会话应被 reconcile 回 idle")
	})
}

func TestStop_OrphanWaitingSessionReconciledToIdle(t *testing.T) {
	convey.Convey("Stop 无活跃 turn 但会话卡在 waiting → reconcile 回 idle 并清 attention", t, func() {
		m := setupChatTest(t)
		m.session.EXPECT().Find(m.ctx, int64(201)).Return(
			&chat_entity.Session{ID: 201, AgentStatus: "waiting", NeedsAttention: true, Status: consts.ACTIVE}, nil)
		// 同上:解析不出 agent → 没有带外轮可中断 → 走遗孤 reconcile。
		m.agent.EXPECT().Find(m.ctx, int64(0)).Return(nil, nil)
		var updated *chat_entity.Session
		m.session.EXPECT().Update(m.ctx, gomock.Any()).DoAndReturn(
			func(_ context.Context, s *chat_entity.Session) error {
				updated = s
				return nil
			})

		resp, err := m.svc.Stop(m.ctx, &chat_svc.StopRequest{SessionID: 201})

		assert.NoError(t, err)
		assert.True(t, resp.Stopped)
		assert.Equal(t, "idle", updated.AgentStatus)
		assert.False(t, updated.NeedsAttention, "reconcile 同时清掉 attention 标记")
	})
}

// TestStop_OutOfBandTurnIsInterruptedNotReconciled 钉死规范「中断带外轮」承诺的第二
// 个可观察改善(docs/specs/2026-08-07-autonomous-turn-resilience.md):「带外轮独占帧流
// 期间，用户点『停止』能真正中断这一轮」。带外轮(自主续轮 / 后台 subagent 活动轮)
// 不进 activeCancels —— 只放宽 Runtime.Abort 的活跃判据够不着这条通路:Stop 在
// activeCancels 取不到条目时直接走 reconcileOrphanStop,把一个真在跑的会话谎报成
// idle 就返回,runner.Abort 一次都没下发,CLI 那一轮照跑。
//
// 决策 3(本轮)把它扩为按 Abort 返回的被中断轮类型断言:这里 runner 上报被中断的
// 是自主轮 → 维持现状,状态留给 driveAutonomousTurn 收尾、Stop 不落库(刻意不
// EXPECT Session().Update);被中断的是 subagent 活动轮 → 由 reconcileOrphanStop
// 接管翻 idle 落库,见 TestStop_SubagentActivityTurnIsReconciledToIdle。
func TestStop_OutOfBandTurnIsInterruptedNotReconciled(t *testing.T) {
	convey.Convey("Stop 无活跃用户轮但自主轮在飞 → 真下发中断,状态留给那一轮收尾,不按遗孤 reconcile 成 idle", t, func() {
		m := setupChatTest(t)

		runner := &abortRecordingRunner{turnKind: agentruntime.TurnKindAutonomous}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		m.session.EXPECT().Find(m.ctx, int64(300)).Return(
			&chat_entity.Session{ID: 300, AgentID: 7, AgentStatus: "running", Status: consts.ACTIVE}, nil)
		m.agent.EXPECT().Find(m.ctx, int64(7)).Return(
			&agent_entity.Agent{ID: 7, AgentBackendID: 12, Status: consts.ACTIVE}, nil)
		m.backend.EXPECT().Find(m.ctx, int64(12)).Return(
			&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
			}, nil)
		// 刻意不 EXPECT Session().Update:自主轮仍在跑,状态归它自己收尾时落,
		// Stop 这里翻 idle 就是谎报(gomock 严格,真调了会直接判失败)。

		resp, err := m.svc.Stop(m.ctx, &chat_svc.StopRequest{SessionID: 300})

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.True(t, resp.Stopped, "带外轮被中断了就该回报已停止")
		assert.Equal(t, []int64{300}, runner.Calls(), "必须真的把中断下发到 runtime")
	})
}

// TestStop_SubagentActivityTurnIsReconciledToIdle 钉死决策 3 的新增接管:Stop 无活跃
// 用户轮、runner 上报被中断的是 subagent 活动轮时,会话的 running/waiting 已无合法
// 依据,且 driveSubagentActivity 不写会话状态 —— 由 reconcileOrphanStop 自己翻 idle
// 并持久化(复用遗孤路径的翻写逻辑),一次点停止即收干净,无需第二次。
func TestStop_SubagentActivityTurnIsReconciledToIdle(t *testing.T) {
	convey.Convey("Stop 无活跃用户轮但 subagent 活动轮在飞 → 中断它并自己把会话 reconcile 回 idle 落库", t, func() {
		m := setupChatTest(t)

		runner := &abortRecordingRunner{turnKind: agentruntime.TurnKindSubagentActivity}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		m.session.EXPECT().Find(m.ctx, int64(310)).Return(
			&chat_entity.Session{ID: 310, AgentID: 7, AgentStatus: "running", Status: consts.ACTIVE}, nil)
		m.agent.EXPECT().Find(m.ctx, int64(7)).Return(
			&agent_entity.Agent{ID: 7, AgentBackendID: 12, Status: consts.ACTIVE}, nil)
		m.backend.EXPECT().Find(m.ctx, int64(12)).Return(
			&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
			}, nil)
		var updated *chat_entity.Session
		m.session.EXPECT().Update(m.ctx, gomock.Any()).DoAndReturn(
			func(_ context.Context, s *chat_entity.Session) error {
				updated = s
				return nil
			})

		resp, err := m.svc.Stop(m.ctx, &chat_svc.StopRequest{SessionID: 310})

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.True(t, resp.Stopped, "subagent 活动轮被中断了就该回报已停止")
		assert.Equal(t, []int64{310}, runner.Calls(), "必须真的把中断下发到 runtime")
		assert.NotNil(t, updated, "subagent 活动轮被中断后要由 Stop 自己落库")
		assert.Equal(t, "idle", updated.AgentStatus, "会话应被 reconcile 回 idle")
	})
}

// TestStop_NoTurnAtAllStillReconcilesOrphan 守住「重启遗孤」那条既有修复不被上面的
// 改动吃掉:runtime 报 ErrNoActiveTurn(app crash / 热重载后内存里什么都不剩)时,
// 会话仍要被 reconcile 回 idle,那颗一直亮着的「停止」按钮才有效。
func TestStop_NoTurnAtAllStillReconcilesOrphan(t *testing.T) {
	convey.Convey("Stop 无用户轮且 runtime 报无活跃轮 → 仍 reconcile 回 idle", t, func() {
		m := setupChatTest(t)

		runner := &abortRecordingRunner{abortErr: agentruntime.ErrNoActiveTurn}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		m.session.EXPECT().Find(m.ctx, int64(301)).Return(
			&chat_entity.Session{ID: 301, AgentID: 7, AgentStatus: "running", Status: consts.ACTIVE}, nil)
		m.agent.EXPECT().Find(m.ctx, int64(7)).Return(
			&agent_entity.Agent{ID: 7, AgentBackendID: 12, Status: consts.ACTIVE}, nil)
		m.backend.EXPECT().Find(m.ctx, int64(12)).Return(
			&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
			}, nil)
		var updated *chat_entity.Session
		m.session.EXPECT().Update(m.ctx, gomock.Any()).DoAndReturn(
			func(_ context.Context, s *chat_entity.Session) error {
				updated = s
				return nil
			})

		resp, err := m.svc.Stop(m.ctx, &chat_svc.StopRequest{SessionID: 301})

		assert.NoError(t, err)
		assert.True(t, resp.Stopped)
		assert.NotNil(t, updated, "没有任何活跃轮时仍要落库")
		assert.Equal(t, "idle", updated.AgentStatus, "遗孤会话应被 reconcile 回 idle")
	})
}

func TestStop_InvalidRequestReturnsError(t *testing.T) {
	convey.Convey("Stop SessionID <= 0 → InvalidParameter", t, func() {
		m := setupChatTest(t)
		_, err := m.svc.Stop(m.ctx, &chat_svc.StopRequest{SessionID: 0})
		assert.Error(t, err)
	})
}

// stopBgRunner 是实现 BackgroundTaskStopper 的最小 runner,记录 StopBackgroundTask 入参。
type stopBgRunner struct {
	gotSid  int64
	gotTask string
	stopErr error
}

func (*stopBgRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{capability.CapStopBackgroundTask: true}}
}

func (*stopBgRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	ch := make(chan agentruntime.Event)
	close(ch)
	return ch, &agentruntime.RunResult{}, nil
}

func (r *stopBgRunner) StopBackgroundTask(_ context.Context, sid int64, taskID string) error {
	r.gotSid = sid
	r.gotTask = taskID
	return r.stopErr
}

func TestStopBackgroundTask_InvalidRequest(t *testing.T) {
	convey.Convey("SessionID<=0 或 ToolCallID 空 → InvalidParameter", t, func() {
		m := setupChatTest(t)
		_, err := m.svc.StopBackgroundTask(m.ctx, &chat_svc.StopBackgroundTaskRequest{SessionID: 0, ToolCallID: "tu1"})
		assert.Error(t, err)
		_, err = m.svc.StopBackgroundTask(m.ctx, &chat_svc.StopBackgroundTaskRequest{SessionID: 1, ToolCallID: ""})
		assert.Error(t, err)
	})
}

func TestStopBackgroundTask_SessionNotFound(t *testing.T) {
	convey.Convey("会话查不到 → ChatSessionNotFound", t, func() {
		m := setupChatTest(t)
		m.session.EXPECT().Find(m.ctx, int64(101)).Return(nil, nil)
		_, err := m.svc.StopBackgroundTask(m.ctx, &chat_svc.StopBackgroundTaskRequest{SessionID: 101, ToolCallID: "tu1"})
		assert.Error(t, err)
	})
}

func TestStopBackgroundTask_AlreadyTerminalIsIdempotent(t *testing.T) {
	convey.Convey("任务已终态(completed) → 幂等 Stopped=true,不碰 runner", t, func() {
		m := setupChatTest(t)
		m.session.EXPECT().Find(m.ctx, int64(42)).Return(
			&chat_entity.Session{ID: 42, AgentID: 7, Status: consts.ACTIVE}, nil)
		m.message.EXPECT().FindSubagentState(m.ctx, int64(42), "tu1").Return("b0", "completed", true, nil)

		resp, err := m.svc.StopBackgroundTask(m.ctx, &chat_svc.StopBackgroundTaskRequest{SessionID: 42, ToolCallID: "tu1"})
		assert.NoError(t, err)
		assert.True(t, resp.Stopped)
	})
}

func TestStopBackgroundTask_NoTaskIDReturnsUnknown(t *testing.T) {
	convey.Convey("running 但缺 task_id(老会话)→ ChatStopBgTaskUnknown", t, func() {
		m := setupChatTest(t)
		m.session.EXPECT().Find(m.ctx, int64(42)).Return(
			&chat_entity.Session{ID: 42, AgentID: 7, Status: consts.ACTIVE}, nil)
		m.message.EXPECT().FindSubagentState(m.ctx, int64(42), "tu1").Return("", "running", true, nil)

		_, err := m.svc.StopBackgroundTask(m.ctx, &chat_svc.StopBackgroundTaskRequest{SessionID: 42, ToolCallID: "tu1"})
		assert.Error(t, err)
	})
}

func TestStopBackgroundTask_SuccessFlipsCanceled(t *testing.T) {
	convey.Convey("running + task_id → 下发 stop_task 并把块翻 canceled", t, func() {
		m := setupChatTest(t)
		runner := &stopBgRunner{}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		defer restore()

		m.session.EXPECT().Find(m.ctx, int64(42)).Return(
			&chat_entity.Session{ID: 42, AgentID: 7, Status: consts.ACTIVE}, nil)
		m.message.EXPECT().FindSubagentState(m.ctx, int64(42), "tu1").Return("b0n82mqaj", "running", true, nil)
		m.agent.EXPECT().Find(m.ctx, int64(7)).Return(
			&agent_entity.Agent{ID: 7, AgentBackendID: 3, Status: consts.ACTIVE}, nil)
		m.backend.EXPECT().Find(m.ctx, int64(3)).Return(
			&agent_backend_entity.AgentBackend{ID: 3, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE}, nil)
		m.message.EXPECT().FlipSubagentStatus(m.ctx, int64(42), "tu1", "canceled", "").Return(nil)

		resp, err := m.svc.StopBackgroundTask(m.ctx, &chat_svc.StopBackgroundTaskRequest{SessionID: 42, ToolCallID: "tu1"})
		assert.NoError(t, err)
		assert.True(t, resp.Stopped)
		assert.Equal(t, int64(42), runner.gotSid)
		assert.Equal(t, "b0n82mqaj", runner.gotTask)
	})
}

func TestListAgentSessions(t *testing.T) {
	convey.Convey("ListAgentSessions", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		convey.Convey("中段分页：返回当前页 sessions、total、hasMore=true", func() {
			m.session.EXPECT().ListByAgentPaged(ctx, int64(7), 20, 20).Return([]*chat_entity.Session{
				{ID: 30, AgentID: 7, Title: "newer", AgentStatus: "idle", LastMessageAt: 1700000300000},
				{ID: 25, AgentID: 7, Title: "older", AgentStatus: "idle", LastMessageAt: 1700000250000},
			}, nil)
			m.session.EXPECT().CountByAgent(ctx, int64(7)).Return(int64(42), nil)

			resp, err := m.svc.ListAgentSessions(ctx, &chat_svc.ListAgentSessionsRequest{
				AgentID: 7, Offset: 20, Limit: 20,
			})
			assert.NoError(t, err)
			assert.Len(t, resp.Sessions, 2)
			assert.Equal(t, int64(42), resp.Total)
			assert.True(t, resp.HasMore, "20+2 < 42 → hasMore=true")
			assert.Equal(t, "newer", resp.Sessions[0].Title)
		})

		convey.Convey("末页：offset+len == total → hasMore=false", func() {
			m.session.EXPECT().ListByAgentPaged(ctx, int64(7), 40, 20).Return([]*chat_entity.Session{
				{ID: 2, AgentID: 7, Title: "tail-a", AgentStatus: "idle"},
				{ID: 1, AgentID: 7, Title: "tail-b", AgentStatus: "idle"},
			}, nil)
			m.session.EXPECT().CountByAgent(ctx, int64(7)).Return(int64(42), nil)

			resp, err := m.svc.ListAgentSessions(ctx, &chat_svc.ListAgentSessionsRequest{
				AgentID: 7, Offset: 40, Limit: 20,
			})
			assert.NoError(t, err)
			assert.Len(t, resp.Sessions, 2)
			assert.False(t, resp.HasMore)
		})

		convey.Convey("limit=0 默认走 20", func() {
			m.session.EXPECT().ListByAgentPaged(ctx, int64(7), 0, 20).Return(nil, nil)
			m.session.EXPECT().CountByAgent(ctx, int64(7)).Return(int64(0), nil)

			resp, err := m.svc.ListAgentSessions(ctx, &chat_svc.ListAgentSessionsRequest{
				AgentID: 7, Offset: 0, Limit: 0,
			})
			assert.NoError(t, err)
			assert.Empty(t, resp.Sessions)
			assert.False(t, resp.HasMore)
		})

		convey.Convey("limit 超上限 → 裁到 100", func() {
			m.session.EXPECT().ListByAgentPaged(ctx, int64(7), 0, 100).Return(nil, nil)
			m.session.EXPECT().CountByAgent(ctx, int64(7)).Return(int64(0), nil)

			_, err := m.svc.ListAgentSessions(ctx, &chat_svc.ListAgentSessionsRequest{
				AgentID: 7, Offset: 0, Limit: 999,
			})
			assert.NoError(t, err)
		})

		convey.Convey("agentID<=0 → InvalidParameter", func() {
			_, err := m.svc.ListAgentSessions(ctx, &chat_svc.ListAgentSessionsRequest{
				AgentID: 0, Offset: 0, Limit: 20,
			})
			assert.Error(t, err)
		})

		convey.Convey("offset<0 → InvalidParameter", func() {
			_, err := m.svc.ListAgentSessions(ctx, &chat_svc.ListAgentSessionsRequest{
				AgentID: 7, Offset: -1, Limit: 20,
			})
			assert.Error(t, err)
		})
	})
}

// setPermissionModeFixture 复用：每个用例都要 Find session（用于 DB 写），
// 走 happy 路径时还得 Find agent / backend / provider。提前注入 mocks 后
// 用例只关心「这次切到什么 mode + runtime 行为」。session 是返回的引用，
// 测试可以读 PermissionMode 字段验证 Update 入参。
func setPermissionModeFixture(t *testing.T) (*chatMocks, *chat_entity.Session) {
	t.Helper()
	m := setupChatTest(t)
	chat_svc.RegisterGateway(&fakeChatGateway{
		status: httpgateway.GatewayStatus{State: "running", URL: "http://127.0.0.1:60080"},
	})
	t.Cleanup(func() { chat_svc.RegisterGateway(nil) })

	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")
	return m, sess
}

func TestSetPermissionMode_InvalidMode(t *testing.T) {
	convey.Convey("mode 不在白名单 → ChatPermissionModeInvalid（不查 DB / 不调 runtime）", t, func() {
		m := setupChatTest(t)
		_, err := m.svc.SetPermissionMode(m.ctx, &chat_svc.SetPermissionModeRequest{
			SessionID: 100, Mode: "wild-west",
		})
		assert.Error(t, err)
	})
}

func TestSetPermissionMode_SessionNotFound(t *testing.T) {
	convey.Convey("session 不存在 → ChatSessionNotFound", t, func() {
		m := setupChatTest(t)
		m.session.EXPECT().Find(gomock.Any(), int64(999)).Return(nil, nil)
		_, err := m.svc.SetPermissionMode(m.ctx, &chat_svc.SetPermissionModeRequest{
			SessionID: 999, Mode: "plan",
		})
		assert.Error(t, err)
	})
}

func TestSetPermissionMode_RunnerUnsupported(t *testing.T) {
	convey.Convey("runner 不实现 PermissionModeSetter → Unsupported（不写 DB）", t, func() {
		m, _ := setPermissionModeFixture(t)
		// 用 fakeSteerableRunner — 没实现 PermissionModeSetter。
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, &fakeSteerableRunner{})
		t.Cleanup(restore)
		// 关键断言：session Update 不应该被调用 —— mock_chat_repo 默认严格 mock，
		// 没 EXPECT Update 就调到会失败。

		_, err := m.svc.SetPermissionMode(m.ctx, &chat_svc.SetPermissionModeRequest{
			SessionID: 100, Mode: "plan",
		})
		assert.Error(t, err)
	})
}

func TestSetPermissionMode_HappyPath(t *testing.T) {
	convey.Convey("DB 写成功 + runtime 下发成功 → Applied=true", t, func() {
		m, sess := setPermissionModeFixture(t)
		runner := &fakePermissionRunner{}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		m.session.EXPECT().UpdatePermissionMode(gomock.Any(), int64(100), "plan").DoAndReturn(
			func(_ context.Context, _ int64, mode string) error {
				assert.Equal(t, "plan", mode, "DB 写入的是请求里的 mode")
				assert.Equal(t, "plan", sess.PermissionMode, "内存 session 也要同步，供启动路径使用")
				return nil
			},
		)

		resp, err := m.svc.SetPermissionMode(m.ctx, &chat_svc.SetPermissionModeRequest{
			SessionID: 100, Mode: "plan",
		})
		assert.NoError(t, err)
		assert.True(t, resp.Applied)
		assert.Equal(t, "plan", resp.Mode)
		assert.True(t, runner.setCalled, "runtime setter 应当被调用")
		assert.Equal(t, "plan", runner.setMode)
		assert.Equal(t, int64(100), runner.setSID)
	})
}

func TestSetPermissionMode_NoActiveTurnNonFatal(t *testing.T) {
	convey.Convey("runtime 返 ErrNoActiveTurn → 不报错（DB 已持久化，下次 spawn 生效）", t, func() {
		m, sess := setPermissionModeFixture(t)
		runner := &fakePermissionRunner{setErr: agentruntime.ErrNoActiveTurn}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		m.session.EXPECT().UpdatePermissionMode(gomock.Any(), int64(100), "acceptEdits").DoAndReturn(
			func(_ context.Context, _ int64, mode string) error {
				assert.Equal(t, "acceptEdits", mode)
				assert.Equal(t, "acceptEdits", sess.PermissionMode)
				return nil
			},
		)

		resp, err := m.svc.SetPermissionMode(m.ctx, &chat_svc.SetPermissionModeRequest{
			SessionID: 100, Mode: "acceptEdits",
		})
		assert.NoError(t, err, "NoActive 必须吞掉 —— 这是预启动场景的核心契约")
		assert.True(t, resp.Applied)
		assert.Equal(t, "acceptEdits", resp.Mode)
		assert.True(t, runner.setCalled)
	})
}

func TestSetPermissionMode_CodexPersistsWithoutRuntimeSetter(t *testing.T) {
	convey.Convey("codex default/plan 写 DB 后直接成功，不要求 runtime setter", t, func() {
		m := setupChatTest(t)
		sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE}
		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
			ID: 7, Name: "Codex", AgentBackendID: 12, Status: consts.ACTIVE,
		}, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "", Status: consts.ACTIVE,
		}, nil)
		m.session.EXPECT().UpdatePermissionMode(gomock.Any(), int64(100), "plan").Return(nil)

		resp, err := m.svc.SetPermissionMode(m.ctx, &chat_svc.SetPermissionModeRequest{
			SessionID: 100,
			Mode:      "plan",
		})
		assert.NoError(t, err)
		assert.True(t, resp.Applied)
		assert.Equal(t, "plan", resp.Mode)
		assert.Equal(t, "plan", sess.PermissionMode)
	})
}

func TestSetPermissionMode_CodexRejectsActiveTurn(t *testing.T) {
	for _, status := range []string{"running", "waiting"} {
		t.Run(status, func(t *testing.T) {
			m := setupChatTest(t)
			m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
				ID: 100, AgentID: 7, AgentStatus: status, Status: consts.ACTIVE,
			}, nil)
			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
				ID: 7, Name: "Codex", AgentBackendID: 12, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "", Status: consts.ACTIVE,
			}, nil)

			_, err := m.svc.SetPermissionMode(m.ctx, &chat_svc.SetPermissionModeRequest{
				SessionID: 100,
				Mode:      "plan",
			})
			assert.Error(t, err)
		})
	}
}

func TestSetPermissionMode_CodexRejectsClaudeOnlyMode(t *testing.T) {
	convey.Convey("codex 不接受 acceptEdits / bypassPermissions", t, func() {
		m := setupChatTest(t)
		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
			ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
		}, nil)
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
			ID: 7, Name: "Codex", AgentBackendID: 12, Status: consts.ACTIVE,
		}, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: "", Status: consts.ACTIVE,
		}, nil)

		_, err := m.svc.SetPermissionMode(m.ctx, &chat_svc.SetPermissionModeRequest{
			SessionID: 100,
			Mode:      "acceptEdits",
		})
		assert.Error(t, err)
	})
}

func TestSetPermissionMode_RuntimeOtherErrorStillReturnsError(t *testing.T) {
	convey.Convey("runtime 返非 NoActive 错 → 返错（DB 已写但调用方应当知道）", t, func() {
		m, _ := setPermissionModeFixture(t)
		runner := &fakePermissionRunner{setErr: errors.New("stdin broken")}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		m.session.EXPECT().UpdatePermissionMode(gomock.Any(), int64(100), "bypassPermissions").Return(nil)

		_, err := m.svc.SetPermissionMode(m.ctx, &chat_svc.SetPermissionModeRequest{
			SessionID: 100, Mode: "bypassPermissions",
		})
		assert.Error(t, err)
	})
}

func TestSetPermissionMode_DBWriteFailure(t *testing.T) {
	convey.Convey("DB 写失败 → ChatPermissionModeInternal（runtime 不应被调）", t, func() {
		m, _ := setPermissionModeFixture(t)
		runner := &fakePermissionRunner{}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		m.session.EXPECT().UpdatePermissionMode(gomock.Any(), int64(100), "default").Return(errors.New("db down"))

		_, err := m.svc.SetPermissionMode(m.ctx, &chat_svc.SetPermissionModeRequest{
			SessionID: 100, Mode: "default",
		})
		assert.Error(t, err)
		assert.False(t, runner.setCalled, "DB 失败后不应再调 runtime")
	})
}

// providerSessionGoneRunner 模拟"claudecode CLI resume 失效"：runner.Run 直接
// 返 wrapping ErrSessionNotFound 的 err（acquireSession 早退），或者通过
// result.StopErr 抬到上层（0-frame fallback）。两个分支都要让 chat_svc 识别 +
// 清掉 sess.ProviderSessionID + emit i18n 错误。
type providerSessionGoneRunner struct {
	mode string // "early" → Run 直接返 err；"stop" → result.StopErr
	err  error
}

func (providerSessionGoneRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{}
}
func (r providerSessionGoneRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	wrapped := r.err
	if wrapped == nil {
		wrapped = fmt.Errorf("%w: No conversation found with session ID: gone", claudecode.ErrSessionNotFound)
	}
	switch r.mode {
	case "early":
		return nil, nil, wrapped
	case "stop":
		events := make(chan agentruntime.Event)
		close(events)
		return events, &agentruntime.RunResult{StopErr: wrapped}, nil
	}
	panic("providerSessionGoneRunner: unknown mode " + r.mode)
}

// TestSend_ClaudeCodeProviderSessionGoneEarlyClearsAndSurfacesI18n 用户报告的核心
// 修复点 ① —— runner.Run 直接返 wrapping ErrSessionNotFound 的 err（OpenSession
// 健康检查窗口拿到 stderr 后 acquireSession 早退）时，chat_svc 必须：
//   - 把 sess.ProviderSessionID 清空并持久化（下一轮 Send 才能 spawn 全新 CLI 会话）
//   - 把错误替换成 i18n.NewError(ChatProviderSessionGone) 的人话文案，让前端
//     看到「CLI 会话已过期 …」而不是英文 stderr。
func TestSend_ClaudeCodeProviderSessionGoneEarlyClearsAndSurfacesI18n(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx

	runner := providerSessionGoneRunner{mode: "early"}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, ProviderSessionID: "cc-gone", AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
	}, nil)

	var (
		clearedMu  sync.Mutex
		clearedSID bool
	)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
		if s.ID == 100 && s.ProviderSessionID == "" {
			clearedMu.Lock()
			clearedSID = true
			clearedMu.Unlock()
		}
		return nil
	}).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	var errorEvent *chat_svc.ChatStreamEvent
	for _, ev := range m.events {
		payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
		if ok && payload.Kind == chat_svc.StreamError {
			cp := payload
			errorEvent = &cp
			break
		}
	}
	require.NotNil(t, errorEvent, "ErrSessionNotFound 路径必须 emit StreamError")
	assert.Contains(t, errorEvent.Error, "CLI 会话已过期",
		"StreamError.Error 必须是 ChatProviderSessionGone 的 i18n 中文文案")
	assert.NotContains(t, errorEvent.Error, "No conversation found",
		"i18n 替换之后不应当再回退到英文 stderr")

	clearedMu.Lock()
	defer clearedMu.Unlock()
	assert.True(t, clearedSID,
		"必须在 DB 上把 ProviderSessionID 置空，否则下一轮 Send 还会再撞同一个失效 id")
}

func TestSend_PiAgentNativeSessionGoneClearsAndSurfacesI18n(t *testing.T) {
	m := setupChatTest(t)
	runner := providerSessionGoneRunner{
		mode: "early",
		err:  fmt.Errorf("%w: No session found matching 'pi-native-gone'", agentruntime.ErrSessionNotFound),
	}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, runner)
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, ProviderSessionID: "pi-native-gone", AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Pi", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypePiAgent), Status: consts.ACTIVE,
	}, nil)

	cleared := make(chan struct{}, 1)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
		if s.ID == 100 && s.ProviderSessionID == "" {
			select {
			case cleared <- struct{}{}:
			default:
			}
		}
		return nil
	}).AnyTimes()
	expectNoPiTranscriptRecovery(m, 100)
	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
		if msg.Role == "user" {
			msg.ID = 1000
		} else {
			msg.ID = 1001
		}
		return nil
	}).Times(2)
	m.dbMock.ExpectCommit()
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := m.svc.Send(m.ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	var errorText string
	for _, ev := range m.events {
		if payload, ok := ev.Payload.(chat_svc.ChatStreamEvent); ok && payload.Kind == chat_svc.StreamError {
			errorText = payload.Error
			break
		}
	}
	assert.Contains(t, errorText, "CLI 会话已过期")
	assert.NotContains(t, errorText, "No session found matching")
	select {
	case <-cleared:
	default:
		t.Fatal("Pi native session loss must clear provider_session_id")
	}
}

// TestSend_ClaudeCodeProviderSessionGoneViaStopErrClearsAndSurfacesI18n 修复点 ②
// —— 0-frame fallback 路径：runner 正常返回 events，但 result.StopErr 抬着
// ErrSessionNotFound（CLI spawn 起来后才命中 stderr → ExitErr → StopErr）。
// chat_svc 的处理必须和 early err 路径完全一致。
func TestSend_ClaudeCodeProviderSessionGoneViaStopErrClearsAndSurfacesI18n(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx

	runner := providerSessionGoneRunner{mode: "stop"}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, ProviderSessionID: "cc-gone", AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
	}, nil)

	var (
		clearedMu  sync.Mutex
		clearedSID bool
	)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
		if s.ID == 100 && s.ProviderSessionID == "" {
			clearedMu.Lock()
			clearedSID = true
			clearedMu.Unlock()
		}
		return nil
	}).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()

	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	var errorEvent *chat_svc.ChatStreamEvent
	for _, ev := range m.events {
		payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
		if ok && payload.Kind == chat_svc.StreamError {
			cp := payload
			errorEvent = &cp
			break
		}
	}
	require.NotNil(t, errorEvent, "StopErr 路径必须 emit StreamError")
	assert.Contains(t, errorEvent.Error, "CLI 会话已过期",
		"StopErr 走的也是 i18n 替换分支，文案应当一致")

	clearedMu.Lock()
	defer clearedMu.Unlock()
	assert.True(t, clearedSID, "StopErr 命中 ErrSessionNotFound 时同样必须清空 ProviderSessionID")
}

// passivePermissionModeRunner 模拟 claudecode 后端在 turn 中途 emit
// EventPermissionModeChanged（被动 ExitPlanMode 流程），让 chat_svc.runTurn
// 走 mode 同步分支。
type passivePermissionModeRunner struct {
	emitMode  string
	emitTwice bool
}

func (passivePermissionModeRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{}
}
func (r passivePermissionModeRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	events := make(chan agentruntime.Event, 4)
	events <- agentruntime.TextDelta{Text: "preface"}
	events <- agentruntime.PermissionModeChanged{Mode: r.emitMode}
	if r.emitTwice {
		// 同 mode 再发一遍:service 应当幂等,不再二次写 DB / 二次推 patch。
		events <- agentruntime.PermissionModeChanged{Mode: r.emitMode}
	}
	close(events)
	return events, &agentruntime.RunResult{}, nil
}

// TestSend_PassivePermissionModeChangePersistsAndEmitsPatch 验证 CLI 自身切换
// permission mode 之后：
//   - chat_sessions.permission_mode 通过 UpdatePermissionMode 落到 DB；
//   - 内存里的 sess.PermissionMode 同步到新值；
//   - 推一条 StreamSessionStatus 给前端，payload 里携带新 permissionMode。
//
// 同时验证幂等：CLI 重复发同一个 mode 不会触发二次写 DB / 二次 emit。
func TestSend_PassivePermissionModeChangePersistsAndEmitsPatch(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, passivePermissionModeRunner{
		emitMode:  "default",
		emitTwice: true, // 验证幂等
	})
	t.Cleanup(restore)

	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "running", Status: consts.ACTIVE,
		PermissionMode: "plan",
	}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Claude", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "", Status: consts.ACTIVE,
	}, nil)
	// 幂等期望：UpdatePermissionMode("default") 必须只调一次，即便事件被发了两次。
	m.session.EXPECT().UpdatePermissionMode(gomock.Any(), int64(100), "default").Return(nil).Times(1)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	// 1) 内存 session 已经同步到新 mode（runTurn 改的是 *sess 上的字段）
	assert.Equal(t, "default", sess.PermissionMode, "sess.PermissionMode 必须翻到 CLI 通报的新值")

	// 2) emitter 上恰好有一条 StreamSessionStatus 带 permissionMode：default
	var modePatches []chat_svc.ChatStreamEvent
	for _, ev := range m.events {
		payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
		if !ok || payload.Kind != chat_svc.StreamSessionStatus {
			continue
		}
		if payload.SessionStatus != nil && payload.SessionStatus.PermissionMode != "" {
			modePatches = append(modePatches, payload)
		}
	}
	require.Len(t, modePatches, 1, "StreamSessionStatus 携带 permissionMode 必须且只能 emit 一次（幂等）")
	require.NotNil(t, modePatches[0].SessionStatus)
	assert.Equal(t, "default", modePatches[0].SessionStatus.PermissionMode)
}

// TestSend_StreamToolUseCarriesCanonical 断言 emit 出去的 Edit / Write tool_use
// 事件带 Canonical (FileEdit / FileWrite),前端 CanonicalToolRouter 据此分发到
// canonical-tool/<kind>/card.tsx 渲染。旧 ToolDiff/ToolWrite sidecar 已删 — 由
// runtime translator 算出 canonical 后透传到 dispatcher_emitter,不再分两路计算。
func TestSend_StreamToolUseCarriesCanonical(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, scriptedRunner{events: []agentruntime.RuntimeEvent{
		{Kind: agentruntime.EventToolUseStart, ToolUse: &agentruntime.ToolUseEvent{
			ID:    "toolu_edit",
			Name:  "Edit",
			Input: []byte(`{"file_path":"/x.go","old_string":"a\n","new_string":"b\n"}`),
		}},
		{Kind: agentruntime.EventToolResult, ToolResult: &agentruntime.ToolResultEvent{
			ToolCallID: "toolu_edit",
			Content:    "ok",
		}},
		{Kind: agentruntime.EventToolUseStart, ToolUse: &agentruntime.ToolUseEvent{
			ID:    "toolu_write",
			Name:  "Write",
			Input: []byte(`{"file_path":"/y.go","content":"hello\n"}`),
		}},
		{Kind: agentruntime.EventToolResult, ToolResult: &agentruntime.ToolResultEvent{
			ToolCallID: "toolu_write",
			Content:    "ok",
		}},
		{Kind: agentruntime.EventDone},
	}})
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(201)).Return(&chat_entity.Session{
		ID: 201, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(201)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 2000
			} else {
				msg.ID = 2001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()
	m.message.EXPECT().List(gomock.Any(), int64(201)).Return(nil, nil).AnyTimes()
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	// 轮内 checkpoint 已从 Update 改走 CheckpointBlocks(整表替换 → 差分写,见
	// chat_repo.syncBlocks);这条用例盯的正是 checkpoint 落了什么,两条路都要收。
	m.message.EXPECT().CheckpointBlocks(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 201, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	var editEv, writeEv *chat_svc.ChatStreamEvent
	for _, ev := range m.events {
		payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
		if !ok || payload.Kind != chat_svc.StreamToolUse {
			continue
		}
		switch payload.ToolCallID {
		case "toolu_edit":
			ev := payload
			editEv = &ev
		case "toolu_write":
			ev := payload
			writeEv = &ev
		}
	}

	require.NotNil(t, editEv, "Edit tool_use 事件必须 emit")
	require.NotNil(t, editEv.Canonical, "Edit 事件必须携带 Canonical FileEdit,前端 live 才能走 FileEditCard")
	assert.Equal(t, "file.edit", string(editEv.Canonical.Kind))
	require.NotNil(t, editEv.Canonical.FileEdit)
	require.Len(t, editEv.Canonical.FileEdit.Files, 1)
	assert.Equal(t, "/x.go", editEv.Canonical.FileEdit.Files[0].Path)

	require.NotNil(t, writeEv, "Write tool_use 事件必须 emit")
	require.NotNil(t, writeEv.Canonical, "Write 事件必须携带 Canonical FileWrite,前端 live 才能走 FileWriteCard")
	assert.Equal(t, "file.write", string(writeEv.Canonical.Kind))
	require.NotNil(t, writeEv.Canonical.FileWrite)
	assert.Equal(t, "/y.go", writeEv.Canonical.FileWrite.Path)
	assert.Equal(t, "hello\n", writeEv.Canonical.FileWrite.Content)
}

// repo 报错时,ListAgents 返回的 error 必须带上真实 cause —— 这是 2026-07-14「新建对话发送失败:
// 操作失败」事故的回归护栏:当时 53 处调用点把 err 整个丢掉,前端和日志都只剩一句「操作失败」。
func TestListAgents_SurfacesRepoCause(t *testing.T) {
	ctrl := gomock.NewController(t)
	agentMock := mock_agent_repo.NewMockAgentRepo(ctrl)
	prev := agent_repo.Agent()
	agent_repo.RegisterAgent(agentMock)
	t.Cleanup(func() { agent_repo.RegisterAgent(prev) })

	cause := errors.New("SQL logic error: no such column: run_id (1)")
	agentMock.EXPECT().List(gomock.Any()).Return(nil, cause)

	svc := chat_svc.NewChat(chat_svc.NoopEmitter{})
	_, err := svc.ListAgents(context.Background(), &chat_svc.ListAgentsRequest{})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no such column: run_id")
	assert.ErrorIs(t, err, cause)
}

// TestSend_CoalescedStreamPreservesTextAndOrdering 是流式合帧的端到端回归。
//
// 合帧把「一个 token 一条 Wails 事件」并成「一批一条」,风险全在两处:文本会不会被
// 吞掉、事件顺序会不会错位。这里让一轮真实的 runTurn(含正文 → 工具 → 正文 → 收尾)
// 跑过 coalescingEmitter 验这两条。
//
// 注意这条测试用的是真实时钟,所以它**不**断言「合并成了几条」—— 那取决于事件到达
// 的快慢,断言条数会变成一条看机器脸色的测试。合并本身的判定(间隔 / 字节阈值 /
// 换 kind / 跨 stream)由 coalescing_emitter_test.go 注入假时钟逐条钉死;这里只负责
// 端到端证明「不管合成几条,内容和顺序都不变」。断言:
//  1. 所有 chunk 拼起来与 runner 吐出的原文逐字相同(一个字都不能丢);
//  2. tool_use 出现在它前面那段正文之后、后面那段正文之前(顺序不变);
//  3. 收尾的 done 之前没有残留的未 flush 文本。
func TestSend_CoalescedStreamPreservesTextAndOrdering(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx

	// 把 svc 换成「经过合帧」的那一条链路 —— setupChatTest 已经把仓储 mock 都注册好了。
	var (
		mu  sync.Mutex
		got []chat_svc.ChatStreamEvent
	)
	rec := chat_svc.EmitterFunc(func(_ context.Context, _ string, payload any) {
		if ev, ok := payload.(chat_svc.ChatStreamEvent); ok {
			mu.Lock()
			got = append(got, ev)
			mu.Unlock()
		}
	})
	m.svc = chat_svc.NewChat(chat_svc.NewCoalescingEmitter(rec))
	chat_svc.RegisterChat(m.svc)

	before := []string{"Let ", "me ", "check ", "that ", "file", ".\n"}
	after := []string{"Found ", "it", ": ", "all ", "good", "."}

	events := make([]agentruntime.RuntimeEvent, 0, len(before)+len(after)+2)
	for _, s := range before {
		events = append(events, agentruntime.RuntimeEvent{Kind: agentruntime.EventTextDelta, Text: s})
	}
	events = append(events,
		agentruntime.RuntimeEvent{Kind: agentruntime.EventToolUseStart, ToolUse: &agentruntime.ToolUseEvent{
			ID: "t1", Name: "Read", Input: []byte(`{}`),
		}},
		agentruntime.RuntimeEvent{Kind: agentruntime.EventToolResult, ToolResult: &agentruntime.ToolResultEvent{
			ToolCallID: "t1", Content: "ok",
		}},
	)
	for _, s := range after {
		events = append(events, agentruntime.RuntimeEvent{Kind: agentruntime.EventTextDelta, Text: s})
	}
	events = append(events, agentruntime.RuntimeEvent{Kind: agentruntime.EventDone})

	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, scriptedRunner{events: events})
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21",
		ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE,
	}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.dbMock.ExpectCommit()
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	// 轮内 checkpoint 已从 Update 改走 CheckpointBlocks(整表替换 → 差分写,见
	// chat_repo.syncBlocks);这条用例盯的正是 checkpoint 落了什么,两条路都要收。
	m.message.EXPECT().CheckpointBlocks(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil).AnyTimes()
	m.message.EXPECT().UpdateUsage(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	mu.Lock()
	defer mu.Unlock()

	// 1) 一个字都没丢。
	var text strings.Builder
	for _, ev := range got {
		if ev.Kind == chat_svc.StreamChunk {
			text.WriteString(ev.Delta)
		}
	}
	assert.Equal(t, strings.Join(before, "")+strings.Join(after, ""), text.String(),
		"合帧后 chunk 拼起来必须与原文逐字相同")

	// 2) 顺序不变:tool_use 夹在两段正文中间。
	idxOf := func(pred func(chat_svc.ChatStreamEvent) bool) int {
		for i, ev := range got {
			if pred(ev) {
				return i
			}
		}
		return -1
	}
	firstChunk := idxOf(func(e chat_svc.ChatStreamEvent) bool { return e.Kind == chat_svc.StreamChunk })
	toolUse := idxOf(func(e chat_svc.ChatStreamEvent) bool { return e.Kind == chat_svc.StreamToolUse })
	chunkAfterTool := idxOf(func(e chat_svc.ChatStreamEvent) bool {
		return e.Kind == chat_svc.StreamChunk && strings.Contains(e.Delta, "Found")
	})
	require.NotEqual(t, -1, firstChunk)
	require.NotEqual(t, -1, toolUse)
	require.NotEqual(t, -1, chunkAfterTool)
	assert.Less(t, firstChunk, toolUse, "工具调用前的正文必须先到")
	assert.Less(t, toolUse, chunkAfterTool, "工具调用之后的正文必须后到")

	// 3) done 是最后一条内容事件之后才出现的,且它之前的正文已经全部 flush。
	done := idxOf(func(e chat_svc.ChatStreamEvent) bool { return e.Kind == chat_svc.StreamDone })
	require.NotEqual(t, -1, done, "收尾必须 emit done")
	assert.Less(t, chunkAfterTool, done, "done 之前不得残留未 flush 的正文")
}

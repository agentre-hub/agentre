package chat_svc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo/mock_llm_provider_repo"
	chatblocks "github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/view"
)

// directRunRunner 记录下发的 RunRequest 并回一个固定实际模型，供 runTurn 直连测试用。
// providerFallbackKey 非空时模拟远端 daemon 回传的回退信号(决策 9)。
type directRunRunner struct {
	request             chan agentruntime.RunRequest
	actualModel         string
	providerFallbackKey string
}

func (*directRunRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{}
}

func (r *directRunRunner) Run(_ context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	r.request <- req
	events := make(chan agentruntime.Event)
	close(events)
	return events, &agentruntime.RunResult{Model: r.actualModel, ProviderFallbackKey: r.providerFallbackKey}, nil
}

type directRunMocks struct {
	agent    *mock_agent_repo.MockAgentRepo
	backend  *mock_agent_backend_repo.MockAgentBackendRepo
	provider *mock_llm_provider_repo.MockLLMProviderRepo
	session  *mock_chat_repo.MockSessionRepo
	message  *mock_chat_repo.MockMessageRepo
}

func setupDirectRunTest(t *testing.T) (*directRunMocks, context.Context) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	m := &directRunMocks{
		agent:    mock_agent_repo.NewMockAgentRepo(ctrl),
		backend:  mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl),
		provider: mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl),
		session:  mock_chat_repo.NewMockSessionRepo(ctrl),
		message:  mock_chat_repo.NewMockMessageRepo(ctrl),
	}
	agent_repo.RegisterAgent(m.agent)
	agent_backend_repo.RegisterAgentBackend(m.backend)
	llm_provider_repo.RegisterLLMProvider(m.provider)
	chat_repo.RegisterSession(m.session)
	chat_repo.RegisterMessage(m.message)
	return m, context.Background()
}

// expectDirectResolvable 让直接跑 runTurn 的测试把传入的 prov 变成可被 ResolveTarget
// 解析的配置（EffectiveLLMConfig v1 seam）：runTurn 的 effectiveLLMForNonRemoteTurn 会
// 再经 ResolveTarget 查一次 FindByKey + FindModelByKey(DefaultModelKey)，测试必须把这些
// 查询也搭上，否则单次期望会打爆。
func expectDirectResolvable(m *directRunMocks, prov *llm_provider_entity.LLMProvider) {
	m.provider.EXPECT().FindByKey(gomock.Any(), prov.ProviderKey).Return(prov, nil).AnyTimes()
	m.provider.EXPECT().FindModelByKey(gomock.Any(), prov.DefaultModelKey).Return(
		&llm_provider_model_entity.LLMProviderModel{
			ModelKey: prov.DefaultModelKey, ModelID: "model-" + prov.ProviderKey,
			Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE,
		}, nil).AnyTimes()
}

// TestRunTurn_ProviderFallbackAppendsPersistentNotice 钉死决策 8：会话 provider_key
// 指向的供应商缺失/停用 → 本轮回退 agent 绑定，并在 assistant transcript 追加一条
// 持久 notice（结构化 {"providerKey":..}，前端 t() 渲染）。notice 随 runTurn 的
// turnExtras 携带，不依赖 result.Model（与已删除的模型偏离提示解耦）。
func TestRunTurn_ProviderFallbackAppendsPersistentNotice(t *testing.T) {
	m, ctx := setupDirectRunTest(t)
	var streamEvents []ChatStreamEvent
	s := NewChat(EmitterFunc(func(_ context.Context, _ string, payload any) {
		if event, ok := payload.(ChatStreamEvent); ok {
			streamEvents = append(streamEvents, event)
		}
	})).(*chatSvc)
	runner := &directRunRunner{
		request:     make(chan agentruntime.RunRequest, 1),
		actualModel: "actual-model",
	}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
	t.Cleanup(restore)

	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, ProviderKey: "gone-provider", Status: consts.ACTIVE,
	}
	a := &agent_entity.Agent{ID: 7, AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`}
	be := &agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}
	prov := &llm_provider_entity.LLMProvider{
		ProviderKey: "key-21", Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE,
		Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21",
	}
	expectDirectResolvable(m, prov)
	assistant := &chat_entity.Message{ID: 1001, SessionID: 100, Role: "assistant", BlocksJSON: "[]"}

	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	var persisted *chat_entity.Message
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msg *chat_entity.Message) error {
			msgCopy := *msg
			persisted = &msgCopy
			return nil
		}).AnyTimes()

	s.runTurn(ctx, sess, a, be, prov, nil, assistant, "stream", "", false, nil, turnExtras{
		providerFallbackNotice: &blocks.NoticeBlock{Level: "info", Text: view.EncodeProviderFallback("gone-provider", "")},
	})

	select {
	case req := <-runner.request:
		assert.Equal(t, "key-21", req.Provider.ProviderKey, "回退后 prov 应为 agent 绑定")
	default:
		t.Fatal("runtime did not receive a request")
	}
	require.NotNil(t, persisted)
	persistedBlocks, err := persisted.GetBlocks()
	require.NoError(t, err)
	require.Len(t, persistedBlocks, 1, "回退时 transcript 应追加一条持久 notice")
	notice, ok := persistedBlocks[0].(blocks.NoticeBlock)
	require.True(t, ok)
	assert.Equal(t, "info", notice.Level)
	var payload struct {
		ProviderKey string `json:"providerKey"`
	}
	require.NoError(t, json.Unmarshal([]byte(notice.Text), &payload))
	assert.Equal(t, "gone-provider", payload.ProviderKey, "结构化 payload 携带被回退的会话 provider_key")

	var liveNotice bool
	for _, event := range streamEvents {
		if event.Kind != StreamDone || event.Message == nil {
			continue
		}
		for _, block := range event.Message.Blocks {
			if block.Type == "notice" && block.ProviderKey == "gone-provider" {
				liveNotice = true
			}
		}
	}
	assert.True(t, liveNotice, "完成的流必须携带该持久 notice")
}

// TestRunTurn_NoModelDeviationNotice 钉死「不再产生模型偏离提示」：即使 runner 上报的
// 实际模型与请求模型不同,transcript 也不得再追加偏离 notice（#26 已整体移除,
// ChatBlock.selectedModel/actualModel 与 modelDeviationNotice 一并删除）。
func TestRunTurn_NoModelDeviationNotice(t *testing.T) {
	m, ctx := setupDirectRunTest(t)
	s := NewChat(NoopEmitter{}).(*chatSvc)
	runner := &directRunRunner{
		request:     make(chan agentruntime.RunRequest, 1),
		actualModel: "actual-model",
	}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
	t.Cleanup(restore)

	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, ProviderKey: "key-99", Status: consts.ACTIVE,
	}
	a := &agent_entity.Agent{ID: 7, AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`}
	be := &agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-99", Status: consts.ACTIVE,
	}
	prov := &llm_provider_entity.LLMProvider{
		ProviderKey: "key-99", Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE,
		Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-99",
	}
	expectDirectResolvable(m, prov)
	assistant := &chat_entity.Message{ID: 1001, SessionID: 100, Role: "assistant", BlocksJSON: "[]"}

	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	var persisted *chat_entity.Message
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msg *chat_entity.Message) error {
			msgCopy := *msg
			persisted = &msgCopy
			return nil
		}).AnyTimes()

	s.runTurn(ctx, sess, a, be, prov, nil, assistant, "stream", "", false, nil, turnExtras{})

	select {
	case req := <-runner.request:
		assert.Equal(t, "key-99", req.Provider.ProviderKey, "本轮 prov 应为会话所选供应商")
	default:
		t.Fatal("runtime did not receive a request")
	}
	require.NotNil(t, persisted)
	persistedBlocks, err := persisted.GetBlocks()
	require.NoError(t, err)
	require.Empty(t, persistedBlocks, "不再存在模型偏离提示,transcript 无 notice 块")
}

// TestRunTurn_RemoteFallbackSignalAppendsPersistentNotice 钉死决策 9 桌面侧收尾：远端
// daemon 自解 effectiveProviderKey 失败、回退 agent 绑定后经 ack → RunResult 回传的
// providerFallbackKey,runTurn 必须据此在 transcript 追加同一条持久 notice(与本地 Q3
// 一致);未回退时(result.ProviderFallbackKey 为空)不追加。
func TestRunTurn_RemoteFallbackSignalAppendsPersistentNotice(t *testing.T) {
	m, ctx := setupDirectRunTest(t)
	s := NewChat(NoopEmitter{}).(*chatSvc)
	runner := &directRunRunner{
		request:             make(chan agentruntime.RunRequest, 1),
		providerFallbackKey: "gone-provider",
	}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
	t.Cleanup(restore)

	sess := &chat_entity.Session{
		ID: 100, AgentID: 7, ProviderKey: "gone-provider", Status: consts.ACTIVE,
	}
	a := &agent_entity.Agent{ID: 7, AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`}
	be := &agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}
	prov := &llm_provider_entity.LLMProvider{
		ProviderKey: "key-21", Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE,
		Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21",
	}
	expectDirectResolvable(m, prov)
	assistant := &chat_entity.Message{ID: 1001, SessionID: 100, Role: "assistant", BlocksJSON: "[]"}

	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	var persisted *chat_entity.Message
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msg *chat_entity.Message) error {
			msgCopy := *msg
			persisted = &msgCopy
			return nil
		}).AnyTimes()

	s.runTurn(ctx, sess, a, be, prov, nil, assistant, "stream", "", false, nil, turnExtras{})

	select {
	case <-runner.request:
	default:
		t.Fatal("runtime did not receive a request")
	}
	require.NotNil(t, persisted)
	persistedBlocks, err := persisted.GetBlocks()
	require.NoError(t, err)
	require.Len(t, persistedBlocks, 1, "回退信号必须追加一条持久 notice")
	notice, ok := persistedBlocks[0].(blocks.NoticeBlock)
	require.True(t, ok)
	assert.Equal(t, "info", notice.Level)
	var payload struct {
		ProviderKey string `json:"providerKey"`
	}
	require.NoError(t, json.Unmarshal([]byte(notice.Text), &payload))
	assert.Equal(t, "gone-provider", payload.ProviderKey, "持久 notice 携带被回退的会话 provider_key")
}

// TestSessionProviderOverride_FallbackNoticeCarriesProviderName 钉死设计决策 2：会话
// 所选供应商还在(只是停用/类型不兼容)时,回退 notice 一并带上它的展示名 —— 与切换
// notice 同源同形,同一次改动覆盖。
func TestSessionProviderOverride_FallbackNoticeCarriesProviderName(t *testing.T) {
	m, ctx := setupDirectRunTest(t)
	s := NewChat(NoopEmitter{}).(*chatSvc)
	be := &agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
	}
	m.provider.EXPECT().FindByKey(ctx, "stale-key").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "stale-key", Name: "中转 · GLM 5.2",
		Type: string(llm_provider_entity.TypeAnthropic), Status: consts.DELETE,
	}, nil)

	_, notice, err := s.sessionProviderOverride(ctx, be, "stale-key", "", nil)

	require.NoError(t, err)
	require.NotNil(t, notice)
	assert.JSONEq(t, `{"providerKey":"stale-key","providerName":"中转 · GLM 5.2"}`, notice.Text)
}

// TestSessionProviderOverride_FallbackNoticeOmitsNameWhenProviderGone 钉死设计决策 2
// 的边界：供应商已被删(查不到实体)时没有名字可给,notice 保持只显示 key —— 前端据此
// 回退渲染,不能出现「有时有名有时是 UUID」的不稳定文案。
func TestSessionProviderOverride_FallbackNoticeOmitsNameWhenProviderGone(t *testing.T) {
	m, ctx := setupDirectRunTest(t)
	s := NewChat(NoopEmitter{}).(*chatSvc)
	be := &agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
	}
	m.provider.EXPECT().FindByKey(ctx, "gone-key").Return(nil, nil)

	_, notice, err := s.sessionProviderOverride(ctx, be, "gone-key", "", nil)

	require.NoError(t, err)
	require.NotNil(t, notice)
	assert.JSONEq(t, `{"providerKey":"gone-key"}`, notice.Text)
}

// TestLoadSession_OverlaysApprovalOntoInFlightTurnNotNoticeRow 钉死:轮中切换供应商会把
// 一条只承载 notice 的旁白行(appendProviderSwitchNotice,role=assistant,NextSeq 排在
// 在跑的 assistant 之后)追加进 transcript。LoadSession 里两处「末条 assistant」推导都
// 必须跳过它:
//   - ActiveStream 指到旁白行 → 重挂的前端订上一条没人 emit 的流名,余下的流式内容全
//     看不见、也等不到终态;
//   - 待决审批 overlay 挂到旁白行 → 前端把 pending 卡搬到那一行,resolved 事件按在跑
//     那条的 id 反扫 liveBlocks 落空 → 卡片永远 pending。
//
// producer 侧钉死:前端跳过旁白行的那一半(use-chat-session)单独修不好这个 —— 后端
// 一旦把审批块塞进旁白行,那行就不再「只有 notice」,前端反而正好挑中它。
func TestLoadSession_OverlaysApprovalOntoInFlightTurnNotNoticeRow(t *testing.T) {
	m, ctx := setupDirectRunTest(t)
	s := NewChat(NoopEmitter{}).(*chatSvc)

	noticeRow := &chat_entity.Message{ID: 43, SessionID: 9, Role: "assistant", Seq: 3}
	require.NoError(t, noticeRow.SetBlocks([]blocks.ContentBlock{blocks.NoticeBlock{
		Level: "info", Text: view.EncodeProviderSwitch("session-key", "", "中转 · GLM 5.2", ""),
	}}))

	m.session.EXPECT().Find(ctx, int64(9)).Return(&chat_entity.Session{
		ID: 9, AgentID: 7, AgentStatus: "running", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(ctx, int64(7)).Return(nil, nil)
	m.message.EXPECT().FillBlocks(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	m.message.EXPECT().ListMeta(ctx, int64(9)).Return([]*chat_entity.Message{
		{ID: 41, SessionID: 9, Role: "user", BlocksJSON: "[]", Seq: 1},
		{ID: 42, SessionID: 9, Role: "assistant", BlocksJSON: "[]", Seq: 2},
		noticeRow,
	}, nil)

	// 活跃 turn + 一条挂起审批:LoadSession 的 overlay 分支的两个前提。
	s.activeCancels.Store(int64(9), &activeTurnControl{})
	s.toolApprovals[9] = []*chatblocks.ToolApprovalBlock{
		{RequestID: "org-1", ToolName: "org_create_department", Status: "pending"},
	}

	resp, err := s.LoadSession(ctx, &LoadSessionRequest{SessionID: 9})
	require.NoError(t, err)

	assert.Equal(t, StreamName(9, 42), resp.Session.ActiveStream,
		"重挂的流名要指向在跑的那一轮,不是切换 notice 的旁白行")
	require.Len(t, resp.Messages, 3)
	assert.True(t, hasToolApprovalBlock(resp.Messages[1]),
		"待决审批 overlay 挂在在跑的 assistant(42)上")
	assert.False(t, hasToolApprovalBlock(resp.Messages[2]),
		"旁白行不该被塞进审批卡 —— 塞了它就不再『只有 notice』,前端的跳过也就跟着失效")
}

func hasToolApprovalBlock(cm ChatMessage) bool {
	for _, b := range cm.Blocks {
		if b.Type == "tool_approval" {
			return true
		}
	}
	return false
}

package chat_svc_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

func newBuiltinAgent(id, backendID int64) *agent_entity.Agent {
	return &agent_entity.Agent{ID: id, AgentBackendID: backendID, Status: consts.ACTIVE, PromptJSON: `[]`}
}

// newActiveProvider 构造一条可被 provider-default 解析的供应商：Enabled=On + 指向
// 一条启用默认模型的 DefaultModelKey（EffectiveLLMConfig v1 seam 的 ResolveTarget
// 前置条件）。
func newActiveProvider(key, ptype string) *llm_provider_entity.LLMProvider {
	return &llm_provider_entity.LLMProvider{
		ProviderKey: key, Type: ptype, Status: consts.ACTIVE,
		Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-" + key,
	}
}

// expectProviderResolvable 为「会话 provider_key > agent 绑定 → ResolveTarget 解析
// provider-default」这条链的默认模型查询搭 mock（EffectiveLLMConfig v1 seam）：
// ResolveTarget 解析空 ModelKey 时会查一次 FindModelByKey(DefaultModelKey)。
// FindByKey 由各测试显式 AnyTimes 提供（resolveAgentBackend / sessionProviderOverride /
// ResolveTarget 各会查一次），避免同一 key 重复注册。
func expectProviderResolvable(m *chatMocks, key string) {
	m.provider.EXPECT().FindModelByKey(gomock.Any(), "mk-"+key).Return(
		&llm_provider_model_entity.LLMProviderModel{
			ModelKey: "mk-" + key, ModelID: "model-" + key,
			Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE,
		}, nil).AnyTimes()
}

// TestSend_NewSession_PersistsAndValidatesProviderKey 钉死「新建会话 SendRequest.ProviderKey
// 校验 + 与 Session 一起 Create 落库」（决策 2）：
//   - 合法 key：随首条消息与 Session 一并落库，本轮 prov 即该供应商；
//   - 不存在 / 已停用 / 与后端 kind 不兼容：Send 直接报错，不落库（复用不可对话错误语义）。
func TestSend_NewSession_PersistsAndValidatesProviderKey(t *testing.T) {
	t.Run("valid key persists with session and drives the turn", func(t *testing.T) {
		m := setupChatTest(t)
		ctx := m.ctx
		runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
		t.Cleanup(restore)

		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(newBuiltinAgent(7, 12), nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
		}, nil)
		m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(newActiveProvider("key-21", string(llm_provider_entity.TypeAnthropic)), nil).AnyTimes()
		expectProviderResolvable(m, "key-99")
		m.provider.EXPECT().FindByKey(gomock.Any(), "key-99").Return(newActiveProvider("key-99", string(llm_provider_entity.TypeAnthropic)), nil).AnyTimes()

		m.session.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			assert.Equal(t, "key-99", s.ProviderKey, "所选供应商必须与 Session 一起落库")
			s.ID = 100
			return nil
		})

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
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).Times(1)
		m.dbMock.ExpectCommit()

		m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
		m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

		resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{AgentID: 7, Text: "hi", ProviderKey: "key-99"})
		require.NoError(t, err)
		chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

		select {
		case req := <-runner.requests:
			require.NotNil(t, req.Provider)
			assert.Equal(t, "key-99", req.Provider.ProviderKey, "本轮 prov 应取会话所选供应商")
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for runtime request")
		}
	})

	// 校验失败路径：claudecode CLI 登录后端（be.LLMProviderKey 为空）不强制 gateway，
	// 最干净地隔离「ProviderKey 校验」本身。
	setup := func(t *testing.T, provider *llm_provider_entity.LLMProvider) *chatMocks {
		m := setupChatTest(t)
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(newBuiltinAgent(7, 12), nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "", Status: consts.ACTIVE,
		}, nil)
		m.provider.EXPECT().FindByKey(gomock.Any(), "key-99").Return(provider, nil)
		return m
	}

	cases := []struct {
		name     string
		provider *llm_provider_entity.LLMProvider
	}{
		{"missing provider is rejected", nil},
		{"inactive provider is rejected", &llm_provider_entity.LLMProvider{
			ProviderKey: "key-99", Type: string(llm_provider_entity.TypeAnthropic), Status: 0,
		}},
		{"kind-incompatible provider is rejected", newActiveProvider("key-99", string(llm_provider_entity.TypeOpenAIResponse))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := setup(t, tc.provider)
			resp, err := m.svc.Send(m.ctx, &chat_svc.SendRequest{AgentID: 7, Text: "hi", ProviderKey: "key-99"})
			require.Nil(t, resp)
			require.Error(t, err, "非法 ProviderKey 必须在落库前报错")
			assert.NoError(t, m.dbMock.ExpectationsWereMet(), "校验失败不得发任何 DB 写")
		})
	}
}

// TestSend_NewSession_FixedModelPersistsAndDrivesTurn 钉死 spec 2026-08-11「新建与已有
// 会话流程」：新会话 SendRequest 带 ProviderKey + ModelKey（fixed-model）时，模型随首条
// 消息与 Session 一起校验并落库，本轮就用该固定模型解析（EffectiveLLMConfig v1）—— 不是
// 只落 provider 再等下一轮才生效。
func TestSend_NewSession_FixedModelPersistsAndDrivesTurn(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
	t.Cleanup(restore)

	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(newBuiltinAgent(7, 12), nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(newActiveProvider("key-21", string(llm_provider_entity.TypeAnthropic)), nil).AnyTimes()
	expectProviderResolvable(m, "key-21")
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-99").Return(newActiveProvider("key-99", string(llm_provider_entity.TypeAnthropic)), nil).AnyTimes()
	// 固定模型必须存在、启用且归属 key-99。
	m.provider.EXPECT().FindModelByKey(gomock.Any(), "mk-fixed").Return(
		&llm_provider_model_entity.LLMProviderModel{ProviderID: 0, ModelKey: "mk-fixed", ModelID: "claude-haiku-4-5", Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE},
		nil).AnyTimes()

	var created *chat_entity.Session
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
		created = s
		s.ID = 100
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
	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{AgentID: 7, Text: "hi", ProviderKey: "key-99", ModelKey: "mk-fixed"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	require.NotNil(t, created)
	assert.Equal(t, "key-99", created.ProviderKey)
	assert.Equal(t, "mk-fixed", created.ModelKey, "fixed-model 必须随首条消息与 Session 一起落库")

	select {
	case req := <-runner.requests:
		require.NotNil(t, req.Effective)
		assert.Equal(t, agentruntime.EffectiveModeFixedModel, req.Effective.Mode)
		assert.Equal(t, "mk-fixed", req.Effective.ModelKey, "本轮就按会话固定模型解析")
		assert.Equal(t, "claude-haiku-4-5", req.Effective.ModelID)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime request")
	}
}

// TestSend_NewSession_RejectsUnusableFixedModel 新建会话选了 fixed-model 时，模型缺失 /
// 停用 / 归属另一家供应商一律拒绝落库（与 SetChatSessionModelTarget 同一套校验口径）。
func TestSend_NewSession_RejectsUnusableFixedModel(t *testing.T) {
	cases := []struct {
		name  string
		model *llm_provider_model_entity.LLMProviderModel
	}{
		{name: "模型不存在", model: nil},
		{name: "模型已停用", model: &llm_provider_model_entity.LLMProviderModel{ProviderID: 0, ModelKey: "mk-fixed", ModelID: "haiku", Status: consts.ACTIVE}},
		{name: "模型归属另一家供应商", model: &llm_provider_model_entity.LLMProviderModel{ProviderID: 999, ModelKey: "mk-fixed", ModelID: "haiku", Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := setupChatTest(t)
			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(newBuiltinAgent(7, 12), nil)
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
			}, nil)
			m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(newActiveProvider("key-21", string(llm_provider_entity.TypeAnthropic)), nil).AnyTimes()
			m.provider.EXPECT().FindByKey(gomock.Any(), "key-99").Return(newActiveProvider("key-99", string(llm_provider_entity.TypeAnthropic)), nil).AnyTimes()
			m.provider.EXPECT().FindModelByKey(gomock.Any(), "mk-fixed").Return(tc.model, nil)

			resp, err := m.svc.Send(m.ctx, &chat_svc.SendRequest{AgentID: 7, Text: "hi", ProviderKey: "key-99", ModelKey: "mk-fixed"})
			require.Nil(t, resp)
			require.Error(t, err, "非法 fixed-model 必须在落库前报错")
			assert.NoError(t, m.dbMock.ExpectationsWereMet(), "校验失败不得发任何 DB 写")
		})
	}
}

// TestSend_ExistingSession_ProviderKeyOverridesAgentBinding 钉死决策 3：已有会话解析
// provider_key > agent 绑定。SendRequest.ProviderKey 对已有会话被忽略（B：不可事后改）。
func TestSend_ExistingSession_ProviderKeyOverridesAgentBinding(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", ProviderKey: "key-99", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(newBuiltinAgent(7, 12), nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(newActiveProvider("key-21", string(llm_provider_entity.TypeAnthropic)), nil).AnyTimes()
	expectProviderResolvable(m, "key-21")
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-99").Return(newActiveProvider("key-99", string(llm_provider_entity.TypeAnthropic)), nil).AnyTimes()
	expectProviderResolvable(m, "key-99")
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
		SessionID: 100, Text: "hi", ProviderKey: "ignored-key",
	})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	select {
	case req := <-runner.requests:
		require.NotNil(t, req.Provider)
		assert.Equal(t, "key-99", req.Provider.ProviderKey, "已有会话必须按会话 provider_key 解析,且忽略请求里的 ProviderKey")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime request")
	}
}

// TestSend_ExistingSession_NoProviderKeyUsesAgentBinding 钉死：无 provider_key 的会话
// 行为完全不变,仍按 agent 绑定解析(决策 1 硬不变式)。
func TestSend_ExistingSession_NoProviderKeyUsesAgentBinding(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", ProviderKey: "", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(newBuiltinAgent(7, 12), nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(newActiveProvider("key-21", string(llm_provider_entity.TypeAnthropic)), nil).AnyTimes()
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

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	select {
	case req := <-runner.requests:
		require.NotNil(t, req.Provider)
		assert.Equal(t, "key-21", req.Provider.ProviderKey, "无 provider_key 时按 agent 绑定解析")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime request")
	}
}

// TestRegenerate_ExistingSession_ProviderKeyOverridesAgentBinding 钉死决策 3：已有会话
// 的 Regenerate 重跑也必须按「会话 provider_key > agent 绑定」解析 prov —— 否则会话
// 选了供应商 X，重新生成的一轮却悄悄用 agent 绑定供应商，行为不一致。
func TestRegenerate_ExistingSession_ProviderKeyOverridesAgentBinding(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx

	runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", ProviderKey: "key-99", Status: consts.ACTIVE,
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
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(newActiveProvider("key-21", string(llm_provider_entity.TypeAnthropic)), nil).AnyTimes()
	expectProviderResolvable(m, "key-21")
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-99").Return(newActiveProvider("key-99", string(llm_provider_entity.TypeAnthropic)), nil).AnyTimes()
	expectProviderResolvable(m, "key-99")
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-99").Return(newActiveProvider("key-99", string(llm_provider_entity.TypeAnthropic)), nil).AnyTimes()
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
	assert.NotZero(t, resp.AssistantMessageID)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	select {
	case req := <-runner.requests:
		require.NotNil(t, req.Provider)
		assert.Equal(t, "key-99", req.Provider.ProviderKey, "Regenerate 重跑必须按会话 provider_key 解析,而非 agent 绑定")
	case <-time.After(2 * time.Second):
		t.Fatal("runtime never received the regenerated turn")
	}
}

// TestSend_ExistingSession_MissingSessionProviderFallsBackWithNotice 钉死决策 8：会话
// provider_key 指向的供应商缺失 → 本轮回退 agent 绑定,并追加一条持久 transcript notice;
// provider_key 不清除。
func TestSend_ExistingSession_MissingSessionProviderFallsBackWithNotice(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", ProviderKey: "gone-provider", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(newBuiltinAgent(7, 12), nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(newActiveProvider("key-21", string(llm_provider_entity.TypeAnthropic)), nil).AnyTimes()
	expectProviderResolvable(m, "key-21")
	m.provider.EXPECT().FindByKey(gomock.Any(), "gone-provider").Return(nil, nil)
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
	var persisted *chat_entity.Message
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msg *chat_entity.Message) error {
			msgCopy := *msg
			persisted = &msgCopy
			return nil
		}).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	select {
	case req := <-runner.requests:
		require.NotNil(t, req.Provider)
		assert.Equal(t, "key-21", req.Provider.ProviderKey, "供应商缺失时应回退 agent 绑定")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime request")
	}

	require.NotNil(t, persisted, "assistant 消息应被持久化")
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
	assert.True(t, noticeFound, "回退时 transcript 必须追加一条持久 notice,携带被回退的 provider_key")
}

// TestSend_ExistingSession_DisabledProviderDefaultsFallsBack 钉死 spec 2026-08-11
// 决策 3/7：enabled 是独立于软删除 status 的「可运行状态」。provider-default 会话钉的
// 供应商被**禁用**（enabled=false、status=ACTIVE）时，与 #39 同款回退语义 —— 本轮回退
// agent 绑定并追加持久 notice（不是配置损坏，不阻止；只有「Provider 存在但默认模型非法」
// 才阻止）。
func TestSend_ExistingSession_DisabledProviderDefaultsFallsBack(t *testing.T) {
	m := setupChatTest(t)
	ctx := m.ctx
	runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
	t.Cleanup(restore)

	// 会话钉的 provider 存在但 enabled=false（被用户停用），status 仍是 ACTIVE。
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", ProviderKey: "disabled-provider", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(newBuiltinAgent(7, 12), nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(newActiveProvider("key-21", string(llm_provider_entity.TypeAnthropic)), nil).AnyTimes()
	expectProviderResolvable(m, "key-21")
	m.provider.EXPECT().FindByKey(gomock.Any(), "disabled-provider").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "disabled-provider", Type: string(llm_provider_entity.TypeAnthropic),
		Enabled: llm_provider_entity.EnabledOff, Status: consts.ACTIVE,
	}, nil).AnyTimes()
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
	var persisted *chat_entity.Message
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msg *chat_entity.Message) error {
			msgCopy := *msg
			persisted = &msgCopy
			return nil
		}).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, Text: "hi"})
	require.NoError(t, err, "provider-default 的停用供应商应回退 agent 绑定，不阻止本轮")
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	select {
	case req := <-runner.requests:
		require.NotNil(t, req.Provider)
		assert.Equal(t, "key-21", req.Provider.ProviderKey, "停用供应商应回退 agent 绑定")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime request")
	}

	require.NotNil(t, persisted, "assistant 消息应被持久化")
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
		if json.Unmarshal([]byte(nb.Text), &payload) == nil && payload.ProviderKey == "disabled-provider" {
			noticeFound = true
		}
	}
	assert.True(t, noticeFound, "回退时 transcript 必须追加一条持久 notice,携带被回退的 provider_key")
}

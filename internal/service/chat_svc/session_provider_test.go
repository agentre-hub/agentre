package chat_svc_test

import (
	"context"
	"errors"
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
	"github.com/agentre-hub/agentre/internal/pkg/httpgateway"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// 本文件覆盖 docs/specs/2026-08-10-session-provider-switch.md 的会话级供应商切换
// （决策 1 / 11、「切换流程与生效时机」的写入与校验半边）与「有效供应商解析（唯一
// 口径）」在展示侧的两个消费点（LoadSession 展示字段、复制启动命令）。

// expectSwitchBackend 搭一条「会话 → agent → backend」的解析链：切换入口按会话钉住
// 的那一档（没钉住则 agent 默认档）校验供应商与后端 kind 是否匹配。
func expectSwitchBackend(
	m *chatMocks,
	ctx context.Context,
	sess *chat_entity.Session,
	backendType agent_backend_entity.BackendType,
	agentProviderKey string,
) {
	m.session.EXPECT().Find(ctx, sess.ID).Return(sess, nil)
	m.agent.EXPECT().Find(ctx, sess.AgentID).Return(&agent_entity.Agent{
		ID: sess.AgentID, AgentBackendID: 12, Status: consts.ACTIVE,
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(backendType), LLMProviderKey: agentProviderKey, Status: consts.ACTIVE,
	}, nil)
}

// noticeTextOf 取出一条消息里唯一那个 notice 块的 Text（持久化的结构化负载原文）。
func noticeTextOf(t *testing.T, msg *chat_entity.Message) string {
	t.Helper()
	require.NotNil(t, msg, "应该新建了一条承载 notice 的消息")
	bs, err := msg.GetBlocks()
	require.NoError(t, err)
	require.Len(t, bs, 1, "切换 notice 消息只带一个 notice 块")
	nb, ok := bs[0].(blocks.NoticeBlock)
	require.True(t, ok, "块类型应为 NoticeBlock")
	return nb.Text
}

// noticeLevelOf 取出一条消息里唯一那个 notice 块的 Level。
func noticeLevelOf(t *testing.T, msg *chat_entity.Message) string {
	t.Helper()
	require.NotNil(t, msg, "应该新建了一条承载 notice 的消息")
	bs, err := msg.GetBlocks()
	require.NoError(t, err)
	require.Len(t, bs, 1, "切换 notice 消息只带一个 notice 块")
	nb, ok := bs[0].(blocks.NoticeBlock)
	require.True(t, ok, "块类型应为 NoticeBlock")
	return nb.Level
}

// TestSetChatSessionModelTarget_PersistsAndAppendsNotice：provider-default 切换（非空
// providerKey + 空 modelKey）→ 同一条原子语句写 provider_key + model_key（spec 2026-08-11
// 决策 1）+ 向 transcript 追加一条持久 notice（决策 9），notice 是结构化负载而不是现成文案
// （前端走 t() 渲染），仍用既有 kind=switch。
func TestSetChatSessionModelTarget_PersistsAndAppendsNotice(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "running", Status: consts.ACTIVE}
	expectSwitchBackend(m, ctx, sess, agent_backend_entity.TypeClaudeCode, "agent-bound")
	m.provider.EXPECT().FindByKey(ctx, "session-key").Return(&llm_provider_entity.LLMProvider{
		ID: 33, ProviderKey: "session-key", Type: string(llm_provider_entity.TypeAnthropic),
		Enabled: llm_provider_entity.EnabledOn, Status: consts.ACTIVE,
	}, nil)
	m.session.EXPECT().UpdateModelTarget(ctx, int64(100), "session-key", "").Return(nil)
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(7, nil)
	var created *chat_entity.Message
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msg *chat_entity.Message) error {
			created = msg
			return nil
		})

	resp, err := m.svc.SetChatSessionModelTarget(ctx, &chat_svc.SetChatSessionModelTargetRequest{
		SessionID: 100, ProviderKey: "session-key",
	})
	require.NoError(t, err)
	assert.Equal(t, "session-key", resp.ProviderKey)
	assert.Empty(t, resp.ModelKey, "provider-default 落库的 modelKey 恒为空")
	assert.Equal(t, "agent-bound", resp.AgentProviderKey, "回传 agent 绑定 key 供 pill 渲染回落标签")
	assert.Equal(t, 7, created.Seq)
	assert.Equal(t, `{"providerKey":"session-key","kind":"switch"}`, noticeTextOf(t, created))
}

// TestSetChatSessionModelTarget_FixedModelPersistsAndAppendsNotice：fixed-model 切换
// （双 key 非空）→ 原子写两列，notice 负载带上 modelKey + 模型显示名（后端产出时已解析
// 到的实体，非前端按 key 查表）。
func TestSetChatSessionModelTarget_FixedModelPersistsAndAppendsNotice(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "running", Status: consts.ACTIVE}
	expectSwitchBackend(m, ctx, sess, agent_backend_entity.TypeClaudeCode, "agent-bound")
	m.provider.EXPECT().FindByKey(ctx, "session-key").Return(&llm_provider_entity.LLMProvider{
		ID: 33, ProviderKey: "session-key", Name: "中转 · GLM 5.2", Type: string(llm_provider_entity.TypeAnthropic),
		Enabled: llm_provider_entity.EnabledOn, Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindModelByKey(ctx, "mk-haiku").Return(&llm_provider_model_entity.LLMProviderModel{
		ProviderID: 33, ModelKey: "mk-haiku", ModelID: "glm-5.2", Name: "GLM 5.2",
		Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE,
	}, nil)
	m.session.EXPECT().UpdateModelTarget(ctx, int64(100), "session-key", "mk-haiku").Return(nil)
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(7, nil)
	var created *chat_entity.Message
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msg *chat_entity.Message) error {
			created = msg
			return nil
		})

	resp, err := m.svc.SetChatSessionModelTarget(ctx, &chat_svc.SetChatSessionModelTargetRequest{
		SessionID: 100, ProviderKey: "session-key", ModelKey: "mk-haiku",
	})
	require.NoError(t, err)
	assert.Equal(t, "session-key", resp.ProviderKey)
	assert.Equal(t, "mk-haiku", resp.ModelKey)
	assert.Equal(t, `{"providerKey":"session-key","providerName":"中转 · GLM 5.2","modelKey":"mk-haiku","modelName":"GLM 5.2","kind":"switch"}`, noticeTextOf(t, created))
}

// TestSetChatSessionModelTarget_ClearsBackToAgentBinding：双空 = 改回跟随 agent 绑定
// （inherit-agent）；不查供应商，notice 说明的是「跟随绑定」。
func TestSetChatSessionModelTarget_ClearsBackToAgentBinding(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	sess := &chat_entity.Session{ID: 100, AgentID: 7, Status: consts.ACTIVE, ProviderKey: "session-key", ModelKey: "mk-haiku"}
	expectSwitchBackend(m, ctx, sess, agent_backend_entity.TypeClaudeCode, "")
	m.session.EXPECT().UpdateModelTarget(ctx, int64(100), "", "").Return(nil)
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil)
	var created *chat_entity.Message
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msg *chat_entity.Message) error {
			created = msg
			return nil
		})

	resp, err := m.svc.SetChatSessionModelTarget(ctx, &chat_svc.SetChatSessionModelTargetRequest{
		SessionID: 100, ProviderKey: "", ModelKey: "",
	})
	require.NoError(t, err)
	assert.Empty(t, resp.ProviderKey)
	assert.Empty(t, resp.ModelKey)
	assert.Equal(t, `{"kind":"switch"}`, noticeTextOf(t, created))
}

// TestSetChatSessionModelTarget_NoOpWhenUnchanged：选中当前已生效的同一**完整组合**
// （ProviderKey + ModelKey 都相同，spec 决策 1）不写库、也不追加 notice —— 否则每点一次
// 就往 transcript 里塞一条「已改用 X」，而实际上什么都没变。mock 上没有 UpdateModelTarget /
// NextSeq / Create 期望。
func TestSetChatSessionModelTarget_NoOpWhenUnchanged(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	sess := &chat_entity.Session{ID: 100, AgentID: 7, Status: consts.ACTIVE, ProviderKey: "session-key", ModelKey: "mk-haiku"}
	expectSwitchBackend(m, ctx, sess, agent_backend_entity.TypeClaudeCode, "agent-bound")

	resp, err := m.svc.SetChatSessionModelTarget(ctx, &chat_svc.SetChatSessionModelTargetRequest{
		SessionID: 100, ProviderKey: "session-key", ModelKey: "mk-haiku",
	})
	require.NoError(t, err)
	assert.Equal(t, "session-key", resp.ProviderKey)
	assert.Equal(t, "mk-haiku", resp.ModelKey)
	assert.Equal(t, "agent-bound", resp.AgentProviderKey)
}

// TestSetChatSessionModelTarget_NoOpComparesCompletePair：no-op 必须比较完整组合 ——
// 会话当前是 fixed-model(session-key/mk-haiku)，用户改选同一 provider 的 provider-default
// （modelKey 空）是不同的目标，必须写入并把 modelKey 清空，不能误判为 no-op。
func TestSetChatSessionModelTarget_NoOpComparesCompletePair(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	sess := &chat_entity.Session{ID: 100, AgentID: 7, Status: consts.ACTIVE, ProviderKey: "session-key", ModelKey: "mk-haiku"}
	expectSwitchBackend(m, ctx, sess, agent_backend_entity.TypeClaudeCode, "agent-bound")
	m.provider.EXPECT().FindByKey(ctx, "session-key").Return(&llm_provider_entity.LLMProvider{
		ID: 33, ProviderKey: "session-key", Type: string(llm_provider_entity.TypeAnthropic),
		Enabled: llm_provider_entity.EnabledOn, Status: consts.ACTIVE,
	}, nil)
	m.session.EXPECT().UpdateModelTarget(ctx, int64(100), "session-key", "").Return(nil)
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(7, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	resp, err := m.svc.SetChatSessionModelTarget(ctx, &chat_svc.SetChatSessionModelTargetRequest{
		SessionID: 100, ProviderKey: "session-key", ModelKey: "",
	})
	require.NoError(t, err)
	assert.Equal(t, "session-key", resp.ProviderKey)
	assert.Empty(t, resp.ModelKey)
}

// TestSetChatSessionModelTarget_RejectsUnusableProvider：决策 2/3 —— 复用新建会话那套校验，
// 不通过一律拒绝写库（会话保持原 target），也不产出 notice。mock 上没有 UpdateModelTarget /
// Create 期望，真写了就会失败。
func TestSetChatSessionModelTarget_RejectsUnusableProvider(t *testing.T) {
	cases := []struct {
		name     string
		backend  agent_backend_entity.BackendType
		provider *llm_provider_entity.LLMProvider
	}{
		{name: "供应商不存在", backend: agent_backend_entity.TypeClaudeCode, provider: nil},
		{
			name:    "供应商已停用",
			backend: agent_backend_entity.TypeClaudeCode,
			provider: &llm_provider_entity.LLMProvider{
				ID: 33, ProviderKey: "session-key", Type: string(llm_provider_entity.TypeAnthropic),
				Status: consts.DELETE,
			},
		},
		{
			name:    "供应商类型与后端 kind 不兼容",
			backend: agent_backend_entity.TypeClaudeCode,
			provider: &llm_provider_entity.LLMProvider{
				ID: 34, ProviderKey: "session-key", Type: string(llm_provider_entity.TypeOpenAIResponse),
				Status: consts.ACTIVE,
			},
		},
		{
			name:    "供应商已禁用(Enabled=Off)",
			backend: agent_backend_entity.TypeClaudeCode,
			provider: &llm_provider_entity.LLMProvider{
				ID: 35, ProviderKey: "session-key", Type: string(llm_provider_entity.TypeAnthropic),
				Enabled: llm_provider_entity.EnabledOff, Status: consts.ACTIVE,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := setupChatTest(t)
			ctx := context.Background()

			sess := &chat_entity.Session{ID: 100, AgentID: 7, Status: consts.ACTIVE, ProviderKey: "old-key"}
			expectSwitchBackend(m, ctx, sess, tc.backend, "agent-bound")
			m.provider.EXPECT().FindByKey(ctx, "session-key").Return(tc.provider, nil)

			_, err := m.svc.SetChatSessionModelTarget(ctx, &chat_svc.SetChatSessionModelTargetRequest{
				SessionID: 100, ProviderKey: "session-key",
			})
			assert.Error(t, err)
		})
	}
}

// TestSetChatSessionModelTarget_RejectsUnusableModel：fixed-model 的 Model 必须存在、
// 启用且归属所选供应商；缺失/停用/归属错误一律拒绝写库。
func TestSetChatSessionModelTarget_RejectsUnusableModel(t *testing.T) {
	cases := []struct {
		name  string
		model *llm_provider_model_entity.LLMProviderModel
	}{
		{
			name:  "模型不存在",
			model: nil,
		},
		{
			name: "模型已停用",
			model: &llm_provider_model_entity.LLMProviderModel{
				ProviderID: 33, ModelKey: "mk-haiku", ModelID: "haiku", Status: consts.ACTIVE,
			},
		},
		{
			name: "模型归属另一家供应商",
			model: &llm_provider_model_entity.LLMProviderModel{
				ProviderID: 999, ModelKey: "mk-haiku", ModelID: "haiku",
				Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := setupChatTest(t)
			ctx := context.Background()

			sess := &chat_entity.Session{ID: 100, AgentID: 7, Status: consts.ACTIVE}
			expectSwitchBackend(m, ctx, sess, agent_backend_entity.TypeClaudeCode, "agent-bound")
			m.provider.EXPECT().FindByKey(ctx, "session-key").Return(&llm_provider_entity.LLMProvider{
				ID: 33, ProviderKey: "session-key", Type: string(llm_provider_entity.TypeAnthropic),
				Enabled: llm_provider_entity.EnabledOn, Status: consts.ACTIVE,
			}, nil)
			m.provider.EXPECT().FindModelByKey(ctx, "mk-haiku").Return(tc.model, nil)

			_, err := m.svc.SetChatSessionModelTarget(ctx, &chat_svc.SetChatSessionModelTargetRequest{
				SessionID: 100, ProviderKey: "session-key", ModelKey: "mk-haiku",
			})
			assert.Error(t, err)
		})
	}
}

// TestSetChatSessionModelTarget_Boundaries：边界。
func TestSetChatSessionModelTarget_Boundaries(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	_, err := m.svc.SetChatSessionModelTarget(ctx, &chat_svc.SetChatSessionModelTargetRequest{SessionID: 0})
	assert.Error(t, err, "SessionID <= 0 → InvalidParameter")

	m.session.EXPECT().Find(ctx, int64(404)).Return(nil, nil)
	_, err = m.svc.SetChatSessionModelTarget(ctx, &chat_svc.SetChatSessionModelTargetRequest{SessionID: 404})
	assert.Error(t, err, "会话不存在 → ChatSessionNotFound")
}

// TestSetChatSessionModelTarget_RejectsModelWithoutProvider：modelKey 单独出现（providerKey
// 空）是畸形目标 —— fixed-model 必须有 provider，不能落出「没有 provider 的固定模型」这种
// 下一轮必失败的会话。
func TestSetChatSessionModelTarget_RejectsModelWithoutProvider(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	sess := &chat_entity.Session{ID: 100, AgentID: 7, Status: consts.ACTIVE}
	expectSwitchBackend(m, ctx, sess, agent_backend_entity.TypeClaudeCode, "agent-bound")

	_, err := m.svc.SetChatSessionModelTarget(ctx, &chat_svc.SetChatSessionModelTargetRequest{
		SessionID: 100, ProviderKey: "", ModelKey: "mk-fixed",
	})
	assert.Error(t, err, "modelKey 非空 + providerKey 空 → 拒绝写库")
}

// TestLoadSession_DisplaysEffectiveProvider：展示口径 —— 会话选了供应商时，供应商类型
// 与上下文窗口按 effective provider（会话 > agent 绑定）算，而不是 agent 绑定；同时把
// 会话 key 与 agent 绑定 key 一并回传给前端渲染 pill。
func TestLoadSession_DisplaysEffectiveProvider(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	m.session.EXPECT().Find(ctx, int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, Status: consts.ACTIVE, ProviderKey: "session-key", ModelKey: "mk-session-key",
	}, nil)
	m.agent.EXPECT().Find(ctx, int64(7)).Return(&agent_entity.Agent{
		ID: 7, AgentBackendID: 12, Status: consts.ACTIVE,
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "agent-bound", LLMModelKey: "mk-agent-bound", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(ctx, "agent-bound").Return(&llm_provider_entity.LLMProvider{
		ID: 33, ProviderKey: "agent-bound", Type: string(llm_provider_entity.TypeOpenAIChat),
		Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-agent-bound", Status: consts.ACTIVE,
	}, nil).AnyTimes()
	m.provider.EXPECT().FindModelByKey(ctx, "mk-agent-bound").Return(
		&llm_provider_model_entity.LLMProviderModel{ModelKey: "mk-agent-bound", ModelID: "gpt-5", ContextWindow: 111_000, Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE},
		nil).AnyTimes()
	m.provider.EXPECT().FindByKey(ctx, "session-key").Return(&llm_provider_entity.LLMProvider{
		ID: 34, ProviderKey: "session-key", Type: string(llm_provider_entity.TypeAnthropic),
		Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-session-key", Status: consts.ACTIVE,
	}, nil).AnyTimes()
	m.provider.EXPECT().FindModelByKey(ctx, "mk-session-key").Return(
		&llm_provider_model_entity.LLMProviderModel{ProviderID: 34, ModelKey: "mk-session-key", ModelID: "claude-sonnet-4-6", ContextWindow: 222_000, Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE},
		nil).AnyTimes()
	expectTranscriptWindowFilled(m)
	m.message.EXPECT().ListMeta(ctx, int64(100)).Return(nil, nil)

	resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 100})
	require.NoError(t, err)
	assert.Equal(t, string(llm_provider_entity.TypeAnthropic), resp.Session.LLMProviderType,
		"用量口径必须按会话实际会用的供应商算")
	assert.Equal(t, 222_000, resp.Session.ContextWindow, "上下文窗口同样按 effective provider 算")
	assert.Equal(t, "session-key", resp.Session.ProviderKey)
	assert.Equal(t, "agent-bound", resp.Session.AgentProviderKey)
	assert.Equal(t, "mk-session-key", resp.Session.ModelKey, "会话钉的 fixed-model key 随展示透传给前端水合 pill")
	assert.Equal(t, "mk-agent-bound", resp.Session.AgentModelKey, "agent 绑定固定模型 key 随展示透传给前端渲染「跟随绑定」")
}

// TestLoadSession_FallsBackToAgentBindingForDisplay：会话 provider_key 为空时展示完全
// 不变（硬不变量 1）—— 仍按 agent 绑定解析。
func TestLoadSession_FallsBackToAgentBindingForDisplay(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	expectLoadSessionBackend(m, ctx, 100, 7, 12, agent_backend_entity.TypeClaudeCode,
		&llm_provider_entity.LLMProvider{
			ID: 33, ProviderKey: "agent-bound", Type: string(llm_provider_entity.TypeAnthropic),
			Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-agent-bound", Status: consts.ACTIVE,
		})
	m.provider.EXPECT().FindModelByKey(ctx, "mk-agent-bound").Return(
		&llm_provider_model_entity.LLMProviderModel{ModelKey: "mk-agent-bound", ModelID: "claude-opus-4-1", ContextWindow: 111_000, Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE},
		nil).AnyTimes()

	resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 100})
	require.NoError(t, err)
	assert.Equal(t, string(llm_provider_entity.TypeAnthropic), resp.Session.LLMProviderType)
	assert.Equal(t, 111_000, resp.Session.ContextWindow)
	assert.Empty(t, resp.Session.ProviderKey, "未选具体供应商 → 会话 key 为空，前端显示 agent 绑定名")
	assert.Equal(t, "agent-bound", resp.Session.AgentProviderKey)
}

// TestGetLaunchCommand_UsesEffectiveProvider：复制启动命令按 effective provider 解析，
// 复制出的命令与该会话实际执行一致（问题 5）。
func TestGetLaunchCommand_UsesEffectiveProvider(t *testing.T) {
	t.Setenv("AGENTRE_DATA_DIR", t.TempDir())
	chat_svc.RegisterGateway(&fakeChatGateway{
		status: httpgateway.GatewayStatus{State: "running", URL: "http://127.0.0.1:60080"},
	})
	t.Cleanup(func() { chat_svc.RegisterGateway(nil) })

	m := setupChatTest(t)
	ctx := context.Background()

	m.session.EXPECT().Find(ctx, int64(3)).Return(&chat_entity.Session{
		ID: 3, AgentID: 7, ProviderSessionID: "sess-uuid", Status: consts.ACTIVE, ProviderKey: "session-key",
	}, nil)
	m.agent.EXPECT().Find(ctx, int64(7)).Return(&agent_entity.Agent{
		ID: 7, AgentBackendID: 22, Status: consts.ACTIVE,
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(22)).Return(&agent_backend_entity.AgentBackend{
		ID: 22, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "agent-bound", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(ctx, "agent-bound").Return(&llm_provider_entity.LLMProvider{
		ID: 33, ProviderKey: "agent-bound", Type: string(llm_provider_entity.TypeAnthropic),
		Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-agent-bound", Status: consts.ACTIVE,
	}, nil).AnyTimes()
	m.provider.EXPECT().FindModelByKey(ctx, "mk-agent-bound").Return(
		&llm_provider_model_entity.LLMProviderModel{ModelKey: "mk-agent-bound", ModelID: "claude-opus-4-1", Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE},
		nil).AnyTimes()
	m.provider.EXPECT().FindByKey(ctx, "session-key").Return(&llm_provider_entity.LLMProvider{
		ID: 34, ProviderKey: "session-key", Type: string(llm_provider_entity.TypeAnthropic),
		Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-session-key", Status: consts.ACTIVE,
	}, nil).AnyTimes()
	m.provider.EXPECT().FindModelByKey(ctx, "mk-session-key").Return(
		&llm_provider_model_entity.LLMProviderModel{ModelKey: "mk-session-key", ModelID: "claude-sonnet-4-6", Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE},
		nil).AnyTimes()

	resp, err := m.svc.GetLaunchCommand(ctx, &chat_svc.LaunchCommandRequest{SessionID: 3})
	require.NoError(t, err)
	assert.Contains(t, resp.Command, "--model claude-sonnet-4-6", "命令要用会话所选供应商的模型")
	assert.NotContains(t, resp.Command, "claude-opus-4-1")
}

// 以下覆盖 docs/specs/2026-09-01-session-reasoning-effort.md 的会话级思考力度切换
// （决策 1 / 7、「生效时机」「no-op」「转录提示」与「失败与恢复」）。

// TestSetChatSessionReasoningEffort_PersistsAndAppendsNotice：写单列 reasoning_effort
// （决策 1）+ 向 transcript 追加一条 info 级持久 notice（决策 7），notice 带上切换后的
// 档位，走既有 notice 通道的结构化负载而不是现成文案。
func TestSetChatSessionReasoningEffort_PersistsAndAppendsNotice(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "running", Status: consts.ACTIVE}
	expectSwitchBackend(m, ctx, sess, agent_backend_entity.TypeCodex, "agent-bound")
	m.session.EXPECT().UpdateReasoningEffort(ctx, int64(100), "max").Return(nil)
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(7, nil)
	var created *chat_entity.Message
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msg *chat_entity.Message) error {
			created = msg
			return nil
		})

	resp, err := m.svc.SetChatSessionReasoningEffort(ctx, &chat_svc.SetChatSessionReasoningEffortRequest{
		SessionID: 100, ReasoningEffort: "max",
	})
	require.NoError(t, err)
	assert.Equal(t, "max", resp.ReasoningEffort)
	assert.Equal(t, 7, created.Seq)
	assert.Equal(t, `{"kind":"reasoning_effort","reasoningEffort":"max"}`, noticeTextOf(t, created),
		"notice 负载必须带上切换到的档位，且走既有 notice 通道的结构化形态")
	assert.Equal(t, "info", noticeLevelOf(t, created))
}

// TestSetChatSessionReasoningEffort_ClearsBackToBackendDefault：空串 = 改回「跟随后端
// 配置」，照常写库并落 notice；回传该会话那一档 backend 的配置档位，供弹层的「→ 跟随
// 后端配置 · <档位>」解析副行渲染（决策 11）。
func TestSetChatSessionReasoningEffort_ClearsBackToBackendDefault(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	sess := &chat_entity.Session{ID: 100, AgentID: 7, Status: consts.ACTIVE, ReasoningEffort: "high"}
	m.session.EXPECT().Find(ctx, sess.ID).Return(sess, nil)
	m.agent.EXPECT().Find(ctx, sess.AgentID).Return(&agent_entity.Agent{
		ID: sess.AgentID, AgentBackendID: 12, Status: consts.ACTIVE,
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeCodex), ReasoningEffort: "medium", Status: consts.ACTIVE,
	}, nil)
	m.session.EXPECT().UpdateReasoningEffort(ctx, int64(100), "").Return(nil)
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil)
	var created *chat_entity.Message
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msg *chat_entity.Message) error {
			created = msg
			return nil
		})

	resp, err := m.svc.SetChatSessionReasoningEffort(ctx, &chat_svc.SetChatSessionReasoningEffortRequest{
		SessionID: 100, ReasoningEffort: "",
	})
	require.NoError(t, err)
	assert.Empty(t, resp.ReasoningEffort)
	assert.Equal(t, "medium", resp.BackendReasoningEffort, "回传后端配置档位供「跟随后端配置」解析副行")
	assert.Equal(t, `{"kind":"reasoning_effort"}`, noticeTextOf(t, created),
		"改回跟随后端配置照样落痕迹：空档由 kind 承载，不退化成看不出来的空 notice")
}

// TestSetChatSessionReasoningEffort_NoOpWhenUnchanged：选中的就是**会话行上**已有的
// 同一个值 → 不写库、不落 notice（mock 上没有 UpdateReasoningEffort / NextSeq / Create
// 期望，真写了就会失败）。
func TestSetChatSessionReasoningEffort_NoOpWhenUnchanged(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	sess := &chat_entity.Session{ID: 100, AgentID: 7, Status: consts.ACTIVE, ReasoningEffort: "high"}
	expectSwitchBackend(m, ctx, sess, agent_backend_entity.TypeCodex, "agent-bound")

	resp, err := m.svc.SetChatSessionReasoningEffort(ctx, &chat_svc.SetChatSessionReasoningEffortRequest{
		SessionID: 100, ReasoningEffort: "high",
	})
	require.NoError(t, err)
	assert.Equal(t, "high", resp.ReasoningEffort)
}

// TestSetChatSessionReasoningEffort_ComparesSessionRowNotEffective：比较的是会话行上的
// 值而非有效档位 —— 会话行为空、后端配置是 high 时显式选 high 是一次真实写入（把「跟随
// 后端」钉成「就是 high」），不是 no-op。
func TestSetChatSessionReasoningEffort_ComparesSessionRowNotEffective(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	sess := &chat_entity.Session{ID: 100, AgentID: 7, Status: consts.ACTIVE, ReasoningEffort: ""}
	m.session.EXPECT().Find(ctx, sess.ID).Return(sess, nil)
	m.agent.EXPECT().Find(ctx, sess.AgentID).Return(&agent_entity.Agent{
		ID: sess.AgentID, AgentBackendID: 12, Status: consts.ACTIVE,
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeCodex), ReasoningEffort: "high", Status: consts.ACTIVE,
	}, nil)
	m.session.EXPECT().UpdateReasoningEffort(ctx, int64(100), "high").Return(nil)
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(4, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	resp, err := m.svc.SetChatSessionReasoningEffort(ctx, &chat_svc.SetChatSessionReasoningEffortRequest{
		SessionID: 100, ReasoningEffort: "high",
	})
	require.NoError(t, err)
	assert.Equal(t, "high", resp.ReasoningEffort)
}

// TestSetChatSessionReasoningEffort_RejectsInvalidEffort：非法档位按既有校验拒绝并原样
// 报错（复用已有错误码），会话保持原档位 —— mock 上没有任何写入期望。
func TestSetChatSessionReasoningEffort_RejectsInvalidEffort(t *testing.T) {
	for _, effort := range []string{"ultra", "HIGH", "off", "minimal"} {
		t.Run(effort, func(t *testing.T) {
			m := setupChatTest(t)
			ctx := context.Background()

			_, err := m.svc.SetChatSessionReasoningEffort(ctx, &chat_svc.SetChatSessionReasoningEffortRequest{
				SessionID: 100, ReasoningEffort: effort,
			})
			assert.Error(t, err, "非法档位不得写库")
		})
	}
}

// TestSetChatSessionReasoningEffort_Boundaries：SessionID <= 0 → InvalidParameter；
// 会话不存在 → ChatSessionNotFound，不折成成功。
func TestSetChatSessionReasoningEffort_Boundaries(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	_, err := m.svc.SetChatSessionReasoningEffort(ctx, &chat_svc.SetChatSessionReasoningEffortRequest{SessionID: 0, ReasoningEffort: "high"})
	assert.Error(t, err, "SessionID <= 0 → InvalidParameter")

	m.session.EXPECT().Find(ctx, int64(404)).Return(nil, nil)
	_, err = m.svc.SetChatSessionReasoningEffort(ctx, &chat_svc.SetChatSessionReasoningEffortRequest{SessionID: 404, ReasoningEffort: "high"})
	assert.Error(t, err, "会话不存在 → ChatSessionNotFound")
}

// TestSetChatSessionReasoningEffort_NoticeFailureStillSucceeds：notice 写失败只记日志，
// 切换本身仍报成功 —— 库里的档位已经改了，报成失败会让用户重试并追加第二条痕迹。
func TestSetChatSessionReasoningEffort_NoticeFailureStillSucceeds(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	sess := &chat_entity.Session{ID: 100, AgentID: 7, Status: consts.ACTIVE}
	expectSwitchBackend(m, ctx, sess, agent_backend_entity.TypeCodex, "agent-bound")
	m.session.EXPECT().UpdateReasoningEffort(ctx, int64(100), "low").Return(nil)
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(0, errors.New("boom"))

	resp, err := m.svc.SetChatSessionReasoningEffort(ctx, &chat_svc.SetChatSessionReasoningEffortRequest{
		SessionID: 100, ReasoningEffort: "low",
	})
	require.NoError(t, err, "notice 落库失败不得把切换报成失败")
	assert.Equal(t, "low", resp.ReasoningEffort)
}

// TestSetChatSessionReasoningEffort_PropagatesWriteFailure：写库失败必须如实报错 ——
// 会话行没改成，前端据此回滚控件（「失败与恢复」）。
func TestSetChatSessionReasoningEffort_PropagatesWriteFailure(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	sess := &chat_entity.Session{ID: 100, AgentID: 7, Status: consts.ACTIVE}
	expectSwitchBackend(m, ctx, sess, agent_backend_entity.TypeCodex, "agent-bound")
	m.session.EXPECT().UpdateReasoningEffort(ctx, int64(100), "low").Return(errors.New("db down"))

	_, err := m.svc.SetChatSessionReasoningEffort(ctx, &chat_svc.SetChatSessionReasoningEffortRequest{
		SessionID: 100, ReasoningEffort: "low",
	})
	assert.Error(t, err)
}

// TestLoadSession_ReportsSessionAndBackendReasoningEffort：读路径把这条会话钉住的
// 档位与它那一档 backend 配置的档位**分别**回传（与 providerKey/agentProviderKey
// 同构）。合成不在这里做：控件自己决定脸上显示哪一个、「跟随后端配置 · <档位>」
// 解析副行显示哪一个（硬不变量 2：有效力度只在 effectiveBackendForSession 合成一次）。
func TestLoadSession_ReportsSessionAndBackendReasoningEffort(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	m.session.EXPECT().Find(ctx, int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, Status: consts.ACTIVE, ReasoningEffort: "high",
	}, nil)
	m.agent.EXPECT().Find(ctx, int64(7)).Return(&agent_entity.Agent{
		ID: 7, AgentBackendID: 12, Status: consts.ACTIVE,
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeCodex), ReasoningEffort: "medium", Status: consts.ACTIVE,
	}, nil)
	expectTranscriptWindowFilled(m)
	m.message.EXPECT().ListMeta(ctx, int64(100)).Return(nil, nil)

	resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 100})
	require.NoError(t, err)
	assert.Equal(t, "high", resp.Session.ReasoningEffort,
		"重开会话时控件要水合到会话行上钉的那一档，而不是恒显示「默认」")
	assert.Equal(t, "medium", resp.Session.AgentReasoningEffort,
		"后端配置的档位另发一格，供弹层「跟随后端配置 · <档位>」解析副行渲染")
}

// TestLoadSession_ReasoningEffortEmptyMeansFollowsBackend：会话行为空是**有含义的
// 取值**（跟随后端配置）——读路径如实留空，不在这里替控件回落成后端那一档。
func TestLoadSession_ReasoningEffortEmptyMeansFollowsBackend(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	m.session.EXPECT().Find(ctx, int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(ctx, int64(7)).Return(&agent_entity.Agent{
		ID: 7, AgentBackendID: 12, Status: consts.ACTIVE,
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeCodex), ReasoningEffort: "xhigh", Status: consts.ACTIVE,
	}, nil)
	expectTranscriptWindowFilled(m)
	m.message.EXPECT().ListMeta(ctx, int64(100)).Return(nil, nil)

	resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 100})
	require.NoError(t, err)
	assert.Empty(t, resp.Session.ReasoningEffort, "跟随后端配置：会话这一格就该是空的")
	assert.Equal(t, "xhigh", resp.Session.AgentReasoningEffort)
}

// TestSend_NewSession_PersistsDraftReasoningEffort：草稿态选定的档位随首条消息与
// Session 一同 Create 落库（spec「新建会话」），并立刻对本轮生效——合成仍只在
// effectiveBackendForSession 一处发生，这里断言的是它读到的会话行确实带上了那一档。
func TestSend_NewSession_PersistsDraftReasoningEffort(t *testing.T) {
	m := setupChatTest(t)
	runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
	t.Cleanup(restore)

	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(newBuiltinAgent(7, 12), nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21",
		ReasoningEffort: "medium", Status: consts.ACTIVE,
	}, nil)
	expectResolvableProvider(m, "key-21", string(llm_provider_entity.TypeAnthropic))

	var created *chat_entity.Session
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
		created = s
		s.ID = 100
		return nil
	})
	expectFirstTurnWrites(m, 100)

	resp, err := m.svc.Send(m.ctx, &chat_svc.SendRequest{AgentID: 7, Text: "hi", ReasoningEffort: "max"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	require.NotNil(t, created)
	assert.Equal(t, "max", created.ReasoningEffort, "草稿态选的档位必须与 Session 一起落库")
	select {
	case req := <-runner.requests:
		require.NotNil(t, req.Backend)
		assert.Equal(t, "max", req.Backend.ReasoningEffort, "首轮就按会话钉住的档位跑")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime request")
	}
}

// TestSend_NewSession_RejectsInvalidDraftReasoningEffort：六档表之外的值在落库前
// 拒绝（与 SetChatSessionReasoningEffort 同一张表、同一个错误），不写进会话行让
// 下游 runtime 静默丢弃。
func TestSend_NewSession_RejectsInvalidDraftReasoningEffort(t *testing.T) {
	m := setupChatTest(t)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(newBuiltinAgent(7, 12), nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
	}, nil)

	resp, err := m.svc.Send(m.ctx, &chat_svc.SendRequest{AgentID: 7, Text: "hi", ReasoningEffort: "ultra"})
	require.Nil(t, resp)
	require.Error(t, err, "非法档位必须在落库前报错")
	assert.NoError(t, m.dbMock.ExpectationsWereMet(), "校验失败不得发任何 DB 写")
}

// TestSend_ExistingSession_IgnoresRequestReasoningEffort：已有会话忽略 SendRequest 上
// 这一格（与 ProviderKey/ModelKey 同一条规则）——改档位走 SetChatSessionReasoningEffort，
// 否则一次普通发送就能悄悄改掉这条会话钉住的档位。
func TestSend_ExistingSession_IgnoresRequestReasoningEffort(t *testing.T) {
	m := setupChatTest(t)
	runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
	t.Cleanup(restore)

	sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE}
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(newBuiltinAgent(7, 12), nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21",
		ReasoningEffort: "medium", Status: consts.ACTIVE,
	}, nil)
	expectResolvableProvider(m, "key-21", string(llm_provider_entity.TypeAnthropic))
	expectExistingTurnWrites(m, 100)

	resp, err := m.svc.Send(m.ctx, &chat_svc.SendRequest{SessionID: 100, Text: "hi", ReasoningEffort: "max"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	assert.Empty(t, sess.ReasoningEffort, "已有会话的档位不会被一次普通发送改写")
	select {
	case req := <-runner.requests:
		require.NotNil(t, req.Backend)
		assert.Equal(t, "medium", req.Backend.ReasoningEffort, "仍按后端配置跑，请求里那一格被忽略")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime request")
	}
}

// expectFirstTurnWrites / expectExistingTurnWrites 搭一轮 Send 的消息与会话写入。
func expectFirstTurnWrites(m *chatMocks, sessionID int64) {
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
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	m.dbMock.ExpectCommit()
	m.message.EXPECT().List(gomock.Any(), sessionID).Return(nil, nil).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
}

func expectExistingTurnWrites(m *chatMocks, sessionID int64) {
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), sessionID).Return(3, nil)
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
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
}

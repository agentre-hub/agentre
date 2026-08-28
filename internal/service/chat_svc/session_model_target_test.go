package chat_svc_test

import (
	"context"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// 本文件覆盖 spec 2026-08-11-llm-provider-models「New and existing chat flow」的
// 执行侧半边：会话持久化的 ModelTarget（inherit-agent / provider-default / fixed-model）
// 必须被**每一个**下一轮执行入口（Send / Regenerate / Edit / Compact / Goal）解析成
// EffectiveLLMConfig —— 用 RunRequest.Effective 断言，而不是只测解析函数本身。

// expectResolvableProvider 搭一条 provider 及其默认模型的可解析链（FindByKey AnyTimes：
// ResolveTarget 会再查一次）。
func expectResolvableProvider(m *chatMocks, key, ptype string) *llm_provider_entity.LLMProvider {
	p := newActiveProvider(key, ptype)
	m.provider.EXPECT().FindByKey(gomock.Any(), key).Return(p, nil).AnyTimes()
	expectProviderResolvable(m, key)
	return p
}

// expectFixedModel 搭 fixed-model 解析：指定 ModelKey 的模型记录。
func expectFixedModel(m *chatMocks, providerID int64, modelKey, modelID string) {
	m.provider.EXPECT().FindModelByKey(gomock.Any(), modelKey).Return(
		&llm_provider_model_entity.LLMProviderModel{
			ProviderID: providerID, ModelKey: modelKey, ModelID: modelID,
			Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE,
		}, nil).AnyTimes()
}

// runSendAndCapture 发一条已有会话的 Send 并抓回 RunRequest；错误直接使测试失败。
func runSendAndCapture(t *testing.T, m *chatMocks, sess *chat_entity.Session, backendType agent_backend_entity.BackendType, agentProviderKey string) *agentruntime.RunRequest {
	t.Helper()
	runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restore := agentruntime.SwapRuntimeForTest(backendType, runner)
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(newBuiltinAgent(7, 12), nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(backendType), LLMProviderKey: agentProviderKey, Status: consts.ACTIVE,
	}, nil)
	// agent 绑定供应商也要可解析（resolveAgentBackend 查一次，ResolveTarget 再查一次）。
	expectResolvableProvider(m, agentProviderKey, string(llm_provider_entity.TypeAnthropic))
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

	resp, err := m.svc.Send(m.ctx, &chat_svc.SendRequest{SessionID: 100, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	select {
	case req := <-runner.requests:
		return &req
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime request")
		return nil
	}
}

// TestSend_ExistingSession_FixedModelResolvesSpecifiedChild：会话钉 fixed-model
// （ProviderKey + ModelKey 都非空）→ 下一轮解析**指定**启用子模型，而不是该 Provider 的
// 默认模型 —— 即使默认模型存在且不同。
func TestSend_ExistingSession_FixedModelResolvesSpecifiedChild(t *testing.T) {
	m := setupChatTest(t)
	// 会话 provider 已钉 key-99，ModelKey 指向一个非默认的固定子模型。
	expectResolvableProvider(m, "key-99", string(llm_provider_entity.TypeAnthropic))
	expectFixedModel(m, 0, "mk-fixed", "model-fixed")
	sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle",
		ProviderKey: "key-99", ModelKey: "mk-fixed", Status: consts.ACTIVE}

	req := runSendAndCapture(t, m, sess, agent_backend_entity.TypeBuiltin, "key-21")
	require.NotNil(t, req.Effective)
	assert.Equal(t, agentruntime.EffectiveModeFixedModel, req.Effective.Mode)
	assert.Equal(t, "key-99", req.Effective.ProviderKey)
	assert.Equal(t, "mk-fixed", req.Effective.ModelKey)
	assert.Equal(t, "model-fixed", req.Effective.ModelID, "fixed-model 必须解析到指定 ModelID")
}

// TestSend_ExistingSession_ProviderDefaultFollowsSessionProvider：会话钉 provider-default
// （providerKey 非空 + modelKey 空）→ 下一轮解析该 Provider **当前**默认模型。
func TestSend_ExistingSession_ProviderDefaultFollowsSessionProvider(t *testing.T) {
	m := setupChatTest(t)
	expectResolvableProvider(m, "key-99", string(llm_provider_entity.TypeAnthropic))
	sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle",
		ProviderKey: "key-99", Status: consts.ACTIVE}

	req := runSendAndCapture(t, m, sess, agent_backend_entity.TypeBuiltin, "key-21")
	require.NotNil(t, req.Effective)
	assert.Equal(t, agentruntime.EffectiveModeProviderDefault, req.Effective.Mode)
	assert.Equal(t, "key-99", req.Effective.ProviderKey)
	assert.Equal(t, "mk-key-99", req.Effective.ModelKey, "provider-default 落到该 Provider 当前默认模型")
	assert.Equal(t, "model-key-99", req.Effective.ModelID)
}

// TestSend_ExistingSession_InheritAgentUsesBackendFixedModel：会话未钉（inherit-agent）→
// 跟随 agent 绑定；backend 钉了固定模型时沿用该固定模型。
func TestSend_ExistingSession_InheritAgentUsesBackendFixedModel(t *testing.T) {
	m := setupChatTest(t)
	be := &agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21",
		LLMModelKey: "mk-be-fixed", Status: consts.ACTIVE,
	}
	expectResolvableProvider(m, "key-21", string(llm_provider_entity.TypeAnthropic))
	expectFixedModel(m, 0, "mk-be-fixed", "model-be-fixed")
	sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE}

	runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(newBuiltinAgent(7, 12), nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(be, nil)
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

	resp, err := m.svc.Send(m.ctx, &chat_svc.SendRequest{SessionID: 100, Text: "hi"})
	require.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	select {
	case r := <-runner.requests:
		require.NotNil(t, r.Effective)
		assert.Equal(t, agentruntime.EffectiveModeFixedModel, r.Effective.Mode)
		assert.Equal(t, "mk-be-fixed", r.Effective.ModelKey, "inherit-agent 沿用 backend 固定模型")
		assert.Equal(t, "model-be-fixed", r.Effective.ModelID)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime request")
	}
}

// TestSend_ExistingSession_InheritAgentNoBackendModelUsesProviderDefault：inherit-agent 且
// backend 未钉固定模型 → provider-default（每轮解析该 Provider 当前默认）。
func TestSend_ExistingSession_InheritAgentNoBackendModelUsesProviderDefault(t *testing.T) {
	m := setupChatTest(t)
	expectResolvableProvider(m, "key-21", string(llm_provider_entity.TypeAnthropic))
	sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE}

	req := runSendAndCapture(t, m, sess, agent_backend_entity.TypeBuiltin, "key-21")
	require.NotNil(t, req.Effective)
	assert.Equal(t, agentruntime.EffectiveModeProviderDefault, req.Effective.Mode)
	assert.Equal(t, "key-21", req.Effective.ProviderKey)
	assert.Equal(t, "model-key-21", req.Effective.ModelID)
}

// TestSend_ExistingSession_FixedModelStrictlyBlocksWhenSessionProviderGone 钉死 spec
// 2026-08-11 决策 7：会话钉 fixed-model（ProviderKey + ModelKey 都非空）后，其 Provider
// 缺失 / 停用 / 不兼容时**严格阻止**下一轮 —— 绝不回退 Agent 绑定、不改用 Provider 默认、
// 不清除 key（系统保留原 target，Picker 显示失效）。
func TestSend_ExistingSession_FixedModelStrictlyBlocksWhenSessionProviderGone(t *testing.T) {
	m := setupChatTest(t)
	// 会话所选 provider 缺失（FindByKey → nil），且会话钉的是 fixed-model（ModelKey 非空）
	// → 下一轮被阻止，而不是回退 agent 绑定。
	m.provider.EXPECT().FindByKey(gomock.Any(), "gone-provider").Return(nil, nil).AnyTimes()
	expectResolvableProvider(m, "key-21", string(llm_provider_entity.TypeAnthropic))
	sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle",
		ProviderKey: "gone-provider", ModelKey: "mk-fixed", Status: consts.ACTIVE}

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(newBuiltinAgent(7, 12), nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(newActiveProvider("key-21", string(llm_provider_entity.TypeAnthropic)), nil).AnyTimes()

	_, err := m.svc.Send(m.ctx, &chat_svc.SendRequest{SessionID: 100, Text: "hi"})
	require.Error(t, err, "fixed-model 的 Provider 缺失必须严格阻止下一轮，不静默回退")
}

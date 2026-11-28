package chat_svc_test

import (
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// ctlConversations 取 agrctl 控制 API 对 chat_svc 的那一面(按 conversation_id 定位与操作)。
func ctlConversations(t *testing.T, m *chatMocks) chat_svc.CtlConversationGateway {
	t.Helper()
	gw, ok := m.svc.(chat_svc.CtlConversationGateway)
	require.True(t, ok, "chatSvc must serve the agrctl conversation port")
	return gw
}

const ctlConvID = "0193b6f0-7c5e-7d2a-9f4b-3c1d2e5f6a7b"

// Given 一条本机会话 When 按它的 conversation_id 定位 Then 带回会话主键、agent 名、
// 由 agent_status 折算的运行态,以及最近一条 assistant 消息与它的错误。
func TestCtlConversation_LookupMapsStatusAndLastTurn(t *testing.T) {
	cases := []struct {
		agentStatus string
		want        chat_svc.CtlConversationStatus
	}{
		{"idle", chat_svc.CtlStatusIdle},
		{"", chat_svc.CtlStatusIdle},
		{"running", chat_svc.CtlStatusRunning},
		{"waiting", chat_svc.CtlStatusWaiting},
		{"error", chat_svc.CtlStatusFailed},
	}
	for _, c := range cases {
		t.Run(c.agentStatus, func(t *testing.T) {
			m := setupChatTest(t)
			sess := &chat_entity.Session{ID: 100, AgentID: 7, ConversationID: ctlConvID, AgentStatus: c.agentStatus, Status: consts.ACTIVE}
			m.session.EXPECT().FindByConversationID(gomock.Any(), ctlConvID).Return(sess, nil)
			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{ID: 7, Name: "reviewer"}, nil)
			m.message.EXPECT().LatestAssistant(gomock.Any(), int64(100)).Return(&chat_entity.Message{ID: 201, ErrorText: "boom"}, nil)

			got, err := ctlConversations(t, m).LookupCtlConversation(m.ctx, ctlConvID)
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, int64(100), got.SessionID)
			assert.Equal(t, ctlConvID, got.ConversationID)
			assert.Equal(t, "reviewer", got.AgentName)
			assert.Equal(t, c.want, got.Status)
			assert.Equal(t, int64(201), got.LastAssistantMessageID)
			assert.Equal(t, "boom", got.LastError)
			assert.Empty(t, got.Device, "a session executed on this desktop names no device")
		})
	}
}

// Given 一个合法但本机没有的 conversation_id When 定位 Then (nil, nil)——由调用方统一报
// 「不存在或无权访问」;畸形 id 在碰库之前就被拒。
func TestCtlConversation_LookupUnknownAndMalformed(t *testing.T) {
	m := setupChatTest(t)
	m.session.EXPECT().FindByConversationID(gomock.Any(), ctlConvID).Return(nil, nil)
	got, err := ctlConversations(t, m).LookupCtlConversation(m.ctx, ctlConvID)
	require.NoError(t, err)
	assert.Nil(t, got)

	_, err = ctlConversations(t, m).LookupCtlConversation(m.ctx, "42")
	require.Error(t, err)
	assert.ErrorIs(t, err, chat_svc.ErrCtlConversationInvalidID)
}

// 覆盖值的校验以「这条会话(或新会话)下一轮落在哪一档后端」为准:不被支持的值报
// CtlOverrideError 并列出可选值,不写库。
func TestCtlConversation_CheckOverridesListsOptions(t *testing.T) {
	expectBackend := func(m *chatMocks, typ agent_backend_entity.BackendType) {
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(newBuiltinAgent(7, 12), nil).AnyTimes()
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(typ), Status: consts.ACTIVE,
		}, nil).AnyTimes()
	}

	t.Run("unknown effort lists the levels", func(t *testing.T) {
		m := setupChatTest(t)
		expectBackend(m, agent_backend_entity.TypeClaudeCode)
		err := ctlConversations(t, m).CheckCtlOverrides(m.ctx, 7, 0, 0, chat_svc.CtlOverrides{Effort: "turbo"})
		var oe *chat_svc.CtlOverrideError
		require.True(t, errors.As(err, &oe), "want a CtlOverrideError, got %v", err)
		assert.Equal(t, "effort", oe.Flag)
		assert.Contains(t, oe.Options, "high")
		assert.Contains(t, oe.Error(), "turbo")
	})

	t.Run("permission mode outside the backend's set lists that set", func(t *testing.T) {
		m := setupChatTest(t)
		expectBackend(m, agent_backend_entity.TypeClaudeCode)
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, &recordingRunner{})
		t.Cleanup(restore)
		err := ctlConversations(t, m).CheckCtlOverrides(m.ctx, 7, 0, 0, chat_svc.CtlOverrides{PermissionMode: "yolo"})
		var oe *chat_svc.CtlOverrideError
		require.True(t, errors.As(err, &oe), "want a CtlOverrideError, got %v", err)
		assert.Equal(t, "permission-mode", oe.Flag)
		assert.Equal(t, []string{"default", "acceptEdits", "plan", "bypassPermissions"}, oe.Options)
	})

	t.Run("an existing session is checked against its own backend", func(t *testing.T) {
		m := setupChatTest(t)
		expectBackend(m, agent_backend_entity.TypeClaudeCode)
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, &recordingRunner{})
		t.Cleanup(restore)
		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{ID: 100, AgentID: 7, Status: consts.ACTIVE}, nil)
		err := ctlConversations(t, m).CheckCtlOverrides(m.ctx, 0, 0, 100, chat_svc.CtlOverrides{PermissionMode: "plan", Effort: "high"})
		require.NoError(t, err)
	})

	t.Run("cli model on a provider-bound backend is rejected", func(t *testing.T) {
		m := setupChatTest(t)
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(newBuiltinAgent(7, 12), nil).AnyTimes()
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
			ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "pk", Status: consts.ACTIVE,
		}, nil).AnyTimes()
		err := ctlConversations(t, m).CheckCtlOverrides(m.ctx, 7, 0, 0, chat_svc.CtlOverrides{CLIModel: "opus"})
		var oe *chat_svc.CtlOverrideError
		require.True(t, errors.As(err, &oe), "want a CtlOverrideError, got %v", err)
		assert.Equal(t, "cli-model", oe.Flag)
	})
}

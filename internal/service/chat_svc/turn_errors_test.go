package chat_svc

import (
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo/mock_llm_provider_repo"
)

func TestGatewayRoutesLLM_IncludesAntigravity(t *testing.T) {
	assert.True(t, gatewayRoutesLLM(&agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeAntigravity)}))
	assert.True(t, gatewayRoutesLLM(&agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}))
	assert.False(t, gatewayRoutesLLM(&agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeACP)}))
}

func TestResolveAgentBackend_Antigravity(t *testing.T) {
	// providerKey 为空 = 未绑供应商：不登记 FindByKey 期望，查了供应商 gomock 就会让用例失败。
	setupWithKey := func(t *testing.T, providerKey string, provType llm_provider_entity.ProviderType, remote bool) (*chatSvc, *chat_entity.Session, *execTargetPinMocks) {
		_, m, svc := setupExecTargetPinTest(t)
		ctrl := gomock.NewController(t)
		pm := mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl)
		prev := llm_provider_repo.LLMProvider()
		llm_provider_repo.RegisterLLMProvider(pm)
		t.Cleanup(func() { llm_provider_repo.RegisterLLMProvider(prev) })
		if providerKey != "" {
			pm.EXPECT().FindByKey(gomock.Any(), providerKey).Return(&llm_provider_entity.LLMProvider{
				ProviderKey: providerKey, Type: string(provType), Status: consts.ACTIVE,
			}, nil)
		}
		sess := &chat_entity.Session{ID: 970, AgentID: 470, ExecAgentBackendID: 871}
		be := &agent_backend_entity.AgentBackend{ID: 871, Type: string(agent_backend_entity.TypeAntigravity), LLMProviderKey: providerKey}
		if remote {
			be.DeviceFingerprint = "sha256:remote-box"
		}
		m.agent.EXPECT().Find(gomock.Any(), int64(470)).Return(&agent_entity.Agent{ID: 470, AgentBackendID: 871}, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(871)).Return(be, nil)
		return svc, sess, m
	}
	setup := func(t *testing.T, provType llm_provider_entity.ProviderType, remote bool) (*chatSvc, *chat_entity.Session, *execTargetPinMocks) {
		return setupWithKey(t, "gem", provType, remote)
	}
	requireCode := func(t *testing.T, err error, want int) {
		t.Helper()
		require.Error(t, err)
		var httpErr *httputils.Error
		require.True(t, errors.As(err, &httpErr), "want a coded error, got %v", err)
		assert.Equal(t, want, httpErr.Code)
	}

	t.Run("remote with gemini provider resolves for agentred dispatch", func(t *testing.T) {
		svc, sess, _ := setup(t, llm_provider_entity.TypeGemini, true)
		_, be, prov, err := svc.resolveAgentBackend(t.Context(), sess, sess.AgentID, sess.ProjectID)
		require.NoError(t, err)
		assert.Equal(t, int64(871), be.ID)
		require.NotNil(t, prov)
		assert.Equal(t, "gem", prov.ProviderKey)
	})

	t.Run("non-gemini provider is not chattable", func(t *testing.T) {
		svc, sess, _ := setup(t, llm_provider_entity.TypeAnthropic, true)
		_, _, _, err := svc.resolveAgentBackend(t.Context(), sess, sess.AgentID, sess.ProjectID)
		requireCode(t, err, code.ChatAgentNotChattable)
	})

	t.Run("unbound is not chattable instead of falling back to CLI login", func(t *testing.T) {
		svc, sess, _ := setupWithKey(t, "", "", false)
		_, _, _, err := svc.resolveAgentBackend(t.Context(), sess, sess.AgentID, sess.ProjectID)
		requireCode(t, err, code.ChatAgentNotChattable)
	})

	t.Run("local without a running gateway fails", func(t *testing.T) {
		svc, sess, _ := setup(t, llm_provider_entity.TypeGemini, false)
		_, _, _, err := svc.resolveAgentBackend(t.Context(), sess, sess.AgentID, sess.ProjectID)
		requireCode(t, err, code.ChatBackendGatewayUnavailable)
	})
}

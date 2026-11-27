package exec_target_svc_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/service/exec_target_svc"
)

// antigravity 与 claudecode/codex 同档（经网关取 LLM），但必须绑 gemini 供应商。
func TestBlockReasonForBackend_GivenAntigravity(t *testing.T) {
	ctx, m, _ := setupPickExecTargetTest(t)
	m.remoteDevice.EXPECT().List(gomock.Any()).Return(nil, nil).AnyTimes()
	m.remoteDevice.EXPECT().ListDeviceProviders(gomock.Any()).Return(nil).AnyTimes()
	gemini := &llm_provider_entity.LLMProvider{ID: 1, ProviderKey: "gem", Type: string(llm_provider_entity.TypeGemini), Status: 1}
	be := func(remote bool) *agent_backend_entity.AgentBackend {
		b := &agent_backend_entity.AgentBackend{ID: 921, Type: string(agent_backend_entity.TypeAntigravity), LLMProviderKey: "gem"}
		if remote {
			b.DeviceFingerprint = pickTestFingerprint(97)
		}
		return b
	}

	t.Run("local with gateway is chattable", func(t *testing.T) {
		ok, reason, _ := exec_target_svc.BlockReasonForBackend(ctx, be(false), gemini, true)
		assert.True(t, ok)
		assert.Empty(t, reason)
	})
	t.Run("local without gateway is blocked", func(t *testing.T) {
		ok, reason, _ := exec_target_svc.BlockReasonForBackend(ctx, be(false), gemini, false)
		assert.False(t, ok)
		assert.Equal(t, exec_target_svc.BlockReasonGatewayNotRunning, reason)
	})
	t.Run("remote is dispatchable without local gateway", func(t *testing.T) {
		ok, reason, _ := exec_target_svc.BlockReasonForBackend(ctx, be(true), gemini, false)
		assert.True(t, ok)
		assert.Empty(t, reason)
	})
	t.Run("missing provider is blocked", func(t *testing.T) {
		ok, reason, _ := exec_target_svc.BlockReasonForBackend(ctx, be(false), nil, true)
		assert.False(t, ok)
		assert.Equal(t, exec_target_svc.BlockReasonBackendRequiresProvider, reason)
	})
	t.Run("unbound is blocked instead of falling back to CLI login", func(t *testing.T) {
		unbound := be(false)
		unbound.LLMProviderKey = ""
		ok, reason, _ := exec_target_svc.BlockReasonForBackend(ctx, unbound, nil, true)
		assert.False(t, ok)
		assert.Equal(t, exec_target_svc.BlockReasonBackendRequiresProvider, reason)
	})
	t.Run("non-gemini provider is blocked as type mismatch", func(t *testing.T) {
		ok, reason, _ := exec_target_svc.BlockReasonForBackend(ctx, be(false), activeProvider("gem"), true)
		assert.False(t, ok)
		assert.Equal(t, exec_target_svc.BlockReasonBackendRequiresProvider, reason)
	})
}

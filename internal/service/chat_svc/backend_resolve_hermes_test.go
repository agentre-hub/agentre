package chat_svc

import (
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// 本文件锁住 hermes 在「轮次解析」里的插槽语义：
//   - 本机 hermes 能解析成一轮可执行的 backend，且不解析 Agentre LLMProvider、
//     不要求本地网关（它自带模型配置并自持 URL）。
//   - 远端 hermes 明确拒绝，给结构化错误码，而不是让它悄悄走 agentred 通道。
//   - selectRunner 真能拿到已注册的 hermes runtime（只声明 abort）。

func TestResolveAgentBackend_GivenLocalHermes_ThenResolvesWithoutProviderOrGateway(t *testing.T) {
	ctx, m, svc := setupExecTargetPinTest(t)
	sess := &chat_entity.Session{ID: 950, AgentID: 450, ExecAgentBackendID: 851}
	m.agent.EXPECT().Find(ctx, int64(450)).Return(&agent_entity.Agent{ID: 450, AgentBackendID: 851}, nil)
	m.backend.EXPECT().Find(ctx, int64(851)).Return(&agent_backend_entity.AgentBackend{
		ID: 851, Type: string(agent_backend_entity.TypeHermes),
	}, nil)

	a, be, prov, err := svc.resolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID)
	require.NoError(t, err)
	assert.Equal(t, int64(450), a.ID)
	assert.Equal(t, int64(851), be.ID)
	assert.Nil(t, prov, "hermes 自带模型配置，不解析 Agentre LLMProvider")
}

func TestResolveAgentBackend_GivenRemoteHermes_ThenRejectsWithRemoteHermesError(t *testing.T) {
	ctx, m, svc := setupExecTargetPinTest(t)
	sess := &chat_entity.Session{ID: 951, AgentID: 451, ExecAgentBackendID: 852}
	m.agent.EXPECT().Find(ctx, int64(451)).Return(&agent_entity.Agent{ID: 451, AgentBackendID: 852}, nil)
	m.backend.EXPECT().Find(ctx, int64(852)).Return(&agent_backend_entity.AgentBackend{
		ID: 852, Type: string(agent_backend_entity.TypeHermes), DeviceFingerprint: "sha256:remote-box",
	}, nil)

	_, _, _, err := svc.resolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID)
	require.Error(t, err)
	var httpErr *httputils.Error
	require.True(t, errors.As(err, &httpErr))
	assert.Equal(t, code.ChatBackendRemoteHermesUnavailable, httpErr.Code)
}

func TestSelectRunner_GivenLocalHermes_ThenReturnsRegisteredRuntimeWithAbortOnly(t *testing.T) {
	ctx, _, svc := setupExecTargetPinTest(t)
	be := &agent_backend_entity.AgentBackend{ID: 853, Type: string(agent_backend_entity.TypeHermes)}

	runner, err := svc.selectRunner(ctx, be, 1)
	require.NoError(t, err)
	require.NotNil(t, runner, "hermes runtime 必须已注册，否则一轮对话起不来")
	assert.True(t, runner.Capabilities().Has(capability.CapAbort), "hermes 至少声明 abort")
	assert.False(t, runner.Capabilities().Has(capability.CapSteer), "hermes 尚不支持 steer")
	assert.False(t, runner.Capabilities().Has(capability.CapCompact), "hermes 尚不支持 compress")
}

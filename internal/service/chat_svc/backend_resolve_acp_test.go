package chat_svc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
)

// 本文件锁住 acp 在「轮次解析」里的插槽语义：
//   - 本机 acp 能解析成一轮可执行的 backend，且不解析 Agentre LLMProvider、
//     不要求本地网关（ACP Agent 自带模型配置与凭证）。
//   - 远端 acp 同样可解析 —— 它是本机 CLI 子进程，和 claudecode/codex/piagent
//     同档可派发给 agentred，不是 hermes/openclaw 那种明确拒绝的形状。
//   - selectRunner 真能拿到已注册的 acp runtime（blank import 缺了它就是 nil）。
func TestResolveAgentBackend_GivenLocalACP_ThenResolvesWithoutProviderOrGateway(t *testing.T) {
	ctx, m, svc := setupExecTargetPinTest(t)
	sess := &chat_entity.Session{ID: 960, AgentID: 460, ExecAgentBackendID: 861}
	m.agent.EXPECT().Find(ctx, int64(460)).Return(&agent_entity.Agent{ID: 460, AgentBackendID: 861}, nil)
	m.backend.EXPECT().Find(ctx, int64(861)).Return(&agent_backend_entity.AgentBackend{
		ID: 861, Type: string(agent_backend_entity.TypeACP),
	}, nil)

	a, be, prov, err := svc.resolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID)
	require.NoError(t, err)
	assert.Equal(t, int64(460), a.ID)
	assert.Equal(t, int64(861), be.ID)
	assert.Nil(t, prov, "acp 自带模型配置，不解析 Agentre LLMProvider")
}

func TestResolveAgentBackend_GivenRemoteACP_ThenResolvesForAgentredDispatch(t *testing.T) {
	ctx, m, svc := setupExecTargetPinTest(t)
	sess := &chat_entity.Session{ID: 961, AgentID: 461, ExecAgentBackendID: 862}
	m.agent.EXPECT().Find(ctx, int64(461)).Return(&agent_entity.Agent{ID: 461, AgentBackendID: 862}, nil)
	m.backend.EXPECT().Find(ctx, int64(862)).Return(&agent_backend_entity.AgentBackend{
		ID: 862, Type: string(agent_backend_entity.TypeACP), DeviceFingerprint: "sha256:remote-box",
	}, nil)

	a, be, _, err := svc.resolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID)
	require.NoError(t, err, "远端 acp 可派发到 agentred，不能被解析层拒绝")
	assert.Equal(t, int64(461), a.ID)
	assert.Equal(t, int64(862), be.ID)
}

func TestSelectRunner_GivenLocalACP_ThenReturnsRegisteredRuntime(t *testing.T) {
	ctx, _, svc := setupExecTargetPinTest(t)
	be := &agent_backend_entity.AgentBackend{ID: 863, Type: string(agent_backend_entity.TypeACP)}

	runner, err := svc.selectRunner(ctx, be, 1)
	require.NoError(t, err)
	require.NotNil(t, runner, "acp runtime 必须已注册（chat.go 的 blank import），否则一轮对话起不来")
	assert.True(t, runner.Capabilities().Has(capability.CapAbort))
	assert.True(t, runner.Capabilities().Has(capability.CapToolPermission))
	assert.True(t, runner.Capabilities().Has(capability.CapImageInput))
	assert.True(t, runner.Capabilities().Has(capability.CapMCPTools))
	assert.False(t, runner.Capabilities().Has(capability.CapSteer), "acp 尚不支持 steer")
	assert.False(t, runner.Capabilities().Has(capability.CapCompact), "acp 尚不支持 compress")
	assert.False(t, runner.Capabilities().Has(capability.CapForkSession), "acp 尚不支持 fork")
}

package exec_target_svc_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/service/exec_target_svc"
)

// 本文件锁住 acp 在「可对话判定」里的插槽语义：acp 后端跑的是一条本机 CLI
// 子进程(和 claudecode/codex/piagent 同档),本地可跑、远端 agentred 也可跑
// (daemon 侧注册了 runtime,不需要 daemon 本地密钥)。ACP Agent 自带
// provider/model/凭证,因此不查 provider、不查本地网关 —— 落到 default 会变
// 成 BlockReasonUnknownBackend,那是「能建能存、聊不了」。
func TestBlockReasonForBackend_GivenACP_ThenChattableLocalAndRemote(t *testing.T) {
	ctx, _, _ := setupPickExecTargetTest(t)

	t.Run("local acp is chattable without provider or gateway", func(t *testing.T) {
		be := &agent_backend_entity.AgentBackend{ID: 911, Type: string(agent_backend_entity.TypeACP)}
		chattable, reason, hint := exec_target_svc.BlockReasonForBackend(ctx, be, inactiveProvider(), false)
		assert.True(t, chattable, "本机 acp 不依赖 Agentre provider 或本地网关")
		assert.Empty(t, reason)
		assert.Empty(t, hint)
	})

	t.Run("remote acp is dispatchable to agentred", func(t *testing.T) {
		be := &agent_backend_entity.AgentBackend{
			ID:                912,
			Type:              string(agent_backend_entity.TypeACP),
			DeviceFingerprint: pickTestFingerprint(98),
		}
		chattable, reason, hint := exec_target_svc.BlockReasonForBackend(ctx, be, nil, false)
		assert.True(t, chattable, "acp 是本机 CLI 子进程,可派发给 agentred(和 claudecode/codex/piagent 同档)")
		assert.Empty(t, reason)
		assert.Empty(t, hint)
	})
}

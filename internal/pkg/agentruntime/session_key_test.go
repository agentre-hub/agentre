package agentruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
)

// Given 两个后端上各有一条同号会话, When 生成它们在池里的键, Then 两个键必须不同。
//
// CLISessionPool 是进程级单例:claudecode 与 codex 的 defaultRuntime 拿的是同一个
// 实例。键要是不带后端命名空间,同号会话就会在池里互相顶掉 —— 后来的那条把前一条的
// 子进程 Close 掉,而前一条还以为自己的会话活着。claudecode 从前用的正是裸会话 id。
func TestSessionPoolKey_GivenSameSessionOnTwoBackends_WhenKeying_ThenKeysDiffer(t *testing.T) {
	claude := agentruntime.SessionPoolKey(agent_backend_entity.TypeClaudeCode, 42)
	codex := agentruntime.SessionPoolKey(agent_backend_entity.TypeCodex, 42)

	assert.NotEqual(t, claude, codex)
}

// Given 同一后端的同一条会话, When 反复生成键, Then 拿到的是同一个 —— 跨轮复用整个
// 靠这条。
func TestSessionPoolKey_GivenSameBackendAndSession_WhenKeying_ThenTheKeyIsStable(t *testing.T) {
	first := agentruntime.SessionPoolKey(agent_backend_entity.TypeCodex, 7)
	second := agentruntime.SessionPoolKey(agent_backend_entity.TypeCodex, 7)

	assert.Equal(t, first, second)
	assert.Equal(t, "codex:7", first, "codex 的既有键形状不变,换键会让升级后的第一轮开不出旧会话")
}

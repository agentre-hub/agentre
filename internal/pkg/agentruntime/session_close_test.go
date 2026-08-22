package agentruntime_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
)

// closeRecordingRuntime 是一个认领「会话释放口」的 runtime 替身。
type closeRecordingRuntime struct {
	agentruntime.Runtime
	closed []int64
}

func (r *closeRecordingRuntime) CloseSession(_ context.Context, sessionID int64) {
	r.closed = append(r.closed, sessionID)
}

// plainRuntime 不认领会话释放口(builtin / remote 这类没有常驻子进程的后端)。
type plainRuntime struct{ agentruntime.Runtime }

// Given 注册表里既有认领会话释放口的 runtime 也有不认领的, When 一条会话被删除,
// Then 认领的那些都要收到释放,不认领的被跳过而不是让调用方崩掉。
//
// 调用方从前是逐个后端硬写的(chat_svc.Delete 只点名 claudecode 与 codex):再加一个
// 有常驻子进程的后端就会静默漏掉一处,而漏掉的表现是机器上多一个永不退出的 CLI。
func TestCloseSessionEverywhere_GivenMixedRuntimes_WhenClosing_ThenOnlyClaimantsAreReleased(t *testing.T) {
	claimant := &closeRecordingRuntime{}
	defer agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, claimant)()
	defer agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, plainRuntime{})()

	agentruntime.CloseSessionEverywhere(context.Background(), 42)

	assert.Equal(t, []int64{42}, claimant.closed)
}

// 边界:非正数会话 id 不是任何后端的会话键,不该被派发下去。
func TestCloseSessionEverywhere_GivenNonPositiveSessionID_WhenClosing_ThenNothingIsReleased(t *testing.T) {
	claimant := &closeRecordingRuntime{}
	defer agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, claimant)()

	agentruntime.CloseSessionEverywhere(context.Background(), 0)

	assert.Empty(t, claimant.closed)
}

// closeAllRecordingRuntime 认领「批量收尾口」。
type closeAllRecordingRuntime struct {
	agentruntime.Runtime
	calls int
}

func (r *closeAllRecordingRuntime) CloseAllSessions(context.Context) { r.calls++ }

// Given 注册表里既有认领批量收尾口的 runtime 也有不认领的, When 宿主关机, Then 认领的
// 都要收到,不认领的被跳过。
//
// 宿主此前只扫 CLISessionPool,而 pi 的进程根本不进池 —— 关机路径够不着它。
func TestCloseAllSessionsEverywhere_GivenMixedRuntimes_WhenHostShutsDown_ThenOnlyClaimantsAreClosed(t *testing.T) {
	claimant := &closeAllRecordingRuntime{}
	defer agentruntime.SwapRuntimeForTest(agent_backend_entity.TypePiAgent, claimant)()
	defer agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, plainRuntime{})()

	agentruntime.CloseAllSessionsEverywhere(context.Background())

	assert.Equal(t, 1, claimant.calls)
}

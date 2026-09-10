package chat_svc_test

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// resolveStopBgRunner 是同时实现 BackgroundTaskResolver 与 BackgroundTaskStopper 的
// 最小 runner:记录反查入参与 stop_task 下发入参,可注入「不认识这个 tool_use」。
type resolveStopBgRunner struct {
	resolveTaskID string
	resolveErr    error
	resolveCalls  []string

	gotSid    int64
	gotTask   string
	stopCalls int
	stopErr   error
}

func (*resolveStopBgRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{capability.CapStopBackgroundTask: true}}
}

func (*resolveStopBgRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	ch := make(chan agentruntime.Event)
	close(ch)
	return ch, &agentruntime.RunResult{}, nil
}

func (r *resolveStopBgRunner) ResolveBackgroundTask(_ context.Context, _ int64, toolUseID string) (string, error) {
	r.resolveCalls = append(r.resolveCalls, toolUseID)
	if r.resolveErr != nil {
		return "", r.resolveErr
	}
	return r.resolveTaskID, nil
}

func (r *resolveStopBgRunner) StopBackgroundTask(_ context.Context, sid int64, taskID string) error {
	r.stopCalls++
	r.gotSid = sid
	r.gotTask = taskID
	return r.stopErr
}

// expectStopBgBackend 放行「会话 → agent → backend」这条解析链,让 StopBackgroundTask
// 走到 selectRunner。
func expectStopBgBackend(m *chatMocks, sessionID int64) {
	m.session.EXPECT().Find(m.ctx, sessionID).Return(
		&chat_entity.Session{ID: sessionID, AgentID: 7, Status: consts.ACTIVE}, nil)
	m.agent.EXPECT().Find(m.ctx, int64(7)).Return(
		&agent_entity.Agent{ID: 7, AgentBackendID: 3, Status: consts.ACTIVE}, nil)
	m.backend.EXPECT().Find(m.ctx, int64(3)).Return(
		&agent_backend_entity.AgentBackend{ID: 3, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE}, nil)
}

// TestStopBackgroundTask_NoOverlayButRuntimeKnowsTask 钉死缺陷现场(sess-3797:20 个
// tool_use 完全没有 subagent_state overlay):库里查不到 overlay 时,仍要向 runtime 反查
// 出 CLI task_id 并真的把子任务停掉,而不是「返回成功但什么都没停」。
func TestStopBackgroundTask_NoOverlayButRuntimeKnowsTask(t *testing.T) {
	convey.Convey("Given 库里没有 subagent_state overlay,When 点停止且 runtime 认得该 tool_use,Then 用 runtime 的 task_id 下发 stop_task", t, func() {
		m := setupChatTest(t)
		runner := &resolveStopBgRunner{resolveTaskID: "b0live"}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		defer restore()

		expectStopBgBackend(m, 42)
		m.message.EXPECT().FindSubagentState(m.ctx, int64(42), "tu1").Return("", "", false, nil)
		m.message.EXPECT().FlipSubagentStatus(m.ctx, int64(42), "tu1", "canceled", "").Return(nil)

		resp, err := m.svc.StopBackgroundTask(m.ctx, &chat_svc.StopBackgroundTaskRequest{SessionID: 42, ToolCallID: "tu1"})

		assert.NoError(t, err)
		require.NotNil(t, resp)
		assert.True(t, resp.Stopped)
		assert.Equal(t, []string{"tu1"}, runner.resolveCalls, "应先向 runtime 反查 tool_use → task_id")
		assert.Equal(t, 1, runner.stopCalls, "overlay 缺失不等于任务已停,必须真的下发 stop_task")
		assert.Equal(t, int64(42), runner.gotSid)
		assert.Equal(t, "b0live", runner.gotTask)
	})
}

// TestStopBackgroundTask_NeitherRuntimeNorOverlayLocatesTask 钉死「两边都定位不到时
// 不许谎报成功」:runtime 不认识且库里没有 overlay → 报错给前端,而不是 Stopped=true。
func TestStopBackgroundTask_NeitherRuntimeNorOverlayLocatesTask(t *testing.T) {
	convey.Convey("Given runtime 不认识该 tool_use 且库里没有 overlay,When 点停止,Then 返回 ChatStopBgTaskUnknown 且不下发 stop_task", t, func() {
		m := setupChatTest(t)
		runner := &resolveStopBgRunner{resolveErr: agentruntime.ErrBackgroundTaskUnknown}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		defer restore()

		expectStopBgBackend(m, 42)
		m.message.EXPECT().FindSubagentState(m.ctx, int64(42), "tu1").Return("", "", false, nil)

		resp, err := m.svc.StopBackgroundTask(m.ctx, &chat_svc.StopBackgroundTaskRequest{SessionID: 42, ToolCallID: "tu1"})

		assert.Nil(t, resp)
		var httpErr *httputils.Error
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, code.ChatStopBgTaskUnknown, httpErr.Code)
		assert.Equal(t, 0, runner.stopCalls, "定位不到任务标识时不得下发 stop_task")
	})
}

// TestStopBackgroundTask_RuntimeTaskIDWinsOverOverlay 钉死定位顺序:活着的子进程报的
// task_id 比库里的 overlay 权威(overlay 可能是上一个子进程留下的旧值)。
func TestStopBackgroundTask_RuntimeTaskIDWinsOverOverlay(t *testing.T) {
	convey.Convey("Given overlay 与 runtime 给出不同 task_id,When 点停止,Then 下发 runtime 报的那个", t, func() {
		m := setupChatTest(t)
		runner := &resolveStopBgRunner{resolveTaskID: "b0live"}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		defer restore()

		expectStopBgBackend(m, 42)
		m.message.EXPECT().FindSubagentState(m.ctx, int64(42), "tu1").Return("b0stale", "running", true, nil)
		m.message.EXPECT().FlipSubagentStatus(m.ctx, int64(42), "tu1", "canceled", "").Return(nil)

		resp, err := m.svc.StopBackgroundTask(m.ctx, &chat_svc.StopBackgroundTaskRequest{SessionID: 42, ToolCallID: "tu1"})

		assert.NoError(t, err)
		require.NotNil(t, resp)
		assert.True(t, resp.Stopped)
		assert.Equal(t, "b0live", runner.gotTask, "runtime 的反查结果优先于库里的 overlay")
	})
}

// TestStopBackgroundTask_RunnerWithoutResolverFallsBackToOverlay 守住「runner 不实现
// 新端口时行为与今天完全一致」:codex / builtin / remote 那几路仍按 overlay 的 task_id 停。
func TestStopBackgroundTask_RunnerWithoutResolverFallsBackToOverlay(t *testing.T) {
	convey.Convey("Given runner 不实现反查端口,When overlay 里有 task_id,Then 仍按 overlay 下发 stop_task", t, func() {
		m := setupChatTest(t)
		runner := &stopBgRunner{}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		defer restore()

		expectStopBgBackend(m, 42)
		m.message.EXPECT().FindSubagentState(m.ctx, int64(42), "tu1").Return("b0n82mqaj", "running", true, nil)
		m.message.EXPECT().FlipSubagentStatus(m.ctx, int64(42), "tu1", "canceled", "").Return(nil)

		resp, err := m.svc.StopBackgroundTask(m.ctx, &chat_svc.StopBackgroundTaskRequest{SessionID: 42, ToolCallID: "tu1"})

		assert.NoError(t, err)
		require.NotNil(t, resp)
		assert.True(t, resp.Stopped)
		assert.Equal(t, "b0n82mqaj", runner.gotTask)
	})
}

// TestStopBackgroundTask_EvictedSubprocessStaysIdempotent 守住既有幂等语义:子进程已
// evict(runner 返 ErrNoActiveTurn)时任务随之消失,仍算停成功。
func TestStopBackgroundTask_EvictedSubprocessStaysIdempotent(t *testing.T) {
	convey.Convey("Given 下发 stop_task 时 runner 报 ErrNoActiveTurn,When 点停止,Then 幂等成功", t, func() {
		m := setupChatTest(t)
		runner := &resolveStopBgRunner{
			resolveErr: agentruntime.ErrBackgroundTaskUnknown,
			stopErr:    agentruntime.ErrNoActiveTurn,
		}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		defer restore()

		expectStopBgBackend(m, 42)
		m.message.EXPECT().FindSubagentState(m.ctx, int64(42), "tu1").Return("b0n82mqaj", "running", true, nil)

		resp, err := m.svc.StopBackgroundTask(m.ctx, &chat_svc.StopBackgroundTaskRequest{SessionID: 42, ToolCallID: "tu1"})

		assert.NoError(t, err)
		require.NotNil(t, resp)
		assert.True(t, resp.Stopped)
		assert.Equal(t, 1, runner.stopCalls)
	})
}

// TestStopBackgroundTask_TerminalOverlayShortCircuitsRuntime 守住另一条既有幂等语义:
// overlay 已是终态时任务本就不在跑,连 runtime 都不必问。
func TestStopBackgroundTask_TerminalOverlayShortCircuitsRuntime(t *testing.T) {
	convey.Convey("Given overlay 状态已是 completed,When 点停止,Then 幂等成功且不碰 runtime", t, func() {
		m := setupChatTest(t)
		runner := &resolveStopBgRunner{resolveTaskID: "b0live"}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		defer restore()

		m.session.EXPECT().Find(m.ctx, int64(42)).Return(
			&chat_entity.Session{ID: 42, AgentID: 7, Status: consts.ACTIVE}, nil)
		m.message.EXPECT().FindSubagentState(m.ctx, int64(42), "tu1").Return("b0stale", "completed", true, nil)

		resp, err := m.svc.StopBackgroundTask(m.ctx, &chat_svc.StopBackgroundTaskRequest{SessionID: 42, ToolCallID: "tu1"})

		assert.NoError(t, err)
		require.NotNil(t, resp)
		assert.True(t, resp.Stopped)
		assert.Empty(t, runner.resolveCalls)
		assert.Equal(t, 0, runner.stopCalls)
	})
}

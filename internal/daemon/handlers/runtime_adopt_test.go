package handlers_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

// TestRuntime_AdoptDoesNotSilenceTheTurnAlreadyStreaming
//
// Given 一条会话在这条连接上开着一轮、事件正往外发,
// When 同一条连接接管它(runtime.session.attach → Adopt,浏览器从草稿页落到详情页时
//
//	就是这个次序:先 runtime.run,再在同一条中继通道上 attach),
//
// Then 这一轮**剩下的事件照样发得出去**。
//
// Adopt 放进内存会话表的是**占位行**——它背后没有任何一轮在跑,存在只为让这条连接
// 解得出会话的 backend(控制 RPC 要用)。它无条件盖掉那一格的话,正在跑的那一轮
// 就此 isCurrent == false,fanout 里每一条事件都撞上 `if !current { continue }`,
// 一条也进不了通知日志。
//
// 真机上量到的样子(2026-09-03,agentred 联调机):claude CLI 答完了整整一轮,
// fanout 汇总日志报 totalEvents=286 / currentGeneration=false,而 journal 里只剩
// 3 帧 —— 循环之前注入的 prelude、循环之后无条件发的终态帧,以及开始帧。控制台上
// 就是「点进去还没有消息」,不报错、不跳号。
func TestRuntime_AdoptDoesNotSilenceTheTurnAlreadyStreaming(t *testing.T) {
	// fanout 的收尾日志落在最后一帧**之后**。不等它,这条 goroutine 会活过本用例,
	// 与下一条用例换全局 logger 的那一下撞成 data race(包内其余用例共用同一个全局)。
	captured := captureRuntimeLogs(t)
	events := make(chan agentruntime.Event)
	rt := &fullRT{}
	rt.runFn = func(context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		return events, &agentruntime.RunResult{ProviderSessionID: "psid-adopt"}, nil
	}
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{ID: 1, Type: string(agent_backend_entity.TypeClaudeCode), Name: "x"}

	_, err := h.Run(ctx, wire.RunParams{
		Backend:        backendJSON(t, be),
		ConversationID: convID(91),
		Cwd:            "/tmp",
		UserText:       "看看目录",
		// 浏览器发起时这两格必带(dispatch.ts 的 sourceDevice / sourceDeviceName),
		// 缺了就没有 prelude —— 而 prelude 正是真机上唯一活下来的那条用户消息。
		SourceDevice:     "sha256:browser",
		SourceDeviceName: "Edge · macOS",
	})
	require.NoError(t, err)
	// 开始帧 + prelude(这一轮的用户消息)。
	notif.waitFrames(t, 2)

	// 接管之前的那一条:证明这一轮本来发得出去。
	events <- agentruntime.TextDelta{Text: "before"}
	notif.waitFrames(t, 3)

	// 详情页在**同一条连接**上接管这条会话。
	h.Adopt(ctx, convID(91), agent_backend_entity.TypeClaudeCode)

	// 接管之后的那一条:这才是被丢掉的那些。
	events <- agentruntime.TextDelta{Text: "after"}
	close(events)

	frames := notif.waitFrames(t, 5)
	require.Len(t, frames, 5, "开始帧 + prelude + 两条事件 + 终态帧,一条都不许少")
	assert.Equal(t, wire.NotifyRunResultDone, frames[4].method)

	texts := make([]string, 0, 2)
	for _, frame := range frames {
		if frame.method != wire.NotifyEvent {
			continue
		}
		if delta, ok := frame.params.(*wire.EventFrame).Event.(agentruntime.TextDelta); ok {
			texts = append(texts, delta.Text)
		}
	}
	assert.Equal(t, []string{"before", "after"}, texts,
		"接管是为了让这条连接答得上控制 RPC,不是把正在跑的那一轮静音")

	require.Eventually(t, func() bool {
		return strings.Contains(captured.String(), "handlers.RuntimeHandlers.fanout: session ended")
	}, time.Second, 10*time.Millisecond, "等 fanout 自己收工,别把它留给下一条用例")
}

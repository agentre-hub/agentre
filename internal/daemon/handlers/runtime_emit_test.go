package handlers_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// ── 会话通知出口:落库的是块,推出去的分两级 ───────────────────────────────────
//
// 通知日志退役之后(规格 2026-09-05 决策 1),出口只剩一件事:把这一帧推给此刻活着的
// 那条连接。内容的事实由块表持有。逐条事件推出去的那一路是**预览帧**,即时呈现用,
// 不带编号、不参与对端的游标推进与去重(决策 4);同一段内容的**持久帧**由
// turnTranscript.publishDurable 在块落库之后另发,带着台账取到的号 —— 下面这两条
// 用例守的是预览那一路,所以鉴别对象是没有接转录端口的那个 rig(不落块 = 不发持久帧)。

// runEmitTurn 跑一轮只产两条事件的执行,交回推出去的那几帧。
// 开始通知 + 两条事件 + 终态帧 = 4 条通知。
func runEmitTurn(t *testing.T) (*recordingOutbound, []notifyFrame) {
	t.Helper()
	rt := &fullRT{}
	rt.runFn = func(context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event, 2)
		ch <- agentruntime.TextDelta{Text: "hi"}
		ch <- agentruntime.Done{}
		close(ch)
		return ch, &agentruntime.RunResult{ProviderSessionID: "psid-1"}, nil
	}
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{ID: 1, Type: string(agent_backend_entity.TypeClaudeCode), Name: "x"}
	_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(42), AgentID: 7, Cwd: "/tmp"})
	require.NoError(t, err)
	return notif, notif.waitFrames(t, 4)
}

// Given 一轮**不落块**的执行连着发出四条会话通知(这个 rig 没有接转录端口),
// When  它们被推给此刻活着的连接,
// Then  每一条都不带编号 —— 事件帧显式标成预览帧,其余三类帧协议上没有这一格,
//
//	对端按「这一帧没有序号」直接消费(remote.dispatchNotification 的 seq==0 分支)。
//
// 会拒绝的错误实现:出口自己现编一个号。宿主重启后同一份内容会被重新编号,对端的游标
// 随即指向一个宿主认不回来的位置 —— 那正是这一轮要消灭的问题 B。号只能来自
// transcript_repo 的台账,而那一路(持久帧实时发布)由集成用例守。
func TestRuntime_Emit_PushedNotificationsAreUnnumbered(t *testing.T) {
	_, frames := runEmitTurn(t)

	require.Len(t, frames, 4)
	for i, frame := range frames {
		require.NotNil(t, frame.notification, "推出去的必须是已经转换好的 Protobuf 通知")
		assert.Zerof(t, protowire.NotificationSeq(frame.notification),
			"第 %d 条(%s)不该带编号", i, frame.method)
	}
}

// Given 逐条事件推出去的那一路(这个 rig 不落块,所以推出去的只有这一路),
// When  它们被推出去,
// Then  每一条都显式标着 preview —— 预览帧必须在协议上可区分,不能靠 seq=0 表达
//
//	(决策 4:消费侧把 seq <= 游标当重复丢弃,seq=0 会被无条件吞掉)。
func TestRuntime_Emit_EventFramesAreMarkedPreview(t *testing.T) {
	_, frames := runEmitTurn(t)

	events := 0
	for _, frame := range frames {
		if frame.method != wire.NotifyEvent {
			continue
		}
		events++
		ef, ok := frame.params.(*wire.EventFrame)
		require.True(t, ok, "expected EventFrame, got %T", frame.params)
		assert.True(t, ef.Preview, "逐条推的实时事件帧是预览帧;同一段内容的持久帧另发")
	}
	assert.Positive(t, events, "这一轮必须推出过事件帧")
}

// TestNotifierPort_TakesAnAlreadyConvertedNotification 钉死推送端口的契约:它收的是
// **已经转换好**的 Protobuf 通知,不是「method + 随便什么 params」。
//
// 这条不是文字狱式的源码断言,而是这次改动要守的那个不变量本身:只要端口收的是
// *agentrewire.RpcNotification,任何一个实现(真连接 / 扇出队列 / 会话路由)都没有东西
// 可以再转换一次;把参数放宽回 any,重复转换就会立刻悄悄长回来 —— 它不改变任何可观察
// 行为,除了让每个 token 多付一次 JSON 解码与整棵消息树的构造,没有别的测试会因此变红。
func TestNotifierPort_TakesAnAlreadyConvertedNotification(t *testing.T) {
	method, ok := reflect.TypeOf((*handlers.NotifierPort)(nil)).Elem().MethodByName("Notify")
	require.True(t, ok, "NotifierPort 必须有 Notify")
	require.Equal(t, 1, method.Type.NumIn(), "Notify 只收一条已经转换好的通知")
	assert.Equal(t, reflect.TypeOf((*agentrewire.RpcNotification)(nil)), method.Type.In(0))
}

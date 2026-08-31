package handlers_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// ── 会话通知出口的 seq 与「一条通知只转换一次」────────────────────────────────
//
// 出口对一条通知做三件相互咬合的事:转换成 Protobuf 通知、以 seq=0 落库、把**同一条
// 消息**盖上库分配的 seq 推出去。三件事各由下面一个用例钉死 —— 它们分开写是因为搞砸
// 其中任何一件都不会让另外两件变红,而三种搞砸法在真机上的表现都是「没有报错、但客户端
// 的转录乱了」。

// runEmitTurn 跑一轮只产两条事件的执行,交回出口上落的日志行与推出去的帧。
// 两条事件 + 终态帧 = 3 条通知,seq 依次 1/2/3。
func runEmitTurn(t *testing.T) (*recordingOutbound, []journalRow, []notifyFrame) {
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
	frames := notif.waitFrames(t, 3)
	return notif, notif.journalRows(), frames
}

// TestRuntime_Emit_PushedNotificationCarriesTheJournalAssignedSeq
//
// Given 一轮执行连着发出三条会话通知,
// When 每条都先落库拿到 seq、再推给此刻活着的连接,
// Then 推出去的那条 Protobuf 通知上带的就是库为它分配的 seq(1/2/3),不是 0。
//
// 这是桌面端补齐与去重的唯一依据:seq 丢了(留 0)会让 dispatchNotification 走进
// 「不带 seq」的旁路,乱序与重复此后没有任何东西挡得住,而且不报错。
func TestRuntime_Emit_PushedNotificationCarriesTheJournalAssignedSeq(t *testing.T) {
	_, rows, frames := runEmitTurn(t)

	require.Len(t, rows, 3)
	require.Len(t, frames, 3)
	for i, frame := range frames {
		require.NotNil(t, frame.notification, "推出去的必须是已经转换好的 Protobuf 通知")
		assert.Equal(t, rows[i].seq, protowire.NotificationSeq(frame.notification),
			"第 %d 条推出去的通知必须带着库分配的 seq", i)
		assert.NotZero(t, protowire.NotificationSeq(frame.notification))
	}
}

// TestRuntime_Emit_JournaledPayloadStoresSeqZero
//
// Given 同一轮的三条通知,
// When 它们落进通知日志,
// Then BLOB 里的 seq 一律是 0 —— seq 是日志**行**自己的属性(行的 seq 列),补齐重放
// 时由 pullUntilCaughtUp 用行上的 seq 重新盖回消息。BLOB 里存一份第二真相,一旦与
// seq 列对不上,补齐出来的转录顺序就和实时流不是一回事。
func TestRuntime_Emit_JournaledPayloadStoresSeqZero(t *testing.T) {
	_, rows, _ := runEmitTurn(t)

	require.Len(t, rows, 3)
	for i, row := range rows {
		assert.Equal(t, int64(i+1), row.seq, "行的 seq 列按 (对端, 会话) 从 1 起递增")
		stored, err := protowire.DecodeNotification(row.blob)
		require.NoError(t, err)
		assert.Zero(t, protowire.NotificationSeq(stored), "落库的字节里 seq 必须是 0")
	}
}

// TestRuntime_Emit_PushesTheJournaledMessageInsteadOfConvertingItAgain
//
// Given 一条通知已经被转换成 Protobuf 并落库,
// When 出口把它推给对端,
// Then 推出去的就是**那一条消息本身**、只多盖了一个 seq —— 落库的字节盖上行 seq 之后
// 与推出去的消息逐字段相等。
//
// 拒绝的实现:推送端口拿着 wire 帧自己再转换一次(转换要对密封事件做一次 JSON 解码 +
// 重建整棵消息树,而这条路径是流式输出的每一个 token)。那种实现下这个断言仍可能碰巧
// 相等,所以真正把它挡死的是端口签名本身 —— 见
// TestNotifierPort_TakesAnAlreadyConvertedNotification。
func TestRuntime_Emit_PushesTheJournaledMessageInsteadOfConvertingItAgain(t *testing.T) {
	_, rows, frames := runEmitTurn(t)

	require.Len(t, rows, 3)
	require.Len(t, frames, 3)
	for i, row := range rows {
		want, err := protowire.DecodeNotification(row.blob)
		require.NoError(t, err)
		require.True(t, protowire.SetNotificationSeq(want, row.seq))
		assert.True(t, proto.Equal(want, frames[i].notification),
			"第 %d 条:推出去的消息应当就是落库的那一条 + 行 seq\nwant=%v\n got=%v", i, want, frames[i].notification)
	}
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

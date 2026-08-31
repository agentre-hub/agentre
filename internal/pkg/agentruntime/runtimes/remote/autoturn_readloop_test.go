package remote

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/protorpctest"
)

// readLoopAlive 用一次真的 RPC 往返判定「读循环还在推进」。
//
// 为什么这个探针有说服力:protorpc.Conn.Serve 是**单 goroutine**,它既 inline 派发
// 通知(RpcFrame_Notification),又负责把 RPC 应答交回等待方(RpcFrame_Response)。
// 通知 handler 一旦在某个 channel 上阻塞,应答就永远送不回来 —— 所以「Barrier 超时」
// 精确等价于「读循环被这条通知焊死了」,而不是别的什么慢。
func readLoopAlive(t *testing.T, r *Runtime, within time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	return protorpctest.Barrier(ctx, r.conn())
}

// TestAutonomousTurnEvent_SlowConsumerDoesNotStallTheReadLoop
//
// Given 一条已开的自主续轮,消费方(chat_svc.driveAutonomousTurn)此刻没有在 drain
//
//	它的 Events —— 现实里它随时可能正卡在一次落库上;
//
// When  daemon 连着推来的事件超过该轮的缓冲;
// Then  读循环必须仍然在推进。
//
// 这一条是 runtime.go 那句「notify handler(read loop 直接调用)做 non-blocking
// send ... 而不是阻塞 WebSocket 读循环」在自主续轮这一侧的同款要求:同一条读循环、
// 同一个危险,per-Run 那条路径早就守住了,自主续轮这条没有。
//
// 后果不止是「慢」:读循环停了,这条连接上**所有**会话的通知和**所有**在飞 RPC 的
// 应答一起停。而解除阻塞要等消费方先往前走,消费方要往前走却可能正等着一个走不回来
// 的 RPC 应答 —— 闭环。sess-3110 在 claudecode 的 readLoop 上就是这么冻死五个多
// 小时的。
func TestAutonomousTurnEvent_SlowConsumerDoesNotStallTheReadLoop(t *testing.T) {
	_, _, capture, rt := setupRemote(t)
	const sid = int64(4101)

	turns := rt.AutonomousTurns(sid)
	capture.deliver(t, wire.NotifyAutonomousTurnStarted,
		wire.AutonomousTurnStartedFrame{ConversationID: convOf(sid), Trigger: "background-task"})

	var turn agentruntime.AutonomousTurn
	select {
	case turn = <-turns:
	case <-time.After(2 * time.Second):
		t.Fatal("自主续轮没有交付给消费方")
	}
	require.NotNil(t, turn.Events)

	// 刻意**不** drain turn.Events:模拟消费方正卡在一次落库上。
	for i := 0; i < 512; i++ {
		capture.deliver(t, wire.NotifyAutonomousTurnEvent, wire.EventFrame{
			ConversationID: convOf(sid),
			Event:          agentruntime.TextDelta{Text: fmt.Sprintf("delta-%03d", i)},
		})
	}

	require.NoError(t, readLoopAlive(t, rt, 3*time.Second),
		"消费方没跟上就把整条连接的读循环焊死了 —— 这条连接上所有会话的通知与所有在飞 RPC 应答一并停摆")
}

// TestAutonomousTurnStarted_UndrainedTurnsDoNotStallTheReadLoop
//
// Given 消费方还没有开始 range AutonomousTurns()(App 刚起来 / watcher 还没接上);
// When  daemon 连着宣告多轮自主续轮,超过交付 channel 的缓冲;
// Then  读循环必须仍然在推进。
//
// 这条路径比事件那条更容易踩到:交付 channel 只有 4 格,而 App 重启后「回放一整段
// 历史」是常态。更糟的是这个阻塞的发送是**持着 a.mu** 做的,而断连清理
// (closeAllAutoSessions)也要 a.mu —— 于是连断连都拆不开这个死结。
func TestAutonomousTurnStarted_UndrainedTurnsDoNotStallTheReadLoop(t *testing.T) {
	_, _, capture, rt := setupRemote(t)
	const sid = int64(4102)

	// 拿到 channel 但**不**读它。
	_ = rt.AutonomousTurns(sid)

	for i := 0; i < 16; i++ {
		capture.deliver(t, wire.NotifyAutonomousTurnStarted,
			wire.AutonomousTurnStartedFrame{ConversationID: convOf(sid), Trigger: fmt.Sprintf("turn-%d", i)})
	}

	require.NoError(t, readLoopAlive(t, rt, 3*time.Second),
		"没人接自主续轮就把读循环焊死了 —— 且这个发送持着 a.mu,断连清理也拿不到锁")
}

// TestPrepareRun_SlowConsumerKeepsItsTurnAndLosesNoEvents
//
// Given 一轮正在跑的用户轮,消费方(chat_svc 的 turn 循环)此刻没在 drain —— 现实里
//
//	它随时可能正卡在一次落库上(一条 12MB 的 blocks_json 检查点就够久了);
//
// When  daemon 连着推来的事件远超过任何固定缓冲;
// Then  这一轮必须照常跑完:一条事件都不少,StopErr 干净。
//
// 旧实现是「有界 128 + 溢出就取消这一个 generation」。不阻塞读循环这一半是对的,
// 代价那一半太重:用户的一轮会因为消费方**慢了一下**而直接判死,前端拿到的是
// 「event delivery exceeded bounded buffer」。128 帧在流式回复里不过一两秒的量。
//
// 换成 orderedpipe 之后两头都占:读循环照样永不阻塞(见上面那两条用例的同款判据),
// 而慢消费方只是慢,不再丢轮。代价是消费方真的停摆时缓冲无上限 —— 那是 bug 不是
// 稳态,详见 orderedpipe 的包注释。
func TestPrepareRun_SlowConsumerKeepsItsTurnAndLosesNoEvents(t *testing.T) {
	_, cli, capture, rt := setupRemote(t)
	const (
		sid   = int64(4103)
		burst = 512
	)
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, result any) error {
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: params.(wire.RunParams).ConversationID}
			return nil
		})

	events, runResult, err := rt.Run(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: "claudecode", ID: 1, Name: "x"},
		SessionID: sid,
		UserText:  "hello",
	})
	require.NoError(t, err)

	// 刻意**不** drain:模拟消费方正卡在一次落库上。
	for i := 0; i < burst; i++ {
		capture.deliver(t, wire.NotifyEvent, wire.EventFrame{
			ConversationID: convOf(sid),
			Event:          agentruntime.TextDelta{Text: fmt.Sprintf("delta-%03d", i)},
		})
	}
	require.NoError(t, readLoopAlive(t, rt, 3*time.Second),
		"读循环必须照常推进 —— 这一半旧实现也是对的,不能因为换实现丢掉")

	capture.deliver(t, wire.NotifyRunResultDone, wire.RunResultDoneFrame{ConversationID: convOf(sid)})

	got := make([]string, 0, burst)
	for ev := range events {
		if td, ok := ev.(agentruntime.TextDelta); ok {
			got = append(got, td.Text)
		}
	}
	require.Len(t, got, burst, "消费方慢不该丢事件")
	for i, text := range got {
		require.Equal(t, fmt.Sprintf("delta-%03d", i), text, "顺序也必须原样保留")
	}
	require.NoError(t, runResult.StopErr, "消费方慢了一下,不该把用户这一轮判死")
}

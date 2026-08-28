package claudecode

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// —— readLoop 的推进不得依赖消费方的节奏(sess-3110)——
//
// readLoop 是单 goroutine,并且是**唯一**能 close 各轮事件 channel 的人。只要它在
// 「投递事件」或「交出新一轮」这两件事上会阻塞,而解除阻塞又要等消费方先把另一路
// drain 完 —— 消费方却在等 readLoop 去 close 那一路 —— 就是一个闭环死锁。
//
// 现场 sess-3110:自主续轮活跃期间 5 个后台 subagent 按 owner 各开一路旁路活动轮
// (sideActivities),而消费方(Runtime.SubagentActivity / startSubagentActivityWatcher)
// 是**串行 inline** drain 的:一路没 close 就轮不到下一路。第二路灌满缓冲后 readLoop
// 卡死在 feed 上,整条会话冻结 5 小时 —— CLI 还在跑,agentre 一帧都收不到,会话
// agent_status 永久停在 running,派遣卡的工具数永远停在卡死那一刻。
//
// 下面两个用例分别压住闭环里的两个阻塞点:每轮的事件投递、旁路活动轮的交出。
// 消费方复刻生产形态(串行 inline drain),断言 readLoop 仍能把帧流推到收尾。
//
// 自主续轮的交出(autoCh)刻意不在此列,它的有界缓冲是**安全**的 back-pressure:
// 自主续轮由 active 单槽位严格串行 —— readLoop 交出第 N+1 轮之前,第 N 轮必然已经
// 在 finishActiveTurn 里被 close,消费方永远能自己走完,不构成闭环。

const (
	bgSubAgentOwnerA = "toolu_owner_a"
	bgSubAgentOwnerB = "toolu_owner_b"
)

// writeBackgroundTaskNotification 写一帧「后台型 task_notification」——
// 有 output_file、无 subagent_type,readLoop 据此起一轮自主续轮(主线轮)。
func writeBackgroundTaskNotification(stdout io.Writer, sid, taskID string) {
	writeFrame(stdout, `{"type":"system","subtype":"task_notification","session_id":%q,"tool_use_id":"toolu_bg_%s","task_id":%q,"status":"completed","output_file":"/tmp/tasks/%s.output","summary":"done"}`,
		sid, taskID, taskID, taskID)
}

// writeSubagentFrame 写一帧后台 subagent 的内部活动(带 parent_tool_use_id)。
func writeSubagentFrame(stdout io.Writer, owner, id string) {
	writeFrame(stdout, `{"type":"assistant","parent_tool_use_id":%q,"message":{"id":%q,"content":[{"type":"text","text":"x"}]}}`, owner, id)
}

// fakeTwoBackgroundSubagentsDuringAutonomousTurn 复刻 sess-3110 的帧序:自主续轮
// (主线轮)开着,两个上一轮派出的后台 subagent 交错吐内部活动,第二个吐得远多于
// 单轮 channel 缓冲。收尾的 result 帧排在最后 —— readLoop 卡住就永远送不到,
// 两路旁路活动轮也就永远不会被 close。
func fakeTwoBackgroundSubagentsDuringAutonomousTurn(stdin io.Reader, stdout io.Writer) {
	const sid = "sess-3110-events"
	writeBackgroundTaskNotification(stdout, sid, "t1")
	// owner A 先开一路:串行消费方会一直停在这一路上,直到它被 close。
	writeSubagentFrame(stdout, bgSubAgentOwnerA, "a0")
	// owner B 另开一路:没人 drain,超过单轮缓冲后 readLoop 就卡在投递上。
	for i := range 64 {
		writeSubagentFrame(stdout, bgSubAgentOwnerB, fmt.Sprintf("b%d", i))
	}
	writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
	_, _ = io.Copy(io.Discard, stdin) // 保持子进程存活,别让 EOF 替我们收尾
}

// serialSubagentActivityConsumer 复刻生产消费方:单 goroutine,一路 drain 到 close
// 才轮到下一路(Runtime.SubagentActivity 的 inline drainStream)。每收尾一路,把它的
// ToolUseID 报出来。
func serialSubagentActivityConsumer(sess *Session) <-chan string {
	finished := make(chan string, 32)
	go func() {
		defer close(finished)
		for act := range sess.SubagentActivity() {
			for range act.Events { //nolint:revive // 只需 drain,不断言内容
			}
			finished <- act.ToolUseID
		}
	}()
	return finished
}

// awaitActivities 等 want 路旁路活动轮各自收尾;超时即判定 readLoop 已被消费方卡死。
func awaitActivities(t *testing.T, finished <-chan string, want int, d time.Duration) {
	t.Helper()
	got := make([]string, 0, want)
	deadline := time.After(d)
	for len(got) < want {
		select {
		case id, ok := <-finished:
			if !ok {
				t.Fatalf("SubagentActivity 提前 close,只收尾了 %d/%d 路:%v", len(got), want, got)
			}
			got = append(got, id)
		case <-deadline:
			t.Fatalf("readLoop 被串行消费方卡死:只收尾了 %d/%d 路旁路活动轮(%v)", len(got), want, got)
		}
	}
}

// TestSession_ReadLoopNotBlockedByUndrainedSideActivity 锁定 sess-3110 的第一个阻塞点:
// Given 自主续轮活跃、两路旁路活动轮并发开着,When 消费方串行 drain(第一路没 close
// 就轮不到第二路)且第二路的事件远超单轮缓冲,Then readLoop 仍必须把 result 帧读到,
// 把两路活动轮一并收尾。
//
// 会被本用例拒绝的错误实现:readLoop 直接往每轮的有界 channel 上做阻塞发送 —— 第二路
// 灌满即死锁,一路都收尾不了。
func TestSession_ReadLoopNotBlockedByUndrainedSideActivity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := New(WithBinary("fake"), pipeSpawner(t, fakeTwoBackgroundSubagentsDuringAutonomousTurn))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	go func() {
		for at := range sess.AutonomousTurns() {
			for range at.Events { //nolint:revive // 只需 drain
			}
		}
	}()

	awaitActivities(t, serialSubagentActivityConsumer(sess), 2, 10*time.Second)
}

// fakeManyBackgroundSubagentsDuringAutonomousTurn 同样是自主续轮活跃期间的旁路活动,
// 但压的是**交出新一轮**这个阻塞点:owner 数远多于 SubagentActivity() 出口的缓冲,
// 而串行消费方只会认领第一路。
func fakeManyBackgroundSubagentsDuringAutonomousTurn(stdin io.Reader, stdout io.Writer) {
	const sid = "sess-3110-handoff"
	writeBackgroundTaskNotification(stdout, sid, "t1")
	for i := range manyBackgroundSubagents {
		writeSubagentFrame(stdout, fmt.Sprintf("toolu_owner_%02d", i), fmt.Sprintf("s%d", i))
	}
	writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
	_, _ = io.Copy(io.Discard, stdin)
}

// manyBackgroundSubagents 取得比 SubagentActivity() 出口缓冲大一截 —— 一轮里同时挂
// 十几个 run_in_background subagent 在真实现场里很常见(sess-3110 当时 5 个,同一批
// 派遣里再多几个就撞上出口缓冲)。
const manyBackgroundSubagents = 24

// TestSession_ReadLoopNotBlockedHandingOffSideActivities 锁定第二个阻塞点:
// Given 自主续轮活跃、同时有远超出口缓冲的 owner 各自吐内部活动,When 消费方串行
// 认领(停在第一路上),Then readLoop 仍必须读到 result 帧并把所有旁路活动轮收尾。
//
// 会被本用例拒绝的错误实现:readLoop 往有界的 subagentCh 上做阻塞发送 —— 缓冲满了
// 就再也读不到后面的帧,包括那条能解除所有阻塞的 result。
func TestSession_ReadLoopNotBlockedHandingOffSideActivities(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := New(WithBinary("fake"), pipeSpawner(t, fakeManyBackgroundSubagentsDuringAutonomousTurn))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	go func() {
		for at := range sess.AutonomousTurns() {
			for range at.Events { //nolint:revive // 只需 drain
			}
		}
	}()

	awaitActivities(t, serialSubagentActivityConsumer(sess), manyBackgroundSubagents, 10*time.Second)
}

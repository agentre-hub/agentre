package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// extractTextField 抓 stdin user frame 里 message.content[0].text 字段的 best-effort 取值。
// 跟原 shell fake 的 sed 正则等价（"text":"..."），失败 fallback "ack"。
var textFieldRE = regexp.MustCompile(`"text":"([^"]*)"`)

func extractTextField(line string) string {
	if m := textFieldRE.FindStringSubmatch(line); len(m) == 2 && m[1] != "" {
		return m[1]
	}
	return "ack"
}

// extractStringField 通用版：抓 JSON 行里 "<key>":"<value>" 模式的字段。
// fake CLI 处理 control_request 时拿 request_id / mode 用。
func extractStringField(line, key string) string {
	re := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `":"([^"]*)"`)
	if m := re.FindStringSubmatch(line); len(m) == 2 {
		return m[1]
	}
	return ""
}

// fakePersistent 模拟常驻 claude 子进程：每条 user frame 起一轮，喂 init + 回声
// assistant + result，直到 stdin EOF。
func fakePersistent(stdin io.Reader, stdout io.Writer) {
	const sid = "sess-persistent"
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	turn := 0
	for sc.Scan() {
		turn++
		reply := extractTextField(sc.Text())
		writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
		writeFrame(stdout, `{"type":"assistant","message":{"id":"m%d","content":[{"type":"text","text":"echo:%s"}]}}`, turn, reply)
		writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":%d,"output_tokens":%d}}`, sid, turn, turn)
	}
}

// fakeInterrupt 模拟"长 turn 被中断"：user frame 触发 init+partial，不发 result；
// control_request{interrupt} 触发 control_response{success} + result{interrupted}。
func fakeInterrupt(stdin io.Reader, stdout io.Writer) {
	const sid = "sess-interrupt"
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	turn := 0
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.Contains(line, `"type":"user"`):
			turn++
			reply := extractTextField(line)
			writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"m%d","content":[{"type":"text","text":"partial:%s"}]}}`, turn, reply)
		case strings.Contains(line, `"type":"control_request"`):
			reqID := extractStringField(line, "request_id")
			writeFrame(stdout, `{"type":"control_response","response":{"subtype":"success","request_id":%q}}`, reqID)
			writeFrame(stdout, `{"type":"result","subtype":"interrupted","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
		}
	}
}

// fakeSetMode 模拟 turn 之间切 mode：control_request → success（request_id 在
// response 内层，对齐真 CLI）；user frame → init + echo + result。
func fakeSetMode(stdin io.Reader, stdout io.Writer) {
	const sid = "sess-set-mode"
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	turn := 0
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.Contains(line, `"type":"control_request"`):
			reqID := extractStringField(line, "request_id")
			mode := extractStringField(line, "mode")
			writeFrame(stdout, `{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":{"mode":%q}}}`, reqID, mode)
		case strings.Contains(line, `"type":"user"`):
			turn++
			reply := extractTextField(line)
			writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"m%d","content":[{"type":"text","text":"echo:%s"}]}}`, turn, reply)
			writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
		}
	}
}

// fakeStopTask 模拟空闲态收到 control_request{stop_task}：抓 task_id 塞进 gotTaskID
// 供断言,回 control_response{success}。不处理 user frame —— StopTask 走空闲控制通道,
// 由常驻 readLoop dispatch control_response。
func fakeStopTask(gotTaskID chan<- string) fakeCLIFunc {
	return func(stdin io.Reader, stdout io.Writer) {
		sc := bufio.NewScanner(stdin)
		sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
		for sc.Scan() {
			line := sc.Text()
			if strings.Contains(line, `"type":"control_request"`) && strings.Contains(line, `"subtype":"stop_task"`) {
				reqID := extractStringField(line, "request_id")
				select {
				case gotTaskID <- extractStringField(line, "task_id"):
				default:
				}
				writeFrame(stdout, `{"type":"control_response","response":{"subtype":"success","request_id":%q}}`, reqID)
			}
		}
	}
}

// fakeMidTurnSetMode 模拟"长 turn 飞行中切 mode"：user frame 触发 init+partial
// （不发 result）；control_request{set_permission_mode} → success +
// status{permissionMode} + result{success}（结束本轮）。
func fakeMidTurnSetMode(readyForControl chan<- struct{}) fakeCLIFunc {
	return func(stdin io.Reader, stdout io.Writer) {
		const sid = "sess-mid-turn-set-mode"
		sc := bufio.NewScanner(stdin)
		sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
		turn := 0
		notifiedReady := false
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.Contains(line, `"type":"user"`):
				turn++
				reply := extractTextField(line)
				writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
				writeFrame(stdout, `{"type":"assistant","message":{"id":"m%d","content":[{"type":"text","text":"partial:%s"}]}}`, turn, reply)
				if !notifiedReady {
					close(readyForControl)
					notifiedReady = true
				}
			case strings.Contains(line, `"type":"control_request"`) && strings.Contains(line, `"subtype":"set_permission_mode"`):
				reqID := extractStringField(line, "request_id")
				mode := extractStringField(line, "mode")
				writeFrame(stdout, `{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":{"mode":%q}}}`, reqID, mode)
				writeFrame(stdout, `{"type":"system","subtype":"status","session_id":%q,"permissionMode":%q}`, sid, mode)
				writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
			}
		}
	}
}

// fakeRetry 模拟 Anthropic SDK 命中可重试错误退避两次：每条 user frame → init +
// 2×api_retry + assistant text + result.success。
func fakeRetry(stdin io.Reader, stdout io.Writer) {
	const sid = "sess-retry"
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	for sc.Scan() {
		reply := extractTextField(sc.Text())
		writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
		writeFrame(stdout, `{"type":"system","subtype":"api_retry","attempt":1,"max_retries":10,"retry_delay_ms":585.8,"error_status":529,"error":"rate_limit","session_id":%q,"uuid":"u1"}`, sid)
		writeFrame(stdout, `{"type":"system","subtype":"api_retry","attempt":2,"max_retries":10,"retry_delay_ms":1229.3,"error_status":529,"error":"rate_limit","session_id":%q,"uuid":"u2"}`, sid)
		writeFrame(stdout, `{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"echo:%s"}]}}`, reply)
		writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
	}
}

// fakeAlive 模拟健康的常驻进程：阻塞读 stdin，直到 EOF 才返回。
// 不写 stdout/stderr —— 用于健康检查窗口存活的回归。
func fakeAlive(stdin io.Reader, _ io.Writer) {
	_, _ = io.Copy(io.Discard, stdin)
}

// fakePassiveMode 模拟 Claude Code 2.1.145 trace：命中 "no-mode" 文本则发不带
// permissionMode 的 status 帧（前向兼容场景）；其它情况发 status{permissionMode:"default"}
// （ExitPlanMode 被批准后 CLI 自动从 plan → default 的回执）。
func fakePassiveMode(stdin io.Reader, stdout io.Writer) {
	const sid = "sess-passive-mode"
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	turn := 0
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, `"type":"user"`) {
			continue
		}
		turn++
		reply := extractTextField(line)
		writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[],"permissionMode":"plan"}`, sid)
		writeFrame(stdout, `{"type":"assistant","message":{"id":"m%d","content":[{"type":"text","text":"echo:%s"}]}}`, turn, reply)
		if strings.Contains(reply, "no-mode") {
			// 前向兼容：status 帧无 permissionMode → 不抬事件
			writeFrame(stdout, `{"type":"system","subtype":"status","status":null,"session_id":%q,"uuid":"u-no-mode"}`, sid)
		} else {
			writeFrame(stdout, `{"type":"system","subtype":"status","status":null,"permissionMode":"default","session_id":%q,"uuid":"u-passive"}`, sid)
		}
		writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
	}
}

// fakeManualCompact 复刻真实 CLI 收到手动 /compact(作为普通 user 帧)时的帧序列:
// 轮起步即推一帧 status:"compacting"(压缩进行中、子进程还活着的唯一信号),压缩完成
// 后才推 compact_boundary,最后 result 收尾。真实压缩(大上下文)可耗时 >120s;这里
// 用帧顺序复刻,不真睡。注意 resume 轮没有 per-turn init —— status 帧就是本轮首帧。
func fakeManualCompact(stdin io.Reader, stdout io.Writer) {
	const sid = "sess-manual-compact"
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	for sc.Scan() {
		if !strings.Contains(sc.Text(), `"type":"user"`) {
			continue
		}
		writeFrame(stdout, `{"type":"system","subtype":"status","status":"compacting","session_id":%q}`, sid)
		writeFrame(stdout, `{"type":"system","subtype":"compact_boundary","compact_metadata":{"pre_tokens":30117,"post_tokens":2697,"trigger":"manual","duration_ms":20696},"session_id":%q}`, sid)
		writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
	}
}

// resumeMissingStderr 复刻真实 CLI 命中 `claude --resume <gone-id>` 时的 stderr 输出。
const resumeMissingStderr = "No conversation found with session ID: 07dcda59-d426-4d66-b6d3-12d6d59bc5a3\n"

// drainText 消费 events channel，把所有 EventTextDelta 拼起来；忽略其它事件。
func drainText(t *testing.T, ch <-chan Event) string {
	t.Helper()
	var b strings.Builder
	for ev := range ch {
		if ev.Kind == EventTextDelta {
			b.WriteString(ev.Text)
		}
	}
	return b.String()
}

// TestSession_MultiTurn 走一遍 OpenSession → Turn × 2 → Close，验证：
//   - 两轮 Turn 用的是同一个子进程（fake 在 stdin EOF 时才退出）
//   - 每轮的事件 channel 在 result 帧后正常关闭，不会跨轮串味
//   - 助手文本能完整透出
func TestSession_MultiTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := New(WithBinary("fake"), pipeSpawner(t, fakePersistent))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)

	// Turn 1
	ch1, err := sess.Turn(ctx, "alpha")
	require.NoError(t, err)
	got1 := drainText(t, ch1)
	assert.Equal(t, "echo:alpha", got1)

	// Turn 2 —— 复用同一个 session
	ch2, err := sess.Turn(ctx, "beta")
	require.NoError(t, err)
	got2 := drainText(t, ch2)
	assert.Equal(t, "echo:beta", got2)

	require.NoError(t, sess.Close(ctx))
}

// TestSession_ManualCompactStatusReachesTurn 钉死:手动 /compact 轮的起步帧
// status:"compacting" 必须路由进本轮事件流。它是子进程「正在压缩、还活着」的唯一存活
// 信号 —— 曾被 isNonTurnFrame 当会话级噪音丢弃,导致 runtime 起步看门狗在压缩 >120s 时
// 把健康子进程误判为卡死硬杀(errStartupTimeout,用户侧表现为 /compact 总是报错)。
func TestSession_ManualCompactStatusReachesTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := New(WithBinary("fake"), pipeSpawner(t, fakeManualCompact))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)

	ch, err := sess.Turn(ctx, "/compact")
	require.NoError(t, err)

	var sawCompacting, sawBoundary bool
	for ev := range ch {
		switch ev.Kind {
		case EventStatus:
			if ev.Status == "compacting" {
				sawCompacting = true
			}
		case EventCompactBoundary:
			sawBoundary = true
		}
	}
	assert.True(t, sawCompacting, "status:compacting 起步帧应路由进本轮事件流(否则看门狗看不到存活信号)")
	assert.True(t, sawBoundary, "compact_boundary 帧仍应到达本轮")

	require.NoError(t, sess.Close(ctx))
}

// TestSession_CloseStopsTurns 验证 Close 之后再 Turn 会拿到错误。
func TestSession_CloseStopsTurns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := New(WithBinary("fake"), pipeSpawner(t, fakePersistent))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	require.NoError(t, sess.Close(ctx))

	_, err = sess.Turn(ctx, "after-close")
	assert.Error(t, err)
}

// fakeInterruptNoAck 模拟 CLI 收到 control_request 但迟迟不回执（卡在别的处理上，
// 既不回 control_response 也不发 result）。吞掉 stdin 所有行、绝不 ack —— 让
// Interrupt / StopTask 的 ack 等待落在 interruptAckBound 的有界超时上。
func fakeInterruptNoAck(gotLines chan<- string) fakeCLIFunc {
	return func(stdin io.Reader, stdout io.Writer) {
		sc := bufio.NewScanner(stdin)
		sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
		for sc.Scan() {
			select {
			case gotLines <- sc.Text():
			default:
			}
		}
	}
}

// TestSession_Interrupt_PendingAckIsBounded 验证 CLI 不回执时 Interrupt 的
// ack 等待**有界**（interruptAckBound=500ms），超时返回独立哨兵 ErrInterruptPending
// （errors.Is 可判），control_request 帧只写一次 —— 而不是无界挂到子进程死。
func TestSession_Interrupt_PendingAckIsBounded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got := make(chan string, 8)
	c := New(WithBinary("fake"), pipeSpawner(t, fakeInterruptNoAck(got)))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	start := time.Now()
	err = sess.Interrupt(ctx)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInterruptPending), "应返回可 errors.Is 判定的 ErrInterruptPending,got: %v", err)
	assert.Less(t, elapsed, 1500*time.Millisecond, "Interrupt 应在 interruptAckBound 内返回,got %v", elapsed)

	// 帧只写一次：fake 收到的 control_request 恰好一条 interrupt。
	var controlRequests []string
	collectUntil := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(collectUntil) {
		select {
		case line := <-got:
			if strings.Contains(line, `"type":"control_request"`) {
				controlRequests = append(controlRequests, line)
			}
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	require.Len(t, controlRequests, 1, "Interrupt 应恰好写一帧 control_request")
	assert.Contains(t, controlRequests[0], `"subtype":"interrupt"`)
}

// TestSession_StopTask_PendingAckIsBounded 验证 StopTask 的 ack
// 等待同样有界,CLI 不回执时返回 ErrInterruptPending（不无界挂死）。
func TestSession_StopTask_PendingAckIsBounded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got := make(chan string, 8)
	c := New(WithBinary("fake"), pipeSpawner(t, fakeInterruptNoAck(got)))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	start := time.Now()
	err = sess.StopTask(ctx, "b0n82mqaj")
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInterruptPending), "StopTask 超时应同样返回 ErrInterruptPending,got: %v", err)
	assert.Less(t, elapsed, 1500*time.Millisecond, "StopTask 应在 interruptAckBound 内返回,got %v", elapsed)
}

// TestSession_Interrupt 验证 control_request{interrupt} 路径：
//   - Turn 启动后子进程写出 partial assistant 块，然后阻塞读 stdin（不发 result）；
//   - Interrupt 写 control_request 帧 → fake 回 control_response{success} + result 帧；
//   - Interrupt 调用返回 nil；events channel 自然关闭；partial 文本保留。
func TestSession_Interrupt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := New(WithBinary("fake"), pipeSpawner(t, fakeInterrupt))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	ch, err := sess.Turn(ctx, "long-job")
	require.NoError(t, err)

	// 等 partial 出来再 Interrupt，否则可能在 user frame 被 fake 处理之前就发 ctrl 帧。
	// init 帧先到 → EventInit;跳过非 text_delta 直到拿到 partial 文本。
	var first Event
	for {
		ev, ok := <-ch
		require.True(t, ok, "expected partial text_delta before interrupt")
		if ev.Kind == EventTextDelta {
			first = ev
			break
		}
	}
	assert.Equal(t, "partial:long-job", first.Text)

	require.NoError(t, sess.Interrupt(ctx))

	// drain 剩余事件（result 帧到达后 channel 关闭）
	for range ch { //nolint:revive // 仅用于 drain
	}
}

// TestSession_InterruptAfterClose 验证 Close 之后 Interrupt 返回错误（不 panic）。
func TestSession_InterruptAfterClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := New(WithBinary("fake"), pipeSpawner(t, fakeInterrupt))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	require.NoError(t, sess.Close(ctx))

	assert.Error(t, sess.Interrupt(ctx))
}

// TestSession_StopTask 验证 control_request{stop_task} 路径：写出帧带 subtype
// stop_task + task_id,fake 回 control_response{success} → StopTask 返 nil。
// 空闲态(无 turn 在飞)也能停 —— 常驻 readLoop dispatch control_response。
func TestSession_StopTask(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	gotTaskID := make(chan string, 1)
	c := New(WithBinary("fake"), pipeSpawner(t, fakeStopTask(gotTaskID)))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	require.NoError(t, sess.StopTask(ctx, "b0n82mqaj"))

	select {
	case id := <-gotTaskID:
		assert.Equal(t, "b0n82mqaj", id, "stop_task 帧应带 CLI task_id")
	case <-ctx.Done():
		t.Fatal("fake CLI never received stop_task control_request")
	}
}

// TestSession_StopTaskAfterClose 验证 Close 之后 StopTask 返错(不 panic)。
func TestSession_StopTaskAfterClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	gotTaskID := make(chan string, 1)
	c := New(WithBinary("fake"), pipeSpawner(t, fakeStopTask(gotTaskID)))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	require.NoError(t, sess.Close(ctx))

	assert.Error(t, sess.StopTask(ctx, "b0n82mqaj"))
}

// TestSession_SetPermissionMode 验证 control_request{set_permission_mode} 路径：
//   - fake 收到 control_request → 回 control_response{success}（request_id 在
//     response 内层，对齐真 CLI）；
//   - SetPermissionMode 返 nil；
//   - 切换后 Turn 仍能正常 drain（验证 scanner 状态没被打乱）。
func TestSession_SetPermissionMode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := New(WithBinary("fake"), pipeSpawner(t, fakeSetMode))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	require.NoError(t, sess.SetPermissionMode(ctx, "plan"))

	ch, err := sess.Turn(ctx, "after-switch")
	require.NoError(t, err)
	got := drainText(t, ch)
	assert.Equal(t, "echo:after-switch", got)
}

// TestSession_SetPermissionMode_MidTurn 复刻用户报告的核心 bug：
// Turn 已开飞但 result 帧尚未到达时（典型场景：长 turn 中用户点 mode pill），
// SetPermissionMode 必须能立刻把 control_request 写下去并在 control_response
// 回来后返 nil；不能一直阻塞到 Turn 自然 done。
func TestSession_SetPermissionMode_MidTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	readyForControl := make(chan struct{})
	c := New(WithBinary("fake"), pipeSpawner(t, fakeMidTurnSetMode(readyForControl)))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	ch, err := sess.Turn(ctx, "long-job")
	require.NoError(t, err)

	// 等 partial 出来再切 mode，保证 Turn goroutine 已经在读 scanner。
	// init 帧先到 → EventInit;跳过非 text_delta 直到看到 partial 文本。
	for {
		ev, ok := <-ch
		require.True(t, ok, "expected partial text_delta before set-mode")
		if ev.Kind == EventTextDelta {
			break
		}
	}
	select {
	case <-readyForControl:
	case <-ctx.Done():
		require.NoError(t, ctx.Err(), "fake CLI should be ready to receive control_request")
	}

	// 给 SetPermissionMode 一个紧凑的截止：当前实现卡在 turnMu 上会让本步超时。
	setCtx, setCancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer setCancel()
	require.NoError(t, sess.SetPermissionMode(setCtx, "plan"),
		"SetPermissionMode 必须能在 Turn 在飞时立刻送出 control_request 并拿到 control_response")

	// drain 剩余事件（fake 在 set-mode 之后会发 status + result 让本轮收尾）。
	var sawModeChange bool
	for ev := range ch {
		if ev.Kind == EventPermissionModeChanged && ev.PermissionMode == "plan" {
			sawModeChange = true
		}
	}
	assert.True(t, sawModeChange, "fake 已发 system{status,permissionMode:plan}，应被抬成 EventPermissionModeChanged")
}

// TestSession_SetPermissionMode_InvalidMode 验证白名单校验在写帧之前生效。
func TestSession_SetPermissionMode_InvalidMode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := New(WithBinary("fake"), pipeSpawner(t, fakeSetMode))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	assert.Error(t, sess.SetPermissionMode(ctx, "nope"))
	assert.Error(t, sess.SetPermissionMode(ctx, ""))
}

// TestSession_RetryEventsArriveBeforeDone 验证 Session.Turn 能把 system.api_retry
// 帧抬成 EventRetry 推到本轮事件 channel，且不影响后续 text / done 的顺序。
// 这是 Claude 后端"重试可视化"的最底层契约——chat_svc 会用这条信号驱动
// 前端 RetryNoticeCard。
func TestSession_RetryEventsArriveBeforeDone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := New(WithBinary("fake"), pipeSpawner(t, fakeRetry))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	ch, err := sess.Turn(ctx, "alpha")
	require.NoError(t, err)

	var (
		retries  []Event
		text     string
		sawText  bool
		eventLog []EventKind
	)
	for ev := range ch {
		eventLog = append(eventLog, ev.Kind)
		switch ev.Kind {
		case EventRetry:
			retries = append(retries, ev)
		case EventTextDelta:
			text += ev.Text
			sawText = true
		}
	}

	require.Len(t, retries, 2, "fake script emits 2 api_retry frames; event log: %v", eventLog)
	require.NotNil(t, retries[0].Retry)
	assert.Equal(t, 1, retries[0].Retry.Attempt)
	assert.Equal(t, 10, retries[0].Retry.MaxAttempts)
	assert.Equal(t, 529, retries[0].Retry.ErrorStatus)
	assert.Equal(t, "rate_limit", retries[0].Retry.ErrorCode)
	assert.InDelta(t, 585.8, retries[0].Retry.DelayMs, 0.0001)
	assert.Equal(t, "sess-retry", retries[0].SessionID)

	require.NotNil(t, retries[1].Retry)
	assert.Equal(t, 2, retries[1].Retry.Attempt)

	assert.True(t, sawText, "retry 之后的 assistant text 必须到达")
	assert.Equal(t, "echo:alpha", text)
}

// TestSession_SetPermissionModeAfterClose 验证 Close 后调返错而不 panic。
func TestSession_SetPermissionModeAfterClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := New(WithBinary("fake"), pipeSpawner(t, fakeSetMode))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	require.NoError(t, sess.Close(ctx))

	assert.Error(t, sess.SetPermissionMode(ctx, "plan"))
}

// TestSession_OpenSessionRejectsResumeMissing 复刻用户报告的核心 bug：
// 真实场景下 `claude --resume <gone-id>` 会立刻写 stderr 并 exit 1，但是 OpenSession
// 之前 spawn 完就直接返回 handle，错误被 boundedBuffer 静默吃掉 → 前端无任何报错。
// 修复后 OpenSession 在 200ms 早退检测窗口里必须拿到 wrapped ErrSessionNotFound。
func TestSession_OpenSessionRejectsResumeMissing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c := New(WithBinary("fake"), pipeSpawner(t, nil, withExitCode(1), withStderr(resumeMissingStderr)))
	sess, err := c.OpenSession(ctx)
	require.Error(t, err, "立刻 exit 的子进程必须让 OpenSession 报错")
	assert.Nil(t, sess, "出错时不应返回半成品 Session")
	assert.ErrorIs(t, err, ErrSessionNotFound, "命中 stderr 'No conversation found' → ErrSessionNotFound")
	assert.Contains(t, err.Error(), "No conversation found",
		"错误文案必须包含 CLI 真实 stderr，方便用户排查")
}

// TestSession_OpenSessionHealthyPassesCheckWindow 健康路径回归：进程 spawn 后只
// 阻塞读 stdin（典型的 claude --print 流式守护行为），200ms 健康检查窗口里没退出
// 也没首帧 → OpenSession 必须正常返回 Session。
func TestSession_OpenSessionHealthyPassesCheckWindow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := New(WithBinary("fake"), pipeSpawner(t, fakeAlive))
	start := time.Now()
	sess, err := c.OpenSession(ctx)
	elapsed := time.Since(start)
	require.NoError(t, err)
	require.NotNil(t, sess)
	t.Cleanup(func() { _ = sess.Close(context.Background()) })

	assert.GreaterOrEqual(t, elapsed, claudeStartupCheckTimeout,
		"OpenSession 必须等满健康检查窗口，确保给坏 spawn 足够时间冒出来")
}

// TestSession_ExitErrSurfacesProviderSessionGone 进程死亡后 Session.ExitErr
// 必须把分类后的 ErrSessionNotFound 露出来。runtime 层 0-frame fallback 用
// 这个方法把 RunResult.StopErr 替换成真错。
func TestSession_ExitErrSurfacesProviderSessionGone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 直接构造 process 模拟 "OpenSession 之后才发现进程已死" 的链路；nil fakeCLI
	// 让 process 在构造时就处于已退出状态（同步关 exit channel）。
	p := newPipeProcess(t, ctx, nil, withExitCode(1), withStderr(resumeMissingStderr))
	require.True(t, p.hasExited(), "nil fakeCLI 构造的 process 应当立刻报 hasExited=true")

	s := &Session{proc: p}
	exitErr := s.ExitErr()
	require.Error(t, exitErr)
	assert.ErrorIs(t, exitErr, ErrSessionNotFound)
}

// TestSession_PassivePermissionModeChange 验证 CLI 自身切换 permission mode
// 后的 system{subtype:"status",permissionMode:...} 帧会被抬成
// EventPermissionModeChanged。真实场景：用户启动 plan mode，AI 调 ExitPlanMode
// 工具被批准后，CLI 自动切到 default 并发这条 status 帧。
// 帧形态来自 Claude Code 2.1.145 trace。
func TestSession_PassivePermissionModeChange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := New(WithBinary("fake"), pipeSpawner(t, fakePassiveMode))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	ch, err := sess.Turn(ctx, "exit-plan")
	require.NoError(t, err)

	var (
		modeChanges []Event
		eventLog    []EventKind
	)
	for ev := range ch {
		eventLog = append(eventLog, ev.Kind)
		if ev.Kind == EventPermissionModeChanged {
			modeChanges = append(modeChanges, ev)
		}
	}

	require.Len(t, modeChanges, 1, "expected exactly one EventPermissionModeChanged; eventLog=%v", eventLog)
	assert.Equal(t, "default", modeChanges[0].PermissionMode, "CLI 退出 plan mode 后切到 default")
	assert.Equal(t, "sess-passive-mode", modeChanges[0].SessionID)

	// EventDone 必须紧随 EventPermissionModeChanged，验证 status 帧没把后续 result
	// 帧打乱。
	var lastIdx int
	for i, k := range eventLog {
		if k == EventPermissionModeChanged {
			lastIdx = i
		}
	}
	require.Less(t, lastIdx, len(eventLog)-1, "status frame must not be the terminal event")
	assert.Equal(t, EventDone, eventLog[len(eventLog)-1], "result 帧产出的 EventDone 必须是最后一条")
}

// TestSession_DoneUsesLastAssistantUsage 同 TestStream_DoneUsesLastAssistantUsage，
// 但走 Session.parseLine（常驻进程多 turn 路径）。额外验证 turn 之间 lastAssistantUsage
// 不串味——第二轮就算 assistant 帧没带 usage，也不能把第一轮的值带过来。
func TestSession_DoneUsesLastAssistantUsage(t *testing.T) {
	s := &Session{}

	// Turn 1：两次内部 API call。result.usage 是累加；正确口径是最后一帧 assistant 的 per-call usage。
	turn1Frames := []string{
		`{"type":"system","subtype":"init","session_id":"sx"}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"X","input":{}}],"usage":{"input_tokens":200,"output_tokens":50,"cache_read_input_tokens":10000,"cache_creation_input_tokens":0}}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`,
		`{"type":"assistant","message":{"id":"m2","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":50,"output_tokens":20,"cache_read_input_tokens":10300,"cache_creation_input_tokens":50}}}`,
		`{"type":"result","subtype":"success","session_id":"sx","usage":{"input_tokens":250,"output_tokens":70,"cache_read_input_tokens":20300,"cache_creation_input_tokens":50}}`,
	}
	var doneEv *Event
	for _, line := range turn1Frames {
		evs, isResult := s.parseLine([]byte(line))
		if isResult {
			require.Len(t, evs, 1)
			doneEv = &evs[0]
		}
	}
	require.NotNil(t, doneEv, "expected EventDone after turn 1 result frame")
	assert.Equal(t, 50, doneEv.Usage.PromptTokens, "Turn1 EventDone.PromptTokens 必须取 last per-call (50)，不是 result 累加 (250)")
	assert.Equal(t, 10300, doneEv.Usage.CachedTokens)
	assert.Equal(t, 50, doneEv.Usage.CacheCreationTokens)
	assert.Equal(t, 20, doneEv.Usage.CompletionTokens)

	// Turn 2 起始：parseLine 应已把 lastAssistantUsage 清空，避免 turn 间串味。
	// 极简 turn：assistant 帧不带 usage → EventDone 必须 fallback 到 result.usage（10/5/3/1），
	// 而不是 turn1 的 50/20 余值。
	turn2Frames := []string{
		`{"type":"assistant","message":{"id":"m3","content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"result","subtype":"success","session_id":"sx","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":3,"cache_creation_input_tokens":1}}`,
	}
	doneEv = nil
	for _, line := range turn2Frames {
		evs, isResult := s.parseLine([]byte(line))
		if isResult {
			require.Len(t, evs, 1)
			doneEv = &evs[0]
		}
	}
	require.NotNil(t, doneEv)
	assert.Equal(t, 10, doneEv.Usage.PromptTokens, "Turn2 没 assistant usage → fallback 到 result.usage；不能拿 turn1 的余值")
	assert.Equal(t, 3, doneEv.Usage.CachedTokens)
	assert.Equal(t, 1, doneEv.Usage.CacheCreationTokens)
	assert.Equal(t, 5, doneEv.Usage.CompletionTokens)
}

// TestSession_GLMRealFrameShape 回归 Bug 2：复刻 GLM (https://huu.dqy.ink) 实际返回
// 的 assistant 帧 shape（来自 ~/.claude/projects/.../7470a64f-…jsonl 的实测样本），
// 多余的 server_tool_use / service_tier / cache_creation 对象 / iterations 数组等
// 字段不该让 json.Unmarshal 失败；rawUsage 的 4 个 int 字段必须能从中正确抽出。
//
// 这个 case 跑过 → 说明 parser 在 JSONL 形态的帧上工作正常；usage = 0 的现象一定
// 是 STDOUT 流跟 JSONL 落盘的字段路径不一致（多半 --include-partial-messages
// 让 CLI 把 usage 移到了 stream_event 类帧里，需要 raw dump 进一步定位）。
// 跑不过 → parser 有隐藏 bug，需要直接修。
func TestSession_GLMRealFrameShape(t *testing.T) {
	s := &Session{}

	// 完全照搬 7470a64f-…jsonl:line 实测 assistant 帧 message 段，外层 type
	// 是 STDOUT 协议的样子(没 parentUuid / uuid / timestamp 这些 JSONL 元数据)。
	glmFrame := `{"type":"assistant","message":{"type":"message","id":"02177969507279077fce418cd3a659821a063326c55dce3b59e46","role":"assistant","content":[{"type":"thinking","thinking":"The user wants to see the directory contents.","signature":""}],"model":"glm-5.1","stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":36079,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":61,"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":"standard","cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0},"inference_geo":"","iterations":[],"speed":"standard"},"stop_details":null}}`

	// 喂入 init + glm assistant + 一个无 usage 的 result，断言 EventDone.Usage
	// 必须取自 lastAssistantUsage（即 glm assistant 帧里的 usage）。
	frames := []string{
		`{"type":"system","subtype":"init","session_id":"sx","model":"glm-5.1"}`,
		glmFrame,
		`{"type":"result","subtype":"success","session_id":"sx"}`,
	}
	var doneEv *Event
	for _, line := range frames {
		evs, isResult := s.parseLine([]byte(line))
		if isResult {
			require.Len(t, evs, 1)
			doneEv = &evs[0]
		}
	}
	require.NotNil(t, doneEv, "result 帧应当产出 EventDone")
	assert.Equal(t, 36079, doneEv.Usage.PromptTokens, "GLM 实测帧 input_tokens 必须被解出")
	assert.Equal(t, 61, doneEv.Usage.CompletionTokens, "GLM 实测帧 output_tokens 必须被解出")
	assert.Equal(t, "glm-5.1", doneEv.Model, "system.init.model = glm-5.1 必须透到 EventDone.Model")
}

// TestSession_StreamEventMessageDeltaUsage 回归 Bug 2 真正的根因:
// --include-partial-messages 模式下,CLI 把 Anthropic SSE delta 包成 type=stream_event
// 帧推到 STDOUT;每次内部 API call 的最终 usage 在 stream_event.event.type =
// message_delta 上,**不在**随后那条 merged 'assistant' 帧上 —— 后者的 usage 是
// CLI 给的 message_start 状态(input_tokens=0 / output_tokens=0)的副本。parser
// 必须:
//
//	(1) 解 stream_event message_delta 把真 usage 存 lastAssistantUsage;
//	(2) 不让 merged assistant 帧的 0 usage 把它打回 0(zero-clobber guard)。
//
// 数据来自 /tmp/cc-raw.log 实测(GLM via gateway,session_id=a948e6aa-…)。
func TestSession_StreamEventMessageDeltaUsage(t *testing.T) {
	s := &Session{}

	frames := []string{
		`{"type":"system","subtype":"init","session_id":"sx","model":"glm-5.1"}`,
		// 第 1 次 API call:message_start usage 是 0 占位
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":null,"event":{"type":"message_start","message":{"type":"message","id":"m1","role":"assistant","content":[],"model":"glm-5.1","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0}}}}`,
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":null,"event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`,
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hi"}}}`,
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":null,"event":{"type":"content_block_stop","index":0}}`,
		// message_delta 才带真 usage
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":null,"event":{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"input_tokens":1180,"output_tokens":61,"cache_read_input_tokens":34496}}}`,
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":null,"event":{"type":"message_stop"}}`,
		// merged assistant 帧:usage 全 0(CLI 没把 delta 累回去)
		`{"type":"assistant","parent_tool_use_id":null,"message":{"type":"message","id":"m1","role":"assistant","content":[{"type":"thinking","thinking":"hi"}],"model":"glm-5.1","stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0}}}`,
		`{"type":"result","subtype":"success","session_id":"sx"}`,
	}

	var doneEv *Event
	var usageEvents []Event
	for _, line := range frames {
		evs, isResult := s.parseLine([]byte(line))
		for _, ev := range evs {
			if ev.Kind == EventUsage {
				usageEvents = append(usageEvents, ev)
			}
		}
		if isResult {
			require.Len(t, evs, 1)
			doneEv = &evs[0]
		}
	}

	require.NotNil(t, doneEv, "result 帧应当产出 EventDone")
	assert.Equal(t, 1180, doneEv.Usage.PromptTokens, "EventDone.PromptTokens 必须取自 message_delta(1180),不是 merged assistant 帧的 0")
	assert.Equal(t, 61, doneEv.Usage.CompletionTokens)
	assert.Equal(t, 34496, doneEv.Usage.CachedTokens)

	// message_delta 应当顺手 emit 一条 EventUsage 让上层 (chat_svc → 前端进度条)
	// 在 turn 内实时刷新「已用上下文」。merged assistant 帧的 0 usage 不应再 emit
	// EventUsage(避免进度条骤降到 0)。
	require.Len(t, usageEvents, 1, "应仅 message_delta emit 一条 EventUsage,merged assistant 帧的 0 usage 不该 emit")
	assert.Equal(t, 1180, usageEvents[0].Usage.PromptTokens)
}

// TestSession_EmitsEventInitOnSystemInitWithModel —— 长 Session 多轮场景下,每个
// turn 开头 CLI 都会发 system.init(model 可能变),parseLine 应当 emit 一条
// EventInit 携带 SessionID + Model,让上层 agentruntime 实时刷新 catalog 兜底的
// context window,而不是等 EventDone 才知道。
func TestSession_EmitsEventInitOnSystemInitWithModel(t *testing.T) {
	s := &Session{}
	evs, _ := s.parseLine([]byte(`{"type":"system","subtype":"init","session_id":"sx","model":"claude-sonnet-4-6"}`))
	require.Len(t, evs, 1, "system.init 帧带 model 时应 emit 一条 EventInit")
	assert.Equal(t, EventInit, evs[0].Kind)
	assert.Equal(t, "sx", evs[0].SessionID)
	assert.Equal(t, "claude-sonnet-4-6", evs[0].Model)
}

// TestSession_DoesNotEmitEventInitWhenModelMissing —— init 帧不报 model 时不发
// EventInit,避免引导上层用空 model 做无效 catalog 查询。
func TestSession_DoesNotEmitEventInitWhenModelMissing(t *testing.T) {
	s := &Session{}
	evs, _ := s.parseLine([]byte(`{"type":"system","subtype":"init","session_id":"sx"}`))
	for _, ev := range evs {
		assert.NotEqual(t, EventInit, ev.Kind, "model 缺省时不应 emit EventInit")
	}
}

// TestSession_ReplayRealRawLog 端到端回放:如果 AGENTRE_REPLAY_CC_RAW 指向一份
// 真实 /tmp/cc-raw.log,把每一行喂给 parseLine,断言最终 EventDone.Usage 非零。
// 默认 env 未设跳过(CI / 其它开发机上没这个文件)。给 GLM repro 用,排查时打开。
func TestSession_ReplayRealRawLog(t *testing.T) {
	path := os.Getenv("AGENTRE_REPLAY_CC_RAW")
	if path == "" {
		t.Skip("set AGENTRE_REPLAY_CC_RAW to replay an actual raw log")
	}
	f, err := os.Open(path) //nolint:gosec // 测试 helper,path 来自 env。
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	s := &Session{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	var doneEv *Event
	var usageEmits int
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		evs, isResult := s.parseLine(line)
		for _, ev := range evs {
			if ev.Kind == EventUsage {
				usageEmits++
			}
		}
		if isResult {
			require.Len(t, evs, 1)
			cp := evs[0]
			doneEv = &cp
		}
	}
	require.NoError(t, sc.Err())
	require.NotNil(t, doneEv, "raw log 必须包含至少一个 result 帧")
	assert.NotEqual(t, 0, doneEv.Usage.PromptTokens, "回放真 log:PromptTokens 不该是 0")
	t.Logf("replay done: model=%q usage=%+v EventUsage_emits=%d",
		doneEv.Model, doneEv.Usage, usageEmits)
}

// TestSession_StreamEventSubagentMessageDeltaSkipped 锁住:subagent 内部 API call
// (parent_tool_use_id != "") 的 stream_event message_delta 用量不应影响主 agent
// 的 lastAssistantUsage —— 跟现有 assistant 帧的 subagent 过滤语义一致。
func TestSession_StreamEventSubagentMessageDeltaSkipped(t *testing.T) {
	s := &Session{}
	frames := []string{
		`{"type":"system","subtype":"init","session_id":"sx","model":"glm-5.1"}`,
		// 主 agent 一帧:真 usage
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":null,"event":{"type":"message_delta","delta":{},"usage":{"input_tokens":500,"output_tokens":20,"cache_read_input_tokens":0}}}`,
		// 然后跟一个 subagent 的 message_delta:input_tokens 很小(子会话上下文)
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":"toolu-A","event":{"type":"message_delta","delta":{},"usage":{"input_tokens":50,"output_tokens":10,"cache_read_input_tokens":0}}}`,
		`{"type":"result","subtype":"success","session_id":"sx"}`,
	}
	var doneEv *Event
	for _, line := range frames {
		evs, isResult := s.parseLine([]byte(line))
		if isResult {
			doneEv = &evs[0]
		}
	}
	require.NotNil(t, doneEv)
	assert.Equal(t, 500, doneEv.Usage.PromptTokens, "subagent message_delta 不能覆盖主 agent 的 500")
}

// TestSession_StatusFrameWithoutPermissionMode 前向兼容回归：CLI 可能在未来给
// system{subtype:"status"} 帧加别的字段（例如 status:running）但没有 permissionMode。
// 我们必须静默忽略，不能产生伪事件，也不能打断后续 result 帧。
func TestSession_StatusFrameWithoutPermissionMode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := New(WithBinary("fake"), pipeSpawner(t, fakePassiveMode))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	ch, err := sess.Turn(ctx, "no-mode")
	require.NoError(t, err)

	var modeChanges int
	sawDone := false
	for ev := range ch {
		if ev.Kind == EventPermissionModeChanged {
			modeChanges++
		}
		if ev.Kind == EventDone {
			sawDone = true
		}
	}
	assert.Zero(t, modeChanges, "status 帧没有 permissionMode → 不抬事件")
	assert.True(t, sawDone, "result 帧仍要正常关闭 channel")
}

// TestSession_TurnReturnsExitErrWhenProcessDied 模拟 "session 已开但子进程
// 已经暴毙" 的边界场景：Turn 写 stdin 时拿 broken pipe，方法必须把 broken pipe
// 翻成真正的 ErrSessionNotFound（来自子进程 stderr）。
func TestSession_TurnReturnsExitErrWhenProcessDied(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 直接构造 process + Session（绕开 OpenSession 的健康检查，模拟"健康检查
	// 后才发生的进程暴毙"——理论上这种 race 几乎不存在，但要给上层兜底兜得住）。
	p := newPipeProcess(t, ctx, nil, withExitCode(1), withStderr(resumeMissingStderr))
	require.True(t, p.hasExited(), "nil fakeCLI 构造的 process 应当立刻报 hasExited=true")

	s := newSession(p, nil, "") // 不起读循环:本例只验 Turn 写 stdin 的失败路径

	_, err := s.Turn(ctx, "hello")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSessionNotFound,
		"子进程死了之后 Turn 写 stdin 拿 broken pipe，应当被替换成真实退出错误")
}

// fakeBackgroundTask 复刻真实 CLI 2.1.162 抓到的「后台任务 + 自主续轮」帧序。
// turn1:启动 run_in_background → result#1;随后不等 stdin 自主吐
// task_updated(后台任务收尾状态推送) → task_notification(后台型) → 续轮
// (init+text+result#2);turn2:正常回声。
//
// 关键:真实 CLI 在 result#1 之后、task_notification 之前先吐一帧
// system{subtype:"task_updated"}(后台任务完成的状态 patch)。它空闲到达、既非
// 后台 task_notification 也非已知非 turn 帧 —— 若 readLoop 把它当 turn 起始帧卡在
// <-pendingTurns 上,后面的 task_notification / 续轮就永远读不到(见 sess-429)。
func fakeBackgroundTask(stdin io.Reader, stdout io.Writer) {
	const sid = "sess-bgtask"
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	turn := 0
	for sc.Scan() {
		turn++
		reply := extractTextField(sc.Text())
		if turn == 1 {
			// turn1:启动后台任务,以 result#1 收尾(模型主动结束本轮)。
			writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a1","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"sleep 1","run_in_background":true}}]}}`)
			writeFrame(stdout, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu1","content":"Command running in background with ID: bg1"}]}}`)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a2","content":[{"type":"text","text":"started:%s"}]}}`, reply)
			writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
			// —— 不等下一条 stdin,自主吐后台完成续轮 ——
			// 真 CLI 先吐一帧 task_updated(后台任务收尾的状态 patch),再吐 task_notification。
			writeFrame(stdout, `{"type":"system","subtype":"task_updated","task_id":"bg1","patch":{"status":"completed","end_time":1780625678929},"session_id":%q}`, sid)
			writeFrame(stdout, `{"type":"system","subtype":"task_notification","task_id":"bg1","tool_use_id":"tu1","status":"completed","output_file":"/tmp/tasks/bg1.output","summary":"Background command completed"}`)
			writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a3","content":[{"type":"text","text":"autonomous:listing"}]}}`)
			writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":2,"output_tokens":2}}`, sid)
			continue
		}
		// turn2:普通回声。
		writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
		writeFrame(stdout, `{"type":"assistant","message":{"id":"a4","content":[{"type":"text","text":"echo:%s"}]}}`, reply)
		writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
	}
}

// TestSession_BackgroundTaskAutonomousTurn 是本案基石回归:
//
//	(a) Turn1 channel 只收到 turn1 文本("started:..."),在 result#1 后 close,
//	    不串入自主续轮的 "autonomous:listing";
//	(b) Session.AutonomousTurns() 吐出自主续轮,其文本 = "autonomous:listing";
//	(c) Turn2 只收到 "echo:beta",无错位。
func TestSession_BackgroundTaskAutonomousTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := New(WithBinary("fake"), pipeSpawner(t, fakeBackgroundTask))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	// (a) Turn1 干净收尾。
	ch1, err := sess.Turn(ctx, "alpha")
	require.NoError(t, err)
	got1 := drainText(t, ch1)
	assert.Equal(t, "started:alpha", got1)
	assert.NotContains(t, got1, "autonomous", "Turn1 不应吞掉自主续轮帧")

	// (b) 自主续轮经 AutonomousTurns 吐出。
	var at *AutoTurn
	select {
	case at = <-sess.AutonomousTurns():
	case <-time.After(2 * time.Second):
		t.Fatal("expected an autonomous turn within 2s")
	}
	require.NotNil(t, at)
	assert.Equal(t, "background_task", at.Trigger)
	assert.Equal(t, "autonomous:listing", drainText(t, at.Events))

	// (c) Turn2 无错位。
	ch2, err := sess.Turn(ctx, "beta")
	require.NoError(t, err)
	assert.Equal(t, "echo:beta", drainText(t, ch2))
}

// fakeBackgroundTasksChanged 复刻真实 CLI 2.1.205 抓到的「后台任务空闲完成」帧序 ——
// 与 fakeBackgroundTask(2.1.162)的唯一差别:2.1.205 在 result#1 之后、task_updated
// 之前,先吐一帧 system{subtype:"background_tasks_changed"}(后台任务清单变化推送)。
// 真机抓帧(sleep 18 后台任务、turn 先结束)的空闲帧序:
//
//	result#1 → background_tasks_changed → task_updated → task_notification(后台型)
//	→ init → assistant → result#2
//
// background_tasks_changed 空闲到达时,既非后台型 task_notification、也不在旧
// isNonTurnFrame 白名单里(2.1.162 尚未有此 subtype),readLoop 会把它当 turn 起始帧
// 卡死在 <-pendingTurns 上 —— 后面的 task_notification / 自主续轮永远读不到(sess-1535
// 「后台任务完成续不上对话」复发,与 sess-429 同类,只是新增了一个 subtype)。
func fakeBackgroundTasksChanged(stdin io.Reader, stdout io.Writer) {
	const sid = "sess-bgtaskschanged"
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	turn := 0
	for sc.Scan() {
		turn++
		reply := extractTextField(sc.Text())
		if turn == 1 {
			writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a1","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"sleep 18","run_in_background":true}}]}}`)
			writeFrame(stdout, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu1","content":"Command running in background with ID: bg1"}]}}`)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a2","content":[{"type":"text","text":"started:%s"}]}}`, reply)
			writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
			// —— 空闲自主续轮:2.1.205 先吐 background_tasks_changed(清单变化),再吐
			// task_updated(状态 patch),最后 task_notification(后台型)起自主续轮。
			writeFrame(stdout, `{"type":"system","subtype":"background_tasks_changed","tasks":[],"session_id":%q}`, sid)
			writeFrame(stdout, `{"type":"system","subtype":"task_updated","task_id":"bg1","patch":{"status":"completed","end_time":1783590366870},"session_id":%q}`, sid)
			writeFrame(stdout, `{"type":"system","subtype":"task_notification","task_id":"bg1","tool_use_id":"tu1","status":"completed","output_file":"/tmp/tasks/bg1.output","summary":"Background command completed"}`)
			writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a3","content":[{"type":"text","text":"autonomous:listing"}]}}`)
			writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":2,"output_tokens":2}}`, sid)
			continue
		}
		writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
		writeFrame(stdout, `{"type":"assistant","message":{"id":"a4","content":[{"type":"text","text":"echo:%s"}]}}`, reply)
		writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
	}
}

// TestSession_BackgroundTasksChangedFrameKeepsReaderAlive 钉死 sess-1535 复发:CLI 2.1.205
// 在后台任务空闲完成时,于 task_notification 之前先吐一帧 background_tasks_changed。它空闲
// 到达、既非后台型 task_notification 也不在旧 isNonTurnFrame 白名单 —— 修复前 readLoop 卡死
// 在 <-pendingTurns,后台完成的自主续轮永远到不了 autoCh,本测试会超时。
func TestSession_BackgroundTasksChangedFrameKeepsReaderAlive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := New(WithBinary("fake"), pipeSpawner(t, fakeBackgroundTasksChanged))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	// (a) Turn1 干净收尾,不吞自主续轮帧。
	ch1, err := sess.Turn(ctx, "alpha")
	require.NoError(t, err)
	got1 := drainText(t, ch1)
	assert.Equal(t, "started:alpha", got1)
	assert.NotContains(t, got1, "autonomous", "Turn1 不应吞掉自主续轮帧")

	// (b) background_tasks_changed 空闲到达不得卡死读循环:自主续轮必须仍能浮现。
	var at *AutoTurn
	select {
	case at = <-sess.AutonomousTurns():
	case <-time.After(2 * time.Second):
		t.Fatal("background_tasks_changed 空闲到达后读循环卡死:自主续轮从未到达 " +
			"(该帧落入 <-pendingTurns 阻塞,task_notification 再也读不到)")
	}
	require.NotNil(t, at)
	assert.Equal(t, "background_task", at.Trigger)
	assert.Equal(t, "autonomous:listing", drainText(t, at.Events))

	// (c) Turn2 无错位。
	ch2, err := sess.Turn(ctx, "beta")
	require.NoError(t, err)
	assert.Equal(t, "echo:beta", drainText(t, ch2))
}

// fakeIdleSetModeThenAutonomous 模拟「空闲(无 user turn 在飞)切 mode → 后台任务完成
// 自主续轮」:控制帧到达即回 control_response + system{status,permissionMode}(对齐真
// CLI,见 SetPermissionMode doc),随后不等任何 stdin 自主吐一轮后台完成续轮
// (task_notification 起始 + assistant + result)。
func fakeIdleSetModeThenAutonomous(stdin io.Reader, stdout io.Writer) {
	const sid = "sess-idle-setmode-auto"
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, `"type":"control_request"`) {
			continue
		}
		reqID := extractStringField(line, "request_id")
		mode := extractStringField(line, "mode")
		// 真 CLI:set_permission_mode 回 control_response + system{status,permissionMode}。
		writeFrame(stdout, `{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":{"mode":%q}}}`, reqID, mode)
		writeFrame(stdout, `{"type":"system","subtype":"status","session_id":%q,"permissionMode":%q}`, sid, mode)
		// 随后后台任务完成,CLI 自主续轮(无 user turn 触发)。
		writeFrame(stdout, `{"type":"system","subtype":"task_notification","task_id":"bg1","tool_use_id":"tu1","status":"completed","output_file":"/tmp/tasks/bg1.output","summary":"Background command completed"}`)
		writeFrame(stdout, `{"type":"assistant","message":{"id":"a1","content":[{"type":"text","text":"autonomous:listing"}]}}`)
		writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
	}
}

// TestSession_IdleSetPermissionModeKeepsReaderAlive 锁定一个真实缺陷:readLoop 对
// 每一帧都走 route→currentTurn;空闲(active==nil、非后台 task_notification)时 currentTurn
// 落到 <-pendingTurns 阻塞。空闲 SetPermissionMode 收到的 control_response / 随后的
// system{status} 都属于「非 turn 归属」帧,会把读循环永久卡在 pendingTurns 上 —— 之后
// 后台任务完成的自主续轮再也读不到 stdout。
//
// SetPermissionMode 本身仍返 nil(control_response 在 parseLine 阶段已 dispatch 到 ctrl
// channel,早于 route 阻塞),所以 bug 对调用方不可见;但读循环已冻住。本测试断言自主
// 续轮仍能在限时内经 AutonomousTurns() 浮现 —— 修复前会超时。
func TestSession_IdleSetPermissionModeKeepsReaderAlive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := New(WithBinary("fake"), pipeSpawner(t, fakeIdleSetModeThenAutonomous))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	// 空闲切 mode:control_response 让本调用返回,但 control_response + status 帧不应
	// 把读循环卡在 pendingTurns 上。
	require.NoError(t, sess.SetPermissionMode(ctx, "plan"))

	// 后台任务完成的自主续轮必须仍能浮现。读循环若已卡死,这一轮永远到不了 autoCh。
	var at *AutoTurn
	select {
	case at = <-sess.AutonomousTurns():
	case <-time.After(2 * time.Second):
		t.Fatal("空闲 SetPermissionMode 后读循环卡死:自主续轮从未到达 " +
			"(control_response / 空闲 status 落入 <-pendingTurns 阻塞)")
	}
	require.NotNil(t, at)
	assert.Equal(t, "background_task", at.Trigger)
	assert.Equal(t, "autonomous:listing", drainText(t, at.Events))
}

func TestParseSystemTask_CarriesTaskType(t *testing.T) {
	f := rawFrame{
		Type: "system", Subtype: "task_started",
		TaskID: "bg1", ToolUseID: "tu1", Description: "Sleep for 5 seconds",
		TaskType: "local_bash",
	}
	ev, ok := parseSystemTask(f, "sx")
	require.True(t, ok)
	require.NotNil(t, ev.Tool)
	require.NotNil(t, ev.Tool.Subagent)
	assert.Equal(t, "local_bash", ev.Tool.Subagent.TaskType)
	assert.Equal(t, "Sleep for 5 seconds", ev.Tool.Subagent.TaskDescription)
}

func TestBackgroundTaskAutonomousTurn_CarriesCompletedTask(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := New(WithBinary("fake"), pipeSpawner(t, fakeBackgroundTask))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	ch1, err := sess.Turn(ctx, "alpha")
	require.NoError(t, err)
	_ = drainText(t, ch1)

	var at *AutoTurn
	select {
	case at = <-sess.AutonomousTurns():
	case <-time.After(2 * time.Second):
		t.Fatal("expected autonomous turn")
	}
	require.NotNil(t, at.CompletedTask)
	assert.Equal(t, "tu1", at.CompletedTask.ToolUseID)
	assert.Equal(t, "bg1", at.CompletedTask.TaskID)
	assert.Equal(t, "completed", at.CompletedTask.Status)
	assert.Equal(t, "Background command completed", at.CompletedTask.Summary)
}

// fakeBackgroundSubagent 复刻真实 CLI 2.1.185 抓到的「run_in_background 子 agent
// (Agent/Task 工具)」帧序(见 /tmp 抓帧):
//
//	turn1:主 agent 起 Agent(run_in_background:true)→ tool_result(异步启动,带
//	       output_file)→ text → result#1(本轮收尾、会话转空闲)。
//	空闲态:子 agent 的内部子对话实时流出 —— assistant/user 帧带 parent_tool_use_id
//	       =Agent 工具 tool_use_id(内部文本 / 内层 Bash 调用 / 内层 bash 完成通知
//	       (output_file 为空)/ 内层 tool_result),夹 task_progress / task_updated。
//	完成:  task_notification(后台型:有 output_file、无 subagent_type)→ 起自主续轮。
//	续轮:  init + assistant(主 agent 总结)+ result#2。
//	turn2: 普通回声。
//
// 关键缺陷(Phase 1 修复):空闲态第一帧子 agent 内部活动(parent_tool_use_id 的
// assistant 帧)既非后台型 task_notification、也不在 isNonTurnFrame 白名单 —— 旧逻辑
// 在 currentTurn 落到 <-pendingTurns 阻塞,冻住读循环;后续完成通知 / 自主续轮永远读
// 不到(与后台 bash 的 sess-429 同类,但触发源是后台 subagent 的内部活动)。
const fakeBgSubAgentTU = "toolu_agent" // Agent 工具 tool_use_id == subagent 卡片 key

func fakeBackgroundSubagent(stdin io.Reader, stdout io.Writer) {
	const sid = "sess-bgsubagent"
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	turn := 0
	for sc.Scan() {
		turn++
		reply := extractTextField(sc.Text())
		if turn == 1 {
			// turn1:启动后台 subagent,以 result#1 收尾(模型不等子任务结束本轮)。
			writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a1","content":[{"type":"tool_use","id":%q,"name":"Agent","input":{"subagent_type":"general-purpose","description":"explore","prompt":"go","run_in_background":true}}]}}`, fakeBgSubAgentTU)
			writeFrame(stdout, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":%q,"content":"Async agent launched successfully. output_file: /tmp/tasks/sub.output"}]}}`, fakeBgSubAgentTU)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a2","content":[{"type":"text","text":"started:%s"}]}}`, reply)
			writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
			// —— 不等下一条 stdin:子 agent 内部子对话在空闲态实时流出(parent_tool_use_id)——
			writeFrame(stdout, `{"type":"assistant","parent_tool_use_id":%q,"message":{"id":"s1","content":[{"type":"text","text":"subagent thinking"}]}}`, fakeBgSubAgentTU)
			writeFrame(stdout, `{"type":"assistant","parent_tool_use_id":%q,"message":{"id":"s2","content":[{"type":"tool_use","id":"sub_bash","name":"Bash","input":{"command":"sleep 6"}}]}}`, fakeBgSubAgentTU)
			writeFrame(stdout, `{"type":"system","subtype":"task_progress","task_id":"subtask","tool_use_id":%q,"subagent_type":"general-purpose"}`, fakeBgSubAgentTU)
			writeFrame(stdout, `{"type":"system","subtype":"task_started","task_id":"innerbash","task_type":"local_bash"}`)
			writeFrame(stdout, `{"type":"system","subtype":"task_notification","task_id":"innerbash","tool_use_id":"sub_bash","status":"completed","output_file":"","summary":"inner bash done"}`)
			writeFrame(stdout, `{"type":"user","parent_tool_use_id":%q,"message":{"content":[{"type":"tool_result","tool_use_id":"sub_bash","content":"SUBAGENT_DONE"}]}}`, fakeBgSubAgentTU)
			writeFrame(stdout, `{"type":"assistant","parent_tool_use_id":%q,"message":{"id":"s3","content":[{"type":"text","text":"subagent final"}]}}`, fakeBgSubAgentTU)
			writeFrame(stdout, `{"type":"system","subtype":"task_updated","task_id":"subtask","patch":{"status":"completed"},"session_id":%q}`, sid)
			// 后台型完成通知:有 output_file、无 subagent_type → isBackgroundTaskNotification=true。
			writeFrame(stdout, `{"type":"system","subtype":"task_notification","task_id":"subtask","tool_use_id":%q,"status":"completed","output_file":"/tmp/tasks/sub.output","summary":"Agent came to rest"}`, fakeBgSubAgentTU)
			// —— 自主续轮:主 agent 总结子 agent 结果 ——
			writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a3","content":[{"type":"text","text":"autonomous:subagent-summary"}]}}`)
			writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":2,"output_tokens":2}}`, sid)
			continue
		}
		// turn2:普通回声。
		writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
		writeFrame(stdout, `{"type":"assistant","message":{"id":"a4","content":[{"type":"text","text":"echo:%s"}]}}`, reply)
		writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
	}
}

// TestSession_BackgroundSubagentActivityTurn 锁定 Phase 2:后台 subagent 的空闲内部活动
// 经 SubagentActivity() 作为一轮独立事件流吐出(keyed by Agent tool_use_id),其后台完成
// 仍触发既有自主续轮(AutonomousTurns)。
func TestSession_BackgroundSubagentActivityTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := New(WithBinary("fake"), pipeSpawner(t, fakeBackgroundSubagent))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	ch1, err := sess.Turn(ctx, "alpha")
	require.NoError(t, err)
	assert.Equal(t, "started:alpha", drainText(t, ch1))

	// (a) subagent 活动轮:keyed by Agent tool_use_id,事件流含子 agent 内部文本/工具。
	var act *SubagentActivity
	select {
	case act = <-sess.SubagentActivity():
	case <-time.After(2 * time.Second):
		t.Fatal("expected a subagent activity turn within 2s")
	}
	require.NotNil(t, act)
	assert.Equal(t, fakeBgSubAgentTU, act.ToolUseID)
	var sawNestedText, sawNestedTool bool
	for ev := range act.Events {
		if ev.ParentToolUseID != fakeBgSubAgentTU {
			continue
		}
		if ev.Kind == EventTextDelta && ev.Text == "subagent thinking" {
			sawNestedText = true
		}
		if ev.Kind == EventPreToolUse {
			sawNestedTool = true
		}
	}
	assert.True(t, sawNestedText, "活动轮应含子 agent 内部文本帧")
	assert.True(t, sawNestedTool, "活动轮应含子 agent 内部工具调用帧")

	// (b) 后台完成仍触发既有自主续轮(总结)。
	var at *AutoTurn
	select {
	case at = <-sess.AutonomousTurns():
	case <-time.After(2 * time.Second):
		t.Fatal("expected autonomous summary turn within 2s")
	}
	require.NotNil(t, at)
	assert.Equal(t, "autonomous:subagent-summary", drainText(t, at.Events))
	require.NotNil(t, at.CompletedTask)
	assert.Equal(t, fakeBgSubAgentTU, at.CompletedTask.ToolUseID)

	// (c) turn2 无错位。
	ch2, err := sess.Turn(ctx, "beta")
	require.NoError(t, err)
	assert.Equal(t, "echo:beta", drainText(t, ch2))
}

const (
	fakeBgSubAgentA = "toolu_agent_a"
	fakeBgSubAgentB = "toolu_agent_b"
)

// fakeConcurrentBackgroundSubagents 复刻 sess-2275 抓到的「一轮里派两个 run_in_background
// subagent」帧序:两个子 agent 在空闲态**交替**吐自己的内部活动(parent_tool_use_id 一个是
// A 一个是 B),各自完成时再各发一帧后台型 task_notification。
func fakeConcurrentBackgroundSubagents(stdin io.Reader, stdout io.Writer) {
	const sid = "sess-bgsubagents"
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	turn := 0
	for sc.Scan() {
		turn++
		reply := extractTextField(sc.Text())
		if turn == 1 {
			writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a1","content":[{"type":"tool_use","id":%q,"name":"Agent","input":{"subagent_type":"general-purpose","description":"T7","prompt":"go","run_in_background":true}},{"type":"tool_use","id":%q,"name":"Agent","input":{"subagent_type":"general-purpose","description":"T10","prompt":"go","run_in_background":true}}]}}`, fakeBgSubAgentA, fakeBgSubAgentB)
			writeFrame(stdout, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":%q,"content":"Async agent launched. output_file: /tmp/tasks/a.output"},{"type":"tool_result","tool_use_id":%q,"content":"Async agent launched. output_file: /tmp/tasks/b.output"}]}}`, fakeBgSubAgentA, fakeBgSubAgentB)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a2","content":[{"type":"text","text":"started:%s"}]}}`, reply)
			writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
			// —— 空闲态:两个子 agent 交替产出内部活动 ——
			writeFrame(stdout, `{"type":"assistant","parent_tool_use_id":%q,"message":{"id":"s1","content":[{"type":"tool_use","id":"sub_a1","name":"Read","input":{}}]}}`, fakeBgSubAgentA)
			writeFrame(stdout, `{"type":"user","parent_tool_use_id":%q,"message":{"content":[{"type":"tool_result","tool_use_id":"sub_a1","content":"A1"}]}}`, fakeBgSubAgentA)
			writeFrame(stdout, `{"type":"assistant","parent_tool_use_id":%q,"message":{"id":"s2","content":[{"type":"tool_use","id":"sub_b1","name":"Bash","input":{}}]}}`, fakeBgSubAgentB)
			writeFrame(stdout, `{"type":"user","parent_tool_use_id":%q,"message":{"content":[{"type":"tool_result","tool_use_id":"sub_b1","content":"B1"}]}}`, fakeBgSubAgentB)
			writeFrame(stdout, `{"type":"assistant","parent_tool_use_id":%q,"message":{"id":"s3","content":[{"type":"tool_use","id":"sub_a2","name":"Edit","input":{}}]}}`, fakeBgSubAgentA)
			writeFrame(stdout, `{"type":"user","parent_tool_use_id":%q,"message":{"content":[{"type":"tool_result","tool_use_id":"sub_a2","content":"A2"}]}}`, fakeBgSubAgentA)
			// A 完成 → 自主续轮;随后 B 完成 → 再一轮。
			writeFrame(stdout, `{"type":"system","subtype":"task_notification","task_id":"ta","tool_use_id":%q,"status":"completed","output_file":"/tmp/tasks/a.output","summary":"A done"}`, fakeBgSubAgentA)
			writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a3","content":[{"type":"text","text":"autonomous:A"}]}}`)
			writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":2,"output_tokens":2}}`, sid)
			writeFrame(stdout, `{"type":"system","subtype":"task_notification","task_id":"tb","tool_use_id":%q,"status":"completed","output_file":"/tmp/tasks/b.output","summary":"B done"}`, fakeBgSubAgentB)
			writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a4","content":[{"type":"text","text":"autonomous:B"}]}}`)
			writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":2,"output_tokens":2}}`, sid)
			continue
		}
		writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
		writeFrame(stdout, `{"type":"assistant","message":{"id":"a5","content":[{"type":"text","text":"echo:%s"}]}}`, reply)
		writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
	}
}

// TestSession_ConcurrentBackgroundSubagentsSplitByOwner 锁定 sess-2275 的第二处缺陷:
// 同一轮派出的两个后台 subagent 在空闲态交替产出内部活动时,活动轮的单槽位(s.active)
// 被先到的那个 owner 占住,之后**两个** subagent 的帧全被喂进同一轮 —— 消费方
// (chat_svc.driveSubagentActivity)按 act.ToolUseID 过滤子块,另一个 subagent 这段时间的
// 内部活动在收尾时被整段丢弃,既不落库也进不了它自己那张派遣卡。
//
// 断言:每一轮活动流里的帧都只属于该轮的 ToolUseID,且两个 owner 都拿到过自己的活动轮。
func TestSession_ConcurrentBackgroundSubagentsSplitByOwner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := New(WithBinary("fake"), pipeSpawner(t, fakeConcurrentBackgroundSubagents))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	// 活动轮 / 自主轮都要有人 drain,否则 readLoop 投递时阻塞。
	type ownedTools struct {
		owner string
		tools []string
	}
	collected := make(chan ownedTools, 8)
	go func() {
		for act := range sess.SubagentActivity() {
			got := ownedTools{owner: act.ToolUseID}
			for ev := range act.Events {
				if ev.Kind == EventPreToolUse && ev.Tool != nil {
					got.tools = append(got.tools, ev.Tool.ID+"@"+ev.ParentToolUseID)
				}
			}
			collected <- got
		}
		close(collected)
	}()
	go func() {
		for at := range sess.AutonomousTurns() {
			for range at.Events { //nolint:revive // drain
			}
		}
	}()

	ch1, err := sess.Turn(ctx, "alpha")
	require.NoError(t, err)
	assert.Equal(t, "started:alpha", drainText(t, ch1))

	// 收齐三次内部工具调用(A 两次、B 一次)。A 的两次被 B 隔开 → A 会出现两轮活动流。
	byOwner := map[string][]string{}
	deadline := time.After(3 * time.Second)
	for len(byOwner[fakeBgSubAgentA])+len(byOwner[fakeBgSubAgentB]) < 3 {
		select {
		case got, ok := <-collected:
			if !ok {
				t.Fatal("subagent activity channel closed before both owners appeared")
			}
			for _, tool := range got.tools {
				assert.True(t, strings.HasSuffix(tool, "@"+got.owner),
					"活动轮 %s 里混进了别的 subagent 的帧: %s", got.owner, tool)
			}
			byOwner[got.owner] = append(byOwner[got.owner], got.tools...)
		case <-deadline:
			t.Fatalf("timed out waiting for both owners' activity turns, got %v", byOwner)
		}
	}

	assert.Contains(t, byOwner[fakeBgSubAgentA], "sub_a1@"+fakeBgSubAgentA)
	assert.Contains(t, byOwner[fakeBgSubAgentB], "sub_b1@"+fakeBgSubAgentB)
	assert.Contains(t, byOwner[fakeBgSubAgentA], "sub_a2@"+fakeBgSubAgentA)
}

// drainTextWithin 同 drainText,但带时限:轮的流被喂错时 ch 永不 close,不设时限会把
// 测试挂到 go test 的全局超时,失败信息也看不出是哪一轮饿死。
func drainTextWithin(t *testing.T, ch <-chan Event, d time.Duration) string {
	t.Helper()
	var b strings.Builder
	deadline := time.After(d)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return b.String()
			}
			if ev.Kind == EventTextDelta {
				b.WriteString(ev.Text)
			}
		case <-deadline:
			t.Fatalf("这一轮 %s 内没等到终帧(已收到文本 %q)", d, b.String())
			return b.String()
		}
	}
}

// fakeUserTurnDuringSubagentActivity 复刻 sess-2974 抓到的帧序:turn1 派了一个
// run_in_background subagent 后即 result 收尾,子 agent 在空闲态实时吐内部活动(此时
// 活动轮占住 s.active);**活动还开着的时候**用户发了新消息,CLI 为它起主线的
// init → assistant → result#2,这些帧的 parent_tool_use_id 全是 null。
//
// 主线轮结束后子 agent 继续吐活动,验证让位是干净切分而不是把子 agent 的流丢掉。
func fakeUserTurnDuringSubagentActivity(stdin io.Reader, stdout io.Writer) {
	const sid = "sess-userturn-during-activity"
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	turn := 0
	for sc.Scan() {
		turn++
		reply := extractTextField(sc.Text())
		if turn == 1 {
			writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a1","content":[{"type":"tool_use","id":%q,"name":"Agent","input":{"subagent_type":"general-purpose","description":"explore","prompt":"go","run_in_background":true}}]}}`, fakeBgSubAgentTU)
			writeFrame(stdout, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":%q,"content":"Async agent launched successfully. output_file: /tmp/tasks/sub.output"}]}}`, fakeBgSubAgentTU)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a2","content":[{"type":"text","text":"started:%s"}]}}`, reply)
			writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
			// —— 空闲态:子 agent 内部活动开一轮活动轮,并**保持开着**(不发让位帧)——
			writeFrame(stdout, `{"type":"assistant","parent_tool_use_id":%q,"message":{"id":"s1","content":[{"type":"text","text":"subagent thinking"}]}}`, fakeBgSubAgentTU)
			writeFrame(stdout, `{"type":"assistant","parent_tool_use_id":%q,"message":{"id":"s2","content":[{"type":"tool_use","id":"sub_bash","name":"Bash","input":{"command":"sleep 6"}}]}}`, fakeBgSubAgentTU)
			writeFrame(stdout, `{"type":"system","subtype":"task_progress","task_id":"subtask","tool_use_id":%q,"subagent_type":"general-purpose"}`, fakeBgSubAgentTU)
			continue // 回到 Scan 等用户的下一条消息:活动轮此刻仍是 s.active
		}
		// turn2:用户在活动轮开着时发来的消息。CLI 为它起主线一轮,帧不带 parent_tool_use_id。
		writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
		writeFrame(stdout, `{"type":"assistant","message":{"id":"a3","content":[{"type":"text","text":"echo:%s"}]}}`, reply)
		writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":2,"output_tokens":2}}`, sid)
		// 主线轮收尾后子 agent 还在跑,继续吐活动 → 应另开一轮活动轮。
		writeFrame(stdout, `{"type":"assistant","parent_tool_use_id":%q,"message":{"id":"s3","content":[{"type":"text","text":"subagent after"}]}}`, fakeBgSubAgentTU)
	}
}

// TestSession_UserTurnDuringSubagentActivityNotSwallowed 锁定 sess-2974:后台 subagent
// 的活动轮占住 s.active 期间,用户新发一轮 —— currentTurn 的让位规则只认「后台型完成
// 通知」和「另一个 subagent owner 的帧」,主线帧(parent_tool_use_id 为 null)不让位,
// 于是用户这一轮的 init / 回答 / result 全被喂进活动轮:
//   - 回答被消费方按 ParentToolCallID 过滤后整段丢弃(assistant 行永远空);
//   - result 收尾的是**活动轮**,用户那一轮的 activeTurn 永远留在 pendingTurns、ch 不
//     close → drainStream 永久阻塞 → 会话卡住,最终被 startup 看门狗误杀成
//     errStartupTimeout。
//
// 断言:(a) 用户轮拿到自己的文本并正常收尾;(b) 活动轮不含主线文本;(c) 主线轮之后
// 子 agent 的后续活动仍能另开一轮活动轮(让位是干净切分,不是丢流)。
func TestSession_UserTurnDuringSubagentActivityNotSwallowed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := New(WithBinary("fake"), pipeSpawner(t, fakeUserTurnDuringSubagentActivity))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	acts := make(chan *SubagentActivity, 4)
	go func() {
		defer close(acts)
		for act := range sess.SubagentActivity() {
			acts <- act
		}
	}()

	ch1, err := sess.Turn(ctx, "alpha")
	require.NoError(t, err)
	assert.Equal(t, "started:alpha", drainText(t, ch1))

	// 活动轮已开 = s.active 被它占住,此刻再发用户轮才是要复刻的竞态。
	var act1 *SubagentActivity
	select {
	case act1 = <-acts:
	case <-time.After(2 * time.Second):
		t.Fatal("2s 内没等到 subagent 活动轮")
	}
	require.NotNil(t, act1)
	assert.Equal(t, fakeBgSubAgentTU, act1.ToolUseID)

	act1Text := make(chan string, 1)
	go func() {
		var b strings.Builder
		for ev := range act1.Events {
			if ev.Kind == EventTextDelta {
				b.WriteString(ev.Text)
			}
		}
		act1Text <- b.String()
	}()

	ch2, err := sess.Turn(ctx, "beta")
	require.NoError(t, err)
	// (a) 修复前:主线帧全进活动轮,ch2 一帧不得、永不 close → 这里超时红。
	assert.Equal(t, "echo:beta", drainTextWithin(t, ch2, 3*time.Second))

	// (b) 活动轮只该有子 agent 自己的内部文本。
	select {
	case got := <-act1Text:
		assert.Contains(t, got, "subagent thinking")
		assert.NotContains(t, got, "echo:beta", "用户轮的回答不得被喂进 subagent 活动轮")
	case <-time.After(2 * time.Second):
		t.Fatal("2s 内活动轮没有收尾")
	}

	// (c) 让位后子 agent 的后续活动仍要另开一轮活动轮,不能整条流丢掉。
	select {
	case act2 := <-acts:
		require.NotNil(t, act2)
		assert.Equal(t, fakeBgSubAgentTU, act2.ToolUseID)
	case <-time.After(2 * time.Second):
		t.Fatal("2s 内没等到让位后的第二轮 subagent 活动")
	}
}

// TestSession_IdleBackgroundSubagentKeepsReaderAlive 锁定 Phase 1 缺陷:后台 subagent
// 的内部活动在空闲态(result#1 之后、无 user turn 在飞)实时流出时,读循环不得卡死。
//
//	(a) turn1 干净收尾,只含 "started:alpha",不串入空闲子 agent 帧;
//	(b) 后台 subagent 完成的自主续轮必须经 AutonomousTurns() 浮现,文本 =
//	    "autonomous:subagent-summary",CompletedTask 指向 Agent 工具 tool_use_id
//	    (= subagent 卡片 key,供 FlipSubagentStatus 跨轮翻成 completed)。读循环若卡死
//	    在第一帧空闲子 agent 内部帧上,这一轮永远到不了 autoCh —— 修复前会超时;
//	(c) turn2 无错位。
func TestSession_IdleBackgroundSubagentKeepsReaderAlive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := New(WithBinary("fake"), pipeSpawner(t, fakeBackgroundSubagent))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	// (a) turn1 干净收尾。
	ch1, err := sess.Turn(ctx, "alpha")
	require.NoError(t, err)
	got1 := drainText(t, ch1)
	assert.Equal(t, "started:alpha", got1)
	assert.NotContains(t, got1, "subagent", "turn1 不应吞掉空闲子 agent 内部帧")

	// (b) 后台 subagent 完成的自主续轮必须浮现(修复前:读循环卡死在空闲子 agent 内部帧)。
	var at *AutoTurn
	select {
	case at = <-sess.AutonomousTurns():
	case <-time.After(2 * time.Second):
		t.Fatal("后台 subagent 空闲内部活动卡死读循环:自主续轮从未到达 " +
			"(parent_tool_use_id 的 assistant/user 帧落入 <-pendingTurns 阻塞)")
	}
	require.NotNil(t, at)
	assert.Equal(t, "background_task", at.Trigger)
	assert.Equal(t, "autonomous:subagent-summary", drainText(t, at.Events))
	require.NotNil(t, at.CompletedTask)
	assert.Equal(t, fakeBgSubAgentTU, at.CompletedTask.ToolUseID,
		"CompletedTask 须指向 Agent 工具 tool_use_id 以翻转 subagent 卡片")

	// (c) turn2 无错位。
	ch2, err := sess.Turn(ctx, "beta")
	require.NoError(t, err)
	assert.Equal(t, "echo:beta", drainText(t, ch2))
}

// fakeIdleSessionLevelSystemFrames 复刻 CLI 2.1.216 的「后台任务空闲完成」帧序 ——
// 与 fakeBackgroundTasksChanged(2.1.205)的差别:2.1.216 在 result#1 之后、后台任务
// 状态帧之前,还会先吐若干**新增的**会话级 system 子类型:
//
//	result#1 → post_turn_summary → task_summary → hook_started → hook_response
//	→ session_state_changed → background_tasks_changed → task_updated
//	→ task_notification(后台型) → init → assistant → result#2
//
// 这几个新 subtype 都不在旧 isNonTurnFrame 白名单里(该白名单每次 CLI 升级都要手工
// 补名字:2.1.162 补 task_updated、2.1.205 补 background_tasks_changed),空闲到达时
// 会被当成 turn 起始帧卡死在 <-pendingTurns 上 —— 后面的 task_notification / 自主
// 续轮永远读不到,且该 session 的 control_request 回执也再无人 dispatch
// (sess-2014「会话还在跑但发不出去、也收不到新内容」)。
func fakeIdleSessionLevelSystemFrames(stdin io.Reader, stdout io.Writer) {
	const sid = "sess-idle-system-frames"
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	turn := 0
	for sc.Scan() {
		turn++
		reply := extractTextField(sc.Text())
		if turn == 1 {
			writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a1","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"sleep 18","run_in_background":true}}]}}`)
			writeFrame(stdout, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu1","content":"Command running in background with ID: bg1"}]}}`)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a2","content":[{"type":"text","text":"started:%s"}]}}`, reply)
			writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
			// —— 空闲:2.1.216 新增的会话级 system 帧,一帧都不该认领排队的 user Turn ——
			writeFrame(stdout, `{"type":"system","subtype":"post_turn_summary","detail":"","session_id":%q}`, sid)
			writeFrame(stdout, `{"type":"system","subtype":"task_summary","detail":"1 background task","session_id":%q}`, sid)
			writeFrame(stdout, `{"type":"system","subtype":"hook_started","hook_id":"h1","hook_name":"post-tool","hook_event":"PostToolUse","session_id":%q}`, sid)
			writeFrame(stdout, `{"type":"system","subtype":"hook_response","hook_id":"h1","session_id":%q}`, sid)
			writeFrame(stdout, `{"type":"system","subtype":"session_state_changed","state":"idle","session_id":%q}`, sid)
			// —— 已知的后台任务帧序(2.1.205 起)——
			writeFrame(stdout, `{"type":"system","subtype":"background_tasks_changed","tasks":[],"session_id":%q}`, sid)
			writeFrame(stdout, `{"type":"system","subtype":"task_updated","task_id":"bg1","patch":{"status":"completed"},"session_id":%q}`, sid)
			writeFrame(stdout, `{"type":"system","subtype":"task_notification","task_id":"bg1","tool_use_id":"tu1","status":"completed","output_file":"/tmp/tasks/bg1.output","summary":"Background command completed"}`)
			writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a3","content":[{"type":"text","text":"autonomous:listing"}]}}`)
			writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":2,"output_tokens":2}}`, sid)
			continue
		}
		writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
		writeFrame(stdout, `{"type":"assistant","message":{"id":"a4","content":[{"type":"text","text":"echo:%s"}]}}`, reply)
		writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
	}
}

// TestSession_IdleSessionLevelSystemFramesKeepReaderAlive 钉死 sess-2014:CLI 升级
// 新增的会话级 system 子类型(post_turn_summary / task_summary / hook_* /
// session_state_changed)空闲到达时,不得认领排队的 user Turn、不得卡死 readLoop。
//
// 这是同一个坑的第三次复发(sess-429 → sess-1535 → sess-2014),所以断言的是**类**
// 而不是某个具体 subtype:白名单已反转成 canStartUserTurn,任何非「轮内容帧」的
// system 子类型(含将来新增的)都必须走丢弃路径。
func TestSession_IdleSessionLevelSystemFramesKeepReaderAlive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := New(WithBinary("fake"), pipeSpawner(t, fakeIdleSessionLevelSystemFrames))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	// (a) Turn1 干净收尾,不吞自主续轮帧。
	ch1, err := sess.Turn(ctx, "alpha")
	require.NoError(t, err)
	got1 := drainText(t, ch1)
	assert.Equal(t, "started:alpha", got1)
	assert.NotContains(t, got1, "autonomous", "Turn1 不应吞掉自主续轮帧")

	// (b) 新增会话级 system 帧空闲到达不得卡死读循环:自主续轮必须仍能浮现。
	var at *AutoTurn
	select {
	case at = <-sess.AutonomousTurns():
	case <-time.After(2 * time.Second):
		t.Fatal("会话级 system 帧(post_turn_summary/task_summary/hook_*/session_state_changed)" +
			"空闲到达后读循环卡死:自主续轮从未到达(该帧落入 <-pendingTurns 阻塞)")
	}
	require.NotNil(t, at)
	assert.Equal(t, "background_task", at.Trigger)
	assert.Equal(t, "autonomous:listing", drainText(t, at.Events))

	// (c) Turn2 无错位 —— 读循环仍然活着,后续 user 轮能正常起。
	ch2, err := sess.Turn(ctx, "beta")
	require.NoError(t, err)
	assert.Equal(t, "echo:beta", drainText(t, ch2))
}

// fakeIdleInitAfterResult 复刻 sess-2187 现场抓到的帧序(CLI 2.1.220):一轮以 result
// 收尾、会话转空闲(没有任何排队的 user Turn)之后 15ms,子进程又自发推了一帧
// system{subtype:"init"} —— 会话 cwd 下的 skill 目录被后台 subagent 改动,CLI 重新
// 广播了一次会话初始化。随后后台 subagent 陆续完成,推 task_updated / task_notification。
//
// init 是 canStartUserTurn 白名单里的「轮内容帧」(它通常就是一轮的首帧),空闲到达时
// 会一路落到 <-pendingTurns 上永久阻塞:readLoop 死掉,后面所有后台完成帧都读不出来。
func fakeIdleInitAfterResult(stdin io.Reader, stdout io.Writer) {
	const sid = "sess-idle-init-after-result"
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	turn := 0
	for sc.Scan() {
		turn++
		reply := extractTextField(sc.Text())
		if turn == 1 {
			writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a1","content":[{"type":"tool_use","id":"tu1","name":"Task","input":{"description":"eval1","run_in_background":true}}]}}`)
			writeFrame(stdout, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu1","content":"Task running in background with ID: bgagent1"}]}}`)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a2","content":[{"type":"text","text":"started:%s"}]}}`, reply)
			writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
			// —— 空闲:result 之后子进程自发重播的会话初始化帧(sess-2187 现场)——
			writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
			// —— 后台 subagent 完成:必须仍能读到并起自主续轮 ——
			writeFrame(stdout, `{"type":"system","subtype":"task_updated","task_id":"bgagent1","patch":{"status":"completed"},"session_id":%q}`, sid)
			writeFrame(stdout, `{"type":"system","subtype":"task_notification","task_id":"bgagent1","tool_use_id":"tu1","status":"completed","output_file":"/tmp/tasks/bgagent1.output","summary":"eval1 done"}`)
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a3","content":[{"type":"text","text":"autonomous:graded"}]}}`)
			writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":2,"output_tokens":2}}`, sid)
			continue
		}
		writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
		writeFrame(stdout, `{"type":"assistant","message":{"id":"a4","content":[{"type":"text","text":"echo:%s"}]}}`, reply)
		writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
	}
}

// TestSession_IdleInitFrameAfterResultKeepsReaderAlive 钉死 sess-2187:**轮内容帧本身**
// 空闲到达也不得认领排队的 user Turn。
//
// 前四次复发(sess-429 / 1535 / 2014)全是 CLI 新增的会话级 system 子类型,靠把
// isNonTurnFrame 反转成 canStartUserTurn 白名单挡住了;这次漏的是白名单**内部**的
// system{subtype:"init"} —— 它确实是一轮的首帧,但 CLI 也会在空闲态自发重播它。
// 于是 readLoop 卡死在 <-pendingTurns 上:6 个后台 subagent 里 4 个在之后 10 分钟内
// 陆续完成,task_updated / task_notification 一帧都没被读出来 —— 前端 subagent 卡在
// 「运行中」,自主续轮永不浮现,对话框再无任何新内容。
//
// 所以断言的是**类**不是某个 subtype:空闲(无 Send 在途)时任何帧都只能被丢弃。
func TestSession_IdleInitFrameAfterResultKeepsReaderAlive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := New(WithBinary("fake"), pipeSpawner(t, fakeIdleInitAfterResult))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	// (a) Turn1 干净收尾,不吞后面的空闲帧。
	ch1, err := sess.Turn(ctx, "alpha")
	require.NoError(t, err)
	got1 := drainText(t, ch1)
	assert.Equal(t, "started:alpha", got1)
	assert.NotContains(t, got1, "autonomous", "Turn1 不应吞掉自主续轮帧")

	// (b) 空闲态自发重播的 init 不得卡死读循环:后台 subagent 完成的自主续轮必须仍能浮现。
	var at *AutoTurn
	select {
	case at = <-sess.AutonomousTurns():
	case <-time.After(2 * time.Second):
		t.Fatal("result 之后空闲到达的 system{subtype:\"init\"} 卡死了读循环:" +
			"后台 subagent 完成的自主续轮从未到达(该帧落入 <-pendingTurns 阻塞)")
	}
	require.NotNil(t, at)
	assert.Equal(t, "background_task", at.Trigger)
	require.NotNil(t, at.CompletedTask)
	assert.Equal(t, "completed", at.CompletedTask.Status)
	assert.Equal(t, "autonomous:graded", drainText(t, at.Events))

	// (c) Turn2 无错位 —— 读循环仍然活着,后续 user 轮能正常起。
	ch2, err := sess.Turn(ctx, "beta")
	require.NoError(t, err)
	assert.Equal(t, "echo:beta", drainText(t, ch2))
}

// fakeDiesOnControlRequest 模拟「子进程收到 control_request 后直接死掉、不回
// control_response」:普通 user 轮正常回声;一旦从 stdin 读到 control_request 就
// 返回 —— stdout 随之 EOF,readLoop 收尾。
func fakeDiesOnControlRequest(stdin io.Reader, stdout io.Writer) {
	const sid = "sess-dies-on-control"
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, `"control_request"`) {
			return // 子进程崩了:回执永远不会来
		}
		reply := extractTextField(line)
		writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
		writeFrame(stdout, `{"type":"assistant","message":{"id":"a1","content":[{"type":"text","text":"echo:%s"}]}}`, reply)
		writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
	}
}

// TestSession_ControlRequestUnblocksWhenReaderDies 钉死「等 control_response 的调用方
// 必须随 readLoop 收尾一起被打醒」:子进程死掉后再没有人 dispatch 回执,若只等
// ctx / ch,调用链就永久静默挂起 —— 这正是 sess-2014 里 SetPermissionMode 卡住、
// Send 的 Wails RPC 不返回、前端连报错都弹不出来的那条路径。
//
// 修复前:shutdownReader 只收尾 active / pendingTurns,不碰 ctrlPending,本测试超时。
func TestSession_ControlRequestUnblocksWhenReaderDies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c := New(WithBinary("fake"), pipeSpawner(t, fakeDiesOnControlRequest, withExitCode(1)))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	// 先跑一轮,确认会话本来是健康的。
	ch, err := sess.Turn(ctx, "alpha")
	require.NoError(t, err)
	assert.Equal(t, "echo:alpha", drainText(t, ch))

	done := make(chan error, 1)
	go func() { done <- sess.SetPermissionMode(ctx, "plan") }()

	select {
	case err := <-done:
		require.Error(t, err, "子进程已退出,SetPermissionMode 必须带错误返回而不是静默成功")
		assert.Contains(t, err.Error(), "set_permission_mode",
			"错误应指明是哪个 control_request 被中断,便于排查")
	case <-time.After(2 * time.Second):
		t.Fatal("子进程已退出、readLoop 已收尾,SetPermissionMode 仍在等 control_response —— " +
			"调用方永久挂起(ctrlPending 没被 shutdownReader 打醒)")
	}
}

// TestCanStartUserTurn 锁死反转后的归属白名单:只有「轮内容帧」有资格认领排队的
// user Turn,其余一律不认领(未知帧默认丢弃,而不是把 readLoop 卡死)。
func TestCanStartUserTurn(t *testing.T) {
	parse := func(s string) rawFrame {
		var f rawFrame
		if err := json.Unmarshal([]byte(s), &f); err != nil {
			t.Fatalf("bad fixture: %v", err)
		}
		return f
	}

	cases := []struct {
		name string
		line string
		want bool
	}{
		{"assistant 帧", `{"type":"assistant","message":{"id":"a1","content":[]}}`, true},
		{"user 帧", `{"type":"user","message":{"content":[]}}`, true},
		{"result 帧", `{"type":"result","subtype":"success"}`, true},
		{"stream_event 帧", `{"type":"stream_event","event":{"type":"message_start"}}`, true},
		{"system init 帧", `{"type":"system","subtype":"init","model":"m"}`, true},
		{"system compact_boundary 帧", `{"type":"system","subtype":"compact_boundary"}`, true},

		{"system status 帧", `{"type":"system","subtype":"status","status":"compacting"}`, false},
		{"system task_started", `{"type":"system","subtype":"task_started","task_id":"bg1"}`, false},
		{"system task_updated", `{"type":"system","subtype":"task_updated","task_id":"bg1"}`, false},
		{"system task_progress", `{"type":"system","subtype":"task_progress","task_id":"bg1"}`, false},
		{"system background_tasks_changed", `{"type":"system","subtype":"background_tasks_changed"}`, false},
		{"system task_notification", `{"type":"system","subtype":"task_notification","task_id":"bg1"}`, false},
		// —— 2.1.216 新增,以及一切将来新增的 system 子类型 ——
		{"system post_turn_summary", `{"type":"system","subtype":"post_turn_summary"}`, false},
		{"system task_summary", `{"type":"system","subtype":"task_summary"}`, false},
		{"system hook_started", `{"type":"system","subtype":"hook_started","hook_id":"h1"}`, false},
		{"system session_state_changed", `{"type":"system","subtype":"session_state_changed"}`, false},
		{"system 未知子类型", `{"type":"system","subtype":"brand_new_subtype_from_the_future"}`, false},
		{"control_response", `{"type":"control_response","response":{"request_id":"r1"}}`, false},
		{"未知顶层类型", `{"type":"queue-operation","operation":"enqueue"}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, canStartUserTurn(parse(c.line)))
		})
	}
}

// TestSession_APIErrorMessageFrameBecomesEventError 回归 sess-2153:CLI 2.1.216 在
// API 连接中途断开时,吐一个 model:"<synthetic>" 的合成 assistant 帧,顶层带
// isApiErrorMessage:true + error:"server_error",content 是
// "API Error: Connection closed mid-response. The response above may be incomplete."。
// 旧逻辑把它当 EventTextDelta 拼进上一段真实输出的正文 block(前端就混着渲染);
// 现在必须翻成 EventError,让上层落 error_text / 独立 ErrorCard。
func TestSession_APIErrorMessageFrameBecomesEventError(t *testing.T) {
	const line = `{"type":"assistant","message":{"id":"m1","model":"<synthetic>","role":"assistant","stop_reason":"stop_sequence","content":[{"type":"text","text":"API Error: Connection closed mid-response. The response above may be incomplete."}]},"error":"server_error","isApiErrorMessage":true,"session_id":"sess-xyz"}`

	s := &Session{}
	events, isResult := s.parseLine([]byte(line))

	require.Len(t, events, 1)
	ev := events[0]
	assert.Equal(t, EventError, ev.Kind)
	require.Error(t, ev.Err)
	assert.ErrorIs(t, ev.Err, ErrAPIError)
	assert.Contains(t, ev.Err.Error(), "Connection closed mid-response")

	var apiErr *APIError
	require.ErrorAs(t, ev.Err, &apiErr)
	assert.Equal(t, "server_error", apiErr.Code)

	// 不抢终结:真正的 turn 结束仍由随后的 result(EventDone)帧驱动,避免 CLI 若补发
	// result 造成 double-terminate。
	assert.False(t, isResult)

	// 关键回归:这句提示不能作为文本增量泄漏进正文。
	for _, e := range events {
		assert.NotEqual(t, EventTextDelta, e.Kind, "API 错误帧不应产出 EventTextDelta")
	}
}

// TestSession_NormalAssistantFrameStillEmitsText 防过度拦截:没有 isApiErrorMessage
// 标记的普通 assistant 帧照常出 EventTextDelta。
func TestSession_NormalAssistantFrameStillEmitsText(t *testing.T) {
	const line = `{"type":"assistant","message":{"id":"m2","model":"claude-opus-4-8","role":"assistant","content":[{"type":"text","text":"hello world"}]},"session_id":"sess-xyz"}`

	s := &Session{}
	events, isResult := s.parseLine([]byte(line))

	require.NotEmpty(t, events)
	assert.Equal(t, EventTextDelta, events[0].Kind)
	assert.Equal(t, "hello world", events[0].Text)
	assert.False(t, isResult)
}

// TestSession_SubagentModelEvent 验证 Session.parseLine 这条解码路径（与
// frameDecoder.decodeLine 共用 parseAssistantContentWithUsage，见 stream_test.go
// TestStream_SubagentModelEvent）同样验证：subagent 内部帧（parent_tool_use_id
// 非空）携带非空 message.model 时产出 EventSubagentModel；主 agent 自己的帧即便带
// model 也不产出。
func TestSession_SubagentModelEvent(t *testing.T) {
	const line = `{"type":"assistant","message":{"id":"s1","model":"claude-haiku-4-5-20251001","content":[{"type":"tool_use","id":"sub-tu","name":"Bash","input":{"command":"echo hi"}}]},"parent_tool_use_id":"toolu-parent","session_id":"sess-xyz"}`

	s := &Session{}
	events, isResult := s.parseLine([]byte(line))

	require.NotEmpty(t, events)
	var modelEvents []Event
	for _, e := range events {
		if e.Kind == EventSubagentModel {
			modelEvents = append(modelEvents, e)
		}
	}
	require.Len(t, modelEvents, 1)
	assert.Equal(t, "toolu-parent", modelEvents[0].ParentToolUseID)
	assert.Equal(t, "claude-haiku-4-5-20251001", modelEvents[0].Model)
	assert.False(t, isResult)
}

// TestSession_MainAgentFrameModelDoesNotEmitSubagentModel 是隔离守卫：主 agent
// 自己的帧（parent_tool_use_id 为空）即便带 message.model，也绝不能被误判成 subagent
// 内部帧而产出 EventSubagentModel——那会污染主 agent 的模型展示。
func TestSession_MainAgentFrameModelDoesNotEmitSubagentModel(t *testing.T) {
	const line = `{"type":"assistant","message":{"id":"m1","model":"claude-opus-5","content":[{"type":"tool_use","id":"toolu-parent","name":"Agent","input":{"description":"probe"}}]},"session_id":"sess-xyz"}`

	s := &Session{}
	events, isResult := s.parseLine([]byte(line))

	require.NotEmpty(t, events)
	for _, e := range events {
		assert.NotEqual(t, EventSubagentModel, e.Kind, "main agent frame must never produce EventSubagentModel")
	}
	assert.False(t, isResult)
}

// TestSession_SubagentFrameMissingModelDoesNotEmit 老 CLI 兼容：subagent 内部帧没有
// message.model 字段时不产出 EventSubagentModel，正常路径（如 EventTextDelta）不受影响。
func TestSession_SubagentFrameMissingModelDoesNotEmit(t *testing.T) {
	const line = `{"type":"assistant","message":{"id":"s2","content":[{"type":"text","text":"legacy subagent text"}]},"parent_tool_use_id":"toolu-parent","session_id":"sess-xyz"}`

	s := &Session{}
	events, isResult := s.parseLine([]byte(line))

	require.NotEmpty(t, events)
	for _, e := range events {
		assert.NotEqual(t, EventSubagentModel, e.Kind, "subagent frame without message.model must not produce EventSubagentModel")
	}
	assert.False(t, isResult)
}

// TestSession_SubagentModelEvent_SyntheticSentinelNotEmitted 覆盖 wrap-up 复审第二轮
// Finding 3:CLI 在 API 错误帧上用 model:"<synthetic>"(见 errors.go 顶部注释)。若一帧
// isApiErrorMessage:true 的帧其首个 text 块与顶层 error 都为空,apiErrorEvent 会返回
// ok=false 并落入 parseAssistantContentWithUsage;该帧若带 parent_tool_use_id,当前会
// 产出 EventSubagentModel{Model:"<synthetic>"} —— first-wins 会把这个哨兵永久钉进
// subagent_state.model 与数据库,徽标显示 "<synthetic>"。生产侧必须过滤掉这个已知哨兵值。
func TestSession_SubagentModelEvent_SyntheticSentinelNotEmitted(t *testing.T) {
	const line = `{"type":"assistant","message":{"id":"m1","model":"<synthetic>","content":[]},"parent_tool_use_id":"toolu-parent","isApiErrorMessage":true,"session_id":"sess-xyz"}`

	s := &Session{}
	events, isResult := s.parseLine([]byte(line))

	for _, e := range events {
		assert.NotEqual(t, EventSubagentModel, e.Kind, "CLI synthetic API-error sentinel model must never surface as EventSubagentModel")
	}
	assert.False(t, isResult)
}

// TestSession_SubagentModelEvent_APIErrorFlagGovernsRegardlessOfSentinelString 覆盖
// 判定必须以帧的权威标志 isApiErrorMessage 为准，不能靠嗅探
// message.model 的字符串值是否等于当前已知的占位符 "<synthetic>"。本测试用一个
// 不同于该字面量的占位符值,模拟 CLI 未来改动占位符取值——isApiErrorMessage:true
// 且首个 text 块与顶层 error 都为空,apiErrorEvent 因而放行,帧落进
// parseAssistantContentWithUsage。若判定仍靠字符串嗅探,这个陌生占位符值会被当成
// subagent 的实际模型产出 EventSubagentModel,first-wins 语义下永久钉进
// subagent_state.model。
func TestSession_SubagentModelEvent_APIErrorFlagGovernsRegardlessOfSentinelString(t *testing.T) {
	const line = `{"type":"assistant","message":{"id":"m1","model":"<future-placeholder>","content":[]},"parent_tool_use_id":"toolu-parent","isApiErrorMessage":true,"session_id":"sess-xyz"}`

	s := &Session{}
	events, isResult := s.parseLine([]byte(line))

	for _, e := range events {
		assert.NotEqual(t, EventSubagentModel, e.Kind, "isApiErrorMessage:true frame must never surface a model, regardless of the placeholder string value")
	}
	assert.False(t, isResult)
}

// TestSession_RawSinkReceivesFramesFromReadLoop 校验生产多轮路径(Session.readLoop)
// 也把每帧原始 stdout 喂给 rawSink,而不仅是一次性 Stream 路径。
func TestSession_RawSinkReceivesFramesFromReadLoop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var mu sync.Mutex
	var got []string
	c := New(WithBinary("fake"), pipeSpawner(t, fakePersistent), WithRawSink(func(b []byte) {
		mu.Lock()
		got = append(got, string(b))
		mu.Unlock()
	}))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close(ctx) })

	ch, err := sess.Turn(ctx, "hello")
	require.NoError(t, err)
	for range ch { //nolint:revive // 只为把这一轮 drain 完
	}

	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(got, "\n")
	assert.Contains(t, joined, `"subtype":"init"`)
	assert.Contains(t, joined, `"type":"assistant"`)
	assert.Contains(t, joined, `"type":"result"`)
}

// fakeBgSubAgentTU2 是第二个并发 run_in_background subagent 的 Agent tool_use_id。
const fakeBgSubAgentTU2 = "toolu_agent2"

// fakeInterleavedMainThreadAndSubagents 复刻 sess-2980 抓到的帧序。与 sess-2974
// (fakeUserTurnDuringSubagentActivity)的决定性差异是**交错**:那一版里用户轮的主线帧
// 是连成一段的(init → assistant → result),中间没有 subagent 帧插进来;真实现场里
// turn1 派了多个 run_in_background subagent,它们在用户轮进行**期间**持续吐内部活动,
// 于是帧流长这样:
//
//	init(主线) → subagent A 帧 → 主线 thinking/tool_use → subagent A 帧 →
//	主线 tool_use → subagent B 帧 → ... → result(主线)
//
// 现场表现:用户那一轮的 assistant 消息落库是空 []，CLI 明明答了 60 多块主线内容。
func fakeInterleavedMainThreadAndSubagents(stdin io.Reader, stdout io.Writer) {
	const sid = "sess-interleave"
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	turn := 0
	for sc.Scan() {
		turn++
		reply := extractTextField(sc.Text())
		if turn == 1 {
			writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
			for _, tu := range []string{fakeBgSubAgentTU, fakeBgSubAgentTU2} {
				writeFrame(stdout, `{"type":"assistant","message":{"id":"a1","content":[{"type":"tool_use","id":%q,"name":"Agent","input":{"subagent_type":"general-purpose","description":"explore","prompt":"go","run_in_background":true}}]}}`, tu)
				writeFrame(stdout, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":%q,"content":"Async agent launched successfully. output_file: /tmp/tasks/sub.output"}]}}`, tu)
			}
			writeFrame(stdout, `{"type":"assistant","message":{"id":"a2","content":[{"type":"text","text":"started:%s"}]}}`, reply)
			writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
			// 空闲态:subagent A 开一轮活动轮并保持开着 —— 用户下一条消息就撞在这上面。
			writeFrame(stdout, `{"type":"assistant","parent_tool_use_id":%q,"message":{"id":"s1","content":[{"type":"text","text":"A1"}]}}`, fakeBgSubAgentTU)
			continue
		}
		// turn2:用户在活动轮开着时发的消息。CLI 照常为它起主线一轮 —— 但两个后台
		// subagent 还在跑,它们的帧与主线帧**交错**到达。
		writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
		writeFrame(stdout, `{"type":"assistant","parent_tool_use_id":%q,"message":{"id":"s2","content":[{"type":"text","text":"A2"}]}}`, fakeBgSubAgentTU)
		writeFrame(stdout, `{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"echo:%s"}]}}`, reply)
		writeFrame(stdout, `{"type":"assistant","parent_tool_use_id":%q,"message":{"id":"s3","content":[{"type":"text","text":"B1"}]}}`, fakeBgSubAgentTU2)
		writeFrame(stdout, `{"type":"assistant","message":{"id":"m2","content":[{"type":"text","text":"-tail"}]}}`)
		writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":2,"output_tokens":2}}`, sid)
	}
}

// TestSession_MainThreadFramesInterleavedWithSubagentActivity 锁定 sess-2980:后台
// subagent 的活动帧与用户轮的主线帧交错到达时,用户轮必须完整拿到自己的主线正文。
//
// 现场:s.active 是**单槽位**,currentTurn 靠让位在「活动轮」与「用户轮」之间切换。
// 主线帧让位认领用户轮之后,紧接着到达的 subagent 帧因为让位前置条件
// (s.active.subagentToolUseID != "")为假而不让位 —— 它被喂进用户轮,而用户轮这边
// 按 ParentToolCallID 找不到归属直接丢弃;更要命的是活动轮/用户轮反复切换的过程中
// 用户轮被提前收尾,后续主线帧再也回不到它,整轮正文落库成空 []。
func TestSession_MainThreadFramesInterleavedWithSubagentActivity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := New(WithBinary("fake"), pipeSpawner(t, fakeInterleavedMainThreadAndSubagents))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	go func() {
		for act := range sess.SubagentActivity() {
			go func(a *SubagentActivity) {
				for range a.Events { //nolint:revive // 只需 drain,不断言内容
				}
			}(act)
		}
	}()

	ch1, err := sess.Turn(ctx, "alpha")
	require.NoError(t, err)
	assert.Equal(t, "started:alpha", drainText(t, ch1))

	ch2, err := sess.Turn(ctx, "beta")
	require.NoError(t, err)
	// 修复前:主线正文被交错的 subagent 帧挤掉,这里拿不到完整文本。
	assert.Equal(t, "echo:beta-tail", drainTextWithin(t, ch2, 3*time.Second))
}

const fakeFgSubAgentTU = "toolu_fg_agent"

// fakeForegroundSubagent 复刻 sess-3090:主线轮里派 run_in_background:false 的 Agent,
// 子 agent 的内部帧(parent_tool_use_id=Agent tool_use_id)在 Agent 自己的 tool_result
// 之前就到达。与后台 subagent(先 tool_result「异步启动」,再在空闲/下一轮吐内部活动)
// 不同 —— 前台子步骤必须留在当前主线轮,否则 chat_svc.driveSubagentActivity 找不到
// 尚未落库的发起消息,会把子工具 drain 丢掉,卡片只剩「无子步骤」。
func fakeForegroundSubagent(stdin io.Reader, stdout io.Writer) {
	const sid = "sess-fgsubagent"
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	for sc.Scan() {
		reply := extractTextField(sc.Text())
		writeFrame(stdout, `{"type":"system","subtype":"init","session_id":%q,"cwd":"/tmp","model":"m","tools":[]}`, sid)
		writeFrame(stdout, `{"type":"assistant","message":{"id":"a1","content":[{"type":"tool_use","id":%q,"name":"Agent","input":{"subagent_type":"general-purpose","description":"spec-axis","prompt":"verify","run_in_background":false}}]}}`, fakeFgSubAgentTU)
		writeFrame(stdout, `{"type":"system","subtype":"task_started","task_id":"fg1","tool_use_id":%q,"description":"spec-axis","subagent_type":"general-purpose","task_type":"local_agent"}`, fakeFgSubAgentTU)
		// 现场第一帧子活动是带 parent 的 user prompt,随后才是内部 Read/Bash。
		writeFrame(stdout, `{"type":"user","parent_tool_use_id":%q,"message":{"content":[{"type":"text","text":"verify the spec"}]}}`, fakeFgSubAgentTU)
		writeFrame(stdout, `{"type":"assistant","parent_tool_use_id":%q,"message":{"id":"s1","content":[{"type":"tool_use","id":"sub_read","name":"Read","input":{"file_path":"SPEC.md"}}]}}`, fakeFgSubAgentTU)
		writeFrame(stdout, `{"type":"user","parent_tool_use_id":%q,"message":{"content":[{"type":"tool_result","tool_use_id":"sub_read","content":"# spec"}]}}`, fakeFgSubAgentTU)
		writeFrame(stdout, `{"type":"assistant","parent_tool_use_id":%q,"message":{"id":"s2","content":[{"type":"tool_use","id":"sub_bash","name":"Bash","input":{"command":"git log"}}]}}`, fakeFgSubAgentTU)
		writeFrame(stdout, `{"type":"user","parent_tool_use_id":%q,"message":{"content":[{"type":"tool_result","tool_use_id":"sub_bash","content":"abc123"}]}}`, fakeFgSubAgentTU)
		writeFrame(stdout, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":%q,"content":"status: complete"}]}}`, fakeFgSubAgentTU)
		writeFrame(stdout, `{"type":"assistant","message":{"id":"a2","content":[{"type":"text","text":"done:%s"}]}}`, reply)
		writeFrame(stdout, `{"type":"result","subtype":"success","session_id":%q,"usage":{"input_tokens":1,"output_tokens":1}}`, sid)
	}
}

// TestSession_ForegroundSubagentNestedFramesStayOnMainTurn 锁定 sess-3090:
// 前台 Agent 的内部工具帧必须出现在发起它的那条主线轮上,且不得另开 SubagentActivity。
func TestSession_ForegroundSubagentNestedFramesStayOnMainTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := New(WithBinary("fake"), pipeSpawner(t, fakeForegroundSubagent))
	sess, err := c.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	ch, err := sess.Turn(ctx, "alpha")
	require.NoError(t, err)

	var text strings.Builder
	var nested []string
	var sawOuterAgent bool
	for ev := range ch {
		if ev.Kind == EventTextDelta {
			text.WriteString(ev.Text)
		}
		if ev.Kind != EventPreToolUse || ev.Tool == nil {
			continue
		}
		if ev.ParentToolUseID == "" && ev.Tool.ID == fakeFgSubAgentTU {
			sawOuterAgent = true
			continue
		}
		if ev.ParentToolUseID == fakeFgSubAgentTU {
			nested = append(nested, ev.Tool.ID)
		}
	}
	assert.Equal(t, "done:alpha", text.String())
	assert.True(t, sawOuterAgent, "主线轮应含外层 Agent tool_use")
	assert.Equal(t, []string{"sub_read", "sub_bash"}, nested,
		"前台 subagent 的内部工具必须留在主线轮,不能被旁路进 SubagentActivity")

	select {
	case act, ok := <-sess.SubagentActivity():
		if ok {
			t.Fatalf("前台 subagent 不得另开 SubagentActivity, got ToolUseID=%s", act.ToolUseID)
		}
	default:
	}
}

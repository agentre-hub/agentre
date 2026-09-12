package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/pkg/claudecode"
)

// TestHandleControlRequest_BypassExitPlanMode 回归「bypass 快照吞掉计划审批」:
// active.permissionMode 还停留在 bypassPermissions(快照失同步,或会话真在 bypass)
// 时,ExitPlanMode 的 control_request 被短路自动 allow,前端永远看不到计划审批卡,
// 然后 CLI 自切 mode(实测 acceptEdits)—— 全程没有用户审批。
//
// 约定:计划审批是流程门禁不是工具权限,bypassPermissions 短路对 ExitPlanMode
// 永远不生效 —— 必须注册 permWaiter 并 emit ToolPermissionRequest 走审批卡。
func TestHandleControlRequest_BypassExitPlanMode(t *testing.T) {
	Convey("Given 快照=bypassPermissions, When ExitPlanMode control_request 到达, Then 不自动放行而是发起审批", t, func() {
		active := &claudeActive{handle: &fakeCCHandle{}, permissionMode: "bypassPermissions"}
		out := make(chan agentruntime.Event, 1)

		handleControlRequest(&claudecode.ControlRequestEvent{
			RequestID: "req-plan-1",
			ToolName:  "ExitPlanMode",
			Input:     json.RawMessage(`{"plan":"# Plan\n1. do it"}`),
		}, active, out)

		So(active.takePermWaiter("req-plan-1"), ShouldNotBeNil)
		var got agentruntime.Event
		select {
		case got = <-out:
		default:
		}
		perm, ok := got.(agentruntime.ToolPermissionRequest)
		So(ok, ShouldBeTrue)
		So(perm.RequestID, ShouldEqual, "req-plan-1")
		So(perm.ToolName, ShouldEqual, "ExitPlanMode")
	})

	Convey("Given 快照=bypassPermissions, When 普通工具 control_request 到达, Then 仍走短路自动 allow", t, func() {
		responded := make(chan claudecode.PermissionResult, 1)
		active := &claudeActive{handle: &fakeCCHandle{respondedResults: responded}, permissionMode: "bypassPermissions"}
		out := make(chan agentruntime.Event, 1)

		handleControlRequest(&claudecode.ControlRequestEvent{
			RequestID: "req-bash-1",
			ToolName:  "Bash",
			Input:     json.RawMessage(`{"command":"ls"}`),
		}, active, out)

		var res claudecode.PermissionResult
		select {
		case res = <-responded:
		case <-time.After(2 * time.Second):
			t.Fatal("auto-allow RespondToControl not called within 2s")
		}
		So(res.Behavior, ShouldEqual, "allow")
		So(active.takePermWaiter("req-bash-1"), ShouldBeNil)
		So(len(out), ShouldEqual, 0)
	})
}

// TestSetPermissionMode_SyncsActiveSnapshot 回归「快照失同步」根因:
// Runtime.SetPermissionMode 把切换发给 CLI 后不更新 active.permissionMode,
// bypass 会话轮间切到 plan 后快照仍是 bypassPermissions(CLI 的空闲 status 回显帧
// 被 demux reader 丢弃,复用进程的下一轮也不重发 mode),于是 ExitPlanMode /
// 工具审批照旧被 bypass 短路吞掉。
func TestSetPermissionMode_SyncsActiveSnapshot(t *testing.T) {
	Convey("Given 缓存的 claudeActive 快照=bypassPermissions, When SetPermissionMode(plan) 成功, Then 快照同步为 plan", t, func() {
		r := New()
		a := &claudeActive{handle: &fakeCCHandle{}, permissionMode: "bypassPermissions"}
		r.cache.Put(sessionKey(7), a)

		err := r.SetPermissionMode(context.Background(), 7, "plan")

		So(err, ShouldBeNil)
		So(a.permissionModeSnapshot(), ShouldEqual, "plan")
	})

	Convey("Given CLI 拒绝切 mode, When SetPermissionMode 失败, Then 快照保持原值", t, func() {
		r := New()
		a := &claudeActive{
			handle:         &fakeCCHandle{setPermissionModeErr: context.DeadlineExceeded},
			permissionMode: "bypassPermissions",
		}
		r.cache.Put(sessionKey(8), a)

		err := r.SetPermissionMode(context.Background(), 8, "plan")

		So(err, ShouldNotBeNil)
		So(a.permissionModeSnapshot(), ShouldEqual, "bypassPermissions")
	})
}

// startTurnDrain 起一条 drainStream(等价一轮 turn 的帧流),返回喂帧 channel、
// 本轮事件出口和「关掉帧流并等 drain 退出」的收尾函数。自主续轮 / 后台 subagent
// 活动轮与 user turn 的唯一差别就是它们不调 setOut —— 这里一律不调,复现带外轮。
func startTurnDrain(a *claudeActive) (chan claudecode.Event, chan agentruntime.Event, func()) {
	frames := make(chan claudecode.Event, 4)
	evOut := make(chan agentruntime.Event, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		drainStream(&ccChanStream{ch: frames}, evOut, &agentruntime.RunResult{}, a, nil)
		close(evOut)
	}()
	return frames, evOut, func() {
		close(frames)
		<-done
	}
}

func recvEvent(t *testing.T, ch <-chan agentruntime.Event, what string) agentruntime.Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatalf("%s: 通道已关闭", what)
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: 2s 内没等到事件", what)
		return nil
	}
}

// TestSubmitDecision_ResolvedEventFollowsRequestChannel 回归 sess-2425
// 「已经审批完成了，还是显示审批状态」:
//
// 自主续轮 / 后台 subagent 活动轮刻意不 setOut(避免与 user turn 抢写 a.out),
// 于是终态事件走 a.outChan() 时落到 nil / 上一轮已结束的陈旧 channel 被丢弃。
// chat_svc 的 UserAskResolvedHandler / ToolPermissionResolvedHandler 因此永远收不到,
// markSessionRunning 不执行,chat_sessions.agent_status 卡在 "waiting" ——
// 侧栏「审批」胶囊要一直挂到本轮 finalize(带外轮可以跑几小时)。
//
// 约定:终态事件回到 **发起该 control_request 的那条事件通道**,而不是 session
// 全局的 a.out —— 请求从哪条通道来,应答就回哪条通道。
func TestSubmitDecision_ResolvedEventFollowsRequestChannel(t *testing.T) {
	Convey("Given AskUserQuestion 在带外轮通道上发起(全程不 setOut), When SubmitAnswer 成功, Then UserAskResolved 回到该轮通道", t, func() {
		r := New()
		a := &claudeActive{handle: &fakeCCHandle{}}
		r.cache.Put(sessionKey(2425), a)
		frames, evOut, stop := startTurnDrain(a)
		defer stop()

		frames <- claudecode.Event{Kind: claudecode.EventControlRequest, ControlRequest: &claudecode.ControlRequestEvent{
			RequestID: "req-ask-1",
			ToolName:  "AskUserQuestion",
			Input:     json.RawMessage(`{"questions":[{"question":"什么时候修？","header":"修复时机","options":[{"label":"现在修"}]}]}`),
		}}
		ask, ok := recvEvent(t, evOut, "UserAskRequest").(agentruntime.UserAskRequest)
		So(ok, ShouldBeTrue)
		So(ask.RequestID, ShouldEqual, "req-ask-1")

		err := r.SubmitAnswer(context.Background(), 2425, "req-ask-1", nil,
			[]agentruntime.AskAnswer{{QuestionIndex: 0, Labels: []string{"现在修"}}}, false)
		So(err, ShouldBeNil)

		resolved, ok := recvEvent(t, evOut, "UserAskResolved").(agentruntime.UserAskResolved)
		So(ok, ShouldBeTrue)
		So(resolved.RequestID, ShouldEqual, "req-ask-1")
		So(resolved.Skipped, ShouldBeFalse)
	})

	Convey("Given 工具审批在带外轮通道上发起(全程不 setOut), When SubmitToolPermission 成功, Then ToolPermissionResolved 回到该轮通道", t, func() {
		r := New()
		a := &claudeActive{handle: &fakeCCHandle{}, permissionMode: "default"}
		r.cache.Put(sessionKey(2425), a)
		frames, evOut, stop := startTurnDrain(a)
		defer stop()

		frames <- claudecode.Event{Kind: claudecode.EventControlRequest, ControlRequest: &claudecode.ControlRequestEvent{
			RequestID: "req-perm-1",
			ToolName:  "Bash",
			Input:     json.RawMessage(`{"command":"ls"}`),
		}}
		perm, ok := recvEvent(t, evOut, "ToolPermissionRequest").(agentruntime.ToolPermissionRequest)
		So(ok, ShouldBeTrue)
		So(perm.RequestID, ShouldEqual, "req-perm-1")

		err := r.SubmitToolPermission(context.Background(), 2425, "req-perm-1", true, false, "")
		So(err, ShouldBeNil)

		resolved, ok := recvEvent(t, evOut, "ToolPermissionResolved").(agentruntime.ToolPermissionResolved)
		So(ok, ShouldBeTrue)
		So(resolved.RequestID, ShouldEqual, "req-perm-1")
		So(resolved.Allowed, ShouldBeTrue)
	})

	// 边界:waiter 跨轮存活(register/take 不随 turn 清理),本轮 drain 退出后
	// 通道已 close —— 迟到的应答不能往已关闭通道写(panic),只能静默丢弃。
	Convey("Given 发起该请求的那一轮已结束, When 迟到的 SubmitAnswer 到达, Then 不 panic 也不写已关闭通道", t, func() {
		r := New()
		a := &claudeActive{handle: &fakeCCHandle{}}
		r.cache.Put(sessionKey(2425), a)
		frames, evOut, stop := startTurnDrain(a)

		frames <- claudecode.Event{Kind: claudecode.EventControlRequest, ControlRequest: &claudecode.ControlRequestEvent{
			RequestID: "req-ask-late",
			ToolName:  "AskUserQuestion",
			Input:     json.RawMessage(`{"questions":[{"question":"还答吗？","header":"迟到","options":[{"label":"答"}]}]}`),
		}}
		_, ok := recvEvent(t, evOut, "UserAskRequest").(agentruntime.UserAskRequest)
		So(ok, ShouldBeTrue)
		stop() // 本轮结束:drain 退出 + evOut 关闭

		So(func() {
			_ = r.SubmitAnswer(context.Background(), 2425, "req-ask-late", nil,
				[]agentruntime.AskAnswer{{QuestionIndex: 0, Labels: []string{"答"}}}, false)
		}, ShouldNotPanic)
	})
}

// TestRuntime_PendingWaiters 覆盖 R7:重连的客户端要能枚举某会话当前所有阻塞中的
// waiter 并重建审批/提问卡片,而不只是能按已知 requestID 回答。PendingWaiters 是
// registerAskWaiter / registerPermWaiter 的读侧 —— 之前只有 register + take,没有
// list。
func TestRuntime_PendingWaiters(t *testing.T) {
	Convey("Given 一个 permWaiter 和一个 askWaiter 都在阻塞, When PendingWaiters, Then 两者都出现在快照里", t, func() {
		r := New()
		a := &claudeActive{handle: &fakeCCHandle{}}
		r.cache.Put(sessionKey(3001), a)

		a.registerPermWaiter("perm-1", "Bash", json.RawMessage(`{"command":"ls"}`), nil)
		a.registerAskWaiter("ask-1", []agentruntime.AskQuestion{{Question: "继续吗？"}}, json.RawMessage(`{"questions":[]}`), nil)

		snap := r.PendingWaiters(context.Background(), 3001)

		So(snap.ToolPermissions, ShouldHaveLength, 1)
		So(snap.ToolPermissions[0].RequestID, ShouldEqual, "perm-1")
		So(snap.ToolPermissions[0].ToolName, ShouldEqual, "Bash")
		So(string(snap.ToolPermissions[0].Input), ShouldEqual, `{"command":"ls"}`)

		So(snap.AskUserQuestions, ShouldHaveLength, 1)
		So(snap.AskUserQuestions[0].RequestID, ShouldEqual, "ask-1")
		So(snap.AskUserQuestions[0].Questions, ShouldResemble, []agentruntime.AskQuestion{{Question: "继续吗？"}})
	})

	Convey("Given 会话没有任何阻塞中的 waiter, When PendingWaiters, Then 返回空快照", t, func() {
		r := New()
		a := &claudeActive{handle: &fakeCCHandle{}}
		r.cache.Put(sessionKey(3002), a)

		snap := r.PendingWaiters(context.Background(), 3002)

		So(snap.ToolPermissions, ShouldBeEmpty)
		So(snap.AskUserQuestions, ShouldBeEmpty)
	})

	Convey("Given sessionID 不在缓存里(未 spawn / 已 evict), When PendingWaiters, Then 返回空快照不 panic", t, func() {
		r := New()
		So(func() {
			snap := r.PendingWaiters(context.Background(), 9999)
			So(snap.ToolPermissions, ShouldBeEmpty)
			So(snap.AskUserQuestions, ShouldBeEmpty)
		}, ShouldNotPanic)
	})
}

// TestSubmitAnswer_SecondSubmitOnSameRequestID_ErrWaiterNotFound 与
// TestSubmitToolPermission_SecondSubmitOnSameRequestID_ErrWaiterNotFound
// 覆盖 R8 幂等提交的生产端一半:take-and-delete 命中一次之后,同一个 requestID
// 再提交必须能被消费方(daemon handler)按 errors.Is 识别为「waiter 已消失」而
// 不是任意错误 —— 这要求第二次失败返回的错误包裹 agentruntime.ErrWaiterNotFound。
func TestSubmitAnswer_SecondSubmitOnSameRequestID_ErrWaiterNotFound(t *testing.T) {
	Convey("Given 一个 AskUserQuestion 已经被回答过一次, When 同一 requestID 再次 SubmitAnswer, Then 返回 ErrWaiterNotFound", t, func() {
		r := New()
		a := &claudeActive{handle: &fakeCCHandle{}}
		r.cache.Put(sessionKey(4001), a)
		frames, evOut, stop := startTurnDrain(a)
		defer stop()

		frames <- claudecode.Event{Kind: claudecode.EventControlRequest, ControlRequest: &claudecode.ControlRequestEvent{
			RequestID: "req-dup-1",
			ToolName:  "AskUserQuestion",
			Input:     json.RawMessage(`{"questions":[{"question":"再问一次？","header":"h","options":[{"label":"好"}]}]}`),
		}}
		_, ok := recvEvent(t, evOut, "UserAskRequest").(agentruntime.UserAskRequest)
		So(ok, ShouldBeTrue)

		answers := []agentruntime.AskAnswer{{QuestionIndex: 0, Labels: []string{"好"}}}
		err := r.SubmitAnswer(context.Background(), 4001, "req-dup-1", nil, answers, false)
		So(err, ShouldBeNil)

		err = r.SubmitAnswer(context.Background(), 4001, "req-dup-1", nil, answers, false)
		So(errors.Is(err, agentruntime.ErrWaiterNotFound), ShouldBeTrue)
	})
}

func TestSubmitToolPermission_SecondSubmitOnSameRequestID_ErrWaiterNotFound(t *testing.T) {
	Convey("Given 一个工具审批已经被处理过一次, When 同一 requestID 再次 SubmitToolPermission, Then 返回 ErrWaiterNotFound", t, func() {
		r := New()
		a := &claudeActive{handle: &fakeCCHandle{}, permissionMode: "default"}
		r.cache.Put(sessionKey(4002), a)
		frames, evOut, stop := startTurnDrain(a)
		defer stop()

		frames <- claudecode.Event{Kind: claudecode.EventControlRequest, ControlRequest: &claudecode.ControlRequestEvent{
			RequestID: "req-dup-2",
			ToolName:  "Bash",
			Input:     json.RawMessage(`{"command":"ls"}`),
		}}
		_, ok := recvEvent(t, evOut, "ToolPermissionRequest").(agentruntime.ToolPermissionRequest)
		So(ok, ShouldBeTrue)

		err := r.SubmitToolPermission(context.Background(), 4002, "req-dup-2", true, false, "")
		So(err, ShouldBeNil)

		err = r.SubmitToolPermission(context.Background(), 4002, "req-dup-2", true, false, "")
		So(errors.Is(err, agentruntime.ErrWaiterNotFound), ShouldBeTrue)
	})
}

// TestPendingWaiters_ConcurrentWithRegisterAndTake 钉住枚举的并发安全:
// PendingWaiters 是唯一一条在 drain goroutine 之外读 permWaiters/askWaiters 的
// 路径,而它天然与 handleControlRequest 的 register、SubmitXxx 的 take 并发 ——
// 重连的客户端查待决策时,远端那一轮正在照跑。
//
// 会被拒绝的错误实现:pendingWaiters 不持 permMu/askMu 就遍历 map。-race 下
// 立即报 data race;不带 -race 时 Go 运行时也会对并发读写 map 直接 fatal。
func TestPendingWaiters_ConcurrentWithRegisterAndTake(t *testing.T) {
	Convey("Given 一轮在不停登记/取走 waiter, When 同时反复枚举, Then 无 data race 且每次快照自洽", t, func() {
		a := &claudeActive{handle: &fakeCCHandle{}}

		const n = 200
		var wg sync.WaitGroup
		wg.Add(3)

		go func() {
			defer wg.Done()
			for i := range n {
				id := "perm-" + strconv.Itoa(i)
				a.registerPermWaiter(id, "Bash", json.RawMessage(`{"command":"ls"}`), nil)
				a.takePermWaiter(id)
			}
		}()
		go func() {
			defer wg.Done()
			for i := range n {
				id := "ask-" + strconv.Itoa(i)
				a.registerAskWaiter(id, []agentruntime.AskQuestion{{Question: "q"}}, nil, nil)
				a.takeAskWaiter(id)
			}
		}()
		// goconvey 的 So 只能在测试主 goroutine 上调用,枚举侧先记账,Wait 后再断言。
		var partial int
		go func() {
			defer wg.Done()
			for range n {
				snap := a.pendingWaiters()
				// 每条枚举出来的记录都必须是完整载荷,不能读到半初始化的 waiter。
				for _, p := range snap.ToolPermissions {
					if p.RequestID == "" || p.ToolName != "Bash" || len(p.Input) == 0 {
						partial++
					}
				}
				for _, q := range snap.AskUserQuestions {
					if q.RequestID == "" || len(q.Questions) != 1 {
						partial++
					}
				}
			}
		}()

		wg.Wait()
		So(partial, ShouldEqual, 0)
		So(a.pendingWaiters(), ShouldResemble, agentruntime.WaiterSnapshot{})
	})
}

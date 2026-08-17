package claudecode

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cago-frame/agents/provider"
)

// claudeStartupCheckTimeout 是 OpenSession 在 spawn 完之后等子进程"是不是立刻
// 自杀"的窗口。健康 CLI 启动后只会阻塞读 stdin（几毫秒内不会自然 exit），坏
// CLI（resume 不存在 / 二进制路径错）会在百毫秒内写 stderr + exit。200ms 留出
// 慢机的余量，错过这个窗口的早退由 Turn / 0-frame fallback 兜底（也走 ExitErr）。
var claudeStartupCheckTimeout = 200 * time.Millisecond

// interruptAckBound 是 Interrupt / StopTask 等 control_request 写帧后等 ack 的
// 上界（与 chat_svc 的 piStopAbortWriteBound=500ms 同值）。CLI 不回执（卡在别的
// 处理上）时超时返回 ErrInterruptPending —— 帧已写、请求在途,不无界挂住调用方
// 的 goroutine,也不因超时杀子进程。
const interruptAckBound = 500 * time.Millisecond

// Session 是一个常驻的 claude 子进程。多个 Turn 复用同一个 stdin/stdout，
// 适合"每个 chat session 一个子进程"的部署形态——避免每轮 spawn 的 cold start，
// 也让 hooks/--settings 注入只做一次。
//
// 调用约束：
//   - Turn 串行调用；上一个 Turn 的事件 channel 必须完全 drain（或上下文取消）
//     之后再调下一个 Turn。
//   - Close 之后 Turn 必须返回错误。
//
// 与 Stream 的区别：Stream 是一次性会话（spawn+drain+exit），Session 跨多轮。
// pkg 内两套并存：probe / 简单一次性问答继续用 Stream；chat_svc 这类需要长会话
// 的走 Session。
type Session struct {
	proc            *process
	cleanupSettings func()

	scanner *bufio.Scanner

	// rawSink 若非 nil,readLoop 每读到一行非空 stdout 就同步回调一次(未解析的
	// stream-json 帧)。debug 级原始帧转储用;由 OpenSession 从 Client.rawSink 注入。
	// 回调收到的 []byte 是 scanner 复用缓冲,不得跨调用留存。
	rawSink func([]byte)

	sessionID string // 由 system init 帧填入；首次 Turn 后稳定
	model     string // 由 system init 帧 model 字段填入；新一轮如果换 model CLI 会重新发 init

	// turnMu 串行化 user Turn 生命周期 —— 上一个 Turn 的事件 channel 收尾(done)之前
	// 拒绝下一个 Turn 启动。Turn 的 waiter goroutine 持有该锁直到本轮 done。
	// 自主轮不走 turnMu(它没有对应的 Turn 调用)。
	turnMu sync.Mutex
	// stdinMu 串行化对 proc.stdin 的写 —— Turn 的 user frame 写完就释放，
	// Interrupt 的 control_request 写时再单独获取。**绝不**跨 turn 生命周期持有。
	stdinMu sync.Mutex
	closed  bool // Close 已调用，stdinMu 保护

	// control_request → 等 control_response 的 channel registry。
	// key = request_id（Interrupt 生成的 v4 UUID）；parseLine 看到 control_response
	// 帧时按 request_id 路由。
	ctrlMu      sync.Mutex
	ctrlPending map[string]chan controlResponse

	// lastErr 来自 routeUntilResult / Close 路径检测到的退出错误（typically
	// proc.exitErrIfDone），ExitErr 优先读它再 fallback 到 proc。原子的 *error
	// 兼容多 goroutine 读写。
	lastErr atomic.Value // holds *error

	// lastAssistantUsage 跟踪当前 turn 内最后一帧 assistant.message.usage。
	// result.usage 是整轮所有内部 API call 用量的累加，不能直接拿来当"当前
	// 上下文占用"（工具循环越多越虚高）。EventDone 优先吐 lastAssistantUsage
	// 反映模型这一刻看到的输入大小；缺省 fallback 到 result.usage。
	// 每个 result 帧吐完 EventDone 后置 nil，避免跨 turn 串味。
	// 仅 readLoop(单 goroutine)读写,无需额外锁。
	lastAssistantUsage *rawUsage

	// —— 常驻 demux reader 状态(sinkMu 保护)——
	// readLoop 占住 scanner 整个子进程生命周期,把每帧 demux 到「当前活跃轮」的 ch。
	// 一刻只有一个活跃轮(CLI 串行 emit,每轮 result 收尾)。归属规则:某轮以「后台型
	// task_notification」开头 → 自主轮(经 autoCh 吐出);否则按 FIFO 取一个 pendingTurns
	// 里等待的 user Turn。
	sinkMu       sync.Mutex
	active       *activeTurn            // 当前正在投递帧的轮;nil = 轮间空闲
	pendingTurns chan *activeTurn       // 已写 stdin、等待其帧到达的 user Turn(FIFO)
	autoCh       chan *AutoTurn         // AutonomousTurns() 返回的 channel;子进程退出时 close
	subagentCh   chan *SubagentActivity // 后台 subagent 活动轮的出口(无消费方时缓冲兜底)

	// readerDone 在 readLoop 收尾(子进程 EOF / Close)时 close。等 control_response
	// 的调用方必须一并 select 它:reader 一走就再没有人 dispatch 回执,只等 ch / ctx
	// 会永久静默挂起(Wails RPC 的 ctx 没有 deadline,表现为「发送没反应且不报错」)。
	// 刻意用它而不是 proc.exit —— 子进程退出后 reader 可能还在 drain 缓冲里的帧,
	// 那期间合法回执仍会到达;readLoop 真正收尾才是「回执不可能再来」的准确时点。
	readerDone chan struct{}
}

// controlResponse 是 control_response 帧 response 字段的最小子集。
// subtype 通常是 "success" / "error"。
type controlResponse struct {
	Subtype string `json:"subtype"`
	Error   string `json:"error,omitempty"`
}

// OpenSession spawns a persistent claude subprocess in stream-json mode.
//
// 与 Client.Stream 的差异：不写任何 user frame，留给后续 Turn 触发。返回时
// 进程已启动但还没有 frame 流过——首个 frame 由首个 Turn 的 user input 触发。
func (c *Client) OpenSession(ctx context.Context, opts ...RunOption) (*Session, error) {
	spec := runSpec{
		model:                c.model,
		systemPrompt:         c.systemPrompt,
		permissionMode:       c.permissionMode,
		sessionID:            c.sessionID,
		settings:             c.settings,
		mcpConfig:            c.mcpConfig,
		allowedTools:         c.allowedTools,
		effort:               c.effort,
		permissionPromptTool: c.permissionPromptTool,
	}
	for _, o := range opts {
		o(&spec)
	}
	if spec.resumeSessionAtUUID != "" && !spec.forkSession {
		return nil, errors.New("claudecode: ResumeSessionAt requires ForkSession (would destructively rewind source session)")
	}
	settings, cleanupSettings, err := prepareSettings(spec.settings, c.settingsEnv)
	if err != nil {
		return nil, err
	}
	spec.settings = settings

	p, err := c.spawn(ctx, processSpec{
		binary: c.binary,
		args:   buildArgs(spec),
		cwd:    c.cwd,
		env:    c.env,
	})
	if err != nil {
		cleanupSettings()
		return nil, err
	}

	// 健康检查窗口：claude CLI 命中 "No conversation found"、二进制路径错、
	// 启动参数被拒等启动期失败都会几十毫秒内 exit + 写 stderr。这里 200ms 内
	// 等 reaper goroutine close exit channel，命中就把分类后的退出错误返出去，
	// 避免后续 Turn 拿到 broken pipe + 用户看不到真错。
	select {
	case <-p.exit:
		exitErr := p.exitErrIfDone()
		if exitErr == nil {
			// exit 0 但根本没 frame —— 不太可能但兜底一下。
			exitErr = errors.New("claudecode: subprocess exited during OpenSession without emitting init frame")
		}
		cleanupSettings()
		return nil, exitErr
	case <-time.After(claudeStartupCheckTimeout):
		// 进程仍存活 → 视为健康，由后续 Turn 接管 stdout。
	case <-ctx.Done():
		// 调用方取消（极少见）→ 关 stdin 触发 CLI 退出，再返 ctx.Err。
		_ = p.stdin.Close()
		cleanupSettings()
		return nil, ctx.Err()
	}

	s := newSession(p, c.rawSink, spec.sessionID)
	s.cleanupSettings = cleanupSettings
	go s.readLoop()
	return s, nil
}

// newSession 组装一个 Session(不起读循环 —— 由调用方决定)。所有 channel 字段只在
// 这里初始化:手写 &Session{...} 字面量漏掉一个,就是 nil channel 上的永久阻塞,
// 所以构造只此一处。
func newSession(p *process, rawSink func([]byte), sessionID string) *Session {
	sc := bufio.NewScanner(p.stdout)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	return &Session{
		proc:         p,
		scanner:      sc,
		rawSink:      rawSink,
		sessionID:    sessionID,
		pendingTurns: make(chan *activeTurn, 4),
		autoCh:       make(chan *AutoTurn, 8),
		subagentCh:   make(chan *SubagentActivity, 8),
		readerDone:   make(chan struct{}),
	}
}

// SessionID 返回 claude 报告的 session_id。Open 后到首个 Turn 完成之前可能为空——
// 优先取 system init / result 帧里的 session_id；调用方如果只关心"我们传给 CLI
// 的那个 UUID"，应直接保留 WithSessionID 的入参。
func (s *Session) SessionID() string { return s.sessionID }

// Turn 写一条 user frame，返回本轮的事件 channel。channel 在 result 帧到达后关闭。
//
// 并发约束：同一时刻只能有一个 Turn 在飞。第二个 Turn 调用会阻塞直到上一个 Turn
// 的事件 channel 被完全 drain（result 帧出现 → goroutine 关 channel → 释放 mu）。
func (s *Session) Turn(ctx context.Context, prompt string, images ...Image) (<-chan Event, error) {
	s.turnMu.Lock() // 抢 user turn slot —— 上一个 turn 没收尾(done)不让进
	s.stdinMu.Lock()
	if s.closed {
		s.stdinMu.Unlock()
		s.turnMu.Unlock()
		return nil, errors.New("claudecode: session closed")
	}

	// images 非空时 user frame 携带 base64 image content block(图片在前,文本在后)。
	enc, err := buildUserFrame(prompt, images)
	if err != nil {
		s.stdinMu.Unlock()
		s.turnMu.Unlock()
		return nil, err
	}
	// 先登记到 FIFO、再写 stdin。顺序是「登记在前」而不是反过来:这样「本轮的响应帧
	// 到达时队列里一定已有本轮」成为硬不变量,currentTurn 才能对 pendingTurns 做**非
	// 阻塞**认领 —— 空闲(无 Turn 在途)时到达的帧一律丢弃,而不是把 readLoop 永久卡在
	// <-pendingTurns 上(见 currentTurn / canStartUserTurn 里的 sess-2187)。
	at := newActiveTurn(false)
	s.pendingTurns <- at

	if _, err := fmt.Fprintf(s.proc.stdin, "%s\n", enc); err != nil {
		s.unregisterPendingTurn(at) // 没写进去 = 本轮的帧永远不会来,别留在队里错配下一轮
		s.stdinMu.Unlock()
		s.turnMu.Unlock()
		// broken pipe 几乎一定意味着子进程已经死了 —— 这种情况下用 reaper
		// 抓到的分类后退出错误（含 ErrSessionNotFound）替换原始 err，让上层
		// 能用 errors.Is 判定 + 给用户人话提示。
		if exitErr := s.proc.exitErrIfDone(); exitErr != nil {
			s.rememberExitErr(exitErr)
			return nil, exitErr
		}
		return nil, err
	}
	s.stdinMu.Unlock() // stdin 写完立刻释放，给 Interrupt 让路

	go func() {
		defer s.turnMu.Unlock() // 本轮 done(result/EOF)后再放 turn 锁
		select {
		case <-at.done:
		case <-ctx.Done():
			// 消费方放弃:标记 abandon 让 reader 停投递、丢弃余帧;等 reader 真正
			// 读到本轮 result(或子进程 EOF)关 done 后再放 turnMu,避免下一轮帧串味。
			s.markAbandoned(at)
			<-at.done
		}
	}()
	return at.ch, nil
}

// AutonomousTurns 返回 CLI 自主续轮(后台任务完成续轮)的 channel。子进程退出
// (scanner EOF / Close)时 close。消费方 range 它,每个 *AutoTurn 是一轮独立的
// 事件流。无消费方时缓冲(8)兜底,满后 readLoop 在投递下一轮时阻塞(back-pressure)。
func (s *Session) AutonomousTurns() <-chan *AutoTurn { return s.autoCh }

// SubagentActivity 返回「后台 subagent 活动轮」的 channel。子进程退出时 close。消费方 range
// 它,每个 *SubagentActivity 是一轮独立事件流(见类型注释)。无消费方时缓冲(8)兜底。
func (s *Session) SubagentActivity() <-chan *SubagentActivity { return s.subagentCh }

// readLoop 占住 scanner 整个子进程生命周期,把每帧 demux 到当前活跃轮。
// 归属:某轮以「后台型 task_notification」开头 → 自主轮(经 AutonomousTurns 吐出);
// 否则按 FIFO 取一个 pendingTurns 里等待的 user Turn。每轮以 result 收尾。
//
// scanner 退出(EOF / 错误)= 子进程死亡:snapshot 真错给 Session.ExitErr 用,
// 再收尾所有未决轮 + close autoCh。
func (s *Session) readLoop() {
	for s.scanner.Scan() {
		line := s.scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if s.rawSink != nil {
			s.rawSink(line)
		}
		events, done := s.parseLine(line) // 同时把 control_response dispatch 给 ctrlPending
		var f rawFrame
		_ = json.Unmarshal(line, &f) // 仅供归属判定(后台型 task_notification)
		s.route(f, events, done)
	}
	if exitErr := s.proc.exitErrIfDone(); exitErr != nil {
		s.rememberExitErr(exitErr)
	}
	s.shutdownReader()
}

// route 把一帧的事件投给当前活跃轮;done 时收尾该轮。
func (s *Session) route(f rawFrame, events []Event, done bool) {
	at := s.currentTurn(f)
	if at == nil {
		// 自主轮起始标记(已建立 active 并吐 autoCh),或空闲态的非 turn 帧
		// (control_response / status):均无归属轮,本帧事件不下发。
		return
	}
	s.feed(at, events)
	if done {
		s.finishActiveTurn(at)
	}
}

// subagentOwnerID 取一帧空闲后台 subagent 活动帧所属的 Agent 工具 tool_use_id。
// 有效来源仅限 assistant/user 帧携带的 parent_tool_use_id —— 这是子 agent 内部
// API 轮的归属标识符。系统帧(task_notification 等)不携带有效 owner:它们要么
// 已被 isBackgroundTaskNotification 认领起自主续轮,要么因 canStartUserTurn 为 false
// 被丢弃。
// 让 system 帧的 tool_use_id 充当 owner 会在空闲态收到非后台 task_notification
// 时错误地开启一个活动轮,与无 user Turn 等待时的 Phase-1 丢弃语义冲突。
// 取不到返回 ""(调用方按 Phase-1 丢弃)。
func subagentOwnerID(f rawFrame) string {
	if (f.Type == "assistant" || f.Type == "user") && f.ParentToolUseID != "" {
		return f.ParentToolUseID
	}
	return ""
}

// isMainThreadTurnFrame 判定一帧属于「主 agent 自己这一轮」而非某个后台 subagent 的
// 内部活动:user 轮起步的 system:init,以及不带 parent_tool_use_id 的 assistant / user
// 内容帧。
//
// 判据只收这两类是有意的:stream_event / system:task_* / result 在一轮子 agent 活动里
// 也合法出现(且都不带 parent_tool_use_id),拿它们判让位会把一轮正常的活动切得粉碎。
// init 是 CLI 为新 user 轮起步时必发的首帧(2.1.216 抓帧实证),用它当让位点,主线这一轮
// 的第一帧就能落到正确的轮上。
func isMainThreadTurnFrame(f rawFrame) bool {
	switch f.Type {
	case "system":
		return f.Subtype == "init"
	case "assistant", "user":
		return f.ParentToolUseID == ""
	}
	return false
}

// currentTurn 返回当前活跃轮;轮间(active==nil)时按归属规则建立新轮:
//   - 活动轮收到「后台型完成通知」→ 收尾活动轮,落到下方起自主续轮。
//   - 活动轮收到**另一个** owner 的空闲 subagent 帧 → 收尾当前活动轮,落到下方按新 owner
//     另开一轮。同一轮里可以并发派多个 run_in_background subagent,它们在空闲态交替说话;
//     不切轮的话单槽位被先到的 owner 占住,另一个的帧全被塞进它的活动轮,消费方按
//     ToolUseID 过滤子块时整段丢弃(sess-2275)。
//   - 活动轮收到主线帧(system:init / 无 parent_tool_use_id 的 assistant·user)且确有 user
//     Turn 在排队 → 收尾当前活动轮,落到下方认领那一轮。用户在活动轮开着时发消息,CLI 为
//     它起的整轮都是主线帧;不让位的话这一轮会被整段喂进活动轮、连 result 都收错轮,
//     user 的 activeTurn 永远留在 pendingTurns(sess-2974)。
//   - 后台型 task_notification → 自主轮,经 autoCh 吐出,返回 nil(调用方丢弃起始标记)。
//   - 无资格起轮的帧(control_response / 空闲 status / 后台任务状态帧 / 任何未知
//     类型)→ 返回 nil,不认领排队的 user Turn;否则读循环会被这些会话级帧卡死在
//     <-pendingTurns 上,后续 Turn / 自主轮再也读不到 stdout(见 canStartUserTurn)。
//   - 空闲后台 subagent 帧(有 owner) → 开活动轮,经 subagentCh 吐出,首帧也喂进去。
//   - 空闲后台 subagent 帧(无 owner) → 丢弃(兜底,不卡读循环)。
//   - 否则 → **非阻塞**取 FIFO 队首 user Turn;队空(无 Turn 在途)就丢弃本帧。
//     Turn 的登记先于 stdin 写,所以真属于某轮的首帧到达时队首一定已就位。
func (s *Session) currentTurn(f rawFrame) *activeTurn {
	s.sinkMu.Lock()
	if s.active != nil {
		// 后台 subagent 活动轮要让位的三种情况:收到「后台型完成通知」(收尾后落到下方起
		// 自主续轮),收到另一个 subagent 的空闲活动帧(收尾后落到下方按新 owner 另开一轮),
		// 或收到主线帧**且确有 user Turn 在排队**(收尾后落到下方认领那一轮)。
		//
		// 第三条是 sess-2974:用户在活动轮开着时发新消息,CLI 为它起主线 init → 回答 →
		// result,这些帧 parent_tool_use_id 全是 null、owner 为空,旧规则不让位,于是整轮
		// 被喂进活动轮 —— 回答被消费方按 ParentToolCallID 过滤掉,result 收尾的还是活动轮,
		// user 那一轮的 activeTurn 永远留在 pendingTurns、ch 不 close,会话就此卡死。
		// 必须带 len(pendingTurns)>0 这个前提:空闲态 CLI 会自发重播 system:init(sess-2187),
		// 没有轮在排队时让位只会白白腰斩一轮正常的子 agent 活动。
		owner := subagentOwnerID(f)
		yield := s.active.subagentToolUseID != "" &&
			(isBackgroundTaskNotification(f) ||
				(owner != "" && owner != s.active.subagentToolUseID) ||
				(isMainThreadTurnFrame(f) && len(s.pendingTurns) > 0))
		if !yield {
			at := s.active
			s.sinkMu.Unlock()
			return at
		}
		done := s.active
		s.sinkMu.Unlock()
		s.finishActiveTurn(done) // 清 active 槽 + close 活动轮 ch/done
		s.sinkMu.Lock()
	}
	if isBackgroundTaskNotification(f) {
		at := newActiveTurn(true)
		s.active = at
		s.sinkMu.Unlock()
		s.autoCh <- &AutoTurn{
			Events:    at.ch,
			SessionID: s.sessionID,
			Trigger:   triggerBackgroundTask,
			CompletedTask: &CompletedBackgroundTask{
				ToolUseID: f.ToolUseID,
				TaskID:    f.TaskID,
				Status:    normalizeTaskStatus(f.Status),
				Summary:   f.Summary,
			},
		}
		return nil
	}
	if !canStartUserTurn(f) {
		// status:"compacting" 例外:它是手动 /compact 轮的起步首帧 —— 既是子进程「正在压缩、
		// 还活着」的存活证据,又是该轮的进度信号。轮起步(active==nil)有 user Turn 排队时认领
		// 它并把进度喂进去:让 runtime 起步看门狗解除(否则大上下文压缩 >120s 会被误判卡死硬杀
		// 成 errStartupTimeout),前端也能显示「压缩中」。非阻塞 select 保住 canStartUserTurn 的
		// 「无资格帧不认领排队轮」不变量:确有排队轮才认领,没有则退回按会话级帧丢弃,
		// 不会把 readLoop 卡死在 <-pendingTurns 上。
		if isCompactingStatusFrame(f) {
			select {
			case at := <-s.pendingTurns:
				s.active = at
				s.sinkMu.Unlock()
				return at
			default:
			}
		}
		s.sinkMu.Unlock()
		return nil // 会话级帧,空闲到达无归属轮:不认领 user Turn slot
	}
	if owner := subagentOwnerID(f); owner != "" && isIdleBackgroundSubagentFrame(f) {
		at := newActiveTurn(true)
		at.subagentToolUseID = owner
		s.active = at
		s.sinkMu.Unlock()
		s.subagentCh <- &SubagentActivity{ToolUseID: owner, Events: at.ch, SessionID: s.sessionID}
		return at // 与 AutoTurn 不同:首帧(子 agent 内部活动)要喂进活动轮
	}
	if isIdleBackgroundSubagentFrame(f) {
		s.sinkMu.Unlock()
		return nil // owner 取不到的兜底:仍按 Phase 1 丢弃,不卡读循环
	}
	s.sinkMu.Unlock()
	select {
	case at := <-s.pendingTurns: // user 轮起始:取队首(登记先于 stdin 写,故一定已在队)
		s.sinkMu.Lock()
		s.active = at
		s.sinkMu.Unlock()
		return at
	default:
		// 空闲(没有任何 Turn 在途):轮内容帧也只丢弃。这是「readLoop 永不阻塞在
		// pendingTurns 上」的最后一道保险,与 canStartUserTurn 白名单互补 —— 白名单
		// 挡的是 CLI 新增的会话级 system 子类型,这里挡的是白名单**内部**的帧被子进程
		// 在空闲态自发重播(sess-2187 的 system{subtype:"init"})。
		return nil
	}
}

// unregisterPendingTurn 把一个已登记、但最终没能写进 stdin 的轮从 FIFO 里摘掉,
// 保持其余元素的顺序 —— 它的帧永远不会来,留在队里会被下一轮的首帧错配认领。
func (s *Session) unregisterPendingTurn(target *activeTurn) {
	keep := make([]*activeTurn, 0, cap(s.pendingTurns))
	for drained := false; !drained; {
		select {
		case at := <-s.pendingTurns:
			if at != target {
				keep = append(keep, at)
			}
		default:
			drained = true
		}
	}
	for _, at := range keep {
		s.pendingTurns <- at
	}
}

// canStartUserTurn 判定一帧是否有资格认领一个排队中的 user Turn(pendingTurns 队首)。
//
// 这里刻意是**正向白名单**:只有「轮内容帧」——即 parseLine 会为其产出 turn 事件的
// 那几类 —— 才允许起一轮;其余一律不认领,空闲到达时直接丢弃。
//
//   - assistant / user / stream_event / result:一轮的正文与收尾。
//   - system{subtype:"init"}:每轮起手的会话初始化帧,通常就是一轮的首帧。
//   - system{subtype:"compact_boundary"}:压缩轮的正文帧(压缩轮首帧是
//     status:"compacting",见 isCompactingStatusFrame 的非阻塞认领)。
//
// 其余全部返回 false,含:control_response(control_request 的回执,已在 parseLine
// 按 request_id dispatch 给等待者)、system{subtype:"status"}(会话级状态推送)、
// 后台任务生命周期帧(task_started / task_updated / task_progress /
// background_tasks_changed / task_notification)、以及**任何未知的 system 子类型或
// 未知顶层类型**。
//
// 为什么是白名单而不是黑名单:这里原本是一张「非 turn 帧」黑名单,CLI 每加一个会话级
// 子类型就要手工补一个名字,补漏一次就是一次生产事故 —— 帧落到函数末尾的
// `<-s.pendingTurns` 上永久阻塞,readLoop 死掉,该 session 之后既收不到任何 CLI 输出
// (自主续轮再也浮不出来),也拿不到任何 control_request 回执(SetPermissionMode 永久
// 挂起 → 前端「发不出去」且无报错)。已经复发三次:
//
//	sess-429  (CLI 2.1.162 新增 task_updated)
//	sess-1535 (CLI 2.1.205 新增 background_tasks_changed)
//	sess-2014 (CLI 2.1.216 新增 post_turn_summary / task_summary / hook_* /
//	           session_state_changed)
//
// 反转成白名单后,默认行为从「阻塞」变成「丢弃」:CLI 将来再加新帧类型,最坏是少渲染
// 一条会话级状态,而不是整条会话卡死。
//
// 白名单**本身**并不足以保证不阻塞:sess-2187(CLI 2.1.220)漏的是白名单内部的
// system{subtype:"init"} —— 它确实通常是一轮的首帧,但 CLI 也会在 result 之后的空闲态
// 自发重播它(会话 cwd 下的 skill 目录被后台 subagent 改动时)。真正的兜底在
// currentTurn:认领 pendingTurns 改成非阻塞,空闲即丢弃。
func canStartUserTurn(f rawFrame) bool {
	switch f.Type {
	case "assistant", "user", "stream_event", "result":
		return true
	case "system":
		return f.Subtype == "init" || f.Subtype == "compact_boundary"
	}
	return false
}

// isCompactingStatusFrame 判定一帧是否是 status:"compacting" —— 手动 /compact(及自动
// 压缩)起步时 CLI 立刻推的进度帧。它虽属 system{subtype:"status"}(canStartUserTurn
// 为 false 的会话级帧),但在轮起步、有 user Turn 排队时必须由 currentTurn 认领该轮:
// 它是压缩进行中子进程仍存活的唯一信号,丢了会让 runtime 起步看门狗把一次正常的长压缩
// 误判为卡死。
func isCompactingStatusFrame(f rawFrame) bool {
	return f.Type == "system" && f.Subtype == "status" && f.Status == "compacting"
}

// isIdleBackgroundSubagentFrame 判定一帧是否为「后台 subagent(run_in_background 的
// Agent/Task 工具)在空闲态(轮间)产生的内部活动」。这类帧既非后台型 task_notification
// (isBackgroundTaskNotification 已在 currentTurn 中先行认领、起自主续轮),又恰好是
// canStartUserTurn 放行的 assistant / user 帧,但同样不该认领一个排队的 user Turn ——
// 否则 readLoop 卡死在 <-pendingTurns 上,后台 subagent 完成的续轮永远读不到
// (与 sess-429 同类)。
//
// 真 CLI 2.1.185 抓帧实测:后台 subagent 起一轮后主 agent 即 result 收尾、会话转空闲,
// 子 agent 的内部子对话随后在空闲态实时流出。两类需要在此拦下:
//   - assistant / user 帧带 parent_tool_use_id:子 agent 内部 API 轮的文本 / 工具调用 /
//     工具结果。前台 subagent 的同类帧由 active 轮承接(currentTurn 在 active!=nil 时已
//     先返回),所以空闲到达者必属后台 subagent。
//   - 非后台型 task_notification:子 agent 内层 bash 完成通知(output_file 为空)等。后台
//     型(output_file 非空、无 subagent_type)已被 isBackgroundTaskNotification 先认领,
//     故走到这里的 task_notification 一律非后台型。
//
// 注意:Phase 1 止血仅丢弃这类帧;Phase 2(当前)改为经 subagentCh 路由进独立活动轮,
// 按 parent_tool_use_id 嵌套渲染回发起 subagent 的那张卡。
func isIdleBackgroundSubagentFrame(f rawFrame) bool {
	if (f.Type == "assistant" || f.Type == "user") && f.ParentToolUseID != "" {
		return true
	}
	return f.Type == "system" && f.Subtype == "task_notification"
}

// feed 把事件投给 at.ch;at 已被消费方放弃(abandon)时丢弃余帧,避免 reader 阻塞。
func (s *Session) feed(at *activeTurn, events []Event) {
	for _, ev := range events {
		select {
		case at.ch <- ev:
		case <-at.abandon:
			return
		}
	}
}

// finishActiveTurn 收尾一轮:清 active 槽 + close ch(唤醒消费方 range)+ close done(唤醒 waiter)。
func (s *Session) finishActiveTurn(at *activeTurn) {
	s.sinkMu.Lock()
	if s.active == at {
		s.active = nil
	}
	s.sinkMu.Unlock()
	close(at.ch)
	close(at.done)
}

// markAbandoned 标记某轮消费方已放弃(Turn 的 ctx 取消)。close abandon 让 feed 停
// 投递;done 仍由 readLoop 在 result/EOF 时 close。幂等。
func (s *Session) markAbandoned(at *activeTurn) {
	select {
	case <-at.abandon:
	default:
		close(at.abandon)
	}
}

// shutdownReader 在 scanner 退出后收尾:打醒等 control_response 的调用方 + close
// 当前活跃轮 + 排空 pendingTurns(让各自 Turn 的 waiter 解除阻塞)+ close autoCh。
//
// readerDone 必须**第一个**关:后面几步会 close 一串 channel、可能触发调用方立刻
// 重试,让「回执不会再来」这个事实先对所有等待者可见,少一个中间态。
func (s *Session) shutdownReader() {
	close(s.readerDone)
	s.sinkMu.Lock()
	at := s.active
	s.active = nil
	s.sinkMu.Unlock()
	if at != nil {
		close(at.ch)
		close(at.done)
	}
	for {
		select {
		case p := <-s.pendingTurns:
			close(p.ch)
			close(p.done)
		default:
			close(s.autoCh)
			close(s.subagentCh)
			return
		}
	}
}

// rememberExitErr 写到 lastErr，多次写以第一次为准（首因优先 —— 后续路径多半
// 是 broken pipe 这种 derivative error）。
func (s *Session) rememberExitErr(err error) {
	if err == nil {
		return
	}
	s.lastErr.CompareAndSwap(nil, &err)
}

// ExitErr 子进程已退出时返其分类后的退出错误（含 ErrSessionNotFound）；
// 还活着或 exit 0 + 无 stderr 命中 → 返 nil。
//
// 优先读 Session.lastErr（首次检测点的真错），fallback 到 proc.exitErrIfDone
// 拿当前快照。runtime 层 0-frame fallback 用它替换通用 StopErr 消息。
func (s *Session) ExitErr() error {
	if v := s.lastErr.Load(); v != nil {
		if pe, ok := v.(*error); ok && pe != nil {
			return *pe
		}
	}
	if s.proc == nil {
		return nil
	}
	return s.proc.exitErrIfDone()
}

// parseLine 是 frameDecoder.decodeLine 的"无状态副本"——session 多轮场景下不能
// 用 frameDecoder.done 把 reader 钉死。返回 (events, isResult)。
func (s *Session) parseLine(line []byte) ([]Event, bool) {
	var f rawFrame
	if err := json.Unmarshal(line, &f); err != nil {
		return nil, false
	}
	switch f.Type {
	case "system":
		if f.Subtype == "init" {
			if f.SessionID != "" {
				s.sessionID = f.SessionID
			}
			if f.Model != "" {
				s.model = f.Model
				return []Event{{Kind: EventInit, SessionID: s.sessionID, Model: f.Model}}, false
			}
		}
		if ev, ok := parseSystemTask(f, s.sessionID); ok {
			return []Event{ev}, false
		}
		// system{subtype:"status",...} — CLI 通报会话级状态。两个独立维度,允许同一帧同时填:
		//   - permissionMode: mode 变化 (主动 set_permission_mode 回执 / 被动 ExitPlanMode 切换)
		//   - status: 运行态字符串 ("compacting" 等)
		// 两者都空 → 静默忽略 (前向兼容,可能是 CLI 引入了新字段)。
		if f.Subtype == "status" {
			return statusEvents(s.sessionID, f), false
		}
		return nil, false
	case "assistant":
		// isApiErrorMessage:true —— CLI 合成的 API 错误帧(model:"<synthetic>"),不是模型
		// 正文。翻成 EventError 走上层 stopErr → error_text → 独立 ErrorCard,不拼进文本
		// block(见 sess-2153)。非终结:turn 结束仍由随后的 result 帧驱动。
		if f.IsAPIErrorMessage {
			if ev, ok := apiErrorEvent(f, s.sessionID); ok {
				return []Event{ev}, false
			}
		}
		events, usage := parseAssistantContentWithUsage(f.Message, s.sessionID, f.ParentToolUseID, f.IsAPIErrorMessage)
		// 仅记录主 agent 帧的 usage：parent_tool_use_id != "" 的帧来自 Task/Agent
		// subagent 内部 API call，那是独立 Anthropic 会话（自己的 system prompt /
		// context window），用它的用量覆盖主 agent 的会让进度条骤降到 subagent 的
		// 小上下文，明显错。
		//
		// 额外:--include-partial-messages 模式下 CLI 把每次 API call 的真实 usage
		// 放在 stream_event message_delta 上,随后这条 merged assistant 帧的
		// usage 字段是 message_start 状态的 0 拷贝。zero-clobber guard:全 0 视为
		// "没新信息",不要覆盖已经从 stream_event 抓到的真值。
		if usage != nil && f.ParentToolUseID == "" && !isZeroUsage(usage) {
			s.lastAssistantUsage = usage
			// 每个主 agent 帧附加一条 EventUsage，让上层在 turn 内实时刷新
			// 「已用上下文」。EventDone 仍按 resolveDoneUsage 兜底，不变。
			events = append(events, Event{
				Kind:      EventUsage,
				SessionID: s.sessionID,
				Usage: provider.Usage{
					PromptTokens:        usage.InputTokens,
					CompletionTokens:    usage.OutputTokens,
					CachedTokens:        usage.CacheReadInputTokens,
					CacheCreationTokens: usage.CacheCreationInputTokens,
				},
			})
		}
		return events, false
	case "stream_event":
		return s.parseStreamEvent(f), false
	case "user":
		return parseUserContent(f.Message, s.sessionID, f.ParentToolUseID, f.ToolUseResult), false
	case "result":
		if f.SessionID != "" {
			s.sessionID = f.SessionID
		}
		ev := Event{Kind: EventDone, SessionID: s.sessionID, Model: s.model}
		ev.Usage = resolveDoneUsage(s.lastAssistantUsage, f.Usage)
		// 当前 turn 结束，下一轮重新累积 lastAssistantUsage。
		s.lastAssistantUsage = nil
		return []Event{ev}, true
	case "control_response":
		// 路由给在 ctrlPending 上等的 Interrupt 调用者；不产生 Event。
		// 解 request_id 失败 / 没有匹配的等待者 → 直接丢，下游 Interrupt 会
		// 因 ctx 超时返回 ctx.Err。
		s.dispatchControlResponse(line)
		return nil, false
	case "control_request":
		// claude → host：can_use_tool 许可请求。把 ControlRequestEvent 塞到
		// 主 Event 流，runtime 层异步处理（解析 input、emit EventAskUserQuestion、
		// 等用户答完后 Session.RespondToControl 回写）。
		if ev, ok := parseControlRequest(line); ok {
			return []Event{{Kind: EventControlRequest, SessionID: s.sessionID, ControlRequest: ev}}, false
		}
		return nil, false
	}
	return nil, false
}

// dispatchControlResponse 按 request_id 投递 control_response 给 ctrlPending
// 上的等待者。frame schema（实测 Claude CLI 2.1.145 + SDK 0.1.77，
// request_id 在 response **内层**，不是顶层）：
//
//	{"type":"control_response","response":{"subtype":"success"|"error","request_id":"...","error":"..."}}
func (s *Session) dispatchControlResponse(line []byte) {
	resp, reqID, ok := parseControlResponse(line)
	if !ok {
		return
	}
	s.ctrlMu.Lock()
	ch := s.ctrlPending[reqID]
	s.ctrlMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- resp:
	default:
	}
}

// parseControlResponse 拆 control_response 帧；返回 (response payload, request_id, ok)。
// 与真 CLI 对齐：request_id 在 response 内层。空 request_id 视为无效。
func parseControlResponse(line []byte) (controlResponse, string, bool) {
	var f struct {
		Response struct {
			Subtype   string `json:"subtype"`
			RequestID string `json:"request_id"`
			Error     string `json:"error,omitempty"`
		} `json:"response"`
	}
	if err := json.Unmarshal(line, &f); err != nil || f.Response.RequestID == "" {
		return controlResponse{}, "", false
	}
	return controlResponse{Subtype: f.Response.Subtype, Error: f.Response.Error}, f.Response.RequestID, true
}

// Interrupt 写一帧 control_request{subtype:"interrupt"} 让 CLI 软中断当前 turn。
// CLI 会回一帧 control_response 标 success / error，本方法阻塞等这条回执（或 ctx
// 取消 / 子进程收尾 / interruptAckBound 超时）。中断成功后 CLI 还会发一个 result 帧
// （subtype 通常是 "interrupted" /
// "error_during_execution"）让正在 drain 的 Turn 自然返 done —— **子进程保留**，
// 下一轮可以直接复用同一个 Session。
//
// ack 等待有界（interruptAckBound=500ms）：CLI 不回执（卡在别的处理上）时超时返回
// ErrInterruptPending —— 帧已写、中断在途，CLI 处理到后会自然收尾；调用方应把它视
// 为「中断已下发」而非失败，不得因此杀子进程。
//
// 调用约束：可以和 Turn 并发调用（stdinMu 只在写帧期间持有，turnMu 不参与）。
// Close 之后调返错。同一个 Session 并发多次 Interrupt 不冲突（各自 request_id）
// 但意义不大 —— CLI 一时刻只一个 turn。
func (s *Session) Interrupt(ctx context.Context) error {
	s.stdinMu.Lock()
	if s.closed {
		s.stdinMu.Unlock()
		return errors.New("claudecode: session closed")
	}
	reqID := newControlRequestID()
	ch := make(chan controlResponse, 1)
	s.ctrlMu.Lock()
	if s.ctrlPending == nil {
		s.ctrlPending = map[string]chan controlResponse{}
	}
	s.ctrlPending[reqID] = ch
	s.ctrlMu.Unlock()

	frame := map[string]any{
		"type":       "control_request",
		"request_id": reqID,
		"request":    map[string]any{"subtype": "interrupt"},
	}
	enc, mErr := json.Marshal(frame)
	if mErr != nil {
		s.stdinMu.Unlock()
		s.forgetControlRequest(reqID)
		return mErr
	}
	if _, err := fmt.Fprintf(s.proc.stdin, "%s\n", enc); err != nil {
		s.stdinMu.Unlock()
		s.forgetControlRequest(reqID)
		return err
	}
	s.stdinMu.Unlock()

	defer s.forgetControlRequest(reqID)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.readerDone:
		return s.controlRequestAbortedErr("interrupt")
	case <-time.After(interruptAckBound):
		// CLI 不回执：中断帧已写、在途，CLI 处理到后 readLoop 会 dispatch 迟到的
		// control_response / result 让本轮自然收尾。有界返回独立哨兵，不无界挂死。
		return ErrInterruptPending
	case resp := <-ch:
		if resp.Subtype != "success" {
			if resp.Error != "" {
				return fmt.Errorf("claudecode: interrupt rejected: %s", resp.Error)
			}
			return fmt.Errorf("claudecode: interrupt rejected (subtype=%q)", resp.Subtype)
		}
		return nil
	}
}

// StopTask 写一帧 control_request{subtype:"stop_task", task_id:…} 让 CLI 停掉某个
// 具体的后台任务(run_in_background Bash / subagent),而非中断整个 turn —— 后台任务
// 在 turn 结束后仍存活,常驻 readLoop 在轮间照样 dispatch control_response(见
// SetPermissionMode 注释),所以空闲态也能停。CLI 停掉任务后会另发一帧后台型
// task_notification(status cancelled/failed),经既有自主续轮把 subagent_state 翻终态。
//
// 调用约束同 Interrupt:与 Turn 可并发(只持 stdinMu 写帧);Close 后返错。
// CLI 若回执 not_found / not_running(任务已自然结束/竞态)视为幂等成功返 nil。
func (s *Session) StopTask(ctx context.Context, taskID string) error {
	s.stdinMu.Lock()
	if s.closed {
		s.stdinMu.Unlock()
		return errors.New("claudecode: session closed")
	}
	reqID := newControlRequestID()
	ch := make(chan controlResponse, 1)
	s.ctrlMu.Lock()
	if s.ctrlPending == nil {
		s.ctrlPending = map[string]chan controlResponse{}
	}
	s.ctrlPending[reqID] = ch
	s.ctrlMu.Unlock()

	frame := map[string]any{
		"type":       "control_request",
		"request_id": reqID,
		"request":    map[string]any{"subtype": "stop_task", "task_id": taskID},
	}
	enc, mErr := json.Marshal(frame)
	if mErr != nil {
		s.stdinMu.Unlock()
		s.forgetControlRequest(reqID)
		return mErr
	}
	if _, err := fmt.Fprintf(s.proc.stdin, "%s\n", enc); err != nil {
		s.stdinMu.Unlock()
		s.forgetControlRequest(reqID)
		return err
	}
	s.stdinMu.Unlock()

	defer s.forgetControlRequest(reqID)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.readerDone:
		return s.controlRequestAbortedErr("stop_task")
	case <-time.After(interruptAckBound):
		// 与 Interrupt 同款有界：stop_task 帧已写、在途，CLI 停掉任务后会另发
		// task_notification 收尾。超时返回 ErrInterruptPending，不无界挂死。
		return ErrInterruptPending
	case resp := <-ch:
		return stopTaskResponseErr(resp)
	}
}

// stopTaskResponseErr 把 control_response 翻成 StopTask 返回错误。subtype=="success"
// → nil;CLI 报 not_found / not_running 表示任务已不在跑,当幂等成功;其它 subtype
// 带原始 error 文本以便排查。
func stopTaskResponseErr(resp controlResponse) error {
	if resp.Subtype == "success" {
		return nil
	}
	if strings.Contains(resp.Error, "not_found") || strings.Contains(resp.Error, "not_running") {
		return nil
	}
	if resp.Error != "" {
		return fmt.Errorf("claudecode: stop_task rejected: %s", resp.Error)
	}
	return fmt.Errorf("claudecode: stop_task rejected (subtype=%q)", resp.Subtype)
}

// controlRequestAbortedErr 给「readLoop 已收尾、回执不可能再来」的等待者一个明确
// 错误。带上 ExitErr 的首因(ErrSessionNotFound / 非 0 退出等),让上层能 errors.Is
// 判定并给用户人话提示 —— 而不是静默挂到天荒地老。
func (s *Session) controlRequestAbortedErr(subtype string) error {
	if err := s.ExitErr(); err != nil {
		return fmt.Errorf("claudecode: %s aborted, session ended: %w", subtype, err)
	}
	return fmt.Errorf("claudecode: %s aborted: session ended before control_response", subtype)
}

func (s *Session) forgetControlRequest(reqID string) {
	s.ctrlMu.Lock()
	delete(s.ctrlPending, reqID)
	s.ctrlMu.Unlock()
}

// validPermissionModes 是 claude --permission-mode 接受的全集。运行时切换走
// control_request{set_permission_mode} 也用同一组取值。
var validPermissionModes = map[string]struct{}{
	"default":           {},
	"acceptEdits":       {},
	"plan":              {},
	"bypassPermissions": {},
}

// SetPermissionMode 写一帧 control_request{subtype:"set_permission_mode"} 让 CLI
// 切换 permission mode，对齐 Claude TUI 的 Shift+Tab 行为 —— 包括 Turn 在飞时
// 用户点击 mode pill 的场景。
//
// 调用约束：
//   - mode 必须在 {default, acceptEdits, plan, bypassPermissions} 之中，否则
//     直接返错，不发任何帧。
//   - Close 之后调用返错。
//   - 可在 Turn 期间并发调用（与 Interrupt 同模型）—— 写帧只持 stdinMu；等
//     control_response 走 ctrlPending channel，由 Turn goroutine 的 scanner
//     reader 通过 parseLine.dispatchControlResponse 路由回来。Turn 之间调用
//     则用 TryLock 抢 turnMu 自己 drain 一次 scanner（避免 Turn 不在场时
//     channel 永远等不到 dispatcher）。
//
// CLI 在 set_permission_mode 之后还会发 system{subtype:"status",permissionMode:...}
// 帧；同 ExitPlanMode 路径，会被 parseLine 抬成 EventPermissionModeChanged
// 让上层把 DB / UI mode 同步到 CLI 实际状态。
func (s *Session) SetPermissionMode(ctx context.Context, mode string) error {
	if _, ok := validPermissionModes[mode]; !ok {
		return fmt.Errorf("claudecode: invalid permission mode %q (want default|acceptEdits|plan|bypassPermissions)", mode)
	}

	s.stdinMu.Lock()
	if s.closed {
		s.stdinMu.Unlock()
		return errors.New("claudecode: session closed")
	}
	reqID := newControlRequestID()
	ch := make(chan controlResponse, 1)
	s.ctrlMu.Lock()
	if s.ctrlPending == nil {
		s.ctrlPending = map[string]chan controlResponse{}
	}
	s.ctrlPending[reqID] = ch
	s.ctrlMu.Unlock()

	frame := map[string]any{
		"type":       "control_request",
		"request_id": reqID,
		"request":    map[string]any{"subtype": "set_permission_mode", "mode": mode},
	}
	enc, mErr := json.Marshal(frame)
	if mErr != nil {
		s.stdinMu.Unlock()
		s.forgetControlRequest(reqID)
		return mErr
	}
	if _, err := fmt.Fprintf(s.proc.stdin, "%s\n", enc); err != nil {
		s.stdinMu.Unlock()
		s.forgetControlRequest(reqID)
		return err
	}
	s.stdinMu.Unlock()

	defer s.forgetControlRequest(reqID)

	// 持久 readLoop 一直在 drain scanner,control_response 一定被 dispatch 到 ch
	// (不论此刻有没有 user turn 在飞),这里只需等 ch / readerDone / ctx —— 不再需要
	// 在 Turn 不在场时自己 TryLock turnMu drain scanner(那会和 readLoop 抢同一个
	// scanner)。readerDone 是必须的第三条腿:reader 一走就没人 dispatch 回执了,
	// 只等 ch 与一个无 deadline 的 ctx = 永久静默挂起。
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.readerDone:
		return s.controlRequestAbortedErr("set_permission_mode")
	case resp := <-ch:
		return setPermissionModeResponseErr(resp)
	}
}

// setPermissionModeResponseErr 把 control_response 翻译成 SetPermissionMode 的
// 返回错误。subtype=="success" → nil；其它 subtype 带原始 error 文本以便排查。
func setPermissionModeResponseErr(resp controlResponse) error {
	if resp.Subtype == "success" {
		return nil
	}
	if resp.Error != "" {
		return fmt.Errorf("claudecode: set_permission_mode rejected: %s", resp.Error)
	}
	return fmt.Errorf("claudecode: set_permission_mode rejected (subtype=%q)", resp.Subtype)
}

// newControlRequestID 生成 v4 UUID 作 request_id。crypto/rand 失败时退到
// 时间戳兜底（不破坏唯一性 —— Interrupt 调频极低）。
func newControlRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("req-%d", len(b))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Kill 硬杀子进程（SIGKILL），用于 Close 的「关 stdin → 优雅退出」对卡死子进程
// 无效的场景（CLI 卡在 MCP 初始化、阻塞在 socket 上、不读 stdin）。SIGKILL 不可
// 被忽略 → 子进程死亡 → reaper 关 stdout pipe → readLoop 拿 EOF → shutdownReader
// 收尾所有未决轮，等在 Turn channel 上的消费方解阻塞。重入安全。
func (s *Session) Kill() {
	s.stdinMu.Lock()
	if s.closed {
		s.stdinMu.Unlock()
		return
	}
	s.closed = true
	s.stdinMu.Unlock()
	s.proc.kill()
	s.removeSettings()
}

// Close 关 stdin（触发 CLI exit）并 wait 子进程。
// 重入安全：多次调用只第一次生效。
func (s *Session) Close(ctx context.Context) error {
	s.stdinMu.Lock()
	if s.closed {
		s.stdinMu.Unlock()
		s.removeSettings()
		return nil
	}
	s.closed = true
	stdin := s.proc.stdin
	s.stdinMu.Unlock()
	defer s.removeSettings()

	if stdin != nil {
		_ = stdin.Close()
	}
	_ = s.proc.stdout.Close()
	_, err := s.proc.wait(ctx)
	return err
}

func (s *Session) removeSettings() {
	if s.cleanupSettings != nil {
		s.cleanupSettings()
	}
}

// parseAssistantContentWithUsage 把 assistant 帧 inner message 解码成 Event 列表，
// 同时返回 inner message.usage（本次 API call 的 per-call 用量）。
//
// parentToolUseID 对应原始帧顶层的 parent_tool_use_id；主 agent 自己的帧传 ""；
// subagent 内部帧透传外层 Agent.tool_use_id。
//
// isAPIErrorMessage 对应原始帧顶层的 isApiErrorMessage，是判定该帧是否为 CLI 合成
// API 错误帧的权威标志（见下方模型解析处的用法），调用方需原样透传 rawFrame 上的值。
//
// 返回的 *rawUsage == nil 表示这一帧没带 usage 字段（老 CLI 或简化 stub）；
// 调用方据此跟踪"最后一次 per-call 用量"以正确计算上下文窗口占用，参见
// [resolveDoneUsage]。
// parseStreamEvent 处理 type=stream_event 帧。--include-partial-messages 模式
// 下,CLI 把每次内部 API call 的 Anthropic SSE delta 包成这种帧推到 STDOUT。
// 我们只关心 event.type == message_delta 上挂的 usage —— 那是本次 API call
// 的最终 per-call 用量,GLM / openrouter 等 provider 经 gateway 走时这是唯一
// 可信来源(随后 merged assistant 帧的 usage 是 0 占位)。
//
// 其它子类型(message_start / content_block_* / message_stop)目前不消费 —— 内容
// 仍由 merged assistant 帧承载,parser 不需要重复解。
//
// subagent 过滤:沿用 assistant 帧的语义,parent_tool_use_id != "" 的 stream_event
// 来自 Task/Agent 子会话,其 message_delta usage 不能影响主 agent 的进度条。
func (s *Session) parseStreamEvent(f rawFrame) []Event {
	return parseStreamEventUsage(f, s.sessionID, func(u *rawUsage) {
		s.lastAssistantUsage = u
	})
}

// isZeroUsage 判定一份 rawUsage 是否四项全 0。用于 zero-clobber guard:
// stream_event message_delta 写过的真值,不能被随后 merged assistant 帧的全 0
// usage 打回 0。
func isZeroUsage(u *rawUsage) bool {
	if u == nil {
		return true
	}
	return u.InputTokens == 0 && u.OutputTokens == 0 &&
		u.CacheReadInputTokens == 0 && u.CacheCreationInputTokens == 0
}

// apiErrorEvent 把 CLI 的 isApiErrorMessage:true 合成 assistant 帧翻成 EventError。
//
// 文本取 message.content 里第一个非空 text block(即 "API Error: ..." 提示);缺失时
// 用顶层 error 分类码(f.ErrorField)兜底。两个 decoder —— Session.parseLine 与
// frameDecoder.decodeLine —— 共用本函数,避免平行副本漂移。返回 (Event{}, false)
// 表示这帧没有可用文本,调用方回退到正常 assistant 解析(不吞帧)。
func apiErrorEvent(f rawFrame, sid string) (Event, bool) {
	text := firstAssistantText(f.Message)
	if text == "" {
		text = f.ErrorField
	}
	if text == "" {
		return Event{}, false
	}
	return Event{Kind: EventError, SessionID: sid, Err: &APIError{Text: text, Code: f.ErrorField}}, true
}

// firstAssistantText 返回 assistant message.content 里第一个非空 text block 的文本;
// 没有则返回空串。
func firstAssistantText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m rawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	for _, c := range m.Content {
		if c.Type == "text" && c.Text != "" {
			return c.Text
		}
	}
	return ""
}

func parseAssistantContentWithUsage(raw json.RawMessage, sid, parentToolUseID string, isAPIErrorMessage bool) ([]Event, *rawUsage) {
	if len(raw) == 0 {
		return nil, nil
	}
	var m rawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, nil
	}
	out := make([]Event, 0, len(m.Content))
	for _, c := range m.Content {
		switch c.Type {
		case "text":
			if c.Text == "" {
				continue
			}
			out = append(out, Event{Kind: EventTextDelta, SessionID: sid, Text: c.Text, ParentToolUseID: parentToolUseID})
		case "thinking":
			if c.Thinking == "" {
				continue
			}
			out = append(out, Event{Kind: EventThinkingDelta, SessionID: sid, Text: c.Thinking, ParentToolUseID: parentToolUseID})
		case "tool_use":
			out = append(out, Event{
				Kind:            EventPreToolUse,
				SessionID:       sid,
				Tool:            &ToolEvent{ID: c.ID, Name: c.Name, Input: c.Input},
				ParentToolUseID: parentToolUseID,
			})
		}
	}
	// R2：subagent 内部帧（parent_tool_use_id 非空）携带非空 message.model 时，
	// 抬成独立事件透传给上游翻译层。主 agent 帧（parentToolUseID == ""）即便带
	// model 也不产出——那是 EventInit/EventDone 的既有职责，不得被污染（R5）。
	// 老 CLI 不发 message.model 时同样不产出。
	//
	// first-wins（R3，同一子代理只认第一个实际模型）由上游累计态负责去重，这里
	// 每次遇到都如实产出一条，不做去重判断。
	//
	// isAPIErrorMessage 过滤:CLI 合成的 API 错误帧(isApiErrorMessage:true)的
	// message.model 是占位符而非真实模型(见 errors.go 顶部注释)。正常情况下
	// apiErrorEvent 会把这类帧接住翻成 EventError,不会走到这里;但若该帧首个
	// text 块与顶层 error 都为空,apiErrorEvent 判定「无可用文本」而放行,帧就会
	// 落进本函数的正常 assistant 解析路径。判定必须以调用方传入的权威标志为准
	// (R2 修订),不能嗅探占位符字符串的值——占位符取值可能随 CLI 版本变化,而
	// R3 的 first-wins 意味着一旦记错就永久钉进 subagent_state.model 与数据库。
	if parentToolUseID != "" && m.Model != "" && !isAPIErrorMessage {
		out = append(out, Event{
			Kind:            EventSubagentModel,
			SessionID:       sid,
			Model:           m.Model,
			ParentToolUseID: parentToolUseID,
		})
	}
	return out, m.Usage
}

// parseUserContent 把 user 帧的 message + 顶层 tool_use_result 翻译成
// EventPostToolUse 列表。
//
// toolUseResult 是 CLI 在 user 帧顶层（跟 message 同级）吐的工具结构化元数据
// （TaskCreate 的 {"task":{"id":"1"}} 之类）；一条 user 帧通常只承载一个 tool_result
// block,所以 meta 与 block 一对一,直接挂到对应 ToolEvent.ResultMeta 上即可。
// 缺省（普通工具帧没这个字段）时为 nil,ResultMeta 也留 nil。
func parseUserContent(raw json.RawMessage, sid, parentToolUseID string, toolUseResult json.RawMessage) []Event {
	if len(raw) == 0 {
		return nil
	}
	var m struct {
		Content []rawContentBlock `json:"content"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	out := make([]Event, 0, len(m.Content))
	for _, c := range m.Content {
		if c.Type != "tool_result" {
			continue
		}
		tool := &ToolEvent{
			ID:         c.ToolUseID,
			Response:   decodeToolResultContent(c.Content),
			ResultMeta: toolUseResult,
		}
		if c.IsError {
			tool.Err = errIfToolError(c.IsError)
		}
		out = append(out, Event{Kind: EventPostToolUse, SessionID: sid, Tool: tool, ParentToolUseID: parentToolUseID})
	}
	return out
}

// parseSystemTask 与 frameDecoder.decodeSystemTask 等价，给 Session 共用。
//
// 处理两类系统帧：
//   - task_started / task_progress / task_notification ── subagent 生命周期
//   - api_retry ── CLI 把 Anthropic SDK 的可重试错误抬成 first-class 协议帧，
//     字段（attempt / max_retries / retry_delay_ms / error_status / error）直接在帧顶层
//
// 返回 (Event, false) 表示既不是 task_* 也不是 api_retry。
func parseSystemTask(f rawFrame, sid string) (Event, bool) {
	if f.Subtype == "api_retry" {
		return Event{
			Kind:      EventRetry,
			SessionID: sid,
			Retry: &RetryEvent{
				Attempt:     f.Attempt,
				MaxAttempts: f.MaxRetries,
				DelayMs:     f.RetryDelayMs,
				ErrorStatus: f.ErrorStatus,
				ErrorCode:   f.ErrorField,
			},
		}, true
	}
	if f.Subtype == "compact_boundary" {
		ev := Event{Kind: EventCompactBoundary, SessionID: sid, Compact: &CompactEvent{}}
		if len(f.CompactMetadata) > 0 {
			var m struct {
				PreTokens  int    `json:"pre_tokens"`
				PostTokens int    `json:"post_tokens"`
				Trigger    string `json:"trigger"`
				DurationMs int    `json:"duration_ms"`
			}
			// 字段缺失/类型不符时保持零值,UI 自行退化展示。
			_ = json.Unmarshal(f.CompactMetadata, &m)
			ev.Compact.PreTokens = m.PreTokens
			ev.Compact.PostTokens = m.PostTokens
			ev.Compact.Trigger = m.Trigger
			ev.Compact.DurationMs = m.DurationMs
		}
		return ev, true
	}
	var kind EventKind
	switch f.Subtype {
	case "task_started":
		kind = EventTaskStarted
	case "task_progress":
		kind = EventTaskProgress
	case "task_notification":
		kind = EventTaskNotification
	default:
		return Event{}, false
	}
	meta := &SubagentMeta{
		TaskID:          f.TaskID,
		SubagentType:    f.SubagentType,
		TaskType:        f.TaskType,
		TaskDescription: f.Description,
		Prompt:          f.Prompt,
		LastToolName:    f.LastToolName,
		Status:          normalizeTaskStatus(f.Status),
	}
	if len(f.Usage) > 0 {
		var u taskUsage
		if err := json.Unmarshal(f.Usage, &u); err == nil {
			meta.TotalTokens = u.TotalTokens
			meta.ToolUses = u.ToolUses
			meta.DurationMs = u.DurationMs
		}
	}
	return Event{
		Kind:      kind,
		SessionID: sid,
		Tool:      &ToolEvent{ID: f.ToolUseID, Subagent: meta},
	}, true
}

// statusEvents 把 system{subtype:"status"} 帧拆成最多两条独立事件 (Status / PermissionMode),
// 互相不互斥 —— 同一帧两字段都非空时两条都 emit。两字段都空则返回 nil
// (前向兼容:未来 CLI 加新字段不要因为 Status / PermissionMode 都空就误触发).
func statusEvents(sid string, f rawFrame) []Event {
	var out []Event
	if f.Status != "" {
		out = append(out, Event{Kind: EventStatus, SessionID: sid, Status: f.Status})
	}
	if f.PermissionMode != "" {
		out = append(out, Event{Kind: EventPermissionModeChanged, SessionID: sid, PermissionMode: f.PermissionMode})
	}
	return out
}

// errIfToolError 把 tool_result.is_error 翻译成 error。抽出来仅是为了让
// parseUserContent 保持纯函数（与 frameDecoder.decodeUser 的内联 errors.New
// 等价）。
func errIfToolError(isErr bool) error {
	if !isErr {
		return nil
	}
	return errToolReported
}

var errToolReported = errors.New("tool reported error")

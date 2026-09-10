package claudecode

import "github.com/agentre-hub/agentre/internal/pkg/orderedpipe"

// AutoTurn 是 CLI 在没有新 user 输入的情况下自主跑的一轮 —— 当前唯一来源是
// run_in_background Bash 任务完成后,CLI 自主注入 <task-notification> 并跑完整
// 一轮(列目录之类)。Events 与普通 Turn 的事件流同形:result 帧到达后 close。
//
// 消费方(agentruntime claudecode runtime)从 Session.AutonomousTurns() 收到本值,
// drain Events 翻译成 agentruntime 事件,再由 chat_svc 落成一条纯 assistant 轮。
type AutoTurn struct {
	Events    <-chan Event
	SessionID string
	Trigger   string // 当前固定 "background_task"
	// CompletedTask 仅 background_task 触发的自主轮带:那个完成的后台任务的身份,
	// 供上层把对应 subagent_state 翻成 completed/failed。
	CompletedTask *CompletedBackgroundTask
}

// CompletedBackgroundTask 是触发本自主轮的后台命令完成信息(从 task_notification 帧抽出)。
type CompletedBackgroundTask struct {
	ToolUseID string
	TaskID    string
	Status    string // "completed" / "failed"(空 → 视为 completed)
	Summary   string // CLI task_notification.summary,如 "Background command \"…\" completed (exit code 0)"
}

// triggerBackgroundTask 是目前唯一的 AutoTurn 触发原因。
const triggerBackgroundTask = "background_task"

// SubagentActivity 是一轮「后台 subagent 在空闲态产生的内部活动」的事件流。它由 readLoop
// 在空闲态遇到第一帧后台 subagent 内部活动时开出(keyed by 发起该 subagent 的 Agent 工具
// tool_use_id),以「后台型 task_notification(子 agent 完成)」收尾——该完成帧随即触发既有
// 自主续轮(AutonomousTurns)跑主 agent 总结。Events 与普通 Turn 同形:子 agent 内部 assistant/
// user 帧(ParentToolUseID==ToolUseID)、子 agent 的 task_progress/task_updated 等。
//
// 消费方(chat_svc)据 ToolUseID 定位发起 subagent 的那条「发起消息」,把事件嵌套渲染回那张
// AgentSpawnCard。
type SubagentActivity struct {
	ToolUseID string
	Events    <-chan Event
	SessionID string
}

// activeTurn 是 readLoop 当前正在投递帧的那一轮 —— 可能是用户发起的 Turn,也可能
// 是自主轮。一刻只有一个(CLI 串行 emit 各轮,每轮以 result 收尾,从不交错)。
type activeTurn struct {
	// events 是本轮的事件出口。刻意是 pipe 而不是裸 channel:readLoop 既是投递者
	// 又是唯一的 close 者,它在投递上阻塞就会和「等着被 close」的消费方结成死锁
	// (sess-3110,见 pipe 的注释)。收尾 = events.Close();消费方放弃 = events.Abandon()。
	events     *orderedpipe.Pipe[Event]
	done       chan struct{} // readLoop 在本轮收尾(result/EOF)时 close,唤醒 Turn 的 waiter
	autonomous bool          // 自主轮 = true(经 AutonomousTurns 吐出,无对应 Turn 调用)
	// subagentToolUseID 非空 = 本轮是「后台 subagent 活动轮」,值为发起该 subagent 的 Agent
	// 工具 tool_use_id。用于:readLoop 在收到后台完成 task_notification 时识别要收尾的是活动轮。
	subagentToolUseID string
	// mainToolUseIDs 是本主线轮已经见到的外层 tool_use id(ParentToolUseID 为空)。
	// 前台 Agent 的内部帧 parent_tool_use_id 落在这个集合里,必须留在当前主线轮
	// (sess-3090);上一轮后台 subagent 的 parent 不在集合里,继续走 sideActivities
	// (sess-2980)。
	mainToolUseIDs map[string]struct{}
}

func (at *activeTurn) rememberMainToolUse(id string) {
	if at == nil || id == "" || at.subagentToolUseID != "" {
		return
	}
	if at.mainToolUseIDs == nil {
		at.mainToolUseIDs = make(map[string]struct{})
	}
	at.mainToolUseIDs[id] = struct{}{}
}

func (at *activeTurn) launchedOnThisTurn(id string) bool {
	if at == nil || id == "" {
		return false
	}
	_, ok := at.mainToolUseIDs[id]
	return ok
}

// newActiveTurn 造一轮的投递两件套。
func newActiveTurn(autonomous bool) *activeTurn {
	return &activeTurn{
		events:     orderedpipe.New[Event](),
		done:       make(chan struct{}),
		autonomous: autonomous,
	}
}

// feed 把一帧解析出的事件投给本轮。永不阻塞(见 activeTurn.events)。
func (at *activeTurn) feed(events []Event) {
	for _, ev := range events {
		at.events.Push(ev)
	}
}

// finish 收尾本轮:关出口(唤醒消费方 range)+ 关 done(唤醒 Turn 的 waiter)。幂等。
func (at *activeTurn) finish() {
	at.events.Close()
	select {
	case <-at.done:
	default:
		close(at.done)
	}
}

// isBackgroundTaskNotification 判定一帧是否为「后台命令完成」通知 —— 它是自主续轮
// 的起始标记,与 subagent(Task 工具)的 task_notification 区分:
//   - 后台型:有 output_file(落在 tasks/<id>.output),无 subagent_type。
//   - subagent 型:有 subagent_type / description,无 output_file。
//
// 只判帧形。「这个后台任务是主线派的还是某个子 agent 自己派的」是另一维,由
// startsAutonomousTurnLocked 补上 —— 起自主续轮请用它,别直接用本函数。
func isBackgroundTaskNotification(f rawFrame) bool {
	return f.Type == "system" &&
		f.Subtype == "task_notification" &&
		f.OutputFile != "" &&
		f.SubagentType == ""
}

// rememberSubagentToolUse 记下一个由后台 subagent 派出的 tool_use id(见 Session.subagentToolUseIDs)。
func (s *Session) rememberSubagentToolUse(id string) {
	if id == "" {
		return
	}
	s.sinkMu.Lock()
	s.subagentToolUseIDs[id] = struct{}{}
	s.sinkMu.Unlock()
}

// startsAutonomousTurnLocked 在 isBackgroundTaskNotification 之上再加一道**归属**判定:
// 只有主线自己派出的后台任务完成才是自主续轮的起始标记。
//
// sess-3504:一个后台 subagent 在空闲态自己派了条 run_in_background Bash,它完成时 CLI
// 吐出的 task_notification 与主线派的完全同形(有 output_file、无 subagent_type,且系统帧
// 一律不带 parent_tool_use_id),但 CLI **不会**为它起主线一轮(实测该帧之后没有 system:init,
// 主线沉默十余分钟)。据它起轮就造出一个等不到 result 的空自主轮:桌面端显示一个「后台任务
// 完成」的空 assistant 气泡、会话长期停在 running 且毫无输出;它还占住 active 槽 —— 让位判据
// 的前置对主线轮恒假,后续 subagent 帧走 sideActivities 却因主线轮永不收尾而永不结帐(消费方
// 串行卡在那一路上,别的 subagent 卡片一起停更),真正的下一轮也会被整段吞进这个气泡。
//
// 判据取自派发帧:tool_use 落在 subagentToolUseIDs 里就是子 agent 派的。归属未知(如
// --resume 跨进程,派发帧不在本进程的 stdout 里)时按主线处理,保持既有行为。
//
// 调用方持 sinkMu。
func (s *Session) startsAutonomousTurnLocked(f rawFrame) bool {
	if !isBackgroundTaskNotification(f) {
		return false
	}
	_, bySubagent := s.subagentToolUseIDs[f.ToolUseID]
	return !bySubagent
}

// normalizeTaskStatus 把 CLI 的任务状态拼写归一到内部取值。CLI 用英式 "cancelled"
// (task status 枚举 working|input_required|completed|failed|cancelled),内部 /
// SubagentStateBlock 用美式 "canceled"(见 blocks.SubagentStateBlock.Status 注释)。
// 在 CLI 状态进入内部模型的边界归一一次,下游(前端 mapStatus / AgentSpawnCard)不必
// 各自兼容两种拼写。
func normalizeTaskStatus(status string) string {
	if status == "cancelled" {
		return "canceled"
	}
	return status
}

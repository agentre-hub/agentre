package claudecode

import (
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
)

// AutonomousTurns 实现 agentruntime.AutonomousTurnSource:把底层 claudecode.Session
// 的自主续轮(后台任务完成 CLI 自主跑的一轮)桥接成 agentruntime 事件流。
//
// 每个 AutoTurn 复用 drainStream(同 translator / control 协议 / tasks 聚合)。本桥接
// 按 AutoTurn 顺序 **inline** drain —— 自主轮之间不重叠。
//
// 本轮的事件出口是这里新建的 evOut:同步 control_request 经
// handleControlRequest(evOut) 到达前端,异步应答(SubmitAnswer /
// SubmitToolPermission)也按 waiter 记下的 evOut 回投 —— drainStream 用
// attachOut/detachOut 圈住它的存活期,与可能并存的 user turn 通道互不干扰。
//
// sessionID 未 spawn / 已 evict → 返回一个立即 close 的 channel。子进程退出时底层
// AutonomousTurns channel close,本 channel 随之 close。
func (r *Runtime) AutonomousTurns(sessionID int64) <-chan agentruntime.AutonomousTurn {
	out := make(chan agentruntime.AutonomousTurn, 4)
	v, ok := r.cache.Get(sessionKey(sessionID))
	if !ok {
		close(out)
		return out
	}
	a, ok := v.(*claudeActive)
	if !ok || a.handle == nil {
		close(out)
		return out
	}
	src := a.handle.AutonomousTurns()
	if src == nil {
		close(out)
		return out
	}
	go func() {
		defer close(out)
		for at := range src {
			evOut := make(chan agentruntime.Event, 32)
			result := &agentruntime.RunResult{ProviderSessionID: at.SessionID}
			var completed *agentruntime.CompletedBackgroundTask
			if at.CompletedTask != nil {
				completed = &agentruntime.CompletedBackgroundTask{
					ToolUseID: at.CompletedTask.ToolUseID,
					TaskID:    at.CompletedTask.TaskID,
					Status:    at.CompletedTask.Status,
					Summary:   at.CompletedTask.Summary,
				}
			}
			// 先把这一轮交给 consumer(它并发 drain evOut),随后 inline 翻译填 evOut。
			// inline(非 goroutine)保证多个自主轮之间顺序处理、不重叠。
			token := a.nextTurnToken(agentruntime.TurnKindAutonomous)
			out <- agentruntime.AutonomousTurn{Events: evOut, Result: result, Trigger: at.Trigger, CompletedTask: completed, TurnToken: token}
			stream := &ccChanStream{ch: at.Events, sidFn: func() string { return at.SessionID }}
			// 自主续轮的子进程早已存活(由首轮 spawn),不存在「起步即卡死」, 不挂看门狗。
			// 但本轮占着 Session 活跃槽位:期间起的 user turn 收不到任何帧,必须让它的
			// startup 看门狗暂停计时,否则健康子进程会被误杀(见 claudeActive.outOfBand)。
			a.enterOutOfBand()
			drainStream(stream, evOut, result, a, nil)
			a.leaveOutOfBand()
			if sid := stream.SessionID(); sid != "" {
				result.ProviderSessionID = sid
			}
			close(evOut)
		}
	}()
	return out
}

// SubagentActivity 实现 agentruntime.SubagentActivitySource:把底层 claudecode.Session
// 的后台 subagent 内部活动流桥接成 agentruntime 事件流。
//
// 每个 SubagentActivity 复用 drainStream(同 translator / control 协议 / tasks 聚合)。本桥接
// 按活动顺序 **inline** drain —— subagent 活动轮之间不重叠。
//
// 串行是**延迟**上的取舍,不再是正确性前提:一主线轮里同时挂多路活动轮时,后来的几路
// 要等前一路被 close 才轮得到,期间它们的帧攒在 Session 那边。这曾经是死锁 ——
// claudecode.Session 的 readLoop 既投递又是唯一的 close 者,串行消费方一停,它就被
// 焊死在投递/交出上(sess-3110,冻了五个多小时)。现在 Session 侧的出口是非阻塞
// pipe(见 pkg/claudecode/pipe.go),readLoop 的推进不再依赖这里的节奏。
// 真要让多路活动实时并行渲染,得先让 chat_svc.driveSubagentActivity 能安全并发写
// **同一条发起消息**(AppendSubagentChildren / PatchSubagentProgress 是读-改-写)。
//
// 事件出口与应答回投的规则同 AutonomousTurns 的注释。
//
// sessionID 未 spawn / 已 evict → 返回一个立即 close 的 channel。子进程退出时底层
// SubagentActivity channel close,本 channel 随之 close。
func (r *Runtime) SubagentActivity(sessionID int64) <-chan agentruntime.SubagentActivity {
	out := make(chan agentruntime.SubagentActivity, 4)
	v, ok := r.cache.Get(sessionKey(sessionID))
	if !ok {
		close(out)
		return out
	}
	a, ok := v.(*claudeActive)
	if !ok || a.handle == nil {
		close(out)
		return out
	}
	src := a.handle.SubagentActivity()
	if src == nil {
		close(out)
		return out
	}
	go func() {
		defer close(out)
		for sa := range src {
			evOut := make(chan agentruntime.Event, 32)
			result := &agentruntime.RunResult{ProviderSessionID: sa.SessionID}
			// 先把这一轮活动交给 consumer(它并发 drain evOut),随后 inline 翻译填 evOut。
			// inline(非 goroutine)保证多个活动轮之间顺序处理、不重叠。
			out <- agentruntime.SubagentActivity{ToolUseID: sa.ToolUseID, Events: evOut, TurnToken: a.nextTurnToken(agentruntime.TurnKindSubagentActivity)}
			stream := &ccChanStream{ch: sa.Events, sidFn: func() string { return sa.SessionID }}
			// 活动轮的子进程早已存活(由首轮 spawn),不存在「起步即卡死」, 不挂看门狗。
			// 与自主续轮同理:本轮占着 Session 活跃槽位,期间起的 user turn 收不到帧。
			a.enterOutOfBand()
			drainStream(stream, evOut, result, a, nil)
			a.leaveOutOfBand()
			close(evOut)
		}
	}()
	return out
}

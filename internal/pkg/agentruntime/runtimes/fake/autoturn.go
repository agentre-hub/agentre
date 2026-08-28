package fake

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// AutonomousOutputPrefix 是自主续轮文本的固定前缀:e2e-autonomous-output:<label>。
// spec 据此断言「自主轮的已流出内容此刻可见」,与普通回显(ReplyPrefix)区分。
const AutonomousOutputPrefix = "e2e-autonomous-output:"

// autoTurnTrigger 镜像 claudecode 的自主续轮触发原因(后台任务完成)。
const autoTurnTrigger = "background_task"

// autoTurnChunks 是标记文本之后再慢速追加的分片数。分片间隔可调,
// 默认 10 × 500ms ≈ 5s —— 给 spec 留出「自主轮还在流式时用户再发一条」的时间窗。
const autoTurnChunks = 10

// AutonomousTurns 实现 agentruntime.AutonomousTurnSource:返回该会话的自主续轮 channel。
//
// 与真实 claudecode 桥接(runtimes/claudecode/autoturn.go)的差异,都是 fake 的刻意简化:
//   - 无子进程 / 无 LRU,channel 按 sessionID 惰性建后常驻,**不 close** —— 从未触发
//     e2e-bg-task 指令的会话拿到的就是一个永远空闲的 channel(调用安全,consumer 的
//     watcher goroutine 只是一直阻塞在 range 上,随 app 进程退出);
//   - 不带 CompletedTask:fake 不产出真实的后台 Bash tool_use 块,没有可定向翻转的
//     subagent_state,chat_svc 对 nil CompletedTask 全程 no-op。
//
// chat_svc 每会话只订阅一次(autoWatchers 去重),所以同一 channel 只有一个 consumer。
func (r *Runtime) AutonomousTurns(sessionID int64) <-chan agentruntime.AutonomousTurn {
	return r.autoTurnChan(sessionID)
}

// autoTurnChan 取(必要时建)某会话的自主续轮 channel。
func (r *Runtime) autoTurnChan(sessionID int64) chan agentruntime.AutonomousTurn {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.autoTurns[sessionID]
	if !ok {
		ch = make(chan agentruntime.AutonomousTurn, 4)
		r.autoTurns[sessionID] = ch
	}
	return ch
}

// scheduleAutonomousTurn 在用户轮收尾后延迟推一轮自主续轮:先把 turn 交给 consumer
// (它并发 drain Events),再慢速灌文本 —— 顺序与真实桥接一致。
//
// 独立 goroutine 且不带 ctx:本轮的 ctx 在 turn 收尾时就被 cancel,而真实的自主续轮
// 本就发生在用户轮之后。所有 channel 缓冲都开到大于发送次数(turn channel 4、
// 事件 channel > 分片数),发送永不阻塞 → 即使没人 drain,goroutine 也必然自行退出。
func (r *Runtime) scheduleAutonomousTurn(sessionID int64, label string) {
	startDelay := configuredAutoTurnDelay()
	chunkDelay := configuredAutoTurnChunkDelay()
	turns := r.autoTurnChan(sessionID)
	go func() {
		time.Sleep(startDelay)
		events := make(chan agentruntime.Event, autoTurnChunks+4)
		turns <- agentruntime.AutonomousTurn{
			Events: events,
			Result: &agentruntime.RunResult{
				ProviderSessionID: fmt.Sprintf("e2e-fake-%d", sessionID),
				Model:             "e2e-fake-model",
				ContextWindow:     ContextWindowTokens,
			},
			Trigger: autoTurnTrigger,
		}
		// **每一片都带标记文本**,而不是只在首片:per-turn 流是 Wails 事件,没有重放,
		// 前端要收到 StreamAutonomousStarted 后才 openStream 订阅,首片很可能在订阅建立前
		// 就发出去而丢失(实测:慢首屏时 spec 永远等不到标记)。分片慢速追加,整轮持续数秒,
		// 构成「流式中用户再发一条」的可观测窗口。
		for i := 1; i <= autoTurnChunks; i++ {
			time.Sleep(chunkDelay)
			events <- agentruntime.TextDelta{
				Text: fmt.Sprintf("%s%s#%d ", AutonomousOutputPrefix, label, i),
			}
		}
		events <- agentruntime.Done{}
		close(events)
	}()
}

// Steer 实现 agentruntime.Steerer,只为让「自主轮流式中用户再发一条」走到与真实
// claudecode 完全相同的前端分支:
//
//	composer 见到 streaming 就先调 Enqueue → 真实 claudecode 在自主轮期间 inTurn=false
//	→ ErrNoActiveTurn → chat_svc 翻成 ChatSteerNoActive → 前端回退成 doSend 另起一轮。
//
// fake 不支持真正的 mid-turn 注入,所以用户轮进行中报一个普通错(前端提示,不会误把
// 正常流式当成"另起一轮")。
func (r *Runtime) Steer(_ context.Context, sessionID int64, _, _ string) error {
	if !r.hasActiveTurn(sessionID) {
		return agentruntime.ErrNoActiveTurn
	}
	return errors.New("agentruntime/runtimes/fake: mid-turn steer not supported")
}

// enterTurn / leaveTurn / hasActiveTurn 维护每会话进行中的用户轮计数。
func (r *Runtime) enterTurn(sessionID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inTurn[sessionID]++
}

func (r *Runtime) leaveTurn(sessionID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n := r.inTurn[sessionID] - 1; n > 0 {
		r.inTurn[sessionID] = n
	} else {
		delete(r.inTurn, sessionID)
	}
}

func (r *Runtime) hasActiveTurn(sessionID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inTurn[sessionID] > 0
}

// configuredAutoTurnDelay 是「用户轮结束 → 自主轮开始」的间隔,默认 800ms:
// 必须晚于 chat_svc 把该用户轮 finalize(落库 + StreamDone),否则两轮在前端重叠,
// 观测到的就不是自主轮本身了。可用 AGENTRE_E2E_FAKE_AUTOTURN_DELAY_MS 调。
func configuredAutoTurnDelay() time.Duration {
	return envDuration("AGENTRE_E2E_FAKE_AUTOTURN_DELAY_MS", 800*time.Millisecond)
}

// configuredAutoTurnChunkDelay 是自主轮分片间隔,默认 500ms(× autoTurnChunks ≈ 5s)。
// 可用 AGENTRE_E2E_FAKE_AUTOTURN_CHUNK_MS 调。
func configuredAutoTurnChunkDelay() time.Duration {
	return envDuration("AGENTRE_E2E_FAKE_AUTOTURN_CHUNK_MS", 500*time.Millisecond)
}

// envDuration 读毫秒数环境变量;未设 / 非法 / 非正数 → fallback。
func envDuration(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}

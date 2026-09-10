package claudecode

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/httpgateway"
)

// claudeActive 一个 chat session 当前的常驻 claude 子进程状态。
// 与现有顶层 claudecode.go.claudeActive 平行,emit channel 类型由
// chan<- RuntimeEvent 换成 chan<- agentruntime.Event(其它字段一致)。
type claudeActive struct {
	sessionUUID string
	handle      ccSessionHandle
	steer       *httpgateway.SteerInbox
	pool        *agentruntime.CLISessionPool
	poolKey     string
	inTurn      atomic.Bool
	// outOfBand 记录本 session 上正在流的「带外轮」数量 —— 自主续轮(后台任务完成
	// CLI 自主跑的一轮)和后台 subagent 活动轮。它们占着 pkg/claudecode.Session 的
	// 活跃槽位:期间新起的 user turn 虽已把 user 帧写进 stdin,但真 CLI 要等当前轮
	// result 之后才为它起 init(2.1.216 抓帧实证),所以 user turn 在这段时间内
	// **一帧都收不到**。startup 看门狗必须据此暂停计时,否则会把一个健康、正忙的
	// 子进程当成「起步即卡死」硬杀(sess-1950)。
	outOfBand atomic.Int32
	// mainThreadOutOfBand 是 outOfBand 中**占着 CLI 主线**的那一部分 —— 只有自主续轮
	// 算数。区别只在后台 subagent 活动轮:它跑的是子 agent 的内部循环,CLI 主线此刻
	// 空闲,用户新消息该正正经经起一轮(Session.currentTurn 会让位给排队的 user Turn,
	// 见 sess-2974 分支),而不是插进那个子 agent 的上下文里被它的工具循环吞掉。
	//
	// 「主线是否忙」和「帧流是否被占着」是两个问题:看门狗与空闲清扫问的是后者
	// (outOfBand),Steer 问的是前者(mainThreadBusy)。
	mainThreadOutOfBand atomic.Int32
	// turnSeq 会话级轮计数器:每次 Run(用户轮)入口 / 自主续轮入口 / 后台 subagent
	// 活动轮入口递增一次,值随 RunResult / AutonomousTurn / SubagentActivity 暴露给
	// chat_svc,供 Abort 按 token 精确寻址(决策 1)。
	turnSeq atomic.Uint64
	// curTurnToken / curTurnKind 记录「当前活跃轮」的 token 与类型(0=暂无)。每次轮
	// 入口(nextTurnToken)覆盖为最新一轮;Abort(turnToken!=0)只在该 token 仍是当前
	// 活跃轮时才中断,否则 stale no-op。curTurnKind 供 Abort 上报被中断轮的类型。
	curTurnToken atomic.Uint64
	curTurnKind  atomic.Value // holds agentruntime.TurnKind
	// askWaiters 记录当前阻塞中的 AskUserQuestion control_request。
	askMu      sync.Mutex
	askWaiters map[string]*askWaiter

	// permWaiters 记录当前阻塞中的非-AskUserQuestion can_use_tool control_request。
	permMu      sync.Mutex
	permWaiters map[string]*permWaiter

	// permissionMode CLI 当前的 permission mode 快照。spawn 时直赋(发布前无竞争),
	// 之后的读写走 permissionModeSnapshot / setPermissionModeSnapshot:drain goroutine
	// 收到 EventPermissionModeChanged 时同步,Runtime.SetPermissionMode(用户切 pill,
	// claudecode 允许轮中切)成功后也写 —— 跨 goroutine,由 modeMu 保护。
	modeMu         sync.Mutex
	permissionMode string

	// outMu/liveOuts 当前仍在 drain 的事件出口 channel 集合。一个 session 上可以
	// 同时有多条:user turn 一条,自主续轮 / 后台 subagent 活动轮各自一条。
	// 异步应答(SubmitAnswer / SubmitToolPermission)按 waiter 记下的通道回投,
	// emitTo 只往还在集合里的通道写 —— drain 退出时 detachOut 先摘除、调用方随后
	// 才 close,因此不会向已关闭 channel 写入。
	outMu    sync.Mutex
	liveOuts map[chan<- agentruntime.Event]struct{}

	// tasks 跨 turn 聚合 TaskCreate / TaskUpdate 工具调用为 canonical.PlanUpdate
	// 快照流。drainStream 每帧调 observePreToolUse / observePostToolUse,变更时
	// 合成 agentruntime.PlanUpdated 推 out。详见 task_aggregator.go 头注。
	tasks *taskAggregator
}

// permissionModeSnapshot 读当前 mode 快照。
func (a *claudeActive) permissionModeSnapshot() string {
	a.modeMu.Lock()
	defer a.modeMu.Unlock()
	return a.permissionMode
}

// setPermissionModeSnapshot 写 mode 快照。CLI 侧切换成功后调用
// (drainStream 的 status 帧同步 / Runtime.SetPermissionMode 的主动切换)。
func (a *claudeActive) setPermissionModeSnapshot(mode string) {
	a.modeMu.Lock()
	a.permissionMode = mode
	a.modeMu.Unlock()
}

// enterOutOfBand / leaveOutOfBand 圈住一轮**主线**带外轮(自主续轮)的 drain 期。
// outOfBandActive 供 startup 看门狗判定「此刻没帧是因为帧流被带外轮占着」;
// mainThreadBusy 另外据此判定「此刻插话进得去」(见 Runtime.Steer)。
func (a *claudeActive) enterOutOfBand() {
	a.outOfBand.Add(1)
	a.mainThreadOutOfBand.Add(1)
}

func (a *claudeActive) leaveOutOfBand() {
	a.outOfBand.Add(-1)
	a.mainThreadOutOfBand.Add(-1)
}

// enterSubagentActivity / leaveSubagentActivity 圈住一轮后台 subagent 活动轮的
// drain 期。它同样占着帧流(看门狗要据此暂停计时、空闲清扫要据此认为会话忙),
// 但**不占主线** —— 见 mainThreadOutOfBand 的注释。
func (a *claudeActive) enterSubagentActivity() { a.outOfBand.Add(1) }
func (a *claudeActive) leaveSubagentActivity() { a.outOfBand.Add(-1) }

// outOfBandActive 报告此刻是否有带外轮在流。
func (a *claudeActive) outOfBandActive() bool { return a.outOfBand.Load() > 0 }

// mainThreadBusy 报告 CLI 主线此刻是否正在跑一轮 —— 用户轮(inTurn)或自主续轮。
// 为真时新消息只能插进那一轮(Steer);为假时主线空闲,该起新一轮(Session.Turn)。
func (a *claudeActive) mainThreadBusy() bool {
	return a.inTurn.Load() || a.mainThreadOutOfBand.Load() > 0
}

// Busy 实现池的 sessionBusyReporter:带外轮(自主续轮 / 后台 subagent 活动轮)在流时
// 这条会话正忙。用户轮由池的 active 状态覆盖,带外轮不经过 acquireSession,没有任何
// 人替它 MarkActive —— 只有自报这一条路能让空闲清扫看见它(sess-3244)。
func (a *claudeActive) Busy() bool { return a.outOfBandActive() }

// nextTurnToken 为新一轮入口分配自增 token 并记录为当前活跃轮,返回新 token。
// kind 是这一轮的类型(用户轮 / 自主续轮 / 后台 subagent 活动轮)。
func (a *claudeActive) nextTurnToken(kind agentruntime.TurnKind) uint64 {
	t := a.turnSeq.Add(1)
	a.curTurnToken.Store(t)
	a.curTurnKind.Store(kind)
	return t
}

// currentTurnKind 读当前活跃轮的类型;尚无任何轮记录时返回 TurnKindNone。
func (a *claudeActive) currentTurnKind() agentruntime.TurnKind {
	if v := a.curTurnKind.Load(); v != nil {
		return v.(agentruntime.TurnKind)
	}
	return agentruntime.TurnKindNone
}

// attachOut / detachOut 由 drainStream 圈住一轮的事件出口存活期。
func (a *claudeActive) attachOut(out chan<- agentruntime.Event) {
	if out == nil {
		return
	}
	a.outMu.Lock()
	if a.liveOuts == nil {
		a.liveOuts = make(map[chan<- agentruntime.Event]struct{})
	}
	a.liveOuts[out] = struct{}{}
	a.outMu.Unlock()
}

func (a *claudeActive) detachOut(out chan<- agentruntime.Event) {
	a.outMu.Lock()
	delete(a.liveOuts, out)
	a.outMu.Unlock()
}

// emitTo 把异步应答的终态事件投回发起该请求的那一轮的通道。通道已 detach
// (那一轮 drain 已结束)或缓冲满时丢弃 —— 前端有乐观更新 + 历史回放兜底。
// 持锁发送:detachOut 会等在途发送完成,调用方随后 close 才安全。
func (a *claudeActive) emitTo(out chan<- agentruntime.Event, ev agentruntime.Event) {
	if out == nil {
		return
	}
	a.outMu.Lock()
	defer a.outMu.Unlock()
	if _, live := a.liveOuts[out]; !live {
		return
	}
	select {
	case out <- ev:
	default:
	}
}

// askWaiter 单次 AskUserQuestion 调用的状态。out 是发起该 control_request 的那一轮
// 的事件出口 —— 应答必须回到同一条通道,session 上可能同时有多条(user turn +
// 自主续轮 / 后台 subagent 活动轮)。
type askWaiter struct {
	questions []agentruntime.AskQuestion
	rawInput  json.RawMessage
	out       chan<- agentruntime.Event
}

// permWaiter 单次非-AskUserQuestion 工具审批的状态。out 语义同 askWaiter。
type permWaiter struct {
	toolName string
	rawInput json.RawMessage
	out      chan<- agentruntime.Event
}

// Close 释放 claudeActive 持有的所有资源。幂等。
// PID 交出底层 CLI 子进程号,供池快照把进程与会话对上;拿不到时为 0。
func (a *claudeActive) PID() int {
	if a == nil || a.handle == nil {
		return 0
	}
	if provider, ok := a.handle.(interface{ PID() int }); ok {
		return provider.PID()
	}
	return 0
}

// Kill 硬杀底层子进程(整组 SIGKILL),是 CLISessionPool 在优雅关闭超出宽限期后的
// 升级口。Close 走的是「关 stdin 等 CLI 自己退出」,对卡在 MCP 初始化、根本不读
// stdin 的 CLI 永不返回 —— 那种条目只能靠这一刀收尾。
func (a *claudeActive) Kill(ctx context.Context) error {
	if a == nil || a.handle == nil {
		return nil
	}
	return a.handle.Kill(ctx)
}

func (a *claudeActive) Close(ctx context.Context) error {
	if a.handle != nil {
		_ = a.handle.Close(ctx)
	}
	if a.steer != nil && a.sessionUUID != "" {
		a.steer.Forget(a.sessionUUID)
	}
	a.askMu.Lock()
	a.askWaiters = nil
	a.askMu.Unlock()
	a.permMu.Lock()
	a.permWaiters = nil
	a.permMu.Unlock()
	return nil
}

func (a *claudeActive) registerPermWaiter(reqID, toolName string, rawInput json.RawMessage, out chan<- agentruntime.Event) {
	a.permMu.Lock()
	defer a.permMu.Unlock()
	if a.permWaiters == nil {
		a.permWaiters = make(map[string]*permWaiter)
	}
	a.permWaiters[reqID] = &permWaiter{toolName: toolName, rawInput: rawInput, out: out}
	if a.pool != nil && a.poolKey != "" {
		a.pool.MarkWaiting(a.poolKey)
	}
}

func (a *claudeActive) takePermWaiter(reqID string) *permWaiter {
	a.permMu.Lock()
	defer a.permMu.Unlock()
	w := a.permWaiters[reqID]
	if w != nil {
		delete(a.permWaiters, reqID)
	}
	if a.pool != nil && a.poolKey != "" {
		a.pool.MarkActive(a.poolKey)
	}
	return w
}

func (a *claudeActive) registerAskWaiter(reqID string, questions []agentruntime.AskQuestion, rawInput json.RawMessage, out chan<- agentruntime.Event) {
	a.askMu.Lock()
	defer a.askMu.Unlock()
	if a.askWaiters == nil {
		a.askWaiters = make(map[string]*askWaiter)
	}
	a.askWaiters[reqID] = &askWaiter{questions: questions, rawInput: rawInput, out: out}
	if a.pool != nil && a.poolKey != "" {
		a.pool.MarkWaiting(a.poolKey)
	}
}

func (a *claudeActive) takeAskWaiter(reqID string) *askWaiter {
	a.askMu.Lock()
	defer a.askMu.Unlock()
	w := a.askWaiters[reqID]
	if w != nil {
		delete(a.askWaiters, reqID)
	}
	if a.pool != nil && a.poolKey != "" {
		a.pool.MarkActive(a.poolKey)
	}
	return w
}

// pendingWaiters snapshots every currently-blocked waiter (R7) — the read
// half of registerAskWaiter / registerPermWaiter, whose only prior read
// path was take-by-requestID. Order within each slice is unspecified (map
// iteration); callers needing a stable order sort by RequestID themselves.
func (a *claudeActive) pendingWaiters() agentruntime.WaiterSnapshot {
	var snap agentruntime.WaiterSnapshot

	a.permMu.Lock()
	if len(a.permWaiters) > 0 {
		snap.ToolPermissions = make([]agentruntime.PendingToolPermission, 0, len(a.permWaiters))
		for reqID, w := range a.permWaiters {
			snap.ToolPermissions = append(snap.ToolPermissions, agentruntime.PendingToolPermission{
				RequestID: reqID,
				ToolName:  w.toolName,
				Input:     append([]byte(nil), w.rawInput...),
			})
		}
	}
	a.permMu.Unlock()

	a.askMu.Lock()
	if len(a.askWaiters) > 0 {
		snap.AskUserQuestions = make([]agentruntime.PendingAskUserQuestion, 0, len(a.askWaiters))
		for reqID, w := range a.askWaiters {
			snap.AskUserQuestions = append(snap.AskUserQuestions, agentruntime.PendingAskUserQuestion{
				RequestID: reqID,
				Questions: append([]agentruntime.AskQuestion(nil), w.questions...),
			})
		}
	}
	a.askMu.Unlock()

	return snap
}

// newUUIDv4 generates a v4 UUID without adding a dependency.
func newUUIDv4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		binary.BigEndian.PutUint64(b[0:8], uint64(time.Now().UnixNano()))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// buildHookSettingsJSONString 生成注册 PostToolUse hook 的 settings JSON。
// 返回值直接走 claude CLI 的 --settings <json>(CLI 原生接受 JSON 字符串或文件
// 路径,见 `claude --help`)。
func buildHookSettingsJSONString(executablePath string) (string, error) {
	bin := shellEscapePath(executablePath)
	settings := map[string]any{
		"hooks": map[string]any{
			"PostToolUse": []any{map[string]any{
				"matcher": "",
				"hooks": []any{map[string]any{
					"type":    "command",
					"command": bin + " claudecode hook post-tool",
				}},
			}},
		},
	}
	body, err := json.Marshal(settings)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func shellEscapePath(s string) string {
	if !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// projectsRoot is claude CLI's default project root, overridable via
// AGENTRE_CLAUDE_PROJECTS_DIR. Used by acquireSession to read JSONL for
// UserAnchor extraction.
func projectsRoot() string {
	if env := strings.TrimSpace(os.Getenv("AGENTRE_CLAUDE_PROJECTS_DIR")); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

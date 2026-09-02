package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/agents/provider"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/agentre-hub/agentre/internal/daemon/connection"
	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/handlers/mock_handlers"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	piagentrt "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/piagent"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// ── fake Runtimes ───────────────────────────────────────────────────────────
//
// fullRT implements agentruntime.Runtime + ALL 8 optional sub-interfaces.
// Use for happy-path tests. bareRT implements only Runtime so the handler
// hits its "type assert failed → ErrUnsupported" branch.

type runCall struct {
	req agentruntime.RunRequest
}

type fullRT struct {
	mu sync.Mutex

	cap capability.Capabilities

	runFn   func(ctx context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error)
	runReqs []runCall

	// autoFn 提供 AutonomousTurns(sid) 的返回 channel;nil → 立即 close(不转发)。
	autoFn func(sid int64) <-chan agentruntime.AutonomousTurn

	steerErr   error
	steerCalls []steerCall

	cancelSteerFn   func(int64, string) ([]string, error)
	cancelSteerArgs []cancelSteerCall

	drainFn   func(int64) []agentruntime.ConsumedSteer
	drainArgs []int64

	abortErr     error
	abortCalls   []int64
	abortTokens  []uint64
	abortOutcome agentruntime.AbortOutcome

	stopBgErr   error
	stopBgCalls []stopBgCall

	setModeErr   error
	setModeCalls []setModeCall

	submitAnswerErr   error
	submitAnswerCalls []submitAnswerCall

	submitToolPermErr   error
	submitToolPermCalls []submitToolPermCall

	pendingWaiters agentruntime.WaiterSnapshot

	goalErr        error
	getGoalCalls   []goalCall
	setGoalCalls   []goalCall
	clearGoalCalls []goalCall
}

type steerCall struct {
	sid      int64
	queuedID string
	text     string
}

type cancelSteerCall struct {
	sid      int64
	queuedID string
}

type setModeCall struct {
	sid  int64
	mode string
}

type stopBgCall struct {
	sid    int64
	taskID string
}

type submitAnswerCall struct {
	sid       int64
	requestID string
	questions []agentruntime.AskQuestion
	answers   []agentruntime.AskAnswer
	skipped   bool
}

type submitToolPermCall struct {
	sid                int64
	requestID          string
	allow              bool
	alwaysAllowSession bool
	denyReason         string
}

type goalCall struct {
	req agentruntime.GoalRequest
}

// eventText 把一条密封事件还原成它在 wire 上的 JSON 文本 —— 这些用例断言的是
// 「转发出去的整串内容」,不关心具体事件类型。
func eventText(t *testing.T, event agentruntime.Event) string {
	t.Helper()
	raw, err := json.Marshal(event)
	require.NoError(t, err)
	return string(raw)
}

func (r *fullRT) Capabilities() capability.Capabilities { return r.cap }

func (r *fullRT) AutonomousTurns(sid int64) <-chan agentruntime.AutonomousTurn {
	r.mu.Lock()
	fn := r.autoFn
	r.mu.Unlock()
	if fn == nil {
		ch := make(chan agentruntime.AutonomousTurn)
		close(ch)
		return ch
	}
	return fn(sid)
}

func (r *fullRT) Run(ctx context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	r.mu.Lock()
	r.runReqs = append(r.runReqs, runCall{req: req})
	fn := r.runFn
	r.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	ch := make(chan agentruntime.Event)
	close(ch)
	return ch, &agentruntime.RunResult{}, nil
}

func (r *fullRT) Steer(_ context.Context, sid int64, queuedID, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steerCalls = append(r.steerCalls, steerCall{sid, queuedID, text})
	return r.steerErr
}

func (r *fullRT) CancelSteer(_ context.Context, sid int64, queuedID string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancelSteerArgs = append(r.cancelSteerArgs, cancelSteerCall{sid, queuedID})
	if r.cancelSteerFn != nil {
		return r.cancelSteerFn(sid, queuedID)
	}
	return nil, nil
}

func (r *fullRT) DrainPending(_ context.Context, sid int64) []agentruntime.ConsumedSteer {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.drainArgs = append(r.drainArgs, sid)
	if r.drainFn != nil {
		return r.drainFn(sid)
	}
	return nil
}

func (r *fullRT) Abort(_ context.Context, sid int64, turnToken uint64) (agentruntime.AbortOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.abortCalls = append(r.abortCalls, sid)
	r.abortTokens = append(r.abortTokens, turnToken)
	return r.abortOutcome, r.abortErr
}

func (r *fullRT) StopBackgroundTask(_ context.Context, sid int64, taskID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopBgCalls = append(r.stopBgCalls, stopBgCall{sid, taskID})
	return r.stopBgErr
}

func (r *fullRT) SetPermissionMode(_ context.Context, sid int64, mode string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setModeCalls = append(r.setModeCalls, setModeCall{sid, mode})
	return r.setModeErr
}

func (r *fullRT) SubmitAnswer(_ context.Context, sid int64, requestID string, questions []agentruntime.AskQuestion, answers []agentruntime.AskAnswer, skipped bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.submitAnswerCalls = append(r.submitAnswerCalls, submitAnswerCall{sid, requestID, questions, answers, skipped})
	return r.submitAnswerErr
}

func (r *fullRT) SubmitToolPermission(_ context.Context, sid int64, requestID string, allow, always bool, deny string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.submitToolPermCalls = append(r.submitToolPermCalls, submitToolPermCall{sid, requestID, allow, always, deny})
	return r.submitToolPermErr
}

func (r *fullRT) PendingWaiters(_ context.Context, _ int64) agentruntime.WaiterSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pendingWaiters
}

func (r *fullRT) GetGoal(_ context.Context, req agentruntime.GoalRequest) (*agentruntime.Goal, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.getGoalCalls = append(r.getGoalCalls, goalCall{req: req})
	return &agentruntime.Goal{ThreadID: req.ProviderSessionID, Objective: "ship goal rpc", Status: "active"}, r.goalErr
}

func (r *fullRT) SetGoal(_ context.Context, req agentruntime.GoalRequest) (*agentruntime.Goal, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setGoalCalls = append(r.setGoalCalls, goalCall{req: req})
	return &agentruntime.Goal{ThreadID: req.ProviderSessionID, Objective: "ship goal rpc", Status: "active"}, r.goalErr
}

func (r *fullRT) ClearGoal(_ context.Context, req agentruntime.GoalRequest) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearGoalCalls = append(r.clearGoalCalls, goalCall{req: req})
	return true, r.goalErr
}

// bareRT only implements Runtime — type-asserting any sub-interface fails.
type bareRT struct{}

func (bareRT) Capabilities() capability.Capabilities { return capability.Capabilities{} }
func (bareRT) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	ch := make(chan agentruntime.Event)
	close(ch)
	return ch, &agentruntime.RunResult{}, nil
}

// recordingOutbound 扮演会话通知出口的两半:通知日志(handlers.JournalPort)与推送
// (handlers.NotifierPort)。两半合在一个 fake 里是为了共用一份有序 steps —— 「先落库
// 后推送」只有在同一条时间线上才证得了:每条 notify 之前必须有它自己那条 append。
type recordingOutbound struct {
	mu      sync.Mutex
	frames  []notifyFrame
	rows    []journalRow
	steps   []string
	resolve []string // NotifyFor 每次解析推送目标时收到的对端指纹
	nextSeq map[string]int64

	// appendFail 返回非 nil 时该条通知落库失败:不记行、不分配 seq。
	appendFail func(method string, payload json.RawMessage) error
	// notifyFail 返回非 nil 时该条通知推送失败(连接已死 / 写超时)。
	notifyFail func(method string) error
	// offline 为真时解析不到活连接(对端不在线),推送无目标。
	offline bool

	notifyC chan struct{}
}

// notifyFrame 是推出去的一条通知。notification 是端口实际收到的那条 Protobuf 消息;
// params 是把它翻回 wire 形状之后的帧,断言读起来仍是领域语言(而且顺带证明推出去的
// 那条消息本身是完整、可解的)。
type notifyFrame struct {
	method       string
	params       any
	notification *agentrewire.RpcNotification
}

// journalRow 是 fake 日志里的一行,形状对齐 daemon_notification_logs。
// blob 是落库的原始字节(不含 seq),payload 是它翻回 wire 形状后的 JSON。
type journalRow struct {
	peer    string
	session string
	method  string
	payload string
	blob    []byte
	seq     int64
}

func newRecordingOutbound() *recordingOutbound {
	return &recordingOutbound{notifyC: make(chan struct{}, 64), nextSeq: map[string]int64{}}
}

// Append 模拟仓储的原子 seq 分配:每个 (对端, 会话) 从 1 起递增,失败时不推进。
func (n *recordingOutbound) Append(_ context.Context, peer, session string, payload []byte) (int64, error) {
	n.mu.Lock()
	defer n.unlockAndSignal()
	notification, err := protowire.DecodeNotification(payload)
	if err != nil {
		return 0, err
	}
	method, value, err := protowire.ProtoNotificationToWire(notification)
	if err != nil {
		return 0, err
	}
	params, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	if n.appendFail != nil {
		if err := n.appendFail(method, params); err != nil {
			n.steps = append(n.steps, "append-failed:"+method)
			return 0, err
		}
	}
	key := peer + "|" + session
	n.nextSeq[key]++
	seq := n.nextSeq[key]
	n.rows = append(n.rows, journalRow{
		peer: peer, session: session, method: method, payload: string(params),
		blob: append([]byte(nil), payload...), seq: seq,
	})
	n.steps = append(n.steps, stepKey("append", method, seq))
	return seq, nil
}

// notifierFor 是注入给 handlers.RuntimeDeps.NotifyFor 的解析函数。
func (n *recordingOutbound) notifierFor(peer string) handlers.NotifierPort {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.resolve = append(n.resolve, peer)
	if n.offline {
		return nil
	}
	return n
}

// resolvedPeers 返回 NotifyFor 每次被调用时收到的对端指纹,按调用顺序。
func (n *recordingOutbound) resolvedPeers() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.resolve...)
}

func (n *recordingOutbound) setOffline(off bool) {
	n.mu.Lock()
	n.offline = off
	n.mu.Unlock()
}

func (n *recordingOutbound) Notify(notification *agentrewire.RpcNotification) error {
	method, value, err := protowire.ProtoNotificationToWire(notification)
	if err != nil {
		return err
	}
	seq := protowire.NotificationSeq(notification)
	n.mu.Lock()
	defer n.unlockAndSignal()
	if n.notifyFail != nil {
		if err := n.notifyFail(method); err != nil {
			n.steps = append(n.steps, stepKey("notify-failed", method, seq))
			return err
		}
	}
	n.frames = append(n.frames, notifyFrame{method: method, params: framePointer(value), notification: notification})
	n.steps = append(n.steps, stepKey("notify", method, seq))
	return nil
}

func (*recordingOutbound) Request(_ context.Context, _ string, _ any, _ any) error { return nil }

// unlockAndSignal 解锁并唤醒 waitFrames / waitSteps 的等待者(非阻塞)。
func (n *recordingOutbound) unlockAndSignal() {
	n.mu.Unlock()
	select {
	case n.notifyC <- struct{}{}:
	default:
	}
}

// stepKey 把一步记成 "<动作>:<method>#<seq>",让 R1 的顺序断言能按 (method, seq)
// 精确配对,而不是只数条数。
func stepKey(action, method string, seq int64) string {
	return fmt.Sprintf("%s:%s#%d", action, method, seq)
}

// frameSeq 读出推送帧上盖的 seq。
func frameSeq(params any) int64 {
	switch f := params.(type) {
	case *wire.EventFrame:
		return f.Seq
	case *wire.RunResultDoneFrame:
		return f.Seq
	case *wire.AutonomousTurnStartedFrame:
		return f.Seq
	}
	return -1
}

// framePointer 把 ProtoNotificationToWire 交回的值形帧换成指针形 —— 用例一律按指针
// 断言(生产上推的也是指针帧)。
func framePointer(value any) any {
	switch v := value.(type) {
	case wire.EventFrame:
		return &v
	case wire.RunResultDoneFrame:
		return &v
	case wire.AutonomousTurnStartedFrame:
		return &v
	}
	return value
}

func (n *recordingOutbound) snapshot() []notifyFrame {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]notifyFrame, len(n.frames))
	copy(out, n.frames)
	return out
}

func (n *recordingOutbound) journalRows() []journalRow {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]journalRow, len(n.rows))
	copy(out, n.rows)
	return out
}

func (n *recordingOutbound) stepLog() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.steps...)
}

// waitFrames blocks until n.snapshot() yields at least want frames, or test fails.
func (n *recordingOutbound) waitFrames(t *testing.T, want int) []notifyFrame {
	t.Helper()
	n.waitFor(t, func() bool { return len(n.snapshot()) >= want },
		func() string { return fmt.Sprintf("%d notify frames; got %d", want, len(n.snapshot())) })
	return n.snapshot()
}

// waitSteps 等到出口上至少发生了 want 步(落库 / 推送,含失败),用于推送必然失败、
// 等不到 frame 的场景。
func (n *recordingOutbound) waitSteps(t *testing.T, want int) []string {
	t.Helper()
	n.waitFor(t, func() bool { return len(n.stepLog()) >= want },
		func() string { return fmt.Sprintf("%d outbound steps; got %v", want, n.stepLog()) })
	return n.stepLog()
}

func (n *recordingOutbound) waitFor(t *testing.T, done func() bool, describe func() string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for !done() {
		select {
		case <-n.notifyC:
		case <-deadline:
			t.Fatalf("timed out waiting for %s", describe())
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

type sessionKey struct{ peer, session string }

// recordingSessions 扮演会话生命周期的写入口(handlers.SessionLifecyclePort)与读出口
// (handlers.SessionQueryPort)—— 生产上也是同一个仓储的两侧(daemon.daemonSessionStore)。
// 它记一条有序 steps,因为生命周期的价值全在**次序**上:起手 running、轮末 idle,顺序
// 颠倒的实现会让清单永远报错状态;同一批会话行也按这个次序推进,供读侧查。
type recordingSessions struct {
	mu      sync.Mutex
	starts  []handlers.SessionRecord
	log     []string
	stepC   chan struct{}
	rows    map[sessionKey]handlers.SessionRecord
	findErr error
	// finishEntered / finishHold 让用例把 fanout 卡在轮末收尾里,见 holdFinish。
	finishEntered chan struct{}
	finishHold    chan struct{}
}

func newRecordingSessions() *recordingSessions {
	return &recordingSessions{stepC: make(chan struct{}, 64), rows: map[sessionKey]handlers.SessionRecord{}}
}

func (s *recordingSessions) Start(_ context.Context, rec handlers.SessionRecord) error {
	s.mu.Lock()
	defer s.unlockAndSignal()
	s.starts = append(s.starts, rec)
	s.log = append(s.log, "start:"+rec.PeerSessionID)
	s.rows[sessionKey{peer: rec.PeerFingerprint, session: rec.PeerSessionID}] = rec
	return nil
}

func (s *recordingSessions) Running(_ context.Context, peer, session string) error {
	s.mu.Lock()
	defer s.unlockAndSignal()
	s.log = append(s.log, "running:"+session)
	s.setLifecycleLocked(peer, session, wire.SessionLifecycleRunning)
	return nil
}

func (s *recordingSessions) Finish(_ context.Context, peer, session string) error {
	s.awaitFinishRelease()
	s.mu.Lock()
	defer s.unlockAndSignal()
	s.log = append(s.log, "finish:"+session)
	s.setLifecycleLocked(peer, session, wire.SessionLifecycleIdle)
	return nil
}

// holdFinish 把 fanout 卡在**轮末收尾**那一瞬间(这一轮已经结束,而生命周期行还没落回
// idle)。这一瞬间在生产上不是一闪而过:Finish 是一次同步的 SQLite 写,与流式落库抢锁
// 时能拖到几十毫秒以上。返回「已进入 Finish」的信号与放行函数。
func (s *recordingSessions) holdFinish() (<-chan struct{}, func()) {
	entered, hold := make(chan struct{}, 1), make(chan struct{})
	s.mu.Lock()
	s.finishEntered, s.finishHold = entered, hold
	s.mu.Unlock()
	return entered, sync.OnceFunc(func() { close(hold) })
}

// awaitFinishRelease 是 holdFinish 的受理侧:先报「进来了」,再等放行。没装闸时直接过。
func (s *recordingSessions) awaitFinishRelease() {
	s.mu.Lock()
	entered, hold := s.finishEntered, s.finishHold
	s.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if hold != nil {
		<-hold
	}
}

// Find / List 让 recordingSessions 同时充当**查询出口**(生产上是同一个仓储的两侧,
// 见 daemon.daemonSessionStore):Start / Running / Finish 按同样的语义推进这批行,
// 提交决策解不出会话时读的就是它们。
func (s *recordingSessions) Find(_ context.Context, peer, session string) (*handlers.SessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.findErr != nil {
		return nil, s.findErr
	}
	row, ok := s.rows[sessionKey{peer: peer, session: session}]
	if !ok {
		return nil, nil
	}
	return &row, nil
}

func (s *recordingSessions) List(_ context.Context, peer, _ string) ([]handlers.SessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []handlers.SessionRecord
	for k, row := range s.rows {
		if k.peer == peer {
			out = append(out, row)
		}
	}
	return out, nil
}

// setLifecycle 直接摆一条会话行的生命周期状态。interrupted 只由 daemon 启动清扫
// 写(R10),进程内没有触发点,只能这么造。
func (s *recordingSessions) setLifecycle(peer, session, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setLifecycleLocked(peer, session, state)
}

func (s *recordingSessions) setLifecycleLocked(peer, session, state string) {
	k := sessionKey{peer: peer, session: session}
	row := s.rows[k]
	row.PeerFingerprint, row.PeerSessionID, row.LifecycleState = peer, session, state
	s.rows[k] = row
}

func (s *recordingSessions) unlockAndSignal() {
	s.mu.Unlock()
	select {
	case s.stepC <- struct{}{}:
	default:
	}
}

func (s *recordingSessions) steps() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.log...)
}

func (s *recordingSessions) started() []handlers.SessionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]handlers.SessionRecord(nil), s.starts...)
}

func (s *recordingSessions) waitSteps(t *testing.T, want int) []string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for len(s.steps()) < want {
		select {
		case <-s.stepC:
		case <-deadline:
			t.Fatalf("timed out waiting for %d lifecycle steps; got %v", want, s.steps())
		}
	}
	return s.steps()
}

func countStep(steps []string, want string) int {
	n := 0
	for _, s := range steps {
		if s == want {
			n++
		}
	}
	return n
}

type lockedLogBuffer struct {
	mu   sync.Mutex
	b    bytes.Buffer
	logs *observer.ObservedLogs
}

func (b *lockedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var combined strings.Builder
	combined.WriteString(b.b.String())
	if b.logs != nil {
		for _, entry := range b.logs.All() {
			combined.WriteString(entry.Message)
			_, _ = fmt.Fprint(&combined, entry.ContextMap())
		}
	}
	return combined.String()
}

func captureRuntimeLogs(t *testing.T) *lockedLogBuffer {
	t.Helper()
	oldWriter := log.Writer()
	oldFlags := log.Flags()
	oldPrefix := log.Prefix()
	core, observed := observer.New(zapcore.DebugLevel)
	oldLogger := logger.Default()
	logger.SetLogger(zap.New(core))
	captured := &lockedLogBuffer{logs: observed}
	log.SetOutput(captured)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		logger.SetLogger(oldLogger)
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
		log.SetPrefix(oldPrefix)
	})
	return captured
}

func setupRuntimeTest(t *testing.T, rt agentruntime.Runtime) (
	context.Context,
	*recordingOutbound,
	*mock_handlers.MockGatewayPort,
	*mock_handlers.MockLLMProviderLookupPort,
	*handlers.RuntimeHandlers,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	notif := newRecordingOutbound()
	gw := mock_handlers.NewMockGatewayPort(ctrl)
	lookup := mock_handlers.NewMockLLMProviderLookupPort(ctrl)
	sess := newRecordingSessions()
	h := handlers.NewRuntimeHandlers(handlers.RuntimeDeps{
		NotifyFor:    notif.notifierFor,
		Journal:      notif,
		Sessions:     sess,
		SessionQuery: sess,
		Gateway:      gw,
		Lookup:       lookup,
		RuntimeFor: func(_ agent_backend_entity.BackendType) agentruntime.Runtime {
			return rt
		},
	})
	return context.Background(), notif, gw, lookup, h
}

func setupRuntimeTestWithCLIOverlay(t *testing.T, rt agentruntime.Runtime,
	resolve func(string) (string, bool),
) (context.Context, *recordingOutbound, *handlers.RuntimeHandlers) {
	t.Helper()
	notif := newRecordingOutbound()
	sess := newRecordingSessions()
	h := handlers.NewRuntimeHandlers(handlers.RuntimeDeps{
		NotifyFor: notif.notifierFor, Journal: notif, Sessions: sess, SessionQuery: sess,
		RuntimeFor:        func(agent_backend_entity.BackendType) agentruntime.Runtime { return rt },
		CLIPathForBackend: resolve,
	})
	return context.Background(), notif, h
}

// setupRuntimeTestWithSessions 同 setupRuntimeTest,但把会话生命周期出口也交回来
// 供断言用(其余用例不关心它,免得每个都多接一个返回值)。
func setupRuntimeTestWithSessions(t *testing.T, rt agentruntime.Runtime) (
	context.Context,
	*recordingOutbound,
	*recordingSessions,
	*handlers.RuntimeHandlers,
) {
	t.Helper()
	notif := newRecordingOutbound()
	sess := newRecordingSessions()
	h := newRuntimeHandlersOn(rt, sess, notif)
	return context.Background(), notif, sess, h
}

// newRuntimeHandlersOn 造一个共用**同一批会话行**、但内存会话表各自独立的 handler ——
// 生产上每条连接的 bindConn 都这么造一个(见 daemon.bindConn),而它们背后是同一个
// Daemon 级的会话仓储。判别「轮次真的结束了」与「这个 handler 从没拥有过它」需要的
// 正是这个形状。
func newRuntimeHandlersOn(rt agentruntime.Runtime, sess *recordingSessions, notif *recordingOutbound) *handlers.RuntimeHandlers {
	return handlers.NewRuntimeHandlers(handlers.RuntimeDeps{
		NotifyFor:    notif.notifierFor,
		Journal:      notif,
		Sessions:     sess,
		SessionQuery: sess,
		RuntimeFor: func(_ agent_backend_entity.BackendType) agentruntime.Runtime {
			return rt
		},
	})
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func backendJSON(t *testing.T, be agent_backend_entity.AgentBackend) json.RawMessage {
	return mustJSON(t, be)
}

// ── Capabilities ────────────────────────────────────────────────────────────

func TestRuntime_Run_GivenAccountCLIOverlay_WhenExecuting_ThenUsesOverlayWithoutPersistingItInBackendIdentity(t *testing.T) {
	rt := &fullRT{}
	ctx, notif, h := setupRuntimeTestWithCLIOverlay(t, rt, func(syncID string) (string, bool) {
		assert.Equal(t, "backend-sync-1", syncID)
		return "/private/bin/claude", true
	})
	be := agent_backend_entity.AgentBackend{
		Type: string(agent_backend_entity.TypeClaudeCode), CLIPath: "/desktop/bin/claude",
	}
	be.SyncID = "backend-sync-1"

	_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(71)})
	require.NoError(t, err)
	_ = notif.waitFrames(t, 1)
	rt.mu.Lock()
	runReqs := append([]runCall(nil), rt.runReqs...)
	rt.mu.Unlock()
	require.Len(t, runReqs, 1)
	assert.Equal(t, "/private/bin/claude", runReqs[0].req.Backend.CLIPath)
	assert.Equal(t, "/desktop/bin/claude", be.CLIPath, "the overlay is applied to the execution copy only")
}

func TestRuntime_Run_GivenNoSuccessfulAccountSnapshot_WhenExecuting_ThenKeepsPairedDesktopCLIPath(t *testing.T) {
	rt := &fullRT{}
	ctx, notif, h := setupRuntimeTestWithCLIOverlay(t, rt, func(string) (string, bool) { return "", false })
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode), CLIPath: "/paired/bin/claude"}

	_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(72)})
	require.NoError(t, err)
	_ = notif.waitFrames(t, 1)
	rt.mu.Lock()
	runReqs := append([]runCall(nil), rt.runReqs...)
	rt.mu.Unlock()
	require.Len(t, runReqs, 1)
	assert.Equal(t, "/paired/bin/claude", runReqs[0].req.Backend.CLIPath,
		"unclaimed daemons and pre-snapshot paired desktop calls keep their existing execution path")
}

func TestRuntime_Capabilities_Found(t *testing.T) {
	rt := &fullRT{cap: capability.Capabilities{}}
	ctx, _, _, _, h := setupRuntimeTest(t, rt)

	out, err := h.Capabilities(ctx, wire.CapabilitiesParams{BackendType: string(agent_backend_entity.TypeClaudeCode)})
	require.NoError(t, err)
	assert.Equal(t, rt.cap, out.Capabilities)
}

func TestRuntime_Capabilities_Unknown(t *testing.T) {
	ctx, _, _, _, h := setupRuntimeTest(t, nil)
	_, err := h.Capabilities(ctx, wire.CapabilitiesParams{BackendType: "nope"})
	require.Error(t, err)
}

func TestRuntime_GoalRoutesWithoutActiveTurn(t *testing.T) {
	rt := &fullRT{}
	ctx, _, _, _, h := setupRuntimeTest(t, rt)
	objective := "ship goal rpc"
	status := "active"
	budget := 123
	params := wire.GoalParams{
		ConversationID:    convID(42),
		AgentID:           7,
		ProviderSessionID: "thread-goal",
		Backend:           backendJSON(t, agent_backend_entity.AgentBackend{ID: 3, Type: string(agent_backend_entity.TypeCodex), Name: "codex"}),
		Cwd:               "/tmp/work",
		Objective:         &objective,
		Status:            &status,
		TokenBudget:       &budget,
	}

	setOut, err := h.SetGoal(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, setOut.Goal)
	assert.Equal(t, "thread-goal", setOut.Goal.ThreadID)
	require.Len(t, rt.setGoalCalls, 1)
	setReq := rt.setGoalCalls[0].req
	assert.Equal(t, handlers.RuntimeSessionKey(convID(42)), setReq.SessionID)
	assert.Equal(t, int64(7), setReq.AgentID)
	assert.Equal(t, "thread-goal", setReq.ProviderSessionID)
	assert.Equal(t, "/tmp/work", setReq.Cwd)
	require.NotNil(t, setReq.Backend)
	assert.Equal(t, string(agent_backend_entity.TypeCodex), setReq.Backend.Type)
	require.NotNil(t, setReq.Objective)
	assert.Equal(t, "ship goal rpc", *setReq.Objective)
	require.NotNil(t, setReq.Status)
	assert.Equal(t, "active", *setReq.Status)
	require.NotNil(t, setReq.TokenBudget)
	assert.Equal(t, 123, *setReq.TokenBudget)

	getOut, err := h.GetGoal(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, getOut.Goal)
	assert.Equal(t, "thread-goal", getOut.Goal.ThreadID)
	require.Len(t, rt.getGoalCalls, 1)
	require.NotNil(t, rt.getGoalCalls[0].req.Backend)
	assert.Equal(t, string(agent_backend_entity.TypeCodex), rt.getGoalCalls[0].req.Backend.Type)

	clearOut, err := h.ClearGoal(ctx, params)
	require.NoError(t, err)
	assert.True(t, clearOut.Cleared)
	require.Len(t, rt.clearGoalCalls, 1)
	require.NotNil(t, rt.clearGoalCalls[0].req.Backend)
	assert.Equal(t, string(agent_backend_entity.TypeCodex), rt.clearGoalCalls[0].req.Backend.Type)
}

func TestRuntime_GoalWithProviderUsesDaemonProviderAndGateway(t *testing.T) {
	rt := &fullRT{}
	ctx, _, gw, lookup, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{
		ID:             3,
		Type:           string(agent_backend_entity.TypeCodex),
		Name:           "codex",
		LLMProviderKey: "provider-key",
	}
	lookup.EXPECT().FindByKey(ctx, "provider-key").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "provider-key",
		Type:        string(llm_provider_entity.TypeOpenAIResponse),
		Status:      consts.ACTIVE,
	}, nil)
	lookup.EXPECT().ResolveModel(ctx, "provider-key", "").Return(handlers.EffectiveModel{ModelKey: "", ModelID: "gpt-5-codex"}, nil)
	gw.EXPECT().URL().Return("http://127.0.0.1:12345")
	gw.EXPECT().IssueToken(ctx, gomock.Any(), time.Hour).DoAndReturn(
		func(_ context.Context, got *agent_backend_entity.AgentBackend, _ time.Duration) (string, error) {
			assert.Equal(t, int64(3), got.ID)
			assert.Equal(t, "provider-key", got.LLMProviderKey)
			return "goal-token", nil
		})
	gw.EXPECT().RevokeToken("goal-token")

	_, err := h.GetGoal(ctx, wire.GoalParams{
		ConversationID:    convID(42),
		AgentID:           7,
		ProviderSessionID: "thread-goal",
		Backend:           backendJSON(t, be),
	})
	require.NoError(t, err)
	require.Len(t, rt.getGoalCalls, 1)
	req := rt.getGoalCalls[0].req
	require.NotNil(t, req.Provider)
	assert.Equal(t, "provider-key", req.Provider.ProviderKey)
	require.NotNil(t, req.Effective)
	assert.Equal(t, "gpt-5-codex", req.Effective.ModelID)
	assert.Equal(t, "http://127.0.0.1:12345", req.GatewayURL)
	assert.Equal(t, "goal-token", req.GatewayToken)
}

func TestRuntime_GoalMissingBackendReturnsNoActiveTurn(t *testing.T) {
	rt := &fullRT{}
	ctx, _, _, _, h := setupRuntimeTest(t, rt)

	_, err := h.GetGoal(ctx, wire.GoalParams{ConversationID: convID(42), ProviderSessionID: "thread-goal"})
	require.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
}

// ── Run ─────────────────────────────────────────────────────────────────────

func TestRuntime_Run_NoProvider_EmitsEventsAndDone(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event, 3)
		ch <- agentruntime.TextDelta{Text: "hi"}
		ch <- agentruntime.Done{}
		close(ch)
		return ch, &agentruntime.RunResult{
			ProviderSessionID: "psid-1",
			Model:             "claude-sonnet-4-6",
			ContextWindow:     200000,
		}, nil
	}
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)

	be := agent_backend_entity.AgentBackend{ID: 1, Type: string(agent_backend_entity.TypePiAgent), Name: "x"}
	ack, err := h.Run(ctx, wire.RunParams{
		Backend:        backendJSON(t, be),
		ConversationID: convID(42),
		AgentID:        7,
		Cwd:            "/tmp",
		UserText:       "hello",
		Compact:        true,
		EnabledPlugins: map[string]bool{"browser@openai-bundled": true},
	})
	require.NoError(t, err)
	assert.Equal(t, convID(42), ack.ConversationID)
	assert.Equal(t, "psid-1", ack.ProviderSessionID)
	require.Len(t, rt.runReqs, 1)
	assert.True(t, rt.runReqs[0].req.Compact)
	assert.Equal(t, map[string]bool{"browser@openai-bundled": true}, rt.runReqs[0].req.EnabledPlugins)

	// 2 events + 1 runResultDone = 3 frames expected.
	frames := notif.waitFrames(t, 3)

	assert.Equal(t, wire.NotifyEvent, frames[0].method)
	assert.Equal(t, wire.NotifyEvent, frames[1].method)
	assert.Equal(t, wire.NotifyRunResultDone, frames[2].method)

	// First event frame must carry sessionId 42 and a text_delta event payload.
	ef0, ok := frames[0].params.(*wire.EventFrame)
	require.True(t, ok, "expected wire.EventFrame, got %T", frames[0].params)
	assert.Equal(t, convID(42), ef0.ConversationID)
	assert.Equal(t, agentruntime.TextDelta{Text: "hi"}, ef0.Event)

	// Final frame carries the RunResult fields.
	done, ok := frames[2].params.(*wire.RunResultDoneFrame)
	require.True(t, ok, "expected wire.RunResultDoneFrame, got %T", frames[2].params)
	assert.Equal(t, convID(42), done.ConversationID)
	assert.Equal(t, "psid-1", done.ProviderSessionID)
	assert.Equal(t, "claude-sonnet-4-6", done.Model)
	assert.Equal(t, 200000, done.ContextWindow)
	assert.Empty(t, done.StopErrMsg)
	assert.Zero(t, done.StopErrCode)

	// Session must be cleared after fanout finishes so subsequent Steer
	// returns ErrNoActiveTurn — exercised by a follow-up call.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err = h.Steer(ctx, wire.SteerParams{ConversationID: convID(42), Text: "late"})
		assert.Error(c, err)
	}, time.Second, 10*time.Millisecond)
	assert.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
}

func TestRuntime_Run_GivenRPCContextEndsAfterAck_ThenNonPiTurnKeepsDelivering(t *testing.T) {
	events := make(chan agentruntime.Event, 2)
	var runCtx context.Context
	rt := &fullRT{}
	rt.runFn = func(ctx context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		runCtx = ctx
		return events, &agentruntime.RunResult{}, nil
	}
	baseCtx, notif, _, _, h := setupRuntimeTest(t, rt)
	rpcCtx, cancelRPC := context.WithCancel(baseCtx)

	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}
	_, err := h.Run(rpcCtx, wire.RunParams{
		Backend:        backendJSON(t, be),
		ConversationID: convID(43),
		UserText:       "hello",
	})
	require.NoError(t, err)
	cancelRPC() // Protobuf writes the ACK, then ends the per-request context.

	select {
	case <-runCtx.Done():
		t.Fatal("runtime turn context ended with the acknowledged RPC")
	default:
	}
	events <- agentruntime.TextDelta{Text: "assistant reply"}
	events <- agentruntime.Done{}
	close(events)

	frames := notif.waitFrames(t, 3) // assistant + done + terminal result
	assert.Equal(t, agentruntime.TextDelta{Text: "assistant reply"}, frames[0].params.(*wire.EventFrame).Event)
	assert.Equal(t, wire.NotifyRunResultDone, frames[2].method)
}

func TestRuntime_Run_ForwardsContentBearingEventsWithoutLoggingPayload(t *testing.T) {
	const (
		inputSentinel   = "SENTINEL_DAEMON_TOOL_INPUT"
		resultSentinel  = "SENTINEL_DAEMON_TOOL_RESULT"
		metaSentinel    = "SENTINEL_DAEMON_TOOL_META"
		taskSentinel    = "SENTINEL_DAEMON_SUBAGENT_TASK"
		summarySentinel = "SENTINEL_DAEMON_SUBAGENT_SUMMARY"
		runErrSentinel  = "SENTINEL_DAEMON_SUBAGENT_RUN_ERROR"
		stopErrSentinel = "SENTINEL_DAEMON_STOP_ERROR"
	)
	events := []agentruntime.Event{
		agentruntime.ToolCall{ID: "outer-safe-id", Name: "subagent", Input: json.RawMessage(`{"task":"` + inputSentinel + `"}`)},
		agentruntime.ToolResult{ToolCallID: "outer-safe-id", Content: resultSentinel, Meta: json.RawMessage(`{"detail":"` + metaSentinel + `"}`)},
		agentruntime.SubagentStarted{ToolCallID: "outer-safe-id", Info: agentruntime.SubagentInfo{
			Mode: "parallel", Runs: []agentruntime.SubagentRun{{ID: "run-safe-1", Task: taskSentinel, Status: "running"}},
		}},
		agentruntime.SubagentProgress{ToolCallID: "outer-safe-id", Info: agentruntime.SubagentInfo{
			Mode: "parallel", Runs: []agentruntime.SubagentRun{{ID: "run-safe-1", Task: taskSentinel, Status: "completed", Summary: summarySentinel}},
		}},
		agentruntime.SubagentDone{ToolCallID: "outer-safe-id", Info: agentruntime.SubagentInfo{
			Mode: "parallel", Runs: []agentruntime.SubagentRun{{ID: "run-safe-1", Task: taskSentinel, Status: "failed", Summary: summarySentinel, ErrorMessage: runErrSentinel}},
		}},
	}
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event, len(events))
		for _, event := range events {
			ch <- event
		}
		close(ch)
		return ch, &agentruntime.RunResult{StopErr: errors.New(stopErrSentinel)}, nil
	}
	captured := captureRuntimeLogs(t)
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)

	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)}
	_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(142)})
	require.NoError(t, err)
	frames := notif.waitFrames(t, len(events)+1)

	var forwarded strings.Builder
	for _, frame := range frames {
		switch params := frame.params.(type) {
		case wire.EventFrame:
			forwarded.WriteString(eventText(t, params.Event))
		case *wire.EventFrame:
			forwarded.WriteString(eventText(t, params.Event))
		case wire.RunResultDoneFrame:
			forwarded.WriteString(params.StopErrMsg)
		case *wire.RunResultDoneFrame:
			forwarded.WriteString(params.StopErrMsg)
		}
	}
	for _, sentinel := range []string{inputSentinel, resultSentinel, metaSentinel, taskSentinel, summarySentinel, runErrSentinel, stopErrSentinel} {
		assert.Contains(t, forwarded.String(), sentinel, "wire forwarding must remain lossless")
	}
	require.Eventually(t, func() bool {
		logs := captured.String()
		return strings.Contains(logs, "handlers.RuntimeHandlers.fanout: session ended") &&
			strings.Contains(logs, "runtime.autonomousTurn: source closed")
	}, time.Second, 10*time.Millisecond)
	for _, sentinel := range []string{inputSentinel, resultSentinel, metaSentinel, taskSentinel, summarySentinel, runErrSentinel, stopErrSentinel} {
		assert.NotContains(t, captured.String(), sentinel)
	}
}

func TestRuntime_Run_AutonomousPayloadAndStopErrorRemainForwardedButNotLogged(t *testing.T) {
	const (
		resultSentinel  = "SENTINEL_DAEMON_AUTONOMOUS_RESULT"
		metaSentinel    = "SENTINEL_DAEMON_AUTONOMOUS_META"
		stopErrSentinel = "SENTINEL_DAEMON_AUTONOMOUS_STOP_ERROR"
	)
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event, 1)
		ch <- agentruntime.Done{}
		close(ch)
		return ch, &agentruntime.RunResult{}, nil
	}
	rt.autoFn = func(_ int64) <-chan agentruntime.AutonomousTurn {
		out := make(chan agentruntime.AutonomousTurn, 1)
		events := make(chan agentruntime.Event, 1)
		events <- agentruntime.ToolResult{
			ToolCallID: "autonomous-safe-id",
			Content:    resultSentinel,
			Meta:       json.RawMessage(`{"detail":"` + metaSentinel + `"}`),
		}
		close(events)
		out <- agentruntime.AutonomousTurn{
			Events:  events,
			Result:  &agentruntime.RunResult{StopErr: errors.New(stopErrSentinel)},
			Trigger: "background_task",
		}
		close(out)
		return out
	}
	captured := captureRuntimeLogs(t)
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)

	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}
	_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(143)})
	require.NoError(t, err)
	frames := notif.waitFrames(t, 5)

	var forwarded strings.Builder
	for _, frame := range frames {
		switch params := frame.params.(type) {
		case wire.EventFrame:
			forwarded.WriteString(eventText(t, params.Event))
		case *wire.EventFrame:
			forwarded.WriteString(eventText(t, params.Event))
		case wire.RunResultDoneFrame:
			forwarded.WriteString(params.StopErrMsg)
		case *wire.RunResultDoneFrame:
			forwarded.WriteString(params.StopErrMsg)
		}
	}
	for _, sentinel := range []string{resultSentinel, metaSentinel, stopErrSentinel} {
		assert.Contains(t, forwarded.String(), sentinel, "autonomous wire forwarding must remain lossless")
	}
	require.Eventually(t, func() bool {
		logs := captured.String()
		return strings.Contains(logs, "runtime.autonomousTurn: forwarded") &&
			strings.Contains(logs, "runtime.autonomousTurn: source closed") &&
			strings.Contains(logs, "handlers.RuntimeHandlers.fanout: session ended")
	}, time.Second, 10*time.Millisecond)
	for _, sentinel := range []string{resultSentinel, metaSentinel, stopErrSentinel} {
		assert.NotContains(t, captured.String(), sentinel)
	}
}

func TestRuntime_Run_ForwardsAutonomousTurn(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event, 1)
		ch <- agentruntime.Done{}
		close(ch)
		return ch, &agentruntime.RunResult{ProviderSessionID: "psid-1"}, nil
	}
	rt.autoFn = func(_ int64) <-chan agentruntime.AutonomousTurn {
		out := make(chan agentruntime.AutonomousTurn, 1)
		evs := make(chan agentruntime.Event, 1)
		evs <- agentruntime.TextDelta{Text: "autonomous:listing"}
		close(evs)
		out <- agentruntime.AutonomousTurn{
			Events:  evs,
			Result:  &agentruntime.RunResult{ProviderSessionID: "psid-1", Model: "claude-sonnet-4-6"},
			Trigger: "background_task",
		}
		close(out)
		return out
	}
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)

	be := agent_backend_entity.AgentBackend{ID: 1, Type: string(agent_backend_entity.TypeClaudeCode), Name: "x"}
	_, err := h.Run(ctx, wire.RunParams{
		Backend: backendJSON(t, be), ConversationID: convID(42), AgentID: 7, Cwd: "/tmp", UserText: "hi",
	})
	require.NoError(t, err)

	// run fanout: Done + runResultDone = 2;autonomous: Started + Event + Done = 3 → 5 total。
	frames := notif.waitFrames(t, 5)

	// 只挑自主续轮三类帧,断言顺序 + 内容(与 run 帧的交错无关)。
	var (
		started   *wire.AutonomousTurnStartedFrame
		autoEvent *wire.EventFrame
		autoDone  *wire.RunResultDoneFrame
		order     []string
	)
	for _, f := range frames {
		switch f.method {
		case wire.NotifyAutonomousTurnStarted:
			order = append(order, "started")
			started = f.params.(*wire.AutonomousTurnStartedFrame)
		case wire.NotifyAutonomousTurnEvent:
			order = append(order, "event")
			autoEvent = f.params.(*wire.EventFrame)
		case wire.NotifyAutonomousTurnDone:
			order = append(order, "done")
			autoDone = f.params.(*wire.RunResultDoneFrame)
		}
	}
	require.NotNil(t, started)
	require.NotNil(t, autoEvent)
	require.NotNil(t, autoDone)
	assert.Equal(t, []string{"started", "event", "done"}, order)
	assert.Equal(t, convID(42), started.ConversationID)
	assert.Equal(t, "background_task", started.Trigger)
	assert.Equal(t, convID(42), autoEvent.ConversationID)
	assert.Equal(t, agentruntime.TextDelta{Text: "autonomous:listing"}, autoEvent.Event)
	assert.Equal(t, convID(42), autoDone.ConversationID)
	assert.Equal(t, "claude-sonnet-4-6", autoDone.Model)
}

// TestRuntime_Run_UserMessageMarker: R18 —— 浏览器在空闲会话上「开新一轮」时,daemon
// 在事件流开头注入一条 user_message 标记(携带发起方设备身份与用户文本),扇出给同一条
// 会话的其余订阅者,让桌面端把这一轮落成一行带来源标识的用户消息。
func TestRuntime_Run_UserMessageMarker(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event, 3)
		ch <- agentruntime.TextDelta{Text: "reply"}
		ch <- agentruntime.Done{}
		close(ch)
		return ch, &agentruntime.RunResult{ProviderSessionID: "psid-1"}, nil
	}
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)

	be := agent_backend_entity.AgentBackend{ID: 1, Type: string(agent_backend_entity.TypeClaudeCode), Name: "x"}
	_, err := h.Run(ctx, wire.RunParams{
		Backend:          backendJSON(t, be),
		ConversationID:   convID(42),
		AgentID:          7,
		Cwd:              "/tmp",
		UserText:         "浏览器发来的消息",
		SourceDevice:     "sha256:web-device",
		SourceDeviceName: "Chrome · macOS",
	})
	require.NoError(t, err)

	// 标记 + 后端事件 + 终态 = 3 帧。
	frames := notif.waitFrames(t, 3)

	require.Equal(t, wire.NotifyEvent, frames[0].method)
	ef0, ok := frames[0].params.(*wire.EventFrame)
	require.True(t, ok, "expected EventFrame, got %T", frames[0].params)
	assert.Equal(t, convID(42), ef0.ConversationID)
	assert.Equal(t, agentruntime.UserMessageEvent{
		Text:             "浏览器发来的消息",
		SourceDevice:     "sha256:web-device",
		SourceDeviceName: "Chrome · macOS",
	}, ef0.Event)

	// 后续后端事件原样跟在标记之后。
	ef1, ok := frames[1].params.(*wire.EventFrame)
	require.True(t, ok)
	assert.IsType(t, agentruntime.TextDelta{}, ef1.Event)
}

// TestRuntime_Run_NoUserMessageMarkerWhenNoSource: R18 单端零变化 —— 桌面端自己发消息
// 不带 SourceDevice,daemon 不注入 user_message 标记,事件流与今天逐帧一致。
func TestRuntime_Run_NoUserMessageMarkerWhenNoSource(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event, 2)
		ch <- agentruntime.TextDelta{Text: "hi"}
		ch <- agentruntime.Done{}
		close(ch)
		return ch, &agentruntime.RunResult{ProviderSessionID: "psid-1"}, nil
	}
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)

	be := agent_backend_entity.AgentBackend{ID: 1, Type: string(agent_backend_entity.TypeClaudeCode), Name: "x"}
	_, err := h.Run(ctx, wire.RunParams{
		Backend: backendJSON(t, be), ConversationID: convID(42), AgentID: 7, Cwd: "/tmp", UserText: "hi",
	})
	require.NoError(t, err)

	frames := notif.waitFrames(t, 3)
	ef0, ok := frames[0].params.(*wire.EventFrame)
	require.True(t, ok)
	assert.IsType(t, agentruntime.TextDelta{}, ef0.Event, "没有 SourceDevice 就不该注入 user_message 标记")
}

func TestRuntime_Run_BadBackendJSON_Errors(t *testing.T) {
	ctx, _, _, _, h := setupRuntimeTest(t, &fullRT{})
	_, err := h.Run(ctx, wire.RunParams{Backend: json.RawMessage(`{bad`)})
	require.Error(t, err)
}

func TestRuntime_Run_BuiltinBackend_Rejected(t *testing.T) {
	ctx, _, _, _, h := setupRuntimeTest(t, &fullRT{})
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeBuiltin)}
	_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be)})
	require.Error(t, err)
}

func TestRuntime_Run_OpenClawRemoteSecretUnavailable(t *testing.T) {
	ctx, _, _, _, h := setupRuntimeTest(t, nil)
	_, err := h.Run(ctx, wire.RunParams{
		Backend:        backendJSON(t, agent_backend_entity.AgentBackend{ID: 9, Type: string(agent_backend_entity.TypeOpenClaw), DeviceFingerprint: "7"}),
		ConversationID: convID(91),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote secret enrollment is unavailable")
}

func TestRuntime_Run_UnknownBackendType_Errors(t *testing.T) {
	// runtimeFor returns nil for unknown type.
	ctx, _, _, _, h := setupRuntimeTest(t, nil)
	be := agent_backend_entity.AgentBackend{Type: "ghost"}
	_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be)})
	require.Error(t, err)
}

// TestRuntime_Run_EffectiveKeyPreferredOverAgentBinding 钉死决策 9:wire 带的
// effectiveProviderKey(会话 provider_key 优先,chat_svc 组装)优先于 agent 绑定 ——
// daemon 必须按 wire 的 key 自解,而不是只认 be.LLMProviderKey。
func TestRuntime_Run_EffectiveKeyPreferredOverAgentBinding(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event)
		close(ch)
		return ch, &agentruntime.RunResult{}, nil
	}
	ctx, _, gw, lookup, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{
		Type:           string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "agent-bound-key",
	}
	// 会话所选供应商在 daemon 上也存在 → 用它,不用 agent 绑定。
	lookup.EXPECT().FindByKey(ctx, "session-key").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "session-key", Type: string(llm_provider_entity.TypeAnthropic),
		Status: consts.ACTIVE,
	}, nil)
	lookup.EXPECT().ResolveModel(ctx, "session-key", "").Return(handlers.EffectiveModel{ModelKey: "", ModelID: "claude-x"}, nil)
	gw.EXPECT().URL().Return("").AnyTimes()

	_, err := h.Run(ctx, wire.RunParams{
		Backend:        backendJSON(t, be),
		ConversationID: convID(42),
		LLMProviderKey: "session-key",
	})
	require.NoError(t, err)
	require.Len(t, rt.runReqs, 1)
	assert.Equal(t, "session-key", rt.runReqs[0].req.Provider.ProviderKey, "远端必须按 wire effectiveProviderKey 自解")
}

// TestRuntime_Run_EffectiveKeyMissing_FallsBackToAgentBinding 钉死决策 9 回退:会话
// provider_key 在 daemon 缺失 → 回退 agent 绑定执行,并在 ack 回传 providerFallbackKey
// 信号,桌面端据此追加一条持久 notice(与本地 Q3 一致)。
func TestRuntime_Run_EffectiveKeyMissing_FallsBackToAgentBinding(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event)
		close(ch)
		return ch, &agentruntime.RunResult{}, nil
	}
	ctx, _, gw, lookup, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{
		Type:           string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "agent-bound-key",
	}
	// 会话所选供应商在 daemon 缺失 → 查 agent 绑定。
	lookup.EXPECT().FindByKey(ctx, "session-key").Return(nil, errors.New("provider session-key not configured"))
	lookup.EXPECT().FindByKey(ctx, "agent-bound-key").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "agent-bound-key", Type: string(llm_provider_entity.TypeAnthropic),
		Status: consts.ACTIVE,
	}, nil)
	lookup.EXPECT().ResolveModel(ctx, "agent-bound-key", "").Return(handlers.EffectiveModel{ModelKey: "", ModelID: "claude-x"}, nil)
	gw.EXPECT().URL().Return("").AnyTimes()

	ack, err := h.Run(ctx, wire.RunParams{
		Backend:        backendJSON(t, be),
		ConversationID: convID(42),
		LLMProviderKey: "session-key",
	})
	require.NoError(t, err)
	require.Len(t, rt.runReqs, 1)
	assert.Equal(t, "agent-bound-key", rt.runReqs[0].req.Provider.ProviderKey, "缺会话 key 时应回退 agent 绑定")
	assert.Equal(t, "session-key", ack.ProviderFallbackKey, "回退必须回传信号供桌面端追加 notice")
}

// TestRuntime_Run_EffectiveKeyInactive_FallsBackToAgentBinding 钉死决策 9 回退的非
// active 分支:会话 provider_key 指向的供应商停用(IsActive=false)同样回退 agent 绑定
// 并回传信号。
func TestRuntime_Run_EffectiveKeyInactive_FallsBackToAgentBinding(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event)
		close(ch)
		return ch, &agentruntime.RunResult{}, nil
	}
	ctx, _, gw, lookup, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{
		Type:           string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "agent-bound-key",
	}
	// 会话所选供应商已停用(Status=0) → 回退 agent 绑定。
	lookup.EXPECT().FindByKey(ctx, "session-key").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "session-key", Type: string(llm_provider_entity.TypeAnthropic),
		Status: 0,
	}, nil)
	lookup.EXPECT().FindByKey(ctx, "agent-bound-key").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "agent-bound-key", Type: string(llm_provider_entity.TypeAnthropic),
		Status: consts.ACTIVE,
	}, nil)
	lookup.EXPECT().ResolveModel(ctx, "agent-bound-key", "").Return(handlers.EffectiveModel{ModelKey: "", ModelID: "claude-x"}, nil)
	gw.EXPECT().URL().Return("").AnyTimes()

	ack, err := h.Run(ctx, wire.RunParams{
		Backend:        backendJSON(t, be),
		ConversationID: convID(42),
		LLMProviderKey: "session-key",
	})
	require.NoError(t, err)
	require.Len(t, rt.runReqs, 1)
	assert.Equal(t, "agent-bound-key", rt.runReqs[0].req.Provider.ProviderKey, "非 active 会话 key 应回退 agent 绑定")
	assert.Equal(t, "session-key", ack.ProviderFallbackKey)
}

// TestRuntime_Run_EffectiveKeyMissing_NoAgentBinding_FallsBackToCLILogin 钉死决策 9
// 边界:会话 provider_key 在 daemon 缺失且 agent 未绑定任何供应商(CLI 登录态) →
// 回退 CLI 登录态(provider=nil)并回传信号,不报错。
func TestRuntime_Run_EffectiveKeyMissing_NoAgentBinding_FallsBackToCLILogin(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event)
		close(ch)
		return ch, &agentruntime.RunResult{}, nil
	}
	ctx, _, _, lookup, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{
		Type:           string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "",
	}
	lookup.EXPECT().FindByKey(ctx, "session-key").Return(nil, errors.New("provider session-key not configured"))

	ack, err := h.Run(ctx, wire.RunParams{
		Backend:        backendJSON(t, be),
		ConversationID: convID(42),
		LLMProviderKey: "session-key",
	})
	require.NoError(t, err)
	require.Len(t, rt.runReqs, 1)
	assert.Nil(t, rt.runReqs[0].req.Provider, "无 agent 绑定回退 CLI 登录态(provider=nil)")
	assert.Equal(t, "session-key", ack.ProviderFallbackKey, "回退必须回传信号")
}

func TestRuntime_Run_ProviderLookupMissing_ReturnsProviderMissingCode(t *testing.T) {
	rt := &fullRT{}
	ctx, _, _, lookup, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{
		Type:           string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "missing-key",
	}
	lookup.EXPECT().FindByKey(ctx, "missing-key").Return(nil, errors.New("provider missing-key not configured"))

	_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(73)})
	require.Error(t, err)

	var rpcErr *rpcerror.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, rpcerror.ErrProviderMissing.Code, rpcErr.Code)
	assert.Contains(t, rpcErr.Message, "missing-key")
}

// TestRuntime_Run_EffectiveConfig_MatchesDesktopFieldForField 钉死 spec 的收敛判据：
// 相同输入下 daemon 与桌面构造出的 EffectiveLLMConfig **逐字段相同**。两端从此共用
// agentruntime.NewEffectiveLLMConfig，桌面喂的是 llm_provider_svc.ResolveTarget 的解析
// 结果、daemon 喂的是自家目录的解析结果，装配规则只有那一份 —— daemon 手写那份曾漏填
// ContextWindow 与 MaxOutput。
func TestRuntime_Run_EffectiveConfig_MatchesDesktopFieldForField(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event)
		close(ch)
		return ch, &agentruntime.RunResult{}, nil
	}
	ctx, _, gw, lookup, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{
		Type:           string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "pk",
		LLMModelKey:    "model-opus",
	}
	lookup.EXPECT().FindByKey(ctx, "pk").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "pk",
		Type:        string(llm_provider_entity.TypeAnthropic),
		Name:        "Anthropic 主号",
		BaseURL:     "https://api.example.com",
		APIKey:      "sk-fixture",
		Status:      consts.ACTIVE,
	}, nil)
	lookup.EXPECT().ResolveModel(ctx, "pk", "model-opus").Return(handlers.EffectiveModel{
		ModelKey:      "model-opus",
		ModelID:       "claude-opus-4-5",
		ContextWindow: 500000,
		MaxOutput:     64000,
	}, nil)
	gw.EXPECT().URL().Return("").AnyTimes()

	_, err := h.Run(ctx, wire.RunParams{
		Backend:        backendJSON(t, be),
		ConversationID: convID(42),
		LLMProviderKey: "pk",
		LLMModelKey:    "model-opus",
	})
	require.NoError(t, err)
	require.Len(t, rt.runReqs, 1)

	// 桌面同一套输入下会构造的那一份（同一个构造口，喂桌面解析口的等价字段）。
	desktop := agentruntime.NewEffectiveLLMConfig(agentruntime.EffectiveLLMConfigInput{
		ProviderKey:      "pk",
		ProviderType:     string(llm_provider_entity.TypeAnthropic),
		ProviderName:     "Anthropic 主号",
		TargetModelKey:   "model-opus",
		ResolvedModelKey: "model-opus",
		ResolvedModelID:  "claude-opus-4-5",
		ContextWindow:    500000,
		MaxOutput:        64000,
		BaseURL:          "https://api.example.com",
		APIKey:           "sk-fixture",
		HasAPIKey:        true,
	})
	assert.Equal(t, desktop, rt.runReqs[0].req.Effective, "daemon 与桌面必须逐字段相同")
}

func TestRuntime_Run_RuntimeReturnsErr_RevokesToken(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		return nil, nil, errors.New("boom")
	}
	ctx, _, _, _, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}
	_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be)})
	require.Error(t, err)
}

// TestRuntime_Run_WithProvider_ReusesPermanentTokenAcrossTurns pins the daemon
// analog of the local "long session steer dies" fix: the gateway token is baked
// into the persistent claude subprocess at spawn and reused across turns, so it
// must be minted ONCE per session, be PERMANENT (ttl=0), and NEVER be revoked at
// turn end. The old code minted a fresh time.Hour token every turn and revoked
// it in fanout, leaving the reused subprocess holding a dead token from turn 2.
func TestRuntime_Run_WithProvider_ReusesPermanentTokenAcrossTurns(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event, 1)
		ch <- agentruntime.Done{}
		close(ch)
		return ch, &agentruntime.RunResult{ProviderSessionID: "psid"}, nil
	}
	ctx, _, gw, lookup, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{
		ID:             3,
		Type:           string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "pk",
	}
	lookup.EXPECT().FindByKey(ctx, "pk").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "pk", Type: string(llm_provider_entity.TypeAnthropic),
		Status: consts.ACTIVE,
	}, nil).Times(2)
	lookup.EXPECT().ResolveModel(ctx, "pk", "").Return(handlers.EffectiveModel{ModelKey: "", ModelID: "claude-x"}, nil).Times(2)
	gw.EXPECT().URL().Return("http://gw").AnyTimes()
	// ttl=0 (permanent), minted exactly once; NO RevokeToken EXPECT → gomock
	// fails if anything revokes it (e.g. a leftover turn-end revoke).
	gw.EXPECT().IssueTokenFor(ctx, gomock.Any(), "pk", "", time.Duration(0)).Return("sess-token", nil).Times(1)
	// 后续轮只把既有 token 的路由目标对齐到同一家（没换供应商 → 原地不动）。
	gw.EXPECT().SetTokenTarget("sess-token", "pk", "").Return("pk", true).Times(1)

	runOnce := func() {
		_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(42), UserText: "hi"})
		require.NoError(t, err)
		// Let the async fanout settle (session unregisters) before the next turn.
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, serr := h.Steer(ctx, wire.SteerParams{ConversationID: convID(42), Text: "x"})
			assert.ErrorIs(c, serr, agentruntime.ErrNoActiveTurn)
		}, time.Second, 10*time.Millisecond)
	}
	runOnce() // turn 1 mints sess-token
	runOnce() // turn 2 must reuse it

	require.Len(t, rt.runReqs, 2)
	assert.Equal(t, "sess-token", rt.runReqs[0].req.GatewayToken)
	assert.Equal(t, "sess-token", rt.runReqs[1].req.GatewayToken, "turn 2 must reuse the same permanent token")
}

// TestRuntime_Run_SessionTokenFollowsEffectiveProvider 钉死决策 3 + 12 的 daemon 侧：
// daemon 自家网关的会话常驻 token 同样按 effective provider 签发；桌面端中途换了供应商
// （下一轮 wire 带新的 effectiveProviderKey）时，只改这条既有 token 的路由目标 ——
// token 字符串必须不变（它已烤进 daemon 本机 spawn 的 CLI 子进程 env）。
func TestRuntime_Run_SessionTokenFollowsEffectiveProvider(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event, 1)
		ch <- agentruntime.Done{}
		close(ch)
		return ch, &agentruntime.RunResult{ProviderSessionID: "psid"}, nil
	}
	ctx, _, gw, lookup, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{
		ID:             3,
		Type:           string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "agent-bound-key",
	}
	activeProvider := func(key string) *llm_provider_entity.LLMProvider {
		return &llm_provider_entity.LLMProvider{
			ProviderKey: key, Type: string(llm_provider_entity.TypeAnthropic),
			Status: consts.ACTIVE,
		}
	}
	lookup.EXPECT().FindByKey(ctx, "first-key").Return(activeProvider("first-key"), nil)
	lookup.EXPECT().FindByKey(ctx, "switched-key").Return(activeProvider("switched-key"), nil)
	lookup.EXPECT().ResolveModel(ctx, "first-key", "").Return(handlers.EffectiveModel{ModelKey: "", ModelID: "claude-x"}, nil)
	lookup.EXPECT().ResolveModel(ctx, "switched-key", "").Return(handlers.EffectiveModel{ModelKey: "", ModelID: "claude-x"}, nil)
	gw.EXPECT().URL().Return("http://gw").AnyTimes()
	// 首轮按 effective key 签一个永久 token；换供应商后**不得**再签第二个。
	gw.EXPECT().IssueTokenFor(ctx, gomock.Any(), "first-key", "", time.Duration(0)).
		Return("sess-token", nil).Times(1)
	gw.EXPECT().SetTokenTarget("sess-token", "switched-key", "").Return("first-key", true).Times(1)

	runOnce := func(providerKey string) {
		_, err := h.Run(ctx, wire.RunParams{
			Backend: backendJSON(t, be), ConversationID: convID(42), UserText: "hi", LLMProviderKey: providerKey,
		})
		require.NoError(t, err)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, serr := h.Steer(ctx, wire.SteerParams{ConversationID: convID(42), Text: "x"})
			assert.ErrorIs(c, serr, agentruntime.ErrNoActiveTurn)
		}, time.Second, 10*time.Millisecond)
	}
	runOnce("first-key")
	runOnce("switched-key")

	require.Len(t, rt.runReqs, 2)
	assert.Equal(t, "sess-token", rt.runReqs[0].req.GatewayToken)
	assert.Equal(t, "sess-token", rt.runReqs[1].req.GatewayToken, "换供应商不换 token 字符串")
	assert.Equal(t, "switched-key", rt.runReqs[1].req.Provider.ProviderKey)
}

func TestRuntime_Run_StopErrAborted_RehydratesCode(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event)
		close(ch)
		return ch, &agentruntime.RunResult{StopErr: agentruntime.ErrAborted}, nil
	}
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}
	_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(1)})
	require.NoError(t, err)

	frames := notif.waitFrames(t, 1)
	done, ok := frames[0].params.(*wire.RunResultDoneFrame)
	require.True(t, ok)
	assert.Equal(t, wire.ErrCodeAborted, done.StopErrCode)
	assert.Equal(t, agentruntime.ErrAborted.Error(), done.StopErrMsg)
}

// ── Steer / CancelSteer / DrainPending / Abort / SetPermissionMode ─────────

// runWithRT registers a session by calling Run with a runtime whose Run
// returns a never-closing channel — the goroutine stays alive so the
// session row remains registered for subsequent control RPCs.
func runWithRT(t *testing.T, h *handlers.RuntimeHandlers, ctx context.Context, conversationID string) {
	t.Helper()
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}
	ack, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: conversationID})
	require.NoError(t, err)
	require.Equal(t, conversationID, ack.ConversationID)
}

// runtimeWithLiveSession installs the fake RT and starts a session with the
// given sid; the runtime's events channel stays open so the row stays alive.
func runtimeWithLiveSession(t *testing.T, rt *fullRT, sid int64) (
	context.Context,
	*recordingOutbound,
	*handlers.RuntimeHandlers,
	chan agentruntime.Event,
) {
	t.Helper()
	live := make(chan agentruntime.Event)
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		return live, &agentruntime.RunResult{}, nil
	}
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)
	runWithRT(t, h, ctx, convID(sid))
	return ctx, notif, h, live
}

func TestRuntime_Steer_Success(t *testing.T) {
	rt := &fullRT{}
	ctx, _, h, live := runtimeWithLiveSession(t, rt, 9)
	defer close(live)

	_, err := h.Steer(ctx, wire.SteerParams{ConversationID: convID(9), QueuedID: "q-1", Text: "stop"})
	require.NoError(t, err)
	assert.Equal(t, []steerCall{{sid: handlers.RuntimeSessionKey(convID(9)), queuedID: "q-1", text: "stop"}}, rt.steerCalls)
}

func TestRuntime_Steer_NoSession_ErrNoActiveTurn(t *testing.T) {
	ctx, _, _, _, h := setupRuntimeTest(t, &fullRT{})
	_, err := h.Steer(ctx, wire.SteerParams{ConversationID: convID(99), Text: "x"})
	require.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
}

func TestRuntime_Steer_BackendUnsupported_ErrUnsupported(t *testing.T) {
	// bareRT implements only Runtime — Steerer assertion fails.
	rt := &fullRT{} // start session via fullRT so registration succeeds
	ctx, _, _, _, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}
	live := make(chan agentruntime.Event)
	defer close(live)
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		return live, &agentruntime.RunResult{}, nil
	}
	_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(5)})
	require.NoError(t, err)

	// Swap in a bare runtime *after* the session is registered, so the
	// Steer handler resolves via runtimeFor and finds bareRT (no Steerer).
	h.SwapRuntimeFor(func(_ agent_backend_entity.BackendType) agentruntime.Runtime { return bareRT{} })

	_, err = h.Steer(ctx, wire.SteerParams{ConversationID: convID(5), Text: "x"})
	require.ErrorIs(t, err, agentruntime.ErrUnsupported)
}

func TestRuntime_ControlRPCs_BackendUnsupported_ErrUnsupported(t *testing.T) {
	rt := &fullRT{}
	ctx, _, h, live := runtimeWithLiveSession(t, rt, 5)
	defer close(live)

	h.SwapRuntimeFor(func(_ agent_backend_entity.BackendType) agentruntime.Runtime { return bareRT{} })

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "cancel steer",
			call: func() error {
				_, err := h.CancelSteer(ctx, wire.CancelSteerParams{ConversationID: convID(5), QueuedID: "q-1"})
				return err
			},
		},
		{
			name: "drain pending",
			call: func() error {
				_, err := h.DrainPending(ctx, wire.DrainParams{ConversationID: convID(5)})
				return err
			},
		},
		{
			name: "abort",
			call: func() error {
				_, err := h.Abort(ctx, wire.AbortParams{ConversationID: convID(5)})
				return err
			},
		},
		{
			name: "stop background task",
			call: func() error {
				_, err := h.StopBackgroundTask(ctx, wire.StopBackgroundTaskParams{ConversationID: convID(5), TaskID: "b0"})
				return err
			},
		},
		{
			name: "set permission mode",
			call: func() error {
				_, err := h.SetPermissionMode(ctx, wire.SetPermissionModeParams{ConversationID: convID(5), Mode: "plan"})
				return err
			},
		},
		{
			name: "submit answer",
			call: func() error {
				_, err := h.SubmitAnswer(ctx, wire.SubmitAnswerParams{ConversationID: convID(5), RequestID: "r-1"})
				return err
			},
		},
		{
			name: "submit tool permission",
			call: func() error {
				_, err := h.SubmitToolPermission(ctx, wire.SubmitToolPermissionParams{ConversationID: convID(5), RequestID: "p-1"})
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, tt.call(), agentruntime.ErrUnsupported)
		})
	}
}

func TestRuntime_CancelSteer_ReturnsRemoved(t *testing.T) {
	rt := &fullRT{
		cancelSteerFn: func(_ int64, _ string) ([]string, error) {
			return []string{"a", "b"}, nil
		},
	}
	ctx, _, h, live := runtimeWithLiveSession(t, rt, 1)
	defer close(live)

	out, err := h.CancelSteer(ctx, wire.CancelSteerParams{ConversationID: convID(1), QueuedID: "q-1"})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, out.Removed)
}

func TestRuntime_CancelSteer_NotFound_RehydrateSentinel(t *testing.T) {
	rt := &fullRT{
		cancelSteerFn: func(_ int64, _ string) ([]string, error) {
			return nil, agentruntime.ErrSteerNotFound
		},
	}
	ctx, _, h, live := runtimeWithLiveSession(t, rt, 1)
	defer close(live)

	_, err := h.CancelSteer(ctx, wire.CancelSteerParams{ConversationID: convID(1), QueuedID: "q-x"})
	require.ErrorIs(t, err, agentruntime.ErrSteerNotFound)
}

func TestRuntime_DrainPending_ReturnsSteers(t *testing.T) {
	rt := &fullRT{
		drainFn: func(_ int64) []agentruntime.ConsumedSteer {
			return []agentruntime.ConsumedSteer{{QueuedID: "q1", Text: "a"}}
		},
	}
	ctx, _, h, live := runtimeWithLiveSession(t, rt, 2)
	defer close(live)

	out, err := h.DrainPending(ctx, wire.DrainParams{ConversationID: convID(2)})
	require.NoError(t, err)
	assert.Equal(t, []agentruntime.ConsumedSteer{{QueuedID: "q1", Text: "a"}}, out.Steers)
}

// fakeSteerSource 是 handlers.SteerSourcePort 的内存实现,供单元测试直接注入来源映射。
type fakeSteerSource struct {
	mu sync.Mutex
	m  map[string]handlers.SteerSourceEntry
}

func newFakeSteerSource() *fakeSteerSource {
	return &fakeSteerSource{m: map[string]handlers.SteerSourceEntry{}}
}

func (f *fakeSteerSource) Record(id string, e handlers.SteerSourceEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[id] = e
}

func (f *fakeSteerSource) Consume(id string) (handlers.SteerSourceEntry, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.m[id]
	if ok {
		delete(f.m, id)
	}
	return e, ok
}

func (f *fakeSteerSource) Forget(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, id)
}

func newRuntimeHandlersOnWithSource(t *testing.T, rt agentruntime.Runtime, src handlers.SteerSourcePort) (
	context.Context,
	*recordingOutbound,
	*handlers.RuntimeHandlers,
	chan agentruntime.Event,
) {
	t.Helper()
	notif := newRecordingOutbound()
	sess := newRecordingSessions()
	h := handlers.NewRuntimeHandlers(handlers.RuntimeDeps{
		NotifyFor:    notif.notifierFor,
		Journal:      notif,
		Sessions:     sess,
		SessionQuery: sess,
		SteerSource:  src,
		RuntimeFor: func(_ agent_backend_entity.BackendType) agentruntime.Runtime {
			return rt
		},
	})
	live := make(chan agentruntime.Event)
	rt.(*fullRT).runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		return live, &agentruntime.RunResult{}, nil
	}
	runWithRT(t, h, context.Background(), convID(2))
	return context.Background(), notif, h, live
}

// TestRuntime_DrainPending_StampsSubmitterSource (R17):轮末残留的 pending steer
// 必须带着提交方来源返回 —— 它们和实时消费的 SteerConsumed 共用同一张映射表。
func TestRuntime_DrainPending_StampsSubmitterSource(t *testing.T) {
	rt := &fullRT{drainFn: func(_ int64) []agentruntime.ConsumedSteer {
		return []agentruntime.ConsumedSteer{{QueuedID: "q1", Text: "a"}, {QueuedID: "q2", Text: "b"}}
	}}
	src := newFakeSteerSource()
	src.Record("q1", handlers.SteerSourceEntry{Peer: "sha256:other-device", Name: "iPhone"})
	// q2 没进映射(本机/未知)→ 保持空。
	ctx, _, h, live := newRuntimeHandlersOnWithSource(t, rt, src)
	defer close(live)

	out, err := h.DrainPending(ctx, wire.DrainParams{ConversationID: convID(2)})
	require.NoError(t, err)
	require.Len(t, out.Steers, 2)
	assert.Equal(t, "sha256:other-device", out.Steers[0].SourcePeer)
	assert.Equal(t, "iPhone", out.Steers[0].SourceName)
	assert.Empty(t, out.Steers[1].SourcePeer)
	// 消费即删:同一 queuedID 不会二次盖来源。
	_, ok := src.Consume("q1")
	assert.False(t, ok, "consumed source mapping must be removed")
}

// TestRuntime_Fanout_StampsSteerConsumedSource (R17):实时消费路径 —— fanout 把
// Steer RPC 记下的提交方盖到 SteerConsumed 的每条 steer 上,并随密封 EventFrame 流出。
func TestRuntime_Fanout_StampsSteerConsumedSource(t *testing.T) {
	rt := &fullRT{}
	src := newFakeSteerSource()
	src.Record("q-live", handlers.SteerSourceEntry{Peer: "sha256:other-device", Name: "iPad"})
	_, notif, _, live := newRuntimeHandlersOnWithSource(t, rt, src)

	live <- agentruntime.SteerConsumed{
		Steers: []agentruntime.ConsumedSteer{{QueuedID: "q-live", Text: "go on"}},
	}
	close(live)

	frames := notif.waitFrames(t, 2) // SteerConsumed + runResultDone
	var ef *wire.EventFrame
	for _, f := range frames {
		if f.method == wire.NotifyEvent {
			ef = f.params.(*wire.EventFrame)
		}
	}
	require.NotNil(t, ef, "fanout must emit the SteerConsumed event frame")
	sc, ok := ef.Event.(agentruntime.SteerConsumed)
	require.True(t, ok)
	require.Len(t, sc.Steers, 1)
	assert.Equal(t, "q-live", sc.Steers[0].QueuedID)
	assert.Equal(t, "sha256:other-device", sc.Steers[0].SourcePeer)
	assert.Equal(t, "iPad", sc.Steers[0].SourceName)
}

func TestRuntime_Abort_Success(t *testing.T) {
	rt := &fullRT{}
	ctx, _, h, live := runtimeWithLiveSession(t, rt, 3)
	defer close(live)

	_, err := h.Abort(ctx, wire.AbortParams{ConversationID: convID(3)})
	require.NoError(t, err)
	assert.Equal(t, []int64{handlers.RuntimeSessionKey(convID(3))}, rt.abortCalls)
}

// TestRuntime_Abort_PassesTokenAndReturnsInterruptedTurnKind 钉死决策 1 的 daemon 侧:
// RuntimeHandlers.Abort 把 wire.AbortParams.TurnToken 透传给 runtime 的 Abort,并把
// 被中断轮的类型经 wire.AbortResult.TurnKind 返给对端 —— spec 测试接缝「remote wire
// + daemon handler:AbortParams 携带 token,daemon 侧透传并返回轮类型」。
func TestRuntime_Abort_PassesTokenAndReturnsInterruptedTurnKind(t *testing.T) {
	rt := &fullRT{abortOutcome: agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindSubagentActivity}}
	ctx, _, h, live := runtimeWithLiveSession(t, rt, 3)
	defer close(live)

	res, err := h.Abort(ctx, wire.AbortParams{ConversationID: convID(3), TurnToken: 42})
	require.NoError(t, err)
	assert.Equal(t, []int64{handlers.RuntimeSessionKey(convID(3))}, rt.abortCalls)
	assert.Equal(t, []uint64{42}, rt.abortTokens, "daemon 侧必须把 turnToken 原样透传给 runtime")
	assert.Equal(t, agentruntime.TurnKindSubagentActivity, res.TurnKind)
}

func TestRuntime_Abort_NoSession_ErrNoActiveTurn(t *testing.T) {
	ctx, _, _, _, h := setupRuntimeTest(t, &fullRT{})
	_, err := h.Abort(ctx, wire.AbortParams{ConversationID: convID(7)})
	require.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
}

func TestRuntime_StopBackgroundTask_Success(t *testing.T) {
	rt := &fullRT{}
	ctx, _, h, live := runtimeWithLiveSession(t, rt, 3)
	defer close(live)

	_, err := h.StopBackgroundTask(ctx, wire.StopBackgroundTaskParams{ConversationID: convID(3), TaskID: "b0n82mqaj"})
	require.NoError(t, err)
	assert.Equal(t, []stopBgCall{{sid: handlers.RuntimeSessionKey(convID(3)), taskID: "b0n82mqaj"}}, rt.stopBgCalls)
}

func TestRuntime_StopBackgroundTask_NoSession_ErrNoActiveTurn(t *testing.T) {
	ctx, _, _, _, h := setupRuntimeTest(t, &fullRT{})
	_, err := h.StopBackgroundTask(ctx, wire.StopBackgroundTaskParams{ConversationID: convID(7), TaskID: "b0"})
	require.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
}

func TestRuntime_SetPermissionMode_Success(t *testing.T) {
	rt := &fullRT{}
	ctx, _, h, live := runtimeWithLiveSession(t, rt, 4)
	defer close(live)

	_, err := h.SetPermissionMode(ctx, wire.SetPermissionModeParams{ConversationID: convID(4), Mode: "plan"})
	require.NoError(t, err)
	assert.Equal(t, []setModeCall{{sid: handlers.RuntimeSessionKey(convID(4)), mode: "plan"}}, rt.setModeCalls)
}

func TestRuntime_SubmitAnswer_Success(t *testing.T) {
	rt := &fullRT{}
	ctx, _, h, live := runtimeWithLiveSession(t, rt, 5)
	defer close(live)

	qs := []agentruntime.AskQuestion{{Question: "ok?"}}
	as := []agentruntime.AskAnswer{{QuestionIndex: 0, Labels: []string{"yes"}}}
	_, err := h.SubmitAnswer(ctx, wire.SubmitAnswerParams{
		ConversationID: convID(5), RequestID: "r-1", Questions: qs, Answers: as, Skipped: false,
	})
	require.NoError(t, err)
	require.Len(t, rt.submitAnswerCalls, 1)
	assert.Equal(t, "r-1", rt.submitAnswerCalls[0].requestID)
	assert.Equal(t, as, rt.submitAnswerCalls[0].answers)
}

func TestRuntime_SubmitToolPermission_Success(t *testing.T) {
	rt := &fullRT{}
	ctx, _, h, live := runtimeWithLiveSession(t, rt, 6)
	defer close(live)

	_, err := h.SubmitToolPermission(ctx, wire.SubmitToolPermissionParams{
		ConversationID: convID(6), RequestID: "p-1", Allow: false, DenyReason: "nope",
	})
	require.NoError(t, err)
	require.Len(t, rt.submitToolPermCalls, 1)
	assert.Equal(t, "p-1", rt.submitToolPermCalls[0].requestID)
	assert.Equal(t, "nope", rt.submitToolPermCalls[0].denyReason)
	assert.False(t, rt.submitToolPermCalls[0].allow)
}

// ── 会话生命周期落库 ────────────────────────────────────────────────────────
//
// daemon_sessions 的一行 = 「这条会话是谁的、在跑什么、处于哪一步」。没有它,重连的
// 客户端拿到的会话清单永远是空的,R10 的启动清扫也没有可扫的对象。

// TestRuntime_Run_RecordsSessionRowThenMovesItToIdleAtTurnEnd 覆盖生命周期的主链:
// runtime.run 起手时按 (对端, 会话) 建行并置 running(带上客户端展示要用的 agent id /
// cwd / backend 类型),轮末事件流关闭后置 idle,等待下一轮。
func TestRuntime_Run_RecordsSessionRowThenMovesItToIdleAtTurnEnd(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event, 1)
		ch <- agentruntime.Done{}
		close(ch)
		return ch, &agentruntime.RunResult{}, nil
	}
	ctx, notif, sess, h := setupRuntimeTestWithSessions(t, rt)
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}
	_, err := h.Run(ctx, wire.RunParams{
		Backend: backendJSON(t, be), ConversationID: convID(5), AgentID: 7, Cwd: "/work",
	})
	require.NoError(t, err)

	notif.waitFrames(t, 2) // Done + runResultDone
	assert.Equal(t, []string{"start:" + convID(5), "finish:" + convID(5)}, sess.waitSteps(t, 2))
	assert.Equal(t, []handlers.SessionRecord{{
		PeerSessionID: convID(5), AgentID: 7, Cwd: "/work",
		BackendType:    string(agent_backend_entity.TypeClaudeCode),
		LifecycleState: wire.SessionLifecycleRunning,
	}}, sess.started(), "起手必须建行并置 running,带上客户端展示要用的元数据")
}

// TestRuntime_Run_PersistsTitleAgentSyncIDAndProviderSessionID 覆盖 R7 + 决策 8 的
// 落库:每轮 runtime.run 起手时把会话标题、Agent 同步标识与 daemon 从 result 收回的
// provider_session_id 一并写进那一行。
func TestRuntime_Run_PersistsTitleAgentSyncIDAndProviderSessionID(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event, 1)
		ch <- agentruntime.Done{}
		close(ch)
		return ch, &agentruntime.RunResult{ProviderSessionID: "claude-abc123"}, nil
	}
	ctx, notif, sess, h := setupRuntimeTestWithSessions(t, rt)
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}
	_, err := h.Run(ctx, wire.RunParams{
		Backend: backendJSON(t, be), ConversationID: convID(5), AgentID: 7, Cwd: "/work",
		Title: "fix the bug", AgentSyncID: "01HXsync000000000000000000",
	})
	require.NoError(t, err)

	notif.waitFrames(t, 2) // Done + runResultDone
	assert.Equal(t, []handlers.SessionRecord{{
		PeerSessionID:     convID(5),
		AgentID:           7,
		Cwd:               "/work",
		BackendType:       string(agent_backend_entity.TypeClaudeCode),
		LifecycleState:    wire.SessionLifecycleRunning,
		Title:             "fix the bug",
		AgentSyncID:       "01HXsync000000000000000000",
		ProviderSessionID: "claude-abc123",
	}}, sess.started(), "起手建行必须带上标题、Agent 同步标识与 daemon 收回的 provider_session_id")
}

// TestRuntime_Run_ForwardsAgentSyncIDToRuntime 覆盖 web 发起的随手对话:浏览器没有
// daemon 本地自增 agentID,没选项目时 cwd 也为空,runtime 只能用账号级同步标识解析
// Agent 工作目录。协议边界收到的 AgentSyncID 必须原样进入 RunRequest。
func TestRuntime_Run_ForwardsAgentSyncIDToRuntime(t *testing.T) {
	rt := &fullRT{}
	ctx, notif, _, h := setupRuntimeTestWithSessions(t, rt)
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}

	_, err := h.Run(ctx, wire.RunParams{
		Backend: backendJSON(t, be), ConversationID: convID(5), AgentID: 0, Cwd: "",
		AgentSyncID: "01KZNE7YKJQ6A79YVDCMW1A63R",
	})
	require.NoError(t, err)
	notif.waitFrames(t, 1) // runResultDone
	require.Len(t, rt.runReqs, 1)
	assert.Equal(t, "01KZNE7YKJQ6A79YVDCMW1A63R", rt.runReqs[0].req.AgentSyncID,
		"web 随手对话必须把协议里的 AgentSyncID 交给 runtime 解析兜底 cwd")
}

// TestRuntime_Run_ContinuationResolvesStoredProviderSessionID 覆盖决策 8:provider_session_id
// 落库之后,续话不再需要调用方提供 —— 调用方空着字段发下一轮时,daemon 用自己落库的那
// 份把它续上。
func TestRuntime_Run_ContinuationResolvesStoredProviderSessionID(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event, 1)
		ch <- agentruntime.Done{}
		close(ch)
		return ch, &agentruntime.RunResult{ProviderSessionID: "claude-abc123"}, nil
	}
	ctx, notif, _, h := setupRuntimeTestWithSessions(t, rt)
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}
	// 第一轮:daemon 从 result 收回 providerSessionID 并落库。
	_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(5), AgentID: 7, Cwd: "/work"})
	require.NoError(t, err)
	notif.waitFrames(t, 2)
	require.Len(t, rt.runReqs, 1)

	// 第二轮:调用方不再提供 providerSessionID(决策 8)。
	_, err = h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(5), AgentID: 7, Cwd: "/work"})
	require.NoError(t, err)
	notif.waitFrames(t, 2)
	require.Len(t, rt.runReqs, 2)
	assert.Equal(t, "claude-abc123", rt.runReqs[1].req.ProviderSessionID,
		"续话不需要调用方提供 providerSessionID,daemon 拿自己落库的那份续上")
}

// TestRuntime_Run_FreshSessionSkipsStoredProviderSessionID 覆盖挂账修复:决策 8 把「空
// RunParams.ProviderSessionID」重载成「用落库那份续话」,但 regenerate 与 provider 会话
// 失效恢复这两条路径的空字段本意是「起全新会话」。freshSession=true 显式声明这一轮不续
// 任何落库的 provider_session_id —— daemon 必须把 ProviderSessionID 保持为空让 runtime
// 新建,而不是拿落库的旧 id 顶掉(否则 regenerate 退化成续旧上下文、gone 恢复永远撞同
// 一个失效 id)。
func TestRuntime_Run_FreshSessionSkipsStoredProviderSessionID(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event, 1)
		ch <- agentruntime.Done{}
		close(ch)
		return ch, &agentruntime.RunResult{ProviderSessionID: "claude-abc123"}, nil
	}
	ctx, notif, _, h := setupRuntimeTestWithSessions(t, rt)
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}
	// 第一轮:daemon 从 result 收回 providerSessionID 并落库。
	_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(5), AgentID: 7, Cwd: "/work"})
	require.NoError(t, err)
	notif.waitFrames(t, 2)
	require.Len(t, rt.runReqs, 1)

	// 第二轮:调用方显式声明 freshSession —— 落库已有旧 id,也必须起全新会话。
	_, err = h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(5), AgentID: 7, Cwd: "/work", FreshSession: true})
	require.NoError(t, err)
	notif.waitFrames(t, 2)
	require.Len(t, rt.runReqs, 2)
	assert.Equal(t, "", rt.runReqs[1].req.ProviderSessionID,
		"freshSession=true 时 daemon 不得用落库的 provider_session_id 续话,必须保持为空起全新会话")
}

// TestRuntime_Run_AutonomousTurnMovesLifecycleBackToRunning 覆盖自主续轮:backend
// 自发跑的一轮同样是「一轮执行中」,会话必须在这段时间报 running 而不是停在 idle ——
// 否则重连的客户端会把一条正在产出事件的会话显示成闲置。
func TestRuntime_Run_AutonomousTurnMovesLifecycleBackToRunning(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event)
		close(ch)
		return ch, &agentruntime.RunResult{}, nil
	}
	rt.autoFn = func(_ int64) <-chan agentruntime.AutonomousTurn {
		out := make(chan agentruntime.AutonomousTurn, 1)
		evs := make(chan agentruntime.Event)
		close(evs)
		out <- agentruntime.AutonomousTurn{Trigger: "hook", Events: evs, Result: &agentruntime.RunResult{}}
		close(out)
		return out
	}
	ctx, _, sess, h := setupRuntimeTestWithSessions(t, rt)
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}
	_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(5)})
	require.NoError(t, err)

	// 主轮 start + 主轮 finish + 自主续轮 running + 自主续轮 finish;两条 fanout
	// goroutine 交错,所以断言内容与条数而不是顺序。
	steps := sess.waitSteps(t, 4)
	assert.Contains(t, steps, "running:"+convID(5), "自主续轮开始时会话必须回到 running")
	assert.Equal(t, 2, countStep(steps, "finish:"+convID(5)), "自主续轮结束同样要落回 idle")
}

// ── SubmitAnswer / SubmitToolPermission idempotency (R8) ────────────────────
//
// Neither call may ever surface an error for "already answered" or "session
// gone" — a reconnected client cannot tell whether its previous submission
// arrived, so an error would make it misreport to the user.

func TestRuntime_SubmitAnswer_SameRequestIDTwice_SecondCallStillSucceeds(t *testing.T) {
	rt := &fullRT{}
	ctx, _, h, live := runtimeWithLiveSession(t, rt, 5)
	defer close(live)

	_, err := h.SubmitAnswer(ctx, wire.SubmitAnswerParams{ConversationID: convID(5), RequestID: "r-1"})
	require.NoError(t, err)

	// Real backends take-and-delete the waiter on first submit; simulate
	// the second call landing on an already-taken requestID.
	rt.submitAnswerErr = agentruntime.ErrWaiterNotFound
	_, err = h.SubmitAnswer(ctx, wire.SubmitAnswerParams{ConversationID: convID(5), RequestID: "r-1"})
	require.NoError(t, err)
	assert.Len(t, rt.submitAnswerCalls, 2)
}

func TestRuntime_SubmitAnswer_WaiterAlreadyGone_IdempotentSuccess(t *testing.T) {
	rt := &fullRT{submitAnswerErr: agentruntime.ErrWaiterNotFound}
	ctx, _, h, live := runtimeWithLiveSession(t, rt, 5)
	defer close(live)

	_, err := h.SubmitAnswer(ctx, wire.SubmitAnswerParams{ConversationID: convID(5), RequestID: "vanished"})
	require.NoError(t, err)
}

func TestRuntime_SubmitAnswer_SessionGone_IdempotentSuccess(t *testing.T) {
	// No live session ever registered for this sid — the R10 "daemon
	// restarted, session marked interrupted" case reduces to this same
	// resolveSession failure at the current (non-persistent) daemon.
	ctx, _, _, _, h := setupRuntimeTest(t, &fullRT{})
	_, err := h.SubmitAnswer(ctx, wire.SubmitAnswerParams{ConversationID: convID(999), RequestID: "r-1"})
	require.NoError(t, err)
}

func TestRuntime_SubmitToolPermission_SameRequestIDTwice_SecondCallStillSucceeds(t *testing.T) {
	rt := &fullRT{}
	ctx, _, h, live := runtimeWithLiveSession(t, rt, 6)
	defer close(live)

	_, err := h.SubmitToolPermission(ctx, wire.SubmitToolPermissionParams{ConversationID: convID(6), RequestID: "p-1", Allow: true})
	require.NoError(t, err)

	rt.submitToolPermErr = agentruntime.ErrWaiterNotFound
	_, err = h.SubmitToolPermission(ctx, wire.SubmitToolPermissionParams{ConversationID: convID(6), RequestID: "p-1", Allow: true})
	require.NoError(t, err)
	assert.Len(t, rt.submitToolPermCalls, 2)
}

func TestRuntime_SubmitToolPermission_WaiterAlreadyGone_IdempotentSuccess(t *testing.T) {
	rt := &fullRT{submitToolPermErr: agentruntime.ErrWaiterNotFound}
	ctx, _, h, live := runtimeWithLiveSession(t, rt, 6)
	defer close(live)

	_, err := h.SubmitToolPermission(ctx, wire.SubmitToolPermissionParams{ConversationID: convID(6), RequestID: "vanished"})
	require.NoError(t, err)
}

func TestRuntime_SubmitToolPermission_SessionGone_IdempotentSuccess(t *testing.T) {
	ctx, _, _, _, h := setupRuntimeTest(t, &fullRT{})
	_, err := h.SubmitToolPermission(ctx, wire.SubmitToolPermissionParams{ConversationID: convID(999), RequestID: "p-1"})
	require.NoError(t, err)
}

// TestRuntime_Submit_BackendNotRegistered_IsNotFoldedIntoSuccess 覆盖 R8 幂等的
// **边界**:会话是活的,但它的 backend 在本 daemon 上根本没注册(接线故障)。今天这条
// 路径与「会话不在 / 已结束」共用 ErrNoActiveTurn,于是被一并折成成功 —— 一台接错线的
// daemon 会把每一次决策提交都报成 OK,而没有任何 waiter 被回答,叠加 R9 的不设过期
// = 会话永久挂死,且客户端与运维两边都看不到任何异常。
//
// 收窄必须**不改过线错误码**:另外 7 个控制 RPC(steer / abort / setPermissionMode
// / goal.* …)在同一条 resolveSession 上返回同一个 sentinel,换码会让桌面端的
// errors.Is(err, ErrNoActiveTurn) 集体失效。所以这里同时钉住「报错」与「码不变」。
func TestRuntime_Submit_BackendNotRegistered_IsNotFoldedIntoSuccess(t *testing.T) {
	unwire := func(h *handlers.RuntimeHandlers) {
		h.SwapRuntimeFor(func(_ agent_backend_entity.BackendType) agentruntime.Runtime { return nil })
	}

	t.Run("submitAnswer", func(t *testing.T) {
		ctx, _, h, live := runtimeWithLiveSession(t, &fullRT{}, 5)
		defer close(live)
		unwire(h)

		_, err := h.SubmitAnswer(ctx, wire.SubmitAnswerParams{ConversationID: convID(5), RequestID: "r-1"})
		require.Error(t, err, "接线故障不是 R8 的幂等场景,不能报成 OK")
		code, ok := wire.CodeForSentinel(err)
		require.True(t, ok, "过线错误码必须仍然是既有 sentinel")
		assert.Equal(t, wire.ErrCodeNoActiveTurn, code, "其它 7 个控制 RPC 的错误码一字未改")
	})

	t.Run("submitToolPermission", func(t *testing.T) {
		ctx, _, h, live := runtimeWithLiveSession(t, &fullRT{}, 6)
		defer close(live)
		unwire(h)

		_, err := h.SubmitToolPermission(ctx, wire.SubmitToolPermissionParams{ConversationID: convID(6), RequestID: "p-1"})
		require.Error(t, err)
		code, ok := wire.CodeForSentinel(err)
		require.True(t, ok)
		assert.Equal(t, wire.ErrCodeNoActiveTurn, code)
	})
}

// TestRuntime_Submit_SessionOwnedByAnotherHandler 覆盖 R8 幂等的另一条**边界**:
// 会话在本 daemon 上还在跑,但提交落到了一个从没拥有过它的 RuntimeHandlers 上。
//
// 生产上这不是「多客户端」才有的事:registry 是全局一份,每条新接入的连接都把 13 个
// runtime.* 重新 Register 一遍(覆盖),而一台桌面端本来就同时握着 2-3 条连接(连接池
// 租约 / 设备监视心跳 / 刷新探测)。折成成功的话,桌面端 callSession 的「重挂后重试」
// 永不触发,客户端把「已送达」报给前端而没有任何 waiter 被回答 —— 叠加 R9 的不设过期
// 就是永久挂死。所以这一条必须如实报错,且错误码不变(callSession 只认 ErrNoActiveTurn)。
func TestRuntime_Submit_SessionOwnedByAnotherHandler_IsNotFoldedIntoSuccess(t *testing.T) {
	// live 会话:owner 起的那一轮不结束,会话行停在 running。
	newPair := func(t *testing.T, sid int64) (*fullRT, *handlers.RuntimeHandlers, *recordingSessions) {
		t.Helper()
		rt := &fullRT{}
		live := make(chan agentruntime.Event)
		t.Cleanup(func() { close(live) })
		rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
			return live, &agentruntime.RunResult{}, nil
		}
		sess := newRecordingSessions()
		owner := newRuntimeHandlersOn(rt, sess, newRecordingOutbound())
		runWithRT(t, owner, context.Background(), convID(sid))
		// 后接入那条连接的 handler:同一批会话行,自己的内存会话表是空的。
		return rt, newRuntimeHandlersOn(rt, sess, newRecordingOutbound()), sess
	}

	t.Run("submitAnswer", func(t *testing.T) {
		rt, other, _ := newPair(t, 11)
		_, err := other.SubmitAnswer(context.Background(), wire.SubmitAnswerParams{ConversationID: convID(11), RequestID: "r-1"})
		require.Error(t, err, "会话还在跑,提交没送达就不能报成 OK")
		code, ok := wire.CodeForSentinel(err)
		require.True(t, ok, "过线错误码必须仍然是既有 sentinel")
		assert.Equal(t, wire.ErrCodeNoActiveTurn, code, "桌面端 callSession 只认它才会重挂重试")
		assert.Empty(t, rt.submitAnswerCalls, "没有任何 waiter 被回答")
	})

	t.Run("submitToolPermission", func(t *testing.T) {
		rt, other, _ := newPair(t, 12)
		_, err := other.SubmitToolPermission(context.Background(), wire.SubmitToolPermissionParams{ConversationID: convID(12), RequestID: "p-1"})
		require.Error(t, err)
		code, ok := wire.CodeForSentinel(err)
		require.True(t, ok)
		assert.Equal(t, wire.ErrCodeNoActiveTurn, code)
		assert.Empty(t, rt.submitToolPermCalls)
	})

	// 接管之后就解得出会话了 —— 这正是客户端收到错误后走的那条路,证明报错是可行动的。
	t.Run("adopt then retry succeeds", func(t *testing.T) {
		rt, other, _ := newPair(t, 13)
		other.Adopt(context.Background(), convID(13), agent_backend_entity.TypeClaudeCode)
		_, err := other.SubmitToolPermission(context.Background(), wire.SubmitToolPermissionParams{ConversationID: convID(13), RequestID: "p-1"})
		require.NoError(t, err)
		require.Len(t, rt.submitToolPermCalls, 1)
	})

	// 判别依据读不出来时维持 R8 的幂等:证不了会话还在跑,就不拿一个坏掉的库去换
	// 用户面前一个假失败。
	t.Run("lifecycle unreadable stays idempotent", func(t *testing.T) {
		_, other, sess := newPair(t, 14)
		sess.mu.Lock()
		sess.findErr = errors.New("database is locked")
		sess.mu.Unlock()
		_, err := other.SubmitToolPermission(context.Background(), wire.SubmitToolPermissionParams{ConversationID: convID(14), RequestID: "p-1"})
		require.NoError(t, err)
	})
}

// TestRuntime_Submit_SessionNoLongerRunning_StaysIdempotent 钉住 R8 本身:轮次确已
// 结束(idle)、或那一轮的子进程随上一个 daemon 进程消亡(interrupted)、或这条会话
// 根本不在本 daemon 上(查无此行),提交一律照旧幂等返回成功 —— 重连的客户端分不清
// 自己上一次提交到没到,报错会让它对着用户误报失败。
func TestRuntime_Submit_SessionNoLongerRunning_StaysIdempotent(t *testing.T) {
	states := map[string]string{
		"idle":        wire.SessionLifecycleIdle,
		"interrupted": wire.SessionLifecycleInterrupted,
	}
	for name, state := range states {
		t.Run(name, func(t *testing.T) {
			rt := &fullRT{}
			sess := newRecordingSessions()
			other := newRuntimeHandlersOn(rt, sess, newRecordingOutbound())
			// 会话行在库里,但已经不是 running。
			sess.setLifecycle("", "21", state)

			_, err := other.SubmitAnswer(context.Background(), wire.SubmitAnswerParams{ConversationID: convID(21), RequestID: "r-1"})
			require.NoError(t, err)
			_, err = other.SubmitToolPermission(context.Background(), wire.SubmitToolPermissionParams{ConversationID: convID(21), RequestID: "p-1"})
			require.NoError(t, err)
		})
	}

	t.Run("no such session", func(t *testing.T) {
		rt := &fullRT{}
		sess := newRecordingSessions()
		other := newRuntimeHandlersOn(rt, sess, newRecordingOutbound())

		_, err := other.SubmitAnswer(context.Background(), wire.SubmitAnswerParams{ConversationID: convID(22), RequestID: "r-1"})
		require.NoError(t, err)
		_, err = other.SubmitToolPermission(context.Background(), wire.SubmitToolPermissionParams{ConversationID: convID(22), RequestID: "p-1"})
		require.NoError(t, err)
	})
}

// TestRuntime_Submit_DuringTurnTeardown_StaysIdempotent 钉住轮末那一瞬间:fanout 正在
// 收尾 —— 这一轮已经结束,而生命周期行还没落回 idle —— 而一条决策提交恰好落在这里。
//
// 它必须仍按 R8 幂等成功。这一瞬间的「解不出会话」是本 daemon 自己的收尾顺序造成的,
// 不是「提交落到了一个从没拥有过这条会话的 handler」;报错等于给用户一个假失败。窗口
// 也不是一闪而过:Finish 是一次同步的 SQLite 写,与流式落库抢锁时能拖到几十毫秒以上。
//
// 收尾顺序因此是:先把行落回 idle,再摘掉内存会话表 —— 两者之间落进来的提交解得出会话,
// 照旧走到 backend,由「waiter 已经不在了」按 R8 折成成功。反过来(先摘表)那一瞬间的
// 提交会看到「表里没有 + 行还在跑」,正是本轮新加的那条判别器判成真错误的形状。
func TestRuntime_Submit_DuringTurnTeardown_StaysIdempotent(t *testing.T) {
	rt := &fullRT{}
	live := make(chan agentruntime.Event)
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		return live, &agentruntime.RunResult{}, nil
	}
	sess := newRecordingSessions()
	h := newRuntimeHandlersOn(rt, sess, newRecordingOutbound())
	runWithRT(t, h, context.Background(), convID(31))

	entered, release := sess.holdFinish()
	close(live) // 一轮结束 → fanout 开始收尾
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("fanout 没有走到轮末收尾")
	}
	defer release()

	_, err := h.SubmitToolPermission(context.Background(),
		wire.SubmitToolPermissionParams{ConversationID: convID(31), RequestID: "p-1"})
	assert.NoError(t, err, "轮末收尾中的提交按 R8 幂等成功,不能变成假失败")
	_, err = h.SubmitAnswer(context.Background(),
		wire.SubmitAnswerParams{ConversationID: convID(31), RequestID: "r-1"})
	assert.NoError(t, err, "轮末收尾中的提交按 R8 幂等成功,不能变成假失败")
}

// TestRuntime_AllEventsRoundTripThroughNotify proves every sealed Event type
// can be pumped through the notify fanout (i.e. the JSON marshal step in
// the Run handler tolerates all 19 kinds without panic / silent drop).
func TestRuntime_AllEventsRoundTripThroughNotify(t *testing.T) {
	events := []agentruntime.Event{
		agentruntime.TextDelta{Text: "t"},
		agentruntime.ThinkingDelta{Text: "th"},
		agentruntime.PermissionModeChanged{Mode: "plan"},
		agentruntime.ContextWindowUpdated{Tokens: 200000},
		agentruntime.PlanUpdated{},
		agentruntime.UsageUpdate{},
		agentruntime.Retry{Message: "rate limited"},
		agentruntime.SteerConsumed{},
		agentruntime.Done{},
	}
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event, len(events))
		for _, e := range events {
			ch <- e
		}
		close(ch)
		return ch, &agentruntime.RunResult{}, nil
	}
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)

	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}
	_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(100)})
	require.NoError(t, err)

	frames := notif.waitFrames(t, len(events)+1) // +1 for runResultDone

	// Every event frame must carry ConversationID + a non-empty Event RawMessage
	// whose top-level "kind" is non-empty.
	for i := range events {
		f := frames[i]
		assert.Equal(t, wire.NotifyEvent, f.method)
		ef, ok := f.params.(*wire.EventFrame)
		require.True(t, ok, "frame %d: expected EventFrame, got %T", i, f.params)
		assert.Equal(t, convID(100), ef.ConversationID)
		assert.NotNil(t, ef.Event, "frame %d must carry an event", i)
	}
}

// ── 会话通知出口:先落库,后推送 ───────────────────────────────────────────

// assertJournaledBeforePushed 断言时间线上每条推送都排在它自己那条落库之后(R1),
// 且没有任何一条通知是没落库就推出去的。按 (method, seq) 精确配对,不只数条数。
func assertJournaledBeforePushed(t *testing.T, steps []string) {
	t.Helper()
	appendedAt := map[string]int{}
	for i, s := range steps {
		if key, ok := strings.CutPrefix(s, "append:"); ok {
			appendedAt[key] = i
		}
	}
	for i, s := range steps {
		key, ok := strings.CutPrefix(s, "notify:")
		if !ok {
			continue
		}
		at, journaled := appendedAt[key]
		require.Truef(t, journaled, "推送了一条没落库的通知 %s;时间线 %v", key, steps)
		assert.Lessf(t, at, i, "通知 %s 的推送排在它自己的落库之前;时间线 %v", key, steps)
	}
}

func journalSeqs(rows []journalRow) []int64 {
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.seq)
	}
	return out
}

func journalMethods(rows []journalRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.method)
	}
	return out
}

// TestRuntime_Run_JournalsEveryNotificationBeforePushingWithSeq 覆盖 R1 与 R6 的 daemon
// 半边:五类会话通知(runtime.event / runtime.runResultDone / autonomousTurn.started /
// .event / .done)每一条都先以下一个 seq 落进通知日志,落库成功之后才带着这个 seq 推出去。
// 日志里存的是不含 seq 的帧原样,seq 是行自己的属性。
// 会拒绝的错误实现:直接推不落库;落了库但帧上不盖 seq;推完再补落库;只覆盖 run 流的
// 两类而漏掉自主续轮的三类。
func TestRuntime_Run_JournalsEveryNotificationBeforePushingWithSeq(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event, 1)
		ch <- agentruntime.TextDelta{Text: "hi"}
		close(ch)
		return ch, &agentruntime.RunResult{ProviderSessionID: "psid-1"}, nil
	}
	rt.autoFn = func(_ int64) <-chan agentruntime.AutonomousTurn {
		out := make(chan agentruntime.AutonomousTurn, 1)
		evs := make(chan agentruntime.Event, 1)
		evs <- agentruntime.TextDelta{Text: "autonomous"}
		close(evs)
		out <- agentruntime.AutonomousTurn{Events: evs, Result: &agentruntime.RunResult{}, Trigger: "background_task"}
		close(out)
		return out
	}
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)

	be := agent_backend_entity.AgentBackend{ID: 1, Type: string(agent_backend_entity.TypeClaudeCode), Name: "x"}
	_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(42), Cwd: "/tmp", UserText: "hi"})
	require.NoError(t, err)

	// run 流 2 条(event + runResultDone)+ 自主续轮 3 条(started + event + done)。
	frames := notif.waitFrames(t, 5)
	rows := notif.journalRows()

	require.Len(t, rows, 5, "五类通知每条都必须落库")
	assert.ElementsMatch(t, []int64{1, 2, 3, 4, 5}, journalSeqs(rows), "seq 从 1 起单调无洞")
	assert.ElementsMatch(t, []string{
		wire.NotifyEvent,
		wire.NotifyRunResultDone,
		wire.NotifyAutonomousTurnStarted,
		wire.NotifyAutonomousTurnEvent,
		wire.NotifyAutonomousTurnDone,
	}, journalMethods(rows), "五类通知一条都不能漏")

	bySeq := map[int64]journalRow{}
	for _, r := range rows {
		bySeq[r.seq] = r
		assert.Equal(t, convID(42), r.session, "落库的会话身份就是线上那条 conversation_id")
		assert.NotContains(t, r.payload, `"seq"`, "日志里存的是不含 seq 的帧原样,seq 是行自己的列")
	}

	// 每条推出去的帧都盖着它那条日志行的 seq,且 method 与该行一致。
	pushedSeqs := make([]int64, 0, len(frames))
	for _, f := range frames {
		seq := frameSeq(f.params)
		row, ok := bySeq[seq]
		require.Truef(t, ok, "推送帧带的 seq=%d 在日志里不存在(method=%s)", seq, f.method)
		assert.Equal(t, row.method, f.method)
		pushedSeqs = append(pushedSeqs, seq)
	}
	assert.ElementsMatch(t, []int64{1, 2, 3, 4, 5}, pushedSeqs, "每条推送都带自己的 seq")

	assertJournaledBeforePushed(t, notif.stepLog())

	// 落库与推送必须用同一个对端身份 —— 单测的 ctx 上没有连接,指纹为空串,这里钉的是
	// 「两边取的是同一个值」,真实指纹的捕获由带真连接的集成路径覆盖。
	resolved := notif.resolvedPeers()
	require.NotEmpty(t, resolved, "推送目标必须在发送时解析")
	for _, peer := range resolved {
		assert.Equal(t, rows[0].peer, peer)
	}
}

// TestRuntime_Run_PushFailureLeavesJournalIntact 覆盖 R2:推送失败(连接已死 / 写超时)
// 时该条通知已经落库、seq 已经推进,daemon 记日志后继续处理下一条 —— 不回滚、不重试、
// 不阻塞后续通知。会拒绝的错误实现:推送失败就删日志行 / 重试 / 中断 fanout。
func TestRuntime_Run_PushFailureLeavesJournalIntact(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event, 2)
		ch <- agentruntime.TextDelta{Text: "one"}
		ch <- agentruntime.TextDelta{Text: "two"}
		close(ch)
		return ch, &agentruntime.RunResult{ProviderSessionID: "psid-1"}, nil
	}
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)
	notif.notifyFail = func(string) error { return errors.New("connection reset by peer") }

	be := agent_backend_entity.AgentBackend{ID: 1, Type: string(agent_backend_entity.TypePiAgent), Name: "x"}
	_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(42)})
	require.NoError(t, err)

	// 2 条 event + 1 条 runResultDone,每条一次落库 + 一次失败推送 = 6 步。
	steps := notif.waitSteps(t, 6)

	rows := notif.journalRows()
	require.Len(t, rows, 3, "推送失败不影响落库")
	assert.Equal(t, []int64{1, 2, 3}, journalSeqs(rows), "推送失败不回滚 seq,后续通知照常推进")
	assert.Equal(t, []string{wire.NotifyEvent, wire.NotifyEvent, wire.NotifyRunResultDone}, journalMethods(rows),
		"第一条推送失败后,后面的通知仍然继续落库")
	assert.Empty(t, notif.snapshot(), "推送全失败时不该有任何帧被记下")

	attempts := 0
	for _, s := range steps {
		if strings.HasPrefix(s, "notify-failed:") {
			attempts++
		}
	}
	assert.Equal(t, 3, attempts, "每条通知只尝试推一次,不重试")
}

// TestRuntime_Run_JournalFailureSkipsPushAndDoesNotAdvanceSeq 覆盖 R3:落库失败时该条
// 通知不推送,seq 也不推进 —— 后面那条通知拿到的是紧接着的 seq,客户端看到的连续 seq
// 因此仍是完整序列。会拒绝的错误实现:落库失败照样推;或 handler 自己维护一个计数器当
// seq(那样后一条会带 3 而不是 2,客户端会误判丢了一条并触发无谓补洞)。
func TestRuntime_Run_JournalFailureSkipsPushAndDoesNotAdvanceSeq(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event, 3)
		ch <- agentruntime.TextDelta{Text: "ok-1"}
		ch <- agentruntime.TextDelta{Text: "boom"}
		ch <- agentruntime.TextDelta{Text: "ok-2"}
		close(ch)
		return ch, &agentruntime.RunResult{}, nil
	}
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)
	notif.appendFail = func(_ string, payload json.RawMessage) error {
		if strings.Contains(string(payload), "boom") {
			return errors.New("disk I/O error")
		}
		return nil
	}

	be := agent_backend_entity.AgentBackend{ID: 1, Type: string(agent_backend_entity.TypePiAgent), Name: "x"}
	_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(42)})
	require.NoError(t, err)

	// 落库成功的 3 条(ok-1 / ok-2 / runResultDone)会被推出去;失败那条不推。
	frames := notif.waitFrames(t, 3)
	require.Len(t, frames, 3)

	for _, f := range frames {
		if ef, ok := f.params.(*wire.EventFrame); ok {
			assert.NotContains(t, eventText(t, ef.Event), "boom", "落库失败的通知不得推送")
		}
	}
	assert.Equal(t, []int64{1, 2, 3}, []int64{
		frameSeq(frames[0].params), frameSeq(frames[1].params), frameSeq(frames[2].params),
	}, "落库失败不推进 seq:后一条拿到的是紧接着的 seq,不是跳号")

	rows := notif.journalRows()
	require.Len(t, rows, 3, "失败那条不落行")
	assert.Equal(t, []int64{1, 2, 3}, journalSeqs(rows))
	assert.Contains(t, notif.stepLog(), "append-failed:"+wire.NotifyEvent)
}

// TestRuntime_Run_OfflinePeerJournalsAndResumesPushingOnReconnect 覆盖断连场景下的出口
// 行为:对端不在线时通知照样落库(重连后才补得齐),而推送目标是**发送那一刻**才解析的
// —— 对端回来之后,同一轮里后续的通知立刻又推得出去。会拒绝的错误实现:把推送端口在
// runtime.run 期间静态捕获(重连后所有通知永远发往那条死连接),或对端不在线时干脆不落库。
func TestRuntime_Run_OfflinePeerJournalsAndResumesPushingOnReconnect(t *testing.T) {
	rt := &fullRT{}
	live := make(chan agentruntime.Event)
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		return live, &agentruntime.RunResult{}, nil
	}
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)
	notif.setOffline(true) // 客户端此刻断开着

	be := agent_backend_entity.AgentBackend{ID: 1, Type: string(agent_backend_entity.TypePiAgent), Name: "x"}
	_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(42)})
	require.NoError(t, err)

	live <- agentruntime.TextDelta{Text: "while-offline"}
	notif.waitSteps(t, 1)
	require.Len(t, notif.journalRows(), 1, "对端不在线也必须落库")
	assert.Empty(t, notif.snapshot(), "没有活连接时不推送")

	notif.setOffline(false) // 客户端重连
	live <- agentruntime.TextDelta{Text: "after-reconnect"}
	frames := notif.waitFrames(t, 1)
	require.Len(t, frames, 1)
	assert.Equal(t, int64(2), frameSeq(frames[0].params), "断连期间那条已经占了 seq=1")

	close(live)
	frames = notif.waitFrames(t, 2)
	assert.Equal(t, wire.NotifyRunResultDone, frames[1].method)
	assert.Equal(t, int64(3), frameSeq(frames[1].params))
	assert.Equal(t, []int64{1, 2, 3}, journalSeqs(notif.journalRows()))
	assert.Len(t, notif.resolvedPeers(), 3,
		"每条通知都要重新解析一次推送目标(解析一次就缓存下来的实现会一直推给旧连接)")
}

type blockingPreparedPiRT struct {
	entered chan struct{}
	once    sync.Once
}

func (r *blockingPreparedPiRT) Run(ctx context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	r.once.Do(func() { close(r.entered) })
	<-ctx.Done()
	return nil, nil, ctx.Err()
}

func (r *blockingPreparedPiRT) PrepareRun(ctx context.Context, _ agentruntime.RunRequest) (piagentrt.PreparedRun, error) {
	r.once.Do(func() { close(r.entered) })
	<-ctx.Done()
	return nil, ctx.Err()
}

type blockingStartPreparedPiRT struct {
	prepared *blockingStartPreparedPiRun
}

func newBlockingStartPreparedPiRT() *blockingStartPreparedPiRT {
	return &blockingStartPreparedPiRT{prepared: &blockingStartPreparedPiRun{
		entered: make(chan struct{}),
		closed:  make(chan struct{}),
	}}
}

func (r *blockingStartPreparedPiRT) PrepareRun(context.Context, agentruntime.RunRequest) (piagentrt.PreparedRun, error) {
	return r.prepared, nil
}

type blockingStartPreparedPiRun struct {
	entered   chan struct{}
	closed    chan struct{}
	enterOnce sync.Once
	closeOnce sync.Once
}

func (p *blockingStartPreparedPiRun) Start(ctx context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	p.enterOnce.Do(func() { close(p.entered) })
	<-ctx.Done()
	return nil, nil, ctx.Err()
}

func (*blockingStartPreparedPiRun) ProviderSessionID() string { return "pi-session-new" }

func (p *blockingStartPreparedPiRun) Close(context.Context) error {
	p.closeOnce.Do(func() { close(p.closed) })
	return nil
}

type cancelReturningStartPiRT struct {
	prepared *cancelReturningStartPiRun
}

func newCancelReturningStartPiRT() *cancelReturningStartPiRT {
	return &cancelReturningStartPiRT{prepared: &cancelReturningStartPiRun{
		started:  make(chan struct{}),
		closeErr: errors.New("prepared close failed"),
	}}
}

func (r *cancelReturningStartPiRT) PrepareRun(context.Context, agentruntime.RunRequest) (piagentrt.PreparedRun, error) {
	return r.prepared, nil
}

type cancelReturningStartPiRun struct {
	mu        sync.Mutex
	started   chan struct{}
	startOnce sync.Once
	closeErr  error
	closes    int
}

func (p *cancelReturningStartPiRun) Start(ctx context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	p.startOnce.Do(func() { close(p.started) })
	<-ctx.Done()
	events := make(chan agentruntime.Event)
	close(events)
	return events, &agentruntime.RunResult{ProviderSessionID: p.ProviderSessionID()}, nil
}

func (*cancelReturningStartPiRun) ProviderSessionID() string { return "pi-session-cancel-race" }

func (p *cancelReturningStartPiRun) Close(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closes++
	return p.closeErr
}

func (p *cancelReturningStartPiRun) closeCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closes
}

type settlingAcceptedPiRT struct {
	prepared *settlingAcceptedPiRun
}

func newSettlingAcceptedPiRT() *settlingAcceptedPiRT {
	return &settlingAcceptedPiRT{prepared: &settlingAcceptedPiRun{
		events:       make(chan agentruntime.Event),
		result:       &agentruntime.RunResult{ProviderSessionID: "pi-session-accepted"},
		closeEntered: make(chan struct{}),
	}}
}

func (r *settlingAcceptedPiRT) PrepareRun(context.Context, agentruntime.RunRequest) (piagentrt.PreparedRun, error) {
	return r.prepared, nil
}

func (r *settlingAcceptedPiRT) Abort(context.Context, int64, uint64) (agentruntime.AbortOutcome, error) {
	r.prepared.result.UserAnchor = "pi-user-anchor-after-stop"
	r.prepared.result.StopErr = agentruntime.ErrAborted
	r.prepared.finish()
	return agentruntime.AbortOutcome{}, nil
}

type settlingAcceptedPiRun struct {
	mu           sync.Mutex
	events       chan agentruntime.Event
	result       *agentruntime.RunResult
	closeEntered chan struct{}
	finishOnce   sync.Once
	closeOnce    sync.Once
	closeCalls   int
}

type blockingAbortAcceptedPiRT struct {
	prepared     *settlingAcceptedPiRun
	abortEntered chan struct{}
	allowAbort   chan struct{}
	abortOnce    sync.Once
}

func newBlockingAbortAcceptedPiRT() *blockingAbortAcceptedPiRT {
	return &blockingAbortAcceptedPiRT{
		prepared: &settlingAcceptedPiRun{
			events:       make(chan agentruntime.Event),
			result:       &agentruntime.RunResult{ProviderSessionID: "pi-session-concurrent"},
			closeEntered: make(chan struct{}),
		},
		abortEntered: make(chan struct{}),
		allowAbort:   make(chan struct{}),
	}
}

func (r *blockingAbortAcceptedPiRT) PrepareRun(context.Context, agentruntime.RunRequest) (piagentrt.PreparedRun, error) {
	return r.prepared, nil
}

func (r *blockingAbortAcceptedPiRT) Abort(context.Context, int64, uint64) (agentruntime.AbortOutcome, error) {
	r.abortOnce.Do(func() { close(r.abortEntered) })
	<-r.allowAbort
	r.prepared.finish()
	return agentruntime.AbortOutcome{}, nil
}

func (p *settlingAcceptedPiRun) Start(context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	return p.events, p.result, nil
}

func (*settlingAcceptedPiRun) ProviderSessionID() string { return "pi-session-accepted" }

func (p *settlingAcceptedPiRun) Close(context.Context) error {
	p.mu.Lock()
	p.closeCalls++
	p.mu.Unlock()
	p.closeOnce.Do(func() { close(p.closeEntered) })
	p.finish()
	return nil
}

func (p *settlingAcceptedPiRun) finish() {
	p.finishOnce.Do(func() { close(p.events) })
}

func (p *settlingAcceptedPiRun) closes() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closeCalls
}

type scriptedPreparedPiRT struct {
	mu       sync.Mutex
	prepared []*scriptedPreparedPiRun
	requests []agentruntime.RunRequest
	active   *scriptedPreparedPiRun
}

func newScriptedPreparedPiRT(ids ...string) *scriptedPreparedPiRT {
	r := &scriptedPreparedPiRT{}
	for _, id := range ids {
		r.prepared = append(r.prepared, &scriptedPreparedPiRun{
			owner:             r,
			providerSessionID: id,
			events:            make(chan agentruntime.Event),
			result:            &agentruntime.RunResult{ProviderSessionID: id},
			closed:            make(chan struct{}),
		})
	}
	return r
}

func (r *scriptedPreparedPiRT) PrepareRun(_ context.Context, req agentruntime.RunRequest) (piagentrt.PreparedRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
	idx := len(r.requests) - 1
	if idx >= len(r.prepared) {
		return nil, errors.New("unexpected Pi preparation")
	}
	return r.prepared[idx], nil
}

// gatedPreparedPiRT 把 PrepareRun 停在中途直到测试放行,用于制造「准备期间这条会话
// 被别人接管」的时序。
type gatedPreparedPiRT struct {
	*scriptedPreparedPiRT
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newGatedPreparedPiRT(id string) *gatedPreparedPiRT {
	return &gatedPreparedPiRT{
		scriptedPreparedPiRT: newScriptedPreparedPiRT(id),
		entered:              make(chan struct{}),
		release:              make(chan struct{}),
	}
}

func (r *gatedPreparedPiRT) PrepareRun(ctx context.Context, req agentruntime.RunRequest) (piagentrt.PreparedRun, error) {
	r.once.Do(func() { close(r.entered) })
	<-r.release
	return r.scriptedPreparedPiRT.PrepareRun(ctx, req)
}

func (r *scriptedPreparedPiRT) Abort(context.Context, int64, uint64) (agentruntime.AbortOutcome, error) {
	r.mu.Lock()
	active := r.active
	r.mu.Unlock()
	if active == nil {
		return agentruntime.AbortOutcome{}, agentruntime.ErrNoActiveTurn
	}
	active.result.StopErr = agentruntime.ErrAborted
	active.finish()
	return agentruntime.AbortOutcome{}, nil
}

type scriptedPreparedPiRun struct {
	mu                sync.Mutex
	owner             *scriptedPreparedPiRT
	providerSessionID string
	events            chan agentruntime.Event
	result            *agentruntime.RunResult
	closed            chan struct{}
	finishOnce        sync.Once
	closeOnce         sync.Once
	startCalls        int
	closeCalls        int
}

func (p *scriptedPreparedPiRun) ProviderSessionID() string { return p.providerSessionID }

func (p *scriptedPreparedPiRun) Start(context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	p.mu.Lock()
	p.startCalls++
	p.mu.Unlock()
	p.owner.mu.Lock()
	p.owner.active = p
	p.owner.mu.Unlock()
	return p.events, p.result, nil
}

func (p *scriptedPreparedPiRun) Close(context.Context) error {
	p.mu.Lock()
	p.closeCalls++
	p.mu.Unlock()
	p.closeOnce.Do(func() { close(p.closed) })
	p.finish()
	return nil
}

func (p *scriptedPreparedPiRun) counts() (start, closed int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.startCalls, p.closeCalls
}

func (p *scriptedPreparedPiRun) finish() {
	p.finishOnce.Do(func() { close(p.events) })
}

type trackingGenerationRegistry struct {
	mu       sync.Mutex
	owners   map[int64]string
	releases map[string]int
}

func newTrackingGenerationRegistry() *trackingGenerationRegistry {
	return &trackingGenerationRegistry{
		owners:   map[int64]string{},
		releases: map[string]int{},
	}
}

func (r *trackingGenerationRegistry) ClaimConnection(_ connection.Conn, sessionID int64, generation string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owners[sessionID] != "" {
		return false
	}
	r.owners[sessionID] = generation
	return true
}

func (r *trackingGenerationRegistry) ReleaseConnection(_ connection.Conn, sessionID int64, generation string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owners[sessionID] != generation {
		return false
	}
	delete(r.owners, sessionID)
	r.releases[generation]++
	return true
}

func (r *trackingGenerationRegistry) owner(sessionID int64) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.owners[sessionID]
}

func (r *trackingGenerationRegistry) releaseCount(generation string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.releases[generation]
}

type blockingTerminalNotifier struct {
	recording *recordingOutbound
	entered   chan struct{}
	allow     chan struct{}
	once      sync.Once
}

type doneObservingContext struct {
	context.Context
	observed chan struct{}
	done     chan struct{}
	once     sync.Once
}

func newDoneObservingContext() *doneObservingContext {
	return &doneObservingContext{
		Context:  context.Background(),
		observed: make(chan struct{}),
		done:     make(chan struct{}),
	}
}

func (c *doneObservingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.done
}

func (n *blockingTerminalNotifier) Notify(notification *agentrewire.RpcNotification) error {
	if protowire.NotificationMethod(notification) == wire.NotifyRunResultDone {
		n.once.Do(func() { close(n.entered) })
		<-n.allow
	}
	return n.recording.Notify(notification)
}

func TestRuntime_PiPendingGenerationIsAbortableBeforePreparationReturns(t *testing.T) {
	rt := &blockingPreparedPiRT{entered: make(chan struct{})}
	ctx, _, _, _, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)}

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	params := wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(41), PermissionMode: "generation-41"}
	_, err := h.Run(runCtx, params)
	require.NoError(t, err)
	errC := make(chan error, 1)
	go func() {
		_, err := h.Run(runCtx, params)
		errC <- err
	}()
	<-rt.entered

	_, err = h.Abort(ctx, wire.AbortParams{ConversationID: convID(41)})
	require.NoError(t, err)
	require.ErrorIs(t, <-errC, context.Canceled)
}

func TestRuntime_ConnectionCloseCancelsPendingPiPreparation(t *testing.T) {
	rt := &blockingPreparedPiRT{entered: make(chan struct{})}
	ctx, _, _, _, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)}
	params := wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(141), PermissionMode: "generation-141"}

	_, err := h.Run(ctx, params)
	require.NoError(t, err)
	prepareErrC := make(chan error, 1)
	go func() {
		_, prepareErr := h.Run(ctx, params)
		prepareErrC <- prepareErr
	}()
	<-rt.entered

	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), time.Second)
	defer cancelCleanup()
	require.NoError(t, h.Close(cleanupCtx))
	require.ErrorIs(t, <-prepareErrC, context.Canceled)
	_, err = h.Abort(ctx, wire.AbortParams{ConversationID: convID(141)})
	require.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
}

func TestRuntime_ConnectionCloseClosesPreparedPiResourcesBeforeStart(t *testing.T) {
	rt := newScriptedPreparedPiRT("pi-session-prepared")
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)}
	params := wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(142), PermissionMode: "generation-142"}

	_, err := h.Run(ctx, params)
	require.NoError(t, err)
	_, err = h.Run(ctx, params)
	require.NoError(t, err)

	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), time.Second)
	defer cancelCleanup()
	require.NoError(t, h.Close(cleanupCtx))
	started, closed := rt.prepared[0].counts()
	assert.Zero(t, started)
	assert.Equal(t, 1, closed, "disconnect must close the exact prepared Pi process")
	assert.Empty(t, notif.snapshot(), "prepared cleanup must not emit terminal frames to the closed connection")
}

func TestRuntime_ConnectionCloseClosesRunningPiResourcesWithoutTerminalNotify(t *testing.T) {
	rt := newScriptedPreparedPiRT("pi-session-running")
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)}
	params := wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(143), PermissionMode: "generation-143"}

	_, err := h.Run(ctx, params)
	require.NoError(t, err)
	ack, err := h.Run(ctx, params)
	require.NoError(t, err)
	params.ProviderSessionID = ack.ProviderSessionID
	_, err = h.Run(ctx, params)
	require.NoError(t, err)

	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), time.Second)
	defer cancelCleanup()
	require.NoError(t, h.Close(cleanupCtx))
	started, closed := rt.prepared[0].counts()
	assert.Equal(t, 1, started)
	assert.Equal(t, 1, closed, "disconnect must close the running Pi process/tool tree")
	assert.Empty(t, notif.snapshot(), "disconnect cleanup must suppress terminal fanout to the closed connection")
}

func TestRuntime_ConnectionCloseWaitsForConcurrentExplicitAbortWithoutDeadlock(t *testing.T) {
	rt := newBlockingAbortAcceptedPiRT()
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)}
	params := wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(144), PermissionMode: "generation-144"}

	_, err := h.Run(ctx, params)
	require.NoError(t, err)
	ack, err := h.Run(ctx, params)
	require.NoError(t, err)
	params.ProviderSessionID = ack.ProviderSessionID
	_, err = h.Run(ctx, params)
	require.NoError(t, err)

	abortErrC := make(chan error, 1)
	go func() {
		_, abortErr := h.Abort(ctx, wire.AbortParams{ConversationID: convID(144)})
		abortErrC <- abortErr
	}()
	<-rt.abortEntered

	cleanupErrC := make(chan error, 1)
	go func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), time.Second)
		defer cancelCleanup()
		cleanupErrC <- h.Close(cleanupCtx)
	}()
	select {
	case <-rt.prepared.closeEntered:
	case <-time.After(time.Second):
		t.Fatal("disconnect cleanup did not close the exact prepared resource while Stop was in flight")
	}
	assert.Empty(t, notif.snapshot(), "disconnect must suppress terminal fanout before Stop settles")
	close(rt.allowAbort)

	require.NoError(t, <-abortErrC)
	require.NoError(t, <-cleanupErrC)
	assert.Equal(t, 1, rt.prepared.closes())
	assert.Empty(t, notif.snapshot(), "concurrent Stop and disconnect must not emit to the closed connection")
}

func TestRuntime_PiAbortStartRaceFinalizesOwnerAndAllowsRetry(t *testing.T) {
	for iteration := 0; iteration < 25; iteration++ {
		t.Run(fmt.Sprintf("iteration-%d", iteration), func(t *testing.T) {
			rt := newCancelReturningStartPiRT()
			ctrl := gomock.NewController(t)
			t.Cleanup(ctrl.Finish)
			registry := newTrackingGenerationRegistry()
			outbound := newRecordingOutbound()
			sessions := newRecordingSessions()
			h := handlers.NewRuntimeHandlers(handlers.RuntimeDeps{
				NotifyFor:          outbound.notifierFor,
				Journal:            outbound,
				Sessions:           sessions,
				SessionQuery:       sessions,
				Gateway:            mock_handlers.NewMockGatewayPort(ctrl),
				Lookup:             mock_handlers.NewMockLLMProviderLookupPort(ctrl),
				GenerationRegistry: registry,
				RuntimeFor: func(_ agent_backend_entity.BackendType) agentruntime.Runtime {
					return rt
				},
			})
			ctx := context.Background()
			be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)}
			params := wire.RunParams{
				Backend: backendJSON(t, be), ConversationID: convID(244), PermissionMode: "generation-racing",
			}

			_, err := h.Run(ctx, params)
			require.NoError(t, err)
			ack, err := h.Run(ctx, params)
			require.NoError(t, err)
			params.ProviderSessionID = ack.ProviderSessionID
			startErrC := make(chan error, 1)
			go func() {
				_, startErr := h.Run(ctx, params)
				startErrC <- startErr
			}()
			<-rt.prepared.started

			_, abortErr := h.Abort(ctx, wire.AbortParams{ConversationID: params.ConversationID})
			require.ErrorContains(t, abortErr, "prepared close failed")
			require.ErrorIs(t, <-startErrC, context.Canceled)
			assert.Equal(t, 1, rt.prepared.closeCalls(), "the exact prepared owner must close once")
			assert.Equal(t, 1, registry.releaseCount("generation-racing"))

			retry := params
			retry.ProviderSessionID = ""
			retry.PermissionMode = "generation-retry"
			_, err = h.Run(ctx, retry)
			require.NoError(t, err, "cancel completion must release registration for an exact retry")
			assert.Equal(t, "generation-retry", registry.owner(handlers.RuntimeSessionKey(retry.ConversationID)))
			_, err = h.Abort(ctx, wire.AbortParams{ConversationID: retry.ConversationID})
			require.NoError(t, err)
			assert.Empty(t, registry.owner(handlers.RuntimeSessionKey(retry.ConversationID)))
		})
	}
}

func TestRuntime_PiAbortDuringPromptAcknowledgementClosesExactPreparedProcess(t *testing.T) {
	rt := newBlockingStartPreparedPiRT()
	ctx, _, _, _, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)}
	params := wire.RunParams{
		Backend: backendJSON(t, be), ConversationID: convID(44), ProviderSessionID: "pi-session-old", PermissionMode: "generation-44",
	}

	_, err := h.Run(ctx, params)
	require.NoError(t, err)
	ack, err := h.Run(ctx, params)
	require.NoError(t, err)
	params.ProviderSessionID = ack.ProviderSessionID
	startErrC := make(chan error, 1)
	go func() {
		_, err := h.Run(ctx, params)
		startErrC <- err
	}()
	<-rt.prepared.entered

	_, err = h.Abort(ctx, wire.AbortParams{ConversationID: convID(44)})
	require.NoError(t, err)
	require.ErrorIs(t, <-startErrC, context.Canceled)
	<-rt.prepared.closed
}

func TestRuntime_PiPrepareReturnsIdentityBeforeSecondRunStartsPrompt(t *testing.T) {
	rt := newScriptedPreparedPiRT("pi-session-new")
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)}
	params := wire.RunParams{
		Backend:           backendJSON(t, be),
		ConversationID:    convID(42),
		ProviderSessionID: "pi-session-old",
		ForkAnchor:        "pi-entry-1",
		UserText:          "replacement",
		PermissionMode:    "generation-42",
	}

	registrationAck, err := h.Run(ctx, params)
	require.NoError(t, err)
	assert.Empty(t, registrationAck.ProviderSessionID)
	ack, err := h.Run(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, "pi-session-new", ack.ProviderSessionID)
	require.Len(t, rt.requests, 1)
	assert.Empty(t, rt.requests[0].PermissionMode, "transport generation ownership must not reach Pi runtime")
	assert.Equal(t, "pi-session-old", rt.requests[0].ProviderSessionID)
	assert.Equal(t, "pi-entry-1", rt.requests[0].ForkAnchor)
	startCalls, _ := rt.prepared[0].counts()
	assert.Zero(t, startCalls, "the preparation response must precede prompt Start")
	assert.Empty(t, notif.snapshot(), "registration and preparation must not emit turn events")

	params.ProviderSessionID = ack.ProviderSessionID
	startAck, err := h.Run(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, "pi-session-new", startAck.ProviderSessionID)
	startCalls, _ = rt.prepared[0].counts()
	assert.Equal(t, 1, startCalls)

	rt.prepared[0].finish()
	frames := notif.waitFrames(t, 1)
	assert.Equal(t, wire.NotifyRunResultDone, frames[0].method)
}

func TestRuntime_PiAbortSettlesAcceptedTurnBeforeClosingPreparedProcess(t *testing.T) {
	rt := newSettlingAcceptedPiRT()
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)}
	params := wire.RunParams{
		Backend: backendJSON(t, be), ConversationID: convID(53), PermissionMode: "generation-53",
	}

	_, err := h.Run(ctx, params)
	require.NoError(t, err)
	ack, err := h.Run(ctx, params)
	require.NoError(t, err)
	params.ProviderSessionID = ack.ProviderSessionID
	_, err = h.Run(ctx, params)
	require.NoError(t, err)

	_, err = h.Abort(ctx, wire.AbortParams{ConversationID: convID(53)})
	require.NoError(t, err)
	frames := notif.waitFrames(t, 1)
	require.Len(t, frames, 1)
	assert.Equal(t, wire.NotifyRunResultDone, frames[0].method)
	done := frames[0].params.(*wire.RunResultDoneFrame)
	assert.Equal(t, "pi-session-accepted", done.ProviderSessionID)
	assert.Equal(t, "pi-user-anchor-after-stop", done.UserAnchor)
	assert.Equal(t, wire.ErrCodeAborted, done.StopErrCode)
	assert.Equal(t, 1, rt.prepared.closes(),
		"exact-owner finalization closes the accepted prepared process after runtime settlement")
}

func TestRuntime_PiAbortBoundsWaitForClaimedTerminalNotification(t *testing.T) {
	rt := newScriptedPreparedPiRT("shared-native-session")
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	notif := &blockingTerminalNotifier{
		recording: newRecordingOutbound(),
		entered:   make(chan struct{}),
		allow:     make(chan struct{}),
	}
	sessions := newRecordingSessions()
	h := handlers.NewRuntimeHandlers(handlers.RuntimeDeps{
		NotifyFor:    func(string) handlers.NotifierPort { return notif },
		Journal:      notif.recording,
		Sessions:     sessions,
		SessionQuery: sessions,
		Gateway:      mock_handlers.NewMockGatewayPort(ctrl),
		Lookup:       mock_handlers.NewMockLLMProviderLookupPort(ctrl),
		RuntimeFor: func(_ agent_backend_entity.BackendType) agentruntime.Runtime {
			return rt
		},
	})
	ctx := context.Background()
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)}
	params := wire.RunParams{
		Backend: backendJSON(t, be), ConversationID: convID(154), PermissionMode: "generation-154",
	}

	_, err := h.Run(ctx, params)
	require.NoError(t, err)
	ack, err := h.Run(ctx, params)
	require.NoError(t, err)
	params.ProviderSessionID = ack.ProviderSessionID
	_, err = h.Run(ctx, params)
	require.NoError(t, err)
	rt.prepared[0].finish()
	<-notif.entered

	abortErrC := make(chan error, 1)
	go func() {
		_, abortErr := h.Abort(context.Background(), wire.AbortParams{ConversationID: params.ConversationID})
		abortErrC <- abortErr
	}()
	select {
	case abortErr := <-abortErrC:
		require.ErrorIs(t, abortErr, context.DeadlineExceeded,
			"terminal delivery already owns finalization, but Abort must still have an internal bound")
	case <-time.After(3 * time.Second):
		close(notif.allow)
		<-abortErrC
		t.Fatal("Abort waited without an internal bound for the claimed terminal notification")
	}
	close(notif.allow)
	_ = notif.recording.waitFrames(t, 1)
	select {
	case <-rt.prepared[0].closed:
	case <-time.After(time.Second):
		t.Fatal("claimed terminal delivery did not complete exact-owner finalization")
	}
}

func TestRuntime_PiAbortWaitsForClaimedTerminalNotification(t *testing.T) {
	rt := newScriptedPreparedPiRT("shared-native-session")
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	notif := &blockingTerminalNotifier{
		recording: newRecordingOutbound(),
		entered:   make(chan struct{}),
		allow:     make(chan struct{}),
	}
	generationRegistry := newTrackingGenerationRegistry()
	sessions := newRecordingSessions()
	h := handlers.NewRuntimeHandlers(handlers.RuntimeDeps{
		NotifyFor:          func(string) handlers.NotifierPort { return notif },
		Journal:            notif.recording,
		Sessions:           sessions,
		SessionQuery:       sessions,
		Gateway:            mock_handlers.NewMockGatewayPort(ctrl),
		Lookup:             mock_handlers.NewMockLLMProviderLookupPort(ctrl),
		GenerationRegistry: generationRegistry,
		RuntimeFor: func(_ agent_backend_entity.BackendType) agentruntime.Runtime {
			return rt
		},
	})
	ctx := context.Background()
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)}
	params := wire.RunParams{
		Backend: backendJSON(t, be), ConversationID: convID(54), PermissionMode: "generation-54",
	}

	_, err := h.Run(ctx, params)
	require.NoError(t, err)
	ack, err := h.Run(ctx, params)
	require.NoError(t, err)
	params.ProviderSessionID = ack.ProviderSessionID
	_, err = h.Run(ctx, params)
	require.NoError(t, err)
	rt.prepared[0].finish()
	<-notif.entered

	abortCtx := newDoneObservingContext()
	abortErrC := make(chan error, 1)
	go func() {
		_, abortErr := h.Abort(abortCtx, wire.AbortParams{ConversationID: convID(54)})
		abortErrC <- abortErr
	}()
	<-abortCtx.observed
	close(notif.allow)
	require.NoError(t, <-abortErrC)
	frames := notif.recording.waitFrames(t, 1)
	require.Len(t, frames, 1)
	assert.Equal(t, wire.NotifyRunResultDone, frames[0].method)
	assert.Equal(t, 1, generationRegistry.releaseCount("generation-54"))

	retry := params
	retry.ProviderSessionID = ""
	retry.PermissionMode = "generation-54-retry"
	_, err = h.Run(ctx, retry)
	require.NoError(t, err, "terminal completion must release registration before Abort returns")
	assert.Equal(t, "generation-54-retry", generationRegistry.owner(handlers.RuntimeSessionKey(retry.ConversationID)))
	_, err = h.Abort(ctx, wire.AbortParams{ConversationID: retry.ConversationID})
	require.NoError(t, err)
}

func TestRuntime_PiAbortSettlementCannotTerminateOrNotifyForNewerGeneration(t *testing.T) {
	rt := newScriptedPreparedPiRT("shared-native-session", "shared-native-session")
	rt.prepared[0].result.Model = "stale-model"
	rt.prepared[1].result.Model = "current-model"
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)}
	params := wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(55), PermissionMode: "generation-55-1"}

	_, err := h.Run(ctx, params)
	require.NoError(t, err)
	firstAck, err := h.Run(ctx, params)
	require.NoError(t, err)
	params.ProviderSessionID = firstAck.ProviderSessionID
	_, err = h.Run(ctx, params)
	require.NoError(t, err)
	_, err = h.Abort(ctx, wire.AbortParams{ConversationID: convID(55)})
	require.NoError(t, err)
	firstFrames := notif.waitFrames(t, 1)
	require.Len(t, firstFrames, 1)
	firstDone := firstFrames[0].params.(*wire.RunResultDoneFrame)
	assert.Equal(t, "stale-model", firstDone.Model)
	assert.Equal(t, wire.ErrCodeAborted, firstDone.StopErrCode)

	params.ProviderSessionID = firstAck.ProviderSessionID
	params.PermissionMode = "generation-55-2"
	_, err = h.Run(ctx, params)
	require.NoError(t, err)
	staleParams := params
	staleParams.PermissionMode = "generation-55-1"
	_, err = h.Run(ctx, staleParams)
	require.ErrorContains(t, err, "stale Pi generation")
	rt.mu.Lock()
	assert.Len(t, rt.requests, 1, "a delayed old preparation request must not prepare the retry")
	rt.mu.Unlock()
	secondAck, err := h.Run(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, "shared-native-session", secondAck.ProviderSessionID)
	params.ProviderSessionID = secondAck.ProviderSessionID
	_, err = h.Run(ctx, params)
	require.NoError(t, err)

	rt.prepared[1].finish()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, steerErr := h.Steer(ctx, wire.SteerParams{ConversationID: convID(55), Text: "after completion"})
		assert.ErrorIs(c, steerErr, agentruntime.ErrNoActiveTurn)
	}, time.Second, 10*time.Millisecond)

	frames := notif.waitFrames(t, 2)
	require.Len(t, frames, 2, "each exact generation may emit one terminal result")
	assert.Equal(t, wire.NotifyRunResultDone, frames[1].method)
	done := frames[1].params.(*wire.RunResultDoneFrame)
	assert.Equal(t, "current-model", done.Model)

	_, firstClosed := rt.prepared[0].counts()
	_, secondClosed := rt.prepared[1].counts()
	assert.Equal(t, 1, firstClosed, "the stale generation must finalize its own resource exactly once")
	assert.Equal(t, 1, secondClosed, "natural completion must finalize the retry resource exactly once")
}

// 断连重连后的 runtime.session.attach(Adopt)会把内存会话表里那一格换成一条新行,
// 而 Daemon 级 generation 属主表里的预约仍然记在**被顶替的** owner 名下。preparePi
// 返回时发现自己已经不是当前行,按 canceled 收尾 —— 这条收尾路径必须照样交还预约,
// 否则这条会话的 generation 永久被占,之后谁都开不出新一轮。
func TestRuntime_ReadoptedPiSessionStillReleasesTheOverwrittenGeneration(t *testing.T) {
	rt := newGatedPreparedPiRT("pi-session-readopted")
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	notif := newRecordingOutbound()
	sessions := newRecordingSessions()
	registry := newTrackingGenerationRegistry()
	h := handlers.NewRuntimeHandlers(handlers.RuntimeDeps{
		NotifyFor:          notif.notifierFor,
		Journal:            notif,
		Sessions:           sessions,
		SessionQuery:       sessions,
		Gateway:            mock_handlers.NewMockGatewayPort(ctrl),
		Lookup:             mock_handlers.NewMockLLMProviderLookupPort(ctrl),
		GenerationRegistry: registry,
		RuntimeFor: func(_ agent_backend_entity.BackendType) agentruntime.Runtime {
			return rt
		},
	})
	ctx := context.Background()
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)}
	params := wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(57), PermissionMode: "generation-57"}

	_, err := h.Run(ctx, params)
	require.NoError(t, err)
	require.Equal(t, "generation-57", registry.owner(handlers.RuntimeSessionKey(convID(57))))

	prepareErrC := make(chan error, 1)
	go func() {
		_, prepareErr := h.Run(ctx, params)
		prepareErrC <- prepareErr
	}()
	<-rt.entered
	h.Adopt(ctx, convID(57), agent_backend_entity.TypePiAgent)
	close(rt.release)

	require.ErrorIs(t, <-prepareErrC, context.Canceled)
	assert.Equal(t, 1, registry.releaseCount("generation-57"))
	assert.Empty(t, registry.owner(handlers.RuntimeSessionKey(convID(57))), "被顶替的 owner 也必须交还 generation 预约")
	_, closed := rt.prepared[0].counts()
	assert.Equal(t, 1, closed, "被顶替的这一轮仍要关掉自己那个 Pi 进程")

	retry := params
	retry.PermissionMode = "generation-57-retry"
	_, err = h.Run(ctx, retry)
	require.NoError(t, err, "预约交还后这条会话必须还能开新一轮")
	assert.Equal(t, "generation-57-retry", registry.owner(handlers.RuntimeSessionKey(convID(57))))
}

func TestRuntime_FanoutLogsEventClassificationWithoutSerializedPayload(t *testing.T) {
	const secret = "secret-tool-input-and-private-prompt"
	logs := captureRuntimeLogs(t)

	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event, 1)
		ch <- agentruntime.ErrorEvent{Err: errors.New(secret)}
		close(ch)
		return ch, &agentruntime.RunResult{}, nil
	}
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}
	_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: convID(66)})
	require.NoError(t, err)
	_ = notif.waitFrames(t, 2)

	assert.Contains(t, logs.String(), "eventKind:ErrorEvent")
	assert.NotContains(t, logs.String(), secret)
	assert.NotContains(t, logs.String(), "payload=")
}

func (*blockingPreparedPiRT) Capabilities() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{
		capability.CapAbort:       true,
		capability.CapForkSession: true,
	}}
}

func (*blockingStartPreparedPiRT) Capabilities() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{
		capability.CapAbort:       true,
		capability.CapForkSession: true,
	}}
}

func (*blockingStartPreparedPiRT) Run(context.Context, agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	return nil, nil, errors.New("blocking-start Pi runtime must use PrepareRun")
}

func (*cancelReturningStartPiRT) Capabilities() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{
		capability.CapAbort:       true,
		capability.CapForkSession: true,
	}}
}

func (*cancelReturningStartPiRT) Run(context.Context, agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	return nil, nil, errors.New("cancel-returning Pi runtime must use PrepareRun")
}

func (*settlingAcceptedPiRT) Capabilities() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{
		capability.CapAbort:       true,
		capability.CapForkSession: true,
	}}
}

func (*settlingAcceptedPiRT) Run(context.Context, agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	return nil, nil, errors.New("settling accepted Pi runtime must use PrepareRun")
}

func (*blockingAbortAcceptedPiRT) Capabilities() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{
		capability.CapAbort:       true,
		capability.CapForkSession: true,
	}}
}

func (*blockingAbortAcceptedPiRT) Run(context.Context, agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	return nil, nil, errors.New("blocking-abort Pi runtime must use PrepareRun")
}

func (*scriptedPreparedPiRT) Capabilities() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{
		capability.CapAbort:       true,
		capability.CapForkSession: true,
	}}
}

func (*scriptedPreparedPiRT) Run(context.Context, agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	return nil, nil, errors.New("scripted Pi runtime must use PrepareRun")
}

func (*blockingTerminalNotifier) Request(context.Context, string, any, any) error { return nil }

// ── fixed-model（决策 11）────────────────────────────────────────────────────

// TestRuntime_Run_FixedModel_ResolvesSpecificModel 钉死决策 11 的 Run 侧 fixed-model：
// wire 同时携带 LLMProviderKey + LLMModelKey，daemon 必须精确解析该模型并装进
// req.Effective（ModelID = 指定模型的 model id），而不是回落 Provider 默认模型。
func TestRuntime_Run_FixedModel_ResolvesSpecificModel(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event)
		close(ch)
		return ch, &agentruntime.RunResult{}, nil
	}
	ctx, _, gw, lookup, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{
		Type:           string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "pk",
	}
	lookup.EXPECT().FindByKey(ctx, "pk").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "pk", Type: string(llm_provider_entity.TypeAnthropic),
		DefaultModelKey: "model-default", Status: consts.ACTIVE,
	}, nil)
	lookup.EXPECT().ResolveModel(ctx, "pk", "model-fixed").Return(
		handlers.EffectiveModel{ModelKey: "model-fixed", ModelID: "claude-opus-4-5"}, nil)
	gw.EXPECT().URL().Return("").AnyTimes()

	_, err := h.Run(ctx, wire.RunParams{
		Backend:        backendJSON(t, be),
		ConversationID: convID(42),
		LLMProviderKey: "pk",
		LLMModelKey:    "model-fixed",
	})
	require.NoError(t, err)
	require.Len(t, rt.runReqs, 1)
	req := rt.runReqs[0].req
	require.NotNil(t, req.Effective, "fixed-model 必须产出执行侧配置")
	assert.Equal(t, agentruntime.EffectiveModeFixedModel, req.Effective.Mode)
	assert.Equal(t, "model-fixed", req.Effective.ModelKey)
	assert.Equal(t, "claude-opus-4-5", req.Effective.ModelID)
	assert.Equal(t, "pk", req.Effective.ProviderKey)
}

// TestRuntime_Run_FixedModel_BackendPinned 钉死 backend 固定模型在远端生效：inherit-agent
// 会话（未钉 provider）时，桌面端把 backend 固定模型作为执行侧 ModelKey 透传过来
// （remoteKeysOnlyEffective → be.LLMModelKey），daemon 按 wire 的 model key 精确解析。
func TestRuntime_Run_FixedModel_BackendPinned(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event)
		close(ch)
		return ch, &agentruntime.RunResult{}, nil
	}
	ctx, _, gw, lookup, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{
		Type:           string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "pk",
		LLMModelKey:    "model-fixed",
	}
	lookup.EXPECT().FindByKey(ctx, "pk").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "pk", Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE,
	}, nil)
	lookup.EXPECT().ResolveModel(ctx, "pk", "model-fixed").Return(
		handlers.EffectiveModel{ModelKey: "model-fixed", ModelID: "claude-opus-4-5"}, nil)
	gw.EXPECT().URL().Return("").AnyTimes()

	// 未钉会话：桌面端透传 backend 固定模型作为 wire model key。
	_, err := h.Run(ctx, wire.RunParams{
		Backend:        backendJSON(t, be),
		ConversationID: convID(42),
		LLMProviderKey: "pk",
		LLMModelKey:    "model-fixed",
	})
	require.NoError(t, err)
	require.Len(t, rt.runReqs, 1)
	req := rt.runReqs[0].req
	require.NotNil(t, req.Effective)
	assert.Equal(t, "model-fixed", req.Effective.ModelKey)
	assert.Equal(t, "claude-opus-4-5", req.Effective.ModelID)
}

// TestRuntime_Run_FixedModel_ModelMissing_Blocks 钉死「fixed-model 缺失/停用 → 下一轮
// 严格阻止，绝不静默降级为 Provider 默认模型」（决策 7/11）。
func TestRuntime_Run_FixedModel_ModelMissing_Blocks(t *testing.T) {
	rt := &fullRT{}
	ctx, _, _, lookup, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{
		Type:           string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "pk",
	}
	lookup.EXPECT().FindByKey(ctx, "pk").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "pk", Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE,
	}, nil)
	lookup.EXPECT().ResolveModel(ctx, "pk", "model-gone").Return(
		handlers.EffectiveModel{}, errors.New("model model-gone not configured on provider pk"))

	_, err := h.Run(ctx, wire.RunParams{
		Backend:        backendJSON(t, be),
		ConversationID: convID(42),
		LLMProviderKey: "pk",
		LLMModelKey:    "model-gone",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model-gone", "必须点出缺失的模型，而不是静默换默认")
	require.Len(t, rt.runReqs, 0, "被阻止的轮次不得触碰 runtime")
}

// TestRuntime_Run_FixedModel_ProviderMissing_Blocks 钉死 fixed-model 的 Provider
// 缺失 → 严格阻止（不回退 agent 绑定、不降级）。与 provider-default 的 #39 回退形成
// 对比：固定目标承载能力/成本/合规预期，静默换 Provider 同样危险。
func TestRuntime_Run_FixedModel_ProviderMissing_Blocks(t *testing.T) {
	rt := &fullRT{}
	ctx, _, _, lookup, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{
		Type:           string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "agent-bound-key",
	}
	// 会话钉的 provider 在 daemon 缺失，且带 fixed model → 阻止，不回退。
	lookup.EXPECT().FindByKey(ctx, "session-key").Return(nil, errors.New("provider session-key not configured"))

	_, err := h.Run(ctx, wire.RunParams{
		Backend:        backendJSON(t, be),
		ConversationID: convID(42),
		LLMProviderKey: "session-key",
		LLMModelKey:    "model-fixed",
	})
	require.Error(t, err)
	var rpcErr *rpcerror.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, rpcerror.ErrProviderMissing.Code, rpcErr.Code)
	require.Len(t, rt.runReqs, 0)
}

// TestRuntime_Goal_FixedModel_Resolves 钉死 Goal 侧 fixed-model：GoalParams 携带
// ProviderKey + ModelKey，daemon 精确解析并装进 req.Effective。
func TestRuntime_Goal_FixedModel_Resolves(t *testing.T) {
	rt := &fullRT{}
	ctx, _, gw, lookup, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{
		ID:             3,
		Type:           string(agent_backend_entity.TypeCodex),
		Name:           "codex",
		LLMProviderKey: "provider-key",
	}
	lookup.EXPECT().FindByKey(ctx, "provider-key").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "provider-key",
		Type:        string(llm_provider_entity.TypeOpenAIResponse),
		Status:      consts.ACTIVE,
	}, nil)
	lookup.EXPECT().ResolveModel(ctx, "provider-key", "model-fixed").Return(
		handlers.EffectiveModel{ModelKey: "model-fixed", ModelID: "gpt-5-codex"}, nil)
	gw.EXPECT().URL().Return("http://127.0.0.1:12345")
	gw.EXPECT().IssueToken(ctx, gomock.Any(), time.Hour).Return("goal-token", nil)
	gw.EXPECT().RevokeToken("goal-token")

	_, err := h.GetGoal(ctx, wire.GoalParams{
		ConversationID:    convID(42),
		AgentID:           7,
		ProviderSessionID: "thread-goal",
		Backend:           backendJSON(t, be),
		LLMProviderKey:    "provider-key",
		LLMModelKey:       "model-fixed",
	})
	require.NoError(t, err)
	require.Len(t, rt.getGoalCalls, 1)
	req := rt.getGoalCalls[0].req
	require.NotNil(t, req.Effective)
	assert.Equal(t, agentruntime.EffectiveModeFixedModel, req.Effective.Mode)
	assert.Equal(t, "model-fixed", req.Effective.ModelKey)
	assert.Equal(t, "gpt-5-codex", req.Effective.ModelID)
}

// TestRuntime_Run_PinnedProviderDefault_NotDraggedByBackendFixedModel 钉死 spec 决策 1
// 的远端半边：会话钉了 Provider 且选 provider-default（wire model key 为空）时，即使
// backend 主绑定同家并固定了模型（be.LLMModelKey 非空），daemon 也必须解析 Provider
// 当前默认模型（provider-default），绝不能被 backend 固定模型带偏成 fixed-model。
// 会话是否钉住只有桌面端知道，wire 的 model key 已是解析结果，daemon 不得再自行派生。
func TestRuntime_Run_PinnedProviderDefault_NotDraggedByBackendFixedModel(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event)
		close(ch)
		return ch, &agentruntime.RunResult{}, nil
	}
	ctx, _, gw, lookup, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{
		Type:           string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "pk",
		LLMModelKey:    "model-fixed",
	}
	lookup.EXPECT().FindByKey(ctx, "pk").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "pk", Type: string(llm_provider_entity.TypeAnthropic),
		DefaultModelKey: "model-default", Status: consts.ACTIVE,
	}, nil)
	// 必须按 provider-default（空 model key）解析，而不是 be.LLMModelKey="model-fixed"。
	lookup.EXPECT().ResolveModel(ctx, "pk", "").Return(
		handlers.EffectiveModel{ModelKey: "model-default", ModelID: "claude-sonnet-4-6"}, nil)
	gw.EXPECT().URL().Return("").AnyTimes()

	_, err := h.Run(ctx, wire.RunParams{
		Backend:        backendJSON(t, be),
		ConversationID: convID(42),
		LLMProviderKey: "pk",
	})
	require.NoError(t, err)
	require.Len(t, rt.runReqs, 1)
	req := rt.runReqs[0].req
	require.NotNil(t, req.Effective)
	assert.Equal(t, agentruntime.EffectiveModeProviderDefault, req.Effective.Mode)
	assert.Equal(t, "model-default", req.Effective.ModelKey)
	assert.Equal(t, "claude-sonnet-4-6", req.Effective.ModelID)
}

// TestRuntime_Run_ProviderDefault_ResolvesDefaultModel 钉死 provider-default 在 daemon
// 侧解析当前默认模型并装进 req.Effective（决策 8/11）：wire 不带 model key → 用
// Provider 的 DefaultModelKey 解析出默认模型 id。
func TestRuntime_Run_ProviderDefault_ResolvesDefaultModel(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event)
		close(ch)
		return ch, &agentruntime.RunResult{}, nil
	}
	ctx, _, gw, lookup, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{
		Type:           string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "pk",
	}
	lookup.EXPECT().FindByKey(ctx, "pk").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "pk", Type: string(llm_provider_entity.TypeAnthropic),
		DefaultModelKey: "model-default", Status: consts.ACTIVE,
	}, nil)
	lookup.EXPECT().ResolveModel(ctx, "pk", "").Return(
		handlers.EffectiveModel{ModelKey: "model-default", ModelID: "claude-sonnet-4-6"}, nil)
	gw.EXPECT().URL().Return("").AnyTimes()

	_, err := h.Run(ctx, wire.RunParams{
		Backend:        backendJSON(t, be),
		ConversationID: convID(42),
		LLMProviderKey: "pk",
	})
	require.NoError(t, err)
	require.Len(t, rt.runReqs, 1)
	req := rt.runReqs[0].req
	require.NotNil(t, req.Effective)
	assert.Equal(t, agentruntime.EffectiveModeProviderDefault, req.Effective.Mode)
	assert.Equal(t, "model-default", req.Effective.ModelKey)
	assert.Equal(t, "claude-sonnet-4-6", req.Effective.ModelID)
}

// TestRuntime_Run_PersistsProjectSyncID 覆盖「项目在会话发起那一刻就落库」。
//
// 此前 agentred 只存 cwd,服务端按 (指纹, cwd) 反推项目。日活跃统计走的是一条不上行
// 任何路径的纯计数通道,反推那条路在那里用不了 —— 项目必须随起手的这一轮记下来,而
// 且和 Title / AgentSyncID 同批幂等覆盖(会话可以换项目)。
func TestRuntime_Run_PersistsProjectSyncID(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event, 1)
		ch <- agentruntime.Done{}
		close(ch)
		return ch, &agentruntime.RunResult{}, nil
	}
	ctx, notif, sess, h := setupRuntimeTestWithSessions(t, rt)
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}
	_, err := h.Run(ctx, wire.RunParams{
		Backend: backendJSON(t, be), ConversationID: convID(5), AgentID: 7, Cwd: "/work",
		ProjectSyncID: "01HXproj00000000000000000",
	})
	require.NoError(t, err)

	notif.waitFrames(t, 2)
	started := sess.started()
	require.Len(t, started, 1)
	assert.Equal(t, "01HXproj00000000000000000", started[0].ProjectSyncID,
		"起手建行必须带上发起方报的项目同步标识")
}

// 终态帧带上本轮的计时(耗时 / 首 token / tok/s)。
//
// 为什么这三个数必须由 daemon 量:按帧重建转录的消费方 —— 浏览器控制台、peer
// 视图 —— 手里只有事件流。桌面端本机会话上那三个数是 chat_svc 在 runtime 之上
// 算完落进自己库的,一格都过不了 wire,于是那边的 meta 只剩「模型 —、耗时 0.0s」。
// 口径与「哪条事件动哪一下表」两边共用 internal/pkg/turnstats。
func TestRuntime_Run_DoneFrameCarriesTurnStats(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event)
		go func() {
			defer close(ch)
			ch <- agentruntime.TextDelta{Text: "hi"}
			// 让墙上时间真的走过几毫秒:三个数都以 ms 为单位,同一纳秒内跑完的
			// 一轮量出 0 是对的,但那样这条用例就证不出接线。
			time.Sleep(8 * time.Millisecond)
			ch <- agentruntime.UsageUpdate{Usage: &provider.Usage{CompletionTokens: 60}}
			ch <- agentruntime.Done{}
		}()
		return ch, &agentruntime.RunResult{Model: "claude-sonnet-4-6"}, nil
	}
	ctx, notif, _, _, h := setupRuntimeTest(t, rt)

	be := agent_backend_entity.AgentBackend{ID: 1, Type: string(agent_backend_entity.TypePiAgent), Name: "x"}
	_, err := h.Run(ctx, wire.RunParams{
		Backend: backendJSON(t, be), ConversationID: convID(42), AgentID: 7, Cwd: "/tmp", UserText: "hello",
	})
	require.NoError(t, err)

	frames := notif.waitFrames(t, 4)
	done, ok := frames[3].params.(*wire.RunResultDoneFrame)
	require.True(t, ok, "expected wire.RunResultDoneFrame, got %T", frames[3].params)
	assert.GreaterOrEqual(t, done.DurationMs, 8, "耗时是墙上时间")
	assert.Greater(t, done.TokensPerSec, 0.0, "分子是本轮累加的 completion token")
	// 首 token 由第一条 TextDelta 记下,必然早于收口。
	assert.LessOrEqual(t, done.FirstTokenMs, done.DurationMs)
}

// convID 把一个短会话号折成一条**格式合法**的 conversation_id,只在测试里用:
// 线上身份是 uuid,而这些用例真正要断言的是"同一个值原样往返"与"两条不同的对话
// 互不并轨",一个可读、可复现的映射比随机 uuid 更好读。
func convID(n int64) string {
	return fmt.Sprintf("00000000-0000-7000-8000-%012d", n)
}

// Given 线上给来的 conversation_id 不是一条对话身份（空串 / 旧的整数会话号），
// When 对端起一轮，Then runtime.run 与它的八个兄弟处理器一样在边界上拒掉。
//
// 只有 Run 漏了这道校验。放行的后果不是「这一轮失败」而是**串账**：身份键收缩到
// conversation_id 之后，daemon_sessions 的主键就是它，空串于是成了一个人人都能写的
// 合法主键——每个这么发的对端都落在同一行上，通知日志也共用 (” , seq) 那一串序号，
// 谁也读不回自己的转录。
func TestRuntime_Run_GivenAConversationIDThatIsNotOne_ThenItIsRejectedAtTheBoundary(t *testing.T) {
	for _, conversationID := range []string{"", "42", "not-a-uuid"} {
		t.Run(conversationID, func(t *testing.T) {
			rt := &fullRT{}
			ctx, _, _, _, h := setupRuntimeTest(t, rt)
			be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}

			_, err := h.Run(ctx, wire.RunParams{Backend: backendJSON(t, be), ConversationID: conversationID})
			require.Error(t, err)
			var rpcErr *rpcerror.Error
			require.ErrorAs(t, err, &rpcErr)
			assert.Equal(t, rpcerror.CodeInvalidParams, rpcErr.Code)
		})
	}
}

// TestRuntime_Run_ReasoningEffortRunParamWinsOverBackendPayload 钉死规格决策 5 的
// 取值优先级:本轮有效思考力度作为**独立 run 参数**过线(浏览器发的是空壳 backend,
// 塞进负载里那条路上恒为空),非空即胜过 backend 负载上那一格。
//
// 落点是本轮 backend 副本:decision 3 把「有效力度」合成在唯一那个边界上,下游
// launchIdentity / 各 runtime 的 session 构造一字不改就同时拿到它。
func TestRuntime_Run_ReasoningEffortRunParamWinsOverBackendPayload(t *testing.T) {
	rt := &fullRT{}
	rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		ch := make(chan agentruntime.Event)
		close(ch)
		return ch, &agentruntime.RunResult{}, nil
	}
	ctx, _, _, _, h := setupRuntimeTest(t, rt)
	be := agent_backend_entity.AgentBackend{
		Type: string(agent_backend_entity.TypeClaudeCode), ReasoningEffort: "low",
	}

	_, err := h.Run(ctx, wire.RunParams{
		Backend:         backendJSON(t, be),
		ConversationID:  convID(42),
		ReasoningEffort: "max",
	})
	require.NoError(t, err)
	require.Len(t, rt.runReqs, 1)
	require.NotNil(t, rt.runReqs[0].req.Backend)
	assert.Equal(t, "max", rt.runReqs[0].req.Backend.ReasoningEffort,
		"run 参数非空必须胜过 backend 负载上那一格")
}

// TestRuntime_Run_ReasoningEffortFallsBackToBackendPayload 钉死同一条决策的另一半
// (硬不变量 6):run 参数**缺省不等于「用户选了默认」**。老桌面端根本不带这个字段,
// 把缺省读成空档会让它们的后端配置在升级 agentred 之后集体失效。
func TestRuntime_Run_ReasoningEffortFallsBackToBackendPayload(t *testing.T) {
	for _, runParam := range []string{"", "   "} {
		t.Run("runParam="+runParam, func(t *testing.T) {
			rt := &fullRT{}
			rt.runFn = func(_ context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
				ch := make(chan agentruntime.Event)
				close(ch)
				return ch, &agentruntime.RunResult{}, nil
			}
			ctx, _, _, _, h := setupRuntimeTest(t, rt)
			be := agent_backend_entity.AgentBackend{
				Type: string(agent_backend_entity.TypeClaudeCode), ReasoningEffort: "medium",
			}

			_, err := h.Run(ctx, wire.RunParams{
				Backend:         backendJSON(t, be),
				ConversationID:  convID(42),
				ReasoningEffort: runParam,
			})
			require.NoError(t, err)
			require.Len(t, rt.runReqs, 1)
			require.NotNil(t, rt.runReqs[0].req.Backend)
			assert.Equal(t, "medium", rt.runReqs[0].req.Backend.ReasoningEffort,
				"run 参数缺省时回落后端配置,不能把这一轮拍成空档")
		})
	}
}

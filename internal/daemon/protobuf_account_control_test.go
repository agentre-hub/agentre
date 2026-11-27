package daemon

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	dbpkg "github.com/cago-frame/cago/database/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/auth"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/pkg/ctlaccount"
	"github.com/agentre-hub/agentre/internal/pkg/relaypeer"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

// protobuf_account_control_test.go 钉住 V27:agrctl 每条命令都是一条**新的**账号连接,
// 而 agentred 的 runtime 控制面(提交作答 / 中止 / 插话)按连接持有会话表。发起这一轮的
// 那条连接关掉之后,answer / stop / send --steer 必须照样落到那条还在跑的一轮上 ——
// 这里驱动的是真实的 agentred(真实 handler 栈、真实 auth.account 与自省),只把 backend
// 换成一个停在待作答上的假 runtime;账号对端就是 agrctl 用的那个 ctlaccount.Client。

const (
	cliInitiator = "sha256:agrctl-cli-device"
	askRequestID = "ask-flavor"
	toolRequest  = "tool-bash"
)

// waitingRT 是一个停在 AskUserQuestion 与工具审批上的 claudecode backend:Run 开出的
// 那一轮一直不结束,直到被中止;到达它的每一次作答 / 审批 / 插话都记下来 —— 到达这里
// 就是到达了那一轮。
type waitingRT struct {
	mu       sync.Mutex
	stops    map[int64]chan struct{}
	answers  []string
	allowed  []string
	steers   []string
	aborted  []int64
	started  chan int64
	finished chan int64
}

func newWaitingRT() *waitingRT {
	return &waitingRT{stops: map[int64]chan struct{}{}, started: make(chan int64, 4), finished: make(chan int64, 4)}
}

func (*waitingRT) Capabilities() capability.Capabilities { return capability.Capabilities{} }

func (w *waitingRT) Run(_ context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	stop := make(chan struct{})
	w.mu.Lock()
	w.stops[req.SessionID] = stop
	w.mu.Unlock()
	ch := make(chan agentruntime.Event)
	go func() {
		defer close(ch)
		<-stop
		w.finished <- req.SessionID
	}()
	w.started <- req.SessionID
	return ch, &agentruntime.RunResult{}, nil
}

func (w *waitingRT) PendingWaiters(_ context.Context, sid int64) agentruntime.WaiterSnapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, live := w.stops[sid]; !live {
		return agentruntime.WaiterSnapshot{}
	}
	return agentruntime.WaiterSnapshot{
		AskUserQuestions: []agentruntime.PendingAskUserQuestion{{RequestID: askRequestID, Questions: []agentruntime.AskQuestion{{
			Question: "Which flavor?", Header: "Flavor", Options: []agentruntime.AskOption{{Label: "Vanilla"}, {Label: "Chocolate"}},
		}}}},
		ToolPermissions: []agentruntime.PendingToolPermission{{RequestID: toolRequest, ToolName: "Bash", Input: json.RawMessage(`{"command":"ls"}`)}},
	}
}

func (w *waitingRT) SubmitAnswer(_ context.Context, _ int64, requestID string, _ []agentruntime.AskQuestion, answers []agentruntime.AskAnswer, _ bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, a := range answers {
		w.answers = append(w.answers, requestID+"="+a.Labels[0])
	}
	return nil
}

func (w *waitingRT) SubmitToolPermission(_ context.Context, _ int64, requestID string, allow, _ bool, _ string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if allow {
		w.allowed = append(w.allowed, requestID)
	}
	return nil
}

func (w *waitingRT) Steer(_ context.Context, _ int64, _, text string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.steers = append(w.steers, text)
	return nil
}

func (*waitingRT) DrainPending(context.Context, int64) []agentruntime.ConsumedSteer { return nil }

func (w *waitingRT) Abort(_ context.Context, sid int64, _ uint64) (agentruntime.AbortOutcome, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	stop, live := w.stops[sid]
	if !live {
		return agentruntime.AbortOutcome{}, agentruntime.ErrNoActiveTurn
	}
	delete(w.stops, sid)
	close(stop)
	w.aborted = append(w.aborted, sid)
	return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindUser}, nil
}

type waitingRecord struct{ answers, allowed, steers []string }

func (w *waitingRT) record() waitingRecord {
	w.mu.Lock()
	defer w.mu.Unlock()
	return waitingRecord{
		answers: append([]string(nil), w.answers...),
		allowed: append([]string(nil), w.allowed...),
		steers:  append([]string(nil), w.steers...),
	}
}

// selfPeer is ctlaccount's Peer onto the real agentred: every Open is a new
// in-process channel served exactly like a relay channel (the daemon's
// registry plus its per-connection handlers), authenticated with a real
// auth.account — what each agrctl invocation gets through the account relay.
type selfPeer struct {
	d          *Daemon
	credential string
}

func (p selfPeer) Open(ctx context.Context, _ string) (*relaypeer.Conn, error) {
	client, server := relaypeer.Pipe()
	conn := protorpc.NewConn(protorpc.NewPayloadFrameConn(server), p.d.protobufRegistry.Clone())
	p.d.bindProtobufConn(conn)
	go conn.Serve(dbpkg.WithContextDB(context.Background(), p.d.db))
	return relaypeer.OpenChannel(ctx, client, p.credential)
}

func (selfPeer) Close() {}

type emptyCatalog struct{}

func (emptyCatalog) List(agentrewire.CtlKind) ([]*agentrewire.CtlResource, error) {
	return nil, nil
}

func (emptyCatalog) ByID(agentrewire.CtlKind, int64) *agentrewire.CtlResource {
	return nil
}

func (emptyCatalog) Path(agentrewire.CtlKind, int64) string {
	return ""
}

func (emptyCatalog) Locate(agentrewire.CtlKind, string) (*agentrewire.CtlResource, error) {
	return nil, ctlaccount.ErrSessionNotVisible
}

func cliIntrospection() map[string]any {
	return map[string]any{"account_id": scopeAccount, "device_id": 9, "kind": auth.KindCLI,
		"peer_fingerprint": cliInitiator, "expires_in": 0}
}

func accountClient(d *Daemon, credential string) *ctlaccount.Client {
	return accountClientOn(d, credential, nil, emptyCatalog{})
}

// accountClientOn is agrctl's account client onto the real agentred under
// credential, with the account server srv and the account's resources cat.
func accountClientOn(d *Daemon, credential string, srv ctlaccount.Server, cat ctlaccount.Catalog) *ctlaccount.Client {
	return ctlaccount.New(ctlaccount.Options{Server: srv, Peer: selfPeer{d: d, credential: credential}, Catalog: cat})
}

// openAs opens one account connection to the real agentred under credential.
func openAs(t *testing.T, rig *scopeRig, credential string) *relaypeer.Conn {
	t.Helper()
	conn, err := selfPeer{d: rig.daemon, credential: credential}.Open(rig.ctx, "")
	require.NoError(t, err)
	return conn
}

// sendAndHangUp is `agrctl send` without --wait: one account connection starts
// the turn on the real agentred and closes; the turn keeps running there.
func sendAndHangUp(t *testing.T, rig *scopeRig, rt *waitingRT, credential string, n int64) string {
	t.Helper()
	conv := convID(n)
	conn := openAs(t, rig, credential)
	_, err := wirecall.RuntimeRun(rig.ctx, conn, &agentrewire.RuntimeRunRequest{
		ConversationId: conv, Cwd: t.TempDir(), UserText: "ask me a flavor", Backend: &agentrewire.AgentBackend{Type: "claudecode"},
	})
	require.NoError(t, err)
	select {
	case <-rt.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the turn never reached the backend")
	}
	require.NoError(t, conn.Close())
	<-conn.Done()
	require.Eventually(t, func() bool {
		row, err := rig.daemon.sessionStore.Find(rig.ctx, cliInitiator, conv)
		return err == nil && row != nil && row.LifecycleState == "running"
	}, 5*time.Second, 10*time.Millisecond, "the turn must still be running after the sender hung up")
	return conv
}

func newAccountControlRig(t *testing.T) (*scopeRig, *waitingRT) {
	t.Helper()
	rt := newWaitingRT()
	t.Cleanup(agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, rt))
	return newScopeRig(t, cliIntrospection()), rt
}

// Given a turn started by an account connection that has since closed, When a
// new account connection answers its question (agrctl answer --option), Then
// the answer reaches the live turn.
func TestDaemonAccountControl_GivenTheSenderHungUp_WhenANewConnectionAnswersTheQuestion_ThenTheTurnGetsIt(t *testing.T) {
	rig, rt := newAccountControlRig(t)
	conv := sendAndHangUp(t, rig, rt, "cli-credential", 1)

	kind, err := accountClient(rig.daemon, "cli-credential").Answer(rig.ctx, ctlaccount.AnswerInput{Session: conv, Item: askRequestID, Options: []string{"Vanilla"}})

	require.NoError(t, err)
	assert.Equal(t, "question", kind)
	assert.Equal(t, []string{askRequestID + "=Vanilla"}, rt.record().answers)
}

// … When it allows the pending tool permission (agrctl answer --allow), Then
// the decision reaches the live turn.
func TestDaemonAccountControl_GivenTheSenderHungUp_WhenANewConnectionAllowsTheToolPermission_ThenTheTurnGetsIt(t *testing.T) {
	rig, rt := newAccountControlRig(t)
	conv := sendAndHangUp(t, rig, rt, "cli-credential", 2)

	kind, err := accountClient(rig.daemon, "cli-credential").Answer(rig.ctx, ctlaccount.AnswerInput{Session: conv, Item: toolRequest, Allow: true})

	require.NoError(t, err)
	assert.Equal(t, "tool_permission", kind)
	assert.Equal(t, []string{toolRequest}, rt.record().allowed)
}

// … When it stops the session (agrctl stop), Then the live turn is aborted.
func TestDaemonAccountControl_GivenTheSenderHungUp_WhenANewConnectionStops_ThenTheTurnIsAborted(t *testing.T) {
	rig, rt := newAccountControlRig(t)
	conv := sendAndHangUp(t, rig, rt, "cli-credential", 3)

	reply, err := accountClient(rig.daemon, "cli-credential").Stop(rig.ctx, conv)

	require.NoError(t, err)
	assert.Equal(t, ctlaccount.StopReply{Stopped: true, Running: true}, reply)
	select {
	case <-rt.finished:
	case <-time.After(5 * time.Second):
		t.Fatal("stop never ended the live turn")
	}
}

// … When it interjects (agrctl send --session --steer), Then the steer reaches
// the live turn.
func TestDaemonAccountControl_GivenTheSenderHungUp_WhenANewConnectionSteers_ThenTheTurnGetsIt(t *testing.T) {
	rig, rt := newAccountControlRig(t)
	conv := sendAndHangUp(t, rig, rt, "cli-credential", 4)

	reply, err := accountClient(rig.daemon, "cli-credential").Send(rig.ctx, ctlaccount.SendInput{Session: conv, Steer: true, Text: "also sprinkles"})

	require.NoError(t, err)
	assert.True(t, reply.Steered)
	assert.Equal(t, []string{"also sprinkles"}, rt.record().steers)
}

// Given a turn another initiator started, When a personal token holding chat
// and approve answers, stops or steers it, Then every one is refused and nothing
// reaches the turn: taking part in a session from a new connection never widens
// what a token may act on.
func TestDaemonAccountControl_GivenAnotherInitiatorsTurn_WhenAPersonalTokenActsOnIt_ThenRefusedAndTheTurnSeesNothing(t *testing.T) {
	rig, rt := newAccountControlRig(t)
	conv := sendAndHangUp(t, rig, rt, "cli-credential", 5)
	rig.verdict.Store(patIntrospection("read", "chat", "approve"))
	token := accountClient(rig.daemon, "pat-credential")

	_, err := token.Answer(rig.ctx, ctlaccount.AnswerInput{Session: conv, Item: askRequestID, Options: []string{"Vanilla"}})
	assert.EqualError(t, err, "token may only act on sessions it started")
	_, err = token.Answer(rig.ctx, ctlaccount.AnswerInput{Session: conv, Item: toolRequest, Allow: true})
	assert.EqualError(t, err, "token may only act on sessions it started")
	_, err = token.Stop(rig.ctx, conv)
	assert.EqualError(t, err, "token may only act on sessions it started")
	_, err = token.Send(rig.ctx, ctlaccount.SendInput{Session: conv, Steer: true, Text: "hijack"})
	assert.EqualError(t, err, "token may only act on sessions it started")

	assert.Equal(t, waitingRecord{}, rt.record())
	select {
	case <-rt.finished:
		t.Fatal("a refused stop ended the turn")
	default:
	}
}

// Given a turn a personal token started itself on a connection that has since
// closed, When the same token steers, answers and stops it from new
// connections, Then each reaches the live turn.
func TestDaemonAccountControl_GivenAPersonalTokensOwnTurn_WhenItActsFromNewConnections_ThenTheTurnGetsEach(t *testing.T) {
	rt := newWaitingRT()
	t.Cleanup(agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, rt))
	rig := newScopeRig(t, patIntrospection("read", "chat", "approve"))
	conv := convID(6)
	conn := openAs(t, rig, "pat-credential")
	_, err := wirecall.RuntimeRun(rig.ctx, conn, &agentrewire.RuntimeRunRequest{
		ConversationId: conv, Cwd: t.TempDir(), UserText: "ask me", Backend: &agentrewire.AgentBackend{Type: "claudecode"},
	})
	require.NoError(t, err)
	<-rt.started
	require.NoError(t, conn.Close())
	<-conn.Done()

	token := accountClient(rig.daemon, "pat-credential")

	_, err = token.Send(rig.ctx, ctlaccount.SendInput{Session: conv, Steer: true, Text: "no nuts"})
	require.NoError(t, err)
	_, err = token.Answer(rig.ctx, ctlaccount.AnswerInput{Session: conv, Item: askRequestID, Options: []string{"Chocolate"}})
	require.NoError(t, err)
	reply, err := token.Stop(rig.ctx, conv)
	require.NoError(t, err)

	assert.Equal(t, []string{"no nuts"}, rt.record().steers)
	assert.Equal(t, []string{askRequestID + "=Chocolate"}, rt.record().answers)
	assert.True(t, reply.Stopped)
	select {
	case <-rt.finished:
	case <-time.After(5 * time.Second):
		t.Fatal("stop never ended the token's own turn")
	}
}

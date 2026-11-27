package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/pkg/ctlaccount"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

// protobuf_account_stop_test.go 钉住 F4:账号模式下 `agrctl send`(异步,发起的那条连接随即关掉)
// 之后再 `agrctl stop`(一条新连接:先 attach,再 DrainPending + Abort),这一轮按用户停止落 idle,
// 而不是 failed —— agrctl get session / list sessions 与 agentred 的会话生命周期都是。
// backend 被中断时像 Claude CLI 那样收场:一条不带停止 sentinel 的诊断(码 0)。没有 stop 的同样
// 收场仍是故障。整条链是真的 agentred handler 栈 + 真的 ctlaccount.Client,只有 backend 是假的。

const stopBoxFP = "sha256:stop-box"

// interruptRT 是一个一直跑到被中断(或被判故障)才收场的 claudecode backend。
type interruptRT struct {
	mu      sync.Mutex
	turns   map[int64]*interruptTurn
	started chan int64
}

type interruptTurn struct {
	ch     chan agentruntime.Event
	result *agentruntime.RunResult
}

func newInterruptRT() *interruptRT {
	return &interruptRT{turns: map[int64]*interruptTurn{}, started: make(chan int64, 4)}
}

func (*interruptRT) Capabilities() capability.Capabilities { return capability.Capabilities{} }

func (r *interruptRT) Run(_ context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	turn := &interruptTurn{ch: make(chan agentruntime.Event), result: &agentruntime.RunResult{}}
	r.mu.Lock()
	r.turns[req.SessionID] = turn
	r.mu.Unlock()
	r.started <- req.SessionID
	return turn.ch, turn.result, nil
}

// end 让 sid 上那一轮以 stopErr 收场;没有在跑的一轮交回 false。
func (r *interruptRT) end(sid int64, stopErr error) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	turn, live := r.turns[sid]
	if !live {
		return false
	}
	delete(r.turns, sid)
	turn.result.StopErr = stopErr
	close(turn.ch)
	return true
}

// Abort 是 Claude CLI 的软中断:这一轮收场时带的是一条码 0、不带停止 sentinel 的诊断。
func (r *interruptRT) Abort(_ context.Context, sid int64, _ uint64) (agentruntime.AbortOutcome, error) {
	if !r.end(sid, errors.New("[ede_diagnostic] result_type=user last_content_type=n/a stop_reason=null")) {
		return agentruntime.AbortOutcome{}, agentruntime.ErrNoActiveTurn
	}
	return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindUser}, nil
}

func (r *interruptRT) DrainPending(context.Context, int64) []agentruntime.ConsumedSteer { return nil }

// stopServer is the account server for these runs: the agentred stop-box is
// online and nothing is saved in the account index.
type stopServer struct{}

func (stopServer) GetRaw(_ context.Context, pathAndQuery string) ([]byte, error) {
	if pathAndQuery == "/v1/devices" {
		return envelope(`{"devices":[{"id":7,"name":"stop-box","kind":"agentred","fingerprint":"` + stopBoxFP + `","online":true}]}`), nil
	}
	return envelope(`{"items":[]}`), nil
}

func (stopServer) PostRaw(context.Context, string, []byte) ([]byte, error) {
	return envelope(`{}`), nil
}

type stopRig struct {
	*scopeRig
	rt     *interruptRT
	client *ctlaccount.Client
}

func newStopRig(t *testing.T) *stopRig {
	t.Helper()
	rt := newInterruptRT()
	t.Cleanup(agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, rt))
	rig := newScopeRig(t, cliIntrospection())
	return &stopRig{scopeRig: rig, rt: rt, client: stopClient(rig.daemon, "cli-credential")}
}

// stopClient is agrctl's account client onto the real agentred, under credential.
func stopClient(d *Daemon, credential string) *ctlaccount.Client {
	return ctlaccount.New(ctlaccount.Options{Server: stopServer{}, Peer: selfPeer{d: d, credential: credential}, Catalog: emptyCatalog{}})
}

// sendAndHangUp is `agrctl send` without --wait onto the interrupting backend.
func (r *stopRig) sendAndHangUp(t *testing.T, n int64) (string, int64) {
	t.Helper()
	conv := convID(n)
	conn := openAs(t, r.scopeRig, "cli-credential")
	_, err := wirecall.RuntimeRun(r.ctx, conn, &agentrewire.RuntimeRunRequest{
		ConversationId: conv, Cwd: t.TempDir(), UserText: "write me a long essay", Backend: &agentrewire.AgentBackend{Type: "claudecode"},
	})
	require.NoError(t, err)
	var sid int64
	select {
	case sid = <-r.rt.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the turn never reached the backend")
	}
	require.NoError(t, conn.Close())
	<-conn.Done()
	require.Equal(t, "running", r.lifecycle(t, conv), "the turn must still be running after the sender hung up")
	return conv, sid
}

// lifecycle is agentred's own lifecycle for conv, read from its session store.
func (r *stopRig) lifecycle(t *testing.T, conv string) string {
	t.Helper()
	row, err := r.daemon.sessionStore.Find(r.ctx, cliInitiator, conv)
	require.NoError(t, err)
	require.NotNil(t, row)
	return row.LifecycleState
}

// settled waits until agentred's lifecycle for conv leaves running.
func (r *stopRig) settled(t *testing.T, conv string) string {
	t.Helper()
	require.Eventually(t, func() bool { return r.lifecycle(t, conv) != "running" }, 5*time.Second, 10*time.Millisecond,
		"the turn never settled")
	return r.lifecycle(t, conv)
}

// reported is the status agrctl shows for conv in get session and in list sessions.
func (r *stopRig) reported(t *testing.T, conv string) (get, list string) {
	t.Helper()
	detail, err := r.client.GetSession(r.ctx, conv)
	require.NoError(t, err)
	rows, err := r.client.ListSessions(r.ctx, ctlaccount.SessionFilter{})
	require.NoError(t, err)
	for _, row := range rows {
		if row.ID == conv {
			list = row.Status
		}
	}
	return detail.Status, list
}

// Given a turn started by an account connection that has since closed, When agrctl
// stops it from a new connection and the backend ends with Claude's soft-interrupt
// diagnostic, Then agentred settles the session idle and agrctl get session / list
// sessions both show idle.
func TestDaemonAccountStop_GivenTheSenderHungUp_WhenANewConnectionStops_ThenTheSessionSettlesIdle(t *testing.T) {
	rig := newStopRig(t)
	conv, _ := rig.sendAndHangUp(t, 11)

	reply, err := rig.client.Stop(rig.ctx, conv)
	require.NoError(t, err)
	require.True(t, reply.Stopped)

	assert.Equal(t, "idle", rig.settled(t, conv), "agentred's lifecycle")
	get, list := rig.reported(t, conv)
	assert.Equal(t, ctlaccount.StatusIdle, get, "agrctl get session")
	assert.Equal(t, ctlaccount.StatusIdle, list, "agrctl list sessions")
}

// Given the same hung-up turn, When nobody stops it and it ends with the same
// unclassified error, Then it is a genuine failure: failed everywhere.
func TestDaemonAccountStop_GivenTheSenderHungUp_WhenTheTurnFailsWithoutAStop_ThenTheSessionSettlesFailed(t *testing.T) {
	rig := newStopRig(t)
	conv, sid := rig.sendAndHangUp(t, 12)

	require.True(t, rig.rt.end(sid, errors.New("[ede_diagnostic] result_type=user last_content_type=n/a stop_reason=null")))

	assert.Equal(t, "failed", rig.settled(t, conv), "agentred's lifecycle")
	get, list := rig.reported(t, conv)
	assert.Equal(t, ctlaccount.StatusFailed, get, "agrctl get session")
	assert.Equal(t, ctlaccount.StatusFailed, list, "agrctl list sessions")
}

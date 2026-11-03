package remote

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// session_process_test.go 钉住桌面端 → agentred 的会话进程查询/结束：请求带着会话的线上身份与发起端，
// 响应的 uint32 pid/端口转成 int，agentred 答「不属于本会话」时落成 agentruntime.ErrSessionProcessNotOwned。

var _ agentruntime.SessionProcessInspector = (*Runtime)(nil)

type processDaemon struct {
	mu       sync.Mutex
	listReqs []*agentrewire.RuntimeListSessionProcessesRequest
	killReqs []*agentrewire.RuntimeKillSessionProcessRequest
	procs    []*agentrewire.SessionProcess
	failList error
	failKill error
}

func newProcessRig(t *testing.T) (*Runtime, *processDaemon) {
	t.Helper()
	clientPipe, serverPipe := newFramePipePair()
	d := &processDaemon{}
	reg := protorpc.NewRegistry()
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_LIST_SESSION_PROCESSES),
		func() *agentrewire.RuntimeListSessionProcessesRequest {
			return &agentrewire.RuntimeListSessionProcessesRequest{}
		},
		func(_ context.Context, req *agentrewire.RuntimeListSessionProcessesRequest) (*agentrewire.RuntimeListSessionProcessesResponse, error) {
			d.mu.Lock()
			defer d.mu.Unlock()
			d.listReqs = append(d.listReqs, req)
			if d.failList != nil {
				return nil, d.failList
			}
			return &agentrewire.RuntimeListSessionProcessesResponse{Processes: d.procs}, nil
		})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_KILL_SESSION_PROCESS),
		func() *agentrewire.RuntimeKillSessionProcessRequest {
			return &agentrewire.RuntimeKillSessionProcessRequest{}
		},
		func(_ context.Context, req *agentrewire.RuntimeKillSessionProcessRequest) (*agentrewire.Empty, error) {
			d.mu.Lock()
			defer d.mu.Unlock()
			d.killReqs = append(d.killReqs, req)
			if d.failKill != nil {
				return nil, d.failKill
			}
			return &agentrewire.Empty{}, nil
		})
	ctx, cancel := context.WithCancel(context.Background())
	server := protorpc.NewConn(serverPipe, reg)
	client := protorpc.NewConn(clientPipe, protorpc.NewRegistry())
	go server.Serve(ctx)
	go client.Serve(ctx)
	t.Cleanup(func() { cancel(); _ = clientPipe.Close() })
	return New(&injectConn{conn: client, pipe: clientPipe}, WithConversationIDResolver(convOf)), d
}

func TestListSessionProcesses_GivenDaemonProcesses_ThenRequestCarriesIdentityAndWireIntsBecomeInts(t *testing.T) {
	rt, d := newProcessRig(t)
	rt.rememberOrigin(convOf(4), "peer-A")
	d.procs = []*agentrewire.SessionProcess{
		{Pid: 321, Command: "node server.js", Ports: []uint32{3000, 3001}, StartedAtMs: 1700, Cwd: "/w"},
		{Pid: 322, Command: "sleep 9"},
	}

	got, err := rt.ListSessionProcesses(context.Background(), convOf(4))

	require.NoError(t, err)
	require.Len(t, d.listReqs, 1)
	assert.Equal(t, convOf(4), d.listReqs[0].GetConversationId())
	assert.Equal(t, "peer-A", d.listReqs[0].GetPeerFingerprint())
	assert.Equal(t, []agentruntime.SessionProcess{
		{PID: 321, Command: "node server.js", Ports: []int{3000, 3001}, StartedAtMs: 1700, Cwd: "/w"},
		{PID: 322, Command: "sleep 9"},
	}, got)
}

func TestListSessionProcesses_GivenDaemonError_ThenErrorReturned(t *testing.T) {
	rt, d := newProcessRig(t)
	d.failList = &protorpc.Error{Code: protorpc.CodeInternal, Message: "boom"}

	_, err := rt.ListSessionProcesses(context.Background(), convOf(4))

	require.Error(t, err)
}

func TestKillSessionProcess_GivenProcess_ThenRequestCarriesPidAndStartTime(t *testing.T) {
	rt, d := newProcessRig(t)
	rt.rememberOrigin(convOf(4), "peer-A")

	require.NoError(t, rt.KillSessionProcess(context.Background(), convOf(4), 321, 1700))

	require.Len(t, d.killReqs, 1)
	assert.Equal(t, convOf(4), d.killReqs[0].GetConversationId())
	assert.Equal(t, "peer-A", d.killReqs[0].GetPeerFingerprint())
	assert.Equal(t, uint32(321), d.killReqs[0].GetPid())
	assert.Equal(t, int64(1700), d.killReqs[0].GetStartedAtMs())
}

func TestKillSessionProcess_GivenNotOwned_ThenMapsToSentinel(t *testing.T) {
	rt, d := newProcessRig(t)
	d.failKill = &protorpc.Error{Code: rpcerror.CodeSessionProcessNotOwned, Message: "not owned"}

	err := rt.KillSessionProcess(context.Background(), convOf(4), 321, 1700)

	assert.ErrorIs(t, err, agentruntime.ErrSessionProcessNotOwned)
}

func TestKillSessionProcess_GivenOtherDaemonError_ThenNotTheSentinel(t *testing.T) {
	rt, d := newProcessRig(t)
	d.failKill = &protorpc.Error{Code: protorpc.CodeInternal, Message: "boom"}

	err := rt.KillSessionProcess(context.Background(), convOf(4), 321, 1700)

	require.Error(t, err)
	assert.NotErrorIs(t, err, agentruntime.ErrSessionProcessNotOwned)
}

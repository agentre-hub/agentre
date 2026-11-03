package app

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/port_forward_svc"
)

// stubSessionProcessChat 只实现会话进程这一块；嵌入的 ChatSvc 留 nil，碰到别的方法会当场 panic。
type stubSessionProcessChat struct {
	chat_svc.ChatSvc

	listResp     *chat_svc.ListSessionProcessesResponse
	execDeviceID int64
	err          error

	gotList *chat_svc.ListSessionProcessesRequest
	gotKill *chat_svc.KillSessionProcessRequest
	gotSID  int64
}

func (s *stubSessionProcessChat) ListSessionProcesses(_ context.Context, req *chat_svc.ListSessionProcessesRequest) (*chat_svc.ListSessionProcessesResponse, error) {
	s.gotList = req
	return s.listResp, s.err
}

func (s *stubSessionProcessChat) KillSessionProcess(_ context.Context, req *chat_svc.KillSessionProcessRequest) (*chat_svc.KillSessionProcessResponse, error) {
	s.gotKill = req
	return &chat_svc.KillSessionProcessResponse{}, s.err
}

func (s *stubSessionProcessChat) SessionExecDevice(_ context.Context, sessionID int64) (int64, error) {
	s.gotSID = sessionID
	return s.execDeviceID, s.err
}

func withStubSessionProcessChat(t *testing.T, stub *stubSessionProcessChat) *App {
	t.Helper()
	original := chat_svc.Chat()
	t.Cleanup(func() { chat_svc.RegisterChat(original) })
	chat_svc.RegisterChat(stub)
	return &App{ctx: context.Background()}
}

func TestSessionProcessBindingsPassThrough(t *testing.T) {
	stub := &stubSessionProcessChat{listResp: &chat_svc.ListSessionProcessesResponse{
		Processes: []chat_svc.SessionProcessView{{PID: 10, Ports: []int{3000}}},
	}}
	a := withStubSessionProcessChat(t, stub)

	resp, err := a.ListSessionProcesses(42)
	require.NoError(t, err)
	assert.Equal(t, int64(42), stub.gotList.SessionID)
	require.Len(t, resp.Processes, 1)
	assert.Equal(t, 10, resp.Processes[0].PID)

	require.NoError(t, a.KillSessionProcess(42, 10, 1700))
	assert.Equal(t, &chat_svc.KillSessionProcessRequest{SessionID: 42, PID: 10, StartedAt: 1700}, stub.gotKill)

	stub.err = errors.New("boom")
	_, err = a.ListSessionProcesses(42)
	assert.EqualError(t, err, "boom")
	assert.EqualError(t, a.KillSessionProcess(42, 10, 1700), "boom")
}

func TestOpenSessionProcessPort(t *testing.T) {
	stub := &stubSessionProcessChat{}
	a := withStubSessionProcessChat(t, stub)

	url, err := a.OpenSessionProcessPort(42, 3000)
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:3000", url)
	assert.Equal(t, int64(42), stub.gotSID)

	_, err = a.OpenSessionProcessPort(42, 0)
	assert.Error(t, err, "端口越界")
	_, err = a.OpenSessionProcessPort(42, 70000)
	assert.Error(t, err, "端口越界")

}

// withRemoteSession 让 42 号会话落在 7 号设备上:chat 桩交回执行设备 7。
func withRemoteSession(t *testing.T, pf *stubPortForwardSvc) (*App, *stubSessionProcessChat) {
	t.Helper()
	stub := &stubSessionProcessChat{execDeviceID: 7}
	a, _ := withForwards(t, pf)
	original := chat_svc.Chat()
	t.Cleanup(func() { chat_svc.RegisterChat(original) })
	chat_svc.RegisterChat(stub)
	return a, stub
}

// Given 远程会话, When 打开端口, Then 取执行设备、确保转发存在、绑本机监听并交回环回地址。
func TestOpenSessionProcessPortRemoteEnsuresForwardAndOpensIt(t *testing.T) {
	pf := &stubPortForwardSvc{mapping: &port_forward_svc.MappingView{ID: "11", Port: 3000, Enabled: true}}
	a, _ := withRemoteSession(t, pf)

	address, err := a.OpenSessionProcessPort(42, 3000)
	require.NoError(t, err)
	assert.Equal(t, "7", pf.gotDeviceID)
	assert.Equal(t, 3000, pf.gotPort)

	parsed, perr := url.Parse(address)
	require.NoError(t, perr)
	host, _, serr := net.SplitHostPort(parsed.Host)
	require.NoError(t, serr)
	assert.True(t, net.ParseIP(host).IsLoopback(), "地址不是环回的: "+address)
	assert.True(t, listenerAlive(t, address))
}

// Given 确保转发失败(设备离线), Then 业务码带着过桥,且不绑监听。
func TestOpenSessionProcessPortRemoteEnsureFailurePropagates(t *testing.T) {
	pf := &stubPortForwardSvc{err: &httputils.Error{Code: code.PortForwardDeviceOffline, Msg: "离线"}}
	a, _ := withRemoteSession(t, pf)

	_, err := a.OpenSessionProcessPort(42, 3000)
	require.Error(t, err)
	assert.Equal(t, "agentre-code:21000 离线", err.Error())
}

// Given 服务说执行设备取不到(远程会话没记录设备), Then 报错而不是对设备 0 开转发。
func TestOpenSessionProcessPortRemoteWithoutExecDeviceFails(t *testing.T) {
	pf := &stubPortForwardSvc{}
	a, stub := withRemoteSession(t, pf)
	stub.execDeviceID, stub.err = 0, errors.New("remote device not found")

	_, err := a.OpenSessionProcessPort(42, 3000)
	require.Error(t, err)
	assert.Empty(t, pf.gotDeviceID, "没有执行设备时不该去确保转发")
}

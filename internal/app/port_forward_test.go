package app

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/service/port_forward_svc"
)

// stubPortForwardSvc 记下这一层到底把什么转给了服务层。绑定层是一层薄壳,值得
// 钉住的就是这两件事:参数原样过去了,失败**带着业务码**过桥回来。
type stubPortForwardSvc struct {
	gotDeviceID  string
	gotMappingID string
	gotPort      int
	gotName      string
	gotEnabled   bool

	mappings []port_forward_svc.MappingView
	mapping  *port_forward_svc.MappingView
	err      error
}

func (s *stubPortForwardSvc) List(_ context.Context, deviceID string) ([]port_forward_svc.MappingView, error) {
	s.gotDeviceID = deviceID
	return s.mappings, s.err
}

func (s *stubPortForwardSvc) Create(_ context.Context, deviceID string, port int, name string) (*port_forward_svc.MappingView, error) {
	s.gotDeviceID, s.gotPort, s.gotName = deviceID, port, name
	return s.mapping, s.err
}

func (s *stubPortForwardSvc) SetEnabled(_ context.Context, deviceID, mappingID string, enabled bool) (*port_forward_svc.MappingView, error) {
	s.gotDeviceID, s.gotMappingID, s.gotEnabled = deviceID, mappingID, enabled
	return s.mapping, s.err
}

func (s *stubPortForwardSvc) Delete(_ context.Context, deviceID, mappingID string) error {
	s.gotDeviceID, s.gotMappingID = deviceID, mappingID
	return s.err
}

func withStubPortForward(t *testing.T, stub *stubPortForwardSvc) *App {
	t.Helper()
	original := port_forward_svc.Default()
	t.Cleanup(func() { port_forward_svc.SetDefault(original) })
	port_forward_svc.SetDefault(stub)
	return &App{ctx: context.Background()}
}

func TestPortForwardListPassesThroughAndCarriesTheCodeAcrossTheBridge(t *testing.T) {
	stub := &stubPortForwardSvc{mappings: []port_forward_svc.MappingView{
		{ID: "11", Port: 3000, Name: "dev server", Enabled: true},
	}}
	a := withStubPortForward(t, stub)

	views, err := a.PortForwardList("7")
	require.NoError(t, err)
	assert.Equal(t, "7", stub.gotDeviceID)
	require.Len(t, views, 1)
	assert.Equal(t, "11", views[0].ID)
	assert.Equal(t, 3000, views[0].Port)

	// 设备离线这一态必须**过得了 wails 那座桥**:wails 只把 error 序列化成
	// Error(),码在那里会被丢掉。前端据此不渲染新增入口,而不是拿到一句笼统
	// 的失败文本去猜。契约在 coded_error.go,对面在 remote-fs-port.ts。
	stub.err = &httputils.Error{Code: code.PortForwardDeviceOffline, Msg: "这台设备此刻连不上"}
	_, err = a.PortForwardList("7")
	require.Error(t, err)
	assert.Equal(t, "agentre-code:21000 这台设备此刻连不上", err.Error())
}

func TestPortForwardCreatePassesThroughAndDistinguishesTheUserFixableFailures(t *testing.T) {
	stub := &stubPortForwardSvc{mapping: &port_forward_svc.MappingView{
		ID: "11", Port: 3000, Name: "dev server", Enabled: true,
	}}
	a := withStubPortForward(t, stub)

	view, err := a.PortForwardCreate("7", 3000, "dev server")
	require.NoError(t, err)
	assert.Equal(t, "7", stub.gotDeviceID)
	assert.Equal(t, 3000, stub.gotPort)
	assert.Equal(t, "dev server", stub.gotName)
	assert.Equal(t, "11", view.ID)

	// 端口已被声明与端口越界是新增表单要分开说的两件事,两个码各自过桥。
	stub.err = &httputils.Error{Code: code.PortForwardPortTaken, Msg: "端口已映射"}
	_, err = a.PortForwardCreate("7", 3000, "dev server")
	require.Error(t, err)
	assert.Equal(t, "agentre-code:21002 端口已映射", err.Error())

	stub.err = &httputils.Error{Code: code.PortForwardInvalidPort, Msg: "端口越界"}
	_, err = a.PortForwardCreate("7", 70000, "dev server")
	require.Error(t, err)
	assert.Equal(t, "agentre-code:21003 端口越界", err.Error())
}

func TestPortForwardSetEnabledPassesThrough(t *testing.T) {
	stub := &stubPortForwardSvc{mapping: &port_forward_svc.MappingView{
		ID: "11", Port: 3000, Name: "dev server", Enabled: false,
	}}
	a := withStubPortForward(t, stub)

	view, err := a.PortForwardSetEnabled("7", "11", false)
	require.NoError(t, err)
	assert.Equal(t, "7", stub.gotDeviceID)
	assert.Equal(t, "11", stub.gotMappingID)
	assert.False(t, stub.gotEnabled)
	assert.False(t, view.Enabled)

	stub.err = &httputils.Error{Code: code.PortForwardNotDeclared, Msg: "已不在这台设备上"}
	_, err = a.PortForwardSetEnabled("7", "11", true)
	require.Error(t, err)
	assert.Equal(t, "agentre-code:21001 已不在这台设备上", err.Error())
}

func TestPortForwardDeletePassesThrough(t *testing.T) {
	stub := &stubPortForwardSvc{}
	a := withStubPortForward(t, stub)

	require.NoError(t, a.PortForwardDelete("7", "11"))
	assert.Equal(t, "7", stub.gotDeviceID)
	assert.Equal(t, "11", stub.gotMappingID)

	stub.err = &httputils.Error{Code: code.PortForwardDeviceOffline, Msg: "连不上"}
	err := a.PortForwardDelete("7", "11")
	require.Error(t, err)
	assert.Equal(t, "agentre-code:21000 连不上", err.Error())
}

func TestPortForwardKeepsUncodedFailuresIntact(t *testing.T) {
	// 编一个码比不给码更糟:没带码的失败原样过去,前端落到 unknown 那一档并
	// 把原文带上,而不是被伪装成某一种已知失败。
	stub := &stubPortForwardSvc{err: errors.New("boom")}
	a := withStubPortForward(t, stub)

	_, err := a.PortForwardList("7")
	require.Error(t, err)
	assert.Equal(t, "boom", err.Error())
}

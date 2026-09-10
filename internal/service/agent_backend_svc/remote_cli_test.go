package agent_backend_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

const remoteTestFingerprint = "sha256:remote-test-device"

// fakeRemoteCLI 是 remoteCLIPort 的测试替身：记录调用次数 / 入参，
// 按 setup 时塞的 resp/err 回应。
type fakeRemoteCLI struct {
	resolveCalls       int
	resolveDeviceID    int64
	resolveBackendType string
	resolveResp        *ResolveCLIPathResponse
	resolveErr         error

	probeCalls int
	probeResp  *handlers.CLIProbeResult
	probeErr   error
}

func (f *fakeRemoteCLI) ResolveCLIPath(_ context.Context, deviceID int64, backendType string) (*ResolveCLIPathResponse, error) {
	f.resolveCalls++
	f.resolveDeviceID = deviceID
	f.resolveBackendType = backendType
	return f.resolveResp, f.resolveErr
}

func (f *fakeRemoteCLI) Probe(_ context.Context, _ int64, _ handlers.CLIProbeParams) (*handlers.CLIProbeResult, error) {
	f.probeCalls++
	return f.probeResp, f.probeErr
}

func newSvcWithRemote(t *testing.T, remote remoteCLIPort) *agentBackendSvc {
	installRemoteDeviceFixture(t)
	return &agentBackendSvc{remoteCLI: remote}
}

func installRemoteDeviceFixture(t *testing.T) {
	t.Helper()
	ctrl := gomock.NewController(t)
	rd := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	rd.EXPECT().DeviceFingerprint().Return("sha256:local-test-device", nil).AnyTimes()
	rd.EXPECT().List(gomock.Any()).Return([]*remote_device_svc.DeviceView{{
		ID: 42, DaemonFingerprint: remoteTestFingerprint, Name: "remote-test-device", Online: true,
	}}, nil).AnyTimes()
	previous := remote_device_svc.Default()
	remote_device_svc.SetDefault(rd)
	t.Cleanup(func() { remote_device_svc.SetDefault(previous) })
}

func assertRemoteProbeSoftFail(
	t *testing.T,
	probeErr error,
	backendID int64,
	messageContains string,
) {
	t.Helper()
	fake := &fakeRemoteCLI{probeErr: probeErr}
	svc := setupSvcWithRemoteAndBackend(t, fake, remoteTestBackend(backendID, remoteTestFingerprint))
	resp, err := svc.Test(context.Background(), &TestBackendRequest{ID: backendID})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.False(t, resp.OK)
	assert.Contains(t, resp.Message, messageContains)
	assert.Equal(t, 1, fake.probeCalls)
}

func TestResolveCLIPath_RemoteDispatch(t *testing.T) {
	convey.Convey("ResolveCLIPath 远端分支（DeviceID 非空）", t, func() {
		ctx := context.Background()

		convey.Convey("DeviceID 空 → 不调远端", func() {
			fake := &fakeRemoteCLI{}
			s := newSvcWithRemote(t, fake)
			resp, err := s.ResolveCLIPath(ctx, &ResolveCLIPathRequest{Type: "claudecode", DeviceID: ""})
			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.Equal(t, 0, fake.resolveCalls)
		})

		convey.Convey("DeviceID 非空 → 调远端，透传 path/found", func() {
			fake := &fakeRemoteCLI{resolveResp: &ResolveCLIPathResponse{Path: "/remote/bin/claude", Found: true}}
			s := newSvcWithRemote(t, fake)
			resp, err := s.ResolveCLIPath(ctx, &ResolveCLIPathRequest{Type: "claudecode", DeviceID: remoteTestFingerprint})
			require.NoError(t, err)
			assert.Equal(t, 1, fake.resolveCalls)
			assert.Equal(t, int64(42), fake.resolveDeviceID)
			assert.Equal(t, "claudecode", fake.resolveBackendType)
			assert.Equal(t, "/remote/bin/claude", resp.Path)
			assert.True(t, resp.Found)
		})

		convey.Convey("DeviceID 非法 → InvalidParameter，且不调远端", func() {
			fake := &fakeRemoteCLI{}
			s := newSvcWithRemote(t, fake)
			_, err := s.ResolveCLIPath(ctx, &ResolveCLIPathRequest{Type: "claudecode", DeviceID: "not-a-number"})
			require.Error(t, err)
			assert.Equal(t, 0, fake.resolveCalls)
		})

		convey.Convey("远端 device 不存在 → 映射到 RemoteDeviceNotFound", func() {
			fake := &fakeRemoteCLI{resolveErr: ErrRemoteDeviceNotFound}
			s := newSvcWithRemote(t, fake)
			_, err := s.ResolveCLIPath(ctx, &ResolveCLIPathRequest{Type: "claudecode", DeviceID: remoteTestFingerprint})
			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, code.RemoteDeviceNotFound, httpErr.Code)
		})

		convey.Convey("远端拨号失败 → 映射到 RemoteDeviceDialFailed", func() {
			fake := &fakeRemoteCLI{resolveErr: ErrRemoteDialFailed}
			s := newSvcWithRemote(t, fake)
			_, err := s.ResolveCLIPath(ctx, &ResolveCLIPathRequest{Type: "claudecode", DeviceID: remoteTestFingerprint})
			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, code.RemoteDeviceDialFailed, httpErr.Code)
		})

		convey.Convey("ctx 超时 → 映射到 RemoteDeviceTimeout", func() {
			fake := &fakeRemoteCLI{resolveErr: context.DeadlineExceeded}
			s := newSvcWithRemote(t, fake)
			_, err := s.ResolveCLIPath(ctx, &ResolveCLIPathRequest{Type: "claudecode", DeviceID: remoteTestFingerprint})
			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, code.RemoteDeviceTimeout, httpErr.Code)
		})

		convey.Convey("其它远端错误 → 映射到 RemoteCLIDetectFailed，并带上原文", func() {
			fake := &fakeRemoteCLI{resolveErr: errors.New("rpc: io.EOF")}
			s := newSvcWithRemote(t, fake)
			_, err := s.ResolveCLIPath(ctx, &ResolveCLIPathRequest{Type: "claudecode", DeviceID: remoteTestFingerprint})
			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, code.RemoteCLIDetectFailed, httpErr.Code)
			assert.Contains(t, httpErr.Msg, "rpc: io.EOF")
			assert.NotContains(t, httpErr.Msg, "MISSING")
		})
	})
}

// setupSvcWithRemoteAndBackend 构造一个注入了 fake remoteCLI 的 agentBackendSvc
// 并把传入的 backend 通过 mock repo 暴露给 Test 调用（按 backend.ID 命中 Find）。
// 返回 svc 让用例可继续注入 gateway / prober 等。
func setupSvcWithRemoteAndBackend(t *testing.T, remote remoteCLIPort, backend *agent_backend_entity.AgentBackend) *agentBackendSvc {
	t.Helper()
	installRemoteDeviceFixture(t)
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl)
	repo.EXPECT().Find(gomock.Any(), backend.ID).Return(backend, nil).AnyTimes()
	agent_backend_repo.RegisterAgentBackend(repo)
	return &agentBackendSvc{
		remoteCLI: remote,
		probes:    map[string]context.CancelFunc{},
	}
}

// remoteTestBackend 返回一个能通过 entity.Check 的最小可用 backend。
// type 默认 claudecode（与远端 CLI 场景对齐）；调用方传 deviceID/ID 区分用例。
func remoteTestBackend(id int64, deviceID string) *agent_backend_entity.AgentBackend {
	return &agent_backend_entity.AgentBackend{
		ID:                id,
		Type:              string(agent_backend_entity.TypeClaudeCode),
		Name:              "remote-cc",
		DeviceFingerprint: deviceID,
		Status:            consts.ACTIVE,
	}
}

func TestTest_RemotePath_CallsRemoteProbe(t *testing.T) {
	convey.Convey("远端 device → 调 daemon cli.probe，透传 pong", t, func() {
		fake := &fakeRemoteCLI{probeResp: &handlers.CLIProbeResult{Text: "pong"}}
		svc := setupSvcWithRemoteAndBackend(t, fake, remoteTestBackend(1, remoteTestFingerprint))
		resp, err := svc.Test(context.Background(), &TestBackendRequest{ID: 1})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.True(t, resp.OK)
		assert.Equal(t, "pong", resp.Message)
		assert.Equal(t, 1, fake.probeCalls)
		assert.Equal(t, 0, fake.resolveCalls, "Probe 路径不应触碰 ResolveCLIPath")
	})
}

func TestTest_LocalPath_DoesNotCallRemoteProbe(t *testing.T) {
	convey.Convey("本地 device（DeviceID 空）→ 不拨远端", t, func() {
		fake := &fakeRemoteCLI{}
		// 用 ID==0 + draft 路径绕开 mock repo Find；本地分支需要 prober / provider。
		// 这里只关心「不调远端」，让本地分支自然失败也行。
		svc := &agentBackendSvc{remoteCLI: fake, probes: map[string]context.CancelFunc{}}
		_, _ = svc.Test(context.Background(), &TestBackendRequest{
			Type:           string(agent_backend_entity.TypeClaudeCode),
			Name:           "local-cc",
			LLMProviderKey: "",
		})
		assert.Equal(t, 0, fake.probeCalls)
	})
}

func TestTest_RemoteDeviceNotFound_ReturnsSoftFailWithI18n(t *testing.T) {
	convey.Convey("远端 device 不存在 → OK:false + RemoteDeviceNotFound 文案", t, func() {
		assertRemoteProbeSoftFail(t, ErrRemoteDeviceNotFound, 3, "远端设备")
	})
}

func TestTest_RemoteDialFailed_ReturnsSoftFailWithI18n(t *testing.T) {
	convey.Convey("远端拨号失败 → OK:false + RemoteDeviceDialFailed 文案", t, func() {
		assertRemoteProbeSoftFail(t, ErrRemoteDialFailed, 4, "agentred")
	})
}

func TestTest_RemoteTimeout_ReturnsSoftFailWithI18n(t *testing.T) {
	convey.Convey("远端响应超时 → OK:false + RemoteDeviceTimeout 文案", t, func() {
		fake := &fakeRemoteCLI{probeErr: context.DeadlineExceeded}
		svc := setupSvcWithRemoteAndBackend(t, fake, remoteTestBackend(5, remoteTestFingerprint))
		resp, err := svc.Test(context.Background(), &TestBackendRequest{ID: 5})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.False(t, resp.OK)
		assert.Contains(t, resp.Message, "超时")
	})
}

func TestTest_RemoteGenericError_WrappedInProbeFailedI18n(t *testing.T) {
	convey.Convey("远端 cli.probe 返回非 sentinel 错误 → RemoteCLIProbeFailed 包装原文", t, func() {
		fake := &fakeRemoteCLI{probeErr: errors.New("claude exited code 1")}
		svc := setupSvcWithRemoteAndBackend(t, fake, remoteTestBackend(6, remoteTestFingerprint))
		resp, err := svc.Test(context.Background(), &TestBackendRequest{ID: 6})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.False(t, resp.OK)
		assert.Contains(t, resp.Message, "远端测试连接失败")
		assert.Contains(t, resp.Message, "claude exited code 1")
	})
}

func TestTest_RemoteInvalidDeviceID_ReturnsInvalidParameter(t *testing.T) {
	convey.Convey("DeviceID 非法字符串 → InvalidParameter，不拨远端", t, func() {
		fake := &fakeRemoteCLI{}
		svc := setupSvcWithRemoteAndBackend(t, fake, remoteTestBackend(7, "not-a-number"))
		_, err := svc.Test(context.Background(), &TestBackendRequest{ID: 7})
		require.Error(t, err)
		assert.Equal(t, 0, fake.probeCalls)
	})
}

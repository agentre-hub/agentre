package chat_svc

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo/mock_syncstate_repo"
	"github.com/agentre-hub/agentre/internal/service/dispatch_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// 派到另一台桌面端的新会话（spec 决策 11「派到另一台桌面端」）：发起方选定了这台设备与引擎
// 配置，这台桌面端复核**自己**通过派发（成员 + 配置 + 项目）后照此执行；不通过把逐设备原因
// 返回发起方显示，绝不另挑设备或另选配置。

func installPeerSyncState(t *testing.T, ctrl *gomock.Controller) *mock_syncstate_repo.MockSyncStateRepo {
	t.Helper()
	sync := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	prev := syncstate_repo.SyncState()
	syncstate_repo.RegisterSyncState(sync)
	t.Cleanup(func() { syncstate_repo.RegisterSyncState(prev) })
	return sync
}

func TestAuthorizePeerFresh_GivenSelfPassesDispatch_ThenRunsPinnedToSelfOnTheNamedProfile(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	ctrl := gomock.NewController(t)
	installPairedDevice(t, ctrl, 7) // 本机指纹 sha256:self
	sync := installPeerSyncState(t, ctrl)
	sync.EXPECT().FindLocalID(ctx, syncwire.KindEngineProfile, "profile-sync-31").Return(int64(31), nil)

	got, err := svc.authorizePeerFresh(ctx, 3, 9, "profile-sync-31")

	require.NoError(t, err)
	assert.Equal(t, peerFreshPick{engineProfileID: 31, device: "sha256:self"}, got)
	require.Len(t, m.dispatch.validated, 1)
	assert.Equal(t, dispatch_svc.PlanRequest{AgentID: 3, ProjectID: 9, EngineProfileID: 31, ManualDevice: "sha256:self"}, m.dispatch.validated[0])
	assert.Empty(t, m.dispatch.reqs, "复核通过后不再另起一次派发方案")
}

func TestAuthorizePeerFresh_GivenNoProfileNamed_ThenValidatesMembersEffectiveProfile(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	installPairedDevice(t, gomock.NewController(t), 7)

	got, err := svc.authorizePeerFresh(ctx, 3, 0, "")

	require.NoError(t, err)
	assert.Equal(t, peerFreshPick{device: "sha256:self"}, got)
	assert.Equal(t, int64(0), m.dispatch.validated[0].EngineProfileID)
}

func TestAuthorizePeerFresh_GivenSelfFailsDispatch_ThenReturnsSelfsReasonAndNeverRepicks(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	installPairedDevice(t, gomock.NewController(t), 7)
	m.dispatch.validateErr = errors.New("manual device unavailable")
	m.dispatch.plan = &dispatch_svc.Plan{Found: true, Chosen: "sha256:other", Devices: []*dispatch_svc.DeviceResult{
		{Fingerprint: "sha256:self", IsSelf: true, Eligible: false, Reason: &dispatch_svc.ReasonView{Code: "runtime_missing"}},
		{Fingerprint: "sha256:other", Name: "Box", Eligible: true, Chosen: true},
	}}

	_, err := svc.authorizePeerFresh(ctx, 3, 9, "")

	var noDevice *NoEligibleDeviceError
	require.ErrorAs(t, err, &noDevice)
	require.Len(t, noDevice.Devices, 1, "只带这台桌面端自己的结果：别的设备不是这一轮的候选")
	assert.Equal(t, devicefp.Carrier("sha256:self"), noDevice.Devices[0].Fingerprint)
	assert.Contains(t, err.Error(), "runtime_missing")
	assert.NotContains(t, err.Error(), "Box")
}

func TestAuthorizePeerFresh_GivenUnknownProfileSyncID_ThenRejectsWithoutValidating(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	ctrl := gomock.NewController(t)
	installPairedDevice(t, ctrl, 7)
	sync := installPeerSyncState(t, ctrl)
	sync.EXPECT().FindLocalID(ctx, syncwire.KindEngineProfile, "gone").Return(int64(0), nil)

	_, err := svc.authorizePeerFresh(ctx, 3, 9, "gone")

	requireEngineCode(t, err, code.EngineProfileNotFound)
	assert.Empty(t, m.dispatch.validated)
}

// 复核不通过时整条新会话路径在建会话之前就返回：没有会话行、没有第二次派发、没有任何一轮。
func TestRunFreshPeerSession_GivenSelfFailsDispatch_ThenReturnsReasonBeforeCreatingAnything(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	ctrl := gomock.NewController(t)
	installPairedDevice(t, ctrl, 7)
	sync := installPeerSyncState(t, ctrl)
	sync.EXPECT().FindLocalID(ctx, syncwire.KindAgent, "agent-sync").Return(int64(3), nil)
	m.dispatch.validateErr = errors.New("manual device unavailable")
	m.dispatch.plan = &dispatch_svc.Plan{Devices: []*dispatch_svc.DeviceResult{
		{Fingerprint: "sha256:self", IsSelf: true, Reason: &dispatch_svc.ReasonView{Code: "project_path_missing"}},
	}}

	// m.session 是严格 mock：任何 Create / Update 都会让用例失败。
	_, err := svc.runFreshPeerSession(ctx, wire.RunParams{AgentSyncID: "agent-sync", ConversationID: "00000000-0000-7000-8000-000000000001", UserText: "hi"}, PeerSessionSource{Device: "fp-peer"}, nil)

	var noDevice *NoEligibleDeviceError
	require.ErrorAs(t, err, &noDevice)
	assert.Contains(t, err.Error(), "project_path_missing")
	require.Len(t, m.dispatch.reqs, 1, "只为取原因问了一次方案，不是再挑一次")
	assert.Equal(t, devicefp.Carrier("sha256:self"), m.dispatch.reqs[0].ManualDevice)
}

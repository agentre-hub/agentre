package chat_svc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/daemon/relaytransport"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo/mock_llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo/mock_project_location_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
)

// stubServerSvc 是 server_svc.ServerSvc 的最小测试实现：只有 ListDevices / AccessToken
// 有意义，其余方法返回零值（chat_svc 在桌面目标判定里只调 ListDevices）。
type stubServerSvc struct {
	devices []server_svc.Device
	err     error
}

func (s stubServerSvc) GetState(context.Context) (*server_state_entity.ServerState, error) {
	return nil, nil
}
func (s stubServerSvc) StartLogin(context.Context, string) (*server_svc.StartLoginResult, error) {
	return nil, nil
}
func (s stubServerSvc) PollLoginToken(context.Context, string) (bool, error) { return false, nil }
func (s stubServerSvc) CancelLogin(context.Context) error                    { return nil }
func (s stubServerSvc) ListDevices(context.Context) ([]server_svc.Device, error) {
	return s.devices, s.err
}
func (s stubServerSvc) Logout(context.Context) error                     { return nil }
func (s stubServerSvc) Refresh(context.Context) error                    { return nil }
func (s stubServerSvc) RefreshWithBackoff(context.Context)               {}
func (s stubServerSvc) Offline() bool                                    { return false }
func (s stubServerSvc) ClearLogin(context.Context) error                 { return nil }
func (s stubServerSvc) CheckURL(context.Context, string) (string, error) { return "", nil }
func (s stubServerSvc) SetEmitter(func(any))                             {}
func (s stubServerSvc) AccessToken() string                              { return "tok" }
func (s stubServerSvc) NewInboundHubLink(context.Context) (*relaytransport.HubLink, error) {
	return nil, nil
}
func (s stubServerSvc) DialDaemonRelay(context.Context, string, string) (client.ProtobufConnection, error) {
	return nil, nil
}
func (s stubServerSvc) DialDesktopRelay(context.Context, string, string) (client.ProtobufConnection, error) {
	return nil, nil
}
func (s stubServerSvc) SyncPush(context.Context, []syncwire.PushItem) ([]syncwire.PushResult, error) {
	return nil, nil
}
func (s stubServerSvc) SyncPull(context.Context, int64, int) (*syncwire.PullPage, error) {
	return nil, nil
}
func (s stubServerSvc) ReportLocalPaths(context.Context, []syncwire.LocalPathReportItem) error {
	return nil
}
func (s stubServerSvc) PutAvatar(context.Context, string, string, string) error { return nil }
func (s stubServerSvc) GetAvatar(context.Context, string) (string, string, error) {
	return "", "", nil
}

// setupDesktopTargetTest 装配桌面目标可用性判定所需的最小 mock 集：账号设备清单经
// server_svc stub 注入，本机指纹固定为 sha256:self（不走 setupPickExecTargetTest 的
// 「无指纹」默认桩，因为本机判定是这套测试的核心）。
func setupDesktopTargetTest(t *testing.T, devices []server_svc.Device) (context.Context, *pickExecTargetMocks, chat_svc.ChatSvc) {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := &pickExecTargetMocks{
		execTarget:         mock_agent_repo.NewMockAgentExecTargetRepo(ctrl),
		execTargetOverride: mock_agent_repo.NewMockAgentExecTargetOverrideRepo(ctrl),
		backend:            mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl),
		provider:           mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl),
		project:            mock_project_repo.NewMockProjectRepo(ctrl),
		projectLocation:    mock_project_location_repo.NewMockProjectLocationRepo(ctrl),
		remoteDevice:       mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl),
	}

	previousExecTarget := agent_repo.AgentExecTarget()
	previousOverride := agent_repo.AgentExecTargetOverride()
	previousBackend := agent_backend_repo.AgentBackend()
	previousProvider := llm_provider_repo.LLMProvider()
	previousProject := project_repo.Project()
	previousProjectLocation := project_location_repo.ProjectLocation()
	previousRemoteDevice := remote_device_svc.Default()
	previousServer := server_svc.Server()

	agent_repo.RegisterAgentExecTarget(m.execTarget)
	agent_repo.RegisterAgentExecTargetOverride(m.execTargetOverride)
	agent_backend_repo.RegisterAgentBackend(m.backend)
	llm_provider_repo.RegisterLLMProvider(m.provider)
	project_repo.RegisterProject(m.project)
	project_location_repo.RegisterProjectLocation(m.projectLocation)
	remote_device_svc.SetDefault(m.remoteDevice)
	server_svc.SetDefault(stubServerSvc{devices: devices})

	m.execTargetOverride.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	m.remoteDevice.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()
	m.backend.EXPECT().ListByDevice(gomock.Any(), "sha256:self").Return(nil, nil).AnyTimes()
	// 具名目标不在账号清单时退回本机配对表（localPairedDeviceView → List）。
	m.remoteDevice.EXPECT().List(gomock.Any()).Return(nil, nil).AnyTimes()

	t.Cleanup(func() {
		agent_repo.RegisterAgentExecTarget(previousExecTarget)
		agent_repo.RegisterAgentExecTargetOverride(previousOverride)
		agent_backend_repo.RegisterAgentBackend(previousBackend)
		llm_provider_repo.RegisterLLMProvider(previousProvider)
		project_repo.RegisterProject(previousProject)
		project_location_repo.RegisterProjectLocation(previousProjectLocation)
		remote_device_svc.SetDefault(previousRemoteDevice)
		server_svc.SetDefault(previousServer)
	})
	return context.Background(), m, chat_svc.NewChat(nil)
}

// Given another running desktop is a named exec target, when this desktop lists
// availability, then it is selectable and classified as a desktop (R18 / R15).
func TestListExecTargetAvailability_GivenRunningNamedDesktop_ThenSelectableAndKindDesktop(t *testing.T) {
	ctx, m, svc := setupDesktopTargetTest(t, []server_svc.Device{{
		Kind: "desktop", Fingerprint: "sha256:desktop-b", Name: "MacBook Pro", Online: true,
	}})
	m.execTarget.EXPECT().ListByAgent(ctx, int64(40)).Return([]*agent_entity.AgentExecTarget{
		{ID: 1, AgentID: 40, AgentBackendID: 71, SortOrder: 0},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(71)).Return(&agent_backend_entity.AgentBackend{
		ID: 71, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "sha256:desktop-b",
	}, nil)

	statuses, err := svc.ListExecTargetAvailability(ctx, 40, 0)
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.True(t, statuses[0].Available)
	assert.Equal(t, "desktop", statuses[0].Kind, "a named desktop target must be classified as desktop")
	assert.Equal(t, chat_svc.BlockReason(""), statuses[0].Reason)
}

// Given a named desktop target whose Agentre App is not running, when availability
// is listed, then the desktop-specific wording surfaces — not the agentred offline
// wording (R2).
func TestListExecTargetAvailability_GivenNamedDesktopAppNotRunning_ThenDesktopNotRunningReason(t *testing.T) {
	ctx, m, svc := setupDesktopTargetTest(t, []server_svc.Device{{
		Kind: "desktop", Fingerprint: "sha256:desktop-b", Name: "MacBook Pro", Online: false,
	}})
	m.execTarget.EXPECT().ListByAgent(ctx, int64(41)).Return([]*agent_entity.AgentExecTarget{
		{ID: 1, AgentID: 41, AgentBackendID: 72, SortOrder: 0},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(72)).Return(&agent_backend_entity.AgentBackend{
		ID: 72, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "sha256:desktop-b",
	}, nil)

	statuses, err := svc.ListExecTargetAvailability(ctx, 41, 0)
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.False(t, statuses[0].Available)
	assert.Equal(t, chat_svc.BlockReasonExecTargetDesktopNotRunning, statuses[0].Reason)
	assert.NotEqual(t, chat_svc.BlockReasonExecTargetOffline, statuses[0].Reason,
		"desktop App not running must stay distinct from machine offline")
	assert.NotEmpty(t, statuses[0].Hint)
}

// Given a backend pointing at this desktop's own fingerprint, when availability
// is listed, then it stays a local target (no peer dial, no account lookup) —
// self target is never dispatched over the peer relay (R14 / R18).
func TestListExecTargetAvailability_GivenSelfFingerprint_ThenKindLocalAndAvailable(t *testing.T) {
	ctx, m, svc := setupDesktopTargetTest(t, nil) // 本机档不查账号清单
	m.execTarget.EXPECT().ListByAgent(ctx, int64(42)).Return([]*agent_entity.AgentExecTarget{
		{ID: 1, AgentID: 42, AgentBackendID: 73, SortOrder: 0},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(73)).Return(&agent_backend_entity.AgentBackend{
		ID: 73, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "sha256:self",
	}, nil)

	statuses, err := svc.ListExecTargetAvailability(ctx, 42, 0)
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.True(t, statuses[0].Available)
	assert.Equal(t, "local", statuses[0].Kind, "self stays local and never becomes a peer dial target")
}

// Given a self-fingerprint backend (R13 canonicalized local) with a project-bound
// session whose project IS configured locally, when availability is listed, then
// the tier is available and carries the local project path — not misjudged as
// project-path-missing. The local path lives on projects.path; project_locations
// has no row keyed by the self fingerprint (self is never a paired agentred).
func TestListExecTargetAvailability_GivenSelfFingerprintAndProjectBound_ThenLocalPathUsed(t *testing.T) {
	ctx, m, svc := setupDesktopTargetTest(t, nil) // 本机档不查账号清单
	m.execTarget.EXPECT().ListByAgent(ctx, int64(45)).Return([]*agent_entity.AgentExecTarget{
		{ID: 1, AgentID: 45, AgentBackendID: 76, SortOrder: 0},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(76)).Return(&agent_backend_entity.AgentBackend{
		ID: 76, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "sha256:self",
	}, nil)
	m.project.EXPECT().Find(ctx, int64(403)).Return(&project_entity.Project{ID: 403, LocalPathMissing: false, Path: "/local/proj"}, nil).Times(2)
	// 不注册 projectLocation 的任何 EXPECT：本机档一次都不该查 project_locations。

	statuses, err := svc.ListExecTargetAvailability(ctx, 45, 403)
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.True(t, statuses[0].Available, "a locally-configured project must be usable on the self backend")
	assert.Equal(t, "/local/proj", statuses[0].ProjectPath)
}

// Given a named desktop fingerprint that is not in the account device list, when
// availability is listed, then it degrades to unpaired (not a silent success).
func TestListExecTargetAvailability_GivenUnknownDesktopFingerprint_ThenUnpaired(t *testing.T) {
	ctx, m, svc := setupDesktopTargetTest(t, []server_svc.Device{{
		Kind: "desktop", Fingerprint: "sha256:other", Name: "Other", Online: true,
	}})
	m.execTarget.EXPECT().ListByAgent(ctx, int64(43)).Return([]*agent_entity.AgentExecTarget{
		{ID: 1, AgentID: 43, AgentBackendID: 74, SortOrder: 0},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(74)).Return(&agent_backend_entity.AgentBackend{
		ID: 74, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "sha256:desktop-b",
	}, nil)

	statuses, err := svc.ListExecTargetAvailability(ctx, 43, 0)
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.False(t, statuses[0].Available)
	assert.Equal(t, chat_svc.BlockReasonExecTargetUnpaired, statuses[0].Reason)
}

// Given the server device list is unreachable, when availability is listed for a
// named desktop target, then it degrades to unpaired instead of a silent success.
func TestListExecTargetAvailability_GivenServerListError_ThenUnpaired(t *testing.T) {
	ctx, m, svc := setupDesktopTargetTest(t, nil)
	server_svc.SetDefault(stubServerSvc{devices: nil, err: errors.New("server down")})
	t.Cleanup(func() { server_svc.SetDefault(stubServerSvc{devices: nil}) })
	m.execTarget.EXPECT().ListByAgent(ctx, int64(44)).Return([]*agent_entity.AgentExecTarget{
		{ID: 1, AgentID: 44, AgentBackendID: 75, SortOrder: 0},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(75)).Return(&agent_backend_entity.AgentBackend{
		ID: 75, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "sha256:desktop-b",
	}, nil)

	statuses, err := svc.ListExecTargetAvailability(ctx, 44, 0)
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.False(t, statuses[0].Available)
	assert.Equal(t, chat_svc.BlockReasonExecTargetUnpaired, statuses[0].Reason)
}

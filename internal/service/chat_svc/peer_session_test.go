package chat_svc

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

type peerSessionTestDeps struct {
	agent   *mock_agent_repo.MockAgentRepo
	backend *mock_agent_backend_repo.MockAgentBackendRepo
	session *mock_chat_repo.MockSessionRepo
	message *mock_chat_repo.MockMessageRepo
	device  *mock_remote_device_svc.MockRemoteDeviceSvc
	project *mock_project_repo.MockProjectRepo
	svc     *chatSvc
	// projects 是这台电脑上的项目清单，由用例按需摆好；projectListCalls 记下它被
	// 读了几次——「一次列举只读一遍」是这份清单唯一的性能约束，它得测得到。
	projects         []*project_entity.Project
	projectListCalls int
}

func setupPeerSessionTest(t *testing.T) *peerSessionTestDeps {
	t.Helper()
	ctrl := gomock.NewController(t)
	deps := &peerSessionTestDeps{
		agent:   mock_agent_repo.NewMockAgentRepo(ctrl),
		backend: mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl),
		session: mock_chat_repo.NewMockSessionRepo(ctrl),
		message: mock_chat_repo.NewMockMessageRepo(ctrl),
		device:  mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl),
		project: mock_project_repo.NewMockProjectRepo(ctrl),
		svc:     NewChat(NoopEmitter{}).(*chatSvc),
	}
	prevAgent, prevBackend, prevSession, prevMessage, prevDevice := agent_repo.Agent(), agent_backend_repo.AgentBackend(), chat_repo.Session(), chat_repo.Message(), remote_device_svc.Default()
	prevProject := project_repo.Project()
	agent_repo.RegisterAgent(deps.agent)
	agent_backend_repo.RegisterAgentBackend(deps.backend)
	chat_repo.RegisterSession(deps.session)
	chat_repo.RegisterMessage(deps.message)
	remote_device_svc.SetDefault(deps.device)
	project_repo.RegisterProject(deps.project)
	// 项目清单是列会话时的一张查询表；不摆内容的用例读到的是空表。
	deps.project.EXPECT().List(gomock.Any()).DoAndReturn(
		func(context.Context) ([]*project_entity.Project, error) {
			deps.projectListCalls++
			return deps.projects, nil
		}).AnyTimes()
	t.Cleanup(func() {
		agent_repo.RegisterAgent(prevAgent)
		agent_backend_repo.RegisterAgentBackend(prevBackend)
		chat_repo.RegisterSession(prevSession)
		chat_repo.RegisterMessage(prevMessage)
		remote_device_svc.SetDefault(prevDevice)
		project_repo.RegisterProject(prevProject)
		ctrl.Finish()
	})
	return deps
}

// Given desktop-owned chat rows, when an account peer asks for the session list,
// then every row keeps its actual title, Agent identity, live status, and last activity.
func TestListPeerSessions_GivenDesktopSessions_ThenReturnsCompleteNonDegradedSummaries(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	agent := &agent_entity.Agent{
		ID:             7,
		Name:           "Release captain",
		SyncMeta:       syncmeta_entity.SyncMeta{SyncID: "01HXAGENTIDENTITY0000000000"},
		AgentBackendID: 11,
		Status:         consts.ACTIVE,
	}
	deps.device.EXPECT().DeviceFingerprint().Return("sha256:desktop", nil)
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{agent}, nil)
	deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter(7, ""), 0, math.MaxInt).
		Return([]*chat_entity.Session{
			{ID: 41, AgentID: 7, Title: "Ship the release", AgentStatus: "waiting", LastMessageAt: 1710000000000, ProviderSessionID: "provider-41", Status: consts.ACTIVE},
			{ID: 42, AgentID: 7, Title: "Investigate timeout", AgentStatus: "error", LastMessageAt: 1710000001000, Status: consts.ACTIVE},
			{ID: 43, AgentID: 7, Title: "Document the release", AgentStatus: "idle", LastMessageAt: 1710000002000, Status: consts.ACTIVE},
		}, nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(&agent_backend_entity.AgentBackend{ID: 11, Type: string(agent_backend_entity.TypeClaudeCode)}, nil).AnyTimes()

	got, err := deps.svc.ListPeerSessions(ctx, "")
	require.NoError(t, err)
	require.Len(t, got.Sessions, 3)

	assert.Equal(t, wire.SessionSummary{
		SessionID:         41,
		PeerFingerprint:   "sha256:desktop",
		AgentID:           7,
		Title:             "Ship the release",
		AgentSyncID:       "01HXAGENTIDENTITY0000000000",
		ProviderSessionID: "provider-41",
		BackendType:       string(agent_backend_entity.TypeClaudeCode),
		LifecycleState:    wire.SessionLifecycleRunning,
		WaitingForInput:   true,
		LastMessageAt:     1710000000000,
	}, got.Sessions[0])
	assert.Equal(t, wire.SessionLifecycleInterrupted, got.Sessions[1].LifecycleState)
	assert.False(t, got.Sessions[1].WaitingForInput)
	assert.Equal(t, int64(1710000001000), got.Sessions[1].LastMessageAt)
	assert.Equal(t, wire.SessionLifecycleIdle, got.Sessions[2].LifecycleState)
	assert.False(t, got.Sessions[2].WaitingForInput)
	assert.Equal(t, int64(1710000002000), got.Sessions[2].LastMessageAt)

	// Guard R5: desktop rows must never become the round-A unnamed fallback.
	assert.NotEmpty(t, got.Sessions[0].Title, "title must be the stored desktop title, not a placeholder")
	assert.NotEmpty(t, got.Sessions[0].AgentSyncID, "AgentSyncID lets the peer resolve the stored name and avatar")
	assert.NotEqual(t, "Unnamed", got.Sessions[0].Title)
}

// Given a corrupt desktop row missing first-class title or Agent identity, when
// it is listed, then it is omitted instead of being fabricated into a degraded group.
func TestListPeerSessions_GivenMissingTitleOrAgentIdentity_ThenOmitsRatherThanDegrade(t *testing.T) {
	for name, tc := range map[string]struct {
		title     string
		agentSync string
	}{
		"blank title":            {title: "", agentSync: "01HXAGENTIDENTITY0000000000"},
		"missing Agent identity": {title: "Ship the release", agentSync: ""},
	} {
		t.Run(name, func(t *testing.T) {
			deps := setupPeerSessionTest(t)
			ctx := context.Background()
			deps.device.EXPECT().DeviceFingerprint().Return("sha256:desktop", nil)
			deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{{
				ID: 7, Name: "Release captain", Status: consts.ACTIVE,
				SyncMeta: syncmeta_entity.SyncMeta{SyncID: tc.agentSync},
			}}, nil)
			deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter(7, ""), 0, math.MaxInt).
				Return([]*chat_entity.Session{{ID: 41, AgentID: 7, Title: tc.title, AgentStatus: "idle", Status: consts.ACTIVE}}, nil)

			got, err := deps.svc.ListPeerSessions(ctx, "")
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Empty(t, got.Sessions, "never emit a blank or guessed fallback summary")
		})
	}
}

// Given one unusable row next to healthy ones, when an account peer lists sessions,
// then only the unusable row is dropped. A single corrupt row must not blind the peer
// to the whole machine: ListPeerSessions is the web console's only way in, and an
// error there leaves the browser with no list and no reason.
func TestListPeerSessions_GivenOneCorruptRow_ThenServesEveryHealthyRow(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	deps.device.EXPECT().DeviceFingerprint().Return("sha256:desktop", nil)
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{{
		ID: 7, Name: "Release captain", Status: consts.ACTIVE, AgentBackendID: 11,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "01HXAGENTIDENTITY0000000000"},
	}}, nil)
	deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter(7, ""), 0, math.MaxInt).
		Return([]*chat_entity.Session{
			{ID: 40, AgentID: 7, Title: "", AgentStatus: "idle", Status: consts.ACTIVE},
			{ID: 41, AgentID: 7, Title: "Ship the release", AgentStatus: "idle", Status: consts.ACTIVE},
			{ID: 42, AgentID: 7, Title: "Investigate timeout", AgentStatus: "nonsense", Status: consts.ACTIVE},
			{ID: 43, AgentID: 7, Title: "Document the release", AgentStatus: "idle", Status: consts.ACTIVE},
		}, nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).
		Return(&agent_backend_entity.AgentBackend{ID: 11, Type: string(agent_backend_entity.TypeClaudeCode)}, nil).AnyTimes()

	got, err := deps.svc.ListPeerSessions(ctx, "")
	require.NoError(t, err)
	require.Len(t, got.Sessions, 2, "an untitled row and an unknown-status row are dropped; the rest are served")
	assert.Equal(t, int64(41), got.Sessions[0].SessionID)
	assert.Equal(t, int64(43), got.Sessions[1].SessionID)
}

// Given the backend lookup fails for infrastructure reasons, when sessions are listed,
// then the call still fails. Only per-row metadata defects are skippable — swallowing a
// database error would serve a silently short list as if it were complete.
func TestListPeerSessions_GivenRepositoryFailure_ThenStillFails(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	deps.device.EXPECT().DeviceFingerprint().Return("sha256:desktop", nil)
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{{
		ID: 7, Name: "Release captain", Status: consts.ACTIVE, AgentBackendID: 11,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "01HXAGENTIDENTITY0000000000"},
	}}, nil)
	deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter(7, ""), 0, math.MaxInt).
		Return([]*chat_entity.Session{
			{ID: 41, AgentID: 7, Title: "Ship the release", AgentStatus: "idle", Status: consts.ACTIVE},
		}, nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, errors.New("database is gone"))

	got, err := deps.svc.ListPeerSessions(ctx, "")
	require.Error(t, err)
	assert.Nil(t, got)
}

type recordingPeerSubscriber struct {
	done chan struct{}
}

func (*recordingPeerSubscriber) Notify(string, any) error { return nil }
func (s *recordingPeerSubscriber) Done() <-chan struct{}  { return s.done }

// Given the desktop UI already owns a session, when a remote peer attaches,
// then it is added as an additional subscriber and is removed when its channel closes.
func TestAttachPeerSession_GivenLiveDesktopSession_ThenAddsAndCleansUpRemoteSubscriber(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	deps.session.EXPECT().Find(ctx, int64(41)).Return(&chat_entity.Session{
		ID: 41, AgentID: 7, AgentStatus: "waiting", Status: consts.ACTIVE,
	}, nil)
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(&agent_entity.Agent{ID: 7, AgentBackendID: 11, Status: consts.ACTIVE}, nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(&agent_backend_entity.AgentBackend{ID: 11, Type: string(agent_backend_entity.TypeClaudeCode)}, nil)
	deps.message.EXPECT().List(ctx, int64(41)).Return(nil, nil)

	subscriber := &recordingPeerSubscriber{done: make(chan struct{})}
	got, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{SessionID: 41}, subscriber)
	require.NoError(t, err)
	assert.Equal(t, wire.SessionAttachResult{
		SessionID: 41, BackendType: string(agent_backend_entity.TypeClaudeCode), LifecycleState: wire.SessionLifecycleRunning,
	}, got)
	assert.Equal(t, 1, deps.svc.peerSubscriberCount(41), "remote attaches alongside the desktop UI; it must not replace a local subscriber")

	close(subscriber.done)
	require.Eventually(t, func() bool {
		return deps.svc.peerSubscriberCount(41) == 0
	}, time.Second, time.Millisecond, "closing the peer channel must remove its remote presence")
}

func TestAttachPeerSession_GivenInvalidOrMissingSession_ThenRejects(t *testing.T) {
	deps := setupPeerSessionTest(t)
	subscriber := &recordingPeerSubscriber{done: make(chan struct{})}
	_, err := deps.svc.AttachPeerSession(context.Background(), wire.SessionAttachParams{}, subscriber)
	require.Error(t, err)

	deps.session.EXPECT().Find(gomock.Any(), int64(99)).Return(nil, nil)
	_, err = deps.svc.AttachPeerSession(context.Background(), wire.SessionAttachParams{SessionID: 99}, subscriber)
	assert.True(t, errors.Is(err, ErrPeerSessionNotFound))
}

// 桌面端的会话清单要把**它自己知道的项目归属**说出来。
//
// 这条对话属于哪个项目，在这台电脑上是一个明写在库里的事实（chat_sessions.project_id）。
// 但它此前没有出口：清单只交出标题 / Agent / 后端 / 生命周期，server 那边于是只剩
// 一条判法——拿 (指纹, cwd) 去比账号里 agentred 配的项目路径。桌面端在那条路上两头
// 都对不上（它没有「这条会话的 cwd」这一列可报，它的本机路径也不在那份名单里），
// 于是从这台机器保存进账号的每一条对话，在控制台项目轴上都掉进「随手对话」。
//
// 交出去的是**项目的同步标识**而不是本地自增主键：那是账号里跨机通用的那个名字，
// 也正是 server 项目树上的键。
func TestListPeerSessions_GivenSessionInAProject_ThenNamesTheProjectSyncID(t *testing.T) {
	deps := setupPeerSessionTest(t)
	deps.projects = []*project_entity.Project{{
		ID: 3, Name: "dsp2b", Status: consts.ACTIVE,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "01HXPROJECTIDENTITY000000000"},
	}}
	ctx := context.Background()
	deps.device.EXPECT().DeviceFingerprint().Return("sha256:desktop", nil)
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{{
		ID: 7, Name: "Release captain", Status: consts.ACTIVE,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "01HXAGENTIDENTITY0000000000"},
	}}, nil)
	deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter(7, ""), 0, math.MaxInt).
		Return([]*chat_entity.Session{
			{ID: 41, AgentID: 7, ProjectID: 3, Title: "Ship the release", AgentStatus: "idle", Status: consts.ACTIVE},
			{ID: 42, AgentID: 7, Title: "Free chat", AgentStatus: "idle", Status: consts.ACTIVE},
		}, nil)

	got, err := deps.svc.ListPeerSessions(ctx, "")
	require.NoError(t, err)
	require.Len(t, got.Sessions, 2)
	assert.Equal(t, "01HXPROJECTIDENTITY000000000", got.Sessions[0].ProjectSyncID)
	assert.Empty(t, got.Sessions[1].ProjectSyncID, "自由会话不属于任何项目，如实留空")
}

// 项目还没拿到同步标识（未登录时建的行，R12a 认领之前）就如实留空，不拿本地主键
// 凑一个：那个数字在账号里谁也不认识，server 会照它建出一个永远配不上真项目的幽灵组。
func TestListPeerSessions_GivenProjectWithoutSyncID_ThenLeavesItBlank(t *testing.T) {
	deps := setupPeerSessionTest(t)
	deps.projects = []*project_entity.Project{{ID: 3, Name: "dsp2b", Status: consts.ACTIVE}}
	ctx := context.Background()
	deps.device.EXPECT().DeviceFingerprint().Return("sha256:desktop", nil)
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{{
		ID: 7, Name: "Release captain", Status: consts.ACTIVE,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "01HXAGENTIDENTITY0000000000"},
	}}, nil)
	deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter(7, ""), 0, math.MaxInt).
		Return([]*chat_entity.Session{
			{ID: 41, AgentID: 7, ProjectID: 3, Title: "Ship the release", AgentStatus: "idle", Status: consts.ACTIVE},
		}, nil)

	got, err := deps.svc.ListPeerSessions(ctx, "")
	require.NoError(t, err)
	require.Len(t, got.Sessions, 1)
	assert.Empty(t, got.Sessions[0].ProjectSyncID)
}

// 项目清单只取一遍：一台电脑上几十个 Agent、几百条会话，逐条回库查一次项目就是
// 几百次往返，而这份清单在一次列举里不会变。
func TestListPeerSessions_ReadsTheProjectListOnce(t *testing.T) {
	deps := setupPeerSessionTest(t)
	deps.projects = []*project_entity.Project{{
		ID: 3, Name: "dsp2b", Status: consts.ACTIVE,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "01HXPROJECTIDENTITY000000000"},
	}}
	ctx := context.Background()
	deps.device.EXPECT().DeviceFingerprint().Return("sha256:desktop", nil)
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{
		{ID: 7, Name: "A", Status: consts.ACTIVE, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "sync-a"}},
		{ID: 8, Name: "B", Status: consts.ACTIVE, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "sync-b"}},
	}, nil)
	deps.session.EXPECT().ListIndexPaged(ctx, gomock.Any(), 0, math.MaxInt).
		DoAndReturn(func(_ context.Context, filter chat_repo.SessionIndexFilter, _, _ int) ([]*chat_entity.Session, error) {
			agentID := *filter.AgentID
			return []*chat_entity.Session{
				{ID: agentID * 10, AgentID: agentID, ProjectID: 3, Title: "t", AgentStatus: "idle", Status: consts.ACTIVE},
			}, nil
		}).Times(2)

	got, err := deps.svc.ListPeerSessions(ctx, "")
	require.NoError(t, err)
	require.Len(t, got.Sessions, 2)
	assert.Equal(t, 1, deps.projectListCalls, "几百条会话逐条回库查项目就是几百次往返")
}

// Given 桌面端的会话行上钉了会话级模型目标，When 账号对端要会话清单，
// Then 那两格原样报出来，并声明这台机器认识它们。
//
// 三态与 chat_sessions.provider_key/model_key 逐字同义（chat_entity/session.go）：
// 两者皆空 = 跟随 Agent 绑定、provider 非空 + model 空 = 供应商默认、两者非空 = 固定
// 模型。空**有含义**，所以「这台机器认不认识这两格」必须另外声明，不能靠空推断。
func TestListPeerSessions_GivenSessionModelTarget_ThenReportsItAndDeclaresSupport(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	agent := &agent_entity.Agent{
		ID:             7,
		Name:           "Release captain",
		SyncMeta:       syncmeta_entity.SyncMeta{SyncID: "01HXAGENTIDENTITY0000000000"},
		AgentBackendID: 11,
		Status:         consts.ACTIVE,
	}
	deps.device.EXPECT().DeviceFingerprint().Return("sha256:desktop", nil)
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{agent}, nil)
	deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter(7, ""), 0, math.MaxInt).
		Return([]*chat_entity.Session{
			{ID: 41, AgentID: 7, Title: "Fixed model", AgentStatus: "idle", Status: consts.ACTIVE,
				ProviderKey: "prov-anthropic", ModelKey: "sonnet-4-6"},
			{ID: 42, AgentID: 7, Title: "Provider default", AgentStatus: "idle", Status: consts.ACTIVE,
				ProviderKey: "prov-anthropic"},
			{ID: 43, AgentID: 7, Title: "Follows the agent", AgentStatus: "idle", Status: consts.ACTIVE},
		}, nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).
		Return(&agent_backend_entity.AgentBackend{ID: 11, Type: string(agent_backend_entity.TypeClaudeCode)}, nil).AnyTimes()

	got, err := deps.svc.ListPeerSessions(ctx, "")
	require.NoError(t, err)
	require.Len(t, got.Sessions, 3)

	assert.Equal(t, "prov-anthropic", got.Sessions[0].ProviderKey)
	assert.Equal(t, "sonnet-4-6", got.Sessions[0].ModelKey)

	assert.Equal(t, "prov-anthropic", got.Sessions[1].ProviderKey)
	assert.Empty(t, got.Sessions[1].ModelKey, "供应商默认：模型这一格就该是空的")

	assert.Empty(t, got.Sessions[2].ProviderKey, "跟随 Agent 绑定：两格都空")
	assert.Empty(t, got.Sessions[2].ModelKey)

}

// peerListFilter 是 ListPeerSessions 每个 agent 那一问用的 filter。
func peerListFilter(agentID int64, keyword string) chat_repo.SessionIndexFilter {
	return chat_repo.SessionIndexFilter{AgentID: &agentID, Keyword: keyword}
}

// ── 清单的关键词收窄 ────────────────────────────────────────────────────────
//
// 桌面端是 session.list 的服务方之一(浏览器的机器轴打到它)。此前它把库里每个 agent
// 的**全部**会话整份投影出去 —— 这台机器 3500 条会话就是 3500 份摘要过线。关键词
// 因此下推到查询,而不是取回来再筛:后者省的只是带宽,库还是白读一遍。
func TestListPeerSessions_NarrowsByKeyword(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	agent := &agent_entity.Agent{
		ID:             7,
		Name:           "Release captain",
		SyncMeta:       syncmeta_entity.SyncMeta{SyncID: "01HXAGENTIDENTITY0000000000"},
		AgentBackendID: 11,
		Status:         consts.ACTIVE,
	}
	deps.device.EXPECT().DeviceFingerprint().Return("sha256:desktop", nil)
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{agent}, nil)
	deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter(7, "happy"), 0, math.MaxInt).
		Return([]*chat_entity.Session{
			{ID: 41, AgentID: 7, Title: "看看happy是怎么实现中继的", AgentStatus: "idle", Status: consts.ACTIVE},
		}, nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(&agent_backend_entity.AgentBackend{ID: 11, Type: string(agent_backend_entity.TypeClaudeCode)}, nil).AnyTimes()

	got, err := deps.svc.ListPeerSessions(ctx, "happy")
	require.NoError(t, err)
	require.Len(t, got.Sessions, 1)
	assert.Equal(t, int64(41), got.Sessions[0].SessionID)
}

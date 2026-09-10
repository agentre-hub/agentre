package chat_svc

import (
	"context"
	"errors"
	"math"
	"strconv"
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
	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
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
	// 会话摘要上的 peer_fingerprint 报的是这台桌面端自己。
	deps.device.EXPECT().DeviceFingerprint().Return(testDesktopFingerprint, nil).AnyTimes()
	// conversation_id → 本地主键的反查是 conversation_id 唯一索引上的一次查询
	// (见 ResolvePeerConversation)。41/42/43 是这些用例里本机有的三条会话。
	deps.session.EXPECT().FindByConversationID(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, conversationID string) (*chat_entity.Session, error) {
			for _, id := range []int64{41, 42, 43} {
				if conversationID == convID(id) {
					return &chat_entity.Session{ID: id, ConversationID: conversationID, Status: consts.ACTIVE}, nil
				}
			}
			return nil, nil
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
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{agent}, nil)
	deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter(""), 0, math.MaxInt).
		Return([]*chat_entity.Session{
			{ID: 41, ConversationID: convID(41), AgentID: 7, Title: "Ship the release", AgentStatus: "waiting", LastMessageAt: 1710000000000, ProviderSessionID: "provider-41", Status: consts.ACTIVE},
			{ID: 42, ConversationID: convID(42), AgentID: 7, Title: "Investigate timeout", AgentStatus: "error", LastMessageAt: 1710000001000, Status: consts.ACTIVE},
			{ID: 43, ConversationID: convID(43), AgentID: 7, Title: "Document the release", AgentStatus: "idle", LastMessageAt: 1710000002000, Status: consts.ACTIVE},
		}, nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(&agent_backend_entity.AgentBackend{ID: 11, Type: string(agent_backend_entity.TypeClaudeCode)}, nil).AnyTimes()

	got, err := deps.svc.ListPeerSessions(ctx, wire.SessionListParams{})
	require.NoError(t, err)
	require.Len(t, got.Sessions, 3)

	assert.Equal(t, wire.SessionSummary{
		ConversationID:    convID(41),
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
	// 桌面端的 AgentStatus="error" 过线是 failed,**不是** interrupted。
	// interrupted 是自锁终态,消费方对它的既定纪律是不去 attach —— 拿它表达
	// 「上一轮跑挂了」就等于让一条报错收场的对话再也接不上实时流。
	assert.Equal(t, wire.SessionLifecycleFailed, got.Sessions[1].LifecycleState)
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
			deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{{
				ID: 7, Name: "Release captain", Status: consts.ACTIVE,
				SyncMeta: syncmeta_entity.SyncMeta{SyncID: tc.agentSync},
			}}, nil)
			deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter(""), 0, math.MaxInt).
				Return([]*chat_entity.Session{{ID: 41, ConversationID: convID(41), AgentID: 7, Title: tc.title, AgentStatus: "idle", Status: consts.ACTIVE}}, nil)

			got, err := deps.svc.ListPeerSessions(ctx, wire.SessionListParams{})
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
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{{
		ID: 7, Name: "Release captain", Status: consts.ACTIVE, AgentBackendID: 11,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "01HXAGENTIDENTITY0000000000"},
	}}, nil)
	deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter(""), 0, math.MaxInt).
		Return([]*chat_entity.Session{
			{ID: 40, ConversationID: convID(40), AgentID: 7, Title: "", AgentStatus: "idle", Status: consts.ACTIVE},
			{ID: 41, ConversationID: convID(41), AgentID: 7, Title: "Ship the release", AgentStatus: "idle", Status: consts.ACTIVE},
			{ID: 42, ConversationID: convID(42), AgentID: 7, Title: "Investigate timeout", AgentStatus: "nonsense", Status: consts.ACTIVE},
			{ID: 43, ConversationID: convID(43), AgentID: 7, Title: "Document the release", AgentStatus: "idle", Status: consts.ACTIVE},
		}, nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).
		Return(&agent_backend_entity.AgentBackend{ID: 11, Type: string(agent_backend_entity.TypeClaudeCode)}, nil).AnyTimes()

	got, err := deps.svc.ListPeerSessions(ctx, wire.SessionListParams{})
	require.NoError(t, err)
	require.Len(t, got.Sessions, 2, "an untitled row and an unknown-status row are dropped; the rest are served")
	assert.Equal(t, convID(41), got.Sessions[0].ConversationID)
	assert.Equal(t, convID(43), got.Sessions[1].ConversationID)
}

// Given the backend lookup fails for infrastructure reasons, when sessions are listed,
// then the call still fails. Only per-row metadata defects are skippable — swallowing a
// database error would serve a silently short list as if it were complete.
func TestListPeerSessions_GivenRepositoryFailure_ThenStillFails(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{{
		ID: 7, Name: "Release captain", Status: consts.ACTIVE, AgentBackendID: 11,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "01HXAGENTIDENTITY0000000000"},
	}}, nil)
	deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter(""), 0, math.MaxInt).
		Return([]*chat_entity.Session{
			{ID: 41, ConversationID: convID(41), AgentID: 7, Title: "Ship the release", AgentStatus: "idle", Status: consts.ACTIVE},
		}, nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, errors.New("database is gone"))

	got, err := deps.svc.ListPeerSessions(ctx, wire.SessionListParams{})
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
	got, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, subscriber)
	require.NoError(t, err)
	assert.Equal(t, wire.SessionAttachResult{
		ConversationID: convID(41), BackendType: string(agent_backend_entity.TypeClaudeCode), LifecycleState: wire.SessionLifecycleRunning,
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

	// 本机没有这条对话:反查枚举完本机会话仍然对不上,在打库之前就报"不在这台机器上"。
	_, err = deps.svc.AttachPeerSession(context.Background(), wire.SessionAttachParams{ConversationID: convID(99)}, subscriber)
	assert.True(t, errors.Is(err, ErrPeerSessionNotFound))

	// 不成其为对话身份的取值(空、旧的裸数字会话号)与"不在这台机器上"分开报。
	_, err = deps.svc.AttachPeerSession(context.Background(), wire.SessionAttachParams{ConversationID: "99"}, subscriber)
	assert.True(t, errors.Is(err, ErrPeerSessionInvalidID))
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
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{{
		ID: 7, Name: "Release captain", Status: consts.ACTIVE,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "01HXAGENTIDENTITY0000000000"},
	}}, nil)
	deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter(""), 0, math.MaxInt).
		Return([]*chat_entity.Session{
			{ID: 41, ConversationID: convID(41), AgentID: 7, ProjectID: 3, Title: "Ship the release", AgentStatus: "idle", Status: consts.ACTIVE},
			{ID: 42, ConversationID: convID(42), AgentID: 7, Title: "Free chat", AgentStatus: "idle", Status: consts.ACTIVE},
		}, nil)

	got, err := deps.svc.ListPeerSessions(ctx, wire.SessionListParams{})
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
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{{
		ID: 7, Name: "Release captain", Status: consts.ACTIVE,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "01HXAGENTIDENTITY0000000000"},
	}}, nil)
	deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter(""), 0, math.MaxInt).
		Return([]*chat_entity.Session{
			{ID: 41, ConversationID: convID(41), AgentID: 7, ProjectID: 3, Title: "Ship the release", AgentStatus: "idle", Status: consts.ACTIVE},
		}, nil)

	got, err := deps.svc.ListPeerSessions(ctx, wire.SessionListParams{})
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
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{
		{ID: 7, Name: "A", Status: consts.ACTIVE, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "sync-a"}},
		{ID: 8, Name: "B", Status: consts.ACTIVE, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "sync-b"}},
	}, nil)
	deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter(""), 0, math.MaxInt).
		Return([]*chat_entity.Session{
			{ID: 70, ConversationID: convID(70), AgentID: 7, ProjectID: 3, Title: "t", AgentStatus: "idle", Status: consts.ACTIVE},
			{ID: 80, ConversationID: convID(80), AgentID: 8, ProjectID: 3, Title: "t", AgentStatus: "idle", Status: consts.ACTIVE},
		}, nil)

	got, err := deps.svc.ListPeerSessions(ctx, wire.SessionListParams{})
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
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{agent}, nil)
	deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter(""), 0, math.MaxInt).
		Return([]*chat_entity.Session{
			{ID: 41, ConversationID: convID(41), AgentID: 7, Title: "Fixed model", AgentStatus: "idle", Status: consts.ACTIVE,
				ProviderKey: "prov-anthropic", ModelKey: "sonnet-4-6"},
			{ID: 42, ConversationID: convID(42), AgentID: 7, Title: "Provider default", AgentStatus: "idle", Status: consts.ACTIVE,
				ProviderKey: "prov-anthropic"},
			{ID: 43, ConversationID: convID(43), AgentID: 7, Title: "Follows the agent", AgentStatus: "idle", Status: consts.ACTIVE},
		}, nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).
		Return(&agent_backend_entity.AgentBackend{ID: 11, Type: string(agent_backend_entity.TypeClaudeCode)}, nil).AnyTimes()

	got, err := deps.svc.ListPeerSessions(ctx, wire.SessionListParams{})
	require.NoError(t, err)
	require.Len(t, got.Sessions, 3)

	assert.Equal(t, "prov-anthropic", got.Sessions[0].ProviderKey)
	assert.Equal(t, "sonnet-4-6", got.Sessions[0].ModelKey)

	assert.Equal(t, "prov-anthropic", got.Sessions[1].ProviderKey)
	assert.Empty(t, got.Sessions[1].ModelKey, "供应商默认：模型这一格就该是空的")

	assert.Empty(t, got.Sessions[2].ProviderKey, "跟随 Agent 绑定：两格都空")
	assert.Empty(t, got.Sessions[2].ModelKey)

}

// Given 桌面端的会话行上钉了思考力度，When 账号对端要会话清单，Then 那一格照样报出来。
//
// 与上面两格同一形态的显示镜像（spec 2026-09-01 决策 1）：空**有含义** —— 跟随该会话
// 那一档 backend 的配置，所以留空而不是补一个档位。
func TestListPeerSessions_GivenSessionReasoningEffort_ThenReportsIt(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	agent := &agent_entity.Agent{
		ID:             7,
		Name:           "Release captain",
		SyncMeta:       syncmeta_entity.SyncMeta{SyncID: "01HXAGENTIDENTITY0000000000"},
		AgentBackendID: 11,
		Status:         consts.ACTIVE,
	}
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{agent}, nil)
	deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter(""), 0, math.MaxInt).
		Return([]*chat_entity.Session{
			{ID: 41, ConversationID: convID(41), AgentID: 7, Title: "Pinned effort", AgentStatus: "idle", Status: consts.ACTIVE,
				ReasoningEffort: "xhigh"},
			{ID: 42, ConversationID: convID(42), AgentID: 7, Title: "Follows the backend", AgentStatus: "idle", Status: consts.ACTIVE},
		}, nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).
		Return(&agent_backend_entity.AgentBackend{ID: 11, Type: string(agent_backend_entity.TypeCodex)}, nil).AnyTimes()

	got, err := deps.svc.ListPeerSessions(ctx, wire.SessionListParams{})
	require.NoError(t, err)
	require.Len(t, got.Sessions, 2)
	assert.Equal(t, "xhigh", got.Sessions[0].ReasoningEffort)
	assert.Empty(t, got.Sessions[1].ReasoningEffort, "跟随后端配置：这一格就该是空的")
}

// peerListFilter 是 ListPeerSessions 每个 agent 那一问用的 filter。
func peerListFilter(keyword string) chat_repo.SessionIndexFilter {
	return chat_repo.SessionIndexFilter{Keyword: keyword}
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
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{agent}, nil)
	deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter("happy"), 0, math.MaxInt).
		Return([]*chat_entity.Session{
			{ID: 41, ConversationID: convID(41), AgentID: 7, Title: "看看happy是怎么实现中继的", AgentStatus: "idle", Status: consts.ACTIVE},
		}, nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(&agent_backend_entity.AgentBackend{ID: 11, Type: string(agent_backend_entity.TypeClaudeCode)}, nil).AnyTimes()

	got, err := deps.svc.ListPeerSessions(ctx, wire.SessionListParams{Keyword: "happy"})
	require.NoError(t, err)
	require.Len(t, got.Sessions, 1)
	assert.Equal(t, convID(41), got.Sessions[0].ConversationID)
}

// ── 清单的分页 ──────────────────────────────────────────────────────────────
//
// 关键词只帮到「用户正在搜」的那一刻。不搜索时机器轴照旧要把这台机器的每一条会话
// 都投影出去 —— 3500 条会话就是 3500 份摘要过线,浏览器再一次性画出来。页因此也要
// 下推到查询:一次只取一页,总数单独数。

// Given 桌面端上会话很多，When 账号对端只要一页，Then 只查那一页并把总数一并说出来。
func TestListPeerSessions_PagesAndReportsTheTotal(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	agent := &agent_entity.Agent{
		ID: 7, Name: "Release captain", AgentBackendID: 11, Status: consts.ACTIVE,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "01HXAGENTIDENTITY0000000000"},
	}
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{agent}, nil)
	deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter(""), 0, 2).
		Return([]*chat_entity.Session{
			{ID: 41, ConversationID: convID(41), AgentID: 7, Title: "one", AgentStatus: "idle", Status: consts.ACTIVE},
			{ID: 42, ConversationID: convID(42), AgentID: 7, Title: "two", AgentStatus: "idle", Status: consts.ACTIVE},
		}, nil)
	deps.session.EXPECT().CountIndex(ctx, peerListFilter("")).Return(int64(44), nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(&agent_backend_entity.AgentBackend{ID: 11, Type: string(agent_backend_entity.TypeClaudeCode)}, nil).AnyTimes()

	got, err := deps.svc.ListPeerSessions(ctx, wire.SessionListParams{Limit: 2})
	require.NoError(t, err)
	require.Len(t, got.Sessions, 2)
	assert.Equal(t, int64(44), got.Total)
	assert.True(t, got.HasMore)
	assert.Equal(t, wire.EncodeSessionListCursor(2), got.Cursor)
}

// Given 上一页给了游标，When 对端拿它接着翻，Then 偏移量下推到查询而不是取回整份再切。
func TestListPeerSessions_ContinuesFromTheCursor(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	agent := &agent_entity.Agent{
		ID: 7, Name: "Release captain", AgentBackendID: 11, Status: consts.ACTIVE,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "01HXAGENTIDENTITY0000000000"},
	}
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{agent}, nil)
	deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter(""), 2, 2).
		Return([]*chat_entity.Session{
			{ID: 43, ConversationID: convID(43), AgentID: 7, Title: "three", AgentStatus: "idle", Status: consts.ACTIVE},
		}, nil)
	deps.session.EXPECT().CountIndex(ctx, peerListFilter("")).Return(int64(3), nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(&agent_backend_entity.AgentBackend{ID: 11, Type: string(agent_backend_entity.TypeClaudeCode)}, nil).AnyTimes()

	got, err := deps.svc.ListPeerSessions(ctx, wire.SessionListParams{
		Limit:  2,
		Cursor: wire.EncodeSessionListCursor(2),
	})
	require.NoError(t, err)
	require.Len(t, got.Sessions, 1)
	assert.False(t, got.HasMore)
	assert.Empty(t, got.Cursor, "最后一页不该再给游标")
}

// Given 老客户端不带 limit，When 它要清单，Then 照旧拿整份，且不为总数多查一次库。
func TestListPeerSessions_UnpagedStillAnswersEverything(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	agent := &agent_entity.Agent{
		ID: 7, Name: "Release captain", AgentBackendID: 11, Status: consts.ACTIVE,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "01HXAGENTIDENTITY0000000000"},
	}
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{agent}, nil)
	deps.session.EXPECT().ListIndexPaged(ctx, peerListFilter(""), 0, math.MaxInt).
		Return([]*chat_entity.Session{
			{ID: 41, ConversationID: convID(41), AgentID: 7, Title: "one", AgentStatus: "idle", Status: consts.ACTIVE},
		}, nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(&agent_backend_entity.AgentBackend{ID: 11, Type: string(agent_backend_entity.TypeClaudeCode)}, nil).AnyTimes()

	got, err := deps.svc.ListPeerSessions(ctx, wire.SessionListParams{})
	require.NoError(t, err)
	require.Len(t, got.Sessions, 1)
	assert.Equal(t, int64(1), got.Total)
	assert.False(t, got.HasMore)
}

// Given 游标是坏的，When 对端拿它翻页，Then 当场报错而不是从头开始。
func TestListPeerSessions_RejectsABadCursor(t *testing.T) {
	deps := setupPeerSessionTest(t)

	_, err := deps.svc.ListPeerSessions(context.Background(), wire.SessionListParams{Limit: 2, Cursor: "nonsense"})
	assert.Error(t, err)
}

// ── 按对话身份收窄 ──────────────────────────────────────────────────────────
//
// 调用方已经知道自己要哪几条时(详情页要一条摘要、账号要它保存过的那些),此前只能
// 把整台机器的清单翻一遍去找 —— 详情页每跑完一轮就翻一次。

// Given 调用方点名了几条对话，When 它要清单，Then 查询按这几条收窄，不翻整份。
func TestListPeerSessions_NarrowsByConversationIDs(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	agent := &agent_entity.Agent{
		ID: 7, Name: "Release captain", AgentBackendID: 11, Status: consts.ACTIVE,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "01HXAGENTIDENTITY0000000000"},
	}
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{agent}, nil)
	filter := peerListFilter("")
	filter.ConversationIDs = []string{convID(41)}
	deps.session.EXPECT().ListIndexPaged(ctx, filter, 0, math.MaxInt).
		Return([]*chat_entity.Session{
			{ID: 41, ConversationID: convID(41), AgentID: 7, Title: "one", AgentStatus: "idle", Status: consts.ACTIVE},
		}, nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(&agent_backend_entity.AgentBackend{ID: 11, Type: string(agent_backend_entity.TypeClaudeCode)}, nil).AnyTimes()

	got, err := deps.svc.ListPeerSessions(ctx, wire.SessionListParams{
		ConversationIDs: []string{convID(41)},
	})
	require.NoError(t, err)
	require.Len(t, got.Sessions, 1)
	assert.Equal(t, convID(41), got.Sessions[0].ConversationID)
}

// Given 点名的条数超过上限，When 它要清单，Then 当场报错而不是悄悄少给几条 ——
// 少给的那条在调用方那里读起来是「这条对话不在这台机器上了」。
func TestListPeerSessions_RejectsTooManyConversationIDs(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ids := make([]string, wire.SessionListMaxIDs+1)
	for i := range ids {
		ids[i] = convID(int64(i + 1))
	}

	_, err := deps.svc.ListPeerSessions(context.Background(), wire.SessionListParams{ConversationIDs: ids})
	assert.Error(t, err)
}

// ── 会话计数 ────────────────────────────────────────────────────────────────

// Given 账号对端只想知道这台机器忙不忙，When 它问计数，Then 三个数各走一条 COUNT，
// 一条会话摘要都不投影 —— 设备卡片此前是把整份清单拉过去自己数的。
func TestCountPeerSessions_AnswersWithCountsNotAList(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	deps.session.EXPECT().CountIndex(ctx, peerListFilter("")).Return(int64(3500), nil)
	deps.session.EXPECT().CountActive(ctx, []string{"running"}).Return(int64(2), nil)
	deps.session.EXPECT().CountActive(ctx, []string{"waiting"}).Return(int64(1), nil)

	got, err := deps.svc.CountPeerSessions(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3500), got.Total)
	assert.Equal(t, int64(2), got.Running)
	assert.Equal(t, int64(1), got.Waiting)
}

// convID 是这些用例里本机第 n 条会话**落库的那个** conversation_id。取值形态无所谓
// (库里存什么就是什么),这里沿用一个确定性派生,只是为了让「同一条会话」在用例的
// 多处写出同一个字面值。
const testDesktopFingerprint = "sha256:desktop"

func convID(n int64) string {
	return conversationid.Derive(conversationid.Namespace, testDesktopFingerprint, strconv.FormatInt(n, 10))
}

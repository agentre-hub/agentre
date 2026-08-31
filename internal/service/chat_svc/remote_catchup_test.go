package chat_svc

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/protorpctest"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

// scriptedDaemonClient 是一条可脚本化的 daemon 连接:按 method 应答,并记下调用顺序。
type scriptedDaemonClient struct {
	mu     sync.Mutex
	calls  []string
	params map[string][]any
	script func(method string, params, result any) error
}

func newScriptedDaemonClient(script func(method string, params, result any) error) *scriptedDaemonClient {
	return &scriptedDaemonClient{params: map[string][]any{}, script: script}
}

func (c *scriptedDaemonClient) Call(_ context.Context, method string, params, result any) error {
	c.mu.Lock()
	c.calls = append(c.calls, method)
	c.params[method] = append(c.params[method], params)
	c.mu.Unlock()
	return c.script(method, params, result)
}

func (*scriptedDaemonClient) Notify(_ string, _ any) error { return nil }
func (*scriptedDaemonClient) Handle(_ string, _ func(context.Context, json.RawMessage) (any, error)) {
}
func (*scriptedDaemonClient) Closed() <-chan struct{} { return nil }
func (*scriptedDaemonClient) Close() error            { return nil }

func (c *scriptedDaemonClient) countOf(method string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.params[method])
}

func (c *scriptedDaemonClient) attachedSessions() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.params[wire.MethodSessionAttach]))
	for _, p := range c.params[wire.MethodSessionAttach] {
		out = append(out, p.(wire.SessionAttachParams).ConversationID)
	}
	return out
}

// Given 桌面 App 退出后重开,库里两条会话记着跑在同一台配对 daemon 上(一条在这段
// 时间里又产生了内容,一条没有),When 启动补齐,Then 按 exec_device_id 连回那台
// daemon,先取会话清单与状态,再只对真正落下内容的那条走 attach → pull → 待决策清单。
//
// exec_device_id 在此之前是一列只写数据:写进去、再没人读。桌面端因此在重启后不知道
// 「该连谁」,补齐三步无从发起 —— 用户故事「退出桌面 App 后下次打开看到这段时间发生
// 的全部内容」直接不成立(catchUpAll 只遍历本进程在飞的轮次,而刚启动时它必然是空的)。
func TestCatchUpRemoteSessions_ConnectsByExecDeviceAndRunsThreeSteps(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	const (
		deviceID int64 = 7
		behind   int64 = 100 // 游标 17,daemon 上已到 20
		caughtUp int64 = 101 // 游标 5,daemon 上也是 5
	)
	const fp = "sha256:beef"

	client := newScriptedDaemonClient(func(method string, params, result any) error {
		switch method {
		case wire.MethodSessionList:
			*(result.(*wire.SessionListResult)) = wire.SessionListResult{Sessions: []wire.SessionSummary{
				{ConversationID: execConvID(behind), LifecycleState: wire.SessionLifecycleRunning, LatestSeq: 20},
				{ConversationID: execConvID(caughtUp), LifecycleState: wire.SessionLifecycleIdle, LatestSeq: 5},
			}}
		case wire.MethodSessionAttach:
			p := params.(wire.SessionAttachParams)
			*(result.(*wire.SessionAttachResult)) = wire.SessionAttachResult{
				ConversationID: p.ConversationID, LifecycleState: wire.SessionLifecycleRunning, LatestSeq: 20,
			}
		case wire.MethodSessionPull:
			p := params.(wire.SessionPullParams)
			*(result.(*wire.SessionPullResult)) = wire.SessionPullResult{Cursor: p.Cursor}
		case wire.MethodSessionPendingWaiters:
			*(result.(*wire.SessionPendingWaitersResult)) = wire.SessionPendingWaitersResult{}
		}
		return nil
	})

	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	lease := mock_remote_device_svc.NewMockLease(ctrl)
	lease.EXPECT().Client().Return(protorpctest.WrapConnection(client)).AnyTimes()
	lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	lease.EXPECT().Release().AnyTimes()
	pool.EXPECT().Borrow(gomock.Any(), deviceID).Return(lease, nil).Times(1)

	rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	rds.EXPECT().Get(gomock.Any(), deviceID).
		Return(&remote_device_svc.DeviceView{ID: deviceID, DaemonFingerprint: fp}, nil).AnyTimes()
	// 补齐一路都要探这台 daemon 认不认补齐族 RPC(R18):这台认得,于是设备状态上
	// 那条「版本过旧」被撤下来。
	prevSvc := remote_device_svc.Default()
	remote_device_svc.SetDefault(rds)
	t.Cleanup(func() { remote_device_svc.SetDefault(prevSvc) })

	rows := []*chat_entity.Session{
		{ID: behind, ConversationID: execConvID(behind), AgentID: 9, Status: consts.ACTIVE, ExecDeviceID: deviceID, ExecDeviceFingerprint: fp, EventCursor: 17},
		{ID: caughtUp, ConversationID: execConvID(caughtUp), AgentID: 9, Status: consts.ACTIVE, ExecDeviceID: deviceID, ExecDeviceFingerprint: fp, EventCursor: 5},
	}
	sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
	sessRepo.EXPECT().ListRemoteExecSessions(gomock.Any()).Return(rows, nil)
	// 游标端口经 chat_sessions 读写(session_cursor.go),补齐一路都会问到它。
	for _, row := range rows {
		sessRepo.EXPECT().Find(gomock.Any(), row.ID).Return(row, nil).AnyTimes()
	}
	sessRepo.EXPECT().UpdateEventCursor(gomock.Any(), gomock.Any(), fp, gomock.Any()).Return(nil).AnyTimes()
	// daemon 说 caughtUp 那条早就空闲了,本地那行的 running 是上一个进程留下的遗孤:
	// 它没有任何内容可重放,所以除了这一笔没有别的东西会改写它。behind 那条 daemon 说
	// 还在跑,一行都不能碰。
	sessRepo.EXPECT().ResetActiveSessionsByIDs(gomock.Any(), []int64{caughtUp}).Return(int64(0), nil)

	agtRepo := mock_agent_repo.NewMockAgentRepo(ctrl)
	agtRepo.EXPECT().Find(gomock.Any(), int64(9)).
		Return(&agent_entity.Agent{ID: 9, AgentBackendID: 3}, nil).AnyTimes()
	beRepo := mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl)
	beRepo.EXPECT().Find(gomock.Any(), int64(3)).Return(&agent_backend_entity.AgentBackend{
		ID: 3, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "7",
	}, nil).AnyTimes()

	prevSess, prevAgent, prevBE := chat_repo.Session(), agent_repo.Agent(), agent_backend_repo.AgentBackend()
	chat_repo.RegisterSession(sessRepo)
	agent_repo.RegisterAgent(agtRepo)
	agent_backend_repo.RegisterAgentBackend(beRepo)
	t.Cleanup(func() {
		chat_repo.RegisterSession(prevSess)
		agent_repo.RegisterAgent(prevAgent)
		agent_backend_repo.RegisterAgentBackend(prevBE)
	})

	svc := NewChat(NoopEmitter{}).(*chatSvc)
	svc.setConnPoolForTest(pool)

	require.NoError(t, svc.CatchUpRemoteSessions(context.Background()))

	assert.Equal(t, 1, client.countOf(wire.MethodSessionList),
		"会话清单是补齐的第一步,每台 daemon 问一次")
	assert.Equal(t, []string{execConvID(behind)}, client.attachedSessions(),
		"只有真正落下内容的那条才发 attach —— 清单交回的 latestSeq 就是用来分辨这件事的")
	assert.Equal(t, 1, client.countOf(wire.MethodSessionPull))
	assert.Equal(t, 1, client.countOf(wire.MethodSessionPendingWaiters),
		"待决策清单是第三步:断连前就阻塞、宣告事件早在游标之前的那些只能靠它找回来")

	// 补齐重放出来的内容以「没有 user 行的一轮」交付,消费方必须在补齐**之前**装好:
	// 没人 drain 会把通知读循环顶住,内容也永远进不了转录。
	_, watching := svc.autoWatchers.Load(behind)
	assert.True(t, watching, "补齐的会话必须先接上轮次消费方,重放内容才有落点")
}

// Given App 重启后,库里两条会话都停在 running:一条 daemon 说还在跑(且这段时间一个字
// 都没产出),另一条 daemon 说早就空闲了,When 启动补齐,
// Then 只有 daemon 不认它在跑的那条被收尾成 error,还在跑的那条原样留着。
//
// 启动期的 blanket 清理(bootstrap.ResetStaleActiveSessions)已经不碰远端会话了 ——
// 在连上 daemon 之前无从判断,一律翻 error 就是假失败。判据只能来自 daemon 交回的
// 生命周期,而它恰好就在补齐的第一步(会话清单)里。有内容可重放的会话由重放自己写终态;
// 一个字都没产出的会话没有任何东西会改写它,所以这一步是它唯一的判定。
func TestCatchUpRemoteSessions_OnlyFailsSessionsTheDaemonIsNotRunning(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	const (
		deviceID int64 = 7
		stillRun int64 = 100 // daemon 说还在跑:不能判失败
		longDone int64 = 101 // daemon 说空闲且已追平:本地那条 running 是重启遗孤
	)
	const fp = "sha256:beef"

	client := newScriptedDaemonClient(func(method string, params, result any) error {
		switch method {
		case wire.MethodSessionList:
			*(result.(*wire.SessionListResult)) = wire.SessionListResult{Sessions: []wire.SessionSummary{
				{ConversationID: execConvID(stillRun), LifecycleState: wire.SessionLifecycleRunning, LatestSeq: 5},
				{ConversationID: execConvID(longDone), LifecycleState: wire.SessionLifecycleIdle, LatestSeq: 5},
			}}
		case wire.MethodSessionAttach:
			p := params.(wire.SessionAttachParams)
			*(result.(*wire.SessionAttachResult)) = wire.SessionAttachResult{
				ConversationID: p.ConversationID, LifecycleState: wire.SessionLifecycleRunning, LatestSeq: 5,
			}
		case wire.MethodSessionPull:
			p := params.(wire.SessionPullParams)
			*(result.(*wire.SessionPullResult)) = wire.SessionPullResult{Cursor: p.Cursor, OldestSeq: 1}
		case wire.MethodSessionPendingWaiters:
			*(result.(*wire.SessionPendingWaitersResult)) = wire.SessionPendingWaitersResult{}
		}
		return nil
	})

	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	lease := mock_remote_device_svc.NewMockLease(ctrl)
	lease.EXPECT().Client().Return(protorpctest.WrapConnection(client)).AnyTimes()
	lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	lease.EXPECT().Release().AnyTimes()
	pool.EXPECT().Borrow(gomock.Any(), deviceID).Return(lease, nil).Times(1)

	rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	rds.EXPECT().Get(gomock.Any(), deviceID).
		Return(&remote_device_svc.DeviceView{ID: deviceID, DaemonFingerprint: fp}, nil).AnyTimes()
	prevSvc := remote_device_svc.Default()
	remote_device_svc.SetDefault(rds)
	t.Cleanup(func() { remote_device_svc.SetDefault(prevSvc) })

	rows := []*chat_entity.Session{
		{ID: stillRun, ConversationID: execConvID(stillRun), AgentID: 9, AgentStatus: "running", Status: consts.ACTIVE,
			ExecDeviceID: deviceID, ExecDeviceFingerprint: fp, EventCursor: 5},
		{ID: longDone, ConversationID: execConvID(longDone), AgentID: 9, AgentStatus: "running", Status: consts.ACTIVE,
			ExecDeviceID: deviceID, ExecDeviceFingerprint: fp, EventCursor: 5},
	}
	sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
	sessRepo.EXPECT().ListRemoteExecSessions(gomock.Any()).Return(rows, nil)
	for _, row := range rows {
		sessRepo.EXPECT().Find(gomock.Any(), row.ID).Return(row, nil).AnyTimes()
	}
	sessRepo.EXPECT().UpdateEventCursor(gomock.Any(), gomock.Any(), fp, gomock.Any()).Return(nil).AnyTimes()
	sessRepo.EXPECT().ResetActiveSessionsByIDs(gomock.Any(), []int64{longDone}).Return(int64(1), nil).Times(1)

	agtRepo := mock_agent_repo.NewMockAgentRepo(ctrl)
	agtRepo.EXPECT().Find(gomock.Any(), int64(9)).
		Return(&agent_entity.Agent{ID: 9, AgentBackendID: 3}, nil).AnyTimes()
	beRepo := mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl)
	beRepo.EXPECT().Find(gomock.Any(), int64(3)).Return(&agent_backend_entity.AgentBackend{
		ID: 3, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "7",
	}, nil).AnyTimes()

	prevSess, prevAgent, prevBE := chat_repo.Session(), agent_repo.Agent(), agent_backend_repo.AgentBackend()
	chat_repo.RegisterSession(sessRepo)
	agent_repo.RegisterAgent(agtRepo)
	agent_backend_repo.RegisterAgentBackend(beRepo)
	t.Cleanup(func() {
		chat_repo.RegisterSession(prevSess)
		agent_repo.RegisterAgent(prevAgent)
		agent_backend_repo.RegisterAgentBackend(prevBE)
	})

	svc := NewChat(NoopEmitter{}).(*chatSvc)
	svc.setConnPoolForTest(pool)

	require.NoError(t, svc.CatchUpRemoteSessions(context.Background()))
}

// Given 某台配对 daemon 此刻拨不通(开机自启早于 Wi-Fi/VPN 就绪,或 daemon 正在重启),
// 而另一台是通的,When 启动补齐,Then 拨不通那台上的会话一条都不判失败 —— 没拿到判据
// 就不下结论 —— 通的那台照常按 daemon 交回的生命周期收尾。
//
// 拨不通就全判 error 是一个**永久**的假失败:blanket 的 ResetStaleActiveSessions 已经
// 不碰远端行,这条路是该状态此后唯一的写方,而补齐只跑一次;一条在桌面端离线期间没产出
// 新内容的会话也不会被重放改写。于是「开机时 Wi-Fi 慢了两秒」= 该设备上每条远端会话
// 永久红着,正是 F6 想消除的那个失效模式。
func TestCatchUpRemoteSessions_DialFailureDoesNotJudgeSessions(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	const (
		offline int64 = 7 // 拨不通
		online  int64 = 8 // 通的
	)
	const fp = "sha256:cafe"

	client := newScriptedDaemonClient(func(method string, _, result any) error {
		switch method {
		case wire.MethodSessionList:
			*(result.(*wire.SessionListResult)) = wire.SessionListResult{Sessions: []wire.SessionSummary{
				{ConversationID: execConvID(200), LifecycleState: wire.SessionLifecycleIdle, LatestSeq: 5},
			}}
		case wire.MethodSessionPendingWaiters:
			*(result.(*wire.SessionPendingWaitersResult)) = wire.SessionPendingWaitersResult{}
		}
		return nil
	})

	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	pool.EXPECT().Borrow(gomock.Any(), offline).Return(nil, assertDialErr).Times(1)
	lease := mock_remote_device_svc.NewMockLease(ctrl)
	lease.EXPECT().Client().Return(protorpctest.WrapConnection(client)).AnyTimes()
	lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	lease.EXPECT().Release().AnyTimes()
	pool.EXPECT().Borrow(gomock.Any(), online).Return(lease, nil).Times(1)

	rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	rds.EXPECT().Get(gomock.Any(), gomock.Any()).
		Return(&remote_device_svc.DeviceView{ID: online, DaemonFingerprint: fp}, nil).AnyTimes()
	prevSvc := remote_device_svc.Default()
	remote_device_svc.SetDefault(rds)
	t.Cleanup(func() { remote_device_svc.SetDefault(prevSvc) })

	rows := []*chat_entity.Session{
		{ID: 100, ConversationID: execConvID(100), AgentID: 9, AgentStatus: "running", Status: consts.ACTIVE,
			ExecDeviceID: offline, ExecDeviceFingerprint: "sha256:beef", EventCursor: 5},
		{ID: 200, ConversationID: execConvID(200), AgentID: 9, AgentStatus: "running", Status: consts.ACTIVE,
			ExecDeviceID: online, ExecDeviceFingerprint: fp, EventCursor: 5},
	}
	sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
	sessRepo.EXPECT().ListRemoteExecSessions(gomock.Any()).Return(rows, nil)
	for _, row := range rows {
		sessRepo.EXPECT().Find(gomock.Any(), row.ID).Return(row, nil).AnyTimes()
	}
	sessRepo.EXPECT().UpdateEventCursor(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	// 通的那台上,daemon 说 200 早就空闲了 —— 它才有判据,才该收尾。100 一行都不能碰:
	// 这里没有对它的 ResetActiveSessionsByIDs 期望,多调一次 gomock 就会失败。
	sessRepo.EXPECT().ResetActiveSessionsByIDs(gomock.Any(), []int64{200}).Return(int64(1), nil).Times(1)

	agtRepo := mock_agent_repo.NewMockAgentRepo(ctrl)
	agtRepo.EXPECT().Find(gomock.Any(), int64(9)).
		Return(&agent_entity.Agent{ID: 9, AgentBackendID: 3}, nil).AnyTimes()
	beRepo := mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl)
	beRepo.EXPECT().Find(gomock.Any(), int64(3)).Return(&agent_backend_entity.AgentBackend{
		ID: 3, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "8",
	}, nil).AnyTimes()

	prevSess, prevAgent, prevBE := chat_repo.Session(), agent_repo.Agent(), agent_backend_repo.AgentBackend()
	chat_repo.RegisterSession(sessRepo)
	agent_repo.RegisterAgent(agtRepo)
	agent_backend_repo.RegisterAgentBackend(beRepo)
	t.Cleanup(func() {
		chat_repo.RegisterSession(prevSess)
		agent_repo.RegisterAgent(prevAgent)
		agent_backend_repo.RegisterAgentBackend(prevBE)
	})

	svc := NewChat(NoopEmitter{}).(*chatSvc)
	svc.setConnPoolForTest(pool)

	assert.NoError(t, svc.CatchUpRemoteSessions(context.Background()),
		"关着的远端盒子是常态,不该让另一台上的会话跟着补不成")
}

// Given 一台 daemon 上的会话全部补完,且 daemon 说它们都不在跑了,When 补齐返回,
// Then 这台设备的池连接引用全部归还,连接回到池子里等待空闲回收。
//
// 引用是 remoteRuntimeForDevice 按会话逐条加的,而唯一的减引用 releaseRemoteRuntime
// 只从 runTurn 的 defer 调 —— 补齐出来的会话永远不跑 turn,于是 entry.sessions 永不
// 清空、lease.Release() 永不发生:那条 daemon 连接在整个进程存活期内都不能被空闲回收,
// 而且开销随「历史上远端跑过的会话数」线性增长。
func TestCatchUpRemoteSessions_ReturnsPooledConnWhenNothingIsLive(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	const (
		deviceID int64 = 7
		behind   int64 = 100
		caughtUp int64 = 101
	)
	const fp = "sha256:beef"

	client := newScriptedDaemonClient(func(method string, params, result any) error {
		switch method {
		case wire.MethodSessionList:
			*(result.(*wire.SessionListResult)) = wire.SessionListResult{Sessions: []wire.SessionSummary{
				{ConversationID: execConvID(behind), LifecycleState: wire.SessionLifecycleIdle, LatestSeq: 20},
				{ConversationID: execConvID(caughtUp), LifecycleState: wire.SessionLifecycleIdle, LatestSeq: 5},
			}}
		case wire.MethodSessionAttach:
			p := params.(wire.SessionAttachParams)
			*(result.(*wire.SessionAttachResult)) = wire.SessionAttachResult{
				ConversationID: p.ConversationID, LifecycleState: wire.SessionLifecycleIdle, LatestSeq: 20,
			}
		case wire.MethodSessionPull:
			p := params.(wire.SessionPullParams)
			*(result.(*wire.SessionPullResult)) = wire.SessionPullResult{Cursor: p.Cursor}
		case wire.MethodSessionPendingWaiters:
			*(result.(*wire.SessionPendingWaitersResult)) = wire.SessionPendingWaitersResult{}
		}
		return nil
	})

	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	lease := mock_remote_device_svc.NewMockLease(ctrl)
	lease.EXPECT().Client().Return(protorpctest.WrapConnection(client)).AnyTimes()
	lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	lease.EXPECT().Release().Times(1)
	pool.EXPECT().Borrow(gomock.Any(), deviceID).Return(lease, nil).Times(1)

	rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	rds.EXPECT().Get(gomock.Any(), deviceID).
		Return(&remote_device_svc.DeviceView{ID: deviceID, DaemonFingerprint: fp}, nil).AnyTimes()
	prevSvc := remote_device_svc.Default()
	remote_device_svc.SetDefault(rds)
	t.Cleanup(func() { remote_device_svc.SetDefault(prevSvc) })

	rows := []*chat_entity.Session{
		{ID: behind, ConversationID: execConvID(behind), AgentID: 9, AgentStatus: "running", Status: consts.ACTIVE,
			ExecDeviceID: deviceID, ExecDeviceFingerprint: fp, EventCursor: 17},
		{ID: caughtUp, ConversationID: execConvID(caughtUp), AgentID: 9, AgentStatus: "running", Status: consts.ACTIVE,
			ExecDeviceID: deviceID, ExecDeviceFingerprint: fp, EventCursor: 5},
	}
	registerCatchUpRepos(t, ctrl, rows, func(sessRepo *mock_chat_repo.MockSessionRepo) {
		sessRepo.EXPECT().ResetActiveSessionsByIDs(gomock.Any(), []int64{behind, caughtUp}).
			Return(int64(2), nil).Times(1)
	})

	svc := NewChat(NoopEmitter{}).(*chatSvc)
	svc.setConnPoolForTest(pool)

	require.NoError(t, svc.CatchUpRemoteSessions(context.Background()))
	assert.Zero(t, svc.remoteRuntimeCount(deviceID),
		"补齐借的引用要还干净,否则这条池连接在进程存活期内永远不能被空闲回收")
}

// Given 这台 daemon 上有一条会话还在跑,When 补齐返回,Then 它那份引用留着 ——
// 池连接不能被回收,否则它接下来推的每一条通知都没有目标。归还只针对已经结束的那些。
func TestCatchUpRemoteSessions_KeepsPooledConnForSessionStillRunning(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	const (
		deviceID int64 = 7
		stillRun int64 = 100
		longDone int64 = 101
	)
	const fp = "sha256:beef"

	client := newScriptedDaemonClient(func(method string, params, result any) error {
		switch method {
		case wire.MethodSessionList:
			*(result.(*wire.SessionListResult)) = wire.SessionListResult{Sessions: []wire.SessionSummary{
				{ConversationID: execConvID(stillRun), LifecycleState: wire.SessionLifecycleRunning, LatestSeq: 5},
				{ConversationID: execConvID(longDone), LifecycleState: wire.SessionLifecycleIdle, LatestSeq: 5},
			}}
		case wire.MethodSessionAttach:
			p := params.(wire.SessionAttachParams)
			*(result.(*wire.SessionAttachResult)) = wire.SessionAttachResult{
				ConversationID: p.ConversationID, LifecycleState: wire.SessionLifecycleRunning, LatestSeq: 5,
			}
		case wire.MethodSessionPull:
			p := params.(wire.SessionPullParams)
			*(result.(*wire.SessionPullResult)) = wire.SessionPullResult{Cursor: p.Cursor, OldestSeq: 1}
		case wire.MethodSessionPendingWaiters:
			*(result.(*wire.SessionPendingWaitersResult)) = wire.SessionPendingWaitersResult{}
		}
		return nil
	})

	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	lease := mock_remote_device_svc.NewMockLease(ctrl)
	lease.EXPECT().Client().Return(protorpctest.WrapConnection(client)).AnyTimes()
	lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	lease.EXPECT().Release().Times(0)
	pool.EXPECT().Borrow(gomock.Any(), deviceID).Return(lease, nil).Times(1)

	rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	rds.EXPECT().Get(gomock.Any(), deviceID).
		Return(&remote_device_svc.DeviceView{ID: deviceID, DaemonFingerprint: fp}, nil).AnyTimes()
	prevSvc := remote_device_svc.Default()
	remote_device_svc.SetDefault(rds)
	t.Cleanup(func() { remote_device_svc.SetDefault(prevSvc) })

	rows := []*chat_entity.Session{
		{ID: stillRun, ConversationID: execConvID(stillRun), AgentID: 9, AgentStatus: "running", Status: consts.ACTIVE,
			ExecDeviceID: deviceID, ExecDeviceFingerprint: fp, EventCursor: 5},
		{ID: longDone, ConversationID: execConvID(longDone), AgentID: 9, AgentStatus: "running", Status: consts.ACTIVE,
			ExecDeviceID: deviceID, ExecDeviceFingerprint: fp, EventCursor: 5},
	}
	registerCatchUpRepos(t, ctrl, rows, func(sessRepo *mock_chat_repo.MockSessionRepo) {
		sessRepo.EXPECT().ResetActiveSessionsByIDs(gomock.Any(), []int64{longDone}).
			Return(int64(1), nil).Times(1)
	})

	svc := NewChat(NoopEmitter{}).(*chatSvc)
	svc.setConnPoolForTest(pool)

	require.NoError(t, svc.CatchUpRemoteSessions(context.Background()))
	assert.Equal(t, 1, svc.remoteRuntimeCount(deviceID),
		"还在跑的那条得留着引用:它接下来推的通知要落在这条连接上")
}

// registerCatchUpRepos 装上补齐一路会问到的三个 repo(会话/agent/后端),并把
// ResetActiveSessionsByIDs 的期望交给调用方定。
func registerCatchUpRepos(
	t *testing.T, ctrl *gomock.Controller, rows []*chat_entity.Session,
	expectReset func(*mock_chat_repo.MockSessionRepo),
) {
	t.Helper()
	sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
	sessRepo.EXPECT().ListRemoteExecSessions(gomock.Any()).Return(rows, nil)
	for _, row := range rows {
		sessRepo.EXPECT().Find(gomock.Any(), row.ID).Return(row, nil).AnyTimes()
	}
	sessRepo.EXPECT().UpdateEventCursor(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil).AnyTimes()
	expectReset(sessRepo)

	agtRepo := mock_agent_repo.NewMockAgentRepo(ctrl)
	agtRepo.EXPECT().Find(gomock.Any(), int64(9)).
		Return(&agent_entity.Agent{ID: 9, AgentBackendID: 3}, nil).AnyTimes()
	beRepo := mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl)
	beRepo.EXPECT().Find(gomock.Any(), int64(3)).Return(&agent_backend_entity.AgentBackend{
		ID: 3, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "7",
	}, nil).AnyTimes()

	prevSess, prevAgent, prevBE := chat_repo.Session(), agent_repo.Agent(), agent_backend_repo.AgentBackend()
	chat_repo.RegisterSession(sessRepo)
	agent_repo.RegisterAgent(agtRepo)
	agent_backend_repo.RegisterAgentBackend(beRepo)
	t.Cleanup(func() {
		chat_repo.RegisterSession(prevSess)
		agent_repo.RegisterAgent(prevAgent)
		agent_backend_repo.RegisterAgentBackend(prevBE)
	})
}

// Given 一条远端会话此刻的 agent backend 解析不出来(agent 行被删/后端被换掉),
// 而 daemon 正跑着它,When 启动补齐,Then 不判它失败 —— 补不了它不等于 daemon 说它没跑。
//
// live 只在**发给 daemon 的那批**上算,收尾却拿全量调:解析不出后端的会话必然落在
// live 之外,于是每次开 App 都被翻成 error,哪怕远端正一个字一个字地跑着它。
func TestCatchUpRemoteSessions_UnresolvedBackendSessionIsNotJudged(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	const deviceID int64 = 7
	const fp = "sha256:beef"

	client := newScriptedDaemonClient(func(method string, _, result any) error {
		if method == wire.MethodSessionList {
			*(result.(*wire.SessionListResult)) = wire.SessionListResult{Sessions: []wire.SessionSummary{
				{ConversationID: execConvID(100), LifecycleState: wire.SessionLifecycleRunning, LatestSeq: 9},
			}}
		}
		return nil
	})

	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	lease := mock_remote_device_svc.NewMockLease(ctrl)
	lease.EXPECT().Client().Return(protorpctest.WrapConnection(client)).AnyTimes()
	lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	lease.EXPECT().Release().AnyTimes()
	pool.EXPECT().Borrow(gomock.Any(), deviceID).Return(lease, nil).Times(1)

	rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	rds.EXPECT().Get(gomock.Any(), deviceID).
		Return(&remote_device_svc.DeviceView{ID: deviceID, DaemonFingerprint: fp}, nil).AnyTimes()
	prevSvc := remote_device_svc.Default()
	remote_device_svc.SetDefault(rds)
	t.Cleanup(func() { remote_device_svc.SetDefault(prevSvc) })

	row := &chat_entity.Session{ID: 100, ConversationID: execConvID(100), AgentID: 9, AgentStatus: "running", Status: consts.ACTIVE,
		ExecDeviceID: deviceID, ExecDeviceFingerprint: fp, EventCursor: 5}
	sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
	sessRepo.EXPECT().ListRemoteExecSessions(gomock.Any()).Return([]*chat_entity.Session{row}, nil)
	sessRepo.EXPECT().Find(gomock.Any(), row.ID).Return(row, nil).AnyTimes()
	sessRepo.EXPECT().UpdateEventCursor(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	// 这里没有 ResetActiveSessionsByIDs 期望:调一次 gomock 就失败。

	// agent 行没了 —— sessionBackend 返回 nil,watchCatchUpTurns 跳过这条会话。
	agtRepo := mock_agent_repo.NewMockAgentRepo(ctrl)
	agtRepo.EXPECT().Find(gomock.Any(), int64(9)).Return(nil, assertDialErr).AnyTimes()

	prevSess, prevAgent := chat_repo.Session(), agent_repo.Agent()
	chat_repo.RegisterSession(sessRepo)
	agent_repo.RegisterAgent(agtRepo)
	t.Cleanup(func() {
		chat_repo.RegisterSession(prevSess)
		agent_repo.RegisterAgent(prevAgent)
	})

	svc := NewChat(NoopEmitter{}).(*chatSvc)
	svc.setConnPoolForTest(pool)

	require.NoError(t, svc.CatchUpRemoteSessions(context.Background()))
}

// Given 启动补齐那一刻设备拨不通(会话因此一条都没判),When 设备监视随后报它重新上线,
// Then 补齐重跑一遍,这次拿到判据的会话才收尾;补成了之后再上线不重复补。
//
// 补齐只在 App.Startup 的一个 goroutine 里跑一次,此后没有任何东西重跑它 ——
// 开机自启早于 Wi-Fi/VPN 就绪的那一次失败因此是终局:该设备在本进程内再不会补齐或接管。
func TestCatchUpRemoteDevice_RetriesWhenDeviceComesBackOnline(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	const deviceID int64 = 7
	const fp = "sha256:beef"

	client := newScriptedDaemonClient(func(method string, _, result any) error {
		switch method {
		case wire.MethodSessionList:
			*(result.(*wire.SessionListResult)) = wire.SessionListResult{Sessions: []wire.SessionSummary{
				{ConversationID: execConvID(100), LifecycleState: wire.SessionLifecycleIdle, LatestSeq: 5},
			}}
		case wire.MethodSessionPendingWaiters:
			*(result.(*wire.SessionPendingWaitersResult)) = wire.SessionPendingWaitersResult{}
		}
		return nil
	})

	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	lease := mock_remote_device_svc.NewMockLease(ctrl)
	lease.EXPECT().Client().Return(protorpctest.WrapConnection(client)).AnyTimes()
	lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	lease.EXPECT().Release().AnyTimes()
	gomock.InOrder(
		pool.EXPECT().Borrow(gomock.Any(), deviceID).Return(nil, assertDialErr).Times(1),
		pool.EXPECT().Borrow(gomock.Any(), deviceID).Return(lease, nil).Times(1),
	)

	rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	rds.EXPECT().Get(gomock.Any(), deviceID).
		Return(&remote_device_svc.DeviceView{ID: deviceID, DaemonFingerprint: fp}, nil).AnyTimes()
	prevSvc := remote_device_svc.Default()
	remote_device_svc.SetDefault(rds)
	t.Cleanup(func() { remote_device_svc.SetDefault(prevSvc) })

	row := &chat_entity.Session{ID: 100, ConversationID: execConvID(100), AgentID: 9, AgentStatus: "running", Status: consts.ACTIVE,
		ExecDeviceID: deviceID, ExecDeviceFingerprint: fp, EventCursor: 5}
	sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
	// 启动那次一遍,设备回来那次一遍;补成之后的第三次不再读库。
	sessRepo.EXPECT().ListRemoteExecSessions(gomock.Any()).
		Return([]*chat_entity.Session{row}, nil).Times(2)
	sessRepo.EXPECT().Find(gomock.Any(), row.ID).Return(row, nil).AnyTimes()
	sessRepo.EXPECT().UpdateEventCursor(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	sessRepo.EXPECT().ResetActiveSessionsByIDs(gomock.Any(), []int64{100}).Return(int64(1), nil).Times(1)

	agtRepo := mock_agent_repo.NewMockAgentRepo(ctrl)
	agtRepo.EXPECT().Find(gomock.Any(), int64(9)).
		Return(&agent_entity.Agent{ID: 9, AgentBackendID: 3}, nil).AnyTimes()
	beRepo := mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl)
	beRepo.EXPECT().Find(gomock.Any(), int64(3)).Return(&agent_backend_entity.AgentBackend{
		ID: 3, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "7",
	}, nil).AnyTimes()

	prevSess, prevAgent, prevBE := chat_repo.Session(), agent_repo.Agent(), agent_backend_repo.AgentBackend()
	chat_repo.RegisterSession(sessRepo)
	agent_repo.RegisterAgent(agtRepo)
	agent_backend_repo.RegisterAgentBackend(beRepo)
	t.Cleanup(func() {
		chat_repo.RegisterSession(prevSess)
		agent_repo.RegisterAgent(prevAgent)
		agent_backend_repo.RegisterAgentBackend(prevBE)
	})

	svc := NewChat(NoopEmitter{}).(*chatSvc)
	svc.setConnPoolForTest(pool)

	ctx := context.Background()
	require.NoError(t, svc.CatchUpRemoteSessions(ctx))
	assert.Zero(t, client.countOf(wire.MethodSessionList), "拨号都没成,一条 RPC 也发不出去")

	// 设备监视报它回来了。
	require.NoError(t, svc.CatchUpRemoteDevice(ctx, deviceID))
	assert.Equal(t, 1, client.countOf(wire.MethodSessionList),
		"设备回来就补一次:会话清单是补齐的第一步")

	// 补成了的设备再上线不重复补:接下来的断连由 remote.Runtime 自己的重连接管负责。
	require.NoError(t, svc.CatchUpRemoteDevice(ctx, deviceID))
	assert.Equal(t, 1, client.countOf(wire.MethodSessionList))
}

type dialErr string

func (e dialErr) Error() string { return string(e) }

const assertDialErr = dialErr("daemon offline")

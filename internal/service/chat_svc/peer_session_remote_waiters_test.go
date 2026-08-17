package chat_svc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-ai/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-ai/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-ai/agentre/internal/repository/agent_repo"
	"github.com/agentre-ai/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-ai/agentre/internal/repository/chat_repo"
	"github.com/agentre-ai/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-ai/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

// registerPeerWaiterRepos 给这条会话装上「会话 → agent → backend」这一串解析所需的
// 仓储替身,三个都是**严格**的 mock:除了这里明写的三次 Find,任何别的仓储调用(尤其
// UpdateExecDaemon 这次落库写)都会当场把用例判红 —— 只读查询不许写库。
func registerPeerWaiterRepos(t *testing.T, ctrl *gomock.Controller, backend *agent_backend_entity.AgentBackend) {
	t.Helper()
	sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
	agtRepo := mock_agent_repo.NewMockAgentRepo(ctrl)
	beRepo := mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl)
	sessRepo.EXPECT().Find(gomock.Any(), int64(42)).Return(&chat_entity.Session{
		ID: 42, AgentID: 7, Status: consts.ACTIVE,
	}, nil).AnyTimes()
	agtRepo.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, AgentBackendID: 12, Status: consts.ACTIVE,
	}, nil).AnyTimes()
	beRepo.EXPECT().Find(gomock.Any(), int64(12)).Return(backend, nil).AnyTimes()

	prevSess, prevAgent, prevBackend := chat_repo.Session(), agent_repo.Agent(), agent_backend_repo.AgentBackend()
	chat_repo.RegisterSession(sessRepo)
	agent_repo.RegisterAgent(agtRepo)
	agent_backend_repo.RegisterAgentBackend(beRepo)
	t.Cleanup(func() {
		chat_repo.RegisterSession(prevSess)
		agent_repo.RegisterAgent(prevAgent)
		agent_backend_repo.RegisterAgentBackend(prevBackend)
	})
}

// remoteWaiterBackend 是一条钉在**另一台机器**上的 backend(DeviceID 非本机指纹)。
func remoteWaiterBackend() *agent_backend_entity.AgentBackend {
	return &agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), DeviceID: "7", Status: consts.ACTIVE,
	}
}

// Given 一条跑在远端 agentred 上的会话此刻正卡在工具审批 / 提问上(那一轮还在飞,
// 该设备的 *remote.Runtime 因此正握着 lease 留在缓存里),When 浏览器经中转向这台
// 桌面端问「这条会话还阻塞着哪些待决策」,Then 桌面端必须把远端那份快照如实交出来。
//
// 这是浏览器画审批卡 / 提问卡的**唯一**数据源:它不订阅桌面端的 Wails 事件,事件流
// 本身也从不说明哪些请求仍在阻塞。交空快照的语义是「确实没有待决策」—— 远端 agent
// 正阻塞着等答复,网页却一片空白,用户既不知情也答不了。
func TestPendingPeerSessionWaiters_GivenRemoteSessionBlockedOnDecision_ThenServesRemoteSnapshot(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	backend := remoteWaiterBackend()
	registerPeerWaiterRepos(t, ctrl, backend)

	want := wire.SessionPendingWaitersResult{
		ToolPermissions: []agentruntime.PendingToolPermission{{
			RequestID: "perm-1", ToolName: "Bash", Input: json.RawMessage(`{"command":"ls -la"}`),
		}},
		AskUserQuestions: []agentruntime.PendingAskUserQuestion{{
			RequestID: "ask-1", Questions: []agentruntime.AskQuestion{{ID: "q1", Question: "继续?"}},
		}},
	}
	var asked wire.SessionPendingWaitersParams
	client := newRecordingDaemonClient()
	client.expect(wire.MethodSessionPendingWaiters, func(params, result any) error {
		p, ok := params.(wire.SessionPendingWaitersParams)
		if !ok {
			return errors.New("pendingWaiters params shape")
		}
		asked = p
		*(result.(*wire.SessionPendingWaitersResult)) = want
		return nil
	})

	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	lease := mock_remote_device_svc.NewMockLease(ctrl)
	lease.EXPECT().Client().Return(client).AnyTimes()
	lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	lease.EXPECT().Release().AnyTimes()
	// 只许拨这一次 —— 那一轮自己借的那条。只读查询再借一条正是下面那条守卫要钉死的反面。
	pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease, nil).Times(1)

	svc := &chatSvc{emitter: NoopEmitter{}}
	svc.setConnPoolForTest(pool)
	ctx := context.Background()

	// 那一轮正卡在审批上:runTurn 的 defer 还没跑,lease 还握在手里。
	_, release, err := svc.borrowRemoteRuntimeForTurn(ctx, backend, 42)
	require.NoError(t, err)
	t.Cleanup(release)

	got, err := svc.PendingPeerSessionWaiters(ctx, wire.SessionPendingWaitersParams{SessionID: 42})

	require.NoError(t, err)
	assert.Equal(t, want, got, "浏览器画审批卡的唯一数据源就是这份快照")
	assert.Equal(t, int64(42), asked.SessionID,
		"读侧问的会话键必须与写侧提交答案的那个一致,否则浏览器会照着别处的 requestID 去答")
	assert.Equal(t, 1, client.count(wire.MethodSessionPendingWaiters),
		"待决策要真的去那台 daemon 上问一次")
}

// Given 那台设备此刻**没有**在跑的连接(缓存里没有它的 runtime,或引用已经归零把
// lease 还给了池),When 浏览器问这条会话的待决策,Then 只读查询必须回空、不拨号、
// 不落库,也不报错。
//
// 没有在跑的连接就意味着本机没有在那台设备上开着的轮次 ——「没有待决策」是这一支的
// 真话。为一份必然为空的快照去借一条远端连接会当场引出两个副作用:一次 pool.Borrow
// (拨号并占住池引用)和一次 recordExecDaemon 落库写。让「浏览器查一眼待决策」触发
// 落库写是错的;这条用例存在的意义就是不让谁哪天顺手把它改回去借连接。
func TestPendingPeerSessionWaiters_GivenNoLiveRemoteRuntime_ThenEmptyWithoutDialing(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	registerPeerWaiterRepos(t, ctrl, remoteWaiterBackend())

	// 不给 Borrow 任何期望:拨一次号就是一次 unexpected call,用例当场红。
	// 落库同理 —— registerPeerWaiterRepos 的会话仓储替身没有 UpdateExecDaemon 期望。
	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	svc := &chatSvc{emitter: NoopEmitter{}}
	svc.setConnPoolForTest(pool)

	got, err := svc.PendingPeerSessionWaiters(context.Background(),
		wire.SessionPendingWaitersParams{SessionID: 42})

	require.NoError(t, err, "没有在跑的连接是正常态,不是故障")
	assert.Equal(t, wire.SessionPendingWaitersResult{}, got)
	assert.Zero(t, svc.remoteRuntimeCount(7), "只读查询不得给任何会话记引用")
}

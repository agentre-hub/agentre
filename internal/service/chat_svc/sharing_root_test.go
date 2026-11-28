package chat_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/mock_chat_svc"
	"github.com/agentre-hub/agentre/internal/service/workspace_fs_svc"
)

// 本文件钉住 spec 2026-11-24-chat-right-toolwindows 决策 22 与「关联工具窗 · 同一份代码上在跑」:
// 只比较本桌面端拥有的、正在运行(含后台活动)的会话,按(执行设备,AI 当前所在的工作根)判定,
// 并把「写过同一个文件」算成「第一个文件 + 数量」。

type sharingRig struct {
	svc      *chatSvc
	sessions *mock_chat_repo.MockSessionRepo
	agents   *mock_agent_repo.MockAgentRepo
	port     *mock_chat_svc.MockWorkRootsPort
	written  map[int64][]workspace_fs_svc.WrittenPath
}

func newSharingRig(t *testing.T) *sharingRig {
	t.Helper()
	ctrl := gomock.NewController(t)
	rig := &sharingRig{
		sessions: mock_chat_repo.NewMockSessionRepo(ctrl),
		agents:   mock_agent_repo.NewMockAgentRepo(ctrl),
		port:     mock_chat_svc.NewMockWorkRootsPort(ctrl),
		written:  map[int64][]workspace_fs_svc.WrittenPath{},
	}
	prevSessions, prevAgents := chat_repo.Session(), agent_repo.Agent()
	chat_repo.RegisterSession(rig.sessions)
	agent_repo.RegisterAgent(rig.agents)
	t.Cleanup(func() {
		chat_repo.RegisterSession(prevSessions)
		agent_repo.RegisterAgent(prevAgents)
	})
	rig.svc = &chatSvc{
		workRoots: rig.port,
		writtenPaths: func(_ context.Context, id int64) ([]workspace_fs_svc.WrittenPath, error) {
			return rig.written[id], nil
		},
	}
	return rig
}

func liveSession(id, agentID, device int64, status string) *chat_entity.Session {
	return &chat_entity.Session{ID: id, AgentID: agentID, Title: "session-" + string(rune('0'+id)),
		AgentStatus: status, ExecDeviceID: device, Status: consts.ACTIVE}
}

// rootsAt 造一份认领集合:主根 /repo、worktree /repo/wt,AI 在 aiAt 那个根里。
func rootsAt(aiAt string) []workspace_fs_svc.WorkRootView {
	return []workspace_fs_svc.WorkRootView{
		{Path: "/repo", Name: "repo", IsPrimary: true, AIHere: aiAt == "/repo"},
		{Path: "/repo/wt", Name: "wt", IsWorktree: true, AIHere: aiAt == "/repo/wt"},
	}
}

func (r *sharingRig) self(root string) {
	r.sessions.EXPECT().Find(gomock.Any(), int64(7)).Return(liveSession(7, 70, 0, "running"), nil)
	r.port.EXPECT().WorkRoots(gomock.Any(), int64(7)).Return(rootsAt(root), nil)
}

func (r *sharingRig) active(rows ...*chat_entity.Session) {
	r.sessions.EXPECT().ListActive(gomock.Any(), []string{"running", "waiting"}).Return(rows, nil)
}

func (r *sharingRig) agent(id int64, name string) {
	r.agents.EXPECT().Find(gomock.Any(), id).Return(&agent_entity.Agent{ID: id, Name: name}, nil)
}

// Given 另一个运行中的会话与本会话在同一执行设备同一工作根, When 汇总, Then 它被列出(标题 / Agent / 根名)。
func TestListSessionsSharingRoot_IncludesSameDeviceAndRoot(t *testing.T) {
	rig := newSharingRig(t)
	rig.self("/repo")
	rig.active(liveSession(7, 70, 0, "running"), liveSession(8, 80, 0, "running"))
	rig.port.EXPECT().WorkRoots(gomock.Any(), int64(8)).Return(rootsAt("/repo"), nil)
	rig.agent(80, "Reviewer")

	got, err := rig.svc.ListSessionsSharingRoot(context.Background(), 7)

	require.NoError(t, err)
	require.Len(t, got, 1, "the asking session itself is never listed")
	assert.EqualValues(t, 8, got[0].SessionID)
	assert.Equal(t, "session-8", got[0].Title)
	assert.Equal(t, "Reviewer", got[0].AgentName)
	assert.Equal(t, "repo", got[0].RootName)
	assert.Equal(t, "running", got[0].Status)
	assert.Equal(t, 0, got[0].OverlapCount)
	assert.Empty(t, got[0].OverlapFirst)
}

// Given 同一路径但另一台执行设备, When 汇总, Then 排除,且不为它发任何工作根查询。
func TestListSessionsSharingRoot_ExcludesOtherDevice(t *testing.T) {
	rig := newSharingRig(t)
	rig.self("/repo")
	rig.active(liveSession(8, 80, 3, "running"))

	got, err := rig.svc.ListSessionsSharingRoot(context.Background(), 7)

	require.NoError(t, err)
	assert.Empty(t, got)
}

// Given 另一个会话的 AI 在 worktree(同仓库不同工作根), When 汇总, Then 排除。
func TestListSessionsSharingRoot_ExcludesOtherRoot(t *testing.T) {
	rig := newSharingRig(t)
	rig.self("/repo")
	rig.active(liveSession(8, 80, 0, "running"))
	rig.port.EXPECT().WorkRoots(gomock.Any(), int64(8)).Return(rootsAt("/repo/wt"), nil)

	got, err := rig.svc.ListSessionsSharingRoot(context.Background(), 7)

	require.NoError(t, err)
	assert.Empty(t, got)
}

// Given 同根但空闲的会话, When 汇总, Then 排除;有后台活动的空闲会话则算在跑。
func TestListSessionsSharingRoot_RunningMeansTurnOrBackgroundActivity(t *testing.T) {
	rig := newSharingRig(t)
	rig.self("/repo")
	rig.active(liveSession(8, 80, 0, "idle"))
	rig.svc.addBgRunning(9, "toolu_1")
	rig.sessions.EXPECT().Find(gomock.Any(), int64(9)).Return(liveSession(9, 90, 0, "idle"), nil)
	rig.port.EXPECT().WorkRoots(gomock.Any(), int64(9)).Return(rootsAt("/repo"), nil)
	rig.agent(90, "Builder")

	got, err := rig.svc.ListSessionsSharingRoot(context.Background(), 7)

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.EqualValues(t, 9, got[0].SessionID, "idle session 8 is excluded, background-active 9 is in")
	assert.Equal(t, "running", got[0].Status, "background activity reads as running")
}

// Given 后台活动里的会话不是本桌面端的活跃顶层会话(已删除 / 查不到 / 子代理委派), When 汇总, Then 排除。
func TestListSessionsSharingRoot_ExcludesSessionsNotOwnedByThisDesktop(t *testing.T) {
	rig := newSharingRig(t)
	rig.self("/repo")
	rig.active()
	rig.svc.addBgRunning(9, "a") // 本库没有这条会话
	rig.svc.addBgRunning(10, "b")
	rig.svc.addBgRunning(11, "c")
	rig.sessions.EXPECT().Find(gomock.Any(), int64(9)).Return(nil, errors.New("not found"))
	deleted := liveSession(10, 90, 0, "running")
	deleted.Status = consts.DELETE
	rig.sessions.EXPECT().Find(gomock.Any(), int64(10)).Return(deleted, nil)
	sub := liveSession(11, 90, 0, "running")
	sub.Purpose = chat_entity.SessionPurposeSubagent
	rig.sessions.EXPECT().Find(gomock.Any(), int64(11)).Return(sub, nil)

	got, err := rig.svc.ListSessionsSharingRoot(context.Background(), 7)

	require.NoError(t, err)
	assert.Empty(t, got)
}

// Given 两个会话写过同一批文件, When 汇总, Then 取对方写入顺序里第一个共同文件并给出共同文件数(相对根显示)。
func TestListSessionsSharingRoot_OverlapIsFirstSharedFilePlusCount(t *testing.T) {
	rig := newSharingRig(t)
	rig.self("/repo")
	rig.active(liveSession(8, 80, 0, "running"), liveSession(12, 81, 0, "waiting"))
	rig.port.EXPECT().WorkRoots(gomock.Any(), int64(8)).Return(rootsAt("/repo"), nil)
	rig.port.EXPECT().WorkRoots(gomock.Any(), int64(12)).Return(rootsAt("/repo"), nil)
	rig.agent(80, "A")
	rig.agent(81, "B")
	rig.written[7] = []workspace_fs_svc.WrittenPath{
		{Path: "/repo/a.go"}, {Path: "/repo/src/b.go"}, {Path: "c.go", Dir: "/repo/src"},
	}
	rig.written[8] = []workspace_fs_svc.WrittenPath{
		{Path: "/repo/x.go"}, {Path: "b.go", Dir: "/repo/src"}, {Path: "/repo/a.go"}, {Path: "/repo/src/c.go"},
	}
	rig.written[12] = []workspace_fs_svc.WrittenPath{{Path: "/repo/unrelated.go"}}

	got, err := rig.svc.ListSessionsSharingRoot(context.Background(), 7)

	require.NoError(t, err)
	require.Len(t, got, 2)
	byID := map[int64]*SharingRootSession{got[0].SessionID: got[0], got[1].SessionID: got[1]}
	assert.Equal(t, "src/b.go", byID[8].OverlapFirst, "first of the other session's writes that this session also wrote")
	assert.Equal(t, 3, byID[8].OverlapCount)
	assert.Equal(t, "waiting", byID[12].Status)
	assert.Equal(t, 0, byID[12].OverlapCount)
	assert.Empty(t, byID[12].OverlapFirst)
}

// Given 本会话的 AI 不在任何已认领的根里, When 汇总, Then 没有可比较的根,空列表且不扫其它会话。
func TestListSessionsSharingRoot_NoRootForThisSessionIsEmpty(t *testing.T) {
	rig := newSharingRig(t)
	rig.self("")

	got, err := rig.svc.ListSessionsSharingRoot(context.Background(), 7)

	require.NoError(t, err)
	assert.Empty(t, got)
}

// 单个同伴会话的工作根取不到只降级(跳过它),不让整个关联窗失败。
func TestListSessionsSharingRoot_PeerFailureSkipsThatPeer(t *testing.T) {
	rig := newSharingRig(t)
	rig.self("/repo")
	rig.active(liveSession(8, 80, 0, "running"), liveSession(12, 81, 0, "running"))
	rig.port.EXPECT().WorkRoots(gomock.Any(), int64(8)).Return(nil, errors.New("device offline"))
	rig.port.EXPECT().WorkRoots(gomock.Any(), int64(12)).Return(rootsAt("/repo"), nil)
	rig.agent(81, "B")

	got, err := rig.svc.ListSessionsSharingRoot(context.Background(), 7)

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.EqualValues(t, 12, got[0].SessionID)
}

package chat_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
)

// 本文件锁住 R15b / 决策36「会话粘性」：续轮按会话钉住的那一档解析 backend（不重挑，
// 即使排序里有更靠前的档现在可用）；同机多档时取的是原档而不是"同机第一档"；只有
// 钉档记录已经不存在时才按当前执行目标恢复；无该值的老会话回落到按 R15 顺序挑并写回。

type execTargetPinMocks struct {
	agent              *mock_agent_repo.MockAgentRepo
	execTarget         *mock_agent_repo.MockAgentExecTargetRepo
	execTargetOverride *mock_agent_repo.MockAgentExecTargetOverrideRepo
	backend            *mock_agent_backend_repo.MockAgentBackendRepo
	session            *mock_chat_repo.MockSessionRepo
}

func setupExecTargetPinTest(t *testing.T) (context.Context, *execTargetPinMocks, *chatSvc) {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := &execTargetPinMocks{
		agent:              mock_agent_repo.NewMockAgentRepo(ctrl),
		execTarget:         mock_agent_repo.NewMockAgentExecTargetRepo(ctrl),
		execTargetOverride: mock_agent_repo.NewMockAgentExecTargetOverrideRepo(ctrl),
		backend:            mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl),
		session:            mock_chat_repo.NewMockSessionRepo(ctrl),
	}

	prevAgent := agent_repo.Agent()
	prevExecTarget := agent_repo.AgentExecTarget()
	prevOverride := agent_repo.AgentExecTargetOverride()
	prevBackend := agent_backend_repo.AgentBackend()
	prevSession := chat_repo.Session()
	agent_repo.RegisterAgent(m.agent)
	agent_repo.RegisterAgentExecTarget(m.execTarget)
	agent_repo.RegisterAgentExecTargetOverride(m.execTargetOverride)
	agent_backend_repo.RegisterAgentBackend(m.backend)
	chat_repo.RegisterSession(m.session)
	// R14 顺序解析的宽松桩：这批用例不关心本端覆盖（默认无覆盖）。
	m.execTargetOverride.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	t.Cleanup(func() {
		agent_repo.RegisterAgent(prevAgent)
		agent_repo.RegisterAgentExecTarget(prevExecTarget)
		agent_repo.RegisterAgentExecTargetOverride(prevOverride)
		agent_backend_repo.RegisterAgentBackend(prevBackend)
		chat_repo.RegisterSession(prevSession)
	})

	return context.Background(), m, NewChat(NoopEmitter{}).(*chatSvc)
}

// localClaudeCode 是一个"CLI 自身 login 状态"的本地 claudecode 后端：LLMProviderKey
// 为空短路判可用（既有行为,chat.go:391 一样),不需要额外 mock provider/gateway。
func localClaudeCode(id int64) *agent_backend_entity.AgentBackend {
	return &agent_backend_entity.AgentBackend{ID: id, Type: string(agent_backend_entity.TypeClaudeCode)}
}

// ── ② 续轮按会话钉住的那一档解析,不因排序里有更靠前的档现在可用而改派 ──────

func TestResolveAgentBackend_GivenSessionWithStickyPin_WhenContinuingTurn_ThenUsesPinnedTargetNotReordered(t *testing.T) {
	ctx, m, svc := setupExecTargetPinTest(t)

	// Agent 的执行目标列表把 backend 51 排在最前;会话却钉在 backend 52 上
	// (比如它是首轮实际落地的那一档)。续轮必须直接用 52,绝不能因为 51"排在
	// 更前面"就改派——这里干脆不给 ListByAgent / PickExecTarget 设任何期望,
	// 一旦解析函数误触发挑选逻辑,gomock 会因未预期调用直接判这条用例失败。
	sess := &chat_entity.Session{ID: 900, AgentID: 401, ExecAgentBackendID: 52}
	m.agent.EXPECT().Find(ctx, int64(401)).Return(&agent_entity.Agent{ID: 401, AgentBackendID: 51}, nil)
	m.backend.EXPECT().Find(ctx, int64(52)).Return(localClaudeCode(52), nil)

	a, be, _, err := svc.resolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID)
	require.NoError(t, err)
	assert.Equal(t, int64(401), a.ID)
	assert.Equal(t, int64(52), be.ID, "续轮必须解析到会话钉住的档,不是列表里排最前的档")
}

// ── ③ 同机多档时续轮取的是原档,不是"同机第一档" ─────────────────────────

func TestResolveAgentBackend_GivenSameDeviceMultipleTargets_WhenContinuingTurn_ThenResolvesExactPinnedTargetNotFirstOnDevice(t *testing.T) {
	ctx, m, svc := setupExecTargetPinTest(t)

	// 同一台远端设备(daemon 指纹通过 DeviceID="9" 表达)上挂了两档:71 排最前、
	// 72 排第二;会话钉在 72 上。resolveAgentBackend 必须精确解析出 72,而不是
	// 落到"这台设备上的第一档"71——同样不给 ListByAgent 设期望,证明续轮压根
	// 没有回头扫过列表。
	sess := &chat_entity.Session{ID: 901, AgentID: 402, ExecAgentBackendID: 72}
	m.agent.EXPECT().Find(ctx, int64(402)).Return(&agent_entity.Agent{ID: 402, AgentBackendID: 71}, nil)
	target72 := &agent_backend_entity.AgentBackend{
		ID: 72, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: "9",
	}
	m.backend.EXPECT().Find(ctx, int64(72)).Return(target72, nil)

	_, be, _, err := svc.resolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID)
	require.NoError(t, err)
	assert.Equal(t, int64(72), be.ID, "同机多档续轮必须取原档,不是同机第一档 71")
}

// ── 钉档记录已被删除时，按 Agent 当前执行目标恢复并替换失效钉档 ────────────

func TestResolveAgentBackend_GivenPinnedBackendWasDeleted_WhenCurrentTargetIsAvailable_ThenRecoversAndRepins(t *testing.T) {
	ctx, m, svc := setupExecTargetPinTest(t)

	sess := &chat_entity.Session{ID: 902, AgentID: 403, ExecAgentBackendID: 61}
	m.agent.EXPECT().Find(ctx, int64(403)).Return(&agent_entity.Agent{ID: 403, AgentBackendID: 62}, nil)
	// 用户删掉旧 backend 61 并重新给 Agent 配了 backend 62。旧会话里的钉档是
	// 派生引用，不能让它永久盖过 Agent 当前配置并报成“未绑定可对话后端”。
	m.backend.EXPECT().Find(ctx, int64(61)).Return(nil, nil)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(403)).Return([]*agent_entity.AgentExecTarget{
		{ID: 13, AgentID: 403, AgentBackendID: 62, SortOrder: 0},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(62)).Return(localClaudeCode(62), nil)
	m.session.EXPECT().UpdateExecDaemon(ctx, int64(902), int64(0), "", int64(62)).Return(nil)

	_, be, _, err := svc.resolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID)
	require.NoError(t, err)
	assert.Equal(t, int64(62), be.ID)
	assert.Equal(t, int64(62), sess.ExecAgentBackendID, "恢复后要替换内存与数据库里的失效钉档")
}

// ── ④ 无该值的老会话回落到按 R15 顺序挑,并且真的可以被调用方写回 ──────────

func TestResolveAgentBackend_GivenNoStickyValueAndMultipleTargets_ThenPicksFirstAvailableByR15Order(t *testing.T) {
	ctx, m, svc := setupExecTargetPinTest(t)

	// 老会话:没有钉住任何一档(ExecAgentBackendID 是迁移默认值 0)。Agent 的执行
	// 目标列表非空且有两档:501 排最前但类型未知(判 BlockReasonUnknownBackend
	// 不可用),502 排第二且是"CLI 登录态"本地 claudecode(判可用)。回落挑选必须
	// 按 R15 顺序跳过 501、落到 502——而不是直接采用 a.AgentBackendID(=501)。
	sess := &chat_entity.Session{ID: 903, AgentID: 404, ProjectID: 0}
	m.agent.EXPECT().Find(ctx, int64(404)).Return(&agent_entity.Agent{ID: 404, AgentBackendID: 501}, nil)
	targets := []*agent_entity.AgentExecTarget{
		{ID: 11, AgentID: 404, AgentBackendID: 501, SortOrder: 0},
		{ID: 12, AgentID: 404, AgentBackendID: 502, SortOrder: 1},
	}
	m.execTarget.EXPECT().ListByAgent(ctx, int64(404)).Return(targets, nil).AnyTimes()
	m.backend.EXPECT().Find(ctx, int64(501)).Return(&agent_backend_entity.AgentBackend{
		ID: 501, Type: "totally-unknown-type",
	}, nil).AnyTimes()
	m.backend.EXPECT().Find(ctx, int64(502)).Return(localClaudeCode(502), nil).AnyTimes()

	_, be, _, err := svc.resolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID)
	require.NoError(t, err)
	assert.Equal(t, int64(502), be.ID, "老会话没钉住时必须跳过不可用的 501,按顺序落到可用的 502")
}

func TestPinExecTargetIfUnset_GivenSessionWithoutPin_ThenWritesBackViaUpdateExecDaemonAndUpdatesInMemory(t *testing.T) {
	ctx, m, svc := setupExecTargetPinTest(t)

	sess := &chat_entity.Session{ID: 904, AgentID: 405, ExecAgentBackendID: 0}
	be := localClaudeCode(502)
	m.session.EXPECT().
		UpdateExecDaemon(ctx, int64(904), int64(0), "", int64(502)).
		Return(nil).Times(1)

	svc.pinExecTargetIfUnset(ctx, sess, be)

	assert.Equal(t, int64(502), sess.ExecAgentBackendID, "写库成功后内存实体也要同步,同一轮内后续读取才不会看到旧的 0")
}

func TestPinExecTargetIfUnset_GivenSessionAlreadyPinned_ThenDoesNotWriteAgain(t *testing.T) {
	ctx, _, svc := setupExecTargetPinTest(t)

	// 已经钉过(ExecAgentBackendID != 0):不重复写——没给 UpdateExecDaemon 设任何
	// 期望,一旦触发写入 gomock 会判这条用例失败。
	sess := &chat_entity.Session{ID: 905, AgentID: 406, ExecAgentBackendID: 61}
	be := localClaudeCode(61)

	svc.pinExecTargetIfUnset(ctx, sess, be)

	assert.Equal(t, int64(61), sess.ExecAgentBackendID)
}

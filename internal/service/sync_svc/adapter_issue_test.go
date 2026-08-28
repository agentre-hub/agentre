package sync_svc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/issue_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo/mock_issue_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo/mock_syncstate_repo"
)

// indexOfSyncKind 报告某个对象类型在 syncKinds 里的位置；不在其中返回 -1。
func indexOfSyncKind(kind string) int {
	for i, k := range syncKinds {
		if k == kind {
			return i
		}
	}
	return -1
}

// TestSyncKinds_GivenTheBoard_PlacesEveryReferencedKindFirst 看板并入同步组：三个
// 新对象类型都在 syncKinds 里，且顺序满足「被引用者在前」——label → issue →
// issue_label，任务本身还排在它引用的项目 / Agent / backend 之后。认领（R12a）与
// 每一处遍历全部类型的地方都按这个顺序走，父行因此先入队、先落地。
func TestSyncKinds_GivenTheBoard_PlacesEveryReferencedKindFirst(t *testing.T) {
	adapters := defaultAdapters(nil)
	for _, kind := range []string{syncwire.KindLabel, syncwire.KindIssue, syncwire.KindIssueLabel} {
		assert.True(t, kindKnown(kind), kind)
		assert.NotNil(t, adapters[kind], "%s 必须有适配器，否则入队时被静默丢弃", kind)
		assert.GreaterOrEqual(t, indexOfSyncKind(kind), 0, kind)
	}
	assert.Less(t, indexOfSyncKind(syncwire.KindLabel), indexOfSyncKind(syncwire.KindIssue),
		"标签是任务的被引用者")
	assert.Less(t, indexOfSyncKind(syncwire.KindIssue), indexOfSyncKind(syncwire.KindIssueLabel),
		"任务是关联行的被引用者")
	for _, referenced := range []string{
		syncwire.KindProject, syncwire.KindAgent, syncwire.KindAgentBackend,
	} {
		assert.Less(t, indexOfSyncKind(referenced), indexOfSyncKind(syncwire.KindIssue),
			"任务引用 %s，它必须排在前面", referenced)
	}
}

// ── 标签 ────────────────────────────────────────────────────────────────────

// TestLabelAdapter_LoadCarriesOnlyTheCatalogFields 标签载荷就是规格那三个键：
// name / tone / status，没有本地自增 ID。
func TestLabelAdapter_LoadCarriesOnlyTheCatalogFields(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindRow(gomock.Any(), syncwire.KindLabel, "label-1", gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, dest any) (bool, error) {
			*dest.(*issue_entity.Label) = issue_entity.Label{
				ID: 9, Name: "bug", Tone: issue_entity.ToneRed, Status: consts.ACTIVE,
				Updatetime: 1700, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "label-1"},
			}
			return true, nil
		})
	syncstate_repo.RegisterSyncState(state)

	out, err := labelAdapter{}.load(context.Background(), "label-1")
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "label-1", out.SyncID)
	assert.Equal(t, int64(1700), out.UpdatedAt)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(out.Payload, &payload))
	assert.Equal(t, "bug", payload["name"])
	assert.Equal(t, issue_entity.ToneRed, payload["tone"])
	assert.EqualValues(t, consts.ACTIVE, payload["status"])
	assert.NotContains(t, payload, "id")
	assert.NoError(t, syncwire.GuardPayload(syncwire.KindLabel, out.Payload))
}

// TestLabelAdapter_ApplyCreatesWithTheIncomingSyncID 下行的新标签沿用源端的同步
// 标识（R1：标识终身不变）。
func TestLabelAdapter_ApplyCreatesWithTheIncomingSyncID(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindRow(gomock.Any(), syncwire.KindLabel, "label-1", gomock.Any()).Return(false, nil)
	syncstate_repo.RegisterSyncState(state)

	labels := mock_issue_repo.NewMockLabelRepo(ctrl)
	labels.EXPECT().FindByName(gomock.Any(), "feature").Return(nil, nil)
	var created *issue_entity.Label
	labels.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, l *issue_entity.Label) error { created = l; return nil })
	issue_repo.RegisterLabel(labels)

	require.NoError(t, labelAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindLabel, SyncID: "label-1", Version: 4,
		Payload: []byte(`{"name":"feature","tone":"blue","status":1}`),
	}, map[string]int64{}))

	require.NotNil(t, created)
	assert.Equal(t, "label-1", created.SyncID)
	assert.Equal(t, "feature", created.Name)
	assert.Equal(t, issue_entity.ToneBlue, created.Tone)
	assert.Equal(t, consts.ACTIVE, created.Status)
}

// TestLabelAdapter_ApplyAdoptsTheRowAlreadyHoldingTheName 两端各自建了同名标签
// （种子标签之外的用户自建标签带着两个不同的同步标识）：本机那一行还占着
// uniq_labels_name_active，硬插会撞唯一索引把整轮下行带崩。名字就是自然键——
// 接管本机那一行，让它跟随账号里胜出的同步标识，不为同一件事再插一行。
func TestLabelAdapter_ApplyAdoptsTheRowAlreadyHoldingTheName(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindRow(gomock.Any(), syncwire.KindLabel, "label-remote", gomock.Any()).Return(false, nil)
	syncstate_repo.RegisterSyncState(state)

	labels := mock_issue_repo.NewMockLabelRepo(ctrl)
	labels.EXPECT().FindByName(gomock.Any(), "spike").Return(&issue_entity.Label{
		ID: 12, Name: "spike", Tone: issue_entity.ToneGray, Status: consts.ACTIVE,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "label-local"},
	}, nil)
	var updated *issue_entity.Label
	labels.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, l *issue_entity.Label) error { updated = l; return nil })
	issue_repo.RegisterLabel(labels)

	require.NoError(t, labelAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindLabel, SyncID: "label-remote",
		Payload: []byte(`{"name":"spike","tone":"violet","status":1}`),
	}, map[string]int64{}))

	require.NotNil(t, updated, "接管本机那一行，不再插一行")
	assert.Equal(t, int64(12), updated.ID)
	assert.Equal(t, "label-remote", updated.SyncID)
	assert.Equal(t, issue_entity.ToneViolet, updated.Tone)
}

// TestLabelAdapter_RemoveSoftDeletesAndDetachesFromTasks 墓碑到达：标签软删，
// 并从全部任务上摘掉——否则本机留下一串指向已消失标签的关联行。
func TestLabelAdapter_RemoveSoftDeletesAndDetachesFromTasks(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindLocalID(gomock.Any(), syncwire.KindLabel, "label-1").Return(int64(9), nil)
	syncstate_repo.RegisterSyncState(state)

	labels := mock_issue_repo.NewMockLabelRepo(ctrl)
	labels.EXPECT().Delete(gomock.Any(), int64(9)).Return(nil)
	issue_repo.RegisterLabel(labels)
	links := mock_issue_repo.NewMockIssueLabelRepo(ctrl)
	links.EXPECT().DeleteByLabel(gomock.Any(), int64(9)).Return(nil)
	issue_repo.RegisterIssueLabel(links)

	require.NoError(t, labelAdapter{}.remove(context.Background(), &inbound{
		Kind: syncwire.KindLabel, SyncID: "label-1", DeletedAt: 1700,
	}))
}

// ── 任务 ────────────────────────────────────────────────────────────────────

// TestIssueAdapter_LoadTranslatesEveryLocalIDIntoASyncIdentifier R2：任务的项目、
// 执行 Agent 与机器在载荷里全是同步标识；本机自增 ID、会话 ID 与运行态一律不上行。
func TestIssueAdapter_LoadTranslatesEveryLocalIDIntoASyncIdentifier(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindRow(gomock.Any(), syncwire.KindIssue, "issue-1", gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, dest any) (bool, error) {
			*dest.(*issue_entity.Issue) = issue_entity.Issue{
				ID: 3, ProjectID: 4, Title: "Ship it", Body: "why",
				State: issue_entity.StateOpen, Stage: issue_entity.StageDoing, Position: 2.5,
				AssigneeAgentID: 5, AgentBackendID: 6,
				LLMProviderKey: "anthropic-main", LLMModelKey: "opus",
				SessionID: 77, AgentStatus: issue_entity.AgentStatusIdle,
				Updatetime: 1700, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "issue-1"},
			}
			return true, nil
		})
	syncstate_repo.RegisterSyncState(state)

	projects := mock_project_repo.NewMockProjectRepo(ctrl)
	projects.EXPECT().Find(gomock.Any(), int64(4)).Return(&project_entity.Project{
		ID: 4, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "proj-1"},
	}, nil)
	project_repo.RegisterProject(projects)
	agents := mock_agent_repo.NewMockAgentRepo(ctrl)
	agents.EXPECT().Find(gomock.Any(), int64(5)).Return(&agent_entity.Agent{
		ID: 5, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "agent-1"},
	}, nil)
	agent_repo.RegisterAgent(agents)
	backends := mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl)
	backends.EXPECT().Find(gomock.Any(), int64(6)).Return(&agent_backend_entity.AgentBackend{
		ID: 6, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "backend-1"},
	}, nil)
	agent_backend_repo.RegisterAgentBackend(backends)

	out, err := issueAdapter{}.load(context.Background(), "issue-1")
	require.NoError(t, err)
	require.NotNil(t, out)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(out.Payload, &payload))
	assert.Equal(t, "proj-1", payload["project_sync_id"])
	assert.Equal(t, "agent-1", payload["agent_sync_id"])
	assert.Equal(t, "backend-1", payload["agent_backend_sync_id"])
	assert.Equal(t, "Ship it", payload["title"])
	assert.Equal(t, "why", payload["description"])
	assert.Equal(t, issue_entity.StageDoing, payload["stage"])
	assert.EqualValues(t, 2.5, payload["position"])
	assert.Equal(t, "anthropic-main", payload["llm_provider_key"])
	assert.Equal(t, "opus", payload["llm_model_key"])
	for _, absent := range []string{"project_id", "assignee_agent_id", "agent_backend_id",
		"session_id", "agent_status", "id"} {
		assert.NotContains(t, payload, absent, "载荷里不出现任何本地自增 ID 或运行态")
	}
	assert.NoError(t, syncwire.GuardPayload(syncwire.KindIssue, out.Payload))
}

// TestIssueAdapter_RefsBlockOnEveryReferencedObject R2a：任务的三个跨机引用都要
// 先解析出来，解析不出这一行暂缓落地，绝不写悬空引用。
func TestIssueAdapter_RefsBlockOnEveryReferencedObject(t *testing.T) {
	refs := issueAdapter{}.refs(&inbound{Kind: syncwire.KindIssue, Payload: []byte(
		`{"project_sync_id":"p","agent_sync_id":"a","agent_backend_sync_id":"b"}`)})
	assert.Contains(t, refs, ref{Kind: syncwire.KindProject, SyncID: "p"})
	assert.Contains(t, refs, ref{Kind: syncwire.KindAgent, SyncID: "a"})
	assert.Contains(t, refs, ref{Kind: syncwire.KindAgentBackend, SyncID: "b"})
}

// TestIssueAdapter_ApplyResolvesSyncIdentifiersBackToLocalRows 下行的反方向：
// 载荷里的同步标识解析回本机的自增 ID，落在这台机器自己的项目 / Agent / 机器上。
func TestIssueAdapter_ApplyResolvesSyncIdentifiersBackToLocalRows(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindRow(gomock.Any(), syncwire.KindIssue, "issue-1", gomock.Any()).Return(false, nil)
	syncstate_repo.RegisterSyncState(state)

	issues := mock_issue_repo.NewMockIssueRepo(ctrl)
	var created *issue_entity.Issue
	issues.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, i *issue_entity.Issue) error { created = i; return nil })
	issue_repo.RegisterIssue(issues)

	require.NoError(t, issueAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindIssue, SyncID: "issue-1", Version: 9,
		Payload: []byte(`{"title":"Ship it","description":"why","stage":"doing","position":2.5,` +
			`"project_sync_id":"proj-1","agent_sync_id":"agent-1","agent_backend_sync_id":"backend-1",` +
			`"llm_provider_key":"anthropic-main","llm_model_key":"opus","closed_at":0}`),
	}, map[string]int64{
		"project:proj-1":          40,
		"agent:agent-1":           50,
		"agent_backend:backend-1": 60,
	}))

	require.NotNil(t, created)
	assert.Equal(t, "issue-1", created.SyncID)
	assert.Equal(t, int64(40), created.ProjectID)
	assert.Equal(t, int64(50), created.AssigneeAgentID)
	assert.Equal(t, int64(60), created.AgentBackendID)
	assert.Equal(t, issue_entity.StageDoing, created.Stage)
	assert.Equal(t, issue_entity.StateOpen, created.State)
	assert.EqualValues(t, 2.5, created.Position)
	assert.Equal(t, consts.ACTIVE, created.Status)
}

// TestIssueAdapter_ApplyGivenTheDoneStage_ClosesTheTask 状态轴本轮消失，state 只由
// 阶段推导：落在「已完成」列的任务在本机也是 closed，关闭时刻沿用源端那一个。
func TestIssueAdapter_ApplyGivenTheDoneStage_ClosesTheTask(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindRow(gomock.Any(), syncwire.KindIssue, "issue-2", gomock.Any()).Return(false, nil)
	syncstate_repo.RegisterSyncState(state)

	issues := mock_issue_repo.NewMockIssueRepo(ctrl)
	var created *issue_entity.Issue
	issues.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, i *issue_entity.Issue) error { created = i; return nil })
	issue_repo.RegisterIssue(issues)

	require.NoError(t, issueAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindIssue, SyncID: "issue-2",
		Payload: []byte(`{"title":"done","stage":"done","closed_at":1699}`),
	}, map[string]int64{}))

	require.NotNil(t, created)
	assert.Equal(t, issue_entity.StateClosed, created.State)
	assert.Equal(t, int64(1699), created.ClosedAt)
}

// TestIssueAdapter_RemoveSoftDeletes 墓碑到达：任务在本机软删（R6）。
func TestIssueAdapter_RemoveSoftDeletes(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindLocalID(gomock.Any(), syncwire.KindIssue, "issue-1").Return(int64(3), nil)
	syncstate_repo.RegisterSyncState(state)

	issues := mock_issue_repo.NewMockIssueRepo(ctrl)
	issues.EXPECT().Delete(gomock.Any(), int64(3)).Return(nil)
	issue_repo.RegisterIssue(issues)

	require.NoError(t, issueAdapter{}.remove(context.Background(), &inbound{
		Kind: syncwire.KindIssue, SyncID: "issue-1", DeletedAt: 1700,
	}))
}

// ── 任务 ↔ 标签 ─────────────────────────────────────────────────────────────

// TestIssueLabelAdapter_LoadCarriesBothSidesAsSyncIdentifiers 关联行跨机表达的是
// 「哪个任务挂了哪个标签」：两端的本地自增主键各不相同，只能靠同步标识指认。
func TestIssueLabelAdapter_LoadCarriesBothSidesAsSyncIdentifiers(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindRow(gomock.Any(), syncwire.KindIssueLabel, "link-1", gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, dest any) (bool, error) {
			*dest.(*issue_entity.IssueLabel) = issue_entity.IssueLabel{
				IssueID: 3, LabelID: 9,
				SyncMeta: syncmeta_entity.SyncMeta{SyncID: "link-1", SyncUpdatedAt: 1700},
			}
			return true, nil
		})
	syncstate_repo.RegisterSyncState(state)

	issues := mock_issue_repo.NewMockIssueRepo(ctrl)
	issues.EXPECT().Find(gomock.Any(), int64(3)).Return(&issue_entity.Issue{
		ID: 3, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "issue-1"},
	}, nil)
	issue_repo.RegisterIssue(issues)
	labels := mock_issue_repo.NewMockLabelRepo(ctrl)
	labels.EXPECT().Find(gomock.Any(), int64(9)).Return(&issue_entity.Label{
		ID: 9, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "label-1"},
	}, nil)
	issue_repo.RegisterLabel(labels)

	out, err := issueLabelAdapter{}.load(context.Background(), "link-1")
	require.NoError(t, err)
	require.NotNil(t, out)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(out.Payload, &payload))
	assert.Equal(t, map[string]any{"issue_sync_id": "issue-1", "label_sync_id": "label-1"}, payload)
	assert.NoError(t, syncwire.GuardPayload(syncwire.KindIssueLabel, out.Payload))
}

// TestIssueLabelAdapter_LoadGivenEitherSideIsGone_SendsNothing 两端之一在本机已经
// 不存在：这条关联没有可表达的跨机引用，交给它自己的删除路径去落墓碑，这里不发
// 一条带空引用的上行（与 projectAgentAdapter 同一口径）。
func TestIssueLabelAdapter_LoadGivenEitherSideIsGone_SendsNothing(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindRow(gomock.Any(), syncwire.KindIssueLabel, "link-1", gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, dest any) (bool, error) {
			*dest.(*issue_entity.IssueLabel) = issue_entity.IssueLabel{
				IssueID: 3, LabelID: 9,
				SyncMeta: syncmeta_entity.SyncMeta{SyncID: "link-1"},
			}
			return true, nil
		})
	syncstate_repo.RegisterSyncState(state)

	issues := mock_issue_repo.NewMockIssueRepo(ctrl)
	issues.EXPECT().Find(gomock.Any(), int64(3)).Return(nil, nil)
	issue_repo.RegisterIssue(issues)
	labels := mock_issue_repo.NewMockLabelRepo(ctrl)
	labels.EXPECT().Find(gomock.Any(), int64(9)).Return(&issue_entity.Label{
		ID: 9, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "label-1"},
	}, nil).AnyTimes()
	issue_repo.RegisterLabel(labels)

	out, err := issueLabelAdapter{}.load(context.Background(), "link-1")
	require.NoError(t, err)
	assert.Nil(t, out)
}

// TestIssueLabelAdapter_ApplyKeepsTheIncomingSyncID 下行落地：两个标识解析回本机
// 行，关联行沿用源端的同步标识（重新生成一个会让同一件事在账号里变成两份）。
func TestIssueLabelAdapter_ApplyKeepsTheIncomingSyncID(t *testing.T) {
	ctrl := gomock.NewController(t)
	links := mock_issue_repo.NewMockIssueLabelRepo(ctrl)
	var landed *issue_entity.IssueLabel
	links.EXPECT().UpsertFromSync(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, row *issue_entity.IssueLabel) error { landed = row; return nil })
	issue_repo.RegisterIssueLabel(links)

	require.NoError(t, issueLabelAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindIssueLabel, SyncID: "link-1",
		Payload: []byte(`{"issue_sync_id":"issue-1","label_sync_id":"label-1"}`),
	}, map[string]int64{"issue:issue-1": 3, "label:label-1": 9}))

	require.NotNil(t, landed)
	assert.Equal(t, "link-1", landed.SyncID)
	assert.Equal(t, int64(3), landed.IssueID)
	assert.Equal(t, int64(9), landed.LabelID)
}

// TestIssueLabelAdapter_ApplyGivenAnUnresolvedSide_Defers R2a：任务或标签还没到，
// 这一行暂缓落地，绝不写一条指向不存在行的关联。
func TestIssueLabelAdapter_ApplyGivenAnUnresolvedSide_Defers(t *testing.T) {
	ctrl := gomock.NewController(t)
	links := mock_issue_repo.NewMockIssueLabelRepo(ctrl)
	issue_repo.RegisterIssueLabel(links)

	err := issueLabelAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindIssueLabel, SyncID: "link-1",
		Payload: []byte(`{"issue_sync_id":"issue-1","label_sync_id":"label-1"}`),
	}, map[string]int64{"issue:issue-1": 3})

	assert.ErrorIs(t, err, errRefMissing)
}

// TestIssueLabelAdapter_RemoveDeletesBySyncID 墓碑到达：按同步标识删掉那一行。
// 关联表是硬删（没有 status 列），本地 ID 也指认不了它（联合主键）。
func TestIssueLabelAdapter_RemoveDeletesBySyncID(t *testing.T) {
	ctrl := gomock.NewController(t)
	links := mock_issue_repo.NewMockIssueLabelRepo(ctrl)
	links.EXPECT().DeleteBySyncID(gomock.Any(), "link-1").Return(nil)
	issue_repo.RegisterIssueLabel(links)

	require.NoError(t, issueLabelAdapter{}.remove(context.Background(), &inbound{
		Kind: syncwire.KindIssueLabel, SyncID: "link-1", DeletedAt: 1700,
	}))
}

// TestPull_GivenAnIssueLabelPointingAtNothing_StillCompletesTheRound 关联行的墓碑
// **不**跟着任务 / 标签的删除一起下发（三个适配器都用 baseAdapter 的空 children），
// 所以账号里会留下指向已被别处删掉的标签或任务的孤儿关联对象。那是允许的——但只有
// 在它落不了地时被**跳过**才允许：一条落不了地的行如果让整轮下行报错，一个孤儿就能
// 把此后每一轮、每一种对象类型全部卡死，正是表名白名单那一类的故障形状。
//
// 这里让一条两个引用都解析不出的 issue_label 混在两条正常项目之间，断言这一轮照常
// 走完：前后两条都落了地、游标照常推进，孤儿那一条进暂缓队列（R2a），30 天后被
// gcDeferred 当成引用丢失清掉。
func TestPull_GivenAnIssueLabelPointingAtNothing_StillCompletesTheRound(t *testing.T) {
	h := newHarness(t, true)
	h.svc.adapters[syncwire.KindIssueLabel] = issueLabelAdapter{}
	h.transport.pages = []*syncwire.PullPage{{
		Items: []syncwire.PullItem{
			{Kind: "project", SyncID: "p-1", Version: 1, Payload: []byte(`{"name":"A"}`)},
			{
				Kind: syncwire.KindIssueLabel, SyncID: "link-orphan", Version: 2,
				Payload: []byte(`{"issue_sync_id":"issue-gone","label_sync_id":"label-gone"}`),
			},
			{Kind: "project", SyncID: "p-2", Version: 3, Payload: []byte(`{"name":"C"}`)},
		},
		NextCursor: 3,
	}}

	require.NoError(t, h.svc.SyncOnce(context.Background()), "一个孤儿关联不该让整轮下行失败")

	assert.Equal(t, "A", h.adapter.rows["p-1"])
	assert.Equal(t, "C", h.adapter.rows["p-2"], "孤儿后面的行照常落地")
	st, err := h.svc.loadCursor(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, int64(3), st.Cursor, "游标照常推进，下一轮不会再从同一页重来")
	require.Len(t, h.inbound.rows, 1)
	assert.Equal(t, "link-orphan", h.inbound.rows[0].EntitySyncID)
	// 它是被**认出来**的暂缓，不是被兜底捞进队列的失败：引擎记下了在等哪一个引用，
	// replayDeferred 才有东西可等。兜底那条路径这一列是空的。
	assert.Equal(t, "issue:issue-gone", h.inbound.rows[0].MissingSyncID,
		"关联行把两端都声明成阻塞引用，解析不出时按 R2a 暂缓而不是报错")
	assert.Empty(t, h.svc.getLastErr(), "这一轮是干净的，同步状态里不该留一句错误")
}

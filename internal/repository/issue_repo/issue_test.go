package issue_repo_test

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/issue_entity"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo"
)

func setupIssueRepo(t *testing.T) (context.Context, sqlmock.Sqlmock, issue_repo.IssueRepo) {
	t.Helper()
	ctx, _, mock := testutils.Database(t)
	return ctx, mock, issue_repo.NewIssue()
}

func TestIssueCreate(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `issues`").WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectCommit()

	err := repo.Create(ctx, &issue_entity.Issue{
		Title: "demo", State: issue_entity.StateOpen, Status: consts.ACTIVE,
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestIssueDeleteSoft(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `issues` SET").
		WithArgs(consts.DELETE, sqlmock.AnyArg(), int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Delete(ctx, 7))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestIssueFind(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `issues` WHERE id = \\? AND status = \\? ORDER BY `issues`.`id` LIMIT \\?").
		WithArgs(int64(7), consts.ACTIVE, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "title", "state", "status"}).
			AddRow(int64(7), "demo", issue_entity.StateOpen, consts.ACTIVE))

	got, err := repo.Find(ctx, 7)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(7), got.ID)
	assert.Equal(t, issue_entity.StateOpen, got.State)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestIssueFindNotFound(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `issues` WHERE id = \\? AND status = \\?").
		WithArgs(int64(99), consts.ACTIVE, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	got, err := repo.Find(ctx, 99)
	require.NoError(t, err)
	assert.Nil(t, got, "未找到时返回 nil,nil")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestIssueList_LabelFilter(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `issues` WHERE status = \\? AND id IN \\(SELECT `issue_id` FROM `issue_labels` WHERE label_id IN \\(\\?,\\?\\)\\) ORDER BY updatetime DESC, id DESC").
		WithArgs(consts.ACTIVE, int64(1), int64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "title"}).
			AddRow(int64(7), "demo"))

	rows, err := repo.List(ctx, issue_repo.ListFilter{LabelIDs: []int64{1, 2}})
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, int64(7), rows[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIssueUpdate 钉住 Update 写下的列：执行归属三列与 sync_id 都在里面 —— 少写一列
// 就是「表单存了等于没存」，而这类遗漏在别处不会让任何用例变红。
func TestIssueUpdate(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `issues` SET `agent_backend_id`=\\?,`agent_status`=\\?,`assignee_agent_id`=\\?,`body`=\\?,`closed_at`=\\?,`llm_model_key`=\\?,`llm_provider_key`=\\?,`position`=\\?,`project_id`=\\?,`session_id`=\\?,`stage`=\\?,`state`=\\?,`sync_id`=\\?,`title`=\\?,`updatetime`=\\? WHERE id = \\? AND status = \\?").
		WithArgs(
			int64(11),                    // agent_backend_id
			issue_entity.AgentStatusIdle, // agent_status
			int64(3),                     // assignee_agent_id
			"body",                       // body
			int64(0),                     // closed_at
			"gpt-5",                      // llm_model_key
			"openai",                     // llm_provider_key
			float64(0),                   // position
			int64(5),                     // project_id
			int64(0),                     // session_id
			"",                           // stage
			issue_entity.StateOpen,       // state
			sqlmock.AnyArg(),             // sync_id
			"new title",                  // title
			sqlmock.AnyArg(),             // updatetime
			int64(7),                     // id
			consts.ACTIVE,                // status
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.Update(ctx, &issue_entity.Issue{
		ID:              7,
		ProjectID:       5,
		Title:           "new title",
		Body:            "body",
		State:           issue_entity.StateOpen,
		AgentStatus:     issue_entity.AgentStatusIdle,
		AssigneeAgentID: 3,
		AgentBackendID:  11,
		LLMProviderKey:  "openai",
		LLMModelKey:     "gpt-5",
		Status:          consts.ACTIVE,
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestIssueList_PositionSort(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `issues` WHERE status = \\? AND stage = \\? ORDER BY stage, position ASC, id ASC").
		WithArgs(consts.ACTIVE, issue_entity.StageDoing).
		WillReturnRows(sqlmock.NewRows([]string{"id", "stage", "position"}).
			AddRow(int64(3), issue_entity.StageDoing, 10.0).
			AddRow(int64(4), issue_entity.StageDoing, 20.0))

	rows, err := repo.List(ctx, issue_repo.ListFilter{Stage: issue_entity.StageDoing, Sort: "position"})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, int64(3), rows[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestIssueStageCounts(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectQuery("SELECT stage, count\\(\\*\\) as cnt FROM `issues` WHERE status = \\? GROUP BY `stage`").
		WithArgs(consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"stage", "cnt"}).
			AddRow(issue_entity.StageTodo, int64(2)).
			AddRow(issue_entity.StageDone, int64(5)))

	got, err := repo.StageCounts(ctx, issue_repo.ListFilter{})
	require.NoError(t, err)
	assert.Equal(t, int64(2), got[issue_entity.StageTodo])
	assert.Equal(t, int64(5), got[issue_entity.StageDone])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestIssueUpdate_WritesStagePosition(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `issues` SET.*`position`.*`stage`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.Update(ctx, &issue_entity.Issue{ID: 7, Stage: issue_entity.StageDoing, Position: 12.5})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIssueReassignProject 见 SessionRepo.ReassignProject 的同名用例：WHERE 里
// 只能有 project_id，软删的 issue 也得跟着改挂（R11a）。
func TestIssueReassignProject(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `issues` SET `project_id`=\\?,`updatetime`=\\? WHERE project_id = \\?$").
		WithArgs(int64(9), sqlmock.AnyArg(), int64(4)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	require.NoError(t, repo.ReassignProject(ctx, 4, 9))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIssueList_ProjectSubtree 项目范围是一**组** id（选中项目 + 其子树），不是单个
// project_id —— 父项目的看板必须连子项目的任务一起装进来。
func TestIssueList_ProjectSubtree(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `issues` WHERE status = \\? AND project_id IN \\(\\?,\\?,\\?\\) ORDER BY updatetime DESC, id DESC").
		WithArgs(consts.ACTIVE, int64(4), int64(5), int64(6)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))

	rows, err := repo.List(ctx, issue_repo.ListFilter{ProjectIDs: []int64{4, 5, 6}})
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIssueList_UnassignedScope 「未归属」是 project_id = 0 这一档，与「全部项目」
// （不加任何 project 条件）必须分得开。
func TestIssueList_UnassignedScope(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `issues` WHERE status = \\? AND project_id IN \\(\\?\\)").
		WithArgs(consts.ACTIVE, int64(0)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))

	_, err := repo.List(ctx, issue_repo.ListFilter{ProjectIDs: []int64{0}})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIssueList_KeywordMatchesTitleBody 关键词匹配标题与描述。
func TestIssueList_KeywordMatchesTitleBody(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `issues` WHERE status = \\? AND \\(title LIKE \\? ESCAPE '\\\\' OR body LIKE \\? ESCAPE '\\\\'\\)").
		WithArgs(consts.ACTIVE, "%wire%", "%wire%").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))

	_, err := repo.List(ctx, issue_repo.ListFilter{Keyword: "wire"})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIssueList_KeywordMatchesIssueNumber 输入 `#179` 或 `179` 都命中该条 —— 编号
// 是 id，不是正文里恰好出现的数字，所以三个条件是 OR 而不是替换。
func TestIssueList_KeywordMatchesIssueNumber(t *testing.T) {
	for _, keyword := range []string{"#179", "179"} {
		t.Run(keyword, func(t *testing.T) {
			ctx, mock, repo := setupIssueRepo(t)
			mock.ExpectQuery("SELECT \\* FROM `issues` WHERE status = \\? AND \\(title LIKE \\? ESCAPE '\\\\' OR body LIKE \\? ESCAPE '\\\\' OR id = \\?\\)").
				WithArgs(consts.ACTIVE, "%"+keyword+"%", "%"+keyword+"%", int64(179)).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(179)))

			_, err := repo.List(ctx, issue_repo.ListFilter{Keyword: keyword})
			require.NoError(t, err)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// TestIssueList_KeywordEscapesLikeWildcards 用户输入的 % / _ 是字面量,不是通配符 ——
// 否则搜一个 `_` 会把整个库都搜出来。
func TestIssueList_KeywordEscapesLikeWildcards(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `issues` WHERE status = \\? AND \\(title LIKE \\?").
		WithArgs(consts.ACTIVE, `%100\%\_done%`, `%100\%\_done%`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	_, err := repo.List(ctx, issue_repo.ListFilter{Keyword: "100%_done"})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIssueList_LabelMatchAll 「全部满足」与「任意一个」是两套语义：前者要求这个
// issue 上出现了全部被选中的标签，靠 GROUP BY + HAVING 计数表达。
func TestIssueList_LabelMatchAll(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `issues` WHERE status = \\? AND id IN \\(SELECT `issue_id` FROM `issue_labels` WHERE label_id IN \\(\\?,\\?\\) GROUP BY `issue_id` HAVING COUNT\\(DISTINCT label_id\\) = \\?\\)").
		WithArgs(consts.ACTIVE, int64(1), int64(2), 2).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))

	_, err := repo.List(ctx, issue_repo.ListFilter{LabelIDs: []int64{1, 2}, LabelMatchAll: true})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIssueList_NoLabelOnly 「只看没有标签的」。
func TestIssueList_NoLabelOnly(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `issues` WHERE status = \\? AND id NOT IN \\(SELECT `issue_id` FROM `issue_labels`\\)").
		WithArgs(consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))

	_, err := repo.List(ctx, issue_repo.ListFilter{NoLabel: true})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIssueList_TimeRanges 更新时间与创建时间各是一段闭区间（0 = 该端不限）。
func TestIssueList_TimeRanges(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `issues` WHERE status = \\? AND updatetime >= \\? AND updatetime <= \\? AND createtime >= \\? AND createtime <= \\?").
		WithArgs(consts.ACTIVE, int64(10), int64(20), int64(30), int64(40)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	_, err := repo.List(ctx, issue_repo.ListFilter{
		UpdatedFrom: 10, UpdatedTo: 20, CreatedFrom: 30, CreatedTo: 40,
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIssueList_DoneRetention 「已完成保留多久」只裁剪已完成那一列，未完成的卡片
// 一张都不能因为它消失；已完成但没记下关闭时间的历史行退回 updatetime，不然会被
// 保留窗口静默吞掉。
func TestIssueList_DoneRetention(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `issues` WHERE status = \\? AND \\(stage <> \\? OR \\(CASE WHEN closed_at > 0 THEN closed_at ELSE updatetime END\\) >= \\?\\)").
		WithArgs(consts.ACTIVE, issue_entity.StageDone, int64(555)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	_, err := repo.List(ctx, issue_repo.ListFilter{DoneAfter: 555})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIssueStageCounts_HonorsTheWholeFilter 列头的「命中」数与列表用同一套条件算，
// 否则计数会和眼前的卡片对不上。
func TestIssueStageCounts_HonorsTheWholeFilter(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectQuery("SELECT stage, count\\(\\*\\) as cnt FROM `issues` WHERE status = \\? AND project_id IN \\(\\?\\) AND \\(title LIKE \\? ESCAPE '\\\\' OR body LIKE \\? ESCAPE '\\\\'\\) AND id IN \\(SELECT `issue_id` FROM `issue_labels` WHERE label_id IN \\(\\?\\)\\) GROUP BY `stage`").
		WithArgs(consts.ACTIVE, int64(4), "%x%", "%x%", int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"stage", "cnt"}).AddRow(issue_entity.StageTodo, int64(2)))

	got, err := repo.StageCounts(ctx, issue_repo.ListFilter{
		ProjectIDs: []int64{4}, Keyword: "x", LabelIDs: []int64{1},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), got[issue_entity.StageTodo])
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIssueCountUnfinishedByProject 项目选择器每一项右侧的计数：**未完成**的任务数，
// 按 project_id 分组（0 = 未归属），子树汇总与筛选无关，都由 service 在上面做。
func TestIssueCountUnfinishedByProject(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectQuery("SELECT project_id, count\\(\\*\\) as cnt FROM `issues` WHERE status = \\? AND stage <> \\? GROUP BY `project_id`").
		WithArgs(consts.ACTIVE, issue_entity.StageDone).
		WillReturnRows(sqlmock.NewRows([]string{"project_id", "cnt"}).
			AddRow(int64(0), int64(3)).
			AddRow(int64(4), int64(2)))

	got, err := repo.CountUnfinishedByProject(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), got[0])
	assert.Equal(t, int64(2), got[4])
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIssueCreate_GeneratesSyncID 行创建时就地生成同步标识（R1）。
func TestIssueCreate_GeneratesSyncID(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `issues`").WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectCommit()

	issue := &issue_entity.Issue{Title: "demo", State: issue_entity.StateOpen, Status: consts.ACTIVE}
	require.NoError(t, repo.Create(ctx, issue))
	assert.NotEmpty(t, issue.SyncID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIssueCreate_PreservesAnIncomingSyncID 下行落地的任务必须沿用源端的同步标识
// （R1：标识终身不变）。这一条正是 issue / label 不需要 issue_labels 那样一个专用
// UpsertFromSync 的理由：它们有自增主键，同步层用 FindRow / FindLocalID 就能按标识
// 认出本机那一行，而 Create 只在标识为空时才生成——把标识先填好交给它，它原样落库。
func TestIssueCreate_PreservesAnIncomingSyncID(t *testing.T) {
	ctx, mock, repo := setupIssueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `issues`").WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectCommit()

	issue := &issue_entity.Issue{Title: "from another machine", State: issue_entity.StateOpen, Status: consts.ACTIVE}
	issue.SyncID = "issue-from-peer"
	require.NoError(t, repo.Create(ctx, issue))
	assert.Equal(t, "issue-from-peer", issue.SyncID, "重新铸一个标识会让同一个任务在账号里变成两份")
	assert.NoError(t, mock.ExpectationsWereMet())
}

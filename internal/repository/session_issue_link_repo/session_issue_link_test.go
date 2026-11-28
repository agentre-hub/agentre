package session_issue_link_repo_test

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/repository/session_issue_link_repo"
)

var linkCols = []string{"id", "session_id", "issue_id", "createtime"}

// Given 一对未关联的(会话,任务), When 关联, Then 写出一行并带建行时间;
// 重复关联靠冲突忽略保证幂等(不报错)。
func TestSessionIssueLink_LinkInsertsAndIsIdempotent(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	for range 2 {
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO `session_issue_links` .*(ON CONFLICT DO NOTHING|ON DUPLICATE KEY)").
			WithArgs(int64(3), int64(8), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectCommit()
	}

	repo := session_issue_link_repo.NewSessionIssueLink()
	require.NoError(t, repo.Link(ctx, 3, 8))
	require.NoError(t, repo.Link(ctx, 3, 8))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionIssueLink_LinkReturnsDatabaseError(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `session_issue_links`").WillReturnError(errors.New("disk full"))
	mock.ExpectRollback()

	assert.Error(t, session_issue_link_repo.NewSessionIssueLink().Link(ctx, 3, 8))
}

// 取消关联只删这一对;没有这一对时不是错误(幂等)。
func TestSessionIssueLink_UnlinkDeletesOnlyThePairAndIsIdempotent(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	for _, affected := range []int64{1, 0} {
		mock.ExpectBegin()
		mock.ExpectExec("DELETE FROM `session_issue_links` WHERE session_id = \\? AND issue_id = \\?").
			WithArgs(int64(3), int64(8)).
			WillReturnResult(sqlmock.NewResult(0, affected))
		mock.ExpectCommit()
	}

	repo := session_issue_link_repo.NewSessionIssueLink()
	require.NoError(t, repo.Unlink(ctx, 3, 8))
	require.NoError(t, repo.Unlink(ctx, 3, 8))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionIssueLink_UnlinkReturnsDatabaseError(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `session_issue_links`").WillReturnError(errors.New("locked"))
	mock.ExpectRollback()

	assert.Error(t, session_issue_link_repo.NewSessionIssueLink().Unlink(ctx, 3, 8))
}

// 按会话列:只取这个会话的行,先关联的在前。
func TestSessionIssueLink_ListBySessionReturnsThatSessionsLinksOldestFirst(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectQuery("SELECT \\* FROM `session_issue_links` WHERE session_id = \\? ORDER BY id ASC").
		WithArgs(int64(3)).
		WillReturnRows(sqlmock.NewRows(linkCols).AddRow(1, 3, 8, 100).AddRow(2, 3, 9, 200))

	got, err := session_issue_link_repo.NewSessionIssueLink().ListBySession(ctx, 3)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.EqualValues(t, 8, got[0].IssueID)
	assert.EqualValues(t, 9, got[1].IssueID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionIssueLink_ListBySessionReturnsDatabaseError(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectQuery("SELECT \\* FROM `session_issue_links`").WillReturnError(errors.New("boom"))

	_, err := session_issue_link_repo.NewSessionIssueLink().ListBySession(ctx, 3)
	assert.Error(t, err)
}

// 按任务列:一个任务关联的多个会话。
// 看板一次取全部卡片的关联:一条 IN 查询,先关联的在前。
func TestSessionIssueLink_ListByIssuesReturnsTheirLinksOldestFirstInOneQuery(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectQuery("SELECT \\* FROM `session_issue_links` WHERE issue_id IN \\(\\?,\\?\\) ORDER BY id ASC").
		WithArgs(int64(8), int64(9)).
		WillReturnRows(sqlmock.NewRows(linkCols).AddRow(1, 3, 8, 100).AddRow(2, 6, 9, 200).AddRow(4, 5, 8, 300))

	got, err := session_issue_link_repo.NewSessionIssueLink().ListByIssues(ctx, []int64{8, 9})
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.EqualValues(t, 3, got[0].SessionID)
	assert.EqualValues(t, 6, got[1].SessionID)
	assert.EqualValues(t, 5, got[2].SessionID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 没有任务 → 不查库。
func TestSessionIssueLink_ListByIssuesEmptyDoesNotQuery(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	got, err := session_issue_link_repo.NewSessionIssueLink().ListByIssues(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionIssueLink_ListByIssuesReturnsDatabaseError(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectQuery("SELECT \\* FROM `session_issue_links`").WillReturnError(errors.New("boom"))

	_, err := session_issue_link_repo.NewSessionIssueLink().ListByIssues(ctx, []int64{8})
	assert.Error(t, err)
}

// 批量数每个任务的关联会话数(「另有 N 个会话」的分母):一次分组查询,没有关联的任务不在结果里。
func TestSessionIssueLink_CountByIssuesGroupsInOneQuery(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectQuery("SELECT issue_id, COUNT\\(\\*\\) AS n FROM `session_issue_links` WHERE issue_id IN \\(\\?,\\?\\) GROUP BY `issue_id`").
		WithArgs(int64(8), int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "n"}).AddRow(8, 3))

	got, err := session_issue_link_repo.NewSessionIssueLink().CountByIssues(ctx, []int64{8, 9})
	require.NoError(t, err)
	assert.Equal(t, map[int64]int64{8: 3}, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 空入参不发 SQL。
func TestSessionIssueLink_CountByIssuesEmptyInputSkipsTheQuery(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	got, err := session_issue_link_repo.NewSessionIssueLink().CountByIssues(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionIssueLink_CountByIssuesReturnsDatabaseError(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectQuery("SELECT issue_id, COUNT").WillReturnError(errors.New("boom"))

	_, err := session_issue_link_repo.NewSessionIssueLink().CountByIssues(ctx, []int64{8})
	assert.Error(t, err)
}

// 会话被删除:清掉它的全部关联,别的会话的不动。
func TestSessionIssueLink_DeleteBySessionRemovesAllOfThatSession(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `session_issue_links` WHERE session_id = \\?").
		WithArgs(int64(3)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	require.NoError(t, session_issue_link_repo.NewSessionIssueLink().DeleteBySession(ctx, 3))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionIssueLink_DeleteBySessionReturnsDatabaseError(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `session_issue_links`").WillReturnError(errors.New("locked"))
	mock.ExpectRollback()

	assert.Error(t, session_issue_link_repo.NewSessionIssueLink().DeleteBySession(ctx, 3))
}

// 任务被删除:清掉它的全部关联。
func TestSessionIssueLink_DeleteByIssueRemovesAllOfThatIssue(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `session_issue_links` WHERE issue_id = \\?").
		WithArgs(int64(8)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	require.NoError(t, session_issue_link_repo.NewSessionIssueLink().DeleteByIssue(ctx, 8))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionIssueLink_DeleteByIssueReturnsDatabaseError(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `session_issue_links`").WillReturnError(errors.New("locked"))
	mock.ExpectRollback()

	assert.Error(t, session_issue_link_repo.NewSessionIssueLink().DeleteByIssue(ctx, 8))
}

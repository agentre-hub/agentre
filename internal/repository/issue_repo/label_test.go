package issue_repo_test

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/issue_entity"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo"
)

func TestLabelList(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := issue_repo.NewLabel()
	mock.ExpectQuery("SELECT \\* FROM `labels`").
		WithArgs(consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "tone"}).
			AddRow(int64(2), "bug", "bug"))

	got, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "bug", got[0].Name)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLabelListByIDs(t *testing.T) {
	t.Run("returns matching labels", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)
		repo := issue_repo.NewLabel()
		mock.ExpectQuery("SELECT \\* FROM `labels` WHERE id IN \\(\\?,\\?\\) AND status = \\?").
			WithArgs(int64(1), int64(2), consts.ACTIVE).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "tone"}).
				AddRow(int64(1), "feature", "feature").
				AddRow(int64(2), "bug", "bug"))

		got, err := repo.ListByIDs(ctx, []int64{1, 2})
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "feature", got[0].Name)
		assert.Equal(t, "bug", got[1].Name)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty ids returns nil with no query", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)
		repo := issue_repo.NewLabel()

		got, err := repo.ListByIDs(ctx, []int64{})
		require.NoError(t, err)
		assert.Nil(t, got)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestLabelFindNotFound(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := issue_repo.NewLabel()
	mock.ExpectQuery("SELECT \\* FROM `labels` WHERE id = \\? AND status = \\? ORDER BY `labels`.`id` LIMIT \\?").
		WithArgs(int64(99), consts.ACTIVE, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	got, err := repo.Find(ctx, 99)
	require.NoError(t, err)
	assert.Nil(t, got, "not found should return nil,nil")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestIssueLabelSetLabels(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := issue_repo.NewIssueLabel()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `issue_labels` WHERE issue_id = \\?").
		WithArgs(int64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label_id", "sync_id"}))
	mock.ExpectExec("INSERT INTO `issue_labels`").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	require.NoError(t, repo.SetLabels(ctx, 5, []int64{1, 2}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestIssueLabelSetLabels_DeduplicatesLabelIDs(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := issue_repo.NewIssueLabel()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `issue_labels` WHERE issue_id = \\?").
		WithArgs(int64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label_id", "sync_id"}))
	// 关联行带上同步元数据六列，因此每行 8 个参数；两个标签 id 各写一行，重复的
	// 那个被去重掉。
	mock.ExpectExec("INSERT INTO `issue_labels` \\(`issue_id`,`label_id`,`sync_id`,`sync_account_id`,`sync_version`,`sync_updated_at`,`sync_origin_fingerprint`,`sync_deleted_at`\\)").
		WithArgs(
			int64(5), int64(1), sqlmock.AnyArg(), int64(0), int64(0), int64(0), "", int64(0),
			int64(5), int64(2), sqlmock.AnyArg(), int64(0), int64(0), int64(0), "", int64(0),
		).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	require.NoError(t, repo.SetLabels(ctx, 5, []int64{1, 1, 2}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestIssueLabelListByIssue(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := issue_repo.NewIssueLabel()
	mock.ExpectQuery("SELECT `label_id` FROM `issue_labels` WHERE issue_id = \\? ORDER BY label_id ASC").
		WithArgs(int64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"label_id"}).
			AddRow(int64(1)).
			AddRow(int64(3)))

	ids, err := repo.ListByIssue(ctx, 5)
	require.NoError(t, err)
	assert.Equal(t, []int64{1, 3}, ids)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestIssueLabelListByIssues(t *testing.T) {
	t.Run("groups rows into map", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)
		repo := issue_repo.NewIssueLabel()
		mock.ExpectQuery("SELECT \\* FROM `issue_labels` WHERE issue_id IN \\(\\?,\\?\\) ORDER BY issue_id ASC, label_id ASC").
			WithArgs(int64(5), int64(6)).
			WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label_id"}).
				AddRow(int64(5), int64(1)).
				AddRow(int64(5), int64(2)).
				AddRow(int64(6), int64(1)))

		got, err := repo.ListByIssues(ctx, []int64{5, 6})
		require.NoError(t, err)
		assert.Equal(t, map[int64][]int64{5: {1, 2}, 6: {1}}, got)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty input returns empty map with no query", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)
		repo := issue_repo.NewIssueLabel()

		got, err := repo.ListByIssues(ctx, []int64{})
		require.NoError(t, err)
		assert.Equal(t, map[int64][]int64{}, got)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestLabelCreate 标签目录不再只读：新建一行并就地生成同步标识（R1）。
func TestLabelCreate(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := issue_repo.NewLabel()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `labels`").WillReturnResult(sqlmock.NewResult(9, 1))
	mock.ExpectCommit()

	label := &issue_entity.Label{Name: "wire", Tone: issue_entity.ToneBlue, Status: consts.ACTIVE}
	require.NoError(t, repo.Create(ctx, label))
	assert.NotEmpty(t, label.SyncID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestLabelUpdate 改名与换色是同一次写入（两者都只改标签本身，不动任何任务）。
func TestLabelUpdate(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := issue_repo.NewLabel()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `labels` SET `name`=\\?,`sync_id`=\\?,`tone`=\\?,`updatetime`=\\? WHERE id = \\? AND status = \\?").
		WithArgs("wire", sqlmock.AnyArg(), issue_entity.ToneViolet, sqlmock.AnyArg(), int64(9), consts.ACTIVE).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Update(ctx, &issue_entity.Label{
		ID: 9, Name: "wire", Tone: issue_entity.ToneViolet,
	}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestLabelDeleteSoft 删除是软删（labels.status 已有该语义），行留在库里。
func TestLabelDeleteSoft(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := issue_repo.NewLabel()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `labels` SET `status`=\\?,`updatetime`=\\? WHERE id = \\?").
		WithArgs(consts.DELETE, sqlmock.AnyArg(), int64(9)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Delete(ctx, 9))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestLabelFindByName 重名判定只看 active 行 —— 软删掉的名字应该能被重新用起来。
func TestLabelFindByName(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := issue_repo.NewLabel()
	mock.ExpectQuery("SELECT \\* FROM `labels` WHERE name = \\? AND status = \\?").
		WithArgs("bug", consts.ACTIVE, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(int64(2), "bug"))

	got, err := repo.FindByName(ctx, "bug")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(2), got.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLabelFindByNameNotFound(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := issue_repo.NewLabel()
	mock.ExpectQuery("SELECT \\* FROM `labels` WHERE name = \\? AND status = \\?").
		WithArgs("nope", consts.ACTIVE, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	got, err := repo.FindByName(ctx, "nope")
	require.NoError(t, err)
	assert.Nil(t, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIssueLabelDeleteByLabel 软删一个标签时把它从全部任务上摘掉 —— 这正是删除前
// 要说清的爆炸半径（「这个标签会从 N 个任务上移除」），任务本身不受影响。
func TestIssueLabelDeleteByLabel(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := issue_repo.NewIssueLabel()
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `issue_labels` WHERE label_id = \\?").
		WithArgs(int64(9)).WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	require.NoError(t, repo.DeleteByLabel(ctx, 9))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIssueLabelCountByLabel 「被 N 个任务使用」只数还在的任务：软删掉的任务不该
// 撑大爆炸半径。
func TestIssueLabelCountByLabel(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := issue_repo.NewIssueLabel()
	mock.ExpectQuery("SELECT label_id, count\\(\\*\\) as cnt FROM `issue_labels` JOIN issues ON issues.id = issue_labels.issue_id WHERE issues.status = \\? GROUP BY `label_id`").
		WithArgs(consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"label_id", "cnt"}).
			AddRow(int64(2), int64(4)).
			AddRow(int64(3), int64(1)))

	got, err := repo.CountByLabel(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(4), got[2])
	assert.Equal(t, int64(1), got[3])
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIssueLabelUpsertFromSync 下行落地一条关联行时必须沿用源端的同步标识：重新
// 生成一个会让同一件事在账号里变成两份，两端从此各挂各的。同一对 (issue, label)
// 已经有行时只把标识对上，不再插一行——联合主键上硬插会撞。
func TestIssueLabelUpsertFromSync(t *testing.T) {
	t.Run("creates the row carrying the incoming sync id", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)
		repo := issue_repo.NewIssueLabel()
		mock.ExpectQuery("SELECT \\* FROM `issue_labels` WHERE issue_id = \\? AND label_id = \\?").
			WithArgs(int64(3), int64(9), 1).
			WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label_id", "sync_id"}))
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO `issue_labels`").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		row := &issue_entity.IssueLabel{IssueID: 3, LabelID: 9}
		row.SyncID = "link-1"
		require.NoError(t, repo.UpsertFromSync(ctx, row))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("adopts the identifier of a row already on that pair", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)
		repo := issue_repo.NewIssueLabel()
		mock.ExpectQuery("SELECT \\* FROM `issue_labels` WHERE issue_id = \\? AND label_id = \\?").
			WithArgs(int64(3), int64(9), 1).
			WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label_id", "sync_id"}).
				AddRow(int64(3), int64(9), "link-local"))
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE `issue_labels` SET `sync_id`=\\? WHERE issue_id = \\? AND label_id = \\?").
			WithArgs("link-1", int64(3), int64(9)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		row := &issue_entity.IssueLabel{IssueID: 3, LabelID: 9}
		row.SyncID = "link-1"
		require.NoError(t, repo.UpsertFromSync(ctx, row))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("without a sync id it is not a sync landing at all", func(t *testing.T) {
		ctx, _, mock := testutils.Database(t)
		require.NoError(t, issue_repo.NewIssueLabel().UpsertFromSync(ctx,
			&issue_entity.IssueLabel{IssueID: 3, LabelID: 9}))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestIssueLabelDeleteBySyncID 墓碑到达时按同步标识删：关联表是 (issue_id,
// label_id) 联合主键，本地 ID 在另一台机器上指向完全不同的两行，指认不了它。
func TestIssueLabelDeleteBySyncID(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := issue_repo.NewIssueLabel()
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `issue_labels` WHERE sync_id = \\?").
		WithArgs("link-1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.DeleteBySyncID(ctx, "link-1"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestLabelCreate_PreservesAnIncomingSyncID 与任务同理：标签有自增主键，同步层按
// 标识就能认出本机那一行，Create 只在标识为空时生成，因此下行落地的标签原样沿用
// 源端那一个（这也是种子标签能按名字在两台机器上收敛成同一个对象的前提）。
func TestLabelCreate_PreservesAnIncomingSyncID(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := issue_repo.NewLabel()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `labels`").WillReturnResult(sqlmock.NewResult(9, 1))
	mock.ExpectCommit()

	label := &issue_entity.Label{Name: "wire", Tone: issue_entity.ToneBlue, Status: consts.ACTIVE}
	label.SyncID = "label-from-peer"
	require.NoError(t, repo.Create(ctx, label))
	assert.Equal(t, "label-from-peer", label.SyncID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIssueLabelSetLabels_KeepsSurvivorsAndTheirSyncIDs 改标签只能动真正变化的那
// 几行：留下来的关联行**原样不动**。整批删掉再重建会给每个幸存者铸一个新的同步
// 标识，账号里于是堆出一串孤儿旧对象 + 一串重复新对象（标识终身不变，R1）。
func TestIssueLabelSetLabels_KeepsSurvivorsAndTheirSyncIDs(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := issue_repo.NewIssueLabel()
	mock.ExpectBegin()
	// 先看清现状：1 与 2 已经挂着，各自带着自己的同步标识。
	mock.ExpectQuery("SELECT \\* FROM `issue_labels` WHERE issue_id = \\?").
		WithArgs(int64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label_id", "sync_id"}).
			AddRow(int64(5), int64(1), "link-1").
			AddRow(int64(5), int64(2), "link-2"))
	// 只删被摘掉的那一个（2），不是「这条任务的全部关联行」。
	mock.ExpectExec("DELETE FROM `issue_labels` WHERE issue_id = \\? AND label_id IN \\(\\?\\)").
		WithArgs(int64(5), int64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
	// 只插新增的那一个（3）；幸存的 1 一行写入都没有，它那个标识因此原封不动。
	mock.ExpectExec("INSERT INTO `issue_labels`").
		WithArgs(int64(5), int64(3), sqlmock.AnyArg(), int64(0), int64(0), int64(0), "", int64(0)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.SetLabels(ctx, 5, []int64{1, 3}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIssueLabelSetLabels_NothingChangedWritesNothing 没有增删就不该有写入：一次
// 无关标签的编辑（只改标题）不能把关联行搅动一遍。
func TestIssueLabelSetLabels_NothingChangedWritesNothing(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := issue_repo.NewIssueLabel()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `issue_labels` WHERE issue_id = \\?").
		WithArgs(int64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label_id", "sync_id"}).
			AddRow(int64(5), int64(1), "link-1"))
	mock.ExpectCommit()

	require.NoError(t, repo.SetLabels(ctx, 5, []int64{1}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestIssueLabelListRowsByIssue 整行 lister：摘掉一个标签要凭那一行**自己的**同步
// 标识落墓碑（关联表是硬删，R6），只回 label_id 的 ListByIssue 拿不出标识。
func TestIssueLabelListRowsByIssue(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := issue_repo.NewIssueLabel()
	mock.ExpectQuery("SELECT \\* FROM `issue_labels` WHERE issue_id = \\? ORDER BY label_id ASC").
		WithArgs(int64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label_id", "sync_id", "sync_version"}).
			AddRow(int64(5), int64(1), "link-1", int64(7)).
			AddRow(int64(5), int64(2), "link-2", int64(8)))

	rows, err := repo.ListRowsByIssue(ctx, 5)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, int64(1), rows[0].LabelID)
	assert.Equal(t, "link-1", rows[0].SyncID)
	assert.Equal(t, int64(8), rows[1].SyncVersion)
	assert.NoError(t, mock.ExpectationsWereMet())
}

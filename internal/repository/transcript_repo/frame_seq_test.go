package transcript_repo_test

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
)

// 取号从本会话台账的末尾接着往下走,并且**在同一个事务里落库** —— 分配与落库不可分,
// 落库失败即没有分配,调用方据此不发布这一帧(spec「帧编号」)。
func TestFrameSeqRepo_AllocateContinuesFromTheSessionTailInOneTransaction(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COALESCE\\(MAX\\(seq\\), 0\\) FROM `chat_frame_seqs` WHERE session_id = \\?").
		WithArgs(int64(41)).
		WillReturnRows(sqlmock.NewRows([]string{"seq"}).AddRow(int64(7)))
	mock.ExpectExec("INSERT INTO `chat_frame_seqs`").
		WithArgs(
			int64(41), int64(92), 3, 0, int64(8),
			int64(41), int64(92), 3, 1, int64(9),
			int64(41), int64(92), -1, 0, int64(10),
		).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	seqs, err := transcript_repo.NewFrameSeq().Allocate(ctx, 41, []transcript_repo.FrameKey{
		{MessageID: 92, BlockIdx: 3, Ordinal: 0},
		{MessageID: 92, BlockIdx: 3, Ordinal: 1},
		{MessageID: 92, BlockIdx: -1, Ordinal: 0},
	})

	require.NoError(t, err)
	assert.Equal(t, []int64{8, 9, 10}, seqs)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 一个号都不要时不发查询:没被访问过的对话不付出任何代价。
func TestFrameSeqRepo_AllocateWithoutKeysIssuesNoQuery(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	seqs, err := transcript_repo.NewFrameSeq().Allocate(ctx, 41, nil)

	require.NoError(t, err)
	assert.Empty(t, seqs)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 同一个帧位置被原地修补过多次时台账里有多行:交回 seq 最大的那一行 —— 它才是该位置
// **当前内容**的号,也正是对端最后一次实时收到的那个号。
func TestFrameSeqRepo_LoadKeepsTheLatestSeqPerFramePosition(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `chat_frame_seqs` WHERE session_id = \\? ORDER BY seq ASC").
		WithArgs(int64(41)).
		WillReturnRows(sqlmock.NewRows([]string{"session_id", "message_id", "block_idx", "ordinal", "seq"}).
			AddRow(int64(41), int64(92), 0, 0, int64(1)).
			AddRow(int64(41), int64(92), 1, 0, int64(2)).
			AddRow(int64(41), int64(92), 1, 0, int64(5)))

	ledger, err := transcript_repo.NewFrameSeq().Load(ctx, 41)

	require.NoError(t, err)
	assert.Equal(t, map[transcript_repo.FrameKey]int64{
		{MessageID: 92, BlockIdx: 0, Ordinal: 0}: 1,
		{MessageID: 92, BlockIdx: 1, Ordinal: 0}: 5,
	}, ledger)
	require.NoError(t, mock.ExpectationsWereMet())
}

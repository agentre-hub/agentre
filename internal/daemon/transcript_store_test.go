package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/daemon/repository/session_repo"
	"github.com/agentre-hub/agentre/internal/daemon/repository/session_repo/mock_session_repo"
	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/pkg/transcript"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo/mock_transcript_repo"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

const (
	storeTestConversationID = "conv-1"
	storeTestSessionID      = int64(7)
	storeTestPeer           = devicefp.Initiator("peerA")
)

var errStoreTestBoom = errors.New("boom")

type transcriptStoreFixture struct {
	sqlmock   sqlmock.Sqlmock
	sessions  *mock_session_repo.MockSessionRepo
	messages  *mock_transcript_repo.MockMessageRepo
	frameSeqs *mock_transcript_repo.MockFrameSeqRepo
	store     transcriptStore
	purger    transcriptPurger
}

// newTestTranscriptPorts 按 New 的接法造出同一台 daemon 上的写入侧与删除侧。
func newTestTranscriptPorts(gdb *gorm.DB) (transcriptStore, transcriptPurger) {
	backlog := newBacklogMemo()
	return transcriptStore{db: gdb, backlog: backlog}, transcriptPurger{db: gdb, backlog: backlog}
}

// setupTranscriptStoreTest 注入会话、消息与帧台账三个仓储的 mock。事务边界本身落在
// sqlmock 上(StartTurn 的 BEGIN/COMMIT),消息的写入与读回都由 mock 应答。
func setupTranscriptStoreTest(t *testing.T) (context.Context, *transcriptStoreFixture) {
	t.Helper()
	ctrl := gomock.NewController(t)
	ctx, gdb, mock := testutils.Database(t)
	f := &transcriptStoreFixture{
		sqlmock:   mock,
		sessions:  mock_session_repo.NewMockSessionRepo(ctrl),
		messages:  mock_transcript_repo.NewMockMessageRepo(ctrl),
		frameSeqs: mock_transcript_repo.NewMockFrameSeqRepo(ctrl),
	}
	prevSession, prevMessage, prevFrameSeq := session_repo.Session(), transcript_repo.Message(), transcript_repo.FrameSeq()
	t.Cleanup(func() {
		session_repo.RegisterSession(prevSession)
		transcript_repo.RegisterMessage(prevMessage)
		transcript_repo.RegisterFrameSeq(prevFrameSeq)
	})
	session_repo.RegisterSession(f.sessions)
	transcript_repo.RegisterMessage(f.messages)
	transcript_repo.RegisterFrameSeq(f.frameSeqs)
	f.store, f.purger = newTestTranscriptPorts(gdb)

	f.sessions.EXPECT().LocalID(gomock.Any(), storeTestConversationID).Return(storeTestSessionID, nil).AnyTimes()
	f.messages.EXPECT().NextSeq(gomock.Any(), storeTestSessionID).Return(1, nil).AnyTimes()
	var lastID int64
	f.messages.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, m *transcript_entity.Message) error {
			lastID++
			m.ID = lastID
			return nil
		}).AnyTimes()
	return ctx, f
}

// expectBacklogReads 声明整段读回转录的次数 —— 它就是这组用例要守的东西。
func (f *transcriptStoreFixture) expectBacklogReads(times int) {
	f.messages.EXPECT().List(gomock.Any(), storeTestSessionID).Return([]*transcript_entity.Message{}, nil).Times(times)
}

// startTurn 带着用户那一句开一轮,交回这一轮的 assistant。
func (f *transcriptStoreFixture) startTurn(t *testing.T, ctx context.Context) *transcript_entity.Message {
	t.Helper()
	f.sqlmock.ExpectBegin()
	f.sqlmock.ExpectCommit()
	_, assistant, err := f.store.StartTurn(ctx, storeTestConversationID, "你好", nil, transcript.UserSource{})
	require.NoError(t, err)
	require.NotNil(t, assistant)
	return assistant
}

func (f *transcriptStoreFixture) finishTurn(t *testing.T, ctx context.Context, assistant *transcript_entity.Message) {
	t.Helper()
	f.messages.EXPECT().Update(gomock.Any(), assistant).Return(nil)
	require.NoError(t, f.store.FinishTurn(ctx, assistant))
}

// allocate 给这条消息的一帧取号;allocErr 非空时台账那一侧失败。
func (f *transcriptStoreFixture) allocate(
	t *testing.T, ctx context.Context, msg *transcript_entity.Message, allocErr error,
) {
	t.Helper()
	keys := []transcript.FrameKey{{MessageID: msg.ID, BlockIdx: transcript.MessageDerivedBlockIdx}}
	if allocErr != nil {
		f.frameSeqs.EXPECT().Allocate(gomock.Any(), storeTestSessionID, keys).Return(nil, allocErr)
		_, err := f.store.AllocateFrameSeqs(ctx, storeTestSessionID, keys)
		require.Error(t, err)
		return
	}
	f.frameSeqs.EXPECT().Allocate(gomock.Any(), storeTestSessionID, keys).Return([]int64{1}, nil)
	_, err := f.store.AllocateFrameSeqs(ctx, storeTestSessionID, keys)
	require.NoError(t, err)
}

// liveTurn 走一遍实时那一轮在转录端口上的完整形状(handlers.turnTranscript):开轮、
// 收口,收口之后那一发取号成功。
func (f *transcriptStoreFixture) liveTurn(t *testing.T, ctx context.Context) {
	t.Helper()
	assistant := f.startTurn(t, ctx)
	f.finishTurn(t, ctx, assistant)
	f.allocate(t, ctx, assistant, nil)
}

// Given 同一进程里这条会话已经完整跑过一轮(开轮时补齐过编号,收口那一发也取到了号);
// When  第二次、第三次开轮;
// Then  不再整段读回转录 —— 历史里已经没有没号的帧。
func TestTranscriptStore_GivenPreviousTurnSettled_WhenStartingLaterTurns_ThenTranscriptIsNotReadBack(t *testing.T) {
	ctx, f := setupTranscriptStoreTest(t)
	f.expectBacklogReads(1)

	f.liveTurn(t, ctx)
	f.liveTurn(t, ctx)
	f.startTurn(t, ctx)

	assert.NoError(t, f.sqlmock.ExpectationsWereMet())
}

// Given 上一轮之后,转录里可能留下了没号的帧;
// When  再开一轮;
// Then  照旧整段读回补齐编号 —— 否则新一轮先占掉小号,历史被编到它后面。
func TestTranscriptStore_GivenPreviousTurnMayLeaveUnnumberedFrames_WhenStartingNextTurn_ThenBacklogIsReadAgain(t *testing.T) {
	cases := []struct {
		name     string
		previous func(t *testing.T, ctx context.Context, f *transcriptStoreFixture)
	}{
		{
			name: "轮内有一批帧取号失败,收口那一发取号成功",
			previous: func(t *testing.T, ctx context.Context, f *transcriptStoreFixture) {
				assistant := f.startTurn(t, ctx)
				f.allocate(t, ctx, assistant, errStoreTestBoom)
				f.finishTurn(t, ctx, assistant)
				f.allocate(t, ctx, assistant, nil)
			},
		},
		{
			name: "收口之后没有人取号(导入回放只落库不发布)",
			previous: func(t *testing.T, ctx context.Context, f *transcriptStoreFixture) {
				assistant := f.startTurn(t, ctx)
				f.finishTurn(t, ctx, assistant)
			},
		},
		{
			name: "轮内取过号但这一轮没有收口",
			previous: func(t *testing.T, ctx context.Context, f *transcriptStoreFixture) {
				assistant := f.startTurn(t, ctx)
				f.allocate(t, ctx, assistant, nil)
			},
		},
		{
			name: "完整跑完一轮之后会话被删",
			previous: func(t *testing.T, ctx context.Context, f *transcriptStoreFixture) {
				f.liveTurn(t, ctx)
				f.sessions.EXPECT().Find(gomock.Any(), storeTestPeer, storeTestConversationID).
					Return(&session_repo.DaemonSession{ID: storeTestSessionID}, nil)
				f.sqlmock.ExpectExec("DELETE FROM `chat_message_blocks`").
					WithArgs(storeTestSessionID).WillReturnResult(sqlmock.NewResult(0, 2))
				f.frameSeqs.EXPECT().DeleteBySession(gomock.Any(), storeTestSessionID).Return(int64(1), nil)
				f.messages.EXPECT().DeleteFromSeq(gomock.Any(), storeTestSessionID, 0).Return(int64(2), nil)
				_, err := f.purger.DeleteAll(ctx, storeTestPeer, storeTestConversationID)
				require.NoError(t, err)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, f := setupTranscriptStoreTest(t)
			f.expectBacklogReads(2)

			tc.previous(t, ctx, f)
			f.startTurn(t, ctx)

			assert.NoError(t, f.sqlmock.ExpectationsWereMet())
		})
	}
}

// Given 开轮时整段读回转录失败(这一轮报错,没开成);
// When  再开一轮;
// Then  照旧整段读回 —— 没补齐成功的会话不能被记成「已补齐」。
func TestTranscriptStore_GivenBacklogReadFailed_WhenStartingNextTurn_ThenBacklogIsReadAgain(t *testing.T) {
	ctx, f := setupTranscriptStoreTest(t)
	f.messages.EXPECT().List(gomock.Any(), storeTestSessionID).Return(nil, errStoreTestBoom)
	f.expectBacklogReads(1)

	_, _, err := f.store.StartTurn(ctx, storeTestConversationID, "你好", nil, transcript.UserSource{})
	require.ErrorIs(t, err, errStoreTestBoom)
	f.liveTurn(t, ctx)
	f.startTurn(t, ctx)

	assert.NoError(t, f.sqlmock.ExpectationsWereMet())
}

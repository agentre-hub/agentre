package transcript_repo_test

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
)

// 消息版本(chat_messages.updatetime)是增量读的游标:前端带上它手上最大的版本,
// 只取版本更新的消息。游标成立的前提是**每一次**改动一条消息正文或元数据的写入都把
// 那条消息的版本推到一个新值 —— 包括轮次早已收尾之后才改写旧消息块行的那几条路
// (后台任务翻转 / 进度 / 子块追加)。少推一处,增量读就永远看不见那次改写。

// freshVersion 匹配一个「本次写入新签发」的版本:不早于期望登记时刻的毫秒时钟。
// 原样写回旧值(或干脆没写)的实现过不了它。
type freshVersion struct{ floor int64 }

func (f freshVersion) Match(v driver.Value) bool {
	got, ok := v.(int64)
	return ok && got >= f.floor
}

// expectVersionBump 期望把宿主消息的版本推到一个新值。调用方把它放在块行改写的同一个
// BEGIN … COMMIT 里:版本必须与它描述的那次改写同时可见。
func expectVersionBump(mock sqlmock.Sqlmock, messageID int64) {
	mock.ExpectExec("UPDATE `chat_messages` SET `updatetime`=\\? WHERE id = \\?").
		WithArgs(freshVersion{floor: time.Now().UnixMilli()}, messageID).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

// TestMessageRepo_ListMetaSince 钉住增量读的 SQL:版本筛选在 SQL 里,不是把整条会话
// 的元数据读回来再在内存里挑。
func TestMessageRepo_ListMetaSince(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE session_id = \\? AND updatetime > \\? ORDER BY seq ASC").
		WithArgs(int64(3), int64(500)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "role", "seq", "updatetime"}).
			AddRow(1, 3, "assistant", 1, 900).
			AddRow(7, 3, "user", 7, 650))

	got, err := transcript_repo.NewMessage().ListMetaSince(ctx, 3, 500)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, int64(900), got[0].Updatetime, "旧消息的块后来被改写过,同样在列")
	assert.Equal(t, "", got[0].BlocksJSON, "元数据查询不取正文")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestMessageRepo_ListVersions 钉住「本会话现在有哪些消息、各自什么版本」的轻量查询:
// 只取 id 与版本两列,不读整行元数据,更不碰块表。增量读据它判断删除,写过路径的
// 缓存据它判断失效。
func TestMessageRepo_ListVersions(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT id, updatetime FROM `chat_messages` WHERE session_id = \\? ORDER BY seq ASC").
		WithArgs(int64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "updatetime"}).
			AddRow(1, 900).
			AddRow(2, 400))

	got, err := transcript_repo.NewMessage().ListVersions(ctx, 3)
	require.NoError(t, err)
	assert.Equal(t, []transcript_repo.MessageVersion{{ID: 1, Version: 900}, {ID: 2, Version: 400}}, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestMessageRepo_SaveWritersIssueTheVersionInsideTheTransaction 覆盖整行 Save 的三条写入
// (轮内 checkpoint 的两种 + 收尾 Update):版本是新签发的,而且是在拿到写锁(BEGIN)
// 之后才签发 —— 先签版本再排队等锁,另一个写者可能带着更大的版本先提交、被前端读走,
// 这条带小版本的写入随后才提交,就落在了前端游标的后面,永远读不到。
func TestMessageRepo_SaveWritersIssueTheVersionInsideTheTransaction(t *testing.T) {
	row := transcript_entity.BlockRow(42, 0, cagoblocks.StoredBlock{Type: "text", Data: []byte(`{"text":"a"}`)})
	writers := map[string]func(context.Context, transcript_repo.MessageRepo, *transcript_entity.Message) error{
		"UpsertBlocks": func(ctx context.Context, r transcript_repo.MessageRepo, m *transcript_entity.Message) error {
			return r.UpsertBlocks(ctx, m, []*transcript_entity.MessageBlock{row}, -1)
		},
		"CheckpointBlocks": func(ctx context.Context, r transcript_repo.MessageRepo, m *transcript_entity.Message) error {
			return r.CheckpointBlocks(ctx, m, m.BlocksJSON)
		},
		"Update": func(ctx context.Context, r transcript_repo.MessageRepo, m *transcript_entity.Message) error {
			return r.Update(ctx, m)
		},
	}
	for name, write := range writers {
		t.Run(name+": BEGIN 失败时不签发版本", func(t *testing.T) {
			ctx, _, mock := testutils.Database(t)
			mock.ExpectBegin().WillReturnError(assert.AnError)

			m := &transcript_entity.Message{ID: 42, SessionID: 3, Role: "assistant", BlocksJSON: "[]", Seq: 1, Updatetime: 7}
			require.Error(t, write(ctx, transcript_repo.NewMessage(), m))
			assert.Equal(t, int64(7), m.Updatetime, "版本只能在持有写锁之后签发")
		})
	}
}

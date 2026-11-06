package chat_svc_test

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// 收尾 reload 改增量(spec 2026-11-05 决策 4):前端带上手上最大的消息版本
// (SinceVersion),后端只交回版本更新的消息;每条消息带自己的版本,响应带本会话当前
// 的最大版本(下一次的游标)与当前全部消息 id(删除 / 截断由此可见)。
// SinceVersion 为 0 时与改动前逐字一致。

func messageIDs(msgs []chat_svc.ChatMessage) []int64 {
	out := make([]int64, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.ID)
	}
	return out
}

func entityIDs(msgs []*chat_entity.Message) []int64 {
	out := make([]int64, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.ID)
	}
	return out
}

// versionedMessages 造 n 条元数据,版本默认都在游标(5000)之前。
func versionedMessages(sessionID int64, n int) []*chat_entity.Message {
	msgs := seqMessages(sessionID, n)
	for i, m := range msgs {
		m.Updatetime = int64(1000 + i)
	}
	return msgs
}

func expectLoadSessionShell(m *chatMocks, ctx context.Context, sessionID int64) {
	m.session.EXPECT().Find(ctx, sessionID).Return(&chat_entity.Session{ID: sessionID, AgentID: 7, Status: consts.ACTIVE}, nil)
	m.agent.EXPECT().Find(ctx, int64(7)).Return(&agent_entity.Agent{ID: 7, Status: consts.ACTIVE}, nil)
}

func TestLoadSession_SinceVersion(t *testing.T) {
	t.Run("只交回版本新于游标的消息;旧消息的块后来被改写过同样交回", func(t *testing.T) {
		m := setupChatTest(t)
		ctx := context.Background()
		total := chat_svc.TranscriptBlockWindow + 5
		msgs := versionedMessages(3, total)
		// 第一条早在窗口之外,但它承载的后台任务卡在后一轮被翻转过。
		msgs[0].Updatetime = 9000
		msgs[total-2].Updatetime = 8000
		msgs[total-1].Updatetime = 8500

		expectLoadSessionShell(m, ctx, 3)
		m.message.EXPECT().ListMeta(ctx, int64(3)).Return(msgs, nil)
		var filled []*chat_entity.Message
		m.message.EXPECT().FillBlocks(ctx, gomock.Any()).DoAndReturn(fillBody(&filled)).Times(1)

		resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 3, SinceVersion: 5000})
		require.NoError(t, err)

		assert.Equal(t, []int64{msgs[0].ID, msgs[total-2].ID, msgs[total-1].ID}, messageIDs(resp.Messages))
		assert.Equal(t, []int64{msgs[total-2].ID, msgs[total-1].ID}, entityIDs(filled),
			"正文只补「变了且在窗口里」的那几条,不重读整个窗口")
		assert.False(t, resp.Messages[0].BlocksLoaded, "窗口外的消息与全量读同一口径:只给元数据")
		assert.True(t, resp.Messages[2].BlocksLoaded)
		assert.Equal(t, int64(9000), resp.Messages[0].Version)
		assert.Equal(t, int64(9000), resp.Version, "下一次的游标是本会话当前的最大版本")
		assert.Equal(t, entityIDs(msgs), resp.MessageIDs, "当前全部消息 id:前端据此发现被删的消息")
	})

	t.Run("游标之后什么都没变 → 不补任何正文,消息为空,游标不变", func(t *testing.T) {
		m := setupChatTest(t)
		ctx := context.Background()
		msgs := versionedMessages(3, 4)

		expectLoadSessionShell(m, ctx, 3)
		m.message.EXPECT().ListMeta(ctx, int64(3)).Return(msgs, nil)
		// 严格 mock:FillBlocks 没有期望,被调用即失败。

		resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 3, SinceVersion: 1003})
		require.NoError(t, err)
		assert.Empty(t, resp.Messages)
		assert.Equal(t, int64(1003), resp.Version)
		assert.Equal(t, entityIDs(msgs), resp.MessageIDs)
	})

	t.Run("游标为 0 → 全量读照旧,并带上每条消息的版本与当前游标", func(t *testing.T) {
		m := setupChatTest(t)
		ctx := context.Background()
		msgs := versionedMessages(3, 3)

		expectLoadSessionShell(m, ctx, 3)
		m.message.EXPECT().ListMeta(ctx, int64(3)).Return(msgs, nil)
		var filled []*chat_entity.Message
		m.message.EXPECT().FillBlocks(ctx, gomock.Any()).DoAndReturn(fillBody(&filled)).Times(1)

		resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 3})
		require.NoError(t, err)
		assert.Equal(t, entityIDs(msgs), messageIDs(resp.Messages))
		assert.Len(t, filled, 3)
		assert.Equal(t, int64(1001), resp.Messages[1].Version)
		assert.Equal(t, int64(1002), resp.Version)
		assert.Equal(t, entityIDs(msgs), resp.MessageIDs)
	})
}

func TestLoadSessionBlocksByType_SinceVersion(t *testing.T) {
	t.Run("版本筛选交给 SQL,只按类型补变化的消息;两次查询之间新写入的消息留到下一次", func(t *testing.T) {
		m := setupChatTest(t)
		ctx := context.Background()
		versions := []transcript_repo.MessageVersion{{ID: 1, Version: 9000}, {ID: 2, Version: 1000}, {ID: 3, Version: 8000}}
		changed := []*chat_entity.Message{
			{ID: 1, SessionID: 6, Role: "assistant", Seq: 1, Updatetime: 9000},
			{ID: 3, SessionID: 6, Role: "assistant", Seq: 3, Updatetime: 8000},
			// ListVersions 之后才落库的一条:不在当前 id 列表里,本次不交回。
			{ID: 4, SessionID: 6, Role: "assistant", Seq: 4, Updatetime: 9500},
		}
		gomock.InOrder(
			m.message.EXPECT().ListVersions(ctx, int64(6)).Return(versions, nil),
			m.message.EXPECT().ListMetaSince(ctx, int64(6), int64(5000)).Return(changed, nil),
		)
		m.message.EXPECT().FillBlocksByType(ctx, gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, batch []*chat_entity.Message, _ []string) error {
				assert.Equal(t, []int64{1, 3}, entityIDs(batch), "只补变化且在列的消息")
				for _, b := range batch {
					b.BlocksJSON = `[{"type":"tool_use","data":{"id":"tu","name":"Write","input":{}}}]`
				}
				return nil
			}).Times(1)
		// 严格 mock:ListMeta 没有期望 —— 增量读不再读整条会话的元数据。

		resp, err := m.svc.LoadSessionBlocksByType(ctx, &chat_svc.LoadSessionBlocksByTypeRequest{
			SessionID: 6, Types: []string{chat_svc.ChatBlockTypeToolUse}, SinceVersion: 5000,
		})
		require.NoError(t, err)
		assert.Equal(t, []int64{1, 3}, messageIDs(resp.Messages))
		require.Len(t, resp.Messages[0].Blocks, 1)
		assert.False(t, resp.Messages[0].BlocksLoaded)
		assert.Equal(t, int64(9000), resp.Messages[0].Version)
		assert.Equal(t, int64(9000), resp.Version, "游标取自 id 列表那一刻,晚到的那条下次照样交回")
		assert.Equal(t, []int64{1, 2, 3}, resp.MessageIDs)
	})

	t.Run("游标为 0 → 整条会话照旧,并带上版本与 id 列表", func(t *testing.T) {
		m := setupChatTest(t)
		ctx := context.Background()
		msgs := []*chat_entity.Message{
			{ID: 1, SessionID: 6, Role: "user", Seq: 1, Updatetime: 300},
			{ID: 2, SessionID: 6, Role: "assistant", Seq: 2, Updatetime: 200},
		}
		m.message.EXPECT().ListMeta(ctx, int64(6)).Return(msgs, nil)
		m.message.EXPECT().FillBlocksByType(ctx, gomock.Len(2), gomock.Any()).Return(nil).Times(1)

		resp, err := m.svc.LoadSessionBlocksByType(ctx, &chat_svc.LoadSessionBlocksByTypeRequest{
			SessionID: 6, Types: []string{chat_svc.ChatBlockTypeToolUse},
		})
		require.NoError(t, err)
		assert.Equal(t, []int64{1, 2}, messageIDs(resp.Messages))
		assert.Equal(t, int64(300), resp.Version)
		assert.Equal(t, []int64{1, 2}, resp.MessageIDs)
	})
}

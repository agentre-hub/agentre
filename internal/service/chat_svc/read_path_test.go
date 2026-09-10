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
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// 读路径的取数形态(spec 2026-08-27 决策 6):元数据全量 + 块按需取。
//
// 这三组用例守的是「前端能看到的信息集合不缩水」——**不是**历史截断:每一条消息的
// 元数据(轮次、token 计数、错误、时间)照旧全量下发,缩掉的只是窗口外那些消息的正文,
// 而正文有两条按需取回的路:向上滚动(LoadMessageBlocks)与派生视图按类型点查
// (LoadSessionBlocksByType)。

// seqMessages 造一串只有元数据的消息(BlocksJSON 留空 = 正文还没补)。
func seqMessages(sessionID int64, n int) []*chat_entity.Message {
	msgs := make([]*chat_entity.Message, 0, n)
	for i := range n {
		msgs = append(msgs, &chat_entity.Message{
			ID: int64(i + 1), SessionID: sessionID, Role: "assistant",
			Seq: i + 1, TotalInputTokens: (i + 1) * 100,
		})
	}
	return msgs
}

// fillBody 是 FillBlocks 的 mock 实现:就地给点名的消息补一段正文。
func fillBody(captured *[]*chat_entity.Message) func(context.Context, []*chat_entity.Message) error {
	return func(_ context.Context, batch []*chat_entity.Message) error {
		*captured = batch
		for _, m := range batch {
			m.BlocksJSON = `[{"type":"text","data":{"text":"body"}}]`
		}
		return nil
	}
}

// TestLoadSession_MetadataFullBlocksWindowed:打开一条块数远超窗口的会话,取数只发
// 一次元数据查询加一次有界的正文取数;窗口外的消息元数据一条不少,但不带正文。
func TestLoadSession_MetadataFullBlocksWindowed(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	total := chat_svc.TranscriptBlockWindow + 5
	msgs := seqMessages(3, total)

	m.session.EXPECT().Find(ctx, int64(3)).Return(&chat_entity.Session{ID: 3, AgentID: 7, Status: consts.ACTIVE}, nil)
	m.agent.EXPECT().Find(ctx, int64(7)).Return(&agent_entity.Agent{ID: 7, Status: consts.ACTIVE}, nil)
	m.message.EXPECT().ListMeta(ctx, int64(3)).Return(msgs, nil)
	var filled []*chat_entity.Message
	m.message.EXPECT().FillBlocks(ctx, gomock.Any()).DoAndReturn(fillBody(&filled)).Times(1)

	resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 3})
	require.NoError(t, err)

	require.Len(t, resp.Messages, total, "元数据全量:每条消息都在,轮次与 token 计数不缩水")
	assert.Equal(t, 100, resp.Messages[0].TotalInputTokens)
	assert.Equal(t, total*100, resp.Messages[total-1].TotalInputTokens)

	require.Len(t, filled, chat_svc.TranscriptBlockWindow, "正文只取末尾一个窗口")
	assert.Equal(t, msgs[total-1].ID, filled[len(filled)-1].ID, "窗口取的是最近的那几条")

	assert.False(t, resp.Messages[0].BlocksLoaded, "窗口外的消息标明正文还没取")
	assert.Empty(t, resp.Messages[0].Blocks)
	assert.True(t, resp.Messages[total-1].BlocksLoaded)
	require.NotEmpty(t, resp.Messages[total-1].Blocks)
	assert.Equal(t, "body", resp.Messages[total-1].Blocks[0].Text)
}

// TestLoadSession_ShortSessionLoadsEverything:消息数不到窗口时一次全取,BlocksLoaded
// 全真 —— 绝大多数会话的观感与改动前逐字一致。
func TestLoadSession_ShortSessionLoadsEverything(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	msgs := seqMessages(4, 2)
	m.session.EXPECT().Find(ctx, int64(4)).Return(&chat_entity.Session{ID: 4, AgentID: 8, Status: consts.ACTIVE}, nil)
	m.agent.EXPECT().Find(ctx, int64(8)).Return(&agent_entity.Agent{ID: 8, Status: consts.ACTIVE}, nil)
	m.message.EXPECT().ListMeta(ctx, int64(4)).Return(msgs, nil)
	var filled []*chat_entity.Message
	m.message.EXPECT().FillBlocks(ctx, gomock.Any()).DoAndReturn(fillBody(&filled)).Times(1)

	resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: 4})
	require.NoError(t, err)
	assert.Len(t, filled, 2)
	for _, cm := range resp.Messages {
		assert.True(t, cm.BlocksLoaded)
	}
}

// TestLoadMessageBlocks 向上滚动:取回 beforeSeq 之前的最后一段正文,并说明还有没有
// 更早的一段可取。
func TestLoadMessageBlocks(t *testing.T) {
	t.Run("取回给定 seq 之前的一段,并报告还有更早的", func(t *testing.T) {
		m := setupChatTest(t)
		ctx := context.Background()
		m.message.EXPECT().ListMeta(ctx, int64(5)).Return(seqMessages(5, 10), nil)
		var filled []*chat_entity.Message
		m.message.EXPECT().FillBlocks(ctx, gomock.Any()).DoAndReturn(fillBody(&filled)).Times(1)

		resp, err := m.svc.LoadMessageBlocks(ctx, &chat_svc.LoadMessageBlocksRequest{
			SessionID: 5, BeforeSeq: 6, Limit: 3,
		})
		require.NoError(t, err)
		require.Len(t, resp.Messages, 3)
		assert.Equal(t, []int{3, 4, 5}, []int{resp.Messages[0].Seq, resp.Messages[1].Seq, resp.Messages[2].Seq})
		assert.True(t, resp.Messages[0].BlocksLoaded)
		assert.Equal(t, "body", resp.Messages[0].Blocks[0].Text)
		assert.True(t, resp.HasMore, "seq 1、2 还没取,前端要继续往上滚")
		assert.Len(t, filled, 3, "只对这一段发取数")
	})

	t.Run("取到会话开头 → hasMore 为假", func(t *testing.T) {
		m := setupChatTest(t)
		ctx := context.Background()
		m.message.EXPECT().ListMeta(ctx, int64(5)).Return(seqMessages(5, 10), nil)
		m.message.EXPECT().FillBlocks(ctx, gomock.Any()).Return(nil).Times(1)

		resp, err := m.svc.LoadMessageBlocks(ctx, &chat_svc.LoadMessageBlocksRequest{
			SessionID: 5, BeforeSeq: 3, Limit: 5,
		})
		require.NoError(t, err)
		require.Len(t, resp.Messages, 2)
		assert.False(t, resp.HasMore)
	})

	t.Run("已经在开头 → 空结果,不发取数", func(t *testing.T) {
		m := setupChatTest(t)
		ctx := context.Background()
		m.message.EXPECT().ListMeta(ctx, int64(5)).Return(seqMessages(5, 10), nil)

		resp, err := m.svc.LoadMessageBlocks(ctx, &chat_svc.LoadMessageBlocksRequest{
			SessionID: 5, BeforeSeq: 1, Limit: 5,
		})
		require.NoError(t, err)
		assert.Empty(t, resp.Messages)
		assert.False(t, resp.HasMore)
	})

	t.Run("limit 超过一个窗口时被钉回窗口大小", func(t *testing.T) {
		m := setupChatTest(t)
		ctx := context.Background()
		total := chat_svc.TranscriptBlockWindow * 3
		m.message.EXPECT().ListMeta(ctx, int64(5)).Return(seqMessages(5, total), nil)
		m.message.EXPECT().FillBlocks(ctx, gomock.Any()).Return(nil).Times(1)

		resp, err := m.svc.LoadMessageBlocks(ctx, &chat_svc.LoadMessageBlocksRequest{
			SessionID: 5, BeforeSeq: total, Limit: 1 << 20,
		})
		require.NoError(t, err)
		assert.Len(t, resp.Messages, chat_svc.TranscriptBlockWindow)
		assert.True(t, resp.HasMore)
	})

	t.Run("sessionId 非法 → 报错", func(t *testing.T) {
		m := setupChatTest(t)
		_, err := m.svc.LoadMessageBlocks(context.Background(), &chat_svc.LoadMessageBlocksRequest{SessionID: 0, BeforeSeq: 3})
		assert.Error(t, err)
	})
}

// TestLoadSessionBlocksByType 派生视图的取数:整条会话的元数据 + 只有点名类型的块。
// 后台任务面板要的是带 subagent 元数据的 tool_use 卡,而 subagent 累计态是**独立的
// subagent_state 块**(投影时预扫合入 tool_use)——少取这一类,面板就会把所有后台任务
// 判成不存在,正是决策 6 说的「静默算错」。
func TestLoadSessionBlocksByType(t *testing.T) {
	t.Run("tool_use 连带取 subagent_state,投影后合成一张带 subagent 的卡", func(t *testing.T) {
		m := setupChatTest(t)
		ctx := context.Background()
		msgs := []*chat_entity.Message{
			{ID: 1, SessionID: 6, Role: "user", Seq: 1},
			{ID: 2, SessionID: 6, Role: "assistant", Seq: 2},
		}
		m.message.EXPECT().ListMeta(ctx, int64(6)).Return(msgs, nil)
		m.message.EXPECT().FillBlocksByType(ctx, gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, batch []*chat_entity.Message, types []string) error {
				assert.Equal(t, []string{"tool_use", "subagent_state", "nested_tool_use"}, types)
				batch[0].BlocksJSON = "[]"
				batch[1].BlocksJSON = `[{"type":"tool_use","data":{"id":"tu1","name":"Task",` +
					`"input":{"run_in_background":true}}},` +
					`{"type":"subagent_state","data":{"parent_tool_call_id":"tu1",` +
					`"kind":"local_agent","task_id":"t1","status":"running"}}]`
				return nil
			}).Times(1)

		resp, err := m.svc.LoadSessionBlocksByType(ctx, &chat_svc.LoadSessionBlocksByTypeRequest{
			SessionID: 6, Types: []string{chat_svc.ChatBlockTypeToolUse},
		})
		require.NoError(t, err)
		require.Len(t, resp.Messages, 2, "元数据全量:轮次结构不因按类型取块而残缺")
		assert.Empty(t, resp.Messages[0].Blocks)
		require.Len(t, resp.Messages[1].Blocks, 1, "subagent_state 不作为独立块下行")
		block := resp.Messages[1].Blocks[0]
		assert.Equal(t, chat_svc.ChatBlockTypeToolUse, block.Type)
		assert.Equal(t, "tu1", block.ToolCallID)
		require.NotNil(t, block.Subagent)
		assert.Equal(t, "t1", block.Subagent.TaskID)
		assert.Equal(t, "local_agent", block.Subagent.Kind)
		assert.Equal(t, true, block.ToolInput["run_in_background"])
	})

	t.Run("点名之外的类型不下行", func(t *testing.T) {
		m := setupChatTest(t)
		ctx := context.Background()
		msgs := []*chat_entity.Message{{ID: 1, SessionID: 7, Role: "assistant", Seq: 1}}
		m.message.EXPECT().ListMeta(ctx, int64(7)).Return(msgs, nil)
		m.message.EXPECT().FillBlocksByType(ctx, gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, batch []*chat_entity.Message, types []string) error {
				assert.Equal(t, []string{"text"}, types)
				// 仓储只按类型取,这里多塞一个 thinking 块证明投影侧也筛一次 ——
				// 两道筛子同一口径,任何一道漏掉都会让前端重新拿到不该拿的正文。
				batch[0].BlocksJSON = `[{"type":"text","data":{"text":"hi"}},` +
					`{"type":"thinking","data":{"text":"secret"}}]`
				return nil
			}).Times(1)

		resp, err := m.svc.LoadSessionBlocksByType(ctx, &chat_svc.LoadSessionBlocksByTypeRequest{
			SessionID: 7, Types: []string{chat_svc.ChatBlockTypeText},
		})
		require.NoError(t, err)
		require.Len(t, resp.Messages[0].Blocks, 1)
		assert.Equal(t, "hi", resp.Messages[0].Blocks[0].Text)
	})

	t.Run("一个类型都没点名 → 报错,而不是退化成整条转录全取", func(t *testing.T) {
		m := setupChatTest(t)
		_, err := m.svc.LoadSessionBlocksByType(context.Background(), &chat_svc.LoadSessionBlocksByTypeRequest{SessionID: 7})
		assert.Error(t, err)
	})
}

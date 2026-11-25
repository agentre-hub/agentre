package chat_svc_test

import (
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

func ctlSession(id int64, conv, status string, agentID int64) *chat_entity.Session {
	return &chat_entity.Session{ID: id, ConversationID: conv, AgentID: agentID, Title: "t" + conv,
		AgentStatus: status, LastMessageAt: 1000 + id, Createtime: 500 + id, Status: consts.ACTIVE}
}

func ctlMsg(id int64, seq int, role string, bs ...blocks.ContentBlock) *chat_entity.Message {
	m := &chat_entity.Message{ID: id, SessionID: 100, Seq: seq, Role: role, Createtime: int64(seq) * 10}
	if err := m.SetBlocks(bs); err != nil {
		panic(err)
	}
	return m
}

// Given 三条会话(本机 idle、本机 running、跑在远端设备上的 waiting) When 按 status / device / limit
// 列出 Then 只留下命中的,按最近活动顺序,agent 名与设备名带齐;agent 与项目收窄下推给仓储。
func TestCtlList_FiltersStatusDeviceAndLimit(t *testing.T) {
	setup := func(t *testing.T) (*chatMocks, *[]chat_repo.SessionIndexFilter) {
		m := setupChatTest(t)
		var seen []chat_repo.SessionIndexFilter
		remote := ctlSession(3, "c3", "waiting", 7)
		remote.ExecDeviceID = 9
		all := []*chat_entity.Session{ctlSession(1, "c1", "idle", 7), ctlSession(2, "c2", "running", 7), remote}
		m.session.EXPECT().ListIndexPaged(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, f chat_repo.SessionIndexFilter, offset, _ int) ([]*chat_entity.Session, error) {
				seen = append(seen, f)
				if offset > 0 {
					return nil, nil
				}
				return all, nil
			}).AnyTimes()
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{ID: 7, Name: "planner"}, nil).AnyTimes()
		return m, &seen
	}

	t.Run("status", func(t *testing.T) {
		m, _ := setup(t)
		got, err := ctlConversations(t, m).ListCtlConversations(m.ctx, chat_svc.CtlListFilter{Status: chat_svc.CtlStatusRunning})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "c2", got[0].ConversationID)
		assert.Equal(t, "planner", got[0].AgentName)
		assert.Equal(t, "tc2", got[0].Title)
		assert.Equal(t, int64(1002), got[0].LastMessageAt)
		assert.Equal(t, int64(502), got[0].CreatedAt)
	})
	t.Run("device local keeps only this desktop's sessions", func(t *testing.T) {
		m, _ := setup(t)
		got, err := ctlConversations(t, m).ListCtlConversations(m.ctx, chat_svc.CtlListFilter{Device: "local"})
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, []string{"c1", "c2"}, []string{got[0].ConversationID, got[1].ConversationID})
	})
	t.Run("device name of a remote device", func(t *testing.T) {
		m, _ := setup(t)
		got, err := ctlConversations(t, m).ListCtlConversations(m.ctx, chat_svc.CtlListFilter{Device: "device #9"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "c3", got[0].ConversationID)
		assert.Equal(t, "device #9", got[0].Device)
	})
	t.Run("limit and agent / project push down", func(t *testing.T) {
		m, seen := setup(t)
		got, err := ctlConversations(t, m).ListCtlConversations(m.ctx, chat_svc.CtlListFilter{AgentID: 7, ProjectID: 5, Limit: 2})
		require.NoError(t, err)
		assert.Len(t, got, 2)
		require.NotEmpty(t, *seen)
		f := (*seen)[0]
		require.NotNil(t, f.AgentID)
		require.NotNil(t, f.ProjectID)
		assert.Equal(t, int64(7), *f.AgentID)
		assert.Equal(t, int64(5), *f.ProjectID)
	})
}

// Given 6 条消息(3 轮) When 取最近 2 轮 Then 从倒数第 2 个用户消息起、按时间顺序、只含文本;
// before 是第一条的 seq,还有更早的。
func TestCtlMessages_LastTurnsAndCursor(t *testing.T) {
	m := setupChatTest(t)
	desc := []*chat_entity.Message{
		ctlMsg(16, 6, "assistant", blocks.ThinkingBlock{Text: "hmm"}, blocks.TextBlock{Text: "a3"}),
		ctlMsg(15, 5, "user", blocks.TextBlock{Text: "u3"}),
		ctlMsg(14, 4, "assistant", blocks.TextBlock{Text: "a2"}),
		ctlMsg(13, 3, "user", blocks.TextBlock{Text: "u2"}),
		ctlMsg(12, 2, "assistant", blocks.TextBlock{Text: "a1"}),
		ctlMsg(11, 1, "user", blocks.TextBlock{Text: "u1"}),
	}
	m.message.EXPECT().ListMetaBefore(gomock.Any(), int64(100), gomock.Any(), gomock.Any()).Return(desc, nil)
	expectTranscriptWindowFilled(m)

	page, err := ctlConversations(t, m).ListCtlMessages(m.ctx, 100, chat_svc.CtlMessagesQuery{Last: 2})
	require.NoError(t, err)
	require.Len(t, page.Messages, 4)
	texts := make([]string, 0, len(page.Messages))
	for _, msg := range page.Messages {
		texts = append(texts, msg.Role+":"+msg.Text)
		assert.Empty(t, msg.Blocks, "default mode carries text only")
	}
	assert.Equal(t, []string{"user:u2", "assistant:a2", "user:u3", "assistant:a3"}, texts)
	assert.True(t, page.HasMore)
	assert.Equal(t, 3, page.BeforeSeq)
}

// Given 只有 1 轮 When 取最近 20 轮 Then 全部返回,没有更多。
func TestCtlMessages_FewerTurnsThanAsked(t *testing.T) {
	m := setupChatTest(t)
	m.message.EXPECT().ListMetaBefore(gomock.Any(), int64(100), gomock.Any(), gomock.Any()).Return([]*chat_entity.Message{
		ctlMsg(12, 2, "assistant", blocks.TextBlock{Text: "a1"}),
		ctlMsg(11, 1, "user", blocks.TextBlock{Text: "u1"}),
	}, nil)
	expectTranscriptWindowFilled(m)
	page, err := ctlConversations(t, m).ListCtlMessages(m.ctx, 100, chat_svc.CtlMessagesQuery{Last: 20})
	require.NoError(t, err)
	assert.Len(t, page.Messages, 2)
	assert.False(t, page.HasMore)
	assert.Equal(t, 0, page.BeforeSeq)
}

// Given --before 游标 When 翻页 Then 只查 seq 小于游标的消息。--full 带工具调用与结果、不带思考。
func TestCtlMessages_BeforeCursorAndFull(t *testing.T) {
	m := setupChatTest(t)
	m.message.EXPECT().ListMetaBefore(gomock.Any(), int64(100), 3, gomock.Any()).Return([]*chat_entity.Message{
		ctlMsg(12, 2, "assistant",
			blocks.ThinkingBlock{Text: "secret thoughts"},
			blocks.ToolUseBlock{ID: "tu1", Name: "Bash", Input: map[string]any{"command": "ls"}},
			blocks.ToolResultBlock{ToolUseID: "tu1", Content: []blocks.ContentBlock{blocks.TextBlock{Text: "file.go"}}, IsError: true},
			blocks.TextBlock{Text: "done"}),
		ctlMsg(11, 1, "user", blocks.TextBlock{Text: "u1"}),
	}, nil)
	expectTranscriptWindowFilled(m)
	page, err := ctlConversations(t, m).ListCtlMessages(m.ctx, 100, chat_svc.CtlMessagesQuery{Last: 20, BeforeSeq: 3, Full: true})
	require.NoError(t, err)
	require.Len(t, page.Messages, 2)
	asst := page.Messages[1]
	assert.Equal(t, "done", asst.Text)
	require.Len(t, asst.Blocks, 3)
	assert.Equal(t, "tool_use", asst.Blocks[0].Type)
	assert.Equal(t, "Bash", asst.Blocks[0].Name)
	assert.Equal(t, "ls", asst.Blocks[0].Input["command"])
	assert.Equal(t, "tool_result", asst.Blocks[1].Type)
	assert.Equal(t, "file.go", asst.Blocks[1].Text)
	assert.True(t, asst.Blocks[1].IsError)
	assert.Equal(t, "text", asst.Blocks[2].Type)
}

// Given 一条有用量与最终文本的会话 When 取详情 Then 带后端名、模型、累计 token 与最后一轮最终文本。
func TestCtlDetail_BackendModelTokensAndLastText(t *testing.T) {
	m := setupChatTest(t)
	sess := ctlSession(100, ctlConvID, "idle", 7)
	sess.ExecAgentBackendID = 12
	sess.ProviderKey, sess.ModelKey = "pk", "mk"
	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil).AnyTimes()
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{ID: 7, Name: "planner", AgentBackendID: 3}, nil).AnyTimes()
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{ID: 12, Name: "claude-local", Status: consts.ACTIVE}, nil)
	last := ctlMsg(21, 2, "assistant", blocks.TextBlock{Text: "the answer"})
	m.message.EXPECT().LatestAssistant(gomock.Any(), int64(100)).Return(last, nil).AnyTimes()
	m.message.EXPECT().ListMeta(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
		{ID: 20, Role: "user"},
		{ID: 21, Role: "assistant", TotalInputTokens: 100, CompletionTokens: 7},
		{ID: 22, Role: "assistant", TotalInputTokens: 50, CompletionTokens: 3},
	}, nil)
	m.message.EXPECT().Find(gomock.Any(), int64(21)).Return(last, nil)

	d, err := ctlConversations(t, m).DetailCtlConversation(m.ctx, 100)
	require.NoError(t, err)
	require.NotNil(t, d)
	assert.Equal(t, "claude-local", d.Backend)
	assert.Equal(t, "pk", d.ProviderKey)
	assert.Equal(t, "mk", d.ModelKey)
	assert.Equal(t, 150, d.InputTokens)
	assert.Equal(t, 10, d.OutputTokens)
	assert.Equal(t, "the answer", d.LastText)
	assert.Equal(t, "planner", d.AgentName)
}

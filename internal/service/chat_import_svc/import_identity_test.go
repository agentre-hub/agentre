package chat_import_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
)

// import_identity_test.go 钉住导入的**身份**这一半:这条对话的号谁铸的、这个 Agent
// 按哪个标识认。两件事只在跨机导入(控制台点一台机器的历史会话)上才有分歧,本机
// 导入照旧不带这两格。

const testConversationID = "0199a0b1-c2d3-7e4f-8a9b-0c1d2e3f4a5b"

// TestImport_GivenCallerMintedConversationID_ThenAdoptsIt:号是**发起端**铸的,
// 本机原样收下 —— 与 runtime.run 同一条规矩(发起端铸号、对端从不发号)。另铸一个
// 会让同一条对话在两侧有两个身份,控制台此后再也寻址不到它。
func TestImport_GivenCallerMintedConversationID_ThenAdoptsIt(t *testing.T) {
	m := withMocks(t, testCwd)
	claudeAgentAndBackend(m)
	installSource(t, &fakeSource{
		backend:    agent_backend_entity.TypeClaudeCode,
		transcript: twoTurnTranscript(),
	})

	m.session.EXPECT().ListIDsByProviderSessions(gomock.Any(), []string{testSession}).
		Return(map[string]int64{}, nil)
	var created *chat_entity.Session
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			s.ID = 55
			created = s
			return nil
		})
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).Times(4).Return(nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

	req := importReq()
	req.ConversationID = testConversationID
	got, err := m.svc.Import(context.Background(), req, nil)

	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, testConversationID, created.ConversationID, "落库的是调用方铸的那个号")
	assert.Equal(t, testConversationID, got.ConversationID, "应答交回的也是它")
	assert.Equal(t, testSession, got.ProviderSessionID)
	assert.Equal(t, "修一个 bug", got.Title)
}

// TestImport_GivenNoConversationID_ThenLeavesMintingToTheRepo:本机导入不带号,
// 建行时由 chat_repo.Session().Create 铸 —— 本包不在这里抢着铸一个。
func TestImport_GivenNoConversationID_ThenLeavesMintingToTheRepo(t *testing.T) {
	m := withMocks(t, testCwd)
	claudeAgentAndBackend(m)
	installSource(t, &fakeSource{
		backend:    agent_backend_entity.TypeClaudeCode,
		transcript: twoTurnTranscript(),
	})

	m.session.EXPECT().ListIDsByProviderSessions(gomock.Any(), []string{testSession}).
		Return(map[string]int64{}, nil)
	var created *chat_entity.Session
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			s.ID = 55
			require.Empty(t, s.ConversationID, "本包不铸号:空着交给建行那一层")
			// 仓储在这里铸号,应答要交回它铸的那个。
			s.ConversationID = testConversationID
			created = s
			return nil
		})
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).Times(4).Return(nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

	got, err := m.svc.Import(context.Background(), importReq(), nil)

	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, testConversationID, got.ConversationID)
}

// TestImport_GivenAgentSyncID_ThenResolvesAgentByIt:跨机场景**一律以同步标识为准**。
// 请求里同时带着的 AgentID 是历史兼容,它是发起端库里的自增主键 —— 采信它就会把会话
// 落到本机那个碰巧同号的 Agent 名下。
func TestImport_GivenAgentSyncID_ThenResolvesAgentByIt(t *testing.T) {
	m := withMocks(t, testCwd)
	claudeAgentAndBackend(m)
	installSource(t, &fakeSource{
		backend:    agent_backend_entity.TypeClaudeCode,
		transcript: twoTurnTranscript(),
	})

	m.syncState.EXPECT().FindLocalID(gomock.Any(), syncwire.KindAgent, "agent-sync-abc").
		Return(int64(7), nil)
	m.session.EXPECT().ListIDsByProviderSessions(gomock.Any(), []string{testSession}).
		Return(map[string]int64{}, nil)
	var created *chat_entity.Session
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			s.ID = 55
			created = s
			return nil
		})
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).Times(4).Return(nil)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

	req := importReq()
	req.AgentSyncID = "agent-sync-abc"
	// 发起端库里的主键,本机的 7 号是**另一个** Agent。带上它是为了证明它不被采信。
	req.AgentID = 999
	_, err := m.svc.Import(context.Background(), req, nil)

	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, int64(7), created.AgentID, "认的是同步标识解出来的本机 Agent,不是线上送来的主键")
}

// TestImport_GivenUnknownAgentSyncID_ThenFailsBeforeWriting:标识在本机解不出
// (同步还没落地 / 该 Agent 已删除)时明确报错,而不是落到别的 Agent 上或建一条
// 无主会话 —— 后两种都要等用户接着聊时才发现,那时转录已经在库里了。
func TestImport_GivenUnknownAgentSyncID_ThenFailsBeforeWriting(t *testing.T) {
	m := withMocks(t, testCwd)
	installSource(t, &fakeSource{
		backend:    agent_backend_entity.TypeClaudeCode,
		transcript: twoTurnTranscript(),
	})

	m.syncState.EXPECT().FindLocalID(gomock.Any(), syncwire.KindAgent, "agent-sync-missing").
		Return(int64(0), nil)
	// 一行都不写:没有 Create 的 EXPECT,真调到就是失败。

	req := importReq()
	req.AgentSyncID = "agent-sync-missing"
	req.AgentID = 0
	got, err := m.svc.Import(context.Background(), req, nil)

	require.Error(t, err)
	require.ErrorIs(t, err, ErrAgentNotFound)
	assert.Nil(t, got)
	assert.Equal(t, 0, m.tx.ran, "解不出 Agent 连事务都不该开")
}

// TestImport_GivenAlreadyImported_ThenReportsTheStoredConversationIdentity:判重命中
// 时应答指的是**库里那条**。跨机调用方手上只有它刚铸的那个号,拿不到库里那条的身份
// 就没法把用户送过去(wire.ExecuteResult 的契约)。
func TestImport_GivenAlreadyImported_ThenReportsTheStoredConversationIdentity(t *testing.T) {
	m := withMocks(t, testCwd)
	claudeAgentAndBackend(m)
	installSource(t, &fakeSource{
		backend:    agent_backend_entity.TypeClaudeCode,
		transcript: twoTurnTranscript(),
	})

	m.session.EXPECT().ListIDsByProviderSessions(gomock.Any(), []string{testSession}).
		Return(map[string]int64{testSession: 88}, nil)
	m.session.EXPECT().Find(gomock.Any(), int64(88)).
		Return(&chat_entity.Session{ID: 88, ConversationID: "0199a0b1-c2d3-7e4f-8a9b-000000000088", Title: "早就导过的那条"}, nil)
	// 一行都不写:没有 Create / Message().Create 的 EXPECT。

	req := importReq()
	req.ConversationID = testConversationID
	got, err := m.svc.Import(context.Background(), req, nil)

	require.NoError(t, err)
	assert.True(t, got.AlreadyImported)
	assert.Equal(t, int64(88), got.SessionID)
	assert.Equal(t, "0199a0b1-c2d3-7e4f-8a9b-000000000088", got.ConversationID,
		"交回库里那条的身份,而不是本次铸的号")
	assert.Equal(t, "早就导过的那条", got.Title)
	assert.Equal(t, 0, m.tx.ran, "判重命中根本不开事务")
}

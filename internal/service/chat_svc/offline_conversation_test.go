package chat_svc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cago-frame/agents/provider"
	"github.com/cago-frame/agents/provider/providertest"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
)

// Hard invariant 1:未登录 / 没有 server 配置时,新建并跑一条对话全程**一个网络
// 请求都不发**。
//
// 这是 spec 2026-08-31 决策 1 的守卫。conversation_id 由发起端在建档那一刻本地
// 铸号(UUIDv7 无需任何协调),被拒的方案是「server 发号」—— 那会让新建对话需要
// 联网 + 登录。真改成那样时这个用例立刻红,而不是等到有人离线开会话才发现。
//
// 探针形态照抄 server_svc/sync_test.go:108 那条「未登录时一个网络请求都不发」:
// 起一个 httptest 当账号服务端、把 server_svc 指过去、只翻一个 called 标志。
func TestSend_GivenNoLoggedInAccount_ThenCreatesAndRunsAConversationWithoutAnyNetworkCall(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	// 装一个**真的** server_svc,base URL 指向那台探针 —— 未登录只体现在
	// server_state 那一行上,而不是靠"没装服务"把网络路径整个拿掉。
	prevServer := server_svc.Server()
	server_svc.SetDefault(server_svc.New(server_svc.NewHTTPClient(srv.URL, ""), nil))
	t.Cleanup(func() { server_svc.SetDefault(prevServer) })

	m := setupChatTest(t)
	ctx := m.ctx
	const firstUserText = "离线也要能开新对话"

	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE,
		PromptJSON: `["You are helpful."]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: "builtin", LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "key-21", Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21",
		ID: 21, Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE,
	}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")

	fp := providertest.New().QueueStream(
		provider.StreamChunk{ContentDelta: "hi"},
		provider.StreamChunk{FinishReason: provider.FinishStop, Usage: &provider.Usage{PromptTokens: 1, CompletionTokens: 1}},
	)
	chat_svc.SetProviderBuilderForTest(func(*llm_provider_entity.LLMProvider) (provider.Provider, error) { return fp, nil })
	t.Cleanup(chat_svc.ResetProviderBuilderForTest)

	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			// 本地发起的对话不由调用方指定身份:号在仓储层建行的那一刻铸出来
			// (见 chat_repo.Session().Create),所以这里交给仓储的是空串。
			assert.Empty(t, s.ConversationID, "本地新建的对话不该由服务层指定身份")
			s.ID = 100
			return nil
		})

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).Times(2)
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.dbMock.ExpectCommit()

	m.message.EXPECT().List(gomock.Any(), int64(100)).Return([]*chat_entity.Message{
		{ID: 1000, SessionID: 100, Role: "user", BlocksJSON: encodeText(firstUserText), Seq: 1},
		{ID: 1001, SessionID: 100, Role: "assistant", BlocksJSON: "[]", Seq: 2},
	}, nil).AnyTimes()
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{AgentID: 7, Text: firstUserText})
	require.NoError(t, err)
	require.Equal(t, int64(100), resp.SessionID)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	assert.False(t, called, "未登录时新建并跑一条对话不该向账号服务端发任何请求")
}

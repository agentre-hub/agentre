package chat_svc_test

import (
	"context"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cago-frame/agents/provider"
	"github.com/cago-frame/agents/provider/providertest"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo/mock_syncstate_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// peerRunAdapter 是从 web 把新对话派到这台桌面端上（R17）的入口形状：runtime.run
// 落到 chatSvc.RunPeerSession。测试经由它调用，与 internal/peer 的 inboundSessionAdapter
// 同构，避免在 chat_svc 测试里反向 import internal/peer。
type peerRunAdapter interface {
	RunPeerSession(ctx context.Context, params wire.RunParams, source chat_svc.PeerSessionSource) (*chat_svc.SendResponse, error)
}

// Given a web peer sends runtime.run with a fresh session id + agentSyncId + cwd + first
// message, when the session does not exist on this desktop yet, then the desktop creates
// a new chat_sessions row for that agent (resolved by account sync id) and project
// (resolved by the reported local path) and runs the first turn through the normal Send
// path, returning the newly created session id — the browser then follows that id.
func TestRunPeerSession_GivenUnknownSession_ThenCreatesFreshDesktopSessionAndRunsFirstTurn(t *testing.T) {
	m := setupChatTest(t)
	wirePeerConversations(t, m.session, 41)
	ctx := m.ctx

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	// R17 的本地解析：账号级 agentSyncId → 本机 agent 行；cwd → 本机 project 行。
	syncMock := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	prevSync := syncstate_repo.SyncState()
	syncstate_repo.RegisterSyncState(syncMock)
	t.Cleanup(func() { syncstate_repo.RegisterSyncState(prevSync) })
	syncMock.EXPECT().FindLocalID(ctx, syncwire.KindAgent, "01HXAGENTIDENTITY0000000000").
		Return(int64(7), nil)

	projMock := mock_project_repo.NewMockProjectRepo(ctrl)
	prevProj := project_repo.Project()
	project_repo.RegisterProject(projMock)
	t.Cleanup(func() { project_repo.RegisterProject(prevProj) })
	projAgentMock := mock_project_repo.NewMockProjectAgentRepo(ctrl)
	prevProjAgent := project_repo.ProjectAgent()
	project_repo.RegisterProjectAgent(projAgentMock)
	t.Cleanup(func() { project_repo.RegisterProjectAgent(prevProjAgent) })

	const cwd = "/Users/wyz/agentre-server"
	proj := &project_entity.Project{ID: 5, Name: "agentre-server", Path: cwd, Status: consts.ACTIVE}
	projMock.EXPECT().List(ctx).Return([]*project_entity.Project{proj}, nil)

	// 对端点的是一条桌面端上还不存在的会话（全新会话 id）→ RunPeerSession 走建会话分支。

	// send（SessionID=0）内部：解析 agent → backend → provider。
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: "builtin", LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{
		ID: 21, ProviderKey: "key-21", Type: string(llm_provider_entity.TypeAnthropic),
		Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", Status: consts.ACTIVE,
	}, nil).AnyTimes()
	m.provider.EXPECT().FindByKey(gomock.Any(), "session-provider").Return(&llm_provider_entity.LLMProvider{
		ID: 22, ProviderKey: "session-provider", Type: string(llm_provider_entity.TypeAnthropic),
		Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-session-default", Status: consts.ACTIVE,
	}, nil).AnyTimes()
	m.provider.EXPECT().FindModelByKey(gomock.Any(), "mk-session-fixed").Return(
		&llm_provider_model_entity.LLMProviderModel{
			ProviderID: 22, ModelKey: "mk-session-fixed", ModelID: "claude-opus-4-1",
			Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE,
		}, nil).AnyTimes()

	fp := providertest.New().
		QueueStream(
			provider.StreamChunk{ContentDelta: "hello"},
			provider.StreamChunk{ContentDelta: "world"},
			provider.StreamChunk{FinishReason: provider.FinishStop, Usage: &provider.Usage{PromptTokens: 5, CompletionTokens: 2}},
		)
	chat_svc.SetProviderBuilderForTest(func(_ *llm_provider_entity.LLMProvider) (provider.Provider, error) {
		return fp, nil
	})
	t.Cleanup(chat_svc.ResetProviderBuilderForTest)

	// send（SessionID=0）内部：resolveProjectContext 校验项目 + agent 成员。
	projMock.EXPECT().Find(gomock.Any(), int64(5)).Return(proj, nil)
	// 第二次是 buildRunRequest 取项目的同步标识——远端一轮要带着它，好让对端把项目
	// 记进自己的会话行。刻意不与上面那次合并：resolveProjectContext 只在 SessionID=0
	// 的首轮跑，而续轮同样要报项目，合并会让续轮报空。
	projMock.EXPECT().Find(gomock.Any(), int64(5)).Return(proj, nil)
	projAgentMock.EXPECT().ListByProjects(gomock.Any(), []int64{5}).
		Return(map[int64][]*project_entity.ProjectAgent{5: {{ProjectID: 5, AgentID: 7}}}, nil)

	// 新建会话行：钉住解析出的 agent + project，标题由首条消息派生。
	const firstUserText = "帮我看看这个项目"
	m.session.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
			assert.Equal(t, int64(7), s.AgentID)
			assert.Equal(t, int64(5), s.ProjectID)
			assert.Equal(t, "session-provider", s.ProviderKey, "the peer-selected provider must persist with the new session")
			assert.Equal(t, "mk-session-fixed", s.ModelKey, "the peer-selected fixed model must persist with the new session")
			assert.Equal(t, firstUserText, s.Title)
			s.ID = 100
			return nil
		})

	// 首轮消息落库（事务内），携带对端来源标识（R21：浏览器发起的消息带来源 pill）。
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

	adapter, ok := m.svc.(peerRunAdapter)
	require.True(t, ok, "chatSvc must implement the peer run adapter")
	resp, err := adapter.RunPeerSession(ctx, wire.RunParams{
		ConversationID: convID(90001),
		AgentSyncID:    "01HXAGENTIDENTITY0000000000",
		Cwd:            cwd,
		Title:          "帮我看看这个项目",
		UserText:       firstUserText,
		LLMProviderKey: "session-provider",
		LLMModelKey:    "mk-session-fixed",
		SourceDevice:   "fp-web",
	}, chat_svc.PeerSessionSource{Device: "fp-web", Name: "Chrome · macOS"})
	require.NoError(t, err)
	assert.Equal(t, int64(100), resp.SessionID)

	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	var got string
	for _, ev := range m.events {
		payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
		if !ok {
			continue
		}
		if payload.Kind == chat_svc.StreamChunk {
			got += payload.Delta
		}
	}
	assert.Equal(t, "helloworld", got)
}

// Given a web peer sends runtime.run with a cwd that no longer matches any local project,
// when no session exists, then the run is rejected (ErrPeerProjectNotFound) without
// creating a phantom session or silently running in a wrong directory.
func TestRunPeerSession_GivenCwdMatchesNoLocalProject_ThenRejectsWithoutCreatingSession(t *testing.T) {
	m := setupChatTest(t)
	wirePeerConversations(t, m.session, 41)
	ctx := m.ctx

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	syncMock := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	prevSync := syncstate_repo.SyncState()
	syncstate_repo.RegisterSyncState(syncMock)
	t.Cleanup(func() { syncstate_repo.RegisterSyncState(prevSync) })
	syncMock.EXPECT().FindLocalID(ctx, syncwire.KindAgent, "01HXAGENTIDENTITY0000000000").
		Return(int64(7), nil)

	projMock := mock_project_repo.NewMockProjectRepo(ctrl)
	prevProj := project_repo.Project()
	project_repo.RegisterProject(projMock)
	t.Cleanup(func() { project_repo.RegisterProject(prevProj) })
	// 本机项目行里没有 /Users/old/removed 这条路径（项目刚被删/改路径）。
	projMock.EXPECT().List(ctx).Return([]*project_entity.Project{
		{ID: 5, Name: "agentre-server", Path: "/Users/wyz/agentre-server", Status: consts.ACTIVE},
	}, nil)

	adapter, ok := m.svc.(peerRunAdapter)
	require.True(t, ok)
	_, err := adapter.RunPeerSession(ctx, wire.RunParams{
		ConversationID: convID(90003),
		AgentSyncID:    "01HXAGENTIDENTITY0000000000",
		Cwd:            "/Users/old/removed",
		UserText:       "hi",
	}, chat_svc.PeerSessionSource{Device: "fp-web", Name: "Chrome"})
	require.ErrorIs(t, err, chat_svc.ErrPeerProjectNotFound)
}

// Given a web peer sends runtime.run for an existing desktop session, when that session
// exists, then behavior is unchanged: the run is a new turn on that session — no fresh
// session is created and the account-sync agent / project resolution is never consulted
// (no syncstate.FindLocalID / project.List expectations are set, so a wrong branch into
// fresh-session creation would fail the mock).
func TestRunPeerSession_GivenExistingSession_ThenContinuesThatSession(t *testing.T) {
	m := setupChatTest(t)
	wirePeerConversations(t, m.session, 41)
	ctx := m.ctx

	runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
	t.Cleanup(restore)

	m.session.EXPECT().Find(gomock.Any(), int64(41)).Return(&chat_entity.Session{
		ID: 41, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil).AnyTimes()
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil).AnyTimes()
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil).AnyTimes()
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{
		ID: 21, ProviderKey: "key-21", Type: string(llm_provider_entity.TypeAnthropic),
		Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-21", Status: consts.ACTIVE,
	}, nil).AnyTimes()
	m.provider.EXPECT().FindModelByKey(gomock.Any(), "mk-key-21").Return(
		&llm_provider_model_entity.LLMProviderModel{
			ProviderID: 21, ModelKey: "mk-key-21", ModelID: "claude-sonnet-4-6",
			Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE,
		}, nil).AnyTimes()
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(41)).Return(3, nil).AnyTimes()
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			if msg.Role == "user" {
				msg.ID = 1000
			} else {
				msg.ID = 1001
			}
			return nil
		}).AnyTimes()
	m.dbMock.ExpectCommit()
	m.message.EXPECT().List(gomock.Any(), int64(41)).Return(nil, nil).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	adapter, ok := m.svc.(peerRunAdapter)
	require.True(t, ok)
	resp, err := adapter.RunPeerSession(ctx, wire.RunParams{
		ConversationID: convID(41),
		UserText:       "再帮我看看",
	}, chat_svc.PeerSessionSource{Device: "fp-web", Name: "Chrome · macOS"})
	require.NoError(t, err)
	assert.Equal(t, int64(41), resp.SessionID)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	select {
	case req := <-runner.requests:
		assert.Equal(t, int64(41), req.SessionID)
		assert.Equal(t, int64(7), req.AgentID)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime request")
	}
}

// Given a web peer sends runtime.run with an agentSyncId the desktop does not know yet,
// when no session exists, then the run is rejected with a clear error — no phantom
// session is created and no silent fallback to another agent happens.
func TestRunPeerSession_GivenUnknownAgentSyncId_ThenRejectsWithoutCreatingSession(t *testing.T) {
	m := setupChatTest(t)
	wirePeerConversations(t, m.session, 41)
	ctx := m.ctx

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	syncMock := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	prevSync := syncstate_repo.SyncState()
	syncstate_repo.RegisterSyncState(syncMock)
	t.Cleanup(func() { syncstate_repo.RegisterSyncState(prevSync) })
	syncMock.EXPECT().FindLocalID(ctx, syncwire.KindAgent, "sync-id-not-here").
		Return(int64(0), nil)

	// 会话不存在 → 走建会话分支；解析不出 agent → 拒绝。

	// 解析不出 agent：不得创建会话行、不得发起任何轮次。一旦误建了会话，gomock
	// 会因 session.Create / agent.Find 未设期望直接判失败。
	adapter, ok := m.svc.(peerRunAdapter)
	require.True(t, ok)
	_, err := adapter.RunPeerSession(ctx, wire.RunParams{
		ConversationID: convID(90002),
		AgentSyncID:    "sync-id-not-here",
		UserText:       "hi",
	}, chat_svc.PeerSessionSource{Device: "fp-web", Name: "Chrome"})
	require.Error(t, err)
}

// Given 对端把一条新对话派到这台桌面端上并带着草稿态选中的思考力度，When 这台机器
// 建出会话行，Then 那一档钉在**这条会话自己**的那一列上（与 LLMProviderKey /
// LLMModelKey 同一条规则，spec 2026-09-01「新建会话」）。
//
// 空串是有含义的取值：对端什么都没选时这一列留空 = 跟随后端配置，绝不把后端配的
// 档位写成「这条会话自己选的」——那会让此后改后端配置对它失效。
func TestRunPeerSession_GivenFreshDispatchReasoningEffort_ThenPinsItOnTheCreatedRow(t *testing.T) {
	cases := []struct {
		name      string
		dispatch  string
		wantOnRow string
	}{
		{name: "dispatch carries a level", dispatch: "xhigh", wantOnRow: "xhigh"},
		{name: "dispatch carries nothing", dispatch: "", wantOnRow: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := setupChatTest(t)
			wirePeerConversations(t, m.session, 41)
			ctx := m.ctx

			ctrl := gomock.NewController(t)
			t.Cleanup(ctrl.Finish)
			syncMock := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
			prevSync := syncstate_repo.SyncState()
			syncstate_repo.RegisterSyncState(syncMock)
			t.Cleanup(func() { syncstate_repo.RegisterSyncState(prevSync) })
			syncMock.EXPECT().FindLocalID(ctx, syncwire.KindAgent, "01HXAGENTIDENTITY0000000000").
				Return(int64(7), nil)

			runner := &recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
			t.Cleanup(restore)

			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(newBuiltinAgent(7, 12), nil)
			// 后端自己配着 medium：会话行仍只记「这条会话自己的选择」，不抄后端那一格。
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
				ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21",
				ReasoningEffort: "medium", Status: consts.ACTIVE,
			}, nil)
			expectResolvableProvider(m, "key-21", string(llm_provider_entity.TypeAnthropic))

			var created *chat_entity.Session
			m.session.EXPECT().Create(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
					created = s
					s.ID = 100
					return nil
				})
			expectFirstTurnWrites(m, 100)

			adapter, ok := m.svc.(peerRunAdapter)
			require.True(t, ok, "chatSvc must implement the peer run adapter")
			resp, err := adapter.RunPeerSession(ctx, wire.RunParams{
				ConversationID:  convID(90002),
				AgentSyncID:     "01HXAGENTIDENTITY0000000000",
				Title:           "hi",
				UserText:        "hi",
				ReasoningEffort: tc.dispatch,
				SourceDevice:    "fp-peer-desktop",
			}, chat_svc.PeerSessionSource{Device: "fp-peer-desktop", Name: "Peer Desktop"})
			require.NoError(t, err)
			chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

			require.NotNil(t, created)
			assert.Equal(t, tc.wantOnRow, created.ReasoningEffort,
				"派过来的档位必须钉在建出的这条会话行上，空则留空（跟随后端配置）")
		})
	}
}

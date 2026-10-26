package chat_svc_test

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo/mock_syncstate_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

func TestFreshPeerCLIModelChoice(t *testing.T) {
	convey.Convey("Given a fresh desktop inbound CLI choice, when starting a session, then it is stored and reaches the first turn, including CLI default", t, func() {
		for _, choice := range [][2]string{{"fixed", "sonnet"}, {"cli-default", ""}, {"", ""}} {
			m := setupChatTest(t)
			wirePeerConversations(t, m.session, 41)
			syncRepo := mock_syncstate_repo.NewMockSyncStateRepo(gomock.NewController(t))
			prev := syncstate_repo.SyncState()
			syncstate_repo.RegisterSyncState(syncRepo)
			t.Cleanup(func() { syncstate_repo.RegisterSyncState(prev) })
			syncRepo.EXPECT().FindLocalID(gomock.Any(), syncwire.KindAgent, "agent-sync").Return(int64(7), nil)
			runner := &cliSetterRunner{recordingRunner: recordingRunner{requests: make(chan agentruntime.RunRequest, 1)}}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
			t.Cleanup(restore)
			m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(newBuiltinAgent(7, 12), nil)
			m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode", DefaultModel: "opus", Status: consts.ACTIVE}, nil)
			var created *chat_entity.Session
			m.session.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, sess *chat_entity.Session) error { created = sess; sess.ID = 100; return nil })
			expectFirstTurnWrites(m, 100)
			adapter := m.svc.(peerRunAdapter)
			resp, err := adapter.RunPeerSession(m.ctx, wire.RunParams{ConversationID: convID(90002), AgentSyncID: "agent-sync", UserText: "hi", CLIModelMode: choice[0], CLIModel: choice[1]}, chat_svc.PeerSessionSource{Device: "peer"})
			require.NoError(t, err)
			chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)
			require.NotNil(t, created)
			require.Equal(t, choice[0], created.CLIModelMode)
			require.Equal(t, choice[1], created.CLIModel)
			select {
			case req := <-runner.requests:
				require.Equal(t, choice[0], req.CLIModelMode)
				require.Equal(t, choice[1], req.CLIModel)
			default:
				t.Fatal("first turn was not started")
			}
			restore()
			syncstate_repo.RegisterSyncState(prev)
		}
	})
}

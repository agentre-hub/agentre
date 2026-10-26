package chat_svc_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo/mock_transcript_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

type cliSetterRunner struct {
	recordingRunner
	values []string
	err    error
}

func (r *cliSetterRunner) SetCLIModel(_ context.Context, _ int64, value string) error {
	r.values = append(r.values, value)
	return r.err
}

func TestSessionCLIModelSwitch(t *testing.T) {
	Convey("Given a native Claude session, When selecting a model, Then idle switches validate before persisting and busy switches defer", t, func() {
		for _, tc := range []struct {
			status      string
			reject      bool
			storageFail bool
		}{{"idle", false, false}, {"idle", true, false}, {"running", false, false}, {"idle", false, true}} {
			ctrl := gomock.NewController(t)
			sessions := mock_chat_repo.NewMockSessionRepo(ctrl)
			agents := mock_agent_repo.NewMockAgentRepo(ctrl)
			backends := mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl)
			messages := mock_transcript_repo.NewMockMessageRepo(ctrl)
			oldS, oldA, oldB, oldM := chat_repo.Session(), agent_repo.Agent(), agent_backend_repo.AgentBackend(), transcript_repo.Message()
			chat_repo.RegisterSession(sessions)
			agent_repo.RegisterAgent(agents)
			agent_backend_repo.RegisterAgentBackend(backends)
			transcript_repo.RegisterMessage(messages)
			sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: tc.status}
			be := &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode", DefaultModel: "opus"}
			sessions.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
			agents.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{ID: 7, AgentBackendID: 12}, nil)
			backends.EXPECT().Find(gomock.Any(), int64(12)).Return(be, nil)
			runner := &cliSetterRunner{}
			if tc.reject {
				runner.err = errors.New("Model 'bad' not found")
			}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
			if !tc.reject {
				var persistErr error
				if tc.storageFail {
					persistErr = errors.New("disk full")
				}
				sessions.EXPECT().UpdateCLIModel(gomock.Any(), int64(100), "fixed", "sonnet").Return(persistErr)
				if !tc.storageFail {
					messages.EXPECT().CreateAtNextSeq(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, msgs ...*chat_entity.Message) error {
						require.Contains(t, noticeTextOf(t, msgs[0]), `"kind":"cli_model"`)
						return nil
					})
				}
			}
			svc := chat_svc.NewChat(nil)
			resp, err := svc.SetChatSessionCLIModel(context.Background(), &chat_svc.SetChatSessionCLIModelRequest{SessionID: 100, Mode: "fixed", Value: "sonnet"})
			if tc.storageFail {
				require.Error(t, err)
				So(sess.CLIModelMode, ShouldBeEmpty)
			} else if tc.reject {
				require.ErrorContains(t, err, "Model 'bad' not found")
				So(sess.CLIModelMode, ShouldBeEmpty)
			} else {
				require.NoError(t, err)
				So(resp.Mode, ShouldEqual, "fixed")
				So(sess.ProviderKey, ShouldBeEmpty)
				So(sess.ModelKey, ShouldBeEmpty)
			}
			if tc.status == "running" {
				So(runner.values, ShouldBeEmpty)
			} else if tc.storageFail {
				So(runner.values, ShouldResemble, []string{"sonnet", "opus"})
			} else {
				So(runner.values, ShouldResemble, []string{"sonnet"})
			}
			restore()
			chat_repo.RegisterSession(oldS)
			agent_repo.RegisterAgent(oldA)
			agent_backend_repo.RegisterAgentBackend(oldB)
			transcript_repo.RegisterMessage(oldM)
			ctrl.Finish()
		}
	})
}

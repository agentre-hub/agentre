package chat_svc

import (
	"context"
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/consts"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/view"
)

func TestCLIModelDeviationAndProjection(t *testing.T) {
	Convey("Given a fixed alias and CLI reports, When a turn finishes, Then resolved aliases do not warn but real deviations carry both models", t, func() {
		req := agentruntime.RunRequest{CLIModelMode: "fixed", CLIModel: "sonnet"}
		models := []agentruntime.CLIModel{{Value: "sonnet", ModelID: "claude-sonnet"}}
		So(cliModelDeviation(req, "claude-sonnet", models, true), ShouldBeNil)
		notice := cliModelDeviation(req, "claude-opus", models, true)
		So(notice, ShouldNotBeNil)
		projected := noticeBlockToChatBlock(*notice)
		So(projected.NoticeKind, ShouldEqual, "cli_model_deviation")
		So(projected.CLIModel, ShouldEqual, "sonnet")
		So(projected.ActualModel, ShouldEqual, "claude-opus")
		req.CLIModelMode = "cli-default"
		So(cliModelDeviation(req, "claude-opus", models, true), ShouldBeNil)
		projected = noticeBlockToChatBlock(blocks.NoticeBlock{Text: view.EncodeCLIModelSwitch("fixed", "sonnet")})
		So(projected.CLIModelMode, ShouldEqual, "fixed")
	})
}

func TestCLIModelDeviationWithoutCatalog(t *testing.T) {
	Convey("Given a fixed alias, When the CLI catalog could not be read, Then a differing report is not claimed as a deviation but a literal mismatch-free report stays silent too", t, func() {
		req := agentruntime.RunRequest{CLIModelMode: "fixed", CLIModel: "sonnet"}
		So(cliModelDeviation(req, "claude-sonnet-5-5", nil, false), ShouldBeNil)
		So(cliModelDeviation(req, "sonnet", nil, false), ShouldBeNil)
	})
	Convey("Given a manually entered ID absent from a readable catalog, When the CLI reports another model, Then the deviation is reported", t, func() {
		req := agentruntime.RunRequest{CLIModelMode: "fixed", CLIModel: "my-model"}
		models := []agentruntime.CLIModel{{Value: "sonnet", ModelID: "claude-sonnet"}}
		So(cliModelDeviation(req, "claude-sonnet", models, true), ShouldNotBeNil)
	})
}

func TestSessionCLIModelTurnResolution(t *testing.T) {
	Convey("Given three session choices, When validating a draft and building a turn, Then malformed or conflicting choices fail and valid choices reach the runtime", t, func() {
		be := &agent_backend_entity.AgentBackend{Type: "claudecode"}
		for _, tc := range []struct {
			mode, value, provider string
			valid                 bool
		}{{"", "", "", true}, {"cli-default", "", "", true}, {"fixed", "sonnet", "", true}, {"fixed", "", "", false}, {"fixed", "sonnet", "gateway", false}, {"other", "", "", false}} {
			req := &SendRequest{CLIModelMode: tc.mode, CLIModel: tc.value, ProviderKey: tc.provider}
			So(validateDraftCLIModel(context.Background(), req, be) == nil, ShouldEqual, tc.valid)
		}
		be.LLMProviderKey = "gateway"
		So(validateDraftCLIModel(context.Background(), &SendRequest{CLIModelMode: "cli-default"}, be), ShouldNotBeNil)
	})
	Convey("Given a stale turn snapshot after a mid-turn choice, When the next turn refreshes its selection, Then the latest persisted choice is used", t, func() {
		ctrl := gomock.NewController(t)
		repo := mock_chat_repo.NewMockSessionRepo(ctrl)
		old := chat_repo.Session()
		chat_repo.RegisterSession(repo)
		defer chat_repo.RegisterSession(old)
		sess := &chat_entity.Session{ID: 42, CLIModelMode: "fixed", CLIModel: "old"}
		repo.EXPECT().Find(gomock.Any(), int64(42)).Return(&chat_entity.Session{ID: 42, CLIModelMode: "fixed", CLIModel: "latest", Status: consts.ACTIVE}, nil)
		So(refreshCLIModelChoice(context.Background(), sess), ShouldBeNil)
		req := agentruntime.RunRequest{}
		applySessionCLIModel(&req, sess)
		So(req.CLIModelMode, ShouldEqual, "fixed")
		So(req.CLIModel, ShouldEqual, "latest")
	})
}

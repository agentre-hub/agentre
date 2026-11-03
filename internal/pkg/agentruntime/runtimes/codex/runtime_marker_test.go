package codex

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// Given 会话带 conversation_id 设 goal，When codex 为 goal 起（或复用）CLI，
// Then 交给 CLI 启动器的 RunRequest 携带这条对话身份，CLI 才会注入 AGENTRE_SESSION。
func TestSetGoal_GivenConversationID_ThenSessionFactoryRequestCarriesIt(t *testing.T) {
	Convey("goal 路径起的 codex CLI 带会话标记所需的 ConversationID", t, func() {
		var got agentruntime.RunRequest
		restore := SetSessionFactoryForTest(func(req agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			got = req
			return &fakeRuntimeSession{}, nil
		})
		defer restore()

		objective := "ship"
		_, err := NewWithPool(nil).SetGoal(context.Background(), agentruntime.GoalRequest{
			Backend:        &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex), EnvJSON: "{}"},
			AgentID:        7,
			SessionID:      42,
			ConversationID: "conv-goal-1",
			Cwd:            t.TempDir(),
			Objective:      &objective,
		})

		So(err, ShouldBeNil)
		So(got.ConversationID, ShouldEqual, "conv-goal-1")
	})
}

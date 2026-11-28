package chat_svc_test

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/exec_target_svc"
)

// 会话信息工具窗要的两项只读事实：CLI 会话 ID 与本会话带入的记忆。
func TestLoadSession_SessionInfoFacts(t *testing.T) {
	convey.Convey("LoadSession 暴露会话信息工具窗用的 providerSessionId 与带入的记忆", t, func() {
		m := setupChatTest(t)
		ctx := context.Background()
		exec_target_svc.RegisterCwdResolver(func(_ context.Context, _ *chat_entity.Session) (string, error) {
			return "", nil
		})
		t.Cleanup(func() { exec_target_svc.RegisterCwdResolver(nil) })

		load := func(id int64, sess *chat_entity.Session) *chat_svc.LoadSessionResponse {
			sess.ID, sess.AgentID, sess.Status = id, 70, consts.ACTIVE
			m.session.EXPECT().Find(ctx, id).Return(sess, nil)
			m.agent.EXPECT().Find(ctx, int64(70)).Return(&agent_entity.Agent{
				ID: 70, Name: "A", AgentBackendID: 71, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(ctx, int64(71)).Return(&agent_backend_entity.AgentBackend{
				ID: 71, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
			}, nil)
			expectTranscriptWindowFilled(m)
			m.message.EXPECT().ListMeta(ctx, id).Return(nil, nil)
			resp, err := m.svc.LoadSession(ctx, &chat_svc.LoadSessionRequest{SessionID: id})
			assert.NoError(t, err)
			return resp
		}

		convey.Convey("providerSessionId 原样给出，没有时为空", func() {
			assert.Equal(t, "cli-abc-123", load(201, &chat_entity.Session{ProviderSessionID: "cli-abc-123"}).Session.ProviderSessionID)
			assert.Equal(t, "", load(202, &chat_entity.Session{}).Session.ProviderSessionID)
		})

		convey.Convey("带入的记忆按层给出条目，不含标题、遗漏提示与使用说明", func() {
			snapshot := "# Memory\n\n### Agent\n- [m1] 喜欢简短回答 (2026-11-01)\n- [m2] 用 pnpm (2026-11-02) [has details]\n(3 more not included: agrctl list memory)\n\n### Project: Alpha\n- [m9] 部署走 gitea (2026-11-03)\n\nWorth remembering: x\nRead: agrctl get memory <id>"
			resp := load(203, &chat_entity.Session{MemorySnapshot: snapshot, MemorySnapshotTaken: true})
			assert.Equal(t, [][]string{
				{
					"Agent",
					"[m1] 喜欢简短回答 (2026-11-01)",
					"[m2] 用 pnpm (2026-11-02) [has details]",
				},
				{"Project: Alpha", "[m9] 部署走 gitea (2026-11-03)"},
			}, resp.Session.CarriedMemory)
		})

		convey.Convey("没带记忆（空快照或只有使用说明）时为空列表而不是 nil", func() {
			resp := load(204, &chat_entity.Session{MemorySnapshotTaken: true})
			assert.NotNil(t, resp.Session.CarriedMemory)
			assert.Empty(t, resp.Session.CarriedMemory)
			resp = load(205, &chat_entity.Session{MemorySnapshot: "# Memory\n\nRead: agrctl get memory <id>", MemorySnapshotTaken: true})
			assert.Empty(t, resp.Session.CarriedMemory)
		})
	})
}

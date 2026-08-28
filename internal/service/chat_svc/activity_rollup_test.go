package chat_svc

import (
	"context"
	"math"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/activityrollup"
)

// TestActivityRollup_CountsDesktopSessionsByDayAndDimensions 覆盖桌面端这一侧的取数:
// 本地会话被压成天 × 维度的计数,本地自增 id(agent_id / project_id / backend_id)一律
// 换成账号级同步标识 —— 对端拿本机主键毫无用处。
//
// 「哪一天」看的是**建立**时刻而不是最后活动时刻(activityrollup.Aggregate 的注释):
// 这里三条会话的 LastMessageAt 都晚于各自的建立日,而它们仍落在建立那天。
//
// 标题与 cwd 就在会话行上,一个都不出现在结果里:这条边界是那个开关向用户承诺的东西。
func TestActivityRollup_CountsDesktopSessionsByDayAndDimensions(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	deps.projects = []*project_entity.Project{
		{ID: 5, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "p1"}},
	}
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{
		{ID: 7, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "a1"}, AgentBackendID: 11, Status: consts.ACTIVE},
	}, nil)
	// 后端类型一次取齐。逐条会话回库查一次,一年的会话就是几百次往返 —— 而这份表在
	// 一次上报里不会变。
	deps.backend.EXPECT().List(ctx).Return([]*agent_backend_entity.AgentBackend{
		{ID: 11, Type: string(agent_backend_entity.TypeClaudeCode)},
		{ID: 12, Type: string(agent_backend_entity.TypeCodex)},
	}, nil)
	deps.session.EXPECT().ListByAgentPaged(ctx, int64(7), 0, math.MaxInt).Return([]*chat_entity.Session{
		// 同一天、同一组合的两条 → 计数 2。标题与 cwd 摆在这里就是为了守它们不外泄。
		// 落哪一天看 Createtime。这三条的 LastMessageAt 一律晚于建立时刻(会话被续过),
		// 却都留在它们建立的那天 —— 那正是这条口径的全部意义。
		{ID: 41, AgentID: 7, ProjectID: 5, Title: "改一个 bug", Cwd: "/Users/me/secret",
			Createtime: 1787875200000, LastMessageAt: 1788048000000, Status: consts.ACTIVE}, // 建于 2026-08-28 00:00 UTC
		{ID: 42, AgentID: 7, ProjectID: 5, Title: "另一件事", Cwd: "/Users/me/other",
			Createtime: 1787918400000, LastMessageAt: 1788048000000, Status: consts.ACTIVE}, // 建于 2026-08-28 12:00 UTC
		// 这条钉了另一档后端,且自由会话(ProjectID = 0) → 另一个组合。
		{ID: 43, AgentID: 7, ExecAgentBackendID: 12, ProviderKey: "openai", ModelKey: "gpt-5.2",
			Createtime: 1787788800000, LastMessageAt: 1788048000000, Status: consts.ACTIVE}, // 建于 2026-08-27 00:00 UTC
	}, nil)

	got, err := deps.svc.ActivityRollup(ctx, "", "UTC")
	require.NoError(t, err)
	assert.Equal(t, []activityrollup.Bucket{
		{Day: "2026-08-27", AgentSyncID: "a1", BackendType: string(agent_backend_entity.TypeCodex),
			ProviderKey: "openai", ModelKey: "gpt-5.2", SessionCount: 1},
		{Day: "2026-08-28", AgentSyncID: "a1", BackendType: string(agent_backend_entity.TypeClaudeCode),
			ProjectSyncID: "p1", SessionCount: 2},
	}, got)
	assert.Equal(t, 1, deps.projectListCalls, "项目清单一次上报只读一遍")
}

// TestActivityRollup_BucketsInTheRequestedZone 覆盖时区透传:日界由调用方(服务端)定,
// 一个账号下分散各地的机器才会落在同一套日界上。
func TestActivityRollup_BucketsInTheRequestedZone(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	deps.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{
		{ID: 7, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "a1"}, Status: consts.ACTIVE},
	}, nil)
	deps.backend.EXPECT().List(ctx).Return(nil, nil)
	deps.session.EXPECT().ListByAgentPaged(ctx, int64(7), 0, math.MaxInt).Return([]*chat_entity.Session{
		// 建于 UTC 2026-08-27 23:30 = 上海 2026-08-28 07:30。
		{ID: 41, AgentID: 7, Createtime: 1787873400000, LastMessageAt: 1787873400000, Status: consts.ACTIVE},
	}, nil)

	got, err := deps.svc.ActivityRollup(ctx, "", "Asia/Shanghai")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "2026-08-28", got[0].Day)
}

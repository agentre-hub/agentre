package chat_svc

import (
	"context"
	"math"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/activityrollup"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

// ActivityRollup 交出这台电脑上按 (天 × 维度组合) 的会话计数。
//
// 它是活跃统计上报在桌面端这一侧的取数口:回包里只有天、几个账号级标识和一个计数 ——
// 标题、cwd、对话内容一个字都不出去。那条边界是这个开关向用户承诺的东西,所以摊平在
// 这里一次做完(见 activityrollup.Aggregate 的入参类型),调用方拿不到原始会话行。
//
// sinceDay 是闭区间下界("YYYY-MM-DD",按 timeZone 切),空串 = 有多少给多少。回填不是
// 另一种模式,就是一次不带下界的调用。timeZone 由服务端给,解不开时落回 UTC。
//
// 三张查询表(Agent / 后端 / 项目)各读一次就够:它们在一次上报里不会变,而下面是「每个
// Agent × 它的每条会话」两层循环,逐条回库就是几百次往返。
func (s *chatSvc) ActivityRollup(ctx context.Context, sinceDay, timeZone string) ([]activityrollup.Bucket, error) {
	agents, err := agent_repo.Agent().List(ctx)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	backendTypes, err := backendTypeByID(ctx)
	if err != nil {
		return nil, err
	}
	projectSyncIDs, err := projectSyncIDByID(ctx)
	if err != nil {
		return nil, err
	}

	items := make([]activityrollup.Activity, 0)
	for _, agent := range agents {
		if agent == nil {
			continue
		}
		sessions, err := chat_repo.Session().ListByAgentPaged(ctx, agent.ID, 0, math.MaxInt)
		if err != nil {
			return nil, operationFailedWithCause(ctx, err)
		}
		for _, session := range sessions {
			if session == nil {
				continue
			}
			items = append(items, activityrollup.Activity{
				CreatedAt:     session.Createtime,
				LastMessageAt: session.LastMessageAt,
				// 本地自增 id 换成账号级同步标识:对端拿本机主键毫无用处。
				AgentSyncID:   agent.SyncID,
				BackendType:   backendTypes[sessionBackendID(session, agent)],
				ProviderKey:   session.ProviderKey,
				ModelKey:      session.ModelKey,
				ProjectSyncID: projectSyncIDs[session.ProjectID],
			})
		}
	}

	loc := time.UTC
	if timeZone != "" {
		if parsed, loadErr := time.LoadLocation(timeZone); loadErr == nil {
			loc = parsed
		}
	}
	return activityrollup.Aggregate(items, loc, sinceDay), nil
}

// backendTypeByID 把后端档位表压成 id → 类型 的查询表。缺档(被删了)时那一格解出空串,
// 如实留空 —— 空是「这条会话说不出后端类型」,不是某个默认后端。
func backendTypeByID(ctx context.Context) (map[int64]string, error) {
	backends, err := agent_backend_repo.AgentBackend().List(ctx)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	out := make(map[int64]string, len(backends))
	for _, backend := range backends {
		if backend != nil {
			out[backend.ID] = backend.Type
		}
	}
	return out, nil
}

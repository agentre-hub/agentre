package chat_svc

import (
	"context"
	"time"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
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

	loc := time.UTC
	if timeZone != "" {
		if parsed, loadErr := time.LoadLocation(timeZone); loadErr == nil {
			loc = parsed
		}
	}

	agentByID := make(map[int64]*agent_entity.Agent, len(agents))
	for _, agent := range agents {
		if agent != nil {
			agentByID[agent.ID] = agent
		}
	}

	// 一条查询,而不是「每个 agent 各查一遍、每次不设上限」:统计问的是整台机器的
	// 会话,按 agent 分段取回来只是把同一张表读了 N 次。窗口下推到 SQL —— 界面问的
	// 通常只是最近 30 天,在内存里丢掉不要的那些等于为一张 30 天的图读三年的会话。
	sessions, err := chat_repo.Session().ListForRollup(ctx, dayStartMillis(sinceDay, loc))
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	items := make([]activityrollup.Activity, 0, len(sessions))
	for _, session := range sessions {
		if session == nil {
			continue
		}
		agent := agentByID[session.AgentID]
		if agent == nil {
			// Agent 档已经不在了:这条会话说不出 agent 身份,计进去只会得到一个空维度。
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
	return activityrollup.Aggregate(items, loc, sinceDay), nil
}

// dayStartMillis 把 "YYYY-MM-DD" 这个下界折成该时区当天零点的毫秒时刻;空串给 0
// (不设下界)。解不动的日期同样给 0 —— 少读几条会让统计凭空少一段,而多读一段只是
// 慢一点,分桶那一步照旧会把窗口外的丢掉。
func dayStartMillis(day string, loc *time.Location) int64 {
	if day == "" {
		return 0
	}
	parsed, err := time.ParseInLocation(time.DateOnly, day, loc)
	if err != nil {
		return 0
	}
	return parsed.UnixMilli()
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

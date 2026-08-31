package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/activityrollup"
)

// ActivityDeps 是 ActivityHandlers 的显式构造入参。
type ActivityDeps struct {
	// Sessions 读会话行。活跃统计只用得上其中的活动时刻与几个维度标识。
	Sessions SessionQueryPort
}

// ActivityHandlers 实现活跃统计的纯计数上报。
//
// 它刻意与补齐族(SessionCatchupHandlers)分开:那一族回答「这条对话里发生了什么」,
// 而这一族**只**回答「哪天有几条」。同一个端口读同一张表,但交出去的东西完全不同,
// 合成一个类型会让那条边界随时间被磨掉。
type ActivityHandlers struct {
	deps ActivityDeps
}

// NewActivityHandlers 组装活跃统计 handler。
func NewActivityHandlers(deps ActivityDeps) *ActivityHandlers {
	return &ActivityHandlers{deps: deps}
}

// ActivityRollup 交出这台机器上按 (天 × 维度组合) 的会话计数。
//
// sinceDay 是闭区间下界("YYYY-MM-DD",按 timeZone 切),空串表示「这台机器有多少给
// 多少」—— 回填不是另一种模式,就是一次不带下界的调用。
//
// timeZone 是 IANA 时区名,由调用方(服务端)给:一个账号下的机器可能分散在不同时区,
// 日界必须只有一套。解不开时落回 UTC 而不是报错 —— 整份统计因为一个时区名失败,是拿
// 用户的数据去赌一件无关的事。
//
// 会话按账号维度读(不限对端):这台机器上的会话本来就都属于这一个账号,而统计是账号
// 级的。
func (h *ActivityHandlers) ActivityRollup(ctx context.Context, sinceDay, timeZone string) ([]activityrollup.Bucket, error) {
	rows, err := h.deps.Sessions.List(ctx, "", "")
	if err != nil {
		return nil, fmt.Errorf("list sessions for activity rollup: %w", err)
	}

	loc := time.UTC
	if timeZone != "" {
		if parsed, loadErr := time.LoadLocation(timeZone); loadErr == nil {
			loc = parsed
		}
	}

	items := make([]activityrollup.Activity, 0, len(rows))
	for _, row := range rows {
		// 只摊这七格。标题与 cwd 就在 row 上,不进来 —— 聚合之后再想剔除就晚了。
		items = append(items, activityrollup.Activity{
			CreatedAt:     row.Createtime,
			LastMessageAt: row.LastMessageAt,
			AgentSyncID:   row.AgentSyncID,
			BackendType:   row.BackendType,
			ProviderKey:   row.ProviderKey,
			ModelKey:      row.ModelKey,
			ProjectSyncID: row.ProjectSyncID,
		})
	}
	return activityrollup.Aggregate(items, loc, sinceDay), nil
}

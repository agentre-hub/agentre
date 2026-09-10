// Package projectsync 解「本地项目 → 账号级同步标识」。
//
// 单开一个叶子包而不是留在 chat_svc:两个账号朝向的读者要同一份口径 —— 远端一轮
// 上报的 projectSyncID(chat_svc)与交给账号对端的会话摘要 / 日活跃分组
// (chat_svc/peerstream、chat_svc.ActivityRollup)。peerstream 是 chat_svc 的叶子,
// 反向 import 不成立,共用的这一份就得住在两边都够得着的地方。
package projectsync

import (
	"context"

	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/svcerr"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
)

// projectsyncErrors 是本包的错误报告口。CallerSkip=1 抵消下面那层薄 wrapper,
// 让日志的 caller 字段仍然指向真正的业务调用点。
var projectsyncErrors = svcerr.Reporter{LogMessage: "chat_svc.projectsync: operation failed", CallerSkip: 1}

func operationFailedWithCause(ctx context.Context, cause error, fields ...zap.Field) error {
	return projectsyncErrors.OperationFailedWithCause(ctx, cause, fields...)
}

// OfSession 解出这条会话所属项目的**账号级同步标识**。
//
// 远端一轮要带的是它而不是本地 project_id:后者是这台机器的自增主键,对端拿了没用。
// 之所以由发起端报、而不是让服务端事后从 cwd 推(agent_sessions 决策 12),是因为
// 日活跃统计按项目分组,而那条通道只上行计数、不上行任何路径 —— 推不出来。
//
// 两种情况都返回空串,语义一致:「这一轮不属于任何项目」。
//   - ProjectID = 0:自由会话。此时**不查库** —— 每轮为一次注定落空的查询往返数据库
//     是纯粹的浪费。
//   - 项目还没认领同步标识:如实留空,不猜、不拿本地 id 顶替。
func OfSession(ctx context.Context, sess *chat_entity.Session) (string, error) {
	if sess == nil || sess.ProjectID == 0 {
		return "", nil
	}
	project, err := project_repo.Project().Find(ctx, sess.ProjectID)
	if err != nil {
		return "", operationFailedWithCause(ctx, err)
	}
	if project == nil {
		return "", nil
	}
	return project.SyncID, nil
}

// ByProjectID 是「本地项目主键 → 账号级同步标识」的查询表。
//
// 还没认领同步标识的项目(未登录期间建的行,R12a 之前)不进表:交出去的必须是账号
// 认得的那个名字,拿本地主键凑一个只会在账号那边建出一个配不上真项目的组。
func ByProjectID(ctx context.Context) (map[int64]string, error) {
	projects, err := project_repo.Project().List(ctx)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	out := make(map[int64]string, len(projects))
	for _, p := range projects {
		if p != nil && p.SyncID != "" {
			out[p.ID] = p.SyncID
		}
	}
	return out, nil
}

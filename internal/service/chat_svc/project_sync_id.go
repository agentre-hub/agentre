package chat_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
)

// projectSyncIDOfSession 解出这条会话所属项目的**账号级同步标识**。
//
// 远端一轮要带的是它而不是本地 project_id:后者是这台机器的自增主键,对端拿了没用。
// 之所以由发起端报、而不是让服务端事后从 cwd 推(agent_sessions 决策 12),是因为
// 日活跃统计按项目分组,而那条通道只上行计数、不上行任何路径 —— 推不出来。
//
// 两种情况都返回空串,语义一致:「这一轮不属于任何项目」。
//   - ProjectID = 0:自由会话。此时**不查库** —— 每轮为一次注定落空的查询往返数据库
//     是纯粹的浪费。
//   - 项目还没认领同步标识:如实留空,不猜、不拿本地 id 顶替。
func projectSyncIDOfSession(ctx context.Context, sess *chat_entity.Session) (string, error) {
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

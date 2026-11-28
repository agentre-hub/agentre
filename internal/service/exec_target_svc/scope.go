package exec_target_svc

import (
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
)

// SessionBackendID 是「这条会话该用哪个 backend」的单一读口（R15b / 决策36）：
// 钉住了就用钉住的那一档，没钉住（首轮 / 全部老会话）才回落到 Agent 的主档。
//
// 凡是拿着一条**已持久化会话**去解析 backend 的读路径都必须经过这里——同一台机器
// 上可以有多档，回到 a.AgentBackendID 会让续轮拿到另一档的 CLI 路径、供应商、模型
// 路由与技能授权，正是决策 16 禁止的中途漂移。这里只读不写：钉住的写入是 startTurn
// 的职责（pinExecTargetIfUnset），查询一下不该把会话钉死。
func SessionBackendID(sess *chat_entity.Session, a *agent_entity.Agent) int64 {
	if sess != nil && sess.ExecAgentBackendID > 0 {
		return sess.ExecAgentBackendID
	}
	if a == nil {
		return 0
	}
	return a.AgentBackendID
}

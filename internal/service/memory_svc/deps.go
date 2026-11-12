package memory_svc

import "context"

//go:generate mockgen -source deps.go -destination mock_memory_svc/mock_deps.go -package mock_memory_svc

// TargetSource 是 TargetAdvisor 读 agent 执行目标的窄端口：宿主（internal/app）用
// exec_target_svc 与 chat_svc 实现它。能力集未知的远端目标（本机没连着那台设备、还没读到它
// 上报的能力）由实现直接略去 —— 不知道就不下结论。
type TargetSource interface {
	ExecTargets(ctx context.Context, agentID int64) ([]ExecTarget, error)
}

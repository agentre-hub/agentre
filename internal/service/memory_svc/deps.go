package memory_svc

import "context"

//go:generate mockgen -source deps.go -destination mock_memory_svc/mock_deps.go -package mock_memory_svc

// TargetSource 是 TargetAdvisor 读 agent 判档事实的窄端口：宿主（internal/app）按成员
// 生效的引擎配置的运行时能力实现它（每名成员一项）。能力未知的运行时由实现直接略去 ——
// 不知道就不下结论。
type TargetSource interface {
	ExecTargets(ctx context.Context, agentID int64) ([]ExecTarget, error)
}

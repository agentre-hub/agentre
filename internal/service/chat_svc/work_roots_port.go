package chat_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/service/workspace_fs_svc"
)

//go:generate mockgen -source work_roots_port.go -destination mock_chat_svc/mock_work_roots_port.go -package mock_chat_svc

// WorkRootsPort 是本服务对「会话工作根认领集合」的窄依赖(ISP + DIP):AI 当前位置
// 变化、写入目标、`git worktree add` 的路径都要交给认领方判定,ExitWorktree 选择删除
// 时撤销那个根,变化之后取一份完整集合推给前端。实现方是 workspace_fs_svc(本机走叶子
// 包、远端走 agentred 的 git 判定);单测注入 mockgen 桩。
type WorkRootsPort interface {
	WorkRoots(ctx context.Context, sessionID int64) ([]workspace_fs_svc.WorkRootView, error)
	Claim(ctx context.Context, sessionID int64, candidates []string) ([]workspace_fs_svc.WorkRootView, error)
	Revoke(ctx context.Context, sessionID int64, root string) (bool, error)
}

// workRootsPort 交回本服务此刻用的认领方:单测注入的桩优先,否则走 workspace_fs_svc
// 的进程单例(每次现取 —— 绑定层单测会替换它)。
func (s *chatSvc) workRootsPort() WorkRootsPort {
	if s.workRoots != nil {
		return s.workRoots
	}
	return workspace_fs_svc.Default()
}

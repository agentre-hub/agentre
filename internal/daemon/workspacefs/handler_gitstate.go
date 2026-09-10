package workspacefs

import (
	"context"
	"path/filepath"

	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	pkgworkspacefs "github.com/agentre-hub/agentre/internal/pkg/workspacefs"
	"github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
)

// GitState 汇总 req.Root 仓库的只读 git 状态快照:分支 / worktree 短名 / 未
// 提交数 / 领先落后 / common git dir。req.Root 必须是非空绝对路径,格式不合法
// → rpcerror.ErrInvalidParams(与 GitBranches 同一道边界)。
func (h *Handlers) GitState(ctx context.Context, req wire.GitStateReq) (*wire.GitStateResp, error) {
	if req.Root == "" || !filepath.IsAbs(req.Root) {
		return nil, rpcerror.ErrInvalidParams
	}

	res := pkgworkspacefs.GitState(ctx, req.Root)

	return &wire.GitStateResp{
		NotARepo:    res.NotARepo,
		Branch:      res.Branch,
		Worktree:    res.Worktree,
		Dirty:       res.Dirty,
		Ahead:       res.Ahead,
		Behind:      res.Behind,
		HasUpstream: res.HasUpstream,
		CommonDir:   res.CommonDir,
	}, nil
}

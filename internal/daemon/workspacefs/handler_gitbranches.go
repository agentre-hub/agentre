package workspacefs

import (
	"context"
	"path/filepath"

	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	pkgworkspacefs "github.com/agentre-hub/agentre/internal/pkg/workspacefs"
	"github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
)

// GitBranches 列出 req.Root 仓库的本地分支与远程跟踪分支,供"本分支档"的基线
// 选择使用。req.Root 必须是非空绝对路径,格式不合法 → rpcerror.ErrInvalidParams。
func (h *Handlers) GitBranches(ctx context.Context, req wire.GitBranchesReq) (*wire.GitBranchesResp, error) {
	if req.Root == "" || !filepath.IsAbs(req.Root) {
		return nil, rpcerror.ErrInvalidParams
	}

	res, err := pkgworkspacefs.GitBranches(ctx, req.Root)
	if err != nil {
		return nil, err
	}

	branches := make([]wire.Branch, len(res.Branches))
	for i, b := range res.Branches {
		branches[i] = wire.Branch{Name: b.Name, Remote: b.Remote}
	}
	return &wire.GitBranchesResp{
		NotARepo:        res.NotARepo,
		Branches:        branches,
		CurrentBranch:   res.CurrentBranch,
		DefaultBaseline: res.DefaultBaseline,
	}, nil
}

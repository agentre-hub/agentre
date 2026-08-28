package workspacefs

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	pkgworkspacefs "github.com/agentre-hub/agentre/internal/pkg/workspacefs"
	"github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
)

// GitChanges 返回 req.Root 的 git 变动列表(未提交档或本分支档,设计决策 8)。
//   - req.Root 必须是非空绝对路径,格式不合法 → rpcerror.ErrInvalidParams。
//   - req.Scope 取值之外的字符串是 host 侧调用方的编程错误(不是 git 故障),
//     → rpcerror.ErrInvalidParams,不走单条 git 子命令的静默容错。
//   - req.Scope==wire.ScopeBranch 且 req.BaseRef 为空 → wire.ErrBaselineRequired。
func (h *Handlers) GitChanges(ctx context.Context, req wire.GitChangesReq) (*wire.GitChangesResp, error) {
	if req.Root == "" || !filepath.IsAbs(req.Root) {
		return nil, rpcerror.ErrInvalidParams
	}
	var scope pkgworkspacefs.GitChangesScope
	switch req.Scope {
	case wire.ScopeUncommitted:
		scope = pkgworkspacefs.ScopeUncommitted
	case wire.ScopeBranch:
		scope = pkgworkspacefs.ScopeBranch
	default:
		return nil, rpcerror.ErrInvalidParams
	}

	res, err := pkgworkspacefs.GitChanges(ctx, req.Root, scope, req.BaseRef, h.maxEntries)
	if err != nil {
		if errors.Is(err, pkgworkspacefs.ErrBaselineRequired) {
			return nil, wire.ErrBaselineRequired
		}
		return nil, err
	}

	changes := make([]wire.Change, len(res.Changes))
	for i, c := range res.Changes {
		changes[i] = wire.Change{
			Path:    c.Path,
			OldPath: c.OldPath,
			Status:  string(c.Status),
			Added:   c.Added,
			Deleted: c.Deleted,
			Binary:  c.Binary,
		}
	}
	return &wire.GitChangesResp{
		NotARepo:  res.NotARepo,
		Changes:   changes,
		Truncated: res.Truncated,
	}, nil
}

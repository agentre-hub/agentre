package workspacefs

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	pkgworkspacefs "github.com/agentre-hub/agentre/internal/pkg/workspacefs"
	"github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
)

// GitFileContent 返回 req.Root 下 req.RelPath 所指文件在 git HEAD 的版本(对比
// 档左列,与工作区内容并排比较)。
//   - root 为空 → wire.ErrNoCwd;非空但非绝对路径 → rpcerror.ErrInvalidParams;
//     relPath 越界(含 ".."、绝对路径、跟随符号链接后逃出 root)→
//     wire.ErrPathRefused,与 ReadFile 同一道边界。
//   - 非 git 仓库 → NotARepo=true 的降级结果;文件未跟踪 / 不在 HEAD →
//     HasHead=false 的空基线,均不报错(与 GitChanges / GitBranches 的容错
//     约定一致)。
func (h *Handlers) GitFileContent(ctx context.Context, req wire.GitFileContentReq) (*wire.GitFileContentResp, error) {
	if req.Root != "" && !filepath.IsAbs(req.Root) {
		return nil, rpcerror.ErrInvalidParams
	}

	res, err := pkgworkspacefs.GitFileContent(ctx, req.Root, req.RelPath)
	if err != nil {
		if errors.Is(err, pkgworkspacefs.ErrPathRefused) {
			return nil, wire.ErrPathRefused
		}
		if errors.Is(err, pkgworkspacefs.ErrNoCwd) {
			return nil, wire.ErrNoCwd
		}
		return nil, err
	}
	return &wire.GitFileContentResp{
		Content:  res.Content,
		NotARepo: res.NotARepo,
		HasHead:  res.HasHead,
	}, nil
}

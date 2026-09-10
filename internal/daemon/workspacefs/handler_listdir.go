package workspacefs

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	pkgworkspacefs "github.com/agentre-hub/agentre/internal/pkg/workspacefs"
	"github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
)

// ListDir 列出 req.Root 下 req.RelPath 所指目录的一层内容。
//   - req.Root 必须是非空绝对路径——它是会话已解析出的工作目录,由 host 侧
//     调用方负责解析;daemon 只校验格式,不像 remotefs.ListDir 那样在空值时
//     兜底 $HOME(spec 设计决策 2)。格式不合法 → rpcerror.ErrInvalidParams。
//   - req.RelPath 越界(含 ".."、绝对路径,解析后逃出 root)→ wire.ErrPathRefused。
func (h *Handlers) ListDir(ctx context.Context, req wire.ListDirReq) (*wire.ListDirResp, error) {
	if req.Root == "" || !filepath.IsAbs(req.Root) {
		return nil, rpcerror.ErrInvalidParams
	}

	res, err := pkgworkspacefs.ListDir(ctx, req.Root, req.RelPath, req.IncludeIgnored, h.maxEntries)
	if err != nil {
		if errors.Is(err, pkgworkspacefs.ErrPathRefused) {
			return nil, wire.ErrPathRefused
		}
		return nil, err
	}

	entries := make([]wire.Entry, len(res.Entries))
	for i, e := range res.Entries {
		entries[i] = wire.Entry{
			Name:       e.Name,
			IsDir:      e.IsDir,
			Size:       e.Size,
			ModTime:    e.ModTime.Unix(),
			Symlink:    e.Symlink,
			GitIgnored: e.GitIgnored,
		}
	}
	return &wire.ListDirResp{
		Path:      res.Path,
		Entries:   entries,
		Truncated: res.Truncated,
	}, nil
}

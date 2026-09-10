package workspacefs

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	pkgworkspacefs "github.com/agentre-hub/agentre/internal/pkg/workspacefs"
	"github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
)

// SearchFiles 从 req.Root 递归搜索 basename 含 req.Query 子串(不区分大小写)的
// 文件与目录。
//   - root 为空 → wire.ErrNoCwd;非空但非绝对路径 → rpcerror.ErrInvalidParams
//     (root 契约同 ReadFile / ListDir:调用方已解析出的绝对工作目录)。
//   - 遍历恒从 root 开始,没有路径入参,因此不存在"搜索根逃出 cwd"的请求;
//     ".git" 恒不进入,符号链接不跟随(见叶子包 SearchFiles)。
//   - 命中上限 / 目录数预算触发时 Truncated=true,结果不完整这件事必须过 wire。
func (h *Handlers) SearchFiles(ctx context.Context, req wire.SearchFilesReq) (*wire.SearchFilesResp, error) {
	if req.Root != "" && !filepath.IsAbs(req.Root) {
		return nil, rpcerror.ErrInvalidParams
	}

	res, err := pkgworkspacefs.SearchFiles(ctx, req.Root, req.Query, req.IncludeIgnored,
		h.maxSearchHits, h.maxSearchDirs)
	if err != nil {
		if errors.Is(err, pkgworkspacefs.ErrNoCwd) {
			return nil, wire.ErrNoCwd
		}
		return nil, err
	}

	hits := make([]wire.SearchHit, len(res.Hits))
	for i, hit := range res.Hits {
		hits[i] = wire.SearchHit{Path: hit.Path, IsDir: hit.IsDir}
	}
	return &wire.SearchFilesResp{Hits: hits, Truncated: res.Truncated}, nil
}

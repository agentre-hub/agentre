package workspacefs

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	pkgworkspacefs "github.com/agentre-hub/agentre/internal/pkg/workspacefs"
	"github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
)

// ReadFile 读取 req.Root 下 req.RelPath 所指文件的内容(会话级文件预览,纯读)。
//   - root 为空 → wire.ErrNoCwd(会话配置问题,不是路径问题);非空但非绝对
//     路径 → rpcerror.ErrInvalidParams(与 ListDir 的 root 契约一致:root 是调用方
//     已解析出的绝对工作目录)。
//   - relPath 越界(含 ".."、绝对路径、跟随符号链接后逃出 root)→
//     wire.ErrPathRefused,与 ListDir / GitFileContent 同一道边界。
//   - 结果字段(binary / tooLarge / contentType)按叶子包的判定原样透传,不在
//     这里新增错误码(spec 决策 5:binary/tooLarge 走视图字段)。
func (h *Handlers) ReadFile(ctx context.Context, req wire.ReadFileReq) (*wire.ReadFileResp, error) {
	if req.Root != "" && !filepath.IsAbs(req.Root) {
		return nil, rpcerror.ErrInvalidParams
	}

	res, err := pkgworkspacefs.ReadFile(ctx, req.Root, req.RelPath)
	if err != nil {
		if errors.Is(err, pkgworkspacefs.ErrPathRefused) {
			return nil, wire.ErrPathRefused
		}
		if errors.Is(err, pkgworkspacefs.ErrNoCwd) {
			return nil, wire.ErrNoCwd
		}
		return nil, err
	}
	return &wire.ReadFileResp{
		Content:     res.Content,
		ContentType: res.ContentType,
		Binary:      res.Binary,
		TooLarge:    res.TooLarge,
	}, nil
}

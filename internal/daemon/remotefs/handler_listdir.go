package remotefs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/agentre-hub/agentre/internal/pkg/remotefs/pathguard"
	"github.com/agentre-hub/agentre/internal/pkg/remotefs/wire"
)

// osUserHomeDir 包级 indirection,Options.HomeFn 缺省时用。生产 = os.UserHomeDir。
var osUserHomeDir = os.UserHomeDir

// ListDir 列出 req.Path 下的目录项。
// - req.Path 为空 → 解析为 $HOME
// - 路径不合法或命中黑名单 → wire.ErrPathRefused
// - 超过 maxEntries 条目 → 截断并设 resp.Truncated = true
// - 不过滤隐藏文件,不排序;排序/过滤留给前端处理
func (h *Handlers) ListDir(ctx context.Context, req wire.ListDirReq) (*wire.ListDirResp, error) {
	cleaned, err := pathguard.ResolvePath(req.Path, h.homeFn)
	if err != nil {
		if errors.Is(err, pathguard.ErrPathRefused) {
			return nil, wire.ErrPathRefused
		}
		return nil, err
	}

	dirents, err := os.ReadDir(cleaned)
	if err != nil {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil, wire.ErrNotFound
		case errors.Is(err, fs.ErrPermission):
			return nil, wire.ErrPermDenied
		}
		// ENOTDIR 在不同平台 sentinel 不一,用 Lstat 二次判定。
		if info, statErr := os.Lstat(cleaned); statErr == nil && !info.IsDir() {
			return nil, wire.ErrNotDir
		}
		return nil, err
	}

	entries := make([]wire.Entry, 0, len(dirents))
	truncated := false
	for _, d := range dirents {
		if len(entries) >= h.maxEntries {
			truncated = true
			break
		}
		info, lerr := os.Lstat(filepath.Join(cleaned, d.Name()))
		if lerr != nil {
			// 单个 entry 出错(并发删等),跳过不致命。
			continue
		}
		entries = append(entries, wire.Entry{
			Name:    d.Name(),
			IsDir:   info.IsDir(),
			Size:    sizeOrZero(info),
			ModTime: info.ModTime().Unix(),
			Symlink: info.Mode()&os.ModeSymlink != 0,
		})
	}

	return &wire.ListDirResp{
		Path:      cleaned,
		Entries:   entries,
		Truncated: truncated,
	}, nil
}

func sizeOrZero(info fs.FileInfo) int64 {
	if info.IsDir() {
		return 0
	}
	return info.Size()
}

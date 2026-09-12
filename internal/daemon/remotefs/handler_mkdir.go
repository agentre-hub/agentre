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

func (h *Handlers) Mkdir(ctx context.Context, req wire.MkdirReq) (*wire.MkdirResp, error) {
	parent, err := pathguard.ResolvePath(req.Parent, h.homeFn)
	if err != nil {
		if errors.Is(err, pathguard.ErrPathRefused) {
			return nil, wire.ErrPathRefused
		}
		return nil, err
	}
	if err := pathguard.ValidateName(req.Name); err != nil {
		return nil, wire.ErrInvalidName
	}
	target := filepath.Join(parent, req.Name)
	// 双保险:再走一次 ResolvePath,防 name 在某些边角(空白尾被 ValidateName
	// 挡到了,但若以后放宽规则,此处仍能托底)绕过黑名单。
	target, err = pathguard.ResolvePath(target, h.homeFn)
	if err != nil {
		return nil, wire.ErrPathRefused
	}

	if err := os.Mkdir(target, 0o755); err != nil {
		switch {
		case errors.Is(err, fs.ErrExist):
			return nil, wire.ErrMkdirExists
		case errors.Is(err, fs.ErrNotExist):
			return nil, wire.ErrNotFound
		case errors.Is(err, fs.ErrPermission):
			return nil, wire.ErrPermDenied
		}
		return nil, err
	}
	return &wire.MkdirResp{Path: target}, nil
}

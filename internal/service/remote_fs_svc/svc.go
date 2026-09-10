// Package remote_fs_svc 是 host 侧 remotefs.* RPC 的薄业务封装。
// 通过 remote_device_svc.Pool().Borrow 拿 lease,Call wire.Method*,
// 把 wire sentinel 翻成 code.RemoteFsXxx i18n 错误。
//
// 服务层不依赖 DB,只依赖 RemoteDeviceSvc(已 mockable)。
package remote_fs_svc

//go:generate mockgen -source svc.go -destination mock_remote_fs_svc/mock_svc.go

import (
	"context"
	"errors"
	"strconv"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/remotefs/pathguard"
	"github.com/agentre-hub/agentre/internal/pkg/remotefs/wire"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// RemoteFsSvc 给 Wails 绑定层调,deviceID 字符串化(与 ProjectLocationSvc 一致)。
type RemoteFsSvc interface {
	ListDir(ctx context.Context, deviceID, path string) (*ListDirView, error)
	Mkdir(ctx context.Context, deviceID, parent, name string) (*MkdirView, error)
}

// EntryView 是单个目录项的视图。
type EntryView struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	Mtime   int64  `json:"mtime"`
	Symlink bool   `json:"symlink,omitempty"`
}

// ListDirView 是 ListDir 的返回视图。
type ListDirView struct {
	Path      string      `json:"path"`
	Entries   []EntryView `json:"entries"`
	Truncated bool        `json:"truncated"`
}

// MkdirView 是 Mkdir 的返回视图。
type MkdirView struct {
	Path string `json:"path"`
}

var defaultSvc RemoteFsSvc = &remoteFsImpl{}

func Default() RemoteFsSvc { return defaultSvc }

type remoteFsImpl struct {
	// rdSvc 默认走 remote_device_svc.Default();单测注入 mock。
	rdSvc remote_device_svc.RemoteDeviceSvc
}

func (s *remoteFsImpl) deviceSvc() remote_device_svc.RemoteDeviceSvc {
	if s.rdSvc != nil {
		return s.rdSvc
	}
	return remote_device_svc.Default()
}

func parseDeviceID(str string) (int64, error) {
	id, err := strconv.ParseInt(str, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid deviceID")
	}
	return id, nil
}

func (s *remoteFsImpl) ListDir(ctx context.Context, deviceID, path string) (*ListDirView, error) {
	dID, err := parseDeviceID(deviceID)
	if err != nil {
		return nil, i18n.NewError(ctx, code.RemoteDeviceNotFound)
	}
	// host 侧先校一遍,避免无谓 Borrow。空 path 透传给 daemon 由 daemon 自己
	// os.UserHomeDir;只校验非空 path。noopHome 让 ResolvePath 在 path != ""
	// 分支不触发 home lookup。
	if path != "" {
		if _, gerr := pathguard.ResolvePath(path, noopHome); gerr != nil {
			return nil, i18n.NewError(ctx, code.RemoteFsPathRefused)
		}
	}
	lease, err := s.deviceSvc().Pool().Borrow(ctx, dID)
	if err != nil {
		return nil, mapBorrowErr(ctx, err)
	}
	defer lease.Release()

	protoResp, cerr := protorpc.CallMethod(ctx, lease.Client().Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_REMOTE_FS_LIST_DIR),
		&agentrewire.RemoteFsListDirRequest{Path: path}, func() *agentrewire.RemoteFsListDirResponse { return &agentrewire.RemoteFsListDirResponse{} })
	if cerr != nil {
		return nil, mapCallErr(ctx, cerr)
	}
	resp := protowire.RemoteFsListResponseFromProto(protoResp)
	view := &ListDirView{
		Path:      resp.Path,
		Entries:   make([]EntryView, len(resp.Entries)),
		Truncated: resp.Truncated,
	}
	for i, e := range resp.Entries {
		view.Entries[i] = EntryView{
			Name:    e.Name,
			IsDir:   e.IsDir,
			Size:    e.Size,
			Mtime:   e.ModTime,
			Symlink: e.Symlink,
		}
	}
	return view, nil
}

func (s *remoteFsImpl) Mkdir(ctx context.Context, deviceID, parent, name string) (*MkdirView, error) {
	dID, err := parseDeviceID(deviceID)
	if err != nil {
		return nil, i18n.NewError(ctx, code.RemoteDeviceNotFound)
	}
	if err := pathguard.ValidateName(name); err != nil {
		return nil, i18n.NewError(ctx, code.RemoteFsMkdirInvalidName)
	}
	if parent != "" {
		if _, gerr := pathguard.ResolvePath(parent, noopHome); gerr != nil {
			return nil, i18n.NewError(ctx, code.RemoteFsPathRefused)
		}
	}
	lease, err := s.deviceSvc().Pool().Borrow(ctx, dID)
	if err != nil {
		return nil, mapBorrowErr(ctx, err)
	}
	defer lease.Release()

	resp, cerr := protorpc.CallMethod(ctx, lease.Client().Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_REMOTE_FS_MKDIR),
		&agentrewire.RemoteFsMkdirRequest{Parent: parent, Name: name}, func() *agentrewire.RemoteFsMkdirResponse { return &agentrewire.RemoteFsMkdirResponse{} })
	if cerr != nil {
		return nil, mapCallErr(ctx, cerr)
	}
	return &MkdirView{Path: resp.GetPath()}, nil
}

// noopHome 让 host 侧 ResolvePath 在 path != "" 分支不触发 home lookup。
// host 不知道远端 home,真解析在 daemon。
func noopHome() (string, error) { return "/", nil }

func mapBorrowErr(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, remote_device_svc.ErrDeviceNotFound):
		return i18n.NewError(ctx, code.RemoteDeviceNotFound)
	case errors.Is(err, remote_device_svc.ErrDeviceUnauthorized):
		return i18n.NewError(ctx, code.RemoteDeviceUnauthorized)
	}
	return i18n.NewError(ctx, code.RemoteFsDeviceOffline)
}

func mapCallErr(ctx context.Context, err error) error {
	switch wire.FromRPCError(err) {
	case wire.ErrPathRefused:
		return i18n.NewError(ctx, code.RemoteFsPathRefused)
	case wire.ErrPermDenied:
		return i18n.NewError(ctx, code.RemoteFsPermDenied)
	case wire.ErrNotFound:
		return i18n.NewError(ctx, code.RemoteFsNotFound)
	case wire.ErrNotDir:
		return i18n.NewError(ctx, code.RemoteFsNotDir)
	case wire.ErrMkdirExists:
		return i18n.NewError(ctx, code.RemoteFsMkdirExists)
	case wire.ErrInvalidName:
		return i18n.NewError(ctx, code.RemoteFsMkdirInvalidName)
	}
	return i18n.NewError(ctx, code.RemoteRunnerCallFailed)
}

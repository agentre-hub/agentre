package agent_backend_svc

import (
	"context"
	"errors"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/cliprober"
	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// ResolveCLIPath 把 CLI 后端类型解析成「目标机」CLI 搜索路径中的可执行文件绝对路径。
//
// 路由按 req.DeviceID 分发：
//   - 空串 → 本地，主进程走 cliprober 扫本机 $PATH；
//   - 非空 → 远端，主进程拨该 device 调 daemon cli.resolvePath，让远端扫它自己的 $PATH。
//
// 命中后会过滤 cmux.app / Claude.app 这种 App Bundle 内的 CLI shim
// （路径含 ".app/Contents/"）：那种二进制运行后通常会拉起 GUI 而不是真 CLI，
// 不能直接给 claudecode / codex 后端用。该过滤逻辑在 cliprober/clienv 内部完成。
//
// 非 CLI 类型（builtin / 未知）才以 AgentBackendInvalidType 报错。
func (s *agentBackendSvc) ResolveCLIPath(ctx context.Context, req *ResolveCLIPathRequest) (*ResolveCLIPathResponse, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	t := strings.TrimSpace(req.Type)
	deviceID, hasDevice, err := localPairedDeviceID(ctx, strings.TrimSpace(req.DeviceID))
	if err != nil {
		if errors.Is(err, ErrRemoteDeviceNotFound) {
			return nil, i18n.NewError(ctx, code.RemoteDeviceNotFound)
		}
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	if hasDevice {
		return s.resolveCLIPathRemote(ctx, deviceID, t)
	}
	return s.resolveCLIPathLocal(ctx, t)
}

func (s *agentBackendSvc) resolveCLIPathLocal(ctx context.Context, backendType string) (*ResolveCLIPathResponse, error) {
	path, found, err := cliprober.ResolveCLIPath(backendType)
	if err != nil {
		// cliprober 当前只在 backendType 非 claudecode/codex 时返回 ErrInvalidType。
		return nil, i18n.NewError(ctx, code.AgentBackendInvalidType)
	}
	return &ResolveCLIPathResponse{Path: path, Found: found}, nil
}

func (s *agentBackendSvc) resolveCLIPathRemote(ctx context.Context, deviceID int64, backendType string) (*ResolveCLIPathResponse, error) {
	remote := s.remoteCLI
	if remote == nil {
		remote = realRemoteCLI{}
	}
	resp, err := remote.ResolveCLIPath(ctx, deviceID, backendType)
	if err != nil {
		switch {
		case errors.Is(err, ErrRemoteDeviceNotFound):
			return nil, i18n.NewError(ctx, code.RemoteDeviceNotFound)
		case errors.Is(err, ErrRemoteDialFailed):
			// 把原始错误（含网络 / TLS / token 子原因）打到日志便于排查，
			// 用户看到的是 i18n 文案，不暴露内部细节。
			logger.Ctx(ctx).Warn("remote cli.resolvePath dial failed",
				zap.Int64("deviceID", deviceID), zap.Error(err))
			return nil, i18n.NewError(ctx, code.RemoteDeviceDialFailed)
		case errors.Is(err, context.DeadlineExceeded):
			return nil, i18n.NewError(ctx, code.RemoteDeviceTimeout)
		default:
			// 远端 cli.resolvePath 子进程错误（PATH 扫描失败等）—— err.Error()
			// 已经是 cliprober 包装好的人话，套一层 i18n 模板带上原文给用户；
			// 同时记 warn 日志保留 raw 便于排查。
			logger.Ctx(ctx).Warn("remote cli.resolvePath failed",
				zap.Int64("deviceID", deviceID), zap.Error(err))
			return nil, i18n.NewError(ctx, code.RemoteCLIDetectFailed, err.Error())
		}
	}
	return resp, nil
}

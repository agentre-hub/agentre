package agent_backend_svc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// ErrRemoteDialFailed 把 dial 阶段任何失败（网络 / TLS / token 等）统一抽象成
// 一个 sentinel，由调用方按 code.RemoteDeviceDialFailed 映射。不区分子原因 ——
// 用户操作都是「去远端启 daemon 后重试」。
var ErrRemoteDialFailed = errors.New("remote device dial failed")

// ErrRemoteDeviceNotFound 当底层 DialOnce 返回 device-row-missing 时透传。
// 与 remote_device_svc.ErrDeviceNotFound 等价；独立一份让调用方不依赖
// remote_device_svc 包。
var ErrRemoteDeviceNotFound = errors.New("remote device not found")

// remoteCLIPort 主进程拨远端 + 调 cli.* RPC 的窄接口。
// 测试用 fakeRemoteCLI 替身；生产实现是 realRemoteCLI。
type remoteCLIPort interface {
	ResolveCLIPath(ctx context.Context, deviceID int64, backendType string) (*ResolveCLIPathResponse, error)
	Probe(ctx context.Context, deviceID int64, req handlers.CLIProbeParams) (*handlers.CLIProbeResult, error)
}

// realRemoteCLI 拨远端 device 调 daemon cli.* RPC 的生产实现。
type realRemoteCLI struct{}

func (realRemoteCLI) ResolveCLIPath(ctx context.Context, deviceID int64, backendType string) (*ResolveCLIPathResponse, error) {
	lease, err := remote_device_svc.Default().Pool().Borrow(ctx, deviceID)
	if err != nil {
		if errors.Is(err, remote_device_svc.ErrDeviceNotFound) {
			return nil, ErrRemoteDeviceNotFound
		}
		return nil, fmt.Errorf("%w: %v", ErrRemoteDialFailed, err)
	}
	defer lease.Release()

	resp, err := resolveRemoteCLIPath(ctx, lease.Client().Conn(), backendType)
	if err != nil {
		return nil, fmt.Errorf("rpc cli.resolvePath: %w", err)
	}
	return &ResolveCLIPathResponse{Path: resp.Path, Found: resp.Found}, nil
}

func resolveRemoteCLIPath(ctx context.Context, conn *protorpc.Conn, backendType string) (*agentrewire.CLIResolvePathResponse, error) {
	return protorpc.CallMethod(ctx, conn, uint32(agentrewire.RpcMethod_RPC_METHOD_CLI_RESOLVE_PATH),
		&agentrewire.CLIResolvePathRequest{Type: backendType}, func() *agentrewire.CLIResolvePathResponse { return &agentrewire.CLIResolvePathResponse{} })
}

func (realRemoteCLI) Probe(ctx context.Context, deviceID int64, req handlers.CLIProbeParams) (*handlers.CLIProbeResult, error) {
	lease, err := remote_device_svc.Default().Pool().Borrow(ctx, deviceID)
	if err != nil {
		if errors.Is(err, remote_device_svc.ErrDeviceNotFound) {
			return nil, ErrRemoteDeviceNotFound
		}
		return nil, fmt.Errorf("%w: %v", ErrRemoteDialFailed, err)
	}
	defer lease.Release()

	resp, err := probeRemoteCLI(ctx, lease.Client().Conn(), req)
	if err != nil {
		return nil, fmt.Errorf("rpc cli.probe: %w", err)
	}
	return &handlers.CLIProbeResult{Text: resp.Text}, nil
}

func probeRemoteCLI(ctx context.Context, conn *protorpc.Conn, request handlers.CLIProbeParams) (*agentrewire.CLIProbeResponse, error) {
	return protorpc.CallMethod(ctx, conn, uint32(agentrewire.RpcMethod_RPC_METHOD_CLI_PROBE), &agentrewire.CLIProbeRequest{
		BackendType: request.BackendType, LlmProviderKey: request.LLMProviderKey, CliPath: request.CLIPath,
		Sandbox: request.Sandbox, Approval: request.Approval, Model: request.Model,
	}, func() *agentrewire.CLIProbeResponse { return &agentrewire.CLIProbeResponse{} })
}

// probeRemote 在远端 device 上跑一次 cli.probe，把结果折叠回主进程 Test 的
// TestBackendResponse 语义：成功 → OK:true + 文本；失败 → OK:false + i18n 文案。
// 不在这里装 deps（gateway / token / model），全部由 daemon 自己装。
//
// TODO（remote-cancel）：远端 probe 暂不支持 CancelTest；前端取消按钮在此分支下
// 无效。需要 cli.probe 返回 sessionID + cli.cancel 配套后才能接 registerProbe。
func (s *agentBackendSvc) probeRemote(ctx context.Context, b *agent_backend_entity.AgentBackend, deviceID int64) (*TestBackendResponse, error) {
	remote := s.remoteCLI
	if remote == nil {
		remote = realRemoteCLI{}
	}
	start := time.Now()
	// Model 不传 —— daemon 自己根据 LLMProviderKey 查 provider 决定模型。
	resp, err := remote.Probe(ctx, deviceID, handlers.CLIProbeParams{
		BackendType:    b.Type,
		LLMProviderKey: b.LLMProviderKey,
		CLIPath:        b.CLIPath,
		Sandbox:        b.Sandbox,
		Approval:       b.Approval,
	})
	latency := time.Since(start).Milliseconds()
	if err != nil {
		var msg string
		switch {
		case errors.Is(err, ErrRemoteDeviceNotFound):
			msg = i18n.NewError(ctx, code.RemoteDeviceNotFound).Error()
		case errors.Is(err, ErrRemoteDialFailed):
			logger.Ctx(ctx).Warn("remote cli.probe dial failed",
				zap.Int64("deviceID", deviceID), zap.Error(err))
			msg = i18n.NewError(ctx, code.RemoteDeviceDialFailed).Error()
		case errors.Is(err, context.DeadlineExceeded):
			msg = i18n.NewError(ctx, code.RemoteDeviceTimeout).Error()
		default:
			// 远端 cli.probe 子进程错误（CLI 退出 / stderr 等）—— err.Error()
			// 已经是 cliprober 包装好的人话，套一层 i18n 模板带上原文给用户；
			// 同时记 warn 日志保留 raw 便于排查。
			logger.Ctx(ctx).Warn("remote cli.probe failed",
				zap.Int64("deviceID", deviceID), zap.Error(err))
			msg = i18n.NewError(ctx, code.RemoteCLIProbeFailed, err.Error()).Error()
		}
		return &TestBackendResponse{OK: false, Message: msg, LatencyMs: latency}, nil
	}
	return &TestBackendResponse{OK: true, Message: strings.TrimSpace(resp.Text), LatencyMs: latency}, nil
}

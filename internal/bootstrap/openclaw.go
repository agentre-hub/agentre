package bootstrap

import (
	"context"
	"errors"
	goruntime "runtime"

	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/backendcred"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/pkg/openclawgateway"
	"github.com/agentre-hub/agentre/internal/service/agent_backend_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

// openClawRuntimeConfig 是 OpenClaw runtime 的配置解析口：把这一轮的后端（引擎配置摊成的
// 视图，或协议切换前仍在用的旧后端行）解成 Gateway 连接配置。地址与校验取自后端本身，
// 设备本地的 token 按它的同步身份找槽位（spec 2026-11-25 决策 12：槽位从后端 sync_id
// 改为引擎配置 sync_id，迁移规则 8 已把 token 搬过去）。OpenClaw 只在桌面端本机运行：
// 指向另一台机器的后端拿不到这台设备上的 token。
func openClawRuntimeConfig(ctx context.Context, backend *agent_backend_entity.AgentBackend) (openclawgateway.Config, error) {
	if backend == nil || !backend.IsOpenClaw() {
		return openclawgateway.Config{}, i18n.NewError(ctx, code.AgentBackendNotFound)
	}
	if remote_device_svc.TargetsAnotherMachine(backend.DeviceFingerprint) {
		return openclawgateway.Config{}, agent_backend_svc.ErrOpenClawRemoteSecretUnavailable
	}
	if err := backend.Check(ctx); err != nil {
		return openclawgateway.Config{}, err
	}
	store := keychain.Default()
	if store == nil {
		return openclawgateway.Config{}, backendcred.ErrStoreUnavailable
	}
	token := ""
	if account := backendcred.OpenClawTokenAccount(backend.SyncID); account != "" {
		got, err := store.Get(account)
		switch {
		case err == nil:
			token = got
		case !errors.Is(err, backendcred.ErrNotFound):
			return openclawgateway.Config{}, err
		}
	}
	identity, err := backendcred.OpenClawIdentity(store)
	if err != nil {
		return openclawgateway.Config{}, err
	}
	return openclawgateway.Config{
		URL: backend.OpenClawGatewayURL, Token: token, Identity: identity,
		ClientVersion: configs.Version, Platform: goruntime.GOOS,
	}, nil
}

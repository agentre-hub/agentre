package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/cago-frame/cago/pkg/gogo"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
	"github.com/agentre-hub/agentre/internal/repository/sync_account_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
)

// InitServer wires the server_state_repo + server_svc defaults.
// The server_svc starts WITHOUT a wails event emitter; app.go.startup binds it
// later via server_svc.Server().SetEmitter(...).
//
// The keychain backend is established earlier in Init (initKeychain), before any
// keychain-dependent service is assembled; InitServer must not reset it.
//
// Reads the persisted server_url to point the http client at the right host;
// when no row exists yet, the client base URL is empty and StartLogin will
// rebuild it from the user-supplied URL anyway.
func InitServer(ctx context.Context) error {
	server_state_repo.RegisterServerState(server_state_repo.NewServerState())

	row, _ := server_state_repo.ServerState().Get(ctx)
	baseURL := ""
	if row != nil {
		baseURL = row.ServerURL
	}
	svc := server_svc.New(server_svc.NewHTTPClient(baseURL, ""), nil)
	server_svc.SetDefault(svc)

	// 工作区多端同步：server_svc 充当网络出入口，域服务通过 sync_svc.Notify 交出
	// 改动。未登录时同步引擎自己就是空操作（R12），因此这里无条件装配。
	//
	// sync_account_repo 是同步归属的身份来源：行 / 队列 / 游标上盖的账号键由它分配
	// （server 的 user_id 跨 server 不唯一，见 sync_account_entity）。
	sync_account_repo.RegisterSyncAccount(sync_account_repo.NewSyncAccount())
	syncstate_repo.RegisterSyncState(syncstate_repo.NewSyncState())
	sync_svc.SetDefault(sync_svc.New(svc))
	logger.Ctx(ctx).Debug("bootstrap.InitServer: services initialized",
		zap.Bool("hasServerURL", baseURL != ""))
	return nil
}

// SyncBoot 起 30 秒周期的下行轮询（R3）。与 ServerBoot 一样由 app.go.startup 调用。
func SyncBoot(ctx context.Context) {
	logger.Ctx(ctx).Debug("bootstrap.SyncBoot: starting sync service")
	sync_svc.Default().Start(ctx)
}

// ServerBoot runs the per-startup server-side warm-up: ensures a device fingerprint
// exists, refreshes the access token if logged in, emits the current state.
//
// Should be called from app.go.startup, after SetEmitter is bound. Runs in a
// goroutine (via gogo.Go) so it doesn't block UI; logs all errors.
func ServerBoot(ctx context.Context) {
	gogo.Go(func() error {
		logger.Ctx(ctx).Debug("bootstrap.ServerBoot: starting")
		row, err := server_state_repo.ServerState().Get(ctx)
		if err != nil {
			logger.Default().Warn("server boot: load state", zap.Error(err))
			return nil
		}
		if row == nil {
			// Fresh install — persist a fingerprint so future logins reuse it.
			fp := newBootFingerprint()
			if err := server_state_repo.ServerState().Save(ctx, &server_state_entity.ServerState{ID: 1, DeviceFingerprint: fp}); err != nil {
				logger.Default().Warn("server boot: persist fingerprint", zap.Error(err))
			}
			return nil
		}
		if row.DeviceFingerprint == "" {
			row.DeviceFingerprint = newBootFingerprint()
			if err := server_state_repo.ServerState().Save(ctx, row); err != nil {
				logger.Default().Warn("server boot: persist fingerprint", zap.Error(err))
			}
		}

		// Inconsistent-state guard: any of (user_id, device_id, keychain_account)
		// being zero when others are non-zero means a previous logout was interrupted.
		// Clear and re-emit logged_out so the UI doesn't show stale identity.
		userBound := row.ServerUserID != 0
		deviceBound := row.DeviceID != 0
		keychainBound := row.KeychainAccount != ""
		if userBound != deviceBound || userBound != keychainBound {
			_ = server_svc.Server().ClearLogin(ctx) // also emits logged_out{reason:"refresh_expired"}
			return nil
		}

		if !row.IsLoggedIn() {
			logger.Ctx(ctx).Debug("bootstrap.ServerBoot: no logged-in account")
			return nil
		}

		// Refresh on boot — keeps the access token fresh after a long sleep.
		//
		// 刷新失败**不等于**该登出：服务端够不着 / 5xx 时 RefreshWithBackoff 保留
		// 登录态、把自己标成离线并退避重试，只有服务端明确拒绝这份 refresh_token
		// 才清本地登录。（旧实现一律清，一次服务端停机就把 keychain 里的凭据删了，
		// 服务端恢复也回不来，用户只能重新扫码登录。）
		server_svc.Server().RefreshWithBackoff(ctx)
		logger.Ctx(ctx).Debug("bootstrap.ServerBoot: refresh completed",
			zap.Int64("userId", row.ServerUserID), zap.Int64("deviceId", row.DeviceID))
		return nil
	})
}

func newBootFingerprint() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

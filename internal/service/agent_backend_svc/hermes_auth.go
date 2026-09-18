package agent_backend_svc

import (
	"context"
	"errors"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/hermes"
	"github.com/agentre-hub/agentre/internal/pkg/backendcred"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// defaultHermesCredentials is the singleton the registered runtime reaches
// through hermes.DefaultCredentialSource. It resolves the keychain lazily so
// bootstrap's isolated-keychain swap is honored.
var defaultHermesCredentials = backendcred.NewHermesCredentials(func() backendcred.Store {
	if kc := keychain.Default(); kc != nil {
		return kc
	}
	return nil
})

func init() {
	hermes.SetDefaultCredentialSource(defaultHermesCredentials)
}

func (s *agentBackendSvc) credentialStore() *backendcred.HermesCredentials {
	if s != nil && s.hermes != nil {
		return s.hermes
	}
	return defaultHermesCredentials
}

// Hermes test-connection codes. The frontend localizes these so a gated serve
// reports "login required" / "login expired" instead of a generic failure.
const (
	HermesCodeLoginRequired       = "HERMES_LOGIN_REQUIRED"
	HermesCodeLoginExpired        = "HERMES_LOGIN_EXPIRED"
	HermesCodeInvalidCredentials  = "HERMES_INVALID_CREDENTIALS"
	HermesCodeRateLimited         = "HERMES_RATE_LIMITED"
	HermesCodeProviderUnsupported = "HERMES_PROVIDER_UNSUPPORTED"
	HermesCodeProviderUnavailable = "HERMES_PROVIDER_UNAVAILABLE"
	HermesCodeProviderNotFound    = "HERMES_PROVIDER_NOT_FOUND"
	HermesCodeUnreachable         = "HERMES_UNREACHABLE"
)

// hermesAuthReasons 是 Hermes 认证失败的**唯一**一张对照表:哨兵 → 业务码 → 前端码。
//
// 三者必须一一对应:同一个失败,本机路径回业务码(中文一句话)、设备操作回前端码
// (线上传的那个串),而收到前端码的一侧还要翻回业务码。表散成两份的话,某一个原因
// 迟早会在一条路径上说成另一句话。
var hermesAuthReasons = []struct {
	sentinel error
	bizCode  int
	result   string
}{
	{hermes.ErrLoginRequired, code.HermesLoginRequired, HermesCodeLoginRequired},
	{hermes.ErrLoginExpired, code.HermesLoginExpired, HermesCodeLoginExpired},
	{hermes.ErrInvalidCredentials, code.HermesLoginRejected, HermesCodeInvalidCredentials},
	{hermes.ErrAuthRateLimited, code.HermesRateLimited, HermesCodeRateLimited},
	{hermes.ErrPasswordLoginUnsupported, code.HermesProviderUnsupported, HermesCodeProviderUnsupported},
	{hermes.ErrAuthProviderUnavailable, code.HermesProviderUnavailable, HermesCodeProviderUnavailable},
	{hermes.ErrAuthUnreachable, code.HermesUnreachable, HermesCodeUnreachable},
}

// hermesAuthCode maps the auth-layer sentinels to (business code, frontend code).
func hermesAuthCode(err error) (int, string, bool) {
	if err == nil {
		return 0, "", false
	}
	for _, reason := range hermesAuthReasons {
		if errors.Is(err, reason.sentinel) {
			return reason.bizCode, reason.result, true
		}
	}
	return 0, "", false
}

// hermesBizCode 是反向:绑定设备回的结构化结果码 → 本地化的业务码。
func hermesBizCode(resultCode string) (int, bool) {
	if resultCode == "" {
		return 0, false
	}
	for _, reason := range hermesAuthReasons {
		if reason.result == resultCode {
			return reason.bizCode, true
		}
	}
	return 0, false
}

// hermesAuthError turns an auth failure into the localized business error the
// Wails layer can prefix with its business code. It deliberately drops the raw
// error: only the mapped reason is user-facing, never the server's text.
func hermesAuthError(ctx context.Context, err error) error {
	if bizCode, _, ok := hermesAuthCode(err); ok {
		return i18n.NewError(ctx, bizCode)
	}
	return err
}

// ListHermesAuthProviders reads the serve's provider directory so the editor
// never hard-codes "basic".
func (s *agentBackendSvc) ListHermesAuthProviders(ctx context.Context, req *ListHermesAuthProvidersRequest) (*ListHermesAuthProvidersResponse, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	base, err := agent_backend_entity.NormalizeHermesURL(req.URL)
	if err != nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	deviceID, remote, err := boundCredentialDevice(ctx, devicefp.Carrier(strings.TrimSpace(req.DeviceID)))
	if err != nil {
		return nil, err
	}
	if remote {
		// 目录由那台设备去读:能不能连上这个 serve 是**它**的网络说了算。
		response, err := s.credentials().HermesAuthProviders(ctx, deviceID,
			&agentrewire.HermesAuthProvidersRequest{HermesUrl: base})
		if err != nil {
			return nil, remoteCredentialError(ctx, deviceID, err)
		}
		if resultCode := response.GetCode(); resultCode != "" {
			return nil, hermesCodeError(ctx, resultCode)
		}
		items := make([]HermesAuthProviderItem, 0, len(response.GetProviders()))
		for _, p := range response.GetProviders() {
			items = append(items, HermesAuthProviderItem{
				Name:             p.GetName(),
				DisplayName:      p.GetDisplayName(),
				SupportsPassword: p.GetSupportsPassword(),
			})
		}
		return &ListHermesAuthProvidersResponse{Providers: items}, nil
	}
	providers, err := hermes.ListAuthProviders(ctx, base, nil)
	if err != nil {
		return nil, hermesAuthError(ctx, err)
	}
	items := make([]HermesAuthProviderItem, 0, len(providers))
	for _, p := range providers {
		items = append(items, HermesAuthProviderItem{
			Name:             p.Name,
			DisplayName:      p.DisplayName,
			SupportsPassword: p.SupportsPassword,
		})
	}
	return &ListHermesAuthProvidersResponse{Providers: items}, nil
}

// LoginHermes runs the native PKCE chain, persists only the refresh token and
// returns the non-sensitive identity for display. The password lives only for
// the duration of this call.
func (s *agentBackendSvc) LoginHermes(ctx context.Context, req *LoginHermesRequest) (*LoginHermesResponse, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	base, err := agent_backend_entity.NormalizeHermesURL(req.URL)
	if err != nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	deviceID, remote, err := boundCredentialDevice(ctx, devicefp.Carrier(strings.TrimSpace(req.DeviceID)))
	if err != nil {
		return nil, err
	}
	if remote {
		// 密码只以内存形态穿过中继,登录由那台设备完成,refresh token 落在它那里(决策 3)。
		response, err := s.credentials().HermesLogin(ctx, deviceID, &agentrewire.HermesLoginRequest{
			HermesUrl: base, Provider: req.Provider, Username: req.Username, Password: req.Password,
		})
		if err != nil {
			return nil, remoteCredentialError(ctx, deviceID, err)
		}
		if resultCode := response.GetCode(); resultCode != "" {
			return nil, hermesCodeError(ctx, resultCode)
		}
		return &LoginHermesResponse{Provider: response.GetProvider(), UserID: response.GetUserId()}, nil
	}
	identity, err := s.credentialStore().Login(ctx, backendcred.HermesLogin{
		BaseURL:  base,
		Provider: req.Provider,
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		return nil, hermesAuthError(ctx, err)
	}
	return &LoginHermesResponse{Provider: identity.Provider, UserID: identity.UserID}, nil
}

// LogoutHermes drops the stored credential for the serve and clears the two
// display fields on a saved backend. Credentials are keyed by URL, so every
// backend pointing at the same serve shares the logout.
func (s *agentBackendSvc) LogoutHermes(ctx context.Context, req *LogoutHermesRequest) (*LogoutHermesResponse, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	rawURL := strings.TrimSpace(req.URL)
	device := devicefp.Carrier(strings.TrimSpace(req.DeviceID))
	if req.ID > 0 {
		row, err := agent_backend_repo.AgentBackend().Find(ctx, req.ID)
		if err != nil {
			return nil, err
		}
		if row == nil {
			return nil, i18n.NewError(ctx, code.AgentBackendNotFound)
		}
		if strings.TrimSpace(row.HermesURL) != "" {
			rawURL = row.HermesURL
		}
		// 保存行上的绑定设备说了算:凭据在那台机器上,与请求里带的草稿设备无关。
		device = row.DeviceFingerprint
		if strings.TrimSpace(row.HermesAuthProvider) != "" || strings.TrimSpace(row.HermesUserID) != "" {
			row.HermesAuthProvider = ""
			row.HermesUserID = ""
			row.Updatetime = s.now()
			if err := agent_backend_repo.AgentBackend().Update(ctx, row); err != nil {
				return nil, err
			}
			sync_svc.NotifyUpdate(ctx, syncwire.KindAgentBackend, row.ID, row.SyncMeta)
		}
	}
	if base, err := agent_backend_entity.NormalizeHermesURL(rawURL); err == nil && base != "" {
		deviceID, remote, err := boundCredentialDevice(ctx, device)
		if err != nil {
			return nil, err
		}
		if remote {
			if _, err := s.credentials().HermesLogout(ctx, deviceID,
				&agentrewire.HermesLogoutRequest{HermesUrl: base}); err != nil {
				return nil, remoteCredentialError(ctx, deviceID, err)
			}
			return &LogoutHermesResponse{}, nil
		}
		if err := s.credentialStore().Logout(base); err != nil {
			return nil, err
		}
	}
	return &LogoutHermesResponse{}, nil
}

// deleteHermesCredential is the delete-backend hook. The credential is keyed by
// URL and shared by every backend on this device pointing at the same serve, so
// it is cleared only when no remaining local backend still uses that URL. It is
// best-effort: a failure is logged and never blocks the delete, and when the
// remaining backends cannot be listed the login is kept rather than risk logging
// out another backend (a leftover credential is harmless).
func (s *agentBackendSvc) deleteHermesCredential(ctx context.Context, backend *agent_backend_entity.AgentBackend) {
	if backend == nil || !backend.IsHermes() || strings.TrimSpace(backend.HermesURL) == "" {
		return
	}
	base, err := agent_backend_entity.NormalizeHermesURL(backend.HermesURL)
	if err != nil {
		return
	}
	inUse, err := hermesURLUsedByAnotherBackendOn(ctx, backend.ID, base, backend.DeviceFingerprint)
	if err != nil {
		logger.Ctx(ctx).Warn("agent_backend delete: cannot list backends; keeping the shared hermes login",
			zap.Int64("id", backend.ID), zap.Error(err))
		return
	}
	if inUse {
		return
	}
	if err := s.credentialStore().Logout(base); err != nil {
		logger.Ctx(ctx).Warn("agent_backend delete: hermes keychain delete failed; the credential is keyed by URL, so a leak is harmless",
			zap.Int64("id", backend.ID), zap.Error(err))
	}
}

// hermesURLUsedByAnotherBackendOn reports whether an active Hermes backend other
// than deletedID, bound to the same device, points at the normalized URL. The
// question is per device because the credential is: one serve, one device, one
// login (decision 5), and logging out would hit every backend sharing it.
func hermesURLUsedByAnotherBackendOn(
	ctx context.Context, deletedID int64, base string, device devicefp.Carrier,
) (bool, error) {
	rows, err := agent_backend_repo.AgentBackend().List(ctx)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row == nil || row.ID == deletedID || !row.IsHermes() ||
			!sameBoundDevice(row.DeviceFingerprint, device) {
			continue
		}
		if other, err := agent_backend_entity.NormalizeHermesURL(row.HermesURL); err == nil && other == base {
			return true, nil
		}
	}
	return false, nil
}

// sameBoundDevice 判断两个后端是否绑在同一台设备上。空指纹与本机指纹都读作「本机」
// (R13 认领前后的两种写法),其余按指纹逐字比。
func sameBoundDevice(a, b devicefp.Carrier) bool {
	if !remote_device_svc.TargetsAnotherMachine(a) && !remote_device_svc.TargetsAnotherMachine(b) {
		return true
	}
	return a == b
}

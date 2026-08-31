package server_svc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"runtime"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/buildinfo"
	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
)

// keychainAccountName 是 hub refresh_token 在 OS keychain 中挂的账号名。
// 桌面端一台机器只有一份联机凭证，所以全局共用一个常量。
const keychainAccountName = "agentre.server.refresh_token"

// accountForDeviceFingerprint 是桌面端唯一设备指纹的 keychain 账号,与
// remote_device_svc.accountForDeviceFingerprint（及 watcher_svc 的
// keychainFingerprintAccount）必须一致——R5 决策 8:账号侧不得另生成指纹。
const accountForDeviceFingerprint = "agentre-device-fingerprint"

// osHostname 是设备显示名的取值口，抽成变量供测试替换。
var osHostname = os.Hostname

// ---- request / response DTOs（私有，只在本包内用）----

type deviceAuthorizeReq struct {
	DeviceKind  string `json:"device_kind"`
	Fingerprint string `json:"fingerprint"`
	Platform    string `json:"platform"`
	Version     string `json:"version"`
	// Name 是设备列表里的显示名，取本机主机名。设备流是账号侧拿到它的唯一途径：
	// 不带上，服务端只能拿指纹缩写当名字，账号下每台机器就都长得一样了。
	Name string `json:"name"`
}

type deviceAuthorizeResp struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	Interval                int    `json:"interval"`
	ExpiresIn               int    `json:"expires_in"`
}

type deviceTokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	DeviceID     int64  `json:"device_id"`
}

// pollEnvelope 比通用 envelope 多一个 error 字段（device flow 在 4xx 时会带）。
type pollEnvelope struct {
	Code  int             `json:"code"`
	Msg   string          `json:"msg"`
	Data  deviceTokenResp `json:"data"`
	Error string          `json:"error"`
}

type meResp struct {
	UserID      int64  `json:"user_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
	GithubLogin string `json:"github_login"`
	DeviceID    int64  `json:"device_id"`
}

// StartLogin handshakes / authorizes / persists URL + fingerprint.
func (s *service) StartLogin(ctx context.Context, serverURL string) (*StartLoginResult, error) {
	s.mu.Lock()
	if s.loginInFlight {
		s.mu.Unlock()
		return nil, ErrAlreadyInProgress
	}
	s.loginInFlight = true
	s.mu.Unlock()
	defer s.markLoginDone()

	logger.Ctx(ctx).Info("server_svc.StartLogin: device-flow authorize starting",
		zap.String("serverURL", serverURL))

	// 状态先读、闸门先过，**再**动 client：下面那行会就地把 client 换成新地址并清空
	// access token，而此刻用户连批准都还没做。已登录时走完这一段的收场是一次静默
	// 登出——手上是上一套 server 的 refresh_token，client 却指着新的，下一次 401
	// 触发的刷新会被判成 invalid_grant（凭据被明确拒绝），withAuth 据此清掉本地登录。
	//
	// 界面现在也挡着它（登录入口只在登出时出现），但一条不变量不能只由界面守着；
	// agentred 那边同源的闸门（已认领必须先 unclaim）一直在后端。
	row, err := server_state_repo.ServerState().Get(ctx)
	if err != nil {
		return nil, err
	}
	if row == nil {
		row = &server_state_entity.ServerState{ID: 1}
	}
	if row.IsLoggedIn() {
		logger.Ctx(ctx).Warn("server_svc.StartLogin: refused, this desktop is already signed in",
			zap.String("currentServerURL", row.ServerURL), zap.String("requestedServerURL", serverURL))
		return nil, ErrAlreadyLoggedIn
	}

	// rebuild client at the requested hub URL (the bootstrap-time client may be empty)
	s.setClient(NewHTTPClient(serverURL, ""))

	if _, err := s.getClient().Healthcheck(ctx); err != nil {
		logger.Ctx(ctx).Warn("server_svc.StartLogin: hub Healthcheck failed",
			zap.String("serverURL", serverURL), zap.Error(err))
		return nil, err
	}
	// R5 决策 8:桌面端指纹一律复用 LAN 配对 keychain 指纹,不另生成随机值。
	// server_state 里的指纹必须与 keychain 一致(硬不变量),因此旧安装遗留的
	// 随机指纹在此被覆盖为 keychain 值。
	fp, err := s.ensureDeviceFingerprint()
	if err != nil {
		return nil, err
	}
	if row.DeviceFingerprint != fp {
		row.DeviceFingerprint = fp
		// Persist eagerly so a later Save failure doesn't cause the next StartLogin
		// to generate a different fingerprint (which would orphan the hub's pending
		// authorization).
		if err := server_state_repo.ServerState().Save(ctx, row); err != nil {
			return nil, err
		}
	}
	row.ServerURL = serverURL

	// 主机名取不到不影响登录：空串上报，服务端回退到指纹缩写。
	hostname, _ := osHostname()
	req := deviceAuthorizeReq{
		DeviceKind:  "desktop",
		Fingerprint: row.DeviceFingerprint,
		Platform:    runtimePlatform(),
		Version:     buildVersion(),
		Name:        hostname,
	}
	var env envelope[deviceAuthorizeResp]
	if _, err := s.getClient().do(ctx, http.MethodPost, "/v1/oauth/device/authorize", req, &env); err != nil {
		return nil, err
	}
	if env.Code != 0 {
		return nil, ErrServerUnreachable
	}

	if err := server_state_repo.ServerState().Save(ctx, row); err != nil {
		return nil, err
	}

	return &StartLoginResult{
		DeviceCode:              env.Data.DeviceCode,
		UserCode:                env.Data.UserCode,
		VerificationURI:         env.Data.VerificationURI,
		VerificationURIComplete: env.Data.VerificationURIComplete,
		Interval:                env.Data.Interval,
		ExpiresIn:               env.Data.ExpiresIn,
	}, nil
}

// PollLoginToken polls /v1/oauth/device/token and reports whether the user has approved.
func (s *service) PollLoginToken(ctx context.Context, deviceCode string) (bool, error) {
	body := map[string]string{
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		"device_code": deviceCode,
	}
	var env pollEnvelope
	status, err := s.getClient().do(ctx, http.MethodPost, "/v1/oauth/device/token", body, &env)
	if err != nil {
		// the do() helper wraps connection errors as ErrServerUnreachable already.
		// For 4xx with an "error" field, do() may return an *httpErr — that's fine,
		// we still get to read env.Error / env.Code below if the body decoded.
		if env.Error == "" {
			return false, err
		}
	}

	switch env.Error {
	case "authorization_pending", "slow_down":
		return false, nil
	case "expired_token":
		return false, ErrLoginExpired
	case "access_denied":
		return false, ErrAccessDenied
	}

	if status != http.StatusOK || env.Code != 0 {
		return false, ErrServerUnreachable
	}

	// Success path: persist refresh token in keychain, set access token on client.
	if err := keychain.Default().Set(keychainAccountName, env.Data.RefreshToken); err != nil {
		return false, err
	}
	s.getClient().SetAccessToken(env.Data.AccessToken)

	row, err := server_state_repo.ServerState().Get(ctx)
	if err != nil {
		return false, err
	}
	if row == nil {
		row = &server_state_entity.ServerState{ID: 1}
	}
	row.DeviceID = env.Data.DeviceID
	row.KeychainAccount = keychainAccountName

	if me, mErr := s.fetchMe(ctx); mErr == nil && me != nil {
		row.ServerUserID = me.UserID
	}

	if err := server_state_repo.ServerState().Save(ctx, row); err != nil {
		return false, err
	}
	logger.Ctx(ctx).Info("server_svc.PollLoginToken: login completed",
		zap.Int64("deviceID", row.DeviceID),
		zap.Int64("userID", row.ServerUserID))
	s.emit(map[string]any{"kind": "logged_in", "state": row})
	return true, nil
}

// CancelLogin clears the in-flight flag so the user can retry StartLogin.
func (s *service) CancelLogin(_ context.Context) error {
	s.markLoginDone()
	return nil
}

// fetchMe pulls /v1/auth/me — assumes the access token has already been set on the client.
func (s *service) fetchMe(ctx context.Context) (*meResp, error) {
	var env envelope[meResp]
	if _, err := s.getClient().do(ctx, http.MethodGet, "/v1/auth/me", nil, &env); err != nil {
		return nil, err
	}
	if env.Code != 0 {
		return nil, ErrServerUnreachable
	}
	return &env.Data, nil
}

func (s *service) markLoginDone() {
	s.mu.Lock()
	s.loginInFlight = false
	s.mu.Unlock()
}

// ensureDeviceFingerprint returns the desktop's canonical device fingerprint,
// creating it on first use with the same pattern remote_device_svc uses
// (add.go ensureDeviceFingerprint): keychain read → generate sha256 → persist.
// The generated value therefore matches the LAN pairing fingerprint byte-for-byte.
func (s *service) ensureDeviceFingerprint() (string, error) {
	fp, err := keychain.Default().Get(accountForDeviceFingerprint)
	if err == nil && fp != "" {
		return fp, nil
	}
	if err != nil && !errors.Is(err, keychain.ErrNotFound) {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	newFP := "sha256:" + hex.EncodeToString(sum[:])
	if err := keychain.Default().Set(accountForDeviceFingerprint, newFP); err != nil {
		return "", err
	}
	return newFP, nil
}

// runtimePlatform returns "<GOOS>/<GOARCH>" for the device_authorize payload.
func runtimePlatform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// buildVersion returns the application version. Falls back to "dev" when no
// build-time ldflag injection is present.
func buildVersion() string {
	if buildinfo.CommitID != "" {
		return "dev+" + buildinfo.ShortCommitID()
	}
	return "dev"
}

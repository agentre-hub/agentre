package fakes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/bootstrap"
	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
)

// The local end-to-end sync suite needs this app to come up already connected to
// a server. Login normally goes through RFC 8628 Device Flow whose terminus is
// GitHub OAuth — nobody can click that in an e2e — so the runner seeds the
// account, the device row and one refresh token straight into the server's
// PostgreSQL and hands the resulting identity to this process through env.
//
// What is seeded here is exactly what a completed login leaves behind: the
// single server_state row plus the two keychain entries. The access token is NOT
// seeded — bootstrap.ServerBoot trades the refresh token for one over real HTTP
// at startup, so a bad credential fails loudly instead of yielding a half-logged-in
// app.
const (
	e2eServerURLEnv    = "AGENTRE_E2E_SERVER_URL"
	e2eServerUserIDEnv = "AGENTRE_E2E_SERVER_USER_ID"
	e2eDeviceIDEnv     = "AGENTRE_E2E_DEVICE_ID"
	e2eDeviceFPEnv     = "AGENTRE_E2E_DEVICE_FINGERPRINT"
	e2eRefreshTokenEnv = "AGENTRE_E2E_REFRESH_TOKEN" //nolint:gosec // G101: environment-variable name, not a credential
)

// keychainAccountRefreshToken / keychainAccountFingerprint mirror the constants
// server_svc/login.go keeps unexported (keychainAccountName,
// accountForDeviceFingerprint). They are part of the on-disk login shape, so a
// drift between the two would show up as "the seeded app is not logged in" on
// the very first spec.
const (
	keychainAccountRefreshToken = "agentre.server.refresh_token" //nolint:gosec // G101: keychain account identifier, not a credential
	keychainAccountFingerprint  = "agentre-device-fingerprint"
)

// installE2ELoggedInAccount makes this app instance a logged-in desktop of the
// account the runner seeded. Absent env (every other e2e suite) it is a no-op,
// so the committed core-flow suite keeps running fully offline.
func installE2ELoggedInAccount(ctx context.Context) error {
	baseURL := strings.TrimSpace(os.Getenv(e2eServerURLEnv))
	if baseURL == "" {
		return nil
	}
	userID, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(e2eServerUserIDEnv)), 10, 64)
	if err != nil {
		return fmt.Errorf("parse server user id: %w", err)
	}
	if userID <= 0 {
		return errors.New("server user id must be positive")
	}
	deviceID, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(e2eDeviceIDEnv)), 10, 64)
	if err != nil {
		return fmt.Errorf("parse device id: %w", err)
	}
	if deviceID <= 0 {
		return errors.New("device id must be positive")
	}
	fingerprint := strings.TrimSpace(os.Getenv(e2eDeviceFPEnv))
	refreshToken := strings.TrimSpace(os.Getenv(e2eRefreshTokenEnv))
	if fingerprint == "" || refreshToken == "" {
		return errors.New("missing fingerprint or refresh token")
	}

	kc := keychain.Default()
	if kc == nil {
		return errors.New("no keychain backend installed")
	}
	// Prefer whatever is already in the keychain over the seeded env value.
	// `wails dev` runs this same binary a second time to generate the frontend
	// bindings, so Install executes twice per run; refresh tokens rotate on use,
	// and replaying the spent env token in the second process is "refresh token
	// reuse detected". The keychain dir is shared by both processes, so the last
	// rotation always wins — exactly how a real desktop behaves across restarts.
	if stored, kerr := kc.Get(keychainAccountRefreshToken); kerr == nil && stored != "" {
		refreshToken = stored
	}

	// Trade the refresh token for an access token before anything else can touch
	// the network. Doing it here — rather than leaving it to bootstrap.ServerBoot
	// — means a bad seeded credential fails startup before later sync assertions
	// obscure the actual composition failure.
	access, rotated, err := exchangeRefreshToken(ctx, baseURL, refreshToken)
	if err != nil {
		return fmt.Errorf("initial token exchange: %w", err)
	}
	// Refresh tokens rotate on every use: persist the NEW one, or the next
	// refresh (ServerBoot's) would replay a spent token and log this desktop out.
	if err := kc.Set(keychainAccountRefreshToken, rotated); err != nil {
		return fmt.Errorf("store refresh token: %w", err)
	}
	// server_svc.login asserts server_state.device_fingerprint equals this entry.
	if err := kc.Set(keychainAccountFingerprint, fingerprint); err != nil {
		return fmt.Errorf("store device fingerprint: %w", err)
	}

	if err := server_state_repo.ServerState().Save(ctx, &server_state_entity.ServerState{
		ID:                1,
		ServerURL:         baseURL,
		DeviceID:          deviceID,
		DeviceFingerprint: fingerprint,
		ServerUserID:      userID,
		KeychainAccount:   keychainAccountRefreshToken,
	}); err != nil {
		return fmt.Errorf("persist server state: %w", err)
	}

	// bootstrap.InitServer already built server_svc from the (then empty)
	// server_url, and sync_svc on top of it. Rebuild both now that the row points
	// somewhere: the http client caches its base URL at construction time.
	svc := server_svc.New(server_svc.NewHTTPClient(baseURL, access), nil)
	server_svc.SetDefault(svc)
	sync_svc.SetDefault(sync_svc.New(svc))

	// 登录夹具换过 keychain 并重绑了 server_svc:boot 时 InitRemoteDevice 捕获的是当时
	// (系统)keychain 与旧 server_svc。不重建的话,随后 seed 的本机 backend 会认领 boot
	// 旧指纹,remote-agentred 装配后 self identity 与登录身份分叉,本地轮误走未配对
	// agentred。生产登录复用既有 keychain,不会触发这个分叉；重建只属于 harness。
	if err := rebindRemoteDeviceAfterLogin(ctx); err != nil {
		return fmt.Errorf("rebind remote device: %w", err)
	}

	logger.Ctx(ctx).Info("e2efakes.login: seeded a logged-in desktop",
		zap.String("serverURL", baseURL), zap.Int64("deviceId", deviceID))
	return nil
}

// rebindRemoteDeviceAfterLogin 按**当前** keychain 与 server_svc 重建 remote_device_svc。
// 它必须在 seed 本机 backend 之前执行(install.go 在 installE2ELoggedInAccount 之后才建
// 本机档),并且是 remote agentred 播种不得再触碰 remote_device_svc 的前提 —— 播种时
// ConnPool 已经持有登录后重绑的 server_svc,self identity 不会再变。
func rebindRemoteDeviceAfterLogin(ctx context.Context) error {
	return bootstrap.InitRemoteDevice(ctx)
}

// exchangeRefreshToken performs the same POST /v1/oauth/token/refresh the desktop
// does. Rejection errors retain status/code but never echo the credential-bearing
// response body into logs or preserved artifacts.
func exchangeRefreshToken(ctx context.Context, baseURL, refreshToken string) (access, rotated string, err error) {
	body, err := json.Marshal(map[string]string{"refresh_token": refreshToken})
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext( //nolint:gosec // G704: dedicated E2E composition trusts the runner-provided loopback fake URL
		ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/oauth/token/refresh", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req) //nolint:gosec // G704: request URL is the trusted E2E endpoint above
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"data"`
	}
	if jerr := json.Unmarshal(raw, &envelope); jerr != nil {
		return "", "", fmt.Errorf("refresh rejected: status %d invalid response", resp.StatusCode)
	}
	if envelope.Code != 0 {
		return "", "", fmt.Errorf("refresh rejected: status %d code %d", resp.StatusCode, envelope.Code)
	}
	if envelope.Data.AccessToken == "" || envelope.Data.RefreshToken == "" {
		return "", "", fmt.Errorf("refresh rejected: status %d incomplete token response", resp.StatusCode)
	}
	return envelope.Data.AccessToken, envelope.Data.RefreshToken, nil
}

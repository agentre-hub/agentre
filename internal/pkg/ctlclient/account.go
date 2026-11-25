package ctlclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/agrctlcred"
	"github.com/agentre-hub/agentre/internal/pkg/cagoenvelope"
)

// DefaultServerURL 是 agrctl login 不带 --server 时的账号 server，与桌面端登录框的默认值相同。
const DefaultServerURL = "https://app.agentrehub.com"

// 账号执行者对外给出的失败。消息就是给人看的那句话，ctlcmd 原样打印。
var (
	// ErrLoginExpired：refresh token 失效或本设备已被吊销，只能重新登录。
	ErrLoginExpired = errors.New("the agrctl login has expired or this device was revoked — run 'agrctl login' again")
	// ErrNotLoggedIn：没有登录凭据（例如另一个进程刚刚 logout）。
	ErrNotLoggedIn = errors.New("agrctl is not logged in — run 'agrctl login'")
	// ErrTokenRejected：server 不认这枚个人 token（吊销、过期或不存在）。个人 token 不刷新。
	ErrTokenRejected = errors.New("the server rejected the personal token (revoked, expired or unknown)")

	ErrAuthorizationPending = errors.New("authorization pending")
	ErrSlowDown             = errors.New("slow down")
	ErrAccessDenied         = errors.New("the login was denied in the browser")
	ErrAuthorizationExpired = errors.New("the login was not approved in time — run 'agrctl login' again")
)

// UnreachableError 是够不着账号 server（拨号失败、超时、连接被断）。
type UnreachableError struct {
	Server string
	Err    error
}

func (e *UnreachableError) Error() string {
	return fmt.Sprintf("cannot reach the account server %s: %v", e.Server, e.Err)
}

func (e *UnreachableError) Unwrap() error { return e.Err }

// accessTokenSkew 是 access token 到期前提前刷新的余量，免得请求在路上过期。
const accessTokenSkew = 30 * time.Second

// accountHTTP 是对账号 server 的所有请求共用的 client。
var accountHTTP = &http.Client{Timeout: 60 * time.Second}

// DeviceLoginRequest 是 agrctl 作为 `cli` 设备申请 Device Flow 授权时自报的身份。
type DeviceLoginRequest struct {
	Fingerprint string
	Platform    string
	Version     string
	Name        string
}

// DeviceAuthorization 是 /v1/oauth/device/authorize 的应答。
type DeviceAuthorization struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	Interval                int    `json:"interval"`
	ExpiresIn               int    `json:"expires_in"`
}

// DeviceTokens 是批准后 /v1/oauth/device/token 发下的一对凭据。
type DeviceTokens struct {
	AccessToken      string `json:"access_token"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
	DeviceID         int64  `json:"device_id"`
}

// AccountIdentity 是 /v1/auth/me 认出的账号。
type AccountIdentity struct {
	UserID      int64  `json:"user_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	GithubLogin string `json:"github_login"`
}

// Label 是给人看的账号名：邮箱 > 显示名 > GitHub 登录名 > #id。
func (a *AccountIdentity) Label() string {
	for _, v := range []string{a.Email, a.DisplayName, a.GithubLogin} {
		if v != "" {
			return v
		}
	}
	return fmt.Sprintf("#%d", a.UserID)
}

// AuthorizeDevice 发起 Device Flow：本机 agrctl 以 `cli` 设备、自己的指纹申请授权。
func AuthorizeDevice(ctx context.Context, server string, req DeviceLoginRequest) (*DeviceAuthorization, error) {
	var out DeviceAuthorization
	status, raw, err := callServer(ctx, server, http.MethodPost, "/v1/oauth/device/authorize", "", map[string]string{
		"device_kind": "cli",
		"fingerprint": req.Fingerprint,
		"platform":    req.Platform,
		"version":     req.Version,
		"name":        req.Name,
	})
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, &ServerError{Status: status, Message: accountErrMsg(raw)}
	}
	if err := cagoenvelope.Decode(raw, &out); err != nil {
		return nil, fmt.Errorf("decode device authorization: %w", err)
	}
	if out.DeviceCode == "" || out.UserCode == "" || out.ExpiresIn <= 0 {
		return nil, errors.New("the account server returned an invalid device authorization")
	}
	return &out, nil
}

// PollDeviceToken 轮询一次授权结果。还没批准时返回 ErrAuthorizationPending / ErrSlowDown，
// 被拒或过期时返回 ErrAccessDenied / ErrAuthorizationExpired。
func PollDeviceToken(ctx context.Context, server, deviceCode string) (*DeviceTokens, error) {
	status, raw, err := callServer(ctx, server, http.MethodPost, "/v1/oauth/device/token", "", map[string]string{
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		"device_code": deviceCode,
	})
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		switch oauthError(raw) {
		case "authorization_pending":
			return nil, ErrAuthorizationPending
		case "slow_down":
			return nil, ErrSlowDown
		case "access_denied":
			return nil, ErrAccessDenied
		case "expired_token", "invalid_grant":
			return nil, ErrAuthorizationExpired
		}
		return nil, &ServerError{Status: status, Message: accountErrMsg(raw)}
	}
	var out DeviceTokens
	if err := cagoenvelope.Decode(raw, &out); err != nil {
		return nil, fmt.Errorf("decode device token: %w", err)
	}
	if out.AccessToken == "" || out.RefreshToken == "" || out.ExpiresIn <= 0 {
		return nil, errors.New("the account server returned an invalid device token")
	}
	return &out, nil
}

// FetchIdentity 问 server 这枚 access token 属于哪个账号（凭据是不透明的，本地读不出来）。
func FetchIdentity(ctx context.Context, server, accessToken string) (*AccountIdentity, error) {
	status, raw, err := callServer(ctx, server, http.MethodGet, "/v1/auth/me", accessToken, nil)
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, &ServerError{Status: status, Message: accountErrMsg(raw)}
	}
	var me AccountIdentity
	if err := cagoenvelope.Decode(raw, &me); err != nil || me.UserID <= 0 {
		return nil, errors.New("the account server did not identify the account")
	}
	return &me, nil
}

// Account 是账号执行者的传输层：带 Bearer 调账号 server。凭据要么是一枚个人 token（原样用、
// 不刷新），要么是 agrctlcred 里的登录态（access token 过期或撞 401 时用 refresh token 换新）。
type Account struct {
	// Server 是账号 server 的 base URL（无结尾 /）。
	Server   string
	personal string
	dir      string
	now      func() time.Time
}

// PersonalTokenAccount 用一枚个人 token 连 server。
func PersonalTokenAccount(server, token string) *Account {
	return &Account{Server: strings.TrimRight(server, "/"), personal: token, now: time.Now}
}

// LoginAccount 用 dir 里保存的登录态连 server（server 取自凭据）。没有登录时返回的错误满足
// errors.Is(err, os.ErrNotExist)。
func LoginAccount(dir string) (*Account, error) {
	c, err := agrctlcred.Load(dir)
	if err != nil {
		return nil, err
	}
	return &Account{Server: strings.TrimRight(c.ServerURL, "/"), dir: dir, now: time.Now}, nil
}

// PersonalToken 报告这个执行者用的是个人 token（否则是登录态）。
func (a *Account) PersonalToken() bool { return a.personal != "" }

// PostRaw 发一个带 Bearer 的 JSON POST，返回原始响应体；非 2xx 返回 *ServerError。
func (a *Account) PostRaw(ctx context.Context, path string, body []byte) ([]byte, error) {
	return a.authed(ctx, http.MethodPost, path, body)
}

// GetRaw 发一个带 Bearer 的 GET，返回原始响应体（cago 外壳由调用方解）；非 2xx 返回 *ServerError。
func (a *Account) GetRaw(ctx context.Context, pathAndQuery string) ([]byte, error) {
	return a.authed(ctx, http.MethodGet, pathAndQuery, nil)
}

// DeleteRaw 发一个带 Bearer 的 DELETE，返回原始响应体；非 2xx 返回 *ServerError。
func (a *Account) DeleteRaw(ctx context.Context, path string) ([]byte, error) {
	return a.authed(ctx, http.MethodDelete, path, nil)
}

// PatchRaw 发一个带 Bearer 的 JSON PATCH，返回原始响应体；非 2xx 返回 *ServerError。
func (a *Account) PatchRaw(ctx context.Context, path string, body []byte) ([]byte, error) {
	return a.authed(ctx, http.MethodPatch, path, body)
}

// Bearer 交出此刻该用的那枚凭据：relay 握手（Authorization 与 auth.account 的 credential）
// 用的就是它。登录态快过期时先刷新。
func (a *Account) Bearer(ctx context.Context) (string, error) { return a.bearer(ctx) }

// Reauthorize 在对端拒收 stale（relay 回 401）之后换一枚新的。个人 token 不刷新，
// 返回 ErrTokenRejected。
func (a *Account) Reauthorize(ctx context.Context, stale string) (string, error) {
	if a.PersonalToken() {
		return "", ErrTokenRejected
	}
	return a.refresh(ctx, stale)
}

// DeviceName 是本执行者在对端那里的来源名（会话里「来自 <设备>」）：登录态是这台 cli
// 设备的名字，个人 token 没有设备，记作 agrctl。
func (a *Account) DeviceName() string {
	if !a.PersonalToken() {
		if c, err := agrctlcred.Load(a.dir); err == nil && strings.TrimSpace(c.DeviceName) != "" {
			return c.DeviceName
		}
	}
	return "agrctl"
}

// Fingerprint 是登录态这台 cli 设备的指纹——它发起的会话就记在这个发起方名下。个人 token
// 没有设备，交回空串（它的指纹要问 server）。
func (a *Account) Fingerprint() string {
	if a.PersonalToken() {
		return ""
	}
	if c, err := agrctlcred.Load(a.dir); err == nil {
		return strings.TrimSpace(c.Fingerprint)
	}
	return ""
}

// Revoke 在 server 吊销 deviceID（agrctl logout 用它吊销本 `cli` 设备）。
func (a *Account) Revoke(ctx context.Context, deviceID int64) error {
	body, _ := json.Marshal(map[string]int64{"device_id": deviceID})
	_, err := a.authed(ctx, http.MethodPost, "/v1/oauth/token/revoke", body)
	return err
}

// authed 带着当前 access token 发请求；撞 401 时（登录态）刷新一次再重试。
func (a *Account) authed(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	token, err := a.bearer(ctx)
	if err != nil {
		return nil, err
	}
	status, raw, err := a.send(ctx, method, path, token, body)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized {
		if a.PersonalToken() {
			return nil, ErrTokenRejected
		}
		if token, err = a.refresh(ctx, token); err != nil {
			return nil, err
		}
		if status, raw, err = a.send(ctx, method, path, token, body); err != nil {
			return nil, err
		}
	}
	if status >= 300 {
		return nil, &ServerError{Status: status, Message: accountErrMsg(raw)}
	}
	return raw, nil
}

func (a *Account) send(ctx context.Context, method, path, token string, body []byte) (int, []byte, error) {
	var payload any
	if body != nil {
		payload = json.RawMessage(body)
	}
	return callServer(ctx, a.Server, method, path, token, payload)
}

// bearer 交出本次请求用的 access token：个人 token 原样；登录态用缓存的、快过期就先刷新。
func (a *Account) bearer(ctx context.Context) (string, error) {
	if a.PersonalToken() {
		return a.personal, nil
	}
	c, err := a.load()
	if err != nil {
		return "", err
	}
	if c.AccessToken != "" && a.fresh(c) {
		return c.AccessToken, nil
	}
	return a.refresh(ctx, c.AccessToken)
}

func (a *Account) fresh(c *agrctlcred.Credentials) bool {
	return a.now().Add(accessTokenSkew).Unix() < c.AccessExpiresAt
}

func (a *Account) load() (*agrctlcred.Credentials, error) {
	c, err := agrctlcred.Load(a.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotLoggedIn
		}
		return nil, err
	}
	if c.Expired() {
		return nil, ErrLoginExpired
	}
	return c, nil
}

// refresh 用 refresh token 换一对新的。stale 是调用方手上被判过期的那枚 access token。
//
// refresh token 是一次性的、重放会吊销整条链，所以并发的 agrctl 进程必须串行刷新：先取
// 跨进程锁，再重读凭据——存着的 access token 已经不是 stale 且仍有效，说明别的进程刚刷过，
// 直接用它。换到新的一对后先落盘再用。
func (a *Account) refresh(ctx context.Context, stale string) (string, error) {
	unlock, err := agrctlcred.Lock(a.dir)
	if err != nil {
		return "", err
	}
	defer unlock()
	c, err := a.load()
	if err != nil {
		return "", err
	}
	if c.AccessToken != "" && c.AccessToken != stale && a.fresh(c) {
		return c.AccessToken, nil
	}
	if c.RefreshExpiresAt > 0 && a.now().Unix() >= c.RefreshExpiresAt {
		return "", a.markExpired(c)
	}
	status, raw, err := callServer(ctx, a.Server, http.MethodPost, "/v1/oauth/token/refresh", "",
		map[string]string{"refresh_token": c.RefreshToken})
	if err != nil {
		return "", err
	}
	if credentialRejected(status, raw) {
		return "", a.markExpired(c)
	}
	if status >= 300 {
		return "", &ServerError{Status: status, Message: accountErrMsg(raw)}
	}
	var out DeviceTokens
	if err := cagoenvelope.Decode(raw, &out); err != nil || out.AccessToken == "" || out.RefreshToken == "" {
		return "", errors.New("the account server returned an invalid token refresh")
	}
	now := a.now()
	c.AccessToken, c.RefreshToken = out.AccessToken, out.RefreshToken
	c.AccessExpiresAt = now.Add(time.Duration(out.ExpiresIn) * time.Second).Unix()
	c.RefreshExpiresAt = 0
	if out.RefreshExpiresIn > 0 {
		c.RefreshExpiresAt = now.Add(time.Duration(out.RefreshExpiresIn) * time.Second).Unix()
	}
	if err := agrctlcred.Save(a.dir, c); err != nil {
		return "", err
	}
	return c.AccessToken, nil
}

// markExpired 把登录标成已作废：清掉两枚凭据、留下账号信息，交回 ErrLoginExpired。
func (a *Account) markExpired(c *agrctlcred.Credentials) error {
	c.RefreshToken, c.RefreshExpiresAt, c.AccessToken, c.AccessExpiresAt = "", 0, "", 0
	if err := agrctlcred.Save(a.dir, c); err != nil {
		return err
	}
	return ErrLoginExpired
}

// credentialRejected：server 明确说这份凭据不作数了（OAuth invalid_grant / invalid_client，
// 或 401 / 403）。与桌面端 server_svc.credentialRejection 同一判据。
func credentialRejected(status int, raw []byte) bool {
	if status < 300 {
		return false
	}
	if e := oauthError(raw); e == "invalid_grant" || e == "invalid_client" {
		return true
	}
	return status == http.StatusUnauthorized || status == http.StatusForbidden
}

// callServer 发一次请求并读完响应体。拨号 / 传输层失败返回 *UnreachableError。
func callServer(ctx context.Context, server, method, path, token string, body any) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(server, "/")+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := accountHTTP.Do(req)
	if err != nil {
		return 0, nil, &UnreachableError{Server: server, Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return 0, nil, &UnreachableError{Server: server, Err: err}
	}
	return resp.StatusCode, raw, nil
}

// oauthError 取响应里的 RFC 6749 error 码（cago 外壳的顶层或 data 里都认）。
func oauthError(raw []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	_ = cagoenvelope.Decode(raw, &e)
	return e.Error
}

// accountErrMsg 从 {"error": …}（ctl 接口）或 cago 外壳的 msg 里取消息，都没有就回原始文本。
func accountErrMsg(raw []byte) string {
	var e struct {
		Error string `json:"error"`
		Msg   string `json:"msg"`
	}
	if json.Unmarshal(raw, &e) == nil {
		if e.Error != "" {
			return e.Error
		}
		if e.Msg != "" {
			return e.Msg
		}
	}
	return strings.TrimSpace(string(raw))
}

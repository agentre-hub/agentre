package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/agentre-ai/agentre/internal/daemon/rpc"
	"github.com/agentre-ai/agentre/internal/daemon/state"
	"github.com/agentre-ai/agentre/internal/pkg/paths"
)

const (
	defaultLoginPollInterval = 5 * time.Second
	slowDownPollIncrement    = 5 * time.Second
)

type loginHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type loginDeps struct {
	dataDir     func() (string, error)
	http        loginHTTPDoer
	openBrowser func(string) error
	wait        func(time.Duration) error
	platform    string
	version     string
	// hostname 提供设备列表里的显示名。取不到时按空串上报，由服务端回退到指纹
	// 缩写——一个取不到的显示名不该让整次登录失败。
	hostname func() (string, error)
	// daemonRunning 报告本机是否有 daemon 正持有 state.json（见 statefile.go）。
	daemonRunning func() bool
}

type deviceAuthorizeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	Interval                int    `json:"interval"`
	ExpiresIn               int    `json:"expires_in"`
}

type deviceTokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
	DeviceID         int64  `json:"device_id"`
}

type publicKeyResponse struct {
	CurrentKID              string            `json:"current_kid"`
	Keys                    map[string]string `json:"keys"`
	PublicKey               string            `json:"public_key"`
	MaxTokenLifetimeSeconds int64             `json:"max_token_lifetime_seconds"`
}

type oauthErrorResponse struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
}

func newLoginCmd() *cobra.Command {
	return newLoginCmdWithDeps(loginDeps{
		dataDir:     paths.AgentredDataDir,
		http:        &http.Client{Timeout: 15 * time.Second},
		openBrowser: openBrowser,
		wait: func(delay time.Duration) error {
			time.Sleep(delay)
			return nil
		},
		platform:      runtime.GOOS,
		version:       agentredBuildIdentity(),
		hostname:      os.Hostname,
		daemonRunning: daemonIsRunning,
	})
}

func newLoginCmdWithDeps(deps loginDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Claim this daemon through account device authorization",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// 闸门放在**网络之前**：设备流一旦发起就在服务端占了一个待批准的
			// 授权，而这次登录的结果根本存不下来。
			if err := requireNoRunningDaemon(deps.daemonRunning); err != nil {
				return err
			}
			dir, err := deps.dataDir()
			if err != nil {
				return err
			}
			st, err := state.Load(dir)
			if err != nil {
				return err
			}
			if st.IsClaimed() {
				return fmt.Errorf("daemon is already claimed; run agentred unclaim before logging into another account")
			}
			serverURL, err := cmd.Flags().GetString("server")
			if err != nil {
				return err
			}
			serverURL, err = validServerURL(serverURL)
			if err != nil {
				return err
			}

			return login(cmd, deps, st, serverURL)
		},
	}
	cmd.Flags().String("server", strings.TrimSpace(os.Getenv("AGENTRED_SERVER_URL")), "account server base URL (or AGENTRED_SERVER_URL)")
	return cmd
}

func login(cmd *cobra.Command, deps loginDeps, st *state.State, serverURL string) error {
	authorize := deviceAuthorizeResponse{}
	// 主机名取不到不影响登录：空串上报，服务端回退到指纹缩写。
	name := ""
	if deps.hostname != nil {
		if got, err := deps.hostname(); err == nil {
			name = got
		}
	}
	if _, err := doLoginJSON(cmd, deps.http, http.MethodPost, serverURL+"/v1/oauth/device/authorize", map[string]any{
		"device_kind": "agentred",
		// 账号侧的设备指纹与 auth.pair 交给桌面端做 TOFU 的那一个是同一个东西:
		// rpc.DaemonFingerprint(instance uuid)。桌面端手上只有这个形态 —— 它按本地
		// 配对行里的 DaemonFingerprint 向 server 点名中转目标(server 拿它查
		// devices.fingerprint),也按它把 LAN 与账号两个来源合并成设备面板的一行。
		// 登记裸 uuid 会让两边永远对不上:中转恒报「这台 daemon 从未登记过」,面板恒把
		// 已认领的机器标成未认领。
		"fingerprint": rpc.DaemonFingerprint(st.InstanceUUID()),
		"platform":    deps.platform,
		"version":     deps.version,
		// 设备流是账号侧拿到这台机器名字的唯一途径：不带上它，设备列表里每台机器
		// 都只能叫指纹缩写。
		"name": name,
	}, &authorize); err != nil {
		return fmt.Errorf("authorize device login: %w", err)
	}
	if authorize.DeviceCode == "" || authorize.UserCode == "" || authorize.VerificationURIComplete == "" || authorize.ExpiresIn <= 0 {
		return fmt.Errorf("authorize device login: invalid response")
	}
	verificationURL, err := validHTTPURL(authorize.VerificationURIComplete)
	if err != nil {
		return fmt.Errorf("authorize device login: invalid verification URL")
	}

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "User code: %s\n", authorize.UserCode)
	_, _ = fmt.Fprintf(out, "Open this URL to approve this daemon: %s\n", verificationURL)
	if err := deps.openBrowser(verificationURL); err != nil {
		_, _ = fmt.Fprintf(out, "Could not open a browser automatically; open the URL above manually: %v\n", err)
	}

	interval := time.Duration(authorize.Interval) * time.Second
	if interval <= 0 {
		interval = defaultLoginPollInterval
	}
	deadline := time.Now().Add(time.Duration(authorize.ExpiresIn) * time.Second)
	var token deviceTokenResponse
	for {
		if time.Now().Add(interval).After(deadline) {
			return fmt.Errorf("device authorization expired before approval")
		}
		if err := deps.wait(interval); err != nil {
			return fmt.Errorf("wait to poll device authorization: %w", err)
		}
		oauthErr, err := doLoginJSON(cmd, deps.http, http.MethodPost, serverURL+"/v1/oauth/device/token", map[string]string{
			"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
			"device_code": authorize.DeviceCode,
		}, &token)
		if err != nil {
			return fmt.Errorf("poll device authorization: %w", err)
		}
		if oauthErr == nil {
			break
		}
		switch oauthErr.Code {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += slowDownPollIncrement
			continue
		case "expired_token", "access_denied", "invalid_grant":
			return fmt.Errorf("device authorization %s: %s", oauthErr.Code, oauthErr.Description)
		default:
			return fmt.Errorf("device authorization failed: %s", oauthErr.Code)
		}
	}
	if token.AccessToken == "" || token.RefreshToken == "" || token.ExpiresIn <= 0 || token.RefreshExpiresIn <= 0 {
		return fmt.Errorf("poll device authorization: invalid token response")
	}
	accountID, err := accountIDFromAccessToken(token.AccessToken)
	if err != nil {
		return err
	}

	key := publicKeyResponse{}
	if _, err := doLoginJSON(cmd, deps.http, http.MethodGet, serverURL+"/v1/keys", nil, &key); err != nil {
		return fmt.Errorf("fetch verification public key: %w", err)
	}
	if key.CurrentKID == "" || key.Keys[key.CurrentKID] == "" || key.MaxTokenLifetimeSeconds <= 0 {
		return fmt.Errorf("fetch verification public key: invalid response")
	}
	now := time.Now()
	st.ClaimWithKeySet(accountID, key.CurrentKID, key.Keys, key.MaxTokenLifetimeSeconds, state.AccountCredential{
		DeviceID:              token.DeviceID,
		AccessToken:           token.AccessToken,
		AccessTokenExpiresAt:  now.Add(time.Duration(token.ExpiresIn) * time.Second).Unix(),
		RefreshToken:          token.RefreshToken,
		RefreshTokenExpiresAt: now.Add(time.Duration(token.RefreshExpiresIn) * time.Second).Unix(),
	})
	st.Mutate(func(current *state.State) {
		current.HubServerURL = serverURL
	})
	if err := st.Save(); err != nil {
		return fmt.Errorf("save account claim: %w", err)
	}
	_, _ = fmt.Fprintln(out, "Logged in and claimed this daemon.")
	return nil
}

// doLoginJSON handles both the raw endpoint payload described by the public
// contract and cago's {data: ...} response envelope.
func doLoginJSON(cmd *cobra.Command, client loginHTTPDoer, method, endpoint string, requestBody any, responseBody any) (*oauthErrorResponse, error) {
	var body io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(cmd.Context(), method, endpoint, body)
	if err != nil {
		return nil, err
	}
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		oauthErr := oauthErrorResponse{}
		if err := decodeLoginResponse(payload, &oauthErr); err == nil && oauthErr.Code != "" {
			return &oauthErr, nil
		}
		return nil, fmt.Errorf("server returned %s: %s", resp.Status, strings.TrimSpace(string(payload)))
	}
	if err := decodeLoginResponse(payload, responseBody); err != nil {
		return nil, err
	}
	return nil, nil
}

func decodeLoginResponse(payload []byte, target any) error {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return err
	}
	if len(envelope.Data) != 0 && string(envelope.Data) != "null" {
		return json.Unmarshal(envelope.Data, target)
	}
	return json.Unmarshal(payload, target)
}

func accountIDFromAccessToken(accessToken string) (string, error) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("device token does not contain an account identifier")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode device token account identifier: %w", err)
	}
	var claims struct {
		UID json.RawMessage `json:"uid"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || len(claims.UID) == 0 {
		return "", fmt.Errorf("device token does not contain an account identifier")
	}
	var accountID string
	if err := json.Unmarshal(claims.UID, &accountID); err != nil {
		var accountNumber json.Number
		decoder := json.NewDecoder(bytes.NewReader(claims.UID))
		decoder.UseNumber()
		if err := decoder.Decode(&accountNumber); err != nil {
			return "", fmt.Errorf("device token does not contain an account identifier")
		}
		accountID = accountNumber.String()
	}
	if accountID == "" || accountID == "0" {
		return "", fmt.Errorf("device token does not contain an account identifier")
	}
	return accountID, nil
}

func validServerURL(raw string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return "", newUsageError("--server is required (or set AGENTRED_SERVER_URL)")
	}
	if _, err := validHTTPURL(trimmed); err != nil {
		return "", newUsageError("--server must be an http(s) base URL")
	}
	return trimmed, nil
}

func validHTTPURL(raw string) (string, error) {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("invalid http(s) URL")
	}
	return raw, nil
}

func openBrowser(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", url) //nolint:gosec // validHTTPURL permits only http(s) URLs.
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url) //nolint:gosec // validHTTPURL permits only http(s) URLs.
	default:
		command = exec.Command("xdg-open", url) //nolint:gosec // validHTTPURL permits only http(s) URLs.
	}
	return command.Start()
}

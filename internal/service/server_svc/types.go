package server_svc

import "errors"

// StartLoginResult 是 StartLogin 返回给前端、用来跳出浏览器的 device-flow 元数据。
// 字段对应 hub /v1/oauth/device/authorize 的响应。
type StartLoginResult struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationURI         string `json:"verificationURI"`
	VerificationURIComplete string `json:"verificationURIComplete"`
	Interval                int    `json:"interval"`
	ExpiresIn               int    `json:"expiresIn"`
}

// Device 与 hub /v1/devices 的 ListDevicesItem 对应（仅保留桌面端需要的字段）。
type Device struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Platform    string `json:"platform"`
	Version     string `json:"version"`
	Fingerprint string `json:"fingerprint"`
	LastSeenAt  int64  `json:"lastSeenAt"`
	Status      int    `json:"status"`
	// Online 是 daemon 在 server 上的中继在线登记(R20),即中转路径此刻是否可达。
	// 与 Status(账号侧授权标志)不是一回事:设备面板按 R15 呈现「可达路径」用的是它。
	Online       bool `json:"online"`
	IsThisDevice bool `json:"isThisDevice"`
}

// server_svc 内部用的语义错误。Wails 绑定层会按错误把它们映射到 i18n.NewError。
var (
	ErrAlreadyInProgress = errors.New("server: login already in progress")
	ErrNotLoggedIn       = errors.New("server: not logged in")
	ErrServerUnreachable = errors.New("server: unreachable")
	ErrAccessDenied      = errors.New("server: access denied")
	ErrLoginExpired      = errors.New("server: device code expired")
	ErrRefreshFailed     = errors.New("server: refresh failed")
	// ErrRefreshRejected 表示服务端**明确**拒绝了本机存着的 refresh_token
	// （轮换过 / 被吊销 / 设备已删）。只有它才代表「凭据真的没了」，也只有它
	// 才允许清掉本地登录；服务端够不着、5xx、反代 404 一律不是。
	ErrRefreshRejected = errors.New("server: refresh token rejected")
	// ErrDesktopAppNotRunning identifies an addressable desktop whose Agentre
	// App process is not currently registered with the relay. It is deliberately
	// distinct from client.ErrRelayDaemonOffline, which remains agentred-only.
	ErrDesktopAppNotRunning = errors.New("relay: Agentre App is not running on the target desktop")
)

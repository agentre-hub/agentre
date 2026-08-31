// Package remote_device_svc 编排桌面端 LAN 配对 / 探活 / TLS 信任管理。
package remote_device_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/daemon/client"
)

//go:generate mockgen -source ports.go -destination mock_remote_device_svc/mock_ports.go

// DaemonDialPort 抽象一次 RPC over WS 握手。真实现 wrap internal/daemon/client。
//
//   - Pair / Connect 走「短连接」语义：内部 Dial + 调一个 RPC + 关闭，
//     仅返回握手结果，连接不会留给调用方。
//   - Open 走「长连接」语义：内部 Dial + auth.connect 鉴权 + 把连接保留给
//     调用方，由调用方负责 defer Close。给 DialOnce 这类短 RPC 场景用。
//   - OpenAccount 与 Open 同为长连接语义，但出示的是账号凭据（auth.account）：
//     本机对这台 daemon 没有配对时走它。
type DaemonDialPort interface {
	Pair(ctx context.Context, args PairArgs) (PairResult, error)
	Connect(ctx context.Context, args ConnectArgs) (ConnectResult, error)
	Open(ctx context.Context, args ConnectArgs) (client.ProtobufConnection, error)
	OpenAccount(ctx context.Context, args AccountArgs) (client.ProtobufConnection, error)
}

// PairArgs 是 auth.pair 的入参。
type PairArgs struct {
	URL               string
	TLSMode           string
	TLSCertPEM        string
	Code              string
	DeviceName        string
	DeviceFingerprint string
}

// PairResult 是 auth.pair 的返回。
type PairResult struct {
	DeviceToken       string
	DaemonFingerprint string
	InstanceUUID      string
}

// ConnectArgs 是 auth.connect 的入参。
type ConnectArgs struct {
	URL                       string
	TLSMode                   string
	TLSCertPEM                string
	DeviceFingerprint         string
	DeviceToken               string
	ExpectedDaemonFingerprint string
}

// AccountArgs 是直连 auth.account 的入参。Credential 是账号签发的访问凭据，
// daemon 用缓存的公钥本地验签（R3，零网络往返）并从中取出本端身份（决策 8，因此
// 这里不再有 DeviceFingerprint 可报）；ExpectedDaemonFingerprint 必填，与
// auth.connect 一样把连接钉死在本地登记的那台 daemon 上。
type AccountArgs struct {
	URL                       string
	TLSMode                   string
	TLSCertPEM                string
	Credential                string
	ExpectedDaemonFingerprint string
}

// ConnectResult 是 auth.connect 的返回；ActualFingerprint 在 -32001 时由服务端 error.data 提供，
// 正常成功时填 expected。
type ConnectResult struct {
	InstanceUUID      string
	ActualFingerprint string
}

// RelayDialPort 提供账号中转（relay）路径的拨号（R6）。真实现由 server_svc 提供
// （bootstrap 注入），消费方只有 ConnPool。
type RelayDialPort interface {
	// Open 经账号中转连接指定指纹的 daemon，并在该通道上完成 auth.account 握手，
	// 呈现 peerFingerprint——与 LAN 路径 auth.connect 呈现的是同一值（R5）。
	Open(ctx context.Context, daemonFingerprint, peerFingerprint string) (client.ProtobufConnection, error)
}

// AccountCredentialPort 提供当前账号凭据（access token）。ConnPool 在本机对目标
// daemon 没有配对时用它走直连的 auth.account —— 这正是「server 不可用」时仍能连上
// 同账号 daemon 的那条路径（R3）。真实现由 server_svc 提供（bootstrap 注入）。
type AccountCredentialPort interface {
	// AccessToken 返回当前账号访问凭据；未登录时为空串。
	AccessToken() string
}

// KeychainPort 抽象 OS keychain（internal/pkg/keychain 接口的窄子集）。
type KeychainPort interface {
	Get(account string) (string, error)
	Set(account, secret string) error
	Delete(account string) error
}

// WatcherPort 是 remote_device_svc 反向消费 watcher_svc 的窄接口。SetWatcher 注入;
// 单测可以注入 mock 验证 Start/Stop/Restart 被调到。
type WatcherPort interface {
	Start(ctx context.Context, deviceID int64) error
	Stop(deviceID int64)
	Restart(ctx context.Context, deviceID int64) error
}

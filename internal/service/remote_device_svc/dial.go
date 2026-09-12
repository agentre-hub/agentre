// internal/service/remote_device_svc/dial.go
package remote_device_svc

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/daemon/identity"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// realDial wraps internal/daemon/client to satisfy DaemonDialPort.
type realDial struct{}

// NewDaemonDial constructs the production DaemonDialPort.
func NewDaemonDial() DaemonDialPort { return &realDial{} }

func (realDial) Pair(ctx context.Context, args PairArgs) (PairResult, error) {
	tlsCfg, err := client.BuildTLSConfig(client.TLSMode(args.TLSMode), args.TLSCertPEM)
	if err != nil {
		return PairResult{}, fmt.Errorf("%w: %v", ErrTLSConfig, err)
	}
	c, err := client.DialProtobuf(ctx, client.Options{URL: args.URL, TLSConfig: tlsCfg})
	if err != nil {
		return PairResult{}, translateProtocolError(err)
	}
	defer func() { _ = c.Close() }()
	res, err := c.AuthPair(ctx, &agentrewire.AuthPairRequest{Code: args.Code, DeviceName: args.DeviceName, DeviceFingerprint: args.DeviceFingerprint})
	if err != nil {
		return PairResult{}, translatePairRPCError(err)
	}
	return PairResult{
		DeviceToken: res.GetDeviceToken(), DaemonFingerprint: devicefp.Carrier(res.GetDaemonFingerprint()), InstanceUUID: res.GetInstanceUuid(),
	}, nil
}

func (realDial) Connect(ctx context.Context, args ConnectArgs) (ConnectResult, error) {
	tlsCfg, err := client.BuildTLSConfig(client.TLSMode(args.TLSMode), args.TLSCertPEM)
	if err != nil {
		return ConnectResult{}, fmt.Errorf("%w: %v", ErrTLSConfig, err)
	}
	c, err := client.DialProtobuf(ctx, client.Options{URL: args.URL, TLSConfig: tlsCfg})
	if err != nil {
		return ConnectResult{}, translateProtocolError(err)
	}
	defer func() { _ = c.Close() }()
	res, err := c.AuthConnect(ctx, &agentrewire.AuthConnectRequest{DeviceFingerprint: args.DeviceFingerprint, DeviceToken: args.DeviceToken, ExpectedDaemonFingerprint: string(args.ExpectedDaemonFingerprint)})
	if err != nil {
		return ConnectResult{}, translateConnectRPCError(err)
	}
	actual := identity.DaemonFingerprint(res.GetInstanceUuid())
	if err := verifyDaemonIdentity(actual, args.ExpectedDaemonFingerprint); err != nil {
		return ConnectResult{}, err
	}
	return ConnectResult{InstanceUUID: res.GetInstanceUuid(), ActualFingerprint: actual}, nil
}

// Open 与 Connect 同样跑 TLS 握手 + auth.connect 鉴权，但**不**关闭连接，
// 把 *client.Client 直接交给调用方。调用方必须 defer c.Close()。
// 给 DialOnce 这类「短 RPC 但需要保持已鉴权会话」的场景用。
func (realDial) Open(ctx context.Context, args ConnectArgs) (client.ProtobufConnection, error) {
	tlsCfg, err := client.BuildTLSConfig(client.TLSMode(args.TLSMode), args.TLSCertPEM)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTLSConfig, err)
	}
	c, err := client.DialProtobuf(ctx, client.Options{URL: args.URL, TLSConfig: tlsCfg})
	if err != nil {
		return nil, translateProtocolError(err)
	}
	res, err := c.AuthConnect(ctx, &agentrewire.AuthConnectRequest{DeviceFingerprint: args.DeviceFingerprint, DeviceToken: args.DeviceToken, ExpectedDaemonFingerprint: string(args.ExpectedDaemonFingerprint)})
	if err != nil {
		_ = c.Close()
		return nil, translateConnectRPCError(err)
	}
	if err := verifyDaemonIdentity(identity.DaemonFingerprint(res.GetInstanceUuid()), args.ExpectedDaemonFingerprint); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// OpenAccount 与 Open 一样是长连接语义，但出示账号凭据走 auth.account：本机对
// 这台 daemon 没有配对时的直连握手。daemon 只用缓存的公钥与吊销列表本地判定，
// 整个握手是一次 RPC、零网络往返（R3）——server 不可达也照常接受。
// 握手成功后按返回的 instanceUUID 复核 TOFU 指纹，避免把「另一台 daemon」当成
// 本地登记的那台缓存进 ConnPool。
func (realDial) OpenAccount(ctx context.Context, args AccountArgs) (client.ProtobufConnection, error) {
	tlsCfg, err := client.BuildTLSConfig(client.TLSMode(args.TLSMode), args.TLSCertPEM)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTLSConfig, err)
	}
	c, err := client.DialProtobuf(ctx, client.Options{URL: args.URL, TLSConfig: tlsCfg})
	if err != nil {
		return nil, translateProtocolError(err)
	}
	res, err := c.AuthAccount(ctx, &agentrewire.AuthAccountRequest{Credential: args.Credential})
	if err != nil {
		_ = c.Close()
		return nil, translateAccountRPCError(err)
	}
	if err := verifyDaemonIdentity(identity.DaemonFingerprint(res.GetInstanceUuid()), args.ExpectedDaemonFingerprint); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// OpenDirect 见 DaemonDialPort 上的注释。地址之间并发竞速（client.RaceProtobuf），
// 一个地址拨不通、证书不符或不应答，都不耽误其它地址。
func (realDial) OpenDirect(ctx context.Context, args DirectArgs) (client.ProtobufConnection, string, error) {
	if len(args.URLs) == 0 {
		return nil, "", errors.New("no direct address to dial")
	}
	tlsCfg, err := client.BuildTLSConfig(client.TLSPinCert, args.CertPEM)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrTLSConfig, err)
	}
	// conns[i] 由第 i 条路径的拨号 goroutine 写入；RaceProtobuf 收齐全部结果才返回，
	// 所以返回之后读它是安全的。赢家是哪个地址只能这样对回去。
	conns := make([]client.ProtobufConnection, len(args.URLs))
	paths := make([]client.ProtobufPath, len(args.URLs))
	for i, address := range args.URLs {
		paths[i] = client.ProtobufPath{
			Name: address,
			Dial: func(ctx context.Context) (client.ProtobufConnection, error) {
				c, err := openDirectAt(ctx, address, tlsCfg.Clone(), args)
				conns[i] = c
				return c, err
			},
		}
	}
	winner, err := client.RaceProtobuf(ctx, paths...)
	if err != nil {
		return nil, "", err
	}
	for i, c := range conns {
		if c != nil && c == winner {
			return winner, args.URLs[i], nil
		}
	}
	return winner, "", nil
}

// openDirectAt 对单个地址做「固定证书的 TLS → auth.direct → TOFU 复核」。
//
// 凭据只在 TLS 握手成功之后才发出：证书与固定值不一致时 BuildTLSConfig 的校验在握手里
// 就失败，DialProtobuf 连 WebSocket 升级都不会发。这是一次普通的连接失败，**不是**
// ErrTOFUMismatch——IP 变了、证书换了都长这样（D16），不该拉响 TOFU 告警。
func openDirectAt(ctx context.Context, address string, tlsCfg *tls.Config, args DirectArgs) (client.ProtobufConnection, error) {
	u, err := url.Parse(address)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "wss" {
		// 没有 TLS 就没有证书可以对固定值，凭据绝不能明文发出去。
		return nil, errors.New("direct address is not wss, refusing to present the credential without a pinned TLS peer")
	}
	c, err := client.DialProtobuf(ctx, client.Options{URL: address, TLSConfig: tlsCfg})
	if err != nil {
		return nil, translateProtocolError(err)
	}
	res, err := c.AuthDirect(ctx, &agentrewire.AuthDirectRequest{Credential: args.Credential})
	if err != nil {
		_ = c.Close()
		return nil, translateAccountRPCError(err)
	}
	if err := verifyDaemonIdentity(identity.DaemonFingerprint(res.GetInstanceUuid()), args.ExpectedDaemonFingerprint); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// verifyDaemonIdentity 核对「刚握手的这台 daemon 是不是本地登记的那台」。
//
// actual 必须是桌面端自己按对端应答里的 instanceUuid 重算出来的值（identity.
// DaemonFingerprint），绝不能回填调用方的 expected：auth.connect 把 expected 发给对端、
// 由对端自己比对，那只是一次礼貌的自我申报——局域网里冒充 daemon 的一方当然会说「是我」。
// 所以这道比对必须在本端做一遍，两端各做一次才成立。
//
// expected 为空时同样不通过：没有可锚定的身份就不该把连接当成「那台机器」的连接
// （它会被 ConnPool 按 deviceID 缓存下来）。
func verifyDaemonIdentity(actual, expected devicefp.Carrier) error {
	if expected == "" || actual != expected {
		return ErrTOFUMismatch
	}
	return nil
}

// translateProtocolError 把 client 层的协议哨兵折成 svc 自己的一套。
//
// 单独一层是因为它与凭据无关:握手在协议这一层就没谈成,后面的 -32001 / -32004
// 根本不会发生;而设备面板对这两类的处置也不同(升级远端 agentred vs 重新配对)。
func translateProtocolError(err error) error {
	switch {
	case errors.Is(err, client.ErrPeerProtocolUnsupported):
		return ErrProtocolUnsupported
	case errors.Is(err, client.ErrPeerProtocolVersionMismatch):
		return ErrProtocolVersionMismatch
	}
	return err
}

// translatePairRPCError maps daemon typed RPC error codes to the svc-internal
// sentinels consumed by Add's translatePairError. Unmapped errors pass through
// and are caught by the default branch (RemoteDeviceDialFailed).
func translatePairRPCError(err error) error {
	if protocolErr := translateProtocolError(err); protocolErr != err {
		return protocolErr
	}
	var protobufErr *protorpc.Error
	if errors.As(err, &protobufErr) {
		switch protobufErr.Code {
		case -32004:
			return ErrPairingInvalid
		case -32001:
			return ErrUnauthorized
		}
	}
	var rpcErr *rpcerror.Error
	if errors.As(err, &rpcErr) {
		switch rpcErr.Code {
		case -32004: // rpcerror.ErrPairing
			return ErrPairingInvalid
		case -32001: // rpcerror.ErrUnauthorized
			return ErrUnauthorized
		}
	}
	return err
}

// translateConnectRPCError additionally distinguishes TOFU mismatch from
// generic Unauthorized by checking if the daemon's HandleConnect set a
// fingerprint-related message.
func translateConnectRPCError(err error) error {
	if protocolErr := translateProtocolError(err); protocolErr != err {
		return protocolErr
	}
	var protobufErr *protorpc.Error
	if errors.As(err, &protobufErr) && protobufErr.Code == -32001 {
		if strings.Contains(strings.ToLower(protobufErr.Message), "fingerprint") {
			return ErrTOFUMismatch
		}
		return ErrUnauthorized
	}
	var rpcErr *rpcerror.Error
	if errors.As(err, &rpcErr) {
		switch rpcErr.Code {
		case -32001:
			if isFingerprintMismatch(rpcErr) {
				return ErrTOFUMismatch
			}
			return ErrUnauthorized
		}
	}
	return err
}

// translateAccountRPCError maps the daemon's auth.account / auth.direct
// rejection to ErrUnauthorized so ConnPool keeps classifying it as terminal.
// HandleAccount has six distinguishable reasons (expired / bad signature /
// account mismatch / revoked / missing key / malformed) and returns them all
// under -32001 — the desktop classifies by code, never by message.
//
// -32007 (account server unreachable) deliberately passes through untouched:
// the credential was never rejected, so it must neither trigger a credential
// refresh nor read as "retrying is pointless" (spec H3).
func translateAccountRPCError(err error) error {
	if protocolErr := translateProtocolError(err); protocolErr != err {
		return protocolErr
	}
	var protobufErr *protorpc.Error
	if errors.As(err, &protobufErr) && protobufErr.Code == -32001 {
		return ErrUnauthorized
	}
	var rpcErr *rpcerror.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == -32001 {
		return ErrUnauthorized
	}
	return err
}

// isFingerprintMismatch inspects the typed RPC error to decide whether the
// daemon's -32001 was a TOFU mismatch (vs a stale token). The daemon's
// HandleConnect emits message "daemon fingerprint mismatch (TOFU)" — we
// detect that case-insensitively. Some implementations may instead populate
// error.data.actualFingerprint; we cover both.
func isFingerprintMismatch(e *rpcerror.Error) bool {
	if strings.Contains(strings.ToLower(e.Message), "fingerprint") {
		return true
	}
	if len(e.Details) > 0 {
		var m map[string]any
		if json.Unmarshal(e.Details, &m) == nil {
			if _, has := m["actualFingerprint"]; has {
				return true
			}
		}
	}
	return false
}

// internal/service/remote_device_svc/dial.go
package remote_device_svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/daemon/identity"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
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
		DeviceToken: res.GetDeviceToken(), DaemonFingerprint: res.GetDaemonFingerprint(), InstanceUUID: res.GetInstanceUuid(),
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
	res, err := c.AuthConnect(ctx, &agentrewire.AuthConnectRequest{DeviceFingerprint: args.DeviceFingerprint, DeviceToken: args.DeviceToken, ExpectedDaemonFingerprint: args.ExpectedDaemonFingerprint})
	if err != nil {
		return ConnectResult{}, translateConnectRPCError(err)
	}
	return ConnectResult{InstanceUUID: res.GetInstanceUuid(), ActualFingerprint: args.ExpectedDaemonFingerprint}, nil
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
	_, err = c.AuthConnect(ctx, &agentrewire.AuthConnectRequest{DeviceFingerprint: args.DeviceFingerprint, DeviceToken: args.DeviceToken, ExpectedDaemonFingerprint: args.ExpectedDaemonFingerprint})
	if err != nil {
		_ = c.Close()
		return nil, translateConnectRPCError(err)
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
	if identity.DaemonFingerprint(res.GetInstanceUuid()) != args.ExpectedDaemonFingerprint {
		_ = c.Close()
		return nil, ErrTOFUMismatch
	}
	return c, nil
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

// translateAccountRPCError maps the daemon's auth.account rejection to
// ErrUnauthorized so ConnPool keeps classifying it as terminal. HandleAccount
// has six distinguishable reasons (expired / bad signature / account mismatch /
// revoked / missing key / malformed) and returns them all under -32001 — the
// desktop classifies by code, never by message.
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

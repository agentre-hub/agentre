package remote_device_svc

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// ErrTOFUMismatch is returned by DaemonDialPort.Connect when the server's
// daemonFingerprint no longer matches the one pinned at pair time.
var ErrTOFUMismatch = errors.New("tofu mismatch")

func (s *service) Refresh(ctx context.Context, id int64) (*DeviceView, error) {
	row, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, i18n.NewError(ctx, code.RemoteDeviceNotFound)
	}

	token, err := s.keychain.Get(keychainAccountForToken(id))
	if err != nil || token == "" {
		_ = s.repo.UpdateLastSeen(ctx, id, row.LastSeenAt, "unauthorized")
		row.LastError = "unauthorized"
		return s.toView(row), nil //nolint:nilerr // keychain miss is surfaced via row.LastError, not as an RPC error
	}
	fp, err := s.keychain.Get(accountForDeviceFingerprint)
	if err != nil || fp == "" {
		_ = s.repo.UpdateLastSeen(ctx, id, row.LastSeenAt, "unauthorized")
		row.LastError = "unauthorized"
		return s.toView(row), nil //nolint:nilerr // keychain miss is surfaced via row.LastError, not as an RPC error
	}

	_, err = s.dial.Connect(ctx, ConnectArgs{
		URL: row.URL, TLSMode: row.TLSMode, TLSCertPEM: row.TLSCertPEM,
		DeviceFingerprint: fp, DeviceToken: token,
		ExpectedDaemonFingerprint: row.DaemonFingerprint,
	})
	switch {
	case err == nil:
		ts := nowMs()
		_ = s.repo.UpdateLastSeen(ctx, id, ts, "")
		row.LastSeenAt = ts
		row.LastError = ""
		return s.toView(row), nil
	case errors.Is(err, ErrTOFUMismatch):
		_ = s.repo.UpdateLastSeen(ctx, id, row.LastSeenAt, "tofu_mismatch")
		row.LastError = "tofu_mismatch"
		return s.toView(row), nil
	case errors.Is(err, ErrUnauthorized):
		_ = s.repo.UpdateLastSeen(ctx, id, row.LastSeenAt, "unauthorized")
		row.LastError = "unauthorized"
		return s.toView(row), nil
	// 协议层的不一致必须与网络失败分开落库：设备面板只读 LastError，
	// 混进 "dial_failed:" 会把「升级远端 agentred」说成「检查网络与端口」。
	case errors.Is(err, ErrProtocolUnsupported):
		_ = s.repo.UpdateLastSeen(ctx, id, row.LastSeenAt, "protocol_unsupported")
		row.LastError = "protocol_unsupported"
		return s.toView(row), nil
	case errors.Is(err, ErrProtocolVersionMismatch):
		_ = s.repo.UpdateLastSeen(ctx, id, row.LastSeenAt, "protocol_mismatch")
		row.LastError = "protocol_mismatch"
		return s.toView(row), nil
	default:
		msg := "dial_failed:" + err.Error()
		_ = s.repo.UpdateLastSeen(ctx, id, row.LastSeenAt, msg)
		row.LastError = msg
		return s.toView(row), nil
	}
}

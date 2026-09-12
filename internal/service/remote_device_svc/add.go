package remote_device_svc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/daemon/identity"
	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/deviceidentity"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// pairingCodeLen 与 spec §4.3 一致 — base32 6 字符。
const pairingCodeLen = 6

func nowMs() int64 { return time.Now().UnixMilli() }

func (s *service) Add(ctx context.Context, req AddRequest) (*DeviceView, error) {
	if err := validateAddRequest(ctx, req); err != nil {
		return nil, err
	}
	existing, err := s.repo.FindByURL(ctx, req.URL)
	if err != nil {
		return nil, err
	}
	// 按地址查到的是账号直连行时先不判「已配对」：用户多半正照着 agentred 印出的地址给同一台
	// 机器手动配对（D14）。握完手按指纹决定：同一台就转成手动配对行，否则地址属于别的机器。
	urlHeldByAccountDirect := existing.IsAccountDirect()
	if existing != nil && !urlHeldByAccountDirect {
		return nil, i18n.NewError(ctx, code.RemoteDeviceAlreadyPaired)
	}
	name, err := s.deriveDisplayName(ctx, req)
	if err != nil {
		return nil, err
	}
	fp, err := s.ensureDeviceFingerprint()
	if err != nil {
		return nil, err
	}
	result, err := s.dial.Pair(ctx, PairArgs{
		URL: req.URL, TLSMode: req.TLSMode, TLSCertPEM: req.TLSCertPEM,
		Code: req.PairingCode, DeviceName: name, DeviceFingerprint: string(fp),
	})
	if err != nil {
		return nil, translatePairError(ctx, err)
	}
	// 对端自报的 daemonFingerprint 必须自己算得出来。它不是一个普通字段:它进指纹唯一
	// 索引、决定这次配对落在哪一行,并成为此后每一次连接 TOFU 复核的锚——而复核用的是
	// derive(instanceUuid)。采信一个算不出来的值,等于让对端自己定义「我是谁」,而且此后
	// 每次连接都会因为锚与实测值不一致而报成「指纹变了」。
	if result.DaemonFingerprint != identity.DaemonFingerprint(result.InstanceUUID) {
		logger.Ctx(ctx).Warn("remote_device_svc.Add: peer reported a fingerprint it cannot derive from its own instance uuid",
			zap.String("url", req.URL))
		return nil, i18n.NewError(ctx, code.RemoteDeviceTOFUMismatch)
	}
	// 指纹只有握完手才知道，所以这一步必须在 Pair 之后：本机可能已经有这台机器的
	// 一行「只有中转路径」的收编记录（AdoptAccountDevices）。那是同一台机器，再建
	// 一行会让它在面板上变成两台、在连接池里变成两个 entry，指纹唯一索引也会拒掉。
	// 正确的收场是把那一行升级成双路径。
	if existing, ferr := s.repo.FindByFingerprint(ctx, result.DaemonFingerprint); ferr != nil {
		return nil, ferr
	} else if existing != nil {
		// D14：一台「来自账号的直连」的机器（有地址/证书/账号凭据，但没被本机手动
		// LAN 配对过）同样要走升级路径，而不是「已配对」拒绝——手动配对赢，把它
		// 转成手动配对行，此后不再随账号登出被清（ClearAccountDirect 只认
		// IsAccountDirect）。真正已经手动配对过的行（两者都不是）才拒绝。
		if !existing.IsRelayOnly() && !existing.IsAccountDirect() {
			return nil, i18n.NewError(ctx, code.RemoteDeviceAlreadyPaired)
		}
		if existing.IsAccountDirect() {
			if err := s.repo.ClearAccountDirect(ctx, existing.ID); err != nil {
				return nil, err
			}
		}
		return s.upgradeRelayOnly(ctx, existing, req, result)
	}
	if urlHeldByAccountDirect {
		// 占着这个地址的账号直连行属于另一台机器（两个局域网里同一个私有地址）：新建一行只会
		// 撞地址唯一索引。
		return nil, i18n.NewError(ctx, code.RemoteDeviceAlreadyPaired)
	}
	row := &paired_agentred_entity.PairedAgentred{
		Name: name, URL: req.URL,
		DaemonFingerprint: result.DaemonFingerprint, InstanceUUID: result.InstanceUUID,
		TLSMode: req.TLSMode, TLSCertPEM: req.TLSCertPEM,
		PairedAt: nowMs(), Status: 1, // consts.ACTIVE
	}
	if err := row.Check(ctx); err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, row); err != nil {
		return nil, err
	}
	if err := s.keychain.Set(keychainAccountForToken(row.ID), result.DeviceToken); err != nil {
		if delErr := s.repo.Delete(ctx, row.ID); delErr != nil {
			logger.Ctx(ctx).Warn("rollback after keychain.Set failed", zap.Error(delErr))
		}
		return nil, i18n.NewError(ctx, code.RemoteDeviceKeychainFailed)
	}
	if s.watcher != nil {
		_ = s.watcher.Start(ctx, row.ID)
	}
	return s.toView(row), nil
}

func validateAddRequest(ctx context.Context, req AddRequest) error {
	if strings.TrimSpace(req.URL) == "" {
		return i18n.NewError(ctx, code.RemoteDeviceURLInvalid)
	}
	if !strings.HasPrefix(req.URL, "ws://") && !strings.HasPrefix(req.URL, "wss://") {
		return i18n.NewError(ctx, code.RemoteDeviceURLInvalid)
	}
	if !strings.HasSuffix(req.URL, "/rpc") {
		return i18n.NewError(ctx, code.RemoteDeviceURLInvalid)
	}
	if len(strings.TrimSpace(req.PairingCode)) != pairingCodeLen {
		return i18n.NewError(ctx, code.RemoteDevicePairingInvalid)
	}
	return nil
}

func (s *service) deriveDisplayName(ctx context.Context, req AddRequest) (string, error) {
	if n := strings.TrimSpace(req.DisplayName); n != "" {
		return n, nil
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		return "", i18n.NewError(ctx, code.RemoteDeviceURLInvalid)
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil {
		rows, err := s.repo.List(ctx)
		if err != nil {
			return "", err
		}
		n := 1
		for _, r := range rows {
			if strings.HasPrefix(r.Name, "agentred-") {
				n++
			}
		}
		return fmt.Sprintf("agentred-%d", n), nil
	}
	first := strings.SplitN(host, ".", 2)[0]
	if first == "" {
		first = host
	}
	return first, nil
}

// ensureDeviceFingerprint 交给 deviceidentity.Ensure —— 本机指纹唯一的生成处。
//
// 只能有这一处:两份实现「一样」要靠注释维持,任何一次单边修改(哪怕只是错误信息不同)
// 都会让 LAN 配对与账号登录拿到不同的指纹,而症状是同一台机器在 server 上变成两台设备。
func (s *service) ensureDeviceFingerprint() (devicefp.Carrier, error) {
	return deviceidentity.Ensure(s.keychain)
}

// translatePairError maps DaemonDialPort errors back to local i18n codes. The
// real DialPort implementation in dial.go wraps typed RPC error codes into
// sentinel errors below; the unit tests just pass plain errors and get the
// generic Unauthorized code.
var (
	ErrPairingInvalid = errors.New("pairing invalid")
	ErrUnauthorized   = errors.New("unauthorized")
	ErrTLSConfig      = errors.New("tls config invalid")

	// ErrProtocolUnsupported: 那台机器上的 agentred 根本不说 agentre 的
	// Protobuf 子协议(WebSocket 升级被 426 挡回)。
	ErrProtocolUnsupported = errors.New("remote agentred speaks no agentre protobuf protocol")
	// ErrProtocolVersionMismatch: 它说这套协议,但版本与本桌面对不上——包括
	// 「压根没报版本」的更老 agentred。
	ErrProtocolVersionMismatch = errors.New("remote agentred speaks another agentre protocol version")
)

func translatePairError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, ErrProtocolUnsupported):
		return i18n.NewError(ctx, code.RemoteDeviceProtocolUnsupported)
	case errors.Is(err, ErrProtocolVersionMismatch):
		return i18n.NewError(ctx, code.RemoteDeviceProtocolVersionMismatch)
	case errors.Is(err, ErrPairingInvalid):
		return i18n.NewError(ctx, code.RemoteDevicePairingInvalid)
	case errors.Is(err, ErrUnauthorized):
		return i18n.NewError(ctx, code.RemoteDeviceUnauthorized)
	case errors.Is(err, ErrTLSConfig):
		return i18n.NewError(ctx, code.RemoteDeviceTLSConfigInvalid)
	default:
		// i18n.NewError 不保留 cause；把原始错误打到日志方便排查 LAN 网络 / TLS 握手问题。
		logger.Ctx(ctx).Warn("remote device dial failed", zap.Error(err))
		return i18n.NewError(ctx, code.RemoteDeviceDialFailed)
	}
}

// upgradeRelayOnly 把一行「只有中转路径」的收编记录升级成双路径：补上刚握手拿到的
// LAN 地址与 TLS 配置，设备令牌落到它自己的 keychain 账号下。
//
// 刻意不新建行：同一台机器（同一 daemon 指纹）在本机只能有一行，否则设备面板会把它
// 显示成两台、连接池会为它开两个 entry、两份在线状态各说各话。
//
// 端点变了，长连状态机必须按新端点重来，所以是 Restart 而不是 Start。
func (s *service) upgradeRelayOnly(
	ctx context.Context, row *paired_agentred_entity.PairedAgentred, req AddRequest, result PairResult,
) (*DeviceView, error) {
	if err := s.repo.UpdateEndpoint(ctx, row.ID, req.URL, result.DaemonFingerprint); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateTLS(ctx, row.ID, req.TLSMode, req.TLSCertPEM); err != nil {
		return nil, err
	}
	if err := s.keychain.Set(keychainAccountForToken(row.ID), result.DeviceToken); err != nil {
		// 不回滚成「没有这一行」：升级之前它就在，而且经中转是可用的。地址已经写进去，
		// 少的只是直连令牌——直连拨号会拿账号凭据兜底（见 ConnPool.openAny）。
		logger.Ctx(ctx).Warn("remote_device_svc.upgradeRelayOnly: storing the pairing token failed",
			zap.Int64("deviceID", row.ID), zap.Error(err))
		return nil, i18n.NewError(ctx, code.RemoteDeviceKeychainFailed)
	}
	row.URL = req.URL
	row.DaemonFingerprint = result.DaemonFingerprint
	row.InstanceUUID = result.InstanceUUID
	row.TLSMode = req.TLSMode
	row.TLSCertPEM = req.TLSCertPEM
	if s.watcher != nil {
		_ = s.watcher.Restart(ctx, row.ID)
	}
	return s.toView(row), nil
}

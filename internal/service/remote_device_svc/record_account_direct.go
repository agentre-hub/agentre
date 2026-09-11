package remote_device_svc

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// AccountDirectDelivery 是账号握手下发的自动直连内容（D3/D6）：一台 agentred 的
// 全部可路由地址、固定校验用的证书 PEM，以及本地直连凭据（opaque，不落库，只进
// 系统钥匙串）。
type AccountDirectDelivery struct {
	DaemonFingerprint devicefp.Carrier
	// URLs 是下发的全部可路由地址；顺序由调用方（task 9）决定，首次记录时取第一个
	// 作为地址位的初值（D6：尚未直连成功过时显示列表中的第一个）。
	URLs []string
	// CertPEM 是这台 agentred 的证书，pin-cert 固定校验用。
	CertPEM string
	// Credential 是账号签发或沿用的本地直连凭据，进系统钥匙串，绝不落库。
	Credential string
}

// RecordAccountDirect 见 svc.go 上的接口注释。
func (s *service) RecordAccountDirect(ctx context.Context, d AccountDirectDelivery) error {
	fp := devicefp.Carrier(strings.TrimSpace(string(d.DaemonFingerprint)))
	urls := sanitizeDirectURLs(d.URLs)
	if fp == "" || len(urls) == 0 || strings.TrimSpace(d.CertPEM) == "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}

	// D15：用户手动移除过这台机器（软删行留在 ListDeleted 里），后续下发不得把它
	// 悄悄收回来——与 AdoptAccountDevices 的墓碑判据同一条规则，但这里只判定一个
	// 指纹、不做批量回收：tombstonedFingerprints 那种"清单里没有就回收墓碑"的逻辑
	// 需要完整的账号设备清单才安全，拿它跑单指纹调用会把其它无关指纹的墓碑一并
	// 误判成"不在账号里"而回收掉。
	tombstoned, err := s.isTombstoned(ctx, fp)
	if err != nil {
		return err
	}
	if tombstoned {
		logger.Ctx(ctx).Debug("remote_device_svc.RecordAccountDirect: honoring the user's removal of this machine",
			zap.String("fingerprint", string(fp)))
		return nil
	}

	existing, err := s.repo.FindByFingerprint(ctx, fp)
	if err != nil {
		return err
	}
	// D14 的另一半：这一行已经是本机自己 LAN 配对来的（手动，非 relay-only、非
	// account-direct），账号下发的内容不覆盖它——手动配对永远赢。
	if existing != nil && !existing.IsRelayOnly() && !existing.IsAccountDirect() {
		logger.Ctx(ctx).Debug("remote_device_svc.RecordAccountDirect: a manually paired row wins, skipping delivery",
			zap.String("fingerprint", string(fp)), zap.Int64("deviceID", existing.ID))
		return nil
	}

	address := addressSlot(existing, urls)
	urlsJSON, err := json.Marshal(urls)
	if err != nil {
		return err
	}

	// 中转赢下的每一次借用都会带回同一份下发：端点（地址位、全部地址、证书）没变就不写库、
	// 不重启 watcher——重启会拆掉一条健康的探活连接——凭据也只在确实换了一张时才重写。
	unchanged := existing != nil && existing.IsAccountDirect() && existing.URL == address &&
		existing.TLSCertPEM == d.CertPEM && slices.Equal(existing.DirectURLs(), urls)
	isNew := existing == nil
	var id int64
	switch {
	case unchanged:
		id = existing.ID
		if stored, getErr := s.keychain.Get(keychainAccountForToken(id)); getErr == nil && stored == d.Credential {
			return nil
		}
	case isNew:
		row := &paired_agentred_entity.PairedAgentred{
			Name:              adoptedName(AccountDevice{Fingerprint: fp}, fp),
			URL:               address,
			DaemonFingerprint: fp,
			TLSMode:           "pin-cert",
			TLSCertPEM:        d.CertPEM,
			Origin:            "account",
			PairedAt:          nowMs(),
			Status:            1, // consts.ACTIVE
		}
		row.SetDirectURLs(urls)
		if err := row.Check(ctx); err != nil {
			return err
		}
		if err := s.repo.Create(ctx, row); err != nil {
			return err
		}
		id = row.ID
	default:
		id = existing.ID
		if err := s.repo.UpsertAccountDirect(ctx, id, address, string(urlsJSON), d.CertPEM); err != nil {
			return err
		}
	}

	if err := s.keychain.Set(keychainAccountForToken(id), d.Credential); err != nil {
		logger.Ctx(ctx).Warn("remote_device_svc.RecordAccountDirect: storing the direct credential failed",
			zap.Int64("deviceID", id), zap.Error(err))
		if isNew {
			// 新行没有凭据就是半成品：地址/证书都写好了但拨不通，不如不留。硬删而不是只软删：
			// 软删行是「用户移除过这台机器」的墓碑，留下它这台机器就再也收不回来了。
			if delErr := s.repo.Delete(ctx, id); delErr != nil {
				logger.Ctx(ctx).Warn("rollback after keychain.Set failed", zap.Error(delErr))
			} else if purgeErr := s.repo.Purge(ctx, id); purgeErr != nil {
				logger.Ctx(ctx).Warn("rollback after keychain.Set failed", zap.Error(purgeErr))
			}
		}
		return i18n.NewError(ctx, code.RemoteDeviceKeychainFailed)
	}
	if unchanged {
		return nil
	}

	if s.watcher != nil {
		if isNew {
			_ = s.watcher.Start(ctx, id)
		} else {
			// 端点/证书可能变了（重新收编升级、或 D16 的证书轮换），长连状态机必须
			// 按新内容重来，与 add.go 的 upgradeRelayOnly 同一理由。
			_ = s.watcher.Restart(ctx, id)
		}
	}
	logger.Ctx(ctx).Info("remote_device_svc.RecordAccountDirect: recorded account-direct delivery",
		zap.String("fingerprint", string(fp)), zap.Int64("deviceID", id), zap.Int("addresses", len(urls)))
	return nil
}

// RecordDirectSuccess 见 svc.go 上的接口注释。
func (s *service) RecordDirectSuccess(ctx context.Context, deviceID int64, address string) error {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil
	}
	row, err := s.repo.Get(ctx, deviceID)
	if err != nil {
		return err
	}
	if row == nil || !row.IsAccountDirect() {
		// 手动配对行的地址位就是用户配的那个 URL，不该被"最近成功地址"覆盖；
		// 纯中转收编行压根没有地址可言。
		return nil
	}
	found := false
	for _, u := range row.DirectURLs() {
		if u == address {
			found = true
			break
		}
	}
	if !found {
		// 不是这一行下发过的地址，不采信——防止拿到不相干的地址污染地址位。
		return nil
	}
	if row.URL == address {
		return nil // 已经是当前地址位，省一次空写。
	}
	return s.repo.UpdateDirectAddress(ctx, deviceID, address)
}

// addressSlot 决定一次下发之后的地址位（D6）：一行已有的账号直连行，地址位要么是
// 最近一次直连成功的地址（RecordDirectSuccess 写入），要么是上一次下发的第一个。它仍在
// 这次下发的列表里就原样保留——中转赢下竞速时的再下发不能把最近成功的地址打回第一个；
// 不在了（新行、收编行、地址已变）才取这次列表的第一个。
func addressSlot(existing *paired_agentred_entity.PairedAgentred, urls []string) string {
	if existing != nil && existing.IsAccountDirect() {
		for _, u := range urls {
			if u == existing.URL {
				return u
			}
		}
	}
	return urls[0]
}

// isTombstoned 只判断这一个指纹是否有软删行，不做批量回收（回收只在整轮账号设备
// 清单对账时安全，见 adopt.go 的 tombstonedFingerprints）。
func (s *service) isTombstoned(ctx context.Context, fp devicefp.Carrier) (bool, error) {
	deleted, err := s.repo.ListDeleted(ctx)
	if err != nil {
		return false, err
	}
	for _, row := range deleted {
		if row != nil && row.DaemonFingerprint == fp {
			return true, nil
		}
	}
	return false, nil
}

// sanitizeDirectURLs trims blanks and drops empties/dupes while preserving order
// （首个非空、去重后的地址是 D6 的地址位默认值）。
func sanitizeDirectURLs(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, u := range in {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return out
}

package remote_device_svc

import (
	"context"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/code"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// RemovedDevice 是一台被用户从本机移除过的机器（D15）：只有指纹与移除时的名字，
// 移除记录里不再有地址、证书或凭据。
type RemovedDevice struct {
	Fingerprint devicefp.Carrier `json:"fingerprint"`
	Name        string           `json:"name"`
}

// ListRemoved 按指纹列出本机的移除记录，每台机器一条，取最近那条记录的名字。
//
// 设备面板拿它把「仍在账号里、但用户不要它出现在这台桌面上」的机器从账号独有行里
// 去掉，并给出「已移除」入口。「仍在账号里」由面板对照账号清单判断：本服务不依赖账号层。
// 空指纹的移除记录不列出——它挡不住任何收编（见 tombstonedFingerprints），也无从对照。
func (s *service) ListRemoved(ctx context.Context) ([]RemovedDevice, error) {
	deleted, err := s.repo.ListDeleted(ctx)
	if err != nil {
		return nil, err
	}
	out := []RemovedDevice{}
	seen := make(map[devicefp.Carrier]struct{}, len(deleted))
	for _, row := range deleted {
		if row == nil {
			continue
		}
		fp := devicefp.Carrier(strings.TrimSpace(string(row.DaemonFingerprint)))
		if fp == "" {
			continue
		}
		if _, dup := seen[fp]; dup {
			continue
		}
		seen[fp] = struct{}{}
		out = append(out, RemovedDevice{Fingerprint: fp, Name: row.Name})
	}
	return out, nil
}

// Restore 删掉这台机器的全部移除记录（D15 恢复）。
//
// 它只撤销「不要这台机器」的意图，不自己建行：移除记录没了，下一次账号设备清单
// 刷新里的收编（app 层 ServerListDevices → AdoptAccountDevices）就会把仍在账号里的
// 它收成只走中转的行，下一次成功的账号握手再照 D3/D6 下发直连内容。这台机器
// 没有移除记录时什么也不做。
func (s *service) Restore(ctx context.Context, fingerprint devicefp.Carrier) error {
	fp := devicefp.Carrier(strings.TrimSpace(string(fingerprint)))
	if fp == "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	deleted, err := s.repo.ListDeleted(ctx)
	if err != nil {
		return err
	}
	for _, row := range deleted {
		if row == nil || row.DaemonFingerprint != fp {
			continue
		}
		if err := s.repo.Purge(ctx, row.ID); err != nil {
			return err
		}
		logger.Ctx(ctx).Info("remote_device_svc.Restore: dropped a removal record so the machine can be adopted again",
			zap.String("fingerprint", string(fp)), zap.Int64("deviceID", row.ID))
	}
	return nil
}

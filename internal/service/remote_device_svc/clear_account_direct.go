package remote_device_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// ClearAccountDirect 见 svc.go 上的接口注释（D10 桌面端登出）。
//
// 为什么不是 Delete：这些行描述的机器仍然在账号里（下一次账号握手会再下发/再
// 收编）；DiscardAdoptedDevices 处理的是纯收编行的删除，这里是另一半——曾经直连
// 过的账号行退回同一种"只有中转路径"的形状，而不是先删后重建（重建会让面板闪一下、
// 连接池换一个新 entry）。
func (s *service) ClearAccountDirect(ctx context.Context) (int, error) {
	rows, err := s.repo.List(ctx)
	if err != nil {
		return 0, err
	}
	cleared := 0
	for _, row := range rows {
		if row == nil || !row.IsAccountDirect() {
			continue
		}
		if err := s.repo.ClearAccountDirect(ctx, row.ID); err != nil {
			return cleared, err
		}
		if err := s.keychain.Delete(keychainAccountForToken(row.ID)); err != nil {
			logger.Ctx(ctx).Warn("remote_device_svc.ClearAccountDirect: keychain delete failed; credential left dangling",
				zap.Int64("deviceID", row.ID), zap.Error(err))
		}
		if s.watcher != nil {
			// 端点从"有直连地址"变成"只有中转"，长连状态机必须按新形状重来，
			// 与 add.go 的 upgradeRelayOnly 同一理由（反方向）。
			_ = s.watcher.Restart(ctx, row.ID)
		}
		cleared++
		logger.Ctx(ctx).Info("remote_device_svc.ClearAccountDirect: cleared an account-direct row back to relay-only",
			zap.Int64("deviceID", row.ID))
	}
	return cleared, nil
}

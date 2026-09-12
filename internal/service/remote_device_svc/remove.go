// internal/service/remote_device_svc/remove.go
package remote_device_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// Remove 软删一行并清掉它在钥匙串里的凭据。软删行是「用户不要这台机器」的墓碑，
// 按指纹挡住后续收编与下发（adopt.go 的 tombstonedFingerprints）。
//
// 「来自账号的直连」行先退回收编行的形状再软删（D15）：墓碑只需要指纹，下发的地址列表、
// 当前地址与 pin-cert 证书都得跟着移除一起从本机消失。清不掉就不删——宁可让用户再点一次，
// 也不留一块带着地址与证书的墓碑。退回收编形状的墓碑随后也和收编来的墓碑一样，在那台
// 机器离开账号后被回收。
func (s *service) Remove(ctx context.Context, id int64) error {
	row, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if row.IsAccountDirect() {
		if err := s.repo.ClearAccountDirect(ctx, id); err != nil {
			return err
		}
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	if err := s.keychain.Delete(keychainAccountForToken(id)); err != nil {
		logger.Ctx(ctx).Warn("remote_device remove: keychain delete failed; id not reused, leak is harmless",
			zap.Int64("id", id), zap.Error(err))
	}
	if s.watcher != nil {
		s.watcher.Stop(id)
	}
	return nil
}

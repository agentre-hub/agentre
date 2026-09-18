package bootstrap

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/pkg/paths"
)

// KeychainDirEnv 把 keychain 后端指向一个隔离的 file keychain 目录。独立 E2E main
// 与正式 main 的本地真实验证都要求 device token / fingerprint / 登录凭据落在各自隔离
// 目录，而不是生产 system keychain。对应 launcher 负责创建 0700 目录并注入这个变量。
const KeychainDirEnv = "AGENTRE_KEYCHAIN_DIR"

// initKeychain 在 bootstrap 装配任何依赖 keychain 的服务(Server / Remote Device /
// ConnPool / watcher)之前确立默认 keychain 后端,让它们捕获同一个实例。
//
// 未设置 KeychainDirEnv → 平台 system keychain,槽位取当前构建渠道的
// Identity().KeychainService(生产行为):stable/beta/nightly/dev 各用自己的 service,
// 互不读写(design decision 8)。构建标记非法时直接报错、不选任何 service —— 调用方
// (Init)已经先因为同一个错误拒绝启动,这里只是不让 keychain 自己发明一个默认槽位。
// 设置了 KeychainDirEnv → 建立并校验 file keychain;目录缺失 / 权限不安全 / 不可写都让
// 启动失败,绝不回退 NewSystem() —— 回退会让一次隔离验证静默写进用户的真实凭据存储。
func initKeychain(_ context.Context) error {
	dir := strings.TrimSpace(os.Getenv(KeychainDirEnv))
	if dir == "" {
		c, err := paths.CurrentChannel()
		if err != nil {
			return fmt.Errorf("select system keychain service: %w", err)
		}
		keychain.SetDefault(keychain.NewSystem(c.Identity().KeychainService))
		return nil
	}
	if err := keychain.ValidateFileDir(dir); err != nil {
		return fmt.Errorf("isolated keychain dir not usable: %w", err)
	}
	keychain.SetDefault(keychain.NewFile(dir))
	return nil
}

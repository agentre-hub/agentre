package bootstrap

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/keychain"
)

// KeychainDirEnv 把 keychain 后端指向一个隔离的 file keychain 目录。独立 E2E main
// 与正式 main 的本地真实验证都要求 device token / fingerprint / 登录凭据落在各自隔离
// 目录，而不是生产 system keychain。对应 launcher 负责创建 0700 目录并注入这个变量。
const KeychainDirEnv = "AGENTRE_KEYCHAIN_DIR"

// initKeychain 在 bootstrap 装配任何依赖 keychain 的服务(Server / Remote Device /
// ConnPool / watcher)之前确立默认 keychain 后端,让它们捕获同一个实例。
//
// 未设置 KeychainDirEnv → 平台 system keychain(生产行为)。设置了 → 建立并校验 file
// keychain;目录缺失 / 权限不安全 / 不可写都让启动失败,绝不回退 NewSystem() —— 回退会
// 让一次隔离验证静默写进用户的真实凭据存储。
func initKeychain(_ context.Context) error {
	dir := strings.TrimSpace(os.Getenv(KeychainDirEnv))
	if dir == "" {
		keychain.SetDefault(keychain.NewSystem())
		return nil
	}
	if err := keychain.ValidateFileDir(dir); err != nil {
		return fmt.Errorf("isolated keychain dir not usable: %w", err)
	}
	keychain.SetDefault(keychain.NewFile(dir))
	return nil
}

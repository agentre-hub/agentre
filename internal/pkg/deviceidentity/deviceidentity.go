// Package deviceidentity 是桌面端设备指纹的**唯一来源**：keychain 账号名，以及「读出或
// 铸出」的规则。值空间的格式本身在 pkg/wire/devicefp（两端共用一个实现）。
//
// 为什么要有这个包：这份东西从前有三处（LAN 配对、账号登录、启动期预铸），其中两份实现
// 逐字节相同、靠注释「必须一致」同步，第三份干脆写进了别的值空间（见 internal/bootstrap
// 的 ServerBoot）。指纹一旦漂移，症状不是报错，而是同一台机器在 server 上变成两台设备。
//
// 依赖方向：只认 keychain 与 devicefp 两个包，不认识任何域服务 —— 于是四个调用点
// （remote_device_svc、server_svc、remote_device_watcher_svc、bootstrap）都不必为了共用
// 它而互相依赖。
package deviceidentity

import (
	"crypto/rand"
	"errors"

	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// KeychainAccount 是本机设备指纹在 keychain 里的账号名（service 名见 internal/pkg/keychain）。
//
// 这个名字被四处读：LAN 配对的 Add / Refresh / 连接池、账号登录、watcher 心跳。它们必须
// 落到**同一个**条目上 —— 打错一个字符不会报错，只会 Get 到 ErrNotFound，于是那个调用点
// 当场铸一个新指纹，同一台机器在 server 上变成两台设备。所以它只在这里定义一次。
const KeychainAccount = "agentre-device-fingerprint"

// seedLen 是实例标识的长度。
//
// 它与指纹的强度无关（32 字节已经均匀随机，再哈希一次不会更强），只与「指纹 = 这个种子的
// sha256」这条格式有关：种子得先存在，指纹才谈得上派生。
const seedLen = 32

// Ensure 读出本机设备指纹；没有就铸一个、存下、再返回。
//
// 它是这个值**唯一**的生成处（R5 决策 8：账号侧不得另生成指纹）。传 nil keychain 返回错误
// 而不是零值：调用方要么装配了 keychain，要么就不该问；悄悄返回空串会让空指纹一路流到
// server，在那里表现为「一台没有身份的机器」。
func Ensure(kc keychain.Keychain) (devicefp.Carrier, error) {
	if kc == nil {
		return "", errors.New("deviceidentity: no keychain backend")
	}
	fp, err := kc.Get(KeychainAccount)
	if err == nil && fp != "" {
		return devicefp.Carrier(fp), nil
	}
	if err != nil && !errors.Is(err, keychain.ErrNotFound) {
		return "", err
	}
	raw := make([]byte, seedLen)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	// 种子按**字节**交出去：string 在 Go 里就是字节容器，非 UTF-8 是常态不是边界情况。
	newFP := string(devicefp.FromSeed(string(raw)))
	if err := kc.Set(KeychainAccount, newFP); err != nil {
		return "", err
	}
	return devicefp.Carrier(newFP), nil
}

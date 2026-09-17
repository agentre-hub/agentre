// Package paths 提供 Agentre 桌面端的根目录定位。
// 单独成包是为了让 bootstrap、agentruntime 等都能引用，避免与 bootstrap 形成 import 环。
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AppName 是 cago 的应用名，也是正式版渠道身份各名称的根。
const AppName = "agentre"

// AppNameAgentred is the AppDataDir leaf for the agentred daemon binary.
// 与 AppName 分开，保证 agentre desktop 与 agentred daemon 在同机运行时文件系统完全隔离。
const AppNameAgentred = "agentred"

// Channel 是构建渠道：正式版(stable)、Beta、Nightly 与 Dev 地位相同，只由构建标记决定，
// 与运行方式(wails dev / go run / 安装包)无关。
type Channel string

const (
	ChannelStable  Channel = "stable"
	ChannelBeta    Channel = "beta"
	ChannelNightly Channel = "nightly"
	ChannelDev     Channel = "dev"
)

// buildChannel 由 ldflags `-X github.com/agentre-hub/agentre/internal/pkg/paths.buildChannel=<value>`
// 注入。留空即 Dev：任何忘记打标记的构建都碰不到正式版数据；正式版必须显式标记 stable。
var buildChannel string

// Identity 是一个渠道在磁盘、系统与发布物上的全部身份，四个渠道两两不冲突，
// 从而能在同一台机器上并排安装、同时运行。
type Identity struct {
	Channel Channel
	// DisplayName 是窗口标题 / bundle 名 / 桌面入口 / Windows 产品名。
	DisplayName string
	// BundleID 是 macOS CFBundleIdentifier。
	BundleID string
	// DataDirName 是 AppDataDir 在平台配置根下的叶子名。
	DataDirName string
	// KeychainService 是系统钥匙串的 service 槽位。
	KeychainService string
	// ReleaseAssetPrefix 是发布包文件名前缀；Dev 不发布，为空。
	ReleaseAssetPrefix string
	// LinuxCommand 是 Linux 包名与 /usr/bin 下的命令名。
	LinuxCommand string
}

var channelIdentities = map[Channel]Identity{
	ChannelStable: {
		Channel: ChannelStable, DisplayName: "Agentre", BundleID: "com.agentrehub.agentre",
		DataDirName: "agentre", KeychainService: "agentre", ReleaseAssetPrefix: "agentre-", LinuxCommand: "agentre",
	},
	ChannelBeta: {
		Channel: ChannelBeta, DisplayName: "Agentre Beta", BundleID: "com.agentrehub.agentre.beta",
		DataDirName: "agentre-beta", KeychainService: "agentre-beta", ReleaseAssetPrefix: "agentre-beta-", LinuxCommand: "agentre-beta",
	},
	ChannelNightly: {
		Channel: ChannelNightly, DisplayName: "Agentre Nightly", BundleID: "com.agentrehub.agentre.nightly",
		DataDirName: "agentre-nightly", KeychainService: "agentre-nightly", ReleaseAssetPrefix: "agentre-nightly-", LinuxCommand: "agentre-nightly",
	},
	// Dev 不发布，没有发布包前缀。
	ChannelDev: {
		Channel: ChannelDev, DisplayName: "Agentre Dev", BundleID: "com.agentrehub.agentre.dev",
		DataDirName: "agentre-dev", KeychainService: "agentre-dev", ReleaseAssetPrefix: "", LinuxCommand: "agentre-dev",
	},
}

// AllChannels 按固定顺序返回全部四个渠道。
func AllChannels() []Channel {
	return []Channel{ChannelStable, ChannelBeta, ChannelNightly, ChannelDev}
}

// ParseChannel 把构建标记解析成渠道：空串即 Dev；四个取值之外的一律报错，
// 不静默归入任何渠道，避免读写错误渠道的数据。
func ParseChannel(value string) (Channel, error) {
	if value == "" {
		return ChannelDev, nil
	}
	c := Channel(value)
	if _, ok := channelIdentities[c]; !ok {
		return "", fmt.Errorf("invalid build channel %q: want one of %s", value, allowedChannels())
	}
	return c, nil
}

func allowedChannels() string {
	names := make([]string, 0, len(channelIdentities))
	for _, c := range AllChannels() {
		names = append(names, string(c))
	}
	return strings.Join(names, ", ")
}

// CurrentChannel 返回本二进制构建时标记的渠道；标记非法时报错，进程应拒绝启动。
func CurrentChannel() (Channel, error) {
	return ParseChannel(buildChannel)
}

// Identity 返回渠道身份；c 必须是 AllChannels 之一(非法值返回只带 Channel 的零身份)。
func (c Channel) Identity() Identity {
	if id, ok := channelIdentities[c]; ok {
		return id
	}
	return Identity{Channel: c}
}

// IsDevMode 判断当前进程是否跑在 wails dev 下：wails dev 会给它编译并启动的二进制注入
// devserver 环境变量(指向 vite dev server)。它只服务 wails dev 的运行机制(跳过单实例锁、
// dev 代理重试)，不参与渠道判定——数据目录与窗口标题一律来自渠道身份。
func IsDevMode() bool {
	return strings.TrimSpace(os.Getenv("devserver")) != ""
}

// AppDataDir 返回 Agentre 本地状态的根目录：<平台配置根>/<渠道 DataDirName>。
//
//	macOS    ~/Library/Application Support/agentre[-beta|-nightly|-dev]/
//	Windows  %LOCALAPPDATA%\agentre[-beta|-nightly|-dev]\
//	Linux    ~/.config/agentre[-beta|-nightly|-dev]/
//
// 构建标记非法时先报错(即使设置了覆盖)，让进程拒绝启动。否则：
//  1. AGENTRE_DATA_DIR 显式覆盖(测试 / 排查 / 自定义)——优先级最高，只改数据目录。
//  2. 默认 → DefaultAppDataDir(平台配置根, CurrentChannel())。
func AppDataDir() (string, error) {
	c, err := CurrentChannel()
	if err != nil {
		return "", err
	}
	if dir := strings.TrimSpace(os.Getenv("AGENTRE_DATA_DIR")); dir != "" {
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return DefaultAppDataDir(base, c), nil
}

// DefaultAppDataDir resolves channel c's desktop data dir beneath a platform
// config root. Isolation guards use the same canonical definition.
func DefaultAppDataDir(base string, c Channel) string {
	return filepath.Join(base, c.Identity().DataDirName)
}

// AgentredDataDir 返回 agentred daemon 的本地状态根目录。
//
//	macOS    ~/Library/Application Support/agentred/
//	Windows  %LOCALAPPDATA%\agentred\
//	Linux    ~/.config/agentred/
//
// 测试 / 排查可用 AGENTRED_DATA_DIR 覆盖（绕过平台默认）。
func AgentredDataDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("AGENTRED_DATA_DIR")); dir != "" {
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(base, AppNameAgentred), nil
}

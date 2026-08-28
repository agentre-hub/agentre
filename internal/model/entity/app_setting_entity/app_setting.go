// Package app_setting_entity 维护 App 全局 key-value 设置项的充血实体。
//
// 服务侧设置持久化到 SQLite；纯前端体验偏好（主题、窗口尺寸等）继续走 localStorage。
package app_setting_entity

import (
	"context"
	"net"
	"strconv"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// 预定义的 key 常量。新增 key 时在这里登记，避免 service 层散写魔法字符串。
const (
	KeyProxyListenHost = "proxy.listen_host"
	KeyProxyListenPort = "proxy.listen_port"

	// KeyUpdateChannel 自动更新通道：stable / beta / nightly。
	KeyUpdateChannel = "update.channel"
	// KeyDownloadMirror 下载镜像前缀；空串表示直连 GitHub。
	KeyDownloadMirror = "update.download_mirror"
	// KeyLastUpdateCheck 上次"检查更新"的 Unix 时间戳，启动自动检查用它做 24h 节流。
	KeyLastUpdateCheck = "update.last_check"
	// KeySkippedUpdateVersion 用户点「跳过此版本」的版本号；空串表示没有跳过任何版本。
	// 只压制到达提示与设置入口红点，不影响状态栏常驻的「有新版本」陈述。
	KeySkippedUpdateVersion = "update.skipped_version"

	// KeyDebugLogging 是否开启 debug 级别日志（"true"/"false"）；缺省关闭。
	// 取代旧的 AGENTRE_DEBUG 环境变量，由「设置 → 版本 & 更新」开关写入。
	KeyDebugLogging = "logger.debug_enabled"

	// 通知设置。bool 型存 "true"/"false"。
	KeyNotifyEnabled           = "notify.enabled"             // 通知总开关
	KeyNotifyOnlyWhenUnfocused = "notify.only_when_unfocused" // 仅窗口未激活时通知
	KeyNotifySystem            = "notify.system"              // 系统原生通知
	KeyNotifyToast             = "notify.toast"               // 应用内 toast
)

// DefaultProxyListenHost 缺省监听地址 —— loopback，只允许本机访问。
const DefaultProxyListenHost = "127.0.0.1"

// DefaultProxyListenPort 缺省监听端口。
// 选 52401 是为了避开常见服务端口段，新装实例与残留 seed='0' 的旧数据会被迁移到这里。
const DefaultProxyListenPort = 52401

// DefaultUpdateChannel 缺省更新通道。
const DefaultUpdateChannel = "stable"

// AppSetting 一行 key-value 设置项记录。
type AppSetting struct {
	Key        string `gorm:"column:key;primaryKey;type:text;not null"`
	Value      string `gorm:"column:value;type:text;not null"`
	Updatetime int64  `gorm:"column:updatetime;type:bigint;not null;default:0"`
}

// TableName 绑定表名。
func (*AppSetting) TableName() string { return "app_settings" }

// IsProxyKey 判断当前 key 是否属于本地 HTTP 代理段，service 层用它决定是否触发 Restart。
func (s *AppSetting) IsProxyKey() bool {
	if s == nil {
		return false
	}
	return strings.HasPrefix(s.Key, "proxy.")
}

// ValidateProxyHost 校验 proxy.listen_host 取值是否为合法 IP。
// 空字符串视为非法（service 层调用前已 TrimSpace）。
func ValidateProxyHost(ctx context.Context, v string) error {
	t := strings.TrimSpace(v)
	if t == "" {
		return i18n.NewError(ctx, code.AppSettingInvalidHost)
	}
	if net.ParseIP(t) == nil {
		return i18n.NewError(ctx, code.AppSettingInvalidHost)
	}
	return nil
}

// ValidateProxyPort 校验 proxy.listen_port 取值是否为 [0, 65535] 的整数。
// 0 表示随机分配。
func ValidateProxyPort(ctx context.Context, v string) error {
	t := strings.TrimSpace(v)
	if t == "" {
		return i18n.NewError(ctx, code.AppSettingInvalidPort)
	}
	n, err := strconv.Atoi(t)
	if err != nil {
		return i18n.NewError(ctx, code.AppSettingInvalidPort)
	}
	if n < 0 || n > 65535 {
		return i18n.NewError(ctx, code.AppSettingInvalidPort)
	}
	return nil
}

// ParseProxyPort 帮助 bootstrap / svc 从 raw value 解析端口；解析失败返回 0。
func ParseProxyPort(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 || n > 65535 {
		return 0
	}
	return n
}

// ParseDebugLogging 解析 logger.debug_enabled 取值；"true"/"1"/"yes"/"on"（忽略大小写、
// 前后空白）视为开启，其余一律关闭。给 bootstrap 启动恢复开关时复用。
func ParseDebugLogging(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

// ValidateUpdateChannel 校验 update.channel 取值是否为合法通道名。
// 空串视为非法（service 层调用前已 TrimSpace）。
func ValidateUpdateChannel(ctx context.Context, v string) error {
	switch strings.TrimSpace(v) {
	case "stable", "beta", "nightly":
		return nil
	}
	return i18n.NewError(ctx, code.InvalidParameter)
}

// ValidateBoolSetting 校验布尔型设置项取值，仅接受 "true"/"false"（service 层调用前已 TrimSpace）。
func ValidateBoolSetting(ctx context.Context, v string) error {
	switch strings.TrimSpace(v) {
	case "true", "false":
		return nil
	}
	return i18n.NewError(ctx, code.AppSettingInvalidBool)
}

// ParseBoolSetting 解析布尔型设置项；仅 "true" 视为开启，其余（含空）关闭。
func ParseBoolSetting(v string) bool {
	return strings.TrimSpace(v) == "true"
}

// Package pathguard 提供 remotefs 子系统的路径与名字合法性校验。
// daemon 与 host svc 都 import 本包,确保前后端用同一套规则。
package pathguard

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/pathdepth"
)

var (
	// ErrPathRefused 表示路径非法或命中黑名单(系统虚拟 fs)。前端应展示
	// 「路径被拒绝」类文案,不暴露具体原因避免成探测信道。
	ErrPathRefused = errors.New("remotefs: path refused")
	// ErrInvalidName mkdir / 未来 rename 用,名字含非法字符或长度越界。
	ErrInvalidName = errors.New("remotefs: invalid name")
)

// refusedPrefixes 命中后整条路径都拒绝:既包括 /proc 这个根本身,也包括
// /proc/cpuinfo 这种子路径。用前缀匹配 + 边界保护,避免 /devious 这种
// 偶然命中。
var refusedPrefixes = []string{"/proc", "/sys", "/dev"}

// HomeFunc 返回当前用户 home;daemon 端注入 os.UserHomeDir。
type HomeFunc func() (string, error)

// ResolvePath 把请求的 raw 规范化为可用于 os 调用的绝对路径。
//   - raw == "" → 调 homeFn() 取 home
//   - 必须绝对(以 / 开头)
//   - 若任意 ".." 段导致路径深度归零(根以上),则拒绝(在 Clean 之前检查)
//   - 不能命中 refusedPrefixes
func ResolvePath(raw string, homeFn HomeFunc) (string, error) {
	if raw == "" {
		home, err := homeFn()
		if err != nil {
			return "", err
		}
		raw = home
	}
	if !filepath.IsAbs(raw) {
		return "", ErrPathRefused
	}
	// 模拟路径遍历:用栈跟踪路径深度,若 ".." 导致深度归零(根以上)则拒绝。
	// filepath.Clean 在 POSIX 上会把 /../etc 解析为 /etc(无残留 ..)，
	// 所以必须在 Clean 之前对原始路径做安全检查。
	// 允许 //a/./b/c/.. → /a/b(深度不归零),拒绝 /../etc(根处 .. 归零)。
	if pathdepth.EscapesRoot(raw) {
		return "", ErrPathRefused
	}
	cleaned := filepath.Clean(raw)
	for _, p := range refusedPrefixes {
		if cleaned == p || strings.HasPrefix(cleaned, p+"/") {
			return "", ErrPathRefused
		}
	}
	return cleaned, nil
}

// ValidateName 校验 mkdir 用的文件夹名:不能含 / 或为 . / ..,不能首尾空白,
// 不能为空,长度 <=255。
func ValidateName(name string) error {
	if name == "" || name == "." || name == ".." {
		return ErrInvalidName
	}
	if strings.ContainsRune(name, '/') {
		return ErrInvalidName
	}
	if strings.TrimSpace(name) != name {
		return ErrInvalidName
	}
	if len(name) > 255 {
		return ErrInvalidName
	}
	return nil
}

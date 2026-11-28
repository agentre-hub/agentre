package devicefacts

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"

	"github.com/agentre-hub/agentre/internal/pkg/ccoauth"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// LoginProbe 判 Claude Code 与 Codex 的原生登录态:只看 CLI 自己的凭据**在不在**,
// 从不读凭据文件的正文、不取钥匙串条目的值。判断不了时答「未知」—— 派发把未知当
// 可用,一个猜错的「未登录」会把会话从一台本来跑得了的设备上挪走。
type LoginProbe struct {
	// Home 是当前用户的主目录;空 = 判断不了。
	Home   string
	Getenv func(string) string
	// Exists 判一个文件在不在;只有这一处会碰凭据文件,它也只 stat。
	Exists func(path string) (bool, error)
	// ReadFile 只用来读 Codex 的 config.toml(不含凭据),看凭据存在哪。
	ReadFile func(path string) ([]byte, error)
	// Keychain 问系统钥匙串里有没有这个服务名的条目(不取值)。nil = 这个平台上
	// CLI 不用钥匙串存凭据。
	Keychain func(service string) (bool, error)
}

// DefaultLoginProbe 是这台机器上的真实探测。
func DefaultLoginProbe() LoginProbe {
	home, _ := os.UserHomeDir()
	p := LoginProbe{
		Home:     home,
		Getenv:   os.Getenv,
		Exists:   fileExists,
		ReadFile: os.ReadFile,
	}
	if runtime.GOOS == "darwin" {
		p.Keychain = macKeychainHas
	}
	return p
}

// ClaudeCode 判 Claude Code:配置目录($CLAUDE_CONFIG_DIR,缺省 ~/.claude)下的
// .credentials.json 在 → 已登录;否则(macOS)看 CLI 自己的钥匙串服务名。
func (p LoginProbe) ClaudeCode() string {
	envDir := p.getenv("CLAUDE_CONFIG_DIR")
	dir := envDir
	if dir == "" {
		if p.Home == "" {
			return syncwire.NativeLoginUnknown
		}
		dir = filepath.Join(p.Home, ".claude")
	}
	present, err := p.Exists(filepath.Join(dir, ".credentials.json"))
	if err != nil {
		return syncwire.NativeLoginUnknown
	}
	if present {
		return syncwire.NativeLoginLoggedIn
	}
	if p.Keychain == nil {
		return syncwire.NativeLoginLoggedOut
	}
	found, err := p.Keychain(ccoauth.KeychainServiceName(envDir))
	switch {
	case err != nil:
		return syncwire.NativeLoginUnknown
	case found:
		return syncwire.NativeLoginLoggedIn
	default:
		return syncwire.NativeLoginLoggedOut
	}
}

// codexKeyringStore 认出 config.toml 把凭据放到了系统钥匙串(keyring / auto)——
// 那时没有 auth.json 也说明不了没登录。
var codexCredentialStore = regexp.MustCompile(`(?m)^\s*cli_auth_credentials_store\s*=\s*"([^"]*)"`)

// Codex 判 Codex:主目录($CODEX_HOME,缺省 ~/.codex)下的 auth.json 在 → 已登录;
// 不在且 config.toml 没把凭据放进钥匙串 → 未登录;放进了钥匙串 → 未知。
func (p LoginProbe) Codex() string {
	dir := p.getenv("CODEX_HOME")
	if dir == "" {
		if p.Home == "" {
			return syncwire.NativeLoginUnknown
		}
		dir = filepath.Join(p.Home, ".codex")
	}
	present, err := p.Exists(filepath.Join(dir, "auth.json"))
	if err != nil {
		return syncwire.NativeLoginUnknown
	}
	if present {
		return syncwire.NativeLoginLoggedIn
	}
	if p.ReadFile != nil {
		if body, err := p.ReadFile(filepath.Join(dir, "config.toml")); err == nil {
			if m := codexCredentialStore.FindSubmatch(body); m != nil && string(m[1]) != "file" {
				return syncwire.NativeLoginUnknown
			}
		}
	}
	return syncwire.NativeLoginLoggedOut
}

func (p LoginProbe) getenv(key string) string {
	if p.Getenv == nil {
		return ""
	}
	return p.Getenv(key)
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// securityItemNotFound 是 /usr/bin/security 找不到条目时的退出码。
const securityItemNotFound = 44

// macKeychainHas 问登录钥匙串里有没有这个服务名的通用密码条目。不带 -w / -g:
// security 只打印条目属性,不取密码,因此也不会弹出钥匙串授权框。
func macKeychainHas(service string) (bool, error) {
	//nolint:gosec // G204: 固定的系统工具与只读子命令;service 是 Claude Code 自己的钥匙串服务名(ccoauth.KeychainServiceName),不是外部输入
	cmd := exec.Command("/usr/bin/security", "find-generic-password", "-s", service)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == securityItemNotFound {
		return false, nil
	}
	return false, err
}

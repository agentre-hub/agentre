package piagent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// Pi 子进程（subagent）要解析 --model agentre-<providerKey>/<model>，就得在自己进程里
// registerProvider。父进程那份 per-session provider 扩展走 --extension，而 pi 没有把
// extension 传给子进程的通道（--extension 只能显式传，也没有对应的 env），子进程只继承进程
// env。所以 Agentre 另外把一个静态、env 驱动的扩展装进 Pi 的全局扩展发现目录：
//
//	<Pi 配置目录>/extensions/agentre-provider.js
//
// 父进程与所有子进程都会自动加载它，provider 定义每会话经
// agentruntime.PiAgentProviderRegistryEnvKey 下发，密钥仍只在 env。
const childProviderExtensionFileName = "agentre-provider.js"

// childProviderExtensionSource 是安装的静态扩展源码：不含供应商、密钥与会话信息，只读注册表
// env。没有注册表 env 时什么都不注册 —— 用户在 Agentre 之外跑的 pi 行为完全不变。
const childProviderExtensionSource = `export default function agentreProvider(pi) {
	const raw = process.env["` + agentruntime.PiAgentProviderRegistryEnvKey + `"];
	if (!raw) return;
	let registry;
	try {
		registry = JSON.parse(raw);
	} catch (error) {
		process.stderr.write("agentre-provider: ignoring invalid ` + agentruntime.PiAgentProviderRegistryEnvKey + `: " + error + "\n");
		return;
	}
	if (!registry || typeof registry !== "object" || Array.isArray(registry)) return;
	for (const [name, config] of Object.entries(registry)) {
		if (!name || !config || typeof config !== "object") continue;
		pi.registerProvider(name, config);
	}
}
`

// childProviderExtensionInstaller 物化全局子进程 provider 扩展的可注入点：生产实现
// ensureChildProviderExtension；单测可换成 fake，使会话装配测试不碰用户的 ~/.pi/agent。
// 入参是本次运行下发给 pi 进程的 env 覆盖（agent 后端 env_json），与 pi 子进程拿到的是同一份。
var childProviderExtensionInstaller = ensureChildProviderExtension

// SetChildProviderExtensionInstallerForTest 替换 childProviderExtensionInstaller，返回恢复函数。
func SetChildProviderExtensionInstallerForTest(fn func(env map[string]string) error) func() {
	old := childProviderExtensionInstaller
	childProviderExtensionInstaller = fn
	return func() { childProviderExtensionInstaller = old }
}

// ensureChildProviderExtension 把静态扩展写到 <Pi 配置目录>/extensions/agentre-provider.js。
// 幂等：内容一致则不重写（不反复动用户的文件），内容陈旧则覆盖（文件由 Agentre 拥有）。
func ensureChildProviderExtension(env map[string]string) error {
	dir, err := piAgentDir(env)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "extensions", childProviderExtensionFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("piagent: create pi extensions dir: %w", err)
	}
	installed, readErr := os.ReadFile(path) //nolint:gosec // G304: 路径由 PI_CODING_AGENT_DIR / HOME 拼出，不是用户输入
	switch {
	case readErr == nil && string(installed) == childProviderExtensionSource:
		return nil
	case readErr != nil && !errors.Is(readErr, os.ErrNotExist):
		return fmt.Errorf("piagent: read installed provider extension %q: %w", path, readErr)
	}
	// 先写临时文件再改名：并发首装（一台 daemon 上多个会话、或升级后第一条会话）不能让任何
	// pi 子进程读到半截文件——那等于那个子进程没注册 provider，正是要修的那个失败。
	tmp, err := os.CreateTemp(filepath.Dir(path), childProviderExtensionFileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("piagent: create provider extension temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // 改名成功后删不到（已不在），失败路径靠它收尾
	if _, err := tmp.WriteString(childProviderExtensionSource); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("piagent: write provider extension temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("piagent: close provider extension temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return fmt.Errorf("piagent: chmod provider extension temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("piagent: replace provider extension %q: %w", path, err)
	}
	return nil
}

// piAgentDir 解析 pi CLI 的配置目录（与 pi 自己的 getAgentDir 同一口径）：PI_CODING_AGENT_DIR
// 优先，否则 <home>/.pi/agent（与 transcript.go 的 sessionsRoot 同一约定）。
// 查 env 时本次运行下发给 pi 的覆盖优先于本进程 env —— agent 后端 env_json 里给的
// PI_CODING_AGENT_DIR / HOME 会一起进 pi 子进程，装到别处等于没装（远端 daemon 尤其常见）。
func piAgentDir(env map[string]string) (string, error) {
	if dir := strings.TrimSpace(lookupPiEnv(env, "PI_CODING_AGENT_DIR")); dir != "" {
		return dir, nil
	}
	home := strings.TrimSpace(lookupPiEnv(env, "HOME"))
	if home == "" {
		home = strings.TrimSpace(lookupPiEnv(env, "USERPROFILE"))
	}
	if home == "" {
		resolved, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("piagent: resolve user home dir: %w", err)
		}
		home = resolved
	}
	return filepath.Join(home, ".pi", "agent"), nil
}

// lookupPiEnv 先看本次运行下发给 pi 的 env 覆盖，再看本进程 env —— 与 pkg/piagent 的
// buildEnv（os.Environ() 之上叠加覆盖）同一口径。
func lookupPiEnv(env map[string]string, key string) string {
	if value, ok := env[key]; ok && strings.TrimSpace(value) != "" {
		return value
	}
	return os.Getenv(key)
}

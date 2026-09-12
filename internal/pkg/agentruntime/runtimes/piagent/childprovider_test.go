package piagent

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// nodeAvailable 返回本机 node 路径：安装的扩展由 Pi 的 node 运行时加载，「读 env → 注册
// provider」这条契约只有真加载一次才观察得到。没有 node 的机器跳过（`make test` 本身需要
// node 构建前端）。
func nodeAvailable(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available: the installed extension is loaded by Pi's node runtime")
	}
	return node
}

// nodeHarnessSource 是装载扩展的替身 Pi：只收集 registerProvider 的调用，打印成 JSON 对象。
// 它的输出就是扩展对外可观察的全部效果。
const nodeHarnessSource = `import { pathToFileURL } from "node:url";

const [extensionPath] = process.argv.slice(2);
const registered = {};
const pi = {
	registerProvider(name, config) {
		registered[name] = config;
	},
};
const extension = await import(pathToFileURL(extensionPath).href);
await extension.default(pi);
process.stdout.write(JSON.stringify(registered));
`

func TestEnsureChildProviderExtension_InstallsUnderPiExtensionsDir(t *testing.T) {
	root := t.TempDir()

	require.NoError(t, ensureChildProviderExtension(map[string]string{"PI_CODING_AGENT_DIR": root}))

	// 落点就是 Pi 的全局扩展发现目录（<配置目录>/extensions/*.js）：子进程只继承 env，没有
	// 任何 --extension 能到达它，只有这个目录能让子进程自己也注册 provider。
	path := filepath.Join(root, "extensions", childProviderExtensionFileName)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
	content, err := os.ReadFile(path) //nolint:gosec // G304: 路径由 PI_CODING_AGENT_DIR(=t.TempDir()) 拼出
	require.NoError(t, err)
	assert.NotEmpty(t, content)
	// Pi 用 extension.default 当工厂函数，扩展必须导出它。
	assert.Contains(t, string(content), "export default")
	// 写临时文件再改名的落地：目录里不许留下任何中间产物（那是用户的 pi 配置目录）。
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, childProviderExtensionFileName, entries[0].Name())
}

// TestEnsureChildProviderExtension_PrefersRunEnvOverProcessEnv 钉住远端 daemon 最容易踩的那条：
// agent 后端 env_json 里给的 PI_CODING_AGENT_DIR 会一起下发给 pi 子进程，装到本进程 env
// 解析出的目录等于没装（子进程根本不看那里）。
func TestEnsureChildProviderExtension_PrefersRunEnvOverProcessEnv(t *testing.T) {
	processDir := t.TempDir()
	runDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", processDir)

	require.NoError(t, ensureChildProviderExtension(map[string]string{"PI_CODING_AGENT_DIR": runDir}))

	_, err := os.Stat(filepath.Join(runDir, "extensions", childProviderExtensionFileName))
	require.NoError(t, err, "必须装到本次运行下发给 pi 的配置目录")
	_, err = os.Stat(filepath.Join(processDir, "extensions", childProviderExtensionFileName))
	assert.True(t, os.IsNotExist(err), "本进程 env 的目录这次不是 pi 的配置目录")
}

func TestEnsureChildProviderExtension_UsesRunEnvHomeWhenConfigDirUnset(t *testing.T) {
	runHome := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("HOME", t.TempDir())

	require.NoError(t, ensureChildProviderExtension(map[string]string{"HOME": runHome}))

	_, err := os.Stat(filepath.Join(runHome, ".pi", "agent", "extensions", childProviderExtensionFileName))
	require.NoError(t, err, "env_json 覆盖 HOME 时也要落在同一条链上解析出的配置目录")
}

func TestEnsureChildProviderExtension_DefaultsToProcessHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("$HOME 不是 Windows 的 home 解析来源")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", "")

	require.NoError(t, ensureChildProviderExtension(nil))

	_, err := os.Stat(filepath.Join(home, ".pi", "agent", "extensions", childProviderExtensionFileName))
	require.NoError(t, err, "运行 env 什么都没给时必须落在 pi 的默认配置目录")
}

func TestEnsureChildProviderExtension_RewritesStaleContentAndSkipsIdenticalContent(t *testing.T) {
	root := t.TempDir()
	env := map[string]string{"PI_CODING_AGENT_DIR": root}
	path := filepath.Join(root, "extensions", childProviderExtensionFileName)

	require.NoError(t, ensureChildProviderExtension(env))
	installed, err := os.ReadFile(path) //nolint:gosec // G304: 同上
	require.NoError(t, err)

	// 旧版本（或别人占了这个文件名）必须被覆盖：装上去的内容就是合同。
	require.NoError(t, os.WriteFile(path, []byte("export default function (pi) {}\n"), 0o644))
	require.NoError(t, ensureChildProviderExtension(env))
	rewritten, err := os.ReadFile(path) //nolint:gosec // G304: 同上
	require.NoError(t, err)
	assert.Equal(t, string(installed), string(rewritten))

	// 内容一致时不重写，避免每次会话装配都去动用户的文件。
	before, err := os.Stat(path)
	require.NoError(t, err)
	require.NoError(t, ensureChildProviderExtension(env))
	after, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, after.ModTime().Equal(before.ModTime()), "内容一致时不得重写")
}

func TestEnsureChildProviderExtension_ReportsUnusableConfigDir(t *testing.T) {
	// 配置目录位置被一个普通文件占住 → MkdirAll 失败。装不上必须是可上报的错误（调用方
	// 只告警不拦会话），不能假装成功。
	blocked := filepath.Join(t.TempDir(), "blocked")
	require.NoError(t, os.WriteFile(blocked, []byte("not a dir"), 0o644))

	err := ensureChildProviderExtension(map[string]string{"PI_CODING_AGENT_DIR": blocked})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blocked")
}

func TestChildProviderExtension_RegistersProvidersFromEnv(t *testing.T) {
	node := nodeAvailable(t)
	root := t.TempDir()
	require.NoError(t, ensureChildProviderExtension(map[string]string{"PI_CODING_AGENT_DIR": root}))
	extensionPath := filepath.Join(root, "extensions", childProviderExtensionFileName)

	// Pi 用 jiti 加载扩展，不受 node 自身 .js 的 ESM 判定策略影响；这里补一个 type:module 的
	// package.json，让本测试在任何 node 版本下都以同一语义加载同一个文件。
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte("{\"type\":\"module\"}\n"), 0o644))
	harness := filepath.Join(root, "load-extension.mjs")
	require.NoError(t, os.WriteFile(harness, []byte(nodeHarnessSource), 0o644))

	// 注册表就是 agentruntime 下发给子进程的那份 config：扩展必须原样交给 registerProvider。
	registry := `{"agentre-a": {"name": "a", "baseUrl": "https://a.example.com", ` +
		`"api": "openai-completions", "apiKey": "$KEY_A", ` +
		`"models": [{"id": "m-a", "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}]}, ` +
		`"agentre-b": {"name": "b", "baseUrl": "https://b.example.com", ` +
		`"api": "anthropic-messages", "apiKey": "$KEY_B", ` +
		`"models": [{"id": "m-b", "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}]}}`

	cases := []struct {
		name       string
		registry   string
		want       string
		wantStderr string
	}{
		{"没有注册表 env 时什么都不注册", "", "{}", ""},
		{"有注册表时逐个原样注册", registry, registry, ""},
		{"注册表不是合法 JSON 时不注册也不抛错", "{oops", "{}", "agentre-provider:"},
		{"注册表不是对象时不注册", `["agentre-a"]`, "{}", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := runExtensionHarness(t, node, harness, extensionPath, tc.registry)
			require.NoError(t, err, "扩展加载失败：%s", stderr)
			assert.JSONEq(t, tc.want, stdout)
			if tc.wantStderr == "" {
				return
			}
			assert.Contains(t, stderr, tc.wantStderr)
		})
	}
}

// runExtensionHarness 在子 node 进程里真加载装好的扩展，返回它注册了什么。env 从当前进程
// 继承（子 Pi 就是这样继承父进程 env 的），只重设注册表变量 —— 本机真跑着 Agentre 时，
// 当前进程 env 里就带着它。
func runExtensionHarness(t *testing.T, node, harness, extensionPath, registry string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := exec.Command(node, harness, extensionPath) //nolint:gosec // G204: node 来自 LookPath，参数是测试自建路径
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, agentruntime.PiAgentProviderRegistryEnvKey+"=") {
			continue
		}
		env = append(env, entry)
	}
	if registry != "" {
		env = append(env, agentruntime.PiAgentProviderRegistryEnvKey+"="+registry)
	}
	cmd.Env = env
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	runErr := cmd.Run()
	return out.String(), errOut.String(), runErr
}

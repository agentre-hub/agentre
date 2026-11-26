package cliprober

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbeAntigravity_ShowsVersion(t *testing.T) {
	bin := writeExecutable(t, t.TempDir(), "agy", `[ "$1" = "--version" ] && echo 1.5.16`)
	resp, err := Probe(context.Background(), ProbeRequest{Type: "antigravity", CLIPath: bin})
	require.NoError(t, err)
	assert.Equal(t, "1.5.16", resp.Text)
}

func TestProbeAntigravity_ResolvesAgyOnPATH(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "agy", `echo 1.6.0`)
	t.Setenv("PATH", dir)
	resp, err := Probe(context.Background(), ProbeRequest{Type: "antigravity"})
	require.NoError(t, err)
	assert.Equal(t, "1.6.0", resp.Text)
}

func TestProbeAntigravity_VersionGate(t *testing.T) {
	for _, tc := range []struct {
		ver     string
		tooOld  bool
		garbage bool
	}{
		{ver: "1.5.0"}, {ver: "1.10.2"}, {ver: "2.0.0"},
		{ver: "1.4.99", tooOld: true}, {ver: "0.9", tooOld: true},
		{ver: "garbage", garbage: true},
	} {
		t.Run(tc.ver, func(t *testing.T) {
			bin := writeExecutable(t, t.TempDir(), "agy", "echo "+tc.ver)
			resp, err := Probe(context.Background(), ProbeRequest{Type: "antigravity", CLIPath: bin})
			switch {
			case tc.tooOld:
				require.Error(t, err)
				assert.Contains(t, err.Error(), "版本过低，需要 1.5 或更新")
				assert.Contains(t, err.Error(), tc.ver)
			case tc.garbage:
				require.Error(t, err)
				assert.Contains(t, err.Error(), "garbage")
			default:
				require.NoError(t, err)
				assert.Equal(t, tc.ver, resp.Text)
			}
		})
	}
}

func TestProbeAntigravity_NotInstalled(t *testing.T) {
	// 「哪儿都找不到 agy」由注入点给出：清空 PATH 不够，增强 PATH 还会并入登录 shell 与
	// ~/.local/bin，开发机上装着 agy 时用例会假红。
	prev := resolveAntigravityBinary
	resolveAntigravityBinary = func(string, []string) (string, bool) { return "", false }
	_, err := Probe(context.Background(), ProbeRequest{Type: "antigravity"})
	resolveAntigravityBinary = prev
	require.Error(t, err)
	assert.Contains(t, err.Error(), "未安装")

	_, err = Probe(context.Background(), ProbeRequest{Type: "antigravity", CLIPath: filepath.Join(t.TempDir(), "nope")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "未安装")
}

func TestProbeAntigravity_ExitFailure(t *testing.T) {
	bin := writeExecutable(t, t.TempDir(), "agy", "echo boom >&2; exit 3")
	_, err := Probe(context.Background(), ProbeRequest{Type: "antigravity", CLIPath: bin})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestResolveCLIPath_Antigravity(t *testing.T) {
	dir := t.TempDir()
	want := writeExecutable(t, dir, "agy", "echo 1.5.16")
	t.Setenv("PATH", dir)
	got, found, err := ResolveCLIPath("antigravity")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, want, got)
}

func TestScanAllCLIs_ExcludesAntigravity(t *testing.T) {
	for _, r := range ScanAllCLIs() {
		assert.NotEqual(t, "antigravity", r.BackendType)
	}
}

// 探测与真正起 agy 的 cliprocess.Start 必须看到同一份环境：GUI 启动的桌面端 PATH 很瘦，
// npm / nvm 装的 agy 是靠同目录解释器跑起来的启动脚本。只有经 clienv 增强过的 PATH（含
// 可执行文件所在目录）才找得到它，否则探测报失败而会话照样能跑。
func TestProbeAntigravity_ChildSeesTheAugmentedLaunchEnvironment(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "agy-runtime", `echo 1.5.16`)
	bin := writeExecutable(t, dir, "agy", `exec agy-runtime "$@"`)
	t.Setenv("PATH", t.TempDir())

	resp, err := Probe(context.Background(), ProbeRequest{Type: "antigravity", CLIPath: bin})
	require.NoError(t, err)
	assert.Equal(t, "1.5.16", resp.Text)
}

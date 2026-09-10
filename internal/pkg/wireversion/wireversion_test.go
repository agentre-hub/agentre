package wireversion_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
)

// Given the wire protocol version is owned by @agentre-hub/agentre-wire's
// package.json, When the Go constant is read, Then the two must be byte
// identical — Go cannot read package.json at build time, so this guard is the
// only thing stopping the handshake from advertising a version nobody else
// speaks.
func TestProtocol_GivenWirePackageJSON_WhenCompared_ThenGoConstantMatchesVerbatim(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "resolve guard test path")
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
	manifestPath := filepath.Join(repoRoot, "frontend", "packages", "agentre-wire", "package.json")

	// manifestPath is derived from this test file's own location inside the repo.
	raw, err := os.ReadFile(manifestPath)
	require.NoError(t, err)

	var manifest struct {
		Version string `json:"version"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.NotEmpty(t, manifest.Version, "agentre-wire package.json carries no version")

	require.Equal(t, manifest.Version, wireversion.Protocol,
		"wireversion.Protocol must be updated together with frontend/packages/agentre-wire/package.json")
}

// Given the version window this round collapses to a single point (both ends
// pinned to the same released version, see docs/specs/2026-08-31-conversation-
// centric-addressing.md "协议版本窗口"), When the Go constant is read, Then
// MinSupported must be pinned exactly as tightly as Protocol is: to the same
// package.json — leaving it to drift independently would silently open (or
// close) the window without anyone having decided to.
func TestMinSupported_GivenWirePackageJSON_WhenCompared_ThenGoConstantMatchesVerbatim(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "resolve guard test path")
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
	manifestPath := filepath.Join(repoRoot, "frontend", "packages", "agentre-wire", "package.json")

	raw, err := os.ReadFile(manifestPath)
	require.NoError(t, err)

	var manifest struct {
		Version string `json:"version"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.NotEmpty(t, manifest.Version, "agentre-wire package.json carries no version")

	require.Equal(t, manifest.Version, wireversion.MinSupported,
		"wireversion.MinSupported must be updated together with frontend/packages/agentre-wire/package.json")
}

// previousProtocol 是两级帧之前的那一档协议版本 —— 本轮之前发布的构建报出来的就是它。
//
// 它写死成字面量而不是从 Protocol 推算:这条守卫要钉的正是"上一档被关在门外"这个
// 具体事实,推算出来的值会随 Protocol 一起漂,守卫也就跟着失效了。
const previousProtocol = "0.1.0"

// Given 两级帧改了线上契约(预览帧不带 seq、补齐只回块级持久帧),旧构建按老契约
// 解读新帧就会静默错位;
// When  一台落后一档的构建(报出 previousProtocol)来握手;
// Then  握手当场拒绝并说明双方版本 —— 而不是握上手、直到第一条新帧才炸。
//
// 这是"不做跨版本降级分支"的另一半(spec「兼容性」):不给旧构建留兼容路径,就必须
// 保证它根本连不上。窗口在本轮抬升之后收成一个点(MinSupported == Protocol,见
// methodset_test.go 的守恒律),上一档因此落在 MinSupported 之下。
func TestMatch_GivenABuildOneVersionBehind_WhenItHandshakes_ThenItIsRejectedThere(t *testing.T) {
	t.Parallel()

	require.NotEqual(t, previousProtocol, wireversion.Protocol,
		"协议版本必须已经跨过两级帧这一档,否则上一档的构建仍旧握得上手")
	require.False(t, wireversion.Match(previousProtocol, previousProtocol),
		"落后一档的构建必须在握手处被拒")
	require.Contains(t, wireversion.Reject(previousProtocol, previousProtocol), previousProtocol,
		"拒绝理由要带上对端报出的版本")
	require.Contains(t, wireversion.Reject(previousProtocol, previousProtocol), wireversion.Protocol,
		"拒绝理由要带上本方窗口")
}

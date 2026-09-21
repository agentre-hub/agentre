package guard_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// protoCheckManifestRel 是跑 buf 的那个入口所在的 package.json(相对仓库根)。
// buf 不在 PATH 上,它是 @agentre-hub/agentre-wire 的 devDependency,因此唯一的入口
// 是那个包的 pnpm 脚本。
const protoCheckManifestRel = "frontend/packages/agentre-wire/package.json"

// TestEveryGeneratedOutputIsUnderTheFreshnessCheck 守住「签入的产物与重跑
// buf generate 不一致时会变红」这条仍然成立。
//
// 真正做这件事的是 `pnpm proto:check`:它重跑 buf generate,再 `git diff --exit-code`
// 比对签入的产物。那条守卫的有效范围就是它 git diff 后面列的那几个路径 —— 新加一个
// 落在范围之外的 out:,产物会照常生成,却再也没有任何东西比对它,于是"忘了重新生成"
// 从此静默通过。Swift 产物正是这样一个新增的 out:。
//
// 这里因此断言:buf.gen.yaml 里的**每一个** out:,都落在 proto:check 的 git diff 范围内。
func TestEveryGeneratedOutputIsUnderTheFreshnessCheck(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	moduleRoot := filepath.Join(root, "pkg", "wire")

	script := protoCheckScript(t, root)
	require.Contains(t, script, "buf generate",
		"proto:check 不再重新生成产物,比对的就只是自己")
	require.Contains(t, script, "git diff --exit-code",
		"proto:check 不再比对签入的产物")

	// proto:check 的 cwd 是 pkg/wire(脚本开头 `cd ../../../pkg/wire`),
	// git diff 后面那几个路径因此都相对它解析。
	scopes := diffScopes(t, script, moduleRoot)
	require.NotEmpty(t, scopes, "从 proto:check 里解不出任何 git diff 路径")

	outs := generatedOutputs(t, moduleRoot)
	require.Len(t, outs, 3, "buf.gen.yaml 的 out: 条数变了,请连同本守卫一起复核")

	for _, out := range outs {
		abs := filepath.Clean(filepath.Join(moduleRoot, filepath.FromSlash(out)))
		require.DirExists(t, abs, "out: %s 指向的目录不存在", out)

		covered := false
		for _, scope := range scopes {
			if abs == scope || strings.HasPrefix(abs, scope+string(filepath.Separator)) {
				covered = true
				break
			}
		}
		require.True(t, covered,
			"out: %s 不在 proto:check 的 git diff 范围内(%v);它的产物过期时没有任何守卫会变红",
			out, scopes)
	}
}

// protoCheckScript 取出 proto:check 这条 pnpm 脚本的原文。
func protoCheckScript(t *testing.T, root string) string {
	t.Helper()
	//nolint:gosec // 路径是本文件里的常量拼在仓库根上,不是外部输入。
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(protoCheckManifestRel)))
	require.NoError(t, err)

	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))
	script, ok := manifest.Scripts["proto:check"]
	require.True(t, ok, "%s 里没有 proto:check 脚本", protoCheckManifestRel)
	return script
}

// diffScopes 解出 proto:check 里 `git diff --exit-code --` 后面那几个路径,
// 并解析成绝对路径。
func diffScopes(t *testing.T, script, moduleRoot string) []string {
	t.Helper()
	_, after, found := strings.Cut(script, "git diff --exit-code -- ")
	require.True(t, found, "proto:check 的 git diff 没有 `-- <路径>` 限定,范围无从判断")

	scopes := make([]string, 0, 2)
	for _, field := range strings.Fields(after) {
		if strings.HasPrefix(field, "&") || strings.HasPrefix(field, "|") {
			break
		}
		scopes = append(scopes, filepath.Clean(filepath.Join(moduleRoot, filepath.FromSlash(field))))
	}
	return scopes
}

// generatedOutputs 取出 buf.gen.yaml 里全部 out: 的值。
//
// 这里按行读而不是解 YAML:pkg/wire 是协议产物的 module,它的 go.mod 只该有协议本身
// 需要的依赖,为一条守卫引一个 YAML 库不值得。out: 在本文件里永远是 `    out: <路径>`
// 这一种形态,读错了下面那条 DirExists 立刻会说出来。
func generatedOutputs(t *testing.T, moduleRoot string) []string {
	t.Helper()
	//nolint:gosec // 同上:moduleRoot 由 repoRoot 算出,文件名是常量。
	raw, err := os.ReadFile(filepath.Join(moduleRoot, "buf.gen.yaml"))
	require.NoError(t, err)

	content := string(raw)
	require.Contains(t, content, "clean: true",
		"buf.gen.yaml 不再 clean;out: 目录里的陈旧产物会留下来,比对看不出已经没人生成它了")

	outs := make([]string, 0, 3)
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(trimmed, "out:"); ok {
			outs = append(outs, strings.TrimSpace(value))
		}
	}
	return outs
}

package workspacefs

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/procattr"
)

// runGit 在 dir 下执行 `git args...`,可选喂 stdin,返回 trimmed 之前的原始
// stdout。stderr 丢弃(不透出细节,避免探测信道)。返回的 err 可能是
// *exec.ExitError,调用方按需检查退出码(例如 check-ignore 的 exit=1 是合法
// 的"无匹配",不是失败)。
//
// 子命令与选项全部是调用点硬编码的;唯一来自调用方的值是基线 ref(RefExists /
// mergeBase 的入参,daemon 侧由客户端经 wire 传入),它们先过 isRefName 把
// "-" 开头的值挡在外面,不会被 git 当选项吃掉。
//
// 全局带 --no-optional-locks:本包只读,而 status / diff 顺手刷新索引时会去抢
// .git/index.lock —— 面板恰恰在轮次结束(agent 刚跑完 git)时取数,抢锁会让
// agent 自己的 git add/commit 报 "Unable to create '.git/index.lock'"。
func runGit(ctx context.Context, dir string, stdin []byte, args ...string) (string, error) {
	args = append([]string{"--no-optional-locks"}, args...)
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // G204: controlled args, refs guarded by isRefName
	procattr.ApplyNoConsoleWindow(cmd)
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	err := cmd.Run()
	return out.String(), err
}

// gitIgnoredSet 用 `git check-ignore --stdin -z` 判定 names(相对 dir)中哪些
// 被 git 忽略,自动尊重嵌套 .gitignore / .git/info/exclude / 全局 excludes
// (设计决策 7)。
//
// dir 不在任何 git 工作树内、或 git 调用因任何原因失败时,返回空集合——
// 调用方据此不对任何条目打忽略标记(设计决策 7:"非 git 目录不判定")。
// exit code 1("none of the specified files is ignored")是合法结果,不当
// 失败处理。
func gitIgnoredSet(ctx context.Context, dir string, names []string) map[string]bool {
	ignored := make(map[string]bool)
	if len(names) == 0 {
		return ignored
	}

	var stdin bytes.Buffer
	for _, n := range names {
		stdin.WriteString(n)
		stdin.WriteByte(0)
	}

	out, err := runGit(ctx, dir, stdin.Bytes(), "check-ignore", "--stdin", "-z")
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			// 非 git 目录(fatal, exit>=128)、git 缺失、ctx 取消等:无法判定,
			// 跳过标记而不是把错误往上冒泡(单条 git 子命令容错的既有约定)。
			return ignored
		}
		// exit==1: 合法的"无匹配", out 为空, 落到下面的正常解析。
	}

	for _, n := range strings.Split(strings.TrimRight(out, "\x00"), "\x00") {
		if n != "" {
			ignored[n] = true
		}
	}
	return ignored
}

// gitOutputSafe 跑一条 git 子命令,失败时返回空串而不冒泡 error——单条 git
// 子命令容错的既有约定(呼应 internal/service/chat_svc/git_state.go:23-26):
// 调用方把这当"这个字段/这批数据拿不到",而不是让整个请求失败。
func gitOutputSafe(ctx context.Context, dir string, args ...string) string {
	out, err := runGit(ctx, dir, nil, args...)
	if err != nil {
		return ""
	}
	return out
}

// isInsideWorkTree 判定 dir 是否在某个 git 工作树内。这是唯一让 GitChanges /
// GitBranches 整体降级为 NotARepo 的检查;其余单条 git 子命令失败只让对应
// 数据留空,不影响这个判定(同一容错约定)。
func isInsideWorkTree(ctx context.Context, dir string) bool {
	_, err := runGit(ctx, dir, nil, "rev-parse", "--is-inside-work-tree")
	return err == nil
}

// workspacePrefix 返回 dir 相对仓库根的路径前缀:dir 就是仓库根时为空串,是子
// 目录时形如 "sub/"(git 用 "/" 分隔,与它自己输出的路径同一形状)。
//
// 需要它是因为 `git status --porcelain` 与 `git diff` 的路径恒相对仓库根、且覆盖
// 整个仓库,无视命令是在哪个子目录里跑的;而本包对外的路径契约是"相对 dir"。
// 拿不到前缀(非 git 目录、git 调用失败)时返回空串,退化成"dir 即仓库根"的旧
// 行为,与本包其余单条 git 子命令的容错约定一致。
func workspacePrefix(ctx context.Context, dir string) string {
	return strings.TrimSpace(gitOutputSafe(ctx, dir, "rev-parse", "--show-prefix"))
}

// trimWorkspacePrefix 把仓库根相对路径折成 dir 相对路径;path 不在 dir 子树内时
// 第二个返回值为 false。prefix 为空(dir 即仓库根)时恒原样放行。
func trimWorkspacePrefix(path, prefix string) (string, bool) {
	if prefix == "" {
		return path, true
	}
	return strings.CutPrefix(path, prefix)
}

// isRefName 挡掉不能当 git 位置参数使的值:空串,以及 "-" 开头的值——后者会被
// git 当选项解析(`git merge-base --octopus HEAD` 退出码 0 且返回 HEAD 自己,
// 「本分支档」就静默变成了「对 HEAD 比较」)。基线 ref 是过 wire 进来的调用方
// 输入,这是它进入 argv 前唯一的关口;git 本身也不允许 "-" 开头的引用名,拒掉
// 不会误伤合法分支。
func isRefName(ref string) bool {
	return ref != "" && !strings.HasPrefix(ref, "-")
}

// RefExists 用 `git rev-parse --verify` 校验 dir 仓库内 ref 是否存在。
// 供调用方校验持久化的基线值是否仍然有效(设计决策 9:"若持久化的 ref 已不
// 存在则回落到默认推断"),也被 DefaultBaseline 自身复用。
func RefExists(ctx context.Context, dir, ref string) bool {
	if !isRefName(ref) {
		return false
	}
	_, err := runGit(ctx, dir, nil, "rev-parse", "--verify", ref)
	return err == nil
}

// DefaultBaseline 按 `origin/HEAD` 指向的分支 → `main` → `master` 的顺序推断
// "本分支"档的默认基线,每一步都用 RefExists 校验该 ref 确实存在,返回第一个
// 命中者;三者都不存在时返回空串(设计决策 9)。
func DefaultBaseline(ctx context.Context, dir string) string {
	if out, err := runGit(ctx, dir, nil, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		if short, ok := strings.CutPrefix(strings.TrimSpace(out), "refs/remotes/"); ok && RefExists(ctx, dir, short) {
			return short
		}
	}
	for _, candidate := range [...]string{"main", "master"} {
		if RefExists(ctx, dir, candidate) {
			return candidate
		}
	}
	return ""
}

// mergeBase 求 baseline 与 HEAD 的 merge-base commit,是"本分支档"变动比较
// 的参照点(Git 模式设计:"先求基线与 HEAD 的 merge-base,再以工作区对
// merge-base 做比较")。
func mergeBase(ctx context.Context, dir, baseline string) (string, error) {
	if !isRefName(baseline) {
		return "", errors.New("workspacefs: not a ref name")
	}
	out, err := runGit(ctx, dir, nil, "merge-base", baseline, "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

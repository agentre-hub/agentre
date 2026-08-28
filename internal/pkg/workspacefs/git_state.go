package workspacefs

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
)

// GitStateResult 是 GitState 的返回结果:一份只读 git 状态快照(分支 /
// worktree 短名 / 未提交数 / 领先落后 / common git dir)。dir 不在任何 git
// 工作树内时 NotARepo=true,其余字段恒为零值(与 GitChanges / GitBranches 的
// 容错约定一致)。
type GitStateResult struct {
	NotARepo bool
	// Branch 是当前分支短名;detached HEAD 时为空(不回显 "HEAD" 伪分支名,与
	// currentBranch/GitBranches 同一约定)。
	Branch string
	// Worktree 是 attached worktree 的短名;主 checkout 恒为空。
	Worktree string
	// Dirty 是 `git status --porcelain=v1` 的非空行数(工作区 + 暂存区改动)。
	Dirty int
	// Ahead/Behind 是 HEAD 相对 upstream 的领先/落后提交数;HasUpstream=false
	// 时两者恒为 0(没有 upstream 无法比较,不是"恰好 0 个提交"的真实结果)。
	Ahead       int
	Behind      int
	HasUpstream bool
	// CommonDir 是 `git rev-parse --git-common-dir` 解析出的绝对路径。同一主
	// 仓库的所有 worktree(主 checkout 与每个 attached worktree)共享同一个
	// CommonDir——下游任务(工作根认领,spec 任务 2)据此判定"指回同一主仓库",
	// 而不是去比较各 worktree 互不相同的自身路径。
	CommonDir string
}

// GitState 汇总 dir 的只读 git 状态快照。dir 为空或不在任何 git 工作树内时
// 返回 NotARepo=true 的降级结果,不报错(与 isInsideWorkTree 记录的容错约定
// 一致)。除该判定外,其余单条 git 子命令失败只让对应字段留零值,不影响别的
// 字段(同一容错约定)。
//
// 这段逻辑原是 chat_svc 私有且只认本机会话的 cwd;下沉到这个 host 与 daemon
// 共用的叶子包,是为了让远端 agentred 会话也能拿到与本机同形的快照(daemon
// 侧 handler 调的是同一份实现,spec 硬不变量 5:本地与远端会话行为一致)。
func GitState(ctx context.Context, dir string) GitStateResult {
	if dir == "" || !isInsideWorkTree(ctx, dir) {
		return GitStateResult{NotARepo: true}
	}

	st := GitStateResult{Branch: currentBranch(ctx, dir)}

	gitDir := resolveGitPath(dir, gitOutputSafe(ctx, dir, "rev-parse", "--git-dir"))
	commonDir := resolveGitPath(dir, gitOutputSafe(ctx, dir, "rev-parse", "--git-common-dir"))
	st.CommonDir = commonDir
	if gitDir != "" && commonDir != "" && gitDir != commonDir {
		// gitDir 形如 <common>/worktrees/<name>; 取尾段做短名。
		st.Worktree = filepath.Base(gitDir)
	}

	st.Dirty = countNonEmptyLines(gitOutputSafe(ctx, dir, "status", "--porcelain=v1"))

	if out := gitOutputSafe(ctx, dir, "rev-list", "--left-right", "--count", "@{u}...HEAD"); out != "" {
		parts := strings.Fields(strings.TrimSpace(out))
		if len(parts) == 2 {
			behind, errB := strconv.Atoi(parts[0])
			ahead, errA := strconv.Atoi(parts[1])
			if errB == nil && errA == nil {
				st.Behind, st.Ahead, st.HasUpstream = behind, ahead, true
			}
		}
	}

	return st
}

// resolveGitPath 把 `git rev-parse --git-dir` / `--git-common-dir` 的输出规整
// 成绝对路径:git 在仓库根跑时返回相对路径(如 ".git"),在 worktree / 子目录
// 里跑时可能已经是绝对路径,而绝对路径本身有时已经被 git 解析穿了符号链接
// (例如 macOS 上 /var 是指向 /private/var 的符号链接:git 在 worktree 里直接
// 打印出的绝对 git-dir 已经落在 /private/var 下,而我们自己拼出来的
// "dir/.git" 还留在 /var)。两种输入统一走 EvalSymlinks 再比较,CommonDir 才
// 能在主 checkout 与 worktree 之间按字符串相等判定"同一主仓库";解析失败
// (路径尚不存在等)时退化为未解析符号链接的绝对路径,而不是整体报错。
func resolveGitPath(dir, out string) string {
	p := strings.TrimSpace(out)
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// countNonEmptyLines 数 s 里的非空行,用于把 `git status --porcelain` 的输出
// 折成一个"未提交文件数"。
func countNonEmptyLines(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

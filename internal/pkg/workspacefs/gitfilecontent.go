package workspacefs

import (
	"context"
	"path/filepath"
)

// GitFileContentResult 是 GitFileContent 的返回结果(对比档左列:同一文件在
// git HEAD 的版本,与工作区内容并排比较)。
type GitFileContentResult struct {
	Content  string // HEAD 的 blob 正文;NotARepo 或 !HasHead 时恒为空
	NotARepo bool   // dir 不在任何 git 工作树内;为 true 时其余字段恒为零值
	HasHead  bool   // 文件在 HEAD 存在;为 false 表示空基线(未跟踪/不在 HEAD,对比全部新增)
}

// GitFileContent 返回 dir 下 relPath 所指文件在 git HEAD 的版本,只读
// `git show HEAD:<path>`(git 从对象库取 blob,不触碰工作区文件)。
//   - dir 为空 → ErrNoCwd;relPath 越界(含 ".."、绝对路径、跟随符号链接后逃出
//     dir)→ ErrPathRefused,与 ReadFile 同一套边界。
//   - dir 不在任何 git 工作树内 → NotARepo=true 的降级结果,不报错(与
//     GitChanges / GitBranches 的容错约定一致)。
//   - 文件未跟踪 / 不在 HEAD(仅暂存未提交、工作区已删除但从未提交亦属此列)
//     → HasHead=false 的空基线,不报错;工作区已删除但仍在 HEAD 的文件正常
//     返回 HEAD 版本(对比删除文件正需要它)。
//   - cwd 是仓库子目录时,relPath 经 workspacePrefix 换算成仓库根相对路径再
//     交给 git——git 的 <rev>:<path> 路径恒相对仓库根、无视命令在哪个子目录
//     跑,与 GitChanges 的路径折回同一先例。
//   - 纯读:经 --no-optional-locks 不抢索引锁、不改 git 状态、不写文件、正文
//     不进日志。
func GitFileContent(ctx context.Context, dir, relPath string) (*GitFileContentResult, error) {
	if dir == "" {
		return nil, ErrNoCwd
	}
	path, err := resolveRelPath(dir, relPath)
	if err != nil {
		return nil, err
	}

	// 跟随符号链接后再校验仍落在 dir 之内,与 ReadFile 同一道链接逃逸闸门:
	// relPath 在工作区解析到 cwd 之外的请求一律拒绝,不因 git show 读的是对象
	// 库就跳过。文件在 HEAD 里、工作区已删除(对比档恰要读这种)时 EvalSymlinks
	// 报 ENOENT,此时没有链接可跟随,跳过重校验而不是让删除文件的对比读不出来。
	if _, err := resolveWithinRoot(dir, path, relPath, true); err != nil {
		return nil, err
	}

	if !isInsideWorkTree(ctx, dir) {
		return &GitFileContentResult{NotARepo: true}, nil
	}

	// relPath 相对 dir;git 的 <rev>:<path> 路径恒相对仓库根,先经 workspacePrefix
	// 折成仓库根相对路径。前缀是 git 自己输出的 "/" 分隔形如 "sub/",relPath 契约
	// 也是 "/" 分隔,直接拼接。
	repoPath := workspacePrefix(ctx, dir) + filepath.ToSlash(relPath)
	out, err := runGit(ctx, dir, nil, "show", "HEAD:"+repoPath)
	if err != nil {
		// 未跟踪 / 不在 HEAD(或路径是目录、HEAD 未生):git 拿不到 blob → 空基线,
		// 不报错(单条 git 子命令容错的既有约定,同 git_changes.go 的 mergeBase
		// 退化)。
		return &GitFileContentResult{}, nil //nolint:nilerr // git 报错即"不在 HEAD",正是空基线的正常输入,不是失败
	}
	return &GitFileContentResult{Content: out, HasHead: true}, nil
}

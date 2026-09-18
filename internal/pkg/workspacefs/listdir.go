package workspacefs

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/pathdepth"
)

// ListDir 列出 root 下 relPath 所指目录的一层内容。
//   - root 是会话已解析的绝对工作目录,是路径越界校验的信任边界。
//   - relPath 相对 root;空串表示 root 本身。含 ".."、绝对路径,或解析后
//     逃出 root 的请求一律拒绝为 ErrPathRefused(路径约束,与
//     internal/pkg/remotefs/pathguard 的取舍一致)。
//   - includeIgnored 为 false 时,被 git 忽略的条目从结果中剔除;为 true
//     时保留并在 Entry.GitIgnored 上打标记。".git" 条目恒不出现,不受
//     includeIgnored 影响。
//   - 结果条目数超过 maxEntries 时截断并设 Truncated=true。
func ListDir(ctx context.Context, root, relPath string, includeIgnored bool, maxEntries int) (*ListDirResult, error) {
	dir, err := resolveRelPath(root, relPath)
	if err != nil {
		return nil, err
	}

	dirents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("workspacefs: read dir: %w", err)
	}

	names := make([]string, 0, len(dirents))
	for _, d := range dirents {
		if d.Name() == ".git" {
			continue
		}
		names = append(names, d.Name())
	}
	ignored := gitIgnoredSet(ctx, dir, names)

	entries := make([]Entry, 0, len(names))
	for _, d := range dirents {
		name := d.Name()
		if name == ".git" {
			continue
		}
		info, lerr := os.Lstat(filepath.Join(dir, name))
		if lerr != nil {
			// 单个 entry 出错(并发删除等),跳过不致命,呼应
			// internal/daemon/remotefs 既有的单条目容错约定。
			continue
		}
		isIgnored := ignored[name]
		if isIgnored && !includeIgnored {
			continue
		}
		entries = append(entries, Entry{
			Name:       name,
			IsDir:      info.IsDir(),
			Size:       sizeOrZero(info),
			ModTime:    info.ModTime(),
			Symlink:    info.Mode()&os.ModeSymlink != 0,
			GitIgnored: isIgnored,
		})
	}

	entries, truncated := truncateEntries(entries, maxEntries)

	return &ListDirResult{
		Path:      dir,
		Entries:   entries,
		Truncated: truncated,
	}, nil
}

func sizeOrZero(info fs.FileInfo) int64 {
	if info.IsDir() {
		return 0
	}
	return info.Size()
}

// resolveRelPath 把 relPath(相对 root,"/" 分隔)解析为绝对路径,并保证结果
// 仍在 root 之内。算法与 internal/pkg/remotefs/pathguard.ResolvePath 同源但
// 方向相反:pathguard 要求 raw 必须绝对,这里要求 relPath 必须相对且解析后
// 不逃出 root。
//
// 判定是**字面**的,不解析符号链接:root 里指向外面的目录链接展开后确实会列到
// root 之外。这是刻意的——工作目录里 symlink 进 vendor / 共享目录是常规用法,
// 而这一层的信任边界只是"别让 relPath 自己拼出 ..",不是沙箱(条目本身带
// Entry.Symlink 标记)。真正需要防的"跟随链接读文件内容"由
// countUntrackedFileLines 的常规文件闸门挡住。
func resolveRelPath(root, relPath string) (string, error) {
	if relPath == "" {
		return root, nil
	}
	if filepath.IsAbs(relPath) || strings.HasPrefix(relPath, "/") {
		return "", ErrPathRefused
	}

	// 用栈深度模拟路径遍历:任何 ".." 把深度打到负数即视为越界(在 Clean/Join
	// 之前检查,Clean 会静默吞掉这些残留)。判据与 remotefs 的绝对路径规范化同源。
	if pathdepth.EscapesRoot(relPath) {
		return "", ErrPathRefused
	}

	resolved := filepath.Join(root, filepath.FromSlash(relPath))
	// 深度检查已经保证不越界,这里再做一次前缀校验兜底(防御性冗余,呼应
	// 路径约束"解析后必须仍在 cwd 之内"这句话本身)。
	if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return "", ErrPathRefused
	}
	return resolved, nil
}

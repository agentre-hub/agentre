package workspacefs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// maxUntrackedTextSize 是未跟踪文件被判定为二进制前允许读取计行的字节上限
// (Git 模式设计决策 10:">1MiB ... 判为二进制")。
const maxUntrackedTextSize = 1 << 20 // 1 MiB

// GitChanges 返回 dir 的 git 变动列表(Git 模式)。
//
// dir 不在任何 git 工作树内时返回 NotARepo=true 的降级结果,不报错——与
// isInsideWorkTree 记录的容错约定一致。
//
// scope==ScopeUncommitted("未提交档"):以 `git status --porcelain=v1 -z`
// 取路径与状态,`git diff --numstat -z HEAD` 取已跟踪文件的 +/− 行数。
//
// status 带 -uall:默认的 -unormal 会把一个全新目录折成单条 "newdir/" 记录,
// 既看不到里面的文件、也算不出行数(而 agent 新建的文件恰恰是最需要看行数的,
// 设计决策 10),扁平行模型的 basename 还会变成空串。被忽略的条目不受影响,
// 仍然不出现在 status 输出里。
//
// scope==ScopeBranch("本分支档"):baseline 必须非空,调用方先用
// DefaultBaseline 或持久化值算出(baseline 为空返回 ErrBaselineRequired,
// 这是调用方的编程错误而非 git 故障,不走单条子命令容错的静默降级)。以
// baseline 与 HEAD 的 merge-base 为参照,`git diff --name-status -z` 取状态、
// `git diff --numstat -z` 取行数,已提交与未提交的改动一并计入。若 baseline
// 存在但求不出 merge-base(例如两条不相关历史),按既有容错约定退化为空
// 变动而不是报错。
//
// 两档共用:未跟踪文件恒来自 status --porcelain 的 "??" 条目(diff 系命令
// 看不到未跟踪文件),新增行数由 Go 侧读文件计行得出,>1MiB 或含 NUL 字节
// 判为二进制;变动数超过 maxEntries 时截断。
//
// dir 可以只是仓库里的一个子目录(项目路径指到 monorepo 的 frontend/ 之类)。
// git 两条命令的路径恒相对仓库根、且覆盖整个仓库,所以结果先经 workspacePrefix
// 折回 dir 相对路径,子树之外的变动不列 —— Change.Path 的契约是"相对 dir",
// 本包自己拿它读文件、前端拿它拼 cwd 打开文件,都只有这一种解释。
func GitChanges(ctx context.Context, dir string, scope GitChangesScope, baseline string, maxEntries int) (*GitChangesResult, error) {
	if !isInsideWorkTree(ctx, dir) {
		return &GitChangesResult{NotARepo: true}, nil
	}
	if scope == ScopeBranch && baseline == "" {
		return nil, ErrBaselineRequired
	}

	porcelain := parsePorcelainEntries(gitOutputSafe(ctx, dir, "status", "--porcelain=v1", "-z", "-uall"))

	statuses := make(map[string]changeMeta, len(porcelain))
	var untrackedPaths []string
	for _, e := range porcelain {
		if e.xy == "??" {
			untrackedPaths = append(untrackedPaths, e.path)
			continue
		}
		if scope == ScopeUncommitted {
			statuses[e.path] = changeMeta{status: classifyPorcelainXY(e.xy), oldPath: e.oldPath}
		}
	}

	diffRef := "HEAD"
	if scope == ScopeBranch {
		mb, err := mergeBase(ctx, dir, baseline)
		if err != nil {
			// baseline 存在但求不出 merge-base:单条子命令失败不冒泡,退化为
			// 空变动(容错约定,呼应 git_state.go 的既有取舍)。
			return &GitChangesResult{}, nil //nolint:nilerr // 见上方注释
		}
		diffRef = mb
		statuses = parseNameStatusEntries(gitOutputSafe(ctx, dir, "diff", "--name-status", "-z", diffRef))
	}

	numstat := parseNumstat(gitOutputSafe(ctx, dir, "diff", "--numstat", "-z", diffRef))
	prefix := workspacePrefix(ctx, dir)

	changes := make([]Change, 0, len(statuses)+len(untrackedPaths))
	for path, meta := range statuses {
		rel, inside := trimWorkspacePrefix(path, prefix)
		if !inside {
			continue
		}
		// 旧路径可能落在工作目录之外(从子树外面重命名进来);它只进 tooltip,
		// 不参与任何路径拼接,折不回来时原样保留仓库根相对值。
		oldPath := meta.oldPath
		if oldRel, ok := trimWorkspacePrefix(oldPath, prefix); ok {
			oldPath = oldRel
		}
		c := Change{Path: rel, OldPath: oldPath, Status: meta.status}
		if ns, ok := numstat[path]; ok {
			c.Added, c.Deleted, c.Binary = ns.added, ns.deleted, ns.binary
		}
		changes = append(changes, c)
	}
	for _, p := range untrackedPaths {
		rel, inside := trimWorkspacePrefix(p, prefix)
		if !inside {
			continue
		}
		added, binary := countUntrackedFileLines(filepath.Join(dir, rel))
		changes = append(changes, Change{Path: rel, Status: ChangeUntracked, Added: added, Binary: binary})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })

	changes, truncated := truncateEntries(changes, maxEntries)

	return &GitChangesResult{Changes: changes, Truncated: truncated}, nil
}

// GitBranches 列出 dir 仓库的本地分支与远程跟踪分支,供"本分支档"的基线选择
// 使用。dir 不在任何 git 工作树内时返回 NotARepo=true。
func GitBranches(ctx context.Context, dir string) (*GitBranchesResult, error) {
	if !isInsideWorkTree(ctx, dir) {
		return &GitBranchesResult{NotARepo: true}, nil
	}
	local := refBranches(ctx, dir, "refs/heads/", false)
	remote := refBranches(ctx, dir, "refs/remotes/", true)
	branches := make([]Branch, 0, len(local)+len(remote))
	branches = append(branches, local...)
	branches = append(branches, remote...)
	sort.Slice(branches, func(i, j int) bool { return branches[i].Name < branches[j].Name })
	return &GitBranchesResult{
		Branches:        branches,
		CurrentBranch:   currentBranch(ctx, dir),
		DefaultBaseline: DefaultBaseline(ctx, dir),
	}, nil
}

// currentBranch 取当前分支短名。用 symbolic-ref 而非 rev-parse --abbrev-ref:
// detached HEAD 下前者失败(经 gitOutputSafe 落成空串),后者会返回字符串
// "HEAD",那不是一个分支名。
func currentBranch(ctx context.Context, dir string) string {
	return strings.TrimSpace(gitOutputSafe(ctx, dir, "symbolic-ref", "--short", "HEAD"))
}

// refBranches 列举 prefix 下的分支短名。remote==true 时跳过 "<remote>/HEAD"
// 这个符号指针(它指向默认分支,不是一个真实分支,不应出现在选择器里)。
func refBranches(ctx context.Context, dir, prefix string, remote bool) []Branch {
	out := gitOutputSafe(ctx, dir, "for-each-ref", "--format=%(refname)", prefix)
	var branches []Branch
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name := strings.TrimPrefix(line, prefix)
		if remote && strings.HasSuffix(name, "/HEAD") {
			continue
		}
		branches = append(branches, Branch{Name: name, Remote: remote})
	}
	return branches
}

// changeMeta 是解析 git 输出得到的、Change 里除行数以外的部分,用路径做 join
// key 去接 numstat 的行数。
type changeMeta struct {
	status  ChangeStatus
	oldPath string
}

// porcelainEntry 是 `git status --porcelain=v1 -z` 里的一条记录。
type porcelainEntry struct {
	xy      string // 两字符状态码,如 " M"、"??"、"R "
	path    string // 当前路径
	oldPath string // 仅 rename/copy(xy[0] 为 'R'/'C')非空
}

// parsePorcelainEntries 解析 `git status --porcelain=v1 -z` 的 NUL 分隔输出。
// rename/copy 记录在 porcelain 里的顺序是"新路径的 XY 行"在前、"旧路径"作为
// 紧随其后的独立 token——与 diff 系命令(旧路径在前)相反。
func parsePorcelainEntries(out string) []porcelainEntry {
	tokens := splitNulTokens(out)
	entries := make([]porcelainEntry, 0, len(tokens))
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if len(tok) < 3 {
			continue // 防御性:格式不完整的 token 跳过,不让它污染后续记录
		}
		xy, path := tok[:2], tok[3:]
		e := porcelainEntry{xy: xy, path: path}
		if xy[0] == 'R' || xy[0] == 'C' {
			i++
			if i < len(tokens) {
				e.oldPath = tokens[i]
			}
		}
		entries = append(entries, e)
	}
	return entries
}

// classifyPorcelainXY 把 porcelain 的两字符状态码归到五类状态之一。
// 优先级 重命名 > 新增 > 删除 > 修改;"??" 由调用方单独按未跟踪处理,不
// 经过这里。
func classifyPorcelainXY(xy string) ChangeStatus {
	x, y := xy[0], xy[1]
	switch {
	case x == 'R' || y == 'R' || x == 'C' || y == 'C':
		return ChangeRenamed
	case x == 'A' || y == 'A':
		return ChangeAdded
	case x == 'D' || y == 'D':
		return ChangeDeleted
	default:
		return ChangeModified
	}
}

// parseNameStatusEntries 解析 `git diff --name-status -z <ref>` 的 NUL 分隔
// 输出,用于"本分支档"。与 porcelain 相反,rename 记录里旧路径在前、新路径
// 在后。返回以新路径(当前路径)为 key 的 map。
func parseNameStatusEntries(out string) map[string]changeMeta {
	tokens := splitNulTokens(out)
	result := make(map[string]changeMeta, len(tokens))
	for i := 0; i < len(tokens); i++ {
		code := tokens[i]
		status := classifyNameStatusCode(code)
		if status == ChangeRenamed {
			i++
			oldPath := ""
			if i < len(tokens) {
				oldPath = tokens[i]
			}
			i++
			newPath := ""
			if i < len(tokens) {
				newPath = tokens[i]
			}
			result[newPath] = changeMeta{status: status, oldPath: oldPath}
			continue
		}
		i++
		path := ""
		if i < len(tokens) {
			path = tokens[i]
		}
		result[path] = changeMeta{status: status}
	}
	return result
}

// classifyNameStatusCode 把 `--name-status` 的状态码(如 "M"、"A"、"D"、
// "R100")归到五类状态之一(未跟踪不会出现在 diff 输出里,不在此列)。
func classifyNameStatusCode(code string) ChangeStatus {
	switch {
	case strings.HasPrefix(code, "R"), strings.HasPrefix(code, "C"):
		return ChangeRenamed
	case code == "A":
		return ChangeAdded
	case code == "D":
		return ChangeDeleted
	default:
		return ChangeModified
	}
}

// numstatEntry 是 `git diff --numstat -z` 里一条记录的行数部分。
type numstatEntry struct {
	added, deleted int
	binary         bool
}

// parseNumstat 解析 `git diff --numstat -z <ref>` 的 NUL 分隔输出。二进制
// 文件的行数字段是 "-"(git 自己给出的信号,不是 Go 侧推断),据此打
// Binary 标记。rename 记录的路径字段为空,紧随其后是旧路径、新路径两个
// token(与 name-status 同序),取新路径做 key 以对齐 parseNameStatusEntries
// 的 join key。
func parseNumstat(out string) map[string]numstatEntry {
	tokens := splitNulTokens(out)
	result := make(map[string]numstatEntry, len(tokens))
	for i := 0; i < len(tokens); i++ {
		fields := strings.SplitN(tokens[i], "\t", 3)
		if len(fields) != 3 {
			continue
		}
		addedStr, deletedStr, path := fields[0], fields[1], fields[2]
		var ne numstatEntry
		if addedStr == "-" || deletedStr == "-" {
			ne.binary = true
		} else {
			ne.added, _ = strconv.Atoi(addedStr)
			ne.deleted, _ = strconv.Atoi(deletedStr)
		}
		if path == "" {
			i++ // 旧路径,join key 用不到,跳过
			i++
			if i < len(tokens) {
				path = tokens[i]
			}
		}
		result[path] = ne
	}
	return result
}

// splitNulTokens 把 NUL 分隔的 git 输出拆成 token 列表,丢弃尾部因结尾 NUL
// 产生的空 token。
func splitNulTokens(out string) []string {
	if out == "" {
		return nil
	}
	parts := strings.Split(out, "\x00")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// countUntrackedFileLines 为未跟踪文件计行(Git 模式设计决策 10)。
// >1MiB 的文件在 stat 阶段就判为二进制,不读取全文件内容;≤1MiB 的文件读入
// 后按是否含 NUL 字节判定二进制。文件在检查途中消失等意外一律降级为
// "拿不到行数",不报错(与本包其余 git 子命令的容错约定一致)。
//
// 用 Lstat 且只对常规文件计行:git 会把未跟踪的符号链接也列成一条 "??" 记录,
// 跟随它读取既会把工作目录之外那个文件的行数带进面板,也可能撞上 /dev/zero 这
// 类字符设备 —— 跟随后 Size 为 0,过得了上面那道 1MiB 闸门,而读取永远不会
// 结束。非常规文件一律按"拿不到行数"处理。
func countUntrackedFileLines(path string) (added int, binary bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, false
	}
	if !info.Mode().IsRegular() {
		return 0, false
	}
	if info.Size() > maxUntrackedTextSize {
		return 0, true
	}
	//nolint:gosec // G304: path 由 dir 与 git status 报告的仓库内路径拼接而来,不是外部不可信输入
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return 0, true
	}
	return countLines(content), false
}

// countLines 数 content 的行数:末尾没有换行符的最后一段也算一行,空文件为
// 0 行。
func countLines(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	n := bytes.Count(content, []byte{'\n'})
	if content[len(content)-1] != '\n' {
		n++
	}
	return n
}

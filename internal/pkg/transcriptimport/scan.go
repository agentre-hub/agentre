package transcriptimport

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 本文件是三个磁盘读取器共用的 JSONL 扫描脚手架。分工与 Filter.Matches 一致:
// **与后端无关的判据住在契约这一侧**,方言(diskRecord 怎么解、一条记录算不算
// 一轮的起点)留在各自的 runtime 包里。
//
// 为什么不各写一份:各抄一份的差异不会报错,只会让某一家少几行候选、少一段缺口、
// 或者少一道防护 —— codex 的 resolveLocator 副本就曾漏掉另外两家都有的
// root == "" 防护(其调用方另行检查,因此没构成缺陷,但那正是这种形状孕育的差异)。

const (
	// ScanHeadLines 是扫描期允许读的文件头行数上限。候选行只需要会话 id / cwd /
	// 来源标记 / 首条用户消息,它们都在开头几十行内;超过就放弃标题,不解全文。
	ScanHeadLines = 200
	// ScanTailBytes 是扫描期回读的文件尾字节数,用来取最后一条记录的时间。
	ScanTailBytes = 64 << 10
	// MaxRecordBytes 是单行上限,与 pkg/claudecode 的帧上限同量级(内联的
	// tool_result 可能很大)。按 bufio 默认的 64KiB 上限,长行会让扫描直接停在
	// 那里 —— 表现是转录从中间被截断而不报错。
	MaxRecordBytes = 16 << 20
)

// 定位符解析的三种拒绝理由。各读取器按自己的包名包一层再返回,消费方只判非 nil;
// 声明成哨兵是为了让守卫测试断言的是**理由**,而不是某一句中文。
var (
	// ErrLocatorRootUnavailable 转录根目录取不到(拿不到 HOME、环境变量为空)。
	ErrLocatorRootUnavailable = errors.New("transcript root unavailable")
	// ErrLocatorInvalid 定位符本身不合法(空、当前目录、绝对路径)。
	ErrLocatorInvalid = errors.New("invalid transcript locator")
	// ErrLocatorEscapes 定位符还原之后落在根目录之外。
	ErrLocatorEscapes = errors.New("transcript locator escapes its root")
)

// ResolveLocator 把根内相对定位符还原成绝对路径,并挡住越出根目录的路径。
//
// 定位符来自前端,不可信:这是三个读取器共同的**安全边界**,所以判据只留一处。
// root == "" 必须先于任何路径运算拒绝 —— 否则 filepath.Join 会把定位符还原成
// 进程当前目录下的路径,"在不在根内"这个判断随之失效。
func ResolveLocator(root string, loc Locator) (string, error) {
	if root == "" {
		return "", ErrLocatorRootUnavailable
	}
	rel := filepath.Clean(filepath.FromSlash(string(loc)))
	if rel == "" || rel == "." || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: %q", ErrLocatorInvalid, loc)
	}
	abs := filepath.Join(root, rel)
	inside, err := filepath.Rel(root, abs)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", ErrLocatorEscapes, loc)
	}
	return abs, nil
}

// NewRecordScanner 按记录(一行一条)扫描一份转录,单行上限见 MaxRecordBytes。
func NewRecordScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), MaxRecordBytes)
	return sc
}

// FirstLine 取一段正文的首行并去掉两端空白 —— 候选标题的口径。
func FirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// LastRecordTime 回读文件尾 ScanTailBytes,取最后一条带时间戳的记录的时间。
// 拿不到就退回 fallback(通常是首条记录的时间),不谎报"现在"。
//
// recordTime 由调用方给:一条记录的时间戳在哪个字段是方言,不在这一侧。
func LastRecordTime(f *os.File, fallback time.Time, recordTime func(line []byte) time.Time) time.Time {
	st, err := f.Stat()
	if err != nil {
		return fallback
	}
	size := st.Size()
	start := size - ScanTailBytes
	if start < 0 {
		start = 0
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil {
		return fallback
	}
	lines := strings.Split(string(buf), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if ts := recordTime([]byte(lines[i])); !ts.IsZero() {
			return ts
		}
	}
	return fallback
}

// BuildGaps 按固定次序声明缺口,计数为 0 的种类不出现 —— UI 拿它渲染导入前的提示
// 与转录内的灰字说明,出一条 Count=0 的缺口等于凭空吓人一跳。
//
// 结构上不可能出现某种缺口的后端(pi 的子代理内部过程内联在 details.messages 里,
// 不存在"子文件缺失"这回事)传 0 即可。
func BuildGaps(emptyThinking, badLines, unclosed, missingSubagents int) []Gap {
	var gaps []Gap
	add := func(kind GapKind, n int) {
		if n > 0 {
			gaps = append(gaps, Gap{Kind: kind, Count: n})
		}
	}
	add(GapThinkingUnavailable, emptyThinking)
	add(GapSubagentInternals, missingSubagents)
	add(GapUnclosedToolCall, unclosed)
	add(GapUnparsableRecords, badLines)
	return gaps
}

// WalkChain 从叶子沿父指针回溯,返回正序的链;被抛弃的分支(fork / 回退 / 编辑
// 重发留下的旧记录)因此一条都不在链上。环(理论上不该有)靠 seen 挡住。
//
// 节点类型由调用方给:索引期留下的骨架是各家自己的形状,这里只认"父指针"。
func WalkChain[T any](nodes map[string]T, leaf string, parentOf func(T) string) []string {
	var chain []string
	seen := map[string]struct{}{}
	for cur := leaf; cur != ""; {
		node, ok := nodes[cur]
		if !ok {
			break
		}
		if _, dup := seen[cur]; dup {
			break
		}
		seen[cur] = struct{}{}
		chain = append(chain, cur)
		cur = parentOf(node)
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

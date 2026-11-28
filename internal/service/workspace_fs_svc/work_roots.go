package workspace_fs_svc

import (
	"context"
	"path"
	"path/filepath"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// 工作根:本会话被允许列举 / 搜索 / 读取的目录集合。
//
// 信任边界是"已认领的工作根集合"而不只是会话 cwd(spec 硬不变量 2):AI 常
// 在同一个主仓库的另一个 worktree 里干活。
// 集合之外仍然一律拒绝——边界只是变宽了一格,没有放松。
//
// 认领两个条件缺一不可:
//  1. 候选路径(AI 当前位置、写入目标、`git worktree add` 的路径、用户点击的路径,
//     见 Claim;写入路径另由取工作根时补喂)属于一个尚未认领的工作树(否则它本来
//     就在集合里,不是新根);
//  2. 该路径解析出的 git 公共目录(GitState.CommonDir)与会话 cwd 的相同,
//     即它指回同一个主仓库。
//
// 只满足条件 1 的 /tmp/patch.diff 不是工作根 —— 少了条件 2,任何被 AI 写过的
// 路径都会变成一个假的工作根,那等于取消了边界本身。
//
// 条件 1 按**工作树身份**判,不按路径字符串前缀判。同一主仓库下,(CommonDir,
// worktree 短名)唯一确定一个工作树:CommonDir 由叶子包在执行设备上解析成真实
// 路径,短名由 git 在同一 CommonDir 内保证唯一。于是
//   - 建在主 checkout 目录树里的 worktree(<repo>/.claude/worktrees/x、
//     <repo>/.worktrees/x)字符串上落在主根之内,身份却是另一个工作树 → 认领;
//   - 同一个目录换一种符号链接写法(/tmp 与 /private/tmp)身份不变 → 不重复认领。
// git 判定失败(远端掉线、git 报错)的候选不认领、也不让整个取数失败。
//
// 判定所需的 git 事实一律经 gitStateAt 取,本机走叶子包、远端走
// workspacefs.gitState RPC(daemon 侧是同一份叶子实现),因此本地会话与远端
// agentred 会话认领出的集合形状一致(硬不变量 5)。认领结果持久化、git 判定按
// 会话缓存,见 claim.go。

// WrittenPath 是 AI 的一次写入:Path 是工具调用里的原始路径(可能是相对路径),
// Dir 是该调用执行时 AI 所在的目录(没有记录时为空串)。相对路径按 Dir 补全,
// Dir 为空时回落会话 cwd。
type WrittenPath struct {
	Path string
	Dir  string
}

// SessionWrittenPathsResolver 返回本会话 AI 写过的文件路径(按首次出现顺序)。
//
// 这是本服务**自己**声明的第二个窄接口(ISP + DIP),实现方同样是 chat_svc ——
// "哪些路径被写过"只有持有会话消息的那一侧答得出;本服务因此不必跨域读 chat
// 表,单测也只注入一个闭包。注入模式与 SessionWorkspaceResolver 一致。
type SessionWrittenPathsResolver func(ctx context.Context, sessionID int64) ([]WrittenPath, error)

var resolveWrittenPathsFn SessionWrittenPathsResolver

// RegisterSessionWrittenPaths 由 bootstrap 注入 chat_svc 的实现。未注入时
// 认领集合退化成"只有会话 cwd",即今天的行为——安全方向的降级。
func RegisterSessionWrittenPaths(fn SessionWrittenPathsResolver) { resolveWrittenPathsFn = fn }

// maxRootAscent 是"从被写文件向上找工作树根"的层数上限。向上每走一层都是一次
// git 调用(远端是一跳 RPC),给它一个上限,免得畸形路径把一次侧栏取数拖成
// 几十跳。真实仓库里被写文件离工作树根远不到这个深度。
const maxRootAscent = 32

// WorkRootView 是一个已认领的工作根。
//
//   - Path 是这个根的绝对路径(远端会话是那台机器上的路径);
//   - Name 是它的显示名(路径末段);
//   - IsWorktree 区分"主 checkout"与"attached worktree"(spec 里根切换器要
//     显示的"它是主仓库还是 worktree");
//   - IsPrimary 标记会话 cwd 对应的那个根 —— 它恒是第一项,也是根消失时的
//     回落目标。IsPrimary 与 IsWorktree 是两件事:会话 cwd 本身也可能是一个
//     worktree。
type WorkRootView struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	IsWorktree bool   `json:"isWorktree"`
	IsPrimary  bool   `json:"isPrimary"`
	// AIHere 标记 AI 当前位置所在的那个根(最多一个;位置不在任何根内时一个都不标)。
	AIHere bool `json:"aiHere"`
	// Removed 只出现在 RemovedWorkRoots 交回的项上:这个根已从认领集合中消失。
	Removed bool `json:"removed,omitempty"`
}

// SessionAILocationResolver 返回本会话的 AI 当前位置:执行设备上的一个绝对目录,空串 =
// 没有位置记录(即会话 cwd)。实现方是 chat_svc —— 位置由工具事件随到随更,只有持有
// 事件流的那一侧知道。注入模式与 SessionWrittenPathsResolver 一致。
type SessionAILocationResolver func(ctx context.Context, sessionID int64) (string, error)

var resolveAILocationFn SessionAILocationResolver

// RegisterSessionAILocation 由 bootstrap 注入 chat_svc 的实现。未注入时按「没有位置
// 记录」处理:AI 在主根。
func RegisterSessionAILocation(fn SessionAILocationResolver) { resolveAILocationFn = fn }

// AIRootIndex 给出目录 dir 属于 roots 里的哪个根:包含它的**最深**的根(嵌在主 checkout
// 里的 worktree 优先于主根),按路径段比较;dir 为空串(没有位置记录)即主根。不属于任何
// 根时返回 -1。WorkRoots 据它标 AIHere,chat_svc 据它判「AI 的根变没变」—— 两处同一口径。
func AIRootIndex(roots []WorkRootView, dir string) int {
	if dir == "" {
		for i, r := range roots {
			if r.IsPrimary {
				return i
			}
		}
		return -1
	}
	dir = cleanPath(dir)
	best, bestLen := -1, -1
	for i, r := range roots {
		root := cleanPath(r.Path)
		if isWithin(root, dir) && len(root) > bestLen {
			best, bestLen = i, len(root)
		}
	}
	return best
}

// WorkRoots 返回本会话已认领的工作根集合,第一项恒是会话 cwd 对应的根。
func (s *workspaceFsImpl) WorkRoots(ctx context.Context, sessionID int64) ([]WorkRootView, error) {
	deviceID, cwd, err := s.workspace(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	roots, err := s.claimedRoots(ctx, sessionID, deviceID, cwd)
	if err != nil {
		return nil, err
	}
	s.markAIHere(ctx, sessionID, roots)
	return roots, nil
}

func (s *workspaceFsImpl) RefreshWorkRoots(ctx context.Context, sessionID int64) ([]WorkRootView, error) {
	deviceID, cwd, err := s.workspace(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	s.stateFor(sessionID, deviceID, cwd).markClaimedUnverified()
	return s.WorkRoots(ctx, sessionID)
}

// markAIHere 在 roots(已是副本)上标出 AI 当前所在的根。位置取不到时一个都不标:
// 标错一个根会让前端的「跟随 AI」切到错的 checkout,不标只是少一个提示。
func (s *workspaceFsImpl) markAIHere(ctx context.Context, sessionID int64, roots []WorkRootView) {
	fn := s.aiLocation
	if fn == nil {
		fn = resolveAILocationFn
	}
	dir := ""
	if fn != nil {
		d, err := fn(ctx, sessionID)
		if err != nil {
			logger.Ctx(ctx).Warn("workspace_fs_svc.WorkRoots: ai location unavailable, no root marked",
				zap.Int64("sessionID", sessionID), zap.Error(err))
			return
		}
		dir = d
	}
	if i := AIRootIndex(roots, dir); i >= 0 {
		roots[i].AIHere = true
	}
}

// rootFor 解析出这次调用实际使用的 {deviceID, root}:root 为空串即会话 cwd;
// 非空时必须命中已认领的工作根集合,否则 WorkspaceFsPathRefused —— 这是硬
// 不变量 2 的闸门,放在 host 侧是因为"哪些根被认领"是会话事实,daemon 那头
// 只认请求里的 Root(它继续按同一份叶子实现强制 relPath 不逃出 Root)。
//
// 命中判定是**精确相等**而不是"落在某个根的子树里":root 参数的合法取值就是
// WorkRoots 给出的那几个,任何拼出来的下级目录都该以 relPath 的形式表达,
// 走叶子包那道越界校验。
func (s *workspaceFsImpl) rootFor(ctx context.Context, sessionID int64, root string) (int64, string, error) {
	deviceID, cwd, err := s.workspace(ctx, sessionID)
	if err != nil {
		return 0, "", err
	}
	if root == "" {
		return deviceID, cwd, nil
	}
	cleaned := cleanPath(root)
	if cleaned == cleanPath(cwd) {
		return deviceID, cwd, nil
	}
	// 先按已知集合放行:读文件 / 列目录不该每次都重扫会话写过的全部路径。不在已知集合里
	// 时才补喂一遍写入路径再判(刚写进一个尚未认领的 worktree)。
	roots, err := s.knownRoots(ctx, sessionID, deviceID, cwd)
	if err != nil {
		return 0, "", err
	}
	if p, ok := matchRoot(roots, cleaned); ok {
		return deviceID, p, nil
	}
	roots, err = s.claimedRoots(ctx, sessionID, deviceID, cwd)
	if err != nil {
		return 0, "", err
	}
	if p, ok := matchRoot(roots, cleaned); ok {
		return deviceID, p, nil
	}
	// 越界是可见的:被拒的具体路径不进日志(与 ErrPathRefused 不回显原因同一
	// 取舍,避免成为路径探测信道),只记会话与已认领根的个数。
	logger.Ctx(ctx).Warn("workspace_fs_svc.rootFor: root outside claimed work roots",
		zap.Int64("sessionID", sessionID), zap.Int64("deviceID", deviceID),
		zap.Int("claimedCount", len(roots)))
	return 0, "", i18n.NewError(ctx, code.WorkspaceFsPathRefused)
}

// matchRoot 在集合里找与 cleaned(已 cleanPath)相同的根,交回集合里的写法。
func matchRoot(roots []WorkRootView, cleaned string) (string, bool) {
	for _, r := range roots {
		if cleanPath(r.Path) == cleaned {
			return r.Path, true
		}
	}
	return "", false
}

// anchorWrittenPath 把一次写入补全成绝对路径:绝对路径原样(清理后)返回;相对
// 路径按该调用所在目录 Dir 补全,Dir 缺失(或本身不是绝对路径)时回落会话 cwd。
// 路径为空时 ok=false。
func anchorWrittenPath(w WrittenPath, cwd string) (string, bool) {
	if w.Path == "" {
		return "", false
	}
	if isAbsPath(w.Path) {
		return cleanPath(w.Path), true
	}
	base := w.Dir
	if !isAbsPath(base) {
		base = cwd
	}
	return joinPath(base, w.Path), true
}

// WrittenFile 给出一次写入的文件的绝对路径:按 anchorWrittenPath 的规则补全(相对路径按
// w.Dir,缺失时按 fallback)。补全不出绝对路径时 ok=false。
func WrittenFile(w WrittenPath, fallback string) (string, bool) {
	p, ok := anchorWrittenPath(w, fallback)
	if !ok || !isAbsPath(p) {
		return "", false
	}
	return p, true
}

// WrittenDir 给出一次写入所在的目录(认领候选):WrittenFile 的上一级。取工作根时补喂写入
// 路径与 chat_svc 实时认领写入目标走这同一个口径。
func WrittenDir(w WrittenPath, fallback string) (string, bool) {
	p, ok := WrittenFile(w, fallback)
	if !ok {
		return "", false
	}
	return dirOf(p), true
}

// SamePath 报告两条执行设备上的路径清理后是否相同。
func SamePath(a, b string) bool { return cleanPath(a) == cleanPath(b) }

// RelativeTo 把 p 显示成相对 root 的路径;p 不在 root 之内时原样交回。
func RelativeTo(root, p string) string {
	r, c := cleanPath(root), cleanPath(p)
	if r == c || !isWithin(r, c) {
		return p
	}
	return strings.TrimLeft(c[len(strings.TrimRight(r, `/\`)):], `/\`)
}

// CleanLocation 把一条位置信号(执行设备上的目录)清理成规范写法;不是绝对路径时
// ok=false —— 没有锚点的位置不可信,不参与位置更新与认领。远端路径按那台机器的规则
// 切(见下方「路径工具」)。
func CleanLocation(p string) (string, bool) {
	if !isAbsPath(p) {
		return "", false
	}
	return cleanPath(p), true
}

// logClaimGitFailure 记录一次被跳过的认领判定。具体路径不进日志(与 rootFor 越界
// 不回显路径同一取舍)。
func logClaimGitFailure(ctx context.Context, deviceID int64, err error) {
	logger.Ctx(ctx).Warn("workspace_fs_svc.claimRoot: git state failed, candidate not claimed",
		zap.Int64("deviceID", deviceID), zap.Error(err))
}

// writtenPathsOf 取本会话 AI 写过的路径;端口未注入时按"没写过"处理(集合退化
// 成只有 cwd,是安全方向的降级)。
func (s *workspaceFsImpl) writtenPathsOf(ctx context.Context, sessionID int64) ([]WrittenPath, error) {
	fn := s.writtenPaths
	if fn == nil {
		fn = resolveWrittenPathsFn
	}
	if fn == nil {
		return nil, nil
	}
	return fn(ctx, sessionID)
}

// ── 路径工具 ────────────────────────────────────────────────────────────────
//
// 远端会话的路径是**那台机器**上的路径,不能一律按本机的 filepath 规则切:
// Windows 桌面配 Linux daemon 时 filepath.Dir("/a/b") 会切出 "\a",
// filepath.IsAbs("/a") 更是直接为 false。以 "/" 开头的一律按 POSIX 规则处理,
// 其余(Windows 盘符路径)才交给 filepath。macOS / Linux 上两条分支等价。

func isSlashPath(p string) bool { return strings.HasPrefix(p, "/") }

func isAbsPath(p string) bool { return isSlashPath(p) || filepath.IsAbs(p) }

func cleanPath(p string) string {
	if isSlashPath(p) {
		return path.Clean(p)
	}
	return filepath.Clean(p)
}

func joinPath(base, rel string) string {
	if isSlashPath(base) {
		return path.Join(base, filepath.ToSlash(rel))
	}
	return filepath.Join(base, rel)
}

func dirOf(p string) string {
	if isSlashPath(p) {
		return path.Dir(p)
	}
	return filepath.Dir(p)
}

func baseOf(p string) string {
	if isSlashPath(p) {
		return path.Base(p)
	}
	return filepath.Base(p)
}

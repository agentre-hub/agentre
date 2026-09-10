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
// 信任边界从"单个会话 cwd"扩成"已认领的工作根集合"(spec 硬不变量 2):AI 常
// 在同一个主仓库的另一个 worktree 里干活,而侧栏此前只能看会话自己的 cwd。
// 集合之外仍然一律拒绝——边界只是变宽了一格,没有放松。
//
// 认领两个条件缺一不可:
//  1. AI 的写入路径落在当前已认领的根之外(否则它本来就在集合里,不是新根);
//  2. 该路径解析出的 git 公共目录(GitState.CommonDir)与会话 cwd 的相同,
//     即它指回同一个主仓库。
//
// 只满足条件 1 的 /tmp/patch.diff 不是工作根 —— 少了条件 2,任何被 AI 写过的
// 路径都会变成一个假的工作根,那等于取消了边界本身。
//
// 判定所需的 git 事实一律经 gitStateAt 取,本机走叶子包、远端走
// workspacefs.gitState RPC(daemon 侧是同一份叶子实现),因此本地会话与远端
// agentred 会话认领出的集合形状一致(硬不变量 5)。

// SessionWrittenPathsResolver 返回本会话 AI 写过的文件路径(按首次出现顺序)。
//
// 这是本服务**自己**声明的第二个窄接口(ISP + DIP),实现方同样是 chat_svc ——
// "哪些路径被写过"只有持有会话消息的那一侧答得出;本服务因此不必跨域读 chat
// 表,单测也只注入一个闭包。注入模式与 SessionWorkspaceResolver 一致。
type SessionWrittenPathsResolver func(ctx context.Context, sessionID int64) ([]string, error)

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
}

// WorkRoots 返回本会话已认领的工作根集合,第一项恒是会话 cwd 对应的根。
func (s *workspaceFsImpl) WorkRoots(ctx context.Context, sessionID int64) ([]WorkRootView, error) {
	deviceID, cwd, err := s.workspace(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return s.claimedRoots(ctx, sessionID, deviceID, cwd)
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
	roots, err := s.claimedRoots(ctx, sessionID, deviceID, cwd)
	if err != nil {
		return 0, "", err
	}
	for _, r := range roots {
		if cleanPath(r.Path) == cleaned {
			return deviceID, r.Path, nil
		}
	}
	// 越界是可见的:被拒的具体路径不进日志(与 ErrPathRefused 不回显原因同一
	// 取舍,避免成为路径探测信道),只记会话与已认领根的个数。
	logger.Ctx(ctx).Warn("workspace_fs_svc.rootFor: root outside claimed work roots",
		zap.Int64("sessionID", sessionID), zap.Int64("deviceID", deviceID),
		zap.Int("claimedCount", len(roots)))
	return 0, "", i18n.NewError(ctx, code.WorkspaceFsPathRefused)
}

// claimedRoots 从会话 cwd 出发算出完整的工作根集合。
func (s *workspaceFsImpl) claimedRoots(ctx context.Context, sessionID, deviceID int64, cwd string) ([]WorkRootView, error) {
	primary, err := s.gitStateAt(ctx, deviceID, cwd)
	if err != nil {
		return nil, err
	}
	roots := []WorkRootView{{
		Path: cwd, Name: baseOf(cwd), IsWorktree: primary.Worktree != "", IsPrimary: true,
	}}
	if primary.NotARepo || primary.CommonDir == "" {
		// 会话 cwd 不在任何 git 工作树内:没有"同一个主仓库"可指回,认领条件 2
		// 永远不成立。直接单根返回,顺带省掉一次会话消息扫描。
		return roots, nil
	}

	written, err := s.writtenPathsOf(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	for _, p := range written {
		if !isAbsPath(p) {
			// 相对路径的锚点就是会话 cwd,它天然落在已认领的根里,认不出新根。
			continue
		}
		dir := dirOf(cleanPath(p))
		if withinAnyRoot(roots, dir) {
			continue // 条件 1 不成立
		}
		root, st, ok, cerr := s.claimRoot(ctx, deviceID, dir, primary.CommonDir)
		if cerr != nil {
			return nil, cerr
		}
		if !ok || withinAnyRoot(roots, root) {
			continue // 条件 2 不成立,或向上找到的根已经在集合里
		}
		roots = append(roots, WorkRootView{Path: root, Name: baseOf(root), IsWorktree: st.Worktree != ""})
		logger.Ctx(ctx).Info("workspace_fs_svc.claimedRoots: claimed work root",
			zap.Int64("sessionID", sessionID), zap.Int64("deviceID", deviceID),
			zap.String("rootName", baseOf(root)), zap.Bool("isWorktree", st.Worktree != ""))
	}
	return roots, nil
}

// claimRoot 判定 dir 是否指回 commonDir 那个主仓库,并给出它所属工作树的根。
//
// 认领的是**工作树的根**而不是被写文件所在的那一层子目录:根的名字与内容都要
// 能当侧栏的一个根用。GitState 只给 CommonDir 不给工作树根,所以这里靠"同一
// 工作树内 CommonDir 与 worktree 短名都不变"向上走:一旦上一层换了主仓库、
// 换了 worktree、或根本不在工作树内,就说明刚才那层就是根。这样 worktree 嵌在
// 主 checkout 里(两者 CommonDir 相同)时也不会一路爬到主仓库去。
func (s *workspaceFsImpl) claimRoot(ctx context.Context, deviceID int64, dir, commonDir string) (string, *GitStateView, bool, error) {
	st, err := s.gitStateAt(ctx, deviceID, dir)
	if err != nil {
		return "", nil, false, err
	}
	if st.NotARepo || st.CommonDir != commonDir {
		return "", nil, false, nil
	}
	root := dir
	for i := 0; i < maxRootAscent; i++ {
		parent := dirOf(root)
		if parent == root {
			break
		}
		pst, perr := s.gitStateAt(ctx, deviceID, parent)
		if perr != nil {
			return "", nil, false, perr
		}
		if pst.NotARepo || pst.CommonDir != commonDir || pst.Worktree != st.Worktree {
			break
		}
		root, st = parent, pst
	}
	return root, st, true, nil
}

// writtenPathsOf 取本会话 AI 写过的路径;端口未注入时按"没写过"处理(集合退化
// 成只有 cwd,是安全方向的降级)。
func (s *workspaceFsImpl) writtenPathsOf(ctx context.Context, sessionID int64) ([]string, error) {
	fn := s.writtenPaths
	if fn == nil {
		fn = resolveWrittenPathsFn
	}
	if fn == nil {
		return nil, nil
	}
	return fn(ctx, sessionID)
}

// withinAnyRoot 判定 p 是否已经落在某个已认领的根(含根本身)之内。
func withinAnyRoot(roots []WorkRootView, p string) bool {
	for _, r := range roots {
		root := cleanPath(r.Path)
		if p == root || strings.HasPrefix(p, root+sepOf(root)) {
			return true
		}
	}
	return false
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

func sepOf(p string) string {
	if isSlashPath(p) {
		return "/"
	}
	return string(filepath.Separator)
}

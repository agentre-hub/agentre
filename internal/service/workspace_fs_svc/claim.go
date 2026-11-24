package workspace_fs_svc

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/workspace_fs_entity"
	"github.com/agentre-hub/agentre/internal/repository/workspace_fs_repo"
)

// 认领集合的状态与持久化。
//
// 认领结果落在 session_work_roots(workspace_fs_repo):重新打开会话与重启后仍在,
// 取工作根时读这份集合,不再每次从会话写过的全部路径重新爬一遍。
//
// 每个会话在本进程里有一份 claimState,装着
//   - 主根的 git 判定与已读入的认领集合;
//   - git 判定缓存 judged:目录 → GitState。远端会话每次判定都是一跳 RPC,同一会话
//     里判过的目录不再重问(spec「远端会话」:结果按会话缓存)。本机会话走同一份
//     缓存,两端认领行为一致(硬不变量 5)。只缓存「在某个 git 工作树内」的答案:
//     NotARepo 的目录之后可能被 `git worktree add` 变成工作树,不能把它判死;
//   - 写入路径来源已喂过的目录 fed:写入路径仍是候选来源(老会话靠它获得根),但
//     每个目录在本进程里只喂一次,而不是每次取工作根都整份重算;git 判定失败的
//     目录不记,下次再试。
//
// 持久化的根在本进程第一次读入时按认领规则校验一次:路径不再是认领时的那个工作树
// (不是 git 仓库、公共目录不同、worktree 短名变了)就剔除并从库里删掉;校验时 git
// 判定失败则保留(判不准不收窄),下次取工作根时重试。
//
// 并发:同一会话的取数与认领在 claimState.mu 下串行(含其间的 git 判定),避免两路
// 同时爬同一个工作树、重复认领。

// claimState 是一个会话在本进程里的认领状态。deviceID / cwd 变了(会话换了执行
// 设备或项目路径)就整份重建。
type claimState struct {
	mu       sync.Mutex
	deviceID int64
	cwd      string

	primary *GitStateView // 主根(会话 cwd)的 git 判定;nil = 尚未判定成 git 仓库
	loaded  bool          // 持久化的根已读入
	roots   []WorkRootView
	// identity 记 worktree 短名 → 根路径(主根也在其中):同一主仓库下短名唯一
	// 确定一个工作树,判「是否已认领」靠它,不靠路径字符串。
	identity   map[string]string
	unverified map[string]struct{}      // 读入后尚未校验成功的持久化根(按路径)
	judged     map[string]*GitStateView // 目录 → git 判定(只缓存工作树内的答案)
	fed        map[string]struct{}      // 写入路径来源已喂过的目录
}

func newClaimState(deviceID int64, cwd string) *claimState {
	return &claimState{
		deviceID:   deviceID,
		cwd:        cwd,
		identity:   map[string]string{},
		unverified: map[string]struct{}{},
		judged:     map[string]*GitStateView{},
		fed:        map[string]struct{}{},
	}
}

// stateFor 取会话的认领状态(不加锁;调用方随后锁 st.mu)。
func (s *workspaceFsImpl) stateFor(sessionID, deviceID int64, cwd string) *claimState {
	s.claimsMu.Lock()
	defer s.claimsMu.Unlock()
	if s.claims == nil {
		s.claims = map[int64]*claimState{}
	}
	st, ok := s.claims[sessionID]
	if !ok || st.deviceID != deviceID || st.cwd != cwd {
		st = newClaimState(deviceID, cwd)
		s.claims[sessionID] = st
	}
	return st
}

func (s *workspaceFsImpl) workRootRepo() workspace_fs_repo.WorkRootRepo {
	return workspace_fs_repo.WorkRoot()
}

// claimedRoots 返回会话的工作根集合:读入持久化的认领,再把尚未喂过的写入路径
// 当候选认领一遍。
func (s *workspaceFsImpl) claimedRoots(ctx context.Context, sessionID, deviceID int64, cwd string) ([]WorkRootView, error) {
	st := s.stateFor(sessionID, deviceID, cwd)
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := s.ensureLoaded(ctx, sessionID, st); err != nil {
		return nil, err
	}
	if st.primary == nil {
		// 会话 cwd 不在任何 git 工作树内:没有"同一个主仓库"可指回,认领条件 2
		// 永远不成立。直接单根返回,顺带省掉一次会话消息扫描。
		return st.snapshot(), nil
	}
	written, err := s.writtenPathsOf(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	for _, w := range written {
		dir, ok := WrittenDir(w, cwd)
		if !ok {
			continue
		}
		if _, done := st.fed[dir]; done {
			continue
		}
		_, settled, err := s.claimDir(ctx, sessionID, st, dir)
		if err != nil {
			return nil, err
		}
		if settled {
			st.fed[dir] = struct{}{}
		}
	}
	return st.snapshot(), nil
}

func (s *workspaceFsImpl) Claim(ctx context.Context, sessionID int64, candidates []string) ([]WorkRootView, error) {
	deviceID, cwd, err := s.workspace(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	st := s.stateFor(sessionID, deviceID, cwd)
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := s.ensureLoaded(ctx, sessionID, st); err != nil {
		return nil, err
	}
	if st.primary == nil {
		return nil, nil
	}
	var added []WorkRootView
	for _, c := range candidates {
		if !isAbsPath(c) {
			continue // 相对路径没有可靠的锚点;调用方负责按 AI 所在目录补全
		}
		c = cleanPath(c)
		root, settled, err := s.claimDir(ctx, sessionID, st, c)
		if err != nil {
			return nil, err
		}
		if root == nil && settled && st.judged[c] == nil {
			// 候选本身不在工作树内:多半是文件(用户点击 / 写入目标)或尚不存在的
			// 目录,改问它所在的目录。
			if parent := dirOf(c); parent != c {
				root, _, err = s.claimDir(ctx, sessionID, st, parent)
				if err != nil {
					return nil, err
				}
			}
		}
		if root != nil {
			added = append(added, *root)
		}
	}
	return added, nil
}

func (s *workspaceFsImpl) Revoke(ctx context.Context, sessionID int64, root string) (bool, error) {
	deviceID, cwd, err := s.workspace(ctx, sessionID)
	if err != nil {
		return false, err
	}
	target := cleanPath(root)
	if root == "" || target == cleanPath(cwd) {
		return false, nil // 主根不能撤销
	}
	st := s.stateFor(sessionID, deviceID, cwd)
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := s.ensureLoaded(ctx, sessionID, st); err != nil {
		return false, err
	}
	for _, r := range st.roots {
		if cleanPath(r.Path) != target {
			continue
		}
		if err := s.workRootRepo().Delete(ctx, sessionID, r.Path); err != nil {
			logger.Ctx(ctx).Error("workspace_fs_svc.Revoke: delete claimed root failed",
				zap.Int64("sessionID", sessionID), zap.Error(err))
			return false, err
		}
		st.drop(r.Path)
		logger.Ctx(ctx).Info("workspace_fs_svc.Revoke: revoked work root",
			zap.Int64("sessionID", sessionID), zap.Int64("deviceID", deviceID),
			zap.String("rootName", baseOf(r.Path)))
		return true, nil
	}
	return false, nil
}

// ensureLoaded 判定主根(判成 git 仓库后缓存),读入持久化的认领(每进程一次),并
// 校验尚未校验成功的持久化根。主根的 git 判定失败照旧让整次取数失败。
func (s *workspaceFsImpl) ensureLoaded(ctx context.Context, sessionID int64, st *claimState) error {
	if st.primary == nil {
		p, err := s.judge(ctx, st, st.cwd)
		if err != nil {
			return err
		}
		if p.NotARepo || p.CommonDir == "" {
			return nil
		}
		st.primary = p
		st.identity[p.Worktree] = st.cwd
	}
	if !st.loaded {
		rows, err := s.workRootRepo().ListBySession(ctx, sessionID)
		if err != nil {
			logger.Ctx(ctx).Error("workspace_fs_svc.ensureLoaded: list claimed roots failed",
				zap.Int64("sessionID", sessionID), zap.Error(err))
			return err
		}
		for _, row := range rows {
			if _, dup := st.identity[row.Worktree]; dup {
				continue
			}
			st.add(WorkRootView{Path: row.Path, Name: baseOf(row.Path), IsWorktree: row.Worktree != ""}, row.Worktree)
			st.unverified[row.Path] = struct{}{}
		}
		st.loaded = true
	}
	for _, r := range append([]WorkRootView(nil), st.roots...) {
		if _, pending := st.unverified[r.Path]; !pending {
			continue
		}
		gs, err := s.judge(ctx, st, r.Path)
		if err != nil {
			logClaimGitFailure(ctx, st.deviceID, err)
			continue // 判不准:保留,下次再校验
		}
		delete(st.unverified, r.Path)
		if gs.NotARepo || gs.CommonDir != st.primary.CommonDir || st.identity[gs.Worktree] != r.Path {
			st.drop(r.Path)
			if err := s.workRootRepo().Delete(ctx, sessionID, r.Path); err != nil {
				logger.Ctx(ctx).Warn("workspace_fs_svc.ensureLoaded: delete stale claimed root failed",
					zap.Int64("sessionID", sessionID), zap.Error(err))
			}
			logger.Ctx(ctx).Info("workspace_fs_svc.ensureLoaded: dropped stale work root",
				zap.Int64("sessionID", sessionID), zap.Int64("deviceID", st.deviceID),
				zap.String("rootName", baseOf(r.Path)))
		}
	}
	return nil
}

// claimDir 按认领规则判定目录 dir,成立时认领它所在工作树的根并持久化。
//   - root 是新认领的根(没有新认领时为 nil);
//   - settled=false 表示 git 判定失败、结论未定(调用方可以稍后重试);
//   - err 只来自持久化失败。
func (s *workspaceFsImpl) claimDir(ctx context.Context, sessionID int64, st *claimState, dir string) (*WorkRootView, bool, error) {
	gs, err := s.judge(ctx, st, dir)
	if err != nil {
		logClaimGitFailure(ctx, st.deviceID, err)
		return nil, false, nil
	}
	if gs.NotARepo || gs.CommonDir != st.primary.CommonDir {
		return nil, true, nil // 条件 2 不成立
	}
	if _, dup := st.identity[gs.Worktree]; dup {
		return nil, true, nil // 条件 1 不成立:这个工作树已经在集合里(不论路径怎么写)
	}
	root, ok := s.worktreeRootOf(ctx, st, dir, gs)
	if !ok {
		return nil, false, nil // 向上找根时 git 判定失败
	}
	view := WorkRootView{Path: root, Name: baseOf(root), IsWorktree: gs.Worktree != ""}
	if err := s.workRootRepo().Create(ctx, &workspace_fs_entity.WorkRoot{
		SessionID: sessionID, Path: root, Worktree: gs.Worktree,
	}); err != nil {
		logger.Ctx(ctx).Error("workspace_fs_svc.claimDir: persist claimed root failed",
			zap.Int64("sessionID", sessionID), zap.Error(err))
		return nil, false, err
	}
	st.add(view, gs.Worktree)
	logger.Ctx(ctx).Info("workspace_fs_svc.claimDir: claimed work root",
		zap.Int64("sessionID", sessionID), zap.Int64("deviceID", st.deviceID),
		zap.String("rootName", view.Name), zap.Bool("isWorktree", view.IsWorktree))
	return &view, true, nil
}

// worktreeRootOf 给出 dir(其 git 状态为 gs)所属工作树的根。
//
// 认领的是**工作树的根**而不是被写文件所在的那一层子目录:根的名字与内容都要
// 能当侧栏的一个根用。GitState 只给 CommonDir 不给工作树根,所以这里靠"同一
// 工作树内 CommonDir 与 worktree 短名都不变"向上走:一旦上一层换了主仓库、
// 换了 worktree、或根本不在工作树内,就说明刚才那层就是根。这样 worktree 嵌在
// 主 checkout 里(两者 CommonDir 相同)时也不会一路爬到主仓库去。
//
// 向上任何一层 git 判定失败都按"不认领"处理(ok=false)并记一条 Warn:认领只会
// 让边界变宽,判不准时就不变宽,也不该让整个侧栏取数因此失败。
func (s *workspaceFsImpl) worktreeRootOf(ctx context.Context, st *claimState, dir string, gs *GitStateView) (string, bool) {
	root := dir
	for i := 0; i < maxRootAscent; i++ {
		parent := dirOf(root)
		if parent == root {
			break
		}
		pst, err := s.judge(ctx, st, parent)
		if err != nil {
			logClaimGitFailure(ctx, st.deviceID, err)
			return "", false
		}
		if pst.NotARepo || pst.CommonDir != gs.CommonDir || pst.Worktree != gs.Worktree {
			break
		}
		root = parent
	}
	return root, true
}

// judge 取 dir 的 git 判定,同一会话内命中缓存就不再问(远端即不再发 RPC)。
func (s *workspaceFsImpl) judge(ctx context.Context, st *claimState, dir string) (*GitStateView, error) {
	dir = cleanPath(dir)
	if gs, ok := st.judged[dir]; ok {
		return gs, nil
	}
	gs, err := s.gitStateAt(ctx, st.deviceID, dir)
	if err != nil {
		return nil, err
	}
	if !gs.NotARepo {
		st.judged[dir] = gs
	}
	return gs, nil
}

func (st *claimState) add(view WorkRootView, worktree string) {
	st.roots = append(st.roots, view)
	st.identity[worktree] = view.Path
}

// drop 把 path 这个根移出集合,并清掉落在它之内的 git 判定缓存:同一路径以后若
// 又成了工作树(撤销后重建),要重新判定。
func (st *claimState) drop(path string) {
	kept := st.roots[:0]
	for _, r := range st.roots {
		if r.Path != path {
			kept = append(kept, r)
		}
	}
	st.roots = kept
	for wt, p := range st.identity {
		if p == path {
			delete(st.identity, wt)
		}
	}
	delete(st.unverified, path)
	root := cleanPath(path)
	for dir := range st.judged {
		if isWithin(root, dir) {
			delete(st.judged, dir)
		}
	}
}

// isWithin 判 p 是否就是 root 或落在它之下(按路径段,不按裸字符串前缀:
// /a/wt-notes 不在 /a/wt 之下)。两者都已 cleanPath。
func isWithin(root, p string) bool {
	if p == root {
		return true
	}
	sep := string(filepath.Separator)
	if isSlashPath(root) {
		sep = "/"
	}
	return strings.HasPrefix(p, strings.TrimSuffix(root, sep)+sep)
}

// snapshot 返回集合的副本,第一项恒是主根。
func (st *claimState) snapshot() []WorkRootView {
	out := make([]WorkRootView, 0, len(st.roots)+1)
	out = append(out, WorkRootView{
		Path: st.cwd, Name: baseOf(st.cwd), IsWorktree: st.primary != nil && st.primary.Worktree != "", IsPrimary: true,
	})
	return append(out, st.roots...)
}

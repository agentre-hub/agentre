package workspace_fs_svc

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/workspace_fs_entity"
	"github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
	"github.com/agentre-hub/agentre/internal/repository/workspace_fs_repo"
	mockWR "github.com/agentre-hub/agentre/internal/repository/workspace_fs_repo/mock_workspace_fs_repo"
)

// rootStore 是认领集合仓库 mock 背后的数据:mockgen 的 mock 把调用转进这里,两个
// svc 实例共用同一份 store 就等于「重启后读到同一个库」。createErr 非空时 Create 失败。
type rootStore struct {
	mu        sync.Mutex
	rows      []workspace_fs_entity.WorkRoot
	createErr error
}

// paths 是本会话已认领(未移除)的行。
func (st *rootStore) paths(sessionID int64) []string {
	return st.pathsWhere(sessionID, false)
}

// removedPaths 是本会话已移除(removed_at 非零)的行。
func (st *rootStore) removedPaths(sessionID int64) []string {
	return st.pathsWhere(sessionID, true)
}

func (st *rootStore) pathsWhere(sessionID int64, removed bool) []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []string
	for _, r := range st.rows {
		if r.SessionID == sessionID && (r.RemovedAt != 0) == removed {
			out = append(out, r.Path)
		}
	}
	return out
}

func (st *rootStore) put(row workspace_fs_entity.WorkRoot) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.rows = append(st.rows, row)
}

// useStore 把认领集合仓库换成背靠 store 的 mock(经 RegisterWorkRoot 注入,用例结束还原)。
func (r *rig) useStore(t *testing.T, ctrl *gomock.Controller, store *rootStore) {
	t.Helper()
	r.store = store
	repo := mockWR.NewMockWorkRootRepo(ctrl)
	repo.EXPECT().ListBySession(gomock.Any(), gomock.Any()).AnyTimes().
		DoAndReturn(func(_ context.Context, sessionID int64) ([]*workspace_fs_entity.WorkRoot, error) {
			store.mu.Lock()
			defer store.mu.Unlock()
			var out []*workspace_fs_entity.WorkRoot
			for i := range store.rows {
				if store.rows[i].SessionID == sessionID {
					row := store.rows[i]
					out = append(out, &row)
				}
			}
			return out, nil
		})
	repo.EXPECT().Create(gomock.Any(), gomock.Any()).AnyTimes().
		DoAndReturn(func(_ context.Context, row *workspace_fs_entity.WorkRoot) error {
			store.mu.Lock()
			defer store.mu.Unlock()
			if store.createErr != nil {
				return store.createErr
			}
			for i, existing := range store.rows {
				if existing.SessionID == row.SessionID && existing.Path == row.Path {
					store.rows[i].RemovedAt = 0 // 同一路径认领回来:重新记成已认领
					store.rows[i].Worktree = row.Worktree
					return nil
				}
			}
			store.rows = append(store.rows, *row)
			return nil
		})
	repo.EXPECT().MarkRemoved(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().
		DoAndReturn(func(_ context.Context, sessionID int64, path string, at int64) error {
			store.mu.Lock()
			defer store.mu.Unlock()
			for i, existing := range store.rows {
				if existing.SessionID == sessionID && existing.Path == path && existing.RemovedAt == 0 {
					store.rows[i].RemovedAt = at
				}
			}
			return nil
		})
	repo.EXPECT().Delete(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().
		DoAndReturn(func(_ context.Context, sessionID int64, path string) error {
			store.mu.Lock()
			defer store.mu.Unlock()
			kept := store.rows[:0]
			for _, existing := range store.rows {
				if existing.SessionID != sessionID || existing.Path != path {
					kept = append(kept, existing)
				}
			}
			store.rows = kept
			return nil
		})
	repo.EXPECT().DeleteBySession(gomock.Any(), gomock.Any()).AnyTimes().
		DoAndReturn(func(_ context.Context, sessionID int64) error {
			store.mu.Lock()
			defer store.mu.Unlock()
			kept := store.rows[:0]
			for _, existing := range store.rows {
				if existing.SessionID != sessionID {
					kept = append(kept, existing)
				}
			}
			store.rows = kept
			return nil
		})
	prev := workspace_fs_repo.WorkRoot()
	workspace_fs_repo.RegisterWorkRoot(repo)
	t.Cleanup(func() { workspace_fs_repo.RegisterWorkRoot(prev) })
}

func rootPaths(roots []WorkRootView) []string {
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		out = append(out, r.Path)
	}
	return out
}

func TestClaim_SameRepoWorktreeIsClaimedAndPersisted(t *testing.T) {
	convey.Convey("认领候选:同一主仓库的 worktree", t, func() {
		main := filepath.Join(t.TempDir(), "main")
		initRepoAt(t, main)
		nested := filepath.Join(main, ".claude", "worktrees", "x")
		addWorktreeAt(t, main, nested, "nested")

		convey.Convey("AI 位置落进 worktree 的子目录 → 认领它的根并持久化,WorkRoots 里出现", func() {
			r := newRig(t, 0, main)
			r.withWritten()

			added, err := r.svc.Claim(r.ctx, 42, []string{nested})
			require.NoError(t, err)
			require.Len(t, added, 1)
			assert.Equal(t, nested, added[0].Path)
			assert.Equal(t, "x", added[0].Name)
			assert.True(t, added[0].IsWorktree)
			assert.False(t, added[0].IsPrimary)
			assert.Equal(t, []string{nested}, r.store.paths(42))
			require.Len(t, r.store.rows, 1)
			assert.Equal(t, "x", r.store.rows[0].Worktree)

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			assert.Equal(t, []string{main, nested}, rootPaths(roots))

			convey.Convey("再认领同一个 worktree(换个子路径)→ 不重复,返回空", func() {
				again, err := r.svc.Claim(r.ctx, 42, []string{filepath.Join(nested, "a.go")})
				require.NoError(t, err)
				assert.Empty(t, again)
				assert.Equal(t, []string{nested}, r.store.paths(42))
			})
		})

		convey.Convey("用户点击 worktree 里文件的绝对路径 → 同样认领", func() {
			r := newRig(t, 0, main)
			r.withWritten()

			added, err := r.svc.Claim(r.ctx, 42, []string{filepath.Join(nested, "a.go")})
			require.NoError(t, err)
			assert.Equal(t, []string{nested}, rootPaths(added))
		})

		convey.Convey("相对路径与空串候选被忽略", func() {
			r := newRig(t, 0, main)
			r.withWritten()

			added, err := r.svc.Claim(r.ctx, 42, []string{"", ".claude/worktrees/x"})
			require.NoError(t, err)
			assert.Empty(t, added)
			assert.Empty(t, r.store.paths(42))
		})

		convey.Convey("持久化失败 → Claim 报错,集合里也不出现这个根", func() {
			r := newRig(t, 0, main)
			r.withWritten()
			r.store.createErr = errors.New("disk full")

			_, err := r.svc.Claim(r.ctx, 42, []string{nested})
			require.Error(t, err)

			r.store.createErr = nil
			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			assert.Equal(t, []string{main}, rootPaths(roots))
		})
	})
}

func TestClaim_Negatives(t *testing.T) {
	convey.Convey("不指回同一主仓库或 git 判定失败 → 不认领、不报错", t, func() {
		convey.Convey("本机:/tmp 目录与另一个仓库都不认领", func() {
			wr := newWorktreeRig(t)
			other := filepath.Join(t.TempDir(), "other")
			initRepoAt(t, other)
			r := newRig(t, 0, wr.main)
			r.withWritten()

			added, err := r.svc.Claim(r.ctx, 42, []string{wr.foreign, filepath.Join(wr.foreign, "patch.diff"), other})
			require.NoError(t, err)
			assert.Empty(t, added)
			assert.Empty(t, r.store.paths(42))
			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			assert.Equal(t, []string{wr.main}, rootPaths(roots))
		})

		convey.Convey("远端:不同公共目录不认领,git 判定失败的候选不认领也不报错", func() {
			r := newRig(t, 7, "/remote/main")
			r.withWritten()
			r.expectGitStateOrErr(map[string]wire.GitStateResp{
				"/remote/main": {CommonDir: "/remote/main/.git"},
				"/other/repo":  {CommonDir: "/other/repo/.git"},
				"/remote/wt":   {CommonDir: "/remote/main/.git", Worktree: "wt"},
			}, map[string]error{"/remote/broken": errors.New("connection lost")})

			added, err := r.svc.Claim(r.ctx, 42, []string{"/tmp", "/other/repo", "/remote/broken", "/remote/wt"})
			require.NoError(t, err)
			assert.Equal(t, []string{"/remote/wt"}, rootPaths(added))
			assert.Equal(t, []string{"/remote/wt"}, r.store.paths(42))
		})

		convey.Convey("会话 cwd 不在 git 仓库里 → 什么都不认领", func() {
			r := newRig(t, 7, "/remote/plain")
			r.withWritten()
			r.expectGitState(map[string]wire.GitStateResp{
				"/remote/wt": {CommonDir: "/remote/main/.git", Worktree: "wt"},
			})

			added, err := r.svc.Claim(r.ctx, 42, []string{"/remote/wt"})
			require.NoError(t, err)
			assert.Empty(t, added)
		})
	})
}

// 认领持久化:换一个全新的 svc 实例(等同重启)读同一份库,认领的根仍在,
// 不依赖写入路径重算。
func TestClaim_PersistedRootsSurviveRestart(t *testing.T) {
	wr := newWorktreeRig(t)
	store := &rootStore{}

	before := newRig(t, 0, wr.main)
	before.useStore(t, gomock.NewController(t), store)
	before.withWritten()
	added, err := before.svc.Claim(before.ctx, 42, []string{wr.wt})
	require.NoError(t, err)
	require.Len(t, added, 1)

	after := newRig(t, 0, wr.main)
	after.useStore(t, gomock.NewController(t), store)
	after.withWritten() // 重启后的会话没有任何写入记录可供重算
	roots, err := after.svc.WorkRoots(after.ctx, 42)
	require.NoError(t, err)
	require.Len(t, roots, 2)
	assert.Equal(t, wr.wt, roots[1].Path)
	assert.Equal(t, "wt", roots[1].Name)
	assert.True(t, roots[1].IsWorktree)

	// 别的会话看不到这个认领。
	other, err := after.svc.WorkRoots(after.ctx, 43)
	require.NoError(t, err)
	assert.Len(t, other, 1)
}

// 写入路径仍是候选来源:老会话靠它获得根,并且认领结果同样持久化。
func TestWorkRoots_WrittenPathClaimIsPersisted(t *testing.T) {
	wr := newWorktreeRig(t)
	r := newRig(t, 0, wr.main)
	r.withWritten(filepath.Join(wr.wt, "pkg", "x.go"))

	roots, err := r.svc.WorkRoots(r.ctx, 42)
	require.NoError(t, err)
	assert.Equal(t, []string{wr.main, wr.wt}, rootPaths(roots))
	assert.Equal(t, []string{wr.wt}, r.store.paths(42))
}

// 持久化的根后来不再是同一主仓库的那个工作树(被删掉 / 换成别的东西)→ 按认领规则
// 它不在集合里:重新读入时剔除并从库里删掉;判定时 git 失败则保留(判不准不收窄)。
func TestWorkRoots_StalePersistedRoot(t *testing.T) {
	convey.Convey("持久化的根重新读入时按认领规则校验", t, func() {
		convey.Convey("不再是 git 仓库 → 剔除并删除", func() {
			r := newRig(t, 7, "/remote/main")
			r.withWritten()
			r.store.put(workspace_fs_entity.WorkRoot{SessionID: 42, Path: "/remote/gone", Worktree: "gone"})
			r.store.put(workspace_fs_entity.WorkRoot{SessionID: 42, Path: "/remote/wt", Worktree: "wt"})
			r.expectGitState(map[string]wire.GitStateResp{
				"/remote/main": {CommonDir: "/remote/main/.git"},
				"/remote/wt":   {CommonDir: "/remote/main/.git", Worktree: "wt"},
			})

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			assert.Equal(t, []string{"/remote/main", "/remote/wt"}, rootPaths(roots))
			assert.Equal(t, []string{"/remote/wt"}, r.store.paths(42))
		})

		convey.Convey("换成另一个仓库 / 另一个 worktree → 剔除并删除", func() {
			r := newRig(t, 7, "/remote/main")
			r.withWritten()
			r.store.put(workspace_fs_entity.WorkRoot{SessionID: 42, Path: "/remote/a", Worktree: "a"})
			r.store.put(workspace_fs_entity.WorkRoot{SessionID: 42, Path: "/remote/b", Worktree: "b"})
			r.expectGitState(map[string]wire.GitStateResp{
				"/remote/main": {CommonDir: "/remote/main/.git"},
				"/remote/a":    {CommonDir: "/elsewhere/.git"},
				"/remote/b":    {CommonDir: "/remote/main/.git", Worktree: "b2"},
			})

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			assert.Equal(t, []string{"/remote/main"}, rootPaths(roots))
			assert.Empty(t, r.store.paths(42))
		})

		convey.Convey("校验时 git 判定失败 → 保留,下次再校验", func() {
			r := newRig(t, 7, "/remote/main")
			r.withWritten()
			r.store.put(workspace_fs_entity.WorkRoot{SessionID: 42, Path: "/remote/wt", Worktree: "wt"})
			failing := map[string]error{"/remote/wt": errors.New("connection lost")}
			seen := r.expectGitStateOrErr(map[string]wire.GitStateResp{
				"/remote/main": {CommonDir: "/remote/main/.git"},
				"/remote/wt":   {CommonDir: "/remote/main/.git", Worktree: "wt"},
			}, failing)

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			require.Len(t, roots, 2)
			assert.Equal(t, "/remote/wt", roots[1].Path)
			assert.True(t, roots[1].IsWorktree)
			assert.Equal(t, []string{"/remote/wt"}, r.store.paths(42))

			delete(failing, "/remote/wt")
			_, err = r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			_, err = r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			assert.Equal(t, []string{"/remote/main", "/remote/wt", "/remote/wt"}, *seen,
				"失败的那次校验要重试,成功之后不再问")
		})
	})
}

// spec「worktree 被移除」:当前进程里认领的 worktree 被别的方式删掉(Codex / pi / Bash
// 跑 `git worktree remove`)后,界面重取工作根(RefreshWorkRoots,前端在挂载与轮次结束时
// 调)要发现它已不在集合里;每个已认领的根每次只重问一次本身(不逐层爬),判定失败则保留。
// 内部取数(WorkRoots,AI 每次换位置都会用)仍走缓存,不为此多发 RPC。
func TestRefreshWorkRoots_DropsWorktreeRemovedDuringTheRun(t *testing.T) {
	r := newRig(t, 7, "/remote/main")
	r.withWritten()
	states := map[string]wire.GitStateResp{
		"/remote/main": {CommonDir: "/remote/main/.git"},
		"/remote/wt":   {CommonDir: "/remote/main/.git", Worktree: "wt"},
		"/remote/keep": {CommonDir: "/remote/main/.git", Worktree: "keep"},
		"/remote":      {NotARepo: true},
	}
	failing := map[string]error{}
	seen := r.expectGitStateOrErr(states, failing)

	added, err := r.svc.Claim(r.ctx, 42, []string{"/remote/wt", "/remote/keep"})
	require.NoError(t, err)
	require.Len(t, added, 2)

	// 什么都没变:重取仍是三个根,每个已认领的根只多问一次本身。
	before := len(*seen)
	roots, err := r.svc.RefreshWorkRoots(r.ctx, 42)
	require.NoError(t, err)
	assert.Equal(t, []string{"/remote/main", "/remote/wt", "/remote/keep"}, rootPaths(roots))
	assert.ElementsMatch(t, []string{"/remote/wt", "/remote/keep"}, (*seen)[before:])

	// /remote/wt 被删掉;/remote/keep 这次判定失败。
	delete(states, "/remote/wt")
	failing["/remote/keep"] = errors.New("connection lost")
	before = len(*seen)
	internal, err := r.svc.WorkRoots(r.ctx, 42)
	require.NoError(t, err)
	assert.Len(t, internal, 3, "内部取数不重验")
	assert.Len(t, *seen, before, "内部取数不发 RPC")

	roots, err = r.svc.RefreshWorkRoots(r.ctx, 42)
	require.NoError(t, err)
	assert.Equal(t, []string{"/remote/main", "/remote/keep"}, rootPaths(roots), "删掉的根出集合,判不准的保留")
	assert.Equal(t, []string{"/remote/keep"}, r.store.paths(42), "删掉的根也从库里删除")
}

// 远端 git 判定按会话缓存:同一会话里已判过的目录不再发 gitState RPC,
// 取工作根不再每次逐层重爬。
func TestWorkRoots_RemoteJudgementsCachedPerSession(t *testing.T) {
	r := newRig(t, 7, "/remote/main")
	r.withWritten("/remote/wt/pkg/x.go", "/tmp/patch.diff")
	seen := r.expectGitState(map[string]wire.GitStateResp{
		"/remote/main":   {CommonDir: "/remote/main/.git"},
		"/remote/wt/pkg": {CommonDir: "/remote/main/.git", Worktree: "wt"},
		"/remote/wt":     {CommonDir: "/remote/main/.git", Worktree: "wt"},
	})

	first, err := r.svc.WorkRoots(r.ctx, 42)
	require.NoError(t, err)
	assert.Equal(t, []string{"/remote/main", "/remote/wt"}, rootPaths(first))
	askedOnce := append([]string(nil), *seen...)

	for i := 0; i < 3; i++ {
		again, err := r.svc.WorkRoots(r.ctx, 42)
		require.NoError(t, err)
		assert.Equal(t, rootPaths(first), rootPaths(again))
	}
	// AI 位置在已判过的目录里来回变化,也不再问。
	added, err := r.svc.Claim(r.ctx, 42, []string{"/remote/wt/pkg", "/remote/wt", "/remote/main"})
	require.NoError(t, err)
	assert.Empty(t, added)
	// root 参数闸门同样走缓存后的集合。
	_, err = r.svc.GitState(r.ctx, 42, "/remote/wt")
	require.NoError(t, err)

	assert.Equal(t, append(askedOnce, "/remote/wt"), *seen,
		"只有 GitState 自己那一跳是新的;认领判定一个目录都没有重问")
	assert.Equal(t, []string{"/remote/main", "/remote/wt/pkg", "/remote/wt", "/remote", "/tmp"}, askedOnce)
}

func TestRevoke(t *testing.T) {
	convey.Convey("撤销认领", t, func() {
		wr := newWorktreeRig(t)

		convey.Convey("撤销已认领的根 → 集合与库里都没有它,写入路径重算也不把它认领回来", func() {
			r := newRig(t, 0, wr.main)
			r.withWritten(filepath.Join(wr.wt, "pkg", "x.go"))
			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			require.Len(t, roots, 2)

			revoked, err := r.svc.Revoke(r.ctx, 42, wr.wt)
			require.NoError(t, err)
			assert.True(t, revoked)
			assert.Empty(t, r.store.paths(42))

			roots, err = r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			assert.Equal(t, []string{wr.main}, rootPaths(roots))
		})

		convey.Convey("重启后撤销持久化的根", func() {
			r := newRig(t, 0, wr.main)
			r.withWritten()
			r.store.put(workspace_fs_entity.WorkRoot{SessionID: 42, Path: wr.wt, Worktree: "wt"})

			revoked, err := r.svc.Revoke(r.ctx, 42, wr.wt+"/")
			require.NoError(t, err)
			assert.True(t, revoked)
			assert.Empty(t, r.store.paths(42))
		})

		convey.Convey("主根不能撤销;不在集合里的路径什么也不做", func() {
			r := newRig(t, 0, wr.main)
			r.withWritten()
			r.store.put(workspace_fs_entity.WorkRoot{SessionID: 42, Path: wr.wt, Worktree: "wt"})

			revoked, err := r.svc.Revoke(r.ctx, 42, wr.main)
			require.NoError(t, err)
			assert.False(t, revoked)
			revoked, err = r.svc.Revoke(r.ctx, 42, wr.foreign)
			require.NoError(t, err)
			assert.False(t, revoked)

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			assert.Equal(t, []string{wr.main, wr.wt}, rootPaths(roots))
			assert.Equal(t, []string{wr.wt}, r.store.paths(42))
		})

		convey.Convey("撤销后再认领同一个 worktree → 重新判定并认领回来", func() {
			r := newRig(t, 0, wr.main)
			r.withWritten()
			_, err := r.svc.Claim(r.ctx, 42, []string{wr.wt})
			require.NoError(t, err)
			_, err = r.svc.Revoke(r.ctx, 42, wr.wt)
			require.NoError(t, err)

			added, err := r.svc.Claim(r.ctx, 42, []string{wr.wt})
			require.NoError(t, err)
			assert.Equal(t, []string{wr.wt}, rootPaths(added))
		})
	})
}

// Given 一条会话认领过 worktree, When 会话被删除(ForgetSession), Then 库里的认领与本进程里
// 的认领状态一并清掉:之后按同一会话号取工作根只剩主根,不靠内存里的旧集合。
func TestForgetSession_DropsPersistedAndInMemoryClaims(t *testing.T) {
	wr := newWorktreeRig(t)
	r := newRig(t, 0, wr.main)
	r.withWritten()
	added, err := r.svc.Claim(r.ctx, 42, []string{wr.wt})
	require.NoError(t, err)
	require.Len(t, added, 1)

	require.NoError(t, r.svc.ForgetSession(r.ctx, 42))

	assert.Empty(t, r.store.paths(42))
	roots, err := r.svc.WorkRoots(r.ctx, 42)
	require.NoError(t, err)
	assert.Equal(t, []string{wr.main}, rootPaths(roots))
}

// Given 库里有一行认领与已在集合里的工作树是同一个(另一种路径写法), When 读入持久化集合,
// Then 这一行从库里删掉,不在之后每次启动都被读出来再跳过。
func TestWorkRoots_DuplicatePersistedRowIsDeleted(t *testing.T) {
	wr := newWorktreeRig(t)
	r := newRig(t, 0, wr.main)
	r.withWritten()
	r.store.put(workspace_fs_entity.WorkRoot{SessionID: 42, Path: wr.main + "-alias", Worktree: ""})

	roots, err := r.svc.WorkRoots(r.ctx, 42)
	require.NoError(t, err)
	assert.Equal(t, []string{wr.main}, rootPaths(roots))
	assert.Empty(t, r.store.paths(42))
}

// Given 一个已在集合里的认领根, When 按它读目录, Then 闸门直接按已知集合放行,不为每次读
// 重扫会话写过的全部路径(那是取工作根时的补喂,不是读文件的前置)。
func TestRootFor_KnownClaimedRootDoesNotRescanWrittenPaths(t *testing.T) {
	wr := newWorktreeRig(t)
	r := newRig(t, 0, wr.main)
	r.withWritten()
	_, err := r.svc.Claim(r.ctx, 42, []string{wr.wt})
	require.NoError(t, err)
	scans := 0
	r.svc.writtenPaths = func(context.Context, int64) ([]WrittenPath, error) {
		scans++
		return nil, nil
	}

	_, err = r.svc.ListDir(r.ctx, 42, wr.wt, "", false)
	require.NoError(t, err)
	assert.Zero(t, scans)
}

// spec「worktree 被移除」:变更窗里被移除的根以「<名称>（已移除）」分组显示。界面没开着
// 这个会话时(后台跑的会话、派发出去的会话)前端从没见过这个根,所以「它曾是一个根」
// 记在认领行上(removed_at)。RemovedWorkRoots 交回从认领集合中消失的根:撤销的、重验时
// 已不是认领时那个工作树的(目录没了 / 换成了别的 worktree);又被认领回来的不在其中;
// 换到另一个仓库(会话目录改了)不算移除。
func TestRemovedWorkRoots(t *testing.T) {
	convey.Convey("从认领集合中消失的根", t, func() {
		removedPaths := func(r *rig) []string {
			t.Helper()
			removed, err := r.svc.RemovedWorkRoots(r.ctx, 42)
			require.NoError(t, err)
			for _, v := range removed {
				assert.True(t, v.Removed, "every entry is flagged removed")
			}
			return rootPaths(removed)
		}

		convey.Convey("撤销的根 → 在移除清单里,带名称与类型;集合里没有它", func() {
			r := newRig(t, 7, "/remote/main")
			r.withWritten()
			r.expectGitState(map[string]wire.GitStateResp{
				"/remote/main": {CommonDir: "/remote/main/.git"},
				"/remote/wt":   {CommonDir: "/remote/main/.git", Worktree: "wt"},
				"/remote":      {NotARepo: true},
			})
			_, err := r.svc.Claim(r.ctx, 42, []string{"/remote/wt"})
			require.NoError(t, err)
			assert.Empty(t, removedPaths(r))

			_, err = r.svc.Revoke(r.ctx, 42, "/remote/wt")
			require.NoError(t, err)
			removed, err := r.svc.RemovedWorkRoots(r.ctx, 42)
			require.NoError(t, err)
			assert.Equal(t, []WorkRootView{{Path: "/remote/wt", Name: "wt", IsWorktree: true, Removed: true}}, removed)
			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			assert.Equal(t, []string{"/remote/main"}, rootPaths(roots))

			convey.Convey("又被认领回来 → 不再在移除清单里", func() {
				_, err := r.svc.Claim(r.ctx, 42, []string{"/remote/wt"})
				require.NoError(t, err)
				assert.Empty(t, removedPaths(r))
			})
		})

		convey.Convey("读入时已不在的持久化根 → 在移除清单里;换到另一个仓库的不算", func() {
			r := newRig(t, 7, "/remote/main")
			r.withWritten()
			r.store.put(workspace_fs_entity.WorkRoot{SessionID: 42, Path: "/remote/gone", Worktree: "gone"})
			r.store.put(workspace_fs_entity.WorkRoot{SessionID: 42, Path: "/remote/other", Worktree: "other"})
			r.store.put(workspace_fs_entity.WorkRoot{SessionID: 42, Path: "/remote/b", Worktree: "b"})
			r.expectGitState(map[string]wire.GitStateResp{
				"/remote/main":  {CommonDir: "/remote/main/.git"},
				"/remote/other": {CommonDir: "/elsewhere/.git"},
				"/remote/b":     {CommonDir: "/remote/main/.git", Worktree: "b2"},
			})
			_, err := r.svc.RefreshWorkRoots(r.ctx, 42)
			require.NoError(t, err)
			assert.Equal(t, []string{"/remote/gone", "/remote/b"}, removedPaths(r))
		})

		convey.Convey("运行中被删掉、界面重取时发现的根 → 在移除清单里", func() {
			r := newRig(t, 7, "/remote/main")
			r.withWritten()
			states := map[string]wire.GitStateResp{
				"/remote/main": {CommonDir: "/remote/main/.git"},
				"/remote/wt":   {CommonDir: "/remote/main/.git", Worktree: "wt"},
				"/remote":      {NotARepo: true},
			}
			r.expectGitStateOrErr(states, map[string]error{})
			_, err := r.svc.Claim(r.ctx, 42, []string{"/remote/wt"})
			require.NoError(t, err)
			delete(states, "/remote/wt")
			_, err = r.svc.RefreshWorkRoots(r.ctx, 42)
			require.NoError(t, err)
			assert.Equal(t, []string{"/remote/wt"}, removedPaths(r))
		})
	})
}

// spec「worktree 被移除」+ R4:移除记在认领行上(removed_at),不只在进程内存里 —— 应用
// 重启后,这一轮里从没打开过的会话取工作根时同样拿到它(removed=true);同一路径被认领
// 回来时清掉;会话删除时连同已移除的行一起删掉。
func TestRemovedWorkRoots_SurviveRestart(t *testing.T) {
	wr := newWorktreeRig(t)
	store := &rootStore{}

	before := newRig(t, 0, wr.main)
	before.useStore(t, gomock.NewController(t), store)
	before.withWritten()
	_, err := before.svc.Claim(before.ctx, 42, []string{wr.wt})
	require.NoError(t, err)
	revoked, err := before.svc.Revoke(before.ctx, 42, wr.wt)
	require.NoError(t, err)
	require.True(t, revoked)
	assert.Empty(t, store.paths(42), "the row is no longer a claimed root")
	assert.Equal(t, []string{wr.wt}, store.removedPaths(42), "the row stays, marked removed")

	after := newRig(t, 0, wr.main)
	after.useStore(t, gomock.NewController(t), store)
	after.withWritten()
	roots, err := after.svc.WorkRoots(after.ctx, 42)
	require.NoError(t, err)
	assert.Equal(t, []string{wr.main}, rootPaths(roots), "a removed row is not loaded as a root")
	removed, err := after.svc.RemovedWorkRoots(after.ctx, 42)
	require.NoError(t, err)
	assert.Equal(t, []WorkRootView{{Path: wr.wt, Name: "wt", IsWorktree: true, Removed: true}}, removed)

	// 认领回来:清掉 removed_at,不再出现在移除清单里。
	added, err := after.svc.Claim(after.ctx, 42, []string{wr.wt})
	require.NoError(t, err)
	assert.Equal(t, []string{wr.wt}, rootPaths(added))
	assert.Equal(t, []string{wr.wt}, store.paths(42))
	assert.Empty(t, store.removedPaths(42))
	removed, err = after.svc.RemovedWorkRoots(after.ctx, 42)
	require.NoError(t, err)
	assert.Empty(t, removed)

	// 会话删除:已移除的行也一并删掉。
	_, err = after.svc.Revoke(after.ctx, 42, wr.wt)
	require.NoError(t, err)
	require.NoError(t, after.svc.ForgetSession(after.ctx, 42))
	assert.Empty(t, store.paths(42))
	assert.Empty(t, store.removedPaths(42))
}

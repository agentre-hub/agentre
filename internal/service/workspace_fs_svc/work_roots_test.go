package workspace_fs_svc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
)

// initRepoAt 在指定目录(而不是新开一个 t.TempDir())建一个真实 git 仓库,
// 让主 checkout 与它的 worktree 能落在同一个父目录下。
func initRepoAt(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
}

// worktreeRig 建「主仓库 + 同一主仓库的另一个 worktree + 一个无关目录」三件套,
// 这正是本任务要分辨的三种候选路径。
type worktreeRig struct {
	main    string // 会话 cwd:主 checkout
	wt      string // 同一主仓库的 attached worktree
	foreign string // 与本仓库无关的目录(另一个仓库 / 纯 /tmp 目录)
}

func newWorktreeRig(t *testing.T) worktreeRig {
	t.Helper()
	base := t.TempDir()
	rig := worktreeRig{
		main:    filepath.Join(base, "main"),
		wt:      filepath.Join(base, "wt"),
		foreign: filepath.Join(base, "foreign"),
	}
	initRepoAt(t, rig.main)
	runGit(t, rig.main, "worktree", "add", "-q", "-b", "side", rig.wt)
	require.NoError(t, os.MkdirAll(filepath.Join(rig.wt, "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(rig.wt, "pkg", "x.go"), []byte("package pkg\n"), 0o644))
	require.NoError(t, os.MkdirAll(rig.foreign, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(rig.foreign, "patch.diff"), []byte("diff\n"), 0o644))
	return rig
}

// expectGitState 让 mock 客户端按 Root 回一份预置的 gitState 结果:认领判定在
// 远端是若干次 workspacefs.gitState 往返,次数取决于向上找工作树根走了几层,
// 因此这里按 Root 查表而不是钉死调用次数。
// 返回值是被问过的 Root 序列,调用方据此断言"哪些路径进过 RPC"。
func (r *rig) expectGitState(states map[string]wire.GitStateResp) *[]string {
	seen := &[]string{}
	r.rd.EXPECT().Pool().Return(r.pool).AnyTimes()
	r.pool.EXPECT().Borrow(gomock.Any(), gomock.Any()).Return(r.lease, nil).AnyTimes()
	r.lease.EXPECT().Client().Return(r.client).AnyTimes()
	r.lease.EXPECT().Release().AnyTimes()
	r.fallback[wire.MethodGitState] = func(_ context.Context, _ string, req any, out any) error {
		root := req.(wire.GitStateReq).Root
		*seen = append(*seen, root)
		resp, ok := states[root]
		if !ok {
			resp = wire.GitStateResp{NotARepo: true}
		}
		*out.(*wire.GitStateResp) = resp
		return nil
	}
	return seen
}

// withWritten 把「本会话 AI 写过的路径」这个窄端口换成固定清单。
func (r *rig) withWritten(paths ...string) {
	r.svc.writtenPaths = func(context.Context, int64) ([]string, error) {
		return paths, nil
	}
}

func TestWorkRoots_ClaimsWorktreeOfSameMainRepo(t *testing.T) {
	convey.Convey("工作根认领", t, func() {
		wr := newWorktreeRig(t)

		convey.Convey("没有任何写入 → 只有会话 cwd 一个根", func() {
			r := newRig(t, 0, wr.main)
			r.withWritten()

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			require.Len(t, roots, 1)
			assert.Equal(t, wr.main, roots[0].Path)
			assert.Equal(t, "main", roots[0].Name)
			assert.True(t, roots[0].IsPrimary)
			assert.False(t, roots[0].IsWorktree)
		})

		convey.Convey("写进同一主仓库的另一个 worktree → 认领成第二个根", func() {
			r := newRig(t, 0, wr.main)
			r.withWritten(filepath.Join(wr.wt, "pkg", "x.go"))

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			require.Len(t, roots, 2)
			assert.Equal(t, wr.main, roots[0].Path)
			assert.True(t, roots[0].IsPrimary)
			// 认领的是 worktree 的根,而不是被写文件所在的那一层子目录。
			assert.Equal(t, wr.wt, roots[1].Path)
			assert.Equal(t, "wt", roots[1].Name)
			assert.True(t, roots[1].IsWorktree)
			assert.False(t, roots[1].IsPrimary)
		})

		convey.Convey("写进 cwd 之内 → 不认领新根(条件一不成立)", func() {
			r := newRig(t, 0, wr.main)
			r.withWritten(filepath.Join(wr.main, "internal", "a.go"))

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			require.Len(t, roots, 1)
			assert.Equal(t, wr.main, roots[0].Path)
		})

		convey.Convey("写进 cwd 之外但不指回同一主仓库 → 不认领(条件二不成立)", func() {
			convey.Convey("非 git 目录的 /tmp/patch.diff", func() {
				r := newRig(t, 0, wr.main)
				r.withWritten(filepath.Join(wr.foreign, "patch.diff"))

				roots, err := r.svc.WorkRoots(r.ctx, 42)
				require.NoError(t, err)
				require.Len(t, roots, 1)
				assert.Equal(t, wr.main, roots[0].Path)
			})

			convey.Convey("另一个独立 git 仓库", func() {
				other := filepath.Join(t.TempDir(), "other")
				initRepoAt(t, other)
				r := newRig(t, 0, wr.main)
				r.withWritten(filepath.Join(other, "b.go"))

				roots, err := r.svc.WorkRoots(r.ctx, 42)
				require.NoError(t, err)
				require.Len(t, roots, 1)
				assert.Equal(t, wr.main, roots[0].Path)
			})
		})

		convey.Convey("名字以 cwd 为前缀的兄弟目录不算落在根内 → 照样认领", func() {
			// wt-sibling 与会话 cwd 是**兄弟**,只是名字恰好以它开头。判"已在
			// 某个根内"若按裸字符串前缀比,这个 worktree 会被当成 main 的子目录
			// 而永远认领不到,侧栏就少一个根。
			sibling := wr.main + "-notes"
			runGit(t, wr.main, "worktree", "add", "-q", "-b", "notes", sibling)
			r := newRig(t, 0, wr.main)
			r.withWritten(filepath.Join(sibling, "n.go"))

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			require.Len(t, roots, 2)
			assert.Equal(t, sibling, roots[1].Path)
			assert.True(t, roots[1].IsWorktree)
		})

		convey.Convey("同一个 worktree 被写多次 → 只认领一次", func() {
			r := newRig(t, 0, wr.main)
			r.withWritten(
				filepath.Join(wr.wt, "pkg", "x.go"),
				filepath.Join(wr.wt, "pkg", "y.go"),
				filepath.Join(wr.wt, "z.go"),
			)

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			require.Len(t, roots, 2)
			assert.Equal(t, wr.wt, roots[1].Path)
		})
	})
}

func TestRootParam_OutsideClaimedSetRefused(t *testing.T) {
	convey.Convey("root 参数的信任边界是已认领的工作根集合", t, func() {
		wr := newWorktreeRig(t)

		convey.Convey("已认领的 worktree root → 列举 / 读取 / 搜索都成功", func() {
			r := newRig(t, 0, wr.main)
			r.withWritten(filepath.Join(wr.wt, "pkg", "x.go"))

			view, err := r.svc.ListDir(r.ctx, 42, wr.wt, "pkg", false)
			require.NoError(t, err)
			assert.Equal(t, filepath.Join(wr.wt, "pkg"), view.Path)
			require.Len(t, view.Entries, 1)
			assert.Equal(t, "x.go", view.Entries[0].Name)

			read, err := r.svc.ReadFile(r.ctx, 42, wr.wt, "pkg/x.go")
			require.NoError(t, err)
			assert.Equal(t, "package pkg\n", read.Content)

			hits, err := r.svc.SearchFiles(r.ctx, 42, wr.wt, "x.go", false)
			require.NoError(t, err)
			require.Len(t, hits.Hits, 1)
			assert.Equal(t, "pkg/x.go", hits.Hits[0].Path)
		})

		convey.Convey("未认领的 root → 列举 / 读取 / 搜索一律 ErrPathRefused", func() {
			r := newRig(t, 0, wr.main)
			r.withWritten(filepath.Join(wr.foreign, "patch.diff")) // 写过,但没被认领

			refused := i18n.NewError(r.ctx, code.WorkspaceFsPathRefused).Error()

			_, err := r.svc.ListDir(r.ctx, 42, wr.foreign, "", false)
			require.Error(t, err)
			assert.Equal(t, refused, err.Error())

			_, err = r.svc.ReadFile(r.ctx, 42, wr.foreign, "patch.diff")
			require.Error(t, err)
			assert.Equal(t, refused, err.Error())

			_, err = r.svc.SearchFiles(r.ctx, 42, wr.foreign, "patch", false)
			require.Error(t, err)
			assert.Equal(t, refused, err.Error())

			_, err = r.svc.GitChanges(r.ctx, 42, wr.foreign, wire.ScopeUncommitted, "")
			require.Error(t, err)
			assert.Equal(t, refused, err.Error())

			_, err = r.svc.GitFileContent(r.ctx, 42, wr.foreign, "patch.diff")
			require.Error(t, err)
			assert.Equal(t, refused, err.Error())

			// GitState 走同一道闸门:它同样接一个调用方给的 root,不过闸门就等于
			// 让前端拿到"随便指一个目录,报回它的分支 / 未提交数 / commonDir"这
			// 条路径探测信道 —— 远端会话下那还是发进别人机器的一跳 RPC。
			_, err = r.svc.GitState(r.ctx, 42, wr.foreign)
			require.Error(t, err)
			assert.Equal(t, refused, err.Error())
		})

		convey.Convey("越界 root 不得拼进相对路径绕过", func() {
			r := newRig(t, 0, wr.main)
			r.withWritten()
			// root 空串仍是会话 cwd,relPath 的 ".." 越界照旧被拒。
			_, err := r.svc.ListDir(r.ctx, 42, "", "../foreign", false)
			require.Error(t, err)
			assert.Equal(t, i18n.NewError(r.ctx, code.WorkspaceFsPathRefused).Error(), err.Error())
		})
	})
}

func TestRootParam_RemoteSessionRunsSameJudgement(t *testing.T) {
	convey.Convey("远端会话走同一套判定(硬不变量 5)", t, func() {
		convey.Convey("未认领的 root 被拒,且绝不作为 Root 发进任何 RPC", func() {
			r := newRig(t, 7, "/remote/main")
			r.withWritten()
			// 认领判定本身要向远端问一次 cwd 的 gitState;除此之外不该有别的
			// 往返,更不该把 "/etc" 当 Root 发出去。
			seen := r.expectGitState(map[string]wire.GitStateResp{
				"/remote/main": {CommonDir: "/remote/main/.git"},
			})
			_, err := r.svc.ListDir(r.ctx, 42, "/etc", "", false)
			require.Error(t, err)
			assert.Equal(t, i18n.NewError(r.ctx, code.WorkspaceFsPathRefused).Error(), err.Error())
			assert.Equal(t, []string{"/remote/main"}, *seen)
		})

		convey.Convey("认领判定靠远端 gitState:同一 commonDir 的 worktree 被认领,随后可列举", func() {
			r := newRig(t, 7, "/remote/main")
			r.withWritten("/remote/wt/pkg/x.go")
			states := map[string]wire.GitStateResp{
				"/remote/main":   {CommonDir: "/remote/main/.git"},
				"/remote/wt/pkg": {CommonDir: "/remote/main/.git", Worktree: "wt"},
				"/remote/wt":     {CommonDir: "/remote/main/.git", Worktree: "wt"},
				"/remote":        {NotARepo: true},
			}
			r.expectGitState(states)

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			require.Len(t, roots, 2)
			assert.Equal(t, "/remote/wt", roots[1].Path)
			assert.True(t, roots[1].IsWorktree)

			// 认领之后,这个 root 才被允许发进 workspacefs.listDir RPC。
			r.expectProto(wire.MethodListDir, wire.ListDirReq{Root: "/remote/wt", RelPath: "pkg"}).
				DoAndReturn(func(_ context.Context, _ string, _ any, out any) error {
					out.(*wire.ListDirResp).Path = "/remote/wt/pkg"
					return nil
				})
			view, err := r.svc.ListDir(r.ctx, 42, "/remote/wt", "pkg", false)
			require.NoError(t, err)
			assert.Equal(t, "/remote/wt/pkg", view.Path)
		})
	})
}

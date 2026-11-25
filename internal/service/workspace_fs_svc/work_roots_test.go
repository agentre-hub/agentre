package workspace_fs_svc

import (
	"context"
	"errors"
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
	return r.expectGitStateOrErr(states, nil)
}

// expectGitStateOrErr 同 expectGitState,另外让 failing 里的 Root 回一个 RPC 错误,
// 用来复现「git 判定失败」(远端掉线、daemon 侧 git 报错)。
func (r *rig) expectGitStateOrErr(states map[string]wire.GitStateResp, failing map[string]error) *[]string {
	seen := &[]string{}
	r.rd.EXPECT().Pool().Return(r.pool).AnyTimes()
	r.pool.EXPECT().Borrow(gomock.Any(), gomock.Any()).Return(r.lease, nil).AnyTimes()
	r.lease.EXPECT().Client().Return(r.client).AnyTimes()
	r.lease.EXPECT().Release().AnyTimes()
	r.fallback["workspacefs.gitState"] = func(_ context.Context, _ string, req any, out any) error {
		root := req.(wire.GitStateReq).Root
		*seen = append(*seen, root)
		if err, bad := failing[root]; bad {
			return err
		}
		resp, ok := states[root]
		if !ok {
			resp = wire.GitStateResp{NotARepo: true}
		}
		*out.(*wire.GitStateResp) = resp
		return nil
	}
	return seen
}

// withWritten 把「本会话 AI 写过的路径」这个窄端口换成固定清单(调用所在目录
// 都没有记录,即 Dir 为空)。
func (r *rig) withWritten(paths ...string) {
	ws := make([]WrittenPath, 0, len(paths))
	for _, p := range paths {
		ws = append(ws, WrittenPath{Path: p})
	}
	r.withWrites(ws...)
}

// withWrites 同 withWritten,但每次写入可带调用所在目录。
func (r *rig) withWrites(ws ...WrittenPath) {
	r.svc.writtenPaths = func(context.Context, int64) ([]WrittenPath, error) {
		return ws, nil
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
			r.expectProto("workspacefs.listDir", wire.ListDirReq{Root: "/remote/wt", RelPath: "pkg"}).
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

// addWorktreeAt 在 main 仓库里把一个新 worktree 建到 dir(可以落在 main 自己的目录树
// 之内,正是 Claude Code EnterWorktree / Hermes 的默认布局),并在里面放一个文件。
func addWorktreeAt(t *testing.T, main, dir, branch string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(dir), 0o755))
	runGit(t, main, "worktree", "add", "-q", "-b", branch, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644))
}

// 问题 5:建在仓库内部的 worktree 路径前缀落在主根之内,不能因此被当成「已在根内」。
func TestWorkRoots_ClaimsWorktreeNestedInsideClaimedRoot(t *testing.T) {
	convey.Convey("嵌在主 checkout 目录树里的 worktree 也认领为新根", t, func() {
		for _, rel := range []string{".claude/worktrees/x", ".worktrees/x"} {
			convey.Convey(rel, func() {
				main := filepath.Join(t.TempDir(), "main")
				initRepoAt(t, main)
				nested := filepath.Join(main, filepath.FromSlash(rel))
				addWorktreeAt(t, main, nested, "nested")
				r := newRig(t, 0, main)
				r.withWritten(filepath.Join(nested, "a.go"))

				roots, err := r.svc.WorkRoots(r.ctx, 42)
				require.NoError(t, err)
				require.Len(t, roots, 2)
				assert.Equal(t, main, roots[0].Path)
				assert.Equal(t, nested, roots[1].Path)
				assert.Equal(t, "x", roots[1].Name)
				assert.True(t, roots[1].IsWorktree)
			})
		}

		convey.Convey("远端会话:同一 commonDir、不同 worktree 的嵌套目录被认领", func() {
			r := newRig(t, 7, "/remote/main")
			r.withWritten("/remote/main/.claude/worktrees/x/pkg/a.go")
			r.expectGitState(map[string]wire.GitStateResp{
				"/remote/main":                         {CommonDir: "/remote/main/.git"},
				"/remote/main/.claude/worktrees/x/pkg": {CommonDir: "/remote/main/.git", Worktree: "x"},
				"/remote/main/.claude/worktrees/x":     {CommonDir: "/remote/main/.git", Worktree: "x"},
				"/remote/main/.claude/worktrees":       {CommonDir: "/remote/main/.git"},
			})

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			require.Len(t, roots, 2)
			assert.Equal(t, "/remote/main/.claude/worktrees/x", roots[1].Path)
			assert.True(t, roots[1].IsWorktree)
		})
	})
}

// 问题 7:相对写入路径不能直接跳过。
func TestWorkRoots_RelativeWrittenPathAnchored(t *testing.T) {
	convey.Convey("相对写入路径按 AI 所在目录补全", t, func() {
		convey.Convey("无记录目录 → 回落会话 cwd:../wt/pkg/x.go 认领 wt", func() {
			wr := newWorktreeRig(t)
			r := newRig(t, 0, wr.main)
			r.withWritten("../wt/pkg/x.go")

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			require.Len(t, roots, 2)
			assert.Equal(t, wr.wt, roots[1].Path)
			assert.True(t, roots[1].IsWorktree)
		})

		convey.Convey("有记录目录 → 按该目录补全,而不是会话 cwd", func() {
			wr := newWorktreeRig(t)
			r := newRig(t, 0, wr.main)
			// 同一个相对路径:按 cwd 补全落在 main 里(不认领),按 Dir 补全才落进 wt。
			r.withWrites(WrittenPath{Path: "pkg/x.go", Dir: wr.wt})

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			require.Len(t, roots, 2)
			assert.Equal(t, wr.wt, roots[1].Path)
		})

		convey.Convey("远端会话同样补全:Dir 为空回落 cwd,有 Dir 按 Dir", func() {
			r := newRig(t, 7, "/remote/main")
			r.withWrites(
				WrittenPath{Path: "../wt/pkg/x.go"},
				WrittenPath{Path: "y.go", Dir: "/remote/wt2"},
			)
			r.expectGitState(map[string]wire.GitStateResp{
				"/remote/main":   {CommonDir: "/remote/main/.git"},
				"/remote/wt/pkg": {CommonDir: "/remote/main/.git", Worktree: "wt"},
				"/remote/wt":     {CommonDir: "/remote/main/.git", Worktree: "wt"},
				"/remote/wt2":    {CommonDir: "/remote/main/.git", Worktree: "wt2"},
			})

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			require.Len(t, roots, 3)
			assert.Equal(t, "/remote/wt", roots[1].Path)
			assert.Equal(t, "/remote/wt2", roots[2].Path)
		})
	})
}

// 比较路径前统一为真实路径:同一个目录经符号链接换个写法(macOS 的 /tmp 与
// /private/tmp)不能被当成两个地方。
func TestWorkRoots_SymlinkSpellingsAreOnePlace(t *testing.T) {
	convey.Convey("符号链接写法不同的同一目录只算一个根", t, func() {
		convey.Convey("本机:cwd 走链接,写入走真实路径", func() {
			realDir := t.TempDir()
			link := filepath.Join(t.TempDir(), "link")
			require.NoError(t, os.Symlink(realDir, link))
			initRepoAt(t, filepath.Join(realDir, "main"))
			runGit(t, filepath.Join(realDir, "main"), "worktree", "add", "-q", "-b", "side", filepath.Join(realDir, "wt"))

			r := newRig(t, 0, filepath.Join(link, "main"))
			r.withWritten(
				filepath.Join(realDir, "main", "a.go"), // 就是会话 cwd
				filepath.Join(realDir, "wt", "b.go"),   // 另一个 worktree
				filepath.Join(link, "wt", "c.go"),      // 同一个 worktree 的另一种写法
			)

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			require.Len(t, roots, 2)
			assert.Equal(t, filepath.Join(link, "main"), roots[0].Path)
			assert.Equal(t, "wt", roots[1].Name)
		})

		convey.Convey("远端:/tmp 与 /private/tmp 指向同一 checkout", func() {
			r := newRig(t, 7, "/tmp/r/main")
			r.withWritten("/private/tmp/r/main/a.go")
			r.expectGitState(map[string]wire.GitStateResp{
				"/tmp/r/main":         {CommonDir: "/private/tmp/r/main/.git"},
				"/private/tmp/r/main": {CommonDir: "/private/tmp/r/main/.git"},
			})

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			require.Len(t, roots, 1)
			assert.Equal(t, "/tmp/r/main", roots[0].Path)
		})
	})
}

// 判定已认领工作树里的写入只问一次 git:向上找工作树根只发生在要认领新根时,
// 远端会话下每一层都是一跳 RPC。
func TestWorkRoots_WritesInClaimedWorktreeAskGitOncePerDir(t *testing.T) {
	r := newRig(t, 7, "/remote/main")
	r.withWritten("/remote/main/a/b/c/x.go", "/remote/main/a/b/c/y.go", "/remote/main/d/z.go")
	seen := r.expectGitState(map[string]wire.GitStateResp{
		"/remote/main":       {CommonDir: "/remote/main/.git"},
		"/remote/main/a/b/c": {CommonDir: "/remote/main/.git"},
		"/remote/main/d":     {CommonDir: "/remote/main/.git"},
	})

	roots, err := r.svc.WorkRoots(r.ctx, 42)
	require.NoError(t, err)
	require.Len(t, roots, 1)
	assert.Equal(t, []string{"/remote/main", "/remote/main/a/b/c", "/remote/main/d"}, *seen)
}

func TestWorkRoots_RemoteNegatives(t *testing.T) {
	convey.Convey("远端:不指回同一主仓库或 git 判定失败 → 不认领、不报错", t, func() {
		convey.Convey("非 git 的 /tmp 文件与另一个仓库都不认领", func() {
			r := newRig(t, 7, "/remote/main")
			r.withWritten("/tmp/patch.diff", "/other/repo/b.go")
			r.expectGitState(map[string]wire.GitStateResp{
				"/remote/main": {CommonDir: "/remote/main/.git"},
				"/other/repo":  {CommonDir: "/other/repo/.git"},
			})

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			require.Len(t, roots, 1)
		})

		convey.Convey("候选目录的 gitState 失败 → 跳过它,其余照常认领", func() {
			r := newRig(t, 7, "/remote/main")
			r.withWritten("/remote/broken/a.go", "/remote/wt/b.go")
			r.expectGitStateOrErr(map[string]wire.GitStateResp{
				"/remote/main": {CommonDir: "/remote/main/.git"},
				"/remote/wt":   {CommonDir: "/remote/main/.git", Worktree: "wt"},
			}, map[string]error{"/remote/broken": errors.New("git exploded")})

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			require.Len(t, roots, 2)
			assert.Equal(t, "/remote/wt", roots[1].Path)
		})

		convey.Convey("向上找工作树根时 gitState 失败 → 这个候选不认领", func() {
			r := newRig(t, 7, "/remote/main")
			r.withWritten("/remote/wt3/pkg/a.go")
			r.expectGitStateOrErr(map[string]wire.GitStateResp{
				"/remote/main":    {CommonDir: "/remote/main/.git"},
				"/remote/wt3/pkg": {CommonDir: "/remote/main/.git", Worktree: "wt3"},
			}, map[string]error{"/remote/wt3": errors.New("connection lost")})

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			require.Len(t, roots, 1)
		})
	})
}

func TestRelativeTo(t *testing.T) {
	assert.Equal(t, "a/b.go", RelativeTo("/r", "/r/a/b.go"))
	assert.Equal(t, "a/b.go", RelativeTo("/r/", "/r/a/b.go"))
	assert.Equal(t, "/rx/a.go", RelativeTo("/r", "/rx/a.go"), "a sibling with a shared prefix is not inside")
	assert.Equal(t, "/elsewhere/a.go", RelativeTo("/r", "/elsewhere/a.go"))
	assert.Equal(t, "a.go", RelativeTo("/", "/a.go"))
}

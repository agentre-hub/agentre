package workspace_fs_svc

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
)

// withAILocation 把「本会话 AI 当前位置」这个窄端口换成固定答案。
func (r *rig) withAILocation(dir string, err error) {
	r.svc.aiLocation = func(context.Context, int64) (string, error) { return dir, err }
}

// aiHereRoots 取 roots 里标了 AIHere 的那些根的路径。
func aiHereRoots(roots []WorkRootView) []string {
	var out []string
	for _, r := range roots {
		if r.AIHere {
			out = append(out, r.Path)
		}
	}
	return out
}

// spec「工作根与 worktree 跟随」:下拉里 AI 当前所在的根标「AI 在这里」—— WorkRoots
// 据会话的 AI 当前位置标出恰好一个根(位置不在任何根内时一个都不标)。
func TestWorkRoots_MarksTheRootTheAIIsIn(t *testing.T) {
	convey.Convey("WorkRoots 标出 AI 当前所在的根", t, func() {
		main := filepath.Join(t.TempDir(), "main")
		initRepoAt(t, main)
		nested := filepath.Join(main, ".claude", "worktrees", "x")
		addWorktreeAt(t, main, nested, "nested")

		convey.Convey("AI 在嵌套 worktree 的子目录里 → 标那个 worktree(最深的根),不标主根", func() {
			r := newRig(t, 0, main)
			r.withWritten(filepath.Join(nested, "a.go"))
			r.withAILocation(filepath.Join(nested, "pkg"), nil)

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			require.Len(t, roots, 2)
			assert.Equal(t, []string{nested}, aiHereRoots(roots))
		})

		convey.Convey("没有位置记录(空串)→ 会话工作目录,标主根", func() {
			r := newRig(t, 0, main)
			r.withWritten(filepath.Join(nested, "a.go"))
			r.withAILocation("", nil)

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			assert.Equal(t, []string{main}, aiHereRoots(roots))
		})

		convey.Convey("AI 在任何根之外(/tmp)→ 一个都不标", func() {
			r := newRig(t, 0, main)
			r.withWritten()
			r.withAILocation(filepath.Join(t.TempDir(), "elsewhere"), nil)

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			assert.Empty(t, aiHereRoots(roots))
		})

		convey.Convey("位置取不到(端口报错)→ 集合照常返回,一个都不标", func() {
			r := newRig(t, 0, main)
			r.withWritten()
			r.withAILocation("", errors.New("db down"))

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			require.Len(t, roots, 1)
			assert.Empty(t, aiHereRoots(roots))
		})

		convey.Convey("远端会话:位置是执行设备上的路径,按同一规则标", func() {
			r := newRig(t, 7, "/remote/main")
			r.withWritten("/remote/main/.worktrees/y/a.go")
			r.withAILocation("/remote/main/.worktrees/y", nil)
			r.expectGitState(map[string]wire.GitStateResp{
				"/remote/main":              {CommonDir: "/remote/main/.git"},
				"/remote/main/.worktrees/y": {CommonDir: "/remote/main/.git", Worktree: "y"},
				"/remote/main/.worktrees":   {CommonDir: "/remote/main/.git"},
			})

			roots, err := r.svc.WorkRoots(r.ctx, 42)
			require.NoError(t, err)
			assert.Equal(t, []string{"/remote/main/.worktrees/y"}, aiHereRoots(roots))
		})
	})
}

// AIRootIndex 是「某个目录属于集合里哪个根」的唯一判定(chat_svc 据它判 AI 的根变没变)。
func TestAIRootIndex(t *testing.T) {
	roots := []WorkRootView{
		{Path: "/r/main", IsPrimary: true},
		{Path: "/r/main/.worktrees/x"},
		{Path: "/r/wt"},
	}
	cases := []struct {
		dir  string
		want int
	}{
		{"", 0},                       // 没有位置记录 → 主根
		{"/r/main", 0},                // 主根本身
		{"/r/main/src", 0},            // 主根之内
		{"/r/main/.worktrees/x/a", 1}, // 嵌套 worktree:最深的根
		{"/r/wt", 2},
		{"/r/wt-notes", -1}, // 按路径段比,不按裸前缀
		{"/tmp", -1},
		{"/r/main/./src/..", 0}, // 先清理再比
	}
	for _, c := range cases {
		assert.Equal(t, c.want, AIRootIndex(roots, c.dir), "dir=%q", c.dir)
	}
	assert.Equal(t, -1, AIRootIndex(nil, ""), "空集合")
}

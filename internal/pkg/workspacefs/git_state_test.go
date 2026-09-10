package workspacefs_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/workspacefs"
)

func TestGitState_HappyPath(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "ai-chat")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")

	// 制造两个 untracked 文件(dir 来自 t.TempDir, 路径可控)
	require.NoError(t, exec.Command("touch", filepath.Join(dir, "a.txt")).Run()) //nolint:gosec // G204: test, path 来自 TempDir
	require.NoError(t, exec.Command("touch", filepath.Join(dir, "b.txt")).Run()) //nolint:gosec // G204: test, path 来自 TempDir

	st := workspacefs.GitState(context.Background(), dir)
	assert.False(t, st.NotARepo)
	assert.Equal(t, "ai-chat", st.Branch)
	assert.Equal(t, 2, st.Dirty)
	assert.False(t, st.HasUpstream) // 没 push 过, 无 upstream
	assert.Empty(t, st.Worktree)
	assert.NotEmpty(t, st.CommonDir)
}

func TestGitState_NotARepo(t *testing.T) {
	dir := t.TempDir()
	st := workspacefs.GitState(context.Background(), dir)
	assert.True(t, st.NotARepo)
	assert.Empty(t, st.CommonDir)
}

func TestGitState_EmptyDir_NotARepo(t *testing.T) {
	st := workspacefs.GitState(context.Background(), "")
	assert.True(t, st.NotARepo)
}

// TestGitState_WorktreeSharesCommonDirWithMain 是本任务的核心断言:同一主仓库的
// 主 checkout 与其 attached worktree 必须共享同一个 CommonDir——下游任务(工作根
// 认领,spec 任务 2)据此判定"指回同一主仓库",而不是去比较各自互不相同的路径。
func TestGitState_WorktreeSharesCommonDirWithMain(t *testing.T) {
	main := initRepo(t)
	wt := filepath.Join(t.TempDir(), "wt-feat")
	runGit(t, main, "worktree", "add", "-b", "feat", wt)

	mainSt := workspacefs.GitState(context.Background(), main)
	wtSt := workspacefs.GitState(context.Background(), wt)

	assert.Equal(t, "feat", wtSt.Branch)
	assert.NotEmpty(t, wtSt.Worktree) // 非主 checkout → 非空
	assert.Empty(t, mainSt.Worktree)  // 主 checkout → 空
	assert.NotEmpty(t, mainSt.CommonDir)
	assert.Equal(t, mainSt.CommonDir, wtSt.CommonDir)
}

// TestGitState_AheadBehind_TracksUpstream 覆盖"领先落后"这一档:本地在已推送的
// 分支上再提交一次, 应该报 ahead=1 / behind=0 / hasUpstream=true。
func TestGitState_AheadBehind_TracksUpstream(t *testing.T) {
	bare := t.TempDir()
	runGit(t, bare, "init", "-q", "--bare")

	dir := initRepo(t)
	runGit(t, dir, "remote", "add", "origin", bare)
	runGit(t, dir, "push", "-q", "-u", "origin", "main")
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "second")

	st := workspacefs.GitState(context.Background(), dir)
	assert.False(t, st.NotARepo)
	assert.True(t, st.HasUpstream)
	assert.Equal(t, 1, st.Ahead)
	assert.Equal(t, 0, st.Behind)
}

package workspacefs_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/workspacefs"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
)

func TestGitState_HappyPath(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	dir := initRepo(t)
	runGit(t, dir, "checkout", "-q", "-b", "ai-chat")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), nil, 0o644))

	resp, err := h.GitState(context.Background(), wire.GitStateReq{Root: dir})
	require.NoError(t, err)
	assert.False(t, resp.NotARepo)
	assert.Equal(t, "ai-chat", resp.Branch)
	assert.Equal(t, 2, resp.Dirty)
	assert.False(t, resp.HasUpstream)
	assert.Empty(t, resp.Worktree)
	assert.NotEmpty(t, resp.CommonDir)
}

func TestGitState_NonRepo_Degrades(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	dir := t.TempDir()

	resp, err := h.GitState(context.Background(), wire.GitStateReq{Root: dir})
	require.NoError(t, err)
	assert.True(t, resp.NotARepo)
	assert.Empty(t, resp.Branch)
	assert.Empty(t, resp.CommonDir)
}

func TestGitState_InvalidRoot(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})

	_, err := h.GitState(context.Background(), wire.GitStateReq{Root: ""})
	assert.ErrorIs(t, err, rpcerror.ErrInvalidParams)

	_, err = h.GitState(context.Background(), wire.GitStateReq{Root: "relative/path"})
	assert.ErrorIs(t, err, rpcerror.ErrInvalidParams)
}

// TestGitState_WorktreeSharesCommonDirWithMain 是本任务(远端 git 状态)的核心
// 断言:同一主仓库的主 checkout 与其 attached worktree 走 daemon handler 各自
// 查询,必须报回同一个 CommonDir——下游任务(工作根认领)据此判定"指回同一主
// 仓库",这条判定必须跨得过 daemon RPC 边界。
func TestGitState_WorktreeSharesCommonDirWithMain(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	main := initRepo(t)
	wt := filepath.Join(t.TempDir(), "wt-feat")
	runGit(t, main, "worktree", "add", "-b", "feat", wt)

	mainResp, err := h.GitState(context.Background(), wire.GitStateReq{Root: main})
	require.NoError(t, err)
	wtResp, err := h.GitState(context.Background(), wire.GitStateReq{Root: wt})
	require.NoError(t, err)

	assert.Equal(t, "feat", wtResp.Branch)
	assert.NotEmpty(t, wtResp.Worktree)
	assert.Empty(t, mainResp.Worktree)
	assert.NotEmpty(t, mainResp.CommonDir)
	assert.Equal(t, mainResp.CommonDir, wtResp.CommonDir)
}

func TestGitState_AheadBehind_TracksUpstream(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	bare := t.TempDir()
	runGit(t, bare, "init", "-q", "--bare")

	dir := initRepo(t)
	runGit(t, dir, "remote", "add", "origin", bare)
	runGit(t, dir, "push", "-q", "-u", "origin", "main")
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "second")

	resp, err := h.GitState(context.Background(), wire.GitStateReq{Root: dir})
	require.NoError(t, err)
	assert.True(t, resp.HasUpstream)
	assert.Equal(t, 1, resp.Ahead)
	assert.Equal(t, 0, resp.Behind)
}

package workspacefs_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/workspacefs"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
)

// runGit 在 dir 下执行 git args。测试 helper,失败直接 t.Fatal。与
// internal/pkg/workspacefs 测试包的同名 helper 保持同一形态(prior art:
// internal/service/chat_svc/git_state_test.go:16-24)。
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // G204: test helper, args 来自测试内常量
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")
	return dir
}

func TestGitChanges_Uncommitted_Happy(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	dir := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o644))
	runGit(t, dir, "add", "a.txt")
	runGit(t, dir, "commit", "-q", "-m", "seed")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\ny\n"), 0o644))

	resp, err := h.GitChanges(context.Background(), wire.GitChangesReq{Root: dir, Scope: wire.ScopeUncommitted})
	require.NoError(t, err)
	assert.False(t, resp.NotARepo)
	require.Len(t, resp.Changes, 1)
	assert.Equal(t, "a.txt", resp.Changes[0].Path)
	assert.Equal(t, "modified", resp.Changes[0].Status)
	assert.Equal(t, 1, resp.Changes[0].Added)
}

func TestGitChanges_BranchScope_RequiresBaseRef(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	dir := initRepo(t)

	_, err := h.GitChanges(context.Background(), wire.GitChangesReq{Root: dir, Scope: wire.ScopeBranch})
	require.Error(t, err)
	assert.True(t, errors.Is(err, wire.ErrBaselineRequired))
}

func TestGitChanges_BranchScope_Happy(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	dir := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644))
	runGit(t, dir, "add", "b.txt")
	runGit(t, dir, "commit", "-q", "-m", "on main")

	resp, err := h.GitChanges(context.Background(), wire.GitChangesReq{Root: dir, Scope: wire.ScopeBranch, BaseRef: "main"})
	require.NoError(t, err)
	assert.False(t, resp.NotARepo)
	assert.Empty(t, resp.Changes, "baseline == HEAD: no diff yet")
}

func TestGitChanges_UnknownScope_InvalidParams(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	dir := initRepo(t)

	_, err := h.GitChanges(context.Background(), wire.GitChangesReq{Root: dir, Scope: "bogus"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, rpcerror.ErrInvalidParams))
}

func TestGitChanges_NonRepo_Degrades(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	dir := t.TempDir()

	resp, err := h.GitChanges(context.Background(), wire.GitChangesReq{Root: dir, Scope: wire.ScopeUncommitted})
	require.NoError(t, err)
	assert.True(t, resp.NotARepo)
}

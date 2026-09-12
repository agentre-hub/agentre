package workspacefs_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/workspacefs"
	"github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
)

func TestGitBranches_ListsLocal(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	dir := initRepo(t)
	runGit(t, dir, "branch", "feature/x")

	resp, err := h.GitBranches(context.Background(), wire.GitBranchesReq{Root: dir})
	require.NoError(t, err)
	assert.False(t, resp.NotARepo)

	names := map[string]wire.Branch{}
	for _, b := range resp.Branches {
		names[b.Name] = b
	}
	assert.Contains(t, names, "main")
	assert.False(t, names["main"].Remote)
	assert.Contains(t, names, "feature/x")
}

func TestGitBranches_NonRepo_Degrades(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	dir := t.TempDir()

	resp, err := h.GitBranches(context.Background(), wire.GitBranchesReq{Root: dir})
	require.NoError(t, err)
	assert.True(t, resp.NotARepo)
	assert.Empty(t, resp.Branches)
	assert.Empty(t, resp.CurrentBranch)
	assert.Empty(t, resp.DefaultBaseline)
}

// 当前分支与默认基线必须过得了 wire —— host 侧远端分支没有别的途径拿到它们。
func TestGitBranches_CarriesCurrentBranchAndDefaultBaseline(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	dir := initRepo(t) // 建在 main 上
	runGit(t, dir, "checkout", "-q", "-b", "feature/x")

	resp, err := h.GitBranches(context.Background(), wire.GitBranchesReq{Root: dir})
	require.NoError(t, err)
	assert.Equal(t, "feature/x", resp.CurrentBranch)
	assert.Equal(t, "main", resp.DefaultBaseline)
}

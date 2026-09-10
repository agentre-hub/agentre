package workspacefs_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/workspacefs"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
)

func TestGitFileContent_Committed_HasHead(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	dir := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("line1\nline2\n"), 0o644))
	runGit(t, dir, "add", "a.txt")
	runGit(t, dir, "commit", "-q", "-m", "seed")

	resp, err := h.GitFileContent(context.Background(), wire.GitFileContentReq{Root: dir, RelPath: "a.txt"})
	require.NoError(t, err)
	assert.False(t, resp.NotARepo)
	assert.True(t, resp.HasHead)
	assert.Equal(t, "line1\nline2\n", resp.Content)
}

func TestGitFileContent_Untracked_EmptyBaseline(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	dir := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("uncommitted"), 0o644))

	resp, err := h.GitFileContent(context.Background(), wire.GitFileContentReq{Root: dir, RelPath: "new.txt"})
	require.NoError(t, err)
	assert.False(t, resp.NotARepo)
	assert.False(t, resp.HasHead)
	assert.Empty(t, resp.Content)
}

func TestGitFileContent_NonRepo_Degrades(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	dir := t.TempDir()

	resp, err := h.GitFileContent(context.Background(), wire.GitFileContentReq{Root: dir, RelPath: "a.txt"})
	require.NoError(t, err)
	assert.True(t, resp.NotARepo)
	assert.False(t, resp.HasHead)
	assert.Empty(t, resp.Content)
}

func TestGitFileContent_RelPathEscape_PathRefused(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	dir := initRepo(t)

	_, err := h.GitFileContent(context.Background(), wire.GitFileContentReq{Root: dir, RelPath: "../etc/passwd"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, wire.ErrPathRefused))
}

func TestGitFileContent_EmptyRoot_NoCwd(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})

	_, err := h.GitFileContent(context.Background(), wire.GitFileContentReq{Root: ""})
	require.Error(t, err)
	assert.True(t, errors.Is(err, wire.ErrNoCwd))
}

func TestGitFileContent_RelativeRoot_InvalidParams(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})

	_, err := h.GitFileContent(context.Background(), wire.GitFileContentReq{Root: "relative/root", RelPath: "a.txt"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, rpcerror.ErrInvalidParams))
}

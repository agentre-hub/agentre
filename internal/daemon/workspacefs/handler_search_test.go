package workspacefs_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/workspacefs"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
)

func TestSearchFiles_Happy(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "Target.go"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "other.txt"), nil, 0o644))

	resp, err := h.SearchFiles(context.Background(), wire.SearchFilesReq{Root: root, Query: "target"})
	require.NoError(t, err)
	assert.False(t, resp.Truncated)
	require.Len(t, resp.Hits, 1)
	assert.Equal(t, "src/Target.go", resp.Hits[0].Path)
	assert.False(t, resp.Hits[0].IsDir)
}

func TestSearchFiles_EmptyRoot_NoCwd(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	_, err := h.SearchFiles(context.Background(), wire.SearchFilesReq{Root: ""})
	require.Error(t, err)
	assert.True(t, errors.Is(err, wire.ErrNoCwd))
}

func TestSearchFiles_RelativeRoot_InvalidParams(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	_, err := h.SearchFiles(context.Background(), wire.SearchFilesReq{Root: "relative/root", Query: "x"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, rpcerror.ErrInvalidParams))
}

func TestSearchFiles_TruncatedByHitCap(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{MaxSearchHits: 2})
	root := t.TempDir()
	for i := 0; i < 4; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(root, "target"+strconv.Itoa(i)), nil, 0o644))
	}

	resp, err := h.SearchFiles(context.Background(), wire.SearchFilesReq{Root: root, Query: "target"})
	require.NoError(t, err)
	assert.Len(t, resp.Hits, 2)
	assert.True(t, resp.Truncated)
}

func TestSearchFiles_TruncatedByDirBudget(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{MaxSearchDirs: 1})
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a", "b"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a", "b", "target.txt"), nil, 0o644))

	resp, err := h.SearchFiles(context.Background(), wire.SearchFilesReq{Root: root, Query: "target"})
	require.NoError(t, err)
	assert.True(t, resp.Truncated, "目录预算耗尽必须显式回报截断")
	assert.Empty(t, resp.Hits)
}

// TestRegister_SearchFiles 确认新方法真的挂在了 registry 上,且 sentinel 被翻成
// *rpcerror.Error(与既有五个方法同一条管线)。

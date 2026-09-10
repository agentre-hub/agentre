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

func TestListDir_Happy(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi"), 0o644))

	resp, err := h.ListDir(context.Background(), wire.ListDirReq{Root: root})
	require.NoError(t, err)
	assert.Equal(t, root, resp.Path)
	assert.False(t, resp.Truncated)

	names := map[string]wire.Entry{}
	for _, e := range resp.Entries {
		names[e.Name] = e
	}
	assert.True(t, names["sub"].IsDir)
	assert.False(t, names["a.txt"].IsDir)
	assert.Equal(t, int64(2), names["a.txt"].Size)
}

func TestListDir_RelPathEscape(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	root := t.TempDir()

	_, err := h.ListDir(context.Background(), wire.ListDirReq{Root: root, RelPath: "../etc"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, wire.ErrPathRefused))
}

func TestListDir_EmptyRoot_InvalidParams(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	_, err := h.ListDir(context.Background(), wire.ListDirReq{Root: ""})
	require.Error(t, err)
	assert.True(t, errors.Is(err, rpcerror.ErrInvalidParams))
}

func TestListDir_RelativeRoot_InvalidParams(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	_, err := h.ListDir(context.Background(), wire.ListDirReq{Root: "relative/root"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, rpcerror.ErrInvalidParams))
}

func TestListDir_GitDirAlwaysHidden(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))

	resp, err := h.ListDir(context.Background(), wire.ListDirReq{Root: root, IncludeIgnored: true})
	require.NoError(t, err)
	for _, e := range resp.Entries {
		assert.NotEqual(t, ".git", e.Name)
	}
}

func TestListDir_Truncated(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{MaxEntries: 3})
	root := t.TempDir()
	for i := 0; i < 5; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(root, "f"+strconv.Itoa(i)), nil, 0o644))
	}
	resp, err := h.ListDir(context.Background(), wire.ListDirReq{Root: root})
	require.NoError(t, err)
	assert.True(t, resp.Truncated)
	assert.Len(t, resp.Entries, 3)
}

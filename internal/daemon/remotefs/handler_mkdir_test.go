package remotefs_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/remotefs/wire"
)

func TestMkdir_Happy(t *testing.T) {
	h, home := setupHandlers(t)
	resp, err := h.Mkdir(context.Background(), wire.MkdirReq{Parent: home, Name: "newdir"})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "newdir"), resp.Path)
	info, err := os.Lstat(resp.Path)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestMkdir_NonAbsoluteParent(t *testing.T) {
	h, _ := setupHandlers(t)
	_, err := h.Mkdir(context.Background(), wire.MkdirReq{Parent: "rel", Name: "x"})
	assert.True(t, errors.Is(err, wire.ErrPathRefused))
}

func TestMkdir_ParentBlacklisted(t *testing.T) {
	h, _ := setupHandlers(t)
	_, err := h.Mkdir(context.Background(), wire.MkdirReq{Parent: "/proc", Name: "x"})
	assert.True(t, errors.Is(err, wire.ErrPathRefused))
}

func TestMkdir_InvalidName(t *testing.T) {
	h, home := setupHandlers(t)
	cases := []string{"", ".", "..", "a/b", " leading", "trailing ", string(make([]byte, 300))}
	for _, n := range cases {
		_, err := h.Mkdir(context.Background(), wire.MkdirReq{Parent: home, Name: n})
		assert.Truef(t, errors.Is(err, wire.ErrInvalidName), "name=%q", n)
	}
}

func TestMkdir_AlreadyExists(t *testing.T) {
	h, home := setupHandlers(t)
	require.NoError(t, os.Mkdir(filepath.Join(home, "dup"), 0o755))
	_, err := h.Mkdir(context.Background(), wire.MkdirReq{Parent: home, Name: "dup"})
	assert.True(t, errors.Is(err, wire.ErrMkdirExists))
}

func TestMkdir_ParentMissing(t *testing.T) {
	h, home := setupHandlers(t)
	_, err := h.Mkdir(context.Background(), wire.MkdirReq{Parent: filepath.Join(home, "nope"), Name: "x"})
	assert.True(t, errors.Is(err, wire.ErrNotFound))
}

func TestMkdir_PermDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only perm test")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	h, home := setupHandlers(t)
	parent := filepath.Join(home, "ro")
	require.NoError(t, os.Mkdir(parent, 0o555))
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	_, err := h.Mkdir(context.Background(), wire.MkdirReq{Parent: parent, Name: "child"})
	assert.True(t, errors.Is(err, wire.ErrPermDenied))
}

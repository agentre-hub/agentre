package workspacefs_test

import (
	"context"
	"encoding/base64"
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

func TestReadFile_TextHappy(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644))

	resp, err := h.ReadFile(context.Background(), wire.ReadFileReq{Root: root, RelPath: "a.txt"})
	require.NoError(t, err)
	assert.Equal(t, "hello", resp.Content)
	assert.Empty(t, resp.ContentType)
	assert.False(t, resp.Binary)
	assert.False(t, resp.TooLarge)
}

func TestReadFile_ImageBase64(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	root := t.TempDir()
	raw := []byte{0x89, 0x50, 0x4e, 0x47} // 任意字节,png 扩展名即可
	require.NoError(t, os.WriteFile(filepath.Join(root, "pic.png"), raw, 0o644))

	resp, err := h.ReadFile(context.Background(), wire.ReadFileReq{Root: root, RelPath: "pic.png"})
	require.NoError(t, err)
	assert.Equal(t, "image/png", resp.ContentType)
	assert.Equal(t, base64.StdEncoding.EncodeToString(raw), resp.Content)
	assert.False(t, resp.Binary)
	assert.False(t, resp.TooLarge)
}

func TestReadFile_RelPathEscape_PathRefused(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	root := t.TempDir()

	_, err := h.ReadFile(context.Background(), wire.ReadFileReq{Root: root, RelPath: "../etc/passwd"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, wire.ErrPathRefused))
}

func TestReadFile_EmptyRoot_NoCwd(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})

	_, err := h.ReadFile(context.Background(), wire.ReadFileReq{Root: ""})
	require.Error(t, err)
	assert.True(t, errors.Is(err, wire.ErrNoCwd))
}

func TestReadFile_RelativeRoot_InvalidParams(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})

	_, err := h.ReadFile(context.Background(), wire.ReadFileReq{Root: "relative/root", RelPath: "a.txt"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, rpcerror.ErrInvalidParams))
}

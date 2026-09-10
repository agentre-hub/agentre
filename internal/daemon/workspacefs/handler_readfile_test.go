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
	"github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
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

// Given 转录里的路径来自当时那次工具调用,文件之后被删掉是正常情况,
// When 读一个工作根内已经不存在的 relPath,
// Then daemon 回可分辨的 ErrNotFound —— 而不是一个笼统失败,host 侧才能把它
// 画成终态的「文件不存在」而不是给一个必然再失败的重试按钮。
func TestReadFile_Missing_NotFound(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	root := t.TempDir()

	_, err := h.ReadFile(context.Background(), wire.ReadFileReq{Root: root, RelPath: "ghost.md"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, wire.ErrNotFound))
}

// 中间目录不存在与目标文件不存在是同一件事:两者都是"这条路径在那台机器上没有"。
func TestReadFile_MissingParentDir_NotFound(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	root := t.TempDir()

	_, err := h.ReadFile(context.Background(), wire.ReadFileReq{Root: root, RelPath: "nope/ghost.md"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, wire.ErrNotFound))
}

// Given 新码不得成为"cwd 外那个文件在不在"的探测器(spec 决策 6 与硬不变量),
// When relPath 越界且越界后的目标同样不存在,
// Then 仍然只回 ErrPathRefused —— 越界判定优先,存在性一个字都不透。
func TestReadFile_EscapingMissingPath_StillPathRefused(t *testing.T) {
	h := workspacefs.NewHandlers(workspacefs.Options{})
	root := t.TempDir()

	_, err := h.ReadFile(context.Background(), wire.ReadFileReq{Root: root, RelPath: "../surely-no-such-file-9f3a2c"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, wire.ErrPathRefused))
	assert.False(t, errors.Is(err, wire.ErrNotFound))
}

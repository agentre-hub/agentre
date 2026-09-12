package workspacefs_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/workspacefs"
)

func TestReadFile_Text(t *testing.T) {
	dir := t.TempDir()
	content := "hello\nworld\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "note.md"), []byte(content), 0o644))

	res, err := workspacefs.ReadFile(context.Background(), dir, "note.md")
	require.NoError(t, err)
	assert.Equal(t, content, res.Content)
	assert.Equal(t, "", res.ContentType)
	assert.False(t, res.Binary)
	assert.False(t, res.TooLarge)
}

func TestReadFile_NestedRelPath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "a.txt"), []byte("x"), 0o644))

	res, err := workspacefs.ReadFile(context.Background(), dir, "sub/a.txt")
	require.NoError(t, err)
	assert.Equal(t, "x", res.Content)
}

func TestReadFile_EscapeRejected(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644))
	cases := []string{"..", "../a.txt", "sub/../..", "/etc/passwd", "a/../../../etc/passwd"}
	for _, rp := range cases {
		_, err := workspacefs.ReadFile(context.Background(), dir, rp)
		assert.Truef(t, errors.Is(err, workspacefs.ErrPathRefused), "relPath=%q err=%v", rp, err)
	}
}

func TestReadFile_SymlinkEscapeRejected(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "work")
	require.NoError(t, os.Mkdir(dir, 0o755))
	outside := filepath.Join(parent, "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "link.txt")))

	_, err := workspacefs.ReadFile(context.Background(), dir, "link.txt")
	assert.True(t, errors.Is(err, workspacefs.ErrPathRefused), "跟随符号链接逃出 cwd 应拒绝")
}

func TestReadFile_SymlinkInsideCwd(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "real.txt"), []byte("hi"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "link.txt")))

	res, err := workspacefs.ReadFile(context.Background(), dir, "link.txt")
	require.NoError(t, err)
	assert.Equal(t, "hi", res.Content, "cwd 内符号链接可正常跟随读取")
}

func TestReadFile_NULBinary(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "blob.bin"), []byte{0x68, 0x00, 0x69}, 0o644))

	res, err := workspacefs.ReadFile(context.Background(), dir, "blob.bin")
	require.NoError(t, err)
	assert.True(t, res.Binary)
	assert.Equal(t, "", res.Content)
	assert.False(t, res.TooLarge)
}

func TestReadFile_TextTooLarge(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte("a"), 1<<20+1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "big.txt"), data, 0o644))

	res, err := workspacefs.ReadFile(context.Background(), dir, "big.txt")
	require.NoError(t, err)
	assert.True(t, res.TooLarge)
	assert.Equal(t, "", res.Content)
	assert.False(t, res.Binary)
}

func TestReadFile_TextAtThreshold(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte("a"), 1<<20)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "edge.txt"), data, 0o644))

	res, err := workspacefs.ReadFile(context.Background(), dir, "edge.txt")
	require.NoError(t, err)
	assert.False(t, res.TooLarge)
	assert.Equal(t, string(data), res.Content)
}

func TestReadFile_Image(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		ext  string
		mime string
	}{
		{"png", ".png", "image/png"},
		{"jpg", ".jpg", "image/jpeg"},
		{"jpeg", ".jpeg", "image/jpeg"},
		{"gif", ".gif", "image/gif"},
		{"webp", ".webp", "image/webp"},
		{"avif", ".avif", "image/avif"},
		{"bmp", ".bmp", "image/bmp"},
		{"ico", ".ico", "image/x-icon"},
		{"svg", ".svg", "image/svg+xml"},
	}
	// 含 NUL 的字节:证明图片是唯一允许传输的二进制,不因含 NUL 被当作 binary。
	raw := []byte{0x00, 0x01, 0x02, 0xff}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, "img"+tc.ext)
			require.NoError(t, os.WriteFile(path, raw, 0o644))

			res, err := workspacefs.ReadFile(context.Background(), dir, "img"+tc.ext)
			require.NoError(t, err)
			assert.False(t, res.Binary)
			assert.False(t, res.TooLarge)
			assert.Equal(t, tc.mime, res.ContentType)
			assert.Equal(t, base64.StdEncoding.EncodeToString(raw), res.Content)
		})
	}
}

func TestReadFile_ImageTooLarge(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte{0xab}, 10<<20+1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "big.png"), data, 0o644))

	res, err := workspacefs.ReadFile(context.Background(), dir, "big.png")
	require.NoError(t, err)
	assert.True(t, res.TooLarge)
	assert.Equal(t, "", res.Content)
	assert.False(t, res.Binary)
}

func TestReadFile_DirectoryRejected(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))

	_, err := workspacefs.ReadFile(context.Background(), dir, "sub")
	assert.True(t, errors.Is(err, workspacefs.ErrPathRefused), "目录不是可读文件,应拒绝")
}

func TestReadFile_SymlinkToDirectoryRejected(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "real"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "linkdir")))

	_, err := workspacefs.ReadFile(context.Background(), dir, "linkdir")
	assert.True(t, errors.Is(err, workspacefs.ErrPathRefused))
}

func TestReadFile_EmptyCwdRejected(t *testing.T) {
	_, err := workspacefs.ReadFile(context.Background(), "", "a.txt")
	assert.True(t, errors.Is(err, workspacefs.ErrNoCwd), "cwd 为空应拒绝")
}

func TestReadFile_MissingFileErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := workspacefs.ReadFile(context.Background(), dir, "nope.txt")
	require.Error(t, err)
	assert.False(t, errors.Is(err, workspacefs.ErrPathRefused), "不存在的文件是读取失败,不是越界")
}

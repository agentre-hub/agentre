package pathguard_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/pkg/remotefs/pathguard"
)

func TestResolvePath(t *testing.T) {
	homeFn := func() (string, error) { return "/home/me", nil }
	convey.Convey("ResolvePath", t, func() {
		convey.Convey("空 path → home", func() {
			got, err := pathguard.ResolvePath("", homeFn)
			assert.NoError(t, err)
			assert.Equal(t, "/home/me", got)
		})
		convey.Convey("绝对路径 happy", func() {
			got, err := pathguard.ResolvePath("/home/me/Work", homeFn)
			assert.NoError(t, err)
			assert.Equal(t, "/home/me/Work", got)
		})
		convey.Convey("Clean 折叠 //a/./b/c/..", func() {
			got, err := pathguard.ResolvePath("//a/./b/c/..", homeFn)
			assert.NoError(t, err)
			assert.Equal(t, "/a/b", got)
		})
		convey.Convey("非绝对路径 → ErrPathRefused", func() {
			_, err := pathguard.ResolvePath("home/me", homeFn)
			assert.True(t, errors.Is(err, pathguard.ErrPathRefused))
		})
		convey.Convey("根处 .. 深度超界 → ErrPathRefused", func() {
			_, err := pathguard.ResolvePath("/../etc", homeFn)
			assert.True(t, errors.Is(err, pathguard.ErrPathRefused))
		})
		convey.Convey("/a/.. → Clean 到根 / → 允许", func() {
			got, err := pathguard.ResolvePath("/a/..", homeFn)
			assert.NoError(t, err)
			assert.Equal(t, "/", got)
		})
		convey.Convey("黑名单根 /proc → ErrPathRefused", func() {
			_, err := pathguard.ResolvePath("/proc", homeFn)
			assert.True(t, errors.Is(err, pathguard.ErrPathRefused))
		})
		convey.Convey("黑名单子路径 /sys/fs → ErrPathRefused", func() {
			_, err := pathguard.ResolvePath("/sys/fs", homeFn)
			assert.True(t, errors.Is(err, pathguard.ErrPathRefused))
		})
		convey.Convey("/devious 不黑（不是 /dev 的子路径）", func() {
			got, err := pathguard.ResolvePath("/devious", homeFn)
			assert.NoError(t, err)
			assert.Equal(t, "/devious", got)
		})
		convey.Convey("homeFn 报错 → 透传", func() {
			_, err := pathguard.ResolvePath("", func() (string, error) {
				return "", errors.New("no home")
			})
			assert.Error(t, err)
		})
	})
}

func TestValidateName(t *testing.T) {
	convey.Convey("ValidateName", t, func() {
		convey.Convey("happy", func() {
			assert.NoError(t, pathguard.ValidateName("new-folder"))
			assert.NoError(t, pathguard.ValidateName(".hidden"))
			assert.NoError(t, pathguard.ValidateName("中文目录"))
		})
		convey.Convey("空 → ErrInvalidName", func() {
			assert.True(t, errors.Is(pathguard.ValidateName(""), pathguard.ErrInvalidName))
		})
		convey.Convey("含 / → ErrInvalidName", func() {
			assert.True(t, errors.Is(pathguard.ValidateName("a/b"), pathguard.ErrInvalidName))
		})
		convey.Convey("等于 .. → ErrInvalidName", func() {
			assert.True(t, errors.Is(pathguard.ValidateName(".."), pathguard.ErrInvalidName))
		})
		convey.Convey("等于 . → ErrInvalidName", func() {
			assert.True(t, errors.Is(pathguard.ValidateName("."), pathguard.ErrInvalidName))
		})
		convey.Convey("首尾空白 → ErrInvalidName", func() {
			assert.True(t, errors.Is(pathguard.ValidateName(" foo"), pathguard.ErrInvalidName))
			assert.True(t, errors.Is(pathguard.ValidateName("foo "), pathguard.ErrInvalidName))
		})
		convey.Convey("长度 >255 → ErrInvalidName", func() {
			long := strings.Repeat("x", 256)
			assert.True(t, errors.Is(pathguard.ValidateName(long), pathguard.ErrInvalidName))
		})
	})
}

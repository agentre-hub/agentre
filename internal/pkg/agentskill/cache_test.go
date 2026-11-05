package agentskill

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRegistryStamp_ChangesWhenPluginRegistryChanges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	reg := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	assert.NoError(t, os.MkdirAll(filepath.Dir(reg), 0o755))

	missing := RegistryStamp()
	assert.NoError(t, os.WriteFile(reg, []byte("{}"), 0o600))
	created := RegistryStamp()
	assert.NotEqual(t, missing, created, "新建注册文件应改变指纹")

	later := time.Now().Add(time.Hour)
	assert.NoError(t, os.Chtimes(reg, later, later))
	assert.NotEqual(t, created, RegistryStamp(), "mtime 变化应改变指纹")
}

func TestCachedDiscovery_NilCachePassesThroughAndErrorsNotCached(t *testing.T) {
	n := 0
	load := func() (int, error) { n++; return n, nil }
	v, err := CachedDiscovery[int](nil, "k", "", false, load)
	assert.NoError(t, err)
	assert.Equal(t, 1, v)

	c := NewDiscoveryCache()
	boom := errors.New("boom")
	_, err = CachedDiscovery(c, "k", "", false, func() (int, error) { return 0, boom })
	assert.ErrorIs(t, err, boom)
	v, err = CachedDiscovery(c, "k", "", false, load)
	assert.NoError(t, err)
	assert.Equal(t, 2, v, "失败后下一次必须重新加载")
}

// Given 一次加载还在跑(读的是失效之前的世界)
// When  手动刷新让这个作用域失效,在途的加载随后才完成
// Then  失效之后的请求拿到重新加载的结果:在途那次不能把旧结果写回缓存,失效之后到达的
//
//	请求也不能去搭它的车。
func TestCachedDiscovery_GivenInvalidationDuringLoad_ThenLaterCallersGetAFreshLoad(t *testing.T) {
	c := NewDiscoveryCache()
	entered, release := make(chan struct{}), make(chan struct{})
	staleDone := make(chan string, 1)
	go func() {
		v, _ := CachedDiscovery(c, "local|claude|cmds", "", false, func() (string, error) {
			close(entered)
			<-release
			return "old", nil
		})
		staleDone <- v
	}()
	<-entered

	c.InvalidatePrefix("local|claude|")
	fresh := make(chan string, 1)
	go func() {
		v, _ := CachedDiscovery(c, "local|claude|cmds", "", false, func() (string, error) { return "new", nil })
		fresh <- v
	}()
	var got string
	select {
	case got = <-fresh: // 没去搭在途那次的车
		close(release)
	case <-time.After(500 * time.Millisecond): // 搭上了:放行在途那次,拿它的结果
		close(release)
		got = <-fresh
	}
	assert.Equal(t, "new", got, "失效之后的请求不能搭失效之前那次加载的车")
	assert.Equal(t, "old", <-staleDone, "在途那次的调用方照旧拿到它自己的结果")

	v, err := CachedDiscovery(c, "local|claude|cmds", "", false, func() (string, error) { return "reloaded", nil })
	assert.NoError(t, err)
	assert.Equal(t, "new", v, "在途那次完成后不能把旧结果写回缓存")
}

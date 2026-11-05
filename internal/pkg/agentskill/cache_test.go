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

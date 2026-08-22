package agentruntime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSession 用 channel 同步 Close 调用，避免 LRU 异步 close 的 race。
type fakeSession struct {
	mu     sync.Mutex
	id     string
	closed bool
	done   chan struct{}
}

func newFakeSession(id string) *fakeSession {
	return &fakeSession{id: id, done: make(chan struct{})}
}

func (f *fakeSession) Close(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.done)
	}
	return nil
}

func (f *fakeSession) IsClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fakeSession) WaitClosed(t *testing.T) {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(2 * time.Second):
		t.Fatalf("session %q: Close 没在 2s 内被调用", f.id)
	}
}

func TestSessionCache_LRUEviction(t *testing.T) {
	c := NewSessionCache(2)
	a := newFakeSession("a")
	b := newFakeSession("b")
	c.Put("a", a)
	c.Put("b", b)

	t.Run("size 上限内不 evict", func(t *testing.T) {
		assert.False(t, a.IsClosed())
		assert.False(t, b.IsClosed())
	})

	t.Run("超出时 evict 最老的", func(t *testing.T) {
		x := newFakeSession("c")
		c.Put("c", x)
		a.WaitClosed(t)
		assert.True(t, a.IsClosed(), "a 是最老的，应被 Close")
		assert.False(t, b.IsClosed())
		assert.False(t, x.IsClosed())
	})

	t.Run("Get 把 key 提到最新位置", func(t *testing.T) {
		_, ok := c.Get("b")
		require.True(t, ok)
		d := newFakeSession("d")
		c.Put("d", d)
		// 此时 cache = [d (front), b, c → evicted? 不，cap=2 触发 evict c]。Wait — cap=2 上面已超额。
		// 注：上一步把 c 放进去后 cache=[c,b]，b 是最老 → 现在 Get(b) 把 b 提到前面：cache=[b,c]。
		// 加 d 触发 evict → c 出栈。
		assert.False(t, b.IsClosed(), "b 刚被 Get 提到最新，不该被 evict")
	})
}

func TestSessionCache_Remove(t *testing.T) {
	c := NewSessionCache(2)
	a := newFakeSession("a")
	c.Put("a", a)
	c.Remove("a")
	a.WaitClosed(t)
	assert.True(t, a.IsClosed())
	_, ok := c.Get("a")
	assert.False(t, ok)
}

func TestSessionCache_RemoveAll(t *testing.T) {
	c := NewSessionCache(4)
	a := newFakeSession("a")
	b := newFakeSession("b")
	d := newFakeSession("d")
	c.Put("a", a)
	c.Put("b", b)
	c.Put("d", d)
	c.RemoveAll()
	a.WaitClosed(t)
	b.WaitClosed(t)
	d.WaitClosed(t)
	assert.Equal(t, 0, c.Len(), "RemoveAll 后 cache 应为空")
	_, ok := c.Get("a")
	assert.False(t, ok, "RemoveAll 后任何 key 都不该命中")
}

func TestSessionCache_PutReplacesAndClosesOld(t *testing.T) {
	c := NewSessionCache(4)
	a1 := newFakeSession("a1")
	a2 := newFakeSession("a2")
	c.Put("a", a1)
	c.Put("a", a2)
	a1.WaitClosed(t)
	assert.True(t, a1.IsClosed(), "替换 key 应关闭旧的")
	assert.False(t, a2.IsClosed())
}

func TestCLISessionPool_PrunesOnlyIdleSessions(t *testing.T) {
	t.Run("Given more than cap idle sessions, when MarkIdle prunes, then oldest idle sessions close", func(t *testing.T) {
		p := NewCLISessionPool(2)
		a := newFakeSession("a")
		b := newFakeSession("b")
		c := newFakeSession("c")

		p.Put("claudecode:1", a)
		p.MarkIdle("claudecode:1")
		p.Put("codex:2", b)
		p.MarkIdle("codex:2")
		p.Put("claudecode:3", c)
		p.MarkIdle("claudecode:3")

		a.WaitClosed(t)
		assert.True(t, a.IsClosed(), "oldest idle CLI session should be closed")
		assert.False(t, b.IsClosed())
		assert.False(t, c.IsClosed())
		assert.Equal(t, 2, p.Len())
		assert.Equal(t, 2, p.IdleLen())
	})

	t.Run("Given all sessions are active or waiting, when pool exceeds cap, then no session closes", func(t *testing.T) {
		p := NewCLISessionPool(1)
		active := newFakeSession("active")
		waiting := newFakeSession("waiting")

		p.Put("claudecode:active", active)
		p.MarkActive("claudecode:active")
		p.Put("codex:waiting", waiting)
		p.MarkWaiting("codex:waiting")

		time.Sleep(50 * time.Millisecond)
		assert.False(t, active.IsClosed())
		assert.False(t, waiting.IsClosed())
		assert.Equal(t, 2, p.Len())
		assert.Equal(t, 0, p.IdleLen())
	})

	t.Run("Given active sessions exist and idle sessions exceed cap, when pruning, then only oldest idle closes", func(t *testing.T) {
		p := NewCLISessionPool(1)
		active := newFakeSession("active")
		oldIdle := newFakeSession("old-idle")
		newIdle := newFakeSession("new-idle")

		p.Put("claudecode:active", active)
		p.MarkActive("claudecode:active")
		p.Put("codex:old-idle", oldIdle)
		p.MarkIdle("codex:old-idle")
		p.Put("codex:new-idle", newIdle)
		p.MarkIdle("codex:new-idle")

		oldIdle.WaitClosed(t)
		assert.False(t, active.IsClosed())
		assert.True(t, oldIdle.IsClosed())
		assert.False(t, newIdle.IsClosed())
		assert.Equal(t, 2, p.Len())
	})
}

func TestRunnerCache_GetOrCreate(t *testing.T) {
	c := NewRunnerCache()
	r1 := newFakeSession("r1")
	got, err := c.GetOrCreate(7, 100, func() (ctxCloser, error) { return r1, nil })
	require.NoError(t, err)
	assert.Same(t, r1, got.(*fakeSession))

	// 同 updatetime → 复用，不调 build。
	called := false
	got2, err := c.GetOrCreate(7, 100, func() (ctxCloser, error) {
		called = true
		return newFakeSession("never"), nil
	})
	require.NoError(t, err)
	assert.Same(t, r1, got2.(*fakeSession))
	assert.False(t, called)

	// updatetime 变 → 关旧建新。
	r2 := newFakeSession("r2")
	got3, err := c.GetOrCreate(7, 101, func() (ctxCloser, error) { return r2, nil })
	require.NoError(t, err)
	assert.Same(t, r2, got3.(*fakeSession))
	r1.WaitClosed(t)
	assert.True(t, r1.IsClosed())
}

func TestRunnerCache_Drop(t *testing.T) {
	c := NewRunnerCache()
	r := newFakeSession("r")
	_, err := c.GetOrCreate(7, 100, func() (ctxCloser, error) { return r, nil })
	require.NoError(t, err)
	c.Drop(7)
	r.WaitClosed(t)
	assert.True(t, r.IsClosed())
}

// wedgedSession 模拟「优雅关闭救不回来」的子进程:claudecode.Session.Close 是
// 「关 stdin → 等子进程退出」,CLI 卡在 MCP 初始化、根本不读 stdin 时它永不返回。
// 只有硬杀(整组 SIGKILL)能让它收尾 —— Kill 之后 Close 才放行。
type wedgedSession struct {
	killed   chan struct{}
	closed   chan struct{}
	killOnce sync.Once
}

func newWedgedSession() *wedgedSession {
	return &wedgedSession{killed: make(chan struct{}), closed: make(chan struct{})}
}

func (w *wedgedSession) Close(_ context.Context) error {
	<-w.killed
	close(w.closed)
	return nil
}

func (w *wedgedSession) Kill(_ context.Context) error {
	w.killOnce.Do(func() { close(w.killed) })
	return nil
}

// Given 一个优雅关闭永不返回的会话, When 池把它 evict 掉, Then 宽限期一过就升级到
// 硬杀,进程不会被永久留下。
//
// 回归的是原来的 closeWithTimeout:名字里写着 timeout,实现却是
// Close(context.Background()) —— 卡死的 CLI 会让那个 goroutine 和它的子进程一起
// 永远留着,而 Kill() 就在旁边从没被用过。
func TestCloseWithTimeout_GivenCloseNeverReturns_WhenGraceExpires_ThenSessionIsKilled(t *testing.T) {
	restore := setCloseGraceForTest(20 * time.Millisecond)
	defer restore()

	p := NewCLISessionPool(8)
	w := newWedgedSession()
	p.Put("wedged", w)

	p.Remove("wedged")

	select {
	case <-w.killed:
	case <-time.After(2 * time.Second):
		t.Fatal("宽限期过后没有升级到硬杀:卡死的子进程会被永久留下")
	}
	select {
	case <-w.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("硬杀之后 Close 仍未收尾")
	}
}

// Given 一个正常关闭的会话, When 池把它 evict 掉, Then 不该被硬杀。
func TestCloseWithTimeout_GivenCloseReturnsPromptly_WhenEvicted_ThenSessionIsNotKilled(t *testing.T) {
	restore := setCloseGraceForTest(500 * time.Millisecond)
	defer restore()

	p := NewCLISessionPool(8)
	s := newKillableSession()
	p.Put("healthy", s)

	p.Remove("healthy")

	s.WaitClosed(t)
	time.Sleep(50 * time.Millisecond)
	assert.False(t, s.WasKilled(), "正常关闭的会话不该被硬杀")
}

type killableSession struct {
	*fakeSession
	mu     sync.Mutex
	killed bool
}

func newKillableSession() *killableSession {
	return &killableSession{fakeSession: newFakeSession("killable")}
}

func (k *killableSession) Kill(_ context.Context) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.killed = true
	return nil
}

func (k *killableSession) WasKilled() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.killed
}

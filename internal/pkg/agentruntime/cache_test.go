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

// unkillableSession 的 Close 永不返回,而且不实现硬杀口 —— 关机不能被这种条目挂住。
type unkillableSession struct{}

func (unkillableSession) Close(context.Context) error {
	select {}
}

// Given 池里既有正常会话也有卡死会话, When 关机路径调 CloseAll, Then 它要等到每个
// 条目真的收尾(卡死的那个经硬杀)之后才返回,池清空。
//
// RemoveAll 是 fire-and-forget 的:调用方拿不到任何「收干净了」的保证,宿主进程紧接着
// 退出时那些 goroutine 连同子进程一起被留下。关机路径要的是保证。
func TestCloseAll_GivenWedgedAndHealthySessions_WhenClosingAll_ThenItWaitsForBoth(t *testing.T) {
	restore := setCloseGraceForTest(20 * time.Millisecond)
	defer restore()

	p := NewCLISessionPool(8)
	healthy := newFakeSession("healthy")
	wedged := newWedgedSession()
	p.Put("healthy", healthy)
	p.Put("wedged", wedged)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, p.CloseAll(ctx))

	assert.True(t, healthy.IsClosed(), "CloseAll 返回时正常会话应当已经关掉")
	select {
	case <-wedged.closed:
	default:
		t.Fatal("CloseAll 返回时卡死会话还没收尾")
	}
	assert.Equal(t, 0, p.Len())
}

// Given 一个连硬杀都救不回来的会话, When CloseAll 的 ctx 到期, Then 它如实返回
// ctx 错误,而不是无限期挂住关机。
func TestCloseAll_GivenSessionThatNeverSettles_WhenContextExpires_ThenItReportsTheDeadline(t *testing.T) {
	restore := setCloseGraceForTest(10 * time.Millisecond)
	defer restore()

	p := NewCLISessionPool(8)
	p.Put("stuck", &unkillableSession{}) // 不认领硬杀口,Close 永不返回

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := p.CloseAll(ctx)

	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// Given 一个优雅关闭救不回来的会话和一个短上界, When CloseAll 的 ctx 到期, Then 还没
// 收尾的条目要在返回前被硬杀,而不是被留在外面。
//
// 上界到了就撒手不管等于把「关机有上界」变成「关机会漏进程」:宿主进程紧接着退出,
// 那些子进程自带进程组,不会被连坐。
func TestCloseAll_GivenContextExpiresBeforeGrace_WhenClosing_ThenOutstandingSessionsAreKilled(t *testing.T) {
	restore := setCloseGraceForTest(10 * time.Second) // 宽限期远大于 ctx:只能靠 ctx 那一路兜底
	defer restore()

	p := NewCLISessionPool(8)
	wedged := newWedgedSession()
	p.Put("wedged", wedged)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := p.CloseAll(ctx)

	assert.ErrorIs(t, err, context.DeadlineExceeded)
	select {
	case <-wedged.killed:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 到期后没有硬杀掉还没收尾的会话:它会被留成孤儿")
	}
}

// Given 池里有常驻会话, When 宿主要立刻退出而调 KillAll, Then 每个条目当场被硬杀、
// 池清空,且调用方不必等任何一个 Close 返回。
//
// 桌面端「确认退出」这条路上 Shutdown 必须在 100ms 内返回(见 app_quit_test),优雅
// 关闭放在异步 goroutine 里等于进程先退、子进程被留下 —— 那条路要的就是这一刀。
func TestKillAll_GivenPooledSessions_WhenHostExitsNow_ThenEverySessionIsKilledSynchronously(t *testing.T) {
	p := NewCLISessionPool(8)
	wedged := newWedgedSession()
	p.Put("wedged", wedged)

	p.KillAll()

	select {
	case <-wedged.killed:
	default:
		t.Fatal("KillAll 返回时会话还没被硬杀")
	}
	assert.Equal(t, 0, p.Len())
}

// Given 一个已经闲置超过存活期的会话, When 清扫跑过, Then 它被关掉并移出池。
//
// 池此前只有条数上限(8 条 idle),没有时间上限:一个开过一次就再没碰过的会话会把
// claude / codex 子进程连同它的 MCP server 一直挂着 —— 只要 idle 条数不到 8,它可以
// 活到宿主退出为止。
func TestPruneIdleOlderThan_GivenIdleSessionPastItsTTL_WhenSweeping_ThenItIsReleased(t *testing.T) {
	p := NewCLISessionPool(8)
	s := newFakeSession("stale")
	p.Put("stale", s)
	p.MarkIdle("stale")

	assert.Equal(t, 1, p.PruneIdleOlderThan(0))

	s.WaitClosed(t)
	assert.Equal(t, 0, p.Len())
}

// Given 正在跑一轮(active)和正在等审批(waiting)的会话, When 清扫跑过, Then 它们
// 一个都不能动 —— 按时间清扫绝不能把正忙的子进程杀掉。
func TestPruneIdleOlderThan_GivenBusySessions_WhenSweeping_ThenTheyAreUntouched(t *testing.T) {
	p := NewCLISessionPool(8)
	active := newFakeSession("active")
	waiting := newFakeSession("waiting")
	p.Put("active", active)
	p.Put("waiting", waiting)
	p.MarkWaiting("waiting")

	assert.Equal(t, 0, p.PruneIdleOlderThan(0))

	assert.False(t, active.IsClosed())
	assert.False(t, waiting.IsClosed())
	assert.Equal(t, 2, p.Len())
}

// Given 一个刚刚转入 idle 的会话, When 清扫按正常存活期跑过, Then 它留着 —— 跨轮复用
// 正是池存在的理由。
func TestPruneIdleOlderThan_GivenRecentlyIdledSession_WhenSweeping_ThenItStaysForReuse(t *testing.T) {
	p := NewCLISessionPool(8)
	s := newFakeSession("fresh")
	p.Put("fresh", s)
	p.MarkIdle("fresh")

	assert.Equal(t, 0, p.PruneIdleOlderThan(time.Hour))

	assert.False(t, s.IsClosed())
	assert.Equal(t, 1, p.Len())
}

// Given 池里同时有正在跑的和已经闲下来的会话, When 取快照, Then 每条都带上会话键、
// 状态与闲置起点,顺序是 LRU 的新到旧。
//
// 排查「机器上怎么多了一堆 claude 进程」「这个会话怎么卡住了」此前只能翻日志:池
// 本身对外一个字都不说,连「现在有几个」都要靠 Len 猜。
func TestSnapshot_GivenMixedStates_WhenObserving_ThenEachEntryIsDescribed(t *testing.T) {
	p := NewCLISessionPool(8)
	p.Put("claudecode:1", newFakeSession("one"))
	p.Put("codex:2", newFakeSession("two"))
	p.MarkIdle("claudecode:1")

	got := p.Snapshot()

	require.Len(t, got, 2)
	assert.Equal(t, "claudecode:1", got[0].Key, "最近动过的排在最前")
	assert.Equal(t, CLISessionIdle, got[0].State)
	assert.False(t, got[0].IdleSince.IsZero(), "idle 条目要能看出闲了多久")
	assert.Equal(t, "codex:2", got[1].Key)
	assert.Equal(t, CLISessionActive, got[1].State)
	assert.True(t, got[1].IdleSince.IsZero(), "非 idle 条目没有闲置起点")
}

// 边界:空池交回空快照,而不是 nil 之外的什么惊喜。
func TestSnapshot_GivenEmptyPool_WhenObserving_ThenItIsEmpty(t *testing.T) {
	assert.Empty(t, NewCLISessionPool(8).Snapshot())
}

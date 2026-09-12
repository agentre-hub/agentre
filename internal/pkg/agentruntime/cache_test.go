package agentruntime

import (
	"context"
	"sync"
	"sync/atomic"
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

// busyFakeSession 是一条会自报「此刻在不在跑」的会话 —— 生产里的 claudeActive
// 在带外轮(自主续轮 / 后台 subagent 活动轮)期间就是这个样子:池里的状态还停在
// idle(上一个用户轮结束时标的),子进程却正忙。
type busyFakeSession struct {
	*fakeSession
	busy atomic.Bool
}

func newBusyFakeSession(id string) *busyFakeSession {
	s := &busyFakeSession{fakeSession: newFakeSession(id)}
	s.busy.Store(true)
	return s
}

func (b *busyFakeSession) Busy() bool { return b.busy.Load() }

// Given 一条状态为 idle、闲置时刻早已过期,但自报正忙的会话(带外轮在飞),
// When 按时间清扫跑过, Then 它一个都不能动。
//
// 钉死 sess-3244:自主续轮不走 acquireSession,池里没人替它 MarkActive,条目整轮
// 都是 idle 且 idleFor 从上一个用户轮结束时刻接着涨。15 分钟到点时清扫把 stdin 关了,
// 子进程继续跑完(甚至提交了代码),但宿主再也收不到一帧 —— 界面永远停在最后一个
// 没有 tool_result 的 tool_use,还不报错。
func TestPruneIdleOlderThan_GivenIdleEntryThatReportsItselfBusy_WhenSweeping_ThenItIsKept(t *testing.T) {
	p := NewCLISessionPool(8)
	s := newBusyFakeSession("autonomous-turn-in-flight")
	p.Put("claudecode:3244", s)
	p.MarkIdle("claudecode:3244")

	assert.Equal(t, 0, p.PruneIdleOlderThan(0))
	assert.False(t, s.IsClosed(), "带外轮在飞的会话不该被按时间清扫释放")
	assert.Equal(t, 1, p.Len())

	// 带外轮收尾后它才重新变成一条普通的过期 idle 会话。
	s.busy.Store(false)
	assert.Equal(t, 1, p.PruneIdleOlderThan(0))
	s.WaitClosed(t)
}

// Given 空闲条数超过上限、且最老的那条自报正忙, When 按上限清扫, Then 逐出的是
// 下一条真正闲着的,正忙的那条留下。
func TestMarkIdle_GivenOldestIdleEntryIsBusy_WhenPruningToCap_ThenTheIdleOneIsEvicted(t *testing.T) {
	p := NewCLISessionPool(1)
	busy := newBusyFakeSession("busy")
	free := newFakeSession("free")

	p.Put("claudecode:busy", busy)
	p.MarkIdle("claudecode:busy")
	p.Put("claudecode:free", free)
	p.MarkIdle("claudecode:free")

	free.WaitClosed(t)
	assert.False(t, busy.IsClosed(), "带外轮在飞的会话不该被按上限逐出")
	assert.Equal(t, 1, p.Len())
}

// Given 一条闲置时刻早已过期、期间一直有带外轮在飞的会话, When 带外轮收尾后清扫再跑过,
// Then 它必须留下 —— 闲置计时从带外轮结束那一刻重新起算。
//
// 钉死 sess-3321:自报忙只在带外轮**在飞时**豁免,`idleAt` 从头到尾没人重新打点,于是
// 自主续轮一收尾,条目立刻带着「从上一个用户轮算起」的超龄闲置时长落进下一次 tick 的
// 射程 —— 实测自主续轮 11:48:16–11:49:57 跑完,11:50:16 那次清扫直接把它回收了,那轮里
// 刚起的后台任务的完成通知就此没了着落。
func TestPruneIdleOlderThan_GivenBusyEntryThatJustFinished_WhenSweeping_ThenItsIdleClockRestarted(t *testing.T) {
	const ttl = 200 * time.Millisecond
	p := NewCLISessionPool(8)
	s := newBusyFakeSession("out-of-band-turn")
	p.Put("claudecode:3321", s)
	p.MarkIdle("claudecode:3321")

	time.Sleep(ttl + 50*time.Millisecond) // 上一个用户轮结束后已经超过 TTL

	assert.Equal(t, 0, p.PruneIdleOlderThan(ttl), "带外轮在飞:豁免")
	assert.False(t, p.Snapshot()[0].IdleSince.IsZero())

	s.busy.Store(false) // 带外轮收尾
	assert.Equal(t, 0, p.PruneIdleOlderThan(ttl), "刚收尾的会话闲置计时应从这一刻重新起算")
	assert.False(t, s.IsClosed())
	assert.Equal(t, 1, p.Len())
}

// Given 一条带启动身份的池内会话, When 下一轮报上同一个身份, Then 复用它。
//
// 「启动身份变了就该重开」此前被三个后端各写一遍(claudecode 四段内联比较、codex 一张
// 带 512 FIFO 上限的旁路表、piagent 一张无上限旁路表),其中两份对「这个池键没记录过」
// 给出相反答案。规则收在池里之后,三家只提供各自的身份串。
func TestGetWithIdentity_GivenSameIdentity_WhenGetting_ThenTheEntryIsReused(t *testing.T) {
	p := NewCLISessionPool(8)
	s := newFakeSession("same")
	p.PutWithIdentity("claudecode:1", "model-a|provider-a", s)

	got, ok := p.GetWithIdentity("claudecode:1", "model-a|provider-a")

	require.True(t, ok, "身份未变必须复用,不能白起一个进程")
	assert.Same(t, s, got)
	assert.False(t, s.IsClosed())
	assert.Equal(t, 1, p.Len())
}

// Given 一条带启动身份的池内会话, When 下一轮的身份不同, Then 驱逐并关掉它。
func TestGetWithIdentity_GivenChangedIdentity_WhenGetting_ThenTheEntryIsEvicted(t *testing.T) {
	p := NewCLISessionPool(8)
	s := newFakeSession("stale")
	p.PutWithIdentity("claudecode:1", "model-a|provider-a", s)

	got, ok := p.GetWithIdentity("claudecode:1", "model-b|provider-a")

	assert.False(t, ok, "身份变了必须重开,复用的是拿旧参数起来的进程")
	assert.Nil(t, got)
	s.WaitClosed(t)
	assert.Equal(t, 0, p.Len(), "被驱逐的条目要从池里摘掉")
}

// Given 一条没有记录过启动身份的池内会话, When 下一轮报上一个身份, Then 按「已变」
// 处理:驱逐重开。
//
// 这是两种相反语义的收口(spec 决策 4):误判为变化的代价是多起一次进程,误判为未变化
// 的代价是整轮跑在上一个供应商/模型上而无人发觉。
func TestGetWithIdentity_GivenUnrecordedIdentity_WhenGetting_ThenItCountsAsChanged(t *testing.T) {
	p := NewCLISessionPool(8)
	s := newFakeSession("unrecorded")
	p.Put("claudecode:1", s) // 没有身份的老路径:池键存在,身份未记录

	got, ok := p.GetWithIdentity("claudecode:1", "model-a|provider-a")

	assert.False(t, ok, "未记录过的池键按已变处理")
	assert.Nil(t, got)
	s.WaitClosed(t)
	assert.Equal(t, 0, p.Len())
}

// Given 一条带身份的会话被池淘汰后同一个池键又放进新条目, When 用老身份来取,
// Then 取不到 —— 身份跟着条目一起消失,不残留在任何旁路表里。
//
// codex 的 512 条 FIFO 上限正是为绕开「池自行淘汰条目时旁路表不被回收」而存在的。
func TestGetWithIdentity_GivenEvictedEntry_WhenKeyIsReused_ThenTheOldIdentityIsGone(t *testing.T) {
	p := NewCLISessionPool(1)
	first := newFakeSession("first")
	p.PutWithIdentity("claudecode:1", "model-a", first)
	p.MarkIdle("claudecode:1")

	// 池按 LRU 上限自行淘汰它(不经过任何后端代码)。
	p.Put("codex:2", newFakeSession("second"))
	p.MarkIdle("codex:2")
	first.WaitClosed(t)

	// 同一个池键回到池里,这次没人记身份。
	third := newFakeSession("third")
	p.Put("claudecode:1", third)

	_, ok := p.GetWithIdentity("claudecode:1", "model-a")

	assert.False(t, ok, "老身份不该在条目被淘汰后继续替新条目背书")
	third.WaitClosed(t)
}

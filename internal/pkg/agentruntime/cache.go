package agentruntime

import (
	"container/list"
	"context"
	"sync"
	"time"
)

// ctxCloser 是缓存条目能关闭子进程的最小接口。
// cago claudecode.Session / codex.Session / claudecode.Runner / codex.Runner 都满足。
type ctxCloser interface {
	Close(context.Context) error
}

// ctxKiller 是缓存条目可选实现的硬杀口:优雅关闭在宽限期内没收住时,由 closeWithTimeout
// 升级到它。claudecode / codex 的会话适配器都实现了它(底层是整组 SIGKILL)。
type ctxKiller interface {
	Kill(context.Context) error
}

type CLISessionState string

const (
	CLISessionActive  CLISessionState = "active"
	CLISessionWaiting CLISessionState = "waiting"
	CLISessionIdle    CLISessionState = "idle"
)

const DefaultCLISessionIdleCap = 8

var defaultCLISessionPool = NewCLISessionPool(DefaultCLISessionIdleCap)

// DefaultCLISessionPool returns the process-wide CLI session pool shared by
// claudecode and codex runtimes. The desktop app has one instance; each
// agentred daemon process has its own instance.
func DefaultCLISessionPool() *CLISessionPool { return defaultCLISessionPool }

// CLISessionPool keeps persistent CLI subprocess sessions across turns.
// Only idle sessions count toward the cap. Active/waiting sessions are never
// evicted by cap pruning, so busy turns cannot be killed by unrelated sessions.
type CLISessionPool struct {
	mu      sync.Mutex
	idleCap int
	ll      *list.List
	index   map[string]*list.Element
}

type cliSessionEntry struct {
	key   string
	val   ctxCloser
	state CLISessionState
}

func NewCLISessionPool(idleCap int) *CLISessionPool {
	if idleCap <= 0 {
		idleCap = 1
	}
	return &CLISessionPool{
		idleCap: idleCap,
		ll:      list.New(),
		index:   map[string]*list.Element{},
	}
}

func (p *CLISessionPool) Get(key string) (ctxCloser, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	el, ok := p.index[key]
	if !ok {
		return nil, false
	}
	p.ll.MoveToFront(el)
	return el.Value.(*cliSessionEntry).val, true
}

func (p *CLISessionPool) Put(key string, v ctxCloser) {
	p.mu.Lock()
	if el, ok := p.index[key]; ok {
		old := el.Value.(*cliSessionEntry).val
		el.Value = &cliSessionEntry{key: key, val: v, state: CLISessionActive}
		p.ll.MoveToFront(el)
		p.mu.Unlock()
		go closeWithTimeout(old)
		return
	}
	el := p.ll.PushFront(&cliSessionEntry{key: key, val: v, state: CLISessionActive})
	p.index[key] = el
	p.mu.Unlock()
}

func (p *CLISessionPool) MarkActive(key string)  { p.mark(key, CLISessionActive, false) }
func (p *CLISessionPool) MarkWaiting(key string) { p.mark(key, CLISessionWaiting, false) }
func (p *CLISessionPool) MarkIdle(key string)    { p.mark(key, CLISessionIdle, true) }

func (p *CLISessionPool) mark(key string, state CLISessionState, prune bool) {
	var closing []ctxCloser
	p.mu.Lock()
	if el, ok := p.index[key]; ok {
		el.Value.(*cliSessionEntry).state = state
		p.ll.MoveToFront(el)
	}
	if prune {
		closing = p.pruneLocked()
	}
	p.mu.Unlock()
	for _, old := range closing {
		go closeWithTimeout(old)
	}
}

func (p *CLISessionPool) pruneLocked() []ctxCloser {
	var closing []ctxCloser
	for p.idleLenLocked() > p.idleCap {
		var victim *list.Element
		for el := p.ll.Back(); el != nil; el = el.Prev() {
			if el.Value.(*cliSessionEntry).state == CLISessionIdle {
				victim = el
				break
			}
		}
		if victim == nil {
			break
		}
		ent := victim.Value.(*cliSessionEntry)
		p.ll.Remove(victim)
		delete(p.index, ent.key)
		closing = append(closing, ent.val)
	}
	return closing
}

func (p *CLISessionPool) Remove(key string) {
	p.mu.Lock()
	el, ok := p.index[key]
	if !ok {
		p.mu.Unlock()
		return
	}
	ent := el.Value.(*cliSessionEntry)
	p.ll.Remove(el)
	delete(p.index, key)
	p.mu.Unlock()
	go closeWithTimeout(ent.val)
}

// RemoveAll 摘空池并在后台关闭每个条目,不等收尾。需要「收干净了」的保证时用 CloseAll。
func (p *CLISessionPool) RemoveAll() {
	for _, v := range p.drainAll() {
		go closeWithTimeout(v)
	}
}

// CloseAll 同步收掉全部条目:每个条目走与 evict 相同的「优雅关闭 → 超时硬杀」路径,
// 全部收尾后才返回。ctx 是关机路径的上界 —— 到期就如实回错,绝不让一个不认领硬杀口的
// 卡死条目把关机挂住。
//
// 与 RemoveAll 的区别就是这一句保证:RemoveAll 是 fire-and-forget,宿主进程紧接着退出
// 时那些 goroutine 连同子进程一起被留下。
func (p *CLISessionPool) CloseAll(ctx context.Context) error {
	olds := p.drainAll()
	if len(olds) == 0 {
		return nil
	}
	var wg sync.WaitGroup
	for _, v := range olds {
		wg.Add(1)
		go func(c ctxCloser) {
			defer wg.Done()
			closeWithTimeout(c)
		}(v)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		// 上界到了就撒手不管等于把「关机有上界」变成「关机会漏进程」:还没收尾的条目
		// 在这里当场硬杀,收尾留给后台跑着的 closeWithTimeout。
		for _, v := range olds {
			if killer, ok := v.(ctxKiller); ok {
				_ = killer.Kill(context.Background())
			}
		}
		return ctx.Err()
	}
}

// KillAll 摘空池并当场硬杀每个条目,不等任何一个 Close 返回。
//
// 给「宿主马上就要退出」那条路用:桌面端确认退出后 Shutdown 必须在 100ms 内返回
// (见 internal/app 的退出契约),把优雅关闭放进异步 goroutine 等于进程先退、子进程
// 被留下 —— 它们自带进程组,不会被连坐。收尾(Close)照旧在后台跑完。
func (p *CLISessionPool) KillAll() {
	for _, v := range p.drainAll() {
		if killer, ok := v.(ctxKiller); ok {
			_ = killer.Kill(context.Background())
		}
		go closeWithTimeout(v)
	}
}

// drainAll 原子地摘空池并交回原来的条目。
func (p *CLISessionPool) drainAll() []ctxCloser {
	p.mu.Lock()
	defer p.mu.Unlock()
	olds := make([]ctxCloser, 0, p.ll.Len())
	for el := p.ll.Front(); el != nil; el = el.Next() {
		olds = append(olds, el.Value.(*cliSessionEntry).val)
	}
	p.ll.Init()
	p.index = map[string]*list.Element{}
	return olds
}

func (p *CLISessionPool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ll.Len()
}

func (p *CLISessionPool) IdleLen() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.idleLenLocked()
}

func (p *CLISessionPool) idleLenLocked() int {
	n := 0
	for el := p.ll.Front(); el != nil; el = el.Next() {
		if el.Value.(*cliSessionEntry).state == CLISessionIdle {
			n++
		}
	}
	return n
}

// closeGracePeriod 是优雅关闭的宽限期,超时后升级到硬杀。单测覆写成毫秒级。
var closeGracePeriod = 3 * time.Second

// setCloseGraceForTest 覆写宽限期,返回还原函数。仅供单测使用。
func setCloseGraceForTest(d time.Duration) func() {
	old := closeGracePeriod
	closeGracePeriod = d
	return func() { closeGracePeriod = old }
}

// closeWithTimeout 关掉一个被 evict 的 session,优雅关闭救不回来时升级到硬杀。
//
// 优雅关闭对卡死的 CLI 无效:claudecode.Session.Close 是「关 stdin → 等子进程退出」,
// 而卡在 MCP 初始化里的 CLI 根本不读 stdin,这一步永不返回 —— 从前这里传的是
// context.Background(),于是 goroutine 连同它的整棵子进程树被永久留下。宽限期一到就
// 调 Kill(整组 SIGKILL):进程死亡后 Close 那一路自然收尾。
//
// 不实现 ctxKiller 的实现体(测试替身、纯内存 Runner)退化成原来的行为:等 Close 自己
// 返回,不做别的。
func closeWithTimeout(c ctxCloser) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = c.Close(context.Background())
	}()
	killer, canKill := c.(ctxKiller)
	if !canKill {
		<-done
		return
	}
	timer := time.NewTimer(closeGracePeriod)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		_ = killer.Kill(context.Background())
		<-done
	}
}

// RunnerCache 按 (backendID, updatetime) 缓存 cliagent.Runner-likes。
// updatetime 变化 = entity 重配；老 Runner 关掉重建。
type RunnerCache struct {
	mu      sync.Mutex
	entries map[int64]*runnerEntry
}

type runnerEntry struct {
	updatetime int64
	runner     ctxCloser
}

// NewRunnerCache 构造空缓存。
func NewRunnerCache() *RunnerCache { return &RunnerCache{entries: map[int64]*runnerEntry{}} }

// GetOrCreate 命中则返回；updatetime 变了则关旧建新。
// build 由调用方提供——claudecode.Runner 和 codex.Runner 类型不同，无法在缓存层抽象。
func (c *RunnerCache) GetOrCreate(backendID, updatetime int64, build func() (ctxCloser, error)) (ctxCloser, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[backendID]; ok {
		if e.updatetime == updatetime {
			return e.runner, nil
		}
		go closeWithTimeout(e.runner)
	}
	r, err := build()
	if err != nil {
		return nil, err
	}
	c.entries[backendID] = &runnerEntry{updatetime: updatetime, runner: r}
	return r, nil
}

// Drop 移除并关闭某 backend 的 Runner。
func (c *RunnerCache) Drop(backendID int64) {
	c.mu.Lock()
	e, ok := c.entries[backendID]
	if ok {
		delete(c.entries, backendID)
	}
	c.mu.Unlock()
	if ok {
		go closeWithTimeout(e.runner)
	}
}

// Package remotepool 持有「一台配对 daemon 上共享的 *remote.Runtime + 它的池化租约」
// 这一套引用计数缓存。
//
// 它从 chat_svc 拆出来的判据是自足性:整套状态就是 mu + cache 两个字段,对外只有
// (设备, 会话) 这一组键,不认识 turn / 转录 / 前端投影。宿主(chat_svc)通过 Host
// 交出四件它自己才知道的事:连接池、设备解析、实例标识、以及怎么把一条连接装配成
// *remote.Runtime(五类通知 observer 与重连端口都留在宿主侧)。
//
// 生命周期不变量(每一条都对应过一次真实事故,改动前先读 adoptLease / releaseGeneration
// 的注释):
//   - 一条池化连接上只能有一个 *remote.Runtime —— 它是那条连接上通知 handler 的属主;
//   - 引用归零只把 lease 还给池,**不摘 cache entry**;
//   - 只有池那侧宣告连接失效(Lease.Closed())才摘 entry。
package remotepool

import (
	"context"
	"sync"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

// Host 是本包对宿主(chat_svc)的窄依赖(ISP):只声明池真正调用的四件事。
type Host interface {
	// ConnPool 返回当前生效的 daemon 连接池(测试注入点留在宿主侧)。
	ConnPool() remote_device_svc.ConnPool
	// PairedDeviceID 把 backend 持久化的 DeviceID(指纹或历史数值行 ID)解析成本机
	// paired_agentreds 的行 ID;解析不出 → (0,false),调用方报「不可派发」。
	PairedDeviceID(ctx context.Context, deviceRef string) (int64, bool)
	// DaemonFingerprint 取该设备此刻的 daemon 实例标识;取不到返回空串。
	DaemonFingerprint(ctx context.Context, deviceID int64) string
	// RecordExecDaemon 把「这条会话跑在哪台 daemon 的哪个实例上、钉在哪一档」落库。
	RecordExecDaemon(ctx context.Context, sessionID, deviceID int64, fingerprint string, agentBackendID int64)
	// NewRuntime 把一条已鉴权连接装配成该设备共享的 *remote.Runtime。observer 与重连
	// 端口都在宿主侧装,池只负责这个实例的寿命;entry 交给宿主是因为重连端口要往
	// 同一个 entry 里换 lease。
	NewRuntime(deviceID int64, entry *Entry, conn client.ProtobufConnection, fingerprint string) *remote.Runtime
}

// Entry tracks a shared *remote.Runtime built on top of a Pool Lease, and the set
// of session IDs currently using it. remote_device_svc.Pool 负责底层 conn 复用 +
// idle 回收 + daemon drop evict;本包只是把 lease.Client() 升成 *remote.Runtime
// (handlers conn-scoped,一台 device 装一组就够)。
//
// entry 的寿命跟的是**那条池化连接**,不是本进程手上还有几个会话引用:runtime 是这条
// 连接上五类通知 handler 的属主,连接还活着就不能为它另造一个(见 releaseGeneration
// 与 ForDevice)。
type Entry struct {
	runtime  *remote.Runtime
	lease    remote_device_svc.Lease
	sessions map[int64]*Generation
	// leased 记 entry.lease 此刻是不是还没归还。引用归零时 lease 还给池(池的空闲回收
	// 因此与今天完全一致)而 entry 留着,此后它为 false —— 下一次借用必须重新借一条,
	// 否则这一轮进行中连接会被空闲回收抽走。
	leased bool
}

// Generation is the exact lease owner for one turn. A stale release compares this
// pointer before deleting the session reference, so it cannot release the device
// lease after a newer same-SessionID retry begins.
type Generation struct{}

// Pool 是 device → (runtime, lease) 的会话引用计数缓存。
type Pool struct {
	host  Host
	mu    sync.Mutex
	cache map[int64]*Entry
}

// New 构造一个空池。
func New(host Host) *Pool { return &Pool{host: host} }

// Borrow 返回该 device 共享的 *remote.Runtime。第一次 borrow 会从池借一个 lease 并
// wrap 成 runtime;后续同 device borrow 直接命中 cache。lease.Closed() 关闭(daemon
// drop / idle / Pool.Close)→ watchLeaseClosed 把 entry 摘掉,下次 borrow 走冷路径重建。
//
// 同 sessionID 多次 borrow 对 sessions set 幂等。deviceRef 解析不出本机配对行 ID 时
// 返回 ErrInvalidDevice;拨号失败返回 *DialError(包着原始 err 与设备号供日志)。
func (p *Pool) Borrow(
	ctx context.Context, deviceRef string, sessionID, agentBackendID int64,
) (*remote.Runtime, error) {
	return p.borrowOwned(ctx, deviceRef, sessionID, agentBackendID, nil)
}

// BorrowForTurn 与 Borrow 相同,但这次借用带着一轮自己的 generation token:迟到的旧
// release 比对指针后不会顶掉同会话新一轮的引用。第二个返回值是该轮的释放函数。
//
// 出错时不留下任何引用,交回的 release 是 no-op 而非 nil —— 调用方在错误路径上
// 无需(也不该)调它。改动这里前先读 borrowRemoteRuntimeForTurn 的错误契约。
func (p *Pool) BorrowForTurn(
	ctx context.Context, deviceRef string, sessionID, agentBackendID int64,
) (*remote.Runtime, func(), error) {
	deviceID, ok := p.host.PairedDeviceID(ctx, deviceRef)
	if !ok {
		return nil, func() {}, ErrInvalidDevice
	}
	generation := &Generation{}
	rt, err := p.borrowOwned(ctx, deviceRef, sessionID, agentBackendID, generation)
	if err != nil {
		return nil, func() {}, err
	}
	return rt, func() { p.ReleaseGeneration(deviceID, sessionID, generation) }, nil
}

func (p *Pool) borrowOwned(
	ctx context.Context, deviceRef string, sessionID, agentBackendID int64, generation *Generation,
) (*remote.Runtime, error) {
	deviceID, ok := p.host.PairedDeviceID(ctx, deviceRef)
	if !ok {
		return nil, ErrInvalidDevice
	}
	rt, fp, err := p.ForDevice(ctx, deviceID, []int64{sessionID}, generation)
	if err != nil {
		return nil, &DialError{DeviceID: deviceID, Err: err}
	}
	// 会话在库里记下「跑在哪台 daemon 的哪个实例上」——游标端口的读写守卫全靠它,
	// 不写就永远判失效,断连补齐退化成断连即终止(见 remote_reconnect.go);而 App
	// 重启后「该连谁」也全靠这一行(见 CatchUpRemoteSessions)。runtime 是 device 级
	// 共享的,同一台设备上的第二条会话走 cache 命中那条路,它自己的执行位置同样要落库。
	p.host.RecordExecDaemon(ctx, sessionID, deviceID, fp, agentBackendID)
	return rt, nil
}

// Cached 交出某台配对 daemon 上**此刻已经在跑的**那个 *remote.Runtime,没有就交 nil。
// 它是只读控制路径(如浏览器查一眼待决策)专用的:命中条件与 ForDevice 的 fast path
// 逐字一致(条目在、且它手上还握着 lease),但一件副作用都不做。
//
// 三件事都不能顺手做:借连接(pool.Borrow)为一次「查一眼」拨号并占住池引用;
// RecordExecDaemon 是一次数据库写 —— 只读查询不该改会话的执行归属;addSessionRefs 记下
// 的引用没有对应的 release,那条 lease 从此还不掉。
//
// 交 nil 的语义是「本机此刻没有在那台设备上开着的轮次」:没有在跑的连接,那边也就没有
// 本机要照看的待决策,调用方据此回空而不是当故障。
func (p *Pool) Cached(deviceID int64) *remote.Runtime {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.cache[deviceID]
	if !ok || !entry.leased {
		return nil
	}
	return entry.runtime
}

// ForDevice 取(或建)某台配对 daemon 上共享的 *remote.Runtime,并把 sessionIDs 记进
// 它的引用集;顺带交回该 daemon 此刻的实例标识。
//
// 它与 Borrow 分开,是因为补齐(CatchUpRemoteSessions)手上只有 (设备, 会话) 而
// **没有 agent backend**:重启后要连回的那台 daemon 是从 chat_sessions.exec_device_id
// 读出来的,不经过 turn 的后端选择。owner 非 nil 时这次借用带着一轮自己的 generation
// token(见 Generation);控制路径(owner==nil)只在引用缺失时补一个占位,不覆盖当前
// 轮的 owner。
func (p *Pool) ForDevice(
	ctx context.Context, deviceID int64, sessionIDs []int64, owner *Generation,
) (*remote.Runtime, string, error) {
	// Fast path: cache hit —— entry 手上还握着 lease,连借都不用借。
	p.mu.Lock()
	if p.cache == nil {
		p.cache = map[int64]*Entry{}
	}
	if entry, ok := p.cache[deviceID]; ok && entry.leased {
		addSessionRefs(entry, sessionIDs, owner)
		p.mu.Unlock()
		return entry.runtime, p.host.DaemonFingerprint(ctx, deviceID), nil
	}
	p.mu.Unlock()

	// Cold path: 借 lease,再看能不能沿用留在 cache 里的那个 runtime
	lease, err := p.host.ConnPool().Borrow(ctx, deviceID)
	if err != nil {
		return nil, "", err
	}
	fp := p.host.DaemonFingerprint(ctx, deviceID)

	if entry, installed := p.adoptLease(deviceID, lease, sessionIDs, owner, nil); entry != nil {
		if installed {
			go p.watchLeaseClosed(deviceID, entry, lease)
		} else {
			lease.Release()
		}
		return entry.runtime, fp, nil
	}

	// entry 先建出来:重连端口要往里换 lease,所以它必须先于 runtime 存在。
	entry := &Entry{lease: lease, leased: true, sessions: map[int64]*Generation{}}
	addSessionRefs(entry, sessionIDs, owner)
	entry.runtime = p.host.NewRuntime(deviceID, entry, lease.Client(), fp)

	// TOCTOU 输家:用赢家的 entry,自己刚建的这个丢掉。「查」与「装」交给 adoptLease
	// 在同一个临界区里做完 —— 分成两次上锁的话两条冷路径会各自查到「没有」,后装的那个
	// 把先装的整个覆盖掉(见 adoptLease 的说明)。fresh 非 nil,交回的 entry 因此非 nil。
	installedEntry, installed := p.adoptLease(deviceID, lease, sessionIDs, owner, entry)
	if installed {
		go p.watchLeaseClosed(deviceID, installedEntry, lease)
	} else {
		lease.Release()
	}
	return installedEntry.runtime, fp, nil
}

// adoptLease 试着把这次借到的 lease 交给 deviceID 在 cache 里已有的那个 entry,并把
// sessionIDs 记进它的引用集。交回 (要用的 entry, 这条 lease 是否被装了进去)。
//
// 没有可用的 entry(没有条目、或它那条池化连接已经被回收)时:fresh 非 nil 就把它装
// 进 cache 并交回它,fresh 为 nil 则交回 nil,调用方据此去新建。「查」与「装」因此在
// **同一个临界区**里 —— 分开两次上锁的话,两条同时走冷路径的 borrow 会各自查到「没有」,
// 后装的那个把先装的整个覆盖掉。代价有两条:被覆盖的那个 entry 的 lease 从此没人还
// (那一轮的 release 按 deviceID 查 cache,查到的是覆盖它的那个,generation 比不上就
// 直接返回),它那条池化连接再也不会空闲回收;而两个 runtime 同时挂在同一条连接上抢注
// 同名 handler —— 正是下面这一段说的 R18 / R10。
//
// 为什么非沿用不可:entry.runtime 是**那条连接**上五类通知 handler 的属主,也是自主
// 续轮消费方(chat_svc 每会话只订阅一次)订阅的那个实例。连接还活着却为它另造一个
// runtime,新实例会把 handler 抢注过去,而消费方还挂在旧实例上 —— 别的端在这台机器上
// 发起的一轮于是被投进一个没有消费方的补齐轮,既不落库也不报错(R18);新实例的会话表
// 又是空的,对那条会话提交工具决议当场 ErrNoActiveTurn(R10)。
//
// 「还能沿用吗」问的是**这次借到的是不是同一条池化连接**(见 samePooledConn),不是
// 「上一条 lease 还没关闭吗」:池的 tryEvictIdle 与 watchClient 都是先把 entry 从表里
// 摘掉、再关 closedCh,落进这两步之间的 Borrow 会拨一条新连接,而旧那条此刻还没关 ——
// 「还没关」并不蕴含「借到的是同一条」。
func (p *Pool) adoptLease(
	deviceID int64,
	lease remote_device_svc.Lease,
	sessionIDs []int64,
	owner *Generation,
	fresh *Entry,
) (*Entry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cache == nil {
		p.cache = map[int64]*Entry{}
	}
	entry, ok := p.cache[deviceID]
	if ok && entry.leased {
		// 期间已经有人给它借了一条(并发的另一次 borrow / 重连端口换过 lease):
		// 用它手上那条,自己这条还回去。
		addSessionRefs(entry, sessionIDs, owner)
		return entry, false
	}
	if ok && !samePooledConn(entry.lease, lease) {
		// 借到的是另一条连接 —— 上一条已经被池摘走(daemon drop / 空闲回收 /
		// Pool.Close),这个 entry 连同它的 runtime 一起作废(它自己的 watchClose
		// 会收尾挂在上面的会话与消费方)。
		delete(p.cache, deviceID)
		ok = false
	}
	if !ok {
		if fresh == nil {
			return nil, false
		}
		// fresh 由调用方建好(lease / leased / 引用集都已就位),这里只负责装。
		p.cache[deviceID] = fresh
		return fresh, true
	}
	entry.lease = lease
	entry.leased = true
	addSessionRefs(entry, sessionIDs, owner)
	return entry, true
}

// samePooledConn 报告两条 lease 是不是同一条池化连接上的 —— 也就是挂在前者上的
// runtime 能不能接着用后者。
//
// 判据是 Lease.Closed():按契约它交回的是**池 entry 级**的信号(daemon drop /
// 空闲超时 / Pool.Close 时关闭,与 Release 无关),同一个池 entry 交出的每一条
// lease 因此拿到同一个 channel —— 它就是那条连接的身份。而 Borrow 只会在 entry
// 还没失效时把它交出来,所以「同一条」自带「还活着」。
//
// 不用「上一条还没关闭吗」来代替:池摘表与关信号是两步(先 delete 后 close),
// 落进中间的 Borrow 会拨一条新连接而旧信号尚未关闭,那时「还没关」是真的、
// 「同一条」却是假的,沿用旧 runtime 等于把这一轮发给一条正被关掉的 socket。
func samePooledConn(a, b remote_device_svc.Lease) bool {
	if a == nil || b == nil {
		return false
	}
	// 契约里 closedCh 恒非 nil;真为 nil 时两个 nil 会假装成「同一条」,宁可重建。
	closed := a.Closed()
	return closed != nil && closed == b.Closed()
}

// addSessionRefs 调用方必须持 Pool.mu。owner 非 nil = 这一轮的 generation token,
// 直接安装(它接管这条会话的引用);owner 为 nil 的控制路径只在引用缺失时补占位,
// 不覆盖当前轮的 owner —— 覆盖了的话那一轮的 release 就比不上指针、永远释放不掉。
func addSessionRefs(entry *Entry, sessionIDs []int64, owner *Generation) {
	for _, sid := range sessionIDs {
		if owner != nil {
			entry.sessions[sid] = owner
			continue
		}
		if entry.sessions[sid] == nil {
			entry.sessions[sid] = &Generation{}
		}
	}
}

// watchLeaseClosed 监听某条 lease 的 Closed()(池那侧通知它失效),然后把这边的 cache
// entry 摘掉,下次 borrow 走冷路径重建 runtime。
//
// 只在**这条** lease 仍是该 entry 当前那条时才摘:runtime 已经自己重连换过 lease 的话
// entry 还活着,摘掉它会让下一轮 borrow 为同一台设备造出第二个 *remote.Runtime,两个
// runtime 在同一条池化连接上抢注同名 handler,在飞会话的事件会被路由到不认识它的那个
// 然后静默丢弃。lease 参数因此是显式的,不能读 entry.lease —— 那是会变的。
func (p *Pool) watchLeaseClosed(deviceID int64, entry *Entry, lease remote_device_svc.Lease) {
	<-lease.Closed()
	p.mu.Lock()
	cur, ok := p.cache[deviceID]
	if ok && cur == entry && entry.lease == lease {
		delete(p.cache, deviceID)
	}
	p.mu.Unlock()
}

// Release decrements the session refcount for deviceID. 当最后一个 session release
// 时,把 lease 还给池(池自己负责 idle 回收 + 后续 borrow 复用),但 cache entry 与
// 它的 runtime 留着 —— 见 ReleaseGeneration。
func (p *Pool) Release(deviceID, sessionID int64) {
	p.mu.Lock()
	entry, ok := p.cache[deviceID]
	if !ok {
		p.mu.Unlock()
		return
	}
	generation := entry.sessions[sessionID]
	p.mu.Unlock()
	if generation != nil {
		p.ReleaseGeneration(deviceID, sessionID, generation)
	}
}

// ReleaseGeneration 只在 sessionID 的引用仍属于 generation 时才减引用:迟到的旧
// release 因此顶不掉同会话新一轮的引用。
func (p *Pool) ReleaseGeneration(deviceID, sessionID int64, generation *Generation) {
	p.mu.Lock()
	entry, ok := p.cache[deviceID]
	if !ok || entry.sessions[sessionID] != generation {
		p.mu.Unlock()
		return
	}
	delete(entry.sessions, sessionID)
	if len(entry.sessions) > 0 {
		p.mu.Unlock()
		return
	}
	// 引用归零只把 lease 还给池(池的空闲回收计时因此照旧),**不摘 cache entry**:
	// 那条连接还活着,而 entry.runtime 是它上面通知 handler 的属主、也是自主续轮消费方
	// 订阅的实例。摘掉它,下一轮 borrow 会为同一条连接另造一个 runtime 并抢走 handler
	// —— 别的端此后在这条会话上发起的一轮就没人落库了(R18),对它提交工具决议也当场
	// ErrNoActiveTurn(R10)。连接真被回收时由 watchLeaseClosed 摘 entry。
	//
	// 要还的那条必须在解锁**之前**抓下来:entry 从此留在 map 里,解锁之后并发的另一次
	// borrow 会走冷路径(fast path 因 leased==false 落空)并在 adoptLease 里把
	// entry.lease 换成它刚借的那条。到那时再读 entry.lease,读到的是别人这一轮正用着的
	// 那条 —— 把它提前还掉,而自己这条的池引用永远掉不下去,那条连接从此不再空闲回收。
	lease := entry.lease
	entry.leased = false
	p.mu.Unlock()
	lease.Release()
}

// Reattach 是重连端口用的换租约:把新借到的 lease 换进同一个 entry 并保证它留在
// cache 里,交回要归还的旧那条(可能为 nil)。
//
// entry 必须留在 cache 里(断连时 watchLeaseClosed 可能已经把它摘掉了,这里装回去):
// 摘掉它,下一轮 borrow 会为同一台设备造出第二个 *remote.Runtime,而两个 runtime 会在
// 同一条池化连接上抢注同名 handler —— 在飞会话的事件从此被路由到不认识它的那个,静默
// 丢弃,既没有错误也没有 seq 跳号。
func (p *Pool) Reattach(deviceID int64, entry *Entry, lease remote_device_svc.Lease) remote_device_svc.Lease {
	p.mu.Lock()
	old := entry.lease
	entry.lease = lease
	entry.leased = true
	if cur, ok := p.cache[deviceID]; !ok || cur == entry {
		if p.cache == nil {
			p.cache = map[int64]*Entry{}
		}
		p.cache[deviceID] = entry
	}
	p.mu.Unlock()
	go p.watchLeaseClosed(deviceID, entry, lease)
	return old
}

// Count returns the number of sessions currently sharing the runtime for
// deviceID. Returns 0 if no entry exists.
func (p *Pool) Count(deviceID int64) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry, ok := p.cache[deviceID]; ok {
		return len(entry.sessions)
	}
	return 0
}

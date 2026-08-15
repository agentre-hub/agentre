package remote_device_svc

//go:generate mockgen -source conn_pool.go -destination mock_remote_device_svc/mock_conn_pool.go

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-ai/agentre/internal/daemon/client"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/repository/remote_device_repo"
)

// ErrPoolClosed 在 Pool.Close 之后调 Borrow 返回。生产路径只在 bootstrap
// shutdown 后才会出现,正常调用方记 warn 即可。
var ErrPoolClosed = errors.New("conn pool closed")

// ErrDeviceNotFound 在 repo 拿不到 device row 时返回。上层(agent_backend_svc
// / chat_svc 等)拿到后通常映射成自己的 i18n 错误码。
var ErrDeviceNotFound = errors.New("remote device not found")

// ErrDeviceUnauthorized 在 keychain 缺 token / device fingerprint,或 daemon
// 拒绝鉴权时返回。
var ErrDeviceUnauthorized = errors.New("remote device unauthorized")

// ConnPool 给上层(chat_svc / agent_backend_svc)提供 device-shared 的已鉴权
// daemon 连接。并发安全;Borrow 与 Lease.Release 可在任意 goroutine 调用。
//
// 与 remote_device_watcher_svc 并存,但**不共享 WS** —— watcher 的 WS 只跑
// health.ping,Pool 的 WS 只跑业务 RPC。详见 spec §0 Q12。
type ConnPool interface {
	Borrow(ctx context.Context, deviceID int64) (Lease, error)
	Close() error
}

// Lease 一次借用的句柄。语义:
//   - Client() 在 Release 前永远非 nil 且可用(底层若已 Close 则 Call 会返
//     net.ErrClosed 等错误,调用方据此重新 Borrow)。
//   - Closed() 在 entry 失效(daemon drop / idle 超时 / Pool.Close)时关闭。
//     chat_svc 用它桥接 *remote.Runtime 失效。
//   - Release() 幂等。
type Lease interface {
	Client() agentruntime.DaemonClientPort
	Closed() <-chan struct{}
	Release()
}

// Option 用于 NewConnPool 的可选配置。
type Option func(*pool)

// WithIdleTimeout 覆盖 refcount=0 后到 entry evict 的等待时长。生产 30s,
// 测试用 50ms / 0(立即 evict)。默认 30s。
func WithIdleTimeout(d time.Duration) Option {
	return func(p *pool) { p.idleTimeout = d }
}

// WithRelayDial 注入账号中转拨号端口。注入后 Borrow 会并发发起直连与中转两条
// 尝试、先到者胜（R6），任一路径不可用不构成失败；未注入时保持纯 LAN 行为。
func WithRelayDial(relay RelayDialPort) Option {
	return func(p *pool) { p.relay = relay }
}

// WithAccountCredential 注入账号凭据来源。注入后，本机对目标 daemon 没有配对时
// 直连改出示账号凭据（auth.account）；未注入或未登录时保持既有行为。
func WithAccountCredential(credentials AccountCredentialPort) Option {
	return func(p *pool) { p.credentials = credentials }
}

// NewConnPool 构造一个生产 ConnPool。
//   - repo: 查 device row(URL / TLS / fingerprint)
//   - kc:   读 keychain 的 token + device fingerprint(用本包窄接口 KeychainPort)
//   - dial: 真正打 ws + auth.connect 的端口(生产 NewDaemonDial(),测试 mock)
func NewConnPool(
	repo remote_device_repo.PairedAgentredRepo,
	kc KeychainPort,
	dial DaemonDialPort,
	opts ...Option,
) ConnPool {
	p := &pool{
		repo:        repo,
		kc:          kc,
		dial:        dial,
		idleTimeout: 30 * time.Second,
		entries:     map[int64]*entry{},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// ── 实现 ─────────────────────────────────────────────────────────────────

type pool struct {
	repo        remote_device_repo.PairedAgentredRepo
	kc          KeychainPort
	dial        DaemonDialPort
	relay       RelayDialPort         // 可空:未注入时 Borrow 纯 LAN
	credentials AccountCredentialPort // 可空:未注入时直连只认配对令牌
	idleTimeout time.Duration

	mu      sync.Mutex
	entries map[int64]*entry
	closed  bool
}

// pooledClient 是 entry.client 的窄接口,允许 internal test 用 fake 替身。
// 生产路径 = *client.Client(已实现 DaemonClientPort,即 pooledClient)。
type pooledClient interface {
	agentruntime.DaemonClientPort
}

// entry 单 device 的活连接 + refcount。
type entry struct {
	deviceID int64
	client   pooledClient  // raw; only pool 自己调 Close
	closedCh chan struct{} // entry 失效时关闭(once)

	mu        sync.Mutex
	refcount  int
	idleTimer *time.Timer
	evicted   bool // markDone 用,防多关 closedCh
}

// lease Pool.Borrow 返回的具体类型。
type lease struct {
	e           *entry
	pool        *pool
	releaseOnce sync.Once
}

func (l *lease) Client() agentruntime.DaemonClientPort {
	return noopCloseClient{DaemonClientPort: l.e.client}
}
func (l *lease) Closed() <-chan struct{} { return l.e.closedCh }
func (l *lease) Release() {
	l.releaseOnce.Do(func() { l.pool.releaseEntry(l.e) })
}

// noopCloseClient wraps a DaemonClientPort and turns Close into a no-op.
// 防止 lease 持有者(尤其 *remote.Runtime.Close())把池子里的 conn 关掉。
// 只有 Pool 自己持有 raw *client.Client。
type noopCloseClient struct {
	agentruntime.DaemonClientPort
}

func (noopCloseClient) Close() error { return nil }

// acquire 在 p.mu 已持有时被调。同时取消 idleTimer。
func (e *entry) acquire() {
	e.mu.Lock()
	e.refcount++
	if e.idleTimer != nil {
		e.idleTimer.Stop()
		e.idleTimer = nil
	}
	e.mu.Unlock()
}

// isEvicted true 表示 closedCh 已关。
func (e *entry) isEvicted() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.evicted
}

// ── pool 方法 ────────────────────────────────────────────────────────────

func (p *pool) Borrow(ctx context.Context, deviceID int64) (Lease, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrPoolClosed
	}
	if e, ok := p.entries[deviceID]; ok && !e.isEvicted() {
		e.acquire()
		p.mu.Unlock()
		return &lease{e: e, pool: p}, nil
	}
	p.mu.Unlock()

	// 释放 pool.mu 再做慢操作(repo / keychain / dial)。
	row, err := p.repo.Get(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ErrDeviceNotFound
	}
	token, err := p.kc.Get(keychainAccountForToken(deviceID))
	if err != nil {
		// 读不到配对令牌不再直接判死:账号凭据(若有)仍可能连上这台 daemon。
		logger.Ctx(ctx).Warn("conn pool: pairing token unreadable",
			zap.Int64("deviceID", deviceID), zap.Error(err))
		token = ""
	}
	fp, err := p.kc.Get(accountForDeviceFingerprint)
	if err != nil || fp == "" {
		return nil, ErrDeviceUnauthorized
	}
	credential := p.accountCredential()
	if token == "" && credential == "" {
		// 既没有本地配对令牌、账号也没登录 —— 没有任何身份可出示。
		return nil, ErrDeviceUnauthorized
	}
	if token == "" {
		logger.Ctx(ctx).Info("conn pool: no local pairing, dialing with the account credential",
			zap.Int64("deviceID", deviceID))
	}
	c, err := p.openAny(ctx, ConnectArgs{
		URL:                       row.URL,
		TLSMode:                   row.TLSMode,
		TLSCertPEM:                row.TLSCertPEM,
		DeviceFingerprint:         fp,
		DeviceToken:               token,
		ExpectedDaemonFingerprint: row.DaemonFingerprint,
	}, credential)
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
			// 直连的 auth.connect 明确拒绝了凭据(设备令牌被撤销 / 已解除配对)。
			// 这是终止条件:上层(chat_svc.terminalBorrowError / remote_fs_svc /
			// sync_provider)靠这个 sentinel 判定「重试也没用」,少了它重连循环会
			// 永远重试一台再也不会接受自己的 daemon。包一层而不是直接返回 sentinel,
			// 是为了保住 R6 要求的「两条路径各自的失败原因」。
			return nil, fmt.Errorf("%w: %w", ErrDeviceUnauthorized, err)
		}
		return nil, err
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = c.Close()
		return nil, ErrPoolClosed
	}
	if existing, ok := p.entries[deviceID]; ok && !existing.isEvicted() {
		// 并发输家:用赢家的 entry,关掉自己刚拨的 client。
		existing.acquire()
		p.mu.Unlock()
		_ = c.Close()
		return &lease{e: existing, pool: p}, nil
	}
	e := &entry{
		deviceID: deviceID,
		client:   c,
		closedCh: make(chan struct{}),
		refcount: 1,
	}
	p.entries[deviceID] = e
	go p.watchClient(e)
	p.mu.Unlock()

	logger.Ctx(ctx).Info("conn pool: new entry, dialed daemon",
		zap.Int64("deviceID", deviceID))
	return &lease{e: e, pool: p}, nil
}

// accountCredential 返回当前账号凭据；未注入凭据来源或未登录时是空串。
func (p *pool) accountCredential() string {
	if p.credentials == nil {
		return ""
	}
	return p.credentials.AccessToken()
}

// openAny 拨一台 daemon：未注入 relay 时只走 LAN 直连；注入了 relay 时并发发起
// 直连与中转两条路径、先到者胜（R6）。两条路径解析出的对端标识（DeviceFingerprint）
// 相同——该硬不变量由 client.Race 守卫。
//
// 直连的凭据优先级：该指纹有本地配对就沿用 auth.connect（R2，行为不变）；没有配对
// 才用账号凭据走 auth.account（R3）。中转路径恒用 auth.account，不受影响。
func (p *pool) openAny(ctx context.Context, args ConnectArgs, credential string) (*client.Client, error) {
	// 账号来源收编的行没有 LAN 地址（paired_agentred_entity.IsRelayOnly）：直连不是
	// 「拨了没通」而是**根本不存在**这条路。拿空地址去竞速只会白等一次拨号超时，
	// 还会把「这台机器本来就没有 LAN 路径」包装成一条看起来像网络故障的失败原因。
	if strings.TrimSpace(args.URL) == "" {
		if p.relay == nil {
			return nil, fmt.Errorf("device %s has no LAN address and no relay is configured",
				args.ExpectedDaemonFingerprint)
		}
		return p.relay.Open(ctx, args.ExpectedDaemonFingerprint, args.DeviceFingerprint)
	}
	direct := func(ctx context.Context) (*client.Client, error) {
		if args.DeviceToken != "" {
			return p.dial.Open(ctx, args)
		}
		return p.dial.OpenAccount(ctx, AccountArgs{
			URL:                       args.URL,
			TLSMode:                   args.TLSMode,
			TLSCertPEM:                args.TLSCertPEM,
			Credential:                credential,
			DeviceFingerprint:         args.DeviceFingerprint,
			ExpectedDaemonFingerprint: args.ExpectedDaemonFingerprint,
		})
	}
	if p.relay == nil {
		return direct(ctx)
	}
	return client.Race(ctx,
		client.Path{
			Name:        "direct",
			Fingerprint: args.DeviceFingerprint,
			Dial:        direct,
		},
		client.Path{
			Name:        "relay",
			Fingerprint: args.DeviceFingerprint,
			Dial: func(ctx context.Context) (*client.Client, error) {
				return p.relay.Open(ctx, args.ExpectedDaemonFingerprint, args.DeviceFingerprint)
			},
		},
	)
}

// watchClient 在 entry 建好后启,监听底层 conn 死亡 → evict。
// goroutine 在 closedCh 关闭或 entry 显式 evict(idle / Pool.Close)后退出。
//
// 注意:e.client 可能在 entry 被 tryEvictIdle / Pool.Close 清空,因此先在
// entry mutex 下抓本地引用,nil 时直接退出。
func (p *pool) watchClient(e *entry) {
	e.mu.Lock()
	c := e.client
	e.mu.Unlock()
	if c == nil {
		return
	}
	<-c.Closed()

	// 远端 daemon 断了(进程崩 / 网络断 / TLS 失败)。打 Warn 让运维区分
	// "用户主动 Close" vs "remote 单方面失效"——前者走 Pool.Close 路径,
	// 不经过 watchClient。
	logger.Default().Warn("conn pool: daemon connection dropped, evicting entry",
		zap.Int64("deviceID", e.deviceID))

	p.mu.Lock()
	if cur, ok := p.entries[e.deviceID]; ok && cur == e {
		delete(p.entries, e.deviceID)
	}
	p.mu.Unlock()

	e.mu.Lock()
	if e.idleTimer != nil {
		e.idleTimer.Stop()
		e.idleTimer = nil
	}
	if !e.evicted {
		e.evicted = true
		close(e.closedCh)
	}
	e.mu.Unlock()
}

func (p *pool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	evicting := make([]*entry, 0, len(p.entries))
	for _, e := range p.entries {
		evicting = append(evicting, e)
	}
	p.entries = map[int64]*entry{}
	p.mu.Unlock()

	for _, e := range evicting {
		e.mu.Lock()
		if e.idleTimer != nil {
			e.idleTimer.Stop()
			e.idleTimer = nil
		}
		c := e.client
		e.client = nil
		if !e.evicted {
			e.evicted = true
			close(e.closedCh)
		}
		e.mu.Unlock()
		if c != nil {
			_ = c.Close()
		}
	}
	return nil
}

func (p *pool) releaseEntry(e *entry) {
	e.mu.Lock()
	if e.refcount <= 0 {
		// 已经全部释放(典型场景:Pool.Close 已 evict);幂等。
		e.mu.Unlock()
		return
	}
	e.refcount--
	if e.refcount > 0 {
		e.mu.Unlock()
		return
	}
	// refcount 落到 0:启动 idle timer。
	e.idleTimer = time.AfterFunc(p.idleTimeout, func() { p.tryEvictIdle(e) })
	e.mu.Unlock()
}

// tryEvictIdle 由 idleTimer 触发。重检 refcount + entry 在 map 中。
func (p *pool) tryEvictIdle(e *entry) {
	p.mu.Lock()
	e.mu.Lock()
	if e.refcount != 0 || e.evicted {
		e.mu.Unlock()
		p.mu.Unlock()
		return
	}
	cur, ok := p.entries[e.deviceID]
	if !ok || cur != e {
		// entry 已被其它路径(daemon drop / Pool.Close)替换或删除。
		e.mu.Unlock()
		p.mu.Unlock()
		return
	}
	delete(p.entries, e.deviceID)
	p.mu.Unlock()

	c := e.client
	e.client = nil
	e.evicted = true
	close(e.closedCh)
	e.mu.Unlock()
	logger.Default().Debug("conn pool: idle timeout, evicted entry",
		zap.Int64("deviceID", e.deviceID))
	_ = c.Close()
}

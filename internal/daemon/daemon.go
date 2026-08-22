package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	dbpkg "github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/agentre-ai/agentre/internal/daemon/enginesnapshot"
	"github.com/agentre-ai/agentre/internal/daemon/handlers"
	daemonmigrations "github.com/agentre-ai/agentre/internal/daemon/migrations"
	"github.com/agentre-ai/agentre/internal/daemon/notifier"
	"github.com/agentre-ai/agentre/internal/daemon/pairing"
	"github.com/agentre-ai/agentre/internal/daemon/remotefs"
	"github.com/agentre-ai/agentre/internal/daemon/repository/notification_repo"
	"github.com/agentre-ai/agentre/internal/daemon/repository/session_repo"
	"github.com/agentre-ai/agentre/internal/daemon/rpc"
	"github.com/agentre-ai/agentre/internal/daemon/sessions"
	"github.com/agentre-ai/agentre/internal/daemon/state"
	daemonworkspacefs "github.com/agentre-ai/agentre/internal/daemon/workspacefs"
	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-ai/agentre/internal/pkg/ccoauth"
	"github.com/agentre-ai/agentre/internal/pkg/httpgateway"
	"github.com/agentre-ai/agentre/internal/pkg/pty"
	"github.com/agentre-ai/agentre/internal/pkg/pty/local"
)

// dbFileName 是 agentred 自己的 SQLite 库文件名,位于 Options.DataDir(=
// paths.AgentredDataDir())根目录。与桌面端的 agentre.db 各自独立、互不引用
// (见 internal/daemon/migrations 包注释)。
const dbFileName = "agentred.db"

// Options configures the Daemon at construction time.
type Options struct {
	DataDir     string
	LANHost     string
	LANPort     int
	TLSCertFile string
	TLSKeyFile  string
	// HubServerURL is the account server base URL used for the daemon's
	// outbound relay connection. An empty URL leaves LAN-only operation intact.
	HubServerURL string

	// CCUsageFetcher 注入 claudecode.usage handler 用的 OAuth 拉取函数。
	// 留空 → 走 ccoauth.NewLocalFetcher()(从当前机器环境读 token + 调真实 endpoint);
	// 集成测试传入 stub 屏蔽真实网络 / 真实 keychain。
	CCUsageFetcher handlers.CCUsageFetcher
}

// Daemon assembles and runs all agentred sub-systems.
type Daemon struct {
	opts           Options
	state          *state.State
	gateway        *httpgateway.Gateway
	pairing        *pairing.Manager
	ratelim        *pairing.RateLimiter
	registry       *rpc.Registry
	auth           *rpc.AuthHandlers
	engineSnapshot *enginesnapshot.Manager

	// db is agentred's own SQLite handle (session durability: daemon_sessions
	// + daemon_notification_logs — see internal/daemon/migrations). Deliberately
	// a per-instance field, never a package-level global set via db.SetDefault:
	// integration_test.go constructs several Daemon values in one test process,
	// and a global would make them silently share one database. Callers reach
	// it through ctx (db.WithContextDB at the Run ctx boundary), never directly.
	db *gorm.DB

	// journal 是通知日志的写入口,Daemon 级一份(会话日志按 (对端, 会话) 分区,不随
	// 连接生灭 —— 断连重连不重置任何序号)。
	journal handlers.JournalPort

	// sessionStore 是会话身份与生命周期的存取口,同样 Daemon 级。
	sessionStore daemonSessionStore

	// catchup 是断连重连的补齐族 handler(清单 / 拉取 / 待决策 / 接管)。它按对端
	// 限定、不随连接生灭,所以是 Daemon 级构造、静态注册的 —— 唯一的例外是显式接管,
	// 它要知道是**哪条连接**在接管,见 bindConn。
	catchup *handlers.SessionCatchupHandlers

	// sessionDelete 是会话删除 handler。与补齐族同为 Daemon 级、静态注册:它按对端
	// 限定、改的是库而不是「通知推给谁」,与哪条连接在调用无关。
	sessionDelete *handlers.SessionDeleteHandlers

	mu  sync.RWMutex
	lan *rpc.LANServer
	hub *rpc.HubLink
	mux *rpc.Multiplexer

	// conns 是 daemon 的推送路由表:会话通知按**会话**解析到发起它的那条连接,
	// MCP 反向隧道从同一份状态里解析目标,daemon 上没有第二个「当前连接」的全局。
	conns connRegistry

	// steerSource 是「queuedID → 提交方对端」的映射(R17),Daemon 级一份:Steer RPC
	// 可能落在任意一条连接上,而 SteerConsumed 由发起会话那条连接的 fanout 发出。
	steerSource *steerSourceStore

	// generations 是 Daemon 级的 generation 属主表:一条会话上在飞的那一个 generation
	// 属于创建它的那条连接 + 那个不透明 token。重连必须等旧属主完成清理才拿得到它,
	// 而迟到的旧连接清理也顶不掉重连的 generation(见 sessions.Registry)。
	generations *sessions.Registry

	// runtimeHandlers 记住每条连接的 RuntimeHandlers,只为**关机时**能把每条连接
	// 拥有的 Pi generation 逐个收掉(见 closeRuntimeConnections)。连接自己断开时
	// 由 bindConn 的 Done 监视直接调它那一个 rh.Close。
	runtimeMu       sync.RWMutex
	runtimeHandlers map[*rpc.Conn]*handlers.RuntimeHandlers
}

const daemonConnectionCleanupTimeout = 3 * time.Second

// cliSessionSweepInterval 是 idle CLI 会话清扫的巡检间隔。
const cliSessionSweepInterval = time.Minute

// sessionKey 是 daemon 侧的会话身份(R16):(对端设备指纹, 对端会话 id)。会话 id 是
// 各客户端本地自增的,两个对端各自持有同一个 id 时是两条互不相干的会话。
type sessionKey struct {
	peer string
	sid  int64
}

// connRegistry 记录 daemon 此刻能把通知推给谁,两张互补的表:
//   - live:已认证且还活着的连接。登记只发生在 auth.pair / auth.connect / auth.account 成功那一刻
//     (bindConn 是 LANServer 的 OnConn 回调,跑在鉴权**之前**),所以完成 WS 升级却
//     从不认证的连接(LAN 扫描器 / 鉴权失败的客户端 / 掉队的重连)根本进不来;
//   - claims:每条会话的推送目标 —— **发起该会话的那条连接**。
//
// 为什么推送目标不能按设备指纹解析:一台桌面端会同时开 2-3 条**同指纹**的已认证连接
// —— 连接池那条承载会话(chat_svc 的 remote.New(lease.Client())),设备监视心跳与刷新
// 探测各占一条。按指纹索引时,后认证的心跳连接会抢走正在跑的会话的通知(daemon 侧推送
// 「成功」,而发起会话的那条一条也收不到,没有错误也没有 seq 跳号),它关闭时还会把正在
// 用的那条一并删掉(此后只落库不推送)。两种表现都是整轮无限期卡住,且不需要任何异常连接。
//
// 接管规则:一条会话的推送目标是最近为它发过 runtime.* 的那条连接,起点是创建它的
// runtime.run(见 trackSessionOwner)。key 里的指纹取自发起调用的那条连接自己的
// rpc.AuthState,所以只有**同指纹**的连接接得走一条会话 —— 指纹是接管的授权,不是路由
// 键。属主连接断开 → 该会话的登记随之撤销 → 通知照常落库、不推送(会话挂起,R2),
// 等同指纹的新连接再发一次 runtime.* 接管(与重连补齐是同一件事)。
//
// 不变量:claims 里的连接必然同时在 live 里(remove 在同一把锁下一起清)。
//
// 多客户端同时在场:一条会话的推送**目标**仍是 claims 里那一条(发起 / 已授权接管它
// 的连接),但推送的**收件人**是一个集合 —— subs 里登记的、**上过这条会话**的那些同账号
// 连接(发起它的,以及按 R12 显式接管 / 控制过它的),桌面与手机因此同时收到同一会话的
// 实时事件。未认领 daemon 没有账号可言,一个订阅者都不成立,行为与单目标时代完全一致
// (R13)。MCP 反向隧道不在扇出之列:它按会话解析到发起端那一条(tunnelTargetFor,决策 9)。
//
// 收件人为什么必须按**会话**而不是按账号取:推出去的帧只带一个 sessionId
// (wire.EventFrame / RunResultDoneFrame / AutonomousTurnStartedFrame),而会话主键是
// sessionKey 的两段。把一条会话的事件推给一条从没上过它的同账号连接,对方只剩裸
// sessionId 可用,只能落进它**自己**那条同号会话 —— 别人的转录被写进你的对话。R12 放宽
// 的是可见性的**过滤条件**(list / attach 能看到并操作全部会话),「会话主键结构不变」。
type connRegistry struct {
	// claimedAccountID 交回 daemon 此刻的归属账号(未认领为空)。订阅资格拿它与连接
	// 自己的 AuthState.AccountID 比 —— 每次解析时现问,不缓存:解除归属(R19)之后
	// 那些还连着的连接必须立刻失去订阅资格。留空(零值 connRegistry)= 未认领。
	claimedAccountID func() string

	mu     sync.Mutex
	seq    uint64
	live   map[*rpc.Conn]liveConn
	claims map[sessionKey]sessionClaim
	// subs 是每条会话的订阅者集合:上过这条会话的那些连接。属主换人不清空它 ——
	// 接管的语义是「此后由我消费」,不是「把另一方踢下线」。
	subs map[sessionKey]map[*rpc.Conn]struct{}
}

// liveConn is an authenticated connection's push port.
//
// n 是同步端口,会话属主走它 —— 推送失败要如实回到 sessionEmitter(「只落库不推送」
// 的判定靠这个返回值)。fanout 是这条连接**作为订阅者**时的异步端口,只有带账号身份的
// 连接才有:一个卡住的订阅者不得阻塞会话本身与其余订阅者,所以扇出那一路一律经它排队。
type liveConn struct {
	n      handlers.NotifierPort
	fanout *asyncNotifier
}

// subscriberQueueDepth 是每个订阅者的投递缓冲深度。写满 = 这个订阅者已经落后这么多帧,
// 继续排队只会无界吃内存:此时丢弃并记日志,客户端按帧上的 seq 看到跳号,走既有的游标
// 补齐(R6)把缺口拉回来 —— 通知本来就已经落库了。
const subscriberQueueDepth = 256

// queuedNotification 是排队中的一条通知。params 在**入队时**就序列化好:调用方
// (sessionEmitter)手里那个帧是可变的(SetSeq 写的就是它),留到投递时才 marshal
// 会读到被后一条通知改过的内容。
type queuedNotification struct {
	method string
	params json.RawMessage
}

// asyncNotifier 是一个订阅者的投递队列:一条 goroutine 顺序发,这个订阅者收到的帧因此
// 仍是原序;入队不阻塞调用方,所以慢的、写不动的订阅者只影响它自己。
type asyncNotifier struct {
	n    handlers.NotifierPort
	ch   chan queuedNotification
	stop chan struct{}
	once sync.Once
}

func newAsyncNotifier(n handlers.NotifierPort) *asyncNotifier {
	a := &asyncNotifier{
		n:    n,
		ch:   make(chan queuedNotification, subscriberQueueDepth),
		stop: make(chan struct{}),
	}
	go a.run()
	return a
}

func (a *asyncNotifier) run() {
	for {
		select {
		case <-a.stop:
			return
		case q := <-a.ch:
			if err := a.n.Notify(q.method, q.params); err != nil {
				log.Printf("daemon: fan-out to subscriber failed method=%s err=%v", q.method, err)
			}
		}
	}
}

func (a *asyncNotifier) Notify(method string, params any) error {
	payload, err := json.Marshal(params)
	if err != nil {
		return err
	}
	select {
	case a.ch <- queuedNotification{method: method, params: payload}:
		return nil
	default:
		log.Printf("daemon: subscriber is %d frames behind; dropped %s (it catches up by cursor)",
			subscriberQueueDepth, method)
		return fmt.Errorf("daemon: subscriber queue full, dropped %s", method)
	}
}

func (a *asyncNotifier) Request(context.Context, string, any, any) error {
	// 反向请求(MCP 隧道)按会话解析到发起端那一条连接,从不经过订阅者(决策 9)。
	return errors.New("daemon: subscriber ports do not carry reverse requests")
}

// close 停掉投递 goroutine。幂等:连接关闭与重新鉴权都会走到它。
func (a *asyncNotifier) close() { a.once.Do(func() { close(a.stop) }) }

// fanoutNotifier 是一条会话的推送出口:属主连接同步收,同账号的其余订阅者各自异步收。
// 它只在真有订阅者时才出现 —— 单目标时 ownerOf 交回的仍是那条连接的端口本身。
//
// 属主那一路刻意保持同步:推送失败要如实回给 sessionEmitter(R2/R3 的「只落库不推送」
// 判定就靠这个返回值),而未认领 daemon 上只有这一路,行为因此与扇出之前逐字节相同。
// 新增的订阅者一律走异步端口,所以「多接一个客户端」不会给会话添一个能把它卡死的单点。
type fanoutNotifier struct {
	primary handlers.NotifierPort
	extras  []handlers.NotifierPort
}

func (f fanoutNotifier) Notify(method string, params any) error {
	var delivered bool
	var lastErr error
	for _, extra := range f.extras {
		if err := extra.Notify(method, params); err != nil {
			lastErr = err
			continue
		}
		delivered = true
	}
	if f.primary != nil {
		return f.primary.Notify(method, params)
	}
	// 属主已经断开,只剩订阅者:全都投递不出去才算这条通知没推出去。
	if delivered {
		return nil
	}
	return lastErr
}

func (f fanoutNotifier) Request(ctx context.Context, method string, params any, result any) error {
	if f.primary == nil {
		return errors.New("daemon: session has no owner connection for a reverse request")
	}
	return f.primary.Request(ctx, method, params, result)
}

// sessionClaim 记住会话此刻的属主连接本身:撤销要按**连接身份**做,不能按指纹 ——
// 同一台设备的另一条连接来去,不得影响正在跑的会话。
type sessionClaim struct {
	// conn receives this session's notifications and may change on an authorized
	// attach/control request. mcpConn remains the connection of the peer that
	// initiated the session, so a cross-account control cannot reroute tools.
	conn    *rpc.Conn
	mcpConn *rpc.Conn
	at      uint64
}

// peerIdentity 取连接的对端身份(设备指纹)。身份是鉴权成功那一刻才成立的,报了指纹
// 没通过鉴权不算;空指纹不构成可匹配身份(否则一条空指纹连接就能冒领会话的通知),
// 空指纹在 registerMethods 的 auth.* 入参处就已挡下,这里是第二道。
func peerIdentity(c *rpc.Conn) (string, bool) {
	if c == nil {
		return "", false
	}
	auth := c.Auth()
	if !auth.Authenticated || auth.DeviceFingerprint == "" {
		return "", false
	}
	return auth.DeviceFingerprint, true
}

// connClosed 报告连接是否已经关闭。登记前必须查一次:Done 监视 goroutine 与登记是并发
// 的,先关后登记会在表里留下一条指向死连接的陈旧条目,永远等不到清理。
func connClosed(c *rpc.Conn) bool {
	select {
	case <-c.Done():
		return true
	default:
		return false
	}
}

// add 在连接完成 auth.pair / auth.connect / auth.account 之后登记它。同一条连接改认另一个指纹时,
// 它先前以旧指纹认领的会话一并作废 —— 否则旧对端的会话通知会推给一条已经属于别人的连接。
func (r *connRegistry) add(c *rpc.Conn, n handlers.NotifierPort) {
	if n == nil {
		return
	}
	if _, ok := peerIdentity(c); !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dropLocked(c) // 重新鉴权 = 重置这条连接的会话认领(它可以再发 runtime.* 认回来)
	if connClosed(c) {
		return
	}
	if r.live == nil {
		r.live = map[*rpc.Conn]liveConn{}
	}
	entry := liveConn{n: n}
	// 只有带账号身份的连接(auth.account,见任务 6 的账号门)才可能成为订阅者,
	// 也只有它需要那条投递 goroutine —— 纯 LAN 配对的 daemon 因此一条也不起。
	if c.Auth().AccountID != "" {
		entry.fanout = newAsyncNotifier(n)
	}
	r.live[c] = entry
}

// claimTicket 是一次认领的回执,交给 undoClaim 还原用(见 trackSessionOwner:认领跑在
// handler 之前,handler 拒了这一条就得还原)。ok 为假表示这次调用什么都没改。
type claimTicket struct {
	key  sessionKey
	conn *rpc.Conn
	at   uint64
	prev sessionClaim
	ok   bool
	// addedSub 记这次认领是不是**新**把这条连接加进该会话的订阅者集合。被拒的调用
	// 不该让调用方留在会话里旁听,还原时按它撤回;它本来就在里面时不动。
	addedSub bool
}

// claim records a caller's own-peer session target. Account-authorized cross-peer
// operations use claimFor after their origin discriminator has been checked.
func (r *connRegistry) claim(c *rpc.Conn, sid int64) claimTicket {
	peer, ok := peerIdentity(c)
	if !ok {
		return claimTicket{}
	}
	return r.claimFor(c, peer, sid)
}

// claimFor records an already-authorized target peer. Normal callers use
// claim; account-level controls reach this only after ResolveSessionPeer.
func (r *connRegistry) claimFor(c *rpc.Conn, peer string, sid int64) claimTicket {
	if peer == "" || sid == 0 {
		return claimTicket{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, live := r.live[c]; !live {
		return claimTicket{}
	}
	if connClosed(c) {
		r.dropLocked(c)
		return claimTicket{}
	}
	if r.claims == nil {
		r.claims = map[sessionKey]sessionClaim{}
	}
	r.seq++
	k := sessionKey{peer: peer, sid: sid}
	prev := r.claims[k]
	mcpConn := prev.mcpConn
	if _, live := r.live[mcpConn]; !live {
		// A session run arrives from its own peer and pins that exact connection.
		// A cross-peer attach/control may never substitute its caller: use an
		// already-live connection of the persisted origin or leave it unavailable.
		if callerPeer, ok := peerIdentity(c); ok && callerPeer == peer {
			mcpConn = c
		} else {
			mcpConn = r.liveForPeerLocked(peer)
		}
	}
	r.claims[k] = sessionClaim{conn: c, mcpConn: mcpConn, at: r.seq}
	added := r.addSubLocked(k, c)
	return claimTicket{key: k, conn: c, at: r.seq, prev: prev, ok: true, addedSub: added}
}

// addSubLocked 把这条连接登记为该会话的订阅者,返回它是不是新加进去的。只有带账号
// 身份的连接(有 fanout 端口)才构成订阅者 —— 纯 LAN 配对的连接不参与扇出(R13)。
func (r *connRegistry) addSubLocked(k sessionKey, c *rpc.Conn) bool {
	if r.live[c].fanout == nil {
		return false
	}
	if r.subs == nil {
		r.subs = map[sessionKey]map[*rpc.Conn]struct{}{}
	}
	set := r.subs[k]
	if set == nil {
		set = map[*rpc.Conn]struct{}{}
		r.subs[k] = set
	}
	if _, ok := set[c]; ok {
		return false
	}
	set[c] = struct{}{}
	return true
}

// removeSubLocked 摘掉一份订阅;集合空了连键一起删,免得会话表随会话数无界长。
func (r *connRegistry) removeSubLocked(k sessionKey, c *rpc.Conn) {
	set := r.subs[k]
	if set == nil {
		return
	}
	delete(set, c)
	if len(set) == 0 {
		delete(r.subs, k)
	}
}

// undoClaim 撤回一次认领,把属主还原成认领之前的那条连接(没有前主就还原成「无属主」)。
// 还原**不能**简单地删掉这条会话:那会让一个正在被推送的会话平白挂起。三条守则:
//   - 认领已经被更晚的一次接管取代(at 对不上)→ 一概不动,后来者说了算;
//   - 前主此刻已经不在活连接表里(处理期间掉线 / 改认了别的指纹)→ 不写回去,
//     否则表里留下一条指向死连接、或指向已属于别人的连接的条目;
//   - 认领自己已经被撤销(属主连接刚关)→ 前主仍在线时把它还原回来,它才是属主。
func (r *connRegistry) liveForPeerLocked(peer string) *rpc.Conn {
	for c := range r.live {
		if fingerprint, ok := peerIdentity(c); ok && fingerprint == peer {
			return c
		}
	}
	return nil
}

func (r *connRegistry) undoClaim(t claimTicket) {
	if !t.ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if t.addedSub {
		r.removeSubLocked(t.key, t.conn)
	}
	if cur, exists := r.claims[t.key]; exists && cur.at != t.at {
		return
	}
	if fp, ok := peerIdentity(t.prev.conn); ok && fp == t.key.peer {
		if _, live := r.live[t.prev.conn]; live {
			r.claims[t.key] = t.prev
			return
		}
	}
	delete(r.claims, t.key)
}

// remove 撤销这条连接的一切登记(连接关闭时调用)。按连接身份撤销:同一台设备的其它
// 连接、以及重连后的新连接,都不受这条迟到的清理影响。它认领过的会话就此挂起 ——
// 通知继续落库、不推送,等同指纹的新连接接管。
func (r *connRegistry) remove(c *rpc.Conn) {
	if c == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dropLocked(c)
}

func (r *connRegistry) dropLocked(c *rpc.Conn) {
	if lc, ok := r.live[c]; ok && lc.fanout != nil {
		lc.fanout.close() // 摘掉这一份订阅,其余订阅者与它们的队列不受影响
	}
	delete(r.live, c)
	for k, cl := range r.claims {
		if cl.conn == c {
			delete(r.claims, k)
		}
	}
	// 掉一条连接只摘掉它自己那份订阅:同一条会话上的其余客户端继续收实时事件。
	for k := range r.subs {
		r.removeSubLocked(k, c)
	}
}

// ownerOf 返回该会话此刻的推送端口:属主连接 + **这条会话上**的其余同账号订阅者。
// 一个收件人都没有时 nil。
func (r *connRegistry) ownerOf(k sessionKey) handlers.NotifierPort {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.portForLocked(k)
}

// portForLocked 解出该会话此刻的推送出口。属主可能已经不在(它断开了、而同账号的另一个
// 客户端还连着),订阅者也可能一个都没有(未认领 daemon);两者都空才算这条会话没有出口。
func (r *connRegistry) portForLocked(k sessionKey) handlers.NotifierPort {
	if k.peer == "" { // 空指纹不是可匹配身份,不得据此解析出任何收件人
		return nil
	}
	var primary handlers.NotifierPort
	var owner *rpc.Conn
	if cl, ok := r.claims[k]; ok {
		owner = cl.conn
		primary = r.live[cl.conn].n
	}
	extras := r.subscribersLocked(k, owner)
	if len(extras) == 0 {
		return primary // 单目标:交回那条连接的端口本身,不套壳
	}
	return fanoutNotifier{primary: primary, extras: extras}
}

// subscribersLocked 交回**这条会话**的订阅者端口,除 exclude(会话属主,它走同步端口)。
//
// 订阅资格两条,缺一不可:
//   - 这条连接上过这条会话(在 subs[k] 里)—— 帧上只有裸 sessionId,推给没上过它的
//     连接就是把别人的转录塞进对方自己那条同号会话(见 connRegistry 的注释);
//   - daemon 已被某个账号认领,且这条连接是**同一个账号**认证进来的(auth.account 把
//     归属账号写进 AuthState.AccountID,见任务 6 的账号门)。归属账号每次现问,不缓存
//     —— 解除归属(R19)之后那些还连着的连接必须立刻失去订阅资格。
//
// 未认领 daemon 一个订阅者都没有;只走 LAN 配对的连接也没有:配对是设备级信任,不是
// 账号级可见性,多个配对对端不保证属于同一个人(R13)。
func (r *connRegistry) subscribersLocked(k sessionKey, exclude *rpc.Conn) []handlers.NotifierPort {
	if r.claimedAccountID == nil {
		return nil
	}
	claimed := r.claimedAccountID()
	if claimed == "" {
		return nil
	}
	var out []handlers.NotifierPort
	for c := range r.subs[k] {
		if c == exclude {
			continue
		}
		lc, live := r.live[c]
		if !live || lc.fanout == nil {
			continue
		}
		if c.Auth().AccountID != claimed {
			continue
		}
		out = append(out, lc.fanout)
	}
	return out
}

// routerFor 返回该对端的会话通知出口;该对端的会话此刻一个收件人都没有时返回 nil ——
// 调用方(handlers 的 sessionEmitter)据此走「只落库、不推送」的挂起路径。
func (r *connRegistry) routerFor(peer string) handlers.NotifierPort {
	if peer == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for k := range r.claims {
		if k.peer == peer {
			return sessionRouter{reg: r, peer: peer}
		}
	}
	// 属主都断开了,但这个对端还有会话留着订阅者:会话不算挂起,实时流继续推给它们。
	for k := range r.subs {
		if k.peer != peer {
			continue
		}
		if len(r.subscribersLocked(k, nil)) > 0 {
			return sessionRouter{reg: r, peer: peer}
		}
	}
	return nil
}

// tunnelTargetFor resolves the exact session origin carried in the daemon-local
// MCP URL. There is intentionally no newest-connection fallback: a missing
// originating peer is an unavailable tool, not permission to cross-route it.
// 会话通知的订阅者集合(见 subscribersLocked)在这里**没有**位置:内置工具的实现与数据
// 在发起端本地,把工具请求扇出给同账号的其它客户端就是决策 9 明确否掉的那件事。
func (r *connRegistry) tunnelTargetFor(peer string, sid int64) handlers.NotifierPort {
	if peer == "" || sid <= 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	claim, ok := r.claims[sessionKey{peer: peer, sid: sid}]
	if !ok {
		return nil
	}
	return r.live[claim.mcpConn].n
}

// sessionRouter 是某个对端的会话通知出口:按帧上的 sessionId 把每条通知交给发起该会话
// 的那条连接。sessionId 本来就是通知帧上的会话路由字段(见 wire 包注释),daemon 侧按它
// 解析没有引入任何协议内容。
type sessionRouter struct {
	reg  *connRegistry
	peer string
}

func (s sessionRouter) Notify(method string, params any) error {
	sid, ok := frameSessionID(params)
	if !ok {
		return fmt.Errorf("daemon: cannot route %s: frame %T carries no sessionId", method, params)
	}
	n := s.reg.ownerOf(sessionKey{peer: s.peer, sid: sid})
	if n == nil {
		// 发起该会话的连接已经断开:通知已经落库,等同指纹的新连接接管后补齐。
		return fmt.Errorf("daemon: session %d has no live connection", sid)
	}
	return n.Notify(method, params)
}

func (s sessionRouter) Request(context.Context, string, any, any) error {
	// Reverse requests (the MCP tunnel) resolve their explicit peer/session URL
	// discriminator through connRegistry.tunnelTargetFor instead.
	return errors.New("daemon: reverse requests are not routable per session")
}

// frameSessionID 取通知帧上的会话 id。R1 的五类会话通知共用三种帧,每种都以 sessionId
// 做会话路由。新增一类通知帧时必须在这里补一行 —— 漏了会以 error 形式在日志里点名具体
// 类型,而不是静默推给别的连接。
func frameSessionID(params any) (int64, bool) {
	switch f := params.(type) {
	case *wire.EventFrame:
		return f.SessionID, true
	case *wire.RunResultDoneFrame:
		return f.SessionID, true
	case *wire.AutonomousTurnStartedFrame:
		return f.SessionID, true
	}
	return 0, false
}

// notifierForPeer 解析某个对端的推送出口。每次发送时重新解析,绝不静态捕获 —— 断连
// 重连会换一条连接,捕获下来的端口在重连后指向死连接。
func (d *Daemon) notifierForPeer(peer string) handlers.NotifierPort {
	return d.conns.routerFor(peer)
}

// tunnelTargetFor resolves a daemon-local MCP request to its originating
// session owner; no global active-connection heuristic is permitted.
func (d *Daemon) tunnelTargetFor(peer string, sid int64) handlers.NotifierPort {
	return d.conns.tunnelTargetFor(peer, sid)
}

func (d *Daemon) claimedAccountID() string { return d.state.Snapshot().AccountID }

// currentAccessToken returns the daemon's freshest stored access token. HubLink
// re-resolves it at every dial (via HubLinkOptions.AccessTokenProvider), so a
// token refreshed mid-connection is picked up by the next reconnect (R4/R14).
func (d *Daemon) currentAccessToken() string {
	return d.state.Snapshot().Credential.AccessToken
}

// relayServerURL 在每次 dial 前重新解析中转端点，返回 "" 表示这台 daemon 还没有
// 账号可连。HubLink 收到空值会退避重试而不是把链路当作不存在。
//
// 未认领时**先读一次盘**：`agentred login` 是另一个进程，它把凭据写进 state.json
// 就退出。daemon 手里是启动时读到的内存副本，不重新读盘就永远发现不了自己已经被
// 认领 —— 那正是「先起服务、后登录」恒离线的原因。
func (d *Daemon) relayServerURL() string {
	if !d.state.IsClaimed() {
		if _, err := d.state.AdoptClaimFromDisk(); err != nil {
			log.Printf("daemon.relayServerURL: re-read state.json failed: %v", err)
		}
	}
	snap := d.state.Snapshot()
	if snap.AccountID == "" {
		// 没有账号就没有端点。这里返回空而不是返回一个「配置里写着的」地址：
		// 带着空 Bearer 去连只会换来一串 401，把「还没登录」伪装成「登录了但被拒」。
		return ""
	}
	if d.opts.HubServerURL != "" {
		return d.opts.HubServerURL
	}
	return snap.HubServerURL
}

// New constructs a Daemon from Options. It loads persistent state, creates
// sub-systems, and registers all static (non-per-conn) RPC methods.
func New(opts Options) (*Daemon, error) {
	st, err := state.Load(opts.DataDir)
	if err != nil {
		return nil, err
	}
	gormDB, err := openDB(opts.DataDir)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := daemonmigrations.RunMigrations(gormDB); err != nil {
		closeDB(gormDB)
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	// 注入仓储默认实现,让 notification_repo.Notification() 拿到 GORM 版。New 是
	// agentred 的组装根,位置对应桌面端 internal/bootstrap/cago.go 里 RunMigrations
	// 之后的那批 RegisterXxx。实现本身无状态(句柄经 ctx 传),同进程多个 Daemon
	// 注册同一个实现互不干扰。
	notification_repo.RegisterNotification(notification_repo.NewNotification())
	session_repo.RegisterSession(session_repo.NewSession())
	// R10:daemon 启动时把库里全部非终态会话标记为已中断。它们的子进程随上一个 daemon
	// 进程消亡了,不扫的话客户端重连后会看到一批 running 的僵尸会话、接管上去无限期等待。
	// 清扫失败即 New 失败:扫不动说明库本身有问题,而通知落库也走同一个库,让 daemon
	// 带着一个坏库继续跑只会把问题推迟到看不见的地方。
	swept, err := session_repo.Session().InterruptAll(
		dbpkg.WithContextDB(context.Background(), gormDB), wire.SessionLifecycleInterrupted)
	if err != nil {
		closeDB(gormDB)
		return nil, fmt.Errorf("interrupt stale sessions: %w", err)
	}
	if swept > 0 {
		log.Printf("daemon.New: marked %d non-terminal sessions interrupted after restart", swept)
	}
	reg := rpc.NewRegistry()

	pmOpts := pairing.ManagerOpts{TTL: 5 * time.Minute}
	if st.Preferences.PairingCodeTTLSeconds > 0 {
		pmOpts.TTL = time.Duration(st.Preferences.PairingCodeTTLSeconds) * time.Second
	}
	pm := pairing.NewManager(pmOpts)
	rlOpts := pairing.RateLimitOpts{MaxAttempts: 3, Window: 60 * time.Second}
	if st.Preferences.PairingRateLimit.MaxAttemptsPerIP > 0 {
		rlOpts.MaxAttempts = st.Preferences.PairingRateLimit.MaxAttemptsPerIP
	}
	if st.Preferences.PairingRateLimit.WindowSeconds > 0 {
		rlOpts.Window = time.Duration(st.Preferences.PairingRateLimit.WindowSeconds) * time.Second
	}
	rl := pairing.NewRateLimiter(rlOpts)
	auth := rpc.NewAuthHandlers(st, pm, rl)

	d := &Daemon{
		opts: opts, state: st, db: gormDB,
		journal:      notificationJournal{db: gormDB},
		sessionStore: daemonSessionStore{db: gormDB},
		pairing:      pm, ratelim: rl,
		registry: reg, auth: auth,
		steerSource:     newSteerSourceStore(),
		generations:     sessions.NewRegistry(),
		runtimeHandlers: map[*rpc.Conn]*handlers.RuntimeHandlers{},
	}
	// 订阅资格的账号门:登记表每次解析收件人时现问 daemon 此刻的归属账号。
	d.conns.claimedAccountID = d.claimedAccountID
	// 中转链路无条件构造：认领状态改由每次 dial 时重新解析（relayServerURL），
	// 不在这里判一次。判一次的后果是未认领启动的进程即使之后登录了也永远没有链路
	// —— login 是另一个进程，写完 state.json 就退出，没有东西会回来建它。
	d.hub = rpc.NewHubLink(rpc.HubLinkOptions{
		ServerURLProvider:   d.relayServerURL,
		AccessTokenProvider: d.currentAccessToken,
	})
	d.mux = rpc.NewMultiplexer(d.hub)
	d.engineSnapshot = enginesnapshot.New(enginesnapshot.Options{
		State:       d.state,
		ServerURL:   d.relayServerURL,
		AccessToken: d.currentAccessToken,
	})
	d.catchup = handlers.NewSessionCatchupHandlers(handlers.SessionCatchupDeps{
		Sessions:         d.sessionStore,
		Journal:          journalReader{db: gormDB},
		ClaimedAccountID: d.claimedAccountID,
	})
	d.sessionDelete = handlers.NewSessionDeleteHandlers(handlers.SessionDeleteDeps{
		Sessions:         d.sessionStore,
		Journal:          journalPurger{db: gormDB},
		ClaimedAccountID: d.claimedAccountID,
	})
	d.gateway = httpgateway.New("127.0.0.1", 0, NewProviderLookup(st))
	// 内置工具 MCP(org/subagent/group/workflow)隧道:daemon 上 CLI 子进程把请求打到
	// 本机 gateway 的 /mcp/*(URL 已由 runtime.Run 改写成 daemon base),这里捕获后反向
	// 请求回 desktop 执行(真 handler 在 desktop)。仅一条 catch-all,serveMCP 最长前缀
	// 匹配下命中所有 /mcp/* 路径。
	d.gateway.RegisterMCP(httpgateway.RouteMCPPrefix, handlers.NewMCPTunnelHandler(d.tunnelTargetFor))
	d.registerMethods()
	return d, nil
}

// requireAuth returns ErrUnauthorized when the calling connection has not
// completed auth.pair / auth.connect / auth.account. Called by every non-auth handler.
func requireAuth(ctx context.Context) error {
	c := rpc.ConnFromContext(ctx)
	if c == nil || !c.Auth().Authenticated {
		return rpc.ErrUnauthorized
	}
	return nil
}

// requireClaimed restricts account-scoped operations to daemons that have been
// claimed by an account. The nested handler retains the standard RPC auth and
// panic handling provided by wrapGuarded.
func (d *Daemon) requireClaimed(next rpc.HandlerFunc) rpc.HandlerFunc {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		if !d.state.IsClaimed() {
			return nil, rpc.ErrUnauthorized
		}
		return next(ctx, raw)
	}
}

// registerMethods installs all static (non-per-connection) RPC handlers.
func (d *Daemon) registerMethods() {
	d.registry.Register("auth.pair", func(ctx context.Context, p json.RawMessage) (any, error) {
		var pp rpc.PairParams
		if err := jsonUnmarshal(p, &pp); err != nil {
			return nil, rpc.ErrInvalidParams
		}
		// 空指纹不是可匹配身份:rpc/auth.go 的 HandlePair 不拒绝它,配对下来会在
		// PairedPeers 里留一条空键的对端,之后任何 auth.connect 都能顶着空指纹认证成功。
		if pp.DeviceFingerprint == "" {
			return nil, rpc.ErrInvalidParams
		}
		ip := ipFromContext(ctx)
		res, err := d.auth.HandlePair(ctx, ip, pp)
		if err != nil {
			return nil, err
		}
		if c := rpc.ConnFromContext(ctx); c != nil {
			c.SetAuth(rpc.AuthState{
				Authenticated:     true,
				DeviceFingerprint: pp.DeviceFingerprint,
				DeviceName:        pp.DeviceName,
			})
			d.conns.add(c, notifier.New(c))
		}
		return res, nil
	})
	d.registry.Register("auth.connect", func(ctx context.Context, p json.RawMessage) (any, error) {
		var cp rpc.ConnectParams
		if err := jsonUnmarshal(p, &cp); err != nil {
			return nil, rpc.ErrInvalidParams
		}
		if cp.DeviceFingerprint == "" { // 同 auth.pair:空指纹不是可匹配身份
			return nil, rpc.ErrInvalidParams
		}
		res, err := d.auth.HandleConnect(ctx, cp)
		if err != nil {
			return nil, err
		}
		if c := rpc.ConnFromContext(ctx); c != nil {
			c.SetAuth(rpc.AuthState{
				Authenticated:     true,
				DeviceFingerprint: cp.DeviceFingerprint,
			})
			d.conns.add(c, notifier.New(c))
		}
		return res, nil
	})
	d.registry.Register("auth.account", func(ctx context.Context, p json.RawMessage) (any, error) {
		var ap rpc.AccountParams
		if err := jsonUnmarshal(p, &ap); err != nil {
			return nil, rpc.ErrInvalidParams
		}
		if ap.DeviceFingerprint == "" {
			return nil, rpc.ErrInvalidParams
		}
		res, err := d.auth.HandleAccount(ctx, ap)
		if err != nil {
			return nil, err
		}
		if c := rpc.ConnFromContext(ctx); c != nil {
			c.SetAuth(rpc.AuthState{
				Authenticated:     true,
				DeviceFingerprint: ap.DeviceFingerprint,
				AccountID:         d.claimedAccountID(),
			})
			d.conns.add(c, notifier.New(c))
		}
		return res, nil
	})
	d.registry.Register("auth.revoke", wrapGuarded(func(ctx context.Context, params struct {
		DeviceFingerprint string `json:"deviceFingerprint"`
	}) (handlers.OK, error) {
		if err := d.auth.HandleRevoke(ctx, params.DeviceFingerprint); err != nil {
			return handlers.OK{}, err
		}
		return handlers.OK{OK: true}, nil
	}))

	llmH := handlers.NewLLMHandlers(d.state)
	d.registry.Register("llm.upsert", wrapGuarded(llmH.Upsert))
	d.registry.Register("llm.delete", wrapGuarded(llmH.Delete))
	d.registry.Register("llm.list", wrapGuardedNoParams(llmH.List))

	engineH := handlers.NewEngineHandlers(handlers.EngineDeps{State: d.state})
	d.registry.Register("engine.test", d.requireClaimed(wrapGuarded(engineH.Test)))
	d.registry.Register("engine.discover", d.requireClaimed(wrapGuarded(engineH.Discover)))
	d.registry.Register("engine.scan", d.requireClaimed(wrapGuardedNoParams(engineH.Scan)))

	cliH := handlers.NewCLIHandlers(d.gateway, NewProviderLookup(d.state))
	d.registry.Register("cli.resolvePath", wrapGuarded(cliH.ResolvePath))
	d.registry.Register("cli.probe", wrapGuarded(cliH.Probe))

	healthH := handlers.NewHealthHandlers(d.state.InstanceUUID(), d.state, d)
	d.registry.Register("health.ping", wrapGuardedNoParams(healthH.Ping))

	// claudecode.usage:agentred 在它自己所在机器上读 Claude Code 的 OAuth 凭证
	// 并调 api.anthropic.com/api/oauth/usage,返回 5h/7d 配额给桌面 HUD。每台
	// device 的配额是该机器登录账号的,所以必须就地读不能由桌面代理。
	ccFetcher := d.opts.CCUsageFetcher
	if ccFetcher == nil {
		ccFetcher = ccoauth.NewLocalFetcher()
	}
	ccUsageH := handlers.NewCCUsageHandlers(ccFetcher)
	d.registry.Register("claudecode.usage", wrapGuardedNoParams(ccUsageH.Get))

	// skills.list:在 daemon 本机枚举已装技能包(= `claude plugin list --json`),供
	// desktop 给远端 agent 配 per-agent 技能时展 daemon 真实可用集(而非 desktop 的)。
	skillsH := handlers.NewSkillsHandlers()
	d.registry.Register("skills.list", wrapGuarded(skillsH.List))

	// skills.catalog:同一台机器的技能包,但答的是**画得出界面的那一份**目录
	// (已装 + 推荐,逐行标注调用方带来的那一档授权),给没有本地发现器、也拿不到
	// 推荐表的浏览器控制台用 —— 它此前只能让用户手打 skill id。
	// 与 skills.list 并存而不是替换它:desktop 要的是原始发现结果,它自己合并。
	d.registry.Register(wire.MethodSkillsCatalog, wrapGuarded(skillsH.Catalog))

	// 断连重连的补齐族(清单 / 拉取 / 待决策)。静态注册而不是随 bindConn 挂 ——
	// 它们按**对端**限定、读的是库,与「哪条连接」无关;而且它们**不**过
	// trackSessionOwner:看一眼有哪些会话、把历史拉回来,都不该顺带把实时流改指向自己。
	// 改推送目标是 MethodSessionAttach 一个人的职责(见 bindConn)。
	//
	// MethodSessionList 是 daemon 上**唯一**的「列会话」出口。曾经还有一对基于内存
	// 会话表的 session.list / session.get,但那张表没有任何写入方,它恒答空清单;
	// 两个出口并存只会让读到空清单的一方以为自己的会话没了。
	d.registry.Register(wire.MethodSessionList, wrapGuardedNoParams(d.catchup.List))
	d.registry.Register(wire.MethodSessionPull, wrapGuarded(d.catchup.Pull))
	d.registry.Register(wire.MethodSessionPendingWaiters, wrapGuarded(d.catchup.PendingWaiters))

	// 删除与补齐族同样静态注册、同样按对端限定,但它是这条 wire 上唯一会让东西消失的
	// 方法:删掉会话行与它的整段通知日志。它更不该走 trackSessionOwner —— 删一条会话
	// 顺带把实时流指向自己,是删除最不该有的副作用。
	d.registry.Register(wire.MethodSessionDelete, wrapGuarded(d.sessionDelete.Delete))

	// runtime.* RPC 族 1:1 镜像 agentruntime.Runtime + 7 个可选子接口,
	// 把远端 agentre 当成「本地」backend 跑。Handler 在 bindConn
	// 里按连接挂载到 LANServer 为每条连接克隆的私有 registry（要 NotifierPort）。

	// remotefs.Register 接受已构造好的 rpc.HandlerFunc,泛型 wrapGuarded[Req,Res] 的
	// 签名约束与其不匹配,改用 WrapFunc 闭包注入 requireAuth。
	remotefs.Register(d.registry, remotefs.NewHandlers(remotefs.Options{}),
		func(fn rpc.HandlerFunc) rpc.HandlerFunc {
			return func(ctx context.Context, raw json.RawMessage) (any, error) {
				if err := requireAuth(ctx); err != nil {
					return nil, err
				}
				return fn(ctx, raw)
			}
		})

	// workspacefs.Register 挂 workspacefs.* 方法族——刻意与 remotefs.* 分开的
	// 独立方法族(spec 设计决策 5):remotefs.* 浏览远端机器任意绝对路径,
	// workspacefs.* 浏览某个会话已解析出的工作目录。旧 daemon 不认识这三个新
	// 方法时按 JSON-RPC 协议直接回 -32601,而不是静默按 remotefs.* 的旧语义
	// 应答,版本偏斜因此可见。同样用 WrapFunc 闭包注入 requireAuth。
	daemonworkspacefs.Register(d.registry, daemonworkspacefs.NewHandlers(daemonworkspacefs.Options{}),
		func(fn rpc.HandlerFunc) rpc.HandlerFunc {
			return func(ctx context.Context, raw json.RawMessage) (any, error) {
				if err := requireAuth(ctx); err != nil {
					return nil, err
				}
				return fn(ctx, raw)
			}
		})
}

// Run starts the HTTP gateway, IPC unix socket, and LAN WebSocket server,
// blocking until ctx is canceled or a fatal error occurs.
func (d *Daemon) Run(ctx context.Context) error {
	// Inject this Daemon's own db handle at the ctx boundary (not db.SetDefault
	// — see the db field's doc comment). Everything downstream derives its ctx
	// from this one (gateway requests, IPC, per-connection RPC handlers), so a
	// repository calling db.Ctx(ctx) anywhere below this point resolves to this
	// instance's database, never another Daemon's.
	ctx = dbpkg.WithContextDB(ctx, d.db)
	// Outbound relay failures are deliberately isolated from the LAN server and
	// running sessions. HubLink owns logging, heartbeats, and retry for Run's
	// whole lifetime; the multiplexer consumes its raw-frame seam separately.
	hubCtx, hubCancel := context.WithCancel(ctx)
	d.startEngineSnapshotPulls(hubCtx)
	// 常驻 CLI 子进程的按时清扫:daemon 一跑就是几周,池的条数上限管不了「留多久」。
	agentruntime.DefaultCLISessionPool().StartIdleSweeper(ctx,
		agentruntime.DefaultIdleSessionTTL, cliSessionSweepInterval)
	go func() { _ = d.hub.Run(hubCtx) }()
	// 凭据续期与吊销拉取都以「手上已经有凭据」为前提,所以要等认领落地再挂起来 ——
	// 见 runAccountJobsWhenClaimed。中转链路本身不必等:它每次 dial 重新解析端点,
	// 解析不出来就退避重试。
	go d.runAccountJobsWhenClaimed(ctx, hubCtx, hubCancel)
	defer d.mux.Close()
	go d.serveRelayChannels(ctx, d.mux)
	if err := d.gateway.Start(ctx); err != nil {
		return err
	}
	if d.gateway.URL() == "" {
		// Gateway bind failed; keep going — CLI-login backends can still
		// operate without a gateway token. Structured logging is not wired
		// at daemon level yet (T18 MVP); stderr is loud enough for ops.
		fmt.Fprintln(os.Stderr, "agentred: gateway not running; providers without token will attempt CLI login")
	}
	if _, err := d.startIPC(ctx); err != nil {
		return fmt.Errorf("ipc: %w", err)
	}
	lan := rpc.NewLANServer(rpc.LANOpts{
		Host:        d.opts.LANHost,
		Port:        d.opts.LANPort,
		TLSCertFile: d.opts.TLSCertFile,
		TLSKeyFile:  d.opts.TLSKeyFile,
		Registry:    d.registry,
		OnConn:      d.bindConn,
	})
	d.mu.Lock()
	d.lan = lan
	d.mu.Unlock()
	runErr := lan.Run(ctx)
	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), daemonConnectionCleanupTimeout)
	defer cancelCleanup()
	d.shutdown(cleanupCtx)
	return runErr
}

// claimPollInterval 是未认领时重读 state.json 的间隔。只在没有账号期间生效,
// 认领一落地就不再轮询。
const claimPollInterval = 5 * time.Second

// runAccountJobsWhenClaimed 等到这台 daemon 被认领之后,再启动凭据续期与吊销拉取。
//
// 两者都以「手上已经有凭据」为前提:refresher 见到空的 refresh token 会记一行日志
// 后**永久返回**。未认领时直接起等于把它废掉 —— 之后即使登录成功,访问令牌也没人
// 续期,15 分钟后链路带着过期令牌掉线,再也回不来。
func (d *Daemon) startEngineSnapshotPulls(ctx context.Context) {
	if d.engineSnapshot == nil {
		return
	}
	// A successful physical relay dial means the daemon is back online after
	// any outage. Pull asynchronously so snapshot latency/failure cannot block
	// relay registration or an in-flight round.
	d.hub.AddLifecycleListener(func() {
		d.engineSnapshot.PullAsync(ctx, "relay_connected")
	}, nil)
	// The account channel is an independent signal-only connection. It starts
	// only after a claim exists; unclaimed daemons therefore retain their LAN
	// pairing and llm.upsert behavior without making account requests.
	go func() {
		if d.awaitClaim(ctx) {
			d.engineSnapshot.WatchAccountChannel(ctx)
		}
	}()
}

func (d *Daemon) runAccountJobsWhenClaimed(ctx, hubCtx context.Context, stopRelay context.CancelFunc) {
	if !d.awaitClaim(ctx) {
		return
	}
	// The credential refresher keeps the minute-lived access token ahead of
	// expiry; a permanently dead refresh token cancels the hub link so a
	// doomed relay is not kept alive forever (R4/R14). It never propagates
	// to Run — local sessions and LAN stay healthy either way.
	go d.runCredentialRefresh(ctx, stopRelay)
	// R4 的另一半:定期把账号的吊销列表拉到本地。挂在 hubCtx 上是因为它与中转
	// 链路共用同一份设备凭据 —— 凭据永久失效时两者一起停,最后拉到的那份列表
	// 留在 state.json 里继续本地生效(R19 承认的延迟),本地会话与 LAN 直连
	// 全程不受影响。
	go d.runRevocationPoll(hubCtx)
}

// awaitClaim 阻塞到这台 daemon 被认领,返回 false 表示 ctx 先结束了。未认领期间
// 周期性重读 state.json:认领可能是另一个进程(`agentred login`)写下的。
func (d *Daemon) awaitClaim(ctx context.Context) bool {
	for {
		if d.state.IsClaimed() {
			return true
		}
		if _, err := d.state.AdoptClaimFromDisk(); err != nil {
			log.Printf("daemon.awaitClaim: re-read state.json failed: %v", err)
		}
		if d.state.IsClaimed() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(claimPollInterval):
		}
	}
}

// runCredentialRefresh launches the R4/R14 credential refresher for the daemon's
// lifetime. stopRelay is invoked only when the refresh token is permanently dead
// (expired or revoked): the relay link is doomed because the access token can no
// longer be renewed, so the hub link is canceled rather than kept retrying a
// dead connection forever. Local sessions and the LAN server are unaffected.
func (d *Daemon) runCredentialRefresh(ctx context.Context, stopRelay context.CancelFunc) {
	newCredentialRefresher(d.state, d.opts.HubServerURL).run(ctx, stopRelay)
}

// runRevocationPoll launches the R4 revocation-list poller for the daemon's
// relay lifetime. Nothing it does can fail the daemon: a pull failure keeps the
// previously cached list, logs, and retries with backoff.
func (d *Daemon) runRevocationPoll(ctx context.Context) {
	newRevocationPoller(d.state, d.opts.HubServerURL, d.currentAccessToken).run(ctx)
}

// defaultRefreshMargin is how long before AccessTokenExpiresAt the daemon
// proactively refreshes. Task 16 makes the access TTL minute-level; a two-minute
// margin keeps the relay link comfortably ahead of expiry.
const defaultRefreshMargin = 2 * time.Minute

const (
	refreshRetryInitial = time.Second
	refreshRetryMax     = time.Minute
)

func defaultRefreshBackoff(failures int) time.Duration {
	delay := refreshRetryInitial
	for range failures {
		if delay >= refreshRetryMax/2 {
			delay = refreshRetryMax
			break
		}
		delay *= 2
	}
	return delay
}

// credentialRefresher keeps the account relay link alive (R4/R14): the access
// token is minute-lived and must be renewed well before it expires or the hub
// link dies permanently. It exchanges the stored refresh token at the server's
// refresh endpoint, persists the rotated credential, and lets the HubLink
// re-resolve the freshest access token at each dial. Refresh failures never
// propagate to the daemon run loop — the loop logs and retries with backoff.
type credentialRefresher struct {
	state      *state.State
	serverURL  string
	httpClient *http.Client

	now     func() time.Time
	wait    func(context.Context, time.Duration) error
	backoff func(int) time.Duration
	margin  time.Duration
	logf    func(format string, args ...any)
}

func newCredentialRefresher(st *state.State, serverURL string) *credentialRefresher {
	return &credentialRefresher{
		state:      st,
		serverURL:  serverURL,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		now:        time.Now,
		wait:       waitForRefresh,
		backoff:    defaultRefreshBackoff,
		margin:     defaultRefreshMargin,
		logf:       log.Printf,
	}
}

// run refreshes until ctx is canceled or the refresh token is permanently dead.
func (r *credentialRefresher) run(ctx context.Context, stopRelay context.CancelFunc) {
	failures := 0
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		credential := r.state.Snapshot().Credential
		now := r.now()
		if credential.RefreshToken == "" {
			// Legacy/edge credential with nothing to refresh. Leave the hub alone
			// (it keeps using whatever access token it has) rather than tearing
			// down a link that may still work.
			r.logf("daemon.refresh: no refresh token; relay renewal disabled")
			return
		}
		if credential.RefreshTokenExpiresAt > 0 && now.Unix() >= credential.RefreshTokenExpiresAt {
			r.logf("daemon.refresh: refresh token expired at %d; relay renewal stopped (LAN unaffected)",
				credential.RefreshTokenExpiresAt)
			stopRelay()
			return
		}
		delay := r.nextRefreshIn(credential, now)
		if err := r.wait(ctx, delay); err != nil {
			return
		}

		// Retry transient failures with backoff without re-entering the schedule
		// wait: the access token is still due until a refresh succeeds. Only a
		// permanent grant rejection (invalid_grant) stops the loop.
		token, permanent, err := r.refreshOnce(ctx, credential.RefreshToken)
		for err != nil && !permanent {
			r.logf("daemon.refresh: refresh failed; retrying: %v", err)
			if err := r.wait(ctx, r.backoff(failures)); err != nil {
				return
			}
			failures++
			token, permanent, err = r.refreshOnce(ctx, credential.RefreshToken)
		}
		if err != nil {
			r.logf("daemon.refresh: refresh token rejected by server; relay renewal stopped (LAN unaffected): %v", err)
			stopRelay()
			return
		}
		failures = 0
		rotated := state.AccountCredential{
			DeviceID:              credential.DeviceID,
			AccessToken:           token.AccessToken,
			AccessTokenExpiresAt:  r.now().Add(time.Duration(token.ExpiresIn) * time.Second).Unix(),
			RefreshToken:          token.RefreshToken,
			RefreshTokenExpiresAt: r.now().Add(time.Duration(token.RefreshExpiresIn) * time.Second).Unix(),
		}
		r.state.Mutate(func(s *state.State) { s.Credential = rotated })
		if err := r.state.Save(); err != nil {
			r.logf("daemon.refresh: persist refreshed credential: %v", err)
		}
	}
}

// nextRefreshIn schedules the next proactive refresh well before the access
// token expires (defaultRefreshMargin). An already-due token refreshes now.
func (r *credentialRefresher) nextRefreshIn(credential state.AccountCredential, now time.Time) time.Duration {
	if credential.AccessTokenExpiresAt <= 0 {
		return r.margin
	}
	remaining := time.Unix(credential.AccessTokenExpiresAt, 0).Sub(now)
	delay := remaining - r.margin
	if delay < 0 {
		return 0
	}
	return delay
}

type refreshTokenResponse struct {
	AccessToken      string `json:"access_token"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
}

// refreshOnce exchanges a refresh token for a fresh access token plus a rotated
// refresh token. permanent reports an unrecoverable grant failure (expired /
// revoked / replay) — the loop must stop, not retry, and the relay is dead.
// Network errors and server 5xx are transient and retried by the caller.
func (r *credentialRefresher) refreshOnce(ctx context.Context, refreshToken string) (*refreshTokenResponse, bool, error) {
	body, err := json.Marshal(map[string]string{"refresh_token": refreshToken})
	if err != nil {
		return nil, false, fmt.Errorf("encode refresh request: %w", err)
	}
	endpoint := strings.TrimRight(r.serverURL, "/") + "/v1/oauth/token/refresh"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, false, fmt.Errorf("build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("refresh request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, false, fmt.Errorf("read refresh response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var oauthErr struct {
			Code        string `json:"error"`
			Description string `json:"error_description"`
		}
		if decodeServerEnvelope(payload, &oauthErr) == nil && oauthErr.Code != "" {
			return nil, oauthErr.Code == "invalid_grant",
				fmt.Errorf("refresh rejected: %s: %s", oauthErr.Code, oauthErr.Description)
		}
		return nil, false, fmt.Errorf("refresh endpoint returned %s", resp.Status)
	}
	var token refreshTokenResponse
	if err := decodeServerEnvelope(payload, &token); err != nil {
		return nil, false, fmt.Errorf("parse refresh response: %w", err)
	}
	if token.AccessToken == "" || token.RefreshToken == "" || token.ExpiresIn <= 0 || token.RefreshExpiresIn <= 0 {
		return nil, false, fmt.Errorf("refresh endpoint returned an invalid token payload")
	}
	return &token, false, nil
}

// decodeServerEnvelope handles both the raw JSON payload and cago's
// {data: ...} response envelope, mirroring the login flow's decoder. Agentre
// must not import agentre-server; this keeps the daemon-side contract in sync.
func decodeServerEnvelope(payload []byte, target any) error {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return err
	}
	if len(envelope.Data) != 0 && string(envelope.Data) != "null" {
		return json.Unmarshal(envelope.Data, target)
	}
	return json.Unmarshal(payload, target)
}

func waitForRefresh(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// defaultRevocationPollInterval is how often a claimed daemon pulls its account's
// revocation list. It has to stay well under the server's 15m access TTL for the
// pull to add anything: a credential revoked right after a pull is refused
// locally at most one interval later, and its short expiry is the backstop (R4).
const defaultRevocationPollInterval = time.Minute

// revocationsResponse mirrors agentre-server's GET /v1/devices/revocations body
// (device JWT bearer). RevokedJTI is every revoked access-token jti under the
// caller device's account that was issued inside the server's access-TTL window;
// older ones are omitted because expiry alone already invalidates them.
type revocationsResponse struct {
	RevokedJTI []string `json:"revoked_jti"`
	AsOf       int64    `json:"as_of"`
}

type verificationKeysResponse struct {
	CurrentKID              string            `json:"current_kid"`
	Keys                    map[string]string `json:"keys"`
	MaxTokenLifetimeSeconds int64             `json:"max_token_lifetime_seconds"`
}

// revocationPoller keeps the daemon's cached copy of that list fresh (R4
// consumer). The account handshake is verified entirely from cached material
// with zero network round trips (R3), so this loop is the only thing that can
// make a revocation take effect on this machine. A failed or offline pull
// deliberately keeps the previous list and keeps enforcing it — that is R4's
// acknowledged revocation delay (R19), not a fallback — and it never touches
// local sessions or LAN serving.
type revocationPoller struct {
	state       *state.State
	serverURL   string
	httpClient  *http.Client
	accessToken func() string

	interval time.Duration
	// wait is the clock seam shared with the credential refresher: the loop asks
	// for a delay, production sleeps it out, a test releases it immediately.
	wait    func(context.Context, time.Duration) error
	backoff func(int) time.Duration
	logf    func(format string, args ...any)
}

func newRevocationPoller(st *state.State, serverURL string, accessToken func() string) *revocationPoller {
	return &revocationPoller{
		state:       st,
		serverURL:   serverURL,
		httpClient:  &http.Client{Timeout: 15 * time.Second},
		accessToken: accessToken,
		interval:    defaultRevocationPollInterval,
		wait:        waitForRefresh,
		backoff:     defaultRefreshBackoff, // the daemon's shared transient-retry ladder
		logf:        log.Printf,
	}
}

// run pulls until ctx is canceled, starting with an immediate pull so a daemon
// that was offline (or just restarted) re-syncs as soon as it can reach server.
func (p *revocationPoller) run(ctx context.Context) {
	failures := 0
	for {
		if ctx.Err() != nil {
			return
		}
		if len(p.state.Snapshot().VerificationPublicKeys) != 0 {
			if err := p.refreshVerificationKeys(ctx); err != nil {
				p.logf("daemon.verificationKeys: refresh failed; keeping the last key set: err=%v", err)
			}
		}
		list, err := p.pullOnce(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			kept := p.state.Snapshot()
			p.logf("daemon.revocations: pull failed; keeping the last list: revokedCount=%d asOf=%d err=%v",
				len(kept.RevokedJTIs), kept.RevocationsAsOf, err)
			if err := p.wait(ctx, p.backoff(failures)); err != nil {
				return
			}
			failures++
			continue
		}
		failures = 0
		p.state.Mutate(func(s *state.State) {
			s.RevokedJTIs = list.RevokedJTI
			s.RevocationsAsOf = list.AsOf
		})
		// Persisted on every pull: the check has to survive a restart, and it is
		// the only copy an offline daemon has left to enforce.
		if err := p.state.Save(); err != nil {
			p.logf("daemon.revocations: persist revocation list: revokedCount=%d err=%v",
				len(list.RevokedJTI), err)
		}
		if err := p.wait(ctx, p.interval); err != nil {
			return
		}
	}
}

func (p *revocationPoller) refreshVerificationKeys(ctx context.Context) error {
	endpoint := strings.TrimRight(p.serverURL, "/") + "/v1/keys"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build verification keys request: %w", err)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("verification keys request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read verification keys response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("verification keys endpoint returned %s", resp.Status)
	}
	var keys verificationKeysResponse
	if err := decodeServerEnvelope(payload, &keys); err != nil {
		return fmt.Errorf("parse verification keys response: %w", err)
	}
	if keys.CurrentKID == "" || keys.Keys[keys.CurrentKID] == "" || keys.MaxTokenLifetimeSeconds <= 0 {
		return errors.New("verification keys endpoint returned an invalid key set")
	}
	p.state.Mutate(func(s *state.State) {
		s.VerificationCurrentKID = keys.CurrentKID
		s.VerificationPublicKeys = keys.Keys
		s.VerificationPublicKeyPEM = keys.Keys[keys.CurrentKID]
		s.MaxTokenLifetimeSeconds = keys.MaxTokenLifetimeSeconds
	})
	if err := p.state.Save(); err != nil {
		return fmt.Errorf("persist verification keys: %w", err)
	}
	return nil
}

// pullOnce fetches the account's revocation list with the daemon's device
// credential. Every failure — including the 401 a revoked device itself gets —
// is transient here: the caller keeps the last list and retries with backoff.
func (p *revocationPoller) pullOnce(ctx context.Context) (*revocationsResponse, error) {
	token := p.accessToken()
	if token == "" {
		return nil, errors.New("no account access token")
	}
	endpoint := strings.TrimRight(p.serverURL, "/") + "/v1/devices/revocations"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build revocations request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("revocations request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read revocations response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("revocations endpoint returned %s", resp.Status)
	}
	var list revocationsResponse
	if err := decodeServerEnvelope(payload, &list); err != nil {
		return nil, fmt.Errorf("parse revocations response: %w", err)
	}
	// as_of is always set by the endpoint, so its absence means this is not the
	// contract's payload (a captive portal or proxy answering 200, say). Treat
	// it as a failed pull: replacing the cached list with an empty one would
	// silently un-revoke everything.
	if list.AsOf <= 0 {
		return nil, errors.New("revocations endpoint returned a payload without as_of")
	}
	return &list, nil
}

// serveRelayChannels turns server-initiated virtual channels into the same
// connection shape the LAN server creates. The channel ID and relay envelope
// end at the Multiplexer boundary: Conn and every registered handler see only
// a FrameConn.
func (d *Daemon) serveRelayChannels(ctx context.Context, mux *rpc.Multiplexer) {
	for {
		select {
		case <-ctx.Done():
			return
		case channel := <-mux.Accept():
			if channel == nil {
				return
			}
			conn := rpc.NewConn(channel, d.registry.Clone())
			d.bindConn(conn)
			go conn.Serve(ctx)
		}
	}
}

// bindConn is called once per LAN or relay connection.
// 挂载 runtime.* 13 个 RPC 到这条连接的私有 registry。RuntimeHandlers 持有 per-session backend
// type cache,所以是 per-conn 构造的;会话通知**不**推回「这条」连接 —— 它在发送那一刻
// 按会话解析属主连接(见 notifierForPeer / connRegistry),因为 fanout goroutine 会活过
// 这条连接。
//
// bindConn 跑在鉴权**之前**(它是 OnConn 回调,auth.pair / auth.connect / auth.account 是之后才到的
// RPC),所以这里**不**把连接登记成推送目标 —— 登记只发生在鉴权成功那一刻,会话认领
// 更要等到它真为某条会话发 runtime.*,否则一条从不认证的连接就能顶掉正主。
func (d *Daemon) bindConn(c *rpc.Conn) {
	reg := c.Registry()
	n := notifier.New(c)
	rh := handlers.NewRuntimeHandlers(handlers.RuntimeDeps{
		// 会话通知的推送目标在发送那一刻按会话解析,不捕获 n:RuntimeHandlers 是
		// per-conn 的,而它起的 fanout goroutine 会活过这条连接。
		NotifyFor: d.notifierForPeer,
		Journal:   d.journal,
		Sessions:  d.sessionStore,
		// R17:queuedID → 提交方对端映射是 Daemon 级的 —— Steer RPC 可能落在任意一条
		// 连接(同设备多条连接 / 他端接管),而 SteerConsumed 由发起会话那条连接的
		// fanout 发出,两者必须共享同一张表。
		SteerSource: d.steerSource,
		// 同一个仓储的读侧:提交决策解不出会话时,靠它区分「轮次真的结束了」与
		// 「这个 handler 从没拥有过这条会话」(见 idempotentSubmitResult)。
		SessionQuery:       d.sessionStore,
		Gateway:            d.gateway,
		Lookup:             NewProviderLookup(d.state),
		ClaimedAccountID:   d.claimedAccountID,
		GenerationRegistry: d.generations,
		CLIPathForBackend:  d.engineSnapshot.ResolveCLIPath,
	})
	d.runtimeMu.Lock()
	d.runtimeHandlers[c] = rh
	d.runtimeMu.Unlock()
	// runtime.* 全族都过 trackSessionOwner:哪条连接为某会话发了 runtime.*,该会话的
	// 通知此后就推给它(见 connRegistry 的接管规则)。
	regRuntime := func(method string, h rpc.HandlerFunc) {
		reg.Register(method, d.trackSessionOwner(h))
	}
	regRuntime(wire.MethodCapabilities, wrapGuarded(rh.Capabilities))
	regRuntime(wire.MethodRun, wrapGuarded(rh.Run))
	regRuntime(wire.MethodSteer, wrapGuardedSentinel(rh.Steer))
	regRuntime(wire.MethodCancelSteer, wrapGuardedSentinel(rh.CancelSteer))
	regRuntime(wire.MethodDrainPending, wrapGuarded(rh.DrainPending))
	regRuntime(wire.MethodAbort, wrapGuardedSentinel(rh.Abort))
	regRuntime(wire.MethodStopBackgroundTask, wrapGuardedSentinel(rh.StopBackgroundTask))
	regRuntime(wire.MethodSetPermissionMode, wrapGuardedSentinel(rh.SetPermissionMode))
	regRuntime(wire.MethodSubmitAnswer, wrapGuardedSentinel(rh.SubmitAnswer))
	regRuntime(wire.MethodSubmitToolPermission, wrapGuardedSentinel(rh.SubmitToolPermission))
	regRuntime(wire.MethodGetGoal, wrapGuardedSentinel(rh.GetGoal))
	regRuntime(wire.MethodSetGoal, wrapGuardedSentinel(rh.SetGoal))
	regRuntime(wire.MethodClearGoal, wrapGuardedSentinel(rh.ClearGoal))

	// 显式接管。它是补齐族里唯一一个 per-conn 注册的方法,因为它的语义就是「**这条**
	// 连接此后消费这条会话」:改推送目标要知道是哪条连接,让控制 RPC 也跟着回来要知道
	// 是哪个 RuntimeHandlers。
	reg.Register(wire.MethodSessionAttach, d.attachSession(rh))

	// Terminal: local PTY backend; per-conn emitter pushes terminal.data /
	// terminal.exit events back over this ws connection (same per-conn rationale
	// as runtime.* above — events are scoped to whoever opened the terminal).
	termBackend := localPTYBackendAdapter{be: local.NewBackend()}
	termEmitter := handlers.EmitterFunc(func(_ context.Context, name string, payload any) {
		_ = n.Notify(name, payload)
	})
	termH := handlers.NewTerminalHandlers(termBackend, termEmitter)
	reg.Register("terminal.open", wrapGuarded(termH.Open))
	reg.Register("terminal.write", wrapGuarded(termH.Write))
	reg.Register("terminal.resize", wrapGuarded(termH.Resize))
	reg.Register("terminal.close", wrapGuarded(termH.Close))
	// When this connection drops, keep the existing PTY cleanup, drop its push
	// registrations, and close every exact Pi generation owned by this handler.
	// Cleanup is bounded and owner-scoped, so an old socket cannot sweep a
	// reconnect by SessionID.
	go func() {
		<-c.Done()
		termH.CloseAll()
		// 连接断开 → 撤销它的登记与会话认领(避免对死连接推通知 / 发反向请求)。
		// 按连接身份撤销:同一台设备的其它连接、以及重连后的新连接都不受影响。
		d.conns.remove(c)
		cleanupCtx, cancel := context.WithTimeout(context.Background(), daemonConnectionCleanupTimeout)
		defer cancel()
		if err := rh.Close(cleanupCtx); err != nil {
			log.Printf("daemon.bindConn: runtime cleanup failed errorType=%T", err)
		}
		d.runtimeMu.Lock()
		if d.runtimeHandlers[c] == rh {
			delete(d.runtimeHandlers, c)
		}
		d.runtimeMu.Unlock()
	}()
}

// shutdown 是关机时的资源收尾:先收每条连接名下的 Pi generation,再释放跨轮常驻的
// claude / codex 子进程会话。
//
// 后一半此前是缺的。那些子进程自成进程组,不会被 daemon 退出时的 SIGHUP 连坐 ——
// 每重启一次 agentred 就在机器上多留一批孤儿 CLI,还各自握着 MCP server 与网关 token。
// 用 CloseAll 而不是 RemoveAll:关机需要的是「收干净了」的保证,而不是一堆随进程一起
// 消失的 goroutine;ctx 是它的上界。
func (d *Daemon) shutdown(ctx context.Context) {
	if err := d.closeRuntimeConnections(ctx); err != nil {
		log.Printf("daemon.shutdown: connection cleanup failed errorType=%T", err)
	}
	if err := agentruntime.DefaultCLISessionPool().CloseAll(ctx); err != nil {
		log.Printf("daemon.shutdown: CLI session release failed errorType=%T", err)
	}
	// 池外的后端(pi 每轮一个进程,不进池)由注册表广播收尾。
	agentruntime.CloseAllSessionsEverywhere(ctx)
}

func (d *Daemon) closeRuntimeConnections(ctx context.Context) error {
	type ownedRuntime struct {
		connection *rpc.Conn
		handler    *handlers.RuntimeHandlers
	}
	d.runtimeMu.RLock()
	owned := make([]ownedRuntime, 0, len(d.runtimeHandlers))
	for connection, runtimeHandler := range d.runtimeHandlers {
		owned = append(owned, ownedRuntime{connection: connection, handler: runtimeHandler})
	}
	d.runtimeMu.RUnlock()
	for _, current := range owned {
		_ = current.connection.Close()
	}
	results := make(chan error, len(owned))
	for _, current := range owned {
		go func(runtimeHandler *handlers.RuntimeHandlers) {
			results <- runtimeHandler.Close(ctx)
		}(current.handler)
	}
	var cleanupErr error
	for range owned {
		select {
		case err := <-results:
			if err != nil {
				cleanupErr = errors.Join(cleanupErr, err)
			}
		case <-ctx.Done():
			return errors.Join(cleanupErr, ctx.Err())
		}
	}
	return cleanupErr
}

// trackSessionOwner 包住 runtime.* 的注册:daemon **受理**了哪条连接为某会话发的
// runtime.*,该会话此后的通知就推给哪条(接管规则见 connRegistry)。会话 id 直接读参数
// 上的 sessionId —— 13 个 runtime.* 参数里除 capabilities 外都带它,没带的自然不认领。
//
// 必须在 handler **之前**认领:runtime.run 一返回,fanout goroutine 就开始推事件,
// 记晚了首批事件会被当成「没有活属主」只落库,而客户端此刻还没有补齐能力。
//
// 而 handler 拒了这一条时必须还原属主:接管的凭据是「daemon 受理了它」,不是「它发出过」。
// 否则同指纹的另一条连接随便发一条会被拒的 runtime.*(会话 id 不存在于本 daemon、backend
// 不支持该能力、参数非法……)就能把正在跑的会话的推送整个抢走 —— 它不消费,发起会话的那条
// 从此一条也收不到,既没有错误也没有 seq 跳号。
func (d *Daemon) trackSessionOwner(next rpc.HandlerFunc) rpc.HandlerFunc {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			SessionID       int64  `json:"sessionId"`
			PeerFingerprint string `json:"peerFingerprint"`
		}
		var ticket claimTicket
		if err := jsonUnmarshal(raw, &p); err == nil {
			peer, peerErr := handlers.ResolveSessionPeer(ctx, p.PeerFingerprint, d.claimedAccountID)
			if peerErr != nil {
				return nil, peerErr
			}
			ticket = d.conns.claimFor(rpc.ConnFromContext(ctx), peer, p.SessionID)
		}
		res, err := next(ctx, raw)
		if err != nil {
			d.conns.undoClaim(ticket)
		}
		return res, err
	}
}

// attachSession 是显式接管的注册:handler 先校验这条会话确实是调用方的、且还接得回去,
// **受理之后**才真正把它交给这条连接。两件事一起发生,缺一个接管都是半截的:
//
//   - 推送目标改到这条连接(conns.claim)—— 否则通知继续只落库不推送,补齐完了实时流
//     还是不通;
//   - 这条连接的 RuntimeHandlers 认下这条会话(rh.Adopt)—— RuntimeHandlers 是
//     per-conn 构造的,重连后那张内存会话表是空的,不认下来的话客户端刚补齐完、正要
//     回答一条待决策,submitToolPermission 会解不出会话,再被 R8 的幂等折成「成功」:
//     waiter 没人回答、客户端以为答过了,叠加 R9 的不设过期 = 会话永久挂死。
//
// 认领放在 handler **之后**(与 trackSessionOwner 的「之前认领 + 失败回滚」相反):
// 接管不启动任何一轮执行,没有「记晚了首批事件会丢」的问题,而被拒的接管绝不能改变
// 任何东西。接管与读高水位之间落库的那几条不会丢 —— 客户端随后按自己的游标 pull,
// 那一轮 pull 发生在认领之后。
func (d *Daemon) attachSession(rh *handlers.RuntimeHandlers) rpc.HandlerFunc {
	inner := wrapGuardedSentinel(d.catchup.Attach)
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p wire.SessionAttachParams
		if err := jsonUnmarshal(raw, &p); err != nil {
			return nil, rpc.ErrInvalidParams
		}
		peer, err := handlers.ResolveSessionPeer(ctx, p.PeerFingerprint, d.claimedAccountID)
		if err != nil {
			return nil, err
		}
		res, err := inner(ctx, raw)
		if err != nil {
			return nil, err
		}
		attached, ok := res.(wire.SessionAttachResult)
		if !ok {
			return res, nil
		}
		rh.AdoptForPeer(peer, attached.SessionID, agent_backend_entity.BackendType(attached.BackendType))
		d.conns.claimFor(rpc.ConnFromContext(ctx), peer, attached.SessionID)
		log.Printf("runtime.session.attach: session taken over sid=%d state=%s latestSeq=%d",
			attached.SessionID, attached.LifecycleState, attached.LatestSeq)
		return res, nil
	}
}

// wrapGuarded is wrap + requireAuth check. Use for any method except auth.*.
//
// Handler panics 被 recoverHandlerPanic 收住,翻成 ErrInternal 让 daemon 进
// 程继续活着、客户端 chat_svc 得到一条可读错误(走 wire.FromJSONRPCError
// → 触发 StreamError 让前端 UI 解锁"生成中"状态)。历史教训:claudecode
// runtime 在 daemon 进程 nil panic 直接 SIGSEGV 整个 agentred,前端 UI 收不
// 到任何提示,会话永远卡在 generating。
func wrapGuarded[Req any, Res any](fn func(context.Context, Req) (Res, error)) rpc.HandlerFunc {
	return func(ctx context.Context, raw json.RawMessage) (res any, err error) {
		defer recoverHandlerPanic(&err)
		if err := requireAuth(ctx); err != nil {
			return nil, err
		}
		var req Req
		if err := jsonUnmarshal(raw, &req); err != nil {
			return nil, rpc.ErrInvalidParams
		}
		return fn(ctx, req)
	}
}

// wrapGuardedSentinel 同 wrapGuarded,但额外把 handler 返回的 agentruntime
// sentinel(ErrNoActiveTurn / ErrSteerNotFound / ErrUnsupported / ErrAborted)
// 翻成稳定 JSON-RPC error code,客户端 wire.FromJSONRPCError 反向 rehydrate
// 让 errors.Is(err, agentruntime.ErrXxx) 跨进程继续工作。
func wrapGuardedSentinel[Req any, Res any](fn func(context.Context, Req) (Res, error)) rpc.HandlerFunc {
	wrapped := wrapGuarded(fn)
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		res, err := wrapped(ctx, raw)
		if err != nil {
			if mapped := wire.ToJSONRPCError(err); mapped != nil {
				return nil, mapped
			}
		}
		return res, err
	}
}

// wrapGuardedNoParams is wrapNoParams + requireAuth check.
func wrapGuardedNoParams[Res any](fn func(context.Context) (Res, error)) rpc.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (res any, err error) {
		defer recoverHandlerPanic(&err)
		if err := requireAuth(ctx); err != nil {
			return nil, err
		}
		return fn(ctx)
	}
}

// recoverHandlerPanic 是 RPC handler 的最后一道防线:把 panic 翻成 ErrInternal
// 让 daemon 进程不挂、客户端收到结构化错误。stack trace 进日志方便事后定位。
// 命名 err 返回值由调用方提供(`err *error`),defer 写回最终返回。
func recoverHandlerPanic(errOut *error) {
	if r := recover(); r != nil {
		stack := debug.Stack()
		log.Printf("daemon rpc handler panic: %v\n%s", r, stack)
		*errOut = &rpc.Error{
			Code:    rpc.ErrInternal.Code,
			Message: fmt.Sprintf("daemon handler panic: %v", r),
		}
	}
}

func jsonUnmarshal(b json.RawMessage, v any) error {
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, v)
}

// openDB opens agentred's own SQLite handle at <dataDir>/agentred.db (state.Load
// already created dataDir with 0700 before this is called). Deliberately a
// plain gorm.Open — not a cago db.Database() component — because cago's
// database/db package only exports a package-level Default()/SetDefault();
// there is no way to obtain a handle from it without writing that global,
// which this package must not do (see the db field's doc comment).
func openDB(dataDir string) (*gorm.DB, error) {
	dbPath := filepath.Join(dataDir, dbFileName)
	// busy_timeout mirrors internal/bootstrap/cago.go's sqliteDSN: concurrent
	// writers otherwise hit SQLITE_BUSY near-instantly instead of waiting.
	//
	// WAL 不是调优,是这个库的工作负载本身要求的:写侧是**每个流式事件一条**同步事务
	// (handlers/runtime.go 的 fanout 对每个 agentruntime.Event 都落一条日志),读侧是
	// session.pull 的翻页补齐 —— 一段开着的读事务。回滚日志模式下两者互斥:读事务持
	// SHARED,写事务提交要 EXCLUSIVE,于是流式写只能在 busy_timeout 上干等 5 秒,超时
	// 那条通知既不落库也不推送(R3)。WAL 下读方读快照、写方追加,谁也不挡谁。
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	return gorm.Open(sqlite.Open(dsn), &gorm.Config{})
}

// dbSidecarSuffixes 是 SQLite 在主库文件旁边开的两个附属文件。WAL 模式下新写入先落在
// -wal 上(checkpoint 之后才并回主库),只统计主库文件会让「库有多大」在最该看见增长的
// 时刻纹丝不动。
var dbSidecarSuffixes = []string{"-wal", "-shm"}

// dbPath 是这台 daemon 的库文件位置(openDB 打开的那一个)。
func (d *Daemon) dbPath() string {
	return filepath.Join(d.opts.DataDir, dbFileName)
}

// DBStat 交出库文件的路径与体量,喂给两处状态查询(IPC 的 /local/status 与 RPC 的
// health.ping)。统计不到的文件按 0 计:体量是给人看的参考值,读不到某个旁文件不该
// 让一次状态查询失败。
func (d *Daemon) DBStat() handlers.DBStat {
	path := d.dbPath()
	stat := handlers.DBStat{Path: path}
	if fi, err := os.Stat(path); err == nil {
		stat.SizeBytes = fi.Size()
	}
	for _, suffix := range dbSidecarSuffixes {
		if fi, err := os.Stat(path + suffix); err == nil {
			stat.SizeBytes += fi.Size()
		}
	}
	return stat
}

var _ handlers.DBStatPort = (*Daemon)(nil)

// notificationJournal 是 handlers.JournalPort 的 daemon 级实现:把「本该发出的通知」
// 写进本实例的 daemon_notification_logs,seq 由仓储在同一条语句里分配。
//
// 它自己往 ctx 上注入本 Daemon 的 db 句柄:通知的生产者是脱离请求 ctx 的 fanout
// goroutine(它可能拿到的只是一个裸 ctx),而 daemon 故意不写 db.SetDefault(同进程
// 多个 Daemon 会互相串库,见 Daemon.db 注释),所以句柄只能从这里给。
type notificationJournal struct{ db *gorm.DB }

func (j notificationJournal) Append(ctx context.Context, peerFingerprint, peerSessionID, method string, payload json.RawMessage) (int64, error) {
	row := &notification_repo.NotificationLog{
		PeerFingerprint: peerFingerprint,
		PeerSessionID:   peerSessionID,
		Method:          method,
		Payload:         string(payload),
	}
	if err := notification_repo.Notification().Append(dbpkg.WithContextDB(ctx, j.db), row); err != nil {
		return 0, err
	}
	return row.Seq, nil
}

// daemonSessionStore 同时是 handlers 的会话生命周期写入口与查询出口:两个接口在
// handlers 那边按 ISP 分开声明(跑一轮的一侧只写,补齐的一侧只读),daemon 这边由同一
// 个仓储实现供给,不必为此拆成两个类型。
//
// 与 notificationJournal 同理,它自己往 ctx 上注入本 Daemon 的 db 句柄:生命周期的写入
// 方是脱离请求 ctx 的 fanout goroutine,而 daemon 故意不写 db.SetDefault(同进程多个
// Daemon 会互相串库,见 Daemon.db 注释)。
type daemonSessionStore struct{ db *gorm.DB }

// steerSourceStore 是「queuedID → 提交方对端」的映射(R17),Daemon 级一份(见
// Daemon.steerSource 注释)。queuedID 是桌面端生成的 UUID v4,跨对端天然唯一,所以可以
// 用裸 ID 做键而不必按会话/对端隔离。映射在 Steer RPC 时写入、SteerConsumed / 轮末
// DrainPending 消费后删除;撤回的 steer 由 CancelSteer 显式 Forget,不会无界增长。
// 实现满足 handlers.SteerSourcePort(按 DIP 声明在消费方)。
type steerSourceStore struct {
	mu sync.Mutex
	m  map[string]handlers.SteerSourceEntry
}

var _ handlers.SteerSourcePort = (*steerSourceStore)(nil)

func newSteerSourceStore() *steerSourceStore {
	return &steerSourceStore{m: make(map[string]handlers.SteerSourceEntry)}
}

func (s *steerSourceStore) Record(queuedID string, entry handlers.SteerSourceEntry) {
	if queuedID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[queuedID] = entry
}

func (s *steerSourceStore) Consume(queuedID string) (handlers.SteerSourceEntry, bool) {
	if queuedID == "" {
		return handlers.SteerSourceEntry{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[queuedID]
	if ok {
		delete(s.m, queuedID)
	}
	return e, ok
}

func (s *steerSourceStore) Forget(queuedID string) {
	if queuedID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, queuedID)
}

var (
	_ handlers.SessionLifecyclePort = daemonSessionStore{}
	_ handlers.SessionQueryPort     = daemonSessionStore{}
)

func (s daemonSessionStore) Start(ctx context.Context, rec handlers.SessionRecord) error {
	return session_repo.Session().Upsert(dbpkg.WithContextDB(ctx, s.db), &session_repo.DaemonSession{
		PeerFingerprint:   rec.PeerFingerprint,
		PeerSessionID:     rec.PeerSessionID,
		AgentID:           rec.AgentID,
		Cwd:               rec.Cwd,
		BackendType:       rec.BackendType,
		LifecycleState:    rec.LifecycleState,
		Title:             rec.Title,
		AgentSyncID:       rec.AgentSyncID,
		ProviderSessionID: rec.ProviderSessionID,
	})
}

func (s daemonSessionStore) Running(ctx context.Context, peerFingerprint, peerSessionID string) error {
	return session_repo.Session().UpdateLifecycle(
		dbpkg.WithContextDB(ctx, s.db), peerFingerprint, peerSessionID, wire.SessionLifecycleRunning)
}

func (s daemonSessionStore) Finish(ctx context.Context, peerFingerprint, peerSessionID string) error {
	return session_repo.Session().UpdateLifecycle(
		dbpkg.WithContextDB(ctx, s.db), peerFingerprint, peerSessionID, wire.SessionLifecycleIdle)
}

// Delete 删掉这一条 (对端, 会话) 的会话行(handlers.SessionDeletePort)。它只删身份
// 行,那条会话的通知日志由 journalPurger 清 —— 两张表各自的仓储各管各的。
func (s daemonSessionStore) Delete(ctx context.Context, peerFingerprint, peerSessionID string) (int64, error) {
	return session_repo.Session().Delete(
		dbpkg.WithContextDB(ctx, s.db), peerFingerprint, peerSessionID)
}

// CountRunning 数一数这台 daemon 此刻正在跑的会话(本机状态查询用,见 ipcStatus)。
// 只服务本机 IPC,不经 LAN 出去,因此不按对端限定 —— 它答的是「这台机器忙不忙」。
func (s daemonSessionStore) CountRunning(ctx context.Context) (int64, error) {
	return session_repo.Session().CountByLifecycle(
		dbpkg.WithContextDB(ctx, s.db), wire.SessionLifecycleRunning)
}

func (s daemonSessionStore) List(ctx context.Context, peerFingerprint string) ([]handlers.SessionRecord, error) {
	rows, err := session_repo.Session().ListByPeer(dbpkg.WithContextDB(ctx, s.db), peerFingerprint)
	if err != nil {
		return nil, err
	}
	out := make([]handlers.SessionRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, sessionRecordOf(row))
	}
	return out, nil
}

func (s daemonSessionStore) ListAll(ctx context.Context) ([]handlers.SessionRecord, error) {
	rows, err := session_repo.Session().ListAll(dbpkg.WithContextDB(ctx, s.db))
	if err != nil {
		return nil, err
	}
	out := make([]handlers.SessionRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, sessionRecordOf(row))
	}
	return out, nil
}

func (s daemonSessionStore) Find(ctx context.Context, peerFingerprint, peerSessionID string) (*handlers.SessionRecord, error) {
	row, err := session_repo.Session().Find(dbpkg.WithContextDB(ctx, s.db), peerFingerprint, peerSessionID)
	if err != nil || row == nil {
		return nil, err
	}
	rec := sessionRecordOf(row)
	return &rec, nil
}

func sessionRecordOf(row *session_repo.DaemonSession) handlers.SessionRecord {
	return handlers.SessionRecord{
		PeerFingerprint:   row.PeerFingerprint,
		PeerSessionID:     row.PeerSessionID,
		AgentID:           row.AgentID,
		Cwd:               row.Cwd,
		BackendType:       row.BackendType,
		LifecycleState:    row.LifecycleState,
		Title:             row.Title,
		AgentSyncID:       row.AgentSyncID,
		ProviderSessionID: row.ProviderSessionID,
		UpdatedAt:         row.UpdatedAt,
	}
}

// journalPurger 是通知日志的删除侧(会话删除用)。它与 notificationJournal(写一条)
// 和 journalReader(读)分开:整段清空是唯一一条会让已落库的通知消失的路径,handlers
// 那边也按 ISP 单独声明了它(JournalPurgePort)。
type journalPurger struct{ db *gorm.DB }

var _ handlers.JournalPurgePort = journalPurger{}

func (j journalPurger) DeleteAll(ctx context.Context, peerFingerprint, peerSessionID string) (int64, error) {
	return notification_repo.Notification().DeleteAll(
		dbpkg.WithContextDB(ctx, j.db), peerFingerprint, peerSessionID)
}

// journalReader 是通知日志的读侧(补齐用),写侧见 notificationJournal。
type journalReader struct{ db *gorm.DB }

var _ handlers.JournalReaderPort = journalReader{}

func (j journalReader) ListSince(ctx context.Context, peerFingerprint, peerSessionID string, cursor int64, limit int) ([]handlers.JournalRow, bool, error) {
	rows, hasMore, err := notification_repo.Notification().ListSince(
		dbpkg.WithContextDB(ctx, j.db), peerFingerprint, peerSessionID, cursor, limit)
	if err != nil {
		return nil, false, err
	}
	out := make([]handlers.JournalRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, handlers.JournalRow{
			Seq:    row.Seq,
			Method: row.Method,
			// 日志里存的是那条通知的 params 原样(不含 seq),原样交回客户端。
			Payload: json.RawMessage(row.Payload),
		})
	}
	return out, hasMore, nil
}

func (j journalReader) LatestSeq(ctx context.Context, peerFingerprint, peerSessionID string) (int64, error) {
	return notification_repo.Notification().LatestSeq(
		dbpkg.WithContextDB(ctx, j.db), peerFingerprint, peerSessionID)
}

func (j journalReader) OldestSeq(ctx context.Context, peerFingerprint, peerSessionID string) (int64, error) {
	return notification_repo.Notification().OldestSeq(
		dbpkg.WithContextDB(ctx, j.db), peerFingerprint, peerSessionID)
}

func (j journalReader) LatestSeqByPeer(ctx context.Context, peerFingerprint string) (map[string]int64, error) {
	return notification_repo.Notification().LatestSeqByPeer(dbpkg.WithContextDB(ctx, j.db), peerFingerprint)
}

// closeDB 关闭 openDB 拿到的句柄。只在 New 的失败路径上用:Daemon 构造失败时若不关,
// 这个 sql.DB 与它的文件句柄会一直挂着(进程内重试构造会每次泄一份)。构造成功后的
// 句柄跟随进程存活,没有单独的关闭时机。
func closeDB(gormDB *gorm.DB) {
	sqlDB, err := gormDB.DB()
	if err != nil {
		return
	}
	_ = sqlDB.Close()
}

type remoteAddrKey struct{}

func ipFromContext(ctx context.Context) string {
	v := ctx.Value(remoteAddrKey{})
	if s, ok := v.(string); ok {
		host, _, err := net.SplitHostPort(s)
		if err == nil {
			return host
		}
		return s
	}
	return ""
}

// localPTYBackendAdapter bridges *local.Backend (returns pty.Handle) to
// handlers.PTYBackend (returns handlers.PTYHandle). The returned pty.Handle
// structurally satisfies handlers.PTYHandle (identical method set).
type localPTYBackendAdapter struct {
	be *local.Backend
}

func (a localPTYBackendAdapter) Open(ctx context.Context, spec pty.Spec) (handlers.PTYHandle, error) {
	return a.be.Open(ctx, spec)
}

var _ handlers.PTYBackend = localPTYBackendAdapter{}

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
	"strings"
	"sync"
	"time"

	dbpkg "github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/daemon/auth"
	"github.com/agentre-hub/agentre/internal/daemon/connection"
	"github.com/agentre-hub/agentre/internal/daemon/enginesnapshot"
	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	daemonmigrations "github.com/agentre-hub/agentre/internal/daemon/migrations"
	"github.com/agentre-hub/agentre/internal/daemon/pairing"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/daemon/relaytransport"
	"github.com/agentre-hub/agentre/internal/daemon/repository/notification_repo"
	"github.com/agentre-hub/agentre/internal/daemon/repository/session_repo"
	"github.com/agentre-hub/agentre/internal/daemon/sessions"
	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/httpgateway"
	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/internal/pkg/pty/local"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
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
	auth           *auth.AuthHandlers
	engineSnapshot *enginesnapshot.Manager

	// db is agentred's own SQLite handle (session durability: daemon_sessions
	// + daemon_notification_journal — see internal/daemon/migrations). Deliberately
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
	// activity 只回答「哪天有几条」——活跃统计的纯计数上报。刻意与 catchup 分开:
	// 同一张表,交出去的东西完全不同,合成一个类型会让那条边界随时间被磨掉。
	activity *handlers.ActivityHandlers

	// sessionDelete 是会话删除 handler。与补齐族同为 Daemon 级、静态注册:它按对端
	// 限定、改的是库而不是「通知推给谁」,与哪条连接在调用无关。
	sessionDelete      *handlers.SessionDeleteHandlers
	sessionModelTarget *handlers.SessionModelTargetHandlers

	mu  sync.RWMutex
	lan *protorpc.LANServer
	hub *relaytransport.HubLink
	mux *relaytransport.Multiplexer

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
	runtimeMu        sync.RWMutex
	runtimeHandlers  map[connection.Conn]*handlers.RuntimeHandlers
	protobufRegistry *protorpc.Registry
}

const daemonConnectionCleanupTimeout = 3 * time.Second

// cliSessionSweepInterval 是 idle CLI 会话清扫的巡检间隔。
const cliSessionSweepInterval = time.Minute

// sessionKey 是 daemon 侧的会话身份(R16):(对端设备指纹, 对端会话 id)。会话 id 是
// 各客户端本地自增的,两个对端各自持有同一个 id 时是两条互不相干的会话。
type sessionKey struct {
	peer string
	// conversationID 是这条对话的全局身份。从前这里是客户端本地自增的会话号,所以
	// 必须与 peer 配对才唯一;对话身份本身已经全局唯一,peer 留着只承担授权与来源
	// 标注(与 daemon_sessions 上那一列同一次降级)。
	conversationID string
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
// rpcerror.AuthState,所以只有**同指纹**的连接接得走一条会话 —— 指纹是接管的授权,不是路由
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
	live   map[connection.Conn]liveConn
	claims map[sessionKey]sessionClaim
	// subs 是每条会话的订阅者集合:上过这条会话的那些连接。属主换人不清空它 ——
	// 接管的语义是「此后由我消费」,不是「把另一方踢下线」。
	subs map[sessionKey]map[connection.Conn]struct{}
}

// connectionCount reports authenticated client connections that are live now.
// PairedPeers is persisted trust and may remain populated while every client is
// offline, so status must read the live registry instead of deriving from it.
func (r *connRegistry) connectionCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.live)
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

// asyncNotifier 是一个订阅者的投递队列:一条 goroutine 顺序发,这个订阅者收到的帧因此
// 仍是原序;入队不阻塞调用方,所以慢的、写不动的订阅者只影响它自己。
//
// 排队的是**已经转换好**的 Protobuf 通知(见 handlers.NotifierPort):发送侧转换完就
// 不再碰它,所以入队时不需要再拷一份 —— 属主与每个订阅者共享同一条消息,各自在自己的
// goroutine 上 marshal(并发只读)。
type asyncNotifier struct {
	n    handlers.NotifierPort
	ch   chan *agentrewire.RpcNotification
	stop chan struct{}
	once sync.Once
}

func newAsyncNotifier(n handlers.NotifierPort) *asyncNotifier {
	a := &asyncNotifier{
		n:    n,
		ch:   make(chan *agentrewire.RpcNotification, subscriberQueueDepth),
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
			if err := a.n.Notify(q); err != nil {
				log.Printf("daemon: fan-out to subscriber failed method=%s err=%v",
					protowire.NotificationMethod(q), err)
			}
		}
	}
}

func (a *asyncNotifier) Notify(notification *agentrewire.RpcNotification) error {
	select {
	case a.ch <- notification:
		return nil
	default:
		method := protowire.NotificationMethod(notification)
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

func (f fanoutNotifier) Notify(notification *agentrewire.RpcNotification) error {
	var delivered bool
	var lastErr error
	for _, extra := range f.extras {
		if err := extra.Notify(notification); err != nil {
			lastErr = err
			continue
		}
		delivered = true
	}
	if f.primary != nil {
		return f.primary.Notify(notification)
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
	conn    connection.Conn
	mcpConn connection.Conn
	at      uint64
}

// peerIdentity 取连接的对端身份(设备指纹)。身份是鉴权成功那一刻才成立的,报了指纹
// 没通过鉴权不算;空指纹不构成可匹配身份(否则一条空指纹连接就能冒领会话的通知),
// 空指纹在 registerMethods 的 auth.* 入参处就已挡下,这里是第二道。
func peerIdentity(c connection.Conn) (string, bool) {
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
func connClosed(c connection.Conn) bool {
	select {
	case <-c.Done():
		return true
	default:
		return false
	}
}

// add 在连接完成 auth.pair / auth.connect / auth.account 之后登记它。同一条连接改认另一个指纹时,
// 它先前以旧指纹认领的会话一并作废 —— 否则旧对端的会话通知会推给一条已经属于别人的连接。
func (r *connRegistry) add(raw any, n handlers.NotifierPort) {
	c := connection.Normalize(raw)
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
		r.live = map[connection.Conn]liveConn{}
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
	conn connection.Conn
	at   uint64
	prev sessionClaim
	ok   bool
	// addedSub 记这次认领是不是**新**把这条连接加进该会话的订阅者集合。被拒的调用
	// 不该让调用方留在会话里旁听,还原时按它撤回;它本来就在里面时不动。
	addedSub bool
}

// claim records a caller's own-peer session target. Account-authorized cross-peer
// operations use claimFor after their origin discriminator has been checked.
func (r *connRegistry) claim(raw any, conversationID string) claimTicket {
	c := connection.Normalize(raw)
	peer, ok := peerIdentity(c)
	if !ok {
		return claimTicket{}
	}
	return r.claimFor(c, peer, conversationID)
}

// claimFor records an already-authorized target peer. Normal callers use
// claim; account-level controls reach this only after ResolveSessionPeer.
func (r *connRegistry) claimFor(raw any, peer string, conversationID string) claimTicket {
	c := connection.Normalize(raw)
	if peer == "" || conversationID == "" {
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
	k := sessionKey{peer: peer, conversationID: conversationID}
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
func (r *connRegistry) addSubLocked(k sessionKey, c connection.Conn) bool {
	if r.live[c].fanout == nil {
		return false
	}
	if r.subs == nil {
		r.subs = map[sessionKey]map[connection.Conn]struct{}{}
	}
	set := r.subs[k]
	if set == nil {
		set = map[connection.Conn]struct{}{}
		r.subs[k] = set
	}
	if _, ok := set[c]; ok {
		return false
	}
	set[c] = struct{}{}
	return true
}

// removeSubLocked 摘掉一份订阅;集合空了连键一起删,免得会话表随会话数无界长。
func (r *connRegistry) removeSubLocked(k sessionKey, c connection.Conn) {
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
func (r *connRegistry) liveForPeerLocked(peer string) connection.Conn {
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
func (r *connRegistry) remove(raw any) {
	c := connection.Normalize(raw)
	if c == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dropLocked(c)
}

func (r *connRegistry) dropLocked(c connection.Conn) {
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
	var owner connection.Conn
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
func (r *connRegistry) subscribersLocked(k sessionKey, exclude connection.Conn) []handlers.NotifierPort {
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
func (r *connRegistry) tunnelTargetFor(peer string, conversationID string) handlers.NotifierPort {
	if peer == "" || conversationID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	claim, ok := r.claims[sessionKey{peer: peer, conversationID: conversationID}]
	if !ok {
		return nil
	}
	return r.live[claim.mcpConn].n
}

// sessionRouter 是某个对端的会话通知出口:按帧上的 conversationId 把每条通知交给发起
// 该会话的那条连接。conversationId 本来就是通知帧上的会话路由字段(见 wire 包注释),
// daemon 侧按它解析没有引入任何协议内容。
type sessionRouter struct {
	reg  *connRegistry
	peer string
}

func (s sessionRouter) Notify(notification *agentrewire.RpcNotification) error {
	conversationID := protowire.NotificationConversationID(notification)
	if conversationID == "" {
		return fmt.Errorf("daemon: cannot route %s: notification carries no conversationId",
			protowire.NotificationMethod(notification))
	}
	n := s.reg.ownerOf(sessionKey{peer: s.peer, conversationID: conversationID})
	if n == nil {
		// 发起该会话的连接已经断开:通知已经落库,等同指纹的新连接接管后补齐。
		return fmt.Errorf("daemon: conversation %s has no live connection", conversationID)
	}
	return n.Notify(notification)
}

func (s sessionRouter) Request(context.Context, string, any, any) error {
	// Reverse requests (the MCP tunnel) resolve their explicit peer/session URL
	// discriminator through connRegistry.tunnelTargetFor instead.
	return errors.New("daemon: reverse requests are not routable per session")
}

// notifierForPeer 解析某个对端的推送出口。每次发送时重新解析,绝不静态捕获 —— 断连
// 重连会换一条连接,捕获下来的端口在重连后指向死连接。
func (d *Daemon) notifierForPeer(peer string) handlers.NotifierPort {
	return d.conns.routerFor(peer)
}

// tunnelTargetFor resolves a daemon-local MCP request to its originating
// session owner; no global active-connection heuristic is permitted.
func (d *Daemon) tunnelTargetFor(peer string, conversationID string) handlers.NotifierPort {
	return d.conns.tunnelTargetFor(peer, conversationID)
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
// relayLinkOptions 是中继链路里**与运行期身份无关**的那部分配置,单独拎出来是为了
// 能被装配用例读到(见 TestDaemon_RelayLinkSharesTheDirectFrameBound)。
//
// MaxFrameBytes 是直连那条 WebSocket 的载荷预算加一个信封头:两条路收的都是别的设备
// 发来的字节,没理由一条有界一条不有界;而中继这条收到的是服务端**套过信封**的载荷
// (2 字节长度 + 通道 ID),读上限少了这一截,一份刚好顶格的合法载荷就会触发 1009 ——
// 拆掉的是整条链路,那台机器上所有虚拟通道一起陪葬。
//
// 这个数在这里算好传下去,relaytransport 因此不必反向 import 它上面的 protorpc ——
// 传输层不该依赖 RPC 层。
func relayLinkOptions() relaytransport.HubLinkOptions {
	return relaytransport.HubLinkOptions{
		MaxFrameBytes: protorpc.MaxFrameBytes + relaytransport.MaxEnvelopeBytes,
	}
}

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
	auth := auth.NewAuthHandlers(st, pm, rl)

	d := &Daemon{
		opts: opts, state: st, db: gormDB,
		journal:      notificationJournal{db: gormDB},
		sessionStore: daemonSessionStore{db: gormDB},
		pairing:      pm, ratelim: rl,
		auth: auth, protobufRegistry: protorpc.NewRegistry(),
		steerSource:     newSteerSourceStore(),
		generations:     sessions.NewRegistry(),
		runtimeHandlers: map[connection.Conn]*handlers.RuntimeHandlers{},
	}
	// 订阅资格的账号门:登记表每次解析收件人时现问 daemon 此刻的归属账号。
	d.conns.claimedAccountID = d.claimedAccountID
	// 中转链路无条件构造：认领状态改由每次 dial 时重新解析（relayServerURL），
	// 不在这里判一次。判一次的后果是未认领启动的进程即使之后登录了也永远没有链路
	// —— login 是另一个进程，写完 state.json 就退出，没有东西会回来建它。
	hubOpts := relayLinkOptions()
	hubOpts.ServerURLProvider = d.relayServerURL
	hubOpts.AccessTokenProvider = d.currentAccessToken
	d.hub = relaytransport.NewHubLink(hubOpts)
	d.mux = relaytransport.NewMultiplexer(d.hub)
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
	d.activity = handlers.NewActivityHandlers(handlers.ActivityDeps{Sessions: d.sessionStore})
	d.sessionModelTarget = handlers.NewSessionModelTargetHandlers(handlers.SessionModelTargetDeps{
		Sessions:         d.sessionStore,
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
	d.registerProtobufMethods()
	return d, nil
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
	lan := protorpc.NewLANServer(protorpc.LANOpts{
		Host:        d.opts.LANHost,
		Port:        d.opts.LANPort,
		TLSCertFile: d.opts.TLSCertFile,
		TLSKeyFile:  d.opts.TLSKeyFile,
		Registry:    d.protobufRegistry,
		OnConn:      d.bindProtobufConn,
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
	// 账号信号(决策 13)不再是一条独立连接:它现在是同一条中继连接上的保留通道,
	// 由 serveRelayChannels → serveAccountSignal 消费,天然只在 auth.account 握手
	// 成功之后才可能出现——不需要再像旧的 /v1/account/channel 那样额外等
	// awaitClaim。未认领的 daemon 上,relayServerURL() 返回空串,hub 连不上,
	// 保留通道自然也不会出现。
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
//
// 保留号(relaytransport.SignalChannelID,决策 13/14)是一个例外:它不是一条 RPC
// 连接,服务端也从不在它上面完成 auth.pair / auth.connect 握手——把它交给
// protorpc.NewConn 只会让第一帧的协议解码失败。它交给 serveAccountSignal,而不是
// RPC 注册表。
func (d *Daemon) serveRelayChannels(ctx context.Context, mux *relaytransport.Multiplexer) {
	for {
		select {
		case <-ctx.Done():
			return
		case channel := <-mux.Accept():
			if channel == nil {
				return
			}
			if channel.ID() == relaytransport.SignalChannelID {
				go d.serveAccountSignal(ctx, channel)
				continue
			}
			conn := protorpc.NewConn(protorpc.NewPayloadFrameConn(channel), d.protobufRegistry.Clone())
			d.bindProtobufConn(conn)
			go conn.Serve(ctx)
		}
	}
}

// serveAccountSignal consumes the reserved account-signal channel (决策 13):
// it replaces enginesnapshot.Manager's own dial + retry loop against the now
// deleted /v1/account/channel endpoint. The channel is server-opened and
// only-out — a read failure or an empty payload both mean "this channel is
// gone" (the same convention ordinary relay channels use for a close frame),
// and either one simply ends this goroutine; the account still gets refreshed
// on the next relay reconnect (PullAsync("relay_connected"), see
// startEngineSnapshotPulls) or the next time the server reopens the channel.
//
// 未知帧被忽略而不是断开:通道日后会承载别的信号种类,旧的 daemon 构建不该因此
// 判死这条它还认得的通道(与 accountchan_svc 包注释「它可以不可靠」同一前提)。
func (d *Daemon) serveAccountSignal(ctx context.Context, channel relaytransport.PayloadChannel) {
	for {
		payload, err := channel.ReadPayload()
		if err != nil {
			return
		}
		if len(payload) == 0 {
			return
		}
		if _, known, err := syncwire.DecodeAccountChannelFrame(payload); err != nil || !known {
			continue
		}
		d.engineSnapshot.PullAsync(ctx, "account_signal")
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
func (d *Daemon) newRuntimeHandlers() *handlers.RuntimeHandlers {
	return handlers.NewRuntimeHandlers(handlers.RuntimeDeps{
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
		connection connection.Conn
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
	//
	// synchronous(NORMAL): 上面那句「每个流式事件一条同步事务」同时决定了 fsync 的代价 ——
	// SQLite 默认的 FULL 档在 WAL 下每次提交都 fsync WAL,实测 603µs/事件,NORMAL 档
	// 213µs,即每个 token 白付约 390µs、一条三千帧的回复白付约 1.2s。WAL + NORMAL 仍然
	// 崩溃安全:进程崩溃不损坏数据库,只在断电/内核崩溃时可能丢最后若干已提交事务。这份
	// 通知日志本就是可重建的(桌面端按 seq 重新拉取),用它换掉每帧一次 fsync 是划算的。
	// 与 internal/bootstrap/cago.go 的 sqliteDSN 同源取舍。
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
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
// 写进本实例的 daemon_notification_journal,seq 由仓储在同一条语句里分配。
//
// 它自己往 ctx 上注入本 Daemon 的 db 句柄:通知的生产者是脱离请求 ctx 的 fanout
// goroutine(它可能拿到的只是一个裸 ctx),而 daemon 故意不写 db.SetDefault(同进程
// 多个 Daemon 会互相串库,见 Daemon.db 注释),所以句柄只能从这里给。
type notificationJournal struct{ db *gorm.DB }

func (j notificationJournal) Append(ctx context.Context, peerFingerprint, peerSessionID string, payload []byte) (int64, error) {
	row := &notification_repo.NotificationLog{
		ConversationID:  peerSessionID,
		PeerFingerprint: peerFingerprint,
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

// Start 建行 / 幂等覆盖一条会话。handlers.SessionRecord.PeerSessionID 装的就是线上那个
// conversation_id(线格式上早已只有它),库里那一列因此直接由它填;handlers 那一侧的字段
// 名还没跟着改,是本轮之外的一笔命名欠账。
func (s daemonSessionStore) Start(ctx context.Context, rec handlers.SessionRecord) error {
	return session_repo.Session().Upsert(dbpkg.WithContextDB(ctx, s.db), &session_repo.DaemonSession{
		ConversationID:    rec.PeerSessionID,
		PeerFingerprint:   rec.PeerFingerprint,
		AgentID:           rec.AgentID,
		Cwd:               rec.Cwd,
		BackendType:       rec.BackendType,
		LifecycleState:    rec.LifecycleState,
		Title:             rec.Title,
		AgentSyncID:       rec.AgentSyncID,
		ProjectSyncID:     rec.ProjectSyncID,
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

func (s daemonSessionStore) List(ctx context.Context, peerFingerprint, keyword string) ([]handlers.SessionRecord, error) {
	rows, err := session_repo.Session().ListByPeer(dbpkg.WithContextDB(ctx, s.db), peerFingerprint, keyword)
	if err != nil {
		return nil, err
	}
	out := make([]handlers.SessionRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, sessionRecordOf(row))
	}
	return out, nil
}

func (s daemonSessionStore) ListAll(ctx context.Context, keyword string) ([]handlers.SessionRecord, error) {
	rows, err := session_repo.Session().ListAll(dbpkg.WithContextDB(ctx, s.db), keyword)
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

func (s daemonSessionStore) SetModelTarget(
	ctx context.Context, peerFingerprint, peerSessionID, providerKey, modelKey string,
) (int64, error) {
	return session_repo.Session().SetModelTarget(
		dbpkg.WithContextDB(ctx, s.db), peerFingerprint, peerSessionID, providerKey, modelKey)
}

func sessionRecordOf(row *session_repo.DaemonSession) handlers.SessionRecord {
	return handlers.SessionRecord{
		PeerFingerprint:   row.PeerFingerprint,
		PeerSessionID:     row.ConversationID,
		AgentID:           row.AgentID,
		Cwd:               row.Cwd,
		BackendType:       row.BackendType,
		LifecycleState:    row.LifecycleState,
		Title:             row.Title,
		AgentSyncID:       row.AgentSyncID,
		ProjectSyncID:     row.ProjectSyncID,
		ProviderSessionID: row.ProviderSessionID,
		LastMessageAt:     row.LastMessageAt,
		Createtime:        row.Createtime,
		ProviderKey:       row.ProviderKey,
		ModelKey:          row.ModelKey,
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
			Seq:     row.Seq,
			Payload: []byte(row.Payload),
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

package portforward

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/tunnelheader"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
	"github.com/agentre-hub/agentre/pkg/wire/wirelimits"
)

// 一条转发流的设备侧实现。
//
// 三条纪律决定了这个文件的形状,每一条都有对应的用例(stream_test.go):
//
//  1. **一块都不许丢。** terminal.go 的那套流控是有损的(队列满了丢最老的一块,再插
//     一行 [--- output throttled ---])。终端上少几行还看得懂,HTTP 响应体丢一块就是
//     文件损坏、页面白屏。这里没有任何一条丢弃分支。
//  2. **背压不许靠阻塞。** protorpc 的写锁全连接互斥、通知在读循环里同步派发,一条流
//     把 Notify 顶住,顶死的是同一条连接上所有别的会话(硬不变量 2)。所以生产者停的
//     是「从本机 socket 读」这一件事:未确认量顶到窗口就不再读,内核的 TCP 窗口把压力
//     原路还给被转发的那个服务。发通知在流自己的 goroutine 里,期间不握任何
//     open / write / close / ack 要用的锁。
//  3. **101 之后不再解释内容。** 升级前设备在 HTTP 这一层理解请求与响应头(它要写请求
//     行、要认出 101);升级后两个方向都只搬字节,请求体的分块封装也随之停用。

// Notifier 把三条通知送回发起这条流的那个宿主。生产上就是 protorpc.Conn.Notify。
type Notifier func(*agentrewire.RpcNotification) error

// StreamOptions 是连接级注册面交出的那两件东西。
type StreamOptions struct {
	// Gate 是授权闸门。open 只经它拨号 —— 绕过它就等于把「转到哪台机器的哪个端口」
	// 交给调用方决定,而这条限制的立论正是不依赖任何客户端做对。
	Gate *Handlers
	// Notify 把通知写回这条连接。缺席时流照常跑,只是没人听得见。
	Notify Notifier
	// creditIdleTimeout 是响应方向「窗口已经满了却等不到 ack」的容忍上限,见
	// awaitCredit。0 用 defaultCreditIdleTimeout。不导出:它是实现内部的 liveness
	// 边界,不是调用方要配的东西(包内用例可以把它压到毫秒级)。
	creditIdleTimeout time.Duration
}

// Streams 是**一条连接上**开着的全部转发流。
//
// 生命周期跟着承载它的那条连接走(与 terminal.* 同形):连接一断 CloseAll,不留悬挂
// 的流,也不留悬挂的本机 socket。
type Streams struct {
	gate   *Handlers
	notify Notifier

	// creditIdleTimeout 见 StreamOptions。
	creditIdleTimeout time.Duration

	// unwatch 把这份流表从闸门的撤销面上摘下来。注销跟着 CloseAll 走 —— 承载这条
	// 连接的宿主本来就保证连接一断就 CloseAll(BindConn),撤销面因此不需要知道这台
	// 机器上有几条连接,也不会因为连接走掉而留下一份没人再看的流表。
	unwatch func()

	mu        sync.Mutex
	live      map[string]*stream
	reserving map[string]struct{}
	shut      bool
}

func NewStreams(options StreamOptions) *Streams {
	creditIdleTimeout := options.creditIdleTimeout
	if creditIdleTimeout <= 0 {
		creditIdleTimeout = defaultCreditIdleTimeout
	}
	streams := &Streams{
		gate:              options.Gate,
		notify:            options.Notify,
		creditIdleTimeout: creditIdleTimeout,
		live:              make(map[string]*stream),
		reserving:         make(map[string]struct{}),
	}
	if options.Gate != nil {
		// 订阅在构造这一步就完成,而不是交给宿主多写一行:一条能开流却收不到「这条
		// 映射没了」的流表,与规格「映射被删除或停用 → 正在进行的流立即关闭」直接
		// 冲突,而漏写它编译绿、测试绿,只有用户那条还在跑的转发看得出来。
		streams.unwatch = options.Gate.watchRevocations(streams)
	}
	return streams
}

// 流族自己的失败。三种都不是「这次访问不被允许」——那一族在 portforward.go。
var (
	// ErrStreamNotFound:write / close / ack 指向的流不存在。调用方的状态机落后了
	// 一步(它已经收尾了),或者它从没 open 过。
	ErrStreamNotFound = &rpcerror.Error{Code: rpcerror.CodePortForwardStreamNotFound, Message: "port forward: no such stream"}
	// ErrStreamExists:这条连接上已经有一条同号的流。流号由调用方生成,撞号是它的
	// bug,不能默默把前一条顶掉 —— 那会让两个下载互相截断。
	ErrStreamExists = &rpcerror.Error{Code: rpcerror.CodeInvalidParams, Message: "port forward: stream id already in use"}
	// ErrConnectionGone:承载这些流的那条连接已经收尾,不再受理新的 open。
	ErrConnectionGone = &rpcerror.Error{Code: rpcerror.CodeInternal, Message: "port forward: carrying connection is gone"}
)

// maxStreamIDLength 与终端那一族取同一个量级:流号只是这条连接内的一个名字,长到
// 需要上限时它已经不是名字了。
const maxStreamIDLength = 128

// loopbackDialTimeout 只覆盖**拨号**这一步。本机环回上连不上就是连不上,拖长了只会
// 让「端口上没有服务」这条答复来得更晚。
const loopbackDialTimeout = 5 * time.Second

// defaultCreditIdleTimeout 是响应方向「窗口已经满了、却连一条 ack 都等不到」的容忍
// 上限,见 awaitCredit。
//
// 取 2 分钟:窗口是 4 MiB、宿主每消费 1 MiB 回一次累计 ack,这个数因此等价于「消费者
// 得把速率维持在约 8.5 KiB/s 以上」。比这更慢的消费者会被判成已经走掉——而它正是这条
// 上限要收掉的东西:宿主放手之后不会再发 ack,没有上限的话 pump 会一直停在窗口上,
// 连它的流表项、goroutine 与到本机服务的 loopback 连接一起挂着(规格「每一种断开都要
// 有确定的收尾,不留悬挂的流」)。
const defaultCreditIdleTimeout = 2 * time.Minute

var loopbackDialer = &net.Dialer{Timeout: loopbackDialTimeout}

// DialLoopback 是生产上那一条拨号:目标恒为 127.0.0.1:<port>。
//
// 它就住在闸门旁边,因为「目标恒为环回」这件事必须只有一个出处 —— 两个宿主各写一份
// net.Dial,迟早有一份把主机那一格接成了请求里带来的值。
func DialLoopback(ctx context.Context, port int) (net.Conn, error) {
	return loopbackDialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
}

// ---------- 请求这一侧 ----------

// bodyFraming 是设备把请求体写给上游时用的封装。
type bodyFraming int

const (
	// framingNone:没有请求体,或者已经升级(此后是纯字节,不封装)。
	framingNone bodyFraming = iota
	// framingVerbatim:调用方给了 Content-Length,字节逐字写过去,上游按长度读完。
	framingVerbatim
	// framingChunked:有请求体但长度未知(浏览器发的就是 chunked,宿主读到的是已经
	// 解过块的字节),设备重新按块封装,eof 时补上结束块。
	framingChunked
)

// openPlan 是一次 open 在**拨号之前**就能算清的全部东西。先算再拨,是为了让「请求行
// 本身就不合法」这种失败不至于先在那台设备上开一条连接。
type openPlan struct {
	head    []byte
	method  string
	framing bodyFraming
	// port 是这条流转到的那个端口。它跟着计划走而不是另外传一遍,因为撤销面认的正是
	// 端口:声明被停用 / 删除时,要关的是「这个端口上的流」,流表得答得出这一格。
	port int
}

func invalidParams(message string) error {
	return &rpcerror.Error{Code: rpcerror.CodeInvalidParams, Message: message}
}

// validToken 判一段字符串是不是 RFC 7230 的 token(方法名与头名都必须是)。
//
// 这道校验不是洁癖:方法名或头名里放进一个 CR/LF,写出去的就是两个请求 —— 请求走私
// 的入口正在这里,而这条流写的是一个**没有解析框架替它兜底**的裸 socket。
func validToken(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		switch c := value[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// wireRequestHeaders 把线上那一格折成 http.Header,顺带丢掉放不进一行的值。
func wireRequestHeaders(in map[string]*agentrewire.HeaderValues) http.Header {
	out := make(http.Header, len(in))
	for name, values := range in {
		if !validToken(name) {
			continue
		}
		for _, value := range values.GetValues() {
			if strings.ContainsAny(value, "\r\n") {
				continue
			}
			out.Add(name, value)
		}
	}
	return out
}

// wireHeaderValues 把应答头折回线上那一格。
func wireHeaderValues(in map[string][]string) map[string]*agentrewire.HeaderValues {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]*agentrewire.HeaderValues, len(in))
	for name, values := range in {
		out[name] = &agentrewire.HeaderValues{Values: append([]string(nil), values...)}
	}
	return out
}

// isUpgradeRequest 认出「这一跳在做协议升级」。两格都要:Connection 里列了 upgrade,
// 且 Upgrade 说了升到什么。
func isUpgradeRequest(headers http.Header) bool {
	if headers.Get("Upgrade") == "" {
		return false
	}
	for _, value := range headers.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return true
			}
		}
	}
	return false
}

func validContentLength(value string) bool {
	if value == "" {
		return false
	}
	length, err := strconv.ParseInt(value, 10, 64)
	return err == nil && length >= 0
}

// planOpen 把一次 open 折成写给上游的那段请求头。
//
// Host 由设备**重写**成 127.0.0.1:<port>,而不是原样带上调用方那一格:目标主机不在
// 协议里,设备恒连环回,Host 说的必须是它真的连到了哪儿。
func planOpen(request *agentrewire.PortForwardOpenRequest) (*openPlan, error) {
	method := strings.ToUpper(strings.TrimSpace(request.GetMethod()))
	if method == "" {
		method = http.MethodGet
	}
	if !validToken(method) {
		return nil, invalidParams("port forward: invalid request method")
	}
	path := request.GetPath()
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if strings.ContainsAny(path, " \t\r\n") {
		return nil, invalidParams("port forward: invalid request path")
	}

	headers := wireRequestHeaders(request.GetHeaders())
	upgrade := isUpgradeRequest(headers)
	framing, contentLength := framingNone, ""
	if request.GetHasBody() {
		if value := headers.Get("Content-Length"); validContentLength(value) {
			framing, contentLength = framingVerbatim, value
		} else {
			framing = framingChunked
		}
	}

	var head bytes.Buffer
	fmt.Fprintf(&head, "%s %s HTTP/1.1\r\n", method, path)
	fmt.Fprintf(&head, "Host: 127.0.0.1:%d\r\n", request.GetPort())
	for name, values := range tunnelheader.SanitizeWith(headers, tunnelheader.Options{AllowUpgrade: upgrade}) {
		for _, value := range values {
			fmt.Fprintf(&head, "%s: %s\r\n", name, value)
		}
	}
	switch framing {
	case framingVerbatim:
		fmt.Fprintf(&head, "Content-Length: %s\r\n", contentLength)
	case framingChunked:
		head.WriteString("Transfer-Encoding: chunked\r\n")
	case framingNone:
		// 没有请求体就一格框架头都不写:上游在空行处就知道这个请求已经完整,不必
		// 等一个永远不来的 body。
		//
		// 刻意**不**半关写方向。升级请求同样没有请求体,却正要靠这个方向送 101
		// 之后的帧;而对普通请求,一个自带框架的请求头本来就不需要 FIN 才能被答复
		// (半关还会让 net/http 把请求 ctx 当作「客户端走了」取消掉)。
	}
	head.WriteString("\r\n")

	return &openPlan{head: head.Bytes(), method: method, framing: framing, port: int(request.GetPort())}, nil
}

// ---------- 一条流 ----------

// closeReason 是收尾通知里那两格。code 只在有 portforward.* 领域码可说时才非 0。
type closeReason struct {
	code    int32
	token   string
	message string
}

type stream struct {
	id      string
	port    int
	conn    net.Conn
	reader  *bufio.Reader
	notify  Notifier
	method  string
	framing bodyFraming
	window  int64

	// writeMu 串行化写往上游的方向。它**只**盖住 conn.Write,与信用那把锁没有交集,
	// 一次慢写因此不会挡住同一条流的 ack。
	writeMu sync.Mutex

	// mu 盖住信用账与 done。持有它的每一段都是几行赋值,绝不跨越 Notify 或 socket
	// 读写 —— 这是「别的方法仍能应答」在实现上的全部内容。
	//
	// credit 是「信用账变了」的信号:ack 前进或 done 置位时非阻塞地写一格(缓冲 1)。
	// 它取代了原本的 sync.Cond,因为等信用这件事现在**必须有上限**(见 awaitCredit)。
	mu       sync.Mutex
	credit   chan struct{}
	sent     int64
	acked    int64
	done     bool
	upgraded bool

	// creditIdleTimeout 见 StreamOptions;窗口满着却等不到 ack 超过它,这条流就认宿主
	// 已经走掉。
	creditIdleTimeout time.Duration

	closeOnce sync.Once
	reason    closeReason
}

func newStream(id string, conn net.Conn, plan *openPlan, window int64, notify Notifier, creditIdleTimeout time.Duration) *stream {
	return &stream{
		id: id, port: plan.port, conn: conn, reader: bufio.NewReader(conn), notify: notify,
		method: plan.method, framing: plan.framing, window: window,
		credit: make(chan struct{}, 1), creditIdleTimeout: creditIdleTimeout,
	}
}

func (st *stream) emit(notification *agentrewire.RpcNotification) error {
	if st.notify == nil {
		return nil
	}
	return st.notify(notification)
}

func (st *stream) setUpgraded(upgraded bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.upgraded = upgraded
}

// activeFraming 说这一刻写往上游的字节该不该被封装。升级之后一律不封装:那时候流上
// 跑的已经不是 HTTP 了,设备没有立场解释它。
func (st *stream) activeFraming() bodyFraming {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.upgraded {
		return framingNone
	}
	return st.framing
}

// awaitCredit 等到还有信用可用,回这一次最多能读多少字节。
//
// 返回 false 表示这条流已经收尾。这个等待是整套背压唯一的阻塞点,而它阻塞的是**这条
// 流自己的 goroutine**,不是连接的读循环、也不是它的写锁。
//
// **它必须有上限。** 宿主放手之后不会再发 ack,窗口一满就再没有谁来叫醒这个等待:
// 没有上限的话这条流、它的 goroutine 与到本机服务的 loopback 连接会一起挂到整条
// protorpc 连接被拆掉为止(而桌面端那条专属监听按设计握着长活租约,那条连接可以
// 活一整天)。上限按「最后一次 ack 之后静置了多久」算,等 101、等响应头那些与信用无关
// 的等待不会倒计时;
// 每次 ack 都把上限重新推满,所以只是慢、但一直在消费的宿主不会被误杀。
func (st *stream) awaitCredit() (int64, bool) {
	deadline := time.Now().Add(st.creditIdleTimeout)
	for {
		st.mu.Lock()
		if st.done {
			st.mu.Unlock()
			return 0, false
		}
		if credit := st.window - (st.sent - st.acked); credit > 0 {
			st.mu.Unlock()
			return credit, true
		}
		st.mu.Unlock()

		timer := time.NewTimer(time.Until(deadline))
		select {
		case <-st.credit:
			timer.Stop()
			deadline = time.Now().Add(st.creditIdleTimeout)
		case <-timer.C:
			if !st.abandoned() {
				// 截止这一刻恰好来了一条 ack(或窗口本来就不再是满的):不是放弃,
				// 重新起算。
				deadline = time.Now().Add(st.creditIdleTimeout)
				continue
			}
			st.shutdown(closeReason{
				token:   "host_gone",
				message: "port forward: host stopped consuming the response",
			})
			return 0, false
		}
	}
}

// abandoned 判「窗口仍然满着,而且没有任何把它推开的迹象」——awaitCredit 的截止
// 到了之后用它做最后一次复查,免得与一条恰好同时落地的 ack 擦肩而过。
func (st *stream) abandoned() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return !st.done && st.sent-st.acked >= st.window
}

func (st *stream) addSent(n int64) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.sent += n
}

// ack 收下累计已消费字节数。累计而不是增量:丢一条只会让窗口暂时偏紧,下一条就把账
// 追平。倒退的与超过已发量的都按上界收 —— 对端的账错了不该让设备侧的账跟着错。
func (st *stream) ack(consumed int64) {
	st.mu.Lock()
	if consumed > st.acked {
		st.acked = consumed
	}
	if st.acked > st.sent {
		st.acked = st.sent
	}
	st.mu.Unlock()
	st.signalCredit()
}

// signalCredit 叫醒卡在 awaitCredit 上的生产者。缓冲 1 + 非阻塞:信用账是一条单调
// 前进的账,丢一条信号只会让等待者多复查一次账,不会漏掉任何一次前进。
func (st *stream) signalCredit() {
	select {
	case st.credit <- struct{}{}:
	default:
	}
}

// shutdown 关掉到本机服务的那条连接并唤醒等信用的生产者。第一个说出原因的人说了算:
// 宿主主动关流之后上游的读当然也会失败,但用户要听的是前一句。
func (st *stream) shutdown(reason closeReason) {
	st.closeOnce.Do(func() {
		st.reason = reason
		st.mu.Lock()
		st.done = true
		st.mu.Unlock()
		st.signalCredit()
		_ = st.conn.Close()
	})
}

func (st *stream) writeUpstream(data []byte, eof bool) error {
	st.writeMu.Lock()
	defer st.writeMu.Unlock()
	framing := st.activeFraming()
	if len(data) > 0 {
		if framing == framingChunked {
			if _, err := fmt.Fprintf(st.conn, "%x\r\n", len(data)); err != nil {
				return err
			}
		}
		if _, err := st.conn.Write(data); err != nil {
			return err
		}
		if framing == framingChunked {
			if _, err := io.WriteString(st.conn, "\r\n"); err != nil {
				return err
			}
		}
	}
	if eof && framing == framingChunked {
		if _, err := io.WriteString(st.conn, "0\r\n\r\n"); err != nil {
			return err
		}
	}
	return nil
}

// run 是这条流的生产者:读应答头 → 发响应头通知 → 按信用把 body 分片带回。
//
// 它把应答体的 Closer 交回给调用方而不是自己 defer 关掉,因为**关的次序要紧**:
// net/http 的 body.Close 在连接还活着时会先把剩下的字节 drain 干净(为了复用连接),
// 而一条被宿主中途关掉的下载,剩下的可能是几百兆 —— 必须先断 socket 再关 body,
// 那一次 drain 才会立刻以错误收场。
func (st *stream) run() (closeReason, io.Closer) {
	response, err := st.readFinalResponse()
	if err != nil {
		return closeReason{token: "upstream_error", message: err.Error()}, nil
	}
	upgraded := response.StatusCode == http.StatusSwitchingProtocols
	st.setUpgraded(upgraded)
	if emitErr := st.emit(&agentrewire.RpcNotification{
		Payload: &agentrewire.RpcNotification_PortForwardResponse{
			PortForwardResponse: &agentrewire.PortForwardResponseNotification{
				StreamId: st.id,
				Status:   int32(response.StatusCode),
				Headers: wireHeaderValues(tunnelheader.SanitizeWith(response.Header, tunnelheader.Options{
					// 升级应答里的 Connection / Upgrade / Sec-WebSocket-Accept 要原样带回,
					// 宿主拿它们去完成与浏览器那一侧的握手。
					AllowUpgrade: upgraded,
					// Content-Length 在这个方向上是准的(设备逐字搬上游的 body),留着让
					// 浏览器显示得出下载进度;升级之后没有长度可言。
					AllowLength: !upgraded,
				})),
				Upgraded: upgraded,
			},
		},
	}); emitErr != nil {
		return closeReason{token: "host_gone", message: emitErr.Error()}, response.Body
	}

	source := io.Reader(response.Body)
	if upgraded {
		// 101 之后 body 那一格没有意义:ReadResponse 只读到头部为止,升级之后的字节
		// 有一部分已经躺在 bufio 里,继续读的必须是这个 reader 而不是裸 conn。
		source = st.reader
	}
	return st.copyBody(source), response.Body
}

// maxInterimResponses 是一次应答里最多容忍几条中间应答。与 net/http 自己那条客户端
// 路径取同一个数:上游正常最多回一两条,还在回就是它坏了,而这个循环读的是一个不受
// 本进程控制的 socket —— 没有上界的话,一个一直吐 1xx 的本机服务能把这条 goroutine
// 永远拴在那里。
const maxInterimResponses = 5

// isInterimStatus 判一个状态码是不是「还没说完」的中间应答。101 不算 —— 理由见下。
func isInterimStatus(status int) bool {
	return status >= 100 && status < 200 && status != http.StatusSwitchingProtocols
}

// readFinalResponse 读到上游那条**最终**应答,途中跳过全部中间应答。
//
// 1xx 不是最终应答。带 `Expect: 100-continue` 的请求(curl 对超过 1KB 的请求体自动
// 加这一格,浏览器与多数客户端在大上传时同理)上游会先回一条 `100 Continue`,RFC 9110
// 还允许它在最终应答之前回多条。把第一条读到的状态行当成最终应答,这条流就会在那条
// 空的 100 之后立刻以 eof 收尾 —— 上游真正的状态码、响应头与正文一个都到不了宿主,
// 而不带 Expect 的同一个请求却是好的。
//
// **101 不在此列**:它虽然也是 1xx,却是这条流的终点(此后是纯字节,见纪律 3),
// 要原样交给上面那一层。
func (st *stream) readFinalResponse() (*http.Response, error) {
	for interim := 0; ; interim++ {
		response, err := http.ReadResponse(st.reader, &http.Request{Method: st.method})
		if err != nil {
			return nil, err
		}
		if !isInterimStatus(response.StatusCode) {
			return response, nil
		}
		// 中间应答没有正文(ReadResponse 对 1xx 判定的长度就是 0),关掉它接着读下一条。
		_ = response.Body.Close()
		if interim >= maxInterimResponses {
			return nil, errors.New("port forward: too many 1xx responses from the local service")
		}
	}
}

// copyBody 把应答方向的字节按信用一块块带回。**没有任何一条丢弃分支** —— 取不到信用
// 就不读,而不是读了丢掉。
func (st *stream) copyBody(source io.Reader) closeReason {
	buffer := make([]byte, wirelimits.PortForwardChunkBytes)
	for {
		credit, live := st.awaitCredit()
		if !live {
			return closeReason{token: "closed", message: "port forward: stream closed"}
		}
		if credit > int64(len(buffer)) {
			credit = int64(len(buffer))
		}
		n, err := source.Read(buffer[:credit])
		if n > 0 {
			st.addSent(int64(n))
			chunk := make([]byte, n)
			copy(chunk, buffer[:n])
			if emitErr := st.emit(&agentrewire.RpcNotification{
				Payload: &agentrewire.RpcNotification_PortForwardData{
					PortForwardData: &agentrewire.PortForwardDataNotification{StreamId: st.id, Data: chunk},
				},
			}); emitErr != nil {
				return closeReason{token: "host_gone", message: emitErr.Error()}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return closeReason{token: "eof", message: "port forward: upstream finished"}
			}
			return closeReason{token: "upstream_error", message: err.Error()}
		}
	}
}

// pump 跑完一条流,并保证它**一定**留下一条收尾通知。
//
// 先把它从连接的流表里摘掉再发通知:宿主收到 closed 之后再拿这个流号来调什么,得到的
// 必须是 StreamNotFound,而不是一条已经死了的流。
func (st *stream) pump(owner *Streams) {
	reason, body := st.run()
	// 先断 socket,再关 body:次序反了,一次中途取消的大下载会卡在 net/http 的
	// drain 上(见 run 的注释)。
	st.shutdown(reason)
	if body != nil {
		_ = body.Close()
	}
	owner.forget(st)
	_ = st.emit(&agentrewire.RpcNotification{
		Payload: &agentrewire.RpcNotification_PortForwardClosed{
			PortForwardClosed: &agentrewire.PortForwardClosedNotification{
				StreamId: st.id, Code: st.reason.code, Reason: st.reason.token, Message: st.reason.message,
			},
		},
	})
}

// ---------- 连接级的四个方法 ----------

func (s *Streams) lookup(id string) *stream {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live[id]
}

func (s *Streams) forget(st *stream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.live[st.id] == st {
		delete(s.live, st.id)
	}
}

// windowFor 定这条流的信用窗口。0 用默认值;上限压在载荷硬顶之下没有意义(窗口不占
// 设备内存,分片才占),但下限必须容得下一块分片,否则第一块就发不出去。
func windowFor(requested uint64) int64 {
	if requested == 0 || requested > math.MaxInt64 {
		return wirelimits.PortForwardWindowBytes
	}
	return int64(requested)
}

// Open 开一条转发流:先把请求头算清、再经**闸门**拨号、写出请求头,最后才起生产者。
//
// 「端口没有声明」「映射已停用」「端口上没有服务在监听」三种失败都落在这一次调用的
// 应答上(闸门原样交回它们的领域码),而不是等到某条通知里 —— 宿主要拿它去渲染两张
// 不同的失败页。
func (s *Streams) Open(ctx context.Context, request *agentrewire.PortForwardOpenRequest) (*agentrewire.PortForwardOpenResponse, error) {
	id := request.GetStreamId()
	if id == "" || len(id) > maxStreamIDLength {
		return nil, invalidParams("port forward: invalid stream id")
	}
	if s.gate == nil {
		return nil, &rpcerror.Error{Code: rpcerror.CodeInternal, Message: "port forward: no gate wired on this host"}
	}
	plan, err := planOpen(request)
	if err != nil {
		return nil, err
	}
	if err := s.reserve(id); err != nil {
		return nil, err
	}
	defer s.release(id)

	// 拨号在闸门里,判定先于它 —— 这里没有第二条拨号路径。
	conn, err := s.gate.DialDeclared(ctx, int(request.GetPort()))
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(plan.head); err != nil {
		_ = conn.Close()
		logger.Ctx(ctx).Warn("portforward.Open: 写请求头失败", zap.String("streamId", id), zap.Error(err))
		return nil, &rpcerror.Error{Code: rpcerror.CodeInternal, Message: "port forward: cannot write request to the local service"}
	}

	st := newStream(id, conn, plan, windowFor(request.GetWindowBytes()), s.notify, s.creditIdleTimeout)
	if err := s.adopt(st); err != nil {
		_ = conn.Close()
		return nil, err
	}
	go st.pump(s)
	return &agentrewire.PortForwardOpenResponse{StreamId: id}, nil
}

// reserve 占住这个流号。占位与落表分成两步,是为了让**拨号不在锁里** —— 拨一次号最多
// 要 loopbackDialTimeout,握着流表拨就等于让同一条连接上别的流的 ack 排在它后面。
func (s *Streams) reserve(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shut {
		return ErrConnectionGone
	}
	if _, live := s.live[id]; live {
		return ErrStreamExists
	}
	if _, reserving := s.reserving[id]; reserving {
		return ErrStreamExists
	}
	s.reserving[id] = struct{}{}
	return nil
}

func (s *Streams) release(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.reserving, id)
}

func (s *Streams) adopt(st *stream) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shut {
		return ErrConnectionGone
	}
	s.live[st.id] = st
	return nil
}

// Write 送一块请求体(101 之后是纯字节)。这个方向不需要单独的信用:这次调用的应答
// 就是这一块的凭据,调用方在它回来之前不发下一块。
func (s *Streams) Write(ctx context.Context, request *agentrewire.PortForwardWriteRequest) (*agentrewire.Empty, error) {
	st := s.lookup(request.GetStreamId())
	if st == nil {
		return nil, ErrStreamNotFound
	}
	if err := st.writeUpstream(request.GetData(), request.GetEof()); err != nil {
		logger.Ctx(ctx).Warn("portforward.Write: 写本机服务失败",
			zap.String("streamId", request.GetStreamId()), zap.Error(err))
		st.shutdown(closeReason{token: "upstream_error", message: err.Error()})
		return nil, &rpcerror.Error{Code: rpcerror.CodeInternal, Message: "port forward: cannot write to the local service"}
	}
	return &agentrewire.Empty{}, nil
}

// Close 收尾一条流:关掉到本机服务的连接。收尾通知由这条流自己的 goroutine 发出 ——
// 这个方法不等它,否则一个写不动的对端会把调用方一起顶住。
func (s *Streams) Close(_ context.Context, request *agentrewire.PortForwardCloseRequest) (*agentrewire.Empty, error) {
	st := s.lookup(request.GetStreamId())
	if st == nil {
		return nil, ErrStreamNotFound
	}
	st.shutdown(closeReason{token: "closed", message: "port forward: closed by the host"})
	return &agentrewire.Empty{}, nil
}

// Ack 收下累计已消费字节数,并唤醒可能正等着信用的生产者。它必须是一个**永远很快**的
// 方法:它正是消费者用来解开背压的那一格。
func (s *Streams) Ack(_ context.Context, request *agentrewire.PortForwardAckRequest) (*agentrewire.Empty, error) {
	st := s.lookup(request.GetStreamId())
	if st == nil {
		return nil, ErrStreamNotFound
	}
	consumed := request.GetConsumedBytes()
	if consumed > math.MaxInt64 {
		consumed = math.MaxInt64
	}
	st.ack(int64(consumed))
	return &agentrewire.Empty{}, nil
}

// revokePort 关掉这条连接上转到某个端口的全部流:声明被停用或删除时,闸门经撤销面
// 调到这里。
//
// **摘表与关流在同一次调用里做完**,这是「立即」在实现上的全部内容:关流会让生产者
// goroutine 醒过来自己 forget,但那要经过一次调度,而宿主在那条窗口里拿着旧流号发来的
// write 会得到一次成功的应答 —— 一条已经不被允许的转发因此还能再写进去一块。
//
// 关流本身只是几行赋值加一次 socket.Close,收尾通知由流自己的 goroutine 发出,所以这
// 条通路上没有任何一步会把调用方(声明族那次 RPC)顶住。
func (s *Streams) revokePort(port int, reason closeReason) {
	s.mu.Lock()
	var doomed []*stream
	for id, st := range s.live {
		if st.port == port {
			doomed = append(doomed, st)
			delete(s.live, id)
		}
	}
	s.mu.Unlock()
	for _, st := range doomed {
		st.shutdown(reason)
	}
	s.announceRevoked(port, reason)
}

// announceRevoked 告诉这条连接的对端:这个端口此刻起不再允许转发。
//
// **它与上面那几条 closed 是两件事,少了它规格不成立。** closed 只到达此刻正开着流的
// 那一方;而桌面端为一条映射绑的那条专属监听多半正闲着(用户开了标签页晾在那儿),
// 一条流都没有,却正是规格「断开与失败」里要「一并关掉」的东西。所以撤销要在声明这
// 一层也说一句 —— 发给每一条连接,不论它有没有流。
//
// **在自己的 goroutine 里发。** 调用方是声明族那一次 RPC,而它多半来自**另一条**连
// 接(别的客户端改的声明);Notify 要拿这条连接的写锁,同步发就等于让一个写不动的对
// 端把别人那次 SetEnabled / Delete 顶住 —— 与流的收尾通知不进调用方栈是同一条理由。
//
// 代价是它与那几条 closed 之间没有确定的先后。宿主两条都消费得起:closed 收尾的是
// 那一条流,revoked 关的是整条监听,谁先到都不会漏掉其中一件。
func (s *Streams) announceRevoked(port int, reason closeReason) {
	if s.notify == nil {
		return
	}
	// 端口恒在 1..65535(声明落库时就夹住了),转 uint32 无损。
	revoked := &agentrewire.PortForwardRevokedNotification{Port: uint32(port), Reason: reason.token}
	go func() {
		_ = s.notify(&agentrewire.RpcNotification{
			Payload: &agentrewire.RpcNotification_PortForwardRevoked{PortForwardRevoked: revoked},
		})
	}()
}

// CloseAll 是连接断开时的收尾:每一条流都关掉,每一条都留下一条收尾通知(发得出去与
// 否是另一回事——对端可能正是那个走掉的人)。此后不再受理新的 open。
func (s *Streams) CloseAll() {
	if s.unwatch != nil {
		s.unwatch()
	}
	s.mu.Lock()
	s.shut = true
	live := make([]*stream, 0, len(s.live))
	for _, st := range s.live {
		live = append(live, st)
	}
	s.mu.Unlock()
	for _, st := range live {
		st.shutdown(closeReason{token: "connection_closed", message: "port forward: carrying connection closed"})
	}
}

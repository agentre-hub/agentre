// Package portforwardhost 是端口转发协议**宿主那一侧**的消费者:拿一条到设备的
// protorpc 连接,把一个 http.Request 变成设备上的一条转发流 —— 发 open、消费三条
// 通知(响应头 / 数据 / 收尾)、回累计 ack、101 之后把连接交出去。
//
// # 它为什么住在 pkg/wire 里
//
// 这个 module 名义上承载的是 wire 契约本身(今天已有 wirecall / rpcerror / wirelimits /
// protorpc / guard 这些手写包),而这个包不是契约,是契约的消费者。它仍然住在这里,
// 因为**它是 Go 侧唯一被批准的跨仓共享通道**:AGENTS.md 的硬不变量是 agentre-server
// 不得 import github.com/agentre-hub/agentre,唯一的例外就是这个嵌套 module。
//
// 而两个宿主要做的事完全同构 —— 桌面端为一条映射绑一条 127.0.0.1 的专属监听,控制台
// 在 /fw/<设备>/<端口>/* 上开一条路由,底下都是同一段:同一条 open、同一组通知、同一
// 套 ack 节奏。抄一份的代价是实打实的:「Expect: 100-continue 的上传丢掉上游应答」与
// 「ack 被回绝导致大响应静默截断」这两条缺陷都住在这一段里,复制一份就是把两个坑一起
// 复制,或者让两边慢慢漂开。
//
// 宿主自己的东西不在这里:监听/路由的生命周期、租约、鉴权、前缀怎么剥,都留在各自
// 的宿主里,这个包只认「一条连接 + 一个端口」。
package portforwardhost

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
	"github.com/agentre-hub/agentre/pkg/wire/wirelimits"
)

// Proxy 把宿主收到的一个 HTTP 请求变成设备那一侧的一条转发流。
//
// 四条纪律决定了这个文件的形状:
//
//  1. **订阅回调只入队。** 通知是在承载连接的读循环里同步派发的,回调里等一次 RPC
//     往返会把整条连接顶死 —— 同一条连接上还跑着会话、终端、文件系统。所以回调只
//     把字节塞进这条流自己的队列,写给浏览器、回 ack 都在处理这次请求的那个 goroutine 里。
//  2. **ack 回的是累计已消费字节数。** 设备侧按「已发 - 已确认」判窗口,回增量会让它
//     以为窗口一直没被消费,在 4 MiB 处停读,大文件下载就卡死在那里。
//  3. **101 之后不再解释内容。** upgraded 位一到就把连接交出去,两个方向都只搬字节;
//     宿主没有立场解释升级之后的内容。
//  4. **只有「没有下文了」才收场,流控失败不算。** 已经收进队列的字节是欠浏览器的债:
//     ack 被回绝、窗口对不上都不构成把它们丢掉的理由。收场的出口只有三个 —— 设备说
//     这条流结束了(closed)、浏览器自己走了、承载连接没了。
type Proxy struct {
	conn   *protorpc.Conn
	port   uint32
	prefix string

	// onRevoked 是「设备说这个端口不再允许转发了」这件事的出口。分层在这里:判定与
	// 索引都在**宿主**那一层(桌面端是 Listeners,它才知道这条端口属于哪条映射、哪台
	// 设备),而这一层只认得线上那条通知与自己转的那个端口。
	onRevoked func(reason string)

	// renderFailure 是宿主自己的失败呈现;nil 就用包内默认的纯文本(见 failure.go)。
	renderFailure FailureRenderer

	seq atomic.Uint64

	mu      sync.Mutex
	streams map[string]*hostStream
	shut    bool

	unsubscribe func()
}

// windowBytes 是响应方向的信用窗口。与线上默认值取同一个常量:open 里传 0 用的就是
// 它,而 ack 的节奏必须按同一个数算,否则设备侧会在窗口处停读。
const windowBytes = wirelimits.PortForwardWindowBytes

// uploadChunkBytes 是请求体上行时一次送多少。上行没有单独的信用窗口,一块的凭据
// 就是这次调用的应答,所以块大小同时也是这条方向的在途上限。
const uploadChunkBytes = 32 << 10

// closeCallTimeout 是收尾那一次 close 的上界。收尾不该拖住处理这次请求的 goroutine:
// 连接此刻多半已经在断了。
const closeCallTimeout = 5 * time.Second

func NewProxy(conn *protorpc.Conn, port uint32, onRevoked func(reason string), opts ...Option) *Proxy {
	proxy := &Proxy{
		conn:      conn,
		port:      port,
		onRevoked: onRevoked,
		prefix:    randomPrefix(),
		streams:   make(map[string]*hostStream),
	}
	for _, opt := range opts {
		opt(proxy)
	}
	proxy.unsubscribe = conn.Registry().SubscribeNotification(proxy.onNotification)
	return proxy
}

// randomPrefix 让流号在**这条连接内**唯一。同一台设备上的两条映射拿到的是连接池里
// 同一条连接(池按设备号复用),所以光有一个自增计数器不够 —— 两条监听各数各的,
// 迟早撞出同一个号,而撞号在设备侧是「已有一条同号的流」,两个下载会互相截断。
func randomPrefix() string {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		// crypto/rand 在支持的平台上不会失败;真失败了退回时间戳也仍然是个能用的名字。
		return "fw" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "fw" + hex.EncodeToString(raw)
}

func (p *Proxy) nextStreamID() string {
	return p.prefix + "-" + strconv.FormatUint(p.seq.Add(1), 10)
}

// onNotification 在承载连接的读循环里同步跑。**只入队**,见纪律 1。
func (p *Proxy) onNotification(_ context.Context, notification *agentrewire.RpcNotification) error {
	switch payload := notification.GetPayload().(type) {
	case *agentrewire.RpcNotification_PortForwardResponse:
		if stream := p.lookup(payload.PortForwardResponse.GetStreamId()); stream != nil {
			stream.putHead(payload.PortForwardResponse)
		}
	case *agentrewire.RpcNotification_PortForwardData:
		if stream := p.lookup(payload.PortForwardData.GetStreamId()); stream != nil {
			stream.putData(payload.PortForwardData.GetData())
		}
	case *agentrewire.RpcNotification_PortForwardClosed:
		if stream := p.lookup(payload.PortForwardClosed.GetStreamId()); stream != nil {
			stream.finish(payload.PortForwardClosed)
		}
	case *agentrewire.RpcNotification_PortForwardRevoked:
		// 撤销按端口认人:设备侧的撤销面认的就是端口,而这条监听手上也只有端口。
		// **不是**这个端口就不动 —— 同一条连接上还挂着这台设备别的映射的监听
		// (连接池按设备号复用同一条连接),照单全收会把用户别的标签页一起白掉。
		if payload.PortForwardRevoked.GetPort() == p.port {
			p.revoked(payload.PortForwardRevoked.GetReason())
		}
	}
	return nil
}

// revoked 把撤销交给上一层,**在自己的 goroutine 里**。
//
// 生产上的出口是关掉这条监听,而那件事会一路关到这个 proxy 自己(退订、把租约还给
// 连接池、Close 掉 http.Server)。这里是连接的读循环,同步做就等于让这条连接在关自己
// 的过程中不再读 —— 同一条连接上还跑着会话、终端、文件系统。纪律 1 的同一条理由。
func (p *Proxy) revoked(reason string) {
	if p.onRevoked == nil {
		return
	}
	go p.onRevoked(reason)
}

func (p *Proxy) lookup(streamID string) *hostStream {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.streams[streamID]
}

func (p *Proxy) register(streamID string) *hostStream {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.shut {
		return nil
	}
	stream := newHostStream()
	p.streams[streamID] = stream
	return stream
}

func (p *Proxy) forget(streamID string) {
	p.mu.Lock()
	delete(p.streams, streamID)
	p.mu.Unlock()
}

// Close 退订并把还开着的流一律以「宿主没了」收场。宿主收掉这条转发时调(桌面端是
// 关掉那条专属监听)—— 那时候还挂在上面的浏览器连接会被 http.Server.Close 掐断,但
// 正卡在等通知的那些 goroutine 得自己被叫醒。
func (p *Proxy) Close() {
	p.mu.Lock()
	if p.shut {
		p.mu.Unlock()
		return
	}
	p.shut = true
	live := make([]*hostStream, 0, len(p.streams))
	for _, stream := range p.streams {
		live = append(live, stream)
	}
	p.streams = make(map[string]*hostStream)
	unsubscribe := p.unsubscribe
	p.mu.Unlock()

	if unsubscribe != nil {
		unsubscribe()
	}
	for _, stream := range live {
		stream.finish(&agentrewire.PortForwardClosedNotification{Reason: reasonHostGone})
	}
}

// ---------- 一条流的邮箱 ----------

// hostStream 是一条转发流在宿主这一侧的邮箱。读循环往里塞,处理请求的 goroutine 取。
//
// 队列没有上限也不丢:丢一块就是文件损坏或页面白屏。真正的上限是设备那一侧的信用
// 窗口 —— 未被 ack 的量顶到窗口它就停读,压力经内核的 TCP 窗口原路还给被转发的服务。
type hostStream struct {
	head chan *agentrewire.PortForwardResponseNotification

	mu    sync.Mutex
	queue [][]byte
	end   *agentrewire.PortForwardClosedNotification

	signal chan struct{}
	done   chan struct{}
	once   sync.Once
}

func newHostStream() *hostStream {
	return &hostStream{
		head:   make(chan *agentrewire.PortForwardResponseNotification, 1),
		signal: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
}

func (s *hostStream) wake() {
	select {
	case s.signal <- struct{}{}:
	default:
	}
}

func (s *hostStream) putHead(head *agentrewire.PortForwardResponseNotification) {
	select {
	case s.head <- head:
	default: // 恰好一条,重复的丢掉
	}
}

func (s *hostStream) putData(payload []byte) {
	s.mu.Lock()
	s.queue = append(s.queue, payload)
	s.mu.Unlock()
	s.wake()
}

func (s *hostStream) finish(end *agentrewire.PortForwardClosedNotification) {
	s.mu.Lock()
	if s.end == nil {
		s.end = end
	}
	s.mu.Unlock()
	s.once.Do(func() { close(s.done) })
	s.wake()
}

// drain 一次取走已经排上的全部块,并报告这条流是不是已经收尾。
func (s *hostStream) drain() (chunks [][]byte, ended bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chunks, s.queue = s.queue, nil
	return chunks, s.end != nil
}

func (s *hostStream) closedInfo() *agentrewire.PortForwardClosedNotification {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.end
}

func (s *hostStream) finished() bool { return s.closedInfo() != nil }

// ---------- 一次请求 ----------

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	streamID := p.nextStreamID()
	stream := p.register(streamID)
	if stream == nil {
		p.fail(w, r, FailureDeviceUnreachable)
		return
	}
	defer p.forget(streamID)

	// 升级之后 r.Context() 会在 ServeHTTP 返回时被取消,而那正是两个方向还要继续搬
	// 字节的时候,所以这条流挂在自己的 ctx 上。
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()

	// ContentLength == -1 是「有请求体但长度未知」(浏览器发的 chunked),0 才是没有。
	// 这一位必须显式:「还没送到」与「没有请求体」在设备侧是两件事。
	hasBody := r.ContentLength != 0

	if _, err := wirecall.PortForwardOpen(r.Context(), wirecall.On(p.conn), &agentrewire.PortForwardOpenRequest{
		StreamId: streamID,
		Port:     p.port,
		Method:   r.Method,
		// 路径原样送(含查询串),这一层不剥任何前缀。有前缀的宿主(控制台的
		// /fw/<设备>/<端口>)在请求进到这个 Handler 之前就该剥掉 —— 多剥一层会把
		// /assets/x.js 变成 /x.js。
		Path:    r.URL.RequestURI(),
		Headers: toWireHeaders(r.Header),
		HasBody: hasBody,
		// 0 = 用 wirelimits.PortForwardWindowBytes 的默认窗口。
		WindowBytes: 0,
	}); err != nil {
		// 浏览器先走了不是这一层的失败:这次 open 是被 r.Context() 的取消打断的,而
		// openFailureKind 判不出来的一律算「够不着」——那会让宿主把一台正在正常处理
		// 这次 open 的设备记成离线。waitHead 对同一件事的判法就是不当失败(交零值),
		// 这里跟着它走。
		if r.Context().Err() != nil {
			return
		}
		p.fail(w, r, openFailureKind(err))
		return
	}

	// 这条流在设备那边开着了。除非它自己回过 closed,收场时都要补一条 close ——
	// 否则设备侧到本机服务的那条连接就悬在那里。
	defer func() {
		if !stream.finished() {
			p.closeStream(streamID)
		}
	}()

	if hasBody {
		// **上行读完不收这条流。** 请求体的 EOF 只说明「上行结束」,响应恰恰是在这之后
		// 才回来的(上游服务照例先把请求体读完再答复)。上行一到头就 cancel,等于在
		// 响应头到达之前把 waitHead 与下行泵一起掐掉:带请求体的 POST / PUT 会拿到一个
		// 空的 200。浏览器中途走掉那一条另有出口 —— waitHead 与下面那个看守都盯着
		// r.Context()。
		go p.pumpUpload(ctx, streamID, r.Body)
	}

	head, kind := p.waitHead(ctx, r, stream)
	switch {
	case head == nil && kind != noFailure:
		p.fail(w, r, kind)
		return
	case head == nil:
		return // 浏览器先走了:没有人还在等一个答复
	}

	if head.GetUpgraded() {
		p.serveUpgraded(ctx, w, r, streamID, stream, head, cancel)
		return
	}

	// 浏览器断开(关标签 / 取消下载)要能叫醒下行泵 —— 它此刻正卡在等下一块通知上,
	// 而这条流的 ctx 刻意脱开了 r.Context()(升级之后那条路要在 ServeHTTP 返回之后
	// 继续搬字节)。没有这一条,取消下载在设备那一侧就是一条永远悬着的本机连接。
	go func() {
		select {
		case <-r.Context().Done():
			cancel()
		case <-ctx.Done():
		}
	}()

	header := w.Header()
	for name, values := range head.GetHeaders() {
		for _, value := range values.GetValues() {
			header.Add(name, value)
		}
	}
	controller := http.NewResponseController(w)
	w.WriteHeader(normalizeStatus(head.GetStatus()))
	_ = controller.Flush()
	p.pumpDownload(ctx, streamID, stream, w, func() { _ = controller.Flush() })
}

// waitHead 交出响应头,或者「没有响应头,而是这一种失败」。
//
// 第二个返回值是 noFailure 时表示「没有人还在等一个答复」(浏览器先走了),不是一种
// 失败 —— FailureKind 的零值刻意不是任何一种,正是为了让这个区分不需要第三个返回值。
func (p *Proxy) waitHead(
	ctx context.Context, r *http.Request, stream *hostStream,
) (head *agentrewire.PortForwardResponseNotification, kind FailureKind) {
	select {
	case head := <-stream.head:
		return head, noFailure
	case <-stream.done:
		// 「先来了头再收尾」的竞态:头是先于任何正文到的,再取一次。
		select {
		case head := <-stream.head:
			return head, noFailure
		default:
		}
		return nil, closedFailureKind(stream.closedInfo())
	case <-p.conn.Done():
		return nil, FailureDeviceUnreachable
	case <-r.Context().Done():
		return nil, noFailure
	case <-ctx.Done():
		return nil, noFailure
	}
}

// pumpDownload 把设备回来的字节写给浏览器,并按约每消费四分之一个窗口回一次 ack。
func (p *Proxy) pumpDownload(
	ctx context.Context, streamID string, stream *hostStream, dst io.Writer, flush func(),
) {
	const ackEvery = uint64(windowBytes) / 4
	var consumed, acked uint64
	// acking 一旦落下就不再抬起来:设备已经把这条流收掉了,再回 ack 只会又得一个
	// -32073。它管的只是「还发不发 ack」,收不收场另有出口(见下面那个 return)。
	acking := true
	for {
		chunks, ended := stream.drain()
		for _, chunk := range chunks {
			if _, err := dst.Write(chunk); err != nil {
				return
			}
			consumed += uint64(len(chunk))
		}
		if len(chunks) > 0 {
			flush()
			if acking && consumed-acked >= ackEvery {
				acked = consumed
				// 累计量,不是增量(纪律 2)。这次调用等的是设备的应答 —— 它跑在
				// 处理请求的 goroutine 上,不在读循环上,所以等得起。
				if _, err := wirecall.PortForwardAck(ctx, wirecall.On(p.conn),
					&agentrewire.PortForwardAckRequest{StreamId: streamID, ConsumedBytes: consumed}); err != nil {
					// **ack 失败不收场**(纪律 4)。ack 是流控,不是数据通路:这一刻
					// 手上还压着的字节该不该写给浏览器,与设备收没收到这条 ack 无关。
					//
					// 而且被回绝恰恰是正常收尾的必经之路:设备侧先把流从表里摘掉、
					// 再发 closed(daemon/portforward 的 pump,那个次序是对的),
					// 所以流一结束,还飞在路上的 ack 一律得 -32073;真链路上收尾通知
					// 又排在好几兆已发未达的数据帧后面,宿主此刻手上必然还压着一大段。
					// 就地 return 就是把那一段丢掉 —— 状态码 200、Content-Length 也对、
					// 正文却短了几百 KB(curl 报 exit 18 / CURLE_PARTIAL_FILE)。
					//
					// 别的错都还按老样子收场:那是连接这一层没了,剩下的字节本来也来不了。
					if !streamAlreadyEnded(err) {
						return
					}
					acking = false
				}
			}
		}
		if ended {
			return
		}
		select {
		case <-stream.signal:
		case <-stream.done:
		case <-ctx.Done():
			return
		case <-p.conn.Done():
			return
		}
	}
}

// pumpUpload 把请求体一块一块送上去。**上一块的应答回来之前不发下一块** —— 这个方向
// 没有单独的信用,这次调用的应答就是这一块的凭据。
// 它不收流:读到头之后这条流该不该收场,由**调用方**决定 —— 请求体读完只是上行结束
// (响应还在后头),而升级之后浏览器那一侧读到头就是「这条连接没了」。
func (p *Proxy) pumpUpload(ctx context.Context, streamID string, body io.Reader) {
	buf := make([]byte, uploadChunkBytes)
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			if _, werr := wirecall.PortForwardWrite(ctx, wirecall.On(p.conn), &agentrewire.PortForwardWriteRequest{
				StreamId: streamID, Data: bytes.Clone(buf[:n]),
			}); werr != nil {
				return
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				// eof 半关闭上游的写方向:请求体到此为止,响应可以继续流回来。
				_, _ = wirecall.PortForwardWrite(ctx, wirecall.On(p.conn),
					&agentrewire.PortForwardWriteRequest{StreamId: streamID, Eof: true})
			}
			return
		}
	}
}

// serveUpgraded 把连接交出去,此后两个方向都只搬字节(纪律 3)。
func (p *Proxy) serveUpgraded(
	ctx context.Context, w http.ResponseWriter, r *http.Request, streamID string,
	stream *hostStream, head *agentrewire.PortForwardResponseNotification, cancel func(),
) {
	conn, buffered, err := http.NewResponseController(w).Hijack()
	if err != nil {
		p.fail(w, r, FailureUpgradeUnavailable)
		return
	}
	defer func() { _ = conn.Close() }()

	status := normalizeStatus(head.GetStatus())
	var headLine bytes.Buffer
	fmt.Fprintf(&headLine, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
	for name, values := range head.GetHeaders() {
		for _, value := range values.GetValues() {
			fmt.Fprintf(&headLine, "%s: %s\r\n", name, value)
		}
	}
	headLine.WriteString("\r\n")
	if _, werr := conn.Write(headLine.Bytes()); werr != nil {
		return
	}

	// 读要走 buffered.Reader 而不是 conn:服务器可能已经从 socket 上多读了一些字节
	// 到缓冲里,直接读 conn 会把它们丢掉。
	// 交出去之后,上行读到头就是「浏览器那一侧走了」,该把整条流收掉 —— 与带请求体的
	// 普通请求相反,那一条读到头只是上行结束。
	go func() {
		defer cancel()
		p.pumpUpload(ctx, streamID, buffered.Reader)
	}()
	p.pumpDownload(ctx, streamID, stream, conn, func() {})
}

// closeStream 给设备补一条 close。那条流仍会回一条 closed,之后再指向它的调用得
// -32073 —— 属正常,不当错误报。
func (p *Proxy) closeStream(streamID string) {
	ctx, cancel := context.WithTimeout(context.Background(), closeCallTimeout)
	defer cancel()
	_, _ = wirecall.PortForwardClose(ctx, wirecall.On(p.conn),
		&agentrewire.PortForwardCloseRequest{StreamId: streamID})
}

// ---------- 失败怎么说 ----------

// 收尾原因的 token。按 token 分支,不按 message 的文本 —— 文案一改就静默失灵。
const (
	reasonUpstreamError = "upstream_error"
	reasonHostGone      = "host_gone"
	reasonConnClosed    = "connection_closed"
)

// openFailureKind 把设备回绝 open 的那个错误判成一种失败。判据是 rpcerror 的码,
// 不是它带回来的英文串。判不出来的一律算「够不着」——那是最不会误导人的一种。
func openFailureKind(err error) FailureKind {
	var rpcErr *protorpc.Error
	if errors.As(err, &rpcErr) {
		switch rpcErr.Code {
		case rpcerror.CodePortForwardNotDeclared:
			return FailureNotDeclared
		case rpcerror.CodePortForwardDisabled:
			return FailureDisabled
		case rpcerror.CodePortForwardNoListener:
			return FailureNoListener
		}
	}
	return FailureDeviceUnreachable
}

// streamAlreadyEnded 认出「这条流在设备那边已经不在了」。
//
// 设备侧摘表早于发 closed(daemon/portforward 的 pump 就是这个次序,且**必须**是这个
// 次序:宿主收到 closed 之后再拿这个流号来调什么,该得的就是 StreamNotFound)。所以
// 收到它不等于出了错,只等于「这条流的账已经结了」—— 而每一条被摘掉的流都保证还会
// 留下一条 closed(pump / revokePort / CloseAll 三条路都经 shutdown),收场不缺出口。
func streamAlreadyEnded(err error) bool {
	var rpcErr *protorpc.Error
	return errors.As(err, &rpcErr) && rpcErr.Code == rpcerror.CodePortForwardStreamNotFound
}

// closedFailureKind 把一条 closed 通知的 reason token 判成一种失败。按 token 分支,
// 不按 message 的文本 —— 文案一改就静默失灵。
func closedFailureKind(end *agentrewire.PortForwardClosedNotification) FailureKind {
	switch end.GetReason() {
	case reasonUpstreamError:
		return FailureUpstreamGone
	case reasonHostGone, reasonConnClosed:
		return FailureDeviceUnreachable
	}
	return FailureForwardIncomplete
}

// normalizeStatus 挡住线上来的一个不能当状态码用的数:WriteHeader 会 panic,而这条
// 转发正跑在桌面进程里。
func normalizeStatus(status int32) int {
	if status < 100 || status > 599 {
		return http.StatusBadGateway
	}
	return int(status)
}

func toWireHeaders(header http.Header) map[string]*agentrewire.HeaderValues {
	out := make(map[string]*agentrewire.HeaderValues, len(header))
	for name, values := range header {
		out[name] = &agentrewire.HeaderValues{Values: append([]string(nil), values...)}
	}
	return out
}

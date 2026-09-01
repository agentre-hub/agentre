package server_svc

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/daemon/relaytransport"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 本文件是决策 13/10 在桌面端这一侧的落地:桌面端作为中继**客户端**(观察/驱动别的
// 机器)只保留一条常驻的多路复用连接 —— 不管此刻在看零台还是 N 台机器的对话,物理
// socket 数恒为 1。每台想接的机器/对话开一条虚拟通道,目标声明为通道的第一帧载荷
// (conversation:<uuid> 或 machine:<fingerprint>,决策 10/11),不再出现在 URL 里。
// 账号信号(sync_version 等)经由这条连接上的保留通道抵达(relaytransport.SignalChannelID
// = agentre-server relay_svc.SignalChannelID 的逐字同值,决策 14),取代了已删除的
// /v1/account/channel 与本文件从前的 accountchannel.go 实现。
//
// 它与中继的另一条连接(server_svc/relay.go 用不到、internal/peer.Inbound 用的
// NewInboundHubLink)彼此独立:那条是桌面端**自己**作为可寻址目标被别人连(登记 +
// Multiplexer.Accept() 接 RPC),这条是桌面端作为客户端去连别人。两条连接各自的
// HubLink + Multiplexer 不共享。

// relayClientEndpoint 是决策 10 之后的中继客户端入口:URL 上没有 daemon_fingerprint,
// 每条虚拟通道自己声明目标。
const relayClientEndpoint = "/v1/relay/client"

// 与 agentre-server relay_svc.ChannelCode* 逐字同值(见该仓库
// internal/service/relay_svc/target.go)。两个仓库各自维护一份常量而不是共享一个
// 包,原因与 relaytransport.SignalChannelID 同一处注释:Go 后端之间不允许反向
// import。
const (
	channelCodeTargetNotFound    int32 = -32010
	channelCodeTargetOffline     int32 = -32011
	channelCodeForwardFailed     int32 = -32012
	channelCodeTargetInvalid     int32 = -32013
	channelCodeSignalUnavailable int32 = -32014
	channelCodeTargetForbidden   int32 = -32016
)

// ErrRelayTargetInvalid / ErrRelayTargetForbidden / ErrRelaySignalUnavailable
// 是通道级失败(决策 10 把中继的失败粒度从连接降到通道)在桌面端的表达。
// client.ErrRelayDaemonNotFound / ErrRelayDaemonOffline / ErrRelayForwardFailed
// 是既有三个 —— 旧的按连接级 HTTP 状态码分类的时代就有,含义不变,只是现在从通道级
// 错误帧翻译过来,而不是从 WebSocket 握手的 4xx/5xx 翻译过来。
var (
	ErrRelayTargetInvalid     = errors.New("relay: channel target is malformed")
	ErrRelayTargetForbidden   = errors.New("relay: this account may not address that target")
	ErrRelaySignalUnavailable = errors.New("relay: account signal channel is unavailable")
)

// accountChannelBuffer 是每个 DialAccountChannel 订阅者的信号流缓冲——信号只带
// 版本号、彼此可合并,只需要吸收「引擎正在 Pull 时又来了几条」。与旧
// accountchannel.go 同一个常量值,仅换了个家。
const accountChannelBuffer = 16

// residentRelay 是这台桌面端唯一一条中继客户端连接。它在创建后立刻常驻:
// HubLink.Run 的重连循环、Multiplexer 的 Accept() 消费循环,都绑定在与登录状态
// 等长的 ctx 上,不随某一条虚拟通道的借用/归还而起停 —— 借用一台机器只多开/关一条
// 虚拟通道,绝不触碰这条物理连接。
//
// 「常驻」的条件是**登录态**而不是进程活着(规格「常驻与空闲宽限的冲突要裁决」):
// 登出由 Logout 调 dropRelay 把它整条收掉,下一次登录时 ensureRelay 重新懒建。
// 换账号必然经过登出(StartLogin 对已登录的桌面端直接拒绝,见 login.go),所以
// 「换了服务端而旧 socket 还挂在旧账号上」那条路由构造不存在。
type residentRelay struct {
	link *relaytransport.HubLink
	mux  *relaytransport.Multiplexer
	// stop 收掉这条常驻连接。常驻的条件是**登录态**（规格「常驻与空闲宽限的冲突
	// 要裁决」），不是进程活着：登出之后凭据提供者交回空串，再拨下去就是一串注定
	// 401 的、不带身份的请求。见 service.dropRelay。
	stop context.CancelFunc

	subMu sync.Mutex
	subs  map[chan syncwire.AccountChannelFrame]struct{}

	// connMu / connectedCh 让 openTarget 等到物理连接真的建起来再开通道:
	// newResidentRelay 立刻返回(HubLink.Run 在后台异步重拨),而旧实现
	// (client.DialRelayProtobuf)是同步拨号+握手一次做完——dialRelay 的调用方
	// (ConnPool.Borrow 等)一直按「返回即代表已经连上或明确失败」的契约在用。
	// 不等的话,第一次 Borrow 几乎总会撞上「mux.Open() 立刻成功、但物理链路还没
	// 建好」那个窗口,拿到一个当场返回 ErrHubUnavailable 的假失败。
	//
	// connectedCh 在每次连上时关闭、每次断开时换成一个新的未关闭 channel——与
	// relaytransport.Multiplexer 断线后「retired 全部清零、旧记号一条都不作数」
	// 同一个「换代」写法。
	connMu      sync.Mutex
	connectedCh chan struct{}
}

// newResidentRelay 构造并立刻启动一条常驻中继客户端连接。ctx 决定它的寿命——
// ensureRelay 交进来的是一个只由登出(dropRelay)取消的 ctx:这条连接与**登录态**
// 等长,不与某一次调用方的请求 ctx 绑定。
func newResidentRelay(ctx context.Context, hubOpts relaytransport.HubLinkOptions) *residentRelay {
	hubOpts.Endpoint = relayClientEndpoint
	link := relaytransport.NewHubLink(hubOpts)
	mux := relaytransport.NewMultiplexer(link)
	r := &residentRelay{
		link: link, mux: mux,
		subs:        map[chan syncwire.AccountChannelFrame]struct{}{},
		connectedCh: make(chan struct{}),
	}
	// 断线之后是全新的一批订阅者:旧物理连接上还没送达的信号本来就无害地丢弃
	// (accountchan_svc 包注释:漏帧/乱序/重复都无害),调用方据此重新 Dial 一次并
	// 主动 Pull,补齐断线期间的变更——与旧 accountchannel.go 的约定完全一致。
	link.AddLifecycleListener(
		func() {
			r.connMu.Lock()
			close(r.connectedCh)
			r.connMu.Unlock()
		},
		func(error) {
			r.closeAllSubs()
			r.connMu.Lock()
			r.connectedCh = make(chan struct{})
			r.connMu.Unlock()
		},
	)
	go func() { _ = link.Run(ctx) }()
	go r.serveAccepted(ctx)
	return r
}

// close 收掉这条常驻连接:停掉重连循环与 mux,再把订阅者一并关掉,让
// sync_svc 那条读循环退出(它按流关闭理解为「这一路没了」)。
func (r *residentRelay) close() {
	if r.stop != nil {
		r.stop()
	}
	r.mux.Close()
	r.closeAllSubs()
}

// waitConnected 阻塞到这条常驻连接真的建起来,或 ctx 先结束。
func (r *residentRelay) waitConnected(ctx context.Context) error {
	r.connMu.Lock()
	ch := r.connectedCh
	r.connMu.Unlock()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", relaytransport.ErrHubUnavailable, ctx.Err())
	}
}

// serveAccepted 消费这条客户端链路上**服务端主动开的**通道。决策 13/14:这条链路
// 上只有一种服务端会开的通道——保留通道 SignalChannelID,承载账号信号。任何别的
// 号是协议漂移(服务端不该在客户端链路上开别的通道,这条链路上的普通通道永远是
// 本端自己 Open() 出来的),关掉它而不是当 RPC 连接用。
func (r *residentRelay) serveAccepted(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case channel := <-r.mux.Accept():
			if channel == nil {
				return
			}
			if channel.ID() != relaytransport.SignalChannelID {
				_ = channel.Close()
				continue
			}
			go r.serveSignal(channel)
		}
	}
}

// serveSignal 把保留通道上的帧解码后广播给当前全部订阅者,直到通道关闭。未知帧被
// 忽略而不是当作协议错误:通道日后会承载别的信号种类,旧构建不该因此把这条它还
// 认得的通道判死。
//
// 通道走到头就把订阅者一起关掉。服务端订阅建不起来时**只**关这一条保留通道
// (relay_ctr.signalUnavailable 写一帧通道级错误 + 一帧空载荷),物理连接照常服务
// RPC —— 因此链路级的断线收尾在这种情况下不会跑,而规格要求客户端「只把信号那一路
// 标为不可用并退回 30 秒轮询」。不关订阅者的话 sync_svc.consumeAccountSignals 会
// 一直阻塞在一条再也不会有帧的流上:失败被无声吞掉,而不是被标出来。
//
// closeAllSubs 先换掉整张表再逐个 close,所以它与断线收尾各跑一次也不会重复关闭。
func (r *residentRelay) serveSignal(channel relaytransport.PayloadChannel) {
	defer r.closeAllSubs()
	for {
		payload, err := channel.ReadPayload()
		if err != nil {
			return
		}
		if len(payload) == 0 {
			return
		}
		frame, known, err := syncwire.DecodeAccountChannelFrame(payload)
		if err != nil || !known {
			continue
		}
		r.broadcast(frame)
	}
}

func (r *residentRelay) broadcast(frame syncwire.AccountChannelFrame) {
	r.subMu.Lock()
	defer r.subMu.Unlock()
	for sub := range r.subs {
		select {
		case sub <- frame:
		default:
			// 信号可合并、允许丢——见包注释与 accountchan_svc 的设计前提。
		}
	}
}

// subscribe 登记一个信号收件口。closeAllSubs(断线)会把它连同其它订阅者一起关掉;
// unsubscribe 在调用方自己收工(ctx.Done)时摘除,不影响其它订阅者与物理连接本身。
func (r *residentRelay) subscribe() (chan syncwire.AccountChannelFrame, func()) {
	sub := make(chan syncwire.AccountChannelFrame, accountChannelBuffer)
	r.subMu.Lock()
	r.subs[sub] = struct{}{}
	r.subMu.Unlock()
	return sub, func() {
		r.subMu.Lock()
		delete(r.subs, sub)
		r.subMu.Unlock()
	}
}

func (r *residentRelay) closeAllSubs() {
	r.subMu.Lock()
	subs := r.subs
	r.subs = map[chan syncwire.AccountChannelFrame]struct{}{}
	r.subMu.Unlock()
	for sub := range subs {
		close(sub)
	}
}

// DialAccountChannel 实现 sync_svc.AccountChannelDialer:同一个方法名与签名,从前
// 由 accountchannel.go 拨一条独立 WebSocket 实现,现在改为向这条常驻连接的保留
// 通道订阅一份信号流。返回的 channel 在 ctx 结束或物理连接断线时关闭,调用方据此
// 重连(sync_svc.watchAccountChannel 的既有重试循环不用改一行)。
func (s *service) DialAccountChannel(ctx context.Context) (<-chan syncwire.AccountChannelFrame, error) {
	relay, err := s.ensureRelay(ctx)
	if err != nil {
		return nil, err
	}
	sub, unsubscribe := relay.subscribe()
	out := make(chan syncwire.AccountChannelFrame, accountChannelBuffer)
	go func() {
		defer close(out)
		defer unsubscribe()
		for {
			select {
			case <-ctx.Done():
				return
			case frame, ok := <-sub:
				if !ok {
					return
				}
				select {
				case out <- frame:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// ensureRelay 懒创建并返回这台桌面端唯一的常驻中继客户端连接;未登录时不创建
// 任何东西(R12:未登录一个请求都不发)。多次调用只创建一次——这正是「N 台机器
// 同时观察,socket 数恒为 1」的落地点。
func (s *service) ensureRelay(ctx context.Context) (*residentRelay, error) {
	row, err := server_state_repo.ServerState().Get(ctx)
	if err != nil {
		return nil, err
	}
	if row == nil || !row.IsLoggedIn() {
		return nil, ErrNotLoggedIn
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.relay != nil {
		return s.relay, nil
	}
	relayCtx, stop := context.WithCancel(context.Background())
	relay := newResidentRelay(relayCtx, relaytransport.HubLinkOptions{
		ServerURLProvider:   s.relayBaseURL,
		AccessTokenProvider: s.AccessToken,
	})
	relay.stop = stop
	s.relay = relay
	return relay, nil
}

// dropRelay 收掉这台桌面端的常驻中继连接（如果有）。
//
// 登出时调用:常驻只在登录态成立。不收的话这条连接会挂在 context.Background() 上
// 一直活到进程退出,而登出之后 AccessTokenProvider 交回空串——每一次重拨都是一次
// 注定 401 的、不带身份的网络请求。下一次登录由 ensureRelay 重新懒建一条。
func (s *service) dropRelay() {
	s.mu.Lock()
	relay := s.relay
	s.relay = nil
	s.mu.Unlock()
	if relay != nil {
		relay.close()
	}
}

// relayBaseURL 在每次(重)拨号时重新取当前 baseURL——与 AccessToken() 同一模式,
// 让 token 刷新 / 换 server 在下一次物理重拨时生效,而不需要谁去主动踢这条连接。
func (s *service) relayBaseURL() string {
	c := s.getClient()
	if c == nil {
		return ""
	}
	return c.baseURL
}

// openTarget 在这条常驻连接上开一条新虚拟通道、声明目标、完成 auth.account 握手,
// 返回一条可供 protorpc 调用方法的连接。它是 RelayDialPort 的真实现的核心:
// remote_device_svc.ConnPool 借到的每一个 relay Lease 底下都是这样一条通道,
// Lease.Release() 之后的 Close() 只关这一条通道(见 relayChannelConn.Close),从不
// 触碰这条常驻物理连接——ConnPool 现有的 per-device 引用计数与 idle 收回模型不需要
// 改一行就能正确落在通道粒度上。
func (r *residentRelay) openTarget(ctx context.Context, target, credential string) (client.ProtobufConnection, error) {
	if err := r.waitConnected(ctx); err != nil {
		return nil, err
	}
	channel, err := r.mux.Open()
	if err != nil {
		return nil, err
	}
	if err := channel.WritePayload([]byte(target)); err != nil {
		_ = channel.Close()
		return nil, err
	}
	response, err := authAccountOverChannel(channel, credential)
	if err != nil {
		_ = channel.Close()
		return nil, err
	}
	conn := protorpc.NewConn(protorpc.NewPayloadFrameConn(channel), protorpc.NewRegistry())
	go conn.Serve(ctx)
	return &relayChannelConn{conn: conn, selfFP: response.GetPeerFingerprint()}, nil
}

// authAccountOverChannel 手工做一次 auth.account 握手,而不是把 channel 直接交给
// protorpc.Conn 再调 protorpc.CallMethod。原因:目标解析失败时(relay_ctr.fail)
// 服务端在这条虚拟通道上先写一帧 Id=0 的 RpcFrame_Error、再写一帧空载荷把通道关掉
// ——那不是任何一次 protorpc 调用的应答,Id=0 在 protorpc.Conn 的 pending 表里永远
// 找不到收件人,只会被 Debug 日志静默丢弃(见 protorpc.Conn.deliver),调用方能看到
// 的只剩通道被关闭之后的 ErrConnClosed,丢失了「到底是目标不存在/离线/转发失败」
// 这个决策 10 特意做成通道级可区分错误码的信息。这里先手工发送并读取这一帧,拿到
// 精确的 code 再翻译成既有的 client.ErrRelayDaemonNotFound 等哨兵,之后才把同一个
// channel 交给一个全新的 protorpc.Conn 承载后续的真实 RPC 流量——两者的请求 id
// 各自从 1 开始计数,互不干扰(这次握手用的 Id=1 从未注册进新 Conn 的 pending 表)。
func authAccountOverChannel(channel relaytransport.PayloadChannel, credential string) (*agentrewire.AuthAccountResponse, error) {
	request := &agentrewire.AuthAccountRequest{
		Credential:                  credential,
		ProtocolVersion:             wireversion.Protocol,
		MinSupportedProtocolVersion: wireversion.MinSupported,
	}
	payload, err := proto.Marshal(request)
	if err != nil {
		return nil, err
	}
	reqFrame, err := proto.Marshal(&agentrewire.RpcFrame{
		Id: 1,
		Body: &agentrewire.RpcFrame_Request{Request: &agentrewire.Request{
			MethodId: uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT), EncodedPayload: payload,
		}},
	})
	if err != nil {
		return nil, err
	}
	if err := channel.WritePayload(reqFrame); err != nil {
		return nil, err
	}
	for {
		raw, err := channel.ReadPayload()
		if err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			return nil, errors.New("server_svc: relay channel closed before answering auth.account")
		}
		var frame agentrewire.RpcFrame
		if err := proto.Unmarshal(raw, &frame); err != nil {
			continue
		}
		switch body := frame.Body.(type) {
		case *agentrewire.RpcFrame_Error:
			if frame.GetId() == 0 {
				return nil, translateChannelError(body.Error)
			}
			return nil, fmt.Errorf("server_svc: relay rejected account authentication: %s", body.Error.GetMessage())
		case *agentrewire.RpcFrame_Response:
			if frame.GetId() != 1 {
				continue
			}
			var response agentrewire.AuthAccountResponse
			if err := proto.Unmarshal(body.Response.GetEncodedPayload(), &response); err != nil {
				return nil, err
			}
			if !response.GetOk() {
				return nil, errors.New("server_svc: relay rejected account authentication")
			}
			return &response, nil
		default:
			continue
		}
	}
}

// translateChannelError 把决策 10 的通道级错误码翻回既有的 Go 哨兵——大多数调用方
// (chat_svc.terminalBorrowError 等)靠 errors.Is 分辨「重试也没用」,这条翻译层
// 让它们不用感知这一轮从连接级降到通道级的改动。
func translateChannelError(rpcErr *agentrewire.RpcError) error {
	switch rpcErr.GetCode() {
	case channelCodeTargetNotFound:
		return client.ErrRelayDaemonNotFound
	case channelCodeTargetOffline:
		return client.ErrRelayDaemonOffline
	case channelCodeForwardFailed:
		return client.ErrRelayForwardFailed
	case channelCodeTargetInvalid:
		return ErrRelayTargetInvalid
	case channelCodeTargetForbidden:
		return ErrRelayTargetForbidden
	case channelCodeSignalUnavailable:
		return ErrRelaySignalUnavailable
	default:
		return fmt.Errorf("server_svc: relay channel error %d: %s", rpcErr.GetCode(), rpcErr.GetMessage())
	}
}

// relayChannelConn 是一条虚拟通道上完成握手之后的 client.ProtobufConnection。
// Close 只关这一条通道(protorpc.Conn.Close → payloadFrameConn.Close →
// VirtualChannel.Close → Multiplexer.closeChannel),从不触碰这条常驻物理连接——
// 这正是 ConnPool 的 idle 收回可以直接复用、不需要改动的原因。
type relayChannelConn struct {
	conn   *protorpc.Conn
	selfFP string
}

func (c *relayChannelConn) Conn() *protorpc.Conn    { return c.conn }
func (c *relayChannelConn) Closed() <-chan struct{} { return c.conn.Done() }
func (c *relayChannelConn) Close() error            { return c.conn.Close() }
func (c *relayChannelConn) SelfFingerprint() string { return c.selfFP }

var _ client.ProtobufConnection = (*relayChannelConn)(nil)

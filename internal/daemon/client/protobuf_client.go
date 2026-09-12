package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/websocket"

	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

type Options struct {
	URL       string
	TLSConfig *tls.Config
}

// RelayOptions 是中继客户端拨号的入参。它**没有**本端指纹:auth.account 的对端身份
// 由响应方从 AccessToken 里验出来(决策 8),本端在这条连接上的身份从应答回写
// (见 ProtobufClient.SelfFingerprint)。
type RelayOptions struct {
	URL         string
	AccessToken string
	TLSConfig   *tls.Config
}

var (
	ErrRelayDaemonNotFound = errors.New("relay: daemon is not registered under this account")
	ErrRelayDaemonOffline  = errors.New("relay: daemon is registered but currently offline")
	ErrRelayForwardFailed  = errors.New("relay: daemon is online but the relay could not forward to it")

	// ErrPeerProtocolUnsupported means the peer does not speak the
	// agentre-protobuf WebSocket subprotocol at all — the WebSocket upgrade
	// itself was refused with 426.
	ErrPeerProtocolUnsupported = errors.New("protocol: peer does not speak the agentre-protobuf subprotocol")

	// ErrPeerProtocolVersionMismatch means the peer does speak the
	// subprotocol but reported a wire protocol version this build does not
	// accept.
	ErrPeerProtocolVersionMismatch = errors.New("protocol: peer speaks a different agentre wire protocol version")
)

func closeHandshakeBody(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
}

// classifyDialError names the one WebSocket handshake rejection that is about
// the protocol itself rather than about the network: 426 means the peer serves
// this endpoint but refuses the agentre-protobuf subprotocol, which is what an
// agentred too old to speak Protobuf looks like from here.
func classifyDialError(err error, resp *http.Response) error {
	if resp == nil || !errors.Is(err, websocket.ErrBadHandshake) {
		return err
	}
	if resp.StatusCode == http.StatusUpgradeRequired {
		return fmt.Errorf("%w: %w", ErrPeerProtocolUnsupported, err)
	}
	return err
}

func classifyRelayDialError(err error, resp *http.Response) error {
	if resp == nil || !errors.Is(err, websocket.ErrBadHandshake) {
		return err
	}
	switch resp.StatusCode {
	case http.StatusUpgradeRequired:
		return fmt.Errorf("%w: %w", ErrPeerProtocolUnsupported, err)
	case http.StatusNotFound:
		return fmt.Errorf("%w: %w", ErrRelayDaemonNotFound, err)
	case http.StatusConflict:
		return fmt.Errorf("%w: %w", ErrRelayDaemonOffline, err)
	case http.StatusBadGateway:
		return fmt.Errorf("%w: %w", ErrRelayForwardFailed, err)
	default:
		return fmt.Errorf("relay rejected the connection with %s: %w", resp.Status, err)
	}
}

// PeerProtocolVersionError renders the one rejection every handshake shares.
//
// The empty string is not a pass: proto3 gives an absent field the same zero
// value as an explicitly empty one, so an agentred that never filled the field
// in is named as a version mismatch — "could not reach agentred" would send the
// user hunting the network instead of running `make agentred-deploy`.
//
// 它是导出的,因为并非每一次握手都走本包:server_svc 的中继客户端在一条虚拟通道上
// 手工收发 auth.account(要拿到通道级错误码),它同样必须过这个窗口。窗口的判法只
// 该有一处定义 —— 复制一份就是让两条握手路各有各的「算不算兼容」。
func PeerProtocolVersionError(peerProtocol string) error {
	reason := wireversion.Reject(peerProtocol)
	if reason == "" {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrPeerProtocolVersionMismatch, reason)
}

// ClassifyHandshakeError folds the peer's own version rejection back into the
// local sentinel, so both directions of the same disagreement read the same to
// callers: the daemon refuses us with rpcerror.CodeProtocolVersion, we refuse
// the daemon by inspecting its response.
//
// 导出的理由同 PeerProtocolVersionError:中继客户端那条手工握手也要折同一个弯。
func ClassifyHandshakeError(err error) error {
	var rpcErr *rpcerror.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == rpcerror.CodeProtocolVersion {
		return fmt.Errorf("%w: %s (this build speaks wire protocol version %s)", ErrPeerProtocolVersionMismatch, rpcErr.Message, wireversion.Protocol)
	}
	return err
}

type ProtobufPath struct {
	Name        string
	Fingerprint string
	Dial        func(context.Context) (ProtobufConnection, error)
}

func RaceProtobuf(ctx context.Context, paths ...ProtobufPath) (ProtobufConnection, error) {
	if len(paths) == 0 {
		return nil, errors.New("client.RaceProtobuf: no paths")
	}
	for _, path := range paths[1:] {
		if path.Fingerprint != paths[0].Fingerprint {
			return nil, errors.New("client.RaceProtobuf: peer fingerprint mismatch")
		}
	}
	type outcome struct {
		idx  int
		conn ProtobufConnection
		err  error
	}
	results := make(chan outcome, len(paths))
	cancels := make([]context.CancelFunc, len(paths))
	for i, path := range paths {
		pathCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		cancels[i] = cancel
		go func(i int, path ProtobufPath) { conn, err := path.Dial(pathCtx); results <- outcome{i, conn, err} }(i, path)
	}
	cancelExcept := func(keep int) {
		for i, cancel := range cancels {
			if i != keep {
				cancel()
			}
		}
	}
	var winner ProtobufConnection
	var errs []error
	for range paths {
		result := <-results
		if result.conn == nil || result.err != nil {
			if result.conn != nil {
				_ = result.conn.Close()
			}
			err := result.err
			if err == nil {
				err = errors.New("returned no connection")
			}
			label := paths[result.idx].Name
			if label == "" {
				label = fmt.Sprintf("path %d", result.idx+1)
			}
			errs = append(errs, fmt.Errorf("%s path: %w", label, err))
			continue
		}
		if winner == nil {
			winner = result.conn
			cancelExcept(result.idx)
		} else {
			_ = result.conn.Close()
		}
	}
	if winner != nil {
		cancelExcept(-1)
		return winner, nil
	}
	cancelExcept(-1)
	if len(errs) == 1 {
		return nil, errs[0]
	}
	return nil, errors.Join(errs...)
}

// ProtobufClient owns one binary Protobuf RPC connection. It is deliberately
// separate from Client while production callers move from string method names
// to typed messages; the two protocols never share an envelope or codec.
type ProtobufClient struct {
	conn *protorpc.Conn
	// selfFP 是本端在这条连接的握手里出示的设备指纹 —— 也就是对端把本端会话
	// 落进 peer_fingerprint 的那个值。记在这里而不是让每个调用方各自去 keychain
	// 取:conversation_id 的派生输入必须是**对端眼里的本端身份**,而这条连接的
	// 握手是唯一说了算的地方(见 remote.Runtime.conversationID)。
	// 空表示这条连接没做过带指纹的握手(未鉴权的直连单测)。
	selfFP string

	// accountDirectURLs/CertPEM/Credential 是 D3 的自动直连下发内容——只有
	// auth.account 握手、且对端应答携带时才非空;auth.pair / auth.connect 从不
	// 写它们。这里只负责"接住并原样交出"(AccountDirectDelivery),校验与落地由
	// 调用方(remote_device_svc 的连接池)决定,见该包 conn_pool.go 的
	// accountDirectDeliverer 注释。
	accountDirectURLs       []string
	accountDirectCertPEM    string
	accountDirectCredential string
}

// SelfFingerprint 交出本端在这条连接上出示过的设备指纹。
func (c *ProtobufClient) SelfFingerprint() string { return c.selfFP }

// AccountDirectDelivery 交出这条连接的 auth.account 握手应答里携带的自动直连内容
// （D3:agentred 当前可路由的 wss 地址列表、它正在用的证书、为这台桌面端签发或
// 沿用的本地直连凭据）。ok 为 false 表示这条连接没做过 auth.account 握手（例如
// auth.pair / auth.connect），或者对端应答里没有下发内容（D5:agentred 没有可路由
// 地址）——两种情况调用方都不应该记录任何东西。
func (c *ProtobufClient) AccountDirectDelivery() (urls []string, certPEM, credential string, ok bool) {
	if c == nil || len(c.accountDirectURLs) == 0 {
		return nil, "", "", false
	}
	return c.accountDirectURLs, c.accountDirectCertPEM, c.accountDirectCredential, true
}

type ProtobufConnection interface {
	Conn() *protorpc.Conn
	Closed() <-chan struct{}
	Close() error
	// SelfFingerprint 交出本端在这条连接的握手里出示过的设备指纹 —— 也就是对端把
	// 本端会话落进 peer_fingerprint 的那个值。
	//
	// 它在接口里而不是靠类型断言取:这条连接一路上被包了好几层(连接池的
	// noopCloseClient、测试里的录制包装),而包装层嵌的是这个接口 —— 方法在接口里
	// 就自动透传,断言则会在第一层包装处静默退化成空串,让同一条对话换一个身份。
	SelfFingerprint() string
}

var _ ProtobufConnection = (*ProtobufClient)(nil)

// DialProtobuf opens a binary Protobuf WebSocket connection and starts its
// bidirectional request loop. The caller owns the returned client.
func DialProtobuf(ctx context.Context, opts Options) (*ProtobufClient, error) {
	u, err := url.Parse(opts.URL)
	if err != nil {
		return nil, err
	}
	dialer := *websocket.DefaultDialer
	dialer.TLSClientConfig = opts.TLSConfig
	dialer.Subprotocols = []string{protorpc.Subprotocol}
	ws, resp, err := dialer.DialContext(ctx, u.String(), nil)
	closeHandshakeBody(resp)
	if err != nil {
		return nil, classifyDialError(err, resp)
	}
	return newProtobufClient(ctx, ws), nil
}

func newProtobufClient(ctx context.Context, ws *websocket.Conn) *ProtobufClient {
	conn := protorpc.NewConn(protorpc.NewWebSocketFrameConn(ws), protorpc.NewRegistry())
	client := &ProtobufClient{conn: conn}
	go conn.Serve(ctx)
	return client
}

// DialRelayProtobuf connects through the account relay and completes the
// typed auth.account handshake before returning the connection to callers.
func DialRelayProtobuf(ctx context.Context, opts RelayOptions) (*ProtobufClient, error) {
	dialer := *websocket.DefaultDialer
	dialer.TLSClientConfig = opts.TLSConfig
	dialer.Subprotocols = []string{protorpc.Subprotocol}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+opts.AccessToken)
	ws, resp, err := dialer.DialContext(ctx, opts.URL, headers)
	closeHandshakeBody(resp)
	if err != nil {
		return nil, classifyRelayDialError(err, resp)
	}
	client := newProtobufClient(ctx, ws)
	result, err := client.AuthAccount(ctx, &agentrewire.AuthAccountRequest{Credential: opts.AccessToken})
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	if !result.GetOk() {
		_ = client.Close()
		return nil, errors.New("client.DialRelayProtobuf: daemon rejected account authentication")
	}
	return client, nil
}

// AuthAccount authenticates an account relay connection through the stable
// generic method registry. The encoded payload is an AuthAccountRequest.
//
// The protocol version is stamped here rather than at each call site: this is
// the one boundary every handshake passes through, and a caller that forgot to
// advertise would look exactly like a pre-versioning peer to the daemon.
func (c *ProtobufClient) AuthAccount(ctx context.Context, request *agentrewire.AuthAccountRequest) (*agentrewire.AuthAccountResponse, error) {
	request.ProtocolVersion = wireversion.Protocol
	request.MinSupportedProtocolVersion = wireversion.MinSupported
	response, err := wirecall.AuthAccount(ctx, wirecall.On(c.conn), request)
	if err != nil {
		return nil, ClassifyHandshakeError(err)
	}
	if versionErr := PeerProtocolVersionError(response.GetProtocolVersion()); versionErr != nil {
		return nil, versionErr
	}
	// Mode C 的本端身份**由对端认定**:请求体里已经没有指纹可报,对端从已验签的凭据
	// 取出身份后在应答里回写(决策 8)。这里不去自解自己的凭据 —— 那假定两端对 pfp
	// claim 的读法永远一致,一旦不一致 conversation_id 会静默算错。
	c.selfFP = response.GetPeerFingerprint()
	c.accountDirectURLs = response.GetDirectUrls()
	c.accountDirectCertPEM = response.GetTlsCertPem()
	c.accountDirectCredential = response.GetDirectCredential()
	return response, nil
}

// AuthDirect presents the local direct credential an account handshake
// delivered (auth.direct). It knows nothing about TLS: the caller must only
// reach it on a connection whose certificate already matched the pin, because
// this is the moment the credential leaves the desktop. As with auth.account,
// this connection's own identity is the one the responder states.
func (c *ProtobufClient) AuthDirect(ctx context.Context, request *agentrewire.AuthDirectRequest) (*agentrewire.AuthDirectResponse, error) {
	request.ProtocolVersion = wireversion.Protocol
	request.MinSupportedProtocolVersion = wireversion.MinSupported
	response, err := wirecall.AuthDirect(ctx, wirecall.On(c.conn), request)
	if err != nil {
		return nil, ClassifyHandshakeError(err)
	}
	if versionErr := PeerProtocolVersionError(response.GetProtocolVersion()); versionErr != nil {
		return nil, versionErr
	}
	c.selfFP = response.GetPeerFingerprint()
	return response, nil
}

func (c *ProtobufClient) AuthPair(ctx context.Context, request *agentrewire.AuthPairRequest) (*agentrewire.AuthPairResponse, error) {
	c.selfFP = request.GetDeviceFingerprint()
	request.ProtocolVersion = wireversion.Protocol
	request.MinSupportedProtocolVersion = wireversion.MinSupported
	response, err := wirecall.AuthPair(ctx, wirecall.On(c.conn), request)
	if err != nil {
		return nil, ClassifyHandshakeError(err)
	}
	if versionErr := PeerProtocolVersionError(response.GetProtocolVersion()); versionErr != nil {
		return nil, versionErr
	}
	return response, nil
}

func (c *ProtobufClient) AuthConnect(ctx context.Context, request *agentrewire.AuthConnectRequest) (*agentrewire.AuthConnectResponse, error) {
	c.selfFP = request.GetDeviceFingerprint()
	request.ProtocolVersion = wireversion.Protocol
	request.MinSupportedProtocolVersion = wireversion.MinSupported
	response, err := wirecall.AuthConnect(ctx, wirecall.On(c.conn), request)
	if err != nil {
		return nil, ClassifyHandshakeError(err)
	}
	if versionErr := PeerProtocolVersionError(response.GetProtocolVersion()); versionErr != nil {
		return nil, versionErr
	}
	return response, nil
}

// Conn exposes the typed Protobuf RPC operations and registry used for reverse
// requests. It does not expose the WebSocket transport or a stringly Call API.
func (c *ProtobufClient) Conn() *protorpc.Conn { return c.conn }

// Close shuts down the connection. It is safe to call more than once.
func (c *ProtobufClient) Close() error {
	if c == nil || c.conn == nil {
		return errors.New("not connected")
	}
	return c.conn.Close()
}

// Closed fires when the local client closes or the peer disconnects.
func (c *ProtobufClient) Closed() <-chan struct{} {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Done()
}

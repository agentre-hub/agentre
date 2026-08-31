package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/websocket"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

type Options struct {
	URL       string
	TLSConfig *tls.Config
}

type RelayOptions struct {
	URL               string
	AccessToken       string
	DeviceFingerprint string
	TLSConfig         *tls.Config
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
	// accept. A peer that reports nothing at all predates versioning and
	// lands here too.
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

// peerProtocolVersionError renders the one rejection every handshake shares.
//
// The empty string is not a pass: proto3 gives an absent field the same zero
// value as an explicitly empty one, so a pre-versioning agentred reports "" and
// has to be named as such — "could not reach agentred" would send the user
// hunting the network instead of running `make agentred-deploy`.
func peerProtocolVersionError(peerProtocol, peerMinSupported string) error {
	reason := wireversion.Reject(peerProtocol, peerMinSupported)
	if reason == "" {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrPeerProtocolVersionMismatch, reason)
}

// classifyHandshakeError folds the peer's own version rejection back into the
// local sentinel, so both directions of the same disagreement read the same to
// callers: the daemon refuses us with rpcerror.CodeProtocolVersion, we refuse
// the daemon by inspecting its response.
func classifyHandshakeError(err error) error {
	var rpcErr *rpcerror.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == rpcerror.CodeProtocolVersion {
		return fmt.Errorf("%w: %s (this build accepts protocol versions %s to %s)", ErrPeerProtocolVersionMismatch, rpcErr.Message, wireversion.MinSupported, wireversion.Protocol)
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
}

type ProtobufConnection interface {
	Conn() *protorpc.Conn
	Closed() <-chan struct{}
	Close() error
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
	result, err := client.AuthAccount(ctx, &agentrewire.AuthAccountRequest{
		Credential:        opts.AccessToken,
		DeviceFingerprint: opts.DeviceFingerprint,
	})
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
	response, err := protorpc.CallMethod(
		ctx,
		c.conn,
		uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT),
		request,
		func() *agentrewire.AuthAccountResponse { return &agentrewire.AuthAccountResponse{} },
	)
	if err != nil {
		return nil, classifyHandshakeError(err)
	}
	if versionErr := peerProtocolVersionError(response.GetProtocolVersion(), response.GetMinSupportedProtocolVersion()); versionErr != nil {
		return nil, versionErr
	}
	return response, nil
}

func (c *ProtobufClient) AuthPair(ctx context.Context, request *agentrewire.AuthPairRequest) (*agentrewire.AuthPairResponse, error) {
	request.ProtocolVersion = wireversion.Protocol
	request.MinSupportedProtocolVersion = wireversion.MinSupported
	response, err := protorpc.CallMethod(ctx, c.conn, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_PAIR), request,
		func() *agentrewire.AuthPairResponse { return &agentrewire.AuthPairResponse{} })
	if err != nil {
		return nil, classifyHandshakeError(err)
	}
	if versionErr := peerProtocolVersionError(response.GetProtocolVersion(), response.GetMinSupportedProtocolVersion()); versionErr != nil {
		return nil, versionErr
	}
	return response, nil
}

func (c *ProtobufClient) AuthConnect(ctx context.Context, request *agentrewire.AuthConnectRequest) (*agentrewire.AuthConnectResponse, error) {
	request.ProtocolVersion = wireversion.Protocol
	request.MinSupportedProtocolVersion = wireversion.MinSupported
	response, err := protorpc.CallMethod(ctx, c.conn, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_CONNECT), request,
		func() *agentrewire.AuthConnectResponse { return &agentrewire.AuthConnectResponse{} })
	if err != nil {
		return nil, classifyHandshakeError(err)
	}
	if versionErr := peerProtocolVersionError(response.GetProtocolVersion(), response.GetMinSupportedProtocolVersion()); versionErr != nil {
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

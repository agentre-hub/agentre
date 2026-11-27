// Package relaypeer is a relay client for one-shot account peers (agrctl): it
// holds one WebSocket to the account server's /v1/relay/client, opens virtual
// channels to a target (machine:<fingerprint> or conversation:<id>), does the
// auth.account handshake on each and hands back a protorpc connection for the
// typed wirecall functions.
//
// It reuses relaytransport's link and multiplexer, and carries its own copy of
// the handshake the desktop does in server_svc — that one lives behind the
// desktop's services and the cago runtime, which agrctl must not reach
// (internal/guard). Unlike the desktop's resident link it never redials in the
// background: a dropped link ends every open channel, and the next Open dials
// again, so the caller decides when a retry is worth it.
package relaypeer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/daemon/relaytransport"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// ClientPath is the account server's relay route for clients.
const ClientPath = "/v1/relay/client"

// Typed failures. Each wraps the detail the relay or the machine gave.
var (
	// ErrRelayUnavailable: the relay could not be reached, or the link dropped.
	// Retrying later may work.
	ErrRelayUnavailable = errors.New("relaypeer: the account relay is unavailable")
	// ErrCredentialRejected: the relay refused the bearer (HTTP 401) and it
	// could not be reauthorized.
	ErrCredentialRejected = errors.New("relaypeer: the account relay rejected the credential")
	// ErrTargetNotFound: the target does not resolve to a machine of this
	// account (an unsaved conversation, an unknown fingerprint).
	ErrTargetNotFound = errors.New("relaypeer: the target is not found in the account")
	// ErrTargetOffline: the machine is registered but not connected to the relay.
	ErrTargetOffline = errors.New("relaypeer: the target machine is offline")
	// ErrForwardFailed: the machine is online but the relay could not forward to it.
	ErrForwardFailed = errors.New("relaypeer: the relay could not forward to the target machine")
	// ErrTargetInvalid: the target string is malformed.
	ErrTargetInvalid = errors.New("relaypeer: the target is malformed")
	// ErrTargetForbidden: the target exists but cannot be addressed by this account.
	ErrTargetForbidden = errors.New("relaypeer: the target cannot be addressed")
	// ErrPeerVersionMismatch: the machine speaks another wire protocol version.
	// The error is a *VersionMismatchError, which says which end is older.
	ErrPeerVersionMismatch = errors.New("relaypeer: the target machine runs another wire protocol version")
)

// Stale names the end of a version mismatch that has to be upgraded: the older one.
type Stale string

const (
	// StaleDevice: the machine runs the older release.
	StaleDevice Stale = "device"
	// StaleSelf: this build is the older one.
	StaleSelf Stale = "self"
	// StaleUnknown: the machine's version is not known or has no order.
	StaleUnknown Stale = ""
)

// VersionMismatchError is ErrPeerVersionMismatch with both versions: Self is this
// build's, Device the machine's own ("" when it did not say).
type VersionMismatchError struct {
	Self   string
	Device string
}

func (e *VersionMismatchError) Error() string {
	if e.Device == "" {
		return fmt.Sprintf("%v: this build speaks wire protocol version %q", ErrPeerVersionMismatch, e.Self)
	}
	return fmt.Sprintf("%v: the device speaks wire protocol version %q, this build speaks %q", ErrPeerVersionMismatch, e.Device, e.Self)
}

func (e *VersionMismatchError) Is(target error) bool { return target == ErrPeerVersionMismatch }

// Stale is the end to upgrade — the older of the two releases.
func (e *VersionMismatchError) Stale() Stale {
	switch order, ok := wireversion.Compare(e.Device, e.Self); {
	case !ok || order == 0:
		return StaleUnknown
	case order < 0:
		return StaleDevice
	default:
		return StaleSelf
	}
}

// Channel-level failure codes the account relay answers on a channel it
// cannot route (relay_svc.ChannelCode*). They collide numerically with the
// runtime.* codes on purpose — they only ever arrive as an Id=0 frame during
// the handshake, never as an RPC answer.
const (
	channelCodeTargetNotFound int32 = -32010
	channelCodeTargetOffline  int32 = -32011
	channelCodeForwardFailed  int32 = -32012
	channelCodeTargetInvalid  int32 = -32013
	channelCodeTargetForbid   int32 = -32016
)

// MachineTarget addresses a machine by fingerprint (new conversations,
// unsaved ones, machine-scoped calls).
func MachineTarget(fingerprint string) string { return "machine:" + fingerprint }

// ConversationTarget addresses a saved conversation; the relay resolves the
// machine holding it.
func ConversationTarget(conversationID string) string { return "conversation:" + conversationID }

// Options configures a Peer.
type Options struct {
	// ServerURL is the account server's base URL (http or https).
	ServerURL string
	// Token returns the credential for both the WebSocket bearer and the
	// auth.account handshake. It is asked again at every dial.
	Token func(ctx context.Context) (string, error)
	// Reauthorize returns a fresh credential after the relay refused stale with
	// HTTP 401. nil = the credential cannot be renewed (a personal token).
	Reauthorize func(ctx context.Context, stale string) (string, error)
}

// Peer is one relay client link. Safe for concurrent use.
type Peer struct {
	opts Options
	link *relaytransport.HubLink
	mux  *relaytransport.Multiplexer

	mu     sync.Mutex
	token  string
	ctx    context.Context
	cancel context.CancelFunc
	closed bool
}

// New builds a Peer. Nothing is dialed until the first Open.
func New(opts Options) *Peer {
	ctx, cancel := context.WithCancel(context.Background())
	p := &Peer{opts: opts, ctx: ctx, cancel: cancel}
	p.link = relaytransport.NewHubLink(relaytransport.HubLinkOptions{
		Endpoint:            ClientPath,
		ServerURL:           opts.ServerURL,
		AccessTokenProvider: p.currentToken,
		Logf:                func(string, ...any) {},
	})
	p.mux = relaytransport.NewMultiplexer(p.link)
	go p.drainAccepted()
	return p
}

// Close ends the link and every channel on it.
func (p *Peer) Close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.cancel()
	p.mux.Close()
}

func (p *Peer) currentToken() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.token
}

// drainAccepted consumes the channels the relay opens on its own — only the
// reserved account-signal channel on a client link. agrctl has no use for the
// signals; reading them keeps the channel from backing up.
func (p *Peer) drainAccepted() {
	for {
		select {
		case <-p.ctx.Done():
			return
		case ch, ok := <-p.mux.Accept():
			if !ok || ch == nil {
				return
			}
			go func() {
				for {
					if _, err := ch.ReadPayload(); err != nil {
						return
					}
				}
			}()
		}
	}
}

// connect makes sure the link is up, dialing (and reauthorizing once on a 401)
// when it is not. Open holds no lock across the dial: two concurrent Opens on
// a cold link may both dial, and the later one simply replaces the former.
func (p *Peer) connect(ctx context.Context) error {
	if p.link.Connected() {
		return nil
	}
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return fmt.Errorf("%w: closed", ErrRelayUnavailable)
	}
	tok, err := p.opts.Token(ctx)
	if err != nil {
		return err
	}
	p.setToken(tok)
	_, err = p.link.Connect(p.ctx)
	if errors.Is(err, relaytransport.ErrRelayCredentialRejected) && p.opts.Reauthorize != nil {
		fresh, rerr := p.opts.Reauthorize(ctx, tok)
		if rerr != nil {
			return rerr
		}
		p.setToken(fresh)
		_, err = p.link.Connect(p.ctx)
	}
	switch {
	case err == nil:
		return nil
	case errors.Is(err, relaytransport.ErrRelayCredentialRejected):
		return ErrCredentialRejected
	default:
		return fmt.Errorf("%w: %v", ErrRelayUnavailable, err)
	}
}

func (p *Peer) setToken(tok string) {
	p.mu.Lock()
	p.token = tok
	p.mu.Unlock()
}

// Conn is one authenticated channel to a machine. It satisfies wirecall.Caller.
type Conn struct {
	rpc *protorpc.Conn
	// SelfFingerprint is the identity the machine filed this caller under
	// (AuthAccountResponse.peer_fingerprint): the source device of the turns it
	// starts and the origin of the sessions it creates.
	SelfFingerprint string
}

// Conn returns the protorpc connection for wirecall.
func (c *Conn) Conn() *protorpc.Conn { return c.rpc }

// Done closes when the channel ends (closed by either side, or the link dropped).
func (c *Conn) Done() <-chan struct{} { return c.rpc.Done() }

// Close closes this channel only; the link stays up for the next Open.
func (c *Conn) Close() error { return c.rpc.Close() }

// Open opens a channel to target and authenticates on it.
func (p *Peer) Open(ctx context.Context, target string) (*Conn, error) {
	if err := p.connect(ctx); err != nil {
		return nil, err
	}
	channel, err := p.mux.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRelayUnavailable, err)
	}
	if err := channel.WritePayload([]byte(target)); err != nil {
		_ = channel.Close()
		return nil, fmt.Errorf("%w: %v", ErrRelayUnavailable, err)
	}
	resp, err := authAccount(ctx, channel, p.currentToken())
	if err != nil {
		_ = channel.Close()
		return nil, err
	}
	conn := protorpc.NewConn(protorpc.NewPayloadFrameConn(channel), protorpc.NewRegistry())
	go conn.Serve(p.ctx)
	return &Conn{rpc: conn, SelfFingerprint: resp.GetPeerFingerprint()}, nil
}

// authAccount does the auth.account handshake by hand rather than through a
// protorpc.Conn: a channel the relay cannot route is answered with an Id=0
// error frame and then closed, which a protorpc.Conn would drop as a reply to
// nobody, leaving only "connection closed" (the same reason as server_svc's
// authAccountOverChannel).
func authAccount(ctx context.Context, channel relaytransport.PayloadChannel, credential string) (*agentrewire.AuthAccountResponse, error) {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = channel.Close()
		case <-done:
		}
	}()
	abort := func(err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	payload, err := proto.Marshal(&agentrewire.AuthAccountRequest{
		Credential: credential, ProtocolVersion: wireversion.Protocol, MinSupportedProtocolVersion: wireversion.MinSupported,
	})
	if err != nil {
		return nil, err
	}
	frame, err := proto.Marshal(&agentrewire.RpcFrame{Id: 1, Body: &agentrewire.RpcFrame_Request{Request: &agentrewire.Request{
		MethodId: uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT), EncodedPayload: payload,
	}}})
	if err != nil {
		return nil, err
	}
	// A write that fails because the relay already closed the channel is not the
	// reason: the error frame that closed it is already buffered, read it.
	writeErr := channel.WritePayload(frame)
	if writeErr != nil && !errors.Is(writeErr, relaytransport.ErrClosed) {
		return nil, abort(fmt.Errorf("%w: %v", ErrRelayUnavailable, writeErr))
	}
	for {
		raw, err := channel.ReadPayload()
		if err != nil || len(raw) == 0 {
			if err == nil || errors.Is(err, io.EOF) {
				err = errors.New("the channel closed before auth.account was answered")
			}
			return nil, abort(fmt.Errorf("%w: %v", ErrRelayUnavailable, err))
		}
		var reply agentrewire.RpcFrame
		if proto.Unmarshal(raw, &reply) != nil {
			continue
		}
		switch body := reply.GetBody().(type) {
		case *agentrewire.RpcFrame_Error:
			if reply.GetId() == 0 {
				return nil, channelError(body.Error)
			}
			if body.Error.GetCode() == rpcerror.CodeProtocolVersion {
				// The daemon's sentence is written from its own side ("peer" = us, "this
				// build" = the device), so it is read for the device's version, not echoed.
				mismatch := &VersionMismatchError{Self: wireversion.Protocol}
				if _, device, ok := wireversion.ParseRejection(body.Error.GetMessage()); ok {
					mismatch.Device = device
				}
				return nil, mismatch
			}
			return nil, &rpcerror.Error{Code: body.Error.GetCode(), Message: body.Error.GetMessage()}
		case *agentrewire.RpcFrame_Response:
			if reply.GetId() != 1 {
				continue
			}
			var resp agentrewire.AuthAccountResponse
			if err := proto.Unmarshal(body.Response.GetEncodedPayload(), &resp); err != nil {
				return nil, err
			}
			if !resp.GetOk() {
				return nil, errors.New("the target machine refused the account credential")
			}
			if !wireversion.Match(resp.GetProtocolVersion()) {
				return nil, &VersionMismatchError{Self: wireversion.Protocol, Device: resp.GetProtocolVersion()}
			}
			return &resp, nil
		}
	}
}

func channelError(e *agentrewire.RpcError) error {
	var sentinel error
	switch e.GetCode() {
	case channelCodeTargetNotFound:
		sentinel = ErrTargetNotFound
	case channelCodeTargetOffline:
		sentinel = ErrTargetOffline
	case channelCodeForwardFailed:
		sentinel = ErrForwardFailed
	case channelCodeTargetInvalid:
		sentinel = ErrTargetInvalid
	case channelCodeTargetForbid:
		sentinel = ErrTargetForbidden
	default:
		return fmt.Errorf("relaypeer: relay channel error %d: %s", e.GetCode(), e.GetMessage())
	}
	if msg := e.GetMessage(); msg != "" {
		return fmt.Errorf("%w: %s", sentinel, msg)
	}
	return sentinel
}

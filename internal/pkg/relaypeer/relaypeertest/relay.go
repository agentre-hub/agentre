// Package relaypeertest is an in-process stand-in for the account server's
// relay client route (/v1/relay/client) and the machines behind it, for tests
// of relay peers such as agrctl. It speaks the real envelope and channel
// protocol: the first payload on a new channel declares its target, a channel
// level failure is an Id=0 RpcFrame error followed by an empty payload, and an
// empty payload closes a channel. Each machine answers auth.account itself,
// as agentred does, and serves the methods a test registers on its registry.
//
// It never touches the network beyond the httptest listener the caller mounts
// it on.
package relaypeertest

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/relayenvelope"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// Channel-level failure codes, the same values the account server's relay_svc
// answers with.
const (
	CodeTargetNotFound int32 = -32010
	CodeTargetOffline  int32 = -32011
	CodeTargetInvalid  int32 = -32013
)

// Machine is one addressable device behind the relay (an agentred or a desktop).
type Machine struct {
	Fingerprint string
	// Registry serves every method except auth.account, which the relay adds.
	Registry *protorpc.Registry
	// Protocol is the protocol_version auth.account reports; "" = this build's.
	Protocol string
	// RejectVersion makes auth.account refuse the caller with CodeProtocolVersion,
	// the way agentred refuses a peer on another release.
	RejectVersion bool
	// Identify maps the caller's credential to the identity the machine files
	// its sessions under (AuthAccountResponse.peer_fingerprint). nil = "fp:" + credential.
	Identify func(credential string) string
}

// Relay is the fake account relay. Mount it with http.Handle(Path, relay).
type Relay struct {
	// Path is the route the relay is meant to be mounted on.
	Path string

	accept func(bearer string) bool

	mu          sync.Mutex
	machines    map[string]*Machine
	offline     map[string]bool
	saved       map[string]string
	conns       map[*websocket.Conn]struct{}
	dials       int
	credentials []string
}

// New builds a relay that admits a client whose bearer token accept approves.
func New(accept func(bearer string) bool) *Relay {
	return &Relay{Path: "/v1/relay/client", accept: accept, machines: map[string]*Machine{},
		offline: map[string]bool{}, saved: map[string]string{}, conns: map[*websocket.Conn]struct{}{}}
}

// AddMachine makes m addressable as machine:<fingerprint>.
func (r *Relay) AddMachine(m *Machine) {
	if m.Registry == nil {
		m.Registry = protorpc.NewRegistry()
	}
	protorpc.RegisterMethod(m.Registry, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT),
		func() *agentrewire.AuthAccountRequest { return &agentrewire.AuthAccountRequest{} },
		func(ctx context.Context, req *agentrewire.AuthAccountRequest) (*agentrewire.AuthAccountResponse, error) {
			r.mu.Lock()
			r.credentials = append(r.credentials, req.GetCredential())
			r.mu.Unlock()
			if m.RejectVersion {
				return nil, &rpcerror.Error{Code: rpcerror.CodeProtocolVersion, Message: wireversion.Reject(req.GetProtocolVersion() + "-other")}
			}
			fp := "fp:" + req.GetCredential()
			if m.Identify != nil {
				fp = m.Identify(req.GetCredential())
			}
			if conn := protorpc.ConnFromContext(ctx); conn != nil {
				conn.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: fp})
			}
			protocol := m.Protocol
			if protocol == "" {
				protocol = wireversion.Protocol
			}
			return &agentrewire.AuthAccountResponse{Ok: true, ProtocolVersion: protocol, PeerFingerprint: fp}, nil
		})
	r.mu.Lock()
	r.machines[m.Fingerprint] = m
	r.mu.Unlock()
}

// Save records conversationID as saved to the account on the machine fingerprint,
// so conversation:<id> resolves to it.
func (r *Relay) Save(conversationID, fingerprint string) {
	r.mu.Lock()
	r.saved[conversationID] = fingerprint
	r.mu.Unlock()
}

// SetOffline marks a machine as registered but without a relay connection.
func (r *Relay) SetOffline(fingerprint string, offline bool) {
	r.mu.Lock()
	r.offline[fingerprint] = offline
	r.mu.Unlock()
}

// DropConnections closes every client WebSocket, as a relay replica going away would.
func (r *Relay) DropConnections() {
	r.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(r.conns))
	for c := range r.conns {
		conns = append(conns, c)
	}
	r.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// Dials is how many client WebSockets the relay has accepted.
func (r *Relay) Dials() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dials
}

// Credentials are the auth.account credentials the machines have seen, in order.
func (r *Relay) Credentials() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.credentials...)
}

func (r *Relay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	bearer := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	if r.accept != nil && !r.accept(bearer) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	upgrader := websocket.Upgrader{}
	ws, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	r.mu.Lock()
	r.conns[ws] = struct{}{}
	r.dials++
	r.mu.Unlock()
	link := &clientLink{ws: ws, channels: map[string]*channel{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		r.mu.Lock()
		delete(r.conns, ws)
		r.mu.Unlock()
		_ = ws.Close()
		link.closeAll()
	}()
	// The real relay opens the reserved signal channel on every client link.
	_ = link.send(relayenvelope.Wrap("~signal", []byte{0x01}))
	for {
		mt, raw, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if mt != websocket.BinaryMessage {
			continue
		}
		id, payload, err := relayenvelope.Unwrap(raw)
		if err != nil {
			continue
		}
		link.mu.Lock()
		ch := link.channels[id]
		link.mu.Unlock()
		switch {
		case ch != nil && len(payload) == 0:
			link.forget(id)
			ch.markClosed()
		case ch != nil:
			ch.enqueue(payload)
		case len(payload) > 0:
			r.open(ctx, link, id, string(payload))
		}
	}
}

func (r *Relay) open(ctx context.Context, link *clientLink, id, target string) {
	m, code := r.resolve(target)
	if code != 0 {
		frame, _ := proto.Marshal(&agentrewire.RpcFrame{Id: 0, Body: &agentrewire.RpcFrame_Error{
			Error: &agentrewire.RpcError{Code: code, Message: "relay: cannot reach " + target}}})
		_ = link.send(relayenvelope.Wrap(id, frame))
		_ = link.send(relayenvelope.Wrap(id, nil))
		return
	}
	ch := &channel{link: link, id: id, inbound: make(chan []byte, 256), done: make(chan struct{})}
	link.mu.Lock()
	link.channels[id] = ch
	link.mu.Unlock()
	conn := protorpc.NewConn(protorpc.NewPayloadFrameConn(ch), m.Registry)
	go conn.Serve(ctx)
}

func (r *Relay) resolve(target string) (*Machine, int32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var fp string
	switch {
	case strings.HasPrefix(target, "machine:"):
		fp = strings.TrimPrefix(target, "machine:")
	case strings.HasPrefix(target, "conversation:"):
		saved, ok := r.saved[strings.TrimPrefix(target, "conversation:")]
		if !ok {
			return nil, CodeTargetNotFound
		}
		fp = saved
	default:
		return nil, CodeTargetInvalid
	}
	m, ok := r.machines[fp]
	switch {
	case !ok:
		return nil, CodeTargetNotFound
	case r.offline[fp]:
		return nil, CodeTargetOffline
	}
	return m, 0
}

type clientLink struct {
	ws       *websocket.Conn
	writeMu  sync.Mutex
	mu       sync.Mutex
	channels map[string]*channel
}

func (l *clientLink) send(envelope []byte, err error) error {
	if err != nil {
		return err
	}
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	return l.ws.WriteMessage(websocket.BinaryMessage, envelope)
}

func (l *clientLink) forget(id string) {
	l.mu.Lock()
	delete(l.channels, id)
	l.mu.Unlock()
}

func (l *clientLink) closeAll() {
	l.mu.Lock()
	chans := l.channels
	l.channels = map[string]*channel{}
	l.mu.Unlock()
	for _, ch := range chans {
		ch.markClosed()
	}
}

// channel is the machine's end of one virtual channel.
type channel struct {
	link    *clientLink
	id      string
	inbound chan []byte
	done    chan struct{}
	once    sync.Once
}

func (c *channel) enqueue(p []byte) {
	select {
	case c.inbound <- append([]byte(nil), p...):
	case <-c.done:
	}
}

func (c *channel) ReadPayload() ([]byte, error) {
	select {
	case p := <-c.inbound:
		return p, nil
	case <-c.done:
		return nil, io.EOF
	}
}

func (c *channel) WritePayload(p []byte) error {
	select {
	case <-c.done:
		return errClosed
	default:
	}
	return c.link.send(relayenvelope.Wrap(c.id, p))
}

func (c *channel) Close() error {
	c.link.forget(c.id)
	select {
	case <-c.done:
	default:
		_ = c.link.send(relayenvelope.Wrap(c.id, nil))
	}
	c.markClosed()
	return nil
}

func (c *channel) Done() <-chan struct{} { return c.done }

func (c *channel) markClosed() { c.once.Do(func() { close(c.done) }) }

type closedError struct{}

func (closedError) Error() string { return "relaypeertest: channel closed" }

var errClosed error = closedError{}

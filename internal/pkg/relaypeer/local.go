package relaypeer

import (
	"context"
	"io"
	"sync"

	"github.com/agentre-hub/agentre/internal/daemon/relaytransport"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// OpenChannel authenticates on a channel that already reaches one machine and
// hands back the same Conn Open does. It is for a host whose target is itself
// (agentred dispatching to its own runtime): the channel is an in-process Pipe
// served by the host's own registry, so no relay round trip and no self-dial,
// but the identity still comes from the auth.account handshake.
func OpenChannel(ctx context.Context, channel relaytransport.PayloadChannel, credential string) (*Conn, error) {
	resp, err := authAccount(ctx, channel, credential)
	if err != nil {
		_ = channel.Close()
		return nil, err
	}
	conn := protorpc.NewConn(protorpc.NewPayloadFrameConn(channel), protorpc.NewRegistry())
	// The connection outlives the open: it ends when either side closes the channel.
	go conn.Serve(context.WithoutCancel(ctx))
	return &Conn{rpc: conn, SelfFingerprint: resp.GetPeerFingerprint()}, nil
}

// Pipe returns the two ends of an in-process payload channel. Closing either
// end ends both.
func Pipe() (relaytransport.PayloadChannel, relaytransport.PayloadChannel) {
	shared := &pipeState{done: make(chan struct{})}
	a := &pipeEnd{state: shared, inbound: make(chan []byte, 256)}
	b := &pipeEnd{state: shared, inbound: make(chan []byte, 256)}
	a.peer, b.peer = b, a
	return a, b
}

type pipeState struct {
	done chan struct{}
	once sync.Once
}

type pipeEnd struct {
	state   *pipeState
	inbound chan []byte
	peer    *pipeEnd
}

func (p *pipeEnd) ReadPayload() ([]byte, error) {
	select {
	case payload := <-p.inbound:
		return payload, nil
	case <-p.state.done:
		return nil, io.EOF
	}
}

func (p *pipeEnd) WritePayload(payload []byte) error {
	select {
	case <-p.state.done:
		return relaytransport.ErrClosed
	default:
	}
	select {
	case p.peer.inbound <- append([]byte(nil), payload...):
		return nil
	case <-p.state.done:
		return relaytransport.ErrClosed
	}
}

func (p *pipeEnd) Close() error {
	p.state.once.Do(func() { close(p.state.done) })
	return nil
}

func (p *pipeEnd) Done() <-chan struct{} { return p.state.done }

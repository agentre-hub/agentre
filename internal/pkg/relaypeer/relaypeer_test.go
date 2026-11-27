package relaypeer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/relaypeer/relaypeertest"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

const box = "sha256:box"

// relayWith 起一个只认 token 的假 relay，挂一台 box 机器（会答 health.ping）。
func relayWith(t *testing.T, token string) (*relaypeertest.Relay, *httptest.Server) {
	t.Helper()
	var mu sync.Mutex
	current := token
	relay := relaypeertest.New(func(bearer string) bool { mu.Lock(); defer mu.Unlock(); return bearer == current })
	reg := protorpc.NewRegistry()
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_HEALTH_PING),
		func() *agentrewire.HealthPingRequest { return &agentrewire.HealthPingRequest{} },
		func(ctx context.Context, _ *agentrewire.HealthPingRequest) (*agentrewire.HealthPingResponse, error) {
			return &agentrewire.HealthPingResponse{InstanceUuid: protorpc.ConnFromContext(ctx).Auth().DeviceFingerprint}, nil
		})
	relay.AddMachine(&relaypeertest.Machine{Fingerprint: box, Registry: reg})
	mux := http.NewServeMux()
	mux.Handle(relay.Path, relay)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return relay, srv
}

func staticToken(tok string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return tok, nil }
}

func TestPeer_GivenAMachineTarget_WhenOpened_ThenItAuthenticatesWithTheBearerAndCallsGoThrough(t *testing.T) {
	relay, srv := relayWith(t, "tok-1")
	p := New(Options{ServerURL: srv.URL, Token: staticToken("tok-1")})
	t.Cleanup(p.Close)

	conn, err := p.Open(context.Background(), MachineTarget(box))
	require.NoError(t, err)
	assert.Equal(t, "fp:tok-1", conn.SelfFingerprint, "the identity agentred filed us under comes from auth.account")
	ping, err := wirecall.HealthPing(context.Background(), conn, &agentrewire.HealthPingRequest{})
	require.NoError(t, err)
	assert.Equal(t, "fp:tok-1", ping.GetInstanceUuid(), "the call runs on the authenticated channel")
	assert.Equal(t, []string{"tok-1"}, relay.Credentials(), "auth.account carries the same credential as the socket")

	second, err := p.Open(context.Background(), MachineTarget(box))
	require.NoError(t, err)
	_ = second.Close()
	assert.Equal(t, 1, relay.Dials(), "channels share one WebSocket")
}

func TestPeer_GivenAnUnreachableTarget_ThenTheChannelFailureIsTyped(t *testing.T) {
	relay, srv := relayWith(t, "tok")
	p := New(Options{ServerURL: srv.URL, Token: staticToken("tok")})
	t.Cleanup(p.Close)

	_, err := p.Open(context.Background(), MachineTarget("sha256:nobody"))
	assert.True(t, errors.Is(err, ErrTargetNotFound), "err = %v", err)
	_, err = p.Open(context.Background(), ConversationTarget("0193b6f0-7c5e-7d2a-9f4b-3c1d2e5f6a7b"))
	assert.True(t, errors.Is(err, ErrTargetNotFound), "an unsaved conversation does not resolve: %v", err)

	relay.SetOffline(box, true)
	_, err = p.Open(context.Background(), MachineTarget(box))
	assert.True(t, errors.Is(err, ErrTargetOffline), "err = %v", err)
}

func TestPeer_GivenAMachineOnAnotherRelease_ThenOpenSaysItNeedsAnUpgrade(t *testing.T) {
	for name, m := range map[string]*relaypeertest.Machine{
		"the machine refuses us": {Fingerprint: "sha256:old", RejectVersion: true},
		"we refuse the machine":  {Fingerprint: "sha256:old", Protocol: "0.0.1"},
	} {
		t.Run(name, func(t *testing.T) {
			relay, srv := relayWith(t, "tok")
			relay.AddMachine(m)
			p := New(Options{ServerURL: srv.URL, Token: staticToken("tok")})
			t.Cleanup(p.Close)
			_, err := p.Open(context.Background(), MachineTarget("sha256:old"))
			assert.True(t, errors.Is(err, ErrPeerVersionMismatch), "err = %v", err)
			assert.Contains(t, err.Error(), wireversion.Protocol, "the message names this build's version")
			assert.NotContains(t, err.Error(), "peer speaks", "no text written from the other side's perspective")
			assert.NotContains(t, err.Error(), "this build speaks wire protocol version \"0.", "no echo of a self-perspective sentence")
			if m.Protocol != "" {
				assert.Contains(t, err.Error(), m.Protocol, "the device's own version is named when it is known")
			}
		})
	}
}

func TestPeer_GivenTheRelayRejectsTheBearer_ThenItReauthorizesOnceOrReportsTheRejection(t *testing.T) {
	_, srv := relayWith(t, "fresh")
	var stale []string
	p := New(Options{ServerURL: srv.URL, Token: staticToken("old"),
		Reauthorize: func(_ context.Context, s string) (string, error) { stale = append(stale, s); return "fresh", nil }})
	t.Cleanup(p.Close)
	conn, err := p.Open(context.Background(), MachineTarget(box))
	require.NoError(t, err)
	assert.Equal(t, []string{"old"}, stale)
	assert.Equal(t, "fp:fresh", conn.SelfFingerprint, "the handshake uses the reauthorized credential")

	q := New(Options{ServerURL: srv.URL, Token: staticToken("old")})
	t.Cleanup(q.Close)
	_, err = q.Open(context.Background(), MachineTarget(box))
	assert.True(t, errors.Is(err, ErrCredentialRejected), "err = %v", err)
}

func TestPeer_GivenTheRelayDrops_ThenOpenChannelsEndAndTheNextOpenRedials(t *testing.T) {
	relay, srv := relayWith(t, "tok")
	p := New(Options{ServerURL: srv.URL, Token: staticToken("tok")})
	t.Cleanup(p.Close)
	conn, err := p.Open(context.Background(), MachineTarget(box))
	require.NoError(t, err)

	relay.DropConnections()
	select {
	case <-conn.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the channel outlived its relay link")
	}
	_, err = wirecall.HealthPing(context.Background(), conn, &agentrewire.HealthPingRequest{})
	assert.Error(t, err)

	again, err := p.Open(context.Background(), MachineTarget(box))
	require.NoError(t, err)
	_, err = wirecall.HealthPing(context.Background(), again, &agentrewire.HealthPingRequest{})
	require.NoError(t, err)
	assert.Equal(t, 2, relay.Dials())
}

func TestPeer_GivenNoServer_ThenOpenFailsAsUnreachable(t *testing.T) {
	p := New(Options{ServerURL: "http://127.0.0.1:1", Token: staticToken("tok")})
	t.Cleanup(p.Close)
	_, err := p.Open(context.Background(), MachineTarget(box))
	assert.True(t, errors.Is(err, ErrRelayUnavailable), "err = %v", err)
}

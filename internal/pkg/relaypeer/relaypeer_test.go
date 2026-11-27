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

func TestPeer_GivenAMachineOnAnotherRelease_ThenOpenSaysWhichEndIsOlder(t *testing.T) {
	for name, c := range map[string]struct {
		m     *relaypeertest.Machine
		stale Stale
	}{
		"an older machine refuses us": {&relaypeertest.Machine{Fingerprint: "sha256:old", RejectVersion: true, Protocol: "0.0.1"}, StaleDevice},
		"a newer machine refuses us":  {&relaypeertest.Machine{Fingerprint: "sha256:old", RejectVersion: true, Protocol: "99.0.0"}, StaleSelf},
		"we refuse an older machine":  {&relaypeertest.Machine{Fingerprint: "sha256:old", Protocol: "0.0.1"}, StaleDevice},
		"we refuse a newer machine":   {&relaypeertest.Machine{Fingerprint: "sha256:old", Protocol: "99.0.0"}, StaleSelf},
		"a refusal we cannot read":    {&relaypeertest.Machine{Fingerprint: "sha256:old", RejectVersion: true, Protocol: "not-a-version"}, StaleUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			relay, srv := relayWith(t, "tok")
			relay.AddMachine(c.m)
			p := New(Options{ServerURL: srv.URL, Token: staticToken("tok")})
			t.Cleanup(p.Close)
			_, err := p.Open(context.Background(), MachineTarget("sha256:old"))
			assert.True(t, errors.Is(err, ErrPeerVersionMismatch), "err = %v", err)
			var vm *VersionMismatchError
			require.True(t, errors.As(err, &vm), "err = %v", err)
			assert.Equal(t, c.stale, vm.Stale(), "the older end is the one to upgrade")
			assert.Equal(t, wireversion.Protocol, vm.Self)
			if c.stale != StaleUnknown {
				assert.Equal(t, c.m.Protocol, vm.Device, "the device's own version is named when it is known")
			}
			assert.Contains(t, err.Error(), wireversion.Protocol, "the message names this build's version")
			assert.NotContains(t, err.Error(), "peer speaks", "no text written from the other side's perspective")
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

// agrctl 的文案是英文：relay 在通道失败时回的 server 文案（例如内部错误）按握手请求点名的
// 语言回，所以拨 relay 时同样点名 en，与账号接口的 HTTP 请求一致。
func TestPeer_GivenTheRelayIsDialed_ThenTheHandshakeAsksTheServerForEnglish(t *testing.T) {
	relay := relaypeertest.New(func(bearer string) bool { return bearer == "tok-1" })
	relay.AddMachine(&relaypeertest.Machine{Fingerprint: box, Registry: protorpc.NewRegistry()})
	var mu sync.Mutex
	var langs []string
	mux := http.NewServeMux()
	mux.Handle(relay.Path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		langs = append(langs, r.Header.Get("Accept-Language"))
		mu.Unlock()
		relay.ServeHTTP(w, r)
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p := New(Options{ServerURL: srv.URL, Token: staticToken("tok-1")})
	t.Cleanup(p.Close)

	conn, err := p.Open(context.Background(), MachineTarget(box))
	require.NoError(t, err)
	_ = conn.Close()
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"en"}, langs)
}

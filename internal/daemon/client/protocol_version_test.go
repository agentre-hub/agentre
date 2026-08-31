package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// authPeer stands in for one agentred build: it answers every auth handshake
// with the protocol version this fake claims to speak, and records the version
// the desktop advertised.
type authPeer struct {
	server    *httptest.Server
	requested chan string
}

func newAuthPeer(t *testing.T, reply func(methodID uint32) proto.Message) *authPeer {
	t.Helper()
	peer := &authPeer{requested: make(chan string, 4)}
	upgrader := websocket.Upgrader{Subprotocols: []string{protorpc.Subprotocol}}
	peer.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.Close() }()
		for {
			_, payload, readErr := ws.ReadMessage()
			if readErr != nil {
				return
			}
			var frame agentrewire.RpcFrame
			if proto.Unmarshal(payload, &frame) != nil {
				return
			}
			request := frame.GetRequest()
			if request == nil {
				continue
			}
			peer.requested <- advertisedVersion(t, request)
			encoded, marshalErr := proto.Marshal(reply(request.GetMethodId()))
			if marshalErr != nil {
				return
			}
			response, marshalErr := proto.Marshal(&agentrewire.RpcFrame{
				Id:   frame.GetId(),
				Body: &agentrewire.RpcFrame_Response{Response: &agentrewire.Response{MethodId: request.GetMethodId(), EncodedPayload: encoded}},
			})
			if marshalErr != nil {
				return
			}
			if ws.WriteMessage(websocket.BinaryMessage, response) != nil {
				return
			}
		}
	}))
	t.Cleanup(peer.server.Close)
	return peer
}

func advertisedVersion(t *testing.T, request *agentrewire.Request) string {
	t.Helper()
	switch agentrewire.RpcMethod(request.GetMethodId()) {
	case agentrewire.RpcMethod_RPC_METHOD_AUTH_PAIR:
		value := &agentrewire.AuthPairRequest{}
		require.NoError(t, proto.Unmarshal(request.GetEncodedPayload(), value))
		return value.GetProtocolVersion()
	case agentrewire.RpcMethod_RPC_METHOD_AUTH_CONNECT:
		value := &agentrewire.AuthConnectRequest{}
		require.NoError(t, proto.Unmarshal(request.GetEncodedPayload(), value))
		return value.GetProtocolVersion()
	default:
		value := &agentrewire.AuthAccountRequest{}
		require.NoError(t, proto.Unmarshal(request.GetEncodedPayload(), value))
		return value.GetProtocolVersion()
	}
}

func (p *authPeer) url() string { return "ws" + strings.TrimPrefix(p.server.URL, "http") }

func replyWithVersion(version string) func(uint32) proto.Message {
	return func(methodID uint32) proto.Message {
		switch agentrewire.RpcMethod(methodID) {
		case agentrewire.RpcMethod_RPC_METHOD_AUTH_PAIR:
			return &agentrewire.AuthPairResponse{DeviceToken: "token", DaemonFingerprint: "sha256:daemon", ProtocolVersion: version}
		case agentrewire.RpcMethod_RPC_METHOD_AUTH_CONNECT:
			return &agentrewire.AuthConnectResponse{Ok: true, ProtocolVersion: version}
		default:
			return &agentrewire.AuthAccountResponse{Ok: true, InstanceUuid: "uuid-1", ProtocolVersion: version}
		}
	}
}

// Given a daemon built from the same wire package, When the desktop pairs with
// it, Then the handshake succeeds and the request carried this build's version
// — the daemon has to be able to reject us too, so advertising is not optional.
func TestAuthPair_GivenPeerSpeaksTheSameProtocolVersion_WhenPairing_ThenSucceedsAndAdvertisesOurVersion(t *testing.T) {
	peer := newAuthPeer(t, replyWithVersion(wireversion.Protocol))

	c, err := DialProtobuf(t.Context(), Options{URL: peer.url()})
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	result, err := c.AuthPair(t.Context(), &agentrewire.AuthPairRequest{Code: "ABC123", DeviceFingerprint: "sha256:desktop"})
	require.NoError(t, err)
	assert.Equal(t, "token", result.GetDeviceToken())
	assert.Equal(t, wireversion.Protocol, <-peer.requested)
}

// Given an agentred deployed from a different revision, When the desktop
// connects, Then the handshake fails with the version-mismatch sentinel and
// names both versions — `make agentred-deploy` makes skew routine, and "could
// not reach agentred" would send the user hunting the network instead.
func TestAuthConnect_GivenPeerSpeaksAnotherProtocolVersion_WhenConnecting_ThenFailsWithVersionMismatch(t *testing.T) {
	peer := newAuthPeer(t, replyWithVersion("0.0.9"))

	c, err := DialProtobuf(t.Context(), Options{URL: peer.url()})
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	_, err = c.AuthConnect(t.Context(), &agentrewire.AuthConnectRequest{DeviceFingerprint: "sha256:desktop", DeviceToken: "token"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPeerProtocolVersionMismatch)
	assert.NotErrorIs(t, err, ErrPeerProtocolUnsupported)
	assert.Contains(t, err.Error(), "0.0.9")
	assert.Contains(t, err.Error(), wireversion.Protocol)
}

// Given an agentred that predates protocol versioning, When it answers the
// handshake, Then proto3 hands us the empty string and it must be read as "too
// old", never as "same version". This is the whole reason the check cannot be
// written as `peer != "" && peer != ours`.
func TestAuthAccount_GivenPeerOmitsTheProtocolVersion_WhenAuthenticating_ThenRejectedAsTooOld(t *testing.T) {
	peer := newAuthPeer(t, replyWithVersion(""))

	c, err := DialProtobuf(t.Context(), Options{URL: peer.url()})
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	_, err = c.AuthAccount(t.Context(), &agentrewire.AuthAccountRequest{Credential: "token"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPeerProtocolVersionMismatch)
	assert.Contains(t, err.Error(), "no protocol version")
	assert.Contains(t, err.Error(), wireversion.Protocol)
}

// Given a peer that does not offer the agentre-protobuf subprotocol at all,
// When the desktop dials it directly, Then the raw 426 is folded into the
// "speaks no such protocol" sentinel instead of leaking gorilla's bad-handshake
// error verbatim.
func TestDialProtobuf_GivenPeerRefusesTheSubprotocol_WhenDialing_ThenFailsWithProtocolUnsupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "protobuf subprotocol required", http.StatusUpgradeRequired)
	}))
	defer server.Close()

	_, err := DialProtobuf(t.Context(), Options{URL: "ws" + strings.TrimPrefix(server.URL, "http")})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPeerProtocolUnsupported)
	assert.NotErrorIs(t, err, ErrPeerProtocolVersionMismatch)
}

// Same rejection, but arriving through the relay: 426 used to fall into the
// default branch and surface as "relay rejected the connection with 426".
func TestDialRelayProtobuf_GivenRelayPeerRefusesTheSubprotocol_WhenDialing_ThenFailsWithProtocolUnsupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUpgradeRequired)
	}))
	defer server.Close()

	_, err := DialRelayProtobuf(t.Context(), RelayOptions{URL: "ws" + strings.TrimPrefix(server.URL, "http"), AccessToken: "token"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPeerProtocolUnsupported)
	assert.NotErrorIs(t, err, ErrRelayDaemonNotFound)
}

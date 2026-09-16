package hermes

import (
	"context"
	"errors"
	"strings"
)

// ProbeRequest is a single connectivity self-check against a running `hermes
// serve`: reach the root page, read the loopback session token (or mint a
// ws-ticket for a gated serve), complete the WebSocket handshake and receive
// `gateway.ready`.
type ProbeRequest struct {
	// URL is the canonical `http(s)://host:port` of the running serve.
	URL string
	// AuthProvider is the persisted provider hint for a gated serve (may be empty).
	AuthProvider string
	// Credentials supplies the bearer token for a gated serve. nil means the probe
	// only supports the loopback path.
	Credentials CredentialSource
}

// Probe dials the serve and waits for gateway.ready. Success means the endpoint
// is reachable and speaks the Hermes TUI-gateway protocol; it deliberately does
// not create a session or run a prompt (agent turns belong to the chat path).
// The returned message names the endpoint that answered.
func Probe(ctx context.Context, req ProbeRequest) (string, error) {
	baseURL := strings.TrimSpace(req.URL)
	if baseURL == "" {
		return "", errors.New("hermes probe: server URL is required")
	}
	sess, err := dialGatewayWithAuth(ctx, baseURL, req.AuthProvider, req.Credentials, nil, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = sess.Close(context.Background()) }()

	readyCtx, cancel := context.WithTimeout(ctx, gatewayReadyTimeout)
	defer cancel()
	if err := sess.WaitReady(readyCtx); err != nil {
		return "", err
	}
	return baseURL, nil
}

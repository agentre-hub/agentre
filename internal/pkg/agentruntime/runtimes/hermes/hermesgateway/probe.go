package hermesgateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// EventGatewayReady is the event `params.type` a serve sends once the WebSocket
// is accepted. Nothing may be assumed about a connection before it.
const EventGatewayReady = "gateway.ready"

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
	conn, err := Dial(ctx, DialRequest{URL: baseURL, AuthProvider: req.AuthProvider, Credentials: req.Credentials})
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()

	if err := awaitReady(ctx, conn); err != nil {
		return "", err
	}
	return baseURL, nil
}

// awaitReady reads frames until gateway.ready, the connection dies, ctx ends or
// ReadyTimeout elapses. The caller's own cancellation or deadline is returned
// as ctx.Err() so it stays distinguishable from the gateway never getting ready.
// A single WebSocket message may carry several newline-delimited frames; junk
// lines are skipped as the session codec does.
func awaitReady(ctx context.Context, conn *websocket.Conn) error {
	readyCtx, cancel := context.WithTimeout(ctx, ReadyTimeout)
	defer cancel()
	stop := context.AfterFunc(readyCtx, func() { _ = conn.SetReadDeadline(time.Now()) })
	defer stop()
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if readyCtx.Err() != nil {
				return errors.New("hermes gateway: timed out waiting for gateway.ready")
			}
			return ClassifyReadError(err)
		}
		for _, line := range strings.Split(string(message), "\n") {
			if isReadyFrame(line) {
				return nil
			}
		}
	}
}

func isReadyFrame(line string) bool {
	var frame struct {
		Method string `json:"method"`
		Params struct {
			Type string `json:"type"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &frame); err != nil {
		return false
	}
	return frame.Method == "event" && frame.Params.Type == EventGatewayReady
}

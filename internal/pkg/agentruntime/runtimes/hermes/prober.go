package hermes

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/hermes/hermesgateway"
)

// ProbeRequest is a single connectivity self-check against a running `hermes
// serve`; see hermesgateway.ProbeRequest.
type ProbeRequest = hermesgateway.ProbeRequest

// Probe dials the serve and waits for gateway.ready. It lives in the
// side-effect-free hermesgateway package so agentred can test a connection
// without registering this runtime; see hermesgateway.Probe.
func Probe(ctx context.Context, req ProbeRequest) (string, error) {
	return hermesgateway.Probe(ctx, req)
}

package remote

import (
	"context"
	"sort"
	"strings"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// ProviderLeaseSource hands out this desktop's own credential for providers, as
// short leases attached to a run it sends over a direct connection
// (规格「凭据 › agentred：按需租用」:直连派发时桌面端随这一轮附带一份租约). Providers it
// cannot hand out are simply absent from the result.
type ProviderLeaseSource interface {
	ProviderLeases(ctx context.Context, providerKeys []string) []*agentrewire.ProviderLease
}

// WithProviderLeases installs the lease source used for direct runs. Without it
// no run carries a lease and the agentred leases on its own.
func WithProviderLeases(source ProviderLeaseSource) Option {
	return func(r *Runtime) { r.leaseSource = source }
}

// directRouted is implemented by a connection dialed straight to the agentred
// (LAN), as opposed to a channel through the account relay.
type directRouted interface {
	DirectRoute() bool
}

// buildRunRequest turns the run parameters into the wire request, attaching a
// lease per provider the turn may route to when the current connection is
// direct. Over the relay the agentred leases from the account server itself, so
// no key crosses the relay.
func (r *Runtime) buildRunRequest(ctx context.Context, params wire.RunParams) (*agentrewire.RuntimeRunRequest, error) {
	request, err := protowire.RunRequestToProto(params)
	if err != nil {
		return nil, err
	}
	if r.leaseSource == nil {
		return request, nil
	}
	if direct, ok := r.conn().(directRouted); !ok || !direct.DirectRoute() {
		return request, nil
	}
	if keys := runProviderKeys(request); len(keys) > 0 {
		request.ProviderLeases = r.leaseSource.ProviderLeases(ctx, keys)
	}
	return request, nil
}

// runProviderKeys lists, sorted and deduplicated, every provider a turn may
// route to: the turn's own target, the profile's bound provider and each model
// route's provider.
func runProviderKeys(request *agentrewire.RuntimeRunRequest) []string {
	set := map[string]struct{}{}
	add := func(key string) {
		if key = strings.TrimSpace(key); key != "" {
			set[key] = struct{}{}
		}
	}
	add(request.GetLlmProviderKey())
	routes := request.GetBackend().GetModelRoutes()
	add(request.GetBackend().GetLlmProviderKey())
	if profile := request.GetEngineProfile(); profile != nil {
		add(profile.GetLlmProviderKey())
		routes = profile.GetModelRoutes()
	}
	if parsed, err := agent_backend_entity.ParseModelRoutes(routes); err == nil {
		for _, target := range parsed {
			add(target.ProviderKey)
		}
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Package leases holds the provider credential leases an agentred runs with
// (规格「凭据 › agentred：按需租用」). A lease is a provider's key and endpoint for a
// bounded time. It lives in this process's memory only: nothing here ever
// reaches state.json, so a restart starts empty and re-requests on first use.
//
// Two sources feed it:
//
//   - the account server, asked with this device's own account credential when a
//     turn, a provider test or a model discovery needs a provider it holds no valid
//     lease for. A lease still in use is renewed when one third of its lifetime
//     remains (on the next use — every gateway request of a running turn is one);
//   - the desktop, which attaches leases to a run it sends over a direct
//     connection. Those are scoped to that run: preferred while it lasts, dropped
//     when it ends.
//
// The account channel's lease-revoked signal drops both kinds at once.
package leases

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/pkg/cagoenvelope"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

const (
	defaultHTTPTimeout = 15 * time.Second
	maxResponseBytes   = 64 << 10
)

// HTTPDoer is the narrow HTTP seam used for lease requests.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Lease is one provider's credential for a bounded time. Secret — never log it.
type Lease struct {
	ProviderKey string
	APIKey      string
	BaseURL     string
	// IssuedAt is when this process received the lease; ExpiresAt is when it stops
	// being usable. Their difference is the lifetime the renewal threshold is a
	// third of.
	IssuedAt  time.Time
	ExpiresAt time.Time
	// Provider is the provider's non-secret metadata a desktop attached with the
	// lease (an agentred paired without logging in has no snapshot to read it
	// from). Nil for account-issued leases.
	Provider *state.LLMProviderMeta
}

type attachedLease struct {
	Lease
	gen uint64
}

func (l Lease) validAt(now time.Time) bool {
	return l.APIKey != "" && now.Before(l.ExpiresAt)
}

// dueForRenewal reports whether no more than a third of the lifetime remains.
func (l Lease) dueForRenewal(now time.Time) bool {
	lifetime := l.ExpiresAt.Sub(l.IssuedAt)
	return l.ExpiresAt.Sub(now)*3 <= lifetime
}

// Options supplies the account endpoint and this device's credential.
type Options struct {
	ServerURL   func() string
	AccessToken func() string
	HTTPClient  HTTPDoer
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Manager is the in-memory lease cache. The zero value is not usable; call New.
type Manager struct {
	serverURL   func() string
	accessToken func() string
	httpClient  HTTPDoer
	now         func() time.Time

	mu sync.Mutex
	// server holds the account-issued lease per provider key.
	server map[string]Lease
	// attached holds desktop-attached leases: provider key → holder (one run) → lease.
	attached map[string]map[string]attachedLease
	// attachGen numbers Attach calls so a late release of an earlier attach under
	// the same holder never drops a newer one.
	attachGen uint64
	// requesting serializes requests per provider key so concurrent needs share one.
	requesting map[string]*sync.Mutex
}

// New constructs a lease manager.
func New(opts Options) *Manager {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Manager{
		serverURL: opts.ServerURL, accessToken: opts.AccessToken, httpClient: client, now: now,
		server: map[string]Lease{}, attached: map[string]map[string]attachedLease{}, requesting: map[string]*sync.Mutex{},
	}
}

// Get returns a usable lease for providerKey: a valid desktop-attached one first,
// then the account-issued one (renewed when a third of its lifetime remains),
// requesting a new one when none is held. It fails with an error wrapping
// rpcerror.ErrProviderLeaseUnavailable when no valid lease can be had.
func (m *Manager) Get(ctx context.Context, providerKey, purpose string) (Lease, error) {
	providerKey = strings.TrimSpace(providerKey)
	if lease, ok := m.attachedLease(providerKey); ok {
		return lease, nil
	}
	current, held := m.serverLease(providerKey)
	if held && !current.dueForRenewal(m.now()) {
		return current, nil
	}

	gate := m.requestGate(providerKey)
	gate.Lock()
	defer gate.Unlock()
	// 等锁期间别人可能已经申请(或续租)到了。
	if current, held = m.serverLease(providerKey); held && !current.dueForRenewal(m.now()) {
		return current, nil
	}
	fresh, err := m.request(ctx, providerKey, purpose)
	if err == nil {
		m.mu.Lock()
		m.server[providerKey] = fresh
		m.mu.Unlock()
		return fresh, nil
	}
	if held {
		// 续租没成功,旧租约还没到期:照用,下一次使用再续。
		return current, nil
	}
	return Lease{}, fmt.Errorf("provider %q: %w: %w", providerKey, rpcerror.ErrProviderLeaseUnavailable, err)
}

// Attach records the leases a desktop attached to one run (holder names it) and
// returns the function that drops them when the run ends. Calling it again is a
// no-op. A second Attach under the same holder replaces that holder's leases.
func (m *Manager) Attach(holder string, leases []Lease) (release func()) {
	now := m.now()
	m.mu.Lock()
	m.attachGen++
	gen := m.attachGen
	for _, lease := range leases {
		key := strings.TrimSpace(lease.ProviderKey)
		if key == "" || !lease.validAt(now) {
			continue
		}
		if lease.IssuedAt.IsZero() {
			lease.IssuedAt = now
		}
		lease.ProviderKey = key
		if m.attached[key] == nil {
			m.attached[key] = map[string]attachedLease{}
		}
		m.attached[key][holder] = attachedLease{Lease: lease, gen: gen}
	}
	m.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			for key, holders := range m.attached {
				if current, ok := holders[holder]; ok && current.gen == gen {
					delete(holders, holder)
				}
				if len(holders) == 0 {
					delete(m.attached, key)
				}
			}
		})
	}
}

// AttachedProviders returns, by provider key, the metadata desktops attached
// with still-valid leases to the runs in flight.
func (m *Manager) AttachedProviders() map[string]state.LLMProviderMeta {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]state.LLMProviderMeta{}
	for key, holders := range m.attached {
		for _, lease := range holders {
			if lease.validAt(now) && lease.Provider != nil {
				out[key] = *lease.Provider
				break
			}
		}
	}
	return out
}

// Revoke drops every lease (account-issued and attached) of the named providers
// at once; no names means every lease this process holds.
func (m *Manager) Revoke(providerKeys ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(providerKeys) == 0 {
		m.server = map[string]Lease{}
		m.attached = map[string]map[string]attachedLease{}
		return
	}
	for _, key := range providerKeys {
		key = strings.TrimSpace(key)
		delete(m.server, key)
		delete(m.attached, key)
	}
}

func (m *Manager) attachedLease(providerKey string) (Lease, bool) {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	var best Lease
	found := false
	for _, lease := range m.attached[providerKey] {
		if lease.validAt(now) && (!found || lease.ExpiresAt.After(best.ExpiresAt)) {
			best, found = lease.Lease, true
		}
	}
	return best, found
}

func (m *Manager) serverLease(providerKey string) (Lease, bool) {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	lease, ok := m.server[providerKey]
	if ok && !lease.validAt(now) {
		delete(m.server, providerKey)
		return Lease{}, false
	}
	return lease, ok
}

func (m *Manager) requestGate(providerKey string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	gate, ok := m.requesting[providerKey]
	if !ok {
		gate = &sync.Mutex{}
		m.requesting[providerKey] = gate
	}
	return gate
}

// request asks the account server for a lease. Any failure — not logged in, the
// server unreachable, an endpoint it does not serve yet (404), a refusal, or an
// unusable answer — is an error; the caller maps all of them to "no lease".
func (m *Manager) request(ctx context.Context, providerKey, purpose string) (Lease, error) {
	var baseURL, token string
	if m.serverURL != nil {
		baseURL = strings.TrimRight(strings.TrimSpace(m.serverURL()), "/")
	}
	if m.accessToken != nil {
		token = strings.TrimSpace(m.accessToken())
	}
	if baseURL == "" || token == "" {
		return Lease{}, errors.New("account endpoint or credential missing")
	}
	body, err := json.Marshal(syncwire.LeaseRequest{ProviderKey: providerKey, Purpose: purpose})
	if err != nil {
		return Lease{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+syncwire.PathProviderLease, bytes.NewReader(body))
	if err != nil {
		return Lease{}, fmt.Errorf("build lease request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return Lease{}, fmt.Errorf("lease request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return Lease{}, fmt.Errorf("read lease response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return Lease{}, fmt.Errorf("lease endpoint returned %s", resp.Status)
	}
	var response syncwire.LeaseResponse
	if err := cagoenvelope.Decode(payload, &response); err != nil {
		return Lease{}, fmt.Errorf("parse lease response: %w", err)
	}
	now := m.now()
	lease := Lease{
		ProviderKey: strings.TrimSpace(response.ProviderKey), APIKey: response.APIKey, BaseURL: response.BaseURL,
		IssuedAt: now, ExpiresAt: time.UnixMilli(response.ExpiresAt),
	}
	if lease.ProviderKey != providerKey {
		return Lease{}, errors.New("lease response names another provider")
	}
	if !lease.validAt(now) {
		return Lease{}, errors.New("lease response carries no usable credential")
	}
	return lease, nil
}

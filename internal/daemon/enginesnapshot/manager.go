// Package enginesnapshot pulls the account engine subset needed by agentred.
// It deliberately does not implement the workspace sync_objects protocol.
package enginesnapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/devicefacts"
	"github.com/agentre-hub/agentre/internal/pkg/cagoenvelope"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

const (
	defaultHTTPTimeout = 15 * time.Second
	maxResponseBytes   = 4 << 20
)

// HTTPDoer is the narrow HTTP seam used for snapshot pulls.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Options supplies account identity and persistence owned by the daemon.
type Options struct {
	State       *state.State
	ServerURL   func() string
	AccessToken func() string
	HTTPClient  HTTPDoer
	Logf        func(format string, args ...any)
	// OnApplied runs after every successfully applied snapshot (the account's
	// engines may have changed, so device facts re-check). Nil means no-op.
	OnApplied func()
}

// Manager owns the latest successful account snapshot. Provider metadata is
// persisted through state.State (it carries no key — keys only ever arrive as
// in-memory leases); engine profiles and this device's per-runtime CLI paths
// stay in memory only — absolute paths and arbitrary argv never enter
// state.json.
type Manager struct {
	state       *state.State
	serverURL   func() string
	accessToken func() string
	httpClient  HTTPDoer
	logf        func(format string, args ...any)
	onApplied   func()

	pullMu sync.Mutex
	mu     sync.RWMutex
	ready  bool
	// paths 是这台设备按运行时类型的 CLI 路径覆盖。
	paths map[string]string
	// profiles 是账号级引擎配置,按 sync ID 存放。它只在内存里 —— config 可能带任意
	// argv(含凭据),落盘会把执行期秘密写进 state.json。
	profiles map[string]Profile
}

// Profile 是快照里一份账号级引擎配置:类型、独占设置(整份 syncwire.AgentBackendConfig)
// 加上它绑定的供应商与固定模型。账号侧的派发(控制台、agrctl)只点名引擎配置,这一轮
// 跟随配置时要跑的供应商与模型只能从这里来。
type Profile struct {
	syncwire.AgentBackendConfig
	Type string
	Name string
	// ProviderKey / ModelKey 是配置绑定的供应商与固定模型(皆空 = CLI 自身登录态;
	// ModelKey 空 = 跟随供应商默认模型)。
	ProviderKey string
	ModelKey    string
	// ReasoningEffort / EnvJSON 是配置自己的思考力度与透传环境变量(空 = 走模型 / CLI
	// 默认、不透传);同样只在内存里。
	ReasoningEffort string
	EnvJSON         string
}

// New constructs an engine snapshot manager.
func New(opts Options) *Manager {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	logf := opts.Logf
	if logf == nil {
		logf = log.Printf
	}
	return &Manager{
		state: opts.State, serverURL: opts.ServerURL, accessToken: opts.AccessToken,
		httpClient: client, logf: logf, onApplied: opts.OnApplied, paths: map[string]string{},
		profiles: map[string]Profile{},
	}
}

// Pull fetches and atomically applies one complete snapshot. Any fetch, decode,
// validation, or persistence failure leaves provider metadata, CLI paths and
// engine profiles at the previous successful version.
func (m *Manager) Pull(ctx context.Context) error {
	m.pullMu.Lock()
	defer m.pullMu.Unlock()
	if m.state == nil || !m.state.IsLoggedIn() {
		return errors.New("engine snapshot unavailable: daemon is not logged in")
	}
	baseURL, token := m.credentials()
	if baseURL == "" || token == "" {
		return errors.New("engine snapshot unavailable: account endpoint or credential missing")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, appendEndpoint(baseURL, syncwire.PathEngineSnapshot), nil)
	if err != nil {
		return fmt.Errorf("build engine snapshot request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("engine snapshot request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("read engine snapshot response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("engine snapshot endpoint returned %s", resp.Status)
	}
	var snapshot syncwire.EngineSnapshot
	if err := cagoenvelope.Decode(payload, &snapshot); err != nil {
		return fmt.Errorf("parse engine snapshot response: %w", err)
	}

	providers := make(map[string]state.LLMProviderMeta, len(snapshot.Providers))
	for _, provider := range snapshot.Providers {
		key := strings.TrimSpace(provider.ProviderKey)
		if key == "" {
			return errors.New("engine snapshot contains a provider without provider_key")
		}
		models := make([]state.LLMModelMeta, 0, len(provider.Models))
		for _, model := range provider.Models {
			models = append(models, state.LLMModelMeta{
				ModelKey: model.ModelKey, ModelID: model.ModelID, Name: model.Name, Enabled: model.Enabled,
				ContextWindow: optionalInt(model.ContextWindow), MaxOutput: optionalInt(model.MaxOutput),
			})
		}
		providers[key] = state.LLMProviderMeta{
			Name: provider.Name, Type: provider.Type, BaseURL: provider.BaseURL,
			DefaultModelKey: provider.DefaultModelKey, Models: models,
		}
	}
	paths := make(map[string]string, len(snapshot.CLIPaths))
	for _, override := range snapshot.CLIPaths {
		if runtime := strings.TrimSpace(override.Runtime); runtime != "" {
			paths[runtime] = override.CLIPath
		}
	}
	profiles := make(map[string]Profile, len(snapshot.EngineProfiles))
	for _, p := range snapshot.EngineProfiles {
		if key := strings.TrimSpace(p.SyncID); key != "" {
			profiles[key] = Profile{
				AgentBackendConfig: cloneBackendConfig(p.Config),
				Type:               strings.TrimSpace(p.Type), Name: p.Name,
				ProviderKey: strings.TrimSpace(p.ProviderKey), ModelKey: strings.TrimSpace(p.ModelKey),
				ReasoningEffort: strings.TrimSpace(p.ReasoningEffort), EnvJSON: strings.TrimSpace(p.EnvJSON),
			}
		}
	}

	if err := m.state.ReplaceLLMProviders(providers); err != nil {
		return fmt.Errorf("persist engine snapshot: %w", err)
	}
	m.mu.Lock()
	m.paths = paths
	m.profiles = profiles
	m.ready = true
	m.mu.Unlock()
	if m.onApplied != nil {
		m.onApplied()
	}
	return nil
}

func optionalInt(v int) *int64 {
	if v == 0 {
		return nil
	}
	n := int64(v)
	return &n
}

// ResolveCLIPath returns this device's CLI path override for a runtime type.
// The boolean is false until a claimed daemon has applied its first snapshot;
// after that, a missing runtime authoritatively means PATH and returns ("", true).
func (m *Manager) ResolveCLIPath(runtimeType string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.ready {
		return "", false
	}
	return m.paths[strings.TrimSpace(runtimeType)], true
}

// ResolveEngineProfile returns the authoritative account engine profile for a
// sync ID and whether the latest successful snapshot carries that entry. An
// absent entry (or an empty sync ID, or before the first successful snapshot)
// returns false — callers must keep a directly-supplied profile rather than
// clearing it, because a paired desktop request is not necessarily an
// account-scoped browser/cloud shell request.
//
// The returned profile is a defensive copy: the snapshot is shared across turns,
// so a caller mutating slices or RawMessage must not reach the stored copy.
func (m *Manager) ResolveEngineProfile(syncID string) (Profile, bool) {
	key := strings.TrimSpace(syncID)
	if key == "" {
		return Profile{}, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.profiles[key]
	if !ok {
		return Profile{}, false
	}
	p.AgentBackendConfig = cloneBackendConfig(p.AgentBackendConfig)
	return p, true
}

// Engines lists the account engine profiles that carry device facts — ACP,
// Hermes and OpenClaw — keyed by the profile's sync ID and sorted by it. The
// profile's own type decides; empty before the first successful snapshot.
func (m *Manager) Engines() []devicefacts.Engine {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]devicefacts.Engine, 0, len(m.profiles))
	for syncID, p := range m.profiles {
		switch p.Type {
		case "acp":
			out = append(out, devicefacts.Engine{SyncID: syncID, Runtime: "acp", ACPCommand: p.ACPCommand})
		case "hermes":
			out = append(out, devicefacts.Engine{SyncID: syncID, Runtime: "hermes", HermesURL: p.HermesURL})
		case "openclaw":
			out = append(out, devicefacts.Engine{SyncID: syncID, Runtime: "openclaw", OpenClawGatewayURL: p.OpenClawGatewayURL})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SyncID < out[j].SyncID })
	return out
}

// cloneBackendConfig deep-copies the mutable fields (the argv slice and the
// nested model-routes JSON) so a resolved config never aliases the stored one.
func cloneBackendConfig(cfg syncwire.AgentBackendConfig) syncwire.AgentBackendConfig {
	if len(cfg.ACPArgs) > 0 {
		cfg.ACPArgs = append([]string(nil), cfg.ACPArgs...)
	}
	if len(cfg.ModelRoutes) > 0 {
		cfg.ModelRoutes = append(json.RawMessage(nil), cfg.ModelRoutes...)
	}
	return cfg
}

// PullAsync isolates a trigger failure from relay handling and running rounds.
func (m *Manager) PullAsync(ctx context.Context, cause string) {
	go func() {
		if err := m.Pull(ctx); err != nil && ctx.Err() == nil {
			m.logf("enginesnapshot.Manager.Pull: pull failed; keeping previous snapshot cause=%s err=%v", cause, err)
		}
	}()
}

func (m *Manager) credentials() (string, string) {
	var baseURL, token string
	if m.serverURL != nil {
		baseURL = strings.TrimRight(strings.TrimSpace(m.serverURL()), "/")
	}
	if m.accessToken != nil {
		token = strings.TrimSpace(m.accessToken())
	}
	return baseURL, token
}

func appendEndpoint(baseURL, endpoint string) string {
	return strings.TrimRight(baseURL, "/") + endpoint
}

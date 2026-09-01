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
	"strings"
	"sync"
	"time"

	"github.com/agentre-hub/agentre/internal/daemon/state"
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
}

// Manager owns the latest successful account snapshot. Providers are persisted
// through state.State; per-device CLI overlays stay in memory only.
type Manager struct {
	state       *state.State
	serverURL   func() string
	accessToken func() string
	httpClient  HTTPDoer
	logf        func(format string, args ...any)

	pullMu sync.Mutex
	mu     sync.RWMutex
	ready  bool
	paths  map[string]string
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
		httpClient: client, logf: logf, paths: map[string]string{},
	}
}

type snapshotModel struct {
	ModelKey      string `json:"model_key"`
	ModelID       string `json:"model_id"`
	Name          string `json:"name"`
	Enabled       bool   `json:"enabled"`
	ContextWindow *int64 `json:"context_window,omitempty"`
	MaxOutput     *int64 `json:"max_output,omitempty"`
}

type snapshotProvider struct {
	ProviderKey     string          `json:"provider_key"`
	Name            string          `json:"name"`
	Type            string          `json:"type"`
	BaseURL         string          `json:"base_url"`
	APIKey          string          `json:"api_key"`
	DefaultModelKey string          `json:"default_model_key"`
	Models          []snapshotModel `json:"models"`
}

type snapshotCLIOverlay struct {
	BackendSyncID string `json:"backend_sync_id"`
	CLIPath       string `json:"cli_path"`
}

type snapshotResponse struct {
	Providers   []snapshotProvider   `json:"providers"`
	CLIOverlays []snapshotCLIOverlay `json:"cli_overlays"`
}

// Pull fetches and atomically applies one complete snapshot. Any fetch, decode,
// validation, or persistence failure leaves both providers and overlays at the
// previous successful version.
func (m *Manager) Pull(ctx context.Context) error {
	m.pullMu.Lock()
	defer m.pullMu.Unlock()
	if m.state == nil || !m.state.IsClaimed() {
		return errors.New("engine snapshot unavailable: daemon is not claimed")
	}
	baseURL, token := m.credentials()
	if baseURL == "" || token == "" {
		return errors.New("engine snapshot unavailable: account endpoint or credential missing")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, appendEndpoint(baseURL, "/v1/engine/snapshot"), nil)
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
	var snapshot snapshotResponse
	if err := decodeEnvelope(payload, &snapshot); err != nil {
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
				ContextWindow: model.ContextWindow, MaxOutput: model.MaxOutput,
			})
		}
		providers[key] = state.LLMProviderMeta{
			Name: provider.Name, Type: provider.Type, BaseURL: provider.BaseURL, APIKey: provider.APIKey,
			DefaultModelKey: provider.DefaultModelKey, Models: models,
		}
	}
	paths := make(map[string]string, len(snapshot.CLIOverlays))
	for _, overlay := range snapshot.CLIOverlays {
		if key := strings.TrimSpace(overlay.BackendSyncID); key != "" {
			paths[key] = overlay.CLIPath
		}
	}

	if err := m.state.ReplaceLLMProviders(providers); err != nil {
		return fmt.Errorf("persist engine snapshot: %w", err)
	}
	m.mu.Lock()
	m.paths = paths
	m.ready = true
	m.mu.Unlock()
	return nil
}

// ResolveCLIPath returns the per-device overlay for an account backend. The
// boolean is false until a claimed daemon has applied its first snapshot; after
// that, a missing key authoritatively means PATH and returns ("", true).
func (m *Manager) ResolveCLIPath(backendSyncID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.ready {
		return "", false
	}
	return m.paths[strings.TrimSpace(backendSyncID)], true
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

func decodeEnvelope(payload []byte, target any) error {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return err
	}
	if len(envelope.Data) != 0 && string(envelope.Data) != "null" {
		return json.Unmarshal(envelope.Data, target)
	}
	return json.Unmarshal(payload, target)
}

func appendEndpoint(baseURL, endpoint string) string {
	return strings.TrimRight(baseURL, "/") + endpoint
}

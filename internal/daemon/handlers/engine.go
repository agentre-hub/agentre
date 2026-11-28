package handlers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/cliprober"
	"github.com/agentre-hub/agentre/internal/pkg/llmurl"
	"github.com/agentre-hub/agentre/internal/pkg/wireinbound"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// EngineProviderPort 交出「这个 providerKey + modelKey 指向哪个上游」。
//
// 它是端口而不是直接读 StatePort,因为两种执行端的供应商配置**不在同一处**:
// agentred 存在 daemon state 里,桌面端存在库里(llm_provider_svc)。engine.test 的
// 其余部分 —— 探测、计时,以及「未配置」与「上游失败」这两句话 —— 两侧必须逐字
// 相同(控制台把 message 原样显示给用户),所以那一段留在这里共用,只有取配置这一跳
// 换成端口。
//
// ok=false 表示「没配好」而不是出错:找不到、未启用、没有默认模型,在调用方眼里
// 都是同一件事 —— 去把它配上。
type EngineProviderPort interface {
	ProviderAndModel(ctx context.Context, providerKey, modelKey string) (llmurl.Provider, string, bool)
}

// ProviderCredential is the key (and, when the lease names one, the endpoint)
// an account provider runs with. Secret — never log or return it.
type ProviderCredential struct {
	APIKey  string
	BaseURL string
}

// ProviderCredentialPort hands out an account provider's credential for one
// purpose (syncwire.LeasePurpose*): agentred's state holds account providers as
// metadata only, so their key comes from an in-memory lease. It fails with an
// error wrapping rpcerror.ErrProviderLeaseUnavailable when none can be had.
type ProviderCredentialPort interface {
	ProviderCredential(ctx context.Context, providerKey, purpose string) (ProviderCredential, error)
}

// EngineDeps supplies the daemon-local capabilities used by engine RPCs.
type EngineDeps struct {
	State StatePort
	// Credentials 给 state 里账号供应商的 key(租约);本机添加的供应商自带 key 不经它。
	// nil = 账号供应商一律拿不到 key。
	Credentials ProviderCredentialPort
	// Providers 不给时按 State 兜底(agentred 的装配),与 ScanAllCLIs 同一条规矩。
	Providers   EngineProviderPort
	HTTPClient  llmurl.HTTPDoer
	ScanAllCLIs func() []cliprober.CLIProbeResult
}

// EngineHandlers groups the engine.* RPC methods. It only reads credentials
// from daemon state; none of its response types contain a provider credential.
type EngineHandlers struct {
	state       StatePort
	stateSource stateProviders
	providers   EngineProviderPort
	// stateBacked: 供应商取自 daemon state(agentred)。桌面注入自己的端口时,
	// 「不在快照里」没有意义,保持原来的 OK:false 话术。
	stateBacked bool
	probes      *llmurl.Client
	scanAllCLIs func() []cliprober.CLIProbeResult
}

// NewEngineHandlers constructs the engine RPC handlers.
func NewEngineHandlers(deps EngineDeps) *EngineHandlers {
	scan := deps.ScanAllCLIs
	if scan == nil {
		scan = cliprober.ScanAllCLIs
	}
	fallback := stateProviders{state: deps.State, credentials: deps.Credentials}
	providers := deps.Providers
	stateBacked := providers == nil
	if providers == nil {
		providers = fallback
	}
	return &EngineHandlers{
		state:       deps.State,
		stateSource: fallback,
		providers:   providers,
		stateBacked: stateBacked,
		probes:      llmurl.NewClient(deps.HTTPClient),
		scanAllCLIs: scan,
	}
}

// EngineTestParams is the engine.test request payload.
type EngineTestParams struct {
	ProviderKey string `json:"providerKey"`
	ModelKey    string `json:"modelKey,omitempty"`
}

// EngineTestResult is the privacy-safe engine.test response.
type EngineTestResult struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	LatencyMs *int64 `json:"latencyMs,omitempty"`
}

// EngineDiscoverParams is the engine.discover request payload.
type EngineDiscoverParams struct {
	ProviderKey string `json:"providerKey"`
}

// EngineModel is one privacy-safe discovered model.
type EngineModel struct {
	ModelID string `json:"modelId"`
	Name    string `json:"name"`
}

// EngineDiscoverResult is the engine.discover response.
type EngineDiscoverResult struct {
	Models []EngineModel `json:"models"`
}

// EngineScanItem is one privacy-safe CLI scan result.
type EngineScanItem struct {
	BackendType string `json:"backendType"`
	Status      string `json:"status"`
}

// EngineScanResult is the engine.scan response.
type EngineScanResult struct {
	Items []EngineScanItem `json:"items"`
}

// Test runs one minimal upstream request using the provider held in daemon
// state. Provider and model keys are inputs only and never occur in the result.
func (h *EngineHandlers) Test(ctx context.Context, params EngineTestParams) (EngineTestResult, error) {
	if h.stateBacked {
		// 账号供应商只差一份租约时,与「没配好」分开报:稍后账号服务器连上就好了。
		if _, err := h.stateSource.provider(ctx, params.ProviderKey, syncwire.LeasePurposeTest); errors.Is(err, rpcerror.ErrProviderLeaseUnavailable) {
			return EngineTestResult{}, rpcerror.ErrProviderLeaseUnavailable
		}
	}
	provider, modelID, ok := h.providers.ProviderAndModel(withPurpose(ctx, syncwire.LeasePurposeTest), params.ProviderKey, params.ModelKey)
	if !ok {
		if h.stateBacked && !h.inSnapshot(params.ProviderKey) {
			return EngineTestResult{}, rpcerror.ErrProviderMissing
		}
		return EngineTestResult{OK: false, Message: "provider or model is not configured"}, nil
	}
	started := time.Now()
	err := h.probes.Test(ctx, provider, modelID)
	latencyMs := time.Since(started).Milliseconds()
	result := EngineTestResult{LatencyMs: &latencyMs}
	if err != nil {
		result.Message = err.Error()
		return result, nil //nolint:nilerr // upstream failure is returned as the caller-visible OK:false probe result
	}
	result.OK = true
	result.Message = "connection succeeded"
	return result, nil
}

// Discover lists the upstream models for a provider held in daemon state.
func (h *EngineHandlers) Discover(ctx context.Context, params EngineDiscoverParams) (EngineDiscoverResult, error) {
	provider, err := h.stateSource.provider(ctx, params.ProviderKey, syncwire.LeasePurposeDiscover)
	if err != nil {
		if !h.inSnapshot(params.ProviderKey) {
			return EngineDiscoverResult{}, rpcerror.ErrProviderMissing
		}
		if errors.Is(err, rpcerror.ErrProviderLeaseUnavailable) {
			return EngineDiscoverResult{}, rpcerror.ErrProviderLeaseUnavailable
		}
		return EngineDiscoverResult{}, errors.New("provider is not configured")
	}
	models, err := h.probes.Discover(ctx, provider)
	if err != nil {
		return EngineDiscoverResult{}, err
	}
	result := EngineDiscoverResult{Models: make([]EngineModel, 0, len(models))}
	for _, model := range models {
		result.Models = append(result.Models, EngineModel{ModelID: model.ModelID, Name: model.Name})
	}
	return result, nil
}

// Scan checks local CLI availability. Paths are deliberately discarded: the
// browser only receives recognized or unchecked status.
func (h *EngineHandlers) Scan(context.Context) (EngineScanResult, error) {
	probes := h.scanAllCLIs()
	items := make([]EngineScanItem, 0, len(probes))
	for _, probe := range probes {
		items = append(items, EngineScanItem{BackendType: probe.BackendType, Status: wireinbound.EngineScanStatus(probe.Found)})
	}
	return EngineScanResult{Items: items}, nil
}

// inSnapshot 报告 daemon 这里有没有这个供应商(账号快照或本机添加)——「还没同步到这台
// 设备」与「配置不完整」在调用方眼里是两件事,前者稍后重试即可。
func (h *EngineHandlers) inSnapshot(key string) bool {
	if h.state == nil {
		return false
	}
	_, ok := h.state.Snapshot().LookupProvider(strings.TrimSpace(key))
	return ok
}

// purposeKey 把申请租约的用途带过 EngineProviderPort(它的签名不认用途)。
type purposeKey struct{}

func withPurpose(ctx context.Context, purpose string) context.Context {
	return context.WithValue(ctx, purposeKey{}, purpose)
}

func purposeFrom(ctx context.Context) string {
	if purpose, ok := ctx.Value(purposeKey{}).(string); ok && purpose != "" {
		return purpose
	}
	return syncwire.LeasePurposeTest
}

// stateProviders 是 agentred 这一侧的供应商来源:daemon state 里的元数据,key 来自本机
// 添加时给的那份或一份租约。
type stateProviders struct {
	state       StatePort
	credentials ProviderCredentialPort
}

var errProviderNotConfigured = errors.New("provider is not configured")

func (h stateProviders) provider(ctx context.Context, key, purpose string) (llmurl.Provider, error) {
	key = strings.TrimSpace(key)
	if key == "" || h.state == nil {
		return llmurl.Provider{}, errProviderNotConfigured
	}
	entry, ok := h.state.Snapshot().LookupProvider(key)
	if !ok {
		return llmurl.Provider{}, errProviderNotConfigured
	}
	apiKey, baseURL := entry.APIKey, entry.BaseURL
	if !entry.Local {
		if h.credentials == nil {
			return llmurl.Provider{}, rpcerror.ErrProviderLeaseUnavailable
		}
		credential, err := h.credentials.ProviderCredential(ctx, key, purpose)
		if err != nil {
			return llmurl.Provider{}, err
		}
		apiKey = credential.APIKey
		if credential.BaseURL != "" {
			baseURL = credential.BaseURL
		}
	}
	if strings.TrimSpace(apiKey) == "" {
		return llmurl.Provider{}, errProviderNotConfigured
	}
	return llmurl.Provider{Type: entry.Type, BaseURL: baseURL, APIKey: apiKey}, nil
}

func (h stateProviders) ProviderAndModel(ctx context.Context, providerKey, modelKey string) (llmurl.Provider, string, bool) {
	provider, err := h.provider(ctx, providerKey, purposeFrom(ctx))
	if err != nil {
		return llmurl.Provider{}, "", false
	}
	entry, _ := h.state.Snapshot().LookupProvider(strings.TrimSpace(providerKey))
	meta := entry.LLMProviderMeta
	modelKey = strings.TrimSpace(modelKey)
	if modelKey == "" {
		modelKey = meta.DefaultModelKey
	}
	for _, model := range meta.Models {
		if model.ModelKey == modelKey && model.Enabled && strings.TrimSpace(model.ModelID) != "" {
			return provider, model.ModelID, true
		}
	}
	// A provider added locally with one Model string (no catalog) keeps its
	// executable configuration.
	if modelKey == "" && strings.TrimSpace(meta.Model) != "" {
		return provider, meta.Model, true
	}
	return llmurl.Provider{}, "", false
}

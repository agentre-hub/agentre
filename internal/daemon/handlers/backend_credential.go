package handlers

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"time"

	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/hermes/hermesauth"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/hermes/hermesgateway"
	"github.com/agentre-hub/agentre/internal/pkg/backendcred"
	"github.com/agentre-hub/agentre/internal/pkg/openclawgateway"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// backendCredentialProbeTimeout bounds one test connection, matching the
// desktop's own test timeout.
const backendCredentialProbeTimeout = 30 * time.Second

// Hermes result codes. They are the desktop's test-connection vocabulary
// (agent_backend_svc) so both hosts' results localize through one table.
const (
	hermesCodeLoginRequired       = "HERMES_LOGIN_REQUIRED"
	hermesCodeLoginExpired        = "HERMES_LOGIN_EXPIRED"
	hermesCodeInvalidCredentials  = "HERMES_INVALID_CREDENTIALS"
	hermesCodeRateLimited         = "HERMES_RATE_LIMITED"
	hermesCodeProviderUnsupported = "HERMES_PROVIDER_UNSUPPORTED"
	hermesCodeProviderUnavailable = "HERMES_PROVIDER_UNAVAILABLE"
	hermesCodeUnreachable         = "HERMES_UNREACHABLE"
)

// BackendCredentialState is the part of *state.State the credential handlers
// persist through. Every write lands in state.json before it is visible.
type BackendCredentialState interface {
	BackendCredential(account string) (string, bool)
	SetBackendCredential(account, secret string) error
	DeleteBackendCredential(account string) error
	HermesIdentity(normalizedURL string) (state.HermesIdentity, bool)
	SetHermesIdentity(normalizedURL string, identity state.HermesIdentity) error
	DeleteHermesIdentity(normalizedURL string) error
}

// BackendCredentialDeps wires the handlers. The probes are seams: nil uses the
// real Hermes gateway probe and OpenClaw Gateway probe.
type BackendCredentialDeps struct {
	State         BackendCredentialState
	ProbeHermes   func(ctx context.Context, req hermesgateway.ProbeRequest) (string, error)
	ProbeOpenClaw func(ctx context.Context, config openclawgateway.Config, selection openclawgateway.ProbeSelection) (*openclawgateway.ProbeResult, error)
}

// BackendCredentialHandlers answers the device-local backend credential
// operations for the Hermes and OpenClaw backends bound to this daemon.
//
// Plaintext credentials only ever travel toward this device: tokens and
// passwords arrive in requests, the Gateway token and the Hermes refresh token
// are the only ones kept (in state.json), and no response or log line carries
// any of them back out.
type BackendCredentialHandlers struct {
	state         BackendCredentialState
	store         backendcred.Store
	hermes        *backendcred.HermesCredentials
	probeHermes   func(ctx context.Context, req hermesgateway.ProbeRequest) (string, error)
	probeOpenClaw func(ctx context.Context, config openclawgateway.Config, selection openclawgateway.ProbeSelection) (*openclawgateway.ProbeResult, error)
}

// NewBackendCredentialHandlers constructs the handlers. One instance must serve
// every connection: it holds the Hermes access-token cache.
func NewBackendCredentialHandlers(deps BackendCredentialDeps) *BackendCredentialHandlers {
	store := stateCredentialStore{state: deps.State}
	h := &BackendCredentialHandlers{
		state:         deps.State,
		store:         store,
		hermes:        backendcred.NewHermesCredentials(func() backendcred.Store { return store }),
		probeHermes:   deps.ProbeHermes,
		probeOpenClaw: deps.ProbeOpenClaw,
	}
	if h.probeHermes == nil {
		h.probeHermes = hermesgateway.Probe
	}
	if h.probeOpenClaw == nil {
		h.probeOpenClaw = openclawgateway.Probe
	}
	return h
}

// stateCredentialStore adapts state.json to backendcred.Store.
type stateCredentialStore struct {
	state BackendCredentialState
}

func (s stateCredentialStore) Get(account string) (string, error) {
	secret, ok := s.state.BackendCredential(account)
	if !ok {
		return "", backendcred.ErrNotFound
	}
	return secret, nil
}

func (s stateCredentialStore) Set(account, secret string) error {
	return s.state.SetBackendCredential(account, secret)
}

func (s stateCredentialStore) Delete(account string) error {
	return s.state.DeleteBackendCredential(account)
}

func invalidParams(message string) error {
	return &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: message}
}

func normalizedHermesURL(raw string) (string, error) {
	base, err := agent_backend_entity.NormalizeHermesURL(raw)
	if err != nil {
		return "", invalidParams("invalid hermes url")
	}
	return base, nil
}

// Status reports whether a credential is on this device. It never says whether
// a Hermes login has expired: only a connection finds that out.
func (h *BackendCredentialHandlers) Status(_ context.Context, request *agentrewire.BackendCredentialStatusRequest) (*agentrewire.BackendCredentialStatusResponse, error) {
	switch agent_backend_entity.BackendType(request.GetBackendType()) {
	case agent_backend_entity.TypeOpenClaw:
		account := backendcred.OpenClawTokenAccount(request.GetSyncId())
		if account == "" {
			return nil, invalidParams("sync id required")
		}
		_, saved := h.state.BackendCredential(account)
		return &agentrewire.BackendCredentialStatusResponse{OpenclawTokenSaved: saved}, nil
	case agent_backend_entity.TypeHermes:
		base, err := normalizedHermesURL(request.GetHermesUrl())
		if err != nil {
			return nil, err
		}
		response := &agentrewire.BackendCredentialStatusResponse{}
		if _, ok := h.state.BackendCredential(backendcred.HermesAccount(base)); !ok {
			return response, nil
		}
		response.HermesLoggedIn = true
		if identity, ok := h.state.HermesIdentity(base); ok {
			response.HermesProvider = identity.Provider
			response.HermesUserId = identity.UserID
		}
		return response, nil
	default:
		return nil, invalidParams("backend type must be openclaw or hermes")
	}
}

// SetOpenClawToken saves or clears the Gateway token of one backend, keyed by
// its sync_id.
func (h *BackendCredentialHandlers) SetOpenClawToken(ctx context.Context, request *agentrewire.OpenClawTokenSetRequest) (*agentrewire.OpenClawTokenSetResponse, error) {
	account := backendcred.OpenClawTokenAccount(request.GetSyncId())
	if account == "" {
		return nil, invalidParams("sync id required")
	}
	token := strings.TrimSpace(request.GetToken())
	switch {
	case request.GetClear() && token != "":
		return nil, invalidParams("token and clear are exclusive")
	case request.GetClear():
		if err := h.store.Delete(account); err != nil {
			logger.Ctx(ctx).Warn("handlers.BackendCredentialHandlers.SetOpenClawToken: clear failed",
				zap.String("syncId", request.GetSyncId()), zap.Error(err))
			return nil, err
		}
		return &agentrewire.OpenClawTokenSetResponse{TokenSaved: false}, nil
	case token == "":
		return nil, invalidParams("token required unless clearing")
	}
	if err := h.store.Set(account, token); err != nil {
		logger.Ctx(ctx).Warn("handlers.BackendCredentialHandlers.SetOpenClawToken: save failed",
			zap.String("syncId", request.GetSyncId()), zap.Error(err))
		return nil, err
	}
	return &agentrewire.OpenClawTokenSetResponse{TokenSaved: true}, nil
}

// HermesAuthProviders reads the serve's provider directory from this device.
func (h *BackendCredentialHandlers) HermesAuthProviders(ctx context.Context, request *agentrewire.HermesAuthProvidersRequest) (*agentrewire.HermesAuthProvidersResponse, error) {
	base, err := normalizedHermesURL(request.GetHermesUrl())
	if err != nil {
		return nil, err
	}
	providers, err := hermesauth.ListAuthProviders(ctx, base, nil)
	if err != nil {
		if code := hermesResultCode(err); code != "" {
			return &agentrewire.HermesAuthProvidersResponse{Code: code}, nil
		}
		return nil, err
	}
	response := &agentrewire.HermesAuthProvidersResponse{Providers: make([]*agentrewire.HermesAuthProvider, 0, len(providers))}
	for _, provider := range providers {
		response.Providers = append(response.Providers, &agentrewire.HermesAuthProvider{
			Name: provider.Name, DisplayName: provider.DisplayName, SupportsPassword: provider.SupportsPassword,
		})
	}
	return response, nil
}

// HermesLogin runs the password login on this device. The password is used for
// this call only; the refresh token and the display identity are kept.
func (h *BackendCredentialHandlers) HermesLogin(ctx context.Context, request *agentrewire.HermesLoginRequest) (*agentrewire.HermesLoginResponse, error) {
	base, err := normalizedHermesURL(request.GetHermesUrl())
	if err != nil {
		return nil, err
	}
	identity, err := h.hermes.Login(ctx, backendcred.HermesLogin{
		BaseURL: base, Provider: request.GetProvider(), Username: request.GetUsername(), Password: request.GetPassword(),
	})
	if err != nil {
		if code := hermesResultCode(err); code != "" {
			return &agentrewire.HermesLoginResponse{Code: code}, nil
		}
		logger.Ctx(ctx).Warn("handlers.BackendCredentialHandlers.HermesLogin: login failed",
			zap.String("hermesUrl", base), zap.Error(err))
		return nil, err
	}
	if err := h.state.SetHermesIdentity(base, state.HermesIdentity{Provider: identity.Provider, UserID: identity.UserID}); err != nil {
		logger.Ctx(ctx).Warn("handlers.BackendCredentialHandlers.HermesLogin: record identity failed",
			zap.String("hermesUrl", base), zap.Error(err))
		return nil, err
	}
	return &agentrewire.HermesLoginResponse{Provider: identity.Provider, UserId: identity.UserID}, nil
}

// HermesLogout drops this device's credential and identity for the serve.
func (h *BackendCredentialHandlers) HermesLogout(ctx context.Context, request *agentrewire.HermesLogoutRequest) (*agentrewire.HermesLogoutResponse, error) {
	base, err := normalizedHermesURL(request.GetHermesUrl())
	if err != nil {
		return nil, err
	}
	if err := h.hermes.Logout(base); err != nil {
		logger.Ctx(ctx).Warn("handlers.BackendCredentialHandlers.HermesLogout: drop credential failed",
			zap.String("hermesUrl", base), zap.Error(err))
		return nil, err
	}
	if err := h.state.DeleteHermesIdentity(base); err != nil {
		return nil, err
	}
	return &agentrewire.HermesLogoutResponse{}, nil
}

// TestConnection connects to the backend once with this device's credentials.
func (h *BackendCredentialHandlers) TestConnection(ctx context.Context, request *agentrewire.BackendConnectionTestRequest) (*agentrewire.BackendConnectionTestResponse, error) {
	switch agent_backend_entity.BackendType(request.GetBackendType()) {
	case agent_backend_entity.TypeHermes:
		return h.testHermes(ctx, request)
	case agent_backend_entity.TypeOpenClaw:
		return h.testOpenClaw(ctx, request), nil
	default:
		return nil, invalidParams("backend type must be openclaw or hermes")
	}
}

func (h *BackendCredentialHandlers) testHermes(ctx context.Context, request *agentrewire.BackendConnectionTestRequest) (*agentrewire.BackendConnectionTestResponse, error) {
	base, err := normalizedHermesURL(request.GetHermesUrl())
	if err != nil {
		return nil, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, backendCredentialProbeTimeout)
	defer cancel()
	start := time.Now()
	_, err = h.probeHermes(probeCtx, hermesgateway.ProbeRequest{
		URL: base, AuthProvider: request.GetHermesAuthProvider(), Credentials: h.hermes,
	})
	latency := time.Since(start).Milliseconds()
	if err != nil {
		response := &agentrewire.BackendConnectionTestResponse{Code: hermesResultCode(err), LatencyMs: latency}
		if response.Code == "" {
			response.Message = err.Error()
		}
		return response, nil
	}
	return &agentrewire.BackendConnectionTestResponse{Ok: true, LatencyMs: latency}, nil
}

func (h *BackendCredentialHandlers) testOpenClaw(ctx context.Context, request *agentrewire.BackendConnectionTestRequest) *agentrewire.BackendConnectionTestResponse {
	gatewayURL, err := agent_backend_entity.NormalizeOpenClawGatewayURL(request.GetOpenclawGatewayUrl())
	if err != nil {
		return &agentrewire.BackendConnectionTestResponse{Code: openClawURLCode(err)}
	}
	token := request.GetOpenclawToken()
	if token == "" {
		if account := backendcred.OpenClawTokenAccount(request.GetSyncId()); account != "" {
			token, _ = h.state.BackendCredential(account)
		}
	}
	identity, err := backendcred.OpenClawIdentity(h.store)
	if err != nil {
		logger.Ctx(ctx).Warn("handlers.BackendCredentialHandlers.TestConnection: openclaw identity unavailable", zap.Error(err))
		return &agentrewire.BackendConnectionTestResponse{Code: "OPENCLAW_SECRET_UNAVAILABLE"}
	}

	probeCtx, cancel := context.WithTimeout(ctx, backendCredentialProbeTimeout)
	defer cancel()
	start := time.Now()
	result, err := h.probeOpenClaw(probeCtx, openclawgateway.Config{
		URL: gatewayURL, Token: token, Identity: identity, ClientVersion: configs.Version, Platform: runtime.GOOS,
	}, openclawgateway.ProbeSelection{AgentID: request.GetOpenclawAgentId(), Model: request.GetOpenclawDefaultModel()})
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return &agentrewire.BackendConnectionTestResponse{
			Code: openClawResultCode(err), Message: redactSecret(err.Error(), token), LatencyMs: latency,
		}
	}
	response := &agentrewire.BackendConnectionTestResponse{
		Ok: true, LatencyMs: latency, GatewayVersion: result.GatewayVersion, Protocol: int32(result.Protocol),
		GrantedScopes: result.GrantedScopes, Methods: result.Methods, Events: result.Events,
		OpenclawAgents: make([]*agentrewire.OpenClawAgentOption, 0, len(result.Agents)),
		OpenclawModels: make([]*agentrewire.OpenClawModelOption, 0, len(result.Models)),
	}
	for _, agent := range result.Agents {
		response.OpenclawAgents = append(response.OpenclawAgents, &agentrewire.OpenClawAgentOption{
			Id: agent.ID, Name: agent.Name, PrimaryModel: agent.PrimaryModel, Fallbacks: agent.Fallbacks, IsDefault: agent.Default,
		})
	}
	for _, model := range result.Models {
		response.OpenclawModels = append(response.OpenclawModels, &agentrewire.OpenClawModelOption{
			Id: model.ID, Name: model.Name, Provider: model.Provider, Available: model.Available,
		})
	}
	return response
}

// redactSecret removes a credential from text that is about to leave the
// device. The Gateway client already redacts its own errors; this keeps the
// invariant independent of every producer doing so.
func redactSecret(text, secret string) string {
	if secret == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, "[redacted]")
}

// hermesResultCode maps the Hermes auth and gateway sentinels to the desktop's
// result codes; "" means the failure has no structured reason.
func hermesResultCode(err error) string {
	switch {
	case errors.Is(err, hermesauth.ErrLoginRequired):
		return hermesCodeLoginRequired
	case errors.Is(err, hermesauth.ErrLoginExpired):
		return hermesCodeLoginExpired
	case errors.Is(err, hermesauth.ErrInvalidCredentials):
		return hermesCodeInvalidCredentials
	case errors.Is(err, hermesauth.ErrAuthRateLimited):
		return hermesCodeRateLimited
	case errors.Is(err, hermesauth.ErrPasswordLoginUnsupported):
		return hermesCodeProviderUnsupported
	case errors.Is(err, hermesauth.ErrAuthProviderUnavailable):
		return hermesCodeProviderUnavailable
	case errors.Is(err, hermesauth.ErrAuthUnreachable), errors.Is(err, hermesgateway.ErrGatewayUnreachable):
		return hermesCodeUnreachable
	default:
		return ""
	}
}

// openClawAuthCodes are the Gateway RPC codes that mean the token was refused.
var openClawAuthCodes = map[string]struct{}{"AUTH_FAILED": {}, "UNAUTHORIZED": {}, "FORBIDDEN": {}}

// openClawResultCode maps a Gateway probe failure to the desktop's result codes.
func openClawResultCode(err error) string {
	var rpcErr *openclawgateway.RPCError
	switch {
	case errors.As(err, &rpcErr):
		code := strings.ToUpper(strings.TrimSpace(rpcErr.Code))
		reason := strings.ToLower(strings.TrimSpace(rpcErr.Reason))
		if code == "NOT_PAIRED" || reason == "not_paired" {
			return "OPENCLAW_NOT_PAIRED"
		}
		if _, ok := openClawAuthCodes[code]; ok || reason == "unauthorized" || strings.HasPrefix(strings.ToLower(rpcErr.Message), "unauthorized") {
			return "AUTH_FAILED"
		}
		if code == "" {
			return "OPENCLAW_CONNECTION_FAILED"
		}
		return code
	case errors.Is(err, openclawgateway.ErrRequiredScopeMissing):
		return "OPENCLAW_SCOPE_MISSING"
	case errors.Is(err, openclawgateway.ErrProtocolMismatch):
		return "OPENCLAW_PROTOCOL_MISMATCH"
	case errors.Is(err, openclawgateway.ErrSelectedAgentNotFound):
		return "OPENCLAW_AGENT_NOT_FOUND"
	case errors.Is(err, openclawgateway.ErrSelectedModelNotFound):
		return "OPENCLAW_MODEL_NOT_FOUND"
	case errors.Is(err, openclawgateway.ErrRequiredMethodMissing):
		return "OPENCLAW_METHOD_MISSING"
	case errors.Is(err, openclawgateway.ErrRequiredEventMissing):
		return "OPENCLAW_EVENT_MISSING"
	case errors.Is(err, context.Canceled):
		return "OPENCLAW_PROBE_CANCELED"
	case errors.Is(err, context.DeadlineExceeded):
		return "OPENCLAW_PROBE_TIMEOUT"
	default:
		return "OPENCLAW_CONNECTION_FAILED"
	}
}

// openClawURLCode maps a rejected Gateway URL to the desktop's draft codes.
func openClawURLCode(err error) string {
	switch {
	case errors.Is(err, agent_backend_entity.ErrOpenClawGatewayURLRequired):
		return "OPENCLAW_URL_REQUIRED"
	case errors.Is(err, agent_backend_entity.ErrOpenClawGatewayURLScheme):
		return "OPENCLAW_URL_SCHEME"
	case errors.Is(err, agent_backend_entity.ErrOpenClawGatewayURLHost):
		return "OPENCLAW_URL_HOST"
	case errors.Is(err, agent_backend_entity.ErrOpenClawGatewayURLCredentials):
		return "OPENCLAW_URL_CREDENTIALS"
	case errors.Is(err, agent_backend_entity.ErrOpenClawGatewayURLPlaintextRemote):
		return "OPENCLAW_URL_PLAINTEXT_REMOTE"
	default:
		return "OPENCLAW_URL_INVALID"
	}
}

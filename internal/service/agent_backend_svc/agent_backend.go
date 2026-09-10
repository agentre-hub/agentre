package agent_backend_svc

import (
	"context"
	"encoding/base64"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/httpgateway"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/pkg/openclawgateway"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
)

const (
	testProbeTimeout = 30 * time.Second
	fixedTestPrompt  = "Reply with the single word 'pong' and nothing else."
	testTokenTTL     = 60 * time.Second
)

var ErrOpenClawRemoteSecretUnavailable = errors.New("openclaw remote secret enrollment is unavailable")

// AgentBackendSvc Agent 后端应用服务。
type AgentBackendSvc interface {
	List(ctx context.Context, req *ListBackendsRequest) (*ListBackendsResponse, error)
	Create(ctx context.Context, req *CreateBackendRequest) (*CreateBackendResponse, error)
	CreateOpenClaw(ctx context.Context, req *CreateBackendRequest, token string) (*CreateBackendResponse, error)
	Update(ctx context.Context, req *UpdateBackendRequest) (*UpdateBackendResponse, error)
	UpdateOpenClaw(ctx context.Context, req *UpdateBackendRequest, token string, clearToken bool) (*UpdateBackendResponse, error)
	Delete(ctx context.Context, req *DeleteBackendRequest) (*DeleteBackendResponse, error)
	Test(ctx context.Context, req *TestBackendRequest) (*TestBackendResponse, error)
	TestOpenClaw(ctx context.Context, req *TestBackendRequest, token string) (*TestBackendResponse, error)
	CancelTest(ctx context.Context, req *CancelTestBackendRequest) (*CancelTestBackendResponse, error)
	ResolveCLIPath(ctx context.Context, req *ResolveCLIPathRequest) (*ResolveCLIPathResponse, error)
	ScanAndCreateAgentBackends(ctx context.Context, req *ScanAndCreateAgentBackendsRequest) (*ScanAndCreateAgentBackendsResponse, error)
	// ReclaimTombstonedBackends 回收无人引用且超过保留期的后端墓碑(决策 24)。
	ReclaimTombstonedBackends(ctx context.Context, req *ReclaimTombstonedBackendsRequest) (*ReclaimTombstonedBackendsResponse, error)
	// SurveyDanglingBackendReferences 巡检指向非 ACTIVE 后端的会话/执行目标引用，
	// 只报出、不擅自改写(决策 24)。
	SurveyDanglingBackendReferences(ctx context.Context, req *SurveyDanglingBackendReferencesRequest) (*SurveyDanglingBackendReferencesResponse, error)
	ClaimRelativeBackends(ctx context.Context) error
	GetCLIOverlay(ctx context.Context, req *GetCLIOverlayRequest) (*GetCLIOverlayResponse, error)
	SetCLIOverlay(ctx context.Context, req *SetCLIOverlayRequest) (*SetCLIOverlayResponse, error)
	ListCLIOverlays(ctx context.Context, req *ListCLIOverlaysRequest) (*ListCLIOverlaysResponse, error)
}

type agentBackendSvc struct {
	now     func() int64
	prober  Prober
	gateway httpgateway.TokenIssuer
	secrets keychain.Keychain

	openClawProbe openClawProbeFunc
	identityMu    sync.Mutex

	// remoteCLI 用于 device 非空场景拨远端 daemon 调 cli.* RPC。
	// nil → 走 realRemoteCLI 默认实现（dial → call → close）；单测注入 fake。
	remoteCLI remoteCLIPort

	// probes 维护「正在跑的测试」的 cancel 函数；key = 前端传入的 RequestID。
	// 用于实现 CancelTest：用户在 UI 上点取消时调 cancel，prober ctx 立刻 Done。
	probesMu sync.Mutex
	probes   map[string]context.CancelFunc
}

// 默认单例不预置 prober；Test() 在 s.prober == nil 时按 entity.Type 查
// proberRegistry。硬编码 builtinProber 会让其它 backend 错走 in-process 路径，
// s.prober 字段仅留给单测注入 mock 用。
var defaultAgentBackend AgentBackendSvc = &agentBackendSvc{
	now:    func() int64 { return time.Now().UnixMilli() },
	probes: map[string]context.CancelFunc{},
}

// RegisterGateway 由 bootstrap 注入 httpgateway 单例。
func RegisterGateway(g httpgateway.TokenIssuer) {
	if s, ok := defaultAgentBackend.(*agentBackendSvc); ok {
		s.gateway = g
	}
}

// AgentBackend 取默认服务单例。
func AgentBackend() AgentBackendSvc { return defaultAgentBackend }

// ClaimRelativeBackends remains the bootstrap hook for historical callers. The
// append-only migration now promotes rows in place: cloning by device or
// merging by type/name would change their stable sync identities.
func (s *agentBackendSvc) ClaimRelativeBackends(context.Context) error { return nil }

// ListCLIOverlays exposes only non-sensitive status data for all account
// overlays. Absolute paths stay behind GetCLIOverlay's desktop-only seam.
// setCLIOverlayIfAvailable preserves existing Wails create/update request
// shapes while moving their local path into a distinct overlay row. An
// uninitialized remote service only occurs in narrow unit-test composition;
// bootstrap always initializes it before public writes.
func (s *agentBackendSvc) setCLIOverlayIfAvailable(ctx context.Context, backendSyncID, cliPath string) error {
	if strings.TrimSpace(backendSyncID) == "" || remote_device_svc.Default() == nil {
		return nil
	}
	_, err := s.SetCLIOverlay(ctx, &SetCLIOverlayRequest{BackendSyncID: backendSyncID, CLIPath: cliPath})
	return err
}

func (s *agentBackendSvc) ListCLIOverlays(ctx context.Context, _ *ListCLIOverlaysRequest) (*ListCLIOverlaysResponse, error) {
	rows, err := agent_backend_repo.AgentBackend().ListCLIOverlays(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]*CLIOverlayItem, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		status := "path"
		if row.CLIPath != "" {
			status = "recognized"
		}
		items = append(items, &CLIOverlayItem{BackendSyncID: row.BackendSyncID, Fingerprint: row.AgentredFingerprint, Status: status})
	}
	return &ListCLIOverlaysResponse{Items: items}, nil
}

// GetCLIOverlay reads this desktop's overlay. Missing and empty both mean PATH.
func (s *agentBackendSvc) GetCLIOverlay(ctx context.Context, req *GetCLIOverlayRequest) (*GetCLIOverlayResponse, error) {
	if req == nil || strings.TrimSpace(req.BackendSyncID) == "" {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	remote := remote_device_svc.Default()
	if remote == nil {
		return &GetCLIOverlayResponse{Status: "path"}, nil
	}
	fingerprint, err := remote.DeviceFingerprint()
	if err != nil {
		return nil, err
	}
	overlay, err := agent_backend_repo.AgentBackend().FindCLIOverlay(ctx, req.BackendSyncID, fingerprint)
	if err != nil {
		return nil, err
	}
	if overlay == nil || overlay.CLIPath == "" {
		return &GetCLIOverlayResponse{Status: "path"}, nil
	}
	return &GetCLIOverlayResponse{CLIPath: overlay.CLIPath, Status: "recognized"}, nil
}

// SetCLIOverlay writes this desktop's own row only. The caller keeps editing a
// backend identity through the normal API; this method is the local overlay seam.
func (s *agentBackendSvc) SetCLIOverlay(ctx context.Context, req *SetCLIOverlayRequest) (*SetCLIOverlayResponse, error) {
	if req == nil || strings.TrimSpace(req.BackendSyncID) == "" {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	remote := remote_device_svc.Default()
	if remote == nil {
		return nil, errors.New("remote device service unavailable")
	}
	fingerprint, err := remote.DeviceFingerprint()
	if err != nil {
		return nil, err
	}
	overlay, err := agent_backend_repo.AgentBackend().FindCLIOverlay(ctx, req.BackendSyncID, fingerprint)
	if err != nil {
		return nil, err
	}
	if overlay == nil {
		overlay = &agent_backend_entity.CLIOverlay{
			BackendSyncID: req.BackendSyncID, AgentredFingerprint: fingerprint,
			CLIPath: strings.TrimSpace(req.CLIPath), Status: consts.ACTIVE,
		}
		if err := agent_backend_repo.AgentBackend().CreateCLIOverlay(ctx, overlay); err != nil {
			return nil, err
		}
		sync_svc.NotifyCreate(ctx, syncwire.KindAgentBackendCLI, overlay.ID, overlay.SyncMeta)
	} else {
		overlay.CLIPath = strings.TrimSpace(req.CLIPath)
		if err := agent_backend_repo.AgentBackend().UpdateCLIOverlay(ctx, overlay); err != nil {
			return nil, err
		}
		sync_svc.NotifyUpdate(ctx, syncwire.KindAgentBackendCLI, overlay.ID, overlay.SyncMeta)
	}
	status := "path"
	if overlay.CLIPath != "" {
		status = "recognized"
	}
	return &SetCLIOverlayResponse{CLIPath: overlay.CLIPath, Status: status}, nil
}

func (s *agentBackendSvc) List(ctx context.Context, _ *ListBackendsRequest) (*ListBackendsResponse, error) {
	rows, err := agent_backend_repo.AgentBackend().List(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	counts, err := agent_repo.Agent().CountByBackends(ctx, ids)
	if err != nil {
		return nil, err
	}
	items := make([]*BackendItem, 0, len(rows))
	for _, row := range rows {
		// LLMProviderKey == "" 表示 claudecode/codex 后端走 CLI 自身登录，无需查 provider。
		var provider *llm_provider_entity.LLMProvider
		if row.LLMProviderKey != "" {
			p, err := llm_provider_repo.LLMProvider().FindByKey(ctx, row.LLMProviderKey)
			if err != nil {
				return nil, err
			}
			provider = p
		}
		item := s.toItem(ctx, row, provider)
		item.AgentCount = counts[row.ID]
		items = append(items, item)
	}
	return &ListBackendsResponse{Items: items}, nil
}

func (s *agentBackendSvc) Create(ctx context.Context, req *CreateBackendRequest) (*CreateBackendResponse, error) {
	return s.create(ctx, req, "", false)
}

// CreateOpenClaw receives token as a transient method argument instead of a
// Wails DTO field. The token is persisted only after the backend has a stable ID.
func (s *agentBackendSvc) CreateOpenClaw(ctx context.Context, req *CreateBackendRequest, token string) (*CreateBackendResponse, error) {
	return s.create(ctx, req, token, true)
}

func (s *agentBackendSvc) create(ctx context.Context, req *CreateBackendRequest, token string, requireOpenClaw bool) (*CreateBackendResponse, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	routes, err := marshalRouteTargets(req.ModelRoutes)
	if err != nil {
		return nil, i18n.NewError(ctx, code.AgentBackendUnknownAlias)
	}
	now := s.now()
	b := &agent_backend_entity.AgentBackend{
		Type:                  strings.TrimSpace(req.Type),
		Name:                  strings.TrimSpace(req.Name),
		LLMProviderKey:        strings.TrimSpace(req.LLMProviderKey),
		LLMModelKey:           strings.TrimSpace(req.LLMModelKey),
		ModelRoutes:           routes,
		Sandbox:               strings.TrimSpace(req.Sandbox),
		Approval:              strings.TrimSpace(req.Approval),
		EnvJSON:               strings.TrimSpace(req.EnvJSON),
		ReasoningEffort:       strings.TrimSpace(req.ReasoningEffort),
		DefaultPermissionMode: strings.TrimSpace(req.DefaultPermissionMode),
		DefaultModel:          strings.TrimSpace(req.DefaultModel),
		OpenClawGatewayURL:    strings.TrimSpace(req.OpenClawGatewayURL),
		OpenClawAgentID:       strings.TrimSpace(req.OpenClawAgentID),
		OpenClawDefaultModel:  strings.TrimSpace(req.OpenClawDefaultModel),
		OpenClawSessionMode:   strings.TrimSpace(req.OpenClawSessionMode),
		DeviceFingerprint:     strings.TrimSpace(req.DeviceID),
		Status:                consts.ACTIVE,
		Createtime:            now,
		Updatetime:            now,
	}
	var deviceErr error
	b.DeviceFingerprint, deviceErr = normalizeDeviceID(b.DeviceFingerprint)
	if deviceErr != nil {
		return nil, deviceErr
	}
	if requireOpenClaw && !b.IsOpenClaw() {
		return nil, i18n.NewError(ctx, code.AgentBackendInvalidType)
	}
	if b.IsOpenClaw() {
		normalized, err := agent_backend_entity.NormalizeOpenClawGatewayURL(b.OpenClawGatewayURL)
		if err != nil {
			return nil, i18n.NewError(ctx, code.InvalidParameter)
		}
		b.OpenClawGatewayURL = normalized
		if b.OpenClawSessionMode == "" {
			b.OpenClawSessionMode = agent_backend_entity.OpenClawSessionPerAgentRESession
		}
	}
	if err := b.Check(ctx); err != nil {
		return nil, err
	}
	if err := s.validateDeviceID(ctx, b.DeviceFingerprint); err != nil {
		return nil, err
	}

	dup, err := agent_backend_repo.AgentBackend().FindByName(ctx, b.Name)
	if err != nil {
		return nil, err
	}
	if dup != nil {
		return nil, i18n.NewError(ctx, code.AgentBackendNameDuplicated)
	}

	provider, err := s.resolveProviderForSave(ctx, b)
	if err != nil {
		return nil, err
	}
	if err := s.validateRouteProviders(ctx, b); err != nil {
		return nil, err
	}

	if err := agent_backend_repo.AgentBackend().Create(ctx, b); err != nil {
		return nil, err
	}
	if err := s.setCLIOverlayIfAvailable(ctx, b.SyncID, req.CLIPath); err != nil {
		return nil, err
	}
	if b.IsOpenClaw() && token != "" {
		store := s.secretStore()
		if store == nil {
			_ = agent_backend_repo.AgentBackend().Delete(ctx, b.ID)
			return nil, errors.New("openclaw secret store unavailable")
		}
		if err := store.Set(openClawTokenAccount(b.ID), token); err != nil {
			rollbackErr := agent_backend_repo.AgentBackend().Delete(ctx, b.ID)
			return nil, errors.Join(err, rollbackErr)
		}
	}
	sync_svc.NotifyCreate(ctx, syncwire.KindAgentBackend, b.ID, b.SyncMeta)
	return &CreateBackendResponse{Item: s.toItem(ctx, b, provider)}, nil
}

func (s *agentBackendSvc) Update(ctx context.Context, req *UpdateBackendRequest) (*UpdateBackendResponse, error) {
	return s.update(ctx, req, "", false, false)
}

// UpdateOpenClaw changes non-sensitive config and applies an explicit secret
// intent. Empty token with clearToken=false preserves the existing keychain item.
func (s *agentBackendSvc) UpdateOpenClaw(ctx context.Context, req *UpdateBackendRequest, token string, clearToken bool) (*UpdateBackendResponse, error) {
	return s.update(ctx, req, token, clearToken, true)
}

func (s *agentBackendSvc) update(ctx context.Context, req *UpdateBackendRequest, token string, clearToken, requireOpenClaw bool) (*UpdateBackendResponse, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	existing, err := agent_backend_repo.AgentBackend().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, i18n.NewError(ctx, code.AgentBackendNotFound)
	}
	if requireOpenClaw && !existing.IsOpenClaw() {
		return nil, i18n.NewError(ctx, code.AgentBackendInvalidType)
	}
	before := *existing

	newName := strings.TrimSpace(req.Name)
	if newName != existing.Name {
		dup, err := agent_backend_repo.AgentBackend().FindByName(ctx, newName)
		if err != nil {
			return nil, err
		}
		if dup != nil && dup.ID != existing.ID {
			return nil, i18n.NewError(ctx, code.AgentBackendNameDuplicated)
		}
	}

	existing.Name = newName
	existing.LLMProviderKey = strings.TrimSpace(req.LLMProviderKey)
	existing.LLMModelKey = strings.TrimSpace(req.LLMModelKey)
	routes, err := marshalRouteTargets(req.ModelRoutes)
	if err != nil {
		return nil, i18n.NewError(ctx, code.AgentBackendUnknownAlias)
	}
	existing.ModelRoutes = routes
	existing.Sandbox = strings.TrimSpace(req.Sandbox)
	existing.Approval = strings.TrimSpace(req.Approval)
	existing.EnvJSON = strings.TrimSpace(req.EnvJSON)
	existing.ReasoningEffort = strings.TrimSpace(req.ReasoningEffort)
	existing.DefaultPermissionMode = strings.TrimSpace(req.DefaultPermissionMode)
	existing.DefaultModel = strings.TrimSpace(req.DefaultModel)
	existing.OpenClawGatewayURL = strings.TrimSpace(req.OpenClawGatewayURL)
	existing.OpenClawAgentID = strings.TrimSpace(req.OpenClawAgentID)
	existing.OpenClawDefaultModel = strings.TrimSpace(req.OpenClawDefaultModel)
	existing.OpenClawSessionMode = strings.TrimSpace(req.OpenClawSessionMode)
	existing.DeviceFingerprint = strings.TrimSpace(req.DeviceID)
	var deviceErr error
	existing.DeviceFingerprint, deviceErr = normalizeDeviceID(existing.DeviceFingerprint)
	if deviceErr != nil {
		return nil, deviceErr
	}
	existing.Updatetime = s.now()
	if existing.IsOpenClaw() {
		normalized, err := agent_backend_entity.NormalizeOpenClawGatewayURL(existing.OpenClawGatewayURL)
		if err != nil {
			return nil, i18n.NewError(ctx, code.InvalidParameter)
		}
		existing.OpenClawGatewayURL = normalized
		if existing.OpenClawSessionMode == "" {
			existing.OpenClawSessionMode = agent_backend_entity.OpenClawSessionPerAgentRESession
		}
	}

	if err := existing.Check(ctx); err != nil {
		return nil, err
	}
	if err := s.validateDeviceID(ctx, existing.DeviceFingerprint); err != nil {
		return nil, err
	}

	provider, err := s.resolveProviderForSave(ctx, existing)
	if err != nil {
		return nil, err
	}
	if err := s.validateRouteProviders(ctx, existing); err != nil {
		return nil, err
	}

	if err := agent_backend_repo.AgentBackend().Update(ctx, existing); err != nil {
		return nil, err
	}
	if err := s.setCLIOverlayIfAvailable(ctx, existing.SyncID, req.CLIPath); err != nil {
		return nil, err
	}
	if existing.IsOpenClaw() && (token != "" || clearToken) {
		store := s.secretStore()
		if store == nil {
			rollbackErr := agent_backend_repo.AgentBackend().Update(ctx, &before)
			return nil, errors.Join(errors.New("openclaw secret store unavailable"), rollbackErr)
		}
		var secretErr error
		if clearToken {
			secretErr = store.Delete(openClawTokenAccount(existing.ID))
			if errors.Is(secretErr, keychain.ErrNotFound) {
				secretErr = nil
			}
		} else {
			secretErr = store.Set(openClawTokenAccount(existing.ID), token)
		}
		if secretErr != nil {
			rollbackErr := agent_backend_repo.AgentBackend().Update(ctx, &before)
			return nil, errors.Join(secretErr, rollbackErr)
		}
	}
	sync_svc.NotifyUpdate(ctx, syncwire.KindAgentBackend, existing.ID, existing.SyncMeta)
	return &UpdateBackendResponse{Item: s.toItem(ctx, existing, provider)}, nil
}

func (s *agentBackendSvc) Test(ctx context.Context, req *TestBackendRequest) (*TestBackendResponse, error) {
	return s.test(ctx, req, "", false)
}

func (s *agentBackendSvc) TestOpenClaw(ctx context.Context, req *TestBackendRequest, token string) (*TestBackendResponse, error) {
	return s.test(ctx, req, token, true)
}

func (s *agentBackendSvc) test(ctx context.Context, req *TestBackendRequest, transientToken string, requireOpenClaw bool) (*TestBackendResponse, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	entity, err := s.resolveBackendForTest(ctx, req)
	if err != nil {
		return nil, err
	}
	if requireOpenClaw && !entity.IsOpenClaw() {
		return nil, i18n.NewError(ctx, code.AgentBackendInvalidType)
	}
	// OpenClaw 草稿的配置问题走结构化 Code(前端本地化),不要塌成中文「参数错误」。
	if entity.IsOpenClaw() {
		if issue := openClawDraftIssue(entity); issue != nil {
			return issue, nil
		}
	}
	if err := entity.Check(ctx); err != nil {
		return nil, err
	}
	if entity.IsOpenClaw() {
		if remote_device_svc.TargetsAnotherMachine(entity.DeviceFingerprint) {
			return &TestBackendResponse{OK: false, Code: "OPENCLAW_REMOTE_SECRET_UNAVAILABLE"}, nil
		}
		return s.testOpenClaw(ctx, req, entity, transientToken)
	}
	// 远端 device → 不在本地装 deps / gateway / provider，由 daemon 自己装。
	// 主进程只负责拨号 + 转发参数 + 折叠结果。provider FK 校验也下放给 daemon，
	// 因为远端可能有自己的 provider 状态视图（如离线时本地 provider 表过期）。
	// ErrRemoteDeviceNotFound 只对具名指纹发生：本机指纹（R13 认领后的本机 backend）
	// 不是配对 agentred 行——它是本地档，回落到下面的本地测试路径；只有真正未配对
	// 的其他远端指纹才报「远端设备不存在」。
	if did, ok, err := localPairedDeviceID(ctx, entity.DeviceFingerprint); err != nil {
		if !errors.Is(err, ErrRemoteDeviceNotFound) {
			return nil, i18n.NewError(ctx, code.InvalidParameter)
		}
		if remote_device_svc.TargetsAnotherMachine(entity.DeviceFingerprint) {
			return &TestBackendResponse{OK: false, Message: i18n.NewError(ctx, code.RemoteDeviceNotFound).Error()}, nil
		}
	} else if ok {
		return s.probeRemote(ctx, entity, did)
	}

	// builtin 必须有 active provider；claudecode / codex 关联了 provider 则严格匹配类型，
	// 未关联时表示走 CLI 自身登录态，跳过 provider 校验。
	var matchedProvider *llm_provider_entity.LLMProvider
	if entity.IsBuiltin() {
		if _, err := s.requireActiveProvider(ctx, entity.LLMProviderKey); err != nil {
			return nil, err
		}
	} else if entity.LLMProviderKey != "" {
		p, err := s.requireMatchingProvider(ctx, entity)
		if err != nil {
			return nil, err
		}
		matchedProvider = p
	}

	// 单元测试用 s.prober 注入 fake 跳过 LLM；正常路径按 type 查询注册表。
	// 未注册的 backend type 返回 AgentBackendInvalidType。
	prober := s.prober
	if prober == nil {
		prober = proberFor(agent_backend_entity.BackendType(entity.Type))
	}
	if prober == nil {
		return nil, i18n.NewError(ctx, code.AgentBackendInvalidType)
	}

	deps := ProbeDeps{}
	// claudecode / codex 且关联了 provider → 经 gateway 走临时 token；
	// 未关联 provider → 不签 token，让 CLI 直接用 claude/codex login 状态。
	// piagent 不走 gateway（直接连 provider BaseURL，见 buildPiAgentProviderProbe），
	// 即使绑定供应商也不进此分支。
	if !entity.IsBuiltin() && !entity.IsPiAgent() && entity.LLMProviderKey != "" {
		if s.gateway == nil {
			return &TestBackendResponse{OK: false, Message: i18n.NewError(ctx, code.AgentBackendGatewayUnavailable).Error()}, nil
		}
		if st := s.gateway.Status(); st.State != "running" {
			return &TestBackendResponse{OK: false, Message: i18n.NewError(ctx, code.AgentBackendGatewayUnavailable).Error()}, nil
		}
		tok, err := s.gateway.IssueToken(ctx, entity, testTokenTTL)
		if err != nil {
			return &TestBackendResponse{OK: false, Message: err.Error()}, nil
		}
		defer s.gateway.RevokeToken(tok)
		deps.Token = tok
		deps.GatewayURL = s.gateway.URL()
		if matchedProvider != nil {
			deps.Model = providerDefaultModelID(ctx, matchedProvider)
		}
	}

	probeCtx, cancel := context.WithTimeout(ctx, testProbeTimeout)
	defer cancel()

	// 注册 cancel：前端 CancelTest 拿同一 RequestID 触发，prober ctx 立刻 Done。
	// RequestID 留空 → 不可中断（兼容自动化 / 旧前端）。
	if req.RequestID != "" {
		s.registerProbe(req.RequestID, cancel)
		defer s.unregisterProbe(req.RequestID)
	}

	start := time.Now()
	reply, err := prober.Run(probeCtx, entity, deps)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		msg := err.Error()
		switch {
		case errors.Is(err, context.Canceled):
			msg = "已取消"
		case errors.Is(err, context.DeadlineExceeded):
			msg = "测试超时（30s）"
		}
		return &TestBackendResponse{OK: false, Message: msg, LatencyMs: latency}, nil
	}
	return &TestBackendResponse{OK: true, Message: strings.TrimSpace(reply), LatencyMs: latency}, nil
}

type openClawProbeFunc func(
	ctx context.Context,
	config openclawgateway.Config,
	selection openclawgateway.ProbeSelection,
) (*openclawgateway.ProbeResult, error)

const openClawIdentityAccount = "agentre.openclaw.device.identity.seed"

func (s *agentBackendSvc) testOpenClaw(
	ctx context.Context,
	req *TestBackendRequest,
	backend *agent_backend_entity.AgentBackend,
	transientToken string,
) (*TestBackendResponse, error) {
	store := s.secretStore()
	if store == nil {
		return &TestBackendResponse{OK: false, Code: "OPENCLAW_SECRET_UNAVAILABLE"}, nil
	}
	token := transientToken
	if token == "" && backend.ID > 0 {
		stored, err := store.Get(openClawTokenAccount(backend.ID))
		switch {
		case err == nil:
			token = stored
		case errors.Is(err, keychain.ErrNotFound):
		default:
			return &TestBackendResponse{OK: false, Code: "OPENCLAW_SECRET_UNAVAILABLE", Message: err.Error()}, nil
		}
	}
	identity, err := s.openClawIdentity()
	if err != nil {
		return &TestBackendResponse{OK: false, Code: "OPENCLAW_SECRET_UNAVAILABLE", Message: err.Error()}, nil
	}

	probeCtx, cancel := context.WithTimeout(ctx, testProbeTimeout)
	defer cancel()
	if req.RequestID != "" {
		s.registerProbe(req.RequestID, cancel)
		defer s.unregisterProbe(req.RequestID)
	}
	probe := s.openClawProbe
	if probe == nil {
		probe = openclawgateway.Probe
	}
	start := time.Now()
	result, err := probe(probeCtx, openclawgateway.Config{
		URL:           backend.OpenClawGatewayURL,
		Token:         token,
		Identity:      identity,
		ClientVersion: configs.Version,
		Platform:      runtime.GOOS,
	}, openclawgateway.ProbeSelection{
		AgentID: backend.OpenClawAgentID,
		Model:   backend.OpenClawDefaultModel,
	})
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return &TestBackendResponse{
			OK: false, Code: openClawProbeErrorCode(err), Message: err.Error(), LatencyMs: latency,
		}, nil
	}
	response := &TestBackendResponse{
		OK:             true,
		LatencyMs:      latency,
		GatewayVersion: result.GatewayVersion,
		Protocol:       result.Protocol,
		GrantedScopes:  append([]string(nil), result.GrantedScopes...),
		Methods:        append([]string(nil), result.Methods...),
		Events:         append([]string(nil), result.Events...),
		OpenClawAgents: make([]OpenClawAgentOption, 0, len(result.Agents)),
		OpenClawModels: make([]OpenClawModelOption, 0, len(result.Models)),
	}
	for _, agent := range result.Agents {
		response.OpenClawAgents = append(response.OpenClawAgents, OpenClawAgentOption{
			ID: agent.ID, Name: agent.Name, PrimaryModel: agent.PrimaryModel,
			Fallbacks: append([]string(nil), agent.Fallbacks...), Default: agent.Default,
		})
	}
	for _, model := range result.Models {
		response.OpenClawModels = append(response.OpenClawModels, OpenClawModelOption{
			ID: model.ID, Name: model.Name, Provider: model.Provider, Available: model.Available,
		})
	}
	return response, nil
}

// openClawDraftIssue 把 OpenClaw 草稿的配置错误翻成结构化 Code。entity.Check 对所有
// 这些情况一律返回 code.InvalidParameter,前端只能拿到后端 i18n 的中文「参数错误」:
// 既分不清是 URL 还是名称有问题,还把中文糊进英文 UI。返回 nil 表示草稿本身没问题。
func openClawDraftIssue(backend *agent_backend_entity.AgentBackend) *TestBackendResponse {
	if backend == nil {
		return nil
	}
	issue := func(code string) *TestBackendResponse {
		return &TestBackendResponse{OK: false, Code: code}
	}
	if strings.TrimSpace(backend.Name) == "" {
		return issue("OPENCLAW_NAME_REQUIRED")
	}
	if _, err := agent_backend_entity.NormalizeOpenClawGatewayURL(backend.OpenClawGatewayURL); err != nil {
		switch {
		case errors.Is(err, agent_backend_entity.ErrOpenClawGatewayURLRequired):
			return issue("OPENCLAW_URL_REQUIRED")
		case errors.Is(err, agent_backend_entity.ErrOpenClawGatewayURLScheme):
			return issue("OPENCLAW_URL_SCHEME")
		case errors.Is(err, agent_backend_entity.ErrOpenClawGatewayURLHost):
			return issue("OPENCLAW_URL_HOST")
		case errors.Is(err, agent_backend_entity.ErrOpenClawGatewayURLCredentials):
			return issue("OPENCLAW_URL_CREDENTIALS")
		case errors.Is(err, agent_backend_entity.ErrOpenClawGatewayURLPlaintextRemote):
			return issue("OPENCLAW_URL_PLAINTEXT_REMOTE")
		default:
			return issue("OPENCLAW_URL_INVALID")
		}
	}
	if strings.TrimSpace(backend.OpenClawSessionMode) != agent_backend_entity.OpenClawSessionPerAgentRESession {
		return issue("OPENCLAW_SESSION_MODE_INVALID")
	}
	return nil
}

func (s *agentBackendSvc) openClawIdentity() (*openclawgateway.DeviceIdentity, error) {
	s.identityMu.Lock()
	defer s.identityMu.Unlock()
	store := s.secretStore()
	if store == nil {
		return nil, errors.New("openclaw secret store unavailable")
	}
	encoded, err := store.Get(openClawIdentityAccount)
	if err == nil {
		seed, decodeErr := base64.RawURLEncoding.DecodeString(encoded)
		if decodeErr != nil {
			return nil, errors.New("openclaw device identity is invalid")
		}
		return openclawgateway.NewDeviceIdentityFromSeed(seed)
	}
	if !errors.Is(err, keychain.ErrNotFound) {
		return nil, err
	}
	identity, err := openclawgateway.GenerateDeviceIdentity()
	if err != nil {
		return nil, err
	}
	encoded = base64.RawURLEncoding.EncodeToString(identity.Seed())
	if err := store.Set(openClawIdentityAccount, encoded); err != nil {
		return nil, err
	}
	return identity, nil
}

// resolveOpenClawRuntimeConfig is the only boundary that turns persisted
// non-sensitive backend configuration plus keychain state into a live Gateway
// client config. The returned token must never cross DTO or daemon wire types.
func (s *agentBackendSvc) resolveOpenClawRuntimeConfig(ctx context.Context, backendID int64) (openclawgateway.Config, error) {
	backend, err := agent_backend_repo.AgentBackend().Find(ctx, backendID)
	if err != nil {
		return openclawgateway.Config{}, err
	}
	if backend == nil || !backend.IsOpenClaw() {
		return openclawgateway.Config{}, i18n.NewError(ctx, code.AgentBackendNotFound)
	}
	if remote_device_svc.TargetsAnotherMachine(backend.DeviceFingerprint) {
		return openclawgateway.Config{}, ErrOpenClawRemoteSecretUnavailable
	}
	if err := backend.Check(ctx); err != nil {
		return openclawgateway.Config{}, err
	}
	store := s.secretStore()
	if store == nil {
		return openclawgateway.Config{}, errors.New("openclaw secret store unavailable")
	}
	token, err := store.Get(openClawTokenAccount(backend.ID))
	if errors.Is(err, keychain.ErrNotFound) {
		token = ""
		err = nil
	}
	if err != nil {
		return openclawgateway.Config{}, err
	}
	identity, err := s.openClawIdentity()
	if err != nil {
		return openclawgateway.Config{}, err
	}
	return openclawgateway.Config{
		URL: backend.OpenClawGatewayURL, Token: token, Identity: identity,
		ClientVersion: configs.Version, Platform: runtime.GOOS,
	}, nil
}

// ResolveOpenClawRuntimeConfig is the bootstrap adapter for the default
// service. It is intentionally not exposed as an App/Wails method.
func ResolveOpenClawRuntimeConfig(ctx context.Context, backendID int64) (openclawgateway.Config, error) {
	service, ok := defaultAgentBackend.(*agentBackendSvc)
	if !ok || service == nil {
		return openclawgateway.Config{}, errors.New("openclaw backend service unavailable")
	}
	return service.resolveOpenClawRuntimeConfig(ctx, backendID)
}

// openClawGatewayAuthCodes 是网关直接给出的鉴权类 code。真实网关(2026.7.1-2)对
// token 不匹配回的却是 INVALID_REQUEST + "unauthorized: ..." —— 只按 code 匹配会
// 漏掉它,前端于是把原始协议串当文案显示。故同时看 details.reason 与 message。
var openClawGatewayAuthCodes = map[string]struct{}{
	"AUTH_FAILED": {}, "UNAUTHORIZED": {}, "FORBIDDEN": {},
}

func normalizeOpenClawRPCCode(rpcErr *openclawgateway.RPCError) string {
	rpcCode := strings.ToUpper(strings.TrimSpace(rpcErr.Code))
	reason := strings.ToLower(strings.TrimSpace(rpcErr.Reason))
	message := strings.ToLower(rpcErr.Message)
	switch {
	case rpcCode == "NOT_PAIRED" || reason == "not_paired":
		return "OPENCLAW_NOT_PAIRED"
	default:
	}
	if _, ok := openClawGatewayAuthCodes[rpcCode]; ok {
		return "AUTH_FAILED"
	}
	if reason == "unauthorized" || strings.HasPrefix(message, "unauthorized") {
		return "AUTH_FAILED"
	}
	if rpcCode == "" {
		return "OPENCLAW_CONNECTION_FAILED"
	}
	return rpcCode
}

func openClawProbeErrorCode(err error) string {
	var rpcErr *openclawgateway.RPCError
	switch {
	case errors.As(err, &rpcErr):
		return normalizeOpenClawRPCCode(rpcErr)
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

// CancelTest 中断一个还在跑的 Test。
// 未知 RequestID 返回 Canceled=false 而不是错误：前端竞态（Test 已经返回但 cancel 慢半拍）属常态。
func (s *agentBackendSvc) CancelTest(_ context.Context, req *CancelTestBackendRequest) (*CancelTestBackendResponse, error) {
	if req == nil || req.RequestID == "" {
		return &CancelTestBackendResponse{Canceled: false}, nil
	}
	s.probesMu.Lock()
	cancel, ok := s.probes[req.RequestID]
	s.probesMu.Unlock()
	if !ok {
		return &CancelTestBackendResponse{Canceled: false}, nil
	}
	cancel()
	return &CancelTestBackendResponse{Canceled: true}, nil
}

func (s *agentBackendSvc) registerProbe(id string, cancel context.CancelFunc) {
	s.probesMu.Lock()
	defer s.probesMu.Unlock()
	if s.probes == nil {
		s.probes = map[string]context.CancelFunc{}
	}
	s.probes[id] = cancel
}

func (s *agentBackendSvc) unregisterProbe(id string) {
	s.probesMu.Lock()
	defer s.probesMu.Unlock()
	delete(s.probes, id)
}

// resolveBackendForTest 组装用于测试的 entity:
//   - ID>0: 取保存记录;UseDraft=true 时用 draft 覆盖
//   - ID==0: 直接从 draft 拼一个临时 entity
func (s *agentBackendSvc) resolveBackendForTest(ctx context.Context, req *TestBackendRequest) (*agent_backend_entity.AgentBackend, error) {
	var saved *agent_backend_entity.AgentBackend
	if req.ID > 0 {
		got, err := agent_backend_repo.AgentBackend().Find(ctx, req.ID)
		if err != nil {
			return nil, err
		}
		if got == nil {
			return nil, i18n.NewError(ctx, code.AgentBackendNotFound)
		}
		saved = got
		if !req.UseDraft {
			return saved, nil
		}
	}
	out := &agent_backend_entity.AgentBackend{Status: consts.ACTIVE}
	if saved != nil {
		*out = *saved
	}
	if typ := strings.TrimSpace(req.Type); typ != "" {
		out.Type = typ
	}
	if name := strings.TrimSpace(req.Name); name != "" {
		out.Name = name
	}
	if strings.TrimSpace(req.LLMProviderKey) != "" {
		out.LLMProviderKey = strings.TrimSpace(req.LLMProviderKey)
	}
	if strings.TrimSpace(req.LLMModelKey) != "" {
		out.LLMModelKey = strings.TrimSpace(req.LLMModelKey)
	}
	if req.ModelRoutes != nil {
		r, err := marshalRouteTargets(req.ModelRoutes)
		if err != nil {
			return nil, err
		}
		out.ModelRoutes = r
	}
	out.CLIPath = strings.TrimSpace(req.CLIPath)
	out.Sandbox = strings.TrimSpace(req.Sandbox)
	out.Approval = strings.TrimSpace(req.Approval)
	out.EnvJSON = strings.TrimSpace(req.EnvJSON)
	out.ReasoningEffort = strings.TrimSpace(req.ReasoningEffort)
	out.DefaultPermissionMode = strings.TrimSpace(req.DefaultPermissionMode)
	out.DefaultModel = strings.TrimSpace(req.DefaultModel)
	out.OpenClawGatewayURL = strings.TrimSpace(req.OpenClawGatewayURL)
	out.OpenClawAgentID = strings.TrimSpace(req.OpenClawAgentID)
	out.OpenClawDefaultModel = strings.TrimSpace(req.OpenClawDefaultModel)
	out.OpenClawSessionMode = strings.TrimSpace(req.OpenClawSessionMode)
	if out.IsOpenClaw() {
		if out.OpenClawSessionMode == "" {
			out.OpenClawSessionMode = agent_backend_entity.OpenClawSessionPerAgentRESession
		}
		if normalized, err := agent_backend_entity.NormalizeOpenClawGatewayURL(out.OpenClawGatewayURL); err == nil {
			out.OpenClawGatewayURL = normalized
		}
	}
	return out, nil
}

func (s *agentBackendSvc) Delete(ctx context.Context, req *DeleteBackendRequest) (*DeleteBackendResponse, error) {
	existing, err := agent_backend_repo.AgentBackend().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, i18n.NewError(ctx, code.AgentBackendNotFound)
	}
	var restoreToken string
	var removedToken bool
	if existing.IsOpenClaw() {
		store := s.secretStore()
		if store == nil {
			return nil, errors.New("openclaw secret store unavailable")
		}
		value, getErr := store.Get(openClawTokenAccount(existing.ID))
		switch {
		case getErr == nil:
			restoreToken = value
			if err := store.Delete(openClawTokenAccount(existing.ID)); err != nil && !errors.Is(err, keychain.ErrNotFound) {
				return nil, err
			}
			removedToken = true
		case errors.Is(getErr, keychain.ErrNotFound):
		default:
			return nil, getErr
		}
	}
	if err := agent_backend_repo.AgentBackend().Delete(ctx, existing.ID); err != nil {
		if removedToken {
			restoreErr := s.secretStore().Set(openClawTokenAccount(existing.ID), restoreToken)
			return nil, errors.Join(err, restoreErr)
		}
		return nil, err
	}
	// 引用它的执行目标项一并落墓碑，Agent 本身不删（R6）。
	sync_svc.NotifyDelete(ctx, syncwire.KindAgentBackend, existing.ID, existing.SyncMeta)
	return &DeleteBackendResponse{}, nil
}

// resolveProviderForSave 在 Create / Update 路径上统一处理 provider 关联：
//   - builtin 必须有 provider（entity.Check 已强制 LLMProviderKey 非空）。
//   - claudecode / codex 在 LLMProviderKey == "" 时表示走 CLI 自身登录，跳过 FindByKey。
//   - LLMProviderKey != "" 时要求严格匹配 BackendKind 的 provider 类型集合。
func (s *agentBackendSvc) resolveProviderForSave(ctx context.Context, b *agent_backend_entity.AgentBackend) (*llm_provider_entity.LLMProvider, error) {
	if b.LLMProviderKey == "" {
		return nil, nil
	}
	return s.requireMatchingProvider(ctx, b)
}

// requireActiveProvider 把「provider 必须存在且 active」的两次错误码合并到一处。
func (s *agentBackendSvc) requireActiveProvider(ctx context.Context, key string) (*llm_provider_entity.LLMProvider, error) {
	p, err := llm_provider_repo.LLMProvider().FindByKey(ctx, key)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, i18n.NewError(ctx, code.AgentBackendLLMProviderNotFound)
	}
	if !p.IsActive() {
		return nil, i18n.NewError(ctx, code.AgentBackendLLMProviderInactive)
	}
	return p, nil
}

func (s *agentBackendSvc) requireMatchingProvider(ctx context.Context, b *agent_backend_entity.AgentBackend) (*llm_provider_entity.LLMProvider, error) {
	p, err := s.requireActiveProvider(ctx, b.LLMProviderKey)
	if err != nil {
		return nil, err
	}
	kind := b.Kind()
	if kind == nil {
		return nil, i18n.NewError(ctx, code.AgentBackendInvalidType)
	}
	if !kind.ProviderTypeMatch(llm_provider_entity.ProviderType(p.Type)) {
		return nil, i18n.NewError(ctx, code.AgentBackendProviderTypeMismatch)
	}
	// fixed-model（ModelKey 非空）：只接受该 Provider 名下启用且类型兼容的模型。
	if strings.TrimSpace(b.LLMModelKey) != "" {
		if _, err := s.requireOwnedEnabledModel(ctx, p, b.LLMModelKey); err != nil {
			return nil, err
		}
	}
	// piagent 绑定时必须能通过 --model agentre-<key>/<model> 命中该供应商下的模型：
	// provider-default 要求当前能解析出非空的启用默认模型；fixed-model 已在上面校验。
	if kind.RequiresProviderModel() &&
		strings.TrimSpace(b.LLMModelKey) == "" &&
		providerDefaultModelID(ctx, p) == "" {
		return nil, i18n.NewError(ctx, code.AgentBackendProviderModelRequired)
	}
	return p, nil
}

// requireOwnedEnabledModel 校验固定模型目标：存在、启用且属于该 Provider。
// ModelKey 为空返回 (nil, nil)。
func (s *agentBackendSvc) requireOwnedEnabledModel(
	ctx context.Context,
	p *llm_provider_entity.LLMProvider,
	modelKey string,
) (*llm_provider_model_entity.LLMProviderModel, error) {
	if strings.TrimSpace(modelKey) == "" {
		return nil, nil
	}
	m, err := llm_provider_repo.LLMProvider().FindModelByKey(ctx, modelKey)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, i18n.NewError(ctx, code.LLMProviderModelNotFound)
	}
	if m.ProviderID != p.ID {
		return nil, i18n.NewError(ctx, code.LLMProviderModelNotOwned)
	}
	if !m.IsEnabled() {
		return nil, i18n.NewError(ctx, code.LLMProviderModelDisabled)
	}
	return m, nil
}

// providerDefaultModelID 按 provider-default 语义返回 Provider 当前可执行的默认模型 ID。
// 与 llm_provider_svc.ResolveTarget 的默认分支同一规则：Provider 未启用、未配置默认模型、
// 或默认模型缺失 / 停用时返回空串。只取 ModelID，不透出 BaseURL / APIKey 等凭证。
func providerDefaultModelID(ctx context.Context, p *llm_provider_entity.LLMProvider) string {
	if p == nil || !p.IsEnabled() || !p.HasDefaultModel() {
		return ""
	}
	m, err := llm_provider_repo.LLMProvider().FindModelByKey(ctx, p.DefaultModelKey)
	if err != nil || m == nil || !m.IsEnabled() {
		return ""
	}
	return m.ModelID
}

func (s *agentBackendSvc) validateRouteProviders(ctx context.Context, b *agent_backend_entity.AgentBackend) error {
	routes, err := agent_backend_entity.ParseModelRoutes(b.ModelRoutes)
	if err != nil {
		return i18n.NewError(ctx, code.AgentBackendUnknownAlias)
	}
	if len(routes) == 0 {
		return nil
	}
	kind := b.Kind()
	if kind == nil {
		return i18n.NewError(ctx, code.AgentBackendInvalidType)
	}
	for _, route := range routes {
		if strings.TrimSpace(route.ProviderKey) == "" {
			return i18n.NewError(ctx, code.AgentBackendAliasProviderInvalid)
		}
		p, err := llm_provider_repo.LLMProvider().FindByKey(ctx, route.ProviderKey)
		if err != nil {
			return err
		}
		if p == nil || !p.IsActive() || !kind.ProviderTypeMatch(llm_provider_entity.ProviderType(p.Type)) {
			return i18n.NewError(ctx, code.AgentBackendAliasProviderInvalid)
		}
		if strings.TrimSpace(route.ModelKey) != "" {
			if _, err := s.requireOwnedEnabledModel(ctx, p, route.ModelKey); err != nil {
				return err
			}
		}
	}
	return nil
}

// toItem 把 entity + 关联 provider（可能为 nil）打平成前端 DTO。
// ctx 用于查询关联远端设备信息（DeviceName / Online）。
func (s *agentBackendSvc) toItem(ctx context.Context, b *agent_backend_entity.AgentBackend, p *llm_provider_entity.LLMProvider) *BackendItem {
	item := &BackendItem{
		ID:                    b.ID,
		SyncID:                b.SyncID,
		Type:                  b.Type,
		Name:                  b.Name,
		LLMProviderKey:        b.LLMProviderKey,
		LLMModelKey:           b.LLMModelKey,
		ModelRoutes:           routeTargetsFromEntity(b.ModelRoutes),
		Sandbox:               b.Sandbox,
		Approval:              b.Approval,
		EnvJSON:               b.EnvJSON,
		ReasoningEffort:       b.ReasoningEffort,
		DefaultPermissionMode: b.DefaultPermissionMode,
		DefaultModel:          b.DefaultModel,
		OpenClawGatewayURL:    b.OpenClawGatewayURL,
		OpenClawAgentID:       b.OpenClawAgentID,
		OpenClawDefaultModel:  b.OpenClawDefaultModel,
		OpenClawSessionMode:   b.OpenClawSessionMode,
		Createtime:            b.Createtime,
		Updatetime:            b.Updatetime,
	}
	if b.IsOpenClaw() {
		if store := s.secretStore(); store != nil {
			_, err := store.Get(openClawTokenAccount(b.ID))
			item.HasToken = err == nil
		}
	}
	if p != nil {
		item.LLMProviderName = p.Name
		item.LLMProviderType = p.Type
		item.LLMProviderModel = s.effectiveModelID(ctx, b, p)
		item.LLMProviderActive = p.IsActive()
	}
	// 展示口径的设备标识：本机档（空 DeviceID / R13 认领后的本机指纹）一律空串，
	// 见 remote_device_svc.ExternalDeviceID —— 本机指纹不会出现在配对表里，照远端
	// 解析只会得到「没名字 + 离线」，组织架构页据此渲染成「这台电脑未配对它」。
	if deviceID := remote_device_svc.ExternalDeviceID(b.DeviceFingerprint); deviceID != "" {
		item.DeviceID = deviceID
		if dv, err := pairedDeviceView(ctx, deviceID); err == nil && dv != nil {
			item.DeviceName = dv.Name
			item.Online = dv.Online
		}
	}
	return item
}

// marshalRouteTargets 把服务层 DTO 的 RouteTarget map 序列化回 entity 的持久化字符串。
func marshalRouteTargets(routes map[string]RouteTarget) (string, error) {
	if len(routes) == 0 {
		return "{}", nil
	}
	entity := make(map[string]agent_backend_entity.ModelRouteTarget, len(routes))
	for k, v := range routes {
		entity[k] = agent_backend_entity.ModelRouteTarget{
			ProviderKey: v.ProviderKey,
			ModelKey:    v.ModelKey,
		}
	}
	return agent_backend_entity.MarshalModelRoutes(entity)
}

// routeTargetsFromEntity 把 entity 的持久化字符串解析回服务层类型化 RouteTarget。
// 解析失败返回空 map（展示侧容忍脏数据）。
func routeTargetsFromEntity(s string) map[string]RouteTarget {
	routes, err := agent_backend_entity.ParseModelRoutes(s)
	if err != nil {
		return map[string]RouteTarget{}
	}
	out := make(map[string]RouteTarget, len(routes))
	for k, v := range routes {
		out[k] = RouteTarget{ProviderKey: v.ProviderKey, ModelKey: v.ModelKey}
	}
	return out
}

// effectiveModelID 返回后端解析出的实际模型 ID（展示口径）：fixed-model 取指定模型，
// 否则取 Provider 当前默认模型。只取 ModelID，不透出凭证。
func (s *agentBackendSvc) effectiveModelID(ctx context.Context, b *agent_backend_entity.AgentBackend, p *llm_provider_entity.LLMProvider) string {
	if b == nil || p == nil || !p.IsEnabled() {
		return ""
	}
	key := strings.TrimSpace(b.LLMModelKey)
	if key != "" {
		m, err := llm_provider_repo.LLMProvider().FindModelByKey(ctx, key)
		if err != nil || m == nil || !m.IsEnabled() || m.ProviderID != p.ID {
			return ""
		}
		return m.ModelID
	}
	return providerDefaultModelID(ctx, p)
}

func (s *agentBackendSvc) secretStore() keychain.Keychain {
	if s.secrets != nil {
		return s.secrets
	}
	return keychain.Default()
}

func openClawTokenAccount(backendID int64) string {
	return "agentre.openclaw.backend." + strconv.FormatInt(backendID, 10) + ".token"
}

// normalizeDeviceID converts the UI's empty local selection to this
// installation's canonical fingerprint. A nil remote service is retained for
// narrow unit-test construction only; bootstrap initializes it before writes.
func normalizeDeviceID(deviceID string) (string, error) {
	if deviceID != "" || remote_device_svc.Default() == nil {
		return deviceID, nil
	}
	return remote_device_svc.Default().DeviceFingerprint()
}

func (s *agentBackendSvc) validateDeviceID(ctx context.Context, deviceID string) error {
	if deviceID == "" || strings.HasPrefix(deviceID, "sha256:") {
		return nil
	}
	return i18n.NewError(ctx, code.AgentBackendInvalidDevice)
}

// pairedDeviceView resolves a canonical fingerprint to this installation's
// paired-device view. It is intentionally best-effort for presentation; an
// unpaired named target remains a valid persisted/synced target.
func pairedDeviceView(ctx context.Context, fingerprint string) (*remote_device_svc.DeviceView, error) {
	if fingerprint == "" || remote_device_svc.Default() == nil {
		return nil, nil
	}
	rows, err := remote_device_svc.Default().List(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row != nil && row.DaemonFingerprint == fingerprint {
			return row, nil
		}
	}
	return nil, nil
}

// localPairedDeviceID translates a persisted fingerprint only at a local
// dispatch boundary, where the daemon client still requires the paired-row ID.
func localPairedDeviceID(ctx context.Context, fingerprint string) (int64, bool, error) {
	if fingerprint == "" {
		return 0, false, nil
	}
	if !strings.HasPrefix(fingerprint, "sha256:") {
		return 0, false, errors.New("invalid device fingerprint")
	}
	view, err := pairedDeviceView(ctx, fingerprint)
	if err != nil {
		return 0, false, err
	}
	if view == nil {
		return 0, false, ErrRemoteDeviceNotFound
	}
	return view.ID, view.ID > 0, nil
}

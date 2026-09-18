package agent_backend_svc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/hermes"
	"github.com/agentre-hub/agentre/internal/pkg/backendcred"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

// 设备本地后端凭据(规格 2026-09-17-device-local-backend-credentials)。
//
// Hermes / OpenClaw 的凭据只存在后端**绑定的那台设备**上:绑到本机就写本机 keychain,
// 绑到 agentred 就经连接池借一条已配对连接,请那台设备自己存、自己连。桌面端因此有
// 两副面孔,本文件把它们放在一起:
//
//   - 出站:六个操作按 DeviceFingerprint 路由(本地路径一字未改,远端路径走 wirecall)。
//   - 入站:LocalBackendCredentials 是控制台按指纹拨到这台桌面端时的那一面 ——
//     同样六个操作,答的是本机 keychain(wireinbound.BackendCredentialPort)。
//
// 不变量:明文凭据只朝绑定设备走。应答里只有「存没存」「登录成谁」,日志里一个字都没有。

// remoteCredentialsPort 是「在某台已配对设备上做一次凭据操作」的窄接口。
// 生产实现是 realRemoteCredentials(借连接池 → 调 wirecall);单测注入 fake。
type remoteCredentialsPort interface {
	Status(ctx context.Context, deviceID int64, request *agentrewire.BackendCredentialStatusRequest) (*agentrewire.BackendCredentialStatusResponse, error)
	SetOpenClawToken(ctx context.Context, deviceID int64, request *agentrewire.OpenClawTokenSetRequest) (*agentrewire.OpenClawTokenSetResponse, error)
	HermesAuthProviders(ctx context.Context, deviceID int64, request *agentrewire.HermesAuthProvidersRequest) (*agentrewire.HermesAuthProvidersResponse, error)
	HermesLogin(ctx context.Context, deviceID int64, request *agentrewire.HermesLoginRequest) (*agentrewire.HermesLoginResponse, error)
	HermesLogout(ctx context.Context, deviceID int64, request *agentrewire.HermesLogoutRequest) (*agentrewire.HermesLogoutResponse, error)
	TestConnection(ctx context.Context, deviceID int64, request *agentrewire.BackendConnectionTestRequest) (*agentrewire.BackendConnectionTestResponse, error)
}

type realRemoteCredentials struct{}

// callBoundDevice 借一条到 deviceID 的已配对连接跑一次 typed 调用。
// 拨号失败一律折成 ErrRemoteDialFailed / ErrRemoteDeviceNotFound,与 cli.* 同一条判据。
func callBoundDevice[Req any, Resp any](
	ctx context.Context, deviceID int64,
	call func(context.Context, wirecall.Caller, Req) (Resp, error), request Req,
) (Resp, error) {
	var zero Resp
	lease, err := remote_device_svc.Default().Pool().Borrow(ctx, deviceID)
	if err != nil {
		if errors.Is(err, remote_device_svc.ErrDeviceNotFound) {
			return zero, ErrRemoteDeviceNotFound
		}
		return zero, fmt.Errorf("%w: %v", ErrRemoteDialFailed, err)
	}
	defer lease.Release()
	return call(ctx, lease.Client(), request)
}

func (realRemoteCredentials) Status(ctx context.Context, deviceID int64, request *agentrewire.BackendCredentialStatusRequest) (*agentrewire.BackendCredentialStatusResponse, error) {
	return callBoundDevice(ctx, deviceID, wirecall.BackendCredentialStatus, request)
}

func (realRemoteCredentials) SetOpenClawToken(ctx context.Context, deviceID int64, request *agentrewire.OpenClawTokenSetRequest) (*agentrewire.OpenClawTokenSetResponse, error) {
	return callBoundDevice(ctx, deviceID, wirecall.OpenClawTokenSet, request)
}

func (realRemoteCredentials) HermesAuthProviders(ctx context.Context, deviceID int64, request *agentrewire.HermesAuthProvidersRequest) (*agentrewire.HermesAuthProvidersResponse, error) {
	return callBoundDevice(ctx, deviceID, wirecall.HermesAuthProviders, request)
}

func (realRemoteCredentials) HermesLogin(ctx context.Context, deviceID int64, request *agentrewire.HermesLoginRequest) (*agentrewire.HermesLoginResponse, error) {
	return callBoundDevice(ctx, deviceID, wirecall.HermesLogin, request)
}

func (realRemoteCredentials) HermesLogout(ctx context.Context, deviceID int64, request *agentrewire.HermesLogoutRequest) (*agentrewire.HermesLogoutResponse, error) {
	return callBoundDevice(ctx, deviceID, wirecall.HermesLogout, request)
}

func (realRemoteCredentials) TestConnection(ctx context.Context, deviceID int64, request *agentrewire.BackendConnectionTestRequest) (*agentrewire.BackendConnectionTestResponse, error) {
	return callBoundDevice(ctx, deviceID, wirecall.BackendConnectionTest, request)
}

func (s *agentBackendSvc) credentials() remoteCredentialsPort {
	if s.remoteCredentials != nil {
		return s.remoteCredentials
	}
	return realRemoteCredentials{}
}

// localCredentials 是这台桌面端自己的那一面,出站的本机路径与入站注册面共用同一份。
func (s *agentBackendSvc) localCredentials() *LocalBackendCredentials {
	return &LocalBackendCredentials{svc: s}
}

// boundCredentialDevice 解析一个后端的绑定设备。
//
// remote=false 表示绑在本机(空指纹或本机指纹),调用方走本地路径。remote=true 时
// deviceID 是配对表里的行号 —— 未配对 / 指纹不合法都是「那台设备不在这里」,凭据操作
// 到不了它,如实报出来而不是悄悄落在本机。
func boundCredentialDevice(ctx context.Context, fingerprint devicefp.Carrier) (int64, bool, error) {
	if !remote_device_svc.TargetsAnotherMachine(fingerprint) {
		return 0, false, nil
	}
	if !strings.HasPrefix(string(fingerprint), "sha256:") {
		return 0, true, i18n.NewError(ctx, code.AgentBackendInvalidDevice)
	}
	deviceID, ok, err := localPairedDeviceID(ctx, fingerprint)
	switch {
	case errors.Is(err, ErrRemoteDeviceNotFound):
		return 0, true, i18n.NewError(ctx, code.RemoteDeviceNotFound)
	case err != nil:
		return 0, true, err
	case !ok:
		return 0, true, i18n.NewError(ctx, code.RemoteDeviceNotFound)
	}
	return deviceID, true, nil
}

// remoteCredentialError 把一次设备凭据调用的失败翻成用户读得懂的一句话。
// 机器原话只进日志,不进界面。
func remoteCredentialError(ctx context.Context, deviceID int64, err error) error {
	switch {
	case errors.Is(err, ErrRemoteDeviceNotFound):
		return i18n.NewError(ctx, code.RemoteDeviceNotFound)
	case errors.Is(err, ErrRemoteDialFailed):
		logger.Ctx(ctx).Warn("agent_backend_svc.remoteCredentialError: dial failed",
			zap.Int64("deviceId", deviceID), zap.Error(err))
		return i18n.NewError(ctx, code.RemoteDeviceDialFailed)
	case errors.Is(err, context.DeadlineExceeded):
		return i18n.NewError(ctx, code.RemoteDeviceTimeout)
	default:
		logger.Ctx(ctx).Warn("agent_backend_svc.remoteCredentialError: device credential call failed",
			zap.Int64("deviceId", deviceID), zap.Error(err))
		return i18n.NewError(ctx, code.RemoteDeviceAuthOpFailed)
	}
}

// hermesCodeError 把绑定设备回的结构化结果码翻回本地化错误 —— 两条路径(本机 / 远端)
// 于是对同一个原因说同一句话。认不出来的码只当作「那台设备上的凭据操作失败了」。
func hermesCodeError(ctx context.Context, resultCode string) error {
	if bizCode, ok := hermesBizCode(resultCode); ok {
		return i18n.NewError(ctx, bizCode)
	}
	return i18n.NewError(ctx, code.RemoteDeviceAuthOpFailed)
}

// BackendCredentialStatus 查询一个后端在它绑定设备上的凭据状态。
// 只回「存没存 / 登录成谁」,凭据本身不出设备。
func (s *agentBackendSvc) BackendCredentialStatus(
	ctx context.Context, req *BackendCredentialStatusRequest,
) (*BackendCredentialStatusResponse, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	request := &agentrewire.BackendCredentialStatusRequest{
		BackendType: strings.TrimSpace(req.Type),
		SyncId:      strings.TrimSpace(req.SyncID),
	}
	switch agent_backend_entity.BackendType(request.BackendType) {
	case agent_backend_entity.TypeOpenClaw:
		if request.SyncId == "" {
			return nil, i18n.NewError(ctx, code.InvalidParameter)
		}
	case agent_backend_entity.TypeHermes:
		base, err := agent_backend_entity.NormalizeHermesURL(req.HermesURL)
		if err != nil {
			return nil, i18n.NewError(ctx, code.InvalidParameter)
		}
		request.HermesUrl = base
	default:
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}

	deviceID, remote, err := boundCredentialDevice(ctx, devicefp.Carrier(strings.TrimSpace(req.DeviceID)))
	if err != nil {
		return nil, err
	}
	var response *agentrewire.BackendCredentialStatusResponse
	if remote {
		response, err = s.credentials().Status(ctx, deviceID, request)
		if err != nil {
			return nil, remoteCredentialError(ctx, deviceID, err)
		}
	} else {
		response, err = s.localCredentials().Status(ctx, request)
		if err != nil {
			return nil, err
		}
	}
	return &BackendCredentialStatusResponse{
		OpenClawTokenSaved: response.GetOpenclawTokenSaved(),
		HermesLoggedIn:     response.GetHermesLoggedIn(),
		HermesProvider:     response.GetHermesProvider(),
		HermesUserID:       response.GetHermesUserId(),
	}, nil
}

// saveOpenClawToken 把保存 / 清除 token 的意图落到后端绑定的那台设备上。
// 失败由调用方回滚后端配置 —— 配置与凭据必须一起成立(决策 2)。
func (s *agentBackendSvc) saveOpenClawToken(
	ctx context.Context, backend *agent_backend_entity.AgentBackend, token string, clearToken bool,
) error {
	deviceID, remote, err := boundCredentialDevice(ctx, backend.DeviceFingerprint)
	if err != nil {
		return err
	}
	if !remote {
		return s.writeLocalOpenClawToken(backendcred.OpenClawTokenAccount(backend.SyncID), token, clearToken)
	}
	if strings.TrimSpace(backend.SyncID) == "" {
		return errOpenClawTokenSlotMissing
	}
	request := &agentrewire.OpenClawTokenSetRequest{SyncId: backend.SyncID, Clear: clearToken}
	if !clearToken {
		request.Token = token
	}
	if _, err := s.credentials().SetOpenClawToken(ctx, deviceID, request); err != nil {
		return remoteCredentialError(ctx, deviceID, err)
	}
	return nil
}

// writeLocalOpenClawToken 写 / 删本机 keychain 上的一个 OpenClaw token 槽位。
func (s *agentBackendSvc) writeLocalOpenClawToken(account, token string, clearToken bool) error {
	store := s.secretStore()
	switch {
	case store == nil:
		return backendcred.ErrStoreUnavailable
	case account == "":
		return errOpenClawTokenSlotMissing
	case clearToken:
		if err := store.Delete(account); err != nil && !errors.Is(err, backendcred.ErrNotFound) {
			return err
		}
		return nil
	default:
		return store.Set(account, token)
	}
}

// clearCredentialOnBoundDevice 是删除后端时的凭据清理:**尽力而为**。
//
// 绑定设备离线、清不掉、甚至已经不在账号里,删除照样成立(规格「删除后端」);残留的
// 凭据不再被任何后端引用,留着也伤不到谁,比为它挡下一次删除要好。
func (s *agentBackendSvc) clearCredentialOnBoundDevice(ctx context.Context, backend *agent_backend_entity.AgentBackend) {
	if backend == nil {
		return
	}
	deviceID, remote, err := boundCredentialDevice(ctx, backend.DeviceFingerprint)
	if err != nil {
		logger.Ctx(ctx).Warn("agent_backend_svc.clearCredentialOnBoundDevice: bound device unresolved; leaving the credential",
			zap.Int64("id", backend.ID), zap.Error(err))
		return
	}
	if !remote {
		// 本机路径原样:OpenClaw 的槽位在删除前就清掉了(可回滚),这里只剩 Hermes。
		s.deleteHermesCredential(ctx, backend)
		return
	}
	switch {
	case backend.IsOpenClaw():
		if strings.TrimSpace(backend.SyncID) == "" {
			return
		}
		if _, err := s.credentials().SetOpenClawToken(ctx, deviceID, &agentrewire.OpenClawTokenSetRequest{
			SyncId: backend.SyncID, Clear: true,
		}); err != nil {
			logger.Ctx(ctx).Warn("agent_backend_svc.clearCredentialOnBoundDevice: openclaw token clear failed",
				zap.Int64("id", backend.ID), zap.Int64("deviceId", deviceID), zap.Error(err))
		}
	case backend.IsHermes():
		base, err := agent_backend_entity.NormalizeHermesURL(backend.HermesURL)
		if err != nil || base == "" {
			return
		}
		inUse, err := hermesURLUsedByAnotherBackendOn(ctx, backend.ID, base, backend.DeviceFingerprint)
		if err != nil {
			logger.Ctx(ctx).Warn("agent_backend_svc.clearCredentialOnBoundDevice: cannot list backends; keeping the shared hermes login",
				zap.Int64("id", backend.ID), zap.Error(err))
			return
		}
		if inUse {
			return
		}
		if _, err := s.credentials().HermesLogout(ctx, deviceID, &agentrewire.HermesLogoutRequest{HermesUrl: base}); err != nil {
			logger.Ctx(ctx).Warn("agent_backend_svc.clearCredentialOnBoundDevice: hermes logout failed",
				zap.Int64("id", backend.ID), zap.Int64("deviceId", deviceID), zap.Error(err))
		}
	}
}

// testOnBoundDevice 请绑定设备按它自己的凭据连一次,把结果折回桌面端既有的形状。
func (s *agentBackendSvc) testOnBoundDevice(
	ctx context.Context, deviceID int64, backend *agent_backend_entity.AgentBackend, transientToken string,
) *TestBackendResponse {
	response, err := s.credentials().TestConnection(ctx, deviceID, &agentrewire.BackendConnectionTestRequest{
		BackendType:          backend.Type,
		SyncId:               backend.SyncID,
		HermesUrl:            backend.HermesURL,
		HermesAuthProvider:   backend.HermesAuthProvider,
		OpenclawGatewayUrl:   backend.OpenClawGatewayURL,
		OpenclawAgentId:      backend.OpenClawAgentID,
		OpenclawDefaultModel: backend.OpenClawDefaultModel,
		OpenclawToken:        transientToken,
	})
	if err != nil {
		return &TestBackendResponse{OK: false, Message: remoteCredentialError(ctx, deviceID, err).Error()}
	}
	return testResponseFromWire(ctx, response)
}

// testResponseFromWire 把绑定设备的测试应答折回桌面端的测试结果。结构化 code 原样
// 保留(前端本地化),Hermes 的那几个同时补上与本地路径一致的中文兜底。
func testResponseFromWire(ctx context.Context, response *agentrewire.BackendConnectionTestResponse) *TestBackendResponse {
	out := &TestBackendResponse{
		OK:             response.GetOk(),
		Code:           response.GetCode(),
		Message:        response.GetMessage(),
		LatencyMs:      response.GetLatencyMs(),
		GatewayVersion: response.GetGatewayVersion(),
		Protocol:       int(response.GetProtocol()),
		GrantedScopes:  response.GetGrantedScopes(),
		Methods:        response.GetMethods(),
		Events:         response.GetEvents(),
		OpenClawAgents: make([]OpenClawAgentOption, 0, len(response.GetOpenclawAgents())),
		OpenClawModels: make([]OpenClawModelOption, 0, len(response.GetOpenclawModels())),
	}
	if bizCode, ok := hermesBizCode(out.Code); ok {
		out.Message = i18n.NewError(ctx, bizCode).Error()
	}
	for _, agent := range response.GetOpenclawAgents() {
		out.OpenClawAgents = append(out.OpenClawAgents, OpenClawAgentOption{
			ID: agent.GetId(), Name: agent.GetName(), PrimaryModel: agent.GetPrimaryModel(),
			Fallbacks: agent.GetFallbacks(), Default: agent.GetIsDefault(),
		})
	}
	for _, model := range response.GetOpenclawModels() {
		out.OpenClawModels = append(out.OpenClawModels, OpenClawModelOption{
			ID: model.GetId(), Name: model.GetName(), Provider: model.GetProvider(), Available: model.GetAvailable(),
		})
	}
	return out
}

// ── 入站:这台桌面端自己就是那台绑定设备 ──────────────────────────────────

// LocalBackendCredentials 是桌面端作为「绑定设备」时的凭据面(线形状)。
//
// 控制台按设备指纹拨号,设备表里 desktop 与 agentred 混在一起,所以桌面端必须答得出
// 与 agentred 同样的六个方法(wireinbound.Contract 的那六行)。答话的依据是本机
// keychain —— 与桌面端自己点这几个按钮时走的是同一份存储、同一套结果码。
type LocalBackendCredentials struct{ svc *agentBackendSvc }

// BackendCredentials 交出这台桌面端的凭据面,供入站注册面装配。
func BackendCredentials() *LocalBackendCredentials {
	if svc, ok := defaultAgentBackend.(*agentBackendSvc); ok {
		return &LocalBackendCredentials{svc: svc}
	}
	return &LocalBackendCredentials{svc: &agentBackendSvc{now: func() int64 { return time.Now().UnixMilli() }}}
}

func invalidCredentialParams(message string) error {
	return &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: message}
}

// Status 只回「这台机器上存没存」。登录有没有过期只有真连一次才知道。
func (p *LocalBackendCredentials) Status(
	ctx context.Context, request *agentrewire.BackendCredentialStatusRequest,
) (*agentrewire.BackendCredentialStatusResponse, error) {
	switch agent_backend_entity.BackendType(request.GetBackendType()) {
	case agent_backend_entity.TypeOpenClaw:
		account := backendcred.OpenClawTokenAccount(request.GetSyncId())
		if account == "" {
			return nil, invalidCredentialParams("sync id required")
		}
		return &agentrewire.BackendCredentialStatusResponse{OpenclawTokenSaved: p.svc.hasSecret(account)}, nil
	case agent_backend_entity.TypeHermes:
		base, err := agent_backend_entity.NormalizeHermesURL(request.GetHermesUrl())
		if err != nil {
			return nil, invalidCredentialParams("invalid hermes url")
		}
		response := &agentrewire.BackendCredentialStatusResponse{}
		if !p.svc.hasSecret(backendcred.HermesAccount(base)) {
			return response, nil
		}
		response.HermesLoggedIn = true
		// 展示身份存在后端行上(桌面端没有 agentred 那份 state.json),按 URL 找一条。
		if provider, userID, ok := localHermesIdentity(ctx, base); ok {
			response.HermesProvider = provider
			response.HermesUserId = userID
		}
		return response, nil
	default:
		return nil, invalidCredentialParams("backend type must be openclaw or hermes")
	}
}

// SetOpenClawToken 保存或清除一个 OpenClaw 后端在本机的 token 槽位(按 sync_id)。
func (p *LocalBackendCredentials) SetOpenClawToken(
	ctx context.Context, request *agentrewire.OpenClawTokenSetRequest,
) (*agentrewire.OpenClawTokenSetResponse, error) {
	account := backendcred.OpenClawTokenAccount(request.GetSyncId())
	if account == "" {
		return nil, invalidCredentialParams("sync id required")
	}
	token := strings.TrimSpace(request.GetToken())
	clearToken := request.GetClear()
	switch {
	case clearToken && token != "":
		return nil, invalidCredentialParams("token and clear are exclusive")
	case !clearToken && token == "":
		return nil, invalidCredentialParams("token required unless clearing")
	}
	if err := p.svc.writeLocalOpenClawToken(account, token, clearToken); err != nil {
		logger.Ctx(ctx).Warn("agent_backend_svc.LocalBackendCredentials.SetOpenClawToken: keychain write failed",
			zap.String("syncId", request.GetSyncId()), zap.Bool("clear", clearToken), zap.Error(err))
		return nil, err
	}
	return &agentrewire.OpenClawTokenSetResponse{TokenSaved: !clearToken}, nil
}

// HermesAuthProviders 从这台机器读一次 serve 的提供方目录。
func (p *LocalBackendCredentials) HermesAuthProviders(
	ctx context.Context, request *agentrewire.HermesAuthProvidersRequest,
) (*agentrewire.HermesAuthProvidersResponse, error) {
	base, err := agent_backend_entity.NormalizeHermesURL(request.GetHermesUrl())
	if err != nil {
		return nil, invalidCredentialParams("invalid hermes url")
	}
	providers, err := hermes.ListAuthProviders(ctx, base, nil)
	if err != nil {
		if _, resultCode, ok := hermesAuthCode(err); ok {
			return &agentrewire.HermesAuthProvidersResponse{Code: resultCode}, nil
		}
		return nil, err
	}
	response := &agentrewire.HermesAuthProvidersResponse{
		Providers: make([]*agentrewire.HermesAuthProvider, 0, len(providers)),
	}
	for _, provider := range providers {
		response.Providers = append(response.Providers, &agentrewire.HermesAuthProvider{
			Name: provider.Name, DisplayName: provider.DisplayName, SupportsPassword: provider.SupportsPassword,
		})
	}
	return response, nil
}

// HermesLogin 在这台机器上跑一次密码登录:密码只活这一次,keychain 只留 refresh token。
func (p *LocalBackendCredentials) HermesLogin(
	ctx context.Context, request *agentrewire.HermesLoginRequest,
) (*agentrewire.HermesLoginResponse, error) {
	base, err := agent_backend_entity.NormalizeHermesURL(request.GetHermesUrl())
	if err != nil {
		return nil, invalidCredentialParams("invalid hermes url")
	}
	identity, err := p.svc.credentialStore().Login(ctx, backendcred.HermesLogin{
		BaseURL: base, Provider: request.GetProvider(),
		Username: request.GetUsername(), Password: request.GetPassword(),
	})
	if err != nil {
		if _, resultCode, ok := hermesAuthCode(err); ok {
			return &agentrewire.HermesLoginResponse{Code: resultCode}, nil
		}
		logger.Ctx(ctx).Warn("agent_backend_svc.LocalBackendCredentials.HermesLogin: login failed",
			zap.String("hermesUrl", base), zap.Error(err))
		return nil, err
	}
	return &agentrewire.HermesLoginResponse{Provider: identity.Provider, UserId: identity.UserID}, nil
}

// HermesLogout 丢掉这台机器上这个 serve 的凭据。
func (p *LocalBackendCredentials) HermesLogout(
	ctx context.Context, request *agentrewire.HermesLogoutRequest,
) (*agentrewire.HermesLogoutResponse, error) {
	base, err := agent_backend_entity.NormalizeHermesURL(request.GetHermesUrl())
	if err != nil {
		return nil, invalidCredentialParams("invalid hermes url")
	}
	if err := p.svc.credentialStore().Logout(base); err != nil {
		logger.Ctx(ctx).Warn("agent_backend_svc.LocalBackendCredentials.HermesLogout: keychain delete failed",
			zap.String("hermesUrl", base), zap.Error(err))
		return nil, err
	}
	return &agentrewire.HermesLogoutResponse{}, nil
}

// TestConnection 按本机凭据真连一次。草稿 token 只用于这一次,不写入存储。
func (p *LocalBackendCredentials) TestConnection(
	ctx context.Context, request *agentrewire.BackendConnectionTestRequest,
) (*agentrewire.BackendConnectionTestResponse, error) {
	switch agent_backend_entity.BackendType(request.GetBackendType()) {
	case agent_backend_entity.TypeHermes:
		return p.testHermes(ctx, request)
	case agent_backend_entity.TypeOpenClaw:
		return p.testOpenClaw(ctx, request)
	default:
		return nil, invalidCredentialParams("backend type must be openclaw or hermes")
	}
}

func (p *LocalBackendCredentials) testHermes(
	ctx context.Context, request *agentrewire.BackendConnectionTestRequest,
) (*agentrewire.BackendConnectionTestResponse, error) {
	base, err := agent_backend_entity.NormalizeHermesURL(request.GetHermesUrl())
	if err != nil {
		return nil, invalidCredentialParams("invalid hermes url")
	}
	probeCtx, cancel := context.WithTimeout(ctx, testProbeTimeout)
	defer cancel()
	start := time.Now()
	_, err = hermesProbe(probeCtx, hermes.ProbeRequest{
		URL: base, AuthProvider: request.GetHermesAuthProvider(), Credentials: p.svc.credentialStore(),
	})
	latency := time.Since(start).Milliseconds()
	if err != nil {
		response := &agentrewire.BackendConnectionTestResponse{LatencyMs: latency}
		if _, resultCode, ok := hermesAuthCode(err); ok {
			response.Code = resultCode
		} else {
			response.Message = err.Error()
		}
		return response, nil
	}
	return &agentrewire.BackendConnectionTestResponse{Ok: true, LatencyMs: latency}, nil
}

func (p *LocalBackendCredentials) testOpenClaw(
	ctx context.Context, request *agentrewire.BackendConnectionTestRequest,
) (*agentrewire.BackendConnectionTestResponse, error) {
	gatewayURL, err := agent_backend_entity.NormalizeOpenClawGatewayURL(request.GetOpenclawGatewayUrl())
	if err != nil {
		return &agentrewire.BackendConnectionTestResponse{Code: openClawURLCode(err)}, nil
	}
	backend := &agent_backend_entity.AgentBackend{
		SyncMeta:             syncmeta_entity.SyncMeta{SyncID: request.GetSyncId()},
		Type:                 string(agent_backend_entity.TypeOpenClaw),
		OpenClawGatewayURL:   gatewayURL,
		OpenClawAgentID:      request.GetOpenclawAgentId(),
		OpenClawDefaultModel: request.GetOpenclawDefaultModel(),
		OpenClawSessionMode:  agent_backend_entity.OpenClawSessionPerAgentRESession,
	}
	result, err := p.svc.testOpenClaw(ctx, &TestBackendRequest{}, backend, request.GetOpenclawToken())
	if err != nil {
		return nil, err
	}
	return wireTestResponse(result), nil
}

// wireTestResponse 把桌面端的测试结果折成线上的应答。兜底文案里的凭据由产出方
// (testOpenClaw)抹掉,这里不再猜这次用的是哪一个 token。
func wireTestResponse(result *TestBackendResponse) *agentrewire.BackendConnectionTestResponse {
	response := &agentrewire.BackendConnectionTestResponse{
		Ok: result.OK, Code: result.Code, Message: result.Message,
		LatencyMs: result.LatencyMs, GatewayVersion: result.GatewayVersion, Protocol: int32(result.Protocol),
		GrantedScopes: result.GrantedScopes, Methods: result.Methods, Events: result.Events,
		OpenclawAgents: make([]*agentrewire.OpenClawAgentOption, 0, len(result.OpenClawAgents)),
		OpenclawModels: make([]*agentrewire.OpenClawModelOption, 0, len(result.OpenClawModels)),
	}
	for _, agent := range result.OpenClawAgents {
		response.OpenclawAgents = append(response.OpenclawAgents, &agentrewire.OpenClawAgentOption{
			Id: agent.ID, Name: agent.Name, PrimaryModel: agent.PrimaryModel,
			Fallbacks: agent.Fallbacks, IsDefault: agent.Default,
		})
	}
	for _, model := range result.OpenClawModels {
		response.OpenclawModels = append(response.OpenclawModels, &agentrewire.OpenClawModelOption{
			Id: model.ID, Name: model.Name, Provider: model.Provider, Available: model.Available,
		})
	}
	return response
}

// redactSecret 把凭据从即将离开本机的文本里抹掉。
func redactSecret(text, secret string) string {
	if secret == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, "[redacted]")
}

// hasSecret 只回答「这个槽位上有没有东西」,从不把内容交出去。
func (s *agentBackendSvc) hasSecret(account string) bool {
	if account == "" {
		return false
	}
	store := s.secretStore()
	if store == nil {
		return false
	}
	_, err := store.Get(account)
	return err == nil
}

// localHermesIdentity 找一条绑在本机、指向这个 serve 的后端行,取它的展示身份。
// 桌面端的 provider / userId 写在后端行上,凭据本身只在 keychain。
func localHermesIdentity(ctx context.Context, base string) (string, string, bool) {
	rows, err := agent_backend_repo.AgentBackend().List(ctx)
	if err != nil {
		logger.Ctx(ctx).Warn("agent_backend_svc.localHermesIdentity: cannot list backends", zap.Error(err))
		return "", "", false
	}
	for _, row := range rows {
		if row == nil || !row.IsHermes() || remote_device_svc.TargetsAnotherMachine(row.DeviceFingerprint) {
			continue
		}
		if other, err := agent_backend_entity.NormalizeHermesURL(row.HermesURL); err != nil || other != base {
			continue
		}
		if strings.TrimSpace(row.HermesAuthProvider) != "" || strings.TrimSpace(row.HermesUserID) != "" {
			return row.HermesAuthProvider, row.HermesUserID, true
		}
	}
	return "", "", false
}

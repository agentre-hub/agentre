package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/buildinfo"
	"github.com/agentre-hub/agentre/internal/daemon/auth"
	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/notifier"
	"github.com/agentre-hub/agentre/internal/daemon/remotefs"
	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/daemon/transcriptimport"
	"github.com/agentre-hub/agentre/internal/daemon/workspacefs"
	"github.com/agentre-hub/agentre/internal/pkg/ccoauth"
	"github.com/agentre-hub/agentre/internal/pkg/wireinbound"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

func protobufError(err error) error {
	if err == nil {
		return nil
	}
	var rpcErr *rpcerror.Error
	if errors.As(err, &rpcErr) {
		return &protorpc.Error{Code: int32(rpcErr.Code), Message: rpcErr.Message}
	}
	return &protorpc.Error{Code: protorpc.CodeInternal, Message: err.Error()}
}

// requireProtocolVersion gates every handshake on the caller's advertised wire
// protocol version before any credential is looked at.
//
// It runs first on purpose: a version-skewed peer that fails on "device
// fingerprint required" or "unauthorized" sends the operator after credentials
// when the real answer is `make agentred-deploy`.
func requireProtocolVersion(ctx context.Context, peerProtocol, peerMinSupported string) error {
	reason := wireversion.Reject(peerProtocol, peerMinSupported)
	if reason == "" {
		return nil
	}
	logger.Ctx(ctx).Warn("daemon.requireProtocolVersion: rejected handshake",
		zap.String("peerProtocolVersion", peerProtocol), zap.String("peerMinSupportedProtocolVersion", peerMinSupported),
		zap.String("daemonProtocolVersion", wireversion.Protocol), zap.String("daemonMinSupportedProtocolVersion", wireversion.MinSupported))
	return &protorpc.Error{Code: rpcerror.CodeProtocolVersion, Message: reason}
}

func requireProtobufAuth(ctx context.Context) error {
	conn := protorpc.ConnFromContext(ctx)
	if conn == nil || !conn.Auth().Authenticated {
		return &protorpc.Error{Code: int32(rpcerror.ErrUnauthorized.Code), Message: rpcerror.ErrUnauthorized.Message}
	}
	return nil
}

func (d *Daemon) registerProtobufMethods() {
	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_PAIR),
		func() *agentrewire.AuthPairRequest { return &agentrewire.AuthPairRequest{} },
		func(ctx context.Context, request *agentrewire.AuthPairRequest) (*agentrewire.AuthPairResponse, error) {
			if err := requireProtocolVersion(ctx, request.ProtocolVersion, request.MinSupportedProtocolVersion); err != nil {
				return nil, err
			}
			if request.DeviceFingerprint == "" {
				return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "device fingerprint required"}
			}
			result, err := d.auth.HandlePair(ctx, ipFromContext(ctx), auth.PairParams{
				Code: request.Code, DeviceName: request.DeviceName, DeviceFingerprint: request.DeviceFingerprint,
			})
			if err != nil {
				return nil, protobufError(err)
			}
			if conn := protorpc.ConnFromContext(ctx); conn != nil {
				conn.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: request.DeviceFingerprint, DeviceName: request.DeviceName})
				d.conns.add(conn, notifier.NewProtobuf(conn))
			}
			return &agentrewire.AuthPairResponse{
				DeviceToken: result.DeviceToken, DaemonFingerprint: result.DaemonFingerprint,
				InstanceUuid: result.InstanceUUID, ProtocolVersion: wireversion.Protocol,
				MinSupportedProtocolVersion: wireversion.MinSupported,
			}, nil
		})

	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_CONNECT),
		func() *agentrewire.AuthConnectRequest { return &agentrewire.AuthConnectRequest{} },
		func(ctx context.Context, request *agentrewire.AuthConnectRequest) (*agentrewire.AuthConnectResponse, error) {
			if err := requireProtocolVersion(ctx, request.ProtocolVersion, request.MinSupportedProtocolVersion); err != nil {
				return nil, err
			}
			if request.DeviceFingerprint == "" {
				return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "device fingerprint required"}
			}
			result, err := d.auth.HandleConnect(ctx, auth.ConnectParams{
				DeviceFingerprint: request.DeviceFingerprint, DeviceToken: request.DeviceToken,
				ExpectedDaemonFingerprint: request.ExpectedDaemonFingerprint,
			})
			if err != nil {
				return nil, protobufError(err)
			}
			if conn := protorpc.ConnFromContext(ctx); conn != nil {
				peer := d.state.Snapshot().PairedPeers[request.DeviceFingerprint]
				conn.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: request.DeviceFingerprint, DeviceName: peer.DeviceName})
				d.conns.add(conn, notifier.NewProtobuf(conn))
			}
			return &agentrewire.AuthConnectResponse{Ok: result.OK, InstanceUuid: result.InstanceUUID, ProtocolVersion: wireversion.Protocol, MinSupportedProtocolVersion: wireversion.MinSupported}, nil
		})

	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT),
		func() *agentrewire.AuthAccountRequest { return &agentrewire.AuthAccountRequest{} },
		func(ctx context.Context, request *agentrewire.AuthAccountRequest) (*agentrewire.AuthAccountResponse, error) {
			if err := requireProtocolVersion(ctx, request.ProtocolVersion, request.MinSupportedProtocolVersion); err != nil {
				return nil, err
			}
			// 对端身份不再从请求体读(决策 8):HandleAccount 验签后交出凭据 pfp
			// claim 里那个身份,这里只是把它记进连接。
			result, err := d.auth.HandleAccount(ctx, auth.AccountParams{Credential: request.Credential})
			if err != nil {
				return nil, protobufError(err)
			}
			if conn := protorpc.ConnFromContext(ctx); conn != nil {
				conn.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: result.PeerFingerprint, AccountID: d.loggedInAccountID()})
				d.conns.add(conn, notifier.NewProtobuf(conn))
			}
			return protobufAuthAccountResponse(result), nil
		})

	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_REVOKE),
		func() *agentrewire.AuthRevokeRequest { return &agentrewire.AuthRevokeRequest{} },
		func(ctx context.Context, request *agentrewire.AuthRevokeRequest) (*agentrewire.AuthRevokeResponse, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			if err := d.auth.HandleRevoke(ctx, request.DeviceFingerprint); err != nil {
				return nil, protobufError(err)
			}
			return &agentrewire.AuthRevokeResponse{Ok: true}, nil
		})

	llmHandlers := handlers.NewLLMHandlers(d.state)
	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_LLM_UPSERT),
		func() *agentrewire.LLMUpsertRequest { return &agentrewire.LLMUpsertRequest{} },
		func(ctx context.Context, request *agentrewire.LLMUpsertRequest) (*agentrewire.LLMUpsertResponse, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			models := make([]state.LLMModelMeta, 0, len(request.Models))
			for _, model := range request.Models {
				models = append(models, state.LLMModelMeta{ModelKey: model.ModelKey, ModelID: model.ModelId, Name: model.Name, Enabled: model.Enabled, ContextWindow: model.ContextWindow, MaxOutput: model.MaxOutput})
			}
			result, err := llmHandlers.Upsert(ctx, handlers.LLMUpsertParams{ProviderKey: request.ProviderKey, Name: request.Name, Type: request.Type, BaseURL: request.BaseUrl, Model: request.Model, DefaultModelKey: request.DefaultModelKey, Models: models, APIKey: request.ApiKey, ModelRoutes: request.ModelRoutes, UpdatedAt: request.UpdatedAt})
			if err != nil {
				return nil, protobufError(err)
			}
			return &agentrewire.LLMUpsertResponse{Ok: result.OK}, nil
		})
	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_LLM_DELETE),
		func() *agentrewire.LLMDeleteRequest { return &agentrewire.LLMDeleteRequest{} },
		func(ctx context.Context, request *agentrewire.LLMDeleteRequest) (*agentrewire.LLMDeleteResponse, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			result, err := llmHandlers.Delete(ctx, handlers.LLMDeleteParams{ProviderKey: request.ProviderKey})
			if err != nil {
				return nil, protobufError(err)
			}
			return &agentrewire.LLMDeleteResponse{Ok: result.OK}, nil
		})
	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_LLM_LIST),
		func() *agentrewire.LLMListRequest { return &agentrewire.LLMListRequest{} },
		func(ctx context.Context, _ *agentrewire.LLMListRequest) (*agentrewire.LLMListResponse, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			result, err := llmHandlers.List(ctx)
			if err != nil {
				return nil, protobufError(err)
			}
			response := &agentrewire.LLMListResponse{Providers: make([]*agentrewire.LLMProvider, 0, len(result.Providers))}
			for _, provider := range result.Providers {
				models := make([]*agentrewire.LLMModel, 0, len(provider.Models))
				for _, model := range provider.Models {
					models = append(models, &agentrewire.LLMModel{ModelKey: model.ModelKey, ModelId: model.ModelID, Name: model.Name, Enabled: model.Enabled, ContextWindow: model.ContextWindow, MaxOutput: model.MaxOutput})
				}
				response.Providers = append(response.Providers, &agentrewire.LLMProvider{ProviderKey: provider.ProviderKey, Name: provider.Name, Type: provider.Type, BaseUrl: provider.BaseURL, Model: provider.Model, DefaultModelKey: provider.DefaultModelKey, Models: models, MaskedTail: provider.MaskedTail, UpdatedAt: provider.UpdatedAt, ModelRoutes: provider.ModelRoutes})
			}
			return response, nil
		})

	engineHandlers := handlers.NewEngineHandlers(handlers.EngineDeps{State: d.state})
	cliHandlers := handlers.NewCLIHandlers(d.gateway, NewProviderLookup(d.state))
	// 引擎探测一族走共用注册面(wireinbound):方法号↔类型的配对因此只有一处,
	// 两种执行端不会再一边有、一边没有 —— 这正是控制台对桌面机器空转过的那一族。
	//
	// **族闸门取 requireProtobufAuth,而 engine.scan / engine.test 在各自端口里
	// 再过一次 requireProtobufLoggedIn。** 这不是放宽:requireProtobufLoggedIn 第一步
	// 就是 requireProtobufAuth,失败时返回的还是同一个错误值,所以先后顺序与错误
	// 原文都不变。cli.resolvePath 本来就只要求已鉴权。
	wireinbound.RegisterEngineMethods(d.protobufRegistry, wireinbound.EngineDeps{
		Auth: requireProtobufAuth,
		Scan: func(ctx context.Context, _ *agentrewire.EngineScanRequest) (*agentrewire.EngineScanResponse, error) {
			if err := d.requireProtobufLoggedIn(ctx); err != nil {
				return nil, err
			}
			result, err := engineHandlers.Scan(ctx)
			if err != nil {
				return nil, protobufError(err)
			}
			response := &agentrewire.EngineScanResponse{Items: make([]*agentrewire.EngineScanItem, 0, len(result.Items))}
			for _, item := range result.Items {
				response.Items = append(response.Items, &agentrewire.EngineScanItem{BackendType: item.BackendType, Status: item.Status})
			}
			return response, nil
		},
		Test: func(ctx context.Context, request *agentrewire.EngineTestRequest) (*agentrewire.EngineTestResponse, error) {
			if err := d.requireProtobufLoggedIn(ctx); err != nil {
				return nil, err
			}
			result, err := engineHandlers.Test(ctx, handlers.EngineTestParams{ProviderKey: request.ProviderKey, ModelKey: request.ModelKey})
			if err != nil {
				return nil, protobufError(err)
			}
			return &agentrewire.EngineTestResponse{Ok: result.OK, Message: result.Message, LatencyMs: result.LatencyMs}, nil
		},
		ResolveCLIPath: func(ctx context.Context, request *agentrewire.CLIResolvePathRequest) (*agentrewire.CLIResolvePathResponse, error) {
			result, err := cliHandlers.ResolvePath(ctx, handlers.CLIResolvePathParams{Type: request.Type})
			if err != nil {
				return nil, protobufError(err)
			}
			return &agentrewire.CLIResolvePathResponse{Path: result.Path, Found: result.Found}, nil
		},
	})
	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_ENGINE_DISCOVER),
		func() *agentrewire.EngineDiscoverRequest { return &agentrewire.EngineDiscoverRequest{} },
		func(ctx context.Context, request *agentrewire.EngineDiscoverRequest) (*agentrewire.EngineDiscoverResponse, error) {
			if err := d.requireProtobufLoggedIn(ctx); err != nil {
				return nil, err
			}
			result, err := engineHandlers.Discover(ctx, handlers.EngineDiscoverParams{ProviderKey: request.ProviderKey})
			if err != nil {
				return nil, protobufError(err)
			}
			response := &agentrewire.EngineDiscoverResponse{Models: make([]*agentrewire.EngineModel, 0, len(result.Models))}
			for _, model := range result.Models {
				response.Models = append(response.Models, &agentrewire.EngineModel{ModelId: model.ModelID, Name: model.Name})
			}
			return response, nil
		})
	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_CLI_PROBE),
		func() *agentrewire.CLIProbeRequest { return &agentrewire.CLIProbeRequest{} },
		func(ctx context.Context, request *agentrewire.CLIProbeRequest) (*agentrewire.CLIProbeResponse, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			result, err := cliHandlers.Probe(ctx, handlers.CLIProbeParams{BackendType: request.BackendType, LLMProviderKey: request.LlmProviderKey, CLIPath: request.CliPath, Sandbox: request.Sandbox, Approval: request.Approval, Model: request.Model})
			if err != nil {
				return nil, protobufError(err)
			}
			return &agentrewire.CLIProbeResponse{Text: result.Text}, nil
		})

	healthHandlers := handlers.NewHealthHandlers(d.state.InstanceUUID(), d.state, d)
	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_HEALTH_PING),
		func() *agentrewire.HealthPingRequest { return &agentrewire.HealthPingRequest{} },
		func(ctx context.Context, _ *agentrewire.HealthPingRequest) (*agentrewire.HealthPingResponse, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			result, err := healthHandlers.Ping(ctx)
			if err != nil {
				return nil, protobufError(err)
			}
			return protobufHealthPingResponse(&result), nil
		})

	ccFetcher := d.opts.CCUsageFetcher
	if ccFetcher == nil {
		ccFetcher = ccoauth.NewLocalFetcher()
	}
	usageHandlers := handlers.NewCCUsageHandlers(ccFetcher)
	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_CLAUDE_CODE_USAGE),
		func() *agentrewire.ClaudeCodeUsageRequest { return &agentrewire.ClaudeCodeUsageRequest{} },
		func(ctx context.Context, _ *agentrewire.ClaudeCodeUsageRequest) (*agentrewire.ClaudeCodeUsageResponse, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			result, err := usageHandlers.Get(ctx)
			if err != nil {
				return nil, protobufError(err)
			}
			response := &agentrewire.ClaudeCodeUsageResponse{Reason: result.Reason}
			if result.Data != nil {
				response.Data = &agentrewire.ClaudeCodeRateLimits{FiveHourPercent: result.Data.FiveHourPercent, WeeklyPercent: result.Data.WeeklyPercent, SonnetWeeklyPercent: result.Data.SonnetWeeklyPercent, OpusWeeklyPercent: result.Data.OpusWeeklyPercent}
				response.Data.FiveHourResetsAtMs = timePointerMillis(result.Data.FiveHourResetsAt)
				response.Data.WeeklyResetsAtMs = timePointerMillis(result.Data.WeeklyResetsAt)
				response.Data.SonnetWeeklyResetsAtMs = timePointerMillis(result.Data.SonnetWeeklyResetsAt)
				response.Data.OpusWeeklyResetsAtMs = timePointerMillis(result.Data.OpusWeeklyResetsAt)
			}
			return response, nil
		})

	skillsHandlers := handlers.NewSkillsHandlers()
	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_SKILLS_LIST),
		func() *agentrewire.SkillsListRequest { return &agentrewire.SkillsListRequest{} },
		func(ctx context.Context, request *agentrewire.SkillsListRequest) (*agentrewire.SkillsListResponse, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			result, err := skillsHandlers.List(ctx, handlers.SkillsListParams{BackendType: request.BackendType, CLIPath: request.CliPath})
			if err != nil {
				return nil, protobufError(err)
			}
			response := &agentrewire.SkillsListResponse{Packs: make([]*agentrewire.InstalledSkillPack, 0, len(result.Packs))}
			for _, pack := range result.Packs {
				response.Packs = append(response.Packs, &agentrewire.InstalledSkillPack{Id: pack.ID, Name: pack.Name, Description: pack.Description, Skills: append([]string(nil), pack.Skills...), Source: string(pack.Source), Recommended: pack.Recommended, Installed: pack.Installed, GloballyEnabled: pack.GloballyEnabled})
			}
			return response, nil
		})

	selfUpdateHandlers := handlers.NewSelfUpdateHandlers(handlers.SelfUpdateDeps{
		ActiveTurns: d.sessionStore,
		Resolve:     handlers.DefaultSelfUpdateResolve,
		Apply:       handlers.DefaultSelfUpdateApply,
		Restart:     handlers.DefaultSelfUpdateRestart,
	})
	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_AGENTRED_SELF_UPDATE),
		func() *agentrewire.AgentredSelfUpdateRequest { return &agentrewire.AgentredSelfUpdateRequest{} },
		func(ctx context.Context, request *agentrewire.AgentredSelfUpdateRequest) (*agentrewire.AgentredSelfUpdateResponse, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			result, err := selfUpdateHandlers.Update(ctx, handlers.SelfUpdateParams{Channel: request.Channel, Force: request.Force})
			if err != nil {
				return nil, protobufError(err)
			}
			return protobufAgentredSelfUpdateResponse(&result), nil
		})

	// 会话族的线形状收在 internal/pkg/wireinbound,两种执行端共用同一份。这里挂的是
	// **不依赖某一条连接**的那一半(清单 / 统计 / 补齐 / 改档);runtime 族与 attach 要
	// 认领这条连接、要按连接持有 runtime handler,挂在连接级 registry 上
	// (见 protobuf_runtime.go 的 connSessionPorts)。
	wireinbound.RegisterSessionMethods(d.protobufRegistry, d.daemonSessionPorts())

	wireinbound.RegisterPeripheralMethods(d.protobufRegistry, wireinbound.PeripheralDeps{
		Skills:      handlers.NewSkillsHandlers(),
		RemoteFS:    remotefs.NewHandlers(remotefs.Options{}),
		WorkspaceFS: workspacefs.NewHandlers(workspacefs.Options{}),
		// 读取器由 runtime_imports.go 里那几个 blank import 在 init() 时注册进
		// internal/pkg/transcriptimport 的注册表 —— daemon 侧不写第二套解析。
		//
		// 执行侧接的是与跑一轮**同一份**存储:会话身份行落进 daemon_sessions、
		// 回放出的转录落成消息行 + 块行,导入的会话因此和别的会话一样被
		// SESSION_LIST / SESSION_PULL 服务出去,不需要第二条镜像通路。
		TranscriptImport: transcriptimport.NewHandlers(transcriptimport.Options{
			Sessions:          d.sessionStore,
			Transcript:        d.transcript,
			TranscriptPurge:   transcriptPurger{db: d.db},
			SessionDelete:     d.sessionStore,
			LoggedInAccountID: d.loggedInAccountID,
		}),
	})
}

// protobufAuthAccountResponse 把握手结果折成线格式,并**回写对端身份**:调用方在
// 请求体里已经给不出自己的身份了,它在这条连接上的身份只能由这里认定的这个值说了算
// (conversation_id 的派生输入,见 client.ProtobufClient.SelfFingerprint)。
func protobufAuthAccountResponse(result *auth.AccountResult) *agentrewire.AuthAccountResponse {
	return &agentrewire.AuthAccountResponse{
		Ok: result.OK, InstanceUuid: result.InstanceUUID,
		PeerFingerprint:             result.PeerFingerprint,
		ProtocolVersion:             wireversion.Protocol,
		MinSupportedProtocolVersion: wireversion.MinSupported,
		DaemonVersion:               configs.Version,
		DaemonCommit:                buildinfo.ShortCommitID(),
	}
}

// selfUpdateRejectReasons 把 handlers.SelfUpdateRejectReason 翻成 wire 枚举。
// 翻译收在这一张表里,而不是让 handlers 包直接依赖协议编号 —— handlers.Update 的
// 拒绝原因需要可以脱离 wire 单独断言(见 selfupdate_test.go)。
var selfUpdateRejectReasons = map[handlers.SelfUpdateRejectReason]agentrewire.AgentredSelfUpdateRejectReason{
	handlers.SelfUpdateRejectNone:           agentrewire.AgentredSelfUpdateRejectReason_AGENTRED_SELF_UPDATE_REJECT_REASON_UNSPECIFIED,
	handlers.SelfUpdateRejectActiveTurns:    agentrewire.AgentredSelfUpdateRejectReason_AGENTRED_SELF_UPDATE_REJECT_REASON_ACTIVE_TURNS,
	handlers.SelfUpdateRejectInProgress:     agentrewire.AgentredSelfUpdateRejectReason_AGENTRED_SELF_UPDATE_REJECT_REASON_IN_PROGRESS,
	handlers.SelfUpdateRejectNotWritable:    agentrewire.AgentredSelfUpdateRejectReason_AGENTRED_SELF_UPDATE_REJECT_REASON_NOT_WRITABLE,
	handlers.SelfUpdateRejectAlreadyLatest:  agentrewire.AgentredSelfUpdateRejectReason_AGENTRED_SELF_UPDATE_REJECT_REASON_ALREADY_LATEST,
	handlers.SelfUpdateRejectDownloadFailed: agentrewire.AgentredSelfUpdateRejectReason_AGENTRED_SELF_UPDATE_REJECT_REASON_DOWNLOAD_FAILED,
}

// protobufAgentredSelfUpdateResponse 把远程自更新 RPC 的受理判定折成线格式。
func protobufAgentredSelfUpdateResponse(result *handlers.SelfUpdateResult) *agentrewire.AgentredSelfUpdateResponse {
	return &agentrewire.AgentredSelfUpdateResponse{
		Accepted:      result.Accepted,
		RejectReason:  selfUpdateRejectReasons[result.RejectReason],
		Message:       result.Message,
		ActiveTurns:   int32(result.ActiveTurns),
		TargetVersion: result.TargetVersion,
	}
}

// protobufHealthPingResponse 把 health.ping 的处理结果折成线格式。DaemonVersion /
// DaemonCommit 取自构建注入的 configs.Version / buildinfo.ShortCommitID(),
// 与 protobufAuthAccountResponse 同源(决策 4):两处应答语义相同、取值同源,
// 消费端据此得到「这台机器现在跑的是哪一版」。commit 未注入时是空串,
// 消费端据此判定为开发构建(决策 5),不参与「可升级」判定。
func protobufHealthPingResponse(result *handlers.HealthPingResult) *agentrewire.HealthPingResponse {
	response := &agentrewire.HealthPingResponse{
		InstanceUuid: result.InstanceUUID, ServerTimeMs: result.ServerTimeMs,
		Capabilities: append([]string(nil), result.Capabilities...), DbSizeBytes: result.DBSizeBytes,
		DaemonVersion: configs.Version,
		DaemonCommit:  buildinfo.ShortCommitID(),
	}
	for _, provider := range result.Providers {
		out := &agentrewire.HealthProvider{Key: provider.Key, Name: provider.Name, Type: provider.Type, DefaultModelKey: provider.DefaultModelKey}
		for _, model := range provider.Models {
			out.Models = append(out.Models, &agentrewire.HealthModel{Key: model.Key, ModelId: model.ModelID, Name: model.Name, Enabled: model.Enabled})
		}
		response.Providers = append(response.Providers, out)
	}
	return response
}

func (d *Daemon) requireProtobufLoggedIn(ctx context.Context) error {
	if err := requireProtobufAuth(ctx); err != nil {
		return err
	}
	if !d.state.IsLoggedIn() {
		return &protorpc.Error{Code: int32(rpcerror.ErrUnauthorized.Code), Message: rpcerror.ErrUnauthorized.Message}
	}
	return nil
}

func timePointerMillis(value *time.Time) *int64 {
	if value == nil {
		return nil
	}
	millis := value.UnixMilli()
	return &millis
}

// daemonSessionPorts 是会话族里**不依赖某一条连接**的那一半端口。
//
// 留在这里的只有 agentred 自己的事:账号可见性与归属判定住在各个 handler 里
// (ResolveSessionPeer + LoggedInAccountID),哪些方法存在、闸门怎么套、缺席怎么办
// 住在 wireinbound。
//
// 这一侧的 handler 自己就说 agentrewire,所以每一格都是**直接绑**,中间一次转换都
// 没有:补齐那一族的通知日志里存的本来就是这一帧的 protobuf 原样,翻成领域词表再翻
// 回来是每拉一行白走一个来回。
//
// Auth / Error 是端口而不是共用的一句话:agentred 的拒绝语是 rpcerror.ErrUnauthorized
// (大写 U),桌面端答小写的 "unauthorized";错误映射这一侧认 *rpcerror.Error,桌面端
// 还多认一种会话哨兵。两者都在线上,统一掉就是一次没人点头的协议改动。
func (d *Daemon) daemonSessionPorts() wireinbound.SessionPorts {
	return wireinbound.SessionPorts{
		Auth:               requireProtobufAuth,
		Error:              protobufError,
		List:               d.catchup.List,
		Counts:             d.catchup.Counts,
		ActivityRollup:     d.activity.ActivityRollup,
		Pull:               d.catchup.Pull,
		PendingWaiters:     d.catchup.PendingWaiters,
		Delete:             d.sessionDelete.Delete,
		SetModelTarget:     d.sessionModelTarget.SetModelTarget,
		SetReasoningEffort: d.sessionReasoningEffort.SetReasoningEffort,
	}
}

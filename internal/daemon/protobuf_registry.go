package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/daemon/auth"
	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/notifier"
	"github.com/agentre-hub/agentre/internal/daemon/protobufadapter"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/daemon/remotefs"
	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/daemon/transcriptimport"
	"github.com/agentre-hub/agentre/internal/daemon/workspacefs"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	remotewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/ccoauth"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
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
				conn.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: result.PeerFingerprint, AccountID: d.claimedAccountID()})
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
	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_ENGINE_TEST),
		func() *agentrewire.EngineTestRequest { return &agentrewire.EngineTestRequest{} },
		func(ctx context.Context, request *agentrewire.EngineTestRequest) (*agentrewire.EngineTestResponse, error) {
			if err := d.requireProtobufClaimed(ctx); err != nil {
				return nil, err
			}
			result, err := engineHandlers.Test(ctx, handlers.EngineTestParams{ProviderKey: request.ProviderKey, ModelKey: request.ModelKey})
			if err != nil {
				return nil, protobufError(err)
			}
			return &agentrewire.EngineTestResponse{Ok: result.OK, Message: result.Message, LatencyMs: result.LatencyMs}, nil
		})
	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_ENGINE_DISCOVER),
		func() *agentrewire.EngineDiscoverRequest { return &agentrewire.EngineDiscoverRequest{} },
		func(ctx context.Context, request *agentrewire.EngineDiscoverRequest) (*agentrewire.EngineDiscoverResponse, error) {
			if err := d.requireProtobufClaimed(ctx); err != nil {
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
	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_ENGINE_SCAN),
		func() *agentrewire.EngineScanRequest { return &agentrewire.EngineScanRequest{} },
		func(ctx context.Context, _ *agentrewire.EngineScanRequest) (*agentrewire.EngineScanResponse, error) {
			if err := d.requireProtobufClaimed(ctx); err != nil {
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
		})

	cliHandlers := handlers.NewCLIHandlers(d.gateway, NewProviderLookup(d.state))
	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_CLI_RESOLVE_PATH),
		func() *agentrewire.CLIResolvePathRequest { return &agentrewire.CLIResolvePathRequest{} },
		func(ctx context.Context, request *agentrewire.CLIResolvePathRequest) (*agentrewire.CLIResolvePathResponse, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			result, err := cliHandlers.ResolvePath(ctx, handlers.CLIResolvePathParams{Type: request.Type})
			if err != nil {
				return nil, protobufError(err)
			}
			return &agentrewire.CLIResolvePathResponse{Path: result.Path, Found: result.Found}, nil
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
			response := &agentrewire.HealthPingResponse{InstanceUuid: result.InstanceUUID, ServerTimeMs: result.ServerTimeMs, Capabilities: append([]string(nil), result.Capabilities...), DbSizeBytes: result.DBSizeBytes}
			for _, provider := range result.Providers {
				out := &agentrewire.HealthProvider{Key: provider.Key, Name: provider.Name, Type: provider.Type, DefaultModelKey: provider.DefaultModelKey}
				for _, model := range provider.Models {
					out.Models = append(out.Models, &agentrewire.HealthModel{Key: model.Key, ModelId: model.ModelID, Name: model.Name, Enabled: model.Enabled})
				}
				response.Providers = append(response.Providers, out)
			}
			return response, nil
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

	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_LIST),
		func() *agentrewire.SessionListRequest { return &agentrewire.SessionListRequest{} },
		func(ctx context.Context, request *agentrewire.SessionListRequest) (*agentrewire.SessionListResponse, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			result, err := d.catchup.List(ctx, request.GetKeyword())
			if err != nil {
				return nil, protobufError(err)
			}
			response := &agentrewire.SessionListResponse{}
			for _, session := range result.Sessions {
				response.Sessions = append(response.Sessions, &agentrewire.SessionSummary{ConversationId: session.ConversationID, PeerFingerprint: session.PeerFingerprint, AgentId: session.AgentID, Title: session.Title, AgentSyncId: session.AgentSyncID, ProviderSessionId: session.ProviderSessionID, Cwd: session.Cwd, ProjectSyncId: session.ProjectSyncID, BackendType: session.BackendType, LifecycleState: session.LifecycleState, WaitingForInput: session.WaitingForInput, LatestSeq: session.LatestSeq, LastMessageAt: session.LastMessageAt, ProviderKey: session.ProviderKey, ModelKey: session.ModelKey, ReasoningEffort: session.ReasoningEffort})
			}
			return response, nil
		})
	// 活跃统计的纯计数上报:回包里只有天、维度和一个计数,没有标题、路径与内容。
	// 服务端像拉 session.list 一样拉它;不带 since_day 的一次调用就是「回填」。
	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_ACTIVITY_ROLLUP),
		func() *agentrewire.ActivityRollupRequest { return &agentrewire.ActivityRollupRequest{} },
		func(ctx context.Context, req *agentrewire.ActivityRollupRequest) (*agentrewire.ActivityRollupResponse, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			buckets, err := d.activity.ActivityRollup(ctx, req.GetSinceDay(), req.GetTimeZone())
			if err != nil {
				return nil, protobufError(err)
			}
			response := &agentrewire.ActivityRollupResponse{Buckets: make([]*agentrewire.ActivityDailyBucket, 0, len(buckets))}
			for _, bucket := range buckets {
				response.Buckets = append(response.Buckets, &agentrewire.ActivityDailyBucket{
					Day: bucket.Day, AgentSyncId: bucket.AgentSyncID, BackendType: bucket.BackendType,
					ProviderKey: bucket.ProviderKey, ModelKey: bucket.ModelKey,
					ProjectSyncId: bucket.ProjectSyncID, SessionCount: bucket.SessionCount,
				})
			}
			return response, nil
		})
	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL),
		func() *agentrewire.SessionPullRequest { return &agentrewire.SessionPullRequest{} },
		func(ctx context.Context, request *agentrewire.SessionPullRequest) (*agentrewire.SessionPullResponse, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			result, err := d.catchup.Pull(ctx, remotewire.SessionPullParams{ConversationID: request.ConversationId, PeerFingerprint: request.PeerFingerprint, Cursor: request.Cursor, Limit: int(request.Limit)})
			if err != nil {
				return nil, protobufError(err)
			}
			response := &agentrewire.SessionPullResponse{Cursor: result.Cursor, HasMore: result.HasMore, OldestSeq: result.OldestSeq}
			for _, entry := range result.Notifications {
				journaled, err := protobufJournaledNotification(entry)
				if err != nil {
					return nil, protobufError(err)
				}
				response.Notifications = append(response.Notifications, journaled)
			}
			return response, nil
		})
	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_PENDING_WAITERS),
		func() *agentrewire.SessionPendingWaitersRequest { return &agentrewire.SessionPendingWaitersRequest{} },
		func(ctx context.Context, request *agentrewire.SessionPendingWaitersRequest) (*agentrewire.SessionPendingWaitersResponse, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			result, err := d.catchup.PendingWaiters(ctx, remotewire.SessionPendingWaitersParams{ConversationID: request.ConversationId, PeerFingerprint: request.PeerFingerprint})
			if err != nil {
				return nil, protobufError(err)
			}
			return protowire.PendingWaitersResponseToProto(result), nil
		})
	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_DELETE),
		func() *agentrewire.SessionDeleteRequest { return &agentrewire.SessionDeleteRequest{} },
		func(ctx context.Context, request *agentrewire.SessionDeleteRequest) (*agentrewire.SessionDeleteResponse, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			result, err := d.sessionDelete.Delete(ctx, remotewire.SessionDeleteParams{ConversationID: request.ConversationId, PeerFingerprint: request.PeerFingerprint})
			if err != nil {
				return nil, protobufError(err)
			}
			return &agentrewire.SessionDeleteResponse{Deleted: result.Deleted}, nil
		})
	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_SET_MODEL_TARGET),
		func() *agentrewire.SetModelTargetRequest { return &agentrewire.SetModelTargetRequest{} },
		func(ctx context.Context, request *agentrewire.SetModelTargetRequest) (*agentrewire.SetModelTargetResponse, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			_, err := d.sessionModelTarget.SetModelTarget(ctx, remotewire.SetModelTargetParams{ConversationID: request.ConversationId, PeerFingerprint: request.PeerFingerprint, ProviderKey: request.ProviderKey, ModelKey: request.ModelKey})
			if err != nil {
				return nil, protobufError(err)
			}
			return &agentrewire.SetModelTargetResponse{}, nil
		})
	protorpc.RegisterMethod(d.protobufRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_SET_SESSION_REASONING_EFFORT),
		func() *agentrewire.SetSessionReasoningEffortRequest {
			return &agentrewire.SetSessionReasoningEffortRequest{}
		},
		func(ctx context.Context, request *agentrewire.SetSessionReasoningEffortRequest) (*agentrewire.SetSessionReasoningEffortResponse, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			_, err := d.sessionReasoningEffort.SetReasoningEffort(ctx, remotewire.SetSessionReasoningEffortParams{ConversationID: request.ConversationId, PeerFingerprint: request.PeerFingerprint, ReasoningEffort: request.ReasoningEffort})
			if err != nil {
				return nil, protobufError(err)
			}
			return &agentrewire.SetSessionReasoningEffortResponse{}, nil
		})

	protobufadapter.RegisterPeripheralMethods(d.protobufRegistry, protobufadapter.PeripheralDeps{
		Skills:      handlers.NewSkillsHandlers(),
		RemoteFS:    remotefs.NewHandlers(remotefs.Options{}),
		WorkspaceFS: workspacefs.NewHandlers(workspacefs.Options{}),
		// 读取器由 runtime_imports.go 里那几个 blank import 在 init() 时注册进
		// internal/pkg/transcriptimport 的注册表 —— daemon 侧不写第二套解析。
		//
		// 执行侧接的是与跑一轮**同一份**存储:会话身份行落进 daemon_sessions、
		// 回放出的通知落进 daemon_notification_journal,导入的会话因此和别的会话一样
		// 被 SESSION_LIST / SESSION_PULL 服务出去,不需要第二条镜像通路。
		TranscriptImport: transcriptimport.NewHandlers(transcriptimport.Options{
			Sessions:         d.sessionStore,
			Journal:          d.journal,
			JournalPurge:     journalPurger{db: d.db},
			ClaimedAccountID: d.claimedAccountID,
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
	}
}

func (d *Daemon) requireProtobufClaimed(ctx context.Context) error {
	if err := requireProtobufAuth(ctx); err != nil {
		return err
	}
	if !d.state.IsClaimed() {
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

// protobufJournaledNotification 把补齐交出的一行投影到线上的载体。
//
// 单独一个函数而不是留在注册闭包里,是因为这一跳有三样东西必须一起对:seq 盖进载荷
// (客户端按 method 解出的帧里没有它)、seq 留在载体上,以及**发生时刻**原样转交。
// 时刻是最容易在这类逐字段搬运里被漏掉的一样,而漏掉之后没有任何东西会报错 ——
// 下游只是安静地少一列,要到浏览器控制台的转录上才看得出来。
func protobufJournaledNotification(entry remotewire.JournaledNotification) (*agentrewire.JournaledNotification, error) {
	notification, err := protowire.WireNotificationToProto(entry.Method, entry.Params)
	if err != nil {
		return nil, err
	}
	protowire.SetNotificationSeq(notification, entry.Seq)
	return &agentrewire.JournaledNotification{
		Seq:     entry.Seq,
		Payload: notification,
		// 报不出时刻的对端交出 0,这里照样转交 0:「不知道」不能在中途被补成当下。
		Createtime: entry.Createtime,
	}, nil
}

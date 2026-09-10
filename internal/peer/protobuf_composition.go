package peer

import (
	"context"
	"fmt"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/remotefs"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/activityrollup"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	remotewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/wireinbound"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

func productionProtobufInboundDeps() ProtobufInboundDeps {
	adapter := func() inboundSessionAdapter { value, _ := chat_svc.Chat().(inboundSessionAdapter); return value }
	return ProtobufInboundDeps{
		// 决策 8:入站对端的身份从**已验签的凭据**取,而不是它自己说的那个。
		VerifyAccountCredential: func(ctx context.Context, credential string) (string, error) {
			return verifyInboundAccountCredential(ctx, credential)
		},
		// 只答「这台机器上这个 backend 的能力矩阵是什么」。折成线格式那一步与
		// agentred 逐字相同,已经收进 wireinbound —— 这里不再手抄一份。
		Capabilities: func(_ context.Context, params remotewire.CapabilitiesParams) (remotewire.CapabilitiesResult, error) {
			runtime := agentruntime.RuntimeFor(agent_backend_entity.BackendType(params.BackendType))
			if runtime == nil {
				// 没注册的 backend 如实报错,而不是回一份空矩阵成功:空矩阵会被
				// 界面读成「这个 backend 没有权限档位」。
				return remotewire.CapabilitiesResult{}, fmt.Errorf("no runtime registered for backend type %q", params.BackendType)
			}
			return remotewire.CapabilitiesResult{Capabilities: runtime.Capabilities()}, nil
		},
		Peripheral: wireinbound.PeripheralDeps{
			Skills: handlers.NewSkillsHandlers(), RemoteFS: remotefs.NewHandlers(remotefs.Options{}),
			TranscriptImport: newDesktopTranscriptImport(),
			ProjectSetPath:   protobufProjectSetPath, ProjectClearPath: protobufProjectClearPath,
		},
		ListSessions: func(ctx context.Context, params remotewire.SessionListParams) (*remotewire.SessionListResult, error) {
			return adapter().ListPeerSessions(ctx, params)
		},
		CountSessions: func(ctx context.Context) (*remotewire.SessionCountsResult, error) {
			return adapter().CountPeerSessions(ctx)
		},
		ActivityRollup: func(ctx context.Context, sinceDay, timeZone string) ([]activityrollup.Bucket, error) {
			return adapter().ActivityRollup(ctx, sinceDay, timeZone)
		},
		AttachSession: func(ctx context.Context, params remotewire.SessionAttachParams, subscriber chat_svc.PeerSessionSubscriber) (remotewire.SessionAttachResult, error) {
			return adapter().AttachPeerSession(ctx, params, subscriber)
		},
		PullSession: func(ctx context.Context, params remotewire.SessionPullParams, subscriber chat_svc.PeerSessionSubscriber) (remotewire.SessionPullResult, error) {
			return adapter().PullPeerSession(ctx, params, subscriber)
		},
		PendingWaiters: func(ctx context.Context, params remotewire.SessionPendingWaitersParams) (remotewire.SessionPendingWaitersResult, error) {
			return adapter().PendingPeerSessionWaiters(ctx, params)
		},
		DeleteSession: func(ctx context.Context, conversationID string, peerFingerprint string) error {
			if err := requireOwnOrigin(peerFingerprint); err != nil {
				return err
			}
			sessionID, err := chat_svc.ResolvePeerConversation(ctx, conversationID)
			if err != nil {
				return err
			}
			_, err = adapter().Delete(ctx, &chat_svc.DeleteRequest{SessionID: sessionID})
			return err
		},
		SetModelTarget: func(ctx context.Context, conversationID string, providerKey, modelKey string) error {
			sessionID, err := chat_svc.ResolvePeerConversation(ctx, conversationID)
			if err != nil {
				return err
			}
			_, err = adapter().SetChatSessionModelTarget(ctx, &chat_svc.SetChatSessionModelTargetRequest{SessionID: sessionID, ProviderKey: providerKey, ModelKey: modelKey})
			return err
		},
		SetReasoningEffort: func(ctx context.Context, conversationID string, reasoningEffort string) error {
			sessionID, err := chat_svc.ResolvePeerConversation(ctx, conversationID)
			if err != nil {
				return err
			}
			_, err = adapter().SetChatSessionReasoningEffort(ctx, &chat_svc.SetChatSessionReasoningEffortRequest{SessionID: sessionID, ReasoningEffort: reasoningEffort})
			return err
		},
		SetPermissionMode: func(ctx context.Context, conversationID string, mode string) error {
			sessionID, err := chat_svc.ResolvePeerConversation(ctx, conversationID)
			if err != nil {
				return err
			}
			_, err = adapter().SetPermissionMode(ctx, &chat_svc.SetPermissionModeRequest{SessionID: sessionID, Mode: mode})
			return err
		},
		RunSession: func(ctx context.Context, params remotewire.RunParams, source chat_svc.PeerSessionSource) (*chat_svc.SendResponse, error) {
			return adapter().RunPeerSession(ctx, params, source)
		},
		SteerSession: func(ctx context.Context, params remotewire.SteerParams, source chat_svc.PeerSessionSource) (*chat_svc.EnqueueResponse, error) {
			return adapter().EnqueuePeerSession(ctx, params, source)
		},
		CancelSteerSession: func(ctx context.Context, params remotewire.CancelSteerParams) (*chat_svc.CancelQueuedResponse, error) {
			return adapter().CancelPeerSessionQueued(ctx, params)
		},
		SubmitAnswer: func(ctx context.Context, params remotewire.SubmitAnswerParams) (chat_svc.PeerSessionControlResult, error) {
			return adapter().AnswerPeerUserQuestion(ctx, params)
		},
		SubmitToolPermission: func(ctx context.Context, params remotewire.SubmitToolPermissionParams) (chat_svc.PeerSessionControlResult, error) {
			return adapter().AnswerPeerToolPermission(ctx, params)
		},
	}
}

func protobufProjectSetPath(ctx context.Context, request *agentrewire.ProjectSetLocalPathRequest) (*agentrewire.ProjectLocalPathResponse, error) {
	if request.ProjectSyncId == "" {
		return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "project sync id required"}
	}
	id, err := localProjectID(ctx, request.ProjectSyncId)
	if err != nil {
		return nil, wireinbound.ConvertError(err)
	}
	value, err := project_svc.Default().SetLocalPath(ctx, id, request.Path)
	if err != nil {
		return nil, wireinbound.ConvertError(projectPathError(err))
	}
	reportLocalPaths(ctx)
	return &agentrewire.ProjectLocalPathResponse{Path: value.Path, Configured: !value.LocalPathMissing}, nil
}

func protobufProjectClearPath(ctx context.Context, request *agentrewire.ProjectClearLocalPathRequest) (*agentrewire.ProjectLocalPathResponse, error) {
	if request.ProjectSyncId == "" {
		return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "project sync id required"}
	}
	id, err := localProjectID(ctx, request.ProjectSyncId)
	if err != nil {
		return nil, wireinbound.ConvertError(err)
	}
	value, err := project_svc.Default().ClearLocalPath(ctx, id)
	if err != nil {
		return nil, wireinbound.ConvertError(projectPathError(err))
	}
	reportLocalPaths(ctx)
	return &agentrewire.ProjectLocalPathResponse{Path: value.Path, Configured: !value.LocalPathMissing}, nil
}

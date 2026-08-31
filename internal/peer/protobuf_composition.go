package peer

import (
	"context"
	"fmt"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/protobufadapter"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/daemon/remotefs"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/activityrollup"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	remotewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func productionProtobufInboundDeps() ProtobufInboundDeps {
	adapter := func() inboundSessionAdapter { value, _ := chat_svc.Chat().(inboundSessionAdapter); return value }
	return ProtobufInboundDeps{
		Capabilities: func(_ context.Context, backendType string) (*agentrewire.RuntimeCapabilitiesResponse, error) {
			runtime := agentruntime.RuntimeFor(agent_backend_entity.BackendType(backendType))
			if runtime == nil {
				return nil, fmt.Errorf("no runtime registered for backend type %q", backendType)
			}
			capabilities := runtime.Capabilities()
			response := &agentrewire.RuntimeCapabilitiesResponse{PermissionMode: &agentrewire.PermissionModeMeta{AllowedModes: capabilities.PermissionModeMeta.AllowedModes, DefaultMode: capabilities.PermissionModeMeta.DefaultMode, SwitchableDuringTurn: capabilities.PermissionModeMeta.SwitchableDuringTurn, Order: capabilities.PermissionModeMeta.Order, LaunchDefaultMode: capabilities.PermissionModeMeta.LaunchDefaultMode}}
			for name, enabled := range capabilities.Set {
				response.Capabilities = append(response.Capabilities, &agentrewire.CapabilityEntry{Name: string(name), Enabled: enabled})
			}
			return response, nil
		},
		Peripheral: protobufadapter.PeripheralDeps{
			Skills: handlers.NewSkillsHandlers(), RemoteFS: remotefs.NewHandlers(remotefs.Options{}),
			ProjectSetPath: protobufProjectSetPath, ProjectClearPath: protobufProjectClearPath,
		},
		ListSessions: func(ctx context.Context, keyword string) (*remotewire.SessionListResult, error) {
			return adapter().ListPeerSessions(ctx, keyword)
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
		DeleteSession: func(ctx context.Context, sessionID int64, peerFingerprint string) error {
			if err := requireOwnOrigin(peerFingerprint); err != nil {
				return err
			}
			_, err := adapter().Delete(ctx, &chat_svc.DeleteRequest{SessionID: sessionID})
			return err
		},
		SetModelTarget: func(ctx context.Context, sessionID int64, providerKey, modelKey string) error {
			_, err := adapter().SetChatSessionModelTarget(ctx, &chat_svc.SetChatSessionModelTargetRequest{SessionID: sessionID, ProviderKey: providerKey, ModelKey: modelKey})
			return err
		},
		SetPermissionMode: func(ctx context.Context, sessionID int64, mode string) error {
			_, err := adapter().SetPermissionMode(ctx, &chat_svc.SetPermissionModeRequest{SessionID: sessionID, Mode: mode})
			return err
		},
		RunSession: func(ctx context.Context, params remotewire.RunParams, source chat_svc.PeerSessionSource) (*chat_svc.SendResponse, error) {
			return adapter().RunPeerSession(ctx, params, source)
		},
		SteerSession: func(ctx context.Context, params remotewire.SteerParams, source chat_svc.PeerSessionSource) error {
			_, err := adapter().EnqueuePeerSession(ctx, params, source)
			return err
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
		return nil, protobufadapter.ConvertError(err)
	}
	value, err := project_svc.Default().SetLocalPath(ctx, id, request.Path)
	if err != nil {
		return nil, protobufadapter.ConvertError(projectPathError(err))
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
		return nil, protobufadapter.ConvertError(err)
	}
	value, err := project_svc.Default().ClearLocalPath(ctx, id)
	if err != nil {
		return nil, protobufadapter.ConvertError(projectPathError(err))
	}
	reportLocalPaths(ctx)
	return &agentrewire.ProjectLocalPathResponse{Path: value.Path, Configured: !value.LocalPathMissing}, nil
}

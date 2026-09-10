package peer

import (
	"context"
	"fmt"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/remotefs"
	"github.com/agentre-hub/agentre/internal/daemon/workspacefs"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/activityrollup"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	remotewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/wireinbound"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
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
		// 引擎探测一族的族闸门就是「这条连接已鉴权」——— 与桌面端其余入站方法同一道。
		// agentred 那一侧 engine.* 另外要求账号已登录,那是它自己的端口说的话。
		Engine: wireinbound.EngineDeps{
			Auth:           wireinbound.RequireAuthenticated,
			Scan:           desktopEngineScan,
			Test:           newDesktopEngineTest(desktopEngineProviders{}),
			ResolveCLIPath: desktopResolveCLIPath,
		},
		Peripheral: wireinbound.PeripheralDeps{
			Skills: handlers.NewSkillsHandlers(), RemoteFS: remotefs.NewHandlers(remotefs.Options{}),
			// WorkspaceFS 与 RemoteFS 同一形状:零配置构造,与 agentred 那一侧
			// (internal/daemon/protobuf_registry.go)供的是**同一份** handler ——
			// 「这台机器上那个工作目录里有什么」两种执行端答话逐字相同,不该有两套。
			//
			// 挂的是整族七个而不是控制台要的那两个:注册面的闸门单位就是端口
			// (端口缺席 ⇒ 整族不注册 ⇒ 调用方收到 method not found),按方法拆会
			// 给两种执行端共用的那份注册面新开一条「哪几个」的轴,而没有任何调用方
			// 在要它。多出来的五个(listDir / searchFiles / gitBranches / gitState /
			// gitChanges)全是只读的目录与 git 视图,权限上严格弱于同族的 readFile —— 后者
			// 是控制台必须要的,已经能读出正文;少挂那五个换不来任何收窄。
			WorkspaceFS:      workspacefs.NewHandlers(workspacefs.Options{}),
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
		DeleteSession: func(ctx context.Context, conversationID string, peerFingerprint devicefp.Initiator) error {
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
		// AbortSession 停掉这一条会话正在跑的那一轮。
		//
		// **不设 requireOwnOrigin**,与同族的 run / steer / 回答提问一致:停一轮是轮内
		// 控制,谁在看这条会话谁就该停得下来。Delete 那一条要求自己是发起端,是因为
		// 它销毁数据 —— 两者不是同一类动作(agentred 那一侧同样只解析对端、不限发起端)。
		AbortSession: func(ctx context.Context, conversationID string) error {
			sessionID, err := chat_svc.ResolvePeerConversation(ctx, conversationID)
			if err != nil {
				return err
			}
			_, err = adapter().Stop(ctx, &chat_svc.StopRequest{SessionID: sessionID})
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

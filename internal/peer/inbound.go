// Package peer owns the desktop's inbound, session-level peer surface.
package peer

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/daemon/relaytransport"
	"github.com/agentre-hub/agentre/internal/pkg/activityrollup"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
)

type inboundSessionAdapter interface {
	ListPeerSessions(ctx context.Context, keyword string) (*wire.SessionListResult, error)
	ActivityRollup(context.Context, string, string) ([]activityrollup.Bucket, error)
	AttachPeerSession(context.Context, wire.SessionAttachParams, chat_svc.PeerSessionSubscriber) (wire.SessionAttachResult, error)
	PullPeerSession(context.Context, wire.SessionPullParams, chat_svc.PeerSessionSubscriber) (wire.SessionPullResult, error)
	PendingPeerSessionWaiters(context.Context, wire.SessionPendingWaitersParams) (wire.SessionPendingWaitersResult, error)
	Delete(context.Context, *chat_svc.DeleteRequest) (*chat_svc.DeleteResponse, error)
	RunPeerSession(context.Context, wire.RunParams, chat_svc.PeerSessionSource) (*chat_svc.SendResponse, error)
	EnqueuePeerSession(context.Context, wire.SteerParams, chat_svc.PeerSessionSource) (*chat_svc.EnqueueResponse, error)
	AnswerPeerUserQuestion(context.Context, wire.SubmitAnswerParams) (chat_svc.PeerSessionControlResult, error)
	AnswerPeerToolPermission(context.Context, wire.SubmitToolPermissionParams) (chat_svc.PeerSessionControlResult, error)
	SetPermissionMode(context.Context, *chat_svc.SetPermissionModeRequest) (*chat_svc.SetPermissionModeResponse, error)
	SetChatSessionModelTarget(context.Context, *chat_svc.SetChatSessionModelTargetRequest) (*chat_svc.SetChatSessionModelTargetResponse, error)
	SetChatSessionReasoningEffort(context.Context, *chat_svc.SetChatSessionReasoningEffortRequest) (*chat_svc.SetChatSessionReasoningEffortResponse, error)
}

// Inbound keeps the desktop registered through one reconnecting relay link and
// turns relay-initiated virtual channels into private Protobuf RPC registries.
type Inbound struct {
	link             *relaytransport.HubLink
	mux              *relaytransport.Multiplexer
	protobufRegistry *protorpc.Registry
}

func NewInbound(link *relaytransport.HubLink) *Inbound {
	return &Inbound{
		link:             link,
		mux:              relaytransport.NewMultiplexer(link),
		protobufRegistry: NewProtobufInboundRegistry(productionProtobufInboundDeps()),
	}
}

func (p *Inbound) Run(ctx context.Context) error {
	defer p.mux.Close()
	go p.serve(ctx)
	return p.link.Run(ctx)
}

func (p *Inbound) serve(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case channel := <-p.mux.Accept():
			if channel == nil {
				return
			}
			if channel.ID() == relaytransport.SignalChannelID {
				go drainReservedChannel(channel)
				continue
			}
			conn := protorpc.NewConn(protorpc.NewPayloadFrameConn(channel), p.protobufRegistry.Clone())
			go conn.Serve(ctx)
		}
	}
}

// drainReservedChannel 读掉并丢弃保留通道上的帧（决策 13/14）。
//
// 这条入站链路拨的是与 agentred 同一个端点（/v1/relay/daemon），而服务端在那个
// 端点上给每一条连接都开一条保留通道运送账号信号。桌面端在这里**不消费**它：
// 账号信号由这台桌面端唯一的常驻中继客户端连接送达（server_svc 的
// residentRelay.serveSignal），在入站链路上再消费一份只会让同一条信号触发两次
// 同步。要紧的是它绝不能被包成一条 RPC 连接——保留通道只出不进，服务端也从不在
// 它上面完成握手，把它交给注册表就等于让一条没有鉴权的通道去答 RPC。
//
// 读到底而不是当场 Close：关掉它会让服务端那侧的信号写入失败，而这条通道对本端
// 无害；连接收尾时 mux 会一并清掉它。
func drainReservedChannel(channel relaytransport.PayloadChannel) {
	for {
		if _, err := channel.ReadPayload(); err != nil {
			return
		}
	}
}

func localProjectID(ctx context.Context, syncID string) (int64, error) {
	id, err := syncstate_repo.SyncState().FindLocalID(ctx, syncwire.KindProject, syncID)
	if err != nil {
		return 0, &protorpc.Error{Code: protorpc.CodeInternal, Message: err.Error()}
	}
	if id == 0 {
		return 0, &protorpc.Error{Code: wire.ErrCodeProjectNotSynced, Message: "project not synced to this machine"}
	}
	return id, nil
}

func projectPathError(err error) error {
	var httpErr *httputils.Error
	if errors.As(err, &httpErr) {
		switch httpErr.Code {
		case code.ProjectNotFound:
			return &protorpc.Error{Code: wire.ErrCodeProjectNotSynced, Message: httpErr.Msg}
		case code.ProjectInvalidPath:
			return &protorpc.Error{Code: wire.ErrCodeProjectInvalidPath, Message: httpErr.Msg}
		case code.ProjectPathNotExist:
			return &protorpc.Error{Code: wire.ErrCodeProjectPathNotFound, Message: httpErr.Msg}
		}
	}
	return &protorpc.Error{Code: protorpc.CodeInternal, Message: err.Error()}
}

func reportLocalPaths(ctx context.Context) {
	if err := sync_svc.ReportLocalPathsNow(ctx); err != nil {
		logger.Ctx(ctx).Debug("peer.reportLocalPaths: immediate report failed, polling will catch up", zap.Error(err))
	}
}

func requireOwnOrigin(originPeer string) error {
	if originPeer == "" {
		return nil
	}
	device := remote_device_svc.Default()
	if device == nil {
		return &protorpc.Error{Code: -32001, Message: "unauthorized"}
	}
	fingerprint, err := device.DeviceFingerprint()
	if err != nil || fingerprint == "" || fingerprint != originPeer {
		return &protorpc.Error{Code: -32001, Message: "unauthorized"}
	}
	return nil
}

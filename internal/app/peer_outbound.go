package app

import (
	"context"
	"errors"
	"time"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/service/peer_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/server_svc"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// errPeerServiceUnavailable 是 peer_svc 未接线（Startup 前调用 / 测试缺 wiring）时的
// 失败哨兵。绑定层面对 nil 服务 fail-open，绝不 panic。
var errPeerServiceUnavailable = errors.New("peer service is not available")

// peerSvc 是出站对端客户端的访问器，绑定层不直接 import 服务实现。
var peerSvcAccessor = peer_svc.Default

// registerPeerService 在 composition root 注入 peer_svc 的默认实现并把实时事件接到
// Wails EventsEmit（前端用 peer_svc.EventName 订阅）。在 Startup 里 server_svc /
// remote_device_svc / agent_repo / project_repo 均已就绪后调用。
func (a *App) registerPeerService() {
	// 批量广播:一批帧一次 EventsEmit,而不是一帧一次。帧本身逐字不动、seq 与顺序
	// 原样保留(理由见 peerEventBatcher),所以前端的去重口径不变,只是改成按批消费。
	batcher := newPeerEventBatcher(
		peerEventFlushWindow,
		// 定时器一次性用完即弃(每批重新排一次),句柄没有人需要,丢掉即可。
		func(d time.Duration, f func()) { _ = time.AfterFunc(d, f) },
		func(batch []peer_svc.PeerEvent) {
			wailsruntime.EventsEmit(a.ctx, peer_svc.EventName, batch)
		},
	)
	svc := peer_svc.New(
		serverSvcDialer{},
		peerEmitterFunc(batcher.Emit),
		remoteDeviceFingerprintProvider{},
		agentRepoAccessor{},
		projectRepoAccessor{},
	)
	peer_svc.SetDefault(svc)
}

// ── 生产适配器：peer_svc 端口 → 既有单例访问器 ──────────────────────────────

// serverSvcDialer 是 peer_svc.Dialer 的生产实现：经账号中继拨号（DialDesktopRelay）。
type serverSvcDialer struct{}

func (serverSvcDialer) DialDesktopRelay(ctx context.Context, desktopFingerprint, peerFingerprint string) (client.ProtobufConnection, error) {
	return server_svc.Server().DialDesktopRelay(ctx, desktopFingerprint, peerFingerprint)
}

// remoteDeviceFingerprintProvider 是 peer_svc.FingerprintProvider 的生产实现。
type remoteDeviceFingerprintProvider struct{}

func (remoteDeviceFingerprintProvider) DeviceFingerprint() (string, error) {
	return remote_device_svc.Default().DeviceFingerprint()
}

type peerEmitterFunc func(e peer_svc.PeerEvent)

func (f peerEmitterFunc) Emit(e peer_svc.PeerEvent) { f(e) }

// agentRepoAccessor 是 peer_svc.AgentLookup 的生产实现。
type agentRepoAccessor struct{}

func (agentRepoAccessor) Find(ctx context.Context, id int64) (*agent_entity.Agent, error) {
	return agent_repo.Agent().Find(ctx, id)
}

// projectRepoAccessor 是 peer_svc.ProjectLookup 的生产实现。
type projectRepoAccessor struct{}

func (projectRepoAccessor) Find(ctx context.Context, id int64) (*project_entity.Project, error) {
	return project_repo.Project().Find(ctx, id)
}

// ── Wails 绑定：每一条都是 parse → peer_svc.Default().X → return 的薄透传 ──

// PeerListSessions 列出一台远端桌面端上的全部会话（R19 设备页下钻）。
func (a *App) PeerListSessions(fingerprint string) (*wire.SessionListResult, error) {
	if svc := peerSvcAccessor(); svc != nil {
		return svc.ListSessions(a.ctx, fingerprint)
	}
	return nil, errPeerServiceUnavailable
}

// PeerRunFresh 把一条新对话派到一台具名桌面端上并跑首轮（R18），返回对端真实会话 id。
func (a *App) PeerRunFresh(req peer_svc.RunFreshRequest) (wire.RunAck, error) {
	if svc := peerSvcAccessor(); svc != nil {
		return svc.RunFresh(a.ctx, req)
	}
	return wire.RunAck{}, errPeerServiceUnavailable
}

// PeerAttach 接入远端会话并开始接收实时流（R19），返回高水位游标。
func (a *App) PeerAttach(req peer_svc.AttachRequest) (*wire.SessionAttachResult, error) {
	if svc := peerSvcAccessor(); svc != nil {
		return svc.Attach(a.ctx, req)
	}
	return nil, errPeerServiceUnavailable
}

// PeerPull 拉一页远端会话历史（R19 / R7）。
func (a *App) PeerPull(req peer_svc.PullRequest) (*wire.SessionPullResult, error) {
	if svc := peerSvcAccessor(); svc != nil {
		return svc.Pull(a.ctx, req)
	}
	return nil, errPeerServiceUnavailable
}

// PeerSteer 向已接入的远端会话发一条新消息（R19 / R9）。
func (a *App) PeerSteer(req peer_svc.SteerRequest) error {
	if svc := peerSvcAccessor(); svc != nil {
		return svc.Steer(a.ctx, req)
	}
	return errPeerServiceUnavailable
}

// PeerSubmitAnswer 回答远端会话上挂起的提问（R10），AlreadyHandled 如实上浮。
func (a *App) PeerSubmitAnswer(req peer_svc.SubmitAnswerRequest) (*wire.PeerSessionControlResult, error) {
	if svc := peerSvcAccessor(); svc != nil {
		return svc.SubmitAnswer(a.ctx, req)
	}
	return nil, errPeerServiceUnavailable
}

// PeerSubmitToolPermission 决定远端会话上挂起的工具权限（R10）。
func (a *App) PeerSubmitToolPermission(req peer_svc.SubmitToolPermissionRequest) (*wire.PeerSessionControlResult, error) {
	if svc := peerSvcAccessor(); svc != nil {
		return svc.SubmitToolPermission(a.ctx, req)
	}
	return nil, errPeerServiceUnavailable
}

// PeerDetach 结束本端对一条远端会话的接入（R19：关闭 Tab 只结束本端接入，不删除
// 对端会话）。
func (a *App) PeerDetach(fingerprint string, sessionID int64) error {
	if svc := peerSvcAccessor(); svc != nil {
		return svc.Detach(a.ctx, fingerprint, sessionID)
	}
	return errPeerServiceUnavailable
}

// PeerClose 关闭全部对端中继连接（App 退出时调用）。
func (a *App) PeerClose() error {
	if svc := peerSvcAccessor(); svc != nil {
		return svc.Close()
	}
	return nil
}

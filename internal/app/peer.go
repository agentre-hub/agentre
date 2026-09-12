package app

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/peer"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
)

// inboundPeer is the App-lifetime boundary for the desktop's relay presence.
type inboundPeer interface {
	Run(context.Context) error
}

var newInboundPeer = func(ctx context.Context) (inboundPeer, error) {
	link, err := server_svc.Server().NewInboundHubLink(ctx)
	if err != nil {
		return nil, err
	}
	return peer.NewInbound(link), nil
}

// dropAccountChannel 把账号级实时通道踢掉重连。抽成变量供测试替换，与
// newInboundPeer 同一模式；同步未装配（单机构建 / 单元测试）时是空操作。
var dropAccountChannel = func() {
	if s := sync_svc.Default(); s != nil {
		s.DropAccountChannel()
	}
}

// discardAdoptedDevices 去掉收编来的设备行。同样抽成变量供测试替换；设备服务未
// 装配时是空操作。失败只记日志：它是清理，不该让登出这条路径本身失败。
var discardAdoptedDevices = func(ctx context.Context) {
	svc := remote_device_svc.Default()
	if svc == nil {
		return
	}
	if _, err := svc.DiscardAdoptedDevices(ctx); err != nil {
		logger.Ctx(ctx).Warn("app.onServerStateEvent: discarding adopted account agentreds failed", zap.Error(err))
	}
}

// onServerStateEvent 按登录状态的变更维护这台桌面端**挂在服务端上的两条连接**：
// 入站中转登记与账号级实时通道。两条都在建立时绑定了服务端地址与设备凭据，因此
// 都得跟着登录状态走。
func (a *App) onServerStateEvent(payload any) {
	state, ok := payload.(map[string]any)
	if !ok {
		return
	}
	switch state["kind"] {
	case "logged_in":
		// 重建而不是「已经有了就跳过」：登记在建立时就绑定了服务端地址与设备凭据，
		// 而登录事件恰恰意味着这两样可能都变了。跳过等于继续以上一次登录的身份挂在
		// 上一个服务端上 —— 新账号那边看不到这台桌面端，旧账号那边它还在线。
		a.restartInboundPeer(context.Background())
		dropAccountChannel()
	case "logged_out":
		// 登记挂在 context.Background() 上，不主动停就要一直活到应用退出：已经登出的
		// 桌面端仍以旧凭据在旧服务端上保持可寻址。
		a.stopInboundPeer(context.Background())
		dropAccountChannel()
		// 收编来的设备行的依据就是「账号说有这台机器」，账号断了依据就没了。
		discardAdoptedDevices(context.Background())
	}
}

// restartInboundPeer 先停掉现有登记，再按当前登录状态重建。
//
// peerRestartMu 让相邻的两次登录状态变更不会交错成「停 A、起 A、停 B、起 B」之外的
// 顺序：stopInboundPeer 与 startInboundPeer 各自只在自己的临界区里持锁，中间那一瞬
// 若被另一个事件插入，就会留下一条没人认领的登记 —— 正是这次要修掉的东西。
func (a *App) restartInboundPeer(ctx context.Context) {
	a.peerRestartMu.Lock()
	defer a.peerRestartMu.Unlock()
	a.stopInboundPeer(ctx)
	a.startInboundPeer(ctx)
}

// startInboundPeer begins at most one inbound relay registration for this App
// process. A fresh login has no registration until the login event retries this
// method; an already-persisted login may start before token refresh completes,
// because HubLink re-resolves the token on every reconnect.
func (a *App) startInboundPeer(ctx context.Context) {
	a.peerMu.Lock()
	defer a.peerMu.Unlock()
	if a.peerCancel != nil {
		// A registration whose Run has already returned is dead even if its
		// cleanup goroutine has not yet cleared the lifecycle state. Rebuild on
		// this login instead of dropping it on the stale cancel token (the login
		// can land between Run-return and cleanup); the dead goroutine's
		// identity check in startInboundPeer's cleanup will not clear a fresh
		// registration.
		select {
		case <-a.peerDone:
			a.peerCancel = nil
			a.peerDone = nil
		default:
			return
		}
	}
	inbound, err := newInboundPeer(ctx)
	if errors.Is(err, server_svc.ErrNotLoggedIn) {
		return
	}
	if err != nil {
		logger.Ctx(ctx).Warn("app.startInboundPeer: create inbound relay", zap.Error(err))
		return
	}
	peerCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	a.peerCancel = cancel
	a.peerDone = done
	go func() {
		defer close(done)
		if runErr := inbound.Run(peerCtx); runErr != nil {
			logger.Default().Warn("app.startInboundPeer: inbound relay stopped", zap.Error(runErr))
		}
		// HubLink 永久退出（如重试时钟崩溃）后，清掉本登记的 cancel/done：否则
		// peerCancel != nil 会让之后的 logged_in 事件永远无法重建入站登记。若登记
		// 已被 stopInboundPeer 清空或被更新的登记替换，身份比对失败、不重复清理。
		a.peerMu.Lock()
		if a.peerDone == done {
			a.peerCancel = nil
			a.peerDone = nil
		}
		a.peerMu.Unlock()
	}()
}

// stopInboundPeer cancels the relay connection and waits for HubLink to close
// its WebSocket, so a completed App shutdown is no longer addressable.
func (a *App) stopInboundPeer(_ context.Context) {
	a.peerMu.Lock()
	cancel, done := a.peerCancel, a.peerDone
	a.peerCancel = nil
	a.peerDone = nil
	a.peerMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

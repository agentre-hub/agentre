package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

type inboundPeerStub struct {
	started chan struct{}
	stopped chan struct{}
}

func (p *inboundPeerStub) Run(ctx context.Context) error {
	close(p.started)
	<-ctx.Done()
	close(p.stopped)
	return nil
}

// Given App owns a started inbound relay peer, when Shutdown runs, then it
// cancels the peer lifetime and waits for the relay registration to disappear.
func TestAppInboundPeer_GivenRunning_WhenShutdown_ThenStopsBeforeReturning(t *testing.T) {
	stub := &inboundPeerStub{started: make(chan struct{}), stopped: make(chan struct{})}
	previous := newInboundPeer
	newInboundPeer = func(context.Context) (inboundPeer, error) { return stub, nil }
	t.Cleanup(func() { newInboundPeer = previous })

	app := &App{}
	app.startInboundPeer(context.Background())
	select {
	case <-stub.started:
	case <-time.After(time.Second):
		t.Fatal("App did not start its inbound relay peer")
	}

	app.stopInboundPeer(context.Background())
	select {
	case <-stub.stopped:
	case <-time.After(time.Second):
		t.Fatal("App returned before the inbound relay peer stopped")
	}
}

// Given the inbound relay peer exits permanently (its HubLink retry clock gave
// up, so Run returned without a shutdown), when a later login event calls
// startInboundPeer again, then a fresh registration is created rather than
// skipped by the stale cancel token left behind by the dead peer.
func TestAppInboundPeer_GivenPermanentExit_ThenLaterLoginRestartsRegistration(t *testing.T) {
	firstExited := make(chan struct{})
	second := &inboundPeerStub{started: make(chan struct{}), stopped: make(chan struct{})}
	calls := 0
	previous := newInboundPeer
	newInboundPeer = func(context.Context) (inboundPeer, error) {
		calls++
		if calls == 1 {
			return &permanentlyExitingInboundPeer{exited: firstExited}, nil
		}
		return second, nil
	}
	t.Cleanup(func() { newInboundPeer = previous })

	app := &App{}
	app.startInboundPeer(context.Background())
	select {
	case <-firstExited:
	case <-time.After(time.Second):
		t.Fatal("first inbound relay did not exit permanently")
	}
	requireEventuallyNil(t, func() bool {
		app.peerMu.Lock()
		defer app.peerMu.Unlock()
		return app.peerCancel == nil
	}, "dead relay must clear its lifecycle state so a later login can rebuild it")

	app.startInboundPeer(context.Background())
	select {
	case <-second.started:
	case <-time.After(time.Second):
		t.Fatal("later login did not restart the inbound relay after permanent exit")
	}
	app.stopInboundPeer(context.Background())
}

// Given a registration whose Run already returned (done closed) but whose
// cleanup goroutine has not yet cleared the lifecycle state, when a login event
// calls startInboundPeer in that window, then the login must not be dropped on
// the stale cancel token: a fresh registration is created. This is the narrow
// race window the permanent-exit fix leaves open — login lands between
// Run-return and cleanup.
func TestAppInboundPeer_GivenDeadRegistrationWithStaleCancel_WhenStart_ThenRebuilds(t *testing.T) {
	first := &inboundPeerStub{started: make(chan struct{}), stopped: make(chan struct{})}
	second := &inboundPeerStub{started: make(chan struct{}), stopped: make(chan struct{})}
	calls := 0
	previous := newInboundPeer
	newInboundPeer = func(context.Context) (inboundPeer, error) {
		calls++
		if calls == 1 {
			return first, nil
		}
		return second, nil
	}
	t.Cleanup(func() { newInboundPeer = previous })

	app := &App{}
	app.startInboundPeer(context.Background())
	select {
	case <-first.started:
	case <-time.After(time.Second):
		t.Fatal("App did not start its inbound relay peer")
	}
	// Simulate the cleanup window: Run has returned (done closed) but the
	// cleanup goroutine has not yet cleared peerCancel/peerDone — the exact
	// stale-cancel state a concurrent login event races with.
	app.peerMu.Lock()
	close(app.peerDone)
	app.peerMu.Unlock()

	app.startInboundPeer(context.Background())
	select {
	case <-second.started:
	case <-time.After(time.Second):
		t.Fatal("a login landing in the dead-registration cleanup window must rebuild, not skip on the stale cancel")
	}
	app.stopInboundPeer(context.Background())
}

// permanentlyExitingInboundPeer 模拟 HubLink 重试时钟崩溃后的永久退出：Run 直接
// 返回错误，不等待 ctx 取消，也不自行恢复。
type permanentlyExitingInboundPeer struct {
	exited chan struct{}
}

func (p *permanentlyExitingInboundPeer) Run(ctx context.Context) error {
	close(p.exited)
	return errors.New("relay retry clock failed; giving up")
}

func requireEventuallyNil(t *testing.T, condition func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(time.Millisecond)
	}
}

// 换服务器/换账号重新登录时，入站登记必须跟着换。
//
// 登记是在建立时绑定服务端地址与设备凭据的，登录事件只是「有一次登录发生了」——
// 不重建就等于继续用上一次登录的身份挂在上一个服务端上：新账号那边看不到这台
// 桌面端（控制台恒显示离线），而旧账号那边它还在线。桌面端的登录发生在同一个
// 进程内，所以这条路径完全靠事件驱动，没有重启进程这个兜底。
func TestAppInboundPeer_GivenLiveRegistration_WhenLoggingIntoAnotherServer_ThenRebuilds(t *testing.T) {
	first := &inboundPeerStub{started: make(chan struct{}), stopped: make(chan struct{})}
	second := &inboundPeerStub{started: make(chan struct{}), stopped: make(chan struct{})}
	calls := 0
	previous := newInboundPeer
	newInboundPeer = func(context.Context) (inboundPeer, error) {
		calls++
		if calls == 1 {
			return first, nil
		}
		return second, nil
	}
	t.Cleanup(func() { newInboundPeer = previous })

	app := &App{}
	app.onServerStateEvent(map[string]any{"kind": "logged_in"})
	select {
	case <-first.started:
	case <-time.After(time.Second):
		t.Fatal("第一次登录没有建立入站登记")
	}

	// 不登出，直接登录到另一个服务端。
	app.onServerStateEvent(map[string]any{"kind": "logged_in"})
	select {
	case <-first.stopped:
	case <-time.After(time.Second):
		t.Fatal("重新登录必须先停掉指向旧服务端的登记")
	}
	select {
	case <-second.started:
	case <-time.After(time.Second):
		t.Fatal("重新登录必须按新的登录状态重建登记")
	}
	app.stopInboundPeer(context.Background())
}

// 退出登录后，那条中转登记必须停：它挂在 context.Background() 上，不主动停就要
// 一直活到应用退出——已经登出的桌面端仍以旧凭据在旧服务端上保持可寻址。
func TestAppInboundPeer_GivenRegistration_WhenLoggedOut_ThenStops(t *testing.T) {
	stub := &inboundPeerStub{started: make(chan struct{}), stopped: make(chan struct{})}
	previous := newInboundPeer
	newInboundPeer = func(context.Context) (inboundPeer, error) { return stub, nil }
	t.Cleanup(func() { newInboundPeer = previous })

	app := &App{}
	app.onServerStateEvent(map[string]any{"kind": "logged_in"})
	select {
	case <-stub.started:
	case <-time.After(time.Second):
		t.Fatal("登录没有建立入站登记")
	}

	app.onServerStateEvent(map[string]any{"kind": "logged_out"})
	select {
	case <-stub.stopped:
	case <-time.After(time.Second):
		t.Fatal("登出必须停掉入站登记")
	}
}

// 账号级实时通道与中继登记同理：它在建连那一刻就钉死了服务端地址与设备凭据，
// 登录状态一变就必须踢掉重连，否则那条常连一直挂在上一套 server 上，新 server 的
// 通道永远拨不起来（sync_svc.DropAccountChannel）。
func TestAppOnServerStateEvent_GivenLoginChanges_ThenDropsTheAccountChannel(t *testing.T) {
	previousPeer := newInboundPeer
	newInboundPeer = func(context.Context) (inboundPeer, error) {
		return &inboundPeerStub{started: make(chan struct{}), stopped: make(chan struct{})}, nil
	}
	t.Cleanup(func() { newInboundPeer = previousPeer })

	drops := 0
	previousDrop := dropAccountChannel
	dropAccountChannel = func() { drops++ }
	t.Cleanup(func() { dropAccountChannel = previousDrop })

	app := &App{}
	app.onServerStateEvent(map[string]any{"kind": "logged_in"})
	if drops != 1 {
		t.Fatalf("登录换了身份，旧通道必须断开重连；drops = %d", drops)
	}

	app.onServerStateEvent(map[string]any{"kind": "logged_out"})
	if drops != 2 {
		t.Fatalf("登出后不该还有一条挂在旧 server 上的常连；drops = %d", drops)
	}

	app.onServerStateEvent(map[string]any{"kind": "server_offline"})
	if drops != 2 {
		t.Fatalf("服务端够不着不是身份变化，通道自己重连即可；drops = %d", drops)
	}
}

// 登出还要把收编来的设备行去掉：它们的依据就是「账号说有这台机器」，账号断了依据
// 就没了（remote_device_svc.DiscardAdoptedDevices）。登录不清——那时账号还在，
// 紧接着的一次设备清单刷新会按新账号重新收编。
func TestAppOnServerStateEvent_GivenLoggedOut_ThenDiscardsAdoptedDevices(t *testing.T) {
	previousPeer := newInboundPeer
	newInboundPeer = func(context.Context) (inboundPeer, error) {
		return &inboundPeerStub{started: make(chan struct{}), stopped: make(chan struct{})}, nil
	}
	t.Cleanup(func() { newInboundPeer = previousPeer })
	previousDrop := dropAccountChannel
	dropAccountChannel = func() {}
	t.Cleanup(func() { dropAccountChannel = previousDrop })

	discards := 0
	previousDiscard := discardAdoptedDevices
	discardAdoptedDevices = func(context.Context) { discards++ }
	t.Cleanup(func() { discardAdoptedDevices = previousDiscard })

	app := &App{}
	app.onServerStateEvent(map[string]any{"kind": "logged_in"})
	if discards != 0 {
		t.Fatalf("登录时账号还在，收编行不该被清；discards = %d", discards)
	}

	app.onServerStateEvent(map[string]any{"kind": "logged_out"})
	if discards != 1 {
		t.Fatalf("登出必须清掉收编行；discards = %d", discards)
	}
}

// stubClearAccountDirectSvc 覆盖 DiscardAdoptedDevices 与 ClearAccountDirect 两个
// 方法：discardAdoptedDevices 的真实实现在登出时两个都调，嵌入的
// remote_device_svc.RemoteDeviceSvc 零值不覆盖会在调用时 nil 解引用 panic。
type stubClearAccountDirectSvc struct {
	remote_device_svc.RemoteDeviceSvc
	discardCalls int
	clearCalls   int
	// calls 记下两个方法被调的先后。
	calls []string
}

func (s *stubClearAccountDirectSvc) DiscardAdoptedDevices(context.Context) (int, error) {
	s.discardCalls++
	s.calls = append(s.calls, "discard")
	return 0, nil
}

func (s *stubClearAccountDirectSvc) ClearAccountDirect(context.Context) (int, error) {
	s.clearCalls++
	s.calls = append(s.calls, "clear")
	return 0, nil
}

// D10「这些行按今天收编行的方式处理」：今天登出时收编行被整行去掉
// （DiscardAdoptedDevices 只认 IsRelayOnly 的行）。账号直连行必须先被清回收编行的
// 形状、再过 DiscardAdoptedDevices，才会像收编行一样随登出消失；顺序反过来，清完的
// 行就以「只有中转」的样子留在一台已经登出的桌面端上。
func TestAppOnServerStateEvent_GivenLoggedOut_ThenAccountDirectRowsAreDiscardedLikeAdoptedRows(t *testing.T) {
	previousPeer := newInboundPeer
	newInboundPeer = func(context.Context) (inboundPeer, error) {
		return &inboundPeerStub{started: make(chan struct{}), stopped: make(chan struct{})}, nil
	}
	t.Cleanup(func() { newInboundPeer = previousPeer })
	previousDrop := dropAccountChannel
	dropAccountChannel = func() {}
	t.Cleanup(func() { dropAccountChannel = previousDrop })

	original := remote_device_svc.Default()
	t.Cleanup(func() { remote_device_svc.SetDefault(original) })
	stub := &stubClearAccountDirectSvc{}
	remote_device_svc.SetDefault(stub)

	app := &App{}
	app.onServerStateEvent(map[string]any{"kind": "logged_out"})
	if len(stub.calls) != 2 || stub.calls[0] != "clear" || stub.calls[1] != "discard" {
		t.Fatalf("登出必须先把账号直连行清回收编行、再去掉收编行；calls = %v", stub.calls)
	}
}

// D10：桌面端登出不止要清收编行（上面那条用例），还要把「来自账号的直连」行退回
// 收编行的形状——地址、证书、keychain 里的凭据清掉
// （remote_device_svc.ClearAccountDirect），再与收编行一起去掉（见下一条用例）。
func TestAppOnServerStateEvent_GivenLoggedOut_ThenClearsAccountDirectRows(t *testing.T) {
	previousPeer := newInboundPeer
	newInboundPeer = func(context.Context) (inboundPeer, error) {
		return &inboundPeerStub{started: make(chan struct{}), stopped: make(chan struct{})}, nil
	}
	t.Cleanup(func() { newInboundPeer = previousPeer })
	previousDrop := dropAccountChannel
	dropAccountChannel = func() {}
	t.Cleanup(func() { dropAccountChannel = previousDrop })

	original := remote_device_svc.Default()
	t.Cleanup(func() { remote_device_svc.SetDefault(original) })
	stub := &stubClearAccountDirectSvc{}
	remote_device_svc.SetDefault(stub)

	app := &App{}
	app.onServerStateEvent(map[string]any{"kind": "logged_in"})
	if stub.clearCalls != 0 {
		t.Fatalf("登录时账号还在，不该清账号直连行；clearCalls = %d", stub.clearCalls)
	}

	app.onServerStateEvent(map[string]any{"kind": "logged_out"})
	if stub.clearCalls != 1 {
		t.Fatalf("登出必须清掉账号直连行；clearCalls = %d", stub.clearCalls)
	}
	if stub.discardCalls != 1 {
		t.Fatalf("登出必须仍然清掉收编行；discardCalls = %d", stub.discardCalls)
	}
}

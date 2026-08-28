package chat_svc

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/protorpctest"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// recordingEmitter 收下 emit 出去的会话级事件。
type recordingEmitter struct {
	mu  sync.Mutex
	got []emittedEvent
}

type emittedEvent struct {
	Name    string
	Payload ChatStreamEvent
}

func (e *recordingEmitter) Emit(_ context.Context, name string, payload any) {
	ev, ok := payload.(ChatStreamEvent)
	if !ok {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.got = append(e.got, emittedEvent{Name: name, Payload: ev})
}

func (e *recordingEmitter) events() []emittedEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]emittedEvent(nil), e.got...)
}

// Given 某 device 的池化连接断了,When *remote.Runtime 经重连端口要一条新连接,
// Then chat_svc 从池里再借一条、换进同一个 cache entry 并归还旧的。
//
// entry 必须**留在** cache 里:摘掉它,下一轮 borrow 会为同一台设备造出第二个
// *remote.Runtime,而两个 runtime 会在同一条池化连接上抢注同名 handler —— 在飞
// 会话的事件从此被路由到不认识它的那个,静默丢弃。
func TestReconnectRemote_SwapsLeaseAndKeepsCacheEntry(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	lease1 := mock_remote_device_svc.NewMockLease(ctrl)
	lease2 := mock_remote_device_svc.NewMockLease(ctrl)
	client1, client2 := &noopDaemonClient{}, &noopDaemonClient{}
	for _, p := range []struct {
		lease  *mock_remote_device_svc.MockLease
		client *noopDaemonClient
	}{{lease1, client1}, {lease2, client2}} {
		p.lease.EXPECT().Client().Return(p.client).AnyTimes()
		p.lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	}
	gomock.InOrder(
		pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease1, nil),
		pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease2, nil),
	)
	lease1.EXPECT().Release().Times(1)
	lease2.EXPECT().Release().AnyTimes()

	svc := &chatSvc{emitter: NoopEmitter{}}
	svc.setConnPoolForTest(pool)
	installPairedDevice(t, ctrl, 7)
	installExecDaemonRecorder(t, ctrl)

	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: testDeviceFingerprint(7)}
	_, err := svc.borrowRemoteRuntime(context.Background(), be, 100)
	require.NoError(t, err)

	entry, _ := svc.remotePool().SnapshotForTest(7)
	require.NotNil(t, entry)

	got, _, err := svc.reconnectRemote(context.Background(), 7, entry)
	require.NoError(t, err)
	assert.Same(t, client2, got, "重连必须交出新 lease 的连接")

	still, swapped := svc.remotePool().SnapshotForTest(7)
	assert.Same(t, entry, still, "重连后 cache entry 必须还在")
	assert.Same(t, lease2, swapped, "entry 必须持有新 lease")
}

// Given 上一轮已经把引用全还了、而那条池化连接**还活着**,When 同一台设备再借一次,
// Then 交出的必须还是**同一个** *remote.Runtime。
//
// 它是这条连接上五类通知 handler 的属主,也是自主续轮消费方(startAutonomousWatcher,
// 每会话只订阅一次)订阅的那个实例。为同一条连接另造一个 runtime,新实例会把 handler
// 抢注过去,而消费方还挂在旧实例上 —— 别的端在这台机器上发起的一轮于是被投进一个没有
// 消费方的补齐轮:什么都不落库(R18),同时新实例的会话表是空的,对那条会话提交工具
// 决议当场 ErrNoActiveTurn(R10)。
func TestBorrowRemoteRuntime_ReusesRuntimeWhilePooledConnLives(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	// 两条 lease 出自**同一个池 entry**:同一条连接、同一个 Closed() 信号。
	client := &noopDaemonClient{}
	alive := make(chan struct{})
	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	lease1 := mock_remote_device_svc.NewMockLease(ctrl)
	lease2 := mock_remote_device_svc.NewMockLease(ctrl)
	for _, l := range []*mock_remote_device_svc.MockLease{lease1, lease2} {
		l.EXPECT().Client().Return(protorpctest.WrapConnection(client)).AnyTimes()
		l.EXPECT().Closed().Return(alive).AnyTimes()
		l.EXPECT().Release().AnyTimes()
	}
	gomock.InOrder(
		pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease1, nil),
		pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease2, nil),
	)

	svc := &chatSvc{emitter: NoopEmitter{}}
	svc.setConnPoolForTest(pool)
	installPairedDevice(t, ctrl, 7)
	installExecDaemonRecorder(t, ctrl)
	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: testDeviceFingerprint(7)}

	first, release, err := svc.borrowRemoteRuntimeForTurn(context.Background(), be, 100)
	require.NoError(t, err)
	release() // 这一轮跑完:引用归零,lease 还给池(空闲回收照常计时)
	assert.Zero(t, svc.remoteRuntimeCount(7), "引用必须真的归零 —— 否则这条断言什么都没测")

	second, _, err := svc.borrowRemoteRuntimeForTurn(context.Background(), be, 100)
	require.NoError(t, err)
	assert.Same(t, first, second,
		"连接还活着时,同一台设备只能有一个 *remote.Runtime —— 第二个会抢走它的通知 handler")

	entry, held := svc.remotePool().SnapshotForTest(7)
	require.NotNil(t, entry)
	assert.Same(t, lease2, held, "重新借用必须把新 lease 装进同一个 entry")
}

// Given 那条池化连接在两轮之间被回收了(空闲超时 / daemon 掉线),When 同一台设备再
// 借一次,Then 必须**另造**一个 *remote.Runtime —— 旧那个的连接已经死了,沿用它等于
// 把这一轮发给一条不存在的 socket。
func TestBorrowRemoteRuntime_RebuildsRuntimeAfterPooledConnEvicted(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	evicted := make(chan struct{})
	close(evicted) // 第一条 lease 背后的池 entry 已失效
	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	lease1 := mock_remote_device_svc.NewMockLease(ctrl)
	lease2 := mock_remote_device_svc.NewMockLease(ctrl)
	lease1.EXPECT().Client().Return(&noopDaemonClient{}).AnyTimes()
	lease1.EXPECT().Closed().Return(evicted).AnyTimes()
	lease1.EXPECT().Release().AnyTimes()
	lease2.EXPECT().Client().Return(&noopDaemonClient{}).AnyTimes()
	lease2.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	lease2.EXPECT().Release().AnyTimes()
	gomock.InOrder(
		pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease1, nil),
		pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease2, nil),
	)

	svc := &chatSvc{emitter: NoopEmitter{}}
	svc.setConnPoolForTest(pool)
	installPairedDevice(t, ctrl, 7)
	installExecDaemonRecorder(t, ctrl)
	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: testDeviceFingerprint(7)}

	first, release, err := svc.borrowRemoteRuntimeForTurn(context.Background(), be, 100)
	require.NoError(t, err)
	release()

	second, _, err := svc.borrowRemoteRuntimeForTurn(context.Background(), be, 100)
	require.NoError(t, err)
	assert.NotSame(t, first, second, "连接已被回收,不能再沿用挂在它上面的 runtime")
}

// Given 上一条池化连接已经被摘出池子的表、但它的 Closed() 还没来得及关闭(池的
// tryEvictIdle 与 watchClient 都是**先** delete 再 close),When 落在这两步之间的
// 那次 Borrow 因此拨到了一条**新连接**,Then 必须另造一个 *remote.Runtime。
//
// 沿用旧那个等于把这一轮发给旧连接的 client —— 池下一刻就把那条 socket 关了,这一轮
// 的每个 RPC 都打在已关的连接上。所以判据不能是「上一条 lease 还没关闭」(那只是
// 「还没走到 close 那一行」),必须是「这次借到的就是同一条池化连接」。
func TestBorrowRemoteRuntime_RebuildsWhenBorrowLandsOnAnotherPooledConn(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	// 两条 lease 出自**两个不同的池 entry**,而旧那条的信号此刻都还没关闭 ——
	// 这正是 delete 与 close 之间那个窗口的样子。
	oldConn := make(chan struct{})
	newConn := make(chan struct{})
	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	lease1 := mock_remote_device_svc.NewMockLease(ctrl)
	lease2 := mock_remote_device_svc.NewMockLease(ctrl)
	lease1.EXPECT().Client().Return(&noopDaemonClient{}).AnyTimes()
	lease1.EXPECT().Closed().Return(oldConn).AnyTimes()
	lease1.EXPECT().Release().AnyTimes()
	lease2.EXPECT().Client().Return(&noopDaemonClient{}).AnyTimes()
	lease2.EXPECT().Closed().Return(newConn).AnyTimes()
	lease2.EXPECT().Release().AnyTimes()
	gomock.InOrder(
		pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease1, nil),
		pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease2, nil),
	)

	svc := &chatSvc{emitter: NoopEmitter{}}
	svc.setConnPoolForTest(pool)
	installPairedDevice(t, ctrl, 7)
	installExecDaemonRecorder(t, ctrl)
	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: testDeviceFingerprint(7)}

	first, release, err := svc.borrowRemoteRuntimeForTurn(context.Background(), be, 100)
	require.NoError(t, err)
	release()

	second, _, err := svc.borrowRemoteRuntimeForTurn(context.Background(), be, 100)
	require.NoError(t, err)
	assert.NotSame(t, first, second,
		"借到的是另一条池化连接,挂在旧连接上的 runtime 不能再用")
}

// Given 引用归零的归还与同一台设备上的下一次借用同时发生,When 归还方去还 lease,
// Then 它还的必须是**自己解锁前手上那条**。
//
// entry 现在在引用归零后留在 cache 里(这正是本轮改的形态),于是并发的那次借用会走
// 冷路径(fast path 因 leased==false 落空),Borrow 一条新 lease,并在 adoptLease 里
// 把 entry.lease 换掉 —— 而归还方此刻正好在 Unlock 之后、Release 之前读它。读换过之后
// 的那条会同时错两件事:entry.lease 上的数据竞争(go test -race 直接报),以及**还错
// 人** —— 别人这一轮正用着的那条被提前还掉,自己那条的池引用则永远掉不下去,那条连接
// 从此不再空闲回收。
//
// **判它的是 `go test -race`**:竞态本身是确定报得出来的(两次访问之间没有任何同步
// 边),而「还错人」是那次无同步读的代价 —— 它要不要当场兑现取决于调度,不能指望不带
// -race 的一遍跑到。下面那两条不变量断言是第二道网:一旦哪天变成必然发生的错还,
// 不带 -race 也拦得住。
//
// 替身是手写的而不是 mockgen 的:gomock 的 controller 在**每一次**打桩调用上都过同一
// 把锁,归还方的 Release 与借用方的 Borrow 因此被串成一条 happens-before 边,-race
// 看到的是「有序的」两次访问 —— 真竞态会被这把锁整个盖住。下面这对替身在归还路径与
// 借用路径之间不共享任何同步对象,竞态才暴露得出来。
// installLockFreePairedDevice 是 installPairedDevice + installExecDaemonRecorder 的
// 手写版，给上面那两个竞态用例用：gomock 的 controller 在每一次打桩调用上过同一把
// 锁，会在并发借用方之间连出一条 happens-before 边，把这两个用例要暴露的竞态整个
// 盖住。这对替身不共享任何同步对象。
func installLockFreePairedDevice(t *testing.T, deviceID int64) {
	t.Helper()
	prevSvc := remote_device_svc.Default()
	remote_device_svc.SetDefault(staticPairedDevices{rows: []*remote_device_svc.DeviceView{
		{ID: deviceID, DaemonFingerprint: testDeviceFingerprint(deviceID), Online: true},
	}})
	prevRepo := chat_repo.Session()
	chat_repo.RegisterSession(noopExecDaemonRecorder{})
	t.Cleanup(func() {
		remote_device_svc.SetDefault(prevSvc)
		chat_repo.RegisterSession(prevRepo)
	})
}

// staticPairedDevices 只答「本机配对表里有哪些 daemon」；其余方法内嵌接口，被调到就
// panic —— 用例不该走到那里。
type staticPairedDevices struct {
	remote_device_svc.RemoteDeviceSvc
	rows []*remote_device_svc.DeviceView
}

func (s staticPairedDevices) List(context.Context) ([]*remote_device_svc.DeviceView, error) {
	return s.rows, nil
}

func (s staticPairedDevices) Get(_ context.Context, deviceID int64) (*remote_device_svc.DeviceView, error) {
	for _, row := range s.rows {
		if row.ID == deviceID {
			return row, nil
		}
	}
	return nil, nil
}

// noopExecDaemonRecorder 吞掉借出成功后那次会话行写入（R15b），其余方法同上。
type noopExecDaemonRecorder struct {
	chat_repo.SessionRepo
}

func (noopExecDaemonRecorder) UpdateExecDaemon(context.Context, int64, int64, string, int64) error {
	return nil
}

func TestReleaseRemoteRuntimeGeneration_ReleasesTheLeaseItHeld(t *testing.T) {
	const rounds = 200
	pool := &racePool{conn: make(chan struct{})} // 同一条池化连接:每次 Borrow 都落在它上面

	svc := &chatSvc{emitter: NoopEmitter{}}
	svc.setConnPoolForTest(pool)
	installLockFreePairedDevice(t, 7)
	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: testDeviceFingerprint(7)}

	for range rounds {
		_, release, err := svc.borrowRemoteRuntimeForTurn(context.Background(), be, 100)
		require.NoError(t, err)

		// 归还与另一条会话的借用同时发生。
		next := make(chan func(), 1)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			release()
		}()
		go func() {
			defer wg.Done()
			_, rel, berr := svc.borrowRemoteRuntimeForTurn(context.Background(), be, 101)
			if berr != nil {
				next <- func() {}
				return
			}
			next <- rel
		}()
		wg.Wait()
		(<-next)()
	}

	var orphaned, doubled int
	for _, l := range pool.handedOut() {
		switch n := l.releases.Load(); {
		case n == 0:
			orphaned++
		case n > 1:
			doubled++
		}
	}
	assert.Zero(t, orphaned,
		"%d 条 lease 从来没被归还 —— 它们的池引用永远掉不下去,那条连接再也不会空闲回收",
		orphaned)
	assert.Zero(t, doubled,
		"%d 条 lease 被归还了不止一次 —— 归还方还的是别人装进去的那条",
		doubled)
}

// Given 同一台设备上两条冷路径同时开工(cache 里还没有它的条目),When 两边都查过
// 「没有可沿用的条目」之后才各自去装自己刚建的那个,Then 交出的必须是**同一个**
// *remote.Runtime。
//
// 「查」与「装」分成两次上锁的话,后装的那个把先装的整个覆盖掉,代价有两条:
//
//   - 被覆盖的那个 entry 的 lease 从此没人还 —— 它那一轮的 release 按 deviceID 查
//     cache,查到的是覆盖它的那个,generation 比不上就直接返回。那条池化连接的池引用
//     永远掉不下去,再也不会空闲回收;
//   - 两个 runtime 同时挂在**同一条**连接上抢注同名 handler,先来的那一轮的事件会被
//     路由到不认识它的那个实例然后静默丢弃(R18),对那条会话提交工具决议当场
//     ErrNoActiveTurn(R10)—— 正是本轮要修掉的那个形态。
//
// 怎么把那个窗口从「偶尔」压成「每轮都穿」——两件事叠起来,都是真实调度做得到的:
//
//  1. lease.Client() 正好被调在「查」之后、「装」之前,让每一条冷路径在那里合流一次,
//     于是它们都已经查过 cache、都还没装进去(合流点带超时:某一轮少一条冷路径也不
//     吊死);
//  2. 另起一条 goroutine 反复占住 remoteMu 一小会儿,把它压进**饥饿模式** —— Go 的
//     sync.Mutex 在某个等待方等满 1ms 后改成 FIFO 直接交棒,抢锁的一方不再插队。
//     「查」完放开的那一手因此必然被排在队里的另一条冷路径接走。生产上这只是「锁很忙 /
//     持锁方被调度出去」的日常样子,不是什么特殊构造。
func TestBorrowRemoteRuntime_ConcurrentColdPathsShareOneRuntime(t *testing.T) {
	const rounds, callers = 10, 8
	for range rounds {
		// 每轮一个全新的 svc:冷路径要的正是「cache 里还没有这台设备」。
		pool := &coldPathPool{conn: make(chan struct{}), gate: newMeetingPoint(callers)}
		svc := &chatSvc{emitter: NoopEmitter{}}
		svc.setConnPoolForTest(pool)
		installLockFreePairedDevice(t, 7)
		be := &agent_backend_entity.AgentBackend{DeviceFingerprint: testDeviceFingerprint(7)}

		stop := make(chan struct{})
		var hog sync.WaitGroup
		hog.Add(1)
		go func() {
			defer hog.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				svc.remotePool().HoldLockForTest(2 * time.Millisecond)
			}
		}()

		got := make([]*remote.Runtime, callers)
		rel := make([]func(), callers)
		errs := make([]error, callers)
		var wg sync.WaitGroup
		wg.Add(callers)
		for i := range got {
			go func() {
				defer wg.Done()
				got[i], rel[i], errs[i] = svc.borrowRemoteRuntimeForTurn(
					context.Background(), be, int64(100+i))
			}()
		}
		wg.Wait()
		close(stop)
		hog.Wait()

		for i := range got {
			require.NoError(t, errs[i])
			require.Same(t, got[0], got[i],
				"同一条池化连接上只能有一个 *remote.Runtime —— 第二个会抢走它的通知 handler")
		}

		// 被覆盖的那个 entry 的 lease 是还不掉的(那一轮的 release 按 deviceID 查
		// cache,查到的是覆盖它的那个,generation 比不上就直接返回)。这里把每一轮都
		// 正常收尾,再钉一次那条代价。
		for _, r := range rel {
			r()
		}
		for _, l := range pool.handedOut() {
			assert.NotZero(t, l.releases.Load(),
				"有 lease 从来没被归还 —— 它的池引用永远掉不下去,那条连接再也不会空闲回收")
		}
	}
}

// meetingPoint 让 want 条 goroutine 在同一处合流;到场的不足 want 条时按超时放行,
// 不把测试吊死。
type meetingPoint struct {
	mu   sync.Mutex
	n    int
	want int
	open chan struct{}
}

func newMeetingPoint(want int) *meetingPoint {
	return &meetingPoint{want: want, open: make(chan struct{})}
}

func (m *meetingPoint) arrive() {
	m.mu.Lock()
	m.n++
	if m.n == m.want {
		close(m.open)
	}
	m.mu.Unlock()
	select {
	case <-m.open:
	case <-time.After(100 * time.Millisecond):
	}
}

// coldPathLease 在 Client() 上合流一次 —— 冷路径正是在那之后才去装自己建的 entry。
type coldPathLease struct {
	conn     chan struct{}
	gate     *meetingPoint
	releases atomic.Int32
}

func (l *coldPathLease) Client() client.ProtobufConnection {
	l.gate.arrive()
	return &noopDaemonClient{}
}
func (l *coldPathLease) Closed() <-chan struct{} { return l.conn }
func (l *coldPathLease) Release()                { l.releases.Add(1) }
func (l *coldPathLease) LLMUpsert(context.Context, *agentrewire.LLMUpsertRequest) (*agentrewire.LLMUpsertResponse, error) {
	return &agentrewire.LLMUpsertResponse{}, nil
}

// coldPathPool 每次 Borrow 都交出同一条池化连接上的一条新 lease。
type coldPathPool struct {
	conn chan struct{}
	gate *meetingPoint
	mu   sync.Mutex
	out  []*coldPathLease
}

func (p *coldPathPool) Borrow(context.Context, int64) (remote_device_svc.Lease, error) {
	l := &coldPathLease{conn: p.conn, gate: p.gate}
	p.mu.Lock()
	p.out = append(p.out, l)
	p.mu.Unlock()
	return l, nil
}

func (p *coldPathPool) Close() error { return nil }

func (p *coldPathPool) handedOut() []*coldPathLease {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*coldPathLease(nil), p.out...)
}

// raceLease 记账只落在自己身上(原子量各自独立),归还方与借用方之间因此没有同步边。
type raceLease struct {
	conn     chan struct{}
	releases atomic.Int32
}

func (l *raceLease) Client() client.ProtobufConnection { return &noopDaemonClient{} }
func (l *raceLease) Closed() <-chan struct{}           { return l.conn }
func (l *raceLease) Release()                          { l.releases.Add(1) }
func (l *raceLease) LLMUpsert(context.Context, *agentrewire.LLMUpsertRequest) (*agentrewire.LLMUpsertResponse, error) {
	return &agentrewire.LLMUpsertResponse{}, nil
}

// racePool 每次 Borrow 都交出同一条池化连接上的一条新 lease。mu 只在借用侧被拿,
// 归还侧一次都不碰它 —— 否则又会把要观察的那条竞态盖住。
type racePool struct {
	conn chan struct{}
	mu   sync.Mutex
	out  []*raceLease
}

func (p *racePool) Borrow(context.Context, int64) (remote_device_svc.Lease, error) {
	l := &raceLease{conn: p.conn}
	p.mu.Lock()
	p.out = append(p.out, l)
	p.mu.Unlock()
	return l, nil
}

func (p *racePool) Close() error { return nil }

func (p *racePool) handedOut() []*raceLease {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*raceLease(nil), p.out...)
}

// Given 某设备的 *remote.Runtime 已在 cache 里(第一条会话建的),When 第二条会话
// 借用同一台设备,Then 它的执行 daemon 与实例标识**同样**要落库。
//
// 游标的读写守卫是 (会话, 实例标识) 这一对:第二条会话不写,它的 LoadCursor 就永远
// 判失效 —— 断连时被当成「游标失效」直接注入 ErrDaemonDisconnected(R4/R12 双失),
// 而且下一轮换了 *remote.Runtime 后游标从 0 起,第一条实时帧判跳号 → 从 0 整段补齐
// → 把这条会话的全部历史通知重放进当前这一轮,违反硬不变量的「无重复」。
func TestBorrowRemoteRuntime_CacheHit_StillRecordsExecDaemon(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	lease := mock_remote_device_svc.NewMockLease(ctrl)
	lease.EXPECT().Client().Return(&noopDaemonClient{}).AnyTimes()
	lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease, nil).Times(1)

	rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	rds.EXPECT().List(gomock.Any()).Return([]*remote_device_svc.DeviceView{
		{ID: 7, DaemonFingerprint: "sha256:beef"},
	}, nil).AnyTimes()
	rds.EXPECT().Get(gomock.Any(), int64(7)).
		Return(&remote_device_svc.DeviceView{ID: 7, DaemonFingerprint: "sha256:beef"}, nil).AnyTimes()
	prevSvc := remote_device_svc.Default()
	remote_device_svc.SetDefault(rds)
	t.Cleanup(func() { remote_device_svc.SetDefault(prevSvc) })

	sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
	prevRepo := chat_repo.Session()
	chat_repo.RegisterSession(sessRepo)
	t.Cleanup(func() { chat_repo.RegisterSession(prevRepo) })
	sessRepo.EXPECT().UpdateExecDaemon(gomock.Any(), int64(100), int64(7), "sha256:beef", int64(0)).Return(nil)
	sessRepo.EXPECT().UpdateExecDaemon(gomock.Any(), int64(101), int64(7), "sha256:beef", int64(0)).Return(nil)

	svc := &chatSvc{emitter: NoopEmitter{}}
	svc.setConnPoolForTest(pool)

	be := &agent_backend_entity.AgentBackend{DeviceFingerprint: "sha256:beef"}
	first, err := svc.borrowRemoteRuntime(context.Background(), be, 100)
	require.NoError(t, err)
	second, err := svc.borrowRemoteRuntime(context.Background(), be, 101)
	require.NoError(t, err)
	assert.Same(t, first, second, "同一台设备共享同一个 runtime —— 这正是走 cache 命中的那条路")
}

// Given 一条远端会话的连接态发生变化,When runtime 播报,Then chat_svc 在会话级
// 流上 emit 一条 connection_state —— 连接态是运行态之上的修饰,不进 AgentStatus。
func TestRemoteConnState_EmitsSessionLevelStreamEvent(t *testing.T) {
	rec := &recordingEmitter{}
	// 走 NewChat 而不是结构体字面量:播报路径要读 activeCancels(只记有在飞 turn 的
	// 会话,见 rememberConnState),那份 sync.Map 由 NewChat 装配。
	svc := NewChat(rec).(*chatSvc)
	markTurnActive(t, svc, 100)

	svc.onRemoteConnState(100, remote.SessionConnState{State: remote.ConnStateReconnecting})
	svc.onRemoteConnState(100, remote.SessionConnState{
		State: remote.ConnStateConnected, Replayed: 4, PendingDecisions: 1,
	})

	got := rec.events()
	require.Len(t, got, 2)
	assert.Equal(t, ConnStateStreamName(100), got[0].Name)
	assert.Equal(t, StreamConnectionState, got[0].Payload.Kind)
	assert.Equal(t, string(remote.ConnStateReconnecting), got[0].Payload.ConnectionState)

	assert.Equal(t, string(remote.ConnStateConnected), got[1].Payload.ConnectionState)
	assert.Equal(t, 4, got[1].Payload.CaughtUpCount)
	assert.Equal(t, 1, got[1].Payload.PendingDecisions)
}

// registerLoadSessionRepos 给 LoadSession 装上够用的仓储 mock:会话 + 消息 + agent。
// agent 返回 nil —— LoadSession 的 backend / provider / 设备那一大段都挂在 a != nil 下,
// 本用例只关心连接态那一个字段,不需要把整条 detail 装配路径也搭出来。
func registerLoadSessionRepos(t *testing.T, ctrl *gomock.Controller, sessionID int64, sess *chat_entity.Session) {
	t.Helper()
	sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
	msgRepo := mock_chat_repo.NewMockMessageRepo(ctrl)
	agtRepo := mock_agent_repo.NewMockAgentRepo(ctrl)
	sessRepo.EXPECT().Find(gomock.Any(), sessionID).Return(sess, nil).AnyTimes()
	msgRepo.EXPECT().ListMeta(gomock.Any(), sessionID).Return([]*chat_entity.Message{
		{ID: 11, SessionID: sessionID, Role: "assistant", BlocksJSON: "[]", Seq: 1},
	}, nil).AnyTimes()
	msgRepo.EXPECT().FillBlocks(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	agtRepo.EXPECT().Find(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

	prevSess, prevMsg, prevAgent := chat_repo.Session(), chat_repo.Message(), agent_repo.Agent()
	chat_repo.RegisterSession(sessRepo)
	chat_repo.RegisterMessage(msgRepo)
	agent_repo.RegisterAgent(agtRepo)
	t.Cleanup(func() {
		chat_repo.RegisterSession(prevSess)
		chat_repo.RegisterMessage(prevMsg)
		agent_repo.RegisterAgent(prevAgent)
	})
}

// markTurnActive 把该会话标成「有在飞 turn」。LoadSession 用同一份 activeCancels 判定
// ActiveStream,而重挂上来的前端只在拿得到 ActiveStream 时才播种连接态 —— 「重连中的
// 会话」这个场景在真实系统里必然带着一个在飞 turn。
func markTurnActive(t *testing.T, svc *chatSvc, sessionID int64) {
	t.Helper()
	_, cancel := context.WithCancel(context.Background())
	svc.activeCancels.Store(sessionID, cancel)
	t.Cleanup(func() {
		svc.activeCancels.Delete(sessionID)
		cancel()
	})
}

// Given 一条远端会话正处在重连态,When 用户整页重载 webview 后重新 LoadSession,
// Then 响应必须同步带回「此刻正在重连」。
//
// 为什么必须随响应同步返回,而不是重挂时补发一次事件:前端拿到 ActiveStream **之后**
// 才订阅 chat:conn:<sid>,补发会落在订阅建立之前而丢失 —— 那是竞态,不是实现细节。
// 为什么这条会话在重载后还在跑:R4 之后断连不再终结会话,turn goroutine 活着,
// LoadSession 照旧给出 ActiveStream,前端带着 streaming=true 重挂上来;连接态若不
// 一并返回,整个退避窗口里用户看到的都是普通打字指示器(违背 R13)。
func TestLoadSession_ServesCurrentConnStateForReattach(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	registerLoadSessionRepos(t, ctrl, 100, &chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "running", Status: consts.ACTIVE,
	})

	svc := NewChat(NoopEmitter{}).(*chatSvc)
	// 这条会话有在飞 turn:断连不再终结会话,turn goroutine 活着,LoadSession 因此
	// 给得出 ActiveStream,前端才有那条要被修饰的活信号。
	markTurnActive(t, svc, 100)
	ctx := context.Background()
	load := func() *LoadSessionResponse {
		t.Helper()
		resp, err := svc.LoadSession(ctx, &LoadSessionRequest{SessionID: 100})
		require.NoError(t, err)
		return resp
	}

	assert.Equal(t, string(remote.ConnStateConnected), load().Session.ConnectionState,
		"没有任何连接态记录时按已连接返回(与 remote.Runtime.ConnState 的缺省一致)")

	// 通道断了:runtime 经 ConnStateObserver 播报重连态,用户此刻整页重载。
	svc.onRemoteConnState(100, remote.SessionConnState{State: remote.ConnStateReconnecting})
	assert.Equal(t, string(remote.ConnStateReconnecting), load().Session.ConnectionState,
		"重连中的会话重载后必须仍看得出断连,否则退避窗口里只剩打字指示器")

	// 补齐完成回到实时:连接态回落缺省。
	svc.onRemoteConnState(100, remote.SessionConnState{State: remote.ConnStateConnected, Replayed: 3})
	assert.Equal(t, string(remote.ConnStateConnected), load().Session.ConnectionState)

	// 会话终结(重连放弃)后清项:留着终态会泄漏到这条会话的下一轮。
	svc.onRemoteConnState(100, remote.SessionConnState{State: remote.ConnStateReconnecting})
	svc.onRemoteConnState(100, remote.SessionConnState{State: remote.ConnStateLost})
	assert.Equal(t, string(remote.ConnStateConnected), load().Session.ConnectionState)
}

// Given 一条**没有在飞 turn** 的会话(上一轮早已收尾,只剩自主续轮镜像还挂在 runtime
// 上),When 通道断开、runtime 把「还活着」的会话一并翻成重连态,Then 这一帧不得进缓存。
//
// remote.Runtime.enterReconnecting 按 liveSessionIDs()(sessions ∪ autoSessions)播
// reconnecting,而收尾只走 failAllSessions / failSession —— 两者都只覆盖 sessions。
// 只剩自主续轮镜像的会话因此收得到 reconnecting、永远收不到 connected/lost(已用
// remote 包的重连 rig 实测:重连全部失败时,活跃会话拿到 reconnecting→lost,只有镜像
// 的那条只拿到 reconnecting)。记住它就是记一条永远清不掉的旧态:等这条会话下一轮真的
// 跑起来,LoadSession 把它交给重挂上来的前端,一条连接完全正常的 turn 从头到尾顶着断连
// 指示器 —— 正是 session-conn-store.clear 要消灭的那个泄漏,只不过泄漏点挪到了 Go 侧,
// 前端再也清不掉。
func TestLoadSession_DoesNotRememberConnStateWithoutActiveTurn(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	registerLoadSessionRepos(t, ctrl, 100, &chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	})

	svc := NewChat(NoopEmitter{}).(*chatSvc)
	ctx := context.Background()
	load := func() string {
		t.Helper()
		resp, err := svc.LoadSession(ctx, &LoadSessionRequest{SessionID: 100})
		require.NoError(t, err)
		return resp.Session.ConnectionState
	}

	// 上一轮早已收尾,此刻没有在飞 turn;通道断了,这条会话被一并播成重连态。
	svc.onRemoteConnState(100, remote.SessionConnState{State: remote.ConnStateReconnecting})
	assert.Equal(t, string(remote.ConnStateConnected), load(),
		"没有在飞 turn 就没有可修饰的活信号,而这一帧永远等不到收尾帧,不该被记住")

	// 下一轮真的跑起来,连接完全正常:上一轮的旧态不得翻出来。
	markTurnActive(t, svc, 100)
	assert.Equal(t, string(remote.ConnStateConnected), load(),
		"上一轮遗留的重连态泄漏到下一轮 = 一条连接正常的 turn 全程顶着断连指示器")
}

// Given 某台配对设备上的 daemon 版本过旧(补齐族 RPC 一律 method-not-found),
// When 桌面端在它上面开一轮 —— 开轮前的能力探测因此落空,
// Then 这个结论要记到该配对设备上(R18:「且配对设备状态里说明该 daemon 版本过旧」),
// 而不是只在日志里留一行 Warn。
//
// 这条用例连着钉住整条通路:runtime 的探测 → chat_svc 注入的观察端口 → 设备状态。
// 少了 remoteRuntimeForDevice 里那行注入,探测结果就永远出不了 internal/pkg。
func TestRemoteRuntime_OldDaemon_MarksPairedDeviceOutdated(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	client := newRecordingDaemonClient()
	client.expect(wire.MethodSessionList, func(_, _ any) error {
		return &rpcerror.Error{Code: rpcerror.CodeMethodNotFound, Message: "Method not found"}
	})
	client.expect(wire.MethodRun, func(_, result any) error {
		*(result.(*wire.RunAck)) = wire.RunAck{SessionID: 100}
		return nil
	})

	pool := mock_remote_device_svc.NewMockConnPool(ctrl)
	lease := mock_remote_device_svc.NewMockLease(ctrl)
	pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(lease, nil).AnyTimes()
	lease.EXPECT().Client().Return(protorpctest.WrapConnection(client)).AnyTimes()
	lease.EXPECT().Closed().Return(make(chan struct{})).AnyTimes()
	lease.EXPECT().Release().AnyTimes()

	rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	rds.EXPECT().List(gomock.Any()).Return([]*remote_device_svc.DeviceView{
		{ID: 7, DaemonFingerprint: "sha256:beef"},
	}, nil).AnyTimes()
	rds.EXPECT().Get(gomock.Any(), int64(7)).
		Return(&remote_device_svc.DeviceView{ID: 7, DaemonFingerprint: "sha256:beef"}, nil).AnyTimes()
	prevSvc := remote_device_svc.Default()
	remote_device_svc.SetDefault(rds)
	t.Cleanup(func() { remote_device_svc.SetDefault(prevSvc) })

	svc := &chatSvc{emitter: NoopEmitter{}}
	svc.setConnPoolForTest(pool)
	installExecDaemonRecorder(t, ctrl)

	be := &agent_backend_entity.AgentBackend{
		DeviceFingerprint: "sha256:beef", Type: string(agent_backend_entity.TypeClaudeCode), ID: 1, Name: "x",
	}
	rt, err := svc.borrowRemoteRuntime(context.Background(), be, 100)
	require.NoError(t, err)

	_, _, err = rt.Run(context.Background(), agentruntime.RunRequest{
		Backend: be, SessionID: 100, UserText: "hi",
	})
	require.NoError(t, err)
}

package app

import (
	"context"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/portforward"
	"github.com/agentre-hub/agentre/internal/service/port_forward_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// 本文件钉的是**那条专属监听什么时候消失**这件事的生产路径。
//
// 规格给了四种来源(停用 / 删除 / 设备离线 / App 退出),前两种只可能经这一层的绑定
// 发生 —— 用户在界面上就这两个动作。第三种在 internal/pkg/portforward 那一侧(租约
// 失效),第四种在这里的 Shutdown。
//
// 「装配上了」不能只靠读代码:Gateway.Stop 就是这么写着的,而它在生产路径上一个调用
// 点都没有,靠进程退出释放。所以这几条都真的从绑定层驱动一遍,再去那条地址上敲门。

// stubDevices 交出一条没有 transport 的连接。本文件的用例只到「监听在不在」为止,
// 不经它发任何 RPC —— 发什么、怎么回,在 internal/pkg/portforward 的用例里。
type stubDevices struct {
	mu       sync.Mutex
	releases int
}

type stubLease struct {
	devices *stubDevices
	conn    *protorpc.Conn
	closed  chan struct{}
}

func (l *stubLease) Conn() *protorpc.Conn    { return l.conn }
func (l *stubLease) Closed() <-chan struct{} { return l.closed }
func (l *stubLease) Release() {
	l.devices.mu.Lock()
	l.devices.releases++
	l.devices.mu.Unlock()
}

func (d *stubDevices) Borrow(context.Context, int64) (portforward.DeviceConn, error) {
	return &stubLease{devices: d, conn: protorpc.NewConn(nil, protorpc.NewRegistry()), closed: make(chan struct{})}, nil
}

// withForwards 装一个 App:端口转发的声明族打桩,专属监听用一个假的连接池。
func withForwards(t *testing.T, stub *stubPortForwardSvc) (*App, *stubDevices) {
	t.Helper()
	a := withStubPortForward(t, stub)
	devices := &stubDevices{}
	a.portForwards = portforward.NewListeners(devices)
	t.Cleanup(a.portForwards.CloseAll)
	return a, devices
}

func listenerAlive(t *testing.T, address string) bool {
	t.Helper()
	parsed, err := url.Parse(address)
	require.NoError(t, err)
	conn, err := net.DialTimeout("tcp", parsed.Host, time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func requireListenerGone(t *testing.T, address string) {
	t.Helper()
	require.Eventually(t, func() bool { return !listenerAlive(t, address) },
		3*time.Second, 10*time.Millisecond, "这条专属监听本该被关掉,但它还在受理连接: "+address)
}

// Given 一条启用着的映射, When 前端要一条访问地址, Then 桌面端交回一条本机环回地址
// —— 那正是任务 8 拿去喂 BrowserOpenURL 的东西。
func TestPortForwardOpenHandsBackALoopbackAddressForTheSystemBrowser(t *testing.T) {
	a, _ := withForwards(t, &stubPortForwardSvc{})

	address, err := a.PortForwardOpen("7", "11", 5173)
	require.NoError(t, err)

	parsed, perr := url.Parse(address)
	require.NoError(t, perr)
	host, _, serr := net.SplitHostPort(parsed.Host)
	require.NoError(t, serr)
	assert.True(t, net.ParseIP(host).IsLoopback(), "交给系统浏览器的地址不是环回的: "+address)
	assert.True(t, listenerAlive(t, address))
}

// Given 一条转发正开着, When 用户把这条映射**停用**, Then 它的专属监听关掉;而
// 把它启用回来(同一个绑定、另一个入参)不会误关任何东西。
//
// 后半句是这个用例的真判据:「凡是调过 SetEnabled 就关」照样能让前半句过。
func TestPortForwardSetEnabledDisablingClosesThatListenerAndEnablingDoesNot(t *testing.T) {
	stub := &stubPortForwardSvc{mapping: &port_forward_svc.MappingView{ID: "11", Port: 5173}}
	a, _ := withForwards(t, stub)

	address, err := a.PortForwardOpen("7", "11", 5173)
	require.NoError(t, err)

	// 启用不关。
	_, err = a.PortForwardSetEnabled("7", "11", true)
	require.NoError(t, err)
	assert.True(t, listenerAlive(t, address), "把映射启用回来却把它的监听关掉了")

	// 停用关。
	_, err = a.PortForwardSetEnabled("7", "11", false)
	require.NoError(t, err)
	requireListenerGone(t, address)
}

// Given 两条转发开着, When 用户删掉其中一条, Then 只有那一条的监听消失。
//
// 「只有那一条」是真判据:一关关一片的实现照样能让「那一条没了」成立。
func TestPortForwardDeleteClosesOnlyThatMappingsListener(t *testing.T) {
	a, _ := withForwards(t, &stubPortForwardSvc{})

	doomed, err := a.PortForwardOpen("7", "11", 5173)
	require.NoError(t, err)
	kept, err := a.PortForwardOpen("7", "12", 3000)
	require.NoError(t, err)

	require.NoError(t, a.PortForwardDelete("7", "11"))

	requireListenerGone(t, doomed)
	assert.True(t, listenerAlive(t, kept), "删掉一条映射把同一台设备上另一条的监听也带走了")
}

// Given 好几条转发开着, When App 退出, Then 一条不剩,租约也全还给了连接池。
//
// 「App 退出即失效」与网关 token 同一口径。Shutdown 之后紧接着就是进程退出,
// 挂在 goroutine 里的清理未必跑得完 —— 所以这一条必须在 Shutdown 返回前就成立。
func TestShutdownReleasesEveryPortForwardListener(t *testing.T) {
	a, devices := withForwards(t, &stubPortForwardSvc{})
	a.shutdownCleanup = func(context.Context) {}

	first, err := a.PortForwardOpen("7", "11", 5173)
	require.NoError(t, err)
	second, err := a.PortForwardOpen("8", "21", 3000)
	require.NoError(t, err)

	a.Shutdown(context.Background())

	requireListenerGone(t, first)
	requireListenerGone(t, second)
	devices.mu.Lock()
	releases := devices.releases
	devices.mu.Unlock()
	assert.Equal(t, 2, releases, "监听关了但租约没还回去:连接池里那条 entry 的引用计数永远降不下来")
}

// stubRemoteDeviceSvc 只答「解除配对成功」。这条用例问的不是 Remove 干了什么,
// 而是**解除配对之后本机那条入口还在不在**。
type stubRemoteDeviceSvc struct {
	remote_device_svc.RemoteDeviceSvc
	removed int64
}

func (s *stubRemoteDeviceSvc) Remove(_ context.Context, id int64) error {
	s.removed = id
	return nil
}

// Given 一台设备上的转发正开着, When 用户把这台设备**解除配对**, Then 它那条专属
// 监听一并关掉,而另一台设备的监听不受影响。
//
// 少了这一条,解除配对只是软删了行、清了 keychain、停了 watcher —— 连接池没有按设备
// 逐出的入口,而这条监听握着一条**长活**租约(refcount 永远 ≥ 1),idle 回收因此也
// 轮不到它。于是 lease.Closed() 永远不来,那条 127.0.0.1 地址在余下的整个 App 生命期
// 里继续把 HTTP 转进一台刚被解除配对的机器:界面上设备已经没了,本机入口还活着,
// 两半对不上。
//
// 「另一台不受影响」是这个用例的真判据:一关关一片(CloseAll)照样能让前半句成立。
func TestRemoteDeviceRemoveClosesThatDevicesForwardListeners(t *testing.T) {
	a, _ := withForwards(t, &stubPortForwardSvc{})
	original := remote_device_svc.Default()
	t.Cleanup(func() { remote_device_svc.SetDefault(original) })
	remote_device_svc.SetDefault(&stubRemoteDeviceSvc{})

	doomed, err := a.PortForwardOpen("7", "11", 5173)
	require.NoError(t, err)
	kept, err := a.PortForwardOpen("8", "21", 3000)
	require.NoError(t, err)

	require.NoError(t, a.RemoteDeviceRemove(7))

	requireListenerGone(t, doomed)
	assert.True(t, listenerAlive(t, kept),
		"解除配对一台设备,把另一台设备上还开着的转发监听也带走了")
}

package portforward_test

import (
	"context"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/portforward"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// 本文件钉的是**这条专属监听的生命周期**,不是它转发了什么(那在
// pkg/wire/portforwardhost 里,两个宿主共用那一份)。
//
// 规格「访问与鉴权」桌面端那一段给了这条监听四件必须成立的事:只绑环回、跟随这次
// 打开、映射停用/删除或设备离线即关、App 退出即全部释放。四件里有三件是「什么时候
// 消失」——而 Gateway.Stop 在生产路径上一个调用点都没有(靠进程退出释放),所以这里
// 一件都不能只写在代码里而不证。

func target(deviceID int64, mappingID string, port int) portforward.Target {
	return portforward.Target{DeviceID: deviceID, MappingID: mappingID, Port: port}
}

// Given 一条端口映射, When 桌面端为它打开一条转发, Then 它拿到一条**只绑环回**的
// 专属监听地址。
//
// 「只绑环回」不能靠读代码里写没写 127.0.0.1 来证:`net.Listen("tcp", ":0")` 一样跑得
// 通,一样能从本机连上,区别只在局域网里的别人也连得上 —— 那正是规格里这条限制要挡
// 的事。所以这里既看交出来的地址是不是环回,也真的从一个非环回的本机地址上打过去。
func TestOpen_GivenAMapping_WhenTheDesktopOpensIt_ThenItBindsALoopbackOnlyListener(t *testing.T) {
	t.Parallel()
	devices, _ := newDevices(t, 7)
	listeners := portforward.NewListeners(devices)
	t.Cleanup(listeners.CloseAll)

	address, err := listeners.Open(context.Background(), target(7, "11", 5173))
	require.NoError(t, err)

	parsed, err := url.Parse(address)
	require.NoError(t, err)
	assert.Equal(t, "http", parsed.Scheme, "交给系统浏览器的是一条 http 地址")
	host, port, err := net.SplitHostPort(parsed.Host)
	require.NoError(t, err)
	require.NotEmpty(t, port)
	ip := net.ParseIP(host)
	require.NotNil(t, ip, "地址里那一格必须是 IP 而不是主机名 —— 主机名解析得到什么由这台机器的 hosts 决定")
	assert.True(t, ip.IsLoopback(), "这条监听绑到了非环回地址 "+host+":局域网里的别人也够得着它了")

	// 端口确实在这台机器上开着 —— 否则上面那条断言在一个根本没绑成的地址上也成立。
	assert.True(t, dialable(t, address))

	// 同一个端口从非环回的本机地址上连不上。没有非环回地址的机器(纯离线 CI)跳过
	// 这一半:那种机器上「绑 0.0.0.0」与「绑 127.0.0.1」本来就没有可观察的差别。
	outward := firstNonLoopbackIPv4(t)
	if outward == "" {
		t.Log("这台机器没有非环回 IPv4,跳过对外可达性那一半")
		return
	}
	conn, derr := net.DialTimeout("tcp", net.JoinHostPort(outward, port), 500*time.Millisecond)
	if derr == nil {
		_ = conn.Close()
	}
	assert.Error(t, derr, "从 "+outward+" 连上了这条监听:它没有只绑环回")
}

// Given 同一条映射被打开了两次(用户又点了一下「打开」), When 第二次 Open 返回,
// Then 交出的还是同一条地址,而不是又绑一个端口。
//
// 每点一次就多一条监听的话,浏览器里那个标签页指向的地址会与用户手上刚复制的那条
// 不是同一个,而先前那条没人再关得掉 —— 它已经不在任何一份索引里了。
func TestOpen_GivenTheSameMappingOpenedTwice_ThenTheAddressStaysTheSame(t *testing.T) {
	t.Parallel()
	devices, _ := newDevices(t, 7)
	listeners := portforward.NewListeners(devices)
	t.Cleanup(listeners.CloseAll)

	first, err := listeners.Open(context.Background(), target(7, "11", 5173))
	require.NoError(t, err)
	second, err := listeners.Open(context.Background(), target(7, "11", 5173))
	require.NoError(t, err)

	assert.Equal(t, first, second)

	// 只借过一条租约:关掉这条映射之后正好还回去一条。第二次 Open 若也借了一条,
	// 这里会剩一条没人还得掉的租约。
	listeners.CloseMapping(7, "11")
	requireGone(t, first)
	assert.EqualValues(t, 1, devices.releaseCount(7), "第二次 Open 又借了一条租约,而只有一条被还回去")
}

// Given 一条转发正开着, When 这条映射被关掉(停用与删除在这一层是同一件事:这条
// 声明此后不该再有入口), Then 那条监听不再受理连接,而**同一台设备上另一条**映射
// 的监听照旧。
//
// 「另一条照旧」是这个用例的真判据:把关闭做成「一关关一片」的实现照样能让第一条
// 断言过。
func TestCloseMapping_GivenTwoMappingsOnOneDevice_WhenOneIsClosed_ThenOnlyThatListenerGoesAway(t *testing.T) {
	t.Parallel()
	devices, _ := newDevices(t, 7)
	listeners := portforward.NewListeners(devices)
	t.Cleanup(listeners.CloseAll)

	closing, err := listeners.Open(context.Background(), target(7, "11", 5173))
	require.NoError(t, err)
	staying, err := listeners.Open(context.Background(), target(7, "12", 3000))
	require.NoError(t, err)

	listeners.CloseMapping(7, "11")

	requireGone(t, closing)
	assert.True(t, dialable(t, staying), "关掉一条映射把同一台设备上另一条也带走了")
}

// Given 两台设备各开着一条转发, When 其中一台掉线(连接池把那条租约判失效),
// Then 只有它名下的监听关掉,另一台的照旧。
//
// 掉线这条路上没有人来调 Close:是这条监听自己盯着租约的失效信号。生产上那个信号
// 由 remote_device_svc 的连接池在 daemon drop 时发出。
func TestOpen_GivenADeviceGoesOffline_ThenItsListenerClosesAndOtherDevicesAreUntouched(t *testing.T) {
	t.Parallel()
	devices, _ := newDevices(t, 7, 8)
	listeners := portforward.NewListeners(devices)
	t.Cleanup(listeners.CloseAll)

	offline, err := listeners.Open(context.Background(), target(7, "11", 5173))
	require.NoError(t, err)
	other, err := listeners.Open(context.Background(), target(8, "21", 5173))
	require.NoError(t, err)

	devices.deviceGoesOffline(7)

	requireGone(t, offline)
	assert.True(t, dialable(t, other), "一台设备掉线把另一台的监听也带走了")
}

// Given 好几台设备上开着好几条转发, When App 退出调 CloseAll, Then 一条不剩,
// 而且每条租约都还给了连接池。
//
// 租约是**长活**的(见 listeners.go 里的理由),所以「关掉监听」与「还掉租约」是两
// 件事:只关监听会让连接池里那条 entry 的引用计数永远降不下来。
func TestCloseAll_GivenSeveralOpenForwards_WhenTheAppShutsDown_ThenEveryListenerAndLeaseIsReleased(t *testing.T) {
	t.Parallel()
	devices, _ := newDevices(t, 7, 8)
	listeners := portforward.NewListeners(devices)

	first, err := listeners.Open(context.Background(), target(7, "11", 5173))
	require.NoError(t, err)
	second, err := listeners.Open(context.Background(), target(7, "12", 3000))
	require.NoError(t, err)
	third, err := listeners.Open(context.Background(), target(8, "21", 8080))
	require.NoError(t, err)

	listeners.CloseAll()

	requireGone(t, first)
	requireGone(t, second)
	requireGone(t, third)
	assert.EqualValues(t, 2, devices.releaseCount(7), "设备 7 上两条转发的租约没全还回去")
	assert.EqualValues(t, 1, devices.releaseCount(8), "设备 8 上那条转发的租约没还回去")
}

// firstNonLoopbackIPv4 找一个本机的非环回 IPv4 地址;没有就返回空串。
func firstNonLoopbackIPv4(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		if v4 := ipNet.IP.To4(); v4 != nil {
			return v4.String()
		}
	}
	return ""
}

// ---------- 改声明的不是这台桌面端 ----------
//
// 上面那条 CloseMapping 用例走的是**本机发起**的那一路(用户就在这台桌面端上点停用 /
// 删除,绑定层顺手关掉监听)。别的客户端 —— 浏览器控制台,或另一台桌面端 —— 改同一条
// 声明时,这台桌面端不在那次调用里,没人来调 CloseMapping,而地址还留在用户手上。
// 规格「断开与失败」要的是「桌面端那条专属监听一并关掉」,所以这条监听必须自己听设备
// 发来的撤销通知。
//
// 通知按**端口**认人(见 wire.proto 里 PortForwardRevokedNotification 的理由),所以
// 这里两条映射用两个不同的端口。

// revokePort 让设备那一侧宣布这个端口不再允许转发 —— 生产上由 daemon 的声明族在
// SetEnabled / Delete 之后发出,不论改它的是哪一个客户端。
func revokePort(t *testing.T, device *protorpc.Conn, port uint32, reason string) {
	t.Helper()
	require.NoError(t, device.Notify(&agentrewire.RpcNotification{
		Payload: &agentrewire.RpcNotification_PortForwardRevoked{
			PortForwardRevoked: &agentrewire.PortForwardRevokedNotification{Port: port, Reason: reason},
		},
	}))
}

// Given 同一台设备上开着两条转发, When 别的客户端把其中一条停用 / 删除(设备因此发来
// 一条撤销通知), Then 只有那一条的监听关掉、它的租约还回连接池,另一条照旧。
//
// 「另一条照旧」是这里的真判据:一个「一收到撤销就把这台设备上的监听全关掉」的实现
// 同样能让第一条断言过,而用户别的映射的标签页会一起白掉。
func TestOpen_GivenAnotherClientRevokesTheMapping_ThenOnlyThatListenerGoesAway(t *testing.T) {
	t.Parallel()
	for name, reason := range map[string]string{"停用": "mapping_disabled", "删除": "mapping_removed"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			devices, far := newDevices(t, 7)
			listeners := portforward.NewListeners(devices)
			t.Cleanup(listeners.CloseAll)

			revoked, err := listeners.Open(context.Background(), target(7, "11", 5173))
			require.NoError(t, err)
			staying, err := listeners.Open(context.Background(), target(7, "12", 3000))
			require.NoError(t, err)

			revokePort(t, far[7], 5173, reason)

			requireGone(t, revoked)
			assert.True(t, dialable(t, staying), "别的客户端撤销一条映射把同一台设备上另一条也带走了")
			assert.EqualValues(t, 1, devices.releaseCount(7),
				"被撤销那条监听的租约必须还回连接池 —— 只关监听会让池里那条 entry 的引用计数永远降不下来")
		})
	}
}

// Given 对面是一个不认识这条通知的旧构建, When 它收到 / 发出这条协议上多出来的通知,
// Then 这一侧的监听照旧、连接照旧,能力退化成本任务之前的样子(地址还在,访问被设备侧
// 闸门挡下),而不是崩溃或把连接拆掉。
//
// 这里用一条 payload 空着的通知代表「不认识」:protobuf 把不认识的 oneof 字段号收进
// unknown fields,GetPayload() 因此返回 nil —— 与旧构建解出来的形状逐字相同。后半段
// 再发一条真的撤销通知,是为了证明前一条没有把这条连接的派发弄坏(只断言「监听还在」
// 的话,一个把连接拆掉的实现同样过得去)。
func TestOpen_GivenAPeerThatDoesNotKnowThisNotification_ThenTheListenerAndTheConnectionSurvive(t *testing.T) {
	t.Parallel()
	devices, far := newDevices(t, 7)
	listeners := portforward.NewListeners(devices)
	t.Cleanup(listeners.CloseAll)

	address, err := listeners.Open(context.Background(), target(7, "11", 5173))
	require.NoError(t, err)
	barrier, err := listeners.Open(context.Background(), target(7, "12", 3000))
	require.NoError(t, err)

	require.NoError(t, far[7].Notify(&agentrewire.RpcNotification{}))

	// 通知在读循环里按序派发,所以「另一条监听因为它后面那条撤销而关掉了」就证明这条
	// 读不懂的通知已经被派发过了 —— 少了这一步,下面那句断言只是抢在派发之前问的,
	// 一个「见到通知就关」的实现照样蒙混过去(这条用例最初就是这么写的,变异检查判绿)。
	revokePort(t, far[7], 3000, "mapping_disabled")
	requireGone(t, barrier)

	// 关闭本身是异步的(回调不在读循环里跑),所以这里问的是「一直没关」而不是「此刻
	// 还没关」。
	require.Never(t, func() bool { return !dialable(t, address) },
		300*time.Millisecond, 30*time.Millisecond, "一条读不懂的通知不该带走别的监听")

	// 连接本身也还好着:一条真的撤销照旧到得了。
	revokePort(t, far[7], 5173, "mapping_disabled")
	requireGone(t, address)
}

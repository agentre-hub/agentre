// Package portforward 是**桌面端**那一侧的端口转发入口:为一条端口映射在
// `127.0.0.1:<随机端口>` 上绑一条专属监听,把打到它上面的 HTTP 请求经这台桌面端
// 已有的那条设备连接交给被访问的设备(规格「访问与鉴权」桌面端那一段)。
//
// 「一个请求怎么变成设备上的一条转发流」本身**不在这里**:那一段两个宿主同构,住在
// pkg/wire/portforwardhost(Go 侧唯一被批准的跨仓共享通道),本包只管监听的生命周期、
// 租约与索引。
//
// # 为什么不是 internal/pkg/httpgateway 上的又一条路由
//
// Gateway 的身份是「**一个**地址、一份 GatewayStatus、按设置 Restart」:它的 URL() 与
// Status() 被四个 svc 和前端消费,塞进 N 个随机端口会让这两个返回值失去唯一含义。而
// 规格决策 3 的整个论据正是桌面端这条路要**天然跨源** —— 挂进同一个 mux 会把它变回
// 同源,还会与 /ctl/* 和 /mcp/* 撞路径,被转发应用的绝对路径资源(/assets/x.js)也会
// 打到网关根上。生命周期也不同源:Gateway 跟随 App 设置,这条监听跟随「这次打开 /
// 这条声明的启用位 / 这台设备在不在线」。
//
// # 租约为什么是长活的
//
// 别处(port_forward_svc / workspace_fs_svc)都是 Borrow → 一次往返 → defer Release:
// 那些调用在一次 RPC 内结束,租约自然跟着结束。转发不是 —— 一条监听可能开一整天,期间
// 一个字节都不走(用户开了标签页就晾着)。按次借的话,连接池在两次请求之间就把那条
// entry 收了,而这条监听正是靠**这一条**连接活着:它得盯住租约的失效信号才知道设备什么
// 时候掉线。所以租约的生存期与监听严格相等 —— Open 时借,监听关掉时(且只在那时)还。
package portforward

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/agentre-hub/agentre/pkg/wire/portforwardhost"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// Target 指名一条要打开的映射。端口由调用方带来而不是本包去设备上查:
// 「这个端口声明过没有、停用没有」的判定恒在设备侧(规格决策 8),本包多查一遍
// 既拦不住什么,又会让同一件事有两个判定处。端口填错的后果是设备回 -32070,
// 浏览器上如实呈现。
type Target struct {
	DeviceID  int64
	MappingID string
	Port      int
}

// DeviceConn 是一条已经连上那台设备的连接,外加它的失效信号。
//
// 本包住在 internal/pkg,不能反向依赖 service 层,所以这里只声明用得着的那三件事;
// 生产上的实现是 remote_device_svc 连接池的租约,由 internal/app 适配进来。
type DeviceConn interface {
	Conn() *protorpc.Conn
	Closed() <-chan struct{}
	Release()
}

// Devices 是「按设备号要一条连接」这一件事。
type Devices interface {
	Borrow(ctx context.Context, deviceID int64) (DeviceConn, error)
}

var (
	// ErrShutDown:CloseAll 之后再 Open。App 已经在退出了,这时候再绑一个端口没有意义。
	ErrShutDown = errors.New("portforward: listeners already shut down")
	// ErrInvalidPort:端口号不在 1..65535 内。挡在这里不是替设备做判定(那一条恒在
	// 设备侧),而是因为越界的数转成 uint32 会在线上变成**另一个端口号**,设备照着那
	// 个数去判定 —— 与 port_forward_svc.Create 同一个理由。
	ErrInvalidPort = errors.New("portforward: port out of range")
)

// readHeaderTimeout 与 httpgateway 取同一个值:它只覆盖读请求头这一段,不影响之后
// 的长连接与大传输。
const readHeaderTimeout = 30 * time.Second

// Listeners 是本进程内开着的全部专属监听。
type Listeners struct {
	devices Devices

	mu   sync.Mutex
	open map[mappingKey]*forward
	shut bool
}

type mappingKey struct {
	device  int64
	mapping string
}

// forward 是一条映射的那一条监听。
type forward struct {
	address string
	server  *http.Server
	proxy   *portforwardhost.Proxy
	lease   DeviceConn
	stop    chan struct{}
	once    sync.Once
}

func (f *forward) close() {
	f.once.Do(func() {
		close(f.stop)
		// Close 而不是 Shutdown:规格说的是「正在进行的流立即关闭」。优雅关闭会等
		// 那些还开着的转发自己结束,而它们正是这次关闭要收掉的东西 —— 一条挂在被
		// 删掉的映射上的 WebSocket 可以永远不结束。
		_ = f.server.Close()
		f.proxy.Close()
		f.lease.Release()
	})
}

func NewListeners(devices Devices) *Listeners {
	return &Listeners{devices: devices, open: make(map[mappingKey]*forward)}
}

// Open 为这条映射绑一条专属监听,交回它的访问地址(形如 http://127.0.0.1:54321)。
//
// 同一条映射重复 Open 交回同一条地址:用户再点一次「打开」不该多出一条没人索引得到、
// 因而没人关得掉的监听。
func (l *Listeners) Open(ctx context.Context, target Target) (string, error) {
	if target.Port < 1 || target.Port > 65535 {
		return "", ErrInvalidPort
	}
	key := mappingKey{device: target.DeviceID, mapping: target.MappingID}

	l.mu.Lock()
	if l.shut {
		l.mu.Unlock()
		return "", ErrShutDown
	}
	if existing, ok := l.open[key]; ok {
		address := existing.address
		l.mu.Unlock()
		return address, nil
	}
	l.mu.Unlock()

	// 借租约要拨号,不能握着锁做 —— 一台设备连不上会把别的映射的 Open 一起卡住。
	lease, err := l.devices.Borrow(ctx, target.DeviceID)
	if err != nil {
		return "", err
	}

	// 只绑 IPv4 环回。写死主机那一格是这条限制唯一的落点:换成 ":0" 一样跑得通、
	// 一样能从本机连上,区别只在局域网里的别人也够得着(用例 …ThenItBindsALoopbackOnly…
	// 会判红)。
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		lease.Release()
		return "", err
	}

	// 上面刚把端口夹在 1..65535 内,转 uint32 无损。
	//
	// 第三个参数是**别的客户端**改了这条声明时的出口(浏览器控制台,或另一台桌面端)。
	// 本机发起的那一路由绑定层顺手关掉,这一路没有人来调 CloseMapping —— 设备发来的
	// 撤销通知是这台桌面端唯一的知情点。关的是这一条 key,不是这台设备名下的一片:
	// 撤销说的是一个端口,不是一台机器。
	//
	// 通知漏掉会怎样(旧设备不发、连接刚好在断):这条监听留着,访问照旧被设备侧的
	// 闸门挡下(-32070 / -32071 的专页)。地址「看起来还活着」是这条通路唯一的退化
	// 形态,不会变成拆连接或崩溃。连接真断掉时租约会失效,下面那个 goroutine 会把整
	// 条监听关掉,所以断线期间漏掉的撤销不会留下一条活着的监听,重连之后也就没有要
	// 对账的东西。
	proxy := portforwardhost.NewProxy(lease.Conn(), uint32(target.Port), func(string) {
		l.CloseMapping(target.DeviceID, target.MappingID)
	})
	entry := &forward{
		address: "http://" + listener.Addr().String(),
		server:  &http.Server{Handler: proxy, ReadHeaderTimeout: readHeaderTimeout},
		proxy:   proxy,
		lease:   lease,
		stop:    make(chan struct{}),
	}

	l.mu.Lock()
	if l.shut {
		l.mu.Unlock()
		_ = listener.Close()
		proxy.Close()
		lease.Release()
		return "", ErrShutDown
	}
	if existing, ok := l.open[key]; ok {
		// 两个 Open 撞在一起(用户连点两下)。留先到的那条,这一条整个丢掉。
		address := existing.address
		l.mu.Unlock()
		_ = listener.Close()
		proxy.Close()
		lease.Release()
		return address, nil
	}
	l.open[key] = entry
	l.mu.Unlock()

	go func() {
		_ = entry.server.Serve(listener)
	}()
	// 设备掉线这条路上没有人来调 Close:是这条监听自己盯着租约的失效信号。生产上
	// 那个信号由连接池在 daemon drop / Pool.Close 时发出。
	go func() {
		select {
		case <-lease.Closed():
			l.CloseMapping(target.DeviceID, target.MappingID)
		case <-entry.stop:
		}
	}()

	return entry.address, nil
}

// CloseMapping 关掉一条映射的监听。停用与删除在这一层是同一件事:这条声明此后不该
// 再有本机入口。幂等 —— 没开过的映射是 no-op。
func (l *Listeners) CloseMapping(deviceID int64, mappingID string) {
	key := mappingKey{device: deviceID, mapping: mappingID}
	l.mu.Lock()
	entry, ok := l.open[key]
	delete(l.open, key)
	l.mu.Unlock()
	if ok {
		entry.close()
	}
}

// CloseDevice 关掉这台设备名下的全部监听。
func (l *Listeners) CloseDevice(deviceID int64) {
	l.mu.Lock()
	var doomed []*forward
	for key, entry := range l.open {
		if key.device == deviceID {
			doomed = append(doomed, entry)
			delete(l.open, key)
		}
	}
	l.mu.Unlock()
	for _, entry := range doomed {
		entry.close()
	}
}

// CloseAll 释放全部监听与它们持有的租约,此后不再受理 Open。App 退出走这一条。
func (l *Listeners) CloseAll() {
	l.mu.Lock()
	doomed := make([]*forward, 0, len(l.open))
	for key, entry := range l.open {
		doomed = append(doomed, entry)
		delete(l.open, key)
	}
	l.shut = true
	l.mu.Unlock()
	for _, entry := range doomed {
		entry.close()
	}
}

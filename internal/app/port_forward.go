package app

import (
	"context"
	"errors"
	"strconv"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/portforward"
	"github.com/agentre-hub/agentre/internal/service/port_forward_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

/*
端口转发**声明族**的四个绑定:列举 / 新增 / 启停 / 删除某台设备上的端口映射。

映射存在被访问的那台设备上(规格决策 1),所以每个方法都是一次出站往返 —— 这一
层只把参数递过去,不做判定。

这四个绑定的错误**带着业务码过桥**(见 coded_error.go)。设备转发这一面要把三件
出路完全不同的事分开说:那台机器够不着(等它回来,列表出离线态且不出新增入口)、
这个端口已经映射过了(换一个端口)、端口号越界(把它填对)。只交一句本地化文本
的话,前端只能把它们折成同一句「操作失败」,而用户不知道该做哪件事。

deviceID 与 mappingID 都是字符串:两者在设备侧都是 int64,交给 JS 的 number 会丢
精度,而前端只把它们当句柄回传。deviceID 的字符串化与 RemoteFsListDir 一致。
*/

// PortForwardList 列举 device 上的全部端口映射(含已停用的)。
//   - deviceID = paired_agentred.id 字符串化
//   - 设备够不着时以 code.PortForwardDeviceOffline 失败,前端据此渲染离线态
func (a *App) PortForwardList(deviceID string) ([]port_forward_svc.MappingView, error) {
	views, err := port_forward_svc.Default().List(a.ctx, deviceID)
	if err != nil {
		return nil, codedError(err)
	}
	return views, nil
}

// PortForwardCreate 在 device 上新增一条映射,交回设备落库之后的那一行(默认
// 启用)。端口已被声明 / 端口越界各有各的码,新增表单据此给不同的提示。
func (a *App) PortForwardCreate(deviceID string, port int, name string) (*port_forward_svc.MappingView, error) {
	view, err := port_forward_svc.Default().Create(a.ctx, deviceID, port, name)
	if err != nil {
		return nil, codedError(err)
	}
	return view, nil
}

// PortForwardSetEnabled 启停一条映射,交回改完之后的那一行。停用保留声明本身,
// 只是此后的访问一律被拒 —— 本机那条专属监听也一并关掉(规格「断开与失败」)。
//
// 只在设备**确认停用之后**才关那条监听:调用失败时那条声明在设备上还是启用的,
// 关掉监听会让用户手上刚复制的地址无缘无故失效。而真正的闸门恒在设备侧,监听留着
// 也访问不成 —— 它会拿到 -32071,浏览器上如实呈现。
func (a *App) PortForwardSetEnabled(deviceID, mappingID string, enabled bool) (*port_forward_svc.MappingView, error) {
	view, err := port_forward_svc.Default().SetEnabled(a.ctx, deviceID, mappingID, enabled)
	if err != nil {
		return nil, codedError(err)
	}
	if !enabled {
		a.forwards().CloseMapping(parseForwardDeviceID(deviceID), mappingID)
	}
	return view, nil
}

// PortForwardDelete 删除一条映射。幂等:那一行已经被别的客户端删掉时这里不报错。
// 删除立即生效 —— 正在进行的转发流与那条专属监听一并关掉。
func (a *App) PortForwardDelete(deviceID, mappingID string) error {
	if err := port_forward_svc.Default().Delete(a.ctx, deviceID, mappingID); err != nil {
		return codedError(err)
	}
	a.forwards().CloseMapping(parseForwardDeviceID(deviceID), mappingID)
	return nil
}

// PortForwardOpen 为这条映射在本机绑一条专属监听,交回它的访问地址(形如
// http://127.0.0.1:54321)。前端拿到之后用 BrowserOpenURL 交给系统默认浏览器
// (规格决策 11)。
//
// 端口由调用方带来而不是这一层再去设备上查一遍:「这个端口声明过没有、停用没有」
// 的判定恒在设备侧(规格决策 8),这一层多查一遍既拦不住什么,又会让同一件事有两个
// 判定处。端口填错的后果是设备回 -32070,浏览器上如实呈现。
//
// 重复调用同一条映射交回同一条地址 —— 用户再点一次「打开」不该多出一条没人索引得到
// 因而没人关得掉的监听。
func (a *App) PortForwardOpen(deviceID, mappingID string, port int) (string, error) {
	id, err := strconv.ParseInt(deviceID, 10, 64)
	if err != nil || id <= 0 {
		return "", codedError(i18n.NewError(a.ctx, code.RemoteDeviceNotFound))
	}
	address, oerr := a.forwards().Open(a.ctx, portforward.Target{
		DeviceID: id, MappingID: mappingID, Port: port,
	})
	if oerr != nil {
		return "", codedError(mapForwardOpenErr(a.ctx, oerr))
	}
	return address, nil
}

// forwards 交出本进程的专属监听集合,首次用到时装配。
func (a *App) forwards() *portforward.Listeners {
	a.portForwardsMu.Lock()
	defer a.portForwardsMu.Unlock()
	if a.portForwards == nil {
		a.portForwards = portforward.NewListeners(poolDevices{})
	}
	return a.portForwards
}

// parseForwardDeviceID 只在「关掉这条映射的监听」这条路上用。解不出来就给 0 ——
// 那是一个不会命中任何一条监听的设备号,关闭因此是 no-op。这条路上没有可报的错:
// 声明族那一次调用已经成功了,用户要的结果已经达成。
func parseForwardDeviceID(deviceID string) int64 {
	id, err := strconv.ParseInt(deviceID, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// mapForwardOpenErr 把「绑不上这条监听」翻成业务码。
//
// 这段分类住在绑定层而不是 port_forward_svc:那一层是**声明族**的入口(它明说自己
// 不碰本机监听,MappingView.Address 留给上层补),而专属监听是宿主自己的东西。
//
// 兜底给「设备够不着」的理由:借不出租约是这条路上唯一会常态失败的一步,而
// net.Listen("127.0.0.1:0") 在本机没有可预期的失败面。两者对用户的出路也是同一条
// —— 等一会儿再点一次。
func mapForwardOpenErr(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, portforward.ErrInvalidPort):
		return i18n.NewError(ctx, code.PortForwardInvalidPort)
	case errors.Is(err, remote_device_svc.ErrDeviceNotFound):
		return i18n.NewError(ctx, code.RemoteDeviceNotFound)
	case errors.Is(err, remote_device_svc.ErrDeviceUnauthorized):
		return i18n.NewError(ctx, code.RemoteDeviceUnauthorized)
	}
	return i18n.NewError(ctx, code.PortForwardDeviceOffline)
}

// poolDevices 把 remote_device_svc 的连接池适配成 portforward 要的那三件事。
// internal/pkg 不能反向依赖 service 层,所以这层适配住在绑定层。
type poolDevices struct{}

func (poolDevices) Borrow(ctx context.Context, deviceID int64) (portforward.DeviceConn, error) {
	lease, err := remote_device_svc.Default().Pool().Borrow(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	return poolLease{Lease: lease}, nil
}

// poolLease 只补一个 Conn():Closed() 与 Release() 由租约本身满足。
type poolLease struct{ remote_device_svc.Lease }

func (l poolLease) Conn() *protorpc.Conn { return l.Lease.Client().Conn() }

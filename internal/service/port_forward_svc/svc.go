// Package port_forward_svc 是端口转发**声明族**在桌面端这一侧的入口:列举 /
// 新增 / 启停 / 删除某台设备上的端口映射。
//
// 映射存在被访问的那台设备上,不存在这一侧(规格决策 1)。因此这一层没有仓储、
// 不碰 DB —— 每个方法都只是一次出站往返:经 remote_device_svc 的连接池借一条
// 租约,发一次 portforward.* RPC,把设备答复的那几行翻成视图。直连与经中转两条
// 路已经在连接池里收敛成同一个 protorpc.Conn,本层不区分。
//
// **失败照码分类,不照文案。** 设备侧用 rpcerror 的 -32070 段答复(端口没声明 /
// 端口已被占 / 端口越界),本层把它翻成 code.PortForward* 业务码,再由
// internal/app/coded_error.go 写成 `agentre-code:<码>` 过 wails 桥。视图层因此
// 分得开三件出路完全不同的事:等那台机器回来、换一个端口、把端口填对。按 Go
// 错误文本去 match 是错的做法 —— 文案一改就静默失灵,中英还得各猜一遍。
package port_forward_svc

import (
	"context"
	"errors"
	"strconv"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

// PortForwardSvc 给 Wails 绑定层调。
//
// deviceID 与 mappingID 都是字符串:两者在设备侧都是 int64,而 wails 那座桥把它
// 交给 JS 的 number 会丢精度;前端只把它们当句柄回传,不做算术。deviceID 的
// 字符串化与 remote_fs_svc / ProjectLocationSvc 一致。
type PortForwardSvc interface {
	// List 列举这台设备上的全部声明(含已停用的)。设备够不着时给
	// code.PortForwardDeviceOffline —— 视图层据此出离线态且**不出新增入口**。
	List(ctx context.Context, deviceID string) ([]MappingView, error)
	// Create 在这台设备上新增一条声明,交回设备落库之后的那一行(默认启用)。
	// 端口已被声明 / 端口越界各有各的码,新增表单据此给不同的提示。
	Create(ctx context.Context, deviceID string, port int, name string) (*MappingView, error)
	// SetEnabled 启停一条声明,交回改完之后的那一行。停用保留声明本身。
	SetEnabled(ctx context.Context, deviceID, mappingID string, enabled bool) (*MappingView, error)
	// Delete 删除一条声明。**幂等**:两个客户端同时删同一条时,后到的那次不是
	// 错误 —— 用户要的结果已经达成了(设备侧同一口径)。
	Delete(ctx context.Context, deviceID, mappingID string) error
}

// MappingView 是一条映射在界面上的那一行。字段与共享包的
// `PortForwardMappingView` 同形,前端零转换喂进去。
//
// Address 不由本层产出:桌面端那条 `127.0.0.1:<端口>` 地址要等本机监听绑上之后
// 才有(规格「两端的入口」),本层只管声明本身。留在这里是为了让视图类型与共享
// 包保持同一个形状,由上层补齐。
type MappingView struct {
	ID      string `json:"id"`
	Port    int    `json:"port"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Address string `json:"address,omitempty"`
}

var defaultSvc PortForwardSvc = &portForwardImpl{}

func Default() PortForwardSvc { return defaultSvc }

// SetDefault 换掉包级单例(绑定层单测用;composition root 用不到 —— 本服务没有
// 需要注入的依赖,deviceID 由调用方给,连接池走 remote_device_svc.Default())。
func SetDefault(s PortForwardSvc) { defaultSvc = s }

type portForwardImpl struct {
	// rdSvc 默认走 remote_device_svc.Default();单测注入 mock。
	rdSvc remote_device_svc.RemoteDeviceSvc
}

func (s *portForwardImpl) deviceSvc() remote_device_svc.RemoteDeviceSvc {
	if s.rdSvc != nil {
		return s.rdSvc
	}
	return remote_device_svc.Default()
}

func (s *portForwardImpl) List(ctx context.Context, deviceID string) ([]MappingView, error) {
	dID, err := parseDeviceID(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	resp, err := call(ctx, s, dID, wirecall.PortForwardList, &agentrewire.PortForwardListRequest{})
	if err != nil {
		return nil, err
	}
	// 恒非 nil:一条声明都没有时前端出的是空态,不是「读失败」。
	views := make([]MappingView, 0, len(resp.GetMappings()))
	for _, m := range resp.GetMappings() {
		views = append(views, toView(m))
	}
	return views, nil
}

func (s *portForwardImpl) Create(ctx context.Context, deviceID string, port int, name string) (*MappingView, error) {
	dID, err := parseDeviceID(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	// 越界的端口不能就这么转成 uint32 发出去:那会在线上变成另一个端口号,设备
	// 照着那个数去判定。给的是与设备侧同一个越界码 —— 同一件事在两条路径上不该
	// 被说成两句话。
	if port < 1 || port > 65535 {
		return nil, i18n.NewError(ctx, code.PortForwardInvalidPort)
	}
	// 上面刚把 port 夹在 1..65535 内,转 uint32 无损。
	resp, err := call(ctx, s, dID, wirecall.PortForwardCreate, &agentrewire.PortForwardCreateRequest{
		Port: uint32(port), Name: name,
	})
	if err != nil {
		return nil, err
	}
	view := toView(resp.GetMapping())
	return &view, nil
}

func (s *portForwardImpl) SetEnabled(ctx context.Context, deviceID, mappingID string, enabled bool) (*MappingView, error) {
	dID, err := parseDeviceID(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	mID, err := parseMappingID(ctx, mappingID)
	if err != nil {
		return nil, err
	}
	resp, err := call(ctx, s, dID, wirecall.PortForwardSetEnabled, &agentrewire.PortForwardSetEnabledRequest{
		Id: mID, Enabled: enabled,
	})
	if err != nil {
		return nil, err
	}
	view := toView(resp.GetMapping())
	return &view, nil
}

func (s *portForwardImpl) Delete(ctx context.Context, deviceID, mappingID string) error {
	dID, err := parseDeviceID(ctx, deviceID)
	if err != nil {
		return err
	}
	mID, err := parseMappingID(ctx, mappingID)
	if err != nil {
		return err
	}
	// 答复里的 deleted 如实说这一次有没有删到一行,两种都不是错误(设备侧刻意
	// 把删除做成幂等的)。本层因此不看它 —— 把 deleted=false 翻成一个错误会让
	// 「两个客户端同时删同一条」在后到的那一侧变成一次失败。
	_, err = call(ctx, s, dID, wirecall.PortForwardDelete, &agentrewire.PortForwardDeleteRequest{Id: mID})
	return err
}

// ── 出站 ────────────────────────────────────────────────────────────────────

// call 跑一次远端往返。租约在返回前释放,调用方不持有它。
//
// method 收的是 wirecall 里那个方法的**函数本身**,不是方法号加一个应答构造器
// ——「方法号 ↔ 消息类型」的配对因此一次都不在这里出现(同 workspace_fs_svc)。
func call[Req, Resp any](
	ctx context.Context, s *portForwardImpl, deviceID int64,
	method func(context.Context, wirecall.Caller, Req) (Resp, error), req Req,
) (Resp, error) {
	var zero Resp
	lease, err := s.deviceSvc().Pool().Borrow(ctx, deviceID)
	if err != nil {
		return zero, mapBorrowErr(ctx, err)
	}
	defer lease.Release()
	resp, cerr := method(ctx, lease.Client(), req)
	if cerr != nil {
		return zero, mapCallErr(ctx, cerr)
	}
	return resp, nil
}

func toView(m *agentrewire.PortForwardMapping) MappingView {
	return MappingView{
		ID:      strconv.FormatInt(m.GetId(), 10),
		Port:    int(m.GetPort()),
		Name:    m.GetName(),
		Enabled: m.GetEnabled(),
	}
}

func parseDeviceID(ctx context.Context, deviceID string) (int64, error) {
	id, err := strconv.ParseInt(deviceID, 10, 64)
	if err != nil || id <= 0 {
		return 0, i18n.NewError(ctx, code.RemoteDeviceNotFound)
	}
	return id, nil
}

// parseMappingID 解不出来时给「这条声明不在设备上」而不是一句通用参数错误:
// 对调用方来说这两件事的出路是同一条 —— 把列表重新拉一遍。
func parseMappingID(ctx context.Context, mappingID string) (int64, error) {
	id, err := strconv.ParseInt(mappingID, 10, 64)
	if err != nil || id <= 0 {
		return 0, i18n.NewError(ctx, code.PortForwardNotDeclared)
	}
	return id, nil
}

// ── 失败分类 ────────────────────────────────────────────────────────────────

// mapBorrowErr 翻译「连接借不出来」。设备已解除配对 / 凭据失效各有各的出路,
// 与「机器此刻不在线」分开说;其余一律当离线(那台机器过会儿可能就回来了)。
func mapBorrowErr(ctx context.Context, err error) error {
	return remote_device_svc.MapBorrowErr(ctx, err, code.PortForwardDeviceOffline)
}

// mapCallErr 翻译设备答复的失败码。归类不出来的一律落到通用远端调用失败 ——
// 编一个更具体的码比不给码更糟。
func mapCallErr(ctx context.Context, err error) error {
	var rpcErr *protorpc.Error
	if !errors.As(err, &rpcErr) {
		// 连接在这一跳里断了(答复根本没回来):与「借不出来」是同一件事,
		// 视图层看到的都是那台机器够不着。
		return i18n.NewError(ctx, code.PortForwardDeviceOffline)
	}
	switch int(rpcErr.Code) {
	case rpcerror.CodePortForwardNotDeclared:
		return i18n.NewError(ctx, code.PortForwardNotDeclared)
	case rpcerror.CodePortForwardPortTaken:
		return i18n.NewError(ctx, code.PortForwardPortTaken)
	case rpcerror.CodePortForwardInvalidPort:
		return i18n.NewError(ctx, code.PortForwardInvalidPort)
	}
	return i18n.NewError(ctx, code.RemoteRunnerCallFailed)
}

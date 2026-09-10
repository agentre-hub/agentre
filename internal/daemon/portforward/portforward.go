// Package portforward 是端口转发在**被访问的那台设备**上的判定面:这台机器允许把
// 它 127.0.0.1 上的哪些端口转出去(声明族),以及一次 open 该不该落地(授权闸门)。
//
// 两种执行端共用这一份实现。规格「设备侧的目标限制」把 agentred 与桌面端并列写成
// 「两类设备都可能是被访问的一方」,而它们共用同一份实体与仓储(port_forward_entity
// / port_forward_repo,各自一个 SQLite 库);判定若各写一份,两台机器对同一条声明就
// 会给出两种答复。包住在 internal/daemon/ 下而被桌面端一并 import,与 remotefs /
// workspacefs / handlers 是同一条既有路子。
//
// **授权闸门在拨号之前,而且不接受主机那一格。** DialDeclared 只收一个端口号:目标
// 恒为环回这件事因此是类型上的事实,而不是一句注释——调用方连表达「拨到别处」的
// 办法都没有。声明集判定同样在这里,不依赖任何客户端做对(决策 8)。
package portforward

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/port_forward_entity"
	"github.com/agentre-hub/agentre/internal/repository/port_forward_repo"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// 领域失败。每一个都直接带着过线的码:调用方按码分支(规格「断开与失败」要的正是
// 这个分辨率——「等那台机器回来」「去把服务起起来」「那个端口压根没被声明出来」是
// 三件不同的事)。*rpcerror.Error 同时是 protorpc.Error,wireinbound.ConvertError
// 原样放行,途中不会被折成 -32603。
var (
	// ErrNotDeclared:这台设备上没有这个端口的声明(从没建过,或已被删除)。
	ErrNotDeclared = &rpcerror.Error{Code: rpcerror.CodePortForwardNotDeclared, Message: "port forward: port not declared on this device"}
	// ErrDisabled:声明还在,但被停用了。与上一个分开,界面据此提示「把它打开」。
	ErrDisabled = &rpcerror.Error{Code: rpcerror.CodePortForwardDisabled, Message: "port forward: mapping disabled"}
	// ErrNoListener:端口过了声明集判定,但这台设备的环回地址上没有服务在监听。
	ErrNoListener = &rpcerror.Error{Code: rpcerror.CodePortForwardNoListener, Message: "port forward: nothing listening on that port"}
	// ErrPortTaken:新增声明时这个端口在这台设备上已经声明过。
	ErrPortTaken = &rpcerror.Error{Code: rpcerror.CodePortForwardPortTaken, Message: "port forward: port already declared"}
	// ErrInvalidPort:端口号不在 1..65535 内。
	ErrInvalidPort = &rpcerror.Error{Code: rpcerror.CodePortForwardInvalidPort, Message: "port forward: port out of range"}
)

// Dialer 拨到这台设备 127.0.0.1 上的一个端口。**没有主机那一格**,理由见包注释。
type Dialer func(ctx context.Context, port int) (net.Conn, error)

// Options 是宿主交出的那两件东西:自己那个库的仓储,以及拨号的办法。
type Options struct {
	// Repo 是这台设备上的声明集。两个宿主各自一个 SQLite 库,注入各自的实现。
	Repo port_forward_repo.PortForwardRepo
	// Dial 缺席时闸门照常判定,只是判完没有下一步——转发流那一半还没接上。
	Dial Dialer
}

// portRevoker 是「这个端口此刻起不再允许转发」的接收面。生产上唯一的实现是 Streams
// (一条连接上开着的流),而闸门只认这个接口:声明族因此不需要知道这台机器上有几条
// 连接、每条连接上挂着什么。
type portRevoker interface {
	revokePort(port int, reason closeReason)
}

// 撤销的两种由来。code 与 open 被拒时同一族(Disabled / NotDeclared):一条流为什么
// 断,和一次 open 为什么开不起来,在调用方那里是同一个分支表——两套词汇会让界面对
// 同一件事说两句话。
var (
	revokedDisabled = closeReason{
		code: rpcerror.CodePortForwardDisabled, token: "mapping_disabled",
		message: "port forward: mapping disabled",
	}
	revokedRemoved = closeReason{
		code: rpcerror.CodePortForwardNotDeclared, token: "mapping_removed",
		message: "port forward: mapping deleted",
	}
)

// Handlers 是设备侧的这一份判定。
type Handlers struct {
	repo port_forward_repo.PortForwardRepo
	dial Dialer

	// revokeMu 盖住撤销面的订阅表。持有它的每一段都只是几行 map 操作 —— 派发在锁外
	// 做(见 revoke),否则一条流的收尾会挡住另一条连接的订阅/注销。
	revokeMu sync.Mutex
	revokers map[portRevoker]struct{}
}

func NewHandlers(options Options) *Handlers {
	return &Handlers{repo: options.Repo, dial: options.Dial, revokers: make(map[portRevoker]struct{})}
}

// watchRevocations 把一份流表挂到撤销面上,交回注销它的办法。
//
// 注销是**订阅方自己的事**(Streams.CloseAll 调它),所以闸门不必跟踪连接的生命周期:
// 它手上要么是一份还在用的流表,要么什么都没有,不会攒下一堆走掉的连接。
func (h *Handlers) watchRevocations(revoker portRevoker) func() {
	h.revokeMu.Lock()
	h.revokers[revoker] = struct{}{}
	h.revokeMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			h.revokeMu.Lock()
			delete(h.revokers, revoker)
			h.revokeMu.Unlock()
		})
	}
}

// revoke 把「这个端口不再允许转发」派发给每一份订阅着的流表。
//
// 快照之后在锁外派发:声明族这一次 RPC 不该因为某条连接上的流表正忙而排队,而每个
// 订阅者要做的也只是关掉自己那几条流(收尾通知由流自己的 goroutine 发)。
func (h *Handlers) revoke(port int, reason closeReason) {
	h.revokeMu.Lock()
	revokers := make([]portRevoker, 0, len(h.revokers))
	for revoker := range h.revokers {
		revokers = append(revokers, revoker)
	}
	h.revokeMu.Unlock()
	for _, revoker := range revokers {
		revoker.revokePort(port, reason)
	}
}

// DialDeclared 是 open 的**授权闸门**:先判这个端口在不在声明集内、有没有被停用,
// 通过了才拨号。顺序是这个方法唯一的实质内容——先拨再拒,错误码照样是对的,而那台
// 设备上的服务已经收到了一次来自未授权端口的连接。
func (h *Handlers) DialDeclared(ctx context.Context, port int) (net.Conn, error) {
	mapping, err := h.repo.FindByPort(ctx, port)
	if err != nil {
		// 读不出来不能当作「没声明」:那会把一次故障说成一条用户能自己改正的输入
		// 错误,人会去重建一条本来就在的映射。
		logger.Ctx(ctx).Error("portforward.DialDeclared: 读声明集失败", zap.Int("port", port), zap.Error(err))
		return nil, &rpcerror.Error{Code: rpcerror.CodeInternal, Message: "port forward: cannot read declarations"}
	}
	if mapping == nil {
		return nil, ErrNotDeclared
	}
	if !mapping.Enabled {
		return nil, ErrDisabled
	}
	if h.dial == nil {
		return nil, &rpcerror.Error{Code: rpcerror.CodeInternal, Message: "port forward: no dialer wired on this host"}
	}
	conn, err := h.dial(ctx, port)
	if err != nil {
		// 判定过了还连不上,只剩一种解释是用户改得动的:那个端口上没有服务。
		logger.Ctx(ctx).Warn("portforward.DialDeclared: 拨本机服务失败", zap.Int("port", port), zap.Error(err))
		return nil, ErrNoListener
	}
	return conn, nil
}

// List 交出这台设备上的全部声明。不按来源收窄:规格明写任何一个有权连上这台设备的
// 客户端看到的是同一份。
func (h *Handlers) List(ctx context.Context, _ *agentrewire.PortForwardListRequest) (*agentrewire.PortForwardListResponse, error) {
	rows, err := h.repo.List(ctx)
	if err != nil {
		logger.Ctx(ctx).Error("portforward.List: 列举声明失败", zap.Error(err))
		return nil, internalError(err)
	}
	response := &agentrewire.PortForwardListResponse{Mappings: make([]*agentrewire.PortForwardMapping, 0, len(rows))}
	for _, row := range rows {
		response.Mappings = append(response.Mappings, toWire(row))
	}
	return response, nil
}

// Create 新增一条声明,交回**设备落库之后**的那一行。
func (h *Handlers) Create(ctx context.Context, request *agentrewire.PortForwardCreateRequest) (*agentrewire.PortForwardCreateResponse, error) {
	port, err := validPort(request.GetPort())
	if err != nil {
		return nil, err
	}
	// 预检答的是常见那一次:用户填了一个已经在列表里的端口,这是他就地改得动的
	// 输入错误。它**不是**唯一性的真相源——那是库上的 UNIQUE 索引(仓储包注释
	// 明写这一点),下面 Create 的失败因此要落回同一个码,否则同一件事在两条路径
	// 上会被说成两句话。
	existing, err := h.repo.FindByPort(ctx, port)
	if err != nil {
		logger.Ctx(ctx).Error("portforward.Create: 端口点查失败", zap.Int("port", port), zap.Error(err))
		return nil, internalError(err)
	}
	if existing != nil {
		return nil, ErrPortTaken
	}
	row := &port_forward_entity.PortForward{
		Port: port,
		Name: strings.TrimSpace(request.GetName()),
		// 刚填完的声明默认启用:用户新增它就是要用它,再让他多按一次开关没有道理。
		Enabled: true,
	}
	if err := h.repo.Create(ctx, row); err != nil {
		if isDuplicatePort(err) {
			return nil, ErrPortTaken
		}
		logger.Ctx(ctx).Error("portforward.Create: 落库失败", zap.Int("port", port), zap.Error(err))
		return nil, internalError(err)
	}
	return &agentrewire.PortForwardCreateResponse{Mapping: toWire(row)}, nil
}

// SetEnabled 启停一条声明。停用保留声明本身,只是此后的访问一律被拒。
func (h *Handlers) SetEnabled(ctx context.Context, request *agentrewire.PortForwardSetEnabledRequest) (*agentrewire.PortForwardSetEnabledResponse, error) {
	affected, err := h.repo.SetEnabled(ctx, request.GetId(), request.GetEnabled())
	if err != nil {
		logger.Ctx(ctx).Error("portforward.SetEnabled: 写启用位失败", zap.Int64("mappingId", request.GetId()), zap.Error(err))
		return nil, internalError(err)
	}
	if affected == 0 {
		// 要改的那一行确实不存在(另一个客户端刚删掉),调用方的列表落后了一步。
		// 这与「删除幂等」不冲突:那边是「本来就要它没有」,这边是「要改的没了」。
		return nil, ErrNotDeclared
	}
	row, err := h.repo.Get(ctx, request.GetId())
	if err != nil {
		logger.Ctx(ctx).Error("portforward.SetEnabled: 回读失败", zap.Int64("mappingId", request.GetId()), zap.Error(err))
		return nil, internalError(err)
	}
	if row == nil {
		return nil, ErrNotDeclared
	}
	if !row.Enabled {
		// 停用不只是「此后的访问一律被拒」:规格「断开与失败」明写正在进行的流立即
		// 关闭。少了这一步,用户按下开关之后那条还在跑的下载会一直跑到自己结束,
		// 界面上的开关与设备上的实际行为对不上。
		h.revoke(row.Port, revokedDisabled)
	}
	return &agentrewire.PortForwardSetEnabledResponse{Mapping: toWire(row)}, nil
}

// Delete 删除一条声明。deleted 如实说这一次有没有删掉一行,两种都不是错误——两个
// 客户端同时删同一条时,后到的那次不该看见一个错误。
func (h *Handlers) Delete(ctx context.Context, request *agentrewire.PortForwardDeleteRequest) (*agentrewire.PortForwardDeleteResponse, error) {
	// 先读出这一行,只为拿到它的端口:撤销面按端口认流(open 请求里带的就是端口,
	// 流表里没有映射 id 这一格),而行一删掉就再也问不出端口是多少。读失败不能当作
	// 「删不掉」——删除本身还是该发生,只是那一刻没人能被通知到。
	row, err := h.repo.Get(ctx, request.GetId())
	if err != nil {
		logger.Ctx(ctx).Error("portforward.Delete: 删除前回读失败", zap.Int64("mappingId", request.GetId()), zap.Error(err))
		return nil, internalError(err)
	}
	affected, err := h.repo.Delete(ctx, request.GetId())
	if err != nil {
		logger.Ctx(ctx).Error("portforward.Delete: 删除失败", zap.Int64("mappingId", request.GetId()), zap.Error(err))
		return nil, internalError(err)
	}
	if affected > 0 && row != nil {
		h.revoke(row.Port, revokedRemoved)
	}
	return &agentrewire.PortForwardDeleteResponse{Deleted: affected > 0}, nil
}

func validPort(port uint32) (int, error) {
	if port == 0 || port > 65535 {
		return 0, ErrInvalidPort
	}
	return int(port), nil
}

// isDuplicatePort 认出「端口已被声明」这一种写失败。
//
// gorm 只有开了 TranslateError 才给得出 ErrDuplicatedKey,而两个宿主今天都没开;
// 在那之前只剩驱动自己那句话可认(glebarez/sqlite 说 "UNIQUE constraint failed",
// 仓储用例里那条 MySQL 形状的 "Duplicate entry" 是同一件事的另一种说法)。两种都
// 认,是因为认错的代价不对称:漏认只会把一次可就地改正的输入错误说成 -32603。
func isDuplicatePort(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint failed") || strings.Contains(message, "duplicate entry")
}

func internalError(err error) error {
	return &rpcerror.Error{Code: rpcerror.CodeInternal, Message: err.Error()}
}

// toWire 把库里的一行折成线上那一格。
//
// 两处刻意的取舍:
//
//   - **时间戳换算收在这一处边界上。** 仓储写的是 UnixMilli,而线上那一格按协议
//     (wire.proto 里 PortForwardMapping 的注释)是 unix 秒。两者都已成事实,换算
//     只能发生在把行折成帧的这一步——放到每个消费方各自做,迟早有一处忘了,那一
//     处的列表会把时间显示成公元五万年。
//   - **没有所有者那一格,库上与线上都没有。** 全部映射对这台设备上的任何合法
//     客户端都是同一份,不按来源拆分,所以那一列会是恒定值(规格「数据」一节;
//     port_forward_entity 的包注释展开了同一条理由)。线上留一格更糟——用调用方
//     的身份去填,答的是「谁在问」而不是「谁声明的」。
func toWire(row *port_forward_entity.PortForward) *agentrewire.PortForwardMapping {
	return &agentrewire.PortForwardMapping{
		Id:         row.ID,
		Port:       uint32(row.Port),
		Name:       row.Name,
		Enabled:    row.Enabled,
		Createtime: unixSeconds(row.Createtime),
		Updatetime: unixSeconds(row.Updatetime),
	}
}

func unixSeconds(milli int64) int64 {
	if milli == 0 {
		return 0
	}
	return milli / 1000
}

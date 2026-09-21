// Package portforward 是端口转发在**被访问的那台设备**上的判定面:这台机器允许把
// 哪些目标转出去(声明族),以及一次 open 该不该落地(授权闸门)。
//
// 两种执行端共用这一份实现。规格「设备侧的目标限制」把 agentred 与桌面端并列写成
// 「两类设备都可能是被访问的一方」,而它们共用同一份实体与仓储(port_forward_entity
// / port_forward_repo,各自一个 SQLite 库);判定若各写一份,两台机器对同一条声明就
// 会给出两种答复。包住在 internal/daemon/ 下而被桌面端一并 import,与 remotefs /
// workspacefs / handlers 是同一条既有路子。
//
// **open 按映射 id 定位,只拨声明里存着的目标。** 规格「映射与目标」一节把目标从
// 裸端口扩成了 http(s)://host:port(Create/List 按 normalizeTarget 规范化),端口
// 于是不再是一条映射的身份;open 带来的只有 id,闸门按 id 取出那条声明,判过存在与
// 启用之后,拨的是**声明里存着的**目标 —— open 的协议里没有任何一格能改它(规格
// Hard invariant)。主机名在这台设备上解析,https 在这台设备上做 TLS(见 DialTarget)。
package portforward

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
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
	// ErrNotDeclared:这台设备上没有这条映射(从没建过,或已被删除)。
	ErrNotDeclared = &rpcerror.Error{Code: rpcerror.CodePortForwardNotDeclared, Message: "port forward: mapping not declared on this device"}
	// ErrDisabled:声明还在,但被停用了。与上一个分开,界面据此提示「把它打开」。
	ErrDisabled = &rpcerror.Error{Code: rpcerror.CodePortForwardDisabled, Message: "port forward: mapping disabled"}
	// ErrNoListener:映射过了判定,但目标连不上 —— 连接被拒,或网络上够不着。
	ErrNoListener = &rpcerror.Error{Code: rpcerror.CodePortForwardNoListener, Message: "port forward: target refused or unreachable"}
	// ErrNameResolution:目标的主机名在这台设备上解析不出来。
	ErrNameResolution = &rpcerror.Error{Code: rpcerror.CodePortForwardNameResolution, Message: "port forward: target host name did not resolve"}
	// ErrTLSVerification:https 目标的证书没通过校验,而这条映射没有勾「忽略证书错误」。
	ErrTLSVerification = &rpcerror.Error{Code: rpcerror.CodePortForwardTLSVerification, Message: "port forward: target certificate failed verification"}
	// ErrPortTaken:新增声明时这个目标(规范化后的协议、主机、端口)在这台设备上
	// 已经声明过。名字沿用「端口」是历史遗留(见 rpcerror.CodePortForwardPortTaken
	// 的注释)——对调用方来说仍是同一类可以就地改正的输入错误,不是写失败。
	ErrPortTaken = &rpcerror.Error{Code: rpcerror.CodePortForwardPortTaken, Message: "port forward: target already declared"}
	// ErrInvalidTarget:目标写法不合法——路径、查询串、用户信息、http(s) 以外的
	// 协议、端口越界、主机为空,六种理由报同一个码(见 normalizeTarget)。
	ErrInvalidTarget = &rpcerror.Error{Code: rpcerror.CodePortForwardInvalidTarget, Message: "port forward: invalid target"}
)

// Target 是一条声明里存着的目标:规范化之后的协议、主机、端口,外加这条映射自己的
// 证书设置。它只从声明里来(见 storedTarget),open 请求里没有位置放它。
type Target struct {
	Scheme string
	Host   string
	Port   int
	// Insecure 只对 https 有意义:为真时拨号不校验目标的证书。
	Insecure bool
}

// Dialer 拨到一个目标,https 目标连 TLS 握手一起做完。它拿到的 Target 恒是闸门从
// 声明里读出来的那一个。
type Dialer func(ctx context.Context, target Target) (net.Conn, error)

// Options 是宿主交出的那两件东西:自己那个库的仓储,以及拨号的办法。
type Options struct {
	// Repo 是这台设备上的声明集。两个宿主各自一个 SQLite 库,注入各自的实现。
	Repo port_forward_repo.PortForwardRepo
	// Dial 缺席时闸门照常判定,只是判完没有下一步——转发流那一半还没接上。
	Dial Dialer
}

// mappingRevoker 是「这条映射此刻起不再允许转发」的接收面。生产上唯一的实现是
// Streams(一条连接上开着的流),而闸门只认这个接口:声明族因此不需要知道这台机器上
// 有几条连接、每条连接上挂着什么。
type mappingRevoker interface {
	revokeMapping(mappingID int64, reason closeReason)
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
	revokers map[mappingRevoker]struct{}
}

func NewHandlers(options Options) *Handlers {
	return &Handlers{repo: options.Repo, dial: options.Dial, revokers: make(map[mappingRevoker]struct{})}
}

// watchRevocations 把一份流表挂到撤销面上,交回注销它的办法。
//
// 注销是**订阅方自己的事**(Streams.CloseAll 调它),所以闸门不必跟踪连接的生命周期:
// 它手上要么是一份还在用的流表,要么什么都没有,不会攒下一堆走掉的连接。
func (h *Handlers) watchRevocations(revoker mappingRevoker) func() {
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

// revoke 把「这条映射不再允许转发」派发给每一份订阅着的流表。
//
// 快照之后在锁外派发:声明族这一次 RPC 不该因为某条连接上的流表正忙而排队,而每个
// 订阅者要做的也只是关掉自己那几条流(收尾通知由流自己的 goroutine 发)。
func (h *Handlers) revoke(mappingID int64, reason closeReason) {
	h.revokeMu.Lock()
	revokers := make([]mappingRevoker, 0, len(h.revokers))
	for revoker := range h.revokers {
		revokers = append(revokers, revoker)
	}
	h.revokeMu.Unlock()
	for _, revoker := range revokers {
		revoker.revokeMapping(mappingID, reason)
	}
}

// DialDeclared 是 open 的**授权闸门**:先按 id 取出那条声明、判它在不在、有没有被
// 停用,通过了才拨号,拨的是声明里存着的目标。交回拨通的连接与它拨的那个目标(改写
// Host / Location 要按它来)。
//
// 顺序是这个方法唯一的实质内容——先拨再拒,错误码照样是对的,而目标上的服务已经
// 收到了一次未授权的连接。
func (h *Handlers) DialDeclared(ctx context.Context, mappingID int64) (net.Conn, Target, error) {
	mapping, err := h.repo.Get(ctx, mappingID)
	if err != nil {
		// 读不出来不能当作「没声明」:那会把一次故障说成一条用户能自己改正的输入
		// 错误,人会去重建一条本来就在的映射。
		logger.Ctx(ctx).Error("portforward.DialDeclared: 读声明集失败", zap.Int64("mappingId", mappingID), zap.Error(err))
		return nil, Target{}, &rpcerror.Error{Code: rpcerror.CodeInternal, Message: "port forward: cannot read declarations"}
	}
	if mapping == nil {
		return nil, Target{}, ErrNotDeclared
	}
	if !mapping.Enabled {
		return nil, Target{}, ErrDisabled
	}
	target, err := storedTarget(mapping)
	if err != nil {
		// 库里那一格是落库前规范化过的,读回来却解析不了只能是库坏了,不是用户输错。
		logger.Ctx(ctx).Error("portforward.DialDeclared: 声明里的目标解析不了",
			zap.Int64("mappingId", mappingID), zap.String("target", mapping.Target))
		return nil, Target{}, &rpcerror.Error{Code: rpcerror.CodeInternal, Message: "port forward: stored target is malformed"}
	}
	if h.dial == nil {
		return nil, Target{}, &rpcerror.Error{Code: rpcerror.CodeInternal, Message: "port forward: no dialer wired on this host"}
	}
	conn, err := h.dial(ctx, target)
	if err != nil {
		logger.Ctx(ctx).Warn("portforward.DialDeclared: 拨目标失败",
			zap.Int64("mappingId", mappingID), zap.String("target", mapping.Target), zap.Error(err))
		return nil, Target{}, withDeclaredTarget(dialFailure(err), mapping)
	}
	return conn, target, nil
}

// withDeclaredTarget 给「目标连不上」的回绝附上这条声明的 id 与规范化目标
// (Details,约定见 rpcerror.CodePortForwardNoListener):宿主手上只有映射 id,而它的
// 失败页要把话说到具体的目标上(规格「失败的呈现」)。交回的是一份副本 —— 包级的
// 三个错误值是共享的,不能就地写。
func withDeclaredTarget(refusal *rpcerror.Error, mapping *port_forward_entity.PortForward) error {
	details, err := proto.Marshal(&agentrewire.PortForwardMapping{Id: mapping.ID, Target: mapping.Target})
	if err != nil {
		return refusal
	}
	named := *refusal
	named.Details = details
	return &named
}

// dialFailure 把一次拨号失败归成「目标连不上」的三种原因之一(规格「失败归因」):
// 名字解析失败、TLS 校验失败,其余一律算连接被拒 / 够不着 —— 三件事用户要做的不同
// (去查主机名、去勾「忽略证书错误」、去把服务起起来)。按错误的类型判,不按文本。
//
// TLS 握手的其他失败(对方根本不说 TLS、协议版本谈不拢)不算「校验失败」:那不是
// 勾一个选项能解决的事,归到连不上。
func dialFailure(err error) *rpcerror.Error {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ErrNameResolution
	}
	var (
		verifyErr    *tls.CertificateVerificationError
		authorityErr x509.UnknownAuthorityError
		hostnameErr  x509.HostnameError
		invalidErr   x509.CertificateInvalidError
	)
	if errors.As(err, &verifyErr) || errors.As(err, &authorityErr) ||
		errors.As(err, &hostnameErr) || errors.As(err, &invalidErr) {
		return ErrTLSVerification
	}
	return ErrNoListener
}

// storedTarget 把库里那条规范字符串读回三元组。它走的是 Create 用的同一套规范化,
// 所以「落库时接受的写法」与「拨号时认的写法」只有一个出处。
func storedTarget(mapping *port_forward_entity.PortForward) (Target, error) {
	scheme, host, port, err := normalizeURLTarget(mapping.Target)
	if err != nil {
		return Target{}, err
	}
	return Target{Scheme: scheme, Host: host, Port: port, Insecure: mapping.Insecure}, nil
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

// Create 新增一条声明,交回**设备落库之后**的那一行。目标**不可编辑**:要换目标
// 就删掉重建(规格「映射与目标」一节),所以这里没有 Update。
func (h *Handlers) Create(ctx context.Context, request *agentrewire.PortForwardCreateRequest) (*agentrewire.PortForwardCreateResponse, error) {
	scheme, host, port, err := normalizeTarget(request.GetTarget())
	if err != nil {
		return nil, err
	}
	target := canonicalTarget(scheme, host, port)
	// 预检答的是常见那一次:用户填了一个已经在列表里的目标,这是他就地改得动的
	// 输入错误。它**不是**唯一性的真相源——那是库上的 UNIQUE 索引(仓储包注释
	// 明写这一点),下面 Create 的失败因此要落回同一个码,否则同一件事在两条路径
	// 上会被说成两句话。
	existing, err := h.repo.FindByTarget(ctx, target)
	if err != nil {
		logger.Ctx(ctx).Error("portforward.Create: 目标点查失败", zap.String("target", target), zap.Error(err))
		return nil, internalError(err)
	}
	if existing != nil {
		return nil, ErrPortTaken
	}
	row := &port_forward_entity.PortForward{
		Port:     port,
		Target:   target,
		Insecure: request.GetInsecure(),
		Name:     strings.TrimSpace(request.GetName()),
		// 刚填完的声明默认启用:用户新增它就是要用它,再让他多按一次开关没有道理。
		Enabled: true,
	}
	if err := h.repo.Create(ctx, row); err != nil {
		if isDuplicateTarget(err) {
			return nil, ErrPortTaken
		}
		logger.Ctx(ctx).Error("portforward.Create: 落库失败", zap.String("target", target), zap.Error(err))
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
		h.revoke(row.ID, revokedDisabled)
	}
	return &agentrewire.PortForwardSetEnabledResponse{Mapping: toWire(row)}, nil
}

// Delete 删除一条声明。deleted 如实说这一次有没有删掉一行,两种都不是错误——两个
// 客户端同时删同一条时,后到的那次不该看见一个错误。
func (h *Handlers) Delete(ctx context.Context, request *agentrewire.PortForwardDeleteRequest) (*agentrewire.PortForwardDeleteResponse, error) {
	affected, err := h.repo.Delete(ctx, request.GetId())
	if err != nil {
		logger.Ctx(ctx).Error("portforward.Delete: 删除失败", zap.Int64("mappingId", request.GetId()), zap.Error(err))
		return nil, internalError(err)
	}
	if affected > 0 {
		// 撤销面按映射 id 认流,删掉之后照样认得出是哪几条。
		h.revoke(request.GetId(), revokedRemoved)
	}
	return &agentrewire.PortForwardDeleteResponse{Deleted: affected > 0}, nil
}

// validPortNumber 是三种写法共用的端口范围判定:1..65535。
func validPortNumber(port int) bool {
	return port > 0 && port <= 65535
}

// defaultPortFor 是 http(s)://host[:port] 省略端口时按协议取的默认值(规格
// 「映射与目标」一节)。
func defaultPortFor(scheme string) int {
	if scheme == "https" {
		return 443
	}
	return 80
}

// normalizeTarget 把目标的三种写法(纯端口简写 / host:port / http(s)://host[:port])
// 规范化成 (scheme, host, port)。以下写法一律回 ErrInvalidTarget:路径、查询串、
// 用户信息、http(s) 以外的协议、端口越界、主机为空——六种理由报同一个码,规格
// 「映射与目标」一节没有把它们分开,调用方也不需要对六种输入错误分别猜。
func normalizeTarget(raw string) (scheme, host string, port int, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", 0, ErrInvalidTarget
	}
	// 纯端口是 "http://127.0.0.1:<端口>" 的简写。
	if p, convErr := strconv.Atoi(raw); convErr == nil {
		if !validPortNumber(p) {
			return "", "", 0, ErrInvalidTarget
		}
		return "http", "127.0.0.1", p, nil
	}
	if strings.Contains(raw, "://") {
		return normalizeURLTarget(raw)
	}
	return normalizeHostPortTarget(raw)
}

// normalizeURLTarget 处理 http(s)://host[:port] 那一支。
func normalizeURLTarget(raw string) (string, string, int, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", 0, ErrInvalidTarget
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", "", 0, ErrInvalidTarget
	}
	if u.User != nil {
		return "", "", 0, ErrInvalidTarget
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" {
		return "", "", 0, ErrInvalidTarget
	}
	if u.Path != "" && u.Path != "/" {
		return "", "", 0, ErrInvalidTarget
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", "", 0, ErrInvalidTarget
	}
	portStr := u.Port()
	if portStr == "" {
		return scheme, host, defaultPortFor(scheme), nil
	}
	p, convErr := strconv.Atoi(portStr)
	if convErr != nil || !validPortNumber(p) {
		return "", "", 0, ErrInvalidTarget
	}
	return scheme, host, p, nil
}

// normalizeHostPortTarget 处理没有协议前缀的 host:port:它就是 http://host:port 的
// 简写,所以按那条写法走同一套拒绝规则 —— 少写了协议不能让用户信息、路径、查询串
// 混进主机那一格。端口在这种写法里是必填的。
func normalizeHostPortTarget(raw string) (string, string, int, error) {
	if _, _, err := net.SplitHostPort(raw); err != nil {
		return "", "", 0, ErrInvalidTarget
	}
	return normalizeURLTarget("http://" + raw)
}

// canonicalTarget 把规范化的三元组拼回线上/库里那条规范字符串——总是带着端口,
// 即便端口等于协议的默认值(规格「映射与目标」一节)。
// IPv6 字面量带回方括号,这条串才能被 storedTarget 原样读回来。
func canonicalTarget(scheme, host string, port int) string {
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port))
}

// isDuplicateTarget 认出「目标已被声明」这一种写失败。
//
// gorm 只有开了 TranslateError 才给得出 ErrDuplicatedKey,而两个宿主今天都没开;
// 在那之前只剩驱动自己那句话可认(glebarez/sqlite 说 "UNIQUE constraint failed",
// 仓储用例里那条 MySQL 形状的 "Duplicate entry" 是同一件事的另一种说法)。两种都
// 认,是因为认错的代价不对称:漏认只会把一次可就地改正的输入错误说成 -32603。
func isDuplicateTarget(err error) bool {
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
		Target:     row.Target,
		Insecure:   row.Insecure,
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

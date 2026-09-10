package portforwardhost

import (
	"io"
	"net/http"
)

// 这一层遇到自己的失败时,交给宿主的是**是哪一件事**,不是一句写死的话。
//
// 为什么必须是种类而不是「状态码 + 一句中文」:四种失败共用 502(端口上没有服务、
// 设备够不着、上游把请求断了、转发没完成),宿主拿状态码分不开它们,只能去猜 —— 而
// 猜出来的结果会把**被转发应用自己**的 502 也当成这一层的失败改写掉(控制台那一轮的
// 运行期实证)。种类是这一层已经判出来的结论,把它交出去,谁都不必再猜第二遍。
//
// 交出去的**不含**下面那几句默认文案。给了它,宿主就会有人去匹配它,于是又长出一条
// 「上游改一个字就静默失灵」的耦合。

// FailureKind 是这一层自己能产生的七种失败。
//
// 零值刻意不是其中任何一种:一个忘了赋值的 Failure 不该恰好等于「端口没声明」。
type FailureKind int

// noFailure 是那个零值,包内用它说「这次收场**不是**一种失败」——今天只有一件事落在
// 这上面:浏览器自己先走了,没有人还在等一个答复。它不导出:宿主永远不会收到它,
// FailureRenderer 只在真的失败上被调用。
const noFailure FailureKind = 0

const (
	// FailureNotDeclared 设备上没有这个端口的映射。
	FailureNotDeclared FailureKind = iota + 1
	// FailureDisabled 映射在,但被停用了。
	FailureDisabled
	// FailureNoListener 设备连着、映射启用,但那个端口上没有服务在监听。
	FailureNoListener
	// FailureDeviceUnreachable 够不着设备(承载连接没了、握手被别的错误挡下、
	// 或者设备说这条流的宿主/连接已经不在了)。
	FailureDeviceUnreachable
	// FailureUpstreamGone 设备上那个服务把这次请求断开了。
	FailureUpstreamGone
	// FailureForwardIncomplete 这条流收场了,但说不出是上面哪一种。
	FailureForwardIncomplete
	// FailureUpgradeUnavailable 宿主给的 ResponseWriter 交不出底层连接,101 升级做不了。
	FailureUpgradeUnavailable
)

// Failure 是交给宿主的那份归因。
//
// Status 是这一层为该种类选定的默认状态码 —— 宿主通常照用,但它有权改(比如把
// 「够不着」也答成 503)。Port 让宿主能把话说到具体的端口上。
type Failure struct {
	Kind   FailureKind
	Port   uint32
	Status int
}

// FailureRenderer 由宿主提供,负责把一次失败写成响应。
//
// 它**只**在这一层自己的失败上被调用;被转发应用自己的响应(含它自己答的 4xx/5xx)
// 一个字节都不经过这里。这条是本层对宿主的核心承诺。
type FailureRenderer func(w http.ResponseWriter, r *http.Request, f Failure)

// Option 调整一个 Proxy 的可选行为。
type Option func(*Proxy)

// WithFailureRenderer 装上宿主自己的失败渲染。不装就用包内的默认纯文本。
func WithFailureRenderer(render FailureRenderer) Option {
	return func(p *Proxy) { p.renderFailure = render }
}

// defaultStatus 是每一种失败的默认状态码。
func (k FailureKind) defaultStatus() int {
	switch k {
	case FailureNotDeclared:
		return http.StatusNotFound
	case FailureDisabled:
		return http.StatusForbidden
	case FailureUpgradeUnavailable:
		return http.StatusInternalServerError
	case FailureNoListener, FailureDeviceUnreachable, FailureUpstreamGone, FailureForwardIncomplete:
		return http.StatusBadGateway
	}
	return http.StatusBadGateway
}

// 默认文案。设备回来的英文串一律不贴给用户:按码/按 token 判定出是哪一件事,再用
// 自己的话说清下一步。
//
// 它们是**默认值**,不是这一层对用户的承诺:要自己说话的宿主装 WithFailureRenderer。
// 措辞是桌面端口径(「回到 agentre 里」),因为不装钩子的宿主今天只有桌面端一个。
const (
	msgNotDeclared        = "这台设备上已经没有这个端口的映射了。回到 agentre 里重新打开它。"
	msgDisabled           = "这条端口映射已经停用。到 agentre 里把它启用之后再打开。"
	msgNoListener         = "设备上这个端口没有服务在监听。到那台机器上把服务起起来,再刷新这一页。"
	msgDeviceUnreachable  = "这台设备此刻够不着。等它回来之后刷新这一页。"
	msgUpstreamGone       = "设备上这个端口的服务把这次请求断开了。刷新这一页重试。"
	msgForwardIncomplete  = "这次转发没能完成。刷新这一页重试。"
	msgUpgradeUnavailable = "这条转发没能把连接交出去,升级到 WebSocket 失败了。"
)

func (k FailureKind) defaultMessage() string {
	switch k {
	case FailureNotDeclared:
		return msgNotDeclared
	case FailureDisabled:
		return msgDisabled
	case FailureNoListener:
		return msgNoListener
	case FailureDeviceUnreachable:
		return msgDeviceUnreachable
	case FailureUpstreamGone:
		return msgUpstreamGone
	case FailureForwardIncomplete:
		return msgForwardIncomplete
	case FailureUpgradeUnavailable:
		return msgUpgradeUnavailable
	}
	return msgForwardIncomplete
}

// fail 是这一层所有自有失败的唯一出口。
func (p *Proxy) fail(w http.ResponseWriter, r *http.Request, kind FailureKind) {
	f := Failure{Kind: kind, Port: p.port, Status: kind.defaultStatus()}
	if p.renderFailure != nil {
		p.renderFailure(w, r, f)
		return
	}
	writeDefaultFailure(w, f.Status, kind.defaultMessage())
}

func writeDefaultFailure(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, message+"\n")
}

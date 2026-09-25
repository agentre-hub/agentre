package ctl_svc

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// CallerClass 是执行者认定的调用方类别（spec「Routing and approval」），决定写操作走哪条
// 审批路径。
type CallerClass int

const (
	// CallerHuman：握手 token，agrctl 上报 stdin 是 TTY —— 人在终端里，直接执行。
	CallerHuman CallerClass = iota + 1
	// CallerExternal：握手 token，stdin 不是 TTY（外部 AI / 脚本）—— 桌面端全局弹窗审批。
	CallerExternal
	// CallerSession：会话级 token（Agentre 会话里的 agent）—— 在该会话里审批。
	CallerSession
)

// Caller 是一次请求经鉴权后的调用方。
type Caller struct {
	Class CallerClass
	// Session 是会话级 token 绑定的 (agent, session)；只在 Class == CallerSession 时有值。
	Session agenttool.Ref
}

// SessionTokenVerifier 校验会话级 token 并解出绑定的 (agent, session)。
// *agenttool.TokenSigner 直接满足；签发与校验必须是同一个实例。
type SessionTokenVerifier interface {
	Lookup(tok string) (agenttool.Ref, bool)
}

// credential 是 bearer token 本身证明的身份：握手 token（全权）或会话级 token。
type credential struct {
	session bool
	ref     agenttool.Ref
}

// caller 结合 agrctl 上报的分类得出调用方。上报只是护栏（同一系统用户下可以伪造），
// 所以它只能在握手 token 内部区分人工与外部，不能把会话 token 抬成别的类别；
// 握手 token 却自称会话或没上报时，按要审批的外部调用处理。
func (c credential) caller(reported agentrewire.CtlCaller) Caller {
	switch {
	case c.session:
		return Caller{Class: CallerSession, Session: c.ref}
	case reported == agentrewire.CtlCaller_CTL_CALLER_HUMAN:
		return Caller{Class: CallerHuman}
	default:
		return Caller{Class: CallerExternal}
	}
}

// authenticate 常量时间比对握手 token，其次按会话级 token 验签；都不是则 !ok。
// 握手 token 未装配(空)时不接受任何握手请求。
func (h *ctlHandler) authenticate(r *http.Request) (credential, bool) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tok == "" {
		return credential{}, false
	}
	if h.token != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(h.token)) == 1 {
		return credential{}, true
	}
	if h.sessions != nil {
		if ref, ok := h.sessions.Lookup(tok); ok {
			return credential{session: true, ref: ref}, true
		}
	}
	return credential{}, false
}

// sessionRoutes 是会话级 token 能调用的端点（白名单：新端点默认不对会话开放）。
// stop、answer-permission 不在其中——AI 不能停会话，也不能批准自己（Hard invariant 2）。
var sessionRoutes = map[string]bool{
	"agents":    true,
	"projects":  true,
	"resources": true,
	"sessions":  true,
	"send":      true,
	"stream":    true,
}

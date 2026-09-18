package backendcred

import (
	"context"
	"errors"
	"strings"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/hermes/hermesauth"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/hermes/hermesgateway"
	"github.com/agentre-hub/agentre/internal/pkg/openclawgateway"
)

// 结果码是两种执行端对同一次失败说的**同一个串**:桌面端自己点测试连接、agentred
// 被问到时、桌面端被控制台当作绑定设备问到时,读到的必须是同一个码 —— 前端只有一张
// 本地化表,一边多一个字就成了「同一件事在两台设备上说成两句话」。
//
// 它因此住在这个无副作用的叶子包里,而不是各自抄一份:agentred 不能 import 桌面端的
// 服务包(会把它的 init 连同两种 runtime 拖进 daemon),但两边都已经 import 本包。
// 手抄过的那一份已经漏过一次 —— gateway 拨不上时桌面端把 dial 原话当正文递了出去,
// 而 agentred 那一侧早就回 HERMES_UNREACHABLE。

// Hermes 认证 / 网关失败的结果码。
const (
	HermesCodeLoginRequired       = "HERMES_LOGIN_REQUIRED"
	HermesCodeLoginExpired        = "HERMES_LOGIN_EXPIRED"
	HermesCodeInvalidCredentials  = "HERMES_INVALID_CREDENTIALS"
	HermesCodeRateLimited         = "HERMES_RATE_LIMITED"
	HermesCodeProviderUnsupported = "HERMES_PROVIDER_UNSUPPORTED"
	HermesCodeProviderUnavailable = "HERMES_PROVIDER_UNAVAILABLE"
	HermesCodeUnreachable         = "HERMES_UNREACHABLE"
)

// HermesResultCode 把 Hermes 的认证与网关哨兵翻成结果码;"" 表示这次失败说不出
// 结构化的原因,调用方自己决定兜底。
//
// 「连不上」有两个来源:认证 HTTP 打不通与 gateway 拨不上。对用户是同一件事,因此
// 落在同一个码上。
func HermesResultCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, hermesauth.ErrLoginRequired):
		return HermesCodeLoginRequired
	case errors.Is(err, hermesauth.ErrLoginExpired):
		return HermesCodeLoginExpired
	case errors.Is(err, hermesauth.ErrInvalidCredentials):
		return HermesCodeInvalidCredentials
	case errors.Is(err, hermesauth.ErrAuthRateLimited):
		return HermesCodeRateLimited
	case errors.Is(err, hermesauth.ErrPasswordLoginUnsupported):
		return HermesCodeProviderUnsupported
	case errors.Is(err, hermesauth.ErrAuthProviderUnavailable):
		return HermesCodeProviderUnavailable
	case errors.Is(err, hermesauth.ErrAuthUnreachable), errors.Is(err, hermesgateway.ErrGatewayUnreachable):
		return HermesCodeUnreachable
	default:
		return ""
	}
}

// openClawGatewayAuthCodes 是网关直接给出的鉴权类 code:token 被拒的三种说法。
var openClawGatewayAuthCodes = map[string]struct{}{
	"AUTH_FAILED": {}, "UNAUTHORIZED": {}, "FORBIDDEN": {},
}

// OpenClawResultCode 把一次 Gateway 探测的失败翻成结果码。与 Hermes 那一侧不同,
// 这里没有「说不出原因」的出口:认不出来就是一次连接失败。
func OpenClawResultCode(err error) string {
	var rpcErr *openclawgateway.RPCError
	switch {
	case errors.As(err, &rpcErr):
		return openClawRPCCode(rpcErr)
	case errors.Is(err, openclawgateway.ErrRequiredScopeMissing):
		return "OPENCLAW_SCOPE_MISSING"
	case errors.Is(err, openclawgateway.ErrProtocolMismatch):
		return "OPENCLAW_PROTOCOL_MISMATCH"
	case errors.Is(err, openclawgateway.ErrSelectedAgentNotFound):
		return "OPENCLAW_AGENT_NOT_FOUND"
	case errors.Is(err, openclawgateway.ErrSelectedModelNotFound):
		return "OPENCLAW_MODEL_NOT_FOUND"
	case errors.Is(err, openclawgateway.ErrRequiredMethodMissing):
		return "OPENCLAW_METHOD_MISSING"
	case errors.Is(err, openclawgateway.ErrRequiredEventMissing):
		return "OPENCLAW_EVENT_MISSING"
	case errors.Is(err, context.Canceled):
		return "OPENCLAW_PROBE_CANCELED"
	case errors.Is(err, context.DeadlineExceeded):
		return "OPENCLAW_PROBE_TIMEOUT"
	default:
		return "OPENCLAW_CONNECTION_FAILED"
	}
}

// openClawRPCCode 归一化网关自己报的 code。网关对「没配对」与「鉴权不过」有好几种
// 说法(code / reason / message 三格),归到两个码上;其余 code 原样透出。
func openClawRPCCode(rpcErr *openclawgateway.RPCError) string {
	code := strings.ToUpper(strings.TrimSpace(rpcErr.Code))
	reason := strings.ToLower(strings.TrimSpace(rpcErr.Reason))
	message := strings.ToLower(rpcErr.Message)
	if code == "NOT_PAIRED" || reason == "not_paired" {
		return "OPENCLAW_NOT_PAIRED"
	}
	if _, ok := openClawGatewayAuthCodes[code]; ok {
		return "AUTH_FAILED"
	}
	if reason == "unauthorized" || strings.HasPrefix(message, "unauthorized") {
		return "AUTH_FAILED"
	}
	if code == "" {
		return "OPENCLAW_CONNECTION_FAILED"
	}
	return code
}

// OpenClawURLCode 把被拒绝的 Gateway URL 翻成结果码。
func OpenClawURLCode(err error) string {
	switch {
	case errors.Is(err, agent_backend_entity.ErrOpenClawGatewayURLRequired):
		return "OPENCLAW_URL_REQUIRED"
	case errors.Is(err, agent_backend_entity.ErrOpenClawGatewayURLScheme):
		return "OPENCLAW_URL_SCHEME"
	case errors.Is(err, agent_backend_entity.ErrOpenClawGatewayURLHost):
		return "OPENCLAW_URL_HOST"
	case errors.Is(err, agent_backend_entity.ErrOpenClawGatewayURLCredentials):
		return "OPENCLAW_URL_CREDENTIALS"
	case errors.Is(err, agent_backend_entity.ErrOpenClawGatewayURLPlaintextRemote):
		return "OPENCLAW_URL_PLAINTEXT_REMOTE"
	default:
		return "OPENCLAW_URL_INVALID"
	}
}

// RedactSecret 把凭据从即将离开本机的文本里抹掉。产出方(Gateway 客户端)自己已经
// 抹过一遍;这里再抹一次,让「凭据不出现在任何应答里」不依赖每一个产出方都自觉。
func RedactSecret(text, secret string) string {
	if secret == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, "[redacted]")
}

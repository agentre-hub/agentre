package agentruntime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

// SessionTokenRouter 是会话常驻 gateway token 需要的最小网关能力（消费方 ISP）：
// 按本轮的 ModelTarget 签发、在**不换 token 字符串**的前提下改既有 token 的路由目标、
// 撤销。桌面的 httpgateway.TokenRouter 与 daemon 的 handlers.GatewayPort 都自然满足。
type SessionTokenRouter interface {
	IssueTokenFor(
		ctx context.Context, backend *agent_backend_entity.AgentBackend, providerKey, modelKey string, ttl time.Duration,
	) (token string, err error)
	SetTokenTarget(token, providerKey, modelKey string) (previous string, ok bool)
	RevokeToken(token string)
}

// sessionTokenTTL 是会话常驻 token 的 TTL：0 = 永久。
//
// 这条是硬不变量,不是可调参数:token 在首轮 spawn 时烤进 CLI 子进程 env
// （AGENTRE_GATEWAY_TOKEN）,子进程跨轮复用时 env 不重建。旧实现每轮重签 15min TTL
// 的新 token、却只有首轮那个被烤进去,导致长会话(>15min)子进程手里的 token 过期、
// PostToolUse hook 撞 401、SteerInbox 整轮 drain 不到、steer 被压到轮末 DrainPending。
const sessionTokenTTL time.Duration = 0

// SessionTokenCache 是会话常驻 gateway token 的唯一缓存实现，桌面（chat_svc）与 daemon
// （handlers）共用：
//
//   - 签发一次：每个 session 只签一个永久(ttl=0)token，跨轮返回同一个字符串；
//   - 改道：会话中途换供应商/换模型时改既有 token 的路由目标而**不重签** —— 重签会让
//     手里烤着旧 token 的在跑子进程立刻 401；
//   - 撤销：会话结束时撤销并清出缓存，之后该 id 若复活会重签一个新的。
//
// 缓存本身不判断网关是否可用（桌面看 Status().State，daemon 看 URL()），也不返回网关
// URL —— 那两件是宿主侧的事，各自保留。router 用取值函数传入是因为桌面的网关是在
// chatSvc 构造之后由 bootstrap 注入的（RegisterGateway），取值时才有实例。
type SessionTokenCache struct {
	// logPrefix 是日志消息前缀（observability 约定的 package.Method: 形态），由宿主提供，
	// 使排查时一眼看出是桌面还是 daemon 的那条会话 token。
	logPrefix string
	router    func() SessionTokenRouter
	// tokens 是 sessionID(int64) → token(string)。
	tokens sync.Map
}

// NewSessionTokenCache 构造缓存。logPrefix 形如 "chat_svc.signChatTokenFor"；router 返回
// 当前网关实例，网关缺席时返回 nil（此时缓存不签不撤，调用方按「不签」处理）。
func NewSessionTokenCache(logPrefix string, router func() SessionTokenRouter) *SessionTokenCache {
	return &SessionTokenCache{logPrefix: logPrefix, router: router}
}

// EnsureToken 返回该 session 的常驻 token：已有就复用并把路由目标对齐到本轮的
// (providerKey, modelKey)，没有就签一个永久 token 并缓存。
//
// sessionID <= 0 表示不入缓存的一次性签发（无会话上下文）。网关缺席时返回 ("", nil)。
// 签发失败原样透出错误：daemon 据此阻断本轮，桌面按「不签」处理。
func (c *SessionTokenCache) EnsureToken(
	ctx context.Context, sessionID int64, backend *agent_backend_entity.AgentBackend, providerKey, modelKey string,
) (string, error) {
	r := c.currentRouter()
	if r == nil {
		return "", nil
	}
	if sessionID > 0 {
		if v, ok := c.tokens.Load(sessionID); ok {
			tok, _ := v.(string)
			c.route(ctx, r, sessionID, tok, providerKey, modelKey)
			return tok, nil
		}
	}
	tok, err := r.IssueTokenFor(ctx, backend, providerKey, modelKey, sessionTokenTTL)
	if err != nil {
		return "", fmt.Errorf("gateway token: %w", err)
	}
	if sessionID > 0 {
		// 并发首轮兜底:别的 goroutine 抢先签好就用它的,撤掉自己这条避免泄漏。
		if actual, loaded := c.tokens.LoadOrStore(sessionID, tok); loaded {
			r.RevokeToken(tok)
			existing, _ := actual.(string)
			return existing, nil
		}
	}
	return tok, nil
}

// Revoke 撤销并清掉某 session 的常驻 token（会话删除、常驻子进程关闭后调用），让 token
// 寿命跟随子进程。重复调用与未签过的 session 都是 no-op。
func (c *SessionTokenCache) Revoke(sessionID int64) {
	if sessionID <= 0 {
		return
	}
	v, ok := c.tokens.LoadAndDelete(sessionID)
	if !ok {
		return
	}
	if r := c.currentRouter(); r != nil {
		tok, _ := v.(string)
		r.RevokeToken(tok)
	}
}

// route 把会话常驻 token 的路由目标对齐到本轮的 ModelTarget。token 字符串不变,已烤进
// 子进程 env 的那份继续可用;真的换了才记一条日志。找不到 entry = gateway 重启过
// （token 表只在内存里）,子进程手里那个也已失效,记 warn 供排查 —— 不在这里重签,重签
// 也救不回已 spawn 的子进程。
func (c *SessionTokenCache) route(
	ctx context.Context, r SessionTokenRouter, sessionID int64, token, providerKey, modelKey string,
) {
	previous, ok := r.SetTokenTarget(token, providerKey, modelKey)
	if !ok {
		logger.Ctx(ctx).Warn(c.logPrefix+": session token missing from gateway",
			zap.Int64("sessionId", sessionID),
			zap.String("providerKey", providerKey))
		return
	}
	if previous != providerKey {
		logger.Ctx(ctx).Info(c.logPrefix+": gateway token rerouted to new provider",
			zap.Int64("sessionId", sessionID),
			zap.String("previousProviderKey", previous),
			zap.String("providerKey", providerKey))
	}
}

func (c *SessionTokenCache) currentRouter() SessionTokenRouter {
	if c == nil || c.router == nil {
		return nil
	}
	return c.router()
}

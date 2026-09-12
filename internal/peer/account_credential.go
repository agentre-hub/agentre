package peer

import (
	"context"
	"strconv"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/daemon/auth"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
)

// accountCredentialVerifier 在桌面端这一侧兑现 H1:入站对端出示的账号凭据向
// server 在线核验(auth.Introspector,与 agentred 的 HandleAccount 共用同一份
// 缓存与错误分类实现),核验通过的账号还必须等于这台桌面端自己登录的账号。
//
// 它**不**自己维护公钥缓存或验签:那一整套本地面已经随决策(H6)删除,吊销从此
// 对两种执行端同样立即生效,不再受限于桌面端从未有过的吊销轮询。
type accountCredentialVerifier struct {
	// account 交出本机登录的账号 id;未登录时交出空串、不返回错误(接收方未就绪,
	// 与「凭据核验失败」是两件事,调用方据此统一按 H4 拒绝,不必先摸一次网络)。
	account    func(ctx context.Context) (accountID string, err error)
	introspect func(ctx context.Context, credential string) (auth.Introspection, error)
}

func newAccountCredentialVerifier(
	account func(context.Context) (string, error),
	introspect func(context.Context, string) (auth.Introspection, error),
) *accountCredentialVerifier {
	return &accountCredentialVerifier{account: account, introspect: introspect}
}

// Verify 交出 server 核验过的账号身份与对端指纹。任何一步不成立都返回错误,
// 调用方一律以「凭据被拒」的同一错误拒绝 —— 绝不退回去采信请求体。
func (v *accountCredentialVerifier) Verify(ctx context.Context, credential string) (auth.Introspection, error) {
	accountID, err := v.account(ctx)
	if err != nil {
		return auth.Introspection{}, err
	}
	if accountID == "" {
		// H4:这台桌面端自己都不知道登录了哪个账号,没有任何东西可比,握手按
		// 「接收方未就绪」拒绝 —— 与今天「缺少验证材料」同一形态,不打一次网络。
		return auth.Introspection{}, auth.ErrReceiverNotReady
	}
	verified, err := v.introspect(ctx, credential)
	if err != nil {
		return auth.Introspection{}, err
	}
	// 同一账号才是对端。跨账号的凭据即使 server 核验通过也不是这台桌面端的对端 ——
	// 与今天「凭据被拒」同一错误,不额外泄露「它属于谁」。
	if verified.AccountID != accountID {
		logger.Ctx(ctx).Info("peer.accountCredentialVerifier: rejected a credential that belongs to another account",
			zap.String("accountId", accountID), zap.String("credentialAccountId", verified.AccountID))
		return auth.Introspection{}, auth.ErrCredentialInvalid
	}
	return verified, nil
}

// desktopAccountID 交出这台桌面端当前登录的账号 id。没有单例、状态读不到或未
// 登录时交出空串:调用方(accountCredentialVerifier)据此统一走 H4,不当作错误。
func desktopAccountID(ctx context.Context) (string, error) {
	svc := server_svc.Server()
	if svc == nil {
		return "", nil
	}
	state, err := svc.GetState(ctx)
	if err != nil {
		return "", err
	}
	if state == nil || !state.IsLoggedIn() {
		return "", nil
	}
	return strconv.FormatInt(state.ServerUserID, 10), nil
}

// introspectInboundCredential 是生产装配用的核验入口:走 server_svc 的
// IntrospectCredential,后者持有桌面端自己的 server 地址、access token 与
// 刷新路径,核验本体(在线核验、60 秒成功缓存、错误分类)全在 auth.Introspector。
//
// 未装配单例(引导尚未完成)时按 H4 交回「接收方未就绪」,不当作「凭据不合法」。
func introspectInboundCredential(ctx context.Context, credential string) (auth.Introspection, error) {
	svc := server_svc.Server()
	if svc == nil {
		return auth.Introspection{}, auth.ErrReceiverNotReady
	}
	return svc.IntrospectCredential(ctx, credential)
}

// verifyInboundAccountCredential 是生产装配用的验证入口。它是变量而非函数,因为
// 入站握手的验证结果是**集成测试唯一无法从外部构造的输入**(凭据由 server 签),
// 测试经 SwapAccountCredentialVerifierForTest 换掉它。
var verifyInboundAccountCredential = newAccountCredentialVerifier(desktopAccountID, introspectInboundCredential).Verify

// SwapAccountCredentialVerifierForTest 换掉入站账号凭据验证器并交回还原函数。
// 与 agentruntime.SwapRuntimeForTest 同一模式:只给测试用。
func SwapAccountCredentialVerifierForTest(verify func(context.Context, string) (auth.Introspection, error)) func() {
	previous := verifyInboundAccountCredential
	verifyInboundAccountCredential = verify
	return func() { verifyInboundAccountCredential = previous }
}

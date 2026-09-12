package server_svc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
)

// 退避重试的节奏：首次 5 秒，逐次翻倍，封顶 5 分钟。服务端一次重启大多在首几档
// 内就恢复；封顶保证长时间停机时桌面端也只是每 5 分钟敲一次门。
const (
	refreshRetryInitial = 5 * time.Second
	refreshRetryMax     = 5 * time.Minute
)

type refreshResp struct {
	AccessToken      string `json:"access_token"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
}

// refreshEnvelope 比通用 envelope 多一个 error 字段：server 的
// AttachOAuthErrorFields 中间件在拒绝凭据时会把 RFC 6749 的 error 码
// （invalid_grant）拼进 body，这是「凭据真失效」与「服务端出问题」唯一可靠的分界。
type refreshEnvelope struct {
	Code  int         `json:"code"`
	Msg   string      `json:"msg"`
	Data  refreshResp `json:"data"`
	Error string      `json:"error"`
}

// refreshFlight 是一条正在跑的刷新。并发调用方共享它的结果：成功就各自拿新的
// access token 重试自己的请求，失败就共享同一个失败。
type refreshFlight struct {
	done chan struct{}
	err  error
	// settled 标记这条刷新的善后（凭据真被拒时清登录）已经做过了，受 s.refreshMu 保护。
	settled bool
}

// refresh exchanges the stored refresh_token for a new access token.
//
// 失败分两类，调用方靠 IsCredentialRejected 区分：服务端明确拒绝（ErrRefreshRejected，
// 凭据真没了）与够不着 / 5xx / keychain 读不到（原样返回，登录态一个字都不许动）。
// 它只换票，不做善后 —— 清不清登录由调用方决定。
func (s *service) refresh(ctx context.Context) error {
	return s.refreshSince(ctx, s.refreshTicket(), nil)
}

// refreshClearingDeadLogin 是带善后的刷新：凭据被服务端证实作废时清掉本地登录。
// 善后按「刷新」而不是按「调用方」记一次 —— 共享同一个失败的其余并发调用方只拿到
// 错误，不重复清、也不重复发 logged_out。
func (s *service) refreshClearingDeadLogin(ctx context.Context, ticket uint64) error {
	return s.refreshSince(ctx, ticket, s.clearDeadLogin)
}

// clearDeadLogin 是唯一一处「凭据被证实作废 ⇒ 拆掉本地登录」的落点。
func (s *service) clearDeadLogin(ctx context.Context, rerr error) {
	logger.Ctx(ctx).Warn("server_svc.refresh: server rejected the refresh token we hold, clearing login",
		zap.Error(rerr))
	_ = s.clearLogin(ctx)
}

// refreshTicket 取一张「当前刷新代次」的票。调用方在发业务请求**之前**取票，撞 401
// 之后把票交回 refreshSince：代次已经变了就说明别人在这中间把凭据续上了。
func (s *service) refreshTicket() uint64 {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	return s.refreshGen
}

// refreshSince 保证同一时刻只有一条刷新在跑 —— refresh_token 是一次性的，服务端换出
// 新的就把旧的撤销，并发放两条刷新出去必然有一条被判重放（invalid_grant）。
//
//   - 票过期（代次变了）：别人已经刷成功，直接返回 nil，调用方拿新 token 重试即可；
//   - 已有一条在跑：等它，共享同一个结果（ctx 结束则提前返回，不影响那条刷新）；
//   - 否则：自己跑这一条，成功后代次 +1 放行其余等着的调用方。
//
// onRejected（可为 nil）是凭据被证实作废时的善后，按刷新记一次：发起方在放行等待者
// **之前**执行，晚一步才进来的调用方于是读不到凭据、只会得到 ErrRefreshFailed。
func (s *service) refreshSince(ctx context.Context, ticket uint64, onRejected func(context.Context, error)) error {
	s.refreshMu.Lock()
	if s.refreshGen != ticket {
		s.refreshMu.Unlock()
		return nil
	}
	if f := s.refreshInFlight; f != nil {
		s.refreshMu.Unlock()
		select {
		case <-f.done:
			s.settleRejection(ctx, f, onRejected)
			return f.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f := &refreshFlight{done: make(chan struct{})}
	s.refreshInFlight = f
	s.refreshMu.Unlock()

	f.err = s.exchangeRefreshToken(ctx)
	s.settleRejection(ctx, f, onRejected)

	s.refreshMu.Lock()
	s.refreshInFlight = nil
	if f.err == nil {
		s.refreshGen++
	}
	s.refreshMu.Unlock()
	close(f.done)
	return f.err
}

// settleRejection 跑一条刷新的善后，且只跑一次：发起方与等待者谁先到都行，第二个
// 到的什么也不做。发起方带 nil onRejected（裸 Refresh）时，第一个带善后的等待者补上。
func (s *service) settleRejection(ctx context.Context, f *refreshFlight, onRejected func(context.Context, error)) {
	if onRejected == nil || !IsCredentialRejected(f.err) {
		return
	}
	s.refreshMu.Lock()
	first := !f.settled
	f.settled = true
	s.refreshMu.Unlock()
	if first {
		onRejected(ctx, f.err)
	}
}

// exchangeRefreshToken 拿 keychain 里存着的那枚 refresh_token 换一对新的。
// Server-side refresh-token rotation: the response carries a fresh refresh_token
// and we overwrite the keychain entry. The desktop only keeps the latest one.
//
// 一次性令牌意味着「被拒」有两种截然不同的成因，判据必须落在事实上——我们提交的那枚
// **是否仍是**当前存着的那一枚：
//   - 仍是：这份登录真的死了（被吊销 / 设备已删 / 过期），返回 ErrRefreshRejected；
//   - 已经不是：别人（另一个进程，或一条我们没能单飞掉的并发路径）刚刚刷成功，我们
//     提交的只是它消费掉的旧票根。拿当前那枚重来一次，绝不能判这份登录死了。
func (s *service) exchangeRefreshToken(ctx context.Context) error {
	var rejection error
	for attempt := 0; attempt < 2; attempt++ {
		old, err := keychain.Default().Get(keychainAccountName)
		if err != nil {
			// keychain 上锁或后端暂时不可用也走这里 —— 凭据可能好端端躺着，不能据此判死。
			return fmt.Errorf("%w: keychain: %w", ErrRefreshFailed, err)
		}

		var env refreshEnvelope
		status, err := s.getClient().do(ctx, http.MethodPost, "/v1/oauth/token/refresh",
			map[string]string{"refresh_token": old}, &env)
		if err != nil {
			if !credentialRejection(status, env.Error) {
				return err
			}
			rejection = fmt.Errorf("%w: %s", ErrRefreshRejected, env.Error)
			cur, cerr := keychain.Default().Get(keychainAccountName)
			if cerr == nil && cur == "" {
				cerr = errors.New("stored refresh token is empty")
			}
			if cerr != nil {
				// 事实读不出来（钥匙串上锁 / 后端暂时不可用）就不许判死 —— 与函数
				// 开头那次读不到 keychain 同一口径：凭据可能好端端躺着。
				logger.Ctx(ctx).Warn("server_svc.refresh: rejected, but the stored token could not be read back; keeping the login",
					zap.Error(cerr))
				return fmt.Errorf("%w: cannot confirm the rejected token is still the stored one: %v",
					ErrRefreshFailed, cerr)
			}
			if cur == old {
				return rejection
			}
			logger.Ctx(ctx).Info("server_svc.refresh: the token we submitted had already been rotated away, retrying with the stored one")
			continue
		}
		if status != http.StatusOK || env.Code != 0 {
			return fmt.Errorf("%w: code=%d msg=%s", ErrRefreshFailed, env.Code, env.Msg)
		}

		if err := keychain.Default().Set(keychainAccountName, env.Data.RefreshToken); err != nil {
			return err
		}
		s.getClient().SetAccessToken(env.Data.AccessToken)

		return nil
	}
	// 连着两次都被判重放、且存着的那枚每次都在我们手上换掉了：这不是「登录失效」的
	// 证据，登录态必须留着（%v 而非 %w：绝不能让它被当成 ErrRefreshRejected）。
	return fmt.Errorf("%w: refresh token kept being rotated away: %v", ErrRefreshFailed, rejection)
}

// credentialRejection 判定这次失败是不是「服务端说这份凭据不作数了」。
//
// 判据是 OAuth 的 error 码而不是 HTTP 状态：server 把 invalid_grant 映射成
// HTTP 400（device_ctr.oauthErrToHTTP），单看状态码分不出它和「请求被反代改坏了」。
// 401/403 一并算拒绝，是留给「换了鉴权前置」的部署形态。
func credentialRejection(status int, oauthError string) bool {
	if oauthError == "invalid_grant" || oauthError == "invalid_client" {
		return true
	}
	return status == http.StatusUnauthorized || status == http.StatusForbidden
}

// IsCredentialRejected 是给包外（bootstrap / app）用的判据：true 表示这份登录
// 已经被服务端作废，重来多少次都没用；false 表示只是暂时够不着，登录态必须留着。
func IsCredentialRejected(err error) bool { return errors.Is(err, ErrRefreshRejected) }

// withAuth runs fn(ctx); on 401, refreshes once and retries.
//
// 只有服务端**明确拒绝**了 refresh_token、且我们提交的正是当前存着的那一枚，才拆掉
// 本地登录（见 exchangeRefreshToken：重放被拒 ≠ 登录失效）。服务端够不着 / 5xx 时
// 保留登录态，仅把自己标成离线：下一次成功的调用（sync_svc 每 30 秒一轮下行轮询）
// 会自动把它复位回在线，用户不必重新登录。
//
// 取票要在 fn 之前：崩溃重启后 catch-up / sync / 设备清单会几乎同时撞 401，晚到的
// 那几条必须认出「凭据已经被别人续上了」，直接拿新的重试。
func (s *service) withAuth(ctx context.Context, fn func(ctx context.Context) error) error {
	ticket := s.refreshTicket()
	err := fn(ctx)
	if err == nil {
		s.markOnline()
		return nil
	}
	if !is401(err) {
		if transientOutage(err) {
			s.markOffline(err)
		}
		return err
	}
	// 命中 401 = access token 过期。先 refresh 一次再重试。
	logger.Ctx(ctx).Info("server_svc.withAuth: 401 received, refreshing access token")
	if rerr := s.refreshClearingDeadLogin(ctx, ticket); rerr != nil {
		if IsCredentialRejected(rerr) {
			// 登录已由那条刷新善后掉了（一次刷新只清一次）；这里只把失败交回调用方。
			return rerr
		}
		logger.Ctx(ctx).Warn("server_svc.withAuth: refresh unavailable, keeping login and marking offline",
			zap.Error(rerr))
		s.markOffline(rerr)
		return rerr
	}
	if err := fn(ctx); err != nil {
		if transientOutage(err) {
			s.markOffline(err)
		}
		return err
	}
	s.markOnline()
	return nil
}

// RefreshWithBackoff 是开机热身用的刷新：服务端够不着就退避重试，直到刷新成功、
// 凭据被明确拒绝、本机已登出，或 ctx 结束。刷新失败不清登录：那会把一次服务端停机变成
// 一次不可逆的本地登出（keychain 里的 refresh_token 被删，服务端恢复也回不来）。
func (s *service) RefreshWithBackoff(ctx context.Context) {
	delay := refreshRetryInitial
	for {
		row, err := server_state_repo.ServerState().Get(ctx)
		if err != nil || row == nil || !row.IsLoggedIn() {
			// 登出（或状态读不到）时收手：没有凭据可刷，重试只会空转。
			return
		}

		rerr := s.refreshClearingDeadLogin(ctx, s.refreshTicket())
		if rerr == nil {
			s.markOnline()
			return
		}
		if IsCredentialRejected(rerr) {
			// 登录已由那条刷新善后掉了；退避重试就此收手，没有凭据可刷了。
			return
		}

		logger.Ctx(ctx).Warn("server_svc.RefreshWithBackoff: server out of reach, keeping login and retrying",
			zap.Error(rerr), zap.Duration("retryIn", delay))
		s.markOffline(rerr)
		if !s.sleepFn(ctx, delay) {
			return
		}
		delay = nextBackoff(delay)
	}
}

// nextBackoff 翻倍并封顶。
func nextBackoff(d time.Duration) time.Duration {
	if n := d * 2; n < refreshRetryMax {
		return n
	}
	return refreshRetryMax
}

// waitOrDone 等 d，或在 ctx 结束时提前返回 false（不要再试了）。
func waitOrDone(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Offline 报告服务端此刻是否够不着。前端挂载时读它取初值——事件可能早于挂载发出。
func (s *service) Offline() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.offline
}

// markOffline / markOnline 只在状态翻转时发事件，避免退避重试期间刷屏。
func (s *service) markOffline(cause error) {
	s.mu.Lock()
	was := s.offline
	s.offline = true
	s.mu.Unlock()
	if !was {
		s.emit(map[string]any{"kind": "server_offline", "reason": cause.Error()})
	}
}

func (s *service) markOnline() {
	s.mu.Lock()
	was := s.offline
	s.offline = false
	s.mu.Unlock()
	if was {
		s.emit(map[string]any{"kind": "server_online"})
	}
}

// transientOutage 判定一个错误是不是「服务端此刻够不着」：连不上，或服务端自己
// 5xx 了。业务级 4xx 不算——那是请求本身的问题，标离线只会误导用户。
func transientOutage(err error) bool {
	if errors.Is(err, ErrServerUnreachable) {
		return true
	}
	var he interface{ HTTPStatus() int }
	if errors.As(err, &he) {
		return he.HTTPStatus() >= 500
	}
	return false
}

// is401 returns true if err exposes HTTPStatus() == 401.
func is401(err error) bool {
	var he interface{ HTTPStatus() int }
	if errors.As(err, &he) {
		return he.HTTPStatus() == http.StatusUnauthorized
	}
	return false
}

// clearLogin tears down the persisted login: keychain entry, server_state user/device
// fields, and notifies the UI via emitState. Best-effort: each step is independent.
func (s *service) clearLogin(ctx context.Context) error {
	_ = keychain.Default().Delete(keychainAccountName)
	if err := server_state_repo.ServerState().ClearLoginFields(ctx); err != nil {
		logger.Ctx(ctx).Warn("server_svc.clearLogin: ClearLoginFields failed",
			zap.Error(err))
		return err
	}
	logger.Ctx(ctx).Info("server_svc.clearLogin: local login cleared, emitting logged_out")
	s.emit(map[string]any{"kind": "logged_out", "reason": "refresh_expired"})
	return nil
}

// ClearLogin is the exported wrapper around clearLogin for bootstrap-time use
// (e.g. the interrupted-logout guard that finds a half-cleared server_state row).
func (s *service) ClearLogin(ctx context.Context) error { return s.clearLogin(ctx) }

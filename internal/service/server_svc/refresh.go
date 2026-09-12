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

// refresh exchanges the stored refresh_token for a new access token.
// Server-side refresh-token rotation: the response carries a fresh refresh_token
// and we overwrite the keychain entry. The desktop only keeps the latest one.
//
// 失败分两类，调用方靠 IsCredentialRejected 区分：服务端明确拒绝（ErrRefreshRejected，
// 凭据真没了）与够不着 / 5xx / keychain 读不到（原样返回，登录态一个字都不许动）。
func (s *service) refresh(ctx context.Context) error {
	old, err := keychain.Default().Get(keychainAccountName)
	if err != nil {
		// keychain 上锁或后端暂时不可用也走这里 —— 凭据可能好端端躺着，不能据此判死。
		return fmt.Errorf("%w: keychain: %w", ErrRefreshFailed, err)
	}

	var env refreshEnvelope
	status, err := s.getClient().do(ctx, http.MethodPost, "/v1/oauth/token/refresh",
		map[string]string{"refresh_token": old}, &env)
	if err != nil {
		if credentialRejection(status, env.Error) {
			return fmt.Errorf("%w: %s", ErrRefreshRejected, env.Error)
		}
		return err
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
// 只有服务端**明确拒绝**了 refresh_token 才拆掉本地登录。服务端够不着 / 5xx 时
// 保留登录态，仅把自己标成离线：下一次成功的调用（sync_svc 每 30 秒一轮下行轮询）
// 会自动把它复位回在线，用户不必重新登录。
func (s *service) withAuth(ctx context.Context, fn func(ctx context.Context) error) error {
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
	if rerr := s.refresh(ctx); rerr != nil {
		if IsCredentialRejected(rerr) {
			logger.Ctx(ctx).Warn("server_svc.withAuth: server rejected the refresh token, clearing login",
				zap.Error(rerr))
			_ = s.clearLogin(ctx)
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
// 凭据被明确拒绝、本机已登出，或 ctx 结束。它取代了老的「刷新失败即清登录」——
// 那条路径把一次服务端停机变成了一次不可逆的本地登出（keychain 里的 refresh_token
// 被删，服务端恢复也回不来）。
func (s *service) RefreshWithBackoff(ctx context.Context) {
	delay := refreshRetryInitial
	for {
		row, err := server_state_repo.ServerState().Get(ctx)
		if err != nil || row == nil || !row.IsLoggedIn() {
			// 登出（或状态读不到）时收手：没有凭据可刷，重试只会空转。
			return
		}

		rerr := s.refresh(ctx)
		if rerr == nil {
			s.markOnline()
			return
		}
		if IsCredentialRejected(rerr) {
			logger.Ctx(ctx).Warn("server_svc.RefreshWithBackoff: server rejected the stored credential, clearing login",
				zap.Error(rerr))
			_ = s.clearLogin(ctx)
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

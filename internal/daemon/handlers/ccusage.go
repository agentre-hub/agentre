package handlers

import (
	"context"
	"errors"

	"github.com/agentre-hub/agentre/internal/pkg/ccoauth"
)

// CCUsageFetcher 是 ccusage handler 注入 OAuth usage 拉取的钩子。
// 生产里由 NewProductionCCUsageFetcher 构造,从 agentred 所在机器的
// 环境(CLAUDE_CONFIG_DIR / current user / Keychain)读凭证 + 调 Anthropic。
// 测试里注入 stub 控制返回值。
type CCUsageFetcher func(ctx context.Context) (*ccoauth.RateLimits, error)

// CCUsageResult 是 claudecode.usage RPC 的响应。reason 是稳定 enum,
// 客户端按 reason 决定 UI 状态;data 仅在 reason="ok" 时非 nil。
type CCUsageResult struct {
	Reason string              `json:"reason"`
	Data   *ccoauth.RateLimits `json:"data,omitempty"`
}

// CCUsageHandlers 承载 claudecode.usage 一个方法。结构体而非函数,方便后续
// 加 daemon-side 缓存(避免短时多次 RPC 都打到 Anthropic)。
type CCUsageHandlers struct {
	fetch CCUsageFetcher
}

// NewCCUsageHandlers 注入 fetcher。nil 视为"无凭证",所有 Get 调用直接返回
// no_credentials,daemon 启动不强依赖 ccoauth 可用。
func NewCCUsageHandlers(fetch CCUsageFetcher) *CCUsageHandlers {
	return &CCUsageHandlers{fetch: fetch}
}

// Get 调一次 ccoauth.Fetch 并把错误映射成稳定 reason enum。
// reason 与 cc_usage_svc 内部 enum 保持一致:
//
//	ok / no_credentials / auth_expired / network / rate_limited
//
// 上层(桌面 cc_usage_svc.remote_fetcher) 直接把 reason 透传给前端 store。
func (h *CCUsageHandlers) Get(ctx context.Context) (CCUsageResult, error) {
	if h.fetch == nil {
		return CCUsageResult{Reason: "no_credentials"}, nil
	}
	rl, err := h.fetch(ctx)
	if err != nil {
		return CCUsageResult{Reason: classifyFetchErr(err)}, nil
	}
	return CCUsageResult{Reason: "ok", Data: rl}, nil
}

func classifyFetchErr(err error) string {
	switch {
	case errors.Is(err, ccoauth.ErrNoCredentials):
		return "no_credentials"
	case errors.Is(err, ccoauth.ErrAuthExpired):
		return "auth_expired"
	case errors.Is(err, ccoauth.ErrRateLimited):
		return "rate_limited"
	default:
		return "network"
	}
}

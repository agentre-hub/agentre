package data_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// Scope 是一个导入导出范围。
type Scope string

const (
	ScopeLLMProviders  Scope = "llm-providers"
	ScopeAgentBackends Scope = "agent-backends"
	ScopeOrganization  Scope = "organization"
	ScopeRemoteDevices Scope = "remote-devices"
)

// validScopes 当前支持的全部 scope。
var validScopes = map[Scope]struct{}{
	ScopeLLMProviders:  {},
	ScopeAgentBackends: {},
	ScopeOrganization:  {},
	ScopeRemoteDevices: {},
}

// ValidateScopes 校验列表里的 scope 全部识别。
func ValidateScopes(ctx context.Context, scopes []string) error {
	for _, s := range scopes {
		if _, ok := validScopes[Scope(s)]; !ok {
			return i18n.NewError(ctx, code.DataBundleScopeUnknown)
		}
	}
	return nil
}

// scopeSet 把字符串 slice 转 set。
func scopeSet(scopes []string) map[Scope]struct{} {
	out := make(map[Scope]struct{}, len(scopes))
	for _, s := range scopes {
		out[Scope(s)] = struct{}{}
	}
	return out
}

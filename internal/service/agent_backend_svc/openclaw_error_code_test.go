package agent_backend_svc

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/openclawgateway"
)

// 真实网关(2026.7.1-2)对 token 不匹配回的是 INVALID_REQUEST + "unauthorized: gateway
// token mismatch (...)",不是 AUTH_FAILED。旧映射只认 AUTH_FAILED/UNAUTHORIZED/FORBIDDEN,
// 于是前端命不中 keyByCode,直接把原始协议串糊到用户脸上,而写好的 errors.authFailed 永远用不上。
func TestOpenClawProbeErrorCode_MapsGatewayAuthFailures(t *testing.T) {
	cases := []struct {
		name string
		err  *openclawgateway.RPCError
		want string
	}{
		{
			name: "real gateway token mismatch",
			err:  &openclawgateway.RPCError{Code: "INVALID_REQUEST", Message: "unauthorized: gateway token mismatch (set gateway.authToken)"},
			want: "AUTH_FAILED",
		},
		{
			name: "structured unauthorized reason",
			err:  &openclawgateway.RPCError{Code: "INVALID_REQUEST", Reason: "unauthorized", Message: "nope"},
			want: "AUTH_FAILED",
		},
		{
			name: "explicit auth codes pass through",
			err:  &openclawgateway.RPCError{Code: "UNAUTHORIZED", Message: "no"},
			want: "AUTH_FAILED",
		},
		{
			name: "device pairing required",
			err:  &openclawgateway.RPCError{Code: "NOT_PAIRED", Message: "pairing required: device is asking for more scopes than currently approved"},
			want: "OPENCLAW_NOT_PAIRED",
		},
		{
			name: "unrelated rpc errors keep their code",
			err:  &openclawgateway.RPCError{Code: "INVALID_REQUEST", Message: "bad params"},
			want: "INVALID_REQUEST",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, openClawProbeErrorCode(tc.err))
		})
	}
}

// 校验类失败过去全塌成 code.InvalidParameter,前端只能拿到后端 i18n 的中文「参数错误」,
// 既区分不出错在哪个字段,又把中文糊进英文 UI。改成结构化 Code 让前端本地化。
func TestOpenClawDraftIssue_ReturnsStructuredCodes(t *testing.T) {
	base := func() *agent_backend_entity.AgentBackend {
		return &agent_backend_entity.AgentBackend{
			Type:                string(agent_backend_entity.TypeOpenClaw),
			Name:                "OpenClaw",
			OpenClawGatewayURL:  "ws://127.0.0.1:18789",
			OpenClawSessionMode: agent_backend_entity.OpenClawSessionPerAgentRESession,
		}
	}
	cases := []struct {
		name    string
		mutate  func(*agent_backend_entity.AgentBackend)
		wantOK  bool
		wantErr string
	}{
		{name: "valid draft", mutate: func(*agent_backend_entity.AgentBackend) {}, wantOK: true},
		{
			name:    "name required",
			mutate:  func(b *agent_backend_entity.AgentBackend) { b.Name = "  " },
			wantErr: "OPENCLAW_NAME_REQUIRED",
		},
		{
			name:    "url required",
			mutate:  func(b *agent_backend_entity.AgentBackend) { b.OpenClawGatewayURL = "" },
			wantErr: "OPENCLAW_URL_REQUIRED",
		},
		{
			name:    "wrong scheme",
			mutate:  func(b *agent_backend_entity.AgentBackend) { b.OpenClawGatewayURL = "http://127.0.0.1:18789" },
			wantErr: "OPENCLAW_URL_SCHEME",
		},
		{
			name:    "plaintext to remote host",
			mutate:  func(b *agent_backend_entity.AgentBackend) { b.OpenClawGatewayURL = "ws://example.com:18789" },
			wantErr: "OPENCLAW_URL_PLAINTEXT_REMOTE",
		},
		{
			name:    "credentials in url",
			mutate:  func(b *agent_backend_entity.AgentBackend) { b.OpenClawGatewayURL = "ws://user:pass@127.0.0.1:18789" },
			wantErr: "OPENCLAW_URL_CREDENTIALS",
		},
		{
			name:    "query in url",
			mutate:  func(b *agent_backend_entity.AgentBackend) { b.OpenClawGatewayURL = "ws://127.0.0.1:18789/?token=x" },
			wantErr: "OPENCLAW_URL_CREDENTIALS",
		},
		{
			name:    "missing host",
			mutate:  func(b *agent_backend_entity.AgentBackend) { b.OpenClawGatewayURL = "ws://" },
			wantErr: "OPENCLAW_URL_HOST",
		},
		{
			name:    "session mode",
			mutate:  func(b *agent_backend_entity.AgentBackend) { b.OpenClawSessionMode = "per-agent" },
			wantErr: "OPENCLAW_SESSION_MODE_INVALID",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := base()
			tc.mutate(backend)
			got := openClawDraftIssue(backend)
			if tc.wantOK {
				assert.Nil(t, got)
				return
			}
			if assert.NotNil(t, got) {
				assert.False(t, got.OK)
				assert.Equal(t, tc.wantErr, got.Code)
				// 结构化响应不再把后端 i18n 中文当作用户文案。
				assert.NotContains(t, got.Message, "参数错误")
			}
		})
	}
}

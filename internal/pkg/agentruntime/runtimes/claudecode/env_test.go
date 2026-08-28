package claudecode

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

// TestBuildClaudeCodeEnv_DelegatesToAgentruntime 烟测 —— 委托链路通,deps
// 注入的 token / model alias 都能透过子包入口出现在结果里。完整边界覆盖
// (CLI 登录 vs 绑定 provider、env_json 用户追加、model_routes alias)在顶层
// agentruntime 包的 clienv_test.go 里;此处只验"再导出后行为一致"。
func TestBuildClaudeCodeEnv_DelegatesToAgentruntime(t *testing.T) {
	b := &agent_backend_entity.AgentBackend{
		LLMProviderKey: "key-11",
		ModelRoutes:    `{"OPUS":{"providerKey":"key-2"}}`,
	}
	env, err := BuildClaudeCodeEnv(b, CLIDeps{Token: "tok-abc", GatewayURL: "http://gateway.local", ProviderKey: "key-11"})
	require.NoError(t, err)

	// 绑 provider + 给 gateway → AUTH_TOKEN + BASE_URL 都出现;
	// hook 子进程总能拿到的 AGENTRE_* 同样在。
	assert.Equal(t, "tok-abc", env["AGENTRE_GATEWAY_TOKEN"])
	assert.Equal(t, "http://gateway.local", env["AGENTRE_GATEWAY_URL"])
	assert.Equal(t, "tok-abc", env["ANTHROPIC_AUTH_TOKEN"])
	assert.Equal(t, "http://gateway.local", env["ANTHROPIC_BASE_URL"])
	// model_routes OPUS → alias env
	assert.Equal(t, "opus", env["ANTHROPIC_DEFAULT_OPUS_MODEL"])
}

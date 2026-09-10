package agent_backend_entity

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMarshalConfig_RoundTripsEveryTypeExclusiveSetting 是这一层的主契约：九个
// 单类型独占设置整组进一列 JSON、再整组回到同样的字段上。
//
// 它挡住的坏实现是「只加了一半」——某个字段进了 MarshalConfig 却漏了
// UnmarshalConfig（或反过来）。那种漏法在单向测试里看不出来：写的时候一切正常，
// 读回来那一格悄悄变成零值，而零值在这几个字段上都是有意义的合法取值
// （空 sandbox = 走 CLI 默认），于是没有任何一处会报错。
func TestMarshalConfig_RoundTripsEveryTypeExclusiveSetting(t *testing.T) {
	original := &AgentBackend{
		ModelRoutes:           `{"OPUS":{"providerKey":"p-1","modelKey":"m-1"}}`,
		Sandbox:               "workspace-write",
		Approval:              "on-request",
		DefaultPermissionMode: "plan",
		DefaultModel:          "opus",
		OpenClawGatewayURL:    "wss://gw.example/rpc",
		OpenClawAgentID:       "main",
		OpenClawDefaultModel:  "anthropic/claude-sonnet-4-6",
		OpenClawSessionMode:   OpenClawSessionPerAgentRESession,
	}
	require.NoError(t, original.MarshalConfig())

	restored := &AgentBackend{ConfigJSON: original.ConfigJSON}
	require.NoError(t, restored.UnmarshalConfig())

	assert.JSONEq(t, original.ModelRoutes, restored.ModelRoutes)
	assert.Equal(t, original.Sandbox, restored.Sandbox)
	assert.Equal(t, original.Approval, restored.Approval)
	assert.Equal(t, original.DefaultPermissionMode, restored.DefaultPermissionMode)
	assert.Equal(t, original.DefaultModel, restored.DefaultModel)
	assert.Equal(t, original.OpenClawGatewayURL, restored.OpenClawGatewayURL)
	assert.Equal(t, original.OpenClawAgentID, restored.OpenClawAgentID)
	assert.Equal(t, original.OpenClawDefaultModel, restored.OpenClawDefaultModel)
	assert.Equal(t, original.OpenClawSessionMode, restored.OpenClawSessionMode)
}

// TestMarshalConfig_GivenTypeThatOwnsNothing_WritesEmptyObject 大多数行是这一档：
// builtin 后端一个独占设置都没有。它必须落成 "{}" 而不是 "null" —— 列上带
// NOT NULL DEFAULT '{}'，而 "null" 会让下一次 UnmarshalConfig 拿到一个 nil 配置。
func TestMarshalConfig_GivenTypeThatOwnsNothing_WritesEmptyObject(t *testing.T) {
	b := &AgentBackend{Type: string(TypeBuiltin), Name: "内置"}
	require.NoError(t, b.MarshalConfig())
	assert.Equal(t, "{}", b.ConfigJSON)
}

// TestMarshalConfig_GivenEmptyModelRoutes_OmitsTheKey model_routes 的「空」有两种
// 写法（"" 与 "{}"），都不该在配置里留下痕迹：否则每一行 builtin/codex 后端都会
// 平白带一个 {"modelRoutes":{}}，而 Check 与同步载荷都把它当成「没配路由」。
func TestMarshalConfig_GivenEmptyModelRoutes_OmitsTheKey(t *testing.T) {
	for _, empty := range []string{"", "{}", "  "} {
		b := &AgentBackend{ModelRoutes: empty}
		require.NoError(t, b.MarshalConfig())

		var got map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(b.ConfigJSON), &got))
		assert.NotContains(t, got, "modelRoutes", "空路由不该留下键：%q", empty)
	}
}

// TestUnmarshalConfig_GivenBlankColumn_LeavesFieldsZeroed 老行与刚建的行都可能
// 拿到空串（列默认是 '{}'，但 GORM 的零值实体也会走到这里），当作空配置处理，
// 不报错。
func TestUnmarshalConfig_GivenBlankColumn_LeavesFieldsZeroed(t *testing.T) {
	b := &AgentBackend{ConfigJSON: ""}
	require.NoError(t, b.UnmarshalConfig())
	assert.Empty(t, b.Sandbox)
	assert.Empty(t, b.OpenClawGatewayURL)
}

// TestUnmarshalConfig_GivenMalformedJSON_ReturnsError 坏数据必须报出来。静默清零
// 是最坏的那一种：一条 openclaw 后端的网关地址会变成空串，而空网关地址在
// openClawKind.ValidateExtra 之外的地方读起来只是「还没配」。
func TestUnmarshalConfig_GivenMalformedJSON_ReturnsError(t *testing.T) {
	b := &AgentBackend{ConfigJSON: `{"sandbox":`}
	assert.Error(t, b.UnmarshalConfig())
}

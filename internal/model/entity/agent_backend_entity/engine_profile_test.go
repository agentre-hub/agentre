package agent_backend_entity

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
)

func requireEntityCode(t *testing.T, err error, want int) {
	t.Helper()
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, want, httpErr.Code)
}

// Given 一份配好的引擎配置 When 取它的执行视图 Then 每一格设置都原样带上、设备与 CLI
// 路径为空，同步身份就是配置自己的 sync_id（OpenClaw 槽位据此按配置命名）。
func TestEngineProfile_AsBackend_CarriesSettingsWithoutDevice(t *testing.T) {
	p := &EngineProfile{
		ID: 7, Type: string(TypeCodex), Name: "Codex",
		LLMProviderKey: "p-1", LLMModelKey: "m-1",
		Sandbox: "workspace-write", Approval: "never",
		EnvJSON: `{"K":"V"}`, ReasoningEffort: "high",
		Status:   consts.ACTIVE,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "01PROFILE"},
	}
	b := p.AsBackend()
	assert.Equal(t, string(TypeCodex), b.Type)
	assert.Equal(t, "Codex", b.Name)
	assert.Equal(t, "p-1", b.LLMProviderKey)
	assert.Equal(t, "m-1", b.LLMModelKey)
	assert.Equal(t, "workspace-write", b.Sandbox)
	assert.Equal(t, "never", b.Approval)
	assert.Equal(t, `{"K":"V"}`, b.EnvJSON)
	assert.Equal(t, "high", b.ReasoningEffort)
	assert.Equal(t, "01PROFILE", b.SyncID)
	assert.Empty(t, b.DeviceFingerprint)
	assert.Empty(t, b.CLIPath)
	assert.Zero(t, b.ID, "the view is not a backend row; it must not borrow the profile id")
}

// Given 引擎配置的类型专属设置 When 收进 config_json 再摊回 Then 一格不丢。
func TestEngineProfile_ConfigRoundTrip(t *testing.T) {
	original := &EngineProfile{
		Type:                  string(TypeClaudeCode),
		ModelRoutes:           `{"OPUS":{"providerKey":"p-1","modelKey":"m-1"}}`,
		DefaultPermissionMode: "plan",
		DefaultModel:          "opus",
	}
	require.NoError(t, original.MarshalConfig())

	restored := &EngineProfile{ConfigJSON: original.ConfigJSON}
	require.NoError(t, restored.UnmarshalConfig())
	assert.JSONEq(t, original.ModelRoutes, restored.ModelRoutes)
	assert.Equal(t, "plan", restored.DefaultPermissionMode)
	assert.Equal(t, "opus", restored.DefaultModel)
}

func TestEngineProfile_Check(t *testing.T) {
	ctx := context.Background()

	t.Run("Given a valid native claudecode profile Then it passes", func(t *testing.T) {
		p := &EngineProfile{Type: string(TypeClaudeCode), Name: "Claude Code", EnvJSON: "{}", ModelRoutes: "{}"}
		assert.NoError(t, p.Check(ctx))
	})
	t.Run("Given an unknown runtime type Then it is rejected as an invalid type", func(t *testing.T) {
		p := &EngineProfile{Type: "nope", Name: "x"}
		requireEntityCode(t, p.Check(ctx), code.AgentBackendInvalidType)
	})
	t.Run("Given a blank name Then it is rejected", func(t *testing.T) {
		p := &EngineProfile{Type: string(TypeClaudeCode), Name: "  "}
		requireEntityCode(t, p.Check(ctx), code.InvalidParameter)
	})
	t.Run("Given a reasoning effort outside the six levels Then it is rejected", func(t *testing.T) {
		p := &EngineProfile{Type: string(TypeClaudeCode), Name: "x", ReasoningEffort: "extreme"}
		requireEntityCode(t, p.Check(ctx), code.AgentBackendInvalidReasoningEffort)
	})
	t.Run("Given a nil profile Then it reports not found", func(t *testing.T) {
		var p *EngineProfile
		requireEntityCode(t, p.Check(ctx), code.EngineProfileNotFound)
	})
}

// Given 新建时没填名称 Then 默认名是运行时名；未知类型退回类型串本身。
func TestDefaultEngineProfileName(t *testing.T) {
	assert.Equal(t, "Claude Code", DefaultEngineProfileName(string(TypeClaudeCode)))
	assert.Equal(t, "Codex", DefaultEngineProfileName(string(TypeCodex)))
	assert.Equal(t, "Pi Agent", DefaultEngineProfileName(string(TypePiAgent)))
	assert.Equal(t, "mystery", DefaultEngineProfileName("mystery"))
}

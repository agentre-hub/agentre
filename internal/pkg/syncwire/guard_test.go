package syncwire_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/agentre-ai/agentre/internal/pkg/syncwire"
)

// Public guard contract matches server sync_entity.ValidatePayload: kind is
// mandatory because API-key admission is kind-specific.
var _ func(string, []byte) error = syncwire.GuardPayload

// TestGuardPayload_RejectsLocalAutoIncrementIDs R2 的守卫断言：载荷里不出现任何
// 本地自增 ID——「键名以 id 结尾、取值是数字」这个形状只可能是本机主键。
func TestGuardPayload_RejectsLocalAutoIncrementIDs(t *testing.T) {
	for _, payload := range []string{
		`{"name":"x","parent_id":7}`,
		`{"name":"x","agentBackendId":7}`,
		`{"targets":[{"agent-backend-id":7}]}`,
		`{"nested":{"department_id":1}}`,
	} {
		assert.ErrorIs(t, syncwire.GuardPayload("", []byte(payload)), syncwire.ErrPayloadLocalID, payload)
	}
}

// TestGuardPayload_AllowsStringReferences 跨机引用一律是字符串：同步标识、指纹、
// provider_key、model_key，它们照常放行。model_key 是 2026-08-11 LLM Provider
// 多模型契约新增的稳定模型引用（Provider / Models 与 API Key 均不进入账号同步，
// 业务对象只携带 provider_key / model_key 字符串），归一化后是 modelkey，不落进
// apikey / provider / *id 任何一条守卫。
func TestGuardPayload_AllowsStringReferences(t *testing.T) {
	for _, payload := range []string{
		`{"name":"x","parent_sync_id":"01ARZ3ND"}`,
		`{"provider_key":"anthropic-main"}`,
		`{"provider_key":"anthropic-main","model_key":"anthropic-opus-01"}`,
		`{"modelKey":"anthropic-opus-01"}`,
		`{"agent_sync_id":"a","backend_sync_id":"b","sort_order":2}`,
		`{}`,
		``,
	} {
		assert.NoError(t, syncwire.GuardPayload("", []byte(payload)), payload)
	}
}

// TestGuardPayload_RejectsCredentialsAndProviderRows llm_providers 整表（含 APIKey）
// 不出本机（决策 6）：跨机只传 provider_key 这个字符串键。
func TestGuardPayload_RejectsCredentialsAndProviderRows(t *testing.T) {
	assert.ErrorIs(t, syncwire.GuardPayload("", []byte(`{"api_key":"sk-x"}`)), syncwire.ErrPayloadCredential)
	assert.ErrorIs(t, syncwire.GuardPayload("", []byte(`{"apiKey":"sk-x"}`)), syncwire.ErrPayloadCredential)
	assert.ErrorIs(t, syncwire.GuardPayload("", []byte(`{"provider":{"api_key":"sk-x"}}`)), syncwire.ErrPayloadCredential)
	assert.ErrorIs(t, syncwire.GuardPayload("", []byte(`{"providers":[{"name":"p"}]}`)), syncwire.ErrPayloadCredential)
}

// TestGuardPayload_RejectsAvatarContent R16a 的守卫断言：同步载荷里只出现头像
// 的内容哈希，正文（avatar_data_url）一律不得出现，不管值是 data URL 还是别的。
// TestGuardPayload_GivenProviderKind_AllowsAPIKey keeps the desktop guard aligned
// with server ValidatePayload: a provider's API key is account-synced, while the
// same key remains forbidden for every other kind.
func TestGuardPayload_GivenProviderKind_AllowsAPIKey(t *testing.T) {
	assert.NoError(t, syncwire.GuardPayload(syncwire.KindLLMProvider, []byte(`{"api_key":"sk-provider"}`)))
	assert.ErrorIs(t, syncwire.GuardPayload(syncwire.KindAgentBackend, []byte(`{"cli_path":"/usr/local/bin/claude"}`)), syncwire.ErrPayloadCredential)
}

func TestGuardPayload_RejectsAvatarContent(t *testing.T) {
	for _, payload := range []string{
		`{"name":"x","avatar_data_url":"data:image/png;base64,AAAA"}`,
		`{"avatarDataUrl":"data:image/png;base64,AAAA"}`,
	} {
		assert.ErrorIs(t, syncwire.GuardPayload("", []byte(payload)), syncwire.ErrPayloadAvatarContent, payload)
	}
	// avatar_hash 是允许的：它是内容哈希这个字符串引用，不是正文。
	assert.NoError(t, syncwire.GuardPayload("", []byte(`{"avatar_hash":"deadbeef"}`)))
}

// TestGuardPayload_RejectsNonObject 载荷必须是一个 JSON 对象。
func TestGuardPayload_RejectsNonObject(t *testing.T) {
	assert.ErrorIs(t, syncwire.GuardPayload("", []byte(`[1,2]`)), syncwire.ErrPayloadNotObject)
	assert.Error(t, syncwire.GuardPayload("", []byte(`{`)))
}

// TestGuardPayload_TheSharedVectors 这一组向量与 server 侧 sync_entity 的同名测试
// **逐条一致**：两个仓库不能互相 import，规则只能靠两份相同的向量对齐。任何一边加
// 规则，这张表要同步改。
//
// 最后一条是刻意的**反向**断言：env_json 是用户自填的透传环境变量表，按设计随
// backend 明文过机，守卫不看 JSON 字符串内部。守卫的注释不承诺它会被过滤，这条测试
// 把「不过滤」钉死，免得日后有人照着注释误以为凭据一定进不了同步载荷。
func TestGuardPayload_TheSharedVectors(t *testing.T) {
	rejected := []string{
		`{"parent_id":7}`,
		`{"agentBackendId":7}`,
		`{"targets":[{"agent-backend-id":7}]}`,
		`{"nested":{"department_id":1}}`,
		`{"api_key":"sk-x"}`,
		`{"apiKey":"sk-x"}`,
		`{"provider":{"api_key":"sk-x"}}`,
		`{"providers":[{"name":"p"}]}`,
		`{"avatar_data_url":"data:image/png;base64,AAAA"}`,
		`{"avatarDataUrl":"data:image/png;base64,AAAA"}`,
	}
	accepted := []string{
		`{"name":"x","parent_sync_id":"01ARZ3ND"}`,
		`{"provider_key":"anthropic-main"}`,
		`{"agent_sync_id":"a","backend_sync_id":"b","sort_order":2}`,
		`{"avatar_hash":"deadbeef"}`,
		`{}`,
		``,
		`{"env_json":"{\"MY_TOKEN\":\"secret\"}"}`,
	}
	for _, payload := range rejected {
		assert.Error(t, syncwire.GuardPayload("", []byte(payload)), payload)
	}
	for _, payload := range accepted {
		assert.NoError(t, syncwire.GuardPayload("", []byte(payload)), payload)
	}
}

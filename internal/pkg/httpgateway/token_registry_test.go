package httpgateway

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

func TestTokenRegistry_IssueResolveRevoke(t *testing.T) {
	r := NewTokenRegistry()

	b := &agent_backend_entity.AgentBackend{
		ID:             7,
		Type:           string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "key-3",
		ModelRoutes:    `{"OPUS":{"providerKey":"key-5"},"SONNET":{"providerKey":"key-6","modelKey":"mk-6"}}`,
	}
	tok, err := r.Issue(b, b.LLMProviderKey, "", 60*time.Second)
	assert.NoError(t, err)
	assert.NotEmpty(t, tok)
	assert.Equal(t, 1, r.Size())

	got, ok := r.Resolve(tok)
	if assert.True(t, ok) {
		assert.Equal(t, int64(7), got.BackendID)
		assert.Equal(t, "key-3", got.Main.ProviderKey)
		assert.Equal(t, "", got.Main.ModelKey, "未传 modelKey → provider-default")
		assert.Equal(t, agent_backend_entity.TypeClaudeCode, got.BackendType)
		// alias 已规范成大写；tier target 携带完整 ProviderKey+ModelKey
		assert.Equal(t, "key-5", got.Routes["OPUS"].ProviderKey)
		assert.Empty(t, got.Routes["OPUS"].ModelKey)
		assert.Equal(t, "key-6", got.Routes["SONNET"].ProviderKey)
		assert.Equal(t, "mk-6", got.Routes["SONNET"].ModelKey)
	}

	r.Revoke(tok)
	_, ok = r.Resolve(tok)
	assert.False(t, ok)
	assert.Equal(t, 0, r.Size())
}

func TestTokenRegistry_RejectInvalidBackend(t *testing.T) {
	r := NewTokenRegistry()
	cases := []struct {
		name string
		b    *agent_backend_entity.AgentBackend
	}{
		{"nil", nil},
		{"malformed routes", &agent_backend_entity.AgentBackend{ID: 1, LLMProviderKey: "key-2", ModelRoutes: `{not json`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Issue(tc.b, "key-2", "", time.Minute)
			assert.Error(t, err)
		})
	}
}

// TestTokenRegistry_IssueWithoutProvider 守护 CLI 登录模式（backend 没绑 LLM
// provider）：token 仍能发出来，给 PostToolUse hook 子进程访问 /hook/v1/inbox
// 用。LLM 转发端点会因 ResolveModel→"" 自然失败（gateway handle 里 lookup
// provider 返回 nil → 502），不会被误用作 LLM bypass。
func TestTokenRegistry_IssueWithoutProvider(t *testing.T) {
	r := NewTokenRegistry()
	tok, err := r.Issue(&agent_backend_entity.AgentBackend{
		ID:             42,
		Type:           string(agent_backend_entity.TypeClaudeCode),
		LLMProviderKey: "",
	}, "", "", time.Minute)
	assert.NoError(t, err)
	assert.NotEmpty(t, tok)

	entry, ok := r.Resolve(tok)
	if assert.True(t, ok) {
		assert.Equal(t, int64(42), entry.BackendID)
		assert.Equal(t, "", entry.Main.ProviderKey, "hook-only token: provider key is empty")
		assert.Empty(t, entry.Routes)
	}
}

func TestTokenRegistry_ExpireOnResolve(t *testing.T) {
	r := NewTokenRegistry()
	frozen := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return frozen }

	tok, err := r.Issue(&agent_backend_entity.AgentBackend{ID: 1}, "key-1", "", 30*time.Second)
	assert.NoError(t, err)
	assert.Equal(t, 1, r.Size())

	// 还没到点：命中
	_, ok := r.Resolve(tok)
	assert.True(t, ok)

	// 到点：未命中并被清掉
	r.now = func() time.Time { return frozen.Add(31 * time.Second) }
	_, ok = r.Resolve(tok)
	assert.False(t, ok)
	assert.Equal(t, 0, r.Size())
}

func TestTokenRegistry_ZeroTTLNeverExpires(t *testing.T) {
	r := NewTokenRegistry()
	tok, err := r.Issue(&agent_backend_entity.AgentBackend{ID: 1}, "key-1", "", 0)
	assert.NoError(t, err)
	// 模拟未来：仍命中
	r.now = func() time.Time { return time.Now().Add(24 * time.Hour) }
	_, ok := r.Resolve(tok)
	assert.True(t, ok)
}

// TestTokenRegistry_IssueUsesEffectiveProviderKey 钉死决策 3 的签发侧：token 的主供应商
// 来源是**签发时传入的 effective key**（会话 provider_key > agent 绑定），不再由 backend
// 实体自己派生 —— 会话切了供应商之后，同一条 backend 签出来的 token 必须打到会话选的
// 那家，否则 `--model` 换了、请求还打在 agent 绑定那家上。
func TestTokenRegistry_IssueUsesEffectiveProviderKey(t *testing.T) {
	r := NewTokenRegistry()
	b := &agent_backend_entity.AgentBackend{
		ID: 7, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "agent-bound",
	}

	tok, err := r.Issue(b, "session-picked", "", time.Minute)
	assert.NoError(t, err)

	entry, ok := r.Resolve(tok)
	if assert.True(t, ok) {
		assert.Equal(t, "session-picked", entry.Main.ProviderKey, "签发时传入的 effective key 说了算")
		assert.Equal(t, int64(7), entry.BackendID, "身份仍来自 backend")
		tgt, hit := entry.ResolveModel("")
		assert.Equal(t, "session-picked", tgt.ProviderKey)
		assert.False(t, hit)
	}
}

// TestTokenRegistry_SetTokenTargetKeepsTokenString 钉死决策 3/9 的切换侧：token 字符串是
// **会话级常驻**、首轮就烤进 CLI 子进程 env 的，切换 ModelTarget 只改它在网关里的路由目标
// （ProviderKey + ModelKey），绝不重签（重签 = 在跑的子进程手里那个立刻失效，正是被修过的
// 401 事故）。
func TestTokenRegistry_SetTokenTargetKeepsTokenString(t *testing.T) {
	r := NewTokenRegistry()
	b := &agent_backend_entity.AgentBackend{
		ID: 7, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "agent-bound",
		ModelRoutes: `{"OPUS":{"providerKey":"tier-key","modelKey":"tier-mk"}}`,
	}
	tok, err := r.Issue(b, "agent-bound", "", 0)
	assert.NoError(t, err)

	prev, ok := r.SetTokenTarget(tok, "switched", "fixed-mk")
	assert.True(t, ok)
	assert.Equal(t, "agent-bound", prev, "返回旧 key，调用方据此判断是否真的换了")
	assert.Equal(t, 1, r.Size(), "不得多出一条 token")

	entry, found := r.Resolve(tok)
	if assert.True(t, found, "token 字符串不变，仍能解出来") {
		assert.Equal(t, "switched", entry.Main.ProviderKey)
		assert.Equal(t, "fixed-mk", entry.Main.ModelKey, "固定模型切换后 Main 携带完整 ModelKey")
		assert.Equal(t, int64(7), entry.BackendID, "身份不变")
		assert.Equal(t, "tier-key", entry.Routes["OPUS"].ProviderKey, "tier 路由来自 backend 配置，不受切换影响")
		assert.Equal(t, "tier-mk", entry.Routes["OPUS"].ModelKey, "tier 路由携带完整 ModelKey")
	}
}

// TestTokenRegistry_SetTokenTargetUnknownToken 未知 / 已过期 token 一律 (,,false)：
// 调用方（会话切换）据此知道这条 token 已经不在表里，不去伪造一条路由记录。
func TestTokenRegistry_SetTokenTargetUnknownToken(t *testing.T) {
	r := NewTokenRegistry()
	_, ok := r.SetTokenTarget("never-issued", "x", "")
	assert.False(t, ok)
	assert.Equal(t, 0, r.Size())

	frozen := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return frozen }
	tok, err := r.Issue(&agent_backend_entity.AgentBackend{ID: 1}, "key-1", "", 30*time.Second)
	assert.NoError(t, err)
	r.now = func() time.Time { return frozen.Add(31 * time.Second) }
	_, ok = r.SetTokenTarget(tok, "key-2", "")
	assert.False(t, ok, "过期 token 不可再改路由")
}

func TestTokenEntry_ResolveModel(t *testing.T) {
	entry := TokenEntry{
		Main:   TokenTarget{ProviderKey: "key-1"},
		Routes: map[string]TokenTarget{"OPUS": {ProviderKey: "key-5"}, "SONNET": {ProviderKey: "key-6"}},
	}

	cases := []struct {
		input string
		want  string
		hit   bool
	}{
		{"OPUS", "key-5", true},
		{"opus", "key-5", true},
		{"  Sonnet ", "key-6", true},
		{"HAIKU", "key-1", false},
		{"", "key-1", false},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			tgt, hit := entry.ResolveModel(tc.input)
			assert.Equal(t, tc.want, tgt.ProviderKey)
			assert.Equal(t, tc.hit, hit)
		})
	}
}

func TestTokenEntry_ResolveModelEmptyRoutes(t *testing.T) {
	entry := TokenEntry{Main: TokenTarget{ProviderKey: "key-42"}}
	tgt, hit := entry.ResolveModel("anything")
	assert.Equal(t, "key-42", tgt.ProviderKey)
	assert.False(t, hit)
}

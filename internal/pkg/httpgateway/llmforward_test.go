package httpgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
)

// fakeLookup 测试用 provider+model lookup，返回构造时 inject 的 map，同时实现
// ProviderLookup 与 ModelLookup（桌面端 llm_provider_repo 的等价物）。
type fakeLookup struct {
	providers map[string]*llm_provider_entity.LLMProvider
	models    map[string]*llm_provider_model_entity.LLMProviderModel
}

func (f *fakeLookup) FindByKey(_ context.Context, key string) (*llm_provider_entity.LLMProvider, error) {
	return f.providers[key], nil
}

func (f *fakeLookup) FindModelByKey(_ context.Context, modelKey string) (*llm_provider_model_entity.LLMProviderModel, error) {
	return f.models[modelKey], nil
}

func newFakeLookup(items ...*llm_provider_entity.LLMProvider) *fakeLookup {
	m := make(map[string]*llm_provider_entity.LLMProvider, len(items))
	for _, p := range items {
		m[p.ProviderKey] = p
	}
	return &fakeLookup{providers: m, models: map[string]*llm_provider_model_entity.LLMProviderModel{}}
}

func (f *fakeLookup) withModel(m *llm_provider_model_entity.LLMProviderModel) *fakeLookup {
	if f.models == nil {
		f.models = map[string]*llm_provider_model_entity.LLMProviderModel{}
	}
	f.models[m.ModelKey] = m
	return f
}

func assertOpenAIForwarded(
	t *testing.T,
	provider *llm_provider_entity.LLMProvider,
	models []*llm_provider_model_entity.LLMProviderModel,
	handler func(*Forwarder) http.HandlerFunc,
	backendID int64,
	path string,
	body string,
	apiKeyName string,
) {
	t.Helper()
	upstream, rec := newRecordingUpstream(t, `{"id":"openai_x"}`)
	provider.BaseURL = upstream.URL
	tokens := NewTokenRegistry()
	lookup := newFakeLookup(provider)
	for _, m := range models {
		lookup.withModel(m)
	}
	f := NewForwarder(tokens, lookup)

	w := issueAndRequest(t, handler(f), tokens,
		&agent_backend_entity.AgentBackend{ID: backendID, Type: string(agent_backend_entity.TypeCodex), LLMProviderKey: provider.ProviderKey},
		path,
		body,
	)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, path, rec.Path)
	assert.Equal(t, "Bearer "+testAPIKey(apiKeyName), rec.Header.Get("Authorization"))
}

func testAPIKey(name string) string {
	return strings.Join([]string{"k", name}, "-")
}

// newModel 构造一条属于 provider 的稳定模型记录（ModelKey 稳定，ModelID 是执行 id）。
func newModel(key, modelID string) *llm_provider_model_entity.LLMProviderModel {
	return &llm_provider_model_entity.LLMProviderModel{
		ModelKey: key, ModelID: modelID, Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE,
	}
}

func newAnthropicProvider(key, baseURL string) *llm_provider_entity.LLMProvider {
	return &llm_provider_entity.LLMProvider{
		ProviderKey: key, Type: string(llm_provider_entity.TypeAnthropic), Name: "a",
		DefaultModelKey: "mk-" + key + "-default", APIKey: testAPIKey("anthropic"), BaseURL: baseURL,
		Status: consts.ACTIVE,
	}
}

func newOpenAIResponseProvider(key, baseURL string) *llm_provider_entity.LLMProvider {
	return &llm_provider_entity.LLMProvider{
		ProviderKey: key, Type: string(llm_provider_entity.TypeOpenAIResponse), Name: "r",
		DefaultModelKey: "mk-" + key + "-default", APIKey: testAPIKey("resp"), BaseURL: baseURL,
		Status: consts.ACTIVE,
	}
}

func newOpenAIChatProvider(key, baseURL string) *llm_provider_entity.LLMProvider {
	return &llm_provider_entity.LLMProvider{
		ProviderKey: key, Type: string(llm_provider_entity.TypeOpenAIChat), Name: "c",
		DefaultModelKey: "mk-" + key + "-default", APIKey: testAPIKey("chat"), BaseURL: baseURL,
		Status: consts.ACTIVE,
	}
}

// recordingUpstream 起一个 httptest server 抓所有进来的请求，便于断言 path / headers / body。
type recordedRequest struct {
	Path   string
	Method string
	Header http.Header
	Body   []byte
}

func newRecordingUpstream(t *testing.T, response string) (*httptest.Server, *recordedRequest) {
	t.Helper()
	rec := &recordedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.Path = r.URL.Path
		rec.Method = r.Method
		rec.Header = r.Header.Clone()
		rec.Body = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// issueAndRequest 帮测试发一条带 token 的请求到 forwarder handler。
func issueAndRequest(t *testing.T, h http.HandlerFunc, tokens *TokenRegistry, b *agent_backend_entity.AgentBackend, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	tok, err := tokens.Issue(b, b.LLMProviderKey, "", time.Minute)
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestForwarder_AnthropicHappyPath(t *testing.T) {
	upstream, rec := newRecordingUpstream(t, `{"id":"msg_x","content":[{"type":"text","text":"pong"}]}`)
	tokens := NewTokenRegistry()
	lookup := newFakeLookup(newAnthropicProvider("key-1", upstream.URL))
	lookup.withModel(newModel("mk-key-1-default", "claude-sonnet-4-6"))
	f := NewForwarder(tokens, lookup)

	w := issueAndRequest(t, f.AnthropicHandler(), tokens,
		&agent_backend_entity.AgentBackend{ID: 5, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "key-1"},
		"/v1/messages",
		`{"model":"opus","messages":[{"role":"user","content":"hi"}]}`,
	)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "/v1/messages", rec.Path)
	assert.Equal(t, testAPIKey("anthropic"), rec.Header.Get("x-api-key"))
	assert.Empty(t, rec.Header.Get("Authorization"))

	var body map[string]any
	assert.NoError(t, json.Unmarshal(rec.Body, &body))
	assert.Equal(t, "claude-sonnet-4-6", body["model"]) // model 已按默认模型改写
}

// TestForwarder_ProviderDefaultFollowsCurrentDefault 钉死 spec 决策 8/9：provider-default
// 的 model 在本轮按 Provider 当前默认模型解析 —— Provider 默认模型变化后，同一 token 的
// 下一条请求必须改写为新的默认模型，而不是沿用旧值。
func TestForwarder_ProviderDefaultFollowsCurrentDefault(t *testing.T) {
	upstream, rec := newRecordingUpstream(t, `{"ok":"x"}`)
	provider := newAnthropicProvider("key-1", upstream.URL)
	provider.DefaultModelKey = "mk-a"
	lookup := newFakeLookup(provider)
	lookup.withModel(newModel("mk-a", "claude-sonnet-4-6"))
	lookup.withModel(newModel("mk-b", "claude-sonnet-4-7"))

	tokens := NewTokenRegistry()
	f := NewForwarder(tokens, lookup)
	be := &agent_backend_entity.AgentBackend{ID: 5, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "key-1"}
	tok, err := tokens.Issue(be, "key-1", "", 0)
	assert.NoError(t, err)

	send := func() map[string]any {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"opus"}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		rec2 := httptest.NewRecorder()
		f.AnthropicHandler()(rec2, req)
		assert.Equal(t, http.StatusOK, rec2.Code)
		var body map[string]any
		assert.NoError(t, json.Unmarshal(rec.Body, &body))
		return body
	}

	assert.Equal(t, "claude-sonnet-4-6", send()["model"], "当前默认 mk-a → claude-sonnet-4-6")

	// 管理员把默认模型切到 mk-b：provider-default 下一轮必须动态跟随，不用重签 token。
	provider.DefaultModelKey = "mk-b"
	assert.Equal(t, "claude-sonnet-4-7", send()["model"], "默认切换后同一 token 必须解析到新默认")
}

// TestForwarder_FixedModelRoutesToSpecifiedModel 钉死 spec 决策 9：token 的可变路由目标
// 是 ProviderKey+ModelKey —— 主目标带具体 ModelKey（fixed-model）时，请求必须改写为
// 指定 Model 的 ModelID，而不是 Provider 当前默认模型。
func TestForwarder_FixedModelRoutesToSpecifiedModel(t *testing.T) {
	upstream, rec := newRecordingUpstream(t, `{"ok":"x"}`)
	provider := newAnthropicProvider("key-1", upstream.URL)
	provider.DefaultModelKey = "mk-a"
	lookup := newFakeLookup(provider)
	lookup.withModel(newModel("mk-a", "claude-sonnet-4-6"))
	lookup.withModel(newModel("mk-b", "claude-sonnet-4-7"))

	tokens := NewTokenRegistry()
	f := NewForwarder(tokens, lookup)
	be := &agent_backend_entity.AgentBackend{ID: 5, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "key-1"}
	// 主目标 = fixed-model（providerKey + modelKey），与 provider 默认模型（mk-a）不同。
	tok, err := tokens.Issue(be, "key-1", "mk-b", 0)
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"opus"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec2 := httptest.NewRecorder()
	f.AnthropicHandler()(rec2, req)
	assert.Equal(t, http.StatusOK, rec2.Code)

	var body map[string]any
	assert.NoError(t, json.Unmarshal(rec.Body, &body))
	assert.Equal(t, "claude-sonnet-4-7", body["model"], "fixed-model 必须路由到指定 ModelID，而不是默认模型")
}

func TestForwarder_AliasRoutingPicksTierProvider(t *testing.T) {
	// 主 provider 走 fallback；OPUS alias 路由到另一条 provider，确保用上 tier model。
	mainUpstream, mainRec := newRecordingUpstream(t, `{"ok":"main"}`)
	opusUpstream, opusRec := newRecordingUpstream(t, `{"ok":"opus"}`)
	defer mainUpstream.Close()
	defer opusUpstream.Close()

	tokens := NewTokenRegistry()
	main := newAnthropicProvider("key-1", mainUpstream.URL)
	opus := newAnthropicProvider("key-2", opusUpstream.URL)
	lookup := newFakeLookup(main, opus)
	lookup.withModel(newModel("mk-key-1-default", "claude-sonnet-fallback"))
	lookup.withModel(newModel("mk-key-2-default", "claude-opus-4-1"))
	f := NewForwarder(tokens, lookup)

	w := issueAndRequest(t, f.AnthropicHandler(), tokens,
		&agent_backend_entity.AgentBackend{
			ID: 5, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "key-1",
			ModelRoutes: `{"OPUS":{"providerKey":"key-2"}}`,
		},
		"/v1/messages",
		`{"model":"opus","messages":[]}`,
	)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, testAPIKey("anthropic"), opusRec.Header.Get("x-api-key"))
	var body map[string]any
	assert.NoError(t, json.Unmarshal(opusRec.Body, &body))
	assert.Equal(t, "claude-opus-4-1", body["model"])
	// 主 provider 不应该被调到
	assert.Empty(t, mainRec.Path)
}

func TestForwarder_OpenAIResponses(t *testing.T) {
	assertOpenAIForwarded(t,
		newOpenAIResponseProvider("key-1", ""),
		[]*llm_provider_model_entity.LLMProviderModel{newModel("mk-key-1-default", "gpt-5-codex")},
		(*Forwarder).OpenAIResponsesHandler,
		6,
		"/v1/responses",
		`{"model":"gpt-5","input":"hi"}`,
		"resp",
	)
}

func TestForwarder_OpenAIChat(t *testing.T) {
	assertOpenAIForwarded(t,
		newOpenAIChatProvider("key-1", ""),
		[]*llm_provider_model_entity.LLMProviderModel{newModel("mk-key-1-default", "gpt-4o")},
		(*Forwarder).OpenAIChatHandler,
		7,
		"/v1/chat/completions",
		`{"model":"gpt-4o","messages":[]}`,
		"chat",
	)
}

func TestForwarder_RejectsProviderTypeMismatch(t *testing.T) {
	upstream, _ := newRecordingUpstream(t, `{}`)
	tokens := NewTokenRegistry()
	// /v1/messages handler 但 provider 是 openai-chat → 400
	lookup := newFakeLookup(newOpenAIChatProvider("key-1", upstream.URL))
	f := NewForwarder(tokens, lookup)

	w := issueAndRequest(t, f.AnthropicHandler(), tokens,
		&agent_backend_entity.AgentBackend{ID: 1, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "key-1"},
		"/v1/messages",
		`{"model":"opus"}`,
	)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	assert.Contains(t, body["error"], "type mismatch")
}

// TestForwarder_SwitchedProviderRoutesSameTokenToNewUpstream 钉死会话切换供应商的转发
// 侧（规格「网关路由与子进程重启」）：token 字符串整段会话不变（它已烤进 CLI 子进程
// env），换供应商改的只是它的路由目标 —— 同一个 token 的下一条请求必须打到新供应商的
// BaseURL、带新供应商的 APIKey、body 的 model 改写成新供应商的默认模型。
func TestForwarder_SwitchedProviderRoutesSameTokenToNewUpstream(t *testing.T) {
	oldUpstream, oldRec := newRecordingUpstream(t, `{"ok":"old"}`)
	newUpstream, newRec := newRecordingUpstream(t, `{"ok":"new"}`)

	oldProvider := newAnthropicProvider("key-old", oldUpstream.URL)
	switched := newAnthropicProvider("key-new", newUpstream.URL)
	switched.APIKey = "k-switched"

	tokens := NewTokenRegistry()
	lookup := newFakeLookup(oldProvider, switched)
	lookup.withModel(newModel("mk-key-old-default", "claude-old"))
	lookup.withModel(newModel("mk-key-new-default", "claude-new"))
	f := NewForwarder(tokens, lookup)
	be := &agent_backend_entity.AgentBackend{
		ID: 5, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "key-old",
	}
	tok, err := tokens.Issue(be, "key-old", "", 0)
	assert.NoError(t, err)

	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages",
			strings.NewReader(`{"model":"whatever","messages":[]}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		f.AnthropicHandler()(rec, req)
		return rec
	}

	assert.Equal(t, http.StatusOK, send().Code)
	assert.Equal(t, "/v1/messages", oldRec.Path)

	prev, ok := tokens.SetTokenTarget(tok, "key-new", "")
	assert.True(t, ok)
	assert.Equal(t, "key-old", prev)

	assert.Equal(t, http.StatusOK, send().Code, "token 字符串没变，仍然有效")
	assert.Equal(t, "/v1/messages", newRec.Path, "切换后的请求打到新供应商")
	assert.Equal(t, "k-switched", newRec.Header.Get("x-api-key"), "用新供应商的 APIKey")
	var body map[string]any
	assert.NoError(t, json.Unmarshal(newRec.Body, &body))
	assert.Equal(t, "claude-new", body["model"], "model 改写成新供应商的默认模型")
}

// TestForwarder_SwitchedToMissingProviderKeeps502 切换目标在本机缺失/停用时，转发端点
// 维持既有的 502（规格「网关路由与子进程重启」）：不静默回落旧供应商 —— 那会让用户以为
// 换成功了、实际还在老那家上跑。
func TestForwarder_SwitchedToMissingProviderKeeps502(t *testing.T) {
	upstream, _ := newRecordingUpstream(t, `{"ok":"old"}`)
	tokens := NewTokenRegistry()
	f := NewForwarder(tokens, newFakeLookup(newAnthropicProvider("key-old", upstream.URL)))

	tok, err := tokens.Issue(
		&agent_backend_entity.AgentBackend{ID: 5, Type: string(agent_backend_entity.TypeClaudeCode)},
		"key-old", "", 0)
	assert.NoError(t, err)
	_, ok := tokens.SetTokenTarget(tok, "key-gone", "")
	assert.True(t, ok)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"x"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	f.AnthropicHandler()(rec, req)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestForwarder_MissingTokenReturns401(t *testing.T) {
	tokens := NewTokenRegistry()
	lookup := newFakeLookup()
	f := NewForwarder(tokens, lookup)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	f.AnthropicHandler()(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestForwarder_UnknownTokenReturns401(t *testing.T) {
	tokens := NewTokenRegistry()
	lookup := newFakeLookup()
	f := NewForwarder(tokens, lookup)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer nope")
	w := httptest.NewRecorder()
	f.AnthropicHandler()(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestForwarder_ProviderDefaultMissingModelKeeps502 钉死决策 7 的网关侧：Provider 存在
// 但没有合法启用的默认模型（配置损坏）时转发端点 502，不静默把 alias 原样透传。
func TestForwarder_ProviderDefaultMissingModelKeeps502(t *testing.T) {
	upstream, _ := newRecordingUpstream(t, `{"ok":"x"}`)
	provider := newAnthropicProvider("key-1", upstream.URL)
	provider.DefaultModelKey = "mk-gone"
	tokens := NewTokenRegistry()
	f := NewForwarder(tokens, newFakeLookup(provider))

	tok, err := tokens.Issue(
		&agent_backend_entity.AgentBackend{ID: 5, Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "key-1"},
		"key-1", "", 0)
	assert.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"opus"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	f.AnthropicHandler()(rec, req)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestBuildTargetURL(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		path    string
		want    string
	}{
		{"plain host", "https://api.anthropic.com", "/v1/messages", "https://api.anthropic.com/v1/messages"},
		{"trailing slash", "https://api.anthropic.com/", "/v1/messages", "https://api.anthropic.com/v1/messages"},
		{"with /v1", "https://api.anthropic.com/v1", "/v1/messages", "https://api.anthropic.com/v1/messages"},
		{"with /v1/ trailing", "https://api.anthropic.com/v1/", "/v1/messages", "https://api.anthropic.com/v1/messages"},
		{"openai responses", "https://api.openai.com/v1", "/v1/responses", "https://api.openai.com/v1/responses"},
		{"openai chat", "https://api.openai.com", "/v1/chat/completions", "https://api.openai.com/v1/chat/completions"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := buildTargetURL(tc.baseURL, tc.path, llm_provider_entity.TypeAnthropic)
			assert.NoError(t, err)
			if assert.NotNil(t, u) {
				assert.Equal(t, tc.want, u.String())
			}
		})
	}
}

func TestBuildTargetURL_RejectsEmpty(t *testing.T) {
	_, err := buildTargetURL("", "/v1/messages", llm_provider_entity.TypeAnthropic)
	assert.Error(t, err)
}

func TestRewriteModelField(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		model    string
		wantJSON string
	}{
		{"sets model", `{"model":"opus","messages":[]}`, "claude-sonnet-4-6", `{"messages":[],"model":"claude-sonnet-4-6"}`},
		{"empty body passthrough", "", "x", ""},
		{"empty newModel passthrough", `{"model":"foo"}`, "", `{"model":"foo"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := rewriteModelField([]byte(tc.body), tc.model)
			assert.NoError(t, err)
			// 字段顺序 unstable，用解析后比较；空字符串走原样断言
			if tc.wantJSON == "" {
				assert.Empty(t, string(out))
				return
			}
			if tc.model == "" || tc.body == "" {
				assert.Equal(t, tc.wantJSON, string(out))
				return
			}
			var got, want map[string]any
			assert.NoError(t, json.Unmarshal(out, &got))
			assert.NoError(t, json.Unmarshal([]byte(tc.wantJSON), &want))
			assert.Equal(t, want, got)
		})
	}
}

func TestExtractBearerOrAPIKey(t *testing.T) {
	cases := []struct {
		name string
		set  map[string]string
		want string
	}{
		{"bearer", map[string]string{"Authorization": "Bearer tok123"}, "tok123"},
		{"plain auth", map[string]string{"Authorization": "tok"}, "tok"},
		{"x-api-key", map[string]string{"X-Api-Key": "abc"}, "abc"},
		{"none", map[string]string{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for k, v := range tc.set {
				req.Header.Set(k, v)
			}
			assert.Equal(t, tc.want, extractBearerOrAPIKey(req))
		})
	}
}

// ensure url package import isn't dropped if test set shrinks
var _ = url.Parse

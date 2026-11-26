package httpgateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
)

const geminiSessionPath = "/v1beta/models/gemini-3.1-pro-preview:streamGenerateContent"

func newGeminiProvider(key, baseURL string) *llm_provider_entity.LLMProvider {
	return &llm_provider_entity.LLMProvider{
		ProviderKey: key, Type: string(llm_provider_entity.TypeGemini), Name: "g",
		DefaultModelKey: "mk-" + key + "-default", APIKey: testAPIKey("gemini"), BaseURL: baseURL,
		Status: consts.ACTIVE,
	}
}

func issueGeminiToken(t *testing.T, tokens *TokenRegistry, providerKey string) string {
	t.Helper()
	tok, err := tokens.Issue(&agent_backend_entity.AgentBackend{ID: 9, LLMProviderKey: providerKey}, providerKey, "", time.Minute)
	require.NoError(t, err)
	return tok
}

func TestForwarder_Gemini_RewritesKeyQueryAndPathModel(t *testing.T) {
	var gotURL string
	var gotHeader http.Header
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.RequestURI()
		gotHeader = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	tokens := NewTokenRegistry()
	lookup := newFakeLookup(newGeminiProvider("g1", upstream.URL))
	lookup.withModel(newModel("mk-g1-default", "gemini-bound"))
	f := NewForwarder(tokens, lookup)
	tok := issueGeminiToken(t, tokens, "g1")

	cases := map[string]func(r *http.Request){
		"header": func(r *http.Request) { r.Header.Set("X-Goog-Api-Key", tok) },
		"query": func(r *http.Request) {
			q := r.URL.Query()
			q.Set("key", tok)
			r.URL.RawQuery = q.Encode()
		},
	}
	for name, setAuth := range cases {
		t.Run(name, func(t *testing.T) {
			// 主模型与旁路模型都必须被改写成 token 绑定的模型。
			for _, path := range []string{geminiSessionPath, "/v1beta/models/gemini-3.1-flash-lite-preview:streamGenerateContent"} {
				req := httptest.NewRequest(http.MethodPost, path+"?alt=sse", strings.NewReader(`{"contents":[{"parts":[{"text":"hi"}]}],"model":"keep"}`))
				setAuth(req)
				w := httptest.NewRecorder()
				f.GeminiHandler()(w, req)

				assert.Equal(t, http.StatusOK, w.Code)
				assert.Equal(t, "/v1beta/models/gemini-bound:streamGenerateContent?alt=sse", gotURL)
				assert.Equal(t, testAPIKey("gemini"), gotHeader.Get("x-goog-api-key"))
				assert.NotContains(t, gotURL, "key=")
				assert.Empty(t, gotHeader.Get("Authorization"))
				assert.JSONEq(t, `{"contents":[{"parts":[{"text":"hi"}]}],"model":"keep"}`, string(gotBody), "请求体原样透传")
			}
		})
	}
}

func TestForwarder_Gemini_InvalidTokenIs401WithoutUpstream(t *testing.T) {
	hit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	t.Cleanup(upstream.Close)
	f := NewForwarder(NewTokenRegistry(), newFakeLookup(newGeminiProvider("g1", upstream.URL)))

	for name, mk := range map[string]func() *http.Request{
		"missing": func() *http.Request { return httptest.NewRequest(http.MethodPost, geminiSessionPath, nil) },
		"bad header": func() *http.Request {
			r := httptest.NewRequest(http.MethodPost, geminiSessionPath, nil)
			r.Header.Set("x-goog-api-key", "nope")
			return r
		},
		"bad query": func() *http.Request {
			return httptest.NewRequest(http.MethodPost, geminiSessionPath+"?key=nope", nil)
		},
	} {
		w := httptest.NewRecorder()
		f.GeminiHandler()(w, mk())
		assert.Equal(t, http.StatusUnauthorized, w.Code, name)
	}
	assert.False(t, hit)
}

func TestForwarder_Gemini_ProviderTypeMismatchIs400(t *testing.T) {
	tokens := NewTokenRegistry()
	f := NewForwarder(tokens, newFakeLookup(newAnthropicProvider("a1", "http://127.0.0.1:1")))
	req := httptest.NewRequest(http.MethodPost, geminiSessionPath, nil)
	req.Header.Set("x-goog-api-key", issueGeminiToken(t, tokens, "a1"))
	w := httptest.NewRecorder()
	f.GeminiHandler()(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestForwarder_Gemini_UpstreamErrorPassesThrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":429,"message":"quota"}}`))
	}))
	t.Cleanup(upstream.Close)
	tokens := NewTokenRegistry()
	lookup := newFakeLookup(newGeminiProvider("g1", upstream.URL))
	lookup.withModel(newModel("mk-g1-default", "gemini-bound"))
	f := NewForwarder(tokens, lookup)
	req := httptest.NewRequest(http.MethodPost, geminiSessionPath, strings.NewReader(`{}`))
	req.Header.Set("x-goog-api-key", issueGeminiToken(t, tokens, "g1"))
	w := httptest.NewRecorder()
	f.GeminiHandler()(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.JSONEq(t, `{"error":{"code":429,"message":"quota"}}`, w.Body.String())
}

func TestForwarder_Gemini_StreamsSSEChunkByChunk(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: one\n\n"))
		w.(http.Flusher).Flush()
		<-release // 第二块要等测试确认收到第一块后才放行
		_, _ = w.Write([]byte("data: two\n\n"))
	}))
	t.Cleanup(upstream.Close)
	tokens := NewTokenRegistry()
	lookup := newFakeLookup(newGeminiProvider("g1", upstream.URL))
	lookup.withModel(newModel("mk-g1-default", "gemini-bound"))
	gw := httptest.NewServer(NewForwarder(tokens, lookup).GeminiHandler())
	t.Cleanup(gw.Close)

	req, _ := http.NewRequest(http.MethodPost, gw.URL+geminiSessionPath+"?alt=sse", strings.NewReader(`{}`))
	req.Header.Set("x-goog-api-key", issueGeminiToken(t, tokens, "g1"))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	buf := make([]byte, len("data: one\n\n"))
	_, err = io.ReadFull(resp.Body, buf)
	require.NoError(t, err, "第一块必须在上游结束前到达")
	assert.Equal(t, "data: one\n\n", string(buf))
	close(release)
	rest, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "data: two\n\n", string(rest))
}

// 规格「路径里的模型名一律改写成这个 token 绑定的模型」：Gemini 路由没有 CLI --model
// 兜底，解析不出绑定模型（lookup 不具备 FindModelByKey、模型不存在 / 已停用）一律 502，
// 绝不把 agy 的内置模型原样透传到上游。
func TestForwarder_Gemini_ModelResolutionEdgeCases(t *testing.T) {
	hit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	t.Cleanup(upstream.Close)

	t.Run("lookup without ModelLookup is 502, path model never forwarded", func(t *testing.T) {
		tokens := NewTokenRegistry()
		f := NewForwarder(tokens, providerOnlyLookup{newGeminiProvider("g1", upstream.URL)})
		req := httptest.NewRequest(http.MethodPost, geminiSessionPath, nil)
		req.Header.Set("x-goog-api-key", issueGeminiToken(t, tokens, "g1"))
		w := httptest.NewRecorder()
		f.GeminiHandler()(w, req)
		assert.Equal(t, http.StatusBadGateway, w.Code)
		assert.False(t, hit, "解析不出绑定模型时不访问上游")
	})
	t.Run("model not found is 502", func(t *testing.T) {
		tokens := NewTokenRegistry()
		f := NewForwarder(tokens, newFakeLookup(newGeminiProvider("g1", upstream.URL)))
		req := httptest.NewRequest(http.MethodPost, geminiSessionPath, nil)
		req.Header.Set("x-goog-api-key", issueGeminiToken(t, tokens, "g1"))
		w := httptest.NewRecorder()
		f.GeminiHandler()(w, req)
		assert.Equal(t, http.StatusBadGateway, w.Code)
	})
}

// 规格「匹配 POST /v1beta/models/{model}:{method}」：其它方法不转发，带着真实 key 的
// 上游请求只为 POST 发出。
func TestForwarder_Gemini_NonPostIs405WithoutUpstream(t *testing.T) {
	hit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	t.Cleanup(upstream.Close)
	tokens := NewTokenRegistry()
	lookup := newFakeLookup(newGeminiProvider("g1", upstream.URL))
	lookup.withModel(newModel("mk-g1-default", "gemini-bound"))
	f := NewForwarder(tokens, lookup)
	tok := issueGeminiToken(t, tokens, "g1")
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, geminiSessionPath, nil)
		req.Header.Set("x-goog-api-key", tok)
		w := httptest.NewRecorder()
		f.GeminiHandler()(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code, method)
	}
	assert.False(t, hit)
}

func TestForwarder_Gemini_MalformedPathIs404(t *testing.T) {
	tokens := NewTokenRegistry()
	f := NewForwarder(tokens, newFakeLookup(newGeminiProvider("g1", "http://127.0.0.1:1")))
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/no-method-here", nil)
	req.Header.Set("x-goog-api-key", issueGeminiToken(t, tokens, "g1"))
	w := httptest.NewRecorder()
	f.GeminiHandler()(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGateway_MountsGeminiRoute(t *testing.T) {
	g := New("127.0.0.1", 0, newFakeLookup())
	req := httptest.NewRequest(http.MethodPost, geminiSessionPath, nil)
	w := httptest.NewRecorder()
	g.mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code, "路由已挂载：到 handler 才会因缺 token 返 401，而不是 mux 404")
}

type providerOnlyLookup struct {
	p *llm_provider_entity.LLMProvider
}

func (l providerOnlyLookup) FindByKey(context.Context, string) (*llm_provider_entity.LLMProvider, error) {
	if l.p == nil {
		return nil, errors.New("none")
	}
	return l.p, nil
}

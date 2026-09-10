package httpgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/pkg/llmurl"
)

// ProviderLookup 抽象 llm_provider 仓储依赖，方便单测注入 mock。
type ProviderLookup interface {
	FindByKey(ctx context.Context, key string) (*llm_provider_entity.LLMProvider, error)
}

// ModelLookup 是 ProviderLookup 的可选扩展：能按稳定 model_key 解析真实 Model 记录。
//
// 桌面端注入的 llm_provider_repo.LLMProvider() 天然实现它，因此本地网关能在请求时
// 解析 provider-default 的当前默认模型 / fixed-model 的指定模型（决策 8/9：provider-default
// 必须本轮解析当前默认）。旧实现（daemon 侧 state-backed lookup）未实现时，网关退化为
// 不重写 body —— 由 CLI 侧 --model 已带解析结果兜底；daemon 的模型解析能力由远端任务补齐。
type ModelLookup interface {
	FindModelByKey(ctx context.Context, modelKey string) (*llm_provider_model_entity.LLMProviderModel, error)
}

// Forwarder 单实例承担三条路由的 HTTP 转发；类型在 mux 装配阶段绑死，避免每次请求重判。
type Forwarder struct {
	tokens *TokenRegistry
	lookup ProviderLookup
}

// NewForwarder 构造转发器。
func NewForwarder(tokens *TokenRegistry, lookup ProviderLookup) *Forwarder {
	return &Forwarder{tokens: tokens, lookup: lookup}
}

// Tokens 返回转发器持有的 token registry。
func (f *Forwarder) Tokens() *TokenRegistry { return f.tokens }

// AnthropicHandler /v1/messages → 严格匹配 type=anthropic。
func (f *Forwarder) AnthropicHandler() http.HandlerFunc {
	return f.handle(llm_provider_entity.TypeAnthropic)
}

// OpenAIResponsesHandler /v1/responses → 严格匹配 type=openai-response（codex 默认）。
func (f *Forwarder) OpenAIResponsesHandler() http.HandlerFunc {
	return f.handle(llm_provider_entity.TypeOpenAIResponse)
}

// OpenAIChatHandler /v1/chat/completions → 严格匹配 type=openai-chat（codex wire_api=chat）。
func (f *Forwarder) OpenAIChatHandler() http.HandlerFunc {
	return f.handle(llm_provider_entity.TypeOpenAIChat)
}

// handle 是统一转发逻辑：鉴权 → 模型路由 → 严格匹配 provider type → body 改写 →
// httputil.ReverseProxy 透传上游。
func (f *Forwarder) handle(expected llm_provider_entity.ProviderType) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractBearerOrAPIKey(r)
		if token == "" {
			writeJSONError(w, http.StatusUnauthorized, "missing token")
			return
		}
		entry, ok := f.tokens.Resolve(token)
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}

		// 读 body 一次。SSE 请求体很小，全量读入内存可接受。
		body, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "read body: "+err.Error())
			return
		}

		modelField := extractModelField(body)
		rt, _ := entry.ResolveModel(modelField)

		provider, err := f.lookup.FindByKey(r.Context(), rt.ProviderKey)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "lookup provider: "+err.Error())
			return
		}
		if provider == nil || !provider.IsActive() {
			writeJSONError(w, http.StatusBadGateway, "provider missing or inactive")
			return
		}
		if llm_provider_entity.ProviderType(provider.Type) != expected {
			writeJSONError(w, http.StatusBadRequest, "provider type mismatch")
			return
		}

		modelID, err := f.resolveModelID(r.Context(), provider, rt)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "resolve model: "+err.Error())
			return
		}
		rewritten, err := rewriteModelField(body, modelID)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "rewrite model: "+err.Error())
			return
		}

		target, err := buildTargetURL(provider.BaseURL, r.URL.Path, expected)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "build upstream URL: "+err.Error())
			return
		}

		proxy := &httputil.ReverseProxy{
			FlushInterval: -1, // SSE 立刻 flush
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(target)
				pr.Out.URL.Path = target.Path
				pr.Out.URL.RawQuery = r.URL.RawQuery
				pr.Out.Host = target.Host
				// 去掉子进程带来的认证头，按上游协议补正确的。
				pr.Out.Header.Del("Authorization")
				pr.Out.Header.Del("X-Api-Key")
				pr.Out.Header.Del("x-api-key")
				applyUpstreamAuth(pr.Out.Header, expected, provider.APIKey)
				pr.Out.Header.Set("Content-Type", "application/json")
				pr.Out.Body = io.NopCloser(bytes.NewReader(rewritten))
				pr.Out.ContentLength = int64(len(rewritten))
			},
		}
		proxy.ServeHTTP(w, r)
	}
}

// resolveModelID 解析 token target 对应的真实 ModelID（供 body 模型改写）。
//
//   - target.ModelKey 非空（fixed-model）：按指定 model_key 解析；
//   - target.ModelKey 空（provider-default）：按 Provider 当前默认模型解析（决策 9：
//     必须本轮解析当前默认，不能沿用旧快照）；
//   - 解析不到/模型停用 → error（网关 502，不静默降级）。
//
// 无 ModelLookup 能力的 lookup（旧 daemon）返回空串（不重写 body，由 CLI --model 兜底）。
func (f *Forwarder) resolveModelID(ctx context.Context, provider *llm_provider_entity.LLMProvider, target TokenTarget) (string, error) {
	ml, ok := f.lookup.(ModelLookup)
	if !ok {
		return "", nil
	}
	modelKey := target.ModelKey
	if strings.TrimSpace(modelKey) == "" {
		modelKey = provider.DefaultModelKey
	}
	if strings.TrimSpace(modelKey) == "" {
		return "", fmt.Errorf("provider %q has no default model", provider.ProviderKey)
	}
	m, err := ml.FindModelByKey(ctx, modelKey)
	if err != nil {
		return "", err
	}
	if m == nil {
		return "", fmt.Errorf("model %q not found", modelKey)
	}
	if !m.IsEnabled() {
		return "", fmt.Errorf("model %q disabled", modelKey)
	}
	return strings.TrimSpace(m.ModelID), nil
}

// extractBearerOrAPIKey 兼容 OpenAI（Authorization: Bearer xxx）与 Anthropic（x-api-key: xxx）两种鉴权头。
func extractBearerOrAPIKey(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("Authorization")); v != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(v, prefix) {
			return strings.TrimSpace(v[len(prefix):])
		}
		return v
	}
	if v := strings.TrimSpace(r.Header.Get("x-api-key")); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.Header.Get("X-Api-Key")); v != "" {
		return v
	}
	return ""
}

// applyUpstreamAuth 按目标 provider 类型补正确的鉴权头。
func applyUpstreamAuth(h http.Header, t llm_provider_entity.ProviderType, key string) {
	switch t {
	case llm_provider_entity.TypeAnthropic:
		h.Set("x-api-key", key)
		// 保留 client 端发的 anthropic-version 头（如有）。
		if h.Get("anthropic-version") == "" {
			h.Set("anthropic-version", "2023-06-01")
		}
	default:
		h.Set("Authorization", "Bearer "+key)
	}
}

// extractModelField 从 body 里抓 "model" 字段；解析失败或缺字段返空串（→ 走主 provider）。
func extractModelField(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var probe struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return ""
	}
	return probe.Model
}

// rewriteModelField 把 body 里的 "model" 字段改写成目标 provider 的真实 model id。
// 空 newModel 时不改 body。其它字段全部保留。
func rewriteModelField(body []byte, newModel string) ([]byte, error) {
	if len(body) == 0 || strings.TrimSpace(newModel) == "" {
		return body, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		// body 不是 JSON 对象，原样转发。
		return body, nil //nolint:nilerr
	}
	obj["model"] = newModel
	return json.Marshal(obj)
}

// buildTargetURL 根据 provider.BaseURL + 请求路径拼上游 URL。
//
// BaseURL 形态都接受：
//   - "https://api.anthropic.com"          → + "/v1/messages" → "https://api.anthropic.com/v1/messages"
//   - "https://api.anthropic.com/v1"       → 同上
//   - "https://api.anthropic.com/v1/"      → 同上
//
// 类型兜底：openai-chat 走 chat/completions，openai-response 走 responses；
// anthropic 仅识别 /v1/messages；若 BaseURL 已带 /v1 后缀会被剥掉再拼，避免重复。
func buildTargetURL(baseURL, path string, _ llm_provider_entity.ProviderType) (*url.URL, error) {
	return llmurl.Build(baseURL, path)
}

// writeJSONError 输出 `{"error":"..."}` 的 JSON 错误响应。
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

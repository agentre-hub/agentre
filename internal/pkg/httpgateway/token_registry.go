package httpgateway

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

// TokenTarget 是 token 的可变路由目标（spec：ProviderKey + ModelKey）。
//
//   - ProviderKey 非空且 ModelKey 空 → provider-default：每轮由 Gateway 解析 Provider 当前默认模型；
//   - 两个 key 都非空 → fixed-model：解析指定 Model 记录（由后续任务扩展）。
type TokenTarget struct {
	ProviderKey string
	ModelKey    string
}

// TokenEntry 一条 token → backend 路由记录。
//
// 持有 backend 的 ID / 类型 / 主路由目标 / model_routes 解析后的快照；
// 不持有 provider 实体本身——转发时由 llmforward 通过 llm_provider_repo 查实时数据。
type TokenEntry struct {
	BackendID   int64
	BackendType agent_backend_entity.BackendType
	// Main 是主路由目标（会话级常驻 token 的可变部分：ProviderKey+ModelKey）。
	Main TokenTarget
	// Routes 把 alias（OPUS / SONNET / HAIKU 等，**统一大写**）映射到路由目标。
	// 空 map 表示没有 tier 路由（codex / 没配 model_routes 的 claudecode）。
	Routes   map[string]TokenTarget
	ExpireAt time.Time // 0 = 永不过期（chat flow 长 token 用）
}

// IsExpired 当前是否已过期。0 时区时间视作未过期。
func (e TokenEntry) IsExpired(now time.Time) bool {
	if e.ExpireAt.IsZero() {
		return false
	}
	return !now.Before(e.ExpireAt)
}

// ResolveModel 在 routes 里找 alias 对应的路由目标；没命中返回主目标。
// alias 比较前会先转大写——子进程发的请求 body 里 model 字段可能是 "opus" 等小写。
func (e TokenEntry) ResolveModel(modelField string) (TokenTarget, bool) {
	if len(e.Routes) == 0 {
		return e.Main, false
	}
	if t, ok := e.Routes[strings.ToUpper(strings.TrimSpace(modelField))]; ok {
		return t, true
	}
	return e.Main, false
}

// TokenRegistry 内存 token 表。App 退出即清空；不落盘。
//
// 并发模型：RWMutex；Resolve 读锁、Issue/Revoke 写锁；过期 entry 在 Resolve 命中时
// 顺手删除（lazy expire），不启动后台 sweep goroutine。
type TokenRegistry struct {
	mu     sync.RWMutex
	tokens map[string]TokenEntry
	now    func() time.Time
}

// NewTokenRegistry 构造空 registry。
func NewTokenRegistry() *TokenRegistry {
	return &TokenRegistry{
		tokens: make(map[string]TokenEntry),
		now:    time.Now,
	}
}

// ErrInvalidBackend 内部哨兵：Issue 传 nil 时返回。
var ErrInvalidBackend = errors.New("httpgateway: invalid backend for token issue")

// Issue 把 backend 转成 TokenEntry 并存入表，返回随机 token 字符串。
// ttl <= 0 时视为永久（chat flow 长 token 用；TestAgentBackend 传 60s）。
//
// providerKey 是这条 token 的**主供应商**，由调用方按自己的口径给出：chat flow 传本轮
// 的 effective provider（会话 provider_key > agent 绑定，spec 决策 2/3），backend 自测 /
// 探针传 backend 自身绑定。registry 不再自己从 backend 派生 —— 各处各读各的正是「会话
// 选了供应商却打到 agent 绑定那家」的成因。
//
// providerKey == ""（CLI 登录模式，没绑 provider）也允许发 token：
// 这种 token 在 /hook/v1/inbox 上正常用（gateway handler 只 Resolve 不看
// provider），LLM 转发端点会因 ResolveModel→"" 找不到 provider 自然 502，
// 互不干扰。**不允许**会让 hook 子进程在 CLI 登录模式下永远拿不到 token，
// 排队消息没法 mid-turn 注入。
func (r *TokenRegistry) Issue(b *agent_backend_entity.AgentBackend, providerKey, modelKey string, ttl time.Duration) (string, error) {
	if b == nil {
		return "", ErrInvalidBackend
	}
	routes, err := agent_backend_entity.ParseModelRoutes(b.ModelRoutes)
	if err != nil {
		return "", err
	}
	upper := make(map[string]TokenTarget, len(routes))
	for k, v := range routes {
		upper[strings.ToUpper(k)] = TokenTarget{ProviderKey: v.ProviderKey, ModelKey: v.ModelKey}
	}

	tok, err := RandomToken(24)
	if err != nil {
		return "", err
	}
	entry := TokenEntry{
		BackendID:   b.ID,
		BackendType: agent_backend_entity.BackendType(b.Type),
		Main:        TokenTarget{ProviderKey: providerKey, ModelKey: modelKey},
		Routes:      upper,
	}
	if ttl > 0 {
		entry.ExpireAt = r.now().Add(ttl)
	}

	r.mu.Lock()
	r.tokens[tok] = entry
	r.mu.Unlock()
	return tok, nil
}

// Resolve 按 token 查 entry；命中但已过期则原地删并返回 (zero, false)。
func (r *TokenRegistry) Resolve(token string) (TokenEntry, bool) {
	r.mu.RLock()
	entry, ok := r.tokens[token]
	r.mu.RUnlock()
	if !ok {
		return TokenEntry{}, false
	}
	if entry.IsExpired(r.now()) {
		r.mu.Lock()
		// double check 避免并发 issue 同 token 删错
		if cur, ok2 := r.tokens[token]; ok2 && cur.IsExpired(r.now()) {
			delete(r.tokens, token)
		}
		r.mu.Unlock()
		return TokenEntry{}, false
	}
	return entry, true
}

// SetTokenTarget 把既有 token 的主路由目标改成 providerKey + modelKey
// （ModelTarget 契约，spec 2026-08-11 决策 9），**token 字符串不变**，返回它原来的
// 主供应商与是否命中（未签发过 / 已过期 → ("", false)）。
//
// 会话中途换 target 走这里而不是重签：token 是会话级常驻、首轮就烤进 CLI 子进程 env 的，
// 重签会让在跑的子进程手里那个立刻失效（曾经的 401 事故）。tier 路由（Routes）与 backend
// 身份不动 —— 换的只是主路由目标这一件事。
func (r *TokenRegistry) SetTokenTarget(token, providerKey, modelKey string) (previous string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, found := r.tokens[token]
	if !found || entry.IsExpired(r.now()) {
		return "", false
	}
	previous = entry.Main.ProviderKey
	entry.Main = TokenTarget{ProviderKey: providerKey, ModelKey: modelKey}
	r.tokens[token] = entry
	return previous, true
}

// Revoke 删除 token；找不到忽略。
func (r *TokenRegistry) Revoke(token string) {
	r.mu.Lock()
	delete(r.tokens, token)
	r.mu.Unlock()
}

// Size 返回当前 token 数量。
func (r *TokenRegistry) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tokens)
}

// RandomToken 生成 n 字节随机 token 的 hex 编码字符串。
func RandomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

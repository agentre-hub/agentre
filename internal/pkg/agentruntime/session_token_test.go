package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

// fakeTokenRouter 记账式假网关：只记录签发 / 改道 / 撤销三类调用，供断言「签一次、
// 改道不重签、撤销即失效」。
type fakeTokenRouter struct {
	mu sync.Mutex

	issued   []issuedToken
	routed   []routedTarget
	revoked  []string
	nextTok  string
	issueErr error
	// missing 为真时 SetTokenTarget 报「entry 不在」（gateway 重启过）。
	missing bool
	// previous 是 SetTokenTarget 返回的上一个 providerKey。
	previous string
	// beforeStore 在 IssueTokenFor 返回后、调用方 LoadOrStore 之前执行，用来制造并发首轮。
	beforeStore func()
}

type issuedToken struct {
	providerKey string
	modelKey    string
	ttl         time.Duration
	backend     *agent_backend_entity.AgentBackend
}

type routedTarget struct {
	token       string
	providerKey string
	modelKey    string
}

func (f *fakeTokenRouter) IssueTokenFor(
	_ context.Context, b *agent_backend_entity.AgentBackend, providerKey, modelKey string, ttl time.Duration,
) (string, error) {
	f.mu.Lock()
	f.issued = append(f.issued, issuedToken{providerKey: providerKey, modelKey: modelKey, ttl: ttl, backend: b})
	tok, err := f.nextTok, f.issueErr
	if tok == "" && err == nil {
		tok = fmt.Sprintf("tok-%d", len(f.issued))
	}
	hook := f.beforeStore
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return tok, err
}

func (f *fakeTokenRouter) SetTokenTarget(token, providerKey, modelKey string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routed = append(f.routed, routedTarget{token: token, providerKey: providerKey, modelKey: modelKey})
	if f.missing {
		return "", false
	}
	return f.previous, true
}

func (f *fakeTokenRouter) RevokeToken(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, token)
}

func (f *fakeTokenRouter) snapshot() ([]issuedToken, []routedTarget, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]issuedToken(nil), f.issued...), append([]routedTarget(nil), f.routed...),
		append([]string(nil), f.revoked...)
}

func newTestCache(r SessionTokenRouter) *SessionTokenCache {
	return NewSessionTokenCache("test.sessionToken", func() SessionTokenRouter { return r })
}

// TestSessionTokenCache_IssuesOncePerSessionAndReusesAcrossTurns 钉死本缝的核心不变量：
// 同一 session 跨轮拿到**同一个永久(ttl=0)token**。token 首轮就烤进 CLI 子进程 env，
// 重签等于让在跑的子进程立刻 401（PostToolUse hook 撞 401、steer 整轮 drain 不到）。
func TestSessionTokenCache_IssuesOncePerSessionAndReusesAcrossTurns(t *testing.T) {
	r := &fakeTokenRouter{nextTok: "sess-token", previous: "pk"}
	c := newTestCache(r)
	be := &agent_backend_entity.AgentBackend{ID: 3}

	first, err := c.EnsureToken(context.Background(), 42, be, "pk", "")
	require.NoError(t, err)
	second, err := c.EnsureToken(context.Background(), 42, be, "pk", "")
	require.NoError(t, err)

	assert.Equal(t, "sess-token", first)
	assert.Equal(t, "sess-token", second, "第二轮必须复用同一个 token 字符串")
	issued, routed, revoked := r.snapshot()
	require.Len(t, issued, 1, "每会话只签一次")
	assert.Equal(t, time.Duration(0), issued[0].ttl, "会话常驻 token 必须是永久(ttl=0)")
	assert.Equal(t, be, issued[0].backend)
	assert.Equal(t, []routedTarget{{token: "sess-token", providerKey: "pk", modelKey: ""}}, routed,
		"复用轮把既有 token 的路由目标对齐到本轮目标")
	assert.Empty(t, revoked, "轮末不得撤销 —— 撤了下一轮复用的子进程就 401")
}

// TestSessionTokenCache_SwitchReroutesWithoutReissuing 钉死改道语义：会话中途换供应商
// （或换固定模型）时改既有 token 的路由目标，token 字符串不变、不重签。
func TestSessionTokenCache_SwitchReroutesWithoutReissuing(t *testing.T) {
	r := &fakeTokenRouter{nextTok: "sess-token", previous: "first-key"}
	c := newTestCache(r)

	_, err := c.EnsureToken(context.Background(), 7, nil, "first-key", "")
	require.NoError(t, err)
	tok, err := c.EnsureToken(context.Background(), 7, nil, "switched-key", "model-b")
	require.NoError(t, err)

	assert.Equal(t, "sess-token", tok, "换供应商不换 token 字符串")
	issued, routed, _ := r.snapshot()
	assert.Len(t, issued, 1, "换供应商不得重签")
	assert.Equal(t, []routedTarget{{token: "sess-token", providerKey: "switched-key", modelKey: "model-b"}}, routed)
}

// TestSessionTokenCache_TargetMissingKeepsToken 钉死 gateway 重启后的行为：entry 找不到
// 只记 warn，不在这里重签 —— 重签救不回已 spawn 的子进程，反而会多出一个孤儿 token。
func TestSessionTokenCache_TargetMissingKeepsToken(t *testing.T) {
	r := &fakeTokenRouter{nextTok: "sess-token", missing: true}
	c := newTestCache(r)

	_, err := c.EnsureToken(context.Background(), 7, nil, "pk", "")
	require.NoError(t, err)
	tok, err := c.EnsureToken(context.Background(), 7, nil, "pk", "")
	require.NoError(t, err)

	assert.Equal(t, "sess-token", tok)
	issued, _, revoked := r.snapshot()
	assert.Len(t, issued, 1)
	assert.Empty(t, revoked)
}

// TestSessionTokenCache_ConcurrentFirstTurn_KeepsWinnerAndRevokesLoser 钉死并发首轮兜底：
// 两个 goroutine 同时开首轮时只有一个 token 留在缓存里，输的那条必须撤销，否则网关里
// 泄漏一个永不回收的永久 token。
func TestSessionTokenCache_ConcurrentFirstTurn_KeepsWinnerAndRevokesLoser(t *testing.T) {
	r := &fakeTokenRouter{nextTok: "loser"}
	c := newTestCache(r)
	// 模拟另一个 goroutine 抢先:在本次 IssueTokenFor 返回后、LoadOrStore 之前存好 winner。
	var once sync.Once
	r.beforeStore = func() {
		once.Do(func() { c.tokens.Store(int64(9), "winner") })
	}

	tok, err := c.EnsureToken(context.Background(), 9, nil, "pk", "")
	require.NoError(t, err)

	assert.Equal(t, "winner", tok, "抢输的一方必须改用已经存好的那个 token")
	_, _, revoked := r.snapshot()
	assert.Equal(t, []string{"loser"}, revoked, "自己那条要撤掉，避免泄漏")
}

// TestSessionTokenCache_RevokeRevokesAndEvicts 钉死撤销：撤销后该 session 的下一轮重新
// 签一个新的（会话删除后 id 复活的场景）。
func TestSessionTokenCache_RevokeRevokesAndEvicts(t *testing.T) {
	r := &fakeTokenRouter{nextTok: "first"}
	c := newTestCache(r)
	_, err := c.EnsureToken(context.Background(), 5, nil, "pk", "")
	require.NoError(t, err)

	c.Revoke(5)
	_, _, revoked := r.snapshot()
	assert.Equal(t, []string{"first"}, revoked)

	r.mu.Lock()
	r.nextTok = "second"
	r.mu.Unlock()
	tok, err := c.EnsureToken(context.Background(), 5, nil, "pk", "")
	require.NoError(t, err)
	assert.Equal(t, "second", tok, "撤销后缓存里那条也要清掉")

	c.Revoke(5)
	c.Revoke(5)
	_, _, revoked = r.snapshot()
	assert.Equal(t, []string{"first", "second"}, revoked, "重复撤销是 no-op")
	c.Revoke(0)
	c.Revoke(-1)
}

// TestSessionTokenCache_NoSessionIDIsNotCached 钉死无会话 id 的路径（一次性调用）：
// 不入缓存、不改道，每次现签。
func TestSessionTokenCache_NoSessionIDIsNotCached(t *testing.T) {
	r := &fakeTokenRouter{nextTok: "tok"}
	c := newTestCache(r)

	_, err := c.EnsureToken(context.Background(), 0, nil, "pk", "")
	require.NoError(t, err)
	_, err = c.EnsureToken(context.Background(), 0, nil, "pk", "")
	require.NoError(t, err)

	issued, routed, revoked := r.snapshot()
	assert.Len(t, issued, 2)
	assert.Empty(t, routed)
	assert.Empty(t, revoked)
}

// TestSessionTokenCache_IssueErrorSurfacesAndCachesNothing 钉死签发失败：错误透出给调用方
// （daemon 据此阻断本轮，桌面按「不签」处理），且不得把空串缓存成该会话的 token。
func TestSessionTokenCache_IssueErrorSurfacesAndCachesNothing(t *testing.T) {
	boom := errors.New("boom")
	r := &fakeTokenRouter{issueErr: boom}
	c := newTestCache(r)

	tok, err := c.EnsureToken(context.Background(), 11, nil, "pk", "")
	require.ErrorIs(t, err, boom)
	assert.Empty(t, tok)

	r.mu.Lock()
	r.issueErr = nil
	r.nextTok = "later"
	r.mu.Unlock()
	tok, err = c.EnsureToken(context.Background(), 11, nil, "pk", "")
	require.NoError(t, err)
	assert.Equal(t, "later", tok, "失败那次不得在缓存里留下空 token")
}

// TestSessionTokenCache_NoRouterSignsNothing 钉死网关缺席：没有网关时不签、不报错，
// 调用方按「不签」处理（CLI 走自身登录态）。
func TestSessionTokenCache_NoRouterSignsNothing(t *testing.T) {
	c := NewSessionTokenCache("test.sessionToken", func() SessionTokenRouter { return nil })
	tok, err := c.EnsureToken(context.Background(), 3, nil, "pk", "")
	require.NoError(t, err)
	assert.Empty(t, tok)
	c.Revoke(3)

	nilCache := NewSessionTokenCache("test.sessionToken", nil)
	tok, err = nilCache.EnsureToken(context.Background(), 3, nil, "pk", "")
	require.NoError(t, err)
	assert.Empty(t, tok)
	nilCache.Revoke(3)
}

// TestSessionTokenCache_ParallelFirstTurns_OneTokenSurvives 是并发压力面：同一 session
// 的多路首轮只能有一个 token 活下来，其余全部撤销，且所有调用方拿到的是同一个。
func TestSessionTokenCache_ParallelFirstTurns_OneTokenSurvives(t *testing.T) {
	r := &fakeTokenRouter{}
	c := newTestCache(r)

	const n = 8
	got := make([]string, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			tok, err := c.EnsureToken(context.Background(), 77, nil, "pk", "")
			assert.NoError(t, err)
			got[i] = tok
		}()
	}
	wg.Wait()

	for i := 1; i < n; i++ {
		assert.Equal(t, got[0], got[i], "同一 session 的并发首轮必须收敛到同一个 token")
	}
	issued, _, revoked := r.snapshot()
	assert.Len(t, revoked, len(issued)-1, "除留下的那条外全部撤销")
	assert.NotContains(t, revoked, got[0])
}

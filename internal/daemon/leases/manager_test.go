package leases

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

const (
	testToken = "device-access-token"
	key1      = "provider-1"
)

// fakeClock 是一只可拨的钟:续租按剩余寿命判,测试要能把时间推到「只剩三分之一」。
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// leaseServer 是账号服务器的租约端点替身:每次申请发一份新 key(lease-1、lease-2…),
// 寿命 30 分钟;fail 置上时按 status 失败(0 = 直接断开)。
type leaseServer struct {
	t        *testing.T
	clock    *fakeClock
	requests atomic.Int32
	purposes []string
	mu       sync.Mutex
	fail     atomic.Bool
	status   int
	server   *httptest.Server
}

func newLeaseServer(t *testing.T, clock *fakeClock) *leaseServer {
	ls := &leaseServer{t: t, clock: clock, status: http.StatusServiceUnavailable}
	ls.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, syncwire.PathProviderLease, r.URL.Path)
		assert.Equal(t, "Bearer "+testToken, r.Header.Get("Authorization"))
		var request syncwire.LeaseRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		ls.mu.Lock()
		ls.purposes = append(ls.purposes, request.Purpose)
		ls.mu.Unlock()
		if ls.fail.Load() {
			w.WriteHeader(ls.status)
			return
		}
		n := ls.requests.Add(1)
		response := syncwire.LeaseResponse{
			ProviderKey: request.ProviderKey, APIKey: "lease-" + string(rune('0'+n)),
			BaseURL: "https://leased.example", ExpiresAt: clock.Now().Add(30 * time.Minute).UnixMilli(),
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": response})
	}))
	t.Cleanup(ls.server.Close)
	return ls
}

func (ls *leaseServer) seenPurposes() []string {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return append([]string(nil), ls.purposes...)
}

func newTestManager(ls *leaseServer, clock *fakeClock) *Manager {
	return New(Options{
		ServerURL:   func() string { return ls.server.URL },
		AccessToken: func() string { return testToken },
		HTTPClient:  ls.server.Client(),
		Now:         clock.Now,
	})
}

func newClock() *fakeClock { return &fakeClock{now: time.Unix(1_700_000_000, 0)} }

// Given 这台 agentred 手上还没有某个供应商的租约,When 一轮对话要用它,
// Then 用设备凭据向 server 申请一次、带上用途,之后在有效期内复用、不再申请。
func TestManager_GivenNoLease_WhenNeeded_ThenRequestsOnceWithTheDeviceCredentialAndReusesIt(t *testing.T) {
	clock := newClock()
	ls := newLeaseServer(t, clock)
	m := newTestManager(ls, clock)

	lease, err := m.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.NoError(t, err)
	assert.Equal(t, "lease-1", lease.APIKey)
	assert.Equal(t, "https://leased.example", lease.BaseURL)

	clock.Advance(10 * time.Minute)
	again, err := m.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.NoError(t, err)
	assert.Equal(t, "lease-1", again.APIKey)
	assert.Equal(t, int32(1), ls.requests.Load())
	assert.Equal(t, []string{"turn"}, ls.seenPurposes())
}

// Given 一份仍在使用的租约,When 只剩三分之一寿命,Then 续租(再申请一次)换上新值;
// 剩得更多时不续。
func TestManager_GivenALeaseInUse_WhenOneThirdOfItsLifetimeRemains_ThenItIsRenewed(t *testing.T) {
	clock := newClock()
	ls := newLeaseServer(t, clock)
	m := newTestManager(ls, clock)
	_, err := m.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.NoError(t, err)

	clock.Advance(19 * time.Minute) // 剩 11 分钟 > 10
	lease, err := m.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.NoError(t, err)
	assert.Equal(t, "lease-1", lease.APIKey)

	clock.Advance(2 * time.Minute) // 剩 9 分钟 ≤ 三分之一
	lease, err = m.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.NoError(t, err)
	assert.Equal(t, "lease-2", lease.APIKey)
	assert.Equal(t, int32(2), ls.requests.Load())
}

// 续租没成功但旧租约还没到期时照用旧的:server 一时连不上不该立刻掐断进行中的一轮。
func TestManager_GivenRenewalFails_WhenTheOldLeaseIsStillValid_ThenItKeepsServing(t *testing.T) {
	clock := newClock()
	ls := newLeaseServer(t, clock)
	m := newTestManager(ls, clock)
	_, err := m.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.NoError(t, err)

	ls.fail.Store(true)
	clock.Advance(25 * time.Minute)
	lease, err := m.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.NoError(t, err)
	assert.Equal(t, "lease-1", lease.APIKey)

	clock.Advance(6 * time.Minute) // 过期
	_, err = m.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.ErrorIs(t, err, rpcerror.ErrProviderLeaseUnavailable)
}

// 拿不到租约的每一种原因 —— server 还不提供这个端点(404)、拒绝(403)、连不上、没登录 ——
// 对调用方都是同一句「凭据租约过期，账号服务器连不上」。
func TestManager_GivenTheServerCannotIssue_WhenNeeded_ThenFailsWithLeaseUnavailable(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusInternalServerError} {
		clock := newClock()
		ls := newLeaseServer(t, clock)
		ls.fail.Store(true)
		ls.status = status
		_, err := newTestManager(ls, clock).Get(context.Background(), key1, syncwire.LeasePurposeTest)
		require.ErrorIs(t, err, rpcerror.ErrProviderLeaseUnavailable, "status %d", status)
	}

	unreachable := New(Options{ServerURL: func() string { return "http://127.0.0.1:1" }, AccessToken: func() string { return testToken }})
	_, err := unreachable.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.ErrorIs(t, err, rpcerror.ErrProviderLeaseUnavailable)

	loggedOut := New(Options{ServerURL: func() string { return "" }, AccessToken: func() string { return "" }})
	_, err = loggedOut.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.ErrorIs(t, err, rpcerror.ErrProviderLeaseUnavailable)
}

// server 发回的租约若已到期或不是要的那个供应商,不当成一份可用的租约。
func TestManager_GivenAnUnusableResponse_ThenItIsNotALease(t *testing.T) {
	clock := newClock()
	for _, response := range []syncwire.LeaseResponse{
		{ProviderKey: key1, APIKey: "k", ExpiresAt: clock.Now().Add(-time.Minute).UnixMilli()},
		{ProviderKey: "other", APIKey: "k", ExpiresAt: clock.Now().Add(time.Hour).UnixMilli()},
		{ProviderKey: key1, APIKey: "", ExpiresAt: clock.Now().Add(time.Hour).UnixMilli()},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": response})
		}))
		m := New(Options{ServerURL: func() string { return server.URL }, AccessToken: func() string { return testToken }, HTTPClient: server.Client(), Now: clock.Now})
		_, err := m.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
		require.ErrorIs(t, err, rpcerror.ErrProviderLeaseUnavailable)
		server.Close()
	}
}

// Given 手上有两个供应商的租约,When 收到点名其中一个的吊销,Then 立即丢弃它、下次要用时
// 重新申请;另一个不受影响。空名单吊销全部。
func TestManager_GivenARevokeSignal_ThenTheNamedLeasesAreDroppedImmediately(t *testing.T) {
	clock := newClock()
	ls := newLeaseServer(t, clock)
	m := newTestManager(ls, clock)
	_, err := m.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.NoError(t, err)
	_, err = m.Get(context.Background(), "provider-2", syncwire.LeasePurposeTurn)
	require.NoError(t, err)
	require.Equal(t, int32(2), ls.requests.Load())

	m.Revoke(key1)
	ls.fail.Store(true)
	_, err = m.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.ErrorIs(t, err, rpcerror.ErrProviderLeaseUnavailable, "a revoked lease must not keep serving")
	kept, err := m.Get(context.Background(), "provider-2", syncwire.LeasePurposeTurn)
	require.NoError(t, err)
	assert.Equal(t, "lease-2", kept.APIKey)

	m.Revoke()
	_, err = m.Get(context.Background(), "provider-2", syncwire.LeasePurposeTurn)
	require.ErrorIs(t, err, rpcerror.ErrProviderLeaseUnavailable)
}

// 租约只在内存里:一个新进程(重启)手上什么都没有,第一次要用时重新申请。
func TestManager_GivenARestart_WhenNeeded_ThenItRequestsAgain(t *testing.T) {
	clock := newClock()
	ls := newLeaseServer(t, clock)
	_, err := newTestManager(ls, clock).Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.NoError(t, err)

	restarted := newTestManager(ls, clock)
	lease, err := restarted.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.NoError(t, err)
	assert.Equal(t, "lease-2", lease.APIKey)
	assert.Equal(t, int32(2), ls.requests.Load())
}

// 直连派发时桌面端随这一轮附带租约:agentred 优先用它、不去 server 申请;这一轮结束
// (release)就丢掉,之后回到向 server 申请。
func TestManager_GivenAnAttachedLease_ThenItIsPreferredAndDroppedWhenTheRunEnds(t *testing.T) {
	clock := newClock()
	ls := newLeaseServer(t, clock)
	m := newTestManager(ls, clock)

	release := m.Attach("run-1", []Lease{{ProviderKey: key1, APIKey: "desktop-key", BaseURL: "https://desk.example", ExpiresAt: clock.Now().Add(24 * time.Hour)}})
	lease, err := m.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.NoError(t, err)
	assert.Equal(t, "desktop-key", lease.APIKey)
	assert.Equal(t, int32(0), ls.requests.Load(), "an attached lease is used without asking the server")

	release()
	release() // 幂等
	lease, err = m.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.NoError(t, err)
	assert.Equal(t, "lease-1", lease.APIKey)
}

// 同一个供应商被两轮同时附带时,一轮结束不收走另一轮的;过期的附带租约不算数。
func TestManager_GivenTwoRunsAttachingTheSameProvider_WhenOneEnds_ThenTheOtherKeepsIt(t *testing.T) {
	clock := newClock()
	ls := newLeaseServer(t, clock)
	ls.fail.Store(true)
	m := newTestManager(ls, clock)

	releaseA := m.Attach("run-a", []Lease{{ProviderKey: key1, APIKey: "key-a", ExpiresAt: clock.Now().Add(time.Hour)}})
	releaseB := m.Attach("run-b", []Lease{{ProviderKey: key1, APIKey: "key-b", ExpiresAt: clock.Now().Add(2 * time.Hour)}})
	releaseA()
	lease, err := m.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.NoError(t, err)
	assert.Equal(t, "key-b", lease.APIKey)

	clock.Advance(3 * time.Hour)
	_, err = m.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.ErrorIs(t, err, rpcerror.ErrProviderLeaseUnavailable, "an expired attached lease is not used")
	releaseB()
}

// 吊销同样丢掉附带的租约:设备被移除时,哪一份 key 都不该再留在内存里。
func TestManager_GivenARevoke_ThenAttachedLeasesGoToo(t *testing.T) {
	clock := newClock()
	ls := newLeaseServer(t, clock)
	ls.fail.Store(true)
	m := newTestManager(ls, clock)
	release := m.Attach("run-1", []Lease{{ProviderKey: key1, APIKey: "desktop-key", ExpiresAt: clock.Now().Add(time.Hour)}})
	defer release()

	m.Revoke(key1)
	_, err := m.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.True(t, errors.Is(err, rpcerror.ErrProviderLeaseUnavailable))
}

// 同一个供应商同时有好几处要用(一轮里的并发请求)时只申请一次。
func TestManager_GivenConcurrentNeeds_WhenNoLeaseIsHeld_ThenOnlyOneRequestIsMade(t *testing.T) {
	clock := newClock()
	ls := newLeaseServer(t, clock)
	m := newTestManager(ls, clock)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
			assert.NoError(t, err)
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(1), ls.requests.Load())
}

// 同一轮(同一个 holder)再附带一次会换上新的;先前那次的 release 迟到时收不走新的。
func TestManager_GivenAReattachUnderTheSameHolder_WhenTheEarlierReleaseRunsLate_ThenTheNewerLeaseStays(t *testing.T) {
	clock := newClock()
	ls := newLeaseServer(t, clock)
	ls.fail.Store(true)
	m := newTestManager(ls, clock)

	first := m.Attach("run-1", []Lease{{ProviderKey: key1, APIKey: "old", ExpiresAt: clock.Now().Add(time.Hour)}})
	second := m.Attach("run-1", []Lease{{ProviderKey: key1, APIKey: "new", ExpiresAt: clock.Now().Add(time.Hour)}})
	first()
	lease, err := m.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.NoError(t, err)
	assert.Equal(t, "new", lease.APIKey)

	second()
	_, err = m.Get(context.Background(), key1, syncwire.LeasePurposeTurn)
	require.ErrorIs(t, err, rpcerror.ErrProviderLeaseUnavailable)
}

// 桌面端附带的租约还带着供应商的元数据(未登录账号的 agentred 没有快照可读);这一轮
// 还在跑时查得到,结束就没了。服务端租约不带元数据。
func TestManager_GivenAnAttachedLeaseWithMetadata_ThenTheProviderIsKnownForThatRun(t *testing.T) {
	clock := newClock()
	ls := newLeaseServer(t, clock)
	m := newTestManager(ls, clock)
	meta := state.LLMProviderMeta{Name: "Anthropic", Type: "anthropic", DefaultModelKey: "mk", Models: []state.LLMModelMeta{{ModelKey: "mk", ModelID: "claude", Enabled: true}}}
	release := m.Attach("run-1", []Lease{{ProviderKey: key1, APIKey: "desk", ExpiresAt: clock.Now().Add(time.Hour), Provider: &meta}})

	assert.Equal(t, map[string]state.LLMProviderMeta{key1: meta}, m.AttachedProviders())

	release()
	assert.Empty(t, m.AttachedProviders())
}

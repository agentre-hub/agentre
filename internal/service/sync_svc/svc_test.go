package sync_svc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-ai/agentre/internal/pkg/syncwire"
)

// 本文件守规格里最重的那一条判据：**把通道整个关掉，所有功能仍然正确，只是变慢到
// 30 秒**（「账号级实时通道 · 失败处理」）。
//
// 「仍然正确」在这里被具体成「不丢变更」：一条**只存在于 server 上**的变更，在通道
// 一帧都没送到的情况下，照样落到本机、游标照样推进——而不是只断言「没崩」。

// serverOnlyChange 是一条只在 server 上发生过的变更：本端从没见过它，只能靠下行拿到。
func serverOnlyChange() []*syncwire.PullPage {
	return []*syncwire.PullPage{{
		Items: []syncwire.PullItem{{
			Kind: "project", SyncID: "p-1", Version: 11, Payload: []byte(`{"name":"A"}`),
		}},
		NextCursor: 11,
	}}
}

// slowRetry 让重连尝试之间歇一口气：用例里通道永远连不上，不歇会把这一秒钟的
// CPU 全烧在拨号上。
func slowRetry(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(time.Millisecond):
		return true
	}
}

// assertConvergedByPolling 断言那条只在 server 上的变更已经靠轮询落地。
func assertConvergedByPolling(t *testing.T, h *harness) {
	t.Helper()
	assert.Equal(t, "A", h.adapter.rows["p-1"], "只在 server 上的那条变更落到了本机")
	assert.Equal(t, []string{"p-1@A"}, h.adapter.applied)
	assert.Empty(t, h.svc.getLastErr(), "通道连不上不是同步失败")
	st, err := h.svc.Status(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(11), st.Cursor, "游标推进过这条变更，它不会被再拉一次，也没被跳过")
}

// TestStart_GivenAccountChannelUnreachable_KeepsPollingAndLosesNothing （c）通道
// 连不上：退回 30 秒轮询，即今天的行为——不重试到底、不阻塞任何操作，更不丢变更。
func TestStart_GivenAccountChannelUnreachable_KeepsPollingAndLosesNothing(t *testing.T) {
	h := newHarness(t, true)
	registerProjects(t, nil) // ticker 同时驱动本机路径上报（R16）
	h.transport.pages = serverOnlyChange()
	unreachable := errors.New("account channel: dial failed")
	tr := h.withAccountChannel(func(int) (<-chan syncwire.AccountChannelFrame, error) {
		return nil, unreachable
	})
	h.svc.channelRetryWait = slowRetry
	// 轮询周期是唯一被压缩的东西：压的是**等待**，不是行为。生产的 30 秒由
	// TestNew_KeepsTheThirtySecondPoll 与 TestPollIntervalIsThirtySeconds 钉着。
	h.svc.pollEvery = 5 * time.Millisecond

	h.svc.Start(context.Background())
	t.Cleanup(h.svc.Stop)

	for awaitPull(t, tr.pulls, "通道连不上时，轮询照样把变更拉回来") != 11 { //nolint:revive // 等游标推进
	}
	// 轮询还在继续：之后发生的变更同样会在一个周期内到达。
	awaitPull(t, tr.pulls, "轮询在通道连不上之后仍然继续")
	awaitPull(t, tr.pulls, "轮询在通道连不上之后仍然继续")
	h.svc.Stop()

	require.GreaterOrEqual(t, tr.attempts.Load(), int64(1), "通道确实试过")
	assert.Equal(t, tr.attempts.Load(), tr.dialErrs.Load(),
		"每一次拨号都失败——上面那条变更一帧信号都没沾过")
	assertConvergedByPolling(t, h)
}

// TestStart_GivenNoAccountChannelAtAll_KeepsPollingAndLosesNothing （c）把通道整个
// 关掉：出入口根本不提供这条通道（单机构建、旧版 server、部署时关掉）。这是完整
// 可用的形态，不是降级故障——引擎一次拨号都不该发起，功能一条都不该少。
func TestStart_GivenNoAccountChannelAtAll_KeepsPollingAndLosesNothing(t *testing.T) {
	h := newHarness(t, true)
	registerProjects(t, nil)
	h.transport.pages = serverOnlyChange()
	// 出入口就是 fakeTransport：它没有 DialAccountChannel。
	_, hasChannel := Transport(h.transport).(AccountChannelDialer)
	require.False(t, hasChannel, "这个替身刻意不带通道")
	h.svc.pollEvery = 5 * time.Millisecond

	applied := make(chan string, 8)
	h.adapter.applyFn = func(in *inbound) error {
		applied <- in.SyncID
		return nil
	}

	h.svc.Start(context.Background())
	t.Cleanup(h.svc.Stop)

	select {
	case syncID := <-applied:
		assert.Equal(t, "p-1", syncID)
	case <-time.After(3 * time.Second):
		t.Fatal("没有通道时，轮询应当照样把变更拉回来")
	}
	h.svc.Stop()

	assertConvergedByPolling(t, h)
}

// TestNew_KeepsTheThirtySecondPoll 规格：「30 秒轮询保留，不缩短也不删除」。轮询
// 周期上的那个测试接缝只许测试用——生产装出来的引擎必须还是 30 秒，否则通道就从
// 优化变成了关键路径。
func TestNew_KeepsTheThirtySecondPoll(t *testing.T) {
	svc, ok := New(nil).(*service)
	require.True(t, ok)
	assert.Equal(t, PollInterval, svc.pollEvery)
	assert.Equal(t, PollInterval, svc.tickEvery())
	assert.Equal(t, PollInterval, (&service{}).tickEvery(), "零值也按生产的 30 秒算")
}

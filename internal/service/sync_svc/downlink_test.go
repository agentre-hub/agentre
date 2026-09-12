package sync_svc

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
)

// 本文件守的是账号级实时通道这个**第二个**下行触发源（规格「账号级实时通道 ·
// 失败处理」与「同步传播」）：
//
//	a 收到信号立刻 Pull；
//	b 建连与每一次重连各自主动 Pull 一次，不等服务端补发；
//	d 重复、乱序、不认识的信号都无害——版本号只用于「该拉了」，拉哪些由本端游标定。
//
// 「把通道整个关掉仍然正确」那条（c）在 svc_test.go。
//
// 这些测试全都走 Start：轮询周期保持生产的 30 秒（newHarness 不注入更短的周期），
// 因此测试里观察到的每一次 Pull **只可能**来自通道——毫秒级的用例里那个 ticker
// 一次都不会响。

// channelTransport 给 fakeTransport 接上账号级实时通道：每一次拨号取一份脚本
// （连不连得上、连上了给哪条信号流），每一次 SyncPull 的游标另投一个 channel
// 供断言——直接读 fakeTransport.pulledAt 会与后台 goroutine 抢同一个切片。
type channelTransport struct {
	*fakeTransport
	dial     func(attempt int) (<-chan syncwire.AccountChannelFrame, error)
	attempts atomic.Int64
	// dialErrs 记下每一次拨号的结果，供「通道确实一次都没连上」这类断言。
	dialErrs atomic.Int64
	pulls    chan int64
}

func (c *channelTransport) DialAccountChannel(context.Context) (<-chan syncwire.AccountChannelFrame, error) {
	signals, err := c.dial(int(c.attempts.Add(1)))
	if err != nil {
		c.dialErrs.Add(1)
	}
	return signals, err
}

func (c *channelTransport) SyncPull(ctx context.Context, cursor int64, limit int) (*syncwire.PullPage, error) {
	page, err := c.fakeTransport.SyncPull(ctx, cursor, limit)
	// 满了就丢：断言只看「该来的来了」，丢掉多余的那些好过让被测 goroutine
	// 卡在这个 channel 上（那会让 Stop 永远等不到它退出）。
	select {
	case c.pulls <- cursor:
	default:
	}
	return page, err
}

// withAccountChannel 把引擎的出入口换成带通道的那一个。必须在 Start 之前调用。
func (h *harness) withAccountChannel(dial func(attempt int) (<-chan syncwire.AccountChannelFrame, error)) *channelTransport {
	tr := &channelTransport{fakeTransport: h.transport, dial: dial, pulls: make(chan int64, 256)}
	h.svc.transport = tr
	return tr
}

// awaitPull 等下一次下行，返回它用的游标。
func awaitPull(t *testing.T, pulls <-chan int64, why string) int64 {
	t.Helper()
	select {
	case cursor := <-pulls:
		return cursor
	case <-time.After(3 * time.Second):
		t.Fatalf("等不到下行：%s", why)
		return 0
	}
}

// expectNoPull 断言这段时间内没有下行——用来证明某一帧**没有**触发拉取。
func expectNoPull(t *testing.T, pulls <-chan int64, why string) {
	t.Helper()
	select {
	case cursor := <-pulls:
		t.Fatalf("不该有下行（游标 %d）：%s", cursor, why)
	case <-time.After(100 * time.Millisecond):
	}
}

// openChannel 给出一条「连上了就不断」的信号流：拨号成功，帧由测试自己塞。
func openChannel(signals chan syncwire.AccountChannelFrame) func(int) (<-chan syncwire.AccountChannelFrame, error) {
	return func(int) (<-chan syncwire.AccountChannelFrame, error) { return signals, nil }
}

// TestStart_GivenAccountChannelSignal_PullsImmediately （a）通道在时收到信号立刻
// Pull，而不是等下一个 30 秒周期。
func TestStart_GivenAccountChannelSignal_PullsImmediately(t *testing.T) {
	h := newHarness(t, true)
	require.Equal(t, PollInterval, h.svc.tickEvery(),
		"轮询周期保持 30 秒——本用例里看到的 Pull 都不可能是它带来的")
	signals := make(chan syncwire.AccountChannelFrame)
	tr := h.withAccountChannel(openChannel(signals))

	h.svc.Start(context.Background())
	t.Cleanup(h.svc.Stop)

	// 建连本身那一次先消化掉（b 在下一个用例里单独守）。
	awaitPull(t, tr.pulls, "建连之后应当主动拉一次")

	signals <- syncwire.AccountChannelFrame{
		Type: syncwire.AccountChannelSyncVersion, Version: 42,
	}
	assert.Equal(t, int64(0), awaitPull(t, tr.pulls, "信号到达之后应当立刻拉一次"),
		"拉哪些由本端游标决定，与信号里的版本号无关")
}

// TestStart_GivenAccountChannelReconnect_PullsOnEveryConnect （b）首次建连与每一次
// 重连都主动 Pull 一次：通道不保存未送达的信号，断线期间的变更由这一次补齐。
func TestStart_GivenAccountChannelReconnect_PullsOnEveryConnect(t *testing.T) {
	h := newHarness(t, true)
	// 第一次拨号给一条**当场就断**的流，第二次给一条常连的；两次都一帧不发。
	first := make(chan syncwire.AccountChannelFrame)
	close(first)
	second := make(chan syncwire.AccountChannelFrame)
	tr := h.withAccountChannel(func(attempt int) (<-chan syncwire.AccountChannelFrame, error) {
		if attempt == 1 {
			return first, nil
		}
		return second, nil
	})
	h.svc.channelRetryWait = immediateRetry

	h.svc.Start(context.Background())
	t.Cleanup(h.svc.Stop)

	awaitPull(t, tr.pulls, "首次建连之后应当主动拉一次")
	awaitPull(t, tr.pulls, "重连之后应当再主动拉一次，而不是等服务端补发")
	assert.GreaterOrEqual(t, tr.attempts.Load(), int64(2), "断开之后应当重连")
}

// TestStart_GivenDuplicateAndOutOfOrderSignals_PullsFromTheLocalCursor （d）重复与
// 乱序的信号都无害：版本号只用于「该拉了」的判断，拉哪些由本端自己的游标决定，
// 落地因此不会被跳过、也不会重复。
func TestStart_GivenDuplicateAndOutOfOrderSignals_PullsFromTheLocalCursor(t *testing.T) {
	h := newHarness(t, true)
	h.transport.pages = []*syncwire.PullPage{{
		Items: []syncwire.PullItem{{
			Kind: "project", SyncID: "p-1", Version: 11, Payload: []byte(`{"name":"A"}`),
		}},
		NextCursor: 11,
	}}
	signals := make(chan syncwire.AccountChannelFrame)
	tr := h.withAccountChannel(openChannel(signals))

	h.svc.Start(context.Background())
	t.Cleanup(h.svc.Stop)

	// 建连那一轮把唯一一页拉下来，游标推到 11。
	for awaitPull(t, tr.pulls, "建连之后应当主动拉一次") != 11 { //nolint:revive // 见下
		// 一轮下行可能翻多页，等到游标真的推进为止。
	}

	// 乱序（比已见过的版本还旧）、重复：每一条都照常触发一次拉取，且都从**本端
	// 游标 11** 继续——拿信号里的版本号当游标会把 11 之后的变更整段跳过。
	for _, version := range []int64{5, 11, 11} {
		signals <- syncwire.AccountChannelFrame{
			Type: syncwire.AccountChannelSyncVersion, Version: version,
		}
		assert.Equal(t, int64(11), awaitPull(t, tr.pulls, "重复/乱序信号也照常拉一次"))
	}
	expectNoPull(t, tr.pulls, "没有新信号就不该再拉——30 秒的 ticker 在毫秒级用例里不响")

	h.svc.Stop()
	assert.Equal(t, []string{"p-1@A"}, h.adapter.applied, "重复信号没有让同一条重复落地")
	st, err := h.svc.Status(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(11), st.Cursor, "乱序信号没有把游标拖回去")
}

// TestStart_GivenUnknownSignalType_IgnoresIt 通道日后会承载别的通知（server 的
// accountchan_svc.Frame 就是为此带类型标记的）：不认识的种类一律忽略，既不拉取
// 也不断连。
func TestStart_GivenUnknownSignalType_IgnoresIt(t *testing.T) {
	h := newHarness(t, true)
	signals := make(chan syncwire.AccountChannelFrame)
	tr := h.withAccountChannel(openChannel(signals))

	h.svc.Start(context.Background())
	t.Cleanup(h.svc.Stop)
	awaitPull(t, tr.pulls, "建连之后应当主动拉一次")

	signals <- syncwire.AccountChannelFrame{Type: "some_future_notification", Version: 9}
	expectNoPull(t, tr.pulls, "不认识的信号种类不该触发下行")

	// 连接还在：随后的正经信号照常生效。
	signals <- syncwire.AccountChannelFrame{
		Type: syncwire.AccountChannelSyncVersion, Version: 9,
	}
	awaitPull(t, tr.pulls, "忽略未知种类之后，通道仍然可用")
}

// immediateRetry 是重连等待的测试时钟：立刻返回，ctx 结束时收工。
func immediateRetry(ctx context.Context) bool { return ctx.Err() == nil }

// ── 登录身份变了，实时通道必须跟着换 ────────────────────────────────────────
//
// 通道在建连那一刻就把服务端地址与设备凭据钉死了（server_svc.DialAccountChannel
// 拨的是当时的 baseURL、带的是当时的 access token）。登录状态变了却不断开，这条
// 常连就一直挂在**上一套 server** 上：新 server 的通道永远拨不起来，实时下行静默
// 退化成 30 秒轮询，而界面上一切正常。
//
// 它与中继登记（app/peer.go 在 logged_in/logged_out 上停掉重建）是同一个道理，
// 只是这条一直漏着。
//
// 注意本用例**不**注入 immediateRetry：重连等待保持生产的 30 秒，因此这里看到的
// 第二次拨号只可能来自 Drop 本身，而不是等到了下一个重连窗口。
func TestDropAccountChannel_GivenIdentityChanged_RedialsWithoutWaiting(t *testing.T) {
	h := newHarness(t, true)
	first := make(chan syncwire.AccountChannelFrame)
	second := make(chan syncwire.AccountChannelFrame)
	tr := h.withAccountChannel(func(attempt int) (<-chan syncwire.AccountChannelFrame, error) {
		if attempt == 1 {
			return first, nil
		}
		return second, nil
	})

	h.svc.Start(context.Background())
	t.Cleanup(h.svc.Stop)
	awaitPull(t, tr.pulls, "首次建连之后应当主动拉一次")

	h.svc.DropAccountChannel()

	awaitPull(t, tr.pulls, "换了身份之后应当立刻重连并补拉一次，而不是等 30 秒的重连窗口")
	assert.GreaterOrEqual(t, tr.attempts.Load(), int64(2),
		"旧连接必须被真的断开，否则新 server 的通道永远拨不起来")
}

// 没有连接时 Drop 也必须是安全的：登出事件可能落在通道正在重试的空窗里。
func TestDropAccountChannel_GivenNoLiveChannel_IsSafe(t *testing.T) {
	h := newHarness(t, true)
	h.svc.DropAccountChannel()
	h.svc.DropAccountChannel()
}

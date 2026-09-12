package portforward

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/port_forward_entity"
	"github.com/agentre-hub/agentre/internal/repository/port_forward_repo/mock_port_forward_repo"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// 本文件测的是**声明改了,正在跑的流当场就得死**这一条(规格「断开与失败」:映射被
// 删除或停用 → 正在进行的流立即关闭)。
//
// 它落在设备侧那一半:声明族的改动发生在 daemon 级的 Handlers 上,而流活在连接级的
// Streams 里 —— 少了中间那条通路,停用一条正在转发的映射只会让**下一次** open 被拒,
// 已经开着的那条流照旧把字节搬下去,规格明写的行为不成立。
//
// 三条不许踩的线:
//
//   - 关的是**那一条映射**的流。只断言「目标流关了」的用例,一个「一停用就全关」的
//     实现同样过得去 —— 所以每条用例都带一条别的端口上的流,并断言它还活着。
//   - 收尾走**既有语义**:一条 port_forward_closed,带着与 open 拒绝同一族的领域码,
//     调用方按码分支(「去把它打开」和「那个端口压根没被声明出来」是两件事)。
//   - 关掉之后再指向它的 write / close / ack 一律 StreamNotFound,与任务 4 已有的
//     close 语义同一句话,不另造一种说法。

// ---------- 脚手架 ----------

// mappingSet 是一份可以就地改的声明集:用例改它,闸门与声明族读它。仓储一律走
// mockgen 的 mock,不连库。
type mappingSet struct {
	mu   sync.Mutex
	rows map[int64]*port_forward_entity.PortForward
}

// revocableGate 建一份「这几个端口已声明且已启用」的判定面,id 取端口号本身。
func revocableGate(t *testing.T, dial Dialer, ports ...int) (*Handlers, *mappingSet) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mock_port_forward_repo.NewMockPortForwardRepo(ctrl)
	set := &mappingSet{rows: make(map[int64]*port_forward_entity.PortForward, len(ports))}
	for _, port := range ports {
		set.rows[int64(port)] = &port_forward_entity.PortForward{ID: int64(port), Port: port, Enabled: true}
	}

	repo.EXPECT().FindByPort(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, port int) (*port_forward_entity.PortForward, error) {
			set.mu.Lock()
			defer set.mu.Unlock()
			row := set.rows[int64(port)]
			if row == nil {
				return nil, nil
			}
			copied := *row
			return &copied, nil
		}).AnyTimes()
	repo.EXPECT().Get(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id int64) (*port_forward_entity.PortForward, error) {
			set.mu.Lock()
			defer set.mu.Unlock()
			row := set.rows[id]
			if row == nil {
				return nil, nil
			}
			copied := *row
			return &copied, nil
		}).AnyTimes()
	repo.EXPECT().SetEnabled(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id int64, enabled bool) (int64, error) {
			set.mu.Lock()
			defer set.mu.Unlock()
			row := set.rows[id]
			if row == nil {
				return 0, nil
			}
			row.Enabled = enabled
			return 1, nil
		}).AnyTimes()
	repo.EXPECT().Delete(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id int64) (int64, error) {
			set.mu.Lock()
			defer set.mu.Unlock()
			if set.rows[id] == nil {
				return 0, nil
			}
			delete(set.rows, id)
			return 1, nil
		}).AnyTimes()

	return NewHandlers(Options{Repo: repo, Dial: dial}), set
}

// portDialer 按端口分别记下「到本机服务的那条连接关没关」。别的端口不受影响这条判据
// 只能落在这里 —— 「宿主没收到 closed」证不了设备侧那条 socket 还开着。
type portDialer struct {
	mu     sync.Mutex
	closed map[int]bool
}

func newPortDialer() *portDialer { return &portDialer{closed: make(map[int]bool)} }

func (d *portDialer) dial(ctx context.Context, port int) (net.Conn, error) {
	conn, err := DialLoopback(ctx, port)
	if err != nil {
		return nil, err
	}
	return &portTrackedConn{Conn: conn, owner: d, port: port}, nil
}

func (d *portDialer) isClosed(port int) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closed[port]
}

type portTrackedConn struct {
	net.Conn
	owner *portDialer
	port  int
}

func (c *portTrackedConn) Close() error {
	c.owner.mu.Lock()
	c.owner.closed[c.port] = true
	c.owner.mu.Unlock()
	return c.Conn.Close()
}

// closedFor 找这条流的收尾通知(nil = 还没有)。
func (r *recorder) closedFor(streamID string) *agentrewire.PortForwardClosedNotification {
	for _, event := range r.snapshot() {
		if event.kind == "closed" && event.closed.GetStreamId() == streamID {
			return event.closed
		}
	}
	return nil
}

func (r *recorder) waitClosedFor(t *testing.T, streamID string) *agentrewire.PortForwardClosedNotification {
	t.Helper()
	require.Eventually(t, func() bool { return r.closedFor(streamID) != nil }, 10*time.Second, time.Millisecond,
		"映射没了,这条流必须留下一条收尾通知")
	return r.closedFor(streamID)
}

// twoLiveStreams 在两个各自声明过的端口上各开一条**还开着**的流(上游回了头就挂住)。
func twoLiveStreams(t *testing.T) (gate *Handlers, streams *Streams, rec *recorder, dialer *portDialer, portA, portB int) {
	t.Helper()
	portA, _ = hangingServer(t)
	portB, _ = hangingServer(t)
	rec = newRecorder()
	dialer = newPortDialer()
	gate, _ = revocableGate(t, dialer.dial, portA, portB)
	streams = NewStreams(StreamOptions{Gate: gate, Notify: rec.notify})
	t.Cleanup(streams.CloseAll)

	for _, one := range []struct {
		id   string
		port int
	}{{"a", portA}, {"b", portB}} {
		_, err := streams.Open(context.Background(), &agentrewire.PortForwardOpenRequest{
			StreamId: one.id, Port: uint32(one.port), Method: http.MethodGet, Path: "/hang",
		})
		require.NoError(t, err)
	}
	// 两条流都真的跑起来了(响应头已经回来),否则「立即关闭」测的是一条还没活过的流。
	require.Eventually(t, func() bool {
		heads := 0
		for _, event := range rec.snapshot() {
			if event.kind == "head" {
				heads++
			}
		}
		return heads == 2
	}, 10*time.Second, time.Millisecond, "两条流都该已经把响应头带回来")
	return gate, streams, rec, dialer, portA, portB
}

// assertOtherStreamUntouched 钉住「只关那一条映射的流」:别的端口上那条流的本机
// socket 还开着,ack 还答得动,也没有替它发过收尾通知。
func assertOtherStreamUntouched(t *testing.T, streams *Streams, rec *recorder, dialer *portDialer, portB int) {
	t.Helper()
	assert.False(t, dialer.isClosed(portB), "别的端口上那条流的本机连接不该被关掉")
	_, err := streams.Ack(context.Background(), &agentrewire.PortForwardAckRequest{StreamId: "b", ConsumedBytes: 1})
	assert.NoError(t, err, "别的端口上那条流必须还活着")
	assert.Nil(t, rec.closedFor("b"), "别的端口上那条流不该收到收尾通知")
}

// ---------- 目标 1:停用 ----------

// Given 一条正在转发的映射,When 它被停用,Then 那条流立即关闭并留下一条带
// Disabled 领域码的收尾通知,而同一台设备上别的端口的流不受影响。
func TestRevoke_GivenALiveStream_WhenItsMappingIsDisabled_ThenOnlyThatStreamClosesAtOnce(t *testing.T) {
	gate, streams, rec, dialer, portA, portB := twoLiveStreams(t)

	_, err := gate.SetEnabled(context.Background(), &agentrewire.PortForwardSetEnabledRequest{
		Id: int64(portA), Enabled: false,
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool { return dialer.isClosed(portA) }, 5*time.Second, time.Millisecond,
		"停用一条正在转发的映射必须当场断掉它到本机服务的连接")
	closed := rec.waitClosedFor(t, "a")
	assert.Equal(t, "mapping_disabled", closed.GetReason())
	assert.Equal(t, int32(rpcerror.CodePortForwardDisabled), closed.GetCode(),
		"收尾码与 open 被拒时同一族:界面据它提示「把它打开」")

	assertOtherStreamUntouched(t, streams, rec, dialer, portB)
}

// ---------- 目标 2:删除 ----------

// Given 一条正在转发的映射,When 它被删除,Then 那条流立即关闭并留下一条带
// NotDeclared 领域码的收尾通知,而同一台设备上别的端口的流不受影响。
func TestRevoke_GivenALiveStream_WhenItsMappingIsDeleted_ThenOnlyThatStreamClosesAtOnce(t *testing.T) {
	gate, streams, rec, dialer, portA, portB := twoLiveStreams(t)

	deleted, err := gate.Delete(context.Background(), &agentrewire.PortForwardDeleteRequest{Id: int64(portA)})
	require.NoError(t, err)
	require.True(t, deleted.GetDeleted())

	require.Eventually(t, func() bool { return dialer.isClosed(portA) }, 5*time.Second, time.Millisecond,
		"删掉一条正在转发的映射必须当场断掉它到本机服务的连接")
	closed := rec.waitClosedFor(t, "a")
	assert.Equal(t, "mapping_removed", closed.GetReason())
	assert.Equal(t, int32(rpcerror.CodePortForwardNotDeclared), closed.GetCode(),
		"收尾码与 open 被拒时同一族:那个端口已经不在声明集里了")

	assertOtherStreamUntouched(t, streams, rec, dialer, portB)
}

// ---------- 目标 3:关掉之后再来的三个方法 ----------

// Given 一条因为映射被停用 / 删除而关掉的流,When 宿主还拿那个流号调 write / close /
// ack,Then 三个都回 StreamNotFound —— 与任务 4 已有的 close 语义同一句话。
//
// 「立即」在这里是可判定的:声明族那次调用一返回,这个流号就该已经不在流表里了。若要
// 等生产者 goroutine 自己醒过来才摘,宿主在那条窗口里拿到的会是一次成功的 write。
func TestRevoke_GivenARevokedStream_WhenTheHostKeepsUsingIt_ThenStreamNotFoundIsReported(t *testing.T) {
	for name, revoke := range map[string]func(*Handlers, int) error{
		"停用": func(gate *Handlers, port int) error {
			_, err := gate.SetEnabled(context.Background(), &agentrewire.PortForwardSetEnabledRequest{Id: int64(port), Enabled: false})
			return err
		},
		"删除": func(gate *Handlers, port int) error {
			_, err := gate.Delete(context.Background(), &agentrewire.PortForwardDeleteRequest{Id: int64(port)})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			gate, streams, rec, dialer, portA, portB := twoLiveStreams(t)
			require.NoError(t, revoke(gate, portA))

			ctx := context.Background()
			_, err := streams.Write(ctx, &agentrewire.PortForwardWriteRequest{StreamId: "a", Data: []byte("x")})
			assert.Equal(t, int32(rpcerror.CodePortForwardStreamNotFound), code(t, err))
			_, err = streams.Close(ctx, &agentrewire.PortForwardCloseRequest{StreamId: "a"})
			assert.Equal(t, int32(rpcerror.CodePortForwardStreamNotFound), code(t, err))
			_, err = streams.Ack(ctx, &agentrewire.PortForwardAckRequest{StreamId: "a", ConsumedBytes: 1})
			assert.Equal(t, int32(rpcerror.CodePortForwardStreamNotFound), code(t, err))

			assertOtherStreamUntouched(t, streams, rec, dialer, portB)
		})
	}
}

// ---------- 通路本身:连接收尾要能自己注销 ----------

// Given 一条连接上的流族已经收尾(CloseAll),When 此后又有声明被停用,Then 闸门手上
// 已经不再留着那份流表,而声明族照常答复。
//
// 后半句是行为,前半句只能白盒地问:一份走掉的流表继续挂在撤销面上不会答错任何一次
// 调用,它只是永远不被回收 —— 一台长跑的设备每接一条连接就漏一份,而任何黑盒断言都
// 看不见它。撤销面唯一能自己收尾的形状就是「订阅方注销自己」,所以这里直接钉引用数。
func TestRevoke_GivenTheCarryingConnectionIsGone_WhenItsStreamsAreTornDown_ThenTheGateDropsTheReference(t *testing.T) {
	gate, streams, _, _, portA, _ := twoLiveStreams(t)
	gate.revokeMu.Lock()
	watching := len(gate.revokers)
	gate.revokeMu.Unlock()
	require.Equal(t, 1, watching, "开着的那条连接必须挂在撤销面上,否则停用根本传不到流上")

	streams.CloseAll()

	gate.revokeMu.Lock()
	remaining := len(gate.revokers)
	gate.revokeMu.Unlock()
	assert.Zero(t, remaining, "连接收尾之后不该在闸门上留下引用 —— 每接一条连接漏一份")

	resp, err := gate.SetEnabled(context.Background(), &agentrewire.PortForwardSetEnabledRequest{
		Id: int64(portA), Enabled: false,
	})
	require.NoError(t, err)
	assert.False(t, resp.GetMapping().GetEnabled())
}

// ---------- 目标 4:改声明的是别的客户端时,这条连接也得知道 ----------
//
// 前面三条测的是「流当场断掉」。断流只够通知**正在用着这个端口的那一方**:一条流都
// 没开着的宿主收不到任何东西,而桌面端那条专属监听多半正闲着(用户开了标签页晾在
// 那儿)。规格「断开与失败」要的是「桌面端那条专属监听一并关掉」,所以撤销必须在
// 声明这一层也说一句 —— 一条按端口认人的 port_forward_revoked,发给每一条连接,
// 不论它此刻有没有流。
//
// 「发给每一条连接」这件事在这里落到:一份**没有任何流**的流表照样收得到。

// revokedEvents 挑出收到的全部撤销通知。
func (r *recorder) revokedEvents() []*agentrewire.PortForwardRevokedNotification {
	var out []*agentrewire.PortForwardRevokedNotification
	for _, event := range r.snapshot() {
		if event.kind == "revoked" {
			out = append(out, event.revoked)
		}
	}
	return out
}

func (r *recorder) waitRevoked(t *testing.T) []*agentrewire.PortForwardRevokedNotification {
	t.Helper()
	require.Eventually(t, func() bool { return len(r.revokedEvents()) > 0 }, 5*time.Second, time.Millisecond,
		"声明被停用 / 删除时,每一条连接都该收到一条撤销通知")
	return r.revokedEvents()
}

// Given 一条连接上一条流都没开着(桌面端那条监听正闲着),When 这条映射被**别的
// 客户端**停用 / 删除,Then 这条连接照样收到一条按端口认人的撤销通知,而同一台设备
// 上另一条映射的端口没有被撤销。
//
// 「另一条没有被撤销」是这里的真判据:一个「一改就把全部端口都广播一遍」的实现同样
// 能让前一条断言过,而桌面端照着它会把用户别的映射的监听一起关掉。
func TestRevoke_GivenAConnectionWithNoLiveStream_WhenAnotherClientRevokesTheMapping_ThenThatPortIsAnnouncedRevoked(t *testing.T) {
	for name, one := range map[string]struct {
		revoke func(*Handlers, int) error
		token  string
	}{
		"停用": {
			revoke: func(gate *Handlers, port int) error {
				_, err := gate.SetEnabled(context.Background(), &agentrewire.PortForwardSetEnabledRequest{Id: int64(port), Enabled: false})
				return err
			},
			token: "mapping_disabled",
		},
		"删除": {
			revoke: func(gate *Handlers, port int) error {
				_, err := gate.Delete(context.Background(), &agentrewire.PortForwardDeleteRequest{Id: int64(port)})
				return err
			},
			token: "mapping_removed",
		},
	} {
		t.Run(name, func(t *testing.T) {
			const portA, portB = 3000, 4000
			rec := newRecorder()
			gate, _ := revocableGate(t, nil, portA, portB)
			streams := NewStreams(StreamOptions{Gate: gate, Notify: rec.notify})
			t.Cleanup(streams.CloseAll)

			require.NoError(t, one.revoke(gate, portA))

			revoked := rec.waitRevoked(t)
			require.Len(t, revoked, 1, "一次撤销只该说一遍,而且只说被撤销的那个端口")
			assert.Equal(t, uint32(portA), revoked[0].GetPort())
			assert.Equal(t, one.token, revoked[0].GetReason(),
				"撤销的 token 与收尾通知取同一套词汇:同一件事不该有两种说法")
		})
	}
}

// Given 一条正开着流的连接,When 那条映射被停用,Then 它既收到那条流的收尾通知,也
// 收到这条映射的撤销通知 —— 两者是两件事(一条流没了 / 这条声明没了),不能靠一条顶
// 另一条:宿主关掉的是整条监听,而监听并不等于此刻那条流。
func TestRevoke_GivenALiveStream_WhenItsMappingIsDisabled_ThenBothTheStreamCloseAndThePortRevocationAreAnnounced(t *testing.T) {
	gate, _, rec, _, portA, portB := twoLiveStreams(t)

	_, err := gate.SetEnabled(context.Background(), &agentrewire.PortForwardSetEnabledRequest{
		Id: int64(portA), Enabled: false,
	})
	require.NoError(t, err)

	closed := rec.waitClosedFor(t, "a")
	assert.Equal(t, "mapping_disabled", closed.GetReason())

	revoked := rec.waitRevoked(t)
	require.Len(t, revoked, 1)
	assert.Equal(t, uint32(portA), revoked[0].GetPort())
	assert.NotEqual(t, uint32(portB), revoked[0].GetPort(), "别的映射的端口不该跟着被撤销")
}

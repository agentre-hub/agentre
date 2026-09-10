package portforwardhost_test

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/portforwardhost"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
	"github.com/agentre-hub/agentre/pkg/wire/wirelimits"
)

// 本文件钉的是这个 Handler **转发了什么**:请求怎么变成一次 open、三条通知怎么变回
// 一个 HTTP 响应、ack 回的是不是累计量、101 之后还解不解释内容。
//
// 判据全部落在真的一条 protorpc 连接上,而不是打桩的 open/notify —— 方法号配错、
// oneof 撞了、通知没被分流,打桩的用例照样绿。设备那一侧是剧本,所以本文件与宿主
// 无关;两端都是真的那一族用例(带请求体的 Expect: 100-continue、大响应不许截断)
// 需要真的设备侧实现,住在桌面仓的 internal/pkg/portforward 里。

// scriptedDevice 是设备那一侧:记下宿主发过来的每一次调用,并按用例的剧本回通知。
type scriptedDevice struct {
	conn *protorpc.Conn

	mu     sync.Mutex
	opens  []*agentrewire.PortForwardOpenRequest
	writes []*agentrewire.PortForwardWriteRequest
	acks   []uint64
	closes []string

	// bodyEOF 在请求体的那条 eof 到达时鸣一次,让剧本能等「上游把请求体读完了」
	// 再回响应 —— 真的 HTTP 服务就是这个次序。
	bodyEOF chan struct{}

	onOpen func(d *scriptedDevice, req *agentrewire.PortForwardOpenRequest) error
	// onAck 让剧本决定这一次 ack 怎么答。返回非 nil 就是设备回了个错 —— 生产上最常见
	// 的那一个是流已经收尾之后的 -32073。
	onAck func(d *scriptedDevice, req *agentrewire.PortForwardAckRequest) error
}

func scriptDevice(t *testing.T, conn *protorpc.Conn) *scriptedDevice {
	t.Helper()
	device := &scriptedDevice{conn: conn, bodyEOF: make(chan struct{}, 1)}
	registry := conn.Registry()
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_OPEN),
		func() *agentrewire.PortForwardOpenRequest { return &agentrewire.PortForwardOpenRequest{} },
		func(_ context.Context, req *agentrewire.PortForwardOpenRequest) (*agentrewire.PortForwardOpenResponse, error) {
			device.mu.Lock()
			device.opens = append(device.opens, req)
			hook := device.onOpen
			device.mu.Unlock()
			if hook != nil {
				if err := hook(device, req); err != nil {
					return nil, err
				}
			}
			return &agentrewire.PortForwardOpenResponse{StreamId: req.GetStreamId()}, nil
		})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_WRITE),
		func() *agentrewire.PortForwardWriteRequest { return &agentrewire.PortForwardWriteRequest{} },
		func(_ context.Context, req *agentrewire.PortForwardWriteRequest) (*agentrewire.Empty, error) {
			device.mu.Lock()
			device.writes = append(device.writes, req)
			device.mu.Unlock()
			if req.GetEof() {
				select {
				case device.bodyEOF <- struct{}{}:
				default:
				}
			}
			return &agentrewire.Empty{}, nil
		})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_ACK),
		func() *agentrewire.PortForwardAckRequest { return &agentrewire.PortForwardAckRequest{} },
		func(_ context.Context, req *agentrewire.PortForwardAckRequest) (*agentrewire.Empty, error) {
			device.mu.Lock()
			device.acks = append(device.acks, req.GetConsumedBytes())
			hook := device.onAck
			device.mu.Unlock()
			if hook != nil {
				if err := hook(device, req); err != nil {
					return nil, err
				}
			}
			return &agentrewire.Empty{}, nil
		})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_CLOSE),
		func() *agentrewire.PortForwardCloseRequest { return &agentrewire.PortForwardCloseRequest{} },
		func(_ context.Context, req *agentrewire.PortForwardCloseRequest) (*agentrewire.Empty, error) {
			device.mu.Lock()
			device.closes = append(device.closes, req.GetStreamId())
			device.mu.Unlock()
			return &agentrewire.Empty{}, nil
		})
	return device
}

func (d *scriptedDevice) head(streamID string, status int32, header map[string][]string, upgraded bool) {
	headers := map[string]*agentrewire.HeaderValues{}
	for name, values := range header {
		headers[name] = &agentrewire.HeaderValues{Values: values}
	}
	_ = d.conn.Notify(&agentrewire.RpcNotification{
		Payload: &agentrewire.RpcNotification_PortForwardResponse{
			PortForwardResponse: &agentrewire.PortForwardResponseNotification{
				StreamId: streamID, Status: status, Headers: headers, Upgraded: upgraded,
			},
		},
	})
}

func (d *scriptedDevice) data(streamID string, payload []byte) {
	_ = d.conn.Notify(&agentrewire.RpcNotification{
		Payload: &agentrewire.RpcNotification_PortForwardData{
			PortForwardData: &agentrewire.PortForwardDataNotification{StreamId: streamID, Data: payload},
		},
	})
}

func (d *scriptedDevice) finish(streamID, reason string) {
	_ = d.conn.Notify(&agentrewire.RpcNotification{
		Payload: &agentrewire.RpcNotification_PortForwardClosed{
			PortForwardClosed: &agentrewire.PortForwardClosedNotification{StreamId: streamID, Reason: reason},
		},
	})
}

func (d *scriptedDevice) snapshot() (opens []*agentrewire.PortForwardOpenRequest, writes []*agentrewire.PortForwardWriteRequest, acks []uint64, closes []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]*agentrewire.PortForwardOpenRequest(nil), d.opens...),
		append([]*agentrewire.PortForwardWriteRequest(nil), d.writes...),
		append([]uint64(nil), d.acks...),
		append([]string(nil), d.closes...)
}

// openForward 起一台假设备 + 一条把请求交给 Proxy 的 HTTP 服务,返回它的地址与设备
// 那一侧的剧本。
//
// 宿主怎么把请求送到这个 Handler 上(桌面端绑一条 127.0.0.1 的专属监听,控制台开一条
// /fw/<设备>/<端口> 的路由)不在本包的判定范围内,所以这里用最朴素的一个 httptest 服务。
func openForward(t *testing.T, port int) (string, *scriptedDevice) {
	t.Helper()
	hostConn, deviceConn := connPair(t)
	device := scriptDevice(t, deviceConn)
	proxy := portforwardhost.NewProxy(hostConn, uint32(port), nil)
	t.Cleanup(proxy.Close)
	server := httptest.NewServer(proxy)
	t.Cleanup(server.Close)
	return server.URL, device
}

// Given 浏览器打到这条专属监听上的一个 GET, When 设备按序回响应头与两块正文,
// Then 浏览器拿到同一个状态码、同一批响应头与逐字拼起来的正文;而设备收到的 open
// 里带着**原样的**路径(含查询串、不剥前缀)与这条映射的端口。
//
// 「不剥前缀」是桌面端与控制台的分水岭(规格决策 3):控制台要剥 /fw/<设备>/<端口>,
// 桌面端这条路根本没有前缀,多剥一层会把 /assets/x.js 变成 /x.js。
func TestForward_GivenABrowserGET_WhenTheDeviceStreamsBack_ThenTheHeadAndEveryChunkArriveVerbatim(t *testing.T) {
	t.Parallel()
	address, device := openForward(t, 5173)
	device.onOpen = func(d *scriptedDevice, req *agentrewire.PortForwardOpenRequest) error {
		go func() {
			d.head(req.GetStreamId(), 201, map[string][]string{"Content-Type": {"text/html"}, "X-Twice": {"a", "b"}}, false)
			d.data(req.GetStreamId(), []byte("<!doctype html>"))
			d.data(req.GetStreamId(), []byte("hello"))
			d.finish(req.GetStreamId(), "eof")
		}()
		return nil
	}

	resp, err := http.Get(address + "/assets/x.js?v=2&q=a%20b") //nolint:noctx // 用例里的一次同步请求
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, 201, resp.StatusCode)
	assert.Equal(t, "text/html", resp.Header.Get("Content-Type"))
	assert.Equal(t, []string{"a", "b"}, resp.Header.Values("X-Twice"))
	assert.Equal(t, "<!doctype html>hello", string(body))

	opens, _, _, _ := device.snapshot()
	require.Len(t, opens, 1)
	assert.Equal(t, "GET", opens[0].GetMethod())
	assert.Equal(t, "/assets/x.js?v=2&q=a%20b", opens[0].GetPath(), "路径与查询串必须原样送到设备")
	assert.EqualValues(t, 5173, opens[0].GetPort())
	assert.False(t, opens[0].GetHasBody())
	assert.NotEmpty(t, opens[0].GetStreamId())
	assert.LessOrEqual(t, len(opens[0].GetStreamId()), 128)
}

// Given 一个比信用窗口还大的响应, When 宿主边收边写给浏览器, Then 它回的 ack 是
// **累计**已消费字节数:严格递增,末一条覆盖到接近全长。
//
// 回增量的话设备侧会把窗口当成一直没被消费,在窗口处停读,大文件下载卡死在 4 MiB。
func TestForward_GivenAResponseLargerThanTheWindow_ThenTheHostAcksCumulativeConsumedBytes(t *testing.T) {
	t.Parallel()
	address, device := openForward(t, 5173)

	const chunk = 64 << 10
	total := int(wirelimits.PortForwardWindowBytes) / 2 * 3 // 1.5 个窗口
	device.onOpen = func(d *scriptedDevice, req *agentrewire.PortForwardOpenRequest) error {
		go func() {
			d.head(req.GetStreamId(), 200, nil, false)
			for sent := 0; sent < total; sent += chunk {
				d.data(req.GetStreamId(), bytes.Repeat([]byte{'x'}, chunk))
			}
			d.finish(req.GetStreamId(), "eof")
		}()
		return nil
	}

	resp, err := http.Get(address + "/big.bin") //nolint:noctx // 用例里的一次同步请求
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	written, err := io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	assert.EqualValues(t, total, written)

	_, _, acks, _ := device.snapshot()
	require.NotEmpty(t, acks, "整个窗口都消费完了却一条 ack 都没回:设备侧会在窗口处停读")
	for i := 1; i < len(acks); i++ {
		assert.Greater(t, acks[i], acks[i-1], "ack 回的是增量而不是累计已消费字节数")
	}
	assert.GreaterOrEqual(t, acks[len(acks)-1], uint64(wirelimits.PortForwardWindowBytes),
		"末一条 ack 还没盖过一个窗口,设备侧此刻仍然在停读")
}

// Given 一次 ack 被设备以「这条流已经不在了」(-32073)回绝,而宿主手上还压着没写给
// 浏览器的正文, When 这条流照常收尾, Then 浏览器仍然收到**全部**正文。
//
// 这不是个假想的次序,它是正常收尾的必经之路:设备侧**先把流从表里摘掉,再发 closed**
// (internal/daemon/portforward/stream.go 的 pump,那个次序本身是对的 —— 宿主收到 closed
// 之后再拿这个流号来调什么,该得的就是 StreamNotFound)。于是流一结束,还飞在路上的
// ack 一律得 -32073;而真链路上收尾通知排在好几兆已发未达的数据帧后面,宿主此刻手上
// 必然还压着一大段没写出去。
//
// ack 只是流控,不是数据通路 —— 它失败了不代表这些字节不该写给浏览器。把它当致命错误
// 就地收摊,用户拿到的是一个状态码 200、Content-Length 也对、正文却短了几百 KB 的响应
// (curl 报 exit 18 / CURLE_PARTIAL_FILE)。
func TestForward_GivenAnAckRejectedBecauseTheStreamAlreadyEnded_ThenTheClientStillGetsEveryByte(t *testing.T) {
	t.Parallel()
	address, device := openForward(t, 5173)

	const chunk = 64 << 10
	// 头一段要盖过一个 ack 周期(窗口的四分之一),否则第一次 ack 根本不会发出来。
	const firstHalf = int(wirelimits.PortForwardWindowBytes) / 2
	const secondHalf = int(wirelimits.PortForwardWindowBytes) / 4

	// 有 pattern 的填充:只断长度的话,一个补零凑数的实现照样过。
	fill := func(offset, size int) []byte {
		out := make([]byte, size)
		for i := range out {
			out[i] = byte((offset + i) * 7 % 251)
		}
		return out
	}
	want := fill(0, firstHalf+secondHalf)

	// 前半段发完才轮到后半段:一条真的流上,正文与收尾通知是**同一个** goroutine 顺着
	// 发出来的。两段各发各的就会串到一起,收尾通知插到前半段中间去 —— 那是剧本自己的
	// 错,不是被测代码的。
	firstDone := make(chan struct{})
	var rest sync.Once
	device.onOpen = func(d *scriptedDevice, req *agentrewire.PortForwardOpenRequest) error {
		go func() {
			defer close(firstDone)
			d.head(req.GetStreamId(), 200, map[string][]string{
				"Content-Length": {strconv.Itoa(len(want))},
			}, false)
			for sent := 0; sent < firstHalf; sent += chunk {
				d.data(req.GetStreamId(), want[sent:sent+chunk])
			}
		}()
		return nil
	}
	device.onAck = func(d *scriptedDevice, req *agentrewire.PortForwardAckRequest) error {
		// 第一次 ack 落地的这一刻,把剩下的正文与收尾通知都发出去 —— 它们走的是同一条
		// 连接,排在这次 ack 的错误应答前面,所以宿主收到错误时,那些字节**已经在**它
		// 自己的队列里了。丢不丢,全看它拿这个错误怎么办。
		rest.Do(func() {
			<-firstDone
			for sent := firstHalf; sent < len(want); sent += chunk {
				d.data(req.GetStreamId(), want[sent:min(sent+chunk, len(want))])
			}
			d.finish(req.GetStreamId(), "eof")
		})
		return &protorpc.Error{
			Code:    rpcerror.CodePortForwardStreamNotFound,
			Message: "port forward: no such stream",
		}
	}

	resp, err := http.Get(address + "/big.bin") //nolint:noctx // 用例里的一次同步请求
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var got bytes.Buffer
	n, copyErr := io.Copy(&got, resp.Body)
	require.NoError(t, copyErr,
		"正文在第 %d 字节处断了(一共 %d 字节):一次被回绝的 ack 把还没写出去的正文丢掉了", n, len(want))
	require.EqualValues(t, len(want), n, "客户端收到的字节数与设备发出的对不上")
	assert.True(t, bytes.Equal(want, got.Bytes()), "客户端收到的正文与设备发出的不是逐字相同")
}

// Given 设备拒绝这次 open, When 三种拒绝各来一次, Then 浏览器上是三个各不相同的
// 答复。
//
// 三件事的出路完全不同:把端口填对 / 把这条映射启用回来 / 去那台机器上把服务起起来。
// 折成同一句「转发失败」用户就不知道该做哪件事。分支按**码**走,不按 message 文本 ——
// 文案一改就静默失灵。
func TestForward_GivenTheDeviceRefusesTheOpen_ThenEachRefusalBecomesItsOwnAnswer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		code   int32
		status int
	}{
		{name: "端口没声明", code: rpcerror.CodePortForwardNotDeclared, status: http.StatusNotFound},
		{name: "映射已停用", code: rpcerror.CodePortForwardDisabled, status: http.StatusForbidden},
		{name: "端口上没有服务", code: rpcerror.CodePortForwardNoListener, status: http.StatusBadGateway},
	}
	bodies := map[string]string{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			address, device := openForward(t, 5173)
			device.onOpen = func(_ *scriptedDevice, _ *agentrewire.PortForwardOpenRequest) error {
				return &protorpc.Error{Code: tc.code, Message: "port forward: refused"}
			}
			resp, err := http.Get(address + "/") //nolint:noctx // 用例里的一次同步请求
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			assert.Equal(t, tc.status, resp.StatusCode)
			assert.NotContains(t, string(body), "port forward: refused",
				"把设备那句英文原话贴给用户了 —— 码要翻成一句说得清下一步的话")
			assert.NotEmpty(t, strings.TrimSpace(string(body)))
			bodies[tc.name] = strings.TrimSpace(string(body))
		})
	}
	assert.Len(t, uniqueValues(bodies), len(cases), "三种拒绝说的是同一句话,用户分不出该做哪件事")
}

func uniqueValues(m map[string]string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, v := range m {
		out[v] = struct{}{}
	}
	return out
}

// Given 一个带请求体的 POST, When 宿主把它送上去, Then 设备按序收到那些字节,末尾
// 是一条 eof —— 而且**上一块的应答回来之前不会发下一块**(上行没有单独信用,这次调用
// 的应答就是这一块的凭据)。
func TestForward_GivenARequestBody_ThenItGoesUpInOrderAndEndsWithEof(t *testing.T) {
	t.Parallel()
	address, device := openForward(t, 5173)
	device.onOpen = func(d *scriptedDevice, req *agentrewire.PortForwardOpenRequest) error {
		go func() {
			// 先把请求体读完再回响应 —— 上游服务的真实次序。响应先回来的话宿主本来
			// 就该停止上传(浏览器那一侧的取消),那是另一件事。
			<-d.bodyEOF
			d.head(req.GetStreamId(), 201, map[string][]string{"X-Answered": {"yes"}}, false)
			d.data(req.GetStreamId(), []byte("upstream-answer"))
			d.finish(req.GetStreamId(), "eof")
		}()
		return nil
	}

	payload := strings.Repeat("upload-", 20000)
	resp, err := http.Post(address+"/upload", "text/plain", strings.NewReader(payload)) //nolint:noctx // 用例里的一次同步请求
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	answer, rerr := io.ReadAll(resp.Body)
	require.NoError(t, rerr)

	// 上行读完**不是**这条流的收尾:上游正是在把请求体读完之后才答复的,所以响应头、
	// 响应体、状态码都还得原样到浏览器手上。少了这三条断言,「上行一到头就把整条流
	// cancel 掉」这个缺陷会让每一个带请求体的 POST 拿到空的 200,而用例照样绿。
	assert.Equal(t, 201, resp.StatusCode, "带请求体的请求丢了上游的状态码")
	assert.Equal(t, "yes", resp.Header.Get("X-Answered"), "带请求体的请求丢了上游的响应头")
	assert.Equal(t, "upstream-answer", string(answer), "带请求体的请求丢了上游的响应体")

	require.Eventually(t, func() bool {
		_, writes, _, _ := device.snapshot()
		return len(writes) > 0 && writes[len(writes)-1].GetEof()
	}, 5*time.Second, 10*time.Millisecond, "请求体没有以一条 eof 收尾:设备那一侧的上游写方向永远半关不掉")

	opens, writes, _, _ := device.snapshot()
	require.Len(t, opens, 1)
	assert.True(t, opens[0].GetHasBody(), "有请求体却没把 has_body 置上:设备会立刻半关上游写方向")
	var uploaded bytes.Buffer
	for _, w := range writes {
		uploaded.Write(w.GetData())
	}
	assert.Equal(t, payload, uploaded.String())
}

// Given 被转发的服务回 101, When 设备把 upgraded 位置上, Then 宿主把这条连接交出去,
// 此后两个方向都只搬字节。
//
// upgraded 独立于 status:消费者据它切换的是**自己这一侧**的行为,而不是复述一个数字。
func TestForward_GivenAProtocolUpgrade_ThenBothDirectionsCarryRawBytes(t *testing.T) {
	t.Parallel()
	address, device := openForward(t, 5173)
	streamIDs := make(chan string, 1)
	device.onOpen = func(d *scriptedDevice, req *agentrewire.PortForwardOpenRequest) error {
		go func() {
			d.head(req.GetStreamId(), 101, map[string][]string{
				"Upgrade": {"websocket"}, "Connection": {"Upgrade"},
			}, true)
			streamIDs <- req.GetStreamId()
		}()
		return nil
	}

	parsed, err := url.Parse(address)
	require.NoError(t, err)
	conn, err := net.DialTimeout("tcp", parsed.Host, 3*time.Second)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = conn.Write([]byte("GET /ws HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"))
	require.NoError(t, err)

	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Contains(t, statusLine, "101")
	for {
		line, rerr := reader.ReadString('\n')
		require.NoError(t, rerr)
		if strings.TrimSpace(line) == "" {
			break
		}
	}

	streamID := <-streamIDs
	// 设备 → 浏览器:升级之后的字节不再被解释成 HTTP。
	device.data(streamID, []byte{0x81, 0x03, 'h', 'e', 'y'})
	frame := make([]byte, 5)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = io.ReadFull(reader, frame)
	require.NoError(t, err)
	assert.Equal(t, []byte{0x81, 0x03, 'h', 'e', 'y'}, frame)

	// 浏览器 → 设备。
	_, err = conn.Write([]byte{0x88, 0x02, 0x03, 0xe8})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_, writes, _, _ := device.snapshot()
		var up bytes.Buffer
		for _, w := range writes {
			up.Write(w.GetData())
		}
		return bytes.Equal(up.Bytes(), []byte{0x88, 0x02, 0x03, 0xe8})
	}, 5*time.Second, 10*time.Millisecond, "升级之后浏览器发上来的字节没有原样交给设备")
}

// Given 浏览器中途断开(关标签 / 取消下载), When 宿主发觉, Then 它给设备发一条
// close —— 否则设备那一侧到本机服务的连接就悬在那里。
func TestForward_GivenTheBrowserGoesAway_ThenTheHostClosesTheStreamOnTheDevice(t *testing.T) {
	t.Parallel()
	address, device := openForward(t, 5173)
	started := make(chan struct{}, 1)
	device.onOpen = func(d *scriptedDevice, req *agentrewire.PortForwardOpenRequest) error {
		go func() {
			d.head(req.GetStreamId(), 200, nil, false)
			d.data(req.GetStreamId(), []byte("first"))
			started <- struct{}{}
			// 之后什么都不发:这条流对设备来说还开着。
		}()
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"/slow", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	<-started
	buf := make([]byte, 5)
	_, _ = io.ReadFull(resp.Body, buf)
	assert.Equal(t, "first", string(buf))
	cancel()
	_ = resp.Body.Close()

	require.Eventually(t, func() bool {
		_, _, _, closes := device.snapshot()
		return len(closes) == 1
	}, 5*time.Second, 10*time.Millisecond, "浏览器断了却没给设备发 close:设备侧那条本机连接悬着")
}

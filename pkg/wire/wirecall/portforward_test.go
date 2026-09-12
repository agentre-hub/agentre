package wirecall_test

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
	"github.com/agentre-hub/agentre/pkg/wire/wirelimits"
)

// 本文件把 portforward.* 一族打在**真的一条 protorpc 连接**上:两端各是一个
// protorpc.Conn,中间是一对内存管道。只断言结构体字段的用例证不了这一族真的过得了
// 线 —— 方法号配错、oneof 字段号撞了、通知没被派发,那样的用例照样绿。

// pfPipe 是一条内存帧管道的一端,与 protorpc 自己的用例里那个同形。
type pfPipe struct {
	in   chan []byte
	out  chan []byte
	done chan struct{}
	once *sync.Once
}

func pfPipePair() (*pfPipe, *pfPipe) {
	a, b := make(chan []byte, 64), make(chan []byte, 64)
	done := make(chan struct{})
	once := &sync.Once{}
	return &pfPipe{in: a, out: b, done: done, once: once},
		&pfPipe{in: b, out: a, done: done, once: once}
}

func (p *pfPipe) ReadFrame() ([]byte, error) {
	select {
	case b := <-p.in:
		return b, nil
	case <-p.done:
		return nil, io.EOF
	}
}

func (p *pfPipe) WriteFrame(b []byte) error {
	select {
	case p.out <- append([]byte(nil), b...):
		return nil
	case <-p.done:
		return io.EOF
	}
}

func (p *pfPipe) Close() error          { p.once.Do(func() { close(p.done) }); return nil }
func (p *pfPipe) Done() <-chan struct{} { return p.done }

// pfConnPair 起一对已经在跑的连接:client 是调用方(宿主),device 是被访问的设备。
func pfConnPair(t *testing.T) (client, device *protorpc.Conn) {
	t.Helper()

	clientTransport, deviceTransport := pfPipePair()
	client = protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	device = protorpc.NewConn(deviceTransport, protorpc.NewRegistry())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go client.Serve(ctx)
	go device.Serve(ctx)
	return client, device
}

// Given 一台设备持有它自己那份端口映射声明,When 宿主经一条连接列举 / 新增 / 启停 /
// 删除,Then 每一格(端口、名称、启用位、时间戳)都逐字往返。
//
// 声明族是「谁被转发谁持有」这条决策在协议上的落点:宿主手上没有这张表,只能问那台
// 机器,所以这四个方法的每一格都必须真的过得了线。
func TestPortForwardDeclaration_GivenADeviceHoldingItsOwnMappings_WhenTheHostCallsTheFamily_ThenEveryFieldRoundTrips(t *testing.T) {
	t.Parallel()

	client, device := pfConnPair(t)
	ctx := context.Background()

	stored := &agentrewire.PortForwardMapping{
		Id:         7,
		Port:       3000,
		Name:       "dev server",
		Enabled:    true,
		Createtime: 1757000000,
		Updatetime: 1757000001,
	}

	protorpc.RegisterMethod(device.Registry(),
		uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_LIST),
		func() *agentrewire.PortForwardListRequest { return &agentrewire.PortForwardListRequest{} },
		func(_ context.Context, _ *agentrewire.PortForwardListRequest) (*agentrewire.PortForwardListResponse, error) {
			return &agentrewire.PortForwardListResponse{
				Mappings: []*agentrewire.PortForwardMapping{stored},
			}, nil
		})
	protorpc.RegisterMethod(device.Registry(),
		uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_CREATE),
		func() *agentrewire.PortForwardCreateRequest { return &agentrewire.PortForwardCreateRequest{} },
		func(_ context.Context, request *agentrewire.PortForwardCreateRequest) (*agentrewire.PortForwardCreateResponse, error) {
			return &agentrewire.PortForwardCreateResponse{Mapping: &agentrewire.PortForwardMapping{
				Id: 9, Port: request.GetPort(), Name: request.GetName(),
				Enabled: true, Createtime: 1757000010, Updatetime: 1757000010,
			}}, nil
		})
	protorpc.RegisterMethod(device.Registry(),
		uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_SET_ENABLED),
		func() *agentrewire.PortForwardSetEnabledRequest {
			return &agentrewire.PortForwardSetEnabledRequest{}
		},
		func(_ context.Context, request *agentrewire.PortForwardSetEnabledRequest) (*agentrewire.PortForwardSetEnabledResponse, error) {
			return &agentrewire.PortForwardSetEnabledResponse{Mapping: &agentrewire.PortForwardMapping{
				Id: request.GetId(), Port: 3000, Name: "dev server",
				Enabled: request.GetEnabled(), Createtime: 1757000000, Updatetime: 1757000020,
			}}, nil
		})
	protorpc.RegisterMethod(device.Registry(),
		uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_DELETE),
		func() *agentrewire.PortForwardDeleteRequest { return &agentrewire.PortForwardDeleteRequest{} },
		func(_ context.Context, request *agentrewire.PortForwardDeleteRequest) (*agentrewire.PortForwardDeleteResponse, error) {
			return &agentrewire.PortForwardDeleteResponse{Deleted: request.GetId() == 7}, nil
		})

	listed, err := wirecall.PortForwardList(ctx, wirecall.On(client), &agentrewire.PortForwardListRequest{})
	require.NoError(t, err)
	require.Len(t, listed.GetMappings(), 1)
	got := listed.GetMappings()[0]
	require.EqualValues(t, 7, got.GetId())
	require.EqualValues(t, 3000, got.GetPort())
	require.Equal(t, "dev server", got.GetName())
	require.True(t, got.GetEnabled())
	require.EqualValues(t, 1757000000, got.GetCreatetime())
	require.EqualValues(t, 1757000001, got.GetUpdatetime())

	created, err := wirecall.PortForwardCreate(ctx, wirecall.On(client),
		&agentrewire.PortForwardCreateRequest{Port: 5173, Name: "vite"})
	require.NoError(t, err)
	require.EqualValues(t, 5173, created.GetMapping().GetPort())
	require.Equal(t, "vite", created.GetMapping().GetName())
	require.True(t, created.GetMapping().GetEnabled())

	// 停用是「保留声明、拒绝访问」,所以 false 必须是一个**送得出去的值**,不能靠
	// 「省略即不改」表达。
	toggled, err := wirecall.PortForwardSetEnabled(ctx, wirecall.On(client),
		&agentrewire.PortForwardSetEnabledRequest{Id: 7, Enabled: false})
	require.NoError(t, err)
	require.False(t, toggled.GetMapping().GetEnabled())
	require.EqualValues(t, 7, toggled.GetMapping().GetId())

	deleted, err := wirecall.PortForwardDelete(ctx, wirecall.On(client),
		&agentrewire.PortForwardDeleteRequest{Id: 7})
	require.NoError(t, err)
	require.True(t, deleted.GetDeleted())
}

// Given 一条转发流,When 设备把「状态码与响应头」和随后的 body 分片经通知带回,
// Then 宿主先收到响应头、再按发送次序收到每一块 body,最后收到收尾通知。
//
// 时序是这一族的要害:HTTP 的 WriteHeader 必须先于第一块 body,晚一步宿主就只能
// 补一个它自己编的状态码。响应头因此是**独立的一条通知**而不是 open 的应答 ——
// 应答走的是 pending channel、通知走读循环,两条路的先后没有保证,而三条通知同在
// 读循环上按序派发。
func TestPortForwardStream_GivenADeviceStreamingAResponse_WhenTheHostConsumesIt_ThenTheHeadPrecedesEveryBodyChunk(t *testing.T) {
	t.Parallel()

	client, device := pfConnPair(t)
	ctx := context.Background()

	type event struct {
		kind string
		data []byte
	}
	var mu sync.Mutex
	var events []event
	var head *agentrewire.PortForwardResponseNotification
	var closed *agentrewire.PortForwardClosedNotification
	client.Registry().SubscribeNotification(func(_ context.Context, notification *agentrewire.RpcNotification) error {
		mu.Lock()
		defer mu.Unlock()
		switch payload := notification.GetPayload().(type) {
		case *agentrewire.RpcNotification_PortForwardResponse:
			head = payload.PortForwardResponse
			events = append(events, event{kind: "head"})
		case *agentrewire.RpcNotification_PortForwardData:
			events = append(events, event{kind: "data", data: payload.PortForwardData.GetData()})
		case *agentrewire.RpcNotification_PortForwardClosed:
			closed = payload.PortForwardClosed
			events = append(events, event{kind: "closed"})
		}
		return nil
	})

	var openRequest *agentrewire.PortForwardOpenRequest
	protorpc.RegisterMethod(device.Registry(),
		uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_OPEN),
		func() *agentrewire.PortForwardOpenRequest { return &agentrewire.PortForwardOpenRequest{} },
		func(_ context.Context, request *agentrewire.PortForwardOpenRequest) (*agentrewire.PortForwardOpenResponse, error) {
			openRequest = request
			return &agentrewire.PortForwardOpenResponse{StreamId: request.GetStreamId()}, nil
		})

	opened, err := wirecall.PortForwardOpen(ctx, wirecall.On(client), &agentrewire.PortForwardOpenRequest{
		StreamId: "stream-1",
		Port:     3000,
		Method:   "GET",
		Path:     "/assets/app.js?v=2",
		Headers: map[string]*agentrewire.HeaderValues{
			"Accept-Encoding": {Values: []string{"gzip", "br"}},
		},
		WindowBytes: uint64(wirelimits.PortForwardWindowBytes),
	})
	require.NoError(t, err)
	require.Equal(t, "stream-1", opened.GetStreamId())
	require.Equal(t, "/assets/app.js?v=2", openRequest.GetPath())
	require.Equal(t, []string{"gzip", "br"}, openRequest.GetHeaders()["Accept-Encoding"].GetValues())
	require.EqualValues(t, wirelimits.PortForwardWindowBytes, openRequest.GetWindowBytes())

	require.NoError(t, device.Notify(&agentrewire.RpcNotification{
		Payload: &agentrewire.RpcNotification_PortForwardResponse{
			PortForwardResponse: &agentrewire.PortForwardResponseNotification{
				StreamId: "stream-1",
				Status:   200,
				Headers: map[string]*agentrewire.HeaderValues{
					"Content-Type": {Values: []string{"application/javascript"}},
				},
			},
		},
	}))
	for _, chunk := range [][]byte{{0x00, 0x01}, {0x02, 0x03}} {
		require.NoError(t, device.Notify(&agentrewire.RpcNotification{
			Payload: &agentrewire.RpcNotification_PortForwardData{
				PortForwardData: &agentrewire.PortForwardDataNotification{
					StreamId: "stream-1", Data: chunk,
				},
			},
		}))
	}
	require.NoError(t, device.Notify(&agentrewire.RpcNotification{
		Payload: &agentrewire.RpcNotification_PortForwardClosed{
			PortForwardClosed: &agentrewire.PortForwardClosedNotification{
				StreamId: "stream-1", Reason: "eof", Message: "响应已完整送达",
			},
		},
	}))

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(events) == 4
	}, 2*time.Second, time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, "head", events[0].kind, "响应头必须先于任何一块 body 到达")
	require.Equal(t, "data", events[1].kind)
	require.Equal(t, []byte{0x00, 0x01}, events[1].data)
	require.Equal(t, "data", events[2].kind)
	require.Equal(t, []byte{0x02, 0x03}, events[2].data, "body 分片按发送次序到达,一块都不许丢")
	require.Equal(t, "closed", events[3].kind)
	require.EqualValues(t, 200, head.GetStatus())
	require.Equal(t, []string{"application/javascript"}, head.GetHeaders()["Content-Type"].GetValues())
	require.False(t, head.GetUpgraded())
	require.Equal(t, "eof", closed.GetReason())
	require.Zero(t, closed.GetCode(), "正常收尾不带领域错误码")
}

// Given 一次 101 升级,When 设备如实报出它,Then 宿主能把「此后不再是 HTTP」这件事
// 从状态码之外单独读出来,并在两个方向上继续搬纯字节。
func TestPortForwardStream_GivenAProtocolUpgrade_WhenTheDeviceReportsIt_ThenBothDirectionsCarryRawBytes(t *testing.T) {
	t.Parallel()

	client, device := pfConnPair(t)
	ctx := context.Background()

	var mu sync.Mutex
	var upgraded bool
	var inbound [][]byte
	client.Registry().SubscribeNotification(func(_ context.Context, notification *agentrewire.RpcNotification) error {
		mu.Lock()
		defer mu.Unlock()
		switch payload := notification.GetPayload().(type) {
		case *agentrewire.RpcNotification_PortForwardResponse:
			upgraded = payload.PortForwardResponse.GetUpgraded()
		case *agentrewire.RpcNotification_PortForwardData:
			inbound = append(inbound, payload.PortForwardData.GetData())
		}
		return nil
	})

	var written [][]byte
	var writeEOF bool
	protorpc.RegisterMethod(device.Registry(),
		uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_WRITE),
		func() *agentrewire.PortForwardWriteRequest { return &agentrewire.PortForwardWriteRequest{} },
		func(_ context.Context, request *agentrewire.PortForwardWriteRequest) (*agentrewire.Empty, error) {
			mu.Lock()
			defer mu.Unlock()
			written = append(written, request.GetData())
			writeEOF = writeEOF || request.GetEof()
			return &agentrewire.Empty{}, nil
		})

	require.NoError(t, device.Notify(&agentrewire.RpcNotification{
		Payload: &agentrewire.RpcNotification_PortForwardResponse{
			PortForwardResponse: &agentrewire.PortForwardResponseNotification{
				StreamId: "stream-ws",
				Status:   101,
				Headers: map[string]*agentrewire.HeaderValues{
					"Upgrade": {Values: []string{"websocket"}},
				},
				Upgraded: true,
			},
		},
	}))
	require.NoError(t, device.Notify(&agentrewire.RpcNotification{
		Payload: &agentrewire.RpcNotification_PortForwardData{
			PortForwardData: &agentrewire.PortForwardDataNotification{
				StreamId: "stream-ws", Data: []byte{0x81, 0x03, 'h', 'm', 'r'},
			},
		},
	}))

	_, err := wirecall.PortForwardWrite(ctx, wirecall.On(client), &agentrewire.PortForwardWriteRequest{
		StreamId: "stream-ws", Data: []byte{0x88, 0x00}, Eof: true,
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return upgraded && len(inbound) == 1
	}, 2*time.Second, time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	require.True(t, upgraded, "101 之后不再是 HTTP,这一位必须独立于状态码可判定")
	require.Equal(t, []byte{0x81, 0x03, 'h', 'm', 'r'}, inbound[0])
	require.Equal(t, [][]byte{{0x88, 0x00}}, written)
	require.True(t, writeEOF)
}

// Given 消费者跟不上,When 它把「已消费到第几个字节」显式回给生产者,Then 生产者读得到
// 这个累计数并据此决定还读不读本机 socket。
//
// 这是本族**唯一没有先例**的一格,所以它必须在协议里有位置:终端那一族的流控是有损的
// (队列满了丢最老的一块),HTTP 响应体丢一块就是文件损坏;而靠阻塞反压会顶死
// protorpc 的全连接写锁与读循环,同一条连接上别的会话跟着一起停。剩下的只有应用层
// 的窗口/信用 —— 累计已消费字节数由消费者回报,未确认量超过窗口时生产者停止从本机
// socket 读,让内核的 TCP 窗口把压力还给被转发的那个服务。
func TestPortForwardStream_GivenAConsumerFallingBehind_WhenItReportsConsumedBytes_ThenTheProducerReadsTheCumulativeCredit(t *testing.T) {
	t.Parallel()

	client, device := pfConnPair(t)
	ctx := context.Background()

	var mu sync.Mutex
	var acked []uint64
	var ackedStreams []string
	protorpc.RegisterMethod(device.Registry(),
		uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_ACK),
		func() *agentrewire.PortForwardAckRequest { return &agentrewire.PortForwardAckRequest{} },
		func(_ context.Context, request *agentrewire.PortForwardAckRequest) (*agentrewire.Empty, error) {
			mu.Lock()
			defer mu.Unlock()
			// 只记录,断言留到下面的测试 goroutine 上。handler 跑在 protorpc 为每次
			// 调用起的那个 goroutine 上,在这里 require 会 Goexit 掉它 —— 应答帧因此
			// 永远发不出去,而调用方那次 PortForwardAck 用的是不带 deadline 的
			// context.Background(),于是一次真的 stream_id 回归会变成一个包超时的
			// panic,而不是一条读得懂的红。
			ackedStreams = append(ackedStreams, request.GetStreamId())
			acked = append(acked, request.GetConsumedBytes())
			return &agentrewire.Empty{}, nil
		})

	for _, consumed := range []uint64{262144, 1048576} {
		_, err := wirecall.PortForwardAck(ctx, wirecall.On(client), &agentrewire.PortForwardAckRequest{
			StreamId: "stream-1", ConsumedBytes: consumed,
		})
		require.NoError(t, err)
	}

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"stream-1", "stream-1"}, ackedStreams,
		"ack 必须指名它结的是哪一条流的账 —— 同一条连接上并行跑着多条流")
	require.Equal(t, []uint64{262144, 1048576}, acked,
		"回报的是**累计**已消费字节数,而不是增量 —— 丢一条 ack 只会让窗口暂时偏紧,不会让两端的账永久对不上")

	// 窗口与分片都必须远低于载荷硬顶:超限拆掉的是整条物理连接,那台机器上所有
	// 会话一起重连。
	require.Less(t, wirelimits.PortForwardChunkBytes, wirelimits.MaxPayloadBytes)
	require.Less(t, wirelimits.PortForwardChunkBytes, wirelimits.PortForwardWindowBytes,
		"一个窗口里放不下一块分片的话,生产者永远发不出第一块")
}

// Given 设备是端口白名单的唯一判定方,When 一次 open 指向未声明 / 已停用的端口,
// 或那个端口上根本没有服务在监听,Then 三种失败各自带一个可判别的领域码回到宿主。
//
// 「等机器上把服务起起来」与「这个端口压根没被声明出来」是用户要做的两件不同的事,
// 折进一个笼统失败就等于让用户去猜。
func TestPortForwardOpen_GivenTheDeviceRefusesOrCannotDial_WhenTheHostOpens_ThenEachFailureCarriesItsOwnCode(t *testing.T) {
	t.Parallel()

	client, device := pfConnPair(t)
	ctx := context.Background()

	protorpc.RegisterMethod(device.Registry(),
		uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_OPEN),
		func() *agentrewire.PortForwardOpenRequest { return &agentrewire.PortForwardOpenRequest{} },
		func(_ context.Context, request *agentrewire.PortForwardOpenRequest) (*agentrewire.PortForwardOpenResponse, error) {
			switch request.GetPort() {
			case 3000:
				return nil, &rpcerror.Error{
					Code:    rpcerror.CodePortForwardNotDeclared,
					Message: "这台设备上没有声明过 3000 端口",
				}
			case 4000:
				return nil, &rpcerror.Error{
					Code:    rpcerror.CodePortForwardDisabled,
					Message: "这条映射已停用",
				}
			default:
				return nil, &rpcerror.Error{
					Code:    rpcerror.CodePortForwardNoListener,
					Message: "设备上这个端口没有服务在监听",
				}
			}
		})

	for port, want := range map[uint32]int32{
		3000: rpcerror.CodePortForwardNotDeclared,
		4000: rpcerror.CodePortForwardDisabled,
		5000: rpcerror.CodePortForwardNoListener,
	} {
		_, err := wirecall.PortForwardOpen(ctx, wirecall.On(client),
			&agentrewire.PortForwardOpenRequest{StreamId: "s", Port: port})
		var rpcErr *protorpc.Error
		require.ErrorAs(t, err, &rpcErr)
		require.Equal(t, want, rpcErr.Code, "端口 %d 的失败码", port)
	}

	require.NotEqual(t, rpcerror.CodePortForwardNotDeclared, rpcerror.CodePortForwardNoListener)
	require.NotEqual(t, rpcerror.CodePortForwardDisabled, rpcerror.CodePortForwardNotDeclared)
}

// Given 硬不变量「设备侧只连 127.0.0.1 上已声明过的那个端口」,When 有人给 open 请求
// 加一格目标主机,Then 这条守卫判红。
//
// 协议里一旦有位置放主机,那条限制就不再是设备侧说了算的 —— 它变成「调用方最好别填」,
// 而那正是这条不变量要排除的形态。
func TestPortForwardOpenRequest_GivenTheLoopbackOnlyInvariant_ThenTheSchemaCarriesNoTargetHost(t *testing.T) {
	t.Parallel()

	fields := (&agentrewire.PortForwardOpenRequest{}).ProtoReflect().Descriptor().Fields()
	for i := range fields.Len() {
		name := string(fields.Get(i).Name())
		require.NotContains(t, name, "host",
			"open 请求不得携带目标主机:设备侧恒连环回地址")
		require.NotContains(t, name, "addr",
			"open 请求不得携带目标地址:设备侧恒连环回地址")
	}
}

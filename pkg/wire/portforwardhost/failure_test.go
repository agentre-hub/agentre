package portforwardhost_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
)

// 本文件钉的是**归因**:这一层遇到自己的失败时,交给宿主的是「是哪一件事」,而不是
// 一句写死的中文;而被转发应用自己的响应根本不经过这条路。
//
// 判据同样落在真的一条 protorpc 连接上(设备那一侧是剧本),因为「哪一种失败」这件事
// 正是由真实的 rpcerror 码与 closed 的 reason token 决定的 —— 打桩就把要判的东西
// 自己写进去了。

// recorder 收下钩子拿到的每一次失败,并写出宿主自己的正文。
type recorder struct {
	mu   sync.Mutex
	seen []portforwardhost.Failure
}

func (r *recorder) render(w http.ResponseWriter, _ *http.Request, f portforwardhost.Failure) {
	r.mu.Lock()
	r.seen = append(r.seen, f)
	r.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(f.Status)
	_, _ = io.WriteString(w, "<html>host owns this copy</html>")
}

func (r *recorder) calls() []portforwardhost.Failure {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]portforwardhost.Failure(nil), r.seen...)
}

// openForwardRendered 与 openForward 同形,只是把渲染钩子装上。
func openForwardRendered(t *testing.T, port int) (string, *scriptedDevice, *recorder) {
	t.Helper()
	hostConn, deviceConn := connPair(t)
	device := scriptDevice(t, deviceConn)
	rec := &recorder{}
	proxy := portforwardhost.NewProxy(hostConn, uint32(port), nil,
		portforwardhost.WithFailureRenderer(rec.render))
	t.Cleanup(proxy.Close)
	server := httptest.NewServer(proxy)
	t.Cleanup(server.Close)
	return server.URL, device, rec
}

// Given 宿主装了渲染钩子, When 这一层自己遇到六种失败中的任意一种, Then 钩子收到的
// 是该次失败的种类、这条映射的端口与默认状态码,正文完全由宿主写出。
//
// 六种的来路刻意各不相同:三种来自设备按 rpcerror 码回绝 open,三种来自 closed 通知
// 的 reason token —— 那正是今天这一层用来分辨它们的两个真实判据。
func TestFailure_GivenAHostRenderer_WhenTheProxyItselfFails_ThenItIsAttributed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		arrange func(d *scriptedDevice)
		kind    portforwardhost.FailureKind
		status  int
	}{
		{
			name: "端口没声明",
			arrange: func(d *scriptedDevice) {
				d.onOpen = func(_ *scriptedDevice, _ *agentrewire.PortForwardOpenRequest) error {
					return &protorpc.Error{Code: rpcerror.CodePortForwardNotDeclared, Message: "refused"}
				}
			},
			kind:   portforwardhost.FailureNotDeclared,
			status: http.StatusNotFound,
		},
		{
			name: "映射已停用",
			arrange: func(d *scriptedDevice) {
				d.onOpen = func(_ *scriptedDevice, _ *agentrewire.PortForwardOpenRequest) error {
					return &protorpc.Error{Code: rpcerror.CodePortForwardDisabled, Message: "refused"}
				}
			},
			kind:   portforwardhost.FailureDisabled,
			status: http.StatusForbidden,
		},
		{
			name: "端口上没有服务",
			arrange: func(d *scriptedDevice) {
				d.onOpen = func(_ *scriptedDevice, _ *agentrewire.PortForwardOpenRequest) error {
					return &protorpc.Error{Code: rpcerror.CodePortForwardNoListener, Message: "refused"}
				}
			},
			kind:   portforwardhost.FailureNoListener,
			status: http.StatusBadGateway,
		},
		{
			name: "设备够不着——open 被别的错误挡下",
			arrange: func(d *scriptedDevice) {
				d.onOpen = func(_ *scriptedDevice, _ *agentrewire.PortForwardOpenRequest) error {
					return &protorpc.Error{Code: rpcerror.CodeInternal, Message: "boom"}
				}
			},
			kind:   portforwardhost.FailureDeviceUnreachable,
			status: http.StatusBadGateway,
		},
		{
			name: "上游把请求断了",
			arrange: func(d *scriptedDevice) {
				d.onOpen = func(dev *scriptedDevice, req *agentrewire.PortForwardOpenRequest) error {
					go dev.finish(req.GetStreamId(), "upstream_error")
					return nil
				}
			},
			kind:   portforwardhost.FailureUpstreamGone,
			status: http.StatusBadGateway,
		},
		{
			name: "转发没完成",
			arrange: func(d *scriptedDevice) {
				d.onOpen = func(dev *scriptedDevice, req *agentrewire.PortForwardOpenRequest) error {
					go dev.finish(req.GetStreamId(), "something_else")
					return nil
				}
			},
			kind:   portforwardhost.FailureForwardIncomplete,
			status: http.StatusBadGateway,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			address, device, rec := openForwardRendered(t, 5173)
			tc.arrange(device)

			resp, err := http.Get(address + "/") //nolint:noctx // 用例里的一次同步请求
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			calls := rec.calls()
			require.Len(t, calls, 1, "自己的失败必须恰好交给宿主一次")
			assert.Equal(t, tc.kind, calls[0].Kind)
			assert.Equal(t, tc.status, calls[0].Status, "默认状态码要一并交出去")
			assert.EqualValues(t, 5173, calls[0].Port, "宿主要知道是哪个端口")

			assert.Equal(t, tc.status, resp.StatusCode)
			assert.Equal(t, "<html>host owns this copy</html>", string(body),
				"正文必须完全由宿主写出")
		})
	}
}

// Given 宿主装了渲染钩子, When 设备那一侧的服务**自己**答出一个 5xx, Then 钩子一次
// 都不被调用,状态码与正文逐字节原样透传。
//
// 这条是控制台那个缺陷的回归守卫:改写层按状态码猜"这是不是代理自己的失败",于是把
// 被转发应用自己的 502 换成了「端口上没有服务」那张页 —— 服务在跑,却叫用户去起它。
func TestFailure_GivenTheForwardedAppAnswersItself_ThenTheRendererIsNeverCalled(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusBadGateway, http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			address, device, rec := openForwardRendered(t, 5173)
			device.onOpen = func(d *scriptedDevice, req *agentrewire.PortForwardOpenRequest) error {
				go func() {
					d.head(req.GetStreamId(), int32(status), map[string][]string{"Content-Type": {"text/html"}}, false)
					d.data(req.GetStreamId(), []byte("<body id=upstream>the app said this</body>"))
					d.finish(req.GetStreamId(), "eof")
				}()
				return nil
			}

			resp, err := http.Get(address + "/") //nolint:noctx // 用例里的一次同步请求
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			assert.Empty(t, rec.calls(), "被转发应用自己的响应不是这一层的失败,钩子不该被惊动")
			assert.Equal(t, status, resp.StatusCode)
			assert.Equal(t, "<body id=upstream>the app said this</body>", string(body),
				"上游正文必须逐字节原样")
		})
	}
}

// Given 宿主**没有**装渲染钩子, When 这一层自己遇到失败, Then 答复与本轮之前逐字节
// 相同 —— 状态码、Content-Type、Cache-Control 与正文。
//
// 这条是桌面端零变化的守卫:桌面端本轮一行不改,它看到的东西必须一个字都不变。正文
// 写成字面量而不是引用包内常量,是因为「常量改了用例跟着改」等于没有守卫。
func TestFailure_GivenNoRenderer_ThenTheDefaultAnswersAreByteIdentical(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		arrange func(d *scriptedDevice)
		status  int
		body    string
	}{
		{
			name: "端口没声明",
			arrange: func(d *scriptedDevice) {
				d.onOpen = func(_ *scriptedDevice, _ *agentrewire.PortForwardOpenRequest) error {
					return &protorpc.Error{Code: rpcerror.CodePortForwardNotDeclared, Message: "refused"}
				}
			},
			status: http.StatusNotFound,
			body:   "这台设备上已经没有这个端口的映射了。回到 agentre 里重新打开它。\n",
		},
		{
			name: "映射已停用",
			arrange: func(d *scriptedDevice) {
				d.onOpen = func(_ *scriptedDevice, _ *agentrewire.PortForwardOpenRequest) error {
					return &protorpc.Error{Code: rpcerror.CodePortForwardDisabled, Message: "refused"}
				}
			},
			status: http.StatusForbidden,
			body:   "这条端口映射已经停用。到 agentre 里把它启用之后再打开。\n",
		},
		{
			name: "端口上没有服务",
			arrange: func(d *scriptedDevice) {
				d.onOpen = func(_ *scriptedDevice, _ *agentrewire.PortForwardOpenRequest) error {
					return &protorpc.Error{Code: rpcerror.CodePortForwardNoListener, Message: "refused"}
				}
			},
			status: http.StatusBadGateway,
			body:   "设备上这个端口没有服务在监听。到那台机器上把服务起起来,再刷新这一页。\n",
		},
		{
			name: "设备够不着",
			arrange: func(d *scriptedDevice) {
				d.onOpen = func(_ *scriptedDevice, _ *agentrewire.PortForwardOpenRequest) error {
					return &protorpc.Error{Code: rpcerror.CodeInternal, Message: "boom"}
				}
			},
			status: http.StatusBadGateway,
			body:   "这台设备此刻够不着。等它回来之后刷新这一页。\n",
		},
		{
			name: "上游把请求断了",
			arrange: func(d *scriptedDevice) {
				d.onOpen = func(dev *scriptedDevice, req *agentrewire.PortForwardOpenRequest) error {
					go dev.finish(req.GetStreamId(), "upstream_error")
					return nil
				}
			},
			status: http.StatusBadGateway,
			body:   "设备上这个端口的服务把这次请求断开了。刷新这一页重试。\n",
		},
		{
			name: "转发没完成",
			arrange: func(d *scriptedDevice) {
				d.onOpen = func(dev *scriptedDevice, req *agentrewire.PortForwardOpenRequest) error {
					go dev.finish(req.GetStreamId(), "something_else")
					return nil
				}
			},
			status: http.StatusBadGateway,
			body:   "这次转发没能完成。刷新这一页重试。\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			address, device := openForward(t, 5173)
			tc.arrange(device)

			resp, err := http.Get(address + "/") //nolint:noctx // 用例里的一次同步请求
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			assert.Equal(t, tc.status, resp.StatusCode)
			assert.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))
			assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
			assert.Equal(t, tc.body, string(body))
		})
	}
}

// Given 宿主装了钩子、而这次请求的 ResponseWriter 交不出底层连接, When 设备答一个
// 101 升级, Then 钩子拿到的是「升级失败」这一种,状态码 500。
//
// 这一种造不出真实的失败(生产上交给代理的 writer 恒可 Hijack),所以直接把一个
// httptest.ResponseRecorder 递进去 —— 它不实现 http.Hijacker,正是那条分支的前提。
func TestFailure_GivenTheWriterCannotBeHijacked_ThenTheUpgradeFailureIsAttributed(t *testing.T) {
	t.Parallel()
	hostConn, deviceConn := connPair(t)
	device := scriptDevice(t, deviceConn)
	rec := &recorder{}
	proxy := portforwardhost.NewProxy(hostConn, 5173, nil,
		portforwardhost.WithFailureRenderer(rec.render))
	t.Cleanup(proxy.Close)

	device.onOpen = func(d *scriptedDevice, req *agentrewire.PortForwardOpenRequest) error {
		go d.head(req.GetStreamId(), http.StatusSwitchingProtocols, nil, true)
		return nil
	}

	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ws", nil))

	calls := rec.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, portforwardhost.FailureUpgradeUnavailable, calls[0].Kind)
	assert.Equal(t, http.StatusInternalServerError, calls[0].Status)
	assert.EqualValues(t, 5173, calls[0].Port)
	assert.True(t, strings.Contains(w.Body.String(), "host owns this copy"))
}

// Given 宿主**没有**装渲染钩子、而这次请求的 ResponseWriter 交不出底层连接, When 设备
// 答一个 101 升级, Then 答复与本轮之前逐字节相同 —— 500、text/plain、no-store 与那句话。
//
// 第七种失败走不到上面那张表里:它要的前提是一个不可 Hijack 的 writer,而 httptest 起的
// 真服务恒可 Hijack。缺了它,「桌面端零变化」这条守卫就有七分之一没人看着。
func TestFailure_GivenNoRendererAndAWriterThatCannotBeHijacked_ThenTheDefaultAnswerIsByteIdentical(t *testing.T) {
	t.Parallel()
	hostConn, deviceConn := connPair(t)
	device := scriptDevice(t, deviceConn)
	proxy := portforwardhost.NewProxy(hostConn, 5173, nil)
	t.Cleanup(proxy.Close)

	device.onOpen = func(d *scriptedDevice, req *agentrewire.PortForwardOpenRequest) error {
		go d.head(req.GetStreamId(), http.StatusSwitchingProtocols, nil, true)
		return nil
	}

	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ws", nil))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	assert.Equal(t, "这条转发没能把连接交出去,升级到 WebSocket 失败了。\n", w.Body.String())
}

// Given 浏览器在 open 还没有应答的时候就走了, When 这次 open 因此以 ctx 取消收场,
// Then 钩子一次都不被调用 —— 这不是这一层的失败,更不是「设备够不着」。
//
// waitHead 已经把「浏览器先走了」判成不是失败(它交出零值),open 那一段却没有:它把
// context.Canceled 一路喂给 openFailureKind,而那里判不出来的一律算「够不着」。于是同
// 一件事(用户关掉标签页)在两条路上得到两种归因,其中一种还会让宿主认定一台好端端
// 的设备离线了 —— 而设备此刻正在正常处理这次 open。
func TestFailure_GivenTheBrowserLeavesWhileTheOpenIsStillInFlight_ThenNothingIsAttributed(t *testing.T) {
	t.Parallel()
	hostConn, deviceConn := connPair(t)
	device := scriptDevice(t, deviceConn)
	rec := &recorder{}
	proxy := portforwardhost.NewProxy(hostConn, 5173, nil,
		portforwardhost.WithFailureRenderer(rec.render))
	t.Cleanup(proxy.Close)

	opened := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	device.onOpen = func(_ *scriptedDevice, _ *agentrewire.PortForwardOpenRequest) error {
		close(opened)
		<-release // 卡住这次 open,让浏览器在它还没有应答的时候走掉
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	served := make(chan struct{})
	go func() {
		defer close(served)
		proxy.ServeHTTP(httptest.NewRecorder(), request)
	}()

	<-opened
	cancel()
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("浏览器已经走了,ServeHTTP 还没收场")
	}

	assert.Empty(t, rec.calls(), "浏览器自己走了不是这一层的失败,更不是「设备够不着」")
}

// Given 一次请求正卡在等响应头, When 宿主把这条转发收掉(Proxy.Close —— 桌面端关掉
// 那条专属监听、控制台收回租约走的都是它), Then 钩子拿到的是「设备够不着」。
//
// 这条 closed 的 reason 是 host_gone,与 connection_closed 共用 closedFailureKind 里
// 同一条分支,而它此前没有任何用例走到:漏掉那条分支,这次收场就会落到兜底的「转发
// 没完成」上 —— 用户被告知的是「重试一下」,而事实是这条转发已经被收走了,重试也不会
// 再有第二个结果。
func TestFailure_GivenTheHostClosesTheForwardWhileARequestWaits_ThenItIsDeviceUnreachable(t *testing.T) {
	t.Parallel()
	hostConn, deviceConn := connPair(t)
	device := scriptDevice(t, deviceConn)
	rec := &recorder{}
	proxy := portforwardhost.NewProxy(hostConn, 5173, nil,
		portforwardhost.WithFailureRenderer(rec.render))
	t.Cleanup(proxy.Close)

	opened := make(chan struct{})
	device.onOpen = func(_ *scriptedDevice, _ *agentrewire.PortForwardOpenRequest) error {
		close(opened)
		return nil // 这条流开着,但响应头永远不来
	}

	served := make(chan struct{})
	go func() {
		defer close(served)
		proxy.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}()

	<-opened
	proxy.Close()
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("这条转发已经被收掉了,卡在等响应头的那次请求没有被叫醒")
	}

	calls := rec.calls()
	require.Len(t, calls, 1, "自己的失败必须恰好交给宿主一次")
	assert.Equal(t, portforwardhost.FailureDeviceUnreachable, calls[0].Kind)
	assert.Equal(t, http.StatusBadGateway, calls[0].Status)
}

// Given 这条转发已经被宿主收掉, When 收掉的那一瞬间还在路上的请求打进来, Then 钩子
// 拿到的是「设备够不着」——而不是一个空的 200。
//
// 这是 FailureDeviceUnreachable 的第三条入口:register 在 shut 之后交出 nil,连 open
// 都不会发。少了它,这条路上一旦有人把归因漏掉,浏览器拿到的是一张空白页,而空白页
// 是没法告诉用户「这条转发已经不在了」的。
func TestFailure_GivenTheForwardIsAlreadyClosed_WhenARequestStillArrives_ThenItIsDeviceUnreachable(t *testing.T) {
	t.Parallel()
	hostConn, deviceConn := connPair(t)
	device := scriptDevice(t, deviceConn)
	rec := &recorder{}
	proxy := portforwardhost.NewProxy(hostConn, 5173, nil,
		portforwardhost.WithFailureRenderer(rec.render))
	proxy.Close()

	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	calls := rec.calls()
	require.Len(t, calls, 1, "自己的失败必须恰好交给宿主一次")
	assert.Equal(t, portforwardhost.FailureDeviceUnreachable, calls[0].Kind)
	assert.EqualValues(t, 5173, calls[0].Port)
	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "host owns this copy", "答复必须由宿主写出,不能是一张空白页")

	opens, _, _, _ := device.snapshot()
	assert.Empty(t, opens, "转发都收掉了,不该再往设备上开流")
}

// Given 被转发的服务已经答了 200 并送出了一段正文, When 它随后把这次请求断开
// (closed 的 reason 是 upstream_error,也就是本会归因成 FailureUpstreamGone 的那个
// token), Then 钩子一次都不被调用,已经出门的状态码与正文一个字节都不变。
//
// 响应头一出门,这一层就再没有归因的余地了:此刻再把一句失败文案写进去,用户拿到的
// 是一个状态码 200、正文里却掺进一段错误页的下载。上面那条「应用自己答 5xx」的守卫
// 收在 eof 上,盯不住这一条 —— 而 upstream_error 恰恰是唯一一个「头已经出门了、这一层
// 手上却还握着一种失败」的组合。
func TestFailure_GivenTheHeadIsAlreadyOut_WhenTheUpstreamBreaksMidBody_ThenNothingIsRewritten(t *testing.T) {
	t.Parallel()
	address, device, rec := openForwardRendered(t, 5173)
	device.onOpen = func(d *scriptedDevice, req *agentrewire.PortForwardOpenRequest) error {
		go func() {
			d.head(req.GetStreamId(), http.StatusOK, map[string][]string{"Content-Type": {"text/plain"}}, false)
			d.data(req.GetStreamId(), []byte("half-a-file"))
			d.finish(req.GetStreamId(), "upstream_error")
		}()
		return nil
	}

	resp, err := http.Get(address + "/download.bin") //nolint:noctx // 用例里的一次同步请求
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Empty(t, rec.calls(), "响应头已经出门了,这一层不该再往里写归因")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "half-a-file", string(body), "已经写给浏览器的正文必须逐字节原样")
}

package portforward_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	daemonpf "github.com/agentre-hub/agentre/internal/daemon/portforward"
	"github.com/agentre-hub/agentre/internal/model/entity/port_forward_entity"
	"github.com/agentre-hub/agentre/internal/pkg/portforward"
	"github.com/agentre-hub/agentre/internal/repository/port_forward_repo/mock_port_forward_repo"
)

// 本文件是这条转发路上唯一一个**两端都是真的**的用例:宿主那一侧是真的专属监听 +
// 真的 portforwardhost.Proxy,设备那一侧是真的 daemon/portforward.Streams 拨到一个真的
// httptest 服务上,中间是真的一条 protorpc 连接。
//
// 别处的用例各自打桩了对面:pkg/wire/portforwardhost 的 proxy_test.go 里设备是剧本,
// stream_test.go 里宿主是一个记录器。而「带 Expect: 100-continue 的上传」这件事的缺陷恰好横跨两端 —— 客户端看到
// 的两条 100 里有一条来自宿主自己的 net/http(正确的那条),另一条来自设备把中间应答
// 当成了最终应答。任何一端打了桩,这个次序就都测不出来。

// forwardToRealDevice 起一条端到端的转发:返回宿主那条专属监听的地址。
func forwardToRealDevice(t *testing.T, target *httptest.Server) string {
	t.Helper()
	return forwardToRealDeviceWithLatency(t, 0, target)
}

// forwardToRealDeviceWithLatency 同上,但宿主与设备之间那条连接带一段每帧时延 ——
// 横跨「流收尾」的竞态要靠它才钉得住,见 newDevicesWithLatency。
func forwardToRealDeviceWithLatency(
	t *testing.T, latency time.Duration, target *httptest.Server,
) string {
	t.Helper()
	port := targetPort(t, target)

	devices, far := newDevicesWithLatency(t, latency, 7)
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mock_port_forward_repo.NewMockPortForwardRepo(ctrl)
	repo.EXPECT().FindByPort(gomock.Any(), port).DoAndReturn(
		func(_ context.Context, p int) (*port_forward_entity.PortForward, error) {
			return &port_forward_entity.PortForward{ID: 1, Port: p, Enabled: true}, nil
		}).AnyTimes()

	gate := daemonpf.NewHandlers(daemonpf.Options{Repo: repo, Dial: daemonpf.DialLoopback})
	streams := daemonpf.NewStreams(daemonpf.StreamOptions{Gate: gate, Notify: far[7].Notify})
	t.Cleanup(streams.CloseAll)
	daemonpf.RegisterStreamMethods(far[7].Registry(), streams, nil)

	listeners := portforward.NewListeners(devices)
	t.Cleanup(listeners.CloseAll)
	address, err := listeners.Open(context.Background(),
		portforward.Target{DeviceID: 7, MappingID: "m1", Port: port})
	require.NoError(t, err)
	return address
}

func targetPort(t *testing.T, server *httptest.Server) int {
	t.Helper()
	_, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	require.NoError(t, err)
	var port int
	_, err = fmt.Sscanf(portText, "%d", &port)
	require.NoError(t, err)
	return port
}

// Given 一个带 `Expect: 100-continue` 的大上传(curl 对超过 1KB 的请求体自动加这一格),
// When 它经这条专属监听转发到设备上那个真的服务, Then 客户端收到**恰好一条**
// `100 Continue`,随后是上游真正的 `201` 与它的响应头、正文。
//
// 直连同一个服务就是「100 一次 + 201」,经转发必须一模一样。两条 100 之后跟一个空的
// 200,意味着上游真正的应答整个丢了 —— 而这正是浏览器与 curl 上传大文件走的那条路。
func TestForward_GivenAnUploadThatExpects100Continue_ThenTheClientGetsTheRealFinalResponse(t *testing.T) {
	t.Parallel()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("X-Echo-Bytes", fmt.Sprint(n))
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "stored")
	}))
	t.Cleanup(target.Close)

	address := forwardToRealDevice(t, target)
	parsed, err := url.Parse(address)
	require.NoError(t, err)

	conn, err := net.DialTimeout("tcp", parsed.Host, 5*time.Second)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	require.NoError(t, conn.SetDeadline(time.Now().Add(20*time.Second)))

	body := make([]byte, 256<<10)
	for i := range body {
		body[i] = byte(i)
	}
	_, err = fmt.Fprintf(conn, "POST /upload HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\n"+
		"Expect: 100-continue\r\nConnection: close\r\n\r\n", parsed.Host, len(body))
	require.NoError(t, err)

	reader := bufio.NewReader(conn)
	request := &http.Request{Method: http.MethodPost}

	// 第一条应答必须是那个 100 —— 它是宿主自己的 net/http 在读请求体时发出的,
	// 也是 curl 等着才肯把 body 送出去的那一条。
	interim, err := http.ReadResponse(reader, request)
	require.NoError(t, err)
	defer func() { _ = interim.Body.Close() }()
	require.Equal(t, http.StatusContinue, interim.StatusCode,
		"带 Expect 的请求,第一条应答该是 100 Continue")

	_, err = conn.Write(body)
	require.NoError(t, err)

	// 此后不许再来第二条 100:那一条是设备把上游的中间应答当成了最终应答。
	var interims int
	var final *http.Response
	for {
		response, rerr := http.ReadResponse(reader, request)
		require.NoError(t, rerr)
		if response.StatusCode == http.StatusContinue {
			interims++
			_ = response.Body.Close()
			continue
		}
		final = response
		break
	}
	defer func() { _ = final.Body.Close() }()

	assert.Zero(t, interims, "客户端收到了两条 100 Continue:设备把上游的中间应答当成了最终应答")
	assert.Equal(t, http.StatusCreated, final.StatusCode, "客户端拿到的必须是上游真正的最终状态码")
	assert.Equal(t, fmt.Sprint(len(body)), final.Header.Get("X-Echo-Bytes"),
		"上游数出来的字节数必须一字不差地到达客户端")
	got, err := io.ReadAll(final.Body)
	require.NoError(t, err)
	assert.Equal(t, "stored", string(got))
}

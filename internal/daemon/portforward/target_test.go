package portforward

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/port_forward_entity"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// 本文件测的是**按声明里存着的目标拨号**这一半(规格「映射与目标」的打开一条流、上游
// 改写、失败归因三段):
//
//   - 拨号走生产那一条 DialTarget:主机名在设备上解析,https 在设备上做 TLS,证书校验
//     按那条映射自己的设置。失败的三种原因(连接被拒 / 名字解析失败 / TLS 校验失败)
//     各自落成一个码,打在真 socket、真解析器、真 TLS 握手上。
//   - 设备只改写三处:请求的 Host、响应里指向目标自己 origin 的 Location、每条
//     Set-Cookie 的 Domain。改写的依据是**声明里的目标**,所以改写用例里的目标主机
//     是一个解析不出来的名字,拨号器把它接到本机的测试服务上 —— 改写只能从声明里读到
//     这个名字,别处没有。

// ---------- 脚手架 ----------

// oneMapping 建一份只有一条声明(id 1)的流表。
func oneMapping(t *testing.T, notify Notifier, dial Dialer, row port_forward_entity.PortForward) *Streams {
	t.Helper()
	row.ID, row.Enabled = 1, true
	gate := NewHandlers(Options{Repo: mappingsRepo(t, &row), Dial: dial})
	streams := NewStreams(StreamOptions{Gate: gate, Notify: notify})
	t.Cleanup(streams.CloseAll)
	return streams
}

// redirectTo 是一个把**任何**目标都接到 addr 上的拨号器。改写用例靠它把一个解析
// 不出来的主机名接到本机的测试服务上。
func redirectTo(addr string) Dialer {
	return func(ctx context.Context, _ Target) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "tcp", addr)
	}
}

func headOf(t *testing.T, rec *recorder) *agentrewire.PortForwardResponseNotification {
	t.Helper()
	rec.waitClosed(t)
	for _, event := range rec.snapshot() {
		if event.kind == "head" {
			return event.head
		}
	}
	require.FailNow(t, "这条流没有带回响应头")
	return nil
}

func openMapping(t *testing.T, streams *Streams, path string) (*agentrewire.PortForwardOpenResponse, error) {
	t.Helper()
	return streams.Open(context.Background(), &agentrewire.PortForwardOpenRequest{
		StreamId: "s1", MappingId: 1, Method: http.MethodGet, Path: path,
	})
}

// ---------- 按目标拨号与 TLS ----------

// Given 一条 https 映射,目标是一台自签证书的服务,When 按默认设置(校验证书)打开它,
// Then open 以 TLS 校验失败收场;When 这条映射勾了「忽略证书错误」,Then 同一个目标
// 打得开,响应原样带回。
//
// 两半必须同在:只证前一半,一个「https 一律拒绝」的实现也过得去;只证后一半,一个
// 「从不校验」的实现也过得去。
func TestDialTarget_GivenASelfSignedHTTPSTarget_ThenItIsVerifiedByDefaultAndSkippedOnlyWhenTheMappingSaysSo(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "over tls")
	}))
	t.Cleanup(server.Close)
	port := serverPort(t, server)
	target := fmt.Sprintf("https://127.0.0.1:%d", port)

	t.Run("默认校验证书", func(t *testing.T) {
		rec := newRecorder()
		streams := oneMapping(t, rec.notify, DialTarget, port_forward_entity.PortForward{Port: port, Target: target})

		opened, err := openMapping(t, streams, "/")

		assert.Nil(t, opened)
		assert.Equal(t, int32(rpcerror.CodePortForwardTLSVerification), code(t, err),
			"自签证书在默认设置下必须被拒,而且说得出是证书的事")
	})

	t.Run("这条映射勾了忽略证书错误", func(t *testing.T) {
		rec := newRecorder()
		streams := oneMapping(t, rec.notify, DialTarget,
			port_forward_entity.PortForward{Port: port, Target: target, Insecure: true})

		_, err := openMapping(t, streams, "/")
		require.NoError(t, err)

		head := headOf(t, rec)
		assert.EqualValues(t, http.StatusOK, head.GetStatus())
		assert.Equal(t, "over tls", string(rec.dataBytes()))
	})
}

// Given 三条各自连不上的映射,When 分别打开它们,Then 生产拨号器给出的三种失败各自
// 落成一个码:目标端口上没人监听 → 连接被拒;主机名解析不出来 → 名字解析失败;证书
// 不受信任 → TLS 校验失败(上一条用例)。
func TestDialTarget_GivenAnUnreachableTarget_WhenOpening_ThenTheCauseIsAttributed(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closedPort := listenerPort(t, listener.Addr().String())
	require.NoError(t, listener.Close())

	for name, one := range map[string]struct {
		target string
		want   int32
	}{
		"连接被拒": {target: fmt.Sprintf("http://127.0.0.1:%d", closedPort), want: rpcerror.CodePortForwardNoListener},
		// .invalid 是 RFC 2606 保留的顶级域,任何解析器都答不出它。
		"名字解析失败": {target: "http://agentre-port-forward.invalid:80", want: rpcerror.CodePortForwardNameResolution},
	} {
		t.Run(name, func(t *testing.T) {
			rec := newRecorder()
			streams := oneMapping(t, rec.notify, DialTarget, port_forward_entity.PortForward{Target: one.target})

			opened, err := openMapping(t, streams, "/")

			assert.Nil(t, opened)
			assert.Equal(t, one.want, code(t, err))
			assert.Nil(t, rec.closedEvent(), "open 失败的流从没存在过,不该有收尾通知")
		})
	}
}

// ---------- 上游改写:Host ----------

// Given 一条映射的目标是某个主机名,When 按它打开一条流,Then 上游收到的 Host 是目标的
// host[:port],端口等于协议默认值时省略 —— 虚拟主机按 Host 认站,错一格就是另一个站。
func TestStreamOpen_GivenATargetHost_WhenForwarding_ThenTheHostHeaderNamesTheTarget(t *testing.T) {
	for name, one := range map[string]struct {
		target string
		want   string
	}{
		"非默认端口带上端口":      {target: "http://app.internal:8080", want: "app.internal:8080"},
		"http 的 80 省略":   {target: "http://app.internal:80", want: "app.internal"},
		"https 的 443 省略": {target: "https://app.internal:443", want: "app.internal"},
		"https 的 80 不省略": {target: "https://app.internal:80", want: "app.internal:80"},
	} {
		t.Run(name, func(t *testing.T) {
			seen := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				seen <- r.Host
			}))
			t.Cleanup(server.Close)

			rec := newRecorder()
			streams := oneMapping(t, rec.notify, redirectTo(server.Listener.Addr().String()),
				port_forward_entity.PortForward{Target: one.target})
			_, err := streams.Open(context.Background(), &agentrewire.PortForwardOpenRequest{
				StreamId: "s1", MappingId: 1, Method: http.MethodGet, Path: "/",
				Headers: map[string]*agentrewire.HeaderValues{"Host": {Values: []string{"abc.fw.agentrehub.com"}}},
			})
			require.NoError(t, err)

			assert.Equal(t, one.want, <-seen, "Host 必须是目标自己的 host[:port],不是浏览器那一侧的地址")
			rec.waitClosed(t)
		})
	}
}

// ---------- 上游改写:Location ----------

// Given 上游回一个跳转,When 它经转发流回到宿主,Then 指向目标自己 origin 的 Location
// 变成只剩路径与查询串的相对地址(浏览器够不着目标的 origin),指向别处的原样不动。
func TestStreamOpen_GivenARedirect_WhenItPointsAtTheTargetsOwnOrigin_ThenItBecomesRelative(t *testing.T) {
	for name, one := range map[string]struct {
		target   string
		location string
		want     string
	}{
		"同源绝对地址":      {"http://app.internal:8080", "http://app.internal:8080/after?x=1", "/after?x=1"},
		"同源、主机名大小写不同": {"http://app.internal:8080", "http://APP.internal:8080/a", "/a"},
		"同源、省略默认端口":   {"https://app.internal:443", "https://app.internal/login?next=%2F", "/login?next=%2F"},
		"同源、只到根":      {"http://app.internal:8080", "http://app.internal:8080", "/"},
		"同源、协议相对":     {"http://app.internal:8080", "//app.internal:8080/p", "/p"},
		"别的主机不动":      {"http://app.internal:8080", "https://sso.example.com/login", "https://sso.example.com/login"},
		"同主机别的端口不动":   {"http://app.internal:8080", "http://app.internal:9090/x", "http://app.internal:9090/x"},
		"同主机别的协议不动":   {"http://app.internal:8080", "https://app.internal:8080/x", "https://app.internal:8080/x"},
		// 去掉 origin 之后剩下的 //evil.example/x 在浏览器眼里是协议相对地址,会把
		// 用户带到别的站去 —— 这一种宁可不改。
		"同源但路径以双斜杠开头不动": {"http://app.internal:8080", "http://app.internal:8080//evil.example/x", "http://app.internal:8080//evil.example/x"},
		"本来就是相对地址不动":    {"http://app.internal:8080", "/already/relative", "/already/relative"},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", one.location)
				w.WriteHeader(http.StatusFound)
			}))
			t.Cleanup(server.Close)

			rec := newRecorder()
			streams := oneMapping(t, rec.notify, redirectTo(server.Listener.Addr().String()),
				port_forward_entity.PortForward{Target: one.target})
			_, err := openMapping(t, streams, "/")
			require.NoError(t, err)

			head := headOf(t, rec)
			assert.EqualValues(t, http.StatusFound, head.GetStatus())
			assert.Equal(t, []string{one.want}, head.GetHeaders()["Location"].GetValues())
		})
	}
}

// ---------- 上游改写:Set-Cookie ----------

// Given 上游种了带 Domain 的 cookie,When 响应经转发流回到宿主,Then 每一条 Set-Cookie
// 都去掉了 Domain 属性、其余属性原样保留 —— 目标的域名与浏览器看到的转发域名不是
// 一个,带着它浏览器会拒收这条 cookie。
func TestStreamOpen_GivenCookiesWithADomain_WhenForwarded_ThenOnlyTheDomainAttributeIsDropped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Set-Cookie", "sid=abc; Domain=app.internal; Path=/; HttpOnly; Secure; SameSite=Lax")
		w.Header().Add("Set-Cookie", "theme=dark; Max-Age=60; domain=.internal")
		w.Header().Add("Set-Cookie", "plain=1; Path=/x")
	}))
	t.Cleanup(server.Close)

	rec := newRecorder()
	streams := oneMapping(t, rec.notify, redirectTo(server.Listener.Addr().String()),
		port_forward_entity.PortForward{Target: "http://app.internal:8080"})
	_, err := openMapping(t, streams, "/")
	require.NoError(t, err)

	head := headOf(t, rec)
	assert.Equal(t, []string{
		"sid=abc; Path=/; HttpOnly; Secure; SameSite=Lax",
		"theme=dark; Max-Age=60",
		"plain=1; Path=/x",
	}, head.GetHeaders()["Set-Cookie"].GetValues())
}

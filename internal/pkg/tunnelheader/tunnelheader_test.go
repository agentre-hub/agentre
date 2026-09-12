package tunnelheader_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/tunnelheader"
)

// Given 一份带着逐跳头与业务头的请求头,When 缓冲式隧道(MCP)过滤它,Then 逐跳头
// (含 Host / Content-Length / Upgrade)全被剥掉,其余原样留下。
//
// 这一条钉的是搬家前后**行为逐字相同**:清单换了个包住,MCP 那条路不该跟着变。
func TestSanitize_GivenABufferedTunnel_WhenFiltering_ThenEveryHopByHopHeaderIsStripped(t *testing.T) {
	headers := http.Header{
		"Host":              {"example.test"},
		"Content-Length":    {"12"},
		"Connection":        {"keep-alive"},
		"Keep-Alive":        {"timeout=5"},
		"Transfer-Encoding": {"chunked"},
		"Te":                {"trailers"},
		"Trailer":           {"Expires"},
		"Upgrade":           {"websocket"},
		"Proxy-Connection":  {"keep-alive"},
		"Authorization":     {"Bearer token"},
		"Mcp-Session-Id":    {"s-1"},
	}

	out := tunnelheader.Sanitize(headers)

	require.Len(t, out, 2)
	assert.Equal(t, []string{"Bearer token"}, out["Authorization"])
	assert.Equal(t, []string{"s-1"}, out["Mcp-Session-Id"])
}

// Given 一次协议升级,When 转发流过滤它的头,Then Connection / Upgrade / Sec-WebSocket-*
// 都留了下来。
//
// 这是本包存在的理由:默认清单把 Upgrade 当逐跳头剥掉,照搬会让 101 永远上不去 ——
// WebSocket 与 HMR 一起没了,而失败的样子是「升级请求变成了一个普通 GET」,查起来
// 完全不指向头过滤。
func TestSanitizeWith_GivenAProtocolUpgrade_WhenFiltering_ThenTheHandshakeHeadersSurvive(t *testing.T) {
	headers := http.Header{
		"Host":                  {"example.test"},
		"Connection":            {"Upgrade"},
		"Upgrade":               {"websocket"},
		"Sec-Websocket-Key":     {"dGhlIHNhbXBsZSBub25jZQ=="},
		"Sec-Websocket-Version": {"13"},
		"Keep-Alive":            {"timeout=5"},
	}

	out := tunnelheader.SanitizeWith(headers, tunnelheader.Options{AllowUpgrade: true})

	assert.Equal(t, []string{"Upgrade"}, out["Connection"])
	assert.Equal(t, []string{"websocket"}, out["Upgrade"])
	assert.Equal(t, []string{"dGhlIHNhbXBsZSBub25jZQ=="}, out["Sec-Websocket-Key"])
	assert.Equal(t, []string{"13"}, out["Sec-Websocket-Version"])
	assert.NotContains(t, out, "Host", "升级放行的只有握手那两格,别的逐跳头照剥")
	assert.NotContains(t, out, "Keep-Alive")
}

// Given 一条流式转发的应答,When 过滤它的头,Then Content-Length 留了下来 —— 设备逐字
// 搬上游的 body,这个长度是准的,浏览器据它显示下载进度。
func TestSanitizeWith_GivenAStreamedResponse_WhenFiltering_ThenContentLengthSurvives(t *testing.T) {
	headers := http.Header{
		"Content-Length":    {"1048576"},
		"Content-Type":      {"application/octet-stream"},
		"Transfer-Encoding": {"chunked"},
	}

	out := tunnelheader.SanitizeWith(headers, tunnelheader.Options{AllowLength: true})

	assert.Equal(t, []string{"1048576"}, out["Content-Length"])
	assert.Equal(t, []string{"application/octet-stream"}, out["Content-Type"])
	assert.NotContains(t, out, "Transfer-Encoding", "封装是逐跳的事,不该跨隧道带过去")
}

// Given 没有任何头,When 过滤它,Then 回 nil 而不是空 map:线上那一格是可选的,缺席
// 就该缺席。
func TestSanitize_GivenNoHeaders_WhenFiltering_ThenNothingIsReported(t *testing.T) {
	assert.Nil(t, tunnelheader.Sanitize(nil))
	assert.Nil(t, tunnelheader.Sanitize(http.Header{}))
}

// Given 大小写各异的头名,When 判它是不是逐跳头,Then 判据不受大小写影响 —— 线上来的
// 头名是对端写的,不是本机规范化过的。
func TestBlocked_GivenMixedCaseNames_WhenJudging_ThenTheVerdictIsCaseInsensitive(t *testing.T) {
	assert.True(t, tunnelheader.Blocked("cOnNeCtIoN", tunnelheader.Options{}))
	assert.False(t, tunnelheader.Blocked("cOnNeCtIoN", tunnelheader.Options{AllowUpgrade: true}))
	assert.False(t, tunnelheader.Blocked("authorization", tunnelheader.Options{}))
}

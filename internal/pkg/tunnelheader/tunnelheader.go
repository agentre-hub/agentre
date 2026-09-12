// Package tunnelheader 是「哪些头不该跨隧道转发」这一条判据的唯一出处。
//
// 它刻意是一个**叶子**:只 import net/http,于是内置工具 MCP 的反向隧道(缓冲式,
// 由对端用 http.Client 重放)与端口转发的转发流(流式,由设备直接把请求行写进本机
// socket)可以取同一份逐跳头清单,而不必有谁反向依赖谁。
//
// **升级那三格必须能单独放行。** 默认清单里 Upgrade 是逐跳头,剥掉它是对的 ——
// MCP 那条路重放请求时不该把上一跳的升级意图带过去。但端口转发要让 WebSocket 与
// HMR 通,而 101 的前提正是 Connection / Upgrade 原样送到被转发的那个服务:照搬
// 默认清单,升级就永远上不去。所以判据带一个显式的开关,而不是两份各自漂移的清单。
package tunnelheader

import "net/http"

// hopByHopTunnelHeaders 是不该跨隧道转发的逐跳头(+ Host/Content-Length,重放方按
// 目标 URL / body 重算)。其余头(Authorization / Content-Type / Accept / Mcp-* 等)
// 原样转发。
var hopByHopTunnelHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Connection": true, "Keep-Alive": true,
	"Transfer-Encoding": true, "Te": true, "Trailer": true, "Upgrade": true, "Proxy-Connection": true,
}

// upgradeTunnelHeaders 是握手要用到、而默认清单又恰好剥掉的那两格。
// Sec-WebSocket-* 不在逐跳头清单里,本来就原样过。
var upgradeTunnelHeaders = map[string]bool{"Connection": true, "Upgrade": true}

// Options 说明这一次过滤发生在哪条路上。零值就是 MCP 那条缓冲式隧道的判据。
type Options struct {
	// AllowUpgrade 放行 Connection / Upgrade。只在**这一跳确实在做协议升级**时
	// 打开:请求侧是调用方带了 Connection: Upgrade,应答侧是上游回了 101。
	AllowUpgrade bool
	// AllowLength 放行 Content-Length。流式转发的应答方向要它 —— 那个长度是准的
	// (设备逐字搬上游的 body),浏览器据它显示下载进度。缓冲式重放的一侧不要它:
	// 重放方按自己组装的 body 重算。
	AllowLength bool
}

// Blocked 判一个头名在这条路上该不该被剥掉。名字大小写不敏感。
func Blocked(name string, options Options) bool {
	canonical := http.CanonicalHeaderKey(name)
	if !hopByHopTunnelHeaders[canonical] {
		return false
	}
	if options.AllowUpgrade && upgradeTunnelHeaders[canonical] {
		return false
	}
	if options.AllowLength && canonical == "Content-Length" {
		return false
	}
	return true
}

// Sanitize 剥掉全部逐跳头,是缓冲式隧道(MCP)那一条路的判据。
func Sanitize(h http.Header) map[string][]string {
	return SanitizeWith(h, Options{})
}

// SanitizeWith 按给定判据过滤。返回的是一份拷贝:调用方此后改它不会回头影响原请求。
//
// 没有头就回 nil(而不是一个空 map):线上那一格是可选的,缺席就该缺席。这一条与
// 搬过来之前逐字相同 —— MCP 那条路的行为不因为换了个包而变。
func SanitizeWith(h http.Header, options Options) map[string][]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string][]string, len(h))
	for name, values := range h {
		if Blocked(name, options) {
			continue
		}
		copied := make([]string, len(values))
		copy(copied, values)
		out[name] = copied
	}
	return out
}

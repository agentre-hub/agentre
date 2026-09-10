// Package relayenvelope 是中继通道信封的唯一实现。
//
// 格式:2 字节大端通道 ID 长度 + 通道 ID(UTF-8)+ 载荷。中继本身只路由不透明字节,
// 信封是它认得的**唯一**结构 —— 它据此知道一条物理连接上的这一帧属于哪条虚拟通道。
//
// 从前三个宿主各写一份解析(daemon 的 relaytransport、服务端的 relay_svc、浏览器的
// relayEnvelope.ts),三套校验互不相同:浏览器那份只查截断,长度 0 照收(通道 ID 成
// 空串、整段载荷当帧交出去),非法 UTF-8 被 TextDecoder 静默换成 U+FFFD,长度上限也
// 比 daemon 宽 512 倍。中继上的每一帧都是**别的设备**发来的字节,而三份解析里最松的
// 那一份决定了实际的下限。
//
// TypeScript 那一侧的对应物是 @agentre-hub/agentre-wire 的 relay-envelope.ts,同一
// 格式、同一套校验;两边由各自的用例钉住。
package relayenvelope

import (
	"encoding/binary"
	"errors"
	"unicode/utf8"
)

// HeaderBytes 是长度前缀占的字节数。
const HeaderBytes = 2

// MaxChannelIDBytes 是通道 ID 的字节上限。
//
// 取值远宽于实际:服务端发的是 16 字节的 base64url(22 字符),daemon 发的是 16 字节
// 的 hex(32 字符),浏览器一个都不自己造 —— 它只回送服务端发来的那个。
const MaxChannelIDBytes = 128

// MaxEnvelopeBytes 是信封头最多占多少,链路读上限里给它留的就是这一份余量。
//
// 它**不是**载荷预算的一部分:中继上收到的每一帧都是「套过信封的载荷」,所以
// 读上限 = protorpc.MaxFrameBytes + 这个数。少给这一份余量,一份刚好顶格的合法载荷
// 会只因为带了信封就被 1009 打掉,而打掉的是**整条物理连接** —— 上面所有虚拟通道
// 一起陪葬。
const MaxEnvelopeBytes int64 = HeaderBytes + MaxChannelIDBytes

// Wrap 给一帧套上信封。
//
// 空载荷是合法的:它是「这条通道关了」的信号。
func Wrap(channelID string, payload []byte) ([]byte, error) {
	if channelID == "" {
		return nil, errors.New("relay envelope: channel ID is required")
	}
	if len(channelID) > MaxChannelIDBytes {
		return nil, errors.New("relay envelope: channel ID exceeds the envelope limit")
	}
	out := make([]byte, HeaderBytes+len(channelID)+len(payload))
	binary.BigEndian.PutUint16(out, uint16(len(channelID)))
	copy(out[HeaderBytes:], channelID)
	copy(out[HeaderBytes+len(channelID):], payload)
	return out, nil
}

// Unwrap 拆开信封。交回的载荷是 envelope 的切片,不复制。
func Unwrap(envelope []byte) (string, []byte, error) {
	if len(envelope) < HeaderBytes {
		return "", nil, errors.New("relay envelope: shorter than its channel ID length")
	}
	length := int(binary.BigEndian.Uint16(envelope[:HeaderBytes]))
	if length == 0 || length > MaxChannelIDBytes {
		return "", nil, errors.New("relay envelope: invalid channel ID length")
	}
	start := HeaderBytes + length
	if len(envelope) < start {
		return "", nil, errors.New("relay envelope: truncated before its payload")
	}
	id := envelope[HeaderBytes:start]
	if !utf8.Valid(id) {
		return "", nil, errors.New("relay envelope: channel ID is not UTF-8")
	}
	return string(id), envelope[start:], nil
}

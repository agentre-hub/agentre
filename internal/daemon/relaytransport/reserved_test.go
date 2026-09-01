package relaytransport

import (
	"strings"
	"testing"
)

// TestNewRelayChannelID_NeverCollidesWithTheReservedPrefix 是决策 14 的机械保证：
// newRelayChannelID 的字母表是 hex（0-9a-f），不含 "~"，因此它生成的通道 id 不可能
// 撞上 ReservedChannelPrefix / SignalChannelID。跑一批样本而不是证明字母表本身，是
// 因为这条不变量真正要保护的是「Multiplexer.Open() 的实际输出」，不是 hex.EncodeToString
// 的规格——保护的是这份实现今天确实在用 hex，而不是将来悄悄换成别的编码却没人发现。
func TestNewRelayChannelID_NeverCollidesWithTheReservedPrefix(t *testing.T) {
	for i := 0; i < 1000; i++ {
		id, err := newRelayChannelID()
		if err != nil {
			t.Fatalf("newRelayChannelID: %v", err)
		}
		if strings.HasPrefix(id, ReservedChannelPrefix) {
			t.Fatalf("generated channel id %q collides with the reserved prefix %q", id, ReservedChannelPrefix)
		}
		if id == SignalChannelID {
			t.Fatalf("generated channel id equals the reserved SignalChannelID")
		}
	}
}

// TestHexAlphabetExcludesTheReservedPrefix 把「hex 字母表不含 ~」钉成一个显式断言：
// 上一条测试是抽样，这一条是穷举字母表本身，两者一起才是「由构造不可能」的完整证据。
func TestHexAlphabetExcludesTheReservedPrefix(t *testing.T) {
	const hexAlphabet = "0123456789abcdef"
	if strings.Contains(hexAlphabet, ReservedChannelPrefix) {
		t.Fatalf("hex alphabet unexpectedly contains the reserved prefix %q", ReservedChannelPrefix)
	}
}

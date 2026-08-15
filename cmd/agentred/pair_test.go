package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

// 配对输出的全部作用就是让用户把一个**拨得通的**地址粘进桌面端,所以配对码和地址得一起给。
func TestGivenAdvertisedURLsWhenPrintingPairThenShowsCodeAndEveryURL(t *testing.T) {
	var buf bytes.Buffer

	printPair(&buf, map[string]any{
		"code":       "123456",
		"ttlSeconds": float64(120),
		"listenURLs": []any{"ws://192.168.1.9:7456/rpc", "ws://[fd00::1]:7456/rpc"},
	})

	out := buf.String()
	assert.Contains(t, out, "123456")
	assert.Contains(t, out, "120")
	assert.Contains(t, out, "ws://192.168.1.9:7456/rpc")
	assert.Contains(t, out, "ws://[fd00::1]:7456/rpc")
}

// daemon 在通配地址上监听、却一个可路由地址都找不到时,listenURLs 是空的 —— 它宁可
// 什么都不报,也不能把 "[::]" 那种 bind 地址交出去(见 rpc.LANServer.AdvertiseURLs)。
// 那么说清楚该怎么办就落在这里:只印一句 "On desktop, use any of:" 然后一片空白,
// 用户拿着配对码却不知道该往桌面端粘什么,更不会想到这台机器需要 --host。
func TestGivenNoAdvertisableURLWhenPrintingPairThenTellsUserToSetHost(t *testing.T) {
	var buf bytes.Buffer

	printPair(&buf, map[string]any{
		"code":       "123456",
		"ttlSeconds": float64(120),
		"listenURLs": []any{},
	})

	out := buf.String()
	assert.Contains(t, out, "123456", "配对码照发:地址给不出来不代表配对码作废")
	assert.Contains(t, out, "--host", "必须点名这台机器要用 --host 指定地址")
	assert.NotContains(t, out, "use any of", "一个地址都没有的时候,这句引导只会让用户去找不存在的东西")
}

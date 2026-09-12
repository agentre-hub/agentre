package portforward_test

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/wirelimits"
)

// 本文件与 expectcontinue_test.go 同属那一族**两端都是真的**的用例(共用
// forwardToRealDevice),钉的是「大响应体一个字节都不许少」。
//
// 为什么必须两端都真:截断出在 ack 与流收尾的竞态上,而 ack 的应答由设备侧真的那份
// 流表答复 —— 设备一打桩,ack 就永远成功,这条竞态就不存在了。

// largeBodyBytes 取得比信用窗口大得多是有讲究的:
//
//   - 必须 > PortForwardWindowBytes(4 MiB),否则设备一口气就发完了,宿主手上永远
//     不会有「还没写给浏览器的积压」,而积压正是被丢掉的那部分。
//   - 还要在窗口之上再留出好几个 ack 周期(宿主每消费四分之一个窗口回一次 ack),
//     因为被丢掉的触发条件是**流收尾之后还落下至少一次 ack**。
//
// 24 MiB 在环回上不到一秒,却能给出二十来次 ack。
const largeBodyBytes = 24 << 20

// linkLatency 是宿主与设备之间那条连接的每帧交付时延。
//
// 这一格是这条用例能**必现**的关键,而且它补的正是同进程管道相对真链路缺掉的那件事:
// 真链路上帧是排着队走的,设备发出收尾通知时,它前面还积压着好几兆已经发出、宿主
// 尚未收到的数据帧 —— 宿主要把那几兆消费完才看得见「这条流结束了」,而消费的路上
// 每 1 MiB 就落一次 ack。缺了这一段,收尾通知几乎与最后一块数据同时到达,同一条竞态
// 就成了偶发:实测无时延时 10 次里只中 2 次,1ms 时延下 10 次全中。
//
// 1ms/帧对 256 KiB 的分片相当于约 256 MiB/s —— 比真机上的链路还宽松,却已经足够让
// 一个信用窗口(4 MiB,16 帧)的数据排在收尾通知前面。
const linkLatency = time.Millisecond

// TestForward_GivenAResponseFarLargerThanTheWindow_ThenTheClientGetsEveryByte
//
// Given 上游交出一个远大于信用窗口的响应体(带 Content-Length,与浏览器下载文件同形),
// When 它经这条专属监听转发给客户端, Then 客户端收到的字节数与内容都与上游逐字相同。
//
// 少一个字节就是文件损坏:状态码仍是 200、Content-Length 仍是那个数,客户端只会在读到
// 一半时拿到一个 unexpected EOF(curl 报 exit 18 / CURLE_PARTIAL_FILE)。
func TestForward_GivenAResponseFarLargerThanTheWindow_ThenTheClientGetsEveryByte(t *testing.T) {
	require.Greater(t, int64(largeBodyBytes), wirelimits.PortForwardWindowBytes,
		"响应体必须大过信用窗口,否则这条用例根本走不到有积压的那条路")

	want := make([]byte, largeBodyBytes)
	// 有 pattern 的填充,不是零:只断字节数的话,一个「补零凑长度」的实现照样过。
	_, _ = rand.New(rand.NewSource(20260906)).Read(want)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(want)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(want)
	}))
	t.Cleanup(target.Close)

	address := forwardToRealDeviceWithLatency(t, linkLatency, target)

	client := &http.Client{Timeout: 60 * time.Second}
	response, err := client.Get(address + "/big.bin")
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()

	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, int64(len(want)), response.ContentLength,
		"转发必须原样带上上游的 Content-Length,客户端正是照它判断有没有读全")

	var got bytes.Buffer
	got.Grow(len(want))
	n, copyErr := io.Copy(&got, response.Body)
	require.NoError(t, copyErr, "响应体在第 %d 字节处断了(上游一共 %d 字节):%s",
		n, len(want), truncationHint(n, int64(len(want))))
	require.Equal(t, int64(len(want)), n, "客户端收到的字节数与上游对不上")
	assert.True(t, bytes.Equal(want, got.Bytes()), "客户端收到的内容与上游不是逐字相同")
}

// truncationHint 把断点换算成「离结尾还差多少、是不是落在一个信用窗口之内」——
// 落在窗口之内说明丢掉的正是收尾那一刻还压在宿主手上没写出去的那部分。
func truncationHint(got, want int64) string {
	missing := want - got
	return fmt.Sprintf("还差 %d 字节,信用窗口是 %d 字节(差额在一个窗口之内 = 丢的是收尾时的积压)",
		missing, wirelimits.PortForwardWindowBytes)
}

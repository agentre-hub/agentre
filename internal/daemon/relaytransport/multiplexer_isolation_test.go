package relaytransport

import (
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMultiplexer_GivenOneStalledChannel_WhenFramesKeepArriving_ThenOtherChannelsStillGetTheirs
//
// Given 一条虚拟通道的读者停住了(它背后那台桌面端的 protorpc 读循环卡住 / 干脆没人
//
//	在 ReadPayload),而同一条中继上还有别的通道在正常收发;
//
// When  停住那条的入站帧超过它的缓冲;
// Then  别的通道必须照常收到自己的帧。
//
// 修复前不成立,而且后果远不止「慢一点」:dispatch 是在 Multiplexer.receive() 这条
// **单 goroutine** 上 inline 调 enqueue 的,enqueue 一旦阻塞,receive() 就不再 drain
// HubLink.frames;frames(64 格)填满后 HubLink.serve 会走进那个 default 分支,
// 直接 conn.Close() 把**整条中继**判死 —— 一条通道的读者慢,代价是这台 daemon 上
// 所有对端的连接一起断、全部重拨。
//
// 隔离的代价落在闯祸的那条通道自己身上:它被关掉(见下一条用例),而不是由邻居分摊。
func TestMultiplexer_GivenOneStalledChannel_WhenFramesKeepArriving_ThenOtherChannelsStillGetTheirs(t *testing.T) {
	hub := newMultiplexerHubStub()
	mux := newMultiplexer(hub)
	t.Cleanup(mux.Close)
	hub.dial()

	stalled, err := mux.Open()
	require.NoError(t, err)
	healthy, err := mux.Open()
	require.NoError(t, err)

	want := []byte{0xde, 0xad, 0xbe, 0xef}
	// 帧从同一条 hub channel 进来 —— 和真实拓扑一致:健康通道那一帧排在停摆通道
	// 那一大串后面。灌帧要另起 goroutine,否则 receive() 一卡,测试自己也被 hub
	// stub 的 channel 堵住。
	go func() {
		for i := 0; i < virtualChannelBuffer*2; i++ {
			hub.frames <- HubFrame{
				MessageType: websocket.BinaryMessage,
				Payload:     marshalRelayEnvelope(stalled.ID(), []byte{byte(i)}),
			}
		}
		hub.frames <- HubFrame{
			MessageType: websocket.BinaryMessage,
			Payload:     marshalRelayEnvelope(healthy.ID(), want),
		}
	}()

	got := make(chan []byte, 1)
	go func() {
		payload, readErr := healthy.ReadPayload()
		if readErr == nil {
			got <- payload
		}
	}()

	select {
	case payload := <-got:
		assert.Equal(t, want, payload)
	case <-time.After(3 * time.Second):
		t.Fatal("一条通道的读者停住,就把同一条中继上其他通道的帧一起卡死了")
	}
}

// TestMultiplexer_GivenAStalledChannelOverflows_WhenItDoes_ThenOnlyThatChannelIsClosed
//
// 溢出必须有人担责,否则「不阻塞」就变成了「无上限缓冲」——中继是网络入口,对面
// 只要对一条没人读的通道猛灌就能把 daemon 撑爆。担责的是闯祸那条通道自己:关掉它,
// 邻居不受影响。这与 remote runtime 对 per-Run 事件缓冲的处置同一条纪律
// (溢出取消**那一个** generation,而不是整条连接)。
func TestMultiplexer_GivenAStalledChannelOverflows_WhenItDoes_ThenOnlyThatChannelIsClosed(t *testing.T) {
	hub := newMultiplexerHubStub()
	mux := newMultiplexer(hub)
	t.Cleanup(mux.Close)
	hub.dial()

	stalled, err := mux.Open()
	require.NoError(t, err)
	healthy, err := mux.Open()
	require.NoError(t, err)

	go func() {
		for i := 0; i < virtualChannelBuffer*2; i++ {
			hub.frames <- HubFrame{
				MessageType: websocket.BinaryMessage,
				Payload:     marshalRelayEnvelope(stalled.ID(), []byte{byte(i)}),
			}
		}
	}()

	select {
	case <-stalled.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("溢出的通道必须被关掉,否则缓冲无上限")
	}

	select {
	case <-healthy.Done():
		t.Fatal("邻居通道不该被连坐")
	case <-time.After(100 * time.Millisecond):
	}
}

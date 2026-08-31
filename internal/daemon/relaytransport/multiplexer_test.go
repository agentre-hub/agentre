package relaytransport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type multiplexerHubStub struct {
	frames chan HubFrame
	sent   chan HubFrame

	mu           sync.Mutex
	connected    bool
	onDial       []func()
	onDisconnect []func(error)
}

func newMultiplexerHubStub() *multiplexerHubStub {
	return &multiplexerHubStub{frames: make(chan HubFrame, 16), sent: make(chan HubFrame, 16)}
}

func (s *multiplexerHubStub) Send(messageType int, payload []byte) error {
	s.mu.Lock()
	connected := s.connected
	s.mu.Unlock()
	if !connected {
		return ErrHubUnavailable
	}
	s.sent <- HubFrame{MessageType: messageType, Payload: append([]byte(nil), payload...)}
	return nil
}

func (s *multiplexerHubStub) Receive() <-chan HubFrame { return s.frames }
func (s *multiplexerHubStub) AddLifecycleListener(onDial func(), onDisconnect func(error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onDial = append(s.onDial, onDial)
	s.onDisconnect = append(s.onDisconnect, onDisconnect)
}
func (s *multiplexerHubStub) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}
func (s *multiplexerHubStub) dial() {
	s.mu.Lock()
	s.connected = true
	hooks := append([]func(){}, s.onDial...)
	s.mu.Unlock()
	for _, hook := range hooks {
		hook()
	}
}
func (s *multiplexerHubStub) disconnect(err error) {
	s.mu.Lock()
	s.connected = false
	hooks := append([]func(error){}, s.onDisconnect...)
	s.mu.Unlock()
	for _, hook := range hooks {
		hook(err)
	}
}

func TestMultiplexer_GivenOpaqueBinaryFrames_WhenTheyFlow_ThenPreservesBytesWithoutCrossTalk(t *testing.T) {
	hub := newMultiplexerHubStub()
	mux := newMultiplexer(hub)
	t.Cleanup(mux.Close)
	hub.dial()

	first, err := mux.Open()
	require.NoError(t, err)
	second, err := mux.Open()
	require.NoError(t, err)
	want := []byte{0x08, 0xff, 0x00, 0x12, 0x01, 0x80}
	require.NoError(t, first.WritePayload(want))
	sent := receiveFrame(t, hub)
	channelID, got, err := unmarshalRelayEnvelope(sent.Payload)
	require.NoError(t, err)
	assert.Equal(t, first.ID(), channelID)
	assert.Equal(t, want, got)

	hub.frames <- HubFrame{MessageType: websocket.BinaryMessage, Payload: marshalRelayEnvelope(second.ID(), want)}
	read, err := second.ReadPayload()
	require.NoError(t, err)
	assert.Equal(t, want, read)
	select {
	case unexpected := <-first.inbound:
		t.Fatalf("first virtual channel received second channel payload: %x", unexpected)
	default:
	}
}

func TestMultiplexer_GivenInboundOpaquePayload_WhenChannelIsUnknown_ThenAcceptsAndRoutesIt(t *testing.T) {
	hub := newMultiplexerHubStub()
	mux := newMultiplexer(hub)
	t.Cleanup(mux.Close)
	hub.dial()
	want := []byte{0x0a, 0x02, 0xff, 0x00}
	hub.frames <- HubFrame{MessageType: websocket.BinaryMessage, Payload: marshalRelayEnvelope("server-channel-1", want)}
	select {
	case channel := <-mux.Accept():
		assert.Equal(t, "server-channel-1", channel.ID())
		got, err := channel.ReadPayload()
		require.NoError(t, err)
		assert.Equal(t, want, got)
	case <-time.After(time.Second):
		t.Fatal("multiplexer did not accept relay-initiated channel")
	}
}

func TestMultiplexer_GivenEmptyEnvelope_WhenItArrivesForAChannel_ThenClosesOnlyThatChannel(t *testing.T) {
	hub := newMultiplexerHubStub()
	mux := newMultiplexer(hub)
	t.Cleanup(mux.Close)
	hub.dial()
	departing, err := mux.Open()
	require.NoError(t, err)
	surviving, err := mux.Open()
	require.NoError(t, err)
	hub.frames <- HubFrame{MessageType: websocket.BinaryMessage, Payload: marshalRelayEnvelope(departing.ID(), nil)}
	assertChannelClosed(t, departing.Done())
	assertChannelOpen(t, surviving.Done())
	_, err = departing.ReadPayload()
	assert.ErrorIs(t, err, io.EOF)
}

func TestMultiplexer_GivenEmptyEnvelope_WhenChannelIsUnknown_ThenAcceptsNothing(t *testing.T) {
	hub := newMultiplexerHubStub()
	mux := newMultiplexer(hub)
	t.Cleanup(mux.Close)
	hub.dial()
	hub.frames <- HubFrame{MessageType: websocket.BinaryMessage, Payload: marshalRelayEnvelope("never-seen", nil)}
	select {
	case created := <-mux.Accept():
		t.Fatalf("channel close signal created channel %q", created.ID())
	case <-time.After(100 * time.Millisecond):
	}
}

func TestMultiplexer_GivenOneClosedChannel_WhenAnotherUsesRelay_ThenDoesNotReopenRetiredChannel(t *testing.T) {
	hub := newMultiplexerHubStub()
	mux := newMultiplexer(hub)
	t.Cleanup(mux.Close)
	hub.dial()
	closed, err := mux.Open()
	require.NoError(t, err)
	open, err := mux.Open()
	require.NoError(t, err)
	require.NoError(t, closed.Close())
	hub.frames <- HubFrame{MessageType: websocket.BinaryMessage, Payload: marshalRelayEnvelope(closed.ID(), []byte{1})}
	select {
	case recreated := <-mux.Accept():
		t.Fatalf("closed channel was recreated as %q", recreated.ID())
	case <-time.After(100 * time.Millisecond):
	}
	require.NoError(t, open.WritePayload([]byte{2}))
	channelID, _, err := unmarshalRelayEnvelope(receiveFrame(t, hub).Payload)
	require.NoError(t, err)
	assert.Equal(t, open.ID(), channelID)
	assertChannelOpen(t, open.Done())
}

func TestMultiplexer_GivenRelayDisconnect_WhenChannelsAreOpen_ThenClosesAllPromptly(t *testing.T) {
	hub := newMultiplexerHubStub()
	mux := newMultiplexer(hub)
	t.Cleanup(mux.Close)
	hub.dial()
	first, err := mux.Open()
	require.NoError(t, err)
	second, err := mux.Open()
	require.NoError(t, err)
	hub.disconnect(errors.New("relay disconnected"))
	assertChannelClosed(t, first.Done())
	assertChannelClosed(t, second.Done())
	assert.ErrorIs(t, first.WritePayload([]byte{1}), ErrClosed)
}

func TestMultiplexer_GivenHubLinkStops_WhenChannelIsOpen_ThenLifecycleClosesTheChannel(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade relay websocket: %v", err)
			return
		}
		defer func() { _ = ws.Close() }()
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	dialed := make(chan struct{}, 1)
	link := NewHubLink(HubLinkOptions{ServerURL: server.URL, AccessToken: "test-token", OnDial: func() { dialed <- struct{}{} }})
	mux := NewMultiplexer(link)
	t.Cleanup(mux.Close)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- link.Run(ctx) }()
	select {
	case <-dialed:
	case <-time.After(time.Second):
		t.Fatal("HubLink did not establish its relay connection")
	}
	channel, err := mux.Open()
	require.NoError(t, err)
	cancel()
	assertChannelClosed(t, channel.Done())
	require.NoError(t, <-runDone)
}

func TestMultiplexer_GivenDisconnectedHub_WhenWriting_ThenReportsUnavailableWithoutClosingChannel(t *testing.T) {
	hub := newMultiplexerHubStub()
	mux := newMultiplexer(hub)
	t.Cleanup(mux.Close)
	channel, err := mux.Open()
	require.NoError(t, err)
	assert.ErrorIs(t, channel.WritePayload([]byte{1}), ErrHubUnavailable)
	assertChannelOpen(t, channel.Done())
}

func receiveFrame(t *testing.T, hub *multiplexerHubStub) HubFrame {
	t.Helper()
	select {
	case frame := <-hub.sent:
		return frame
	case <-time.After(time.Second):
		t.Fatal("multiplexer did not send a relay frame")
		return HubFrame{}
	}
}

func assertChannelClosed(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("virtual channel did not close")
	}
}

func assertChannelOpen(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
		t.Fatal("virtual channel closed unexpectedly")
	default:
	}
}

/*
retired 是「这条通道已经关了」的记号，它挡住两件事：一条迟到的帧凭空造出幽灵
通道（dispatch），以及 Open() 把同一个 id 再发出去。两件事需要的都只是一个**很短
的时间窗** —— 覆盖通道关掉那一刻还在网上飞的帧，也就是一个 RTT，秒级。

可它此前只增不减，只有整条中继断开才清空。而浏览器每点一次技能面板 / 引擎设置 /
目录选择就是一条新通道，一条稳定跑几周的 agentred 上，这个 map 只会一直长。
channels 是删的，retired 不是 —— 同一个问题服务端的路由缓存早就解过
（clientRouteSweepThreshold + 到期删除），daemon 这边漏了。
*/
func TestMultiplexer_RetiredChannelsExpireInsteadOfAccumulating(t *testing.T) {
	hub := newMultiplexerHubStub()
	hub.dial()
	mux := newMultiplexer(hub)
	t.Cleanup(mux.Close)
	now := time.Now()
	mux.now = func() time.Time { return now }

	channel, err := mux.Open()
	require.NoError(t, err)
	require.NoError(t, channel.Close())
	require.Equal(t, 1, mux.retiredLen(), "刚关掉的通道要记着")

	// 记号还在有效期内：迟到的帧仍然被挡掉，不会造出幽灵通道。
	now = now.Add(retiredChannelTTL / 2)
	mux.dispatch(HubFrame{
		MessageType: websocket.BinaryMessage,
		Payload:     marshalRelayEnvelope(channel.ID(), []byte("late")),
	})
	select {
	case <-mux.Accept():
		t.Fatal("迟到的帧不该造出一条幽灵通道")
	default:
	}

	// 过了有效期就该被清掉，而不是永远留着。清理挂在写入路径上（顺带清），
	// 所以这里再关一条来触发它。
	now = now.Add(retiredChannelTTL)
	other, err := mux.Open()
	require.NoError(t, err)
	require.NoError(t, other.Close())
	require.Equal(t, 1, mux.retiredLen(), "过期的记号必须被清掉，只剩刚关的那一条")
}

// 断线仍然把记号整个清空：重连之后是全新的一批通道 id，旧记号一条都不作数。
func TestMultiplexer_DisconnectStillClearsRetired(t *testing.T) {
	hub := newMultiplexerHubStub()
	hub.dial()
	mux := newMultiplexer(hub)
	t.Cleanup(mux.Close)

	channel, err := mux.Open()
	require.NoError(t, err)
	require.NoError(t, channel.Close())
	require.Equal(t, 1, mux.retiredLen())

	mux.onDisconnect(io.EOF)
	require.Equal(t, 0, mux.retiredLen())
}

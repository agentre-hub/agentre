package relaytransport

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

const (
	relayEnvelopeHeaderSize = 2
	maxRelayChannelIDLength = 128
	virtualChannelBuffer    = 64
)

var ErrClosed = errors.New("relaytransport: channel closed")

type PayloadChannel interface {
	ReadPayload() ([]byte, error)
	WritePayload([]byte) error
	Close() error
	Done() <-chan struct{}
}

type relayHub interface {
	Send(int, []byte) error
	Receive() <-chan HubFrame
	AddLifecycleListener(func(), func(error))
	Connected() bool
}

type Multiplexer struct {
	hub       relayHub
	mu        sync.RWMutex
	channels  map[string]*VirtualChannel
	retired   map[string]struct{}
	accept    chan *VirtualChannel
	connected bool
	closed    bool
	stop      chan struct{}
	stopOnce  sync.Once
}

func NewMultiplexer(link *HubLink) *Multiplexer { return newMultiplexer(link) }
func newMultiplexer(hub relayHub) *Multiplexer {
	m := &Multiplexer{hub: hub, channels: map[string]*VirtualChannel{}, retired: map[string]struct{}{}, accept: make(chan *VirtualChannel, virtualChannelBuffer), connected: hub.Connected(), stop: make(chan struct{})}
	hub.AddLifecycleListener(m.onDial, m.onDisconnect)
	go m.receive()
	return m
}

func (m *Multiplexer) Open() (*VirtualChannel, error) {
	for {
		id, err := newRelayChannelID()
		if err != nil {
			return nil, err
		}
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, ErrClosed
		}
		_, active := m.channels[id]
		_, retired := m.retired[id]
		if active || retired {
			m.mu.Unlock()
			continue
		}
		channel := newVirtualChannel(m, id)
		m.channels[id] = channel
		m.mu.Unlock()
		return channel, nil
	}
}

func (m *Multiplexer) Accept() <-chan *VirtualChannel { return m.accept }
func (m *Multiplexer) Close() {
	m.stopOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		channels := m.takeChannelsLocked()
		m.mu.Unlock()
		for _, channel := range channels {
			channel.markClosed()
		}
		close(m.stop)
	})
}

func (m *Multiplexer) receive() {
	frames := m.hub.Receive()
	for {
		select {
		case <-m.stop:
			return
		case frame, ok := <-frames:
			if !ok {
				m.onDisconnect(io.EOF)
				return
			}
			m.dispatch(frame)
		}
	}
}

func (m *Multiplexer) dispatch(frame HubFrame) {
	if frame.MessageType != websocket.BinaryMessage {
		return
	}
	id, payload, err := unmarshalRelayEnvelope(frame.Payload)
	if err != nil {
		return
	}
	m.mu.Lock()
	if m.closed || !m.connected {
		m.mu.Unlock()
		return
	}
	if _, retired := m.retired[id]; retired {
		m.mu.Unlock()
		return
	}
	if len(payload) == 0 {
		channel := m.channels[id]
		m.mu.Unlock()
		if channel != nil {
			m.closeChannel(channel)
		}
		return
	}
	channel := m.channels[id]
	created := channel == nil
	if created {
		channel = newVirtualChannel(m, id)
		m.channels[id] = channel
	}
	m.mu.Unlock()
	if created {
		// 交给 Accept() 的消费方同样不能阻塞:这里是 receive() 的最后一条停摆路径,
		// 一停就是上面 enqueue 注释里那条「整条中继陪葬」的老路。积压说明没人在
		// Accept,新通道此刻也无从服务 —— 关掉它,而不是拖垮所有已建好的通道。
		select {
		case m.accept <- channel:
		case <-m.stop:
			_ = channel.Close()
			return
		default:
			m.closeChannel(channel)
			return
		}
	}
	channel.enqueue(payload)
}

func (m *Multiplexer) onDial() {
	m.mu.Lock()
	if !m.closed {
		m.connected = true
	}
	m.mu.Unlock()
}
func (m *Multiplexer) onDisconnect(error) {
	m.mu.Lock()
	m.connected = false
	channels := m.takeChannelsLocked()
	m.retired = map[string]struct{}{}
	m.mu.Unlock()
	for _, channel := range channels {
		channel.markClosed()
	}
}
func (m *Multiplexer) closeChannel(channel *VirtualChannel) {
	m.mu.Lock()
	if m.channels[channel.id] == channel {
		delete(m.channels, channel.id)
		m.retired[channel.id] = struct{}{}
	}
	m.mu.Unlock()
	channel.markClosed()
}
func (m *Multiplexer) write(channel *VirtualChannel, payload []byte) error {
	m.mu.RLock()
	active := !m.closed && m.channels[channel.id] == channel
	connected := m.connected
	m.mu.RUnlock()
	if !active {
		return ErrClosed
	}
	if !connected {
		return ErrHubUnavailable
	}
	return m.hub.Send(websocket.BinaryMessage, marshalRelayEnvelope(channel.id, payload))
}
func (m *Multiplexer) takeChannelsLocked() []*VirtualChannel {
	channels := make([]*VirtualChannel, 0, len(m.channels))
	for _, channel := range m.channels {
		channels = append(channels, channel)
	}
	m.channels = map[string]*VirtualChannel{}
	return channels
}

type VirtualChannel struct {
	mux       *Multiplexer
	id        string
	inbound   chan []byte
	done      chan struct{}
	writeMu   sync.Mutex
	closeOnce sync.Once
}

var _ PayloadChannel = (*VirtualChannel)(nil)

func newVirtualChannel(mux *Multiplexer, id string) *VirtualChannel {
	return &VirtualChannel{mux: mux, id: id, inbound: make(chan []byte, virtualChannelBuffer), done: make(chan struct{})}
}
func (c *VirtualChannel) ID() string { return c.id }
func (c *VirtualChannel) WritePayload(payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	return c.mux.write(c, payload)
}
func (c *VirtualChannel) ReadPayload() ([]byte, error) {
	select {
	case payload := <-c.inbound:
		return payload, nil
	case <-c.done:
		return nil, io.EOF
	}
}
func (c *VirtualChannel) Close() error          { c.mux.closeChannel(c); return nil }
func (c *VirtualChannel) Done() <-chan struct{} { return c.done }

// enqueue 投一帧给这条通道的读者。**永不阻塞**。
//
// 调用它的是 Multiplexer.receive() 这条单 goroutine,而 receive() 又是 HubLink.frames
// 唯一的 drainer。在这里阻塞一下,后果是整条中继:frames(64 格)填满后
// HubLink.serve 走 default 分支直接 conn.Close(),这台 daemon 上所有对端一起断线重拨
// —— 一条通道的读者慢,全体分摊。
//
// 缓冲满说明这条通道的读者已经跟不上了(常见成因:它背后那台桌面端的 protorpc 读循环
// 卡住了)。此时关掉**这一条**:既给「不阻塞」封了顶(中继是网络入口,无上限缓冲等于
// 让对面猛灌就能撑爆 daemon),又把代价留在闯祸的通道自己身上。同一条纪律见
// remote runtime 的 per-Run 事件缓冲:溢出取消那一个 generation,不是整条连接。
func (c *VirtualChannel) enqueue(payload []byte) {
	select {
	case <-c.done:
		return
	default:
	}
	select {
	case c.inbound <- append([]byte(nil), payload...):
	default:
		c.mux.closeChannel(c)
	}
}
func (c *VirtualChannel) markClosed() {
	c.writeMu.Lock()
	c.closeOnce.Do(func() { close(c.done) })
	c.writeMu.Unlock()
}

func marshalRelayEnvelope(channelID string, payload []byte) []byte {
	id := []byte(channelID)
	out := make([]byte, relayEnvelopeHeaderSize+len(id)+len(payload))
	binary.BigEndian.PutUint16(out, uint16(len(id)))
	copy(out[relayEnvelopeHeaderSize:], id)
	copy(out[relayEnvelopeHeaderSize+len(id):], payload)
	return out
}
func unmarshalRelayEnvelope(payload []byte) (string, []byte, error) {
	if len(payload) < relayEnvelopeHeaderSize {
		return "", nil, errors.New("relay envelope is missing its channel ID length")
	}
	length := int(binary.BigEndian.Uint16(payload[:relayEnvelopeHeaderSize]))
	if length == 0 || length > maxRelayChannelIDLength {
		return "", nil, errors.New("relay envelope has an invalid channel ID length")
	}
	start := relayEnvelopeHeaderSize + length
	if len(payload) < start {
		return "", nil, errors.New("relay envelope is truncated before its payload")
	}
	id := payload[relayEnvelopeHeaderSize:start]
	if !utf8.Valid(id) {
		return "", nil, errors.New("relay envelope channel ID is not UTF-8")
	}
	return string(id), payload[start:], nil
}
func newRelayChannelID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

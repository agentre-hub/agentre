package chat_svc

import (
	"context"
	"strconv"
	"strings"
	"sync"
)

// sessionEventQueueDepth 是**每个订阅者**的有界缓冲深度。
//
// 与 peerstream 的 peerSubscriberQueueDepth 取同一个数:同一条纪律 —— 订阅者
// (agrctl acp 里的 ACP agent)慢,绝不能拖住真实对话的 runTurn。队列满即丢帧,
// 不阻塞生产者。事件帧不带 seq,丢了不可补齐;这是刻意的:ACP 侧的逐 token 流
// 只服务即时呈现,慢到丢帧说明订阅者已经不健康,继续无界排队只会吃内存。
const sessionEventQueueDepth = 256

// SessionEventSubscriber 是「进程内会话级流事件订阅」端口的实现方，即包了
// NewFanoutEmitter 的那份 Emitter。chatSvc 把它从注入的 emitter 上摘出来。
type SessionEventSubscriber interface {
	// SubscribeSessionEvents 订阅某会话的流事件；返回只读通道 + 取消函数。
	// 取消后通道被关闭，重复调用取消幂等。
	SubscribeSessionEvents(sessionID int64) (<-chan ChatStreamEvent, func())
}

type sessionSubscription struct {
	ch chan ChatStreamEvent
}

// fanoutEmitter 把每一条 ChatStreamEvent 同时交给原 emitter(继续流向 Wails)与
// 任意多个进程内订阅者。装配成 NewChat(NewFanoutEmitter(NewCoalescingEmitter(inner)))
// —— 扇出在最外层,拿到的是 chat_svc 发出的原始事件。
type fanoutEmitter struct {
	inner Emitter

	mu   sync.Mutex
	subs map[int64]map[*sessionSubscription]struct{}
}

// FanoutEmitter 是 NewFanoutEmitter 的返回类型:既是一份普通 Emitter(供 Wails
// 链路继续消费),也带进程内订阅入口。
type FanoutEmitter interface {
	Emitter
	SessionEventSubscriber
}

// NewFanoutEmitter 把 inner 包成带会话级订阅能力的 emitter。
func NewFanoutEmitter(inner Emitter) FanoutEmitter {
	if inner == nil {
		inner = NoopEmitter{}
	}
	return &fanoutEmitter{
		inner: inner,
		subs:  map[int64]map[*sessionSubscription]struct{}{},
	}
}

func (f *fanoutEmitter) Emit(ctx context.Context, stream string, payload any) {
	f.fanout(stream, payload)
	f.inner.Emit(ctx, stream, payload)
}

// fanout 按 stream 名解析 sessionID 后向订阅者非阻塞投递。持锁投递 + 持锁注销,
// 配合有界缓冲的 select/default,既保证「不阻塞生产者」,也排除 send-on-closed。
func (f *fanoutEmitter) fanout(stream string, payload any) {
	ev, ok := payload.(ChatStreamEvent)
	if !ok {
		return
	}
	sessionID, ok := SessionIDFromStreamName(stream)
	if !ok {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for sub := range f.subs[sessionID] {
		select {
		case sub.ch <- ev:
		default: // 满 → 丢这条,绝不阻塞 runTurn
		}
	}
}

// SubscribeSessionEvents 见 SessionEventSubscriber。
func (f *fanoutEmitter) SubscribeSessionEvents(sessionID int64) (<-chan ChatStreamEvent, func()) {
	sub := &sessionSubscription{ch: make(chan ChatStreamEvent, sessionEventQueueDepth)}
	f.mu.Lock()
	if f.subs[sessionID] == nil {
		f.subs[sessionID] = map[*sessionSubscription]struct{}{}
	}
	f.subs[sessionID][sub] = struct{}{}
	f.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			set := f.subs[sessionID]
			if _, ok := set[sub]; !ok {
				return
			}
			delete(set, sub)
			if len(set) == 0 {
				delete(f.subs, sessionID)
			}
			close(sub.ch)
		})
	}
	return sub.ch, cancel
}

// SessionIDFromStreamName 从 per-turn 事件名 "chat:event:<sessionID>:<msgID>" 解出
// sessionID。会话级旁路(chat:autonomous:...)与任何异构名字都返回 ok=false —— 它们
// 不是本轮订阅的语义域。
func SessionIDFromStreamName(stream string) (int64, bool) {
	rest, ok := strings.CutPrefix(stream, StreamEventPrefix+":")
	if !ok {
		return 0, false
	}
	raw, _, ok := strings.Cut(rest, ":")
	if !ok || raw == "" {
		return 0, false
	}
	sessionID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || sessionID <= 0 {
		return 0, false
	}
	return sessionID, true
}

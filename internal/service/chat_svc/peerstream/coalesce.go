package peerstream

import (
	"strings"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

// deltaFlushInterval 与桌面 Wails 侧的 coalescingEmitter 同一个节奏(16ms ≈ 一帧)。
// 不 import chat_svc(它依赖本包),所以在此复述同一个数。
const deltaFlushInterval = 16 * time.Millisecond

// deltaFlushBytes 单批字节上限,防止一次超大 delta 把批撑得过大。
const deltaFlushBytes = 16 << 10

// previewTimer 是尾巴定时器的句柄(time.Timer 满足它)。
type previewTimer interface{ Stop() bool }

// previewCoalescer 是一条会话上还没发出的同类预览文本。受 publication.mu 保护。
//
// 判据与 Wails 侧 coalescingEmitter 一致:距上次发送已满一个间隔(或从未发过)就立即发,
// 否则攒起来并挂一个到「上次发送 + 间隔」的尾巴定时器。gen 在每次摘走待发文本时加一,
// 让已经排队的过期定时器回调认出自己过期而什么都不发。
type previewCoalescer struct {
	kind     previewKind
	buf      strings.Builder
	timer    previewTimer
	gen      uint64
	lastSent time.Time
}

type previewKind uint8

const (
	previewNone previewKind = iota
	previewText
	previewThinking
)

func previewKindOf(event agentruntime.Event) (previewKind, string) {
	switch ev := event.(type) {
	case agentruntime.TextDelta:
		return previewText, ev.Text
	case agentruntime.ThinkingDelta:
		return previewThinking, ev.Text
	}
	return previewNone, ""
}

func (s *Publisher) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Publisher) armTimer(d time.Duration, f func()) previewTimer {
	if s.afterFunc != nil {
		return s.afterFunc(d, f)
	}
	return time.AfterFunc(d, f)
}

// takePreviewLocked 摘走待发文本并停掉尾巴定时器;没有待发文本时返回 false。调用方持 mu。
func (s *Publisher) takePreviewLocked(publication *peerSessionPublication) (agentruntime.Event, bool) {
	c := &publication.preview
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	if c.kind == previewNone {
		return nil, false
	}
	text := c.buf.String()
	kind := c.kind
	c.buf.Reset()
	c.kind = previewNone
	c.gen++
	c.lastSent = s.clock()
	if kind == previewThinking {
		return agentruntime.ThinkingDelta{Text: text}, true
	}
	return agentruntime.TextDelta{Text: text}, true
}

// flushPreviewLocked 把待发文本排给所有订阅者。任何非文本帧、持久帧、终态帧在排队前
// 都先调它,帧的相对顺序因此与不合帧时一致。调用方持 mu。
func (s *Publisher) flushPreviewLocked(publication *peerSessionPublication) {
	if event, ok := s.takePreviewLocked(publication); ok {
		enqueuePreviewLocked(publication, event)
	}
}

func enqueuePreviewLocked(publication *peerSessionPublication, event agentruntime.Event) {
	frame := wire.EventFrame{ConversationID: publication.conversationID, Event: event, Preview: true}
	for _, subscription := range publication.subscribers {
		enqueuePeerFrame(subscription, frame)
	}
}

// addPreviewTextLocked 累积一段文本;到点(或超量)就立即排队,否则挂尾巴定时器。调用方持 mu。
func (s *Publisher) addPreviewTextLocked(publication *peerSessionPublication, kind previewKind, text string) {
	c := &publication.preview
	if c.kind != previewNone && c.kind != kind {
		s.flushPreviewLocked(publication)
	}
	c.kind = kind
	c.buf.WriteString(text)
	elapsed := s.clock().Sub(c.lastSent)
	if c.buf.Len() >= deltaFlushBytes || c.lastSent.IsZero() || elapsed >= deltaFlushInterval {
		s.flushPreviewLocked(publication)
		return
	}
	if c.timer == nil {
		gen := c.gen
		c.timer = s.armTimer(deltaFlushInterval-elapsed, func() { s.firePreview(publication, gen) })
	}
}

// firePreview 是尾巴定时器的回调:仍属于同一代才发,否则已被别的 flush / 释放接管。
func (s *Publisher) firePreview(publication *peerSessionPublication, gen uint64) {
	publication.mu.Lock()
	if publication.closed || publication.preview.gen != gen {
		publication.mu.Unlock()
		return
	}
	s.flushPreviewLocked(publication)
	publication.mu.Unlock()
	publication.signal()
}

// signal 叫醒 flush worker 一次(单槽、不阻塞)。
func (p *peerSessionPublication) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// closeLocked 释放一份 publication:摘出表、停 worker 与定时器、放掉历史与待发文本。
// 调用方持 mu。幂等。之后到达的 attach 在注册处看到 closed 会重建一份新的。
func (s *Publisher) closeLocked(publication *peerSessionPublication) {
	if publication.closed {
		return
	}
	publication.closed = true
	if publication.preview.timer != nil {
		publication.preview.timer.Stop()
	}
	publication.preview = previewCoalescer{}
	publication.history = nil
	publication.createtimes = nil
	publication.subscribers = map[string]*peerSessionSubscription{}
	close(publication.stop)
	s.peerPublications.CompareAndDelete(publication.sessionID, publication)
}

// releaseIfIdle 在没有订阅者时释放 publication(首次装载失败时用)。
func (s *Publisher) releaseIfIdle(publication *peerSessionPublication) {
	publication.mu.Lock()
	defer publication.mu.Unlock()
	if len(publication.subscribers) == 0 {
		s.closeLocked(publication)
	}
}

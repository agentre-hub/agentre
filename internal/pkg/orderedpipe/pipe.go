// Package orderedpipe 提供一条**非阻塞**的有序投递管道:生产方 Push 永不阻塞
// (缓冲无界),一个 pump goroutine 按序把缓冲里的元素送到 Out()。
//
// 它服务的是同一类结构:一条**单 goroutine 的读循环**既负责把帧派发给各路消费方,
// 又是唯一能 close 这些出口的人。这样的读循环一旦在某条有界 channel 上做阻塞发送,
// 而解除阻塞要等某个消费方先往前走 —— 那个消费方却在等读循环去 close 另一路、或
// 去交回一个 RPC 应答 —— 就成了闭环死锁。
//
// 现场(sess-3110):claudecode 的 readLoop 在自主续轮活跃期间按 owner 各开一路旁路
// 活动轮,消费方却是串行 inline drain 的,第二路灌满缓冲后 readLoop 卡死,整条会话
// 连同子进程冻了五个多小时。remote runtime 的自主续轮走的是同一条形状:
// protorpc.Conn.Serve 也是单 goroutine,inline 派发通知的同时还要把 RPC 应答交回
// 等待方。
//
// 换成 pipe 之后,「读循环的推进不依赖任何消费方的节奏」是结构性保证,而不是靠每个
// 消费方都必须并发 drain 的口头约定 —— 后者已经被违反过不止一次,而且从生产方那一侧
// 根本看不出来。
//
// 代价是缓冲无界:消费方停摆时内存随帧数增长。这是刻意的取舍 —— 对立面是整条连接
// 冻死,而消费方停摆本身是 bug、不是稳态。
package orderedpipe

import "sync"

// Pipe 是一条非阻塞的有序投递管道。零值不可用,必须经 New 构造。
type Pipe[T any] struct {
	ch chan T

	mu        sync.Mutex
	buf       []T
	closed    bool
	abandoned bool

	// notify 是 pump 的唤醒信号(容量 1,合并重复唤醒):Push / Close / Abandon 改完
	// 状态后敲一下。pump 只在「查过一遍没活干」之后才去取它,所以不会丢唤醒。
	notify chan struct{}
	// dropped 在 Abandon 时 close:pump 可能正卡在 ch <- v 上,靠它挣脱。
	dropped chan struct{}
}

// chanBuffer 给出口留一点缓冲,让常规节奏下的消费方不必和 pump 做逐个握手。
// 它只影响吞吐,不影响「Push 永不阻塞」——缓冲满了压在 pump 上,压不到生产方。
const chanBuffer = 16

// New 构造一条管道并起它的 pump。Close 之后 pump 退出;永不 Close 的管道会漏一个
// goroutine,所以每条管道都必须有明确的收尾者。
func New[T any]() *Pipe[T] {
	p := &Pipe[T]{
		ch:      make(chan T, chanBuffer),
		notify:  make(chan struct{}, 1),
		dropped: make(chan struct{}),
	}
	go p.pump()
	return p
}

// Out 返回消费方 range 的出口。Close 之后(且缓冲已投完)它被关闭。
func (p *Pipe[T]) Out() <-chan T { return p.ch }

// Push 追加一个元素。已 Close / 已 Abandon 时丢弃 —— 收尾与投递是两条路径,
// 收尾后仍可能有一帧在途。永不阻塞。
func (p *Pipe[T]) Push(v T) {
	p.mu.Lock()
	if p.closed || p.abandoned {
		p.mu.Unlock()
		return
	}
	p.buf = append(p.buf, v)
	p.mu.Unlock()
	p.wake()
}

// Close 标记不再有新元素。pump 把缓冲投完后关闭出口。幂等 —— 同一条管道可能有
// 多条收尾路径(正常收尾与断连清理)都走到。
func (p *Pipe[T]) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()
	p.wake()
}

// Abandon 表示消费方已放弃(比如它那一轮的 ctx 被取消):丢掉缓冲、后续 Push 一并
// 丢弃,并把可能正卡在投递上的 pump 挣脱。出口仍等 Close 才关 —— 收尾时机由生产方
// 决定,与放弃前一致。幂等。
func (p *Pipe[T]) Abandon() {
	p.mu.Lock()
	if p.abandoned {
		p.mu.Unlock()
		return
	}
	p.abandoned = true
	p.buf = nil
	close(p.dropped)
	p.mu.Unlock()
	p.wake()
}

// wake 敲一下 notify(非阻塞,重复唤醒自动合并)。
func (p *Pipe[T]) wake() {
	select {
	case p.notify <- struct{}{}:
	default:
	}
}

// pump 是 Pipe 唯一的投递者,因此出口天然保序。
func (p *Pipe[T]) pump() {
	defer close(p.ch)
	for {
		p.mu.Lock()
		if len(p.buf) > 0 && !p.abandoned {
			v := p.buf[0]
			var zero T
			p.buf[0] = zero // 别让已投出的元素继续被底层数组引用住
			p.buf = p.buf[1:]
			p.mu.Unlock()
			select {
			case p.ch <- v:
			case <-p.dropped:
			}
			continue
		}
		closed := p.closed
		p.mu.Unlock()
		if closed {
			return
		}
		<-p.notify
	}
}

package claudecode

import "sync"

// pipe 是一条**非阻塞**的有序投递管道:生产方 push 永不阻塞(缓冲无界),一个 pump
// goroutine 按序把缓冲里的元素送到 out()。
//
// 为什么需要它(sess-3110):readLoop 是单 goroutine,同时又是**唯一**能 close 各轮
// 事件出口的人。它一旦在有界 channel 上做阻塞发送,而解除阻塞要等消费方先 drain 完
// 另一路 —— 消费方却在等 readLoop 去 close 那一路 —— 就成了闭环死锁。现场:自主续轮
// 活跃期间按 owner 各开一路旁路活动轮,消费方却是串行 inline drain 的,第二路灌满
// 缓冲后 readLoop 卡死,整条会话连同子进程冻了五个多小时。
//
// 换成 pipe 之后,「readLoop 的推进不依赖任何消费方的节奏」是结构性保证,而不是靠
// 每个消费方都必须并发 drain 的口头约定 —— 后者在这里已经被违反过一次,而且从
// Session 这一侧根本看不出来。
//
// 代价是缓冲无界:消费方停摆时内存随帧数增长。这是刻意的取舍 —— 对立面是整条会话
// 冻死,而消费方停摆本身是 bug、不是稳态。
type pipe[T any] struct {
	ch chan T

	mu        sync.Mutex
	buf       []T
	closed    bool
	abandoned bool

	// notify 是 pump 的唤醒信号(容量 1,合并重复唤醒):push / close / abandon 改完
	// 状态后敲一下。pump 只在「查过一遍没活干」之后才去取它,所以不会丢唤醒。
	notify chan struct{}
	// dropped 在 abandon 时 close:pump 可能正卡在 ch <- v 上,靠它挣脱。
	dropped chan struct{}
}

// pipeChanBuffer 给出口留一点缓冲,让常规节奏下的消费方不必和 pump 做逐个握手。
// 它只影响吞吐,不影响「push 永不阻塞」——缓冲满了压在 pump 上,压不到生产方。
const pipeChanBuffer = 16

func newPipe[T any]() *pipe[T] {
	p := &pipe[T]{
		ch:      make(chan T, pipeChanBuffer),
		notify:  make(chan struct{}, 1),
		dropped: make(chan struct{}),
	}
	go p.pump()
	return p
}

// out 返回消费方 range 的出口。close 之后(且缓冲已投完)它被关闭。
func (p *pipe[T]) out() <-chan T { return p.ch }

// push 追加一个元素。已 close / 已 abandon 时丢弃 —— 收尾与投递是两条路径,
// 收尾后仍可能有一帧在途。永不阻塞。
func (p *pipe[T]) push(v T) {
	p.mu.Lock()
	if p.closed || p.abandoned {
		p.mu.Unlock()
		return
	}
	p.buf = append(p.buf, v)
	p.mu.Unlock()
	p.wake()
}

// close 标记不再有新元素。pump 把缓冲投完后关闭出口。幂等 —— finishActiveTurn 与
// shutdownReader 两条收尾路径可能都走到同一轮。
func (p *pipe[T]) close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()
	p.wake()
}

// abandon 表示消费方已放弃(Turn 的 ctx 取消):丢掉缓冲、后续 push 一并丢弃,并把
// 可能正卡在投递上的 pump 挣脱。出口仍等 close 才关 —— 收尾时机由 readLoop 决定,
// 与放弃前一致。幂等。
func (p *pipe[T]) abandon() {
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
func (p *pipe[T]) wake() {
	select {
	case p.notify <- struct{}{}:
	default:
	}
}

// pump 是 pipe 唯一的投递者,因此出口天然保序。
func (p *pipe[T]) pump() {
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

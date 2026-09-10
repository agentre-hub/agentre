package transcript

// publish.go 是「一条已落库的消息此刻该把哪些持久帧发出去」这条判断。两个宿主共用
// 一份（桌面端 chat_svc 的对端发布、agentred 的实时发布）：它们必须在同一帧上定稿、
// 在同一次原地修补上重发，否则同一段内容在两台机器上会被编成两套号，而「同一份内容
// 在宿主重启前后仍是同一个 seq」正是靠这个位置台账成立的（规格 2026-09-05「帧编号」
// 与「复用边界」）。

// FramePublisher 记着「这条转录的哪个位置已经发布过什么内容」，据此挑出此刻该发布的
// 持久帧。零值不可用，构造走 NewFramePublisher。
//
// 它不碰编号台账：取号是宿主的事（分配与落库不可分，见 transcript_repo.FrameSeq）。
// 这里只回答「哪些帧现在该发」。
type FramePublisher struct {
	published map[FrameKey]string
}

func NewFramePublisher() *FramePublisher {
	return &FramePublisher{published: map[FrameKey]string{}}
}

// Pending 挑出此刻该发布的帧。
//
// final=false 是轮内 checkpoint：落在结尾的 text / thinking 块可能只是累加器里还在长
// 的那一段（acc.Snapshot 把未 flush 的缓冲也当块交出来），现在给它取号，下一次
// checkpoint 内容变长就会变成第二个号、第二帧，对端的转录里同一段话出现两次。它们等
// 收口那一次（final=true）再发。消息级派生帧（usage / done）同理：要等这一轮真的结束。
//
// 已经发布过、内容一字未变的位置跳过；内容变了（块被原地修补）则重新交出，由宿主取一个
// 新的末尾号。
func (p *FramePublisher) Pending(keyed []KeyedFrame, final bool) []KeyedFrame {
	if p == nil {
		return nil
	}
	if !final {
		keyed = SettledFrames(keyed)
	}
	pending := make([]KeyedFrame, 0, len(keyed))
	for _, frame := range keyed {
		if prev, ok := p.published[frame.Key]; ok && frame.Fingerprint != "" && prev == frame.Fingerprint {
			continue
		}
		pending = append(pending, frame)
	}
	return pending
}

// Commit 记下这些位置已经发布出去的那份内容。调用方**取到号并发出去之后**才调用它：
// 取号失败时这一帧没有发布，下一次还要再试。
func (p *FramePublisher) Commit(frames []KeyedFrame) {
	if p == nil {
		return
	}
	for _, frame := range frames {
		p.published[frame.Key] = frame.Fingerprint
	}
}

// SettledFrames 砍掉结尾那些「还会继续长」的帧：未收口的正文块与消息级派生帧。
//
// 它同时服务写与读：轮内发布只发定稿的那些帧，而补齐**读**到一条在飞的消息时也只能
// 交出同样这些 —— 编号是一次性的（分配与落库不可分），补齐若先给还在长的正文块或
// 那条空的 Done 取了号，收口时同一个位置的内容变了就得再取一个新号，对端于是收到
// 两份同一段内容。两个时刻必须由同一行代码判定。
func SettledFrames(frames []KeyedFrame) []KeyedFrame {
	end := len(frames)
	for end > 0 {
		last := frames[end-1]
		if last.Key.BlockIdx == MessageDerivedBlockIdx || isGrowingTextBlock(last.BlockType) {
			end--
			continue
		}
		break
	}
	return frames[:end]
}

func isGrowingTextBlock(blockType string) bool {
	switch blockType {
	case "text", "display_text", "thinking":
		return true
	default:
		return false
	}
}

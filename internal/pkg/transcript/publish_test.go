package transcript_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/pkg/transcript"
)

func keyed(messageID int64, blockIdx int, blockType, fingerprint string) transcript.KeyedFrame {
	return transcript.KeyedFrame{
		Key:         transcript.FrameKey{MessageID: messageID, BlockIdx: blockIdx},
		BlockType:   blockType,
		Fingerprint: fingerprint,
	}
}

func keys(frames []transcript.KeyedFrame) []transcript.FrameKey {
	out := make([]transcript.FrameKey, 0, len(frames))
	for _, frame := range frames {
		out = append(out, frame.Key)
	}
	return out
}

// TestFramePublisher_WithholdsFramesThatAreStillGrowing —— 轮内 checkpoint 只发已经
// 定稿的帧:结尾那个还在长的正文块与消息级派生帧留到收口那一次。
//
// 现在就给还在长的正文块取号,下一次 checkpoint 它变长了就会拿到第二个号、第二帧,
// 对端的转录里同一段话出现两次。
func TestFramePublisher_WithholdsFramesThatAreStillGrowing(t *testing.T) {
	frames := []transcript.KeyedFrame{
		keyed(7, 0, "text", "f-text"),
		keyed(7, 1, "tool_use", "f-tool"),
		keyed(7, 2, "text", "f-tail"),
		{Key: transcript.FrameKey{MessageID: 7, BlockIdx: transcript.MessageDerivedBlockIdx}, Fingerprint: "f-done"},
	}

	publisher := transcript.NewFramePublisher()
	assert.Equal(t, keys(frames[:2]), keys(publisher.Pending(frames, false)),
		"轮内只发前两帧:结尾的正文块还会继续长,派生帧要等这一轮结束")
	assert.Equal(t, keys(frames), keys(publisher.Pending(frames, true)),
		"收口那一次全都定稿")
}

// TestFramePublisher_SkipsPositionsAlreadyPublishedUnchanged —— 已经发布过、内容一字
// 未变的位置不再发:重发会让宿主给同一段内容取第二个号。
func TestFramePublisher_SkipsPositionsAlreadyPublishedUnchanged(t *testing.T) {
	first := []transcript.KeyedFrame{keyed(7, 0, "tool_use", "f-1")}
	publisher := transcript.NewFramePublisher()

	pending := publisher.Pending(first, false)
	assert.Len(t, pending, 1, "第一次当然要发")
	publisher.Commit(pending)

	assert.Empty(t, publisher.Pending(first, false), "同一位置同一内容不再发第二遍")
	assert.Empty(t, publisher.Pending(first, true), "收口那一次同样不重发没变过的位置")

	patched := []transcript.KeyedFrame{keyed(7, 0, "tool_use", "f-2")}
	assert.Equal(t, keys(patched), keys(publisher.Pending(patched, false)),
		"块被原地修补过:同一位置指纹变了就要重新发一帧,由宿主取一个新的末尾号")
}

// TestFramePublisher_CommitOnlyAfterTheFramesReallyWentOut —— 取号失败时调用方不
// Commit,那一帧下一次还要再试。这条纪律靠「Pending 与 Commit 分开」表达。
func TestFramePublisher_CommitOnlyAfterTheFramesReallyWentOut(t *testing.T) {
	frames := []transcript.KeyedFrame{keyed(7, 0, "tool_use", "f-1")}
	publisher := transcript.NewFramePublisher()

	_ = publisher.Pending(frames, false) // 取号失败,没有 Commit

	assert.Equal(t, keys(frames), keys(publisher.Pending(frames, false)),
		"没发出去的帧下一次还得发")
}

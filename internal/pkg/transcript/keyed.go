package transcript

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

// FrameKey 定位一个持久帧在转录里的位置：它由哪条消息的哪个块的第几帧投影而来。
//
// BlockIdx = MessageDerivedBlockIdx 表示消息级派生帧（UsageUpdate / ErrorEvent /
// Done）；Ordinal 是该块（或该消息尾部）投影出的第几帧，一个块投影出不止一帧时它
// 才非零。
//
// 这三格合起来是**内容的身份**：它不随「这是转录里的第几帧」漂移，所以原地修补往
// 中间插入一帧时，后面那些帧的身份、进而它们的编号都不变。
type FrameKey struct {
	MessageID int64
	BlockIdx  int
	Ordinal   int
}

// MessageDerivedBlockIdx 是消息级派生帧的块下标 —— 它们不属于任何块。
const MessageDerivedBlockIdx = -1

// KeyedFrame 是一个持久帧连同它在转录里的位置与内容指纹。
type KeyedFrame struct {
	Key   FrameKey
	Frame wire.EventFrame
	// BlockType 是产生这一帧的块类型；消息级派生帧留空。轮内 checkpoint 靠它认出
	// 结尾那些**还会继续长**的正文块。
	BlockType string
	// Fingerprint 是帧内容的指纹。同一个位置指纹变了 = 这个块被原地修补过，修补后
	// 的那一帧要取一个新的末尾号（规格 2026-09-05「帧编号」）。
	Fingerprint string
	Createtime  int64
}

// ProjectKeyedMessage 把一条消息摊成带位置的持久帧。
//
// 位置不是本函数自己数出来的：它把每个块**单独**装进一条同角色的消息交给
// ProjectMessages，于是「这一帧来自哪个块、是该块投影出的第几帧」由投影器自己说了
// 算 —— 因此这里不持有第二份「块 → 帧」的分派表。
//
// 两个宿主（桌面端 chat_svc 的对端发布 / agentred 的补齐读侧）共用这一份：位置与
// 编号挂靠是「同一份内容在宿主重启前后是同一个 seq」的全部依据，复制一份就等于让
// 两台机器对同一段转录给出两套编号（规格「复用边界」的判据）。
func ProjectKeyedMessage(conversationID string, msg *transcript_entity.Message) ([]KeyedFrame, error) {
	if msg == nil {
		return nil, nil
	}
	blocksJSON := msg.BlocksJSON
	if blocksJSON == "" {
		blocksJSON = "[]"
	}
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(blocksJSON), &raw); err != nil {
		return nil, fmt.Errorf("message %d blocks: %w", msg.ID, err)
	}
	out := make([]KeyedFrame, 0, len(raw)+1)
	for idx, block := range raw {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(block, &head); err != nil {
			return nil, fmt.Errorf("message %d block %d: %w", msg.ID, idx, err)
		}
		single := *msg
		single.BlocksJSON = "[" + string(block) + "]"
		clearMessageDerivedFields(&single)
		frames, createtimes, err := ProjectMessages(conversationID, []*transcript_entity.Message{&single})
		if err != nil {
			return nil, err
		}
		// 清空派生那几格之后,单块消息的尾巴是固定的:assistant 恒剩一帧 Done,user 没有。
		frames = frames[:len(frames)-messageDerivedFrameCount(&single)]
		for ordinal := range frames {
			out = append(out, KeyedFrame{
				Key:         FrameKey{MessageID: msg.ID, BlockIdx: idx, Ordinal: ordinal},
				Frame:       frames[ordinal],
				BlockType:   head.Type,
				Fingerprint: FrameFingerprint(frames[ordinal]),
				Createtime:  createtimes[ordinal],
			})
		}
	}
	// 消息级派生帧:同一条消息去掉全部块之后剩下的就是它们,顺序仍由投影器决定。
	derived := *msg
	derived.BlocksJSON = "[]"
	frames, createtimes, err := ProjectMessages(conversationID, []*transcript_entity.Message{&derived})
	if err != nil {
		return nil, err
	}
	for ordinal := range frames {
		out = append(out, KeyedFrame{
			Key:         FrameKey{MessageID: msg.ID, BlockIdx: MessageDerivedBlockIdx, Ordinal: ordinal},
			Frame:       frames[ordinal],
			Fingerprint: FrameFingerprint(frames[ordinal]),
			Createtime:  createtimes[ordinal],
		})
	}
	return out, nil
}

// ProjectKeyedMessages 把整条转录摊成带位置的持久帧,顺序与 ProjectMessages 一致
// (按消息 seq,稳定)。
func ProjectKeyedMessages(conversationID string, messages []*transcript_entity.Message) ([]KeyedFrame, error) {
	sorted := append([]*transcript_entity.Message(nil), messages...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i] == nil {
			return false
		}
		if sorted[j] == nil {
			return true
		}
		return sorted[i].Seq < sorted[j].Seq
	})
	out := make([]KeyedFrame, 0, len(sorted))
	for _, message := range sorted {
		if message == nil {
			continue
		}
		frames, err := ProjectKeyedMessage(conversationID, message)
		if err != nil {
			return nil, err
		}
		out = append(out, frames...)
	}
	return out, nil
}

// clearMessageDerivedFields 清掉「消息级派生帧」读的那几格,只留 Done 那一帧。
func clearMessageDerivedFields(m *transcript_entity.Message) {
	m.PromptTokens, m.CompletionTokens, m.CachedTokens = 0, 0, 0
	m.CacheCreationTokens, m.ReasoningTokens, m.TotalInputTokens = 0, 0, 0
	m.ErrorText = ""
}

// messageDerivedFrameCount 是被 clearMessageDerivedFields 清空后的消息尾部帧数。
func messageDerivedFrameCount(m *transcript_entity.Message) int {
	if m.Role == "assistant" {
		return 1 // Done
	}
	return 0
}

// FrameFingerprint 是一帧内容的指纹。编不出来时交回空串,调用方把它当作「与任何东西
// 都不同」—— 宁可多发一帧,也不要把一次真实的原地修补当成没变过。
func FrameFingerprint(frame wire.EventFrame) string {
	payload, err := json.Marshal(frame.Event)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

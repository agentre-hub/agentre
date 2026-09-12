package chat_entity

import "github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"

// MessageBlock 的存储实体、块拆分（SplitBlocksJSON/JoinBlocks）与 CheckpointBlocks 差分
// （DiffBlocks）已随 Message 一起搬进 transcript_entity（决策 8）。这里只转发两个仍被
// chat_svc 按旧名引用的常量，不再声明块拆分 / 差分本身 —— 那两份实现只在
// transcript_entity/message_block.go 出现一次，由守卫测试
// （transcript_repo.TestBlockSplittingAndCheckpointDiffingHaveOneImplementation）看住。
const (
	BlockCodecRaw          = transcript_entity.BlockCodecRaw
	BlockTypeSubagentState = transcript_entity.BlockTypeSubagentState
)

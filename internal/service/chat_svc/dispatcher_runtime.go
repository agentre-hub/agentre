package chat_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
)

// checkpointAssistantNew 中途 checkpoint(ToolResult 帧后调):把 acc.Snapshot
// 作为中途状态写回 DB(抗 abort,partial 状态可被刷新页面看到)。
func (s *chatSvc) checkpointAssistantNew(ctx context.Context, msg *chat_entity.Message, acc *turn.Accumulator) {
	if msg == nil || acc == nil {
		return
	}
	// SetBlocks 覆写前先留住上一次落库的那份正文 —— 它就是这次差分的基准。
	// 不这样做就只能整表替换,而 checkpoint 是每个 ToolResult 一次的高频调用:
	// 第 k 次重写当时已有的全部 k 个块,单条消息 O(N²)。实测用户库里一条最终
	// 1723 块 / 2 MB 的消息被 checkpoint 840 次,光 DELETE 侧就重写了 723,550 行 /
	// 910 MB,WAL 涨到 1.4 GB、无关的单行读被拖到几十秒。
	prevBlocksJSON := msg.BlocksJSON
	if err := msg.SetBlocks(acc.Snapshot()); err != nil {
		logger.Ctx(ctx).Warn("chat assistant checkpoint encode failed",
			zap.Int64("messageID", msg.ID),
			zap.Error(err))
		return
	}
	if err := transcript_repo.Message().CheckpointBlocks(context.WithoutCancel(ctx), msg, prevBlocksJSON); err != nil {
		logger.Ctx(ctx).Warn("chat assistant checkpoint persist failed",
			zap.Int64("messageID", msg.ID),
			zap.Error(err))
		return
	}
	// 块落了库才轮到取号:这是「分配与落库不可分」的那一刻(决策 3)。轮内 checkpoint
	// 只发已经定稿的那些帧 —— 结尾还会继续长的正文块与消息级派生帧留给收口那一发。
	s.publishPeerMessageFrames(context.WithoutCancel(ctx), msg.SessionID, msg, false)
}

package chat_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
)

// checkpointAssistantNew 中途 checkpoint(ToolResult 帧后调):把累加器的当前状态写回 DB
// (抗 abort,partial 状态可被刷新页面看到)。
//
// 只编码、只写自上一次 checkpoint 以来新增与被改动的块(acc.PrepareCheckpoint):
// checkpoint 是每个 ToolResult 一次的高频调用,整份重编码 + 整份差分让一轮成本 O(N²)。
// 实测用户库里一条最终 1723 块 / 2 MB 的消息被 checkpoint 840 次。msg.BlocksJSON 照旧
// 推进到整份当前正文(由缓存的逐块编码拼出,不重新编码):对端帧从它投影。
func (s *chatSvc) checkpointAssistantNew(ctx context.Context, msg *chat_entity.Message, acc *turn.Accumulator) {
	if msg == nil || acc == nil {
		return
	}
	cp, err := acc.PrepareCheckpoint(msg.BlocksJSON)
	if err != nil {
		logger.Ctx(ctx).Warn("chat assistant checkpoint encode failed",
			zap.Int64("messageID", msg.ID),
			zap.Error(err))
		return
	}
	msg.BlocksJSON = cp.BlocksJSON
	var rows []*transcript_entity.MessageBlock
	for _, c := range cp.Changed {
		rows = append(rows, transcript_entity.BlockRow(msg.ID, c.Idx, c.Block))
	}
	if err := transcript_repo.Message().UpsertBlocks(context.WithoutCancel(ctx), msg, rows, cp.TruncateFrom); err != nil {
		logger.Ctx(ctx).Warn("chat assistant checkpoint persist failed",
			zap.Int64("messageID", msg.ID),
			zap.Error(err))
		return
	}
	acc.CommitCheckpoint(cp)
	// 块落了库才轮到取号:这是「分配与落库不可分」的那一刻(决策 3)。轮内 checkpoint
	// 只发已经定稿的那些帧 —— 结尾还会继续长的正文块与消息级派生帧留给收口那一发。
	_, _ = s.publishPeerMessageFrames(context.WithoutCancel(ctx), msg.SessionID, msg, false)
}

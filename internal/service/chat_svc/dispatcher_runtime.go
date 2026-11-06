package chat_svc

import (
	"context"
	"errors"
	"fmt"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
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
	if err := upsertCheckpoint(context.WithoutCancel(ctx), msg, cp); err != nil {
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

// persistAssistantFinal 收尾那一发(轮末 / 中止):把 msg 的元数据与这一段的整份正文 final
// 落库,块行只写相对已落库版本变了的那些(新增、收尾时被就地改过的卡、长完的尾巴、追加的
// notice),不再整条删了重插。此前一块都没落过的消息照样逐块写全。
//
// msg.BlocksJSON 无论写库成败都推进到 final 的整份正文:随后的终态事件与对端帧带的是它,
// 界面行为与写库是否成功无关。写库失败时不提交,累加器下一次仍交出这些块。
//
// 某一块编码失败时块行一行不交(库里留着上一次成功 checkpoint 的块),元数据行照样写:
// 错误文本 / 用量 / 耗时不随编码失败一起丢掉。
func persistAssistantFinal(
	ctx context.Context, msg *chat_entity.Message, acc *turn.Accumulator, final []cagoblocks.ContentBlock,
) error {
	cp, err := prepareAssistantFinal(msg, acc, final)
	if err != nil {
		return errors.Join(fmt.Errorf("encode final blocks: %w", err),
			transcript_repo.Message().UpsertBlocks(ctx, msg, nil, -1))
	}
	if err := upsertCheckpoint(ctx, msg, cp); err != nil {
		return err
	}
	acc.CommitCheckpoint(cp)
	return nil
}

// prepareAssistantFinal 算出收尾那一发的写集合并把 msg.BlocksJSON 推进到 final 的整份正文。
// 写库与提交由调用方做:插话分段要把写库放进它自己的事务,事务提交之后才能提交写集合。
func prepareAssistantFinal(
	msg *chat_entity.Message, acc *turn.Accumulator, final []cagoblocks.ContentBlock,
) (*turn.Checkpoint, error) {
	cp, err := acc.PrepareFinalCheckpoint(msg.BlocksJSON, final)
	if err != nil {
		return nil, err
	}
	msg.BlocksJSON = cp.BlocksJSON
	return cp, nil
}

// upsertCheckpoint 把 msg 的元数据整行与 cp 里变了的块行写库(ctx 带着事务时写进那个事务)。
func upsertCheckpoint(ctx context.Context, msg *chat_entity.Message, cp *turn.Checkpoint) error {
	var rows []*transcript_entity.MessageBlock // 没有变化的块时为 nil:只写元数据行
	if len(cp.Changed) > 0 {
		rows = make([]*transcript_entity.MessageBlock, 0, len(cp.Changed))
	}
	for _, c := range cp.Changed {
		rows = append(rows, transcript_entity.BlockRow(msg.ID, c.Idx, c.Block))
	}
	return transcript_repo.Message().UpsertBlocks(ctx, msg, rows, cp.TruncateFrom)
}

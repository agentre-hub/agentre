package chat_repo

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
)

// insertBlocks 写入一条消息的全部块行。空块集合不产生任何语句。
func insertBlocks(ctx context.Context, messageID int64, blocksJSON string) error {
	rows, err := chat_entity.SplitBlocksJSON(messageID, blocksJSON)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	return db.Ctx(ctx).Create(&rows).Error
}

// replaceBlocks 用新正文整体替换一条消息的块行 —— 先清旧行再写新行,避免上一版
// 更长的正文在高位 idx 上留下残块。
func replaceBlocks(ctx context.Context, messageID int64, blocksJSON string) error {
	if err := db.Ctx(ctx).
		Exec("DELETE FROM `chat_message_blocks` WHERE message_id = ?", messageID).Error; err != nil {
		return err
	}
	return insertBlocks(ctx, messageID, blocksJSON)
}

// loadBlocksBatch 是一次 IN 查询里最多带几个 message id。SQLite 的绑定变量有上限
// (默认 32766),长会话按整段 id 展开会直接撞上它。
const loadBlocksBatch = 500

// loadBlocks 取回若干条消息的块行,按 message_id 归组。types 非空时只取这几类块 ——
// 派生视图要的就是「按类型点查」,筛选留在 SQL 里,整条转录不必读回内存再过一遍。
func loadBlocks(ctx context.Context, messageIDs []int64, types []string) (map[int64][]*chat_entity.MessageBlock, error) {
	if len(messageIDs) == 0 {
		return nil, nil
	}
	grouped := make(map[int64][]*chat_entity.MessageBlock, len(messageIDs))
	for start := 0; start < len(messageIDs); start += loadBlocksBatch {
		end := min(start+loadBlocksBatch, len(messageIDs))
		q := db.Ctx(ctx).Where("message_id IN ?", messageIDs[start:end])
		if len(types) > 0 {
			q = q.Where("`type` IN ?", types)
		}
		var rows []*chat_entity.MessageBlock
		if err := q.Order("message_id ASC, idx ASC").Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			grouped[row.MessageID] = append(grouped[row.MessageID], row)
		}
	}
	return grouped, nil
}

// fillBlocks 给一批消息填上它们的正文,使实体在内存里照旧携带块(决策 7)。
// types 非空时只填这几类块,其余块不进内存。
func fillBlocks(ctx context.Context, msgs []*chat_entity.Message, types []string) error {
	ids := make([]int64, 0, len(msgs))
	for _, m := range msgs {
		if m != nil && m.ID > 0 {
			ids = append(ids, m.ID)
		}
	}
	grouped, err := loadBlocks(ctx, ids, types)
	if err != nil {
		return err
	}
	for _, m := range msgs {
		if m == nil {
			continue
		}
		blocksJSON, err := chat_entity.JoinBlocks(grouped[m.ID])
		if err != nil {
			return err
		}
		m.BlocksJSON = blocksJSON
	}
	return nil
}

// deleteBlocksOfMessages 按宿主消息的筛选条件删块行。where 是作用在 chat_messages
// 上的条件片段,必须与随后删消息的条件完全一致,块行才不会变成孤儿。
func deleteBlocksOfMessages(ctx context.Context, where string, args ...any) error {
	return db.Ctx(ctx).Exec(
		"DELETE FROM `chat_message_blocks` WHERE message_id IN (SELECT id FROM `chat_messages` WHERE "+where+")",
		args...,
	).Error
}

// findSubagentStateBlock 按定位键点查本会话里的 subagent_state 块,取 message_id 最大的
// 一条(后台任务的发起消息)。无命中返回 (nil, nil)。
//
// WHERE 里那句 `tool_call_id` <> ” 不是多余的守卫,它是这条查询能走定位索引的**前提**:
// idx_chat_message_blocks_tool_call 是部分索引(... WHERE tool_call_id != ”),而 SQLite
// 只在能证明查询蕴含索引的 WHERE 子句时才肯用部分索引 —— `tool_call_id = ?` 里的绑定
// 变量证不出 `? != ”`。少了这一句,EXPLAIN QUERY PLAN 退回 `SCAN chat_message_blocks
// USING INDEX ux_chat_message_blocks_message_idx`(全表串扫)/ 按 type 扫遍全库的
// subagent_state 块,正是拆块表要消灭的那个形态。它不改变结果集:上面已经把
// toolCallID 为空的调用挡掉了。
func findSubagentStateBlock(ctx context.Context, sessionID int64, toolCallID string) (*chat_entity.MessageBlock, error) {
	if toolCallID == "" {
		return nil, nil
	}
	var row chat_entity.MessageBlock
	err := db.Ctx(ctx).
		Model(&chat_entity.MessageBlock{}).
		Joins("JOIN `chat_messages` ON `chat_messages`.`id` = `chat_message_blocks`.`message_id`").
		Where("`chat_messages`.`session_id` = ? AND `chat_message_blocks`.`type` = ?"+
			" AND `chat_message_blocks`.`tool_call_id` = ? AND `chat_message_blocks`.`tool_call_id` <> ''",
			sessionID, chat_entity.BlockTypeSubagentState, toolCallID).
		Order("`chat_message_blocks`.`message_id` DESC").
		Limit(1).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// updateBlockData 就地改写一个块行的正文(重新按阈值编码)。
func updateBlockData(ctx context.Context, row *chat_entity.MessageBlock, data []byte) error {
	codec, encoded := chat_entity.EncodeBlockData(data)
	return db.Ctx(ctx).Exec(
		"UPDATE `chat_message_blocks` SET `codec`=?,`data`=? WHERE message_id = ? AND idx = ?",
		codec, encoded, row.MessageID, row.Idx,
	).Error
}

// appendBlocks 把若干块追加到一条消息正文的末尾。
func appendBlocks(ctx context.Context, messageID int64, blocksJSON string) error {
	if blocksJSON == "" || blocksJSON == "[]" {
		return nil
	}
	rows, err := chat_entity.SplitBlocksJSON(messageID, blocksJSON)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	var next int
	if err := db.Ctx(ctx).
		Table("chat_message_blocks").
		Select("COALESCE(MAX(idx), -1) + 1").
		Where("message_id = ?", messageID).
		Row().Scan(&next); err != nil {
		return err
	}
	for i, row := range rows {
		row.Idx = next + i
	}
	return db.Ctx(ctx).Create(&rows).Error
}

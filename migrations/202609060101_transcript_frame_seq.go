package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609060101 建对端帧编号台账 chat_frame_seqs（决策 3）。
//
// 每条对话持有一个单调计数器：宿主每发布一个**持久帧**就取下一个号，并把这个号连同
// 「这一帧由哪条消息的哪个块的第几帧投影而来」一起落库。宿主重启后按台账重放，编号
// 与「第几个块」无关，也与进程存活无关 —— 对端的游标因此跨重启仍然有效。
//
// 为什么不是 chat_message_blocks 上的一列：一个块可以投影出不止一帧（user_ask 被回答
// 后是 request + resolved 两帧），而被**原地修补**的块每修一次都要取一个新的末尾号
// （subagent_state 的进度推进）。列放不下「一个块 → 多个号」，行可以：同一个
// (message_id, block_idx, ordinal) 允许有多行，seq 最大的那一行才是该位置当前内容的号，
// 被顶掉的旧号原样留着 —— 计数器的下一个值就是本会话 MAX(seq) + 1，它因此永不回退。
//
// block_idx = -1 表示消息级派生帧（UsageUpdate / ErrorEvent / Done），它们不属于任何块。
func migration202609060101() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609060101",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS chat_frame_seqs (
	session_id INTEGER NOT NULL,
	message_id INTEGER NOT NULL,
	block_idx INTEGER NOT NULL,
	ordinal INTEGER NOT NULL,
	seq INTEGER NOT NULL
)`).Error; err != nil {
				return err
			}
			for _, stmt := range []string{
				// 一条对话内 seq 唯一:两帧拿到同一个号会让对端的去重闸门吞掉其中一帧。
				`CREATE UNIQUE INDEX IF NOT EXISTS ux_chat_frame_seqs_session_seq ON chat_frame_seqs(session_id, seq)`,
				// 补齐时按会话整取台账,并按帧位置归并到最大的那个号。
				`CREATE INDEX IF NOT EXISTS idx_chat_frame_seqs_session_frame ON chat_frame_seqs(session_id, message_id, block_idx, ordinal)`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE IF EXISTS chat_frame_seqs`).Error
		},
	}
}

package migrations

import (
	"github.com/cago-frame/cago/pkg/logger"
	"github.com/go-gormigrate/gormigrate/v2"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
)

// migrationBlockSplitBatch 是一次事务里搬运的消息条数。分批提交是因为单事务实测把
// WAL 顶到 572 MB。
const migrationBlockSplitBatch = 500

// migration202608270002 把消息正文从 chat_messages.blocks_json 单列搬到一块一行的
// chat_message_blocks,并回收腾出的空间。
//
// 为什么拆表:blocks_json 让「定位一个块」只能对着整列做 LIKE 全扫,而块级改写要读出
// 并重写宿主消息的全部块(实测单行最大 12.9 MB)。拆成一块一行后定位键成了普通列,
// 块级操作退化成索引点查 + 单行更新。
//
// 形态(见 spec「块存储形态」):
//   - 普通 rowid 表 + UNIQUE(message_id, idx)。实测同一份数据 WITHOUT ROWID 版 1583 MB、
//     rowid 版 1203 MB —— 索引 B-tree 页的行内载荷上限远小于普通表,中等 blob 被过早溢出。
//   - tool_call_id 是定位键,空值不进索引(部分索引)。
//   - data 是块正文的编码字节,codec 标记编码方式,超过 4 KiB 时压缩(见 chat_entity)。
//
// 迁移是**前台阻塞 + 分批提交**的一次性搬迁:一次性、原子、可回滚,代码里不留任何双读
// 分支。实测 2.25 GB 真实库总耗时 72.8 秒,库体积 2.1 G → 1.2 G;绝大多数用户的库远小于
// 此,耗时为秒级。迁移期间不发起任何网络调用。
//
// 无法解析的历史 blocks_json 不静默丢弃:该消息保留元数据行,并留下带 message id 的
// 警告日志供排查。
func migration202608270002() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608270002",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS chat_message_blocks (
	message_id INTEGER NOT NULL,
	idx INTEGER NOT NULL,
	type TEXT NOT NULL DEFAULT '',
	tool_call_id TEXT NOT NULL DEFAULT '',
	codec INTEGER NOT NULL DEFAULT 0,
	data BLOB NOT NULL
)`).Error; err != nil {
				return err
			}
			if err := splitMessageBlocks(tx); err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS ux_chat_message_blocks_message_idx
ON chat_message_blocks(message_id, idx)`).Error; err != nil {
				return err
			}
			// 定位键索引带上 message_id:块级操作那条查询是
			// `tool_call_id=? AND type=?` + `ORDER BY message_id DESC LIMIT 1`
			// (chat_repo.findSubagentStateBlock)。少了第三列,这个索引虽然能等值命中
			// 却要额外排一次序,而下面那个 (type, message_id) 顺带满足了 ORDER BY ——
			// SQLite 的代价模型据此**改选后者**,于是「按定位键点查」退化成「扫遍全库
			// 的 subagent_state 块」。实测 16.2 万块的库上 200 次定位 1240ms → 4.9ms。
			//
			// 另一半在查询侧:部分索引要求调用方显式带上 tool_call_id <> '',否则
			// SQLite 证不出绑定变量满足索引的 WHERE 子句,索引根本不进候选集。两处
			// 缺一不可,见 findSubagentStateBlock 的注释与 bootstrap 的查询计划断言。
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_chat_message_blocks_tool_call
ON chat_message_blocks(tool_call_id, type, message_id) WHERE tool_call_id != ''`).Error; err != nil {
				return err
			}
			// (type, message_id) 服务「按类型取块」那一路(决策 6 / 改动清单一):派生
			// 视图要的是整条会话里某几类块,一条会话动辄几千块而其中只有几百块是它
			// 点名的那几类。少了这个索引,取数只能沿 (message_id, idx) 把这条会话的
			// 全部块行读出来再逐行丢掉,正是「后端点查」要替换掉的那个形态。
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_chat_message_blocks_type_message
ON chat_message_blocks(type, message_id)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`ALTER TABLE chat_messages DROP COLUMN blocks_json`).Error; err != nil {
				return err
			}
			// 开启增量式空间回收,使后续删除能真正把页归还磁盘;auto_vacuum 的切换要靠一次
			// VACUUM 才对既有库生效,这次 VACUUM 同时收走搬迁腾出的空间。
			if err := tx.Exec(`PRAGMA auto_vacuum = INCREMENTAL`).Error; err != nil {
				return err
			}
			return tx.Exec(`VACUUM`).Error
		},
	}
}

// splitMessageBlocks 把每条消息的 blocks_json 拆成块行,按 id 升序分批提交。
func splitMessageBlocks(tx *gorm.DB) error {
	type messageRow struct {
		ID         int64
		BlocksJSON string
	}
	lastID := int64(0)
	for {
		var rows []messageRow
		if err := tx.Raw(
			"SELECT id, blocks_json FROM chat_messages WHERE id > ? ORDER BY id ASC LIMIT ?",
			lastID, migrationBlockSplitBatch,
		).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		var blocks []*chat_entity.MessageBlock
		for _, row := range rows {
			lastID = row.ID
			split, err := chat_entity.SplitBlocksJSON(row.ID, row.BlocksJSON)
			if err != nil {
				logger.Default().Warn("migration202608270002: message blocks are not decodable; metadata kept, blocks dropped",
					zap.Int64("messageId", row.ID), zap.Error(err))
				continue
			}
			blocks = append(blocks, split...)
		}
		if len(blocks) == 0 {
			continue
		}
		if err := tx.Transaction(func(batchTx *gorm.DB) error {
			return batchTx.CreateInBatches(blocks, 200).Error
		}); err != nil {
			return err
		}
	}
}

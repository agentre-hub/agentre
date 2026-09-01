package migrations

import (
	"fmt"

	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
)

// conversationKeyBatch 是本迁移每一轮处理的行数上界。
//
// daemon_notification_journal 是只增不删的永久日志(见 migration202608080011 的注释:
// agentred 不再回收任何一行),体量没有上界,所以回填、去重与重建全部写成「一轮一批、
// 拿上一轮的游标继续」的形状:任何一条语句都不会把整张表读进内存或锁住一整轮流式写入。
const conversationKeyBatch = 2000

// migration202609010002 把两张表的身份键收缩到 conversation_id(规格「会话身份 /
// 身份键收缩为一列」与「迁移与回填 / agentred」):
//
//  1. 回填 —— 按决策 2 为每一行算出确定性的 conversation_id。线上早已只传
//     conversation_id(本轮的破坏性替换),因此 peer_session_id 里可能已经是一个规范
//     uuid:那就原样采用;还是旧的对端本地会话 id 时按
//     UUIDv5(NS, peer_fingerprint + "\0" + peer_session_id) 派生 —— 与桌面端、server
//     对同一条对话算出的值逐位相同,镜像存量因此不会成孤儿。
//  2. 去重 —— 收缩身份键会让「两个对端各自镜像过同一条对话」的两行撞成一行,必须先
//     显式收敛,否则重建时撞主键、整个迁移回滚,库永远升不上来。
//  3. 重建 —— SQLite 改不了主键,只能建新表、分批搬行、换名。daemon_sessions 的主键
//     变成 (conversation_id),daemon_notification_journal 变成 (conversation_id, seq);
//     peer_fingerprint 退出主键、留作来源标注与授权的普通列(session_repo 的读路径仍按
//     它收窄),对端本地那一格 peer_session_id 随之消失。
//
// **迁移体自己包一层 tx.Transaction**:gormigrate.DefaultOptions.UseTransaction 为
// false(见 RunMigrations),交进来的 tx 就是裸库句柄,begin/commit 都是 no-op —— 这样
// 一段多语句重建中途失败会留下一个半迁移的库,而台账里连一行都没有。包上之后要么整批
// 生效,要么整批回滚。
//
// **可重入**:台账行没写成而事务已提交时,整个迁移会再跑一遍 —— 每一步的待办判据都是
// 库里的现状(还没回填的行 / 还撞在一起的行 / 还在不在旧结构上),因此重跑是空转。
func migration202609010002() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609010002",
		Migrate: func(tx *gorm.DB) error {
			return tx.Transaction(func(tx *gorm.DB) error {
				for _, table := range []string{"daemon_sessions", "daemon_notification_journal"} {
					// 已经重建过的表没有 peer_session_id,回填的输入也就不在了。
					if !tx.Migrator().HasColumn(table, "peer_session_id") {
						continue
					}
					if err := backfillConversationIDs(tx, table); err != nil {
						return err
					}
				}
				if err := rebuildDaemonSessions(tx); err != nil {
					return err
				}
				return rebuildNotificationJournal(tx)
			})
		},
		// 不提供 Rollback:回滚要把 conversation_id 拆回 (peer_fingerprint,
		// peer_session_id),而派生是单向的 —— 旧的对端本地会话 id 已经不在库里,算不
		// 回来。降级的路径是从备份恢复,不是反向迁移。
	}
}

// backfillConversationIDs 分批把 conversation_id 填进 table 里还空着的行。
//
// 空串是唯一的待办判据(migration202609010001 把列建成 NOT NULL DEFAULT ”),所以它
// 可以分批、可以重跑:填过的行不会被再碰一次,同一行重算也永远得到同一个值。
func backfillConversationIDs(tx *gorm.DB, table string) error {
	for {
		var pending []struct {
			PeerFingerprint string `gorm:"column:peer_fingerprint"`
			PeerSessionID   string `gorm:"column:peer_session_id"`
		}
		if err := tx.Raw(
			"SELECT DISTINCT peer_fingerprint, peer_session_id FROM "+table+
				" WHERE conversation_id = '' LIMIT ?", conversationKeyBatch,
		).Scan(&pending).Error; err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}
		var advanced int64
		for _, row := range pending {
			id := row.PeerSessionID
			if conversationid.Validate(id) != nil {
				id = conversationid.Derive(conversationid.Namespace, row.PeerFingerprint, row.PeerSessionID)
			}
			res := tx.Exec(
				"UPDATE "+table+" SET conversation_id = ?"+
					" WHERE conversation_id = '' AND peer_fingerprint = ? AND peer_session_id = ?",
				id, row.PeerFingerprint, row.PeerSessionID)
			if res.Error != nil {
				return res.Error
			}
			advanced += res.RowsAffected
		}
		// 一轮下来一行都没推进说明待办判据与写入条件已经对不上了,再循环就是死循环。
		if advanced == 0 {
			return fmt.Errorf("migrations: conversation_id backfill made no progress on %s", table)
		}
	}
}

// rebuildDaemonSessions 把 daemon_sessions 换到主键 (conversation_id) 上。
func rebuildDaemonSessions(tx *gorm.DB) error {
	if !tx.Migrator().HasColumn("daemon_sessions", "peer_session_id") {
		return nil // 已经是新结构(台账行没写成时会重跑到这里)。
	}
	if err := collapseDuplicateSessions(tx); err != nil {
		return err
	}
	if err := tx.Exec(`DROP TABLE IF EXISTS daemon_sessions_rebuild`).Error; err != nil {
		return err
	}
	if err := tx.Exec(`CREATE TABLE daemon_sessions_rebuild (
	conversation_id TEXT NOT NULL,
	peer_fingerprint TEXT NOT NULL,
	agent_id INTEGER NOT NULL DEFAULT 0,
	cwd TEXT NOT NULL DEFAULT '',
	backend_type TEXT NOT NULL DEFAULT '',
	lifecycle_state TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL DEFAULT '',
	agent_sync_id TEXT NOT NULL DEFAULT '',
	provider_session_id TEXT NOT NULL DEFAULT '',
	provider_key TEXT NOT NULL DEFAULT '',
	model_key TEXT NOT NULL DEFAULT '',
	project_sync_id TEXT NOT NULL DEFAULT '',
	createtime INTEGER NOT NULL DEFAULT 0,
	last_message_at INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (conversation_id)
)`).Error; err != nil {
		return err
	}
	const columns = `conversation_id, peer_fingerprint, agent_id, cwd, backend_type, lifecycle_state,
	title, agent_sync_id, provider_session_id, provider_key, model_key, project_sync_id,
	createtime, last_message_at`
	if err := copyInBatches(tx, "daemon_sessions", "daemon_sessions_rebuild", columns); err != nil {
		return err
	}
	if err := tx.Exec(`DROP TABLE daemon_sessions`).Error; err != nil {
		return err
	}
	if err := tx.Exec(`ALTER TABLE daemon_sessions_rebuild RENAME TO daemon_sessions`).Error; err != nil {
		return err
	}
	// 旧主键 (peer_fingerprint, peer_session_id) 顺带服务着「列出某个对端的会话」;
	// 新主键的最左列不再是它,补一条索引把那条查询接回去(ListByPeer 按 last_message_at
	// 倒序取)。
	return tx.Exec(`CREATE INDEX IF NOT EXISTS idx_daemon_sessions_peer_fingerprint
	ON daemon_sessions (peer_fingerprint, last_message_at)`).Error
}

// collapseDuplicateSessions 把落在同一个 conversation_id 上的多行会话收敛成一行,
// 保留最近活动的那一行(last_message_at 最大,同值时取后写入的 rowid)。
//
// 会出现多行是身份键收缩的直接后果:同一条对话被两个对端(比如桌面端与浏览器)分别
// 镜像过时,旧键把它们记成两条,新键说它们本来就是同一条。
func collapseDuplicateSessions(tx *gorm.DB) error {
	for {
		var duplicated []string
		if err := tx.Raw(
			"SELECT conversation_id FROM daemon_sessions"+
				" GROUP BY conversation_id HAVING COUNT(*) > 1 LIMIT ?",
			conversationKeyBatch).Scan(&duplicated).Error; err != nil {
			return err
		}
		if len(duplicated) == 0 {
			return nil
		}
		var advanced int64
		for _, conversationID := range duplicated {
			res := tx.Exec(
				"DELETE FROM daemon_sessions WHERE conversation_id = ? AND rowid NOT IN"+
					" (SELECT rowid FROM daemon_sessions WHERE conversation_id = ?"+
					"  ORDER BY last_message_at DESC, rowid DESC LIMIT 1)",
				conversationID, conversationID)
			if res.Error != nil {
				return res.Error
			}
			advanced += res.RowsAffected
		}
		if advanced == 0 {
			return fmt.Errorf("migrations: duplicate daemon_sessions rows made no progress")
		}
	}
}

// rebuildNotificationJournal 把 daemon_notification_journal 换到主键
// (conversation_id, seq) 上。这是本轮体量最大的一次重建 —— 搬行分批做。
func rebuildNotificationJournal(tx *gorm.DB) error {
	if !tx.Migrator().HasColumn("daemon_notification_journal", "peer_session_id") {
		return nil
	}
	if err := renumberCollidingJournalSeqs(tx); err != nil {
		return err
	}
	if err := tx.Exec(`DROP TABLE IF EXISTS daemon_notification_journal_rebuild`).Error; err != nil {
		return err
	}
	if err := tx.Exec(`CREATE TABLE daemon_notification_journal_rebuild (
	conversation_id TEXT NOT NULL,
	seq INTEGER NOT NULL,
	peer_fingerprint TEXT NOT NULL,
	payload BLOB NOT NULL,
	createtime INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (conversation_id, seq)
)`).Error; err != nil {
		return err
	}
	if err := copyInBatches(tx, "daemon_notification_journal", "daemon_notification_journal_rebuild",
		"conversation_id, seq, peer_fingerprint, payload, createtime"); err != nil {
		return err
	}
	if err := tx.Exec(`DROP TABLE daemon_notification_journal`).Error; err != nil {
		return err
	}
	return tx.Exec(
		`ALTER TABLE daemon_notification_journal_rebuild RENAME TO daemon_notification_journal`).Error
}

// renumberCollidingJournalSeqs 化解「同一条对话的同一个 seq 上有多行」。
//
// seq 是每个对端各自从 1 开始分配的(旧键把对端也算进身份),所以同一条对话被两个对端
// 驱动过时,两串 seq 会原样撞在新主键上。收敛办法是保留最早开始写的那个对端的 seq
// 原样不动 —— 它才是客户端手上游标指的那一串 —— 其余对端整体上移到当前最大 seq 之后,
// 各自的先后次序不变。改动只落在真的撞了的那些对话上,其他对话的游标一格不动。
//
// 待办判据是「还存在重复的 (conversation_id, seq)」,化解完就不再命中,因此可重跑。
func renumberCollidingJournalSeqs(tx *gorm.DB) error {
	for {
		var collided []string
		if err := tx.Raw(
			"SELECT DISTINCT conversation_id FROM daemon_notification_journal"+
				" WHERE conversation_id IN (SELECT conversation_id FROM daemon_notification_journal"+
				"  GROUP BY conversation_id, seq HAVING COUNT(*) > 1) LIMIT ?",
			conversationKeyBatch).Scan(&collided).Error; err != nil {
			return err
		}
		if len(collided) == 0 {
			return nil
		}
		var advanced int64
		for _, conversationID := range collided {
			moved, err := renumberConversation(tx, conversationID)
			if err != nil {
				return err
			}
			advanced += moved
		}
		if advanced == 0 {
			return fmt.Errorf("migrations: journal seq renumbering made no progress")
		}
	}
}

// renumberConversation 把这条对话上「第一串之外」的每一串 seq 整体上移到当前最大
// seq 之后,返回搬动的行数。
//
// 分串的依据是**旧身份** (peer_fingerprint, peer_session_id) 而不是对端一个:seq 是
// 按旧主键各自从 1 开始分配的,所以「一串」就是旧身份的一组。只按对端分会漏掉同一个
// 对端在同一条对话上留下两串的情形 —— 而它恰恰是常态:回填对已经是规范 uuid 的
// peer_session_id 原样采用、对旧的数字 id 按 UUIDv5 派生,同一个对端跨过线格式那次
// 破坏性替换之后,两串就都落在同一个 conversation_id 上。那时 peers 只有一个,旧实现
// 返回 (0, nil),调用方看到「一轮没有推进」直接把整个迁移判死 —— 而且重跑必然复现,
// agentred 从此起不来。
func renumberConversation(tx *gorm.DB, conversationID string) (int64, error) {
	type block struct {
		PeerFingerprint string `gorm:"column:peer_fingerprint"`
		PeerSessionID   string `gorm:"column:peer_session_id"`
	}
	var blocks []block
	// 按「谁先开始写」排序:最早的那一串 seq 保持原值 —— 它才是客户端手上游标指的
	// 那一串 —— 后来者依次让路。
	if err := tx.Raw(
		"SELECT peer_fingerprint, peer_session_id FROM daemon_notification_journal"+
			" WHERE conversation_id = ? GROUP BY peer_fingerprint, peer_session_id"+
			" ORDER BY MIN(createtime), MIN(seq), peer_fingerprint, peer_session_id",
		conversationID).Scan(&blocks).Error; err != nil {
		return 0, err
	}
	if len(blocks) < 2 {
		// 一条对话只剩一串却还在撞,说明同一个旧身份里就有重复的 seq —— 旧主键本来
		// 不允许,库已经坏了。如实报错,别让调用方把它当成「没进展」的死循环。
		return 0, fmt.Errorf(
			"migrations: conversation %q has duplicate seq within a single legacy identity", conversationID)
	}
	var moved int64
	for _, b := range blocks[1:] {
		var offset int64
		if err := tx.Raw(
			"SELECT COALESCE(MAX(seq), 0) FROM daemon_notification_journal WHERE conversation_id = ?",
			conversationID).Scan(&offset).Error; err != nil {
			return moved, err
		}
		// seq 恒 >= 1,加上当前最大值后必然大于它,所以搬过去既不撞已有行,也不改变
		// 这一串自己的先后。
		res := tx.Exec(
			"UPDATE daemon_notification_journal SET seq = seq + ?"+
				" WHERE conversation_id = ? AND peer_fingerprint = ? AND peer_session_id = ?",
			offset, conversationID, b.PeerFingerprint, b.PeerSessionID)
		if res.Error != nil {
			return moved, res.Error
		}
		moved += res.RowsAffected
	}
	return moved, nil
}

// copyInBatches 按 rowid 递增分批把 src 的行搬进 dst,每批至多 conversationKeyBatch 行。
//
// 分批不是优化:这两张表(尤其是通知日志)没有体量上界,一条 INSERT ... SELECT 全表会
// 把整张表的行搬进一条语句里,内存与锁持有时间都随库大小无界增长。
func copyInBatches(tx *gorm.DB, src, dst, columns string) error {
	var cursor int64
	for {
		// rowid 恒 >= 1,所以 0 表示「这一批一行都没有」。
		var upto int64
		if err := tx.Raw(
			"SELECT COALESCE(MAX(rowid), 0) FROM"+
				" (SELECT rowid FROM "+src+" WHERE rowid > ? ORDER BY rowid LIMIT ?)",
			cursor, conversationKeyBatch).Scan(&upto).Error; err != nil {
			return err
		}
		if upto == 0 {
			return nil
		}
		if err := tx.Exec(
			"INSERT INTO "+dst+" ("+columns+") SELECT "+columns+
				" FROM "+src+" WHERE rowid > ? AND rowid <= ?", cursor, upto).Error; err != nil {
			return err
		}
		cursor = upto
	}
}

package migrations

import (
	"fmt"
	"strconv"

	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
)

// backfillBatchSize 是一次取多少行来回填。回填写成分批 + 可重入的形态:每一批只
// 挑还没有身份的行,写完它们就不再落进下一批的取材,所以中途失败重跑不会重复做功,
// 也不会把一整张表的主键读进内存。
const backfillBatchSize = 500

// migration202608080015 回填存量对话的 conversation_id,并在那一列上建唯一索引。
//
// 回填按 spec 2026-08-31 决策 2 确定性派生:
// UUIDv5(NS_AGENTRE_CONVERSATION, peer_fingerprint + "\0" + peer_session_id)。
// 桌面端与 server 各持一份存量、迁移时互不通信,只有确定性派生才能让两边独立算出
// 同一个值 —— 随机铸 v7 会让同一条对话在两侧有两个身份,镜像存量全体成孤儿。
//
// 第一个输入是**这条对话的发起端指纹**:对桌面端而言就是 keychain
// `agentre-device-fingerprint`(R5 决策 8:账号侧不得另生成指纹),也就是这台桌面端
// 向对端出示、并被 server 存进 peer_fingerprint 的那一个值。它**不是**本机 agentred
// 的 identity.DaemonFingerprint —— 两者是不同类的值,取错则两边算出不同的 uuid。
// 该值由 bootstrap 经 RunMigrations 交进来:migrations 包不认识 keychain,也不该认识。
//
// 第二个输入是本行的 id,即 server 侧存的 peer_session_id。
//
// gormigrate 在这个库上**不开事务**(DefaultOptions.UseTransaction 为 false,
// begin() 只是 g.tx = g.db,commit/rollback 是 no-op),所以迁移体自己包一层
// tx.Transaction:回填与建索引中途失败必须整体回滚,否则会留下一半有身份、一半
// 没有的表,而 migrations 账本里又没有这一行。这里不改全局选项 —— 改它会改变
// 所有既有迁移的行为。
func migration202608080015(deviceFingerprint DeviceFingerprintFunc) *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608080015",
		Migrate: func(tx *gorm.DB) error {
			return tx.Transaction(func(tx *gorm.DB) error {
				if err := backfillConversationIDs(tx, deviceFingerprint); err != nil {
					return err
				}
				// 唯一索引在回填之后才建:回填前每一行都是空串,建索引必然撞车。
				return tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS ux_chat_sessions_conversation_id
ON chat_sessions(conversation_id)`).Error
			})
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP INDEX IF EXISTS ux_chat_sessions_conversation_id`).Error
		},
	}
}

// backfillConversationIDs 给每一条还没有身份的会话行算出并写入它的 conversation_id。
//
// 取材不带任何 status / purpose 过滤:软删的会话与子 agent 委派会话同样占着一行,
// 漏掉它们就会让接下来的唯一索引在一堆空串上撞车。
//
// 指纹**惰性取**:全新安装的库里一行都没有,那里不该为了跑一次迁移就去碰 keychain
// (在生产 system keychain 上那是一次可见的访问,还会顺手铸一个此刻没人要的指纹)。
func backfillConversationIDs(tx *gorm.DB, deviceFingerprint DeviceFingerprintFunc) error {
	fingerprint := ""
	for {
		var ids []int64
		if err := tx.Raw(
			`SELECT id FROM chat_sessions WHERE conversation_id = '' ORDER BY id LIMIT ?`,
			backfillBatchSize).Scan(&ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if fingerprint == "" {
			if deviceFingerprint == nil {
				return fmt.Errorf("migration 202608080015: %d rows need a conversation id but no device fingerprint source was provided", len(ids))
			}
			fp, err := deviceFingerprint()
			if err != nil {
				return fmt.Errorf("migration 202608080015: resolve device fingerprint: %w", err)
			}
			if fp == "" {
				// 空指纹会让这批行算出与 server 那边对不上的 uuid,而且是**静默**
				// 对不上。宁可迁移失败:回滚后重来一次仍是同一批行、同一个结果。
				return fmt.Errorf("migration 202608080015: device fingerprint is empty; backfilled conversation ids would not match the server's")
			}
			fingerprint = fp
		}
		for _, id := range ids {
			cid := conversationid.Derive(conversationid.Namespace, fingerprint, strconv.FormatInt(id, 10))
			// WHERE 上再挂一次 conversation_id = '' :派生是幂等的,这一条只是让
			// 重跑时不去碰已经有身份的行。
			if err := tx.Exec(
				`UPDATE chat_sessions SET conversation_id = ? WHERE id = ? AND conversation_id = ''`,
				cid, id).Error; err != nil {
				return err
			}
		}
	}
}

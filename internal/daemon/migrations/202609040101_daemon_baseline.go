package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609040101 是 agentred 侧持久化的基线迁移:一次建出会话表的最终形态,
// 只建终态、不带任何回填与重建路径。
//
// daemon_sessions —— 会话表。行身份是自增的 id：库里每一张表的行身份都是一个与业务取值
// 无关的数字（同一条约定见 desktop 的 internal/bootstrap 与 entity 的
// TestEveryEntityHasAutoIncrementIDPrimaryKey）。对话身份全局唯一，落在
// ux_daemon_sessions_conversation_id 这个唯一索引上 —— session_repo.Upsert 的
// ON CONFLICT (conversation_id) 认的就是它。
// peer_fingerprint 是普通列，做来源标注与授权收窄用（session_repo 的读路径按它收窄），
// 并单独建索引把「列出某个对端的会话、按最近活动倒序」那条查询接住。agent_id 是对端
// （桌面端）本地的数字 agent 主键，原样透传保存。不含「等待输入」列 —— 那是 running
// 之上的实时叠加，不落库（R11）。
// 「某会话最新的 seq」以转录表自己的 MAX(seq) 为唯一真相源，不在会话表重复维护游标。
// provider_key / model_key / reasoning_effort 是会话级覆盖的镜像，只供显示，执行路径
// 不读它们；取值词表由发起端把关，不在 DDL 上加 CHECK —— 档位表会随后端能力演进，写死
// 在表结构里改一次要重写整张表。
func migration202609040101() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609040101",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS daemon_sessions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
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
	reasoning_effort TEXT NOT NULL DEFAULT '',
	project_sync_id TEXT NOT NULL DEFAULT '',
	createtime INTEGER NOT NULL DEFAULT 0,
	last_message_at INTEGER NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			for _, stmt := range []string{
				// 冲突目标必须是库上真实存在的 UNIQUE 约束：session_repo.Upsert 的
				// ON CONFLICT (conversation_id) 认的就是它。
				`CREATE UNIQUE INDEX IF NOT EXISTS ux_daemon_sessions_conversation_id
	ON daemon_sessions (conversation_id)`,
				`CREATE INDEX IF NOT EXISTS idx_daemon_sessions_peer_fingerprint
	ON daemon_sessions (peer_fingerprint, last_message_at)`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE IF EXISTS daemon_sessions`).Error
		},
	}
}

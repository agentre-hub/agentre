package migrations

import (
	"time"

	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608080013 建 sync_accounts 表，并把同步组 sync_account_id 这一列的
// **含义**从「server 的 user_id」改成「本机为 (server 地址, 远端用户主键) 分配的
// 代理键」。
//
// 为什么要改：server 的 user_id 是它自己库里的自增主键，两套自建部署的第一个用户
// 都是 1。归属判定（行属于谁、队列属于谁、游标属于谁）全落在这一个整数上，于是换
// 一套 server 之后本机把 B 的 1 号用户认成 A 的 1 号用户——上一个账号的行照常上行
// 到新 server 里去，而 R13a 说的正是这些行不该参与同步。
//
// **本迁移不改写任何一行数据**，靠两步把存量接上：
//
//  1. 把当前登录的那个账号播种成一行，并**指定主键 = 现有的 server_user_id**。
//     本机所有存量行盖的就是这个数，因此它们的归属一个字都不用改，迁移前后
//     完全等价。
//  2. 把自增起点抬到本机出现过的最大 sync_account_id 之上。少了这一步，日后新
//     分配的键可能撞上更早的某个账号留在行上的旧值——那会把上一个账号的行凭空
//     并进一个毫不相干的新账号。
//
// 之后新增的 (server, 用户) 对由 sync_account_repo.EnsureKey 按需分配，与旧值永不
// 相交。server 侧对这张表一无所知，这是纯本地的一层身份。
func migration202608080013() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608080013",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS sync_accounts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	server_url TEXT NOT NULL DEFAULT '',
	remote_user_id BIGINT NOT NULL DEFAULT 0,
	createtime INTEGER NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_sync_accounts_pair
	ON sync_accounts (server_url, remote_user_id)`).Error; err != nil {
				return err
			}
			// 播种当前账号：主键取现有的 server_user_id，存量行因此原样继续匹配。
			// server_url 为空时不播种——那一对拿不出可查的地址，播了也永远命不中。
			if err := tx.Exec(`INSERT OR IGNORE INTO sync_accounts (id, server_url, remote_user_id, createtime)
	SELECT server_user_id, server_url, server_user_id, ?
	FROM server_state
	WHERE id = 1 AND server_user_id != 0 AND server_url != ''`,
				time.Now().UnixMilli()).Error; err != nil {
				return err
			}
			return raiseSyncAccountFloor(tx)
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE IF EXISTS sync_accounts`).Error
		},
	}
}

// raiseSyncAccountFloor 把 sync_accounts 的自增起点抬到「本机任何一张表上出现过的
// 最大 sync_account_id」之上。
//
// 表名不写死：扫 sqlite_master 找出此刻真的带 sync_account_id 这一列的表。写死一份
// 名单的话，名单与建表迁移一旦漂移，漏掉的那张表上的旧账号值就会在日后被重新分配
// 出去，而这种错没有任何征兆。
func raiseSyncAccountFloor(tx *gorm.DB) error {
	var tables []string
	if err := tx.Raw(`SELECT m.name FROM sqlite_master m
	JOIN pragma_table_info(m.name) p
	WHERE m.type = 'table' AND p.name = 'sync_account_id'`).Scan(&tables).Error; err != nil {
		return err
	}

	floor := int64(0)
	for _, table := range tables {
		var maxID int64
		// 表名来自 sqlite_master 而不是调用方，且经 GORM 的 Table() 转义。
		if err := tx.Table(table).
			Select("COALESCE(MAX(sync_account_id), 0)").Scan(&maxID).Error; err != nil {
			return err
		}
		if maxID > floor {
			floor = maxID
		}
	}
	if floor == 0 {
		// 本机从没有过任何账号归属：自增照常从 1 开始。
		return nil
	}

	// sqlite_sequence 的 name 上**没有唯一索引**（它是 SQLite 的内部表，不带约束），
	// 所以 INSERT OR REPLACE 不会替换、只会再插一行，自增起点根本抬不上去。必须先
	// 看那一行在不在：播过种就 UPDATE，没播过（本机此刻没有登录账号，但更早的账号
	// 在行上留了值）就 INSERT。
	var seeded int64
	if err := tx.Raw(`SELECT COUNT(*) FROM sqlite_sequence WHERE name = 'sync_accounts'`).
		Scan(&seeded).Error; err != nil {
		return err
	}
	if seeded == 0 {
		return tx.Exec(`INSERT INTO sqlite_sequence (name, seq) VALUES ('sync_accounts', ?)`,
			floor).Error
	}
	return tx.Exec(`UPDATE sqlite_sequence SET seq = ? WHERE name = 'sync_accounts' AND seq < ?`,
		floor, floor).Error
}

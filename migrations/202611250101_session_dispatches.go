package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202611250101 建 session_dispatches 表(spec 2026-11-24 决策 21):记录「会话 A
// 把任务派给会话 B」——agrctl send(带不带 --wait、隔离与否)与 agent 派发。自本迁移起记录,
// 不回填历史。
//   - method           agrctl_send / agent_call
//   - status           queued / running / completed / failed / canceled(终态词表同子代理卡)
//   - reply_message_id 被派发方终轮最后一条 assistant 消息,隔离子会话的「最后回复」据它读
//   - finishtime       0 = 未进入终态
//
// 两个方向各一个索引:查「我派发的」按 dispatcher,查「派发我的」按 dispatched。
// 纯新增表,不碰已有数据。
func migration202611250101() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202611250101",
		Migrate: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`CREATE TABLE IF NOT EXISTS session_dispatches (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	dispatcher_session_id INTEGER NOT NULL,
	dispatched_session_id INTEGER NOT NULL,
	method TEXT NOT NULL,
	task TEXT NOT NULL DEFAULT '',
	isolated INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'queued',
	reply_message_id INTEGER NOT NULL DEFAULT 0,
	createtime BIGINT NOT NULL DEFAULT 0,
	finishtime BIGINT NOT NULL DEFAULT 0
)`,
				`CREATE INDEX IF NOT EXISTS ix_session_dispatches_dispatcher ON session_dispatches(dispatcher_session_id)`,
				`CREATE INDEX IF NOT EXISTS ix_session_dispatches_dispatched ON session_dispatches(dispatched_session_id)`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}

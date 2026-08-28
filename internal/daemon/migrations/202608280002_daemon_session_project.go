package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608280002 给 daemon_sessions 补上 project_sync_id:这条会话所属项目的
// 账号级同步标识,由发起方在起手时携带、每轮幂等覆盖(与 title / agent_sync_id 同批)。
//
// 在此之前 agentred 只存 cwd,项目归属由服务端按 (指纹, cwd) 反推(agent_sessions
// 决策 12)。日活跃统计按项目分组,而它走的是一条**不上行任何路径**的纯计数通道,
// 反推那条路在那里用不了 —— 项目必须在发起那一刻记下来。
//
// 老会话补空串:空 = 发起方没报,不是「未知待推导」。
func migration202608280002() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608280002",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec(`ALTER TABLE daemon_sessions ADD COLUMN project_sync_id TEXT NOT NULL DEFAULT ''`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			// SQLite 不支持 DROP COLUMN,回滚只把值清回空串;列结构保留。
			return tx.Exec(`UPDATE daemon_sessions SET project_sync_id = ''`).Error
		},
	}
}

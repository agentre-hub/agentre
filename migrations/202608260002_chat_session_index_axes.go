package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608260002 为会话索引页的三根分组轴补索引。
//
// chat_sessions 此前只有 idx_chat_sessions_agent_status_last(agent_id, status,
// last_message_at) 一个索引，前导列是 agent_id。会话索引页的三根轴里只有「按 agent」
// 用得上它，另外两根（按项目 / 按执行设备）以及不分组的「最近」轴都落不到索引上：
//
//	listIndexPaged  WHERE status=? [AND project_id=? | AND exec_device_id=?]
//	                ORDER BY last_message_at DESC, id DESC
//
// 实测（本机 3331 行真实数据，EXPLAIN QUERY PLAN）：
//
//	按最近   SCAN ... USING INDEX idx_chat_sessions_agent_status_last + USE TEMP B-TREE FOR ORDER BY
//	按项目   SCAN chat_sessions                                       + USE TEMP B-TREE FOR ORDER BY
//	按设备   SCAN chat_sessions                                       + USE TEMP B-TREE FOR ORDER BY
//	CountAll SCAN chat_sessions
//
// 补上这三个索引后全部变成 SEARCH ... USING INDEX，且**排序整个消失**（不再建临时
// B 树）——排序键与索引尾部逐列同序是关键，所以 last_message_at / id 两列都按 DESC
// 建，与 ORDER BY 完全对齐。
//
// purpose <> 'subagent' 是不等值条件，进不了索引前缀，留作取到行之后的过滤；它筛掉
// 的是少数子 agent 会话，不影响上面的定位与排序。
//
// 侧写：这三根轴每次侧边栏渲染、每次索引页切 tab 都要跑，而排序此前每次都要重新
// 物化一遍。当前行数下单次是个位数毫秒，但代价随会话数线性增长，而会话只增不减。
//
// 纯结构迁移（CREATE INDEX IF NOT EXISTS），不回填、不重写任何行。
func migration202608260002() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608260002",
		Migrate: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`CREATE INDEX IF NOT EXISTS idx_chat_sessions_status_last
ON chat_sessions(status, last_message_at DESC, id DESC)`,
				`CREATE INDEX IF NOT EXISTS idx_chat_sessions_project_status_last
ON chat_sessions(project_id, status, last_message_at DESC, id DESC)`,
				`CREATE INDEX IF NOT EXISTS idx_chat_sessions_device_status_last
ON chat_sessions(exec_device_id, status, last_message_at DESC, id DESC)`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}

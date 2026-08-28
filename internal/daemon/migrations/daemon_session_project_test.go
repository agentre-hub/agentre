package migrations_test

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/daemon/migrations"
)

// TestMigrations_DaemonSessionsCarriesProjectSyncID 守「项目在会话发起时就落库」这
// 一列真的建出来了。
//
// 少了它,session_repo.Upsert 的赋值列会在运行时炸 no such column —— 而单元测试用
// 的是 sqlmock,不碰真库,炸不出来。这条测试跑真 SQLite,是那道缺口的唯一守卫。
func TestMigrations_DaemonSessionsCarriesProjectSyncID(t *testing.T) {
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, migrations.RunMigrations(gormDB))

	var columns []struct {
		Name string `gorm:"column:name"`
	}
	require.NoError(t, gormDB.Raw(`SELECT name FROM pragma_table_info('daemon_sessions')`).Scan(&columns).Error)

	names := make([]string, 0, len(columns))
	for _, c := range columns {
		names = append(names, c.Name)
	}
	assert.Contains(t, names, "project_sync_id")
}

// Package migrations 汇总并执行 Agentre 桌面端 SQLite 数据库的全部迁移。
//
// 规范：
//   - 文件名前缀 = 时间戳排序键（YYYYMMDDNNNN），调用顺序按时间升序。
//   - 每个迁移返回一个 *gormigrate.Migration，包含 Migrate 与可选的 Rollback。
//   - 一次迁移只做一件事；新增表、加列、加索引各自独立成文件，方便回滚和 git bisect。
//   - DDL 优先使用原生 SQL，避免依赖 GORM AutoMigrate 的隐式行为。
package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// DeviceFingerprintFunc 交出本机设备指纹 —— 存量对话回填 conversation_id 的第一个
// 输入(spec 2026-08-31 决策 2)。
//
// 做成惰性取值而不是直接收一个字符串,是因为**取值这件事本身有代价**:桌面端那个
// 指纹住在 OS keychain 里,而它的注入方(InitRemoteDevice)按装配顺序排在迁移之后。
// 交一个函数进来,迁移就能只在「确实有行要回填」时才去取它 —— 全新安装的库一行
// 都没有,那里一次 keychain 都不该碰。migrations 包因此也不必认识 keychain。
type DeviceFingerprintFunc func() (string, error)

// RunMigrations 执行全部迁移。新增迁移时把构造函数追加到 migrationList 末尾。
//
// deviceFingerprint 只被需要它的那些迁移使用(见 202608080015);传 nil 时,那些
// 迁移在真的有行要回填时会失败 —— 而不是拿一个空指纹算出一批对不上的 uuid。
func RunMigrations(db *gorm.DB, deviceFingerprint DeviceFingerprintFunc) error {
	m := gormigrate.New(db, gormigrate.DefaultOptions, migrationList(deviceFingerprint))
	return m.Migrate()
}

// migrationList 按时间升序列出全部迁移构造函数。
func migrationList(deviceFingerprint DeviceFingerprintFunc) []*gormigrate.Migration {
	return []*gormigrate.Migration{
		migration202608080001(),
		migration202608080002(),
		migration202608080003(),
		migration202608080004(),
		migration202608080005(),
		migration202608080006(),
		migration202608080007(),
		migration202608080008(),
		migration202608080009(),
		migration202608080010(),
		migration202608080011(),
		migration202608080012(),
		migration202608080013(),
		migration202608080014(),
		migration202608080015(deviceFingerprint),
		migration202609010001(),
	}
}

package bootstrap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
)

func TestInitCreatesCagoRuntime(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	t.Setenv("AGENTRE_ENV", "test")

	runtime, err := Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	cfg := runtime.Config()
	if cfg == nil {
		t.Fatal("Config() returned nil")
	}
	if cfg.AppName != "agentre" {
		t.Fatalf("AppName = %q, want agentre", cfg.AppName)
	}
	if cfg.Env != configs.TEST {
		t.Fatalf("Env = %q, want %q", cfg.Env, configs.TEST)
	}
	// 默认不开 debug —— debug 日志改由 app_settings 开关控制（见 applyDebugLoggingOnBoot）。
	if cfg.Debug {
		t.Fatal("Debug = true, want false (default)")
	}
	if got := runtime.DataDir(); got != dataDir {
		t.Fatalf("DataDir = %q, want %q", got, dataDir)
	}

	var loggerCfg struct {
		Level   string
		LogFile struct {
			Filename string
		}
	}
	if err := cfg.Scan(context.Background(), "logger", &loggerCfg); err != nil {
		t.Fatalf("Scan(logger) error = %v", err)
	}
	if loggerCfg.Level != "info" {
		t.Fatalf("logger level = %q, want info", loggerCfg.Level)
	}
	wantLog := filepath.Join(dataDir, "logs", "agentre.log")
	if loggerCfg.LogFile.Filename != wantLog {
		t.Fatalf("log filename = %q, want %q", loggerCfg.LogFile.Filename, wantLog)
	}

	// SQLite 文件已被创建在 dataDir 下，且 gormigrate 跟踪表存在
	dbPath := filepath.Join(dataDir, "agentre.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("sqlite file %s missing: %v", dbPath, err)
	}

	gormDB := db.Default()
	if gormDB == nil {
		t.Fatal("db.Default() returned nil")
	}
	if !gormDB.Migrator().HasTable("migrations") {
		t.Fatal("gormigrate 'migrations' tracking table not created")
	}

	if _, err := os.Stat(filepath.Join(dataDir, "config.json")); !os.IsNotExist(err) {
		t.Fatalf("config.json should not be created, stat error = %v", err)
	}
}

// TestInitCreatesOnlyCurrentDatabaseSchema pins the unreleased database
// baseline: a fresh install creates the current model directly, without first
// materializing columns or migration ledger entries that only served old
// development databases.
func TestInitCreatesOnlyCurrentDatabaseSchema(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	t.Setenv("AGENTRE_ENV", "test")

	runtime, err := Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	gormDB := db.Default()
	for table, columns := range map[string][]string{
		"llm_providers":              {"default_model_key", "sync_id", "sync_origin_fingerprint"},
		"llm_provider_models":        {"model_key", "model_id"},
		"agent_backends":             {"model_key", "sync_id", "device_fingerprint", "sync_origin_fingerprint"},
		"agent_backend_cli_overlays": {"backend_sync_id", "agentred_fingerprint", "cli_path", "sync_origin_fingerprint"},
		"agents":                     {"sync_origin_fingerprint"},
		"agent_exec_targets":         {"sync_origin_fingerprint"},
		"departments":                {"sync_origin_fingerprint"},
		"projects":                   {"sync_origin_fingerprint"},
		"project_agents":             {"sync_origin_fingerprint"},
		"project_locations":          {"device_fingerprint", "sync_origin_fingerprint"},
		"chat_sessions":              {"model_key", "exec_agent_backend_id", "cwd", "exec_device_fingerprint", "conversation_id"},
		"chat_messages":              {"first_token_ms", "tokens_per_sec", "device_fingerprint"},
		"chat_message_blocks":        {"message_id", "idx", "type", "tool_call_id", "codec", "data"},
		// 任务并入账号级同步组，三张表各带六列同步元数据；issues 另有执行归属三列。
		"issues":       {"agent_backend_id", "llm_provider_key", "llm_model_key", "sync_id", "sync_origin_fingerprint"},
		"labels":       {"sync_id", "sync_account_id", "sync_version", "sync_updated_at", "sync_origin_fingerprint", "sync_deleted_at"},
		"issue_labels": {"sync_id", "sync_account_id", "sync_version", "sync_updated_at", "sync_origin_fingerprint", "sync_deleted_at"},
	} {
		if !gormDB.Migrator().HasTable(table) {
			t.Errorf("current table %q was not created", table)
			continue
		}
		for _, column := range columns {
			if !gormDB.Migrator().HasColumn(table, column) {
				t.Errorf("current column %s.%s was not created", table, column)
			}
		}
	}

	// 决策 14/16 的改名:机器指纹一律叫 device_fingerprint、同步来源一律叫
	// sync_origin_fingerprint。三侧均未发布,旧名不保留兼容列(决策 22),因此
	// 旧名在全新库上必须一个都不存在——这是编译期抓不到的那一类。
	for table, columns := range map[string][]string{
		"llm_providers": {"model", "max_output", "context_window", "sync_origin"},
		// 正文改存 chat_message_blocks 一块一行,单列形态不再存在。
		"chat_messages":              {"blocks_json", "device_id"},
		"agent_backends":             {"device_id", "sync_origin"},
		"agent_backend_cli_overlays": {"sync_origin"},
		"agents":                     {"sync_origin"},
		"agent_exec_targets":         {"sync_origin"},
		"departments":                {"sync_origin"},
		"projects":                   {"sync_origin"},
		"project_agents":             {"sync_origin"},
		"project_locations":          {"daemon_fingerprint", "sync_origin"},
		"chat_sessions":              {"exec_daemon_fingerprint"},
	} {
		for _, column := range columns {
			if gormDB.Migrator().HasColumn(table, column) {
				t.Errorf("legacy column %s.%s must not exist in the fresh baseline", table, column)
			}
		}
	}

	// 块表的三个索引(spec 2026-08-27-schema-overhaul「改动清单一」):
	// UNIQUE(message_id, idx) 决定重组顺序并按消息取块、(tool_call_id, type, message_id)
	// 是块级操作的定位键、(type, message_id) 是派生视图「按类型取块」那一路。索引缺失
	// 编译期抓不到,也不会让任何用例变红 —— 只是悄悄退回全表串扫,正是本轮要消灭的形态。
	for _, index := range []string{
		"ux_chat_message_blocks_message_idx",
		"idx_chat_message_blocks_tool_call",
		"idx_chat_message_blocks_type_message",
	} {
		if !gormDB.Migrator().HasIndex("chat_message_blocks", index) {
			t.Errorf("index %q on chat_message_blocks was not created", index)
		}
	}

	// 对话身份那一列上的唯一索引(spec 2026-08-31 决策 12):它既是「一条对话在本机
	// 只有一行」的约束,也是 conversation_id → 本地主键那一次查询的走法。索引缺失
	// 编译期抓不到,只会让反向寻址退回全表串扫、并放任重复行落库。
	if !gormDB.Migrator().HasIndex("chat_sessions", "ux_chat_sessions_conversation_id") {
		t.Error("unique index ux_chat_sessions_conversation_id on chat_sessions was not created")
	}

	// 索引**存在**证明不了它会被用上,而「按定位键点查」正是拆块表要买到的那件东西。
	// 定位键索引是部分索引且带 ORDER BY 那一列,两个条件各自都能让 SQLite 悄悄改选
	// (type, message_id) 去扫遍全库的 subagent_state 块:
	//   - 少了查询侧的 tool_call_id <> '',SQLite 证不出绑定变量满足部分索引的
	//     WHERE 子句,这个索引根本不进候选集;
	//   - 少了索引侧的第三列 message_id,它满足不了 ORDER BY message_id DESC,代价
	//     模型据此改选顺带满足排序的那一个。
	// 两处都是「用例全绿、库悄悄慢 250 倍」,所以这里对着真正的查询计划断言,而不是
	// 只数索引名。查询与 chat_repo.findSubagentStateBlock 逐字同形。
	var plan []struct {
		Detail string `gorm:"column:detail"`
	}
	if err := gormDB.Raw(
		"EXPLAIN QUERY PLAN SELECT * FROM `chat_message_blocks`"+
			" JOIN `chat_messages` ON `chat_messages`.`id` = `chat_message_blocks`.`message_id`"+
			" WHERE `chat_messages`.`session_id` = ? AND `chat_message_blocks`.`type` = ?"+
			" AND `chat_message_blocks`.`tool_call_id` = ? AND `chat_message_blocks`.`tool_call_id` <> ''"+
			" ORDER BY `chat_message_blocks`.`message_id` DESC LIMIT 1",
		1, "subagent_state", "tu-1",
	).Scan(&plan).Error; err != nil {
		t.Fatalf("explain subagent_state locator query: %v", err)
	}
	var planText string
	for _, row := range plan {
		planText += row.Detail + "\n"
	}
	if !strings.Contains(planText, "idx_chat_message_blocks_tool_call") {
		t.Errorf("subagent_state locator must be an index point lookup on idx_chat_message_blocks_tool_call, got plan:\n%s", planText)
	}

	var historicalMigrationCount int64
	if err := gormDB.Table("migrations").Where("id IN ?", []string{
		"202608110001", // legacy provider/model and route conversion
		"202608200002", // legacy backend CLI/device conversion
		"202608260001", // seconds-to-milliseconds data rewrite
	}).Count(&historicalMigrationCount).Error; err != nil {
		t.Fatalf("count historical migration ledger entries: %v", err)
	}
	if historicalMigrationCount != 0 {
		t.Fatalf("historical migration ledger entries = %d, want 0", historicalMigrationCount)
	}
}

// TestSQLiteDSNShape 回归(design decisions 1/2/5, docs/specs/2026-08-07-autonomous-turn-resilience.md
// 「SQLite 事务与锁定行为」):DSN 必须带 _txlock=immediate,令所有事务以 BEGIN
// IMMEDIATE 开启 —— 写锁在 BEGIN 时即取,冲突走 busy handler 等锁,而不是像默认的
// deferred 事务那样在写升级时撞 SQLITE_BUSY 立即失败(实测 0.2ms 内即报错,busy
// handler 根本不参与)。同时带 _pragma=synchronous(NORMAL)(WAL 下仍崩溃安全),且不
// 再带 busy_timeout —— glebarez 驱动在每个连接建立时无条件硬编码执行
// `pragma BUSY_TIMEOUT(5000)`(glebarez/go-sqlite@v1.21.2/sqlite.go:880,早于处理
// _pragma),该 DSN 参数从未改变过任何行为,保留只会让读代码的人误以为超时是本项目
// 配置的、可调的。
func TestSQLiteDSNShape(t *testing.T) {
	dsn := sqliteDSN(filepath.Join(t.TempDir(), "x.db"))

	if !strings.Contains(dsn, "_txlock=immediate") {
		t.Fatalf("dsn = %q, want it to contain _txlock=immediate", dsn)
	}
	if !strings.Contains(dsn, "_pragma=synchronous(NORMAL)") {
		t.Fatalf("dsn = %q, want it to contain _pragma=synchronous(NORMAL)", dsn)
	}
	if strings.Contains(dsn, "busy_timeout") {
		t.Fatalf("dsn = %q, must not contain busy_timeout (driver hardcodes it unconditionally on every connection)", dsn)
	}
}

// TestSQLiteDSNAppliesPragmas 用真驱动验证 _pragma 参数确实生效,而不只是字符串拼对:
// synchronous 落到 NORMAL(=1)。busy_timeout 保持驱动硬编码的 5000 不变 —— 证明拿掉
// DSN 里冗余的 busy_timeout 参数没有回归实际行为(问题 2)。
func TestSQLiteDSNAppliesPragmas(t *testing.T) {
	gormDB, err := gorm.Open(sqlite.Open(sqliteDSN(filepath.Join(t.TempDir(), "x.db"))), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	sqlDB, err := gormDB.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	var sync int
	if err := gormDB.Raw("PRAGMA synchronous").Scan(&sync).Error; err != nil {
		t.Fatalf("PRAGMA synchronous query error = %v", err)
	}
	if sync != 1 {
		t.Fatalf("synchronous = %d, want 1 (NORMAL)", sync)
	}

	var busyTimeout int
	if err := gormDB.Raw("PRAGMA busy_timeout").Scan(&busyTimeout).Error; err != nil {
		t.Fatalf("PRAGMA busy_timeout query error = %v", err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000 (driver default, unaffected by removing the DSN param)", busyTimeout)
	}
}

// TestConvertToWALSucceedsOnFreshDatabase 回归(design decisions 3/4,规范「journal
// 模式」):启动时对数据库执行一次 WAL 转换,无竞争时必须成功生效。
func TestConvertToWALSucceedsOnFreshDatabase(t *testing.T) {
	gormDB, err := gorm.Open(sqlite.Open(sqliteDSN(filepath.Join(t.TempDir(), "x.db"))), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	sqlDB, err := gormDB.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	convertToWAL(context.Background(), gormDB)

	var mode string
	if err := gormDB.Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		t.Fatalf("PRAGMA journal_mode query error = %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

// TestConvertToWALContinuesOnFailure 回归(design decision 4):转换失败(典型是转换
// 时刻另有连接持有写锁,见规范「journal 模式」)不当作致命错误 —— 这只是一次性能优
// 化,应用可用性不能靠它;失败只记警告、不返回错误,数据库继续以当前 journal 模式运
// 行,下次启动重试。用真驱动:开一条独立连接以 BEGIN IMMEDIATE(DSN _txlock=immediate)
// 持有写锁不提交,验证 convertToWAL 在冲突下既不 panic 也不改变现有 journal 模式。
func TestConvertToWALContinuesOnFailure(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x.db")

	victim, err := gorm.Open(sqlite.Open(sqliteDSN(dbPath)), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open(victim) error = %v", err)
	}
	victimSQL, err := victim.DB()
	if err != nil {
		t.Fatalf("victim.DB() error = %v", err)
	}
	t.Cleanup(func() { _ = victimSQL.Close() })
	if err := victim.Exec("CREATE TABLE t (id INTEGER)").Error; err != nil {
		t.Fatalf("create table: %v", err)
	}

	blocker, err := gorm.Open(sqlite.Open(sqliteDSN(dbPath)), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open(blocker) error = %v", err)
	}
	blockerSQL, err := blocker.DB()
	if err != nil {
		t.Fatalf("blocker.DB() error = %v", err)
	}
	t.Cleanup(func() { _ = blockerSQL.Close() })

	tx := blocker.Begin()
	if tx.Error != nil {
		t.Fatalf("blocker.Begin() error = %v", tx.Error)
	}
	if err := tx.Exec("INSERT INTO t (id) VALUES (1)").Error; err != nil {
		t.Fatalf("blocker insert: %v", err)
	}
	t.Cleanup(func() { tx.Rollback() })

	convertToWAL(context.Background(), victim) // 冲突下不得 panic

	var mode string
	if err := victim.Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		t.Fatalf("PRAGMA journal_mode query error = %v", err)
	}
	if mode == "wal" {
		t.Fatalf("journal_mode = wal, want unchanged — conversion should have failed while blocker holds the write lock")
	}
}

// TestInitIgnoresAGENTREDebugEnv 回归：旧的 AGENTRE_DEBUG 环境变量已被砍掉，
// 改由「设置 → 版本 & 更新 → Debug 日志」开关控制。即使设置该变量，启动也必须
// 保持默认 info 级别、cfg.Debug=false。
func TestInitIgnoresAGENTREDebugEnv(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	t.Setenv("AGENTRE_ENV", "test")
	t.Setenv("AGENTRE_DEBUG", "true") // legacy var must be ignored

	runtime, err := Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	cfg := runtime.Config()
	if cfg.Debug {
		t.Fatal("Debug = true; AGENTRE_DEBUG must be ignored (use the in-app Debug toggle)")
	}

	var loggerCfg struct{ Level string }
	if err := cfg.Scan(context.Background(), "logger", &loggerCfg); err != nil {
		t.Fatalf("Scan(logger) error = %v", err)
	}
	if loggerCfg.Level != "info" {
		t.Fatalf("logger level = %q, want info", loggerCfg.Level)
	}
}

// TestClaimRelativeBackends_GivenOccupiedSortOrder_AtomicallyReplacesTheTarget
// uses the bootstrapped SQLite schema because sqlmock cannot enforce the real
// (agent_id, sort_order) uniqueness constraint. R13 requires a runtime claim
// to replace the old target without changing this desktop's active count.
func TestClaimRelativeBackends_GivenOccupiedSortOrder_AtomicallyReplacesTheTarget(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	t.Setenv("AGENTRE_ENV", "test")

	runtime, err := Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	gdb := db.Default()
	original := &agent_backend_entity.AgentBackend{
		Type: "claudecode", Name: "legacy relative", Status: 1,
	}
	if err := gdb.Create(original).Error; err != nil {
		t.Fatalf("create relative backend: %v", err)
	}
	if err := gdb.Create(&agent_entity.AgentExecTarget{
		AgentID: 1, AgentBackendID: original.ID, SortOrder: 0,
	}).Error; err != nil {
		t.Fatalf("create original target: %v", err)
	}

	claims, err := agent_backend_repo.AgentBackend().ClaimRelative(context.Background(), "sha256:desktop-a")
	if err != nil {
		t.Fatalf("ClaimRelative() error = %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("claim count = %d, want 1", len(claims))
	}

	var targets []agent_entity.AgentExecTarget
	if err := gdb.Where("agent_id = ?", 1).Order("sort_order ASC").Find(&targets).Error; err != nil {
		t.Fatalf("list claimed targets: %v", err)
	}
	if len(targets) != 1 || targets[0].AgentBackendID != claims[0].ClaimedBackend.ID || targets[0].SortOrder != 0 {
		t.Fatalf("claimed targets = %#v, want one replacement at sort order 0", targets)
	}
}

func TestSeedCEOAgent(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	t.Setenv("AGENTRE_ENV", "test")

	runtime, err := Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	gdb := db.Default()
	var count int64
	if err := gdb.Table("agents").Where("system_badge = ?", "DEFAULT").Count(&count).Error; err != nil {
		t.Fatalf("count CEO agent: %v", err)
	}
	if count != 1 {
		t.Fatalf("CEO agent count = %d, want 1", count)
	}

	var ceoSyncID string
	if err := gdb.Table("agents").Where("system_badge = ?", "DEFAULT").
		Select("sync_id").Row().Scan(&ceoSyncID); err != nil {
		t.Fatalf("scan CEO agent sync ID: %v", err)
	}
	if ceoSyncID != agent_entity.DefaultAgentSyncID {
		t.Fatalf("CEO agent sync ID = %q, want canonical %q", ceoSyncID, agent_entity.DefaultAgentSyncID)
	}

	var deptCount int64
	if err := gdb.Table("departments").Count(&deptCount).Error; err != nil {
		t.Fatalf("count departments: %v", err)
	}
	if deptCount != 0 {
		t.Fatalf("departments count = %d, want 0 (no default seed)", deptCount)
	}

	// 回归 Problem 20 / 决策 26:202608080004 的 CEO agent 种子用
	// strftime('%s','now')（秒），全仓其余时间戳统一 UnixMilli——那一行的
	// createtime/updatetime 曾被解释成 1970 年附近。追加的补丁迁移把
	// < 1e11 的种子时间戳换算成毫秒；在全新库上跑完整条迁移链后，这一行应当
	// 已经是毫秒量级（> 1e11，即公元 1973 年之后）而不是秒量级。
	const millisecondFloor = 100_000_000_000
	var ceoCreatetime, ceoUpdatetime int64
	if err := gdb.Table("agents").Where("system_badge = ?", "DEFAULT").
		Select("createtime, updatetime").Row().Scan(&ceoCreatetime, &ceoUpdatetime); err != nil {
		t.Fatalf("scan CEO agent timestamps: %v", err)
	}
	if ceoCreatetime < millisecondFloor {
		t.Fatalf("CEO agent createtime = %d, want a millisecond epoch (> %d); seconds-to-milliseconds patch migration did not run",
			ceoCreatetime, millisecondFloor)
	}
	if ceoUpdatetime < millisecondFloor {
		t.Fatalf("CEO agent updatetime = %d, want a millisecond epoch (> %d); seconds-to-milliseconds patch migration did not run",
			ceoUpdatetime, millisecondFloor)
	}
}

// TestInitRegistersProjectLocationRepo 回归：远端 backend 拉 cwd 时会走
// project_location_repo.ProjectLocation()；bootstrap 漏注册会导致前端只看到
// 「Agent 调用失败：project_location_repo not registered」。
func TestInitRegistersProjectLocationRepo(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	t.Setenv("AGENTRE_ENV", "test")

	prev := project_location_repo.ProjectLocation()
	project_location_repo.RegisterProjectLocation(nil)
	t.Cleanup(func() { project_location_repo.RegisterProjectLocation(prev) })

	runtime, err := Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	if project_location_repo.ProjectLocation() == nil {
		t.Fatal("project_location_repo.ProjectLocation() = nil after Init; bootstrap forgot to RegisterProjectLocation")
	}
}

func TestInitDoesNotResetActiveSessions(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	t.Setenv("AGENTRE_ENV", "test")

	runtime, err := Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	gdb := db.Default()
	now := time.Now().UnixMilli()
	if err := gdb.Exec(`INSERT INTO chat_sessions (id, agent_id, title, agent_status, status, createtime, updatetime)
VALUES (?, ?, ?, ?, ?, ?, ?)`, 9001, 1, "still running", "running", 1, now, now).Error; err != nil {
		t.Fatalf("insert running session: %v", err)
	}

	runtime2, err := Init(context.Background())
	if err != nil {
		t.Fatalf("second Init() error = %v", err)
	}
	t.Cleanup(runtime2.Close)

	var got string
	if err := db.Default().Table("chat_sessions").Select("agent_status").Where("id = ?", 9001).Scan(&got).Error; err != nil {
		t.Fatalf("load session status: %v", err)
	}
	if got != "running" {
		t.Fatalf("agent_status after Init = %q, want running", got)
	}
}

func TestResetStaleActiveSessionsMarksRunningAndWaitingAsError(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	t.Setenv("AGENTRE_ENV", "test")

	runtime, err := Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	gdb := db.Default()
	now := time.Now().UnixMilli()
	rows := []struct {
		id     int64
		status string
	}{
		{9101, "running"},
		{9102, "waiting"},
		{9103, "idle"},
	}
	for _, row := range rows {
		// conversation_id 上有唯一索引:每一行都得有自己的身份,插空串会第二行就撞车。
		if err := gdb.Exec(`INSERT INTO chat_sessions (id, conversation_id, agent_id, title, agent_status, status, createtime, updatetime)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, row.id, testConversationID(row.id), 1, row.status, row.status, 1, now, now).Error; err != nil {
			t.Fatalf("insert %s session: %v", row.status, err)
		}
	}

	if err := ResetStaleActiveSessions(context.Background()); err != nil {
		t.Fatalf("ResetStaleActiveSessions() error = %v", err)
	}

	got := map[int64]string{}
	type row struct {
		ID          int64
		AgentStatus string
	}
	var out []row
	if err := db.Default().Table("chat_sessions").Select("id, agent_status").Where("id IN ?", []int64{9101, 9102, 9103}).Scan(&out).Error; err != nil {
		t.Fatalf("load statuses: %v", err)
	}
	for _, row := range out {
		got[row.ID] = row.AgentStatus
	}
	if got[9101] != "error" || got[9102] != "error" || got[9103] != "idle" {
		t.Fatalf("statuses after reset = %#v, want running/waiting error and idle unchanged", got)
	}
}

// TestResetStaleActiveSessionsLeavesRemoteSessionsAlone 钉死启动期清理的边界:跑在
// 远端 daemon 上的会话不归它管。
//
// 那一轮的执行者是另一台机器上的进程,它不随桌面 App 退出而消亡(R4:断连不终止会话)。
// 在连上那台 daemon 之前无从知道它是不是还在跑,一律翻成 error 就是报一个假失败;而一条
// 在桌面端离线期间没产出任何新内容的远端会话不会被补齐重放改写,那条假失败会**永久**
// 留在界面上 —— 恰好推翻「关掉 App 再打开,看得到这段时间远端跑出来的全部内容」。
// 远端会话的去向改由 chat_svc.CatchUpRemoteSessions 按 daemon 交回的生命周期逐条判定。
func TestResetStaleActiveSessionsLeavesRemoteSessionsAlone(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	t.Setenv("AGENTRE_ENV", "test")

	runtime, err := Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	gdb := db.Default()
	now := time.Now().UnixMilli()
	rows := []struct {
		id       int64
		deviceID int64
		fp       string
	}{
		{9201, 7, "sha256:beef"}, // 远端跑着:不动
		{9202, 0, ""},            // 本机:照旧翻 error
		{9203, 7, ""},            // 记了设备却没有实例标识:游标无从校验,按本机处理
	}
	for _, row := range rows {
		if err := gdb.Exec(`INSERT INTO chat_sessions (id, conversation_id, agent_id, title, agent_status, status, exec_device_id, exec_device_fingerprint, createtime, updatetime)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, row.id, testConversationID(row.id), 1, "t", "running", 1, row.deviceID, row.fp, now, now).Error; err != nil {
			t.Fatalf("insert session %d: %v", row.id, err)
		}
	}

	if err := ResetStaleActiveSessions(context.Background()); err != nil {
		t.Fatalf("ResetStaleActiveSessions() error = %v", err)
	}

	type row struct {
		ID          int64
		AgentStatus string
	}
	var out []row
	if err := db.Default().Table("chat_sessions").Select("id, agent_status").
		Where("id IN ?", []int64{9201, 9202, 9203}).Scan(&out).Error; err != nil {
		t.Fatalf("load statuses: %v", err)
	}
	got := map[int64]string{}
	for _, r := range out {
		got[r.ID] = r.AgentStatus
	}
	if got[9201] != "running" {
		t.Fatalf("remote session status = %q, want running (the daemon may still be running it)", got[9201])
	}
	if got[9202] != "error" || got[9203] != "error" {
		t.Fatalf("local session statuses = %#v, want both error", got)
	}
}

// testConversationID 给手工插进 chat_sessions 的测试行造一个身份。取值形态无所谓,
// 唯一索引只要求彼此不同 —— 生产上这个值由 chat_repo.Session().Create 铸(新行)或
// 由迁移 202608080015 回填(存量行)。
func testConversationID(id int64) string {
	return fmt.Sprintf("0198f4c1-a000-7c0d-8b21-%012d", id)
}

package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-ai/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-ai/agentre/internal/repository/project_location_repo"
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

	var deptCount int64
	if err := gdb.Table("departments").Count(&deptCount).Error; err != nil {
		t.Fatalf("count departments: %v", err)
	}
	if deptCount != 0 {
		t.Fatalf("departments count = %d, want 0 (no default seed)", deptCount)
	}
}

// TestMigrationsAllowSameNamedAgentsFromDifferentMachines 回归 R12a：
//
// 双机办公的用户两边各建过一个「开发」，登录同一账号后两份都该落地，由用户自行删掉
// 多余的那个（规格 R12a）。原先 agents(name) WHERE status=1 上有唯一索引，第二份**插
// 不进来** —— 下行每 30 秒撞一次 2067，那一行连同它的下游永久卡在暂缓队列里，用户连
// 「删掉多余那个」的机会都没有。
//
// 手输重名照旧被拒：那道闸在 agent_svc.Create/Update 的 FindByName 上，与本索引无关。
func TestMigrationsAllowSameNamedAgentsFromDifferentMachines(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dataDir)
	t.Setenv("AGENTRE_ENV", "test")

	runtime, err := Init(context.Background())
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	gdb := db.Default()
	// 本机自己那份（迁移里种下的默认 Agent 就叫「CEO 助手」）。
	var seeded agent_entity.Agent
	if err := gdb.Table("agents").Where("system_badge = ?", "DEFAULT").First(&seeded).Error; err != nil {
		t.Fatalf("load seeded agent: %v", err)
	}

	// 另一台机器同名、但同步标识不同的那一份，经下行落地。
	arriving := map[string]any{
		"name": seeded.Name, "status": 1,
		"sync_id": "01KZQPHK6Q55AKRHWFX5EM0YWH", "sync_account_id": 1,
		"prompt_json": "[]", "skills_json": "[]", "tools_json": "[]",
	}
	if err := gdb.Table("agents").Create(arriving).Error; err != nil {
		t.Fatalf("a same-named agent from another machine must be able to land: %v", err)
	}

	var same int64
	if err := gdb.Table("agents").Where("name = ? AND status = 1", seeded.Name).Count(&same).Error; err != nil {
		t.Fatalf("count same-named agents: %v", err)
	}
	if same != 2 {
		t.Fatalf("same-named active agents = %d, want 2 (both histories kept for the user to merge)", same)
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
		if err := gdb.Exec(`INSERT INTO chat_sessions (id, agent_id, title, agent_status, status, createtime, updatetime)
VALUES (?, ?, ?, ?, ?, ?, ?)`, row.id, 1, row.status, row.status, 1, now, now).Error; err != nil {
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
		if err := gdb.Exec(`INSERT INTO chat_sessions (id, agent_id, title, agent_status, status, exec_device_id, exec_daemon_fingerprint, createtime, updatetime)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, row.id, 1, "t", "running", 1, row.deviceID, row.fp, now, now).Error; err != nil {
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

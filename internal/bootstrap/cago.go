package bootstrap

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/agentre-hub/agentre/internal/buildinfo"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/claudecode"
	openclawrt "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/openclaw"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/piagent"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentskill/claudeskill"  // 触发 discoverer init 注册
	_ "github.com/agentre-hub/agentre/internal/pkg/agentskill/codexskill"   // 触发 discoverer init 注册
	_ "github.com/agentre-hub/agentre/internal/pkg/agentskill/piagentskill" // 触发 discoverer init 注册
	"github.com/agentre-hub/agentre/internal/pkg/agrctlinstall"
	"github.com/agentre-hub/agentre/internal/pkg/ctlendpoint"
	"github.com/agentre-hub/agentre/internal/pkg/httpgateway"
	"github.com/agentre-hub/agentre/internal/pkg/paths"
	"github.com/agentre-hub/agentre/internal/pkg/sysnotify"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
	"github.com/agentre-hub/agentre/internal/repository/hook_repo"
	"github.com/agentre-hub/agentre/internal/repository/issue_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncqueue_repo"
	"github.com/agentre-hub/agentre/internal/service/agent_backend_svc"
	"github.com/agentre-hub/agentre/internal/service/app_settings_svc"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/ctl_svc"
	"github.com/agentre-hub/agentre/internal/service/ctlskill_svc"
	"github.com/agentre-hub/agentre/internal/service/hooktool_svc"
	"github.com/agentre-hub/agentre/internal/service/issue_svc"
	"github.com/agentre-hub/agentre/internal/service/notification_svc"
	"github.com/agentre-hub/agentre/internal/service/orgtool_svc"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
	"github.com/agentre-hub/agentre/internal/service/skill_svc"
	"github.com/agentre-hub/agentre/internal/service/subagent_svc"
	"github.com/agentre-hub/agentre/internal/service/workspace_fs_svc"
	"github.com/agentre-hub/agentre/migrations"

	"github.com/cago-frame/cago"
	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/configs/memory"
	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"

	// 注册 SQLite 驱动
	_ "github.com/cago-frame/cago/database/db/sqlite"
)

// dbFileName 桌面端 SQLite 数据库文件名（位于 AppDataDir 根目录）
const dbFileName = "agentre.db"

var runtime *Runtime

// Runtime owns the cago config and lifecycle hooks used by the desktop app.
type Runtime struct {
	config  *configs.Config
	dataDir string
}

// Init initializes the cago config/logger/database stack for the process.
// 启动顺序：dataDir → logger → SQLite(db.Database 组件) → migrations。
func Init(ctx context.Context) (*Runtime, error) {
	dataDir, err := AppDataDir()
	if err != nil {
		return nil, err
	}

	logsDir, err := LogsDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create logs dir: %w", err)
	}

	dbPath := filepath.Join(dataDir, dbFileName)
	cfg, err := configs.NewConfig(paths.AppName, configs.WithSource(memory.NewSource(defaultConfigValues(logsDir, sqliteDSN(dbPath)))))
	if err != nil {
		return nil, fmt.Errorf("create cago config: %w", err)
	}
	if err := logger.Logger(ctx, cfg); err != nil {
		return nil, fmt.Errorf("init cago logger: %w", err)
	}

	// 注册 SQLite 数据库组件。cago 启动 db 失败时会 panic，由调用方 recover/log。
	cago.New(ctx, cfg).Registry(db.Database())

	// journal_mode 是持久化的数据库属性，一次转换成功即永久生效，不必（也不能）挂进
	// sqliteDSN 的 _pragma 让每个连接重复执行——失败只记警告、不阻断启动，详见
	// convertToWAL。放在 migrations 之前，让迁移本身也跑在 WAL 上。
	convertToWAL(ctx, db.Default())

	// keychain 后端必须在迁移之前确立:存量对话回填 conversation_id 的派生输入是
	// keychain 里那个设备指纹(见 desktopDeviceFingerprint)。它本身不碰数据库、
	// 也不依赖任何服务,提前到这里是纯粹的顺序调整;装配 Server / Remote Device
	// 时仍然捕获的是同一个实例。
	if err := initKeychain(ctx); err != nil {
		return nil, fmt.Errorf("init keychain: %w", err)
	}

	if err := migrations.RunMigrations(db.Default(), desktopDeviceFingerprint); err != nil {
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	// 注入 repository 默认实现，service 层调 llm_provider_repo.LLMProvider() 直接拿到 GORM 版。
	llm_provider_repo.RegisterLLMProvider(llm_provider_repo.NewLLMProvider())
	agent_backend_repo.RegisterAgentBackend(agent_backend_repo.NewAgentBackend())
	app_setting_repo.RegisterAppSetting(app_setting_repo.NewAppSetting())
	department_repo.RegisterDepartment(department_repo.NewDepartment())
	agent_repo.RegisterAgent(agent_repo.NewAgent())
	agent_repo.RegisterAgentExecTarget(agent_repo.NewAgentExecTarget())
	hook_repo.RegisterHook(hook_repo.NewHook())
	hook_repo.RegisterHookEvent(hook_repo.NewHookEvent())
	chat_repo.RegisterSession(chat_repo.NewSession())
	chat_repo.RegisterMessage(chat_repo.NewMessage())
	project_repo.RegisterProject(project_repo.NewProject())
	project_repo.RegisterProjectAgent(project_repo.NewProjectAgent())
	project_location_repo.RegisterProjectLocation(project_location_repo.NewProjectLocation())
	// 本地同步骨架三张表（387-390 行）：真正的入队/出队/上行/下行是后续任务，
	// 这里只注册仓储读写口，让它们有地方落脚。
	syncqueue_repo.RegisterLostChange(syncqueue_repo.NewLostChange())
	syncqueue_repo.RegisterOutboundQueue(syncqueue_repo.NewOutboundQueue())
	syncqueue_repo.RegisterInboundQueue(syncqueue_repo.NewInboundQueue())
	project_svc.SetDefault(project_svc.New())
	issue_repo.RegisterIssue(issue_repo.NewIssue())
	issue_repo.RegisterLabel(issue_repo.NewLabel())
	issue_repo.RegisterIssueLabel(issue_repo.NewIssueLabel())
	issue_svc.SetDefault(issue_svc.New())
	// 把 project_svc 的 cwd 解析注入 chat_svc —— chat_svc 不直接 import project_svc，
	// 避免 project_svc → chat_repo 与 chat_svc → project_svc 形成环。
	chat_svc.RegisterCwdResolver(project_svc.Default().ResolveSessionCwd)
	// 把 chat_svc 的会话解析注入 workspace_fs_svc（它自己声明的窄接口），让它不必
	// 跨域读 chat / agent / agent_backend 三张表。这里懒解析 chat_svc.Chat()：
	// RegisterChat 在 app.go registerChatService() 里才执行，此刻还是 nil。
	workspace_fs_svc.RegisterSessionWorkspaceResolver(
		func(ctx context.Context, sessionID int64) (int64, string, error) {
			return chat_svc.Chat().ResolveSessionWorkspace(ctx, sessionID)
		})
	// 第二个窄接口：工作根认领要知道「本会话 AI 写过哪些路径」，那是 chat 消息
	// 里的事实。这里注入的是包级函数（不经 chat_svc.Chat()），因为它只读消息、
	// 不依赖那个单例的任何状态。
	workspace_fs_svc.RegisterSessionWrittenPaths(chat_svc.SessionWrittenPaths)

	// 启动时按持久化的开关恢复 Debug 日志级别（取代旧 AGENTRE_DEBUG 环境变量）。
	applyDebugLoggingOnBoot(ctx)

	// Server 接入：注册 server_state_repo + server_svc 默认实现。
	// server_svc 此时的 emit 为 nil；app.go.startup 在 wails ctx 就绪后调 SetEmitter 绑定事件源。
	if err := InitServer(ctx); err != nil {
		return nil, fmt.Errorf("init server: %w", err)
	}
	if err := InitRemoteDevice(ctx); err != nil {
		return nil, fmt.Errorf("init remote device: %w", err)
	}
	if err := agent_backend_svc.AgentBackend().ClaimRelativeBackends(ctx); err != nil {
		return nil, fmt.Errorf("claim relative agent backends: %w", err)
	}
	openclawrt.RegisterConfigResolver(openclawrt.ConfigResolverFunc(agent_backend_svc.ResolveOpenClawRuntimeConfig))

	// 装配本地 HTTP 代理。启动失败软降级——只记日志、不阻断 App。
	host, port := loadProxyAddr(ctx)
	gw := httpgateway.New(host, port, llm_provider_repo.LLMProvider())
	if err := gw.Start(ctx); err != nil {
		logger.Default().Warn("httpgateway start", zap.Error(err))
	}
	if st := gw.Status(); st.State != "running" {
		logger.Default().Warn("httpgateway degraded", zap.String("reason", st.Reason))
	}
	agent_backend_svc.RegisterGateway(gw)
	app_settings_svc.RegisterGateway(gw)
	chat_svc.RegisterGateway(gw)

	// 挂组织架构工具 MCP handler(/mcp/org/),并注册 TurnMCPProvider:
	// agent 开了 org 工具的会话 turn 注入该 MCP server(审批在服务端,见 orgtool_svc)。
	// 注意:RegisterDeps(含 chat_svc.Chat() 作为 ApprovalGateway)延迟到 app.go
	// registerChatService() 中 RegisterChat 之后执行——此时 chat_svc.Chat() 已非 nil。
	gw.RegisterMCP("/mcp/org/", orgtool_svc.Default().MCPHandler())
	orgtool_svc.Default().SetGatewayBaseURL(gw.BaseURL())
	chat_svc.RegisterTurnMCPProvider(orgtool_svc.Default().BuildTurnMCP)
	// 挂「调用子 agent」工具 MCP handler(/mcp/subagent/) + 注册 TurnMCPProvider:
	// agent 开了 subagent 工具的会话 turn 注入该 MCP server(无审批门, 见 subagent_svc)。
	gw.RegisterMCP("/mcp/subagent/", subagent_svc.Default().MCPHandler())
	subagent_svc.Default().SetGatewayBaseURL(gw.BaseURL())
	chat_svc.RegisterTurnMCPProvider(subagent_svc.Default().BuildTurnMCP)
	// 挂脚本 Hook 工具 MCP handler(/mcp/hook/) + 注册 TurnMCPProvider:agent 开了 hook 工具
	// 的会话 turn 注入该 MCP server(写操作/执行审批在服务端,见 hooktool_svc)。RegisterDeps
	// (含 chat_svc.Chat())延迟到 app.go registerChatService() 中 RegisterChat 之后执行。
	gw.RegisterMCP("/mcp/hook/", hooktool_svc.Default().MCPHandler())
	hooktool_svc.Default().SetGatewayBaseURL(gw.BaseURL())
	chat_svc.RegisterTurnMCPProvider(hooktool_svc.Default().BuildTurnMCP)

	// 本地控制 API(/ctl/*):供外部 `agrctl ctl` CLI 驱动——列 agent/项目、给指定 agent
	// 建会话并派发任务(等价于「@ 某 agent 发消息」),无需注入 MCP。deps 走 repo/svc 单例
	// (chat 网关懒解析 chat_svc.Chat(),兼容 RegisterChat 尚未执行的时序)。BaseURL 就绪后把
	// 「实际 URL + 控制 token」写进 AppDataDir 的握手文件,CLI 据此定位并鉴权。
	ctl_svc.Default().RegisterDeps(agent_repo.Agent(), ctl_svc.ProjectSvcGateway(), ctl_svc.ChatSvcGateway())
	gw.RegisterControl(ctl_svc.Default().ControlHandler())
	if base := gw.BaseURL(); base != "" {
		if err := ctlendpoint.Write(dataDir, ctlendpoint.Endpoint{URL: base, Token: ctl_svc.Default().Token()}); err != nil {
			logger.Default().Warn("ctl endpoint file write", zap.Error(err))
		}
	}
	// 远端执行(agentred):daemon 上 CLI 子进程访问内置工具 MCP(org/subagent/
	// hook)会被 daemon 改写成 daemon 本地 URL,再经 WS 反向请求隧道回 desktop。这里
	// 装配把隧道请求重放到 desktop 本机 gateway 的 dispatcher。无 client 超时:approval 类
	// 工具可挂几分钟,由 MCP handler 自身上限收口(见 approvalTimeout)。
	remote.RegisterMCPProxyDispatcher(remote.NewLocalGatewayDispatcher(gw.BaseURL, &http.Client{}))

	// 技能包(skill pack)注入:skill_svc 组合 agent 授权 + 发现,chat_svc 按 CapSkills
	// 在 runTurn 注入 RunRequest.EnabledPlugins(runtime 各自渲染到 CLI 配置)。
	skill_svc.Register(agent_repo.Agent(), agent_backend_repo.AgentBackend(), agent_repo.AgentExecTarget(), agent_backend_svc.NewRemoteSkillDiscoverer())
	chat_svc.RegisterEnabledPluginsProvider(func(ctx context.Context, a *agent_entity.Agent, agentBackendID int64) map[string]bool {
		// agentBackendID = 这一轮实际落到的那一档(R15b/R15e);0 = 老会话未钉档,
		// skill_svc 自行回落到主档。
		m, err := skill_svc.Default().EnabledPluginsMapForTarget(ctx, a.ID, agentBackendID)
		if err != nil {
			return nil // 发现/查询失败 → 软降级(本轮不约束技能集),不阻断对话
		}
		return m
	})

	// 注入平台原生通知实现，供前端 App.ShowNotification 调用。
	notification_svc.RegisterNotifier(sysnotify.New())

	// 把 gateway 的 SteerInbox 注入到 claudecode runner，让 Steer 能 Push 进去；
	// 之后 PostToolUse hook 子进程会 GET /hook/v1/inbox 拉走，turn 结束时
	// chat_svc 还会调 runner.DrainPending 把残留转成下一轮的 user msg。
	claudecode.Default().SetSteerInbox(gw.Steer())

	// 安装 agrctl 伴随 CLI 并把 PostToolUse hook 指向它（<AppDataDir>/bin/agrctl）。hook 每次
	// 工具调用都会 exec，用小二进制而非整个桌面 app。从随 app 打进 bundle 的源拷到可写的
	// AppDataDir（版本变则重装）；dev 无 bundle 源则跳过安装。hookCLIPath 始终指向该确定路径，
	// 缺失时该次 hook 优雅失败(不注入 steer)，绝不因回落到 agentre 而误 boot GUI。
	if src, ok := agrctlinstall.BundledSourcePath(); ok {
		if _, _, err := agrctlinstall.EnsureInstalled(dataDir, src, buildinfo.CommitID); err != nil {
			logger.Default().Warn("agrctl install", zap.Error(err))
		}
	}
	agrctlPath := agrctlinstall.InstalledPath(dataDir)
	// dev 覆盖：无 bundle 源时(wails dev)自动安装不发生，开发者 `make agrctl` 后可用
	// AGENTRE_AGRCTL_PATH 指向 build/bin/agrctl 让 hook/steer 在 dev 下也生效。
	if override := strings.TrimSpace(os.Getenv("AGENTRE_AGRCTL_PATH")); override != "" {
		agrctlPath = override
	}
	claudecode.Default().SetHookCLIPath(agrctlPath)

	// ctl 控制通道技能包(internal/pkg/ctlskill)：让 agrctl 以 Claude Code 插件 + 通用
	// Agent Skill 目录两种形态出现在各 CLI 的技能发现里，复用刚算出的 agrctlPath。
	// 拒绝标记、AGENTRE_ENV=test 两道跳过闸，以及失败降级为 warn，都在服务层内部处理，
	// 与上面 agrctl 安装本身同一降级口径。
	ctlskill_svc.Register(agrctlPath, ctlSkillVersion())
	ctlskill_svc.CtlSkill().InstallOnBoot(ctx)

	runtime = &Runtime{config: cfg, dataDir: dataDir}
	return runtime, nil
}

// ctlSkillVersion 技能包清单里的版本号：应用版本，构建注入了 commit 就再缀上它，这样
// 每次发布构建都会触发一次重铺。不能直接用 buildinfo.CommitID —— 它只在 make build 的
// ldflags 里注入，wails dev / go run 起的进程里是空串，写出去就是 "version": "" 的
// plugin.json / marketplace.json。configs.Version 有内置缺省，永远非空。
func ctlSkillVersion() string {
	if commit := buildinfo.ShortCommitID(); commit != "" {
		return configs.Version + "+" + commit
	}
	return configs.Version
}

// sessionResetter is bootstrap's narrow view of chat_repo.SessionRepo (ISP): only the
// startup cleanup this package needs, not the full ~40-method surface.
// chat_repo.Session() satisfies it structurally.
type sessionResetter interface {
	ResetActiveSessions(ctx context.Context) (int64, error)
}

// ResetStaleActiveSessions turns persisted running/waiting sessions left by a
// dead previous desktop process into error. Call this only after the Wails
// single-instance lock has admitted the process as the primary instance.
func ResetStaleActiveSessions(ctx context.Context) error {
	var repo sessionResetter = chat_repo.Session()
	n, err := repo.ResetActiveSessions(ctx)
	if err != nil {
		logger.Default().Warn("reset stale active sessions", zap.Error(err))
		return err
	}
	if n > 0 {
		logger.Default().Info("reset stale active sessions", zap.Int64("count", n))
	}
	return nil
}

// loadProxyAddr 从 app_settings 表读监听地址 / 端口；缺失走默认 127.0.0.1:DefaultProxyListenPort。
func loadProxyAddr(ctx context.Context) (string, int) {
	host := app_setting_entity.DefaultProxyListenHost
	port := app_setting_entity.DefaultProxyListenPort
	if got, err := app_setting_repo.AppSetting().Get(ctx, app_setting_entity.KeyProxyListenHost); err == nil && got != nil && strings.TrimSpace(got.Value) != "" {
		host = strings.TrimSpace(got.Value)
	}
	if got, err := app_setting_repo.AppSetting().Get(ctx, app_setting_entity.KeyProxyListenPort); err == nil && got != nil && strings.TrimSpace(got.Value) != "" {
		port = app_setting_entity.ParseProxyPort(got.Value)
	}
	// 环境变量覆盖(最高优先级):e2e 用 AGENTRE_PROXY_PORT=0 绑 OS 临时端口,与已运行的正式
	// Agentre(固定 52401)互不抢端口,保证 gateway 在 e2e 中可靠起来(否则 BaseURL 为空、
	// 内置工具之类经 gateway 的回投全部失效)。生产不设此变量,行为不变。
	if p, ok := proxyPortFromEnv(); ok {
		port = p
	}
	return host, port
}

// proxyPortFromEnv 解析 AGENTRE_PROXY_PORT 覆盖值。未设置 / 非数字 / 越界 → ok=false(回退默认)。
// 0 合法,表示让 OS 选一个空闲端口(e2e 隔离用)。
func proxyPortFromEnv() (int, bool) {
	raw := strings.TrimSpace(os.Getenv("AGENTRE_PROXY_PORT"))
	if raw == "" {
		return 0, false
	}
	p, err := strconv.Atoi(raw)
	if err != nil || p < 0 || p > 65535 {
		return 0, false
	}
	return p, true
}

// Default returns the initialized runtime, if Init has already been called.
func Default() *Runtime {
	return runtime
}

// Config returns the cago config associated with this runtime.
func (r *Runtime) Config() *configs.Config {
	if r == nil {
		return nil
	}
	return r.config
}

// DataDir returns the resolved data directory for this runtime.
func (r *Runtime) DataDir() string {
	if r == nil {
		return ""
	}
	return r.dataDir
}

// Close flushes logger buffers.
func (r *Runtime) Close() {
	if err := logger.Default().Sync(); err != nil {
		logger.Default().Debug("sync logger", zap.Error(err))
	}
}

// AppDataDir returns the directory for local Agentre state.
// 实际实现在 paths.AppDataDir；保留 wrapper 是为了让现有 internal/bootstrap.AppDataDir
// 调用点（main.go 等）零改动。
func AppDataDir() (string, error) { return paths.AppDataDir() }

// sqliteDSN 给 SQLite 文件路径挂上连接参数。
//
// _txlock=immediate: 令每个事务以 BEGIN IMMEDIATE 开启,在 BEGIN 时就取写锁,冲突走
// busy handler 等锁最多 5s(驱动硬编码的 busy_timeout,见下)。默认的 deferred 事务先
// 拿读快照、写升级时才取锁,而 SQLite 规范在升级冲突时不调用 busy handler、直接返回
// SQLITE_BUSY —— 并发 turn 流式写库时曾借此在 0.2ms 内报 database is locked,busy
// handler 根本没机会介入。glebarez 驱动解析该参数(glebarez/go-sqlite@v1.21.2/sqlite.go:902)
// 对每个池化连接生效;启动后 Exec("BEGIN ...") 只作用单个连接,不可用。
//
// _pragma=synchronous(NORMAL): 配合 WAL(见 convertToWAL)仍崩溃安全 —— 进程崩溃不
// 会损坏数据库,只在断电/内核崩溃时可能丢失最后若干已提交事务,换来 WAL 写性能收益。
//
// 不带 busy_timeout: glebarez 驱动在每个连接建立时无条件硬编码执行
// `pragma BUSY_TIMEOUT(5000)`(glebarez/go-sqlite@v1.21.2/sqlite.go:880,且早于处理
// _pragma),该 DSN 参数从未改变过任何行为,保留只会让读代码的人误以为超时是本项目
// 配置的、可调的。
//
// journal_mode 不在这里设置 —— 见 convertToWAL 的 doc 注释：_pragma 对每个连接无条件
// 执行，首次转换失败会让那次连接建立本身失败进而阻断启动。
func sqliteDSN(dbPath string) string {
	return dbPath + "?_txlock=immediate&_pragma=synchronous(NORMAL)"
}

// convertToWAL 启动时把数据库转换成 WAL journal 模式，仅需成功执行一次 —— 该属性
// 持久化在数据库文件头，后续启动无须重复生效判断。转换失败(典型是转换时刻另有连接
// 持有写锁,实测并发连接下 PRAGMA journal_mode=WAL 会直接报 database is locked)不
// 当作致命错误:这只是一次性能优化，应用可用性不应被它绑架，失败只记警告，下次启动
// 重试。不得挂进 sqliteDSN 的 _pragma —— 那对每个连接都无条件执行，首次转换失败会让
// 那一次连接建立本身失败，进而阻断启动。
func convertToWAL(ctx context.Context, gormDB *gorm.DB) {
	var mode string
	if err := gormDB.Raw("PRAGMA journal_mode=WAL").Scan(&mode).Error; err != nil {
		logger.Ctx(ctx).Warn("bootstrap.convertToWAL: journal mode conversion failed, continuing with current mode", zap.Error(err))
		return
	}
	logger.Ctx(ctx).Info("bootstrap.convertToWAL: journal mode converted", zap.String("mode", mode))
}

func defaultConfigValues(logsDir, dbPath string) map[string]interface{} {
	// 启动默认 info 级别；debug 日志改由「设置 → 版本 & 更新」开关在 Init 末尾按
	// app_settings.logger.debug_enabled 热重载（见 applyDebugLoggingOnBoot）。
	return map[string]interface{}{
		"env":    string(appEnv()),
		"debug":  false,
		"source": "file",
		"logger": map[string]interface{}{
			"level":          "info",
			"disableConsole": false,
			"logFile": map[string]interface{}{
				"enable":        true,
				"filename":      filepath.Join(logsDir, "agentre.log"),
				"errorFilename": filepath.Join(logsDir, "error.log"),
			},
		},
		"db": map[string]interface{}{
			"driver": string(db.SQLite),
			"dsn":    dbPath,
			"debug":  false,
		},
	}
}

func appEnv() configs.Env {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AGENTRE_ENV"))) {
	case string(configs.PROD):
		return configs.PROD
	case string(configs.PRE):
		return configs.PRE
	case string(configs.TEST):
		return configs.TEST
	default:
		return configs.DEV
	}
}

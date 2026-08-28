// Package app contains the Wails binding layer. Methods on App are exposed to
// the frontend via wails generation under frontend/wailsjs/go/app/App.*.
// Each method should remain a thin pass-through to the corresponding service
// singleton — keep business logic in internal/service/<domain>_svc/.
package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agentre-hub/agentre/internal/bootstrap"
	"github.com/agentre-hub/agentre/internal/buildinfo"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/piagent"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/service/agent_svc"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/data_svc"
	"github.com/agentre-hub/agentre/internal/service/department_svc"
	"github.com/agentre-hub/agentre/internal/service/hook_svc"
	"github.com/agentre-hub/agentre/internal/service/hooktool_svc"
	"github.com/agentre-hub/agentre/internal/service/orgtool_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	watcher "github.com/agentre-hub/agentre/internal/service/remote_device_watcher_svc"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
	"github.com/agentre-hub/agentre/internal/service/subagent_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc"

	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

// App is the Wails binding root. Each exported method becomes a frontend RPC.
// RuntimeMode describes whether native window management is allowed. Unknown is
// deliberately distinct so frontend callers can fail closed.
type RuntimeMode string

const (
	RuntimeModeUnknown     RuntimeMode = "unknown"
	RuntimeModeInteractive RuntimeMode = "interactive"
	RuntimeModeHeadless    RuntimeMode = "headless"
)

type App struct {
	ctx              context.Context
	runtimeMode      RuntimeMode
	hookPollerCancel context.CancelFunc
	peerMu           sync.Mutex
	peerCancel       context.CancelFunc
	peerDone         chan struct{}
	// peerRestartMu 串行化「停旧登记 + 建新登记」这一对操作，见 restartInboundPeer。
	peerRestartMu sync.Mutex
	ccUsageStop   func()
	terminalSvc   *terminal_svc.Service

	// quitConfirmed 标记本次退出已被用户确认(或自动更新重启),OnBeforeClose 见到即放行。
	quitConfirmed   atomic.Bool
	finalQuit       func(context.Context)
	finalQuitOnce   sync.Once
	forceExit       func(int)
	forcedExitDelay time.Duration

	shutdownCleanup func(context.Context)
	shutdownOnce    sync.Once

	lastImportPath   string
	lastImportPathMu sync.Mutex
}

// AppInfo contains build and runtime metadata exposed to the frontend.
type AppInfo struct {
	Name        string      `json:"name"`
	Version     string      `json:"version"`
	Commit      string      `json:"commit"`
	Env         string      `json:"env"`
	RuntimeMode RuntimeMode `json:"runtimeMode"`
}

// NewApp creates a new App application struct. Omitted or invalid modes remain
// unknown so native window management stays fail-closed.
func NewApp(modes ...RuntimeMode) *App {
	mode := RuntimeModeUnknown
	if len(modes) == 1 && (modes[0] == RuntimeModeInteractive || modes[0] == RuntimeModeHeadless) {
		mode = modes[0]
	}
	a := &App{
		runtimeMode: mode,
		finalQuit: func(ctx context.Context) {
			wailsruntime.Quit(ctx)
		},
		forceExit:       os.Exit,
		forcedExitDelay: 250 * time.Millisecond,
	}
	a.shutdownCleanup = a.cleanupResources
	return a
}

var resetStaleActiveSessions = bootstrap.ResetStaleActiveSessions

// Startup is wired to wails OnStartup. The context is saved so we can call
// the runtime methods.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.RegisterNotificationHandlers()
	a.resetStaleSessionsOnStartup(ctx)
	a.registerChatService()
	a.hookPollerCancel = hook_svc.StartScheduler(ctx)
	// 常驻 CLI 子进程的按时清扫:池的条数上限管不了「留多久」,一个开过一次就再没
	// 碰过的会话能把 CLI 连同它的 MCP server 挂到退出为止。
	agentruntime.DefaultCLISessionPool().StartIdleSweeper(ctx,
		agentruntime.DefaultIdleSessionTTL, cliSessionSweepInterval)

	// Server 联机：绑定 wails 事件源后启动 boot 协程（最长一次刷新）。
	server_svc.Server().SetEmitter(func(payload any) {
		wailsruntime.EventsEmit(a.ctx, "server.state", payload)
		a.onServerStateEvent(payload)
	})
	bootstrap.ServerBoot(context.Background())
	a.startInboundPeer(context.Background())
	// 出站对端客户端（R18/R19）：接线后前端才能把对话派到另一台桌面端 / 接入其会话。
	a.registerPeerService()
	// 工作区多端同步的下行轮询（R3：30 秒一轮）。未登录时每一轮都是空操作（R12）。
	//
	// 落地什么要喊出来：项目树没有任何推送通道，另一台设备同步过来的项目此前靠
	// 项目页那条 1 秒轮询才现身，轮询随单一会话索引一起删掉之后就只能干等下一次
	// 别的交互。emitter 必须在 SyncBoot 之前绑好，否则第一轮下行没有听众。
	if s := sync_svc.Default(); s != nil {
		s.SetEmitter(func(kinds []string) {
			wailsruntime.EventsEmit(a.ctx, sync_svc.AppliedEvent, kinds)
		})
	}
	bootstrap.SyncBoot(context.Background())

	// Remote device watcher：注入 wails 事件 emitter,Boot 拉起所有 ACTIVE 设备的 watcher。
	// 顺带把 device online/offline 事件接到 cc_usage_svc(动态起/停 per-device 配额 ticker)。
	remoteDeviceEmit := watcher.EmitterFunc(func(p watcher.StateEvent) {
		wailsruntime.EventsEmit(a.ctx, watcher.EventName, p)
		a.onRemoteDeviceState(p.ID, p.Online)
		a.onRemoteDeviceOnline(p.ID, p.Online)
	})
	bootstrap.InitRemoteDeviceWatcher(context.Background(), remoteDeviceEmit)
	bootstrap.RemoteDeviceWatcherBoot(context.Background())

	// 远端会话补齐:按 chat_sessions.exec_device_id 连回各台配对 daemon,把桌面端
	// 离线期间产生的转录与待决策接回来(「关掉 App,下次打开看到这段时间的全部内容」)。
	// 必须在 InitRemoteDevice 之后(要连接池)、且脱离 startup ctx 起 goroutine ——
	// 它会逐台 daemon 拨号,不能把窗口显示卡在这上面。
	//nolint:gosec // G118: startup catch-up deliberately outlives the startup context
	go a.catchUpRemoteSessions()

	// Claude Code OAuth usage HUD:启动后台 60s 轮询,wails event "cc_usage:update"
	// 推送给前端 QuotaMeter。Shutdown 时停所有 ticker。
	a.ccUsageStop = a.startCCUsage()

	a.terminalSvc = newTerminalService(a.ctx)

	// 更新检查:5s 后首次判定,随后常驻 tick 到 a.ctx 结束。
	go a.startAutoUpdateCheck()

	logger.Default().Info("app startup", zap.Any("info", a.Info()))
}

// catchUpRemoteSessions 补齐所有记录了远端执行位置的会话。失败只记日志:某台 daemon
// 关着是常态,不该影响 App 启动。
func (a *App) catchUpRemoteSessions() {
	svc := chat_svc.Chat()
	if svc == nil {
		return
	}
	if err := svc.CatchUpRemoteSessions(context.Background()); err != nil {
		logger.Default().Warn("app.Startup: catch up remote sessions", zap.Error(err))
	}
}

// onRemoteDeviceOnline 某台配对 daemon 重新上线时,补上启动那会儿没做成的补齐。
//
// catchUpRemoteSessions 只在 Startup 跑一次:开机自启早于 Wi-Fi/VPN 就绪、或那台 daemon
// 恰好在重启时,那一次逐台拨号必然失败,而设备重新上线时没有任何东西重跑它 —— 该设备上
// 的会话在本进程内就再也不会被补齐或接管。设备监视本来就在报上线/下线(配额 ticker 已经
// 挂在同一条信号上),补齐挂上去即可,不必另起一个轮询。
//
// 补成过的设备再上线是 no-op(判据在 chat_svc 那侧),所以设备抖动不会变成 attach 风暴。
func (a *App) onRemoteDeviceOnline(id int64, online bool) {
	if !online {
		return
	}
	svc := chat_svc.Chat()
	if svc == nil {
		return
	}
	// 脱离 watcher 的事件回调:补齐要逐条拨号 + 走完补齐三步,不能把回调按在这上面。
	go func() {
		if err := svc.CatchUpRemoteDevice(context.Background(), id); err != nil {
			logger.Default().Warn("app: catch up remote device", zap.Int64("deviceId", id), zap.Error(err))
		}
	}()
}

func (a *App) resetStaleSessionsOnStartup(ctx context.Context) {
	if err := resetStaleActiveSessions(ctx); err != nil {
		logger.Ctx(ctx).Warn("app.Startup: reset stale active sessions", zap.Error(err))
	}
}

// Shutdown is wired to wails OnShutdown.
func (a *App) Shutdown(ctx context.Context) {
	quitConfirmed := a.quitConfirmed.Load()
	logger.Ctx(ctx).Info("app.Shutdown: shutdown started",
		zap.Bool("quitConfirmed", quitConfirmed))
	a.shutdownOnce.Do(func() {
		logger.Ctx(ctx).Info("app.Shutdown: cleanup starting",
			zap.Bool("quitConfirmed", quitConfirmed))
		// Wails 接受退出后本方法不等资源清理(卡住的外部进程不得拖住退出),所以常驻 CLI
		// 子进程必须在这里同步收掉:剩下的清理跑在 goroutine 里,而桌面进程紧接着
		// 退出,那些 goroutine 连同优雅关闭一起消失 —— CLI 自带进程组、不会被连坐,
		// 直接变成孤儿。KillAll 是一串信号投递,不等任何一个子进程退出。
		agentruntime.DefaultCLISessionPool().KillAll()
		// 不是每个后端都把进程放在池里:pi 是每轮一个进程、不进池的,只扫池的收尾
		// 够不着它。给一个很短的上界 —— 它内部是「发信号 → 宽限 → 杀整棵树」,
		// 上界到了就走硬杀那一支。
		killCtx, cancelKill := context.WithTimeout(context.Background(), cliSessionQuitKillTimeout)
		agentruntime.CloseAllSessionsEverywhere(killCtx)
		cancelKill()
		go func() {
			logger.Ctx(ctx).Info("app.Shutdown: shutdown cleanup scheduled")
			a.shutdownCleanup(context.WithoutCancel(ctx))
			logger.Ctx(ctx).Info("app.Shutdown: shutdown cleanup completed")
		}()
	})
	logger.Ctx(ctx).Info("app.Shutdown: shutdown returning")
}

// cleanupResources starts best-effort resource cleanup after Wails has accepted
// the quit. Shutdown deliberately does not wait for this method: a stuck
// external process or connection must not keep the desktop process alive.

// cliSessionReleaseTimeout 是同步收尾常驻 CLI 子进程的上界。
const cliSessionReleaseTimeout = 3 * time.Second

// cliSessionSweepInterval 是 idle CLI 会话清扫的巡检间隔。
const cliSessionSweepInterval = time.Minute

// cliSessionQuitKillTimeout 是确认退出那条路上收掉池外子进程的上界。Shutdown 不得
// 拖住退出(见 app_quit_test 的 100ms 契约),所以这里只留一个很短的窗口。
const cliSessionQuitKillTimeout = 50 * time.Millisecond

func (a *App) cleanupResources(ctx context.Context) {
	a.stopInboundPeer(ctx)
	// 关闭全部出站对端中继连接（R19：本端退出即结束接入，对端会话不受影响）。
	if err := a.PeerClose(); err != nil {
		logger.Ctx(ctx).Warn("app.Shutdown: close outbound peer relay", zap.Error(err))
	}
	if a.hookPollerCancel != nil {
		a.hookPollerCancel()
		a.hookPollerCancel = nil
	}
	if a.ccUsageStop != nil {
		a.ccUsageStop()
		a.ccUsageStop = nil
	}
	// 关停 remote device watcher：让长连守护 goroutine 全部退出。
	if w := watcher.Default(); w != nil {
		w.StopAll()
	}
	// 关闭 device-shared ConnPool:guarantee 所有活 entry 的 client.Close 被调,
	// chat_svc / agent_backend_svc 持有的 lease 自动失效。
	if rd := remote_device_svc.Default(); rd != nil {
		if p := rd.Pool(); p != nil {
			if err := p.Close(); err != nil {
				logger.Ctx(ctx).Warn("conn pool close", zap.Error(err))
			}
		}
	}
	// 收尾常驻 CLI 子进程。这条路(未确认退出 / 没有活跃会话)是同步跑的,所以给一个
	// 明确的上界:上界内优雅关闭,到点还没收尾的当场硬杀,不留孤儿。确认退出那条路
	// 已经在 Shutdown 里先 KillAll 过一遍,这里是 no-op。
	closeCtx, cancelClose := context.WithTimeout(context.WithoutCancel(ctx), cliSessionReleaseTimeout)
	defer cancelClose()
	if err := agentruntime.DefaultCLISessionPool().CloseAll(closeCtx); err != nil {
		logger.Ctx(ctx).Warn("app.Shutdown: release CLI sessions", zap.Error(err))
	}
	// 池外的后端(pi 每轮一个进程,不进池)由注册表广播收尾。
	agentruntime.CloseAllSessionsEverywhere(closeCtx)
	if a.terminalSvc != nil {
		a.terminalSvc.Shutdown()
	}
	logger.Ctx(ctx).Info("app shutdown")
}

// OnBeforeClose is wired to wails OnBeforeClose; it fires on every quit path
// (macOS cmd+Q / menu, Windows close button / Alt+F4, programmatic Quit).
// Returning true prevents the quit. If active sessions exist it emits
// "app:quit-blocked" so the frontend can show a confirmation dialog.
func (a *App) OnBeforeClose(ctx context.Context) (prevent bool) {
	confirmed := a.quitConfirmed.Load()
	logger.Ctx(ctx).Info("app.OnBeforeClose: close requested", zap.Bool("quitConfirmed", confirmed))
	prevent = shouldPreventQuit(ctx, confirmed,
		countActiveSessions,
		func(n int) {
			logger.Ctx(ctx).Info("app.OnBeforeClose: quit blocked by active sessions", zap.Int("count", n))
			wailsruntime.EventsEmit(a.ctx, "app:quit-blocked", map[string]any{"count": n})
		})
	logger.Ctx(ctx).Info("app.OnBeforeClose: close decision", zap.Bool("prevent", prevent))
	return prevent
}

// countActiveSessions reports the running/waiting session count for the quit gate.
// Wails runs OnStartup in a goroutine concurrent with the window run loop (darwin /
// windows / linux all do), so OnBeforeClose can fire before Startup wires the chat
// service — in that window chat_svc.Chat() is still nil and there cannot yet be any
// session the user would lose. Treat the unregistered service as zero rather than
// dereferencing a nil interface: fail-open, never panic on the quit path.
func countActiveSessions(ctx context.Context) (int, error) {
	chat := chat_svc.Chat()
	if chat == nil {
		return 0, nil
	}
	return chat.CountActiveSessions(ctx)
}

// shouldPreventQuit decides whether to block the quit and notify the user.
//   - confirmed (user pressed "quit anyway", or auto-update restart) → allow
//   - count errors or is 0 → allow (fail-open: a count failure must never trap
//     the user in an app they cannot quit)
//   - count > 0 → emit the count and prevent
func shouldPreventQuit(ctx context.Context, confirmed bool,
	count func(context.Context) (int, error), emit func(n int)) bool {
	if confirmed {
		return false
	}
	n, err := count(ctx)
	if err != nil {
		logger.Ctx(ctx).Warn("app.shouldPreventQuit: count active sessions", zap.Error(err))
		return false
	}
	if n == 0 {
		return false
	}
	emit(n)
	return true
}

// ConfirmQuit is called from the frontend when the user confirms quitting with
// active sessions. It marks the quit as confirmed and triggers the real quit,
// which re-enters OnBeforeClose and is allowed through.
func (a *App) ConfirmQuit() {
	logger.Default().Info("app.ConfirmQuit: confirmed quit requested")
	a.quitConfirmed.Store(true)
	a.finalQuitOnce.Do(func() {
		go func() {
			time.Sleep(a.forcedExitDelay)
			logger.Default().Warn("app.ConfirmQuit: forced exit fallback", zap.Int("code", 0))
			a.forceExit(0)
		}()
		go func() {
			logger.Default().Info("app.ConfirmQuit: native quit started")
			a.finalQuit(a.ctx)
			logger.Default().Info("app.ConfirmQuit: native quit returned")
		}()
	})
}

// registerChatService wires the chat service singleton with a real wails-runtime
// emitter so chat_svc.Send-triggered chunks reach the frontend via EventsEmit.
func (a *App) registerChatService() {
	emitter := chat_svc.EmitterFunc(func(_ context.Context, name string, payload any) {
		wailsruntime.EventsEmit(a.ctx, name, payload)
	})
	// 合帧包一层:EventsEmit 每调一次就是一次 json.Marshal + 一次主线程 webview
	// evaluateJavaScript,而流式文本是一个 token 一条。合帧只在高频时生效,不改变
	// 事件的相对顺序(见 chat_svc.NewCoalescingEmitter)。
	// 只包真正接 Wails 的这一处 —— 注入假 emitter 的单测仍逐条看到原始事件。
	chat_svc.RegisterChat(chat_svc.NewChat(chat_svc.NewCoalescingEmitter(emitter)))

	// 注入 orgtool_svc 依赖:必须在 RegisterChat 之后执行,因为 chat_svc.Chat()
	// 在此之前为 nil(chat 服务是懒注册的)。
	// department_svc.Department() 同时满足 OrgQuery + DeptCommand 两个窄接口。
	orgtool_svc.Default().RegisterDeps(
		department_svc.Department(), department_svc.Department(),
		agent_svc.Agent(), agent_repo.Agent(), chat_svc.Chat(),
	)

	// subagent_svc 同样需 chat_svc.Chat() 非 nil(起子 agent 轮),故也在 RegisterChat 之后接线。
	// agent_repo.Agent() 直接满足 AgentGateway(Find/FindByName/List)。
	subagent_svc.Default().RegisterDeps(agent_repo.Agent(), subagent_svc.ChatSvcGateway())

	// hooktool_svc 依赖:hook_svc.Hook() 满足 HookService;agent_repo.Agent() 满足 AgentLookup;
	// chat_svc.Chat() 满足 ApprovalGateway。须在 RegisterChat 之后(chat_svc.Chat() 非 nil)。
	hooktool_svc.Default().RegisterDeps(hook_svc.Hook(), agent_repo.Agent(), chat_svc.Chat())
}

// Greet returns a greeting for the given name.
func (a *App) Greet(name string) string {
	return fmt.Sprintf("Hello %s, It's show time!", name)
}

// Info returns app build and runtime metadata.
func (a *App) Info() AppInfo {
	info := AppInfo{
		Name:        "agentre",
		Version:     configs.Version,
		Commit:      buildinfo.ShortCommitID(),
		Env:         string(configs.DEV),
		RuntimeMode: a.runtimeMode,
	}

	if runtime := bootstrap.Default(); runtime != nil && runtime.Config() != nil {
		cfg := runtime.Config()
		info.Name = cfg.AppName
		info.Version = cfg.Version
		info.Env = string(cfg.Env)
	}

	return info
}

// OpenExternalURL opens url in the user's system browser. The frontend can't use
// window.open() — Wails's embedded webview silently drops it — so any "open in
// browser" action from JS must go through this binding.
func (a *App) OpenExternalURL(url string) {
	wailsruntime.BrowserOpenURL(a.ctx, url)
}

// SelectDirectory 弹出系统目录选择器并返回用户选中的绝对路径；用户取消时返回空串。
//
// 用于新建项目模态 / 设置抽屉的「浏览…」按钮。沿用 wails 自带 runtime API，
// 不引入额外 CGO 依赖；macOS / Windows / Linux 行为一致。
func (a *App) SelectDirectory(title string) (string, error) {
	if strings.TrimSpace(title) == "" {
		title = "选择项目目录"
	}
	return wailsruntime.OpenDirectoryDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title:                title,
		CanCreateDirectories: true,
	})
}

// ExportFileResult 是 ExportData 的返回值。
type ExportFileResult struct {
	Path     string         `json:"path"`
	Canceled bool           `json:"canceled"`
	Summary  map[string]int `json:"summary,omitempty"`
}

// ExportData 收集所选 scopes，弹保存对话框，写入用户选择的路径。
func (a *App) ExportData(req data_svc.ExportRequest) (*ExportFileResult, error) {
	ctx := a.ctx
	res, err := data_svc.Default().Export(ctx, &req)
	if err != nil {
		return nil, err
	}
	defaultName := "agentre-export-" + time.Now().Format("20060102-150405") + ".json"
	path, err := wailsruntime.SaveFileDialog(ctx, wailsruntime.SaveDialogOptions{
		Title:           "导出 Agentre 数据",
		DefaultFilename: defaultName,
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "JSON (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" {
		return &ExportFileResult{Canceled: true}, nil
	}
	if err := os.WriteFile(path, res.JSON, 0o600); err != nil {
		logger.Ctx(ctx).Error("app.ExportData: write file failed", zap.String("path", path), zap.Error(err))
		return nil, i18n.NewError(ctx, code.DataExportWriteFailed)
	}
	return &ExportFileResult{Path: path, Summary: res.Summary}, nil
}

// PreviewImportData 弹打开对话框，读文件，缓存 path，返回 preview。
// 用户取消 → 返回 (nil, nil)。
func (a *App) PreviewImportData() (*data_svc.ImportPreview, error) {
	ctx := a.ctx
	path, err := wailsruntime.OpenFileDialog(ctx, wailsruntime.OpenDialogOptions{
		Title:   "选择 Agentre 导出文件",
		Filters: []wailsruntime.FileFilter{{DisplayName: "JSON (*.json)", Pattern: "*.json"}},
	})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	//nolint:gosec // G304: path is user-selected via OS open dialog
	raw, err := os.ReadFile(path)
	if err != nil {
		logger.Ctx(ctx).Warn("app.PreviewImportData: read failed", zap.String("path", path), zap.Error(err))
		return nil, i18n.NewError(ctx, code.DataImportReadFailed)
	}
	pv, err := data_svc.Default().PreviewImport(ctx, raw)
	if err != nil {
		return nil, err
	}
	a.lastImportPathMu.Lock()
	a.lastImportPath = path
	a.lastImportPathMu.Unlock()
	return pv, nil
}

// ApplyImportFrontendRequest 是 ApplyImportData 的请求体。
type ApplyImportFrontendRequest struct {
	Actions          map[string]data_svc.ItemAction `json:"actions"`
	FallbackStrategy data_svc.ItemAction            `json:"fallbackStrategy"`
}

// ApplyImportData 读取缓存 path，重载文件，调用 ApplyImport。
func (a *App) ApplyImportData(req ApplyImportFrontendRequest) (*data_svc.ApplyImportResult, error) {
	ctx := a.ctx
	a.lastImportPathMu.Lock()
	path := a.lastImportPath
	a.lastImportPathMu.Unlock()
	if strings.TrimSpace(path) == "" {
		return nil, i18n.NewError(ctx, code.DataImportReadFailed)
	}
	//nolint:gosec // G304: path was previously cached from a user-selected OS open dialog
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, i18n.NewError(ctx, code.DataImportReadFailed)
	}
	return data_svc.Default().ApplyImport(ctx, &data_svc.ApplyImportRequest{
		Raw:              raw,
		Actions:          req.Actions,
		FallbackStrategy: req.FallbackStrategy,
	})
}

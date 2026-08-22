// Package claudecode 是 Anthropic Claude Code CLI 的 agent runtime,emit sealed
// agentruntime.Event。本包 init() 时把 *Runtime 注册到 agentruntime.RuntimeFor。
package claudecode

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-ai/agentre/internal/pkg/httpgateway"
	"github.com/agentre-ai/agentre/pkg/claudecode"
)

// defaultRuntime 是包级单例,init() 时登记到 agentruntime.RuntimeFor 注册表。
var defaultRuntime = NewWithPool(agentruntime.DefaultCLISessionPool())

func init() {
	agentruntime.RegisterRuntime(agent_backend_entity.TypeClaudeCode, defaultRuntime)
}

// Default 返回包级默认 *Runtime,供 bootstrap 注入 SteerInbox / app shutdown
// 调 CloseAllSessions / chat_svc 删 session 调 CloseSession 等。
func Default() *Runtime { return defaultRuntime }

// sessionCacheCap = 8:与顶层 claudecode.go.claudeSessionCacheCap 一致。
const sessionCacheCap = 8

// defaultStartupFrameTimeout 是 startup 看门狗的默认阈值:一轮 turn 起步后, 健康的
// CLI 会在数秒内吐首帧(system.init);若这么久还一帧都没有, 判定子进程卡死(典型:
// 群成员轮 CLI 卡在 MCP 初始化连不上 gateway)→ 硬杀子进程让本轮以错误收尾。取值
// 宽松到不会误杀冷启动慢的健康 turn, 又有界到能从无界挂起里恢复。
const defaultStartupFrameTimeout = 120 * time.Second

// errStartupTimeout 是 startup 看门狗杀掉「起步即卡死」子进程后写入 RunResult.StopErr
// 的哨兵错误, 让 chat_svc 把该 turn 收成 error 而非永久 running。
var errStartupTimeout = errors.New("agentruntime/runtimes/claudecode: turn produced no frame within startup timeout (subprocess likely wedged, e.g. MCP init)")

// Runtime claudecode runtime 实现。
type Runtime struct {
	// spawnLocks 按 session key 分桶串行化 acquireSession 的 get-or-spawn,只防同一
	// session 并发首轮 double-spawn。
	//
	// **绝不能退回单把全局锁**:acquireSession 在锁内做阻塞子进程操作(spawn + 同步
	// SetPermissionMode);某个 session 的 CLI 启动期挂起(实测:带 --mcp-config 的子轮
	// 卡在 MCP 初始化)会一直占着全局锁 → 其它**所有** session 的 turn 全
	// 堵在 acquireSession 的锁上,整个 claudecode runtime 宕掉(单聊不输出/停不掉/再发
	// 报 in-flight)。回归见 TestRun_BlockedSpawnDoesNotWedgeOtherSessions。
	//
	// 锁条目按 key 惰性创建后不回收:每个 session key 一把 *sync.Mutex(指针大小),
	// 数量随会话量有界增长,内存可忽略;删除会与并发 LoadOrStore 产生「同一 key 两把锁」
	// 的 double-spawn 竞态,故不做。
	spawnLocks sync.Map // key(string) → *sync.Mutex
	cache      *agentruntime.CLISessionPool
	steer      *httpgateway.SteerInbox
	// hookCLIPath 是注册进 PostToolUse hook 的可执行文件路径;bootstrap 注入
	// <AppDataDir>/bin/agrctl。为空时回落 os.Executable()(供 agentred 守护进程——它自带
	// claudecode 子命令)。见 hookBin。
	hookCLIPath string
	// startupTimeout 是 startup 看门狗阈值;NewWithPool 设默认值, 单测覆写成毫秒级。
	startupTimeout time.Duration
}

// spawnLockFor 返回某 session key 专属的 get-or-spawn 锁(惰性创建)。
func (r *Runtime) spawnLockFor(key string) *sync.Mutex {
	v, _ := r.spawnLocks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// New 默认 8 上限 LRU。
func New() *Runtime {
	return NewWithCap(sessionCacheCap)
}

// NewWithCap 仅供测试覆盖 LRU 触发场景(capacity=2 之类)。
func NewWithCap(capacity int) *Runtime {
	return NewWithPool(agentruntime.NewCLISessionPool(capacity))
}

func NewWithPool(pool *agentruntime.CLISessionPool) *Runtime {
	if pool == nil {
		pool = agentruntime.NewCLISessionPool(sessionCacheCap)
	}
	return &Runtime{cache: pool, startupTimeout: defaultStartupFrameTimeout}
}

// SetSteerInbox 由 bootstrap 在 gateway.Start() 后注入。
func (r *Runtime) SetSteerInbox(ib *httpgateway.SteerInbox) { r.steer = ib }

// sessionKey 把 chat session ID 翻成 cache key。
func sessionKey(id int64) string { return strconv.FormatInt(id, 10) }

// Capabilities 返回 claudecode runtime 的能力矩阵 + permission mode 元数据。
func (r *Runtime) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Set: map[capability.Capability]bool{
			capability.CapSteer:       true,
			capability.CapCancelSteer: true,
			capability.CapDrainSteer:  true,
			capability.CapAbort:       true,
			// 停单个后台任务/子 agent(run_in_background)—— 实现 BackgroundTaskStopper,
			// 下发 control_request{stop_task};前端据此在后台任务面板/AgentSpawn 卡片显示停止按钮。
			capability.CapStopBackgroundTask: true,
			capability.CapSetPermission:      true,
			capability.CapAnswerUserAsk:      true,
			capability.CapToolPermission:     true,
			capability.CapForkSession:        true,
			// translator.EventInit 路径用 llmcatalog 兜底 emit ContextWindowUpdated;
			// Claude Code SDK 协议本身不报窗口,这里靠 catalog 给前端 turn 内总量。
			capability.CapReportContextWindow: true,
			// user frame 携带 base64 image content block(CLI stream-json 原生支持);
			// extractImages 从 RunRequest.UserBlocks 抽 inline 图片经 handle.Stream 透传。
			capability.CapImageInput: true,
			// RunRequest.MCPServers 注入支持:claudecode CLI 接受 --mcp-config 传入
			// 额外 MCP tool 服务器;org/subagent/hook 工具注入消费此 cap。
			capability.CapMCPTools: true,
			// CLI 在 run_in_background Bash 任务完成后自主跑续轮;实现 AutonomousTurnSource。
			capability.CapAutonomousTurn: true,
			// 接受 RunRequest.EnabledPlugins,spawn 时渲进 --settings 的 enabledPlugins。
			capability.CapSkills: true,
		},
		PermissionModeMeta: capability.PermissionModeMeta{
			AllowedModes:         []string{"default", "acceptEdits", "plan", "bypassPermissions"},
			DefaultMode:          "acceptEdits",
			SwitchableDuringTurn: true,
			Order:                []string{"default", "acceptEdits", "plan", "bypassPermissions"},
			// 空串 = chat_svc 不显式落库,pkg/claudecode args.go:86 再兜底成 acceptEdits。
			LaunchDefaultMode: "",
		},
	}
}

// Steer 把 (queuedID, text) 投入到当前 turn 对应的 claude session UUID 的
// SteerInbox。语义同顶层 claudecode.go.Steer。
func (r *Runtime) Steer(ctx context.Context, sessionID int64, queuedID, text string) error {
	v, ok := r.cache.Get(sessionKey(sessionID))
	if !ok {
		return agentruntime.ErrNoActiveTurn
	}
	a := v.(*claudeActive)
	if !a.inTurn.Load() {
		return agentruntime.ErrNoActiveTurn
	}
	if r.steer == nil {
		return errors.New("agentruntime/runtimes/claudecode: steer inbox not configured")
	}
	r.steer.Push(a.sessionUUID, queuedID, text)
	// 诊断 steer 投递路径: 消息 push 进 inbox 的 key(inboxKey)。要和
	// httpgateway.serveHookInbox 的 sid 完全一致, hook 才能在工具边界 drain 到。
	// 不一致 → mid-turn 永远 drain 不到 → 只能等 turn 末 DrainPending。
	logger.Ctx(ctx).Debug("claudecode runtime: steer enqueued to inbox",
		zap.Int64("sessionID", sessionID),
		zap.String("inboxKey", a.sessionUUID),
		zap.String("queuedID", queuedID))
	return nil
}

// CancelSteer 撤回一条尚未被 hook 拉走的排队消息。语义同顶层 CancelSteer。
func (r *Runtime) CancelSteer(_ context.Context, sessionID int64, queuedID string) ([]string, error) {
	v, ok := r.cache.Get(sessionKey(sessionID))
	if !ok {
		return nil, agentruntime.ErrNoActiveTurn
	}
	a := v.(*claudeActive)
	if r.steer == nil || a.sessionUUID == "" {
		return nil, agentruntime.ErrNoActiveTurn
	}
	if queuedID == "" {
		items := r.steer.Drain(a.sessionUUID)
		out := make([]string, 0, len(items))
		for _, it := range items {
			out = append(out, it.ID)
		}
		return out, nil
	}
	if r.steer.Remove(a.sessionUUID, queuedID) {
		return []string{queuedID}, nil
	}
	return nil, agentruntime.ErrSteerNotFound
}

// markIdle Run() 的 drain 完成后调用:清除 inTurn 标记。语义同顶层 markIdle。
func (r *Runtime) markIdle(sessionID int64) {
	if sessionID <= 0 {
		return
	}
	key := sessionKey(sessionID)
	v, ok := r.cache.Get(key)
	if ok {
		v.(*claudeActive).inTurn.Store(false)
	}
	r.cache.MarkIdle(key)
}

// DrainPending 取走未消费的排队消息并清空,返非空时把 inTurn 重新置为 true
// (关 markIdle→acquireSession 之间的 race 窗口)。语义同顶层 DrainPending。
func (r *Runtime) DrainPending(ctx context.Context, sessionID int64) []agentruntime.ConsumedSteer {
	if sessionID <= 0 || r.steer == nil {
		return nil
	}
	v, ok := r.cache.Get(sessionKey(sessionID))
	if !ok {
		return nil
	}
	a := v.(*claudeActive)
	if a.sessionUUID == "" {
		return nil
	}
	out := consumedSteersFromInbox(r.steer.Drain(a.sessionUUID))
	if len(out) > 0 {
		a.inTurn.Store(true)
		// 诊断 steer "等整轮才发出去" 的烟枪: 走到这里 = 消息整轮都没被 PostToolUse
		// hook drain 到(纯文字轮 / 子 agent 期 / 末工具之后发), 直到 turn 收尾才被
		// 扫出来当下一轮 prompt。count>0 出现一次 = 用户体感的那次"延迟"。
		logger.Ctx(ctx).Debug("claudecode runtime: steer drained at TURN END (was not delivered mid-turn)",
			zap.Int64("sessionID", sessionID),
			zap.String("inboxKey", a.sessionUUID),
			zap.Int("count", len(out)))
	}
	return out
}

// Abort 软中断当前 turn。语义同顶层 Abort:「中断该会话当前活跃的那一轮」——
// 活跃轮既包括用户轮(inTurn)也包括带外轮(outOfBandActive:自主续轮 / 后台
// subagent 活动轮独占帧流期间 inTurn 仍是 false)。两者都不活跃时才是真的
// 无轮可中断,返回 ErrNoActiveTurn。
//
// turnToken 语义(决策 1):0 = 中断当前活跃轮(等价旧行为);非 0 = 仅当该轮仍是
// 当前活跃轮时才中断,否则 stale no-op、不触碰其它轮。成功中断时上报被中断轮的
// 类型(curTurnKind)。
func (r *Runtime) Abort(ctx context.Context, sessionID int64, turnToken uint64) (agentruntime.AbortOutcome, error) {
	v, ok := r.cache.Get(sessionKey(sessionID))
	if !ok {
		return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindNone}, agentruntime.ErrNoActiveTurn
	}
	a := v.(*claudeActive)
	if !a.inTurn.Load() && !a.outOfBandActive() {
		return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindNone}, agentruntime.ErrNoActiveTurn
	}
	if turnToken != 0 && a.curTurnToken.Load() != turnToken {
		// stale:该轮已不是当前活跃轮,no-op,不触碰任何其它轮。
		return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindNone}, nil
	}
	if r.steer != nil && a.sessionUUID != "" {
		_ = r.steer.Drain(a.sessionUUID)
	}
	if a.handle != nil {
		if err := a.handle.Interrupt(ctx); err != nil {
			if errors.Is(err, claudecode.ErrInterruptPending) {
				// 中断已下发、ack 异步到（CLI 卡在别的处理上，interruptAckBound 内没回执）。
				// **不** Close、**不** 逐出缓存 —— 帧已写、在途，readLoop 追平后 CLI 会
				// 发 result 帧让本轮自然收尾。只有真错误（写帧失败 / session closed /
				// CLI 拒绝）才走下方 Close+evict 兜底。
				return agentruntime.AbortOutcome{TurnKind: a.currentTurnKind()}, nil
			}
			_ = a.handle.Close(ctx)
			r.cache.Remove(sessionKey(sessionID))
			return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindNone}, err
		}
	}
	return agentruntime.AbortOutcome{TurnKind: a.currentTurnKind()}, nil
}

// StopBackgroundTask 实现 BackgroundTaskStopper：停掉某个后台任务/子 agent(按 CLI
// task_id)。与 Abort 不同,**不校验 inTurn** —— 后台任务在 turn 结束后仍存活,只要
// 子进程还在 LRU 缓存里(还没 evict)就下发 control_request{stop_task}。子进程已 evict
// (任务随之消失)→ ErrNoActiveTurn,由 chat_svc 当「任务已不在」处理。
func (r *Runtime) StopBackgroundTask(ctx context.Context, sessionID int64, taskID string) error {
	if taskID == "" {
		return fmt.Errorf("agentruntime/runtimes/claudecode: empty taskID")
	}
	v, ok := r.cache.Get(sessionKey(sessionID))
	if !ok {
		return agentruntime.ErrNoActiveTurn
	}
	a := v.(*claudeActive)
	if a.handle == nil {
		return agentruntime.ErrNoActiveTurn
	}
	return a.handle.StopTask(ctx, taskID)
}

// SetPermissionMode 实现 PermissionModeSetter。语义同顶层 SetPermissionMode。
//
// CLI 切换成功后必须同步 active.permissionMode 快照:CLI 的空闲 status 回显帧被
// demux reader 丢弃(pkg/claudecode session.go canStartUserTurn),复用进程的下一轮
// 也不重发 mode —— 不写这里,快照会停留在 spawn 时的值,handleControlRequest 的
// bypassPermissions 短路就会吞掉本应弹审批的 control_request。
func (r *Runtime) SetPermissionMode(ctx context.Context, sessionID int64, mode string) error {
	if sessionID <= 0 {
		return fmt.Errorf("agentruntime/runtimes/claudecode: invalid sessionID %d", sessionID)
	}
	v, ok := r.cache.Get(sessionKey(sessionID))
	if !ok {
		return agentruntime.ErrNoActiveTurn
	}
	a := v.(*claudeActive)
	if a.handle == nil {
		return agentruntime.ErrNoActiveTurn
	}
	if err := a.handle.SetPermissionMode(ctx, mode); err != nil {
		return err
	}
	a.setPermissionModeSnapshot(mode)
	return nil
}

// CloseSession 显式释放某个 chat session 的常驻进程。
func (r *Runtime) CloseSession(_ context.Context, sessionID int64) {
	if sessionID <= 0 {
		return
	}
	r.cache.Remove(sessionKey(sessionID))
}

// CloseAllSessions app shutdown 时调,关掉所有常驻 claude 子进程。
func (r *Runtime) CloseAllSessions(_ context.Context) {
	r.cache.RemoveAll()
}

// Run 启动一轮 claudecode CLI 发送。语义同顶层 claudecode.go.Run,emit 类型
// 从 RuntimeEvent 改为 sealed agentruntime.Event。
func (r *Runtime) Run(ctx context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	if req.Backend == nil {
		return nil, nil, fmt.Errorf("agentruntime/runtimes/claudecode: nil backend")
	}

	a, launchMode, err := r.acquireSession(ctx, req)
	if err != nil {
		logger.Ctx(ctx).Error("claudecode runtime: acquireSession failed",
			zap.Int64("sessionID", req.SessionID),
			zap.Int64("agentID", req.AgentID),
			zap.String("cwd", req.Cwd),
			zap.Error(err))
		return nil, nil, err
	}
	logger.Ctx(ctx).Info("claudecode runtime: turn starting",
		zap.Int64("sessionID", req.SessionID),
		zap.String("launchPermissionMode", launchMode),
		zap.String("providerSessionID", a.handle.ID()))

	stream, err := a.handle.Stream(ctx, req.UserText, extractImages(req.UserBlocks))
	if err != nil {
		logger.Ctx(ctx).Error("claudecode runtime: handle.Stream failed",
			zap.Int64("sessionID", req.SessionID),
			zap.String("providerSessionID", a.handle.ID()),
			zap.Error(err))
		a.inTurn.Store(false)
		if req.SessionID > 0 {
			r.cache.Remove(sessionKey(req.SessionID))
		} else {
			_ = a.Close(context.Background())
		}
		return nil, nil, err
	}

	out := make(chan agentruntime.Event, 32)
	token := a.nextTurnToken(agentruntime.TurnKindUser)
	result := &agentruntime.RunResult{
		ProviderSessionID:    a.handle.ID(),
		LaunchPermissionMode: launchMode,
		TurnToken:            token,
	}

	// startup 看门狗:turn 起步后 startupTimeout 内一帧都没有 → 判定子进程卡死(典型:
	// 群成员轮 CLI 卡在 MCP 初始化连不上 gateway),硬杀子进程让 drainStream 的
	// stream.Next() 拿到 EOF 解阻塞、本轮以 errStartupTimeout 收尾,而不是永久挂起。
	// 首帧到达即解除看门狗 —— 合法的长 turn(等审批 / 长工具调用都在首帧之后)不受影响;
	// 起步之后的中途卡死由 CLI 自身 MCP_TOOL_TIMEOUT / approvalTimeout 兜底,不归这里管。
	//
	// 带外轮豁免:本 session 上有自主续轮 / 后台 subagent 活动轮在流时,帧流被它占着,
	// 本轮的 user 帧虽已写进 stdin,但真 CLI 要等那一轮 result 之后才为它起 init ——
	// 「没帧」此刻是排队而非卡死。超时点命中带外轮时重新起表而不是杀,否则会硬杀一个
	// 健康子进程,同时腰斩正在流的那轮带外输出(sess-1950)。
	firstFrame := make(chan struct{})
	var firstFrameOnce sync.Once
	signalFirstFrame := func() { firstFrameOnce.Do(func() { close(firstFrame) }) }
	var startupKilled atomic.Bool
	if r.startupTimeout > 0 {
		timer := time.NewTimer(r.startupTimeout)
		go func() {
			defer timer.Stop()
			for {
				select {
				case <-firstFrame:
					return
				case <-timer.C:
					if a.outOfBandActive() {
						logger.Ctx(ctx).Info("claudecode runtime: startup watchdog deferred, out-of-band turn holds the frame stream",
							zap.Int64("sessionID", req.SessionID),
							zap.String("providerSessionID", a.handle.ID()),
							zap.Duration("startupTimeout", r.startupTimeout))
						timer.Reset(r.startupTimeout)
						continue
					}
					startupKilled.Store(true)
					logger.Ctx(ctx).Warn("claudecode runtime: no frame within startup timeout, killing wedged subprocess",
						zap.Int64("sessionID", req.SessionID),
						zap.String("providerSessionID", a.handle.ID()),
						zap.Duration("startupTimeout", r.startupTimeout))
					_ = a.handle.Kill(ctx)
					return
				}
			}
		}()
	}

	go func() {
		var (
			steerDrain  <-chan []httpgateway.SteerItem
			cancelDrain func()
			steerDone   = make(chan struct{})
		)
		if r.steer != nil && a.sessionUUID != "" {
			steerDrain, cancelDrain = r.steer.SubscribeDrain(a.sessionUUID)
		}
		if steerDrain != nil {
			go func() {
				defer close(steerDone)
				for items := range steerDrain {
					if len(items) == 0 {
						continue
					}
					out <- agentruntime.SteerConsumed{Steers: consumedSteersFromInbox(items)}
				}
			}()
		} else {
			close(steerDone)
		}

		drainStream(stream, out, result, a, signalFirstFrame)
		signalFirstFrame() // 兜底解除看门狗:0 帧自然结束(非卡死)时也要让它退出
		if cancelDrain != nil {
			cancelDrain()
		}
		<-steerDone
		if sid := stream.SessionID(); sid != "" {
			result.ProviderSessionID = sid
		}
		// startup 看门狗杀掉了卡死子进程:收成 errStartupTimeout(优先于下面的 0-frame
		// 兜底消息),并剔除缓存让下一轮重新 spawn 干净子进程。
		if startupKilled.Load() {
			if result.StopErr == nil {
				result.StopErr = errStartupTimeout
			}
			if req.SessionID > 0 {
				r.cache.Remove(sessionKey(req.SessionID))
			}
		}
		// 0-frame 兜底:CLI spawn 起来但立刻退出。语义同顶层 Run。
		if result.Usage == nil && result.StopErr == nil && ctx.Err() == nil {
			if exitErr := a.handle.ExitErr(); exitErr != nil {
				result.StopErr = exitErr
			} else {
				result.StopErr = errors.New("agentruntime/runtimes/claudecode: subprocess produced no events (likely exited on startup)")
			}
			logger.Ctx(ctx).Warn("claudecode runtime: subprocess produced no events",
				zap.Int64("sessionID", req.SessionID),
				zap.String("providerSessionID", result.ProviderSessionID),
				zap.Error(result.StopErr))
			if req.SessionID > 0 {
				r.cache.Remove(sessionKey(req.SessionID))
			}
		}
		// 最佳努力地从 JSONL 抽 UserAnchor。
		if root := projectsRoot(); root != "" && result.ProviderSessionID != "" {
			if msgs, err := claudecode.ReadSessionJSONL(root, result.ProviderSessionID); err == nil {
				result.UserAnchor = claudecode.FindUserAnchorByText(msgs, req.UserText)
			}
		}
		// 生命周期边界:turn 收尾。结构化打 model / usage 让排查
		// 「provider 没识别 / usage 抽空了」这类 third-party provider 接入问题
		// 不用再起 raw dump (--include-partial-messages stream_event 那次踩坑)。
		logTurnDone(ctx, req, result)
		r.markIdle(req.SessionID)
		close(out)
	}()
	return out, result, nil
}

// logTurnDone 把一轮 claudecode turn 收尾时拿到的关键状态结构化写日志。
// usage 不直接 spread 是因为 *provider.Usage 可能 nil (0-frame 兜底分支);
// 这里把每个字段单独抽出来打,grep 时不需要展开嵌套对象。
func logTurnDone(ctx context.Context, req agentruntime.RunRequest, result *agentruntime.RunResult) {
	fields := []zap.Field{
		zap.Int64("sessionID", req.SessionID),
		zap.String("providerSessionID", result.ProviderSessionID),
		zap.String("model", result.Model),
	}
	if result.Usage != nil {
		fields = append(fields,
			zap.Int("promptTokens", result.Usage.PromptTokens),
			zap.Int("completionTokens", result.Usage.CompletionTokens),
			zap.Int("cachedTokens", result.Usage.CachedTokens),
			zap.Int("cacheCreationTokens", result.Usage.CacheCreationTokens),
		)
	} else {
		fields = append(fields, zap.Bool("usageNil", true))
	}
	if result.StopErr != nil {
		fields = append(fields, zap.Error(result.StopErr))
		logger.Ctx(ctx).Warn("claudecode runtime: turn done with stopErr", fields...)
		return
	}
	logger.Ctx(ctx).Info("claudecode runtime: turn done", fields...)
}

func consumedSteersFromInbox(items []httpgateway.SteerItem) []agentruntime.ConsumedSteer {
	if len(items) == 0 {
		return nil
	}
	out := make([]agentruntime.ConsumedSteer, 0, len(items))
	for _, it := range items {
		out = append(out, agentruntime.ConsumedSteer{QueuedID: it.ID, Text: it.Text})
	}
	return out
}

// acquireSession 拿到 chat session 对应的常驻 claude handle,必要时 spawn 新
// 进程。返回 (active, launchMode, err):launchMode 是本次实际下发给 CLI 的
// --permission-mode 值,Run() 写回 RunResult.LaunchPermissionMode,由 chat_svc
// 在主进程侧落库到 session.PermissionModeAtLaunch。
//
// 历史:旧实现直接调 chat_repo.Session().UpdatePermissionModeAtLaunch 写库,
// 在 agentred daemon 进程(不 bootstrap cago/chat_repo)里会 nil panic。
func (r *Runtime) acquireSession(ctx context.Context, req agentruntime.RunRequest) (*claudeActive, string, error) {
	// 按 session key 加锁(非全局):同一 session 的并发首轮串行化避免 double-spawn,
	// 不同 session 互不阻塞 —— 一个 session 卡在 spawn / SetPermissionMode 不会拖垮
	// 其它 session。CLISessionPool 自身按 key 线程安全,无需额外全局互斥。
	key := sessionKey(req.SessionID)
	lk := r.spawnLockFor(key)
	lk.Lock()
	defer lk.Unlock()

	var cur *claudeActive
	if req.SessionID > 0 {
		if v, ok := r.cache.Get(key); ok {
			cur = v.(*claudeActive)
		}
	}

	if req.ForkAnchor != "" && cur != nil {
		r.cache.Remove(key)
		cur = nil
	}

	if cur != nil && cur.launchedEffort != req.Backend.ReasoningEffort {
		r.cache.Remove(key)
		cur = nil
	}

	// 模型是启动期 flag:effectiveModel(解析出的 ModelID / backend.DefaultModel)变化 →
	// evict + 重 spawn(镜像上面 launchedEffort 先例)。模型未变则走下方复用分支,LRU
	// 缓存保留。#26 会话级 override 已移除,effectiveModel 不再含 override。
	if cur != nil && cur.launchedModel != claudeEffectiveModel(req) {
		r.cache.Remove(key)
		cur = nil
	}

	// ModelKey is part of the approved launch identity even when two stable
	// Model rows currently resolve to the same upstream ModelID.
	if cur != nil && cur.launchedModelKey != effectiveModelKey(req) {
		r.cache.Remove(key)
		cur = nil
	}

	// effectiveProviderKey 同是启动期参数(ANTHROPIC_BASE_URL/AUTH_TOKEN 只在 spawn
	// 时下发一次):两个不同供应商可以配同一个 model id,只比 launchedModel 会漏掉换
	// 供应商 —— 会话切换后网关路由虽已改,但复用的旧子进程手里没有指向新供应商的
	// env,请求仍打旧的(spec 2026-08-10 决策 4)。
	if cur != nil && cur.launchedProviderKey != req.EffectiveProviderKey() {
		r.cache.Remove(key)
		cur = nil
	}

	if cur != nil {
		// 复用现有 CLI 子进程:不重新 spawn,因此本轮没有新的 --permission-mode
		// 下发。回吐当前缓存的 mode 让 chat_svc 写库幂等(值不变即 noop)。
		cur.inTurn.Store(true)
		r.cache.MarkActive(key)
		return cur, cur.permissionModeSnapshot(), nil
	}

	isolationUUID := newUUIDv4()
	bin, err := r.hookBin()
	if err != nil {
		return nil, "", fmt.Errorf("agentruntime/runtimes/claudecode: resolve executable path: %w", err)
	}
	settingsJSON, err := buildHookSettingsJSONString(bin)
	if err != nil {
		return nil, "", fmt.Errorf("agentruntime/runtimes/claudecode: build hook settings: %w", err)
	}
	settingsJSON = buildSkillsSettings(req.EnabledPlugins, settingsJSON)

	pureResume := req.ProviderSessionID != "" && req.ForkAnchor == ""
	var cliSessionUUID, inboxKey string
	if pureResume {
		cliSessionUUID = ""
		inboxKey = req.ProviderSessionID
	} else {
		cliSessionUUID = isolationUUID
		inboxKey = isolationUUID
	}

	cwd := req.Cwd
	if cwd == "" {
		cwd, err = agentruntime.ResolveAgentCwd(req.AgentID, req.AgentSyncID)
		if err != nil {
			return nil, "", err
		}
	}
	env, err := BuildClaudeCodeEnv(req.Backend, CLIDeps{
		Token: req.GatewayToken, GatewayURL: req.GatewayURL, ProviderKey: req.EffectiveProviderKey(),
	})
	if err != nil {
		return nil, "", err
	}
	handle, err := ccSessionFactory(ccLaunchSpec{
		Req:                   req,
		Env:                   env,
		Cwd:                   cwd,
		Settings:              settingsJSON,
		SessionUUID:           cliSessionUUID,
		PermissionMode:        req.PermissionMode,
		DefaultPermissionMode: req.Backend.DefaultPermissionMode,
	})
	if err != nil {
		return nil, "", err
	}
	resolvedLaunchMode := resolveLaunchMode(req.PermissionMode, req.Backend.DefaultPermissionMode)
	// stored mode != launch mode 时(目前唯一触发条件: backendDefault=bypass +
	// req.PermissionMode != bypass, 见 resolveLaunchMode 注释), spawn 完成的瞬间
	// 发一次 control_request set_permission_mode 把 CLI 校准到 stored mode 一
	// 让前端 pill 看到的 mode 和 CLI 实际行为一致。失败仅记 warn 不阻 spawn ——
	// launch mode (bypass) 已是最宽松, 用户可以在 pill 上手动重试切换。
	//
	// 必须在 cache.Put + drainStream goroutine 启动前调用同步 SetPermissionMode:
	// 校准结果直赋 claudeActive.permissionMode 初值(发布前,无需 modeMu),发布后
	// 的更新一律走 setPermissionModeSnapshot。
	runtimeMode := resolvedLaunchMode
	if req.PermissionMode != "" && req.PermissionMode != resolvedLaunchMode {
		if err := handle.SetPermissionMode(ctx, req.PermissionMode); err != nil {
			logger.Ctx(ctx).Warn("claudecode runtime: spawn-after SetPermissionMode failed",
				zap.Int64("sessionID", req.SessionID),
				zap.String("launchMode", resolvedLaunchMode),
				zap.String("storedMode", req.PermissionMode),
				zap.Error(err))
		} else {
			runtimeMode = req.PermissionMode
		}
	}
	cur = &claudeActive{
		sessionUUID:         inboxKey,
		handle:              handle,
		steer:               r.steer,
		pool:                r.cache,
		poolKey:             key,
		launchedEffort:      req.Backend.ReasoningEffort,
		launchedModel:       claudeEffectiveModel(req),
		launchedModelKey:    effectiveModelKey(req),
		launchedProviderKey: req.EffectiveProviderKey(),
		permissionMode:      runtimeMode,
		tasks:               newTaskAggregator(),
	}
	if req.SessionID > 0 {
		r.cache.Put(key, cur)
		r.cache.MarkActive(key)
	}
	cur.inTurn.Store(true)
	return cur, resolvedLaunchMode, nil
}

func effectiveModelKey(req agentruntime.RunRequest) string {
	if req.Effective == nil {
		return ""
	}
	return strings.TrimSpace(req.Effective.ModelKey)
}

// drainStream 把 claudecode.Event 翻成 sealed agentruntime.Event;同步累 usage
// / stopErr;control_request 走 handleControlRequest 分派。
//
// 额外:active.tasks 聚合 TaskCreate / TaskUpdate 增量为 canonical.PlanUpdate
// 完整快照(TodoWrite 已经在 translator 里直接出 EventPlanUpdated),emit 顺序
// 是"先翻译完原始 event,再吐 PlanUpdated 快照",保证前端看到的 plan 更新
// 永远在对应 tool_use / tool_result 之后(消费方的 mutation order 不被打破)。
// onFirstFrame 在收到本轮第一帧时回调一次(解除 startup 看门狗) —— 子进程已吐帧
// 即证明它没卡在起步期, 后续无论多慢都不再由看门狗管。
//
// out 的存活期就是本函数的执行期:attach/detach 圈在这里,user turn / 自主续轮 /
// 后台 subagent 活动轮三条路径自动一致 —— 异步应答(SubmitAnswer /
// SubmitToolPermission)据此判断该轮通道是否还能写。调用方在本函数返回后才
// close(out),detach 已先摘除,不会向已关闭 channel 写入。
func drainStream(stream ccStream, out chan<- agentruntime.Event, result *agentruntime.RunResult, active *claudeActive, onFirstFrame func()) {
	if active != nil {
		active.attachOut(out)
		defer active.detachOut(out)
	}
	first := true
	for stream.Next() {
		if first {
			first = false
			if onFirstFrame != nil {
				onFirstFrame()
			}
		}
		ev := stream.Event()
		if result.StopErr != nil && claudeEventShowsProgressAfterError(ev.Kind) {
			result.StopErr = nil
		}
		if ev.Kind == claudecode.EventControlRequest && ev.ControlRequest != nil && active != nil {
			handleControlRequest(ev.ControlRequest, active, out)
			continue
		}
		// 同步 in-process mode 快照 —— ExitPlanMode 批准后 CLI 会自切 mode,
		// handleControlRequest 里的 bypassPermissions 短路判断要看新值。
		if ev.Kind == claudecode.EventPermissionModeChanged && ev.PermissionMode != "" && active != nil {
			active.setPermissionModeSnapshot(ev.PermissionMode)
		}
		translated, usage, stopErr := translate(ev)
		for _, t := range translated {
			out <- t
		}
		if active != nil && active.tasks != nil {
			switch ev.Kind {
			case claudecode.EventPreToolUse:
				emitSnapshot(out, active.tasks.observePreToolUse(ev))
			case claudecode.EventPostToolUse:
				emitSnapshot(out, active.tasks.observePostToolUse(ev))
			}
		}
		if usage != nil {
			result.Usage = usage
		}
		if stopErr != nil {
			result.StopErr = stopErr
		}
		if ev.Kind == claudecode.EventDone && ev.Model != "" {
			result.Model = ev.Model
		}
	}
}

func claudeEventShowsProgressAfterError(kind claudecode.EventKind) bool {
	switch kind {
	case claudecode.EventTextDelta,
		claudecode.EventThinkingDelta,
		claudecode.EventPreToolUse,
		claudecode.EventPostToolUse,
		claudecode.EventTaskStarted,
		claudecode.EventTaskProgress,
		claudecode.EventTaskNotification,
		claudecode.EventRetry,
		claudecode.EventCompactBoundary,
		claudecode.EventControlRequest,
		claudecode.EventSubagentModel:
		return true
	default:
		return false
	}
}

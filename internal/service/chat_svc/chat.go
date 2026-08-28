// Package chat_svc 提供聊天会话 / 消息的业务逻辑层。
package chat_svc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/gogo"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"

	// 显式 blank import 触发本地 runtime 子包 init() 把 *Runtime 注册到 RuntimeFor。
	// remote 是显式构造,不参与全局注册;以下几种为本地后端,必须自注册才能被
	// selectRunner 与 permissionModeMetaFor 解析到。
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/builtin"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/claudecode"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/codex"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/openclaw"
	piagentrt "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/piagent"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/httpgateway"
	"github.com/agentre-hub/agentre/internal/pkg/llmcatalog"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	chatblocks "github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/goal"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/ipc"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/remotepool"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/view"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

const (
	maxSendImages         = 4
	maxSendImageBytes     = 5 * 1024 * 1024
	dataURLBase64Token    = ";base64,"
	piStopAbortWriteBound = 500 * time.Millisecond
)

var sendImageMediaTypes = map[string]struct{}{
	"image/png":  {},
	"image/jpeg": {},
	"image/webp": {},
}

type ChatSvc interface {
	ListAgents(ctx context.Context, req *ListAgentsRequest) (*ListAgentsResponse, error)
	ListAgentSessions(ctx context.Context, req *ListAgentSessionsRequest) (*ListAgentSessionsResponse, error)
	// ListIndexSessions 给单一会话索引翻页：scope=recent 是跨 agent、跨项目的全局
	// 最近活动（「按时间」档），scope=free 是未挂项目的会话（「随手对话」组）。
	ListIndexSessions(ctx context.Context, req *ListIndexSessionsRequest) (*ListIndexSessionsResponse, error)
	LoadSession(ctx context.Context, req *LoadSessionRequest) (*LoadSessionResponse, error)
	// LoadMessageBlocks 取回更早一段消息的正文(前端向上滚动时续接转录)。
	LoadMessageBlocks(ctx context.Context, req *LoadMessageBlocksRequest) (*LoadMessageBlocksResponse, error)
	// LoadSessionBlocksByType 给派生视图按块类型点查整条会话的那一类块。
	LoadSessionBlocksByType(ctx context.Context, req *LoadSessionBlocksByTypeRequest) (*LoadSessionBlocksByTypeResponse, error)
	GetLaunchCommand(ctx context.Context, req *LaunchCommandRequest) (*LaunchCommandResponse, error)
	GetSessionGitState(ctx context.Context, req *GetSessionGitStateRequest) (*GetSessionGitStateResponse, error)
	// ResolveSessionWorkspace 把 sessionID 解析成 {deviceID, cwd}(deviceID 为 0
	// 即本机会话)。实现 workspace_fs_svc 的 SessionWorkspaceResolver 窄接口,
	// 由 bootstrap 注入 —— 让那个服务不必跨域读 chat / agent / agent_backend 表。
	ResolveSessionWorkspace(ctx context.Context, sessionID int64) (deviceID int64, cwd string, err error)
	// ResolveLocalCommandScope 为已有 session 或未持久化的 agent/project 目标解析历史作用域。
	ResolveLocalCommandScope(ctx context.Context, req *ResolveLocalCommandScopeRequest) (*LocalCommandScope, error)
	// PickExecTarget 按 R15 顺序为一个 Agent 挑第一个可用的执行目标档：本机没配对 /
	// 已配对但离线 / 会话绑的项目在那台机器上没配路径 / 既有 BlockReason 四类各自跳过。
	// projectID <= 0（自由会话）不做「该机器上有没有配这个项目的路径」这一项判定。
	// 列表为空 → ChatAgentNoBackend；全部不可用 → *ExecTargetNoneAvailableError（逐档
	// 原因，Wails 只透 Error() 字符串，因此原因也编进了那条字符串里）。
	// 不做会话粘性 —— 挑到之后钉不钉在这一档由调用方决定（R15b，块 4）。
	PickExecTarget(ctx context.Context, agentID int64, projectID int64) (*ExecTargetChoice, error)
	// ListExecTargetAvailability 逐档判定一个 Agent 的执行目标列表可用性（R15，任务
	// 12 的组织架构页用）。与 PickExecTarget 的关键差异是不提前返回——每一档都要给出
	// 结果，供界面同时展示。
	ListExecTargetAvailability(ctx context.Context, agentID int64, projectID int64) ([]ExecTargetAvailabilityView, error)
	Send(ctx context.Context, req *SendRequest) (*SendResponse, error)
	Compact(ctx context.Context, req *CompactRequest) (*CompactResponse, error)
	GetGoal(ctx context.Context, req *GoalRequest) (*GoalResponse, error)
	SetGoal(ctx context.Context, req *SetGoalRequest) (*GoalResponse, error)
	StartGoal(ctx context.Context, req *StartGoalRequest) (*StartGoalResponse, error)
	ClearGoal(ctx context.Context, req *ClearGoalRequest) (*ClearGoalResponse, error)
	Enqueue(ctx context.Context, req *EnqueueRequest) (*EnqueueResponse, error)
	CancelQueued(ctx context.Context, req *CancelQueuedRequest) (*CancelQueuedResponse, error)
	Stop(ctx context.Context, req *StopRequest) (*StopResponse, error)
	StopBackgroundTask(ctx context.Context, req *StopBackgroundTaskRequest) (*StopBackgroundTaskResponse, error)
	SetPermissionMode(ctx context.Context, req *SetPermissionModeRequest) (*SetPermissionModeResponse, error)
	// SetChatSessionModelTarget 切换已有会话的 LLM ModelTarget（空串 = 跟随 agent 绑定）。
	// 原子写 provider_key + model_key 两列，自下一轮生效，不打断正在进行的轮。
	SetChatSessionModelTarget(ctx context.Context, req *SetChatSessionModelTargetRequest) (*SetChatSessionModelTargetResponse, error)
	Regenerate(ctx context.Context, req *RegenerateRequest) (*SendResponse, error)
	Edit(ctx context.Context, req *EditRequest) (*SendResponse, error)
	Rename(ctx context.Context, req *RenameRequest) (*RenameResponse, error)
	Delete(ctx context.Context, req *DeleteRequest) (*DeleteResponse, error)
	MarkSessionRead(ctx context.Context, req *MarkSessionReadRequest) (*MarkSessionReadResponse, error)
	AnswerUserQuestion(ctx context.Context, req *AnswerUserQuestionRequest) (*AnswerUserQuestionResponse, error)
	AnswerToolPermission(ctx context.Context, req *AnswerToolPermissionRequest) (*AnswerToolPermissionResponse, error)
	ResolveExecApproval(ctx context.Context, req *ResolveExecApprovalRequest) (*ResolveExecApprovalResponse, error)
	ResolvePlanAction(ctx context.Context, req *ResolvePlanActionRequest) (*ResolvePlanActionResponse, error)
	// EnsureSession 是 chat_sessions 的统一创建/复用边界。其它 domain 不直接写 chat_repo.Session().Create。
	EnsureSession(ctx context.Context, req *EnsureSessionRequest) (*EnsureSessionResponse, error)
	// ObserveTurn 订阅指定 session 下一次 turn 完成(服务端, 不经 Wails)。
	ObserveTurn(sessionID int64) (<-chan TurnResult, func())
	// AgentBackendHasCapability 报告某 agent 的后端 runtime 是否声明指定能力(领域无关探针)。
	// 后端缺失/类型无法解析 → (false, nil)。MVP 仅解析本地 runtime; 远程后端目前返回 (false, nil)。
	AgentBackendHasCapability(ctx context.Context, agentID int64, wantCap capability.Capability) (bool, error)
	CountActiveSessions(ctx context.Context) (int, error)
	// BeginToolApproval 在 sessionID 当前活跃 turn 上登记一条 pending 工具审批、推流,
	// 并返回等待 channel;无活跃 turn → error(工具 MCP handler 据此拒绝工具调用)。
	// org / hook 等内置写工具共用此入口。
	BeginToolApproval(ctx context.Context, sessionID int64, blk *chatblocks.ToolApprovalBlock) (<-chan bool, error)
	// AnswerToolApproval 按 requestID 唤醒挂起的写工具调用(前端审批入口的唯一后端方法);
	// 未知/重复/已超时 → error。
	AnswerToolApproval(ctx context.Context, sessionID int64, requestID string, allow bool) error
	// FinishToolApproval 把审批置为终态(approved/denied/expired)并推 resolved 事件;
	// requestID 不存在(已被 finalize 取走)→ error。
	FinishToolApproval(ctx context.Context, sessionID int64, requestID, status, result string) error
	// FinalAssistantText 读取某 assistant message 的纯文本(拼接所有 TextBlock)。
	FinalAssistantText(ctx context.Context, messageID int64) (string, error)
	// LatestAssistantText 按 sessionID 取末条 assistant 文本(running peek;无 → 空串)。
	LatestAssistantText(ctx context.Context, sessionID int64) (string, error)
	// SessionProjectID 返回某会话所属的 project id(0=未挂项目);子 agent 工具用它继承调用方项目/cwd。
	SessionProjectID(ctx context.Context, sessionID int64) (int64, error)
	// CatchUpRemoteSessions 在 App 启动后按 chat_sessions.exec_device_id 连回各台配对
	// daemon,把桌面端离线期间远端产生的转录与待决策补回来。见 remote_catchup.go。
	CatchUpRemoteSessions(ctx context.Context) error
	// CatchUpRemoteDevice 在某台 daemon 重新上线时补上启动那次没做成的补齐(启动补齐
	// 只跑一次,开机自启早于网络就绪的那一次拨号失败否则就是终局)。见 remote_catchup.go。
	CatchUpRemoteDevice(ctx context.Context, deviceID int64) error
}

var defaultChat ChatSvc

var defaultGateway httpgateway.TokenRouter

func Chat() ChatSvc { return defaultChat }

func RegisterChat(impl ChatSvc) {
	if s, ok := impl.(*chatSvc); ok && s.gateway == nil {
		s.gateway = defaultGateway
	}
	defaultChat = impl
}

func NewChat(emitter Emitter) ChatSvc {
	if emitter == nil {
		emitter = NoopEmitter{}
	}
	s := &chatSvc{
		emitter:       emitter,
		locks:         &sync.Map{},
		activeCancels: &sync.Map{},
		aborted:       &sync.Map{},
		turnObservers: &sync.Map{},
		toolApprovals: map[int64][]*chatblocks.ToolApprovalBlock{},
		gateway:       defaultGateway,
	}
	s.dispatcher = newPackageDispatcher(s)
	return s
}

// RegisterGateway 由 bootstrap 注入 httpgateway 单例；
// 没有注入时（早期单测、headless 启动）走 CLI 自身 login 路径。
//
// 要的是 TokenRouter 而不是 TokenIssuer：chat flow 的 token 按会话 effective provider
// 路由，且会话中途换供应商时要能在**不换 token 字符串**的前提下改它的路由目标（决策 3）。
func RegisterGateway(g httpgateway.TokenRouter) {
	defaultGateway = g
	if s, ok := defaultChat.(*chatSvc); ok {
		s.gateway = g
	}
}

const renameTitleMaxRunes = 200

type activeTurnControl struct {
	cancel context.CancelFunc

	mu            sync.RWMutex
	gracefulAbort agentruntime.Aborter
}

func (c *activeTurnControl) setGracefulAbort(aborter agentruntime.Aborter) {
	if c == nil || aborter == nil {
		return
	}
	c.mu.Lock()
	c.gracefulAbort = aborter
	c.mu.Unlock()
}

func (c *activeTurnControl) gracefulAborter() (agentruntime.Aborter, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.gracefulAbort, c.gracefulAbort != nil
}

type chatSvc struct {
	emitter Emitter
	// dispatcher 是 svc-bound turn.Dispatcher,注册了带 chat_svc 适配器的 18 个 handler。
	// 在 NewChat 时构造一次(svc-bound,handlers 的 Writer/Persister 持 *chatSvc 引用)。
	// AGENTRE_NEW_DISPATCHER=1 时 runTurn drain loop 通过它处理 Event;默认关。
	dispatcher *turn.Dispatcher
	locks      *sync.Map
	// activeCancels：sessionID(int64) → *activeTurnControl。控制对象也是 generation
	// token；旧 turn 收尾只 CompareAndDelete 自己，不能删掉即时重试的新 cancel。
	activeCancels *sync.Map
	// aborted：sessionID(int64) → struct{}。Stop 触发时 store；runTurn 收尾时
	// LoadAndDelete 判定是否走 StreamAborted 路径 + 跳过 DrainPending 自动接续。
	aborted *sync.Map
	// activeTurnStreams: sessionID(int64) → 当前活跃 turn 的 per-turn 流名(string)。
	// runTurn 起止维护;工具审批(BeginToolApproval)据此路由审批卡到正确的流。
	activeTurnStreams sync.Map
	// toolApprovals: 本会话进行中 turn 上挂起/已决的工具审批 block(org / hook 等内置
	// 写工具共用),finalize 时 merge 进 assistant 消息;LoadSession 时 overlay 到投影。
	toolApprovalsMu sync.Mutex
	toolApprovals   map[int64][]*chatblocks.ToolApprovalBlock
	// toolApprovalWaiters: requestID(string) → chan bool(buffered=1)。BeginToolApproval
	// 登记,AnswerToolApproval LoadAndDelete 后回灌决策,FinishToolApproval 终态兜底清。
	toolApprovalWaiters sync.Map
	// turnObservers：sessionID(int64) → *sync.Map(chan TurnResult → struct{})。
	// 服务端 turn 完成观察口(不经 Wails);调度方在 Send 前 ObserveTurn 订阅,
	// finalize / failTurn 各回灌恰好一条终态用于释放调度位 + 判定 quiesce。
	turnObservers *sync.Map
	// peerPublications keeps remote account peers in a separate ordered stream,
	// so attachment adds presence without replacing the desktop Wails emitter.
	peerPublications sync.Map
	// peerSteerSources maps an existing queued steer to its authenticated caller
	// until that queue entry is consumed; it is metadata, never a second queue.
	peerSteerSources sync.Map
	// autoWatchers：sessionID(int64) → struct{}。startAutonomousWatcher 用它防同一
	// session 重复起 watcher goroutine(每会话一个,惰性启动);watcher 在底层
	// AutonomousTurns channel close(子进程 evict / CloseSession)时退出并清这条。
	autoWatchers sync.Map
	// catchUpPending：deviceID(int64) → struct{}。启动补齐时拨不通(或补齐半途出错)的
	// 设备记在这里 —— 那一刻拿不到任何判据,会话一行都不能碰。设备监视报它重新上线时
	// 由 CatchUpRemoteDevice 重来一次并清掉这条。
	catchUpPending sync.Map
	// subagentActivityWatchers：sessionID(int64) → struct{}。startSubagentActivityWatcher
	// 用它防同一 session 重复起后台 subagent 活动 watcher(每会话一个,惰性启动);watcher
	// 在底层 SubagentActivity channel close(子进程 evict / CloseSession)时退出并清这条。
	subagentActivityWatchers sync.Map
	// bgRunning: sessionID(int64) → *bgRunningSet。per-session「运行中后台 subagent 的
	// tool_use_id 集合」。集合非空 = 该会话有后台 subagent 在跑。后台 subagent 易失
	// (随 CLI 子进程/重启消失)，故不落库；重启后 map 空 = 0 天然正确。见 bg_running.go。
	bgRunning sync.Map
	gateway   httpgateway.TokenRouter
	// chatTokens 是会话常驻 gateway token 的缓存,实现在 agentruntime(桌面与 daemon
	// 共用同一份签发/改道/撤销规则)。惰性构造(tokenCacheOnce):不少单测直接字面量
	// 构造 chatSvc,拿不到 NewChat 的构造时机。
	tokenCacheOnce sync.Once
	chatTokens     *agentruntime.SessionTokenCache

	// remoteCache 是 device → (runtime, lease) 的 session 引用计数缓存。
	// runtime 复用底层 lease.Client(),lease 由 remote_device_svc.Pool 管理 conn
	// 复用 + idle 回收 + daemon drop evict。lease.Closed() 关闭时 watchLeaseClosed
	// 把 entry 从 map 摘掉,下次 borrow 走冷路径重建。
	// goalsImpl 惰性构造,见「目标」一节。
	goalsOnce sync.Once
	goalsImpl *goal.Controller

	// permissionModesImpl 惰性构造,见「权限模式」一节。
	permissionModesOnce sync.Once
	permissionModesImpl *ipc.PermissionModeController

	// remotePoolImpl 惰性构造(remotePoolOnce),见 remote_pool.go。
	remotePoolOnce sync.Once
	remotePoolImpl *remotepool.Pool
	// connStates: sessionID(int64) → 该会话此刻偏离缺省的连接态。onRemoteConnState
	// (chat:conn:<sid> 的发布方)维护,LoadSession 同步读。见 remote_reconnect.go。
	connMu     sync.Mutex
	connStates map[int64]remote.ConnState
	// testHookPool 如果非 nil,代替 remote_device_svc.Default().Pool() 用于测试注入。
	testHookPool remote_device_svc.ConnPool
}

// ── ListAgents ───────────────────────────────────────────────────────────────

// CountActiveSessions 返回正在进行(running|waiting)的会话总数,供退出二次确认判断。
func (s *chatSvc) CountActiveSessions(ctx context.Context) (int, error) {
	n, err := chat_repo.Session().CountActive(ctx, []string{"running", "waiting"})
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

func (s *chatSvc) ListAgents(ctx context.Context, _ *ListAgentsRequest) (*ListAgentsResponse, error) {
	agents, err := agent_repo.Agent().List(ctx)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	resp := &ListAgentsResponse{Agents: make([]ChatAgentItem, 0, len(agents))}
	if len(agents) == 0 {
		return resp, nil
	}

	backendIDs := uniqueNonZeroBackendIDs(agents)
	backends, err := agent_backend_repo.AgentBackend().BatchFind(ctx, backendIDs)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	providerKeys := uniqueProviderKeys(backends)
	providers, err := llm_provider_repo.LLMProvider().BatchFindByKey(ctx, providerKeys)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}

	// 批量查远端 device 视图，避免 per-agent 单次查询的 N+1 问题。DeviceID 是规范
	// 指纹，一律在本机配对表里按 DaemonFingerprint 找。
	fingerprintSet := map[string]struct{}{}
	for _, be := range backends {
		if beTargetsRemote(be) {
			fingerprintSet[be.DeviceFingerprint] = struct{}{}
		}
	}
	fingerprintViews := map[string]*remote_device_svc.DeviceView{}
	if rds := remote_device_svc.Default(); rds != nil && len(fingerprintSet) > 0 {
		// 查不到的指纹 → DeviceID 照常透出，DeviceName 留空 + Online false。
		if rows, lerr := rds.List(ctx); lerr == nil {
			for _, row := range rows {
				if row != nil {
					fingerprintViews[row.DaemonFingerprint] = row
				}
			}
		}
	}

	agentIDs := make([]int64, 0, len(agents))
	for _, a := range agents {
		agentIDs = append(agentIDs, a.ID)
	}
	counts, err := chat_repo.Session().CountRunningByAgents(ctx, agentIDs)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	totals, err := chat_repo.Session().CountByAgents(ctx, agentIDs)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	sessionIDs, err := chat_repo.Session().ListIDsByAgents(ctx, agentIDs)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}

	for _, a := range agents {
		ids := sessionIDs[a.ID]
		if ids == nil {
			ids = []int64{}
		}
		item := ChatAgentItem{
			ID:            a.ID,
			Name:          a.Name,
			AvatarColor:   a.AvatarColor,
			AvatarIcon:    a.AvatarIcon,
			AvatarDataURL: a.AvatarDataURL,
			// 置顶完全由 DB 的 pinned 列承载（含系统 Agent/CEO）：系统 Agent 不再被
			// IsSystem() 强制浮顶，用户置顶/取消后这里透传 DB 值（R: ceo-unpin）。
			Pinned:           a.Pinned,
			HasBackendTarget: a.AgentBackendID > 0,
			ActiveCount:      counts[a.ID],
			TotalSessions:    totals[a.ID],
			SessionIDs:       ids,
		}
		if be := backends[a.AgentBackendID]; be != nil {
			item.BackendType = be.Type
			item.LLMProviderKey = be.LLMProviderKey
			if agent_backend_entity.BackendType(be.Type) == agent_backend_entity.TypeClaudeCode {
				// 仅 claudecode 透出；entity.Check 限定其它后端为空串。
				item.DefaultPermissionMode = be.DefaultPermissionMode
			}
			// 远端 device 归属字段。本机档（空 DeviceID / 本机指纹）走 ExternalDeviceID
			// 收敛成空串后整组留零值，与 LoadSession 同一口径。
			if deviceID := remote_device_svc.ExternalDeviceID(be.DeviceFingerprint); deviceID != "" {
				item.DeviceID = deviceID
				if dv := fingerprintViews[deviceID]; dv != nil {
					item.DeviceName = dv.Name
					item.Online = dv.Online
				}
			}
			gatewayRunning := s.gateway != nil && s.gateway.Status().State == "running"
			item.Chattable, item.BlockReason, item.ChattableHint =
				blockReasonForBackend(ctx, be, providers[be.LLMProviderKey], gatewayRunning)
		} else if a.IsSystem() {
			item.BlockReason = BlockReasonNoBackend
			item.ChattableHint = i18n.T(ctx, code.ChatSystemAgentNoBackendHint)
		} else {
			item.BlockReason = BlockReasonNoBackend
			item.ChattableHint = i18n.T(ctx, code.ChatAgentNoBackendHint)
		}

		sessions, err := chat_repo.Session().ListByAgent(ctx, a.ID, 5)
		if err != nil {
			return nil, operationFailedWithCause(ctx, err)
		}
		item.RecentCount = len(sessions)
		item.Sessions = make([]ChatSessionLite, 0, len(sessions))
		for _, sess := range sessions {
			item.Sessions = append(item.Sessions, s.sessionLiteFromEntity(sess))
		}

		// sidebar 折叠态 attention bubble：拉所有 running/waiting/error 会话。
		// 不受 5 行常规列表的约束；limit=20 防异常数据撑爆 UI，前端去重与本组 sessions 的重叠。
		attention, err := chat_repo.Session().ListAttentionByAgent(ctx, a.ID, 20)
		if err != nil {
			return nil, operationFailedWithCause(ctx, err)
		}
		item.AttentionSessions = make([]ChatSessionLite, 0, len(attention))
		for _, sess := range attention {
			item.AttentionSessions = append(item.AttentionSessions, s.sessionLiteFromEntity(sess))
		}
		resp.Agents = append(resp.Agents, item)
	}
	return resp, nil
}

// ── ListIndexSessions ────────────────────────────────────────────────────────

// ListIndexSessions 见接口注释。两个 scope 各补上一个此前根本拿不到的集合：
//
//   - recent —— 跨 agent、跨项目的全局最近活动。ListChatAgents 每个 agent 只给前 5
//     条，把它们并起来是一个窗口而不是全量；「按时间」这一档要的正是全量的头部。
//   - free —— project_id = 0 的会话。ListSessions 挡在 projectID > 0（0 不是一个
//     项目），所以自由会话此前只能靠「碰巧落在某个 agent 的前 5 条里」被看见。
//
// 分页口径与 ListAgentSessions 完全一致（默认 20 / 上限 100），前端两处翻页逻辑同形。
func (s *chatSvc) ListIndexSessions(ctx context.Context, req *ListIndexSessionsRequest) (*ListIndexSessionsResponse, error) {
	if req == nil || req.Offset < 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	// 认不出的 scope 直接拒绝，不默默当成 recent —— 静默降级会让调用方以为自己拿到的
	// 是「随手对话」，实际是全部会话。
	switch req.Scope {
	case SessionScopeRecent, SessionScopeFree:
	case SessionScopeProject:
		// projectID 0 有专门的 scope（free），从 project 这条路进来必是调用方漏传。
		if req.ProjectID <= 0 {
			return nil, i18n.NewError(ctx, code.InvalidParameter)
		}
	case SessionScopeMachine:
		// 这里**放行 0**：它是本机（chat_entity.Session 的约定），不是「没有机器」。
		// 拒掉它，本机那一组就永远空着 —— 而绝大多数会话都在本机。
		if req.DeviceID < 0 {
			return nil, i18n.NewError(ctx, code.InvalidParameter)
		}
	default:
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = listAgentSessionsDefaultLimit
	}
	if limit > listAgentSessionsMaxLimit {
		limit = listAgentSessionsMaxLimit
	}

	list := chat_repo.Session().ListRecentPaged
	count := chat_repo.Session().CountAll
	switch req.Scope {
	case SessionScopeFree:
		list = chat_repo.Session().ListFreePaged
		count = chat_repo.Session().CountFree
	case SessionScopeProject:
		list = func(ctx context.Context, offset, limit int) ([]*chat_entity.Session, error) {
			return chat_repo.Session().ListByProjectPaged(ctx, req.ProjectID, offset, limit)
		}
		count = func(ctx context.Context) (int64, error) {
			return chat_repo.Session().CountByProject(ctx, req.ProjectID)
		}
	case SessionScopeMachine:
		list = func(ctx context.Context, offset, limit int) ([]*chat_entity.Session, error) {
			return chat_repo.Session().ListByDevicePaged(ctx, req.DeviceID, offset, limit)
		}
		count = func(ctx context.Context) (int64, error) {
			return chat_repo.Session().CountByDevice(ctx, req.DeviceID)
		}
	}

	sessions, err := list(ctx, req.Offset, limit)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	total, err := count(ctx)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}

	resp := &ListIndexSessionsResponse{
		Sessions: make([]ChatSessionLite, 0, len(sessions)),
		Total:    total,
		HasMore:  int64(req.Offset+len(sessions)) < total,
	}
	for _, sess := range sessions {
		resp.Sessions = append(resp.Sessions, s.sessionLiteFromEntity(sess))
	}
	return resp, nil
}

// ── ListAgentSessions ────────────────────────────────────────────────────────

// ListAgentSessions 给「查看全部 N 个会话」popover 翻页拉数据用。
// 服务侧在这里做参数 clamp（offset≥0、limit∈[1,100]），repo 只忠实按参数查；
// hasMore 按 offset+len < total 判定，让前端不用自己算页数。
const (
	listAgentSessionsDefaultLimit = 20
	listAgentSessionsMaxLimit     = 100
)

func (s *chatSvc) ListAgentSessions(ctx context.Context, req *ListAgentSessionsRequest) (*ListAgentSessionsResponse, error) {
	if req == nil || req.AgentID <= 0 || req.Offset < 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = listAgentSessionsDefaultLimit
	}
	if limit > listAgentSessionsMaxLimit {
		limit = listAgentSessionsMaxLimit
	}

	sessions, err := chat_repo.Session().ListByAgentPaged(ctx, req.AgentID, req.Offset, limit)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	total, err := chat_repo.Session().CountByAgent(ctx, req.AgentID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}

	resp := &ListAgentSessionsResponse{
		Sessions: make([]ChatSessionLite, 0, len(sessions)),
		Total:    total,
		HasMore:  int64(req.Offset+len(sessions)) < total,
	}
	for _, sess := range sessions {
		resp.Sessions = append(resp.Sessions, s.sessionLiteFromEntity(sess))
	}
	return resp, nil
}

func (s *chatSvc) sessionLiteFromEntity(sess *chat_entity.Session) ChatSessionLite {
	if sess == nil {
		return ChatSessionLite{}
	}
	return ChatSessionLite{
		ID:             sess.ID,
		AgentID:        sess.AgentID,
		ProjectID:      sess.ProjectID,
		Title:          sess.Title,
		Status:         sess.AgentStatus,
		NeedsAttention: sess.IsWaitingForUser(),
		BgRunning:      s.bgRunningActive(sess.ID),
		LastMessageAt:  sess.LastMessageAt,
		LastReadAt:     sess.LastReadAt,
	}
}

// activeStreamName 给 LoadSession 用:turn 进行中时,让中途打开该会话的前端能重挂到
// per-turn 实时流。per-turn 流名只在用户主动 Send 时由响应给出;子 agent 调用轮 / 自主轮等"非
// 前端发起"的 turn 前端拿不到这个名字 —— 这里按在跑 turn 的(末条真实)assistant 消息把它
// 重建出来,前端据此 openStream 续看。无活跃 turn / 还没建出 assistant 消息时返回空串。
func activeStreamName(activeTurn bool, sessionID int64, msgs []*chat_entity.Message) string {
	if !activeTurn {
		return ""
	}
	if i := view.LastTurnAssistantIndex(msgs); i >= 0 {
		return StreamName(sessionID, msgs[i].ID)
	}
	return ""
}

func (s *chatSvc) LoadSession(ctx context.Context, req *LoadSessionRequest) (*LoadSessionResponse, error) {
	sess, err := chat_repo.Session().Find(ctx, req.SessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if sess == nil {
		return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	a, err := agent_repo.Agent().Find(ctx, sess.AgentID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	// 读路径是「元数据全量 + 块按需取」(决策 6,**不是**历史截断):元数据一条不少,
	// 正文只取最近一个窗口。窗口外的正文有两条按需取回的路 —— 向上滚动走
	// LoadMessageBlocks,派生视图走 LoadSessionBlocksByType 的按类型点查。
	msgs, err := chat_repo.Message().ListMeta(ctx, sess.ID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if err := chat_repo.Message().FillBlocks(ctx, transcriptWindow(msgs)); err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	resp := &LoadSessionResponse{
		Session: ChatSessionDetail{
			ID:                     sess.ID,
			AgentID:                sess.AgentID,
			Title:                  sess.Title,
			AgentStatus:            sess.AgentStatus,
			NeedsAttention:         sess.IsWaitingForUser(),
			BgRunning:              s.bgRunningActive(sess.ID),
			LastMessageAt:          sess.LastMessageAt,
			LastReadAt:             sess.LastReadAt,
			Createtime:             sess.Createtime,
			PermissionMode:         sess.PermissionMode,
			PermissionModeAtLaunch: sess.PermissionModeAtLaunch,
			ProviderKey:            sess.ProviderKey,
			ModelKey:               sess.ModelKey,
			ProjectID:              sess.ProjectID,
		},
		Messages: make([]ChatMessage, 0, len(msgs)),
	}
	// 诊断: 记录这次 serve 出去的 agentStatus + 后端此刻是否有活跃 turn。
	// 前端「过期快照覆盖 running」竞态在 serve 端通常看着无辜(turn 还没起、DB 还是
	// idle),但若 serve 时已有活跃 turn 却吐 非 running/waiting,就是后端侧能直接抓到
	// 的不一致。配合前端 LogClient 上报的 apply 时刻能把竞态时间线对上。
	_, activeTurn := s.activeCancels.Load(sess.ID)
	// ActiveStream 让中途打开本会话的前端重挂到 per-turn 实时流(子 agent 调用轮 / 自主轮等
	// 非前端发起的 turn 没有 Send 响应入口)。无活跃 turn 时为空,前端不重挂。
	resp.Session.ActiveStream = activeStreamName(activeTurn, sess.ID, msgs)
	// 连接态随响应同步返回:重挂上来的前端要在订阅 chat:conn:<sid> 之前就知道这条会话
	// 此刻是不是断着的,否则整个退避窗口里只剩打字指示器。
	resp.Session.ConnectionState = string(s.sessionConnState(sess.ID))
	if activeTurn &&
		sess.AgentStatus != "running" && sess.AgentStatus != "waiting" {
		logger.Ctx(ctx).Warn("chat_svc: LoadSession served non-running status while turn active",
			zap.Int64("sessionId", sess.ID),
			zap.String("agentStatus", sess.AgentStatus),
			zap.Bool("activeTurn", true))
	} else {
		logger.Ctx(ctx).Debug("chat_svc: LoadSession served",
			zap.Int64("sessionId", sess.ID),
			zap.String("agentStatus", sess.AgentStatus))
	}
	if a != nil {
		resp.Session.AgentName = a.Name
		resp.Session.AgentColor = a.AvatarColor
		resp.Session.AgentIcon = a.AvatarIcon
		resp.Session.AgentAvatarDataURL = a.AvatarDataURL
		// BackendType 给前端判断「复制启动命令」这类仅 CLI 后端有效的菜单是否显示。
		// 上下文窗口走统一优先级 resolveContextWindowWithRuntime：
		//   runtime 上报（session.ContextWindow）> 解析出的 ContextWindow
		//   > latestAssistantModel catalog > 解析出的 ModelID catalog。
		// backend 不存在或无 provider 时仍尝试用 latestAssistantModel 兜底；都没有 → 0。
		// 查询失败一律不阻塞加载会话本身。
		var prov *llm_provider_entity.LLMProvider
		var be *agent_backend_entity.AgentBackend
		// 会话已经钉住某一档时(sess.ExecAgentBackendID > 0，R15b / 决策36)优先解析
		// 那一档，而不是 Agent 的（最小 sort_order）默认档——否则多档 Agent 里续轮
		// 落在第二档以后的会话，聊天头会一直展示错误的机器/后端信息。没钉住时回落
		// a.AgentBackendID，与 resolveTurnBackendID 的语义一致。
		displayBackendID := sess.ExecAgentBackendID
		if displayBackendID <= 0 {
			displayBackendID = a.AgentBackendID
		}
		if displayBackendID > 0 {
			be, _ = agent_backend_repo.AgentBackend().Find(ctx, displayBackendID)
			// 钉住的那一档已被删除时，展示口径与执行口径同一恢复边界
			// （resolveAgentBackend：该引用已经不再指向一档，按 Agent 当前列表重挑）：
			// 回落 Agent 当前的第一档，而不是让整组后端字段留空。留空会让前端
			// activeBackendType 变成空串，composer 的模型 pill 与权限模式 pill 一起
			// 不渲染，直到用户发出一条消息、执行侧把钉档换成活的才回来。
			// 这里只读不写：重钉仍归执行侧那一处，展示不制造持久化副作用。
			if be == nil && a.AgentBackendID > 0 && a.AgentBackendID != displayBackendID {
				be, _ = agent_backend_repo.AgentBackend().Find(ctx, a.AgentBackendID)
			}
			if be != nil {
				resp.Session.BackendType = be.Type
				// 展示口径（spec 2026-08-10）：按 effective provider（会话 provider_key >
				// agent 绑定）解析，与这条会话下一轮真正会用的那家一致；agent 绑定 key
				// 一并回传，供 composer 的供应商 pill 渲染「跟随绑定」时的标签。
				prov, _ = s.resolveEffectiveProvider(ctx, sess, be)
				resp.Session.AgentProviderKey = be.LLMProviderKey
				resp.Session.AgentModelKey = be.LLMModelKey
			}
		}
		// ExecTargetCount 给前端聊天头 chip 守卫用（R15 / R20）：多档 Agent 的会话
		// 总是显示机器 chip(含本机)，单档维持既有"只有远端才显示"的行为。
		if targets, terr := agent_repo.AgentExecTarget().ListByAgent(ctx, sess.AgentID); terr == nil {
			resp.Session.ExecTargetCount = len(targets)
		}
		if prov != nil {
			resp.Session.LLMProviderType = prov.Type
		}
		// 展示侧使用同一解析规则（EffectiveLLMConfig v1 seam）：上下文窗口 / 模型目录
		// 走解析出的模型，不直接读 Provider 行。查询失败不阻塞加载会话本身（与 prov
		// 同一容忍度）。modelKey 与 turn 同一口径（sessionModelKeyFor）：会话钉了
		// fixed-model 时展示会话那个模型，而不是 backend 的绑定（spec「Effective
		// configuration」：展示与执行同一解析结果）。
		cfg, _ := s.effectiveLLMForTurn(ctx, prov, sessionModelKeyFor(sess, be, prov))
		resp.Session.ContextWindow = resolveContextWindowWithRuntime(sess, cfg, msgs)

		// Device + cwd 信息: 给前端 chat header 渲染"远端运行 · /home/me/proj"小字使用。
		// be 解析失败 / device 离线 / cwd 查询失败时容忍降级 (字段留空,不让 LoadSession 整体失败);
		// 降级路径都补 debug log,给排查 blank DeviceName / missing Cwd 留信号。
		if be != nil {
			// 展示口径的设备标识：本机档（空 DeviceID / R13 认领后的本机指纹）一律空
			// 串，见 remote_device_svc.ExternalDeviceID。本机指纹拿去配对表里查永远查
			// 不到（不会和自己配对），照远端解析会把本机会话渲染成一台没名字的离线远
			// 端机；device 字段整组只对真正的远端档才有意义。
			if deviceID := remote_device_svc.ExternalDeviceID(be.DeviceFingerprint); deviceID != "" {
				resp.Session.DeviceID = deviceID
				if dv := localPairedDeviceView(ctx, deviceID); dv != nil {
					resp.Session.DeviceName = dv.Name
					resp.Session.Online = dv.Online
				} else {
					logger.Ctx(ctx).Debug("chat_svc.LoadSession: device lookup degraded",
						zap.String("deviceFingerprint", deviceID),
						zap.Int64("sessionID", sess.ID))
				}
			}
			if cwd, cerr := resolveSessionCwd(ctx, sess, be); cerr == nil {
				resp.Session.Cwd = cwd
			} else {
				resp.Session.CwdUnavailableReason = cwdUnavailableReasonFor(cerr)
				logger.Ctx(ctx).Debug("LoadSession: cwd resolve degraded",
					zap.Int64("sessionID", sess.ID),
					zap.Error(cerr))
			}
		}
	}
	for _, m := range msgs {
		cm, err := toChatMessage(m)
		if err != nil {
			return nil, i18n.NewError(ctx, code.ChatBlocksMalformed)
		}
		resp.Messages = append(resp.Messages, cm)
	}
	// 进行中 turn 上挂起/已决的审批 block 还没 finalize 进消息行,overlay 到末条**真实**
	// assistant 消息的投影,中途打开会话也能看到审批卡(finalize 时会真正落库)。
	// 用 msgs 的下标定位(resp.Messages 与它逐条 1:1 投影),与 ActiveStream 同一口径 ——
	// 两处若各挑各的行,前端就会把审批卡搬到一条没人 emit 的流上,resolved 反扫落空、
	// 卡片永远 pending。旁白行(供应商切换 notice)不是一轮,见 view.LastTurnAssistantIndex。
	if pend := s.snapshotToolApprovals(sess.ID); len(pend) > 0 {
		if i := view.LastTurnAssistantIndex(msgs); i >= 0 {
			for _, b := range pend {
				resp.Messages[i].Blocks = append(resp.Messages[i].Blocks, toolApprovalBlockToChatBlock(b))
			}
		}
	}
	return resp, nil
}

// GetLaunchCommand 把当前 session 关联的 CLI 后端配置拼成一条人类可读、可在终端
// 粘贴运行的命令。Token 故意写成占位符 <TOKEN>，不发放实际 token —— 用户自行替换。
//
// builtin 后端没有外部 CLI，直接返回 ChatLaunchCommandNotAvailable。
func (s *chatSvc) GetLaunchCommand(ctx context.Context, req *LaunchCommandRequest) (*LaunchCommandResponse, error) {
	if req == nil || req.SessionID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	sess, err := chat_repo.Session().Find(ctx, req.SessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if sess == nil {
		return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	a, err := agent_repo.Agent().Find(ctx, sess.AgentID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if a == nil {
		return nil, i18n.NewError(ctx, code.AgentNotFound)
	}
	// 钉住的那一档优先（R15b / 决策36）：启动命令要跟这条会话续轮实际用的那一档
	// 一致，否则复制出去的是另一档的 CLI 路径 / 供应商 / 网关口令。
	backendID := sessionBackendID(sess, a)
	if backendID <= 0 {
		return nil, i18n.NewError(ctx, code.ChatAgentNoBackend)
	}
	be, err := agent_backend_repo.AgentBackend().Find(ctx, backendID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if be.IsBuiltin() {
		return nil, i18n.NewError(ctx, code.ChatLaunchCommandNotAvailable)
	}

	// 按 effective provider 解析（会话 provider_key > agent 绑定，spec 2026-08-10）：
	// 复制出去的命令要与这条会话实际执行的那家供应商一致，否则用户拿到的是 agent
	// 绑定那家的 BASE_URL / model。
	prov, err := s.resolveEffectiveProvider(ctx, sess, be)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	// 执行侧配置（EffectiveLLMConfig v1 seam）：--model 用解析出的 ModelID，
	// 不再读 Provider 旧单模型字段。modelKey 与 turn 同一口径（sessionModelKeyFor），
	// 保证复制出去的启动命令与这条会话实际执行用的模型一致（spec 2026-08-11
	// 「Effective configuration」：复制启动命令与执行同一解析结果）。
	cfg, err := s.effectiveLLMForTurn(ctx, prov, sessionModelKeyFor(sess, be, prov))
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}

	// 关联 provider 时拼 gateway URL 并签一个"进程内永久"的 token 内联进命令；
	// CLI 自身 login 模式则什么也不传，BuildLaunchCommand 不会写 BASE_URL/API_KEY。
	//
	// TTL=0 = 永久（gateway 进程生命周期内）。这意味着 gateway 重启所有这种 token
	// 都会失效——这是 token 仅存在内存里的天然安全护栏，前端 toast 已告知用户。
	gatewayURL, gatewayToken := "", ""
	if prov != nil && s.gateway != nil {
		gatewayURL = s.gateway.URL()
		// 按 effective target 签：复制出去的命令要打到这条会话实际用的那家 +
		// 模型（spec 2026-08-11 决策 9：token 路由目标 = ProviderKey+ModelKey）。
		if tok, terr := s.gateway.IssueTokenFor(ctx, be, prov.ProviderKey, sessionModelKeyFor(sess, be, prov), 0); terr == nil {
			gatewayToken = tok
		}
	}

	cwd, err := resolveSessionCwd(ctx, sess, be)
	if err != nil {
		return nil, err
	}
	cmd, err := agentruntime.BuildLaunchCommand(agentruntime.LaunchCommandSpec{
		Backend:           be,
		Effective:         cfg,
		AgentID:           a.ID,
		SessionID:         sess.ID,
		Cwd:               cwd,
		ProviderSessionID: sess.ProviderSessionID,
		GatewayURL:        gatewayURL,
		Token:             gatewayToken,
	})
	if err != nil {
		return nil, i18n.NewError(ctx, code.ChatLaunchCommandNotAvailable)
	}
	return &LaunchCommandResponse{Command: cmd, BackendType: be.Type}, nil
}

type sendOptions struct {
	allowPlanWaiting bool
}

func (s *chatSvc) Send(ctx context.Context, req *SendRequest) (*SendResponse, error) {
	return s.send(ctx, req, sendOptions{})
}

func (s *chatSvc) Compact(ctx context.Context, req *CompactRequest) (*CompactResponse, error) {
	if req == nil || req.SessionID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	sess, err := chat_repo.Session().Find(ctx, req.SessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if sess == nil {
		return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	a, be, prov, err := s.resolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID)
	if err != nil {
		return nil, err
	}
	// 会话 provider_key 优先于 agent 绑定解析（决策 3），与 send / Regenerate / Edit
	// 同源：Compact 也是本地 turn 入口，#26 时代经 prepareTurnRun 的 ModelOverride 走
	// 会话级模型覆盖，override 移除后这里必须补上同款解析，否则带 provider_key 的会话
	// 在 compact 轮会悄悄退回 agent 绑定。provider-default 所选供应商缺失/停用/禁用/不兼容
	// → 回退 agent 绑定并追加一条持久 notice（决策 8，随 turnExtras 携带）；fixed-model
	// 目标失效 → 严格阻止本轮（决策 7）。远端 backend 不走这里——会话 provider 随 wire
	// 透传由 daemon 自解（决策 9）。
	prov, providerFallbackNotice, err := s.resolveSessionProvider(ctx, sess, be, prov)
	if err != nil {
		return nil, err
	}
	gate, err := s.acquireTurnGate(ctx, sess, be)
	if err != nil {
		return nil, err
	}
	gateOwned := true
	defer func() {
		if gateOwned {
			gate.lock.Unlock()
		}
	}()
	// Codex and Pi Agent compact an existing provider-native session; neither may
	// create an empty replacement session merely to satisfy an explicit compact.
	if compactRequiresProviderSession(be) && strings.TrimSpace(sess.ProviderSessionID) == "" {
		return nil, i18n.NewError(ctx, code.ChatCompactNoSession)
	}
	runner, err := s.selectRunner(ctx, be, sess.ID)
	if err != nil {
		logger.Ctx(ctx).Warn("chat_svc.Compact: selectRunner failed",
			zap.Int64("sessionId", sess.ID),
			zap.String("backendType", be.Type),
			zap.Error(err))
		return nil, i18n.NewError(ctx, code.ChatCompactUnsupported)
	}
	releasePreflight := func() {}
	if beTargetsRemote(be) {
		if deviceID, ok := localPairedDeviceID(ctx, be.DeviceFingerprint); ok {
			releasePreflight = func() { s.releaseRemoteRuntime(deviceID, sess.ID) }
		}
	}
	if !runner.Capabilities().Has(capability.CapCompact) {
		releasePreflight()
		return nil, i18n.NewError(ctx, code.ChatCompactUnsupported)
	}
	gateOwned = false
	resp, err := s.startCompactTurn(ctx, sess, a, be, prov, turnExtras{providerFallbackNotice: providerFallbackNotice}, gate.lock)
	if err != nil {
		releasePreflight()
		return nil, err
	}
	return resp, nil
}

func compactRequiresProviderSession(be *agent_backend_entity.AgentBackend) bool {
	return be != nil && (be.IsCodex() || be.IsPiAgent())
}

// chatGoalHost 把 chatSvc 适配成 goal.Host —— 目标链路唯一伸进 chat_svc 的那几件事。
type chatGoalHost struct{ s *chatSvc }

// 编译期确认适配器满足端口。
var _ goal.Host = chatGoalHost{}

func (s *chatSvc) send(ctx context.Context, req *SendRequest, opts sendOptions) (*SendResponse, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	text := strings.TrimSpace(req.Text)
	imageBlocks, err := blocksFromSendImages(ctx, req.Images)
	if err != nil {
		return nil, err
	}
	if text == "" && len(imageBlocks) == 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	if len(text) > chat_entity.MessageTextMaxBytes {
		return nil, i18n.NewError(ctx, code.ChatTextTooLong)
	}
	if req.SessionID < 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}

	var (
		sess          *chat_entity.Session
		targetAgentID = req.AgentID
	)
	if req.SessionID > 0 {
		var err error
		sess, err = chat_repo.Session().Find(ctx, req.SessionID)
		if err != nil {
			return nil, operationFailedWithCause(ctx, err)
		}
		if sess == nil {
			return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
		}
		targetAgentID = sess.AgentID
	}
	if targetAgentID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}

	// sess 非 nil 时是续轮（可能已经钉住某一档，R15b / 决策36）；sess 为 nil 时是
	// 全新会话，还没有可粘的档，projectID 用请求原始值(下面新建 sess 时也是它)。
	pickProjectID := req.ProjectID
	if sess != nil {
		pickProjectID = sess.ProjectID
	}
	// R15a 手动指定：仅新建会话（sess 为 nil）生效，与 ModelOverride 同一条规则——
	// 已有会话早就按 R15b 钉在它落到的那一档上，这个字段对它没有意义。用一个只填了
	// ExecAgentBackendID 的探针 session 喂给 resolveAgentBackend，让
	// resolveTurnBackendID 走"已钉住"分支直接采用它，不再触发 PickExecTarget 的
	// 自动挑选；探针不落库，真正的持久化钉住由下面 startTurn → pinExecTargetIfUnset
	// 对真实新建的 sess 完成。
	resolveSess := sess
	if sess == nil && req.ExecTargetOverride > 0 {
		if err := s.validateExecTargetOverride(ctx, targetAgentID, pickProjectID, req.ExecTargetOverride); err != nil {
			return nil, err
		}
		resolveSess = &chat_entity.Session{ExecAgentBackendID: req.ExecTargetOverride}
	}
	a, be, prov, err := s.resolveAgentBackend(ctx, resolveSess, targetAgentID, pickProjectID)
	if err != nil {
		return nil, err
	}
	var gate *sessionTurnGate
	gateOwned := false
	if req.SessionID > 0 {
		gate, err = s.acquireTurnGate(ctx, sess, be)
		if err != nil {
			return nil, err
		}
		gateOwned = true
		defer func() {
			if gateOwned {
				gate.lock.Unlock()
			}
		}()
	}
	// 指向本机指纹的档（R13 认领后本机 backend 的 DeviceID == 本机指纹）按本机处理：
	// 图片能力校验同样适用于它——它跑在本地 runtime 上，缺 CapImageInput 时也该早退
	// AgentBackendTypeUnsupported，而不是绕过校验把图喂给不支持的 runtime。
	if len(imageBlocks) > 0 && !beTargetsRemote(be) {
		runner, err := s.selectRunner(ctx, be, req.SessionID)
		if err != nil {
			return nil, err
		}
		if !runner.Capabilities().Has(capability.CapImageInput) {
			return nil, i18n.NewError(ctx, code.AgentBackendTypeUnsupported)
		}
	}

	if req.SessionID == 0 {
		// 新建会话所选 LLM ModelTarget（可选，决策 2）：非空时校验供应商/模型存在、启用、
		// 归属与后端 kind 兼容，校验通过后与 Session 一起 Create 落库（spec 2026-08-11
		// 「新建与已有会话流程」：新会话选择保持瞬态，首条 Send 创建 Session 时与
		// ProviderKey/ModelKey 一起持久化）；双空 = 跟随 agent 绑定。
		providerKey := strings.TrimSpace(req.ProviderKey)
		modelKey := strings.TrimSpace(req.ModelKey)
		if providerKey != "" || modelKey != "" {
			sessionProv, _, perr := s.validateSessionModelTarget(ctx, be, providerKey, modelKey)
			if perr != nil {
				return nil, perr
			}
			prov = sessionProv
		}
		// 项目上下文（可选）：仅在新建会话时生效；已存在的会话不再换项目。
		projectID, perr := s.resolveProjectContext(ctx, req.ProjectID, targetAgentID)
		if perr != nil {
			return nil, perr
		}
		permissionMode, perr := ipc.CreatePermissionMode(ctx, be, req.PermissionMode, true)
		if perr != nil {
			return nil, perr
		}
		sess = &chat_entity.Session{
			AgentID:        targetAgentID,
			ProjectID:      projectID,
			PermissionMode: permissionMode,
			// at_launch 同步落库,避免 runtime 在 spawn goroutine 里写入跟前端 LoadSession
			// 抢跑——前端拿到空串后 messages.length>0 时会把 bypass pill 错灰。
			// runtime 后续仍按 resolveLaunchMode 结果幂等覆盖,处理后端默认值回落。
			PermissionModeAtLaunch: permissionMode,
			ProviderKey:            providerKey,
			ModelKey:               modelKey,
			Title:                  sessionTitleFromFirstMessage(text),
			// idle 落库;running 由 startTurn 事务内的 Update 原子翻转 —— 事务失败
			// 时不残留 running(否则空会话永久卡 running,还会 block 退出)。
			AgentStatus: "idle",
			Status:      consts.ACTIVE,
		}
		if err := chat_repo.Session().Create(ctx, sess); err != nil {
			return nil, operationFailedWithCause(ctx, err)
		}
	} else {
		planWaiting, err := s.permissionModes().CanContinuePlanWaiting(ctx, sess, be, opts.allowPlanWaiting)
		if err != nil {
			return nil, err
		}
		if err := s.permissionModes().ApplyRequested(ctx, sess, be, req.PermissionMode, planWaiting); err != nil {
			return nil, err
		}
	}

	// 已有会话：会话 provider_key 优先于 agent 绑定解析（决策 3）；所选供应商缺失/停用/
	// 不兼容 → 回退 agent 绑定并追加一条持久 notice（决策 8）。新建会话已在校验后落库,
	// 无需再走覆盖。
	// 远端 backend 不走这里：会话 provider 随 wire 透传由 daemon 自解（决策 9, task 2）。
	var providerFallbackNotice *blocks.NoticeBlock
	if req.SessionID > 0 {
		var err error
		prov, providerFallbackNotice, err = s.resolveSessionProvider(ctx, sess, be, prov)
		if err != nil {
			return nil, err
		}
	}

	var prelocked *trylockMutex
	if gate != nil {
		prelocked = gate.lock
		gateOwned = false
	}
	return s.startTurn(ctx, sess, a, be, prov, userBlocksForSend(text, imageBlocks), nil /*preTxHook*/, nil /*replacement*/, "" /*forkAnchor*/, turnExtras{
		peerSource:             req.peerSource,
		emitTurnStartedBypass:  req.EmitTurnStartedBypass,
		providerFallbackNotice: providerFallbackNotice,
	}, prelocked)
}

func userBlocksForSend(text string, imageBlocks []blocks.ContentBlock) []blocks.ContentBlock {
	out := make([]blocks.ContentBlock, 0, 1+len(imageBlocks))
	if strings.TrimSpace(text) != "" {
		out = append(out, &blocks.TextBlock{Text: text})
	}
	out = append(out, imageBlocks...)
	return out
}

func blocksFromSendImages(ctx context.Context, images []SendImage) ([]blocks.ContentBlock, error) {
	if len(images) == 0 {
		return nil, nil
	}
	if len(images) > maxSendImages {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	out := make([]blocks.ContentBlock, 0, len(images))
	for _, img := range images {
		mediaType, payload, ok := strings.Cut(strings.TrimSpace(img.DataURL), dataURLBase64Token)
		if !ok || !strings.HasPrefix(mediaType, "data:") {
			return nil, i18n.NewError(ctx, code.InvalidParameter)
		}
		mediaType = strings.TrimPrefix(mediaType, "data:")
		if _, ok := sendImageMediaTypes[mediaType]; !ok {
			return nil, i18n.NewError(ctx, code.InvalidParameter)
		}
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil || len(decoded) == 0 || len(decoded) > maxSendImageBytes {
			return nil, i18n.NewError(ctx, code.InvalidParameter)
		}
		out = append(out, blocks.ImageBlock{
			MediaType: mediaType,
			Source:    blocks.BlobSource{Inline: decoded},
		})
	}
	return out, nil
}

// resolveProjectContext 校验新建会话的项目参数。返回 (projectID, err)。
// projectID=0 表示自由会话（不属于任何项目）。
//
// 规则：
//   - projectID=0 → 直接返回（自由会话）。
//   - 项目必须存在且 active；agent 必须是项目直接成员或继承成员。
//
// cwd 解析交给 project_svc.ResolveSessionCwd，永远 = project.Path。
func (s *chatSvc) resolveProjectContext(ctx context.Context, projectID int64, agentID int64) (int64, error) {
	if projectID == 0 {
		return 0, nil
	}
	p, err := project_repo.Project().Find(ctx, projectID)
	if err != nil {
		return 0, operationFailedWithCause(ctx, err)
	}
	if p == nil || !p.IsActive() {
		return 0, i18n.NewError(ctx, code.ProjectNotFound)
	}
	if ok, mErr := s.isAgentInProjectChain(ctx, agentID, p); mErr != nil {
		return 0, mErr
	} else if !ok {
		return 0, i18n.NewError(ctx, code.ProjectAgentNotMember)
	}
	return p.ID, nil
}

// isAgentInProjectChain 自下而上扫一遍 parent 链，看 agent 是不是直接 / 继承成员。
// 走批量 ListByProjects 一次拉完，避免 N+1。
func (s *chatSvc) isAgentInProjectChain(ctx context.Context, agentID int64, p *project_entity.Project) (bool, error) {
	ids := []int64{p.ID}
	cur := p
	for cur.ParentID > 0 {
		parent, err := project_repo.Project().Find(ctx, cur.ParentID)
		if err != nil {
			return false, operationFailedWithCause(ctx, err)
		}
		if parent == nil {
			break
		}
		ids = append(ids, parent.ID)
		cur = parent
	}
	mapByProj, err := project_repo.ProjectAgent().ListByProjects(ctx, ids)
	if err != nil {
		return false, operationFailedWithCause(ctx, err)
	}
	for _, list := range mapByProj {
		for _, pa := range list {
			if pa.AgentID == agentID {
				return true, nil
			}
		}
	}
	return false, nil
}

// resolveAgentBackend 查 agent → backend → provider 并做完整的"可对话"校验，同时是
// 会话粘性（R15b / 决策36）的唯一解析点：sess 非 nil 且已经钉住某一档
// (ExecAgentBackendID > 0) 时直接解析那一档、不重挑 —— 同一台机器上可以有多档，钉住
// 的是档本身，续轮不因排序里有更靠前的档现在可用而改派。唯一恢复边界是钉住的
// backend 已被删除：该引用已经不再指向一档，按 Agent 当前列表重挑并替换失效钉档。
// 没钉住时（首轮 / sess 为 nil / 老会话）按 R15 顺序挑第一个可用的档
// （PickExecTarget，task 2 的挑选口）。
//
// Agent 的执行目标列表为空时退化为直接用 a.AgentBackendID 解析：这与
// agent_repo.hydrateExecTargets 的语义一致（两者理论上永远同值），提前短路避免对着
// 一个天然只有 0/1 个目标的 Agent 走 PickExecTarget 的逐档 BlockReason 枚举。
//
// Send / Regenerate / Edit / Compact / StartGoal 等所有"起 / 续一轮"的入口都走这条；
// 规则集中在一处避免多处漂移。
func (s *chatSvc) resolveAgentBackend(ctx context.Context, sess *chat_entity.Session, agentID, projectID int64) (
	*agent_entity.Agent,
	*agent_backend_entity.AgentBackend,
	*llm_provider_entity.LLMProvider,
	error,
) {
	a, err := agent_repo.Agent().Find(ctx, agentID)
	if err != nil {
		return nil, nil, nil, operationFailedWithCause(ctx, err)
	}
	if a == nil {
		return nil, nil, nil, i18n.NewError(ctx, code.NotFound)
	}
	backendID, err := s.resolveTurnBackendID(ctx, sess, agentID, projectID, a.AgentBackendID)
	if err != nil {
		return nil, nil, nil, err
	}
	if backendID <= 0 {
		return nil, nil, nil, i18n.NewError(ctx, code.ChatAgentNoBackend)
	}
	be, err := agent_backend_repo.AgentBackend().Find(ctx, backendID)
	if err != nil {
		return nil, nil, nil, operationFailedWithCause(ctx, err)
	}
	if be == nil && sess != nil && sess.ID > 0 && sess.ExecAgentBackendID == backendID {
		choice, pickErr := s.PickExecTarget(ctx, agentID, projectID)
		if pickErr != nil {
			return nil, nil, nil, pickErr
		}
		be = choice.Backend
		sess.ExecAgentBackendID = 0
		s.pinExecTargetIfUnset(ctx, sess, be)
		logger.Ctx(ctx).Info("chat_svc.resolveAgentBackend: recovered deleted pinned exec target",
			zap.Int64("sessionId", sess.ID),
			zap.Int64("deletedAgentBackendId", backendID),
			zap.Int64("agentBackendId", be.ID))
	}
	if be == nil {
		return nil, nil, nil, i18n.NewError(ctx, code.ChatAgentNotChattable)
	}
	kind := be.Kind()
	if kind == nil {
		return nil, nil, nil, i18n.NewError(ctx, code.AgentBackendInvalidType)
	}

	var prov *llm_provider_entity.LLMProvider
	switch agent_backend_entity.BackendType(be.Type) {
	case agent_backend_entity.TypeBuiltin:
		prov, err = llm_provider_repo.LLMProvider().FindByKey(ctx, be.LLMProviderKey)
		if err != nil {
			return nil, nil, nil, operationFailedWithCause(ctx, err)
		}
		if prov == nil || !prov.IsActive() {
			return nil, nil, nil, i18n.NewError(ctx, code.ChatAgentNotChattable)
		}
	case agent_backend_entity.TypeClaudeCode, agent_backend_entity.TypeCodex, agent_backend_entity.TypePiAgent:
		if be.LLMProviderKey != "" {
			prov, err = llm_provider_repo.LLMProvider().FindByKey(ctx, be.LLMProviderKey)
			if err != nil {
				return nil, nil, nil, operationFailedWithCause(ctx, err)
			}
			if prov == nil || !prov.IsActive() ||
				!kind.ProviderTypeMatch(llm_provider_entity.ProviderType(prov.Type)) {
				return nil, nil, nil, i18n.NewError(ctx, code.ChatAgentNotChattable)
			}
			if remoteProviderKnownMissing(ctx, be) {
				return nil, nil, nil, remoteProviderNotConfiguredError(ctx, be.LLMProviderKey)
			}
			if beTargetsRemote(be) {
				break
			}
			if s.gateway == nil || s.gateway.Status().State != "running" {
				return nil, nil, nil, i18n.NewError(ctx, code.ChatBackendGatewayUnavailable)
			}
		}
		// LLMProviderKey == "" → CLI 自身 login 状态生效，不强制 gateway。
	case agent_backend_entity.TypeOpenClaw:
		if beTargetsRemote(be) {
			return nil, nil, nil, fmt.Errorf("openclaw remote secret enrollment is unavailable")
		}
	default:
		return nil, nil, nil, i18n.NewError(ctx, code.AgentBackendInvalidType)
	}

	return a, be, prov, nil
}

// resolveTurnBackendID 是 resolveAgentBackend 的解析半边（R15b / 决策36）：决定这一轮
// 该用哪个 backend id。写回半边见 pinExecTargetIfUnset —— 本函数只读不写。
//
//   - sess 已经钉住(ExecAgentBackendID > 0)：直接用它，不重挑。
//   - 否则这个 Agent 的执行目标列表为空：退化用 fallbackBackendID
//     (=a.AgentBackendID，与 agent_repo.hydrateExecTargets 的语义一致，理论上永远
//     同值)，不经 PickExecTarget（对空列表它本就是 ChatAgentNoBackend）。
//   - 否则按 R15 顺序挑第一个可用的档（PickExecTarget，task 2 的挑选口）。
func (s *chatSvc) resolveTurnBackendID(
	ctx context.Context, sess *chat_entity.Session, agentID, projectID, fallbackBackendID int64,
) (int64, error) {
	if sess != nil && sess.ExecAgentBackendID > 0 {
		return sess.ExecAgentBackendID, nil
	}
	targets, err := agent_repo.AgentExecTarget().ListByAgent(ctx, agentID)
	if err != nil {
		return 0, operationFailedWithCause(ctx, err, zap.Int64("agentId", agentID))
	}
	if len(targets) == 0 {
		return fallbackBackendID, nil
	}
	choice, err := s.PickExecTarget(ctx, agentID, projectID)
	if err != nil {
		return 0, err
	}
	return choice.Target.AgentBackendID, nil
}

// pinExecTargetIfUnset 给一条已持久化的会话首次钉住它落到的那一档（R15b / 决策36）：
// 已经钉过（sess.ExecAgentBackendID != 0）不重复写 —— 续轮的解析短路在
// resolveTurnBackendID 里发生，这里只在"没值"分支实际选出了 be 之后调用一次。
//
// 只处理本机档：远端档的钉住由 recordExecDaemon 在实际 borrow 到 *remote.Runtime 时
// 一并完成（selectRunner → borrowRemoteRuntime，它现在也写 agentBackendID）——那里
// 才拿得到当下真实的 daemon 实例标识，不在这里提前解析一次、写一份可能过期的指纹。
//
// 与 exec_device_id / exec_device_fingerprint 走同一条专用单列更新 UpdateExecDaemon
// 一并写入，三列同生共死，不拆成两个写入点。写库失败只记日志、不阻断这一轮 —— 下一轮
// 会再次落进"没值"分支重新挑选并重试写回，不会永久卡住对话。
func (s *chatSvc) pinExecTargetIfUnset(ctx context.Context, sess *chat_entity.Session, be *agent_backend_entity.AgentBackend) {
	if sess == nil || sess.ID <= 0 || be == nil || sess.ExecAgentBackendID != 0 || beTargetsRemote(be) {
		return
	}
	if err := chat_repo.Session().UpdateExecDaemon(ctx, sess.ID, 0, "", be.ID); err != nil {
		logger.Ctx(ctx).Warn("chat_svc.pinExecTargetIfUnset: persist pinned exec target failed",
			zap.Int64("sessionId", sess.ID), zap.Int64("agentBackendId", be.ID), zap.Error(err))
		return
	}
	sess.ExecAgentBackendID = be.ID
}

// validateNewSessionProvider 校验新建会话所选供应商（spec 决策 2）：非空时供应商必须
// 存在、IsActive 且与后端 kind 兼容（ProviderTypeMatch），否则 Send 直接报错（复用不可
// 对话的错误语义，不在落库后才发现）。返回该校验通过的供应商供本轮 prov 使用。
func (s *chatSvc) validateNewSessionProvider(ctx context.Context, be *agent_backend_entity.AgentBackend, providerKey string) (*llm_provider_entity.LLMProvider, error) {
	key := strings.TrimSpace(providerKey)
	if key == "" {
		return nil, nil
	}
	kind := be.Kind()
	if kind == nil {
		return nil, i18n.NewError(ctx, code.AgentBackendInvalidType)
	}
	prov, err := llm_provider_repo.LLMProvider().FindByKey(ctx, key)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if prov == nil || !prov.IsActive() {
		return nil, i18n.NewError(ctx, code.ChatAgentNotChattable)
	}
	if !kind.ProviderTypeMatch(llm_provider_entity.ProviderType(prov.Type)) {
		return nil, i18n.NewError(ctx, code.ChatAgentNotChattable)
	}
	return prov, nil
}

// resolveSessionProvider 把「会话 provider_key > agent 绑定」应用到已有会话的 turn 入口
// （send / Regenerate / Edit / Compact，决策 3）：本地后端按会话 provider_key 解析 prov，
// provider-default 的所选供应商缺失/停用/禁用/不兼容时回退 agent 绑定并返回一条持久
// notice（决策 8）；fixed-model 目标失效时返回 error，由调用方严格阻止本轮（决策 7，绝不
// 回退）。远端后端不走这里——会话 provider 随 wire 透传给 daemon 自解（决策 9，
// prepareTurnRun 里按 effectiveProviderKey 取 key），本地 provider 表反映不了 daemon 配置，
// 不得据此发 notice。
func (s *chatSvc) resolveSessionProvider(
	ctx context.Context,
	sess *chat_entity.Session,
	be *agent_backend_entity.AgentBackend,
	prov *llm_provider_entity.LLMProvider,
) (*llm_provider_entity.LLMProvider, *blocks.NoticeBlock, error) {
	if sess == nil || strings.TrimSpace(sess.ProviderKey) == "" || be == nil ||
		beTargetsRemote(be) {
		return prov, nil, nil
	}
	return s.sessionProviderOverride(ctx, be, sess.ProviderKey, sess.ModelKey, prov)
}

// sessionProviderOverride 应用「会话 provider_key > agent 绑定」的供应商优先级
// （spec 决策 3 + 2026-08-11 决策 7 的失败语义）。sessKey 为空时原样返回 baseProv（无会话
// 覆盖）。sessModelKey 用于区分 provider-default 与 fixed-model：
//
//   - provider-default（sessModelKey 空）：会话所选供应商缺失 / 停用（enabled=0）/
//     软删除 / 与后端 kind 不兼容 → 回退 agent 绑定（baseProv）并返回一条持久 notice
//     （决策 8），provider_key 不清除，供应商恢复后自动回到会话所选。
//   - fixed-model（sessModelKey 非空）：Provider 或 Model 缺失 / 停用 / 禁用 / 类型
//     不兼容 → 返回 error（spec 决策 7「fixed-model 失效严格阻止下一轮」），绝不回退
//     Agent、不改用 Provider 默认、不清除 key —— 系统保留原 target，Picker 显示失效。
func (s *chatSvc) sessionProviderOverride(
	ctx context.Context,
	be *agent_backend_entity.AgentBackend,
	sessKey, sessModelKey string,
	baseProv *llm_provider_entity.LLMProvider,
) (*llm_provider_entity.LLMProvider, *blocks.NoticeBlock, error) {
	key := strings.TrimSpace(sessKey)
	if key == "" {
		return baseProv, nil, nil
	}
	prov, err := llm_provider_repo.LLMProvider().FindByKey(ctx, key)
	kind := be.Kind()
	if err == nil && prov != nil && prov.IsActive() && prov.IsEnabled() && kind != nil &&
		kind.ProviderTypeMatch(llm_provider_entity.ProviderType(prov.Type)) {
		return prov, nil, nil
	}
	// 展示名(决策 2):实体查到了(只是停用/类型不兼容)就带上它的名字;查询失败或
	// 实体本身不存在(供应商已删)则 view.ProviderDisplayName 返回空串,notice 保持只显示 key。
	name := ""
	if err == nil {
		name = view.ProviderDisplayName(prov)
	}
	// fixed-model：严格阻止下一轮，绝不回退（spec 2026-08-11 决策 7 / Failure）。
	if strings.TrimSpace(sessModelKey) != "" {
		return nil, nil, i18n.NewError(ctx, code.LLMProviderModelTargetInvalid)
	}
	return baseProv, &blocks.NoticeBlock{
		Level: "info",
		Text:  view.EncodeProviderFallback(key, name),
	}, nil
}

// AgentBackendHasCapability 报告某 agent 的后端 runtime 是否声明指定能力(领域无关探针)。
//
// 复用 resolveAgentBackend 的 agent → backend 解析链;对本地后端经全局 runtime 注册表
// (RuntimeFor)取能力矩阵。后端缺失/类型无法解析 → (false, nil)。
// MVP: 远程后端的能力探测需借 session(borrowRemoteRuntime),这里没有 session,
// 暂统一返回 (false, nil)。
func (s *chatSvc) AgentBackendHasCapability(ctx context.Context, agentID int64, wantCap capability.Capability) (bool, error) {
	// 无 session 上下文的探针：没有会话可粘、projectID 也无从谈起。
	_, be, _, err := s.resolveAgentBackend(ctx, nil, agentID, 0)
	if err != nil {
		return false, err
	}
	if be == nil {
		return false, nil
	}
	if beTargetsRemote(be) {
		return false, nil
	}
	r := agentruntime.RuntimeFor(agent_backend_entity.BackendType(be.Type))
	if r == nil {
		return false, nil
	}
	return r.Capabilities().Has(wantCap), nil
}

// Enqueue 在 AI 还在回答时把一条新的用户消息插入当前 turn。
//
//   - claudecode 走 PreToolUse hook + SteerInbox：runner.Steer 把 (queuedID, text)
//     Push 进 in-process 队列，hook 进程 GET 走附在 additionalContext 上。
//   - codex 走 turn/steer JSON-RPC，直接打到 Stream 上（queuedID 被 runner 忽略）。
//   - builtin 走 cago Runner.Steer，queuedID 透传 WithSteerID 用于后续 cancel。
//
// 不会创建 chat_messages 行 —— Enqueue 的语义是「下一条 AI 应该看到的指令」，
// 不是历史消息。如果模型最终响应并写回文本，会落到已存在的 assistant 流里。
//
// 返回 Cancellable=true 当且仅当后端 runner 实现 SteerCanceler。前端按此
// 决定 chip 上的 X 按钮是真撤回还是替换为锁图标。
//
// 返回错误码：
//   - ChatSteerNoActive: 没有正在进行的 turn
//   - ChatSteerUnsupported: 后端不实现 Steerer
//   - ChatSteerInternal: 实现层 I/O 失败
func (s *chatSvc) Enqueue(ctx context.Context, req *EnqueueRequest) (*EnqueueResponse, error) {
	return s.enqueue(ctx, req)
}

func (s *chatSvc) enqueue(ctx context.Context, req *EnqueueRequest) (*EnqueueResponse, error) {
	if req == nil || req.SessionID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}

	sess, err := chat_repo.Session().Find(ctx, req.SessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if sess == nil {
		return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}

	_, be, _, err := s.resolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID)
	if err != nil {
		return nil, err
	}

	runner, err := s.selectRunner(ctx, be, sess.ID)
	if err != nil {
		logger.Ctx(ctx).Warn("chat_svc.Enqueue: selectRunner failed",
			zap.Int64("sessionId", sess.ID),
			zap.String("backendType", be.Type),
			zap.Error(err))
		return nil, i18n.NewError(ctx, code.ChatSteerUnsupported)
	}
	steerer, ok := runner.(agentruntime.Steerer)
	if !ok {
		return nil, i18n.NewError(ctx, code.ChatSteerUnsupported)
	}
	queuedID := newQueuedID()
	if err := steerer.Steer(ctx, sess.ID, queuedID, text); err != nil {
		if errors.Is(err, agentruntime.ErrNoActiveTurn) {
			return nil, i18n.NewError(ctx, code.ChatSteerNoActive)
		}
		logger.Ctx(ctx).Warn("chat_svc.Enqueue: steerer.Steer failed",
			zap.Int64("sessionId", sess.ID),
			zap.String("queuedId", queuedID),
			zap.String("backendType", be.Type),
			zap.Error(err))
		return nil, i18n.NewError(ctx, code.ChatSteerInternal)
	}
	if req.peerSource.Device != "" {
		s.peerSteerSources.Store(queuedID, req.peerSource)
	}
	_, cancellable := runner.(agentruntime.SteerCanceler)
	return &EnqueueResponse{
		SessionID:   sess.ID,
		Queued:      true,
		QueuedID:    queuedID,
		Cancellable: cancellable,
	}, nil
}

// 权限模式状态机已迁到 chat_svc/ipc(ipc.PermissionModeController + 一组自由函数),
// 这里只留会话/后端解析与 DTO 边界。permissionModes() 惰性构造控制器:不少单测直接
// 字面量构造 chatSvc,拿不到 NewChat 的构造时机。
func (s *chatSvc) permissionModes() *ipc.PermissionModeController {
	s.permissionModesOnce.Do(func() {
		s.permissionModesImpl = ipc.NewPermissionModeController(
			chatRunnerSource{s: s}, planBlockProbe{}, func(ctx context.Context, cause error) error {
				return operationFailedWithCause(ctx, cause)
			})
	})
	return s.permissionModesImpl
}

// chatRunnerSource / planBlockProbe 把 chatSvc 适配成 ipc 侧声明的两个窄端口。
type chatRunnerSource struct{ s *chatSvc }

func (r chatRunnerSource) SelectRunner(
	ctx context.Context, be *agent_backend_entity.AgentBackend, sessionID int64,
) (agentruntime.Runtime, error) {
	return r.s.selectRunner(ctx, be, sessionID)
}

type planBlockProbe struct{}

func (planBlockProbe) HasActionablePlan(bs []blocks.ContentBlock) bool {
	return hasActionablePlanBlock(bs)
}

// SetPermissionMode 让前端把 CLI 会话切到指定 mode。
//
// claudecode 使用 Claude permission mode；codex 使用 Codex collaboration mode
// 的 default / plan 子集。写 DB 在 runtime 之前，进程未启动时也会在下次启动生效。
// 校验/落库/下发本身在 ipc.PermissionModeController.SetMode,错误码见那里。
func (s *chatSvc) SetPermissionMode(ctx context.Context, req *SetPermissionModeRequest) (*SetPermissionModeResponse, error) {
	if req == nil || req.SessionID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	if !ipc.IsKnownPermissionMode(req.Mode) {
		return nil, i18n.NewError(ctx, code.ChatPermissionModeInvalid)
	}

	sess, err := chat_repo.Session().Find(ctx, req.SessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if sess == nil {
		return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}

	_, be, _, err := s.resolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID)
	if err != nil {
		return nil, err
	}

	// 后端变更类操作同样必须先对账未决的 Pi transcript recovery：会话可能带着 Pi
	// 替换标记（标记把 provider session + running 态原子落库）后切换了后端，把
	// permission mode 下发到新后端前必须先把标记恢复/清理掉，否则 provider 状态
	// 会建立在未决恢复之上。这里不加 session turn 锁：SetPermissionMode 是允许
	// claudecode 轮内切换的轻量操作（与 Enqueue 一致），真正在跑的轮子在其起点已
	// 通过 gate 对账；未决标记只可能由持有锁的 Pi 轮产生，而 Pi 后端在下方
	// supported 检查就被拒，故此处对账不会与创建标记的轮子并发。
	if _, rerr := s.reconcileTranscriptReplacement(ctx, sess, be); rerr != nil {
		return nil, operationFailedWithCause(ctx, rerr,
			zap.Int64("sessionId", sess.ID),
			zap.String("backendType", be.Type))
	}

	mode, err := s.permissionModes().SetMode(ctx, sess, be, req.Mode)
	if err != nil {
		return nil, err
	}
	return &SetPermissionModeResponse{Applied: true, Mode: mode}, nil
}

func (s *chatSvc) CancelQueued(ctx context.Context, req *CancelQueuedRequest) (*CancelQueuedResponse, error) {
	if req == nil || req.SessionID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}

	sess, err := chat_repo.Session().Find(ctx, req.SessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if sess == nil {
		return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}

	_, be, _, err := s.resolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID)
	if err != nil {
		return nil, err
	}

	runner, err := s.selectRunner(ctx, be, sess.ID)
	if err != nil {
		return nil, i18n.NewError(ctx, code.ChatCancelUnsupported)
	}
	canceler, ok := runner.(agentruntime.SteerCanceler)
	if !ok {
		return nil, i18n.NewError(ctx, code.ChatCancelUnsupported)
	}
	removed, err := canceler.CancelSteer(ctx, sess.ID, req.QueuedID)
	if err != nil {
		switch {
		case errors.Is(err, agentruntime.ErrNoActiveTurn):
			return nil, i18n.NewError(ctx, code.ChatSteerNoActive)
		case errors.Is(err, agentruntime.ErrSteerNotFound):
			return nil, i18n.NewError(ctx, code.ChatCancelNotFound)
		default:
			return nil, i18n.NewError(ctx, code.ChatSteerInternal)
		}
	}
	if removed == nil {
		removed = []string{}
	}
	return &CancelQueuedResponse{Removed: removed}, nil
}

// newQueuedID generates a UUID v4 string used as the SteerInbox / cago
// pending-steer key. Same approach as agentruntime/claudecode.newUUIDv4 but
// kept local to avoid leaking a public helper across packages — Enqueue is
// the only caller that needs it.
func newQueuedID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		binary.BigEndian.PutUint64(b[0:8], uint64(time.Now().UnixNano()))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Regenerate 截到目标 assistant 消息之前，用同一段 user 文本重新走一遍 turn。
//
// 支持策略：
//   - builtin: history 每轮从 chat_messages 重建，删 DB 即足够。
//   - claudecode: 透传 ForkAnchor 给 runner，由 CLI fork 到新 session。
//   - codex: 根据目标 user 到末尾的 user 消息数计算 thread/rollback 的 numTurns。
//   - piagent: 透传持久化的精确 user entry ID；旧空 anchor 显式拒绝。
func (s *chatSvc) Regenerate(ctx context.Context, req *RegenerateRequest) (*SendResponse, error) {
	if req == nil || req.SessionID <= 0 || req.MessageID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}

	sess, err := chat_repo.Session().Find(ctx, req.SessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if sess == nil {
		return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}

	target, err := chat_repo.Message().Find(ctx, req.MessageID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if target == nil || (target.SessionID >= 0 && target.SessionID != sess.ID) {
		return nil, i18n.NewError(ctx, code.ChatMessageNotFound)
	}
	if target.Role != "assistant" {
		return nil, i18n.NewError(ctx, code.ChatRegenerateNotAssistant)
	}

	a, be, prov, err := s.resolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID)
	if err != nil {
		return nil, err
	}
	gate, err := s.acquireTurnGate(ctx, sess, be)
	if err != nil {
		return nil, err
	}
	gateOwned := true
	defer func() {
		if gateOwned {
			gate.lock.Unlock()
		}
	}()
	if gate.reconciled {
		target, err = chat_repo.Message().Find(ctx, req.MessageID)
		if err != nil {
			return nil, operationFailedWithCause(ctx, err)
		}
		if target == nil || target.SessionID != sess.ID {
			return nil, i18n.NewError(ctx, code.ChatMessageNotFound)
		}
		if target.Role != "assistant" {
			return nil, i18n.NewError(ctx, code.ChatRegenerateNotAssistant)
		}
	}
	if target.SessionID != sess.ID {
		return nil, i18n.NewError(ctx, code.ChatMessageNotFound)
	}

	// 找紧邻 target 之前的最后一条 user 消息（按 seq）。
	all, err := chat_repo.Message().List(ctx, sess.ID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	var userAnchor *chat_entity.Message
	for _, m := range all {
		if m.Seq < target.Seq && m.Role == "user" {
			userAnchor = m
		}
	}
	if userAnchor == nil {
		return nil, i18n.NewError(ctx, code.ChatRegenerateNoUserAnchor)
	}
	userBlocks, err := userAnchor.GetBlocks()
	if err != nil {
		return nil, i18n.NewError(ctx, code.ChatBlocksMalformed)
	}

	forkAnchor, ferr := s.backendForkAnchor(ctx, sess, be, userAnchor)
	if ferr != nil {
		return nil, ferr
	}

	if err := s.permissionModes().ApplyRequested(ctx, sess, be, req.PermissionMode, false); err != nil {
		return nil, err
	}

	// preTx 在同一事务里先截掉 user 锚点（含）开始的全部历史，
	// 然后 startTurn 的标准路径会以新的 NextSeq 写回 user + assistant。
	anchorSeq := userAnchor.Seq
	var replacement *transcriptReplacementLifecycle
	preTx := func(txCtx context.Context) error {
		_, derr := chat_repo.Message().DeleteFromSeq(txCtx, sess.ID, anchorSeq)
		return derr
	}
	if be.IsPiAgent() {
		replacement = newTranscriptReplacementLifecycle(sess.ID, anchorSeq, req.MessageID)
		preTx = nil
	}
	// 会话 provider_key 优先于 agent 绑定解析（决策 3），与 send 同源；provider-default
	// 所选供应商缺失/停用/禁用/不兼容 → 回退 agent 绑定并追加一条持久 notice（决策 8）；
	// fixed-model 目标失效 → 严格阻止本轮（决策 7）。
	prov, providerFallbackNotice, err := s.resolveSessionProvider(ctx, sess, be, prov)
	if err != nil {
		return nil, err
	}
	gateOwned = false
	return s.startTurn(ctx, sess, a, be, prov, userBlocks, preTx, replacement, forkAnchor, turnExtras{
		providerFallbackNotice: providerFallbackNotice,
	}, gate.lock)
}

// Edit 编辑历史 user 消息后用新文本重跑 turn。截到目标 user 消息（含）开始的全部
// chat_messages，把新文本作为 user 消息再走 startTurn。
//
// 跟 Regenerate 的区别：
//   - target 必须是 user 而非 assistant
//   - 用 req.Text 替换原始文本（Regenerate 是回放原文）
//   - fork anchor 直接拿 target.ForkAnchor（Regenerate 是先找上一条 user）
func (s *chatSvc) Edit(ctx context.Context, req *EditRequest) (*SendResponse, error) {
	if req == nil || req.SessionID <= 0 || req.MessageID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	if len(text) > chat_entity.MessageTextMaxBytes {
		return nil, i18n.NewError(ctx, code.ChatTextTooLong)
	}

	sess, err := chat_repo.Session().Find(ctx, req.SessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if sess == nil {
		return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}

	target, err := chat_repo.Message().Find(ctx, req.MessageID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if target == nil || (target.SessionID >= 0 && target.SessionID != sess.ID) {
		return nil, i18n.NewError(ctx, code.ChatMessageNotFound)
	}
	if target.Role != "user" {
		return nil, i18n.NewError(ctx, code.ChatEditNotUser)
	}

	a, be, prov, err := s.resolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID)
	if err != nil {
		return nil, err
	}
	gate, err := s.acquireTurnGate(ctx, sess, be)
	if err != nil {
		return nil, err
	}
	gateOwned := true
	defer func() {
		if gateOwned {
			gate.lock.Unlock()
		}
	}()
	if gate.reconciled {
		target, err = chat_repo.Message().Find(ctx, req.MessageID)
		if err != nil {
			return nil, operationFailedWithCause(ctx, err)
		}
		if target == nil || target.SessionID != sess.ID {
			return nil, i18n.NewError(ctx, code.ChatMessageNotFound)
		}
		if target.Role != "user" {
			return nil, i18n.NewError(ctx, code.ChatEditNotUser)
		}
	}
	if target.SessionID != sess.ID {
		return nil, i18n.NewError(ctx, code.ChatMessageNotFound)
	}
	targetBlocks, err := target.GetBlocks()
	if err != nil {
		return nil, i18n.NewError(ctx, code.ChatBlocksMalformed)
	}

	forkAnchor, ferr := s.backendForkAnchor(ctx, sess, be, target)
	if ferr != nil {
		return nil, ferr
	}

	if err := s.permissionModes().ApplyRequested(ctx, sess, be, req.PermissionMode, false); err != nil {
		return nil, err
	}

	anchorSeq := target.Seq
	var replacement *transcriptReplacementLifecycle
	preTx := func(txCtx context.Context) error {
		_, derr := chat_repo.Message().DeleteFromSeq(txCtx, sess.ID, anchorSeq)
		return derr
	}
	if be.IsPiAgent() {
		replacement = newTranscriptReplacementLifecycle(sess.ID, anchorSeq, req.MessageID)
		preTx = nil
	}
	// 会话 provider_key 优先于 agent 绑定解析（决策 3），与 send 同源；provider-default
	// 所选供应商缺失/停用/禁用/不兼容 → 回退 agent 绑定并追加一条持久 notice（决策 8）；
	// fixed-model 目标失效 → 严格阻止本轮（决策 7）。
	prov, providerFallbackNotice, err := s.resolveSessionProvider(ctx, sess, be, prov)
	if err != nil {
		return nil, err
	}
	gateOwned = false
	return s.startTurn(
		ctx,
		sess,
		a,
		be,
		prov,
		replaceTextPreserveImages(text, targetBlocks),
		preTx,
		replacement,
		forkAnchor,
		turnExtras{providerFallbackNotice: providerFallbackNotice},
		gate.lock,
	)
}

// transcriptReplacementLifecycle owns one Pi replacement generation. Its
// marker-derived hidden namespace preserves the exact original rows until the
// prepared process acknowledges the prompt.
type transcriptReplacementLifecycle struct {
	sessionID        int64
	fromSeq          int
	requestMessageID int64
	recovery         *chat_repo.ReplacementRecovery
}

const transcriptRecoveryTimeout = 5 * time.Second

// startTurn is the common tail shared by Send and Regenerate: acquire the
// per-session lock, persist a fresh user+assistant pair in a transaction (with
// an optional pre-step running inside the same tx), then kick off runTurn. Pi
// replacements first prepare, then atomically activate recovery+messages+native
// identity before Start is allowed to send the prompt.
//
// Caller is responsible for resolving sess/a/be/prov consistently with the
// session's actual agent (Send for new sessions, Regenerate for in-place
// rewind). userBlocks is the user message body that will be re-played to the runtime.
//
// preTx, if non-nil, runs at the very top of a normal turn transaction. Non-Pi
// rewinds use it to free seq numbers. Pi replacement activation owns its full
// transaction separately. Returning an error aborts the whole turn (and unlocks).
// turnExtras 是从 SendRequest 透传到 runTurn 的领域无关可选项;普通会话一律零值。
// 在同一会话的自动续轮(auto-continue)里需要保持不变,所以随 runTurn 一路携带。
type turnExtras struct {
	// peerSource marks the normal persisted user row as having been submitted
	// by an attached account peer. Local sends keep this zero value.
	peerSource peerMessageSource
	// emitTurnStartedBypass: 见 SendRequest.EmitTurnStartedBypass。startTurn 在建好
	// assistant 消息后, 经会话级旁路把 per-turn 流名推给已打开的查看者。
	emitTurnStartedBypass bool
	// providerFallbackNotice:会话 provider_key 指向的供应商缺失/停用/不兼容,本轮回退
	// agent 绑定(spec 决策 8)时由 send 填充;runTurn 把它追加成一条持久 transcript notice。
	// 随自动续轮一路携带 —— 回退期间每轮都提示,与 #26 偏离提示"每次发生都提示"先例一致。
	providerFallbackNotice *blocks.NoticeBlock
}

type sessionTurnGate struct {
	lock       *trylockMutex
	reconciled bool
}

func (s *chatSvc) acquireTurnGate(
	ctx context.Context,
	sess *chat_entity.Session,
	be *agent_backend_entity.AgentBackend,
) (*sessionTurnGate, error) {
	lock := s.lockFor(sess.ID)
	if !lock.TryLock() {
		return nil, i18n.NewError(ctx, code.ChatSendInFlight)
	}
	reconciled, err := s.reconcileTranscriptReplacement(ctx, sess, be)
	if err != nil {
		lock.Unlock()
		return nil, operationFailedWithCause(ctx, err,
			zap.Int64("sessionId", sess.ID),
			zap.String("backendType", be.Type))
	}
	return &sessionTurnGate{lock: lock, reconciled: reconciled}, nil
}

func (s *chatSvc) startTurn(
	ctx context.Context,
	sess *chat_entity.Session,
	a *agent_entity.Agent,
	be *agent_backend_entity.AgentBackend,
	prov *llm_provider_entity.LLMProvider,
	userBlocks []blocks.ContentBlock,
	preTx func(txCtx context.Context) error,
	replacement *transcriptReplacementLifecycle,
	forkAnchor string,
	extras turnExtras,
	prelocked *trylockMutex,
) (*SendResponse, error) {
	lock := prelocked
	if lock == nil {
		gate, err := s.acquireTurnGate(ctx, sess, be)
		if err != nil {
			return nil, err
		}
		lock = gate.lock
	}
	ts := &turnStart{
		svc:         s,
		sess:        sess,
		a:           a,
		be:          be,
		prov:        prov,
		userBlocks:  userBlocks,
		preTx:       preTx,
		replacement: replacement,
		forkAnchor:  forkAnchor,
		extras:      extras,
		lock:        lock,
	}
	if err := ts.buildMessages(ctx); err != nil {
		return nil, err
	}
	if err := ts.piPreflight(ctx); err != nil {
		return nil, err
	}
	if err := ts.persistTurnMessages(ctx); err != nil {
		return nil, err
	}
	if err := ts.startPreparedRun(ctx); err != nil {
		return nil, err
	}
	stream := StreamName(ts.sess.ID, ts.assistantMsg.ID)

	ts.emitTurnStarted(ctx, stream)

	ts.dispatchTurn(ctx, stream)

	return &SendResponse{
		SessionID:          ts.sess.ID,
		UserMessageID:      ts.userMsg.ID,
		AssistantMessageID: ts.assistantMsg.ID,
		Stream:             stream,
	}, nil
}

func (s *chatSvc) discardPreparedTurn(sessionID int64, prepared *preparedTurnRun) {
	if prepared == nil {
		return
	}
	if prepared.deferred != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), transcriptRecoveryTimeout)
		_ = prepared.deferred.Close(closeCtx)
		cancel()
	} else if prepared.events != nil {
		if aborter, ok := prepared.runner.(agentruntime.Aborter); ok {
			_, _ = aborter.Abort(context.Background(), sessionID, 0)
		}
	}
	if prepared.events == nil {
		prepared.releaseResources()
		return
	}
	gogo.Go(func() error {
		for range prepared.events {
		}
		prepared.releaseResources()
		return nil
	}, gogo.WithIgnorePanic())
}

func (s *chatSvc) startCompactTurn(
	ctx context.Context,
	sess *chat_entity.Session,
	a *agent_entity.Agent,
	be *agent_backend_entity.AgentBackend,
	prov *llm_provider_entity.LLMProvider,
	extras turnExtras,
	prelocked *trylockMutex,
) (*CompactResponse, error) {
	lock := prelocked
	if lock == nil {
		gate, err := s.acquireTurnGate(ctx, sess, be)
		if err != nil {
			return nil, err
		}
		lock = gate.lock
	}

	// 解析本轮执行侧配置（EffectiveLLMConfig v1 seam）：compact 也是执行入口，
	// assistantMsg.Model 用解析出的 ModelID 占位（真正执行后由 result.Model 覆盖）。
	cfg, err := s.effectiveLLMForNonRemoteTurn(ctx, sess, be, prov)
	if err != nil {
		lock.Unlock()
		return nil, err
	}
	model := ""
	if cfg != nil {
		model = cfg.ModelID
	}
	assistantMsg := &chat_entity.Message{
		SessionID:         sess.ID,
		DeviceFingerprint: be.DeviceFingerprint,
		Role:              "assistant",
		BlocksJSON:        "[]",
		Model:             model,
	}

	if err := db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		txCtx := db.WithContextDB(ctx, tx)
		nextSeq, err := chat_repo.Message().NextSeq(txCtx, sess.ID)
		if err != nil {
			return err
		}
		assistantMsg.Seq = nextSeq
		if err := chat_repo.Message().Create(txCtx, assistantMsg); err != nil {
			return err
		}
		sess.AgentStatus = "running"
		sess.NeedsAttention = false
		sess.LastMessageAt = time.Now().UnixMilli()
		return chat_repo.Session().Update(txCtx, sess)
	}); err != nil {
		lock.Unlock()
		return nil, operationFailedWithCause(ctx, err,
			zap.Int64("sessionId", sess.ID),
			zap.Int64("agentId", a.ID),
			zap.String("backendType", be.Type))
	}

	stream := StreamName(sess.ID, assistantMsg.ID)
	s.markStreamRunningForTest(assistantMsg.ID)
	runCtx := db.WithContextDB(context.Background(), db.Ctx(ctx))
	turnCtx, cancel := context.WithCancel(runCtx)
	turnControl := &activeTurnControl{cancel: cancel}
	s.activeCancels.Store(sess.ID, turnControl)
	gogo.Go(func() error {
		defer lock.Unlock()
		defer s.markStreamDoneForTest(assistantMsg.ID)
		defer func() {
			s.activeCancels.CompareAndDelete(sess.ID, turnControl)
			cancel()
		}()
		s.runTurn(turnCtx, sess, a, be, prov, nil, assistantMsg, stream, "", true, nil, extras)
		return nil
	}, gogo.WithIgnorePanic())

	return &CompactResponse{
		SessionID:          sess.ID,
		AssistantMessageID: assistantMsg.ID,
		Stream:             stream,
	}, nil
}

// markSessionWaiting 把 session 翻成「等用户操作」态：AgentStatus="waiting"，
// 并推送带派生 NeedsAttention=true 的 StreamSessionStatus patch 给前端。turn 进行中遇到 AskUserQuestion / ToolPermission
// 请求时调用，应答后由 markSessionRunning 翻回。
//
// 写库用 context.WithoutCancel(ctx)：用户在等待期间点「停止」会 cancel turnCtx，但等待状态本身
// 仍需要持久化（否则下次 LoadSession 会显示旧的 running，sidebar attention 也会丢）。
// 当前 sess.AgentStatus 已经是目标值时短路，避免重复 emit。
func (s *chatSvc) markSessionWaiting(ctx context.Context, sess *chat_entity.Session, stream string) {
	if sess == nil || sess.IsWaitingForUser() {
		return
	}
	sess.AgentStatus = "waiting"
	sess.NeedsAttention = true
	_ = chat_repo.Session().Update(context.WithoutCancel(ctx), sess)
	logger.Ctx(ctx).Info("chat_svc: session_status emit",
		zap.Int64("sessionId", sess.ID),
		zap.String("stream", stream),
		zap.String("agentStatus", sess.AgentStatus),
		zap.Bool("needsAttention", sess.NeedsAttention),
		zap.String("source", "markSessionWaiting"))
	s.emitter.Emit(ctx, stream, ChatStreamEvent{
		Kind: StreamSessionStatus,
		SessionStatus: &ChatSessionStatusPatch{
			AgentStatus:    sess.AgentStatus,
			NeedsAttention: sess.NeedsAttention,
			BgRunning:      s.bgRunningActive(sess.ID),
		},
	})
}

// markSessionRunning 把 session 从 waiting 翻回 running：清掉 NeedsAttention。
// 在 EventAskUserQuestionAnswered / EventToolPermissionResolved 处调用。
func (s *chatSvc) markSessionRunning(ctx context.Context, sess *chat_entity.Session, stream string) {
	if sess == nil || (sess.AgentStatus == "running" && !sess.IsWaitingForUser()) {
		return
	}
	sess.AgentStatus = "running"
	sess.NeedsAttention = false
	_ = chat_repo.Session().Update(context.WithoutCancel(ctx), sess)
	logger.Ctx(ctx).Info("chat_svc: session_status emit",
		zap.Int64("sessionId", sess.ID),
		zap.String("stream", stream),
		zap.String("agentStatus", sess.AgentStatus),
		zap.Bool("needsAttention", sess.NeedsAttention),
		zap.String("source", "markSessionRunning"))
	s.emitter.Emit(ctx, stream, ChatStreamEvent{
		Kind: StreamSessionStatus,
		SessionStatus: &ChatSessionStatusPatch{
			AgentStatus:    sess.AgentStatus,
			NeedsAttention: sess.NeedsAttention,
			BgRunning:      s.bgRunningActive(sess.ID),
		},
	})
}

// persistSessionStatus 持久化 turn 收尾的 session 状态翻转(idle/error/waiting)：
// 写失败时重试一次,仍失败则把错误返回出来 —— 不再像旧的 `_ = Update` 那样静默吞掉。
//
// 背景:状态写丢失会让会话永久停在 running(turn 其实已 finalize 成 idle),
// 唯一兜底是下次启动 ResetActiveSessions 把残留 running 翻成 error。session 162
// 正是 abort finalize 成 idle 后这一刀写库失败被 `_` 吞掉,结果 DB 卡在 running、
// updatetime 停在写丢失之前。这里做到「重试 + 不静默」,并把真实失败原因(锁竞争 /
// 收尾期连接关闭等)暴露到日志,便于后续定位根因。
func (s *chatSvc) persistSessionStatus(ctx context.Context, sess *chat_entity.Session) error {
	if sess == nil {
		return nil
	}
	err := chat_repo.Session().Update(ctx, sess)
	if err == nil {
		return nil
	}
	logger.Ctx(ctx).Warn("chat_svc: session status persist failed, retrying",
		zap.Int64("sessionId", sess.ID),
		zap.String("agentStatus", sess.AgentStatus),
		zap.Error(err))
	if err := chat_repo.Session().Update(ctx, sess); err != nil {
		logger.Ctx(ctx).Error("chat_svc: session status persist failed after retry",
			zap.Int64("sessionId", sess.ID),
			zap.String("agentStatus", sess.AgentStatus),
			zap.Error(err))
		return err
	}
	return nil
}

type preparedTurnRun struct {
	runner     agentruntime.Runtime
	events     <-chan agentruntime.Event
	result     *agentruntime.RunResult
	req        agentruntime.RunRequest
	deferred   piagentrt.PreparedRun
	deferStart bool
	release    func()
	releaseOne sync.Once
}

func (p *preparedTurnRun) providerSessionIDBeforeStart() (string, error) {
	if p == nil || p.deferred == nil {
		return "", errors.New("pi prepared run has no pre-prompt identity")
	}
	identity, ok := p.deferred.(piagentrt.PreparedRunIdentity)
	if !ok {
		return "", errors.New("pi prepared run does not expose pre-prompt identity")
	}
	providerSessionID := strings.TrimSpace(identity.ProviderSessionID())
	if providerSessionID == "" {
		return "", errors.New("pi prepared run returned an empty pre-prompt identity")
	}
	return providerSessionID, nil
}

func (p *preparedTurnRun) start(ctx context.Context) error {
	if p == nil {
		return nil
	}
	var err error
	switch {
	case p.deferred != nil:
		p.events, p.result, err = p.deferred.Start(ctx)
	case p.deferStart:
		p.events, p.result, err = p.runner.Run(ctx, p.req)
	}
	return err
}

func (p *preparedTurnRun) releaseResources() {
	if p == nil {
		return
	}
	p.releaseOne.Do(p.release)
}

func (s *chatSvc) prepareTurnRun(
	ctx context.Context,
	sess *chat_entity.Session,
	a *agent_entity.Agent,
	be *agent_backend_entity.AgentBackend,
	prov *llm_provider_entity.LLMProvider,
	userMsg, assistantMsg *chat_entity.Message,
	forkAnchor string,
	compact bool,
	deferPrompt bool,
) (*preparedTurnRun, error) {
	runner, release, err := s.selectTurnRunner(ctx, sess, be)
	if err != nil {
		return nil, err
	}
	s.bindLocalPiAbort(ctx, sess.ID, be, runner)
	fail := func(err error) (*preparedTurnRun, error) {
		release()
		return nil, err
	}
	if userMsg != nil && messageHasImage(userMsg) && !runner.Capabilities().Has(capability.CapImageInput) {
		return fail(agentruntime.ErrUnsupported)
	}

	req, err := s.buildRunRequest(ctx, sess, a, be, prov, userMsg, assistantMsg, forkAnchor, compact, runner)
	if err != nil {
		return fail(err)
	}

	prepared := &preparedTurnRun{runner: runner, req: req, release: release}
	if deferPrompt {
		if preparer, ok := runner.(piagentrt.RunPreparer); ok {
			deferred, err := preparer.PrepareRun(ctx, req)
			if err != nil {
				return fail(s.mapTurnError(ctx, sess, be, err))
			}
			prepared.deferred = deferred
		} else {
			prepared.deferStart = true
		}
		return prepared, nil
	}

	events, result, err := runner.Run(ctx, req)
	if err != nil {
		// 用户在 Run 返回前就点了停止(网关型后端要先握手,这个窗口是真实存在的):
		// 这是中止而不是故障,交给调用方按 idle 收敛,别让会话卡在 running / 弹错误卡。
		if s.turnAbortedByUser(sess.ID, err) {
			return fail(errTurnAbortedBeforeStream)
		}
		return fail(s.mapTurnError(ctx, sess, be, err))
	}
	prepared.events = events
	prepared.result = result
	return prepared, nil
}

// selectTurnRunner 解析本轮该用的 runtime:远端 backend 从池里 borrow(带 release),
// 本地按类型取已注册实现。真错必须透传给调用方交给 failTurn。
//
// 成功时第二个返回值恒非 nil(本地档是 no-op),调用方必须调它;出错时交回 nil,
// 调用方直接 return 即可 —— 远端借用失败本就没占住任何租约(见
// borrowRemoteRuntimeForTurn 的错误契约)。
func (s *chatSvc) selectTurnRunner(
	ctx context.Context,
	sess *chat_entity.Session,
	be *agent_backend_entity.AgentBackend,
) (agentruntime.Runtime, func(), error) {
	var (
		runner  agentruntime.Runtime
		release = func() {}
		err     error
	)
	if beTargetsRemote(be) {
		runner, release, err = s.borrowRemoteRuntimeForTurn(ctx, be, sess.ID)
	} else {
		runner, err = s.selectRunner(ctx, be, sess.ID)
	}
	if err != nil {
		// 真错(RemoteRunnerDialFailed / AgentBackendInvalidDevice / AgentBackendInvalidType)
		// 必须透传给调用方交给 failTurn,否则前端永远只看到误导的
		// "unsupported backend type: X",远端 daemon 离线 / DeviceID 失效 /
		// 类型未注册三种情况无法区分。
		fields := make([]zap.Field, 0, 6)
		fields = append(fields,
			zap.Int64("sessionID", sess.ID),
			zap.String("backendType", be.Type),
			zap.String("deviceID", be.DeviceFingerprint),
		)
		fields = append(fields, chatRuntimeErrorLogFields(err)...)
		logger.Ctx(ctx).Error("chat_svc.prepareTurnRun: selectRunner failed", fields...)
		return nil, nil, err
	}
	return runner, release, nil
}

// buildRunRequest 组出本轮下发给 runtime 的 RunRequest:cwd / 生效 LLM 配置 /
// builtin 历史 / 网关签名 / 权限模式。调用方在出错时负责 release。
func (s *chatSvc) buildRunRequest(
	ctx context.Context,
	sess *chat_entity.Session,
	a *agent_entity.Agent,
	be *agent_backend_entity.AgentBackend,
	prov *llm_provider_entity.LLMProvider,
	userMsg, assistantMsg *chat_entity.Message,
	forkAnchor string,
	compact bool,
	runner agentruntime.Runtime,
) (agentruntime.RunRequest, error) {
	cwd, err := resolveSessionCwd(ctx, sess, be)
	if err != nil {
		return agentruntime.RunRequest{}, err
	}
	// 解析本轮执行侧配置（EffectiveLLMConfig v1 seam）：provider-default 在每轮准备时
	// 解析 Provider 当前默认模型；远端 backend 由 daemon 自家解析，desktop 不发本地结果。
	// 解析失败（配置损坏）阻止本轮，不静默降级。
	cfg, err := s.effectiveLLMForNonRemoteTurn(ctx, sess, be, prov)
	if err != nil {
		return agentruntime.RunRequest{}, err
	}
	// 项目的账号级同步标识随一轮过线：远端把它记进自己的会话行，日活跃统计据此
	// 按项目分组（那条通道只上行计数、不上行路径，服务端推不出来）。
	projectSyncID, err := projectSyncIDOfSession(ctx, sess)
	if err != nil {
		return agentruntime.RunRequest{}, err
	}
	req := agentruntime.RunRequest{
		Backend:           be,
		Provider:          prov,
		Effective:         cfg,
		AgentID:           a.ID,
		SessionID:         sess.ID,
		Cwd:               cwd,
		Title:             sess.Title,
		AgentSyncID:       a.SyncID,
		ProjectSyncID:     projectSyncID,
		SystemPrompt:      strings.Join(a.GetPrompt(), "\n"),
		ProviderSessionID: sess.ProviderSessionID,
		// 挂账修复(2026-08-11):本地没有可续的原生会话(regenerate 无锚点 / provider 会话
		// 失效恢复 / 首轮)时声明 freshSession,远端 daemon 据此不拿落库旧 id 续话。
		FreshSession:   strings.TrimSpace(sess.ProviderSessionID) == "",
		Compact:        compact,
		ForkAnchor:     forkAnchor,
		MCPServers:     appendTurnMCP(ctx, nil, a, sess.ID, runner.Capabilities().Has(capability.CapMCPTools)),
		EnabledPlugins: enabledPluginsForTurn(ctx, a, be.ID, runner.Capabilities().Has(capability.CapSkills)),
	}
	if userMsg != nil {
		req.UserText = textOfMessage(userMsg)
		if bs, err := userMsg.GetBlocks(); err == nil {
			req.UserBlocks = bs
		}
	}
	if be.IsBuiltin() {
		// builtin 没有持久化 session — 把历史从 chat_messages 重建后透传。
		msgs, err := chat_repo.Message().List(ctx, sess.ID)
		if err != nil {
			return agentruntime.RunRequest{}, err
		}
		history := make([]agentruntime.HistoryMessage, 0, len(msgs))
		for _, m := range msgs {
			if m.ID == assistantMsg.ID || (userMsg != nil && m.ID == userMsg.ID) {
				continue
			}
			bs, _ := m.GetBlocks()
			history = append(history, agentruntime.HistoryMessage{Role: m.Role, Blocks: bs})
		}
		req.History = history
	}
	if beTargetsRemote(be) {
		// 远端 backend: daemon 自家有 ProviderLookup + Gateway,该自家解。
		// GatewayURL/Token 是 desktop 的 127.0.0.1，Provider 又含明文 APIKey，
		// 都不跨机器；wire 透传 effectiveProviderKey（会话 provider_key 优先，
		// 决策 9），daemon 按它从自己的配置解析；无会话 key 时回落 agent 绑定。
		req.LLMProviderKey = effectiveProviderKey(sess, be)
		req.Provider = nil
	} else if shouldSignChatGateway(be, prov) {
		// Claude Code local 需要 gateway token 给 PostToolUse hook；Codex local
		// 没有 hook，只有本轮存在 effective provider 时才该走 gateway（决策 6），
		// 否则会覆盖其原生 login 并误打到本地 gateway。
		//
		// 按 prov 路由而不是 be.LLMProviderKey：prov 是 turn 入口按会话 provider_key
		// 覆盖解析出来的那家（缺失/停用已回退过），也正是本轮 `--model` 用的那家 ——
		// 会话换了供应商，token 的上游随之改变，字符串不变（决策 3）。modelKey 与
		// effectiveLLMForNonRemoteTurn 同一口径（sessionModelKeyFor）：会话固定模型时带
		// ModelKey（fixed-model 路由到指定模型），provider-default 时为空串（Gateway
		// 每轮解析 Provider 当前默认，决策 9）。
		req.GatewayURL, req.GatewayToken = s.signChatTokenFor(ctx, be, sess.ID, providerKeyOf(prov), sessionModelKeyFor(sess, be, prov))
		// 本轮有 effective provider 却拿不到网关(未注入/未运行)：LLM 本该经本机网关转发
		// 到所选供应商,此时装不上 ANTHROPIC_* / codex model_provider,子进程会**静默**
		// 退回 CLI 自身登录态,把这段对话打到用户没选的那家上游。与 resolveAgentBackend
		// 对「backend 已绑 provider」那半的判定同一口径 —— 那半只看 be.LLMProviderKey,
		// 覆盖不到「登录态 backend 上会话自己选了供应商」这条新路径(决策 6/7)。
		// 只对真正经网关取 LLM 的后端成立,见 gatewayRoutesLLM。
		if prov != nil && gatewayRoutesLLM(be) && (req.GatewayURL == "" || req.GatewayToken == "") {
			return agentruntime.RunRequest{}, i18n.NewError(ctx, code.ChatBackendGatewayUnavailable)
		}
	}
	switch agent_backend_entity.BackendType(be.Type) {
	case agent_backend_entity.TypeClaudeCode:
		req.PermissionMode = ipc.NormalizeStoredPermissionMode(agent_backend_entity.TypeClaudeCode, sess.PermissionMode)
	case agent_backend_entity.TypeCodex:
		req.CollaborationMode = ipc.NormalizeStoredPermissionMode(agent_backend_entity.TypeCodex, sess.PermissionMode)
	}
	return req, nil
}

// errTurnAbortedBeforeStream 表示这一轮在 runner.Run 返回之前就被用户点了停止。
// 它不是故障:调用方走 abortTurnBeforeStream 按 idle 收敛,而不是 failTurn。
var errTurnAbortedBeforeStream = errors.New("chat_svc: turn aborted before stream")

func (s *chatSvc) bindLocalPiAbort(
	ctx context.Context,
	sessionID int64,
	be *agent_backend_entity.AgentBackend,
	runner agentruntime.Runtime,
) {
	if be == nil || !be.IsPiAgent() || beTargetsRemote(be) || runner == nil {
		return
	}
	aborter, ok := runner.(agentruntime.Aborter)
	if !ok {
		return
	}
	raw, ok := s.activeCancels.Load(sessionID)
	if !ok {
		return
	}
	control, _ := raw.(*activeTurnControl)
	control.setGracefulAbort(aborter)
}

func (s *chatSvc) persistUserAnchor(
	ctx context.Context,
	userMsg *chat_entity.Message,
	anchor string,
	hardFailure bool,
) error {
	if userMsg == nil || strings.TrimSpace(anchor) == "" {
		return nil
	}
	userMsg.ForkAnchor = anchor
	if !hardFailure {
		_ = chat_repo.Message().Update(ctx, userMsg)
		return nil
	}
	if err := chat_repo.Message().Update(ctx, userMsg); err != nil {
		logger.Ctx(ctx).Warn("chat_svc.persistUserAnchor: message update failed, retrying",
			zap.Int64("sessionId", userMsg.SessionID),
			zap.Int64("messageId", userMsg.ID),
			zap.String("forkAnchor", userMsg.ForkAnchor),
			zap.Error(err))
		if retryErr := chat_repo.Message().Update(ctx, userMsg); retryErr != nil {
			logger.Ctx(ctx).Error("chat_svc.persistUserAnchor: message update failed after retry",
				zap.Int64("sessionId", userMsg.SessionID),
				zap.Int64("messageId", userMsg.ID),
				zap.String("forkAnchor", userMsg.ForkAnchor),
				zap.Error(retryErr))
			return fmt.Errorf("persist user anchor: %w", retryErr)
		}
	}
	return nil
}

func (s *chatSvc) runTurn(
	ctx context.Context,
	sess *chat_entity.Session,
	a *agent_entity.Agent,
	be *agent_backend_entity.AgentBackend,
	prov *llm_provider_entity.LLMProvider,
	userMsg, assistantMsg *chat_entity.Message,
	stream string,
	forkAnchor string,
	compact bool,
	prepared *preparedTurnRun,
	extras turnExtras,
) {
	startedAt := time.Now()

	if prepared == nil {
		var err error
		prepared, err = s.prepareTurnRun(ctx, sess, a, be, prov, userMsg, assistantMsg, forkAnchor, compact, false)
		if err != nil {
			if errors.Is(err, errTurnAbortedBeforeStream) {
				s.abortTurnBeforeStream(ctx, sess, assistantMsg, stream)
				return
			}
			s.failTurn(ctx, sess, assistantMsg, stream, err)
			return
		}
	}
	defer prepared.releaseResources()
	t := &turnRun{
		svc:          s,
		sess:         sess,
		a:            a,
		be:           be,
		prov:         prov,
		userMsg:      userMsg,
		assistantMsg: assistantMsg,
		stream:       stream,
		compact:      compact,
		extras:       extras,
		runner:       prepared.runner,
		events:       prepared.events,
		result:       prepared.result,
		req:          prepared.req,
	}
	// 登记本 turn 的活跃流名,供工具审批(BeginToolApproval)把审批卡路由到此流。
	// stream 在 SteerConsumed 分段时不变(同 turn 一个流名),Store 一次即可;收尾时清掉。
	s.activeTurnStreams.Store(sess.ID, stream)
	defer s.activeTurnStreams.Delete(sess.ID)
	t.attachRuntime(ctx)
	t.initSegment(startedAt)
	t.consumeEvents(ctx)
	t.finalize(ctx)
}

func (s *chatSvc) persistProviderSessionID(ctx context.Context, sess *chat_entity.Session, providerSessionID, reason string) {
	sid := strings.TrimSpace(providerSessionID)
	if sess == nil || sid == "" || sid == sess.ProviderSessionID {
		return
	}
	sess.SetProviderSession(sid)
	if err := chat_repo.Session().Update(context.WithoutCancel(ctx), sess); err != nil {
		logger.Ctx(ctx).Warn("chat_svc: persist provider_session_id failed",
			zap.Int64("sessionId", sess.ID),
			zap.String("providerSessionID", sid),
			zap.String("reason", reason),
			zap.Error(err))
	}
}

func eventShowsProgressAfterError(ev agentruntime.Event) bool {
	switch ev.(type) {
	case agentruntime.TextDelta,
		agentruntime.ThinkingDelta,
		agentruntime.ToolCall,
		agentruntime.ToolResult,
		agentruntime.UserAskRequest,
		agentruntime.UserAskResolved,
		agentruntime.ToolPermissionRequest,
		agentruntime.ToolPermissionResolved,
		agentruntime.SubagentStarted,
		agentruntime.SubagentProgress,
		agentruntime.SubagentDone,
		agentruntime.SubagentModel,
		agentruntime.Retry,
		agentruntime.PlanUpdated,
		agentruntime.CompactBoundary,
		agentruntime.SteerConsumed:
		return true
	default:
		return false
	}
}

func shouldCheckpointAssistantAfterEvent(ev agentruntime.Event) bool {
	switch ev.(type) {
	case agentruntime.ToolResult,
		agentruntime.UserAskRequest,
		agentruntime.UserAskResolved,
		agentruntime.ToolPermissionRequest,
		agentruntime.ToolPermissionResolved,
		agentruntime.PlanUpdated:
		return true
	default:
		return false
	}
}

// persistAutoContinueTurn 把 turn 结束时 DrainPending 取到的排队消息合并成一条
// user msg + 一条空 assistant msg，落 DB 并构造 StreamSteerConsumed 事件。
// 调用方负责 emit 事件并递归调 runTurn 跑下一轮。
//
// previousAssistant 是刚收尾的 assistant（已 Update 过）；payload 里把它放在
// PreviousAssistantMessage —— 前端 applySteerConsumed 会把它定位到现有位置，
// 然后在它后面插入新的 user + assistant。
func (s *chatSvc) persistAutoContinueTurn(
	ctx context.Context,
	sess *chat_entity.Session,
	be *agent_backend_entity.AgentBackend,
	previousAssistant *chat_entity.Message,
	model string,
	pending []agentruntime.ConsumedSteer,
) (*chat_entity.Message, *chat_entity.Message, *ChatStreamEvent, error) {
	pending = s.withPeerSteerSources(pending)
	merged := joinSteerTexts(pending)
	newUser := &chat_entity.Message{SessionID: sess.ID, Role: "user", DeviceFingerprint: be.DeviceFingerprint}
	if err := newUser.SetBlocks([]blocks.ContentBlock{&blocks.TextBlock{Text: merged}}); err != nil {
		return nil, nil, nil, fmt.Errorf("set merged user blocks: %w", err)
	}
	for _, steer := range pending {
		if steer.SourcePeer != "" {
			if err := persistPeerMessageSource(newUser, peerMessageSource{Device: steer.SourcePeer, Name: steer.SourceName}); err != nil {
				return nil, nil, nil, err
			}
			break
		}
	}
	newAssistant := &chat_entity.Message{
		SessionID:         sess.ID,
		DeviceFingerprint: be.DeviceFingerprint,
		Role:              "assistant",
		BlocksJSON:        "[]",
		Model:             model,
	}

	if err := db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		txCtx := db.WithContextDB(ctx, tx)
		nextSeq, err := chat_repo.Message().NextSeq(txCtx, sess.ID)
		if err != nil {
			return err
		}
		newUser.Seq = nextSeq
		if err := chat_repo.Message().Create(txCtx, newUser); err != nil {
			return err
		}
		newAssistant.Seq = nextSeq + 1
		if err := chat_repo.Message().Create(txCtx, newAssistant); err != nil {
			return err
		}
		sess.LastMessageAt = time.Now().UnixMilli()
		return chat_repo.Session().Update(txCtx, sess)
	}); err != nil {
		return nil, nil, nil, fmt.Errorf("persist auto-continue: %w", err)
	}

	userEvent, err := toChatMessage(newUser)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("encode auto-continue user msg: %w", err)
	}
	userEvent.SessionID = sess.ID
	// R17: 合并多条残留 steers 成一条 user msg 时,取第一条非空来源(来源应一致;
	// 不一致时按先到者标,保持极简)。本机/未知保持空。
	for _, st := range pending {
		if st.SourcePeer != "" || st.SourceName != "" {
			userEvent.SourceDevice = st.SourcePeer
			userEvent.SourceDeviceName = st.SourceName
			break
		}
	}

	payload := &ChatStreamEvent{
		Kind:                     StreamSteerConsumed,
		QueuedIDs:                consumedSteerIDs(pending),
		PreviousAssistantMessage: chatMessageForEvent(sess, previousAssistant),
		UserMessages:             []ChatMessage{userEvent},
		AssistantMessage:         chatMessageForEvent(sess, newAssistant),
	}
	return newUser, newAssistant, payload, nil
}

// joinSteerTexts 把多条排队消息合并成一段（用 "\n\n" 分隔，模型常见的段落分隔
// 习惯；3 条以上也保持可读）。空切片返空串。
func joinSteerTexts(steers []agentruntime.ConsumedSteer) string {
	if len(steers) == 0 {
		return ""
	}
	if len(steers) == 1 {
		return steers[0].Text
	}
	var b strings.Builder
	for i, st := range steers {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(st.Text)
	}
	return b.String()
}

func (s *chatSvc) persistConsumedSteers(
	ctx context.Context,
	sess *chat_entity.Session,
	be *agent_backend_entity.AgentBackend,
	current *chat_entity.Message,
	acc *turn.Accumulator,
	segmentStart time.Time,
	model string,
	steers []agentruntime.ConsumedSteer,
	turnCtx *turn.TurnContext,
) (*chat_entity.Message, *ChatStreamEvent, error) {
	steers = s.withPeerSteerSources(nonEmptyConsumedSteers(steers))
	if len(steers) == 0 {
		return nil, nil, nil
	}

	_ = current.SetBlocks(acc.Finalize())
	current.DurationMs = int(time.Since(segmentStart).Milliseconds())
	current.FirstTokenMs = turnCtx.FirstTokenMs()
	turnCtx.PauseGeneration()
	current.TokensPerSec = turnCtx.TokensPerSec(current.CompletionTokens)

	userMsgs := make([]*chat_entity.Message, 0, len(steers))
	nextAssistant := &chat_entity.Message{
		SessionID:         sess.ID,
		DeviceFingerprint: be.DeviceFingerprint,
		Role:              "assistant",
		BlocksJSON:        "[]",
		Model:             model,
	}

	if err := db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		txCtx := db.WithContextDB(ctx, tx)
		if err := chat_repo.Message().Update(txCtx, current); err != nil {
			return err
		}
		nextSeq, err := chat_repo.Message().NextSeq(txCtx, sess.ID)
		if err != nil {
			return err
		}
		for i, steer := range steers {
			msg := &chat_entity.Message{
				SessionID:         sess.ID,
				DeviceFingerprint: be.DeviceFingerprint,
				Role:              "user",
				Seq:               nextSeq + i,
			}
			if err := msg.SetBlocks([]blocks.ContentBlock{&blocks.TextBlock{Text: steer.Text}}); err != nil {
				return err
			}
			if err := persistPeerMessageSource(msg, peerMessageSource{Device: steer.SourcePeer, Name: steer.SourceName}); err != nil {
				return err
			}
			if err := chat_repo.Message().Create(txCtx, msg); err != nil {
				return err
			}
			userMsgs = append(userMsgs, msg)
		}
		nextAssistant.Seq = nextSeq + len(userMsgs)
		if err := chat_repo.Message().Create(txCtx, nextAssistant); err != nil {
			return err
		}
		sess.LastMessageAt = time.Now().UnixMilli()
		return chat_repo.Session().Update(txCtx, sess)
	}); err != nil {
		return nil, nil, fmt.Errorf("persist consumed steer: %w", err)
	}

	userEvents := make([]ChatMessage, 0, len(userMsgs))
	for i, msg := range userMsgs {
		cm, err := toChatMessage(msg)
		if err != nil {
			return nil, nil, fmt.Errorf("encode consumed steer message: %w", err)
		}
		cm.SessionID = sess.ID
		// R17: 把提交方来源带进 UserMessages —— 他端消息才有;本机/未知保持空,
		// 前端看到空 sourceDevice 就不渲染来源标识。
		if i < len(steers) {
			cm.SourceDevice = steers[i].SourcePeer
			cm.SourceDeviceName = steers[i].SourceName
		}
		userEvents = append(userEvents, cm)
	}

	return nextAssistant, &ChatStreamEvent{
		Kind:                     StreamSteerConsumed,
		QueuedIDs:                consumedSteerIDs(steers),
		PreviousAssistantMessage: chatMessageForEvent(sess, current),
		UserMessages:             userEvents,
		AssistantMessage:         chatMessageForEvent(sess, nextAssistant),
	}, nil
}

func nonEmptyConsumedSteers(steers []agentruntime.ConsumedSteer) []agentruntime.ConsumedSteer {
	if len(steers) == 0 {
		return nil
	}
	out := make([]agentruntime.ConsumedSteer, 0, len(steers))
	for _, steer := range steers {
		if steer.Text == "" {
			continue
		}
		out = append(out, steer)
	}
	return out
}

func consumedSteerIDs(steers []agentruntime.ConsumedSteer) []string {
	ids := make([]string, 0, len(steers))
	for _, steer := range steers {
		if steer.QueuedID != "" {
			ids = append(ids, steer.QueuedID)
		}
	}
	return ids
}

func chatMessageForEvent(sess *chat_entity.Session, msg *chat_entity.Message) *ChatMessage {
	final, err := toChatMessage(msg)
	if err != nil {
		return nil
	}
	if sess != nil {
		final.SessionID = sess.ID
	}
	return &final
}

type trylockMutex struct{ mu sync.Mutex }

// mentionXMLRe 匹配 @ 提及序列化进消息正文的 XML 标签,捕获其可读 label。
var mentionXMLRe = regexp.MustCompile(`<(agent|project)\b[^>]*>([\s\S]*?)</(?:agent|project)>`)

// ── helpers ──────────────────────────────────────────────────────────────────

func uniqueNonZeroBackendIDs(agents []*agent_entity.Agent) []int64 {
	seen := make(map[int64]struct{}, len(agents))
	out := make([]int64, 0, len(agents))
	for _, a := range agents {
		if a.AgentBackendID == 0 {
			continue
		}
		if _, ok := seen[a.AgentBackendID]; ok {
			continue
		}
		seen[a.AgentBackendID] = struct{}{}
		out = append(out, a.AgentBackendID)
	}
	return out
}

// latestAssistantModel 反向扫描，取最近一条带模型 id 的 assistant message。
// 用于 LoadSession 解析 contextWindow——runner 上报的实际模型比 provider 静态配置准，
// 尤其在 claudecode / codex 走 CLI 自身 login 没绑 LLMProvider 的场景。
// 全部消息都没 Model 时返回空串。
func latestAssistantModel(msgs []*chat_entity.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m == nil || m.Role != "assistant" {
			continue
		}
		if m.Model != "" {
			return m.Model
		}
	}
	return ""
}

// resolveContextWindowWithRuntime 统一解析会话当前应展示的上下文窗口（tokens）。
// 0 由前端约定为「不展示用量条」。
//
// 优先级（高 → 低）：
//  1. session.ContextWindow > 0：runner 上轮上报的 modelContextWindow（codex
//     app-server 推送），最权威——用户在 CLI 内 /model 切换后立刻生效；
//  2. cfg.ContextWindow > 0：解析出模型的 ContextWindow（用户在 LLMProvider 显式
//     配置——某些 vendor 给非标准窗口、或绑精简号时表达 UI 上限的明确意图）；
//  3. latestAssistantModel → llmcatalog.Lookup：从历史 assistant message
//     的 Model 字段查表（claudecode CLI login / 显式 --model 都能命中）；
//  4. cfg.ModelID → llmcatalog.Lookup：新会话还没 assistant message 时的兜底。
//
// cfg 是执行侧解析结果（EffectiveLLMConfig v1 seam），不再直接读 Provider 行。
func resolveContextWindowWithRuntime(sess *chat_entity.Session, cfg *agentruntime.EffectiveLLMConfig, msgs []*chat_entity.Message) int {
	if sess != nil && sess.ContextWindow > 0 {
		return sess.ContextWindow
	}
	if cfg != nil && cfg.ContextWindow > 0 {
		return cfg.ContextWindow
	}
	if model := latestAssistantModel(msgs); model != "" {
		if info, ok := llmcatalog.Lookup(model); ok {
			return info.ContextWindow
		}
	}
	if cfg != nil {
		if info, ok := llmcatalog.Lookup(cfg.ModelID); ok {
			return info.ContextWindow
		}
	}
	return 0
}

func uniqueProviderKeys(backends map[int64]*agent_backend_entity.AgentBackend) []string {
	seen := make(map[string]struct{}, len(backends))
	out := make([]string, 0, len(backends))
	for _, b := range backends {
		if b == nil || b.LLMProviderKey == "" {
			continue
		}
		if _, ok := seen[b.LLMProviderKey]; ok {
			continue
		}
		seen[b.LLMProviderKey] = struct{}{}
		out = append(out, b.LLMProviderKey)
	}
	return out
}

// ── RemoteRuntime cache (Pool-backed) ────────────────────────────────────────
//
// 缓存本体已迁到 chat_svc/remotepool,接线见 remote_pool.go;这里只留 selectRunner ——
// 「本地注册表还是远端租约」这条分叉是 turn 侧的选择,不属于池。

// selectRunner 选取本地 / 远端 runner 并把它包装成统一接口。
//
//   - be.IsLocal() → agentruntime.RuntimeFor 注册表里的全局 runner;
//   - be.IsRemote() → borrowRemoteRuntime 拿/起 device-shared 的 *remote.Runtime。
//
// 同一 sessionID 多次调用对远端 cache 是 idempotent (set 语义),Steer / Abort /
// SetPermissionMode 这些 mid-turn 操作可以放心调,不会把 refcount 拉高。
// 调用方无需(也禁止)调 releaseRemoteRuntime —— 释放由 runTurn 的 defer 统一负责。
// 在 Stop / Enqueue / SetPermissionMode 这些 mid-turn 操作里 defer release 会
// 导致提前 release lease,把后续 turn 弄炸。
func (s *chatSvc) selectRunner(ctx context.Context, be *agent_backend_entity.AgentBackend, sessionID int64) (agentruntime.Runtime, error) {
	if be == nil {
		return nil, i18n.NewError(ctx, code.AgentBackendNotFound)
	}
	// 指向本机的档（DeviceID == 本机指纹）就是本地 CLI / 内置 runtime，不走远端
	// borrow——R14 把自己排到第一之后，派发到「自己」必须在本机跑起来。
	if !beTargetsRemote(be) {
		r := agentruntime.RuntimeFor(agent_backend_entity.BackendType(be.Type))
		if r == nil {
			return nil, i18n.NewError(ctx, code.AgentBackendInvalidType)
		}
		return r, nil
	}
	return s.borrowRemoteRuntime(ctx, be, sessionID)
}

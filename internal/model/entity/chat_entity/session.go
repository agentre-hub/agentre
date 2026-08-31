// Package chat_entity 维护聊天会话 / 消息的充血实体。
package chat_entity

import (
	"context"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// allowedAgentStatuses 枚举：
//   - idle     已经收尾的会话（含 abort）
//   - running  turn 进行中，模型正在产生输出
//   - waiting  turn 进行中，等待用户操作（AskUserQuestion / ToolPermission 审批）
//   - error    turn 异常终止
//
// waiting 由 chat_svc.markSessionWaiting 在等用户操作时设置，应答后由 markSessionRunning
// 翻回 running；turn 真结束时回到 idle/error。
var allowedAgentStatuses = map[string]struct{}{
	"idle":    {},
	"running": {},
	"waiting": {},
	"error":   {},
}

// SessionPurposeSubagent 是 chat_sessions.purpose 中标记「子 agent 委派会话」的值。
// chat_svc.SessionPurposeSubagentCall(请求侧 DTO)以此为唯一来源，避免两处字面量漂移。
const SessionPurposeSubagent = "subagent_call"

// Session is one open or historical chat thread scoped to a single Agent.
type Session struct {
	ID int64 `gorm:"column:id;primaryKey;autoIncrement"`
	// ConversationID 是这条对话的**全局身份**(uuid 字符串):同一条对话在桌面端、
	// agentred 与 server 三套库以及线格式上都用这一个值指称。新对话在建档那一刻由
	// chat_repo.Session().Create 铸 UUIDv7,存量行由迁移按 UUIDv5 确定性回填
	// (spec 2026-08-31 决策 1/2)。
	//
	// 它与 ID 是两件事,不合并(决策 12):ID 是被 chat_messages 等表引用的本地主键,
	// 只在本进程/本库内有意义;桌面端因此永久存在一层「conversation_id ↔ 本地主键」
	// 的翻译。写一次就不再改 —— 所以它不在 sessionUpdateWhitelist 里。
	ConversationID string `gorm:"column:conversation_id;type:text;not null;default:''"`
	AgentID        int64  `gorm:"column:agent_id;type:bigint;not null;default:0"`
	Title          string `gorm:"column:title;type:text;not null;default:''"`
	AgentStatus    string `gorm:"column:agent_status;type:text;not null;default:'idle'"`
	LastMessageAt  int64  `gorm:"column:last_message_at;type:bigint;not null;default:0"`
	// LastReadAt 是会话上次被用户「看到」的时间戳（unix ms）。sidebar 折叠态 attention
	// bubble 用 LastMessageAt > LastReadAt 判定「未读」；前端打开会话 + stream done 后
	// 经由 chat_svc.MarkSessionRead 向后端推进；早期 localStorage 字段现在落到 DB。
	LastReadAt        int64  `gorm:"column:last_read_at;type:bigint;not null;default:0"`
	ProviderSessionID string `gorm:"column:provider_session_id;type:text;not null;default:''"`
	// NeedsAttention 是 Wails / frontend 兼容字段，不落库。DB source of truth 是
	// AgentStatus=="waiting"；repo / service 出口会由 ApplyDerivedFields 回填。
	NeedsAttention bool `gorm:"-"`
	// ProjectID = 0 表示自由会话（保留老行为，spec Q5/B 兜底）；> 0 时受 project_svc 管控。
	ProjectID int64 `gorm:"column:project_id;type:bigint;not null;default:0"`
	// Cwd 是这条会话钉住的工作目录（chat_sessions.cwd）。空串 = 不钉，按老规矩
	// 每轮现算（本地 project.Path / AgentCwd 兜底，远端 project_locations）。
	//
	// 它是**导入进来的会话**的落点（spec 2026-08-26「续跑」：工作目录取磁盘转录里
	// 记录的 cwd）：claude 的 --resume 按 cwd 定位 project 目录，从 Agent / 机器 /
	// 随手对话三个入口导进来的会话若按 agent 默认目录起 CLI，那条 provider session
	// id 在那儿根本不存在。远端会话上它是**那台机器上的**路径。
	//
	// 只在建档时写入；chat_repo 的整行 Update 把它 Omit 掉，免得一份读得早的实体
	// 把它抹成空串（与 exec_* 几列同一条理由）。
	Cwd string `gorm:"column:cwd;type:text;not null;default:''"`
	// Purpose 标识会话的内部用途；普通顶层会话为空串。子 agent 委派会话(agent_call)
	// 落 SessionPurposeSubagent —— 这类会话一次性隔离、不是用户顶层会话，repo 层在所有
	// 会话列表/计数里无条件隐藏它。
	Purpose string `gorm:"column:purpose;type:text;not null;default:''"`
	// ContextWindow 是 runner 在最近一轮上报的模型上下文窗口大小（tokens）：
	//   - codex：从 thread/tokenUsage/updated 的 modelContextWindow 字段落库；
	//   - claudecode / builtin：runner 不报，恒为 0，LoadSession 走 provider/catalog 兜底。
	// 0 表示尚未探到；> 0 时是 chat_svc 解析 contextWindow 时最高优先的来源。
	ContextWindow int `gorm:"column:context_window;type:int;not null;default:0"`
	// PermissionMode 是 CLI 会话模式：
	//   - claudecode: default / acceptEdits / plan / bypassPermissions
	//   - codex: default / plan
	// 空串是历史兼容值；claudecode 视为 acceptEdits，codex 视为 default。
	// chat_svc.SetPermissionMode 落库；runTurn 启动时按 backend 归一化后传给
	// claudecode --permission-mode 或 codex turn/start collaborationMode。
	// builtin 后端不读这个字段。
	PermissionMode string `gorm:"column:permission_mode;type:text;not null;default:''"`
	// PermissionModeAtLaunch 是 CLI 子进程 spawn 时下发的 --permission-mode 值的
	// 持久化快照（claudecode 专用）。runtime 通过 set_permission_mode 切换的
	// 当前模式落在 PermissionMode；本字段仅由 runner 在 spawn / respawn 成功后
	// 写入，运行时切换不会动它。前端用它决定 pill 上的 bypass 选项是否还可点：
	// 只有以 bypass 启动的 session 才能在运行时来回切回 bypass（CLI 约束）。
	PermissionModeAtLaunch string `gorm:"column:permission_mode_at_launch;type:text;not null;default:''"`
	// ProviderKey 是会话级 LLM 供应商 key（chat_sessions.provider_key）。
	// 空串 = 跟随 agent 绑定：每轮由 chat_svc 从 be.LLMProviderKey → prov.Model 解析。
	// 非空时在解析 prov 时优先于 agent 绑定（spec 决策 2/3）。新建会话随首条消息落库，
	// 此后可由 chat_svc.SetChatSessionModelTarget 单列改写（2026-08-10 决策 1，自下一轮
	// 生效）；所指向供应商缺失/停用时回退 agent 绑定并追加持久 notice（决策 8）。
	ProviderKey string `gorm:"column:provider_key;type:text;not null;default:''"`
	// ModelKey 是会话级稳定 ModelKey（chat_sessions.model_key，ModelTarget 契约）。
	// 与 ProviderKey 组合成会话的 ModelTarget：
	//   - 两者都空 = inherit-agent（每轮解析 agent 绑定；后端钉了固定模型时沿用）；
	//   - ProviderKey 非空 + ModelKey 空 = provider-default（每轮解析该 Provider 当前默认）；
	//   - 两者都非空 = fixed-model（解析指定启用子模型）。
	// 由 chat_svc.SetChatSessionModelTarget 与 provider_key 同一条原子语句写入；普通
	// Session Save 必须 Omit 这一列，否则轮中切好的模型会被轮次开始时读出的旧实体冲掉。
	ModelKey string `gorm:"column:model_key;type:text;not null;default:''"`
	// ExecDeviceID 执行该会话的配对 daemon(paired_agentreds.id)。0 = 本机执行 ——
	// 也是老数据的默认值，语义与远端执行落地前完全一致。
	ExecDeviceID int64 `gorm:"column:exec_device_id;type:bigint;not null;default:0"`
	// ExecDeviceFingerprint 是上面那台 daemon 的实例标识：daemon 由自己的 instance
	// uuid 派生出的 "sha256:<hex>"(见 internal/daemon/identity.DaemonFingerprint)，与
	// paired_agentreds.daemon_fingerprint 同值、与 auth.connect 的 TOFU pin 同一个身份。
	// daemon 重装 / 换机 / 数据目录被清后它会变，届时 EventCursor 指向的是另一条通知日志。
	ExecDeviceFingerprint string `gorm:"column:exec_device_fingerprint;type:text;not null;default:''"`
	// EventCursor 桌面端已消费到的 daemon 通知 seq(daemon 侧 journal 里单调递增)。
	// 0 = 尚未消费。只有配合 ExecDeviceFingerprint 一起看才有意义，见 CursorValidFor。
	EventCursor int64 `gorm:"column:event_cursor;type:bigint;not null;default:0"`
	// ExecAgentBackendID 是这条会话钉住的执行目标档（R15b / 决策36）：Agent 有序
	// 执行目标列表（agent_exec_targets）里被选中的那一行的 agent_backend_id。
	// 0 = 尚未钉住 —— 首轮与全部老会话的默认值；"落到"发生在第一轮实际起在哪台，
	// 此后续轮一律回到这一档，不因排序里有更靠前的档现在可用而改派。
	//
	// 与 ExecDeviceID / ExecDeviceFingerprint 语义正交：那两列回答"哪台机器 /
	// 哪个 daemon 实例"，这一列回答"哪一档"——同一台机器上可以有多档，钉住的是档
	// 本身（连带它的 backend 配置与技能授权），不是机器。三列由同一条专用单列
	// 更新 UpdateExecDaemon 一并写入、一并加进 Update 的 Omit 清单，同生共死。
	ExecAgentBackendID int64 `gorm:"column:exec_agent_backend_id;type:bigint;not null;default:0"`
	Status             int   `gorm:"column:status;type:int;not null;default:1"`
	Createtime         int64 `gorm:"column:createtime;type:bigint;not null;default:0"`
	Updatetime         int64 `gorm:"column:updatetime;type:bigint;not null;default:0"`
}

func (*Session) TableName() string { return "chat_sessions" }

func (s *Session) IsActive() bool { return s != nil && s.Status == consts.ACTIVE }

// IsWaitingForUser returns whether this session is blocked on user input such
// as AskUserQuestion or a tool permission request.
func (s *Session) IsWaitingForUser() bool {
	return s != nil && s.AgentStatus == "waiting"
}

// ApplyDerivedFields fills non-persisted compatibility fields from persisted
// state. Call this after loading sessions from storage and before projecting to
// Wails DTOs.
func (s *Session) ApplyDerivedFields() {
	if s == nil {
		return
	}
	s.NeedsAttention = s.IsWaitingForUser()
}

// HasProviderSession 是否已绑定 cago cliagent / builtin Session id。
// 空串视为未绑定 — 首条消息时由 runner 生成并回写。
func (s *Session) HasProviderSession() bool { return s != nil && s.ProviderSessionID != "" }

// SetProviderSession 写入 cago Session id；nil receiver 无操作。
func (s *Session) SetProviderSession(id string) {
	if s == nil {
		return
	}
	s.ProviderSessionID = id
}

// RanOnDaemon 会话是否记录了远端执行位置。ExecDeviceID 为 0 表示本机执行(含老数据)。
func (s *Session) RanOnDaemon() bool { return s != nil && s.ExecDeviceID > 0 }

// CursorValidFor 判断 EventCursor 相对当前连上的这台 daemon 是否仍然有效。
// daemonFingerprint 是本次连接上的 daemon 实例标识；与会话记录的不一致(daemon 重装、
// 换机、数据目录被清)时，记录的游标指向的是另一条通知日志，必须判为失效而不是拿去拉。
func (s *Session) CursorValidFor(daemonFingerprint string) bool {
	return s != nil && daemonFingerprint != "" && s.ExecDeviceFingerprint == daemonFingerprint
}

func (s *Session) Check(ctx context.Context) error {
	if s == nil {
		return i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	if s.AgentID <= 0 {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	if _, ok := allowedAgentStatuses[s.AgentStatus]; !ok {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	return nil
}

package ipc

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// ValidatePermissionMode 用 Capabilities().PermissionModeMeta 替代 chat_svc 旧的
// normalizeStoredPermissionMode / validateRequestedPermissionMode 硬编码 switch。
//
// raw=""   → 返回 Caps.PermissionModeMeta.DefaultMode (可能也为"")
// raw 命中 AllowedModes → 原值返回
// raw 不命中 → code.ChatPermissionModeInvalid
//
// 与 ValidateRequestedPermissionMode 的差别只在空串:那一个把空串当非法请求,
// 这一个把空串当「没给,用默认」。
func ValidatePermissionMode(ctx context.Context, bt agent_backend_entity.BackendType, raw string) (string, error) {
	caps := capabilitiesFor(bt)
	mode := strings.TrimSpace(raw)
	if mode == "" {
		return caps.PermissionModeMeta.DefaultMode, nil
	}
	if slices.Contains(caps.PermissionModeMeta.AllowedModes, mode) {
		return mode, nil
	}
	return "", i18n.NewError(ctx, code.ChatPermissionModeInvalid)
}

// ── 权限模式状态机 ───────────────────────────────────────────────────────────
//
// 以下这组从 chat_svc/chat.go 迁入:它们是围绕 capability.PermissionModeMeta 的一台
// 独立状态机(解析 → 校验 → 落库 → 下发 runtime),不认识 turn / 转录 / 前端投影。
// 需要宿主的两件事(选 runner、判定末条 assistant 有没有可操作 plan 块)以窄端口注入。

// ModeDefault / ModeAcceptEdits / ModePlan / ModeBypassPermissions 是四个 mode 字面量。
// 白名单本身由各 runtime 的 PermissionModeMeta 声明,这里只给调用点一个可比较的常量。
const (
	ModeDefault           = "default"
	ModeAcceptEdits       = "acceptEdits"
	ModePlan              = "plan"
	ModeBypassPermissions = "bypassPermissions"
)

// PermissionModeMetaFor 反查 agentruntime 注册表里 runtime 的 PermissionModeMeta;
// 未注册 / 不支持 permission mode(AllowedModes 空)的 backend 返 (零值, false)。
// 替代 chat_svc 原来按 backendType 字面量分支的 4 处 switch。
func PermissionModeMetaFor(bt agent_backend_entity.BackendType) (capability.PermissionModeMeta, bool) {
	r := agentruntime.RuntimeFor(bt)
	if r == nil {
		return capability.PermissionModeMeta{}, false
	}
	meta := r.Capabilities().PermissionModeMeta
	if len(meta.AllowedModes) == 0 {
		return capability.PermissionModeMeta{}, false
	}
	return meta, true
}

// IsKnownPermissionMode 判定 mode 是否被某个已注册 runtime 接受。仅用于
// SetPermissionMode 入口的 fail-fast 预校验(避开一次 DB 查询),后续的真实
// 校验由 ValidateRequestedPermissionMode 按 backendType 精确做。
func IsKnownPermissionMode(mode string) bool {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return false
	}
	for _, r := range agentruntime.RegisteredRuntimes() {
		if slices.Contains(r.Capabilities().PermissionModeMeta.AllowedModes, mode) {
			return true
		}
	}
	return false
}

// NormalizeStoredPermissionMode 把库里存着的 mode 归一到该 backend 的白名单;
// 不支持 permission mode 的 backend 一律空串。
func NormalizeStoredPermissionMode(backendType agent_backend_entity.BackendType, raw string) string {
	mode := strings.TrimSpace(raw)
	meta, ok := PermissionModeMetaFor(backendType)
	if !ok {
		return ""
	}
	if slices.Contains(meta.AllowedModes, mode) {
		return mode
	}
	return meta.DefaultMode
}

// ValidateRequestedPermissionMode 精确校验一个**显式请求**的 mode;空串与不在白名单
// 的一律 ChatPermissionModeInvalid(与 ValidatePermissionMode 的差别正是空串语义)。
func ValidateRequestedPermissionMode(
	ctx context.Context, backendType agent_backend_entity.BackendType, raw string,
) (string, error) {
	mode := strings.TrimSpace(raw)
	if mode == "" {
		return "", i18n.NewError(ctx, code.ChatPermissionModeInvalid)
	}
	meta, ok := PermissionModeMetaFor(backendType)
	if !ok || !slices.Contains(meta.AllowedModes, mode) {
		return "", i18n.NewError(ctx, code.ChatPermissionModeInvalid)
	}
	return mode, nil
}

// CreatePermissionMode 解析新建会话的初始权限模式。planFirst 决定是否套用
// 「先 plan 后 bypass」派生: 交互式会话(有人审阅计划再批准)传 true, 自律会话
// (subagent 调用, 没人审批)传 false —— 后者必须尊重配置的 bypass
// 直接起手, 否则会卡在 plan mode 出计划等审批, 配的 bypass 从未生效。
func CreatePermissionMode(
	ctx context.Context, be *agent_backend_entity.AgentBackend, raw string, planFirst bool,
) (string, error) {
	if be == nil {
		return "", nil
	}
	backendType := agent_backend_entity.BackendType(be.Type)
	// raw 是前端偏好, 可能来自 agent 主后端的 mode 集合(空会话态改选执行目标到另一
	// 个类型后, 前端按主后端推导出的 mode 对实际后端不合法)。后端是唯一知道实际
	// 后端的地方, 在这里做边界归一: 合法就尊重, 不合法就当作没给, 回落到下面的默认
	// 派生, 而不是硬报 ChatPermissionModeInvalid —— 否则一次合法改选连第一条消息都
	// 发不出去。真正需要拒绝非法 mode 的入口是 SetPermissionMode 那条 IPC 线。
	if requested := strings.TrimSpace(raw); requested != "" {
		if mode, err := ValidateRequestedPermissionMode(ctx, backendType, requested); err == nil {
			return mode, nil
		}
	}
	// claudecode + admin 配 bypass 时, 交互式新会话以 plan 起手: CLI 仍按 bypass 启动(由
	// runtime resolveLaunchMode 保证), session.PermissionMode=plan 让前端 pill 显
	// 示 Plan, spawn 后由 runtime SetPermissionMode 把 CLI 切到 plan。"先 plan 后
	// bypass"工作流靠这条派生 + 现有 PlanApproveCard 主按钮(launch==bypass → Bypass)
	// 完成闭环。自律会话(planFirst=false)跳过这条, 直接落 bypass。
	if planFirst && be.IsClaudeCode() && strings.TrimSpace(be.DefaultPermissionMode) == ModeBypassPermissions {
		return ModePlan, nil
	}
	// backend.DefaultPermissionMode 管理员预设兜底(目前 entity.Check 仅放行
	// claudecode 写入);白名单门禁在 entity 层,chat_svc 不按 type 分支。
	if def := strings.TrimSpace(be.DefaultPermissionMode); def != "" {
		return ValidateRequestedPermissionMode(ctx, backendType, def)
	}
	meta, ok := PermissionModeMetaFor(backendType)
	if !ok {
		return "", nil
	}
	return meta.LaunchDefaultMode, nil
}

// RunnerSource 是状态机对宿主的窄端口:把 mode 下发给正在跑的 runtime 之前要先拿到
// 它(本地注册表 / 远端租约由宿主决定)。
type RunnerSource interface {
	SelectRunner(
		ctx context.Context, be *agent_backend_entity.AgentBackend, sessionID int64,
	) (agentruntime.Runtime, error)
}

// PlanProbe 是状态机对宿主的第二个窄端口:plan 块的持久化形态属于转录投影,
// 由宿主判定末条 assistant 里有没有「可操作」的那种。
type PlanProbe interface {
	HasActionablePlan(bs []blocks.ContentBlock) bool
}

// PermissionModeController 是权限模式状态机。
type PermissionModeController struct {
	runners RunnerSource
	plans   PlanProbe
	// fail 是宿主的通用失败包装(把 cause 一并透到前端与日志)。
	fail func(ctx context.Context, cause error) error
	// sessions / messages 是仓储窄端口(见 deps.go)。
	sessions SessionPort
	messages MessagePort
}

// NewPermissionModeController 装配状态机。
func NewPermissionModeController(
	runners RunnerSource, plans PlanProbe, fail func(ctx context.Context, cause error) error,
) *PermissionModeController {
	return &PermissionModeController{
		runners:  runners,
		plans:    plans,
		fail:     fail,
		sessions: sessionRepoDelegate{},
		messages: messageRepoDelegate{},
	}
}

// ApplyRequested 把随一轮请求带上来的 mode 应用到会话(空串 = 不带,直接返回)。
func (c *PermissionModeController) ApplyRequested(
	ctx context.Context,
	sess *chat_entity.Session,
	be *agent_backend_entity.AgentBackend,
	raw string,
	allowWaiting bool,
) error {
	if sess == nil || be == nil || strings.TrimSpace(raw) == "" {
		return nil
	}
	backendType := agent_backend_entity.BackendType(be.Type)
	// 不支持运行时切 mode 的 runtime(meta.SwitchableDuringTurn=false,目前 codex)
	// 在 turn 飞行中拒收 —— 切到 plan 会让 codex CLI 重起 turn,而我们已有
	// pending steer/answer 等状态不能丢。
	if meta, ok := PermissionModeMetaFor(backendType); ok && !meta.SwitchableDuringTurn &&
		(sess.AgentStatus == "running" || (sess.AgentStatus == "waiting" && !allowWaiting)) {
		return i18n.NewError(ctx, code.ChatSendInFlight)
	}
	mode, err := ValidateRequestedPermissionMode(ctx, backendType, raw)
	if err != nil {
		return err
	}
	return c.Persist(ctx, sess, be, mode)
}

// CanContinuePlanWaiting 判定一条停在 waiting 的 codex 会话能不能靠「继续」推进:
// 末条 assistant 里有可操作的 plan 块才算。
func (c *PermissionModeController) CanContinuePlanWaiting(
	ctx context.Context,
	sess *chat_entity.Session,
	be *agent_backend_entity.AgentBackend,
	allow bool,
) (bool, error) {
	if !allow || sess == nil || be == nil || sess.AgentStatus != "waiting" || !be.IsCodex() {
		return false, nil
	}
	msgs, err := c.messages.List(ctx, sess.ID)
	if err != nil {
		return false, c.fail(ctx, err)
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i] == nil || msgs[i].Role != "assistant" {
			continue
		}
		bs, err := msgs[i].GetBlocks()
		if err != nil {
			return false, i18n.NewError(ctx, code.ChatBlocksMalformed)
		}
		return c.plans.HasActionablePlan(bs), nil
	}
	return false, nil
}

// Persist 校验 mode、写库、再下发给正在跑的 runtime(实现了 PermissionModeSetter 的
// 那种)。写 DB 在 runtime 之前 —— 进程未启动时也会在下次启动生效。
func (c *PermissionModeController) Persist(
	ctx context.Context,
	sess *chat_entity.Session,
	be *agent_backend_entity.AgentBackend,
	mode string,
) error {
	backendType := agent_backend_entity.BackendType(be.Type)
	// 支持判定走 meta:AllowedModes 非空即支持。等价于原 switch
	// {ClaudeCode,Codex}/default → Unsupported,但不再有字面量耦合。
	if _, ok := PermissionModeMetaFor(backendType); !ok {
		return i18n.NewError(ctx, code.ChatPermissionModeUnsupported)
	}
	mode, err := ValidateRequestedPermissionMode(ctx, backendType, mode)
	if err != nil {
		return err
	}
	// runtime 是否能在运行时下发(setter)由"是否实现 PermissionModeSetter 接口"决定 —
	// 现状:claudecode 实现,codex 未实现(也不需要,collaborationMode 是 per-turn)。
	// 历史是按 backendType==ClaudeCode 显式 if,改成 runner type-assert 后行为不变。
	runner, rerr := c.runners.SelectRunner(ctx, be, sess.ID)
	if rerr != nil {
		return i18n.NewError(ctx, code.ChatPermissionModeUnsupported)
	}
	setter, _ := runner.(agentruntime.PermissionModeSetter)

	sess.PermissionMode = mode
	if err := c.sessions.UpdatePermissionMode(ctx, sess.ID, mode); err != nil {
		logger.Ctx(ctx).Error("permission mode persist failed",
			zap.Int64("sessionID", sess.ID),
			zap.String("backendType", be.Type),
			zap.String("mode", mode),
			zap.Error(err))
		return i18n.NewError(ctx, code.ChatPermissionModeInternal)
	}

	if setter == nil {
		return nil
	}
	if err := setter.SetPermissionMode(ctx, sess.ID, mode); err != nil {
		if errors.Is(err, agentruntime.ErrNoActiveTurn) {
			logger.Ctx(ctx).Debug("permission mode persisted but no active CLI; will apply on next spawn",
				zap.Int64("sessionID", sess.ID),
				zap.String("mode", mode))
			return nil
		}
		logger.Ctx(ctx).Error("permission mode runtime dispatch failed",
			zap.Int64("sessionID", sess.ID),
			zap.String("mode", mode),
			zap.Error(err))
		return i18n.NewError(ctx, code.ChatPermissionModeInternal)
	}
	return nil
}

// RefreshForAutoContinue 在自动接续前把内存里的 sess.PermissionMode 对齐到库里那份
// (用户可能在上一轮飞行中改过)。读不到就保持原值,不阻断接续。
func (c *PermissionModeController) RefreshForAutoContinue(ctx context.Context, sess *chat_entity.Session) {
	if sess == nil || sess.ID <= 0 {
		return
	}
	fresh, err := c.sessions.Find(ctx, sess.ID)
	if err != nil || fresh == nil {
		if err != nil {
			logger.Ctx(ctx).Warn("refresh permission mode for auto-continue failed",
				zap.Int64("sessionID", sess.ID),
				zap.Error(err))
		}
		return
	}
	sess.PermissionMode = fresh.PermissionMode
}

// SetMode 是 SetPermissionMode 那条 IPC 线的核心:会话与后端已由调用方解析好之后,
// 校验 → 在飞守卫 → 落库下发,交回真正生效的 mode。
//
// 错误码:
//   - mode 不在白名单 → ChatPermissionModeInvalid
//   - builtin / 不支持的后端 → ChatPermissionModeUnsupported
//   - 该 runtime 不支持轮内切换且会话在飞 → ChatSendInFlight
//   - DB 写失败 → ChatPermissionModeInternal
//   - runtime 返 ErrNoActiveTurn → 成功(下次 spawn 生效)
func (c *PermissionModeController) SetMode(
	ctx context.Context,
	sess *chat_entity.Session,
	be *agent_backend_entity.AgentBackend,
	requested string,
) (string, error) {
	backendType := agent_backend_entity.BackendType(be.Type)
	meta, supported := PermissionModeMetaFor(backendType)
	if !supported {
		return "", i18n.NewError(ctx, code.ChatPermissionModeUnsupported)
	}
	mode, err := ValidateRequestedPermissionMode(ctx, backendType, requested)
	if err != nil {
		return "", err
	}
	if !meta.SwitchableDuringTurn &&
		(sess.AgentStatus == "running" || sess.AgentStatus == "waiting") {
		return "", i18n.NewError(ctx, code.ChatSendInFlight)
	}
	if err := c.Persist(ctx, sess, be, mode); err != nil {
		return "", err
	}
	return mode, nil
}

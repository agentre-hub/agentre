package chat_svc

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
)

// ExecTargetChoice 是 PickExecTarget 选中的那一档：目标行本身与它解析出的 backend。
type ExecTargetChoice struct {
	Target  *agent_entity.AgentExecTarget
	Backend *agent_backend_entity.AgentBackend
}

// ExecTargetUnavailable 记录 PickExecTarget 遍历某个 Agent 的执行目标列表时，某一档为
// 什么被跳过 —— 全部不可用时用来逐档报告原因（R15）。
type ExecTargetUnavailable struct {
	AgentBackendID int64
	DeviceID       string // 空 = 本机档
	Reason         BlockReason
	Hint           string
}

// ExecTargetAvailabilityView 是单个执行目标档的可用性判定结果，供组织架构页 Agent
// 详情逐档展示（R15，任务 12）。只含原语字段——不透出 agent_backend_entity.AgentBackend
// 实体（Wails 边界只过 DTO，见 internal/app 的既有约定）；backend 名称/机器等展示信息
// 由前端按 AgentBackendID 去已经在手的 backends 列表里查，不在这里重复。
type ExecTargetAvailabilityView struct {
	AgentBackendID int64       `json:"agentBackendId"`
	Available      bool        `json:"available"`
	Reason         BlockReason `json:"reason"`
	Hint           string      `json:"hint"`
	// ProjectPath 是「这一档所在的机器上，这个项目的路径」——R15a 的改选浮层逐档
	// 展示它（路径回答「换过去在哪个目录干活」，比机器名更有信息量）。会话不绑项目
	// （projectID<=0）或那台机器上没配这个项目时为空串，界面据此不渲染这一行。
	ProjectPath string `json:"projectPath"`
	// Kind 是这一档的目标种类："local"（本机）/ "desktop"（另一台桌面端，派发走 peer
	// 中继）/ "daemon"（agentred）。前端据它把新对话派到正确通道（R18）。
	Kind string `json:"kind"`
	// HasOverride 报告该 Agent 是否有本端顺序覆盖（R14 / R16）：组织架构页据此标注
	// 「正在用本端顺序」并启用「恢复为账号默认顺序」。同一请求里每档同值。
	HasOverride bool `json:"hasOverride"`
}

// ExecTargetNoneAvailableError 在一个 Agent 的执行目标列表非空、但逐档判定全部不可用时
// 由 PickExecTarget 返回。Reasons 按列表顺序给出每一档的原因，供调用方结构化消费；
// text 是同一份信息渲染成的文本，是 Wails 边界唯一透给前端的通道（只过 Error() 字符串）。
//
// 整段文本在构造时就渲染好：Error() 没有 ctx，而表头 / 行格式 / 机器名 / 逐档
// 原因全是用户可见文案，必须按调用方 ctx 的语言查 internal/pkg/code 的语言包。
type ExecTargetNoneAvailableError struct {
	httpErr *httputils.Error
	text    string
	Reasons []ExecTargetUnavailable
}

func (e *ExecTargetNoneAvailableError) Error() string { return e.text }

// newExecTargetNoneAvailableError 把表头与逐档原因渲染成 Wails 边界要的那一段文本。
func newExecTargetNoneAvailableError(ctx context.Context, reasons []ExecTargetUnavailable) *ExecTargetNoneAvailableError {
	lines := make([]string, 0, len(reasons)+1)
	lines = append(lines, i18n.T(ctx, code.ChatAgentNoAvailableExecTarget))
	for i, r := range reasons {
		label := i18n.T(ctx, code.ChatExecTargetDeviceLocal)
		if r.DeviceID != "" {
			label = i18n.T(ctx, code.ChatExecTargetDeviceRemote, r.DeviceID)
		}
		lines = append(lines, i18n.T(ctx, code.ChatExecTargetLineFormat, i+1, r.AgentBackendID, label, r.Hint))
	}
	return &ExecTargetNoneAvailableError{
		httpErr: &httputils.Error{
			Status: http.StatusBadRequest,
			Code:   code.ChatAgentNoAvailableExecTarget,
			Msg:    lines[0],
		},
		text:    strings.Join(lines, "\n"),
		Reasons: reasons,
	}
}

func (e *ExecTargetNoneAvailableError) As(target any) bool {
	if p, ok := target.(**httputils.Error); ok {
		*p = e.httpErr
		return true
	}
	return false
}

// PickExecTarget 见 ChatSvc 接口注释。按 R14 解析后的顺序取第一个可用的档。
func (s *chatSvc) PickExecTarget(ctx context.Context, agentID int64, projectID int64) (*ExecTargetChoice, error) {
	targets, err := s.resolvedExecTargets(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, i18n.NewError(ctx, code.ChatAgentNoBackend)
	}

	unavailable := make([]ExecTargetUnavailable, 0, len(targets))
	for _, target := range targets {
		be, err := agent_backend_repo.AgentBackend().Find(ctx, target.AgentBackendID)
		if err != nil {
			return nil, operationFailedWithCause(ctx, err, zap.Int64("agentBackendId", target.AgentBackendID))
		}
		reason, hint, err := s.evalExecTargetAvailability(ctx, be, projectID)
		if err != nil {
			return nil, err
		}
		if reason == "" {
			return &ExecTargetChoice{Target: target, Backend: be}, nil
		}
		deviceID := ""
		if be != nil {
			deviceID = be.DeviceFingerprint
		}
		unavailable = append(unavailable, ExecTargetUnavailable{
			AgentBackendID: target.AgentBackendID,
			DeviceID:       deviceID,
			Reason:         reason,
			Hint:           hint,
		})
	}
	return nil, newExecTargetNoneAvailableError(ctx, unavailable)
}

// ListExecTargetAvailability 逐档判定一个 Agent 的执行目标列表可用性，供组织架构页
// 展示（R15，任务 12）。与 PickExecTarget 共用同一套判定原语（AgentBackend.Find +
// evalExecTargetAvailability），但刻意**不在遇到第一个可用档时提前返回**——界面要
// 同时看到列表里每一档的状态（含徽标「当前生效/在线/离线/未配对/…」），不只是最终
// 会派发到哪一档。projectID<=0（自由会话）不做项目路径判定，与 PickExecTarget 一致。
// 列表按 R14 解析后的顺序给出（本端覆盖 / 无覆盖时桌面端自己提前），每档标注
// HasOverride。空列表返回空切片、无错误——「保存被拒」是写路径（agent_svc.Update）
// 的职责，读路径只如实报告。
func (s *chatSvc) ListExecTargetAvailability(ctx context.Context, agentID int64, projectID int64) ([]ExecTargetAvailabilityView, error) {
	targets, err := s.resolvedExecTargets(ctx, agentID)
	if err != nil {
		return nil, err
	}
	hasOverride := s.hasExecTargetOverride(ctx, agentID)
	out := make([]ExecTargetAvailabilityView, 0, len(targets))
	for _, target := range targets {
		be, err := agent_backend_repo.AgentBackend().Find(ctx, target.AgentBackendID)
		if err != nil {
			return nil, operationFailedWithCause(ctx, err, zap.Int64("agentBackendId", target.AgentBackendID))
		}
		reason, hint, err := s.evalExecTargetAvailability(ctx, be, projectID)
		if err != nil {
			return nil, err
		}
		// 路径与可用性分开取：evalExecTargetAvailability 一旦在前面某一步（未配对 /
		// 离线 / 供应商类）判出不可用就提前返回，根本走不到路径那一步——而改选浮层
		// 恰恰要把不可用那几档的路径也显示出来（用户据此判断「等它上线值不值」）。
		projectPath := ""
		if projectID > 0 && be != nil {
			p, _, perr := s.execTargetProjectPath(ctx, be, projectID)
			if perr != nil {
				return nil, perr
			}
			projectPath = p
		}
		out = append(out, ExecTargetAvailabilityView{
			AgentBackendID: target.AgentBackendID,
			Available:      reason == "",
			Reason:         reason,
			Hint:           hint,
			ProjectPath:    projectPath,
			HasOverride:    hasOverride,
			Kind:           s.execTargetKind(ctx, be),
		})
	}
	return out, nil
}

// evalExecTargetAvailability 判一档执行目标是否可用（R15）：
//  1. be 为 nil（目标行引用的 backend 已不存在，仅可能出现在 task 7 之前的软删场景）
//     视同没绑后端。
//  2. 远端档：本机没配对该指纹指向的这台 agentred（判据是本地配对表里有没有这一行，
//     不是有没有配对令牌，R2b），或已配对但不在线 —— 不可用。
//  3. backend 自身是否可用：复用既有 BlockReason 判据（blockReasonForBackend），
//     本地远端一视同仁 —— 远端的供应商类原因（RemoteProviderMissing / OpenClaw）已经
//     在那套判据里。
//  4. projectID > 0 时，这一档所在机器上要配了这个项目的路径（决策 34）：本机档看
//     projects.local_path_missing，agentred 档看 project_locations 里 device_id 缓存
//     列命中的那一行。projectID <= 0（自由会话）不受这条约束。
func (s *chatSvc) evalExecTargetAvailability(
	ctx context.Context, be *agent_backend_entity.AgentBackend, projectID int64,
) (BlockReason, string, error) {
	if be == nil {
		return BlockReasonNoBackend, i18n.T(ctx, code.ChatExecTargetHintBackendGone), nil
	}
	// 指向本机的档（DeviceID == 本机指纹）不是「远端配对设备」：它是这台桌面端自己，
	// 按本地判据判可用（R14 把自己排第一的前提是它能被本地派发）。
	if beTargetsRemote(be) {
		reason, hint, err := s.evalRemoteDeviceAvailability(ctx, be)
		if err != nil {
			return "", "", err
		}
		if reason != "" {
			return reason, hint, nil
		}
	}

	prov, err := lookupProviderForBackend(ctx, be)
	if err != nil {
		return "", "", operationFailedWithCause(ctx, err, zap.Int64("agentBackendId", be.ID))
	}
	gatewayRunning := s.gateway != nil && s.gateway.Status().State == "running"
	if chattable, reason, hint := blockReasonForBackend(ctx, be, prov, gatewayRunning); !chattable {
		return reason, hint, nil
	}

	if projectID > 0 {
		reason, hint, err := s.evalExecTargetProjectPath(ctx, be, projectID)
		if err != nil {
			return "", "", err
		}
		if reason != "" {
			return reason, hint, nil
		}
	}
	return "", "", nil
}

// execTargetKind 报告一档执行目标的目标种类（"local" / "desktop" / "daemon"）。
// 本机档（空 DeviceID 或 == 本机指纹）是 local；具名指纹且能在账号设备清单里命中
// kind=desktop 的是 desktop；其余远端档（agentred）是 daemon。无法判定时（账号清单
// 拿不到）如实回 daemon——desktop 归类只在派发通道选择用，误归 daemon 也只是回到
// 既有 agentred 通道的报错，不会派错机器。
func (s *chatSvc) execTargetKind(ctx context.Context, be *agent_backend_entity.AgentBackend) string {
	if !beTargetsRemote(be) {
		return "local"
	}
	if strings.HasPrefix(be.DeviceFingerprint, "sha256:") {
		if info, ok := s.accountDeviceFor(ctx, be.DeviceFingerprint); ok && info.kind == "desktop" {
			return "desktop"
		}
	}
	return "daemon"
}

// accountDeviceFor 在账号设备清单里按指纹找一台设备。server 未接线 / 未登录 / 拉取
// 失败时返回 not-ok（调用方按「无法判定」处理，不误判可用）。
func (s *chatSvc) accountDeviceFor(ctx context.Context, fingerprint string) (namedDeviceInfo, bool) {
	if fingerprint == "" || server_svc.Server() == nil {
		return namedDeviceInfo{}, false
	}
	devices, err := server_svc.Server().ListDevices(ctx)
	if err != nil {
		return namedDeviceInfo{}, false
	}
	for _, d := range devices {
		if d.Fingerprint == fingerprint {
			return namedDeviceInfo{kind: d.Kind, online: d.Online}, true
		}
	}
	return namedDeviceInfo{}, true
}

// namedDeviceInfo 是账号设备清单里一台设备的窄投影。
type namedDeviceInfo struct {
	kind   string // "desktop" | "daemon" | ...
	online bool
}

// localPairedDeviceView 按指纹在本机配对表里找一台 LAN 配对的 agentred。查不到返回 nil
// （未配对是这一档的正常状态之一，R2b，不当异常）。与 agent_backend_svc 的
// pairedDeviceView 同一取法，chat_svc 侧独立声明以保持 consumer-side 窄依赖。
func localPairedDeviceView(ctx context.Context, fingerprint string) *remote_device_svc.DeviceView {
	if fingerprint == "" || remote_device_svc.Default() == nil {
		return nil
	}
	rows, err := remote_device_svc.Default().List(ctx)
	if err != nil {
		return nil
	}
	for _, row := range rows {
		if row != nil && row.DaemonFingerprint == fingerprint {
			return row
		}
	}
	return nil
}

// localPairedDeviceID 在本地派发边界把 backend 持久化的 DeviceID（规范指纹
// sha256:…）解析成本机 paired_agentreds 的行 ID。只有解析出的行 ID 才能交给 daemon
// 池 / 游标 / 路径缓存（那些子系统全部按数值行 ID 建键）。
//
// 返回 (0,false) 的三种情形：DeviceID 为空（本机档）、不是规范指纹（这个值根本不是
// 一台机器的标识），或指纹在本机配对表里查不到（这台 daemon 没在本机配对，不可
// 达）。调用方把它报告为「不可派发」而不是猜一个行号去拨号。与
// agent_backend_svc.localPairedDeviceID 同一取法，chat_svc 侧独立声明以保持
// consumer-side 窄依赖。
func localPairedDeviceID(ctx context.Context, deviceID string) (int64, bool) {
	if !strings.HasPrefix(deviceID, "sha256:") {
		return 0, false
	}
	view := localPairedDeviceView(ctx, deviceID)
	if view == nil {
		return 0, false
	}
	return view.ID, true
}

// evalRemoteDeviceAvailability 见 evalExecTargetAvailability 步骤 2。与 ListAgents 里
// deviceViews 的取法一致：Get 出错（含未配对时的 not-found）一律当未配对处理，不当成
// 真失败中断挑选 —— 未配对本就是这一档的正常状态之一（R2b），不是异常。
func (s *chatSvc) evalRemoteDeviceAvailability(ctx context.Context, be *agent_backend_entity.AgentBackend) (BlockReason, string, error) {
	// 具名指纹目标（另一台桌面端 / 账号 agentred）：在线态只在中继登记里有真相（R2），
	// 先按账号设备清单判；不在清单里再退回本机配对表。DeviceID 只有指纹一种形态，
	// 别的值指不到任何一台机器 —— 与「指纹查不到」同样报未配对（R2b）。
	if !strings.HasPrefix(be.DeviceFingerprint, "sha256:") {
		return BlockReasonExecTargetUnpaired, i18n.T(ctx, code.ChatExecTargetHintUnpaired), nil
	}
	return s.evalNamedRemoteDeviceAvailability(ctx, be)
}

// evalNamedRemoteDeviceAvailability 判一档具名指纹目标的可用性（R15 + R2）。
//
// 判据分三段：
//  1. 账号设备清单（server 中继登记）里命中该指纹：kind=desktop 且 Online → 可用；
//     kind=desktop 且不在线 →「Agentre 没有运行」（R2，与机器离线区分）；agentred
//     在线/离线按既有说法。
//  2. 不在账号清单：可能是本地 LAN 配对的 agentred（指纹），查本机配对表。
//  3. 都查不到：未配对（R2b）。
func (s *chatSvc) evalNamedRemoteDeviceAvailability(ctx context.Context, be *agent_backend_entity.AgentBackend) (BlockReason, string, error) {
	if info, ok := s.accountDeviceFor(ctx, be.DeviceFingerprint); ok && info.kind == "desktop" {
		if !info.online {
			return BlockReasonExecTargetDesktopNotRunning, i18n.T(ctx, code.ChatExecTargetHintDesktopNotRunning), nil
		}
		return "", "", nil
	}
	// 本机配对表（LAN agentred，指纹已认领）：命中即按离线判据。
	view := localPairedDeviceView(ctx, be.DeviceFingerprint)
	if view != nil {
		if !view.Online {
			return BlockReasonExecTargetOffline, i18n.T(ctx, code.ChatExecTargetHintOffline), nil
		}
		return "", "", nil
	}
	return BlockReasonExecTargetUnpaired, i18n.T(ctx, code.ChatExecTargetHintUnpaired), nil
}

// evalExecTargetProjectPath 见 evalExecTargetAvailability 步骤 4。判据是
// execTargetProjectPath 的 configured —— 「配没配」与「配的是哪个目录」是同一次
// 查询的两个投影，不写两份取法。
func (s *chatSvc) evalExecTargetProjectPath(ctx context.Context, be *agent_backend_entity.AgentBackend, projectID int64) (BlockReason, string, error) {
	_, configured, err := s.execTargetProjectPath(ctx, be, projectID)
	if err != nil {
		return "", "", err
	}
	if configured {
		return "", "", nil
	}
	if !beTargetsRemote(be) {
		return BlockReasonExecTargetProjectPathMissing, i18n.T(ctx, code.ChatExecTargetHintLocalPathMissing), nil
	}
	return BlockReasonExecTargetProjectPathMissing, i18n.T(ctx, code.ChatExecTargetHintRemotePathMissing), nil
}

// execTargetProjectPath 取「这一档所在的机器上，这个项目的路径」：本机档看
// projects.path（判据是 LocalPathMissing 状态位，不是 Path 是否为空，见
// project_entity 注释），agentred 档看 project_locations 里该 device_id 那一行。
// configured 报告「那台机器上配没配」，与 path 是否为空刻意分开——判据在状态位/
// 行的存在性上，路径值只是展示用。
func (s *chatSvc) execTargetProjectPath(
	ctx context.Context, be *agent_backend_entity.AgentBackend, projectID int64,
) (string, bool, error) {
	if !beTargetsRemote(be) {
		p, err := project_repo.Project().Find(ctx, projectID)
		if err != nil {
			return "", false, operationFailedWithCause(ctx, err, zap.Int64("projectId", projectID))
		}
		if p == nil || p.LocalPathMissing {
			return "", false, nil
		}
		return p.Path, true, nil
	}
	// 远端档按（project, device_fingerprint）自然键查行 —— device_id 是同一行的缓存
	// 列（决策 26），backend 的 DeviceID 只有规范指纹一种形态。
	loc, err := project_location_repo.ProjectLocation().FindByProjectAndFingerprint(ctx, projectID, be.DeviceFingerprint)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		return "", false, operationFailedWithCause(ctx, err, zap.Int64("projectId", projectID))
	}
	if loc == nil {
		return "", false, nil
	}
	return loc.Path, true, nil
}

// blockReasonForBackend 是「一个 backend 自身是否可对话」的单一判定点，ListAgents（Agent
// 列表的 chattable/blockReason 展示）与 PickExecTarget（R15 逐档挑选）共用 —— 避免两处
// 各写一份、慢慢漂移。prov 是 be.LLMProviderKey 对应的供应商（找不到传 nil）；
// gatewayRunning 只影响本地 CLI 类后端。
//
// 空 LLMProviderKey（CLI 走自身 login 态）在 ClaudeCode/Codex/PiAgent 分支第一条就短路
// 判可用，不做任何可达性预探测——这是既有行为（迁移前的 chat.go:391），必须原样保留。
// hint 是用户可见正文（ListAgents 的 ChattableHint / PickExecTarget 的逐档说明），
// 因此走 internal/pkg/code 的语言包而不是就地写死 —— ctx 只为这个而来。
func blockReasonForBackend(
	ctx context.Context,
	be *agent_backend_entity.AgentBackend, prov *llm_provider_entity.LLMProvider, gatewayRunning bool,
) (chattable bool, reason BlockReason, hint string) {
	switch agent_backend_entity.BackendType(be.Type) {
	case agent_backend_entity.TypeBuiltin:
		switch {
		case prov != nil && prov.IsActive():
			return true, "", ""
		case prov == nil:
			// 内置后端没绑 / 找不到绑定的供应商。
			return false, BlockReasonBackendRequiresProvider, i18n.T(ctx, code.ChatBackendHintActivateProvider)
		default:
			// 后端绑的供应商存在但未激活/缺 Key。
			return false, BlockReasonProviderInactive, i18n.T(ctx, code.ChatBackendHintActivateProvider)
		}
	case agent_backend_entity.TypeClaudeCode, agent_backend_entity.TypeCodex, agent_backend_entity.TypePiAgent:
		if be.LLMProviderKey == "" {
			// 走 CLI 自身 login；这里不做可达性探测，启动失败由 chat turn 兜底报错。
			return true, "", ""
		}
		if prov == nil {
			return false, BlockReasonBackendRequiresProvider, i18n.T(ctx, code.ChatBackendHintActivateProvider)
		}
		if !prov.IsActive() {
			return false, BlockReasonProviderInactive, i18n.T(ctx, code.ChatBackendHintActivateProvider)
		}
		if kind := be.Kind(); kind == nil || !kind.ProviderTypeMatch(llm_provider_entity.ProviderType(prov.Type)) {
			// 与 resolveAgentBackend 保持一致：激活但类型不匹配的 provider
			// 仍不能启动该 CLI backend，不能继续误报为 gateway 缺失。
			return false, BlockReasonBackendRequiresProvider, i18n.T(ctx, code.ChatBackendHintProviderTypeMismatch)
		}
		if remoteProviderKnownMissing(ctx, be) {
			return false, BlockReasonRemoteProviderMissing, i18n.T(ctx, code.ChatBackendHintRemoteProviderMissing)
		}
		if beTargetsRemote(be) {
			return true, "", ""
		}
		if !gatewayRunning {
			return false, BlockReasonGatewayNotRunning, i18n.T(ctx, code.ChatBackendHintGatewayNotRunning)
		}
		return true, "", ""
	case agent_backend_entity.TypeOpenClaw:
		if beTargetsRemote(be) {
			return false, BlockReasonRemoteOpenClawUnavailable, i18n.T(ctx, code.ChatBackendHintRemoteOpenClaw)
		}
		return true, "", ""
	default:
		return false, BlockReasonUnknownBackend, i18n.T(ctx, code.ChatBackendHintUnknownType)
	}
}

// lookupProviderForBackend 取 backend 绑的 provider；空 LLMProviderKey（CLI 登录态）
// 一律不查——不做任何预探测，直接把 nil 交给 blockReasonForBackend 的 CLI 分支，那里
// 的第一条判断本就是 LLMProviderKey == "" → 可用（这是既有行为，chat.go:391 一样）。
func lookupProviderForBackend(ctx context.Context, be *agent_backend_entity.AgentBackend) (*llm_provider_entity.LLMProvider, error) {
	if be == nil || be.LLMProviderKey == "" {
		return nil, nil
	}
	return llm_provider_repo.LLMProvider().FindByKey(ctx, be.LLMProviderKey)
}

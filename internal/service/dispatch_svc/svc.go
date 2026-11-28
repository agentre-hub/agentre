// Package dispatch_svc 是桌面端的派发服务（spec 2026-11-25「派发规则」）：从桌面端自己
// 的来源——本机指纹、账号设备清单与本机配对表、设备偏好与本机优先开关、设备情况、成员
// 生效的引擎配置与「在哪跑」约束、供应商「谁能连到」、项目路径与项目默认设备——拼出
// dispatch.Input，调共享的 dispatch.Evaluate，回答四个问题：新会话派到哪台（连同每台设备
// 的结果）、成员不带项目时现在能跑在哪、手动指定的设备合不合法、一条会话（或尚未保存的
// agent/项目目标）的命令在哪台设备的哪个目录执行。
//
// 原因码在这一层保持为码（dispatch.ReasonCode），由界面渲染；只有必须报错的地方才映射
// 成 code.* 错误。
package dispatch_svc

import (
	"context"

	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/svcerr"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// DispatchSvc 派发服务。
type DispatchSvc interface {
	// Plan 为新会话给出派发方案：选中的设备、用的引擎配置，以及按派发顺序的每台设备的
	// 结果。全部设备都不通过时 Found=false、不报错（界面显示「无处可跑」）。
	Plan(ctx context.Context, req *PlanRequest) (*Plan, error)
	// Availability 成员不带项目时每台设备的判定（成员详情「现在能跑在」）。
	Availability(ctx context.Context, agentID int64) (*Plan, error)
	// ValidateManual 校验手动指定的设备此刻能不能跑这名成员（带项目时含项目路径）。
	ValidateManual(ctx context.Context, req *PlanRequest) error
	// ResolveLocalCommandScope 已有会话或尚未保存的 agent/项目目标的命令执行设备与 cwd。
	ResolveLocalCommandScope(ctx context.Context, req *ScopeRequest) (*CommandScope, error)
	// ResolveSessionWorkspace 把会话解析成 {deviceID, cwd}：deviceID 是本机配对表的行 ID，
	// 0 即本机。实现 workspace_fs_svc 的 SessionWorkspaceResolver 窄接口。
	ResolveSessionWorkspace(ctx context.Context, sessionID int64) (deviceID int64, cwd string, err error)
}

// PlanRequest 一次派发的输入。
type PlanRequest struct {
	AgentID int64 `json:"agentId"`
	// ProjectID 0 = 无项目，不检查项目路径。
	ProjectID int64 `json:"projectId"`
	// EngineProfileID 本会话换用的引擎配置；0 = 成员生效的那份。
	EngineProfileID int64 `json:"engineProfileId"`
	// ManualDevice 本会话手动指定的设备；空 = 自动。
	ManualDevice devicefp.Carrier `json:"manualDevice"`
}

// Plan 派发方案。
type Plan struct {
	Found  bool             `json:"found"`
	Chosen devicefp.Carrier `json:"chosen"`
	// ChosenBy 选中设备因何排在前面：manual / project_default / account_order。
	ChosenBy string `json:"chosenBy"`
	// ManualUnavailable 请求了手动设备但它此刻不能跑，结果已回落到自动派发。
	ManualUnavailable bool       `json:"manualUnavailable"`
	Engine            EngineView `json:"engine"`
	// Devices 参与派发的每台设备，按派发顺序。
	Devices []*DeviceResult `json:"devices"`
}

// EngineView 判定所用的引擎配置。
type EngineView struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Runtime string `json:"runtime"`
	// ModelSource native（CLI 原生登录）/ provider（经供应商）。
	ModelSource  string `json:"modelSource"`
	ProviderKey  string `json:"providerKey"`
	ProviderName string `json:"providerName"`
	ModelKey     string `json:"modelKey"`
}

// DeviceResult 一台设备的判定结果。
type DeviceResult struct {
	Fingerprint devicefp.Carrier `json:"fingerprint"`
	// Name 设备名；本机没有账号名时为空，界面写「本机」。
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	IsSelf bool   `json:"isSelf"`
	// Eligible 全部检查通过；Chosen 是派发选中的那台。
	Eligible bool `json:"eligible"`
	Chosen   bool `json:"chosen"`
	// Reason 未通过时第一条不满足的检查；通过时为 nil。
	Reason *ReasonView `json:"reason"`
	// ProjectPath 绑定项目时这台设备上的项目路径（没配为空）。
	ProjectPath string `json:"projectPath"`

	// pairedID 本机配对表的行 ID（agentred 已配对时），供作用域解析与技能发现借连接池。
	pairedID int64
	// skillPacks 设备上报的、该引擎配置运行时的已装技能包；skillPacksKnown=false 即没报。
	skillPacks      []string
	skillPacksKnown bool
}

// PairedID 本机配对表的行 ID；不是已配对的 agentred 时为 0。
func (d *DeviceResult) PairedID() int64 { return d.pairedID }

// SkillPacks 设备上报的该运行时已装技能包，known=false 表示这台设备没有上报。
func (d *DeviceResult) SkillPacks() (packs []string, known bool) {
	return d.skillPacks, d.skillPacksKnown
}

// ReasonView 是 dispatch.Reason 的界面投影：码加参数，LastSeen 为 Unix 毫秒。
type ReasonView struct {
	Code     string   `json:"code"`
	LastSeen int64    `json:"lastSeen"`
	Runtime  string   `json:"runtime"`
	Labels   []string `json:"labels"`
	Project  string   `json:"project"`
	Provider string   `json:"provider"`
	Engine   string   `json:"engine"`
}

// ScopeRequest 接受且只接受一种目标：已有 SessionID，或尚未保存的 AgentID + ProjectID
// （ProjectID=0 即自由会话）。
type ScopeRequest struct {
	SessionID int64 `json:"sessionId"`
	AgentID   int64 `json:"agentId"`
	ProjectID int64 `json:"projectId"`
}

// CommandScope 命令执行的设备与 cwd：DeviceID 空即本机，Cwd 空即那台设备上的默认目录。
type CommandScope struct {
	DeviceID devicefp.Carrier `json:"deviceId"`
	Cwd      string           `json:"cwd"`
}

// Deps 是派发服务的全部来源。
type Deps struct {
	Devices   Devices
	Facts     Facts
	Prefs     Prefs
	Engines   Engines
	Providers Providers
	Projects  Projects
	Sessions  Sessions
}

type dispatchSvc struct{ Deps }

// New 构造派发服务。
func New(deps Deps) DispatchSvc { return &dispatchSvc{Deps: deps} }

var defaultSvc DispatchSvc

// Dispatch 取 bootstrap 注册的单例；未装配时为 nil。
func Dispatch() DispatchSvc { return defaultSvc }

// RegisterDispatch 由 bootstrap 注册单例。
func RegisterDispatch(impl DispatchSvc) { defaultSvc = impl }

var dispatchErrors = svcerr.Reporter{LogMessage: "dispatch_svc: operation failed", CallerSkip: 1}

func operationFailed(ctx context.Context, cause error, fields ...zap.Field) error {
	return dispatchErrors.OperationFailedWithCause(ctx, cause, fields...)
}

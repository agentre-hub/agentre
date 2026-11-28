// Package dispatch 是「会话该跑在哪台设备」的共享判定:一个纯函数 Evaluate,桌面端
// 派发服务与 agentre-server(控制台、agrctl 账号模式)调用同一份。
//
// 它刻意只依赖标准库(所在 module 零外部依赖),也不带任何界面文案:对每台设备返回
// 稳定的原因码 ReasonCode 与参数(上次在线时间、运行时名、标签、项目名、供应商名、
// 引擎配置名),各宿主自行渲染本地化文本。
//
// 用法:填 Input(设备情况、账号设备顺序、本机优先开关与本机指纹、成员约束、生效的
// 引擎配置、供应商可达范围、项目 id/名称与默认设备、手动指定设备),调 Evaluate 得
// Result:
//
//   - Result.Devices:参与派发的每台设备(只含 desktop 与 agentred,cli/web/mobile
//     永不出现),按优先级排好序,每项带 Eligible 与未通过时的 Reason;
//   - Result.Chosen / Found / ChosenBy:排序后第一台全部通过的设备及它因何排在前面
//     (ByManual / ByProjectDefault / ByAccountOrder);全部不通过时 Found=false。
//
// 排序:手动指定设备 › 项目默认设备 › 账号设备顺序(LocalFirst 且 SelfFingerprint 在
// 参与设备里时,本机提到账号顺序最前;不在顺序里的设备按输入顺序排在末尾)。手动或项目
// 默认设备未通过检查时自动退回下一级,不阻塞。
//
// 检查按下列顺序,第一条不满足的作为原因:在线 › 桌面端 Agentre 在运行 › 本机配对了
// 这台 agentred(RequiresPairing) › 成员约束 › 设备类型支持该运行时(builtin/openclaw
// 仅桌面端) › 装了运行时且版本够(acp 看该引擎配置的命令能否解析;builtin、hermes、
// openclaw 没有本机 CLI,跳过) › 模型来源可用(原生登录:LoginUnknown 视为可用;供应商:
// 设备在其「谁能连到」内) › hermes/openclaw 存有该引擎配置的凭据 › 绑定项目时有项目
// 路径。技能包是否装齐不参与判定。
package dispatch

import "time"

// DeviceKind 是设备类型。只有 KindDesktop 与 KindAgentred 参与派发,其它值(cli、web、
// mobile ...)在 Evaluate 中被忽略。
type DeviceKind string

const (
	KindDesktop  DeviceKind = "desktop"
	KindAgentred DeviceKind = "agentred"
)

// NativeLogin 是某运行时在某设备上的原生登录态。
type NativeLogin string

const (
	LoginUnknown   NativeLogin = "unknown" // 零值也按未知处理,视为可用
	LoginLoggedIn  NativeLogin = "loggedIn"
	LoginLoggedOut NativeLogin = "loggedOut"
)

// RuntimeFact 是设备上报的某运行时情况。
type RuntimeFact struct {
	Found        bool
	Version      string
	BelowMinimum bool
	NativeLogin  NativeLogin
}

// Device 是一台设备的事实。
type Device struct {
	Fingerprint string
	Kind        DeviceKind
	Online      bool
	LastSeen    time.Time
	// AppRunning 仅对桌面端有意义:那台电脑上的 Agentre 是否在运行。
	AppRunning bool
	// RequiresPairing 为真时(从桌面端派发到 agentred),Paired 必须为真。
	RequiresPairing bool
	Paired          bool
	Labels          []string
	// Runtimes 以运行时类型名为键。
	Runtimes map[string]RuntimeFact
	// ACPResolvable 以引擎配置 sync id 为键:该引擎配置的 ACP 命令在此设备上能否解析。
	ACPResolvable map[string]bool
	// Credentials 以引擎配置 sync id 为键:此设备是否存有 Hermes/OpenClaw 端点凭据。
	Credentials map[string]bool
	// ProjectPaths 以项目 id 为键:此设备上是否有该项目的路径。
	ProjectPaths map[string]bool
}

// ModelSource 是引擎配置的模型来源。
type ModelSource string

const (
	ModelNative   ModelSource = "native"
	ModelProvider ModelSource = "provider"
)

// EngineProfile 是生效的引擎配置里派发关心的部分。
type EngineProfile struct {
	SyncID       string
	Name         string
	Runtime      string
	ModelSource  ModelSource
	ProviderKey  string // ModelSource=provider 时有效
	ProviderName string // 用于原因参数
}

// ProviderReach 是供应商「谁能连到」:Any 为真表示任何设备,否则只有 Devices 里的。
type ProviderReach struct {
	Any     bool
	Devices []string
}

// ConstraintKind 是成员「在哪跑」约束的种类。
type ConstraintKind string

const (
	ConstraintAny     ConstraintKind = "" // 零值:任意设备
	ConstraintLabels  ConstraintKind = "labels"
	ConstraintDevices ConstraintKind = "devices"
)

// Constraint 是成员约束。Labels 要求设备带齐全部标签。
type Constraint struct {
	Kind    ConstraintKind
	Labels  []string
	Devices []string
}

// Input 是 Evaluate 的全部输入。
type Input struct {
	Devices []Device
	Engine  EngineProfile
	// Providers 以供应商 key 为键;缺省的供应商视为任何设备可达。
	Providers  map[string]ProviderReach
	Constraint Constraint
	// AccountOrder 是账号设备顺序(指纹)。
	AccountOrder    []string
	LocalFirst      bool
	SelfFingerprint string
	// ProjectID 为空表示无项目,跳过路径检查。
	ProjectID            string
	ProjectName          string
	ProjectDefaultDevice string
	ManualDevice         string
}

// ReasonCode 是稳定的原因码,宿主据此渲染本地化文本。
type ReasonCode string

const (
	ReasonNone                ReasonCode = ""
	ReasonOffline             ReasonCode = "offline"              // LastSeen
	ReasonAppNotRunning       ReasonCode = "app_not_running"      //
	ReasonNotPaired           ReasonCode = "not_paired"           //
	ReasonLabelMissing        ReasonCode = "label_missing"        // Labels(缺的标签)
	ReasonNotInDevices        ReasonCode = "not_in_devices"       //
	ReasonRuntimeDesktopOnly  ReasonCode = "runtime_desktop_only" // Runtime
	ReasonRuntimeMissing      ReasonCode = "runtime_missing"      // Runtime
	ReasonRuntimeTooOld       ReasonCode = "runtime_too_old"      // Runtime
	ReasonNotLoggedIn         ReasonCode = "not_logged_in"        // Runtime
	ReasonProviderUnreachable ReasonCode = "provider_unreachable" // Provider
	ReasonNoCredential        ReasonCode = "no_credential"        // Engine
	ReasonNoProjectPath       ReasonCode = "no_project_path"      // Project
)

// Reason 是原因码加参数;只填与该码相关的字段。
type Reason struct {
	Code     ReasonCode
	LastSeen time.Time
	Runtime  string
	Labels   []string
	Project  string
	Provider string
	Engine   string
}

// DeviceResult 是一台设备的判定结果。
type DeviceResult struct {
	Fingerprint string
	Kind        DeviceKind
	Eligible    bool
	Reason      Reason // Eligible 时为零值
}

// Basis 说明选中设备因何排在前面。
type Basis string

const (
	ByManual         Basis = "manual"
	ByProjectDefault Basis = "project_default"
	ByAccountOrder   Basis = "account_order"
)

// Result 是 Evaluate 的输出。
type Result struct {
	Found    bool
	Chosen   string // Found 时为选中设备指纹
	ChosenBy Basis  // Found 时有效
	// Devices 按优先级排序,含每台参与设备的结果。
	Devices []DeviceResult
}

// 运行时类型名(与引擎配置 runtime 类型一致)。
const (
	runtimeACP      = "acp"
	runtimeBuiltin  = "builtin"
	runtimeHermes   = "hermes"
	runtimeOpenClaw = "openclaw"
)

// Evaluate 对每台参与派发的设备做检查、排序并选出第一台全部通过的设备。
func Evaluate(in Input) Result {
	var res Result
	ordered := order(in)
	for _, o := range ordered {
		r := check(in, o.device)
		res.Devices = append(res.Devices, DeviceResult{
			Fingerprint: o.device.Fingerprint, Kind: o.device.Kind,
			Eligible: r.Code == ReasonNone, Reason: r,
		})
		if !res.Found && r.Code == ReasonNone {
			res.Found, res.Chosen, res.ChosenBy = true, o.device.Fingerprint, o.basis
		}
	}
	return res
}

type ranked struct {
	device Device
	basis  Basis
}

// order 取出参与派发的设备并按优先级排序。
func order(in Input) []ranked {
	byFP := map[string]Device{}
	var inputOrder []string
	for _, d := range in.Devices {
		if d.Kind != KindDesktop && d.Kind != KindAgentred {
			continue
		}
		if _, dup := byFP[d.Fingerprint]; dup {
			continue
		}
		byFP[d.Fingerprint] = d
		inputOrder = append(inputOrder, d.Fingerprint)
	}

	// 账号顺序:顺序里的先,其余按输入顺序;本机优先时本机提到最前。
	var account []string
	seen := map[string]bool{}
	add := func(fp string) {
		if _, ok := byFP[fp]; ok && !seen[fp] {
			seen[fp] = true
			account = append(account, fp)
		}
	}
	if in.LocalFirst {
		add(in.SelfFingerprint)
	}
	for _, fp := range in.AccountOrder {
		add(fp)
	}
	for _, fp := range inputOrder {
		add(fp)
	}

	var out []ranked
	placed := map[string]bool{}
	push := func(fp string, b Basis) {
		if d, ok := byFP[fp]; ok && !placed[fp] {
			placed[fp] = true
			out = append(out, ranked{d, b})
		}
	}
	push(in.ManualDevice, ByManual)
	push(in.ProjectDefaultDevice, ByProjectDefault)
	for _, fp := range account {
		push(fp, ByAccountOrder)
	}
	return out
}

// check 按顺序做检查,返回第一条不满足的原因;全部满足返回零值 Reason。
func check(in Input, d Device) Reason {
	rt := in.Engine.Runtime
	if !d.Online {
		return Reason{Code: ReasonOffline, LastSeen: d.LastSeen}
	}
	if d.Kind == KindDesktop && !d.AppRunning {
		return Reason{Code: ReasonAppNotRunning}
	}
	if d.Kind == KindAgentred && d.RequiresPairing && !d.Paired {
		return Reason{Code: ReasonNotPaired}
	}
	if r := checkConstraint(in.Constraint, d); r.Code != ReasonNone {
		return r
	}
	if d.Kind != KindDesktop && (rt == runtimeBuiltin || rt == runtimeOpenClaw) {
		return Reason{Code: ReasonRuntimeDesktopOnly, Runtime: rt}
	}
	external := rt == runtimeBuiltin || rt == runtimeHermes || rt == runtimeOpenClaw
	if !external {
		if rt == runtimeACP {
			if !d.ACPResolvable[in.Engine.SyncID] {
				return Reason{Code: ReasonRuntimeMissing, Runtime: rt}
			}
		} else {
			f, ok := d.Runtimes[rt]
			if !ok || !f.Found {
				return Reason{Code: ReasonRuntimeMissing, Runtime: rt}
			}
			if f.BelowMinimum {
				return Reason{Code: ReasonRuntimeTooOld, Runtime: rt}
			}
		}
	}
	switch in.Engine.ModelSource {
	case ModelNative:
		if !external && d.Runtimes[rt].NativeLogin == LoginLoggedOut {
			return Reason{Code: ReasonNotLoggedIn, Runtime: rt}
		}
	case ModelProvider:
		if reach, ok := in.Providers[in.Engine.ProviderKey]; ok && !reach.Any && !contains(reach.Devices, d.Fingerprint) {
			return Reason{Code: ReasonProviderUnreachable, Provider: in.Engine.ProviderName}
		}
	}
	if (rt == runtimeHermes || rt == runtimeOpenClaw) && !d.Credentials[in.Engine.SyncID] {
		return Reason{Code: ReasonNoCredential, Engine: in.Engine.Name}
	}
	if in.ProjectID != "" && !d.ProjectPaths[in.ProjectID] {
		return Reason{Code: ReasonNoProjectPath, Project: in.ProjectName}
	}
	return Reason{}
}

func checkConstraint(c Constraint, d Device) Reason {
	switch c.Kind {
	case ConstraintLabels:
		var missing []string
		for _, l := range c.Labels {
			if !contains(d.Labels, l) {
				missing = append(missing, l)
			}
		}
		if len(missing) > 0 {
			return Reason{Code: ReasonLabelMissing, Labels: missing}
		}
	case ConstraintDevices:
		if !contains(c.Devices, d.Fingerprint) {
			return Reason{Code: ReasonNotInDevices}
		}
	}
	return Reason{}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

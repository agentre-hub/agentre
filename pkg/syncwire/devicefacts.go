package syncwire

import "strconv"

// ── 设备情况上报(设备 → server)─────────────────────────────────────────────
//
// 设备情况是**事实**,不是意图:由设备自己探测、自己上报,server 按设备只留最后一次
// 上报与上报时间,不进 sync_objects、不参与冲突合并(规格决策 8)。桌面端与 agentred
// 上报的是同一个结构,控制台、其它桌面端与共享派发函数读的也是它。
//
// **上报里没有任何凭据、token 或环境变量值。** 凭据只以「这台设备上有没有」的布尔
// 出现;devicefacts_test.go 的守卫按字段名与类型钉住这一条:名字像秘密的字段只能是
// 布尔,整棵结构里没有自由键值表。

// PathDeviceFacts 是设备上报自身情况的 server 端点:POST,请求体是 DeviceFactsReport,
// 用设备自己的账号凭据认证(上报的是哪台设备由凭据决定,不由请求体决定)。
const PathDeviceFacts = "/v1/devices/facts"

// ── 设备情况读取(server → 桌面端 / agrctl)────────────────────────────────────
//
// server 按设备只留最后一次上报与上报时间;离线的设备仍交出它最后一份上报与那个时间。
// 读取用账号凭据认证,只返回这个账号自己的设备。

// PathDeviceFactsList 是读账号下全部设备最后一次上报的 server 端点:GET,应答
// DeviceFactsList。与上报端点同一路径、方法不同。
const PathDeviceFactsList = "/v1/devices/facts"

// PathDeviceFactsOf 是读一台设备最后一次上报的 server 端点:GET,应答
// DeviceFactsResponse。deviceID 是 server 的设备 ID。
func PathDeviceFactsOf(deviceID int64) string {
	return "/v1/devices/" + strconv.FormatInt(deviceID, 10) + "/facts"
}

// DeviceFactsResponse 是单台设备的读取应答。Reported=false 表示它还没上报过(Report
// 为 nil)。ReportedAt 是 server 收到最后一份上报的时刻(Unix 毫秒),界面据此写「多久
// 之前的情况」;Report.CollectedAt 是设备探测完成的时刻。
type DeviceFactsResponse struct {
	Reported   bool               `json:"reported"`
	ReportedAt int64              `json:"reported_at,omitempty"`
	Report     *DeviceFactsReport `json:"report"`
}

// DeviceFactsEntry 是列表应答里的一台设备:身份加它最后一次上报。
type DeviceFactsEntry struct {
	DeviceID    int64  `json:"device_id"`
	Fingerprint string `json:"fingerprint"`
	Name        string `json:"name"`
	// Kind 是 desktop 或 agentred。
	Kind string `json:"kind"`
	DeviceFactsResponse
}

// DeviceFactsList 是账号下全部设备(不含 cli)的最后一次上报,没上报过的设备也在
// (Reported=false)。
type DeviceFactsList struct {
	Devices []DeviceFactsEntry `json:"devices"`
}

// Claude Code 与 Codex 的原生登录态。取值与 dispatch.NativeLogin 逐字相同,设备报上来
// 的值原样交给派发函数。
const (
	NativeLoginLoggedIn  = "loggedIn"
	NativeLoginLoggedOut = "loggedOut"
	// NativeLoginUnknown 是「这台设备上判断不了」,派发视为可用。
	NativeLoginUnknown = "unknown"
)

// DeviceFactsReport 是一台设备的完整情况。每次上报都是**整份**:server 用它整体替换
// 这台设备的上一份,不做按项合并 —— 一份丢掉的上报因此不会让 server 停在半新半旧的
// 状态,下一份到达就完整。
type DeviceFactsReport struct {
	// CollectedAt 是这份情况探测完成的时刻(Unix 毫秒)。
	CollectedAt int64 `json:"collected_at"`
	// Runtimes 每种已知运行时(Claude Code、Codex、Pi、Antigravity)一项,未找到的也在。
	Runtimes []RuntimeFact `json:"runtimes"`
	// ACPEngines 账号下每份 ACP 类引擎配置一项。
	ACPEngines []ACPEngineFact `json:"acp_engines"`
	// ServiceEngines 账号下每份 Hermes / OpenClaw 引擎配置一项。
	ServiceEngines []ServiceEngineFact `json:"service_engines"`
	// SkillPacks 每种装着技能包的运行时一项。
	SkillPacks []RuntimeSkillPacks `json:"skill_packs"`
	// LocalPaths 是这台设备上配了本机路径的项目;agentred 没有本机项目路径,恒为空。
	LocalPaths []LocalPathItem `json:"local_paths"`
}

// RuntimeFact 是一种已知运行时在这台设备上的情况。
type RuntimeFact struct {
	// Runtime 是运行时类型(与引擎配置的 type 同一套词:claudecode / codex / piagent /
	// antigravity)。
	Runtime string `json:"runtime"`
	Found   bool   `json:"found"`
	// Version 是可执行文件自报的版本;没找到或读不出时为空。
	Version string `json:"version,omitempty"`
	// Path 是找到的可执行文件路径。
	Path string `json:"path,omitempty"`
	// MinimumVersion 是这种运行时要求的最低版本;没有下限时为空。
	MinimumVersion string `json:"minimum_version,omitempty"`
	// BelowMinimum 版本已读出且低于 MinimumVersion。
	BelowMinimum bool `json:"below_minimum"`
	// NativeLogin 只对 Claude Code 与 Codex 有值:NativeLogin* 之一。
	NativeLogin string `json:"native_login,omitempty"`
}

// ACPEngineFact 是一份 ACP 引擎配置的命令在这台设备上能否解析。
type ACPEngineFact struct {
	EngineSyncID string `json:"engine_sync_id"`
	Resolvable   bool   `json:"resolvable"`
}

// ServiceEngineFact 是一份外部服务型引擎配置(Hermes / OpenClaw)在这台设备上的情况。
type ServiceEngineFact struct {
	EngineSyncID string `json:"engine_sync_id"`
	// Runtime 是 hermes 或 openclaw。
	Runtime string `json:"runtime"`
	// CredentialPresent 这台设备上存着这份配置要用的凭据。只说有没有,从不带值。
	CredentialPresent bool `json:"credential_present"`
	// LastConnection 这台设备最近一次连这份配置的端点的结果:Connection* 之一。设备
	// 只在内存里记它,重启后回到 never。
	LastConnection string `json:"last_connection"`
	// LastConnectionAt 那次连接的时刻(Unix 毫秒);never 时为 0。
	LastConnectionAt int64 `json:"last_connection_at,omitempty"`
}

// 服务型引擎上次连接的结果。
const (
	ConnectionSucceeded = "succeeded"
	ConnectionFailed    = "failed"
	// ConnectionNever 是这台设备(自上次启动以来)没连过这个端点。
	ConnectionNever = "never"
)

// RuntimeSkillPacks 是一种运行时在这台设备上已装的技能包 ID。技能包 ID 按运行时区分
// (Claude 插件与 Codex 对不上),所以按运行时分组。
type RuntimeSkillPacks struct {
	Runtime string   `json:"runtime"`
	PackIDs []string `json:"pack_ids"`
}

package wireinbound

import (
	"slices"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// contract.go 回答一个此前只存在于口头知识里的问题:**谁必须答得出什么。**
//
// 同一个 RpcMethod 枚举有四类调用方和两种执行端。调用方并不总能选被调方是哪一种:
// 浏览器控制台按设备指纹拨号,而设备表里 desktop 与 agentred 混在一起;server 后端
// 同理。于是「这个方法我这一侧没实现」不是一个内部细节 —— 它是用户按下按钮之后
// 什么都不发生。
//
// 这类缺陷此前一个都红不了:两个宿主各手写一份注册面,少注册一个方法既不影响编译,
// 也没有任何测试在看。已经因此上过线的有:控制台对桌面端会话的「停止」是死按钮
// (RUNTIME_ABORT 桌面端没注册,而 SessionDetailHeader 无条件渲染那颗按钮,catch{}
// 把 -32601 吞掉);引擎探测四个方法对 kind=desktop 的机器全线打不通。
//
// 表里每一行是一条**可以去核对的断言**,不是注释:Callers 说谁会发它,Hosts 说因此
// 谁必须答得出,Evidence 给出调用点。两个宿主各有一条守卫测试拿它比对自己的注册面。
//
// 加一个方法、或把一个已有方法接到新的调用方上,都要回来改这张表 —— 那正是本文件
// 存在的意义:让「对面认不认识这个方法」变成一件写下来、且会被检查的事。

// HostKind 是执行端的两种形态。它与设备表里的 device.kind 同名同义。
type HostKind string

const (
	HostAgentred HostKind = "agentred"
	HostDesktop  HostKind = "desktop"
)

// Caller 是调用方的四个类别。分类的依据是「它能打到哪种执行端」,而不是它跑在哪:
// 前两类由连接的建立方式天然限定了对端种类,后两类不能。
type Caller string

const (
	// CallerDesktopToAgentred 经 remote_device_svc 的连接池借已配对 daemon 的连接,
	// 对端**只可能**是 agentred。
	CallerDesktopToAgentred Caller = "桌面端 → agentred"
	// CallerDesktopToDesktop 经 server_svc.DialDesktopRelay,对端只可能是桌面端。
	CallerDesktopToDesktop Caller = "桌面端 → 桌面端"
	// CallerConsole 是浏览器,按 machine:<fingerprint> 或对话解析拨通道。**除非调用点
	// 自己筛了 kind,否则两种执行端都可能在那一头。**
	CallerConsole Caller = "浏览器控制台 → 机器"
	// CallerServerBackend 是 server 自己的 Go 后端(mirror_svc / activity_svc /
	// sessionimport_svc),经 wirecall 直接说 wire 协议。与浏览器同样不天然筛 kind。
	CallerServerBackend Caller = "server 后端 → 机器"
)

// Requirement 是一个方法的实现义务。
type Requirement struct {
	Method  agentrewire.RpcMethod
	Callers []Caller
	// Hosts 是**必须**注册这个方法的执行端。它由 Callers 加上各调用点的 kind 筛选
	// 推出:调用点筛了 kind 就只列那一种,没筛就两种都列。
	Hosts []HostKind
	// Evidence 是推出 Hosts 的那处判据(调用点或筛选点),写成 file:line 便于复核。
	Evidence string
}

// Contract 是这张表本身。
//
// 只收**跨种类可达**的方法与两种执行端各自的专属调用方；纯粹本地的、以及
// 反向调用(agentred → 桌面端的 mcp.proxy,注册在调用方连接上而非执行端注册面)
// 不在其中。
func Contract() []Requirement {
	both := []HostKind{HostAgentred, HostDesktop}
	return []Requirement{
		// ── 握手:三类调用方都发,两种执行端都必须答 ──
		{agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT, []Caller{CallerConsole, CallerDesktopToDesktop, CallerServerBackend}, both,
			"relayClient.ts:284 逐通道握手;桌面端那条经 server_svc/relayclient.go:401 手搓帧,不经 wirecall"},

		// ── 会话族:控制台的会话详情与设备页对两种机器都发 ──
		{agentrewire.RpcMethod_RPC_METHOD_SESSION_LIST, []Caller{CallerConsole, CallerDesktopToDesktop, CallerDesktopToAgentred, CallerServerBackend}, both,
			"useMachineReachability.tsx:275 机器轴含 desktop;SessionDetailView.tsx:882 详情页不筛 kind"},
		{agentrewire.RpcMethod_RPC_METHOD_SESSION_COUNTS, []Caller{CallerConsole}, both,
			"Devices.tsx:472 d.kind === KIND_AGENTRED || d.kind === KIND_DESKTOP"},
		{agentrewire.RpcMethod_RPC_METHOD_SESSION_ATTACH, []Caller{CallerConsole, CallerDesktopToDesktop, CallerDesktopToAgentred, CallerServerBackend}, both,
			"relayClient.ts:424 会话详情通道,目标由对话解析,不筛 kind"},
		{agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL, []Caller{CallerConsole, CallerDesktopToDesktop, CallerDesktopToAgentred, CallerServerBackend}, both,
			"relayClient.ts:506"},
		{agentrewire.RpcMethod_RPC_METHOD_SESSION_PENDING_WAITERS, []Caller{CallerConsole, CallerDesktopToAgentred}, both,
			"useSessionDecisionPorts.ts:95 详情页 clientRef"},
		{agentrewire.RpcMethod_RPC_METHOD_SESSION_DELETE, []Caller{CallerServerBackend}, both,
			"mirror_svc 经 wirecall.SessionDelete;server 侧不筛 kind"},
		{agentrewire.RpcMethod_RPC_METHOD_SET_MODEL_TARGET, []Caller{CallerConsole}, both,
			"SessionDetailView.tsx:1405;sessionMirror.ts:142 写发起端"},
		{agentrewire.RpcMethod_RPC_METHOD_SET_SESSION_REASONING_EFFORT, []Caller{CallerConsole}, both,
			"SessionDetailView.tsx:1466;dispatch.ts:466"},
		{agentrewire.RpcMethod_RPC_METHOD_ACTIVITY_ROLLUP, []Caller{CallerServerBackend}, both,
			"mirror_svc/machineconn.go:111;架构文档明说两种执行端都注册同一个方法"},

		// ── 运行时控制族:控制台的会话详情对两种机器都发 ──
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CAPABILITIES, []Caller{CallerConsole, CallerDesktopToAgentred}, both,
			"DraftSession.tsx:233 派发目标可为 desktop(dispatch.ts:55);SessionDetailView.tsx:729"},
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN, []Caller{CallerConsole, CallerDesktopToDesktop, CallerDesktopToAgentred}, both,
			"dispatch.ts:297,且 :309 显式区分 choice.kind === \"agentred\",说明 desktop 也走这条"},
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STEER, []Caller{CallerConsole, CallerDesktopToDesktop, CallerDesktopToAgentred}, both,
			"useSessionSend.ts:328"},
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CANCEL_STEER, []Caller{CallerConsole, CallerDesktopToAgentred}, both,
			"SessionDetailView.tsx:1310 撤回排队消息"},
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_ABORT, []Caller{CallerConsole, CallerDesktopToAgentred}, both,
			"SessionDetailHeader.tsx:129 停止按钮按 running 无条件渲染,不筛 kind"},
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SET_PERMISSION_MODE, []Caller{CallerConsole, CallerDesktopToAgentred}, both,
			"SessionDetailView.tsx:1262"},
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_ANSWER, []Caller{CallerConsole, CallerDesktopToDesktop, CallerDesktopToAgentred}, both,
			"useSessionDecisionPorts.ts:272,经共享包 TranscriptPorts 递进转录卡片"},
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_TOOL_PERMISSION, []Caller{CallerConsole, CallerDesktopToDesktop, CallerDesktopToAgentred}, both,
			"useSessionDecisionPorts.ts:252,同上"},

		// ── 外围族 ──
		{agentrewire.RpcMethod_RPC_METHOD_SKILLS_CATALOG, []Caller{CallerConsole}, both,
			"skillCatalog.ts:109,执行目标档的设备不筛 kind(dispatch.ts:44)"},
		{agentrewire.RpcMethod_RPC_METHOD_SKILLS_COMMANDS, []Caller{CallerConsole, CallerDesktopToAgentred}, both,
			"skillCatalog.ts:214,fingerprint 来自会话目标机,不筛 kind"},
		{agentrewire.RpcMethod_RPC_METHOD_REMOTE_FS_LIST_DIR, []Caller{CallerConsole, CallerDesktopToAgentred}, both,
			"remotefs.ts:84;projectPorts.ts:170 toPickerMachine 两种 kind 都交给选择器"},
		{agentrewire.RpcMethod_RPC_METHOD_REMOTE_FS_MKDIR, []Caller{CallerConsole, CallerDesktopToAgentred}, both,
			"remotefs.ts:107,同上"},
		{agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_READ_FILE, []Caller{CallerConsole, CallerDesktopToAgentred}, both,
			"filePreviewPorts.ts:117,client 是详情页通道,不筛 kind"},
		{agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_FILE_CONTENT, []Caller{CallerConsole, CallerDesktopToAgentred}, both,
			"filePreviewPorts.ts:137,同上"},

		// ── 导入族:server 后端发,机器清单含 desktop ──
		{agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_SCAN, []Caller{CallerServerBackend, CallerDesktopToAgentred}, both,
			"useMachineReachability.tsx:275 机器清单含 desktop → SessionIndex.tsx:1032 喂给导入端口;sessionimport_ctr 全链路不筛 kind"},
		{agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_OPEN, []Caller{CallerServerBackend, CallerDesktopToAgentred}, both, "同 scan"},
		{agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_TURNS, []Caller{CallerServerBackend, CallerDesktopToAgentred}, both, "同 scan"},
		{agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_EXECUTE, []Caller{CallerServerBackend}, both, "同 scan"},

		// ── 引擎/CLI 探测:调用点经 executionDevice() 放行 desktop,因此两种都在义务里。
		// 这三条目前对桌面端是欠着的(见 KnownGaps),而 ENGINE_DISCOVER 恰好只走
		// onlineAgentred() 那条路 —— 它安全是**巧合,不是设计不变量**,所以两者分开列。
		{agentrewire.RpcMethod_RPC_METHOD_ENGINE_SCAN, []Caller{CallerConsole}, both,
			"enginePorts.ts:457 scanDevice → executionDevice(:420) → isExecutionDevice(:76) 含 desktop"},
		{agentrewire.RpcMethod_RPC_METHOD_ENGINE_TEST, []Caller{CallerConsole}, both,
			"enginePorts.ts:774 testBackend → executionDevice(:771);另一处 :728 经 onlineAgentred() 已收窄"},
		{agentrewire.RpcMethod_RPC_METHOD_CLI_RESOLVE_PATH, []Caller{CallerConsole, CallerDesktopToAgentred}, both,
			"enginePorts.ts:802 resolveBackendCLIPath → executionDevice(:799)"},

		// ── 只有 agentred 会被问到的 ──
		{agentrewire.RpcMethod_RPC_METHOD_ENGINE_DISCOVER, []Caller{CallerConsole}, []HostKind{HostAgentred},
			"enginePorts.ts:744 经 relayRequest → onlineAgentred()(:412)收窄成 agentred"},
		{agentrewire.RpcMethod_RPC_METHOD_AGENTRED_SELF_UPDATE, []Caller{CallerServerBackend, CallerDesktopToAgentred}, []HostKind{HostAgentred},
			"自更新按定义只对 agentred 有意义;桌面端的版本由它自己的更新流程管"},
		{agentrewire.RpcMethod_RPC_METHOD_AUTH_PAIR, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "remote_device_svc/dial.go:34 局域网配对"},
		{agentrewire.RpcMethod_RPC_METHOD_AUTH_CONNECT, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "remote_device_svc/dial.go:53"},
		{agentrewire.RpcMethod_RPC_METHOD_HEALTH_PING, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "remote_device_watcher_svc/watcher.go:188"},
		{agentrewire.RpcMethod_RPC_METHOD_CLAUDE_CODE_USAGE, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "internal/app/cc_usage.go:60"},
		{agentrewire.RpcMethod_RPC_METHOD_LLM_UPSERT, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "remote_device_svc/conn_pool.go:154"},
		{agentrewire.RpcMethod_RPC_METHOD_SKILLS_LIST, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "agent_backend_svc/remote_skills.go:49"},
		{agentrewire.RpcMethod_RPC_METHOD_CLI_PROBE, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "agent_backend_svc/remote_cli.go:81"},
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_DRAIN_PENDING, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "remote/runtime.go:1006"},
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STOP_BACKGROUND_TASK, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "remote/runtime.go:1041"},
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_GET, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "remote/runtime.go:1113"},
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_SET, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "remote/runtime.go:1129"},
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_CLEAR, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "remote/runtime.go:1145"},
		{agentrewire.RpcMethod_RPC_METHOD_TERMINAL_OPEN, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "pty/remote/client_adapter.go:108"},
		{agentrewire.RpcMethod_RPC_METHOD_TERMINAL_WRITE, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "pty/remote/client_adapter.go:115"},
		{agentrewire.RpcMethod_RPC_METHOD_TERMINAL_RESIZE, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "pty/remote/client_adapter.go:119"},
		{agentrewire.RpcMethod_RPC_METHOD_TERMINAL_CLOSE, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "pty/remote/client_adapter.go:123"},
		{agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_LIST_DIR, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "workspace_fs_svc/svc.go:292"},
		{agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_SEARCH_FILES, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "workspace_fs_svc/svc.go:419"},
		{agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_BRANCHES, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "workspace_fs_svc/svc.go:459"},
		{agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_STATE, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "workspace_fs_svc/svc.go:506"},
		{agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_CHANGES, []Caller{CallerDesktopToAgentred}, []HostKind{HostAgentred}, "workspace_fs_svc/svc.go:567"},

		// ── 只有桌面端会被问到的 ──
		{agentrewire.RpcMethod_RPC_METHOD_PROJECT_SET_LOCAL_PATH, []Caller{CallerConsole}, []HostKind{HostDesktop},
			"projectPorts.ts:187 machine.kind === \"agentred\" 走 REST,只有非 agentred 才经中继"},
		{agentrewire.RpcMethod_RPC_METHOD_PROJECT_CLEAR_LOCAL_PATH, []Caller{CallerConsole}, []HostKind{HostDesktop},
			"projectPorts.ts:140 同上"},
	}
}

// KnownGap 是一条**已知未修**的缺口:调用方发得出,而这一侧答不出。
//
// 它存在不是为了让守卫变绿,而是为了让缺口从「读代码才看得见」变成「写在这里、
// 数得清、每一条都欠一个理由」。新长出来的缺口不在表里,守卫立刻判红;修好的缺口
// 忘了删,守卫同样判红(见 TestContract_KnownGapsAreStillGaps)——两个方向都钉住,
// 这张表才不会退化成一张越积越长、没人敢删的豁免清单。
type KnownGap struct {
	Method agentrewire.RpcMethod
	Host   HostKind
	Reason string
}

// KnownGaps 是当前欠着的那些。**每修好一条就删一行** —— 忘了删,守卫会替你记得
// (CheckContract 的 Stale 那一半)。
func KnownGaps() []KnownGap {
	return []KnownGap{
		{agentrewire.RpcMethod_RPC_METHOD_RUNTIME_ABORT, HostDesktop,
			"控制台对桌面端会话的「停止」是死按钮:-32601 被 SessionDetailHeader.tsx:135 的 catch{} 吞掉,按下去毫无反应。桌面端要么实现它,要么那颗按钮不该渲染"},
		{agentrewire.RpcMethod_RPC_METHOD_ENGINE_SCAN, HostDesktop,
			"enginePorts.ts:457 经 executionDevice() 放行 desktop(:76 EXECUTION_DEVICE_KINDS 含 desktop),而引擎快照是 agentred 的账号级能力。要么桌面端实现,要么那处筛选收窄成 agentred"},
		{agentrewire.RpcMethod_RPC_METHOD_ENGINE_TEST, HostDesktop,
			"enginePorts.ts:774 的 testBackend 分支同上。另一处 :728 经 onlineAgentred() 已收窄,是安全的 —— 同一个方法两个调用点限定不同"},
		{agentrewire.RpcMethod_RPC_METHOD_CLI_RESOLVE_PATH, HostDesktop,
			"enginePorts.ts:802 同上"},
		{agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_READ_FILE, HostDesktop,
			"控制台文件预览按会话目标机拨,不筛 kind;桌面端没挂 WorkspaceFS 端口。规格 2026-09-06-console-file-preview 落地时要一并解决"},
		{agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_FILE_CONTENT, HostDesktop,
			"同上"},
	}
}

// ContractResult 是一次比对的结论。两个方向都要为空才算过。
type ContractResult struct {
	// Missing 是这一侧该实现却没实现、且不在 KnownGaps 里的。**新长出来的缺口。**
	Missing []Requirement
	// Stale 是记在 KnownGaps 里、但这一侧其实已经实现了的。修好了忘了删这一行,
	// 豁免清单就开始腐烂 —— 它下一次会掩护一个真缺口。
	Stale []KnownGap
}

// CheckContract 拿一侧的注册面比对这张表。
//
// 宿主各自调它,因为构造一个生产注册面要宿主自己的装配代码(桌面端的
// productionProtobufInboundDeps、agentred 的 daemon.New),那是包外面的事。
func CheckContract(host HostKind, registered []uint32) ContractResult {
	have := make(map[uint32]bool, len(registered))
	for _, methodID := range registered {
		have[methodID] = true
	}
	gapped := make(map[uint32]bool)
	for _, gap := range KnownGaps() {
		if gap.Host == host {
			gapped[uint32(gap.Method)] = true
		}
	}

	var out ContractResult
	required := make(map[uint32]bool)
	for _, req := range Contract() {
		if !slices.Contains(req.Hosts, host) {
			continue
		}
		required[uint32(req.Method)] = true
		if !have[uint32(req.Method)] && !gapped[uint32(req.Method)] {
			out.Missing = append(out.Missing, req)
		}
	}
	for _, gap := range KnownGaps() {
		if gap.Host != host {
			continue
		}
		// 一条豁免只有在「这一侧确实该实现、而且确实没实现」时才成立。已经实现了,
		// 或者压根不在这一侧的义务里,都是这行该被删掉的信号。
		if have[uint32(gap.Method)] || !required[uint32(gap.Method)] {
			out.Stale = append(out.Stale, gap)
		}
	}
	return out
}

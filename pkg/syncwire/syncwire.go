// Package syncwire 是桌面端 ↔ server 工作区同步协议的线上契约:结构、词表与上限。
//
// 从前这份契约在工作区里存在**三份**:
//
//   - agentre/internal/pkg/syncwire —— 领域侧的结构,刻意不带 json 标签
//     (Payload 是 []byte,直接 Marshal 会被编成 base64);
//   - agentre/internal/service/server_svc/sync.go —— 私有的一套,只为补上 json 标签;
//   - agentre-server/internal/api/sync —— 服务端那份,带 gin 的 binding 标签。
//
// 三份的字段集其实一直是对齐的,差的是标签。而标签在这条协议里是**承重的**:
// PushItem.Payload 少一个 omitempty,墓碑就会带上 JSON null,server 的 ValidatePayload
// 判 root.(map[string]any) 失败、整批拒(30501),出站队列被一次删除永久堵死。桌面端
// 那份有 omitempty,服务端那份没有 —— 它不需要,因为它只解不编。合成一份时照搬哪一边,
// 决定了这个坑装不装回来,syncwire_test.go 把它钉住了。
//
// 一个结构同时承担两侧的角色,所以两套标签都要在:json 标签管桌面端的编码与服务端的
// 解码,binding 标签管服务端的入参校验。binding 标签是惰性的,只有 gin 绑定时才读,
// 本 module 因此零外部依赖。
package syncwire

import "encoding/json"

// ── 对象类型词表 ────────────────────────────────────────────────────────────

// 同步组承载的对象类型,与 server 的 sync_entity 逐字一致。
const (
	KindProject         = "project"
	KindDepartment      = "department"
	KindAgent           = "agent"
	KindAgentBackend    = "agent_backend"
	KindAgentExecTarget = "agent_exec_target"
	KindProjectAgent    = "project_agent"
	KindProjectLocation = "project_location"
	KindLLMProvider     = "llm_provider"
	KindAgentBackendCLI = "agent_backend_cli"
	// KindLabel / KindIssue / KindIssueLabel 是看板并入账号级同步组带来的三个类型。
	// 它们互相引用的方向是 label ← issue_label → issue,任务本身还引用项目、Agent
	// 与 backend —— 全部用同步标识表达,载荷里没有一个本地自增 ID。
	KindLabel      = "label"
	KindIssue      = "issue"
	KindIssueLabel = "issue_label"
)

// ── 处置结果词表 ────────────────────────────────────────────────────────────

const (
	// PushStatusAccepted 基版本与该行当前版本相符,或该同步标识 server 从未见过。
	PushStatusAccepted = "accepted"
	// PushStatusConflict 基版本与当前版本不符,或基版本为空但同步标识已存在。
	// 本次上行按后到者胜照常生效,应答里回报被覆盖的版本与来源设备。
	PushStatusConflict = "conflict"
	// PushStatusRejected 这一条没有生效,原因见 Reason。
	PushStatusRejected = "rejected"
)

// 单条拒绝的原因。
//
// **凡是能拒掉一条的理由,都只拒那一条。** 整批拒是一个永久性的堵:上行端整批失败时
// 一行都不出队,下一轮再发同一批、再被同一条拒掉,那台机器的上行队列从此不动 —— 连
// 删除也传不出去。校验不通过的行以 rejected 回报,上行端据此把它移出队列并记进
// 「没能同步的改动」。
const (
	// PushRejectReasonDeleted 该对象在 server 上已是墓碑。删除不会被复活,恢复动作
	// 因此明确失败;界面据此提供「按这份内容新建」—— 那是一个新的同步标识。
	PushRejectReasonDeleted = "deleted"
	// PushRejectReasonKind 对象类型不属于同步组、与该同步标识已有行的类型不符,
	// 或缺少该类型必需的自然键。
	PushRejectReasonKind = "kind_invalid"
	// PushRejectReasonPayload 载荷过不了服务端 ValidatePayload 的守卫。
	PushRejectReasonPayload = "payload_rejected"
)

// ── 业务码 ──────────────────────────────────────────────────────────────────

// CodeResyncRequired 是「设备距上次成功同步已超过墓碑保留窗口」的业务码。
const CodeResyncRequired = 30500

// CodeCursorUnknown 是「下行游标超出本账号版本序列的头」的业务码:那段历史 server
// 不认识 —— 库被重建,或用户换了一套自建服务端。
const CodeCursorUnknown = 30505

// ── 上限 ────────────────────────────────────────────────────────────────────

// MaxPushBatch / MaxPullLimit / MaxLocalPathItems 是三个请求的批量上限。
//
// 它们与 agentre-server 那三条 gin 标签里的字面量逐字相符 —— 标签写不了常量引用,
// 所以那一致性由消费仓自己的守卫盯着。桌面端一次实际发多少是它自己的选择,只要
// 不超过 MaxPushBatch。
const (
	MaxPushBatch      = 500
	MaxPullLimit      = 1000
	MaxLocalPathItems = 2000
)

// ── 线上结构 ────────────────────────────────────────────────────────────────

// PushItem 是一次上行里的一条改动。
type PushItem struct {
	Kind   string `json:"kind"    binding:"required,max=32"`
	SyncID string `json:"sync_id" binding:"required,max=128"`
	// BaseVersion 是发起端最后一次见到的同步版本号;本端新建、server 从未见过的行填 0。
	BaseVersion int64 `json:"base_version"`
	// UpdatedAt 是发起端的最后修改时间,只用于展示与 30 天窗口计算,不参与冲突裁决。
	UpdatedAt int64 `json:"updated_at"`
	// DeletedAt 非零表示这是一条墓碑,值是发起端记下的删除时刻(Unix 毫秒)。
	// 契约上是**时刻**而不是布尔:发起端库、线格式与 server 库三处都是时刻,压成布尔
	// 之后 server 落地只能另行编造一个删除时间(2026-08-27-schema-overhaul 决策 20)。
	DeletedAt           int64  `json:"deleted_at"`
	AgentredFingerprint string `json:"agentred_fingerprint" binding:"max=128"`
	// ScopeSyncID 装什么取决于 kind(project_location 装项目、agent_backend_cli
	// 装后端),与 server 的 sync_objects.scope_sync_id 同义。
	ScopeSyncID string `json:"scope_sync_id" binding:"max=128"`
	// Payload 的 omitempty 是**承重的**:墓碑不带正文,而 json.RawMessage 的零值编出
	// 来是 JSON null —— null 不是对象,server 的 ValidatePayload 会整批拒(30501),
	// 一次删除就把出站队列永久堵死。类型必须是 json.RawMessage 而不是 []byte,否则
	// encoding/json 会把整份文档编成 base64。
	Payload json.RawMessage `json:"payload,omitempty"`
}

// PushResult 是一条上行的处置结果。
//
// Overwritten* 只在 Status 为 conflict 时有值:被这次上行覆盖掉的是哪一版、来自哪台
// 机器、正文是什么。正文只有 server 有 —— 上行端手上那一份是**覆盖别人的**那一份,
// 「追回被覆盖的那一版」靠它。
//
// Merged* 只在自然键合并发生时有值:落败那一份的同步标识、版本与来源机器,它已在
// server 落墓碑。
type PushResult struct {
	SyncID string `json:"sync_id"`
	Kind   string `json:"kind"`
	// Version 是 server 为这次上行分配的新版本号;被拒时是 server 上的当前版本。
	Version                      int64           `json:"version"`
	Status                       string          `json:"status"`
	Reason                       string          `json:"reason,omitempty"`
	OverwrittenVersion           int64           `json:"overwritten_version,omitempty"`
	OverwrittenOriginFingerprint string          `json:"overwritten_origin_fingerprint,omitempty"`
	OverwrittenPayload           json.RawMessage `json:"overwritten_payload,omitempty"`
	MergedSyncID                 string          `json:"merged_sync_id,omitempty"`
	MergedVersion                int64           `json:"merged_version,omitempty"`
	// MergedOriginFingerprint 是落败那一份来自哪台机器(决策 14);空串 = 服务端直写。
	MergedOriginFingerprint string `json:"merged_origin_fingerprint,omitempty"`
}

// PullItem 是下行的一行,墓碑也在其中(DeletedAt > 0),删除靠它到达各端。
type PullItem struct {
	Kind                string          `json:"kind"`
	SyncID              string          `json:"sync_id"`
	ScopeSyncID         string          `json:"scope_sync_id,omitempty"`
	AgentredFingerprint string          `json:"agentred_fingerprint,omitempty"`
	Payload             json.RawMessage `json:"payload"`
	Version             int64           `json:"version"`
	UpdatedAt           int64           `json:"updated_at"`
	// OriginFingerprint 是最后一次修改来自哪台机器(决策 14:跨机引用一律用指纹,
	// 数值设备主键是 server 的本地键,桌面端离线创建的行没有它)。空串 = 服务端直写。
	OriginFingerprint string `json:"origin_fingerprint"`
	// DeletedAt 非零 = 墓碑,值是删除时刻(Unix 毫秒,决策 20)。
	DeletedAt int64 `json:"deleted_at"`
}

// PullPage 是一次下行的一页。
type PullPage struct {
	Items      []PullItem `json:"items"`
	NextCursor int64      `json:"next_cursor"`
	HasMore    bool       `json:"has_more"`
}

// LocalPathItem 是上报组的一条:某个项目在这台设备上的真实本机路径。
//
// 与同步组的那些表无关 —— 本机路径不在桌面端之间流动,只单向上报给 server,
// 按设备分命名空间存放。
type LocalPathItem struct {
	ProjectSyncID string `json:"project_sync_id" binding:"required,max=128"`
	Path          string `json:"path"            binding:"max=1024"`
}

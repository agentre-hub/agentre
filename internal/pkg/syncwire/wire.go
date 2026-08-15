// Package syncwire 定义桌面端与 server 之间工作区同步协议的线上结构、状态字面量
// 与载荷守卫（docs/specs/2026-08-07-workspace-sync.md「双向同步的行为」）。
//
// 它是一个叶子包：同步引擎（internal/service/sync_svc）与网络出入口
// （internal/service/server_svc）都依赖它，因此两者之间不需要互相 import。
//
// 载荷里不出现任何桌面端的本地自增 ID：跨机引用一律是同步标识、agentred 指纹或
// provider_key，全是字符串。GuardPayload 在上行前强制执行这条边界。
package syncwire

import (
	"encoding/json"
	"errors"
)

// 同步组承载的对象类型，与 server 的 sync_entity 逐字一致。
const (
	KindProject         = "project"
	KindDepartment      = "department"
	KindAgent           = "agent"
	KindAgentBackend    = "agent_backend"
	KindAgentExecTarget = "agent_exec_target"
	KindProjectAgent    = "project_agent"
	KindProjectLocation = "project_location"
)

// 一条上行的处置结果（server 的 sync_svc 常量）。
const (
	// PushStatusAccepted 基版本与该行当前版本相符，或该同步标识 server 从未见过。
	PushStatusAccepted = "accepted"
	// PushStatusConflict 基版本与当前版本不符，或基版本为空但同步标识已存在。
	// 本次上行按后到者胜照常生效，应答里回报被覆盖的版本与来源设备，上行端
	// 据此落一条「被覆盖」记录。
	PushStatusConflict = "conflict"
	// PushStatusRejected 这一条没有生效，原因见 Reason。
	PushStatusRejected = "rejected"
)

// PushRejectReasonDeleted 是唯一的单条拒绝原因：该对象在 server 上已是墓碑。
// 删除不会被复活，恢复动作因此明确失败。
const PushRejectReasonDeleted = "deleted"

// CodeResyncRequired 是 server 在「设备距上次成功同步已超过墓碑保留窗口」时返回的
// 业务码（agentre-server internal/pkg/code.SyncResyncRequired）。
const CodeResyncRequired = 30500

// ErrResyncRequired 是 CodeResyncRequired 的客户端表达：上行一律被拒，必须先拉一份
// 全量快照并以之为准。
var ErrResyncRequired = errors.New("sync: resync required")

// CodeCursorUnknown 是 server 在「下行游标超出本账号版本序列的头」时返回的业务码
// （agentre-server internal/pkg/code.SyncCursorUnknown）：那段历史它不认识——库被
// 重建，或用户换了一套自建服务端。
const CodeCursorUnknown = 30505

// ErrCursorUnknown 是 CodeCursorUnknown 的客户端表达。
//
// 它与 ErrResyncRequired **不是**一回事，处置也相反：
//
//   - ErrResyncRequired（上行时）＝「你离线太久」。server 的历史是全的、本端的不全，
//     以快照为准，队列里基版本对不上的一律拦下（R6a）——那正是防复活的那一条。
//   - ErrCursorUnknown（下行时）＝「我不认识你说的那段历史」。server 的历史没了、
//     本端的才是全的，因此必须把 server 不认识的本地行**重新上行**，否则整个工作区
//     静默留在本机，而界面上待同步是 0、没有任何错误可循。
var ErrCursorUnknown = errors.New("sync: server does not recognize this cursor")

// PushItem 是一次上行里的一条改动。
//
// 没有 json 标签是刻意的：Payload 是一份**已经序列化好的 JSON 文档**，直接
// json.Marshal 这个结构会把它编成 base64。上下行的线上编码只在 server_svc 里做，
// 那里把它换成 json.RawMessage。
type PushItem struct {
	Kind   string
	SyncID string
	// BaseVersion 是本端最后一次见到的同步版本号；本端新建、server 从未见过的行填 0。
	BaseVersion int64
	// UpdatedAt 是本端的最后修改时间，只用于展示与 30 天窗口计算，不参与冲突裁决。
	UpdatedAt           int64
	Deleted             bool
	AgentredFingerprint string
	ProjectSyncID       string
	Payload             []byte
}

// PushResult 是一条上行的处置结果。
type PushResult struct {
	SyncID string `json:"sync_id"`
	Kind   string `json:"kind"`
	// Version 是 server 为这次上行分配的新版本号；被拒时是 server 上的当前版本。
	Version int64  `json:"version"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	// OverwrittenVersion / OverwrittenDeviceID / OverwrittenPayload 只在 Status 为
	// conflict 时有值：被这次上行覆盖掉的是哪一版、来自哪台设备、正文是什么。
	//
	// 正文只有 server 有：本端手上那一份是**覆盖别人的**那一份。为了能够追回被
	// 覆盖的那一版」靠它，落一条「被覆盖」记录时记的必须是这一份。
	OverwrittenVersion  int64           `json:"overwritten_version"`
	OverwrittenDeviceID int64           `json:"overwritten_device_id"`
	OverwrittenPayload  json.RawMessage `json:"overwritten_payload"`
	// MergedSyncID / MergedVersion / MergedDeviceID 只在自然键合并发生时有值：
	// 落败那一份的同步标识、版本与来源设备，它已在 server 落墓碑。
	MergedSyncID   string `json:"merged_sync_id"`
	MergedVersion  int64  `json:"merged_version"`
	MergedDeviceID int64  `json:"merged_device_id"`
}

// PullItem 是下行的一行，墓碑也在其中（Deleted = true），删除靠它到达各端。
// 与 PushItem 同理，线上编码在 server_svc 里做，这里不带 json 标签。
type PullItem struct {
	Kind                string
	SyncID              string
	ProjectSyncID       string
	AgentredFingerprint string
	Payload             []byte
	Version             int64
	UpdatedAt           int64
	SourceDeviceID      int64
	Deleted             bool
}

// PullPage 是一次下行的一页。
type PullPage struct {
	Items      []PullItem
	NextCursor int64
	HasMore    bool
}

// LocalPathReportItem 是上报组的一条：某个项目在这台设备上的真实本机路径。
// 与同步组的七张表无关——本机路径不在桌面端之间流动，只单向上报给
// server，按设备分命名空间存放。
type LocalPathReportItem struct {
	ProjectSyncID string
	Path          string
}

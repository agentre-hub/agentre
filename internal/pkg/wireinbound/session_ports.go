package wireinbound

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/activityrollup"
	remotewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

// SessionPorts 是会话族这 16 个方法的宿主实现面。
//
// 会话族的**线形状**(解请求 → 调端口 → 编应答 → 映射错误)对两种执行端是同一份:
// 浏览器接的是 agentred 还是桌面 App,收到的应答必须逐字段同形,否则调用方就得先猜
// 对面是哪一种。此前这一份形状在 internal/daemon 与 internal/peer 各手写了一遍,
// 代价已经兑现过:session.list 的 conversation_ids 收窄桌面端做了、agentred 漏了,
// 编译器和测试都不会红,浏览器要一条会话的摘要却把整台机器搬了回去。
//
// 留在宿主的是**端口**:归属判定、账号可见性、认领与订阅、前置校验、以及下面这三个
// 刻意不统一的横切格。
type SessionPorts struct {
	// Auth 是这一族的鉴权闸门。
	//
	// 它**故意是端口而不是共用的一句话**:两宿主的拒绝语在线上不是同一句 ——
	// agentred 答 rpcerror.ErrUnauthorized(-32001,大写 U 的 "Unauthorized"),
	// 桌面端答 -32001 + 小写的 "unauthorized"。码相同、message 不同,而 message
	// 同样在线上,统一掉就是一次没人点头的协议改动。
	//
	// nil ⇒ **整族不注册**。缺了闸门的一族只能整族关掉:开着而没有闸门,等于把会话
	// 正文交给任何一条连上来的连接 —— 那比 method not found 坏得多。
	Auth func(context.Context) error
	// Error 把宿主的领域错误折成线上的错误。
	//
	// 它同样**故意是端口**:两宿主认的错误集本就不同 —— 桌面端的 protobufPeerError
	// 多认 chat_svc.ErrPeerSessionNotFound(→ -32002),agentred 的 protobufError 认
	// *rpcerror.Error,而它的 runtime 族用的 protobufRuntimeError 还先过
	// remotewire.ToRPCError 换一个领域码。并成一个 switch 会让某一侧的调用方收到一个
	// 它此前从没见过的码。
	//
	// nil ⇒ 整族不注册:答不出话就别开口。
	Error func(error) error
	// DecodeError 只用在 runtime.run 上:请求体解不开时的映射。
	//
	// 它与 Error 分开,是因为两宿主在这一格上答得**不一样** —— agentred 答 -32602
	// invalid params,桌面端把它并进了 Error 因而答 -32603 internal。这条差异既存,
	// 本次收编不替它们做主(两条路其实都不可达:RunRequestFromProto 只在请求为 nil
	// 或 backend 无法 json.Marshal 时失败)。
	//
	// nil ⇒ runtime.run 不注册。
	DecodeError func(error) error

	// ── 清单与统计 ──────────────────────────────────────────────────────────
	List           func(context.Context, remotewire.SessionListParams) (remotewire.SessionListResult, error)
	Counts         func(context.Context) (remotewire.SessionCountsResult, error)
	ActivityRollup func(ctx context.Context, sinceDay, timeZone string) ([]activityrollup.Bucket, error)

	// ── 补齐与归属 ──────────────────────────────────────────────────────────
	//
	// Attach / Pull 的订阅者由端口自己从 ctx 上的连接取(桌面端要把实时流推回这条
	// 连接,agentred 不需要)—— 共用注册面因此不必认识任何一种订阅者类型。
	Attach             func(context.Context, remotewire.SessionAttachParams) (remotewire.SessionAttachResult, error)
	Pull               func(context.Context, remotewire.SessionPullParams) (remotewire.SessionPullResult, error)
	PendingWaiters     func(context.Context, remotewire.SessionPendingWaitersParams) (remotewire.SessionPendingWaitersResult, error)
	Delete             func(context.Context, remotewire.SessionDeleteParams) (remotewire.SessionDeleteResult, error)
	SetModelTarget     func(context.Context, remotewire.SetModelTargetParams) error
	SetReasoningEffort func(context.Context, remotewire.SetSessionReasoningEffortParams) error

	// ── 跑一轮与轮内控制 ────────────────────────────────────────────────────
	Capabilities         func(context.Context, remotewire.CapabilitiesParams) (remotewire.CapabilitiesResult, error)
	Run                  func(context.Context, remotewire.RunParams) (remotewire.RunAck, error)
	Steer                func(context.Context, remotewire.SteerParams) (remotewire.SteerResult, error)
	CancelSteer          func(context.Context, remotewire.CancelSteerParams) (remotewire.CancelSteerResult, error)
	SubmitAnswer         func(context.Context, remotewire.SubmitAnswerParams) (remotewire.PeerSessionControlResult, error)
	SubmitToolPermission func(context.Context, remotewire.SubmitToolPermissionParams) (remotewire.PeerSessionControlResult, error)
	SetPermissionMode    func(context.Context, remotewire.SetPermissionModeParams) error
}

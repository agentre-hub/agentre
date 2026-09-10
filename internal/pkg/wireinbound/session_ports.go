package wireinbound

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/activityrollup"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// SessionPorts 是会话族这 16 个方法的宿主实现面。
//
// 本包守住的是**注册纪律与契约**,不是字段搬运:
//
//  1. 哪些方法存在 —— 方法号 ↔ 请求消息类型的配对,入站这一侧只有这一处。此前它在
//     internal/daemon 与 internal/peer 各写了一遍,代价已经兑现过:session.list 的
//     conversation_ids 收窄桌面端做了、agentred 漏了,编译器和测试都不会红。
//  2. 鉴权闸门与错误映射是**端口**(见 Auth / Error),各宿主传自己那一份。
//  3. **端口缺席 ⇒ 那个方法不注册**,调用方收到 method not found。
//  4. 它是两条契约守卫(contract.go)的施力对象。
//
// 端口收发的是**线上的载体**而不是领域参数。两种执行端在这一格上说的话本就不同:
// agentred 的 handler 自己就说 agentrewire(它的通知日志里存的本来就是这一帧的
// protobuf 原样,再翻成领域词表又翻回来是每拉一行白走一个来回);桌面端的依赖说
// chat_svc 的领域值。端口跟着**宿主真正说的话**定形,于是 agentred 直接绑 handler、
// 一次转换都不做,桌面端在自己的装配处解一次 —— 且解的时候调的是本包导出的映射函数
// (SessionListParamsOf 一族),字段清单因此仍然只有一份。
//
// 这与 PeripheralDeps 里 MCPProxy / ProjectSetPath 早就直接收发 protobuf 是同一条
// 判据,不是新开的口子。
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

	// ── 清单与统计 ──────────────────────────────────────────────────────────
	//
	// ActivityRollup 收的仍是领域值:两种执行端在这一格上交出的都是
	// activityrollup.Bucket,没有哪一侧说 protobuf,端口因此没有理由说。
	List           func(context.Context, *agentrewire.SessionListRequest) (*agentrewire.SessionListResponse, error)
	Counts         func(context.Context) (*agentrewire.SessionCountsResponse, error)
	ActivityRollup func(ctx context.Context, sinceDay, timeZone string) ([]activityrollup.Bucket, error)

	// ── 补齐与归属 ──────────────────────────────────────────────────────────
	//
	// Attach / Pull 的订阅者由端口自己从 ctx 上的连接取(桌面端要把实时流推回这条
	// 连接,agentred 不需要)—— 共用注册面因此不必认识任何一种订阅者类型。
	Attach             func(context.Context, *agentrewire.SessionAttachRequest) (*agentrewire.SessionAttachResponse, error)
	Pull               func(context.Context, *agentrewire.SessionPullRequest) (*agentrewire.SessionPullResponse, error)
	PendingWaiters     func(context.Context, *agentrewire.SessionPendingWaitersRequest) (*agentrewire.SessionPendingWaitersResponse, error)
	Delete             func(context.Context, *agentrewire.SessionDeleteRequest) (*agentrewire.SessionDeleteResponse, error)
	SetModelTarget     func(context.Context, *agentrewire.SetModelTargetRequest) (*agentrewire.SetModelTargetResponse, error)
	SetReasoningEffort func(context.Context, *agentrewire.SetSessionReasoningEffortRequest) (*agentrewire.SetSessionReasoningEffortResponse, error)

	// ── 跑一轮与轮内控制 ────────────────────────────────────────────────────
	Capabilities         func(context.Context, *agentrewire.RuntimeCapabilitiesRequest) (*agentrewire.RuntimeCapabilitiesResponse, error)
	Run                  func(context.Context, *agentrewire.RuntimeRunRequest) (*agentrewire.RuntimeRunResponse, error)
	Steer                func(context.Context, *agentrewire.RuntimeSteerRequest) (*agentrewire.RuntimeSteerResponse, error)
	CancelSteer          func(context.Context, *agentrewire.RuntimeCancelSteerRequest) (*agentrewire.RuntimeCancelSteerResponse, error)
	SubmitAnswer         func(context.Context, *agentrewire.RuntimeSubmitAnswerRequest) (*agentrewire.PeerSessionControlResponse, error)
	SubmitToolPermission func(context.Context, *agentrewire.RuntimeSubmitToolPermissionRequest) (*agentrewire.PeerSessionControlResponse, error)
	SetPermissionMode    func(context.Context, *agentrewire.RuntimeSetPermissionModeRequest) (*agentrewire.Empty, error)
	// Abort 停掉这一条会话**正在跑**的那一轮(CancelSteer 撤的是还没被取走的排队
	// 消息,不是同一件事)。
	//
	// agentred 不从这里挂它:它的 runtime 族挂在**每条连接**的注册面上,还要先占一张
	// claim ticket —— 那是它的会话归属模型,不是这一族共用的形状。端口缺席 ⇒ 不注册,
	// 所以两边不会重复挂上同一个方法号。
	Abort func(context.Context, *agentrewire.RuntimeAbortRequest) (*agentrewire.RuntimeAbortResponse, error)
}

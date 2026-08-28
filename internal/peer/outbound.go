// outbound.go 是桌面端会话级的**出站**对端客户端（任务 10，R18/R19 的出站半边；
// 入站半边在 inbound.go）。传输层复用 server_svc 经账号中继产出的 *client.Client
// （DialDaemonRelay / DialDesktopRelay），本文件不新增任何传输：它只把既有的 wire
// 会话族（list / attach / pull / run / steer / answer / tool-permission）包装成
// 类型化方法，并提供 runtime.event 通知订阅——桌面 A 派活给桌面 B、以及按
// 「设备 → 会话列表 → 一条会话」接入 B 上既有会话，走的都是这一套。
package peer

import (
	"context"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// Outbound 是拨到另一台桌面端（或 agentred）的会话级客户端。调用方把
// server_svc.DialDesktopRelay 产出的已握手 *client.Client 交给它，peer 指纹
// 只在构造时记录为只读标识，不参与鉴权。
type Outbound struct {
	c  client.ProtobufConnection
	fp string
}

// NewOutbound 包装一条已握手、已鉴权的对端中继连接。peerFingerprint 是该目标的
// 设备指纹（DialDesktopRelay 的目标），仅用于会话清单的 PeerFingerprint 语义。
func NewOutbound(c client.ProtobufConnection, peerFingerprint string) *Outbound {
	return &Outbound{c: c, fp: peerFingerprint}
}

// PeerFingerprint 返回这条连接指向的对端指纹（会话合并键的一半，见 R20）。
func (o *Outbound) PeerFingerprint() string { return o.fp }

// Closed 在底层中继连接断开时关闭。
func (o *Outbound) Closed() <-chan struct{} { return o.c.Closed() }

// Close 释放中继连接。幂等。
func (o *Outbound) Close() error { return o.c.Close() }

// ListSessions 列出对端桌面上的全部会话（R19 / R4）。应答形状复用
// wire.SessionSummary，标题、状态、等待输入、最后活动与 Agent 身份齐全，
// 不存在轮 A 的退化形态。
func (o *Outbound) ListSessions(ctx context.Context) (*wire.SessionListResult, error) {
	response, err := protorpc.CallMethod(ctx, o.c.Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_LIST), &agentrewire.SessionListRequest{}, func() *agentrewire.SessionListResponse { return &agentrewire.SessionListResponse{} })
	if err != nil {
		return nil, err
	}
	result := &wire.SessionListResult{}
	for _, s := range response.Sessions {
		result.Sessions = append(result.Sessions, wire.SessionSummary{SessionID: s.SessionId, PeerFingerprint: s.PeerFingerprint, AgentID: s.AgentId, Title: s.Title, AgentSyncID: s.AgentSyncId, ProviderSessionID: s.ProviderSessionId, Cwd: s.Cwd, ProjectSyncID: s.ProjectSyncId, BackendType: s.BackendType, LifecycleState: s.LifecycleState, WaitingForInput: s.WaitingForInput, LatestSeq: s.LatestSeq, LastMessageAt: s.LastMessageAt, ProviderKey: s.ProviderKey, ModelKey: s.ModelKey})
	}
	return result, nil
}

// Attach 把这条连接登记为某条远程会话的实时订阅者（R19 / R6）：此后对端把该会话
// 的 canonical 事件经 runtime.event 推回本连接，直到 Close。LatestSeq 是补齐历史
// 的高水位游标。
func (o *Outbound) Attach(ctx context.Context, params wire.SessionAttachParams) (wire.SessionAttachResult, error) {
	response, err := protorpc.CallMethod(ctx, o.c.Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_ATTACH), &agentrewire.SessionAttachRequest{SessionId: params.SessionID, PeerFingerprint: params.PeerFingerprint}, func() *agentrewire.SessionAttachResponse { return &agentrewire.SessionAttachResponse{} })
	if err != nil {
		var result wire.SessionAttachResult
		return result, err
	}
	return wire.SessionAttachResult{SessionID: response.SessionId, BackendType: response.BackendType, LifecycleState: response.LifecycleState, LatestSeq: response.LatestSeq}, nil
}

// Pull 拉一页游标之后的 journaled 历史（R19 / R7）。桌面端的历史不回收，因此
// OldestSeq 恒为第一条（空历史为 0），与 agentred 的回收语义区分。
func (o *Outbound) Pull(ctx context.Context, params wire.SessionPullParams) (wire.SessionPullResult, error) {
	response, err := protorpc.CallMethod(ctx, o.c.Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL), &agentrewire.SessionPullRequest{SessionId: params.SessionID, PeerFingerprint: params.PeerFingerprint, Cursor: params.Cursor, Limit: int32(params.Limit)}, func() *agentrewire.SessionPullResponse { return &agentrewire.SessionPullResponse{} })
	if err != nil {
		var result wire.SessionPullResult
		return result, err
	}
	result := wire.SessionPullResult{Cursor: response.Cursor, HasMore: response.HasMore, OldestSeq: response.OldestSeq}
	for _, e := range response.Notifications {
		protowire.SetNotificationSeq(e.Payload, e.Seq)
		method, value, x := protowire.ProtoNotificationToWire(e.Payload)
		if x != nil {
			return result, x
		}
		// 不在这里 marshal:Params 装帧本身,真正需要 JSON 的是再往前一步的 Wails
		// 边界,那一跳由 JournaledNotification.MarshalJSON 落出同样的形状。
		result.Notifications = append(result.Notifications, wire.JournaledNotification{Seq: e.Seq, Method: method, Params: value})
	}
	return result, nil
}

// RunFresh 在对端桌面端上新建一条会话并跑首轮（R18）。wire 契约要求 SessionID
// 为正占位（对端按「本机查无此会话」判定建新会话），真正要新建的是它：AgentSyncID
// 是账号级 Agent 标识、Cwd 是本端上报的该机器项目路径。对端建出真会话后把真实 id
// 放进 RunAck.SessionID 返回。FreshSession 恒置 true：即便对端落库里有同号旧上下文
// 也不许续，杜绝派活撞上挂账残留。
func (o *Outbound) RunFresh(ctx context.Context, params wire.RunParams) (wire.RunAck, error) {
	params.FreshSession = true
	request, err := protowire.RunRequestToProto(params)
	if err != nil {
		return wire.RunAck{}, err
	}
	response, err := protorpc.CallMethod(ctx, o.c.Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN), request, func() *agentrewire.RuntimeRunResponse { return &agentrewire.RuntimeRunResponse{} })
	if err != nil {
		var ack wire.RunAck
		return ack, err
	}
	return wire.RunAck{SessionID: response.SessionId, ProviderSessionID: response.ProviderSessionId, LaunchPermissionMode: response.LaunchPermissionMode, ProviderFallbackKey: response.ProviderFallbackKey}, nil
}

// Steer 往已接入的远程会话发一条新消息（R19 / R9），走对端既有发送路径。
func (o *Outbound) Steer(ctx context.Context, params wire.SteerParams) error {
	_, err := protorpc.CallMethod(ctx, o.c.Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STEER), &agentrewire.RuntimeSteerRequest{SessionId: params.SessionID, PeerFingerprint: params.PeerFingerprint, QueuedId: params.QueuedID, Text: params.Text}, func() *agentrewire.Empty { return &agentrewire.Empty{} })
	return err
}

// SubmitAnswer 回答对端会话上挂起的用户提问（R10）。AlreadyHandled 报告同一待决策
// 已被别的端处理过；旧对端返回空对象时保持 false（task 5 的兼容语义）。
func (o *Outbound) SubmitAnswer(ctx context.Context, params wire.SubmitAnswerParams) (wire.PeerSessionControlResult, error) {
	response, err := protorpc.CallMethod(ctx, o.c.Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_ANSWER), &agentrewire.RuntimeSubmitAnswerRequest{SessionId: params.SessionID, PeerFingerprint: params.PeerFingerprint, RequestId: params.RequestID, Questions: protowire.AskQuestionsToProto(params.Questions), Answers: protowire.AskAnswersToProto(params.Answers), Skipped: params.Skipped}, func() *agentrewire.PeerSessionControlResponse { return &agentrewire.PeerSessionControlResponse{} })
	if err != nil {
		var result wire.PeerSessionControlResult
		return result, err
	}
	return wire.PeerSessionControlResult{AlreadyHandled: response.AlreadyHandled}, nil
}

// SubmitToolPermission 决定对端会话上挂起的工具权限（R10），AlreadyHandled 语义
// 同 SubmitAnswer。
func (o *Outbound) SubmitToolPermission(ctx context.Context, params wire.SubmitToolPermissionParams) (wire.PeerSessionControlResult, error) {
	response, err := protorpc.CallMethod(ctx, o.c.Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_TOOL_PERMISSION), &agentrewire.RuntimeSubmitToolPermissionRequest{SessionId: params.SessionID, PeerFingerprint: params.PeerFingerprint, RequestId: params.RequestID, Allow: params.Allow, AlwaysAllowSession: params.AlwaysAllowSession, DenyReason: params.DenyReason}, func() *agentrewire.PeerSessionControlResponse { return &agentrewire.PeerSessionControlResponse{} })
	if err != nil {
		var result wire.PeerSessionControlResult
		return result, err
	}
	return wire.PeerSessionControlResult{AlreadyHandled: response.AlreadyHandled}, nil
}

// HandleEvent 注册 runtime.event 通知订阅：对端把 attached 会话的 canonical 事件
// 帧推回本连接时，每帧解成 wire.EventFrame 交给 fn。每条连接只注册一次；返回值
// 错误会以 RPC 应答错误形式回给对端（对端忽略通知应答）。
func (o *Outbound) HandleEvent(fn func(wire.EventFrame) error) {
	o.c.Conn().Registry().SubscribeNotification(func(_ context.Context, notification *agentrewire.RpcNotification) error {
		// 直接转换手上这条已经解好的通知。
		//
		// 不要退回「proto.Marshal 成 RpcFrame 再 UnmarshalEventNotification」那条:
		// 它把上一层栈刚解完的消息重新序列化再反序列化一遍,只为复用一个按字节切入的
		// helper。这里跑的是**每一个 token**。实测(M1,-benchmem):
		//   TextDelta        1041 ns / 413 B / 12 allocs → 43 ns / 48 B / 2 allocs
		//   64KB ToolResult  16.3 µs / 139.8 KB / 13 allocs → 57 ns / 128 B / 2 allocs
		// 大载荷差 285 倍,是因为那条路径会把整个载荷在堆上多拷两遍。
		// internal/daemon/notifier/protobuf.go 早就写明了同一件事:推的是已经转好的
		// 那条消息,不重新转换。
		method, value, err := protowire.ProtoNotificationToWire(notification)
		if err != nil {
			return err
		}
		// 只要用户轮的事件帧。自主续轮的增量不进 Peer 转录(它在对端也不显示),
		// 终态帧 / Started 走的是别的出口。
		if method != wire.NotifyEvent {
			return nil
		}
		frame, ok := value.(*wire.EventFrame)
		if !ok || frame == nil {
			return nil
		}
		return fn(*frame)
	})
}

package handlers

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// InjectSubagentEvents 把桌面端镜像来的 `agrctl send --wait` 子会话事件写进 agentred 自己的
// 父转录(见 agentrewire.RuntimeInjectSubagentEventsRequest):
//
//   - 父轮还在跑:经 t.mu 写进在途那一轮(同审批卡的路径),落库后推持久帧,每条事件另推一
//     条预览帧;
//   - 父轮已结束:写回那条原消息(finishedCards.Inject),只发变了的持久帧;
//   - toolCallID 找不到(卡不在这条对话里 / 没接存储):空操作,照样成功 —— 桌面端不能因为
//     这张卡找不到就让子会话失败。
//
// 幂等:嵌套步骤按 ID 在 handler 层去重,同一批事件投递几次结果一样。
func (h *RuntimeHandlers) InjectSubagentEvents(ctx context.Context, req *agentrewire.RuntimeInjectSubagentEventsRequest) (*agentrewire.Empty, error) {
	conversationID, toolCallID := req.GetConversationId(), req.GetToolCallId()
	if err := ErrInvalidConversationID(conversationID); err != nil {
		return nil, err
	}
	peer, err := ResolveSessionPeer(ctx, devicefp.Initiator(req.GetPeerFingerprint()), h.deps.LoggedInAccountID)
	if err != nil {
		return nil, err
	}
	events, err := injectableEvents(toolCallID, req.GetEvents())
	if err != nil {
		return nil, err
	}
	injector := SubagentCardInjector{
		NotifyFor: h.deps.NotifyFor, Ctl: h.deps.Ctl, Transcript: h.deps.Transcript, Cards: h.deps.SubagentCards,
	}
	if err := injector.Inject(ctx, peer, conversationID, toolCallID, events); err != nil {
		return nil, err
	}
	return &agentrewire.Empty{}, nil
}

// SubagentCardInjector 把注入方事件写进 agentred 自己的父转录(Daemon 级,不随连接生灭):
// 桌面端经 InjectSubagentEvents 推来的子会话事件走它,ctl 代理在承载 send 的连接断开时
// 给卡收尾(ctlProxy.settleDroppedSend)也走它 —— 「在途那一轮 / 原消息」的分流只有这一份。
type SubagentCardInjector struct {
	NotifyFor  func(peerFingerprint devicefp.Initiator) NotifierPort
	Ctl        *CtlSessions
	Transcript TranscriptPort
	// Cards nil = 不接已收口消息那一格:父轮已结束时这批事件丢弃。
	Cards SubagentCardPort
}

// Inject 把一批事件写给 toolCallID 那张卡:父轮还在跑就经 t.mu 写进在途那一轮并推预览帧,
// 否则写回原消息(finishedCards.Inject),只发变了的持久帧;找不到卡时空操作。
func (j SubagentCardInjector) Inject(
	ctx context.Context, peer devicefp.Initiator, conversationID, toolCallID string, events []agentruntime.Event,
) error {
	em := newSessionEmitter(ctx, j.NotifyFor, conversationID, peer)
	if toolCallID == "" || len(events) == 0 {
		logger.Ctx(em.ctx).Debug("handlers.SubagentCardInjector.Inject: nothing to inject",
			zap.String("conversationId", conversationID), zap.String("toolUseId", toolCallID))
		return nil
	}

	if j.Ctl.injectLive(em.ctx, em.rid, toolCallID, events) {
		for _, ev := range events {
			em.emit(wire.NotifyEvent, &wire.EventFrame{ConversationID: conversationID, Event: ev, Preview: true})
		}
		return nil
	}

	if j.Cards == nil || j.Transcript == nil {
		logger.Ctx(em.ctx).Debug("handlers.SubagentCardInjector.Inject: no card store; ignored",
			zap.String("conversationId", conversationID), zap.String("toolUseId", toolCallID))
		return nil
	}
	cards := &finishedCards{
		finder: j.Cards, port: j.Transcript, conversationID: conversationID,
		publish: func(frame wire.EventFrame) { em.emit(wire.NotifyEvent, &frame) },
	}
	found, err := cards.Inject(em.ctx, toolCallID, events)
	if err != nil {
		return err
	}
	if !found {
		logger.Ctx(em.ctx).Info("handlers.SubagentCardInjector.Inject: card not found; ignored",
			zap.String("conversationId", conversationID), zap.String("toolUseId", toolCallID))
	}
	return nil
}

// injectableEvents 解出请求里的事件,并只留注入方有权写的那几类:点名 toolCallID 的
// agrctl Subagent 帧(Started / Progress / Done)、点名 toolCallID 的子会话模型帧
// (SubagentModel 不带归属,只认点名的那条调用),以及挂在 toolCallID 下的嵌套
// ToolCall / ToolResult。别的一律丢弃 —— 这个口子写的是父转录,不是任意事件的入口。
func injectableEvents(toolCallID string, frames []*agentrewire.RuntimeEventNotification) ([]agentruntime.Event, error) {
	out := make([]agentruntime.Event, 0, len(frames))
	for _, frame := range frames {
		raw, err := proto.Marshal(&agentrewire.RpcFrame{Body: &agentrewire.RpcFrame_Notification{
			Notification: &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_RuntimeEvent{RuntimeEvent: frame}},
		}})
		if err != nil {
			return nil, err
		}
		decoded, _, err := protowire.UnmarshalEventNotification(raw)
		if err != nil {
			return nil, err
		}
		if injectable(toolCallID, decoded.Event) {
			out = append(out, decoded.Event)
		}
	}
	return out, nil
}

func injectable(toolCallID string, ev agentruntime.Event) bool {
	switch e := ev.(type) {
	case agentruntime.SubagentStarted:
		return e.ToolCallID == toolCallID && e.Info.Origin == agentruntime.SubagentOriginAgrctl
	case agentruntime.SubagentProgress:
		return e.ToolCallID == toolCallID && e.Info.Origin == agentruntime.SubagentOriginAgrctl
	case agentruntime.SubagentDone:
		return e.ToolCallID == toolCallID && e.Info.Origin == agentruntime.SubagentOriginAgrctl
	case agentruntime.SubagentModel:
		return e.ToolCallID == toolCallID
	case agentruntime.ToolCall:
		return e.ParentToolCallID == toolCallID
	case agentruntime.ToolResult:
		return e.ParentToolCallID == toolCallID
	}
	return false
}

// eventInjector 是在途那一轮接收外部注入事件的口子(*turnTranscript)。
type eventInjector interface {
	injectSubagentEvents(ctx context.Context, toolCallID string, events []agentruntime.Event) bool
}

// injectLive 把一批事件交给 rid 这条会话此刻在跑的那一轮;没有在跑的轮、或那一轮里没有
// toolCallID 那条外层调用时交回 false。
func (c *CtlSessions) injectLive(ctx context.Context, rid int64, toolCallID string, events []agentruntime.Event) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	var sink approvalSink
	if s := c.sessions[rid]; s != nil {
		sink = s.turn
	}
	c.mu.Unlock()
	injector, ok := sink.(eventInjector)
	return ok && injector.injectSubagentEvents(ctx, toolCallID, events)
}

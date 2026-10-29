package ctl_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// SubagentCards 是 serveSend 对「子代理卡」的窄依赖(ISP,chat_svc 实现):把
// `agrctl send --wait` 建出的子会话绑到调用方会话当前轮里发出这次 send 的那条 shell
// 调用上,原地升级成子代理卡(spec 2026-10-27「Binding」「Card lifecycle」)。
//
// 刻意不并进 ChatGateway:那是 subagent_svc 也在用的 chat_svc.HeadlessRunnerGateway
// 别名,拓宽它会让另一个消费方平白背上这项能力。
type SubagentCards interface {
	// WatchCtlSend 在子会话起轮**之前**登记它:此后子会话的运行时事件先攒着,绑定成功
	// 后补投,起轮第一时间的步骤不会漏。返回的句柄必须 Close。
	WatchCtlSend(childSessionID int64) chat_svc.CtlSendWatch
}

// ChatSubagentCards 生产用子代理卡端口(供 app 接线,须在 RegisterChat 之后)。
func ChatSubagentCards() SubagentCards { return chat_svc.CtlSendSvcGateway() }

// RegisterSubagentCards 装上子代理卡端口;没装时 send 照常执行,只是不升级卡片。
func (s *ctlSvc) RegisterSubagentCards(c SubagentCards) {
	s.mu.Lock()
	s.cards = c
	s.mu.Unlock()
}

// HostBindToolCallHeader / HostBindBackgroundHeader 是 agentred 的 ctl 代理转来 send 时附上的
// 绑定结果:它在自己的父转录里匹配到的 shell 调用 ID 与是否后台(spec 2026-10-27「agrctl send
// 子代理卡」Remote sessions 第 1 条)。与 agentred handlers 的同名常量同值;代理转发前去掉
// 调用方自带的同名头,桌面端也只对在 agentred 上执行的父会话采信它们(chat_svc.bindCtlSend)。
const (
	HostBindToolCallHeader   = "Agentre-Ctl-Bind-Tool-Call"
	HostBindBackgroundHeader = "Agentre-Ctl-Bind-Background"
)

// 不绑定的原因(spec「Binding」规则 3),只进 info 日志。
const (
	notBoundHandshake       = "handshake_token"
	notBoundNoWait          = "no_wait"
	notBoundExistingSession = "existing_session"
	notBoundUnavailable     = "unavailable"
)

// watchSubagentCard 判定这次 send 是否该绑卡,该绑就在起轮前登记子会话;否则记一条 info
// 日志说明原因,交回什么都不做的句柄 —— 调用方的流程因此与今天逐字相同。
//
// 前提:会话 token(才知道是哪个会话发起的)、--wait(父 agent 在等结果,卡片的生命
// 周期才有锚点)、新建的子会话(续写既有会话没有「派给哪个 agent」可匹配)。
func (h *ctlHandler) watchSubagentCard(
	ctx context.Context, cred credential, req sendRequest, childSessionID int64,
) chat_svc.CtlSendWatch {
	reason := ""
	switch {
	case !cred.session:
		reason = notBoundHandshake
	case !req.Wait:
		reason = notBoundNoWait
	case req.SessionID > 0:
		reason = notBoundExistingSession
	case h.cards == nil:
		reason = notBoundUnavailable
	}
	if reason != "" {
		logger.Ctx(ctx).Info("ctl_svc.serveSend: subagent card not bound",
			zap.String("reason", reason),
			zap.Int64("callerSessionId", cred.ref.SessionID),
			zap.Int64("sessionId", childSessionID))
		return noSubagentCard{}
	}
	return h.cards.WatchCtlSend(childSessionID)
}

// noSubagentCard 是不绑卡时的句柄:所有操作都是空操作。
type noSubagentCard struct{}

func (noSubagentCard) Bind(context.Context, chat_svc.CtlSendBindRequest) bool { return false }
func (noSubagentCard) Finish(context.Context, chat_svc.CtlSendOutcome)        {}
func (noSubagentCard) Close()                                                 {}

package peer_svc

import (
	"context"
	"errors"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// RunRequest 是「在对端一条**已经存在**的会话上起新一轮」的入参。
//
// 它与 RunFreshRequest 的分工就是那个号从哪来：RunFreshRequest 不带对话号（本端
// 现铸一个，对端据此新建会话，因此还要 agentId / projectId 去解析后端与 cwd），
// 这里的对话号是对端**已有**的那一条 —— agent、cwd、后端都在对端那行会话上写着，
// 本机不该也不能替它重新解析一遍。
type RunRequest struct {
	Fingerprint    devicefp.Carrier `json:"fingerprint"`
	ConversationID string           `json:"conversationId"`
	UserText       string           `json:"text"`
}

// Run 在对端一条已接入的会话上起新一轮（Peer Tab 的「空闲会话上发消息」）。
//
// 为什么不是 Steer：插话要有**正在进行的轮次**才插得进去，对端对空闲会话一律回
// ErrNoActiveTurn。用户不该关心「这条会话此刻在不在跑」—— 控制台就是按会话状态在
// run / steer 之间分流的，桌面端这一侧此前只有 steer 那半边。
//
// 走常驻中继连接（而不是 RunFresh 那样的短连接）：这条会话已经 Attach 在这条连接
// 上，这一轮跑出来的事件正是从它推回来的。
func (s *service) Run(ctx context.Context, req RunRequest) (wire.RunAck, error) {
	if req.Fingerprint == "" {
		return wire.RunAck{}, errors.New("peer_svc.Run: empty fingerprint")
	}
	if req.ConversationID == "" {
		return wire.RunAck{}, errors.New("peer_svc.Run: empty conversation id")
	}
	e, err := s.ensureConn(ctx, req.Fingerprint)
	if err != nil {
		return wire.RunAck{}, err
	}
	fp, err := s.selfFingerprint()
	if err != nil {
		return wire.RunAck{}, err
	}
	return e.out.Run(ctx, wire.RunParams{
		ConversationID: req.ConversationID,
		UserText:       req.UserText,
		// 与 RunFresh 同一条规矩：本机的承载者身份在这里换角色 —— 这一轮由本机
		// **发起**、交给对端执行，对端把它记成会话上这条用户消息的来源。
		SourceDevice: devicefp.Initiator(fp),
	})
}

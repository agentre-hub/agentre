package chat_svc

import (
	"context"
	"strings"

	"github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

// messageText 拼接某消息内全部 TextBlock 的文本(value/pointer 两种形态都收)。
func messageText(m *chat_entity.Message) (string, error) {
	if m == nil {
		return "", nil
	}
	bs, err := m.GetBlocks()
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, b := range bs {
		switch t := b.(type) {
		case blocks.TextBlock:
			sb.WriteString(t.Text)
		case *blocks.TextBlock:
			sb.WriteString(t.Text)
		}
	}
	return sb.String(), nil
}

// FinalAssistantText 读取某 assistant message 的纯文本。子 agent 工具用它把子 agent
// 最终回复回灌给调用方(TurnResult 只带 message id, 不含文本)。
func (s *chatSvc) FinalAssistantText(ctx context.Context, messageID int64) (string, error) {
	if messageID <= 0 {
		return "", nil
	}
	msg, err := chat_repo.Message().Find(ctx, messageID)
	if err != nil {
		return "", err
	}
	return messageText(msg)
}

// LatestAssistantText 取某会话「最近一条」assistant 文本(running 任务 peek 用;
// 无 assistant 消息 → 空串)。与 FinalAssistantText(按 messageID,已收尾)互补:
// 这里按 sessionID 取末条,不要求轮已完成。
func (s *chatSvc) LatestAssistantText(ctx context.Context, sessionID int64) (string, error) {
	if sessionID <= 0 {
		return "", nil
	}
	msg, err := chat_repo.Message().LatestAssistant(ctx, sessionID)
	if err != nil {
		return "", err
	}
	return messageText(msg)
}

// SessionProjectID 返回某会话所属的 project id(0=未挂项目)。子 agent 工具用它让一次性
// 子会话继承调用方会话的项目/工作目录(子 agent 因此能在调用方的项目里干活)。
func (s *chatSvc) SessionProjectID(ctx context.Context, sessionID int64) (int64, error) {
	if sessionID <= 0 {
		return 0, nil
	}
	sess, err := chat_repo.Session().Find(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	if sess == nil {
		return 0, nil
	}
	return sess.ProjectID, nil
}

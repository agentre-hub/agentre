package terminal_svc

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// 会话终端(对话页的终端工具窗)归属于一条对话:对话被删除时由后端关掉它开的全部终端,
// 不依赖某一个前端删除入口记得去关(批量清理、入站删除都不经过那个入口)。

// OpenForChatSession 同 Open,并记下这个终端属于对话 chatSessionID。开启失败不留归属。
func (s *Service) OpenForChatSession(ctx context.Context, chatSessionID int64, terminalID, deviceID, cwd string, cols, rows uint16) error {
	s.mu.Lock()
	if s.chatOwners == nil {
		s.chatOwners = map[string]int64{}
	}
	s.chatOwners[terminalID] = chatSessionID
	s.mu.Unlock()
	if err := s.Open(ctx, terminalID, deviceID, cwd, cols, rows); err != nil {
		s.mu.Lock()
		if s.chatOwners[terminalID] == chatSessionID {
			delete(s.chatOwners, terminalID)
		}
		s.mu.Unlock()
		return err
	}
	return nil
}

// CloseChatSession 关掉对话 chatSessionID 开的全部终端(对话被删除)。已经结束的终端跳过;
// 关不掉的只记日志 —— 删除本身已经成立,不因一个终端失败回滚。
func (s *Service) CloseChatSession(ctx context.Context, chatSessionID int64) {
	s.mu.Lock()
	var ids []string
	for id, owner := range s.chatOwners {
		if owner == chatSessionID {
			ids = append(ids, id)
			delete(s.chatOwners, id)
		}
	}
	s.mu.Unlock()
	for _, id := range ids {
		if err := s.Close(ctx, id); err != nil && !errors.Is(err, ErrTerminalNotOpen) {
			logger.Ctx(ctx).Warn("terminal_svc.CloseChatSession: close terminal failed",
				zap.Int64("chatSessionId", chatSessionID), zap.String("terminalId", id), zap.Error(err))
		}
	}
}

// forgetChatOwnerLocked 在终端被用户关掉时清掉它的归属。调用方持有 s.mu。
func (s *Service) forgetChatOwnerLocked(terminalID string) {
	delete(s.chatOwners, terminalID)
}

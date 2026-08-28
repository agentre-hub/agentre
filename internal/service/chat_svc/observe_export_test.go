package chat_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
)

// PublishTurnResultForTest 仅供单测直接驱动 publish。
func (s *chatSvc) PublishTurnResultForTest(sessionID int64, r TurnResult) {
	s.publishTurnResult(sessionID, r)
}

// NewChatForTest 暴露具体类型供单测驱动内部方法。
func NewChatForTest(e Emitter) *chatSvc { return NewChat(e).(*chatSvc) }

// FailTurnForTest 仅供单测验证 failTurn 的 publish 不变量。
// 参数顺序对齐真实 failTurn(ctx, sess, msg, stream, err)。
func (s *chatSvc) FailTurnForTest(ctx context.Context, sessID, msgID int64, stream string, err error) {
	s.failTurn(ctx, &chat_entity.Session{ID: sessID}, &chat_entity.Message{ID: msgID}, stream, err)
}

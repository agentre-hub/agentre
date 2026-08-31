package handlers

import (
	"fmt"

	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
)

// ErrInvalidConversationID 是 RPC 边界上「线上给来的不是一条对话身份」的判据。
//
// 它换掉了从前散在 7 处的 `sessionID <= 0`:会话号曾经是个正整数,合法性判据就是
// "> 0";对话身份是 uuid,判据换成格式校验。给的是 CodeInvalidParams —— 请求参数
// 不合法,与「这条对话不在本机」(CodeSessionMissing)是两回事,客户端要分得开。
func ErrInvalidConversationID(conversationID string) error {
	if err := conversationid.Validate(conversationID); err != nil {
		return &rpcerror.Error{
			Code:    rpcerror.CodeInvalidParams,
			Message: fmt.Sprintf("invalid conversation id: %v", err),
		}
	}
	return nil
}

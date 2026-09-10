package chat_svc

import (
	"context"

	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/svcerr"
)

// chatErrors 是本包的错误报告口。CallerSkip=1 抵消下面那层薄 wrapper,
// 让日志的 caller 字段仍然指向真正的业务调用点。
var chatErrors = svcerr.Reporter{LogMessage: "chat_svc: operation failed", CallerSkip: 1}

// operationFailedWithCause 把通用的 OperationFailed 与真实 cause 绑在一起:
// cause 既进日志(供事后排查),也随 Error() 透到前端(供当场排查)。
// fields 用于带上调用点独有的排查字段(sessionId / agentId / …),让调用点无需自己再记一行。
func operationFailedWithCause(ctx context.Context, cause error, fields ...zap.Field) error {
	return chatErrors.OperationFailedWithCause(ctx, cause, fields...)
}

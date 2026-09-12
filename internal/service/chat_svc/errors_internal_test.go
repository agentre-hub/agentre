package chat_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// cause 存在时:Error() = 本地化 headline + 换行 + 原始 cause,让 Wails 边界能把详情带给前端。
func TestOperationFailedWithCause_ErrorCarriesCause(t *testing.T) {
	cause := errors.New("SQL logic error: table chat_sessions has no column named run_id (1)")

	err := operationFailedWithCause(context.Background(), cause)

	assert.Equal(t,
		"操作失败\nSQL logic error: table chat_sessions has no column named run_id (1)",
		err.Error())
}

// cause 为 nil 时退化成原来的通用错误,行为与改动前完全一致。
func TestOperationFailedWithCause_NilCauseDegrades(t *testing.T) {
	err := operationFailedWithCause(context.Background(), nil)

	assert.Equal(t, "操作失败", err.Error())
}

// 契约测试:errors.Is 能穿透到 cause。
// 注:原消费者 orch_svc 已随编排移除删除,这里锁的是 Go 错误包装的通用契约,不是回归护栏。
func TestOperationFailedWithCause_UnwrapsToCause(t *testing.T) {
	sentinel := errors.New("database is locked (5) (SQLITE_BUSY)")

	err := operationFailedWithCause(context.Background(), sentinel)

	assert.True(t, errors.Is(err, sentinel))
}

// 契约测试:errors.As 仍能取出 httputils.Error 且 Code 保持 OperationFailed。
func TestOperationFailedWithCause_AsHTTPError(t *testing.T) {
	err := operationFailedWithCause(context.Background(), errors.New("boom"))

	var httpErr *httputils.Error
	assert.True(t, errors.As(err, &httpErr))
	assert.Equal(t, code.OperationFailed, httpErr.Code)
}

// cause 存在时:恰好记一行 Error 级别日志,message 为固定串。
// 曾经翻过车——某调用点残留手写 logger,导致同一错误被记两遍,靠人眼复审才抓到;
// 这条测试就是补那道护栏。
func TestOperationFailedWithCause_LogsExactlyOneEntry(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))

	_ = operationFailedWithCause(ctx, errors.New("boom"))

	assert.Equal(t, 1, logs.Len())
	entry := logs.All()[0]
	assert.Equal(t, zapcore.ErrorLevel, entry.Level)
	assert.Equal(t, "chat_svc: operation failed", entry.Message)
}

// 传入的 fields 原样带入这一行日志,且同一行还带着 cause(error 字段)。
func TestOperationFailedWithCause_LogsFieldsAndCause(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))
	cause := errors.New("boom")

	_ = operationFailedWithCause(ctx, cause, zap.Int64("sessionId", 7))

	assert.Equal(t, 1, logs.Len())
	fields := logs.All()[0].ContextMap()
	assert.Equal(t, int64(7), fields["sessionId"])
	assert.Equal(t, cause.Error(), fields["error"])
}

// cause 为 nil 时不应该记任何日志——退化路径连日志都不产生。
func TestOperationFailedWithCause_NilCauseLogsNothing(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))

	_ = operationFailedWithCause(ctx, nil)

	assert.Equal(t, 0, logs.Len())
}

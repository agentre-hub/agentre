// Package svcerr 提供 service 层共用的「通用失败 + 真实 cause」错误形状。
//
// 它是横切叶子(internal/pkg),只依赖 cago 的 i18n / logger 与 internal/pkg/code,
// 不 import 任何 service / repository。多个 service 包需要**同一个**错误形状时
// (Wails 边界只过 Error() 字符串,cause 必须被拼进那条字符串,且恰好记一行日志),
// 各写一份会慢慢漂移,故收在这里。
package svcerr

import (
	"context"
	"net/http"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/code"
)

type localizedCauseError struct {
	httpErr *httputils.Error
	cause   error
}

// Error 返回「本地化 headline + 换行 + 原始 cause」。
// Wails 过界只取 Error() 字符串,这是 cause 能到达前端的唯一通道;前端按首个换行拆成 headline/detail。
func (e *localizedCauseError) Error() string {
	if e.cause == nil {
		return e.httpErr.Msg
	}
	return e.httpErr.Msg + "\n" + e.cause.Error()
}

func (e *localizedCauseError) Unwrap() error {
	return e.cause
}

func (e *localizedCauseError) As(target any) bool {
	if p, ok := target.(**httputils.Error); ok {
		*p = e.httpErr
		return true
	}
	return false
}

// Reporter 是某个 service 包的错误报告口。LogMessage 是该包固定的日志 message
// (docs/observability.md 的 "package: 事件" 前缀);CallerSkip 让包内再包一层薄
// wrapper 的调用方把 caller 字段指回**真正的业务调用点**,而不是那层 wrapper。
type Reporter struct {
	LogMessage string
	CallerSkip int
}

// OperationFailedWithCause 把通用的 OperationFailed 与真实 cause 绑在一起:
// cause 既进日志(供事后排查),也随 Error() 透到前端(供当场排查)。
// fields 用于带上调用点独有的排查字段(sessionId / agentId / …),让调用点无需自己再记一行。
//
// 日志 message 用固定串:helper 内拿不到调用方方法名,精确位置由 AddCallerSkip 产生的
// caller 字段(file:line)给出 —— 那本就是 docs/debugging.md 指定的最快过滤维度。
func (r Reporter) OperationFailedWithCause(ctx context.Context, cause error, fields ...zap.Field) error {
	if cause == nil {
		return i18n.NewError(ctx, code.OperationFailed)
	}
	logger.Ctx(ctx).WithOptions(zap.AddCallerSkip(1+r.CallerSkip)).
		Error(r.LogMessage, append(fields, zap.Error(cause))...)
	return &localizedCauseError{
		httpErr: &httputils.Error{
			Status: http.StatusBadRequest,
			Code:   code.OperationFailed,
			Msg:    i18n.T(ctx, code.OperationFailed),
		},
		cause: cause,
	}
}

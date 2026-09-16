// Package protorpclog 把共享协议引擎 pkg/wire/protorpc 的 slog 出口接到本仓的
// cago logger 上。
//
// 引擎住在共享 module 里(agentre-server 也 import 它),所以它只声明一个标准库的
// *slog.Logger 出口,由各宿主装配。本包就是本仓这一侧的装配,两个二进制(桌面 App
// 与 agentred)各自在配好 cago logger 的地方调一次 Install。
//
// 装配前引擎的日志是丢弃的,与 agentred 在 initLogging 之前用 no-op logger 的
// 既有行为一致。
package protorpclog

import (
	"context"
	"log/slog"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// Install 把 cago logger 装进协议引擎。可重复调用。
func Install() { protorpc.SetLogger(slog.New(cagoHandler{})) }

// cagoHandler 把 slog 记录转给 cago 的 ctx-aware logger:宿主的 logger 往往从 ctx
// 上取 trace / 请求标识,所以这里用 logger.Ctx(ctx) 而不是某一份固定实例。
type cagoHandler struct{}

func (cagoHandler) Enabled(context.Context, slog.Level) bool { return true }

func (cagoHandler) Handle(ctx context.Context, r slog.Record) error {
	fields := make([]zap.Field, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		fields = append(fields, zap.Any(a.Key, a.Value.Any()))
		return true
	})
	l := logger.Ctx(ctx)
	switch {
	case r.Level >= slog.LevelError:
		l.Error(r.Message, fields...)
	case r.Level >= slog.LevelWarn:
		l.Warn(r.Message, fields...)
	default:
		l.Debug(r.Message, fields...)
	}
	return nil
}

func (cagoHandler) WithAttrs([]slog.Attr) slog.Handler { return cagoHandler{} }
func (cagoHandler) WithGroup(string) slog.Handler      { return cagoHandler{} }

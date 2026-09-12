// Package protorpclog 把共享协议引擎 pkg/wire/protorpc 的诊断出口接到本仓的
// cago logger 上。
//
// 引擎住在共享 module 里(agentre-server 也 import 它),所以它不能直接依赖 cago:
// 它只声明一个 protorpc.Logger 出口,由各宿主装配。本包就是本仓这一侧的装配,两个
// 二进制(桌面 App 与 agentred)各自在配好 cago logger 的地方调一次 Install。
//
// 装配前引擎的日志是丢弃的,与 agentred 在 initLogging 之前用 no-op logger 的
// 既有行为一致。
package protorpclog

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// Install 把 cago logger 装进协议引擎。可重复调用。
func Install() { protorpc.SetLogger(cagoLogger{}) }

type cagoLogger struct{}

func (cagoLogger) Debug(ctx context.Context, msg string, fields ...protorpc.Field) {
	logger.Ctx(ctx).Debug(msg, zapFields(fields)...)
}

func (cagoLogger) Warn(ctx context.Context, msg string, fields ...protorpc.Field) {
	logger.Ctx(ctx).Warn(msg, zapFields(fields)...)
}

func (cagoLogger) Error(ctx context.Context, msg string, fields ...protorpc.Field) {
	logger.Ctx(ctx).Error(msg, zapFields(fields)...)
}

// zapFields 逐个转成 zap 字段。zap.Any 对 error 会走 NamedError,所以 protorpc.Err
// 落下来仍然是常规的 error 字段,不必在这里特判。
func zapFields(fields []protorpc.Field) []zap.Field {
	out := make([]zap.Field, 0, len(fields))
	for _, field := range fields {
		out = append(out, zap.Any(field.Key, field.Value))
	}
	return out
}

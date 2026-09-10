// Package peerstream 持有「把本机会话交给已鉴权的账号对端」这一套:只读的会话清单/
// 计数投影,以及每条会话那一份**唯一有序的通知宇宙**(历史帧 + 订阅者 + 编号台账)。
//
// 它从 chat_svc 拆出来的判据与 remotepool 一样是自足性:整套状态就是一张
// sessionID → *publication 的表,对外只认 (会话, 订阅者) 这一组键,不认识 turn 队列、
// activeCancels、前端 Wails 流。宿主(chat_svc)在轮里单向调 PublishEvent /
// PublishMessageFrames / PublishTurnDone 把帧交进来,本包不回调宿主。
//
// 生命周期不变量(每一条都对应过一次真实事故,改前先读 publication / flushLoop 的注释):
//   - 一条会话只有一份通知宇宙,持久转录做前缀、实时帧接在后面,重连共用同一套去重编号;
//   - 投递由每条 publication 一条 worker goroutine 串行化,绝不在轮的事件循环里内联
//     阻塞 Notify;
//   - 每个订阅者一条有界队列,写不动的对端只丢自己的帧,靠 seq 跳号自己发起补齐。
//
// Publisher 由 chat_svc 惰性构造并持有一份(不是进程单例):通知宇宙的寿命跟的是
// 那个 chatSvc 实例,与拆分前逐字一致。
package peerstream

import (
	"context"
	"sync"

	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/svcerr"
)

// Publisher 是一台桌面端对账号对端的会话出口。零值可用;chat_svc 用 New 构造。
type Publisher struct {
	// peerPublications keeps remote account peers in a separate ordered stream,
	// so attachment adds presence without replacing the desktop Wails emitter.
	peerPublications sync.Map
}

// New 构造一个空的出口。
func New() *Publisher { return &Publisher{} }

// peerstreamErrors 是本包的错误报告口。CallerSkip=1 抵消下面那层薄 wrapper,
// 让日志的 caller 字段仍然指向真正的业务调用点。
var peerstreamErrors = svcerr.Reporter{LogMessage: "chat_svc.peerstream: operation failed", CallerSkip: 1}

func operationFailedWithCause(ctx context.Context, cause error, fields ...zap.Field) error {
	return peerstreamErrors.OperationFailedWithCause(ctx, cause, fields...)
}

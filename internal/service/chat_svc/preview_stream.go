package chat_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// preview_stream.go 是远端执行那一路的**预览帧**入口。
//
// 规格 2026-09-05「两级帧与补齐」把两级帧的分工落成三条可观察行为,消费方占其中两条:
// 预览帧只用于即时呈现、不得进转录;持久帧是转录与游标的唯一来源。*remote.Runtime
// 因此把预览帧从 Run 交回的那条事件流里摘了出去(那条流就是本轮的转录来源),改由这里
// 接住 —— 逐 token 的文本照旧实时长出来(硬不变量 2),而这一轮落库的正文只由宿主
// 实时发布的持久帧攒成(硬不变量 1:重连补齐因此只补对端真缺的那一段)。
//
// 两条流都在 turnRun 那一个 goroutine 上消费(见 turnRun.consumeEvents):handler 会
// 动 turnCtx 与前端流,两个 goroutine 同时进去就是一场数据竞争。

// previewQueueDepth 是每条会话的预览缓冲深度。满了就丢 —— 预览帧按规格「丢失即丢失、
// 不补」,它的内容此刻正被宿主 checkpoint 进块表,转录不依赖这条流。
const previewQueueDepth = 256

// registerPreviewStream 为这一轮开一条预览通道并登记到会话上;cancel 摘掉登记。
// 同一会话重复登记时后者生效(一条会话同一时刻只有一轮在跑)。
func (s *chatSvc) registerPreviewStream(sessionID int64) (<-chan agentruntime.Event, func()) {
	ch := make(chan agentruntime.Event, previewQueueDepth)
	s.previewStreams.Store(sessionID, ch)
	return ch, func() { s.previewStreams.Delete(sessionID) }
}

// onRemotePreviewEvent 是注入给 *remote.Runtime 的预览出口(remote.PreviewSink)。
//
// 它跑在 runtime 的读循环上,所以**永不阻塞**:那条循环同时还负责把 RPC 应答交回等待
// 方,在这里停一下停的是整条连接(理由与 remote.handleEvent 的 orderedpipe 同源)。
// 缓冲满即丢弃这一帧。
func (s *chatSvc) onRemotePreviewEvent(sessionID int64, ev agentruntime.Event) {
	value, ok := s.previewStreams.Load(sessionID)
	if !ok {
		return
	}
	select {
	case value.(chan agentruntime.Event) <- ev:
	default:
		logger.Default().Debug("chat_svc.onRemotePreviewEvent: preview dropped, queue full",
			zap.Int64("sessionId", sessionID))
	}
}

// applyPreview 把一条预览帧呈现出去:走的是与本机 runtime **同一条**实时路径
// (applyLive),只是累积进一个用完即弃的累加器 —— 呈现要的那点上下文(比如工具卡
// 要认回自己那条 tool_use)在它里面攒着,而这一轮真正落库的正文只由持久帧攒成。
func (t *turnRun) applyPreview(ctx context.Context, ev agentruntime.Event) {
	if ev == nil {
		return
	}
	t.applyLive(ctx, ev, true)
}

// discardEmitter 是持久帧那一路的发射器空位:它只累积,不呈现(呈现归预览帧)。
type discardEmitter struct{}

func (discardEmitter) Emit(context.Context, string, any) {}

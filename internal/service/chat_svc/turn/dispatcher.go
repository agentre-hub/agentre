package turn

import (
	"context"
	"reflect"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// Dispatcher 按 agentruntime.Event 具体类型路由到注册的 Handler。
// 未注册的 Event 默默丢弃(forward-compat,允许 runtime 上线新 Event 类型时
// chat_svc 还没接也不崩)。
type Dispatcher struct {
	handlers map[reflect.Type]Handler
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{handlers: map[reflect.Type]Handler{}}
}

// Register 把 Handler 绑定到一个 Event 类型;sample 传 typed-nil 指针即可,例:
//
//	d.Register((*agentruntime.TextDelta)(nil), handlers.TextDeltaHandler{})
//
// 内部用 reflect.TypeOf().Elem() 取出值类型作为 key。
func (d *Dispatcher) Register(sample agentruntime.Event, h Handler) {
	t := reflect.TypeOf(sample)
	if t == nil {
		return
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	d.handlers[t] = h
}

// Apply 把 ev 路由到对应 handler。ev=nil / 未注册 → no-op 返 nil。
func (d *Dispatcher) Apply(ctx context.Context, ev agentruntime.Event, acc *Accumulator, emit Emitter, view View, turnCtx *TurnContext) error {
	if ev == nil {
		return nil
	}
	t := reflect.TypeOf(ev)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	h, ok := d.handlers[t]
	if !ok {
		return nil
	}
	return h.Apply(ctx, ev, acc, emit, view, turnCtx)
}

package protorpc

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

type genericHandler func(context.Context, []byte) ([]byte, error)

func RegisterMethod[Req proto.Message, Resp proto.Message](
	registry *Registry,
	methodID uint32,
	newRequest func() Req,
	handler func(context.Context, Req) (Resp, error),
) {
	if methodID == 0 {
		panic("protorpc: method ID 0 is reserved")
	}
	registry.methodMu.Lock()
	defer registry.methodMu.Unlock()
	if _, exists := registry.methods[methodID]; exists {
		panic(fmt.Sprintf("protorpc: duplicate method ID %d", methodID))
	}
	registry.methods[methodID] = func(ctx context.Context, payload []byte) (_ []byte, err error) {
		// 守卫挂在这里而不是每个 handler 里:handler 跑在自己的 goroutine 上,
		// 漏一处就等于没有(见 recoverHandler)。
		defer recoverHandler(fmt.Sprintf("method %d", methodID), &err)
		request := newRequest()
		if err := proto.Unmarshal(payload, request); err != nil {
			return nil, &Error{Code: CodeInvalidParams, Message: err.Error()}
		}
		response, err := handler(ctx, request)
		if err != nil {
			return nil, err
		}
		return proto.Marshal(response)
	}
}

func (r *Registry) dispatchMethod(ctx context.Context, methodID uint32, payload []byte) ([]byte, error) {
	r.methodMu.RLock()
	handler, guard := r.methods[methodID], r.guard
	r.methodMu.RUnlock()
	if handler == nil {
		return nil, methodNotFound()
	}
	if guard != nil {
		guarded, err := runGuard(ctx, guard, methodID)
		if err != nil {
			return nil, err
		}
		ctx = guarded
	}
	return handler(ctx, payload)
}

// runGuard calls the guard under the same panic shield as a handler: it runs on
// the request goroutine, and a panic there must refuse the call, not the process.
func runGuard(ctx context.Context, guard MethodGuard, methodID uint32) (_ context.Context, err error) {
	defer recoverHandler(fmt.Sprintf("method guard %d", methodID), &err)
	return guard(ctx, methodID)
}

func CallMethod[Req proto.Message, Resp proto.Message](ctx context.Context, conn *Conn, methodID uint32, request Req, newResponse func() Resp) (Resp, error) {
	var zero Resp
	payload, err := proto.Marshal(request)
	if err != nil {
		return zero, err
	}
	frame, err := conn.call(ctx, &agentrewire.Request{MethodId: methodID, EncodedPayload: payload})
	if err != nil {
		return zero, err
	}
	if frame.GetMethodId() != methodID {
		return zero, ErrResponseType
	}
	response := newResponse()
	if err := proto.Unmarshal(frame.GetEncodedPayload(), response); err != nil {
		return zero, fmt.Errorf("%w: %v", ErrResponseType, err)
	}
	return response, nil
}

func CallMessage(ctx context.Context, conn *Conn, methodID uint32, request, response proto.Message) error {
	payload, err := proto.Marshal(request)
	if err != nil {
		return err
	}
	frame, err := conn.call(ctx, &agentrewire.Request{MethodId: methodID, EncodedPayload: payload})
	if err != nil {
		return err
	}
	if frame.GetMethodId() != methodID {
		return ErrResponseType
	}
	if err := proto.Unmarshal(frame.GetEncodedPayload(), response); err != nil {
		return fmt.Errorf("%w: %v", ErrResponseType, err)
	}
	return nil
}

// MethodGuard judges one inbound call before its handler runs. A non-nil error
// is answered to the caller verbatim and the handler never runs; otherwise the
// handler runs on the returned context, so the guard can hand its verdict on.
type MethodGuard func(ctx context.Context, methodID uint32) (context.Context, error)

// SetMethodGuard installs the guard every registered method on this registry —
// and on every later Clone of it — passes through. It is the host's single place
// to decide who may call what, instead of a check repeated in each handler.
// Calls to unregistered methods still answer method-not-found without it.
func (r *Registry) SetMethodGuard(guard MethodGuard) {
	r.methodMu.Lock()
	defer r.methodMu.Unlock()
	r.guard = guard
}

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
	registry.methods[methodID] = func(ctx context.Context, payload []byte) ([]byte, error) {
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
	handler := r.methods[methodID]
	r.methodMu.RUnlock()
	if handler == nil {
		return nil, methodNotFound()
	}
	return handler(ctx, payload)
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

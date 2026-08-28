package daemon

import (
	"context"
	"math"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/pkg/agentred/protocol"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

type protobufTerminalEmitter struct{ conn *protorpc.Conn }

func newProtobufTerminalEmitter(conn *protorpc.Conn) handlers.Emitter {
	return &protobufTerminalEmitter{conn: conn}
}

func bindProtobufTerminal(conn *protorpc.Conn, backend handlers.PTYBackend) *handlers.TerminalHandlers {
	terminal := handlers.NewTerminalHandlers(backend, newProtobufTerminalEmitter(conn))
	registerProtobufTerminalMethods(conn.Registry(), terminal)
	go func() {
		<-conn.Done()
		terminal.CloseAll()
	}()
	return terminal
}

func (e *protobufTerminalEmitter) Emit(_ context.Context, name string, payload any) {
	var notification *agentrewire.RpcNotification
	switch name {
	case handlers.EventNameTerminalData:
		event, ok := payload.(handlers.TerminalDataEvent)
		if !ok {
			return
		}
		notification = &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_TerminalData{
			TerminalData: &agentrewire.TerminalDataNotification{TerminalId: event.TerminalID, Data: event.Data},
		}}
	case handlers.EventNameTerminalExit:
		event, ok := payload.(protocol.TerminalExitEvent)
		if !ok {
			return
		}
		notification = &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_TerminalExit{
			TerminalExit: &agentrewire.TerminalExitNotification{TerminalId: event.TerminalID, Code: int32(event.Code), Reason: event.Reason, Message: event.Msg},
		}}
	default:
		return
	}
	_ = e.conn.Notify(notification)
}

func registerProtobufTerminalMethods(registry *protorpc.Registry, terminal *handlers.TerminalHandlers) {
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_OPEN),
		func() *agentrewire.TerminalOpenRequest { return &agentrewire.TerminalOpenRequest{} },
		func(ctx context.Context, request *agentrewire.TerminalOpenRequest) (*agentrewire.TerminalOpenResponse, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			if request.Cols > math.MaxUint16 || request.Rows > math.MaxUint16 {
				return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "terminal dimensions exceed uint16"}
			}
			result, err := terminal.Open(ctx, protocol.TerminalOpenParams{
				TerminalID: request.TerminalId, SessionID: request.SessionId, Cwd: request.Cwd,
				Shell: request.Shell, Command: request.Command, Env: append([]string(nil), request.Env...),
				Cols: uint16(request.Cols), Rows: uint16(request.Rows),
			})
			if err != nil {
				return nil, protobufError(err)
			}
			return &agentrewire.TerminalOpenResponse{TerminalId: result.TerminalID}, nil
		})

	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_WRITE),
		func() *agentrewire.TerminalWriteRequest { return &agentrewire.TerminalWriteRequest{} },
		func(ctx context.Context, request *agentrewire.TerminalWriteRequest) (*agentrewire.Empty, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			_, err := terminal.Write(ctx, protocol.TerminalWriteParams{TerminalID: request.TerminalId, Data: string(request.Data)})
			if err != nil {
				return nil, protobufError(err)
			}
			return &agentrewire.Empty{}, nil
		})

	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_RESIZE),
		func() *agentrewire.TerminalResizeRequest { return &agentrewire.TerminalResizeRequest{} },
		func(ctx context.Context, request *agentrewire.TerminalResizeRequest) (*agentrewire.Empty, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			if request.Cols > math.MaxUint16 || request.Rows > math.MaxUint16 {
				return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "terminal dimensions exceed uint16"}
			}
			_, err := terminal.Resize(ctx, protocol.TerminalResizeParams{TerminalID: request.TerminalId, Cols: uint16(request.Cols), Rows: uint16(request.Rows)})
			if err != nil {
				return nil, protobufError(err)
			}
			return &agentrewire.Empty{}, nil
		})

	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_CLOSE),
		func() *agentrewire.TerminalCloseRequest { return &agentrewire.TerminalCloseRequest{} },
		func(ctx context.Context, request *agentrewire.TerminalCloseRequest) (*agentrewire.Empty, error) {
			if err := requireProtobufAuth(ctx); err != nil {
				return nil, err
			}
			_, err := terminal.Close(ctx, protocol.TerminalCloseParams{TerminalID: request.TerminalId, CancelPendingOpen: request.CancelPendingOpen})
			if err != nil {
				return nil, protobufError(err)
			}
			return &agentrewire.Empty{}, nil
		})
}

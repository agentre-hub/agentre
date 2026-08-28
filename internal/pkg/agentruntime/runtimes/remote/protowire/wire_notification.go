package protowire

import (
	"fmt"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// WireNotificationToProto is the typed boundary from handler notification
// values to the Protobuf wire. It never serializes the containing value as a
// JSON carrier.
func WireNotificationToProto(method string, params any) (*agentrewire.RpcNotification, error) {
	switch method {
	case wire.NotifyEvent, wire.NotifyAutonomousTurnEvent:
		frame, ok := eventFrame(params)
		if !ok {
			return nil, fmt.Errorf("protowire: %s 参数类型为 %T", method, params)
		}
		out := &agentrewire.RuntimeEventNotification{SessionId: frame.SessionID, Seq: frame.Seq}
		if err := marshalEvent(out, frame.Event); err != nil {
			return nil, err
		}
		n := &agentrewire.RpcNotification{}
		if method == wire.NotifyEvent {
			n.Payload = &agentrewire.RpcNotification_RuntimeEvent{RuntimeEvent: out}
		} else {
			n.Payload = &agentrewire.RpcNotification_AutonomousTurnEvent{AutonomousTurnEvent: out}
		}
		return n, nil
	case wire.NotifyRunResultDone, wire.NotifyAutonomousTurnDone:
		frame, ok := doneFrame(params)
		if !ok {
			return nil, fmt.Errorf("protowire: %s 参数类型为 %T", method, params)
		}
		out := runResultDoneToProto(frame)
		n := &agentrewire.RpcNotification{}
		if method == wire.NotifyRunResultDone {
			n.Payload = &agentrewire.RpcNotification_RunResultDone{RunResultDone: out}
		} else {
			n.Payload = &agentrewire.RpcNotification_AutonomousTurnDone{AutonomousTurnDone: out}
		}
		return n, nil
	case wire.NotifyAutonomousTurnStarted:
		frame, ok := startedFrame(params)
		if !ok {
			return nil, fmt.Errorf("protowire: %s 参数类型为 %T", method, params)
		}
		return &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_AutonomousTurnStarted{AutonomousTurnStarted: &agentrewire.AutonomousTurnStartedNotification{SessionId: frame.SessionID, Seq: frame.Seq, Trigger: frame.Trigger, TurnToken: frame.TurnToken}}}, nil
	default:
		return nil, fmt.Errorf("protowire: 未知通知 %q", method)
	}
}

// ProtoNotificationToWire converts a typed payload into the process-local
// dispatch shape. 全程不经 JSON:事件以密封值进出,通知信封与日志是 Protobuf。
//
// 三类帧一律返回**指针**:下游要按 seqFrame 盖 seq,而 SetSeq 是指针接收者。
func ProtoNotificationToWire(notification *agentrewire.RpcNotification) (string, any, error) {
	switch payload := notification.GetPayload().(type) {
	case *agentrewire.RpcNotification_RuntimeEvent, *agentrewire.RpcNotification_AutonomousTurnEvent:
		frame := notification.GetRuntimeEvent()
		method := wire.NotifyEvent
		if frame == nil {
			frame = notification.GetAutonomousTurnEvent()
			method = wire.NotifyAutonomousTurnEvent
		}
		event, err := unmarshalEvent(frame)
		if err != nil {
			return "", nil, err
		}
		return method, &wire.EventFrame{SessionID: frame.GetSessionId(), Seq: frame.GetSeq(), Event: event}, nil
	case *agentrewire.RpcNotification_RunResultDone:
		return wire.NotifyRunResultDone, doneWire(payload.RunResultDone), nil
	case *agentrewire.RpcNotification_AutonomousTurnDone:
		return wire.NotifyAutonomousTurnDone, doneWire(payload.AutonomousTurnDone), nil
	case *agentrewire.RpcNotification_AutonomousTurnStarted:
		value := payload.AutonomousTurnStarted
		return wire.NotifyAutonomousTurnStarted, &wire.AutonomousTurnStartedFrame{SessionID: value.GetSessionId(), Seq: value.GetSeq(), Trigger: value.GetTrigger(), TurnToken: value.GetTurnToken()}, nil
	default:
		return "", nil, fmt.Errorf("protowire: unknown typed notification")
	}
}

func doneWire(value *agentrewire.RunResultDoneNotification) *wire.RunResultDoneFrame {
	out := &wire.RunResultDoneFrame{SessionID: value.GetSessionId(), Seq: value.GetSeq(), ProviderSessionID: value.GetProviderSessionId(), UserAnchor: value.GetUserAnchor(), Model: value.GetModel(), ContextWindow: int(value.GetContextWindow()), TurnToken: value.GetTurnToken(), StopErrMsg: value.GetStopErrorMessage(), StopErrCode: int(value.GetStopErrorCode())}
	if usage := value.GetUsage(); usage != nil {
		out.Usage = &wire.UsageWire{PromptTokens: int(usage.GetPromptTokens()), CompletionTokens: int(usage.GetCompletionTokens()), ReasoningTokens: int(usage.GetReasoningTokens()), CachedTokens: int(usage.GetCachedTokens()), CacheCreationTokens: int(usage.GetCacheCreationTokens()), TotalTokens: int(usage.GetTotalTokens())}
	}
	return out
}

func eventFrame(value any) (wire.EventFrame, bool) {
	switch v := value.(type) {
	case wire.EventFrame:
		return v, true
	case *wire.EventFrame:
		if v != nil {
			return *v, true
		}
	}
	return wire.EventFrame{}, false
}
func doneFrame(value any) (wire.RunResultDoneFrame, bool) {
	switch v := value.(type) {
	case wire.RunResultDoneFrame:
		return v, true
	case *wire.RunResultDoneFrame:
		if v != nil {
			return *v, true
		}
	}
	return wire.RunResultDoneFrame{}, false
}
func startedFrame(value any) (wire.AutonomousTurnStartedFrame, bool) {
	switch v := value.(type) {
	case wire.AutonomousTurnStartedFrame:
		return v, true
	case *wire.AutonomousTurnStartedFrame:
		if v != nil {
			return *v, true
		}
	}
	return wire.AutonomousTurnStartedFrame{}, false
}

func runResultDoneToProto(frame wire.RunResultDoneFrame) *agentrewire.RunResultDoneNotification {
	out := &agentrewire.RunResultDoneNotification{SessionId: frame.SessionID, Seq: frame.Seq, ProviderSessionId: frame.ProviderSessionID, UserAnchor: frame.UserAnchor, Model: frame.Model, ContextWindow: int32(frame.ContextWindow), TurnToken: frame.TurnToken, StopErrorMessage: frame.StopErrMsg, StopErrorCode: int32(frame.StopErrCode)}
	if usage := frame.Usage; usage != nil {
		out.Usage = &agentrewire.Usage{PromptTokens: int32(usage.PromptTokens), CompletionTokens: int32(usage.CompletionTokens), ReasoningTokens: int32(usage.ReasoningTokens), CachedTokens: int32(usage.CachedTokens), CacheCreationTokens: int32(usage.CacheCreationTokens), TotalTokens: int32(usage.TotalTokens)}
	}
	return out
}

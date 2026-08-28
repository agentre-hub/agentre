package protowire

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func EncodeNotification(notification *agentrewire.RpcNotification) ([]byte, error) {
	if notification == nil || notification.GetPayload() == nil {
		return nil, errors.New("protowire: notification 缺少 payload")
	}
	return proto.Marshal(notification)
}

func DecodeNotification(data []byte) (*agentrewire.RpcNotification, error) {
	notification := &agentrewire.RpcNotification{}
	if err := proto.Unmarshal(data, notification); err != nil {
		return nil, fmt.Errorf("protowire: decode notification: %w", err)
	}
	if notification.GetPayload() == nil {
		return nil, errors.New("protowire: notification 缺少 payload")
	}
	return notification, nil
}

func NotificationSessionID(notification *agentrewire.RpcNotification) int64 {
	if notification == nil {
		return 0
	}
	switch payload := notification.GetPayload().(type) {
	case *agentrewire.RpcNotification_RuntimeEvent:
		return payload.RuntimeEvent.GetSessionId()
	case *agentrewire.RpcNotification_RunResultDone:
		return payload.RunResultDone.GetSessionId()
	case *agentrewire.RpcNotification_AutonomousTurnStarted:
		return payload.AutonomousTurnStarted.GetSessionId()
	case *agentrewire.RpcNotification_AutonomousTurnEvent:
		return payload.AutonomousTurnEvent.GetSessionId()
	case *agentrewire.RpcNotification_AutonomousTurnDone:
		return payload.AutonomousTurnDone.GetSessionId()
	default:
		return 0
	}
}

// NotificationMethod 交回这条通知的 wire method 名。推送端口收的是已经转换好的
// Protobuf 通知,路由与日志需要的 method 串因此只能从消息本身解出 —— 另带一个 method
// 参数就等于给同一件事留两份真相,而两份真相迟早会分叉。
// 未知 / 空 payload 交回空串,调用方据此报错,而不是猜一个名字把帧推给别人。
func NotificationMethod(notification *agentrewire.RpcNotification) string {
	if notification == nil {
		return ""
	}
	switch notification.GetPayload().(type) {
	case *agentrewire.RpcNotification_RuntimeEvent:
		return wire.NotifyEvent
	case *agentrewire.RpcNotification_RunResultDone:
		return wire.NotifyRunResultDone
	case *agentrewire.RpcNotification_AutonomousTurnStarted:
		return wire.NotifyAutonomousTurnStarted
	case *agentrewire.RpcNotification_AutonomousTurnEvent:
		return wire.NotifyAutonomousTurnEvent
	case *agentrewire.RpcNotification_AutonomousTurnDone:
		return wire.NotifyAutonomousTurnDone
	default:
		return ""
	}
}

func NotificationSeq(notification *agentrewire.RpcNotification) int64 {
	if notification == nil {
		return 0
	}
	switch payload := notification.GetPayload().(type) {
	case *agentrewire.RpcNotification_RuntimeEvent:
		return payload.RuntimeEvent.GetSeq()
	case *agentrewire.RpcNotification_RunResultDone:
		return payload.RunResultDone.GetSeq()
	case *agentrewire.RpcNotification_AutonomousTurnStarted:
		return payload.AutonomousTurnStarted.GetSeq()
	case *agentrewire.RpcNotification_AutonomousTurnEvent:
		return payload.AutonomousTurnEvent.GetSeq()
	case *agentrewire.RpcNotification_AutonomousTurnDone:
		return payload.AutonomousTurnDone.GetSeq()
	default:
		return 0
	}
}

func SetNotificationSeq(notification *agentrewire.RpcNotification, seq int64) bool {
	if notification == nil {
		return false
	}
	switch payload := notification.GetPayload().(type) {
	case *agentrewire.RpcNotification_RuntimeEvent:
		payload.RuntimeEvent.Seq = seq
	case *agentrewire.RpcNotification_RunResultDone:
		payload.RunResultDone.Seq = seq
	case *agentrewire.RpcNotification_AutonomousTurnStarted:
		payload.AutonomousTurnStarted.Seq = seq
	case *agentrewire.RpcNotification_AutonomousTurnEvent:
		payload.AutonomousTurnEvent.Seq = seq
	case *agentrewire.RpcNotification_AutonomousTurnDone:
		payload.AutonomousTurnDone.Seq = seq
	default:
		return false
	}
	return true
}

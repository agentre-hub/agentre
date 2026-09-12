package protowire

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func TestTypedNotificationEncodeDecodeAndSeqCoverAllJournalKinds(t *testing.T) {
	cases := []*agentrewire.RpcNotification{
		{Payload: &agentrewire.RpcNotification_RuntimeEvent{RuntimeEvent: &agentrewire.RuntimeEventNotification{ConversationId: convID(42)}}},
		{Payload: &agentrewire.RpcNotification_RunResultDone{RunResultDone: &agentrewire.RunResultDoneNotification{ConversationId: convID(42)}}},
		{Payload: &agentrewire.RpcNotification_AutonomousTurnStarted{AutonomousTurnStarted: &agentrewire.AutonomousTurnStartedNotification{ConversationId: convID(42)}}},
		{Payload: &agentrewire.RpcNotification_AutonomousTurnEvent{AutonomousTurnEvent: &agentrewire.RuntimeEventNotification{ConversationId: convID(42)}}},
		{Payload: &agentrewire.RpcNotification_AutonomousTurnDone{AutonomousTurnDone: &agentrewire.RunResultDoneNotification{ConversationId: convID(42)}}},
	}
	for _, notification := range cases {
		require.True(t, SetNotificationSeq(notification, 7))
		encoded, err := EncodeNotification(notification)
		require.NoError(t, err)
		decoded, err := DecodeNotification(encoded)
		require.NoError(t, err)
		require.Equal(t, convID(42), NotificationConversationID(decoded))
		require.Equal(t, int64(7), NotificationSeq(decoded))
	}
}

func TestWireNotificationToProtoDirectlyConvertsTypedValues(t *testing.T) {
	event := agentruntime.TextDelta{Text: "hello"}
	cases := []struct {
		method string
		params any
	}{
		{wire.NotifyEvent, &wire.EventFrame{ConversationID: convID(42), Seq: 7, Event: event}},
		{wire.NotifyRunResultDone, &wire.RunResultDoneFrame{ConversationID: convID(42), Seq: 7}},
		{wire.NotifyAutonomousTurnStarted, &wire.AutonomousTurnStartedFrame{ConversationID: convID(42), Seq: 7}},
		{wire.NotifyAutonomousTurnEvent, &wire.EventFrame{ConversationID: convID(42), Seq: 7, Event: event}},
		{wire.NotifyAutonomousTurnDone, &wire.RunResultDoneFrame{ConversationID: convID(42), Seq: 7}},
	}
	for _, tc := range cases {
		n, err := WireNotificationToProto(tc.method, tc.params)
		require.NoError(t, err)
		require.Equal(t, convID(42), NotificationConversationID(n))
		require.Equal(t, int64(7), NotificationSeq(n))
	}
	_, err := WireNotificationToProto(wire.NotifyEvent, json.RawMessage(`{}`))
	require.ErrorContains(t, err, "参数类型")
}

func TestTypedNotificationRejectsMissingPayloadAndSeqRejectsUnknownFuturePayload(t *testing.T) {
	_, err := EncodeNotification(&agentrewire.RpcNotification{})
	require.ErrorContains(t, err, "缺少 payload")
	_, err = DecodeNotification(nil)
	require.ErrorContains(t, err, "缺少 payload")
	require.False(t, SetNotificationSeq(&agentrewire.RpcNotification{}, 1))
	require.Empty(t, NotificationConversationID(&agentrewire.RpcNotification{}))
	require.Zero(t, NotificationSeq(&agentrewire.RpcNotification{}))
}

// TestNotificationMethodNamesEveryJournalKind 钉死「method 名从消息本身读出来」:推送
// 端口交出的是已经转换好的 Protobuf 通知,路由与日志需要的 method 串必须由它自己解出,
// 而不是另带一个可能与消息内容不一致的第二份真相。未知 / 空 payload 交回空串,调用方
// 据此报错,而不是猜一个方法名把帧推给别人。
func TestNotificationMethodNamesEveryJournalKind(t *testing.T) {
	cases := []struct {
		want         string
		notification *agentrewire.RpcNotification
	}{
		{wire.NotifyEvent, &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_RuntimeEvent{RuntimeEvent: &agentrewire.RuntimeEventNotification{}}}},
		{wire.NotifyRunResultDone, &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_RunResultDone{RunResultDone: &agentrewire.RunResultDoneNotification{}}}},
		{wire.NotifyAutonomousTurnStarted, &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_AutonomousTurnStarted{AutonomousTurnStarted: &agentrewire.AutonomousTurnStartedNotification{}}}},
		{wire.NotifyAutonomousTurnEvent, &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_AutonomousTurnEvent{AutonomousTurnEvent: &agentrewire.RuntimeEventNotification{}}}},
		{wire.NotifyAutonomousTurnDone, &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_AutonomousTurnDone{AutonomousTurnDone: &agentrewire.RunResultDoneNotification{}}}},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, NotificationMethod(tc.notification))
	}
	require.Empty(t, NotificationMethod(&agentrewire.RpcNotification{}))
	require.Empty(t, NotificationMethod(nil))
}

// Given 帧分两级(spec 决策 4),预览帧与持久帧必须在 protobuf 这一侧也分得开;
// When  一条预览帧与一条持久帧各走一遍 wire → proto → wire;
// Then  preview 这一格原样往返 —— 协议是二进制的,消费方分不出 seq 的 0 是"没给"
// 还是"给了 0",所以判别只能是这一格。
func TestPreviewLevelSurvivesTheProtobufRoundTrip(t *testing.T) {
	event := agentruntime.TextDelta{Text: "逐字"}
	for _, method := range []string{wire.NotifyEvent, wire.NotifyAutonomousTurnEvent} {
		preview, err := WireNotificationToProto(method, &wire.EventFrame{ConversationID: convID(42), Preview: true, Event: event})
		require.NoError(t, err)
		_, back, err := ProtoNotificationToWire(preview)
		require.NoError(t, err)
		require.True(t, back.(*wire.EventFrame).Preview, "%s: 预览帧过一趟 proto 之后仍是预览帧", method)
		require.Zero(t, back.(*wire.EventFrame).Seq, "%s: 预览帧不带编号", method)

		durable, err := WireNotificationToProto(method, &wire.EventFrame{ConversationID: convID(42), Seq: 6, Event: event})
		require.NoError(t, err)
		_, back, err = ProtoNotificationToWire(durable)
		require.NoError(t, err)
		require.False(t, back.(*wire.EventFrame).Preview, "%s: 持久帧不该被读成预览帧", method)
		require.Equal(t, int64(6), back.(*wire.EventFrame).Seq)
	}
}

package protowire

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// Given 一条补齐分页里的日志条目, When 它随 SessionPullResponse 过一次线,
// Then 它仍是**带类型**的 RpcNotification 而不是不透明字节 —— 收端不必再猜内容,
// 直接读得出 sessionId。
//
// 会拒绝的错误实现:把 JournaledNotification.Payload 改回 bytes(或在装配时
// 先 EncodeNotification 成字节再塞进去)。notification_test.go 覆盖的是编解码
// 本身,对载体形状退化无感;生产装配点在 daemon/protobuf_registry.go 的
// session.pull handler,和这里同形。
func TestSessionPullResponseCarriesTypedJournalPayload(t *testing.T) {
	notification := &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_RuntimeEvent{
		RuntimeEvent: &agentrewire.RuntimeEventNotification{
			SessionId: 42,
			Event:     &agentrewire.RuntimeEventNotification_TextDelta{TextDelta: &agentrewire.TextDelta{Text: "hello"}},
		},
	}}
	require.True(t, SetNotificationSeq(notification, 7))

	response := &agentrewire.SessionPullResponse{Cursor: 7, OldestSeq: 1}
	response.Notifications = append(response.Notifications,
		&agentrewire.JournaledNotification{Seq: 7, Payload: notification})

	// methodID 路径把应答体放进 Response.encoded_payload,过线的就是这串字节。
	encoded, err := proto.Marshal(response)
	require.NoError(t, err)
	decoded := new(agentrewire.SessionPullResponse)
	require.NoError(t, proto.Unmarshal(encoded, decoded))

	require.Len(t, decoded.GetNotifications(), 1)
	entry := decoded.GetNotifications()[0]
	require.Equal(t, int64(7), entry.GetSeq())
	require.Equal(t, int64(42), NotificationSessionID(entry.GetPayload()),
		"日志载荷必须仍是 typed RpcNotification,读得出 sessionId")
	require.Equal(t, "hello", entry.GetPayload().GetRuntimeEvent().GetTextDelta().GetText())
	require.Equal(t, int64(7), decoded.GetCursor())
	require.Equal(t, int64(1), decoded.GetOldestSeq())
}

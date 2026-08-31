package notifier_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/daemon/notifier"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// TestProtobufNotifierWritesTheGivenNotificationUnchanged 钉死推送端口这一层**不再
// 转换**:发送侧(handlers 的会话出口)转换一次并落库,交到这里的就是那条消息本身,
// 端口只负责把它写上连接 —— 逐字段原样,包括发送侧盖上的 seq。
//
// 会退化成什么:这一层若自己再从 wire 帧转一次(它曾经就是那样),每个流式 token 都要
// 多付一次密封事件的 JSON 解码加一整棵消息树的构造,而输出字节一模一样 —— 没有任何
// 别的用例会因此变红。
func TestProtobufNotifierWritesTheGivenNotificationUnchanged(t *testing.T) {
	transport := &recordingFrameConn{done: make(chan struct{})}
	conn := protorpc.NewConn(transport, protorpc.NewRegistry())
	n := notifier.NewProtobuf(conn)
	notification, err := protowire.WireNotificationToProto(wire.NotifyEvent,
		&wire.EventFrame{ConversationID: convID(42), Seq: 7, Event: agentruntime.TextDelta{Text: "hello"}})
	require.NoError(t, err)

	require.NoError(t, n.Notify(notification))
	require.Len(t, transport.frames, 1)
	frame := new(agentrewire.RpcFrame)
	require.NoError(t, proto.Unmarshal(transport.frames[0], frame))
	require.True(t, proto.Equal(notification, frame.GetNotification()),
		"写上连接的必须就是交进来的那条通知")
	require.Equal(t, convID(42), frame.GetNotification().GetRuntimeEvent().GetConversationId())
	require.Equal(t, int64(7), frame.GetNotification().GetRuntimeEvent().GetSeq())
	require.Equal(t, "hello", frame.GetNotification().GetRuntimeEvent().GetTextDelta().GetText())
}

type recordingFrameConn struct {
	frames [][]byte
	done   chan struct{}
}

func (c *recordingFrameConn) ReadFrame() ([]byte, error) { <-c.done; return nil, context.Canceled }
func (c *recordingFrameConn) WriteFrame(frame []byte) error {
	c.frames = append(c.frames, append([]byte(nil), frame...))
	return nil
}
func (c *recordingFrameConn) Close() error          { return nil }
func (c *recordingFrameConn) Done() <-chan struct{} { return c.done }

// convID 把一个短会话号折成一条**格式合法**的 conversation_id,只在测试里用:
// 线上身份是 uuid,而这些用例真正要断言的是"同一个值原样往返"与"两条不同的对话
// 互不并轨",一个可读、可复现的映射比随机 uuid 更好读。
func convID(n int64) string {
	return fmt.Sprintf("00000000-0000-7000-8000-%012d", n)
}

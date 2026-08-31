package peer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

type outboundProtoClient struct{ conn *protorpc.Conn }

func (c *outboundProtoClient) Conn() *protorpc.Conn    { return c.conn }
func (c *outboundProtoClient) Closed() <-chan struct{} { return c.conn.Done() }
func (c *outboundProtoClient) Close() error            { return c.conn.Close() }

func TestOutboundUsesTypedProtobufSessionMethods(t *testing.T) {
	var steered wire.SteerParams
	registry := NewProtobufInboundRegistry(ProtobufInboundDeps{
		ListSessions: func(context.Context, string) (*wire.SessionListResult, error) {
			return &wire.SessionListResult{Sessions: []wire.SessionSummary{{SessionID: 7, Title: "remote"}}}, nil
		},
		AttachSession: func(_ context.Context, p wire.SessionAttachParams, _ chat_svc.PeerSessionSubscriber) (wire.SessionAttachResult, error) {
			return wire.SessionAttachResult{SessionID: p.SessionID, LatestSeq: 12}, nil
		},
		PullSession: func(_ context.Context, p wire.SessionPullParams, _ chat_svc.PeerSessionSubscriber) (wire.SessionPullResult, error) {
			return wire.SessionPullResult{Cursor: p.Cursor + 1, OldestSeq: 1}, nil
		},
		RunSession: func(_ context.Context, p wire.RunParams, _ chat_svc.PeerSessionSource) (*chat_svc.SendResponse, error) {
			require.True(t, p.FreshSession)
			return &chat_svc.SendResponse{SessionID: 42}, nil
		},
		SteerSession: func(_ context.Context, p wire.SteerParams, _ chat_svc.PeerSessionSource) error {
			steered = p
			return nil
		},
		SubmitAnswer: func(context.Context, wire.SubmitAnswerParams) (chat_svc.PeerSessionControlResult, error) {
			return chat_svc.PeerSessionControlResult{AlreadyHandled: true}, nil
		},
		SubmitToolPermission: func(context.Context, wire.SubmitToolPermissionParams) (chat_svc.PeerSessionControlResult, error) {
			return chat_svc.PeerSessionControlResult{AlreadyHandled: true}, nil
		},
	})
	clientTransport, serverTransport := peerProtoPipePair()
	clientConn := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	serverConn := protorpc.NewConn(serverTransport, registry)
	serverConn.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: "sha256:caller"})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go clientConn.Serve(ctx)
	go serverConn.Serve(ctx)
	ob := NewOutbound(&outboundProtoClient{conn: clientConn}, "sha256:peer")

	list, err := ob.ListSessions(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(7), list.Sessions[0].SessionID)
	attached, err := ob.Attach(ctx, wire.SessionAttachParams{SessionID: 7})
	require.NoError(t, err)
	require.Equal(t, int64(12), attached.LatestSeq)
	pulled, err := ob.Pull(ctx, wire.SessionPullParams{SessionID: 7, Cursor: 3})
	require.NoError(t, err)
	require.Equal(t, int64(4), pulled.Cursor)
	ack, err := ob.RunFresh(ctx, wire.RunParams{SessionID: 99, UserText: "go"})
	require.NoError(t, err)
	require.Equal(t, int64(42), ack.SessionID)
	require.NoError(t, ob.Steer(ctx, wire.SteerParams{SessionID: 7, Text: "continue"}))
	require.Equal(t, "continue", steered.Text)
	answer, err := ob.SubmitAnswer(ctx, wire.SubmitAnswerParams{SessionID: 7, RequestID: "a"})
	require.NoError(t, err)
	require.True(t, answer.AlreadyHandled)
	permission, err := ob.SubmitToolPermission(ctx, wire.SubmitToolPermissionParams{SessionID: 7, RequestID: "p", Allow: true})
	require.NoError(t, err)
	require.True(t, permission.AlreadyHandled)
}

func TestOutboundReceivesTypedRuntimeEventNotification(t *testing.T) {
	registry := NewProtobufInboundRegistry(ProtobufInboundDeps{AttachSession: func(_ context.Context, p wire.SessionAttachParams, subscriber chat_svc.PeerSessionSubscriber) (wire.SessionAttachResult, error) {
		require.NoError(t, subscriber.Notify(wire.NotifyEvent, wire.EventFrame{SessionID: p.SessionID, Seq: 13, Event: agentruntime.TextDelta{Text: "hi"}}))
		return wire.SessionAttachResult{SessionID: p.SessionID, LatestSeq: 12}, nil
	}})
	clientTransport, serverTransport := peerProtoPipePair()
	clientConn := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	serverConn := protorpc.NewConn(serverTransport, registry)
	serverConn.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: "sha256:caller"})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go clientConn.Serve(ctx)
	go serverConn.Serve(ctx)
	ob := NewOutbound(&outboundProtoClient{conn: clientConn}, "sha256:peer")
	events := make(chan wire.EventFrame, 1)
	rawEvents := make(chan struct{}, 1)
	clientConn.Registry().SubscribeNotification(func(context.Context, *agentrewire.RpcNotification) error { rawEvents <- struct{}{}; return nil })
	ob.HandleEvent(func(frame wire.EventFrame) error { events <- frame; return nil })
	_, err := ob.Attach(ctx, wire.SessionAttachParams{SessionID: 7})
	require.NoError(t, err)
	select {
	case frame := <-events:
		require.Equal(t, int64(7), frame.SessionID)
		require.Equal(t, int64(13), frame.Seq)
	case <-time.After(time.Second):
		select {
		case <-rawEvents:
			t.Fatal("typed notification arrived but event conversion failed")
		default:
			t.Fatal("typed runtime event was not delivered")
		}
	}
}

// TestOutboundDiscardsAutonomousTurnEventNotifications 钉死:自主续轮的事件帧不走
// HandleEvent 这条出口。Peer Tab 渲染的是**对端用户轮**的转录,把自主续轮的增量混
// 进去会让远端转录多出本地那边根本没显示的内容。
func TestOutboundDiscardsAutonomousTurnEventNotifications(t *testing.T) {
	registry := NewProtobufInboundRegistry(ProtobufInboundDeps{AttachSession: func(_ context.Context, p wire.SessionAttachParams, subscriber chat_svc.PeerSessionSubscriber) (wire.SessionAttachResult, error) {
		// 先发一条自主续轮事件(必须被丢掉),再发一条普通事件(必须到达)——
		// 用「后一条到了」证明前一条是被**丢弃**而不是还没到。
		require.NoError(t, subscriber.Notify(wire.NotifyAutonomousTurnEvent, wire.EventFrame{
			SessionID: p.SessionID, Seq: 1, Event: agentruntime.TextDelta{Text: "autonomous"},
		}))
		require.NoError(t, subscriber.Notify(wire.NotifyEvent, wire.EventFrame{
			SessionID: p.SessionID, Seq: 2, Event: agentruntime.TextDelta{Text: "user-turn"},
		}))
		return wire.SessionAttachResult{SessionID: p.SessionID}, nil
	}})
	ob, ctx := newOutboundOverProtoPipe(t, registry)

	events := make(chan wire.EventFrame, 4)
	ob.HandleEvent(func(frame wire.EventFrame) error { events <- frame; return nil })
	_, err := ob.Attach(ctx, wire.SessionAttachParams{SessionID: 7})
	require.NoError(t, err)

	select {
	case frame := <-events:
		require.Equal(t, int64(2), frame.Seq, "到达的必须是普通事件那条,自主续轮那条应当被丢掉")
		td, ok := frame.Event.(agentruntime.TextDelta)
		require.True(t, ok)
		require.Equal(t, "user-turn", td.Text)
	case <-time.After(time.Second):
		t.Fatal("普通 runtime 事件没有送达")
	}
	select {
	case extra := <-events:
		t.Fatalf("不该再有第二条事件,却收到 seq=%d", extra.Seq)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestOutboundIgnoresNonEventNotifications 钉死:非事件类通知(终态帧 / 自主续轮
// Started)不得被当成事件送进 HandleEvent,也不得让订阅者炸掉后面的通知。
func TestOutboundIgnoresNonEventNotifications(t *testing.T) {
	registry := NewProtobufInboundRegistry(ProtobufInboundDeps{AttachSession: func(_ context.Context, p wire.SessionAttachParams, subscriber chat_svc.PeerSessionSubscriber) (wire.SessionAttachResult, error) {
		require.NoError(t, subscriber.Notify(wire.NotifyRunResultDone, wire.RunResultDoneFrame{SessionID: p.SessionID, Seq: 1}))
		require.NoError(t, subscriber.Notify(wire.NotifyAutonomousTurnStarted, wire.AutonomousTurnStartedFrame{SessionID: p.SessionID, Seq: 2, Trigger: "t"}))
		require.NoError(t, subscriber.Notify(wire.NotifyEvent, wire.EventFrame{
			SessionID: p.SessionID, Seq: 3, Event: agentruntime.TextDelta{Text: "after"},
		}))
		return wire.SessionAttachResult{SessionID: p.SessionID}, nil
	}})
	ob, ctx := newOutboundOverProtoPipe(t, registry)

	events := make(chan wire.EventFrame, 4)
	ob.HandleEvent(func(frame wire.EventFrame) error { events <- frame; return nil })
	_, err := ob.Attach(ctx, wire.SessionAttachParams{SessionID: 7})
	require.NoError(t, err)

	select {
	case frame := <-events:
		require.Equal(t, int64(3), frame.Seq, "只有事件帧该到达,且前面两条非事件通知不能挡住它")
	case <-time.After(time.Second):
		t.Fatal("非事件通知之后的事件帧没有送达")
	}
	select {
	case extra := <-events:
		t.Fatalf("非事件通知不该被当成事件送出,却收到 seq=%d", extra.Seq)
	case <-time.After(100 * time.Millisecond):
	}
}

// newOutboundOverProtoPipe 起一对真的 protorpc 连接,返回接在客户端那侧的 Outbound。
func newOutboundOverProtoPipe(t *testing.T, registry *protorpc.Registry) (*Outbound, context.Context) {
	t.Helper()
	clientTransport, serverTransport := peerProtoPipePair()
	clientConn := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	serverConn := protorpc.NewConn(serverTransport, registry)
	serverConn.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: "sha256:caller"})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go clientConn.Serve(ctx)
	go serverConn.Serve(ctx)
	return NewOutbound(&outboundProtoClient{conn: clientConn}, "sha256:peer"), ctx
}

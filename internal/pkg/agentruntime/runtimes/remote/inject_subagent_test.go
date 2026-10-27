package remote

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// inject_subagent_test.go 钉住桌面端 → agentred 的子会话事件注入(spec 2026-10-27「agrctl send
// 子代理卡」Remote sessions 第 2 条):父会话在 agentred 上执行时,桌面端把 `agrctl send --wait`
// 子会话的事件经 runtime.injectSubagentEvents 交给 agentred,由它写进自己的父转录。请求带着
// 这条会话的线上身份与发起端,事件按 agentred 解码的同一套编码逐条原样到达。

// framePipe 是一对进程内帧通道(protorpc.FrameConn),两端各挂一只 protorpc.Conn。
type framePipe struct {
	in, out chan []byte
	done    chan struct{}
	once    *sync.Once
}

func newFramePipePair() (*framePipe, *framePipe) {
	a, b := make(chan []byte, 64), make(chan []byte, 64)
	done, once := make(chan struct{}), &sync.Once{}
	return &framePipe{a, b, done, once}, &framePipe{b, a, done, once}
}

func (p *framePipe) ReadFrame() ([]byte, error) {
	select {
	case b := <-p.in:
		return b, nil
	case <-p.done:
		return nil, io.EOF
	}
}

func (p *framePipe) WriteFrame(b []byte) error {
	select {
	case p.out <- append([]byte(nil), b...):
		return nil
	case <-p.done:
		return io.EOF
	}
}

func (p *framePipe) Close() error          { p.once.Do(func() { close(p.done) }); return nil }
func (p *framePipe) Done() <-chan struct{} { return p.done }

// injectConn 是桌面端一侧的连接;服务端那一侧只注册注入方法并记下收到的请求。
type injectConn struct {
	conn *protorpc.Conn
	pipe *framePipe
}

func (c *injectConn) Conn() *protorpc.Conn    { return c.conn }
func (c *injectConn) Closed() <-chan struct{} { return nil }
func (c *injectConn) Close() error            { return c.pipe.Close() }
func (c *injectConn) SelfFingerprint() string { return "" }

type injectDaemon struct {
	mu   sync.Mutex
	got  []*agentrewire.RuntimeInjectSubagentEventsRequest
	fail error
}

func (d *injectDaemon) requests() []*agentrewire.RuntimeInjectSubagentEventsRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]*agentrewire.RuntimeInjectSubagentEventsRequest(nil), d.got...)
}

func newInjectRig(t *testing.T) (*Runtime, *injectDaemon) {
	t.Helper()
	clientPipe, serverPipe := newFramePipePair()
	daemon := &injectDaemon{}
	reg := protorpc.NewRegistry()
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_INJECT_SUBAGENT_EVENTS),
		func() *agentrewire.RuntimeInjectSubagentEventsRequest {
			return &agentrewire.RuntimeInjectSubagentEventsRequest{}
		},
		func(_ context.Context, req *agentrewire.RuntimeInjectSubagentEventsRequest) (*agentrewire.Empty, error) {
			daemon.mu.Lock()
			defer daemon.mu.Unlock()
			if daemon.fail != nil {
				return nil, daemon.fail
			}
			daemon.got = append(daemon.got, req)
			return &agentrewire.Empty{}, nil
		})
	ctx, cancel := context.WithCancel(context.Background())
	server := protorpc.NewConn(serverPipe, reg)
	client := protorpc.NewConn(clientPipe, protorpc.NewRegistry())
	go server.Serve(ctx)
	go client.Serve(ctx)
	t.Cleanup(func() { cancel(); _ = clientPipe.Close() })
	rt := New(&injectConn{conn: client, pipe: clientPipe}, WithConversationIDResolver(convOf))
	return rt, daemon
}

// decodeInjected 按 agentred 那一侧的办法(handlers.injectableEvents)把请求里的事件解回来。
func decodeInjected(t *testing.T, req *agentrewire.RuntimeInjectSubagentEventsRequest) []agentruntime.Event {
	t.Helper()
	out := make([]agentruntime.Event, 0, len(req.GetEvents()))
	for _, frame := range req.GetEvents() {
		raw, err := proto.Marshal(&agentrewire.RpcFrame{Body: &agentrewire.RpcFrame_Notification{
			Notification: &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_RuntimeEvent{RuntimeEvent: frame}},
		}})
		require.NoError(t, err)
		decoded, _, err := protowire.UnmarshalEventNotification(raw)
		require.NoError(t, err)
		out = append(out, decoded.Event)
	}
	return out
}

func TestInjectSubagentEvents_GivenParentOnAgentred_WhenInjecting_ThenOneRequestCarriesTheBatchVerbatim(t *testing.T) {
	rt, daemon := newInjectRig(t)
	rt.rememberOrigin(convOf(4), "peer-A")
	started := agentruntime.SubagentStarted{ToolCallID: "sh-1", Info: agentruntime.SubagentInfo{
		Kind: agentruntime.SubagentKindAgrctl, Origin: agentruntime.SubagentOriginAgrctl,
		ChildSessionID: 99, Isolated: true, SubagentType: "reviewer", TaskDescription: "审一下", Status: "running",
	}}
	nested := agentruntime.ToolCall{ID: "agrctl:4:sh-1:c-1", Name: "Read", ParentToolCallID: "sh-1", Input: json.RawMessage(`{"file_path":"a.go"}`)}
	done := agentruntime.SubagentDone{ToolCallID: "sh-1", Info: agentruntime.SubagentInfo{
		Origin: agentruntime.SubagentOriginAgrctl, Status: "completed", FinalText: "LGTM", ToolUses: 1,
	}}

	require.NoError(t, rt.InjectSubagentEvents(context.Background(), 4, "sh-1", []agentruntime.Event{started, nested, done}))

	reqs := daemon.requests()
	require.Len(t, reqs, 1, "a batch is one round trip")
	assert.Equal(t, convOf(4), reqs[0].GetConversationId())
	assert.Equal(t, "peer-A", reqs[0].GetPeerFingerprint(), "the daemon resolves the session through its originating peer")
	assert.Equal(t, "sh-1", reqs[0].GetToolCallId())
	got := decodeInjected(t, reqs[0])
	require.Len(t, got, 3)
	assert.Equal(t, started, got[0])
	assert.Equal(t, "agrctl:4:sh-1:c-1", got[1].(agentruntime.ToolCall).ID)
	assert.Equal(t, "sh-1", got[1].(agentruntime.ToolCall).ParentToolCallID)
	assert.Equal(t, done, got[2])
}

func TestInjectSubagentEvents_GivenUnknownConversationOrDaemonError_ThenTheErrorIsReturned(t *testing.T) {
	rt, daemon := newInjectRig(t)
	rt.conversations = func(int64) string { return "" }
	ev := []agentruntime.Event{agentruntime.SubagentDone{ToolCallID: "sh-1", Info: agentruntime.SubagentInfo{Origin: agentruntime.SubagentOriginAgrctl}}}

	err := rt.InjectSubagentEvents(context.Background(), 4, "sh-1", ev)
	require.Error(t, err, "without a conversation identity there is nowhere to inject")
	assert.Empty(t, daemon.requests(), "nothing is sent")

	rt2, daemon2 := newInjectRig(t)
	daemon2.fail = &protorpc.Error{Code: protorpc.CodeInternal, Message: "boom"}
	require.Error(t, rt2.InjectSubagentEvents(context.Background(), 4, "sh-1", ev))
}

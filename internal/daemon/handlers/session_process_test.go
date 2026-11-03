package handlers_test

import (
	"context"
	"errors"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/handlers/mock_handlers"
	"github.com/agentre-hub/agentre/internal/pkg/procscan"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// fakeProcessScanner 记下每次调用并按预设作答;扫描本身由 procscan 的单测覆盖,
// 这里只关心 handler 怎么把它接到线上。
type fakeProcessScanner struct {
	listed  []procscan.Listed
	listErr error
	killErr error

	listCalls []string
	killCalls []fakeKill
}

type fakeKill struct {
	convID      string
	pid         int
	startedAtMs int64
}

func (f *fakeProcessScanner) List(_ context.Context, convID string) ([]procscan.Listed, error) {
	f.listCalls = append(f.listCalls, convID)
	return f.listed, f.listErr
}

func (f *fakeProcessScanner) Kill(_ context.Context, convID string, pid int, startedAtMs int64) error {
	f.killCalls = append(f.killCalls, fakeKill{convID: convID, pid: pid, startedAtMs: startedAtMs})
	return f.killErr
}

func newSessionProcessHandlers(t *testing.T, scanner *fakeProcessScanner) (*handlers.SessionProcessHandlers, *mock_handlers.MockSessionQueryPort) {
	t.Helper()
	sessions := mock_handlers.NewMockSessionQueryPort(gomock.NewController(t))
	h := handlers.NewSessionProcessHandlers(handlers.SessionProcessDeps{
		Scanner:  scanner,
		Sessions: sessions,
	})
	return h, sessions
}

func requireRPCCode(t *testing.T, err error, code int32) {
	t.Helper()
	var rpcErr *rpcerror.Error
	require.Truef(t, errors.As(err, &rpcErr), "要的是 *rpcerror.Error,拿到 %T: %v", err, err)
	require.Equal(t, code, rpcErr.Code)
}

func TestSessionProcessHandlers_List(t *testing.T) {
	convey.Convey("Given 调用方自己的会话留下了两个进程(一个监听端口、一个已脱离),When 列出,Then 每一格原样映射到线上且按 conversation_id 扫描", t, func() {
		scanner := &fakeProcessScanner{listed: []procscan.Listed{
			{PID: 4242, Command: "node server.js", Ports: []uint16{5391, 8080}, StartedAtMs: 1_700_000_000_000, Cwd: "/work/app"},
			{PID: 77, Command: "tail -f log", StartedAtMs: 1_700_000_001_000},
		}}
		h, sessions := newSessionProcessHandlers(t, scanner)
		sessions.EXPECT().Find(gomock.Any(), devicefp.Initiator(""), convID(8)).Return(&handlers.SessionRecord{PeerSessionID: convID(8)}, nil)

		got, err := h.List(context.Background(), &agentrewire.RuntimeListSessionProcessesRequest{ConversationId: convID(8)})

		require.NoError(t, err)
		require.Equal(t, []string{convID(8)}, scanner.listCalls)
		require.Len(t, got.GetProcesses(), 2)
		first := got.GetProcesses()[0]
		require.Equal(t, uint32(4242), first.GetPid())
		require.Equal(t, "node server.js", first.GetCommand())
		require.Equal(t, []uint32{5391, 8080}, first.GetPorts())
		require.Equal(t, int64(1_700_000_000_000), first.GetStartedAtMs())
		require.Equal(t, "/work/app", first.GetCwd())
		second := got.GetProcesses()[1]
		require.Equal(t, uint32(77), second.GetPid())
		require.Empty(t, second.GetPorts())
		require.Empty(t, second.GetCwd())
	})

	convey.Convey("Given 会话里一个进程都没留下,When 列出,Then 成功返回空清单而不是错误", t, func() {
		h, sessions := newSessionProcessHandlers(t, &fakeProcessScanner{})
		sessions.EXPECT().Find(gomock.Any(), devicefp.Initiator(""), convID(8)).Return(&handlers.SessionRecord{PeerSessionID: convID(8)}, nil)

		got, err := h.List(context.Background(), &agentrewire.RuntimeListSessionProcessesRequest{ConversationId: convID(8)})

		require.NoError(t, err)
		require.Empty(t, got.GetProcesses())
	})

	convey.Convey("Given 这条会话不属于调用方(或根本不存在),When 列出,Then 拒绝且不扫描 —— 否则任一配对对端都能窥见别人会话的进程", t, func() {
		scanner := &fakeProcessScanner{listed: []procscan.Listed{{PID: 1}}}
		h, sessions := newSessionProcessHandlers(t, scanner)
		sessions.EXPECT().Find(gomock.Any(), devicefp.Initiator(""), convID(9)).Return(nil, nil)

		_, err := h.List(context.Background(), &agentrewire.RuntimeListSessionProcessesRequest{ConversationId: convID(9)})

		require.ErrorIs(t, err, rpcerror.ErrSessionNotFound)
		require.Empty(t, scanner.listCalls)
	})

	convey.Convey("Given 请求点名了另一台对端、而这条连接不是本机登录账号,When 列出,Then Unauthorized 且不查库不扫描", t, func() {
		scanner := &fakeProcessScanner{}
		h, _ := newSessionProcessHandlers(t, scanner)

		_, err := h.List(context.Background(), &agentrewire.RuntimeListSessionProcessesRequest{ConversationId: convID(8), PeerFingerprint: "fp-someone-else"})

		require.ErrorIs(t, err, rpcerror.ErrUnauthorized)
		require.Empty(t, scanner.listCalls)
	})

	convey.Convey("Given conversation_id 不是一条对话身份,When 列出,Then 在边界上以 InvalidParams 拒掉", t, func() {
		scanner := &fakeProcessScanner{}
		h, _ := newSessionProcessHandlers(t, scanner)

		_, err := h.List(context.Background(), &agentrewire.RuntimeListSessionProcessesRequest{ConversationId: "42"})

		requireRPCCode(t, err, rpcerror.CodeInvalidParams)
		require.Empty(t, scanner.listCalls)
	})

	convey.Convey("Given 扫描本身失败(平台不支持 / 读不到进程表),When 列出,Then 错误原样上抛(注册处折成内部错误),不伪装成空清单", t, func() {
		h, sessions := newSessionProcessHandlers(t, &fakeProcessScanner{listErr: procscan.ErrUnsupported})
		sessions.EXPECT().Find(gomock.Any(), devicefp.Initiator(""), convID(8)).Return(&handlers.SessionRecord{PeerSessionID: convID(8)}, nil)

		_, err := h.List(context.Background(), &agentrewire.RuntimeListSessionProcessesRequest{ConversationId: convID(8)})

		require.ErrorIs(t, err, procscan.ErrUnsupported)
		var rpcErr *rpcerror.Error
		require.False(t, errors.As(err, &rpcErr), "扫描失败不该冒充某个领域码")
	})

	convey.Convey("Given 查会话的存储出错,When 列出,Then 错误上抛且不扫描", t, func() {
		scanner := &fakeProcessScanner{}
		h, sessions := newSessionProcessHandlers(t, scanner)
		dbErr := errors.New("disk I/O error")
		sessions.EXPECT().Find(gomock.Any(), devicefp.Initiator(""), convID(8)).Return(nil, dbErr)

		_, err := h.List(context.Background(), &agentrewire.RuntimeListSessionProcessesRequest{ConversationId: convID(8)})

		require.ErrorIs(t, err, dbErr)
		require.Empty(t, scanner.listCalls)
	})
}

func TestSessionProcessHandlers_Kill(t *testing.T) {
	convey.Convey("Given 调用方自己的会话,When 结束某个进程,Then 按 conversation_id + pid + 列出时的启动时间交给扫描器核对后结束", t, func() {
		scanner := &fakeProcessScanner{}
		h, sessions := newSessionProcessHandlers(t, scanner)
		sessions.EXPECT().Find(gomock.Any(), devicefp.Initiator(""), convID(8)).Return(&handlers.SessionRecord{PeerSessionID: convID(8)}, nil)

		got, err := h.Kill(context.Background(), &agentrewire.RuntimeKillSessionProcessRequest{ConversationId: convID(8), Pid: 4242, StartedAtMs: 1_700_000_000_000})

		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, []fakeKill{{convID: convID(8), pid: 4242, startedAtMs: 1_700_000_000_000}}, scanner.killCalls)
	})

	convey.Convey("Given 进程已不属于本会话(已退出 / 标记不符 / pid 被复用),When 结束,Then 返回 CodeSessionProcessNotOwned", t, func() {
		h, sessions := newSessionProcessHandlers(t, &fakeProcessScanner{killErr: procscan.ErrNotSessionProcess})
		sessions.EXPECT().Find(gomock.Any(), devicefp.Initiator(""), convID(8)).Return(&handlers.SessionRecord{PeerSessionID: convID(8)}, nil)

		_, err := h.Kill(context.Background(), &agentrewire.RuntimeKillSessionProcessRequest{ConversationId: convID(8), Pid: 4242, StartedAtMs: 1})

		requireRPCCode(t, err, rpcerror.CodeSessionProcessNotOwned)
	})

	convey.Convey("Given 这条会话不属于调用方,When 结束,Then 拒绝且一个信号都不发", t, func() {
		scanner := &fakeProcessScanner{}
		h, sessions := newSessionProcessHandlers(t, scanner)
		sessions.EXPECT().Find(gomock.Any(), devicefp.Initiator(""), convID(9)).Return(nil, nil)

		_, err := h.Kill(context.Background(), &agentrewire.RuntimeKillSessionProcessRequest{ConversationId: convID(9), Pid: 4242, StartedAtMs: 1})

		require.ErrorIs(t, err, rpcerror.ErrSessionNotFound)
		require.Empty(t, scanner.killCalls)
	})

	convey.Convey("Given 请求点名了另一台对端、而这条连接不是本机登录账号,When 结束,Then Unauthorized 且一个信号都不发", t, func() {
		scanner := &fakeProcessScanner{}
		h, _ := newSessionProcessHandlers(t, scanner)

		_, err := h.Kill(context.Background(), &agentrewire.RuntimeKillSessionProcessRequest{ConversationId: convID(8), PeerFingerprint: "fp-someone-else", Pid: 4242, StartedAtMs: 1})

		require.ErrorIs(t, err, rpcerror.ErrUnauthorized)
		require.Empty(t, scanner.killCalls)
	})

	convey.Convey("Given pid 为 0(protobuf 的缺省值,不是任何可结束的进程),When 结束,Then 以 InvalidParams 拒掉且不查库不发信号", t, func() {
		scanner := &fakeProcessScanner{}
		h, _ := newSessionProcessHandlers(t, scanner)

		_, err := h.Kill(context.Background(), &agentrewire.RuntimeKillSessionProcessRequest{ConversationId: convID(8), StartedAtMs: 1})

		requireRPCCode(t, err, rpcerror.CodeInvalidParams)
		require.Empty(t, scanner.killCalls)
	})

	convey.Convey("Given 发信号本身失败(例如权限不足),When 结束,Then 错误原样上抛,不冒充「不属于本会话」", t, func() {
		denied := errors.New("operation not permitted")
		h, sessions := newSessionProcessHandlers(t, &fakeProcessScanner{killErr: denied})
		sessions.EXPECT().Find(gomock.Any(), devicefp.Initiator(""), convID(8)).Return(&handlers.SessionRecord{PeerSessionID: convID(8)}, nil)

		_, err := h.Kill(context.Background(), &agentrewire.RuntimeKillSessionProcessRequest{ConversationId: convID(8), Pid: 4242, StartedAtMs: 1})

		require.ErrorIs(t, err, denied)
		var rpcErr *rpcerror.Error
		require.False(t, errors.As(err, &rpcErr))
	})
}

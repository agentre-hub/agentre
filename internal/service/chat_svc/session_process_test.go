package chat_svc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/mock_agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/procscan"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

type fakeProcScanner struct {
	listed  []procscan.Listed
	listErr error
	killErr error

	listConv string
	listN    int
	killConv string
	killPID  int
	killAt   int64
	killN    int
}

func (f *fakeProcScanner) List(_ context.Context, convID string) ([]procscan.Listed, error) {
	f.listN++
	f.listConv = convID
	return f.listed, f.listErr
}

func (f *fakeProcScanner) Kill(_ context.Context, convID string, pid int, startedAtMs int64) error {
	f.killN++
	f.killConv, f.killPID, f.killAt = convID, pid, startedAtMs
	return f.killErr
}

func sessionProcessSvc(t *testing.T, m *chatMocks, scanner *fakeProcScanner) chat_svc.SessionProcessSvc {
	t.Helper()
	chat_svc.RegisterSessionProcessScanner(scanner)
	t.Cleanup(func() { chat_svc.RegisterSessionProcessScanner(nil) })
	svc, ok := m.svc.(chat_svc.SessionProcessSvc)
	require.True(t, ok, "chatSvc 必须实现 SessionProcessSvc")
	return svc
}

// expectSessionOnBackend 放行「会话 → agent → backend」解析链。deviceFP 非空且不是本机指纹即远程。
func expectSessionOnBackend(m *chatMocks, sessionID int64, conv, deviceFP string) {
	m.session.EXPECT().Find(m.ctx, sessionID).Return(
		&chat_entity.Session{ID: sessionID, AgentID: 7, ConversationID: conv, Status: consts.ACTIVE}, nil)
	m.agent.EXPECT().Find(m.ctx, int64(7)).Return(
		&agent_entity.Agent{ID: 7, AgentBackendID: 3, Status: consts.ACTIVE}, nil)
	m.backend.EXPECT().Find(m.ctx, int64(3)).Return(
		&agent_backend_entity.AgentBackend{ID: 3, Type: string(agent_backend_entity.TypeClaudeCode),
			DeviceFingerprint: devicefp.Carrier(deviceFP), Status: consts.ACTIVE}, nil)
}

func codeOf(t *testing.T, err error) int {
	t.Helper()
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	return httpErr.Code
}

func TestListSessionProcesses(t *testing.T) {
	convey.Convey("Given 本机会话,When 列出,Then 按会话 conversationID 扫描并映射视图", t, func() {
		m := setupChatTest(t)
		sc := &fakeProcScanner{listed: []procscan.Listed{
			{PID: 10, Command: "node server.js", Ports: []uint16{3000, 3001}, StartedAtMs: 1700, Cwd: "/w"},
			{PID: 11, Command: "sleep 9"},
		}}
		svc := sessionProcessSvc(t, m, sc)
		expectSessionOnBackend(m, 42, "conv-42", "")

		resp, err := svc.ListSessionProcesses(m.ctx, &chat_svc.ListSessionProcessesRequest{SessionID: 42})

		require.NoError(t, err)
		assert.Equal(t, "conv-42", sc.listConv)
		require.Len(t, resp.Processes, 2)
		assert.Equal(t, chat_svc.SessionProcessView{PID: 10, Command: "node server.js", Ports: []int{3000, 3001}, StartedAt: 1700, Cwd: "/w"}, resp.Processes[0])
		assert.Empty(t, resp.Processes[1].Ports)
	})
	convey.Convey("Given 扫描失败(含平台不支持),When 列出,Then 返回空列表而不是错误", t, func() {
		for _, scanErr := range []error{errors.New("boom"), procscan.ErrUnsupported} {
			m := setupChatTest(t)
			svc := sessionProcessSvc(t, m, &fakeProcScanner{listErr: scanErr})
			expectSessionOnBackend(m, 42, "conv-42", "")

			resp, err := svc.ListSessionProcesses(m.ctx, &chat_svc.ListSessionProcessesRequest{SessionID: 42})

			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.Empty(t, resp.Processes)
		}
	})
	convey.Convey("Given 会话不存在,When 列出,Then 报 ChatSessionNotFound 且不扫描", t, func() {
		m := setupChatTest(t)
		sc := &fakeProcScanner{}
		svc := sessionProcessSvc(t, m, sc)
		m.session.EXPECT().Find(m.ctx, int64(42)).Return(nil, nil)

		resp, err := svc.ListSessionProcesses(m.ctx, &chat_svc.ListSessionProcessesRequest{SessionID: 42})

		assert.Nil(t, resp)
		assert.Equal(t, code.ChatSessionNotFound, codeOf(t, err))
		assert.Equal(t, 0, sc.listN)
	})
	convey.Convey("Given 远程会话,When 列出,Then 本机不扫描,经远端查询端按 conversationID 取并映射视图", t, func() {
		m := setupChatTest(t)
		sc := &fakeProcScanner{listed: []procscan.Listed{{PID: 1}}}
		svc := sessionProcessSvc(t, m, sc)
		insp, released := remoteInspector(t)
		expectSessionOnBackend(m, 42, "conv-42", "some-other-device-fp")
		insp.EXPECT().ListSessionProcesses(gomock.Any(), "conv-42").Return([]agentruntime.SessionProcess{
			{PID: 10, Command: "node", Ports: []int{3000}, StartedAtMs: 1700, Cwd: "/w"},
		}, nil)

		resp, err := svc.ListSessionProcesses(m.ctx, &chat_svc.ListSessionProcessesRequest{SessionID: 42})

		require.NoError(t, err)
		require.Len(t, resp.Processes, 1)
		assert.Equal(t, chat_svc.SessionProcessView{PID: 10, Command: "node", Ports: []int{3000}, StartedAt: 1700, Cwd: "/w"}, resp.Processes[0])
		assert.Equal(t, 0, sc.listN)
		assert.Equal(t, 1, *released, "借用要归还")
	})
	convey.Convey("Given 远程会话列出失败,When 列出,Then 返回空列表而不是错误", t, func() {
		m := setupChatTest(t)
		svc := sessionProcessSvc(t, m, &fakeProcScanner{})
		insp, released := remoteInspector(t)
		expectSessionOnBackend(m, 42, "conv-42", "some-other-device-fp")
		insp.EXPECT().ListSessionProcesses(gomock.Any(), "conv-42").Return(nil, errors.New("rpc down"))

		resp, err := svc.ListSessionProcesses(m.ctx, &chat_svc.ListSessionProcessesRequest{SessionID: 42})

		require.NoError(t, err)
		assert.Empty(t, resp.Processes)
		assert.Equal(t, 1, *released)
	})
	convey.Convey("Given 远程设备借不到(离线),When 列出,Then 返回空列表而不是错误", t, func() {
		m := setupChatTest(t)
		svc := sessionProcessSvc(t, m, &fakeProcScanner{})
		chat_svc.RegisterRemoteSessionProcessInspector(func(context.Context, *agent_backend_entity.AgentBackend) (agentruntime.SessionProcessInspector, func(), error) {
			return nil, func() {}, errors.New("dial failed")
		})
		t.Cleanup(func() { chat_svc.RegisterRemoteSessionProcessInspector(nil) })
		expectSessionOnBackend(m, 42, "conv-42", "some-other-device-fp")

		resp, err := svc.ListSessionProcesses(m.ctx, &chat_svc.ListSessionProcessesRequest{SessionID: 42})

		require.NoError(t, err)
		assert.Empty(t, resp.Processes)
	})
	convey.Convey("Given 无效参数,When 列出,Then InvalidParameter", t, func() {
		m := setupChatTest(t)
		svc := sessionProcessSvc(t, m, &fakeProcScanner{})
		_, err := svc.ListSessionProcesses(m.ctx, &chat_svc.ListSessionProcessesRequest{SessionID: 0})
		assert.Equal(t, code.InvalidParameter, codeOf(t, err))
	})
}

func TestKillSessionProcess(t *testing.T) {
	req := &chat_svc.KillSessionProcessRequest{SessionID: 42, PID: 10, StartedAt: 1700}
	convey.Convey("Given 本机会话,When 结束进程,Then 把 conversationID/pid/startedAt 交给扫描器", t, func() {
		m := setupChatTest(t)
		sc := &fakeProcScanner{}
		svc := sessionProcessSvc(t, m, sc)
		expectSessionOnBackend(m, 42, "conv-42", "")

		_, err := svc.KillSessionProcess(m.ctx, req)

		require.NoError(t, err)
		assert.Equal(t, "conv-42", sc.killConv)
		assert.Equal(t, 10, sc.killPID)
		assert.Equal(t, int64(1700), sc.killAt)
	})
	convey.Convey("Given 进程不属于本会话,When 结束,Then SessionProcessNotOwned", t, func() {
		m := setupChatTest(t)
		svc := sessionProcessSvc(t, m, &fakeProcScanner{killErr: procscan.ErrNotSessionProcess})
		expectSessionOnBackend(m, 42, "conv-42", "")

		_, err := svc.KillSessionProcess(m.ctx, req)

		assert.Equal(t, code.SessionProcessNotOwned, codeOf(t, err))
	})
	convey.Convey("Given 其它结束失败,When 结束,Then OperationFailed", t, func() {
		m := setupChatTest(t)
		svc := sessionProcessSvc(t, m, &fakeProcScanner{killErr: errors.New("EPERM")})
		expectSessionOnBackend(m, 42, "conv-42", "")

		_, err := svc.KillSessionProcess(m.ctx, req)

		assert.Equal(t, code.OperationFailed, codeOf(t, err))
	})
	convey.Convey("Given 远程会话,When 结束,Then 本机不动手,把 conversationID/pid/startedAt 交给远端查询端", t, func() {
		m := setupChatTest(t)
		sc := &fakeProcScanner{}
		svc := sessionProcessSvc(t, m, sc)
		insp, released := remoteInspector(t)
		expectSessionOnBackend(m, 42, "conv-42", "some-other-device-fp")
		insp.EXPECT().KillSessionProcess(gomock.Any(), "conv-42", 10, int64(1700)).Return(nil)

		_, err := svc.KillSessionProcess(m.ctx, &chat_svc.KillSessionProcessRequest{SessionID: 42, PID: 10, StartedAt: 1700})

		require.NoError(t, err)
		assert.Equal(t, 0, sc.killN)
		assert.Equal(t, 1, *released)
	})
	convey.Convey("Given 远程进程不属于本会话,When 结束,Then SessionProcessNotOwned", t, func() {
		m := setupChatTest(t)
		svc := sessionProcessSvc(t, m, &fakeProcScanner{})
		insp, _ := remoteInspector(t)
		expectSessionOnBackend(m, 42, "conv-42", "some-other-device-fp")
		insp.EXPECT().KillSessionProcess(gomock.Any(), "conv-42", 10, int64(1700)).Return(agentruntime.ErrSessionProcessNotOwned)

		_, err := svc.KillSessionProcess(m.ctx, &chat_svc.KillSessionProcessRequest{SessionID: 42, PID: 10, StartedAt: 1700})

		assert.Equal(t, code.SessionProcessNotOwned, codeOf(t, err))
	})
	convey.Convey("Given 远程其它结束失败,When 结束,Then OperationFailed", t, func() {
		m := setupChatTest(t)
		svc := sessionProcessSvc(t, m, &fakeProcScanner{})
		insp, _ := remoteInspector(t)
		expectSessionOnBackend(m, 42, "conv-42", "some-other-device-fp")
		insp.EXPECT().KillSessionProcess(gomock.Any(), "conv-42", 10, int64(1700)).Return(errors.New("rpc down"))

		_, err := svc.KillSessionProcess(m.ctx, &chat_svc.KillSessionProcessRequest{SessionID: 42, PID: 10, StartedAt: 1700})

		assert.Equal(t, code.OperationFailed, codeOf(t, err))
	})
}

// remoteInspector 装一个生成的远端查询端 mock 当作「会话所在设备」，并统计借用被归还的次数。
func remoteInspector(t *testing.T) (*mock_agentruntime.MockSessionProcessInspector, *int) {
	t.Helper()
	insp := mock_agentruntime.NewMockSessionProcessInspector(gomock.NewController(t))
	released := new(int)
	chat_svc.RegisterRemoteSessionProcessInspector(func(context.Context, *agent_backend_entity.AgentBackend) (agentruntime.SessionProcessInspector, func(), error) {
		return insp, func() { *released++ }, nil
	})
	t.Cleanup(func() { chat_svc.RegisterRemoteSessionProcessInspector(nil) })
	return insp, released
}

// expectSessionOnDevice 同 expectSessionOnBackend,但会话行带着执行设备 execDeviceID。
func expectSessionOnDevice(m *chatMocks, sessionID int64, deviceFP string, execDeviceID int64) {
	m.session.EXPECT().Find(m.ctx, sessionID).Return(
		&chat_entity.Session{ID: sessionID, AgentID: 7, ConversationID: "c", ExecDeviceID: execDeviceID, Status: consts.ACTIVE}, nil)
	m.agent.EXPECT().Find(m.ctx, int64(7)).Return(
		&agent_entity.Agent{ID: 7, AgentBackendID: 3, Status: consts.ACTIVE}, nil)
	m.backend.EXPECT().Find(m.ctx, int64(3)).Return(
		&agent_backend_entity.AgentBackend{ID: 3, Type: string(agent_backend_entity.TypeClaudeCode),
			DeviceFingerprint: devicefp.Carrier(deviceFP), Status: consts.ACTIVE}, nil)
}

func TestSessionExecDevice(t *testing.T) {
	convey.Convey("Given 本机会话,When 询问执行设备,Then 0(本机)", t, func() {
		m := setupChatTest(t)
		svc := sessionProcessSvc(t, m, &fakeProcScanner{})
		expectSessionOnDevice(m, 42, "", 0)
		deviceID, err := svc.SessionExecDevice(m.ctx, 42)
		require.NoError(t, err)
		assert.Equal(t, int64(0), deviceID)
	})
	convey.Convey("Given 远程会话且记录了执行设备,When 询问,Then 交回该设备", t, func() {
		m := setupChatTest(t)
		svc := sessionProcessSvc(t, m, &fakeProcScanner{})
		expectSessionOnDevice(m, 43, "other-fp", 7)
		deviceID, err := svc.SessionExecDevice(m.ctx, 43)
		require.NoError(t, err)
		assert.Equal(t, int64(7), deviceID)
	})
	convey.Convey("Given 远程会话但没有记录执行设备,When 询问,Then RemoteDeviceNotFound,而不是交回设备 0 当本机", t, func() {
		m := setupChatTest(t)
		svc := sessionProcessSvc(t, m, &fakeProcScanner{})
		expectSessionOnDevice(m, 44, "other-fp", 0)
		_, err := svc.SessionExecDevice(m.ctx, 44)
		assert.Equal(t, code.RemoteDeviceNotFound, codeOf(t, err))
	})
}

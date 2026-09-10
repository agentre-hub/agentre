package orgtool_svc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/service/agent_svc"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/department_svc"
	"github.com/agentre-hub/agentre/internal/service/orgtool_svc/mock_orgtool_svc"
)

// writeSvc 构造一个全新的 orgtoolSvc(不碰 Default() 单例),接齐写工具审批所需的全部 5
// 个依赖。approvalTimeout 默认给 4min,需要超时用例的测试自己改小。
type writeDeps struct {
	lookup *mock_orgtool_svc.MockAgentLookup
	query  *mock_orgtool_svc.MockOrgQuery
	dept   *mock_orgtool_svc.MockDeptCommand
	agent  *mock_orgtool_svc.MockAgentCommand
	apv    *mock_orgtool_svc.MockApprovalGateway
}

func newWriteSvc(ctrl *gomock.Controller) (*orgtoolSvc, *writeDeps) {
	d := &writeDeps{
		lookup: mock_orgtool_svc.NewMockAgentLookup(ctrl),
		query:  mock_orgtool_svc.NewMockOrgQuery(ctrl),
		dept:   mock_orgtool_svc.NewMockDeptCommand(ctrl),
		agent:  mock_orgtool_svc.NewMockAgentCommand(ctrl),
		apv:    mock_orgtool_svc.NewMockApprovalGateway(ctrl),
	}
	s := &orgtoolSvc{approvalTimeout: 4 * time.Minute}
	s.RegisterDeps(d.query, d.dept, d.agent, d.lookup, d.apv)
	return s, d
}

// beginCh 让 BeginToolApproval mock 返回测试持有的审批 channel(buffered=1)——往里 push
// true/false 即模拟前端经 chat_svc.AnswerToolApproval 唤醒(waiter 现归 chat_svc 持有,
// orgtool 只 select 这个返回的 channel)。不 push 则触发超时分支。
func beginCh(d *writeDeps, sessionID int64) chan bool {
	apvCh := make(chan bool, 1)
	d.apv.EXPECT().
		BeginToolApproval(gomock.Any(), sessionID, gomock.Any()).
		Return((<-chan bool)(apvCh), nil)
	return apvCh
}

// callWrite 在 goroutine 里发一次写工具 tools/call(handler 会同步挂起),返回 recorder
// 与一个 done channel(请求返回时关闭)。
func callWrite(s *orgtoolSvc, body, token string) (*httptest.ResponseRecorder, <-chan struct{}) {
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		req := httptest.NewRequest("POST", "/mcp/org/", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		s.MCPHandler().ServeHTTP(w, req)
		close(done)
	}()
	return w, done
}

func TestOrgApproval_ApprovedExecutes(t *testing.T) {
	Convey("写工具挂起 → 应答 allow=true → exec 成功 → result 含成功文案;Begin(带 ToolKey=org)/Finish(approved) 均调到", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, d := newWriteSvc(ctrl)
		d.lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(orgEnabledAgent(7), nil)

		// 内联 Begin mock 以捕获 blk.ToolKey(So 不能跨 handler goroutine,mutex 保护后断言)。
		apvCh := make(chan bool, 1)
		var mu sync.Mutex
		var gotToolKey string
		d.apv.EXPECT().BeginToolApproval(gomock.Any(), int64(99), gomock.Any()).
			DoAndReturn(func(_ any, _ int64, blk *blocks.ToolApprovalBlock) (<-chan bool, error) {
				mu.Lock()
				gotToolKey = blk.ToolKey
				mu.Unlock()
				return apvCh, nil
			})
		d.dept.EXPECT().Create(gomock.Any(), gomock.Any()).Return(
			&department_svc.CreateDepartmentResponse{Item: &department_svc.DepartmentItem{ID: 7, Name: "市场部"}}, nil)
		d.apv.EXPECT().FinishToolApproval(gomock.Any(), int64(99), gomock.Any(), "approved", gomock.Any()).Return(nil)

		token := s.mcpHandlerInit().MintToken(7, 99)
		w, done := callWrite(s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_create_department","arguments":{"name":"市场部"}}}`, token)

		apvCh <- true
		<-done

		mu.Lock()
		So(gotToolKey, ShouldEqual, "org")
		mu.Unlock()
		So(w.Code, ShouldEqual, http.StatusOK)
		So(w.Body.String(), ShouldContainSubstring, "市场部")
		So(w.Body.String(), ShouldContainSubstring, "id=7")
	})
}

func TestOrgApproval_ApprovedButExecError(t *testing.T) {
	Convey("批准但 exec 业务错 → Finish(approved, 执行失败...) + result 含「已批准但执行失败」", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, d := newWriteSvc(ctrl)
		d.lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(orgEnabledAgent(7), nil)

		apvCh := beginCh(d, 99)
		d.dept.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, assertErr("循环挂载"))
		// So() 不能在 handler goroutine 的 mock 回调里跑(goconvey gls 不跨协程)——
		// 回调只做 mutex 保护的值捕获,<-done 后在主 goroutine 上断言。
		var mu sync.Mutex
		var finishResult string
		d.apv.EXPECT().FinishToolApproval(gomock.Any(), int64(99), gomock.Any(), "approved", gomock.Any()).
			DoAndReturn(func(_ any, _ int64, _, _, result string) error {
				mu.Lock()
				finishResult = result
				mu.Unlock()
				return nil
			})

		token := s.mcpHandlerInit().MintToken(7, 99)
		w, done := callWrite(s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_create_department","arguments":{"name":"x"}}}`, token)

		apvCh <- true
		<-done

		mu.Lock()
		defer mu.Unlock()
		So(finishResult, ShouldContainSubstring, "执行失败")
		So(w.Code, ShouldEqual, http.StatusOK)
		So(w.Body.String(), ShouldContainSubstring, "已批准但执行失败")
	})
}

func TestOrgApproval_Denied(t *testing.T) {
	Convey("应答 allow=false → Finish(denied) + result 含「用户拒绝」;不 exec", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, d := newWriteSvc(ctrl)
		d.lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(orgEnabledAgent(7), nil)

		apvCh := beginCh(d, 99)
		// 无 dept.Create EXPECT:拒绝不该执行
		d.apv.EXPECT().FinishToolApproval(gomock.Any(), int64(99), gomock.Any(), "denied", gomock.Any()).Return(nil)

		token := s.mcpHandlerInit().MintToken(7, 99)
		w, done := callWrite(s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_create_department","arguments":{"name":"x"}}}`, token)

		apvCh <- false
		<-done

		So(w.Code, ShouldEqual, http.StatusOK)
		So(w.Body.String(), ShouldContainSubstring, "用户拒绝")
	})
}

func TestOrgApproval_Timeout(t *testing.T) {
	Convey("不应答 → 超时 → Finish(expired) + result 含「审批超时」", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, d := newWriteSvc(ctrl)
		s.approvalTimeout = 50 * time.Millisecond
		d.lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(orgEnabledAgent(7), nil)

		beginCh(d, 99) // 返回 channel 但故意不 push,触发超时分支
		d.apv.EXPECT().FinishToolApproval(gomock.Any(), int64(99), gomock.Any(), "expired", gomock.Any()).Return(nil)

		token := s.mcpHandlerInit().MintToken(7, 99)
		w, done := callWrite(s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_create_department","arguments":{"name":"x"}}}`, token)

		<-done

		So(w.Code, ShouldEqual, http.StatusOK)
		So(w.Body.String(), ShouldContainSubstring, "审批超时")
	})
}

func TestOrgApproval_BeginFails(t *testing.T) {
	Convey("BeginToolApproval 报错(无活跃 turn)→ rpc error「审批通道不可用」,无挂起", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, d := newWriteSvc(ctrl)
		d.lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(orgEnabledAgent(7), nil)
		d.apv.EXPECT().BeginToolApproval(gomock.Any(), int64(99), gomock.Any()).Return((<-chan bool)(nil), assertErr("no active turn"))
		// 无 Finish / 无 exec EXPECT

		token := s.mcpHandlerInit().MintToken(7, 99)
		req := httptest.NewRequest("POST", "/mcp/org/", strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_create_department","arguments":{"name":"x"}}}`))
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.MCPHandler().ServeHTTP(w, req)

		So(w.Code, ShouldEqual, http.StatusOK)
		So(w.Body.String(), ShouldContainSubstring, "审批通道不可用")
		So(w.Body.String(), ShouldContainSubstring, "error")
	})
}

func TestOrgApproval_UpdateDepartmentMove(t *testing.T) {
	Convey("org_update_department 带 parentId 变化 → 先 Update 后 Move(两者参数断言)", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, d := newWriteSvc(ctrl)
		d.lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(orgEnabledAgent(7), nil)

		// Load 找现值:部门 5 现 parentId=2,改成 9 应触发 Move。
		d.query.EXPECT().Load(gomock.Any(), gomock.Any()).Return(&department_svc.LoadOrgResponse{
			Departments: []*department_svc.DepartmentItem{
				{ID: 5, Name: "旧名", Description: "旧述", Icon: "i", AccentColor: "c", ParentID: 2, LeadAgentID: 3},
			},
		}, nil)
		apvCh := beginCh(d, 99)

		// So() 不能在 handler goroutine 的 mock 回调里跑(goconvey gls 不跨协程)——
		// 回调只做 mutex 保护的值捕获,<-done 后在主 goroutine 上断言。
		var seq []string
		var mu sync.Mutex
		var upReq *department_svc.UpdateDepartmentRequest
		var mvReq *department_svc.MoveDepartmentRequest
		d.dept.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, req *department_svc.UpdateDepartmentRequest) (*department_svc.UpdateDepartmentResponse, error) {
				mu.Lock()
				seq = append(seq, "update")
				upReq = req
				mu.Unlock()
				return &department_svc.UpdateDepartmentResponse{Item: &department_svc.DepartmentItem{ID: 5, Name: "新名"}}, nil
			})
		d.dept.EXPECT().Move(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, req *department_svc.MoveDepartmentRequest) (*department_svc.MoveDepartmentResponse, error) {
				mu.Lock()
				seq = append(seq, "move")
				mvReq = req
				mu.Unlock()
				return &department_svc.MoveDepartmentResponse{}, nil
			})
		d.apv.EXPECT().FinishToolApproval(gomock.Any(), int64(99), gomock.Any(), "approved", gomock.Any()).Return(nil)

		token := s.mcpHandlerInit().MintToken(7, 99)
		w, done := callWrite(s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_update_department","arguments":{"id":5,"name":"新名","parentId":9}}}`, token)
		apvCh <- true
		<-done

		So(w.Code, ShouldEqual, http.StatusOK)
		mu.Lock()
		defer mu.Unlock()
		So(seq, ShouldResemble, []string{"update", "move"})
		So(upReq.ID, ShouldEqual, 5)
		So(upReq.Name, ShouldEqual, "新名")        // name 显式给了
		So(upReq.Description, ShouldEqual, "旧述") // 没给 → 沿用现值
		So(upReq.Icon, ShouldEqual, "i")         // 透传现值
		So(upReq.AccentColor, ShouldEqual, "c")
		So(mvReq.ID, ShouldEqual, 5)
		So(mvReq.NewParentID, ShouldEqual, 9)
	})
}

func TestOrgApproval_CreateAgentInheritsBackend(t *testing.T) {
	Convey("org_create_agent BackendID=0 → Find(ref.agentID) 取调用者 backend → Create 收到其 AgentBackendID", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, d := newWriteSvc(ctrl)
		caller := orgEnabledAgent(7)
		caller.AgentBackendID = 42
		// 一次 Find 用于开关校验,一次用于继承 backend —— 都返回 caller。
		d.lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(caller, nil).Times(2)

		apvCh := beginCh(d, 99)
		// So() 不能在 handler goroutine 的 mock 回调里跑(goconvey gls 不跨协程)——
		// 回调只做 mutex 保护的值捕获,<-done 后在主 goroutine 上断言。
		var mu sync.Mutex
		var createReq *agent_svc.CreateAgentRequest
		d.agent.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, req *agent_svc.CreateAgentRequest) (*agent_svc.CreateAgentResponse, error) {
				mu.Lock()
				createReq = req
				mu.Unlock()
				return &agent_svc.CreateAgentResponse{Item: &department_svc.AgentItem{ID: 11, Name: "新人"}}, nil
			})
		d.apv.EXPECT().FinishToolApproval(gomock.Any(), int64(99), gomock.Any(), "approved", gomock.Any()).Return(nil)

		token := s.mcpHandlerInit().MintToken(7, 99)
		w, done := callWrite(s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"org_create_agent","arguments":{"name":"新人","departmentId":1}}}`, token)
		apvCh <- true
		<-done

		mu.Lock()
		defer mu.Unlock()
		So(createReq.AgentBackendID, ShouldEqual, 42)
		So(createReq.Name, ShouldEqual, "新人")
		So(w.Code, ShouldEqual, http.StatusOK)
		So(w.Body.String(), ShouldContainSubstring, "新人")
	})
}

// assertErr 是测试用的简单 error。
type assertErr string

func (e assertErr) Error() string { return string(e) }

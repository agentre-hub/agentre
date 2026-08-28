package hooktool_svc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/hook_svc"
	"github.com/agentre-hub/agentre/internal/service/hooktool_svc/mock_hooktool_svc"
)

// writeDeps 接齐写工具审批所需依赖(lookup + hooks + approval)。
type writeDeps struct {
	lookup *mock_hooktool_svc.MockAgentLookup
	hooks  *mock_hooktool_svc.MockHookService
	apv    *mock_hooktool_svc.MockApprovalGateway
}

func newWriteSvc(ctrl *gomock.Controller) (*hooktoolSvc, *writeDeps) {
	d := &writeDeps{
		lookup: mock_hooktool_svc.NewMockAgentLookup(ctrl),
		hooks:  mock_hooktool_svc.NewMockHookService(ctrl),
		apv:    mock_hooktool_svc.NewMockApprovalGateway(ctrl),
	}
	s := &hooktoolSvc{approvalTimeout: 4 * time.Minute}
	s.RegisterDeps(d.hooks, d.lookup, d.apv)
	return s, d
}

// callWrite 在 goroutine 里发一次写工具 tools/call(handler 会同步挂起),返回 recorder + done。
func callWrite(s *hooktoolSvc, body, token string) (*httptest.ResponseRecorder, <-chan struct{}) {
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		req := httptest.NewRequest("POST", "/mcp/hook/", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		s.MCPHandler().ServeHTTP(w, req)
		close(done)
	}()
	return w, done
}

func TestHookApproval_CreateApproved(t *testing.T) {
	Convey("hook_create 挂起 → allow=true → CreateHook 执行,审批卡 ToolKey=hook", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, d := newWriteSvc(ctrl)
		d.lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(hookEnabledAgent(7), nil)
		apvCh := make(chan bool, 1)
		var mu sync.Mutex
		var gotKey, gotName string
		d.apv.EXPECT().BeginToolApproval(gomock.Any(), int64(99), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ int64, blk *blocks.ToolApprovalBlock) (<-chan bool, error) {
				mu.Lock()
				gotKey, gotName = blk.ToolKey, blk.ToolName
				mu.Unlock()
				return apvCh, nil
			})
		d.hooks.EXPECT().CreateHook(gomock.Any(), gomock.Any()).Return(&hook_svc.HookItem{ID: 5, Name: "巡检"}, nil)
		d.apv.EXPECT().FinishToolApproval(gomock.Any(), int64(99), gomock.Any(), "approved", gomock.Any()).Return(nil)

		token := s.mcpHandlerInit().MintToken(7, 99)
		w, done := callWrite(s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hook_create","arguments":{"name":"巡检","interpreter":"bash","command":"echo {}","scheduleExpr":"*/5 * * * *"}}}`, token)
		apvCh <- true
		<-done

		mu.Lock()
		So(gotKey, ShouldEqual, "hook")
		So(gotName, ShouldEqual, "hook_create")
		mu.Unlock()
		So(w.Code, ShouldEqual, http.StatusOK)
		So(w.Body.String(), ShouldContainSubstring, "巡检")
	})
}

func TestHookApproval_Denied(t *testing.T) {
	Convey("allow=false → 不调 CreateHook,回拒绝文案", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, d := newWriteSvc(ctrl)
		d.lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(hookEnabledAgent(7), nil)
		apvCh := make(chan bool, 1)
		d.apv.EXPECT().BeginToolApproval(gomock.Any(), int64(99), gomock.Any()).Return((<-chan bool)(apvCh), nil)
		d.apv.EXPECT().FinishToolApproval(gomock.Any(), int64(99), gomock.Any(), "denied", "").Return(nil)
		// 不 EXPECT CreateHook → 若被调用即 ctrl.Finish 报错

		token := s.mcpHandlerInit().MintToken(7, 99)
		w, done := callWrite(s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hook_create","arguments":{"name":"x","interpreter":"bash","command":"echo {}","scheduleExpr":"* * * * *"}}}`, token)
		apvCh <- false
		<-done
		So(w.Code, ShouldEqual, http.StatusOK)
		So(w.Body.String(), ShouldContainSubstring, "拒绝")
	})
}

func TestHookApproval_CreateApprovedButExecFails(t *testing.T) {
	Convey("allow=true 但 CreateHook 业务错(如重名)→ approved 终态 + 错误进 Result", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, d := newWriteSvc(ctrl)
		d.lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(hookEnabledAgent(7), nil)
		apvCh := make(chan bool, 1)
		d.apv.EXPECT().BeginToolApproval(gomock.Any(), int64(99), gomock.Any()).Return((<-chan bool)(apvCh), nil)
		d.hooks.EXPECT().CreateHook(gomock.Any(), gomock.Any()).Return(nil, context.DeadlineExceeded)
		d.apv.EXPECT().FinishToolApproval(gomock.Any(), int64(99), gomock.Any(), "approved", gomock.Any()).Return(nil)

		token := s.mcpHandlerInit().MintToken(7, 99)
		w, done := callWrite(s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hook_create","arguments":{"name":"x","interpreter":"bash","command":"echo {}","scheduleExpr":"* * * * *"}}}`, token)
		apvCh <- true
		<-done
		So(w.Code, ShouldEqual, http.StatusOK)
		So(w.Body.String(), ShouldContainSubstring, "执行失败")
	})
}

func TestHookApproval_UpdateMergesUnsetFields(t *testing.T) {
	Convey("hook_update 只传 command → 先 Load 现值,未传字段沿用,env 沿用现值(密钥占位)", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, d := newWriteSvc(ctrl)
		d.lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(hookEnabledAgent(7), nil)
		apvCh := make(chan bool, 1)
		d.apv.EXPECT().BeginToolApproval(gomock.Any(), int64(99), gomock.Any()).Return((<-chan bool)(apvCh), nil)
		// loadHook → Load(HookID:3)
		d.hooks.EXPECT().Load(gomock.Any(), &hook_svc.LoadHooksRequest{HookID: 3}).Return(&hook_svc.LoadHooksResponse{
			Hooks: []*hook_svc.HookItem{{
				ID: 3, Name: "巡检", Interpreter: "bash", Command: "old", ScheduleExpr: "*/5 * * * *",
				Timezone: "Asia/Shanghai", Enabled: true,
				Env: []hook_svc.EnvVar{{Key: "TOKEN", Value: "********", Secret: true}},
			}},
		}, nil)
		// UpdateHook 收到 merge 后的 req:command 改了,其余沿用现值。
		var mu sync.Mutex
		var got *hook_svc.UpdateHookRequest
		d.hooks.EXPECT().UpdateHook(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, req *hook_svc.UpdateHookRequest) (*hook_svc.HookItem, error) {
				mu.Lock()
				got = req
				mu.Unlock()
				return &hook_svc.HookItem{ID: 3, Name: req.Name}, nil
			})
		d.apv.EXPECT().FinishToolApproval(gomock.Any(), int64(99), gomock.Any(), "approved", gomock.Any()).Return(nil)

		token := s.mcpHandlerInit().MintToken(7, 99)
		w, done := callWrite(s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hook_update","arguments":{"id":3,"command":"new"}}}`, token)
		apvCh <- true
		<-done
		So(w.Code, ShouldEqual, http.StatusOK)

		mu.Lock()
		defer mu.Unlock()
		So(got.ID, ShouldEqual, 3)
		So(got.Command, ShouldEqual, "new")
		So(got.Name, ShouldEqual, "巡检")                  // 未传 → 沿用
		So(got.ScheduleExpr, ShouldEqual, "*/5 * * * *") // 未传 → 沿用
		So(got.Enabled, ShouldBeTrue)
		So(len(got.Env), ShouldEqual, 1)
		So(got.Env[0].Value, ShouldEqual, "********") // 密钥占位回传,由 hook_svc 保留
	})
}

func TestHookApproval_RunDryApproved(t *testing.T) {
	Convey("hook_run dryRun 默认 true → RunHook(DryRun=true) → 回 RunHookResult JSON", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, d := newWriteSvc(ctrl)
		d.lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(hookEnabledAgent(7), nil)
		apvCh := make(chan bool, 1)
		d.apv.EXPECT().BeginToolApproval(gomock.Any(), int64(99), gomock.Any()).Return((<-chan bool)(apvCh), nil)
		d.hooks.EXPECT().RunHook(gomock.Any(), &hook_svc.RunHookRequest{ID: 3, DryRun: true}).
			Return(&hook_svc.RunHookResult{ExitCode: 0, Persisted: false, NewCount: 2}, nil)
		d.apv.EXPECT().FinishToolApproval(gomock.Any(), int64(99), gomock.Any(), "approved", gomock.Any()).Return(nil)

		token := s.mcpHandlerInit().MintToken(7, 99)
		w, done := callWrite(s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hook_run","arguments":{"id":3}}}`, token)
		apvCh <- true
		<-done
		So(w.Code, ShouldEqual, http.StatusOK)
		So(w.Body.String(), ShouldContainSubstring, "newCount")
	})

	Convey("hook_run dryRun=false → RunHook(DryRun=false)", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, d := newWriteSvc(ctrl)
		d.lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(hookEnabledAgent(7), nil)
		apvCh := make(chan bool, 1)
		d.apv.EXPECT().BeginToolApproval(gomock.Any(), int64(99), gomock.Any()).Return((<-chan bool)(apvCh), nil)
		d.hooks.EXPECT().RunHook(gomock.Any(), &hook_svc.RunHookRequest{ID: 3, DryRun: false}).
			Return(&hook_svc.RunHookResult{ExitCode: 0, Persisted: true, NewCount: 1}, nil)
		d.apv.EXPECT().FinishToolApproval(gomock.Any(), int64(99), gomock.Any(), "approved", gomock.Any()).Return(nil)

		token := s.mcpHandlerInit().MintToken(7, 99)
		w, done := callWrite(s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hook_run","arguments":{"id":3,"dryRun":false}}}`, token)
		apvCh <- true
		<-done
		So(w.Code, ShouldEqual, http.StatusOK)
		So(w.Body.String(), ShouldContainSubstring, "persisted")
	})
}

func TestHookApproval_CreateWithInterpreterPath(t *testing.T) {
	Convey("hook_create 带 interpreterPath → CreateHookRequest 透传该字段", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, d := newWriteSvc(ctrl)
		d.lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(hookEnabledAgent(7), nil)
		apvCh := make(chan bool, 1)
		d.apv.EXPECT().BeginToolApproval(gomock.Any(), int64(99), gomock.Any()).Return((<-chan bool)(apvCh), nil)
		var mu sync.Mutex
		var gotReq *hook_svc.CreateHookRequest
		d.hooks.EXPECT().CreateHook(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, req *hook_svc.CreateHookRequest) (*hook_svc.HookItem, error) {
				mu.Lock()
				gotReq = req
				mu.Unlock()
				return &hook_svc.HookItem{ID: 9, Name: req.Name}, nil
			})
		d.apv.EXPECT().FinishToolApproval(gomock.Any(), int64(99), gomock.Any(), "approved", gomock.Any()).Return(nil)

		token := s.mcpHandlerInit().MintToken(7, 99)
		w, done := callWrite(s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hook_create","arguments":{"name":"自定义解释器","interpreter":"python","interpreterPath":"/opt/homebrew/bin/python3","command":"echo {}","scheduleExpr":"*/10 * * * *"}}}`, token)
		apvCh <- true
		<-done
		So(w.Code, ShouldEqual, http.StatusOK)

		mu.Lock()
		defer mu.Unlock()
		So(gotReq.InterpreterPath, ShouldEqual, "/opt/homebrew/bin/python3")
		So(gotReq.Interpreter, ShouldEqual, "python")
	})
}

func TestHookApproval_UpdateInterpreterPath(t *testing.T) {
	Convey("hook_update 传 interpreterPath → 更新该字段;不传则沿用现值", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, d := newWriteSvc(ctrl)
		d.lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(hookEnabledAgent(7), nil)
		apvCh := make(chan bool, 1)
		d.apv.EXPECT().BeginToolApproval(gomock.Any(), int64(99), gomock.Any()).Return((<-chan bool)(apvCh), nil)
		d.hooks.EXPECT().Load(gomock.Any(), &hook_svc.LoadHooksRequest{HookID: 5}).Return(&hook_svc.LoadHooksResponse{
			Hooks: []*hook_svc.HookItem{{
				ID: 5, Name: "巡检2", Interpreter: "python", InterpreterPath: "/usr/bin/python3",
				Command: "echo {}", ScheduleExpr: "*/5 * * * *", Timezone: "Asia/Shanghai", Enabled: true,
			}},
		}, nil)
		var mu sync.Mutex
		var gotReq *hook_svc.UpdateHookRequest
		d.hooks.EXPECT().UpdateHook(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, req *hook_svc.UpdateHookRequest) (*hook_svc.HookItem, error) {
				mu.Lock()
				gotReq = req
				mu.Unlock()
				return &hook_svc.HookItem{ID: 5, Name: req.Name}, nil
			})
		d.apv.EXPECT().FinishToolApproval(gomock.Any(), int64(99), gomock.Any(), "approved", gomock.Any()).Return(nil)

		token := s.mcpHandlerInit().MintToken(7, 99)
		// 传新的 interpreterPath
		w, done := callWrite(s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hook_update","arguments":{"id":5,"interpreterPath":"/opt/homebrew/bin/python3.12"}}}`, token)
		apvCh <- true
		<-done
		So(w.Code, ShouldEqual, http.StatusOK)

		mu.Lock()
		defer mu.Unlock()
		So(gotReq.InterpreterPath, ShouldEqual, "/opt/homebrew/bin/python3.12") // 已更新
		So(gotReq.Interpreter, ShouldEqual, "python")                           // 未传 → 沿用
	})

	Convey("hook_update 不传 interpreterPath → 沿用现值", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, d := newWriteSvc(ctrl)
		d.lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(hookEnabledAgent(7), nil)
		apvCh := make(chan bool, 1)
		d.apv.EXPECT().BeginToolApproval(gomock.Any(), int64(99), gomock.Any()).Return((<-chan bool)(apvCh), nil)
		d.hooks.EXPECT().Load(gomock.Any(), &hook_svc.LoadHooksRequest{HookID: 5}).Return(&hook_svc.LoadHooksResponse{
			Hooks: []*hook_svc.HookItem{{
				ID: 5, Name: "巡检2", Interpreter: "python", InterpreterPath: "/usr/bin/python3",
				Command: "echo {}", ScheduleExpr: "*/5 * * * *", Timezone: "Asia/Shanghai", Enabled: true,
			}},
		}, nil)
		var mu sync.Mutex
		var gotReq *hook_svc.UpdateHookRequest
		d.hooks.EXPECT().UpdateHook(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, req *hook_svc.UpdateHookRequest) (*hook_svc.HookItem, error) {
				mu.Lock()
				gotReq = req
				mu.Unlock()
				return &hook_svc.HookItem{ID: 5, Name: req.Name}, nil
			})
		d.apv.EXPECT().FinishToolApproval(gomock.Any(), int64(99), gomock.Any(), "approved", gomock.Any()).Return(nil)

		token := s.mcpHandlerInit().MintToken(7, 99)
		// 不传 interpreterPath,只改 command
		w, done := callWrite(s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hook_update","arguments":{"id":5,"command":"new echo {}"}}}`, token)
		apvCh <- true
		<-done
		So(w.Code, ShouldEqual, http.StatusOK)

		mu.Lock()
		defer mu.Unlock()
		So(gotReq.InterpreterPath, ShouldEqual, "/usr/bin/python3") // 沿用现值
		So(gotReq.Command, ShouldEqual, "new echo {}")              // 已更新
	})
}

func TestHookApproval_DeleteApproved(t *testing.T) {
	Convey("hook_delete allow=true → DeleteHook 执行", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		s, d := newWriteSvc(ctrl)
		d.lookup.EXPECT().Find(gomock.Any(), int64(7)).Return(hookEnabledAgent(7), nil)
		apvCh := make(chan bool, 1)
		d.apv.EXPECT().BeginToolApproval(gomock.Any(), int64(99), gomock.Any()).Return((<-chan bool)(apvCh), nil)
		d.hooks.EXPECT().DeleteHook(gomock.Any(), int64(3)).Return(nil)
		d.apv.EXPECT().FinishToolApproval(gomock.Any(), int64(99), gomock.Any(), "approved", gomock.Any()).Return(nil)

		token := s.mcpHandlerInit().MintToken(7, 99)
		w, done := callWrite(s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hook_delete","arguments":{"id":3}}}`, token)
		apvCh <- true
		<-done
		So(w.Code, ShouldEqual, http.StatusOK)
		So(w.Body.String(), ShouldContainSubstring, "已删除")
	})
}

package server_svc_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// reporter 取出设备情况上报口:它不在 ServerSvc 上(见 server_svc.DeviceFactsReporter)。
func reporter(svc server_svc.ServerSvc) server_svc.DeviceFactsReporter {
	return svc.(server_svc.DeviceFactsReporter)
}

func TestReportDeviceFacts(t *testing.T) {
	report := &syncwire.DeviceFactsReport{
		CollectedAt: 1700,
		Runtimes:    []syncwire.RuntimeFact{{Runtime: "claudecode", Found: true, Version: "2.1.0", NativeLogin: syncwire.NativeLoginLoggedIn}},
		LocalPaths:  []syncwire.LocalPathItem{{ProjectSyncID: "p-1", Path: "/work/p1"}},
	}

	Convey("ReportDeviceFacts 把这台桌面端的情况整份 POST 到设备情况端点", t, func() {
		var gotPath, gotMethod string
		var got syncwire.DeviceFactsReport
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath, gotMethod = r.URL.Path, r.Method
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &got)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{}}`))
		}))
		defer srv.Close()

		svc, mRepo, _ := setupServerSvc(t, srv.URL)
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInState(srv.URL), nil)

		So(reporter(svc).ReportDeviceFacts(context.Background(), report), ShouldBeNil)
		So(gotMethod, ShouldEqual, http.MethodPost)
		So(gotPath, ShouldEqual, syncwire.PathDeviceFacts)
		So(got, ShouldResemble, *report)
	})

	Convey("server 还没有这个端点(404)时返回错误,由调用方下次重试", t, func() {
		srv := httptest.NewServer(http.NotFoundHandler())
		defer srv.Close()

		svc, mRepo, _ := setupServerSvc(t, srv.URL)
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInState(srv.URL), nil).AnyTimes()

		So(reporter(svc).ReportDeviceFacts(context.Background(), report), ShouldNotBeNil)
	})

	Convey("未登录时一个网络请求都不发", t, func() {
		called := false
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
		defer srv.Close()

		svc, mRepo, _ := setupServerSvc(t, srv.URL)
		mRepo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{ID: 1}, nil)

		So(reporter(svc).ReportDeviceFacts(context.Background(), report), ShouldNotBeNil)
		So(called, ShouldBeFalse)
	})
}

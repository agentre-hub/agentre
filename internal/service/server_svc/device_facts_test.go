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

// factsReader 取出设备情况读取口:与上报口一样不在 ServerSvc 上。
func factsReader(svc server_svc.ServerSvc) server_svc.DeviceFactsReader {
	return svc.(server_svc.DeviceFactsReader)
}

func TestListDeviceFacts(t *testing.T) {
	Convey("ListDeviceFacts GET 账号下全部设备的最后一次上报", t, func() {
		var gotPath, gotMethod string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath, gotMethod = r.URL.Path, r.Method
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"devices":[` +
				`{"device_id":7,"fingerprint":"sha256:box","name":"box","kind":"agentred","reported":true,"reported_at":1800,"report":{"collected_at":1700}},` +
				`{"device_id":8,"fingerprint":"sha256:new","kind":"desktop","reported":false,"report":null}]}}`))
		}))
		defer srv.Close()

		svc, mRepo, _ := setupServerSvc(t, srv.URL)
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInState(srv.URL), nil)

		list, err := factsReader(svc).ListDeviceFacts(context.Background())
		So(err, ShouldBeNil)
		So(gotMethod, ShouldEqual, http.MethodGet)
		So(gotPath, ShouldEqual, syncwire.PathDeviceFactsList)
		So(list.Devices, ShouldHaveLength, 2)
		So(list.Devices[0].ReportedAt, ShouldEqual, 1800)
		So(list.Devices[0].Report.CollectedAt, ShouldEqual, 1700)
		So(list.Devices[1].Reported, ShouldBeFalse)
	})

	Convey("server 还没有这个端点(404)时返回错误,由调用方保留缓存", t, func() {
		srv := httptest.NewServer(http.NotFoundHandler())
		defer srv.Close()
		svc, mRepo, _ := setupServerSvc(t, srv.URL)
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInState(srv.URL), nil).AnyTimes()

		_, err := factsReader(svc).ListDeviceFacts(context.Background())
		So(err, ShouldNotBeNil)
	})

	Convey("未登录时一个网络请求都不发", t, func() {
		called := false
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
		defer srv.Close()
		svc, mRepo, _ := setupServerSvc(t, srv.URL)
		mRepo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{ID: 1}, nil)

		_, err := factsReader(svc).ListDeviceFacts(context.Background())
		So(err, ShouldNotBeNil)
		So(called, ShouldBeFalse)
	})
}

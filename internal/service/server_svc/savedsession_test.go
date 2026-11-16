package server_svc_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
)

func TestDeleteSavedSession(t *testing.T) {
	const convID = "0194f3a2-7b1c-7d2e-8f30-1a2b3c4d5e6f"

	Convey("已登录：POST /v1/saved-sessions/delete 带上对话身份与执行端指纹，交回执行端那一份的去向", t, func() {
		var gotPath, gotMethod string
		var raw []byte
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath, gotMethod = r.URL.Path, r.Method
			raw, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"machine_status":"pending"}}`))
		}))
		defer srv.Close()

		svc, mRepo, _ := setupServerSvc(t, srv.URL)
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInState(srv.URL), nil)

		status, err := svc.DeleteSavedSession(context.Background(), convID, "sha256:carrier-01")

		So(err, ShouldBeNil)
		So(status, ShouldEqual, server_svc.MachineStatusPending)
		So(gotMethod, ShouldEqual, http.MethodPost)
		So(gotPath, ShouldEqual, "/v1/saved-sessions/delete")
		var body map[string]any
		So(json.Unmarshal(raw, &body), ShouldBeNil)
		So(body["conversation_id"], ShouldEqual, convID)
		So(body["machine_fingerprint"], ShouldEqual, "sha256:carrier-01")
	})

	Convey("本机执行的对话不带 machine_fingerprint 字段（server 的 binding 要求非空时至少 8 位）", t, func() {
		var raw []byte
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"machine_status":"deleted"}}`))
		}))
		defer srv.Close()

		svc, mRepo, _ := setupServerSvc(t, srv.URL)
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInState(srv.URL), nil)

		status, err := svc.DeleteSavedSession(context.Background(), convID, "")
		So(err, ShouldBeNil)
		So(status, ShouldEqual, server_svc.MachineStatusDeleted)
		var body map[string]any
		So(json.Unmarshal(raw, &body), ShouldBeNil)
		_, has := body["machine_fingerprint"]
		So(has, ShouldBeFalse)
	})

	Convey("未登录时不发请求，交回 ErrNotLoggedIn", t, func() {
		called := false
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
		defer srv.Close()

		svc, mRepo, _ := setupServerSvc(t, srv.URL)
		mRepo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{ID: 1}, nil)

		_, err := svc.DeleteSavedSession(context.Background(), convID, "")
		So(errors.Is(err, server_svc.ErrNotLoggedIn), ShouldBeTrue)
		So(called, ShouldBeFalse)
	})

	Convey("server 拒绝（4xx 业务错误）算没删成：交回错误而不是空状态", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":10001,"msg":"bad request"}`))
		}))
		defer srv.Close()

		svc, mRepo, _ := setupServerSvc(t, srv.URL)
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInState(srv.URL), nil)

		status, err := svc.DeleteSavedSession(context.Background(), convID, "")
		So(err, ShouldNotBeNil)
		So(errors.Is(err, server_svc.ErrNotLoggedIn), ShouldBeFalse)
		So(status, ShouldEqual, "")
	})

	Convey("server 够不着交回 ErrServerUnreachable", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL
		srv.Close()

		svc, mRepo, _ := setupServerSvc(t, url)
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInState(url), nil)

		_, err := svc.DeleteSavedSession(context.Background(), convID, "")
		So(errors.Is(err, server_svc.ErrServerUnreachable), ShouldBeTrue)
	})
}

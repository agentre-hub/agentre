package server_svc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
)

// loggedInRow 是一台完成过设备流登录的桌面端在 server_state 里的样子。
func loggedInRow(url string) *server_state_entity.ServerState {
	return &server_state_entity.ServerState{
		ID: 1, ServerURL: url, ServerUserID: 7, DeviceID: 42,
		KeychainAccount: "agentre.server.refresh_token",
	}
}

// rejectRefresh 复刻 server 对失效 refresh_token 的真实回法:
// device_ctr.oauthErrToHTTP 把 ErrInvalidGrant 映射成 HTTP 400,
// middleware.AttachOAuthErrorFields 再往 body 里塞 error=invalid_grant。
func rejectRefresh(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(`{"code":1010,"msg":"invalid","error":"invalid_grant",` +
		`"error_description":"refresh_token expired"}`))
}

func TestRefresh_ClassifiesRejectionApartFromOutage(t *testing.T) {
	Convey("Given the server explicitly rejects the stored refresh_token, When Refresh runs, Then it reports a credential rejection", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/oauth/token/refresh" {
				rejectRefresh(w)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		svc, _, kc := setupServerSvc(t, srv.URL)
		_ = kc.Set("agentre.server.refresh_token", "rt-dead")

		err := svc.Refresh(context.Background())
		So(err, ShouldNotBeNil)
		So(server_svc.IsCredentialRejected(err), ShouldBeTrue)
	})

	Convey("Given the server is unreachable, When Refresh runs, Then it is NOT a credential rejection and the stored token survives", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		srv.Close() // 立刻关掉:连不上,等同 server 挂了

		svc, _, kc := setupServerSvc(t, srv.URL)
		_ = kc.Set("agentre.server.refresh_token", "rt-alive")

		err := svc.Refresh(context.Background())
		So(err, ShouldNotBeNil)
		So(server_svc.IsCredentialRejected(err), ShouldBeFalse)
		v, _ := kc.Get("agentre.server.refresh_token")
		So(v, ShouldEqual, "rt-alive")
	})

	Convey("Given the server answers 500, When Refresh runs, Then it is NOT a credential rejection", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		svc, _, kc := setupServerSvc(t, srv.URL)
		_ = kc.Set("agentre.server.refresh_token", "rt-alive")

		err := svc.Refresh(context.Background())
		So(err, ShouldNotBeNil)
		So(server_svc.IsCredentialRejected(err), ShouldBeFalse)
	})

	Convey("Given the refresh endpoint 404s behind a misrouted reverse proxy, When Refresh runs, Then it is NOT a credential rejection", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		svc, _, kc := setupServerSvc(t, srv.URL)
		_ = kc.Set("agentre.server.refresh_token", "rt-alive")

		err := svc.Refresh(context.Background())
		So(err, ShouldNotBeNil)
		So(server_svc.IsCredentialRejected(err), ShouldBeFalse)
	})
}

func TestWithAuth_KeepsLoginWhenServerIsMerelyUnreachable(t *testing.T) {
	Convey("Given a 401 on the business call and a server that is down for the retry, When ListDevices runs, Then the local login is left intact", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/devices":
				w.WriteHeader(http.StatusUnauthorized)
			case "/v1/oauth/token/refresh":
				w.WriteHeader(http.StatusInternalServerError)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		svc, mRepo, kc := setupServerSvc(t, srv.URL)
		_ = kc.Set("agentre.server.refresh_token", "rt-alive")
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInRow(srv.URL), nil)
		// 关键断言:登录字段一次都不许被清 —— gomock 对未声明的调用直接判错。

		_, err := svc.ListDevices(context.Background())
		So(err, ShouldNotBeNil)
		v, _ := kc.Get("agentre.server.refresh_token")
		So(v, ShouldEqual, "rt-alive")
	})

	Convey("Given a 401 and a server that rejects the refresh_token, When ListDevices runs, Then the local login is cleared", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/devices":
				w.WriteHeader(http.StatusUnauthorized)
			case "/v1/oauth/token/refresh":
				rejectRefresh(w)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		svc, mRepo, kc := setupServerSvc(t, srv.URL)
		_ = kc.Set("agentre.server.refresh_token", "rt-dead")
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInRow(srv.URL), nil)
		mRepo.EXPECT().ClearLoginFields(gomock.Any()).Return(nil)

		_, err := svc.ListDevices(context.Background())
		So(err, ShouldNotBeNil)
		v, _ := kc.Get("agentre.server.refresh_token")
		So(v, ShouldEqual, "")
	})
}

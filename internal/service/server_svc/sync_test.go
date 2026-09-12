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
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
)

func loggedInState(url string) *server_state_entity.ServerState {
	return &server_state_entity.ServerState{
		ID: 1, ServerURL: url, ServerUserID: 7, DeviceID: 42,
		KeychainAccount: "agentre.server.refresh_token",
	}
}

func TestSyncPush(t *testing.T) {
	Convey("SyncPush 把一批改动发到 /v1/sync/push 并解析处置结果", t, func() {
		var body map[string]any
		var gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"results":[
				{"sync_id":"p-1","kind":"project","version":12,"status":"conflict",
				 "overwritten_version":11,"overwritten_origin_fingerprint":"fp-desktop-02"}
			]}}`))
		}))
		defer srv.Close()

		svc, mRepo, _ := setupServerSvc(t, srv.URL)
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInState(srv.URL), nil)

		out, err := svc.SyncPush(context.Background(), []syncwire.PushItem{{
			Kind: "project", SyncID: "p-1", BaseVersion: 8, UpdatedAt: 1700,
			Payload: []byte(`{"name":"Alpha"}`),
		}})

		So(err, ShouldBeNil)
		So(gotPath, ShouldEqual, "/v1/sync/push")
		So(len(out), ShouldEqual, 1)
		So(out[0].Status, ShouldEqual, syncwire.PushStatusConflict)
		So(out[0].Version, ShouldEqual, int64(12))
		So(out[0].OverwrittenVersion, ShouldEqual, int64(11))
		So(out[0].OverwrittenOriginFingerprint, ShouldEqual, "fp-desktop-02")

		items := body["items"].([]any)
		first := items[0].(map[string]any)
		So(first["base_version"], ShouldEqual, float64(8))
		So(first["payload"].(map[string]any)["name"], ShouldEqual, "Alpha")
	})

	Convey("墓碑上行不带 payload 字段，而不是发一个 JSON null（R6）", t, func() {
		// server 的 ValidatePayload 只把「空」当合法（墓碑不带正文），JSON null 解出来
		// 不是对象、会整批 30501 —— 一次删除就能把这台桌面端的出站队列永久堵死。
		var raw []byte
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"results":[
				{"sync_id":"p-1","kind":"project","version":13,"status":"accepted"}
			]}}`))
		}))
		defer srv.Close()

		svc, mRepo, _ := setupServerSvc(t, srv.URL)
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInState(srv.URL), nil)

		_, err := svc.SyncPush(context.Background(), []syncwire.PushItem{{
			Kind: "project", SyncID: "p-1", BaseVersion: 8, UpdatedAt: 1700, DeletedAt: 1700,
		}})
		So(err, ShouldBeNil)

		var body map[string]any
		So(json.Unmarshal(raw, &body), ShouldBeNil)
		first := body["items"].([]any)[0].(map[string]any)
		So(first["deleted_at"], ShouldEqual, float64(1700))
		_, hasPayload := first["payload"]
		So(hasPayload, ShouldBeFalse)
		So(string(raw), ShouldNotContainSubstring, `"payload":null`)
	})

	Convey("超窗口设备的上行翻成 ErrResyncRequired（R6a）", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":30500,"msg":"resync required"}`))
		}))
		defer srv.Close()

		svc, mRepo, _ := setupServerSvc(t, srv.URL)
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInState(srv.URL), nil)

		_, err := svc.SyncPush(context.Background(), []syncwire.PushItem{{Kind: "project", SyncID: "p-1"}})
		So(err, ShouldEqual, syncwire.ErrResyncRequired)
	})

	Convey("未登录时一个网络请求都不发（R12）", t, func() {
		called := false
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
		defer srv.Close()

		svc, mRepo, _ := setupServerSvc(t, srv.URL)
		mRepo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{ID: 1}, nil)

		_, err := svc.SyncPush(context.Background(), []syncwire.PushItem{{Kind: "project", SyncID: "p-1"}})
		So(err, ShouldNotBeNil)
		So(called, ShouldBeFalse)
	})
}

func TestSyncPull(t *testing.T) {
	Convey("SyncPull 按游标增量下行，墓碑也在其中（R6）", t, func() {
		var gotQuery, gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"items":[
				{"kind":"project","sync_id":"p-1","payload":{"name":"A"},"version":11,
				 "updated_at":1700,"origin_fingerprint":"fp-desktop-02","deleted_at":0},
				{"kind":"project","sync_id":"p-2","payload":{},"version":12,"deleted_at":1800}
			],"next_cursor":12,"has_more":false}}`))
		}))
		defer srv.Close()

		svc, mRepo, _ := setupServerSvc(t, srv.URL)
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInState(srv.URL), nil)

		page, err := svc.SyncPull(context.Background(), 10, 200)
		So(err, ShouldBeNil)
		So(gotPath, ShouldEqual, "/v1/sync/pull")
		So(gotQuery, ShouldEqual, "cursor=10&limit=200")
		So(len(page.Items), ShouldEqual, 2)
		So(page.Items[0].Version, ShouldEqual, int64(11))
		So(page.Items[0].OriginFingerprint, ShouldEqual, "fp-desktop-02")
		So(page.Items[1].DeletedAt, ShouldEqual, int64(1800))
		So(page.NextCursor, ShouldEqual, int64(12))
		So(page.HasMore, ShouldBeFalse)
	})

	Convey("server 不认识这个游标时翻成 ErrCursorUnknown", t, func() {
		// 不翻的话它只是一句「rejected with code 30505」，与网络抖动无从区分：那台机器
		// 会安静地一直重试同一个死游标，而账号下什么都没有。
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":30505,"msg":"unknown sync cursor"}`))
		}))
		defer srv.Close()

		svc, mRepo, _ := setupServerSvc(t, srv.URL)
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInState(srv.URL), nil)

		page, err := svc.SyncPull(context.Background(), 500, 200)
		So(err, ShouldEqual, syncwire.ErrCursorUnknown)
		So(page, ShouldBeNil)
	})
}

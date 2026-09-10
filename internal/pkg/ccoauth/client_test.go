package ccoauth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/ccoauth"
)

func TestClientFetchUsage(t *testing.T) {
	Convey("FetchUsage 在 200 时解析并返回 RateLimits", t, func() {
		var gotAuth, gotBeta, gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			gotBeta = r.Header.Get("anthropic-beta")
			gotPath = r.URL.Path
			_, _ = w.Write([]byte(`{
				"five_hour": {"utilization": 33.3, "resets_at": "2026-05-28T13:00:00Z"},
				"seven_day": {"utilization": 7.7,  "resets_at": "2026-06-04T00:00:00Z"}
			}`))
		}))
		defer srv.Close()

		client := ccoauth.NewClient(srv.URL)
		got, err := client.FetchUsage(context.Background(), "tok-123")
		So(err, ShouldBeNil)
		So(got.FiveHourPercent, ShouldAlmostEqual, 33.3, 0.001)
		So(gotAuth, ShouldEqual, "Bearer tok-123")
		So(gotBeta, ShouldEqual, "oauth-2025-04-20")
		So(gotPath, ShouldEqual, "/api/oauth/usage")
	})

	Convey("FetchUsage 在 401 时返回 ErrAuthExpired", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		_, err := ccoauth.NewClient(srv.URL).FetchUsage(context.Background(), "tok")
		So(errors.Is(err, ccoauth.ErrAuthExpired), ShouldBeTrue)
	})

	Convey("FetchUsage 在 429 时返回 ErrRateLimited", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer srv.Close()

		_, err := ccoauth.NewClient(srv.URL).FetchUsage(context.Background(), "tok")
		So(errors.Is(err, ccoauth.ErrRateLimited), ShouldBeTrue)
	})

	Convey("FetchUsage 在 500 时返回 ErrNetwork", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		_, err := ccoauth.NewClient(srv.URL).FetchUsage(context.Background(), "tok")
		So(errors.Is(err, ccoauth.ErrNetwork), ShouldBeTrue)
	})

	Convey("FetchUsage 在响应 JSON 损坏时返回 ErrNetwork", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`not-json`))
		}))
		defer srv.Close()

		_, err := ccoauth.NewClient(srv.URL).FetchUsage(context.Background(), "tok")
		So(errors.Is(err, ccoauth.ErrNetwork), ShouldBeTrue)
	})
}

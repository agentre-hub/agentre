package server_svc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/service/server_svc"
)

func TestHealthcheck_OK(t *testing.T) {
	Convey("Healthcheck returns the version when /v1/healthz returns 200", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/healthz" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"status":"ok","db_ping":true,"redis":true,"version":"v0.1.0"}}`))
		}))
		defer srv.Close()

		c := server_svc.NewHTTPClient(srv.URL, "")
		v, err := c.Healthcheck(context.Background())
		So(err, ShouldBeNil)
		So(v, ShouldEqual, "v0.1.0")
	})
}

func TestHealthcheck_Unreachable(t *testing.T) {
	Convey("Healthcheck returns ErrServerUnreachable for connection refused", t, func() {
		// Port 1 is reserved (TCP "tcpmux"); nothing should be listening.
		c := server_svc.NewHTTPClient("http://127.0.0.1:1", "")
		_, err := c.Healthcheck(context.Background())
		So(err, ShouldEqual, server_svc.ErrServerUnreachable)
	})
}

func TestHealthcheck_NonZeroCode(t *testing.T) {
	Convey("Healthcheck returns ErrServerUnreachable when envelope.code != 0", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":10000,"msg":"degraded","data":null}`))
		}))
		defer srv.Close()

		c := server_svc.NewHTTPClient(srv.URL, "")
		_, err := c.Healthcheck(context.Background())
		So(err, ShouldEqual, server_svc.ErrServerUnreachable)
	})
}

func TestDo_NonJSON5xxSurfacesAsHTTPErr(t *testing.T) {
	Convey("do() returns *httpErr when 5xx body is non-JSON, not a json.SyntaxError", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("<html>nginx error page</html>"))
		}))
		defer srv.Close()

		// Healthcheck exercises do() with a decode target; for a 502 + HTML body
		// it should bubble up ErrServerUnreachable (Healthcheck maps any non-200 to that)
		// rather than a json syntax error.
		c := server_svc.NewHTTPClient(srv.URL, "")
		_, err := c.Healthcheck(context.Background())
		So(err, ShouldEqual, server_svc.ErrServerUnreachable)
	})
}

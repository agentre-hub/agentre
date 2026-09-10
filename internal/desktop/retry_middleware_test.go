package desktop

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentre-hub/agentre/internal/app"
)

// TestDevProxyRetryMiddleware wraps the fail-then-succeed behavior of the wails
// dev asset proxy: its ErrorHandler writes 502 on `connection reset by peer`,
// so first-load subresource requests can be dropped mid-navigation. The
// middleware must retry GET requests and fall through on 5xx until success.
func TestDevProxyRetryMiddleware(t *testing.T) {
	t.Run("Given GET proxy 502s twice Then it retries and returns final 200", func(t *testing.T) {
		var calls int
		var handler http.Handler = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			calls++
			if calls < 3 {
				rw.WriteHeader(http.StatusBadGateway)
				return
			}
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte("ok"))
		})

		mw := devProxyRetryMiddleware(handler)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/index.html", nil))

		if calls != 3 {
			t.Fatalf("handler calls = %d, want 3", calls)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if rec.Body.String() != "ok" {
			t.Fatalf("body = %q, want %q", rec.Body.String(), "ok")
		}
	})

	t.Run("Given GET keep 502ing beyond attempts Then it falls through with last 5xx", func(t *testing.T) {
		var calls int
		var handler http.Handler = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			calls++
			rw.WriteHeader(http.StatusBadGateway)
		})

		mw := devProxyRetryMiddleware(handler)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/main.js", nil))

		if calls != devProxyRetryAttempts {
			t.Fatalf("handler calls = %d, want %d", calls, devProxyRetryAttempts)
		}
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", rec.Code)
		}
	})

	t.Run("Given non-GET request Then it passes through without retry", func(t *testing.T) {
		var calls int
		var handler http.Handler = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			calls++
			rw.WriteHeader(http.StatusOK)
		})

		mw := devProxyRetryMiddleware(handler)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))

		if calls != 1 {
			t.Fatalf("handler calls = %d, want 1", calls)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
}

// TestNewWailsOptionsWiresRetryOnlyInDev ensures the middleware is attached only
// in dev mode, so production stays byte-for-byte untouched.
func TestNewWailsOptionsWiresRetryOnlyInDev(t *testing.T) {
	t.Run("Given production mode Then no asset server middleware", func(t *testing.T) {
		t.Setenv("devserver", "")
		opts := newWailsOptions(app.NewApp(app.RuntimeModeHeadless), nil, "darwin", "/tmp/agentre-test")
		if opts.AssetServer == nil {
			t.Fatal("AssetServer is nil")
		}
		if opts.AssetServer.Middleware != nil {
			t.Fatal("AssetServer.Middleware must be nil in production mode")
		}
	})

	t.Run("Given Wails dev Then asset server middleware is wired", func(t *testing.T) {
		t.Setenv("devserver", "localhost:34115")
		opts := newWailsOptions(app.NewApp(app.RuntimeModeHeadless), nil, "darwin", "/tmp/agentre-test")
		if opts.AssetServer == nil {
			t.Fatal("AssetServer is nil")
		}
		if opts.AssetServer.Middleware == nil {
			t.Fatal("AssetServer.Middleware must be wired in dev mode")
		}
	})
}

package desktop

import (
	"net/http"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// devProxyRetryAttempts is the total number of ServeHTTP passes the dev proxy
// middleware allows for a single GET request. The wails dev ReverseProxy's
// ErrorHandler writes 502 on `connection reset by peer`, so the first load can
// hit a burst of resets (upstream wails#4556, unfixed in v2). One re-dial of
// the already-open upstream connection usually succeeds, so allow a couple of
// retries before giving up.
const devProxyRetryAttempts = 3

// devProxyRetryMiddleware wraps the wails dev asset server's GET handler. It
// retries idempotent GET requests whose upstream response is a 5xx (the proxy
// emits 502 on connection reset), cloning the request fresh for each pass so
// the proxy's Director rewrites the URL to the vite server again. Non-GET
// requests fall through untouched.
func devProxyRetryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			next.ServeHTTP(rw, req)
			return
		}

		for attempt := 1; attempt <= devProxyRetryAttempts; attempt++ {
			rec := &retryResponseRecorder{header: make(http.Header)}
			next.ServeHTTP(rec, req.Clone(req.Context()))

			if rec.status < 500 {
				if !rec.wroteHeader {
					// No status written: the upstream never sent a response header
					// (connection abandoned). Treat as retryable.
					rec.code = http.StatusBadGateway
					rec.status = http.StatusBadGateway
				}
				flushRetryRecorder(rw, rec)
				return
			}

			if attempt == devProxyRetryAttempts {
				logger.Default().Warn("desktop.devProxyRetryMiddleware: proxy still failing after retries",
					zap.String("path", req.URL.Path),
					zap.Int("attempts", attempt),
				)
				flushRetryRecorder(rw, rec)
				return
			}

			logger.Default().Debug("desktop.devProxyRetryMiddleware: proxy 5xx, retrying",
				zap.String("path", req.URL.Path),
				zap.Int("status", rec.status),
				zap.Int("attempt", attempt),
			)
		}
	})
}

// retryResponseRecorder buffers a proxied response without committing to the
// real writer, so a failed 5xx attempt can be thrown away and retried.
type retryResponseRecorder struct {
	header      http.Header
	status      int
	code        int
	wroteHeader bool
	body        []byte
}

func (r *retryResponseRecorder) Header() http.Header { return r.header }

func (r *retryResponseRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.wroteHeader = true
	r.code = code
	r.status = code
}

func (r *retryResponseRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	r.body = append(r.body, b...)
	return len(b), nil
}

// flushRetryRecorder copies a buffered successful response to the real writer.
func flushRetryRecorder(rw http.ResponseWriter, rec *retryResponseRecorder) {
	for k, vs := range rec.header {
		for _, v := range vs {
			rw.Header().Add(k, v)
		}
	}
	rw.WriteHeader(rec.code)
	if len(rec.body) > 0 {
		_, _ = rw.Write(rec.body)
	}
}

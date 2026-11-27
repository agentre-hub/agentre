package llmurl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEffectiveBaseURLGemini(t *testing.T) {
	assert.Equal(t, "https://generativelanguage.googleapis.com", EffectiveBaseURL("gemini", " "))
	assert.Equal(t, "https://huu.dqy.ink", EffectiveBaseURL("gemini", "https://huu.dqy.ink"))
}

func geminiServer(t *testing.T, status int, body string, gotPath, gotKey *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotPath = r.Method + " " + r.URL.RequestURI()
		*gotKey = r.Header.Get("x-goog-api-key")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGeminiDiscover(t *testing.T) {
	ctx := context.Background()

	t.Run("lists models via /v1beta/models with x-goog-api-key and strips models/ prefix only", func(t *testing.T) {
		var path, key string
		srv := geminiServer(t, 200, `{"models":[
			{"name":"models/gemini-2.5-pro","displayName":"Gemini 2.5 Pro"},
			{"name":"antigravity/gemini-3.8-flash","displayName":"AG Flash"},
			{"name":"glm-5.3-flash-free"},
			{"name":""}]}`, &path, &key)
		models, err := NewClient(nil).Discover(ctx, Provider{Type: "gemini", BaseURL: srv.URL, APIKey: "k1"})
		require.NoError(t, err)
		assert.Equal(t, "GET /v1beta/models", path)
		assert.Equal(t, "k1", key)
		assert.Equal(t, []Model{
			{ModelID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro"},
			{ModelID: "antigravity/gemini-3.8-flash", Name: "AG Flash"},
			{ModelID: "glm-5.3-flash-free", Name: "glm-5.3-flash-free"},
		}, models)
	})

	t.Run("401 and 403 surface the existing status error", func(t *testing.T) {
		for _, st := range []int{401, 403} {
			var path, key string
			srv := geminiServer(t, st, `{}`, &path, &key)
			_, err := NewClient(nil).Discover(ctx, Provider{Type: "gemini", BaseURL: srv.URL, APIKey: "bad"})
			assert.ErrorContains(t, err, "upstream returned HTTP status")
		}
	})

	t.Run("unreachable upstream reports a request failure", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		_, err := NewClient(nil).Discover(ctx, Provider{Type: "gemini", BaseURL: url, APIKey: "k"})
		assert.ErrorContains(t, err, "upstream request failed")
	})
}

func TestGeminiTest(t *testing.T) {
	ctx := context.Background()

	t.Run("probes GET /v1beta/models with the key header", func(t *testing.T) {
		var path, key string
		srv := geminiServer(t, 200, `{"models":[]}`, &path, &key)
		err := NewClient(nil).Test(ctx, Provider{Type: "gemini", BaseURL: srv.URL, APIKey: "k2"}, "gemini-2.5-pro")
		require.NoError(t, err)
		assert.Equal(t, "GET /v1beta/models", path)
		assert.Equal(t, "k2", key)
	})

	t.Run("401 is an error", func(t *testing.T) {
		var path, key string
		srv := geminiServer(t, 401, `{}`, &path, &key)
		err := NewClient(nil).Test(ctx, Provider{Type: "gemini", BaseURL: srv.URL, APIKey: "bad"}, "m")
		assert.ErrorContains(t, err, "upstream returned HTTP status 401")
	})
}

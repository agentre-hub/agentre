// Package accountpeer is the hermetic account-peer boundary of the E2E harness: a
// headless agrctl talks to a loopback fake account server, a fake relay and a fake
// agentred, with no desktop involved.
package accountpeer

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/agentre-hub/agentre/internal/pkg/relaypeer/relaypeertest"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

const (
	boxFingerprint = "sha256:e2e-build-box"
	cliFingerprint = "sha256:e2e-cli" // what the box's auth.account reports for the logged-in agrctl
	accessToken    = "e2e-access-token"
	refreshToken   = "e2e-refresh-token" //nolint:gosec // G101: the fake account server's test token, not a credential
)

// World is the loopback fake account server + relay + agentred.
type World struct {
	srv      *httptest.Server
	relay    *relaypeertest.Relay
	Agentred *Agentred

	mu    sync.Mutex
	saved []map[string]any
}

func startWorld(t *testing.T) *World {
	t.Helper()
	w := &World{Agentred: newAgentred()}
	w.relay = relaypeertest.New(func(bearer string) bool { return bearer == accessToken })
	w.relay.AddMachine(&relaypeertest.Machine{
		Fingerprint: boxFingerprint,
		Registry:    w.Agentred.registry(),
		Identify:    func(string) string { return cliFingerprint },
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth/device/authorize", w.deviceAuthorize)
	mux.HandleFunc("/v1/oauth/device/token", w.deviceToken)
	mux.HandleFunc("/v1/auth/me", w.authed(func(rw http.ResponseWriter, _ *http.Request) {
		envelope(rw, map[string]any{"user_id": 7001, "email": "alice@example.com"})
	}))
	mux.HandleFunc("/v1/ctl/resources", w.authed(w.ctlResources))
	mux.HandleFunc("/v1/workspace/dispatch-target", w.authed(w.dispatchTarget))
	mux.HandleFunc("/v1/devices", w.authed(w.devices))
	mux.HandleFunc("/v1/saved-sessions", w.authed(w.saveSession))
	mux.HandleFunc("/v1/agent-sessions", w.authed(w.agentSessions))
	mux.Handle(w.relay.Path, w.relay)
	w.srv = httptest.NewServer(mux)
	t.Cleanup(w.srv.Close)
	return w
}

// ServerURL is the loopback base URL of the fake account server.
func (w *World) ServerURL() string { return w.srv.URL }

func envelope(rw http.ResponseWriter, data any) {
	_ = json.NewEncoder(rw).Encode(map[string]any{"code": 0, "msg": "ok", "data": data})
}

func (w *World) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+accessToken {
			rw.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(rw, `{"code":401,"msg":"unauthorized","data":null}`)
			return
		}
		next(rw, r)
	}
}

// deviceAuthorize and deviceToken are an RFC 8628 server that approves at once.
func (w *World) deviceAuthorize(rw http.ResponseWriter, r *http.Request) {
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body["device_kind"] != "cli" || body["fingerprint"] == "" {
		rw.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(rw, `{"code":400,"msg":"agrctl must log in as a cli device","data":null}`)
		return
	}
	envelope(rw, map[string]any{"device_code": "dc-e2e", "user_code": "WXYZ-1234",
		"verification_uri": "http://127.0.0.1/device", "verification_uri_complete": "http://127.0.0.1/device?user_code=WXYZ-1234",
		"interval": 1, "expires_in": 600})
}

func (w *World) deviceToken(rw http.ResponseWriter, r *http.Request) {
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body["device_code"] != "dc-e2e" {
		rw.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(rw, `{"code":400,"msg":"invalid_grant","data":null,"error":"invalid_grant"}`)
		return
	}
	envelope(rw, map[string]any{"access_token": accessToken, "expires_in": 900, "refresh_token": refreshToken,
		"refresh_expires_in": 86400, "device_id": 42})
}

// ctlResources serves the account's catalog: one agent (reviewer) bound to one backend
// (codex on build-box). Every other kind is empty.
func (w *World) ctlResources(rw http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var req agentrewire.CtlRequest
	if err := protojson.Unmarshal(raw, &req); err != nil || req.GetList() == nil {
		rw.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(rw, `{"error":"the fake account server only lists resources"}`)
		return
	}
	var items []*agentrewire.CtlResource
	switch req.GetList().GetKind() {
	case agentrewire.CtlKind_CTL_KIND_AGENT:
		items = append(items, &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Agent{Agent: &agentrewire.CtlAgent{
			Id: 12, SyncId: "AG-12", Name: "reviewer", BackendIds: []int64{9}}}})
	case agentrewire.CtlKind_CTL_KIND_BACKEND:
		items = append(items, &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Backend{Backend: &agentrewire.CtlBackend{
			Id: 9, SyncId: "BE-9", Name: "codex-box", Type: "codex", Device: "build-box"}}})
	}
	out, _ := protojson.Marshal(&agentrewire.CtlResponse{Result: &agentrewire.CtlResponse_List{List: &agentrewire.CtlListResponse{Items: items}}})
	_, _ = rw.Write(out)
}

func (w *World) dispatchTarget(rw http.ResponseWriter, r *http.Request) {
	agent := r.URL.Query().Get("agent_sync_id")
	chosen := map[string]any{"device_fingerprint": boxFingerprint, "device_id": 2, "device_name": "build-box",
		"backend_sync_id": "BE-9", "backend_type": "codex", "kind": "agentred"}
	tier := map[string]any{"rank": 1, "backend_sync_id": "BE-9", "device_id": 2, "device_name": "build-box",
		"backend_type": "codex", "kind": "agentred", "availability": "available"}
	envelope(rw, map[string]any{"agent_sync_id": agent, "tiers": []any{tier}, "chosen": chosen, "projects": []any{}})
}

func (w *World) devices(rw http.ResponseWriter, _ *http.Request) {
	envelope(rw, map[string]any{"devices": []map[string]any{
		{"id": 2, "name": "build-box", "kind": "agentred", "fingerprint": boxFingerprint, "online": true},
		{"id": 42, "name": "headless", "kind": "cli", "fingerprint": cliFingerprint, "online": false},
	}})
}

// saveSession records a session saved to the account and makes conversation:<id> resolve
// to the machine that runs it.
func (w *World) saveSession(rw http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	w.mu.Lock()
	w.saved = append(w.saved, body)
	w.mu.Unlock()
	if id, _ := body["conversation_id"].(string); id != "" {
		if fp, _ := body["device_fingerprint"].(string); fp != "" {
			w.relay.Save(id, fp)
		}
	}
	envelope(rw, map[string]any{})
}

func (w *World) agentSessions(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()
	items := []map[string]any{}
	for _, it := range w.saved {
		if id := r.URL.Query().Get("conversation_id"); id != "" && it["conversation_id"] != id {
			continue
		}
		items = append(items, it)
	}
	envelope(rw, map[string]any{"items": items, "total": len(items)})
}

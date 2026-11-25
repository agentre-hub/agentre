package ctlcmd

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// plaintextToken 是假 server 在创建 / 轮换时交出的明文；它只该在 stdout 出现一次。
const plaintextToken = "agr_pat_PLAINTEXT-ONCE" //nolint:gosec // G101: 测试用假明文。

// tokenCall 是假 server 收到的一次 token 管理请求。
type tokenCall struct {
	method, path string
	body         map[string]any
	bearer       string
}

type tokenServer struct {
	*fakeServer
	mu    sync.Mutex
	calls []tokenCall
	item  map[string]any
}

func newTokenServer(t *testing.T) *tokenServer {
	t.Helper()
	ts := &tokenServer{fakeServer: newFakeServer(t), item: map[string]any{
		"name": "ci", "scopes": []string{"chat", "approve"}, "created_at": 1_700_000_000_000,
		"expires_at": 1_800_000_000_000, "last_used_at": 0, "rotated_at": 0,
		"created_by_device_id": 42, "created_by_device_name": "build-box",
	}}
	record := func(w http.ResponseWriter, r *http.Request, reply any) {
		raw, _ := io.ReadAll(r.Body)
		c := tokenCall{method: r.Method, path: r.URL.Path, bearer: r.Header.Get("Authorization")}
		_ = json.Unmarshal(raw, &c.body)
		ts.mu.Lock()
		ts.calls = append(ts.calls, c)
		ts.mu.Unlock()
		writeEnvelope(w, reply)
	}
	issue := map[string]any{"token": plaintextToken, "item": ts.item}
	ts.mux.HandleFunc("POST /v1/personal-tokens", func(w http.ResponseWriter, r *http.Request) { record(w, r, issue) })
	ts.mux.HandleFunc("GET /v1/personal-tokens", func(w http.ResponseWriter, r *http.Request) {
		forever := map[string]any{"name": "forever", "scopes": []string{"read"}, "created_at": 1_700_000_000_000, "expires_at": 0,
			"last_used_at": 1_750_000_000_000, "rotated_at": 1_760_000_000_000, "created_by_device_id": 0, "created_by_device_name": ""}
		record(w, r, map[string]any{"tokens": []any{ts.item, forever}})
	})
	ts.mux.HandleFunc("POST /v1/personal-tokens/{name}/rotate", func(w http.ResponseWriter, r *http.Request) { record(w, r, issue) })
	ts.mux.HandleFunc("DELETE /v1/personal-tokens/{name}", func(w http.ResponseWriter, r *http.Request) { record(w, r, map[string]any{}) })
	ts.mux.HandleFunc("GET /v1/personal-tokens/self", func(w http.ResponseWriter, r *http.Request) {
		record(w, r, map[string]any{"item": ts.item})
	})
	return ts
}

func (ts *tokenServer) recorded() []tokenCall {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return append([]tokenCall(nil), ts.calls...)
}

func loggedInTokenServer(t *testing.T) *tokenServer {
	t.Helper()
	dir := accountDirs(t)
	ts := newTokenServer(t)
	ts.saveLogin(t, dir)
	return ts
}

func TestToken_GivenALoginAtATerminalThenCreatePrintsThePlaintextOnceWithTheRequestedScopesAndLifetime(t *testing.T) {
	ts := loggedInTokenServer(t)
	r := runWith([]string{"create", "token", "--name", "ci", "--scope", "chat,approve", "--expires", "90d"}, envMap(), human)
	if r.code != exitOK {
		t.Fatalf("code = %d, stderr = %s", r.code, r.stderr)
	}
	if n := strings.Count(r.stdout+r.stderr, plaintextToken); n != 1 || !strings.Contains(r.stdout, plaintextToken) {
		t.Fatalf("plaintext appears %d times (stdout=%q stderr=%q); want exactly once on stdout", n, r.stdout, r.stderr)
	}
	calls := ts.recorded()
	if len(calls) != 1 || calls[0].method != http.MethodPost || calls[0].path != "/v1/personal-tokens" {
		t.Fatalf("calls = %+v", calls)
	}
	b := calls[0].body
	if b["name"] != "ci" || b["expires_in"] != float64(90*24*3600) || b["no_expiry"] == true {
		t.Fatalf("body = %v", b)
	}
	if scopes, _ := b["scopes"].([]any); len(scopes) != 2 || scopes[0] != "chat" || scopes[1] != "approve" {
		t.Fatalf("scopes = %v", b["scopes"])
	}
	if calls[0].bearer != "Bearer access-0" {
		t.Fatalf("bearer = %q, want the cli device's login", calls[0].bearer)
	}
}

func TestToken_GivenNoLifetimeThenTheServerDefaultAppliesAndNeverWarnsOnStderr(t *testing.T) {
	ts := loggedInTokenServer(t)
	r := runWith([]string{"create", "token", "--name", "ci", "--scope", "read"}, envMap(), human)
	if r.code != exitOK {
		t.Fatalf("code = %d, stderr = %s", r.code, r.stderr)
	}
	b := ts.recorded()[0].body
	if _, ok := b["expires_in"]; ok && b["expires_in"] != float64(0) || b["no_expiry"] == true {
		t.Fatalf("default lifetime must leave the choice to the server: %v", b)
	}
	if strings.Contains(r.stderr, "does not expire") {
		t.Fatalf("unexpected warning: %q", r.stderr)
	}

	r = runWith([]string{"create", "token", "--name", "forever", "--scope", "read", "--expires", "never"}, envMap(), human)
	if r.code != exitOK {
		t.Fatalf("code = %d, stderr = %s", r.code, r.stderr)
	}
	if ts.recorded()[1].body["no_expiry"] != true {
		t.Fatalf("body = %v", ts.recorded()[1].body)
	}
	if !strings.Contains(r.stderr, "does not expire") || !strings.Contains(r.stderr, "delete it") {
		t.Fatalf("stderr = %q, want the never-expires warning", r.stderr)
	}
}

func TestToken_GivenBadFlagsThenUsageErrorBeforeAnyRequest(t *testing.T) {
	ts := loggedInTokenServer(t)
	for _, args := range [][]string{
		{"create", "token", "--scope", "read"},
		{"create", "token", "--name", "ci"},
		{"create", "token", "--name", "ci", "--scope", "read", "--expires", "soon"},
		{"create", "token", "--name", "ci", "--scope", "read", "--expires", "-5m"},
		{"rotate", "token"},
		{"delete", "token"},
		{"list", "tokens", "extra"},
	} {
		r := runWith(args, envMap(), human)
		if r.code != exitUsage {
			t.Errorf("%v: code = %d, want 2 (stderr=%s)", args, r.code, r.stderr)
		}
	}
	if n := len(ts.recorded()); n != 0 {
		t.Fatalf("%d requests sent for bad usage", n)
	}
}

func TestToken_GivenRotateThenTheNewPlaintextIsPrintedOnceAndTheLifetimeIsOnlySentWhenAsked(t *testing.T) {
	ts := loggedInTokenServer(t)
	r := runWith([]string{"rotate", "token", "ci"}, envMap(), human)
	if r.code != exitOK || strings.Count(r.stdout+r.stderr, plaintextToken) != 1 {
		t.Fatalf("code = %d stdout=%q stderr=%q", r.code, r.stdout, r.stderr)
	}
	c := ts.recorded()[0]
	if c.method != http.MethodPost || c.path != "/v1/personal-tokens/ci/rotate" || len(c.body) != 0 {
		t.Fatalf("call = %+v, want an empty body so the server keeps the old rule", c)
	}
	r = runWith([]string{"rotate", "token", "ci", "--expires", "never"}, envMap(), human)
	if r.code != exitOK || ts.recorded()[1].body["no_expiry"] != true || !strings.Contains(r.stderr, "does not expire") {
		t.Fatalf("code = %d body = %v stderr = %q", r.code, ts.recorded()[1].body, r.stderr)
	}
}

func TestToken_GivenListThenEveryFactIsShownButNoTokenCharacters(t *testing.T) {
	loggedInTokenServer(t)
	r := runWith([]string{"list", "tokens"}, envMap(), human)
	if r.code != exitOK {
		t.Fatalf("code = %d, stderr = %s", r.code, r.stderr)
	}
	for _, want := range []string{"NAME", "SCOPES", "CREATED", "EXPIRES", "LAST USED", "LAST ROTATED", "CREATED ON",
		"ci", "chat,approve", "build-box", "forever", "never"} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("list = %q, missing %q", r.stdout, want)
		}
	}
	if strings.Contains(r.stdout+r.stderr, "agr_pat_") {
		t.Fatalf("list leaks token characters: %q", r.stdout)
	}
	j := runWith([]string{"list", "tokens", "-o", "json"}, envMap(), human)
	var rows []map[string]any
	if j.code != exitOK || json.Unmarshal([]byte(j.stdout), &rows) != nil || len(rows) != 2 {
		t.Fatalf("json: code = %d out = %q", j.code, j.stdout)
	}
}

func TestToken_GivenDeleteThenTheNamedTokenIsRevoked(t *testing.T) {
	ts := loggedInTokenServer(t)
	r := runWith([]string{"delete", "token", "my ci"}, envMap(), human)
	if r.code != exitOK {
		t.Fatalf("code = %d, stderr = %s", r.code, r.stderr)
	}
	c := ts.recorded()[0]
	if c.method != http.MethodDelete || c.path != "/v1/personal-tokens/my ci" {
		t.Fatalf("call = %+v", c)
	}
}

func TestToken_GivenAPersonalTokenThenManagingTokensIsRefusedWithoutAnyRequest(t *testing.T) {
	accountDirs(t)
	ts := newTokenServer(t)
	env := envMap("AGENTRE_TOKEN", personalToken, "AGENTRE_SERVER_URL", ts.srv.URL)
	for _, args := range [][]string{
		{"create", "token", "--name", "x", "--scope", "read"}, {"rotate", "token", "ci"},
		{"list", "tokens"}, {"delete", "token", "ci"},
	} {
		r := runWith(args, env, human)
		if r.code != exitFailed || !strings.Contains(r.stderr, "a personal token cannot manage tokens") {
			t.Errorf("%v: code = %d stderr = %q", args, r.code, r.stderr)
		}
	}
	if n := len(ts.recorded()); n != 0 {
		t.Fatalf("%d requests reached the server", n)
	}
}

func TestToken_GivenAnExternalProgramWithTheLoginThenManagingTokensIsRefused(t *testing.T) {
	ts := loggedInTokenServer(t)
	for _, c := range []struct {
		tm  term
		env func(string) (string, bool)
	}{{term{}, envMap()}, {human, envMap("CLAUDECODE", "1")}} {
		r := runWith([]string{"create", "token", "--name", "x", "--scope", "read"}, c.env, c.tm)
		if r.code != exitFailed || !strings.Contains(r.stderr, "needs a person at a terminal") {
			t.Errorf("code = %d stderr = %q", r.code, r.stderr)
		}
	}
	if n := len(ts.recorded()); n != 0 {
		t.Fatalf("%d requests reached the server", n)
	}
}

func TestToken_GivenNoLoginThenItSaysToLogIn(t *testing.T) {
	accountDirs(t)
	r := runWith([]string{"list", "tokens"}, envMap(), human)
	if r.code != exitFailed || !strings.Contains(r.stderr, "agrctl login") {
		t.Fatalf("code = %d stderr = %q", r.code, r.stderr)
	}
}

func TestWhoami_GivenAPersonalTokenThenItShowsTheTokenNameScopesAndExpiryFromTheServer(t *testing.T) {
	accountDirs(t)
	ts := newTokenServer(t)
	env := envMap("AGENTRE_TOKEN", personalToken, "AGENTRE_SERVER_URL", ts.srv.URL)
	r := runWith([]string{"whoami"}, env, term{})
	if r.code != exitOK {
		t.Fatalf("code = %d stderr = %s", r.code, r.stderr)
	}
	for _, want := range []string{"personal token", "ci", "chat,approve", "expires"} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("whoami = %q, missing %q", r.stdout, want)
		}
	}
	if strings.Contains(r.stdout+r.stderr, personalToken) {
		t.Fatalf("whoami leaks the token: %q", r.stdout)
	}
	if c := ts.recorded(); len(c) != 1 || c[0].path != "/v1/personal-tokens/self" {
		t.Fatalf("calls = %+v", c)
	}
}

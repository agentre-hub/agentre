package ctlcmd

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/agentre-hub/agentre/internal/pkg/agrctlcred"
	"github.com/agentre-hub/agentre/internal/pkg/ctlendpoint"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// personalToken 是假 server 认的那枚个人 token。
const personalToken = "agr_pat_good" //nolint:gosec // G101: 假 server 认的测试 token，不是凭据。

// fakeServer 是账号 server 的假实现：Device Flow、刷新（一次性轮换）、吊销、/v1/auth/me，
// 以及 /v1/ctl/resources——后者核对 Bearer 后交给 fakeExecutor 按 id 读写同一份数据集。
type fakeServer struct {
	t    *testing.T
	exec *fakeExecutor
	srv  *httptest.Server

	mu         sync.Mutex
	authorize  map[string]any
	authorized int
	pollQueue  []string
	access     string
	refresh    string
	gen        int
	revoked    bool
	revokeBody map[string]any
	hits       int
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	exec, _ := newFakeExecutor(t, "inner")
	f := &fakeServer{t: t, exec: exec, access: "access-0", refresh: "refresh-0"}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth/device/authorize", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.authorized++
		_ = json.NewDecoder(r.Body).Decode(&f.authorize)
		writeEnvelope(w, map[string]any{"device_code": "dc-1", "user_code": "WXYZ-1234",
			"verification_uri": "https://approve.example/device", "verification_uri_complete": "https://approve.example/device?user_code=WXYZ-1234",
			"interval": 1, "expires_in": 600})
	})
	mux.HandleFunc("/v1/oauth/device/token", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.pollQueue) > 0 {
			code := f.pollQueue[0]
			f.pollQueue = f.pollQueue[1:]
			if code != "" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 400, "msg": code, "data": nil, "error": code})
				return
			}
		}
		writeEnvelope(w, map[string]any{"access_token": f.access, "expires_in": 900, "refresh_token": f.refresh,
			"refresh_expires_in": 86400, "device_id": 42})
	})
	mux.HandleFunc("/v1/auth/me", func(w http.ResponseWriter, r *http.Request) {
		if !f.authed(w, r, false) {
			return
		}
		writeEnvelope(w, map[string]any{"user_id": 7, "email": "alice@example.com"})
	})
	mux.HandleFunc("/v1/oauth/token/refresh", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.revoked || body.RefreshToken != f.refresh {
			f.revoked = true
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 400, "msg": "invalid grant", "data": nil, "error": "invalid_grant"})
			return
		}
		f.gen++
		f.access, f.refresh = "access-"+strconv.Itoa(f.gen), "refresh-"+strconv.Itoa(f.gen)
		writeEnvelope(w, map[string]any{"access_token": f.access, "expires_in": 900, "refresh_token": f.refresh, "refresh_expires_in": 86400})
	})
	mux.HandleFunc("/v1/oauth/token/revoke", func(w http.ResponseWriter, r *http.Request) {
		if !f.authed(w, r, false) {
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = json.NewDecoder(r.Body).Decode(&f.revokeBody)
		f.revoked = true
		writeEnvelope(w, map[string]any{})
	})
	mux.HandleFunc("/v1/ctl/resources", func(w http.ResponseWriter, r *http.Request) {
		if !f.authed(w, r, true) {
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var req agentrewire.CtlRequest
		if err := protojson.Unmarshal(raw, &req); err == nil {
			// 与真 server 的 checkSupported 同一句拒绝：账号调用方写不了桌面的本地路径。
			if wr := req.GetWrite(); wr != nil && wr.GetKind() == agentrewire.CtlKind_CTL_KIND_PROJECT && slices.Contains(wr.GetFields(), "path") {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = io.WriteString(w, `{"error":"a desktop device's local project path cannot be set through the server; set it in the Agentre desktop on that machine"}`)
				return
			}
		}
		f.mu.Lock()
		f.hits++
		f.mu.Unlock()
		r.Header.Set("Authorization", "Bearer inner")
		r.Body = io.NopCloser(strings.NewReader(string(raw)))
		f.exec.serve(w, r)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func writeEnvelope(w http.ResponseWriter, data any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok", "data": data})
}

func (f *fakeServer) authed(w http.ResponseWriter, r *http.Request, allowPersonal bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	got := r.Header.Get("Authorization")
	if (allowPersonal && got == "Bearer "+personalToken) || (!f.revoked && got == "Bearer "+f.access) {
		return true
	}
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = io.WriteString(w, `{"code":401,"msg":"unauthorized","data":null}`)
	return false
}

func (f *fakeServer) serverHits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits
}

// saveLogin 在 dir 里放一份与假 server 当前状态一致的登录凭据。
func (f *fakeServer) saveLogin(t *testing.T, dir string) {
	t.Helper()
	f.mu.Lock()
	c := &agrctlcred.Credentials{ServerURL: f.srv.URL, AccountID: 7, Account: "alice@example.com", DeviceID: 42,
		DeviceName: "build-box", Fingerprint: "sha256:own", RefreshToken: f.refresh, AccessToken: f.access,
		AccessExpiresAt: time.Now().Add(time.Hour).Unix()}
	f.mu.Unlock()
	if err := agrctlcred.Save(dir, c); err != nil {
		t.Fatal(err)
	}
}

// accountDirs 把 agrctl 的数据目录指向一个新的临时目录，交回它；不是 agentred 主机。
func accountDirs(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dir)
	t.Setenv("AGENTRED_DATA_DIR", t.TempDir())
	return dir
}

// fastLoginPoll 让 Device Flow 轮询不睡眠。
func fastLoginPoll(t *testing.T) {
	t.Helper()
	prev := loginPollWait
	loginPollWait = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { loginPollWait = prev })
}

func envMap(kv ...string) func(string) (string, bool) {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

var human = term{tty: true}

func TestLogin_GivenTheUserApprovesThenAgrctlBecomesACliDeviceWithA0600CredentialFile(t *testing.T) {
	dir := accountDirs(t)
	fastLoginPoll(t)
	f := newFakeServer(t)
	f.pollQueue = []string{"authorization_pending", "slow_down", ""}

	r := runWith([]string{"login", "--server", f.srv.URL}, envMap(), human)
	if r.code != exitOK {
		t.Fatalf("login code = %d, want 0 (stderr=%s)", r.code, r.stderr)
	}
	if f.authorize["device_kind"] != "cli" {
		t.Fatalf("authorize device_kind = %v, want cli", f.authorize["device_kind"])
	}
	fp, _ := f.authorize["fingerprint"].(string)
	if !strings.HasPrefix(fp, "sha256:") {
		t.Fatalf("authorize fingerprint = %q, want agrctl's own sha256: fingerprint", fp)
	}
	if !strings.Contains(r.stderr, "WXYZ-1234") || !strings.Contains(r.stderr, "https://approve.example/device") {
		t.Fatalf("stderr = %q, want the user code and the verification URL", r.stderr)
	}
	if !strings.Contains(r.stdout, "alice@example.com") {
		t.Fatalf("stdout = %q, want the account it logged in as", r.stdout)
	}
	c, err := agrctlcred.Load(dir)
	if err != nil {
		t.Fatalf("credentials not saved: %v", err)
	}
	if c.ServerURL != f.srv.URL || c.AccountID != 7 || c.Account != "alice@example.com" || c.DeviceID != 42 ||
		c.Fingerprint != fp || c.RefreshToken != "refresh-0" || c.AccessToken != "access-0" || c.DelegatedBy != "" || c.DeviceName == "" {
		t.Fatalf("credentials = %+v", c)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(agrctlcred.FilePath(dir))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("credential file mode = %v, %v; want 0600", info.Mode().Perm(), err)
		}
	}
	for _, secret := range []string{"refresh-0", "access-0"} {
		if strings.Contains(r.stdout+r.stderr, secret) {
			t.Fatalf("login output leaks a credential: %q / %q", r.stdout, r.stderr)
		}
	}
}

func TestLogin_GivenAlreadyLoggedInThenItRefusesBeforeStartingADeviceFlow(t *testing.T) {
	dir := accountDirs(t)
	f := newFakeServer(t)
	f.saveLogin(t, dir)

	r := runWith([]string{"login", "--server", f.srv.URL}, envMap(), human)
	if r.code != exitFailed {
		t.Fatalf("code = %d, want 1", r.code)
	}
	if !strings.Contains(r.stderr, "already logged in as alice@example.com") || !strings.Contains(r.stderr, "agrctl logout") {
		t.Fatalf("stderr = %q, want the already-logged-in hint naming the account and agrctl logout", r.stderr)
	}
	if f.authorized != 0 {
		t.Fatal("a refused login still started a device flow")
	}
}

func TestLogin_GivenAnExpiredLoginThenLoggingInAgainIsAllowed(t *testing.T) {
	dir := accountDirs(t)
	fastLoginPoll(t)
	f := newFakeServer(t)
	f.saveLogin(t, dir)
	c, _ := agrctlcred.Load(dir)
	c.RefreshToken, c.AccessToken = "", ""
	if err := agrctlcred.Save(dir, c); err != nil {
		t.Fatal(err)
	}
	r := runWith([]string{"login"}, envMap(), human) // 默认沿用作废登录的 server
	if r.code != exitOK {
		t.Fatalf("code = %d, want 0 (stderr=%s)", r.code, r.stderr)
	}
	if c, _ := agrctlcred.Load(dir); c == nil || c.Expired() {
		t.Fatalf("credentials = %+v, want a live login", c)
	}
}

func TestLogin_GivenTheUserDeniesOrTheCodeExpiresThenExit1AndNothingIsSaved(t *testing.T) {
	for code, want := range map[string]string{"access_denied": "denied", "expired_token": "not approved in time"} {
		t.Run(code, func(t *testing.T) {
			dir := accountDirs(t)
			fastLoginPoll(t)
			f := newFakeServer(t)
			f.pollQueue = []string{code}
			r := runWith([]string{"login", "--server", f.srv.URL}, envMap(), human)
			if r.code != exitFailed || !strings.Contains(r.stderr, want) {
				t.Fatalf("code = %d, stderr = %q; want 1 and %q", r.code, r.stderr, want)
			}
			if _, err := agrctlcred.Load(dir); err == nil {
				t.Fatal("a failed login saved credentials")
			}
		})
	}
}

func TestLogin_GivenAnUnreachableServerThenExit1SayingSo(t *testing.T) {
	accountDirs(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + ln.Addr().String()
	_ = ln.Close()
	r := runWith([]string{"login", "--server", url}, envMap(), human)
	if r.code != exitFailed || !strings.Contains(r.stderr, "cannot reach the account server") {
		t.Fatalf("code = %d, stderr = %q; want 1 and the unreachable hint", r.code, r.stderr)
	}
}

func TestLogin_GivenABadServerURLThenUsageError(t *testing.T) {
	accountDirs(t)
	r := runWith([]string{"login", "--server", "ftp://x"}, envMap(), human)
	if r.code != exitUsage {
		t.Fatalf("code = %d, want 2 (stderr=%s)", r.code, r.stderr)
	}
}

func TestLogout_GivenALoginThenTheDeviceIsRevokedAndTheCredentialsDeleted(t *testing.T) {
	dir := accountDirs(t)
	f := newFakeServer(t)
	f.saveLogin(t, dir)
	r := runWith([]string{"logout"}, envMap(), human)
	if r.code != exitOK {
		t.Fatalf("code = %d, want 0 (stderr=%s)", r.code, r.stderr)
	}
	if f.revokeBody["device_id"] != float64(42) {
		t.Fatalf("revoke body = %v, want this device (42)", f.revokeBody)
	}
	if _, err := agrctlcred.Load(dir); err == nil {
		t.Fatal("credentials still on disk after logout")
	}
}

func TestLogout_GivenTheServerIsUnreachableThenTheCredentialsAreStillDeletedWithAHint(t *testing.T) {
	dir := accountDirs(t)
	f := newFakeServer(t)
	f.saveLogin(t, dir)
	f.srv.Close()
	r := runWith([]string{"logout"}, envMap(), human)
	if r.code != exitOK {
		t.Fatalf("code = %d, want 0 (stderr=%s)", r.code, r.stderr)
	}
	if !strings.Contains(r.stderr, "server-side revocation did not complete") || !strings.Contains(r.stderr, "device list") {
		t.Fatalf("stderr = %q, want the incomplete-revocation hint", r.stderr)
	}
	if _, err := agrctlcred.Load(dir); err == nil {
		t.Fatal("credentials still on disk after an offline logout")
	}
}

func TestLogout_GivenNotLoggedInThenExit1(t *testing.T) {
	accountDirs(t)
	r := runWith([]string{"logout"}, envMap(), human)
	if r.code != exitFailed || !strings.Contains(r.stderr, "not logged in") {
		t.Fatalf("code = %d, stderr = %q; want 1 and not logged in", r.code, r.stderr)
	}
}

func TestWhoami_GivenALoginThenItShowsServerAccountDeviceAndExecutorButNoCredential(t *testing.T) {
	dir := accountDirs(t)
	f := newFakeServer(t)
	f.saveLogin(t, dir)
	r := runWith([]string{"whoami"}, envMap(), human)
	if r.code != exitOK {
		t.Fatalf("code = %d, want 0 (stderr=%s)", r.code, r.stderr)
	}
	for _, want := range []string{f.srv.URL, "alice@example.com", "build-box", "executor", "account"} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("whoami = %q, want %q", r.stdout, want)
		}
	}
	for _, secret := range []string{"access-0", "refresh-0", "sha256:own"} {
		if strings.Contains(r.stdout+r.stderr, secret) {
			t.Fatalf("whoami leaks %q: %q", secret, r.stdout)
		}
	}
}

func TestWhoami_GivenASessionTokenThenTheExecutorIsTheSessionEvenWhenLoggedIn(t *testing.T) {
	dir := accountDirs(t)
	f := newFakeServer(t)
	f.saveLogin(t, dir)
	r := runWith([]string{"whoami"}, envMap("AGENTRE_CTL_ENDPOINT", "http://127.0.0.1:1", "AGENTRE_CTL_TOKEN", "sess"), human)
	if r.code != exitOK || !strings.Contains(r.stdout, "session") {
		t.Fatalf("code = %d, whoami = %q; want executor session", r.code, r.stdout)
	}
	if strings.Contains(r.stdout, "sess\n") {
		t.Fatalf("whoami leaks the session credential: %q", r.stdout)
	}
}

func TestWhoami_GivenNoLoginThenTheExecutorIsTheDesktop(t *testing.T) {
	accountDirs(t)
	r := runWith([]string{"whoami"}, envMap(), human)
	if r.code != exitOK || !strings.Contains(r.stdout, "desktop") || !strings.Contains(r.stdout, "not logged in") {
		t.Fatalf("code = %d, whoami = %q; want executor desktop and not logged in", r.code, r.stdout)
	}
}

func TestExecutor_GivenASessionTokenAndALoginThenTheSessionWins(t *testing.T) {
	dir := accountDirs(t)
	f := newFakeServer(t)
	f.saveLogin(t, dir)
	_, sess := newFakeExecutor(t, "sess")
	r := runWith([]string{"list", "agents"}, envMap("AGENTRE_CTL_ENDPOINT", sess.URL, "AGENTRE_CTL_TOKEN", "sess"), human)
	if r.code != exitOK || f.serverHits() != 0 {
		t.Fatalf("code = %d, server hits = %d (stderr=%s); want the session executor, not the account", r.code, f.serverHits(), r.stderr)
	}
}

func TestExecutor_GivenDesktopFlagAndALoginThenTheDesktopIsUsed(t *testing.T) {
	dir := accountDirs(t)
	f := newFakeServer(t)
	f.saveLogin(t, dir)
	_, desk := newFakeExecutor(t, "desk")
	if err := ctlendpoint.Write(dir, ctlendpoint.Endpoint{URL: desk.URL, Token: "desk"}); err != nil {
		t.Fatal(err)
	}
	r := runWith([]string{"list", "agents", "--desktop"}, envMap(), human)
	if r.code != exitOK || f.serverHits() != 0 || !strings.Contains(r.stdout, "architect") {
		t.Fatalf("code = %d, server hits = %d, stdout = %q (stderr=%s); want the desktop", r.code, f.serverHits(), r.stdout, r.stderr)
	}
	w := runWith([]string{"--desktop", "whoami"}, envMap(), human)
	if w.code != exitOK || !strings.Contains(w.stdout, "desktop") {
		t.Fatalf("whoami --desktop = %d %q", w.code, w.stdout)
	}
}

func TestExecutor_GivenDesktopFlagWithoutARunningDesktopThenExit1NotRunning(t *testing.T) {
	dir := accountDirs(t)
	f := newFakeServer(t)
	f.saveLogin(t, dir)
	for _, args := range [][]string{{"list", "agents", "--desktop"}, {"whoami", "--desktop"}} {
		r := runWith(args, envMap(), human)
		if r.code != exitFailed || !strings.Contains(r.stderr, "desktop is not running") {
			t.Fatalf("%v: code = %d, stderr = %q; want 1 and desktop is not running", args, r.code, r.stderr)
		}
	}
	if f.serverHits() != 0 {
		t.Fatal("--desktop fell back to the account")
	}
}

func TestExecutor_GivenALoginThenResourceVerbsGoToTheServer(t *testing.T) {
	dir := accountDirs(t)
	f := newFakeServer(t)
	f.saveLogin(t, dir)
	r := runWith([]string{"list", "agents"}, envMap(), term{})
	if r.code != exitOK || f.serverHits() == 0 || !strings.Contains(r.stdout, "architect") {
		t.Fatalf("code = %d, hits = %d, stdout = %q (stderr=%s); want the account server's agents", r.code, f.serverHits(), r.stdout, r.stderr)
	}
	g := runWith([]string{"get", "agent", "architect"}, envMap(), term{})
	if g.code != exitOK || !strings.Contains(g.stdout, `"architect"`) {
		t.Fatalf("get agent = %d %q (stderr=%s)", g.code, g.stdout, g.stderr)
	}
}

func TestExecutor_GivenAPersonalTokenThenTheServerIsCalledWithIt(t *testing.T) {
	accountDirs(t)
	f := newFakeServer(t)
	env := envMap("AGENTRE_TOKEN", personalToken, "AGENTRE_SERVER_URL", f.srv.URL)
	r := runWith([]string{"create", "department", "--name", "QA", "--parent", "总部"}, env, term{})
	if r.code != exitOK || f.exec.writeCount() != 1 {
		t.Fatalf("code = %d, writes = %d (stderr=%s); a personal token is not held back by the guardrail", r.code, f.exec.writeCount(), r.stderr)
	}
	w := runWith([]string{"whoami"}, env, term{})
	if !strings.Contains(w.stdout, "personal token") || strings.Contains(w.stdout, personalToken) {
		t.Fatalf("whoami = %q, want executor account via a personal token, no token characters", w.stdout)
	}
	bad := runWith([]string{"list", "agents"}, envMap("AGENTRE_TOKEN", "agr_pat_revoked", "AGENTRE_SERVER_URL", f.srv.URL), term{})
	if bad.code != exitFailed || !strings.Contains(bad.stderr, "rejected the personal token") {
		t.Fatalf("rejected token: code = %d, stderr = %q", bad.code, bad.stderr)
	}
}

func TestAccount_GivenALoginAndAnExternalProgramThenWritesAndChatAreRefusedButReadsWork(t *testing.T) {
	dir := accountDirs(t)
	f := newFakeServer(t)
	f.saveLogin(t, dir)
	for name, tm := range map[string]struct {
		term term
		env  func(string) (string, bool)
	}{
		"no terminal":           {term{}, envMap()},
		"agent CLI in terminal": {human, envMap("CLAUDECODE", "1")},
	} {
		t.Run(name, func(t *testing.T) {
			for _, args := range [][]string{
				{"create", "department", "--name", "QA", "--parent", "总部"},
				{"delete", "agent", "reviewer"},
				{"send", "--agent", "reviewer", "do it"},
				{"stop", sessA},
				{"answer", sessA, "q1", "--allow"},
			} {
				r := runWith(args, tm.env, tm.term)
				if r.code != exitFailed || !strings.Contains(r.stderr, "personal token") || !strings.Contains(r.stderr, "AGENTRE_TOKEN") {
					t.Fatalf("%v: code = %d, stderr = %q; want 1 and the issue-a-personal-token hint", args, r.code, r.stderr)
				}
			}
			if f.exec.writeCount() != 0 {
				t.Fatal("a refused write reached the server")
			}
			if r := runWith([]string{"list", "agents"}, tm.env, tm.term); r.code != exitOK {
				t.Fatalf("read refused for an external program: %d %s", r.code, r.stderr)
			}
		})
	}
}

func TestAccount_GivenALoginAndAPersonAtTheTerminalThenWritesGoToTheServer(t *testing.T) {
	dir := accountDirs(t)
	f := newFakeServer(t)
	f.saveLogin(t, dir)
	r := runWith([]string{"create", "department", "--name", "QA", "--parent", "总部"}, envMap(), human)
	if r.code != exitOK || !strings.Contains(r.stdout, "department QA created") {
		t.Fatalf("code = %d, stdout = %q (stderr=%s)", r.code, r.stdout, r.stderr)
	}
	if w := f.exec.onlyWrite(t); w.GetCaller() != agentrewire.CtlCaller_CTL_CALLER_HUMAN {
		t.Fatalf("write caller = %v, want HUMAN", w.GetCaller())
	}
}

func TestAccount_GivenAProjectPathThenTheServersRefusalIsShown(t *testing.T) {
	dir := accountDirs(t)
	f := newFakeServer(t)
	f.saveLogin(t, dir)
	r := runWith([]string{"update", "project", "agentre-hub", "--path", "/tmp/x"}, envMap(), human)
	if r.code != exitFailed || !strings.Contains(r.stderr, "local project path cannot be set through the server") {
		t.Fatalf("code = %d, stderr = %q; want the server's refusal verbatim", r.code, r.stderr)
	}
}

func TestAccount_GivenMemoryThenItIsRefusedOutsideASession(t *testing.T) {
	dir := accountDirs(t)
	f := newFakeServer(t)
	f.saveLogin(t, dir)
	for _, args := range [][]string{{"list", "memory"}, {"get", "memory", "m1"}, {"create", "memory", "--layer", "global", "--summary", "x"}} {
		r := runWith(args, envMap(), human)
		if r.code != exitFailed || !strings.Contains(r.stderr, "memory") || !strings.Contains(r.stderr, "--desktop") {
			t.Fatalf("%v: code = %d, stderr = %q; want 1 and the memory hint", args, r.code, r.stderr)
		}
	}
	if f.serverHits() != 0 {
		t.Fatal("memory reached the account server")
	}
}

func TestAccount_GivenARevokedDeviceThenCommandsFailWithTheReloginHint(t *testing.T) {
	dir := accountDirs(t)
	f := newFakeServer(t)
	f.saveLogin(t, dir)
	f.mu.Lock()
	f.revoked = true
	f.mu.Unlock()
	r := runWith([]string{"list", "agents"}, envMap(), human)
	if r.code != exitFailed || !strings.Contains(r.stderr, "agrctl login") {
		t.Fatalf("code = %d, stderr = %q; want 1 and run agrctl login again", r.code, r.stderr)
	}
	// 作废的登录仍是账号执行者：不悄悄换成本机桌面。
	again := runWith([]string{"list", "agents"}, envMap(), human)
	if again.code != exitFailed || !strings.Contains(again.stderr, "agrctl login") {
		t.Fatalf("second call: code = %d, stderr = %q", again.code, again.stderr)
	}
	if w := runWith([]string{"whoami"}, envMap(), human); !strings.Contains(w.stdout, "expired") {
		t.Fatalf("whoami = %q, want the login shown as expired", w.stdout)
	}
}

func TestHelp_GivenTheAccountVerbsThenEachHasUsageAndTheOverviewListsThem(t *testing.T) {
	for _, verb := range []string{"login", "logout", "whoami"} {
		r := runWith([]string{verb, "--help"}, envMap(), term{})
		if r.code != exitOK || !strings.HasPrefix(r.stdout, "agrctl "+verb) {
			t.Fatalf("%s --help: code = %d, stdout = %q", verb, r.code, r.stdout)
		}
	}
	r := runWith([]string{"help"}, envMap(), term{})
	for _, want := range []string{"agrctl login", "agrctl logout", "agrctl whoami", "--desktop", "AGENTRE_TOKEN"} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("help does not mention %q", want)
		}
	}
}

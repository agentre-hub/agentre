package ctlcmd

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agrctlcred"
)

// 设备管理（仅账号模式）：list / get / update / delete / upgrade device。

// deviceCalls 记下假 server 收到的改写类设备请求。
type deviceCalls struct {
	mu    sync.Mutex
	calls []string
	// deny 非空时，改写类请求一律 403 + 这条消息（个人 token 缺 devices:write）。
	deny string
	// upgrade 是升级接口的应答。
	upgrade map[string]any
}

func (d *deviceCalls) log() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.calls...)
}

func mountDeviceRoutes(w *world) *deviceCalls {
	d := &deviceCalls{upgrade: map[string]any{"accepted": true, "message": "upgrading to 0.9.1", "target_version": "0.9.1"}}
	handle := func(rw http.ResponseWriter, r *http.Request) {
		if !w.srv.authed(rw, r, true) {
			return
		}
		raw, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		d.calls = append(d.calls, r.Method+" "+r.URL.Path+" "+string(raw))
		deny, up := d.deny, d.upgrade
		d.mu.Unlock()
		if deny != "" {
			rw.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(rw).Encode(map[string]any{"code": 403, "msg": deny, "data": nil})
			return
		}
		if r.URL.Path == "/v1/devices/upgrade" {
			writeEnvelope(rw, up)
			return
		}
		writeEnvelope(rw, map[string]any{"display_name": "x"})
	}
	w.srv.mux.HandleFunc("PATCH /v1/devices/{id}", handle)
	w.srv.mux.HandleFunc("POST /v1/devices/upgrade", handle)
	return d
}

func TestListDevices_GivenALogin_ThenShowsNameKindStatusVersionAndTheCurrentDevice(t *testing.T) {
	newWorld(t)
	r := runWith([]string{"list", "devices"}, envMap(), term{})
	require.Equal(t, exitOK, r.code, r.stderr)
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	require.Len(t, lines, 4, r.stdout)
	assert.Regexp(t, `^ID\s+NAME\s+KIND\s+STATUS\s+VERSION\s+CURRENT`, lines[0])
	assert.Regexp(t, `^1\s+laptop\s+desktop\s+offline\s+0\.8\.0\s+-$`, lines[1])
	assert.Regexp(t, `^2\s+build-box\s+agentred\s+online\s+0\.9\.0\s+-$`, lines[2])
	assert.Regexp(t, `^42\s+build-box\s+cli\s+last used \d{4}-\d\d-\d\d \d\d:\d\d\s+agrctl abc1234\s+\*$`, lines[3], "a cli device shows last use, not online")
}

func TestListDevices_JSONShape(t *testing.T) {
	newWorld(t)
	r := runWith([]string{"list", "devices", "-o", "json"}, envMap(), term{})
	require.Equal(t, exitOK, r.code, r.stderr)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &rows), r.stdout)
	require.Len(t, rows, 3)
	assert.Equal(t, map[string]any{"id": float64(2), "name": "build-box", "kind": "agentred", "online": true, "version": "0.9.0", "current": false}, subset(rows[1], "id", "name", "kind", "online", "version", "current"))
	assert.Equal(t, true, rows[2]["current"])
	assert.NotContains(t, r.stdout, "fingerprint", "fingerprints are an internal detail")
}

func subset(m map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		out[k] = m[k]
	}
	return out
}

// mountFacts 让假 server 回答 GET /v1/devices/{id}/facts（agrctl 假定的读口，形状同
// syncwire.DeviceFactsReport）：只有 build-box（2）上报过。
func mountFacts(w *world) {
	w.srv.mux.HandleFunc("GET /v1/devices/{id}/facts", func(rw http.ResponseWriter, r *http.Request) {
		if !w.srv.authed(rw, r, true) {
			return
		}
		if r.PathValue("id") != "2" {
			writeEnvelope(rw, map[string]any{"reported": false})
			return
		}
		writeEnvelope(rw, map[string]any{"reported": true, "report": map[string]any{
			"collected_at": 1_760_000_000_000,
			"runtimes": []any{
				map[string]any{"runtime": "codex", "found": true, "version": "0.9.1", "path": "/usr/local/bin/codex", "below_minimum": false, "native_login": "loggedIn"},
				map[string]any{"runtime": "claudecode", "found": false, "below_minimum": false},
			},
			"service_engines": []any{
				map[string]any{"engine_sync_id": "EN-10", "runtime": "openclaw", "credential_present": true, "last_connection": "succeeded"},
			},
			"local_paths": []any{map[string]any{"project_sync_id": "PJ-1", "path": "/srv/agentre"}},
		}})
	})
}

func TestGetDevice_ListsTheRuntimesEndpointsAndProjectPathsItReported(t *testing.T) {
	w := newWorld(t)
	w.syncIDs()
	mountFacts(w)
	r := runWith([]string{"get", "device", "2"}, envMap(), term{})
	require.Equal(t, exitOK, r.code, r.stderr)
	var out struct {
		ID       int64 `json:"id"`
		Backends any   `json:"backends"`
		Runtimes []struct {
			Runtime     string `json:"runtime"`
			Found       bool   `json:"found"`
			Version     string `json:"version"`
			Path        string `json:"path"`
			NativeLogin string `json:"nativeLogin"`
		} `json:"runtimes"`
		Endpoints []struct {
			Engine         string `json:"engine"`
			Runtime        string `json:"runtime"`
			Credential     bool   `json:"credential"`
			LastConnection string `json:"lastConnection"`
		} `json:"endpoints"`
		ProjectPaths []struct {
			Project string `json:"project"`
			Path    string `json:"path"`
		} `json:"projectPaths"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &out), r.stdout)
	assert.Equal(t, int64(2), out.ID)
	assert.Nil(t, out.Backends, "no more \"backends bound to this device\"")
	require.Len(t, out.Runtimes, 2)
	assert.Equal(t, "codex", out.Runtimes[0].Runtime)
	assert.True(t, out.Runtimes[0].Found)
	assert.Equal(t, "0.9.1", out.Runtimes[0].Version)
	assert.Equal(t, "/usr/local/bin/codex", out.Runtimes[0].Path)
	assert.Equal(t, "loggedIn", out.Runtimes[0].NativeLogin)
	assert.False(t, out.Runtimes[1].Found)
	require.Len(t, out.Endpoints, 1)
	assert.Equal(t, "claw", out.Endpoints[0].Engine, "the engine's name, not its sync id")
	assert.Equal(t, "openclaw", out.Endpoints[0].Runtime)
	assert.True(t, out.Endpoints[0].Credential)
	assert.Equal(t, "succeeded", out.Endpoints[0].LastConnection)
	require.Len(t, out.ProjectPaths, 1)
	assert.Equal(t, "agentre", out.ProjectPaths[0].Project)
	assert.Equal(t, "/srv/agentre", out.ProjectPaths[0].Path)
	assert.NotContains(t, strings.ToLower(r.stdout), "secret")
	assert.NotContains(t, strings.ToLower(r.stdout), "token")
	assert.NotContains(t, strings.ToLower(r.stdout), "apikey")
}

func TestGetDevice_GivenNothingReportedThenItSaysSoAndKeepsTheRest(t *testing.T) {
	w := newWorld(t)
	mountFacts(w)
	r := runWith([]string{"get", "device", "1"}, envMap(), term{})
	require.Equal(t, exitOK, r.code, r.stderr)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &out), r.stdout)
	assert.Equal(t, "laptop", out["name"])
	assert.Equal(t, false, out["reported"])
	assert.NotContains(t, out, "runtimes")

	ambiguous := runWith([]string{"get", "device", "build-box"}, envMap(), term{})
	assert.Equal(t, exitUsage, ambiguous.code, "the agentred and the cli device share a name")
	assert.Contains(t, ambiguous.stderr, "ambiguous")

	missing := runWith([]string{"get", "device", "nope"}, envMap(), term{})
	assert.Equal(t, exitFailed, missing.code)
	assert.Contains(t, missing.stderr, `device "nope" not found`)
}

func TestUpdateDevice_RenamesThroughThePatchRoute(t *testing.T) {
	w := newWorld(t)
	d := mountDeviceRoutes(w)
	r := runWith([]string{"update", "device", "laptop", "--name", "work laptop"}, envMap(), human)
	require.Equal(t, exitOK, r.code, r.stderr)
	assert.Equal(t, []string{`PATCH /v1/devices/1 {"display_name":"work laptop"}`}, d.log())

	assert.Equal(t, exitUsage, runWith([]string{"update", "device", "laptop"}, envMap(), human).code, "--name is required")
	assert.Equal(t, exitUsage, runWith([]string{"update", "device", "laptop", "--name="}, envMap(), human).code, "a blank name is a usage error")
}

func TestDeviceWrites_GivenAPersonalTokenWithoutDevicesWrite_ThenTheServersRefusalIsReported(t *testing.T) {
	w := newWorld(t)
	d := mountDeviceRoutes(w)
	d.deny = "token lacks scope devices:write"
	w.srv.mu.Lock()
	w.srv.revokeDeny = d.deny
	w.srv.mu.Unlock()
	for _, args := range [][]string{
		{"update", "device", "laptop", "--name", "n"},
		{"delete", "device", "laptop"},
		{"upgrade", "device", "2"},
	} {
		r := runWith(args, patEnv(w), term{})
		assert.Equal(t, exitFailed, r.code, args)
		assert.Contains(t, r.stderr, "token lacks scope devices:write", args)
	}
}

func TestDeviceWrites_GivenALoginAndAnExternalProgram_ThenTheyAreRefusedBeforeAnyRequest(t *testing.T) {
	w := newWorld(t)
	d := mountDeviceRoutes(w)
	for _, args := range [][]string{
		{"update", "device", "laptop", "--name", "n"},
		{"delete", "device", "laptop"},
		{"upgrade", "device", "2"},
	} {
		r := runWith(args, envMap(), term{})
		assert.Equal(t, exitFailed, r.code, args)
		assert.Contains(t, r.stderr, "needs a person at a terminal", args)
	}
	assert.Empty(t, d.log())
}

func TestDeleteDevice_RevokesItAfterAHumanConfirms(t *testing.T) {
	w := newWorld(t)
	mountDeviceRoutes(w)
	refused := runWith([]string{"delete", "device", "laptop"}, envMap(), term{tty: true, stdin: "n\n"})
	assert.Equal(t, exitFailed, refused.code)
	assert.Contains(t, refused.stderr, "canceled")
	w.srv.mu.Lock()
	assert.Nil(t, w.srv.revokeBody, "nothing was revoked")
	w.srv.mu.Unlock()

	// 撤销的不是自己，登录态不受影响（假 server 的 revoke 会把整份登录标成已吊销，所以这里用个人 token 跑）。
	ok := runWith([]string{"delete", "device", "laptop"}, patEnv(w), term{})
	require.Equal(t, exitOK, ok.code, ok.stderr)
	w.srv.mu.Lock()
	assert.Equal(t, float64(1), w.srv.revokeBody["device_id"])
	w.srv.mu.Unlock()
	assert.Contains(t, ok.stdout, "deleted device laptop")
}

func TestDeleteDevice_TheCurrentDeviceIsALogout(t *testing.T) {
	w := newWorld(t)
	dir := os.Getenv("AGENTRE_DATA_DIR")
	r := runWith([]string{"delete", "device", "42"}, envMap(), term{tty: true, stdin: "y\n"})
	require.Equal(t, exitOK, r.code, r.stderr)
	w.srv.mu.Lock()
	assert.Equal(t, float64(42), w.srv.revokeBody["device_id"])
	w.srv.mu.Unlock()
	_, err := agrctlcred.Load(dir)
	assert.ErrorIs(t, err, os.ErrNotExist, "the local login is gone, exactly like agrctl logout")
	assert.Contains(t, r.stdout, "logged out")
}

func TestUpgradeDevice_AgentredOnly(t *testing.T) {
	w := newWorld(t)
	d := mountDeviceRoutes(w)
	ok := runWith([]string{"upgrade", "device", "2"}, envMap(), human)
	require.Equal(t, exitOK, ok.code, ok.stderr)
	assert.Equal(t, []string{`POST /v1/devices/upgrade {"device_id":2}`}, d.log())
	assert.Contains(t, ok.stdout, "upgrading to 0.9.1")

	desktop := runWith([]string{"upgrade", "device", "laptop"}, envMap(), human)
	assert.Equal(t, exitFailed, desktop.code)
	assert.Contains(t, desktop.stderr, "only an agentred device can be upgraded")
	assert.Len(t, d.log(), 1, "no request for a non-agentred device")

	d.mu.Lock()
	d.upgrade = map[string]any{"accepted": false, "reject_reason": "busy", "message": "2 turns are running"}
	d.mu.Unlock()
	busy := runWith([]string{"upgrade", "device", "2"}, envMap(), human)
	assert.Equal(t, exitFailed, busy.code)
	assert.Contains(t, busy.stderr, "2 turns are running")
}

func TestDevices_GivenTheDesktopOrASession_ThenTheyNeedALogin(t *testing.T) {
	_, srv := newFakeExecutor(t, "tok")
	accountDirs(t)
	for _, args := range [][]string{{"list", "devices"}, {"get", "device", "x"}, {"update", "device", "x", "--name", "n"}, {"delete", "device", "x"}, {"upgrade", "device", "x"}} {
		r := runWith(args, envFor(srv, "tok"), human)
		assert.Equal(t, exitFailed, r.code, args)
		assert.Contains(t, r.stderr, "device management needs an account login — run 'agrctl login'", args)
	}
}

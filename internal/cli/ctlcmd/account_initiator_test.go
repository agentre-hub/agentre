package ctlcmd

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// get session 的发起方：agentred 的 session.list 摘要里 peer_fingerprint 为空 = 调用方自己，
// 其余按账号设备名单 / 个人 token 认出人话标签；认不出的省略，不编造。

const (
	otherCliFP = "sha256:ci-runner"
	otherPatFP = "pat:someone-elses"
	sessX      = "0193b6f0-7c5e-7d2a-9f4b-3c1d2e5f6a90"
	sessY      = "0193b6f0-7c5e-7d2a-9f4b-3c1d2e5f6a91"
	sessZ      = "0193b6f0-7c5e-7d2a-9f4b-3c1d2e5f6a92"
	consoleFP  = "sha256:console-7"
)

func getDetail(t *testing.T, id string, env func(string) (string, bool)) map[string]any {
	t.Helper()
	r := runWith([]string{"get", "session", id}, env, term{})
	require.Equal(t, exitOK, r.code, r.stderr)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &out), r.stdout)
	return out
}

func initiatorWorld(t *testing.T) *world {
	w := listWorld(t)
	w.extraDevices = []map[string]any{{"id": 43, "name": "ci-runner", "kind": "cli", "fingerprint": otherCliFP, "online": false}}
	w.box.seed(sessX, otherCliFP, otherCliFP, "idle")
	w.box.seed(sessY, otherPatFP, otherPatFP, "idle")
	w.box.seed(sessZ, consoleFP, consoleFP, "idle")
	// server：登录态的 token 列表带每枚 token 的对端指纹，列表与 self 都带账号控制台的对端指纹。
	w.srv.mux.HandleFunc("GET /v1/personal-tokens", func(rw http.ResponseWriter, r *http.Request) {
		writeEnvelope(rw, map[string]any{"console_peer_fingerprint": consoleFP, "tokens": []map[string]any{
			{"name": "ci", "scopes": []string{"read"}, "peer_fingerprint": patFP},
			{"name": "nightly", "scopes": []string{"read"}, "peer_fingerprint": otherPatFP},
		}})
	})
	w.srv.mux.HandleFunc("GET /v1/personal-tokens/self", func(rw http.ResponseWriter, r *http.Request) {
		writeEnvelope(rw, map[string]any{"item": map[string]any{"name": "ci", "scopes": []string{"read"}}, "peer_fingerprint": patFP, "console_peer_fingerprint": consoleFP})
	})
	return w
}

func TestAccountGetSession_InitiatorOfTheCallersOwnSessionIsThisAgrctl(t *testing.T) {
	initiatorWorld(t)
	live := getDetail(t, sessA, envMap())
	assert.Equal(t, "this agrctl (build-box)", live["initiator"])
	assert.Equal(t, "cli", live["initiatorKind"])
	saved := getDetail(t, sessC, envMap()) // 离线设备上的已保存行，发起方同样来自索引
	assert.Equal(t, "this agrctl (build-box)", saved["initiator"])
}

func TestAccountGetSession_InitiatorOfAnotherCliDeviceIsItsName(t *testing.T) {
	initiatorWorld(t)
	out := getDetail(t, sessX, envMap())
	assert.Equal(t, "ci-runner", out["initiator"])
	assert.Equal(t, "cli", out["initiatorKind"])
}

func TestAccountGetSession_InitiatorOfATokenIsItsName(t *testing.T) {
	initiatorWorld(t)
	own := getDetail(t, sessP, envMap("AGENTRE_TOKEN", personalToken))
	assert.Equal(t, "ci", own["initiator"], "the token started it itself: its name")
	assert.Equal(t, "token", own["initiatorKind"])

	// 登录态读得到账号全部 token：别的 token 也说得出名字。
	other := getDetail(t, sessY, envMap())
	assert.Equal(t, "nightly", other["initiator"])
	assert.Equal(t, "token", other["initiatorKind"])

	// 个人 token 只看得到自己：别的 token 的名字不可见 —— 名字省略（不拿占位串冒充 token 名），
	// 种类仍如实是 token。
	hidden := getDetail(t, sessY, envMap("AGENTRE_TOKEN", personalToken))
	assert.NotContains(t, hidden, "initiator")
	assert.Equal(t, "token", hidden["initiatorKind"])
}

func TestAccountGetSession_InitiatorOfTheConsoleIsConsole(t *testing.T) {
	initiatorWorld(t)
	for name, env := range map[string]func(string) (string, bool){"login": envMap(), "personal token": envMap("AGENTRE_TOKEN", personalToken)} {
		out := getDetail(t, sessZ, env)
		assert.Equal(t, "console", out["initiator"], name)
		assert.Equal(t, "console", out["initiatorKind"], name)
	}
}

func TestAccountGetSession_InitiatorNobodyCanNameIsOmitted(t *testing.T) {
	initiatorWorld(t)
	out := getDetail(t, sessB, envMap()) // deskFP 不在设备名单里，也不是 token
	assert.NotContains(t, out, "initiator")
	assert.NotContains(t, out, "initiatorKind")
}

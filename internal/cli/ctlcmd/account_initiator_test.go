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

func TestAccountGetSession_InitiatorOfATokenIsItsNameForItsOwnCallerOnly(t *testing.T) {
	w := initiatorWorld(t)
	w.srv.mux.HandleFunc("GET /v1/personal-tokens/self", func(rw http.ResponseWriter, r *http.Request) {
		_, _ = rw.Write([]byte(`{"code":0,"data":{"item":{"name":"ci","scopes":["read"]},"peer_fingerprint":"` + patFP + `"}}`))
	})
	own := getDetail(t, sessP, envMap("AGENTRE_TOKEN", personalToken))
	assert.Equal(t, "ci", own["initiator"], "the token started it itself: its name")
	assert.Equal(t, "token", own["initiatorKind"])

	other := getDetail(t, sessY, envMap("AGENTRE_TOKEN", personalToken))
	assert.Equal(t, "personal token", other["initiator"], "another token's name is not visible to the caller")
	assert.Equal(t, "token", other["initiatorKind"])
}

func TestAccountGetSession_InitiatorNobodyCanNameIsOmitted(t *testing.T) {
	initiatorWorld(t)
	out := getDetail(t, sessB, envMap()) // deskFP 不在设备名单里，也不是 token
	assert.NotContains(t, out, "initiator")
	assert.NotContains(t, out, "initiatorKind")
}

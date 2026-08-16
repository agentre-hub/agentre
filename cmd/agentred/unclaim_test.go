package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-ai/agentre/internal/daemon/state"
)

// 与 login 同一条红线(见 TestLoginRefusesWhileDaemonIsRunningSoItCannotBeSilentlyReverted):
// 运行中的 daemon 会用内存里那份整文件覆写 state.json,所以此刻解除归属只是在磁盘上停留
// 几秒钟的假象。它尤其致命 —— 用户通常是先 unclaim 再 login,第一步就被回滚的话,第二步
// 会以「已归属」为由拒绝,或者登录成功却同样被覆盖。
func TestUnclaimRefusesWhileDaemonIsRunning(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)
	st.Mutate(func(s *state.State) { s.AccountID = "account-1" })
	require.NoError(t, st.Save())

	var networkCalls int
	cmd := newUnclaimCmdWithDeps(unclaimDeps{
		dataDir:       func() (string, error) { return dir, nil },
		daemonRunning: func() bool { return true },
		http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			networkCalls++
			return nil, assert.AnError
		})},
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(nil)

	err = cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agentred is running")
	assert.Equal(t, 0, networkCalls, "拒绝掉的 unclaim 不该顺手撤销一台仍在服务的设备")

	got, loadErr := state.Load(dir)
	require.NoError(t, loadErr)
	assert.True(t, got.IsClaimed(), "a refused unclaim must not report success it cannot keep")
}

// 从未登录过账号的 daemon 上 unclaim 仍然是纯本地操作（前置规格 R19）：没有服务端
// 地址、也没有凭据，无处可通知，因此一个请求都不该发出去。
func TestUnclaimRemovesClaimWhenNoDaemonIsRunning(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)
	st.Mutate(func(s *state.State) { s.AccountID = "account-1" })
	require.NoError(t, st.Save())

	var networkCalls int
	cmd := newUnclaimCmdWithDeps(unclaimDeps{
		dataDir:       func() (string, error) { return dir, nil },
		daemonRunning: func() bool { return false },
		http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			networkCalls++
			return nil, assert.AnError
		})},
	})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(nil)

	require.NoError(t, cmd.Execute())
	assert.Contains(t, out.String(), "removed")
	assert.Equal(t, 0, networkCalls, "没有服务端地址时 unclaim 仍是纯本地操作")

	got, loadErr := state.Load(dir)
	require.NoError(t, loadErr)
	assert.False(t, got.IsClaimed())
}

// 解除归属要先尽力通知账号侧「解除授权」，让控制台撤销与 `agentred unclaim` 走**同一条**
// 服务端路径（撤销 token 链、拉黑 jti、并把只属于这台机器的账号级同步对象落墓碑），
// 而不是两处各写一份慢慢漂移。
//
// 先换一张新的 access token 再调撤销：它是分钟级的（server AccessTTL=15m），而 unclaim
// 的前置条件正是「daemon 已经停了」——那份存盘的 access token 在真实场景里几乎总是过期
// 的，直接拿它去撤销等于这条通知永远打不出去。
func TestUnclaimBestEffortRevokesTheAccountAuthorizationBeforeClearingLocalState(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)

	var revokeAuth string
	var revokeBody map[string]any
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/oauth/token/refresh":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "refresh-token", body["refresh_token"])
			_, _ = io.WriteString(w, `{"access_token":"fresh-access","expires_in":900,`+
				`"refresh_token":"rotated","refresh_expires_in":7200}`)
		case "/v1/oauth/token/revoke":
			assert.Equal(t, http.MethodPost, r.Method)
			revokeAuth = r.Header.Get("Authorization")
			require.NoError(t, json.NewDecoder(r.Body).Decode(&revokeBody))
			_, _ = io.WriteString(w, `{"code":0,"data":{}}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	st.Mutate(func(s *state.State) {
		s.AccountID = "account-1"
		s.HubServerURL = server.URL
		s.Credential = state.AccountCredential{
			DeviceID:              9,
			AccessToken:           "stale-access",
			AccessTokenExpiresAt:  time.Now().Add(-time.Hour).Unix(),
			RefreshToken:          "refresh-token",
			RefreshTokenExpiresAt: time.Now().Add(24 * time.Hour).Unix(),
		}
	})
	require.NoError(t, st.Save())

	cmd := newUnclaimCmdWithDeps(unclaimDeps{
		dataDir:       func() (string, error) { return dir, nil },
		daemonRunning: func() bool { return false },
		http:          server.Client(),
	})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(nil)
	require.NoError(t, cmd.Execute())

	assert.Equal(t, []string{"/v1/oauth/token/refresh", "/v1/oauth/token/revoke"}, paths)
	assert.Equal(t, "Bearer fresh-access", revokeAuth)
	// 撤销的目标由凭据本身认定（设备 JWT 调用方只能撤自己），不靠参数点名。
	assert.Empty(t, revokeBody["device_id"])
	assert.Contains(t, out.String(), "removed")

	got, loadErr := state.Load(dir)
	require.NoError(t, loadErr)
	assert.False(t, got.IsClaimed())
}

// 用户执行了 unclaim 就必须解除归属：服务端不可达（机器搬走了、账号服务停了、
// 凭据早已被对面撤销）只记一行提示，绝不阻挡本地清理——否则一台再也连不上账号的
// 机器将永远无法回到未认领状态，也就永远无法重新配对或登录另一个账号。
func TestUnclaimClearsLocalClaimEvenWhenTheServerIsUnreachable(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)
	st.Mutate(func(s *state.State) {
		s.AccountID = "account-1"
		s.HubServerURL = "http://127.0.0.1:1"
		s.Credential = state.AccountCredential{
			DeviceID: 9, AccessToken: "stale-access", RefreshToken: "refresh-token",
		}
	})
	require.NoError(t, st.Save())

	cmd := newUnclaimCmdWithDeps(unclaimDeps{
		dataDir:       func() (string, error) { return dir, nil },
		daemonRunning: func() bool { return false },
		http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, assert.AnError
		})},
	})
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(nil)

	require.NoError(t, cmd.Execute())
	assert.Contains(t, out.String(), "removed")
	assert.NotEmpty(t, errOut.String(), "通知不到账号侧要如实说出来，否则用户以为设备已经从账号里消失了")

	got, loadErr := state.Load(dir)
	require.NoError(t, loadErr)
	assert.False(t, got.IsClaimed())
}

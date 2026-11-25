package ctlcmd

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
	"github.com/agentre-hub/agentre/internal/pkg/relaypeer/relaypeertest"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 账号模式（agrctl login / 个人 token）下的 send / wait / stop：经 relay 直连承载会话的那台
// 机器，flag、输出与退出码与桌面模式相同（spec「对话」「失败与恢复」）。

const sessB = "0193b6f0-7c5e-7d2a-9f4b-3c1d2e5f6a7c"

func patEnv(w *world) func(string) (string, bool) {
	return envMap("AGENTRE_TOKEN", personalToken, "AGENTRE_SERVER_URL", w.srv.srv.URL)
}

// Given 一个登录的人 When send 新会话 Then 按派发计划挑第一个可用的后端、在那台机器上开一轮，
// 覆盖、标题、项目与图片都随 runtime.run 过线，会话保存进账号，模型与力度钉在会话上。
func TestAccountSend_GivenALoginThenTheTurnRunsOnTheFirstAvailableBackendAndIsSaved(t *testing.T) {
	w := newWorld(t)
	img := writeTempFile(t, "shot.png", pngBytes)
	r := runWith([]string{"send", "--agent", "reviewer", "--project", "agentre", "--title", "Nightly",
		"--model", "openrouter/openai/gpt-5.1-mini", "--effort", "high", "--permission-mode", "plan",
		"--image", img, "ship", "it"}, envMap(), human)
	require.Equal(t, exitOK, r.code, r.stderr)

	runs := w.box.runRequests()
	require.Len(t, runs, 1)
	run := runs[0]
	id := strings.TrimSpace(r.stdout)
	assert.Equal(t, run.GetConversationId(), id, "stdout is the minted session id")
	assert.NoError(t, conversationid.Validate(id))
	assert.Contains(t, r.stderr, "dispatched to reviewer — build-box")
	assert.Equal(t, "AG-12", run.GetAgentSyncId())
	assert.Equal(t, "PJ-1", run.GetProjectSyncId())
	assert.Equal(t, "/srv/PJ-1", run.GetCwd(), "cwd comes from the dispatch plan")
	assert.Equal(t, "Nightly", run.GetTitle())
	assert.Equal(t, "ship it", run.GetUserText())
	assert.Equal(t, "codex", run.GetBackend().GetType())
	assert.Equal(t, "BE-9", run.GetBackend().GetSyncId(), "an agentred target gets the backend's sync id")
	assert.Equal(t, ownFP, run.GetSourceDevice(), "the source is the identity auth.account reported")
	assert.Equal(t, "build-box", run.GetSourceDeviceName())
	assert.Equal(t, "PV-4", run.GetLlmProviderKey())
	assert.Equal(t, "0b6c-22", run.GetLlmModelKey())
	assert.Equal(t, "high", run.GetReasoningEffort())
	assert.Equal(t, "plan", run.GetPermissionMode())
	require.Len(t, run.GetUserBlocks(), 1)
	assert.Equal(t, "image", run.GetUserBlocks()[0].GetType())
	var blk map[string]any
	require.NoError(t, json.Unmarshal(run.GetUserBlocks()[0].GetData(), &blk))
	assert.Equal(t, "image/png", blk["media_type"])

	q, _ := url.ParseQuery(w.planQueries()[0])
	assert.Equal(t, "AG-12", q.Get("agent_sync_id"))
	assert.Equal(t, "PJ-1", q.Get("project_sync_id"))
	require.Len(t, w.savedBodies(), 1)
	assert.Equal(t, map[string]any{"device_fingerprint": boxFP, "peer_fingerprint": ownFP, "conversation_id": id}, w.savedBodies()[0])
	assert.Equal(t, []string{"run", "model:PV-4/0b6c-22", "effort:high"}, w.box.callLog())
}

func TestAccountSend_GivenIsolatedOrJSONThenTheOutputsMatchTheDesktop(t *testing.T) {
	w := newWorld(t)
	r := runWith([]string{"send", "--agent", "reviewer", "--isolated", "--cli-model", "opus", "-o", "json", "go"}, envMap(), human)
	require.Equal(t, exitOK, r.code, r.stderr)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &out), r.stdout)
	assert.Equal(t, w.box.runRequests()[0].GetConversationId(), out["session"])
	assert.Equal(t, "running", out["status"])
	assert.Empty(t, w.savedBodies(), "--isolated is not saved to the account")
	assert.Equal(t, "fixed", w.box.runRequests()[0].GetCliModelMode())
	assert.Equal(t, "opus", w.box.runRequests()[0].GetCliModel())
	assert.Contains(t, w.box.callLog(), "cli-model:fixed=opus")

	// 没保存的会话 conversation: 解析不出机器，退回按机器在账号的在线设备上找。
	w.box.finish(out["session"].(string), "idle", "isolated answer", "")
	wr := runWith([]string{"wait", out["session"].(string)}, envMap(), human)
	assert.Equal(t, exitOK, wr.code, wr.stderr)
	assert.Equal(t, "isolated answer\n", wr.stdout)
}

func TestAccountSend_GivenDeviceOrBackendThenTheyPickWithinTheAgentsBackends(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		code   int
		target string
		stderr string
	}{
		{"device by name", []string{"--device", "build-box"}, exitOK, "BE-9", "build-box"},
		{"device by fingerprint", []string{"--device", boxFP}, exitOK, "BE-9", "build-box"},
		{"backend by name", []string{"--backend", "codex-remote"}, exitOK, "BE-9", "build-box"},
		{"device offline", []string{"--device", "laptop"}, exitFailed, "BE-5", "offline"},
		{"backend offline", []string{"--backend", "claude-local"}, exitFailed, "BE-5", "offline"},
		{"device not the agent's", []string{"--device", "nowhere"}, exitUsage, "", "one of: laptop, build-box"},
		{"backend not the agent's", []string{"--backend", "claw"}, exitUsage, "", "one of: claude-local, codex-remote"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			r := runWith(append(append([]string{"send", "--agent", "reviewer"}, c.args...), "go"), envMap(), human)
			assert.Equal(t, c.code, r.code, r.stderr)
			assert.Contains(t, r.stderr, c.stderr)
			plans := w.planQueries()
			if c.target != "" {
				q, _ := url.ParseQuery(plans[len(plans)-1])
				assert.Equal(t, c.target, q.Get("target_backend_sync_id"))
			}
			if c.code != exitOK {
				assert.Empty(t, w.box.runRequests(), "nothing ran")
			}
		})
	}
}

func TestAccountSend_GivenNoAvailableBackendThenEveryTierSaysWhy(t *testing.T) {
	w := newWorld(t)
	w.tiers["AG-12"][1].availability = "project_path_missing"
	r := runWith([]string{"send", "--agent", "reviewer", "--project", "agentre", "go"}, envMap(), human)
	assert.Equal(t, exitFailed, r.code)
	assert.Contains(t, r.stderr, "laptop: the device is offline")
	assert.Contains(t, r.stderr, "build-box: the project has no path on that device")
}

func TestAccountSend_GivenBadOverridesThenUsageErrorsBeforeAnythingRuns(t *testing.T) {
	w := newWorld(t)
	for _, args := range [][]string{
		{"--effort", "extreme"},
		{"--model", "openrouter/no-such-model"},
	} {
		r := runWith(append(append([]string{"send", "--agent", "reviewer"}, args...), "go"), envMap(), human)
		assert.Equal(t, exitUsage, r.code, "%v: %s", args, r.stderr)
		assert.Contains(t, r.stderr, "one of:")
	}
	assert.Empty(t, w.box.runRequests())
}

func TestAccountSend_GivenAPiAgentOnAgentredThenTheGenerationIsRegisteredPreparedAndStarted(t *testing.T) {
	w := newWorld(t)
	r := runWith([]string{"send", "--agent", "architect", "go"}, envMap(), human)
	require.Equal(t, exitOK, r.code, r.stderr)
	runs := w.box.runRequests()
	require.Len(t, runs, 3)
	owner := runs[0].GetPermissionMode()
	assert.True(t, strings.HasPrefix(owner, "web-pi-generation-"), owner)
	assert.Equal(t, owner, runs[1].GetPermissionMode())
	assert.Equal(t, owner, runs[2].GetPermissionMode())
	assert.Empty(t, runs[1].GetProviderSessionId())
	assert.Equal(t, "pi-2", runs[2].GetProviderSessionId(), "the start names the prepared generation")
	for _, run := range runs {
		assert.Equal(t, runs[0].GetConversationId(), run.GetConversationId())
	}
}

// ─── 继续 ───

func TestAccountSend_GivenASessionThenItContinuesUnderItsOriginPeer(t *testing.T) {
	w := newWorld(t)
	w.box.seed(sessA, "sha256:desk", "sha256:desk", "idle")
	w.relay.Save(sessA, boxFP)
	r := runWith([]string{"send", "--session", sessA, "--effort", "low", "more"}, envMap(), human)
	require.Equal(t, exitOK, r.code, r.stderr)
	assert.Equal(t, sessA+"\n", r.stdout)
	assert.Contains(t, r.stderr, "dispatched to reviewer")
	run := w.box.runRequests()[0]
	assert.Equal(t, "sha256:desk", run.GetPeerFingerprint(), "a session another peer started is named by its origin")
	assert.Equal(t, "/srv/PJ-1", run.GetCwd())
	assert.Equal(t, "AG-12", run.GetAgentSyncId())
	assert.Equal(t, "codex", run.GetBackend().GetType())
	assert.Equal(t, "low", run.GetReasoningEffort())
	assert.Equal(t, ownFP, run.GetSourceDevice())
	assert.Equal(t, []string{"effort:low", "run"}, w.box.callLog(), "the override is pinned for later turns before this one runs")
	assert.Empty(t, w.savedBodies(), "continuing does not save again")
}

// Given 一条会话上已经钉着模型、CLI 模型与思考力度（此前的覆盖、或控制台换过）When 不带覆盖
// 继续它 Then 这一轮照会话记下的设置跑（spec「模型类覆盖…对之后的轮次生效」，与控制台每轮
// 带上会话有效设置同一条）；带了覆盖的那一项以覆盖为准。
func TestAccountSend_GivenPinnedSettingsThenALaterTurnRunsWithThem(t *testing.T) {
	w := newWorld(t)
	s := w.box.seed(sessA, ownFP, ownFP, "idle")
	s.summary.ProviderKey, s.summary.ModelKey = "PV-4", "0b6c-22"
	s.summary.CliModelMode, s.summary.CliModel = "fixed", "opus"
	s.summary.ReasoningEffort = "high"
	w.relay.Save(sessA, boxFP)

	r := runWith([]string{"send", "--session", sessA, "more"}, envMap(), human)
	require.Equal(t, exitOK, r.code, r.stderr)
	run := w.box.runRequests()[0]
	assert.Equal(t, "PV-4", run.GetLlmProviderKey())
	assert.Equal(t, "0b6c-22", run.GetLlmModelKey())
	assert.Equal(t, "fixed", run.GetCliModelMode())
	assert.Equal(t, "opus", run.GetCliModel())
	assert.Equal(t, "high", run.GetReasoningEffort())

	w.box.finish(sessA, "idle", "done", "")
	r = runWith([]string{"send", "--session", sessA, "--effort", "low", "again"}, envMap(), human)
	require.Equal(t, exitOK, r.code, r.stderr)
	run = w.box.runRequests()[1]
	assert.Equal(t, "low", run.GetReasoningEffort(), "an override wins over the recorded setting")
	assert.Equal(t, "PV-4", run.GetLlmProviderKey(), "the settings not overridden stay as recorded")
	assert.Equal(t, "opus", run.GetCliModel())
}

func TestAccountSend_GivenARunningSessionThenSteerOrSayItIsBusy(t *testing.T) {
	w := newWorld(t)
	w.box.seed(sessA, ownFP, ownFP, "running")
	w.relay.Save(sessA, boxFP)

	busy := runWith([]string{"send", "--session", sessA, "more"}, envMap(), human)
	assert.Equal(t, exitFailed, busy.code)
	assert.Contains(t, busy.stderr, "use --steer to interject, or agrctl wait first")

	r := runWith([]string{"send", "--session", sessA, "--steer", "also", "this"}, envMap(), human)
	require.Equal(t, exitOK, r.code, r.stderr)
	assert.Contains(t, r.stderr, "steered into the running turn of reviewer")
	steers := w.box.steerRequests()
	require.Len(t, steers, 1)
	assert.Equal(t, "also this", steers[0].GetText())
	assert.Equal(t, ownFP, steers[0].GetPeerFingerprint())
	assert.NotEmpty(t, steers[0].GetQueuedId())
	assert.Empty(t, w.box.runRequests())

	// 会话不在运行时 --steer 按普通继续处理。
	w.box.finish(sessA, "idle", "done", "")
	again := runWith([]string{"send", "--session", sessA, "--steer", "next"}, envMap(), human)
	require.Equal(t, exitOK, again.code, again.stderr)
	assert.Len(t, w.box.runRequests(), 1)
}

func TestAccountSend_GivenAPersonalTokenThenOnlyItsOwnSessionsCanBeContinued(t *testing.T) {
	w := newWorld(t)
	w.box.seed(sessA, "sha256:desk", "sha256:desk", "idle")
	w.box.seed(sessB, patFP, patFP, "idle")
	w.relay.Save(sessA, boxFP)
	w.relay.Save(sessB, boxFP)

	foreign := runWith([]string{"send", "--session", sessA, "more"}, patEnv(w), term{})
	assert.Equal(t, exitFailed, foreign.code)
	assert.Contains(t, foreign.stderr, "token may only act on sessions it started")

	own := runWith([]string{"send", "--session", sessB, "more"}, patEnv(w), term{})
	require.Equal(t, exitOK, own.code, own.stderr)
	assert.Equal(t, patFP, w.box.runRequests()[0].GetSourceDevice())
	assert.Equal(t, "agrctl", w.box.runRequests()[0].GetSourceDeviceName())

	// 一个登录的人可以继续别的对端发起的会话。
	person := runWith([]string{"send", "--session", sessA, "more"}, envMap(), human)
	assert.Equal(t, exitOK, person.code, person.stderr)
}

// ─── 等待 ───

func TestAccountWait_GivenEachOutcomeThenItsTextOrPendingAndExitCode(t *testing.T) {
	t.Run("idle prints the last turn's final text", func(t *testing.T) {
		w := newWorld(t)
		w.box.seed(sessA, ownFP, ownFP, "running")
		w.relay.Save(sessA, boxFP)
		w.box.finish(sessA, "idle", "old answer", "")
		w.box.finish(sessA, "idle", "the final answer", "")
		r := runWith([]string{"wait", sessA}, envMap(), term{})
		assert.Equal(t, exitOK, r.code, r.stderr)
		assert.Equal(t, "the final answer\n", r.stdout)
	})
	t.Run("failed exits 1 with the error", func(t *testing.T) {
		w := newWorld(t)
		w.box.seed(sessA, ownFP, ownFP, "running")
		w.relay.Save(sessA, boxFP)
		w.box.finish(sessA, "failed", "", "provider exploded")
		r := runWith([]string{"wait", sessA}, envMap(), term{})
		assert.Equal(t, exitFailed, r.code)
		assert.Contains(t, r.stderr, "provider exploded")
	})
	t.Run("an approval card is waiting for input even though the flag is off", func(t *testing.T) {
		w := newWorld(t)
		w.box.seed(sessA, ownFP, ownFP, "running")
		w.relay.Save(sessA, boxFP)
		w.box.setPending(sessA, &agentrewire.SessionPendingWaitersResponse{Approvals: []*agentrewire.PendingApproval{
			{RequestId: "apr-1", Kind: "ctl", Title: "ctl_update_provider", Summary: []byte(`{"command":"agrctl update provider x"}`), Createtime: 7},
		}}, false)
		r := runWith([]string{"wait", sessA}, envMap(), term{})
		assert.Equal(t, exitWaiting, r.code, r.stderr)
		var out map[string]any
		require.NoError(t, json.Unmarshal([]byte(r.stdout), &out), r.stdout)
		approvals, _ := out["approvals"].([]any)
		require.Len(t, approvals, 1)
		assert.Equal(t, "apr-1", approvals[0].(map[string]any)["requestId"])
		assert.Equal(t, "agrctl update provider x", approvals[0].(map[string]any)["summary"].(map[string]any)["command"])
		assert.Contains(t, r.stderr, "waiting for input")
	})
	t.Run("a tool permission with -o json", func(t *testing.T) {
		w := newWorld(t)
		w.box.seed(sessA, ownFP, ownFP, "running")
		w.relay.Save(sessA, boxFP)
		w.box.setPending(sessA, &agentrewire.SessionPendingWaitersResponse{ToolPermissions: []*agentrewire.PendingToolPermission{
			{RequestId: "req-1", ToolName: "Bash", Input: []byte(`{"command":"ls"}`)},
		}}, true)
		r := runWith([]string{"wait", sessA, "-o", "json"}, envMap(), term{})
		assert.Equal(t, exitWaiting, r.code)
		var out map[string]any
		require.NoError(t, json.Unmarshal([]byte(r.stdout), &out), r.stdout)
		assert.Equal(t, sessA, out["session"])
		assert.Equal(t, "waiting", out["status"])
		perms := out["pending"].(map[string]any)["toolPermissions"].([]any)
		require.Len(t, perms, 1)
		assert.Equal(t, "req-1", perms[0].(map[string]any)["requestId"])
		assert.Equal(t, map[string]any{"command": "ls"}, perms[0].(map[string]any)["input"])
	})
	t.Run("timeout exits 5 and the session keeps running", func(t *testing.T) {
		w := newWorld(t)
		w.box.seed(sessA, ownFP, ownFP, "running")
		w.relay.Save(sessA, boxFP)
		r := runWith([]string{"wait", sessA, "--timeout", "50ms"}, envMap(), term{})
		assert.Equal(t, exitTimeout, r.code)
		assert.Contains(t, r.stderr, "still running")
	})
	t.Run("an unknown session fails", func(t *testing.T) {
		newWorld(t)
		r := runWith([]string{"wait", sessB}, envMap(), term{})
		assert.Equal(t, exitFailed, r.code)
		assert.Contains(t, r.stderr, "session not found")
	})
}

func TestAccountWait_GivenTheRelayDropsMidWaitThenItReconnectsAndStillGetsTheAnswer(t *testing.T) {
	w := newWorld(t)
	w.box.seed(sessA, ownFP, ownFP, "running")
	w.relay.Save(sessA, boxFP)
	w.box.listed = func(n int) {
		switch n {
		case 2:
			go w.relay.DropConnections()
		case 4:
			w.box.finish(sessA, "idle", "after the drop", "")
		}
	}
	r := runWith([]string{"wait", sessA}, envMap(), term{})
	assert.Equal(t, exitOK, r.code, r.stderr)
	assert.Equal(t, "after the drop\n", r.stdout, "the answer comes back once, not lost or doubled")
	assert.GreaterOrEqual(t, w.relay.Dials(), 2)
}

func TestAccountWait_GivenTheDeviceStaysOfflineBeyondTheTimeoutThenItTimesOut(t *testing.T) {
	w := newWorld(t)
	w.box.seed(sessA, ownFP, ownFP, "running")
	w.relay.Save(sessA, boxFP)
	w.relay.SetOffline(boxFP, true)
	r := runWith([]string{"wait", sessA, "--timeout", "80ms"}, envMap(), term{})
	assert.Equal(t, exitTimeout, r.code, r.stderr)
}

func TestAccountSendWait_GivenTheTurnEndsThenItPrintsTheAnswer(t *testing.T) {
	w := newWorld(t)
	w.box.onRun = func(req *agentrewire.RuntimeRunRequest) {
		time.Sleep(20 * time.Millisecond)
		w.box.finish(req.GetConversationId(), "idle", "all done", "")
	}
	r := runWith([]string{"send", "--agent", "reviewer", "--wait", "go"}, envMap(), human)
	assert.Equal(t, exitOK, r.code, r.stderr)
	assert.Equal(t, "all done\n", r.stdout)
}

// ─── 停止 ───

func TestAccountStop_GivenARunningTurnThenUndeliveredSteersAreDroppedAndCounted(t *testing.T) {
	w := newWorld(t)
	s := w.box.seed(sessA, "sha256:desk", "sha256:desk", "running")
	s.queued = []*agentrewire.ConsumedSteer{{QueuedId: "a", Text: "x"}, {QueuedId: "b", Text: "y"}}
	w.relay.Save(sessA, boxFP)
	r := runWith([]string{"stop", sessA}, envMap(), human)
	require.Equal(t, exitOK, r.code, r.stderr)
	assert.Contains(t, r.stdout, "dropped 2 undelivered steer message(s)")
	assert.Equal(t, []string{"drain", "abort:sha256:desk"}, w.box.callLog(),
		"steers are dropped before the turn ends, so agentred has none left to replay")

	idle := runWith([]string{"stop", sessA, "-o", "json"}, envMap(), human)
	assert.Equal(t, exitOK, idle.code)
	assert.Contains(t, idle.stdout, `"running": false`)
}

func TestAccountStop_GivenAnIdleSessionThenItSaysSo(t *testing.T) {
	w := newWorld(t)
	w.box.seed(sessA, ownFP, ownFP, "idle")
	w.relay.Save(sessA, boxFP)
	r := runWith([]string{"stop", sessA}, envMap(), human)
	assert.Equal(t, exitOK, r.code)
	assert.Contains(t, r.stderr, "session is not running")
	assert.Empty(t, w.box.callLog())
}

// ─── 失败与恢复 ───

func TestAccountSend_GivenTheDeviceRunsAnotherReleaseThenItNamesTheDeviceToUpgrade(t *testing.T) {
	w := newWorld(t)
	w.relay.AddMachine(&relaypeertest.Machine{Fingerprint: boxFP, Registry: w.box.registry(), Identify: identify, RejectVersion: true})
	r := runWith([]string{"send", "--agent", "reviewer", "go"}, envMap(), human)
	assert.Equal(t, exitFailed, r.code)
	assert.Contains(t, r.stderr, "device build-box needs to be upgraded")
	assert.Empty(t, w.box.runRequests())
}

func TestAccountSend_GivenTheSessionsDeviceIsOfflineThenItFailsNamingIt(t *testing.T) {
	w := newWorld(t)
	w.box.seed(sessA, ownFP, ownFP, "idle")
	w.relay.Save(sessA, boxFP)
	w.relay.SetOffline(boxFP, true)
	for _, args := range [][]string{{"send", "--session", sessA, "more"}, {"stop", sessA}} {
		r := runWith(args, envMap(), human)
		assert.Equal(t, exitFailed, r.code, "%v", args)
		assert.Contains(t, r.stderr, "offline")
	}
}

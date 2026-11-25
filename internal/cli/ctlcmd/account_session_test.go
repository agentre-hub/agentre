package ctlcmd

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 账号模式下的会话查询与挂起交互：list / get / messages / tail / pending / answer。
// 命令的 flag、输出与退出码与桌面模式相同（conversation_test.go），数据来自账号索引、承载
// 会话的机器（经 relay）与 server 的镜像。

const (
	sessC  = "0193b6f0-7c5e-7d2a-9f4b-3c1d2e5f6a7d" // 笔记本上保存过的会话（离线）
	sessD  = "0193b6f0-7c5e-7d2a-9f4b-3c1d2e5f6a7e" // 笔记本上保存过、镜像里没有内容
	sessP  = "0193b6f0-7c5e-7d2a-9f4b-3c1d2e5f6a7f" // 个人 token 自己发起的会话
	deskFP = "sha256:desk"
)

// listWorld：sessA 保存过且在 box 上（本机 cli 发起），sessB 只在 box 上（别的对端发起，没保存），
// sessC 保存过、在离线的笔记本上，sessP 在 box 上、由个人 token 发起。
func listWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.box.seed(sessA, ownFP, ownFP, "idle").summary.LastMessageAt = 1_700_000_300_000
	w.box.seed(sessB, deskFP, deskFP, "running").summary.LastMessageAt = 1_700_000_200_000
	w.box.session(sessB).summary.AgentSyncId = "AG-3"
	w.box.session(sessB).summary.Title = "desktop run"
	w.box.seed(sessP, patFP, patFP, "idle").summary.LastMessageAt = 1_700_000_050_000
	w.indexRow(sessA, ownFP, boxFP, "stale title", "running", 1_700_000_000_000)
	w.indexRow(sessC, ownFP, laptopFP, "laptop one", "running", 1_700_000_100_000)
	w.indexRow(sessP, patFP, boxFP, "pat run", "idle", 1_700_000_050_000)
	return w
}

func TestAccountListSessions_GivenSavedAndLiveThenTheyMergeAndOfflineIsMarked(t *testing.T) {
	listWorld(t)
	r := runWith([]string{"list", "sessions", "-o", "json"}, envMap(), term{})
	require.Equal(t, exitOK, r.code, r.stderr)
	rows := decodeRows(r.stdout)
	assert.Equal(t, strings.Join([]string{sessA, sessB, sessC, sessP}, ","), rowIDs(rows), "newest first; the unsaved live one is included, nothing twice")

	byID := map[string]map[string]any{}
	for _, row := range rows {
		byID[row["id"].(string)] = row
	}
	assert.Equal(t, "idle", byID[sessA]["status"], "a live answer beats the stale index row")
	assert.Equal(t, "earlier", byID[sessA]["title"])
	assert.Equal(t, "reviewer", byID[sessA]["agent"])
	assert.Equal(t, "build-box", byID[sessA]["device"])
	assert.Equal(t, "running", byID[sessB]["status"])
	assert.Equal(t, "laptop", byID[sessC]["device"], "an offline device's saved session shows its last known state, marked")
	assert.Equal(t, "running", byID[sessC]["status"])
	assert.Equal(t, true, byID[sessC]["deviceOffline"])
	assert.Equal(t, "2023-11-14T22:18:20Z", byID[sessA]["lastActivityAt"])
}

func TestAccountListSessions_GivenTheTableThenItHasTheDesktopColumns(t *testing.T) {
	listWorld(t)
	r := runWith([]string{"list", "sessions"}, envMap(), term{})
	require.Equal(t, exitOK, r.code, r.stderr)
	assert.Regexp(t, `^ID\s+TITLE\s+AGENT\s+DEVICE\s+STATUS\s+LAST ACTIVITY`, r.stdout)
	assert.Contains(t, r.stdout, sessC)
	assert.Contains(t, r.stdout, "laptop (offline)")
}

func TestAccountListSessions_GivenFiltersThenOnlyMatchingRowsRemain(t *testing.T) {
	listWorld(t)
	cases := []struct {
		name string
		args []string
		env  func(string) (string, bool)
		want string
	}{
		{"mine (login) = started by this cli device", []string{"--mine"}, envMap(), sessA + "," + sessC},
		{"mine (personal token) = started by this token", []string{"--mine"}, envMap("AGENTRE_TOKEN", personalToken), sessP},
		{"status", []string{"--status", "running"}, envMap(), sessB + "," + sessC},
		{"agent", []string{"--agent", "reviewer"}, envMap(), sessA + "," + sessC + "," + sessP},
		{"device", []string{"--device", "laptop"}, envMap(), sessC},
		{"limit", []string{"--limit", "2"}, envMap(), sessA + "," + sessB},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := runWith(append([]string{"list", "sessions", "-o", "json"}, c.args...), c.env, term{})
			require.Equal(t, exitOK, r.code, r.stderr)
			assert.Equal(t, c.want, rowIDs(decodeRows(r.stdout)))
		})
	}
}

func TestAccountListSessions_GivenAnExternalProgramThenReadingStillWorks(t *testing.T) {
	listWorld(t)
	r := runWith([]string{"list", "sessions", "-o", "json"}, envMap(), term{})
	assert.Equal(t, exitOK, r.code, "the logged-in account is readable by programs: %s", r.stderr)
	a := runWith([]string{"answer", sessA, "req-1", "--allow"}, envMap(), term{})
	assert.Equal(t, exitFailed, a.code)
	assert.Contains(t, a.stderr, "needs a person at a terminal")
}

func TestAccountGetSession_GivenLiveThenFieldsComeFromTheMachineAndUnknownOnesAreOmitted(t *testing.T) {
	w := listWorld(t)
	w.box.setPending(sessA, &agentrewire.SessionPendingWaitersResponse{ToolPermissions: []*agentrewire.PendingToolPermission{{RequestId: "r1", ToolName: "Bash"}}}, true)
	r := runWith([]string{"get", "session", sessA}, envMap(), term{})
	require.Equal(t, exitOK, r.code, r.stderr)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &out), r.stdout)
	assert.Equal(t, sessA, out["id"])
	assert.Equal(t, "earlier", out["title"])
	assert.Equal(t, "reviewer", out["agent"])
	assert.Equal(t, "build-box", out["device"])
	assert.Equal(t, "codex", out["backend"])
	assert.Equal(t, "waiting", out["status"], "an idle lifecycle with a card waiting reads as waiting")
	assert.EqualValues(t, 1, out["pending"])
	assert.NotContains(t, out, "usage", "what the machine does not report is not invented")
	assert.NotContains(t, out, "createdAt")
	assert.NotContains(t, out, "deviceOffline")
}

func TestAccountGetSession_GivenTheDeviceIsOfflineThenTheSavedRowAnswersWithItsLastKnownState(t *testing.T) {
	listWorld(t)
	r := runWith([]string{"get", "session", sessC}, envMap(), term{})
	require.Equal(t, exitOK, r.code, r.stderr)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &out), r.stdout)
	assert.Equal(t, "laptop one", out["title"])
	assert.Equal(t, "running", out["status"])
	assert.Equal(t, "laptop", out["device"])
	assert.Equal(t, true, out["deviceOffline"])
	assert.NotContains(t, out, "pending")

	gone := runWith([]string{"get", "session", sessB2}, envMap(), term{})
	assert.Equal(t, exitFailed, gone.code)
	assert.Contains(t, gone.stderr, "session not found")
}

const sessB2 = "0193b6f0-7c5e-7d2a-9f4b-3c1d2e5f6a80"

func TestAccountMessages_GivenALiveSessionThenTurnsPageBackwardWithACursor(t *testing.T) {
	w := listWorld(t)
	w.box.addTurn(sessA, "one?", "uno", false)
	second := w.box.addTurn(sessA, "two?", "dos", true)
	r := runWith([]string{"messages", sessA}, envMap(), term{})
	require.Equal(t, exitOK, r.code, r.stderr)
	assert.Equal(t, "user: one?\nassistant: uno\nuser: two?\nassistant: dos\n", r.stdout)

	last := runWith([]string{"messages", sessA, "--last", "1", "-o", "json"}, envMap(), term{})
	require.Equal(t, exitOK, last.code, last.stderr)
	var page messagesPage
	require.NoError(t, json.Unmarshal([]byte(last.stdout), &page), last.stdout)
	require.Len(t, page.Messages, 2)
	assert.Equal(t, "two?", page.Messages[0].Text)
	assert.Equal(t, "dos", page.Messages[1].Text)
	assert.True(t, page.HasMore)
	assert.Equal(t, strconv.FormatInt(second, 10), page.Before)

	human := runWith([]string{"messages", sessA, "--last", "1"}, envMap(), term{})
	assert.Contains(t, human.stderr, "earlier messages exist: agrctl messages "+sessA+" --before "+page.Before)

	older := runWith([]string{"messages", sessA, "--before", page.Before}, envMap(), term{})
	require.Equal(t, exitOK, older.code, older.stderr)
	assert.Equal(t, "user: one?\nassistant: uno\n", older.stdout)

	full := runWith([]string{"messages", sessA, "--last", "1", "--full"}, envMap(), term{})
	assert.Contains(t, full.stdout, `  tool_use Bash {"command":"ls"}`)
	assert.Contains(t, full.stdout, "  tool_result a.txt")
}

func TestAccountMessages_GivenTheDeviceIsOfflineThenTheServerMirrorAnswersTheSameWay(t *testing.T) {
	w := listWorld(t)
	w.mirrorTurn(sessC, "one?", "uno", false)
	w.mirrorTurn(sessC, "two?", "dos", true)
	w.mirrorTurn(sessC, "three?", "tres", false)
	r := runWith([]string{"messages", sessC, "--last", "3"}, envMap(), term{})
	require.Equal(t, exitOK, r.code, r.stderr)
	assert.Equal(t, "user: one?\nassistant: uno\nuser: two?\nassistant: dos\nuser: three?\nassistant: tres\n", r.stdout,
		"pages of the mirror are walked backward until enough turns are in")

	one := runWith([]string{"messages", sessC, "--last", "1", "-o", "json"}, envMap(), term{})
	var page messagesPage
	require.NoError(t, json.Unmarshal([]byte(one.stdout), &page), one.stdout)
	require.Len(t, page.Messages, 2)
	assert.Equal(t, "three?", page.Messages[0].Text)
	assert.True(t, page.HasMore)
	older := runWith([]string{"messages", sessC, "--last", "1", "--before", page.Before}, envMap(), term{})
	assert.Equal(t, "user: two?\nassistant: dos\n", older.stdout)
}

func TestAccountMessages_GivenOfflineAndNothingMirroredThenItSaysTheDeviceIsOffline(t *testing.T) {
	w := listWorld(t)
	w.indexRow(sessD, ownFP, laptopFP, "never synced", "idle", 1)
	r := runWith([]string{"messages", sessD}, envMap(), term{})
	assert.Equal(t, exitFailed, r.code)
	assert.Contains(t, r.stderr, "offline")
	unknown := runWith([]string{"messages", sessB2}, envMap(), term{})
	assert.Equal(t, exitFailed, unknown.code)
	assert.Contains(t, unknown.stderr, "session not found")
}

func tailLines(t *testing.T, out string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(l), &m), l)
		lines = append(lines, m)
	}
	return lines
}

func TestAccountTail_GivenFromThenMissedFramesComeFirstAndUntilIdleExitsZero(t *testing.T) {
	w := listWorld(t)
	first := w.box.addTurn(sessA, "hi", "hello", true)
	r := runWith([]string{"tail", sessA, "--from", "v1:0", "--until-idle"}, envMap(), term{})
	require.Equal(t, exitOK, r.code, r.stderr)
	lines := tailLines(t, r.stdout)
	var types []string
	for _, l := range lines {
		types = append(types, l["type"].(string))
	}
	assert.Equal(t, []string{"user_message", "tool_call", "tool_result", "text", "turn_done", "status"}, types)
	assert.Equal(t, "v1:"+strconv.FormatInt(first, 10), lines[0]["cursor"])
	assert.Equal(t, "idle", lines[len(lines)-1]["status"])
	assert.Equal(t, "hello", lines[3]["data"].(map[string]any)["text"])
	assert.Equal(t, "Bash", lines[1]["data"].(map[string]any)["name"])

	// 从中间的游标续：只补它之后的。
	again := runWith([]string{"tail", sessA, "--from", lines[3]["cursor"].(string), "--until-idle"}, envMap(), term{})
	require.Equal(t, exitOK, again.code, again.stderr)
	var rest []string
	for _, l := range tailLines(t, again.stdout) {
		rest = append(rest, l["type"].(string))
	}
	assert.Equal(t, []string{"turn_done", "status"}, rest)
}

func TestAccountTail_GivenAWaitingSessionThenUntilIdleExitsFour(t *testing.T) {
	w := listWorld(t)
	w.box.session(sessA).summary.LifecycleState = "running"
	w.box.setPending(sessA, &agentrewire.SessionPendingWaitersResponse{AskUserQuestions: []*agentrewire.PendingAskUserQuestion{{RequestId: "q1"}}}, true)
	r := runWith([]string{"tail", sessA, "--until-idle"}, envMap(), term{})
	assert.Equal(t, exitWaiting, r.code, r.stderr)
	assert.Contains(t, r.stdout, `"status":"waiting"`)
}

func TestAccountTail_GivenNoFromThenOnlyNewFramesAreFollowedAndAnInvalidCursorIsRefused(t *testing.T) {
	w := listWorld(t)
	w.box.addTurn(sessA, "old", "old answer", false)
	w.box.session(sessA).summary.LifecycleState = "running"
	w.box.listed = func(n int) {
		if n == 3 {
			w.box.addTurn(sessA, "new", "new answer", false)
			w.box.session(sessA).summary.LifecycleState = "idle"
		}
	}
	r := runWith([]string{"tail", sessA, "--until-idle"}, envMap(), term{})
	require.Equal(t, exitOK, r.code, r.stderr)
	assert.NotContains(t, r.stdout, "old answer")
	assert.Contains(t, r.stdout, "new answer")

	bad := runWith([]string{"tail", sessA, "--from", "nonsense"}, envMap(), term{})
	assert.Equal(t, exitUsage, bad.code)
	assert.Contains(t, bad.stderr, "agrctl messages")
}

func TestAccountTail_GivenTheRelayDropsThenItResumesFromTheLastCursorWithoutRepeats(t *testing.T) {
	w := listWorld(t)
	w.box.session(sessA).summary.LifecycleState = "running"
	w.box.listed = func(n int) {
		switch n {
		case 2:
			w.box.addTurn(sessA, "q", "partial", false)
			go w.relay.DropConnections()
		case 5:
			w.box.addTurn(sessA, "q2", "rest", false)
			w.box.session(sessA).summary.LifecycleState = "idle"
		}
	}
	r := runWith([]string{"tail", sessA, "--from", "v1:0", "--until-idle"}, envMap(), term{})
	require.Equal(t, exitOK, r.code, r.stderr)
	assert.Equal(t, 1, strings.Count(r.stdout, `"text":"partial"`))
	assert.Equal(t, 1, strings.Count(r.stdout, `"text":"rest"`))
	assert.GreaterOrEqual(t, w.relay.Dials(), 2)
}

func pendingCards() *agentrewire.SessionPendingWaitersResponse {
	return &agentrewire.SessionPendingWaitersResponse{
		ToolPermissions: []*agentrewire.PendingToolPermission{{RequestId: "perm-1", ToolName: "Bash", Input: []byte(`{"command":"ls"}`)}},
		AskUserQuestions: []*agentrewire.PendingAskUserQuestion{{RequestId: "ask-1", Questions: []*agentrewire.AskQuestion{{
			Question: "Which?", Options: []*agentrewire.AskOption{{Label: "A"}, {Label: "B"}}}}}},
		Approvals: []*agentrewire.PendingApproval{{RequestId: "appr-1", Kind: "ctl", Title: "ctl_update_provider", Summary: []byte(`{"changes":[]}`), Createtime: 5}},
	}
}

func TestAccountPending_GivenCardsThenQuestionsPermissionsAndApprovalsAreAllListed(t *testing.T) {
	w := listWorld(t)
	w.box.setPending(sessA, pendingCards(), true)
	r := runWith([]string{"pending", sessA, "-o", "json"}, envMap(), term{})
	require.Equal(t, exitOK, r.code, r.stderr)
	var out struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &out), r.stdout)
	require.Len(t, out.Items, 3)
	kinds := map[string]map[string]any{}
	for _, it := range out.Items {
		assert.Equal(t, sessA, it["session"])
		kinds[it["id"].(string)] = it
	}
	assert.Equal(t, "tool_permission", kinds["perm-1"]["kind"])
	assert.Equal(t, "Bash", kinds["perm-1"]["tool"])
	assert.Equal(t, "question", kinds["ask-1"]["kind"])
	assert.Equal(t, "approval", kinds["appr-1"]["kind"])
	assert.Equal(t, "ctl_update_provider", kinds["appr-1"]["title"])

	table := runWith([]string{"pending", sessA}, envMap(), term{})
	assert.Regexp(t, `^ID\s+SESSION\s+KIND\s+SUMMARY`, table.stdout)
}

func TestAccountPending_GivenNoSessionThenEveryWaitingSessionOnOnlineDevicesIsCovered(t *testing.T) {
	w := listWorld(t)
	w.box.setPending(sessA, pendingCards(), true)
	w.box.setPending(sessP, &agentrewire.SessionPendingWaitersResponse{AskUserQuestions: []*agentrewire.PendingAskUserQuestion{{RequestId: "ask-p"}}}, true)
	r := runWith([]string{"pending", "-o", "json"}, envMap(), term{})
	require.Equal(t, exitOK, r.code, r.stderr)
	assert.Contains(t, r.stdout, "ask-p")
	assert.Contains(t, r.stdout, "appr-1")
	assert.Equal(t, 4, strings.Count(r.stdout, `"session"`))
}

func TestAccountAnswer_GivenEachCardKindThenItGoesToTheMatchingMethodWithTheOrigin(t *testing.T) {
	w := listWorld(t)
	w.box.setPending(sessA, pendingCards(), true)

	q := runWith([]string{"answer", sessA, "ask-1", "--option", "2"}, envMap(), human)
	require.Equal(t, exitOK, q.code, q.stderr)
	assert.Equal(t, "answered\n", q.stdout)
	log := w.box.answered()
	require.Len(t, log.answers, 1)
	assert.Equal(t, "ask-1", log.answers[0].GetRequestId())
	assert.Equal(t, ownFP, log.answers[0].GetPeerFingerprint())
	assert.Equal(t, []string{"B"}, log.answers[0].GetAnswers()[0].GetLabels())

	txt := runWith([]string{"answer", sessA, "ask-1", "--text", "custom", "-o", "json"}, envMap(), human)
	require.Equal(t, exitOK, txt.code, txt.stderr)
	assert.JSONEq(t, `{"session":"`+sessA+`","id":"ask-1","answered":true,"kind":"question"}`, txt.stdout)
	assert.Equal(t, "custom", w.box.answered().answers[1].GetAnswers()[0].GetOtherText())

	deny := runWith([]string{"answer", sessA, "perm-1", "--deny", "--message", "no"}, envMap(), human)
	require.Equal(t, exitOK, deny.code, deny.stderr)
	perm := w.box.answered().permissions[0]
	assert.False(t, perm.GetAllow())
	assert.Equal(t, "no", perm.GetDenyReason())
	assert.Equal(t, ownFP, perm.GetPeerFingerprint())

	ok := runWith([]string{"answer", sessA, "appr-1", "--allow"}, envMap(), human)
	require.Equal(t, exitOK, ok.code, ok.stderr)
	appr := w.box.answered().approvals[0]
	assert.Equal(t, sessA, appr.GetConversationId())
	assert.True(t, appr.GetAllow())
}

func TestAccountAnswer_GivenTheWrongAnswerFormOrAGoneItemThenItFailsWithoutCallingAnything(t *testing.T) {
	w := listWorld(t)
	w.box.setPending(sessA, pendingCards(), true)
	cases := []struct {
		name string
		args []string
		code int
		msg  string
	}{
		{"approval needs allow/deny", []string{"appr-1", "--option", "1"}, exitUsage, "approval"},
		{"permission needs allow/deny", []string{"perm-1", "--text", "x"}, exitUsage, "--allow"},
		{"question needs option/text", []string{"ask-1", "--allow"}, exitUsage, "--option"},
		{"unknown option", []string{"ask-1", "--option", "Z"}, exitUsage, "is not one of: A, B"},
		{"gone item", []string{"nope", "--allow"}, exitFailed, "no longer exists"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := runWith(append([]string{"answer", sessA}, c.args...), envMap(), human)
			assert.Equal(t, c.code, r.code, r.stderr)
			assert.Contains(t, r.stderr, c.msg)
		})
	}
	l := w.box.answered()
	assert.Empty(t, l.answers)
	assert.Empty(t, l.permissions)
	assert.Empty(t, l.approvals)

	// 展示时还在、作答时已被别处答掉：agentred 的「不存在」同样说成「该项已不存在」。
	l.approvalGone = true
	gone := runWith([]string{"answer", sessA, "appr-1", "--allow"}, envMap(), human)
	assert.Equal(t, exitFailed, gone.code)
	assert.Contains(t, gone.stderr, "no longer exists")
}

func TestAccountAnswer_GivenAPersonalTokenOnAForeignSessionThenTheRefusalIsPassedThrough(t *testing.T) {
	w := listWorld(t)
	w.box.setPending(sessA, pendingCards(), true) // sessA 由 cli 设备发起，个人 token 不能动
	r := runWith([]string{"answer", sessA, "perm-1", "--allow"}, envMap("AGENTRE_TOKEN", personalToken), term{})
	assert.Equal(t, exitFailed, r.code)
	assert.Contains(t, r.stderr, "token may only act on sessions it started")
	assert.Empty(t, w.box.answered().permissions)
}

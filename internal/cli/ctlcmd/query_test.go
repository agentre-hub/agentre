package ctlcmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sessionsReply = `{"sessions":[
 {"id":"` + sessA + `","title":"Fix the build","agent":"planner","device":"this desktop","status":"running","lastActivityAt":"2026-11-25T10:00:00Z"},
 {"id":"0193b6f0-7c5e-7d2a-9f4b-3c1d2e5f6a7c","title":"","agent":"coder","device":"build-box","status":"waiting"}]}`

func (f *convServer) on(route string, h func(w http.ResponseWriter, body map[string]any)) {
	if f.query == nil {
		f.query = map[string]func(w http.ResponseWriter, body map[string]any){}
	}
	f.query[route] = h
}

// Given 会话列表 When list sessions Then 表格首列是完整会话 id,另含标题 / agent / 设备 / 状态 / 最近活动;
// -o json 原样给出稳定字段。
func TestListSessions_TableAndJSON(t *testing.T) {
	srv, env := newConvServer(t)
	srv.on("conversations", func(w http.ResponseWriter, _ map[string]any) { reply(w, 200, sessionsReply) })

	r := runWith([]string{"list", "sessions"}, env, term{})
	require.Equal(t, 0, r.code, r.stderr)
	lines := splitLines(r.stdout)
	require.Len(t, lines, 3, r.stdout)
	assert.Regexp(t, `^ID\s+TITLE\s+AGENT\s+DEVICE\s+STATUS\s+LAST ACTIVITY`, lines[0])
	assert.Regexp(t, `^`+sessA+`\s+Fix the build\s+planner\s+this desktop\s+running\s+2026-11-25T10:00:00Z`, lines[1])
	assert.Contains(t, lines[2], "build-box")
	assert.Contains(t, lines[2], "waiting")

	r = runWith([]string{"list", "sessions", "-o", "json"}, env, term{})
	require.Equal(t, 0, r.code, r.stderr)
	var out []map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &out), r.stdout)
	require.Len(t, out, 2)
	assert.Equal(t, sessA, out[0]["id"])
	assert.Equal(t, "running", out[0]["status"])
}

func TestListSessions_FiltersGoToTheExecutor(t *testing.T) {
	srv, env := newConvServer(t)
	srv.on("conversations", func(w http.ResponseWriter, _ map[string]any) { reply(w, 200, `{"sessions":[]}`) })
	r := runWith([]string{"list", "sessions", "--agent", "planner", "--device", "build-box", "--status", "waiting", "--limit", "5", "--mine"}, env, term{})
	require.Equal(t, 0, r.code, r.stderr)
	body := srv.bodyFor("conversations")
	assert.Equal(t, "planner", body["agent"])
	assert.Equal(t, "build-box", body["device"])
	assert.Equal(t, "waiting", body["status"])
	assert.EqualValues(t, 5, body["limit"])
	assert.Equal(t, true, body["mine"])

	r = runWith([]string{"list", "sessions", "-o", "json"}, env, term{})
	require.Equal(t, 0, r.code)
	assert.Equal(t, "[]\n", r.stdout, "an empty list is [] not null")
}

func TestListSessions_UsageErrors(t *testing.T) {
	_, env := newConvServer(t)
	for name, args := range map[string][]string{
		"bad status":   {"list", "sessions", "--status", "sleeping"},
		"bad limit":    {"list", "sessions", "--limit", "0"},
		"limit text":   {"list", "sessions", "--limit", "many"},
		"unknown flag": {"list", "sessions", "--nope", "x"},
		"extra arg":    {"list", "sessions", "foo"},
	} {
		r := runWith(args, env, term{})
		assert.Equal(t, 2, r.code, "%s: %s", name, r.stderr)
	}
}

// Given executor 拒绝(422 --mine) When list Then 退出码 2;executor 其它拒绝是 1。
func TestListSessions_ExecutorRefusals(t *testing.T) {
	srv, env := newConvServer(t)
	srv.on("conversations", func(w http.ResponseWriter, _ map[string]any) {
		reply(w, 422, `{"error":"--mine needs an account"}`)
	})
	r := runWith([]string{"list", "sessions", "--mine"}, env, term{})
	assert.Equal(t, 2, r.code)
	assert.Contains(t, r.stderr, "--mine needs an account")
}

func TestGetSession(t *testing.T) {
	srv, env := newConvServer(t)
	srv.on("conversation", func(w http.ResponseWriter, _ map[string]any) {
		reply(w, 200, `{"id":"`+sessA+`","title":"t","agent":"planner","status":"idle","pending":0}`)
	})
	r := runWith([]string{"get", "session", sessA}, env, term{})
	require.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, sessA, srv.bodyFor("conversation")["conversationId"])
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &out), r.stdout)
	assert.Equal(t, "planner", out["agent"])
	assert.NotContains(t, out, "project", "fields the executor omits stay omitted")

	assert.Equal(t, 2, runWith([]string{"get", "session", "17"}, env, term{}).code, "malformed UUID")
	assert.Equal(t, 2, runWith([]string{"get", "session"}, env, term{}).code)

	srv.on("conversation", func(w http.ResponseWriter, _ map[string]any) {
		reply(w, 404, `{"error":"session not found or not accessible"}`)
	})
	r = runWith([]string{"get", "session", sessA}, env, term{})
	assert.Equal(t, 1, r.code)
	assert.Contains(t, r.stderr, "session not found or not accessible")
}

const messagesReply = `{"session":"` + sessA + `","hasMore":true,"before":"7","messages":[
 {"role":"user","at":"2026-11-25T10:00:00Z","text":"build it"},
 {"role":"assistant","text":"done","blocks":[{"type":"tool_use","name":"Bash","input":{"command":"make"}},{"type":"tool_result","text":"ok"},{"type":"text","text":"done"}]}]}`

func TestMessages_PrintsTurnsAndPagingHint(t *testing.T) {
	srv, env := newConvServer(t)
	srv.on("messages", func(w http.ResponseWriter, _ map[string]any) { reply(w, 200, messagesReply) })

	r := runWith([]string{"messages", sessA, "--last", "3", "--before", "9"}, env, term{})
	require.Equal(t, 0, r.code, r.stderr)
	body := srv.bodyFor("messages")
	assert.Equal(t, sessA, body["conversationId"])
	assert.EqualValues(t, 3, body["last"])
	assert.Equal(t, "9", body["before"])
	assert.NotContains(t, body, "full")
	assert.Contains(t, r.stdout, "user: build it")
	assert.Contains(t, r.stdout, "assistant: done")
	assert.NotContains(t, r.stdout, "Bash", "default mode prints text only")
	assert.Contains(t, r.stderr, "--before 7", "more history: stderr says how to page")

	r = runWith([]string{"messages", sessA, "--full"}, env, term{})
	require.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, true, srv.bodyFor("messages")["full"])
	assert.Contains(t, r.stdout, "tool_use Bash")
	assert.Contains(t, r.stdout, "make")
	assert.Contains(t, r.stdout, "tool_result")

	r = runWith([]string{"messages", sessA, "-o", "json"}, env, term{})
	require.Equal(t, 0, r.code, r.stderr)
	var out struct {
		Messages []map[string]any `json:"messages"`
		Before   string           `json:"before"`
		HasMore  bool             `json:"hasMore"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &out), r.stdout)
	assert.Len(t, out.Messages, 2)
	assert.Equal(t, "7", out.Before)
	assert.True(t, out.HasMore)
	assert.Empty(t, r.stderr, "json mode keeps stderr clean")
}

func TestMessages_UsageAndRefusals(t *testing.T) {
	srv, env := newConvServer(t)
	for name, args := range map[string][]string{
		"no session":  {"messages"},
		"bad uuid":    {"messages", "17"},
		"zero last":   {"messages", sessA, "--last", "0"},
		"text last":   {"messages", sessA, "--last", "x"},
		"two session": {"messages", sessA, sessA},
	} {
		assert.Equal(t, 2, runWith(args, env, term{}).code, name)
	}
	srv.on("messages", func(w http.ResponseWriter, _ map[string]any) {
		reply(w, 404, `{"error":"session not found or not accessible"}`)
	})
	r := runWith([]string{"messages", sessA}, env, term{})
	assert.Equal(t, 1, r.code)
	assert.Contains(t, r.stderr, "session not found or not accessible")
	srv.on("messages", func(w http.ResponseWriter, _ map[string]any) {
		reply(w, 400, `{"error":"invalid --before cursor \"x\""}`)
	})
	assert.Equal(t, 2, runWith([]string{"messages", sessA, "--before", "x"}, env, term{}).code)
}

const pendingReply = `{"items":[
 {"id":"q1","session":"` + sessA + `","kind":"question","questions":[{"question":"Which db?","options":[{"label":"pg"},{"label":"mysql"}]}]},
 {"id":"p1","session":"` + sessA + `","kind":"tool_permission","tool":"Bash","input":{"command":"ls"}}]}`

func TestPending(t *testing.T) {
	srv, env := newConvServer(t)
	srv.on("pending", func(w http.ResponseWriter, _ map[string]any) { reply(w, 200, pendingReply) })

	r := runWith([]string{"pending", sessA, "-o", "json"}, env, term{})
	require.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, sessA, srv.bodyFor("pending")["conversationId"])
	var out struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &out), r.stdout)
	require.Len(t, out.Items, 2)
	assert.Equal(t, "question", out.Items[0]["kind"])

	r = runWith([]string{"pending"}, env, term{})
	require.Equal(t, 0, r.code, r.stderr)
	assert.NotContains(t, srv.bodyFor("pending"), "conversationId", "no session = every visible session")
	lines := splitLines(r.stdout)
	require.Len(t, lines, 3, r.stdout)
	assert.Regexp(t, `^ID\s+SESSION\s+KIND\s+SUMMARY`, lines[0])
	assert.Regexp(t, `^q1\s+`+sessA+`\s+question\s+Which db\?`, lines[1])
	assert.Regexp(t, `^p1\s+`+sessA+`\s+tool_permission\s+Bash`, lines[2])

	assert.Equal(t, 2, runWith([]string{"pending", "17"}, env, term{}).code, "malformed UUID")
	srv.on("pending", func(w http.ResponseWriter, _ map[string]any) {
		reply(w, 404, `{"error":"session not found or not accessible"}`)
	})
	assert.Equal(t, 1, runWith([]string{"pending", sessA}, env, term{}).code)
}

func TestAnswer_SendsTheRightShape(t *testing.T) {
	srv, env := newConvServer(t)
	srv.on("answer", func(w http.ResponseWriter, _ map[string]any) { reply(w, 200, `{"answered":true}`) })

	r := runWith([]string{"answer", sessA, "q1", "--option", "2"}, env, term{})
	require.Equal(t, 0, r.code, r.stderr)
	b := srv.bodyFor("answer")
	assert.Equal(t, sessA, b["conversationId"])
	assert.Equal(t, "q1", b["requestId"])
	assert.Equal(t, []any{"2"}, b["options"])

	require.Equal(t, 0, runWith([]string{"answer", sessA, "q1", "--option", "pg", "--option", "mysql"}, env, term{}).code)
	assert.Equal(t, []any{"pg", "mysql"}, srv.bodyFor("answer")["options"])

	require.Equal(t, 0, runWith([]string{"answer", sessA, "q1", "--text", "sqlite please"}, env, term{}).code)
	assert.Equal(t, "sqlite please", srv.bodyFor("answer")["text"])

	require.Equal(t, 0, runWith([]string{"answer", sessA, "p1", "--allow"}, env, term{}).code)
	assert.Equal(t, "allow", srv.bodyFor("answer")["decision"])

	require.Equal(t, 0, runWith([]string{"answer", sessA, "p1", "--deny", "--message", "too risky"}, env, term{}).code)
	b = srv.bodyFor("answer")
	assert.Equal(t, "deny", b["decision"])
	assert.Equal(t, "too risky", b["message"])
}

func TestAnswer_UsageErrorsNeverReachTheExecutor(t *testing.T) {
	srv, env := newConvServer(t)
	for name, args := range map[string][]string{
		"no answer":           {"answer", sessA, "q1"},
		"allow and deny":      {"answer", sessA, "p1", "--allow", "--deny"},
		"allow and option":    {"answer", sessA, "q1", "--allow", "--option", "1"},
		"option and text":     {"answer", sessA, "q1", "--option", "1", "--text", "x"},
		"message w/o deny":    {"answer", sessA, "p1", "--allow", "--message", "x"},
		"bad uuid":            {"answer", "17", "q1", "--allow"},
		"missing id":          {"answer", sessA, "--allow"},
		"too many positional": {"answer", sessA, "q1", "extra", "--allow"},
	} {
		r := runWith(args, env, term{})
		assert.Equal(t, 2, r.code, "%s: %s", name, r.stderr)
	}
	assert.Equal(t, 0, srv.count("/ctl/v1/answer"))
}

func TestAnswer_ExecutorOutcomes(t *testing.T) {
	srv, env := newConvServer(t)
	srv.on("answer", func(w http.ResponseWriter, _ map[string]any) {
		reply(w, 404, `{"error":"the pending item no longer exists (already answered elsewhere or timed out)"}`)
	})
	r := runWith([]string{"answer", sessA, "q1", "--option", "1"}, env, term{})
	assert.Equal(t, 1, r.code)
	assert.Contains(t, r.stderr, "no longer exists")

	srv.on("answer", func(w http.ResponseWriter, _ map[string]any) {
		reply(w, 403, `{"error":"a session token cannot call answer"}`)
	})
	r = runWith([]string{"answer", sessA, "p1", "--allow"}, env, term{})
	assert.Equal(t, 1, r.code)
	assert.Contains(t, r.stderr, "cannot call answer")

	srv.on("answer", func(w http.ResponseWriter, _ map[string]any) {
		reply(w, 422, `{"error":"--option \"x\" is not one of: pg, mysql"}`)
	})
	assert.Equal(t, 2, runWith([]string{"answer", sessA, "q1", "--option", "x"}, env, term{}).code)
}

func TestQueryVerbsHaveHelp(t *testing.T) {
	for _, verb := range []string{"messages", "pending", "answer"} {
		r := runWith([]string{verb, "--help"}, func(string) (string, bool) { return "", false }, term{})
		assert.Equal(t, 0, r.code, verb)
		assert.Contains(t, r.stdout, "agrctl "+verb, verb)
	}
	r := runWith([]string{"help"}, func(string) (string, bool) { return "", false }, term{})
	for _, want := range []string{"agrctl messages", "agrctl pending", "agrctl answer", "list sessions", "get session"} {
		assert.Contains(t, r.stdout, want)
	}
}

func splitLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// bodyFor 是最近一次发给 /ctl/v1/<route> 的请求体。
func (f *convServer) bodyFor(route string) map[string]any { return f.body("/ctl/v1/" + route) }

func TestSessionResourceHelp(t *testing.T) {
	noEnv := func(string) (string, bool) { return "", false }
	r := runWith([]string{"list", "sessions", "--help"}, noEnv, term{})
	assert.Equal(t, 0, r.code)
	assert.Contains(t, r.stdout, "--status running|idle|waiting|failed")
	r = runWith([]string{"get", "session", "-h"}, noEnv, term{})
	assert.Equal(t, 0, r.code)
	assert.Contains(t, r.stdout, "agrctl get session")
	r = runWith([]string{"help", "sessions"}, noEnv, term{})
	assert.Equal(t, 0, r.code)
	assert.Contains(t, r.stdout, "agrctl list sessions")
}

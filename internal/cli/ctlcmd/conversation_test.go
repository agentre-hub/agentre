package ctlcmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	sessA = "0193b6f0-7c5e-7d2a-9f4b-3c1d2e5f6a7b"
)

// convServer 是会话端点(/send /wait /tail /stop)的假执行者:记录收到的请求,按测试给的
// 处理函数作答。
type convServer struct {
	t       *testing.T
	mu      sync.Mutex
	bodies  map[string][]map[string]any
	queries map[string][]string
	send    func(w http.ResponseWriter, body map[string]any)
	wait    func(w http.ResponseWriter, n int, body map[string]any)
	tail    func(w http.ResponseWriter, n int, query string)
	stop    func(w http.ResponseWriter, body map[string]any)
}

func newConvServer(t *testing.T) (*convServer, func(string) (string, bool)) {
	t.Helper()
	f := &convServer{t: t, bodies: map[string][]map[string]any{}, queries: map[string][]string{}}
	mux := http.NewServeMux()
	handle := func(path string, h func(w http.ResponseWriter, r *http.Request, n int, body map[string]any)) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tok" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var body map[string]any
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			f.mu.Lock()
			f.bodies[path] = append(f.bodies[path], body)
			f.queries[path] = append(f.queries[path], r.URL.RawQuery)
			n := len(f.queries[path])
			f.mu.Unlock()
			h(w, r, n, body)
		})
	}
	handle("/ctl/v1/send", func(w http.ResponseWriter, _ *http.Request, _ int, body map[string]any) {
		if f.send == nil {
			_, _ = io.WriteString(w, `{"conversationId":"`+sessA+`","agent":"planner","status":"running","done":false}`)
			return
		}
		f.send(w, body)
	})
	handle("/ctl/v1/wait", func(w http.ResponseWriter, _ *http.Request, n int, body map[string]any) { f.wait(w, n, body) })
	handle("/ctl/v1/tail", func(w http.ResponseWriter, r *http.Request, n int, _ map[string]any) { f.tail(w, n, r.URL.RawQuery) })
	handle("/ctl/v1/stop", func(w http.ResponseWriter, _ *http.Request, _ int, body map[string]any) { f.stop(w, body) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	env := map[string]string{"AGENTRE_CTL_ENDPOINT": srv.URL, "AGENTRE_CTL_TOKEN": "tok"}
	return f, func(k string) (string, bool) { v, ok := env[k]; return v, ok }
}

func (f *convServer) body(path string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	bs := f.bodies[path]
	if len(bs) == 0 {
		f.t.Fatalf("no request to %s", path)
	}
	return bs[len(bs)-1]
}

func (f *convServer) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.bodies[path])
}

func reply(w http.ResponseWriter, code int, body string) {
	w.WriteHeader(code)
	_, _ = io.WriteString(w, body) //nolint:gosec // 测试里的假执行者回写固定的 JSON 夹具。
}

func writeTempFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, data, 0o600))
	return p
}

// 1x1 PNG。
var pngBytes = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
	0x42, 0x60, 0x82,
}

// ---- send ----

// Given 一个 agent When send(不等) Then stdout 只有完整的会话 UUID,stderr 说派给了谁、在哪台设备;
// 覆盖、标题与图片都进了请求。
func TestSend_NewSessionPrintsUUIDAndCarriesFlags(t *testing.T) {
	srv, env := newConvServer(t)
	img := writeTempFile(t, "shot.png", pngBytes)
	r := runWith([]string{"send", "--agent", "planner", "--title", "Nightly", "--effort", "high",
		"--model", "anthropic/claude-opus-5", "--permission-mode", "plan", "--image", img, "--isolated", "ship", "it"}, env, term{})
	require.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, sessA+"\n", r.stdout)
	assert.Contains(t, r.stderr, "dispatched to planner — this desktop")
	body := srv.body("/ctl/v1/send")
	assert.Equal(t, "planner", body["agent"])
	assert.Equal(t, "ship it", body["text"])
	assert.Equal(t, "Nightly", body["title"])
	assert.Equal(t, "high", body["effort"])
	assert.Equal(t, "anthropic/claude-opus-5", body["model"])
	assert.Equal(t, "plan", body["permissionMode"])
	assert.Equal(t, true, body["isolated"])
	images, _ := body["images"].([]any)
	require.Len(t, images, 1)
	assert.True(t, strings.HasPrefix(images[0].(map[string]any)["dataUrl"].(string), "data:image/png;base64,"))
}

func TestSend_JSONOutput(t *testing.T) {
	srv, env := newConvServer(t)
	r := runWith([]string{"send", "--agent", "planner", "--project", "7", "-o", "json", "go"}, env, term{})
	require.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, float64(7), srv.body("/ctl/v1/send")["projectId"])
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &out), r.stdout)
	assert.Equal(t, sessA, out["session"])
	assert.Equal(t, "running", out["status"])
}

func TestSend_ContinueAndSteer(t *testing.T) {
	srv, env := newConvServer(t)
	srv.send = func(w http.ResponseWriter, _ map[string]any) {
		reply(w, 200, `{"conversationId":"`+sessA+`","agent":"planner","steered":true,"status":"running"}`)
	}
	r := runWith([]string{"send", "--session", sessA, "--steer", "--effort", "low", "also", "this"}, env, term{})
	require.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, sessA+"\n", r.stdout)
	body := srv.body("/ctl/v1/send")
	assert.Equal(t, sessA, body["conversationId"])
	assert.Equal(t, true, body["steer"])
	assert.Equal(t, "low", body["effort"])
	assert.NotContains(t, body, "agent")
}

func TestSend_UsageErrors(t *testing.T) {
	img := writeTempFile(t, "shot.png", pngBytes)
	txt := writeTempFile(t, "notes.txt", []byte("plain text, not an image"))
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no agent and no session", []string{"send", "hi"}, "--agent"},
		{"malformed session", []string{"send", "--session", "42", "hi"}, "UUID"},
		{"session with agent", []string{"send", "--session", sessA, "--agent", "planner", "hi"}, "--session"},
		{"session with project", []string{"send", "--session", sessA, "--project", "web", "hi"}, "--session"},
		{"steer with image", []string{"send", "--session", sessA, "--steer", "--image", img, "hi"}, "--steer"},
		{"model with cli-model", []string{"send", "--agent", "planner", "--model", "a/b", "--cli-model", "opus", "hi"}, "--cli-model"},
		{"device in desktop mode", []string{"send", "--agent", "planner", "--device", "build-box", "hi"}, "--device"},
		{"missing image", []string{"send", "--agent", "planner", "--image", "/nope/missing.png", "hi"}, "missing.png"},
		{"not an image", []string{"send", "--agent", "planner", "--image", txt, "hi"}, "not a supported image"},
		{"timeout without wait", []string{"send", "--agent", "planner", "--timeout", "5m", "hi"}, "--timeout"},
		{"bad timeout", []string{"send", "--agent", "planner", "--wait", "--timeout", "soon", "hi"}, "--timeout"},
		{"missing text", []string{"send", "--agent", "planner"}, "text"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, env := newConvServer(t)
			r := runWith(c.args, env, term{})
			assert.Equal(t, 2, r.code, r.stderr)
			assert.Contains(t, r.stderr, c.want)
			assert.Equal(t, 0, srv.count("/ctl/v1/send"), "usage errors never reach the executor")
		})
	}
}

// 执行者的拒绝按类别换算退出码:用法类(值不被支持、向自己发送)2,其余(运行中、看不到)1。
func TestSend_ExecutorRejections(t *testing.T) {
	cases := []struct {
		name   string
		status int
		msg    string
		code   int
	}{
		{"unsupported override", 422, `--effort "turbo" is not supported by the target backend (one of: low, high)`, 2},
		{"own session", 400, "cannot send to your own session", 2},
		{"running", 409, "session is running; use --steer to interject, or agrctl wait first", 1},
		{"not visible", 404, "session not found or not accessible", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, env := newConvServer(t)
			srv.send = func(w http.ResponseWriter, _ map[string]any) {
				reply(w, c.status, fmt.Sprintf(`{"error":%q}`, c.msg))
			}
			r := runWith([]string{"send", "--session", sessA, "again"}, env, term{})
			assert.Equal(t, c.code, r.code)
			assert.Contains(t, r.stderr, c.msg)
			assert.Empty(t, r.stdout)
		})
	}
}

// ---- send --wait / wait 的结果 ----

func outcomeCases() []struct {
	name, body string
	code       int
	stdout     string
	stderr     string
	timeoutMs  float64
	extraArgs  []string
} {
	return []struct {
		name, body string
		code       int
		stdout     string
		stderr     string
		timeoutMs  float64
		extraArgs  []string
	}{
		{"idle", `{"conversationId":"` + sessA + `","status":"idle","text":"the answer"}`, 0, "the answer\n", "", 0, nil},
		{"failed", `{"conversationId":"` + sessA + `","status":"failed","error":"provider exploded"}`, 1, "", "provider exploded", 0, nil},
		{"waiting", `{"conversationId":"` + sessA + `","status":"waiting","pending":{"toolPermissions":[{"requestId":"req-1"}]}}`, 4, `"req-1"`, "waiting for input", 0, nil},
		{"timeout", `{"conversationId":"` + sessA + `","status":"running"}`, 5, "", "still running", 90000, []string{"--timeout", "90s"}},
	}
}

func TestSendWait_Outcomes(t *testing.T) {
	for _, c := range outcomeCases() {
		t.Run(c.name, func(t *testing.T) {
			srv, env := newConvServer(t)
			srv.send = func(w http.ResponseWriter, _ map[string]any) { reply(w, 200, c.body) }
			args := append([]string{"send", "--agent", "planner", "--wait"}, c.extraArgs...)
			r := runWith(append(args, "go"), env, term{})
			assert.Equal(t, c.code, r.code, r.stderr)
			assert.Contains(t, r.stdout, c.stdout)
			assert.Contains(t, r.stderr, c.stderr)
			if c.timeoutMs > 0 {
				assert.Equal(t, c.timeoutMs, srv.body("/ctl/v1/send")["timeoutMs"])
			}
		})
	}
}

func TestWait_Outcomes(t *testing.T) {
	for _, c := range outcomeCases() {
		t.Run(c.name, func(t *testing.T) {
			srv, env := newConvServer(t)
			srv.wait = func(w http.ResponseWriter, _ int, _ map[string]any) { reply(w, 200, c.body) }
			r := runWith(append([]string{"wait", sessA}, c.extraArgs...), env, term{})
			assert.Equal(t, c.code, r.code, r.stderr)
			assert.Contains(t, r.stdout, c.stdout)
			assert.Contains(t, r.stderr, c.stderr)
			assert.Equal(t, sessA, srv.body("/ctl/v1/wait")["conversationId"])
		})
	}
}

func TestWait_JSONAndErrors(t *testing.T) {
	t.Run("-o json carries session and status", func(t *testing.T) {
		srv, env := newConvServer(t)
		srv.wait = func(w http.ResponseWriter, _ int, _ map[string]any) {
			reply(w, 200, `{"conversationId":"`+sessA+`","status":"waiting","pending":{"askUserQuestions":[{"requestId":"ask-1"}]}}`)
		}
		r := runWith([]string{"wait", sessA, "-o", "json"}, env, term{})
		assert.Equal(t, 4, r.code)
		var out map[string]any
		require.NoError(t, json.Unmarshal([]byte(r.stdout), &out), r.stdout)
		assert.Equal(t, sessA, out["session"])
		assert.Equal(t, "waiting", out["status"])
		assert.Contains(t, r.stdout, "ask-1")
	})
	t.Run("malformed id → 2", func(t *testing.T) {
		_, env := newConvServer(t)
		r := runWith([]string{"wait", "17"}, env, term{})
		assert.Equal(t, 2, r.code)
		assert.Contains(t, r.stderr, "UUID")
	})
	t.Run("not visible → 1", func(t *testing.T) {
		srv, env := newConvServer(t)
		srv.wait = func(w http.ResponseWriter, _ int, _ map[string]any) {
			reply(w, 404, `{"error":"session not found or not accessible"}`)
		}
		r := runWith([]string{"wait", sessA}, env, term{})
		assert.Equal(t, 1, r.code)
		assert.Contains(t, r.stderr, "session not found or not accessible")
	})
	t.Run("connection dropped mid-wait → reconnects and keeps waiting", func(t *testing.T) {
		restore := setReconnectDelay(time.Millisecond)
		defer restore()
		srv, env := newConvServer(t)
		srv.wait = func(w http.ResponseWriter, n int, _ map[string]any) {
			if n == 1 {
				hj, _ := w.(http.Hijacker)
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
				return
			}
			reply(w, 200, `{"conversationId":"`+sessA+`","status":"idle","text":"after reconnect"}`)
		}
		r := runWith([]string{"wait", sessA}, env, term{})
		assert.Equal(t, 0, r.code, r.stderr)
		assert.Equal(t, "after reconnect\n", r.stdout)
		assert.Equal(t, 2, srv.count("/ctl/v1/wait"))
	})
}

// ---- tail ----

func TestTail_UntilIdleAndResume(t *testing.T) {
	t.Run("streams NDJSON verbatim and exits 0 when idle", func(t *testing.T) {
		srv, env := newConvServer(t)
		srv.tail = func(w http.ResponseWriter, _ int, _ string) {
			_, _ = io.WriteString(w, `{"cursor":"v1:3","type":"text","data":{"kind":"text_delta","text":"hi"}}`+"\n")
			_, _ = io.WriteString(w, `{"cursor":"v1:3","type":"status","status":"idle"}`+"\n")
		}
		r := runWith([]string{"tail", sessA, "--from", "v1:2", "--until-idle"}, env, term{})
		assert.Equal(t, 0, r.code, r.stderr)
		lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
		require.Len(t, lines, 2)
		assert.Contains(t, lines[0], `"type":"text"`)
		q := srv.queries["/ctl/v1/tail"][0]
		assert.Contains(t, q, "from=v1%3A2")
		assert.Contains(t, q, "untilIdle=true")
		assert.Contains(t, q, "conversationId="+sessA)
	})
	t.Run("waiting → exit 4", func(t *testing.T) {
		srv, env := newConvServer(t)
		srv.tail = func(w http.ResponseWriter, _ int, _ string) {
			_, _ = io.WriteString(w, `{"cursor":"v1:3","type":"status","status":"waiting"}`+"\n")
		}
		r := runWith([]string{"tail", sessA, "--until-idle"}, env, term{})
		assert.Equal(t, 4, r.code)
	})
	t.Run("a dropped stream resumes from the last cursor", func(t *testing.T) {
		restore := setReconnectDelay(time.Millisecond)
		defer restore()
		srv, env := newConvServer(t)
		srv.tail = func(w http.ResponseWriter, n int, _ string) {
			if n == 1 {
				_, _ = io.WriteString(w, `{"cursor":"v1:5","type":"tool_call"}`+"\n")
				return // 没有 status 行就断了
			}
			_, _ = io.WriteString(w, `{"cursor":"v1:6","type":"status","status":"idle"}`+"\n")
		}
		r := runWith([]string{"tail", sessA, "--until-idle"}, env, term{})
		assert.Equal(t, 0, r.code, r.stderr)
		qs := srv.queries["/ctl/v1/tail"]
		require.Len(t, qs, 2)
		assert.NotContains(t, qs[0], "from=")
		assert.Contains(t, qs[1], "from=v1%3A5")
	})
	t.Run("stale cursor → 1 with the executor's hint", func(t *testing.T) {
		srv, env := newConvServer(t)
		srv.tail = func(w http.ResponseWriter, _ int, _ string) {
			reply(w, 410, `{"error":"cursor is no longer valid; read the history with agrctl messages"}`)
		}
		r := runWith([]string{"tail", sessA, "--from", "v1:99"}, env, term{})
		assert.Equal(t, 1, r.code)
		assert.Contains(t, r.stderr, "agrctl messages")
	})
}

// ---- stop ----

func TestStop(t *testing.T) {
	t.Run("running → prints the dropped steer count", func(t *testing.T) {
		srv, env := newConvServer(t)
		srv.stop = func(w http.ResponseWriter, _ map[string]any) {
			reply(w, 200, `{"stopped":true,"running":true,"droppedSteers":2}`)
		}
		r := runWith([]string{"stop", sessA}, env, term{})
		assert.Equal(t, 0, r.code, r.stderr)
		assert.Contains(t, r.stdout, "dropped 2 undelivered steer message(s)")
		assert.Equal(t, sessA, srv.body("/ctl/v1/stop")["conversationId"])
	})
	t.Run("not running → says so, exit 0", func(t *testing.T) {
		srv, env := newConvServer(t)
		srv.stop = func(w http.ResponseWriter, _ map[string]any) {
			reply(w, 200, `{"stopped":false,"running":false,"droppedSteers":0}`)
		}
		r := runWith([]string{"stop", sessA}, env, term{})
		assert.Equal(t, 0, r.code)
		assert.Contains(t, r.stderr, "session is not running")
	})
	t.Run("session token refused → 1", func(t *testing.T) {
		srv, env := newConvServer(t)
		srv.stop = func(w http.ResponseWriter, _ map[string]any) {
			reply(w, 403, `{"error":"a session token cannot call stop"}`)
		}
		r := runWith([]string{"stop", sessA}, env, term{})
		assert.Equal(t, 1, r.code)
		assert.Contains(t, r.stderr, "cannot call stop")
	})
}

// ---- help ----

func TestHelp_DocumentsConversationVerbs(t *testing.T) {
	r := runWith([]string{"help"}, func(string) (string, bool) { return "", false }, term{})
	require.Equal(t, 0, r.code)
	for _, want := range []string{"agrctl wait <session>", "agrctl tail <session>", "agrctl stop <session>", "4 waiting for input", "5 wait timed out"} {
		assert.Contains(t, r.stdout, want)
	}
	for verb, wants := range map[string][]string{
		"send": {"--session <id>", "--steer", "--image <file>", "--model <provider>/<model>", "--cli-model", "--effort", "--permission-mode", "--title", "--timeout", "-o json", "shell"},
		"wait": {"--timeout", "exit 4", "exit 5", "as a background shell command"},
		"tail": {"--from <cursor>", "--until-idle", "NDJSON"},
		"stop": {"steer"},
	} {
		r := runWith([]string{"help", verb}, func(string) (string, bool) { return "", false }, term{})
		require.Equal(t, 0, r.code, r.stderr)
		for _, want := range wants {
			assert.Contains(t, r.stdout, want, "help %s", verb)
		}
	}
}

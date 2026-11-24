package claudecodehook

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newFakeGateway(t *testing.T, msgs []string, status int) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hook/v1/inbox" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok-xyz" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if status != 0 {
			http.Error(w, "boom", status)
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			Messages []string `json:"messages"`
		}{Messages: msgs})
	}))
	t.Cleanup(srv.Close)
	return srv, "tok-xyz"
}

func TestRunPostTool_EmptyQueue(t *testing.T) {
	srv, tok := newFakeGateway(t, nil, 0)
	var out bytes.Buffer
	in := strings.NewReader(`{"session_id":"sid-1"}`)
	RunPostTool(srv.URL, tok, in, &out)

	var got struct {
		HookSpecificOutput map[string]any `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, out.String())
	}
	if got.HookSpecificOutput["hookEventName"] != "PostToolUse" {
		t.Fatalf("hookEventName: %v", got.HookSpecificOutput["hookEventName"])
	}
	if got.HookSpecificOutput["additionalContext"] != nil {
		t.Fatalf("expected no additionalContext, got %v", got.HookSpecificOutput["additionalContext"])
	}
	if _, ok := got.HookSpecificOutput["permissionDecision"]; ok {
		t.Fatalf("PostToolUse must not emit permissionDecision: %v", got.HookSpecificOutput)
	}
}

func TestRunPostTool_WithMessages(t *testing.T) {
	srv, tok := newFakeGateway(t, []string{"do this first", "then that"}, 0)
	var out bytes.Buffer
	RunPostTool(srv.URL, tok, strings.NewReader(`{"session_id":"sid-1"}`), &out)

	var got struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, out.String())
	}
	if got.HookSpecificOutput.HookEventName != "PostToolUse" {
		t.Fatalf("hookEventName: %s", got.HookSpecificOutput.HookEventName)
	}
	ctx := got.HookSpecificOutput.AdditionalContext
	// XML 包裹 + directive 措辞：模型靠 <user-message-while-working> 识别为
	// 高优先级新指令；spike 验证 "supersedes the prior plan" 能让 claude 真正
	// 掉头（中性文案吃不动），但保留在 PostToolUse 边界注入的安全性。
	if !strings.Contains(ctx, "<user-message-while-working") {
		t.Errorf("missing XML wrapper: %q", ctx)
	}
	if !strings.Contains(ctx, "supersedes") {
		t.Errorf("missing directive wording: %q", ctx)
	}
	if !strings.Contains(ctx, "<message index=\"1\">") || !strings.Contains(ctx, "<message index=\"2\">") {
		t.Errorf("messages must be indexed: %q", ctx)
	}
	if !strings.Contains(ctx, "do this first") || !strings.Contains(ctx, "then that") {
		t.Errorf("messages missing: %q", ctx)
	}
}

// TestRunPostTool_SkipsDuringSubagent 规约关键 bug 修复:
// claude CLI 的 PostToolUse hook 在 subagent (Task tool) 内层工具结束时
// 也会触发,payload 里带 agent_id / agent_type 标记自己在 subagent 里.
//
// Given subagent 内层 tool 的 hook payload (含 agent_id) 且 inbox 有排队消息
// When  RunPostTool 执行
// Then  不调 fetchInbox (否则消息被 drain 给 subagent 上下文,
//
//	且 chat_svc 会在 subagent 中间错误切分 turn),emit 空 additionalContext.
//
// 正确的 drain 边界是主 agent 的下一个 tool —— 那次 hook payload 不带 agent_id,
// 走 TestRunPostTool_WithMessages 路径正常 drain.
func TestRunPostTool_SkipsDuringSubagent(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(struct {
			Messages []string `json:"messages"`
		}{Messages: []string{"queued-msg"}})
	}))
	defer srv.Close()

	var out bytes.Buffer
	in := strings.NewReader(`{"session_id":"sid-1","agent_id":"ad6256bd70a3d0d7f","agent_type":"general-purpose","tool_name":"Bash"}`)
	RunPostTool(srv.URL, "tok-xyz", in, &out)

	if hits != 0 {
		t.Fatalf("hook must not fetch inbox while inside subagent (hits=%d), otherwise queued msg leaks into subagent context", hits)
	}
	if strings.Contains(out.String(), "queued-msg") {
		t.Fatalf("hook must not emit queued message in subagent: %s", out.String())
	}
	if strings.Contains(out.String(), "additionalContext") {
		t.Fatalf("subagent hook must omit additionalContext: %s", out.String())
	}
	if !strings.Contains(out.String(), `"hookEventName":"PostToolUse"`) {
		t.Fatalf("must still emit PostToolUse no-op envelope: %s", out.String())
	}
}

// recordingGateway answers every inbox request with msgs and records the query of
// each request it saw.
func recordingGateway(t *testing.T, msgs []string) (string, *[]url.Values) {
	t.Helper()
	var seen []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Query())
		_ = json.NewEncoder(w).Encode(struct {
			Messages []string `json:"messages"`
		}{Messages: msgs})
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &seen
}

// Given 主 agent 的 Bash 跑了 `cd sub`,PostToolUse hook 输入带着命令之后的 cwd;
// When  hook 去收件箱;
// Then  请求带上 cwd 与 tool_use_id(不带 agent_id),插话照常注入。
func TestRunPostTool_ReportsPostCommandCwd(t *testing.T) {
	base, seen := recordingGateway(t, []string{"steer"})
	var out bytes.Buffer
	in := strings.NewReader(`{"session_id":"sid-1","cwd":"/repo/sub","tool_use_id":"toolu_1","tool_name":"Bash","tool_input":{"command":"cd sub"}}`)
	RunPostTool(base, "tok-xyz", in, &out)

	if len(*seen) != 1 {
		t.Fatalf("requests = %d, want 1", len(*seen))
	}
	q := (*seen)[0]
	if q.Get("session_id") != "sid-1" || q.Get("cwd") != "/repo/sub" || q.Get("tool_use_id") != "toolu_1" {
		t.Fatalf("query = %v, want session_id/cwd/tool_use_id", q)
	}
	if q.Has("agent_id") {
		t.Fatalf("main-agent hook must not send agent_id: %v", q)
	}
	if !strings.Contains(out.String(), "steer") {
		t.Fatalf("main-agent boundary still delivers steer: %s", out.String())
	}
}

// Given 子代理里的工具触发 hook(带 agent_id)且带 cwd;
// When  hook 上报;
// Then  请求带上 agent_id 让收件箱只记位置不 drain,且就算对端仍回了消息也不注入子代理。
func TestRunPostTool_SubagentReportsLocationWithoutTakingSteer(t *testing.T) {
	base, seen := recordingGateway(t, []string{"queued-msg"})
	var out bytes.Buffer
	in := strings.NewReader(`{"session_id":"sid-1","agent_id":"a1","agent_type":"general-purpose","cwd":"/repo/.claude/worktrees/x","tool_use_id":"toolu_2"}`)
	RunPostTool(base, "tok-xyz", in, &out)

	if len(*seen) != 1 {
		t.Fatalf("requests = %d, want 1 location report", len(*seen))
	}
	q := (*seen)[0]
	if q.Get("agent_id") != "a1" || q.Get("cwd") != "/repo/.claude/worktrees/x" || q.Get("tool_use_id") != "toolu_2" {
		t.Fatalf("query = %v, want agent_id/cwd/tool_use_id", q)
	}
	if strings.Contains(out.String(), "queued-msg") || strings.Contains(out.String(), "additionalContext") {
		t.Fatalf("subagent hook must not inject steer: %s", out.String())
	}
}

// Given 老 CLI 的 hook 输入不带 cwd;
// When  hook 去收件箱;
// Then  请求不带 cwd、不报错,插话照常注入。
func TestRunPostTool_OldCLIWithoutCwd(t *testing.T) {
	base, seen := recordingGateway(t, []string{"steer"})
	var out bytes.Buffer
	RunPostTool(base, "tok-xyz", strings.NewReader(`{"session_id":"sid-1"}`), &out)

	if len(*seen) != 1 {
		t.Fatalf("requests = %d, want 1", len(*seen))
	}
	if q := (*seen)[0]; q.Has("cwd") || q.Has("tool_use_id") || q.Has("agent_id") {
		t.Fatalf("old payload must not invent location params: %v", q)
	}
	if !strings.Contains(out.String(), "steer") {
		t.Fatalf("steer must still be delivered: %s", out.String())
	}
}

func TestRunPostTool_Gateway5xx_NoOp(t *testing.T) {
	srv, tok := newFakeGateway(t, nil, http.StatusInternalServerError)
	var out bytes.Buffer
	RunPostTool(srv.URL, tok, strings.NewReader(`{"session_id":"sid-1"}`), &out)

	if !strings.Contains(out.String(), `"hookEventName":"PostToolUse"`) {
		t.Fatalf("expected PostToolUse no-op, got %s", out.String())
	}
	if strings.Contains(out.String(), "additionalContext") {
		t.Fatalf("expected no additionalContext on 5xx, got %s", out.String())
	}
}

func TestRunPostTool_MissingSessionID(t *testing.T) {
	srv, tok := newFakeGateway(t, []string{"shouldNotAppear"}, 0)
	var out bytes.Buffer
	RunPostTool(srv.URL, tok, strings.NewReader(`{}`), &out)

	if strings.Contains(out.String(), "shouldNotAppear") {
		t.Fatalf("hook fetched without session_id: %s", out.String())
	}
}

func TestEmitNoop_PostTool(t *testing.T) {
	var out bytes.Buffer
	EmitNoop("post-tool", &out)
	if !strings.Contains(out.String(), `"hookEventName":"PostToolUse"`) {
		t.Fatalf("missing PostToolUse no-op: %s", out.String())
	}
	if strings.Contains(out.String(), "additionalContext") {
		t.Fatalf("no-op should omit additionalContext: %s", out.String())
	}
}

func TestEmitNoop_Unknown(t *testing.T) {
	var out bytes.Buffer
	EmitNoop("unknown-event", &out)
	if strings.TrimSpace(out.String()) != "{}" {
		t.Fatalf("expected {}, got %s", out.String())
	}
}

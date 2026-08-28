package claudecodehook

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentre-hub/agentre/internal/pkg/httpgateway"
)

// realInboxGateway stands up an httptest server that mirrors
// httpgateway.Gateway.serveHookInbox: it destructively Drains the REAL
// SteerInbox for the requested session_id and returns {"messages":[...]}.
// Backing RunPostTool with the real inbox (instead of a static slice) is what
// makes the timing assertions below meaningful — once a message is delivered at
// a boundary it is gone, exactly like production.
func realInboxGateway(t *testing.T, inbox *httpgateway.SteerInbox) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hook/v1/inbox" {
			http.NotFound(w, r)
			return
		}
		items := inbox.Drain(r.URL.Query().Get("session_id"))
		msgs := make([]string, 0, len(items))
		for _, it := range items {
			msgs = append(msgs, it.Text)
		}
		_ = json.NewEncoder(w).Encode(struct {
			Messages []string `json:"messages"`
		}{Messages: msgs})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestSteer_HeldThroughSubagent_DeliveredAtMainBoundary reproduces the
// user-reported "steer 等整轮才发出去" symptom end-to-end, and pins down WHY it
// is intermittent ("有时候又可以").
//
// It replays the exact PostToolUse payloads claude CLI 2.1.161 emits, captured
// empirically (see Scenario C in the investigation):
//   - a tool the MAIN agent runs        → payload has NO  agent_id → drains
//   - a tool a Task SUBAGENT runs inside → payload HAS     agent_id → skipped
//
// Timeline of one turn where the main agent delegates to a subagent, with a
// steer enqueued mid-turn:
//
//	enqueue "switch to plan B"
//	  PostToolUse(subagent Bash)   -> HELD   (guard skips: must not leak into subagent ctx)
//	  PostToolUse(subagent Bash)   -> HELD
//	  PostToolUse(Agent, main)     -> DELIVERED (subagent returned, control back on main agent)
//
// For the entire subagent run the steer is invisible to the model — THAT latency
// is the symptom. It is NOT a guard misclassification: the guard does exactly the
// right thing on every boundary. "有时候又可以" = the turn happened to hit a main-
// agent tool boundary soon after you sent the message.
func TestSteer_HeldThroughSubagent_DeliveredAtMainBoundary(t *testing.T) {
	const sid = "sess-uuid-1"
	inbox := httpgateway.NewSteerInbox()
	base := realInboxGateway(t, inbox)

	// chat_svc.Enqueue -> runtime.Steer -> inbox.Push, while a turn is running.
	inbox.Push(sid, "q1", "switch to plan B")

	// fire runs one PostToolUse hook and returns the DECODED additionalContext
	// (what the model actually receives) — decoding undoes JSON's < escaping
	// of the "<user-message-while-working" wrapper.
	fire := func(payload string) string {
		var out bytes.Buffer
		RunPostTool(base, "tok", strings.NewReader(payload), &out)
		var got struct {
			HookSpecificOutput struct {
				AdditionalContext string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("decode hook output: %v body=%s", err, out.String())
		}
		return got.HookSpecificOutput.AdditionalContext
	}

	subagentBash := `{"session_id":"` + sid + `","agent_id":"a74fd3e87d5ad8781","agent_type":"general-purpose","tool_name":"Bash"}`
	mainAgentBoundary := `{"session_id":"` + sid + `","tool_name":"Agent"}`

	// --- During the subagent: every boundary must HOLD the steer. ---
	if got := fire(subagentBash); strings.Contains(got, "switch to plan B") {
		t.Fatalf("steer leaked into subagent boundary #1 (should be held): %s", got)
	}
	if got := fire(subagentBash); strings.Contains(got, "switch to plan B") {
		t.Fatalf("steer leaked into subagent boundary #2 (should be held): %s", got)
	}

	// --- Subagent returned -> first MAIN-agent boundary: delivered exactly here. ---
	got := fire(mainAgentBoundary)
	if !strings.Contains(got, "switch to plan B") {
		t.Fatalf("steer NOT delivered at the main-agent boundary (this is the bug if it fails): %s", got)
	}
	if !strings.Contains(got, "<user-message-while-working") {
		t.Fatalf("delivered steer missing directive wrapper: %s", got)
	}

	// --- Destructive drain: a later boundary delivers nothing (no double-send). ---
	if again := fire(mainAgentBoundary); strings.Contains(again, "switch to plan B") {
		t.Fatalf("steer double-delivered after drain: %s", again)
	}
}

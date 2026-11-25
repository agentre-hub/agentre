package ctlcmd

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// 账号会话查询命令的假世界补充：账号会话索引、server 镜像转录、假 agentred 上的作答方法。

// indexRow 往账号会话索引里放一行（agent 一律 AG-12）。
func (w *world) indexRow(id, peer, deviceFP, title, lifecycle string, lastMessageAt int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.saved = append(w.saved, map[string]any{"conversation_id": id, "peer_fingerprint": peer, "device_fingerprint": deviceFP,
		"title": title, "agent_sync_id": "AG-12", "lifecycle_state": lifecycle, "last_message_at": lastMessageAt, "backend_type": "codex"})
	w.relay.Save(id, deviceFP)
}

func (w *world) serveIndex(rw http.ResponseWriter, r *http.Request) {
	if !w.srv.authed(rw, r, true) {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	items := []map[string]any{}
	for _, it := range w.saved {
		if id := r.URL.Query().Get("conversation_id"); id != "" && it["conversation_id"] != id {
			continue
		}
		items = append(items, it)
	}
	writeEnvelope(rw, map[string]any{"items": items, "total": len(items)})
}

func (w *world) serveTranscript(rw http.ResponseWriter, r *http.Request) {
	if !w.srv.authed(rw, r, true) {
		return
	}
	q := r.URL.Query()
	w.mu.Lock()
	defer w.mu.Unlock()
	upper, _ := strconv.ParseInt(q.Get("cursor"), 10, 64)
	var frames []map[string]any
	for _, f := range w.mirror[q.Get("conversation_id")] {
		if seq := f["seq"].(int64); upper == 0 || seq < upper {
			frames = append(frames, f)
		}
	}
	// 一页最多两轮：与真 server 一样按轮次从最新往回切。
	page, hasBefore := frames, false
	if starts := userStarts(frames); len(starts) > 2 {
		page, hasBefore = frames[starts[len(starts)-2]:], true
	}
	out := map[string]any{"frames": page, "cursor": int64(0), "has_before": hasBefore}
	if page == nil {
		out["frames"] = []any{}
	} else {
		out["cursor"], out["oldest_seq"] = page[len(page)-1]["seq"], page[0]["seq"]
	}
	writeEnvelope(rw, out)
}

func userStarts(frames []map[string]any) []int {
	var out []int
	for i, f := range frames {
		if ev, ok := f["params"].(map[string]any)["event"].(map[string]any); ok && ev["kind"] == "user_message" {
			out = append(out, i)
		}
	}
	return out
}

// mirrorTurn 在 server 镜像里记一轮（用户一句、助手一段，可带一次工具调用）。
func (w *world) mirrorTurn(id, user, text string, tool bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	add := func(method string, params map[string]any) {
		w.mseq++
		params["conversationId"], params["seq"] = id, w.mseq
		w.mirror[id] = append(w.mirror[id], map[string]any{"seq": w.mseq, "method": method, "params": params, "createtime": 1700000000000 + w.mseq})
	}
	event := func(ev map[string]any) { add("runtime.event", map[string]any{"event": ev}) }
	event(map[string]any{"kind": "user_message", "text": user})
	if tool {
		event(map[string]any{"kind": "tool_use_start", "id": "tc1", "name": "Bash", "input": map[string]any{"command": "ls"}})
		event(map[string]any{"kind": "tool_result", "toolCallId": "tc1", "content": "a.txt"})
	}
	event(map[string]any{"kind": "text_delta", "text": text})
	add("runtime.runResultDone", map[string]any{})
}

// addTurn 在假 agentred 的会话里落一轮，交回用户那句话的帧号。
func (f *fakeAgentred) addTurn(id, user, text string, tool bool) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sessions[id]
	var userSeq int64
	add := func(n *agentrewire.RpcNotification) {
		f.seq++
		s.notes = append(s.notes, &agentrewire.DurableNotification{Seq: f.seq, Payload: n})
	}
	event := func(ev *agentrewire.RuntimeEventNotification) {
		ev.ConversationId = id
		add(&agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_RuntimeEvent{RuntimeEvent: ev}})
	}
	event(&agentrewire.RuntimeEventNotification{Event: &agentrewire.RuntimeEventNotification_UserMessage{UserMessage: &agentrewire.UserMessage{Text: user}}})
	userSeq = f.seq
	if tool {
		event(&agentrewire.RuntimeEventNotification{Event: &agentrewire.RuntimeEventNotification_ToolCall{ToolCall: &agentrewire.ToolCall{Id: "tc1", Name: "Bash", Input: []byte(`{"command":"ls"}`)}}})
		event(&agentrewire.RuntimeEventNotification{Event: &agentrewire.RuntimeEventNotification_ToolResult{ToolResult: &agentrewire.ToolResult{ToolCallId: "tc1", Content: "a.txt"}}})
	}
	event(&agentrewire.RuntimeEventNotification{Event: &agentrewire.RuntimeEventNotification_TextDelta{TextDelta: &agentrewire.TextDelta{Text: text}}})
	add(&agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_RunResultDone{RunResultDone: &agentrewire.RunResultDoneNotification{ConversationId: id}}})
	s.summary.LatestSeq = f.seq
	return userSeq
}

// 作答方法收到的请求，按序记下。
type answerLog struct {
	answers     []*agentrewire.RuntimeSubmitAnswerRequest
	permissions []*agentrewire.RuntimeSubmitToolPermissionRequest
	approvals   []*agentrewire.ToolApprovalAnswerRequest
	// approvalGone 让 toolApproval.answer 答「这张卡已不挂起」。
	approvalGone bool
}

func (f *fakeAgentred) answered() *answerLog {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.answerLog()
}

// answerLog 要求持有 f.mu。
func (f *fakeAgentred) answerLog() *answerLog {
	if f.answersLog == nil {
		f.answersLog = &answerLog{}
	}
	return f.answersLog
}

func (f *fakeAgentred) registerAnswers(reg *protorpc.Registry) {
	m := func(method agentrewire.RpcMethod) uint32 { return uint32(method) }
	protorpc.RegisterMethod(reg, m(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_ANSWER),
		func() *agentrewire.RuntimeSubmitAnswerRequest { return &agentrewire.RuntimeSubmitAnswerRequest{} },
		func(ctx context.Context, req *agentrewire.RuntimeSubmitAnswerRequest) (*agentrewire.PeerSessionControlResponse, error) {
			if err := f.guard(ctx, f.session(req.GetConversationId())); err != nil {
				return nil, err
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			l := f.answerLog()
			l.answers = append(l.answers, req)
			return &agentrewire.PeerSessionControlResponse{}, nil
		})
	protorpc.RegisterMethod(reg, m(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_TOOL_PERMISSION),
		func() *agentrewire.RuntimeSubmitToolPermissionRequest {
			return &agentrewire.RuntimeSubmitToolPermissionRequest{}
		},
		func(ctx context.Context, req *agentrewire.RuntimeSubmitToolPermissionRequest) (*agentrewire.PeerSessionControlResponse, error) {
			if err := f.guard(ctx, f.session(req.GetConversationId())); err != nil {
				return nil, err
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			l := f.answerLog()
			l.permissions = append(l.permissions, req)
			return &agentrewire.PeerSessionControlResponse{}, nil
		})
	protorpc.RegisterMethod(reg, m(agentrewire.RpcMethod_RPC_METHOD_TOOL_APPROVAL_ANSWER),
		func() *agentrewire.ToolApprovalAnswerRequest { return &agentrewire.ToolApprovalAnswerRequest{} },
		func(ctx context.Context, req *agentrewire.ToolApprovalAnswerRequest) (*agentrewire.ToolApprovalAnswerResponse, error) {
			if err := f.guard(ctx, f.session(req.GetConversationId())); err != nil {
				return nil, err
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			l := f.answerLog()
			if l.approvalGone {
				return nil, &rpcerror.Error{Code: rpcerror.CodeInvalidParams, Message: "no pending tool approval " + strconv.Quote(req.GetRequestId()) + " in this session"}
			}
			l.approvals = append(l.approvals, req)
			return &agentrewire.ToolApprovalAnswerResponse{}, nil
		})
}

func decodeRows(raw string) []map[string]any {
	var rows []map[string]any
	_ = json.Unmarshal([]byte(raw), &rows)
	return rows
}

func rowIDs(rows []map[string]any) string {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r["id"].(string))
	}
	return strings.Join(ids, ",")
}

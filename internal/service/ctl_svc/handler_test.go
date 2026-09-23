package ctl_svc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

const testToken = "test-control-token"

// ---- fakes ----

type fakeAgents struct {
	list   []*agent_entity.Agent
	byName map[string]*agent_entity.Agent
	byID   map[int64]*agent_entity.Agent
}

func (f *fakeAgents) List(context.Context) ([]*agent_entity.Agent, error) { return f.list, nil }
func (f *fakeAgents) FindByName(_ context.Context, name string) (*agent_entity.Agent, error) {
	return f.byName[name], nil
}
func (f *fakeAgents) Find(_ context.Context, id int64) (*agent_entity.Agent, error) {
	return f.byID[id], nil
}

type fakeProjects struct{ items []ProjectInfo }

func (f *fakeProjects) List(context.Context) ([]ProjectInfo, error) { return f.items, nil }

type fakeChat struct {
	ensured    *chat_svc.EnsureSessionResponse
	sendResp   *chat_svc.SendResponse
	turnCh     chan chat_svc.TurnResult
	finalText  string
	lastEnsure *chat_svc.EnsureSessionRequest
	lastSend   *chat_svc.SendRequest
	lastStop   *chat_svc.StopRequest
	stopped    bool

	streamCh        chan chat_svc.ChatStreamEvent
	streamSessionID int64
	streamCancelled bool

	answerPermissionReq *chat_svc.AnswerToolPermissionRequest
	answerPermissionErr error
}

func (f *fakeChat) EnsureSession(_ context.Context, req *chat_svc.EnsureSessionRequest) (*chat_svc.EnsureSessionResponse, error) {
	f.lastEnsure = req
	return f.ensured, nil
}
func (f *fakeChat) Send(_ context.Context, req *chat_svc.SendRequest) (*chat_svc.SendResponse, error) {
	f.lastSend = req
	return f.sendResp, nil
}
func (f *fakeChat) ObserveTurn(int64) (<-chan chat_svc.TurnResult, func()) {
	return f.turnCh, func() {}
}
func (f *fakeChat) FinalAssistantText(context.Context, int64) (string, error) {
	return f.finalText, nil
}
func (f *fakeChat) Stop(_ context.Context, req *chat_svc.StopRequest) (*chat_svc.StopResponse, error) {
	f.stopped = true
	f.lastStop = req
	return &chat_svc.StopResponse{}, nil
}
func (f *fakeChat) SessionProjectID(context.Context, int64) (int64, error) {
	return 0, nil
}
func (f *fakeChat) AnswerToolPermission(_ context.Context, req *chat_svc.AnswerToolPermissionRequest) (*chat_svc.AnswerToolPermissionResponse, error) {
	f.answerPermissionReq = req
	if f.answerPermissionErr != nil {
		return nil, f.answerPermissionErr
	}
	return &chat_svc.AnswerToolPermissionResponse{}, nil
}
func (f *fakeChat) SubscribeSessionEvents(sessionID int64) (<-chan chat_svc.ChatStreamEvent, func()) {
	f.streamSessionID = sessionID
	if f.streamCh == nil {
		f.streamCh = make(chan chat_svc.ChatStreamEvent, 16)
	}
	return f.streamCh, func() { f.streamCancelled = true }
}

func newTestHandler(a *fakeAgents, p *fakeProjects, c *fakeChat) http.Handler {
	return &ctlHandler{token: testToken, agents: a, projects: p, chat: c}
}

// 回归：bootstrap 起 gateway 时就挂 handler，deps 却要等 app.Startup(registerChatService
// → RegisterChat 之后)才 RegisterDeps。挂上去的 handler 必须看到后接线的 deps ——
// 构造时若拷贝快照，/ctl/v1/* 恒 503，agrctl ctl 与 agrctl acp 全废；而既有单测都直接
// newCtlHandler 传 deps，抓不到这个先后。
func TestControlHandler_SeesDepsRegisteredAfterMount(t *testing.T) {
	svc := newCtlSvc()
	svc.token = testToken
	mounted := svc.ControlHandler() // 先挂：此刻 deps 尚未接线

	if rec := do(t, mounted, http.MethodGet, "/ctl/v1/agents", testToken, ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("before RegisterDeps: code = %d, want 503", rec.Code)
	}

	svc.RegisterDeps(
		&fakeAgents{list: []*agent_entity.Agent{{ID: 1, Name: "planner"}}},
		&fakeProjects{}, &fakeChat{},
	)

	rec := do(t, mounted, http.MethodGet, "/ctl/v1/agents", testToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("after RegisterDeps: code = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "planner") {
		t.Fatalf("body = %s, want the late-registered agent", rec.Body.String())
	}
}

func do(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// ---- auth ----

func TestControl_RejectsMissingToken(t *testing.T) {
	h := newTestHandler(&fakeAgents{}, &fakeProjects{}, &fakeChat{})
	rec := do(t, h, http.MethodPost, "/ctl/v1/agents", "", "{}")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: code = %d, want 401", rec.Code)
	}
}

func TestControl_RejectsWrongToken(t *testing.T) {
	h := newTestHandler(&fakeAgents{}, &fakeProjects{}, &fakeChat{})
	rec := do(t, h, http.MethodPost, "/ctl/v1/agents", "nope", "{}")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: code = %d, want 401", rec.Code)
	}
}

// ---- agents ----

func TestControl_ListAgents(t *testing.T) {
	agents := &fakeAgents{list: []*agent_entity.Agent{
		{ID: 1, Name: "planner", Description: "plans"},
		{ID: 2, Name: "coder", SystemBadge: "DEFAULT", DepartmentID: 5},
	}}
	h := newTestHandler(agents, &fakeProjects{}, &fakeChat{})
	rec := do(t, h, http.MethodGet, "/ctl/v1/agents", testToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		Agents []agentDTO `json:"agents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Agents) != 2 || out.Agents[0].Name != "planner" || out.Agents[1].ID != 2 {
		t.Fatalf("unexpected agents: %+v", out.Agents)
	}
}

// ---- projects ----

func TestControl_ListProjects(t *testing.T) {
	projects := &fakeProjects{items: []ProjectInfo{{ID: 7, Name: "web", Path: "/repo/web"}}}
	h := newTestHandler(&fakeAgents{}, projects, &fakeChat{})
	rec := do(t, h, http.MethodGet, "/ctl/v1/projects", testToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	var out struct {
		Projects []ProjectInfo `json:"projects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Projects) != 1 || out.Projects[0].Path != "/repo/web" {
		t.Fatalf("unexpected projects: %+v", out.Projects)
	}
}

// ---- send (fire-and-forget) ----

func TestControl_SendByName_CreatesUserChatSession(t *testing.T) {
	agents := &fakeAgents{byName: map[string]*agent_entity.Agent{
		"planner": {ID: 11, Name: "planner"},
	}}
	chat := &fakeChat{
		ensured:  &chat_svc.EnsureSessionResponse{SessionID: 100, Created: true},
		sendResp: &chat_svc.SendResponse{SessionID: 100, AssistantMessageID: 200, Stream: "chat:100:200"},
	}
	h := newTestHandler(agents, &fakeProjects{}, chat)

	rec := do(t, h, http.MethodPost, "/ctl/v1/send", testToken,
		`{"agent":"planner","projectId":3,"text":"ship it"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var out sendDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.SessionID != 100 || out.AssistantMessageID != 200 || out.Done {
		t.Fatalf("unexpected send resp: %+v", out)
	}
	// 建的是普通可见会话，绑定解析出的 agent 与传入的 project。
	if chat.lastEnsure == nil || chat.lastEnsure.Purpose != chat_svc.SessionPurposeUserChat {
		t.Fatalf("ensure purpose = %+v, want UserChat", chat.lastEnsure)
	}
	if chat.lastEnsure.AgentID != 11 || chat.lastEnsure.ProjectID != 3 {
		t.Fatalf("ensure agent/project = %+v", chat.lastEnsure)
	}
	if chat.lastSend == nil || chat.lastSend.SessionID != 100 || chat.lastSend.Text != "ship it" {
		t.Fatalf("send req = %+v", chat.lastSend)
	}
}

func TestControl_SendUnknownAgent_404(t *testing.T) {
	h := newTestHandler(&fakeAgents{byName: map[string]*agent_entity.Agent{}}, &fakeProjects{}, &fakeChat{})
	rec := do(t, h, http.MethodPost, "/ctl/v1/send", testToken, `{"agent":"ghost","text":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown agent: code = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestControl_SendMissingText_400(t *testing.T) {
	agents := &fakeAgents{byName: map[string]*agent_entity.Agent{"a": {ID: 1, Name: "a"}}}
	h := newTestHandler(agents, &fakeProjects{}, &fakeChat{})
	rec := do(t, h, http.MethodPost, "/ctl/v1/send", testToken, `{"agent":"a","text":"  "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty text: code = %d, want 400", rec.Code)
	}
}

// ---- send --wait ----

func TestControl_SendWait_ReturnsFinalText(t *testing.T) {
	agents := &fakeAgents{byName: map[string]*agent_entity.Agent{"a": {ID: 1, Name: "a"}}}
	turnCh := make(chan chat_svc.TurnResult, 1)
	turnCh <- chat_svc.TurnResult{SessionID: 100, AssistantMessageID: 200}
	chat := &fakeChat{
		ensured:   &chat_svc.EnsureSessionResponse{SessionID: 100, Created: true},
		sendResp:  &chat_svc.SendResponse{SessionID: 100, AssistantMessageID: 200},
		turnCh:    turnCh,
		finalText: "the answer",
	}
	h := newTestHandler(agents, &fakeProjects{}, chat)
	rec := do(t, h, http.MethodPost, "/ctl/v1/send", testToken, `{"agent":"a","text":"go","wait":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var out sendDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Done || out.Text != "the answer" {
		t.Fatalf("wait resp = %+v, want done+text", out)
	}
}

func TestControl_SendByID(t *testing.T) {
	agents := &fakeAgents{byID: map[int64]*agent_entity.Agent{9: {ID: 9, Name: "byid"}}}
	chat := &fakeChat{
		ensured:  &chat_svc.EnsureSessionResponse{SessionID: 1},
		sendResp: &chat_svc.SendResponse{SessionID: 1, AssistantMessageID: 2},
	}
	h := newTestHandler(agents, &fakeProjects{}, chat)
	rec := do(t, h, http.MethodPost, "/ctl/v1/send", testToken, `{"agentId":9,"text":"hi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if chat.lastEnsure.AgentID != 9 {
		t.Fatalf("ensure agentID = %d, want 9", chat.lastEnsure.AgentID)
	}
}

func TestControl_UnknownPath_404(t *testing.T) {
	h := newTestHandler(&fakeAgents{}, &fakeProjects{}, &fakeChat{})
	rec := do(t, h, http.MethodPost, "/ctl/v1/bogus", testToken, "{}")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bogus path: code = %d, want 404", rec.Code)
	}
}

// ---- sessions ----

func TestControl_CreateSession_ByName(t *testing.T) {
	agents := &fakeAgents{byName: map[string]*agent_entity.Agent{"planner": {ID: 11, Name: "planner"}}}
	chat := &fakeChat{ensured: &chat_svc.EnsureSessionResponse{SessionID: 100, Created: true}}
	h := newTestHandler(agents, &fakeProjects{}, chat)

	rec := do(t, h, http.MethodPost, "/ctl/v1/sessions", testToken, `{"agent":"planner","projectId":3}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		SessionID int64 `json:"sessionId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.SessionID != 100 {
		t.Fatalf("sessionId = %d, want 100", out.SessionID)
	}
	if chat.lastEnsure == nil || chat.lastEnsure.AgentID != 11 || chat.lastEnsure.ProjectID != 3 {
		t.Fatalf("ensure = %+v", chat.lastEnsure)
	}
}

func TestControl_CreateSession_UnknownAgent_404(t *testing.T) {
	h := newTestHandler(&fakeAgents{byName: map[string]*agent_entity.Agent{}}, &fakeProjects{}, &fakeChat{})
	rec := do(t, h, http.MethodPost, "/ctl/v1/sessions", testToken, `{"agent":"ghost"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestControl_CreateSession_RequiresPost(t *testing.T) {
	h := newTestHandler(&fakeAgents{}, &fakeProjects{}, &fakeChat{})
	rec := do(t, h, http.MethodGet, "/ctl/v1/sessions", testToken, "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d, want 405", rec.Code)
	}
}

// ---- stop ----

func TestControl_Stop(t *testing.T) {
	chat := &fakeChat{}
	h := newTestHandler(&fakeAgents{}, &fakeProjects{}, chat)
	rec := do(t, h, http.MethodPost, "/ctl/v1/stop", testToken, `{"sessionId":100}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if !chat.stopped || chat.lastStop == nil || chat.lastStop.SessionID != 100 {
		t.Fatalf("stop = %+v stopped=%v", chat.lastStop, chat.stopped)
	}
}

func TestControl_Stop_RequiresSessionID(t *testing.T) {
	h := newTestHandler(&fakeAgents{}, &fakeProjects{}, &fakeChat{})
	rec := do(t, h, http.MethodPost, "/ctl/v1/stop", testToken, `{"sessionId":0}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
}

// ---- send images + existing session ----

func TestControl_SendWithImages(t *testing.T) {
	agents := &fakeAgents{byName: map[string]*agent_entity.Agent{"a": {ID: 1, Name: "a"}}}
	chat := &fakeChat{
		ensured:  &chat_svc.EnsureSessionResponse{SessionID: 100, Created: true},
		sendResp: &chat_svc.SendResponse{SessionID: 100, AssistantMessageID: 200},
	}
	h := newTestHandler(agents, &fakeProjects{}, chat)
	rec := do(t, h, http.MethodPost, "/ctl/v1/send", testToken,
		`{"agent":"a","text":"look","images":[{"name":"pic.png","dataUrl":"data:image/png;base64,AAAA"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if chat.lastSend == nil || len(chat.lastSend.Images) != 1 {
		t.Fatalf("images = %+v, want 1", chat.lastSend)
	}
	if got := chat.lastSend.Images[0]; got.Name != "pic.png" || got.DataURL != "data:image/png;base64,AAAA" {
		t.Fatalf("image = %+v", got)
	}
}

func TestControl_SendToExistingSession_SkipsEnsure(t *testing.T) {
	chat := &fakeChat{sendResp: &chat_svc.SendResponse{SessionID: 100, AssistantMessageID: 200}}
	h := newTestHandler(&fakeAgents{}, &fakeProjects{}, chat)
	rec := do(t, h, http.MethodPost, "/ctl/v1/send", testToken, `{"sessionId":100,"text":"again"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if chat.lastEnsure != nil {
		t.Fatalf("existing session must not call EnsureSession: %+v", chat.lastEnsure)
	}
	if chat.lastSend == nil || chat.lastSend.SessionID != 100 {
		t.Fatalf("send = %+v, want sessionId 100", chat.lastSend)
	}
}

// ---- stream (SSE) ----

func TestControl_Stream_PushesEvents(t *testing.T) {
	chat := &fakeChat{streamCh: make(chan chat_svc.ChatStreamEvent, 4)}
	srv := httptest.NewServer(newTestHandler(&fakeAgents{}, &fakeProjects{}, chat))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/ctl/v1/stream?sessionId=42", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}

	// 头已到达 → handler 已订阅(先订阅后写头)。
	chat.streamCh <- chat_svc.ChatStreamEvent{Kind: chat_svc.StreamChunk, Delta: "he"}
	chat.streamCh <- chat_svc.ChatStreamEvent{Kind: chat_svc.StreamChunk, Delta: "llo"}
	chat.streamCh <- chat_svc.ChatStreamEvent{Kind: chat_svc.StreamDone}

	scanner := bufio.NewScanner(resp.Body)
	var got []chat_svc.ChatStreamEvent
	deadline := time.After(2 * time.Second)
	for len(got) < 3 {
		select {
		case <-deadline:
			t.Fatalf("timed out; got %+v", got)
		default:
		}
		if !scanner.Scan() {
			t.Fatalf("stream ended early; scanner err=%v got=%+v", scanner.Err(), got)
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev chat_svc.ChatStreamEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		got = append(got, ev)
	}
	if chat.streamSessionID != 42 {
		t.Fatalf("subscribed sessionId = %d, want 42", chat.streamSessionID)
	}
	if got[0].Delta != "he" || got[1].Delta != "llo" || got[2].Kind != chat_svc.StreamDone {
		t.Fatalf("events = %+v", got)
	}
}

func TestControl_Stream_BadSessionID_400(t *testing.T) {
	h := newTestHandler(&fakeAgents{}, &fakeProjects{}, &fakeChat{})
	rec := do(t, h, http.MethodGet, "/ctl/v1/stream?sessionId=0", testToken, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestControl_Stream_RequiresGet(t *testing.T) {
	h := newTestHandler(&fakeAgents{}, &fakeProjects{}, &fakeChat{})
	rec := do(t, h, http.MethodPost, "/ctl/v1/stream?sessionId=1", testToken, "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d, want 405", rec.Code)
	}
}

// ---- answer-permission ----

// TestControl_AnswerPermission_ForwardsDecision 覆盖 agrctl acp 的权限回灌:
// 桌面侧把 ACP client 选中的 option 决策落到 chat_svc.AnswerToolPermission,
// 字段必须逐字透传(sessionId / requestId / allow / alwaysAllowSession)。
func TestControl_AnswerPermission_ForwardsDecision(t *testing.T) {
	chat := &fakeChat{}
	h := newTestHandler(&fakeAgents{}, &fakeProjects{}, chat)
	rec := do(t, h, http.MethodPost, "/ctl/v1/answer-permission", testToken,
		`{"sessionId":100,"requestId":"req-1","allow":true,"alwaysAllowSession":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	req := chat.answerPermissionReq
	if req == nil {
		t.Fatal("AnswerToolPermission was not called")
	}
	if req.SessionID != 100 || req.RequestID != "req-1" || !req.Allow || !req.AlwaysAllowSession {
		t.Fatalf("req = %+v, want sessionId=100 requestId=req-1 allow=true alwaysAllow=true", req)
	}
}

func TestControl_AnswerPermission_ValidatesParams(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"missing sessionId", `{"requestId":"req-1","allow":true}`},
		{"missing requestId", `{"sessionId":100,"allow":true}`},
		{"blank requestId", `{"sessionId":100,"requestId":"  ","allow":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler(&fakeAgents{}, &fakeProjects{}, &fakeChat{})
			rec := do(t, h, http.MethodPost, "/ctl/v1/answer-permission", testToken, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestControl_AnswerPermission_UnavailableWithoutDeps(t *testing.T) {
	// 直接传 nil ChatGateway(而不是 (*fakeChat)(nil) 这种 typed-nil),后者会让
	// h.chat != nil 恒成立,测不到 503。
	h := &ctlHandler{token: testToken, agents: &fakeAgents{}, projects: &fakeProjects{}}
	rec := do(t, h, http.MethodPost, "/ctl/v1/answer-permission", testToken, `{"sessionId":1,"requestId":"req-1"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestControl_AnswerPermission_RequiresPost(t *testing.T) {
	h := newTestHandler(&fakeAgents{}, &fakeProjects{}, &fakeChat{})
	rec := do(t, h, http.MethodGet, "/ctl/v1/answer-permission", testToken, "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d, want 405", rec.Code)
	}
}

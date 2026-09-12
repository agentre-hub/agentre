package ctl_svc

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
	stopped    bool
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
func (f *fakeChat) Stop(context.Context, *chat_svc.StopRequest) (*chat_svc.StopResponse, error) {
	f.stopped = true
	return &chat_svc.StopResponse{}, nil
}
func (f *fakeChat) SessionProjectID(context.Context, int64) (int64, error) {
	return 0, nil
}

func newTestHandler(a *fakeAgents, p *fakeProjects, c *fakeChat) http.Handler {
	return newCtlHandler(testToken, a, p, c)
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

package ctl_svc

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/dispatch_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

const (
	convA = "0193b6f0-7c5e-7d2a-9f4b-3c1d2e5f6a7b" // 本机会话 100
	convB = "0193b6f0-7c5e-7d2a-9f4b-3c1d2e5f6a7c" // 本机会话 42(会话 token 所在的会话)
	convX = "0193b6f0-7c5e-7d2a-9f4b-3c1d2e5f6aff" // 合法但本机没有
)

// fakeConversations 是 chat_svc.CtlConversationGateway 的内存实现。
type fakeConversations struct {
	mu       sync.Mutex
	sessions map[int64]*chat_svc.CtlConversation
	// describe 非 nil 时由它给出 Describe 的结果(第 n 次调用),模拟状态推进。
	describe func(n int, sessionID int64) *chat_svc.CtlConversation
	calls    int

	checkErr   error
	checkedFor []int64 // agentID / sessionID 交替无所谓,只看是否先于建会话
	// checkedEngine 是新会话校验覆盖时按的那份引擎配置(0 = 成员生效的那份)。
	checkedEngine []int64
	// plan 是派发方案;engines 是 --engine 能解析的引擎配置;planned 记下问方案时的引擎配置。
	plan        *chat_svc.CtlDispatch
	engines     []chat_svc.CtlEngine
	planned     []int64
	pinned      map[int64]pinnedDispatch
	checked     []chat_svc.CtlOverrides
	applied     map[int64]chat_svc.CtlOverrides
	steered     []string
	discardN    int
	discarded   bool
	pending     wire.SessionPendingWaitersResult
	tail        *chat_svc.CtlTail
	tailErr     error
	tailFrom    int64
	tailResume  bool
	tailSession int64

	list             []*chat_svc.CtlConversation
	listFilter       chat_svc.CtlListFilter
	detail           *chat_svc.CtlConversationDetail
	messages         *chat_svc.CtlMessagesPage
	messagesSession  int64
	messagesQuery    chat_svc.CtlMessagesQuery
	answeredQuestion *questionAnswer
	approvals        []chat_svc.CtlPendingApproval
	answeredApproval *approvalAnswer
}

type approvalAnswer struct {
	sessionID int64
	requestID string
	allow     bool
}

type questionAnswer struct {
	sessionID int64
	requestID string
	answers   []blocks.AskAnswerDTO
}

func newFakeConversations(convs ...*chat_svc.CtlConversation) *fakeConversations {
	f := &fakeConversations{sessions: map[int64]*chat_svc.CtlConversation{}, applied: map[int64]chat_svc.CtlOverrides{}}
	for _, c := range convs {
		f.sessions[c.SessionID] = c
	}
	return f
}

func (f *fakeConversations) LookupCtlConversation(_ context.Context, id string) (*chat_svc.CtlConversation, error) {
	if len(id) != 36 {
		return nil, chat_svc.ErrCtlConversationInvalidID
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.sessions {
		if c.ConversationID == id {
			cp := *c
			return &cp, nil
		}
	}
	return nil, nil
}

func (f *fakeConversations) DescribeCtlConversation(_ context.Context, sessionID int64) (*chat_svc.CtlConversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.describe != nil {
		return f.describe(f.calls, sessionID), nil
	}
	c, ok := f.sessions[sessionID]
	if !ok {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}

func (f *fakeConversations) CheckCtlOverrides(_ context.Context, agentID, engineProfileID, sessionID int64, o chat_svc.CtlOverrides) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checked = append(f.checked, o)
	f.checkedEngine = append(f.checkedEngine, engineProfileID)
	f.checkedFor = append(f.checkedFor, agentID+sessionID)
	return f.checkErr
}

func (f *fakeConversations) ResolveCtlEngine(_ context.Context, ref string) (*chat_svc.CtlEngine, error) {
	var options []string
	for i := range f.engines {
		options = append(options, f.engines[i].Name)
		if f.engines[i].Name == ref || strconv.FormatInt(f.engines[i].ID, 10) == ref {
			return &f.engines[i], nil
		}
	}
	return nil, &chat_svc.CtlEngineNotFoundError{Ref: ref, Options: options}
}

func (f *fakeConversations) CtlDispatchPlan(_ context.Context, _, _, engineProfileID int64) (*chat_svc.CtlDispatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.planned = append(f.planned, engineProfileID)
	if f.plan == nil { // 没布置方案的用例:本机通过、自动选中
		return &chat_svc.CtlDispatch{Found: true, Chosen: "sha256:self", Devices: []chat_svc.CtlDevice{
			{Name: "this desktop", Refs: []string{"local"}, Fingerprint: "sha256:self", Eligible: true, Chosen: true},
		}}, nil
	}
	return f.plan, nil
}

type pinnedDispatch struct {
	engineProfileID int64
	device          devicefp.Carrier
}

func (f *fakeConversations) PinCtlDispatch(_ context.Context, sessionID, engineProfileID int64, device devicefp.Carrier) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pinned == nil {
		f.pinned = map[int64]pinnedDispatch{}
	}
	f.pinned[sessionID] = pinnedDispatch{engineProfileID, device}
	return nil
}

func (f *fakeConversations) ApplyCtlOverrides(_ context.Context, sessionID int64, o chat_svc.CtlOverrides) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied[sessionID] = o
	return nil
}

func (f *fakeConversations) SteerCtlConversation(_ context.Context, _ int64, text string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steered = append(f.steered, text)
	return "q-1", nil
}

func (f *fakeConversations) DiscardCtlSteers(context.Context, int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.discarded = true
	return f.discardN, nil
}

func (f *fakeConversations) PendingCtlWaiters(context.Context, int64) (wire.SessionPendingWaitersResult, error) {
	return f.pending, nil
}

func (f *fakeConversations) PendingCtlApprovals(context.Context, int64) []chat_svc.CtlPendingApproval {
	return f.approvals
}

func (f *fakeConversations) AnswerToolApproval(_ context.Context, sessionID int64, requestID string, allow bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answeredApproval = &approvalAnswer{sessionID: sessionID, requestID: requestID, allow: allow}
	return nil
}

func (f *fakeConversations) ListCtlConversations(_ context.Context, flt chat_svc.CtlListFilter) ([]*chat_svc.CtlConversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listFilter = flt
	return f.list, nil
}

func (f *fakeConversations) DetailCtlConversation(_ context.Context, sessionID int64) (*chat_svc.CtlConversationDetail, error) {
	if c := f.sessions[sessionID]; c != nil && f.detail != nil {
		d := *f.detail
		d.CtlConversation = *c
		return &d, nil
	}
	return nil, nil
}

func (f *fakeConversations) ListCtlMessages(_ context.Context, sessionID int64, q chat_svc.CtlMessagesQuery) (*chat_svc.CtlMessagesPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messagesSession, f.messagesQuery = sessionID, q
	return f.messages, nil
}

func (f *fakeConversations) AnswerCtlQuestion(_ context.Context, sessionID int64, requestID string, answers []blocks.AskAnswerDTO) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answeredQuestion = &questionAnswer{sessionID: sessionID, requestID: requestID, answers: answers}
	return nil
}

func (f *fakeConversations) TailCtlConversation(_ context.Context, sessionID, from int64, resume bool) (*chat_svc.CtlTail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tailSession, f.tailFrom, f.tailResume = sessionID, from, resume
	return f.tail, f.tailErr
}

func conv(sessionID int64, id string, status chat_svc.CtlConversationStatus) *chat_svc.CtlConversation {
	return &chat_svc.CtlConversation{SessionID: sessionID, ConversationID: id, AgentID: 11, AgentName: "planner", Status: status}
}

type convRig struct {
	h      *ctlHandler
	chat   *fakeChat
	convs  *fakeConversations
	signer *agenttool.TokenSigner
}

func newConvRig(t *testing.T, convs *fakeConversations) *convRig {
	t.Helper()
	chat := &fakeChat{
		ensured:  &chat_svc.EnsureSessionResponse{SessionID: 100, Created: true},
		sendResp: &chat_svc.SendResponse{SessionID: 100, AssistantMessageID: 200},
	}
	signer := agenttool.NewTokenSigner()
	h := &ctlHandler{
		token: testToken, sessions: signer,
		agents:        &fakeAgents{byName: map[string]*agent_entity.Agent{"planner": {ID: 11, Name: "planner"}}, byID: map[int64]*agent_entity.Agent{11: {ID: 11, Name: "planner"}}},
		projects:      &fakeProjects{},
		chat:          chat,
		conversations: convs,
		pollInterval:  5 * time.Millisecond,
	}
	return &convRig{h: h, chat: chat, convs: convs, signer: signer}
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out
}

// ---- send: 新会话 ----

// Given 一个 agent When send(带 --title 与覆盖) Then 先校验覆盖、再建会话、把覆盖写到新会话上、
// 起轮,stdout 要的是完整的 conversation UUID 而不是数字 id。
func TestSend_NewSession_ReturnsConversationIDAndAppliesOverrides(t *testing.T) {
	convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusRunning))
	rig := newConvRig(t, convs)
	rec := do(t, rig.h, http.MethodPost, "/ctl/v1/send", testToken,
		`{"agent":"planner","text":"go","title":"Nightly","effort":"high","permissionMode":"plan"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	out := decode(t, rec)
	assert.Equal(t, convA, out["conversationId"])
	assert.Equal(t, "running", out["status"])
	assert.Equal(t, "planner", out["agent"])
	assert.NotContains(t, out, "sessionId", "the numeric id is no longer part of the contract")
	require.NotNil(t, rig.chat.lastEnsure)
	assert.Equal(t, "Nightly", rig.chat.lastEnsure.Title)
	assert.Equal(t, chat_svc.CtlOverrides{Effort: "high", PermissionMode: "plan"}, convs.applied[100])
	require.NotNil(t, rig.chat.lastSend)
	assert.Equal(t, int64(100), rig.chat.lastSend.SessionID)
}

// Given 一项目标后端不支持的覆盖 When send Then 422 带可选值,且根本不建会话。
func TestSend_OverrideRejected_422WithoutCreatingSession(t *testing.T) {
	convs := newFakeConversations()
	convs.checkErr = &chat_svc.CtlOverrideError{Flag: "effort", Value: "turbo", Options: []string{"low", "high"}}
	rig := newConvRig(t, convs)
	rec := do(t, rig.h, http.MethodPost, "/ctl/v1/send", testToken, `{"agent":"planner","text":"go","effort":"turbo"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "one of: low, high")
	assert.Nil(t, rig.chat.lastEnsure)
	assert.Nil(t, rig.chat.lastSend)
}

// ---- send: 继续 ----

func TestSend_ContinueByConversationID(t *testing.T) {
	convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusIdle))
	rig := newConvRig(t, convs)
	rec := do(t, rig.h, http.MethodPost, "/ctl/v1/send", testToken, `{"conversationId":"`+convA+`","text":"again","model":"anthropic/opus"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, convA, decode(t, rec)["conversationId"])
	assert.Nil(t, rig.chat.lastEnsure, "continuing never creates a session")
	require.NotNil(t, rig.chat.lastSend)
	assert.Equal(t, int64(100), rig.chat.lastSend.SessionID)
	assert.Equal(t, chat_svc.CtlOverrides{Model: "anthropic/opus"}, convs.applied[100])
}

func TestSend_ContinueRejections(t *testing.T) {
	cases := []struct {
		name string
		body string
		code int
		msg  string
	}{
		{"malformed id", `{"conversationId":"42","text":"x"}`, http.StatusBadRequest, "session UUID"},
		{"unknown id", `{"conversationId":"` + convX + `","text":"x"}`, http.StatusNotFound, "session not found or not accessible"},
		{"with agent", `{"conversationId":"` + convA + `","agent":"planner","text":"x"}`, http.StatusBadRequest, "cannot be combined"},
		{"with isolated", `{"conversationId":"` + convA + `","isolated":true,"text":"x"}`, http.StatusBadRequest, "cannot be combined"},
		{"steer with images", `{"conversationId":"` + convA + `","steer":true,"text":"x","images":[{"dataUrl":"data:image/png;base64,AA"}]}`, http.StatusBadRequest, "--steer"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rig := newConvRig(t, newFakeConversations(conv(100, convA, chat_svc.CtlStatusRunning)))
			rec := do(t, rig.h, http.MethodPost, "/ctl/v1/send", testToken, c.body)
			assert.Equal(t, c.code, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), c.msg)
			assert.Nil(t, rig.chat.lastSend)
		})
	}
}

// Given 会话正在跑 When 不带 --steer 继续 Then 409「会话运行中」;带 --steer 时插进当前这一轮,
// 不开新轮。
func TestSend_RunningSession_RefusesOrSteers(t *testing.T) {
	t.Run("without steer → 409", func(t *testing.T) {
		convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusRunning))
		rig := newConvRig(t, convs)
		rec := do(t, rig.h, http.MethodPost, "/ctl/v1/send", testToken, `{"conversationId":"`+convA+`","text":"more","effort":"low"}`)
		assert.Equal(t, http.StatusConflict, rec.Code)
		assert.Contains(t, rec.Body.String(), "--steer")
		assert.Nil(t, rig.chat.lastSend)
		assert.Empty(t, convs.applied, "a refused send changes no setting")
	})
	t.Run("agentred refuses the turn after dispatch → wait reports session running", func(t *testing.T) {
		rig := newConvRig(t, newFakeConversations(conv(100, convA, chat_svc.CtlStatusFailed)))
		rig.chat.turnCh = make(chan chat_svc.TurnResult, 1)
		rig.chat.turnCh <- chat_svc.TurnResult{SessionID: 100, Err: wire.ErrSessionRunning}
		out := decode(t, do(t, rig.h, http.MethodPost, "/ctl/v1/send", testToken, `{"conversationId":"`+convA+`","text":"more","wait":true}`))
		assert.Equal(t, "failed", out["status"])
		assert.Contains(t, out["error"], "session is running")
	})
	t.Run("with steer → queued into the running turn", func(t *testing.T) {
		convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusRunning))
		rig := newConvRig(t, convs)
		rec := do(t, rig.h, http.MethodPost, "/ctl/v1/send", testToken, `{"conversationId":"`+convA+`","text":"more","steer":true,"effort":"low"}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		out := decode(t, rec)
		assert.Equal(t, chat_svc.CtlOverrides{Effort: "low"}, convs.applied[100], "overrides apply to the following turns even when steering")
		assert.Equal(t, true, out["steered"])
		assert.Equal(t, "running", out["status"])
		assert.Equal(t, []string{"more"}, convs.steered)
		assert.Nil(t, rig.chat.lastSend, "a steer never starts a new turn")
	})
	t.Run("steer on an idle session is a plain continue", func(t *testing.T) {
		convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusIdle))
		rig := newConvRig(t, convs)
		rec := do(t, rig.h, http.MethodPost, "/ctl/v1/send", testToken, `{"conversationId":"`+convA+`","text":"more","steer":true}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Empty(t, convs.steered)
		require.NotNil(t, rig.chat.lastSend)
	})
	for name, sendErr := range map[string]error{
		"desktop turn in flight": &httputils.Error{Code: code.ChatSendInFlight, Msg: "in flight"},
		"agentred refuses":       wire.ErrSessionRunning,
	} {
		t.Run(name+" → 409", func(t *testing.T) {
			rig := newConvRig(t, newFakeConversations(conv(100, convA, chat_svc.CtlStatusIdle)))
			rig.chat.sendErr = sendErr
			rec := do(t, rig.h, http.MethodPost, "/ctl/v1/send", testToken, `{"conversationId":"`+convA+`","text":"more"}`)
			assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "session is running")
		})
	}
}

// ---- 会话级 token ----

// spec「授权与审批」会话级 token:可以继续任何可见会话,自己所在的会话除外;可以读 / 等;
// 不能停止。
func TestSessionToken_ConversationRules(t *testing.T) {
	convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusIdle), conv(42, convB, chat_svc.CtlStatusIdle))
	rig := newConvRig(t, convs)
	tok := rig.signer.MintToken(7, 42)

	rec := do(t, rig.h, http.MethodPost, "/ctl/v1/send", tok, `{"conversationId":"`+convB+`","text":"me"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "cannot send to your own session")
	assert.Nil(t, rig.chat.lastSend)

	rec = do(t, rig.h, http.MethodPost, "/ctl/v1/send", tok, `{"conversationId":"`+convA+`","text":"other"}`)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = do(t, rig.h, http.MethodPost, "/ctl/v1/wait", tok, `{"conversationId":"`+convB+`"}`)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = do(t, rig.h, http.MethodPost, "/ctl/v1/stop", tok, `{"conversationId":"`+convA+`"}`)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// ---- wait ----

func TestWait_Outcomes(t *testing.T) {
	t.Run("idle → final text of the last turn", func(t *testing.T) {
		c := conv(100, convA, chat_svc.CtlStatusIdle)
		c.LastAssistantMessageID = 200
		rig := newConvRig(t, newFakeConversations(c))
		rig.chat.finalText = "the answer"
		out := decode(t, do(t, rig.h, http.MethodPost, "/ctl/v1/wait", testToken, `{"conversationId":"`+convA+`"}`))
		assert.Equal(t, "idle", out["status"])
		assert.Equal(t, "the answer", out["text"])
	})
	t.Run("failed → error summary", func(t *testing.T) {
		c := conv(100, convA, chat_svc.CtlStatusFailed)
		c.LastError = "provider exploded"
		rig := newConvRig(t, newFakeConversations(c))
		out := decode(t, do(t, rig.h, http.MethodPost, "/ctl/v1/wait", testToken, `{"conversationId":"`+convA+`"}`))
		assert.Equal(t, "failed", out["status"])
		assert.Equal(t, "provider exploded", out["error"])
	})
	t.Run("waiting → pending items", func(t *testing.T) {
		convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusWaiting))
		convs.pending = wire.SessionPendingWaitersResult{ToolPermissions: []agentruntime.PendingToolPermission{{RequestID: "req-1", ToolName: "Bash"}}}
		rig := newConvRig(t, convs)
		out := decode(t, do(t, rig.h, http.MethodPost, "/ctl/v1/wait", testToken, `{"conversationId":"`+convA+`"}`))
		assert.Equal(t, "waiting", out["status"])
		assert.Contains(t, mustJSON(t, out["pending"]), "req-1")
	})
	t.Run("running until the timeout → status running", func(t *testing.T) {
		rig := newConvRig(t, newFakeConversations(conv(100, convA, chat_svc.CtlStatusRunning)))
		out := decode(t, do(t, rig.h, http.MethodPost, "/ctl/v1/wait", testToken, `{"conversationId":"`+convA+`","timeoutMs":30}`))
		assert.Equal(t, "running", out["status"])
	})
	t.Run("running then idle → waits it out", func(t *testing.T) {
		convs := newFakeConversations()
		convs.describe = func(n int, id int64) *chat_svc.CtlConversation {
			if n < 3 {
				return conv(id, convA, chat_svc.CtlStatusRunning)
			}
			c := conv(id, convA, chat_svc.CtlStatusIdle)
			c.LastAssistantMessageID = 201
			return c
		}
		convs.sessions[100] = conv(100, convA, chat_svc.CtlStatusRunning)
		rig := newConvRig(t, convs)
		rig.chat.finalText = "done at last"
		out := decode(t, do(t, rig.h, http.MethodPost, "/ctl/v1/wait", testToken, `{"conversationId":"`+convA+`"}`))
		assert.Equal(t, "idle", out["status"])
		assert.Equal(t, "done at last", out["text"])
	})
	t.Run("unknown session → 404", func(t *testing.T) {
		rig := newConvRig(t, newFakeConversations())
		rec := do(t, rig.h, http.MethodPost, "/ctl/v1/wait", testToken, `{"conversationId":"`+convX+`"}`)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

// send --wait 与 wait 是同一套等待:会话进入等待输入就带着待处理项返回,不再一直挂着。
func TestSendWait_ReturnsPendingWhenWaitingForInput(t *testing.T) {
	convs := newFakeConversations()
	convs.describe = func(_ int, id int64) *chat_svc.CtlConversation { return conv(id, convA, chat_svc.CtlStatusWaiting) }
	convs.pending = wire.SessionPendingWaitersResult{AskUserQuestions: []agentruntime.PendingAskUserQuestion{{RequestID: "ask-1"}}}
	rig := newConvRig(t, convs)
	rig.chat.turnCh = make(chan chat_svc.TurnResult, 1)
	out := decode(t, do(t, rig.h, http.MethodPost, "/ctl/v1/send", testToken, `{"agent":"planner","text":"go","wait":true}`))
	assert.Equal(t, "waiting", out["status"])
	assert.Contains(t, mustJSON(t, out["pending"]), "ask-1")
	assert.False(t, rig.chat.stopped, "returning for input must not stop the turn")
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return string(raw)
}

// ---- stop ----

func TestStop_DropsUndeliveredSteers(t *testing.T) {
	t.Run("running → stop and report dropped steers", func(t *testing.T) {
		convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusRunning))
		convs.discardN = 2
		rig := newConvRig(t, convs)
		rec := do(t, rig.h, http.MethodPost, "/ctl/v1/stop", testToken, `{"conversationId":"`+convA+`"}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		out := decode(t, rec)
		assert.Equal(t, true, out["stopped"])
		assert.Equal(t, float64(2), out["droppedSteers"])
		assert.True(t, convs.discarded)
		require.NotNil(t, rig.chat.lastStop)
		assert.Equal(t, int64(100), rig.chat.lastStop.SessionID)
	})
	t.Run("not running → nothing to stop", func(t *testing.T) {
		convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusIdle))
		rig := newConvRig(t, convs)
		out := decode(t, do(t, rig.h, http.MethodPost, "/ctl/v1/stop", testToken, `{"conversationId":"`+convA+`"}`))
		assert.Equal(t, false, out["running"])
		assert.Equal(t, false, out["stopped"])
		assert.False(t, rig.chat.stopped)
		assert.False(t, convs.discarded)
	})
}

// ---- tail ----

func frame(seq int64, preview bool, ev agentruntime.Event) wire.EventFrame {
	return wire.EventFrame{ConversationID: convA, Seq: seq, Preview: preview, Event: ev}
}

// Given --from 一个游标 When tail --until-idle Then 先补齐错过的持久帧,再接实时帧,每行带不透明
// 游标(预览帧沿用最后一个持久帧的游标);轮次结束后会话空闲即以 status 行收尾。
func TestTail_ReplaysFromCursorThenLiveUntilIdle(t *testing.T) {
	live := make(chan chat_svc.CtlTailNotification, 4)
	convs := newFakeConversations()
	var turnDone sync.Once
	done := make(chan struct{})
	convs.describe = func(_ int, id int64) *chat_svc.CtlConversation {
		select {
		case <-done:
			return conv(id, convA, chat_svc.CtlStatusIdle)
		default:
			return conv(id, convA, chat_svc.CtlStatusRunning)
		}
	}
	convs.sessions[100] = conv(100, convA, chat_svc.CtlStatusRunning)
	convs.tail = &chat_svc.CtlTail{
		Replay: []chat_svc.CtlTailNotification{
			{Seq: 3, Method: wire.NotifyEvent, Params: frame(3, false, agentruntime.ToolCall{ID: "t1", Name: "Bash"})},
			{Seq: 4, Method: wire.NotifyEvent, Params: frame(4, false, agentruntime.ToolResult{ToolCallID: "t1", Content: "ok"})},
		},
		Live:      live,
		Lagged:    func() bool { return false },
		LatestSeq: 4,
	}
	rig := newConvRig(t, convs)
	srv := httptest.NewServer(rig.h)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/ctl/v1/tail?conversationId="+convA+"&from=v1:2&untilIdle=true", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var lines []map[string]any
	sc := bufio.NewScanner(resp.Body)
	deadline := time.After(3 * time.Second)
	for sc.Scan() {
		var line map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &line), sc.Text())
		lines = append(lines, line)
		switch {
		case line["type"] == "status" && line["status"] == "running":
			// 补齐完、报过运行态之后才来实时帧。
			live <- chat_svc.CtlTailNotification{Method: wire.NotifyEvent, Params: frame(0, true, agentruntime.TextDelta{Text: "he"})}
			live <- chat_svc.CtlTailNotification{Method: wire.NotifyRunResultDone, Params: wire.RunResultDoneFrame{ConversationID: convA}}
		case line["type"] == "turn_done":
			turnDone.Do(func() { close(done) })
		}
		select {
		case <-deadline:
			t.Fatalf("tail did not end; lines=%v", lines)
		default:
		}
	}
	assert.Equal(t, int64(2), convs.tailFrom)
	assert.True(t, convs.tailResume)
	require.GreaterOrEqual(t, len(lines), 5, "%v", lines)
	assert.Equal(t, "tool_call", lines[0]["type"])
	assert.Equal(t, "v1:3", lines[0]["cursor"])
	assert.Equal(t, "tool_result", lines[1]["type"])
	assert.Equal(t, "v1:4", lines[1]["cursor"])
	var sawText, sawDone bool
	for _, l := range lines[2:] {
		switch l["type"] {
		case "text":
			sawText = true
			assert.Equal(t, "v1:4", l["cursor"], "a preview frame keeps the last durable cursor")
			assert.Equal(t, true, l["preview"])
		case "turn_done":
			sawDone = true
		}
	}
	assert.True(t, sawText && sawDone, "%v", lines)
	last := lines[len(lines)-1]
	assert.Equal(t, "status", last["type"])
	assert.Equal(t, "idle", last["status"])
}

func TestTail_Rejections(t *testing.T) {
	t.Run("malformed cursor → 400", func(t *testing.T) {
		rig := newConvRig(t, newFakeConversations(conv(100, convA, chat_svc.CtlStatusIdle)))
		rec := do(t, rig.h, http.MethodGet, "/ctl/v1/tail?conversationId="+convA+"&from=7", testToken, "")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("stale cursor → 410 pointing at messages", func(t *testing.T) {
		convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusIdle))
		convs.tailErr = chat_svc.ErrCtlCursorInvalid
		rig := newConvRig(t, convs)
		rec := do(t, rig.h, http.MethodGet, "/ctl/v1/tail?conversationId="+convA+"&from=v1:99", testToken, "")
		assert.Equal(t, http.StatusGone, rec.Code)
		assert.Contains(t, rec.Body.String(), "agrctl messages")
	})
	t.Run("unknown session → 404", func(t *testing.T) {
		rig := newConvRig(t, newFakeConversations())
		rec := do(t, rig.h, http.MethodGet, "/ctl/v1/tail?conversationId="+convX, testToken, "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

// ---- 其余端点也按 conversation_id 寻址 ----

func TestConversationAddressing_SessionsStreamAnswer(t *testing.T) {
	convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusIdle))
	rig := newConvRig(t, convs)

	out := decode(t, do(t, rig.h, http.MethodPost, "/ctl/v1/sessions", testToken, `{"agent":"planner"}`))
	assert.Equal(t, convA, out["conversationId"])

	rec := do(t, rig.h, http.MethodPost, "/ctl/v1/answer-permission", testToken,
		`{"conversationId":"`+convA+`","requestId":"req-1","allow":true}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, rig.chat.answerPermissionReq)
	assert.Equal(t, int64(100), rig.chat.answerPermissionReq.SessionID)

	srv := httptest.NewServer(rig.h)
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/ctl/v1/stream?conversationId="+convA, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int64(100), rig.chat.streamSessionID)

	rec = do(t, rig.h, http.MethodGet, "/ctl/v1/stream?conversationId="+convX, testToken, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.True(t, strings.Contains(rec.Body.String(), "not found"))
}

// ---- send --device / --engine:桌面端按共享派发函数校验、首轮之前把设备与引擎配置钉住 ----

// planner 的派发方案(按派发顺序):本机没装运行时、build-box 通过且被自动选中、gpu-box 离线。
func plannerPlan() *chat_svc.CtlDispatch {
	return &chat_svc.CtlDispatch{
		Found: true, EngineProfileID: 31, Chosen: "sha256:bb",
		Devices: []chat_svc.CtlDevice{
			{Name: "this desktop", Refs: []string{"this desktop", "本机", "local", "sha256:self"}, Fingerprint: "sha256:self",
				Reason: &dispatch_svc.ReasonView{Code: "runtime_missing", Runtime: "codex"}},
			{Name: "build-box", Refs: []string{"build-box", "sha256:bb"}, Fingerprint: "sha256:bb", Eligible: true, Chosen: true},
			{Name: "gpu-box", Refs: []string{"gpu-box", "sha256:gpu"}, Fingerprint: "sha256:gpu",
				Reason: &dispatch_svc.ReasonView{Code: "offline"}},
		},
	}
}

func plannerEngines() []chat_svc.CtlEngine {
	return []chat_svc.CtlEngine{{ID: 31, Name: "claude-main"}, {ID: 32, Name: "codex-main"}}
}

func TestSend_NewSessionPinsTheDispatchedDeviceAndEngine(t *testing.T) {
	cases := []struct {
		name, body  string
		wantEngine  int64
		wantDevice  devicefp.Carrier
		wantPlanned int64
	}{
		{"neither → the rule's pick", `{"agent":"planner","text":"go","effort":"high"}`, 31, "sha256:bb", 0},
		{"device by name", `{"agent":"planner","text":"go","effort":"high","device":"build-box"}`, 31, "sha256:bb", 0},
		{"device by fingerprint", `{"agent":"planner","text":"go","device":"sha256:bb"}`, 31, "sha256:bb", 0},
		{"engine by name", `{"agent":"planner","text":"go","effort":"high","engine":"codex-main"}`, 32, "sha256:bb", 32},
		{"engine by id with device", `{"agent":"planner","text":"go","engine":"32","device":"build-box"}`, 32, "sha256:bb", 32},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusIdle))
			convs.plan, convs.engines = plannerPlan(), plannerEngines()
			if c.wantEngine == 32 {
				convs.plan.EngineProfileID = 32
			}
			rig := newConvRig(t, convs)
			rec := do(t, rig.h, http.MethodPost, "/ctl/v1/send", testToken, c.body)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, pinnedDispatch{c.wantEngine, c.wantDevice}, convs.pinned[100], "pinned before the first turn")
			assert.Equal(t, []int64{c.wantPlanned}, convs.planned, "the dispatch rule is asked about the requested engine")
			assert.Equal(t, []int64{c.wantEngine}, convs.checkedEngine, "overrides are checked against the final engine profile")
			require.NotNil(t, rig.chat.lastSend)
			assert.Zero(t, rig.chat.lastSend.ExecTargetOverride, "the old exec target is no longer sent")
		})
	}
}

func TestSend_TheLocalDesktopIsADeviceUnderEveryName(t *testing.T) {
	for _, ref := range []string{"本机", "local", "this desktop", "sha256:self"} {
		t.Run(ref, func(t *testing.T) {
			convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusIdle))
			convs.plan, convs.engines = plannerPlan(), plannerEngines()
			convs.plan.Devices[0].Eligible, convs.plan.Devices[0].Reason = true, nil
			rig := newConvRig(t, convs)
			rec := do(t, rig.h, http.MethodPost, "/ctl/v1/send", testToken, `{"agent":"planner","text":"go","device":"`+ref+`"}`)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, devicefp.Carrier("sha256:self"), convs.pinned[100].device)
		})
	}
}

func TestSend_DeviceOrEngineRejections(t *testing.T) {
	cases := []struct {
		name, body string
		code       int
		msgs       []string
	}{
		{"unknown device lists the dispatchable devices", `{"agent":"planner","text":"go","device":"nowhere"}`,
			http.StatusUnprocessableEntity, []string{"--device", "this desktop", "build-box", "gpu-box"}},
		{"a device the rule rejects → its reason", `{"agent":"planner","text":"go","device":"gpu-box"}`,
			http.StatusConflict, []string{"gpu-box: offline"}},
		{"the local desktop failing the rule says why", `{"agent":"planner","text":"go","device":"local"}`,
			http.StatusConflict, []string{"this desktop: codex is not installed"}},
		{"unknown engine lists them", `{"agent":"planner","text":"go","engine":"ghost"}`,
			http.StatusUnprocessableEntity, []string{"--engine", "claude-main", "codex-main"}},
		{"device when continuing", `{"conversationId":"` + convA + `","text":"go","device":"build-box"}`,
			http.StatusBadRequest, []string{"cannot be combined"}},
		{"engine when continuing", `{"conversationId":"` + convA + `","text":"go","engine":"codex-main"}`,
			http.StatusBadRequest, []string{"cannot be combined"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusIdle))
			convs.plan, convs.engines = plannerPlan(), plannerEngines()
			rig := newConvRig(t, convs)
			rec := do(t, rig.h, http.MethodPost, "/ctl/v1/send", testToken, c.body)
			assert.Equal(t, c.code, rec.Code, rec.Body.String())
			for _, m := range c.msgs {
				assert.Contains(t, rec.Body.String(), m)
			}
			assert.Nil(t, rig.chat.lastEnsure, "a refused target creates no session")
			assert.Nil(t, rig.chat.lastSend)
			assert.Empty(t, convs.pinned)
		})
	}
}

// 没有任何设备通过(无处可跑):逐台列出原因,退出码 1(409);不建会话。
func TestSend_NoEligibleDeviceListsEveryReason(t *testing.T) {
	convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusIdle))
	convs.engines = plannerEngines()
	convs.plan = plannerPlan()
	convs.plan.Found, convs.plan.Chosen = false, ""
	convs.plan.Devices[1].Eligible, convs.plan.Devices[1].Chosen = false, false
	convs.plan.Devices[1].Reason = &dispatch_svc.ReasonView{Code: "no_project_path", Project: "agentre"}
	rig := newConvRig(t, convs)
	rec := do(t, rig.h, http.MethodPost, "/ctl/v1/send", testToken, `{"agent":"planner","text":"go"}`)
	assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	for _, m := range []string{"this desktop: codex is not installed", "build-box: no path for project agentre", "gpu-box: offline"} {
		assert.Contains(t, rec.Body.String(), m)
	}
	assert.Nil(t, rig.chat.lastEnsure)
}

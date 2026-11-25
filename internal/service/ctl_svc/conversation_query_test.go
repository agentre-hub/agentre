package ctl_svc

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

func listed(sessionID int64, id string, status chat_svc.CtlConversationStatus) *chat_svc.CtlConversation {
	c := conv(sessionID, id, status)
	c.Title = "title " + id
	c.LastMessageAt = 1_700_000_000_000
	return c
}

// Given 三条会话 When list sessions Then 稳定的 JSON 字段(id / title / agent / device / status /
// lastActivityAt);本机设备显示 "this desktop";agent 名解析成 id 后下推。
func TestConversations_ListShapesAndPushesFilters(t *testing.T) {
	remote := listed(2, convB, chat_svc.CtlStatusWaiting)
	remote.Device = "build-box"
	convs := newFakeConversations()
	convs.list = []*chat_svc.CtlConversation{listed(1, convA, chat_svc.CtlStatusIdle), remote}
	rig := newConvRig(t, convs)
	rig.h.projects = &fakeProjects{items: []ProjectInfo{{ID: 5, Name: "web"}}}

	rec := do(t, rig.h, http.MethodPost, "/ctl/v1/conversations", testToken,
		`{"agent":"planner","projectId":5,"device":"build-box","status":"waiting","limit":10}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, chat_svc.CtlListFilter{AgentID: 11, ProjectID: 5, Device: "build-box", Status: chat_svc.CtlStatusWaiting, Limit: 10}, convs.listFilter)

	out := decode(t, rec)
	items := out["sessions"].([]any)
	require.Len(t, items, 2)
	first := items[0].(map[string]any)
	assert.Equal(t, convA, first["id"])
	assert.Equal(t, "title "+convA, first["title"])
	assert.Equal(t, "planner", first["agent"])
	assert.Equal(t, "this desktop", first["device"])
	assert.Equal(t, "idle", first["status"])
	assert.Equal(t, "2023-11-14T22:13:20Z", first["lastActivityAt"])
	assert.Equal(t, "build-box", items[1].(map[string]any)["device"])
}

// 非法 status、--mine(桌面不记发起方)、未知 agent 的拒绝。
func TestConversations_ListRejections(t *testing.T) {
	rig := newConvRig(t, newFakeConversations())
	rec := do(t, rig.h, http.MethodPost, "/ctl/v1/conversations", testToken, `{"status":"sleeping"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "running, idle, waiting, failed")

	rec = do(t, rig.h, http.MethodPost, "/ctl/v1/conversations", testToken, `{"mine":true}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "--mine")

	rec = do(t, rig.h, http.MethodPost, "/ctl/v1/conversations", testToken, `{"agent":"nobody"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// Given 一条有详情的会话 When get session Then 拿得到的字段在、拿不到的(项目名、token)省略,
// 待处理项数量来自待处理源。
func TestConversation_GetOmitsWhatIsUnknown(t *testing.T) {
	convs := newFakeConversations(listed(100, convA, chat_svc.CtlStatusWaiting))
	convs.detail = &chat_svc.CtlConversationDetail{Backend: "claude-local", ModelKey: "opus", LastText: "all done"}
	convs.pending = wire.SessionPendingWaitersResult{
		ToolPermissions:  []agentruntime.PendingToolPermission{{RequestID: "p1", ToolName: "Bash"}},
		AskUserQuestions: []agentruntime.PendingAskUserQuestion{{RequestID: "q1"}},
	}
	rig := newConvRig(t, convs)

	rec := do(t, rig.h, http.MethodPost, "/ctl/v1/conversation", testToken, `{"conversationId":"`+convA+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	out := decode(t, rec)
	assert.Equal(t, convA, out["id"])
	assert.Equal(t, "claude-local", out["backend"])
	assert.Equal(t, "opus", out["model"])
	assert.Equal(t, "all done", out["lastText"])
	assert.EqualValues(t, 2, out["pending"])
	assert.Equal(t, "waiting", out["status"])
	for _, k := range []string{"project", "usage", "initiator", "createdAt"} {
		assert.NotContains(t, out, k, "unknown field %s must be omitted, not invented", k)
	}

	rec = do(t, rig.h, http.MethodPost, "/ctl/v1/conversation", testToken, `{"conversationId":"`+convX+`"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = do(t, rig.h, http.MethodPost, "/ctl/v1/conversation", testToken, `{"conversationId":"42"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// Given 一页消息 When messages Then 带 last / before / full 下推,响应带游标。
func TestMessages_PagingAndShape(t *testing.T) {
	convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusIdle))
	convs.messages = &chat_svc.CtlMessagesPage{
		Messages: []chat_svc.CtlMessage{{Seq: 3, Role: "user", Text: "hi", At: 1_700_000_000_000},
			{Seq: 4, Role: "assistant", Text: "yo", Blocks: []chat_svc.CtlMessageBlock{{Type: "text", Text: "yo"}}}},
		HasMore: true, BeforeSeq: 3,
	}
	rig := newConvRig(t, convs)

	rec := do(t, rig.h, http.MethodPost, "/ctl/v1/messages", testToken,
		`{"conversationId":"`+convA+`","last":5,"before":"9","full":true}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, chat_svc.CtlMessagesQuery{Last: 5, BeforeSeq: 9, Full: true}, convs.messagesQuery)
	assert.Equal(t, int64(100), convs.messagesSession)
	out := decode(t, rec)
	assert.Equal(t, convA, out["session"])
	assert.Equal(t, true, out["hasMore"])
	assert.Equal(t, "3", out["before"])
	msgs := out["messages"].([]any)
	require.Len(t, msgs, 2)
	assert.Equal(t, "user", msgs[0].(map[string]any)["role"])
	assert.Equal(t, "hi", msgs[0].(map[string]any)["text"])

	rec = do(t, rig.h, http.MethodPost, "/ctl/v1/messages", testToken, `{"conversationId":"`+convA+`","before":"abc"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "a non-numeric cursor is a usage error")
	rec = do(t, rig.h, http.MethodPost, "/ctl/v1/messages", testToken, `{"conversationId":"`+convX+`"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func askPending() wire.SessionPendingWaitersResult {
	return wire.SessionPendingWaitersResult{
		ToolPermissions: []agentruntime.PendingToolPermission{{RequestID: "p1", ToolName: "Bash", Input: []byte(`{"command":"ls"}`)}},
		AskUserQuestions: []agentruntime.PendingAskUserQuestion{{RequestID: "q1", Questions: []agentruntime.AskQuestion{{
			Question: "Which db?", Header: "DB",
			Options: []agentruntime.AskOption{{Label: "pg", Description: "postgres"}, {Label: "mysql"}},
		}}}},
	}
}

// Given 一条等待中的会话 When pending <session> Then question 与 tool_permission 两类,带稳定字段。
func TestPending_ForOneSession(t *testing.T) {
	convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusWaiting))
	convs.pending = askPending()
	rig := newConvRig(t, convs)

	rec := do(t, rig.h, http.MethodPost, "/ctl/v1/pending", testToken, `{"conversationId":"`+convA+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	items := decode(t, rec)["items"].([]any)
	require.Len(t, items, 2)
	byKind := map[string]map[string]any{}
	for _, it := range items {
		m := it.(map[string]any)
		byKind[m["kind"].(string)] = m
		assert.Equal(t, convA, m["session"])
	}
	assert.Equal(t, "p1", byKind["tool_permission"]["id"])
	assert.Equal(t, "Bash", byKind["tool_permission"]["tool"])
	assert.Equal(t, map[string]any{"command": "ls"}, byKind["tool_permission"]["input"])
	q := byKind["question"]["questions"].([]any)[0].(map[string]any)
	assert.Equal(t, "Which db?", q["question"])
	assert.Equal(t, "pg", q["options"].([]any)[0].(map[string]any)["label"])
}

// Given 两条会话、只有一条在等待 When pending(不给会话) Then 只列等待中的会话里的待处理项。
func TestPending_AcrossSessions(t *testing.T) {
	convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusWaiting))
	convs.list = []*chat_svc.CtlConversation{conv(100, convA, chat_svc.CtlStatusWaiting)}
	convs.pending = askPending()
	rig := newConvRig(t, convs)

	rec := do(t, rig.h, http.MethodPost, "/ctl/v1/pending", testToken, `{}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, chat_svc.CtlStatusWaiting, convs.listFilter.Status)
	assert.Len(t, decode(t, rec)["items"], 2)
}

// Given 提问卡 When answer --option(序号或文本)/ --text Then 按问题的选项映射成答案并投回。
func TestAnswer_Question(t *testing.T) {
	post := func(rig *convRig, body string) *httptest.ResponseRecorder {
		return do(t, rig.h, http.MethodPost, "/ctl/v1/answer", testToken, body)
	}
	newRig := func() *convRig {
		convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusWaiting))
		convs.pending = askPending()
		return newConvRig(t, convs)
	}

	rig := newRig()
	rec := post(rig, `{"conversationId":"`+convA+`","requestId":"q1","options":["2"]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := rig.convs.answeredQuestion
	require.NotNil(t, got)
	assert.Equal(t, int64(100), got.sessionID)
	assert.Equal(t, "q1", got.requestID)
	require.Len(t, got.answers, 1)
	assert.Equal(t, []string{"mysql"}, got.answers[0].Labels, "a number picks the option at that 1-based position")

	rig = newRig()
	require.Equal(t, http.StatusOK, post(rig, `{"conversationId":"`+convA+`","requestId":"q1","options":["pg"]}`).Code)
	assert.Equal(t, []string{"pg"}, rig.convs.answeredQuestion.answers[0].Labels)

	rig = newRig()
	require.Equal(t, http.StatusOK, post(rig, `{"conversationId":"`+convA+`","requestId":"q1","text":"sqlite"}`).Code)
	assert.Equal(t, []string{agentruntime.OtherAnswerLabel}, rig.convs.answeredQuestion.answers[0].Labels)
	assert.Equal(t, "sqlite", rig.convs.answeredQuestion.answers[0].OtherText)

	for name, body := range map[string]string{
		"unknown option":      `{"conversationId":"` + convA + `","requestId":"q1","options":["oracle"]}`,
		"nothing given":       `{"conversationId":"` + convA + `","requestId":"q1"}`,
		"both option + text":  `{"conversationId":"` + convA + `","requestId":"q1","options":["pg"],"text":"x"}`,
		"allow on a question": `{"conversationId":"` + convA + `","requestId":"q1","decision":"allow"}`,
		"single-select twice": `{"conversationId":"` + convA + `","requestId":"q1","options":["pg","mysql"]}`,
	} {
		rig = newRig()
		rec := post(rig, body)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "%s: %s", name, rec.Body.String())
		assert.Nil(t, rig.convs.answeredQuestion, name)
	}
}

// Given 工具权限 When answer --allow / --deny --message Then 走与 acp 同一个权限回答;
// 对权限给 --option、没给决定都是用法错误。
func TestAnswer_ToolPermission(t *testing.T) {
	convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusWaiting))
	convs.pending = askPending()
	rig := newConvRig(t, convs)

	rec := do(t, rig.h, http.MethodPost, "/ctl/v1/answer", testToken,
		`{"conversationId":"`+convA+`","requestId":"p1","decision":"deny","message":"no thanks"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	req := rig.chat.answerPermissionReq
	require.NotNil(t, req)
	assert.Equal(t, "p1", req.RequestID)
	assert.False(t, req.Allow)
	assert.Equal(t, "no thanks", req.DenyReason)

	rec = do(t, rig.h, http.MethodPost, "/ctl/v1/answer", testToken, `{"conversationId":"`+convA+`","requestId":"p1","decision":"allow"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, rig.chat.answerPermissionReq.Allow)

	for name, body := range map[string]string{
		"option on a permission": `{"conversationId":"` + convA + `","requestId":"p1","options":["1"]}`,
		"no decision":            `{"conversationId":"` + convA + `","requestId":"p1"}`,
		"bad decision":           `{"conversationId":"` + convA + `","requestId":"p1","decision":"maybe"}`,
	} {
		rig.chat.answerPermissionReq = nil
		rec := do(t, rig.h, http.MethodPost, "/ctl/v1/answer", testToken, body)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, name)
		assert.Nil(t, rig.chat.answerPermissionReq, name)
	}
}

// Given 待处理项已被别处处理 When answer Then 404「该项已不存在」(exit 1),什么都不投;
// 看不到的会话 404、畸形 id 400。
func TestAnswer_GoneItemAndAddressing(t *testing.T) {
	convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusIdle))
	rig := newConvRig(t, convs)
	rec := do(t, rig.h, http.MethodPost, "/ctl/v1/answer", testToken, `{"conversationId":"`+convA+`","requestId":"gone","decision":"allow"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "no longer exists")
	assert.Nil(t, rig.chat.answerPermissionReq)
	assert.Nil(t, convs.answeredQuestion)

	rec = do(t, rig.h, http.MethodPost, "/ctl/v1/answer", testToken, `{"conversationId":"`+convX+`","requestId":"p1","decision":"allow"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = do(t, rig.h, http.MethodPost, "/ctl/v1/answer", testToken, `{"conversationId":"nope","requestId":"p1","decision":"allow"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// 会话级 token:读(列表 / 详情 / 消息 / 待处理)可以,answer 不行;握手 token 可以答。
func TestSessionToken_ReadsButCannotAnswer(t *testing.T) {
	convs := newFakeConversations(conv(100, convA, chat_svc.CtlStatusWaiting), conv(42, convB, chat_svc.CtlStatusIdle))
	convs.pending = askPending()
	convs.messages = &chat_svc.CtlMessagesPage{}
	convs.detail = &chat_svc.CtlConversationDetail{}
	rig := newConvRig(t, convs)
	tok := rig.signer.MintToken(7, 42)

	for path, body := range map[string]string{
		"conversations": `{}`,
		"conversation":  `{"conversationId":"` + convA + `"}`,
		"messages":      `{"conversationId":"` + convA + `"}`,
		"pending":       `{"conversationId":"` + convA + `"}`,
	} {
		rec := do(t, rig.h, http.MethodPost, "/ctl/v1/"+path, tok, body)
		assert.Equal(t, http.StatusOK, rec.Code, "%s: %s", path, rec.Body.String())
	}
	rec := do(t, rig.h, http.MethodPost, "/ctl/v1/answer", tok, `{"conversationId":"`+convA+`","requestId":"p1","decision":"allow"}`)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Nil(t, rig.chat.answerPermissionReq)
	rec = do(t, rig.h, http.MethodPost, "/ctl/v1/answer", testToken, `{"conversationId":"`+convA+`","requestId":"p1","decision":"allow"}`)
	assert.Equal(t, http.StatusOK, rec.Code, "the desktop handshake token (human or external) can answer")
}

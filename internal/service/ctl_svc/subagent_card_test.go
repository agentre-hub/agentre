package ctl_svc

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/cago-frame/cago/pkg/logger"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// subagent_card_test.go 钉住 serveSend 与子代理卡端口(SubagentCards)之间的契约
// (spec「Binding」):只有会话 token + --wait + 新建子会话才登记并绑定,登记先于起轮,
// 绑定以起轮成功为准,收尾先于响应写出;其余情况一律不碰端口并记 info 日志;send 的
// 请求、响应、状态码与「断开即停止子会话」都与今天逐字相同。

// fakeCards 记下 serveSend 对端口的每一次调用。
type fakeCards struct {
	mu      sync.Mutex
	watched []int64
	watch   *fakeCardWatch
	// sentBeforeWatch 登记那一刻 chat.Send 是否已经被调过(必须为 false:登记先于起轮)。
	chat            *fakeChat
	sentBeforeWatch bool
}

func (f *fakeCards) WatchCtlSend(childSessionID int64) chat_svc.CtlSendWatch {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.watched = append(f.watched, childSessionID)
	if f.chat != nil && f.chat.lastSend != nil {
		f.sentBeforeWatch = true
	}
	f.watch = &fakeCardWatch{}
	return f.watch
}

type fakeCardWatch struct {
	binds    []chat_svc.CtlSendBindRequest
	finishes []chat_svc.CtlSendOutcome
	closed   bool
	// rec 在响应头写出那一刻才被挂上:Finish 时它已非 nil,说明收尾晚于响应。
	rec                 *httptest.ResponseRecorder
	finishedAfterHeader bool
}

func (w *fakeCardWatch) Bind(_ context.Context, req chat_svc.CtlSendBindRequest) bool {
	w.binds = append(w.binds, req)
	return true
}

func (w *fakeCardWatch) Finish(_ context.Context, out chat_svc.CtlSendOutcome) {
	if w.rec != nil {
		w.finishedAfterHeader = true
	}
	w.finishes = append(w.finishes, out)
}

func (w *fakeCardWatch) Close() { w.closed = true }

// observeLogs 把全局 logger 换成可观测的 core,用例结束还原。
func observeLogs(t *testing.T) *observer.ObservedLogs {
	core, logs := observer.New(zapcore.InfoLevel)
	old := logger.Default()
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { logger.SetLogger(old) })
	return logs
}

func notBoundReasons(logs *observer.ObservedLogs) []string {
	entries := logs.FilterMessage("ctl_svc.serveSend: subagent card not bound").All()
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ContextMap()["reason"].(string))
	}
	return out
}

type sendRig struct {
	h      *ctlHandler
	chat   *fakeChat
	cards  *fakeCards
	signer *agenttool.TokenSigner
	turnCh chan chat_svc.TurnResult
}

func newSendRig() *sendRig {
	turnCh := make(chan chat_svc.TurnResult, 1)
	chat := &fakeChat{
		ensured:   &chat_svc.EnsureSessionResponse{SessionID: 100, Created: true},
		sendResp:  &chat_svc.SendResponse{SessionID: 100, AssistantMessageID: 200, Stream: "chat:100:200"},
		turnCh:    turnCh,
		finalText: "the answer",
	}
	cards := &fakeCards{chat: chat}
	signer := agenttool.NewTokenSigner()
	agents := &fakeAgents{
		byName: map[string]*agent_entity.Agent{"reviewer": {ID: 9, Name: "reviewer"}},
		byID:   map[int64]*agent_entity.Agent{9: {ID: 9, Name: "reviewer"}},
	}
	return &sendRig{
		h: &ctlHandler{
			token: testToken, sessions: signer, agents: agents, projects: &fakeProjects{},
			chat: chat, cards: cards,
		},
		chat: chat, cards: cards, signer: signer, turnCh: turnCh,
	}
}

func (r *sendRig) sessionToken() string { return r.signer.MintToken(7, 42) }

// post 发一条 send,并让 Finish 能看到响应是否已写出。
func (r *sendRig) post(token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/ctl/v1/send", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	r.cards.mu.Lock()
	r.cards.watch = nil
	r.cards.mu.Unlock()
	hooked := &recHook{rec: rec, cards: r.cards}
	r.h.ServeHTTP(hooked, req)
	return rec
}

// recHook 在第一次写响应前把 recorder 交给 fake watch,供 Finish 判断先后。
type recHook struct {
	rec   *httptest.ResponseRecorder
	cards *fakeCards
}

func (h *recHook) Header() http.Header { return h.rec.Header() }
func (h *recHook) Write(b []byte) (int, error) {
	return h.rec.Write(b)
}
func (h *recHook) WriteHeader(code int) {
	h.cards.mu.Lock()
	if h.cards.watch != nil {
		h.cards.watch.rec = h.rec
	}
	h.cards.mu.Unlock()
	h.rec.WriteHeader(code)
}

func TestServeSend_SubagentCard(t *testing.T) {
	Convey("serveSend 与子代理卡端口", t, func() {
		logs := observeLogs(t)

		Convey("Given 会话 token + --wait 按名字派发, When 子轮正常结束, Then 起轮前登记一次、起轮后绑定恰好一次、收尾先于响应、响应与握手 token 逐字相同", func() {
			rig := newSendRig()
			rig.turnCh <- chat_svc.TurnResult{SessionID: 100, AssistantMessageID: 200}
			rec := rig.post(rig.sessionToken(), `{"agent":"reviewer","text":"审一下 PR 42","wait":true}`)

			So(rec.Code, ShouldEqual, http.StatusOK)
			So(rig.cards.watched, ShouldResemble, []int64{100})
			So(rig.cards.sentBeforeWatch, ShouldBeFalse)
			w := rig.cards.watch
			So(w.binds, ShouldResemble, []chat_svc.CtlSendBindRequest{{
				CallerSessionID: 42, AgentName: "reviewer", Task: "审一下 PR 42",
			}})
			So(w.finishes, ShouldResemble, []chat_svc.CtlSendOutcome{{
				Status: chat_svc.CtlSendCompleted, FinalText: "the answer", AssistantMessageID: 200,
			}})
			So(w.finishedAfterHeader, ShouldBeFalse)
			So(w.closed, ShouldBeTrue)

			baseline := newSendRig()
			baseline.turnCh <- chat_svc.TurnResult{SessionID: 100, AssistantMessageID: 200}
			want := baseline.post(testToken, `{"agent":"reviewer","text":"审一下 PR 42","wait":true}`)
			So(rec.Body.String(), ShouldEqual, want.Body.String())
		})

		Convey("Given 按 --agent-id 派发且 --isolated, Then 绑定请求带 id、解析出的名字与 isolated", func() {
			rig := newSendRig()
			rig.turnCh <- chat_svc.TurnResult{SessionID: 100, AssistantMessageID: 200}
			rec := rig.post(rig.sessionToken(), `{"agentId":9,"text":"跑测试","wait":true,"isolated":true}`)

			So(rec.Code, ShouldEqual, http.StatusOK)
			So(rig.cards.watch.binds, ShouldResemble, []chat_svc.CtlSendBindRequest{{
				CallerSessionID: 42, AgentName: "reviewer", AgentID: 9, Task: "跑测试", Isolated: true,
			}})
		})

		Convey("Given 子轮以错误收场, Then 卡片收成失败(带原因)且先于响应,响应仍是今天的 500", func() {
			rig := newSendRig()
			rig.turnCh <- chat_svc.TurnResult{SessionID: 100, AssistantMessageID: 200, Err: errors.New("boom")}
			rec := rig.post(rig.sessionToken(), `{"agent":"reviewer","text":"x","wait":true}`)

			So(rec.Code, ShouldEqual, http.StatusInternalServerError)
			So(rec.Body.String(), ShouldContainSubstring, "turn error: boom")
			w := rig.cards.watch
			So(w.finishes, ShouldResemble, []chat_svc.CtlSendOutcome{{
				Status: chat_svc.CtlSendFailed, FinalText: "boom", AssistantMessageID: 200,
			}})
			So(w.finishedAfterHeader, ShouldBeFalse)
		})

		Convey("Given 子轮被中止(无错误), Then 卡片收成已取消,响应仍带最终文本", func() {
			rig := newSendRig()
			rig.turnCh <- chat_svc.TurnResult{SessionID: 100, AssistantMessageID: 200, Aborted: true}
			rec := rig.post(rig.sessionToken(), `{"agent":"reviewer","text":"x","wait":true}`)

			So(rec.Code, ShouldEqual, http.StatusOK)
			So(rig.cards.watch.finishes, ShouldResemble, []chat_svc.CtlSendOutcome{{
				Status: chat_svc.CtlSendCanceled, FinalText: "the answer", AssistantMessageID: 200,
			}})
		})

		Convey("Given agrctl 在等待时断开, Then 子会话照旧被停止,卡片收成已取消,响应仍是 408", func() {
			rig := newSendRig()
			ctx, cancel := context.WithCancel(context.Background())
			req := httptest.NewRequest(http.MethodPost, "/ctl/v1/send",
				bytes.NewReader([]byte(`{"agent":"reviewer","text":"x","wait":true}`))).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer "+rig.sessionToken())
			rec := httptest.NewRecorder()
			cancel()
			rig.h.ServeHTTP(rec, req)

			So(rec.Code, ShouldEqual, http.StatusRequestTimeout)
			So(rig.chat.stopped, ShouldBeTrue)
			So(rig.chat.lastStop.SessionID, ShouldEqual, 100)
			So(rig.cards.watch.finishes, ShouldResemble, []chat_svc.CtlSendOutcome{{Status: chat_svc.CtlSendCanceled}})
			So(rig.cards.watch.closed, ShouldBeTrue)
		})

		Convey("Given send 起轮失败, Then 登记被释放、从不绑定,响应仍是今天的 500", func() {
			rig := newSendRig()
			rig.chat.sendErr = errors.New("no backend")
			rec := rig.post(rig.sessionToken(), `{"agent":"reviewer","text":"x","wait":true}`)

			So(rec.Code, ShouldEqual, http.StatusInternalServerError)
			So(rec.Body.String(), ShouldContainSubstring, "start turn failed")
			So(rig.cards.watch.binds, ShouldBeEmpty)
			So(rig.cards.watch.finishes, ShouldBeEmpty)
			So(rig.cards.watch.closed, ShouldBeTrue)
		})

		Convey("Given 目标 agent 不存在, Then 不登记不绑定,响应仍是 404", func() {
			rig := newSendRig()
			rec := rig.post(rig.sessionToken(), `{"agent":"ghost","text":"x","wait":true}`)
			So(rec.Code, ShouldEqual, http.StatusNotFound)
			So(rig.cards.watched, ShouldBeEmpty)
		})

		Convey("Given 握手 token + --wait, Then 不碰端口,记 info 日志 handshake_token,响应照旧", func() {
			rig := newSendRig()
			rig.turnCh <- chat_svc.TurnResult{SessionID: 100, AssistantMessageID: 200}
			rec := rig.post(testToken, `{"agent":"reviewer","text":"x","wait":true}`)

			So(rec.Code, ShouldEqual, http.StatusOK)
			So(rig.cards.watched, ShouldBeEmpty)
			So(notBoundReasons(logs), ShouldResemble, []string{"handshake_token"})
		})

		Convey("Given 会话 token 但异步 send(无 --wait), Then 不碰端口,记 info 日志 no_wait,响应照旧", func() {
			rig := newSendRig()
			rec := rig.post(rig.sessionToken(), `{"agent":"reviewer","text":"x"}`)

			So(rec.Code, ShouldEqual, http.StatusOK)
			So(rec.Body.String(), ShouldContainSubstring, `"done":false`)
			So(rig.cards.watched, ShouldBeEmpty)
			So(notBoundReasons(logs), ShouldResemble, []string{"no_wait"})
		})

		Convey("Given 会话 token + --wait 但续写既有会话, Then 不碰端口,记 info 日志 existing_session", func() {
			rig := newSendRig()
			rig.turnCh <- chat_svc.TurnResult{SessionID: 100, AssistantMessageID: 200}
			rec := rig.post(rig.sessionToken(), `{"sessionId":100,"text":"x","wait":true}`)

			So(rec.Code, ShouldEqual, http.StatusOK)
			So(rig.cards.watched, ShouldBeEmpty)
			So(notBoundReasons(logs), ShouldResemble, []string{"existing_session"})
		})

		Convey("Given 端口未接线, Then send 照旧完成", func() {
			rig := newSendRig()
			rig.h.cards = nil
			rig.turnCh <- chat_svc.TurnResult{SessionID: 100, AssistantMessageID: 200}
			rec := rig.post(rig.sessionToken(), `{"agent":"reviewer","text":"x","wait":true}`)
			So(rec.Code, ShouldEqual, http.StatusOK)
		})
	})
}

// 回归:ControlHandler 每次请求现读端口,后接线的 RegisterSubagentCards 必须生效。
func TestControlHandler_SeesSubagentCardsRegisteredAfterMount(t *testing.T) {
	Convey("Given handler 先挂上、端口后接线, When 会话 token 发 send --wait, Then 用的是后接线的端口", t, func() {
		svc := newCtlSvc()
		svc.token = testToken
		mounted := svc.ControlHandler()
		rig := newSendRig()
		svc.RegisterDeps(rig.h.agents, &fakeProjects{}, rig.chat)
		svc.RegisterSubagentCards(rig.cards)
		rig.turnCh <- chat_svc.TurnResult{SessionID: 100, AssistantMessageID: 200}

		rec := do(t, mounted, http.MethodPost, "/ctl/v1/send", svc.SessionToken(7, 42),
			`{"agent":"reviewer","text":"x","wait":true}`)
		So(rec.Code, ShouldEqual, http.StatusOK)
		So(rig.cards.watched, ShouldResemble, []int64{100})
	})
}

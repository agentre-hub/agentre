package ctl_svc

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/mock_session_dispatch"
)

// dispatch_test.go 钉住 serveSend 与派发关系登记(chat_svc.DispatchRecorder)的契约
// (spec 2026-11-24 决策 21):会话 token 发出的每次 send(带不带 --wait、隔离与否)都在起轮
// 之前登记一行,起轮成功转运行中,被派发方那一轮收尾时落终态;握手 token 没有派发方,不登记。

const dispatchID = int64(77)

func newDispatchSendRig(t *testing.T) (*sendRig, *mock_session_dispatch.MockDispatchRecorder) {
	t.Helper()
	ctrl := gomock.NewController(t)
	rec := mock_session_dispatch.NewMockDispatchRecorder(ctrl)
	rig := newSendRig()
	rig.h.dispatches = rec
	return rig, rec
}

// done 收到时说明 Finish 已被调用(不带 --wait 的收尾在后台 goroutine 里)。
func finishSignal(done chan<- struct{}) func(context.Context, int64, chat_svc.DispatchOutcome) {
	return func(context.Context, int64, chat_svc.DispatchOutcome) { done <- struct{}{} }
}

func awaitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("dispatch Finish was never called")
	}
}

// Given 会话 token 发 send 不带 --wait, When 起轮成功, Then 登记先于起轮、起轮后转运行中,
// 响应不等收尾;子轮收尾后后台把行收成已完成并带上回复消息。
func TestServeSend_NoWaitRecordsAndTracksDispatch(t *testing.T) {
	rig, rec := newDispatchSendRig(t)
	done := make(chan struct{}, 1)
	var order []string
	rec.EXPECT().Begin(gomock.Any(), chat_svc.DispatchBegin{
		DispatcherSessionID: 42, DispatchedSessionID: 100,
		Method: chat_svc.DispatchMethodAgrctlSend, Task: "写文档", Isolated: false,
	}).DoAndReturn(func(context.Context, chat_svc.DispatchBegin) int64 {
		assert.Nil(t, rig.chat.lastSend, "登记必须先于起轮")
		order = append(order, "begin")
		return dispatchID
	})
	rec.EXPECT().Started(gomock.Any(), dispatchID).Do(func(context.Context, int64) {
		assert.NotNil(t, rig.chat.lastSend, "运行中必须在起轮成功之后")
		order = append(order, "started")
	})
	rec.EXPECT().Finish(gomock.Any(), dispatchID, chat_svc.DispatchOutcome{
		Status: chat_svc.CtlSendCompleted, ReplyMessageID: 200,
	}).Do(finishSignal(done))

	rig.turnCh <- chat_svc.TurnResult{SessionID: 100, AssistantMessageID: 200}
	resp := rig.post(rig.sessionToken(), `{"agent":"reviewer","text":"写文档"}`)

	assert.Equal(t, http.StatusOK, resp.Code)
	awaitDone(t, done)
	assert.Equal(t, []string{"begin", "started"}, order)
}

// 不带 --wait 的子轮失败 / 被中止,行分别收成失败 / 已取消。
func TestServeSend_NoWaitTracksFailedAndCanceledTurns(t *testing.T) {
	cases := map[string]struct {
		res  chat_svc.TurnResult
		want chat_svc.DispatchOutcome
	}{
		"failed": {
			chat_svc.TurnResult{SessionID: 100, AssistantMessageID: 200, Err: errors.New("boom")},
			chat_svc.DispatchOutcome{Status: chat_svc.CtlSendFailed, ReplyMessageID: 200},
		},
		"canceled": {
			chat_svc.TurnResult{SessionID: 100, AssistantMessageID: 200, Aborted: true},
			chat_svc.DispatchOutcome{Status: chat_svc.CtlSendCanceled, ReplyMessageID: 200},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rig, rec := newDispatchSendRig(t)
			done := make(chan struct{}, 1)
			rec.EXPECT().Begin(gomock.Any(), gomock.Any()).Return(dispatchID)
			rec.EXPECT().Started(gomock.Any(), dispatchID)
			rec.EXPECT().Finish(gomock.Any(), dispatchID, tc.want).Do(finishSignal(done))

			rig.turnCh <- tc.res
			resp := rig.post(rig.sessionToken(), `{"agent":"reviewer","text":"x"}`)

			assert.Equal(t, http.StatusOK, resp.Code)
			awaitDone(t, done)
		})
	}
}

// --wait:响应写出之前行已收成终态(与子代理卡同一时机、同一结果)。
func TestServeSend_WaitFinishesDispatchBeforeResponse(t *testing.T) {
	rig, rec := newDispatchSendRig(t)
	rec.EXPECT().Begin(gomock.Any(), chat_svc.DispatchBegin{
		DispatcherSessionID: 42, DispatchedSessionID: 100,
		Method: chat_svc.DispatchMethodAgrctlSend, Task: "审一下", Isolated: false,
	}).Return(dispatchID)
	rec.EXPECT().Started(gomock.Any(), dispatchID)
	finished := false
	rec.EXPECT().Finish(gomock.Any(), dispatchID, chat_svc.DispatchOutcome{
		Status: chat_svc.CtlSendCompleted, ReplyMessageID: 200,
	}).Do(func(context.Context, int64, chat_svc.DispatchOutcome) { finished = true })

	rig.turnCh <- chat_svc.TurnResult{SessionID: 100, AssistantMessageID: 200}
	resp := rig.post(rig.sessionToken(), `{"agent":"reviewer","text":"审一下","wait":true}`)

	assert.Equal(t, http.StatusOK, resp.Code)
	assert.True(t, finished, "Finish 必须在 post 返回(响应写出)之前已被调用")
}

// --isolated 登记 isolated=true;--wait 的失败与断开分别收成失败 / 已取消。
func TestServeSend_IsolatedWaitRecordsIsolatedAndFailureOutcome(t *testing.T) {
	rig, rec := newDispatchSendRig(t)
	rec.EXPECT().Begin(gomock.Any(), chat_svc.DispatchBegin{
		DispatcherSessionID: 42, DispatchedSessionID: 100,
		Method: chat_svc.DispatchMethodAgrctlSend, Task: "跑测试", Isolated: true,
	}).Return(dispatchID)
	rec.EXPECT().Started(gomock.Any(), dispatchID)
	rec.EXPECT().Finish(gomock.Any(), dispatchID, chat_svc.DispatchOutcome{
		Status: chat_svc.CtlSendFailed, ReplyMessageID: 200,
	})

	rig.turnCh <- chat_svc.TurnResult{SessionID: 100, AssistantMessageID: 200, Err: errors.New("boom")}
	resp := rig.post(rig.sessionToken(), `{"agent":"reviewer","text":"跑测试","wait":true,"isolated":true}`)

	assert.Equal(t, http.StatusInternalServerError, resp.Code)
}

func TestServeSend_WaitDisconnectFinishesDispatchCanceled(t *testing.T) {
	rig, rec := newDispatchSendRig(t)
	rec.EXPECT().Begin(gomock.Any(), gomock.Any()).Return(dispatchID)
	rec.EXPECT().Started(gomock.Any(), dispatchID)
	rec.EXPECT().Finish(gomock.Any(), dispatchID, chat_svc.DispatchOutcome{Status: chat_svc.CtlSendCanceled})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/ctl/v1/send",
		bytes.NewReader([]byte(`{"agent":"reviewer","text":"x","wait":true}`))).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+rig.sessionToken())
	resp := httptest.NewRecorder()
	rig.h.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusRequestTimeout, resp.Code)
}

// 起轮失败:行不能停在「等待」,收成失败;响应仍是今天的 500。
func TestServeSend_StartTurnFailureFinishesDispatchFailed(t *testing.T) {
	rig, rec := newDispatchSendRig(t)
	rig.chat.sendErr = errors.New("no backend")
	rec.EXPECT().Begin(gomock.Any(), gomock.Any()).Return(dispatchID)
	rec.EXPECT().Finish(gomock.Any(), dispatchID, chat_svc.DispatchOutcome{Status: chat_svc.CtlSendFailed})

	resp := rig.post(rig.sessionToken(), `{"agent":"reviewer","text":"x"}`)

	assert.Equal(t, http.StatusInternalServerError, resp.Code)
}

// 续写既有会话(sessionId>0)同样登记:被派发方就是那个会话。
func TestServeSend_ExistingSessionIsRecorded(t *testing.T) {
	rig, rec := newDispatchSendRig(t)
	done := make(chan struct{}, 1)
	rec.EXPECT().Begin(gomock.Any(), chat_svc.DispatchBegin{
		DispatcherSessionID: 42, DispatchedSessionID: 555,
		Method: chat_svc.DispatchMethodAgrctlSend, Task: "继续",
	}).Return(dispatchID)
	rec.EXPECT().Started(gomock.Any(), dispatchID)
	rec.EXPECT().Finish(gomock.Any(), dispatchID, gomock.Any()).Do(finishSignal(done))

	rig.turnCh <- chat_svc.TurnResult{SessionID: 555, AssistantMessageID: 9}
	resp := rig.post(rig.sessionToken(), `{"sessionId":555,"text":"继续"}`)

	assert.Equal(t, http.StatusOK, resp.Code)
	awaitDone(t, done)
}

// 握手 token(人 / 外部脚本)没有派发方会话:不登记,send 照旧。
func TestServeSend_HandshakeTokenIsNotRecorded(t *testing.T) {
	rig, _ := newDispatchSendRig(t) // 没有任何 EXPECT:调用即失败

	resp := rig.post(testToken, `{"agent":"reviewer","text":"x"}`)

	assert.Equal(t, http.StatusOK, resp.Code)
}

// 没装登记端口时 send 与今天逐字相同(不 panic、不登记)。
func TestServeSend_WithoutRecorderBehavesAsBefore(t *testing.T) {
	rig := newSendRig()

	resp := rig.post(rig.sessionToken(), `{"agent":"reviewer","text":"x"}`)

	assert.Equal(t, http.StatusOK, resp.Code)
}

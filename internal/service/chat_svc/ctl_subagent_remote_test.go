package chat_svc

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/agents/provider"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap/zaptest/observer"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/ctlbind"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
)

// ctl_subagent_remote_test.go 钉住远端父会话的子代理卡(spec 2026-10-27「agrctl send 子代理卡」
// Remote sessions、Design 12):父会话在 agentred 上执行时,绑定由 agentred 在它的父转录里完成,
// 桌面端只采信它附来的 shell 调用 ID,不在本地匹配、不往本地累加器里写;子会话的事件(overlay
// 起始、进度、嵌套步骤、带最终文本的收尾)经注入 RPC 成批交给 agentred,嵌套步骤按 ID 去重,
// 子会话的事件循环从不等这条 RPC。断线时子会话不受影响,重连后卡片的终态与最终文本照样送达,
// 断线期间的步骤可以缺,桌面端记一条 warn。

const remoteHostCall = "toolu_sh-remote"

// fakeInjector 记下每一批注入;down 时整批失败(模拟 agentred 断线)。
type fakeInjector struct {
	mu      sync.Mutex
	batches [][]agentruntime.Event
	session []int64
	calls   []string
	down    bool
	block   chan struct{} // 非 nil 时每次注入先等它关上
}

func (f *fakeInjector) InjectSubagentEvents(_ context.Context, sessionID int64, toolCallID string, events []agentruntime.Event) error {
	if b := f.block; b != nil {
		<-b
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return errors.New("daemon disconnected")
	}
	f.batches = append(f.batches, append([]agentruntime.Event(nil), events...))
	f.session = append(f.session, sessionID)
	f.calls = append(f.calls, toolCallID)
	return nil
}

func (f *fakeInjector) setDown(down bool) {
	f.mu.Lock()
	f.down = down
	f.mu.Unlock()
}

func (f *fakeInjector) all() []agentruntime.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []agentruntime.Event
	for _, b := range f.batches {
		out = append(out, b...)
	}
	return out
}

func countEvents[T agentruntime.Event](evs []agentruntime.Event) []T {
	var out []T
	for _, ev := range evs {
		if e, ok := ev.(T); ok {
			out = append(out, e)
		}
	}
	return out
}

// remoteParent 让调用方会话此刻有一只「在 agentred 上执行」的父轮(本机只回放持久帧);它的
// 累加器里放一条本可以在本地匹配上的 shell 调用 —— 用来证明桌面端没在本地匹配。
func remoteParent(t *testing.T, svc *chatSvc) *turnRun {
	t.Helper()
	acc := turn.New()
	if err := svc.dispatcher.Apply(context.Background(), bashCall("sh-local", reviewCommand, false), acc, discardTurnEmitter{}, &turn.TurnContext{}); err != nil {
		t.Fatal(err)
	}
	parent := &turnRun{
		svc: svc, sess: &chat_entity.Session{ID: approvalSessionID}, acc: acc,
		previews: make(chan agentruntime.Event),
	}
	svc.activeTurns.Store(approvalSessionID, parent)
	return parent
}

type discardTurnEmitter struct{}

func (discardTurnEmitter) Emit(context.Context, string, any) {}

// localOverlays 父轮本地累加器里的 overlay(远端父轮上必须始终为空)。
func localOverlays(parent *turnRun) int {
	parent.mu.Lock()
	defer parent.mu.Unlock()
	return len(viewOf(parent.acc.Snapshot()).overlays)
}

// remoteWatch 登记子会话,并把它的注入端口换成 inj(resolveErr 非 nil 时解析失败)。
func remoteWatch(svc *chatSvc, inj *fakeInjector, resolved *int) *ctlSendWatch {
	w := svc.WatchCtlSend(ctlChildSessionID).(*ctlSendWatch)
	w.remote = ctlSendRemotePort{
		resolve: func(context.Context, int64) (ctlSubagentInjector, error) {
			if resolved != nil {
				*resolved++
			}
			inj.mu.Lock()
			down := inj.down
			inj.mu.Unlock()
			if down {
				return nil, errors.New("dial agentred: connection refused")
			}
			return inj, nil
		},
		backoff: 5 * time.Millisecond,
		window:  5 * time.Second,
	}
	return w
}

func remoteBind() CtlSendBindRequest {
	req := reviewBind()
	req.HostToolCallID = remoteHostCall
	req.Isolated = true
	return req
}

func remoteWarns(logs *observer.ObservedLogs) []observer.LoggedEntry {
	return logs.FilterMessage("chat_svc.ctlSendRemote: injecting into the remote parent failed; steps until it recovers may be missing").All()
}

// retrySettled 等 Finish 交出的后台重试送达并退出(它退出前最后一件事就是记这条日志):
// 用例结束前不留在跑的 goroutine。
func retrySettled(logs *observer.ObservedLogs) bool {
	return eventually(func() bool {
		return len(logs.FilterMessage("chat_svc.ctlSendRemote.retry: subagent card settled after reconnect").All()) == 1
	})
}

func TestCtlSend_GivenRemoteParent_WhenChildRuns_ThenEventsArePushedToAgentredUnderTheHostBinding(t *testing.T) {
	Convey("Given 父会话在 agentred 上执行, agentred 附来了它绑定的 shell 调用, 子会话先吐了一步才绑定", t, func() {
		deps := setupPeerSessionTest(t)
		ctx := context.Background()
		parent := remoteParent(t, deps.svc)
		inj := &fakeInjector{}
		w := remoteWatch(deps.svc, inj, nil)
		defer w.Close()
		deps.svc.mirrorCtlSendChild(ctlChildSessionID, "claude-sonnet-4", agentruntime.ToolCall{ID: "c-1", Name: "Read", Input: json.RawMessage(`{"file_path":"a.go"}`)})

		Convey("When 绑定, Then 采信那条调用、本地累加器不动,overlay 起始帧与起轮前那一步一起推给 agentred", func() {
			So(w.Bind(ctx, remoteBind()), ShouldBeTrue)

			So(eventually(func() bool { return len(countEvents[agentruntime.ToolCall](inj.all())) == 1 }), ShouldBeTrue)
			So(localOverlays(parent), ShouldEqual, 0)

			all := inj.all()
			started := countEvents[agentruntime.SubagentStarted](all)
			So(started, ShouldHaveLength, 1)
			So(started[0].ToolCallID, ShouldEqual, remoteHostCall)
			So(started[0].Info.Origin, ShouldEqual, agentruntime.SubagentOriginAgrctl)
			So(started[0].Info.Kind, ShouldEqual, agentruntime.SubagentKindAgrctl)
			So(started[0].Info.ChildSessionID, ShouldEqual, ctlChildSessionID)
			So(started[0].Info.Isolated, ShouldBeTrue)
			So(started[0].Info.SubagentType, ShouldEqual, "reviewer")
			So(started[0].Info.TaskDescription, ShouldEqual, "审一下 PR 42")
			So(started[0].Info.Status, ShouldEqual, "running")
			So(all[0], ShouldHaveSameTypeAs, agentruntime.SubagentStarted{})
			nested := countEvents[agentruntime.ToolCall](all)[0]
			So(nested.ID, ShouldEqual, ctlbind.NestedToolCallID(remoteHostCall, "c-1"))
			So(nested.ParentToolCallID, ShouldEqual, remoteHostCall)
			inj.mu.Lock()
			So(inj.session[0], ShouldEqual, approvalSessionID)
			So(inj.calls[0], ShouldEqual, remoteHostCall)
			inj.mu.Unlock()

			Convey("When 子会话继续(重复投递、结果、用量、换了模型), Then 嵌套步骤各只推一份,进度跟上,模型只推先到的那个;Finish 返回时带最终文本的收尾已送达", func() {
				deps.svc.mirrorCtlSendChild(ctlChildSessionID, "claude-opus-4", agentruntime.ToolCall{ID: "c-1", Name: "Read"})
				deps.svc.mirrorCtlSendChild(ctlChildSessionID, "", agentruntime.ToolResult{ToolCallID: "c-1", Content: "package a"})
				deps.svc.mirrorCtlSendChild(ctlChildSessionID, "", agentruntime.ToolResult{ToolCallID: "c-1", Content: "package a"})
				deps.svc.mirrorCtlSendChild(ctlChildSessionID, "", agentruntime.UsageUpdate{Usage: &provider.Usage{CompletionTokens: 30}, TotalInputTokens: 1000})
				So(eventually(func() bool {
					for _, p := range countEvents[agentruntime.SubagentProgress](inj.all()) {
						if p.Info.TotalTokens == 1030 {
							return true
						}
					}
					return false
				}), ShouldBeTrue)

				w.Finish(ctx, CtlSendOutcome{Status: CtlSendCompleted, FinalText: "LGTM", AssistantMessageID: 9901})

				all := inj.all()
				So(countEvents[agentruntime.ToolCall](all), ShouldHaveLength, 1)
				So(countEvents[agentruntime.ToolResult](all), ShouldHaveLength, 1)
				for _, p := range countEvents[agentruntime.SubagentProgress](all) {
					So(p.ToolCallID, ShouldEqual, remoteHostCall)
					So(p.Info.Origin, ShouldEqual, agentruntime.SubagentOriginAgrctl)
				}
				done := countEvents[agentruntime.SubagentDone](all)
				So(done, ShouldHaveLength, 1)
				So(done[0].ToolCallID, ShouldEqual, remoteHostCall)
				So(done[0].Info.Origin, ShouldEqual, agentruntime.SubagentOriginAgrctl)
				So(done[0].Info.Status, ShouldEqual, "completed")
				So(done[0].Info.FinalText, ShouldEqual, "LGTM")
				So(done[0].Info.ToolUses, ShouldEqual, 1)
				So(done[0].Info.TotalTokens, ShouldEqual, 1030)
				models := countEvents[agentruntime.SubagentModel](all)
				So(models, ShouldResemble, []agentruntime.SubagentModel{{ToolCallID: remoteHostCall, Model: "claude-sonnet-4"}})

				So(localOverlays(parent), ShouldEqual, 0)
			})
		})
	})
}

func TestCtlSend_GivenRemoteParentAndChildWithoutLiveModel_WhenFinished_ThenModelFromChildFinalMessageIsPushed(t *testing.T) {
	Convey("Given 远端父会话的卡已绑定、子会话一直没报过模型, When Finish, Then 模型取自子会话最终那条消息,先于收尾推给 agentred", t, func() {
		deps := setupPeerSessionTest(t)
		ctx := context.Background()
		remoteParent(t, deps.svc)
		inj := &fakeInjector{}
		w := remoteWatch(deps.svc, inj, nil)
		defer w.Close()
		deps.message.EXPECT().Find(gomock.Any(), int64(9901)).
			Return(&chat_entity.Message{ID: 9901, SessionID: ctlChildSessionID, Model: "gpt-5"}, nil)
		So(w.Bind(ctx, remoteBind()), ShouldBeTrue)

		w.Finish(ctx, CtlSendOutcome{Status: CtlSendCompleted, FinalText: "ok", AssistantMessageID: 9901})

		all := inj.all()
		So(countEvents[agentruntime.SubagentModel](all), ShouldResemble, []agentruntime.SubagentModel{{ToolCallID: remoteHostCall, Model: "gpt-5"}})
		modelIdx, doneIdx := -1, -1
		for i, ev := range all {
			switch ev.(type) {
			case agentruntime.SubagentModel:
				modelIdx = i
			case agentruntime.SubagentDone:
				doneIdx = i
			}
		}
		So(modelIdx, ShouldBeLessThan, doneIdx)
	})
}

func TestCtlSend_GivenRemoteParentWithBackgroundCall_ThenTheCardKeepsLocalBashKind(t *testing.T) {
	Convey("Given agentred 绑定的是后台 shell 调用, Then overlay 起始帧保留 local_bash", t, func() {
		deps := setupPeerSessionTest(t)
		remoteParent(t, deps.svc)
		inj := &fakeInjector{}
		w := remoteWatch(deps.svc, inj, nil)
		defer w.Close()
		req := remoteBind()
		req.HostBackground = true
		So(w.Bind(context.Background(), req), ShouldBeTrue)
		So(eventually(func() bool { return len(countEvents[agentruntime.SubagentStarted](inj.all())) == 1 }), ShouldBeTrue)
		So(countEvents[agentruntime.SubagentStarted](inj.all())[0].Info.Kind, ShouldEqual, agentruntime.SubagentKindLocalBash)
	})
}

func TestCtlSend_GivenRemoteOrUnknownParent_WhenNoUsableHostBinding_ThenNothingIsBound(t *testing.T) {
	Convey("远端父会话不在本地匹配;拿不到可用的宿主绑定时一律不绑并记 info", t, func() {
		logs := observeChatLogs(t)
		ctx := context.Background()

		Convey("父轮在 agentred 上跑但 agentred 没有附来绑定 → no_host_binding,也不去连 agentred", func() {
			deps := setupPeerSessionTest(t)
			remoteParent(t, deps.svc)
			resolved := 0
			w := remoteWatch(deps.svc, &fakeInjector{}, &resolved)
			defer w.Close()
			So(w.Bind(ctx, reviewBind()), ShouldBeFalse)
			So(ctlNotBoundReasons(logs), ShouldResemble, []string{"no_host_binding"})
			So(resolved, ShouldEqual, 0)
		})

		Convey("本机没有在跑的轮、带着绑定,但父会话并不在 agentred 上执行 → parent_not_remote", func() {
			deps := setupPeerSessionTest(t)
			w := deps.svc.WatchCtlSend(ctlChildSessionID).(*ctlSendWatch)
			defer w.Close()
			w.remote.resolve = func(context.Context, int64) (ctlSubagentInjector, error) { return nil, nil }
			So(w.Bind(ctx, remoteBind()), ShouldBeFalse)
			So(ctlNotBoundReasons(logs), ShouldResemble, []string{"parent_not_remote"})
		})

		Convey("本机没有在跑的轮、也没有绑定 → no_active_turn", func() {
			deps := setupPeerSessionTest(t)
			w := deps.svc.WatchCtlSend(ctlChildSessionID)
			defer w.Close()
			So(w.Bind(ctx, reviewBind()), ShouldBeFalse)
			So(ctlNotBoundReasons(logs), ShouldResemble, []string{"no_active_turn"})
		})
	})
}

func TestCtlSend_GivenRemoteParentWithoutLocalTurn_WhenHostBindingArrives_ThenItIsUsed(t *testing.T) {
	Convey("Given 本机没有那一轮(agentred 的自主续轮)但父会话在 agentred 上执行, Then 照样采信绑定并推送", t, func() {
		deps := setupPeerSessionTest(t)
		inj := &fakeInjector{}
		w := remoteWatch(deps.svc, inj, nil)
		defer w.Close()
		So(w.Bind(context.Background(), remoteBind()), ShouldBeTrue)
		So(eventually(func() bool { return len(countEvents[agentruntime.SubagentStarted](inj.all())) == 1 }), ShouldBeTrue)
	})
}

func TestCtlSend_GivenLocalParent_WhenAForgedHostBindingArrives_ThenTheLocalMatchWins(t *testing.T) {
	Convey("Given 本机父轮, When 绑定请求里带着宿主绑定(本机 agrctl 自己伪造的头), Then 照本地匹配,不推给任何远端", t, func() {
		rig := startApprovalTurn(t)
		rig.feed(bashCall("sh-1", reviewCommand, false), StreamToolUse)
		resolved := 0
		w := remoteWatch(rig.svc, &fakeInjector{}, &resolved)
		defer w.Close()
		req := reviewBind()
		req.HostToolCallID = "sh-forged"
		So(w.Bind(context.Background(), req), ShouldBeTrue)
		v := readParent(rig.svc)
		So(v.overlays, ShouldHaveLength, 1)
		So(v.overlays[0].ParentToolCallID, ShouldEqual, "sh-1")
		So(resolved, ShouldEqual, 0)
	})
}

func TestCtlSend_GivenRemoteParent_WhenInjectionBlocks_ThenTheChildEventLoopNeverWaits(t *testing.T) {
	Convey("Given 注入 RPC 卡住, When 子会话连续吐事件, Then 每次都立刻返回;放开后照样送达", t, func() {
		deps := setupPeerSessionTest(t)
		remoteParent(t, deps.svc)
		inj := &fakeInjector{block: make(chan struct{})}
		w := remoteWatch(deps.svc, inj, nil)
		defer w.Close()
		So(w.Bind(context.Background(), remoteBind()), ShouldBeTrue)

		done := make(chan struct{})
		go func() {
			for i := 0; i < 50; i++ {
				deps.svc.mirrorCtlSendChild(ctlChildSessionID, "", agentruntime.ToolCall{ID: "c-" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Name: "Read"})
			}
			close(done)
		}()
		var returned bool
		select {
		case <-done:
			returned = true
		case <-time.After(time.Second):
		}
		close(inj.block)
		So(returned, ShouldBeTrue)
		So(eventually(func() bool { return len(countEvents[agentruntime.ToolCall](inj.all())) == 50 }), ShouldBeTrue)
	})
}

func TestCtlSend_GivenRemoteParent_WhenAgentredDisconnects_ThenTheTerminalStateStillArrivesAfterReconnect(t *testing.T) {
	Convey("Given 远端父会话的卡已绑定", t, func() {
		logs := observeChatLogs(t)
		deps := setupPeerSessionTest(t)
		ctx := context.Background()
		remoteParent(t, deps.svc)
		inj := &fakeInjector{}
		w := remoteWatch(deps.svc, inj, nil)
		defer w.Close()

		Convey("When 起始帧送达之后断线、子会话继续并结束, Then 子会话不受影响、Finish 不被拖住、只记一条 warn;重连后终态与最终文本送达", func() {
			So(w.Bind(ctx, remoteBind()), ShouldBeTrue)
			So(eventually(func() bool { return len(countEvents[agentruntime.SubagentStarted](inj.all())) == 1 }), ShouldBeTrue)
			inj.setDown(true)

			deps.svc.mirrorCtlSendChild(ctlChildSessionID, "", agentruntime.ToolCall{ID: "c-1", Name: "Read"})
			time.Sleep(20 * time.Millisecond)
			deps.svc.mirrorCtlSendChild(ctlChildSessionID, "m-1", agentruntime.ToolCall{ID: "c-2", Name: "Edit"})
			time.Sleep(20 * time.Millisecond)

			finished := make(chan struct{})
			go func() {
				w.Finish(ctx, CtlSendOutcome{Status: CtlSendCompleted, FinalText: "all done"})
				close(finished)
			}()
			var returned bool
			select {
			case <-finished:
				returned = true
			case <-time.After(time.Second):
			}
			So(returned, ShouldBeTrue)
			time.Sleep(30 * time.Millisecond) // 让重试再失败几次:warn 仍只有一条
			So(countEvents[agentruntime.SubagentDone](inj.all()), ShouldBeEmpty)

			warns := remoteWarns(logs)
			So(warns, ShouldHaveLength, 1)
			So(warns[0].ContextMap()["sessionId"], ShouldEqual, approvalSessionID)
			So(warns[0].ContextMap()["toolCallId"], ShouldEqual, remoteHostCall)

			inj.setDown(false)
			So(eventually(func() bool { return len(countEvents[agentruntime.SubagentDone](inj.all())) == 1 }), ShouldBeTrue)
			done := countEvents[agentruntime.SubagentDone](inj.all())[0]
			So(done.Info.Status, ShouldEqual, "completed")
			So(done.Info.FinalText, ShouldEqual, "all done")
			So(done.Info.ToolUses, ShouldEqual, 2)
			So(countEvents[agentruntime.SubagentModel](inj.all()), ShouldResemble, // 断线时推的模型也随重试送达
				[]agentruntime.SubagentModel{{ToolCallID: remoteHostCall, Model: "m-1"}})
			So(countEvents[agentruntime.SubagentStarted](inj.all()), ShouldHaveLength, 1)
			So(retrySettled(logs), ShouldBeTrue)
			time.Sleep(30 * time.Millisecond)
			So(countEvents[agentruntime.SubagentDone](inj.all()), ShouldHaveLength, 1) // 送达之后不再重发
		})

		Convey("When 绑定那一刻就连不上 agentred, Then 无从核实,不绑", func() {
			inj.setDown(true)
			So(w.Bind(ctx, remoteBind()), ShouldBeFalse)
			So(ctlNotBoundReasons(logs), ShouldResemble, []string{"parent_unreachable"})
		})

		Convey("When 起始帧还没送到就断线, Then 收尾不丢起始帧:重连后起始帧先于收尾一起送达", func() {
			inj.block = make(chan struct{})
			So(w.Bind(ctx, remoteBind()), ShouldBeTrue)
			inj.setDown(true)
			finished := make(chan struct{})
			go func() {
				w.Finish(ctx, CtlSendOutcome{Status: CtlSendFailed, FinalText: "boom"})
				close(finished)
			}()
			close(inj.block)
			<-finished
			So(inj.all(), ShouldBeEmpty)

			inj.setDown(false)
			So(eventually(func() bool { return len(countEvents[agentruntime.SubagentDone](inj.all())) == 1 }), ShouldBeTrue)
			last := inj.all()
			So(countEvents[agentruntime.SubagentStarted](last), ShouldHaveLength, 1)
			startIdx, doneIdx := -1, -1
			for i, ev := range last {
				switch ev.(type) {
				case agentruntime.SubagentStarted:
					startIdx = i
				case agentruntime.SubagentDone:
					doneIdx = i
				}
			}
			So(startIdx, ShouldBeLessThan, doneIdx)
			done := countEvents[agentruntime.SubagentDone](last)[0]
			So(done.Info.FinalText, ShouldEqual, "boom")
			So(done.Info.Status, ShouldEqual, "failed")
			So(retrySettled(logs), ShouldBeTrue)
		})
	})
}

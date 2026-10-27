package piagent

import (
	"context"
	"errors"
	"sync"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// steerRecorder 记下真正送进 pi 当前这一轮的 steer。
type steerRecorder struct {
	mu    sync.Mutex
	texts []string
}

func (s *steerRecorder) Steer(_ context.Context, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.texts = append(s.texts, text)
	return nil
}

func (s *steerRecorder) sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.texts...)
}

// steerHoldSession 是能热切换模型、轮中能收 steer 的 pi 会话替身。
type steerHoldSession struct {
	modelSwitchSession
	steers *steerRecorder
}

func (s *steerHoldSession) ActiveStream() steerStream { return s.steers }

func TestPiSteerNextTurn(t *testing.T) {
	Convey("Given 一条 Pi 会话以 CLI 默认模型开了一轮且这一轮还在跑", t, func() {
		recorder := &steerRecorder{}
		release := make(chan struct{})
		var releaseOnce sync.Once
		finishTurn := func() { releaseOnce.Do(func() { close(release) }) }
		defer finishTurn()
		var sess *steerHoldSession
		restore := SetSessionFactoryForTest(func(agentruntime.RunRequest, map[string]string, string) (sessionHandle, error) {
			sess = &steerHoldSession{
				modelSwitchSession: modelSwitchSession{fakeSession: fakeSession{sid: "pi-session", stream: &blockingStream{release: release}}},
				steers:             recorder,
			}
			return sess, nil
		})
		defer restore()
		r := NewWithPool(agentruntime.NewCLISessionPool(8))
		req := agentruntime.RunRequest{Backend: piBackend(""), SessionID: 41, Cwd: t.TempDir(), UserText: "long essay", CLIModelMode: agentruntime.CLIModelModeCLIDefault}
		events, _, err := r.Run(context.Background(), req)
		require.NoError(t, err)

		Convey("When 挂起切换后排队一条消息 Then 它不进 pi 当前这一轮，轮末作为下一轮交回，下一轮先应用挂起的模型", func() {
			So(r.SteerNextTurn(context.Background(), 41, "q1", "Reply with exactly: OK4"), ShouldBeNil)
			So(recorder.sent(), ShouldBeEmpty)

			finishTurn()
			for range events {
			}
			So(r.DrainPending(context.Background(), 41), ShouldResemble, []agentruntime.ConsumedSteer{{QueuedID: "q1", Text: "Reply with exactly: OK4"}})

			Convey("And 交回之后、下一轮开始前的 Steer 仍被接住，不报无轮可插", func() {
				So(r.Steer(context.Background(), 41, "q2", "and then"), ShouldBeNil)
				So(recorder.sent(), ShouldBeEmpty)

				sess.stream = &emptyStream{}
				next := req
				next.UserText = "Reply with exactly: OK4"
				next.CLIModelMode, next.CLIModel = agentruntime.CLIModelModeFixed, "deepseek/deepseek-flash"
				nextEvents, _, err := r.Run(context.Background(), next)
				require.NoError(t, err)
				for range nextEvents {
				}
				So(sess.models, ShouldResemble, []string{"deepseek/deepseek-flash"})
				So(sess.gotPrompt, ShouldEqual, "Reply with exactly: OK4")
				So(r.DrainPending(context.Background(), 41), ShouldResemble, []agentruntime.ConsumedSteer{{QueuedID: "q2", Text: "and then"}})
				So(r.DrainPending(context.Background(), 41), ShouldBeEmpty)
			})
		})

		Convey("When 轮中照常 Steer Then 仍直接送进 pi 当前这一轮", func() {
			So(r.Steer(context.Background(), 41, "q1", "now do X"), ShouldBeNil)
			So(recorder.sent(), ShouldResemble, []string{"now do X"})
			finishTurn()
			for range events {
			}
			So(r.DrainPending(context.Background(), 41), ShouldBeEmpty)
		})

		Convey("When 这一轮已经结束 Then 留给下一轮的消息报无轮可插", func() {
			finishTurn()
			for range events {
			}
			So(errors.Is(r.SteerNextTurn(context.Background(), 41, "q1", "late"), agentruntime.ErrNoActiveTurn), ShouldBeTrue)
			So(r.DrainPending(context.Background(), 41), ShouldBeEmpty)
		})

		Convey("When 会话被关掉 Then 扣下的消息随之作废", func() {
			So(r.SteerNextTurn(context.Background(), 41, "q1", "held"), ShouldBeNil)
			finishTurn()
			for range events {
			}
			r.CloseSession(context.Background(), 41)
			So(r.DrainPending(context.Background(), 41), ShouldBeEmpty)
		})
	})
}

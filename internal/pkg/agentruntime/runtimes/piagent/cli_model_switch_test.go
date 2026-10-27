package piagent

import (
	"context"
	"errors"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	pkgpiagent "github.com/agentre-hub/agentre/pkg/piagent"
)

// modelSwitchSession 是能热切换模型的 pi 会话替身：记下每次 set_model 的值，按需拒绝。
type modelSwitchSession struct {
	fakeSession
	models []string
	reject string
}

func (s *modelSwitchSession) SetModel(_ context.Context, value string) (string, error) {
	if s.reject != "" {
		return "", errors.New(s.reject)
	}
	s.models = append(s.models, value)
	_, id, _ := strings.Cut(value, "/")
	return id, nil
}

var _ agentruntime.CLIModelSetter = (*Runtime)(nil)
var _ agentruntime.CLIModelStateReader = (*Runtime)(nil)

func TestPiCLIModelSwitch(t *testing.T) {
	Convey("Given 一条 CLI 登录态的 pi 会话以后端默认模型跑完了一轮", t, func() {
		// 每次 spawn 都是一个新进程；sess 指向最近起的那个。
		var sess *modelSwitchSession
		spawns := 0
		var launched []string
		restore := SetSessionFactoryForTest(func(req agentruntime.RunRequest, _ map[string]string, _ string) (sessionHandle, error) {
			spawns++
			launched = append(launched, piModelFallback(req))
			sess = &modelSwitchSession{fakeSession: fakeSession{stream: &emptyStream{}, sid: "pi-session"}}
			return sess, nil
		})
		defer restore()
		r := NewWithPool(agentruntime.NewCLISessionPool(8))
		r.queryModels = func(context.Context, *pkgpiagent.Client) (pkgpiagent.ModelCatalog, error) {
			return pkgpiagent.ModelCatalog{Effective: pkgpiagent.Model{Provider: "anthropic", ID: "claude-sonnet-5-5"}}, nil
		}
		req := agentruntime.RunRequest{Backend: piBackend("zhipu/glm-5.3"), SessionID: 41, Cwd: t.TempDir(), UserText: "one"}
		run := func(req agentruntime.RunRequest) error {
			events, _, err := r.Run(context.Background(), req)
			if err == nil {
				for range events {
				}
			}
			return err
		}
		require.NoError(t, run(req))
		So(launched, ShouldResemble, []string{"zhipu/glm-5.3"})
		So(r.CLIModelState(context.Background(), 41), ShouldResemble, agentruntime.CLIModelState{Active: true, AppliedValue: "zhipu/glm-5.3"})

		Convey("When 空闲时切到另一个模型 Then 发 set_model、进程不重启、下一轮不再切", func() {
			So(r.SetCLIModel(context.Background(), 41, "zhipu/glm-5.4"), ShouldBeNil)
			So(sess.models, ShouldResemble, []string{"zhipu/glm-5.4"})
			So(r.CLIModelState(context.Background(), 41), ShouldResemble, agentruntime.CLIModelState{Active: true, AppliedValue: "zhipu/glm-5.4", ModelID: "glm-5.4"})

			next := req
			next.CLIModelMode, next.CLIModel = "fixed", "zhipu/glm-5.4"
			require.NoError(t, run(next))
			So(spawns, ShouldEqual, 1)
			So(sess.models, ShouldResemble, []string{"zhipu/glm-5.4"})
			So(sess.closed, ShouldBeFalse)
		})

		Convey("When pi 拒绝 Then 返回 pi 原文且会话状态不变", func() {
			sess.reject = "Model not found: zhipu/nope"
			err := r.SetCLIModel(context.Background(), 41, "zhipu/nope")
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "Model not found: zhipu/nope")
			So(r.CLIModelState(context.Background(), 41), ShouldResemble, agentruntime.CLIModelState{Active: true, AppliedValue: "zhipu/glm-5.3"})
			So(spawns, ShouldEqual, 1)
		})

		Convey("When 切回 CLI 默认（空串） Then 用查询进程解析出的默认值 set_model，并按会话口径记成空串", func() {
			So(r.SetCLIModel(context.Background(), 41, ""), ShouldBeNil)
			So(sess.models, ShouldResemble, []string{"anthropic/claude-sonnet-5-5"})
			So(r.CLIModelState(context.Background(), 41), ShouldResemble, agentruntime.CLIModelState{Active: true, AppliedValue: "", ModelID: "claude-sonnet-5-5"})
			So(spawns, ShouldEqual, 1)
		})

		Convey("When 一轮进行中选择两次 Then 这一轮不切，下一轮开始前只应用最后一次", func() {
			r.registerPrepared(41, &preparedRun{})
			So(r.SetCLIModel(context.Background(), 41, "zhipu/glm-5.4"), ShouldBeNil)
			So(r.SetCLIModel(context.Background(), 41, "zhipu/glm-5.5"), ShouldBeNil)
			So(sess.models, ShouldBeEmpty)
			r.mu.Lock()
			delete(r.prepared, 41)
			r.mu.Unlock()

			next := req
			next.CLIModelMode, next.CLIModel = "fixed", "zhipu/glm-5.5"
			require.NoError(t, run(next))
			So(sess.models, ShouldResemble, []string{"zhipu/glm-5.5"})
			So(spawns, ShouldEqual, 1)
		})

		Convey("When 下一轮开始前应用被拒 Then 这一轮不开始、原文留在会话状态里", func() {
			sess.reject = "Model not found: zhipu/nope"
			next := req
			next.CLIModelMode, next.CLIModel = "fixed", "zhipu/nope"
			err := run(next)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "Model not found: zhipu/nope")
			So(sess.streamCall, ShouldEqual, 1)
			So(r.CLIModelState(context.Background(), 41).Error, ShouldContainSubstring, "Model not found: zhipu/nope")

			Convey("And 进程被清扫后失败原文仍在", func() {
				r.pool.Remove(sessionKey(41))
				So(r.CLIModelState(context.Background(), 41).Error, ShouldContainSubstring, "Model not found: zhipu/nope")
			})
		})

		Convey("When 进程不在了 Then 切换返回 ErrNoActiveTurn，重启时以会话选择 --model 启动", func() {
			r.pool.Remove(sessionKey(41))
			So(errors.Is(r.SetCLIModel(context.Background(), 41, "zhipu/glm-5.4"), agentruntime.ErrNoActiveTurn), ShouldBeTrue)
			So(r.CLIModelState(context.Background(), 41), ShouldResemble, agentruntime.CLIModelState{})

			next := req
			next.CLIModelMode, next.CLIModel = "fixed", "zhipu/glm-5.4"
			require.NoError(t, run(next))
			So(spawns, ShouldEqual, 2)
			So(launched[1], ShouldEqual, "zhipu/glm-5.4")
			So(sess.models, ShouldBeEmpty)
			So(r.CLIModelState(context.Background(), 41).AppliedValue, ShouldEqual, "zhipu/glm-5.4")
		})
	})
}

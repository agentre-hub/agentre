package codex

import (
	"context"
	"errors"
	"sync"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	pkgcodex "github.com/agentre-hub/agentre/pkg/codex"
)

// cliModelRuntimeSession 记录 runtime 交给 turn/start 的模型，并按需拒绝某一轮，
// 于是「SetCLIModel 记下 → 下一轮 turn/start 下发 → 记账 / 拒绝」这条链在单测里整跑一遍。
type cliModelRuntimeSession struct {
	*fakeRuntimeSession
	mu        sync.Mutex
	delivered []string
	reject    map[string]error
}

func newCLIModelRuntimeSession(sid, model string) *cliModelRuntimeSession {
	return &cliModelRuntimeSession{
		fakeRuntimeSession: &fakeRuntimeSession{stream: &emptyRuntimeStream{}, sid: sid, model: model},
	}
}

func (s *cliModelRuntimeSession) Stream(_ context.Context, _ string, _ string, model string) (cxStream, error) {
	return s.startTurn(model)
}

func (s *cliModelRuntimeSession) StreamInput(_ context.Context, _ []pkgcodex.UserInput, _ string, model string) (cxStream, error) {
	return s.startTurn(model)
}

func (s *cliModelRuntimeSession) Model() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.model
}

func (s *cliModelRuntimeSession) startTurn(model string) (cxStream, error) {
	s.mu.Lock()
	s.delivered = append(s.delivered, model)
	s.mu.Unlock()
	if err, ok := s.reject[model]; ok {
		return nil, err
	}
	if model != "" {
		// codex 的 turn/start 模型对后续轮持续生效，线程报告的实际模型随之改变。
		s.mu.Lock()
		s.model = model
		s.mu.Unlock()
	}
	return s.stream, nil
}

func (s *cliModelRuntimeSession) deliveredModels() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.delivered...)
}

// cliModelCatalog 让「CLI 默认」的解析有确定答案，不必起真的短命进程。
func cliModelCatalog(effective string) func(context.Context, *pkgcodex.Client) (pkgcodex.ModelCatalog, error) {
	return func(context.Context, *pkgcodex.Client) (pkgcodex.ModelCatalog, error) {
		return pkgcodex.ModelCatalog{
			Models:         []pkgcodex.Model{{ID: effective, Model: effective, DisplayName: effective}},
			EffectiveModel: effective,
		}, nil
	}
}

func cliModelRunRequest(be *agent_backend_entity.AgentBackend, sessionID int64, mode, value, cwd string) agentruntime.RunRequest {
	return agentruntime.RunRequest{
		Backend:      be,
		SessionID:    sessionID,
		Cwd:          cwd,
		UserText:     "hi",
		CLIModelMode: mode,
		CLIModel:     value,
	}
}

func TestCLIModelSwitch_AppliesOnNextTurnStart(t *testing.T) {
	Convey("Given a Codex CLI 登录态会话空闲", t, func() {
		pool := agentruntime.NewCLISessionPool(8)
		sess := newCLIModelRuntimeSession("thread-cli-model", "gpt-5.5")
		spawns := 0
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			spawns++
			return sess, nil
		})
		defer restore()
		r := NewWithPool(pool)
		be := &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex), EnvJSON: "{}"}
		cwd := t.TempDir()
		run := func(mode, value string) *agentruntime.RunResult {
			events, result, err := r.Run(context.Background(), cliModelRunRequest(be, 21, mode, value, cwd))
			So(err, ShouldBeNil)
			for range events {
			}
			return result
		}

		Convey("When 空闲时选了固定模型, Then 下一轮 turn/start 在同一个进程与线程上携带它", func() {
			// 没做过选择的一轮照旧：不下发模型，RunResult 报线程自己的模型。
			So(run("", "").Model, ShouldEqual, "gpt-5.5")
			require.NoError(t, r.SetCLIModel(context.Background(), 21, "gpt-6-luna"))
			result := run("fixed", "gpt-6-luna")
			So(sess.deliveredModels(), ShouldResemble, []string{"", "gpt-6-luna"})
			So(spawns, ShouldEqual, 1)
			So(result.Model, ShouldEqual, "gpt-6-luna")
		})

		Convey("When 轮中进行的选择只随下一轮请求到达, Then 同样在下一轮 turn/start 生效", func() {
			So(run("", "").Model, ShouldEqual, "gpt-5.5")
			// 轮中 chat_svc 不调 SetCLIModel：选择只落在下一轮的请求里。
			So(run("fixed", "gpt-6-luna").Model, ShouldEqual, "gpt-6-luna")
			So(sess.deliveredModels(), ShouldResemble, []string{"", "gpt-6-luna"})
			So(spawns, ShouldEqual, 1)
		})

		Convey("When 选回「跟随 CLI 默认」, Then 下一轮把 config/read 解析到的默认模型显式下发, 不残留上一个固定值", func() {
			So(run("fixed", "gpt-6-luna").Model, ShouldEqual, "gpt-6-luna")
			r.queryModels = cliModelCatalog("gpt-9-default")
			require.NoError(t, r.SetCLIModel(context.Background(), 21, ""))
			result := run("cli-default", "")
			So(sess.deliveredModels(), ShouldResemble, []string{"gpt-6-luna", "gpt-9-default"})
			So(result.Model, ShouldEqual, "gpt-9-default")
			So(spawns, ShouldEqual, 1)
		})

		Convey("When 线程上还挂着固定值而选择被清回「跟随 Agent 绑定」(后端没有默认模型), Then 下一轮也显式复位到 CLI 默认", func() {
			So(run("fixed", "gpt-6-luna").Model, ShouldEqual, "gpt-6-luna")
			r.queryModels = cliModelCatalog("gpt-9-default")
			So(run("", "").Model, ShouldEqual, "gpt-9-default")
			So(sess.deliveredModels(), ShouldResemble, []string{"gpt-6-luna", "gpt-9-default"})
		})
	})
}

func TestCLIModelSwitch_TurnStartRejectionFailsTheTurn(t *testing.T) {
	Convey("Given the CLI rejects the selected model on turn/start", t, func() {
		sess := newCLIModelRuntimeSession("thread-cli-reject", "gpt-5.5")
		sess.reject = map[string]error{
			"bad-model": errors.New("codex app-server: rpc error -32602: model 'bad-model' not found"),
		}
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			return sess, nil
		})
		defer restore()
		r := NewWithPool(nil)
		be := &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex), EnvJSON: "{}"}

		Convey("When 下一轮应用失败, Then 这一轮不开始, 原文交回 chat_svc 的失败通道", func() {
			events, result, err := r.Run(context.Background(), cliModelRunRequest(be, 22, "fixed", "bad-model", t.TempDir()))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "model 'bad-model' not found")
			So(events, ShouldBeNil)
			So(result, ShouldBeNil)
		})
	})
}

// TestCLIModelDefaultResolutionFailure 锁住「不得静默回落到旧模型继续跑」：连 CLI 默认都
// 解析不出来时，这一轮不开跑，原文交回 chat_svc 的失败通道，pill 随之失效。
func TestCLIModelDefaultResolutionFailure(t *testing.T) {
	Convey("Given the thread still carries a fixed model from the last turn", t, func() {
		sess := newCLIModelRuntimeSession("thread-cli-unresolved", "gpt-5.5")
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			return sess, nil
		})
		defer restore()
		r := NewWithPool(nil)
		be := &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex), EnvJSON: "{}"}
		cwd := t.TempDir()
		first, _, err := r.Run(context.Background(), cliModelRunRequest(be, 27, "fixed", "gpt-6-luna", cwd))
		require.NoError(t, err)
		for range first {
		}

		Convey("When 选回 CLI 默认但查询进程报错, Then 这一轮不开始并带上 CLI 原文, 不拿旧固定值接着跑", func() {
			r.queryModels = func(context.Context, *pkgcodex.Client) (pkgcodex.ModelCatalog, error) {
				return pkgcodex.ModelCatalog{}, errors.New("config/read failed: no such method")
			}
			events, result, err := r.Run(context.Background(), cliModelRunRequest(be, 27, "cli-default", "", cwd))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "config/read failed")
			So(events, ShouldBeNil)
			So(result, ShouldBeNil)
			So(sess.deliveredModels(), ShouldResemble, []string{"gpt-6-luna"})
			So(r.CLIModelState(context.Background(), 27).Error, ShouldContainSubstring, "config/read failed")
		})
	})
}

// TestCLIModelState 覆盖 pill 读的那份进程自报：实际模型、挂起口径、失败原文的进出。
func TestCLIModelState(t *testing.T) {
	Convey("Given a live Codex app-server with a settled CLI model choice", t, func() {
		sess := newCLIModelRuntimeSession("thread-cli-state", "gpt-5.5")
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			return sess, nil
		})
		defer restore()
		r := NewWithPool(nil)
		be := &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex), EnvJSON: "{}"}
		cwd := t.TempDir()
		run := func(mode, value string) error {
			events, _, err := r.Run(context.Background(), cliModelRunRequest(be, 24, mode, value, cwd))
			if err == nil {
				for range events {
				}
			}
			return err
		}

		Convey("When 固定模型生效, Then 账本记的是会话语义的选择值与线程实际模型, 不是「挂起」", func() {
			require.NoError(t, run("fixed", "gpt-6-luna"))
			state := r.CLIModelState(context.Background(), 24)
			So(state.Active, ShouldBeTrue)
			So(state.AppliedValue, ShouldEqual, "gpt-6-luna")
			So(state.ModelID, ShouldEqual, "gpt-6-luna")
			So(state.Error, ShouldBeEmpty)
		})

		Convey("When 跟随 CLI 默认生效, Then 记账值是空串(与 chat_svc 的 wanted 同源), 不冒充解析出的模型", func() {
			r.queryModels = func(context.Context, *pkgcodex.Client) (pkgcodex.ModelCatalog, error) {
				return pkgcodex.ModelCatalog{EffectiveModel: "gpt-9-default"}, nil
			}
			require.NoError(t, run("cli-default", ""))
			state := r.CLIModelState(context.Background(), 24)
			So(state.AppliedValue, ShouldBeEmpty)
			So(state.ModelID, ShouldEqual, "gpt-9-default")
		})

		Convey("When turn/start 被拒, Then 状态带 codex 原文; 再选一次即清除(提示重新选择)", func() {
			sess.reject = map[string]error{"bad": errors.New("codex app-server: rpc error -32602: model not found")}
			So(run("fixed", "bad"), ShouldNotBeNil)
			state := r.CLIModelState(context.Background(), 24)
			So(state.Error, ShouldContainSubstring, "model not found")
			// 失败的那一轮会把池里那条摘掉（codex 既有语义）；进程不在时 SetCLIModel 按
			// 契约返回 ErrNoActiveTurn，但选择已由 chat_svc 落库、下一轮以它重启。
			So(errors.Is(r.SetCLIModel(context.Background(), 24, "gpt-6-luna"), agentruntime.ErrNoActiveTurn), ShouldBeTrue)
			So(r.CLIModelState(context.Background(), 24).Error, ShouldBeEmpty)
		})
	})

	Convey("Given no live Codex app-server", t, func() {
		r := NewWithPool(nil)

		Convey("Then 记录选择返回 ErrNoActiveTurn(chat_svc 只落库), 状态是空快照不猜", func() {
			So(errors.Is(r.SetCLIModel(context.Background(), 25, "gpt-6-luna"), agentruntime.ErrNoActiveTurn), ShouldBeTrue)
			state := r.CLIModelState(context.Background(), 25)
			So(state.Active, ShouldBeFalse)
			So(state.ModelID, ShouldBeEmpty)
			So(state.Error, ShouldBeEmpty)
		})
	})
}

// TestCLIModelDefaultResolutionIsReused 钉住「不每轮为同一个字符串起一次查询进程」：
// 已停在 CLI 默认时沿用上一轮确认过的解析结果。
func TestCLIModelDefaultResolutionIsReused(t *testing.T) {
	Convey("Given a session already following the CLI default", t, func() {
		queries := 0
		r := NewWithPool(nil)
		r.queryModels = func(context.Context, *pkgcodex.Client) (pkgcodex.ModelCatalog, error) {
			queries++
			return pkgcodex.ModelCatalog{EffectiveModel: "gpt-9-default"}, nil
		}
		sess := newCLIModelRuntimeSession("thread-cli-reuse", "gpt-9-default")
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			return sess, nil
		})
		defer restore()
		be := &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex), EnvJSON: "{}"}
		cwd := t.TempDir()
		run := func() *agentruntime.RunResult {
			events, result, err := r.Run(context.Background(), cliModelRunRequest(be, 26, "cli-default", "", cwd))
			So(err, ShouldBeNil)
			for range events {
			}
			return result
		}

		Convey("When 连跑两轮, Then 两轮都显式下发同一个默认模型, 但只问一次 CLI", func() {
			So(run().Model, ShouldEqual, "gpt-9-default")
			So(run().Model, ShouldEqual, "gpt-9-default")
			So(sess.deliveredModels(), ShouldResemble, []string{"gpt-9-default", "gpt-9-default"})
			So(queries, ShouldEqual, 1)
		})
	})
}

// TestCLIModelChoiceKeepsLaunchIdentity 锁住「CLI 模型不参与启动身份」：选择换了也要复用
// 同一个 app-server 进程（决策 6），否则每次切换都重启 CLI。
func TestCLIModelChoiceKeepsLaunchIdentity(t *testing.T) {
	Convey("Given a codex backend with its own default model", t, func() {
		be := &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex), DefaultModel: "gpt-5.5"}
		base := cliModelRunRequest(be, 23, "", "", "/tmp/work")
		identity := launchIdentity(base)

		Convey("When the session fixes a CLI model, Then the launch identity is unchanged", func() {
			base.CLIModelMode, base.CLIModel = "fixed", "gpt-6-luna"
			So(launchIdentity(base), ShouldEqual, identity)
		})

		Convey("When the session follows the CLI default, Then the launch identity is unchanged", func() {
			base.CLIModelMode, base.CLIModel = "cli-default", ""
			So(launchIdentity(base), ShouldEqual, identity)
		})
	})
}

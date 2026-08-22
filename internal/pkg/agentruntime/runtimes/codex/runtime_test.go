package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cago-frame/agents/provider"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/capability"
	pkgcodex "github.com/agentre-ai/agentre/pkg/codex"
)

// TestCodexCapabilities 钉死 codex runtime 的能力矩阵 + permission mode 元数据。
// 与 claudecode 的关键差异:CapCancelSteer/CapDrainSteer=false;
// CapReportContextWindow=true;PermissionModeMeta 仅 default/plan,SwitchableDuringTurn=false。
// TestRecordLaunchedModel_Bounded 锁住 launchedModel 的容量裁剪:池按 LRU 上限逐出
// 空闲会话时不回调本包,若不加上限,map 会随进程内用过的会话数无界增长。裁掉的是
// 最旧的 key —— 它若日后回池,spawnKeyChanged 只会误判一次无谓重 spawn,不产错误结果。
func TestRecordLaunchedModel_Bounded(t *testing.T) {
	Convey("Given 一个 codex runtime", t, func() {
		r := New()

		Convey("When 记录超过上限的会话数 Then map 被 FIFO 裁剪到上限以内", func() {
			for i := 0; i < maxTrackedLaunchedModels+64; i++ {
				r.recordLaunchedModel(fmt.Sprintf("sess-%d", i), launchedSpawnKey{model: "gpt-5.5"})
			}
			r.mu.Lock()
			defer r.mu.Unlock()
			So(len(r.launchedModel), ShouldBeLessThanOrEqualTo, maxTrackedLaunchedModels)
			So(len(r.launchedModelOrder), ShouldEqual, len(r.launchedModel))
			// 最旧 64 个被裁掉,最新 512 个保留。
			_, evicted := r.launchedModel["sess-0"]
			_, kept := r.launchedModel[fmt.Sprintf("sess-%d", maxTrackedLaunchedModels+63)]
			So(evicted, ShouldBeFalse)
			So(kept, ShouldBeTrue)
		})

		Convey("When forgetLaunchedModel 剔除已记录 key Then map 与 FIFO 序同步删除", func() {
			r.recordLaunchedModel("a", launchedSpawnKey{model: "m1"})
			r.recordLaunchedModel("b", launchedSpawnKey{model: "m2"})
			r.forgetLaunchedModel("a")
			r.mu.Lock()
			defer r.mu.Unlock()
			_, aGone := r.launchedModel["a"]
			So(aGone, ShouldBeFalse)
			So(r.launchedModelOrder, ShouldResemble, []string{"b"})
		})
	})
}

func TestCodexCapabilities(t *testing.T) {
	Convey("codex Capabilities 矩阵", t, func() {
		r := New()
		caps := r.Capabilities()
		So(caps.Has(capability.CapSteer), ShouldBeTrue)
		So(caps.Has(capability.CapCancelSteer), ShouldBeFalse) // codex fire-and-forget
		So(caps.Has(capability.CapDrainSteer), ShouldBeFalse)  // 无 hook 队列
		So(caps.Has(capability.CapAbort), ShouldBeTrue)
		So(caps.Has(capability.CapImageInput), ShouldBeTrue)
		So(caps.Has(capability.CapSetPermission), ShouldBeTrue)
		So(caps.Has(capability.CapAnswerUserAsk), ShouldBeTrue)
		So(caps.Has(capability.CapToolPermission), ShouldBeTrue)
		So(caps.Has(capability.CapForkSession), ShouldBeTrue)
		So(caps.Has(capability.CapReportContextWindow), ShouldBeTrue)
		So(caps.Has(capability.CapCompact), ShouldBeTrue)
		So(caps.Has(capability.CapMCPTools), ShouldBeTrue)
		So(caps.Has(capability.CapSkills), ShouldBeTrue)
	})

	Convey("codex PermissionModeMeta", t, func() {
		caps := New().Capabilities()
		So(caps.PermissionModeMeta.AllowedModes, ShouldResemble, []string{"default", "plan"})
		So(caps.PermissionModeMeta.DefaultMode, ShouldEqual, "default")
		So(caps.PermissionModeMeta.SwitchableDuringTurn, ShouldBeFalse)
		So(caps.PermissionModeMeta.Order, ShouldResemble, []string{"default", "plan"})
		// LaunchDefaultMode="default":codex 协议每次 launch 必须显式 mode。
		So(caps.PermissionModeMeta.LaunchDefaultMode, ShouldEqual, "default")
	})
}

func TestSubmitToolPermission(t *testing.T) {
	Convey("Given Codex approval request is active, when user allows for session, then approval is submitted and resolved event is emitted", t, func() {
		stream := newApprovalRuntimeStream(pkgcodex.Event{
			Kind: pkgcodex.EventApprovalRequest,
			Approval: &pkgcodex.ApprovalRequestEvent{
				RequestID: "approval-1",
				ItemID:    "item-command",
				ToolName:  "Bash",
				Input:     []byte(`{"command":"rm -rf build"}`),
			},
		})
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			return &fakeRuntimeSession{stream: stream, sid: "thread-approval"}, nil
		})
		defer restore()

		r := New()
		events, _, err := r.Run(context.Background(), agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex), EnvJSON: "{}"},
			SessionID: 42,
			Cwd:       t.TempDir(),
			UserText:  "run it",
		})
		So(err, ShouldBeNil)

		ev := <-events
		req, ok := ev.(agentruntime.ToolPermissionRequest)
		So(ok, ShouldBeTrue)
		So(req.RequestID, ShouldEqual, "approval-1")

		err = r.SubmitToolPermission(context.Background(), 42, "approval-1", true, true, "")
		So(err, ShouldBeNil)
		So(stream.submittedRequestID, ShouldEqual, "approval-1")
		So(stream.submittedAllow, ShouldBeTrue)
		So(stream.submittedAlways, ShouldBeTrue)

		resolved := <-events
		res, ok := resolved.(agentruntime.ToolPermissionResolved)
		So(ok, ShouldBeTrue)
		So(res.RequestID, ShouldEqual, "approval-1")
		So(res.Allowed, ShouldBeTrue)
		So(res.AlwaysAllow, ShouldBeTrue)

		stream.finish()
		for range events {
		}
	})

	Convey("Given no active Codex approval request, when user answers, then no active turn is returned", t, func() {
		err := New().SubmitToolPermission(context.Background(), 42, "missing", true, false, "")
		So(err, ShouldEqual, agentruntime.ErrNoActiveTurn)
	})
}

func TestRun_DefaultModelWhenProviderMissing(t *testing.T) {
	Convey("codex runtime 在 CLI 自身登录态下回填 app-server resolved model", t, func() {
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			return &fakeRuntimeSession{stream: &emptyRuntimeStream{}, sid: "thread-default", model: "gpt-5.6-sol"}, nil
		})
		defer restore()

		events, result, err := New().Run(context.Background(), agentruntime.RunRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type:    string(agent_backend_entity.TypeCodex),
				EnvJSON: "{}",
			},
			SessionID: 1,
			Cwd:       t.TempDir(),
			UserText:  "hello",
		})
		So(err, ShouldBeNil)
		So(result, ShouldNotBeNil)
		for range events {
		}

		So(result.Model, ShouldEqual, "gpt-5.6-sol")
		So(result.ProviderSessionID, ShouldEqual, "thread-default")
	})
}

func TestRun_ModelResolution(t *testing.T) {
	Convey("codex runtime model resolution", t, func() {
		Convey("Given app-server does not report model, when provider is missing, then fallback default is used", func() {
			restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
				return &fakeRuntimeSession{stream: &emptyRuntimeStream{}, sid: "thread-default"}, nil
			})
			defer restore()

			events, result, err := New().Run(context.Background(), agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{
					Type:    string(agent_backend_entity.TypeCodex),
					EnvJSON: "{}",
				},
				SessionID: 1,
				Cwd:       t.TempDir(),
				UserText:  "hello",
			})
			So(err, ShouldBeNil)
			for range events {
			}

			So(result.Model, ShouldEqual, "gpt-5.5")
		})

		Convey("Given provider model is configured, when app-server reports a different model, then thread actual model (sess.Model) wins (design decision 9)", func() {
			restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
				return &fakeRuntimeSession{stream: &emptyRuntimeStream{}, sid: "thread-provider", model: "gpt-5.6-sol"}, nil
			})
			defer restore()

			events, result, err := New().Run(context.Background(), agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{
					Type:    string(agent_backend_entity.TypeCodex),
					EnvJSON: "{}",
				},
				Effective: &agentruntime.EffectiveLLMConfig{ModelID: "gpt-5.4"},
				SessionID: 1,
				Cwd:       t.TempDir(),
				UserText:  "hello",
			})
			So(err, ShouldBeNil)
			for range events {
			}

			So(result.Model, ShouldEqual, "gpt-5.6-sol")
		})

		// sess.Model() 只在 app-server 的 thread start/resume 结果里带 model 时才有值
		// (pkg/codex ensureThread → s.model = thread.Model)。观测不到时不能拿死常量
		// defaultModelID 冒充实际模型:那既会把一个从没跑过的模型 id 写进
		// assistantMsg.Model,又会让 chat_svc 把「观测不到」误判成「所选模型未生效」
		// —— 每一轮都误报。
		Convey("Given app-server does not report model, when a provider model is configured, then it is reported instead of the hardcoded default", func() {
			restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
				return &fakeRuntimeSession{stream: &emptyRuntimeStream{}, sid: "thread-silent"}, nil
			})
			defer restore()

			events, result, err := New().Run(context.Background(), agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{
					Type:    string(agent_backend_entity.TypeCodex),
					EnvJSON: "{}",
				},
				Effective: &agentruntime.EffectiveLLMConfig{ModelID: "glm-4.6"},
				SessionID: 1,
				Cwd:       t.TempDir(),
				UserText:  "hello",
			})
			So(err, ShouldBeNil)
			for range events {
			}

			So(result.Model, ShouldEqual, "glm-4.6")
		})

		Convey("Given the thread reports its actual model (sess.Model), then RunResult.Model reports the thread actual", func() {
			restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
				return &fakeRuntimeSession{stream: &emptyRuntimeStream{}, sid: "thread-actual", model: "gpt-5.5"}, nil
			})
			defer restore()

			events, result, err := New().Run(context.Background(), agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{
					Type:    string(agent_backend_entity.TypeCodex),
					EnvJSON: "{}",
				},
				Effective: &agentruntime.EffectiveLLMConfig{ModelID: "gpt-5.4"},
				SessionID: 1,
				Cwd:       t.TempDir(),
				UserText:  "hello",
			})
			So(err, ShouldBeNil)
			for range events {
			}

			So(result.Model, ShouldEqual, "gpt-5.5")
		})
	})
}

// TestRun_ModelChangeEvictsAndRespawns 锁住 codex 的模型 evict 语义:app-server 进程会
// 被 CLISessionPool 跨轮复用,而 WithModel 绑定在 Client 创建时 —— 解析出的 ModelID 变了
// (会话选供应商换绑定、Provider 默认模型变化等)必须 evict + 重 spawn,否则下一轮复用池里
// 旧模型进程,模型不生效 (RunResult.Model 仍旧模型)。#26 会话级 override 已移除,
// 模型变化只看解析出的 ModelID。
func TestRun_ModelChangeEvictsAndRespawns(t *testing.T) {
	Convey("Given 同一 codex 会话两轮解析出的 ModelID 不同", t, func() {
		var spawnCount int32
		restore := SetSessionFactoryForTest(func(req agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			atomic.AddInt32(&spawnCount, 1)
			model := "gpt-5.5"
			if req.Effective != nil {
				model = req.Effective.ModelID
			}
			return &fakeRuntimeSession{stream: &emptyRuntimeStream{}, sid: "thread-x", model: model}, nil
		})
		defer restore()

		r := New()
		run := func(providerModel string) *agentruntime.RunResult {
			events, result, err := r.Run(context.Background(), agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{
					Type:    string(agent_backend_entity.TypeCodex),
					EnvJSON: "{}",
				},
				Effective: &agentruntime.EffectiveLLMConfig{ModelID: providerModel},
				SessionID: 77,
				Cwd:       t.TempDir(),
				UserText:  "hi",
			})
			So(err, ShouldBeNil)
			for range events {
			}
			return result
		}

		Convey("When 首轮 provider.Model=A, Then 线程模型为 A", func() {
			So(run("gpt-5.5").Model, ShouldEqual, "gpt-5.5")
			So(atomic.LoadInt32(&spawnCount), ShouldEqual, 1)
		})

		Convey("When 同模型再来一轮, Then 复用不重 spawn", func() {
			run("gpt-5.5")
			run("gpt-5.5")
			So(atomic.LoadInt32(&spawnCount), ShouldEqual, 1)
		})

		Convey("When 第二轮 provider.Model 变化为 B, Then evict + 重 spawn,线程模型为 B", func() {
			run("gpt-5.5")
			second := run("gpt-5.6")
			So(second.Model, ShouldEqual, "gpt-5.6")
			So(atomic.LoadInt32(&spawnCount), ShouldEqual, 2)
		})
	})
}

// TestRun_ProviderChangeEvictsAndRespawns 锁住 codex 的 evict 比对键扩展(spec
// 2026-08-10 决策 4):model_provider/base_url 同是启动期 -c 覆盖项(WithModel 绑定在
// Client 创建时),两个不同供应商可以配同一个 model id,只比解析出的 ModelID 会漏掉换
// 供应商 —— 必须把 effectiveProviderKey 也纳入比对,否则会话切换供应商后复用池里的旧
// app-server 进程,请求仍打旧供应商。
func TestRun_ProviderChangeEvictsAndRespawns(t *testing.T) {
	Convey("Given 同一 codex 会话两轮解析出的 ModelID 相同但 ProviderKey 不同", t, func() {
		var spawnCount int32
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			atomic.AddInt32(&spawnCount, 1)
			return &fakeRuntimeSession{stream: &emptyRuntimeStream{}, sid: "thread-y", model: "same-model"}, nil
		})
		defer restore()

		r := New()
		run := func(providerKey string) {
			events, _, err := r.Run(context.Background(), agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{
					Type:    string(agent_backend_entity.TypeCodex),
					EnvJSON: "{}",
				},
				Effective: &agentruntime.EffectiveLLMConfig{ProviderKey: providerKey, ModelID: "same-model"},
				SessionID: 78,
				Cwd:       t.TempDir(),
				UserText:  "hi",
			})
			So(err, ShouldBeNil)
			for range events {
			}
		}

		Convey("When 同供应商再来一轮, Then 复用不重 spawn", func() {
			run("provider-a")
			run("provider-a")
			So(atomic.LoadInt32(&spawnCount), ShouldEqual, 1)
		})

		Convey("When ProviderKey 变化但 model 不变, Then evict + 重 spawn", func() {
			run("provider-a")
			run("provider-b")
			So(atomic.LoadInt32(&spawnCount), ShouldEqual, 2)
		})
	})
}

// TestRun_ModelKeyChangeEvictsAndResumes locks the approved launch identity:
// ProviderKey + ModelKey + resolved ModelID. Stable ProviderKey/ModelID do not
// permit reuse when the persisted fixed-model identity changed; the rebuilt
// app-server must receive the existing native thread ID for context continuity.
func TestRun_ModelKeyChangeEvictsAndResumes(t *testing.T) {
	Convey("Given the same Codex ProviderKey and ModelID with a different ModelKey", t, func() {
		var (
			spawnCount int32
			resumeIDs  []string
		)
		restore := SetSessionFactoryForTest(func(req agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			atomic.AddInt32(&spawnCount, 1)
			resumeIDs = append(resumeIDs, req.ProviderSessionID)
			return &fakeRuntimeSession{stream: &emptyRuntimeStream{}, sid: "native-codex-thread", model: "same-model"}, nil
		})
		defer restore()

		r := New()
		run := func(modelKey, providerSessionID string) {
			events, _, err := r.Run(context.Background(), agentruntime.RunRequest{
				Backend:           &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex), EnvJSON: "{}"},
				Effective:         &agentruntime.EffectiveLLMConfig{ProviderKey: "provider-a", ModelKey: modelKey, ModelID: "same-model"},
				SessionID:         79,
				ProviderSessionID: providerSessionID,
				Cwd:               t.TempDir(),
				UserText:          "hi",
			})
			So(err, ShouldBeNil)
			for range events {
			}
		}

		run("model-a", "")
		run("model-b", "native-codex-thread")

		So(atomic.LoadInt32(&spawnCount), ShouldEqual, 2)
		So(resumeIDs, ShouldResemble, []string{"", "native-codex-thread"})
	})
}

func TestSetGoal_CreatesProviderThreadBeforeFirstTurn(t *testing.T) {
	Convey("Given a Codex chat session has no provider thread yet, when setting a goal, then runtime starts a session and returns the created thread id", t, func() {
		fake := &fakeRuntimeSession{}
		restore := SetSessionFactoryForTest(func(req agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			So(req.ProviderSessionID, ShouldEqual, "")
			So(req.SessionID, ShouldEqual, int64(42))
			return fake, nil
		})
		defer restore()

		objective := "ship before first turn"
		status := "active"
		goal, err := New().SetGoal(context.Background(), agentruntime.GoalRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type:    string(agent_backend_entity.TypeCodex),
				EnvJSON: "{}",
			},
			AgentID:   7,
			SessionID: 42,
			Cwd:       t.TempDir(),
			Objective: &objective,
			Status:    &status,
		})

		So(err, ShouldBeNil)
		So(goal, ShouldNotBeNil)
		So(goal.ThreadID, ShouldEqual, "thread-created-for-goal")
		So(goal.Objective, ShouldEqual, "ship before first turn")
		So(fake.setGoalReq.Objective, ShouldNotBeNil)
		So(*fake.setGoalReq.Objective, ShouldEqual, "ship before first turn")
	})
}

func TestSetGoal_ReleasesOneShotSessionToIdle(t *testing.T) {
	Convey("Given /goal only performs a one-shot Codex RPC, when SetGoal returns, then the cached CLI session is idle for the next turn", t, func() {
		pool := agentruntime.NewCLISessionPool(8)
		fake := &fakeRuntimeSession{}
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			return fake, nil
		})
		defer restore()

		objective := "ship without starting a turn"
		status := "active"
		goal, err := NewWithPool(pool).SetGoal(context.Background(), agentruntime.GoalRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type:    string(agent_backend_entity.TypeCodex),
				EnvJSON: "{}",
			},
			AgentID:   7,
			SessionID: 42,
			Cwd:       t.TempDir(),
			Objective: &objective,
			Status:    &status,
		})

		So(err, ShouldBeNil)
		So(goal, ShouldNotBeNil)
		So(pool.Len(), ShouldEqual, 1)
		So(pool.IdleLen(), ShouldEqual, 1)
	})
}

func TestSetGoal_KeepsActiveTurnSessionActive(t *testing.T) {
	Convey("Given a Codex turn is active, when SetGoal runs against the same session, then the cached CLI session is not marked idle", t, func() {
		pool := agentruntime.NewCLISessionPool(8)
		stream := newBlockingRuntimeStream()
		fake := &fakeRuntimeSession{stream: stream, sid: "thread-active"}
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			return fake, nil
		})
		defer restore()

		r := NewWithPool(pool)
		events, _, err := r.Run(context.Background(), agentruntime.RunRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type:    string(agent_backend_entity.TypeCodex),
				EnvJSON: "{}",
			},
			SessionID: 42,
			Cwd:       t.TempDir(),
			UserText:  "run",
		})
		So(err, ShouldBeNil)
		defer func() {
			stream.finish()
			for range events {
			}
		}()

		objective := "update while active"
		status := "active"
		goal, err := r.SetGoal(context.Background(), agentruntime.GoalRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type:    string(agent_backend_entity.TypeCodex),
				EnvJSON: "{}",
			},
			AgentID:           7,
			SessionID:         42,
			ProviderSessionID: "thread-active",
			Cwd:               t.TempDir(),
			Objective:         &objective,
			Status:            &status,
		})

		So(err, ShouldBeNil)
		So(goal, ShouldNotBeNil)
		So(pool.Len(), ShouldEqual, 1)
		So(pool.IdleLen(), ShouldEqual, 0)
	})
}

func TestRun_ReusesCachedSessionAcrossTurns(t *testing.T) {
	Convey("Given a Codex chat session is idle after one turn, when Run is called again, then the cached CLI session is reused", t, func() {
		pool := agentruntime.NewCLISessionPool(8)
		cached := &countingRuntimeSession{
			sid: "thread-cached",
			streams: []cxStream{
				&emptyRuntimeStream{},
				&emptyRuntimeStream{},
			},
		}
		factoryCalls := 0
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			factoryCalls++
			return cached, nil
		})
		defer restore()

		r := NewWithPool(pool)
		req := agentruntime.RunRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type:    string(agent_backend_entity.TypeCodex),
				EnvJSON: "{}",
			},
			SessionID: 77,
			Cwd:       t.TempDir(),
		}

		events, _, err := r.Run(context.Background(), req)
		So(err, ShouldBeNil)
		for range events {
		}

		req.UserText = "again"
		events, _, err = r.Run(context.Background(), req)
		So(err, ShouldBeNil)
		for range events {
		}

		So(factoryCalls, ShouldEqual, 1)
		So(cached.streamCalls, ShouldEqual, 2)
		So(cached.closed, ShouldBeFalse)
		So(pool.Len(), ShouldEqual, 1)
		So(pool.IdleLen(), ShouldEqual, 1)
	})
}

func TestRun_MCPServersBypassCachedSession(t *testing.T) {
	Convey("Given a Codex chat session has an idle app-server without MCP, when a group turn injects MCPServers, then runtime starts a fresh app-server with MCP config", t, func() {
		pool := agentruntime.NewCLISessionPool(8)
		first := &countingRuntimeSession{
			sid:     "thread-cached",
			streams: []cxStream{&emptyRuntimeStream{}},
		}
		second := &countingRuntimeSession{
			sid:      "thread-cached",
			streams:  []cxStream{&emptyRuntimeStream{}},
			closedCh: make(chan struct{}),
		}
		factoryCalls := 0
		var secondReq agentruntime.RunRequest
		restore := SetSessionFactoryForTest(func(req agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			factoryCalls++
			if factoryCalls == 1 {
				return first, nil
			}
			secondReq = req
			return second, nil
		})
		defer restore()

		r := NewWithPool(pool)
		req := agentruntime.RunRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type:    string(agent_backend_entity.TypeCodex),
				EnvJSON: "{}",
			},
			SessionID: 77,
			Cwd:       t.TempDir(),
		}

		events, _, err := r.Run(context.Background(), req)
		So(err, ShouldBeNil)
		for range events {
		}

		req.MCPServers = []agentruntime.MCPServerSpec{{
			Name:  "group",
			URL:   "http://127.0.0.1:9000/mcp/group/",
			Tools: []string{"group_send"},
		}}
		events, _, err = r.Run(context.Background(), req)
		So(err, ShouldBeNil)
		for range events {
		}

		So(factoryCalls, ShouldEqual, 2)
		So(first.streamCalls, ShouldEqual, 1)
		So(second.streamCalls, ShouldEqual, 1)
		So(secondReq.MCPServers, ShouldHaveLength, 1)
		So(pool.Len(), ShouldEqual, 1)
		second.waitClosed(t)
		So(second.closed, ShouldBeTrue)
	})
}

func TestRun_EnabledPluginsBypassCachedSession(t *testing.T) {
	Convey("Given a Codex chat session has an idle app-server without plugin overrides, when a turn injects EnabledPlugins, then runtime starts a fresh app-server with those overrides", t, func() {
		pool := agentruntime.NewCLISessionPool(8)
		first := &countingRuntimeSession{
			sid:     "thread-cached",
			streams: []cxStream{&emptyRuntimeStream{}, &emptyRuntimeStream{}},
		}
		second := &countingRuntimeSession{
			sid:      "thread-cached",
			streams:  []cxStream{&emptyRuntimeStream{}},
			closedCh: make(chan struct{}),
		}
		factoryCalls := 0
		var secondReq agentruntime.RunRequest
		restore := SetSessionFactoryForTest(func(req agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			factoryCalls++
			if factoryCalls == 1 {
				return first, nil
			}
			secondReq = req
			return second, nil
		})
		defer restore()

		r := NewWithPool(pool)
		req := agentruntime.RunRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type:    string(agent_backend_entity.TypeCodex),
				EnvJSON: "{}",
			},
			SessionID: 77,
			Cwd:       t.TempDir(),
		}

		events, _, err := r.Run(context.Background(), req)
		So(err, ShouldBeNil)
		for range events {
		}

		req.EnabledPlugins = map[string]bool{
			"browser@openai-bundled":           true,
			"superpowers@openai-curated":       false,
			"documents@openai-primary-runtime": true,
		}
		events, _, err = r.Run(context.Background(), req)
		So(err, ShouldBeNil)
		for range events {
		}

		So(factoryCalls, ShouldEqual, 2)
		So(first.streamCalls, ShouldEqual, 1)
		So(second.streamCalls, ShouldEqual, 1)
		So(secondReq.EnabledPlugins, ShouldHaveLength, 3)
		So(pool.Len(), ShouldEqual, 1)
		second.waitClosed(t)
		So(second.closed, ShouldBeTrue)
	})
}

func TestCloseSession_RemovesCachedCodexSession(t *testing.T) {
	Convey("Given a cached idle Codex CLI session, when CloseSession is called, then the session is closed and evicted", t, func() {
		pool := agentruntime.NewCLISessionPool(8)
		cached := &countingRuntimeSession{
			sid:      "thread-close",
			streams:  []cxStream{&emptyRuntimeStream{}},
			closedCh: make(chan struct{}),
		}
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			return cached, nil
		})
		defer restore()

		r := NewWithPool(pool)
		events, _, err := r.Run(context.Background(), agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex), EnvJSON: "{}"},
			SessionID: 88,
			Cwd:       t.TempDir(),
		})
		So(err, ShouldBeNil)
		for range events {
		}
		So(pool.Len(), ShouldEqual, 1)

		r.CloseSession(context.Background(), 88)

		cached.waitClosed(t)
		So(cached.closed, ShouldBeTrue)
		So(pool.Len(), ShouldEqual, 0)
	})
}

func TestRun_EmitsContextWindowUpdateFromTokenUsage(t *testing.T) {
	Convey("codex runtime 在 token usage 帧上报 modelContextWindow 时实时 emit ContextWindowUpdated", t, func() {
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			return &fakeRuntimeSession{stream: &eventRuntimeStream{
				events: []pkgcodex.Event{
					{
						Kind:          pkgcodex.EventUsage,
						ContextWindow: 258400,
						Usage: provider.Usage{
							PromptTokens:     100,
							CompletionTokens: 20,
						},
					},
					{
						Kind:          pkgcodex.EventUsage,
						ContextWindow: 258400,
						Usage: provider.Usage{
							PromptTokens:     120,
							CompletionTokens: 30,
						},
					},
				},
			}, sid: "thread-cw"}, nil
		})
		defer restore()

		events, result, err := New().Run(context.Background(), agentruntime.RunRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type:    string(agent_backend_entity.TypeCodex),
				EnvJSON: "{}",
			},
			SessionID: 1,
			Cwd:       t.TempDir(),
			UserText:  "hello",
		})
		So(err, ShouldBeNil)

		var contextWindows []agentruntime.ContextWindowUpdated
		var usages []agentruntime.UsageUpdate
		for ev := range events {
			switch e := ev.(type) {
			case agentruntime.ContextWindowUpdated:
				contextWindows = append(contextWindows, e)
			case agentruntime.UsageUpdate:
				usages = append(usages, e)
			}
		}

		So(contextWindows, ShouldHaveLength, 1)
		So(contextWindows[0].Tokens, ShouldEqual, 258400)
		So(usages, ShouldHaveLength, 2)
		So(result.ContextWindow, ShouldEqual, 258400)
	})
}

func TestRun_ErrorFollowedByProgressClearsStopErr(t *testing.T) {
	Convey("codex runtime: EventError 后还有进展事件和完成时, StopErr 不应污染成功 turn", t, func() {
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			return &fakeRuntimeSession{stream: &eventRuntimeStream{
				events: []pkgcodex.Event{
					{Kind: pkgcodex.EventError, Err: errors.New("temporary upstream hiccup")},
					{Kind: pkgcodex.EventTextDelta, Text: "recovered"},
					{Kind: pkgcodex.EventDone},
				},
			}, sid: "thread-recovered"}, nil
		})
		defer restore()

		events, result, err := New().Run(context.Background(), agentruntime.RunRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type:    string(agent_backend_entity.TypeCodex),
				EnvJSON: "{}",
			},
			SessionID: 1,
			Cwd:       t.TempDir(),
			UserText:  "hello",
		})
		So(err, ShouldBeNil)

		var text string
		for ev := range events {
			if td, ok := ev.(agentruntime.TextDelta); ok {
				text += td.Text
			}
		}

		So(text, ShouldEqual, "recovered")
		So(result.StopErr, ShouldBeNil)
	})
}

func TestRun_ErrorFollowedOnlyByMetadataKeepsStopErr(t *testing.T) {
	Convey("codex runtime: EventError 后只有 metadata 和完成时, StopErr 仍应保留", t, func() {
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			return &fakeRuntimeSession{stream: &eventRuntimeStream{
				events: []pkgcodex.Event{
					{Kind: pkgcodex.EventError, Err: errors.New("temporary upstream hiccup")},
					{Kind: pkgcodex.EventUsage},
					{Kind: pkgcodex.EventDone},
				},
			}, sid: "thread-failed"}, nil
		})
		defer restore()

		events, result, err := New().Run(context.Background(), agentruntime.RunRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type:    string(agent_backend_entity.TypeCodex),
				EnvJSON: "{}",
			},
			SessionID: 1,
			Cwd:       t.TempDir(),
			UserText:  "hello",
		})
		So(err, ShouldBeNil)
		for range events {
		}

		So(result.StopErr, ShouldNotBeNil)
		So(result.StopErr.Error(), ShouldContainSubstring, "temporary upstream hiccup")
	})
}

func TestRun_DuplicateSessionTurnDoesNotReplaceActiveOwner(t *testing.T) {
	Convey("Given a Codex turn owns a chat session, when another Run starts for the same session, then it is rejected without replacing the first owner", t, func() {
		pool := agentruntime.NewCLISessionPool(8)
		stream := newBlockingRuntimeStream()
		fake := &fakeRuntimeSession{stream: stream, sid: "thread-owner"}
		factoryCalls := 0
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			factoryCalls++
			return fake, nil
		})
		defer restore()

		r := NewWithPool(pool)
		req := agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex), EnvJSON: "{}"},
			SessionID: 707,
			Cwd:       t.TempDir(),
			UserText:  "first",
		}
		firstEvents, _, err := r.Run(context.Background(), req)
		So(err, ShouldBeNil)

		req.UserText = "must not overlap"
		secondEvents, _, secondErr := r.Run(context.Background(), req)

		So(secondErr, ShouldNotBeNil)
		So(secondErr.Error(), ShouldContainSubstring, "active turn")
		So(secondEvents, ShouldBeNil)
		So(factoryCalls, ShouldEqual, 1)

		stream.finish()
		for range firstEvents {
		}
	})
}

func TestRuntimeUnregister_StaleOwnerCannotDeleteReplacement(t *testing.T) {
	Convey("Given a replacement owner is installed, when an old turn defers unregister, then only the expected owner can be removed", t, func() {
		r := New()
		oldOwner := &codexActive{}
		newOwner := &codexActive{}
		r.active[11] = newOwner

		r.unregister(11, oldOwner)

		So(r.active[11], ShouldEqual, newOwner)
		r.unregister(11, newOwner)
		_, exists := r.active[11]
		So(exists, ShouldBeFalse)
	})
}

func TestRun_ControlCallsAreRaceFreeWhileActiveOwnerInitializes(t *testing.T) {
	// Given Run has claimed the session but the app-server has not returned its
	// stream yet, control calls may concurrently observe that provisional owner.
	stream := &interruptibleBlockingRuntimeStream{blockingRuntimeStream: newBlockingRuntimeStream()}
	sess := &blockingStartRuntimeSession{
		fakeRuntimeSession: &fakeRuntimeSession{stream: stream, sid: "thread-initializing"},
		entered:            make(chan struct{}),
		release:            make(chan struct{}),
	}
	restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
		return sess, nil
	})
	defer restore()

	r := New()
	type runResult struct {
		events <-chan agentruntime.Event
		err    error
	}
	runDone := make(chan runResult, 1)
	go func() {
		events, _, err := r.Run(context.Background(), agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex), EnvJSON: "{}"},
			SessionID: 707,
			Cwd:       t.TempDir(),
			UserText:  "initialize",
		})
		runDone <- runResult{events: events, err: err}
	}()

	<-sess.entered
	stopControls := make(chan struct{})
	controlsDone := make(chan struct{})
	go func() {
		defer close(controlsDone)
		for {
			select {
			case <-stopControls:
				return
			default:
				_, _ = r.Abort(context.Background(), 707, 0)
			}
		}
	}()
	close(sess.release)
	result := <-runDone
	close(stopControls)
	<-controlsDone
	if result.err != nil {
		t.Fatalf("Run failed after initialization: %v", result.err)
	}
	stream.finish()
	for range result.events {
	}
}

func TestRun_FailedCachedSessionIsEvictedBeforeNextTurn(t *testing.T) {
	Convey("Given a cached Codex process fails to start a turn, when the next turn runs, then a fresh session is created", t, func() {
		pool := agentruntime.NewCLISessionPool(8)
		bad := &startFailRuntimeSession{sid: "thread-dead", err: pkgcodex.ErrProcessDead}
		good := &countingRuntimeSession{sid: "thread-restarted", streams: []cxStream{&emptyRuntimeStream{}}}
		factoryCalls := 0
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			factoryCalls++
			if factoryCalls == 1 {
				return bad, nil
			}
			return good, nil
		})
		defer restore()

		r := NewWithPool(pool)
		req := agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex), EnvJSON: "{}"},
			SessionID: 808,
			Cwd:       t.TempDir(),
		}
		_, _, firstErr := r.Run(context.Background(), req)
		So(firstErr, ShouldNotBeNil)

		events, _, secondErr := r.Run(context.Background(), req)
		So(secondErr, ShouldBeNil)
		for range events {
		}
		So(factoryCalls, ShouldEqual, 2)
		So(good.streamCalls, ShouldEqual, 1)
	})
}

func TestResolvedEventDelivery_DoesNotDropWhenOutputBufferIsFull(t *testing.T) {
	Convey("Given the runtime event buffer is full, when a request resolves, then the state event waits for delivery instead of being silently dropped", t, func() {
		out := make(chan agentruntime.Event, 1)
		out <- agentruntime.TextDelta{Text: "occupy"}
		active := &codexActive{}
		active.setOut(out)
		delivered := make(chan struct{})

		go func() {
			_ = emitUserAskResolved(active, "request-1", true, nil)
			close(delivered)
		}()

		select {
		case <-delivered:
			So("resolved event returned before there was capacity", ShouldBeBlank)
		case <-time.After(30 * time.Millisecond):
		}
		<-out
		select {
		case <-delivered:
		case <-time.After(time.Second):
			So("resolved event was never delivered", ShouldBeBlank)
		}
		resolved := <-out
		_, ok := resolved.(agentruntime.UserAskResolved)
		So(ok, ShouldBeTrue)
	})
}

func TestSubmitResolution_DoesNotAnswerAfterRuntimeOutputClosed(t *testing.T) {
	Convey("Given the turn output is already closed, when user input is submitted, then backend response and waiter state stay untouched", t, func() {
		backend := &recordingUserInputStream{}
		active := &codexActive{userInput: backend}
		active.registerAskWaiter("input-closed", []agentruntime.AskQuestion{{ID: "q1", Question: "Continue?"}})
		r := New()
		r.active[911] = active

		err := r.SubmitAnswer(context.Background(), 911, "input-closed", nil, nil, true)

		So(err, ShouldEqual, agentruntime.ErrNoActiveTurn)
		So(backend.called, ShouldBeFalse)
		So(active.askWaiter("input-closed"), ShouldNotBeNil)
	})

	Convey("Given the turn output is already closed, when approval is submitted, then backend response and waiter state stay untouched", t, func() {
		backend := newApprovalRuntimeStream(pkgcodex.Event{})
		active := &codexActive{approval: backend}
		active.registerPermWaiter("approval-closed", "shell", json.RawMessage(`{}`))
		r := New()
		r.active[912] = active

		err := r.SubmitToolPermission(context.Background(), 912, "approval-closed", true, false, "")

		So(err, ShouldEqual, agentruntime.ErrNoActiveTurn)
		So(backend.submittedRequestID, ShouldBeBlank)
		So(active.hasPermWaiter("approval-closed"), ShouldBeTrue)
	})
}

func TestRun_ServerResolvedRequestsConvergeRuntimeWaiters(t *testing.T) {
	Convey("Given request_user_input is pending, when app-server resolves it externally, then runtime emits a skipped resolution and releases waiting state", t, func() {
		pool := agentruntime.NewCLISessionPool(8)
		defer pool.RemoveAll()
		stream := newBlockingEventRuntimeStream(
			pkgcodex.Event{
				Kind: pkgcodex.EventRequestUserInput,
				RequestUserInput: &pkgcodex.RequestUserInputEvent{
					RequestID: "input-auto-resolved",
					ItemID:    "question-item",
					Questions: []pkgcodex.RequestUserInputQuestion{{
						ID:       "question-1",
						Header:   "Choice",
						Question: "Continue?",
					}},
				},
			},
			pkgcodex.Event{
				Kind: pkgcodex.EventRequestResolved,
				RequestResolved: &pkgcodex.RequestResolvedEvent{
					RequestID: "input-auto-resolved",
					Kind:      pkgcodex.RequestKindUserInput,
				},
			},
		)
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			return &fakeRuntimeSession{stream: stream, sid: "thread-input-resolved"}, nil
		})
		defer restore()

		r := NewWithPool(pool)
		events, _, err := r.Run(context.Background(), agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex), EnvJSON: "{}"},
			SessionID: 901,
			Cwd:       t.TempDir(),
			UserText:  "ask",
		})
		So(err, ShouldBeNil)

		request, ok := (<-events).(agentruntime.UserAskRequest)
		So(ok, ShouldBeTrue)
		So(request.RequestID, ShouldEqual, "input-auto-resolved")
		resolved, ok := (<-events).(agentruntime.UserAskResolved)
		So(ok, ShouldBeTrue)
		So(resolved.RequestID, ShouldEqual, "input-auto-resolved")
		So(resolved.Skipped, ShouldBeTrue)

		r.mu.Lock()
		active := r.active[901]
		r.mu.Unlock()
		if active == nil {
			t.Fatal("runtime removed the active turn before the request resolution was observed")
		}
		So(active.askWaiter("input-auto-resolved"), ShouldBeNil)
		stream.finish()
		for range events {
		}
		So(pool.IdleLen(), ShouldEqual, 1)
	})

	Convey("Given approval is pending, when app-server resolves it externally, then runtime emits a neutral denial and releases waiting state", t, func() {
		pool := agentruntime.NewCLISessionPool(8)
		defer pool.RemoveAll()
		stream := newBlockingEventRuntimeStream(
			pkgcodex.Event{
				Kind: pkgcodex.EventApprovalRequest,
				Approval: &pkgcodex.ApprovalRequestEvent{
					RequestID: "approval-auto-resolved",
					ItemID:    "command-item",
					ToolName:  "Bash",
					Input:     []byte(`{"command":"pwd"}`),
				},
			},
			pkgcodex.Event{
				Kind: pkgcodex.EventRequestResolved,
				RequestResolved: &pkgcodex.RequestResolvedEvent{
					RequestID: "approval-auto-resolved",
					Kind:      pkgcodex.RequestKindApproval,
				},
			},
		)
		restore := SetSessionFactoryForTest(func(_ agentruntime.RunRequest, _ map[string]string, _ string) (cxSessionHandle, error) {
			return &fakeRuntimeSession{stream: stream, sid: "thread-approval-resolved"}, nil
		})
		defer restore()

		r := NewWithPool(pool)
		events, _, err := r.Run(context.Background(), agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex), EnvJSON: "{}"},
			SessionID: 902,
			Cwd:       t.TempDir(),
			UserText:  "run",
		})
		So(err, ShouldBeNil)

		request, ok := (<-events).(agentruntime.ToolPermissionRequest)
		So(ok, ShouldBeTrue)
		So(request.RequestID, ShouldEqual, "approval-auto-resolved")
		resolved, ok := (<-events).(agentruntime.ToolPermissionResolved)
		So(ok, ShouldBeTrue)
		So(resolved.RequestID, ShouldEqual, "approval-auto-resolved")
		So(resolved.Allowed, ShouldBeFalse)
		So(resolved.AlwaysAllow, ShouldBeFalse)
		So(resolved.DenyReason, ShouldEqual, "approval request resolved by Codex app-server without a decision")

		r.mu.Lock()
		active := r.active[902]
		r.mu.Unlock()
		if active == nil {
			t.Fatal("runtime removed the active turn before the approval resolution was observed")
		}
		So(active.hasPermWaiter("approval-auto-resolved"), ShouldBeFalse)
		stream.finish()
		for range events {
		}
		So(pool.IdleLen(), ShouldEqual, 1)
	})
}

type fakeRuntimeSession struct {
	stream cxStream
	sid    string
	model  string

	setGoalReq pkgcodex.GoalUpdate
}

type blockingStartRuntimeSession struct {
	*fakeRuntimeSession
	entered chan struct{}
	release chan struct{}
}

func (s *blockingStartRuntimeSession) Stream(context.Context, string, string) (cxStream, error) {
	close(s.entered)
	<-s.release
	return s.stream, nil
}

type startFailRuntimeSession struct {
	sid string
	err error
}

func (s *startFailRuntimeSession) Close(context.Context) error { return nil }
func (s *startFailRuntimeSession) ID() string                  { return s.sid }
func (*startFailRuntimeSession) Model() string                 { return "" }
func (s *startFailRuntimeSession) Stream(context.Context, string, string) (cxStream, error) {
	return nil, s.err
}
func (s *startFailRuntimeSession) StreamInput(context.Context, []pkgcodex.UserInput, string) (cxStream, error) {
	return nil, s.err
}
func (s *startFailRuntimeSession) Compact(context.Context) (cxStream, error) {
	return nil, s.err
}
func (*startFailRuntimeSession) GetGoal(context.Context) (*pkgcodex.Goal, error) { return nil, nil }
func (*startFailRuntimeSession) SetGoal(context.Context, pkgcodex.GoalUpdate) (*pkgcodex.Goal, error) {
	return nil, nil
}
func (*startFailRuntimeSession) ClearGoal(context.Context) (bool, error) { return true, nil }
func (s *startFailRuntimeSession) RewindTo(context.Context, string) (string, error) {
	return s.sid, nil
}
func (*startFailRuntimeSession) ActiveStream() cxSteerStream        { return nil }
func (*startFailRuntimeSession) ActiveInterruptor() cxInterruptable { return nil }

func (s *fakeRuntimeSession) Close(context.Context) error { return nil }
func (s *fakeRuntimeSession) ID() string                  { return s.sid }
func (s *fakeRuntimeSession) Model() string               { return s.model }
func (s *fakeRuntimeSession) Stream(context.Context, string, string) (cxStream, error) {
	return s.stream, nil
}
func (s *fakeRuntimeSession) StreamInput(context.Context, []pkgcodex.UserInput, string) (cxStream, error) {
	return s.stream, nil
}
func (s *fakeRuntimeSession) Compact(context.Context) (cxStream, error)       { return s.stream, nil }
func (s *fakeRuntimeSession) GetGoal(context.Context) (*pkgcodex.Goal, error) { return nil, nil }
func (s *fakeRuntimeSession) SetGoal(_ context.Context, req pkgcodex.GoalUpdate) (*pkgcodex.Goal, error) {
	s.setGoalReq = req
	if s.sid == "" {
		s.sid = "thread-created-for-goal"
	}
	objective := ""
	if req.Objective != nil {
		objective = *req.Objective
	}
	status := pkgcodex.GoalStatus("")
	if req.Status != nil {
		status = *req.Status
	}
	return &pkgcodex.Goal{ThreadID: s.sid, Objective: objective, Status: status}, nil
}
func (s *fakeRuntimeSession) ClearGoal(context.Context) (bool, error)          { return true, nil }
func (s *fakeRuntimeSession) RewindTo(context.Context, string) (string, error) { return s.sid, nil }
func (s *fakeRuntimeSession) ActiveStream() cxSteerStream                      { return nil }
func (s *fakeRuntimeSession) ActiveInterruptor() cxInterruptable               { return nil }

type countingRuntimeSession struct {
	streams     []cxStream
	sid         string
	streamCalls int
	closed      bool
	closedCh    chan struct{}
}

func (s *countingRuntimeSession) Close(context.Context) error {
	if !s.closed {
		s.closed = true
		if s.closedCh != nil {
			close(s.closedCh)
		}
	}
	return nil
}
func (s *countingRuntimeSession) ID() string { return s.sid }
func (s *countingRuntimeSession) Model() string {
	return ""
}
func (s *countingRuntimeSession) Stream(context.Context, string, string) (cxStream, error) {
	stream := s.streams[s.streamCalls]
	s.streamCalls++
	return stream, nil
}
func (s *countingRuntimeSession) StreamInput(context.Context, []pkgcodex.UserInput, string) (cxStream, error) {
	return s.Stream(context.Background(), "", "")
}
func (s *countingRuntimeSession) Compact(context.Context) (cxStream, error) {
	return s.Stream(context.Background(), "", "")
}
func (s *countingRuntimeSession) GetGoal(context.Context) (*pkgcodex.Goal, error) { return nil, nil }
func (s *countingRuntimeSession) SetGoal(context.Context, pkgcodex.GoalUpdate) (*pkgcodex.Goal, error) {
	return nil, nil
}
func (s *countingRuntimeSession) ClearGoal(context.Context) (bool, error)          { return true, nil }
func (s *countingRuntimeSession) RewindTo(context.Context, string) (string, error) { return s.sid, nil }
func (s *countingRuntimeSession) ActiveStream() cxSteerStream                      { return nil }
func (s *countingRuntimeSession) ActiveInterruptor() cxInterruptable               { return nil }

func (s *countingRuntimeSession) waitClosed(t *testing.T) {
	t.Helper()
	select {
	case <-s.closedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("cached codex session was not closed")
	}
}

type emptyRuntimeStream struct{}

func (*emptyRuntimeStream) Next() bool            { return false }
func (*emptyRuntimeStream) Event() pkgcodex.Event { return pkgcodex.Event{} }
func (*emptyRuntimeStream) SessionID() string     { return "" }

type blockingRuntimeStream struct {
	done chan struct{}
}

func newBlockingRuntimeStream() *blockingRuntimeStream {
	return &blockingRuntimeStream{done: make(chan struct{})}
}

func (s *blockingRuntimeStream) Next() bool          { <-s.done; return false }
func (*blockingRuntimeStream) Event() pkgcodex.Event { return pkgcodex.Event{} }
func (*blockingRuntimeStream) SessionID() string     { return "" }
func (s *blockingRuntimeStream) finish()             { close(s.done) }

type interruptibleBlockingRuntimeStream struct {
	*blockingRuntimeStream
}

func (*interruptibleBlockingRuntimeStream) Interrupt(context.Context) error { return nil }

type eventRuntimeStream struct {
	events []pkgcodex.Event
	idx    int
}

func (s *eventRuntimeStream) Next() bool {
	if s.idx >= len(s.events) {
		return false
	}
	s.idx++
	return true
}

func (s *eventRuntimeStream) Event() pkgcodex.Event { return s.events[s.idx-1] }
func (s *eventRuntimeStream) SessionID() string     { return "" }

type blockingEventRuntimeStream struct {
	events []pkgcodex.Event
	idx    int
	done   chan struct{}
}

func newBlockingEventRuntimeStream(events ...pkgcodex.Event) *blockingEventRuntimeStream {
	return &blockingEventRuntimeStream{events: events, done: make(chan struct{})}
}

func (s *blockingEventRuntimeStream) Next() bool {
	if s.idx < len(s.events) {
		s.idx++
		return true
	}
	<-s.done
	return false
}

func (s *blockingEventRuntimeStream) Event() pkgcodex.Event { return s.events[s.idx-1] }
func (s *blockingEventRuntimeStream) SessionID() string     { return "" }
func (s *blockingEventRuntimeStream) finish()               { close(s.done) }

type approvalRuntimeStream struct {
	event pkgcodex.Event
	done  chan struct{}
	used  bool

	submittedRequestID string
	submittedAllow     bool
	submittedAlways    bool
}

func newApprovalRuntimeStream(ev pkgcodex.Event) *approvalRuntimeStream {
	return &approvalRuntimeStream{event: ev, done: make(chan struct{})}
}

func (s *approvalRuntimeStream) Next() bool {
	if !s.used {
		s.used = true
		return true
	}
	<-s.done
	return false
}

func (s *approvalRuntimeStream) Event() pkgcodex.Event { return s.event }
func (s *approvalRuntimeStream) SessionID() string     { return "" }

func (s *approvalRuntimeStream) SubmitApproval(_ context.Context, requestID string, allow, alwaysAllowSession bool) error {
	s.submittedRequestID = requestID
	s.submittedAllow = allow
	s.submittedAlways = alwaysAllowSession
	return nil
}

func (s *approvalRuntimeStream) finish() { close(s.done) }

type recordingUserInputStream struct {
	called bool
}

func (s *recordingUserInputStream) SubmitUserInput(context.Context, string, map[string][]string) error {
	s.called = true
	return nil
}

// TestCodexPendingWaiters 覆盖 R7 的 codex 一半:codex 的 Capabilities 里
// CapToolPermission=true(app-server requestApproval 协议),drainStream 会为
// ToolPermissionRequest / UserAskRequest 登记 waiter,所以断连期间产生的待决策
// 必须能被重连的客户端枚举出来并重建卡片,而不是回落成空列表 —— 后者叠加 R9
// (不设过期)就是会话永久卡在等待输入。
func TestCodexPendingWaiters(t *testing.T) {
	Convey("Given 一个审批和一个提问都在阻塞, When PendingWaiters, Then 快照带够重建卡片的载荷", t, func() {
		r := New()
		a := &codexActive{}
		r.mu.Lock()
		r.active[7001] = a
		r.mu.Unlock()

		a.registerPermWaiter("perm-1", "shell", json.RawMessage(`{"command":["ls"]}`))
		a.registerAskWaiter("ask-1", []agentruntime.AskQuestion{{Question: "继续吗？"}})

		snap := r.PendingWaiters(context.Background(), 7001)

		So(snap.ToolPermissions, ShouldHaveLength, 1)
		So(snap.ToolPermissions[0].RequestID, ShouldEqual, "perm-1")
		So(snap.ToolPermissions[0].ToolName, ShouldEqual, "shell")
		So(string(snap.ToolPermissions[0].Input), ShouldEqual, `{"command":["ls"]}`)

		So(snap.AskUserQuestions, ShouldHaveLength, 1)
		So(snap.AskUserQuestions[0].RequestID, ShouldEqual, "ask-1")
		So(snap.AskUserQuestions[0].Questions, ShouldResemble, []agentruntime.AskQuestion{{Question: "继续吗？"}})
	})

	Convey("Given 已经回答过的 requestID, When PendingWaiters, Then 它不再出现在快照里", t, func() {
		r := New()
		a := &codexActive{}
		r.mu.Lock()
		r.active[7002] = a
		r.mu.Unlock()

		a.registerPermWaiter("perm-1", "shell", json.RawMessage(`{}`))
		a.removePermWaiter("perm-1")

		So(r.PendingWaiters(context.Background(), 7002).ToolPermissions, ShouldBeEmpty)
	})

	Convey("Given sessionID 不在 active 表里(未起轮 / 已结束), When PendingWaiters, Then 返回空快照不报错不 panic", t, func() {
		r := New()
		So(func() {
			snap := r.PendingWaiters(context.Background(), 9999)
			So(snap.ToolPermissions, ShouldBeEmpty)
			So(snap.AskUserQuestions, ShouldBeEmpty)
		}, ShouldNotPanic)
	})
}

// TestRun_WebInitiatedFreeSessionResolvesCwdFromSyncID 与 claudecode / piagent 那两条同源
// (2026-08-22 的 AgentCwd(0) 报错):web 发起的对话在这里同样是 AgentID=0 + Cwd 空,
// 兜底目录改由账号级 AgentSyncID 定。
func TestRun_WebInitiatedFreeSessionResolvesCwdFromSyncID(t *testing.T) {
	Convey("Given 一条 web 发起的自由会话:AgentID=0、Cwd 空、只带账号级 AgentSyncID", t, func() {
		dataDir := t.TempDir()
		t.Setenv("AGENTRE_DATA_DIR", dataDir)

		var gotCwd string
		restore := SetSessionFactoryForTest(
			func(_ agentruntime.RunRequest, _ map[string]string, cwd string) (cxSessionHandle, error) {
				gotCwd = cwd
				return &fakeRuntimeSession{stream: &emptyRuntimeStream{}, sid: "thread-web"}, nil
			})
		defer restore()

		Convey("When 起这一轮, Then 起得来,工作目录落在该 Agent 的账号级同步标识下", func() {
			events, _, err := New().Run(context.Background(), agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{
					Type: string(agent_backend_entity.TypeCodex), EnvJSON: "{}",
				},
				SessionID:   1,
				AgentID:     0,
				AgentSyncID: "01KZNE7YKJQ6A79YVDCMW1A63R",
				UserText:    "hi",
			})
			So(err, ShouldBeNil)
			for range events { //nolint:revive // drain
			}
			So(gotCwd, ShouldEqual,
				filepath.Join(dataDir, "agents", "sync-01KZNE7YKJQ6A79YVDCMW1A63R"))
		})
	})
}

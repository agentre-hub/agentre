package claudecode

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/agents/provider"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/pkg/httpgateway"
	"github.com/agentre-hub/agentre/pkg/claudecode"
)

// TestRun_ModelChangeEvictsAndRespawns 锁住 claudecode 的模型 evict 语义:--model 是
// 启动期 flag(WithModel 绑定在 Client 创建时),CLI 子进程又会被 LRU 缓存跨轮复用 ——
// effectiveModel(解析出的 ModelID → backend.DefaultModel)变化必须 evict + 重 spawn,
// 否则下一轮复用旧模型进程,新模型不生效(镜像 codex runtime 的 modelChanged 先例)。
// #26 会话级 override 已移除,模型变化只看解析出的 ModelID / backend.DefaultModel。
func TestRun_ModelChangeEvictsAndRespawns(t *testing.T) {
	Convey("Given 一个带 usage 的假 claude 子进程", t, func() {
		var spawnCount int32
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) {
			atomic.AddInt32(&spawnCount, 1)
			return &fakeCCHandle{
				id: "fake-sid",
				// usage 非空避免 Run 的 0-frame 兜底把 session evict 掉。
				stream: &eventCCStream{events: []claudecode.Event{
					{Kind: claudecode.EventUsage, Usage: provider.Usage{PromptTokens: 1}},
					{Kind: claudecode.EventDone},
				}},
			}, nil
		})
		defer restore()

		r := New()
		ctx := context.Background()
		backend := &agent_backend_entity.AgentBackend{
			Type: string(agent_backend_entity.TypeClaudeCode),
		}
		run := func(providerModel string) {
			events, _, err := r.Run(ctx, agentruntime.RunRequest{
				Backend:   backend,
				SessionID: 77,
				Cwd:       t.TempDir(),
				UserText:  "hi",
				// effective 配置（EffectiveLLMConfig v1 seam）：模型变化走这里。
				Effective: &agentruntime.EffectiveLLMConfig{ProviderKey: "pk", ProviderType: "anthropic", ModelID: providerModel},
			})
			So(err, ShouldBeNil)
			for range events { //nolint:revive // drain
			}
		}

		Convey("When 首轮 provider.Model=A, Then spawn 恰好 1 次", func() {
			run("claude-haiku-4-5")
			So(atomic.LoadInt32(&spawnCount), ShouldEqual, 1)
		})

		Convey("When 同模型再来一轮, Then 复用不重 spawn", func() {
			run("claude-haiku-4-5")
			run("claude-haiku-4-5")
			So(atomic.LoadInt32(&spawnCount), ShouldEqual, 1)
		})

		Convey("When provider.Model 变化为 B, Then evict + 重 spawn", func() {
			run("claude-haiku-4-5")
			run("claude-opus-4-8")
			So(atomic.LoadInt32(&spawnCount), ShouldEqual, 2)
		})
	})
}

// TestRun_ProviderChangeEvictsAndRespawns 锁住 claudecode 的 evict 比对键扩展
// （spec 2026-08-10 决策 4）：--model 与 ANTHROPIC_BASE_URL/AUTH_TOKEN 都是启动期
// 参数，两个不同供应商可以配同一个 model id，只比 model 会漏掉换供应商——必须把
// effectiveProviderKey 也纳入比对，否则会话切换供应商后 CLI 子进程被 LRU 复用，
// 请求仍打到旧供应商（网关路由虽已切换，但子进程手里的 ANTHROPIC_BASE_URL 是启动期
// 烤进去的，运行时改不掉）。
func TestRun_ProviderChangeEvictsAndRespawns(t *testing.T) {
	Convey("Given 同一 claudecode 会话两轮 provider.Model 相同但 ProviderKey 不同", t, func() {
		var spawnCount int32
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) {
			atomic.AddInt32(&spawnCount, 1)
			return &fakeCCHandle{
				id: "fake-sid",
				stream: &eventCCStream{events: []claudecode.Event{
					{Kind: claudecode.EventUsage, Usage: provider.Usage{PromptTokens: 1}},
					{Kind: claudecode.EventDone},
				}},
			}, nil
		})
		defer restore()

		r := New()
		ctx := context.Background()
		backend := &agent_backend_entity.AgentBackend{
			Type: string(agent_backend_entity.TypeClaudeCode),
		}
		run := func(providerKey string) {
			events, _, err := r.Run(ctx, agentruntime.RunRequest{
				Backend:   backend,
				SessionID: 88,
				Cwd:       t.TempDir(),
				UserText:  "hi",
				// effective 配置（EffectiveLLMConfig v1 seam）：供应商 key 变化走这里，
				// 模型 id 相同但供应商不同也必须 evict。
				Effective: &agentruntime.EffectiveLLMConfig{ProviderKey: providerKey, ProviderType: "anthropic", ModelID: "same-model"},
			})
			So(err, ShouldBeNil)
			for range events { //nolint:revive // drain
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
// process must receive the existing native session ID for context continuity.
func TestRun_ModelKeyChangeEvictsAndResumes(t *testing.T) {
	Convey("Given the same Claude ProviderKey and ModelID with a different ModelKey", t, func() {
		var (
			spawnCount int32
			resumeIDs  []string
		)
		restore := SetSessionFactoryForTest(func(spec ccLaunchSpec) (ccSessionHandle, error) {
			atomic.AddInt32(&spawnCount, 1)
			resumeIDs = append(resumeIDs, spec.Req.ProviderSessionID)
			return &fakeCCHandle{
				id: "native-claude-session",
				stream: &eventCCStream{events: []claudecode.Event{
					{Kind: claudecode.EventUsage, Usage: provider.Usage{PromptTokens: 1}},
					{Kind: claudecode.EventDone},
				}},
			}, nil
		})
		defer restore()

		r := New()
		run := func(modelKey, providerSessionID string) {
			events, _, err := r.Run(context.Background(), agentruntime.RunRequest{
				Backend:           &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
				Effective:         &agentruntime.EffectiveLLMConfig{ProviderKey: "provider-a", ModelKey: modelKey, ProviderType: "anthropic", ModelID: "same-model"},
				SessionID:         89,
				ProviderSessionID: providerSessionID,
				Cwd:               t.TempDir(),
				UserText:          "hi",
			})
			So(err, ShouldBeNil)
			for range events { //nolint:revive // drain
			}
		}

		run("model-a", "")
		run("model-b", "native-claude-session")

		So(atomic.LoadInt32(&spawnCount), ShouldEqual, 2)
		So(resumeIDs, ShouldResemble, []string{"", "native-claude-session"})
	})
}

// TestClaudeCodeCapabilities 钉死 claudecode runtime 的能力矩阵 + permission
// mode 元数据。这些值与 chat_svc / 前端 UI gating 的硬编码 switch 一一对应,
// 任何一项偏移都意味着 Plan B 切 dispatcher 后会有 UI/dispatch 错乱。
//
// 历史:旧实现通过"是否实现 Steerer/Aborter/SetPermissionMode/SubmitAnswer"
// 接口隐式声明能力 + 把 mode 白名单硬编码在 chat_svc。Plan A 把这些 facts
// 都收编到 Capabilities() 一个返回值里(spec §5.4)。
func TestClaudeCodeCapabilities(t *testing.T) {
	Convey("claudecode Capabilities 矩阵", t, func() {
		r := New()
		caps := r.Capabilities()
		So(caps.Has(capability.CapSteer), ShouldBeTrue)
		So(caps.Has(capability.CapCancelSteer), ShouldBeTrue)
		So(caps.Has(capability.CapDrainSteer), ShouldBeTrue)
		So(caps.Has(capability.CapAbort), ShouldBeTrue)
		So(caps.Has(capability.CapSetPermission), ShouldBeTrue)
		So(caps.Has(capability.CapAnswerUserAsk), ShouldBeTrue)
		So(caps.Has(capability.CapToolPermission), ShouldBeTrue)
		So(caps.Has(capability.CapForkSession), ShouldBeTrue)
		// CapReportContextWindow=true 由 translator EventInit 路径承担:
		// system.init 帧带 model → llmcatalog.Lookup → emit ContextWindowUpdated。
		// Claude Code SDK 自己不报窗口,这里靠 catalog 兜底,语义和 codex 对称。
		So(caps.Has(capability.CapReportContextWindow), ShouldBeTrue)
		// CapImageInput=true:user frame 携带 base64 image content block(CLI
		// stream-json 原生支持)。extractImages 从 RunRequest.UserBlocks 抽 inline
		// 图片,Run 经 handle.Stream 透传。
		So(caps.Has(capability.CapImageInput), ShouldBeTrue)
		// CapMCPTools=true:claudecode runtime 接受 RunRequest.MCPServers,可带注入
		// 的 MCP tool 服务器启动;org/subagent/hook 工具注入消费此 cap。
		So(caps.Has(capability.CapMCPTools), ShouldBeTrue)
		// CapAutonomousTurn=true:CLI 后台任务完成自主续轮;必须实现 AutonomousTurnSource。
		So(caps.Has(capability.CapAutonomousTurn), ShouldBeTrue)
		// CapSkills=true:runtime 接受 RunRequest.EnabledPlugins,spawn 时渲进
		// --settings 的 enabledPlugins,按 agent 注入技能包开关。
		So(caps.Has(capability.CapSkills), ShouldBeTrue)
		// CapReasoningEffort=true:claude CLI 接受 --effort 五档,composer 因此渲染
		// 会话级思考力度选择器(spec 2026-09-01 决策 6)。
		So(caps.Has(capability.CapReasoningEffort), ShouldBeTrue)
		_, ok := agentruntime.Runtime(r).(agentruntime.AutonomousTurnSource)
		So(ok, ShouldBeTrue)
	})

	Convey("claudecode PermissionModeMeta", t, func() {
		caps := New().Capabilities()
		So(caps.PermissionModeMeta.AllowedModes, ShouldResemble, []string{
			"default", "acceptEdits", "plan", "bypassPermissions",
		})
		So(caps.PermissionModeMeta.DefaultMode, ShouldEqual, "acceptEdits")
		So(caps.PermissionModeMeta.SwitchableDuringTurn, ShouldBeTrue)
		So(caps.PermissionModeMeta.Order, ShouldResemble, []string{
			"default", "acceptEdits", "plan", "bypassPermissions",
		})
		// LaunchDefaultMode="":pkg/claudecode args.go 不附 --permission-mode 时
		// 内部兜底 acceptEdits;chat_svc 用此值区分"用户未显式选"vs"explicitly acceptEdits"。
		So(caps.PermissionModeMeta.LaunchDefaultMode, ShouldEqual, "")
	})
}

// TestRun_ForwardsUserBlockImages 钉死多模态透传:RunRequest.UserBlocks 里的
// inline ImageBlock 被 extractImages 抽出后,经 handle.Stream 透传给底层 CLI
// session(进而进 stream-json user frame 的 base64 image block)。非图片 block
// (TextBlock)不进 images;prompt 仍走 req.UserText。
func TestRun_ForwardsUserBlockImages(t *testing.T) {
	Convey("Run 把 UserBlocks 里的 inline 图片透传给 handle.Stream", t, func() {
		var captured *fakeCCHandle
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) {
			captured = &fakeCCHandle{id: "fake-sid"}
			return captured, nil
		})
		defer restore()

		r := New()
		ctx := context.Background()
		events, _, err := r.Run(ctx, agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
			SessionID: 77,
			Cwd:       t.TempDir(),
			UserText:  "describe",
			UserBlocks: []cagoblocks.ContentBlock{
				cagoblocks.TextBlock{Text: "describe"},
				cagoblocks.ImageBlock{MediaType: "image/png", Source: cagoblocks.BlobSource{Inline: []byte{0x01, 0x02}}},
			},
		})
		So(err, ShouldBeNil)
		for range events {
		}
		So(captured.gotPrompt, ShouldEqual, "describe")
		So(captured.gotImages, ShouldResemble, []claudecode.Image{
			{Data: []byte{0x01, 0x02}, MediaType: "image/png"},
		})
		r.CloseAllSessions(ctx)
	})
}

// TestRun_BlockedSpawnDoesNotWedgeOtherSessions 回归「单个 session 卡死 → 整个
// claudecode runtime 宕掉」。
//
// 现场(2026-06-05 sess-453/458):一个带 --mcp-config 的子轮启动 claude CLI,CLI
// 卡在 MCP 初始化;acquireSession 旧实现持**单把全局 r.mu** 串行化所有 session 的
// get-or-spawn,并在锁内做阻塞子进程操作(spawn / 同步 SetPermissionMode)。卡住的
// 那一轮一直占着全局锁 → 之后**每一个**单聊 turn 都堵在 acquireSession 的锁上,既不
// 输出也停不掉,再发消息报 ChatSendInFlight。codex 走独立 runtime 不受影响。
//
// 不变量:一个 session 的 spawn 阻塞,绝不能拖垮其它 session 的 Run。锁必须按
// session key 分桶,只串行化同一 session 的并发首轮,而非全局互斥。
func TestRun_BlockedSpawnDoesNotWedgeOtherSessions(t *testing.T) {
	Convey("一个 session 的 spawn 阻塞不得拖垮其它 session 的 Run", t, func() {
		entered := make(chan struct{})
		release := make(chan struct{})
		restore := SetSessionFactoryForTest(func(spec ccLaunchSpec) (ccSessionHandle, error) {
			if spec.Req.SessionID == 1 {
				close(entered)
				<-release // 模拟 CLI 启动期(MCP 初始化)永久挂起
			}
			return &fakeCCHandle{
				id:     "sid",
				stream: &eventCCStream{events: []claudecode.Event{{Kind: claudecode.EventDone}}},
			}, nil
		})
		defer restore()

		r := New()
		ctx := context.Background()
		cwd1, cwd2 := t.TempDir(), t.TempDir()

		type res struct {
			events <-chan agentruntime.Event
			err    error
		}
		run := func(sessionID int64, cwd, text string) <-chan res {
			ch := make(chan res, 1)
			go func() {
				events, _, err := r.Run(ctx, agentruntime.RunRequest{
					Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
					SessionID: sessionID,
					Cwd:       cwd,
					UserText:  text,
				})
				ch <- res{events: events, err: err}
			}()
			return ch
		}

		// session 1:卡在 factory(模拟带 MCP 注入的子轮 CLI 挂起);Run 同步段会一直阻塞。
		done1 := run(1, cwd1, "hang")
		<-entered // 确保 session 1 已进入 factory(bug 下此刻全局锁被它独占)

		// session 2:不同 session,必须能在 3s 内正常起 turn,不被 session 1 拖死。
		done2 := run(2, cwd2, "go")
		wedged := false
		var got2 res
		select {
		case got2 = <-done2:
		case <-time.After(3 * time.Second):
			wedged = true
		}

		// 无论是否 wedge,都放行 session 1 并 join 两个 Run —— 让所有读全局 factory
		// 的 goroutine 在 defer restore() 之前退出,失败路径下也不留竞态/泄漏。
		close(release)
		if wedged {
			got2 = <-done2
		}
		got1 := <-done1
		for range got1.events { // drain
		}
		for range got2.events { // drain
		}
		r.CloseAllSessions(ctx)

		So(wedged, ShouldBeFalse) // 全局锁被卡住的 session 1 独占 → session 2 超时 wedge
		So(got2.err, ShouldBeNil)
	})
}

// fakeCCHandle 是 ccSessionHandle 的最简 stub:Stream 返回一个立即 close 的
// 空事件流;其他控制方法 no-op。仅供 Run() 路径不需要真实 CLI 子进程的单测。
//
// setPermissionModeCalls 记录 SetPermissionMode 被调用时收到的 mode 序列, 单测
// 用来断言 spawn-after 同步逻辑;setPermissionModeErr 让单测构造"CLI 拒绝切 mode"
// 场景验证 Run() 不应把这条错误冒泡。
type fakeCCHandle struct {
	id                     string
	setPermissionModeCalls []string
	setPermissionModeErr   error
	stopTaskCalls          []string // 记录 StopTask 收到的 taskID
	stopTaskErr            error
	stream                 ccStream
	// gotPrompt / gotImages 记录最近一次 Stream 收到的入参,Run 透传断言用。
	gotPrompt string
	gotImages []claudecode.Image
	// autoTurns 注入自主续轮(AutonomousTurns 桥接测试用);nil 时方法返回 nil。
	autoTurns <-chan *claudecode.AutoTurn
	// subagentActivity 注入后台 subagent 活动流(SubagentActivity 桥接测试用);nil 时方法返回 nil。
	subagentActivity <-chan *claudecode.SubagentActivity
	// respondedResults 非 nil 时记录 RespondToControl 收到的结果(control_request
	// 自动放行路径在后台 goroutine 里回包,单测经 channel 同步观察)。
	respondedResults chan claudecode.PermissionResult
	// killed 非 nil 时, Kill 会 close 它(once) —— blockingCCStream 等在上面模拟
	// 「子进程被 SIGKILL → stream 解阻塞结束」。killCalls 原子计数 Kill 调用次数。
	killed    chan struct{}
	killOnce  sync.Once
	killCalls int32
	// interruptCalls 原子计数 Interrupt 调用次数,断言 Abort 是否真下发中断
	// (决策 8:Abort 放宽到带外轮活跃也执行中断)。
	interruptCalls int32
	// interruptErr 非 nil 时 Interrupt 返回它(注入 ErrInterruptPending / 写帧失败等
	// 真错误,断言 Abort 走不杀 / Close+evict 哪条路)。
	interruptErr error
	// closeCalls 原子计数 Close 调用次数,断言 ErrInterruptPending 不走 Close+evict 兜底。
	closeCalls int32
	// pid 是这个替身上报的子进程号(池快照的排查字段)。
	pid int
}

func (f *fakeCCHandle) PID() int { return f.pid }

func (f *fakeCCHandle) ID() string { return f.id }
func (f *fakeCCHandle) Close(context.Context) error {
	atomic.AddInt32(&f.closeCalls, 1)
	return nil
}
func (f *fakeCCHandle) Interrupt(context.Context) error {
	atomic.AddInt32(&f.interruptCalls, 1)
	return f.interruptErr
}
func (f *fakeCCHandle) Kill(context.Context) error {
	atomic.AddInt32(&f.killCalls, 1)
	if f.killed != nil {
		f.killOnce.Do(func() { close(f.killed) })
	}
	return nil
}
func (f *fakeCCHandle) SetPermissionMode(_ context.Context, mode string) error {
	f.setPermissionModeCalls = append(f.setPermissionModeCalls, mode)
	return f.setPermissionModeErr
}
func (f *fakeCCHandle) StopTask(_ context.Context, taskID string) error {
	f.stopTaskCalls = append(f.stopTaskCalls, taskID)
	return f.stopTaskErr
}
func (f *fakeCCHandle) RespondToControl(_ context.Context, _ string, res claudecode.PermissionResult) error {
	if f.respondedResults != nil {
		f.respondedResults <- res
	}
	return nil
}
func (f *fakeCCHandle) ExitErr() error                               { return nil }
func (f *fakeCCHandle) AutonomousTurns() <-chan *claudecode.AutoTurn { return f.autoTurns }
func (f *fakeCCHandle) SubagentActivity() <-chan *claudecode.SubagentActivity {
	return f.subagentActivity
}
func (f *fakeCCHandle) Stream(_ context.Context, prompt string, images []claudecode.Image) (ccStream, error) {
	f.gotPrompt = prompt
	f.gotImages = images
	if f.stream != nil {
		return f.stream, nil
	}
	return &fakeCCStream{}, nil
}

type fakeCCStream struct{}

func (s *fakeCCStream) Next() bool              { return false }
func (s *fakeCCStream) Event() claudecode.Event { return claudecode.Event{} }
func (s *fakeCCStream) SessionID() string       { return "" }

type eventCCStream struct {
	events []claudecode.Event
	idx    int
}

func (s *eventCCStream) Next() bool {
	if s.idx >= len(s.events) {
		return false
	}
	s.idx++
	return true
}

func (s *eventCCStream) Event() claudecode.Event { return s.events[s.idx-1] }
func (s *eventCCStream) SessionID() string       { return "" }

// blockingCCStream 模拟「子进程起步后卡死、一帧不吐」:Next() 阻塞到 killed 关闭
// (即 Kill 把子进程 SIGKILL 掉 → stdout EOF)才返 false 结束。无任何事件。
type blockingCCStream struct{ killed chan struct{} }

func (s *blockingCCStream) Next() bool {
	<-s.killed
	return false
}
func (s *blockingCCStream) Event() claudecode.Event { return claudecode.Event{} }
func (s *blockingCCStream) SessionID() string       { return "" }

// TestRun_StartupWatchdogKillsWedgedTurn 钉死 startup 看门狗:turn 起步后
// startupTimeout 内一帧都没有(子进程卡 MCP 初始化), 必须硬杀子进程让 drainStream
// 解阻塞, 并把 RunResult.StopErr 收成 errStartupTimeout —— 而不是永久挂起。
func TestRun_StartupWatchdogKillsWedgedTurn(t *testing.T) {
	Convey("turn 起步后 startupTimeout 内无帧 → 硬杀子进程并以 errStartupTimeout 收尾", t, func() {
		killed := make(chan struct{})
		h := &fakeCCHandle{id: "wedged", killed: killed, stream: &blockingCCStream{killed: killed}}
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) {
			return h, nil
		})
		defer restore()

		r := New()
		r.startupTimeout = 50 * time.Millisecond
		ctx := context.Background()

		events, result, err := r.Run(ctx, agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
			SessionID: 77,
			Cwd:       t.TempDir(),
			UserText:  "hang on mcp init",
		})
		So(err, ShouldBeNil)

		done := make(chan struct{})
		go func() {
			for range events { //nolint:revive // drain
			}
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("Run events channel 没关闭 —— 看门狗没能杀掉卡死的 turn")
		}

		So(atomic.LoadInt32(&h.killCalls), ShouldEqual, 1)
		So(result.StopErr, ShouldNotBeNil)
		So(errors.Is(result.StopErr, errStartupTimeout), ShouldBeTrue)
		r.CloseAllSessions(ctx)
	})
}

// busyUntilCCStream 模拟「子进程健康但忙」:Next() 阻塞到 released 关闭才结束。
// 对应真实形态 —— CLI 正在跑自主续轮,pkg/claudecode.Session 的活跃槽位被自主轮
// 占着,排队中的 user Turn 在自主轮 result 之前一帧都收不到(真 CLI 2.1.216 抓帧
// 实证:mid-turn 写进 stdin 的 user 帧被 CLI 排到当前轮 result 之后才起 init)。
// killed 用于让 Kill 也能解阻塞(硬杀 → stdout EOF)。
type busyUntilCCStream struct {
	released chan struct{}
	killed   chan struct{}
}

func (s *busyUntilCCStream) Next() bool {
	select {
	case <-s.released:
	case <-s.killed:
	}
	return false
}
func (s *busyUntilCCStream) Event() claudecode.Event { return claudecode.Event{} }
func (s *busyUntilCCStream) SessionID() string       { return "" }

// TestRun_UserTurnQueuedBehindAutonomousTurnIsNotStartupKilled 钉死 sess-1950:
// 用户在「后台任务完成的自主续轮」进行中又发了一条消息。
//
// 真实链路(真 CLI 2.1.216 抓帧实证):自主续轮占着 Session 的活跃槽位,新 user Turn
// 的 user 帧虽已写进 stdin,但 CLI 要等当前轮 result 之后才为它起 init —— 即排队中的
// user turn 在自主续轮结束前 **一帧都收不到**。这不是卡死:子进程健康、正在为自主轮
// 正常产出。
//
// 但 startup 看门狗只认「本 turn 起步后 startupTimeout 内无帧」,分不清「卡死」和
// 「排在自主续轮后面」。自主续轮跑超过 startupTimeout(生产默认 120s;sess-1950 的
// 自主轮跑了 3m35s)时,它会 **硬杀一个健康的子进程**:user turn 以 errStartupTimeout
// 失败,正在流的自主续轮也被腰斩(readLoop 拿 EOF → 活跃轮 channel 被 close)。
//
// 期望:自主续轮在飞期间,看门狗不得杀子进程。
func TestRun_UserTurnQueuedBehindAutonomousTurnIsNotStartupKilled(t *testing.T) {
	Convey("自主续轮在飞时排队的 user turn 不该被 startup 看门狗硬杀", t, func() {
		killed := make(chan struct{})
		autoDone := make(chan struct{})
		// 自主续轮仍在流:AutonomousTurns 已吐出一轮,其 Events 尚未 close。
		autoTurns := make(chan *claudecode.AutoTurn, 1)
		autoEvents := make(chan claudecode.Event)
		autoTurns <- &claudecode.AutoTurn{
			Events:        autoEvents,
			SessionID:     "sess-busy",
			Trigger:       "background_task",
			CompletedTask: &claudecode.CompletedBackgroundTask{ToolUseID: "tu1", Status: "completed"},
		}

		h := &fakeCCHandle{
			id:        "busy-with-autonomous-turn",
			killed:    killed,
			autoTurns: autoTurns,
			stream:    &busyUntilCCStream{released: autoDone, killed: killed},
		}
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) {
			return h, nil
		})
		defer restore()

		r := New()
		r.startupTimeout = 100 * time.Millisecond
		ctx := context.Background()

		events, result, err := r.Run(ctx, agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
			SessionID: 1950,
			Cwd:       t.TempDir(),
			UserText:  "给 e2e 补一个 AI 场景 spec",
		})
		So(err, ShouldBeNil)

		// 镜像 chat_svc.startAutonomousWatcher:Run 之后惰性订阅并并发 drain 自主轮。
		autoDrained := make(chan struct{})
		go func() {
			defer close(autoDrained)
			for at := range r.AutonomousTurns(1950) {
				for range at.Events { //nolint:revive // drain
				}
			}
		}()

		// 自主续轮比 startupTimeout 跑得久(生产里 3m35s vs 120s)。
		go func() {
			time.Sleep(300 * time.Millisecond)
			close(autoEvents) // 自主轮自然收尾
			close(autoTurns)  // 没有更多自主轮
			close(autoDone)   // CLI 这才为排队的 user turn 起 init
		}()

		done := make(chan struct{})
		go func() {
			for range events { //nolint:revive // drain
			}
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("Run events channel 没关闭")
		}

		// 核心断言:健康的子进程不该被杀 —— 杀了它就等于腰斩正在流的自主续轮。
		So(atomic.LoadInt32(&h.killCalls), ShouldEqual, 0)
		So(errors.Is(result.StopErr, errStartupTimeout), ShouldBeFalse)
		r.CloseAllSessions(ctx)
	})
}

// TestRun_NoChatRepoRegistered 回归 daemon 路径下的 nil panic:
// agentred daemon 不 bootstrap cago/chat_repo,Runtime.acquireSession 旧实现
// 直接调 chat_repo.Session().UpdatePermissionModeAtLaunch,在 chat_repo 未
// RegisterSession 的进程里(daemon)会触发 nil pointer dereference。
//
// 修法:runtime 不再直接写 repository,把 resolvedLaunchMode 通过
// RunResult.LaunchPermissionMode 同步回吐,由 chat_svc(只在主进程跑)落库。
func TestRun_NoChatRepoRegistered(t *testing.T) {
	Convey("daemon 路径 (chat_repo 未注册) Run 不应 panic", t, func() {
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) {
			return &fakeCCHandle{id: "fake-sid"}, nil
		})
		defer restore()

		r := New()
		ctx := context.Background()
		events, result, err := r.Run(ctx, agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
			SessionID: 42,
			Cwd:       t.TempDir(),
			UserText:  "hello",
		})
		So(err, ShouldBeNil)
		// LaunchPermissionMode 由 acquireSession 在 spawn 路径同步赋值,
		// goroutine 启动后再也不写它 —— 单字段读是 race-free 的。先拍快照再
		// drain 避免 Convey 的 ShouldNotBeNil 走 reflect 扫整个 result 触发
		// 与 drain goroutine 的 race(后者在 0-frame 兜底里写 StopErr)。
		launchMode := result.LaunchPermissionMode

		for range events {
		}
		So(launchMode, ShouldEqual, "")
		// drain 完成后 Close 释放 cache 让 t.Cleanup 不留 fd。
		r.CloseAllSessions(ctx)
	})

	Convey("daemon 路径 backend 有 default permission mode → LaunchPermissionMode 回填", t, func() {
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) {
			return &fakeCCHandle{id: "fake-sid"}, nil
		})
		defer restore()

		r := New()
		ctx := context.Background()
		events, result, err := r.Run(ctx, agentruntime.RunRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type:                  string(agent_backend_entity.TypeClaudeCode),
				DefaultPermissionMode: "bypassPermissions",
			},
			SessionID: 43,
			Cwd:       t.TempDir(),
			UserText:  "hi",
		})
		So(err, ShouldBeNil)
		launchMode := result.LaunchPermissionMode
		for range events {
		}
		So(launchMode, ShouldEqual, "bypassPermissions")
		r.CloseAllSessions(ctx)
	})
}

// TestAutonomousTurns_BridgesSessionAutoTurn 验证 Runtime.AutonomousTurns 把底层
// Session 自主续轮桥接成 agentruntime.AutonomousTurn,事件经 translate 翻译。
func TestAutonomousTurns_BridgesSessionAutoTurn(t *testing.T) {
	Convey("Runtime.AutonomousTurns 桥接底层 Session 自主续轮", t, func() {
		autoSrc := make(chan *claudecode.AutoTurn, 1)
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) {
			return &fakeCCHandle{
				id:        "fake-sid",
				autoTurns: autoSrc,
				// usage 非空避免 Run 的 0-frame 兜底把 session evict 掉。
				stream: &eventCCStream{events: []claudecode.Event{
					{Kind: claudecode.EventUsage, Usage: provider.Usage{PromptTokens: 1}},
					{Kind: claudecode.EventDone},
				}},
			}, nil
		})
		defer restore()

		r := New()
		ctx := context.Background()
		// 先跑一轮把 session spawn + 缓存。
		events, _, err := r.Run(ctx, agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
			SessionID: 77,
			Cwd:       t.TempDir(),
			UserText:  "go",
		})
		So(err, ShouldBeNil)
		for range events { //nolint:revive // drain
		}

		turns := r.AutonomousTurns(77)

		// 注入一轮自主续轮(text + done)。
		atEvents := make(chan claudecode.Event, 3)
		atEvents <- claudecode.Event{Kind: claudecode.EventTextDelta, Text: "autonomous:listing"}
		atEvents <- claudecode.Event{Kind: claudecode.EventDone}
		close(atEvents)
		autoSrc <- &claudecode.AutoTurn{Events: atEvents, SessionID: "fake-sid", Trigger: "background_task"}
		close(autoSrc)

		var at agentruntime.AutonomousTurn
		select {
		case at = <-turns:
		case <-time.After(2 * time.Second):
			t.Fatal("expected a bridged autonomous turn within 2s")
		}
		So(at.Trigger, ShouldEqual, "background_task")

		var text string
		for ev := range at.Events {
			if td, ok := ev.(agentruntime.TextDelta); ok {
				text += td.Text
			}
		}
		So(text, ShouldContainSubstring, "autonomous:listing")
		So(at.Result.ProviderSessionID, ShouldEqual, "fake-sid")

		r.CloseAllSessions(ctx)
	})
}

// TestAutonomousTurns_BridgesCompletedTask 验证 Runtime.AutonomousTurns 把底层
// claudecode.AutoTurn.CompletedTask 透传到 agentruntime.AutonomousTurn.CompletedTask。
func TestAutonomousTurns_BridgesCompletedTask(t *testing.T) {
	Convey("Runtime.AutonomousTurns 把 CompletedTask 透传", t, func() {
		autoSrc := make(chan *claudecode.AutoTurn, 1)
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) {
			return &fakeCCHandle{
				id:        "fake-sid",
				autoTurns: autoSrc,
				stream: &eventCCStream{events: []claudecode.Event{
					{Kind: claudecode.EventUsage, Usage: provider.Usage{PromptTokens: 1}},
					{Kind: claudecode.EventDone},
				}},
			}, nil
		})
		defer restore()

		r := New()
		ctx := context.Background()
		events, _, err := r.Run(ctx, agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
			SessionID: 78,
			Cwd:       t.TempDir(),
			UserText:  "go",
		})
		So(err, ShouldBeNil)
		for range events {
		}

		turns := r.AutonomousTurns(78)

		atEvents := make(chan claudecode.Event, 2)
		atEvents <- claudecode.Event{Kind: claudecode.EventDone}
		close(atEvents)
		autoSrc <- &claudecode.AutoTurn{
			Events:    atEvents,
			SessionID: "fake-sid",
			Trigger:   "background_task",
			CompletedTask: &claudecode.CompletedBackgroundTask{
				ToolUseID: "tu1",
				TaskID:    "task-1",
				Status:    "completed",
				Summary:   "sum",
			},
		}
		close(autoSrc)

		var got agentruntime.AutonomousTurn
		select {
		case got = <-turns:
		case <-time.After(2 * time.Second):
			t.Fatal("expected a bridged autonomous turn within 2s")
		}
		// drain events
		for range got.Events {
		}
		So(got.CompletedTask, ShouldNotBeNil)
		So(got.CompletedTask.ToolCallID, ShouldEqual, "tu1")
		So(got.CompletedTask.TaskID, ShouldEqual, "task-1")
		So(got.CompletedTask.Status, ShouldEqual, "completed")
		So(got.CompletedTask.Summary, ShouldEqual, "sum")

		r.CloseAllSessions(ctx)
	})
}

// TestRuntime_StopBackgroundTask 钉死 BackgroundTaskStopper:turn 结束(idle)后仍能
// 按 task_id 停后台任务(不校验 inTurn);空 taskID / 会话不在缓存的错误路径。
func TestRuntime_StopBackgroundTask(t *testing.T) {
	Convey("turn 结束后仍下发 stop_task(不要求 inTurn)", t, func() {
		h := &fakeCCHandle{id: "fake-sid", stream: &eventCCStream{events: []claudecode.Event{{Kind: claudecode.EventDone}}}}
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) { return h, nil })
		defer restore()

		r := New()
		ctx := context.Background()
		events, _, err := r.Run(ctx, agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
			SessionID: 42,
			Cwd:       t.TempDir(),
			UserText:  "start a bg task",
		})
		So(err, ShouldBeNil)
		for range events { //nolint:revive // drain 到 turn 结束(inTurn=false)
		}

		So(r.StopBackgroundTask(ctx, 42, "b0n82mqaj"), ShouldBeNil)
		So(h.stopTaskCalls, ShouldResemble, []string{"b0n82mqaj"})
		r.CloseAllSessions(ctx)
	})

	Convey("空 taskID 直接返错,不下发", t, func() {
		So(New().StopBackgroundTask(context.Background(), 42, ""), ShouldNotBeNil)
	})

	Convey("会话不在缓存(已 evict/未 spawn)→ ErrNoActiveTurn", t, func() {
		err := New().StopBackgroundTask(context.Background(), 999, "b0n82mqaj")
		So(errors.Is(err, agentruntime.ErrNoActiveTurn), ShouldBeTrue)
	})
}

// TestRuntime_Abort 钉死决策 8:Abort 的活跃判据放宽为 inTurn || outOfBandActive()。
// 带外轮(自主续轮 / 后台 subagent 活动轮)独占帧流期间 inTurn 仍是 false —— 此前
// 会被误判为「无活跃轮」拒绝中断,用户按「停止」停不掉正在飞的带外轮。放宽后
// outOfBandActive 单独也能触发真正的中断;两者都不活跃时契约不变,仍返回
// ErrNoActiveTurn。turnToken=0 时语义与旧版一致:「中断当前活跃的那一轮」。
func TestRuntime_Abort(t *testing.T) {
	Convey("仅带外轮活跃(inTurn=false, outOfBandActive=true) → 执行中断", t, func() {
		r := New()
		h := &fakeCCHandle{}
		a := &claudeActive{handle: h}
		a.enterOutOfBand()
		a.nextTurnToken(agentruntime.TurnKindAutonomous)
		r.cache.Put(sessionKey(501), a)

		outcome, err := r.Abort(context.Background(), 501, 0)

		So(err, ShouldBeNil)
		So(atomic.LoadInt32(&h.interruptCalls), ShouldEqual, 1)
		So(outcome.TurnKind, ShouldEqual, agentruntime.TurnKindAutonomous)
	})

	Convey("用户轮与带外轮都不活跃 → 契约不变,返回 ErrNoActiveTurn", t, func() {
		r := New()
		h := &fakeCCHandle{}
		a := &claudeActive{handle: h}
		r.cache.Put(sessionKey(502), a)

		outcome, err := r.Abort(context.Background(), 502, 0)

		So(errors.Is(err, agentruntime.ErrNoActiveTurn), ShouldBeTrue)
		So(atomic.LoadInt32(&h.interruptCalls), ShouldEqual, 0)
		So(outcome.TurnKind, ShouldEqual, agentruntime.TurnKindNone)
	})
}

// TestRuntime_Abort_TurnToken 钉死决策 1 的 per-turn token 语义:Abort 携带 token
// (0=当前活跃轮)精确寻址,非 0 时仅当该轮仍是当前活跃轮才中断,否则 stale no-op
// 不触碰任何其它轮;并上报被中断轮的类型。
func TestRuntime_Abort_TurnToken(t *testing.T) {
	Convey("stale token(轮已切换)→ no-op,不中断新轮、不返错", t, func() {
		r := New()
		h := &fakeCCHandle{}
		a := &claudeActive{handle: h}
		a.enterOutOfBand()
		r.cache.Put(sessionKey(511), a)

		tokenOld := a.nextTurnToken(agentruntime.TurnKindAutonomous)
		tokenNew := a.nextTurnToken(agentruntime.TurnKindAutonomous)
		So(tokenNew, ShouldNotEqual, tokenOld)

		outcome, err := r.Abort(context.Background(), 511, tokenOld)

		So(err, ShouldBeNil)
		So(atomic.LoadInt32(&h.interruptCalls), ShouldEqual, 0)
		So(outcome.TurnKind, ShouldEqual, agentruntime.TurnKindNone)
	})

	Convey("当前 token → 执行中断并上报被中断轮类型", t, func() {
		r := New()
		h := &fakeCCHandle{}
		a := &claudeActive{handle: h}
		a.enterOutOfBand()
		r.cache.Put(sessionKey(512), a)

		token := a.nextTurnToken(agentruntime.TurnKindAutonomous)

		outcome, err := r.Abort(context.Background(), 512, token)

		So(err, ShouldBeNil)
		So(atomic.LoadInt32(&h.interruptCalls), ShouldEqual, 1)
		So(outcome.TurnKind, ShouldEqual, agentruntime.TurnKindAutonomous)
	})

	Convey("turnToken=0 → 中断当前活跃轮(等价旧行为),上报类型", t, func() {
		r := New()
		h := &fakeCCHandle{}
		a := &claudeActive{handle: h}
		a.enterOutOfBand()
		a.nextTurnToken(agentruntime.TurnKindSubagentActivity)
		r.cache.Put(sessionKey(513), a)

		outcome, err := r.Abort(context.Background(), 513, 0)

		So(err, ShouldBeNil)
		So(atomic.LoadInt32(&h.interruptCalls), ShouldEqual, 1)
		So(outcome.TurnKind, ShouldEqual, agentruntime.TurnKindSubagentActivity)
	})

	Convey("用户轮(inTurn=true)活跃时 turnToken=0 → 上报 userTurn", t, func() {
		r := New()
		h := &fakeCCHandle{}
		a := &claudeActive{handle: h}
		a.inTurn.Store(true)
		a.nextTurnToken(agentruntime.TurnKindUser)
		r.cache.Put(sessionKey(514), a)

		outcome, err := r.Abort(context.Background(), 514, 0)

		So(err, ShouldBeNil)
		So(atomic.LoadInt32(&h.interruptCalls), ShouldEqual, 1)
		So(outcome.TurnKind, ShouldEqual, agentruntime.TurnKindUser)
	})
}

// TestRuntime_Abort_ErrInterruptPending 钉死决策 2:Interrupt 有界超时返回的
// ErrInterruptPending 被 Abort 视为「中断已下发、ack 异步到」→ 返回 nil、不 Close
// 子进程、不逐出缓存;只有写帧失败 / session closed / CLI 拒绝等**真错误**才保留
// runtime.go:248-252 的 Close+evict 兜底。
func TestRuntime_Abort_ErrInterruptPending(t *testing.T) {
	Convey("Interrupt 返 ErrInterruptPending → 返回 nil、不 Close、不逐出缓存", t, func() {
		r := New()
		h := &fakeCCHandle{interruptErr: claudecode.ErrInterruptPending}
		a := &claudeActive{handle: h}
		a.enterOutOfBand()
		a.nextTurnToken(agentruntime.TurnKindAutonomous)
		r.cache.Put(sessionKey(601), a)

		outcome, err := r.Abort(context.Background(), 601, 0)

		So(err, ShouldBeNil)
		So(outcome.TurnKind, ShouldEqual, agentruntime.TurnKindAutonomous)
		So(atomic.LoadInt32(&h.interruptCalls), ShouldEqual, 1)
		So(atomic.LoadInt32(&h.closeCalls), ShouldEqual, 0) // ErrInterruptPending 不应 Close 子进程
		v, ok := r.cache.Get(sessionKey(601))
		So(ok, ShouldBeTrue) // ErrInterruptPending 不应逐出缓存
		So(v, ShouldEqual, a)
	})

	Convey("真错误(写帧失败)→ 保留 Close+evict 兜底", t, func() {
		r := New()
		h := &fakeCCHandle{interruptErr: errors.New("claudecode: write frame: broken pipe")}
		a := &claudeActive{handle: h}
		a.enterOutOfBand()
		a.nextTurnToken(agentruntime.TurnKindAutonomous)
		r.cache.Put(sessionKey(602), a)

		_, err := r.Abort(context.Background(), 602, 0)

		So(err, ShouldNotBeNil)
		// 真错误应 Close 子进程:显式 a.handle.Close 一次,且 cache.Remove 还会异步 close
		// 被逐出的 handle(CLISessionPool.Remove 里 go closeWithTimeout),故 closeCalls 是
		// 1 或 2 —— 钉死「至少一次 Close + 缓存被逐出」这条与 ErrInterruptPending(0 次
		// Close)可区分的判据,不钉死可能被异步 close 竞态的精确次数。
		So(atomic.LoadInt32(&h.closeCalls), ShouldBeGreaterThanOrEqualTo, 1)
		_, ok := r.cache.Get(sessionKey(602))
		So(ok, ShouldBeFalse) // 真错误应逐出缓存
	})
}

func TestRun_ErrorFollowedByProgressClearsStopErr(t *testing.T) {
	Convey("claudecode runtime: EventError 后还有进展事件和完成时, StopErr 不应污染成功 turn", t, func() {
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) {
			return &fakeCCHandle{
				id: "fake-sid",
				stream: &eventCCStream{events: []claudecode.Event{
					{Kind: claudecode.EventError, Err: errors.New("temporary upstream hiccup")},
					{Kind: claudecode.EventTextDelta, Text: "recovered"},
					{Kind: claudecode.EventDone},
				}},
			}, nil
		})
		defer restore()

		r := New()
		ctx := context.Background()
		events, result, err := r.Run(ctx, agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
			SessionID: 42,
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
		r.CloseAllSessions(ctx)
	})
}

func TestRun_ErrorFollowedOnlyByMetadataKeepsStopErr(t *testing.T) {
	Convey("claudecode runtime: EventError 后只有 metadata 和完成时, StopErr 仍应保留", t, func() {
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) {
			return &fakeCCHandle{
				id: "fake-sid",
				stream: &eventCCStream{events: []claudecode.Event{
					{Kind: claudecode.EventError, Err: errors.New("temporary upstream hiccup")},
					{Kind: claudecode.EventUsage},
					{Kind: claudecode.EventDone},
				}},
			}, nil
		})
		defer restore()

		r := New()
		ctx := context.Background()
		events, result, err := r.Run(ctx, agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
			SessionID: 42,
			Cwd:       t.TempDir(),
			UserText:  "hello",
		})
		So(err, ShouldBeNil)
		for range events {
		}

		So(result.StopErr, ShouldNotBeNil)
		So(result.StopErr.Error(), ShouldContainSubstring, "temporary upstream hiccup")
		r.CloseAllSessions(ctx)
	})
}

// TestRun_SpawnAfterSetPermissionMode 锁住「stored mode != launch mode 时
// spawn 后自动校准 CLI」的不变量。「先 plan 后 bypass」工作流靠这条派生:
// backendDefault=bypass 强制 launch=bypass(resolveLaunchMode), 但 stored mode
// (req.PermissionMode) 是用户实际想要的初始 mode(默认 plan)。spawn 完成的瞬间
// 必须发一次 SetPermissionMode 把 CLI 切到 stored, 否则用户看到的 pill 是 Plan
// 但 CLI 还在 bypass 自动执行。
func TestRun_SpawnAfterSetPermissionMode(t *testing.T) {
	Convey("Given backendDefault=bypass + stored=plan, When Run, Then spawn 后 SetPermissionMode(plan) 被调一次", t, func() {
		var captured *fakeCCHandle
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) {
			captured = &fakeCCHandle{id: "fake-sid"}
			return captured, nil
		})
		defer restore()

		r := New()
		ctx := context.Background()
		events, result, err := r.Run(ctx, agentruntime.RunRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type:                  string(agent_backend_entity.TypeClaudeCode),
				DefaultPermissionMode: "bypassPermissions",
			},
			SessionID:      44,
			Cwd:            t.TempDir(),
			UserText:       "hi",
			PermissionMode: "plan",
		})
		So(err, ShouldBeNil)
		// launch 仍然是 bypass(resolveLaunchMode 锁住)。
		launchMode := result.LaunchPermissionMode
		for range events {
		}
		So(launchMode, ShouldEqual, "bypassPermissions")
		So(captured, ShouldNotBeNil)
		So(captured.setPermissionModeCalls, ShouldResemble, []string{"plan"})
		r.CloseAllSessions(ctx)
	})

	Convey("Given stored mode == launch mode, When Run, Then SetPermissionMode 不被调", t, func() {
		var captured *fakeCCHandle
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) {
			captured = &fakeCCHandle{id: "fake-sid"}
			return captured, nil
		})
		defer restore()

		r := New()
		ctx := context.Background()
		// backendDefault=acceptEdits, stored=acceptEdits → launch=acceptEdits, 不需要校准。
		events, _, err := r.Run(ctx, agentruntime.RunRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type:                  string(agent_backend_entity.TypeClaudeCode),
				DefaultPermissionMode: "acceptEdits",
			},
			SessionID:      45,
			Cwd:            t.TempDir(),
			UserText:       "hi",
			PermissionMode: "acceptEdits",
		})
		So(err, ShouldBeNil)
		for range events {
		}
		So(captured, ShouldNotBeNil)
		So(captured.setPermissionModeCalls, ShouldBeEmpty)
		r.CloseAllSessions(ctx)
	})

	Convey("Given SetPermissionMode 失败, Then Run 不应返错(只记 warn)", t, func() {
		var captured *fakeCCHandle
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) {
			captured = &fakeCCHandle{id: "fake-sid", setPermissionModeErr: context.DeadlineExceeded}
			return captured, nil
		})
		defer restore()

		r := New()
		ctx := context.Background()
		events, result, err := r.Run(ctx, agentruntime.RunRequest{
			Backend: &agent_backend_entity.AgentBackend{
				Type:                  string(agent_backend_entity.TypeClaudeCode),
				DefaultPermissionMode: "bypassPermissions",
			},
			SessionID:      46,
			Cwd:            t.TempDir(),
			UserText:       "hi",
			PermissionMode: "plan",
		})
		So(err, ShouldBeNil)
		launchMode := result.LaunchPermissionMode
		for range events {
		}
		So(launchMode, ShouldEqual, "bypassPermissions")
		So(captured.setPermissionModeCalls, ShouldResemble, []string{"plan"})
		r.CloseAllSessions(ctx)
	})
}

// TestSubagentActivity_BridgesSessionActivity 验证 Runtime.SubagentActivity 把底层
// claudecode.Session 的后台 subagent 活动流桥接成 agentruntime.SubagentActivity,
// 事件经 drainStream 翻译(ParentToolUseID → ParentToolCallID)。
func TestSubagentActivity_BridgesSessionActivity(t *testing.T) {
	Convey("Runtime.SubagentActivity 桥接底层 Session 后台 subagent 活动流", t, func() {
		actSrc := make(chan *claudecode.SubagentActivity, 1)
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) {
			return &fakeCCHandle{
				id:               "fake-sid",
				subagentActivity: actSrc,
				// usage 非空避免 Run 的 0-frame 兜底把 session evict 掉。
				stream: &eventCCStream{events: []claudecode.Event{
					{Kind: claudecode.EventUsage, Usage: provider.Usage{PromptTokens: 1}},
					{Kind: claudecode.EventDone},
				}},
			}, nil
		})
		defer restore()

		r := New()
		ctx := context.Background()
		// 先跑一轮把 session spawn + 缓存。
		events, _, err := r.Run(ctx, agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
			SessionID: 88,
			Cwd:       t.TempDir(),
			UserText:  "go",
		})
		So(err, ShouldBeNil)
		for range events { //nolint:revive // drain
		}

		activities := r.SubagentActivity(88)

		// 注入一轮后台 subagent 活动(PreToolUse + done),ParentToolUseID 需被翻译成 ParentToolCallID。
		saEvents := make(chan claudecode.Event, 4)
		saEvents <- claudecode.Event{
			Kind: claudecode.EventPreToolUse,
			Tool: &claudecode.ToolEvent{
				ID:    "inner-tu-1",
				Name:  "bash",
				Input: []byte(`{"command":"ls"}`),
			},
			ParentToolUseID: "<agent tool id>",
		}
		saEvents <- claudecode.Event{Kind: claudecode.EventDone}
		close(saEvents)
		actSrc <- &claudecode.SubagentActivity{ToolUseID: "<agent tool id>", Events: saEvents, SessionID: "fake-sid"}
		close(actSrc)

		var got agentruntime.SubagentActivity
		select {
		case got = <-activities:
		case <-time.After(2 * time.Second):
			t.Fatal("expected a bridged SubagentActivity within 2s")
		}
		So(got.ToolCallID, ShouldEqual, "<agent tool id>")

		var gotParentToolCallID string
		for ev := range got.Events {
			if tu, ok := ev.(agentruntime.ToolCall); ok {
				gotParentToolCallID = tu.ParentToolCallID
			}
		}
		So(gotParentToolCallID, ShouldEqual, "<agent tool id>")

		r.CloseAllSessions(ctx)
	})

	Convey("Runtime.SubagentActivity sessionID 未知时返回立即 close 的 channel", t, func() {
		r := New()
		ch := r.SubagentActivity(999)
		select {
		case _, ok := <-ch:
			So(ok, ShouldBeFalse)
		case <-time.After(time.Second):
			t.Fatal("channel 未立即 close")
		}
	})
}

// TestClaudeEventShowsProgressAfterError_SubagentModel 覆盖 wrap-up 复审第三轮
// Finding 1:claudeEventShowsProgressAfterError 是 chat_svc.eventShowsProgressAfterError
// 的运行时层镜像注册表,claudecode.EventSubagentModel 漏登记。这是两处注册表的一致性
// 缺陷,不是已证实可触发的故障——唯一可能产出空内容 subagent 帧的场景是 `<synthetic>`
// API 错误帧,而该帧已在 pkg/claudecode/session.go 过滤,不会以 EventSubagentModel
// 形式流出 drainStream。
func TestClaudeEventShowsProgressAfterError_SubagentModel(t *testing.T) {
	Convey("claudeEventShowsProgressAfterError 应把 EventSubagentModel 视为错误后的进度(与 chat_svc 镜像一致)", t, func() {
		So(claudeEventShowsProgressAfterError(claudecode.EventSubagentModel), ShouldBeTrue)
	})
}

// TestRun_WebInitiatedFreeSessionResolvesCwdFromSyncID 是 2026-08-22 那条报错的回归:
// 从 web 控制台发起一条**不钉项目**的对话,浏览器发 agentId: 0(它没有、也不该编一个
// 桌面端本地自增主键,见 dispatch.ts)、cwd 空(没选项目就没有路径,见 workspace_svc
// 的 chosenCwd),daemon 原样转给 runtime,兜底 AgentCwd(0) 直接拒 —— 界面上是
// 「coding 已连接,但 Agent 启动失败:agentruntime: AgentCwd needs agentID > 0」。
// 身份改由随请求一起到达的 AgentSyncID 承担。
func TestRun_WebInitiatedFreeSessionResolvesCwdFromSyncID(t *testing.T) {
	Convey("Given 一条 web 发起的自由会话:AgentID=0、Cwd 空、只带账号级 AgentSyncID", t, func() {
		dataDir := t.TempDir()
		t.Setenv("AGENTRE_DATA_DIR", dataDir)

		var gotCwd string
		restore := SetSessionFactoryForTest(func(spec ccLaunchSpec) (ccSessionHandle, error) {
			gotCwd = spec.Cwd
			return &fakeCCHandle{
				id: "fake-sid",
				stream: &eventCCStream{events: []claudecode.Event{
					{Kind: claudecode.EventUsage, Usage: provider.Usage{PromptTokens: 1}},
					{Kind: claudecode.EventDone},
				}},
			}, nil
		})
		defer restore()

		Convey("When 起这一轮, Then 起得来,工作目录落在该 Agent 的账号级同步标识下", func() {
			events, _, err := New().Run(context.Background(), agentruntime.RunRequest{
				Backend: &agent_backend_entity.AgentBackend{
					Type: string(agent_backend_entity.TypeClaudeCode),
				},
				SessionID:   99,
				AgentID:     0,
				AgentSyncID: "01KZNE7YKJQ6A79YVDCMW1A63R",
				UserText:    "hi",
				Effective: &agentruntime.EffectiveLLMConfig{
					ProviderKey: "pk", ProviderType: "anthropic", ModelID: "claude-haiku-4-5",
				},
			})
			So(err, ShouldBeNil)
			for range events { //nolint:revive // drain
			}
			So(gotCwd, ShouldEqual,
				filepath.Join(dataDir, "agents", "sync-01KZNE7YKJQ6A79YVDCMW1A63R"))
		})
	})
}

// TestAutonomousTurns_GivenTurnInFlight_WhenIdleSweeperRuns_ThenSessionIsNotReleased
// 钉死 sess-3244:CLI 自主跑的那一轮不经过 acquireSession,池里没人替它 MarkActive
// —— 条目整轮都停在上一个用户轮结束时标下的 idle,idleFor 接着涨。生产里自主轮跑到
// 第 15 分钟时被空闲清扫关掉 stdin:子进程照常跑完(拿到测试结果、提交了代码),
// 宿主却从此一帧都收不到,界面永远停在最后一个没有 tool_result 的 tool_use,而且
// 清扫不产生 StopErr,连报错都没有。
func TestAutonomousTurns_GivenTurnInFlight_WhenIdleSweeperRuns_ThenSessionIsNotReleased(t *testing.T) {
	Convey("自主续轮在飞时,空闲清扫不该释放这条会话", t, func() {
		autoSrc := make(chan *claudecode.AutoTurn, 1)
		h := &fakeCCHandle{
			id:        "sess-3244",
			autoTurns: autoSrc,
			stream: &eventCCStream{events: []claudecode.Event{
				{Kind: claudecode.EventUsage, Usage: provider.Usage{PromptTokens: 1}},
				{Kind: claudecode.EventDone},
			}},
		}
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) { return h, nil })
		defer restore()

		pool := agentruntime.NewCLISessionPool(8)
		r := NewWithPool(pool)
		ctx := context.Background()

		// 用户轮跑完:池里这条转成 idle,清扫的计时从这一刻开始。
		events, _, err := r.Run(ctx, agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
			SessionID: 3244,
			Cwd:       t.TempDir(),
			UserText:  "继续",
		})
		So(err, ShouldBeNil)
		for range events { //nolint:revive // drain
		}
		snap := pool.Snapshot()
		So(len(snap), ShouldEqual, 1)
		So(snap[0].State, ShouldEqual, agentruntime.CLISessionIdle)

		// 后台任务完成,CLI 自主跑一轮 —— 生产里这一轮光一条 vitest 就跑了三分半。
		turns := r.AutonomousTurns(3244)
		atEvents := make(chan claudecode.Event, 2)
		autoSrc <- &claudecode.AutoTurn{Events: atEvents, SessionID: "sess-3244", Trigger: "background_task"}

		var at agentruntime.AutonomousTurn
		select {
		case at = <-turns:
		case <-time.After(2 * time.Second):
			t.Fatal("expected a bridged autonomous turn within 2s")
		}
		// 收到一帧翻译后的事件,确认这一轮确实在流(drainStream 已经跑起来了)。
		atEvents <- claudecode.Event{Kind: claudecode.EventTextDelta, Text: "still working"}
		select {
		case ev := <-at.Events:
			_, ok := ev.(agentruntime.TextDelta)
			So(ok, ShouldBeTrue)
		case <-time.After(2 * time.Second):
			t.Fatal("expected the autonomous turn to be streaming within 2s")
		}

		// 核心断言:这一轮在飞,清扫一个都不能动。
		So(pool.PruneIdleOlderThan(0), ShouldEqual, 0)
		So(atomic.LoadInt32(&h.closeCalls), ShouldEqual, 0)
		So(pool.Len(), ShouldEqual, 1)

		// 自主轮收尾后它才重新是一条普通的过期 idle 会话,清扫照常回收。
		atEvents <- claudecode.Event{Kind: claudecode.EventDone}
		close(atEvents)
		close(autoSrc)
		for range at.Events { //nolint:revive // drain
		}
		So(pool.PruneIdleOlderThan(0), ShouldEqual, 1)
	})
}

// TestRuntime_Steer_OutOfBandTurn 钉死 sess-3321:Steer 的活跃判据必须与 Abort
// 同一套 —— 「该会话此刻活跃的那一轮」既包括用户轮(inTurn)也包括带外轮
// (自主续轮 / 后台 subagent 活动轮独占帧流期间 inTurn 仍是 false)。
//
// 旧判据只认 inTurn:自主续轮跑着的时候用户发消息,前端 Enqueue 拿到
// ErrNoActiveTurn → 按「turn 已结束」回退成普通 Send → 往一个正在跑轮的 CLI 里
// 写 user 帧。CLI 把它并进当前那一轮(不再单独起 init/result),于是 agentre 这边
// 登记的 user 轮永远等不到帧:整轮空转到 startup 看门狗超时,把一个健康子进程
// 杀掉,界面上留下一串空 assistant 气泡,而真正的回答全堆进了带外轮那条消息里。
func TestRuntime_Steer_OutOfBandTurn(t *testing.T) {
	Convey("仅带外轮活跃(inTurn=false, outOfBandActive=true) → 消息进 inbox", t, func() {
		r := New()
		r.SetSteerInbox(httpgateway.NewSteerInbox())
		a := &claudeActive{handle: &fakeCCHandle{}, sessionUUID: "uuid-3321"}
		a.enterOutOfBand()
		r.cache.Put(sessionKey(521), a)

		err := r.Steer(context.Background(), 521, "q-1", "直接交付")

		So(err, ShouldBeNil)
		items := r.steer.Drain("uuid-3321")
		So(len(items), ShouldEqual, 1)
		So(items[0].Text, ShouldEqual, "直接交付")
	})

	Convey("用户轮与带外轮都不活跃 → 契约不变,返回 ErrNoActiveTurn", t, func() {
		r := New()
		r.SetSteerInbox(httpgateway.NewSteerInbox())
		a := &claudeActive{handle: &fakeCCHandle{}, sessionUUID: "uuid-idle"}
		r.cache.Put(sessionKey(522), a)

		err := r.Steer(context.Background(), 522, "q-1", "直接交付")

		So(errors.Is(err, agentruntime.ErrNoActiveTurn), ShouldBeTrue)
		So(len(r.steer.Drain("uuid-idle")), ShouldEqual, 0)
	})
}

// TestRuntime_Steer_SubagentActivityIsNotMainThread 钉死放宽判据的**边界**:只有
// 主线带外轮(自主续轮)才让 Steer 认账。后台 subagent 活动轮跑的是子 agent 的内部
// 循环,CLI 主线此刻空闲 —— 用户新消息该正正经经起一轮(Session.currentTurn 的
// sess-2974 分支会让位给排队的 user Turn),插进那个子 agent 的上下文里只会被它的
// 工具循环吞掉,主线永远看不到。
func TestRuntime_Steer_SubagentActivityIsNotMainThread(t *testing.T) {
	Convey("只有后台 subagent 活动轮在流 → 仍返回 ErrNoActiveTurn,让调用方去起新一轮", t, func() {
		r := New()
		r.SetSteerInbox(httpgateway.NewSteerInbox())
		a := &claudeActive{handle: &fakeCCHandle{}, sessionUUID: "uuid-activity"}
		a.enterSubagentActivity()
		r.cache.Put(sessionKey(523), a)

		err := r.Steer(context.Background(), 523, "q-1", "直接交付")

		So(errors.Is(err, agentruntime.ErrNoActiveTurn), ShouldBeTrue)
		So(len(r.steer.Drain("uuid-activity")), ShouldEqual, 0)
	})

	Convey("自主续轮在流、期间又挂起一路 subagent 活动轮 → 主线仍忙,Steer 照常认账", t, func() {
		r := New()
		r.SetSteerInbox(httpgateway.NewSteerInbox())
		a := &claudeActive{handle: &fakeCCHandle{}, sessionUUID: "uuid-both"}
		a.enterOutOfBand()
		a.enterSubagentActivity()
		r.cache.Put(sessionKey(524), a)

		err := r.Steer(context.Background(), 524, "q-1", "直接交付")

		So(err, ShouldBeNil)
		So(len(r.steer.Drain("uuid-both")), ShouldEqual, 1)
	})
}

// TestAutonomousTurns_SurfacesConsumedSteer 钉死 sess-3321 归位链路的中间一环:自主
// 续轮期间被 hook 从 inbox 取走的插话,必须以 SteerConsumed 事件出现在**这一轮**的
// 事件流上。订阅只挂在 Run(用户轮)上是不够的 —— 纯自主轮期间根本没有 Run 在飞,
// 那条插话就此无人记账:用户消息一行都不落库,前端排队 chip 等不到 StreamSteerConsumed
// 永不消失,而 CLI 的回答照样堆进自主轮那条 assistant 里。
func TestAutonomousTurns_SurfacesConsumedSteer(t *testing.T) {
	Convey("自主续轮期间 inbox 被 drain → 本轮事件流上出现 SteerConsumed", t, func() {
		autoSrc := make(chan *claudecode.AutoTurn, 1)
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) {
			return &fakeCCHandle{
				id:        "fake-sid",
				autoTurns: autoSrc,
				stream: &eventCCStream{events: []claudecode.Event{
					{Kind: claudecode.EventUsage, Usage: provider.Usage{PromptTokens: 1}},
					{Kind: claudecode.EventDone},
				}},
			}, nil
		})
		defer restore()

		r := New()
		r.SetSteerInbox(httpgateway.NewSteerInbox())
		ctx := context.Background()
		events, _, err := r.Run(ctx, agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
			SessionID: 78,
			Cwd:       t.TempDir(),
			UserText:  "go",
		})
		So(err, ShouldBeNil)
		for range events { //nolint:revive // drain
		}

		turns := r.AutonomousTurns(78)

		atEvents := make(chan claudecode.Event, 3)
		autoSrc <- &claudecode.AutoTurn{Events: atEvents, SessionID: "fake-sid", Trigger: "background_task"}
		close(autoSrc)

		var at agentruntime.AutonomousTurn
		select {
		case at = <-turns:
		case <-time.After(2 * time.Second):
			t.Fatal("expected a bridged autonomous turn within 2s")
		}

		// 自主轮在飞时用户插话 —— Steer 认账(主线正忙),随后 CLI 的 hook 在工具边界
		// 把它取走,inbox 的 drain 通知就该落到这一轮上。
		So(r.Steer(ctx, 78, "q-1", "直接交付"), ShouldBeNil)
		cached, ok := r.cache.Get(sessionKey(78))
		So(ok, ShouldBeTrue)
		inboxKey := cached.(*claudeActive).sessionUUID
		So(len(r.steer.Drain(inboxKey)), ShouldEqual, 1)

		atEvents <- claudecode.Event{Kind: claudecode.EventDone}
		close(atEvents)

		var steered []agentruntime.ConsumedSteer
		for ev := range at.Events {
			if sc, ok := ev.(agentruntime.SteerConsumed); ok {
				steered = append(steered, sc.Steers...)
			}
		}
		So(len(steered), ShouldEqual, 1)
		So(steered[0].Text, ShouldEqual, "直接交付")
		So(steered[0].QueuedID, ShouldEqual, "q-1")

		r.CloseAllSessions(ctx)
	})
}

// Given 同一条会话两轮的推理强度不同, When 第二轮开跑, Then 驱逐并重新 spawn ——
// --effort 与 --model 一样是启动期 flag,复用旧进程就是拿旧档位跑新一轮。
//
// 这一项此前只有 acquireSession 里那段内联比较,没有任何测试钉住;身份收进池之后它是
// 身份串里最容易被漏掉的一段。
func TestRun_ReasoningEffortChangeEvictsAndRespawns(t *testing.T) {
	Convey("Given 同一 claudecode 会话两轮推理强度不同", t, func() {
		var spawnCount int32
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) {
			atomic.AddInt32(&spawnCount, 1)
			return &fakeCCHandle{
				id: "fake-sid",
				stream: &eventCCStream{events: []claudecode.Event{
					{Kind: claudecode.EventUsage, Usage: provider.Usage{PromptTokens: 1}},
					{Kind: claudecode.EventDone},
				}},
			}, nil
		})
		defer restore()

		r := New()
		run := func(effort string) {
			events, _, err := r.Run(context.Background(), agentruntime.RunRequest{
				Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode), ReasoningEffort: effort},
				Effective: &agentruntime.EffectiveLLMConfig{ProviderKey: "pk", ProviderType: "anthropic", ModelID: "same-model"},
				SessionID: 92,
				Cwd:       t.TempDir(),
				UserText:  "hi",
			})
			So(err, ShouldBeNil)
			for range events { //nolint:revive // drain
			}
		}

		Convey("When 同档位再来一轮, Then 复用不重 spawn", func() {
			run("low")
			run("low")
			So(atomic.LoadInt32(&spawnCount), ShouldEqual, 1)
		})

		Convey("When 档位变化, Then evict + 重 spawn", func() {
			run("low")
			run("high")
			So(atomic.LoadInt32(&spawnCount), ShouldEqual, 2)
		})
	})
}

// Given 池里那条会话没有记录过启动身份, When 这条会话又来一轮, Then 按「已变」处理:
// 驱逐并重新 spawn。
//
// 三个后端此前对「这个池键没被记录过」给出两种相反答案(codex 判已变、piagent 判未变)。
// 规则收进池之后统一取「未记录即已变」:误判为变化的代价是多起一次进程,误判为未变化
// 的代价是整轮跑在上一个供应商/模型上而无人发觉(spec 2026-08-26 决策 4)。
func TestRun_UnrecordedLaunchIdentityEvictsAndRespawns(t *testing.T) {
	Convey("Given 池里有一条没记录过启动身份的 claudecode 会话", t, func() {
		var spawnCount int32
		restore := SetSessionFactoryForTest(func(ccLaunchSpec) (ccSessionHandle, error) {
			atomic.AddInt32(&spawnCount, 1)
			return &fakeCCHandle{
				id: "fresh-sid",
				stream: &eventCCStream{events: []claudecode.Event{
					{Kind: claudecode.EventUsage, Usage: provider.Usage{PromptTokens: 1}},
					{Kind: claudecode.EventDone},
				}},
			}, nil
		})
		defer restore()

		r := New()
		stale := &claudeActive{handle: &fakeCCHandle{}, permissionMode: "default"}
		r.cache.Put(sessionKey(91), stale)

		Convey("When 这条会话又来一轮 Then 旧条目被驱逐、重新 spawn", func() {
			events, _, err := r.Run(context.Background(), agentruntime.RunRequest{
				Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)},
				SessionID: 91,
				Cwd:       t.TempDir(),
				UserText:  "hi",
			})
			So(err, ShouldBeNil)
			for range events { //nolint:revive // drain
			}

			So(atomic.LoadInt32(&spawnCount), ShouldEqual, 1)
			v, ok := r.cache.Get(sessionKey(91))
			So(ok, ShouldBeTrue)
			So(v, ShouldNotEqual, stale)
		})
	})
}

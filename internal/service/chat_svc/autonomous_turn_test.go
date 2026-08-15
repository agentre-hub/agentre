package chat_svc_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/agents/provider"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/mock_agentruntime"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/runtimes/remote"
	"github.com/agentre-ai/agentre/internal/service/chat_svc"
)

// abortRecordingRunner 是一个最小的 agentruntime.Runtime + agentruntime.Aborter
// fake,用来断言 driveAutonomousTurn 落库失败分支(结果 3)确实请求了中断,以及
// 中断失败(abortErr,含"子进程已消失"场景)不影响其余可观察结果。
//
// abortTokens 记录每次 Abort 收到的 turnToken,供断言「异步中断携带失败轮的 token」。
// turnKind 配置 Abort 返回的被中断轮类型(决策 1 的 AbortOutcome),让
// reconcileOrphanStop 的分流(自主轮 vs subagent 活动轮)可测。
type abortRecordingRunner struct {
	mu          sync.Mutex
	abortCalls  []int64
	abortTokens []uint64
	abortErr    error
	turnKind    agentruntime.TurnKind
}

func (*abortRecordingRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{}
}

func (*abortRecordingRunner) Run(context.Context, agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	return nil, nil, errors.New("abortRecordingRunner: Run must not be called")
}

func (r *abortRecordingRunner) Abort(_ context.Context, sessionID int64, turnToken uint64) (agentruntime.AbortOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.abortCalls = append(r.abortCalls, sessionID)
	r.abortTokens = append(r.abortTokens, turnToken)
	return agentruntime.AbortOutcome{TurnKind: r.turnKind}, r.abortErr
}

func (r *abortRecordingRunner) Calls() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.abortCalls...)
}

func (r *abortRecordingRunner) Tokens() []uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]uint64(nil), r.abortTokens...)
}

// joinAbort 在**父作用域**里等本遍那个异步中断落地,再放叶子断言跑。
//
// failAutonomousTurnPersist 的中断是 fire-and-forget goroutine(同步等回执会与抽干
// 互相死等,见那里的注释),而 goconvey 为每个叶子重跑一遍父作用域、每遍各
// SwapRuntimeForTest 一个新 runner。不在每遍收干净的话,上一遍还没跑到
// selectRunner 的 goroutine 会解析到**下一遍**的 runner,把中断记到它头上 ——
// 断言随即看到 []int64{100, 100}(GOMAXPROCS=1 下稳定复现)。goroutine 一旦记下
// 调用就不再碰注册表,所以等到调用出现即等于本遍已收口。
func joinAbort(t *testing.T, runner interface{ Calls() []int64 }) {
	t.Helper()
	assert.Eventually(t, func() bool {
		return len(runner.Calls()) == 1
	}, 2*time.Second, 5*time.Millisecond, "中断应被异步下发到 runtime")
}

// autoTurnRunner 是同时实现 agentruntime.Runtime + AutonomousTurnSource 的 fake,
// 用来验证 runTurn 的挂载 type-assert(走 builtin Send 路径,比 claudecode 简单)。
type autoTurnRunner struct {
	autoCh chan agentruntime.AutonomousTurn
}

func (*autoTurnRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Set: map[capability.Capability]bool{capability.CapImageInput: true},
		PermissionModeMeta: capability.PermissionModeMeta{
			AllowedModes:         []string{"default", "acceptEdits", "plan", "bypassPermissions"},
			DefaultMode:          "acceptEdits",
			SwitchableDuringTurn: true,
		},
	}
}

func (*autoTurnRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	events := make(chan agentruntime.Event, 1)
	events <- agentruntime.TextDelta{Text: "ok"}
	close(events)
	return events, &agentruntime.RunResult{ProviderSessionID: "builtin-100"}, nil
}

func (r *autoTurnRunner) AutonomousTurns(int64) <-chan agentruntime.AutonomousTurn { return r.autoCh }

// TestDriveAutonomousTurn_PersistsPureAssistantTurn 是 Phase 3 基石:一轮自主续轮
// 落成 **纯 assistant 消息(无 user 行)**,经会话级旁路通知前端 + 实时 stream +
// 收尾翻 idle。
func TestDriveAutonomousTurn_PersistsPureAssistantTurn(t *testing.T) {
	convey.Convey("自主续轮落纯 assistant 轮(无 user 行)", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle", ProviderSessionID: "sess-abc"}
		be := &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"}

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil).AnyTimes()

		m.dbMock.ExpectBegin()
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(5, nil)
		var createdRoles []string
		m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				createdRoles = append(createdRoles, msg.Role)
				msg.ID = 2001
				return nil
			}).Times(1)
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		m.dbMock.ExpectCommit()
		m.message.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		evs := make(chan agentruntime.Event, 2)
		evs <- agentruntime.TextDelta{Text: "autonomous:listing"}
		close(evs)
		at := agentruntime.AutonomousTurn{
			Events: evs,
			Result: &agentruntime.RunResult{
				ProviderSessionID: "sess-abc",
				Model:             "claude-sonnet-4-6",
				Usage:             &provider.Usage{PromptTokens: 2, CompletionTokens: 2},
			},
			Trigger: "background_task",
		}

		chat_svc.DriveAutonomousTurnForTest(ctx, m.svc, 100, be, at)

		convey.Convey("只建一条 assistant 消息,没有 user 行", func() {
			assert.Equal(t, []string{"assistant"}, createdRoles)
		})

		var (
			startedName    string
			startedStream  string
			startedTrigger string
			startedHasMsg  bool
			sawStarted     bool
			sawDone        bool
			chunk          string
		)
		for _, ev := range m.events {
			p, ok := ev.Payload.(chat_svc.ChatStreamEvent)
			if !ok {
				continue
			}
			switch p.Kind {
			case chat_svc.StreamAutonomousStarted:
				sawStarted = true
				startedName = ev.Name
				startedStream = p.Stream
				startedTrigger = p.Trigger
				startedHasMsg = p.AssistantMessage != nil
			case chat_svc.StreamChunk:
				chunk += p.Delta
			case chat_svc.StreamDone:
				sawDone = true
			}
		}

		convey.Convey("emit 会话级 StreamAutonomousStarted(带 per-turn stream + 新 assistant 行)", func() {
			assert.True(t, sawStarted, "应 emit StreamAutonomousStarted")
			assert.Equal(t, chat_svc.AutonomousStreamName(100), startedName)
			assert.Equal(t, chat_svc.StreamName(100, 2001), startedStream)
			assert.Equal(t, "background_task", startedTrigger)
			assert.True(t, startedHasMsg, "应携带 AssistantMessage 供前端插入")
		})

		convey.Convey("实时 stream chunk + StreamDone", func() {
			assert.Contains(t, chunk, "autonomous:listing")
			assert.True(t, sawDone, "应 emit StreamDone")
		})

		convey.Convey("session 收尾翻 idle", func() {
			assert.Equal(t, "idle", sess.AgentStatus)
			var idleIdx, doneIdx = -1, -1
			for i, ev := range m.events {
				p, ok := ev.Payload.(chat_svc.ChatStreamEvent)
				if !ok {
					continue
				}
				if idleIdx < 0 && p.Kind == chat_svc.StreamSessionStatus &&
					p.SessionStatus != nil && p.SessionStatus.AgentStatus == "idle" {
					idleIdx = i
				}
				if doneIdx < 0 && p.Kind == chat_svc.StreamDone {
					doneIdx = i
				}
			}
			require.GreaterOrEqual(t, idleIdx, 0, "自主轮收尾缺 session_status(idle)")
			require.GreaterOrEqual(t, doneIdx, 0, "自主轮收尾缺 StreamDone")
			assert.Less(t, idleIdx, doneIdx, "自主轮 idle 必须先于 StreamDone")
		})

		convey.Convey("会话级流补发 StreamAutonomousFinished 兜底(带收尾 assistant 消息 id)", func() {
			var (
				finIdx, closedIdx = -1, -1
				finLaunch         int64
				finName           string
			)
			for i, ev := range m.events {
				p, ok := ev.Payload.(chat_svc.ChatStreamEvent)
				if !ok {
					continue
				}
				if p.Kind == chat_svc.StreamAutonomousFinished {
					finIdx = i
					finLaunch = p.LaunchMessageID
					finName = ev.Name
				}
				if p.Kind == chat_svc.StreamClosed {
					closedIdx = i
				}
			}
			require.GreaterOrEqual(t, finIdx, 0, "自主轮收尾缺 StreamAutonomousFinished")
			assert.Equal(t, chat_svc.AutonomousStreamName(100), finName, "兜底终态必须走会话级流")
			assert.Equal(t, int64(2001), finLaunch, "应携带收尾 assistant 消息 id")
			assert.Greater(t, finIdx, closedIdx, "兜底终态在 per-turn StreamClosed 之后补发")
		})
	})
}

// TestDriveAutonomousTurn_TruncatedTurn_PersistsTerminatedNotCompleted 锁定 F3 留下的
// 线头:远端断连时 remote.Runtime 会 close 在飞自主轮的 events 并把终止理由放进
// RunResult.StopErr,而 driveAutonomousTurn 此前完全不看 StopErr —— 一条**被截断**的
// 助手消息于是以「正常跑完」的样子落库(errorText 空、会话翻 idle、emit StreamDone),
// 用户看到的是一条戛然而止却「成功」的回答,分不出这是打断还是答完了。
func TestDriveAutonomousTurn_TruncatedTurn_PersistsTerminatedNotCompleted(t *testing.T) {
	convey.Convey("被打断的自主轮落成终态,而不是正常完成", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle", ProviderSessionID: "sess-abc"}
		be := &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"}

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil).AnyTimes()

		m.dbMock.ExpectBegin()
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(5, nil)
		m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				msg.ID = 2001
				return nil
			}).Times(1)
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		m.dbMock.ExpectCommit()

		var final *chat_entity.Message
		m.message.EXPECT().Update(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				cp := *msg
				final = &cp
				return nil
			}).AnyTimes()

		evs := make(chan agentruntime.Event, 1)
		evs <- agentruntime.TextDelta{Text: "half a sen"}
		close(evs)
		at := agentruntime.AutonomousTurn{
			Events:  evs,
			Result:  &agentruntime.RunResult{StopErr: remote.ErrRunInterrupted},
			Trigger: "background_task",
		}

		chat_svc.DriveAutonomousTurnForTest(ctx, m.svc, 100, be, at)

		convey.Convey("终止理由留在消息文案里,而不是一条空 errorText 的「成功」消息", func() {
			require.NotNil(t, final)
			assert.NotEmpty(t, final.ErrorText,
				"被截断的自主轮必须留下终态文案,否则与正常答完无法区分")
			assert.NotContains(t, final.ErrorText, "agentruntime/runtimes/remote:",
				"终态文案是给用户看的,不是 Go 哨兵字符串")
		})

		convey.Convey("会话落既有的 error 态,而不是 idle", func() {
			assert.Equal(t, "error", sess.AgentStatus)
			assert.False(t, sess.NeedsAttention)
		})

		convey.Convey("收尾 emit StreamError 而不是 StreamDone", func() {
			var sawErr, sawDone bool
			for _, ev := range m.events {
				p, ok := ev.Payload.(chat_svc.ChatStreamEvent)
				if !ok {
					continue
				}
				switch p.Kind {
				case chat_svc.StreamError:
					sawErr = true
					assert.NotEmpty(t, p.Error)
				case chat_svc.StreamDone:
					sawDone = true
				}
			}
			assert.True(t, sawErr, "被打断的自主轮收尾必须 emit StreamError")
			assert.False(t, sawDone, "被打断的一轮不是正常完成,不得 emit StreamDone")
		})
	})
}

// TestDriveAutonomousTurn_BrowserInitiatedRound_PersistsUserMessageWithSource 是 R18 的
// 基石:浏览器在一条空闲会话上「开新一轮」跑起的一轮,daemon 在事件流开头注入一条
// user_message 标记(带发起方设备身份)。driveAutonomousTurn 据它在转录里落成
// **一行用户消息 + 一行 assistant**,用户消息带来源标识 —— 不退化成纯 assistant 轮
// (「没有提问的回复」),与真·自主续轮在界面上可区分。
func TestDriveAutonomousTurn_BrowserInitiatedRound_PersistsUserMessageWithSource(t *testing.T) {
	convey.Convey("浏览器发起的一轮落成用户消息 + assistant,用户消息带来源标识", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle", ProviderSessionID: "sess-abc"}
		be := &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"}

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil).AnyTimes()

		var createdRoles []string
		var createdUser *chat_entity.Message
		m.dbMock.ExpectBegin()
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(5, nil)
		m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				createdRoles = append(createdRoles, msg.Role)
				if msg.Role == "user" {
					cp := *msg
					createdUser = &cp
				}
				msg.ID = 2001 + int64(len(createdRoles))
				return nil
			}).Times(2)
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		m.dbMock.ExpectCommit()
		m.message.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		// 事件流开头是 daemon 注入的 user_message 标记,后面才是后端真正的事件。
		evs := make(chan agentruntime.Event, 3)
		evs <- agentruntime.UserMessageEvent{
			Text:             "浏览器发来的消息",
			SourceDevice:     "sha256:web-device",
			SourceDeviceName: "Chrome · macOS",
		}
		evs <- agentruntime.TextDelta{Text: "reply"}
		close(evs)
		at := agentruntime.AutonomousTurn{
			Events:  evs,
			Result:  &agentruntime.RunResult{ProviderSessionID: "sess-abc", Model: "claude-sonnet-4-6"},
			Trigger: "catch_up",
		}

		chat_svc.DriveAutonomousTurnForTest(ctx, m.svc, 100, be, at)

		convey.Convey("落库两条消息:先 user 后 assistant(顺序正确)", func() {
			assert.Equal(t, []string{"user", "assistant"}, createdRoles)
			if assert.NotNil(t, createdUser) {
				assert.Equal(t, "user", createdUser.Role)
				assert.Equal(t, 5, createdUser.Seq)
				assert.Contains(t, createdUser.BlocksJSON, "浏览器发来的消息")
			}
		})

		convey.Convey("来源写进了落库行:刷新 / 重开会话后经转录读路径仍读得回来", func() {
			require.NotNil(t, createdUser, "浏览器发起的一轮必须落一行 user 消息")
			// 走真实读路径(toChatMessage → peerMessageSourceOf),而不是在 BlocksJSON
			// 里找子串:用户看到的 pill 就是这条路径投影出来的。来源只挂在实时事件上时,
			// 桌面端当场显示「来自 Chrome · macOS」,刷新之后 pill 消失,那行用户消息
			// 看起来像本机自己打的字 —— 多设备协作分不出哪句是谁在哪儿发的。
			reloaded, err := chat_svc.ToChatMessageForTest(createdUser)
			require.NoError(t, err)
			assert.Equal(t, "sha256:web-device", reloaded.SourceDevice,
				"来源必须落库,否则刷新后来源标识消失")
			assert.Equal(t, "Chrome · macOS", reloaded.SourceDeviceName)
		})

		var started *chat_svc.ChatStreamEvent
		for _, ev := range m.events {
			p, ok := ev.Payload.(chat_svc.ChatStreamEvent)
			if !ok {
				continue
			}
			if p.Kind == chat_svc.StreamAutonomousStarted {
				cp := p
				started = &cp
			}
		}

		convey.Convey("StreamAutonomousStarted 携带用户消息且带来源标识(R18 守卫)", func() {
			require.NotNil(t, started, "应 emit StreamAutonomousStarted")
			require.Len(t, started.UserMessages, 1, "浏览器发起的一轮必须带 user 行,不能退化成纯 assistant 轮")
			um := started.UserMessages[0]
			assert.Equal(t, "user", um.Role)
			assert.Equal(t, "sha256:web-device", um.SourceDevice)
			assert.Equal(t, "Chrome · macOS", um.SourceDeviceName)
			assert.Equal(t, "浏览器发来的消息", um.Blocks[0].Text)
		})

		convey.Convey("stream chunk 落在 assistant 上(用户文本不进 assistant)", func() {
			var chunk string
			for _, ev := range m.events {
				p, ok := ev.Payload.(chat_svc.ChatStreamEvent)
				if !ok {
					continue
				}
				if p.Kind == chat_svc.StreamChunk {
					chunk += p.Delta
				}
			}
			assert.Equal(t, "reply", chunk)
		})
	})
}

// TestDriveAutonomousTurn_BrowserInitiatedRound_NameMissing_FallsBackWithoutBlocking 是
// R19 的守卫:发起方没有声明显示名时,用户消息照常落库落地,来源标识回退由前端处理
// (sourceDeviceName 为空 → 前端回退指纹),不阻塞消息落地。
func TestDriveAutonomousTurn_BrowserInitiatedRound_NameMissing_FallsBackWithoutBlocking(t *testing.T) {
	convey.Convey("名字缺失不阻塞:仍落 user + assistant,来源设备指纹在", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle", ProviderSessionID: "sess-abc"}
		be := &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"}

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil).AnyTimes()

		var createdRoles []string
		var createdUser *chat_entity.Message
		m.dbMock.ExpectBegin()
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(7, nil)
		m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				createdRoles = append(createdRoles, msg.Role)
				if msg.Role == "user" {
					cp := *msg
					createdUser = &cp
				}
				msg.ID = 3001 + int64(len(createdRoles))
				return nil
			}).Times(2)
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		m.dbMock.ExpectCommit()
		m.message.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		evs := make(chan agentruntime.Event, 2)
		evs <- agentruntime.UserMessageEvent{Text: "继续", SourceDevice: "sha256:web-device"}
		close(evs)
		at := agentruntime.AutonomousTurn{
			Events:  evs,
			Result:  &agentruntime.RunResult{ProviderSessionID: "sess-abc"},
			Trigger: "catch_up",
		}

		chat_svc.DriveAutonomousTurnForTest(ctx, m.svc, 100, be, at)

		convey.Convey("名字缺失仍落 user + assistant(不阻塞落地)", func() {
			assert.Equal(t, []string{"user", "assistant"}, createdRoles)
		})

		var started *chat_svc.ChatStreamEvent
		for _, ev := range m.events {
			p, ok := ev.Payload.(chat_svc.ChatStreamEvent)
			if !ok {
				continue
			}
			if p.Kind == chat_svc.StreamAutonomousStarted {
				cp := p
				started = &cp
			}
		}
		require.NotNil(t, started, "应 emit StreamAutonomousStarted")
		if assert.Len(t, started.UserMessages, 1) {
			assert.Equal(t, "sha256:web-device", started.UserMessages[0].SourceDevice)
			assert.Empty(t, started.UserMessages[0].SourceDeviceName, "名字缺失保持空,由前端回退指纹")
		}

		convey.Convey("名字缺失时落库仍带指纹,刷新后读得回来(只是没有名字)", func() {
			require.NotNil(t, createdUser)
			reloaded, err := chat_svc.ToChatMessageForTest(createdUser)
			require.NoError(t, err)
			assert.Equal(t, "sha256:web-device", reloaded.SourceDevice)
			assert.Empty(t, reloaded.SourceDeviceName, "名字缺失不落一个空名字字段")
		})
	})
}

// TestDriveAutonomousTurn_LocalInitiatedRound_PersistsUserMessageWithoutSource 是 R22
// 的单端零变化守卫:这一轮由本机自己发起(user_message 标记不带 SourceDevice)时,
// 落库行**逐字节不含来源字段**、转录读路径投影出的 sourceDevice 为空、实时事件里也
// 为空 —— 前端因此不渲染任何「来自 …」pill,单端使用的呈现与今天一致。
// R18/R21 那条落库写点(persistPeerMessageSource)对空 source 必须是 no-op。
func TestDriveAutonomousTurn_LocalInitiatedRound_PersistsUserMessageWithoutSource(t *testing.T) {
	convey.Convey("本机发起的一轮:落库行不带来源字段,DTO 里 sourceDevice 为空", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle", ProviderSessionID: "sess-abc"}
		be := &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"}

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil).AnyTimes()

		var createdRoles []string
		var createdUser *chat_entity.Message
		m.dbMock.ExpectBegin()
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(5, nil)
		m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				createdRoles = append(createdRoles, msg.Role)
				if msg.Role == "user" {
					cp := *msg
					createdUser = &cp
				}
				msg.ID = 4001 + int64(len(createdRoles))
				return nil
			}).Times(2)
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		m.dbMock.ExpectCommit()
		m.message.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		evs := make(chan agentruntime.Event, 2)
		evs <- agentruntime.UserMessageEvent{Text: "本机发的消息"}
		evs <- agentruntime.TextDelta{Text: "reply"}
		close(evs)
		at := agentruntime.AutonomousTurn{
			Events:  evs,
			Result:  &agentruntime.RunResult{ProviderSessionID: "sess-abc"},
			Trigger: "catch_up",
		}

		chat_svc.DriveAutonomousTurnForTest(ctx, m.svc, 100, be, at)

		convey.Convey("照常落 user + assistant 两行", func() {
			assert.Equal(t, []string{"user", "assistant"}, createdRoles)
		})

		convey.Convey("落库行里没有来源字段(R22:呈现与今天逐像素一致)", func() {
			require.NotNil(t, createdUser)
			assert.Contains(t, createdUser.BlocksJSON, "本机发的消息")
			assert.NotContains(t, createdUser.BlocksJSON, "sourceDevice",
				"本机发的消息不得因为经过 persistPeerMessageSource 而多出来源字段")
			assert.NotContains(t, createdUser.BlocksJSON, "sourceDeviceName")
		})

		convey.Convey("重载与实时两条读路径的 sourceDevice 都为空", func() {
			require.NotNil(t, createdUser)
			reloaded, err := chat_svc.ToChatMessageForTest(createdUser)
			require.NoError(t, err)
			assert.Empty(t, reloaded.SourceDevice)
			assert.Empty(t, reloaded.SourceDeviceName)

			var started *chat_svc.ChatStreamEvent
			for _, ev := range m.events {
				p, ok := ev.Payload.(chat_svc.ChatStreamEvent)
				if ok && p.Kind == chat_svc.StreamAutonomousStarted {
					cp := p
					started = &cp
				}
			}
			require.NotNil(t, started, "应 emit StreamAutonomousStarted")
			require.Len(t, started.UserMessages, 1)
			assert.Empty(t, started.UserMessages[0].SourceDevice, "本机发起的一轮不带来源标识")
			assert.Empty(t, started.UserMessages[0].SourceDeviceName)
		})
	})
}

// TestDriveAutonomousTurn_PersistFailure_FlipsErrorEmitsAndInterrupts 覆盖落库
// 最终失败(新建 assistant 消息的事务失败)时的四个可观察结果(design decisions
// 6/7/9 + spec"自主续轮落库失败时的可观察结果"):会话翻 error 并持久化、经会话级
// 流推错误事件(文案复用 mapTurnError,与用户发起的轮次一致)、主动中断 CLI 当前
// 这一轮、事件流仍被抽干。此前(autonomous_turn.go:89-94)只记日志 + drainAndDiscard,
// 这四项全部缺失 —— 一个需要用户回答的交互帧因此把 CLI 子进程永久焊死。
func TestDriveAutonomousTurn_PersistFailure_FlipsErrorEmitsAndInterrupts(t *testing.T) {
	convey.Convey("落库最终失败:会话翻 error + 会话级错误事件 + 中断 CLI + 抽干事件", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle", ProviderSessionID: "sess-abc"}
		be := &agent_backend_entity.AgentBackend{ID: 12, Type: string(agent_backend_entity.TypeClaudeCode)}

		runner := &abortRecordingRunner{}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil).AnyTimes()

		persistErr := errors.New("disk I/O error")
		m.dbMock.ExpectBegin()
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(0, persistErr)
		m.dbMock.ExpectRollback()

		var updatedStatus string
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
				updatedStatus = s.AgentStatus
				return nil
			}).Times(1)

		evs := make(chan agentruntime.Event, 2)
		evs <- agentruntime.TextDelta{Text: "must not be read by the dispatcher"}
		evs <- agentruntime.TextDelta{Text: "must not be read by the dispatcher either"}
		close(evs)
		at := agentruntime.AutonomousTurn{Events: evs, Trigger: "background_task", TurnToken: 7}

		chat_svc.DriveAutonomousTurnForTest(ctx, m.svc, 100, be, at)
		// 中断是异步发出的(见 failAutonomousTurnPersist:同步等回执会与第 4 步的抽干
		// 互相死等),在父作用域里等它落地 —— 叶子里才拿得到确定的调用序列。
		joinAbort(t, runner)

		convey.Convey("1. 会话翻 error 并持久化(独立于失败的消息写事务,允许独立成功)", func() {
			assert.Equal(t, "error", sess.AgentStatus)
			assert.Equal(t, "error", updatedStatus, "应经独立的一次 Session().Update 落库")
		})

		convey.Convey("2. 经会话级流推错误事件,文案复用 mapTurnError(与用户发起的轮次一致)", func() {
			var sawErr bool
			var errText, name string
			for _, ev := range m.events {
				p, ok := ev.Payload.(chat_svc.ChatStreamEvent)
				if !ok || p.Kind != chat_svc.StreamError {
					continue
				}
				sawErr = true
				errText = p.Error
				name = ev.Name
			}
			assert.True(t, sawErr, "应经会话级流 emit 一条 StreamError")
			assert.Equal(t, chat_svc.AutonomousStreamName(100), name, "必须走会话级流,不依赖数据库")
			assert.Equal(t, persistErr.Error(), errText,
				"文案应来自 mapTurnError(此处无特判分支,原样透传落库失败原因)")
		})

		convey.Convey("3. 主动中断 CLI 当前这一轮,使子进程解除等待", func() {
			assert.Equal(t, []int64{100}, runner.Calls(), "应请求中断这一轮")
			assert.Equal(t, []uint64{7}, runner.Tokens(),
				"异步中断必须携带失败轮的 per-turn token,精确寻址(决策 1)")
		})

		convey.Convey("4. Hard invariant:事件流仍被抽干,发生在前三步之后", func() {
			_, ok := <-evs
			assert.False(t, ok, "events channel 应已被抽干,不能有残留未读事件")
		})
	})
}

// TestDriveAutonomousTurn_PersistFailure_InterruptFailureDoesNotAffectOtherResults
// 覆盖 spec 明确要求的边界:"中断失败(含子进程已消失)只记日志,不影响前两步已经
// 产生的可观察结果"。用一个 Abort 返回错误的 runner 验证会话仍翻 error、会话级流
// 仍收到错误事件、事件流仍被抽干,且整个处理过程不 panic、不阻塞。
func TestDriveAutonomousTurn_PersistFailure_InterruptFailureDoesNotAffectOtherResults(t *testing.T) {
	convey.Convey("中断失败不影响会话翻 error / 会话级错误事件 / 抽干事件", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle", ProviderSessionID: "sess-abc"}
		be := &agent_backend_entity.AgentBackend{ID: 12, Type: string(agent_backend_entity.TypeClaudeCode)}

		runner := &abortRecordingRunner{abortErr: errors.New("subprocess already gone")}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil).AnyTimes()

		persistErr := errors.New("disk I/O error")
		m.dbMock.ExpectBegin()
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(0, persistErr)
		m.dbMock.ExpectRollback()
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).Times(1)

		evs := make(chan agentruntime.Event, 1)
		evs <- agentruntime.TextDelta{Text: "must not be read by the dispatcher"}
		close(evs)
		at := agentruntime.AutonomousTurn{Events: evs, Trigger: "background_task"}

		require.NotPanics(t, func() {
			chat_svc.DriveAutonomousTurnForTest(ctx, m.svc, 100, be, at)
		})
		joinAbort(t, runner) // 同上:中断异步发出,父作用域里收干净再断言。

		convey.Convey("中断确实被尝试过(否则谈不上'失败')", func() {
			assert.Equal(t, []int64{100}, runner.Calls(), "应请求中断这一轮")
		})

		convey.Convey("会话仍翻 error 并持久化", func() {
			assert.Equal(t, "error", sess.AgentStatus)
		})

		convey.Convey("会话级流仍收到错误事件", func() {
			var sawErr bool
			for _, ev := range m.events {
				p, ok := ev.Payload.(chat_svc.ChatStreamEvent)
				if ok && p.Kind == chat_svc.StreamError && ev.Name == chat_svc.AutonomousStreamName(100) {
					sawErr = true
				}
			}
			assert.True(t, sawErr, "中断失败不得吞掉已经产生的错误事件")
		})

		convey.Convey("事件流仍被抽干", func() {
			_, ok := <-evs
			assert.False(t, ok, "events channel 应已被抽干,不能有残留未读事件")
		})
	})
}

// abortWaitsForDrainRunner 复刻真实 claudecode.Runtime.Abort 的**阻塞形态**:它调
// Session.Interrupt,后者把 control_request 写进 stdin 后阻塞等 CLI 的 control_response
// (pkg/claudecode/session.go:769 的 select);而那条回执只能由常驻 readLoop 派发,
// readLoop 又可能正停在 feed(at.ch <- ev) 上 —— 只有本轮事件流继续被消费,它才前进。
// 于是 Abort 的完成**依赖 drain 继续进行**。ctx 是 startAutonomousWatcher 传的
// context.Background(),没有 deadline,等不到回执就是永久等待。
//
// drained 关闭 = 本轮事件被读完(drain 已跑完);giveUp 是测试收尾兜底,免得断言失败
// 时留下一个永久阻塞的 goroutine。
type abortWaitsForDrainRunner struct {
	abortRecordingRunner
	drained <-chan struct{}
	giveUp  <-chan struct{}
}

func (r *abortWaitsForDrainRunner) Abort(ctx context.Context, sessionID int64, turnToken uint64) (agentruntime.AbortOutcome, error) {
	outcome, err := r.abortRecordingRunner.Abort(ctx, sessionID, turnToken)
	select {
	case <-r.drained:
	case <-r.giveUp:
	}
	return outcome, err
}

// TestDriveAutonomousTurn_PersistFailure_InterruptDoesNotBlockWatcher 钉死 spec
// 「自主续轮落库失败时的可观察结果」的收尾约束:「失败处置本身不得抛出或**阻塞
// watcher goroutine**:startAutonomousWatcher 是每会话单 goroutine 顺序处理,一轮的
// 失败处置卡住会波及该会话所有后续自主轮」,以及 Hard invariant 的「本轮新增的失败
// 处置全部在 drain 之外**或以非阻塞方式进行**」。
//
// 中断与抽干之间有真实的循环依赖:中断要等 CLI 回执 → 回执要等 readLoop 前进 →
// readLoop 要等本轮事件被消费 → 消费就是第 4 步的抽干。把中断**同步**摆在抽干之前,
// 两者就互相等着,watcher goroutine 永久卡死 —— 比修复前的静默丢弃更糟:连事件流都
// 不再被抽干,Session 活跃槽位永不释放。真实管道只有 48 格缓冲(pkg/claudecode
// activeTurn.ch 16 + runtimes/claudecode autoturn evOut 32),事故那轮 60+ 帧足以填满。
// 中断必须以非阻塞方式发出,让抽干得以进行、回执随之到达。
func TestDriveAutonomousTurn_PersistFailure_InterruptDoesNotBlockWatcher(t *testing.T) {
	convey.Convey("Given 中断要等 CLI 回执、回执要等事件流继续被消费, When 落库失败触发失败处置, Then 处置不得卡住 watcher goroutine", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle", ProviderSessionID: "sess-abc"}
		be := &agent_backend_entity.AgentBackend{ID: 12, Type: string(agent_backend_entity.TypeClaudeCode)}

		giveUp := make(chan struct{})
		var giveUpOnce sync.Once
		t.Cleanup(func() { giveUpOnce.Do(func() { close(giveUp) }) })

		// 无缓冲 evs = 真实管道 48 格填满之后的形态:生产者必须等消费者读。
		evs := make(chan agentruntime.Event)
		drained := make(chan struct{})
		go func() {
			defer close(drained)
			defer close(evs)
			for i := 0; i < 3; i++ {
				select {
				case evs <- agentruntime.TextDelta{Text: "frame produced while the turn is still live"}:
				case <-giveUp:
					return
				}
			}
		}()

		runner := &abortWaitsForDrainRunner{drained: drained, giveUp: giveUp}
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, runner)
		t.Cleanup(restore)

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil).AnyTimes()

		persistErr := errors.New("database is locked (5) (SQLITE_BUSY)")
		m.dbMock.ExpectBegin()
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(0, persistErr)
		m.dbMock.ExpectRollback()
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).Times(1)

		at := agentruntime.AutonomousTurn{Events: evs, Trigger: "background_task"}

		done := make(chan struct{})
		go func() {
			defer close(done)
			chat_svc.DriveAutonomousTurnForTest(ctx, m.svc, 100, be, at)
		}()

		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("失败处置卡住了 watcher goroutine:中断同步等回执、回执等抽干、抽干排在中断之后 —— 死锁")
		}
		joinAbort(t, runner) // 同上:中断异步发出,父作用域里收干净再断言。

		convey.Convey("中断仍然被发出(非阻塞不等于不发)", func() {
			assert.Equal(t, []int64{100}, runner.Calls(), "异步不等于不发:中断必须真的被请求")
		})

		convey.Convey("事件流仍被抽干", func() {
			_, ok := <-evs
			assert.False(t, ok, "events channel 应已被抽干,不能有残留未读事件")
		})

		convey.Convey("会话仍翻 error 并持久化", func() {
			assert.Equal(t, "error", sess.AgentStatus)
		})
	})
}

// TestDriveAutonomousTurn_BackgroundTaskCompletionFlipsAndEmits 验证 Phase 3:
// 自主轮带 CompletedTask 时,(a) emit 的 StreamAutonomousStarted 携带 CompletedTask
// 身份(toolUseId+status),(b) finalize 后定向调 FlipSubagentStatus 把上一条消息里
// 的 subagent_state 块翻成 completed。
func TestDriveAutonomousTurn_BackgroundTaskCompletionFlipsAndEmits(t *testing.T) {
	convey.Convey("后台任务完成的自主轮回流完成 + 定向翻转", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle", ProviderSessionID: "sess-abc"}
		be := &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"}

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil).AnyTimes()

		m.dbMock.ExpectBegin()
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(5, nil)
		m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				msg.ID = 2001
				return nil
			}).Times(1)
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		m.dbMock.ExpectCommit()
		m.message.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		// 关键断言:finalize 后定向翻转上一条消息里的 subagent_state（含 summary）。
		m.message.EXPECT().
			FlipSubagentStatus(gomock.Any(), int64(100), "tu1", "completed", "Background command completed").
			Return(nil).Times(1)

		evs := make(chan agentruntime.Event, 1)
		evs <- agentruntime.TextDelta{Text: "autonomous:done"}
		close(evs)
		at := agentruntime.AutonomousTurn{
			Events:  evs,
			Result:  &agentruntime.RunResult{ProviderSessionID: "sess-abc"},
			Trigger: "background_task",
			CompletedTask: &agentruntime.CompletedBackgroundTask{
				ToolUseID: "tu1",
				Status:    "completed",
				Summary:   "Background command completed",
			},
		}

		chat_svc.DriveAutonomousTurnForTest(ctx, m.svc, 100, be, at)

		var started *chat_svc.ChatStreamEvent
		for _, ev := range m.events {
			p, ok := ev.Payload.(chat_svc.ChatStreamEvent)
			if !ok {
				continue
			}
			if p.Kind == chat_svc.StreamAutonomousStarted {
				cp := p
				started = &cp
			}
		}

		convey.Convey("emit 的 StreamAutonomousStarted 携带 CompletedTask 身份(含 summary)", func() {
			require.NotNil(t, started, "应 emit StreamAutonomousStarted")
			require.NotNil(t, started.CompletedTask, "应携带 CompletedTask")
			assert.Equal(t, "tu1", started.CompletedTask.ToolUseID)
			assert.Equal(t, "completed", started.CompletedTask.Status)
			assert.Equal(t, "Background command completed", started.CompletedTask.Summary)
		})
	})
}

// TestDriveAutonomousTurn_CancelsInFlightSubagent 验证 Fix 2:自主轮结束时仍 running
// 的 subagent_state(没等到 SubagentDone)被翻成 "canceled" 落库,镜像 Send 路径的
// MarkRunningSubagentsCancelled,避免后台任务芯片永远 spin。
func TestDriveAutonomousTurn_CancelsInFlightSubagent(t *testing.T) {
	convey.Convey("自主轮收尾把 in-flight subagent 翻成 canceled", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle", ProviderSessionID: "sess-abc"}
		be := &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"}

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil).AnyTimes()

		m.dbMock.ExpectBegin()
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(5, nil)
		m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				msg.ID = 2001
				return nil
			}).Times(1)
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		m.dbMock.ExpectCommit()

		// 捕获最终落库的 blocks_json(收尾 Update)。
		var finalBlocksJSON string
		m.message.EXPECT().Update(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				finalBlocksJSON = msg.BlocksJSON
				return nil
			}).AnyTimes()

		// 事件流:起一个 subagent,但没有对应 SubagentDone → 块停在 running。
		evs := make(chan agentruntime.Event, 2)
		evs <- agentruntime.SubagentStarted{
			ToolCallID: "sub-1",
			Info:       agentruntime.SubagentInfo{Kind: "local_agent", TaskDescription: "do work"},
		}
		evs <- agentruntime.TextDelta{Text: "working"}
		close(evs)
		at := agentruntime.AutonomousTurn{
			Events:  evs,
			Result:  &agentruntime.RunResult{ProviderSessionID: "sess-abc"},
			Trigger: "background_task",
		}

		chat_svc.DriveAutonomousTurnForTest(ctx, m.svc, 100, be, at)

		convey.Convey("in-flight subagent_state 落库为 canceled 而非 running", func() {
			require.NotEmpty(t, finalBlocksJSON, "应落库 assistant blocks")
			st := subagentStatusInBlocks(t, finalBlocksJSON, "sub-1")
			assert.Equal(t, "canceled", st)
		})
	})
}

// subagentStatusInBlocks 从 blocks_json 里取 parent_tool_call_id==toolUseID 的
// subagent_state 块的 status。
func subagentStatusInBlocks(t *testing.T, blocksJSON, toolUseID string) string {
	t.Helper()
	var stored []struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(blocksJSON), &stored))
	for _, sb := range stored {
		if sb.Type != "subagent_state" {
			continue
		}
		var data struct {
			ParentToolCallID string `json:"parent_tool_call_id"`
			Status           string `json:"status"`
		}
		require.NoError(t, json.Unmarshal(sb.Data, &data))
		if data.ParentToolCallID == toolUseID {
			return data.Status
		}
	}
	t.Fatalf("no subagent_state block for %s in %s", toolUseID, blocksJSON)
	return ""
}

// TestStartAutonomousWatcher_DedupesAndExitsOnClose 验证 watcher 生命周期:每会话
// 只起一个(去重),底层 AutonomousTurns channel close 后干净退出并清去重位。
func TestStartAutonomousWatcher_DedupesAndExitsOnClose(t *testing.T) {
	convey.Convey("watcher 每会话一个 + channel close 后退出", t, func() {
		m := setupChatTest(t)
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		src := mock_agentruntime.NewMockAutonomousTurnSource(ctrl)
		be := &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"}

		ch := make(chan *agentruntime.AutonomousTurn) // 不带值,保持 open
		called := make(chan struct{})
		// 用 <-chan(单向)返回。Times(1) 即验证去重:第二次 start 不再订阅。
		src.EXPECT().AutonomousTurns(int64(100)).
			DoAndReturn(func(int64) <-chan agentruntime.AutonomousTurn {
				out := make(chan agentruntime.AutonomousTurn)
				go func() {
					defer close(out)
					close(called)
					for at := range ch {
						out <- *at
					}
				}()
				return out
			}).Times(1)

		chat_svc.StartAutonomousWatcherForTest(m.svc, 100, be, src)
		<-called // watcher goroutine 已订阅,去重位已占
		assert.True(t, chat_svc.IsAutonomousWatcherActiveForTest(m.svc, 100))

		// 第二次 start:被去重,不再调 AutonomousTurns(Times(1) 验证)。
		chat_svc.StartAutonomousWatcherForTest(m.svc, 100, be, src)

		close(ch) // 让底层 channel close → watcher 退出
		require.Eventually(t, func() bool {
			return !chat_svc.IsAutonomousWatcherActiveForTest(m.svc, 100)
		}, time.Second, 5*time.Millisecond, "watcher 应在 channel close 后退出并清去重位")
	})
}

// TestRunTurn_MountsAutonomousWatcher 验证 runTurn 在 runner 实现 AutonomousTurnSource
// 时(Run 完成、session 已 spawn 后)惰性挂上每会话 watcher。
func TestRunTurn_MountsAutonomousWatcher(t *testing.T) {
	convey.Convey("runTurn 惰性挂 autonomous watcher", t, func() {
		t.Setenv("AGENTRE_DATA_DIR", t.TempDir())
		m := setupChatTest(t)
		ctx := m.ctx

		runner := &autoTurnRunner{autoCh: make(chan agentruntime.AutonomousTurn)}
		t.Cleanup(func() { close(runner.autoCh) }) // 让 watcher 在测试结束后退出,不泄漏
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, runner)
		t.Cleanup(restore)

		sess := &chat_entity.Session{ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE}
		backend := &agent_backend_entity.AgentBackend{ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-11", Status: consts.ACTIVE}
		ag := &agent_entity.Agent{ID: 7, Name: "Builtin", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`}
		prov := &llm_provider_entity.LLMProvider{ID: 11, ProviderKey: "key-11", Type: string(llm_provider_entity.TypeAnthropic), Status: consts.ACTIVE,
			Enabled: llm_provider_entity.EnabledOn, DefaultModelKey: "mk-key-11"}

		m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(sess, nil)
		m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(ag, nil)
		m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(backend, nil)
		m.provider.EXPECT().FindByKey(gomock.Any(), "key-11").Return(prov, nil).AnyTimes()
		expectProviderResolvable(m, "key-11")
		m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
		m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
		m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
		m.dbMock.ExpectBegin()
		m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
		m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
				if msg.Role == "user" {
					msg.ID = 1000
				} else {
					msg.ID = 1001
				}
				return nil
			}).Times(2)
		m.dbMock.ExpectCommit()

		resp, err := m.svc.Send(ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
		require.NoError(t, err)
		chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

		require.Eventually(t, func() bool {
			return chat_svc.IsAutonomousWatcherActiveForTest(m.svc, 100)
		}, time.Second, 5*time.Millisecond, "runTurn 应在 Run 后挂上 watcher")
	})
}

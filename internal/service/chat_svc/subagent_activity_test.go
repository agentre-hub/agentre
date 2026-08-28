package chat_svc_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/mock_agentruntime"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// launchMessageWithSubagentState 构造一条带 subagent_state(parent_tool_call_id=toolCallID)
// 的发起 assistant 消息,模拟后台 subagent 派遣卡所在的消息。
func launchMessageWithSubagentState(id, sessionID int64, toolCallID string) *chat_entity.Message {
	blocksJSON := `[` +
		`{"type":"tool_use","data":{"id":"` + toolCallID + `","name":"Task","input":{"description":"run something"}}},` +
		`{"type":"subagent_state","data":{"parent_tool_call_id":"` + toolCallID + `","kind":"local_agent","description":"run something","status":"running","nested_tool_call_ids":[]}}` +
		`]`
	return &chat_entity.Message{ID: id, SessionID: sessionID, Role: "assistant", BlocksJSON: blocksJSON, Seq: 4}
}

// TestDriveSubagentActivity_NestsChildrenAndPersists 是 Task 5 基石:一轮后台 subagent
// 内部活动流被嵌套渲染回发起卡(emit StreamSubagentActivityStarted),实时 stream,并把
// 新嵌套块跨消息落库进发起消息(AppendSubagentChildren)。
func TestDriveSubagentActivity_NestsChildrenAndPersists(t *testing.T) {
	convey.Convey("后台 subagent 活动流嵌套渲染回发起卡 + 跨消息落库", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		const sid = int64(100)
		const launchID = int64(2001)
		const toolCallID = "toolu_agent"
		be := &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"}

		sess := &chat_entity.Session{ID: sid, AgentID: 7, AgentStatus: "idle", ProviderSessionID: "sess-abc"}
		m.session.EXPECT().Find(gomock.Any(), sid).Return(sess, nil).AnyTimes()

		// 发起消息定位:返回带 subagent_state{parent_tool_call_id:toolu_agent} 的消息。
		launchMsg := launchMessageWithSubagentState(launchID, sid, toolCallID)
		m.message.EXPECT().
			FindAssistantBySubagentToolCallID(gomock.Any(), sid, toolCallID).
			Return(launchMsg, nil).Times(1)

		// 关键断言:收尾把新嵌套子块跨消息落库进发起消息。
		var gotChildJSON string
		var gotChildIDs []string
		m.message.EXPECT().
			AppendSubagentChildren(gomock.Any(), sid, toolCallID, gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ int64, _, childJSON string, childIDs []string) error {
				gotChildJSON = childJSON
				gotChildIDs = childIDs
				return nil
			}).Times(1)

		// 活动事件流:一个嵌套 ToolCall + 嵌套 ToolResult,然后 close。
		evs := make(chan agentruntime.Event, 2)
		evs <- agentruntime.ToolCall{ID: "sub_bash", Name: "Bash", ParentToolCallID: toolCallID, Input: json.RawMessage(`{"command":"ls"}`)}
		evs <- agentruntime.ToolResult{ToolCallID: "sub_bash", Content: "SUBAGENT_DONE", ParentToolCallID: toolCallID}
		close(evs)
		act := agentruntime.SubagentActivity{ToolCallID: toolCallID, Events: evs}

		chat_svc.DriveSubagentActivityForTest(ctx, m.svc, sid, be, act)

		var (
			sawStarted    bool
			startedName   string
			startedStream string
			startedTUID   string
			startedLaunch int64
			sawDone       bool
			doneStream    string
		)
		launchStream := chat_svc.StreamName(sid, launchID)
		for _, ev := range m.events {
			p, ok := ev.Payload.(chat_svc.ChatStreamEvent)
			if !ok {
				continue
			}
			switch p.Kind {
			case chat_svc.StreamSubagentActivityStarted:
				sawStarted = true
				startedName = ev.Name
				startedStream = p.Stream
				startedTUID = p.ToolCallID
				startedLaunch = p.LaunchMessageID
			case chat_svc.StreamDone:
				if ev.Name == launchStream {
					sawDone = true
					doneStream = ev.Name
				}
			}
		}

		convey.Convey("emit 会话级 StreamSubagentActivityStarted(带发起消息 id + tool_use_id)", func() {
			assert.True(t, sawStarted, "应 emit StreamSubagentActivityStarted")
			assert.Equal(t, chat_svc.AutonomousStreamName(sid), startedName)
			assert.Equal(t, launchStream, startedStream)
			assert.Equal(t, toolCallID, startedTUID)
			assert.Equal(t, launchID, startedLaunch)
		})

		convey.Convey("新嵌套子块跨消息落库(含 sub_bash + childIDs)", func() {
			require.NotEmpty(t, gotChildJSON, "应落库子块 JSON")
			assert.Contains(t, gotChildJSON, "sub_bash")
			assert.Contains(t, gotChildJSON, "nested_tool_use")
			assert.Equal(t, []string{"sub_bash"}, gotChildIDs)
		})

		convey.Convey("收尾 emit StreamDone(发起卡 stream)", func() {
			assert.True(t, sawDone, "应在发起卡 stream 上 emit StreamDone")
			assert.Equal(t, launchStream, doneStream)
		})

		convey.Convey("会话级流补发 StreamAutonomousFinished 兜底(带发起消息 id)", func() {
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
				if p.Kind == chat_svc.StreamClosed && ev.Name == launchStream {
					closedIdx = i
				}
			}
			require.GreaterOrEqual(t, finIdx, 0, "活动轮收尾缺 StreamAutonomousFinished")
			assert.Equal(t, chat_svc.AutonomousStreamName(sid), finName, "兜底终态必须走会话级流")
			assert.Equal(t, launchID, finLaunch, "应携带发起消息 id")
			assert.Greater(t, finIdx, closedIdx, "兜底终态在 per-turn StreamClosed 之后补发")
		})

		convey.Convey("session 保持 idle(后台活动不翻 running)", func() {
			assert.Equal(t, "idle", sess.AgentStatus)
		})
	})
}

// TestDriveSubagentActivity_ProgressUpdatesSpawnCard 锁定「后台 subagent 跑着,派遣卡
// 上的工具数 / token 一直不动」:CLI 在会话空闲态每次工具调用都吐 task_progress,但这一轮
// 活动用的是全新空 accumulator,SubagentProgressHandler 的 Mutate 命中不到发起消息里既有
// 的 subagent_state overlay → 静默丢弃,既不推前端也不落库。
//
// Given 发起卡的进度快照停在 9 个工具 / 84739 token
// When  空闲活动轮里到达 SubagentProgress(21 个工具 / 132480 token / Edit)
// Then  ① 发起卡 per-turn 流上 emit subagent_progress,带新数值(实时)
//
//	② 新数值定向落回发起消息(重开会话不回退到旧数字)
func TestDriveSubagentActivity_ProgressUpdatesSpawnCard(t *testing.T) {
	convey.Convey("空闲活动轮里的 task_progress 更新发起卡进度", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		const sid = int64(100)
		const launchID = int64(2001)
		const toolCallID = "toolu_agent"
		be := &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"}

		sess := &chat_entity.Session{ID: sid, AgentID: 7, AgentStatus: "idle", ProviderSessionID: "sess-abc"}
		m.session.EXPECT().Find(gomock.Any(), sid).Return(sess, nil).AnyTimes()

		// 发起消息里已有一份进度快照(派遣那一轮攒下的)。
		launchMsg := &chat_entity.Message{
			ID: launchID, SessionID: sid, Role: "assistant", Seq: 4,
			BlocksJSON: `[` +
				`{"type":"tool_use","data":{"id":"` + toolCallID + `","name":"Agent","input":{"description":"T7"}}},` +
				`{"type":"subagent_state","data":{"parent_tool_call_id":"` + toolCallID + `","kind":"local_agent","description":"T7","status":"running","total_tokens":84739,"tool_uses":9,"last_tool_name":"Read","nested_tool_call_ids":[]}}` +
				`]`,
		}
		m.message.EXPECT().
			FindAssistantBySubagentToolCallID(gomock.Any(), sid, toolCallID).
			Return(launchMsg, nil).Times(1)
		m.message.EXPECT().
			AppendSubagentChildren(gomock.Any(), sid, toolCallID, gomock.Any(), gomock.Any()).
			Return(nil).AnyTimes()

		var gotProgress chat_repo.SubagentProgress
		m.message.EXPECT().
			PatchSubagentProgress(gomock.Any(), sid, toolCallID, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ int64, _ string, p chat_repo.SubagentProgress) error {
				gotProgress = p
				return nil
			}).Times(1)

		evs := make(chan agentruntime.Event, 3)
		evs <- agentruntime.ToolCall{ID: "sub_edit", Name: "Edit", ParentToolCallID: toolCallID}
		evs <- agentruntime.SubagentProgress{ToolCallID: toolCallID, Info: agentruntime.SubagentInfo{
			ToolUses: 21, TotalTokens: 132480, LastToolName: "Edit",
		}}
		evs <- agentruntime.ToolResult{ToolCallID: "sub_edit", Content: "ok", ParentToolCallID: toolCallID}
		close(evs)

		chat_svc.DriveSubagentActivityForTest(ctx, m.svc, sid, be, agentruntime.SubagentActivity{ToolCallID: toolCallID, Events: evs})

		convey.Convey("实时 emit subagent_progress 到发起卡 stream(带新数值)", func() {
			launchStream := chat_svc.StreamName(sid, launchID)
			var found *chat_svc.ChatStreamEvent
			for i := range m.events {
				p, ok := m.events[i].Payload.(chat_svc.ChatStreamEvent)
				if ok && p.Kind == chat_svc.StreamSubagentProgress && m.events[i].Name == launchStream {
					found = &p
				}
			}
			require.NotNil(t, found, "应在发起卡 stream 上 emit subagent_progress")
			assert.Equal(t, toolCallID, found.ToolCallID)
			require.NotNil(t, found.Subagent)
			assert.Equal(t, 21, found.Subagent.ToolUses)
			assert.Equal(t, 132480, found.Subagent.TotalTokens)
			assert.Equal(t, "Edit", found.Subagent.LastToolName)
		})

		convey.Convey("同一条 subagent_progress 镜像到会话级流(前端据此更新已落库的发起卡)", func() {
			// per-turn 流的 meta 只会被合并进那条流的 liveBlocks,而空闲活动轮的派遣卡
			// (Agent 工具的 tool_use 块)早已随发起消息落库、不在任何 liveBlocks 里 ——
			// 只发 per-turn 那一份的话前端必然合并落空。会话级流由 ChatPanel 常驻订阅。
			var found *chat_svc.ChatStreamEvent
			for i := range m.events {
				p, ok := m.events[i].Payload.(chat_svc.ChatStreamEvent)
				if ok && p.Kind == chat_svc.StreamSubagentProgress && m.events[i].Name == chat_svc.AutonomousStreamName(sid) {
					found = &p
				}
			}
			require.NotNil(t, found, "应把 subagent_progress 镜像一份到会话级流")
			assert.Equal(t, toolCallID, found.ToolCallID)
			require.NotNil(t, found.Subagent)
			assert.Equal(t, 21, found.Subagent.ToolUses)
			assert.Equal(t, 132480, found.Subagent.TotalTokens)
		})

		convey.Convey("收尾把最新进度定向落回发起消息", func() {
			assert.Equal(t, 21, gotProgress.ToolUses)
			assert.Equal(t, 132480, gotProgress.TotalTokens)
			assert.Equal(t, "Edit", gotProgress.LastToolName)
		})
	})
}

// TestDriveSubagentActivity_ModelUpdatesSpawnCard 锁定 R6/A9:后台 subagent 在会话
// **空闲活动轮**首次解出实际模型时,模型必须跨轮定向写回发起消息,且不改写该块已有的
// 进度字段(工具数 / tokens / last_tool_name) —— 前台 per-turn 路径的 first-wins +
// 不清空累计态语义(任务 3)在这条跨轮路径上同样成立。
func TestDriveSubagentActivity_ModelUpdatesSpawnCard(t *testing.T) {
	convey.Convey("空闲活动轮里首次解出模型时跨轮写回发起卡", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		const sid = int64(100)
		const launchID = int64(2001)
		const toolCallID = "toolu_agent"
		be := &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"}

		sess := &chat_entity.Session{ID: sid, AgentID: 7, AgentStatus: "idle", ProviderSessionID: "sess-abc"}
		m.session.EXPECT().Find(gomock.Any(), sid).Return(sess, nil).AnyTimes()

		// 发起消息里已有派遣那一刻攒下的进度快照,但还没有模型(等的就是这一轮)。
		launchMsg := &chat_entity.Message{
			ID: launchID, SessionID: sid, Role: "assistant", Seq: 4,
			BlocksJSON: `[` +
				`{"type":"tool_use","data":{"id":"` + toolCallID + `","name":"Agent","input":{"description":"T7"}}},` +
				`{"type":"subagent_state","data":{"parent_tool_call_id":"` + toolCallID + `","kind":"local_agent","description":"T7","status":"running","total_tokens":84739,"tool_uses":9,"last_tool_name":"Read","nested_tool_call_ids":[]}}` +
				`]`,
		}
		m.message.EXPECT().
			FindAssistantBySubagentToolCallID(gomock.Any(), sid, toolCallID).
			Return(launchMsg, nil).Times(1)
		m.message.EXPECT().
			AppendSubagentChildren(gomock.Any(), sid, toolCallID, gomock.Any(), gomock.Any()).
			Return(nil).AnyTimes()

		var gotProgress chat_repo.SubagentProgress
		m.message.EXPECT().
			PatchSubagentProgress(gomock.Any(), sid, toolCallID, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ int64, _ string, p chat_repo.SubagentProgress) error {
				gotProgress = p
				return nil
			}).Times(1)

		evs := make(chan agentruntime.Event, 1)
		evs <- agentruntime.SubagentModel{ToolCallID: toolCallID, Model: "claude-haiku-4-5-20251001"}
		close(evs)

		chat_svc.DriveSubagentActivityForTest(ctx, m.svc, sid, be, agentruntime.SubagentActivity{ToolCallID: toolCallID, Events: evs})

		convey.Convey("跨轮写回的进度快照带上新模型,且既有进度字段原样保留", func() {
			assert.Equal(t, "claude-haiku-4-5-20251001", gotProgress.Model)
			assert.Equal(t, 9, gotProgress.ToolUses, "模型更新不该改写已有工具数")
			assert.Equal(t, 84739, gotProgress.TotalTokens, "模型更新不该改写已有 token 数")
			assert.Equal(t, "Read", gotProgress.LastToolName, "模型更新不该改写已有 last_tool_name")
		})
	})
}

// TestDriveSubagentActivity_ModelMirroredToSessionStream 锁定 wrap-up 复审 Finding 1:
// subagentActivityEmitter.Emit 的会话级镜像 switch(subagent_activity.go:170-173)只转发
// subagent_started/progress/done,漏了 subagent_model。ChatStreamsHost 只把 per-turn 流的
// 事件合并进那条流的 liveBlocks,而空闲活动轮的派遣卡(发起消息的 tool_use 块)早已落库、
// 不在任何 liveBlocks 里 —— 这正是该镜像存在的原因(同一注释 + sess-2275)。模型事件不镜像
// 到会话级流,活动轮里模型徽标就永远不出现,直到 StreamDone 触发 reloadSession() 才补上。
func TestDriveSubagentActivity_ModelMirroredToSessionStream(t *testing.T) {
	convey.Convey("空闲活动轮里 subagent_model 也要镜像到会话级流,前端才能实时刷新已落库的发起卡", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		const sid = int64(100)
		const launchID = int64(2001)
		const toolCallID = "toolu_agent"
		be := &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"}

		sess := &chat_entity.Session{ID: sid, AgentID: 7, AgentStatus: "idle"}
		m.session.EXPECT().Find(gomock.Any(), sid).Return(sess, nil).AnyTimes()

		launchMsg := launchMessageWithSubagentState(launchID, sid, toolCallID)
		m.message.EXPECT().
			FindAssistantBySubagentToolCallID(gomock.Any(), sid, toolCallID).
			Return(launchMsg, nil).Times(1)
		m.message.EXPECT().
			AppendSubagentChildren(gomock.Any(), sid, toolCallID, gomock.Any(), gomock.Any()).
			Return(nil).AnyTimes()
		m.message.EXPECT().
			PatchSubagentProgress(gomock.Any(), sid, toolCallID, gomock.Any()).
			Return(nil).AnyTimes()

		evs := make(chan agentruntime.Event, 1)
		evs <- agentruntime.SubagentModel{ToolCallID: toolCallID, Model: "claude-haiku-4-5-20251001"}
		close(evs)

		chat_svc.DriveSubagentActivityForTest(ctx, m.svc, sid, be, agentruntime.SubagentActivity{ToolCallID: toolCallID, Events: evs})

		var found *chat_svc.ChatStreamEvent
		for i := range m.events {
			p, ok := m.events[i].Payload.(chat_svc.ChatStreamEvent)
			if ok && p.Kind == chat_svc.StreamSubagentModel && m.events[i].Name == chat_svc.AutonomousStreamName(sid) {
				found = &p
			}
		}
		require.NotNil(t, found, "应把 subagent_model 镜像到会话级流,否则活动轮里模型徽标不出现")
		assert.Equal(t, toolCallID, found.ToolCallID)
		assert.Equal(t, "claude-haiku-4-5-20251001", found.Model)
	})
}

// TestDriveSubagentActivity_ModelAlreadyRecorded_NoRedundantPatch 验证跨轮路径与
// per-turn 路径(任务 3)一致的 first-wins:模型已记录后,同一子代理后续活动轮里再遇到
// 别的内部模型帧,既不改写已记录的值,也不会仅为了「同一个模型」白跑一次读-改-写
// (同 TestDriveSubagentActivity_NoProgressSkipsPatch 的动机)。
func TestDriveSubagentActivity_ModelAlreadyRecorded_NoRedundantPatch(t *testing.T) {
	convey.Convey("模型已记录时,后续内部模型帧既不改写记录也不触发多余的 patch", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		const sid = int64(100)
		const launchID = int64(2001)
		const toolCallID = "toolu_agent"
		be := &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"}

		sess := &chat_entity.Session{ID: sid, AgentID: 7, AgentStatus: "idle"}
		m.session.EXPECT().Find(gomock.Any(), sid).Return(sess, nil).AnyTimes()

		launchMsg := &chat_entity.Message{
			ID: launchID, SessionID: sid, Role: "assistant", Seq: 4,
			BlocksJSON: `[` +
				`{"type":"tool_use","data":{"id":"` + toolCallID + `","name":"Agent","input":{"description":"T7"}}},` +
				`{"type":"subagent_state","data":{"parent_tool_call_id":"` + toolCallID + `","kind":"local_agent","description":"T7","status":"running","total_tokens":84739,"tool_uses":9,"model":"claude-opus-5"}}` +
				`]`,
		}
		m.message.EXPECT().
			FindAssistantBySubagentToolCallID(gomock.Any(), sid, toolCallID).
			Return(launchMsg, nil).Times(1)
		m.message.EXPECT().
			AppendSubagentChildren(gomock.Any(), sid, toolCallID, gomock.Any(), gomock.Any()).
			Return(nil).AnyTimes()
		// 关键:不 EXPECT PatchSubagentProgress —— 调用即 ctrl.Finish 失败。

		evs := make(chan agentruntime.Event, 1)
		evs <- agentruntime.SubagentModel{ToolCallID: toolCallID, Model: "claude-haiku-4-5-20251001"}
		close(evs)

		chat_svc.DriveSubagentActivityForTest(ctx, m.svc, sid, be, agentruntime.SubagentActivity{ToolCallID: toolCallID, Events: evs})
	})
}

// TestDriveSubagentActivity_NoProgressSkipsPatch 无进度事件的活动轮不该白写一次库
// (发起消息 blocks_json 动辄几百 KB,读-改-写不能每轮都来一遍)。
func TestDriveSubagentActivity_NoProgressSkipsPatch(t *testing.T) {
	convey.Convey("活动轮没有 task_progress 时不调 PatchSubagentProgress", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		const sid = int64(100)
		const launchID = int64(2001)
		const toolCallID = "toolu_agent"
		be := &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"}

		sess := &chat_entity.Session{ID: sid, AgentID: 7, AgentStatus: "idle"}
		m.session.EXPECT().Find(gomock.Any(), sid).Return(sess, nil).AnyTimes()
		m.message.EXPECT().
			FindAssistantBySubagentToolCallID(gomock.Any(), sid, toolCallID).
			Return(launchMessageWithSubagentState(launchID, sid, toolCallID), nil).Times(1)
		m.message.EXPECT().
			AppendSubagentChildren(gomock.Any(), sid, toolCallID, gomock.Any(), gomock.Any()).
			Return(nil).AnyTimes()
		// 关键:不 EXPECT PatchSubagentProgress —— 调用即 ctrl.Finish 失败。

		evs := make(chan agentruntime.Event, 2)
		evs <- agentruntime.ToolCall{ID: "sub_bash", Name: "Bash", ParentToolCallID: toolCallID}
		evs <- agentruntime.ToolResult{ToolCallID: "sub_bash", Content: "ok", ParentToolCallID: toolCallID}
		close(evs)

		chat_svc.DriveSubagentActivityForTest(ctx, m.svc, sid, be, agentruntime.SubagentActivity{ToolCallID: toolCallID, Events: evs})
	})
}

// TestDriveSubagentActivity_NoLaunchMessageDrains 验证发起消息找不到时:不落库、不 emit
// started,但仍把 act.Events 抽干(别让 Session reader 阻塞)。
func TestDriveSubagentActivity_NoLaunchMessageDrains(t *testing.T) {
	convey.Convey("发起消息找不到时抽干事件不落库", t, func() {
		m := setupChatTest(t)
		ctx := m.ctx

		const sid = int64(100)
		const toolCallID = "toolu_missing"
		be := &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"}

		m.message.EXPECT().
			FindAssistantBySubagentToolCallID(gomock.Any(), sid, toolCallID).
			Return(nil, nil).Times(1)
		// 关键:不调 AppendSubagentChildren(无 EXPECT → 调用即 ctrl.Finish 失败)。

		evs := make(chan agentruntime.Event, 2)
		evs <- agentruntime.ToolCall{ID: "sub_bash", Name: "Bash", ParentToolCallID: toolCallID}
		evs <- agentruntime.ToolResult{ToolCallID: "sub_bash", Content: "x", ParentToolCallID: toolCallID}
		close(evs)
		act := agentruntime.SubagentActivity{ToolCallID: toolCallID, Events: evs}

		chat_svc.DriveSubagentActivityForTest(ctx, m.svc, sid, be, act)

		convey.Convey("事件被抽干(channel 已空)", func() {
			_, open := <-evs
			assert.False(t, open, "act.Events 应被抽干并 close")
		})

		convey.Convey("不 emit StreamSubagentActivityStarted", func() {
			for _, ev := range m.events {
				if p, ok := ev.Payload.(chat_svc.ChatStreamEvent); ok {
					assert.NotEqual(t, chat_svc.StreamSubagentActivityStarted, p.Kind)
				}
			}
		})
	})
}

// TestStartSubagentActivityWatcher_DedupesAndExitsOnClose 验证 watcher 生命周期:每会话
// 只起一个(去重),底层 SubagentActivity channel close 后干净退出并清去重位。
func TestStartSubagentActivityWatcher_DedupesAndExitsOnClose(t *testing.T) {
	convey.Convey("subagent-activity watcher 每会话一个 + channel close 后退出", t, func() {
		m := setupChatTest(t)
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		src := mock_agentruntime.NewMockSubagentActivitySource(ctrl)
		be := &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"}

		ch := make(chan agentruntime.SubagentActivity) // 不带值,保持 open
		called := make(chan struct{})
		// Times(1) 即验证去重:第二次 start 不再订阅。
		src.EXPECT().SubagentActivity(int64(100)).
			DoAndReturn(func(int64) <-chan agentruntime.SubagentActivity {
				out := make(chan agentruntime.SubagentActivity)
				go func() {
					defer close(out)
					close(called)
					for a := range ch {
						out <- a
					}
				}()
				return out
			}).Times(1)

		chat_svc.StartSubagentActivityWatcherForTest(m.svc, 100, be, src)
		<-called // watcher goroutine 已订阅,去重位已占
		assert.True(t, chat_svc.IsSubagentActivityWatcherActiveForTest(m.svc, 100))

		// 第二次 start:被去重,不再调 SubagentActivity(Times(1) 验证)。
		chat_svc.StartSubagentActivityWatcherForTest(m.svc, 100, be, src)

		close(ch) // 让底层 channel close → watcher 退出
		require.Eventually(t, func() bool {
			return !chat_svc.IsSubagentActivityWatcherActiveForTest(m.svc, 100)
		}, time.Second, 5*time.Millisecond, "watcher 应在 channel close 后退出并清去重位")
	})
}

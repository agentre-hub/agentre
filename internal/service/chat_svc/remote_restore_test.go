package chat_svc

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap/zapcore"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/transcript"
	transcriptblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo/mock_transcript_repo"
)

// remote_restore_test.go 钉住远端执行时桌面端靠**持久帧**还原出来的那份转录。
//
// 桌面端的转录是回放 agentred 的持久帧重建的(turnRun.applyDurable,空 TurnContext、
// 没有实时那一路的 overlay)。subagent_state 在 agentred 落库后以 SubagentDone 一帧到达,
// 而 SubagentDoneHandler 只改已有的 overlay —— 回放里没有 Started,所以此前卡片整张没有。

// replayDurable 把一条落库的 assistant 消息投影成持久帧、过 protobuf 线格式,再按到达
// 顺序喂给 applyDurable,返回桌面端回放出的转录块。
func replayDurable(t *testing.T, blocksJSON string) []cagoblocks.ContentBlock {
	t.Helper()
	frames, _, err := transcript.ProjectMessages("conv", []*transcript_entity.Message{{
		SessionID: 100, Role: "assistant", Seq: 1, BlocksJSON: blocksJSON,
	}})
	require.NoError(t, err)

	svc := NewChat(&captureEmitter{}).(*chatSvc)
	sess := &chat_entity.Session{ID: 100}
	tr := &turnRun{
		svc: svc, sess: sess,
		be:      &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"},
		acc:     turn.New(),
		turnCtx: svc.newTurnContext(&chat_entity.Message{ID: 9001, SessionID: 100}, sess, "chat:100:9001", "claudecode"),
		stream:  "chat:100:9001",
	}
	for i, frame := range frames {
		data, err := protowire.MarshalEventNotification(frame.ConversationID, int64(i+1), frame.Event, false)
		require.NoError(t, err)
		n, _, err := protowire.UnmarshalEventNotification(data)
		require.NoError(t, err)
		if _, isDone := n.Event.(agentruntime.Done); isDone {
			continue
		}
		tr.applyDurable(context.Background(), n.Event)
	}
	return tr.acc.Snapshot()
}

// Given agentred 落库的一条 assistant 消息:一条 shell 调用(含嵌套步骤)和绑上去的
// agrctl subagent_state 卡,
// When 桌面端回放它的持久帧,
// Then 卡片带着全部字段和嵌套步骤还原出来。
func TestApplyDurable_GivenRemoteAgrctlCard_ThenDesktopRestoresCardWithNestedSteps(t *testing.T) {
	state, err := json.Marshal(transcriptblocks.SubagentStateBlock{
		ParentToolCallID: "T", Kind: agentruntime.SubagentKindAgrctl, Description: "写测试", Status: "completed",
		Summary: "exit 0", Origin: agentruntime.SubagentOriginAgrctl, ChildSessionID: 77, Isolated: true,
		FinalText: "全部通过", ToolUses: 2, Model: "m-1",
	})
	require.NoError(t, err)
	got := replayDurable(t, `[`+
		`{"type":"tool_use","data":{"id":"T","name":"Bash","input":{"command":"agrctl send --wait"}}},`+
		`{"type":"nested_tool_use","data":{"id":"n1","name":"Read","input":{"path":"a.go"},"parent_tool_call_id":"T"}},`+
		`{"type":"nested_tool_result","data":{"tool_call_id":"n1","content":"ok","parent_tool_call_id":"T"}},`+
		`{"type":"tool_result","data":{"tool_use_id":"T","content":[{"type":"text","data":{"text":"全部通过"}}]}},`+
		`{"type":"subagent_state","data":`+string(state)+`}]`)

	types := make([]string, 0, len(got))
	var card transcriptblocks.SubagentStateBlock
	for _, b := range got {
		types = append(types, b.Type())
		switch v := b.(type) {
		case *transcriptblocks.SubagentStateBlock:
			card = *v
		case transcriptblocks.SubagentStateBlock:
			card = v
		}
	}
	assert.Contains(t, types, "nested_tool_use", "嵌套调用要还原")
	assert.Contains(t, types, "nested_tool_result", "嵌套结果要还原")
	require.Contains(t, types, "subagent_state", "卡片要还原")
	assert.Equal(t, transcriptblocks.SubagentStateBlock{
		ParentToolCallID: "T", Kind: agentruntime.SubagentKindAgrctl, Description: "写测试", Status: "completed",
		Summary: "exit 0", Origin: agentruntime.SubagentOriginAgrctl, ChildSessionID: 77, Isolated: true,
		FinalText: "全部通过", ToolUses: 2, Model: "m-1",
	}, card)
}

func replayedSubagentCards(blocks []cagoblocks.ContentBlock) []transcriptblocks.SubagentStateBlock {
	var cards []transcriptblocks.SubagentStateBlock
	for _, b := range blocks {
		if v, ok := b.(*transcriptblocks.SubagentStateBlock); ok {
			cards = append(cards, *v)
		}
	}
	return cards
}

// Given 一张远端原生子代理卡(running,带 Summary 与 Runs,没有 agrctl 字段),
// When 桌面端回放,Then 同样还原成一张卡,状态与 Summary 不丢。
func TestApplyDurable_GivenRemoteNativeSubagentCard_ThenDesktopRestoresIt(t *testing.T) {
	cards := replayedSubagentCards(replayDurable(t, `[`+
		`{"type":"tool_use","data":{"id":"A","name":"Agent","input":{"description":"查"}}},`+
		`{"type":"subagent_state","data":{"parent_tool_call_id":"A","kind":"local_agent","description":"查","status":"running","summary":"进行中","runs":[{"id":"r1","agent":"explorer","status":"running"}]}}]`))

	require.Len(t, cards, 1)
	assert.Equal(t, "A", cards[0].ParentToolCallID)
	assert.Equal(t, "running", cards[0].Status)
	assert.Equal(t, "进行中", cards[0].Summary)
	assert.Equal(t, "explorer", cards[0].Runs[0].Agent)
}

// Given 一帧原生(非 agrctl)subagent_state 对应的发起调用不在这条消息里(后台任务在别的轮
// 完成),When 桌面端回放,Then 不凭空在本轮长出一张孤儿卡 —— 它仍交回 dispatcher 走原有
// 路径,不去仓储找原消息(agrctl 卡的跨消息写回见下文)。
func TestApplyDurable_GivenSubagentStateWithoutItsToolUse_ThenNoOrphanCard(t *testing.T) {
	cards := replayedSubagentCards(replayDurable(t, `[`+
		`{"type":"subagent_state","data":{"parent_tool_call_id":"EARLIER","kind":"local_bash","status":"completed"}}]`))

	assert.Empty(t, cards)
}

// ── tool_use 在更早消息里的 agrctl 卡(V16)───────────────────────────────────────────
//
// 父轮结束后 agentred 改写原消息里那张卡,把变化的块以新 seq 的持久帧发来。帧不带消息归属:
// 桌面端此刻没有进行中的轮时它们进补齐轮(autonomousTurnRun),有时进当前那一轮(turnRun)。
// 两条路都必须把它们写回卡所在的那条原消息,而不是在本轮长出块。

const (
	earlierSessionID = int64(100)
	earlierMessageID = int64(6300)
)

// earlierCardMessage 是库里那条更早的消息(卡所在处)的替身:定位、读-改-写卡片、追加子块
// 都在它上面跑,与仓储的会话锁语义一致。
type earlierCardMessage struct {
	mu      sync.Mutex
	blocks  []storedBlock
	finds   int
	appends []string // 追加的子块:"<type>:<id>"
	findErr error
}

func (m *earlierCardMessage) card(t *testing.T) transcriptblocks.SubagentStateBlock {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.blocks {
		if b.Type == "subagent_state" {
			var sb transcriptblocks.SubagentStateBlock
			require.NoError(t, json.Unmarshal(b.Data, &sb))
			return sb
		}
	}
	t.Fatal("原消息里没有卡片")
	return transcriptblocks.SubagentStateBlock{}
}

func (m *earlierCardMessage) appended() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.appends...)
}

// registerEarlierCardMessage 把一条含 agrctl 卡 T 的更早消息注册成仓储:T 是一条后台 shell
// 调用,卡片 running,已有嵌套步骤 n1(调用 + 结果)。
func registerEarlierCardMessage(t *testing.T, card transcriptblocks.SubagentStateBlock) *earlierCardMessage {
	t.Helper()
	cardJSON, err := json.Marshal(card)
	require.NoError(t, err)
	store := &earlierCardMessage{}
	require.NoError(t, json.Unmarshal([]byte(`[`+
		`{"type":"tool_use","data":{"id":"T","name":"Bash","input":{"command":"agrctl send --wait","run_in_background":true}}},`+
		`{"type":"tool_result","data":{"tool_use_id":"T","content":[{"type":"text","data":{"text":"running in background"}}]}},`+
		`{"type":"subagent_state","data":`+string(cardJSON)+`},`+
		`{"type":"nested_tool_use","data":{"id":"n1","name":"Read","input":{"path":"a.go"},"parent_tool_call_id":"T"}},`+
		`{"type":"nested_tool_result","data":{"tool_call_id":"n1","content":"ok","parent_tool_call_id":"T"}}]`), &store.blocks))

	ctrl := gomock.NewController(t)
	repo := mock_transcript_repo.NewMockMessageRepo(ctrl)
	repo.EXPECT().FindAssistantBySubagentToolCallID(gomock.Any(), earlierSessionID, gomock.Any()).DoAndReturn(
		func(_ context.Context, _ int64, toolCallID string) (*transcript_entity.Message, error) {
			store.mu.Lock()
			defer store.mu.Unlock()
			store.finds++
			if store.findErr != nil {
				return nil, store.findErr
			}
			if toolCallID != "T" {
				return nil, nil
			}
			body, err := json.Marshal(store.blocks)
			if err != nil {
				return nil, err
			}
			return &transcript_entity.Message{ID: earlierMessageID, SessionID: earlierSessionID, Role: "assistant", BlocksJSON: string(body)}, nil
		}).AnyTimes()
	repo.EXPECT().PatchSubagentState(gomock.Any(), earlierSessionID, gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ int64, toolCallID string, mutate func(*transcriptblocks.SubagentStateBlock) bool) (bool, error) {
			store.mu.Lock()
			defer store.mu.Unlock()
			for i, b := range store.blocks {
				var sb transcriptblocks.SubagentStateBlock
				if b.Type != "subagent_state" || json.Unmarshal(b.Data, &sb) != nil || sb.ParentToolCallID != toolCallID {
					continue
				}
				if mutate(&sb) {
					data, err := json.Marshal(sb)
					if err != nil {
						return true, err
					}
					store.blocks[i].Data = data
				}
				return true, nil
			}
			return false, nil
		}).AnyTimes()
	repo.EXPECT().AppendSubagentChildren(gomock.Any(), earlierSessionID, "T", gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ int64, _ string, childJSON string, ids []string) error {
			var children []storedBlock
			if err := json.Unmarshal([]byte(childJSON), &children); err != nil {
				return err
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			for _, c := range children {
				var id struct {
					ID         string `json:"id"`
					ToolCallID string `json:"tool_call_id"`
				}
				_ = json.Unmarshal(c.Data, &id)
				store.appends = append(store.appends, c.Type+":"+id.ID+id.ToolCallID)
			}
			store.blocks = append(store.blocks, children...)
			for i, b := range store.blocks {
				var sb transcriptblocks.SubagentStateBlock
				if b.Type != "subagent_state" || json.Unmarshal(b.Data, &sb) != nil {
					continue
				}
				for _, id := range ids {
					if !slices.Contains(sb.NestedToolCallIDs, id) {
						sb.NestedToolCallIDs = append(sb.NestedToolCallIDs, id)
					}
				}
				data, err := json.Marshal(sb)
				if err != nil {
					return err
				}
				store.blocks[i].Data = data
			}
			return nil
		}).AnyTimes()
	prev := transcript_repo.Message()
	transcript_repo.RegisterMessage(repo)
	t.Cleanup(func() { transcript_repo.RegisterMessage(prev) })
	return store
}

// streamRecorder 记下每个实时事件连同它的流名。
type streamRecorder struct {
	mu     sync.Mutex
	events []recordedStreamEvent
}

type recordedStreamEvent struct {
	stream string
	ev     ChatStreamEvent
}

func (r *streamRecorder) Emit(_ context.Context, stream string, payload any) {
	if ev, ok := payload.(ChatStreamEvent); ok {
		r.mu.Lock()
		r.events = append(r.events, recordedStreamEvent{stream: stream, ev: ev})
		r.mu.Unlock()
	}
}

func (r *streamRecorder) of(kind ChatStreamEventKind) []recordedStreamEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []recordedStreamEvent
	for _, e := range r.events {
		if e.ev.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// durableRun 是消费持久帧的一轮:当前那一轮(turnRun)或补齐 / 自主续轮(autonomousTurnRun)。
type durableRun struct {
	name  string
	apply func(agentruntime.Event)
	acc   *turn.Accumulator
}

// durableRuns 交回两条 applyDurable 路径各一只,共用 svc。assistant 行留空:本轮的 checkpoint
// 不落库,本轮长没长出块看累加器。
func durableRuns(svc *chatSvc) []durableRun {
	sess := &chat_entity.Session{ID: earlierSessionID, ExecDeviceID: 7}
	be := &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"}
	tr := &turnRun{svc: svc, sess: sess, be: be, acc: turn.New(), stream: "chat:100:6600"}
	at := &autonomousTurnRun{svc: svc, sessionID: earlierSessionID, sess: sess, be: be, acc: turn.New(), stream: "chat:100:6600"}
	ctx := context.Background()
	return []durableRun{
		{name: "turnRun", acc: tr.acc, apply: func(ev agentruntime.Event) { tr.locked(func() { tr.applyDurable(ctx, ev) }) }},
		{name: "autonomousTurnRun", acc: at.acc, apply: func(ev agentruntime.Event) { at.applyDurable(ctx, ev) }},
	}
}

func runningBackgroundAgrctlCard() transcriptblocks.SubagentStateBlock {
	return transcriptblocks.SubagentStateBlock{
		ParentToolCallID: "T", Kind: agentruntime.SubagentKindLocalBash, TaskID: "bg-1", Description: "agrctl send",
		Status: "running", Origin: agentruntime.SubagentOriginAgrctl, ChildSessionID: 77, ToolUses: 1,
		NestedToolCallIDs: []string{"n1"}, Mode: "single",
		Runs: []agentruntime.SubagentRun{{ID: "agrctl", Agent: "reviewer", Task: "审一下", Status: "running"}},
	}
}

// Given 一张 agrctl 后台卡在更早那条消息里(running,已有嵌套步骤 n1),
// When agentred 改写它后发来的持久帧到达(CLI 判的 failed 快照、模型、重复的 n1、新的 n2),
// Then 原消息里那张卡被改成 failed(单运行快照跟着翻)、n2 只追加一次、实时卡片事件发出,
// 本轮一块都不长。
func TestApplyDurable_GivenAgrctlCardInAnEarlierMessage_WhenRewriteFramesArrive_ThenTheOriginalCardIsPatched(t *testing.T) {
	for _, idx := range []int{0, 1} {
		rec := &streamRecorder{}
		svc := NewChat(rec).(*chatSvc)
		run := durableRuns(svc)[idx]
		t.Run(run.name, func(t *testing.T) {
			store := registerEarlierCardMessage(t, runningBackgroundAgrctlCard())
			snapshot := runningBackgroundAgrctlCard()
			snapshot.Status, snapshot.Summary, snapshot.FinalText, snapshot.ToolUses = "failed", "exit 1", "构建失败", 2
			snapshot.Runs = []agentruntime.SubagentRun{{ID: "agrctl", Agent: "reviewer", Task: "审一下", Status: "failed", ErrorMessage: "构建失败", ToolUses: 2}}

			for _, ev := range []agentruntime.Event{
				agentruntime.SubagentDone{ToolCallID: "T", Info: agentruntime.SubagentInfo{
					TaskID: snapshot.TaskID, Kind: snapshot.Kind, TaskDescription: snapshot.Description, Status: snapshot.Status,
					Summary: snapshot.Summary, Mode: snapshot.Mode, Runs: snapshot.Runs, ToolUses: snapshot.ToolUses,
					Origin: snapshot.Origin, ChildSessionID: snapshot.ChildSessionID, FinalText: snapshot.FinalText,
				}},
				agentruntime.SubagentModel{ToolCallID: "T", Model: "m-1"},
				agentruntime.ToolCall{ID: "n1", Name: "Read", Input: json.RawMessage(`{"path":"a.go"}`), ParentToolCallID: "T"},
				agentruntime.ToolResult{ToolCallID: "n1", Content: "ok", ParentToolCallID: "T"},
				agentruntime.ToolCall{ID: "n2", Name: "Bash", Input: json.RawMessage(`{"command":"make"}`), ParentToolCallID: "T"},
				agentruntime.ToolResult{ToolCallID: "n2", Content: "exit 1", IsError: true, ParentToolCallID: "T"},
				agentruntime.ToolResult{ToolCallID: "n2", Content: "exit 1", IsError: true, ParentToolCallID: "T"},
			} {
				run.apply(ev)
			}

			card := store.card(t)
			assert.Equal(t, "failed", card.Status, "CLI 判的终态落到原卡上")
			assert.Equal(t, "构建失败", card.FinalText)
			assert.Equal(t, "exit 1", card.Summary)
			assert.Equal(t, 2, card.ToolUses)
			assert.Equal(t, "m-1", card.Model)
			require.Len(t, card.Runs, 1)
			assert.Equal(t, "failed", card.Runs[0].Status, "单运行快照跟着翻")
			assert.Equal(t, "reviewer", card.Runs[0].Agent)
			assert.Equal(t, []string{"n1", "n2"}, card.NestedToolCallIDs, "既有的嵌套索引不被快照抹掉,n2 只记一次")
			assert.Equal(t, []string{"nested_tool_use:n2", "nested_tool_result:n2"}, store.appended(),
				"只追加新的嵌套步骤,重复投递的不再落块")
			assert.Empty(t, run.acc.Snapshot(), "本轮(追帧那一行)不长任何块")

			started := rec.of(StreamSubagentActivityStarted)
			require.NotEmpty(t, started, "告诉打开着的界面:原消息里的卡又有活动")
			assert.Equal(t, earlierMessageID, started[0].ev.LaunchMessageID)
			assert.Equal(t, "T", started[0].ev.ToolCallID)
			var failed bool
			for _, e := range rec.of(StreamSubagentDone) {
				if e.stream == AutonomousStreamName(earlierSessionID) && e.ev.ToolCallID == "T" &&
					e.ev.Subagent != nil && e.ev.Subagent.Status == "failed" {
					failed = true
				}
			}
			assert.True(t, failed, "实时卡片事件带着 failed 发到会话级流")
		})
	}
}

// Given 补齐轮把卡所在的整条消息重放上来(桌面端重启前没见过这些 seq),
// When 自主续轮那一路回放, Then 卡片在本轮原位还原,与当前那一轮的回放一致。
func TestApplyDurable_GivenCatchUpReplaysTheWholeCardMessage_ThenTheAutonomousRunRestoresTheCardInPlace(t *testing.T) {
	state, err := json.Marshal(transcriptblocks.SubagentStateBlock{
		ParentToolCallID: "T", Kind: agentruntime.SubagentKindLocalBash, Description: "agrctl send", Status: "failed",
		Origin: agentruntime.SubagentOriginAgrctl, ChildSessionID: 77, FinalText: "构建失败",
	})
	require.NoError(t, err)
	frames, _, err := transcript.ProjectMessages("conv", []*transcript_entity.Message{{
		SessionID: earlierSessionID, Role: "assistant", Seq: 1, BlocksJSON: `[` +
			`{"type":"tool_use","data":{"id":"T","name":"Bash","input":{"command":"agrctl send --wait"}}},` +
			`{"type":"subagent_state","data":` + string(state) + `}]`,
	}})
	require.NoError(t, err)

	run := durableRuns(NewChat(&captureEmitter{}).(*chatSvc))[1]
	for _, frame := range frames {
		if _, isDone := frame.Event.(agentruntime.Done); !isDone {
			run.apply(frame.Event)
		}
	}

	cards := replayedSubagentCards(run.acc.Snapshot())
	require.Len(t, cards, 1)
	assert.Equal(t, "failed", cards[0].Status)
	assert.Equal(t, "构建失败", cards[0].FinalText)
	assert.Equal(t, agentruntime.SubagentOriginAgrctl, cards[0].Origin)
}

// Given 定位原消息失败(仓储出错),When 这张 agrctl 卡的嵌套步骤到达,
// Then 它不落进本轮,并记一条 warn —— 缺一步好过在别的消息里长出一块无主步骤。
func TestApplyDurable_GivenEarlierCardLookupFails_ThenTheFrameStaysOutOfThisTurn(t *testing.T) {
	logs := observeChatLogs(t)
	store := registerEarlierCardMessage(t, runningBackgroundAgrctlCard())
	store.findErr = errors.New("database is locked")
	run := durableRuns(NewChat(&captureEmitter{}).(*chatSvc))[1]

	run.apply(agentruntime.ToolCall{ID: "n2", Name: "Bash", ParentToolCallID: "T"})

	assert.Empty(t, run.acc.Snapshot())
	assert.Empty(t, store.appended())
	assert.NotZero(t, logs.FilterLevelExact(zapcore.WarnLevel).Len())
}

// Given 卡所在的那条消息恰是进行中这一轮的前一个分段(插话把这一轮切成了两行,这一轮的流仍
// 以第一行命名),When 改写帧到达, Then 原卡照样改写,但不对这一轮自己的流发活动起止 ——
// 那会让前端把还在跑的这一轮当成结束。
func TestApplyDurable_GivenCardInAnEarlierSegmentOfThisTurn_ThenTheLiveStreamIsNotClosed(t *testing.T) {
	store := registerEarlierCardMessage(t, runningBackgroundAgrctlCard())
	rec := &streamRecorder{}
	svc := NewChat(rec).(*chatSvc)
	live := StreamName(earlierSessionID, earlierMessageID)
	tr := &turnRun{
		svc: svc, sess: &chat_entity.Session{ID: earlierSessionID, ExecDeviceID: 7},
		be: &agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode"}, acc: turn.New(), stream: live,
	}

	tr.locked(func() {
		tr.applyDurable(context.Background(), agentruntime.ToolCall{ID: "n2", Name: "Bash", ParentToolCallID: "T"})
	})

	assert.Equal(t, []string{"nested_tool_use:n2"}, store.appended())
	assert.Empty(t, tr.acc.Snapshot())
	assert.Empty(t, rec.of(StreamSubagentActivityStarted))
	assert.Empty(t, rec.of(StreamDone), "这一轮还在跑")
	assert.Empty(t, rec.of(StreamAutonomousFinished), "这一轮还在跑")
}

// Given 更早那条消息里是一张原生后台子代理卡(不是 agrctl 卡),
// When 它的嵌套步骤一帧帧到达, Then 它们照旧走原有路径(原卡不被改写),且这一轮只为它查一次库。
func TestApplyDurable_GivenNativeCardInAnEarlierMessage_ThenItsFramesKeepTheExistingPathAndAreLookedUpOnce(t *testing.T) {
	native := runningBackgroundAgrctlCard()
	native.Origin, native.Kind, native.Mode, native.Runs = "", agentruntime.SubagentKindLocalAgent, "", nil
	store := registerEarlierCardMessage(t, native)
	run := durableRuns(NewChat(&captureEmitter{}).(*chatSvc))[1]

	run.apply(agentruntime.ToolCall{ID: "n2", Name: "Bash", ParentToolCallID: "T"})
	run.apply(agentruntime.ToolCall{ID: "n3", Name: "Read", ParentToolCallID: "T"})

	assert.Empty(t, store.appended())
	assert.Equal(t, "running", store.card(t).Status)
	assert.Len(t, run.acc.Snapshot(), 2, "原生卡的帧行为不变")
	assert.Equal(t, 1, store.finds)
}

// Given 卡所在的那条消息正是一轮还没收尾的当前那一行(那一轮还在 activeTurns 里,收尾那一发
// 整条落库还没做),When agentred 的改写帧经补齐轮到达,
// Then 写回等那一轮整条落库之后才改卡 —— 否则收尾那一发会把改好的卡整条覆盖回去(R4)。
// 当前那一轮的行若不是卡所在的消息,写回不等。
func TestApplyDurable_GivenTheCardMessagesTurnIsStillFinalizing_ThenWriteBackWaitsForItsFinalSave(t *testing.T) {
	failed := agentruntime.SubagentDone{ToolCallID: "T", Info: agentruntime.SubagentInfo{
		TaskID: "bg-1", Kind: agentruntime.SubagentKindLocalBash, TaskDescription: "agrctl send", Status: "failed",
		Origin: agentruntime.SubagentOriginAgrctl, ChildSessionID: 77, Mode: "single",
		Runs: []agentruntime.SubagentRun{{ID: "agrctl", Agent: "reviewer", Task: "审一下", Status: "failed"}},
	}}
	finalizing := func(svc *chatSvc, rowID int64) *turnRun {
		parent := &turnRun{
			svc: svc, sess: &chat_entity.Session{ID: earlierSessionID, ExecDeviceID: 7},
			assistantMsg: &chat_entity.Message{ID: rowID, SessionID: earlierSessionID},
		}
		svc.activeTurns.Store(earlierSessionID, parent)
		return parent
	}
	applyAsync := func(run durableRun) <-chan struct{} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			run.apply(failed)
		}()
		return done
	}

	t.Run("卡在那一轮当前那一行里:等它整条落库", func(t *testing.T) {
		store := registerEarlierCardMessage(t, runningBackgroundAgrctlCard())
		svc := NewChat(&captureEmitter{}).(*chatSvc)
		parent := finalizing(svc, earlierMessageID)

		done := applyAsync(durableRuns(svc)[1])
		select {
		case <-done:
		case <-time.After(300 * time.Millisecond):
		}
		assert.Equal(t, "running", store.card(t).Status, "那一轮整条落库之前不改卡")

		parent.markPersisted()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("那一轮落库之后写回仍没完成")
		}
		assert.Equal(t, "failed", store.card(t).Status)
	})

	t.Run("那一轮当前那一行是别的消息:不等", func(t *testing.T) {
		store := registerEarlierCardMessage(t, runningBackgroundAgrctlCard())
		svc := NewChat(&captureEmitter{}).(*chatSvc)
		finalizing(svc, earlierMessageID+1)

		select {
		case <-applyAsync(durableRuns(svc)[1]):
		case <-time.After(5 * time.Second):
			t.Fatal("写回在等一轮与这张卡无关的收尾")
		}
		assert.Equal(t, "failed", store.card(t).Status)
	})
}

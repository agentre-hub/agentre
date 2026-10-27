package chat_svc

import (
	"context"
	"encoding/json"
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/transcript"
	transcriptblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
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

	var types []string
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

// Given 一帧 subagent_state 对应的发起调用不在这条消息里(后台任务在别的轮完成),
// When 桌面端回放,Then 不凭空在本轮长出一张孤儿卡 —— 仍走原有的跨消息翻转路径。
func TestApplyDurable_GivenSubagentStateWithoutItsToolUse_ThenNoOrphanCard(t *testing.T) {
	cards := replayedSubagentCards(replayDurable(t, `[`+
		`{"type":"subagent_state","data":{"parent_tool_call_id":"EARLIER","kind":"local_bash","status":"completed"}}]`))

	assert.Empty(t, cards)
}

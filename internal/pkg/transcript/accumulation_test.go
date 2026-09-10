package transcript_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcript"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
)

// discardEmitter 是宿主发射器的替身。发射本身归各宿主自己（桌面端是 Wails 事件、
// agentred 是 RPC 通知），累积出来的块与它无关 —— 这条用例正是要证明这一点。
type discardEmitter struct{ calls int }

func (e *discardEmitter) Emit(context.Context, string, any) { e.calls++ }

// Given 一条后端事件序列（thinking 穿插两轮、一次原地修补、一条孤儿 tool_result），
// When 用共享包的 dispatcher + accumulator 累积，Then 落库正文逐字节等于这一份 ——
// 两个宿主跑的是同一行代码，因此这也是 agentred 落下的那一份。
//
// 断言的是 BlocksJSON 的**字节**而不是块的结构：块正文是真正写进 chat_message_blocks
// 的那串字符，宿主之间的差异（字段顺序、omitempty、嵌套块的编码）只有在字节这一层
// 才看得见。
func TestAccumulate_GivenOneEventSequence_ThenBlocksJSONIsByteIdentical(t *testing.T) {
	t.Parallel()

	dispatcher := transcript.NewTurnDispatcher(transcript.Adapters{})
	acc := turn.New()
	emit := &discardEmitter{}
	turnCtx := &turn.TurnContext{Stream: "chat:41", Waits: turn.NewWaitTracker()}
	toolInput, err := json.Marshal(map[string]any{"path": "README.md"})
	require.NoError(t, err)

	events := []agentruntime.Event{
		agentruntime.ThinkingDelta{Text: "check the file"},
		agentruntime.TextDelta{Text: "reading"},
		agentruntime.ToolCall{ID: "tu-1", Name: "Read", Input: toolInput},
		// 孤儿：没有配对的 tool_use，必须被丢弃而不是落成一块无主的结果。
		agentruntime.ToolResult{ToolCallID: "ghost", Content: "no such call"},
		agentruntime.ToolResult{ToolCallID: "tu-1", Content: "ok"},
		agentruntime.UserAskRequest{RequestID: "ask-1", ToolCallID: "tu-2", Questions: []agentruntime.AskQuestion{
			{Question: "continue?", Options: []agentruntime.AskOption{{Label: "yes"}}},
		}},
		// 原地修补：答案落在**已存在**的那一块上，而不是追加一块。
		agentruntime.UserAskResolved{RequestID: "ask-1", Answers: []agentruntime.AskAnswer{
			{QuestionIndex: 0, Labels: []string{"yes"}},
		}},
		// 第二轮 thinking 按真实位置穿插在工具之后，不回到 index 0。
		agentruntime.ThinkingDelta{Text: "second round"},
		agentruntime.TextDelta{Text: "done"},
	}
	for _, event := range events {
		require.NoError(t, dispatcher.Apply(context.Background(), event, acc, emit, nil, turnCtx))
	}

	message := &transcript_entity.Message{}
	require.NoError(t, message.SetBlocks(acc.Finalize()))

	const want = `[` +
		`{"type":"thinking","data":{"text":"check the file"}},` +
		`{"type":"text","data":{"text":"reading"}},` +
		`{"type":"tool_use","data":{"id":"tu-1","name":"Read","input":{"path":"README.md"}}},` +
		`{"type":"tool_result","data":{"tool_use_id":"tu-1","content":[{"type":"text","data":{"text":"ok"}}]}},` +
		`{"type":"user_ask","data":{"request_id":"ask-1","tool_call_id":"tu-2","questions":[{"question":"continue?","options":[{"label":"yes"}]}],"answered":true,"answers":[{"questionIndex":0,"labels":["yes"]}]}},` +
		`{"type":"thinking","data":{"text":"second round"}},` +
		`{"type":"text","data":{"text":"done"}}` +
		`]`
	assert.Equal(t, want, message.BlocksJSON)
}

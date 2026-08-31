package chat_svc_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// steerBeforeToolResultRunner 复刻真实事故序列(sess-2833):claudecode 的
// PostToolUse hook 在工具跑完、CLI 写出 tool_result 帧**之前**就把排队消息
// drain 走,于是 SteerConsumed 先于同一个工具的 ToolResult 到达 chat_svc。
type steerBeforeToolResultRunner struct{}

func (steerBeforeToolResultRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{}
}

func (steerBeforeToolResultRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	events := make(chan agentruntime.Event, 4)
	events <- agentruntime.ToolCall{
		ID: "tu-1", Name: "Bash", Input: json.RawMessage(`{"command":"grep -rn x"}`),
	}
	events <- agentruntime.SteerConsumed{
		Steers: []agentruntime.ConsumedSteer{{QueuedID: "qid-1", Text: "follow-up"}},
	}
	events <- agentruntime.ToolResult{ToolCallID: "tu-1", Content: "no matches found"}
	events <- agentruntime.TextDelta{Text: "after"}
	close(events)
	return events, &agentruntime.RunResult{}, nil
}

// steerWhileToolNeverResolvesRunner 挂着一个永远等不到 tool_result 的 tool_use
// (AskUserQuestion / subagent 这类结果不走流的工具),用来钉死"推迟分段"必须有界。
type steerWhileToolNeverResolvesRunner struct{}

func (steerWhileToolNeverResolvesRunner) Capabilities() capability.Capabilities {
	return capability.Capabilities{}
}

func (steerWhileToolNeverResolvesRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	events := make(chan agentruntime.Event, 3)
	events <- agentruntime.ToolCall{
		ID: "tu-ask", Name: "AskUserQuestion", Input: json.RawMessage(`{}`),
	}
	events <- agentruntime.SteerConsumed{
		Steers: []agentruntime.ConsumedSteer{{QueuedID: "qid-1", Text: "follow-up"}},
	}
	events <- agentruntime.TextDelta{Text: "after"}
	close(events)
	return events, &agentruntime.RunResult{}, nil
}

// expectSteerSegmentationTurn 搭一轮 builtin turn 的仓储桩:Send 落
// user+assistant,persistConsumedSteers 再落 user+assistant,共 4 条。
func expectSteerSegmentationTurn(t *testing.T, m *chatMocks) {
	t.Helper()

	m.session.EXPECT().Find(gomock.Any(), int64(100)).Return(&chat_entity.Session{
		ID: 100, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(&agent_entity.Agent{
		ID: 7, Name: "Eng", AgentBackendID: 12, Status: consts.ACTIVE, PromptJSON: `[]`,
	}, nil)
	m.backend.EXPECT().Find(gomock.Any(), int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "key-21", Status: consts.ACTIVE,
	}, nil)
	m.provider.EXPECT().FindByKey(gomock.Any(), "key-21").Return(&llm_provider_entity.LLMProvider{
		ID: 21, ProviderKey: "key-21", Type: string(llm_provider_entity.TypeAnthropic),
		Status: consts.ACTIVE, Enabled: llm_provider_entity.EnabledOn,
		DefaultModelKey: "mk-key-21",
	}, nil).AnyTimes()
	expectProviderResolvable(m, "key-21")
	m.session.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()

	createIDs := []int64{1000, 1001, 1002, 1003}
	createIdx := 0
	m.message.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, msg *chat_entity.Message) error {
			msg.ID = createIDs[createIdx]
			createIdx++
			return nil
		}).Times(len(createIDs))

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(1, nil)
	m.dbMock.ExpectCommit()

	m.message.EXPECT().List(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	m.message.EXPECT().Update(gomock.Any(), gomock.Any()).AnyTimes()
	// 轮内 checkpoint 已从 Update 改走 CheckpointBlocks(整表替换 → 差分写,见
	// chat_repo.syncBlocks);这条用例跑到 tool_result,两条路都要许可。
	m.message.EXPECT().CheckpointBlocks(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil).AnyTimes()

	m.dbMock.ExpectBegin()
	m.message.EXPECT().NextSeq(gomock.Any(), int64(100)).Return(3, nil)
	m.dbMock.ExpectCommit()
}

// runSteerSegmentationTurn 跑一轮并挑出 StreamSteerConsumed / StreamDone 两个事件。
func runSteerSegmentationTurn(t *testing.T, m *chatMocks) (consumed, done *chat_svc.ChatStreamEvent) {
	t.Helper()

	resp, err := m.svc.Send(m.ctx, &chat_svc.SendRequest{SessionID: 100, AgentID: 7, Text: "hi"})
	assert.NoError(t, err)
	chat_svc.WaitForStreamForTest(m.svc, resp.AssistantMessageID)

	for _, ev := range m.events {
		payload, ok := ev.Payload.(chat_svc.ChatStreamEvent)
		if !ok {
			continue
		}
		switch payload.Kind {
		case chat_svc.StreamSteerConsumed:
			cp := payload
			consumed = &cp
		case chat_svc.StreamDone:
			cp := payload
			done = &cp
		}
	}
	return consumed, done
}

func blockTypes(blocks []chat_svc.ChatBlock) []string {
	out := make([]string, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, b.Type)
	}
	return out
}

func TestSend_SteerConsumedKeepsInFlightToolResultWithItsToolUse(t *testing.T) {
	// Given 一个 tool_use 已经发出、它的 tool_result 还在路上
	// When runtime 先报告 steer 已被消费（PostToolUse hook 早于 tool_result 帧）
	// Then 分段推迟到 tool_result 落进当前 assistant 之后，收口的那条消息里
	//      tool_use 与 tool_result 成对，结果不会被当孤儿丢掉。
	m := setupChatTest(t)
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, steerBeforeToolResultRunner{})
	t.Cleanup(restore)
	expectSteerSegmentationTurn(t, m)

	consumed, done := runSteerSegmentationTurn(t, m)

	if assert.NotNil(t, consumed) && assert.NotNil(t, consumed.PreviousAssistantMessage) {
		prev := consumed.PreviousAssistantMessage.Blocks
		assert.Equal(t, []string{"tool_use", "tool_result"}, blockTypes(prev),
			"收口的 assistant 必须同时留下 tool_use 和它的 tool_result")
		if assert.Len(t, prev, 2) {
			assert.Equal(t, "tu-1", prev[0].ToolCallID)
			assert.Equal(t, "tu-1", prev[1].ToolCallID)
			assert.Equal(t, "no matches found", prev[1].Text)
		}
	}
	if assert.NotNil(t, done) && assert.NotNil(t, done.Message) {
		assert.Equal(t, []string{"text"}, blockTypes(done.Message.Blocks),
			"新 assistant 只承接分段之后的内容")
		assert.Equal(t, "after", done.Message.Blocks[0].Text)
	}
}

func TestSend_SteerConsumedSplitsAnywayWhenPendingToolNeverResolves(t *testing.T) {
	// Given 挂着一个结果永远不入流的 tool_use（AskUserQuestion）
	// When runtime 报告 steer 已被消费，随后来的是普通文本而不是 tool_result
	// Then 分段最多推迟一个事件就必须落地，不能一路拖到 turn 收尾。
	m := setupChatTest(t)
	restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, steerWhileToolNeverResolvesRunner{})
	t.Cleanup(restore)
	expectSteerSegmentationTurn(t, m)

	consumed, done := runSteerSegmentationTurn(t, m)

	if assert.NotNil(t, consumed) && assert.NotNil(t, consumed.PreviousAssistantMessage) {
		assert.Equal(t, []string{"tool_use"}, blockTypes(consumed.PreviousAssistantMessage.Blocks))
	}
	if assert.NotNil(t, done) && assert.NotNil(t, done.Message) {
		assert.Equal(t, []string{"text"}, blockTypes(done.Message.Blocks),
			"分段之后的文本必须落在新 assistant 上，说明推迟是有界的")
		assert.Equal(t, "after", done.Message.Blocks[0].Text)
	}
}

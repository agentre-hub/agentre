package hermes

import (
	"encoding/json"
	"testing"

	"github.com/cago-frame/agents/provider"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
)

func rawJSON(t *testing.T, s string) json.RawMessage {
	t.Helper()
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}

// TestDecodeEventFrame 锁住 gateway 事件信封的解码:type/session_id/payload。
func TestDecodeEventFrame(t *testing.T) {
	Convey("Given a gateway event params object", t, func() {
		Convey("When type and payload are present Then it decodes", func() {
			ev, ok := decodeEventFrame([]byte(`{"type":"message.delta","session_id":"live-1","payload":{"text":"hi"}}`))
			So(ok, ShouldBeTrue)
			So(ev.Kind, ShouldEqual, EventMessageDelta)
			So(ev.Session, ShouldEqual, "live-1")
			So(string(ev.Payload), ShouldEqual, `{"text":"hi"}`)
		})
		Convey("When type is missing Then it is not an event", func() {
			_, ok := decodeEventFrame([]byte(`{"session_id":"live-1"}`))
			So(ok, ShouldBeFalse)
		})
		Convey("When params is malformed Then it is not an event", func() {
			_, ok := decodeEventFrame([]byte(`{not json`))
			So(ok, ShouldBeFalse)
		})
		Convey("When params is empty Then it is not an event", func() {
			_, ok := decodeEventFrame(nil)
			So(ok, ShouldBeFalse)
		})
	})
}

func TestTranslate_FrameKinds(t *testing.T) {
	type tc struct {
		name    string
		kind    EventKind
		payload string
		check   func(t *testing.T, events []agentruntime.Event, usage *provider.Usage, err error)
	}

	cases := []tc{
		{
			name:    "message.delta emits TextDelta",
			kind:    EventMessageDelta,
			payload: `{"text":"hello"}`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.NoError(t, err)
				require.Len(t, events, 1)
				assert.Equal(t, agentruntime.TextDelta{Text: "hello"}, events[0])
			},
		},
		{
			name:    "message.delta with empty text emits nothing (boundary)",
			kind:    EventMessageDelta,
			payload: `{"text":""}`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.NoError(t, err)
				assert.Empty(t, events)
			},
		},
		{
			name:    "message.delta with missing payload emits nothing (boundary)",
			kind:    EventMessageDelta,
			payload: "",
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.NoError(t, err)
				assert.Empty(t, events)
			},
		},
		{
			name:    "message.interim streams as assistant text",
			kind:    EventMessageInterim,
			payload: `{"text":"aside"}`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.NoError(t, err)
				require.Len(t, events, 1)
				assert.Equal(t, agentruntime.TextDelta{Text: "aside"}, events[0])
			},
		},
		{
			name:    "thinking.delta emits ThinkingDelta",
			kind:    EventThinkingDelta,
			payload: `{"text":"thinking..."}`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.NoError(t, err)
				require.Len(t, events, 1)
				assert.Equal(t, agentruntime.ThinkingDelta{Text: "thinking..."}, events[0])
			},
		},
		{
			name:    "reasoning.delta emits ThinkingDelta",
			kind:    EventReasoningDelta,
			payload: `{"text":"reasoning"}`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.NoError(t, err)
				require.Len(t, events, 1)
				assert.Equal(t, agentruntime.ThinkingDelta{Text: "reasoning"}, events[0])
			},
		},
		{
			name:    "reasoning.available emits ThinkingDelta",
			kind:    EventReasoningAvail,
			payload: `{"text":"available"}`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.NoError(t, err)
				require.Len(t, events, 1)
				assert.Equal(t, agentruntime.ThinkingDelta{Text: "available"}, events[0])
			},
		},
		{
			name:    "tool.start emits ToolCall with raw input and canonical write_file",
			kind:    EventToolStart,
			payload: `{"tool_id":"t1","name":"write_file","args":{"path":"a.txt","content":"hi\n"}}`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.NoError(t, err)
				require.Len(t, events, 1)
				call, ok := events[0].(agentruntime.ToolCall)
				require.True(t, ok)
				assert.Equal(t, "t1", call.ID)
				assert.Equal(t, "write_file", call.Name)
				assert.JSONEq(t, `{"path":"a.txt","content":"hi\n"}`, string(call.Input))
				fw, ok := call.Canonical.(canonical.FileWrite)
				require.True(t, ok, "write_file must map to canonical.FileWrite")
				assert.Equal(t, "a.txt", fw.Path)
				assert.Equal(t, "hi\n", fw.Content)
				assert.Equal(t, 1, fw.Lines)
			},
		},
		{
			name:    "tool.start with empty args keeps raw card (boundary)",
			kind:    EventToolStart,
			payload: `{"tool_id":"t2","name":"terminal"}`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.NoError(t, err)
				require.Len(t, events, 1)
				call, ok := events[0].(agentruntime.ToolCall)
				require.True(t, ok)
				assert.Empty(t, call.Input)
				assert.Nil(t, call.Canonical)
			},
		},
		{
			name:    "tool.start without a name emits nothing (boundary)",
			kind:    EventToolStart,
			payload: `{"tool_id":"t3"}`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.NoError(t, err)
				assert.Empty(t, events)
			},
		},
		{
			name:    "tool.generating emits OutputActivity",
			kind:    EventToolGenerating,
			payload: `{"name":"terminal"}`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.NoError(t, err)
				require.Len(t, events, 1)
				assert.Equal(t, agentruntime.OutputActivity{}, events[0])
			},
		},
		{
			name:    "tool.complete emits a successful ToolResult",
			kind:    EventToolComplete,
			payload: `{"tool_id":"t1","name":"terminal","result":{"output":"ok"}}`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.NoError(t, err)
				require.Len(t, events, 1)
				res, ok := events[0].(agentruntime.ToolResult)
				require.True(t, ok)
				assert.Equal(t, "t1", res.ToolCallID)
				assert.False(t, res.IsError)
			},
		},
		{
			name:    "tool.complete surfaces an error result",
			kind:    EventToolComplete,
			payload: `{"tool_id":"t2","name":"terminal","result":{"error":"boom"}}`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.NoError(t, err)
				require.Len(t, events, 1)
				res, ok := events[0].(agentruntime.ToolResult)
				require.True(t, ok)
				assert.True(t, res.IsError)
			},
		},
		{
			name:    "tool.complete with a string result keeps the text",
			kind:    EventToolComplete,
			payload: `{"tool_id":"t3","name":"terminal","result":"pong"}`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.NoError(t, err)
				require.Len(t, events, 1)
				res := events[0].(agentruntime.ToolResult)
				assert.Equal(t, "pong", res.Content)
			},
		},
		{
			name:    "todo.updated emits a canonical PlanUpdated",
			kind:    EventTodoUpdated,
			payload: `{"todos":[{"id":"1","content":"first","status":"in_progress"},{"id":"2","content":"second","status":"completed"}]}`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.NoError(t, err)
				require.Len(t, events, 1)
				plan, ok := events[0].(agentruntime.PlanUpdated)
				require.True(t, ok)
				require.Len(t, plan.Plan.Steps, 2)
				assert.Equal(t, canonical.StepInProgress, plan.Plan.Steps[0].Status)
				assert.Equal(t, canonical.StepCompleted, plan.Plan.Steps[1].Status)
			},
		},
		{
			name:    "status.update emits RuntimeStatus",
			kind:    EventStatusUpdate,
			payload: `{"kind":"compacting","text":"compressing context"}`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.NoError(t, err)
				require.Len(t, events, 1)
				assert.Equal(t, agentruntime.RuntimeStatus{Status: "compressing context"}, events[0])
			},
		},
		{
			name:    "session.usage returns cumulative usage without events",
			kind:    EventSessionUsage,
			payload: `{"usage":{"model":"hermes-3","prompt":120,"completion":10,"total":130,"calls":2}}`,
			check: func(t *testing.T, events []agentruntime.Event, usage *provider.Usage, err error) {
				require.NoError(t, err)
				assert.Empty(t, events)
				require.NotNil(t, usage)
				assert.Equal(t, 120, usage.PromptTokens)
				assert.Equal(t, 10, usage.CompletionTokens)
			},
		},
		{
			name:    "error frame emits ErrorEvent and a stop error",
			kind:    EventError,
			payload: `{"message":"provider exploded"}`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "provider exploded")
				require.Len(t, events, 1)
				_, ok := events[0].(agentruntime.ErrorEvent)
				assert.True(t, ok)
			},
		},
		{
			name:    "message.complete normal turn reports usage and no stop error",
			kind:    EventMessageComplete,
			payload: `{"text":"done","status":"complete","usage":{"model":"hermes-3","prompt":200,"completion":20,"total":220}}`,
			check: func(t *testing.T, events []agentruntime.Event, usage *provider.Usage, err error) {
				require.NoError(t, err)
				assert.Empty(t, events)
				require.NotNil(t, usage)
				assert.Equal(t, 200, usage.PromptTokens)
				assert.Equal(t, "hermes-3", usageModelFromEvent(Event{Kind: EventMessageComplete, Payload: rawJSON(t, `{"text":"done","status":"complete","usage":{"model":"hermes-3"}}`)}))
			},
		},
		{
			name:    "message.complete with status error emits ErrorEvent and stop error",
			kind:    EventMessageComplete,
			payload: `{"text":"","status":"error","error":"bad key","usage":{"prompt":1}}`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "bad key")
				require.Len(t, events, 1)
				_, ok := events[0].(agentruntime.ErrorEvent)
				assert.True(t, ok)
			},
		},
		{
			name:    "message.complete with status interrupted maps to ErrAborted",
			kind:    EventMessageComplete,
			payload: `{"text":"","status":"interrupted"}`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.ErrorIs(t, err, agentruntime.ErrAborted)
				assert.Empty(t, events)
			},
		},
		{
			name:    "unknown frame kind emits nothing",
			kind:    EventKind("notification.show"),
			payload: `{"text":"hi"}`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.NoError(t, err)
				assert.Empty(t, events)
			},
		},
		{
			name:    "approval.request is deferred to stage 2 (no events, no crash)",
			kind:    EventApprovalRequest,
			payload: `{"session_id":"live-1","request_id":"req-1","command":"rm -rf /"}`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.NoError(t, err)
				assert.Empty(t, events)
			},
		},
		{
			name:    "malformed payload is tolerated",
			kind:    EventMessageDelta,
			payload: `{not json`,
			check: func(t *testing.T, events []agentruntime.Event, _ *provider.Usage, err error) {
				require.NoError(t, err)
				assert.Empty(t, events)
			},
		},
	}

	Convey("Given the hermes translator", t, func() {
		for _, c := range cases {
			Convey("When translating "+c.name, func() {
				ev := Event{Kind: c.kind, Payload: rawJSON(t, c.payload)}
				events, usage, err := translate(ev)
				c.check(t, events, usage, err)
			})
		}
	})
}

func TestRecognizeCanonical_HermesPatch(t *testing.T) {
	Convey("Given Hermes' patch tool in replace mode", t, func() {
		input := rawJSON(t, `{"mode":"replace","path":"a.txt","old_string":"old","new_string":"new"}`)
		got := recognizeCanonical("patch", input)
		edit, ok := got.(canonical.FileEdit)
		So(ok, ShouldBeTrue)
		So(len(edit.Files), ShouldEqual, 1)
		So(edit.Files[0].Path, ShouldEqual, "a.txt")
	})
	Convey("Given Hermes' patch tool in a non-replace mode Then it stays raw", t, func() {
		input := rawJSON(t, `{"mode":"create","path":"a.txt","content":"x"}`)
		So(recognizeCanonical("patch", input), ShouldBeNil)
	})
	Convey("Given an unknown tool Then it stays raw", t, func() {
		So(recognizeCanonical("terminal", rawJSON(t, `{"command":"ls"}`)), ShouldBeNil)
	})
	Convey("Given malformed input Then it stays raw", t, func() {
		So(recognizeCanonical("write_file", rawJSON(t, `{not json`)), ShouldBeNil)
	})
}

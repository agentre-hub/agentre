package piagent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	pkgpi "github.com/agentre-hub/agentre/pkg/piagent"
)

func TestSelectSubagentCandidate_NameGateRunsBeforeClassificationAndTrackerCreation(t *testing.T) {
	classifyCalls := 0
	trackerCalls := 0
	selector := subagentSelector{
		classify: func([]byte) (subagentInvocation, bool) {
			classifyCalls++
			return subagentInvocation{
				Mode: subagentModeSingle,
				Runs: []invocationRun{{ID: "run-0", Agent: "worker", Task: "inspect"}},
			}, true
		},
		newTracker: func(outerID string, inv subagentInvocation) *subagentTracker {
			trackerCalls++
			return newSubagentTracker(outerID, inv)
		},
	}

	// Given a non-subagent name with otherwise valid input, when selection runs,
	// then neither the invocation classifier nor tracker/decoder boundary is called.
	tracker, spawn := selector.selectCandidate("delegate_task", "outer", []byte(`{"agent":"worker","task":"inspect"}`))
	assert.Nil(t, tracker)
	assert.Nil(t, spawn)
	assert.Zero(t, classifyCalls)
	assert.Zero(t, trackerCalls)

	// Given a mixed-case namespaced subagent name, when selection runs, then the
	// same candidate reaches both boundaries exactly once.
	tracker, spawn = selector.selectCandidate("Vendor__SubAgent", "outer", []byte(`{"agent":"worker","task":"inspect"}`))
	require.NotNil(t, tracker)
	require.NotNil(t, spawn)
	assert.Equal(t, 1, classifyCalls)
	assert.Equal(t, 1, trackerCalls)

	_, globallyRecognized := canonical.FromToolUse("Vendor__SubAgent", map[string]any{"agent": "worker", "task": "inspect"})
	assert.False(t, globallyRecognized, "fuzzy Pi recognition must not broaden global canonical matching")
}

func TestClassifySubagentInvocation_SingleAndFlatContracts(t *testing.T) {
	t.Run("official single accepts inactive arrays and blank optional strings", func(t *testing.T) {
		inv, ok := classifySubagentInvocation([]byte(`{
			"agent":" worker ","task":" inspect ","tasks":[],"chain":[],
			"model":"   ","thinking":" ","cwd":""
		}`))
		require.True(t, ok)
		assert.Equal(t, subagentModeSingle, inv.Mode)
		assert.Equal(t, envelopePending, inv.Envelope)
		require.Len(t, inv.Runs, 1)
		assert.Equal(t, "worker", inv.Runs[0].Agent)
		assert.Equal(t, "inspect", inv.Runs[0].Task)
		assert.Empty(t, inv.Runs[0].RequestedModel)
	})

	t.Run("profile flat locks immediately and keeps profile separate", func(t *testing.T) {
		inv, ok := classifySubagentInvocation([]byte(`{"task":"audit","profile":"read-only","model":"gpt-requested"}`))
		require.True(t, ok)
		assert.Equal(t, subagentModeSingle, inv.Mode)
		assert.Equal(t, envelopeFlat, inv.Envelope)
		require.Len(t, inv.Runs, 1)
		assert.Equal(t, "read-only", inv.Runs[0].Profile)
		assert.Empty(t, inv.Runs[0].Agent)
	})

	t.Run("official grouped modes preserve per-run options and enter the stateful runtime slice", func(t *testing.T) {
		parallelInput := []byte(`{"tasks":[{"agent":"a","task":"one","model":" requested-model ","thinking":"high","cwd":"/tmp/work"}]}`)
		parallel, ok := classifySubagentInvocation(parallelInput)
		require.True(t, ok)
		assert.Equal(t, subagentModeParallel, parallel.Mode)
		assert.Equal(t, envelopeOfficial, parallel.Envelope)
		require.Len(t, parallel.Runs, 1)
		assert.Equal(t, "requested-model", parallel.Runs[0].RequestedModel)
		parallelTracker, parallelSpawn := defaultSubagentSelector.selectCandidate("subagent", "outer-parallel", parallelInput)
		require.NotNil(t, parallelTracker)
		require.NotNil(t, parallelSpawn)
		assert.Equal(t, "parallel", parallelSpawn.Mode)
		require.Len(t, parallelSpawn.Runs, 1)
		assert.Equal(t, "requested-model", parallelSpawn.Runs[0].RequestedModel)

		chainInput := []byte(`{"chain":[{"agent":"a","task":"one"},{"agent":"b","task":"two"}]}`)
		chain, ok := classifySubagentInvocation(chainInput)
		require.True(t, ok)
		assert.Equal(t, subagentModeChain, chain.Mode)
		require.Len(t, chain.Runs, 2)
		chainTracker, chainSpawn := defaultSubagentSelector.selectCandidate("subagent", "outer-chain", chainInput)
		require.NotNil(t, chainTracker)
		require.NotNil(t, chainSpawn)
		assert.Equal(t, "chain", chainSpawn.Mode)
	})

	for name, input := range map[string]string{
		"malformed json":                `{`,
		"non object":                    `[]`,
		"control only":                  `{"task_id":"x","action":"stop"}`,
		"known poison":                  `{"agent":"worker","task":"inspect","agentScope":42}`,
		"null optional string poison":   `{"agent":"worker","task":"inspect","model":null}`,
		"null unused string poison":     `{"tasks":[{"agent":"worker","task":"inspect"}],"profile":null}`,
		"null grouped model poison":     `{"tasks":[{"agent":"worker","task":"inspect","model":null}]}`,
		"null confirmation bool poison": `{"agent":"worker","task":"inspect","confirmProjectAgents":null}`,
		"ambiguous official":            `{"agent":"worker","task":"inspect","tasks":[{"agent":"other","task":"other"}]}`,
		"oversized parallel":            `{"tasks":[{"agent":"a","task":"1"},{"agent":"a","task":"2"},{"agent":"a","task":"3"},{"agent":"a","task":"4"},{"agent":"a","task":"5"},{"agent":"a","task":"6"},{"agent":"a","task":"7"},{"agent":"a","task":"8"},{"agent":"a","task":"9"}]}`,
		"task without identity":         `{"task":"inspect"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, ok := classifySubagentInvocation([]byte(input))
			assert.False(t, ok)
		})
	}
}

func TestSubagentTracker_PendingEnvelopeLocksOnceAndStreamsOfficialStepsExactlyOnce(t *testing.T) {
	inv, ok := classifySubagentInvocation([]byte(`{"agent":"worker","task":"inspect","tasks":[],"chain":[],"model":"requested"}`))
	require.True(t, ok)
	tracker := newSubagentTracker("outer", inv)

	ambiguous := []byte(`{"details":{"results":[],"messages":[]}}`)
	events, changed := tracker.consumeUpdate(ambiguous)
	assert.Empty(t, events)
	assert.False(t, changed)
	assert.Equal(t, envelopePending, tracker.envelope)

	mismatched := []byte(`{"details":{"mode":"parallel","results":[]}}`)
	events, changed = tracker.consumeUpdate(mismatched)
	assert.Empty(t, events)
	assert.False(t, changed)
	assert.Equal(t, envelopePending, tracker.envelope)

	poisonedDual := []byte(`{"details":{"results":"bad","messages":[]}}`)
	events, changed = tracker.consumeUpdate(poisonedDual)
	assert.Empty(t, events)
	assert.False(t, changed)
	assert.Equal(t, envelopePending, tracker.envelope)

	callSnapshot := []byte(`{"details":{"mode":"single","results":[{"messages":[
		{"role":"assistant","model":"observed-model","content":[{"type":"toolCall","id":"inner","name":"read","arguments":{"path":"a.go"}}],"stopReason":"toolUse"}
	]}]}}`)
	events, changed = tracker.consumeUpdate(callSnapshot)
	require.Len(t, events, 1)
	call := events[0].(agentruntime.ToolCall)
	assert.Equal(t, "outer", call.ParentToolCallID)
	assert.Equal(t, "run-0", call.SubagentRunID)
	assert.NotEqual(t, "inner", call.ID)
	assert.True(t, changed)
	assert.Equal(t, envelopeOfficial, tracker.envelope)
	assert.Equal(t, "observed-model", tracker.info().Runs[0].Model)
	assert.Equal(t, 1, tracker.info().Runs[0].ToolUses)
	assert.Equal(t, "running", tracker.info().Runs[0].Status)

	resultSnapshot := []byte(`{"details":{"results":[{"messages":[
		{"role":"assistant","model":"observed-model","content":[{"type":"toolCall","id":"inner","name":"read","arguments":{"path":"a.go"}}],"stopReason":"toolUse"},
		{"role":"toolResult","toolCallId":"inner","content":[{"type":"text","text":"ok"}],"isError":false}
	]}]}}`)
	events, _ = tracker.consumeUpdate(resultSnapshot)
	require.Len(t, events, 1)
	result := events[0].(agentruntime.ToolResult)
	assert.Equal(t, call.ID, result.ToolCallID)
	assert.Equal(t, "run-0", result.SubagentRunID)
	assert.Equal(t, "ok", result.Content)

	events, changed = tracker.consumeUpdate(resultSnapshot)
	assert.Empty(t, events)
	assert.False(t, changed)

	strayFlat := []byte(`{"details":{"messages":[{"role":"assistant","content":[{"type":"toolCall","id":"other","name":"bash","arguments":{}}]}]}}`)
	events, changed = tracker.consumeUpdate(strayFlat)
	assert.Empty(t, events)
	assert.False(t, changed)
	assert.Equal(t, envelopeOfficial, tracker.envelope)
}

func TestSubagentTracker_ParallelIsolationOrderingAndCumulativeDedupe(t *testing.T) {
	// Given an official parallel invocation whose sibling runs reuse an inner ID
	// and advance in timestamp order different from input order.
	inv, ok := classifySubagentInvocation([]byte(`{"tasks":[
		{"agent":"first","task":"one"},
		{"agent":"second","task":"two"},
		{"agent":"third","task":"three"}
	]}`))
	require.True(t, ok)
	tracker := newSubagentTracker("outer:parallel", inv)

	initial := tracker.info()
	assert.Equal(t, "parallel", initial.Mode)
	assert.Equal(t, "running", initial.Status)
	assert.Equal(t, []string{"waiting", "waiting", "waiting"}, []string{
		initial.Runs[0].Status, initial.Runs[1].Status, initial.Runs[2].Status,
	})

	mismatched := []byte(`{"details":{"mode":"chain","results":[{"messages":[{"role":"assistant","content":[{"type":"toolCall","id":"ignored","name":"read","arguments":{}}]}]}]}}`)
	events, changed := tracker.consumeUpdate(mismatched)
	assert.Empty(t, events)
	assert.False(t, changed, "official grouped mode is fixed by the input and cannot be remapped by snapshots")

	update := []byte(`{"details":{"mode":"parallel","results":[
		{"agent":"second","task":"changed","messages":[{"role":"assistant","timestamp":200,"model":"model-first","content":[{"type":"toolCall","id":"shared","name":"read","arguments":{"path":"one.go"}}],"stopReason":"toolUse"}]},
		{"agent":"first","task":"changed","messages":[{"role":"assistant","timestamp":100,"model":"model-second","content":[{"type":"toolCall","id":"shared","name":"bash","arguments":{"command":"pwd"}}],"stopReason":"toolUse"}]},
		{"messages":"malformed"}
	]}}`)
	events, changed = tracker.consumeUpdate(update)
	require.True(t, changed)
	require.Len(t, events, 2)
	secondCall := events[0].(agentruntime.ToolCall)
	firstCall := events[1].(agentruntime.ToolCall)
	assert.Equal(t, "run-1", secondCall.SubagentRunID, "valid timestamps order newly discovered sibling boundaries")
	assert.Equal(t, "run-0", firstCall.SubagentRunID)
	assert.NotEqual(t, firstCall.ID, secondCall.ID, "outer+run+inner identity must isolate equal inner IDs")
	assert.Equal(t, "outer:parallel", firstCall.ParentToolCallID)
	assert.Equal(t, "first", tracker.info().Runs[0].Agent, "result values cannot replace input-slot identity")
	assert.Equal(t, "one", tracker.info().Runs[0].Task)
	assert.Equal(t, "second", tracker.info().Runs[1].Agent)
	assert.Equal(t, "model-first", tracker.info().Runs[0].Model)
	assert.Equal(t, "model-second", tracker.info().Runs[1].Model)
	assert.Empty(t, tracker.info().Runs[2].Model, "one malformed run must not suppress or contaminate siblings")

	// When the next cumulative snapshot repeats both calls and adds their results,
	// then only the new result boundaries are emitted, in deterministic timestamp order.
	cumulative := []byte(`{"details":{"mode":"parallel","results":[
		{"messages":[
			{"role":"assistant","timestamp":200,"model":"model-first","content":[{"type":"toolCall","id":"shared","name":"read","arguments":{"path":"one.go"}}],"stopReason":"toolUse"},
			{"role":"toolResult","timestamp":400,"toolCallId":"shared","content":[{"type":"text","text":"one"}]}
		]},
		{"messages":[
			{"role":"assistant","timestamp":100,"model":"model-second","content":[{"type":"toolCall","id":"shared","name":"bash","arguments":{"command":"pwd"}}],"stopReason":"toolUse"},
			{"role":"toolResult","timestamp":300,"toolCallId":"shared","content":[{"type":"text","text":"two"}]}
		]},
		{"messages":[]}
	]}}`)
	events, changed = tracker.consumeUpdate(cumulative)
	assert.False(t, changed, "child results without metadata changes do not require a progress snapshot")
	require.Len(t, events, 2)
	secondResult := events[0].(agentruntime.ToolResult)
	firstResult := events[1].(agentruntime.ToolResult)
	assert.Equal(t, secondCall.ID, secondResult.ToolCallID)
	assert.Equal(t, firstCall.ID, firstResult.ToolCallID)

	events, changed = tracker.consumeUpdate(cumulative)
	assert.Empty(t, events)
	assert.False(t, changed, "repeated cumulative snapshots must be idempotent")
}

func TestSubagentTracker_ParallelFallbackOrderingIsDeterministic(t *testing.T) {
	for name, timestamps := range map[string][2]string{
		"missing": {``, `,"timestamp":100`},
		"equal":   {`,"timestamp":100`, `,"timestamp":100`},
	} {
		t.Run(name, func(t *testing.T) {
			inv, ok := classifySubagentInvocation([]byte(`{"tasks":[{"agent":"a","task":"one"},{"agent":"b","task":"two"}]}`))
			require.True(t, ok)
			tracker := newSubagentTracker("outer", inv)
			details := []byte(fmt.Sprintf(`{"details":{"mode":"parallel","results":[
				{"messages":[{"role":"assistant"%s,"content":[{"type":"toolCall","id":"a","name":"read","arguments":{}}]}]},
				{"messages":[{"role":"assistant"%s,"content":[{"type":"toolCall","id":"b","name":"read","arguments":{}}]}]}
			]}}`, timestamps[0], timestamps[1]))
			events, _ := tracker.consumeUpdate(details)
			require.Len(t, events, 2)
			assert.Equal(t, "run-0", events[0].(agentruntime.ToolCall).SubagentRunID)
			assert.Equal(t, "run-1", events[1].(agentruntime.ToolCall).SubagentRunID)
		})
	}
}

func TestSubagentTracker_ParallelFinalizationAndAggregateStatus(t *testing.T) {
	newTracker := func(t *testing.T) *subagentTracker {
		t.Helper()
		inv, ok := classifySubagentInvocation([]byte(`{"tasks":[
			{"agent":"a","task":"one"},
			{"agent":"b","task":"two"},
			{"agent":"c","task":"three"}
		]}`))
		require.True(t, ok)
		return newSubagentTracker("outer", inv)
	}

	t.Run("running exit zero is not terminal without stop evidence", func(t *testing.T) {
		tracker := newTracker(t)
		_, changed := tracker.consumeUpdate([]byte(`{"details":{"mode":"parallel","results":[
			{"exitCode":0,"messages":[]},{"exitCode":-1,"status":"waiting","messages":[]},{"exitCode":-1,"status":"pending","messages":[]}
		]}}`))
		assert.True(t, changed)
		assert.Equal(t, "running", tracker.info().Runs[0].Status)
		assert.Equal(t, "waiting", tracker.info().Runs[1].Status)
		assert.Equal(t, "running", tracker.info().Status)
	})

	t.Run("parallel placeholder source metadata does not prove activity and unknown source stays absent", func(t *testing.T) {
		tracker := newTracker(t)
		_, changed := tracker.consumeUpdate([]byte(`{"details":{"mode":"parallel","results":[
			{"exitCode":-1,"agentSource":"unknown","messages":[]},
			{"exitCode":-1,"agentSource":"project","messages":[]},
			{"exitCode":-1,"messages":[]}
		]}}`))

		assert.True(t, changed, "known project source enriches metadata")
		assert.Equal(t, []string{"waiting", "waiting", "waiting"}, []string{
			tracker.info().Runs[0].Status, tracker.info().Runs[1].Status, tracker.info().Runs[2].Status,
		})
		assert.Empty(t, tracker.info().Runs[0].AgentSource)
		assert.Equal(t, "project", tracker.info().Runs[1].AgentSource)
	})

	t.Run("mixed terminal outcomes become partial", func(t *testing.T) {
		tracker := newTracker(t)
		_, _ = tracker.consumeFinal([]byte(`{"mode":"parallel","results":[
			{"exitCode":0,"messages":[],"output":"first done"},
			{"exitCode":2,"messages":[],"error":"second failed"},
			{"exitCode":0,"messages":[]}
		]}`), true, "outer failed")
		info := tracker.info()
		assert.Equal(t, []string{"completed", "failed", "completed"}, []string{
			info.Runs[0].Status, info.Runs[1].Status, info.Runs[2].Status,
		})
		assert.Equal(t, "partial", info.Status, "complete grouped evidence is authoritative over outer isError")
		assert.Equal(t, "first done", info.Runs[0].Summary)
	})

	t.Run("authoritative final nonzero exit overrides update-time completion", func(t *testing.T) {
		tracker := newTracker(t)
		_, _ = tracker.consumeUpdate([]byte(`{"details":{"mode":"parallel","results":[
			{"messages":[{"role":"assistant","content":[],"stopReason":"stop"}]},
			{"messages":[]},{"messages":[]}
		]}}`))
		assert.Equal(t, "completed", tracker.info().Runs[0].Status)

		_, _ = tracker.consumeFinal([]byte(`{"mode":"parallel","results":[
			{"exitCode":2,"stopReason":"stop","messages":[{"role":"assistant","content":[],"stopReason":"stop"}]},
			{"exitCode":0,"messages":[]},{"exitCode":0,"messages":[]}
		]}`), false, "outer")

		assert.Equal(t, "failed", tracker.info().Runs[0].Status)
		assert.Equal(t, "partial", tracker.info().Status)
	})

	t.Run("incomplete success is unknown and outer error only changes aggregate", func(t *testing.T) {
		success := newTracker(t)
		_, _ = success.consumeFinal([]byte(`{"mode":"parallel","results":[{"exitCode":0,"messages":[]}]}`), false, "outer")
		assert.Equal(t, []string{"completed", "unknown", "unknown"}, []string{
			success.info().Runs[0].Status, success.info().Runs[1].Status, success.info().Runs[2].Status,
		})
		assert.Equal(t, "unknown", success.info().Status)

		failedOuter := newTracker(t)
		_, _ = failedOuter.consumeFinal(nil, true, "outer failed")
		assert.Equal(t, []string{"unknown", "unknown", "unknown"}, []string{
			failedOuter.info().Runs[0].Status, failedOuter.info().Runs[1].Status, failedOuter.info().Runs[2].Status,
		})
		assert.Equal(t, "failed", failedOuter.info().Status)
	})
}

func TestSubagentTracker_ChainSequencingFinalizationAndUnboundedLength(t *testing.T) {
	items := make([]string, 9)
	for i := range items {
		items[i] = `{"agent":"worker","task":"step"}`
	}
	input := []byte(`{"chain":[` + strings.Join(items, ",") + `]}`)
	inv, ok := classifySubagentInvocation(input)
	require.True(t, ok, "the parallel-only eight-item cap must not reject a valid chain")
	require.Len(t, inv.Runs, 9)
	tracker, spawn := defaultSubagentSelector.selectCandidate("subagent", "outer-chain", input)
	require.NotNil(t, tracker)
	require.NotNil(t, spawn)
	assert.Equal(t, "chain", spawn.Mode)
	require.Len(t, spawn.Runs, 9)

	initial := tracker.info()
	assert.Equal(t, "running", initial.Runs[0].Status)
	for index := 1; index < len(initial.Runs); index++ {
		assert.Equal(t, "waiting", initial.Runs[index].Status)
	}

	// Given the second represented run fails, when partial final details arrive,
	// then untouched later input slots are skipped without invented child activity.
	events, _ := tracker.consumeFinal([]byte(`{"mode":"chain","results":[
		{"exitCode":0,"messages":[{"role":"assistant","content":[{"type":"text","text":"first done"}],"stopReason":"stop"}]},
		{"exitCode":1,"messages":[],"error":"second failed"}
	]}`), true, "outer failed")
	assert.Empty(t, events)
	info := tracker.info()
	assert.Equal(t, "completed", info.Runs[0].Status)
	assert.Equal(t, "failed", info.Runs[1].Status)
	for index := 2; index < len(info.Runs); index++ {
		assert.Equal(t, "skipped", info.Runs[index].Status)
		assert.Zero(t, info.Runs[index].ToolUses)
		assert.Empty(t, info.Runs[index].Model)
		assert.Empty(t, info.Runs[index].Summary)
	}
	assert.Equal(t, "failed", info.Status)
}

func TestDrainStream_NormalCompletionFinalizesIncompleteSubagentsAsUnknown(t *testing.T) {
	stream := &scriptedStream{events: []pkgpi.Event{
		{Kind: pkgpi.EventPreToolUse, Tool: pkgpi.ToolEvent{
			ID: "outer", Name: "subagent",
			Input: []byte(`{"tasks":[{"agent":"a","task":"one"},{"agent":"b","task":"two"}]}`),
		}},
	}}

	got := drainForTest(t, stream)
	require.Len(t, got, 5)
	assert.IsType(t, agentruntime.ToolCall{}, got[0])
	assert.IsType(t, agentruntime.SubagentStarted{}, got[1])
	progress := got[2].(agentruntime.SubagentProgress)
	assert.Equal(t, "unknown", progress.Info.Status)
	assert.Equal(t, []string{"unknown", "unknown"}, []string{
		progress.Info.Runs[0].Status, progress.Info.Runs[1].Status,
	})
	done := got[3].(agentruntime.SubagentDone)
	assert.Equal(t, progress.Info, done.Info)
	assert.IsType(t, agentruntime.Done{}, got[4])
}

func TestDrainStream_AbortFinalizesNonTerminalParallelRuns(t *testing.T) {
	stream := &scriptedStream{events: []pkgpi.Event{
		{Kind: pkgpi.EventPreToolUse, Tool: pkgpi.ToolEvent{
			ID: "outer", Name: "subagent",
			Input: []byte(`{"tasks":[{"agent":"a","task":"one"},{"agent":"b","task":"two"}]}`),
		}},
		{Kind: pkgpi.EventToolUseUpdate, Tool: pkgpi.ToolEvent{
			ID: "outer", Name: "subagent",
			PartialResult: []byte(`{"details":{"mode":"parallel","results":[
				{"messages":[{"role":"assistant","content":[{"type":"text","text":"done"}],"stopReason":"stop"}]},
				{"messages":[{"role":"assistant","content":[{"type":"toolCall","id":"inner","name":"read","arguments":{}}],"stopReason":"toolUse"}]}
			]}}`),
		}},
	}}
	active := &activeSession{}
	active.setAbortRequested(true)

	got := drainForTestWithActive(t, stream, active)
	require.GreaterOrEqual(t, len(got), 7)
	started := got[1].(agentruntime.SubagentStarted)
	assert.Equal(t, "parallel", started.Info.Mode)

	var final *agentruntime.SubagentDone
	for _, event := range got {
		if done, ok := event.(agentruntime.SubagentDone); ok {
			final = &done
		}
	}
	require.NotNil(t, final, "stateful drain must expose a terminal snapshot before abort error")
	assert.Equal(t, "completed", final.Info.Runs[0].Status, "already terminal siblings are preserved")
	assert.Equal(t, "canceled", final.Info.Runs[1].Status)
	assert.Equal(t, "canceled", final.Info.Status)
	assert.IsType(t, agentruntime.ErrorEvent{}, got[len(got)-1])
}

func TestSubagentTracker_ResultBeforeCallAndFinalRecovery(t *testing.T) {
	inv, ok := classifySubagentInvocation([]byte(`{"task":"inspect","profile":"read-only"}`))
	require.True(t, ok)
	tracker := newSubagentTracker("outer", inv)

	orphan := []byte(`{"details":{"messages":[{"role":"toolResult","toolCallId":"late","content":[]} ]}}`)
	events, _ := tracker.consumeUpdate(orphan)
	assert.Empty(t, events)

	finalDetails := []byte(`{"messages":[
		{"role":"toolResult","toolCallId":"late","content":[]},
		{"role":"assistant","model":"actual","content":[{"type":"toolCall","id":"late","name":"bash","arguments":{"command":"pwd"}}],"stopReason":"toolUse"},
		{"role":"assistant","model":"actual","content":[{"type":"text","text":"finished"}],"stopReason":"stop"}
	],"exitCode":0}`)
	events, changed := tracker.consumeFinal(finalDetails, false, "outer text")
	require.Len(t, events, 2)
	call := events[0].(agentruntime.ToolCall)
	result := events[1].(agentruntime.ToolResult)
	assert.Equal(t, call.ID, result.ToolCallID)
	assert.Empty(t, result.Content)
	assert.True(t, changed)
	info := tracker.info()
	assert.Equal(t, "actual", info.Runs[0].Model)
	assert.Equal(t, "finished", info.Runs[0].Summary)
	assert.Equal(t, "completed", info.Runs[0].Status)
}

func TestSubagentTracker_PartialErrorDoesNotFinishActiveRun(t *testing.T) {
	inv, ok := classifySubagentInvocation([]byte(`{"task":"inspect","profile":"write"}`))
	require.True(t, ok)
	tracker := newSubagentTracker("outer", inv)

	// Given an incremental snapshot that reports a broken model-response stream
	// while the outer subagent tool is still active, when the tracker consumes it,
	// then the diagnostic is retained without publishing a terminal run status.
	_, changed := tracker.consumeUpdate([]byte(`{"details":{"messages":[
		{"role":"assistant","content":[{"type":"toolCall","id":"first","name":"edit","arguments":{}}],"stopReason":"toolUse"}
	],"errorMessage":"OpenAI Responses stream ended before a terminal response event"}}`))
	require.True(t, changed)
	info := tracker.info()
	assert.Equal(t, "running", info.Status)
	assert.Equal(t, "running", info.Runs[0].Status)
	assert.Equal(t, "OpenAI Responses stream ended before a terminal response event", info.Runs[0].ErrorMessage)

	// And when later incremental child activity arrives, then it remains attached
	// to the running subagent rather than continuing beneath a failed card.
	events, changed := tracker.consumeUpdate([]byte(`{"details":{"messages":[
		{"role":"assistant","content":[{"type":"toolCall","id":"first","name":"edit","arguments":{}}],"stopReason":"toolUse"},
		{"role":"toolResult","toolCallId":"first","content":[{"type":"text","text":"ok"}]},
		{"role":"assistant","content":[{"type":"toolCall","id":"second","name":"bash","arguments":{}}],"stopReason":"toolUse"}
	],"errorMessage":"OpenAI Responses stream ended before a terminal response event"}}`))
	require.True(t, changed)
	require.Len(t, events, 2)
	assert.Equal(t, "running", tracker.info().Status)
	assert.Equal(t, 2, tracker.info().ToolUses)

	// When the same error reaches the real outer final boundary, then the run is
	// settled as failed because the subagent invocation has actually completed.
	_, changed = tracker.consumeFinal([]byte(`{"messages":[
		{"role":"assistant","content":[{"type":"toolCall","id":"first","name":"edit","arguments":{}}],"stopReason":"toolUse"},
		{"role":"toolResult","toolCallId":"first","content":[{"type":"text","text":"ok"}]},
		{"role":"assistant","content":[{"type":"toolCall","id":"second","name":"bash","arguments":{}}],"stopReason":"toolUse"}
	],"errorMessage":"OpenAI Responses stream ended before a terminal response event"}`), true, "OpenAI Responses stream ended before a terminal response event")
	require.True(t, changed)
	assert.Equal(t, "failed", tracker.info().Status)
	assert.Equal(t, "failed", tracker.info().Runs[0].Status)
}

func TestSubagentTracker_RetriedAttemptErrorDoesNotLatchRunAsFailed(t *testing.T) {
	// pi 把一次失败的模型请求作为一条 assistant 消息记进快照(stopReason=error +
	// errorMessage)后自动重试,快照每帧全量重放这些消息。
	const attemptError = `{"role":"assistant","content":[],"provider":"local-llm",` +
		`"model":"gpt-5.6-sol","stopReason":"error","errorMessage":"Connection error."}`
	newTracker := func(t *testing.T) *subagentTracker {
		t.Helper()
		inv, ok := classifySubagentInvocation([]byte(`{"task":"implement","profile":"write"}`))
		require.True(t, ok)
		return newSubagentTracker("outer", inv)
	}

	t.Run("a still-trailing attempt error reports the diagnostic without settling the run", func(t *testing.T) {
		tracker := newTracker(t)
		_, changed := tracker.consumeUpdate([]byte(`{"details":{"messages":[
			{"role":"assistant","content":[{"type":"toolCall","id":"first","name":"edit","arguments":{}}],"stopReason":"toolUse"},
			{"role":"toolResult","toolCallId":"first","content":[{"type":"text","text":"ok"}]},
			` + attemptError + `
		]}}`))

		require.True(t, changed)
		info := tracker.info()
		assert.Equal(t, "running", info.Runs[0].Status, "the outer subagent tool has not returned yet")
		assert.Equal(t, "running", info.Status)
		assert.Equal(t, "Connection error.", info.Runs[0].ErrorMessage)
	})

	t.Run("child activity after the attempt error clears it instead of running beneath a failed card", func(t *testing.T) {
		tracker := newTracker(t)
		_, _ = tracker.consumeUpdate([]byte(`{"details":{"messages":[
			{"role":"assistant","content":[{"type":"toolCall","id":"first","name":"edit","arguments":{}}],"stopReason":"toolUse"},
			{"role":"toolResult","toolCallId":"first","content":[{"type":"text","text":"ok"}]},
			` + attemptError + `
		]}}`))

		// Given pi retried, when the next full replay carries the retry's own child
		// call after the attempt error, then the card recovers rather than staying
		// pinned to FAILED / "Connection error." while it keeps calling tools.
		events, changed := tracker.consumeUpdate([]byte(`{"details":{"messages":[
			{"role":"assistant","content":[{"type":"toolCall","id":"first","name":"edit","arguments":{}}],"stopReason":"toolUse"},
			{"role":"toolResult","toolCallId":"first","content":[{"type":"text","text":"ok"}]},
			` + attemptError + `,
			{"role":"assistant","content":[{"type":"toolCall","id":"second","name":"bash","arguments":{}}],"stopReason":"toolUse"}
		]}}`))

		require.True(t, changed)
		require.Len(t, events, 1, "only the retry's new child call is emitted")
		info := tracker.info()
		assert.Equal(t, "running", info.Runs[0].Status)
		assert.Equal(t, "running", info.Status)
		assert.Empty(t, info.Runs[0].ErrorMessage, "the superseded attempt must not linger as the card's summary")
		assert.Equal(t, 2, info.ToolUses)
	})

	t.Run("a trailing attempt error at the real final boundary settles the run as failed", func(t *testing.T) {
		tracker := newTracker(t)
		_, changed := tracker.consumeFinal([]byte(`{"messages":[
			{"role":"assistant","content":[{"type":"toolCall","id":"first","name":"edit","arguments":{}}],"stopReason":"toolUse"},
			{"role":"toolResult","toolCallId":"first","content":[{"type":"text","text":"ok"}]},
			`+attemptError+`
		]}`), false, "")

		require.True(t, changed)
		info := tracker.info()
		assert.Equal(t, "failed", info.Runs[0].Status)
		assert.Equal(t, "failed", info.Status)
		assert.Equal(t, "Connection error.", info.Runs[0].ErrorMessage)
	})

	t.Run("a superseded attempt error does not override the final outcome", func(t *testing.T) {
		tracker := newTracker(t)
		_, changed := tracker.consumeFinal([]byte(`{"messages":[
			`+attemptError+`,
			{"role":"assistant","content":[{"type":"text","text":"task done"}],"stopReason":"stop"}
		],"exitCode":0}`), false, "")

		require.True(t, changed)
		info := tracker.info()
		assert.Equal(t, "completed", info.Runs[0].Status)
		assert.Empty(t, info.Runs[0].ErrorMessage)
		assert.Equal(t, "task done", info.Runs[0].Summary)
	})
}

func TestSubagentTracker_UsageFeedsPerRunAndAggregateTotalTokens(t *testing.T) {
	t.Run("flat single snapshots report usage in real time", func(t *testing.T) {
		inv, ok := classifySubagentInvocation([]byte(`{"task":"inspect","profile":"read-only"}`))
		require.True(t, ok)
		tracker := newSubagentTracker("outer", inv)

		// Given a flat progress frame whose details carry the consumed context size
		// (usage.contextTokens = pi's per-turn totalTokens, the live context window),
		// when the tracker consumes it, then it reports that context size — not the
		// sum of per-call input/output/cache tokens, which would double-count history.
		_, changed := tracker.consumeUpdate([]byte(`{"details":{"messages":[
			{"role":"assistant","model":"observed","content":[{"type":"text","text":"working"}],"stopReason":"toolUse"}
		],"usage":{"input":100,"output":20,"cacheRead":5,"cacheWrite":3,"cost":{"total":0.01},"contextTokens":512,"turns":1}}}`))
		require.True(t, changed)
		assert.Equal(t, 512, tracker.info().TotalTokens, "context size, not the 128-token per-call sum")

		// Given a later frame with a larger consumed context, then it updates.
		_, changed = tracker.consumeUpdate([]byte(`{"details":{"messages":[
			{"role":"assistant","model":"observed","content":[{"type":"text","text":"working"}],"stopReason":"toolUse"}
		],"usage":{"input":200,"output":40,"cacheRead":10,"cacheWrite":6,"cost":{"total":0.02},"contextTokens":1024,"turns":2}}}`))
		require.True(t, changed)
		assert.Equal(t, 1024, tracker.info().TotalTokens)
	})

	t.Run("zero-usage frames never wipe recorded totals", func(t *testing.T) {
		inv, ok := classifySubagentInvocation([]byte(`{"task":"inspect","profile":"read-only"}`))
		require.True(t, ok)
		tracker := newSubagentTracker("outer", inv)

		_, changed := tracker.consumeUpdate([]byte(`{"details":{"messages":[],"usage":{"input":100,"output":20,"cacheRead":5,"cacheWrite":3,"cost":{"total":0.01},"contextTokens":512,"turns":1}}}`))
		require.True(t, changed)
		assert.Equal(t, 512, tracker.info().TotalTokens)

		// A progress frame whose usage decodes to zero (or is absent) must not erase
		// the recorded context size; the producer's own frames are monotonic.
		_, _ = tracker.consumeUpdate([]byte(`{"details":{"messages":[],"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"cost":{"total":0},"contextTokens":0,"turns":0}}}`))
		assert.Equal(t, 512, tracker.info().TotalTokens, "zero usage does not overwrite a recorded context size")

		_, _ = tracker.consumeUpdate([]byte(`{"details":{"messages":[]}}`))
		assert.Equal(t, 512, tracker.info().TotalTokens, "missing usage does not wipe a recorded context size")
	})

	t.Run("parallel runs keep per-run totals and aggregate sums them", func(t *testing.T) {
		inv, ok := classifySubagentInvocation([]byte(`{"tasks":[
			{"agent":"a","task":"one"},
			{"agent":"b","task":"two"}
		]}`))
		require.True(t, ok)
		tracker := newSubagentTracker("outer", inv)

		_, changed := tracker.consumeUpdate([]byte(`{"details":{"mode":"parallel","results":[
			{"messages":[],"usage":{"input":90,"output":10,"cacheRead":0,"cacheWrite":0,"cost":{"total":0},"contextTokens":999,"turns":1}},
			{"messages":[],"usage":{"input":30,"output":5,"cacheRead":2,"cacheWrite":1,"cost":{"total":0},"contextTokens":77,"turns":1}}
		]}}`))
		require.True(t, changed)
		assert.Equal(t, 999, tracker.runs[0].totalTokens)
		assert.Equal(t, 77, tracker.runs[1].totalTokens)
		assert.Equal(t, 1076, tracker.info().TotalTokens, "aggregate sums each run's consumed context size")
	})

	t.Run("final flat snapshot carries usage into the terminal snapshot", func(t *testing.T) {
		inv, ok := classifySubagentInvocation([]byte(`{"task":"inspect","profile":"read-only"}`))
		require.True(t, ok)
		tracker := newSubagentTracker("outer", inv)

		_, changed := tracker.consumeFinal([]byte(`{"messages":[
			{"role":"assistant","model":"observed","content":[{"type":"text","text":"done"}],"stopReason":"stop"}
		],"exitCode":0,"usage":{"input":500,"output":60,"cacheRead":30,"cacheWrite":10,"cost":{"total":0.05},"contextTokens":777,"turns":3}}`), false, "outer")
		require.True(t, changed)
		info := tracker.info()
		assert.Equal(t, "completed", info.Runs[0].Status)
		assert.Equal(t, 777, info.TotalTokens, "context size, not the 600-token per-call sum")
	})
}

func TestSubagentTracker_NullToolResultContentIsMalformedRatherThanEmpty(t *testing.T) {
	inv, ok := classifySubagentInvocation([]byte(`{"task":"inspect","profile":"read-only"}`))
	require.True(t, ok)
	tracker := newSubagentTracker("outer", inv)

	events, _ := tracker.consumeUpdate([]byte(`{"details":{"messages":[
		{"role":"assistant","content":[{"type":"toolCall","id":"child","name":"bash","arguments":{}}],"stopReason":"toolUse"},
		{"role":"toolResult","toolCallId":"child","content":null}
	]}}`))

	require.Len(t, events, 1)
	assert.IsType(t, agentruntime.ToolCall{}, events[0])
	assert.False(t, tracker.runs[0].emittedResults["child"])
}

func TestSubagentTracker_FinalStatusFallbacksAndProjectConfirmationCancellation(t *testing.T) {
	t.Run("usable flat envelope without terminal evidence becomes unknown on outer success", func(t *testing.T) {
		inv, ok := classifySubagentInvocation([]byte(`{"task":"inspect","profile":"read-only"}`))
		require.True(t, ok)
		tracker := newSubagentTracker("outer", inv)
		_, _ = tracker.consumeFinal([]byte(`{"messages":[{"role":"assistant","content":[{"type":"text","text":"partial"}]}]}`), false, "outer")
		assert.Equal(t, "unknown", tracker.info().Status)
		assert.Equal(t, "unknown", tracker.info().Runs[0].Status)
	})

	t.Run("missing final envelope follows the sole outer result", func(t *testing.T) {
		inv, ok := classifySubagentInvocation([]byte(`{"agent":"worker","task":"inspect"}`))
		require.True(t, ok)
		tracker := newSubagentTracker("outer", inv)
		_, _ = tracker.consumeFinal(nil, false, "outer")
		assert.Equal(t, "completed", tracker.info().Runs[0].Status)

		tracker = newSubagentTracker("outer", inv)
		_, _ = tracker.consumeFinal(nil, true, "outer")
		assert.Equal(t, "failed", tracker.info().Runs[0].Status)
	})

	t.Run("null exit code is malformed terminal evidence rather than success", func(t *testing.T) {
		inv, ok := classifySubagentInvocation([]byte(`{"task":"inspect","profile":"read-only"}`))
		require.True(t, ok)
		tracker := newSubagentTracker("outer", inv)

		_, _ = tracker.consumeFinal([]byte(`{"messages":[],"exitCode":null}`), false, "outer")

		assert.Equal(t, "unknown", tracker.info().Status)
		assert.Equal(t, "unknown", tracker.info().Runs[0].Status)
	})

	t.Run("official aborted fails while flat aborted cancels", func(t *testing.T) {
		official, ok := classifySubagentInvocation([]byte(`{"agent":"worker","task":"inspect"}`))
		require.True(t, ok)
		officialTracker := newSubagentTracker("outer-official", official)
		_, _ = officialTracker.consumeFinal([]byte(`{"mode":"single","results":[{"messages":[{"role":"assistant","content":[],"stopReason":"aborted"}]}]}`), false, "outer")
		assert.Equal(t, "failed", officialTracker.info().Runs[0].Status)

		flat, ok := classifySubagentInvocation([]byte(`{"task":"inspect","profile":"read-only"}`))
		require.True(t, ok)
		flatTracker := newSubagentTracker("outer-flat", flat)
		_, _ = flatTracker.consumeFinal([]byte(`{"messages":[{"role":"assistant","content":[],"stopReason":"aborted"}]}`), true, "outer")
		assert.Equal(t, "canceled", flatTracker.info().Runs[0].Status)
	})

	t.Run("declined project agents with empty official results cancel", func(t *testing.T) {
		inv, ok := classifySubagentInvocation([]byte(`{"agent":"project-worker","task":"inspect","agentScope":"both"}`))
		require.True(t, ok)
		tracker := newSubagentTracker("outer", inv)
		_, _ = tracker.consumeFinal([]byte(`{"mode":"single","results":[]}`), false, "Canceled by client")
		assert.Equal(t, "canceled", tracker.info().Status)
		assert.Equal(t, "canceled", tracker.info().Runs[0].Status)
	})
}

func TestDrainStream_SubagentOrderingAndUnsupportedFallback(t *testing.T) {
	t.Run("supported final snapshot recovers children before done and outer result", func(t *testing.T) {
		stream := &scriptedStream{events: []pkgpi.Event{
			{Kind: pkgpi.EventPreToolUse, Tool: pkgpi.ToolEvent{ID: "outer", Name: "Vendor__SubAgent", Input: []byte(`{"task":"inspect","profile":"read-only","model":"requested"}`)}},
			{Kind: pkgpi.EventPostToolUse, Tool: pkgpi.ToolEvent{ID: "outer", Name: "Vendor__SubAgent", Content: "outer result", Details: []byte(`{"messages":[
				{"role":"assistant","model":"actual","content":[{"type":"toolCall","id":"inner","name":"read","arguments":{"path":"a.go"}}],"stopReason":"toolUse"},
				{"role":"toolResult","toolCallId":"inner","content":[{"type":"text","text":"ok"}]},
				{"role":"assistant","model":"actual","content":[{"type":"text","text":"done"}],"stopReason":"stop"}
			],"exitCode":0}`)}},
		}}
		got := drainForTest(t, stream)
		require.Len(t, got, 8)
		_, outerCall := got[0].(agentruntime.ToolCall)
		_, started := got[1].(agentruntime.SubagentStarted)
		_, childCall := got[2].(agentruntime.ToolCall)
		_, childResult := got[3].(agentruntime.ToolResult)
		_, progress := got[4].(agentruntime.SubagentProgress)
		_, done := got[5].(agentruntime.SubagentDone)
		outerResult, result := got[6].(agentruntime.ToolResult)
		_, turnDone := got[7].(agentruntime.Done)
		assert.True(t, outerCall && started && childCall && childResult && progress && done && result && turnDone)
		assert.Equal(t, "outer result", outerResult.Content)
	})

	t.Run("official parallel final recovery emits grouped children before aggregate completion", func(t *testing.T) {
		stream := &scriptedStream{events: []pkgpi.Event{
			{Kind: pkgpi.EventPreToolUse, Tool: pkgpi.ToolEvent{ID: "outer", Name: "subagent", Input: []byte(`{"tasks":[{"agent":"a","task":"one"},{"agent":"b","task":"two"}]}`)}},
			{Kind: pkgpi.EventPostToolUse, Tool: pkgpi.ToolEvent{ID: "outer", Name: "subagent", Content: "parallel done", Details: []byte(`{"mode":"parallel","results":[
				{"exitCode":0,"messages":[
					{"role":"assistant","timestamp":200,"content":[{"type":"toolCall","id":"same","name":"read","arguments":{}}],"stopReason":"toolUse"},
					{"role":"toolResult","timestamp":210,"toolCallId":"same","content":[{"type":"text","text":"one"}]},
					{"role":"assistant","timestamp":220,"content":[{"type":"text","text":"first"}],"stopReason":"stop"}
				]},
				{"exitCode":0,"messages":[
					{"role":"assistant","timestamp":100,"content":[{"type":"toolCall","id":"same","name":"bash","arguments":{}}],"stopReason":"toolUse"},
					{"role":"toolResult","timestamp":110,"toolCallId":"same","content":[{"type":"text","text":"two"}]},
					{"role":"assistant","timestamp":120,"content":[{"type":"text","text":"second"}],"stopReason":"stop"}
				]}
			]}`)}},
		}}
		got := drainForTest(t, stream)
		require.Len(t, got, 10)
		assert.IsType(t, agentruntime.ToolCall{}, got[0])
		assert.IsType(t, agentruntime.SubagentStarted{}, got[1])
		assert.Equal(t, "run-1", got[2].(agentruntime.ToolCall).SubagentRunID)
		assert.Equal(t, "run-1", got[3].(agentruntime.ToolResult).SubagentRunID)
		assert.Equal(t, "run-0", got[4].(agentruntime.ToolCall).SubagentRunID)
		assert.Equal(t, "run-0", got[5].(agentruntime.ToolResult).SubagentRunID)
		progress := got[6].(agentruntime.SubagentProgress)
		assert.Equal(t, "completed", progress.Info.Status)
		done := got[7].(agentruntime.SubagentDone)
		assert.Equal(t, "completed", done.Info.Status)
		assert.Equal(t, "parallel done", got[8].(agentruntime.ToolResult).Content)
		assert.IsType(t, agentruntime.Done{}, got[9])
	})

	t.Run("nonmatching name stays ordinary even with valid details", func(t *testing.T) {
		stream := &scriptedStream{events: []pkgpi.Event{
			{Kind: pkgpi.EventPreToolUse, Tool: pkgpi.ToolEvent{ID: "outer", Name: "delegate_task", Input: []byte(`{"task":"inspect","profile":"read-only"}`)}},
			{Kind: pkgpi.EventToolUseUpdate, Tool: pkgpi.ToolEvent{ID: "outer", PartialResult: []byte(`{"details":{"messages":[]}}`)}},
			{Kind: pkgpi.EventPostToolUse, Tool: pkgpi.ToolEvent{ID: "outer", Content: "raw", Details: []byte(`{"messages":[]}`), IsError: true}},
		}}
		got := drainForTest(t, stream)
		require.Len(t, got, 3)
		call := got[0].(agentruntime.ToolCall)
		assert.Nil(t, call.Canonical)
		result := got[1].(agentruntime.ToolResult)
		assert.Equal(t, "raw", result.Content)
		assert.True(t, result.IsError)
		assert.IsType(t, agentruntime.Done{}, got[2])
	})
}

func drainForTest(t *testing.T, stream *scriptedStream) []agentruntime.Event {
	t.Helper()
	return drainForTestWithActive(t, stream, nil)
}

func drainForTestWithActive(t *testing.T, stream *scriptedStream, active *activeSession) []agentruntime.Event {
	t.Helper()
	out := make(chan agentruntime.Event, 32)
	result := &agentruntime.RunResult{}
	drainStream(context.Background(), agentruntime.RunRequest{}, "", stream, out, result, active)
	close(out)
	var got []agentruntime.Event
	for event := range out {
		got = append(got, event)
	}
	return got
}

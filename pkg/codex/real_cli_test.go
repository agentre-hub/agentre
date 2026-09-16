//go:build codexcli

package codex

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRealCodexCLIStartResume(t *testing.T) {
	if os.Getenv("CODEX_REAL_CLI") != "1" {
		t.Skip("set CODEX_REAL_CLI=1 to run against the local codex CLI")
	}
	binary, err := exec.LookPath("codex")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	client := New(
		WithBinary(binary),
		WithCwd(t.TempDir()),
		WithSandbox(SandboxReadOnly),
		WithApproval(ApprovalNever),
		WithKillGrace(2*time.Second),
	)

	firstText, sourceThreadID := collectRealCodexTurn(t, ctx, client, "Reply exactly with: wrapperpong")
	assert.Equal(t, "wrapperpong", strings.TrimSpace(firstText))
	require.NotEmpty(t, sourceThreadID)

	secondText, resumedThreadID := collectRealCodexTurn(t, ctx, client, "Reply exactly with: wrapperpong2", Resume(sourceThreadID))
	assert.Equal(t, sourceThreadID, resumedThreadID)
	assert.Equal(t, "wrapperpong2", strings.TrimSpace(secondText))
}

func TestRealCodexCLIPlanModeEmitsPlanText(t *testing.T) {
	if os.Getenv("CODEX_REAL_CLI") != "1" {
		t.Skip("set CODEX_REAL_CLI=1 to run against the local codex CLI")
	}
	binary, err := exec.LookPath("codex")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	client := New(
		WithBinary(binary),
		WithCwd(t.TempDir()),
		WithSandbox(SandboxReadOnly),
		WithApproval(ApprovalNever),
		WithKillGrace(2*time.Second),
		WithConfig(`model_reasoning_effort="low"`),
	)

	stream, err := client.Stream(ctx,
		"Create a concise two step plan for inspecting this empty temp directory, then stop after the plan. Do not run shell commands.",
		RunCollaborationMode(CollaborationPlan),
	)
	require.NoError(t, err)

	var planText string
	for stream.Next() {
		ev := stream.Event()
		switch ev.Kind {
		case EventPlanUpdated:
			planText += ev.PlanText
		case EventError:
			require.NoError(t, ev.Err)
		}
	}
	require.NoError(t, stream.Close(ctx))
	trimmed := strings.TrimSpace(planText)
	require.NotEmpty(t, trimmed)
	assert.True(t, strings.Contains(trimmed, "\n") || strings.Contains(trimmed, "-"))
}

func TestRealCodexCLIPlanThenDefaultResumeExecutes(t *testing.T) {
	if os.Getenv("CODEX_REAL_CLI") != "1" {
		t.Skip("set CODEX_REAL_CLI=1 to run against the local codex CLI")
	}
	binary, err := exec.LookPath("codex")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client := New(
		WithBinary(binary),
		WithCwd(t.TempDir()),
		WithSandbox(SandboxReadOnly),
		WithApproval(ApprovalNever),
		WithKillGrace(2*time.Second),
		WithConfig(`model_reasoning_effort="low"`),
	)

	stream, err := client.Stream(ctx,
		"Create a one step plan to later reply exactly with PLAN_EXECUTION_PROBE, then stop after the plan. Do not output PLAN_EXECUTION_PROBE in this turn.",
		RunCollaborationMode(CollaborationPlan),
	)
	require.NoError(t, err)

	var planText string
	for stream.Next() {
		ev := stream.Event()
		switch ev.Kind {
		case EventPlanUpdated:
			planText += ev.PlanText
		case EventError:
			require.NoError(t, ev.Err)
		}
	}
	require.NoError(t, stream.Close(ctx))
	require.NotEmpty(t, strings.TrimSpace(planText))
	threadID := stream.SessionID()
	require.NotEmpty(t, threadID)

	executed, resumedThreadID := collectRealCodexTurn(t, ctx, client,
		"Implement the plan. Reply exactly with: PLAN_EXECUTION_PROBE",
		Resume(threadID),
		RunCollaborationMode(CollaborationDefault),
	)
	assert.Equal(t, threadID, resumedThreadID)
	assert.Equal(t, "PLAN_EXECUTION_PROBE", strings.TrimSpace(executed))
}

func TestRealCodexCLIGoalThenTurnStreamsText(t *testing.T) {
	if os.Getenv("CODEX_REAL_CLI") != "1" {
		t.Skip("set CODEX_REAL_CLI=1 to run against the local codex CLI")
	}
	binary, err := exec.LookPath("codex")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	client := New(
		WithBinary(binary),
		WithCwd(t.TempDir()),
		WithSandbox(SandboxReadOnly),
		WithApproval(ApprovalNever),
		WithKillGrace(2*time.Second),
		WithConfig(`model_reasoning_effort="low"`),
	)
	sess, err := client.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	objective := "Reply exactly with GOAL_STREAM_PROBE when asked to execute this goal."
	status := GoalStatusActive
	goal, err := sess.SetGoal(ctx, GoalUpdate{Objective: &objective, Status: &status})
	require.NoError(t, err)
	require.NotNil(t, goal)
	require.NotEmpty(t, sess.ID())

	stream, err := sess.Stream(ctx, "Execute the goal now. Reply exactly with: GOAL_STREAM_PROBE")
	require.NoError(t, err)
	var text strings.Builder
	var done bool
	for stream.Next() {
		ev := stream.Event()
		switch ev.Kind {
		case EventTextDelta:
			text.WriteString(ev.Text)
		case EventDone:
			done = true
		case EventError:
			require.NoError(t, ev.Err)
		}
	}
	require.NoError(t, stream.Err())
	assert.True(t, done)
	assert.Equal(t, "GOAL_STREAM_PROBE", strings.TrimSpace(text.String()))
}

func collectRealCodexTurn(t *testing.T, ctx context.Context, client *Client, prompt string, opts ...RunOption) (string, string) {
	t.Helper()

	stream, err := client.Stream(ctx, prompt, opts...)
	require.NoError(t, err)

	var text strings.Builder
	for stream.Next() {
		ev := stream.Event()
		switch ev.Kind {
		case EventTextDelta:
			text.WriteString(ev.Text)
		case EventError:
			require.NoError(t, ev.Err)
		}
	}
	require.NoError(t, stream.Close(ctx))
	return text.String(), stream.SessionID()
}

package fake

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

func TestRun_EnterWorktreeDirective_MovesCwdThenKeepsTurnRunning(t *testing.T) {
	t.Setenv("AGENTRE_E2E_FAKE_WORKTREE_HOLD_MS", "400")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, _, err := New().Run(ctx, agentruntime.RunRequest{
		SessionID: 71, UserText: "e2e-enter-worktree:/tmp/repo-wt",
	})
	require.NoError(t, err)

	call, ok := nextToolEvent(t, events).(agentruntime.ToolCall)
	require.True(t, ok, "first tool event must be the ToolCall")
	assert.Equal(t, "Bash", call.Name)
	assert.Equal(t, "/tmp/repo-wt", call.Cwd)
	res, ok := nextToolEvent(t, events).(agentruntime.ToolResult)
	require.True(t, ok, "second tool event must be the ToolResult")
	assert.Equal(t, call.ID, res.ToolCallID)
	assert.Equal(t, "/tmp/repo-wt", res.Cwd)

	// 进入 worktree 之后轮次仍在进行:保持窗口内不得收到 Done。
	start := time.Now()
	sawDone := false
	for ev := range events {
		if _, done := ev.(agentruntime.Done); done {
			sawDone = true
			assert.GreaterOrEqual(t, time.Since(start), 300*time.Millisecond, "turn must stay open after entering the worktree")
		}
	}
	assert.True(t, sawDone, "turn must still finish")
}

func TestRun_EnterWorktreeDirective_EmptyPathIsIgnored(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, _, err := New().Run(ctx, agentruntime.RunRequest{SessionID: 72, UserText: "e2e-enter-worktree:"})
	require.NoError(t, err)
	for ev := range events {
		switch ev.(type) {
		case agentruntime.ToolCall, agentruntime.ToolResult:
			t.Fatalf("empty path must not emit tool events, got %T", ev)
		}
	}
}

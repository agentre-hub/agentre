package ipc

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
)

// actionablePlanText 是测试探针认作「可操作 plan」的块文本;plan 块的真实形态属于宿主
// 的转录投影(PlanProbe 注释),状态机只关心探针对末条 assistant 的回答。
const actionablePlanText = "actionable plan"

type textPlanProbe struct{}

func (textPlanProbe) HasActionablePlan(bs []blocks.ContentBlock) bool {
	for _, b := range bs {
		if tb, ok := b.(blocks.TextBlock); ok && tb.Text == actionablePlanText {
			return true
		}
		if tb, ok := b.(*blocks.TextBlock); ok && tb != nil && tb.Text == actionablePlanText {
			return true
		}
	}
	return false
}

// fakeMessages 按 seq 升序持有一个会话的消息,记录点查末条 assistant 的次数。
// 端口上没有整条读的方法,「不读回整条转录」由编译期保证。
type fakeMessages struct {
	msgs        []*chat_entity.Message
	err         error
	latestCalls int
}

func (f *fakeMessages) LatestAssistant(context.Context, int64) (*chat_entity.Message, error) {
	f.latestCalls++
	if f.err != nil {
		return nil, f.err
	}
	for i := len(f.msgs) - 1; i >= 0; i-- {
		if f.msgs[i].Role == "assistant" {
			return f.msgs[i], nil
		}
	}
	return nil, nil
}

func textMessage(t *testing.T, seq int, role, text string) *chat_entity.Message {
	t.Helper()
	m := &chat_entity.Message{ID: int64(seq), SessionID: 100, Role: role, Seq: seq}
	require.NoError(t, m.SetBlocks([]blocks.ContentBlock{blocks.TextBlock{Text: text}}))
	return m
}

func newPlanWaitingController(messages MessagePort) *PermissionModeController {
	c := NewPermissionModeController(nil, textPlanProbe{}, func(_ context.Context, cause error) error { return cause })
	c.messages = messages
	return c
}

var (
	waitingCodexSession = &chat_entity.Session{ID: 100, AgentStatus: "waiting"}
	codexBackend        = &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex)}
)

func TestCanContinuePlanWaiting_JudgesTheLatestAssistant(t *testing.T) {
	tests := []struct {
		name string
		msgs func(t *testing.T) []*chat_entity.Message
		want bool
	}{
		{
			name: "Given the latest assistant carries an actionable plan, When checked, Then the waiting codex session can continue",
			msgs: func(t *testing.T) []*chat_entity.Message {
				return []*chat_entity.Message{
					textMessage(t, 1, "user", "plan it"),
					textMessage(t, 2, "assistant", actionablePlanText),
				}
			},
			want: true,
		},
		{
			name: "Given only an earlier assistant carries the plan, When checked, Then the latest assistant decides and it cannot continue",
			msgs: func(t *testing.T) []*chat_entity.Message {
				return []*chat_entity.Message{
					textMessage(t, 1, "assistant", actionablePlanText),
					textMessage(t, 2, "user", "anything else?"),
					textMessage(t, 3, "assistant", "just text"),
					textMessage(t, 4, "user", "trailing user message"),
				}
			},
			want: false,
		},
		{
			name: "Given the session has no assistant message, When checked, Then it cannot continue",
			msgs: func(t *testing.T) []*chat_entity.Message {
				return []*chat_entity.Message{textMessage(t, 1, "user", actionablePlanText)}
			},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newPlanWaitingController(&fakeMessages{msgs: tc.msgs(t)})
			got, err := c.CanContinuePlanWaiting(context.Background(), waitingCodexSession, codexBackend, true)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCanContinuePlanWaiting_ReadsOnlyTheLatestAssistant(t *testing.T) {
	messages := &fakeMessages{msgs: []*chat_entity.Message{
		textMessage(t, 1, "user", "plan it"),
		textMessage(t, 2, "assistant", actionablePlanText),
	}}
	c := newPlanWaitingController(messages)

	got, err := c.CanContinuePlanWaiting(context.Background(), waitingCodexSession, codexBackend, true)
	require.NoError(t, err)
	assert.True(t, got)
	assert.Equal(t, 1, messages.latestCalls, "只点查 seq 最大的那条 assistant")
}

func TestCanContinuePlanWaiting_Boundaries(t *testing.T) {
	t.Run("Given the transcript read fails, When checked, Then the failure is reported through the host wrapper", func(t *testing.T) {
		boom := errors.New("db down")
		c := newPlanWaitingController(&fakeMessages{err: boom})
		got, err := c.CanContinuePlanWaiting(context.Background(), waitingCodexSession, codexBackend, true)
		require.ErrorIs(t, err, boom)
		assert.False(t, got)
	})
	t.Run("Given the caller does not allow plan waiting, When checked, Then it answers false without reading the transcript", func(t *testing.T) {
		messages := &fakeMessages{}
		c := newPlanWaitingController(messages)
		got, err := c.CanContinuePlanWaiting(context.Background(), waitingCodexSession, codexBackend, false)
		require.NoError(t, err)
		assert.False(t, got)
		assert.Zero(t, messages.latestCalls)
	})
}

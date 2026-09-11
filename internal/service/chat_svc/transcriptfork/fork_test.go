package transcriptfork_test

import (
	"context"
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo/mock_transcript_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/transcriptfork"
)

var (
	codexBackend = &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex)}
	piBackend    = &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)}
)

func registerMessages(t *testing.T) *mock_transcript_repo.MockMessageRepo {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := mock_transcript_repo.NewMockMessageRepo(ctrl)
	prev := transcript_repo.Message()
	transcript_repo.RegisterMessage(repo)
	t.Cleanup(func() { transcript_repo.RegisterMessage(prev) })
	return repo
}

func message(t *testing.T, id int64, seq int, role string, bs ...blocks.ContentBlock) *chat_entity.Message {
	t.Helper()
	m := &chat_entity.Message{ID: id, SessionID: 100, Role: role, Seq: seq}
	require.NoError(t, m.SetBlocks(bs))
	return m
}

func metaOf(msgs []*chat_entity.Message) []*chat_entity.Message {
	out := make([]*chat_entity.Message, 0, len(msgs))
	for _, m := range msgs {
		meta := *m
		meta.BlocksJSON = ""
		out = append(out, &meta)
	}
	return out
}

// fillFrom 模拟 FillBlocks:按 ID 回到 fixture 取全部正文。
func fillFrom(source []*chat_entity.Message) func(context.Context, []*chat_entity.Message) error {
	return func(_ context.Context, msgs []*chat_entity.Message) error {
		for _, m := range msgs {
			for _, src := range source {
				if src.ID == m.ID {
					m.BlocksJSON = src.BlocksJSON
				}
			}
		}
		return nil
	}
}

// serveTranscript 让整条读(List)与窄读(ListMeta + FillBlocks)都按同一份 fixture 作答。
func serveTranscript(t *testing.T, msgs []*chat_entity.Message) {
	t.Helper()
	repo := registerMessages(t)
	repo.EXPECT().List(gomock.Any(), int64(100)).Return(msgs, nil).AnyTimes()
	repo.EXPECT().ListMeta(gomock.Any(), int64(100)).DoAndReturn(
		func(context.Context, int64) ([]*chat_entity.Message, error) { return metaOf(msgs), nil }).AnyTimes()
	repo.EXPECT().FillBlocks(gomock.Any(), gomock.Any()).DoAndReturn(fillFrom(msgs)).AnyTimes()
}

func requireCode(t *testing.T, err error, want int) {
	t.Helper()
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, want, httpErr.Code)
}

func codexTranscript(t *testing.T) []*chat_entity.Message {
	return []*chat_entity.Message{
		message(t, 1, 1, "user", blocks.TextBlock{Text: "first"}),
		message(t, 2, 2, "assistant", blocks.TextBlock{Text: "v1"}),
		message(t, 3, 3, "user", blocks.TextBlock{Text: "second"}),
		message(t, 4, 4, "assistant", blocks.TextBlock{Text: "v2"}),
	}
}

func TestBackendForkAnchor_CodexCountsUserTurnsFromTheAnchor(t *testing.T) {
	msgs := codexTranscript(t)
	tests := []struct {
		name     string
		userMsg  *chat_entity.Message
		want     string
		wantCode int
	}{
		{name: "Given the anchor is the first user turn of two, When the anchor is resolved, Then both turns roll back", userMsg: msgs[0], want: "2"},
		{name: "Given the anchor is the last user turn, When the anchor is resolved, Then one turn rolls back", userMsg: msgs[2], want: "1"},
		{
			name:     "Given no user turn exists at or after the anchor seq, When the anchor is resolved, Then it reports a missing user anchor",
			userMsg:  &chat_entity.Message{ID: 9, SessionID: 100, Role: "user", Seq: 5},
			wantCode: code.ChatRegenerateNoUserAnchor,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			serveTranscript(t, msgs)
			sess := &chat_entity.Session{ID: 100, ProviderSessionID: "cx-abc"}
			got, err := transcriptfork.BackendForkAnchor(context.Background(), sess, codexBackend, tc.userMsg)
			if tc.wantCode != 0 {
				requireCode(t, err, tc.wantCode)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestBackendForkAnchor_CodexReadsOnlyMessageMetadata(t *testing.T) {
	msgs := codexTranscript(t)
	repo := registerMessages(t)
	// 严格 mock:List / FillBlocks 都没有期望,数 user 轮次只需要元数据。
	repo.EXPECT().ListMeta(gomock.Any(), int64(100)).Return(metaOf(msgs), nil).Times(1)

	sess := &chat_entity.Session{ID: 100, ProviderSessionID: "cx-abc"}
	got, err := transcriptfork.BackendForkAnchor(context.Background(), sess, codexBackend, msgs[0])
	require.NoError(t, err)
	assert.Equal(t, "2", got)
}

func TestBackendForkAnchor_PiFailedFirstTurnCondition(t *testing.T) {
	failedAssistant := func(t *testing.T, bs ...blocks.ContentBlock) *chat_entity.Message {
		m := message(t, 1001, 2, "assistant", bs...)
		m.ErrorText = "startup failed"
		return m
	}
	tests := []struct {
		name     string
		msgs     func(t *testing.T) []*chat_entity.Message
		userMsg  func(msgs []*chat_entity.Message) *chat_entity.Message
		wantCode int
	}{
		{
			name: "Given a first user turn whose only assistant failed without output, When the anchor is resolved, Then it retries without a fork",
			msgs: func(t *testing.T) []*chat_entity.Message {
				return []*chat_entity.Message{message(t, 1000, 1, "user", blocks.TextBlock{Text: "retry me"}), failedAssistant(t)}
			},
		},
		{
			name: "Given the failed assistant already produced blocks, When the anchor is resolved, Then the lost provider session fails closed",
			msgs: func(t *testing.T) []*chat_entity.Message {
				return []*chat_entity.Message{
					message(t, 1000, 1, "user", blocks.TextBlock{Text: "retry me"}),
					failedAssistant(t, blocks.TextBlock{Text: "partial answer"}),
				}
			},
			wantCode: code.ChatProviderSessionGone,
		},
		{
			name: "Given the assistant has no error, When the anchor is resolved, Then the lost provider session fails closed",
			msgs: func(t *testing.T) []*chat_entity.Message {
				return []*chat_entity.Message{
					message(t, 1000, 1, "user", blocks.TextBlock{Text: "retry me"}),
					message(t, 1001, 2, "assistant"),
				}
			},
			wantCode: code.ChatProviderSessionGone,
		},
		{
			name: "Given the session holds more than the first turn, When the anchor is resolved, Then the lost provider session fails closed",
			msgs: func(t *testing.T) []*chat_entity.Message {
				return []*chat_entity.Message{
					message(t, 1000, 1, "user", blocks.TextBlock{Text: "retry me"}),
					failedAssistant(t),
					message(t, 1002, 3, "user", blocks.TextBlock{Text: "again"}),
				}
			},
			wantCode: code.ChatProviderSessionGone,
		},
		{
			name: "Given the anchor is not the session's first user message, When the anchor is resolved, Then the lost provider session fails closed",
			msgs: func(t *testing.T) []*chat_entity.Message {
				return []*chat_entity.Message{message(t, 1000, 1, "user", blocks.TextBlock{Text: "retry me"}), failedAssistant(t)}
			},
			userMsg: func([]*chat_entity.Message) *chat_entity.Message {
				return &chat_entity.Message{ID: 999, Role: "user", Seq: 1}
			},
			wantCode: code.ChatProviderSessionGone,
		},
		{
			name: "Given the failed assistant body is malformed, When the anchor is resolved, Then it reports malformed blocks",
			msgs: func(t *testing.T) []*chat_entity.Message {
				bad := &chat_entity.Message{ID: 1001, SessionID: 100, Role: "assistant", Seq: 2, BlocksJSON: "{", ErrorText: "startup failed"}
				return []*chat_entity.Message{message(t, 1000, 1, "user", blocks.TextBlock{Text: "retry me"}), bad}
			},
			wantCode: code.ChatBlocksMalformed,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msgs := tc.msgs(t)
			serveTranscript(t, msgs)
			userMsg := msgs[0]
			if tc.userMsg != nil {
				userMsg = tc.userMsg(msgs)
			}
			got, err := transcriptfork.BackendForkAnchor(context.Background(), &chat_entity.Session{ID: 100}, piBackend, userMsg)
			if tc.wantCode != 0 {
				requireCode(t, err, tc.wantCode)
				return
			}
			require.NoError(t, err)
			assert.Empty(t, got)
		})
	}
}

func TestBackendForkAnchor_PiReadsOnlyTheFailedAssistantBody(t *testing.T) {
	t.Run("Given exactly the first turn, When the anchor is resolved, Then only the assistant's body is filled", func(t *testing.T) {
		user := message(t, 1000, 1, "user", blocks.TextBlock{Text: "retry me"})
		assistant := message(t, 1001, 2, "assistant")
		assistant.ErrorText = "startup failed"
		msgs := []*chat_entity.Message{user, assistant}
		repo := registerMessages(t)
		// 严格 mock:List 没有期望;正文只补那一条失败的 assistant。
		repo.EXPECT().ListMeta(gomock.Any(), int64(100)).Return(metaOf(msgs), nil).Times(1)
		repo.EXPECT().FillBlocks(gomock.Any(), gomock.Len(1)).DoAndReturn(
			func(ctx context.Context, got []*chat_entity.Message) error {
				require.Equal(t, int64(1001), got[0].ID, "只给失败的 assistant 补正文")
				return fillFrom(msgs)(ctx, got)
			}).Times(1)

		got, err := transcriptfork.BackendForkAnchor(context.Background(), &chat_entity.Session{ID: 100}, piBackend, user)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
	t.Run("Given more than two messages, When the anchor is resolved, Then no body is read at all", func(t *testing.T) {
		msgs := []*chat_entity.Message{
			message(t, 1000, 1, "user", blocks.TextBlock{Text: "retry me"}),
			message(t, 1001, 2, "assistant"),
			message(t, 1002, 3, "user", blocks.TextBlock{Text: "again"}),
		}
		repo := registerMessages(t)
		repo.EXPECT().ListMeta(gomock.Any(), int64(100)).Return(metaOf(msgs), nil).Times(1)

		_, err := transcriptfork.BackendForkAnchor(context.Background(), &chat_entity.Session{ID: 100}, piBackend, msgs[0])
		requireCode(t, err, code.ChatProviderSessionGone)
	})
}

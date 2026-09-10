package chat_svc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
)

// fakeAskRunner 是测试用的 Runtime + AskAnswerSink 组合体;记录 SubmitAnswer 调用。
// Run 是 zero-impl —— AnswerUserQuestion 测试只走断言路径,不真的发起 turn。
type fakeAskRunner struct {
	gotSession int64
	gotReqID   string
	gotAnswers []agentruntime.AskAnswer
	gotSkipped bool
	err        error
	calls      int
}

func (f *fakeAskRunner) Capabilities() capability.Capabilities { return capability.Capabilities{} }

func (f *fakeAskRunner) Run(_ context.Context, _ agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	ch := make(chan agentruntime.Event)
	close(ch)
	return ch, &agentruntime.RunResult{}, nil
}

func (f *fakeAskRunner) SubmitAnswer(_ context.Context, sessionID int64, requestID string, _ []agentruntime.AskQuestion, answers []agentruntime.AskAnswer, skipped bool) error {
	f.calls++
	f.gotSession = sessionID
	f.gotReqID = requestID
	f.gotAnswers = answers
	f.gotSkipped = skipped
	return f.err
}

func expectAskBackend(
	m *chatMocks,
	sessionID int64,
	agentID int64,
	backendID int64,
	backendType agent_backend_entity.BackendType,
) {
	m.session.EXPECT().Find(m.ctx, sessionID).Return(&chat_entity.Session{
		ID: sessionID, AgentID: agentID, Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(m.ctx, agentID).Return(&agent_entity.Agent{
		ID: agentID, AgentBackendID: backendID, Status: consts.ACTIVE,
	}, nil)
	m.backend.EXPECT().Find(m.ctx, backendID).Return(&agent_backend_entity.AgentBackend{
		ID: backendID, Type: string(backendType), Status: consts.ACTIVE,
	}, nil)
}

func answerAndAssertSubmitted(
	t *testing.T,
	m *chatMocks,
	fake *fakeAskRunner,
	backendType agent_backend_entity.BackendType,
	sessionID int64,
	agentID int64,
	backendID int64,
	requestID string,
	labels []string,
) {
	t.Helper()
	restore := agentruntime.SwapRuntimeForTest(backendType, fake)
	defer restore()
	expectAskBackend(m, sessionID, agentID, backendID, backendType)

	resp, err := m.svc.AnswerUserQuestion(m.ctx, &chat_svc.AnswerUserQuestionRequest{
		SessionID: sessionID,
		RequestID: requestID,
		Answers: []blocks.AskAnswerDTO{
			{QuestionIndex: 0, Labels: labels},
		},
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, 1, fake.calls)
	assert.Equal(t, sessionID, fake.gotSession)
	assert.Equal(t, requestID, fake.gotReqID)
	assert.False(t, fake.gotSkipped)
	assert.Len(t, fake.gotAnswers, 1)
	assert.Equal(t, labels, fake.gotAnswers[0].Labels)
}

func TestAnswerUserQuestion(t *testing.T) {
	convey.Convey("AnswerUserQuestion", t, func() {
		m := setupChatTest(t)

		convey.Convey("happy path 投递答案给 backend AskAnswerSink", func() {
			fake := &fakeAskRunner{}
			answerAndAssertSubmitted(t, m, fake, agent_backend_entity.TypeClaudeCode, 42, 7, 12, "req-001", []string{"last_read_at int64"})
		})

		convey.Convey("codex backend 也通过同一 AskAnswerSink 投递答案", func() {
			fake := &fakeAskRunner{}
			answerAndAssertSubmitted(t, m, fake, agent_backend_entity.TypeCodex, 43, 8, 13, "ask-001", []string{"backend"})
		})

		// 会话已经钉住某一档时(R15b / 决策36),正在等待应答的这一轮已经跑在那一档
		// 上——答案必须投回同一个 backend/runner,不能按 Agent 当前主档重新解析
		// (那可能已经被改成别的 backend,导致投递方向与实际运行的进程对不上)。
		convey.Convey("会话钉住某一档时答案投回那一档,不是 Agent 当前主档", func() {
			fake := &fakeAskRunner{}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, fake)
			defer restore()

			m.session.EXPECT().Find(m.ctx, int64(44)).Return(&chat_entity.Session{
				ID: 44, AgentID: 9, Status: consts.ACTIVE, ExecAgentBackendID: 15,
			}, nil)
			m.agent.EXPECT().Find(m.ctx, int64(9)).Return(&agent_entity.Agent{
				ID: 9, AgentBackendID: 14, Status: consts.ACTIVE,
			}, nil)
			m.backend.EXPECT().Find(m.ctx, int64(15)).Return(&agent_backend_entity.AgentBackend{
				ID: 15, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
			}, nil)

			resp, err := m.svc.AnswerUserQuestion(m.ctx, &chat_svc.AnswerUserQuestionRequest{
				SessionID: 44,
				RequestID: "req-pinned",
				Answers:   []blocks.AskAnswerDTO{{QuestionIndex: 0, Labels: []string{"x"}}},
			})

			assert.NoError(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, 1, fake.calls, "答案必须投给钉住的档(15),不能因为没解析到就静默丢弃")
		})

		convey.Convey("skipped 路径 Answers 可空", func() {
			fake := &fakeAskRunner{}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, fake)
			defer restore()

			expectAskBackend(m, 42, 7, 12, agent_backend_entity.TypeClaudeCode)

			_, err := m.svc.AnswerUserQuestion(m.ctx, &chat_svc.AnswerUserQuestionRequest{
				SessionID: 42,
				RequestID: "req-skip",
				Skipped:   true,
			})

			assert.NoError(t, err)
			assert.True(t, fake.gotSkipped)
			assert.Equal(t, "req-skip", fake.gotReqID)
		})

		convey.Convey("空 sessionID 或 requestID 返 InvalidParameter，不调 sink", func() {
			fake := &fakeAskRunner{}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, fake)
			defer restore()

			_, err := m.svc.AnswerUserQuestion(m.ctx, &chat_svc.AnswerUserQuestionRequest{
				SessionID: 0, RequestID: "x",
				Answers: []blocks.AskAnswerDTO{{QuestionIndex: 0, Labels: []string{"a"}}},
			})
			assert.Error(t, err)
			assert.Equal(t, 0, fake.calls)

			_, err = m.svc.AnswerUserQuestion(m.ctx, &chat_svc.AnswerUserQuestionRequest{
				SessionID: 1, RequestID: "",
				Answers: []blocks.AskAnswerDTO{{QuestionIndex: 0, Labels: []string{"a"}}},
			})
			assert.Error(t, err)
			assert.Equal(t, 0, fake.calls)
		})

		convey.Convey("非 skipped 但 answers 空 → InvalidParameter", func() {
			fake := &fakeAskRunner{}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, fake)
			defer restore()

			_, err := m.svc.AnswerUserQuestion(m.ctx, &chat_svc.AnswerUserQuestionRequest{
				SessionID: 42, RequestID: "req-x",
				Skipped: false,
				Answers: nil,
			})
			assert.Error(t, err)
			assert.Equal(t, 0, fake.calls)
		})

		convey.Convey("sink SubmitAnswer 失败错误透传", func() {
			fake := &fakeAskRunner{err: errors.New("waiter missing")}
			restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, fake)
			defer restore()

			expectAskBackend(m, 42, 7, 12, agent_backend_entity.TypeClaudeCode)

			_, err := m.svc.AnswerUserQuestion(m.ctx, &chat_svc.AnswerUserQuestionRequest{
				SessionID: 42, RequestID: "req-001",
				Answers: []blocks.AskAnswerDTO{{QuestionIndex: 0, Labels: []string{"x"}}},
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "waiter missing")
		})
	})
}

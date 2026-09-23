package openclaw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/openclawgateway"
)

// OpenClaw question protocol (packages/gateway-protocol schema/questions.ts,
// src/gateway/server-methods/question.ts and question-manager.ts in OpenClaw
// 2026.9.5). Every method and both broadcasts require operator.questions.
const (
	questionResolveMethod = "question.resolve"
	questionGetMethod     = "question.get"
	questionListMethod    = "question.list"

	questionStatusPending  = "pending"
	questionStatusAnswered = "answered"
	questionStatusExpired  = "expired"

	questionReasonNotFound        = "QUESTION_NOT_FOUND"
	questionReasonAlreadyTerminal = "QUESTION_ALREADY_TERMINAL"
)

// gatewayQuestion is one canonical question of a QuestionRecord. url and the
// secretStore binding are not shown: the card has no place for them and the
// Gateway handles a store-bound secret answer on its own.
type gatewayQuestion struct {
	QuestionID string `json:"questionId"`
	Header     string `json:"header"`
	Question   string `json:"question"`
	Options    []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
	MultiSelect bool `json:"multiSelect"`
	IsOther     bool `json:"isOther"`
	IsSecret    bool `json:"isSecret"`
}

// gatewayQuestionAnswers is the QuestionAnswers shape: one string array per
// questionId, used both to answer and in answered records/events.
type gatewayQuestionAnswers struct {
	Answers map[string][]string `json:"answers"`
}

// gatewayQuestionRecord is the question.requested payload, a question.list
// entry and the question.get result's record.
type gatewayQuestionRecord struct {
	ID         string                  `json:"id"`
	Questions  []gatewayQuestion       `json:"questions"`
	SessionKey string                  `json:"sessionKey"`
	Status     string                  `json:"status"`
	Answers    *gatewayQuestionAnswers `json:"answers"`
}

// questionState is the question counterpart to approvalState: questions is the
// card as shown (also the waiter cache SubmitAnswer reads when the caller
// passes nil), terminal marks that the card already resolved.
type questionState struct {
	questions []agentruntime.AskQuestion
	terminal  bool
}

// SubmitAnswer answers a pending OpenClaw question card of the session's
// in-flight turn. It implements agentruntime.AskAnswerSink; questions may be
// nil, since the runtime cached them when the question arrived.
func (r *Runtime) SubmitAnswer(ctx context.Context, sessionID int64, requestID string, questions []agentruntime.AskQuestion, answers []agentruntime.AskAnswer, skipped bool) error {
	r.mu.RLock()
	active := r.active[sessionID]
	r.mu.RUnlock()
	if active == nil {
		return agentruntime.ErrNoActiveTurn
	}
	return active.resolveQuestion(ctx, requestID, questions, answers, skipped)
}

// askQuestionsFromGateway maps the Gateway questions onto the backend-neutral
// card. OpenClaw decides free-form Other per question with isOther.
func askQuestionsFromGateway(questions []gatewayQuestion) ([]agentruntime.AskQuestion, bool) {
	if len(questions) == 0 {
		return nil, false
	}
	out := make([]agentruntime.AskQuestion, 0, len(questions))
	for _, q := range questions {
		if strings.TrimSpace(q.QuestionID) == "" || strings.TrimSpace(q.Question) == "" {
			return nil, false
		}
		var options []agentruntime.AskOption
		for _, option := range q.Options {
			options = append(options, agentruntime.AskOption{Label: option.Label, Description: option.Description})
		}
		out = append(out, agentruntime.AskQuestion{
			ID: q.QuestionID, Question: q.Question, Header: q.Header,
			MultiSelect: q.MultiSelect, IsOther: q.IsOther, IsSecret: q.IsSecret,
			DisallowOther: !q.IsOther, Options: options,
		})
	}
	return out, true
}

// handleQuestionRecord turns a pending question of this session into a card.
// It returns whether the record belongs to this turn (for reconcile's
// visibility set). A question without a session key is never claimed: it may
// belong to any session on the Gateway.
func (a *activeTurn) handleQuestionRecord(record gatewayQuestionRecord) bool {
	id := strings.TrimSpace(record.ID)
	sessionKey := strings.TrimSpace(record.SessionKey)
	if id == "" || sessionKey == "" || !a.matchesSession(sessionKey) {
		return false
	}
	if status := strings.TrimSpace(record.Status); status != "" && status != questionStatusPending {
		return false
	}
	questions, ok := askQuestionsFromGateway(record.Questions)
	if !ok {
		return false
	}
	a.questionMu.Lock()
	if _, seen := a.questions[id]; seen {
		a.questionMu.Unlock()
		return true
	}
	a.questions[id] = &questionState{questions: questions}
	a.questionMu.Unlock()
	logger.Ctx(a.ctx).Info("openclaw.activeTurn.handleQuestionRecord: question requested",
		zap.Int64("sessionId", a.sessionID), zap.String("questionId", id), zap.Int("questionCount", len(questions)))
	a.emit(agentruntime.UserAskRequest{RequestID: id, Questions: questions})
	return true
}

func (a *activeTurn) handleQuestionRequested(raw json.RawMessage) {
	var record gatewayQuestionRecord
	if json.Unmarshal(raw, &record) == nil {
		a.handleQuestionRecord(record)
	}
}

// handleQuestionResolved converges a known card on the Gateway's terminal: an
// answer from anywhere shows answered (secret content stripped), a withdrawal
// or expiry shows skipped. The echo of our own resolve finds the card already
// terminal and is a no-op.
func (a *activeTurn) handleQuestionResolved(raw json.RawMessage) {
	var payload struct {
		ID      string                  `json:"id"`
		Status  string                  `json:"status"`
		Answers *gatewayQuestionAnswers `json:"answers"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return
	}
	a.convergeQuestion(strings.TrimSpace(payload.ID), payload.Status, payload.Answers)
}

// convergeQuestion applies a Gateway status to a known card; pending or an
// unknown status leaves it actionable.
func (a *activeTurn) convergeQuestion(id, status string, answers *gatewayQuestionAnswers) {
	switch strings.TrimSpace(status) {
	case questionStatusAnswered:
		a.questionMu.Lock()
		state := a.questions[id]
		a.questionMu.Unlock()
		if state == nil {
			return
		}
		a.markQuestionTerminal(id, agentruntime.UserAskResolved{
			Answers: agentruntime.RedactSecretAnswers(state.questions, askAnswersFromGateway(state.questions, answers)),
		})
	case gatewayCancelledStatus, questionStatusExpired:
		a.markQuestionTerminal(id, agentruntime.UserAskResolved{Skipped: true})
	}
}

// askAnswersFromGateway maps Gateway answer arrays back onto card answers: a
// value naming an option is that option, anything else is the Other text.
func askAnswersFromGateway(questions []agentruntime.AskQuestion, answers *gatewayQuestionAnswers) []agentruntime.AskAnswer {
	if answers == nil {
		return nil
	}
	var out []agentruntime.AskAnswer
	for index, question := range questions {
		values, ok := answers.Answers[question.ID]
		if !ok {
			continue
		}
		answer := agentruntime.AskAnswer{QuestionIndex: index}
		var other []string
		for _, value := range values {
			if slices.ContainsFunc(question.Options, func(option agentruntime.AskOption) bool { return option.Label == value }) {
				answer.Labels = append(answer.Labels, value)
			} else {
				other = append(other, value)
			}
		}
		if len(other) > 0 {
			answer.Labels = append(answer.Labels, agentruntime.OtherAnswerLabel)
			answer.OtherText = strings.Join(other, ", ")
		}
		out = append(out, answer)
	}
	return out
}

// resolveQuestion answers or cancels a pending card. Resolutions are
// serialized so a repeated or racing call reaches the Gateway at most once.
func (a *activeTurn) resolveQuestion(ctx context.Context, requestID string, questions []agentruntime.AskQuestion, answers []agentruntime.AskAnswer, skipped bool) error {
	requestID = strings.TrimSpace(requestID)
	a.questionResolveMu.Lock()
	defer a.questionResolveMu.Unlock()

	a.questionMu.Lock()
	state := a.questions[requestID]
	if state == nil || state.terminal {
		a.questionMu.Unlock()
		return agentruntime.ErrWaiterNotFound
	}
	stored := state.questions
	a.questionMu.Unlock()
	if len(questions) > 0 && len(questions) != len(stored) {
		return fmt.Errorf("openclaw question: client supplied %d questions but the request recorded %d", len(questions), len(stored))
	}

	params := map[string]any{"id": requestID, "cancel": true}
	if !skipped {
		byQuestion, err := questionAnswerArrays(stored, answers)
		if err != nil {
			return err
		}
		params = map[string]any{"id": requestID, "answers": gatewayQuestionAnswers{Answers: byQuestion}}
	}
	var response struct {
		Status string `json:"status"`
	}
	if err := a.client.Call(ctx, questionResolveMethod, params, &response); err != nil {
		var rpcErr *openclawgateway.RPCError
		if errors.As(err, &rpcErr) && (rpcErr.Reason == questionReasonAlreadyTerminal || rpcErr.Reason == questionReasonNotFound) {
			// The Gateway already closed it (expired, withdrawn, answered
			// elsewhere): show what really happened instead of an error.
			a.refreshQuestionFromGateway(ctx, requestID)
			return nil
		}
		logger.Ctx(ctx).Warn("openclaw.activeTurn.resolveQuestion: question.resolve failed",
			zap.Int64("sessionId", a.sessionID), zap.String("questionId", requestID), zap.Bool("skipped", skipped), zap.Error(err))
		return err
	}
	logger.Ctx(ctx).Info("openclaw.activeTurn.resolveQuestion: question resolved",
		zap.Int64("sessionId", a.sessionID), zap.String("questionId", requestID), zap.Bool("skipped", skipped))
	if skipped {
		a.markQuestionTerminal(requestID, agentruntime.UserAskResolved{Skipped: true})
		return nil
	}
	// The raw answers went to the Gateway only; the event (and so the
	// transcript, persistence, sync and every other client) sees a secret
	// answer as answered without content.
	a.markQuestionTerminal(requestID, agentruntime.UserAskResolved{Answers: agentruntime.RedactSecretAnswers(stored, answers)})
	return nil
}

// questionAnswerArrays builds the QuestionAnswers map: one string array per
// questionId, Other replaced by its text. The Gateway requires an answer for
// every question, so a missing one is an error here rather than a rejected
// RPC. Secret values are sent verbatim (the Gateway does not trim them).
func questionAnswerArrays(questions []agentruntime.AskQuestion, answers []agentruntime.AskAnswer) (map[string][]string, error) {
	byIndex := make(map[int]agentruntime.AskAnswer, len(answers))
	for _, answer := range answers {
		if answer.QuestionIndex < 0 || answer.QuestionIndex >= len(questions) {
			return nil, fmt.Errorf("openclaw question: answer question index %d out of range (have %d questions)", answer.QuestionIndex, len(questions))
		}
		byIndex[answer.QuestionIndex] = answer
	}
	out := make(map[string][]string, len(questions))
	for index, question := range questions {
		answer, ok := byIndex[index]
		if !ok || len(answer.Labels) == 0 {
			return nil, fmt.Errorf("openclaw question: question %d has no answer", index)
		}
		values := make([]string, 0, len(answer.Labels))
		for _, label := range answer.Labels {
			value := label
			if label == agentruntime.OtherAnswerLabel {
				value = answer.OtherText
				if !question.IsSecret {
					value = strings.TrimSpace(value)
				}
				if value == "" {
					return nil, fmt.Errorf("openclaw question: question %d picked %q with empty text", index, agentruntime.OtherAnswerLabel)
				}
			}
			if !slices.Contains(values, value) {
				values = append(values, value)
			}
		}
		out[question.ID] = values
	}
	return out, nil
}

// refreshQuestionFromGateway asks question.get for a card's real state. Only a
// definite answer converges it: NOT_FOUND means the Gateway dropped it
// (skipped); any other failure leaves it actionable.
func (a *activeTurn) refreshQuestionFromGateway(ctx context.Context, id string) {
	var response struct {
		Question gatewayQuestionRecord `json:"question"`
	}
	if err := a.client.Call(ctx, questionGetMethod, map[string]any{"id": id}, &response); err != nil {
		var rpcErr *openclawgateway.RPCError
		if errors.As(err, &rpcErr) && rpcErr.Reason == questionReasonNotFound {
			a.markQuestionTerminal(id, agentruntime.UserAskResolved{Skipped: true})
		}
		return
	}
	a.convergeQuestion(id, response.Question.Status, response.Question.Answers)
}

// reconcileQuestions restores questions raised while offline and converges
// known pending cards the Gateway no longer lists (question.list returns only
// pending records).
func (a *activeTurn) reconcileQuestions() {
	if !a.questionListSupported || a.finished() {
		return
	}
	var response struct {
		Questions []gatewayQuestionRecord `json:"questions"`
	}
	if err := a.client.Call(a.ctx, questionListMethod, map[string]any{}, &response); err != nil {
		return
	}
	visible := make(map[string]struct{}, len(response.Questions))
	for _, record := range response.Questions {
		if a.handleQuestionRecord(record) {
			visible[strings.TrimSpace(record.ID)] = struct{}{}
		}
	}
	a.questionMu.Lock()
	missing := make([]string, 0)
	for id, state := range a.questions {
		if _, ok := visible[id]; !ok && !state.terminal {
			missing = append(missing, id)
		}
	}
	a.questionMu.Unlock()
	slices.Sort(missing)
	for _, id := range missing {
		a.refreshQuestionFromGateway(a.ctx, id)
	}
}

// markQuestionTerminal records a card's first terminal state and emits it once.
func (a *activeTurn) markQuestionTerminal(id string, event agentruntime.UserAskResolved) {
	a.questionMu.Lock()
	state := a.questions[id]
	if state == nil || state.terminal {
		a.questionMu.Unlock()
		return
	}
	state.terminal = true
	a.questionMu.Unlock()
	event.RequestID = id
	a.emit(event)
}

// expirePendingQuestions resolves every unanswered card as skipped: the turn
// is ending and nothing will answer them now.
func (a *activeTurn) expirePendingQuestions() {
	a.questionMu.Lock()
	pending := make([]string, 0)
	for id, state := range a.questions {
		if !state.terminal {
			pending = append(pending, id)
		}
	}
	a.questionMu.Unlock()
	slices.Sort(pending)
	for _, id := range pending {
		a.markQuestionTerminal(id, agentruntime.UserAskResolved{Skipped: true})
	}
}

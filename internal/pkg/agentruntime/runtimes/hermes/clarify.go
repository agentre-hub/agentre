package hermes

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
)

// serverRequestClarify is the `clarify` server->client request method, the
// question counterpart to `approval` (see hermesChoices / approvalParams in
// approvals.go). Hermes sends two wire shapes under the same method: a single
// question (`question` set) or a batch (`questions` set). `answers` only
// appears on a reconnect replay, which Hermes does not resume here (out of
// scope: session.events.since), so it is never decoded.
const serverRequestClarify = "clarify"

// clarifyBatchQuestion is one question inside a batch `clarify` request.
type clarifyBatchQuestion struct {
	QID         string   `json:"qid"`
	Question    string   `json:"question"`
	Choices     []string `json:"choices"`
	MultiSelect bool     `json:"multi_select"`
}

// clarifyParams is the `clarify` server request's params, covering both the
// single-question and batch shapes.
type clarifyParams struct {
	Question    string                 `json:"question"`
	Choices     []string               `json:"choices"`
	MultiSelect bool                   `json:"multi_select"`
	Questions   []clarifyBatchQuestion `json:"questions"`
}

// askState is the clarify counterpart to approvalState: isBatch remembers
// which wire shape must be replied with, questions is the normalized card
// Hermes agreed to show, and terminal marks whether an answer already went
// out (answered, withdrawn, or expired at turn end) so a second resolution
// never reaches Hermes.
type askState struct {
	isBatch   bool
	questions []agentruntime.AskQuestion
	terminal  bool
}

// toAskQuestions converts the wire params into the backend-neutral question
// list. ok=false means the params cannot be turned into a card and the
// request must be declined, mirroring approvalParams.allowedDecisions()
// returning empty.
func (p clarifyParams) toAskQuestions() (questions []agentruntime.AskQuestion, isBatch bool, ok bool) {
	if len(p.Questions) > 0 {
		out := make([]agentruntime.AskQuestion, 0, len(p.Questions))
		for _, q := range p.Questions {
			question := strings.TrimSpace(q.Question)
			qid := strings.TrimSpace(q.QID)
			if question == "" || qid == "" {
				return nil, false, false
			}
			out = append(out, agentruntime.AskQuestion{
				ID:            qid,
				Question:      question,
				MultiSelect:   q.MultiSelect,
				DisallowOther: len(q.Choices) > 0,
				Options:       askOptionsFromChoices(q.Choices),
			})
		}
		return out, true, true
	}
	question := strings.TrimSpace(p.Question)
	if question == "" {
		return nil, false, false
	}
	return []agentruntime.AskQuestion{{
		Question:      question,
		MultiSelect:   p.MultiSelect,
		DisallowOther: len(p.Choices) > 0,
		Options:       askOptionsFromChoices(p.Choices),
	}}, false, true
}

// askOptionsFromChoices maps Hermes' plain-string choices onto AskOption.
// Hermes choices carry no separate description/preview.
func askOptionsFromChoices(choices []string) []agentruntime.AskOption {
	if len(choices) == 0 {
		return nil
	}
	out := make([]agentruntime.AskOption, 0, len(choices))
	for _, c := range choices {
		out = append(out, agentruntime.AskOption{Label: c})
	}
	return out
}

// handleClarifyRequest turns a `clarify` server->client request into a
// question card. Malformed params are declined the same way an unusable
// approval is (see handleApprovalRequest in approvals.go).
func (a *activeTurn) handleClarifyRequest(ctx context.Context, req *ServerRequest) {
	var params clarifyParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		a.reject(ctx, req)
		return
	}
	questions, isBatch, ok := params.toAskQuestions()
	if !ok {
		a.reject(ctx, req)
		return
	}
	a.askMu.Lock()
	if _, seen := a.asks[req.ID]; seen {
		a.askMu.Unlock()
		return
	}
	a.asks[req.ID] = &askState{isBatch: isBatch, questions: questions}
	a.askMu.Unlock()
	logger.Ctx(ctx).Info("hermes.Runtime: clarify requested",
		zap.Int64("sessionID", a.sessionID), zap.String("requestID", req.ID),
		zap.Bool("batch", isBatch), zap.Int("questionCount", len(questions)))
	a.emit(agentruntime.UserAskRequest{RequestID: req.ID, Questions: questions})
}

// resolveAsk answers a pending clarify card, implementing the write half of
// agentruntime.AskAnswerSink for this turn. Answers are serialized under
// askResolveMu so a repeated or racing call converges on the request's first
// terminal state and Hermes is written to at most once per id.
func (a *activeTurn) resolveAsk(ctx context.Context, requestID string, questions []agentruntime.AskQuestion, answers []agentruntime.AskAnswer, skipped bool) error {
	requestID = strings.TrimSpace(requestID)
	a.askResolveMu.Lock()
	defer a.askResolveMu.Unlock()

	a.askMu.Lock()
	state := a.asks[requestID]
	if state == nil || state.terminal {
		a.askMu.Unlock()
		return agentruntime.ErrWaiterNotFound
	}
	stored := state.questions
	isBatch := state.isBatch
	a.askMu.Unlock()
	// The caller may pass nil questions (chat_svc's normal path: the runtime
	// already cached them when it became the waiter). When it does pass them,
	// only the count is checked, matching runtimes/codex's SubmitAnswer.
	if len(questions) > 0 && len(questions) != len(stored) {
		return fmt.Errorf("hermes clarify: client supplied %d questions but the request recorded %d", len(questions), len(stored))
	}

	payload, err := buildClarifyAnswer(stored, isBatch, answers, skipped)
	if err != nil {
		return err
	}
	if err := a.sess.RespondServerRequest(requestID, payload); err != nil {
		if errors.Is(err, errSessionClosed) || errors.Is(err, errServerRequestNotOpen) {
			// The connection (and so the request) is gone: nothing can answer it now.
			a.markAskTerminal(requestID, agentruntime.UserAskResolved{Skipped: true})
			return nil
		}
		logger.Ctx(ctx).Warn("hermes.Runtime: answer clarify failed",
			zap.Int64("sessionID", a.sessionID), zap.String("requestID", requestID), zap.Error(err))
		return err
	}
	logger.Ctx(ctx).Info("hermes.Runtime: clarify answered",
		zap.Int64("sessionID", a.sessionID), zap.String("requestID", requestID), zap.Bool("skipped", skipped))
	a.markAskTerminal(requestID, agentruntime.UserAskResolved{Answers: answers, Skipped: skipped})
	return nil
}

// markAskTerminal records the first terminal state of a known card and emits
// it once; later calls (a race, a late withdrawal) are no-ops.
func (a *activeTurn) markAskTerminal(id string, ev agentruntime.UserAskResolved) {
	a.askMu.Lock()
	state := a.asks[id]
	if state == nil || state.terminal {
		a.askMu.Unlock()
		return
	}
	state.terminal = true
	a.askMu.Unlock()
	ev.RequestID = id
	a.emit(ev)
}

// expirePendingAsks converges every unanswered clarify card as skipped; the
// turn is ending and nothing will answer them now.
func (a *activeTurn) expirePendingAsks() {
	a.askMu.Lock()
	pending := make([]string, 0, len(a.asks))
	for id, state := range a.asks {
		if !state.terminal {
			pending = append(pending, id)
		}
	}
	a.askMu.Unlock()
	slices.Sort(pending)
	for _, id := range pending {
		a.markAskTerminal(id, agentruntime.UserAskResolved{Skipped: true})
	}
}

// buildClarifyAnswer maps the resolved answers onto the exact wire shape
// Hermes' clarify contract expects (tui_gateway/contracts/server_requests.py,
// server.py _clarify_block, tools/clarify_gateway.py; verified against
// hermes-agent origin/main 2026-09-21):
//   - single, answered: {"answer": "<text>"}
//   - single, skipped:  {"answer": ""}
//   - batch, answered:  {"answers": {"<qid>": "<text>", ...}}
//   - batch, skipped:   {} (no "answers" key cancels the whole batch)
func buildClarifyAnswer(questions []agentruntime.AskQuestion, isBatch bool, answers []agentruntime.AskAnswer, skipped bool) (map[string]any, error) {
	if isBatch {
		if skipped {
			return map[string]any{}, nil
		}
		texts, err := batchClarifyAnswers(questions, answers)
		if err != nil {
			return nil, err
		}
		return map[string]any{"answers": texts}, nil
	}
	if skipped {
		return map[string]any{"answer": ""}, nil
	}
	text, err := singleClarifyAnswer(questions, answers)
	if err != nil {
		return nil, err
	}
	return map[string]any{"answer": text}, nil
}

func singleClarifyAnswer(questions []agentruntime.AskQuestion, answers []agentruntime.AskAnswer) (string, error) {
	if len(questions) != 1 {
		return "", fmt.Errorf("hermes clarify: single request expects exactly one question, got %d", len(questions))
	}
	ans, ok := answerForIndex(answers, 0)
	if !ok {
		return "", errors.New("hermes clarify: no answer for the question")
	}
	return clarifyAnswerText(questions[0], ans)
}

// batchClarifyAnswers builds the qid-keyed answers map. A question missing
// from answers is simply absent from the map (Hermes only requires the
// questions it gets an entry for), but every question referenced by an
// answer must resolve to a known qid.
func batchClarifyAnswers(questions []agentruntime.AskQuestion, answers []agentruntime.AskAnswer) (map[string]string, error) {
	if len(answers) == 0 {
		return nil, errors.New("hermes clarify: empty answers for an answered batch")
	}
	result := make(map[string]string, len(answers))
	for _, ans := range answers {
		if ans.QuestionIndex < 0 || ans.QuestionIndex >= len(questions) {
			return nil, fmt.Errorf("hermes clarify: answer question index %d out of range (have %d questions)", ans.QuestionIndex, len(questions))
		}
		q := questions[ans.QuestionIndex]
		if strings.TrimSpace(q.ID) == "" {
			return nil, fmt.Errorf("hermes clarify: question %d missing qid", ans.QuestionIndex)
		}
		text, err := clarifyAnswerText(q, ans)
		if err != nil {
			return nil, err
		}
		result[q.ID] = text
	}
	return result, nil
}

func answerForIndex(answers []agentruntime.AskAnswer, index int) (agentruntime.AskAnswer, bool) {
	for _, ans := range answers {
		if ans.QuestionIndex == index {
			return ans, true
		}
	}
	return agentruntime.AskAnswer{}, false
}

// clarifyAnswerText renders one question's selected labels into the string
// Hermes' answer/answers value expects: the chosen option text for
// single-select, a JSON array string of the chosen option texts for
// multi-select, and the typed text for a question with no choices (Hermes
// rejects free text that is not one of the choices, so a question offering
// choices is never answered with typed text).
func clarifyAnswerText(q agentruntime.AskQuestion, ans agentruntime.AskAnswer) (string, error) {
	if len(ans.Labels) == 0 {
		return "", errors.New("hermes clarify: answer has no selected labels")
	}
	if len(q.Options) == 0 {
		return otherText(ans)
	}
	for _, label := range ans.Labels {
		if !slices.ContainsFunc(q.Options, func(option agentruntime.AskOption) bool { return option.Label == label }) {
			return "", fmt.Errorf("hermes clarify: %q is not one of the question's choices", label)
		}
	}
	if q.MultiSelect {
		encoded, err := json.Marshal(dedupeLabels(ans.Labels))
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
	return ans.Labels[0], nil
}

// otherText extracts the typed text for a no-options question. The shared
// AskAnswer shape carries free text as Labels==[OtherAnswerLabel] +
// OtherText (see agentruntime.BuildUpdatedInputAnswers); a non-conforming
// answer falls back to joining whatever labels arrived.
func otherText(ans agentruntime.AskAnswer) (string, error) {
	if len(ans.Labels) == 1 && ans.Labels[0] == agentruntime.OtherAnswerLabel {
		if strings.TrimSpace(ans.OtherText) == "" {
			return "", fmt.Errorf("hermes clarify: %q selected with empty OtherText", agentruntime.OtherAnswerLabel)
		}
		return ans.OtherText, nil
	}
	return strings.Join(dedupeLabels(ans.Labels), ","), nil
}

func dedupeLabels(labels []string) []string {
	seen := make(map[string]struct{}, len(labels))
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		if _, ok := seen[l]; ok {
			continue
		}
		seen[l] = struct{}{}
		out = append(out, l)
	}
	return out
}

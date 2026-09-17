package codex

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

type GoalStatus string

const (
	GoalStatusActive        GoalStatus = "active"
	GoalStatusPaused        GoalStatus = "paused"
	GoalStatusBlocked       GoalStatus = "blocked"
	GoalStatusUsageLimited  GoalStatus = "usageLimited"
	GoalStatusBudgetLimited GoalStatus = "budgetLimited"
	GoalStatusComplete      GoalStatus = "complete"
)

type Goal struct {
	ThreadID        string     `json:"threadId"`
	Objective       string     `json:"objective"`
	Status          GoalStatus `json:"status"`
	TokenBudget     *int       `json:"tokenBudget"`
	TokensUsed      int        `json:"tokensUsed"`
	TimeUsedSeconds int        `json:"timeUsedSeconds"`
	CreatedAt       int64      `json:"createdAt"`
	UpdatedAt       int64      `json:"updatedAt"`
}

type GoalUpdate struct {
	Objective   *string
	Status      *GoalStatus
	TokenBudget *int
}

type goalResponse struct {
	Goal *Goal `json:"goal"`
}

type goalClearResponse struct {
	Cleared bool `json:"cleared"`
}

func (s *Session) GetGoal(ctx context.Context) (*Goal, error) {
	raw, err := s.callExistingThreadGoal(ctx, appMethodThreadGoalGet, nil)
	if err != nil {
		return nil, err
	}
	var res goalResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return res.Goal, nil
}

func (s *Session) SetGoal(ctx context.Context, update GoalUpdate) (*Goal, error) {
	threadID := strings.TrimSpace(s.ID())
	thread, err := s.ensureThread(ctx, runSpec{
		resumeID: threadID,
		cwd:      s.client.cwd,
		sandbox:  s.client.sandbox,
		approval: s.client.approval,
	})
	if err != nil {
		return nil, err
	}
	threadID = strings.TrimSpace(thread.ThreadID)
	if threadID == "" {
		return nil, errors.New("codex: thread id is required for goal")
	}
	raw, err := s.app.Call(ctx, appMethodThreadGoalSet, goalUpdateParams(threadID, update))
	if err != nil {
		return nil, err
	}
	var res goalResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	if res.Goal == nil {
		return nil, errors.New("codex: thread/goal/set response missing goal")
	}
	return res.Goal, nil
}

func (s *Session) ClearGoal(ctx context.Context) (bool, error) {
	raw, err := s.callExistingThreadGoal(ctx, appMethodThreadGoalClear, nil)
	if err != nil {
		return false, err
	}
	var res goalClearResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return false, err
	}
	return res.Cleared, nil
}

func (s *Session) callExistingThreadGoal(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	threadID := strings.TrimSpace(s.ID())
	if threadID == "" {
		return nil, errors.New("codex: thread id is required for goal")
	}
	thread, err := s.ensureThread(ctx, runSpec{
		resumeID: threadID,
		cwd:      s.client.cwd,
		sandbox:  s.client.sandbox,
		approval: s.client.approval,
	})
	if err != nil {
		return nil, err
	}
	if params == nil {
		params = map[string]any{"threadId": thread.ThreadID}
	} else {
		params["threadId"] = thread.ThreadID
	}
	return s.app.Call(ctx, method, params)
}

func goalUpdateParams(threadID string, update GoalUpdate) map[string]any {
	params := map[string]any{"threadId": threadID}
	if update.Objective != nil {
		params["objective"] = *update.Objective
	}
	if update.Status != nil {
		params["status"] = *update.Status
	}
	if update.TokenBudget != nil {
		params["tokenBudget"] = *update.TokenBudget
	}
	return params
}

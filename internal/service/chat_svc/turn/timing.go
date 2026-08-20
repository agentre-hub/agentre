package turn

import "time"

func (tc *TurnContext) NoteVisibleToken() {
	tc.NoteVisibleTokenAt(time.Now())
}

func (tc *TurnContext) NoteVisibleTokenAt(now time.Time) {
	if tc == nil {
		return
	}
	if tc.StartedAt.IsZero() {
		tc.StartedAt = now
	}
	if tc.FirstTokenAt.IsZero() {
		tc.FirstTokenAt = now
	}
	if tc.BurstStartedAt.IsZero() {
		tc.BurstStartedAt = now
	}
}

func (tc *TurnContext) PauseGeneration() {
	tc.PauseGenerationAt(time.Now())
}

func (tc *TurnContext) PauseGenerationAt(now time.Time) {
	if tc == nil || tc.BurstStartedAt.IsZero() {
		return
	}
	tc.Generation += now.Sub(tc.BurstStartedAt)
	tc.BurstStartedAt = time.Time{}
}

func (tc *TurnContext) FirstTokenMs() int {
	if tc == nil || tc.FirstTokenAt.IsZero() || tc.StartedAt.IsZero() {
		return 0
	}
	ms := int(tc.FirstTokenAt.Sub(tc.StartedAt).Milliseconds())
	if ms < 0 {
		return 0
	}
	return ms
}

func (tc *TurnContext) TokensPerSec(completion int) float64 {
	if tc == nil || completion <= 0 {
		return 0
	}
	gen := tc.Generation
	if !tc.BurstStartedAt.IsZero() {
		gen += time.Since(tc.BurstStartedAt)
	}
	if gen <= 0 {
		return 0
	}
	return float64(completion) / gen.Seconds()
}

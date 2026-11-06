package terminal_svc

import "time"

// SetStartFlushTimerForTest swaps the pump's flush-timer source for an external
// test and returns a func that restores the production implementation.
func SetStartFlushTimerForTest(f func(time.Duration) (<-chan time.Time, func() bool)) func() {
	prev := startFlushTimer
	startFlushTimer = f
	return func() { startFlushTimer = prev }
}

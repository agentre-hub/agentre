package hermes

import (
	"io"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// nextUnsupportedRequestNotice reads the turn stream until the next
// UnsupportedRequestNotice, skipping unrelated events.
func nextUnsupportedRequestNotice(t *testing.T, out <-chan agentruntime.Event) agentruntime.UnsupportedRequestNotice {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-out:
			require.True(t, ok, "the turn ended before an unsupported-request notice arrived")
			if n, isNotice := ev.(agentruntime.UnsupportedRequestNotice); isNotice {
				return n
			}
		case <-timeout:
			t.Fatal("no unsupported-request notice arrived")
		}
	}
}

// TestHermesUnsupportedRequest_NoticeCarriesReadablePurpose covers every method
// in design decision 4's closed vocabulary: the turn must map the (already
// answered by frame.go) method name onto its readable purpose category and
// emit a notice event — never the raw method name itself.
func TestHermesUnsupportedRequest_NoticeCarriesReadablePurpose(t *testing.T) {
	Convey("Given a turn in flight", t, func() {
		cases := []struct {
			method string
			want   agentruntime.UnsupportedRequestPurpose
		}{
			{methodSudo, agentruntime.UnsupportedRequestSudoPassword},
			{methodSecret, agentruntime.UnsupportedRequestSecret},
			{methodVaultUnlockPrompt, agentruntime.UnsupportedRequestVaultUnlock},
			{methodVaultSaveLogin, agentruntime.UnsupportedRequestVaultSaveLogin},
			{methodVaultCode, agentruntime.UnsupportedRequestVaultCode},
			{methodTerminalRead, agentruntime.UnsupportedRequestTerminalRead},
			{methodPreviewRead, agentruntime.UnsupportedRequestPreviewRead},
			{methodWindowRead, agentruntime.UnsupportedRequestWindowRead},
			{methodPreviewAct, agentruntime.UnsupportedRequestPreviewAct},
			{methodTour, agentruntime.UnsupportedRequestTour},
		}
		for i, c := range cases {
			Convey("When Hermes' "+c.method+" request was already answered", func() {
				fake, _, out := startApprovalTurn(t, int64(300+i))
				fake.push(Event{Kind: EventUnsupportedRequest, Method: c.method})

				notice := nextUnsupportedRequestNotice(t, out)

				assert.Equal(t, c.want, notice.Purpose)
				fake.closeEvents(io.EOF)
				_ = collect(t, out)
			})
		}

		Convey("When the method is not in the closed vocabulary Then the notice reads as an unknown request", func() {
			fake, _, out := startApprovalTurn(t, 400)
			fake.push(Event{Kind: EventUnsupportedRequest, Method: "future.unknown"})

			notice := nextUnsupportedRequestNotice(t, out)

			assert.Equal(t, agentruntime.UnsupportedRequestUnknown, notice.Purpose)
			fake.closeEvents(io.EOF)
			_ = collect(t, out)
		})
	})
}

// handleUnsupportedRequest itself must not touch the Session (no answer is
// sent from this layer — frame.go already answered synchronously).
func TestHermesUnsupportedRequest_TurnDoesNotAnswerAgain(t *testing.T) {
	fake, _, out := startApprovalTurn(t, 401)
	fake.push(Event{Kind: EventUnsupportedRequest, Method: methodSudo})

	_ = nextUnsupportedRequestNotice(t, out)

	assert.Empty(t, fake.sentAnswers(), "the turn must not answer an already-answered unsupported request")
	fake.closeEvents(io.EOF)
	_ = collect(t, out)
}

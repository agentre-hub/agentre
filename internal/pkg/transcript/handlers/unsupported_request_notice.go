package handlers

import (
	"context"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
)

// UnsupportedRequestNoticeHandler turns agentruntime.UnsupportedRequestNotice
// into a persisted cago NoticeBlock, following the structured-notice pattern
// (blocks.EncodeUnsupportedRequestNotice) the provider fallback/switch
// notices already use: the wire never carries a display string, only the
// closed Purpose vocabulary — agentre-ui resolves the sentence through i18n.
//
// No live emit: like the provider-fallback/switch notice (appended directly
// to finalBlocks at turn end, chat_svc/turn_run.go), this notice is a
// coarse "something was skipped" banner, not a per-token live signal, so it
// reaches the frontend the same way the persisted block always does — no
// StreamNotice wire kind exists today and adding one is unrelated widening.
type UnsupportedRequestNoticeHandler struct{}

func (UnsupportedRequestNoticeHandler) Apply(_ context.Context, ev agentruntime.Event, acc *turn.Accumulator, _ turn.Emitter, _ *turn.TurnContext) error {
	n := ev.(agentruntime.UnsupportedRequestNotice)
	purpose := string(n.Purpose)
	if purpose == "" || acc == nil {
		return nil
	}
	acc.AddBlock(&cagoblocks.NoticeBlock{
		Level: "info",
		Text:  blocks.EncodeUnsupportedRequestNotice(purpose),
	}, "")
	return nil
}

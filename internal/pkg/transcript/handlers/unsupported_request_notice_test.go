package handlers

import (
	"context"
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
)

func TestUnsupportedRequestNoticeHandler_AppendsStructuredNoticeBlock(t *testing.T) {
	Convey("Given an UnsupportedRequestNotice with a purpose", t, func() {
		acc := turn.New()

		err := UnsupportedRequestNoticeHandler{}.Apply(
			context.Background(),
			agentruntime.UnsupportedRequestNotice{Purpose: agentruntime.UnsupportedRequestSudoPassword},
			acc, nil, nil)

		So(err, ShouldBeNil)
		final := acc.Finalize()
		So(final, ShouldHaveLength, 1)
		notice, ok := final[0].(*cagoblocks.NoticeBlock)
		So(ok, ShouldBeTrue)
		So(notice.Level, ShouldEqual, "info")
		payload, decoded := blocks.DecodeUnsupportedRequestNotice(notice.Text)
		So(decoded, ShouldBeTrue)
		So(payload.Purpose, ShouldEqual, "sudo_password")
	})

	Convey("Given an empty purpose Then no block is appended", t, func() {
		acc := turn.New()

		err := UnsupportedRequestNoticeHandler{}.Apply(
			context.Background(),
			agentruntime.UnsupportedRequestNotice{},
			acc, nil, nil)

		So(err, ShouldBeNil)
		So(acc.Finalize(), ShouldHaveLength, 0)
	})
}

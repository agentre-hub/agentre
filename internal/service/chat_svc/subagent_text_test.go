package chat_svc_test

import (
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

func TestMessageText(t *testing.T) {
	Convey("messageText", t, func() {
		Convey("nil message → empty string", func() {
			result, err := chat_svc.MessageTextExport(nil)
			So(err, ShouldBeNil)
			So(result, ShouldEqual, "")
		})

		Convey("message with pointer TextBlocks → concatenated text", func() {
			msg := &chat_entity.Message{}
			err := msg.SetBlocks([]blocks.ContentBlock{
				&blocks.TextBlock{Text: "hello "},
				&blocks.TextBlock{Text: "world"},
			})
			So(err, ShouldBeNil)

			result, err := chat_svc.MessageTextExport(msg)
			So(err, ShouldBeNil)
			So(result, ShouldEqual, "hello world")
		})

		Convey("message with value TextBlocks → concatenated text", func() {
			msg := &chat_entity.Message{}
			err := msg.SetBlocks([]blocks.ContentBlock{
				blocks.TextBlock{Text: "foo "},
				blocks.TextBlock{Text: "bar"},
			})
			So(err, ShouldBeNil)

			result, err := chat_svc.MessageTextExport(msg)
			So(err, ShouldBeNil)
			So(result, ShouldEqual, "foo bar")
		})

		Convey("message with no TextBlocks → empty string", func() {
			msg := &chat_entity.Message{}
			err := msg.SetBlocks([]blocks.ContentBlock{})
			So(err, ShouldBeNil)

			result, err := chat_svc.MessageTextExport(msg)
			So(err, ShouldBeNil)
			So(result, ShouldEqual, "")
		})
	})
}

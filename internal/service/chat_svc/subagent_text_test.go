package chat_svc_test

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

func TestLatestAssistantText(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	// Register the real message repo (backed by sqlmock DB from testutils.Database).
	prevMsg := chat_repo.Message()
	chat_repo.RegisterMessage(chat_repo.NewMessage())
	t.Cleanup(func() { chat_repo.RegisterMessage(prevMsg) })

	// gorm First() appends `,`chat_messages`.`id`` to ORDER BY; regex adjusted to match.
	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE session_id = \\? AND role = \\? ORDER BY seq DESC,`chat_messages`.`id` LIMIT \\?").
		WithArgs(int64(3), "assistant", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "role", "seq"}).
			AddRow(5, 3, "assistant", 2))
	// 正文按「一块一行」存在块表,由仓储读时重组。
	mock.ExpectQuery("SELECT \\* FROM `chat_message_blocks` WHERE message_id IN \\(\\?\\)").
		WithArgs(int64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"message_id", "idx", "type", "tool_call_id", "codec", "data"}).
			AddRow(5, 0, "text", "", chat_entity.BlockCodecRaw, []byte(`{"text":"进行到一半"}`)))
	svc := chat_svc.NewChat(chat_svc.NoopEmitter{})
	got, err := svc.LatestAssistantText(ctx, 3)
	require.NoError(t, err)
	assert.Equal(t, "进行到一半", got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

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

package claudecode

import (
	"bufio"
	"context"
	"io"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
)

func TestSetModel(t *testing.T) {
	Convey("Given a persistent fake CLI, When selecting or resetting its model, Then control requests are acknowledged without a user turn", t, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		frames := make(chan string, 4)
		c := New(pipeSpawner(t, func(stdin io.Reader, stdout io.Writer) {
			sc := bufio.NewScanner(stdin)
			for sc.Scan() {
				line := sc.Text()
				frames <- line
				id := extractStringField(line, "request_id")
				if extractStringField(line, "subtype") == "get_settings" {
					writeFrame(stdout, `{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":{"applied":{"model":"claude-sonnet-actual"}}}}`, id)
				} else if extractStringField(line, "model") == "bad" {
					writeFrame(stdout, `{"type":"control_response","response":{"subtype":"error","request_id":%q,"error":"Model 'bad' not found"}}`, id)
				} else {
					writeFrame(stdout, `{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":{}}}`, id)
				}
			}
		}))
		sess, err := c.OpenSession(ctx)
		require.NoError(t, err)
		defer func() { _ = sess.Close(ctx) }()
		setter, ok := any(sess).(interface {
			SetModel(context.Context, string) error
		})
		require.True(t, ok, "persistent CLI session must support set_model")
		So(setter.SetModel(ctx, "sonnet"), ShouldBeNil)
		frame := <-frames
		So(extractStringField(frame, "subtype"), ShouldEqual, "set_model")
		So(extractStringField(frame, "model"), ShouldEqual, "sonnet")
		So(setter.SetModel(ctx, ""), ShouldBeNil)
		So(<-frames, ShouldNotContainSubstring, `"model"`)
		err = setter.SetModel(ctx, "bad")
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "Model 'bad' not found")
		reporter, ok := any(sess).(interface {
			CurrentModel(context.Context) (string, error)
		})
		require.True(t, ok, "session must report its own effective model")
		model, err := reporter.CurrentModel(ctx)
		So(err, ShouldBeNil)
		So(model, ShouldEqual, "claude-sonnet-actual")
	})
}

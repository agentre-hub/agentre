package claudecode

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
)

func TestQueryModels(t *testing.T) {
	Convey("Given a CLI stuck before initialization, When the query is cancelled during startup, Then its process is terminated", t, func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var spawned *process
		c := New()
		c.spawner = func(context.Context, processSpec) (*process, error) {
			// 固定 shell 只模拟卡住的 CLI，不调用真实 Claude。
			p, err := startProcess(context.Background(), processSpec{binary: "/bin/sh", args: []string{"-c", "sleep 60 & wait"}})
			require.NoError(t, err)
			spawned = p
			t.Cleanup(p.kill)
			cancel()
			return p, nil
		}
		_, err := c.QueryModels(ctx)
		So(errors.Is(err, context.Canceled), ShouldBeTrue)
		exited := false
		select {
		case <-spawned.exit:
			exited = true
		case <-time.After(time.Second):
		}
		So(exited, ShouldBeTrue)
	})
	Convey("Given a query-only Claude CLI, When models are requested, Then initialize and applied settings are returned without a user turn or model argument", t, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var requests []string
		exited := make(chan struct{})
		c := New(WithModel("must-not-send"), pipeSpawner(t, func(stdin io.Reader, stdout io.Writer) {
			defer close(exited)
			sc := bufio.NewScanner(stdin)
			for sc.Scan() {
				line := sc.Text()
				requests = append(requests, extractStringField(line, "subtype"))
				id := extractStringField(line, "request_id")
				switch extractStringField(line, "subtype") {
				case "initialize":
					writeFrame(stdout, `{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":{"models":[{"value":"default","displayName":"Default Opus","resolvedModel":"claude-opus"},{"value":"sonnet","displayName":"Sonnet","resolvedModel":"claude-sonnet"}]}}}`, id)
				case "get_settings":
					writeFrame(stdout, `{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":{"applied":{"model":"claude-sonnet"}}}}`, id)
				default:
					return
				}
			}
		}))
		spawn := c.spawner
		c.spawner = func(ctx context.Context, spec processSpec) (*process, error) {
			require.NotContains(t, spec.args, "--model")
			return spawn(ctx, spec)
		}
		got, err := c.QueryModels(ctx)
		require.NoError(t, err)
		<-exited
		So(requests, ShouldResemble, []string{"initialize", "get_settings"})
		So(got.Models, ShouldHaveLength, 2)
		So(got.Models[1].Value, ShouldEqual, "sonnet")
		So(got.Models[1].ResolvedModel, ShouldEqual, "claude-sonnet")
		So(got.EffectiveModel, ShouldEqual, "claude-sonnet")
	})
	Convey("Given an old CLI, When initialize is rejected, Then the original error is reported and the process closes", t, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		c := New(pipeSpawner(t, func(stdin io.Reader, stdout io.Writer) {
			sc := bufio.NewScanner(stdin)
			for sc.Scan() {
				writeFrame(stdout, `{"type":"control_response","response":{"subtype":"error","request_id":%q,"error":"unsupported initialize"}}`, extractStringField(sc.Text(), "request_id"))
			}
		}))
		_, err := c.QueryModels(ctx)
		So(err, ShouldNotBeNil)
		So(strings.Contains(err.Error(), "unsupported initialize"), ShouldBeTrue)
	})
}

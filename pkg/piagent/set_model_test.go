package piagent

import (
	"context"
	"strings"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
)

func TestSessionSetModel(t *testing.T) {
	Convey("Given 一条常驻 pi RPC 会话", t, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		Convey("When 切到 pi 认识的 provider/id Then 拆成 {provider, modelId} 发 set_model 并交回 pi 的模型对象", func() {
			client, _, proc := newModelQueryClient(`{"id":"set-model","type":"response","command":"set_model","success":true,"data":{"provider":"openrouter","id":"anthropic/claude-sonnet-5-5","name":"Claude Sonnet 5.5"}}` + "\n")
			session, err := client.OpenSession(ctx)
			require.NoError(t, err)
			defer func() { _ = session.Close(context.Background()) }()

			// id 里本身带斜杠：只按第一个斜杠拆，后半段整体是 modelId。
			model, err := session.SetModel(ctx, "openrouter/anthropic/claude-sonnet-5-5")
			So(err, ShouldBeNil)
			So(model, ShouldResemble, Model{Provider: "openrouter", ID: "anthropic/claude-sonnet-5-5", Name: "Claude Sonnet 5.5"})
			frames := strings.Split(strings.TrimSpace(proc.stdin.String()), "\n")
			So(frames, ShouldHaveLength, 1)
			So(frames[0], ShouldContainSubstring, `"type":"set_model"`)
			So(frames[0], ShouldContainSubstring, `"provider":"openrouter"`)
			So(frames[0], ShouldContainSubstring, `"modelId":"anthropic/claude-sonnet-5-5"`)
			So(frames[0], ShouldNotContainSubstring, `"type":"prompt"`)
		})

		Convey("When pi 拒绝（Model not found） Then 错误带 pi 的原文", func() {
			client, _, _ := newModelQueryClient(`{"id":"set-model","type":"response","command":"set_model","success":false,"error":"Model not found: zhipu/nope"}` + "\n")
			session, err := client.OpenSession(ctx)
			require.NoError(t, err)
			defer func() { _ = session.Close(context.Background()) }()

			_, err = session.SetModel(ctx, "zhipu/nope")
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "Model not found: zhipu/nope")
		})

		Convey("When 值不是 provider/id Then 不发帧直接报错", func() {
			client, _, proc := newModelQueryClient("")
			session, err := client.OpenSession(ctx)
			require.NoError(t, err)
			defer func() { _ = session.Close(context.Background()) }()

			_, err = session.SetModel(ctx, "glm-5.3")
			So(err, ShouldNotBeNil)
			So(proc.stdin.String(), ShouldBeEmpty)
		})
	})
}

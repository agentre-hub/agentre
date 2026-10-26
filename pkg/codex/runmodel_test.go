package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSessionStream_CarriesTurnModelWithoutRestart 锁住 codex 换模型的唯一入口：
// TurnStartParams.model「Override the model for this turn and subsequent turns」。
// 首轮帧与今天逐字相同（不下发 model），其后各轮在同一 app-server 与线程上带上选择，
// 启动参数一个字都不变（spec 2026-10-26 决策 6：会话内切换不重启 CLI）。
func TestSessionStream_CarriesTurnModelWithoutRestart(t *testing.T) {
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondAppServerInit(t, h, sc)

		startReq := readRPCReq(t, sc)
		assert.Equal(t, "thread/start", startReq.Method)
		respondRPC(h, startReq, map[string]any{
			"thread": map[string]any{"id": "thread-cli-model"},
			"model":  "gpt-5.5",
		})

		first := readRPCReq(t, sc)
		assert.Equal(t, "turn/start", first.Method)
		assert.JSONEq(t, `{"threadId":"thread-cli-model","input":[{"type":"text","text":"hello","text_elements":[]}]}`, string(first.Params))
		respondRPC(h, first, map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress"}})
		h.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-cli-model", "turnId": "turn-1", "turn": map[string]any{"id": "turn-1", "status": "completed"}}})

		second := readRPCReq(t, sc)
		assert.Equal(t, "turn/start", second.Method)
		assert.JSONEq(t, `{"threadId":"thread-cli-model","input":[{"type":"text","text":"again","text_elements":[]}],"model":"gpt-6-luna"}`, string(second.Params))
		respondRPC(h, second, map[string]any{"turn": map[string]any{"id": "turn-2", "status": "inProgress"}})
		h.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-cli-model", "turnId": "turn-2", "turn": map[string]any{"id": "turn-2", "status": "completed"}}})
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sess, err := client.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	first, err := sess.Stream(ctx, "hello")
	require.NoError(t, err)
	for first.Next() {
	}
	require.NoError(t, first.Close(ctx))

	second, err := sess.Stream(ctx, "again", RunModel("gpt-6-luna"))
	require.NoError(t, err)
	for second.Next() {
	}
	require.NoError(t, second.Close(ctx))

	// 同一个 app-server 进程：模型选择不改变启动参数，也不需要重开。
	require.Len(t, runner.opts, 1)
	// turn/start 的模型覆盖本轮及其后各轮，线程由此改跑它。
	assert.Equal(t, "gpt-6-luna", sess.Model())
}

// TestTurnStartParams_TurnModelWinsInCollaborationMode 锁住协作模式下的优先级：
// 顶层 model 与会话级选择一致，plan 模式的 collaborationMode.settings.model 跟着它走，
// 而不是线程启动时的旧模型。
func TestTurnStartParams_TurnModelWinsInCollaborationMode(t *testing.T) {
	params, err := turnStartParamsInput(
		appThreadStartResult{ThreadID: "thread-plan", Model: "gpt-5.5"},
		userInput("plan this"),
		CollaborationPlan,
		"",
		"gpt-6-luna",
	)
	require.NoError(t, err)

	raw, err := json.Marshal(params)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"threadId":"thread-plan",
		"input":[{"type":"text","text":"plan this","text_elements":[]}],
		"model":"gpt-6-luna",
		"collaborationMode":{"mode":"plan","settings":{"model":"gpt-6-luna"}}
	}`, string(raw))
}

// TestSessionStream_RejectedTurnModelKeepsCodexError 锁住「被拒的这一轮不开始、报 codex
// 原文、线程模型不被记成没生效的值」：chat_svc 靠这条错误让该轮失败并让 pill 失效。
func TestSessionStream_RejectedTurnModelKeepsCodexError(t *testing.T) {
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondAppServerInit(t, h, sc)

		startReq := readRPCReq(t, sc)
		respondRPC(h, startReq, map[string]any{
			"thread": map[string]any{"id": "thread-rejected"},
			"model":  "gpt-5.5",
		})

		turnReq := readRPCReq(t, sc)
		assert.Equal(t, "turn/start", turnReq.Method)
		assert.Contains(t, string(turnReq.Params), `"model":"bad-model"`)
		respondRPCError(h, turnReq, -32602, "model 'bad-model' not found")
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sess, err := client.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	_, err = sess.Stream(ctx, "hello", RunModel("bad-model"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model 'bad-model' not found")
	assert.Equal(t, "gpt-5.5", sess.Model())
}

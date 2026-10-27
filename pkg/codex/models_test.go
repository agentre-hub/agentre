package codex

import (
	"bufio"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientQueryModels(t *testing.T) {
	// Given a codex app-server that answers model/list and config/read.
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondAppServerInit(t, h, sc)

		listReq := readRPCReq(t, sc)
		assert.Equal(t, "model/list", listReq.Method)
		// 查询不带任何模型参数:客户端若配了 model,也不能出现在查询帧里;
		// 但 params 成员必须在(真实 app-server 拒绝缺 params 的请求)。
		assert.JSONEq(t, `{}`, string(listReq.Params))
		respondRPC(h, listReq, map[string]any{"data": []map[string]any{
			{"id": "gpt-6-luna", "model": "gpt-6-luna", "displayName": "GPT-6 Luna", "isDefault": true, "defaultReasoningEffort": "high", "hidden": false},
			{"id": "gpt-5.5", "model": "gpt-5.5", "displayName": "GPT-5.5", "hidden": false},
			{"id": "internal-only", "model": "internal-only", "displayName": "Internal", "hidden": true},
		}})

		cfgReq := readRPCReq(t, sc)
		assert.Equal(t, "config/read", cfgReq.Method)
		assert.JSONEq(t, `{}`, string(cfgReq.Params))
		respondRPC(h, cfgReq, map[string]any{"config": map[string]any{"model": "gpt-6-luna"}})
	}

	client := New(
		WithBinary("codex-test"),
		WithModel("gpt-5.4"),
		WithAppServerRunnerForTesting(runner),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// When the catalog is queried, Then hidden models are dropped and the effective
	// model comes from config/read, not from the isDefault flag.
	catalog, err := client.QueryModels(ctx)
	require.NoError(t, err)
	require.Len(t, catalog.Models, 2)
	assert.Equal(t, Model{ID: "gpt-6-luna", Model: "gpt-6-luna", DisplayName: "GPT-6 Luna", IsDefault: true, DefaultReasoningEffort: "high"}, catalog.Models[0])
	assert.Equal(t, Model{ID: "gpt-5.5", Model: "gpt-5.5", DisplayName: "GPT-5.5"}, catalog.Models[1])
	assert.Equal(t, "gpt-6-luna", catalog.EffectiveModel)

	// 查询进程只发了 initialize/initialized/model/list/config/read,没有起 thread。
	require.Len(t, runner.opts, 1)
	assert.Equal(t, []string{"app-server", "--listen", "stdio://"}, runner.opts[0].Args)
	assert.Equal(t, "codex-test", runner.opts[0].Binary)
}

func TestClientQueryModels_EffectiveModelIsNotGuessedFromIsDefault(t *testing.T) {
	// Given config/read reports no applied model.
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondAppServerInit(t, h, sc)
		listReq := readRPCReq(t, sc)
		respondRPC(h, listReq, map[string]any{"data": []map[string]any{
			{"id": "gpt-6-luna", "model": "gpt-6-luna", "displayName": "GPT-6 Luna", "isDefault": true},
		}})
		respondRPC(h, readRPCReq(t, sc), map[string]any{"config": map[string]any{}})
	}
	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// When querying, Then no effective model is guessed from the isDefault entry.
	catalog, err := client.QueryModels(ctx)
	require.NoError(t, err)
	require.Len(t, catalog.Models, 1)
	assert.Empty(t, catalog.EffectiveModel)
}

func TestClientQueryModels_UnsupportedCLI(t *testing.T) {
	// Given an app-server that rejects model/list (older CLI).
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondAppServerInit(t, h, sc)
		respondRPCError(h, readRPCReq(t, sc), -32601, "method not found: model/list")
	}
	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// When querying, Then the CLI original error is returned instead of a made-up list.
	_, err := client.QueryModels(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "method not found: model/list")
}

// rejectParamlessLikeRealAppServer 照真实 codex app-server(0.162.1)的行为:
// 请求帧缺 params 成员时直接以 -32600 拒绝,不进入方法处理。返回 false 表示已拒绝。
func rejectParamlessLikeRealAppServer(h *fakeAppServerHandle, req rpcReq) bool {
	if len(req.Params) == 0 {
		respondRPCError(h, req, -32600, "Invalid request: missing field `params`")
		return false
	}
	return true
}

func TestClientQueryModels_RealAppServerRequiresParams(t *testing.T) {
	// Given an app-server that, like the real codex CLI, rejects any request without params.
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondAppServerInit(t, h, sc)
		listReq := readRPCReq(t, sc)
		if !rejectParamlessLikeRealAppServer(h, listReq) {
			return
		}
		respondRPC(h, listReq, map[string]any{"data": []map[string]any{
			{"id": "gpt-6-luna", "model": "gpt-6-luna", "displayName": "GPT-6 Luna"},
		}})
		cfgReq := readRPCReq(t, sc)
		if !rejectParamlessLikeRealAppServer(h, cfgReq) {
			return
		}
		respondRPC(h, cfgReq, map[string]any{"config": map[string]any{"model": "gpt-6-luna"}})
	}
	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// When querying, Then both model/list and config/read are accepted and answered.
	catalog, err := client.QueryModels(ctx)
	require.NoError(t, err)
	require.Len(t, catalog.Models, 1)
	assert.Equal(t, "gpt-6-luna", catalog.EffectiveModel)
}

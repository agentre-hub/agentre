package piagent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// modelQueryRunner 记录查询进程的启动参数,并把它接到一个预置帧的假进程上。
type modelQueryRunner struct {
	proc *captureProc
	opts procOptions
}

func (r *modelQueryRunner) Start(_ context.Context, opts procOptions) (processHandle, error) {
	r.opts = opts
	return r.proc, nil
}

func newModelQueryClient(stdout string) (*Client, *modelQueryRunner, *captureProc) {
	proc := &captureProc{
		stdin:  &lockedBuffer{},
		stdout: strings.NewReader(stdout),
		done:   make(chan error, 1),
	}
	runner := &modelQueryRunner{proc: proc}
	client := New(
		WithBinary("pi-test"),
		WithRPCProcessRunnerForTesting(runner),
		WithKillGrace(time.Second),
	)
	return client, runner, proc
}

func TestClientQueryModels(t *testing.T) {
	// Given a pi RPC that answers get_available_models then get_state.
	script := strings.Join([]string{
		`{"id":"cli-models","type":"response","command":"get_available_models","success":true,"data":{"models":[{"provider":"anthropic","id":"claude-sonnet-5-5","name":"Claude Sonnet 5.5"},{"provider":"zhipu","id":"glm-5.3","name":"GLM 5.3"}]}}`,
		`{"id":"cli-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"query-session","model":{"provider":"zhipu","id":"glm-5.3","name":"GLM 5.3"}}}`,
		"",
	}, "\n")
	client, runner, proc := newModelQueryClient(script)
	// 客户端带着模型与会话配置,查询进程也必须把它们丢掉。
	client = withQueryNoise(client)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// When the catalog is queried
	catalog, err := client.QueryModels(ctx)
	require.NoError(t, err)

	// Then the CLI's own list is returned with the provider kept alongside the id.
	require.Len(t, catalog.Models, 2)
	assert.Equal(t, Model{Provider: "anthropic", ID: "claude-sonnet-5-5", Name: "Claude Sonnet 5.5"}, catalog.Models[0])
	assert.Equal(t, Model{Provider: "zhipu", ID: "glm-5.3", Name: "GLM 5.3"}, catalog.Models[1])
	// Then the effective model comes from get_state, not from the list order.
	assert.Equal(t, Model{Provider: "zhipu", ID: "glm-5.3", Name: "GLM 5.3"}, catalog.Effective)

	// Then the query process only asked for the two catalogs, never a prompt.
	frames := strings.Split(strings.TrimSpace(proc.stdin.String()), "\n")
	require.Len(t, frames, 2)
	assert.Contains(t, frames[0], `"type":"get_available_models"`)
	assert.Contains(t, frames[1], `"type":"get_state"`)
	assert.NotContains(t, proc.stdin.String(), `"type":"prompt"`)

	// Then the process was started with a query-only command line: the backend's own
	// model / session / thinking / extension flags must not leak into it.
	assert.Equal(t, []string{"--mode", "rpc"}, runner.opts.Args)
	assert.Equal(t, "pi-test", runner.opts.Binary)
}

// withQueryNoise 给客户端装上真实会话才会有的启动参数,验证查询进程把它们全部丢掉。
func withQueryNoise(c *Client) *Client {
	noisy := *c
	noisy.model = "zhipu/glm-5.3"
	noisy.thinking = "high"
	noisy.systemPrompt = "you are a test"
	noisy.session = "pi-session-1"
	noisy.extensions = []string{"/tmp/ext.mjs"}
	return &noisy
}

func TestClientQueryModels_EffectiveIsNotGuessedFromList(t *testing.T) {
	// Given get_state reports no model at all.
	script := strings.Join([]string{
		`{"id":"cli-models","type":"response","command":"get_available_models","success":true,"data":{"models":[{"provider":"anthropic","id":"claude-sonnet-5-5","name":"Claude Sonnet 5.5"}]}}`,
		`{"id":"cli-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"query-session"}}`,
		"",
	}, "\n")
	client, _, _ := newModelQueryClient(script)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// When querying, Then no effective model is guessed from the first list entry.
	catalog, err := client.QueryModels(ctx)
	require.NoError(t, err)
	require.Len(t, catalog.Models, 1)
	assert.Equal(t, Model{}, catalog.Effective)
}

func TestClientQueryModels_UnsupportedCLI(t *testing.T) {
	// Given an older pi whose get_available_models answers without a catalog.
	script := strings.Join([]string{
		`{"id":"cli-models","type":"response","command":"get_available_models","success":true,"data":{}}`,
		"",
	}, "\n")
	client, _, _ := newModelQueryClient(script)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// When querying, Then the unsupported protocol is reported instead of an empty list.
	_, err := client.QueryModels(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get_available_models")
}

func TestClientQueryModels_FailureResponse(t *testing.T) {
	// Given pi rejects get_available_models.
	script := strings.Join([]string{
		`{"id":"cli-models","type":"response","command":"get_available_models","success":false,"error":"unavailable"}`,
		"",
	}, "\n")
	client, _, _ := newModelQueryClient(script)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// When querying, Then the failure is reported with the command that failed.
	_, err := client.QueryModels(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get_available_models")
}

func TestClientQueryModels_StateFailure(t *testing.T) {
	// Given the list works but get_state is rejected.
	script := strings.Join([]string{
		`{"id":"cli-models","type":"response","command":"get_available_models","success":true,"data":{"models":[{"provider":"anthropic","id":"claude-sonnet-5-5","name":"Claude Sonnet 5.5"}]}}`,
		`{"id":"cli-state","type":"response","command":"get_state","success":false,"error":"unavailable"}`,
		"",
	}, "\n")
	client, _, _ := newModelQueryClient(script)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// When querying, Then the whole query fails: a list without resolution is not "跟随 CLI 默认".
	_, err := client.QueryModels(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get_state")
}

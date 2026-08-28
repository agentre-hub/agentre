package piagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStream_RawSinkReceivesFrames 校验 pi-agent 子进程普通原始 stdout JSON-RPC 帧
// 会喂给 rawSink —— debug 级原始帧转储底座。走 Text → Stream → startRPC
// 真实注入路径,而非直接 newStream,确保 Client.rawSink 真的接到 rpcProcess。
func TestStream_RawSinkSanitizesSensitiveFrames(t *testing.T) {
	// Given successful fork, message, tool, and metadata frames carry prompt text,
	// images, session content, and credential-shaped values,
	// When raw diagnostics are enabled,
	// Then the sink keeps only useful protocol metadata and never receives payload bodies.
	secrets := []string{
		"fork-secret-prompt",
		"user-secret-prompt",
		"secret-image-base64",
		"assistant-secret-delta",
		"tool-secret-argument",
		"tool-secret-result",
		"assistant-secret-content",
		"credential-secret-token",
	}
	script := strings.Join([]string{
		`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"session-old","credential":"credential-secret-token"}}`,
		`{"id":"session-fork","type":"response","command":"fork","success":true,"data":{"text":"fork-secret-prompt","cancelled":false}}`, //nolint:misspell // Pi RPC field uses British spelling.
		`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"session-new"}}`,
		`{"id":"session-entries-before","type":"response","command":"get_entries","success":true,"data":{"entries":[],"leafId":null}}`,
		`{"type":"response","command":"prompt","success":true,"data":{"credential":"credential-secret-token"}}`,
		`{"type":"message_start","message":{"role":"user","content":"user-secret-prompt","images":[{"data":"secret-image-base64"}]}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"assistant-secret-delta"}}`,
		`{"type":"tool_execution_start","toolCallId":"tool-1","toolName":"bash","args":{"command":"tool-secret-argument","token":"credential-secret-token"}}`,
		`{"type":"tool_execution_end","toolCallId":"tool-1","toolName":"bash","result":{"content":[{"type":"text","text":"tool-secret-result"}]}}`,
		`{"type":"message_end","message":{"role":"assistant","content":"assistant-secret-content"}}`,
		`{"type":"agent_end","messages":[{"role":"assistant","content":"assistant-secret-content","stopReason":"stop"}],"willRetry":false}`,
		`{"type":"agent_settled"}`,
		`{"id":"session-entries-after","type":"response","command":"get_entries","success":true,"data":{"entries":[{"type":"message","id":"turn-user","parentId":null,"message":{"role":"user","content":"user-secret-prompt"}}],"leafId":"turn-user"}}`,
		`{"type":"response","command":"get_session_stats","success":true,"data":{"contextUsage":{"contextWindow":200000},"credential":"credential-secret-token"}}`,
		"",
	}, "\n")
	client, _, _ := newSingleProcessCaptureClient(script)
	client.session = "session-old"

	var mu sync.Mutex
	var got []string
	client.rawSink = func(line []byte) {
		mu.Lock()
		got = append(got, string(line))
		mu.Unlock()
	}

	stream, err := client.Stream(context.Background(), "user-secret-prompt", RunForkAnchor("fork-user"))
	require.NoError(t, err)
	for stream.Next() {
	}

	mu.Lock()
	joined := strings.Join(got, "\n")
	mu.Unlock()
	for _, secret := range secrets {
		assert.NotContains(t, joined, secret)
	}
	assert.Contains(t, joined, `"command":"fork"`)
	assert.Contains(t, joined, `"cancelled":false`) //nolint:misspell // Pi RPC field uses British spelling.
	assert.Contains(t, joined, `"type":"message_start"`)
	assert.Contains(t, joined, `"role":"user"`)
	assert.Contains(t, joined, `"type":"message_update"`)
	assert.Contains(t, joined, `"toolName":"bash"`)
	assert.Contains(t, joined, `"command":"get_session_stats"`)
}

func TestFailureResponseErrorOmitsUntrustedPayloads(t *testing.T) {
	// Given Pi rejects an RPC command with secret-bearing error/data variants,
	// When pkg/piagent creates the caller-visible error,
	// Then it retains the safe command identity but none of the failure payload.
	secrets := []string{
		"private user prompt: rotate payroll keys",
		"data:image/png;base64,PRIVATE_IMAGE_BYTES",
		"Authorization: Bearer private-token-value",
		"session entry content: private conversation",
	}
	tests := []struct {
		name     string
		response rpcResponse
	}{
		{
			name: "error field",
			response: rpcResponse{
				Type: "response", Command: "prompt", Error: strings.Join(secrets, " | "),
			},
		},
		{
			name: "data object fallback",
			response: rpcResponse{
				Type: "response", Command: "prompt", Data: json.RawMessage(`{"message":"` + secrets[0] + `","token":"` + secrets[2] + `"}`),
			},
		},
		{
			name: "substitute data string fallback",
			response: rpcResponse{
				Type: "response", Command: "prompt", Data: json.RawMessage(`"` + secrets[3] + `"`),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := failureResponseError(tt.response)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "prompt")
			assertOmitsSensitiveValues(t, err.Error(), secrets)
		})
	}
}

func TestStreamFailureEventsOmitUntrustedMessages(t *testing.T) {
	// Given Pi embeds prompt, image, session-entry, and credential material in
	// terminal or streaming failure strings, when the stream settles, then only
	// a stable failure classification leaves pkg/piagent.
	secrets := []string{
		"private user prompt: calculate acquisition price",
		"data:image/png;base64,PRIVATE_FAILURE_IMAGE",
		"session-entry-private-history",
		"Bearer private-provider-token",
	}
	failurePayload := strings.Join(secrets, " | ")
	tests := []struct {
		name  string
		lines []string
	}{
		{
			name: "assistant delta error",
			lines: []string{
				`{"type":"response","command":"prompt","success":true}`,
				`{"type":"message_update","assistantMessageEvent":{"type":"error","reason":` + quotedJSON(t, failurePayload) + `}}`,
				`{"type":"agent_settled"}`,
			},
		},
		{
			name: "terminal agent end errorMessage",
			lines: []string{
				`{"type":"response","command":"prompt","success":true}`,
				`{"type":"agent_end","messages":[{"role":"assistant","stopReason":"error","errorMessage":` + quotedJSON(t, failurePayload) + `}],"willRetry":false}`,
				`{"type":"agent_settled"}`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := newCaptureClient(strings.Join(append(tt.lines, ""), "\n"))

			_, err := client.Text(context.Background(), secrets[0])

			require.Error(t, err)
			assert.Contains(t, err.Error(), "piagent")
			assertOmitsSensitiveValues(t, err.Error(), secrets)
		})
	}
}

func TestStreamRetryStatusOmitsFailureMessage(t *testing.T) {
	// Given Pi's retry event repeats a secret-bearing provider failure string,
	// When the event is surfaced, then runtime status remains useful but contains
	// no raw failure payload.
	secrets := []string{
		"private retry prompt",
		"Bearer private-retry-token",
		"session-entry-retry-history",
	}
	failurePayload := strings.Join(secrets, " | ")
	script := strings.Join([]string{
		`{"type":"response","command":"prompt","success":true}`,
		`{"type":"auto_retry_start","errorMessage":` + quotedJSON(t, failurePayload) + `}`,
		`{"type":"agent_end","messages":[{"role":"assistant","stopReason":"stop"}],"willRetry":false}`,
		`{"type":"agent_settled"}`,
		`{"type":"response","command":"get_session_stats","success":true,"data":{}}`,
		"",
	}, "\n")
	client, _ := newCaptureClient(script)

	stream, err := client.Stream(context.Background(), secrets[0])
	require.NoError(t, err)
	var statuses []string
	for stream.Next() {
		if ev := stream.Event(); ev.Kind == EventRuntimeStatus {
			statuses = append(statuses, ev.Text)
		}
	}
	require.NoError(t, stream.Close(context.Background()))

	require.NotEmpty(t, statuses)
	assert.Contains(t, strings.Join(statuses, "\n"), "retry")
	assertOmitsSensitiveValues(t, strings.Join(statuses, "\n"), secrets)
}

func TestProcessAndCommandErrorsOmitPayloads(t *testing.T) {
	// Given process boundaries return errors containing command arguments,
	// prompts, credentials, and raw stderr, when pkg/piagent reports them, then
	// only stable operation/exit classifications remain.
	secrets := []string{
		"private system prompt: do not disclose",
		"private user prompt: inspect payroll",
		"private-env-token-value",
		"stderr contained private session entry",
	}

	t.Run("process start error", func(t *testing.T) {
		client := New(
			WithRPCProcessRunnerForTesting(privacyStartErrorRunner{}),
			WithSystemPrompt(secrets[0]),
			WithEnv(map[string]string{"PI_PRIVATE_TOKEN": secrets[2]}),
		)

		_, err := client.Stream(context.Background(), secrets[1])

		require.Error(t, err)
		assert.Contains(t, err.Error(), "start")
		assertOmitsSensitiveValues(t, err.Error(), secrets)
	})

	t.Run("command write error", func(t *testing.T) {
		proc := &rpcProcess{stdin: privacyWriteError{}}

		err := proc.writeJSON(map[string]any{
			"type": "prompt", "message": secrets[1], "images": []string{secrets[2]},
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "prompt")
		assertOmitsSensitiveValues(t, err.Error(), secrets)
	})

	t.Run("stdout scan error", func(t *testing.T) {
		proc := &rpcProcess{lines: privacyScanner{err: errors.New("read failed: " + strings.Join(secrets, " | "))}}

		err := processDeadOrScanError(proc)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "read")
		assertOmitsSensitiveValues(t, err.Error(), secrets)
	})

	t.Run("generic exit error and stderr", func(t *testing.T) {
		err := wrapExitError(
			errors.New("command failed: "+strings.Join(secrets[:3], " | ")),
			secrets[3]+" | Authorization: Bearer "+secrets[2],
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "process exited")
		assertOmitsSensitiveValues(t, err.Error(), secrets)
		var exitErr *ExitError
		require.ErrorAs(t, err, &exitErr)
		assertOmitsSensitiveValues(t, exitErr.Err.Error(), secrets)
		assert.Empty(t, exitErr.Stderr)
	})

	t.Run("missing session preserves identity only", func(t *testing.T) {
		err := wrapExitError(
			errors.New("exit status 1"),
			"No session found matching 'pi-native-gone'\n"+secrets[3]+"\nBearer "+secrets[2],
		)

		require.ErrorIs(t, err, ErrSessionNotFound)
		assert.Contains(t, err.Error(), "pi-native-gone")
		assertOmitsSensitiveValues(t, err.Error(), secrets)
	})
}

func TestStream_RawSinkSummarizesSessionEntriesWithoutPayloads(t *testing.T) {
	// Given get_entries responses contain historical prompts and image data,
	// When raw diagnostics are enabled for an anchor-tracked turn,
	// Then only a redacted command marker reaches the sink while sensitive payloads stay out.
	script := strings.Join([]string{
		`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"session-1"}}`,
		`{"id":"session-entries-before","type":"response","command":"get_entries","success":true,"data":{"entries":[{"type":"message","id":"before-leaf","parentId":null,"message":{"role":"user","content":"historical-secret-prompt","images":[{"data":"secret-image-data"}]}}],"leafId":"before-leaf"}}`,
		`{"type":"response","command":"prompt","success":true}`,
		`{"type":"agent_end","messages":[],"willRetry":false}`,
		`{"type":"agent_settled"}`,
		`{"id":"session-entries-after","type":"response","command":"get_entries","success":true,"data":{"entries":[{"type":"message","id":"before-leaf","parentId":null,"message":{"role":"user","content":"historical-secret-prompt"}},{"type":"message","id":"turn-user","parentId":"before-leaf","message":{"role":"user","content":"current-secret-prompt"}}],"leafId":"turn-user"}}`,
		`{"type":"response","command":"get_session_stats","success":true,"data":{}}`,
		"",
	}, "\n")
	client, _, _ := newSingleProcessCaptureClient(script)

	var mu sync.Mutex
	var got []string
	client.rawSink = func(line []byte) {
		mu.Lock()
		got = append(got, string(line))
		mu.Unlock()
	}

	stream, err := client.Stream(context.Background(), "current-secret-prompt", RunCaptureUserAnchor())
	require.NoError(t, err)
	for stream.Next() {
	}

	mu.Lock()
	joined := strings.Join(got, "\n")
	mu.Unlock()
	assert.Contains(t, joined, `"command":"get_entries"`)
	assert.Contains(t, joined, `"payload":"redacted"`)
	assert.NotContains(t, joined, `"leafId"`)
	assert.NotContains(t, joined, "historical-secret-prompt")
	assert.NotContains(t, joined, "current-secret-prompt")
	assert.NotContains(t, joined, "secret-image-data")
	assert.Contains(t, joined, `"command":"prompt"`)
	assert.Contains(t, joined, `"command":"get_session_stats"`)
}

func assertOmitsSensitiveValues(t *testing.T, got string, secrets []string) {
	t.Helper()
	for _, secret := range secrets {
		assert.NotContains(t, got, secret)
	}
}

func quotedJSON(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}

type privacyStartErrorRunner struct{}

func (privacyStartErrorRunner) Start(_ context.Context, opts procOptions) (processHandle, error) {
	systemPrompt := ""
	for i := range opts.Args {
		if opts.Args[i] == "--append-system-prompt" && i+1 < len(opts.Args) {
			systemPrompt = opts.Args[i+1]
			break
		}
	}
	privateToken := ""
	for _, item := range opts.Env {
		if strings.HasPrefix(item, "PI_PRIVATE_TOKEN=") {
			privateToken = strings.TrimPrefix(item, "PI_PRIVATE_TOKEN=")
			break
		}
	}
	return nil, fmt.Errorf("start command failed with %s and %s", systemPrompt, privateToken)
}

type privacyWriteError struct{}

func (privacyWriteError) Write(p []byte) (int, error) {
	return 0, fmt.Errorf("write failed for %s", p)
}

type privacyScanner struct {
	err error
}

func (privacyScanner) Scan() bool    { return false }
func (privacyScanner) Bytes() []byte { return nil }
func (privacyScanner) Text() string  { return "" }
func (s privacyScanner) Err() error  { return s.err }

package openclawgateway

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// Given an OpenClaw 2026.9.5 Gateway, question.request / question.resolve /
// question.get / question.list and the question.requested / question.resolved
// broadcasts all require operator.questions (core-descriptors.ts,
// server-broadcast.ts EVENT_SCOPE_GUARDS). When AgentRE connects, then it must
// request that scope and sign it into the device proof, or pending questions
// never reach the turn.
func TestClientConnectRequestsQuestionsScope(t *testing.T) {
	gatewayURL := newTestGateway(t, func(conn *websocket.Conn, _ int) {
		const nonce = "questions-nonce"
		writeChallenge(t, conn, nonce)
		connect := readTestRequest(t, conn)
		var params testConnectParams
		require.NoError(t, json.Unmarshal(connect.Params, &params))
		assert.Contains(t, params.Scopes, "operator.questions")

		publicKey, err := base64.RawURLEncoding.DecodeString(params.Device.PublicKey)
		require.NoError(t, err)
		signature, err := base64.RawURLEncoding.DecodeString(params.Device.Signature)
		require.NoError(t, err)
		payload := BuildDeviceAuthPayload(
			params.Device.ID, params.Client.ID, params.Client.Mode, params.Role,
			params.Scopes, params.Device.SignedAt, "", nonce, params.Client.Platform, "",
		)
		assert.True(t, ed25519.Verify(ed25519.PublicKey(publicKey), []byte(payload), signature),
			"the requested scopes must be the ones signed into the device proof")
		writeHello(t, conn, connect.ID, ProtocolVersion, params.Scopes, "questions-conn")
	})
	client, err := NewClient(Config{URL: gatewayURL, Identity: testIdentity(t), Platform: "linux"})
	require.NoError(t, err)
	defer client.Close()
	_, err = client.Start(context.Background())
	require.NoError(t, err)
}

// Given Debug logging is on, when a question.resolve carrying a secret answer
// goes out, its response echoes the canonical answers, and the Gateway
// broadcasts question.resolved with them, then the secret reaches the Gateway
// verbatim but none of the raw-frame debug lines carries it — the frames are
// still logged, only the answer values are redacted.
func TestClientDebugLogRedactsQuestionAnswers(t *testing.T) {
	const secret = "hunter2-very-secret"
	core, logs := observer.New(zapcore.DebugLevel)
	previous := logger.Default()
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { logger.SetLogger(previous) })

	received := make(chan json.RawMessage, 1)
	gatewayURL := newTestGateway(t, func(conn *websocket.Conn, _ int) {
		writeChallenge(t, conn, "redact-nonce")
		connect := readTestRequest(t, conn)
		writeHello(t, conn, connect.ID, ProtocolVersion, RequiredOperatorScopes, "redact-conn")
		resolve := readTestRequest(t, conn)
		require.Equal(t, "question.resolve", resolve.Method)
		received <- resolve.Params
		answers := map[string]any{"answers": map[string]any{"token": []string{secret}, "target": []string{"staging"}}}
		writeTestJSON(t, conn, map[string]any{
			"type": "res", "id": resolve.ID, "ok": true,
			"payload": map[string]any{"status": "answered", "answers": answers},
		})
		writeTestJSON(t, conn, map[string]any{
			"type": "event", "event": "question.resolved", "seq": 2,
			"payload": map[string]any{"id": "question-1", "status": "answered", "answers": answers},
		})
	})
	client, err := NewClient(Config{URL: gatewayURL, Identity: testIdentity(t), Platform: "linux"})
	require.NoError(t, err)
	defer client.Close()
	_, err = client.Start(context.Background())
	require.NoError(t, err)

	var out struct {
		Status string `json:"status"`
	}
	require.NoError(t, client.Call(context.Background(), "question.resolve", map[string]any{
		"id":      "question-1",
		"answers": map[string]any{"answers": map[string][]string{"token": {secret}, "target": {"staging"}}},
	}, &out))
	assert.Equal(t, "answered", out.Status)
	assert.Contains(t, string(<-received), secret, "the secret answer must still reach the Gateway")
	select {
	case event := <-client.Events():
		require.Equal(t, "question.resolved", event.Name)
		assert.Contains(t, string(event.Payload), secret, "the event payload handed to the runtime is not rewritten")
	case <-time.After(2 * time.Second):
		t.Fatal("question.resolved event not delivered")
	}

	rawFrames := 0
	for _, entry := range logs.All() {
		assert.NotContains(t, entry.Message, secret)
		for key, value := range entry.ContextMap() {
			text, _ := json.Marshal(value)
			assert.NotContains(t, string(text), secret, "log field %q leaked the secret answer", key)
			if key == "frame" {
				rawFrames++
			}
		}
	}
	// connect + hello + challenge + question.resolve req + res + event.
	assert.GreaterOrEqual(t, rawFrames, 6, "raw frames must still be logged, with answers redacted")
}

package accountpeer

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
)

// Given 一台没有桌面的机器上的 agrctl 与一个假账号 server + 假 relay + 假 agentred
// When 依次 login → send（异步）→ wait → pending → answer → wait → messages
// Then 整条链路按 CLI 契约（stdout、退出码 0/4）走通，且全程不碰桌面与 agrctl 的隔离目录之外。
func TestHeadlessAgrctlAccountPeerSmoke(t *testing.T) {
	w := startWorld(t)
	cli := newCLI(t, w)

	// login：Device Flow 由假 server 自动批准。
	login := cli.run(t, false, "login", "--server", w.ServerURL())
	require.Equal(t, 0, login.code, login.stderr)
	assert.Contains(t, login.stdout, "logged in as alice@example.com")
	assert.Contains(t, login.stderr, "WXYZ-1234", "login prints the user code to approve")
	assert.True(t, cli.credentialsFileIs0600(t), "credentials are stored 0600 under the isolated data dir")

	// send（异步）：stdout 只有会话 UUID，agrctl 立即退出，这一轮在设备上继续。
	send := cli.run(t, true, "send", "--agent", "reviewer", "ask: which flavor?")
	require.Equal(t, 0, send.code, send.stderr)
	id := strings.TrimSpace(send.stdout)
	require.NoError(t, conversationid.Validate(id), "stdout is the minted conversation id, got %q", send.stdout)
	assert.Contains(t, send.stderr, "dispatched to reviewer — build-box")

	// wait：fake 后端提了一个问题 → 退出码 4，stdout 是待处理项 JSON。
	waiting := cli.run(t, false, "wait", id)
	require.Equal(t, 4, waiting.code, waiting.stderr)
	assert.Contains(t, waiting.stdout, "ask-1")

	// pending：-o json 列出这个提问与选项。
	pending := cli.run(t, false, "pending", id, "-o", "json")
	require.Equal(t, 0, pending.code, pending.stderr)
	var listed struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(pending.stdout), &listed), pending.stdout)
	require.Len(t, listed.Items, 1)
	item := listed.Items[0]
	assert.Equal(t, "ask-1", item["id"])
	assert.Equal(t, "question", item["kind"])
	assert.Equal(t, id, item["session"])
	assert.Contains(t, pending.stdout, "Which flavor?")

	// answer：按选项文本回答。
	answer := cli.run(t, true, "answer", id, "ask-1", "--option", "Vanilla")
	require.Equal(t, 0, answer.code, answer.stderr)
	assert.Equal(t, "answered\n", answer.stdout)
	assert.Equal(t, []string{"Vanilla"}, w.Agentred.AnsweredLabels())

	// wait：回答之后这一轮收尾，退出码 0，stdout 是最终文本。
	done := cli.run(t, false, "wait", id)
	require.Equal(t, 0, done.code, done.stderr)
	assert.Equal(t, "You chose Vanilla.\n", done.stdout)

	// messages：用户那句与助手最终文本。
	messages := cli.run(t, false, "messages", id)
	require.Equal(t, 0, messages.code, messages.stderr)
	assert.Contains(t, messages.stdout, "ask: which flavor?")
	assert.Contains(t, messages.stdout, "You chose Vanilla.")
}

package openclaw

import (
	"context"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/openclawgateway"
)

const (
	// chatSendMethod 是开轮用的 RPC:网关只在这条路径上把发起者的设备记成审批
	// reviewer,agent 方法不绑,于是本设备看不到自己这一轮触发的 exec 审批。
	chatSendMethod = "chat.send"
	// sessionSubscribeMethod 让本连接收到该会话的 agent/chat 事件。chat.send 的轮次
	// 不广播事件,只发给订阅者。两者都不属于 requiredRuntimeMethods,要按广播判断。
	sessionSubscribeMethod = "sessions.messages.subscribe"
)

// subscribeSessionMessages 订阅会话消息流,并返回网关规范化后的会话 key
// (agent:<agentId>:<key>)。用「请求 key」订阅即可 —— 规范化由网关完成并在应答里
// 回报,后续 chat.send 必须用这个规范 key:带 agentId 时它与非规范 key 会被判冲突
// (INVALID_REQUEST: agentId "main" does not match session key ...)。
func subscribeSessionMessages(ctx context.Context, client *openclawgateway.Client, sessionKey, agentID string) (string, error) {
	params := map[string]any{"key": sessionKey}
	if agentID != "" {
		params["agentId"] = agentID
	}
	var out struct {
		Subscribed bool   `json:"subscribed"`
		Key        string `json:"key"`
	}
	if err := client.Call(ctx, sessionSubscribeMethod, params, &out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Key), nil
}

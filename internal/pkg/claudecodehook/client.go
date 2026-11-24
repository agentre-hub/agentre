// Package claudecodehook implements the agrctl claudecode PostToolUse hook
// handler. Each invocation is one short-lived process spawned by the claude
// CLI; it reads the hook payload from stdin, fetches pending Steer messages
// from the agentre httpgateway (env-provided URL+token), and emits the
// appropriate hook output JSON to stdout.
package claudecodehook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// hookPayload subsets the JSON claude writes to the hook stdin. Only the
// fields we read are decoded; ignoring everything else keeps us resilient to
// CLI schema growth.
//
// AgentID 仅在 hook 被 subagent (Task tool) 内层工具触发时非空 —— 主 agent
// 自己的工具(含调 Task tool 本身)payload 里都没这两个字段。用它把 drain
// 限制到主 agent 边界:见 RunPostTool 的早退判断。
//
// Cwd 是工具跑完之后 CLI 所在的目录(Bash `cd` 之后就是新目录),ToolUseID 是这次
// 调用的 id;两者随收件箱请求报给 gateway,盖到这次调用的工具事件上。老 CLI 不报
// 时为空,请求不带它们。
type hookPayload struct {
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`
	Cwd       string `json:"cwd"`
	ToolUseID string `json:"tool_use_id"`
}

// inboxQuery 是收件箱请求的查询参数。AgentID 非空时 gateway 只记位置、不 drain。
type inboxQuery struct {
	SessionID string
	ToolUseID string
	Cwd       string
	AgentID   string
}

func parsePayload(r io.Reader) (hookPayload, error) {
	var p hookPayload
	dec := json.NewDecoder(r)
	dec.UseNumber()
	if err := dec.Decode(&p); err != nil {
		return hookPayload{}, fmt.Errorf("claudecodehook: decode payload: %w", err)
	}
	return p, nil
}

// fetchInbox calls GET <base>/hook/v1/inbox?session_id=<sid>[&tool_use_id=…&cwd=…&agent_id=…]
// with bearer auth. Empty optional fields are omitted. Returns the message slice
// (possibly empty) or an error.
func fetchInbox(ctx context.Context, base, token string, in inboxQuery) ([]string, error) {
	u, err := url.Parse(strings.TrimRight(base, "/") + "/hook/v1/inbox")
	if err != nil {
		return nil, fmt.Errorf("claudecodehook: parse base url: %w", err)
	}
	q := u.Query()
	q.Set("session_id", in.SessionID)
	for k, v := range map[string]string{"tool_use_id": in.ToolUseID, "cwd": in.Cwd, "agent_id": in.AgentID} {
		if v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("claudecodehook: GET inbox: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("claudecodehook: inbox status %d", resp.StatusCode)
	}
	var body struct {
		Messages []string `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("claudecodehook: decode inbox: %w", err)
	}
	return body.Messages, nil
}

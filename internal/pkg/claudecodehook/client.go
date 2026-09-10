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
type hookPayload struct {
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`
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

// fetchInbox calls GET <base>/hook/v1/inbox?session_id=<sid> with bearer
// auth. Returns the message slice (possibly empty) or an error.
func fetchInbox(ctx context.Context, base, token, sid string) ([]string, error) {
	u, err := url.Parse(strings.TrimRight(base, "/") + "/hook/v1/inbox")
	if err != nil {
		return nil, fmt.Errorf("claudecodehook: parse base url: %w", err)
	}
	q := u.Query()
	q.Set("session_id", sid)
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

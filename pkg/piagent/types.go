package piagent

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
)

type rpcResponse struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Command string          `json:"command,omitempty"`
	Success bool            `json:"success"`
	Error   string          `json:"error,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type rpcEvent struct {
	Type                  string          `json:"type"`
	Message               json.RawMessage `json:"message,omitempty"`
	Messages              json.RawMessage `json:"messages,omitempty"`
	AssistantMessageEvent assistantDelta  `json:"assistantMessageEvent,omitempty"`
	ID                    string          `json:"id,omitempty"`
	Method                string          `json:"method,omitempty"`
	ToolCallID            string          `json:"toolCallId,omitempty"`
	ToolName              string          `json:"toolName,omitempty"`
	Args                  json.RawMessage `json:"args,omitempty"`
	PartialResult         json.RawMessage `json:"partialResult,omitempty"`
	Result                json.RawMessage `json:"result,omitempty"`
	IsError               bool            `json:"isError,omitempty"`
	Reason                string          `json:"reason,omitempty"`
	ErrorMessage          string          `json:"errorMessage,omitempty"`
}

type assistantDelta struct {
	Type     string          `json:"type,omitempty"`
	Delta    string          `json:"delta,omitempty"`
	Content  string          `json:"content,omitempty"`
	Reason   string          `json:"reason,omitempty"`
	Partial  json.RawMessage `json:"partial,omitempty"`
	ToolCall json.RawMessage `json:"toolCall,omitempty"`
}

type assistantMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	Provider     string          `json:"provider"`
	Model        string          `json:"model"`
	Usage        *usageWire      `json:"usage"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage,omitempty"`
	// ResponseID 是一次 provider 响应的稳定身份(msg_2026…)。agent_end 会把本轮
	// 每条 assistant 消息连同 usage 原样重发一遍,靠它认出「这条记过了」。
	ResponseID string `json:"responseId,omitempty"`
	// Timestamp 是 responseId 缺省时(老 pi / 某些 provider)的兜底身份材料。
	Timestamp int64 `json:"timestamp,omitempty"`
}

type usageWire struct {
	Input      int       `json:"input"`
	Output     int       `json:"output"`
	CacheRead  int       `json:"cacheRead"`
	CacheWrite int       `json:"cacheWrite"`
	Cost       *costWire `json:"cost,omitempty"`
}

type sessionStateWire struct {
	SessionID   string            `json:"sessionId"`
	SessionFile string            `json:"sessionFile,omitempty"`
	Model       *sessionModelWire `json:"model,omitempty"`
}

type sessionModelWire struct {
	ContextWindow int `json:"contextWindow"`
}

type forkResultWire struct {
	Canceled *bool `json:"cancelled"` //nolint:misspell // Pi RPC names this protocol field "cancelled".
}

type sessionEntriesWire struct {
	Entries []sessionEntryWire `json:"entries"`
	LeafID  string             `json:"leafId"`
}

type sessionEntryWire struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	ParentID string `json:"parentId"`
	Message  struct {
		Role string `json:"role"`
	} `json:"message"`
}

type sessionStatsWire struct {
	ContextUsage *contextUsageWire `json:"contextUsage,omitempty"`
}

type contextUsageWire struct {
	ContextWindow int `json:"contextWindow"`
}

type costWire struct {
	Total float64 `json:"total"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func buildEnv(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := os.Environ()
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

func buildRPCArgs(c *Client) []string {
	args := []string{"--mode", "rpc"}
	if c.noSession {
		args = append(args, "--no-session")
	}
	if strings.TrimSpace(c.sessionDir) != "" {
		args = append(args, "--session-dir", strings.TrimSpace(c.sessionDir))
	}
	if strings.TrimSpace(c.session) != "" {
		args = append(args, "--session", strings.TrimSpace(c.session))
	}
	if strings.TrimSpace(c.systemPrompt) != "" {
		args = append(args, "--append-system-prompt", strings.TrimSpace(c.systemPrompt))
	}
	if strings.TrimSpace(c.model) != "" {
		args = append(args, "--model", strings.TrimSpace(c.model))
	}
	if thinking := NormalizeThinkingLevel(c.thinking); thinking != "" {
		args = append(args, "--thinking", thinking)
	}
	for _, ext := range c.extensions {
		args = append(args, "--extension", ext)
	}
	return args
}

// NormalizeThinkingLevel 把落库的 reasoning_effort 映射为 pi CLI 的 --thinking 值。
// 六档（low/medium/high/xhigh/max）原样透传:本机 `pi --help` 明写 --thinking 支持
// off/minimal/low/medium/high/xhigh/max(spec 2026-09-01「三后端下发档位的收敛」),
// 旧的 max→xhigh 降档前提已不成立。非法值(含大小写错、含空格)→ "" 表示不下发,走
// pi 自身默认。
func NormalizeThinkingLevel(level string) string {
	switch strings.TrimSpace(level) {
	case "low", "medium", "high", "xhigh", "max":
		return strings.TrimSpace(level)
	default:
		return ""
	}
}

func cloneMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Package openclawgateway implements the public OpenClaw Gateway WebSocket
// protocol used by external operator clients. It intentionally has no service,
// repository, Wails, or agentruntime dependencies.
package openclawgateway

import (
	"encoding/json"
	"errors"
	"fmt"
)

const ProtocolVersion = 4

var RequiredOperatorScopes = []string{
	"operator.read",
	"operator.write",
	"operator.approvals",
	// operator.questions gates question.* RPCs and the question.requested /
	// question.resolved broadcasts; without it an ask_user question never
	// reaches the turn. A device paired with fewer scopes gets PAIRING_REQUIRED
	// (reason scope-upgrade) until the operator approves the upgrade.
	"operator.questions",
}

var (
	ErrDisconnected         = errors.New("openclaw gateway disconnected")
	ErrProtocolMismatch     = errors.New("openclaw gateway protocol mismatch")
	ErrRequiredScopeMissing = errors.New("openclaw gateway required scope missing")
)

type Config struct {
	URL           string
	Token         string
	Identity      *DeviceIdentity
	ClientVersion string
	Platform      string
}

type Hello struct {
	Type     string `json:"type"`
	Protocol int    `json:"protocol"`
	Server   struct {
		Version string `json:"version"`
	} `json:"server"`
	Features struct {
		Methods []string `json:"methods"`
		Events  []string `json:"events"`
	} `json:"features"`
	Auth struct {
		Scopes []string `json:"scopes"`
	} `json:"auth"`
}

type Event struct {
	Name    string
	Payload json.RawMessage
}

type EventGap struct{}

// RPCError is the structured Gateway response error. Message is sanitized by
// the client before it crosses the package boundary.
type RPCError struct {
	Code         string
	Reason       string
	Message      string
	Retryable    bool
	RetryAfterMs int64
}

func (e *RPCError) Error() string {
	if e == nil {
		return "openclaw gateway RPC error"
	}
	if e.Message == "" {
		return fmt.Sprintf("openclaw gateway RPC %s", e.Code)
	}
	return fmt.Sprintf("openclaw gateway RPC %s: %s", e.Code, e.Message)
}

type requestFrame struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type responseError struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	Retryable    bool   `json:"retryable,omitempty"`
	RetryAfterMs int64  `json:"retryAfterMs,omitempty"`
	Details      struct {
		Reason string `json:"reason,omitempty"`
	} `json:"details,omitempty"`
}

type gatewayFrame struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	OK      bool            `json:"ok,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   *responseError  `json:"error,omitempty"`
	Event   string          `json:"event,omitempty"`
	Seq     int64           `json:"seq,omitempty"`
}

type pendingResponse struct {
	payload json.RawMessage
	err     error
}

package protocol

// TerminalOpenParams is the terminal.open RPC request. TerminalID is optional
// for legacy clients; when supplied, the daemon claims and returns that identity.
type TerminalOpenParams struct {
	TerminalID string   `json:"terminalId,omitempty"`
	SessionID  int64    `json:"sessionId"`
	Cwd        string   `json:"cwd"`
	Shell      string   `json:"shell,omitempty"`
	Command    string   `json:"command,omitempty"`
	Env        []string `json:"env,omitempty"`
	Cols       uint16   `json:"cols"`
	Rows       uint16   `json:"rows"`
}

// TerminalOpenResult returns the claimed PTY id which the desktop uses
// opaquely for subsequent write/resize/close calls.
type TerminalOpenResult struct {
	TerminalID string `json:"terminalId"`
}

type TerminalWriteParams struct {
	TerminalID string `json:"terminalId"`
	Data       string `json:"data"`
}

type TerminalResizeParams struct {
	TerminalID string `json:"terminalId"`
	Cols       uint16 `json:"cols"`
	Rows       uint16 `json:"rows"`
}

type TerminalCloseParams struct {
	TerminalID        string `json:"terminalId"`
	CancelPendingOpen bool   `json:"cancelPendingOpen,omitempty"`
}

// TerminalDataEvent is the daemon→client push for raw stdout chunks.
type TerminalDataEvent struct {
	TerminalID string `json:"terminalId"`
	Data       []byte `json:"data"`
}

// TerminalExitEvent — Reason is one of:
// "natural" | "killed" | "connection_lost" | "daemon_shutdown" | "error"
type TerminalExitEvent struct {
	TerminalID string `json:"terminalId"`
	Code       int    `json:"code"`
	Reason     string `json:"reason"`
	Msg        string `json:"msg,omitempty"`
}

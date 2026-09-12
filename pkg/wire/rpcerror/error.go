// Package rpcerror defines transport-neutral structured RPC failures.
package rpcerror

const (
	CodeMethodNotFound  int32 = -32601
	CodeInvalidParams   int32 = -32602
	CodeInternal        int32 = -32603
	CodeCanceled        int32 = -32800
	CodeUnauthorized    int32 = -32001
	CodeSessionMissing  int32 = -32002
	CodeProviderMissing int32 = -32003
	CodePairing         int32 = -32004
	CodeShuttingDown    int32 = -32005
	// CodeProtocolVersion is returned by a handshake handler whose peer
	// advertised a wire protocol version it does not accept.
	CodeProtocolVersion int32 = -32006
	// CodeAccountServerUnreachable is returned by auth.account (auth.direct
	// never contacts the account server) when the responder could not reach
	// the account server (or got no answer) to verify the caller's
	// credential, and no cached success
	// covers it. It is deliberately distinct from CodeUnauthorized: a caller
	// that sees -32001 refreshes its own credential and retries, which is
	// pointless when the credential was never actually rejected — the
	// account server was simply unreachable. Callers must treat this code as
	// retryable, not as "this device will never work" (spec
	// 2026-09-11-opaque-credentials-auto-direct H3).
	CodeAccountServerUnreachable int32 = -32007
)

// Error is the stable failure shape shared by binary Protobuf RPC adapters.
// Details contains method-specific Protobuf bytes when a contract defines it.
type Error struct {
	Code    int32
	Message string
	Details []byte
}

func (e *Error) Error() string { return e.Message }

var (
	ErrMethodNotFound           = &Error{Code: CodeMethodNotFound, Message: "Method not found"}
	ErrInvalidParams            = &Error{Code: CodeInvalidParams, Message: "Invalid params"}
	ErrInternal                 = &Error{Code: CodeInternal, Message: "Internal error"}
	ErrUnauthorized             = &Error{Code: CodeUnauthorized, Message: "Unauthorized"}
	ErrSessionNotFound          = &Error{Code: CodeSessionMissing, Message: "Session not found"}
	ErrProviderMissing          = &Error{Code: CodeProviderMissing, Message: "LLM provider not configured"}
	ErrPairing                  = &Error{Code: CodePairing, Message: "Pairing code invalid / expired / rate-limited"}
	ErrShuttingDown             = &Error{Code: CodeShuttingDown, Message: "Daemon shutting down"}
	ErrAccountServerUnreachable = &Error{Code: CodeAccountServerUnreachable, Message: "Account server unreachable"}
)

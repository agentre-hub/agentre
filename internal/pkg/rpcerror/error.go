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
	ErrMethodNotFound  = &Error{Code: CodeMethodNotFound, Message: "Method not found"}
	ErrInvalidParams   = &Error{Code: CodeInvalidParams, Message: "Invalid params"}
	ErrInternal        = &Error{Code: CodeInternal, Message: "Internal error"}
	ErrUnauthorized    = &Error{Code: CodeUnauthorized, Message: "Unauthorized"}
	ErrSessionNotFound = &Error{Code: CodeSessionMissing, Message: "Session not found"}
	ErrProviderMissing = &Error{Code: CodeProviderMissing, Message: "LLM provider not configured"}
	ErrPairing         = &Error{Code: CodePairing, Message: "Pairing code invalid / expired / rate-limited"}
	ErrShuttingDown    = &Error{Code: CodeShuttingDown, Message: "Daemon shutting down"}
)

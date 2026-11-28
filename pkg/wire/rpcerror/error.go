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
	// CodeScopeDenied refuses a call the caller is authenticated for but not
	// permitted to make: a personal token without the scope the method needs,
	// or acting on a session it did not start. Distinct from CodeUnauthorized
	// for the same reason as CodeAccountServerUnreachable: the credential was
	// accepted, so refreshing it and retrying can never succeed.
	CodeScopeDenied int32 = -32005
	// CodeProviderLeaseUnavailable is returned by an agentred that needs a
	// provider's key (a turn, a provider test or a model discovery) but holds
	// no valid lease for it and could not obtain one: the lease expired and the
	// account server is unreachable (or does not issue leases yet), and the
	// caller attached none. Distinct from CodeProviderMissing — the provider is
	// configured; retrying once the account server is reachable succeeds.
	CodeProviderLeaseUnavailable int32 = -32008
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
	ErrAccountServerUnreachable = &Error{Code: CodeAccountServerUnreachable, Message: "Account server unreachable"}
	ErrProviderLeaseUnavailable = &Error{Code: CodeProviderLeaseUnavailable, Message: "provider credential lease expired and the account server is unreachable"}
)

// ErrScopeDenied refuses a call for the scope it needs, naming that scope.
func ErrScopeDenied(scope string) *Error {
	return &Error{Code: CodeScopeDenied, Message: "token lacks scope " + scope}
}

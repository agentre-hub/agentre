// Package wireversion carries the single agentre ↔ agentred wire protocol
// version this build speaks.
//
// The version's owner is `frontend/packages/agentre-wire/package.json`: that
// package publishes the schema, the generated messages and the codecs, so its
// release number is what "the protocol" means. Go cannot read package.json at
// build time, so the value is restated here and pinned by the guard test in
// wireversion_test.go — change one without the other and the build goes red.
package wireversion

import "fmt"

// Protocol is the wire protocol version advertised in every handshake.
//
// Keep it byte identical to the `version` field of
// frontend/packages/agentre-wire/package.json.
const Protocol = "0.3.0"

// Match reports whether a peer-reported protocol version is one this build
// accepts.
//
// Today that is an exact match, because agentre is unreleased and carries no
// compatibility burden. The handshake still transports the version as a free
// string rather than a bool or a closed enum, so introducing an N-1 window
// later (by reading the peer's min_supported_protocol_version) only widens
// this predicate — it does not break the protocol a second time.
//
// A peer that reports the empty string predates protocol versioning and is not
// accepted: proto3 gives an absent string field the same zero value as an
// explicitly empty one, so treating "" as "same version" would silently let
// exactly the peers this check exists for through.
func Match(peer string) bool { return peer != "" && peer == Protocol }

// Reject explains why a peer-reported protocol version is not accepted, or
// returns "" when it is. Both sides of a handshake render the same sentence
// from here: the desktop wraps it in its own sentinel, the daemon puts it on
// the wire as rpcerror.CodeProtocolVersion.
func Reject(peer string) string {
	if Match(peer) {
		return ""
	}
	if peer == "" {
		return fmt.Sprintf("peer reported no protocol version (it predates protocol versioning); this build speaks %s", Protocol)
	}
	return fmt.Sprintf("peer speaks protocol version %s, this build speaks %s", peer, Protocol)
}

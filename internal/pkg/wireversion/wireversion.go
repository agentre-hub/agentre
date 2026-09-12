// Package wireversion carries the agentre ↔ agentred wire protocol version
// this build speaks, and decides whether a peer's version is the same one.
//
// The version number itself is not this package's to choose: it belongs to the
// protocol module github.com/agentre-hub/agentre/pkg/wire, where it is
// declared on the schema itself; Protocol below simply reads it. What lives
// here is the host's side of the handshake — the comparison, and the sentence
// a rejected peer is told.
package wireversion

import (
	"fmt"

	"github.com/agentre-hub/agentre/pkg/wire/protocolversion"
)

// Protocol is the wire protocol version this build speaks and advertises in
// every handshake.
//
// It is not a value this package owns: the protocol declares its own version
// on the schema, as the (agentre.wire.protocol_version) file option, and this
// is the protocol module's reading of it. There is nothing here to keep in
// sync — bump the option in pkg/wire/proto/agentre/wire/wire.proto, regenerate,
// and every consumer that imports the module moves with it.
var Protocol = protocolversion.Protocol()

// MinSupported is the oldest peer protocol version this build accepts, which
// is its own: compatibility is exact equality, so there is no older version to
// accept.
//
// It exists because the handshake messages carry a
// min_supported_protocol_version field that hosts on both ends fill in, and
// this is what this build puts there. It no longer participates in any
// decision — see Match — so it is written out here as the release's own number
// rather than read from the protocol module: the same schema can be spoken by
// a host that only accepts its own release and by one that keeps a lower
// floor, and this build is the former.
const MinSupported = "0.1.0"

// Match reports whether a peer speaks this build's protocol version.
//
// The comparison is equality and nothing else. The desktop and agentred are
// two independently deployed binaries, so version skew is a routine failure
// mode rather than something to negotiate around: within one release both ends
// carry the same generated schema, and across releases neither end can read
// the other's conventions off the frames, so an older peer is turned away at
// the handshake instead of being given a downgrade branch.
//
// Only the peer's protocol_version is examined; its advertised
// min_supported_protocol_version is the peer's own statement of what it
// accepts, and under exact equality the check is symmetric — the peer runs the
// same comparison against us and reaches the same verdict, so reading its
// floor here could only ever duplicate that answer.
//
// proto3 gives an absent string field the same zero value as an explicitly
// empty one, so a peer that leaves the field out is indistinguishable from one
// that sends an empty string; both differ from Protocol and are rejected. This
// is why the check cannot be written as `peer != "" && peer != Protocol`.
func Match(peerProtocol string) bool { return peerProtocol == Protocol }

// Reject explains why a peer-reported protocol version is not accepted, or
// returns "" when it is. Both sides of a handshake render the same sentence
// from here: the desktop wraps it in its own sentinel, the daemon puts it on
// the wire as rpcerror.CodeProtocolVersion. The message always carries both
// versions, so whichever side reads it can tell which release to look at.
func Reject(peerProtocol string) string {
	if Match(peerProtocol) {
		return ""
	}
	return fmt.Sprintf("peer speaks wire protocol version %q, this build speaks %q; both ends must run the same release", peerProtocol, Protocol)
}

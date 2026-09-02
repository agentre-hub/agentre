// Package wireversion carries the agentre ↔ agentred wire protocol version
// window this build speaks and accepts.
//
// The version's owner is `frontend/packages/agentre-wire/package.json`: that
// package publishes the schema, the generated messages and the codecs, so its
// release number is what "the protocol" means. Go cannot read package.json at
// build time, so the value is restated here and pinned by the guard tests in
// wireversion_test.go — change one without the other and the build goes red.
package wireversion

import (
	"fmt"
	"strconv"
	"strings"
)

// Protocol is the wire protocol version this build speaks and advertises in
// every handshake.
//
// Keep it byte identical to the `version` field of
// frontend/packages/agentre-wire/package.json.
const Protocol = "0.2.0"

// MinSupported is the oldest peer protocol version this build still accepts.
//
// Together with Protocol it defines the [MinSupported, Protocol] window a
// handshake checks the peer against — see Match. This round MinSupported is
// pinned equal to Protocol (the window is a single point): widening it later
// is only safe the moment the method set (agentrewire.RpcMethod) has not
// changed since the last time MinSupported was reset to Protocol, a
// conservation law enforced by
// internal/pkg/wireversion/methodset_test.go's
// TestMethodSet_GivenTheMethodSetDigestWasLastUpdated_....
//
// Keep it byte identical to the `version` field of
// frontend/packages/agentre-wire/package.json, exactly like Protocol.
const MinSupported = "0.2.0"

// version is a parsed MAJOR.MINOR.PATCH triple. Handshake versions in this
// protocol are never pre-release or build-metadata strings, so a minimal
// three-integer comparison — rather than pulling in a full semver dependency
// — is all a window comparison needs.
type version [3]int

func parseVersion(s string) (version, bool) {
	var v version
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return version{}, false
		}
		v[i] = n
	}
	return v, true
}

// compare returns -1, 0 or 1 as a is less than, equal to or greater than b.
func (a version) compare(b version) int {
	for i := range a {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return 0
}

// atLeast reports whether v is at or above floor.
func atLeast(floor, v version) bool { return floor.compare(v) <= 0 }

// windowMatch is the pure predicate behind Match.
//
// A window has a floor (MinSupported) but deliberately no ceiling: a build's
// own Protocol names the release it happens to be, not a hard upper bound on
// what it can talk to. Within one method-set generation (see the
// conservation law on MinSupported), a newer peer only ever adds fields an
// older peer's protobuf decoder silently ignores, so there is nothing to gate
// on above the floor. The two-sided check is therefore:
//
//  1. the peer's Protocol must not be older than this build's MinSupported
//     (this build still speaks that generation), and
//  2. this build's Protocol must not be older than the peer's MinSupported
//     (the peer still speaks this build's generation).
//
// This is also the only shape under which two different Protocol values can
// ever match at all: a variant that additionally capped each side at its own
// Protocol as an upper bound would require peerProtocol <= selfProtocol AND
// selfProtocol <= peerProtocol simultaneously, which collapses to exact
// equality and defeats the window entirely.
//
// A peer that reports the empty string for its protocol version predates
// protocol versioning and is never accepted: proto3 gives an absent string
// field the same zero value as an explicitly empty one, so treating "" as "in
// range" would silently let exactly the peers this check exists for through.
//
// A peer that reports a protocol version but omits
// min_supported_protocol_version predates the windowing mechanism itself
// (this round is the one introducing the field). It is treated as
// advertising a floor at its own protocol version, the conservative reading:
// it does not widen what such a peer is assumed to still support.
func windowMatch(selfProtocol, selfMinSupported, peerProtocol, peerMinSupported string) bool {
	peerVersion, ok := parseVersion(peerProtocol)
	if !ok {
		return false
	}
	selfVersion, ok := parseVersion(selfProtocol)
	if !ok {
		return false
	}
	selfMin, ok := parseVersion(selfMinSupported)
	if !ok {
		return false
	}
	if !atLeast(selfMin, peerVersion) {
		return false
	}

	peerMin, ok := parseVersion(peerMinSupported)
	if !ok {
		// Peer omitted its window: fall back to a floor at its own protocol
		// version rather than assuming it tolerates anything.
		peerMin = peerVersion
	}
	return atLeast(peerMin, selfVersion)
}

// Match reports whether a peer-reported protocol version and minimum
// supported version are compatible with this build: the peer's Protocol must
// not be older than this build's MinSupported, and this build's Protocol must
// not be older than the peer's reported minimum supported version. See
// windowMatch for why the check has no upper bound.
func Match(peerProtocol, peerMinSupported string) bool {
	return windowMatch(Protocol, MinSupported, peerProtocol, peerMinSupported)
}

// Reject explains why a peer-reported protocol version is not accepted, or
// returns "" when it is. Both sides of a handshake render the same sentence
// from here: the desktop wraps it in its own sentinel, the daemon puts it on
// the wire as rpcerror.CodeProtocolVersion. The message always carries both
// the peer's reported version and this build's own window, so whichever side
// reads it can tell which release to look at.
func Reject(peerProtocol, peerMinSupported string) string {
	if Match(peerProtocol, peerMinSupported) {
		return ""
	}
	if peerProtocol == "" {
		return fmt.Sprintf("peer reported no protocol version (it predates protocol versioning); this build accepts protocol versions %s to %s", MinSupported, Protocol)
	}
	return fmt.Sprintf("peer speaks protocol version %s, this build accepts protocol versions %s to %s", peerProtocol, MinSupported, Protocol)
}

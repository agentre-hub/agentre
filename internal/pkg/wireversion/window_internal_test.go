package wireversion

import "testing"

// Given two builds each advertise a window [minSupported, protocol], When
// their windows are checked against each other, Then the handshake matches
// exactly when each side's Protocol falls inside the other side's window —
// this is windowMatch's whole contract, and it is exercised here with
// synthetic self/peer values because the package's own Protocol/MinSupported
// constants are pinned equal to each other this round (see
// docs/specs/2026-08-31-conversation-centric-addressing.md "协议版本窗口"),
// so the live constants alone cannot exhibit a genuine (non-degenerate)
// window.
func TestWindowMatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                           string
		selfProtocol, selfMinSupported string
		peerProtocol, peerMinSupported string
		want                           bool
	}{
		{
			name:         "identical versions on both sides match",
			selfProtocol: "0.1.0", selfMinSupported: "0.1.0",
			peerProtocol: "0.1.0", peerMinSupported: "0.1.0",
			want: true,
		},
		{
			name:         "different versions each at or above the other's floor match",
			selfProtocol: "0.3.0", selfMinSupported: "0.1.0",
			peerProtocol: "0.2.0", peerMinSupported: "0.1.0",
			want: true,
		},
		{
			name:         "peer newer than self but self is still at or above peer's floor matches",
			selfProtocol: "0.1.0", selfMinSupported: "0.1.0",
			peerProtocol: "0.2.0", peerMinSupported: "0.1.0",
			want: true,
		},
		{
			name:         "peer protocol falls below self's floor rejects",
			selfProtocol: "0.3.0", selfMinSupported: "0.2.0",
			peerProtocol: "0.1.0", peerMinSupported: "0.1.0",
			want: false,
		},
		{
			name:         "self protocol falls below peer's floor rejects",
			selfProtocol: "0.1.0", selfMinSupported: "0.1.0",
			peerProtocol: "0.5.0", peerMinSupported: "0.2.0",
			want: false,
		},
		{
			name:         "peer that predates windowing (empty min) is treated as a floor at its own protocol; a newer self still matches",
			selfProtocol: "0.3.0", selfMinSupported: "0.1.0",
			peerProtocol: "0.2.0", peerMinSupported: "",
			want: true,
		},
		{
			name:         "peer that predates windowing (empty min) rejects a self older than its own protocol",
			selfProtocol: "0.1.0", selfMinSupported: "0.1.0",
			peerProtocol: "0.2.0", peerMinSupported: "",
			want: false,
		},
		{
			name:         "peer that predates protocol versioning entirely (empty protocol) always rejects",
			selfProtocol: "0.1.0", selfMinSupported: "0.1.0",
			peerProtocol: "", peerMinSupported: "",
			want: false,
		},
		{
			name:         "malformed peer protocol rejects",
			selfProtocol: "0.1.0", selfMinSupported: "0.1.0",
			peerProtocol: "not-a-version", peerMinSupported: "0.1.0",
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := windowMatch(tc.selfProtocol, tc.selfMinSupported, tc.peerProtocol, tc.peerMinSupported)
			if got != tc.want {
				t.Errorf("windowMatch(%q,%q,%q,%q) = %v, want %v",
					tc.selfProtocol, tc.selfMinSupported, tc.peerProtocol, tc.peerMinSupported, got, tc.want)
			}
		})
	}
}

package ctlaccount

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInitiatorOf(t *testing.T) {
	devices := []accountDevice{
		{Name: "mac", DisplayName: "work mac", Kind: "desktop", Fingerprint: "sha256:desk"},
		{Name: "box", Kind: "agentred", Fingerprint: "sha256:box"},
		{Name: "ci", Kind: "cli", Fingerprint: "sha256:ci"},
	}
	c := &Client{opts: Options{
		SelfFingerprint: func(context.Context) (string, error) { return "sha256:ci", nil },
		ConsolePeer:     func(context.Context) (string, error) { return "sha256:console", nil },
		TokenName: func(_ context.Context, peer string) (string, error) {
			if peer == "pat:abc" {
				return "nightly", nil
			}
			return "", nil
		},
	}}
	cases := []struct{ name, peer, label, kind string }{
		{"empty is the caller", "", "this agrctl (ci)", "cli"},
		{"console", "sha256:console", "console", "console"},
		{"desktop device", "sha256:desk", "work mac", "desktop"},
		{"agentred device", "sha256:box", "box", "agentred"},
		{"a token whose name is visible", "pat:abc", "nightly", "token"},
		{"a token whose name is not visible", "pat:zzz", "", "token"},
		{"unknown", "sha256:ghost", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			label, kind := c.initiatorOf(context.Background(), tc.peer, devices)
			assert.Equal(t, tc.label, label)
			assert.Equal(t, tc.kind, kind)
		})
	}
	t.Run("no console source means console is unknown", func(t *testing.T) {
		bare := &Client{}
		label, kind := bare.initiatorOf(context.Background(), "sha256:console", devices)
		assert.Empty(t, label+kind)
	})
}

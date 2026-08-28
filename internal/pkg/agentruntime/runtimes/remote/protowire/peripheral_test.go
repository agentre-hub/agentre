package protowire

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

func TestMCPProxyRoundTripPreservesRepeatedHeadersAndBinaryBody(t *testing.T) {
	want := wire.MCPProxyRequest{Path: "/mcp/org/", Method: "POST", Headers: map[string][]string{"Set-Cookie": {"a=1", "b=2"}}, Body: []byte{0, 255}}
	got := MCPProxyRequestFromProto(MCPProxyRequestToProto(want))
	require.Equal(t, want, got)
	wantResponse := wire.MCPProxyResponse{Status: 200, Headers: map[string][]string{"X": {"a", "b"}}, Body: []byte{255, 0}}
	gotResponse := MCPProxyResponseFromProto(MCPProxyResponseToProto(wantResponse))
	require.Equal(t, wantResponse, gotResponse)
}

func TestSkillCatalogRoundTrip(t *testing.T) {
	want := wire.SkillCatalogResult{Discovery: wire.SkillDiscoveryOK, Packs: []wire.SkillPackSummary{{ID: "pack", Name: "Pack", Description: "desc", Skills: []string{"a"}, Installed: true, Enabled: true, GloballyEnabled: true}}}
	got := SkillCatalogResponseFromProto(SkillCatalogResponseToProto(want))
	require.Equal(t, want, got)
}

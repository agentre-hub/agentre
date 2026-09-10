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

func TestSkillCommandsRoundTrip(t *testing.T) {
	want := wire.SkillCommandsResult{Discovery: wire.SkillDiscoveryOK, Commands: []wire.SkillCommand{
		{Name: "superpowers:brainstorming", Description: "先想清楚再动手"},
		// 描述为空是常态(CLI 原生解析出来的 skill 常常只有一个裸名字),
		// 往返之后仍要是空串而不是丢掉整条。
		{Name: "cago"},
	}}
	got := SkillCommandsResponseFromProto(SkillCommandsResponseToProto(want))
	require.Equal(t, want, got)
}

// TestSkillCommandsRoundTripEmptyIsNotNil 空清单往返后仍是空切片:调用方按
// discovery 判断「问没问出来」,而不是按 nil —— 两者在这条协议上是两回事。
func TestSkillCommandsRoundTripEmptyIsNotNil(t *testing.T) {
	want := wire.SkillCommandsResult{Discovery: wire.SkillDiscoveryUnavailable, Commands: []wire.SkillCommand{}}
	got := SkillCommandsResponseFromProto(SkillCommandsResponseToProto(want))
	require.Equal(t, want, got)
	require.NotNil(t, got.Commands)
}

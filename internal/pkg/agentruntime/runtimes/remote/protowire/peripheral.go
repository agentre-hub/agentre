package protowire

import (
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func MCPProxyRequestToProto(value wire.MCPProxyRequest) *agentrewire.MCPProxyRequest {
	return &agentrewire.MCPProxyRequest{Path: value.Path, Method: value.Method, Headers: headersToProto(value.Headers), Body: append([]byte(nil), value.Body...)}
}
func MCPProxyRequestFromProto(value *agentrewire.MCPProxyRequest) wire.MCPProxyRequest {
	return wire.MCPProxyRequest{Path: value.GetPath(), Method: value.GetMethod(), Headers: headersFromProto(value.GetHeaders()), Body: append([]byte(nil), value.GetBody()...)}
}
func MCPProxyResponseToProto(value wire.MCPProxyResponse) *agentrewire.MCPProxyResponse {
	return &agentrewire.MCPProxyResponse{Status: int32(value.Status), Headers: headersToProto(value.Headers), Body: append([]byte(nil), value.Body...)}
}
func MCPProxyResponseFromProto(value *agentrewire.MCPProxyResponse) wire.MCPProxyResponse {
	return wire.MCPProxyResponse{Status: int(value.GetStatus()), Headers: headersFromProto(value.GetHeaders()), Body: append([]byte(nil), value.GetBody()...)}
}

func headersToProto(values map[string][]string) map[string]*agentrewire.HeaderValues {
	if values == nil {
		return nil
	}
	out := make(map[string]*agentrewire.HeaderValues, len(values))
	for key, value := range values {
		out[key] = &agentrewire.HeaderValues{Values: append([]string(nil), value...)}
	}
	return out
}
func headersFromProto(values map[string]*agentrewire.HeaderValues) map[string][]string {
	if values == nil {
		return nil
	}
	out := make(map[string][]string, len(values))
	for key, value := range values {
		out[key] = append([]string(nil), value.GetValues()...)
	}
	return out
}

func SkillCatalogResponseToProto(value wire.SkillCatalogResult) *agentrewire.SkillCatalogResponse {
	out := &agentrewire.SkillCatalogResponse{Discovery: value.Discovery}
	for _, pack := range value.Packs {
		out.Packs = append(out.Packs, &agentrewire.SkillPackSummary{Id: pack.ID, Name: pack.Name, Description: pack.Description, Skills: append([]string(nil), pack.Skills...), Installed: pack.Installed, Enabled: pack.Enabled, GloballyEnabled: pack.GloballyEnabled})
	}
	return out
}
func SkillCatalogResponseFromProto(value *agentrewire.SkillCatalogResponse) wire.SkillCatalogResult {
	out := wire.SkillCatalogResult{Discovery: value.GetDiscovery(), Packs: make([]wire.SkillPackSummary, 0, len(value.GetPacks()))}
	for _, pack := range value.GetPacks() {
		out.Packs = append(out.Packs, wire.SkillPackSummary{ID: pack.GetId(), Name: pack.GetName(), Description: pack.GetDescription(), Skills: append([]string(nil), pack.GetSkills()...), Installed: pack.GetInstalled(), Enabled: pack.GetEnabled(), GloballyEnabled: pack.GetGloballyEnabled()})
	}
	return out
}

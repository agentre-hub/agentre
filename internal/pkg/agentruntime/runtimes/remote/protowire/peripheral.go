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
func SkillCommandsResponseToProto(value wire.SkillCommandsResult) *agentrewire.SkillCommandsResponse {
	out := &agentrewire.SkillCommandsResponse{Discovery: value.Discovery, Commands: make([]*agentrewire.SkillCommand, 0, len(value.Commands))}
	for _, command := range value.Commands {
		out.Commands = append(out.Commands, &agentrewire.SkillCommand{Name: command.Name, Description: command.Description})
	}
	return out
}
func SkillCommandsResponseFromProto(value *agentrewire.SkillCommandsResponse) wire.SkillCommandsResult {
	out := wire.SkillCommandsResult{Discovery: value.GetDiscovery(), Commands: make([]wire.SkillCommand, 0, len(value.GetCommands()))}
	for _, command := range value.GetCommands() {
		out.Commands = append(out.Commands, wire.SkillCommand{Name: command.GetName(), Description: command.GetDescription()})
	}
	return out
}

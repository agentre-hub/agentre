package protowire

import (
	"encoding/json"
	"fmt"

	"github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

func RunRequestToProto(value wire.RunParams) (*agentrewire.RuntimeRunRequest, error) {
	backend, err := decodeBackendJSON(value.Backend)
	if err != nil {
		return nil, err
	}
	out := &agentrewire.RuntimeRunRequest{Backend: BackendToProto(backend), AgentId: value.AgentID, ConversationId: value.ConversationID, PeerFingerprint: string(value.PeerFingerprint), Cwd: value.Cwd, Title: value.Title, AgentSyncId: value.AgentSyncID, ProjectSyncId: value.ProjectSyncID, SystemPrompt: value.SystemPrompt, ProviderSessionId: value.ProviderSessionID, FreshSession: value.FreshSession, UserText: value.UserText, Compact: value.Compact, ForkAnchor: value.ForkAnchor, PermissionMode: value.PermissionMode, CollaborationMode: value.CollaborationMode, EnabledPlugins: value.EnabledPlugins, LlmProviderKey: value.LLMProviderKey, LlmModelKey: value.LLMModelKey, ReasoningEffort: value.ReasoningEffort, SourceDevice: string(value.SourceDevice), SourceDeviceName: value.SourceDeviceName}
	out.UserBlocks = blocksToProto(value.UserBlocks)
	for _, message := range value.History {
		out.History = append(out.History, &agentrewire.HistoryMessage{Role: message.Role, Blocks: blocksToProto(message.Blocks)})
	}
	for _, server := range value.MCPServers {
		out.McpServers = append(out.McpServers, &agentrewire.MCPServer{Name: server.Name, Url: server.URL, Headers: server.Headers, Tools: append([]string(nil), server.Tools...)})
	}
	return out, nil
}

func RunRequestFromProto(value *agentrewire.RuntimeRunRequest) (wire.RunParams, error) {
	if value == nil {
		return wire.RunParams{}, fmt.Errorf("protowire: nil runtime run request")
	}
	backend, err := json.Marshal(BackendFromProto(value.GetBackend()))
	if err != nil {
		return wire.RunParams{}, err
	}
	out := wire.RunParams{Backend: backend, AgentID: value.GetAgentId(), ConversationID: value.GetConversationId(), PeerFingerprint: devicefp.Initiator(value.GetPeerFingerprint()), Cwd: value.GetCwd(), Title: value.GetTitle(), AgentSyncID: value.GetAgentSyncId(), ProjectSyncID: value.GetProjectSyncId(), SystemPrompt: value.GetSystemPrompt(), ProviderSessionID: value.GetProviderSessionId(), FreshSession: value.GetFreshSession(), UserText: value.GetUserText(), UserBlocks: blocksFromProto(value.GetUserBlocks()), Compact: value.GetCompact(), ForkAnchor: value.GetForkAnchor(), PermissionMode: value.GetPermissionMode(), CollaborationMode: value.GetCollaborationMode(), EnabledPlugins: value.GetEnabledPlugins(), LLMProviderKey: value.GetLlmProviderKey(), LLMModelKey: value.GetLlmModelKey(), ReasoningEffort: value.GetReasoningEffort(), SourceDevice: devicefp.Initiator(value.GetSourceDevice()), SourceDeviceName: value.GetSourceDeviceName()}
	for _, message := range value.GetHistory() {
		out.History = append(out.History, wire.HistoryMessageWire{Role: message.GetRole(), Blocks: blocksFromProto(message.GetBlocks())})
	}
	for _, server := range value.GetMcpServers() {
		out.MCPServers = append(out.MCPServers, agentruntime.MCPServerSpec{Name: server.GetName(), URL: server.GetUrl(), Headers: server.GetHeaders(), Tools: append([]string(nil), server.GetTools()...)})
	}
	return out, nil
}

func GoalRequestToProto(value wire.GoalParams) (*agentrewire.RuntimeGoalRequest, error) {
	backend, err := decodeBackendJSON(value.Backend)
	if err != nil {
		return nil, err
	}
	return &agentrewire.RuntimeGoalRequest{ConversationId: value.ConversationID, PeerFingerprint: string(value.PeerFingerprint), AgentId: value.AgentID, AgentSyncId: value.AgentSyncID, ProviderSessionId: value.ProviderSessionID, Backend: BackendToProto(backend), Cwd: value.Cwd, Objective: value.Objective, Status: value.Status, TokenBudget: intPtrTo32(value.TokenBudget), LlmProviderKey: value.LLMProviderKey, LlmModelKey: value.LLMModelKey}, nil
}

func GoalRequestFromProto(value *agentrewire.RuntimeGoalRequest) (wire.GoalParams, error) {
	if value == nil {
		return wire.GoalParams{}, fmt.Errorf("protowire: nil runtime goal request")
	}
	var backend json.RawMessage
	if value.Backend != nil {
		encoded, err := json.Marshal(BackendFromProto(value.Backend))
		if err != nil {
			return wire.GoalParams{}, err
		}
		backend = encoded
	}
	return wire.GoalParams{ConversationID: value.GetConversationId(), PeerFingerprint: devicefp.Initiator(value.GetPeerFingerprint()), AgentID: value.GetAgentId(), AgentSyncID: value.GetAgentSyncId(), ProviderSessionID: value.GetProviderSessionId(), Backend: backend, Cwd: value.GetCwd(), Objective: value.Objective, Status: value.Status, TokenBudget: int32PtrToInt(value.TokenBudget), LLMProviderKey: value.GetLlmProviderKey(), LLMModelKey: value.GetLlmModelKey()}, nil
}

func GoalResponseFromProto(value *agentrewire.RuntimeGoalResponse) *agentruntime.Goal {
	if value == nil || value.Goal == nil {
		return nil
	}
	goal := value.Goal
	return &agentruntime.Goal{ThreadID: goal.GetThreadId(), Objective: goal.GetObjective(), Status: goal.GetStatus(), TokenBudget: int32PtrToInt(goal.TokenBudget), TokensUsed: int(goal.GetTokensUsed()), TimeUsedSeconds: int(goal.GetTimeUsedSeconds()), CreatedAt: goal.GetCreatedAt(), UpdatedAt: goal.GetUpdatedAt()}
}

func decodeBackendJSON(data []byte) (*agent_backend_entity.AgentBackend, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var out agent_backend_entity.AgentBackend
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("protowire: decode backend: %w", err)
	}
	return &out, nil
}
func blocksToProto(values []blocks.StoredBlock) []*agentrewire.StoredBlock {
	out := make([]*agentrewire.StoredBlock, 0, len(values))
	for _, v := range values {
		out = append(out, &agentrewire.StoredBlock{Type: v.Type, Data: append([]byte(nil), v.Data...)})
	}
	return out
}
func blocksFromProto(values []*agentrewire.StoredBlock) []blocks.StoredBlock {
	out := make([]blocks.StoredBlock, 0, len(values))
	for _, v := range values {
		out = append(out, blocks.StoredBlock{Type: v.GetType(), Data: append(json.RawMessage(nil), v.GetData()...)})
	}
	return out
}
func intPtrTo32(v *int) *int32 {
	if v == nil {
		return nil
	}
	x := int32(*v)
	return &x
}
func int32PtrToInt(v *int32) *int {
	if v == nil {
		return nil
	}
	x := int(*v)
	return &x
}

// BackendToProto 把一行 agent backend 翻上线。它导出是因为 goal / run 两条路径的
// 请求都携带这一行,而这里是本仓唯一的领域值 ↔ protobuf 转换边界。
func BackendToProto(v *agent_backend_entity.AgentBackend) *agentrewire.AgentBackend {
	if v == nil {
		return nil
	}
	return &agentrewire.AgentBackend{Id: v.ID, Type: v.Type, Name: v.Name, LlmProviderKey: v.LLMProviderKey, LlmModelKey: v.LLMModelKey, DeviceFingerprint: string(v.DeviceFingerprint), CliPath: v.CLIPath, ModelRoutes: v.ModelRoutes, Sandbox: v.Sandbox, Approval: v.Approval, EnvJson: v.EnvJSON, ReasoningEffort: v.ReasoningEffort, DefaultPermissionMode: v.DefaultPermissionMode, DefaultModel: v.DefaultModel, OpenclawGatewayUrl: v.OpenClawGatewayURL, OpenclawAgentId: v.OpenClawAgentID, OpenclawDefaultModel: v.OpenClawDefaultModel, OpenclawSessionMode: v.OpenClawSessionMode, Status: int32(v.Status), Createtime: v.Createtime, Updatetime: v.Updatetime, SyncId: v.SyncID, SyncAccountId: v.SyncAccountID, SyncVersion: v.SyncVersion, SyncUpdatedAt: v.SyncUpdatedAt, SyncOriginFingerprint: string(v.SyncOriginFingerprint), SyncDeletedAt: v.SyncDeletedAt}
}

// BackendFromProto 把线上那一行 agent backend 翻回实体。它与 BackendToProto 成对,
// 是 run / goal 两条路径唯一的那一份,收端不经过 JSON 中转。
func BackendFromProto(v *agentrewire.AgentBackend) *agent_backend_entity.AgentBackend {
	if v == nil {
		return nil
	}
	return &agent_backend_entity.AgentBackend{ID: v.GetId(), Type: v.GetType(), Name: v.GetName(), LLMProviderKey: v.GetLlmProviderKey(), LLMModelKey: v.GetLlmModelKey(), DeviceFingerprint: devicefp.Carrier(v.GetDeviceFingerprint()), CLIPath: v.GetCliPath(), ModelRoutes: v.GetModelRoutes(), Sandbox: v.GetSandbox(), Approval: v.GetApproval(), EnvJSON: v.GetEnvJson(), ReasoningEffort: v.GetReasoningEffort(), DefaultPermissionMode: v.GetDefaultPermissionMode(), DefaultModel: v.GetDefaultModel(), OpenClawGatewayURL: v.GetOpenclawGatewayUrl(), OpenClawAgentID: v.GetOpenclawAgentId(), OpenClawDefaultModel: v.GetOpenclawDefaultModel(), OpenClawSessionMode: v.GetOpenclawSessionMode(), Status: int(v.GetStatus()), Createtime: v.GetCreatetime(), Updatetime: v.GetUpdatetime(), SyncMeta: syncmeta_entity.SyncMeta{SyncID: v.GetSyncId(), SyncAccountID: v.GetSyncAccountId(), SyncVersion: v.GetSyncVersion(), SyncUpdatedAt: v.GetSyncUpdatedAt(), SyncOriginFingerprint: devicefp.LastWriter(v.GetSyncOriginFingerprint()), SyncDeletedAt: v.GetSyncDeletedAt()}}
}

// GoalRequestToDomain 把线上的 goal 请求翻成领域请求。SessionID 由宿主按自己的
// 会话键规则补上 —— 那是宿主的进程内约定,不是线上契约。
//
// 它替掉的是「proto → wire.GoalParams(backend 存成 JSON)→ 领域请求(再 unmarshal
// 回来)」那条两跳路径:同一行 backend 在一次调用里被 marshal 又 unmarshal 一遍,
// 而两端本来就都是同一个实体类型。
func GoalRequestToDomain(value *agentrewire.RuntimeGoalRequest) (agentruntime.GoalRequest, error) {
	if value == nil {
		return agentruntime.GoalRequest{}, fmt.Errorf("protowire: nil runtime goal request")
	}
	return agentruntime.GoalRequest{
		AgentID:           value.GetAgentId(),
		AgentSyncID:       value.GetAgentSyncId(),
		ProviderSessionID: value.GetProviderSessionId(),
		Backend:           BackendFromProto(value.GetBackend()),
		Cwd:               value.GetCwd(),
		Objective:         value.Objective,
		Status:            value.Status,
		TokenBudget:       int32PtrToInt(value.TokenBudget),
	}, nil
}

// GoalToProto 把一条目标翻上线。nil 目标(还没设过)照样是 nil,不是空目标。
func GoalToProto(goal *agentruntime.Goal) *agentrewire.Goal {
	if goal == nil {
		return nil
	}
	return &agentrewire.Goal{ThreadId: goal.ThreadID, Objective: goal.Objective, Status: goal.Status, TokenBudget: intPtrTo32(goal.TokenBudget), TokensUsed: int32(goal.TokensUsed), TimeUsedSeconds: int32(goal.TimeUsedSeconds), CreatedAt: goal.CreatedAt, UpdatedAt: goal.UpdatedAt}
}

// StoredBlocksFromProto 把线上块列表翻回持久化块。
func StoredBlocksFromProto(values []*agentrewire.StoredBlock) []blocks.StoredBlock {
	return blocksFromProto(values)
}

// HistoryMessagesFromProto 把线上历史消息翻回它们的 wire 镜像形态(块仍是持久化
// envelope,由调用方按自己的需要解码)。
func HistoryMessagesFromProto(values []*agentrewire.HistoryMessage) []wire.HistoryMessageWire {
	if len(values) == 0 {
		return nil
	}
	out := make([]wire.HistoryMessageWire, 0, len(values))
	for _, message := range values {
		out = append(out, wire.HistoryMessageWire{Role: message.GetRole(), Blocks: blocksFromProto(message.GetBlocks())})
	}
	return out
}

// MCPServersFromProto 把线上 MCP server 声明翻回领域值。
func MCPServersFromProto(values []*agentrewire.MCPServer) []agentruntime.MCPServerSpec {
	if len(values) == 0 {
		return nil
	}
	out := make([]agentruntime.MCPServerSpec, 0, len(values))
	for _, server := range values {
		out = append(out, agentruntime.MCPServerSpec{Name: server.GetName(), URL: server.GetUrl(), Headers: server.GetHeaders(), Tools: append([]string(nil), server.GetTools()...)})
	}
	return out
}

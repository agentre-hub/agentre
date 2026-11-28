// Package sessionenginetest 是 sessionengine 的测试替身：「旧夹具桥」。
//
// chat_svc 与 chat_svc/peerstream 的一大批既有用例按引擎配置出现之前的形状搭夹具 ——
// Agent 的 AgentBackendID + agent_backend_repo mock 里的那一行后端。桥把「成员生效的引擎
// 配置」与「按 ID 读配置」答成那一行后端摊成的配置（同一份设置，ID 沿用后端 ID），于是这些
// 用例原样走引擎配置的解析路径，每次解析仍只读一次那一行后端。只给测试用。
package sessionenginetest

import (
	"context"
	"sync"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

var devices sync.Map

// LegacyEffective 把成员生效的引擎配置答成它 AgentBackendID 那一行后端摊成的配置。
func LegacyEffective(ctx context.Context, a *agent_entity.Agent) (*agent_backend_entity.EngineProfile, error) {
	if a == nil || a.AgentBackendID <= 0 {
		return nil, nil
	}
	return legacyProfile(ctx, a.AgentBackendID)
}

// LegacyProfiles 按 ID 读配置 = 读同 ID 的那一行后端。
type LegacyProfiles struct {
	agent_backend_repo.EngineProfileRepo
}

func (LegacyProfiles) Find(ctx context.Context, id int64) (*agent_backend_entity.EngineProfile, error) {
	return legacyProfile(ctx, id)
}

func (LegacyProfiles) FindAnyStatus(ctx context.Context, id int64) (*agent_backend_entity.EngineProfile, error) {
	return legacyProfile(ctx, id)
}

// DeviceOf 桥最近一次答出的那份配置（= 后端 ID）所在的设备。
func DeviceOf(profileID int64) devicefp.Carrier {
	if v, ok := devices.Load(profileID); ok {
		return v.(devicefp.Carrier)
	}
	return ""
}

func legacyProfile(ctx context.Context, backendID int64) (*agent_backend_entity.EngineProfile, error) {
	be, err := agent_backend_repo.AgentBackend().Find(ctx, backendID)
	if err != nil || be == nil {
		return nil, err
	}
	devices.Store(be.ID, be.DeviceFingerprint)
	return &agent_backend_entity.EngineProfile{
		ID: be.ID, Type: be.Type, Name: be.Name, LLMProviderKey: be.LLMProviderKey, LLMModelKey: be.LLMModelKey,
		ModelRoutes: be.ModelRoutes, Sandbox: be.Sandbox, Approval: be.Approval, EnvJSON: be.EnvJSON,
		ReasoningEffort: be.ReasoningEffort, DefaultPermissionMode: be.DefaultPermissionMode, DefaultModel: be.DefaultModel,
		OpenClawGatewayURL: be.OpenClawGatewayURL, OpenClawAgentID: be.OpenClawAgentID,
		OpenClawDefaultModel: be.OpenClawDefaultModel, OpenClawSessionMode: be.OpenClawSessionMode,
		HermesURL: be.HermesURL, HermesAuthProvider: be.HermesAuthProvider, HermesUserID: be.HermesUserID,
		ACPCommand: be.ACPCommand, ACPArgs: be.ACPArgs, ConfigJSON: be.ConfigJSON,
		Status: be.Status, SyncMeta: be.SyncMeta,
	}, nil
}

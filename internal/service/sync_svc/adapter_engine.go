package sync_svc

import (
	"context"
	"encoding/json"

	"github.com/cago-frame/cago/pkg/consts"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// engineProfileAdapter 同步引擎配置（去设备的后端）。载荷与 agentBackendAdapter 同一套
// 身份字段与 config 规则，但没有设备（信封里的 agentred_fingerprint 恒空）也没有
// cli_path——路径是每设备覆盖，自成 device_cli_path。
type engineProfileAdapter struct{ baseAdapter }

func (engineProfileAdapter) kind() string { return syncwire.KindEngineProfile }

func (engineProfileAdapter) load(ctx context.Context, syncID string) (*outbound, error) {
	row := &agent_backend_entity.EngineProfile{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindEngineProfile, syncID, row)
	if err != nil || !found {
		return nil, err
	}
	// FindRow 是裸读：独占设置只在 config_json 列里，Go 字段未经解码恒为零。
	if err := row.UnmarshalConfig(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(syncwire.EngineProfilePayload{
		Type:            row.Type,
		Name:            row.Name,
		ProviderKey:     row.LLMProviderKey,
		ModelKey:        row.LLMModelKey,
		EnvJSON:         row.EnvJSON,
		ReasoningEffort: row.ReasoningEffort,
		Config:          row.AsBackend().Config(),
		AccountDefault:  row.IsAccountDefault,
	})
	if err != nil {
		return nil, err
	}
	return &outbound{SyncID: row.SyncID, UpdatedAt: row.Updatetime, Payload: payload}, nil
}

// 供应商按 provider_key 引用，与 agentBackendAdapter 一样不是阻塞引用。
func (engineProfileAdapter) refs(*inbound) []ref { return nil }

func (engineProfileAdapter) apply(ctx context.Context, in *inbound, _ map[string]int64) error {
	var p syncwire.EngineProfilePayload
	if err := json.Unmarshal(in.Payload, &p); err != nil {
		return err
	}
	row := &agent_backend_entity.EngineProfile{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindEngineProfile, in.SyncID, row)
	if err != nil {
		return err
	}
	row.Type, row.Name, row.LLMProviderKey, row.LLMModelKey = p.Type, p.Name, p.ProviderKey, p.ModelKey
	row.EnvJSON, row.ReasoningEffort = p.EnvJSON, p.ReasoningEffort
	// config 整体替换本地那一份：缺席的键变空。经 AgentBackend 的同一份 SetConfig 转换，
	// 规则只写一份。
	cfg := &agent_backend_entity.AgentBackend{}
	cfg.SetConfig(p.Config)
	if err := cfg.MarshalConfig(); err != nil {
		return err
	}
	row.ConfigJSON = cfg.ConfigJSON
	if err := row.UnmarshalConfig(); err != nil {
		return err
	}
	row.Status = consts.ACTIVE
	// 过不了既有校验的载荷不落库，本地原值不变。
	if err := row.Check(ctx); err != nil {
		return err
	}
	if !found {
		row.SyncID = in.SyncID
		if err := agent_backend_repo.EngineProfile().Create(ctx, row); err != nil {
			return err
		}
	} else if err := agent_backend_repo.EngineProfile().Update(ctx, row); err != nil {
		return err
	}
	// 账号默认只经 SetAccountDefault 改（Update 不写这一列）。没带标记不去清：默认
	// 永远有一份，换默认是「另一份被标上」时那次 SetAccountDefault 顺手清掉的。
	if p.AccountDefault && !row.IsAccountDefault {
		return agent_backend_repo.EngineProfile().SetAccountDefault(ctx, row.ID)
	}
	return nil
}

// remove 软删配置并让引用它的成员与部门回落（成员 → 部门链 → 账号默认）。引用方自己
// 的同步行由删除发起端一并重发，这里只保证本机不留悬空引用。
func (engineProfileAdapter) remove(ctx context.Context, in *inbound) error {
	id, err := syncstate_repo.SyncState().FindLocalID(ctx, syncwire.KindEngineProfile, in.SyncID)
	if err != nil || id == 0 {
		return err
	}
	if err := agent_backend_repo.EngineProfile().Delete(ctx, id); err != nil {
		return err
	}
	if err := agent_repo.Agent().ClearEngineProfile(ctx, id); err != nil {
		return err
	}
	return department_repo.Department().ClearDefaultEngineProfile(ctx, id)
}

// engineProfileSyncIDOf 取活的引擎配置的同步标识；id <= 0、已删或不在时返回空串——
// 载荷里不送一个对端永远等不到的引用。
func engineProfileSyncIDOf(ctx context.Context, id int64) (string, error) {
	if id <= 0 {
		return "", nil
	}
	profile, err := agent_backend_repo.EngineProfile().Find(ctx, id)
	if err != nil || profile == nil {
		return "", err
	}
	return syncIDOf(profile.SyncMeta), nil
}

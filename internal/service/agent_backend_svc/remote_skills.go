package agent_backend_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/agentskill"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// RemoteSkillDiscoverer 经 device 连接池调 daemon skills.list,枚举远端 daemon 本机已装
// 技能包,供 skill_svc 注入(结构化满足其 RemoteDiscoverer 端口)。daemon 不可达 / RPC
// 失败时软降级为空 —— 与本地 claudeskill 发现器一致(CLI 不可用→空发现),让技能配置
// 面板在远端离线时照常可用,只是不展 daemon 已装集。
type RemoteSkillDiscoverer struct{}

// NewRemoteSkillDiscoverer 构造远端技能发现器(bootstrap 注入 skill_svc)。
func NewRemoteSkillDiscoverer() *RemoteSkillDiscoverer { return &RemoteSkillDiscoverer{} }

// ListSkills 借 deviceID 的 daemon 连接调 skills.list,返 daemon 本机已装技能包。
func (*RemoteSkillDiscoverer) ListSkills(ctx context.Context, deviceID int64, backendType string) ([]agentskill.SkillPack, error) {
	rds := remote_device_svc.Default()
	if rds == nil || rds.Pool() == nil {
		return []agentskill.SkillPack{}, nil
	}
	lease, err := rds.Pool().Borrow(ctx, deviceID)
	if err != nil {
		logger.Ctx(ctx).Warn("agent_backend_svc.RemoteSkillDiscoverer.ListSkills: dial failed",
			zap.Int64("deviceID", deviceID), zap.Error(err))
		return []agentskill.SkillPack{}, nil
	}
	defer lease.Release()

	packs, err := listRemoteSkills(ctx, lease.Client().Conn(), backendType)
	if err != nil {
		logger.Ctx(ctx).Warn("agent_backend_svc.RemoteSkillDiscoverer.ListSkills: rpc failed",
			zap.Int64("deviceID", deviceID), zap.Error(err))
		return []agentskill.SkillPack{}, nil
	}
	return packs, nil
}

func listRemoteSkills(ctx context.Context, conn *protorpc.Conn, backendType string) ([]agentskill.SkillPack, error) {
	response, err := protorpc.CallMethod(ctx, conn, uint32(agentrewire.RpcMethod_RPC_METHOD_SKILLS_LIST),
		&agentrewire.SkillsListRequest{BackendType: backendType}, func() *agentrewire.SkillsListResponse { return &agentrewire.SkillsListResponse{} })
	if err != nil {
		return nil, err
	}
	packs := make([]agentskill.SkillPack, 0, len(response.Packs))
	for _, pack := range response.Packs {
		packs = append(packs, agentskill.SkillPack{ID: pack.Id, Name: pack.Name, Description: pack.Description,
			Skills: append([]string(nil), pack.Skills...), Source: agentskill.Source(pack.Source), Recommended: pack.Recommended,
			Installed: pack.Installed, GloballyEnabled: pack.GloballyEnabled})
	}
	return packs, nil
}

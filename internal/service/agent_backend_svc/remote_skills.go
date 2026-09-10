package agent_backend_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentskill"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
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

	packs, err := listRemoteSkills(ctx, lease.Client(), backendType)
	if err != nil {
		logger.Ctx(ctx).Warn("agent_backend_svc.RemoteSkillDiscoverer.ListSkills: rpc failed",
			zap.Int64("deviceID", deviceID), zap.Error(err))
		return []agentskill.SkillPack{}, nil
	}
	return packs, nil
}

func listRemoteSkills(ctx context.Context, conn wirecall.Caller, backendType string) ([]agentskill.SkillPack, error) {
	response, err := wirecall.SkillsList(ctx, conn, &agentrewire.SkillsListRequest{BackendType: backendType})
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

// ListSkillCommands 借 deviceID 的连接调 skills.commands,返**那台机器**此刻叫得动的
// skill 名字。
//
// 它与 ListSkills 的分工见 skill_svc.RemoteDiscoverer 的端口注释:后者只答可配置的
// plugin 包,而输入框要的名字还含 CLI 自己解析的 user / project / system skill。
//
// 软降级口径与 ListSkills 一致(拨不通 / RPC 失败 → 空清单):输入框照常能用,只是
// 没有补全 —— 整块报错会把用户正在打的那句话一起打掉。判别值那一维在这里读不到,
// 因为端口只回清单;需要区分「答不出」与「没有」的是浏览器控制台那条路,它直接读
// RPC 的 discovery。
func (*RemoteSkillDiscoverer) ListSkillCommands(
	ctx context.Context, deviceID int64, backendType, cwd string,
	authorized []agent_entity.AgentSkillItem,
) ([]agentskill.SkillCommand, error) {
	rds := remote_device_svc.Default()
	if rds == nil || rds.Pool() == nil {
		return []agentskill.SkillCommand{}, nil
	}
	lease, err := rds.Pool().Borrow(ctx, deviceID)
	if err != nil {
		logger.Ctx(ctx).Warn("agent_backend_svc.RemoteSkillDiscoverer.ListSkillCommands: dial failed",
			zap.Int64("deviceID", deviceID), zap.Error(err))
		return []agentskill.SkillCommand{}, nil
	}
	defer lease.Release()

	request := &agentrewire.SkillCommandsRequest{BackendType: backendType, Cwd: cwd}
	for _, item := range authorized {
		request.Authorized = append(request.Authorized,
			&agentrewire.SkillAuthorization{Id: item.ID, Enabled: item.Enabled})
	}
	response, err := wirecall.SkillCommands(ctx, lease.Client(), request)
	if err != nil {
		logger.Ctx(ctx).Warn("agent_backend_svc.RemoteSkillDiscoverer.ListSkillCommands: rpc failed",
			zap.Int64("deviceID", deviceID), zap.Error(err))
		return []agentskill.SkillCommand{}, nil
	}
	commands := make([]agentskill.SkillCommand, 0, len(response.Commands))
	for _, command := range response.Commands {
		commands = append(commands, agentskill.SkillCommand{
			Name: command.GetName(), Description: command.GetDescription(),
		})
	}
	return commands, nil
}

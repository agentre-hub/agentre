// Package fakes provides deterministic composition reachable only from the dedicated E2E main.
// 它和 e2e/ 下的 Playwright 工程同处一个目录树,但单独成包,避免 Go 源码与
// TS/Playwright 工具链在同一目录里混在一起。
package fakes

import (
	"context"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	fakert "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/fake"
	"github.com/agentre-hub/agentre/internal/pkg/agentskill"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/service/agent_backend_svc"
	"github.com/agentre-hub/agentre/internal/service/agent_svc"
)

type codexSkillDiscoverer struct{}

func (codexSkillDiscoverer) Discover(context.Context, agentskill.DiscoverQuery) ([]agentskill.SkillPack, error) {
	return []agentskill.SkillPack{
		{
			ID:              "browser@openai-bundled",
			Name:            "browser",
			Skills:          []string{"browser"},
			Source:          agentskill.SourceInstalled,
			Installed:       true,
			GloballyEnabled: true,
		},
		{
			ID:              "superpowers@openai-curated",
			Name:            "superpowers",
			Skills:          []string{"tdd"},
			Source:          agentskill.SourceInstalled,
			Installed:       true,
			GloballyEnabled: false,
		},
	}, nil
}

type claudeSkillDiscoverer struct{}

func (claudeSkillDiscoverer) Discover(context.Context, agentskill.DiscoverQuery) ([]agentskill.SkillPack, error) {
	return []agentskill.SkillPack{
		{
			ID:              "superpowers@claude-plugins-official",
			Name:            "superpowers",
			Skills:          []string{"tdd"},
			Source:          agentskill.SourceInstalled,
			Installed:       true,
			GloballyEnabled: true,
		},
	}, nil
}

// Install:
//  1. 用确定性 fake 覆盖 claudecode runtime(无子进程/无登录);
//  2. seed 冒烟场景需要的最小本地 claudecode backend 并挂到默认 CEO agent,
//     让前端"建会话→发消息→看回复"无需真实 CLI 即可跑通。
//
// 隔离 keychain 由 bootstrap(initKeychain,见 internal/bootstrap/keychain.go)在装配
// Server / Remote Device 之前按 AGENTRE_KEYCHAIN_DIR 建立,这里不再覆盖。
func Install(ctx context.Context) error {
	// 先接账号:随后 seed 出来的 backend / agent 才会带着账号进出站队列(R3)。
	if err := installE2ELoggedInAccount(ctx); err != nil {
		return fmt.Errorf("seed logged-in account: %w", err)
	}
	agentruntime.RegisterRuntime(agent_backend_entity.TypeClaudeCode, fakert.New())
	agentskill.RegisterDiscoverer(agent_backend_entity.TypeClaudeCode, claudeSkillDiscoverer{})
	agentskill.RegisterDiscoverer(agent_backend_entity.TypeCodex, codexSkillDiscoverer{})

	// 幂等:正常每次 e2e run 用全新 AGENTRE_DATA_DIR(临时目录),但 wails dev 热重载
	// 会重启 app 进程,backend 可能已存在 —— 命中则复用,避免重名报错后跳过挂载。
	const backendName = "E2E Local Backend"
	var backendID int64
	if existing, err := agent_backend_repo.AgentBackend().FindByName(ctx, backendName); err != nil {
		return fmt.Errorf("lookup local backend: %w", err)
	} else if existing != nil {
		backendID = existing.ID
	} else {
		resp, err := agent_backend_svc.AgentBackend().Create(ctx, &agent_backend_svc.CreateBackendRequest{
			Type: string(agent_backend_entity.TypeClaudeCode),
			Name: backendName,
		})
		if err != nil {
			return fmt.Errorf("create local backend: %w", err)
		}
		backendID = resp.Item.ID
	}

	ceo, err := agent_repo.Agent().FindSystem(ctx)
	if err != nil {
		return fmt.Errorf("find system agent: %w", err)
	}
	if ceo == nil {
		return fmt.Errorf("system agent not found")
	}

	if _, err := agent_svc.Agent().Update(ctx, &agent_svc.UpdateAgentRequest{
		ID:          ceo.ID,
		Name:        ceo.Name,
		ExecTargets: []agent_svc.ExecTargetInputDTO{{AgentBackendID: backendID}},
	}); err != nil {
		return fmt.Errorf("attach local backend to system agent: %w", err)
	}

	logger.Ctx(ctx).Info("e2efakes.Install: e2e fakes installed",
		zap.Int64("backendID", backendID), zap.Int64("agentID", ceo.ID))
	return nil
}

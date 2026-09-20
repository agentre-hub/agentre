package agent_backend_svc

import (
	"context"
	"errors"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/acp"
)

// acpProbe 是 acp.Probe 的间接引用，让单测能跳过真实子进程（hermesProbe 同款
// seam）。
var acpProbe = acp.Probe

// acpProber 探测一条 acp 后端：起 backend 声明的 ACP agent 子进程、完成
// initialize 握手并校验 protocolVersion==1，然后关掉进程。它不跑 session/new、
// 不跑一轮 prompt —— 照 hermesProber 的口径「握手即止」。
type acpProber struct{}

func (acpProber) Run(ctx context.Context, b *agent_backend_entity.AgentBackend, _ ProbeDeps) (string, error) {
	if b == nil {
		return "", errors.New("nil backend")
	}
	return acpProbe(ctx, acp.ProbeRequest{
		Command: b.ACPCommand,
		Args:    b.ACPArgs,
	})
}

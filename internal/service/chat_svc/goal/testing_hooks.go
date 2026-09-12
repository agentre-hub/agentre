package goal

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// SetOnSessionForTest 从已解析好的会话上下文直接下发一次目标,跳过 Set 的库查询。
// 测试专用:既有回归用例要在没有仓储 mock 的情况下钉「一次性远端 goal RPC 必须还掉
// 它借的租约」。
func (c *Controller) SetOnSessionForTest(
	ctx context.Context,
	sess *chat_entity.Session,
	a *agent_entity.Agent,
	be *agent_backend_entity.AgentBackend,
	prov *llm_provider_entity.LLMProvider,
	patch Patch,
) (*agentruntime.Goal, func(), error) {
	return c.setOnSession(ctx, sess, a, be, prov, patch)
}

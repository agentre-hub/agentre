package chat_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
)

// EnabledPluginsProvider 按「这一轮落到的那一档」给 turn 返回技能包覆盖 map(仅显式
// 覆盖:强制开=true / 强制关=false;未列出的 plugin 沿用全局 ~/.claude 配置=继承)。
// bootstrap 注册 skill_svc 的实现;nil = 不注入。在 runTurn 单点生效,单聊/
// Regenerate 全覆盖(与 turn_mcp 同一接缝)。
//
// agentBackendID 指名这一轮实际解析到的那一档(R15b / R15e):技能授权挂在单个执行
// 目标上,同一台机器上可以有多档,「取哪一份」只能由这一轮落到的那一档回答,不能
// 回到「Agent 的主档」——那正是决策 36 要消除的歧义。0 = 未钉档(老会话),由
// provider 自行回落到主档。
type EnabledPluginsProvider func(ctx context.Context, a *agent_entity.Agent, agentBackendID int64) map[string]bool

var enabledPluginsProvider EnabledPluginsProvider

// RegisterEnabledPluginsProvider bootstrap 接线入口。
func RegisterEnabledPluginsProvider(p EnabledPluginsProvider) { enabledPluginsProvider = p }

// enabledPluginsForTurn runTurn 组 RunRequest 时调;capOK = runner 声明 CapSkills。
// 未注册 provider 或 cap 不支持 → nil(runtime 忽略)。
func enabledPluginsForTurn(
	ctx context.Context, a *agent_entity.Agent, agentBackendID int64, capOK bool,
) map[string]bool {
	if enabledPluginsProvider == nil || !capOK {
		return nil
	}
	return enabledPluginsProvider(ctx, a, agentBackendID)
}

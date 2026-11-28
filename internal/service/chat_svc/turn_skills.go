package chat_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
)

// EnabledPluginsProvider 给 turn 返回技能包覆盖 map(仅显式覆盖:强制开=true / 强制关=
// false;未列出的 plugin 沿用全局 ~/.claude 配置=继承)。bootstrap 注册 skill_svc 的实现;
// nil = 不注入。在 runTurn 单点生效,单聊/Regenerate 全覆盖(与 turn_mcp 同一接缝)。
//
// 授权挂在成员上(spec 2026-11-25「成员详情『执行』栏」),按运行时校验:runtime 是这一轮
// 引擎配置的运行时类型,授权时的运行时与它不同时授权失效。空串 = 说不出这一轮的运行时,
// 由 provider 自行回落到成员生效的那份。
type EnabledPluginsProvider func(ctx context.Context, a *agent_entity.Agent, runtime string) map[string]bool

var enabledPluginsProvider EnabledPluginsProvider

// RegisterEnabledPluginsProvider bootstrap 接线入口。
func RegisterEnabledPluginsProvider(p EnabledPluginsProvider) { enabledPluginsProvider = p }

// enabledPluginsForTurn runTurn 组 RunRequest 时调;capOK = runner 声明 CapSkills。
// 未注册 provider 或 cap 不支持 → nil(runtime 忽略)。
func enabledPluginsForTurn(
	ctx context.Context, a *agent_entity.Agent, runtime string, capOK bool,
) map[string]bool {
	if enabledPluginsProvider == nil || !capOK {
		return nil
	}
	return enabledPluginsProvider(ctx, a, runtime)
}

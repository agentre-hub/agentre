package daemon

// 这里的 blank imports 触发各 backend runtime 子包的 init(),让它们
// 把自己 RegisterRuntime 到 agentruntime 注册表里。务必保留此文件 ——
// 没有这些 import,daemon.handlers.RuntimeHandlers.Run 时
// agentruntime.RuntimeFor(bt) 会返 nil 报 "backend %q not registered"。
//
// TypeBuiltin 实际在 daemon 端被拒绝(builtin 只跑在 agentre 桌面端
// 进程内),这里 import 它仅为对称,确保 RegisteredRuntimes() 能枚举
// 完整集合(将来 capability 协商可能用到)。
import (
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/builtin"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/claudecode"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/codex"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/piagent"

	// 技能包发现器:skills.list RPC 在 daemon 本机枚举已装 plugin 时,经
	// agentskill.DiscovererFor 反查,需这些 init() 自注册到发现器注册表。
	_ "github.com/agentre-hub/agentre/internal/pkg/agentskill/claudeskill"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentskill/codexskill"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentskill/piagentskill"
)

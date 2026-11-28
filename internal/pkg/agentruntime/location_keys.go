package agentruntime

// RevokeRootMetaKey 是执行侧加进 ToolResult.Meta(一个 JSON 对象)的键,值是这次工具调用
// 删除掉的已认领工作根(执行设备上的绝对路径;Claude Code ExitWorktree 选择删除时就是被删
// 的 worktree)。它随 Meta 经 wire 到桌面,消费方(chat_svc)据此撤销这个根 —— 本机与远端
// 会话同一条路。撤销改的是认领集合,不移动 AI 当前位置,主代理与子代理的结果一样生效。
const RevokeRootMetaKey = "agentre_revoke_root"

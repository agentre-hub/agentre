// Capability 是后端 runtime 暴露的能力名(string),前端按 set membership 查。
// 文字列对齐 internal/pkg/agentruntime/capability/capability.go 里的 const。
// 加新 cap 时,后端先加,前端这里同步即可(不强制 — Set.has(unknown) 也是 false)。
export type Capability =
  | "steer"
  | "cancel_steer"
  | "drain_steer"
  | "abort"
  | "stop_background_task"
  | "image_input"
  | "set_permission_mode"
  | "answer_user_ask"
  | "tool_permission_gate"
  | "fork_session"
  | "report_context_window"
  | "compact"
  | "goal"
  | "mcp_tools"
  | "skills"
  | "reasoning_effort";

// PermissionModeMeta 镜像后端 capability.PermissionModeMeta:
//   - allowedModes: runtime 接受的 mode 集合(claudecode = 4 档, codex = 2 档,
//     builtin/remote = 空)
//   - defaultMode: runtime 默认 mode(未传时使用)
//   - switchableDuringTurn: 是否允许在 turn 中切换(codex = false)
//   - order: pill cycle 顺序(allowedModes 的一个排列)
export type PermissionModeMeta = {
  allowedModes: string[];
  defaultMode: string;
  switchableDuringTurn: boolean;
  order: string[];
};

// Capabilities 是 useSessionCapabilities() 返回的不可变对象。
// 不直接暴露内部 Set 是为了:
//   1. 强类型化的 has(cap) 接口(未知 cap 字面量直接 TS 报错)
//   2. PermissionModeMeta 保持 readonly 引用,组件可以放心放 dep array
export class Capabilities {
  constructor(
    private readonly set: ReadonlySet<string>,
    public readonly permissionModeMeta: PermissionModeMeta,
  ) {}
  has(c: Capability): boolean {
    return this.set.has(c);
  }
}

// frontend/src/stores/types.ts
//
// 集中定义 session 投影类型，消除各文件内的分散定义。
// 各 store / hook 从此文件 re-export，消费方统一从 @/stores/types 导入。
//
// 纯类型文件：0 运行时代码。

// AgentStatus: session 级运行态 token。唯一定义；components/agentre/types.ts re-exports 此类型。
export type AgentStatus = "idle" | "running" | "waiting" | "error";

// SessionConnectionState: 本机与执行该会话那台远端 daemon 之间的**通道**状态
// （后端 remote.ConnState / chat_svc StreamConnectionState 事件的 connectionState）。
//
// 它**不是**第五个 AgentStatus:断连期间远端可能正在全速跑,会话仍然是「运行中」,
// 断的只是通道。做成第五个运行态取值会污染整套状态语言,并让所有既有的状态判定点
// (退出确认的活跃态集合、侧边栏计数、工具栏禁用逻辑)错误归类。因此它单独成型,
// 由 session-conn-store 持有,只用来改变转录流里活信号的**形态**。
export type SessionConnectionState = "connected" | "reconnecting" | "lost";

// SessionMetaSnapshot: session-meta-store 持有的静态字段快照。
// 等同于 session-meta-store.ts 的 SessionMeta，此处做 re-export 别名。
//
// 注：不含 id 字段 —— sessionId 是 session-meta-store.metas 这个 Map 的 key，
// 不在 value 里重复。spec §5.1 的字段表里写了 id 是笔误，实际以本类型为准。
export type SessionMetaSnapshot = {
  agentId: number;
  agentName: string;
  agentColor: string;
  projectId?: number;
  title: string;
  lastMessageAt?: number;
  lastReadAt?: number;
  // permissionModeAtLaunch 是 CLI spawn 时下发的快照,session 生命周期内不变。
  // PlanApproveCard 据此判断 ExitPlanMode 弹卡要不要给 bypass 选项。
  permissionModeAtLaunch?: string;
};

// SessionStatusPatch: session-status-store upsert 接受的入参类型。
// 与 session-status-store.ts 的 SessionStatusPatch 对齐（不含 doneTick/lastDoneEvent）。
export type SessionStatusPatch = {
  agentStatus: AgentStatus;
  needsAttention: boolean;
  permissionMode?: string;
  bgRunning?: boolean;
};

// SessionView: useSessionWithOverlays 返回的合并投影。
// 包含 session 静态字段 + 运行时态 + 合并后的已读时间戳。
export type SessionView = {
  id: number;
  agentId: number;
  agentName: string;
  agentColor: string;
  projectId?: number;
  title: string;
  lastMessageAt: number;
  agentStatus: AgentStatus;
  needsAttention: boolean;
  permissionMode?: string;
  lastReadAt: number;
  bgRunning?: boolean;
};

// AttentionInput / AttentionReason 已搬进共享包（`@agentre-hub/agentre-ui` 的
// session-index/attention）：判定与投影两端共用一份，agentre-server 不再各画一遍。
// 这里保留同名 re-export，宿主的既有 import 路径一个都不用改。
export type { AttentionInput, AttentionReason } from "@agentre-hub/agentre-ui";

// ChatSessionStatusEvent: 后端 SSE 透传的 session_status 字段类型。
// 与 use-chat-stream.ts 的 ChatSessionStatusPatch 对齐。
// agentStatus 在后端/Wails 边界以 string 到达；cast 发生在写入 store 的调用侧。
export type ChatSessionStatusEvent = {
  agentStatus: string;
  needsAttention: boolean;
  permissionMode?: string;
  contextWindow?: number;
  bgRunning?: boolean;
};

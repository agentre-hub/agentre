/**
 * 会话运行态的**展示**投影：状态 token → 一组 Tailwind 类名。
 *
 * `AgentStatus` 在宿主侧还是领域 token（桌面端定义在 `src/stores/types.ts`，
 * 被 store / 通知 / 状态栏共用），这里刻意**不**去 import 它，而是各留一份
 * 同形的字符串联合 —— 让 store 反向依赖 UI 包会把依赖方向拧过来。
 *
 * 两份声明靠结构类型天然互通，靠宿主的
 * `src/components/agentre/__tests__/transcript-dto-contract.test.ts` 里的双向
 * 断言防漂移：任一侧加减一个状态都会在 `tsc` 阶段红。
 */
export type AgentStatus = "idle" | "running" | "waiting" | "error";

export interface AgentStatusStyle {
  /** 全大写的短标签；按约定不进 i18n（与 RUNNING/IDLE 这类状态码同类）。 */
  label: string;
  dotClassName: string;
  textClassName: string;
  pillClassName: string;
}

export const statusConfig: Record<AgentStatus, AgentStatusStyle> = {
  running: {
    label: "RUNNING",
    dotClassName: "bg-status-running",
    textClassName: "text-status-running-text",
    pillClassName: "bg-status-running-bg text-status-running-text",
  },
  waiting: {
    label: "WAITING",
    dotClassName: "bg-status-waiting",
    textClassName: "text-status-waiting-text",
    pillClassName: "bg-status-waiting-bg text-status-waiting-text",
  },
  idle: {
    label: "IDLE",
    dotClassName: "bg-status-idle",
    textClassName: "text-muted-foreground",
    pillClassName: "bg-secondary text-muted-foreground",
  },
  error: {
    label: "ERROR",
    dotClassName: "bg-status-error",
    textClassName: "text-status-error",
    pillClassName: "bg-destructive-soft text-status-error",
  },
};

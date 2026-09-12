import type * as React from "react";

/**
 * 执行归属三颗 pill 的**端口契约**。
 *
 * Agent 的候选是纯数据（名字 + 头像色），包里画得出来；机器与模型不是 —— 机器要
 * `useExecTargetCandidates(agentId, projectId)`（Wails + `remote.device.state`
 * 事件），模型要宿主自己的供应商目录与兼容判据。两者都留在宿主，包只把**同一形状
 * 的触发器类串**递过去，宿主把它并进自己触发器的 `cn()`：三颗 pill 摆在同一排，
 * 不能一颗是 tone 小 chip、一颗是 input 描边方框。
 */

export interface BoardAgentOption {
  id: number;
  name: string;
  /** 头像色 token，如 "agent-1"。 */
  color?: string;
}

export interface ExecPillContext {
  /** 共享 pill 形状；宿主并进自己触发器的 className。 */
  className: string;
  /** 已选中的 Agent；到这里一定不是 null（没选时那两颗 pill 是禁用态，端口不被调用）。 */
  agentId: number;
  /** 任务当前挂在哪个项目；`null` = 未归属。机器候选要靠它算「那台机器上的路径」。 */
  projectId: number | null;
  /** 提交在飞：其余字段一律可读不可改，这两颗也在「其余」里。 */
  disabled?: boolean;
}

/** 机器（执行目标）那一颗。 */
export type ExecTargetPort = (ctx: ExecPillContext) => React.ReactNode;

/** 模型那一颗。`backendId` 是已选中的执行目标，模型要用它的 backendType 过兼容判据。 */
export type ModelTargetPort = (
  ctx: ExecPillContext & { backendId: number | null },
) => React.ReactNode;

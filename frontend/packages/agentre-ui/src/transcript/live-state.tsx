import * as React from "react";

import type { TranscriptBlockToolPermission } from "./dto";

/**
 * 转录里少数几张卡片要读/写宿主的**实时流**状态（正在跑的那一轮）。
 *
 * 为什么不并进 `TranscriptPorts`：端口是一个模块级常量对象，往上面挂一个
 * `isStreamActive()` 方法，卡片只能拿到调用那一刻的值 —— 流结束时没有任何东西
 * 通知 React 重渲染，计划卡的按钮会永久停在禁用态。**反应式读取必须是 hook**，
 * 这就是这层单独存在的全部理由。写入侧（乐观更新）不需要反应式，所以是普通方法。
 *
 * 与端口的另一处不同：这层**有默认实现**。端口用 null + 抛错，因为「忘了接线」
 * 会表现成按钮点了没反应，只有用户能发现；而「宿主没有实时流」是一个合法形态
 * （agentre-server 第一版就是只读转录），此时 `false` / no-op 正是正确答案，
 * 不是接线遗漏。
 */

export interface MarkToolPermissionResolvedInput {
  sessionId: number;
  /** 这张卡所属的 assistant 消息 id —— 宿主据此定位要就地改写的那条流。 */
  assistantMessageId: number;
  toolPermission: TranscriptBlockToolPermission;
}

export interface TranscriptLiveState {
  /**
   * 会话是否有正在跑的流。**必须是 hook**：宿主要用自己的订阅机制实现
   * （桌面端是 zustand selector），流起停时消费方要跟着重渲染。
   *
   * 实现方无需处理 `sessionId` 缺失 —— 外层 `useIsStreamActive` 已经兜住。
   */
  useIsStreamActive(sessionId: number | undefined): boolean;

  /**
   * 把工具授权卡就地标成已决议（乐观更新）。权威状态仍由后端事件覆盖回来，
   * 所以这一步纯粹是把「点下去到后端回包」之间的空窗填上。
   */
  markToolPermissionResolved(input: MarkToolPermissionResolvedInput): void;
}

/**
 * 没有实时流的宿主用的默认实现。语义是「这个宿主不存在正在跑的流」，
 * 不是「查询失败」。
 */
export const noopTranscriptLiveState: TranscriptLiveState = {
  useIsStreamActive: () => false,
  markToolPermissionResolved: () => {},
};

const TranscriptLiveStateContext = React.createContext<TranscriptLiveState>(
  noopTranscriptLiveState,
);

/**
 * `value` 应当是**模块级常量**：它的成员被当作 hook 调用，每次渲染换一个新对象
 * 会让 hook 的调用来源在两次渲染之间漂移。桌面端的实现见
 * `src/components/agentre/transcript-live-state-desktop.ts`。
 */
export function TranscriptLiveStateProvider({
  value,
  children,
}: {
  value: TranscriptLiveState;
  children: React.ReactNode;
}) {
  return (
    <TranscriptLiveStateContext.Provider value={value}>
      {children}
    </TranscriptLiveStateContext.Provider>
  );
}

export function useIsStreamActive(sessionId: number | undefined): boolean {
  const state = React.useContext(TranscriptLiveStateContext);
  // 无条件调用：hook 数必须在两次渲染之间稳定，所以先取值再按 sessionId 兜底，
  // 不能写成 `sessionId ? state.useIsStreamActive(...) : false`。
  const active = state.useIsStreamActive(sessionId);

  // 问不出所属会话就谈不上「这个会话在跑」，禁用动作没有依据。
  return sessionId === undefined ? false : active;
}

export function useMarkToolPermissionResolved(): (
  input: MarkToolPermissionResolvedInput,
) => void {
  const state = React.useContext(TranscriptLiveStateContext);

  return state.markToolPermissionResolved;
}

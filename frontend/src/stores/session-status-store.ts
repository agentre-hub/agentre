// frontend/src/stores/session-status-store.ts
//
// session-status-store 是「session 运行时态」的唯一前端 source of truth。
// 任何想知道一个 session 当前 agentStatus / needsAttention / permissionMode 的
// 渲染处（tab / sidebar 行 / 详情 toolbar）都从这里读，不再各自维护 overlay。
//
// 写入路径有 4 条，全部往这里汇：
//   1. chat-agents-store.reload() 拉回 ListChatAgents 后 bulkUpsert 所有 sessions。
//   2. useChatSession.reload() 拉回 LoadChatSession 后 upsert 单条。
//   3. ChatPanel.doSend / Regenerate / Edit 成功后乐观 upsert("running")，
//      不依赖后端在 turn 起手时 emit session_status。
//   4. ChatStreamsHost 收到 session_status 事件后 upsert。
//
// 同值短路（upsert 内、bulkUpsert 逐条）保证只有真的字段变化才换 Map 引用，
// 避免 ListChatAgents 一刷就让所有订阅者集体 re-render。

import { create } from "zustand";

import type { chat_svc } from "../../wailsjs/go/models";

// SessionStatusPatch 类型定义已统一到 @/stores/types，此处 import 供内部用 + re-export。
import type { AgentStatus, SessionStatusPatch } from "./types";
export type { SessionStatusPatch };

// DoneEvent 对应 turn 结束时的各种终态事件。
// chat-streams-store 的 finishStream / consumeSteer 通过 bumpDone 写入；
// chat-panel 通过 useSessionStatus(sid)?.lastDoneEvent 拉取做副作用。
//
// 字段含义与 ChatStreamEvent 对齐（仅保留 done 路径需要的最小子集）：
//   - message: done/error/aborted 时后端随事件附带的最终（或 partial）ChatMessage（可选）
//   - error:   error 事件的错误文案
//   - steer_consumed 携带所有 ChatStreamEvent 字段（queuedIds / assistantMessage 等）
export type DoneEvent =
  | { kind: "done"; message?: chat_svc.ChatMessage; [key: string]: unknown }
  | {
      kind: "error";
      error?: string;
      message?: chat_svc.ChatMessage;
      [key: string]: unknown;
    }
  | { kind: "aborted"; message?: chat_svc.ChatMessage; [key: string]: unknown }
  | { kind: "closed"; [key: string]: unknown }
  | { kind: "steer_consumed"; [key: string]: unknown };

export type SessionStatusValue = {
  agentStatus: AgentStatus;
  needsAttention: boolean;
  // permissionMode 只有 claudecode 被动 ExitPlanMode 透传时才带值；
  // 详情页 Composer 据此回写 useChatSession session.permissionMode。
  permissionMode?: string;
  // bgRunning 为 true 时表示该 session 有后台运行中的子任务；
  // 由 reconcileBgRunningOnFinalize 写入，任何 upsert patch 若省略则保留旧值。
  bgRunning: boolean;
  // doneTick 每次 turn 结束（done/error/aborted/closed/steer_consumed）自增一次；
  // ChatPanel 订阅变化后调 reload，拿到后端写好的最终 blocks。
  doneTick: number;
  // lastDoneEvent 缓存最近一次结束事件，供 ChatPanel 做副作用（MarkRead/错误文案等）。
  lastDoneEvent: DoneEvent | null;
};

type State = {
  statuses: Map<number, SessionStatusValue>;
};

type Actions = {
  // upsert 写单条；同值短路。permissionMode 缺省 = 不携带（不会清掉旧 mode）。
  // doneTick/lastDoneEvent 字段从既有 entry 保留（只有 bumpDone 才修改它们）。
  upsert: (sessionId: number, patch: SessionStatusPatch) => void;
  // bulkUpsert 整批刷（ListChatAgents 的入口）。逐条同值短路，整批没变化时
  // 直接返回旧 state，订阅者不动。
  bulkUpsert: (entries: Iterable<[number, SessionStatusPatch]>) => void;
  remove: (sessionId: number) => void;
  // bumpDone 自增 doneTick 并记录最近事件；由 chat-streams-store 调用。
  bumpDone: (sessionId: number, ev: DoneEvent) => void;
  // 测试隔离用，生产代码不该调。
  __reset: () => void;
};

function isSamePatch(a: SessionStatusValue, b: SessionStatusPatch): boolean {
  return (
    a.agentStatus === b.agentStatus &&
    a.needsAttention === b.needsAttention &&
    (a.permissionMode ?? "") === (b.permissionMode ?? "") &&
    (b.bgRunning ?? a.bgRunning ?? false) === (a.bgRunning ?? false)
  );
}

export const useSessionStatusStore = create<State & Actions>((set) => ({
  statuses: new Map(),

  upsert: (sessionId, patch) =>
    set((state) => {
      const prev = state.statuses.get(sessionId);
      if (prev && isSamePatch(prev, patch)) return state;
      const statuses = new Map(state.statuses);
      statuses.set(sessionId, {
        agentStatus: patch.agentStatus,
        needsAttention: patch.needsAttention,
        permissionMode: patch.permissionMode,
        bgRunning: patch.bgRunning ?? prev?.bgRunning ?? false,
        doneTick: prev?.doneTick ?? 0,
        lastDoneEvent: prev?.lastDoneEvent ?? null,
      });
      return { statuses };
    }),

  bulkUpsert: (entries) =>
    set((state) => {
      let changed = false;
      const statuses = new Map(state.statuses);
      for (const [sid, patch] of entries) {
        const prev = statuses.get(sid);
        if (prev && isSamePatch(prev, patch)) continue;
        statuses.set(sid, {
          agentStatus: patch.agentStatus,
          needsAttention: patch.needsAttention,
          permissionMode: patch.permissionMode,
          bgRunning: patch.bgRunning ?? prev?.bgRunning ?? false,
          doneTick: prev?.doneTick ?? 0,
          lastDoneEvent: prev?.lastDoneEvent ?? null,
        });
        changed = true;
      }
      return changed ? { statuses } : state;
    }),

  remove: (sessionId) =>
    set((state) => {
      if (!state.statuses.has(sessionId)) return state;
      const statuses = new Map(state.statuses);
      statuses.delete(sessionId);
      return { statuses };
    }),

  bumpDone: (sessionId, ev) =>
    set((state) => {
      const prev = state.statuses.get(sessionId);
      const next: SessionStatusValue = {
        agentStatus: prev?.agentStatus ?? "idle",
        needsAttention: prev?.needsAttention ?? false,
        permissionMode: prev?.permissionMode,
        bgRunning: prev?.bgRunning ?? false,
        doneTick: (prev?.doneTick ?? 0) + 1,
        lastDoneEvent: ev,
      };
      const statuses = new Map(state.statuses);
      statuses.set(sessionId, next);
      return { statuses };
    }),

  __reset: () => set({ statuses: new Map() }),
}));

// selectSessionStatus 是给非 React 处 / 显式 store.getState() 用的选择器。
// React 组件直接走 useSessionStatus(sid) 一行更省。
export function selectSessionStatus(sessionId: number) {
  return (s: State): SessionStatusValue | null =>
    s.statuses.get(sessionId) ?? null;
}

// useSessionStatus 给单个 sid 订阅最小读路径。Map 整体变化但本 sid 对象引用
// 没变时不会触发本组件 re-render（依赖 zustand 的 Object.is 默认比较 + 我们
// upsert 内同值短路保证对象稳定）。
export function useSessionStatus(sessionId: number): SessionStatusValue | null {
  return useSessionStatusStore((s) =>
    sessionId > 0 ? (s.statuses.get(sessionId) ?? null) : null,
  );
}

// normalizeSessionSnapshot 在异步 RPC 快照进入运行态 store 前统一处理时序。
// LiveStream 已存在时，较早发起的 LoadChatSession/ListChatAgents 可能仍返回
// idle/error；这类快照不能覆盖实时事件已经写入的 running/waiting。其它字段仍取
// 快照，避免把 bgRunning 等独立状态一起冻结。
export function normalizeSessionSnapshot(
  sessionId: number,
  patch: SessionStatusPatch,
  hasActiveStream: boolean,
): SessionStatusPatch {
  if (!hasActiveStream) return patch;
  const live = useSessionStatusStore.getState().statuses.get(sessionId);
  if (live?.agentStatus !== "running" && live?.agentStatus !== "waiting") {
    return patch;
  }
  if (patch.agentStatus === "running" || patch.agentStatus === "waiting") {
    return patch;
  }
  return {
    ...patch,
    agentStatus: live.agentStatus,
    needsAttention: live.needsAttention,
  };
}

// markSessionRunning 在 Send / Regenerate / Edit 成功返回后乐观把 session 翻成
// running 态。后端落库已经是 running,但 turn 起手没 emit session_status 事件,
// 不补一刀的话 tab / toolbar / sidebar 读 session-status-store 会停在 idle。
// permissionMode 取 store 当前值,避免覆盖刚 set 的 plan/default 等。
export function markSessionRunning(sessionId: number): void {
  if (!sessionId) return;
  const prev = useSessionStatusStore.getState().statuses.get(sessionId);
  useSessionStatusStore.getState().upsert(sessionId, {
    agentStatus: "running",
    needsAttention: false,
    permissionMode: prev?.permissionMode,
  });
}

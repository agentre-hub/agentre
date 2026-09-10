// frontend/src/stores/queued-messages-store.ts
//
// queued-messages-store 是「当前 turn 进行中排队消息」的独立 store。
// 与 chat-streams-store 解耦，职责单一：持有 append / consume / clear 操作。
//
// 状态迁移本身不在这里：入队 / 消费 / 撤回都归共享包的 steer-queue 纯归约
// （`@agentre-hub/agentre-ui`），浏览器控制台用的是同一份。这个 store 只负责
// 「按 sessionId 存」与「被丢弃的那一份放哪儿」这两件宿主自己的事。
//
// 消费方：
//   - chat-panel.tsx: 读 queuedBySession.get(sid) 渲染 QueuedMessagesBar；
//     doEnqueue 调 append；doCancelQueued 调 consume（按 id 过滤）。
//   - chat-streams-store.finishStream: 调 markDropped 暂存该 session 的残留。
//   - chat-streams-store.consumeSteer: 调 consume（按 ids 过滤）消费掉被后端取走的条目。

import {
  clearSteerQueue,
  consumeSteers,
  emptySteerQueue,
  enqueueSettledSteer,
  type QueuedItem,
  type SteerQueueState,
} from "@agentre-hub/agentre-ui";
import { create } from "zustand";

// QueuedMessage 就是共享包里那一条：桌面端这一路在入队应答里就拿到了句柄
// （EnqueueResponse.queuedId/cancellable），所以永远是「已落定」的那一档。
export type QueuedMessage = QueuedItem;

// DroppedQueue 记录「回合收尾时还没被 AI 消费、原本会静默丢弃」的排队条目。
// sessionId 记录来源会话，restoreDropped 按它把条目放回原队列。
export type DroppedQueue = {
  sessionId: number;
  items: QueuedMessage[];
  at: number;
} | null;

type State = {
  queuedBySession: Map<number, SteerQueueState>;
  // 最近一次被「标记为丢弃」的排队条目（同一时刻最多一条）。null = 无。
  dropped: DroppedQueue;
};

type Actions = {
  append: (sessionId: number, msg: QueuedMessage) => void;
  // consume 移除指定 ids（不传则取出全部并清空）。返回被移除的条目（供 doCancelQueued 使用）。
  consume: (sessionId: number, ids?: string[]) => QueuedMessage[];
  // clear 清空指定 session 的所有排队条目。
  clear: (sessionId: number) => void;
  // markDropped 把指定 session 的排队条目整体挪进 dropped 并清空队列（回合收尾
  // 未消费路径）。队列为空时 no-op，不清任何东西，也不覆盖已有 dropped。
  markDropped: (sessionId: number) => void;
  // dismissDropped 丢弃 dropped 记录（用户选择「丢弃」）。
  dismissDropped: () => void;
  // restoreDropped 把 dropped 条目按原 session 追加回排队队列（用户选择「恢复为草稿」）。
  restoreDropped: () => void;
  // 测试隔离用，生产代码不该调。
  __reset: () => void;
};

/** 这条会话此刻的队列；没有就是空的那一份。 */
function queueOf(state: State, sessionId: number): SteerQueueState {
  return state.queuedBySession.get(sessionId) ?? emptySteerQueue;
}

/**
 * 写回一条会话的队列；空队列直接摘掉这一格（`has(sid)` 的语义因此不变）。
 *
 * 没有实际变化时交回**原来那张 Map**：订阅这张表的组件按引用判重渲染，每次都造
 * 一张新的会让「清一个根本没有队列的会话」也把整个 ChatPanel 重渲染一遍。
 */
function withQueue(
  state: State,
  sessionId: number,
  next: SteerQueueState,
): Map<number, SteerQueueState> {
  const current = state.queuedBySession.get(sessionId);
  if (next === current) return state.queuedBySession;
  if (next.items.length === 0 && current === undefined) {
    return state.queuedBySession;
  }
  const map = new Map(state.queuedBySession);
  if (next.items.length === 0) map.delete(sessionId);
  else map.set(sessionId, next);
  return map;
}

export const useQueuedMessagesStore = create<State & Actions>((set, get) => ({
  queuedBySession: new Map(),
  dropped: null,

  append: (sessionId, msg) =>
    set((state) => ({
      queuedBySession: withQueue(
        state,
        sessionId,
        enqueueSettledSteer(queueOf(state, sessionId), msg),
      ),
    })),

  consume: (sessionId, ids) => {
    const current = queueOf(get(), sessionId);
    if (ids === undefined) {
      // 全部取出并清空
      set((state) => ({
        queuedBySession: withQueue(
          state,
          sessionId,
          clearSteerQueue(queueOf(state, sessionId)),
        ),
      }));
      return current.items;
    }
    const idSet = new Set(ids);
    const removed = current.items.filter((m) => idSet.has(m.id));
    set((state) => ({
      queuedBySession: withQueue(
        state,
        sessionId,
        consumeSteers(
          queueOf(state, sessionId),
          ids.map((id) => ({ queuedId: id })),
        ),
      ),
    }));
    return removed;
  },

  clear: (sessionId) =>
    set((state) => ({
      queuedBySession: withQueue(
        state,
        sessionId,
        clearSteerQueue(queueOf(state, sessionId)),
      ),
    })),

  markDropped: (sessionId) =>
    set((state) => {
      const items = queueOf(state, sessionId).items;
      if (items.length === 0) return state;
      return {
        queuedBySession: withQueue(state, sessionId, emptySteerQueue),
        dropped: { sessionId, items, at: Date.now() },
      };
    }),

  dismissDropped: () => set({ dropped: null }),

  restoreDropped: () =>
    set((state) => {
      if (!state.dropped) return state;
      const { sessionId, items } = state.dropped;
      const next = items.reduce(
        (queue, item) => enqueueSettledSteer(queue, item),
        queueOf(state, sessionId),
      );
      return {
        queuedBySession: withQueue(state, sessionId, next),
        dropped: null,
      };
    }),

  __reset: () => set({ queuedBySession: new Map(), dropped: null }),
}));

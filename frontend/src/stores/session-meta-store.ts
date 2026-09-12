// frontend/src/stores/session-meta-store.ts
//
// session-meta-store 缓存「显示一个 session tab 所需的静态字段」, 比如 agentColor /
// agentName / projectId。这些字段在 ChatSessionLite (ListChatAgents) 里没有 —
// 只能从 ChatSessionDetail (LoadChatSession) 拿。Tab strip 不主动 LoadSession,
// 但 ChatPanelHost 给每个 tab 都 mount 了 ChatPanel, 后者用 useChatSession 拉详情,
// 顺手往这里 setMeta, 让 TabStrip 在 detail 到位后能渲染出 agent 色 / 项目色等线索。
//
// 不持久化: 重启后由 useChatSession 重新填。

import { create } from "zustand";

import type { SessionMetaSnapshot } from "./types";

// SessionMeta 是 session-meta-store 持有的静态字段快照。
// 类型定义已统一到 @/stores/types（SessionMetaSnapshot），此处做别名 re-export。
export type SessionMeta = SessionMetaSnapshot;

type State = {
  metas: Map<number, SessionMeta>;
};

type Actions = {
  // setMeta 是 replace 语义: 调用方传完整 SessionMeta, 整条覆盖。
  // useChatSession 拉到 ChatSessionDetail 后用这个, 因为 detail 含完整字段(包括 projectId)。
  setMeta: (sessionId: number, meta: SessionMeta) => void;
  // bulkUpsert 是 merge 语义: 每条 patch 与已有 meta 合并, 未提供的字段保留。
  // chat-agents-store 从 ListChatAgents 批量灌的时候用 —— 它只有 ChatSessionLite 的子集,
  // 不能整条 replace, 否则会把 useChatSession 写好的 projectId 等字段擦掉。
  bulkUpsert: (entries: Iterable<[number, Partial<SessionMeta>]>) => void;
  // reset 仅用于测试隔离, 生产代码不该调。
  __reset: () => void;
};

function isSameMeta(a: SessionMeta, b: SessionMeta): boolean {
  return (
    a.agentId === b.agentId &&
    a.agentName === b.agentName &&
    a.agentColor === b.agentColor &&
    (a.projectId ?? -1) === (b.projectId ?? -1) &&
    a.title === b.title &&
    (a.lastMessageAt ?? 0) === (b.lastMessageAt ?? 0) &&
    (a.lastReadAt ?? 0) === (b.lastReadAt ?? 0) &&
    (a.permissionModeAtLaunch ?? "") === (b.permissionModeAtLaunch ?? "")
  );
}

export const useSessionMetaStore = create<State & Actions>((set) => ({
  metas: new Map(),
  setMeta: (sessionId, meta) =>
    set((state) => {
      const prev = state.metas.get(sessionId);
      if (prev && isSameMeta(prev, meta)) {
        return state;
      }
      const metas = new Map(state.metas);
      metas.set(sessionId, meta);
      return { metas };
    }),
  bulkUpsert: (entries) =>
    set((state) => {
      let changed = false;
      const metas = new Map(state.metas);
      for (const [sid, patch] of entries) {
        const prev = metas.get(sid);
        // merge: 已有 meta 在底, patch 字段覆盖在上。
        // 首次 upsert 时 prev=undefined, patch 必须自带必填字段(agentId/agentName/agentColor/title)。
        const merged = (prev ? { ...prev, ...patch } : patch) as SessionMeta;
        if (prev && isSameMeta(prev, merged)) continue;
        metas.set(sid, merged);
        changed = true;
      }
      return changed ? { metas } : state;
    }),
  __reset: () => set({ metas: new Map() }),
}));

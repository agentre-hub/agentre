// frontend/src/stores/session-meta-store.ts
//
// session-meta-store 缓存「显示一个 session tab 所需的静态字段」: 标题 / agentId /
// agentName / agentColor / projectId / 活动与已读时间。三条路往里写:
//   - useChatSession 拉到 ChatSessionDetail (LoadChatSession) 后 setMeta —— 只有被激活、
//     mount 了 ChatPanel 的标签才走这条;
//   - 会话列表 (chat-agents-store / session-index-store) 把已加载那几页 bulkUpsert 进来;
//   - loadMissing 给剩下的 (典型是重启后恢复、从没激活过、又不在已加载页里的标签)
//     按 id 批量取索引行 (ListChatSessionsByIDs, 不带消息) 补上。
//
// 不持久化: 重启后由上面三条路重新填。

import { create } from "zustand";

import { ListChatSessionsByIDs } from "../../wailsjs/go/app/App";
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
  // loadMissing 把缓存里还没有的那些 id 一次批量取回来。每个 id 一辈子只取一次:
  // 会话已不存在或读取失败时安静作罢, 标签保持占位, 不反复去取。
  loadMissing: (sessionIds: Iterable<number>) => Promise<void>;
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

/** 会话索引行（ListChatIndexSessions / ListChatSessionsByIDs 同一种行）里 meta 关心的字段。 */
export type IndexRowMeta = {
  agentId?: number;
  projectId?: number;
  title?: string;
  lastMessageAt?: number;
  lastReadAt?: number;
};

/**
 * 一条索引行 → bulkUpsert 用的 meta 补丁，两处取索引行的入口共用这一份投影。
 * agentId / projectId 是索引的两个分组维度：projectId 为 0 即「随手对话」——这是个有意义
 * 的取值，所以载荷**没提到**它们时不写这个键，而不是补一个 0 把会话搬到别的组去。
 */
export function metaPatchFromIndexRow(s: IndexRowMeta): Partial<SessionMeta> {
  return {
    ...(s.agentId === undefined ? {} : { agentId: s.agentId }),
    ...(s.projectId === undefined ? {} : { projectId: s.projectId }),
    title: s.title || "",
    lastMessageAt: s.lastMessageAt ?? 0,
    lastReadAt: s.lastReadAt ?? 0,
  };
}

// loadMissing 已经发出去过的 id。挂在模块上而不是 state 里: 它不驱动渲染, 只给几个
// 同时调 loadMissing 的投影 (标签条 / 溢出菜单 / 快捷键作用域) 去重。
const requested = new Set<number>();

export const useSessionMetaStore = create<State & Actions>((set, get) => ({
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
  loadMissing: async (sessionIds) => {
    const metas = get().metas;
    const ids: number[] = [];
    for (const sid of sessionIds) {
      if (sid <= 0 || metas.has(sid) || requested.has(sid)) continue;
      requested.add(sid);
      ids.push(sid);
    }
    if (ids.length === 0) return;
    let sessions;
    try {
      ({ sessions } = await ListChatSessionsByIDs({ ids }));
    } catch {
      return;
    }
    // 取的途中面板可能已经 setMeta 了较新的详情: 只填仍然空着的, 不拿索引行盖它。
    const current = get().metas;
    get().bulkUpsert(
      (sessions ?? [])
        .filter((s) => !current.has(s.id))
        .map((s): [number, Partial<SessionMeta>] => [
          s.id,
          metaPatchFromIndexRow(s),
        ]),
    );
  },
  __reset: () => {
    requested.clear();
    set({ metas: new Map() });
  },
}));

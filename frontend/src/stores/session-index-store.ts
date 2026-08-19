// frontend/src/stores/session-index-store.ts
//
// 单一会话索引的分页来源（规格：docs/specs/2026-08-16-unified-chat-index.md）。
//
// 它是「对话 / 项目」合并后**唯一**的会话清单通路，取代了此前那两条各说各话的：
//   - ListChatAgents 每个 agent 只给前 5 条 → 并起来是窗口，不是全量，「按时间」无源；
//   - ProjectListSessions 返回另一个形状（无 bgRunning、无 project_id）+ 1 秒轮询
//     → 同一条会话在两个页面显示不一样（问题 3/4）。
//
// 职责边界很窄，是这个设计的关键：
//   - **会话的事实**（标题 / 活动时间 / 已读 / agent / 项目 / 运行态）一律写进
//     session-meta-store 与 session-status-store，与对话侧同一套 store；
//   - **本 store 只留 id 的顺序**，加上翻页需要的 total / hasMore。
//
// 于是 attention / 未读只算一次（走 useSessionAttentionList），项目页那段内联
// computeAttention 与它的绕行注释一并消失。
import { create } from "zustand";

import { ListChatIndexSessions } from "../../wailsjs/go/app/App";
import { hasSessionStream, useChatStreamsStore } from "./chat-streams-store";
import { useSessionMetaStore, type SessionMeta } from "./session-meta-store";
import {
  normalizeSessionSnapshot,
  useSessionStatusStore,
} from "./session-status-store";
import type { AgentStatus, SessionStatusPatch } from "./types";

/** 与后端 `chat_svc.SessionIndexScope` 同源。 */
export type IndexScope =
  | { kind: "recent" }
  | { kind: "free" }
  | { kind: "project"; projectID: number };

export function recentScope(): IndexScope {
  return { kind: "recent" };
}
export function freeScope(): IndexScope {
  return { kind: "free" };
}
export function projectScope(projectID: number): IndexScope {
  return { kind: "project", projectID };
}

/** 页缓存的键。项目之间必须互不相同，否则两个项目组会共用一份 id 列表。 */
export function scopeKey(scope: IndexScope): string {
  return scope.kind === "project" ? `project:${scope.projectID}` : scope.kind;
}

export const INDEX_PAGE_SIZE = 20;
/** 与后端 listAgentSessionsMaxLimit 同值；超过会被服务端裁掉，前端先自己收住。 */
const INDEX_MAX_LIMIT = 100;

export type IndexPage = {
  /** 已加载的会话 id，保持服务端给的顺序（最近活动优先）。 */
  ids: number[];
  total: number;
  hasMore: boolean;
  loading: boolean;
  error: string | null;
};

const emptyPage: IndexPage = {
  ids: [],
  total: 0,
  hasMore: false,
  loading: false,
  error: null,
};

type State = { pages: Map<string, IndexPage> };

type Actions = {
  loadFirstPage: (scope: IndexScope, limit?: number) => Promise<void>;
  loadMore: (scope: IndexScope, limit?: number) => Promise<void>;
  /**
   * 把**已经加载过**的 scope 各自重拉一遍。挂在 reloadSidebarSources 下面 ——
   * 没被打开过的项目组不会因为别处发了一轮消息就凭空发 RPC。
   */
  reloadLoaded: () => Promise<void>;
  // 测试隔离用，生产代码不该调。
  __reset: () => void;
};

// 每个 scope 一把 inflight 锁：两个项目组同时首屏加载不该互相 dedupe 掉。
const inflight = new Map<string, Promise<void>>();

type IndexSessionLite = {
  id: number;
  agentId?: number;
  projectId?: number;
  title?: string;
  status?: string;
  needsAttention?: boolean;
  bgRunning?: boolean;
  lastMessageAt?: number;
  lastReadAt?: number;
};

/**
 * 把一页会话摊进两个 store。
 *
 * 运行态经 normalizeSessionSnapshot —— 异步 RPC 拿回来的可能已经是旧快照，
 * 直播流还在跑时不许把 running 拍回 idle（与 chat-agents-store 同一口径）。
 */
function fanOutToStores(sessions: IndexSessionLite[]): void {
  const streamsState = useChatStreamsStore.getState();
  const statusEntries: [number, SessionStatusPatch][] = [];
  const metaEntries: [number, Partial<SessionMeta>][] = [];

  for (const s of sessions) {
    statusEntries.push([
      s.id,
      normalizeSessionSnapshot(
        s.id,
        {
          agentStatus: (s.status as AgentStatus) || "idle",
          needsAttention: s.needsAttention ?? false,
          bgRunning: s.bgRunning ?? false,
        },
        hasSessionStream(streamsState, s.id),
      ),
    ]);
    metaEntries.push([
      s.id,
      {
        // agentId / projectId 是索引的两个分组维度，缺一不可：按一维分组时行首要
        // 放另一维（决策 4/5）。projectId 为 0 即「随手对话」——这是个有意义的取值，
        // 所以载荷**没提到**它时不写这个键，而不是补一个 0 把会话搬到别的组去。
        ...(s.agentId === undefined ? {} : { agentId: s.agentId }),
        ...(s.projectId === undefined ? {} : { projectId: s.projectId }),
        title: s.title || "",
        lastMessageAt: s.lastMessageAt ?? 0,
        lastReadAt: s.lastReadAt ?? 0,
      },
    ]);
  }

  useSessionStatusStore.getState().bulkUpsert(statusEntries);
  useSessionMetaStore.getState().bulkUpsert(metaEntries);
}

function clampLimit(limit: number): number {
  if (!Number.isFinite(limit) || limit <= 0) return INDEX_PAGE_SIZE;
  return Math.min(limit, INDEX_MAX_LIMIT);
}

/** id 顺序是否一致。变了才值得换 ids 数组的引用。 */
function sameIDs(a: readonly number[], b: readonly number[]): boolean {
  if (a === b) return true;
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}

export const useSessionIndexStore = create<State & Actions>((set, get) => {
  function patchPage(key: string, patch: Partial<IndexPage>): void {
    set((state) => {
      const pages = new Map(state.pages);
      pages.set(key, { ...(pages.get(key) ?? emptyPage), ...patch });
      return { pages };
    });
  }

  /**
   * 落一页结果。内容与缓存里那份完全一致时**原样返回 state** —— 不换 pages Map,
   * 订阅它的 useIndexGroups 就不会重算所有组和所有行。
   *
   * 为什么值得: reloadLoaded 每轮对话被调两次(起手 / 落定), 而绝大多数轮次这些 scope
   * 一条都没变。
   */
  function commitPage(
    key: string,
    next: { ids: number[]; total: number; hasMore: boolean },
  ): void {
    set((state) => {
      const prev = state.pages.get(key);
      const idsUnchanged = !!prev && sameIDs(prev.ids, next.ids);
      if (
        prev &&
        idsUnchanged &&
        prev.total === next.total &&
        prev.hasMore === next.hasMore &&
        !prev.loading &&
        prev.error === null
      ) {
        return state;
      }
      const pages = new Map(state.pages);
      pages.set(key, {
        ...(prev ?? emptyPage),
        ...next,
        // 只有 total/hasMore 变化时保住 ids 的引用, 让 useGroupRows 那层的 memo 也不动。
        ids: idsUnchanged ? prev.ids : next.ids,
        loading: false,
        error: null,
      });
      return { pages };
    });
  }

  function fetchPage(
    scope: IndexScope,
    offset: number,
    limit: number,
    append: boolean,
  ): Promise<void> {
    const key = scopeKey(scope);
    const existing = inflight.get(key);
    if (existing) return existing;

    // loading 的语义是「这一格首屏还没数据」, 不是「有个请求在飞」: 已经有页缓存时
    // (重拉 / 翻页都走这条)不再翻它 —— 换 pages Map 会让 useIndexGroups 重算全部组和行,
    // 而这个字段目前没有任何渲染处在读。
    if (!get().pages.has(key)) patchPage(key, { loading: true, error: null });
    const run = (async () => {
      try {
        const resp = await ListChatIndexSessions({
          scope: scope.kind,
          projectId: scope.kind === "project" ? scope.projectID : 0,
          offset,
          limit,
        } as Parameters<typeof ListChatIndexSessions>[0]);
        const sessions = (resp?.sessions ?? []) as IndexSessionLite[];
        fanOutToStores(sessions);

        const incoming = sessions.map((s) => s.id);
        const prev = append ? (get().pages.get(key)?.ids ?? []) : [];
        // 翻页期间有新会话插到最前面会把窗口整体后移，第二页第一条可能就是第一页的
        // 最后一条 —— 去重而不是让它在列表里出现两次。
        const seen = new Set(prev);
        const ids = [...prev, ...incoming.filter((id) => !seen.has(id))];

        commitPage(key, {
          ids,
          total: resp?.total ?? ids.length,
          hasMore: resp?.hasMore ?? false,
        });
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        patchPage(key, { loading: false, error: msg });
      } finally {
        inflight.delete(key);
      }
    })();
    inflight.set(key, run);
    return run;
  }

  return {
    pages: new Map(),

    loadFirstPage: (scope, limit = INDEX_PAGE_SIZE) =>
      fetchPage(scope, 0, clampLimit(limit), false),

    loadMore: (scope, limit = INDEX_PAGE_SIZE) => {
      const page = get().pages.get(scopeKey(scope));
      if (page && !page.hasMore) return Promise.resolve();
      return fetchPage(scope, page?.ids.length ?? 0, clampLimit(limit), true);
    },

    reloadLoaded: async () => {
      const keys = [...get().pages.keys()];
      await Promise.all(
        keys.map((key) => {
          const page = get().pages.get(key);
          // 重拉时把已经翻出来的那么多条一次拉回来，避免用户滚到一半被截回首屏。
          // 超过单次上限的部分会被截掉 —— 侧栏没有这么长的组，时间轴滚回去即可。
          const limit = clampLimit(page?.ids.length ?? INDEX_PAGE_SIZE);
          return fetchPage(parseScopeKey(key), 0, limit, false);
        }),
      );
    },

    __reset: () => {
      inflight.clear();
      set({ pages: new Map() });
    },
  };
});

function parseScopeKey(key: string): IndexScope {
  if (key === "free") return freeScope();
  if (key.startsWith("project:")) {
    return projectScope(Number(key.slice("project:".length)));
  }
  return recentScope();
}

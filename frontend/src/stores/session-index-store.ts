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

/**
 * 与后端 `chat_svc.SessionIndexScope` 同源。
 *
 * `keyword` 是索引搜索框里那个词，和「按哪一维分组」正交：它不换一条查询，只是把
 * 同一条查询收窄。搜索走取数而不是前端过滤，是因为前端手上只有首屏那一页（项目组
 * 5 条 / 时间轴 30 条），在那上面做匹配等于「只搜得到最近几条」。
 */
export type IndexScope =
  | { kind: "recent"; keyword: string }
  | { kind: "free"; keyword: string }
  | { kind: "project"; projectID: number; keyword: string }
  | { kind: "machine"; deviceID: number; keyword: string }
  | { kind: "agent"; agentID: number; keyword: string };

export function recentScope(keyword = ""): IndexScope {
  return { kind: "recent", keyword };
}
export function freeScope(keyword = ""): IndexScope {
  return { kind: "free", keyword };
}
export function projectScope(projectID: number, keyword = ""): IndexScope {
  return { kind: "project", projectID, keyword };
}
/**
 * 某一台机器上的会话。`deviceID = 0` 是**本机**（chat_entity.Session 的约定），
 * 不是「不限机器」—— 它和别的机器一样是一格，只是绝大多数会话都落在它上面。
 */
export function machineScope(deviceID: number, keyword = ""): IndexScope {
  return { kind: "machine", deviceID, keyword };
}
/**
 * 某个 agent 名下的会话。**只在搜索时用**：不搜索时 Agent 轴的会话由 ListChatAgents
 * 顺带给出（每个 agent 前 5 条），不必为了摆一屏多发 N 个 RPC；一开搜那个窗口就不够
 * 用了，得按 agent 各查一遍全量。
 */
export function agentScope(agentID: number, keyword = ""): IndexScope {
  return { kind: "agent", agentID, keyword };
}

/**
 * 页缓存的键。项目之间必须互不相同，否则两个项目组会共用一份 id 列表；
 * 关键词同样进 key —— 否则搜索结果会盖掉未搜索的那份缓存，清空搜索框时整棵列表
 * 会先塌成搜索结果再重拉。
 */
export function scopeKey(scope: IndexScope): string {
  const base =
    scope.kind === "project"
      ? `project:${scope.projectID}`
      : scope.kind === "machine"
        ? `machine:${scope.deviceID}`
        : scope.kind === "agent"
          ? `agent:${scope.agentID}`
          : scope.kind;
  return scope.keyword ? `${base}?q=${scope.keyword}` : base;
}

export const INDEX_PAGE_SIZE = 20;
/** 与后端 listAgentSessionsMaxLimit 同值；超过会被服务端裁掉，前端先自己收住。 */
const INDEX_MAX_LIMIT = 100;

export type IndexPage = {
  /**
   * 这一页是哪个 scope 取回来的。刷新时照它原样重拉 —— 此前是把 key 反解回 scope，
   * 而那个反解不认 `machine:`，机器组于是每次刷新都去重拉 recent、自己永远停在首屏。
   */
  scope: IndexScope;
  /** 已加载的会话 id，保持服务端给的顺序（最近活动优先）。 */
  ids: number[];
  total: number;
  hasMore: boolean;
  loading: boolean;
  error: string | null;
};

const emptyPage: IndexPage = {
  scope: recentScope(),
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
    next: { scope: IndexScope; ids: number[]; total: number; hasMore: boolean },
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

    // 上一个关键词的结果不再有用：一边打字一边搜的话，每敲一个字符都会多留一份
    // 页缓存。未搜索的那份（keyword 为空）刻意留着 —— 清空搜索框要立刻回到原列表，
    // 不该再等一轮 RPC。
    if (scope.keyword) pruneOtherKeywords(scope.keyword);

    // loading 的语义是「这一格首屏还没数据」, 不是「有个请求在飞」: 已经有页缓存时
    // (重拉 / 翻页都走这条)不再翻它 —— 换 pages Map 会让 useIndexGroups 重算全部组和行,
    // 而这个字段目前没有任何渲染处在读。
    if (!get().pages.has(key)) patchPage(key, { loading: true, error: null });
    const run = (async () => {
      try {
        const resp = await ListChatIndexSessions({
          scope: scope.kind,
          projectId: scope.kind === "project" ? scope.projectID : 0,
          // 0 是本机、是合法值：这里必须原样发出去，被吞成 undefined 会让服务端
          // 当成漏传（它只拒负数）。
          deviceId: scope.kind === "machine" ? scope.deviceID : 0,
          agentId: scope.kind === "agent" ? scope.agentID : 0,
          keyword: scope.keyword,
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
          scope,
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

  /** 丢掉「关键词不是 keep 的那一个」的搜索页。未搜索的页（keyword 为空）不动。 */
  function pruneOtherKeywords(keep: string): void {
    set((state) => {
      let changed = false;
      const pages = new Map(state.pages);
      for (const [key, page] of pages) {
        if (page.scope.keyword && page.scope.keyword !== keep) {
          pages.delete(key);
          inflight.delete(key);
          changed = true;
        }
      }
      return changed ? { pages } : state;
    });
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
      const loaded = [...get().pages.values()];
      await Promise.all(
        loaded.map((page) => {
          // 重拉时把已经翻出来的那么多条一次拉回来，避免用户滚到一半被截回首屏。
          // 超过单次上限的部分会被截掉 —— 侧栏没有这么长的组，时间轴滚回去即可。
          const limit = clampLimit(page.ids.length || INDEX_PAGE_SIZE);
          return fetchPage(page.scope, 0, limit, false);
        }),
      );
    },

    __reset: () => {
      inflight.clear();
      set({ pages: new Map() });
    },
  };
});

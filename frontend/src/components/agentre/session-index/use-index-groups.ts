// frontend/src/components/agentre/session-index/use-index-groups.ts
//
// 把「当前轴」变成一串待渲染的组。这是单一会话索引的装配层
// （规格：docs/specs/2026-08-16-unified-chat-index.md）。
//
// 三个轴的组**来源不同**，这是有意的 —— 每个轴各自有一条能给出完整答案的查询：
//   - 项目：组顺序 = 项目树摊平；每组的会话 = ListChatIndexSessions(scope=project)
//           末尾常驻「随手对话」组 = scope=free（决策 6，空态也在）
//   - Agent：组顺序 = 置顶优先 + 最近活动；每组的会话 = ListChatAgents 的
//           前 5 条 + attention（其余走「查看全部 N」翻页）
//   - 时间：无分组，单个平铺组 = scope=recent
//
// 本 hook 只产出「组 → 有序的会话 id」。**行长什么样不在这里**：行的字段一律从
// session-meta-store / session-status-store / attention 现算，与对话侧同一套投影。
//
// 组骨架摆好之后，**组内的分配与排序交给共享投影**（`@agentre-hub/agentre-ui` 的
// buildAxisGroups，经 index-projection.ts 适配）—— 桌面端与 agentre-server 的
// 「组怎么分、行怎么排」只该有一份实现（规格 2026-08-18「共享包承载什么」）。
import * as React from "react";

import { flattenProjectTree, type IndexAxis } from "@/lib/session-axis";
import { useChatAgentsStore } from "@/stores/chat-agents-store";
import {
  agentScope,
  freeScope,
  machineScope,
  projectScope,
  recentScope,
  scopeKey,
  useSessionIndexStore,
  type IndexScope,
} from "@/stores/session-index-store";
import { useSessionMetaStore } from "@/stores/session-meta-store";

import { projectIndexGroups, type IndexGroup } from "./index-projection";
import type { MachineRosterEntry } from "./machine-roster";

import type { app } from "../../../../wailsjs/go/models";

export type { IndexGroup, IndexGroupKind } from "./index-projection";

/** 每个项目组 / 随手对话组首屏展开的条数，与 Agent 组的前 5 条对齐。 */
export const GROUP_PAGE_SIZE = 5;
/** 时间轴首屏条数。它没有组头吃掉纵向空间，可以多给一些。 */
export const TIME_PAGE_SIZE = 30;
/**
 * 搜索时每组首屏条数。比 GROUP_PAGE_SIZE 大得多是有意的：搜索结果的「首屏」就是
 * 答案本身，让人为了看第 6 条命中再点一次「查看全部」没有道理。超出的部分照旧走
 * 「查看全部 N」，那个 N 此时也是过滤后的总数。
 */
export const SEARCH_PAGE_SIZE = 50;

/** 某个 scope 首屏拉几条。 */
function firstPageSize(scope: IndexScope): number {
  if (scope.keyword) return SEARCH_PAGE_SIZE;
  return scope.kind === "recent" ? TIME_PAGE_SIZE : GROUP_PAGE_SIZE;
}

/**
 * 某个轴要用到哪些 scope。effect 据此按需拉取，不多发一个 RPC。
 *
 * `keyword` 与「按哪一维分组」正交：它不换查询，只把每条 scope 收窄，所以每一条都得
 * 带上它 —— 漏掉哪一个，那一组在搜索时给出的就是未过滤的列表，混在过滤后的兄弟组
 * 之间。搜索之所以必须走取数，是因为前端手上只有首屏那一页（项目组 5 条 / 时间轴
 * 30 条），在那上面做匹配等于「只搜得到最近几条」。
 */
export function scopesForAxis(
  axis: IndexAxis,
  projectIDs: readonly number[],
  deviceIDs: readonly number[] = [],
  agentIDs: readonly number[] = [],
  keyword = "",
): IndexScope[] {
  if (axis === "time") return [recentScope(keyword)];
  // Agent 轴平时的会话由 ListChatAgents 直接给（每个 agent 前 5 条），不必为了摆一屏
  // 多发 N 个 RPC；一开搜那个窗口就不够用了，得按 agent 各查一遍全量。
  if (axis === "agent") {
    return keyword ? agentIDs.map((id) => agentScope(id, keyword)) : [];
  }
  // 机器轴每台机器一条查询，与项目轴同形：组的总数只有取数方数得出来，
  // 客户端把 recent 那一页分桶只能得到「这一页里恰好有几条」。
  if (axis === "machine")
    return deviceIDs.map((id) => machineScope(id, keyword));
  return [
    ...projectIDs.map((id) => projectScope(id, keyword)),
    freeScope(keyword),
  ];
}

/**
 * agent 行的顺序：置顶优先，同段内按「名下会话的最近活动」倒序，从没活动过的沉底。
 * 与合并前对话页的 agentRowByActivity 同一口径。
 */
function orderAgents(
  agents: readonly { id: number; pinned?: boolean; sessionIds?: number[] }[],
  metas: ReadonlyMap<number, { lastMessageAt?: number }>,
): number[] {
  const rows = agents.map((a) => {
    let ts = 0;
    for (const sid of a.sessionIds ?? []) {
      const at = metas.get(sid)?.lastMessageAt ?? 0;
      if (at > ts) ts = at;
    }
    return { id: a.id, pinned: !!a.pinned, ts };
  });
  const byActivity = (x: { ts: number }, y: { ts: number }) => {
    if (x.ts === y.ts) return 0;
    if (x.ts === 0) return 1; // 从没活动过的沉底，而不是排到最新的前面
    if (y.ts === 0) return -1;
    return y.ts - x.ts;
  };
  return [
    ...rows.filter((r) => r.pinned).sort(byActivity),
    ...rows.filter((r) => !r.pinned).sort(byActivity),
  ].map((r) => r.id);
}

/**
 * agent 名下已知的会话 id：前 5 条 + attention，去重。
 * 组内按最近活动倒序这一步不在这里 —— 那是共享投影的事（见 index-projection.ts），
 * 这里只保证「哪些 id 属于这一组」，顺序留给它。
 */
function agentSessionIDs(agent: {
  sessions?: { id: number }[];
  attentionSessions?: { id: number }[];
}): number[] {
  const ids = new Set<number>();
  for (const s of agent.sessions ?? []) ids.add(s.id);
  for (const s of agent.attentionSessions ?? []) ids.add(s.id);
  return [...ids];
}

/** agent 名下「常规列表」的会话 id：只有 ListChatAgents 给的前 5 条，不含 attention 池。 */
function agentRegularIDs(agent: { sessions?: { id: number }[] }): number[] {
  return (agent.sessions ?? []).map((s) => s.id);
}

export function useIndexGroups(
  axis: IndexAxis,
  tree: app.ProjectTreeNode[],
  /** 机器轴的机器名单（本机 + 配对的 daemon）。别的轴用不到，缺省空。 */
  machines: readonly MachineRosterEntry[] = [],
  /** 搜索词（已 debounce、已归一）。空串 = 没在搜，取数与渲染都与从前一致。 */
  keyword = "",
): IndexGroup[] {
  const agents = useChatAgentsStore((s) => s.agents);
  const pages = useSessionIndexStore((s) => s.pages);
  const metas = useSessionMetaStore((s) => s.metas);
  const loadFirstPage = useSessionIndexStore((s) => s.loadFirstPage);

  const projectOrder = React.useMemo(() => flattenProjectTree(tree), [tree]);
  const projectIDs = React.useMemo(
    () => projectOrder.map((p) => p.id),
    [projectOrder],
  );
  const deviceIDs = React.useMemo(
    () => machines.map((m) => m.deviceId),
    [machines],
  );
  const agentIDs = React.useMemo(() => agents.map((a) => a.id), [agents]);

  // 按需拉取当前轴要用到的 scope。已经有页缓存的不重拉 —— 刷新走
  // reloadSidebarSources，不由渲染驱动。
  const scopeSignature = React.useMemo(
    () =>
      scopesForAxis(axis, projectIDs, deviceIDs, agentIDs, keyword)
        .map(scopeKey)
        .join("|"),
    [axis, projectIDs, deviceIDs, agentIDs, keyword],
  );
  React.useEffect(() => {
    for (const scope of scopesForAxis(
      axis,
      projectIDs,
      deviceIDs,
      agentIDs,
      keyword,
    )) {
      if (pages.has(scopeKey(scope))) continue;
      void loadFirstPage(scope, firstPageSize(scope));
    }
    // pages 刻意不进依赖：它每次加载完都会变，进依赖会让这个 effect 自激。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    scopeSignature,
    axis,
    projectIDs,
    deviceIDs,
    agentIDs,
    keyword,
    loadFirstPage,
  ]);

  return React.useMemo(() => {
    // 先摆这一轴的**组骨架**：有哪些组、按什么顺序、每组已取到哪一页。这一段留在
    // 宿主，因为它就是「每轴一条查询」本身 —— 空项目组照摆、「随手对话」常驻
    // （决策 6）也只有宿主说得出，共享投影按决策 10 只摆有会话的组。
    const buildSlots = (): IndexGroup[] => {
      if (axis === "time") {
        const page = pages.get(scopeKey(recentScope(keyword)));
        return [
          {
            key: "flat",
            kind: "flat",
            refID: 0,
            depth: 0,
            sessionIDs: page?.ids ?? [],
            total: page?.total ?? 0,
          },
        ];
      }

      if (axis === "machine") {
        // 名单顺序说了算（本机最前，其余在线优先）—— 投影不重排组。
        // 一条会话都没有的机器照样摆出来（决策 10）：刚配好的 daemon 得看得见。
        return machines.map((m) => {
          const page = pages.get(scopeKey(machineScope(m.deviceId, keyword)));
          return {
            key: `machine:${m.deviceId}`,
            kind: "machine" as const,
            refID: m.deviceId,
            depth: 0,
            sessionIDs: page?.ids ?? [],
            total: page?.total ?? 0,
          };
        });
      }

      if (axis === "agent") {
        const byID = new Map(agents.map((a) => [a.id, a]));
        return orderAgents(agents, metas).flatMap((id) => {
          const agent = byID.get(id);
          if (!agent) return [];
          // 搜索时这一组的会话来自该 agent 自己那条过滤查询；不搜索时仍是
          // ListChatAgents 顺带给的前 5 条 + attention 池。总数跟着同一处走 ——
          // 拿未过滤的 totalSessions 去配过滤后的行，组头会写着「会话 44」而下面
          // 只挂着一条搜索结果。
          const page = keyword
            ? pages.get(scopeKey(agentScope(id, keyword)))
            : undefined;
          if (keyword) {
            return [
              {
                key: `agent:${id}`,
                kind: "agent" as const,
                refID: id,
                depth: 0,
                sessionIDs: page?.ids ?? [],
                recentIDs: page?.ids ?? [],
                total: page?.total ?? 0,
              },
            ];
          }
          return [
            {
              key: `agent:${id}`,
              kind: "agent" as const,
              refID: id,
              depth: 0,
              sessionIDs: agentSessionIDs(agent),
              recentIDs: agentRegularIDs(agent),
              total: Number(agent.totalSessions ?? 0),
            },
          ];
        });
      }

      const groups: IndexGroup[] = projectOrder.map(({ id, depth }) => {
        const page = pages.get(scopeKey(projectScope(id, keyword)));
        return {
          key: `project:${id}`,
          kind: "project" as const,
          refID: id,
          depth,
          sessionIDs: page?.ids ?? [],
          total: page?.total ?? 0,
        };
      });
      // 「随手对话」常驻，空态也在（决策 6）：否则一条自由会话都没有的用户，在项目
      // 轴下看不到任何「不属于项目」的入口。它排在全部项目之后。
      const free = pages.get(scopeKey(freeScope(keyword)));
      groups.push({
        key: "free",
        kind: "free",
        refID: 0,
        depth: 0,
        sessionIDs: free?.ids ?? [],
        total: free?.total ?? 0,
      });
      return groups;
    };

    // 组内的分配、补齐与排序过共享投影，桌面端不再自己排一遍。
    return projectIndexGroups(axis, buildSlots(), metas);
  }, [axis, agents, metas, pages, projectOrder, machines, keyword]);
}

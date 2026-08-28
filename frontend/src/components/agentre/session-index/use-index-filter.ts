import * as React from "react";

import { useSessionAttentionList } from "@/stores/attention-store";
import type { SessionMeta } from "@/stores/session-meta-store";

import type { IndexGroup } from "./use-index-groups";

import type { app } from "../../../../wailsjs/go/models";

/** 筛选 chips：单选。null = 全部（决策 8）。 */
type StatusFilter = "running" | "unread" | null;

type UseIndexFilterOptions = {
  groups: IndexGroup[];
  metas: Map<number, SessionMeta>;
  projectByID: Map<number, app.ProjectItem>;
};

type IndexFilter = {
  query: string;
  setQuery: React.Dispatch<React.SetStateAction<string>>;
  statusFilter: StatusFilter;
  setStatusFilter: React.Dispatch<React.SetStateAction<StatusFilter>>;
  /** 小写化 + trim 后的搜索词。空串 = 没在搜。*/
  needle: string;
  unreadCount: number;
  /** 命中集合。`null` = 没有任何筛选，整棵列表原样渲染。*/
  visibleSessionIDs: ReadonlySet<number> | null;
};

// useIndexFilter 收拢左栏的搜索 / 状态筛选:输入态、attention 读数,以及由两者
// 算出的命中集合。
function useIndexFilter({
  groups,
  metas,
  projectByID,
}: UseIndexFilterOptions): IndexFilter {
  const [query, setQuery] = React.useState("");
  const [statusFilter, setStatusFilter] = React.useState<StatusFilter>(null);
  const needle = query.trim().toLowerCase();

  const allSessionIDs = React.useMemo(
    () => [...new Set(groups.flatMap((g) => g.sessionIDs))],
    [groups],
  );
  const attentionItems = useSessionAttentionList(allSessionIDs);
  const reasonBySession = React.useMemo(
    () => new Map(attentionItems.map((i) => [i.sessionId, i.reason])),
    [attentionItems],
  );
  const unreadCount = React.useMemo(
    () => attentionItems.filter((i) => i.reason === "unread").length,
    [attentionItems],
  );

  /**
   * 命中集合。`null` = 没有任何筛选，整棵列表原样渲染。
   *
   * 搜索同时匹配**会话标题 / 项目名 / agent 名**（决策 8）。一条会话被留下的条件是
   * 「它自己命中」**或**「它所属的组命中」—— 搜 agent 名时该 agent 名下的会话应该
   * 全留（你在找那个 agent），搜某句标题时才收窄到那几条。
   */
  const visibleSessionIDs = React.useMemo<ReadonlySet<number> | null>(() => {
    if (!needle && statusFilter === null) return null;
    const hit = new Set<number>();
    for (const sid of allSessionIDs) {
      const meta = metas.get(sid);
      if (!meta) continue;
      if (statusFilter !== null && reasonBySession.get(sid) !== statusFilter) {
        continue;
      }
      if (!needle) {
        hit.add(sid);
        continue;
      }
      const projectName =
        meta.projectId && meta.projectId > 0
          ? (projectByID.get(meta.projectId)?.name ?? "")
          : "";
      const haystack = [meta.title ?? "", meta.agentName ?? "", projectName]
        .join("\n")
        .toLowerCase();
      if (haystack.includes(needle)) hit.add(sid);
    }
    return hit;
  }, [
    needle,
    statusFilter,
    allSessionIDs,
    metas,
    reasonBySession,
    projectByID,
  ]);

  return {
    query,
    setQuery,
    statusFilter,
    setStatusFilter,
    needle,
    unreadCount,
    visibleSessionIDs,
  };
}

export { useIndexFilter };
export type { IndexFilter, StatusFilter };

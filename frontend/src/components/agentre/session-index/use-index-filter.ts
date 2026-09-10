import * as React from "react";

import { useSessionAttentionList } from "@/stores/attention-store";

/** 筛选 chips：单选。null = 全部（决策 8）。 */
type StatusFilter = "running" | "unread" | null;

/**
 * 搜索态。它与状态 chip 拆成两个 hook，因为改完之后两者落在取数的**两侧**：
 * 关键词进查询（组装组之前就要有），状态 chip 过已经摆好的行（组装之后才算得出）。
 */
type IndexSearch = {
  query: string;
  setQuery: React.Dispatch<React.SetStateAction<string>>;
  /** 小写化 + trim 后的搜索词，随输入即时变。空串 = 没在搜。*/
  needle: string;
  /**
   * 喂给取数的搜索词：`needle` 去抖之后的值。搜索走的是服务端查询，每敲一个字符就
   * 发一轮 RPC 没有必要。
   */
  keyword: string;
  /** 搜索是否生效。拖拽排序、空态文案按它判断（决策 9）。 */
  searching: boolean;
};

type UseIndexFilterOptions = {
  /** 当前轴摆出来的全部会话 id（用于算未读读数与状态命中）。 */
  sessionIDs: readonly number[];
};

type IndexFilter = {
  statusFilter: StatusFilter;
  setStatusFilter: React.Dispatch<React.SetStateAction<StatusFilter>>;
  unreadCount: number;
  /**
   * 状态 chip 的命中集合。`null` = 没按状态筛。
   *
   * **搜索不在这里**：关键词是取数 scope 的一部分（见 use-index-groups），列表本身
   * 就已经是过滤后的，再在前端过一遍只会把「首屏窗口之外的命中」重新挡掉 —— 那正是
   * 此前搜不到东西的原因。留在前端的只有 running / 未读，它们是 attention store 的
   * 实时派生态，落不进 SQL。
   */
  visibleSessionIDs: ReadonlySet<number> | null;
};

/** 搜索去抖窗口。够短到打完一个词就出结果，够长到不会逐字符打 RPC。 */
const SEARCH_DEBOUNCE_MS = 200;

function useDebounced<T>(value: T, delayMs: number): T {
  const [settled, setSettled] = React.useState(value);
  React.useEffect(() => {
    if (settled === value) return;
    const timer = setTimeout(() => setSettled(value), delayMs);
    return () => clearTimeout(timer);
  }, [value, delayMs, settled]);
  return settled;
}

// useIndexSearch 只管搜索框本身:输入态、归一化、去抖。它必须在组装组之前调用 ——
// 关键词是取数 scope 的一部分。
function useIndexSearch(): IndexSearch {
  const [query, setQuery] = React.useState("");
  const needle = query.trim().toLowerCase();
  const keyword = useDebounced(needle, SEARCH_DEBOUNCE_MS);
  return { query, setQuery, needle, keyword, searching: needle.length > 0 };
}

// useIndexFilter 管状态 chip:attention 读数与命中集合。它过的是已经摆好的行,
// 所以在组装组之后调用。
function useIndexFilter({ sessionIDs }: UseIndexFilterOptions): IndexFilter {
  const [statusFilter, setStatusFilter] = React.useState<StatusFilter>(null);

  const attentionItems = useSessionAttentionList(sessionIDs);
  const reasonBySession = React.useMemo(
    () => new Map(attentionItems.map((i) => [i.sessionId, i.reason])),
    [attentionItems],
  );
  const unreadCount = React.useMemo(
    () => attentionItems.filter((i) => i.reason === "unread").length,
    [attentionItems],
  );

  const visibleSessionIDs = React.useMemo<ReadonlySet<number> | null>(() => {
    if (statusFilter === null) return null;
    const hit = new Set<number>();
    for (const sid of sessionIDs) {
      if (reasonBySession.get(sid) === statusFilter) hit.add(sid);
    }
    return hit;
  }, [statusFilter, sessionIDs, reasonBySession]);

  return { statusFilter, setStatusFilter, unreadCount, visibleSessionIDs };
}

export { useIndexFilter, useIndexSearch };
export type { IndexFilter, IndexSearch, StatusFilter };

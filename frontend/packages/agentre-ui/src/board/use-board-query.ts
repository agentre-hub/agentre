import * as React from "react";

import {
  buildFilterChips,
  activeConditionCount,
  dropChip,
  type FilterChip,
} from "./query-conditions";
import type { BoardQuery } from "./query-types";

/**
 * 输入停手到发起查询之间的等待。按键就查会把每个字母都变成一次往返；查询期间旧结果
 * 留在原地，只有输入框右端那枚转圈在动。
 */
export const BOARD_SEARCH_DEBOUNCE_MS = 200;

export interface UseBoardQueryResult {
  /** 输入框里此刻的字（可能还没发出去）。 */
  keywordDraft: string;
  setKeywordDraft: (keyword: string) => void;
  /** 防抖在途 = 那枚转圈。 */
  pending: boolean;
  chips: FilterChip[];
  conditionCount: number;
  /** 改一条条件；其余原样带走。 */
  patch: (partial: Partial<BoardQuery>) => void;
  removeChip: (chip: FilterChip) => void;
}

/**
 * 查询面**唯一**的一支 hook：关键词防抖、chip 摊开与条件计数都在这里，筛选栏的几个
 * 呈现件只收算好的结果。
 */
export function useBoardQuery(
  query: BoardQuery,
  onQueryChange: (next: BoardQuery) => void,
): UseBoardQueryResult {
  const [draft, setDraft] = React.useState(query.keyword);
  const [committed, setCommitted] = React.useState(query.keyword);

  // 定时器里要用的是**最新**的 query 与回调，但它们不能进依赖数组：宿主每次渲染
  // 新建一个 query 对象就会重置计时，200ms 永远等不到头。
  const latest = React.useRef({ query, onQueryChange });
  React.useEffect(() => {
    latest.current = { query, onQueryChange };
  });

  // 关键词被外面改了（摘掉 chip、宿主复位）→ 输入框跟上。
  React.useEffect(() => {
    if (query.keyword === committed) return;
    setCommitted(query.keyword);
    setDraft(query.keyword);
  }, [committed, query.keyword]);

  React.useEffect(() => {
    if (draft === committed) return;
    const timer = setTimeout(() => {
      setCommitted(draft);
      latest.current.onQueryChange({ ...latest.current.query, keyword: draft });
    }, BOARD_SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [committed, draft]);

  const patch = React.useCallback(
    (partial: Partial<BoardQuery>) => {
      onQueryChange({ ...query, ...partial });
    },
    [onQueryChange, query],
  );

  const removeChip = React.useCallback(
    (chip: FilterChip) => {
      onQueryChange(dropChip(query, chip));
    },
    [onQueryChange, query],
  );

  return {
    keywordDraft: draft,
    setKeywordDraft: setDraft,
    pending: draft !== committed,
    chips: buildFilterChips(query),
    conditionCount: activeConditionCount(query),
    patch,
    removeChip,
  };
}

import * as React from "react";

import {
  BOARD_STAGES,
  type BoardCardView,
  type BoardStage,
  type BoardViewModel,
} from "./types";

/**
 * 未筛选时已完成列只渲染最近这么多张，其余折叠成一行。已完成是「已经不用管了」
 * 的那一堆，它把还要干的三列挤出视野没有意义。
 */
export const DONE_VISIBLE_LIMIT = 5;

/**
 * 折叠已完成列时留下的是**最近完成的那几张**，而不是列内位置排在前面的那几张。
 *
 * 列内次序是人拖出来的 `position`，与「什么时候完成的」无关：照位置切前 N 张，
 * 刚拖进已完成的那张会立刻掉进折叠行里 —— 干完一件事它就从眼前消失，正是这条
 * 折叠规则要避免的。挑出来之后仍按列内次序渲染，拖出来的顺序不被打乱。
 */
function mostRecent(cards: BoardCardView[], limit: number): BoardCardView[] {
  const keep = new Set(
    [...cards]
      .sort((a, b) => (b.updatedAt ?? 0) - (a.updatedAt ?? 0))
      .slice(0, limit)
      .map((card) => card.id),
  );
  return cards.filter((card) => keep.has(card.id));
}

export interface BoardColumnState {
  stage: BoardStage;
  /** 此刻真要画出来的卡片（已完成列折叠时是最近 `DONE_VISIBLE_LIMIT` 张）。 */
  cards: BoardCardView[];
  /** 被折叠掉的张数；0 = 不画那一行。 */
  hiddenCount: number;
  /** 全部数量，不随筛选缩水。 */
  total: number;
  /** 命中数（筛选态才显示）。 */
  matched: number;
}

export interface UseBoardColumnsResult {
  columns: BoardColumnState[];
  /** 四列加起来一张卡都没有 —— 走空态，而不是画四个「拖到这里」。 */
  isEmpty: boolean;
  expandDone: () => void;
}

/**
 * 看板的**唯一**一支 hook：把视图模型摊成四列固定的渲染状态，并持有「已完成列
 * 展开了没」这一点纯 UI 状态。
 *
 * 筛选生效时已完成列**强制展开**：命中被藏在折叠行里等于搜了却搜不到。展开是
 * 就地的，不跳转、不换页。
 */
export function useBoardColumns(
  viewModel: BoardViewModel,
): UseBoardColumnsResult {
  const [doneExpanded, setDoneExpanded] = React.useState(false);
  const { columns: source, filtering } = viewModel;

  const columns = React.useMemo<BoardColumnState[]>(
    () =>
      BOARD_STAGES.map((stage) => {
        const view = source[stage];
        const cards = view?.cards ?? [];
        const collapsed =
          stage === "done" && !filtering && !doneExpanded
            ? cards.length - DONE_VISIBLE_LIMIT
            : 0;

        return {
          stage,
          cards: collapsed > 0 ? mostRecent(cards, DONE_VISIBLE_LIMIT) : cards,
          hiddenCount: Math.max(collapsed, 0),
          total: view?.total ?? cards.length,
          matched: view?.matched ?? cards.length,
        };
      }),
    [doneExpanded, filtering, source],
  );

  const expandDone = React.useCallback(() => setDoneExpanded(true), []);

  return {
    columns,
    isEmpty: columns.every((column) => column.cards.length === 0),
    expandDone,
  };
}

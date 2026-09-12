import * as React from "react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";

import { BoardColumn } from "./board-column";
import { BoardEmptyState } from "./board-empty-state";
import { BoardSkeleton } from "./board-skeleton";
import { useBoardColumns } from "./use-board-columns";
import type { BoardDragBindings, BoardPorts, BoardViewModel } from "./types";

export interface IssueBoardProps {
  viewModel: BoardViewModel;
  ports: BoardPorts;
  /** 拖拽手势由宿主接（包里没有 dnd-kit），这里只收算好的视觉态。 */
  drag?: BoardDragBindings;
  /** 相对时间的「现在」；不给就取 `Date.now()`。 */
  nowMs?: number;
  className?: string;
}

/**
 * 看板本体：四列固定、每列自己纵向滚、列头常驻，整块板横向可滚。
 *
 * 宿主中立 —— 数据从 `viewModel` 进、动作从 `ports` 出，桌面端（Wails）与
 * agentre-server（HTTP）画的是同一块板。
 */
export function IssueBoard({
  viewModel,
  ports,
  drag,
  nowMs,
  className,
}: IssueBoardProps) {
  const { t } = useUiTranslation();
  // 宿主若喂一个走动的 nowMs（相对时间要跳秒），这里跟着它走；不给才退回挂载那一
  // 刻。渲染期直接调 Date.now() 是不纯的——同一次渲染两次调用给出两个答案。
  const [mountedNow] = React.useState(Date.now);
  const resolvedNow = nowMs ?? mountedNow;
  const { columns, isEmpty, expandDone } = useBoardColumns(viewModel);

  return (
    <section
      aria-label={t("board.aria")}
      data-testid="issue-board"
      className={cn(
        "flex min-h-0 flex-1 items-stretch gap-3 overflow-x-auto bg-sidebar px-5 py-3.5",
        className,
      )}
    >
      {viewModel.loading ? (
        <BoardSkeleton />
      ) : isEmpty ? (
        <BoardEmptyState
          kind={viewModel.filtering ? "noMatches" : "noTasks"}
          onCreateTask={ports.onCreateTask}
          onClearFilters={ports.onClearFilters}
        />
      ) : (
        columns.map((column) => (
          <BoardColumn
            key={column.stage}
            state={column}
            filtering={viewModel.filtering}
            keyword={viewModel.keyword}
            ports={ports}
            drag={drag?.column?.(column.stage)}
            cardDrag={drag?.card}
            onExpand={expandDone}
            nowMs={resolvedNow}
          />
        ))
      )}
    </section>
  );
}

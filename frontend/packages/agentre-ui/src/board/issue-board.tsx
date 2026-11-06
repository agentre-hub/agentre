import * as React from "react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";

import { BoardColumn } from "./board-column";
import { BoardEmptyState } from "./board-empty-state";
import { BoardSkeleton } from "./board-skeleton";
import { useBoardColumns } from "./use-board-columns";
import { BOARD_STAGE_META } from "./stages";
import type {
  BoardDragBindings,
  BoardPorts,
  BoardStage,
  BoardViewModel,
} from "./types";

export interface IssueBoardProps {
  viewModel: BoardViewModel;
  ports: BoardPorts;
  /** 拖拽手势由宿主接（包里没有 dnd-kit），这里只收算好的视觉态。 */
  drag?: BoardDragBindings;
  /** 相对时间的「现在」；不给就取 `Date.now()`。 */
  nowMs?: number;
  className?: string;
  /**
   * `stage`：手机档 —— 顶部四个阶段标签（带条数），一次只画选中的那一列，卡片
   * 满宽，不接拖拽（移动走卡片菜单）。不传（`columns`）时与桌面一致。
   */
  layout?: "columns" | "stage";
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
  layout = "columns",
}: IssueBoardProps) {
  const { t } = useUiTranslation();
  const [activeStage, setActiveStage] = React.useState<BoardStage>("todo");
  // 宿主若喂一个走动的 nowMs（相对时间要跳秒），这里跟着它走；不给才退回挂载那一
  // 刻。渲染期直接调 Date.now() 是不纯的——同一次渲染两次调用给出两个答案。
  const [mountedNow] = React.useState(Date.now);
  const resolvedNow = nowMs ?? mountedNow;
  const { columns, isEmpty, expandDone } = useBoardColumns(viewModel);

  const stageLayout = layout === "stage";
  const shown = stageLayout
    ? columns.filter((column) => column.stage === activeStage)
    : columns;

  return (
    <section
      aria-label={t("board.aria")}
      data-testid="issue-board"
      className={cn(
        "flex min-h-0 flex-1 items-stretch gap-3 bg-sidebar",
        stageLayout ? "flex-col px-3 py-2" : "overflow-x-auto px-5 py-3.5",
        className,
      )}
    >
      {stageLayout && !viewModel.loading && !isEmpty ? (
        <div
          role="tablist"
          aria-label={t("board.aria")}
          data-testid="board-stage-tabs"
          className="flex shrink-0 gap-2"
        >
          {columns.map((column) => {
            const selected = column.stage === activeStage;
            return (
              <button
                key={column.stage}
                type="button"
                role="tab"
                aria-selected={selected}
                data-testid={`board-stage-tab-${column.stage}`}
                onClick={() => setActiveStage(column.stage)}
                className={cn(
                  "inline-flex min-h-11 flex-1 cursor-pointer items-center justify-center gap-1 rounded-full px-2 text-aux font-semibold transition-colors",
                  "focus-visible:focus-outline",
                  selected
                    ? "bg-foreground text-background"
                    : "bg-field-fill text-muted-foreground",
                )}
              >
                {t(BOARD_STAGE_META[column.stage].labelKey)}
                <span className="font-mono text-2xs">{column.total}</span>
              </button>
            );
          })}
        </div>
      ) : null}
      {viewModel.loading ? (
        <BoardSkeleton />
      ) : isEmpty ? (
        <BoardEmptyState
          kind={viewModel.filtering ? "noMatches" : "noTasks"}
          scope={viewModel.emptyScope}
          onCreateTask={ports.onCreateTask}
          onClearFilters={ports.onClearFilters}
        />
      ) : (
        shown.map((column) => (
          <BoardColumn
            key={column.stage}
            state={column}
            filtering={viewModel.filtering}
            keyword={viewModel.keyword}
            ports={ports}
            layout={layout}
            drag={stageLayout ? undefined : drag?.column?.(column.stage)}
            cardDrag={stageLayout ? undefined : drag?.card}
            onExpand={expandDone}
            nowMs={resolvedNow}
          />
        ))
      )}
    </section>
  );
}

import { Plus } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";

import { BoardCard } from "./board-card";
import { BOARD_STAGE_META } from "./stages";
import type { BoardColumnState } from "./use-board-columns";
import type {
  BoardCardDragBinding,
  BoardColumnDragBinding,
  BoardPorts,
} from "./types";

export interface BoardColumnProps {
  state: BoardColumnState;
  /** 筛选生效中：列头计数变「命中 / 全部」。 */
  filtering: boolean;
  /** 此刻生效的关键词；卡片据此高亮命中片段。 */
  keyword?: string;
  ports: BoardPorts;
  drag?: BoardColumnDragBinding;
  cardDrag?: (cardId: number) => BoardCardDragBinding | undefined;
  onExpand?: () => void;
  nowMs?: number;
}

/**
 * 一列。
 *
 * 列头**不在**滚动容器里 —— 此前是整块看板一起滚，滚到下面就不知道自己在哪一
 * 列。落点高亮画在整列上而不是卡片边框上：只换卡片边框色，放到哪一列全靠猜。
 */
export function BoardColumn({
  state,
  filtering,
  keyword,
  ports,
  drag,
  cardDrag,
  onExpand,
  nowMs,
}: BoardColumnProps) {
  const { t } = useUiTranslation();
  const meta = BOARD_STAGE_META[state.stage];
  const Icon = meta.icon;
  const label = t(meta.labelKey);

  return (
    <section
      // 落点挂在**整列**上而不是里面那个滚动容器：列头那一条带在容器外面，挂在
      // 容器上时拖到列头既不高亮也接不住，而「目标列整列高亮」说的就是整列。
      ref={drag?.setNodeRef}
      data-slot="board-column"
      data-stage={state.stage}
      data-testid={`board-column-${state.stage}`}
      data-drop-state={drag?.dropState}
      aria-label={label}
      className={cn(
        "flex h-full max-h-full w-[300px] min-w-0 shrink-0 flex-col rounded-lg transition-colors",
        drag?.dropState === "over" &&
          "bg-primary-soft ring-1 ring-primary/30 ring-inset",
      )}
    >
      <header
        data-testid={`board-column-header-${state.stage}`}
        className="flex shrink-0 items-center gap-2 px-2 py-2"
      >
        <Icon
          className={cn("size-3.5 shrink-0", meta.accent)}
          aria-hidden="true"
        />
        <h2 className="text-aux font-semibold">{label}</h2>
        <span
          data-testid={`board-column-count-${state.stage}`}
          className="font-mono text-2xs font-semibold text-muted-foreground"
        >
          {filtering
            ? t("board.hitCount", {
                matched: state.matched,
                total: state.total,
              })
            : state.total}
        </span>
        {ports.onCreateTask ? (
          // 从这一列建出来的任务落在这一列：阶段预置成它，不必建完再拖一次。
          <button
            type="button"
            data-testid={`board-column-add-${state.stage}`}
            aria-label={t("board.addTask", { stage: label })}
            onClick={() => ports.onCreateTask?.(state.stage)}
            className={cn(
              "ml-auto inline-flex size-5 cursor-pointer items-center justify-center rounded-md text-muted-foreground transition-colors",
              "hover:bg-secondary hover:text-foreground",
              "focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40",
            )}
          >
            <Plus className="size-3.5" aria-hidden="true" />
          </button>
        ) : null}
      </header>
      <div
        data-testid={`board-column-scroll-${state.stage}`}
        className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto px-2 pb-2"
      >
        {state.cards.map((card) => (
          <BoardCard
            key={card.id}
            card={card}
            ports={ports}
            keyword={keyword}
            drag={cardDrag?.(card.id)}
            nowMs={nowMs}
          />
        ))}
        {state.hiddenCount > 0 ? (
          <button
            type="button"
            onClick={onExpand}
            className={cn(
              "cursor-pointer rounded-md border border-dashed border-border-strong px-3 py-2 text-2xs text-muted-foreground transition-colors",
              "hover:bg-secondary hover:text-foreground",
              "focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40",
            )}
          >
            {t("board.moreDone", { count: state.hiddenCount })}
          </button>
        ) : null}
        {state.cards.length === 0 ? (
          // 空列是一个**放置目标**，该说出它能干什么，而不是「暂无」。
          <p className="rounded-md border border-dashed border-border-strong px-3 py-6 text-center text-2xs text-muted-foreground">
            {t("board.emptyColumn")}
          </p>
        ) : null}
      </div>
    </section>
  );
}

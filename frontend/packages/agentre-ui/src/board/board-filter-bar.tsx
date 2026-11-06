import * as React from "react";
import { SlidersHorizontal } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "../ui/dialog";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";

import { BoardFilterChips } from "./filter-chips";
import { BoardFilterPanel } from "./filter-panel";
import { BoardSearchBox } from "./board-search";
import { useBoardQuery } from "./use-board-query";
import type {
  BoardQuery,
  BoardQueryPorts,
  LabelUsageView,
  ScopeProjectNode,
} from "./query-types";

export interface BoardFilterBarProps {
  query: BoardQuery;
  labels: LabelUsageView[];
  /** 范围 chip 要说出项目名字。 */
  projects?: ScopeProjectNode[];
  /** 命中数，显示在搜索框右侧紧邻处。 */
  matchedCount?: number;
  /** 宿主的取数在途；与包内的 200ms 防抖共同决定那枚转圈。 */
  searching?: boolean;
  ports: Pick<BoardQueryPorts, "onQueryChange">;
  onManageLabels?: () => void;
  /**
   * 弹层档：筛选面板换成底部弹层，选项是 44px 的 chip，底部按钮写出
   * `matchedCount`（此刻选择下会看到几条）。不传时与桌面一致。
   */
  sheet?: boolean;
  className?: string;
}

/**
 * 搜索 + 筛选 + chip 行。
 *
 * 「筛选」按钮上的数字是**生效的条件个数**（六条里有几条在收窄看板），不是标签
 * 个数；chip 反过来会比条件多 —— 选中的每枚标签各占一枚，才摘得掉其中一枚。
 */
export function BoardFilterBar({
  query,
  labels,
  projects,
  matchedCount,
  searching,
  ports,
  onManageLabels,
  sheet = false,
  className,
}: BoardFilterBarProps) {
  const { t } = useUiTranslation();
  const [open, setOpen] = React.useState(false);
  const state = useBoardQuery(query, ports.onQueryChange);

  const trigger = (
    <button
      type="button"
      data-testid="filter-trigger"
      aria-expanded={open}
      aria-label={t("board.filter.button")}
      className={cn(
        "inline-flex h-8 shrink-0 cursor-pointer items-center gap-1.5 rounded-lg bg-field-fill px-2 text-xs transition-colors",
        "hover:bg-field-fill-hover",
        "focus-visible:focus-outline",
        sheet && "h-11 min-w-11 justify-center px-3",
      )}
    >
      <SlidersHorizontal
        className="size-3.5 text-muted-foreground"
        aria-hidden="true"
      />
      {/* 窄到最小窗口宽度时只剩图标：那点横向空间要留给搜索框与 chip 行。
          名字改由 aria-label 说，`hidden` 会把文本从无障碍树里一并拿掉。 */}
      <span className="max-[860px]:hidden">{t("board.filter.button")}</span>
      {state.conditionCount > 0 ? (
        <span
          data-testid="filter-count"
          className="rounded-full bg-primary-soft px-1.5 font-mono text-2xs text-primary-text"
        >
          {state.conditionCount}
        </span>
      ) : null}
    </button>
  );
  const manageLabels = onManageLabels
    ? () => {
        setOpen(false);
        onManageLabels();
      }
    : undefined;

  return (
    <div className={cn("flex min-w-0 items-center gap-2", className)}>
      <BoardSearchBox
        value={state.keywordDraft}
        onChange={state.setKeywordDraft}
        busy={state.pending || searching}
        matchedCount={matchedCount}
      />
      {sheet ? (
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger asChild>{trigger}</DialogTrigger>
          <DialogContent
            aria-describedby={undefined}
            data-testid="filter-sheet"
            className="top-auto bottom-0 left-0 max-w-none translate-x-0 translate-y-0 rounded-b-none pb-[env(safe-area-inset-bottom)] w-full"
          >
            <DialogHeader>
              <DialogTitle>{t("board.filter.button")}</DialogTitle>
            </DialogHeader>
            <BoardFilterPanel
              sheet
              query={query}
              labels={labels}
              patch={state.patch}
              onManageLabels={manageLabels}
            />
            <div className="p-4">
              <button
                type="button"
                data-testid="filter-sheet-apply"
                onClick={() => setOpen(false)}
                className="inline-flex min-h-11 w-full cursor-pointer items-center justify-center rounded-lg bg-primary px-4 text-sm font-medium text-primary-foreground focus-visible:focus-outline"
              >
                {matchedCount === undefined
                  ? t("board.filter.apply")
                  : t("board.filter.showCount", { count: matchedCount })}
              </button>
            </div>
          </DialogContent>
        </Dialog>
      ) : (
        <Popover open={open} onOpenChange={setOpen}>
          <PopoverTrigger asChild>{trigger}</PopoverTrigger>
          <PopoverContent align="start" className="w-80 p-0">
            <BoardFilterPanel
              query={query}
              labels={labels}
              patch={state.patch}
              onManageLabels={manageLabels}
            />
          </PopoverContent>
        </Popover>
      )}
      <BoardFilterChips
        chips={state.chips}
        labels={labels}
        projects={projects}
        onRemove={state.removeChip}
      />
    </div>
  );
}

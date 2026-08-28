import * as React from "react";
import { SlidersHorizontal } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
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
  className,
}: BoardFilterBarProps) {
  const { t } = useUiTranslation();
  const [open, setOpen] = React.useState(false);
  const state = useBoardQuery(query, ports.onQueryChange);

  return (
    <div className={cn("flex min-w-0 items-center gap-2", className)}>
      <BoardSearchBox
        value={state.keywordDraft}
        onChange={state.setKeywordDraft}
        busy={state.pending || searching}
        matchedCount={matchedCount}
      />
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <button
            type="button"
            data-testid="filter-trigger"
            aria-expanded={open}
            aria-label={t("board.filter.button")}
            className={cn(
              "inline-flex h-8 shrink-0 cursor-pointer items-center gap-1.5 rounded-lg border border-input bg-input-bg px-2 text-xs transition-colors",
              "hover:bg-secondary/60",
              "focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40",
            )}
          >
            <SlidersHorizontal
              className="size-3.5 text-muted-foreground"
              aria-hidden="true"
            />
            {/* 窄到最小窗口宽度时只剩图标：那点横向空间要留给搜索框与 chip 行。
                名字改由 aria-label 说，`hidden` 会把文本从无障碍树里一并拿掉。 */}
            <span className="max-[860px]:hidden">
              {t("board.filter.button")}
            </span>
            {state.conditionCount > 0 ? (
              <span
                data-testid="filter-count"
                className="rounded-full bg-primary-soft px-1.5 font-mono text-2xs text-primary-text"
              >
                {state.conditionCount}
              </span>
            ) : null}
          </button>
        </PopoverTrigger>
        <PopoverContent align="start" className="w-80 p-0">
          <BoardFilterPanel
            query={query}
            labels={labels}
            patch={state.patch}
            onManageLabels={
              onManageLabels
                ? () => {
                    setOpen(false);
                    onManageLabels();
                  }
                : undefined
            }
          />
        </PopoverContent>
      </Popover>
      <BoardFilterChips
        chips={state.chips}
        labels={labels}
        projects={projects}
        onRemove={state.removeChip}
      />
    </div>
  );
}

import { ClipboardList, SearchX } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { Button } from "../ui/button";

/**
 * 空态分两种，**出路不同**：板上一张卡都没有 → 新建任务；筛选筛没了 → 清除筛选。
 * 合成一句「还没有 Issue」的话，筛选筛空的人会去建一条本来就已经存在的任务。
 */
export type BoardEmptyKind = "noTasks" | "noMatches";

export interface BoardEmptyStateProps {
  kind: BoardEmptyKind;
  onCreateTask?: () => void;
  onClearFilters?: () => void;
}

export function BoardEmptyState({
  kind,
  onCreateTask,
  onClearFilters,
}: BoardEmptyStateProps) {
  const { t } = useUiTranslation();
  const noMatches = kind === "noMatches";
  const Icon = noMatches ? SearchX : ClipboardList;
  const action = noMatches ? onClearFilters : onCreateTask;

  return (
    <div
      data-testid="board-empty-state"
      className="flex w-full flex-col items-center justify-center gap-3 py-16 text-center"
    >
      <Icon className="size-6 text-decorative-foreground" aria-hidden="true" />
      <p className="text-xs text-muted-foreground">
        {noMatches ? t("board.empty.noMatches") : t("board.empty.noTasks")}
      </p>
      {action ? (
        <Button
          size="sm"
          variant={noMatches ? "outline" : "default"}
          onClick={() => action()}
        >
          {noMatches ? t("board.empty.clearFilters") : t("board.empty.newTask")}
        </Button>
      ) : null}
    </div>
  );
}

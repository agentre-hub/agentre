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

import { ProjectScopePopover } from "./scope-popover";
import { ProjectScopeTrigger } from "./scope-trigger";
import { useProjectScope } from "./use-project-scope";
import type { ProjectScope, ScopeProjectNode } from "./query-types";

export interface ProjectScopePickerProps {
  scope: ProjectScope;
  /** 扁平前序的项目列表（`ProjectFlat` 同形）。 */
  projects: ScopeProjectNode[];
  /** 未归属的未完成任务数；0 或不给 = 不提供这一项（点进去必定是空板）。 */
  unassignedCount?: number;
  onScopeChange: (scope: ProjectScope) => void;
  /**
   * 弹层档：底部弹层，选完不自动关，底部按钮写出 `matchedCount`（此刻范围下会
   * 看到几条）。不传时与桌面一致。
   */
  sheet?: boolean;
  matchedCount?: number;
  className?: string;
}

/**
 * 「项目范围」选择器：标题栏上的触发器 + 一个弹层。范围含**整棵子树**，所以选中
 * 父项目时触发器会挂一枚 `+N`。
 */
export function ProjectScopePicker({
  scope,
  projects,
  unassignedCount = 0,
  onScopeChange,
  sheet = false,
  matchedCount,
  className,
}: ProjectScopePickerProps) {
  const { t } = useUiTranslation();
  const state = useProjectScope(projects, scope, onScopeChange);

  if (sheet) {
    return (
      <Dialog open={state.open} onOpenChange={state.setOpen}>
        <DialogTrigger asChild>
          <ProjectScopeTrigger
            scope={scope}
            selected={state.selected}
            open={state.open}
            className={cn("min-h-11", className)}
          />
        </DialogTrigger>
        <DialogContent
          aria-describedby={undefined}
          data-testid="scope-sheet"
          className="top-auto bottom-0 left-0 max-w-none translate-x-0 translate-y-0 rounded-b-none pb-[env(safe-area-inset-bottom)] w-full"
        >
          <DialogHeader>
            <DialogTitle>{t("board.scope.label")}</DialogTitle>
          </DialogHeader>
          <ProjectScopePopover
            sheet
            scope={scope}
            rows={state.rows}
            needle={state.needle}
            onNeedleChange={state.setNeedle}
            onSearchKeyDown={state.onSearchKeyDown}
            cursor={state.cursor}
            unassignedCount={unassignedCount}
            onPick={onScopeChange}
          />
          <div className="p-4">
            <button
              type="button"
              data-testid="scope-sheet-apply"
              onClick={() => state.setOpen(false)}
              className="inline-flex min-h-11 w-full cursor-pointer items-center justify-center rounded-lg bg-primary px-4 text-sm font-medium text-primary-foreground focus-visible:focus-outline"
            >
              {matchedCount === undefined
                ? t("board.filter.apply")
                : t("board.filter.showCount", { count: matchedCount })}
            </button>
          </div>
        </DialogContent>
      </Dialog>
    );
  }

  return (
    <Popover open={state.open} onOpenChange={state.setOpen}>
      <PopoverTrigger asChild>
        <ProjectScopeTrigger
          scope={scope}
          selected={state.selected}
          open={state.open}
          className={className}
        />
      </PopoverTrigger>
      <PopoverContent align="start" className="w-72 p-0">
        <ProjectScopePopover
          scope={scope}
          rows={state.rows}
          needle={state.needle}
          onNeedleChange={state.setNeedle}
          onSearchKeyDown={state.onSearchKeyDown}
          cursor={state.cursor}
          unassignedCount={unassignedCount}
          onPick={(next) => {
            onScopeChange(next);
            state.setOpen(false);
          }}
        />
      </PopoverContent>
    </Popover>
  );
}

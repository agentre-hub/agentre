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
  className,
}: ProjectScopePickerProps) {
  const state = useProjectScope(projects, scope, onScopeChange);

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

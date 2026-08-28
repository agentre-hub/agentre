import * as React from "react";
import { Plus, X } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { ProjectGlyph } from "../session-index/project-glyph";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";

import { BOARD_STAGE_META } from "./stages";
import { toneClass } from "./tones";
import { BOARD_STAGES, type BoardStage } from "./types";
import type { LabelUsageView, ScopeProjectNode } from "./query-types";

/**
 * 属性行上那一颗 pill 的形状 —— 执行段那三颗**用的是同一串**（经端口递给宿主），
 * 三颗三个样子摆在一排是此前被否掉的做法。
 */
export const TASK_PILL_CLASS = cn(
  "inline-flex h-7 max-w-[12rem] cursor-pointer items-center gap-1.5 rounded-full border border-border-strong px-2.5 text-2xs text-foreground transition-colors",
  // `w-auto` / `justify-start` / `bg-transparent` 不是这颗 pill 自己需要的默认值，
  // 是**递给宿主之后**要压住的三条：`ModelTargetPicker` 的触发器自带 `w-full`
  // `justify-between` `bg-input-bg`，那三条与本串不冲突、tailwind-merge 全留着，
  // 模型那一颗于是撑满、填色，和左右两颗不是同一个形状。
  "w-auto justify-start bg-transparent",
  "hover:bg-secondary/60",
  "focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40",
  "disabled:cursor-not-allowed disabled:opacity-50",
);

export interface TaskStagePillProps {
  stage: BoardStage;
  onChange: (stage: BoardStage) => void;
  disabled?: boolean;
}

/** 阶段。**编辑态照常可改** —— 与卡片菜单的「移动到」说的是同一件事。 */
export function TaskStagePill({
  stage,
  onChange,
  disabled,
}: TaskStagePillProps) {
  const { t } = useUiTranslation();
  const meta = BOARD_STAGE_META[stage];
  const Icon = meta.icon;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          data-testid="task-pill-stage"
          disabled={disabled}
          className={TASK_PILL_CLASS}
        >
          <Icon
            className={cn("size-3 shrink-0", meta.accent)}
            aria-hidden="true"
          />
          <span className="truncate">{t(meta.labelKey)}</span>
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        {BOARD_STAGES.map((candidate) => {
          const item = BOARD_STAGE_META[candidate];
          const ItemIcon = item.icon;
          return (
            <DropdownMenuItem
              key={candidate}
              data-testid={`task-stage-${candidate}`}
              onSelect={() => onChange(candidate)}
            >
              <ItemIcon
                className={cn("size-3.5", item.accent)}
                aria-hidden="true"
              />
              {t(item.labelKey)}
            </DropdownMenuItem>
          );
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export interface TaskProjectPillProps {
  projectId: number | null;
  projects: ScopeProjectNode[];
  onChange: (projectId: number | null) => void;
  disabled?: boolean;
}

/** 项目。带字形，一键 × 改回「未归属」。 */
export function TaskProjectPill({
  projectId,
  projects,
  onChange,
  disabled,
}: TaskProjectPillProps) {
  const { t } = useUiTranslation();
  const project = projects.find((item) => item.id === projectId);

  return (
    <span className="inline-flex items-center">
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            data-testid="task-pill-project"
            disabled={disabled}
            className={cn(TASK_PILL_CLASS, project && "rounded-r-none pr-1.5")}
          >
            <ProjectGlyph
              project={
                project ? { name: project.name, color: project.color } : null
              }
              glyph={project?.glyph}
              className="size-3 shrink-0 rounded-[3px]"
            />
            <span className="truncate">
              {project?.name ?? t("board.scope.unassigned")}
            </span>
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="max-h-64 overflow-y-auto">
          <DropdownMenuItem
            data-testid="task-project-none"
            onSelect={() => onChange(null)}
          >
            {t("board.scope.unassigned")}
          </DropdownMenuItem>
          {projects.map((item) => (
            <DropdownMenuItem
              key={item.id}
              data-testid={`task-project-${item.id}`}
              onSelect={() => onChange(item.id)}
            >
              <ProjectGlyph
                project={{ name: item.name, color: item.color }}
                glyph={item.glyph}
                className="size-3.5 shrink-0 rounded-[4px]"
              />
              {item.name}
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
      {project ? (
        <button
          type="button"
          data-testid="task-pill-project-clear"
          disabled={disabled}
          aria-label={t("board.form.clearProject")}
          onClick={() => onChange(null)}
          className={cn(
            TASK_PILL_CLASS,
            "rounded-l-none border-l-0 px-1.5 text-muted-foreground hover:text-foreground",
          )}
        >
          <X className="size-3" aria-hidden="true" />
        </button>
      ) : null}
    </span>
  );
}

export interface TaskLabelChipsProps {
  labelIds: number[];
  labels: LabelUsageView[];
  onChange: (labelIds: number[]) => void;
  disabled?: boolean;
}

/**
 * 标签**不套在 pill 里** —— 标签本身就是 chip，直接站在这一行上；卡片上的标签也是
 * 裸 chip，两处一致。末尾一颗虚线 `+` 打开选择器。
 */
export function TaskLabelChips({
  labelIds,
  labels,
  onChange,
  disabled,
}: TaskLabelChipsProps) {
  const { t } = useUiTranslation();
  const [open, setOpen] = React.useState(false);
  const chosen = labels.filter((label) => labelIds.includes(label.id));

  return (
    <span className="inline-flex min-w-0 items-center gap-1">
      {chosen.map((label) => (
        <span
          key={label.id}
          data-testid={`task-label-${label.id}`}
          className={cn(
            "group/label inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-2xs",
            toneClass(label.tone),
          )}
        >
          {label.name}
          <button
            type="button"
            data-testid={`task-label-remove-${label.id}`}
            aria-label={t("board.chip.remove", { name: label.name })}
            disabled={disabled}
            onClick={() => onChange(labelIds.filter((id) => id !== label.id))}
            // 悬停某一颗才露出它自己的移除键，不让一排 × 抢走标签本身的注意力。
            className="cursor-pointer opacity-0 transition-opacity group-hover/label:opacity-100 focus-visible:opacity-100 focus-visible:outline-none"
          >
            <X className="size-3" aria-hidden="true" />
          </button>
        </span>
      ))}
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <button
            type="button"
            data-testid="task-label-add"
            disabled={disabled}
            aria-label={t("board.form.addLabel")}
            className="inline-flex size-5 cursor-pointer items-center justify-center rounded-full border border-dashed border-border-strong text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40 disabled:cursor-not-allowed disabled:opacity-50"
          >
            <Plus className="size-3" aria-hidden="true" />
          </button>
        </PopoverTrigger>
        <PopoverContent align="start" className="w-56 p-1">
          <div className="flex flex-wrap gap-1">
            {labels.map((label) => {
              const active = labelIds.includes(label.id);
              return (
                <button
                  key={label.id}
                  type="button"
                  data-testid={`task-label-option-${label.id}`}
                  aria-pressed={active}
                  onClick={() =>
                    onChange(
                      active
                        ? labelIds.filter((id) => id !== label.id)
                        : [...labelIds, label.id],
                    )
                  }
                  className={cn(
                    "cursor-pointer rounded-full px-2 py-0.5 text-2xs transition-opacity focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40",
                    toneClass(label.tone),
                    active
                      ? "ring-1 ring-primary"
                      : "opacity-70 hover:opacity-100",
                  )}
                >
                  {label.name}
                </button>
              );
            })}
          </div>
        </PopoverContent>
      </Popover>
    </span>
  );
}

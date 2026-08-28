import * as React from "react";
import { ChevronDown, FolderOpen, Inbox } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { ProjectGlyph } from "../session-index/project-glyph";

import type { ScopeRow } from "./scope-tree";
import type { ProjectScope } from "./query-types";

export interface ProjectScopeTriggerProps {
  scope: ProjectScope;
  /** 范围是项目时它那一行；用来取父级路径与子树规模。 */
  selected?: ScopeRow;
  open: boolean;
  className?: string;
}

/**
 * 标题栏上那颗范围触发器。
 *
 * 放不下时**先截断父级路径、保留子项目名**：两个不同父项目下的同名子项目若从尾部
 * 截断会变成同一个样子，那就等于没说自己是哪一个。
 */
export const ProjectScopeTrigger = React.forwardRef<
  HTMLButtonElement,
  ProjectScopeTriggerProps & React.ComponentPropsWithoutRef<"button">
>(function ProjectScopeTrigger(
  { scope, selected, open, className, ...rest },
  ref,
) {
  const { t } = useUiTranslation();
  const node = selected?.node;
  const name =
    scope.kind === "all"
      ? t("board.scope.all")
      : scope.kind === "unassigned"
        ? t("board.scope.unassigned")
        : (node?.name ?? "");
  const path = selected?.path ?? [];
  const descendants = selected?.descendantCount ?? 0;

  return (
    <button
      {...rest}
      ref={ref}
      type="button"
      data-testid="scope-trigger"
      aria-label={t("board.scope.label")}
      aria-expanded={open}
      aria-haspopup="dialog"
      className={cn(
        "inline-flex h-8 min-w-0 max-w-[18rem] cursor-pointer items-center gap-1.5 rounded-lg border border-input bg-input-bg px-2 text-xs transition-colors",
        "hover:bg-secondary/60",
        "focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40",
        className,
      )}
    >
      {scope.kind === "all" ? (
        <FolderOpen
          className="size-3.5 shrink-0 text-muted-foreground"
          aria-hidden="true"
        />
      ) : scope.kind === "unassigned" ? (
        <Inbox
          className="size-3.5 shrink-0 text-muted-foreground"
          aria-hidden="true"
        />
      ) : (
        <ProjectGlyph
          project={node ? { name: node.name, color: node.color } : null}
          glyph={node?.glyph}
          className="size-3.5 shrink-0 rounded-[4px]"
        />
      )}
      {path.length > 0 ? (
        <span
          data-testid="scope-trigger-path"
          className="min-w-0 truncate text-muted-foreground"
        >
          {path.join(" / ")}
        </span>
      ) : null}
      <span
        data-testid="scope-trigger-name"
        className="shrink-0 whitespace-nowrap text-foreground"
      >
        {name}
      </span>
      {descendants > 0 ? (
        <span
          data-testid="scope-trigger-badge"
          title={t("board.scope.includesChildren", { count: descendants })}
          className="shrink-0 rounded-full bg-secondary px-1.5 font-mono text-2xs text-muted-foreground"
        >
          {t("board.scope.plusN", { count: descendants })}
        </span>
      ) : null}
      <ChevronDown
        className="size-3.5 shrink-0 text-muted-foreground"
        aria-hidden="true"
      />
    </button>
  );
});

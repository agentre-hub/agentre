import * as React from "react";
import { Check, FolderOpen, Inbox, Search } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { ProjectGlyph } from "../session-index/project-glyph";

import { splitMatch, type ScopeRow } from "./scope-tree";
import type { ProjectScope } from "./query-types";

/** 行的共同外壳：选中态与游标态是**两个**视觉，不共用一块底色。 */
function rowClassName(selected: boolean, cursor: boolean, dimmed: boolean) {
  return cn(
    "flex w-full cursor-pointer items-center gap-1.5 rounded-md px-2 py-1.5 text-left text-xs transition-colors",
    "focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40",
    !cursor && !selected && "hover:bg-secondary/60",
    // 键盘当前行：accent 底，跟着方向键走。
    cursor && "bg-accent",
    // 已选中项：primary 软底 + ✓，与游标各说各的。
    selected && "bg-primary-soft text-primary-text",
    dimmed && "opacity-60",
  );
}

function CountBadge({ id, value }: { id: string; value: number }) {
  return (
    <span
      data-testid={`scope-count-${id}`}
      className="ml-auto shrink-0 font-mono text-2xs text-muted-foreground"
    >
      {value}
    </span>
  );
}

export interface ProjectScopePopoverProps {
  scope: ProjectScope;
  rows: ScopeRow[];
  needle: string;
  onNeedleChange: (needle: string) => void;
  onSearchKeyDown: (event: React.KeyboardEvent) => void;
  cursor: number;
  /** 未归属的未完成任务数；0 = 这一项**根本不出现**。 */
  unassignedCount: number;
  onPick: (scope: ProjectScope) => void;
}

/**
 * 弹层：置顶两项常驻，其下的项目树自己滚。
 *
 * 计数是「该项目**及其子树**里未完成的任务数」，**不随筛选缩水** —— 数字由宿主按未
 * 筛选口径算好（`ScopeProjectNode.unfinished`），这里原样画，搜索也不动它。
 */
export function ProjectScopePopover({
  scope,
  rows,
  needle,
  onNeedleChange,
  onSearchKeyDown,
  cursor,
  unassignedCount,
  onPick,
}: ProjectScopePopoverProps) {
  const { t } = useUiTranslation();
  const selectedRef = React.useRef<HTMLButtonElement | null>(null);

  // 打开时滚动到已选中项：树很长时，看不见自己此刻在哪一段等于没有上下文。
  React.useEffect(() => {
    selectedRef.current?.scrollIntoView?.({ block: "nearest" });
  }, []);

  return (
    <div className="flex max-h-[24rem] min-h-0 flex-col">
      <div
        data-testid="scope-pinned"
        className="flex shrink-0 flex-col gap-0.5 p-1"
      >
        <button
          type="button"
          data-testid="scope-row-all"
          data-selected={scope.kind === "all" ? "true" : undefined}
          onClick={() => onPick({ kind: "all" })}
          className={rowClassName(scope.kind === "all", false, false)}
        >
          <FolderOpen
            className="size-3.5 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
          <span className="truncate">{t("board.scope.all")}</span>
          {scope.kind === "all" ? (
            <Check
              data-testid="scope-check"
              className="ml-auto size-3.5 shrink-0"
              aria-hidden="true"
            />
          ) : null}
        </button>
        {unassignedCount > 0 ? (
          <button
            type="button"
            data-testid="scope-row-unassigned"
            data-selected={scope.kind === "unassigned" ? "true" : undefined}
            onClick={() => onPick({ kind: "unassigned" })}
            className={rowClassName(scope.kind === "unassigned", false, false)}
          >
            <Inbox
              className="size-3.5 shrink-0 text-muted-foreground"
              aria-hidden="true"
            />
            <span className="truncate">{t("board.scope.unassigned")}</span>
            <CountBadge id="unassigned" value={unassignedCount} />
            {scope.kind === "unassigned" ? (
              <Check
                data-testid="scope-check"
                className="size-3.5 shrink-0"
                aria-hidden="true"
              />
            ) : null}
          </button>
        ) : null}
      </div>
      <div className="shrink-0 border-t border-border-strong px-2 py-1.5">
        <div className="flex items-center gap-1.5">
          <Search
            className="size-3.5 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
          <input
            data-testid="scope-search"
            // 打开就落在搜索框上：上下键要有落点，否则「键盘移动当前行」得先用
            // 鼠标点进来，键盘用户够不着这条路。
            autoFocus
            value={needle}
            onChange={(event) => onNeedleChange(event.target.value)}
            onKeyDown={onSearchKeyDown}
            placeholder={t("board.scope.searchPlaceholder")}
            aria-label={t("board.scope.searchPlaceholder")}
            className="h-6 w-full min-w-0 bg-transparent text-xs outline-none placeholder:text-muted-foreground"
          />
        </div>
      </div>
      <div
        data-testid="scope-tree"
        className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto p-1"
      >
        {rows.length === 0 ? (
          <p className="px-2 py-3 text-center text-2xs text-muted-foreground">
            {t("board.scope.noMatch")}
          </p>
        ) : null}
        {rows.map((row, index) => {
          const selected =
            scope.kind === "project" && scope.projectId === row.node.id;
          const isCursor = index === cursor;

          return (
            <button
              key={row.node.id}
              type="button"
              ref={selected ? selectedRef : undefined}
              data-testid={`scope-row-${row.node.id}`}
              data-depth={row.node.depth}
              data-selected={selected ? "true" : undefined}
              data-cursor={isCursor ? "true" : undefined}
              data-ancestor-only={row.ancestorOnly ? "true" : undefined}
              onClick={() =>
                onPick({ kind: "project", projectId: row.node.id })
              }
              className={rowClassName(selected, isCursor, row.ancestorOnly)}
            >
              {Array.from({ length: row.node.depth }).map((_, level) => (
                // 逐级一条竖引导线：第三层及更深也认得出挂在谁下面。
                <span
                  key={level}
                  data-testid="scope-guide"
                  aria-hidden="true"
                  className="ml-1 h-4 w-px shrink-0 self-stretch bg-border-strong"
                />
              ))}
              <ProjectGlyph
                project={{ name: row.node.name, color: row.node.color }}
                glyph={row.node.glyph}
                className="size-3.5 shrink-0 rounded-[4px]"
              />
              <span className="truncate">
                {splitMatch(row.node.name, needle).map((segment, i) =>
                  segment.match ? (
                    <mark
                      key={i}
                      data-slot="scope-match"
                      className="bg-transparent font-semibold text-primary-text"
                    >
                      {segment.text}
                    </mark>
                  ) : (
                    <React.Fragment key={i}>{segment.text}</React.Fragment>
                  ),
                )}
              </span>
              <CountBadge
                id={String(row.node.id)}
                value={row.node.unfinished ?? 0}
              />
              {selected ? (
                <Check
                  data-testid="scope-check"
                  className="size-3.5 shrink-0"
                  aria-hidden="true"
                />
              ) : null}
            </button>
          );
        })}
      </div>
    </div>
  );
}

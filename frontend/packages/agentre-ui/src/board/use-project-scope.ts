import * as React from "react";

import { buildScopeRows, filterScopeRows, type ScopeRow } from "./scope-tree";
import type { ProjectScope, ScopeProjectNode } from "./query-types";

export interface UseProjectScopeResult {
  open: boolean;
  setOpen: (open: boolean) => void;
  needle: string;
  setNeedle: (needle: string) => void;
  /** 当前要画的树行（已按关键词收窄，命中项的祖先仍在）。 */
  rows: ScopeRow[];
  /** 选中项那一行（触发器要它的路径与 `+N`）；范围不是项目时为 undefined。 */
  selected: ScopeRow | undefined;
  /** 键盘游标落在 `rows` 的第几行；-1 = 还没动过。 */
  cursor: number;
  onSearchKeyDown: (event: React.KeyboardEvent) => void;
}

/**
 * 范围选择器**唯一**的一支 hook：搜索、键盘游标与「哪一行被选中」都在这里，三个
 * 呈现件（触发器 / 弹层 / 行）只收算好的结果。
 *
 * 游标只在**树**里走：置顶的「全部项目」「未归属」是两个固定快捷入口（Tab 可达），
 * 让上下键从它们开始，等于每次找项目都要先按两下无关的方向键。
 */
export function useProjectScope(
  projects: ScopeProjectNode[],
  scope: ProjectScope,
  onScopeChange: (next: ProjectScope) => void,
): UseProjectScopeResult {
  const [open, setOpen] = React.useState(false);
  const [needle, setNeedle] = React.useState("");
  const [cursor, setCursor] = React.useState(-1);

  const allRows = React.useMemo(() => buildScopeRows(projects), [projects]);
  const rows = React.useMemo(
    () => filterScopeRows(allRows, needle),
    [allRows, needle],
  );

  const selected = React.useMemo(
    () =>
      scope.kind === "project"
        ? allRows.find((row) => row.node.id === scope.projectId)
        : undefined,
    [allRows, scope],
  );

  // 每次打开都从「已选中项」重新起步：上次关掉时游标停在哪，与这次要找什么无关。
  React.useEffect(() => {
    if (!open) return;
    setNeedle("");
    setCursor(-1);
  }, [open]);

  const onSearchKeyDown = React.useCallback(
    (event: React.KeyboardEvent) => {
      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault();
        setCursor((current) => {
          const next = event.key === "ArrowDown" ? current + 1 : current - 1;
          if (rows.length === 0) return -1;
          return Math.min(Math.max(next, 0), rows.length - 1);
        });
        return;
      }
      if (event.key === "Enter" && cursor >= 0 && cursor < rows.length) {
        event.preventDefault();
        onScopeChange({ kind: "project", projectId: rows[cursor].node.id });
        setOpen(false);
      }
    },
    [cursor, onScopeChange, rows],
  );

  return {
    open,
    setOpen,
    needle,
    setNeedle,
    rows,
    selected,
    cursor,
    onSearchKeyDown,
  };
}

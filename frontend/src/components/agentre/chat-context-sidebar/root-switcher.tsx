import { ChevronDown, FolderGit2, Pin } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

import type { WorkRoot } from "./views/use-work-roots";

type Props = {
  roots: WorkRoot[];
  /** 当前工作根的绝对路径。 */
  current: string;
  pinned: boolean;
  /** 每个根下「本次会话」的变更数，按根的绝对路径查。 */
  changeCounts: Map<string, number>;
  onSelect: (path: string) => void;
};

/**
 * RootSwitcher 是多工作根会话的根切换器（spec「工作根 · 呈现」）：**只在工作根
 * ≥ 2 时渲染**，位置在一级 tab 条之上——它同时管辖「变更」与「目录」两页，挂进
 * 任一页内部都不对（决策 10）。绝大多数会话只有一个根，因此零新增 chrome。
 *
 * 它显示当前根的名字、它是主仓库还是 worktree、以及该根下的变更数；只有被用户
 * 手动固定时才多一个「已固定」标记——自动跟随是默认状态，不作标注（决策 9）。
 */
export function RootSwitcher({
  roots,
  current,
  pinned,
  changeCounts,
  onSelect,
}: Props) {
  const { t } = useTranslation();
  if (roots.length < 2) return null;
  const active = roots.find((r) => r.path === current) ?? roots[0];

  return (
    <div className="flex h-8 shrink-0 items-center border-b border-border px-2">
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            data-testid="root-switcher"
            title={active.path}
            className={cn(
              "inline-flex h-6 min-w-0 max-w-full cursor-pointer items-center gap-1 rounded-md px-1.5 text-3xs",
              "text-muted-foreground transition-colors hover:bg-muted/50",
              "focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
            )}
          >
            <FolderGit2 className="size-3 shrink-0" aria-hidden="true" />
            <span className="min-w-0 truncate font-medium text-foreground">
              {active.name}
            </span>
            <RootKind root={active} />
            <ChangeCount count={changeCounts.get(active.path) ?? 0} />
            {pinned ? (
              <span className="inline-flex shrink-0 items-center gap-0.5 opacity-80">
                <Pin className="size-2.5" aria-hidden="true" />
                {t("chatContext.workRoots.pinned")}
              </span>
            ) : null}
            <ChevronDown
              className="size-3 shrink-0 opacity-60"
              aria-hidden="true"
            />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="min-w-56">
          {roots.map((root) => (
            <DropdownMenuItem
              key={root.path}
              data-testid="root-option"
              data-root={root.path}
              onSelect={() => onSelect(root.path)}
              className="gap-1.5 text-xs"
            >
              <span className="min-w-0 flex-1 truncate">{root.name}</span>
              <RootKind root={root} />
              <ChangeCount count={changeCounts.get(root.path) ?? 0} />
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

/** 一个根是主仓库还是 worktree；两者都不是（罕见）时不加任何标注。 */
function RootKind({ root }: { root: WorkRoot }) {
  const { t } = useTranslation();
  if (root.isPrimary) {
    return (
      <span className="shrink-0 opacity-70">
        {t("chatContext.workRoots.mainRepo")}
      </span>
    );
  }
  if (root.isWorktree) {
    return (
      <span className="shrink-0 opacity-70">
        {t("chatContext.workRoots.worktree")}
      </span>
    );
  }
  return null;
}

/** 该根下「本次会话」改动的文件数；0 不显示（没有变更就没有可说的）。 */
function ChangeCount({ count }: { count: number }) {
  if (count <= 0) return null;
  return (
    <span className="shrink-0 font-mono text-3xs opacity-80">{count}</span>
  );
}

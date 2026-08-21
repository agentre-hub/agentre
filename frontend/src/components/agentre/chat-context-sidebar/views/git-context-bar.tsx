import { ChevronDown, GitBranch } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

import type { workspace_fs_svc } from "@/../wailsjs/go/models";

import type { GitScope } from "./use-git-changes";

type Props = {
  scope: GitScope;
  onScopeChange: (scope: GitScope) => void;
  /** 当前生效的基线；空串表示推断不出，按钮退化成「选择基线」。 */
  baseRef: string;
  branches: workspace_fs_svc.GitBranchesView | null;
  onSelectBaseline: (ref: string) => void;
};

/**
 * GitContextBar 是 Git 模式的第三行：左边两档切换，右边常驻的基线按钮。
 *
 * 基线只在「本分支」档有意义，所以按钮只在该档出现；它常驻可见是刻意的（决策
 * 9）——不展开就能确认这一屏算的是相对谁的变动。
 */
export function GitContextBar({
  scope,
  onScopeChange,
  baseRef,
  branches,
  onSelectBaseline,
}: Props) {
  const { t } = useTranslation();
  const options = branches?.branches ?? [];
  return (
    <div className="flex h-7 shrink-0 items-center gap-1 border-b border-border px-2">
      <div
        role="tablist"
        aria-label={t("chatContext.git.scope.label")}
        className="inline-flex overflow-hidden rounded-full border border-border"
      >
        <ScopeTab
          label={t("chatContext.git.scope.uncommitted")}
          active={scope === "uncommitted"}
          onClick={() => onScopeChange("uncommitted")}
        />
        <ScopeTab
          label={t("chatContext.git.scope.branch")}
          active={scope === "branch"}
          onClick={() => onScopeChange("branch")}
        />
      </div>
      {scope === "branch" ? (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              // 可见文本只有 ref 名（240px 里放不下更多），完整语义留给无障碍标签。
              aria-label={
                baseRef
                  ? t("chatContext.git.baseline", { ref: baseRef })
                  : t("chatContext.git.pickBaseline")
              }
              title={
                baseRef
                  ? t("chatContext.git.baseline", { ref: baseRef })
                  : t("chatContext.git.pickBaseline")
              }
              className="ml-auto inline-flex max-w-[108px] items-center gap-0.5 rounded-md px-1 py-0.5 font-mono text-3xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
            >
              <GitBranch className="size-3 shrink-0" aria-hidden="true" />
              <span className="truncate">
                {baseRef || t("chatContext.git.pickBaseline")}
              </span>
              <ChevronDown className="size-2.5 shrink-0" aria-hidden="true" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="max-h-64 overflow-auto">
            {options.length === 0 ? (
              <DropdownMenuItem disabled>
                {t("chatContext.git.noBranches")}
              </DropdownMenuItem>
            ) : (
              <DropdownMenuRadioGroup
                value={baseRef}
                onValueChange={onSelectBaseline}
              >
                {options.map((branch) => (
                  <DropdownMenuRadioItem
                    key={branch.name}
                    value={branch.name}
                    className="font-mono text-2xs"
                  >
                    {branch.name}
                  </DropdownMenuRadioItem>
                ))}
              </DropdownMenuRadioGroup>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      ) : null}
    </div>
  );
}

function ScopeTab({
  label,
  active,
  onClick,
}: {
  label: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      onClick={onClick}
      className={cn(
        "px-2 py-0.5 text-3xs whitespace-nowrap transition-colors",
        "focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
        active
          ? "bg-accent font-semibold text-foreground"
          : "text-muted-foreground hover:bg-muted/50",
      )}
    >
      {label}
    </button>
  );
}

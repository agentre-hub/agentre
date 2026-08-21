import { useTranslation } from "react-i18next";

import { cn } from "@/lib/utils";

import type { ChatSidebarTab } from "@/stores/chat-sidebar-store";

type Props = {
  active: ChatSidebarTab;
  onChange: (tab: ChatSidebarTab) => void;
  outlineCount: number;
  filesCount: number;
  /** Git 段的角标：数据已加载时才有值，未加载 / 加载中传 null（不显示角标）。 */
  gitCount: number | null;
};

/**
 * TabBar 是侧栏的顶层三段：大纲 / 文件 / Git（Git 从「文件」页内的一档提升为
 * 顶层 tab，设计决策 1）。
 *
 * 三段等宽、不带图标：保留图标会让英文标签在 240px 下被截断（mockup 板 A3），
 * 去掉图标后中英双语在 240px 与 190px 下都能塞下且不换行（mockup 板 A2）。
 */
export function TabBar({
  active,
  onChange,
  outlineCount,
  filesCount,
  gitCount,
}: Props) {
  const { t } = useTranslation();
  return (
    <div
      className="flex h-9 shrink-0 items-center gap-1 border-b border-border px-3"
      role="tablist"
    >
      <Tab
        label={t("chatContext.outline.label")}
        count={outlineCount}
        active={active === "outline"}
        onClick={() => onChange("outline")}
      />
      <Tab
        label={t("chatContext.files.label")}
        count={filesCount}
        active={active === "files"}
        onClick={() => onChange("files")}
      />
      <Tab
        label={t("chatContext.git.label")}
        count={gitCount}
        active={active === "git"}
        onClick={() => onChange("git")}
      />
    </div>
  );
}

function Tab({
  label,
  count,
  active,
  onClick,
}: {
  label: string;
  count: number | null;
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
        "inline-flex min-w-0 flex-1 items-center justify-center gap-1 rounded-md px-1 py-1.5 text-xs font-medium whitespace-nowrap transition-colors",
        "focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
        active
          ? "bg-primary/10 text-primary"
          : "text-muted-foreground hover:bg-muted/50",
      )}
    >
      <span className="truncate">{label}</span>
      {count != null ? (
        <span className="shrink-0 font-mono text-3xs opacity-80">{count}</span>
      ) : null}
    </button>
  );
}

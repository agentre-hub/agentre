import { useTranslation } from "react-i18next";

import { cn } from "@/lib/utils";

import type { ChatSidebarTab } from "@/stores/chat-sidebar-store";

type Props = {
  active: ChatSidebarTab;
  onChange: (tab: ChatSidebarTab) => void;
  outlineCount: number;
  /**
   * 「变更」段的角标是**当前档**的文件数（spec「一级导航与页面 chrome」）；
   * 「未提交」档的数据尚未加载时传 null（不显示角标）。
   */
  changesCount: number | null;
};

/**
 * TabBar 是侧栏的顶层三段：大纲 / 变更 / 目录（设计决策 1——Git 不再是一级
 * tab，它的两个视角分别并入「变更」页与「目录」页）。
 *
 * 三段等宽、不带图标：保留图标会让英文标签在 240px 下被截断（mockup 板 A3），
 * 去掉图标后中英双语在 240px 与 190px 下都能塞下且不换行（mockup 板 A2）。
 * 「目录」段不显示计数——目录树的首要职责是找文件，一个总数没有意义。
 */
export function TabBar({
  active,
  onChange,
  outlineCount,
  changesCount,
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
        label={t("chatContext.changes.label")}
        count={changesCount}
        active={active === "changes"}
        onClick={() => onChange("changes")}
      />
      <Tab
        label={t("chatContext.directory.label")}
        count={null}
        active={active === "directory"}
        onClick={() => onChange("directory")}
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

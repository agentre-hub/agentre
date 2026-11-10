import type { KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";

import { cn } from "@agentre-hub/agentre-ui";

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
      className="flex h-9 shrink-0 items-center gap-1 px-3"
      role="tablist"
      onKeyDown={(e) => handleTablistKeyDown<ChatSidebarTab>(e, onChange)}
    >
      <Tab
        value="outline"
        label={t("chatContext.outline.label")}
        count={outlineCount}
        active={active === "outline"}
        onClick={() => onChange("outline")}
      />
      <Tab
        value="changes"
        label={t("chatContext.changes.label")}
        count={changesCount}
        active={active === "changes"}
        onClick={() => onChange("changes")}
      />
      <Tab
        value="directory"
        label={t("chatContext.directory.label")}
        count={null}
        active={active === "directory"}
        onClick={() => onChange("directory")}
      />
    </div>
  );
}

/**
 * 侧栏里手写 tablist 的键盘模型，与共享 `SegmentedControl` 相同：←/→ 回绕移动、
 * Home/End 到两端，焦点与选中一起走；配合每个 tab 的 roving tabindex（只有当前
 * 项是 0），Tab 键一次就离开整组。这里不直接换成 `SegmentedControl`——它的基础
 * 类（h-7 / px-3 / text-sm / 悬停配色）与这两处的外观不同，换了视觉就变了。
 *
 * 每个 tab 用 `data-value` 携带自己的取值。
 */
export function handleTablistKeyDown<T extends string>(
  event: KeyboardEvent<HTMLElement>,
  onSelect: (value: T) => void,
) {
  const tabs = Array.from(
    event.currentTarget.querySelectorAll<HTMLElement>(
      '[role="tab"]:not(:disabled)',
    ),
  );
  if (tabs.length === 0) return;
  const current = tabs.indexOf(document.activeElement as HTMLElement);
  let next: number;
  switch (event.key) {
    case "ArrowRight":
      next = current < 0 ? 0 : (current + 1) % tabs.length;
      break;
    case "ArrowLeft":
      next =
        current < 0
          ? tabs.length - 1
          : (current - 1 + tabs.length) % tabs.length;
      break;
    case "Home":
      next = 0;
      break;
    case "End":
      next = tabs.length - 1;
      break;
    default:
      return;
  }
  event.preventDefault();
  const target = tabs[next];
  target.focus();
  const value = target.dataset.value;
  if (value !== undefined) onSelect(value as T);
}

function Tab({
  value,
  label,
  count,
  active,
  onClick,
}: {
  value: string;
  label: string;
  count: number | null;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      role="tab"
      data-value={value}
      aria-selected={active}
      tabIndex={active ? 0 : -1}
      onClick={onClick}
      className={cn(
        "inline-flex min-w-0 flex-1 items-center justify-center gap-1 rounded-md px-1 py-1.5 text-xs font-medium whitespace-nowrap transition-colors",
        "focus-visible:focus-outline",
        // 分段控件：当前页是按钮填充，其余透明、悬停出底；Tab 条下不画线。
        active
          ? "bg-button-fill text-foreground"
          : "text-muted-foreground hover:bg-accent/60 hover:on-accent",
      )}
    >
      <span className="truncate">{label}</span>
      {count != null ? (
        <span className="shrink-0 font-mono text-3xs opacity-80">{count}</span>
      ) : null}
    </button>
  );
}

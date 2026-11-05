// frontend/src/components/agentre/chat-tabs/tab-overflow-menu.tsx
import { ChevronDown } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
  statusConfig,
} from "@agentre-hub/agentre-ui";

import { cn } from "@agentre-hub/agentre-ui";
import { useChatTabsStore } from "@/stores/chat-tabs-store";

import { useTabsView } from "./use-tabs-view";

export function TabOverflowMenu({
  hiddenTabIds,
}: {
  /** 当前挤出 tab 条可视区的 tab id；不传即视为都可见。 */
  hiddenTabIds?: ReadonlySet<string>;
} = {}) {
  const { t } = useTranslation();
  // Overflow menu shows tabs in stable openedAt order (not active-first),
  // so users get a consistent temporal list.
  const rawTabs = useChatTabsStore((s) => s.tabs);
  const viewMap = new Map(useTabsView().map((v) => [v.id, v]));
  const sortedTabs = [...rawTabs]
    .sort((a, b) => a.openedAt - b.openedAt)
    .map((t) => viewMap.get(t.id))
    .filter((v): v is NonNullable<typeof v> => v != null);

  // 看不见、又需要用户的 tab（出错 / 审批 / 提问等，都落在 pillText 上）计数。
  const attentionHidden = hiddenTabIds
    ? sortedTabs.filter((v) => hiddenTabIds.has(v.id) && v.pillText != null)
        .length
    : 0;

  const activeTabId = useChatTabsStore((s) => s.activeTabId);
  const setActive = useChatTabsStore((s) => s.setActive);

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={t("chatTabs.overflow.openMenu")}
          className="relative inline-flex size-7 items-center justify-center rounded-md hover:bg-accent"
        >
          <ChevronDown className="size-3.5" aria-hidden="true" />
          {attentionHidden > 0 ? (
            <span
              data-testid="tab-overflow-attention-count"
              className="absolute -right-0.5 -top-0.5 inline-flex min-w-3.5 items-center justify-center rounded-full bg-status-waiting-bg px-1 text-[9px] font-semibold text-status-waiting-text"
            >
              {attentionHidden}
            </span>
          ) : null}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" sideOffset={4} className="w-96 p-0">
        <div className="px-3 py-2 font-mono text-2xs font-semibold uppercase tracking-wider text-muted-foreground">
          {t("chatTabs.overflow.openTabs", { count: sortedTabs.length })}
        </div>
        <div className="flex flex-col py-1">
          {sortedTabs.map((t, idx) => {
            // 颜色与其余状态记号同源；闲置那一格留透明占位，保住行内对齐。
            const dotCls =
              t.status === "idle" ? null : statusConfig[t.status].dotClassName;
            return (
              <DropdownMenuItem
                key={t.id}
                data-active={t.id === activeTabId}
                current={t.id === activeTabId}
                onSelect={() => setActive(t.id)}
                className="h-7 gap-2 rounded-none px-3 text-xs"
              >
                <span
                  data-testid="overflow-row-status-dot"
                  aria-hidden="true"
                  className={cn(
                    "size-1.5 shrink-0 rounded-full",
                    dotCls ?? "bg-transparent",
                  )}
                />
                <span
                  className="inline-flex size-4 shrink-0 items-center justify-center rounded-sm"
                  style={{ backgroundColor: t.avatar.color }}
                >
                  <span className="text-[9px] font-semibold text-agent-foreground">
                    {t.avatar.letter}
                  </span>
                </span>
                <span className="min-w-0 flex-1 truncate whitespace-nowrap">
                  {t.title}
                </span>
                {t.projectColor ? (
                  <span
                    data-testid="overflow-row-project-chip"
                    aria-hidden="true"
                    className="h-3 w-1 shrink-0 rounded-sm"
                    style={{ backgroundColor: t.projectColor }}
                  />
                ) : null}
                {idx < 9 ? (
                  <kbd className="shrink-0 rounded-sm bg-button-fill px-1 font-mono text-2xs text-muted-foreground">
                    ⌘{idx + 1}
                  </kbd>
                ) : null}
              </DropdownMenuItem>
            );
          })}
        </div>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

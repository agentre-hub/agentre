// frontend/src/components/agentre/chat-tabs/tab-overflow-menu.tsx
import { ChevronDown } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";
import { useChatTabsStore } from "@/stores/chat-tabs-store";

import type { TabStatus } from "./tab";
import { useTabsView } from "./use-tabs-view";

const STATUS_DOT_CLASS: Record<TabStatus, string | null> = {
  idle: null,
  running: "bg-status-running",
  waiting: "bg-status-waiting",
  error: "bg-destructive",
};

export function TabOverflowMenu() {
  const { t } = useTranslation();
  // Overflow menu shows tabs in stable openedAt order (not active-first),
  // so users get a consistent temporal list.
  const rawTabs = useChatTabsStore((s) => s.tabs);
  const viewMap = new Map(useTabsView().map((v) => [v.id, v]));
  const sortedTabs = [...rawTabs]
    .sort((a, b) => a.openedAt - b.openedAt)
    .map((t) => viewMap.get(t.id))
    .filter((v): v is NonNullable<typeof v> => v != null);

  const activeTabId = useChatTabsStore((s) => s.activeTabId);
  const setActive = useChatTabsStore((s) => s.setActive);

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={t("chatTabs.overflow.openMenu")}
          className="inline-flex size-7 items-center justify-center rounded-md hover:bg-accent"
        >
          <ChevronDown className="size-3.5" aria-hidden="true" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" sideOffset={4} className="w-96 p-0">
        <div className="px-3 py-2 font-mono text-2xs font-semibold uppercase tracking-wider text-muted-foreground">
          {t("chatTabs.overflow.openTabs", { count: sortedTabs.length })}
        </div>
        <div className="flex flex-col py-1">
          {sortedTabs.map((t, idx) => {
            const dotCls = STATUS_DOT_CLASS[t.status];
            return (
              <DropdownMenuItem
                key={t.id}
                data-active={t.id === activeTabId}
                onSelect={() => setActive(t.id)}
                className={cn(
                  "h-7 gap-2 rounded-none px-3 text-xs",
                  t.id === activeTabId && "bg-sidebar-active-bg",
                )}
              >
                <span
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
                  <span className="text-[9px] font-semibold text-white">
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
                  <kbd className="shrink-0 rounded-sm border border-border bg-secondary px-1 font-mono text-2xs text-muted-foreground">
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

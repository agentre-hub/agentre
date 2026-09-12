import * as React from "react";
import { useTranslation } from "react-i18next";
import {
  LayoutDashboard,
  MessageCircle,
  Settings,
  Users,
  Webhook,
  type LucideIcon,
} from "lucide-react";

import i18n from "@/i18n";
import { cn } from "@/lib/utils";

import { scoreItem } from "../score";
import type { CommandSource, OnSelectCtx } from "../types";

export type NavigationItem = {
  key: string;
  path: string;
  labelKey: string;
  icon: LucideIcon;
};

// 与 App.tsx 左侧 rail 的导航项一一对应（labelKey + path 同步）。
const NAV_ITEMS: NavigationItem[] = [
  { key: "nav-chat", path: "/chat", labelKey: "nav.chat", icon: MessageCircle },
  {
    key: "nav-issues",
    path: "/issues",
    labelKey: "nav.issues",
    icon: LayoutDashboard,
  },
  { key: "nav-org", path: "/org", labelKey: "nav.org", icon: Users },
  {
    key: "nav-hooks",
    path: "/hooks",
    labelKey: "nav.hooks",
    icon: Webhook,
  },
  {
    key: "nav-settings",
    path: "/settings",
    labelKey: "nav.settings",
    icon: Settings,
  },
];

function useItems(): { items: NavigationItem[]; loading: boolean } {
  return { items: NAV_ITEMS, loading: false };
}

function getScore(query: string, item: NavigationItem): number {
  return scoreItem({
    query,
    title: i18n.t(item.labelKey),
    subtitle: item.path,
  });
}

function renderItem(item: NavigationItem): React.ReactNode {
  return <NavigationRow item={item} />;
}

function NavigationRow({ item }: { item: NavigationItem }) {
  const { t } = useTranslation();
  const Icon = item.icon;
  return (
    <div className="flex min-w-0 items-center gap-3">
      <Icon className="size-4 shrink-0 text-primary-text" aria-hidden="true" />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="text-sm text-foreground">{t(item.labelKey)}</span>
        <span className="truncate font-mono text-2xs text-muted-foreground">
          {item.path}
        </span>
      </div>
      <kbd
        className={cn(
          "rounded-sm border border-border bg-card px-1.5 py-0.5 font-mono text-2xs font-medium text-muted-foreground",
          "opacity-0 group-data-[selected=true]/cmditem:opacity-100",
        )}
        aria-hidden="true"
      >
        ↵
      </kbd>
    </div>
  );
}

function onSelect(item: NavigationItem, ctx: OnSelectCtx): void {
  ctx.close();
  ctx.navigate(item.path);
}

export const navigationSource: CommandSource<NavigationItem> = {
  id: "navigation",
  heading: i18n.t("commandPalette.nav.heading"),
  modes: ["default"],
  useItems,
  getScore,
  renderItem,
  onSelect,
};

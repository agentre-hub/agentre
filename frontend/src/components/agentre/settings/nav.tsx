// 左侧导航:桌面宽度下的一级分组 + 窄屏下拉,以及窄屏判定。

import * as React from "react";
import { useTranslation } from "react-i18next";
import type { LucideIcon } from "lucide-react";

import { Button } from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

import {
  settingsNavSections,
  compactSettingsNavItems,
  type SettingsPageId,
} from "./pages";

export function canUseMatchMedia() {
  return (
    typeof window !== "undefined" && typeof window.matchMedia === "function"
  );
}

export function useMediaQuery(query: string) {
  return React.useSyncExternalStore(
    React.useCallback(
      (onStoreChange) => {
        if (!canUseMatchMedia()) {
          return () => {};
        }

        const mediaQuery = window.matchMedia(query);

        mediaQuery.addEventListener("change", onStoreChange);

        return () => {
          mediaQuery.removeEventListener("change", onStoreChange);
        };
      },
      [query],
    ),
    () => (canUseMatchMedia() ? window.matchMedia(query).matches : false),
    () => false,
  );
}

export type SettingsNavButtonProps = {
  activePage: SettingsPageId;
  backendGap: boolean;
  item: {
    icon: LucideIcon;
    id?: SettingsPageId;
    labelKey: string;
  };
  onPageChange: (page: SettingsPageId) => void;
  providerGap: boolean;
};

export function SettingsNavButton({
  activePage,
  backendGap,
  item,
  onPageChange,
  providerGap,
}: SettingsNavButtonProps) {
  const { t } = useTranslation();
  const Icon = item.icon;
  const active = item.id === activePage;
  const pageId = item.id;
  const label = t(item.labelKey);
  const gapDot =
    item.id === "agent-backend"
      ? backendGap
        ? t("settings.nav.agentBackendDot")
        : undefined
      : item.id === "llm-providers"
        ? providerGap
          ? t("settings.nav.llmProviderDot")
          : undefined
        : undefined;

  return (
    <Button
      key={item.labelKey}
      type="button"
      variant="ghost"
      data-testid={pageId ? `settings-nav-${pageId}` : undefined}
      aria-current={active ? "page" : undefined}
      className={cn(
        "h-[30px] shrink-0 justify-start gap-2 px-2.5 text-sm font-normal whitespace-nowrap text-foreground lg:w-full",
        active &&
          "bg-primary-soft font-medium text-primary-text hover:bg-primary-soft hover:text-primary-text",
      )}
      onClick={pageId ? () => onPageChange(pageId) : undefined}
    >
      <Icon
        data-icon="inline-start"
        className={active ? "text-primary-text" : undefined}
        aria-hidden="true"
      />
      {label}
      {gapDot ? (
        <span
          aria-hidden="true"
          className="ml-auto inline-block size-1.5 shrink-0 rounded-full bg-status-waiting"
          data-gap-dot={item.id}
          title={gapDot}
        />
      ) : null}
    </Button>
  );
}

export type SettingsNavProps = {
  activePage: SettingsPageId;
  backendGap: boolean;
  onPageChange: (page: SettingsPageId) => void;
  providerGap: boolean;
  // R12:未登录时「同步」这个导航项整个不出现(不是灰掉)。
  syncEnabled: boolean;
};

// R12:未登录时本规格引入的一切都不存在——过滤掉「同步」这一项,不是禁用它。

// R12:未登录时本规格引入的一切都不存在——过滤掉「同步」这一项,不是禁用它。
export function visibleNavItems<T extends { id?: SettingsPageId }>(
  items: T[],
  syncEnabled: boolean,
): T[] {
  return syncEnabled ? items : items.filter((item) => item.id !== "sync");
}

export function SettingsNav({
  activePage,
  backendGap,
  onPageChange,
  providerGap,
  syncEnabled,
}: SettingsNavProps) {
  const { t } = useTranslation();
  const showFullNav = useMediaQuery("(min-width: 1024px)");

  return (
    <aside
      aria-label={t("settings.nav.settings")}
      className="flex w-full shrink-0 flex-col gap-2 border-b border-border bg-sidebar px-3 py-3 lg:w-[220px] lg:gap-[18px] lg:border-b-0 lg:border-r lg:py-4"
    >
      <div className="px-1.5 text-sm font-semibold lg:pb-2">
        {t("settings.nav.settings")}
      </div>
      <div className="flex flex-wrap gap-1.5 pb-1 lg:flex-col lg:flex-nowrap lg:gap-[18px] lg:p-0">
        {(showFullNav
          ? settingsNavSections
          : [
              {
                labelKey: "settings.nav.engine",
                items: compactSettingsNavItems,
              },
            ]
        ).map((section) => (
          <div
            key={section.labelKey}
            className="flex min-w-0 flex-wrap gap-1 lg:flex-col lg:flex-nowrap lg:gap-0.5"
          >
            {showFullNav ? (
              <div className="hidden px-2 pb-1.5 pt-1 font-mono text-2xs font-semibold uppercase tracking-[0.12em] text-muted-foreground lg:block">
                {t(section.labelKey)}
              </div>
            ) : null}
            {visibleNavItems(section.items, syncEnabled).map((item) => (
              <SettingsNavButton
                key={item.labelKey}
                activePage={activePage}
                backendGap={backendGap}
                item={item}
                onPageChange={onPageChange}
                providerGap={providerGap}
              />
            ))}
          </div>
        ))}
      </div>
    </aside>
  );
}

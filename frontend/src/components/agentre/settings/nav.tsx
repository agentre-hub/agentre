// 设置对话框的左列导航：外框色、固定宽度、分组标题与图标齐全，不随窗口宽度塌成一行
// （窗口最小 860 宽也放得下）。

import * as React from "react";
import { useTranslation } from "react-i18next";
import type { LucideIcon } from "lucide-react";

import { Button } from "@agentre-hub/agentre-ui";
import { cn } from "@agentre-hub/agentre-ui";

import { settingsNavSections, type SettingsPageId } from "./pages";

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
        "h-[34px] w-full shrink-0 justify-start gap-2 rounded-lg px-2.5 text-sm font-normal whitespace-nowrap text-foreground",
        // 当前项：前景色 8% 的相对填充（与按钮填充同一档），不用品牌色。
        active &&
          "bg-button-fill font-medium hover:bg-button-fill hover:text-foreground",
      )}
      onClick={pageId ? () => onPageChange(pageId) : undefined}
    >
      <Icon data-icon="inline-start" aria-hidden="true" />
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
  /**
   * 列头「设置」用什么元素渲染。对话框里传 DialogTitle，让它同时是对话框的可及名；
   * 不在对话框里渲染（如单测）时是普通 div。
   */
  titleAs?: React.ElementType;
};

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
  titleAs: Title = "div",
}: SettingsNavProps) {
  const { t } = useTranslation();

  return (
    <aside
      aria-label={t("settings.nav.settings")}
      className="flex w-[236px] shrink-0 flex-col gap-[18px] overflow-y-auto bg-chrome px-3 py-[18px]"
    >
      <Title className="px-2 pb-1.5 pt-1 text-lg font-semibold tracking-normal text-foreground">
        {t("settings.nav.settings")}
      </Title>
      {settingsNavSections.map((section) => (
        <div key={section.labelKey} className="flex min-w-0 flex-col gap-0.5">
          <div className="px-2 pb-1.5 pt-1 font-mono text-2xs font-semibold uppercase tracking-[0.12em] text-muted-foreground">
            {t(section.labelKey)}
          </div>
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
    </aside>
  );
}

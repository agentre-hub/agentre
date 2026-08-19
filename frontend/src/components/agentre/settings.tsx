import * as React from "react";
import { useTranslation } from "react-i18next";
import { useLocation, useNavigate } from "react-router-dom";
import {
  AlertCircle,
  Bell,
  Cable,
  Cpu,
  Database,
  Info,
  Keyboard,
  Network,
  RefreshCw,
  Server,
  Sparkles,
  SunMoon,
  Wrench,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import { LANGUAGE_STORAGE_KEY, type SupportedLanguage } from "@/i18n";

import type { AppTheme, AppThemePreference } from "./chrome";
import { useChatAgents } from "@/hooks/use-chat-agents";
import { AgentBackendsPanel } from "./agent-backends";
import { DataBackupPanel } from "./data-backup";
import { RemoteDevicesPanel } from "./remote-devices/remote-devices-panel";
import { LlmProvidersPanel } from "./llm-providers";
import { SettingsProxyPanel } from "./settings-proxy";
import { NotificationsPanel } from "./notifications-panel";
import { KeyboardShortcutsPanel } from "./shortcuts";
import { SyncPanel, useSyncStatus } from "./sync";
import { UnderConstructionPage } from "./under-construction-page";
import { UpdateSection } from "./update-section";

type SettingsNavSection = {
  labelKey: string;
  items: {
    id?: SettingsPageId;
    icon: LucideIcon;
    labelKey: string;
  }[];
};

type SettingsPageId =
  | "agent-backend"
  | "appearance"
  | "remote-devices"
  | "data-backup"
  | "keyboard-shortcuts"
  | "llm-providers"
  | "local-proxy"
  | "mcp-servers"
  | "notifications"
  | "skills-tools"
  | "sync"
  | "version-logs";

const settingsPageIds = new Set<SettingsPageId>([
  "agent-backend",
  "appearance",
  "remote-devices",
  "data-backup",
  "keyboard-shortcuts",
  "llm-providers",
  "local-proxy",
  "mcp-servers",
  "notifications",
  "skills-tools",
  "sync",
  "version-logs",
]);

function isSettingsPageId(value: unknown): value is SettingsPageId {
  return (
    typeof value === "string" && settingsPageIds.has(value as SettingsPageId)
  );
}

const settingsNavSections: SettingsNavSection[] = [
  {
    labelKey: "settings.nav.general",
    items: [
      { icon: SunMoon, id: "appearance", labelKey: "settings.nav.appearance" },
      {
        icon: Bell,
        id: "notifications",
        labelKey: "settings.nav.notifications",
      },
      {
        icon: Keyboard,
        id: "keyboard-shortcuts",
        labelKey: "settings.nav.keyboardShortcuts",
      },
      {
        icon: Database,
        id: "data-backup",
        labelKey: "settings.nav.dataBackup",
      },
    ],
  },
  {
    labelKey: "settings.nav.engine",
    items: [
      {
        icon: Sparkles,
        id: "llm-providers",
        labelKey: "settings.nav.llmProvider",
      },
      { icon: Cpu, id: "agent-backend", labelKey: "settings.nav.agentBackend" },
    ],
  },
  {
    labelKey: "settings.nav.integrations",
    items: [
      { icon: Network, id: "local-proxy", labelKey: "settings.nav.localProxy" },
      { icon: Server, id: "mcp-servers", labelKey: "settings.nav.mcpServers" },
      {
        icon: Wrench,
        id: "skills-tools",
        labelKey: "settings.nav.skillsTools",
      },
      {
        icon: Cable,
        id: "remote-devices",
        labelKey: "settings.nav.remoteDevices",
      },
      { icon: RefreshCw, id: "sync", labelKey: "settings.nav.sync" },
    ],
  },
  {
    labelKey: "settings.nav.about",
    items: [
      { icon: Info, id: "version-logs", labelKey: "settings.nav.versionLogs" },
    ],
  },
];

const underConstructionSettingsPages: Record<
  Exclude<
    SettingsPageId,
    | "agent-backend"
    | "appearance"
    | "remote-devices"
    | "keyboard-shortcuts"
    | "llm-providers"
    | "local-proxy"
    | "version-logs"
    | "data-backup"
    | "notifications"
    | "sync"
  >,
  {
    descriptionKey: string;
    icon: LucideIcon;
    titleKey: string;
  }
> = {
  "mcp-servers": {
    titleKey: "settings.underConstruction.mcpServers.title",
    descriptionKey: "settings.underConstruction.mcpServers.description",
    icon: Server,
  },
  "skills-tools": {
    titleKey: "settings.underConstruction.skillsTools.title",
    descriptionKey: "settings.underConstruction.skillsTools.description",
    icon: Wrench,
  },
};

const compactSettingsNavItems = settingsNavSections
  .flatMap((section) => section.items)
  .filter((item): item is typeof item & { id: SettingsPageId } =>
    Boolean(item.id),
  );

function canUseMatchMedia() {
  return (
    typeof window !== "undefined" && typeof window.matchMedia === "function"
  );
}

function useMediaQuery(query: string) {
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

type SettingsNavButtonProps = {
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

function SettingsNavButton({
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

type SettingsNavProps = {
  activePage: SettingsPageId;
  backendGap: boolean;
  onPageChange: (page: SettingsPageId) => void;
  providerGap: boolean;
  // R12:未登录时「同步」这个导航项整个不出现(不是灰掉)。
  syncEnabled: boolean;
};

// R12:未登录时本规格引入的一切都不存在——过滤掉「同步」这一项,不是禁用它。
function visibleNavItems<T extends { id?: SettingsPageId }>(
  items: T[],
  syncEnabled: boolean,
): T[] {
  return syncEnabled ? items : items.filter((item) => item.id !== "sync");
}

function SettingsNav({
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

type SettingsPageHeaderProps = {
  actions?: React.ReactNode;
  description: string;
  title: string;
};

function SettingsPageHeader({
  actions,
  description,
  title,
}: SettingsPageHeaderProps) {
  // 没有页级操作的设置页不套外层行，DOM 与加 actions 之前保持一致。
  if (!actions) {
    return (
      <div className="flex max-w-3xl flex-col gap-1.5">
        <h1 className="text-2xl font-semibold tracking-normal">{title}</h1>
        <p className="text-sm leading-relaxed text-muted-foreground">
          {description}
        </p>
      </div>
    );
  }

  // 标题块必须可收缩（min-w-0 flex-1），否则 max-w-3xl 的固有宽度加上操作按钮会撑破
  // 容器，操作被挤到下一行并左对齐——描述越长、按钮越多越容易触发。
  return (
    <div
      data-slot="settings-page-header"
      className="flex items-start justify-between gap-3"
    >
      <div className="flex min-w-0 max-w-3xl flex-1 flex-col gap-1.5">
        <h1 className="text-2xl font-semibold tracking-normal">{title}</h1>
        <p className="text-sm leading-relaxed text-muted-foreground">
          {description}
        </p>
      </div>
      <div className="flex shrink-0 items-center gap-2">{actions}</div>
    </div>
  );
}

type AppearanceSettingsProps = {
  effectiveTheme: AppTheme;
  onThemePreferenceChange: (themePreference: AppThemePreference) => void;
  themePreference: AppThemePreference;
};

const themePreferenceOptions = [
  {
    labelKey: "theme.system",
    value: "system",
  },
  {
    labelKey: "theme.light",
    value: "light",
  },
  {
    labelKey: "theme.dark",
    value: "dark",
  },
] satisfies {
  labelKey: string;
  value: AppThemePreference;
}[];

type ThemePreferenceSelectProps = Omit<
  AppearanceSettingsProps,
  "effectiveTheme"
>;

function ThemePreferenceSelect({
  onThemePreferenceChange,
  themePreference,
}: ThemePreferenceSelectProps) {
  const { t } = useTranslation();
  const labelId = React.useId();

  return (
    <div className="flex flex-col gap-2 p-4">
      <div className="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex min-w-0 flex-col">
          <span id={labelId} className="text-sm font-medium">
            {t("settings.appearance.themeMode.label")}
          </span>
        </div>
        <div className="w-full sm:w-[220px]">
          <Select
            value={themePreference}
            onValueChange={(value) =>
              onThemePreferenceChange(value as AppThemePreference)
            }
          >
            <SelectTrigger
              aria-label={t("settings.appearance.themeMode.label")}
              aria-labelledby={labelId}
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {themePreferenceOptions.map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {t(option.labelKey)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>
    </div>
  );
}

function AppearanceSettings({
  effectiveTheme,
  onThemePreferenceChange,
  themePreference,
}: AppearanceSettingsProps) {
  const { i18n, t } = useTranslation();
  const followsSystem = themePreference === "system";
  const language =
    i18n.resolvedLanguage === "zh-CN" || i18n.resolvedLanguage === "en"
      ? i18n.resolvedLanguage
      : "en";

  function handleLanguageChange(next: string) {
    const supportedLanguage = next as SupportedLanguage;
    void i18n.changeLanguage(supportedLanguage);

    try {
      localStorage.setItem(LANGUAGE_STORAGE_KEY, supportedLanguage);
    } catch {
      // Embedded previews may block localStorage.
    }
  }

  return (
    <>
      <SettingsPageHeader
        title={t("settings.appearance.title")}
        description={t("settings.appearance.description")}
      />
      <section className="overflow-hidden rounded-lg border border-border bg-card">
        <div className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
          <div className="flex min-w-0 flex-1 flex-col">
            <h2 className="text-sm font-semibold">
              {t("settings.appearance.colorMode.title")}
            </h2>
          </div>
          <Badge
            variant="secondary"
            className="rounded-sm px-1.5 py-0 font-mono text-2xs font-medium"
          >
            {followsSystem
              ? t("theme.system")
              : effectiveTheme === "dark"
                ? t("theme.dark")
                : t("theme.light")}
          </Badge>
        </div>
        <ThemePreferenceSelect
          onThemePreferenceChange={onThemePreferenceChange}
          themePreference={themePreference}
        />
        <div className="flex flex-col gap-2 border-t border-border p-4">
          <div className="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex min-w-0 flex-col">
              <span className="text-sm font-medium">{t("language.label")}</span>
            </div>
            <div className="w-full sm:w-[220px]">
              <Select value={language} onValueChange={handleLanguageChange}>
                <SelectTrigger aria-label={t("language.label")}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="zh-CN">{t("language.zh-CN")}</SelectItem>
                  <SelectItem value="en">{t("language.en")}</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
        </div>
      </section>
    </>
  );
}

function AgentBackendSettings({
  onOpenLlmProviders,
  onOpenProxySettings,
}: {
  onOpenLlmProviders: () => void;
  onOpenProxySettings: () => void;
}) {
  const { t } = useTranslation();

  // 与 LLM 供应商页同一条规则：页级操作（自动识别 / 新建后端）属于 H1 行，卡片里不再
  // 重复一层页头。按钮开的弹窗和扫描进行态归面板自己管，所以页头交给面板渲染，
  // 面板只把按钮塞进 actions 槽，状态不用上提。
  return (
    <AgentBackendsPanel
      onOpenLlmProviders={onOpenLlmProviders}
      onOpenProxySettings={onOpenProxySettings}
      renderHeader={(actions) => (
        <SettingsPageHeader
          title={t("settings.agentBackend.title")}
          description={t("settings.agentBackend.description")}
          actions={actions}
        />
      )}
    />
  );
}

function LocalProxySettings() {
  const { t } = useTranslation();

  return (
    <>
      <SettingsPageHeader
        title={t("settings.localProxy.title")}
        description={t("settings.localProxy.description")}
      />
      <SettingsProxyPanel />
    </>
  );
}

function LlmProviderSettings({
  onOpenAgentBackends,
  providerGap,
}: {
  onOpenAgentBackends: () => void;
  providerGap: boolean;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();

  // 「新增供应商」是页级操作，位置属于 H1 行，但它开的创建弹窗归面板自己管：
  // 这里把页头(以及必须夹在页头与工作区之间的黄条)交给面板渲染，面板只把按钮
  // 塞进 actions 槽，弹窗状态不用上提。
  return (
    <LlmProvidersPanel
      onOpenAgentBackends={onOpenAgentBackends}
      renderHeader={(actions) => (
        <>
          <SettingsPageHeader
            title={t("settings.llmProvider.title")}
            description={t("settings.llmProvider.description")}
            actions={actions}
          />
          {providerGap ? (
            <Alert className="border-status-waiting/40 bg-status-waiting/10 text-status-waiting">
              <AlertCircle className="size-4" aria-hidden="true" />
              <AlertTitle className="text-xs font-semibold">
                {t("settings.llmProvider.gapBanner.title")}
              </AlertTitle>
              <AlertDescription className="text-2xs leading-relaxed">
                {t("settings.llmProvider.gapBanner.description")}
                <Button
                  type="button"
                  variant="link"
                  className="h-auto px-0 text-2xs"
                  onClick={() => navigate("/org")}
                >
                  {t("settings.llmProvider.gapBanner.goToOrg")}
                </Button>
              </AlertDescription>
            </Alert>
          ) : null}
        </>
      )}
    />
  );
}

function SettingsUnderConstruction({ page }: { page: SettingsPageId }) {
  const { t } = useTranslation();

  if (
    page === "appearance" ||
    page === "agent-backend" ||
    page === "remote-devices" ||
    page === "keyboard-shortcuts" ||
    page === "llm-providers" ||
    page === "local-proxy" ||
    page === "version-logs" ||
    page === "data-backup" ||
    page === "notifications" ||
    page === "sync"
  ) {
    return null;
  }

  const pageConfig = underConstructionSettingsPages[page];

  return (
    <UnderConstructionPage
      className="px-0 py-0"
      description={t(pageConfig.descriptionKey)}
      icon={pageConfig.icon}
      title={t(pageConfig.titleKey)}
    />
  );
}

type SettingsPageProps = {
  effectiveTheme: AppTheme;
  onThemePreferenceChange: (themePreference: AppThemePreference) => void;
  themePreference: AppThemePreference;
};

function SettingsPage({
  effectiveTheme,
  onThemePreferenceChange,
  themePreference,
}: SettingsPageProps) {
  const location = useLocation();
  const settingsPage = (location.state as { settingsPage?: unknown } | null)
    ?.settingsPage;
  // 支持外部深链 (如空聊天态 / 不可对话弹窗 navigate("/settings", { state:
  // { settingsPage } })): 只在首次挂载时读一次 router state 作为初始页。
  const [activePage, setActivePage] = React.useState<SettingsPageId>(() =>
    isSettingsPageId(settingsPage) ? settingsPage : "appearance",
  );
  React.useEffect(() => {
    if (isSettingsPageId(settingsPage)) setActivePage(settingsPage);
  }, [settingsPage]);

  // 设置导航打点 + LLM 供应商页黄条的数据源：一次共享的 ListChatAgents
  // (useChatAgents)。gap 只在「确有缺口」时出现：Agent 后端页缺「有 Agent 没绑
  // 后端」，LLM 供应商页缺「有后端绑了未激活/缺失的供应商」(= blockReason
  // provider-inactive / backend-requires-provider)。
  const { agents } = useChatAgents();
  const backendGap = agents.some(
    (a) => a.blockReason === "no-backend" && !a.hasBackendTarget,
  );
  const providerGap = agents.some(
    (a) =>
      a.blockReason === "provider-inactive" ||
      a.blockReason === "backend-requires-provider",
  );

  // R12:未登录时「同步」这一项整个不存在——不是灰掉、不是点进去提示先登录。
  // `Status()` 未登录时返回 `{Enabled:false}` 而不是抛错,`status` 初始为
  // `null`(加载中)同样按「不存在」处理,避免先出现再消失的闪烁。首次加载
  // 完成之前不做重定向判断——否则一个合法已登录用户深链到 "sync" 会在
  // `syncEnabled` 还没来得及从初始的 false 变 true 之前就被赶回「外观」。
  const { status: syncStatus, loading: syncStatusLoading } = useSyncStatus();
  const syncEnabled = syncStatus?.Enabled === true;
  React.useEffect(() => {
    if (syncStatusLoading) return;
    if (activePage === "sync" && !syncEnabled) setActivePage("appearance");
  }, [activePage, syncEnabled, syncStatusLoading]);

  return (
    <div
      data-slot="settings-page"
      className="flex min-h-0 min-w-0 flex-1 flex-col lg:flex-row"
    >
      <SettingsNav
        activePage={activePage}
        backendGap={backendGap}
        onPageChange={setActivePage}
        providerGap={providerGap}
        syncEnabled={syncEnabled}
      />
      <main className="min-w-0 flex-1 overflow-auto bg-background">
        <div className="flex min-h-full w-full min-w-0 max-w-[1180px] flex-col gap-6 px-4 py-5 sm:px-6 lg:gap-8 lg:px-10 lg:py-8">
          {activePage === "remote-devices" ? (
            <RemoteDevicesPanel
              onOpenAgentBackends={() => setActivePage("agent-backend")}
            />
          ) : activePage === "appearance" ? (
            <AppearanceSettings
              effectiveTheme={effectiveTheme}
              onThemePreferenceChange={onThemePreferenceChange}
              themePreference={themePreference}
            />
          ) : activePage === "agent-backend" ? (
            <AgentBackendSettings
              onOpenLlmProviders={() => setActivePage("llm-providers")}
              onOpenProxySettings={() => setActivePage("local-proxy")}
            />
          ) : activePage === "llm-providers" ? (
            <LlmProviderSettings
              onOpenAgentBackends={() => setActivePage("agent-backend")}
              providerGap={providerGap}
            />
          ) : activePage === "local-proxy" ? (
            <LocalProxySettings />
          ) : activePage === "keyboard-shortcuts" ? (
            <KeyboardShortcutsPanel />
          ) : activePage === "data-backup" ? (
            <DataBackupPanel />
          ) : activePage === "notifications" ? (
            <NotificationsPanel />
          ) : activePage === "sync" ? (
            syncEnabled ? (
              <SyncPanel />
            ) : null
          ) : activePage === "version-logs" ? (
            <UpdateSection />
          ) : (
            <SettingsUnderConstruction page={activePage} />
          )}
        </div>
      </main>
    </div>
  );
}

export { SettingsPage };

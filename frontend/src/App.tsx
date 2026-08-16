import { useEffect, useLayoutEffect, useMemo, useState } from "react";
import {
  LocalCommandsProvider,
  TerminalTransportProvider,
  TranscriptLiveStateProvider,
  TranscriptPortsProvider,
} from "@agentre-ai/agentre-ui";
import { useTranslation } from "react-i18next";
import { Toaster } from "sonner";
import {
  MemoryRouter,
  Navigate,
  Outlet,
  Route,
  Routes,
  useOutletContext,
  useLocation,
  useNavigate,
} from "react-router-dom";
import type { IconifyIcon } from "@iconify/types";
import briefcaseIcon from "@iconify-icons/tabler/briefcase";
import buildingCommunityIcon from "@iconify-icons/tabler/building-community";
import layoutKanbanIcon from "@iconify-icons/tabler/layout-kanban";
import messageCircleIcon from "@iconify-icons/tabler/message-circle";
import settingsIcon from "@iconify-icons/tabler/settings";
import webhookIcon from "@iconify-icons/tabler/webhook";

import {
  AppStatusBar,
  AppTopBar,
  ChatPage,
  ChatStreamsHost,
  ChatTabsShortcuts,
  TurnCompleteNotifier,
  NotificationToastViewport,
  CommandPalette,
  HooksPage,
  IssuesPage,
  OrgChartPage,
  PaletteScopeBridge,
  ProjectsPage,
  QuitConfirmDialog,
  ShortcutsProvider,
  SidebarButton,
  SettingsPage,
  ThemeToggle,
  isPrimaryShortcut,
  type AppTheme,
  type AppThemePreference,
  type DesktopPlatform,
} from "@/components/agentre";
import { TabStrip } from "@/components/agentre/chat-tabs/tab-strip";
import { desktopLocalCommandsAccess } from "@/components/agentre/local-commands-access-desktop";
import { desktopTranscriptLiveState } from "@/components/agentre/transcript-live-state-desktop";
import { desktopTranscriptPorts } from "@/components/agentre/transcript-ports-desktop";
import { desktopTerminalTransport } from "@/components/agentre/terminal/terminal-transport-desktop";
import { ChatPanelHost } from "@/components/agentre/chat-tabs/chat-panel-host";
import { useChatAgents } from "@/hooks/use-chat-agents";
import { deriveAppStatusBarState } from "@/lib/app-status-bar";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useSessionMetaStore } from "@/stores/session-meta-store";
import { useSessionReadStore } from "@/stores/session-read-store";
import { useSessionStatusStore } from "@/stores/session-status-store";
import {
  Environment,
  WindowCenter,
  WindowGetSize,
  WindowIsFullscreen,
  WindowSetSize,
  WindowShow,
} from "../wailsjs/runtime/runtime";
import { Info as FetchAppInfo } from "../wailsjs/go/app/App";

type NavItem = {
  icon: IconifyIcon;
  labelKey: string;
  path?: string;
};

const navItems: NavItem[] = [
  {
    path: "/chat",
    labelKey: "nav.chat",
    icon: messageCircleIcon,
  },
  {
    path: "/projects",
    labelKey: "nav.projects",
    icon: briefcaseIcon,
  },
  {
    path: "/issues",
    labelKey: "nav.issues",
    icon: layoutKanbanIcon,
  },
  {
    path: "/org",
    labelKey: "nav.org",
    icon: buildingCommunityIcon,
  },
  {
    path: "/hooks",
    labelKey: "nav.hooks",
    icon: webhookIcon,
  },
];

const settingsNavItem: NavItem = {
  path: "/settings",
  labelKey: "nav.settings",
  icon: settingsIcon,
};

const pageBreadcrumbKeys: Record<string, string> = {
  "/chat": "nav.chat",
  "/projects": "nav.projects",
  "/hooks": "nav.hooks",
  "/issues": "nav.issues",
  "/org": "nav.org",
  "/settings": "nav.settings",
};

const themeStorageKey = "agentre.theme";
const windowSizeStorageKey = "agentre.windowSize";
const lastPathStorageKey = "agentre.lastPath";
const defaultPath = "/chat";
const windowSizeSaveDelayMs = 250;
const minWindowWidth = 860;
const minWindowHeight = 640;
const maxWindowWidth = 4096;
const maxWindowHeight = 3072;
const selectableTextSelector = "[data-selectable-text='true']";

type StoredWindowSize = {
  height: number;
  width: number;
};

type RuntimeMode = "interactive" | "headless" | "unknown";

type AppOutletContext = {
  effectiveTheme: AppTheme;
  onThemePreferenceChange: (themePreference: AppThemePreference) => void;
  themePreference: AppThemePreference;
};

function normalizePlatform(platform: string): DesktopPlatform {
  if (platform === "darwin" || platform === "windows" || platform === "linux") {
    return platform;
  }

  return "unknown";
}

function detectBrowserPlatform(): DesktopPlatform {
  if (typeof navigator === "undefined") {
    return "unknown";
  }

  const userAgent = navigator.userAgent.toLowerCase();
  if (userAgent.includes("mac")) {
    return "darwin";
  }
  if (userAgent.includes("win")) {
    return "windows";
  }
  if (userAgent.includes("linux")) {
    return "linux";
  }

  return "unknown";
}

function hasWailsRuntime() {
  return (
    typeof window !== "undefined" &&
    typeof (window as Window & { runtime?: unknown }).runtime === "object" &&
    (window as Window & { runtime?: unknown }).runtime !== null
  );
}

function isAppTheme(value: string | null): value is AppTheme {
  return value === "light" || value === "dark";
}

function isAppThemePreference(
  value: string | null,
): value is AppThemePreference {
  return value === "system" || isAppTheme(value);
}

function getBrowserStorage() {
  if (typeof window === "undefined") {
    return null;
  }

  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

function readStoredThemePreference(): AppThemePreference | null {
  const storage = getBrowserStorage();

  if (typeof storage?.getItem !== "function") {
    return null;
  }

  try {
    const value = storage.getItem(themeStorageKey);

    return isAppThemePreference(value) ? value : null;
  } catch {
    return null;
  }
}

function writeStoredThemePreference(themePreference: AppThemePreference) {
  const storage = getBrowserStorage();

  if (typeof storage?.setItem !== "function") {
    return;
  }

  try {
    storage.setItem(themeStorageKey, themePreference);
  } catch {
    // Some embedded previews may block localStorage.
  }
}

function getKnownPaths(): Set<string> {
  const paths = new Set<string>();
  for (const item of navItems) {
    if (item.path) {
      paths.add(item.path);
    }
  }
  if (settingsNavItem.path) {
    paths.add(settingsNavItem.path);
  }
  return paths;
}

function isKnownPath(path: string | null): path is string {
  return typeof path === "string" && getKnownPaths().has(path);
}

function readStoredLastPath(): string | null {
  const storage = getBrowserStorage();

  if (typeof storage?.getItem !== "function") {
    return null;
  }

  try {
    const value = storage.getItem(lastPathStorageKey);

    return isKnownPath(value) ? value : null;
  } catch {
    return null;
  }
}

function writeStoredLastPath(path: string) {
  const storage = getBrowserStorage();

  if (typeof storage?.setItem !== "function" || !isKnownPath(path)) {
    return;
  }

  try {
    storage.setItem(lastPathStorageKey, path);
  } catch {
    // Some embedded previews may block localStorage.
  }
}

function getInitialPath(): string {
  return readStoredLastPath() ?? defaultPath;
}

function clampWindowDimension(value: number, min: number, max: number) {
  return Math.min(Math.max(Math.round(value), min), max);
}

function numberFromStorage(value: unknown) {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function normaliseWindowSize(value: unknown): StoredWindowSize | null {
  if (!value || typeof value !== "object") {
    return null;
  }

  const record = value as Record<string, unknown>;
  const rawWidth = numberFromStorage(record.width ?? record.w);
  const rawHeight = numberFromStorage(record.height ?? record.h);

  if (rawWidth === null || rawHeight === null) {
    return null;
  }

  if (rawWidth < minWindowWidth || rawHeight < minWindowHeight) {
    return null;
  }

  return {
    height: clampWindowDimension(rawHeight, minWindowHeight, maxWindowHeight),
    width: clampWindowDimension(rawWidth, minWindowWidth, maxWindowWidth),
  };
}

function readStoredWindowSize(): StoredWindowSize | null {
  const storage = getBrowserStorage();

  if (typeof storage?.getItem !== "function") {
    return null;
  }

  try {
    const value = storage.getItem(windowSizeStorageKey);

    return value ? normaliseWindowSize(JSON.parse(value)) : null;
  } catch {
    return null;
  }
}

function writeStoredWindowSize(size: StoredWindowSize) {
  const storage = getBrowserStorage();
  const normalised = normaliseWindowSize(size);

  if (!normalised || typeof storage?.setItem !== "function") {
    return;
  }

  try {
    storage.setItem(windowSizeStorageKey, JSON.stringify(normalised));
  } catch {
    // Some embedded previews may block localStorage.
  }
}

function prefersDarkTheme() {
  if (
    typeof window === "undefined" ||
    typeof window.matchMedia !== "function"
  ) {
    return false;
  }

  try {
    return window.matchMedia("(prefers-color-scheme: dark)").matches;
  } catch {
    return false;
  }
}

function getSystemTheme(): AppTheme {
  return prefersDarkTheme() ? "dark" : "light";
}

function getInitialThemePreference(): AppThemePreference {
  return readStoredThemePreference() ?? "system";
}

function resolveThemePreference(
  themePreference: AppThemePreference,
  systemTheme: AppTheme,
): AppTheme {
  return themePreference === "system" ? systemTheme : themePreference;
}

function applyDocumentTheme(theme: AppTheme) {
  if (typeof document === "undefined") {
    return;
  }

  document.documentElement.classList.toggle("dark", theme === "dark");
  document.documentElement.dataset.theme = theme;
  document.documentElement.style.colorScheme = theme;
}

function isNavItemActive(pathname: string, itemPath: string | undefined) {
  if (!itemPath) {
    return false;
  }

  return pathname === itemPath || pathname.startsWith(`${itemPath}/`);
}

function getElementFromEventTarget(target: EventTarget | null) {
  return target instanceof Element ? target : null;
}

function getElementFromNode(node: Node | null) {
  if (!node) {
    return null;
  }

  return node instanceof Element ? node : node.parentElement;
}

function closestSelectableTextElement(element: Element | null) {
  return element?.closest(selectableTextSelector) ?? null;
}

function isEditableSelectAllTarget(target: EventTarget | null) {
  const element = getElementFromEventTarget(target);

  return Boolean(
    element?.closest(
      "input, textarea, select, [contenteditable='true'], [role='combobox']",
    ),
  );
}

function isSelectAllShortcut(event: KeyboardEvent, platform: DesktopPlatform) {
  if (event.defaultPrevented || event.altKey || event.shiftKey) {
    return false;
  }

  if (event.key.toLowerCase() !== "a") {
    return false;
  }

  return isPrimaryShortcut(event, platform);
}

function getSelectedTextContainer() {
  const selection = document.getSelection();

  if (!selection || selection.rangeCount === 0 || selection.isCollapsed) {
    return null;
  }

  return (
    closestSelectableTextElement(getElementFromNode(selection.anchorNode)) ??
    closestSelectableTextElement(getElementFromNode(selection.focusNode))
  );
}

function selectTextContainer(element: Element) {
  const selection = document.getSelection();

  if (!selection) {
    return;
  }

  const range = document.createRange();
  range.selectNodeContents(element);
  selection.removeAllRanges();
  selection.addRange(range);
}

function usePreventGlobalSelectAll(platform: DesktopPlatform) {
  useEffect(() => {
    if (typeof document === "undefined") {
      return;
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (!isSelectAllShortcut(event, platform)) {
        return;
      }

      if (isEditableSelectAllTarget(event.target)) {
        return;
      }

      const targetSelectableText = closestSelectableTextElement(
        getElementFromEventTarget(event.target),
      );
      const selectedTextContainer = getSelectedTextContainer();
      const textContainer = targetSelectableText ?? selectedTextContainer;

      event.preventDefault();

      if (textContainer) {
        selectTextContainer(textContainer);
      }
    };

    document.addEventListener("keydown", handleKeyDown);

    return () => {
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [platform]);
}

function useAutoHideScrollbars() {
  useEffect(() => {
    if (typeof document === "undefined") {
      return;
    }

    // WKWebView 对 ::-webkit-scrollbar 的类/属性选择器切换不重绘，必须改 CSS
    // 自定义属性的值才能触发 thumb 重绘。把 --sb-thumb 设到事件 target 上而非
    // <html>，让变量沿 DOM 级联——只影响那一个滚动元素自己的 scrollbar，
    // 不会让兄弟节点的 scrollbar 一起亮起。
    const hideDelayMs = 900;
    const visibleColor =
      "color-mix(in oklab, var(--muted-foreground) 30%, transparent)";
    const timers = new WeakMap<HTMLElement, number>();

    const resolveTarget = (event: Event): HTMLElement | null => {
      if (event.target instanceof HTMLElement) {
        return event.target;
      }
      if (event.target instanceof Document) {
        return event.target.documentElement;
      }
      return null;
    };

    const markScrolling = (event: Event) => {
      const target = resolveTarget(event);
      if (!target) {
        return;
      }

      target.style.setProperty("--sb-thumb", visibleColor);

      const previous = timers.get(target);
      if (previous !== undefined) {
        window.clearTimeout(previous);
      }

      const timer = window.setTimeout(() => {
        target.style.removeProperty("--sb-thumb");
        timers.delete(target);
      }, hideDelayMs);

      timers.set(target, timer);
    };

    const listenerOptions: AddEventListenerOptions = {
      capture: true,
      passive: true,
    };

    // scroll 是主信号；wheel / touchmove 兜底处理"已经到边界、不再产生
    // scroll 事件"或者虚拟列表吞掉 scroll 的情况。
    document.addEventListener("scroll", markScrolling, listenerOptions);
    document.addEventListener("wheel", markScrolling, listenerOptions);
    document.addEventListener("touchmove", markScrolling, listenerOptions);

    return () => {
      document.removeEventListener("scroll", markScrolling, { capture: true });
      document.removeEventListener("wheel", markScrolling, { capture: true });
      document.removeEventListener("touchmove", markScrolling, {
        capture: true,
      });
    };
  }, []);
}

function usePersistedWindowSize(runtimeMode: RuntimeMode) {
  useLayoutEffect(() => {
    if (runtimeMode !== "interactive" || !hasWailsRuntime()) {
      return;
    }

    const storedWindowSize = readStoredWindowSize();

    if (storedWindowSize) {
      try {
        WindowSetSize(storedWindowSize.width, storedWindowSize.height);
      } catch {
        // Browser previews and test doubles may not expose every Wails API.
      }
    }

    try {
      WindowCenter();
    } catch {
      // Browser previews and test doubles may not expose every Wails API.
    }

    try {
      WindowShow();
    } catch {
      // Browser previews and test doubles may not expose every Wails API.
    }

    let mounted = true;
    let saveTimer: number | undefined;

    const saveCurrentWindowSize = async () => {
      try {
        const isFullscreen = await WindowIsFullscreen();

        if (!mounted || isFullscreen) {
          return;
        }

        const size = await WindowGetSize();

        if (mounted) {
          writeStoredWindowSize({ height: size.h, width: size.w });
        }
      } catch {
        // Wails runtime calls can reject during startup/shutdown.
      }
    };

    const scheduleSave = () => {
      if (saveTimer !== undefined) {
        window.clearTimeout(saveTimer);
      }

      saveTimer = window.setTimeout(() => {
        saveTimer = undefined;
        void saveCurrentWindowSize();
      }, windowSizeSaveDelayMs);
    };

    const flushSave = () => {
      if (saveTimer !== undefined) {
        window.clearTimeout(saveTimer);
        saveTimer = undefined;
      }

      void saveCurrentWindowSize();
    };

    window.addEventListener("resize", scheduleSave);
    window.addEventListener("beforeunload", flushSave);
    window.addEventListener("pagehide", flushSave);

    return () => {
      mounted = false;

      if (saveTimer !== undefined) {
        window.clearTimeout(saveTimer);
      }

      window.removeEventListener("resize", scheduleSave);
      window.removeEventListener("beforeunload", flushSave);
      window.removeEventListener("pagehide", flushSave);
    };
  }, [runtimeMode]);
}

function AppLayout() {
  const { t } = useTranslation();
  const [platform, setPlatform] = useState<DesktopPlatform>(
    detectBrowserPlatform,
  );
  const [systemTheme, setSystemTheme] = useState<AppTheme>(getSystemTheme);
  const [themePreference, setThemePreference] = useState<AppThemePreference>(
    getInitialThemePreference,
  );
  const [appVersion, setAppVersion] = useState<string>("dev");
  const [runtimeMode, setRuntimeMode] = useState<RuntimeMode>("unknown");
  const location = useLocation();
  const navigate = useNavigate();
  const effectiveTheme = resolveThemePreference(themePreference, systemTheme);

  usePreventGlobalSelectAll(platform);
  usePersistedWindowSize(runtimeMode);
  useAutoHideScrollbars();

  useLayoutEffect(() => {
    applyDocumentTheme(effectiveTheme);
    writeStoredThemePreference(themePreference);
  }, [effectiveTheme, themePreference]);

  useEffect(() => {
    writeStoredLastPath(location.pathname);
  }, [location.pathname]);

  useEffect(() => {
    if (
      typeof window === "undefined" ||
      typeof window.matchMedia !== "function"
    ) {
      return;
    }

    let mediaQuery: MediaQueryList;

    try {
      mediaQuery = window.matchMedia("(prefers-color-scheme: dark)");
    } catch {
      return;
    }

    const handleColorSchemeChange = (event?: MediaQueryListEvent) => {
      setSystemTheme((event?.matches ?? mediaQuery.matches) ? "dark" : "light");
    };

    handleColorSchemeChange();

    if (typeof mediaQuery.addEventListener === "function") {
      mediaQuery.addEventListener("change", handleColorSchemeChange);

      return () => {
        mediaQuery.removeEventListener("change", handleColorSchemeChange);
      };
    }

    const legacyMediaQuery = mediaQuery as MediaQueryList & {
      addListener?: (listener: (event: MediaQueryListEvent) => void) => void;
      removeListener?: (listener: (event: MediaQueryListEvent) => void) => void;
    };

    legacyMediaQuery.addListener?.(handleColorSchemeChange);

    return () => {
      legacyMediaQuery.removeListener?.(handleColorSchemeChange);
    };
  }, []);

  useEffect(() => {
    let mounted = true;

    if (hasWailsRuntime()) {
      void Environment()
        .then((environment) => {
          if (mounted) {
            setPlatform(normalizePlatform(environment.platform));
          }
        })
        .catch(() => {
          // Browser previews do not expose Wails runtime APIs.
        });
    }

    void (async () => {
      try {
        const info = await FetchAppInfo();
        if (!mounted) return;
        const mode = info?.runtimeMode;
        setRuntimeMode(
          mode === "interactive" || mode === "headless" ? mode : "unknown",
        );
        const ver = info?.version?.trim();
        if (ver) setAppVersion(ver);
      } catch {
        // 浏览器预览模式下 Wails 绑定不存在；保留 dev 兜底。
      }
    })();

    return () => {
      mounted = false;
    };
  }, []);

  // reconcileMissingSessions: 启动时用 ListChatAgents 拿到真实会话集，
  // 把 localStorage 恢复出来的 tabs 里已不存在的会话清掉。
  const { agents } = useChatAgents();
  const sessionStatuses = useSessionStatusStore((s) => s.statuses);
  const sessionMetas = useSessionMetaStore((s) => s.metas);
  const readOverrides = useSessionReadStore((s) => s.overrides);
  const statusBarState = useMemo(
    () =>
      deriveAppStatusBarState(
        agents,
        sessionStatuses,
        sessionMetas,
        readOverrides,
      ),
    [agents, sessionStatuses, sessionMetas, readOverrides],
  );
  const reconcileMissingSessions = useChatTabsStore(
    (s) => s.reconcileMissingSessions,
  );
  const openSession = useChatTabsStore((s) => s.openSession);
  useEffect(() => {
    if (agents.length === 0) return;
    const existing = new Set<number>();
    for (const a of agents) {
      for (const id of a.sessionIds) existing.add(id);
    }
    reconcileMissingSessions(existing);
  }, [agents, reconcileMissingSessions]);

  const breadcrumbKey = pageBreadcrumbKeys[location.pathname];
  const breadcrumb = breadcrumbKey ? t(breadcrumbKey) : "";
  const hasChat =
    location.pathname === "/chat" || location.pathname === "/projects";

  return (
    // 端口挂在应用根而不是转录子树：markdown-text 被三棵树共用(转录、文件预览
    // 面板、聊天输入的提及回显),它底下的 rich-link / markdown-image 要用宿主
    // 能力(打开路径 / 外部链接 / 读工作区文件),只挂转录上另外两棵会取不到。
    <TranscriptPortsProvider ports={desktopTranscriptPorts}>
      <TranscriptLiveStateProvider value={desktopTranscriptLiveState}>
        {/* 终端传输同样挂在应用根：终端标签页由 ChatPanelHost 渲染，
            而本地命令卡片(转录里)未来也要盯同一条 PTY。 */}
        <TerminalTransportProvider transport={desktopTerminalTransport}>
          {/* 本地命令接缝也挂应用根：卡片在转录里，而同一条命令 attach 到终端
              标签后由 ChatPanelHost 渲染，两棵子树都要读得到。 */}
          <LocalCommandsProvider access={desktopLocalCommandsAccess}>
            <ShortcutsProvider platform={platform}>
              <ChatTabsShortcuts />
              <div className="flex h-full min-h-full flex-col overflow-hidden bg-background text-foreground">
                <AppTopBar
                  appName="Agentre"
                  breadcrumb={breadcrumb}
                  platform={platform}
                />

                <div className="flex min-h-0 min-w-0 flex-1">
                  <aside
                    aria-label={t("app.navigationLabel")}
                    className="flex w-14 shrink-0 flex-col items-center gap-1 border-r border-border bg-rail px-2 py-3"
                  >
                    {navItems.map((item) => (
                      <SidebarButton
                        key={item.labelKey}
                        data-testid={`nav-${item.path?.slice(1) ?? item.labelKey}`}
                        label={t(item.labelKey)}
                        icon={item.icon}
                        active={isNavItemActive(location.pathname, item.path)}
                        onClick={
                          item.path ? () => navigate(item.path!) : undefined
                        }
                      />
                    ))}
                    <ThemeToggle
                      className="mt-auto"
                      effectiveTheme={effectiveTheme}
                      themePreference={themePreference}
                      onThemePreferenceChange={setThemePreference}
                    />
                    <SidebarButton
                      data-testid="nav-settings"
                      label={t(settingsNavItem.labelKey)}
                      icon={settingsNavItem.icon}
                      active={isNavItemActive(
                        location.pathname,
                        settingsNavItem.path,
                      )}
                      onClick={() => navigate(settingsNavItem.path!)}
                    />
                  </aside>

                  <Outlet
                    context={{
                      effectiveTheme,
                      onThemePreferenceChange: setThemePreference,
                      themePreference,
                    }}
                  />

                  <div
                    data-page-has-chat={hasChat}
                    className="flex min-h-0 min-w-0 flex-1 flex-col"
                    style={{ display: hasChat ? "flex" : "none" }}
                  >
                    <TabStrip />
                    <ChatPanelHost />
                  </div>
                </div>

                <AppStatusBar
                  agentCount={statusBarState.agentCount}
                  runningCount={statusBarState.runningCount}
                  approvalCount={statusBarState.approvalIds.length}
                  unreadCount={statusBarState.unreadIds.length}
                  attentionIds={[
                    ...statusBarState.approvalIds,
                    ...statusBarState.unreadIds,
                  ]}
                  status={statusBarState.indicatorStatus}
                  version={appVersion}
                  onAttentionClick={(sessionId) => openSession(sessionId)}
                />
                <PaletteScopeBridge />
                <CommandPalette />
                <Toaster
                  position="bottom-right"
                  richColors
                  theme={effectiveTheme}
                />
              </div>
            </ShortcutsProvider>
          </LocalCommandsProvider>
        </TerminalTransportProvider>
      </TranscriptLiveStateProvider>
    </TranscriptPortsProvider>
  );
}

function SettingsRoute() {
  const { effectiveTheme, onThemePreferenceChange, themePreference } =
    useOutletContext<AppOutletContext>();

  return (
    <SettingsPage
      effectiveTheme={effectiveTheme}
      onThemePreferenceChange={onThemePreferenceChange}
      themePreference={themePreference}
    />
  );
}

function App() {
  return (
    <MemoryRouter initialEntries={[getInitialPath()]}>
      {/* 跨路由长存的流式订阅器:用户切到 /projects 等页面时,/chat 整棵会
          unmount,但这里继续维持 Wails EventsOn,把 chunk/tool 事件累到全局
          store,切回来时 ChatPanel 能从 store 还原完整流式状态。*/}
      <ChatStreamsHost />
      <TurnCompleteNotifier />
      <NotificationToastViewport />
      {/* 退出二次确认:常驻订阅 "app:quit-blocked",活跃会话存在时拦截退出弹框。*/}
      <QuitConfirmDialog />
      <Routes>
        <Route element={<AppLayout />}>
          <Route path="/chat" element={<ChatPage />} />
          <Route path="/projects" element={<ProjectsPage />} />
          <Route path="/issues" element={<IssuesPage />} />
          <Route path="/hooks" element={<HooksPage />} />
          <Route path="/org" element={<OrgChartPage />} />
          <Route path="/settings" element={<SettingsRoute />} />
          <Route path="*" element={<Navigate to="/chat" replace />} />
        </Route>
      </Routes>
    </MemoryRouter>
  );
}

export default App;

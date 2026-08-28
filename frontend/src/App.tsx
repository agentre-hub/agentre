import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useState,
} from "react";
import {
  LocalCommandHistoryProvider,
  LocalCommandsProvider,
  TerminalTransportProvider,
  TranscriptLiveStateProvider,
  TranscriptPortsProvider,
  // 与 base.css 里的滚动条规则是一对：那半边把滑块颜色绑到 --sb-thumb 并默认
  // 透明，这半边在滚动时改值。此前这个 hook 就写在本文件里，agentre-server 那侧
  // 因此没有滚动条样式；现在两端共用同一份。
  useAutoHideScrollbars,
} from "@agentre-hub/agentre-ui";
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
import buildingCommunityIcon from "@iconify-icons/tabler/building-community";
import layoutKanbanIcon from "@iconify-icons/tabler/layout-kanban";
import messageCircleIcon from "@iconify-icons/tabler/message-circle";
import settingsIcon from "@iconify-icons/tabler/settings";
import webhookIcon from "@iconify-icons/tabler/webhook";

import {
  AppStatusBar,
  AppTopBar,
  ChatStreamsHost,
  ChatTabsShortcuts,
  TurnCompleteNotifier,
  NotificationToastViewport,
  CommandPalette,
  HooksPage,
  IssuesPage,
  SessionIndexPage,
  OrgChartPage,
  PaletteScopeBridge,
  QuitConfirmDialog,
  ShortcutsProvider,
  SyncAppliedHost,
  SidebarButton,
  SettingsPage,
  ThemeToggle,
  isPrimaryShortcut,
  type AppTheme,
  type AppThemePreference,
  type DesktopPlatform,
} from "@/components/agentre";
import { ThemeProvider, useTheme } from "@agentre-hub/agentre-ui";
import { TabStrip } from "@/components/agentre/chat-tabs/tab-strip";
import { desktopLocalCommandHistoryAccess } from "@/components/agentre/local-command-history-access-desktop";
import { desktopLocalCommandsAccess } from "@/components/agentre/local-commands-access-desktop";
import { desktopTranscriptLiveState } from "@/components/agentre/transcript-live-state-desktop";
import { useDesktopTranscriptPorts } from "@/components/agentre/transcript-ports-desktop";
import { desktopTerminalTransport } from "@/components/agentre/terminal/terminal-transport-desktop";
import { ChatPanelHost } from "@/components/agentre/chat-tabs/chat-panel-host";
import { useChatAgents } from "@/hooks/use-chat-agents";
import { deriveAppStatusBarState } from "@/lib/app-status-bar";
import { UpdateChecksumDialogHost } from "@/components/agentre/update-section";
import {
  unskippedUpdate,
  useUpdateStore,
  useUpdateWatch,
} from "@/stores/update-store";
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
  "/hooks": "nav.hooks",
  "/issues": "nav.issues",
  "/org": "nav.org",
  "/settings": "nav.settings",
};

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
  const [appVersion, setAppVersion] = useState<string>("dev");
  const [runtimeMode, setRuntimeMode] = useState<RuntimeMode>("unknown");
  const location = useLocation();
  const navigate = useNavigate();
  // 主题三态、存储与系统跟随都在共享包里（浏览器宿主同用一份）；这里只消费。
  const { effectiveTheme, setThemePreference, themePreference } = useTheme();

  usePreventGlobalSelectAll(platform);
  usePersistedWindowSize(runtimeMode);
  useAutoHideScrollbars();

  useEffect(() => {
    writeStoredLastPath(location.pathname);
  }, [location.pathname]);

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

  // 更新检查:订阅后台检查结果 + 窗口重新获得焦点时补一次(受 24h 节流)。
  useUpdateWatch();
  // 齿轮红点只认「还没被跳过的新版本」;状态栏胶囊不受跳过影响,那是两码事。
  const hasPendingUpdate = useUpdateStore((s) => unskippedUpdate(s) !== null);
  // 红点是纯装饰(aria-hidden),它的信息由设置按钮自己的可读名承载。
  const settingsLabel = hasPendingUpdate
    ? t("nav.settingsUpdateAvailable")
    : t(settingsNavItem.labelKey);
  const openUpdateSettings = useCallback(() => {
    navigate("/settings", { state: { settingsPage: "version-logs" } });
  }, [navigate]);

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
  const hasChat = location.pathname === "/chat";

  const ports = useDesktopTranscriptPorts();

  return (
    // 端口挂在应用根而不是转录子树：markdown-text 被三棵树共用(转录、文件预览
    // 面板、聊天输入的提及回显),它底下的 rich-link / markdown-image 要用宿主
    // 能力(打开路径 / 外部链接 / 读工作区文件),只挂转录上另外两棵会取不到。
    <TranscriptPortsProvider ports={ports}>
      <TranscriptLiveStateProvider value={desktopTranscriptLiveState}>
        {/* 终端传输同样挂在应用根：终端标签页由 ChatPanelHost 渲染，
            而本地命令卡片(转录里)未来也要盯同一条 PTY。 */}
        <TerminalTransportProvider transport={desktopTerminalTransport}>
          {/* 本地命令接缝也挂应用根：卡片在转录里，而同一条命令 attach 到终端
              标签后由 ChatPanelHost 渲染，两棵子树都要读得到。 */}
          <LocalCommandsProvider access={desktopLocalCommandsAccess}>
            {/* `!` Shell 历史是可选能力：桌面端挂上它，composer 才渲染历史弹层。 */}
            <LocalCommandHistoryProvider
              access={desktopLocalCommandHistoryAccess}
            >
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
                      {/* 外壳样式归宿主：wails-no-drag 与导航栏配色是桌面独有的，
                          按钮说什么、点一下变成什么在共享包里。 */}
                      <ThemeToggle className="wails-no-drag mt-auto size-10 rounded-lg text-sidebar-icon hover:bg-rail-accent hover:text-sidebar-accent-foreground [&_svg:not([class*='size-'])]:size-[18px]" />
                      <SidebarButton
                        data-testid="nav-settings"
                        label={settingsLabel}
                        icon={settingsNavItem.icon}
                        badge={hasPendingUpdate}
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
                    onOpenUpdateSettings={openUpdateSettings}
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
            </LocalCommandHistoryProvider>
          </LocalCommandsProvider>
        </TerminalTransportProvider>
      </TranscriptLiveStateProvider>
    </TranscriptPortsProvider>
  );
}

/**
 * `/projects` 的重定向。用组件而不是 `<Navigate to="/chat" />`：后者会把 query
 * 丢掉，而 `?focus=<id>`（会话设置页点「项目」进来）正是靠 query 传项目 id 的。
 */
function RedirectToChat() {
  const location = useLocation();
  return (
    <Navigate to={{ pathname: "/chat", search: location.search }} replace />
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
    // 主题挂在最外层：<html> 上那次 class 写入要早于任何页面渲染，晚一帧就是一次白闪。
    <ThemeProvider>
      <MemoryRouter initialEntries={[getInitialPath()]}>
        {/* 跨路由长存的流式订阅器:用户切到 /projects 等页面时,/chat 整棵会
          unmount,但这里继续维持 Wails EventsOn,把 chunk/tool 事件累到全局
          store,切回来时 ChatPanel 能从 store 还原完整流式状态。*/}
        <ChatStreamsHost />
        <TurnCompleteNotifier />
        {/* 多端同步落地什么就刷什么：项目树没有推送通道，此前靠项目页那条 1 秒
          轮询兜着，轮询随单一会话索引一起删掉了。挂在根上，因为左栏的数据源
          与当前路由无关。*/}
        <SyncAppliedHost />
        <NotificationToastViewport />
        {/* 退出二次确认:常驻订阅 "app:quit-blocked",活跃会话存在时拦截退出弹框。*/}
        <QuitConfirmDialog />
        {/* 校验文件拉不到时的「仍要继续」确认:下载可以从设置页,也可以从状态栏的
          更新面板发起,对话只挂一处才两边都在。*/}
        <UpdateChecksumDialogHost />
        <Routes>
          <Route element={<AppLayout />}>
            <Route path="/chat" element={<SessionIndexPage />} />
            {/* 决策 1：「项目」不再是一个导航项，它退化成索引的一个分组维度。
              保留重定向是因为会话设置页的「项目」入口发的是 /projects?focus=<id>，
              query 必须原样带过去 —— 索引那边靠它打开项目设置抽屉。 */}
            <Route path="/projects" element={<RedirectToChat />} />
            <Route path="/issues" element={<IssuesPage />} />
            <Route path="/hooks" element={<HooksPage />} />
            <Route path="/org" element={<OrgChartPage />} />
            <Route path="/settings" element={<SettingsRoute />} />
            <Route path="*" element={<Navigate to="/chat" replace />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </ThemeProvider>
  );
}

export default App;

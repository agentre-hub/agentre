// 应用外壳:一个布局(侧栏 + 顶栏 + outlet)、两条重定向、以及路由表。
//
// 拆出来的四块都在 lib/ 与 hooks/ 下 —— 路由词汇(lib/app-routing.ts)、运行环境
// 判定(lib/platform.ts)、选区助手(lib/text-selection.ts)与两个钩子
// (use-prevent-global-select-all、use-persisted-window-size)。原先它们与这个布局
// 挤在同一个文件里(786 行)。

import { useCallback, useEffect, useMemo, useState } from "react";
// 与 base.css 里的滚动条规则是一对：那半边把滑块颜色绑到 --sb-thumb 并默认
// 透明，这半边在滚动时改值。此前这个 hook 就写在本文件里，agentre-server 那侧
// 因此没有滚动条样式；现在两端共用同一份。
import {
  LocalCommandHistoryProvider,
  LocalCommandsProvider,
  TerminalTransportProvider,
  TranscriptLiveStateProvider,
  TranscriptPortsProvider,
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
import { Environment } from "../wailsjs/runtime/runtime";
import { Info as FetchAppInfo } from "../wailsjs/go/app/App";

import { usePersistedWindowSize } from "./hooks/use-persisted-window-size";
import { usePreventGlobalSelectAll } from "./hooks/use-prevent-global-select-all";
import {
  navItems,
  settingsNavItem,
  pageBreadcrumbKeys,
  writeStoredLastPath,
  getInitialPath,
  isNavItemActive,
} from "./lib/app-routing";
import {
  normalizePlatform,
  detectBrowserPlatform,
  hasWailsRuntime,
  type RuntimeMode,
} from "./lib/platform";

type AppOutletContext = {
  effectiveTheme: AppTheme;
  onThemePreferenceChange: (themePreference: AppThemePreference) => void;
  themePreference: AppThemePreference;
};

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

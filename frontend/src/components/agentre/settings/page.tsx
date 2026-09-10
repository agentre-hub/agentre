// 设置页的外壳:统一 Header + 左侧导航 + 按当前页 id 选中一个分区。

import * as React from "react";
import { useLocation } from "react-router-dom";

import type { AppTheme, AppThemePreference } from "../chrome";
import { useChatAgents } from "@/hooks/use-chat-agents";
import { DataBackupPanel } from "../data-backup";
import { FileSettingsPanel } from "../file-settings-panel";
import { RemoteDevicesPanel } from "../remote-devices/remote-devices-panel";
import { NotificationsPanel } from "../notifications-panel";
import { KeyboardShortcutsPanel } from "../shortcuts";
import { SyncPanel, useSyncStatus } from "../sync";
import { UpdateSection } from "../update-section";

import { AppearanceSettings } from "./appearance";
import { SettingsNav } from "./nav";
import { isSettingsPageId, type SettingsPageId } from "./pages";
import {
  AgentBackendSettings,
  LocalProxySettings,
  CtlSkillSettings,
  LlmProviderSettings,
  SettingsUnderConstruction,
} from "./sections";

export type SettingsPageProps = {
  effectiveTheme: AppTheme;
  onThemePreferenceChange: (themePreference: AppThemePreference) => void;
  themePreference: AppThemePreference;
};

export function SettingsPage({
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
  // `Status()` 未登录时返回 `{enabled:false}` 而不是抛错,`status` 初始为
  // `null`(加载中)同样按「不存在」处理,避免先出现再消失的闪烁。首次加载
  // 完成之前不做重定向判断——否则一个合法已登录用户深链到 "sync" 会在
  // `syncEnabled` 还没来得及从初始的 false 变 true 之前就被赶回「外观」。
  const { status: syncStatus, loading: syncStatusLoading } = useSyncStatus();
  const syncEnabled = syncStatus?.enabled === true;
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
          ) : activePage === "files" ? (
            <FileSettingsPanel />
          ) : activePage === "notifications" ? (
            <NotificationsPanel />
          ) : activePage === "skills-tools" ? (
            <CtlSkillSettings />
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

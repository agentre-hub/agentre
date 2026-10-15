// 设置对话框（规格 2026-10-15「设置对话框」）：浮在当前页面上的模态框，左列导航
// （外框色）+ 右侧内容区（内容页色、单独滚动）+ 右上角关闭。设置不是路由：开关与当前
// 子页都在 stores/settings-dialog-store，底下的页面保持原样不卸载。

import * as React from "react";
import { X } from "lucide-react";
import { Dialog as DialogPrimitive } from "radix-ui";
import { useTranslation } from "react-i18next";

import { cn } from "@agentre-hub/agentre-ui";
import type { AppTheme, AppThemePreference } from "../chrome";
import { useChatAgents } from "@/hooks/use-chat-agents";
import { useSettingsDialogStore } from "@/stores/settings-dialog-store";
import { DataBackupPanel } from "../data-backup";
import { FileSettingsPanel } from "../file-settings-panel";
import { RemoteDevicesPanel } from "../remote-devices/remote-devices-panel";
import { NotificationsPanel } from "../notifications-panel";
import { KeyboardShortcutsPanel } from "../shortcuts";
import { SyncPanel, useSyncStatus } from "../sync";
import { UpdateSection } from "../update-section";

import { AppearanceSettings } from "./appearance";
import { SettingsNav } from "./nav";
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
  /** 列头「设置」用的元素；对话框里是 DialogTitle（见 SettingsNav）。 */
  titleAs?: React.ElementType;
};

/**
 * 设置的内容：左列导航 + 右侧内容区。当前子页由 settings-dialog-store 持有——深链入口
 * 调 `openSettings(page)` 切过来，导航点击写回去，下次不带子页打开时落在这里。
 */
export function SettingsPage({
  effectiveTheme,
  onThemePreferenceChange,
  themePreference,
  titleAs,
}: SettingsPageProps) {
  const activePage = useSettingsDialogStore((s) => s.page);
  const setActivePage = useSettingsDialogStore((s) => s.setPage);

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
  }, [activePage, setActivePage, syncEnabled, syncStatusLoading]);

  return (
    <div data-slot="settings-page" className="flex min-h-0 min-w-0 flex-1">
      <SettingsNav
        activePage={activePage}
        backendGap={backendGap}
        onPageChange={setActivePage}
        providerGap={providerGap}
        syncEnabled={syncEnabled}
        titleAs={titleAs}
      />
      {/* 不是 main 地标：页面上始终只有底下那一页的一个 main。右侧多留的边距
          (pr-16) 给右上角的关闭按钮让位，页标题行的按钮不会与它重叠。 */}
      <div
        data-slot="settings-content"
        className="min-w-0 flex-1 overflow-auto bg-sheet"
      >
        <div className="flex min-h-full w-full min-w-0 flex-col gap-8 py-8 pl-10 pr-16">
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
      </div>
    </div>
  );
}

/**
 * 模态外壳。不用 DialogShell：它是表单弹窗的尺寸阶梯（420/560/760）与头脚结构，
 * 设置要的是近乎铺满窗口的双栏框、回焦到打开前的位置、以及标题栏那条拖动带。
 */
export function SettingsDialog(props: Omit<SettingsPageProps, "titleAs">) {
  const { t } = useTranslation();
  const open = useSettingsDialogStore((s) => s.open);
  const closeSettings = useSettingsDialogStore((s) => s.closeSettings);

  // 没有 Dialog.Trigger（齿轮、⌘,、命令面板都只是改 store），Radix 关窗时找不到
  // 该回焦的元素。打开那一刻记下焦点，关掉时还回去。layout effect 早于 Radix
  // FocusScope 把焦点挪进对话框的那次 effect。
  const returnFocusRef = React.useRef<HTMLElement | null>(null);
  React.useLayoutEffect(() => {
    if (open && document.activeElement instanceof HTMLElement) {
      returnFocusRef.current = document.activeElement;
    }
  }, [open]);

  return (
    <DialogPrimitive.Root
      open={open}
      onOpenChange={(next) => {
        if (!next) closeSettings();
      }}
    >
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay
          data-slot="settings-dialog-overlay"
          className="fixed inset-0 z-50 bg-settings-scrim duration-200 data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 motion-reduce:animate-none"
        />
        {/* 标题栏那一条仍能拖动窗口（macOS 红绿灯是原生的，不受遮罩影响）。按在这里
            不算「点遮罩」，见下面的 onPointerDownOutside。 */}
        <div
          data-slot="settings-drag-region"
          aria-hidden="true"
          className="wails-drag fixed inset-x-0 top-0 z-50 h-11"
          // 模态期间 Radix 给 body 设了 pointer-events:none，这一条得自己要回来。
          style={{ pointerEvents: "auto" }}
        />
        <DialogPrimitive.Content
          data-slot="settings-dialog"
          aria-describedby={undefined}
          onPointerDownOutside={(event) => {
            const target = event.target;
            if (
              target instanceof Element &&
              target.closest('[data-slot="settings-drag-region"]')
            ) {
              event.preventDefault();
            }
          }}
          onCloseAutoFocus={(event) => {
            const target = returnFocusRef.current;
            returnFocusRef.current = null;
            if (target?.isConnected) {
              event.preventDefault();
              target.focus();
            }
          }}
          className={cn(
            "fixed left-1/2 top-1/2 z-50 flex h-[calc(100dvh-4rem)] w-[calc(100vw-7rem)] max-w-[1180px] -translate-x-1/2 -translate-y-1/2 overflow-hidden rounded-2xl bg-sheet text-foreground shadow-overlay outline-none",
            "duration-200 data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 motion-reduce:animate-none",
          )}
        >
          <SettingsPage {...props} titleAs={DialogPrimitive.Title} />
          <DialogPrimitive.Close
            aria-label={t("common.close")}
            className="absolute right-3.5 top-3.5 inline-flex size-8 items-center justify-center rounded-lg text-muted-foreground transition-colors hover:bg-button-fill hover:text-foreground focus-visible:focus-outline"
          >
            <X className="size-4" aria-hidden="true" />
          </DialogPrimitive.Close>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}

import * as React from "react";
import { useTranslation } from "react-i18next";
import {
  ArrowUp,
  ChevronUp,
  CircleAlert,
  Download,
  Loader2,
  Minus,
  RotateCw,
  Search,
  Square,
  TriangleAlert,
  X,
} from "lucide-react";

import {
  Popover,
  PopoverContent,
  PopoverTrigger,
  ThemeToggle,
} from "@agentre-hub/agentre-ui";
import type { AppTheme, AppThemePreference } from "@agentre-hub/agentre-ui";

import logoMarkUrl from "@/assets/images/logo-mark.png";
import { cn } from "@/lib/utils";
import { useCommandPaletteStore } from "@/stores/command-palette-store";
import { useUpdateStore } from "@/stores/update-store";
import type { AgentStatus } from "@/stores/types";
import {
  Quit,
  WindowMinimise,
  WindowToggleMaximise,
} from "../../../wailsjs/runtime/runtime";

import { StatusDot } from "./primitives";
import { UpdatePanel } from "./update-panel";
import { formatChord } from "./shortcuts/format";
import { useOptionalShortcutsContext } from "./shortcuts/shortcuts-provider";

type DesktopPlatform = "darwin" | "windows" | "linux" | "unknown";

function NativeWindowControlsInset({ className }: { className?: string }) {
  return (
    <div
      data-slot="native-window-controls-inset"
      aria-hidden="true"
      className={cn("hidden w-[68px] shrink-0 sm:block", className)}
    />
  );
}

function runWindowAction(action: () => void) {
  if (typeof window === "undefined" || !("runtime" in window)) {
    return;
  }

  action();
}

function isNoDragTarget(
  target: EventTarget | null,
  currentTarget: HTMLElement,
) {
  if (typeof Element === "undefined" || !(target instanceof Element)) {
    return false;
  }

  const noDragElement = target.closest(".wails-no-drag");

  return noDragElement !== null && currentTarget.contains(noDragElement);
}

type WindowsWindowControlsProps = {
  className?: string;
};

function WindowsWindowControls({ className }: WindowsWindowControlsProps) {
  const { t } = useTranslation();

  return (
    <div
      data-slot="windows-window-controls"
      className={cn(
        "wails-no-drag flex h-full shrink-0 items-stretch",
        className,
      )}
    >
      <button
        type="button"
        aria-label={t("app.window.minimize")}
        className="wails-no-drag inline-flex w-11 cursor-pointer items-center justify-center text-muted-foreground outline-none transition-colors hover:bg-rail-accent hover:text-accent-foreground focus-visible:bg-rail-accent focus-visible:text-accent-foreground"
        onClick={() => runWindowAction(WindowMinimise)}
      >
        <Minus className="size-4" aria-hidden="true" />
      </button>
      <button
        type="button"
        aria-label={t("app.window.maximize")}
        className="wails-no-drag inline-flex w-11 cursor-pointer items-center justify-center text-muted-foreground outline-none transition-colors hover:bg-rail-accent hover:text-accent-foreground focus-visible:bg-rail-accent focus-visible:text-accent-foreground"
        onClick={() => runWindowAction(WindowToggleMaximise)}
      >
        <Square className="size-3.5" aria-hidden="true" />
      </button>
      <button
        type="button"
        aria-label={t("app.window.close")}
        className="wails-no-drag inline-flex w-11 cursor-pointer items-center justify-center text-muted-foreground outline-none transition-colors hover:bg-destructive hover:text-destructive-foreground focus-visible:bg-destructive focus-visible:text-destructive-foreground dark:hover:bg-destructive/60 dark:focus-visible:bg-destructive/60"
        onClick={() => runWindowAction(Quit)}
      >
        <X className="size-4" aria-hidden="true" />
      </button>
    </div>
  );
}

type CommandPaletteTriggerProps = React.ComponentProps<"button"> & {
  placeholder?: string;
};

function CommandPaletteTrigger({
  className,
  placeholder,
  onClick,
  ...props
}: CommandPaletteTriggerProps) {
  const { t } = useTranslation();
  const openPalette = useCommandPaletteStore((s) => s.setOpen);
  // kbd 文案跟随用户重绑：从 shortcuts 上下文拿 palette.open 的当前绑定。
  // 浮在 ShortcutsProvider 之外（极少数测试场景）时退回默认 ⌘P。
  const shortcuts = useOptionalShortcutsContext();
  const chord = shortcuts?.bindings.get("palette.open");
  const shortcutLabel = chord ? formatChord(chord, shortcuts!.platform) : "⌘P";
  const resolvedPlaceholder =
    placeholder ?? t("app.commandPalette.placeholder");
  const openLabel = t("app.commandPalette.open");

  return (
    <button
      type="button"
      onClick={(event) => {
        onClick?.(event);
        if (event.defaultPrevented) return;
        openPalette(true);
      }}
      aria-label={openLabel}
      title={openLabel}
      className={cn(
        "hidden h-[30px] w-[520px] max-w-[40vw] items-center gap-2 rounded-md border border-border bg-card/60 px-2 text-left text-xs text-muted-foreground shadow-xs outline-none transition-colors hover:bg-card hover:text-foreground md:flex",
        "wails-no-drag cursor-text",
        className,
      )}
      {...props}
    >
      <Search className="size-3.5 shrink-0" aria-hidden="true" />
      <span className="min-w-0 flex-1 truncate">{resolvedPlaceholder}</span>
      <kbd className="rounded-sm border border-border bg-secondary/60 px-1.5 py-0.5 font-mono text-2xs font-medium text-muted-foreground">
        {shortcutLabel}
      </kbd>
    </button>
  );
}

type AppTopBarProps = React.ComponentProps<"header"> & {
  appName?: string;
  breadcrumb?: string;
  platform?: DesktopPlatform;
};

function AppTopBar({
  appName = "Agentre",
  breadcrumb,
  className,
  onDoubleClick,
  platform = "unknown",
  ...props
}: AppTopBarProps) {
  function handleTitleBarDoubleClick(
    event: React.MouseEvent<HTMLElement, MouseEvent>,
  ) {
    onDoubleClick?.(event);

    if (
      event.defaultPrevented ||
      isNoDragTarget(event.target, event.currentTarget)
    ) {
      return;
    }

    runWindowAction(WindowToggleMaximise);
  }

  return (
    <header
      className={cn(
        "wails-drag flex h-11 shrink-0 items-center gap-3 border-b border-border bg-rail px-3",
        platform === "windows" && "pr-0",
        className,
      )}
      {...props}
      onDoubleClick={handleTitleBarDoubleClick}
    >
      {platform === "darwin" ? <NativeWindowControlsInset /> : null}

      <div className="flex min-w-0 items-center gap-2">
        <span className="inline-flex size-[22px] shrink-0 items-center justify-center">
          <img
            src={logoMarkUrl}
            alt=""
            aria-hidden="true"
            className="size-full object-contain"
            draggable={false}
          />
        </span>
        <span className="text-sm font-semibold">{appName}</span>
        {breadcrumb ? (
          <>
            <span className="font-mono text-sm text-decorative-foreground">
              /
            </span>
            <span className="min-w-0 truncate text-sm text-muted-foreground">
              {breadcrumb}
            </span>
          </>
        ) : null}
      </div>

      <div className="min-w-0 flex-1" />
      <CommandPaletteTrigger />
      <div className="min-w-0 flex-1" />

      <div className="flex h-full shrink-0 items-center gap-2">
        {platform === "windows" ? <WindowsWindowControls /> : null}
      </div>
    </header>
  );
}

/**
 * UpdateStatusPill 占据状态栏右下角原先那段版本号的位置。
 *
 * 只读 store、只回调，不订阅任何东西 —— 订阅挂在 App 层（useUpdateWatch），
 * 否则每个渲染状态栏的测试都要先造一套 wails runtime。
 *
 * 没有更新时它退回今天的灰色版本号：「有更新」要是一次真的状态跃迁，
 * 而不是一直挂在那的装饰。
 */
function UpdateStatusPill({
  version,
  onOpenSettings,
}: {
  version: string;
  onOpenSettings: () => void;
}) {
  const { t } = useTranslation();
  const phase = useUpdateStore((s) => s.phase);
  // 面板开合放在 store 里:到达提示的「查看更新」要能把它拉开。
  const open = useUpdateStore((s) => s.panelOpen);
  const setPanelOpen = useUpdateStore((s) => s.setPanelOpen);

  const base =
    "wails-no-drag inline-flex cursor-pointer items-center gap-1.5 rounded px-1.5 py-0.5 font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50";

  let content: React.ReactNode;
  let tone = "text-muted-foreground hover:bg-accent hover:text-foreground";
  // 无障碍名单独给：胶囊里的文案是缩写（「检查中」「下载中 42%」），
  // 读屏要听到的是「什么状态 + 哪个版本」。
  let ariaLabel = t("update.pill.aria.idle", { version });

  switch (phase.kind) {
    case "checking":
      content = (
        <>
          <Loader2
            className="size-3 shrink-0 animate-spin"
            aria-hidden="true"
          />
          {t("update.pill.checking")}
        </>
      );
      tone = "text-muted-foreground";
      ariaLabel = t("update.pill.aria.checking", { version });
      break;
    case "available":
      content = (
        <>
          <ArrowUp className="size-3 shrink-0" aria-hidden="true" />
          {t("update.pill.available", { version: phase.info.latestVersion })}
        </>
      );
      tone =
        "border border-primary/25 bg-primary-soft text-primary-text hover:bg-primary-soft/80";
      ariaLabel = t("update.pill.aria.available", {
        version: phase.info.latestVersion,
      });
      break;
    case "downloading":
      content = (
        <>
          <Download className="size-3 shrink-0" aria-hidden="true" />
          {t("update.pill.downloading", { percent: phase.progress })}
        </>
      );
      tone =
        "border border-primary/25 bg-primary-soft text-primary-text hover:bg-primary-soft/80";
      ariaLabel = t("update.pill.aria.downloading", {
        version: phase.info.latestVersion,
        percent: phase.progress,
      });
      break;
    case "installed":
      content = (
        <>
          <RotateCw className="size-3 shrink-0" aria-hidden="true" />
          {t("update.pill.restart")}
        </>
      );
      tone =
        "border border-status-running/30 bg-status-running-bg text-status-running hover:opacity-90";
      ariaLabel = t("update.pill.aria.installed", {
        version: phase.info.latestVersion,
      });
      break;
    case "error":
      content = (
        <>
          <TriangleAlert className="size-3 shrink-0" aria-hidden="true" />
          {t("update.pill.failed")}
        </>
      );
      tone =
        "border border-destructive/30 bg-destructive-soft text-destructive hover:opacity-90";
      ariaLabel = t("update.pill.aria.failed", { version });
      break;
    default:
      // idle 与 uptodate：和今天完全一样的一段灰字，只是可以点开看看。
      content = version;
      break;
  }

  return (
    <Popover open={open} onOpenChange={setPanelOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={ariaLabel}
          disabled={phase.kind === "checking"}
          className={cn(
            base,
            tone,
            phase.kind === "checking" && "cursor-default",
          )}
        >
          {content}
        </button>
      </PopoverTrigger>
      <PopoverContent
        align="end"
        side="top"
        sideOffset={8}
        className="w-[380px] p-0"
      >
        <UpdatePanel version={version} onOpenSettings={onOpenSettings} />
      </PopoverContent>
    </Popover>
  );
}

type AppStatusBarProps = React.ComponentProps<"footer"> & {
  agentCount: number;
  runningCount: number;
  approvalCount: number;
  unreadCount: number;
  attentionIds: number[];
  onAttentionClick: (sessionId: number) => void;
  /** 更新面板里的「打开更新设置」——宿主负责深链到设置页。 */
  onOpenUpdateSettings?: () => void;
  status: AgentStatus;
  version: string;
};

function AppStatusBar({
  agentCount,
  runningCount,
  approvalCount,
  unreadCount,
  attentionIds,
  className,
  onAttentionClick,
  onOpenUpdateSettings,
  status,
  version,
  ...props
}: AppStatusBarProps) {
  const { t } = useTranslation();
  const agentSummary = t("statusBar.agentSummary", {
    count: agentCount,
    running: runningCount,
  });

  const attentionParts: string[] = [];
  if (approvalCount > 0) {
    attentionParts.push(
      t(
        approvalCount === 1
          ? "statusBar.approval_one"
          : "statusBar.approval_other",
        { count: approvalCount },
      ),
    );
  }
  if (unreadCount > 0) {
    attentionParts.push(
      t(unreadCount === 1 ? "statusBar.unread_one" : "statusBar.unread_other", {
        count: unreadCount,
      }),
    );
  }

  const attentionSummary =
    attentionParts.length > 0 ? attentionParts.join(" · ") : null;
  const firstAttentionId = attentionIds[0];

  return (
    <footer
      className={cn(
        "flex h-7 shrink-0 items-center gap-3 border-t border-border bg-rail px-3 font-mono text-2xs leading-none text-muted-foreground",
        className,
      )}
      {...props}
    >
      <span className="flex items-center gap-1.5 font-medium">
        <StatusDot status={status} size="xs" />
        {agentSummary}
      </span>
      {attentionSummary !== null && firstAttentionId !== undefined ? (
        <>
          <span className="hidden text-border-strong sm:inline">·</span>
          <button
            type="button"
            onClick={() => onAttentionClick(firstAttentionId)}
            aria-label={t("statusBar.attentionAria", {
              summary: attentionSummary,
            })}
            title={attentionSummary}
            className="group flex min-w-0 cursor-pointer items-center gap-1.5 rounded font-medium text-status-waiting transition-colors hover:bg-rail-accent hover:text-accent-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
          >
            <CircleAlert className="size-3.5 shrink-0" aria-hidden="true" />
            <span className="min-w-0 truncate">{attentionSummary}</span>
            <ChevronUp
              className="size-3 shrink-0 opacity-60 transition-opacity group-hover:opacity-100"
              aria-hidden="true"
            />
          </button>
        </>
      ) : null}
      <span className="min-w-0 flex-1" />
      <UpdateStatusPill
        version={version}
        onOpenSettings={onOpenUpdateSettings ?? (() => {})}
      />
    </footer>
  );
}

export {
  AppStatusBar,
  AppTopBar,
  CommandPaletteTrigger,
  NativeWindowControlsInset,
  ThemeToggle,
  WindowsWindowControls,
};
export type { AppTheme, AppThemePreference, DesktopPlatform };

import * as React from "react";
import { useTranslation } from "react-i18next";
import {
  ChevronUp,
  CircleAlert,
  Minus,
  Monitor,
  Moon,
  Search,
  Square,
  Sun,
  X,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";

import logoMarkUrl from "@/assets/images/logo-mark.png";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { useCommandPaletteStore } from "@/stores/command-palette-store";
import type { AgentStatus } from "@/stores/types";
import {
  Quit,
  WindowMinimise,
  WindowToggleMaximise,
} from "../../../wailsjs/runtime/runtime";

import { StatusDot } from "./primitives";
import { formatChord } from "./shortcuts/format";
import { useOptionalShortcutsContext } from "./shortcuts/shortcuts-provider";

type DesktopPlatform = "darwin" | "windows" | "linux" | "unknown";
type AppTheme = "light" | "dark";
type AppThemePreference = AppTheme | "system";

const themePreferenceOrder: AppThemePreference[] = ["system", "light", "dark"];

const themePreferenceMeta: Record<
  AppThemePreference,
  { icon: LucideIcon; labelKey: string }
> = {
  system: { icon: Monitor, labelKey: "theme.system" },
  light: { icon: Sun, labelKey: "theme.lightMode" },
  dark: { icon: Moon, labelKey: "theme.darkMode" },
};

function nextThemePreference(
  themePreference: AppThemePreference,
): AppThemePreference {
  const currentIndex = themePreferenceOrder.indexOf(themePreference);
  const nextIndex =
    (currentIndex < 0 ? 0 : currentIndex + 1) % themePreferenceOrder.length;

  return themePreferenceOrder[nextIndex];
}

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

type ThemeToggleProps = {
  className?: string;
  effectiveTheme?: AppTheme;
  onThemePreferenceChange?: (themePreference: AppThemePreference) => void;
  themePreference?: AppThemePreference;
};

function ThemeToggle({
  className,
  effectiveTheme,
  onThemePreferenceChange,
  themePreference,
}: ThemeToggleProps) {
  const { t } = useTranslation();

  if (!themePreference || !onThemePreferenceChange) {
    return null;
  }

  const meta = themePreferenceMeta[themePreference];
  const Icon = meta.icon;
  const next = nextThemePreference(themePreference);
  const nextMeta = themePreferenceMeta[next];
  const currentDescription =
    themePreference === "system" && effectiveTheme
      ? t("theme.systemWithResolved", {
          resolved:
            effectiveTheme === "dark" ? t("theme.dark") : t("theme.light"),
        })
      : t(meta.labelKey);
  const nextDescription = t(nextMeta.labelKey);

  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      className={cn(
        "wails-no-drag group relative size-10 overflow-visible rounded-lg text-sidebar-icon hover:bg-rail-accent hover:text-sidebar-accent-foreground [&_svg:not([class*='size-'])]:size-[18px]",
        className,
      )}
      aria-label={t("theme.toggle", {
        current: currentDescription,
        next: nextDescription,
      })}
      title={t("theme.toggleTitle", {
        current: currentDescription,
        next: nextDescription,
      })}
      onClick={() => onThemePreferenceChange(next)}
    >
      <Icon data-icon="only" aria-hidden="true" />
    </Button>
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

type AppStatusBarProps = React.ComponentProps<"footer"> & {
  agentCount: number;
  runningCount: number;
  approvalCount: number;
  unreadCount: number;
  attentionIds: number[];
  onAttentionClick: (sessionId: number) => void;
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
      <span className="text-muted-foreground">{version}</span>
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

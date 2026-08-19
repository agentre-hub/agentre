import * as React from "react";
import { useTranslation } from "react-i18next";
import {
  ChevronDown,
  CircleHelp,
  ClipboardList,
  ShieldCheck,
  ShieldOff,
} from "lucide-react";

import { cn } from "@/lib/utils";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";

import {
  PERMISSION_MODE_META_UI,
  fallbackPermissionModeMetaUI,
  permissionModeDisabledReason,
  type PermissionMode,
  type PermissionModeDisableCtx,
} from "./types";

const ICON_MAP = {
  "circle-question": CircleHelp,
  "shield-check": ShieldCheck,
  "clipboard-list": ClipboardList,
  "shield-off": ShieldOff,
} as const;

export interface PermissionModePillProps {
  mode: PermissionMode;
  onSelect: (mode: PermissionMode) => void;
  /**
   * 当前 runtime 允许的 mode 集合,按 cycle 顺序传入(对应
   * caps.permissionModeMeta.order)。空数组 = runtime 不支持 permission mode 切换,
   * pill 会渲染但 popover 为空。
   */
  modes: readonly PermissionMode[];
  /** 切换被后端拒绝时显示的提示;null = 没有错误。 */
  errorMessage?: string | null;
  disabled?: boolean;
  /**
   * runtime key(claudecode/codex/builtin/remote)。仅供
   * permissionModeDisabledReason 判定 claudecode bypass-lockout 规则使用。
   * 留空时所有 mode 均可选(不触发 lockout)。
   */
  runtimeKey?: string | null;
  /**
   * 当前会话 spawn 时下发的 permission mode。bypass-lockout 判定输入。
   * 空串/undefined/null = pre-spawn 或老会话(视作非 bypass 启动)。
   */
  permissionModeAtLaunch?: PermissionMode | null;
  /** 当前会话是否已经启动(spawn 过 CLI 子进程;典型判定 messages.length > 0)。 */
  hasActiveSession?: boolean;
}

export function PermissionModePill({
  mode,
  onSelect,
  modes,
  errorMessage,
  disabled,
  runtimeKey,
  permissionModeAtLaunch,
  hasActiveSession,
}: PermissionModePillProps) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const meta =
    PERMISSION_MODE_META_UI[mode] ?? fallbackPermissionModeMetaUI(mode);
  const Icon = ICON_MAP[meta.iconName];

  function handleSelect(next: PermissionMode) {
    setOpen(false);
    onSelect(next);
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          disabled={disabled}
          aria-label={t("permissionMode.aria", { label: meta.label })}
          title={
            disabled
              ? t("permissionMode.titleDisabled", { label: meta.label })
              : t("permissionMode.title", { label: meta.label })
          }
          className={cn(
            "inline-flex h-6 cursor-pointer items-center gap-1 rounded-md border px-2 text-2xs font-medium leading-none transition-colors",
            "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
            "disabled:cursor-not-allowed disabled:opacity-60",
            meta.pillClass,
          )}
        >
          <Icon className={cn("size-3", meta.iconClass)} aria-hidden="true" />
          {/* 极窄的 composer(侧栏 + 右侧面板同开)退成纯图标:模式语义由图标配色、
              aria-label 与 title 承载,标签是最后一档才让位的。 */}
          <span className="@max-[620px]/composer:hidden">{meta.label}</span>
          <ChevronDown
            className={cn("size-2.5", meta.iconClass)}
            aria-hidden="true"
          />
        </button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        side="top"
        sideOffset={8}
        className="w-[320px] p-0"
      >
        <div className="flex items-center justify-between border-b border-border px-3.5 py-2.5">
          <div className="flex items-center gap-1.5">
            <ShieldCheck
              className="size-3.5 text-foreground"
              aria-hidden="true"
            />
            <span className="text-xs font-semibold text-foreground">
              {t("permissionMode.heading")}
            </span>
          </div>
          <kbd className="inline-flex items-center gap-1 rounded-sm border border-border bg-muted px-1.5 py-0.5 font-mono text-2xs text-foreground">
            Shift<span className="text-decorative-foreground">+</span>Tab
          </kbd>
        </div>
        <ul className="flex flex-col gap-0.5 p-1.5" role="listbox">
          {modes.map((m) => {
            const itemMeta =
              PERMISSION_MODE_META_UI[m] ?? fallbackPermissionModeMetaUI(m);
            const ItemIcon = ICON_MAP[itemMeta.iconName];
            const active = m === mode;
            const ctx: PermissionModeDisableCtx = {
              hasActiveSession,
              permissionModeAtLaunch,
            };
            const disabledReason = permissionModeDisabledReason(
              m,
              runtimeKey,
              ctx,
            );
            const isDisabled = disabledReason != null;
            return (
              <li key={m}>
                <button
                  type="button"
                  role="option"
                  aria-selected={active}
                  aria-disabled={isDisabled || undefined}
                  disabled={isDisabled}
                  onClick={() => {
                    if (isDisabled) return;
                    handleSelect(m);
                  }}
                  title={isDisabled ? disabledReason : undefined}
                  className={cn(
                    "flex w-full items-start gap-2.5 rounded-md px-2.5 py-2 text-left transition-colors",
                    isDisabled
                      ? "cursor-not-allowed opacity-50"
                      : "cursor-pointer",
                    active && !isDisabled ? "bg-accent" : "",
                    !active && !isDisabled ? "hover:bg-accent/60" : "",
                  )}
                >
                  <span className="mt-px inline-flex size-5 shrink-0 items-center justify-center">
                    <ItemIcon
                      className={cn("size-3.5", itemMeta.iconClass)}
                      aria-hidden="true"
                    />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center gap-1.5">
                      <span className="text-xs font-semibold text-foreground">
                        {itemMeta.label}
                      </span>
                      <span className="font-mono text-2xs text-muted-foreground">
                        {itemMeta.key}
                      </span>
                      {active ? (
                        <span className="ml-auto inline-flex items-center gap-1 rounded-sm bg-primary-soft px-1.5 py-px font-mono text-2xs font-semibold tracking-wider text-primary-text">
                          <span className="size-1 rounded-full bg-primary" />
                          {t("permissionMode.active")}
                        </span>
                      ) : null}
                    </span>
                    {itemMeta.desc ? (
                      <span className="mt-0.5 block text-2xs leading-snug text-muted-foreground">
                        {itemMeta.desc}
                      </span>
                    ) : null}
                    {isDisabled ? (
                      <span className="mt-1 block text-2xs leading-snug text-destructive">
                        {disabledReason}
                      </span>
                    ) : null}
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
        <div className="border-t border-border px-3.5 py-2 text-2xs text-muted-foreground">
          {t("permissionMode.footer")}
        </div>
        {errorMessage ? (
          <div className="border-t border-destructive/40 bg-destructive-soft px-3.5 py-1.5 text-2xs text-destructive">
            {errorMessage}
          </div>
        ) : null}
      </PopoverContent>
    </Popover>
  );
}

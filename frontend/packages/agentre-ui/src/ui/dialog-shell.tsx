/**
 * 弹窗外壳：两端共用的那一份（规格 2026-08-22「A 段」，决策 6）。
 *
 * 它原来只住在 `agentre-server`。2026-08-21 那一轮把它留在宿主，理由是「桌面端没有
 * 对应形态」——这一轮桌面端的项目弹窗正要获得这个形态（保存态在头部、脚部只有
 * 「完成」、错误落脚部左侧、窄屏贴底 sheet），前提没了，它就该进包。
 *
 * 七条规范，逐条对着一个具体毛病：
 *
 *  1. **尺寸成阶梯**：sm 420（一个决定）/ md 560（一件事的表单）/ lg 760（带浏览
 *     面板）。由用途决定，不由调用点临时塞 className——否则确认框和目录选择器一样宽。
 *  2. **窄屏是贴底 sheet**：`<640px` 满宽贴底、上圆角、最高 90dvh、脚部固定在安全区
 *     之上、拖动条可见。**基础样式就是 sheet，`sm:` 才把它变回浮卡**——反过来写
 *     （浮卡基础 + `max-sm:` 覆盖）在窄屏上会先画一帧浮卡再跳。同一个组件、同一套
 *     props，表单不重写第二份。
 *  3. **只有 body 滚**：头与脚 `shrink-0`，body `min-h-0 flex-1 overflow-y-auto`。
 *     `min-h-0` 是关键：不写它 flex 子项不会缩，头脚会被正文顶出去。
 *  4. **错误落在弹窗里**：整窗级错误摆在脚部左侧、与按钮同一行——点了按钮的人视线
 *     就在那。字段级错误归调用方摆在字段下面。
 *  5. **主按钮自带 busy**：转圈 + 禁用，且 busy 期间 Esc 与点遮罩都不关窗——写请求
 *     正在飞，关掉只会让人以为没提交。
 *  6. **危险确认是一种形态**，不是一段文案：头部一道 danger 分隔线 + destructive
 *     主按钮，后果写在正文，不写进标题。
 *  7. **即时保存的弹窗没有「保存」**：保存态显示在头部右侧，脚部只有「完成」。
 *
 * **窄屏那一档必须在真的窄视口下看**：`sm:` 断点读的是视口宽度，不是容器宽度，
 * 在宽视口里缩一个框模拟出来的窄屏是假的。
 */
import * as React from "react";
import { Dialog as DialogPrimitive } from "radix-ui";
import { Check, Loader2, X } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { Button } from "./button";
import { cn } from "../lib/utils";

export type DialogShellSize = "sm" | "md" | "lg";

/** 保存态：即时保存的弹窗把它摆在头部右侧（规范 7）。 */
export type DialogShellSaveState = "idle" | "saving" | "saved" | "error";

const SIZE_CLASS: Record<DialogShellSize, string> = {
  sm: "sm:max-w-[420px]",
  md: "sm:max-w-[560px]",
  lg: "sm:max-w-[760px]",
};

export function DialogShell({
  open,
  onOpenChange,
  size = "md",
  busy = false,
  danger = false,
  className,
  children,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  size?: DialogShellSize;
  /** busy 期间不许 Esc / 点遮罩关窗（规范 5）。 */
  busy?: boolean;
  danger?: boolean;
  className?: string;
  children: React.ReactNode;
}) {
  const blockWhileBusy = React.useCallback(
    (event: { preventDefault: () => void }) => {
      if (busy) event.preventDefault();
    },
    [busy],
  );
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay
          data-slot="dialog-shell-overlay"
          className="fixed inset-0 z-50 bg-scrim backdrop-blur-[3px] backdrop-saturate-150 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0"
        />
        <DialogPrimitive.Content
          data-slot="dialog-shell-content"
          onEscapeKeyDown={blockWhileBusy}
          onPointerDownOutside={blockWhileBusy}
          onInteractOutside={blockWhileBusy}
          className={cn(
            "fixed z-50 flex flex-col overflow-hidden bg-card text-card-foreground shadow-overlay outline-none",
            // 窄屏（基础）：贴底、满宽、上圆角、从下滑入。
            "inset-x-0 bottom-0 max-h-[90dvh] rounded-t-2xl border-t border-border",
            "data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:slide-in-from-bottom",
            "data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:slide-out-to-bottom",
            // ≥640px：居中浮卡，按用途取一档宽度。
            "sm:inset-x-auto sm:bottom-auto sm:left-1/2 sm:top-1/2 sm:w-[calc(100%-2rem)] sm:max-h-[min(86dvh,720px)] sm:-translate-x-1/2 sm:-translate-y-1/2 sm:rounded-xl sm:rounded-t-xl sm:border",
            SIZE_CLASS[size],
            "sm:data-[state=open]:zoom-in-95 sm:data-[state=open]:slide-in-from-bottom-0",
            "sm:data-[state=closed]:zoom-out-95 sm:data-[state=closed]:slide-out-to-bottom-0",
            danger && "border-destructive/40",
            className,
          )}
        >
          {/* 拖动条只在 sheet 那一档露出来：宽屏的浮卡拖不动，画一条只会说谎。 */}
          <div
            data-slot="dialog-shell-grip"
            aria-hidden="true"
            className="flex shrink-0 justify-center pt-2 sm:hidden"
          >
            <span className="h-1 w-9 rounded-full bg-border-strong" />
          </div>
          {children}
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}

export function DialogShellHeader({
  title,
  subtitle,
  danger = false,
  saveState = "idle",
  onClose,
  busy = false,
  actions,
}: {
  title: string;
  subtitle?: string;
  danger?: boolean;
  saveState?: DialogShellSaveState;
  onClose?: () => void;
  busy?: boolean;
  /** 头部右侧的额外动作（如某一节的「＋ 添加」）。 */
  actions?: React.ReactNode;
}) {
  const { t } = useUiTranslation();
  return (
    <div
      data-slot="dialog-shell-header"
      className={cn(
        "flex shrink-0 items-start gap-3 border-b px-5 py-3.5",
        danger
          ? "border-destructive/30 bg-destructive-soft/40"
          : "border-border",
      )}
    >
      <div className="min-w-0 flex-1">
        <DialogPrimitive.Title className="text-sm font-semibold tracking-tight text-foreground">
          {title}
        </DialogPrimitive.Title>
        {subtitle ? (
          <DialogPrimitive.Description className="mt-0.5 text-xs leading-relaxed text-muted-foreground">
            {subtitle}
          </DialogPrimitive.Description>
        ) : null}
      </div>
      {saveState !== "idle" ? (
        <span
          data-slot="dialog-shell-save-state"
          aria-live="polite"
          className={cn(
            "mt-0.5 inline-flex shrink-0 items-center gap-1 text-2xs",
            saveState === "error"
              ? "text-destructive"
              : "text-muted-foreground",
          )}
        >
          {saveState === "saving" ? (
            <>
              <Loader2 className="size-3 animate-spin" aria-hidden="true" />
              {t("common.saving")}
            </>
          ) : saveState === "saved" ? (
            <>
              <Check
                className="size-3 text-status-running"
                aria-hidden="true"
              />
              {t("common.saved")}
            </>
          ) : (
            t("common.saveFailed")
          )}
        </span>
      ) : null}
      {actions ? <div className="shrink-0">{actions}</div> : null}
      {onClose ? (
        <button
          type="button"
          aria-label={t("common.close")}
          disabled={busy}
          onClick={onClose}
          className="-mr-1 mt-0.5 inline-flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-40"
        >
          <X className="size-4" aria-hidden="true" />
        </button>
      ) : null}
    </div>
  );
}

/**
 * 只有它滚（规范 3）。`min-h-0` 不能少：flex 子项默认 `min-height:auto`，正文一长
 * 就把头脚顶出可视区，而不是自己出滚动条。
 */
export function DialogShellBody({
  children,
  className,
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div
      data-slot="dialog-shell-body"
      className={cn("min-h-0 flex-1 overflow-y-auto px-5 py-4", className)}
    >
      {children}
    </div>
  );
}

export function DialogShellFooter({
  error,
  left,
  children,
}: {
  /** 整窗级错误：与按钮同一行、在它们左边（规范 4）。 */
  error?: string | null;
  /** 没有错误时这块摆什么（如目录选择器里当前选中的路径）。**错误优先**。 */
  left?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div
      data-slot="dialog-shell-footer"
      className="flex shrink-0 items-center gap-3 border-t border-border bg-secondary/40 px-5 py-3 pb-[max(0.75rem,env(safe-area-inset-bottom))]"
    >
      <div className="min-w-0 flex-1">
        {error ? (
          <p role="alert" className="truncate text-xs text-destructive">
            {error}
          </p>
        ) : (
          (left ?? null)
        )}
      </div>
      <div className="flex shrink-0 items-center gap-2">{children}</div>
    </div>
  );
}

/**
 * 主按钮自带 busy（规范 5）：转圈 + 禁用。放在构件里而不是每个调用点各写一遍——
 * 「提交中还能再点一次」这种漏，每个调用点都有一次机会犯。
 */
export function DialogShellSubmit({
  busy = false,
  disabled,
  children,
  ...props
}: React.ComponentProps<typeof Button> & { busy?: boolean }) {
  return (
    <Button disabled={busy || disabled} {...props}>
      {busy ? (
        <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
      ) : null}
      {children}
    </Button>
  );
}

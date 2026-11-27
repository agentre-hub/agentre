import * as React from "react";
import { createPortal } from "react-dom";
import { Minus, MoreHorizontal } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  Button,
  DropdownMenu,
  DropdownMenuTrigger,
  cn,
} from "@agentre-hub/agentre-ui";
import { detectBrowserPlatform } from "@/lib/platform";
import {
  useToolWindowLayout,
  type FloatRect,
} from "@/stores/tool-window-layout-store";

import { useOptionalShortcutsContext } from "../shortcuts/shortcuts-provider";
import { matchFocusedToolWindowShortcut } from "../shortcuts/tool-window-shortcuts";
import { useToolWindowLayoutContext } from "./layout-context";
import { ToolWindowMenuContent } from "./tool-window-menu";
import type { ToolWindowDefinition } from "./types";

/**
 * 工具窗此刻在哪儿画：停靠在槽位里、浮在编辑区上方（浮动模式）、窄窗口下盖在
 * 对话上方的右侧覆盖层、或最大化占满工具窗区域。
 */
export type ToolWindowPresentation =
  | "docked"
  | "float"
  | "overlay"
  | "maximized";

type Props = {
  window: ToolWindowDefinition;
  presentation?: ToolWindowPresentation;
  /** 焦点移到编辑区或其它工具窗时会自动收起（只用于标注，收起由布局负责）。 */
  autoHide?: boolean;
  className?: string;
  style?: React.CSSProperties;
};

/** 浮动窗没被拖过时的默认位置与大小：靠右上角。 */
const DEFAULT_FLOAT_STYLE: React.CSSProperties = {
  top: 12,
  right: 12,
  width: 340,
  height: 460,
};

/** 拖动时至少留这么多在工具窗区域里，免得拖丢。 */
const KEEP_VISIBLE = { x: 80, y: 32 };

type FrameSlots = {
  /** 标题栏里留给工具窗自己的那一段（名称与 ⋯ 之间）；还没挂上时为 null。 */
  titleBar: HTMLElement | null;
  /**
   * 外壳的按键处理（⇧Esc / Esc / ⇧⌘'）。内容里有从别的 React 树挂进来的 DOM（终端
   * 面板住在常驻宿主里、只把容器挂进窗口）时，它们的按键不经 React 冒到外壳，内容
   * 用原生监听把按键交给这里。
   */
  handleKey: (event: KeyboardEvent) => void;
};

const FrameSlotsContext = React.createContext<FrameSlots | null>(null);

/**
 * 把 children 放进所在工具窗的标题栏（spec「终端与网页预览的标题栏同时承载各自的
 * 标签」）。不在工具窗外壳里时原地渲染。
 */
export function ToolWindowTitleBarSlot({
  children,
}: {
  children: React.ReactNode;
}) {
  const slots = React.useContext(FrameSlotsContext);
  if (slots === null) return <>{children}</>;
  return slots.titleBar ? createPortal(children, slots.titleBar) : null;
}

/** 所在工具窗外壳的按键处理；不在外壳里时为 null。 */
export function useToolWindowFrameKeys():
  | ((event: KeyboardEvent) => void)
  | null {
  return React.useContext(FrameSlotsContext)?.handleKey ?? null;
}

/**
 * 一个工具窗的外壳（spec「工具窗标题栏」）：带名称的区域，标题栏显示名称（带数量
 * 的工具窗跟着数字），⋯ 与「隐藏」平时不占眼，悬停或焦点落进来时出现；焦点在
 * 工具窗里时整圈描边。内容超出可视高度时在外壳内滚动。
 *
 * 被图标条点开时它自己把焦点拿进来（store 的 focusRequest），启动时恢复显示的
 * 工具窗不抢输入框的焦点。焦点在里面时：⇧Esc 隐藏、Esc 把焦点还给输入框、⇧⌘'
 * 最大化 / 还原。浮动时拖标题栏移动、拖右下角改大小，位置与大小记进布局。
 */
function ToolWindowFrameImpl({
  window: w,
  presentation = "docked",
  autoHide = false,
  className,
  style,
}: Props) {
  const { t } = useTranslation();
  const platform =
    useOptionalShortcutsContext()?.platform ?? detectBrowserPlatform();
  const { focusEditor } = useToolWindowLayoutContext();
  const hide = useToolWindowLayout((s) => s.hide);
  const toggleMaximize = useToolWindowLayout((s) => s.toggleMaximize);
  const setFloatRect = useToolWindowLayout((s) => s.setFloatRect);
  const floatRect = useToolWindowLayout((s) =>
    presentation === "float" ? s.floatRect[w.id] : undefined,
  );
  const focusRequested = useToolWindowLayout((s) => s.focusRequest === w.id);
  const clearFocusRequest = useToolWindowLayout((s) => s.clearFocusRequest);
  const ref = React.useRef<HTMLElement>(null);

  React.useEffect(() => {
    if (!focusRequested) return;
    ref.current?.focus();
    clearFocusRequest();
  }, [focusRequested, clearFocusRequest]);

  const handleKey = React.useCallback(
    (event: KeyboardEvent) => {
      // 菜单等弹层经 React 树冒泡上来的按键不算「焦点在工具窗里」；弹层自己处理过
      // （例如 Esc 关菜单）的也不再处理。
      if (event.defaultPrevented) return;
      if (!ref.current?.contains(event.target as Node)) return;
      const action = matchFocusedToolWindowShortcut(event, platform);
      if (action === null) return;
      event.preventDefault();
      if (action === "hide") {
        hide(w.id);
        focusEditor();
      } else if (action === "returnFocus") {
        // 「失焦自动收起」的工具窗随焦点离开由布局收起；浮动窗不收起。
        focusEditor();
      } else {
        toggleMaximize(w.id);
      }
    },
    [platform, hide, focusEditor, toggleMaximize, w.id],
  );
  const onKeyDown = (e: React.KeyboardEvent<HTMLElement>) =>
    handleKey(e.nativeEvent);

  // 描边画在内容之上，由「焦点是否在工具窗内」这个事实决定渲不渲染（鼠标、键盘聚焦都算）。
  const [focused, setFocused] = React.useState(false);
  const onFocus = () => setFocused(true);
  const onBlur = (e: React.FocusEvent<HTMLElement>) => {
    if (!e.currentTarget.contains(e.relatedTarget)) setFocused(false);
  };

  const [titleBar, setTitleBar] = React.useState<HTMLElement | null>(null);
  const slots = React.useMemo<FrameSlots>(
    () => ({ titleBar, handleKey }),
    [titleBar, handleKey],
  );

  const trackPointer = (
    e: React.PointerEvent,
    next: (dx: number, dy: number, start: FloatRect) => FloatRect,
  ) => {
    const el = ref.current;
    if (e.button !== 0 || !el) return;
    e.preventDefault();
    if (!el.contains(document.activeElement)) el.focus({ preventScroll: true });
    const start: FloatRect = {
      x: el.offsetLeft,
      y: el.offsetTop,
      width: el.offsetWidth,
      height: el.offsetHeight,
    };
    const parent = el.parentElement;
    const onMove = (ev: PointerEvent) => {
      const rect = next(ev.clientX - e.clientX, ev.clientY - e.clientY, start);
      setFloatRect(w.id, keepInside(rect, parent));
    };
    const onUp = () => {
      document.removeEventListener("pointermove", onMove);
      document.removeEventListener("pointerup", onUp);
      document.removeEventListener("pointercancel", onUp);
      document.body.style.userSelect = "";
    };
    document.addEventListener("pointermove", onMove);
    document.addEventListener("pointerup", onUp);
    document.addEventListener("pointercancel", onUp);
    document.body.style.userSelect = "none";
  };

  const floating = presentation === "float";
  const onHeaderPointerDown = floating
    ? (e: React.PointerEvent) => {
        // 标题栏上的按钮照常点；拖的是标题栏本身。
        if ((e.target as Element).closest("button")) return;
        trackPointer(e, (dx, dy, s) => ({ ...s, x: s.x + dx, y: s.y + dy }));
      }
    : undefined;

  const floatStyle: React.CSSProperties | undefined = floating
    ? floatRect
      ? {
          left: floatRect.x,
          top: floatRect.y,
          width: floatRect.width,
          height: floatRect.height,
        }
      : DEFAULT_FLOAT_STYLE
    : undefined;

  return (
    <section
      ref={ref}
      aria-label={w.label}
      tabIndex={-1}
      data-tool-window={w.id}
      data-floating={presentation === "float" || presentation === "overlay"}
      data-auto-hide={autoHide}
      data-maximized={presentation === "maximized"}
      onKeyDown={onKeyDown}
      onFocus={onFocus}
      onBlur={onBlur}
      style={{ ...floatStyle, ...style }}
      className={cn(
        "group/tool-window flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden bg-sheet outline-none",
        // 停靠的与对话卡同一张纸：同圆角、同 --sheet-shadow；浮动 / 覆盖 / 最大化保持浮层的样子。
        presentation === "docked"
          ? "rounded-xl shadow-[var(--sheet-shadow)]"
          : "rounded-[10px]",
        presentation !== "float" && "relative",
        // 盖在工具窗区域上方的那几种：所在的覆盖层不接指针，它们自己接。
        presentation !== "docked" && "pointer-events-auto",
        floating &&
          "absolute max-h-[calc(100%-16px)] max-w-[calc(100%-16px)] shadow-overlay",
        presentation === "overlay" && "shadow-overlay",
        className,
      )}
    >
      <div
        data-tool-window-title-bar=""
        onPointerDown={onHeaderPointerDown}
        className={cn(
          "flex h-8 shrink-0 items-center gap-1 pr-1 pl-3",
          floating && "cursor-move select-none",
        )}
      >
        <h3 className="flex min-w-0 items-baseline gap-1.5 text-xs font-semibold text-foreground">
          <span className="truncate">{w.label}</span>
          {w.count != null ? (
            <span className="shrink-0 font-mono text-3xs font-normal text-muted-foreground">
              {w.count}
            </span>
          ) : null}
        </h3>
        {/* 工具窗自己的标题栏内容（终端 / 网页预览的标签）经 ToolWindowTitleBarSlot 挂到这里。 */}
        <div
          ref={setTitleBar}
          className="flex h-full min-w-0 flex-1 items-center"
        />
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label={t("toolWindows.options", { name: w.label })}
              title={t("toolWindows.options", { name: w.label })}
              className={HEADER_ACTION_CLASS}
            >
              <MoreHorizontal aria-hidden="true" />
            </Button>
          </DropdownMenuTrigger>
          <ToolWindowMenuContent id={w.id} align="end" />
        </DropdownMenu>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={t("toolWindows.hide")}
          title={t("toolWindows.hide")}
          onClick={() => hide(w.id)}
          className={HEADER_ACTION_CLASS}
        >
          <Minus aria-hidden="true" />
        </Button>
      </div>
      <div className="flex min-h-0 flex-1 flex-col overflow-auto">
        <FrameSlotsContext.Provider value={slots}>
          {w.content}
        </FrameSlotsContext.Provider>
      </div>
      {floating ? (
        <span
          aria-hidden="true"
          onPointerDown={(e) =>
            trackPointer(e, (dx, dy, s) => ({
              ...s,
              width: s.width + dx,
              height: s.height + dy,
            }))
          }
          className="absolute right-0 bottom-0 size-3 cursor-nwse-resize touch-none"
        />
      ) : null}
      {focused ? (
        <span
          aria-hidden="true"
          data-tool-window-focus-ring=""
          className="pointer-events-none absolute inset-0 rounded-[inherit] shadow-[inset_0_0_0_2px_var(--ring)]"
        />
      ) : null}
    </section>
  );
}

/** ⋯ 与「隐藏」：悬停或焦点落进工具窗时出现；菜单开着时保持可见。 */
const HEADER_ACTION_CLASS =
  "opacity-0 group-focus-within/tool-window:opacity-100 group-hover/tool-window:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100";

/** 浮动窗拖动后至少有一角留在工具窗区域里。 */
function keepInside(rect: FloatRect, parent: HTMLElement | null): FloatRect {
  const width = parent?.clientWidth ?? 0;
  const height = parent?.clientHeight ?? 0;
  if (width <= 0 || height <= 0) return rect;
  return {
    ...rect,
    x: Math.min(rect.x, width - KEEP_VISIBLE.x),
    y: Math.min(rect.y, height - KEEP_VISIBLE.y),
  };
}

export const ToolWindowFrame = React.memo(ToolWindowFrameImpl);

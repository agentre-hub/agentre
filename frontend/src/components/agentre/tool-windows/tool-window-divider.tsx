import * as React from "react";
import { useTranslation } from "react-i18next";

import { cn } from "@agentre-hub/agentre-ui";
import {
  DIVIDER_LIMITS,
  useToolWindowLayout,
  type DividerId,
} from "@/stores/tool-window-layout-store";

/** 键盘每按一下改变的比例。 */
const KEY_STEP = 0.02;

const LABEL_KEYS: Record<DividerId, string> = {
  right: "toolWindows.dividers.right",
  bottom: "toolWindows.dividers.bottom",
  rightSplit: "toolWindows.dividers.rightSplit",
  bottomSplit: "toolWindows.dividers.bottomSplit",
};

/**
 * 分隔条把左右两块分开时是竖条（aria-orientation=vertical），上下分开时是横条。
 * 比例永远是「被调整的那一块」占容器的份额：右侧列宽、底部行高、右上高、底部左宽。
 */
const VERTICAL: Record<DividerId, boolean> = {
  right: true,
  bottom: false,
  rightSplit: false,
  bottomSplit: true,
};

/** 指针位置 → 比例。 */
function ratioAt(id: DividerId, rect: DOMRect, x: number, y: number): number {
  switch (id) {
    case "right":
      return (rect.right - x) / rect.width;
    case "bottom":
      return (rect.bottom - y) / rect.height;
    case "rightSplit":
      return (y - rect.top) / rect.height;
    case "bottomSplit":
      return (x - rect.left) / rect.width;
  }
}

/** 方向键 → 比例变化（+1 让被调整的那一块变大）。 */
function keyDelta(id: DividerId, key: string): number {
  const grow: Record<DividerId, string> = {
    right: "ArrowLeft",
    bottom: "ArrowUp",
    rightSplit: "ArrowDown",
    bottomSplit: "ArrowRight",
  };
  const shrink: Record<DividerId, string> = {
    right: "ArrowRight",
    bottom: "ArrowDown",
    rightSplit: "ArrowUp",
    bottomSplit: "ArrowLeft",
  };
  if (key === grow[id]) return 1;
  if (key === shrink[id]) return -1;
  return 0;
}

type Props = {
  id: DividerId;
  /** 比例相对的容器（指针拖动时按它的矩形换算）。 */
  containerRef: React.RefObject<HTMLElement | null>;
  className?: string;
};

/**
 * 槽位之间、编辑区与槽位之间的可拖分隔条（spec「槽位与默认」「无障碍」）：指针拖，
 * 或获得焦点后用 ⌃⇧方向键（也接受不带修饰键的方向键）调整；比例记进全局布局。
 */
export function ToolWindowDivider({ id, containerRef, className }: Props) {
  const { t } = useTranslation();
  const ratio = useToolWindowLayout((s) => s.ratios[id]);
  const setRatio = useToolWindowLayout((s) => s.setRatio);
  const [dragging, setDragging] = React.useState(false);
  const vertical = VERTICAL[id];
  const { min, max } = DIVIDER_LIMITS[id];

  const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    const rect = containerRef.current?.getBoundingClientRect();
    if (!rect || rect.width <= 0 || rect.height <= 0) return;
    e.preventDefault();
    e.currentTarget.focus({ preventScroll: true });
    const onMove = (ev: PointerEvent) => {
      setRatio(id, ratioAt(id, rect, ev.clientX, ev.clientY));
    };
    const onUp = () => {
      document.removeEventListener("pointermove", onMove);
      document.removeEventListener("pointerup", onUp);
      document.removeEventListener("pointercancel", onUp);
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
      setDragging(false);
    };
    document.addEventListener("pointermove", onMove);
    document.addEventListener("pointerup", onUp);
    document.addEventListener("pointercancel", onUp);
    document.body.style.cursor = vertical ? "col-resize" : "row-resize";
    document.body.style.userSelect = "none";
    setDragging(true);
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (e.altKey || e.metaKey || e.ctrlKey !== e.shiftKey) return;
    const delta = keyDelta(id, e.key);
    if (delta === 0) return;
    e.preventDefault();
    setRatio(id, ratio + delta * KEY_STEP);
  };

  return (
    <div
      role="separator"
      tabIndex={0}
      aria-orientation={vertical ? "vertical" : "horizontal"}
      aria-label={t(LABEL_KEYS[id])}
      aria-valuemin={Math.round(min * 100)}
      aria-valuemax={Math.round(max * 100)}
      aria-valuenow={Math.round(ratio * 100)}
      data-dragging={dragging || undefined}
      onPointerDown={onPointerDown}
      onKeyDown={onKeyDown}
      className={cn(
        "relative z-20 shrink-0 touch-none outline-none select-none",
        vertical ? "w-2 cursor-col-resize" : "h-2 cursor-row-resize",
        // 视觉上只是一条 1px 的线，悬停 / 拖动 / 键盘焦点时着色。
        "after:absolute after:bg-transparent after:transition-colors after:content-['']",
        vertical
          ? "after:inset-y-0 after:left-1/2 after:w-px after:-translate-x-1/2"
          : "after:inset-x-0 after:top-1/2 after:h-px after:-translate-y-1/2",
        "hover:after:bg-primary/40 focus-visible:after:bg-primary data-[dragging]:after:bg-primary/60",
        className,
      )}
    />
  );
}

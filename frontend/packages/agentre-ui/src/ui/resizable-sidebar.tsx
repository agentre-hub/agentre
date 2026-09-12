import * as React from "react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";

import {
  SIDEBAR_DEFAULT_WIDTH,
  SIDEBAR_MAX_WIDTH,
  SIDEBAR_MIN_WIDTH,
  clampSidebarWidth,
  readSidebarWidth,
  writeSidebarWidth,
} from "./sidebar-width-state";

type ResizableSidebarProps = {
  // localStorage 命名空间，例 "chat" / "projects"，写入 key 是 agentre.sidebarWidth.<key>
  persistenceKey: string;
  ariaLabel: string;
  className?: string;
  children: React.ReactNode;
  // 拖拽手柄所在边。默认 "right"（左侧栏：往右拖增宽）。
  // 用于右侧栏时传 "left"：手柄在左边缘，往左拖增宽（dx 反向）。
  edge?: "left" | "right";
  // 首次读不到 localStorage 时的宽度兜底。默认 320px。
  defaultWidth?: number;
  // 宿主用例的抓手。两端各有各的 testid 词汇，包这边只做透传。
  testId?: string;
};

// ResizableSidebar 是 chat / projects 等页面侧栏的共享容器：
// - 渲染一个固定宽度的 <aside>，宽度来自 localStorage（首次默认 defaultWidth）；
// - 边缘 4px 命中区 + 1px 视觉条作为拖拽手柄；左右侧栏由 edge 区分。
// - 拖拽中走 pointer events，移到 document 监听，避免离开手柄就丢拖拽；
// - 结束时一次性 writeSidebarWidth，避免每 px 都打一次 localStorage。
function ResizableSidebar({
  persistenceKey,
  ariaLabel,
  className,
  children,
  edge = "right",
  defaultWidth = SIDEBAR_DEFAULT_WIDTH,
  testId,
}: ResizableSidebarProps) {
  const { t } = useUiTranslation();
  const [width, setWidth] = React.useState<number>(() =>
    readSidebarWidth(persistenceKey, defaultWidth),
  );
  const [dragging, setDragging] = React.useState(false);

  const startDrag = React.useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      // 只接受主按键的拖拽，避免右键 / 中键误触。
      if (e.button !== 0) return;
      e.preventDefault();
      const startX = e.clientX;
      const startWidth = width;
      // 右侧栏（edge=left）：鼠标往左移 → 宽度变大，dx 取反。
      const sign = edge === "left" ? -1 : 1;
      let latest = width;

      const onMove = (ev: PointerEvent) => {
        const next = clampSidebarWidth(
          startWidth + sign * (ev.clientX - startX),
          defaultWidth,
        );
        latest = next;
        setWidth(next);
      };
      const onUp = () => {
        document.removeEventListener("pointermove", onMove);
        document.removeEventListener("pointerup", onUp);
        document.removeEventListener("pointercancel", onUp);
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
        writeSidebarWidth(persistenceKey, latest);
        setDragging(false);
      };

      document.addEventListener("pointermove", onMove);
      document.addEventListener("pointerup", onUp);
      document.addEventListener("pointercancel", onUp);
      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
      setDragging(true);
    },
    [defaultWidth, edge, persistenceKey, width],
  );

  return (
    <aside
      aria-label={ariaLabel}
      data-testid={testId}
      style={{ width: `${width}px` }}
      className={cn(
        "relative flex shrink-0 flex-col bg-sidebar",
        edge === "left" ? "border-l border-border" : "border-r border-border",
        className,
      )}
    >
      {children}
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label={t("resizableSidebar.resizeAria", { label: ariaLabel })}
        aria-valuemin={SIDEBAR_MIN_WIDTH}
        aria-valuemax={SIDEBAR_MAX_WIDTH}
        aria-valuenow={width}
        title={t("resizableSidebar.resizeTitle")}
        onPointerDown={startDrag}
        className={cn(
          "absolute inset-y-0 z-20 block w-2 cursor-col-resize touch-none select-none",
          edge === "left" ? "-left-1" : "-right-1",
          // 视觉上是一条 1px 高亮条，hover / drag 时着色。
          "after:absolute after:inset-y-0 after:left-1/2 after:w-px after:-translate-x-1/2 after:bg-transparent after:transition-colors after:content-['']",
          "hover:after:bg-primary/40",
          dragging && "after:bg-primary/60 hover:after:bg-primary/60",
        )}
      />
    </aside>
  );
}

export { ResizableSidebar };
export type { ResizableSidebarProps };

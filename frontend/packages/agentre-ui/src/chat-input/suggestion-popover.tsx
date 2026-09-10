import * as React from "react";
import { createPortal } from "react-dom";

import { cn } from "../lib/utils";

const CURSOR_GAP = 4;
const VIEWPORT_MARGIN = 8;
const MAX_HEIGHT = 288;
const MIN_HEIGHT = 96;

type SuggestionPopoverProps = {
  open: boolean;
  anchorRect: { left: number; top: number; bottom: number } | null;
  selectedIndex: number;
  itemCount: number;
  ariaLabel: string;
  listboxId?: string;
  testId?: string;
  className?: string;
  footer?: React.ReactNode;
  /** 点击弹层与编辑器之外的区域时触发 —— 消费者用它关掉菜单。 */
  onDismiss?: () => void;
  /** 编辑器 contentEditable DOM;点击它不视为外部点击,交给编辑器自身的 selection 逻辑。 */
  editorElement?: HTMLElement | null;
  children: (activeRef: React.Ref<HTMLButtonElement>) => React.ReactNode;
};

// SuggestionPopover 统一输入框候选菜单的视口行为：以光标为锚点向上展开，
// 最多显示约 9 行，空间不足时缩短并内部滚动，键盘高亮项始终回到可视区。
export function SuggestionPopover({
  open,
  anchorRect,
  selectedIndex,
  itemCount,
  ariaLabel,
  listboxId,
  testId,
  className,
  footer,
  onDismiss,
  editorElement,
  children,
}: SuggestionPopoverProps): React.ReactElement | null {
  const activeRef = React.useRef<HTMLButtonElement>(null);
  const popoverRef = React.useRef<HTMLDivElement>(null);

  React.useEffect(() => {
    activeRef.current?.scrollIntoView({ block: "nearest" });
  }, [selectedIndex, itemCount, open]);

  // 点击弹层与编辑器之外的区域 → 关闭菜单。用 pointerdown(先于 mousedown)截获,
  // 弹层自身与编辑器内的点击都放行:前者用于选中项(项上挂 onMouseDown),
  // 后者交给编辑器 selectionUpdate 的 trigger 检测决定去留。
  React.useEffect(() => {
    if (!open || !onDismiss) return;
    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target;
      if (!(target instanceof Node)) return;
      if (popoverRef.current?.contains(target)) return;
      if (editorElement?.contains(target)) return;
      onDismiss();
    };
    document.addEventListener("pointerdown", handlePointerDown);
    return () => document.removeEventListener("pointerdown", handlePointerDown);
  }, [open, onDismiss, editorElement]);

  if (!open || !anchorRect || itemCount === 0) return null;

  const roomAbove = anchorRect.top - CURSOR_GAP - VIEWPORT_MARGIN;
  const style: React.CSSProperties = {
    position: "fixed",
    left: anchorRect.left,
    bottom: window.innerHeight - anchorRect.top + CURSOR_GAP,
    maxHeight: Math.max(MIN_HEIGHT, Math.min(MAX_HEIGHT, roomAbove)),
    zIndex: 50,
  };

  const popoverClassName = cn(
    "min-w-[14rem] max-w-[20rem] rounded-md border border-border bg-popover p-1 text-popover-foreground shadow-md",
    className,
  );

  if (!footer) {
    return createPortal(
      <div
        id={listboxId}
        data-testid={testId}
        role="listbox"
        aria-label={ariaLabel}
        style={style}
        ref={popoverRef}
        className={cn(popoverClassName, "overflow-y-auto overscroll-contain")}
      >
        {children(activeRef)}
      </div>,
      document.body,
    );
  }

  return createPortal(
    <div
      data-testid={testId}
      style={style}
      ref={popoverRef}
      className={cn(popoverClassName, "flex flex-col overflow-hidden")}
    >
      <div
        id={listboxId}
        role="listbox"
        aria-label={ariaLabel}
        className="min-h-0 overflow-y-auto overscroll-contain"
      >
        {children(activeRef)}
      </div>
      {footer}
    </div>,
    document.body,
  );
}

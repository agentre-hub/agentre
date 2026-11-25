import * as React from "react";
import { ChevronRight } from "lucide-react";
import { ContextMenu as ContextMenuPrimitive } from "radix-ui";

import { cn } from "../lib/utils";
import { TOUCH_MENU_ROW, TouchProvider, useTouch } from "./touch";

type RowRect = Pick<DOMRect, "top" | "bottom">;

/** 行与菜单之间留的缝，菜单不贴死在行上。 */
const ANCHOR_GAP = 4;
/** 菜单离视口边缘至少留的余量。 */
const VIEWPORT_PAD = 8;

/**
 * 触屏长按菜单贴在被长按的行旁：优先放行下方，下方放不下而上方放得下就翻到上方，
 * 两边都放不下仍取下方（菜单自己限高滚动）。`top` 是菜单顶边的视口坐标。
 * 横向不在这里算：锚点取行的右下角，Radix 在右侧放不下时翻到左侧，
 * 正好是菜单右缘与行右缘对齐。
 */
function placeMenuAgainstRow(
  row: RowRect,
  menu: { height: number },
  viewport: { height: number },
): { side: "below" | "above"; top: number } {
  const below = row.bottom + ANCHOR_GAP;
  const fitsBelow = below + menu.height <= viewport.height - VIEWPORT_PAD;
  const above = row.top - ANCHOR_GAP - menu.height;
  if (fitsBelow || above < VIEWPORT_PAD) {
    return { side: "below", top: below };
  }
  return { side: "above", top: above };
}

type AnchorRef = React.RefObject<HTMLElement | null>;

// ContextMenuContent 把 anchorRef 登记在这里，同一个 ContextMenu 下的 Trigger 在
// 开菜单那一刻读它：Radix 的锚点是触发事件的坐标，这里把坐标换成行的右下角。
const AnchorRegistry = React.createContext<React.RefObject<AnchorRef | null>>({
  current: null,
});

function ContextMenu({
  ...props
}: React.ComponentProps<typeof ContextMenuPrimitive.Root>) {
  const registryRef = React.useRef<AnchorRef | null>(null);
  return (
    <AnchorRegistry value={registryRef}>
      <ContextMenuPrimitive.Root data-slot="context-menu" {...props} />
    </AnchorRegistry>
  );
}

function ContextMenuTrigger({
  onContextMenu,
  onPointerDown,
  ...props
}: React.ComponentProps<typeof ContextMenuPrimitive.Trigger>) {
  const registryRef = React.useContext(AnchorRegistry);
  // Radix 在我们的 handler 之后才读 event.clientX/Y（右键立即读，触屏长按 700ms 后读
  // 同一个事件对象），所以在这里改写合成事件的坐标即可改掉锚点。
  const snapToRow = (event: React.MouseEvent | React.PointerEvent) => {
    const rect = registryRef.current?.current?.getBoundingClientRect();
    if (!rect) return;
    event.clientX = rect.right;
    event.clientY = rect.bottom;
  };
  return (
    <ContextMenuPrimitive.Trigger
      data-slot="context-menu-trigger"
      onContextMenu={(event) => {
        onContextMenu?.(event);
        snapToRow(event);
      }}
      onPointerDown={(event) => {
        onPointerDown?.(event);
        snapToRow(event);
      }}
      {...props}
    />
  );
}

function ContextMenuContent({
  className,
  touch,
  anchorRef,
  children,
  ...props
}: React.ComponentProps<typeof ContextMenuPrimitive.Content> & {
  /** 触屏档：菜单行 44px、15px 字。不传则跟随最近的 TouchProvider。 */
  touch?: boolean;
  /**
   * 把菜单锚定到这个元素（通常是被长按的行）：贴在它下方、右缘对齐，下方放不下翻到
   * 上方，行本身完整露出。不传则菜单跟着手指 / 光标落点（桌面行为）。
   */
  anchorRef?: AnchorRef;
}) {
  const inherited = useTouch();
  const registryRef = React.useContext(AnchorRegistry);
  React.useLayoutEffect(() => {
    registryRef.current = anchorRef ?? null;
    return () => {
      registryRef.current = null;
    };
  }, [registryRef, anchorRef]);

  const [placement, setPlacement] = React.useState<{
    side: "below" | "above";
    alignOffset: number;
  } | null>(null);
  const measure = React.useCallback(
    (node: HTMLDivElement | null) => {
      const row = anchorRef?.current?.getBoundingClientRect();
      // 卸载时（node 为 null）不清状态：Radix 会在每次渲染后把 ref 摘下再挂上，
      // 这里一清就是死循环；下次打开挂载时会按新的行位置重新量。
      if (!node || !row) return;
      const placed = placeMenuAgainstRow(row, node.getBoundingClientRect(), {
        height: window.innerHeight,
      });
      // 锚点在行的右下角，对齐方式是顶边对锚点，所以偏移 = 目标顶边 - 行底边。
      const alignOffset = placed.top - row.bottom;
      setPlacement((prev) =>
        prev?.side === placed.side && prev.alignOffset === alignOffset
          ? prev
          : { side: placed.side, alignOffset },
      );
    },
    [anchorRef],
  );

  return (
    <ContextMenuPrimitive.Portal>
      <ContextMenuPrimitive.Content
        ref={anchorRef ? measure : undefined}
        data-slot="context-menu-content"
        data-anchor-side={placement?.side}
        alignOffset={placement?.alignOffset}
        className={cn(
          "z-50 min-w-[10rem] overflow-hidden rounded-md bg-popover p-1 text-popover-foreground shadow-md motion-safe:data-[state=closed]:animate-out motion-safe:data-[state=closed]:fade-out-0 motion-safe:data-[state=closed]:zoom-out-95 motion-safe:data-[state=open]:animate-in motion-safe:data-[state=open]:fade-in-0 motion-safe:data-[state=open]:zoom-in-95",
          className,
        )}
        {...props}
      >
        <TouchProvider touch={touch ?? inherited}>{children}</TouchProvider>
      </ContextMenuPrimitive.Content>
    </ContextMenuPrimitive.Portal>
  );
}

function ContextMenuItem({
  className,
  variant = "default",
  ...props
}: React.ComponentProps<typeof ContextMenuPrimitive.Item> & {
  variant?: "default" | "destructive";
}) {
  const touch = useTouch();
  return (
    <ContextMenuPrimitive.Item
      data-slot="context-menu-item"
      data-variant={variant}
      className={cn(
        "relative flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-hidden select-none hover:bg-accent hover:text-accent-foreground focus:bg-accent focus:text-accent-foreground data-[disabled]:pointer-events-none data-[disabled]:opacity-50 data-[variant=destructive]:text-destructive-text data-[variant=destructive]:hover:bg-destructive/10 data-[variant=destructive]:hover:text-destructive-text data-[variant=destructive]:focus:bg-destructive/10 data-[variant=destructive]:focus:text-destructive-text [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 [&_svg:not([class*='text-'])]:text-muted-foreground",
        touch && TOUCH_MENU_ROW,
        className,
      )}
      {...props}
    />
  );
}

function ContextMenuSeparator({
  className,
  ...props
}: React.ComponentProps<typeof ContextMenuPrimitive.Separator>) {
  return (
    <ContextMenuPrimitive.Separator
      data-slot="context-menu-separator"
      className={cn("-mx-1 my-1 h-px bg-border", className)}
      {...props}
    />
  );
}

function ContextMenuSub({
  ...props
}: React.ComponentProps<typeof ContextMenuPrimitive.Sub>) {
  return <ContextMenuPrimitive.Sub data-slot="context-menu-sub" {...props} />;
}

function ContextMenuSubTrigger({
  className,
  children,
  ...props
}: React.ComponentProps<typeof ContextMenuPrimitive.SubTrigger>) {
  const touch = useTouch();
  return (
    <ContextMenuPrimitive.SubTrigger
      data-slot="context-menu-sub-trigger"
      className={cn(
        "flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-hidden select-none hover:bg-accent hover:text-accent-foreground focus:bg-accent focus:text-accent-foreground data-[state=open]:bg-accent data-[state=open]:text-accent-foreground [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 [&_svg:not([class*='text-'])]:text-muted-foreground",
        touch && TOUCH_MENU_ROW,
        className,
      )}
      {...props}
    >
      {children}
      <ChevronRight className="ml-auto size-4" />
    </ContextMenuPrimitive.SubTrigger>
  );
}

function ContextMenuSubContent({
  className,
  ...props
}: React.ComponentProps<typeof ContextMenuPrimitive.SubContent>) {
  return (
    <ContextMenuPrimitive.SubContent
      data-slot="context-menu-sub-content"
      className={cn(
        "z-50 min-w-[8rem] overflow-hidden rounded-md bg-popover p-1 text-popover-foreground shadow-lg motion-safe:data-[state=closed]:animate-out motion-safe:data-[state=closed]:fade-out-0 motion-safe:data-[state=closed]:zoom-out-95 motion-safe:data-[state=open]:animate-in motion-safe:data-[state=open]:fade-in-0 motion-safe:data-[state=open]:zoom-in-95",
        className,
      )}
      {...props}
    />
  );
}

export {
  placeMenuAgainstRow,
  ContextMenu,
  ContextMenuTrigger,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
};

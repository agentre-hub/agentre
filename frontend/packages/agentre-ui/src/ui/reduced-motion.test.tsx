import { fireEvent, render, screen } from "@testing-library/react";
import * as React from "react";
import { describe, expect, it, vi } from "vitest";

import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
  ContextMenuTrigger,
} from "./context-menu";
import { Dialog, DialogContent, DialogTitle } from "./dialog";
import { DialogShell, DialogShellHeader } from "./dialog-shell";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "./dropdown-menu";
import { HoverCard, HoverCardContent, HoverCardTrigger } from "./hover-card";
import { Popover, PopoverContent, PopoverTrigger } from "./popover";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "./select";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "./tooltip";

/**
 * 弹层的进出场动画只在 `motion-safe` 下出现（docs/design.md §8）。
 *
 * 写成 `motion-reduce:animate-none` 不算数：`data-[state=open]:animate-in` 是
 * 「类 + 属性」两段选择器，特异性 (0,2,0)，压过媒体查询里只有一个类的
 * `motion-reduce:animate-none` (0,1,0)——开了「减少动态效果」照样动。所以这里
 * 要求每条动画工具类本身就挂在 `motion-safe:` 下，而不是检查有没有一条兜底类。
 */
const MOTION_UTILITY =
  /(?:^|:)(?:animate-(?:in|out)|(?:fade|zoom)-(?:in|out)(?:-\S+)?|slide-(?:in|out)-(?:from|to)-\S+)$/;

function motionUtilities(el: Element | null): string[] {
  if (!el) throw new Error("expected the animated surface to be rendered");
  return [...el.classList].filter((c) => MOTION_UTILITY.test(c));
}

const bySlot = (slot: string) => () =>
  document.querySelector(`[data-slot="${slot}"]`);
const surface = () => screen.getByTestId("surface");

const cases: Array<[string, () => React.ReactElement, () => Element | null]> = [
  [
    "Dialog 遮罩",
    () => (
      <Dialog open>
        <DialogContent>
          <DialogTitle>标题</DialogTitle>
        </DialogContent>
      </Dialog>
    ),
    bySlot("dialog-overlay"),
  ],
  [
    "Dialog 内容",
    () => (
      <Dialog open>
        <DialogContent>
          <DialogTitle>标题</DialogTitle>
        </DialogContent>
      </Dialog>
    ),
    bySlot("dialog-content"),
  ],
  [
    "DialogShell 遮罩",
    () => (
      <DialogShell open onOpenChange={vi.fn()}>
        <DialogShellHeader title="标题" />
      </DialogShell>
    ),
    bySlot("dialog-shell-overlay"),
  ],
  [
    "DialogShell 内容",
    () => (
      <DialogShell open onOpenChange={vi.fn()}>
        <DialogShellHeader title="标题" />
      </DialogShell>
    ),
    bySlot("dialog-shell-content"),
  ],
  [
    "Popover",
    () => (
      <Popover open>
        <PopoverTrigger>开</PopoverTrigger>
        <PopoverContent data-testid="surface">内容</PopoverContent>
      </Popover>
    ),
    surface,
  ],
  [
    "HoverCard",
    () => (
      <HoverCard open>
        <HoverCardTrigger>开</HoverCardTrigger>
        <HoverCardContent data-testid="surface">内容</HoverCardContent>
      </HoverCard>
    ),
    surface,
  ],
  [
    "Tooltip",
    () => (
      <TooltipProvider>
        <Tooltip open>
          <TooltipTrigger>开</TooltipTrigger>
          <TooltipContent data-testid="surface">提示</TooltipContent>
        </Tooltip>
      </TooltipProvider>
    ),
    surface,
  ],
  [
    "Select",
    () => (
      <Select open defaultValue="a">
        <SelectTrigger aria-label="引擎">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="a">甲</SelectItem>
        </SelectContent>
      </Select>
    ),
    bySlot("select-content"),
  ],
  [
    "DropdownMenu",
    () => (
      <DropdownMenu open>
        <DropdownMenuTrigger>开</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem>项</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    ),
    bySlot("dropdown-menu-content"),
  ],
  [
    "DropdownMenu 子菜单",
    () => (
      <DropdownMenu open>
        <DropdownMenuTrigger>开</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuSub open>
            <DropdownMenuSubTrigger>更多</DropdownMenuSubTrigger>
            <DropdownMenuSubContent>
              <DropdownMenuItem>子项</DropdownMenuItem>
            </DropdownMenuSubContent>
          </DropdownMenuSub>
        </DropdownMenuContent>
      </DropdownMenu>
    ),
    bySlot("dropdown-menu-sub-content"),
  ],
  [
    "ContextMenu",
    () => (
      <ContextMenu>
        <ContextMenuTrigger>区域</ContextMenuTrigger>
        <ContextMenuContent>
          <ContextMenuItem>项</ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>
    ),
    bySlot("context-menu-content"),
  ],
  [
    "ContextMenu 子菜单",
    () => (
      <ContextMenu>
        <ContextMenuTrigger>区域</ContextMenuTrigger>
        <ContextMenuContent>
          <ContextMenuSub open>
            <ContextMenuSubTrigger>更多</ContextMenuSubTrigger>
            <ContextMenuSubContent>
              <ContextMenuItem>子项</ContextMenuItem>
            </ContextMenuSubContent>
          </ContextMenuSub>
        </ContextMenuContent>
      </ContextMenu>
    ),
    bySlot("context-menu-sub-content"),
  ],
];

describe("弹层进出场动画只在 motion-safe 下出现", () => {
  it.each(cases)("%s", (_name, ui, find) => {
    render(ui());
    // 右键菜单没有受控的 open，只能靠右键打开。
    const contextTrigger = screen.queryByText("区域");
    if (contextTrigger) fireEvent.contextMenu(contextTrigger);

    const utilities = motionUtilities(find());
    // 没开「减少动态效果」时仍然要有进出场——把动画整个删掉不算修好。
    expect(utilities.length).toBeGreaterThan(0);
    for (const cls of utilities) {
      expect(cls).toMatch(/(?:^|:)motion-safe:/);
    }
  });
});

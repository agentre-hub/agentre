import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Alert } from "./alert";
import { Badge } from "./badge";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from "./context-menu";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "./dialog";
import {
  DialogShell,
  DialogShellFooter,
  DialogShellHeader,
} from "./dialog-shell";
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
import { ResizableSidebar } from "./resizable-sidebar";
import {
  Table,
  TableBody,
  TableCell,
  TableFooter,
  TableHead,
  TableHeader,
  TableRow,
} from "./table";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "./tooltip";

// 无边框改版：浮层与容器只靠表面色与阴影，不画边、不画页眉页脚分隔线。
// 断言「任何 border 类（含 sm: / [&_tr]: 等变体前缀）都不存在」。
const borderClasses = (el: Element | null) =>
  Array.from(el?.classList ?? []).filter((c) => /(^|[:\]])border(-|$)/.test(c));

describe("无边框浮层", () => {
  it("Dialog 浮卡、页眉、页脚都没有边", () => {
    render(
      <Dialog open>
        <DialogContent>
          <DialogHeader data-testid="h">
            <DialogTitle>t</DialogTitle>
            <DialogDescription>d</DialogDescription>
          </DialogHeader>
          <DialogFooter data-testid="f">x</DialogFooter>
        </DialogContent>
      </Dialog>,
    );
    expect(borderClasses(screen.getByRole("dialog"))).toEqual([]);
    expect(borderClasses(screen.getByTestId("h"))).toEqual([]);
    expect(borderClasses(screen.getByTestId("f"))).toEqual([]);
  });

  it("DialogShell 浮卡、页眉、页脚都没有边，danger 靠填充表达", () => {
    render(
      <DialogShell open onOpenChange={() => {}} danger>
        <DialogShellHeader title="删除" danger />
        <DialogShellFooter>ok</DialogShellFooter>
      </DialogShell>,
    );
    const q = (s: string) => document.querySelector(`[data-slot="${s}"]`);
    expect(borderClasses(q("dialog-shell-content"))).toEqual([]);
    expect(q("dialog-shell-content")).toHaveAttribute("data-danger", "true");
    expect(borderClasses(q("dialog-shell-header"))).toEqual([]);
    expect(q("dialog-shell-header")).toHaveClass("bg-destructive-soft/40");
    expect(borderClasses(q("dialog-shell-footer"))).toEqual([]);
  });

  it("DropdownMenu 与子菜单没有边", () => {
    render(
      <DropdownMenu open>
        <DropdownMenuTrigger>o</DropdownMenuTrigger>
        <DropdownMenuContent data-testid="c">
          <DropdownMenuItem>a</DropdownMenuItem>
          <DropdownMenuSub open>
            <DropdownMenuSubTrigger>s</DropdownMenuSubTrigger>
            <DropdownMenuSubContent data-testid="sc">
              <DropdownMenuItem>b</DropdownMenuItem>
            </DropdownMenuSubContent>
          </DropdownMenuSub>
        </DropdownMenuContent>
      </DropdownMenu>,
    );
    expect(borderClasses(screen.getByTestId("c"))).toEqual([]);
    expect(borderClasses(screen.getByTestId("sc"))).toEqual([]);
  });

  it("ContextMenu 没有边", () => {
    render(
      <ContextMenu>
        <ContextMenuTrigger>t</ContextMenuTrigger>
        <ContextMenuContent data-testid="c">
          <ContextMenuItem>a</ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>,
    );
    fireEvent.contextMenu(screen.getByText("t"));
    expect(borderClasses(screen.getByTestId("c"))).toEqual([]);
  });

  it("Popover / HoverCard / Tooltip 没有边", () => {
    render(
      <TooltipProvider>
        <Popover open>
          <PopoverTrigger>p</PopoverTrigger>
          <PopoverContent data-testid="p">x</PopoverContent>
        </Popover>
        <HoverCard open>
          <HoverCardTrigger>h</HoverCardTrigger>
          <HoverCardContent data-testid="hc">x</HoverCardContent>
        </HoverCard>
        <Tooltip open>
          <TooltipTrigger>t</TooltipTrigger>
          <TooltipContent data-testid="tt">x</TooltipContent>
        </Tooltip>
      </TooltipProvider>,
    );
    for (const id of ["p", "hc", "tt"]) {
      expect(borderClasses(screen.getByTestId(id)), id).toEqual([]);
    }
  });
});

describe("无边框容器", () => {
  it("Table 的表头、行、表尾都没有线", () => {
    const { container } = render(
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>h</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow>
            <TableCell>c</TableCell>
          </TableRow>
        </TableBody>
        <TableFooter>
          <TableRow>
            <TableCell>f</TableCell>
          </TableRow>
        </TableFooter>
      </Table>,
    );
    for (const el of container.querySelectorAll(
      "thead, tbody, tfoot, tr, th, td",
    )) {
      expect(borderClasses(el), el.tagName).toEqual([]);
    }
  });

  it("Alert 与 Badge 各档都没有边", () => {
    render(
      <>
        <Alert variant="default">a</Alert>
        <Alert variant="destructive">b</Alert>
        <Alert variant="warning">c</Alert>
        <Badge variant="default">1</Badge>
        <Badge variant="outline">2</Badge>
        <Badge variant="destructive">3</Badge>
      </>,
    );
    for (const el of screen.getAllByRole("alert")) {
      expect(borderClasses(el)).toEqual([]);
    }
    for (const el of document.querySelectorAll('[data-slot="badge"]')) {
      expect(borderClasses(el)).toEqual([]);
    }
  });
});

describe("ResizableSidebar surface", () => {
  const renderSide = (
    props: Partial<React.ComponentProps<typeof ResizableSidebar>> = {},
  ) => {
    render(
      <ResizableSidebar persistenceKey="k" ariaLabel="S" {...props}>
        x
      </ResizableSidebar>,
    );
    return screen.getByRole("complementary", { name: "S" });
  };

  it("surface=chrome 用 bg-chrome，sheet 用 bg-sheet", () => {
    expect(renderSide({ surface: "chrome" })).toHaveClass("bg-chrome");
    document.body.innerHTML = "";
    const sheet = renderSide({ surface: "sheet" });
    expect(sheet).toHaveClass("bg-sheet");
    expect(sheet).not.toHaveClass("bg-chrome");
  });

  it("默认与任一 surface 都不画边", () => {
    expect(borderClasses(renderSide())).toEqual([]);
    document.body.innerHTML = "";
    expect(
      borderClasses(renderSide({ edge: "left", surface: "sheet" })),
    ).toEqual([]);
  });
});

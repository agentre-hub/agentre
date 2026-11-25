import { fireEvent, render, screen } from "@testing-library/react";
import * as React from "react";
import { afterEach, describe, expect, it } from "vitest";

import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
  placeMenuAgainstRow,
} from "./context-menu";

function rect(x: number, y: number, w: number, h: number) {
  return { left: x, top: y, right: x + w, bottom: y + h, width: w, height: h };
}

function openMenu(props: { touch?: boolean } = {}) {
  render(
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <div data-testid="row">会话</div>
      </ContextMenuTrigger>
      <ContextMenuContent {...props}>
        <ContextMenuItem>重命名</ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>,
  );
  fireEvent.contextMenu(screen.getByTestId("row"), { clientX: 5, clientY: 5 });
  return screen.getByRole("menuitem", { name: "重命名" });
}

describe("ContextMenu 触屏档", () => {
  it("touch：菜单行 44px 高、15px 字", () => {
    const item = openMenu({ touch: true });
    expect(item).toHaveClass("min-h-11");
    expect(item).toHaveClass("text-prose");
  });

  it("不传 touch：桌面行不变", () => {
    const item = openMenu();
    expect(item).not.toHaveClass("min-h-11");
    expect(item).not.toHaveClass("text-prose");
    expect(item).toHaveClass("text-sm");
  });
});

describe("placeMenuAgainstRow：贴行下方，放不下翻上方", () => {
  const viewport = { height: 844 };
  const menu = { height: 176 };

  it("下方放得下：菜单顶在行底下", () => {
    const p = placeMenuAgainstRow(rect(0, 100, 390, 60), menu, viewport);
    expect(p).toEqual({ side: "below", top: 164 });
  });

  it("下方放不下：翻到行上方，菜单底在行顶上，被长按的行不被盖住", () => {
    const p = placeMenuAgainstRow(rect(0, 740, 390, 60), menu, viewport);
    expect(p.side).toBe("above");
    expect(p.top + menu.height).toBeLessThanOrEqual(740);
  });

  it("上下都放不下：仍选下方（菜单自己限高滚动）", () => {
    const p = placeMenuAgainstRow(rect(0, 100, 390, 60), menu, { height: 200 });
    expect(p.side).toBe("below");
  });
});

describe("ContextMenuContent anchorRef", () => {
  function mockRects(row: HTMLElement, rowRect: ReturnType<typeof rect>) {
    const original = HTMLElement.prototype.getBoundingClientRect;
    HTMLElement.prototype.getBoundingClientRect = function () {
      if (this === row) return rowRect as DOMRect;
      if (this.getAttribute("data-slot") === "context-menu-content") {
        return rect(0, 0, 200, 176) as DOMRect;
      }
      return original.call(this);
    };
    return () => {
      HTMLElement.prototype.getBoundingClientRect = original;
    };
  }

  function Harness({ rowRect }: { rowRect: ReturnType<typeof rect> }) {
    const ref = React.useRef<HTMLDivElement>(null);
    React.useLayoutEffect(() => {
      if (ref.current) restore = mockRects(ref.current, rowRect);
    });
    return (
      <ContextMenu>
        <ContextMenuTrigger asChild>
          <div data-testid="row" ref={ref}>
            会话
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent anchorRef={ref}>
          <ContextMenuItem>重命名</ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>
    );
  }
  let restore = () => {};
  afterEach(() => restore());

  it("行下方放得下：菜单标记在下方", () => {
    Object.defineProperty(window, "innerHeight", {
      value: 844,
      configurable: true,
    });
    render(<Harness rowRect={rect(0, 100, 390, 60)} />);
    fireEvent.contextMenu(screen.getByTestId("row"), {
      clientX: 5,
      clientY: 5,
    });
    const content = document.querySelector(
      '[data-slot="context-menu-content"]',
    );
    expect(content).toHaveAttribute("data-anchor-side", "below");
  });

  it("行贴近视口底：菜单翻到行上方", () => {
    Object.defineProperty(window, "innerHeight", {
      value: 844,
      configurable: true,
    });
    render(<Harness rowRect={rect(0, 740, 390, 60)} />);
    fireEvent.contextMenu(screen.getByTestId("row"), {
      clientX: 5,
      clientY: 5,
    });
    const content = document.querySelector(
      '[data-slot="context-menu-content"]',
    );
    expect(content).toHaveAttribute("data-anchor-side", "above");
  });

  it("不传 anchorRef：没有锚定标记，桌面行为不变", () => {
    openMenu();
    const content = document.querySelector(
      '[data-slot="context-menu-content"]',
    );
    expect(content).not.toHaveAttribute("data-anchor-side");
  });
});

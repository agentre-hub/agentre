import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { MentionPopover } from "../mention-popover";
import type { MentionItem, MentionMenuState } from "../types";

const items: MentionItem[] = [
  { kind: "agent", refId: 12, label: "Reviewer", color: "agent-3" },
  { kind: "project", refId: 3, label: "Web", path: "/w", color: "agent-5" },
];

const state: MentionMenuState = {
  open: true,
  anchorRect: { left: 10, top: 40, bottom: 60 },
  items,
  selectedIndex: 0,
  query: "",
};

// 40 个 agent —— 远超弹层能显示的行数,用来逼出高度上限 + 滚动。
function manyAgents(n: number): MentionItem[] {
  return Array.from({ length: n }, (_, i) => ({
    kind: "agent" as const,
    refId: i + 1,
    label: `Agent ${i + 1}`,
  }));
}

// 光标离视口顶部足够远(768 - 600),可用空间不会成为约束。
const roomy: MentionMenuState = {
  ...state,
  anchorRect: { left: 10, top: 600, bottom: 620 },
  items: manyAgents(40),
};

describe("MentionPopover", () => {
  it("renders grouped agent + project items", () => {
    render(<MentionPopover state={state} onPick={vi.fn()} onHover={vi.fn()} />);
    expect(screen.getByRole("listbox")).toBeInTheDocument();
    expect(screen.getByText("Reviewer")).toBeInTheDocument();
    expect(screen.getByText("Web")).toBeInTheDocument();
    // section headers present
    expect(screen.getByText("Agents")).toBeInTheDocument();
    expect(screen.getByText("Projects")).toBeInTheDocument();
  });

  it("Given a nested project, When rendered, Then hierarchy is indented and the project name keeps priority over its path", () => {
    const nestedProject: MentionItem = {
      kind: "project",
      refId: 4,
      label: "Desktop",
      path: "/platform/desktop",
      depth: 2,
    };
    render(
      <MentionPopover
        state={{ ...state, items: [nestedProject] }}
        onPick={vi.fn()}
        onHover={vi.fn()}
      />,
    );

    const label = screen.getByText("Desktop");
    expect(label).toHaveClass("min-w-0", "flex-1");
    expect(label.closest("button")).toHaveStyle({ paddingLeft: "32px" });
    expect(screen.getByText("/platform/desktop")).toHaveClass(
      "max-w-[40%]",
      "shrink-0",
    );
  });

  it("calls onPick on mousedown", () => {
    const onPick = vi.fn();
    render(<MentionPopover state={state} onPick={onPick} onHover={vi.fn()} />);
    screen
      .getByText("Reviewer")
      .closest("button")!
      .dispatchEvent(
        new MouseEvent("mousedown", { bubbles: true, cancelable: true }),
      );
    expect(onPick).toHaveBeenCalledWith(items[0]);
  });

  it("caps the list height so a long list scrolls instead of growing off-screen", () => {
    render(<MentionPopover state={roomy} onPick={vi.fn()} onHover={vi.fn()} />);
    const box = screen.getByRole("listbox");
    expect(box.style.maxHeight).toBe("288px");
    expect(box).toHaveClass("overflow-y-auto");
  });

  it("shrinks the list to the space above the cursor", () => {
    // 光标顶边 150 → 减掉 4px 间距 + 8px 视口留白 = 138。
    render(
      <MentionPopover
        state={{ ...roomy, anchorRect: { left: 10, top: 150, bottom: 170 } }}
        onPick={vi.fn()}
        onHover={vi.fn()}
      />,
    );
    expect(screen.getByRole("listbox").style.maxHeight).toBe("138px");
  });

  it("keeps ~3 rows even when the cursor is jammed against the viewport top", () => {
    render(
      <MentionPopover
        state={{ ...roomy, anchorRect: { left: 10, top: 40, bottom: 60 } }}
        onPick={vi.fn()}
        onHover={vi.fn()}
      />,
    );
    expect(screen.getByRole("listbox").style.maxHeight).toBe("96px");
  });

  it("scrolls the keyboard-selected option into view", () => {
    const { rerender } = render(
      <MentionPopover
        state={{ ...roomy, selectedIndex: 0 }}
        onPick={vi.fn()}
        onHover={vi.fn()}
      />,
    );
    // 列表未虚拟化 —— 第 21 项此刻已在 DOM 里,且 rerender 会复用同一节点。
    const target = screen.getByText("Agent 21").closest("button")!;
    const scrollIntoView = vi.fn();
    target.scrollIntoView = scrollIntoView;

    rerender(
      <MentionPopover
        state={{ ...roomy, selectedIndex: 20 }}
        onPick={vi.fn()}
        onHover={vi.fn()}
      />,
    );

    expect(scrollIntoView).toHaveBeenCalledWith({ block: "nearest" });
  });

  it("takes hover selection from pointer movement, not from the list scrolling under a still cursor", () => {
    const onHover = vi.fn();
    render(<MentionPopover state={state} onPick={vi.fn()} onHover={onHover} />);
    const option = screen.getByText("Reviewer").closest("button")!;

    // 键盘翻页时列表在静止鼠标下滚动,只会触发 mouseenter —— 不该抢走选中。
    fireEvent.mouseEnter(option);
    expect(onHover).not.toHaveBeenCalled();

    fireEvent.mouseMove(option);
    expect(onHover).toHaveBeenCalledWith(0);
  });

  it("renders nothing when closed", () => {
    const { container } = render(
      <MentionPopover
        state={{ ...state, open: false }}
        onPick={vi.fn()}
        onHover={vi.fn()}
      />,
    );
    expect(container.firstChild).toBeNull();
  });
});

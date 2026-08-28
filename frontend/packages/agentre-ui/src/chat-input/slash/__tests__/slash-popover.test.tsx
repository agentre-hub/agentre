import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { SlashCommand } from "../types";
import { SlashPopover } from "../slash-popover";
import type { SlashMenuState } from "../use-slash-menu";

function commands(count: number): SlashCommand[] {
  return Array.from({ length: count }, (_, index) => ({
    name: `skill-${index + 1}`,
    label: `$skill-${index + 1}`,
    trigger: "$" as const,
    resolve: () => ({
      kind: "literal_text" as const,
      text: `$skill-${index + 1}`,
    }),
  }));
}

const roomyState: SlashMenuState = {
  open: true,
  anchorRect: { left: 10, top: 600, bottom: 620 },
  items: commands(40),
  selectedIndex: 0,
  query: "",
  trigger: "$",
};

describe("SlashPopover", () => {
  it("Given many skills, When the menu opens, Then it is height-capped and scrollable", () => {
    render(
      <SlashPopover state={roomyState} onPick={vi.fn()} onHover={vi.fn()} />,
    );

    const listbox = screen.getByRole("listbox");
    expect(listbox.style.maxHeight).toBe("288px");
    expect(listbox).toHaveClass("overflow-y-auto", "overscroll-contain");
  });

  it("Given little room above the cursor, When the menu opens, Then it fits the available viewport height", () => {
    render(
      <SlashPopover
        state={{
          ...roomyState,
          anchorRect: { left: 10, top: 150, bottom: 170 },
        }}
        onPick={vi.fn()}
        onHover={vi.fn()}
      />,
    );

    expect(screen.getByRole("listbox").style.maxHeight).toBe("138px");
  });

  it("Given keyboard navigation in a long list, When selection changes, Then the active skill scrolls into view", () => {
    const { rerender } = render(
      <SlashPopover state={roomyState} onPick={vi.fn()} onHover={vi.fn()} />,
    );
    const target = screen.getByText("$skill-21").closest("button")!;
    const scrollIntoView = vi.fn();
    target.scrollIntoView = scrollIntoView;

    rerender(
      <SlashPopover
        state={{ ...roomyState, selectedIndex: 20 }}
        onPick={vi.fn()}
        onHover={vi.fn()}
      />,
    );

    expect(scrollIntoView).toHaveBeenCalledWith({ block: "nearest" });
  });
});

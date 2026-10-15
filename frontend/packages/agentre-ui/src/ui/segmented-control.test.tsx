import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { SegmentedControl, SegmentedControlItem } from "../index";

function Harness({
  onValueChange,
  disabledB = false,
}: {
  onValueChange?: (v: string) => void;
  disabledB?: boolean;
}) {
  const [value, setValue] = useState("a");
  return (
    <SegmentedControl
      aria-label="视图"
      value={value}
      onValueChange={(v) => {
        setValue(v);
        onValueChange?.(v);
      }}
    >
      <SegmentedControlItem value="a" aria-controls="panel-a">
        A
      </SegmentedControlItem>
      <SegmentedControlItem value="b" disabled={disabledB}>
        B
      </SegmentedControlItem>
      <SegmentedControlItem value="c">C</SegmentedControlItem>
    </SegmentedControl>
  );
}

describe("SegmentedControl", () => {
  it("exposes tablist/tab semantics with aria-selected and roving tabindex", () => {
    render(<Harness />);

    expect(screen.getByRole("tablist", { name: "视图" })).toBeInTheDocument();
    const [a, b, c] = screen.getAllByRole("tab");
    expect(a).toHaveAttribute("aria-selected", "true");
    expect(b).toHaveAttribute("aria-selected", "false");
    expect(a).toHaveAttribute("tabindex", "0");
    expect(b).toHaveAttribute("tabindex", "-1");
    expect(c).toHaveAttribute("tabindex", "-1");
    expect(a).toHaveAttribute("aria-controls", "panel-a");
  });

  it("selects an item on click and reports it", async () => {
    const user = userEvent.setup();
    const onValueChange = vi.fn();
    render(<Harness onValueChange={onValueChange} />);

    await user.click(screen.getByRole("tab", { name: "C" }));

    expect(onValueChange).toHaveBeenCalledWith("c");
    expect(screen.getByRole("tab", { name: "C" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });

  it("moves and selects with arrow keys, wrapping at the ends, plus Home/End", async () => {
    const user = userEvent.setup();
    render(<Harness />);

    screen.getByRole("tab", { name: "A" }).focus();
    await user.keyboard("{ArrowRight}");
    expect(screen.getByRole("tab", { name: "B" })).toHaveFocus();
    expect(screen.getByRole("tab", { name: "B" })).toHaveAttribute(
      "aria-selected",
      "true",
    );

    await user.keyboard("{End}");
    expect(screen.getByRole("tab", { name: "C" })).toHaveFocus();
    await user.keyboard("{ArrowRight}");
    expect(screen.getByRole("tab", { name: "A" })).toHaveFocus();
    await user.keyboard("{ArrowLeft}");
    expect(screen.getByRole("tab", { name: "C" })).toHaveFocus();
    await user.keyboard("{Home}");
    expect(screen.getByRole("tab", { name: "A" })).toHaveFocus();
    expect(screen.getByRole("tab", { name: "A" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });

  it("skips disabled items for keyboard and click", async () => {
    const user = userEvent.setup();
    const onValueChange = vi.fn();
    render(<Harness disabledB onValueChange={onValueChange} />);

    screen.getByRole("tab", { name: "A" }).focus();
    await user.keyboard("{ArrowRight}");
    expect(screen.getByRole("tab", { name: "C" })).toHaveFocus();
    expect(onValueChange).toHaveBeenLastCalledWith("c");

    await user.click(screen.getByRole("tab", { name: "B" }));
    expect(onValueChange).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("tab", { name: "B" })).toBeDisabled();
  });

  it("is borderless: field-fill track, raised selected segment, shared focus ring", () => {
    render(<Harness />);

    const list = screen.getByRole("tablist");
    expect(list).toHaveClass("bg-field-fill");
    expect(list.className).not.toMatch(/\bborder(-|\b)/);
    const [a] = screen.getAllByRole("tab");
    expect(a.className).toContain("focus-visible:focus-outline");
    expect(a.className).toContain("data-[state=active]:bg-sheet");
    expect(a.className).not.toMatch(/\bborder(-|\b)/);
  });
});

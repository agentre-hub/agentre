import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { SessionFilterChips } from "./session-filter-chips";

describe("SessionFilterChips", () => {
  it("Given the controlled all value, When Running is pressed, Then it reports running and exposes the three-chip group semantics", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <SessionFilterChips value="all" unreadCount={3} onChange={onChange} />,
    );

    const group = screen.getByRole("group", { name: "Filter sessions" });
    const all = screen.getByTestId("filter-chip-all");
    const running = screen.getByTestId("filter-chip-running");
    const unread = screen.getByTestId("filter-chip-unread");

    expect(group).toContainElement(all);
    expect(group).toContainElement(running);
    expect(group).toContainElement(unread);
    expect(all).toHaveAttribute("aria-pressed", "true");
    expect(running).toHaveAttribute("aria-pressed", "false");
    expect(unread).toHaveAttribute("aria-pressed", "false");
    expect(unread).toHaveTextContent(/Unread\s*3/);

    await user.click(running);
    await user.click(all);
    expect(onChange.mock.calls).toEqual([["running"], ["all"]]);
  });

  it.each([
    ["running", "filter-chip-running"],
    ["unread", "filter-chip-unread"],
  ] as const)(
    "Given %s is controlled as pressed, When it is pressed again, Then it reports all",
    async (value, testId) => {
      const user = userEvent.setup();
      const onChange = vi.fn();
      render(
        <SessionFilterChips
          value={value}
          unreadCount={0}
          onChange={onChange}
        />,
      );

      const active = screen.getByTestId(testId);
      expect(active).toHaveAttribute("aria-pressed", "true");

      await user.click(active);

      expect(onChange).toHaveBeenCalledWith("all");
    },
  );

  it("Given zero unread sessions, When the chips render, Then Unread stays available without a numeric badge", () => {
    render(
      <SessionFilterChips value="all" unreadCount={0} onChange={vi.fn()} />,
    );

    const unread = screen.getByTestId("filter-chip-unread");
    expect(unread).toHaveTextContent("Unread");
    expect(unread).not.toHaveTextContent(/\d/);
    expect(unread.querySelector("[data-slot='unread-count']")).toBeNull();
  });
});

describe("SessionFilterChips item list and touch size", () => {
  it("Given items with counts, Then only counts above zero show and the attention chip has its label", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <SessionFilterChips
        value="all"
        onChange={onChange}
        items={[
          { value: "all" },
          { value: "attention", count: 2 },
          { value: "running", count: 0 },
        ]}
      />,
    );
    expect(screen.getByTestId("filter-chip-attention")).toHaveTextContent(
      /Needs you\s*2/,
    );
    expect(screen.getByTestId("filter-chip-running")).toHaveTextContent(
      /^Running$/,
    );
    expect(screen.queryByTestId("filter-chip-unread")).toBeNull();
    await user.click(screen.getByTestId("filter-chip-attention"));
    expect(onChange).toHaveBeenCalledWith("attention");
  });

  it("Given the touch size, Then every chip has a 44px hit area; default stays compact", () => {
    const { rerender } = render(
      <SessionFilterChips
        value="all"
        unreadCount={0}
        onChange={() => {}}
        size="touch"
      />,
    );
    expect(screen.getByTestId("filter-chip-all")).toHaveClass("min-h-11");
    rerender(
      <SessionFilterChips value="all" unreadCount={0} onChange={() => {}} />,
    );
    expect(screen.getByTestId("filter-chip-all")).not.toHaveClass("min-h-11");
  });
});

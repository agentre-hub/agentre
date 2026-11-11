import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { MemoryBudgetMeter } from "./memory-budget-meter";
import { MemoryEntryGroups } from "./memory-entry-groups";
import type { MemoryEntryGroupsProps, MemoryEntryView } from "./types";

function entry(id: number, summary: string): MemoryEntryView {
  return {
    id,
    summary,
    detail: "",
    pinned: false,
    archived: false,
    hasPrevious: false,
    creator: { kind: "user" },
    createdAt: 0,
    updatedAt: 0,
  };
}

function renderGroups(overrides: Partial<MemoryEntryGroupsProps> = {}) {
  const props: MemoryEntryGroupsProps = {
    normalLabel: "Own",
    normal: [entry(1, "pinned first"), entry(2, "second")],
    overBudget: [entry(3, "too much")],
    archived: [{ ...entry(4, "old fact"), archived: true }],
    editingId: null,
    onEditingChange: vi.fn(),
    onSave: vi.fn(async () => ({ ok: true as const })),
    onAction: vi.fn(async () => ({ ok: true as const })),
    moveTargetsFor: () => [],
    ...overrides,
  };
  const view = render(<MemoryEntryGroups {...props} />);
  return { ...view, props };
}

describe("MemoryEntryGroups", () => {
  it("Given normal, over-budget and archived entries, Then each is its own group with a count, in that order", () => {
    renderGroups();
    const headings = screen
      .getAllByRole("heading", { level: 3 })
      .map((h) => h.textContent);
    expect(headings).toEqual([
      "Own2",
      "Over budget, not carried this time1",
      "Archived1",
    ]);
    const normal = screen.getByRole("region", { name: "Own" });
    expect(
      within(normal)
        .getAllByTestId(/^memory-row-/)
        .map((row) => row.dataset.testid),
    ).toEqual(["memory-row-1", "memory-row-2"]);
  });

  it("Then over-budget rows are dimmed and normal rows are not", () => {
    renderGroups();
    expect(screen.getByTestId("memory-row-3")).toHaveAttribute(
      "data-dimmed",
      "true",
    );
    expect(screen.getByTestId("memory-row-1")).not.toHaveAttribute(
      "data-dimmed",
    );
  });

  it("Given archived entries, Then the group starts collapsed and opens on click", async () => {
    renderGroups();
    expect(screen.queryByTestId("memory-row-4")).toBeNull();
    const toggle = screen.getByRole("button", { name: /Archived/ });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    await userEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByTestId("memory-row-4")).toHaveTextContent("old fact");
  });

  it("Given empty over-budget and archived lists, Then those groups are not rendered", () => {
    renderGroups({ overBudget: [], archived: [] });
    expect(screen.getAllByRole("heading", { level: 3 })).toHaveLength(1);
  });

  it("Given inherited layers, Then they come last with per-layer counts and open that layer on click", async () => {
    const onOpen = vi.fn();
    renderGroups({
      inherited: [
        { key: "global", label: "Global", count: 3, onOpen },
        { key: "department:1", label: "Backend", count: 5, onOpen: vi.fn() },
      ],
    });
    const headings = screen.getAllByRole("heading", { level: 3 });
    expect(headings.at(-1)).toHaveTextContent("Inherited");
    await userEvent.click(
      screen.getByRole("button", { name: "Open Global, 3 entries" }),
    );
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it("Given an editing id, Then only that row shows the edit form", () => {
    renderGroups({ editingId: 2 });
    const summaries = screen.getAllByRole("textbox", { name: "Summary" });
    expect(summaries).toHaveLength(1);
    expect(summaries[0]).toHaveValue("second");
  });
});

describe("MemoryBudgetMeter", () => {
  it("Given usage under budget, Then it reads used / budget and is not flagged", () => {
    render(<MemoryBudgetMeter used={1610} budget={2000} />);
    const meter = screen.getByRole("meter");
    expect(meter).toHaveAccessibleName("Budget usage");
    expect(meter).toHaveAttribute("aria-valuenow", "1610");
    expect(meter).toHaveAttribute("aria-valuemax", "2000");
    expect(meter).not.toHaveAttribute("data-over");
    expect(screen.getByText("1,610 / 2,000 chars")).toBeInTheDocument();
  });

  it("Given usage over budget, Then it is flagged with the attention color", () => {
    render(<MemoryBudgetMeter used={2240} budget={2000} />);
    const meter = screen.getByRole("meter");
    expect(meter).toHaveAttribute("data-over", "true");
    expect(meter.querySelector("[data-meter-fill]")?.className).toContain(
      "bg-status-waiting",
    );
    expect(screen.getByText("2,240 / 2,000 chars")).toBeInTheDocument();
  });
});

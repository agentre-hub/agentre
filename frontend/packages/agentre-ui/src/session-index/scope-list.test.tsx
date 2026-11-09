import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import {
  ScopeList,
  type ScopeListProps,
  type ScopeProjectNode,
} from "./scope-list";

const projects: ScopeProjectNode[] = [
  {
    id: "p1",
    name: "agentre",
    count: 5,
    needsYou: true,
    children: [{ id: "p1a", name: "frontend", count: 2 }],
  },
  { id: "p2", name: "docs", count: 1 },
];

const base: ScopeListProps = {
  allCount: 9,
  selected: { kind: "all" },
  onSelect: () => {},
  projects,
  agentGroups: [
    {
      id: "d1",
      name: "研发部",
      agents: [
        {
          id: "a1",
          name: "Coder",
          online: true,
          machineName: "本机",
          count: 3,
          needsYou: true,
        },
        {
          id: "a2",
          name: "Reviewer",
          online: false,
          machineName: "构建机",
          count: 0,
        },
      ],
    },
  ],
  machines: [
    { id: "m1", name: "本机", online: true, count: 6 },
    { id: "m2", name: "构建机", online: false, count: 3 },
  ],
};

function setup(props: Partial<ScopeListProps> = {}) {
  const onSelect = vi.fn();
  render(<ScopeList {...base} onSelect={onSelect} {...props} />);
  return { onSelect, user: userEvent.setup() };
}

describe("ScopeList", () => {
  it("pins All conversations with its count and reports the all scope", async () => {
    const { onSelect, user } = setup({
      selected: { kind: "project", id: "p2" },
    });
    const all = screen.getByTestId("scope-all");
    expect(all).toHaveTextContent("All conversations");
    expect(all).toHaveTextContent("9");
    await user.click(all);
    expect(onSelect).toHaveBeenCalledWith({ kind: "all" });
  });

  it("Given counts the host cannot tell, When rendered, Then rows show no number rather than a made-up one", () => {
    setup({
      allCount: undefined,
      projects: [{ id: "p2", name: "docs" }],
      machines: [{ id: "m1", name: "本机", online: true }],
    });
    expect(screen.getByTestId("scope-all").textContent).toBe(
      "All conversations",
    );
    expect(screen.getByTestId("scope-item-project-p2").textContent).not.toMatch(
      /\d/,
    );
  });

  it("offers three tabs, starting on projects", () => {
    setup();
    const tabs = screen.getAllByRole("tab");
    expect(tabs.map((tab) => tab.textContent)).toEqual([
      "Project",
      "Agent",
      "Machine",
    ]);
    expect(screen.getByRole("tab", { name: "Project" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByText("agentre")).toBeInTheDocument();
  });

  it("renders projects as a collapsible tree and selects a leaf", async () => {
    const { onSelect, user } = setup();
    expect(screen.getByText("frontend")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Toggle agentre" }));
    expect(screen.queryByText("frontend")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Toggle agentre" }));
    await user.click(screen.getByTestId("scope-item-project-p1a"));
    expect(onSelect).toHaveBeenCalledWith({ kind: "project", id: "p1a" });
  });

  it("shows counts, and an amber dot only on items that need you", () => {
    setup();
    const needs = screen.getByTestId("scope-item-project-p1");
    expect(needs).toHaveTextContent("5");
    const dot = within(needs).getByTestId("scope-needs-you");
    expect(dot.className).toContain("bg-status-waiting");
    expect(
      within(screen.getByTestId("scope-item-project-p2")).queryByTestId(
        "scope-needs-you",
      ),
    ).toBeNull();
  });

  it("marks the selected item with a check and the primary-soft fill", () => {
    setup({ selected: { kind: "project", id: "p2" } });
    const row = screen.getByTestId("scope-item-project-p2");
    expect(row).toHaveAttribute("aria-current", "true");
    expect(row.className).toContain("bg-primary-soft");
    expect(within(row).getByTestId("scope-check")).toBeInTheDocument();
    expect(
      within(screen.getByTestId("scope-item-project-p1")).queryByTestId(
        "scope-check",
      ),
    ).toBeNull();
  });

  it("gives every row a 44px hit area", () => {
    setup();
    for (const id of ["scope-all", "scope-item-project-p1"]) {
      expect(screen.getByTestId(id).className).toContain("min-h-11");
    }
  });

  it("groups agents by department with online dot and machine", async () => {
    const { onSelect, user } = setup();
    await user.click(screen.getByRole("tab", { name: "Agent" }));
    expect(screen.getByText("研发部")).toBeInTheDocument();
    const coder = screen.getByTestId("scope-item-agent-a1");
    expect(coder).toHaveTextContent("本机");
    expect(coder).toHaveTextContent("3");
    expect(within(coder).getByTestId("scope-online")).toHaveAttribute(
      "data-online",
      "true",
    );
    expect(
      within(screen.getByTestId("scope-item-agent-a2")).getByTestId(
        "scope-online",
      ),
    ).toHaveAttribute("data-online", "false");
    await user.click(coder);
    expect(onSelect).toHaveBeenCalledWith({ kind: "agent", id: "a1" });
  });

  it("splits machines into online and offline sections, offline dimmed but selectable", async () => {
    const { onSelect, user } = setup();
    await user.click(screen.getByRole("tab", { name: "Machine" }));
    expect(screen.getByText("Online")).toBeInTheDocument();
    expect(screen.getByText("Offline")).toBeInTheDocument();
    const off = screen.getByTestId("scope-item-machine-m2");
    expect(off.className).toContain("opacity-50");
    expect(screen.getByTestId("scope-item-machine-m1").className).not.toContain(
      "opacity-50",
    );
    await user.click(off);
    expect(onSelect).toHaveBeenCalledWith({ kind: "machine", id: "m2" });
  });

  it("searches the active tab with a 16px input and highlights the match", async () => {
    const { user } = setup();
    const input = screen.getByRole("searchbox");
    expect(input.className).toContain("text-base");
    await user.type(input, "front");
    expect(screen.queryByText("docs")).not.toBeInTheDocument();
    // 命中项的祖先保留，让树读得通
    expect(screen.getByTestId("scope-item-project-p1")).toBeInTheDocument();
    expect(
      screen.getByTestId("scope-item-project-p1a").querySelector("mark"),
    ).toHaveTextContent("front");
    await user.clear(input);
    await user.type(input, "zzz");
    expect(screen.getByText("No matches")).toBeInTheDocument();
  });

  it("Given a search keeps a parent forced open, When its chevron is tapped, Then nothing hidden flips and the tree is still open after clearing", async () => {
    const { user } = setup();
    const input = screen.getByRole("searchbox");
    await user.type(input, "front");
    // 搜索中树一律展开：箭头点了看不出变化，就不该在背后把它折起来。
    await user.click(screen.getByRole("button", { name: "Toggle agentre" }));
    expect(screen.getByText("front")).toBeInTheDocument();
    await user.clear(input);
    expect(screen.getByTestId("scope-item-project-p1a")).toBeInTheDocument();
  });
});

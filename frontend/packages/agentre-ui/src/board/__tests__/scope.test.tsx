import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, describe, expect, it, vi } from "vitest";

import { ProjectScopePicker } from "../project-scope-picker";
import { buildScopeRows, filterScopeRows } from "../scope-tree";
import type { ProjectScope, ScopeProjectNode } from "../query-types";

/** 一棵 alpha ▸ beta ▸ gamma + 独立 delta 的扁平前序树（与 ProjectFlat 同形）。 */
const PROJECTS: ScopeProjectNode[] = [
  { id: 1, name: "alpha", depth: 0, unfinished: 7 },
  { id: 2, name: "beta", depth: 1, unfinished: 4 },
  { id: 3, name: "gamma", depth: 2, unfinished: 1 },
  { id: 4, name: "delta", depth: 0, unfinished: 0 },
];

beforeAll(() => {
  // happy-dom 没有 scrollIntoView；呈现件本身也不该赌它存在。
  Element.prototype.scrollIntoView ??= vi.fn();
});

function renderPicker(
  scope: ProjectScope,
  over: Partial<React.ComponentProps<typeof ProjectScopePicker>> = {},
) {
  const onScopeChange = vi.fn();
  render(
    <ProjectScopePicker
      scope={scope}
      projects={PROJECTS}
      unassignedCount={2}
      onScopeChange={onScopeChange}
      {...over}
    />,
  );
  return { onScopeChange };
}

describe("项目范围的树推导", () => {
  it("Given a flat pre-order list, When rows are built, Then each row knows its ancestor path and how many projects hang under it", () => {
    const rows = buildScopeRows(PROJECTS);

    expect(rows.map((row) => row.path)).toEqual([
      [],
      ["alpha"],
      ["alpha", "beta"],
      [],
    ]);
    expect(rows.map((row) => row.descendantCount)).toEqual([2, 1, 0, 0]);
  });

  it("Given a needle matching a deep project, When rows are filtered, Then the hit keeps its ancestors and they stay selectable but dimmed", () => {
    const rows = filterScopeRows(buildScopeRows(PROJECTS), "gam");

    expect(rows.map((row) => row.node.name)).toEqual([
      "alpha",
      "beta",
      "gamma",
    ]);
    expect(rows.map((row) => row.ancestorOnly)).toEqual([true, true, false]);
  });
});

describe("范围触发器", () => {
  it("Given each scope kind, When the trigger renders, Then it names 全部项目 / 未归属 / the project", () => {
    const labels = (["all", "unassigned"] as const).map((kind) => {
      const view = render(
        <ProjectScopePicker
          scope={{ kind }}
          projects={PROJECTS}
          unassignedCount={2}
          onScopeChange={vi.fn()}
        />,
      );
      const text = screen.getByTestId("scope-trigger").textContent ?? "";
      view.unmount();
      return text;
    });

    expect(labels[0]).toContain("All projects");
    expect(labels[1]).toContain("Unassigned");

    renderPicker({ kind: "project", projectId: 2 });
    expect(screen.getByTestId("scope-trigger")).toHaveTextContent("beta");
  });

  it("Given a parent project is selected, When the trigger renders, Then a +N badge says the scope is wider than one project", () => {
    renderPicker({ kind: "project", projectId: 1 });

    expect(screen.getByTestId("scope-trigger-badge")).toHaveTextContent("+2");
  });

  it("Given a leaf project is selected, When the trigger renders, Then there is no +N badge", () => {
    renderPicker({ kind: "project", projectId: 3 });

    expect(screen.queryByTestId("scope-trigger-badge")).toBeNull();
  });

  it("Given a nested project, When the trigger runs out of room, Then the parent path truncates and the project name is kept whole", () => {
    renderPicker({ kind: "project", projectId: 3 });

    const path = screen.getByTestId("scope-trigger-path");
    const name = screen.getByTestId("scope-trigger-name");

    expect(path).toHaveTextContent("alpha / beta");
    expect(path.className).toContain("truncate");
    expect(path.className).toContain("min-w-0");
    // 同名子项目从尾部截断会变成同一个样子，所以名字这一段不许被压缩。
    expect(name.className).toContain("shrink-0");
  });
});

describe("范围弹层", () => {
  async function open(scope: ProjectScope = { kind: "all" }, over = {}) {
    const user = userEvent.setup();
    const handles = renderPicker(scope, over);
    await user.click(screen.getByTestId("scope-trigger"));
    return { user, ...handles };
  }

  it("Given the popover is open, When it renders, Then 全部项目 / 未归属 sit outside the scrolling tree", async () => {
    await open();

    const pinned = screen.getByTestId("scope-pinned");
    const tree = screen.getByTestId("scope-tree");

    expect(within(pinned).getByTestId("scope-row-all")).toBeInTheDocument();
    expect(
      within(pinned).getByTestId("scope-row-unassigned"),
    ).toBeInTheDocument();
    expect(tree.contains(pinned)).toBe(false);
    expect(tree.className).toContain("overflow-y-auto");
  });

  it("Given no unassigned tasks, When the popover renders, Then 未归属 is not offered at all", async () => {
    await open({ kind: "all" }, { unassignedCount: 0 });

    expect(screen.queryByTestId("scope-row-unassigned")).toBeNull();
  });

  it("Given a deep project, When its row renders, Then depth indents it and a guide line says whom it hangs under", async () => {
    await open();

    const gamma = screen.getByTestId("scope-row-3");
    expect(gamma.getAttribute("data-depth")).toBe("2");
    expect(within(gamma).getAllByTestId("scope-guide")).toHaveLength(2);
  });

  it("Given a project row, When its count renders, Then it is the unfiltered subtree count and a search does not shrink it", async () => {
    const { user } = await open();

    expect(screen.getByTestId("scope-count-1")).toHaveTextContent("7");
    await user.type(screen.getByTestId("scope-search"), "gam");

    expect(screen.getByTestId("scope-count-1")).toHaveTextContent("7");
  });

  it("Given a search hit, When the tree renders, Then ancestors stay listed, stay clickable and the hit fragment is marked", async () => {
    const { user, onScopeChange } = await open();
    await user.type(screen.getByTestId("scope-search"), "gam");

    expect(screen.queryByTestId("scope-row-4")).toBeNull();
    const alpha = screen.getByTestId("scope-row-1");
    expect(alpha.getAttribute("data-ancestor-only")).toBe("true");
    expect(
      within(screen.getByTestId("scope-row-3")).getByText("gam"),
    ).toHaveAttribute("data-slot", "scope-match");

    await user.click(alpha);
    expect(onScopeChange).toHaveBeenCalledWith({
      kind: "project",
      projectId: 1,
    });
  });

  it("Given the keyboard moves through the tree, When a row is under the cursor, Then it is a different visual from the selected row", async () => {
    const { user } = await open({ kind: "project", projectId: 2 });

    const selected = screen.getByTestId("scope-row-2");
    expect(selected.getAttribute("data-selected")).toBe("true");
    expect(within(selected).getByTestId("scope-check")).toBeInTheDocument();

    await user.type(screen.getByTestId("scope-search"), "{ArrowDown}");
    const cursor = document.querySelector('[data-cursor="true"]');

    expect(cursor).not.toBeNull();
    expect(cursor).not.toBe(selected);
    expect(cursor?.className).toContain("bg-accent");
    // 已选中项的底色不是 accent —— 两个状态两个视觉。
    expect(selected.className).not.toContain("bg-accent");
  });

  it("Given the popover has just opened, When ArrowDown is pressed with nothing clicked, Then the cursor moves", async () => {
    const { user } = await open({ kind: "all" });

    // 搜索框拿到焦点 = 上下键有落点。不然「键盘移动当前行」要先用鼠标点进去，
    // 键盘用户根本够不着。
    expect(document.activeElement).toBe(screen.getByTestId("scope-search"));
    await user.keyboard("{ArrowDown}");

    expect(document.querySelector('[data-cursor="true"]')).not.toBeNull();
  });

  it("Given Enter on the cursor row, When the tree has focus, Then that scope is chosen", async () => {
    const { user, onScopeChange } = await open({ kind: "all" });

    await user.type(screen.getByTestId("scope-search"), "{ArrowDown}{Enter}");

    expect(onScopeChange).toHaveBeenCalledWith({
      kind: "project",
      projectId: 1,
    });
  });
});

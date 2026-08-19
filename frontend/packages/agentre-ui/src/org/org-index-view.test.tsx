import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { OrgAgentRow } from "./org-agent-row";
import { OrgGroupHeader } from "./org-group-header";
import { OrgInsertLine } from "./org-insert-line";
import { buildOrgIndex, EMPTY_ORG_FILTERS } from "./org-index-model";
import type { OrgAgentModel, OrgDepartmentModel } from "./types";

/**
 * 这一组用例扮演的是 **agentre-server**：没有 Wails、没有桌面端 store、没有 dnd-kit，
 * 只有普通对象与回调。桌面端那份「拖着走」的行为由宿主的 org-index 用例覆盖，
 * 这里钉的是「不给拖拽装配时，索引照样画得出来」这条跨仓前提。
 */

const departments: OrgDepartmentModel[] = [
  { id: 1, name: "Engineering", leadAgentName: "Eva", memberCount: 2 },
  { id: 2, name: "Platform", parentId: 1, memberCount: 0 },
];

const agents: OrgAgentModel[] = [
  { id: 2, name: "Eva", description: "Head of engineering", departmentId: 1 },
  { id: 3, name: "Bob", parentAgentId: 2, backend: { name: "Claude Code" } },
];

function model() {
  return buildOrgIndex({ agents, departments, filters: EMPTY_ORG_FILTERS });
}

describe("OrgAgentRow（只吃 props）", () => {
  it("画出名字、描述、后端徽标与行内的 ↳ 主管", () => {
    const rows = model().groups[0].rows;
    const subordinate = rows.find((row) => row.agent.id === 3)!;

    render(
      <OrgAgentRow
        row={subordinate}
        indent={0}
        selected={false}
        onSelect={vi.fn()}
      />,
    );

    expect(screen.getByTestId("org-row-3")).toBeInTheDocument();
    expect(screen.getByText("Bob")).toBeInTheDocument();
    expect(screen.getByText("Claude Code")).toBeInTheDocument();
    // 下属与主管平排，从属关系写在行内。
    expect(screen.getByText("↳ Eva")).toBeInTheDocument();
  });

  it("没给拖拽绑定就没有拖拽柄，但占位还在（左缘仍与邻居对齐）", () => {
    const row = model().groups[0].rows[0];

    render(
      <OrgAgentRow row={row} indent={0} selected={false} onSelect={vi.fn()} />,
    );

    expect(screen.queryByTestId(`org-row-handle-${row.agent.id}`)).toBeNull();
    // 只剩「选中」那一个按钮。
    expect(screen.getAllByRole("button")).toHaveLength(1);
  });

  it("给了拖拽绑定才长出柄，柄上的事件原样交给宿主", async () => {
    const user = userEvent.setup();
    const onKeyDown = vi.fn();
    const row = model().groups[0].rows[0];

    render(
      <OrgAgentRow
        row={row}
        indent={0}
        selected={false}
        onSelect={vi.fn()}
        dragHandle={{ onKeyDown }}
      />,
    );

    const handle = screen.getByTestId(`org-row-handle-${row.agent.id}`);
    handle.focus();
    await user.keyboard(" ");
    expect(onKeyDown).toHaveBeenCalled();
  });

  it("点行把选中交回宿主，选中态与落点态都只由 props 决定", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    const row = model().groups[0].rows[0];

    render(
      <OrgAgentRow
        row={row}
        indent={2}
        selected
        onSelect={onSelect}
        droppable
        dropState="invalid"
      />,
    );

    const node = screen.getByTestId(`org-row-${row.agent.id}`);
    expect(node).toHaveAttribute("aria-current", "true");
    expect(node).toHaveAttribute("data-drop-kind", "agent");
    expect(node).toHaveAttribute("data-drop-state", "invalid");
    expect(node).toHaveAttribute("data-indent", "2");

    await user.click(screen.getByTestId(`org-row-select-${row.agent.id}`));
    expect(onSelect).toHaveBeenCalledWith({ kind: "agent", id: row.agent.id });
  });
});

describe("OrgGroupHeader（只吃 props）", () => {
  it("组头带负责人与成员数，子部门缩在父部门里", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    const groups = model().groups;

    render(
      <>
        <OrgGroupHeader
          group={groups[0]}
          selected={false}
          onSelect={onSelect}
        />
        <OrgGroupHeader
          group={groups[1]}
          selected={false}
          onSelect={onSelect}
        />
      </>,
    );

    expect(screen.getByText("Lead · Eva")).toBeInTheDocument();
    expect(screen.getByText("2 members")).toBeInTheDocument();
    // 空部门照常摆组头（决策 13）。
    expect(screen.getByTestId("org-group-2")).toHaveAttribute(
      "data-depth",
      "1",
    );

    await user.click(screen.getByTestId("org-group-select-2"));
    expect(onSelect).toHaveBeenCalledWith({ kind: "department", id: 2 });
  });
});

describe("OrgInsertLine（只吃 props）", () => {
  it("非法落点留在原地画成拒绝态，而不是让高亮消失", () => {
    render(<OrgInsertLine id="insert-1-0-0" dropState="invalid" />);

    const line = screen.getByTestId("insert-1-0-0");
    expect(line).toHaveAttribute("data-drop-kind", "reorder");
    expect(line).toHaveAttribute("data-drop-state", "invalid");
    expect(line.className).toContain("bg-destructive");
  });
});
